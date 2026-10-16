package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"vpsmon/internal/release"
)

// 远程升级任务：创建 → Agent 获取（得到官方签名清单原文）→ 上报结果（设计 29.13、29.14）。
func TestUpgradeTasks(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	f := newFakeReleases(t)
	s.releaseBase, s.releaseKeys = f.srv.URL, []release.PublicKey{f.key}
	manifest := f.publish("0.4.0", "stable")
	if rec := do(h, "POST", "/api/v1/agent-releases/sync", admin, nil); rec.Code != 200 {
		t.Fatalf("同步：%d %s", rec.Code, rec.Body)
	}

	_, v, _ := createNode(t, h, admin, `{"name":"hk-1"}`)
	_, pending, _ := createNode(t, h, admin, `{"name":"hk-2"}`)
	_, res := enroll(h, v.EnrollCode, "hk-1", "m1")
	do(h, "POST", "/api/v1/agent/report", res.AgentToken, []byte(`{"agent_version":"0.3.0","system":{"boot_id":"b"}}`))
	agent := res.AgentToken

	// 没有任务
	if rec := do(h, "GET", "/api/v1/agent/upgrade", agent, nil); !strings.Contains(rec.Body.String(), `"upgrade":false`) {
		t.Fatalf("没有任务时：%s", rec.Body)
	}

	// 只能选择已同步并验签的版本
	body := func(ids string, ver string) []byte {
		return []byte(`{"server_ids":[` + ids + `],"version":"` + ver + `"}`)
	}
	if rec := do(h, "POST", "/api/v1/upgrade-tasks", admin, body(itoa(v.ServerID), "9.9.9")); rec.Code != 422 {
		t.Errorf("未同步的版本应拒绝：%d %s", rec.Code, rec.Body)
	}
	rec := do(h, "POST", "/api/v1/upgrade-tasks", admin, body(itoa(v.ServerID)+","+itoa(pending.ServerID)+",999", "v0.4.0"))
	var cr struct {
		Created []UpgradeTask
		Skipped []struct {
			ServerID int64 `json:"server_id"`
			Reason   string
		}
	}
	json.Unmarshal(rec.Body.Bytes(), &cr)
	if rec.Code != 201 || len(cr.Created) != 1 || cr.Created[0].FromVersion != "0.3.0" || len(cr.Skipped) != 2 {
		t.Fatalf("创建任务：%d %s", rec.Code, rec.Body)
	}
	tid := cr.Created[0].ID
	// 已有进行中的任务时跳过
	json.Unmarshal(do(h, "POST", "/api/v1/upgrade-tasks", admin, body(itoa(v.ServerID), "0.4.0")).Body.Bytes(), &cr)
	if len(cr.Created) != 0 || len(cr.Skipped) != 1 {
		t.Errorf("重复创建应跳过：%+v", cr)
	}

	// Agent 获取任务：清单原文与签名，可用官方公钥独立验签
	var task struct {
		Upgrade   bool
		TaskID    int64 `json:"task_id"`
		Version   string
		Manifest  []byte
		Signature string `json:"manifest_signature"`
	}
	json.Unmarshal(do(h, "GET", "/api/v1/agent/upgrade", agent, nil).Body.Bytes(), &task)
	if !task.Upgrade || task.TaskID != tid || task.Version != "0.4.0" || string(task.Manifest) != string(manifest) {
		t.Fatalf("Agent 获取任务：%+v", task)
	}
	if _, err := release.VerifyManifest([]release.PublicKey{f.key}, task.Manifest, []byte(task.Signature)); err != nil {
		t.Errorf("下发的清单应能独立验签：%v", err)
	}
	status := func(token string, id int64, st string) int {
		b, _ := json.Marshal(map[string]any{"task_id": id, "status": st, "reason": "r"})
		return do(h, "POST", "/api/v1/agent/upgrade/status", token, b).Code
	}
	// 已交给 updater 后不能取消；其他节点的 Token 不能更新本任务
	if c := status(agent, tid, "staged"); c != 204 {
		t.Fatalf("staged：%d", c)
	}
	if rec := do(h, "POST", "/api/v1/upgrade-tasks/"+itoa(tid)+"/cancel", admin, nil); rec.Code != 409 {
		t.Errorf("staged 后不能取消：%d", rec.Code)
	}
	_, other, _ := createNode(t, h, admin, `{"name":"hk-3"}`)
	_, ores := enroll(h, other.EnrollCode, "hk-3", "m3")
	if c := status(ores.AgentToken, tid, "success"); c != 404 {
		t.Errorf("【安全】其他节点的 Token 不能更新本任务：%d", c)
	}
	if c := status(agent, tid, "cancelled"); c != 422 {
		t.Errorf("Agent 只能上报 staged/success/failed/rolled_back：%d", c)
	}
	if c := status(agent, tid, "success"); c != 204 {
		t.Fatalf("success：%d", c)
	}
	if c := status(agent, tid, "failed"); c != 404 {
		t.Errorf("已结束的任务不能再更新：%d", c)
	}
	var list struct{ Items []UpgradeTask }
	json.Unmarshal(do(h, "GET", "/api/v1/upgrade-tasks?server_id="+itoa(v.ServerID), admin, nil).Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].Status != UpgradeSuccess {
		t.Errorf("任务列表：%+v", list.Items)
	}

	// 取消与超时
	do(h, "POST", "/api/v1/agent/report", agent, []byte(`{"agent_version":"0.3.5","system":{"boot_id":"b"}}`))
	json.Unmarshal(do(h, "POST", "/api/v1/upgrade-tasks", admin, body(itoa(v.ServerID), "0.4.0")).Body.Bytes(), &cr)
	if len(cr.Created) != 1 {
		t.Fatalf("应可再次创建：%+v", cr)
	}
	if rec := do(h, "POST", "/api/v1/upgrade-tasks/"+itoa(cr.Created[0].ID)+"/cancel", admin, nil); rec.Code != 204 {
		t.Errorf("取消：%d %s", rec.Code, rec.Body)
	}
	json.Unmarshal(do(h, "POST", "/api/v1/upgrade-tasks", admin, body(itoa(v.ServerID), "0.4.0")).Body.Bytes(), &cr)
	if _, err := s.store.ExpireUpgradeTasks(time.Now().Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(do(h, "GET", "/api/v1/upgrade-tasks?server_id="+itoa(v.ServerID), admin, nil).Body.Bytes(), &list)
	if list.Items[0].Status != UpgradeFailed || list.Items[0].Reason == "" {
		t.Errorf("超时的任务应判为失败：%+v", list.Items[0])
	}
}

// 任务创建后节点已在本机升级到目标版本：任务记为成功，而不是“升级失败（已是 x，无需升级）”
func TestUpgradeAlreadyCurrent(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	f := newFakeReleases(t)
	s.releaseBase, s.releaseKeys = f.srv.URL, []release.PublicKey{f.key}
	f.publish("0.5.0", "stable")
	do(h, "POST", "/api/v1/agent-releases/sync", admin, nil)
	_, v, _ := createNode(t, h, admin, `{"name":"jp-1"}`)
	_, res := enroll(h, v.EnrollCode, "jp-1", "m1")
	agent := res.AgentToken
	report := func(ver string) {
		do(h, "POST", "/api/v1/agent/report", agent, []byte(`{"agent_version":"`+ver+`","system":{"boot_id":"b"}}`))
	}
	create := func() int64 {
		var cr struct{ Created []UpgradeTask }
		json.Unmarshal(do(h, "POST", "/api/v1/upgrade-tasks", admin, []byte(`{"server_ids":[`+itoa(v.ServerID)+`],"version":"0.5.0"}`)).Body.Bytes(), &cr)
		if len(cr.Created) != 1 {
			t.Fatalf("创建任务失败")
		}
		return cr.Created[0].ID
	}
	last := func() UpgradeTask {
		var list struct{ Items []UpgradeTask }
		json.Unmarshal(do(h, "GET", "/api/v1/upgrade-tasks?server_id="+itoa(v.ServerID), admin, nil).Body.Bytes(), &list)
		return list.Items[0]
	}

	// 1. 新版 Agent 查询时带 current_version：面板直接完成任务，不下发
	report("0.4.1")
	create()
	if rec := do(h, "GET", "/api/v1/agent/upgrade?current_version=0.5.0", agent, nil); !strings.Contains(rec.Body.String(), `"upgrade":false`) {
		t.Errorf("已是目标版本时不应下发：%s", rec.Body)
	}
	if tk := last(); tk.Status != UpgradeSuccess || tk.Reason != alreadyAtReason {
		t.Errorf("应记为成功：%+v", tk)
	}

	// 2. 旧版 Agent 报告 failed（已是 0.5.0，无需升级），面板看到的版本已是 0.5.0：改记为成功
	report("0.4.1")
	id := create()
	do(h, "GET", "/api/v1/agent/upgrade?current_version=0.4.1", agent, nil)
	report("0.5.0")
	b, _ := json.Marshal(map[string]any{"task_id": id, "status": "failed", "reason": "已是 0.5.0，无需升级"})
	if rec := do(h, "POST", "/api/v1/agent/upgrade/status", agent, b); rec.Code != 204 {
		t.Fatalf("上报：%d", rec.Code)
	}
	if tk := last(); tk.Status != UpgradeSuccess {
		t.Errorf("应改记为成功：%+v", tk)
	}

	// 3. 真正的失败（版本仍低于目标）照常记为失败
	report("0.4.1")
	id = create()
	do(h, "GET", "/api/v1/agent/upgrade?current_version=0.4.1", agent, nil)
	b, _ = json.Marshal(map[string]any{"task_id": id, "status": "failed", "reason": "下载失败"})
	do(h, "POST", "/api/v1/agent/upgrade/status", agent, b)
	if tk := last(); tk.Status != UpgradeFailed {
		t.Errorf("真正的失败应保留：%+v", tk)
	}
	if alreadyAt("0.5.0", "0.5.0") != true || alreadyAt("0.6.0", "0.5.0") != true || alreadyAt("0.4.1", "0.5.0") || alreadyAt("", "0.5.0") {
		t.Error("alreadyAt")
	}
}

// 不能远程升级的节点在创建任务时直接说明原因，而不是让任务一直“等待获取”（设计 29.13）
func TestUpgradeBlockers(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	f := newFakeReleases(t)
	s.releaseBase, s.releaseKeys = f.srv.URL, []release.PublicKey{f.key}
	f.publish("0.4.0", "stable")
	if rec := do(h, "POST", "/api/v1/agent-releases/sync", admin, nil); rec.Code != 200 {
		t.Fatalf("同步：%d %s", rec.Code, rec.Body)
	}
	node := func(name, report string) int64 {
		_, v, _ := createNode(t, h, admin, `{"name":"`+name+`"}`)
		_, res := enroll(h, v.EnrollCode, name, "m-"+name)
		do(h, "POST", "/api/v1/agent/report", res.AgentToken, []byte(report))
		return int64(v.ServerID)
	}
	old := node("old", `{"agent_version":"0.2.0","system":{"boot_id":"b"}}`)
	off := node("off", `{"agent_version":"0.3.0","system":{"boot_id":"b","remote_upgrade":false}}`)
	on := node("on", `{"agent_version":"0.3.0","system":{"boot_id":"b","remote_upgrade":true}}`)
	unknown := node("unknown", `{"agent_version":"0.3.0","system":{"boot_id":"b"}}`)

	rec := do(h, "POST", "/api/v1/upgrade-tasks", admin, []byte(`{"server_ids":[`+itoa(old)+`,`+itoa(off)+`,`+itoa(on)+`,`+itoa(unknown)+`],"version":"0.4.0"}`))
	var cr struct {
		Created []UpgradeTask
		Skipped []struct {
			ServerID int64 `json:"server_id"`
			Reason   string
		}
	}
	json.Unmarshal(rec.Body.Bytes(), &cr)
	if rec.Code != 201 || len(cr.Created) != 2 || len(cr.Skipped) != 2 {
		t.Fatalf("创建：%d %s", rec.Code, rec.Body)
	}
	why := map[int64]string{}
	for _, sk := range cr.Skipped {
		why[sk.ServerID] = sk.Reason
	}
	if !strings.Contains(why[old], "不支持远程升级") || !strings.Contains(why[old], "sudo vpsmon-agent upgrade") {
		t.Errorf("旧版 Agent：%q", why[old])
	}
	if !strings.Contains(why[off], "enable-remote-upgrade") {
		t.Errorf("未启用远程升级：%q", why[off])
	}

	// 15 分钟没有被领取：判为失败并说明原因（不用等满 1 小时）
	if n, err := s.store.ExpireUpgradeTasks(time.Now().Add(10 * time.Minute)); err != nil || len(n) != 0 {
		t.Fatalf("10 分钟时不应过期：%d %v", len(n), err)
	}
	if n, err := s.store.ExpireUpgradeTasks(time.Now().Add(16 * time.Minute)); err != nil || len(n) != 2 {
		t.Fatalf("16 分钟时应过期 2 个：%d %v", len(n), err)
	}
	var list struct{ Items []UpgradeTask }
	json.Unmarshal(do(h, "GET", "/api/v1/upgrade-tasks?server_id="+itoa(unknown), admin, nil).Body.Bytes(), &list)
	if list.Items[0].Status != UpgradeFailed || !strings.Contains(list.Items[0].Reason, "没有领取") {
		t.Errorf("未领取的任务：%+v", list.Items[0])
	}
}
