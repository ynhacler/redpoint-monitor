package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"vpsmon/internal/release"
)

// seenAt 把节点最后一次上报的时间设为 at（测试用模拟时间推进时让节点保持在线或变为离线）。
func seenAt(s *Server, id int64, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sn := s.latest[id]; sn != nil {
		sn.ReceivedAt = at
	}
}

func TestStageTargets(t *testing.T) {
	cases := []struct {
		stages []RolloutStage
		total  int
		want   []int
	}{
		{[]RolloutStage{{Count: 2}, {Percent: 20}, {Percent: 100}}, 50, []int{2, 10, 50}},
		{[]RolloutStage{{Count: 2}, {Percent: 20}, {Count: 1}}, 5, []int{2, 2, 5}},      // 不递减；最后一批总是全部
		{[]RolloutStage{{Count: 10}, {Percent: 50}}, 4, []int{4, 4}},                    // 不超过总数
		{[]RolloutStage{{Percent: 1}, {Percent: 30}, {Percent: 60}}, 7, []int{1, 3, 7}}, // 百分比向上取整
		{[]RolloutStage{{Count: 1}, {Count: 3}, {Percent: 100}}, 3, []int{1, 3, 3}},
	}
	for _, c := range cases {
		got := stageTargets(c.stages, c.total)
		if len(got) != len(c.want) {
			t.Fatalf("%v", got)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("stageTargets(%+v, %d) = %v，应为 %v", c.stages, c.total, got, c.want)
				break
			}
		}
	}
}

func TestDecideRollout(t *testing.T) {
	now := time.Unix(10_000, 0)
	obs := 30 * time.Minute
	cases := []struct {
		name               string
		stage, n, max, ack int
		doneAt             int64
		st                 stageStat
		offline            []string
		want               string
		wantDone           int64
	}{
		{"还有进行中的任务：等待", 1, 3, 0, 0, 0, stageStat{Tasks: 2, Success: 1, Active: 1}, nil, rolloutWait, 0},
		{"刚结束：记录时间并开始观察", 1, 3, 0, 0, 0, stageStat{Tasks: 2, Success: 2}, nil, rolloutWait, 10_000},
		{"观察中", 1, 3, 0, 0, 9_000, stageStat{Tasks: 2, Success: 2}, nil, rolloutWait, 9_000},
		{"观察期满：下一批", 1, 3, 0, 0, 10_000 - 1800, stageStat{Tasks: 2, Success: 2}, nil, rolloutAdvance, 10_000 - 1800},
		{"失败超过允许数：暂停", 1, 3, 0, 0, 0, stageStat{Tasks: 2, Success: 1, Failed: 1}, nil, rolloutPause, 10_000},
		{"失败在允许范围内", 1, 3, 1, 0, 10_000 - 1800, stageStat{Tasks: 2, Success: 1, Failed: 1}, nil, rolloutAdvance, 10_000 - 1800},
		{"继续后已确认的失败不再暂停", 1, 3, 0, 1, 10_000 - 1800, stageStat{Tasks: 2, Success: 1, Failed: 1}, nil, rolloutAdvance, 10_000 - 1800},
		{"已升级的节点离线：暂停", 2, 3, 0, 0, 9_000, stageStat{Tasks: 3, Success: 3}, []string{"hk-1"}, rolloutPause, 9_000},
		{"最后一批结束：完成（不观察）", 3, 3, 0, 0, 0, stageStat{Tasks: 5, Success: 5}, nil, rolloutComplete, 10_000},
		{"本批全部被跳过：不观察，直接下一批", 1, 3, 0, 0, 0, stageStat{}, nil, rolloutAdvance, 10_000},
	}
	for _, c := range cases {
		got, reason, done := decideRollout(c.stage, c.n, c.max, c.ack, obs, c.doneAt, c.st, c.offline, now)
		if got != c.want || done != c.wantDone {
			t.Errorf("%s：得到 %s（%s）doneAt=%d，应为 %s doneAt=%d", c.name, got, reason, done, c.want, c.wantDone)
		}
		if got == rolloutPause && reason == "" {
			t.Errorf("%s：暂停应说明原因", c.name)
		}
	}
}

// 灰度升级：第一批按台数创建任务 → 失败自动暂停 → 继续 → 观察期满后下一批 → 完成（设计 29.16）。
func TestRolloutFlow(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	f := newFakeReleases(t)
	s.releaseBase, s.releaseKeys = f.srv.URL, []release.PublicKey{f.key}
	f.publish("0.4.0", "stable")
	if rec := do(h, "POST", "/api/v1/agent-releases/sync", admin, nil); rec.Code != 200 {
		t.Fatalf("同步：%d %s", rec.Code, rec.Body)
	}
	type node struct {
		id    int64
		agent string
	}
	var nodes []node
	for i, name := range []string{"n1", "n2", "n3", "n4"} {
		_, v, _ := createNode(t, h, admin, `{"name":"`+name+`"}`)
		_, res := enroll(h, v.EnrollCode, name, "m"+itoa(int64(i)))
		do(h, "POST", "/api/v1/agent/report", res.AgentToken, []byte(`{"agent_version":"0.3.0","system":{"boot_id":"b","remote_upgrade":true}}`))
		nodes = append(nodes, node{v.ServerID, res.AgentToken})
	}
	_, pending, _ := createNode(t, h, admin, `{"name":"pending"}`)
	ids := itoa(pending.ServerID)
	for _, n := range nodes {
		ids += "," + itoa(n.id)
	}

	// 参数校验
	for _, bad := range []string{
		`{"server_ids":[` + itoa(nodes[0].id) + `],"version":"0.4.0","stages":[{"count":1},{"percent":100}]}`,
		`{"server_ids":[` + ids + `],"version":"0.4.0","stages":[{"count":1}]}`,
		`{"server_ids":[` + ids + `],"version":"0.4.0","stages":[{"count":1,"percent":5},{"percent":100}]}`,
		`{"server_ids":[` + ids + `],"version":"0.4.0","stages":[{"count":1},{"percent":100}],"observe_minutes":1}`,
	} {
		if rec := do(h, "POST", "/api/v1/upgrade-rollouts", admin, []byte(bad)); rec.Code != 422 {
			t.Errorf("应拒绝 %s：%d %s", bad, rec.Code, rec.Body)
		}
	}

	rec := do(h, "POST", "/api/v1/upgrade-rollouts", admin,
		[]byte(`{"server_ids":[`+ids+`],"version":"0.4.0","stages":[{"count":1},{"percent":60},{"percent":100}],"observe_minutes":5}`))
	var ro UpgradeRollout
	json.Unmarshal(rec.Body.Bytes(), &ro)
	// 待安装的节点被跳过，由后面的节点补足：第一批 1 台
	if rec.Code != 201 || ro.Status != RolloutRunning || ro.Progress[0].Tasks != 1 || len(ro.Skipped) != 1 || ro.Skipped[0].ServerID != pending.ServerID {
		t.Fatalf("创建：%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/v1/upgrade-rollouts", admin,
		[]byte(`{"server_ids":[`+ids+`],"version":"0.4.0","stages":[{"count":1},{"percent":100}]}`)); rec.Code != 409 {
		t.Errorf("同时只能有一个进行中的灰度升级：%d", rec.Code)
	}

	report := func(n node, st string) {
		var task struct {
			TaskID int64 `json:"task_id"`
		}
		json.Unmarshal(do(h, "GET", "/api/v1/agent/upgrade", n.agent, nil).Body.Bytes(), &task)
		b, _ := json.Marshal(map[string]any{"task_id": task.TaskID, "status": st})
		if rec := do(h, "POST", "/api/v1/agent/upgrade/status", n.agent, b); rec.Code != 204 {
			t.Fatalf("上报 %s：%d %s", st, rec.Code, rec.Body)
		}
	}
	get := func() UpgradeRollout {
		var list struct{ Items []UpgradeRollout }
		json.Unmarshal(do(h, "GET", "/api/v1/upgrade-rollouts", admin, nil).Body.Bytes(), &list)
		return list.Items[0]
	}

	// 第一批失败：自动暂停
	report(nodes[0], UpgradeRolledBack)
	now := time.Now()
	s.checkRollouts(now)
	if r := get(); r.Status != RolloutPaused || !strings.Contains(r.Reason, "第 1 批") {
		t.Fatalf("失败应自动暂停：%+v", r)
	}
	// 暂停期间不推进
	s.checkRollouts(now.Add(time.Hour))
	if r := get(); r.CurrentStage != 1 {
		t.Fatalf("暂停时不应推进：%+v", r)
	}

	// 继续：确认已有的失败，观察期重新开始，期满后开始第二批（累计 60% = 3 台，即再 2 台）
	if rec := do(h, "POST", "/api/v1/upgrade-rollouts/"+itoa(ro.ID)+"/resume", admin, nil); rec.Code != 204 {
		t.Fatalf("继续：%d %s", rec.Code, rec.Body)
	}
	s.checkRollouts(now)
	if r := get(); r.Status != RolloutRunning || r.CurrentStage != 1 || r.StageDoneAt == 0 {
		t.Fatalf("继续后应开始观察：%+v", r)
	}
	s.checkRollouts(now.Add(6 * time.Minute))
	r := get()
	if r.CurrentStage != 2 || r.Progress[1].Tasks != 2 || r.Progress[1].Target != 2 {
		t.Fatalf("观察期满应开始第二批：%+v", r)
	}

	// 第二批成功，观察期满后最后一批，最后一批成功后完成
	report(nodes[1], UpgradeSuccess)
	report(nodes[2], UpgradeSuccess)
	for _, n := range nodes[1:3] {
		do(h, "POST", "/api/v1/agent/report", n.agent, []byte(`{"agent_version":"0.4.0","system":{"boot_id":"b","remote_upgrade":true}}`))
	}
	for _, n := range nodes[1:3] {
		seenAt(s, n.id, now.Add(13*time.Minute))
	}
	s.checkRollouts(now.Add(7 * time.Minute))
	s.checkRollouts(now.Add(13 * time.Minute))
	if r := get(); r.CurrentStage != 3 || r.Progress[2].Tasks != 1 {
		t.Fatalf("应开始最后一批：%+v", r)
	}
	report(nodes[3], UpgradeSuccess)
	do(h, "POST", "/api/v1/agent/report", nodes[3].agent, []byte(`{"agent_version":"0.4.0","system":{"boot_id":"b","remote_upgrade":true}}`))
	for _, n := range nodes[1:] {
		seenAt(s, n.id, now.Add(14*time.Minute))
	}
	s.checkRollouts(now.Add(14 * time.Minute))
	if r := get(); r.Status != RolloutCompleted {
		t.Fatalf("应完成：%+v", r)
	}
	if rec := do(h, "POST", "/api/v1/upgrade-rollouts/"+itoa(ro.ID)+"/cancel", admin, nil); rec.Code != 409 {
		t.Errorf("已完成的不能取消：%d", rec.Code)
	}
}

// 已升级的节点离线时自动暂停；取消时撤回未领取的任务。
func TestRolloutOfflinePauseAndCancel(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	f := newFakeReleases(t)
	s.releaseBase, s.releaseKeys = f.srv.URL, []release.PublicKey{f.key}
	f.publish("0.4.0", "stable")
	do(h, "POST", "/api/v1/agent-releases/sync", admin, nil)
	var agents []string
	var sids []int64
	ids := ""
	for i, name := range []string{"a", "b", "c"} {
		_, v, _ := createNode(t, h, admin, `{"name":"`+name+`"}`)
		_, res := enroll(h, v.EnrollCode, name, "m"+itoa(int64(i)))
		do(h, "POST", "/api/v1/agent/report", res.AgentToken, []byte(`{"agent_version":"0.3.0","system":{"boot_id":"b","remote_upgrade":true}}`))
		agents = append(agents, res.AgentToken)
		sids = append(sids, v.ServerID)
		if ids != "" {
			ids += ","
		}
		ids += itoa(v.ServerID)
	}
	var ro UpgradeRollout
	json.Unmarshal(do(h, "POST", "/api/v1/upgrade-rollouts", admin,
		[]byte(`{"server_ids":[`+ids+`],"version":"0.4.0","stages":[{"count":1},{"percent":100}],"observe_minutes":5}`)).Body.Bytes(), &ro)
	var task struct {
		TaskID int64 `json:"task_id"`
	}
	json.Unmarshal(do(h, "GET", "/api/v1/agent/upgrade", agents[0], nil).Body.Bytes(), &task)
	b, _ := json.Marshal(map[string]any{"task_id": task.TaskID, "status": UpgradeSuccess})
	do(h, "POST", "/api/v1/agent/upgrade/status", agents[0], b)

	// 升级成功后很久没有上报：离线 → 暂停
	seenAt(s, sids[0], time.Now().Add(-time.Hour))
	s.checkRollouts(time.Now())
	got, _ := s.store.GetRollout(ro.ID)
	if got.Status != RolloutPaused || !strings.Contains(got.Reason, "离线") {
		t.Fatalf("已升级的节点离线应暂停：%+v", got)
	}
	// 继续（确认离线节点）后开始下一批，再取消：未领取的任务被撤回
	do(h, "POST", "/api/v1/upgrade-rollouts/"+itoa(ro.ID)+"/resume", admin, nil)
	for _, id := range sids[1:] {
		seenAt(s, id, time.Now().Add(2*time.Hour))
	}
	s.checkRollouts(time.Now().Add(time.Hour))
	s.checkRollouts(time.Now().Add(2 * time.Hour))
	if got, _ := s.store.GetRollout(ro.ID); got.CurrentStage != 2 {
		t.Fatalf("继续后应开始下一批：%+v", got)
	}
	if rec := do(h, "POST", "/api/v1/upgrade-rollouts/"+itoa(ro.ID)+"/cancel", admin, nil); rec.Code != 204 {
		t.Fatalf("取消：%d %s", rec.Code, rec.Body)
	}
	tasks, _ := s.store.rolloutTasks(ro.ID)
	for _, tk := range tasks[1:] {
		if tk.Status != UpgradeCancelled {
			t.Errorf("取消后未领取的任务应撤回：%+v", tk)
		}
	}
}
