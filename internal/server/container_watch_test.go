package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

// 记录的更新规则（修订第 166 条）：只增不减；少于记录时记下开始缺少的时间；截断时不判断缺少。
func TestUpdateExpect(t *testing.T) {
	m := map[string]*containerExpect{}
	if !updateExpect(m, map[string]int{"nginx": 1, "node": 3}, false, 100) || m["node"].Count != 3 || m["nginx"].MissingSince != 0 {
		t.Fatalf("首次应记下数量：%+v", m)
	}
	// node 少了一个：开始缺少；再次上报不重复记时间
	updateExpect(m, map[string]int{"nginx": 1, "node": 2}, false, 200)
	if m["node"].MissingSince != 200 || m["node"].Count != 3 {
		t.Fatalf("应记下开始缺少的时间：%+v", m["node"])
	}
	if updateExpect(m, map[string]int{"nginx": 1, "node": 2}, false, 300) || m["node"].MissingSince != 200 {
		t.Fatalf("仍在缺少时不应变化：%+v", m["node"])
	}
	if got := missingOf(m, map[string]int{"nginx": 1, "node": 2}); len(got) != 1 || got[0].Label != "node" || got[0].Missing != 1 {
		t.Fatalf("缺少列表不对：%+v", got)
	}
	// 回来：清除
	updateExpect(m, map[string]int{"nginx": 1, "node": 3}, false, 400)
	if m["node"].MissingSince != 0 {
		t.Fatalf("回来后应清除：%+v", m["node"])
	}
	// 列表被截断（满 50 个）：不判断缺少，但仍会增加
	updateExpect(m, map[string]int{"node": 4}, true, 500)
	if m["nginx"].MissingSince != 0 || m["node"].Count != 4 {
		t.Fatalf("截断时不应判断缺少：%+v %+v", m["nginx"], m["node"])
	}
	// 全部停止（上报中没有容器）
	updateExpect(m, map[string]int{}, false, 600)
	if got := missingOf(m, nil); len(got) != 2 {
		t.Fatalf("全部停止时两个都缺少：%+v", got)
	}
}

// 端到端：上报 → 告警（持续 2 分钟）→ 不再提醒 → 恢复；按进程名统计，重建（ID 变化）不误报；面板重启后记录仍在。
func TestContainerStoppedAlert(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	id, _, _ := s.store.CreateServer("docker-host", 0, 1)
	t0 := time.Now().Add(time.Hour)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	report := func(sec int, cs ...protocol.Container) {
		t.Helper()
		rep := protocol.Report{System: protocol.System{BootID: "b"}, Containers: cs}
		s.mu.Lock()
		s.latest[id] = &snapshot{ReceivedAt: at(sec), At: at(sec), Report: rep}
		s.mu.Unlock()
		s.store.DB.Exec(`UPDATE servers SET last_seen_at = ?, enroll_state = 'enrolled' WHERE id = ?`, at(sec).Unix(), id)
		s.trackContainers(id, rep, at(sec))
		if err := s.evaluateAlerts(at(sec)); err != nil {
			t.Fatal(err)
		}
	}
	firing := func() []AlertEvent {
		items, _, _ := s.store.ListAlertEvents(AlertQuery{State: StateFiring, Limit: 10})
		var out []AlertEvent
		for _, e := range items {
			if e.Type == AlertContainer {
				out = append(out, e)
			}
		}
		return out
	}
	nginx := protocol.Container{ID: "aaaaaaaaaaaa", Runtime: "docker", Command: "nginx"}
	pg := protocol.Container{ID: "bbbbbbbbbbbb", Runtime: "docker", Command: "postgres"}
	noCmd := protocol.Container{ID: "cccccccccccc", Runtime: "docker"} // 读不到进程名：不统计

	report(0, nginx, pg, noCmd)
	// 重建：ID 变了、进程名相同，不误报
	report(30, protocol.Container{ID: "dddddddddddd", Runtime: "docker", Command: "nginx"}, pg)
	report(200, protocol.Container{ID: "dddddddddddd", Runtime: "docker", Command: "nginx"}, pg)
	if f := firing(); len(f) != 0 {
		t.Fatalf("重建与没有进程名的容器不应告警：%+v", f)
	}
	// postgres 停止：2 分钟后告警
	report(260, nginx)
	report(300, nginx)
	if f := firing(); len(f) != 0 {
		t.Fatal("不满 2 分钟不应告警")
	}
	report(390, nginx)
	f := firing()
	if len(f) != 1 || !strings.Contains(f[0].Message, "容器已停止：postgres") {
		t.Fatalf("应告警 postgres：%+v", f)
	}
	// 节点详情列出已停止的容器
	var v struct {
		Missing []containerMissing `json:"containers_missing"`
	}
	json.Unmarshal(do(h, "GET", "/api/v1/servers/"+itoa(id), admin, nil).Body.Bytes(), &v)
	if len(v.Missing) != 1 || v.Missing[0].Label != "postgres" || v.Missing[0].Since != at(260).Unix() {
		t.Fatalf("详情中的已停止容器不对：%+v", v.Missing)
	}

	// 面板重启：内存缓存清空，记录从数据库读回，告警不会被误判为恢复
	s.cwatch = newContainerWatch()
	report(400, nginx)
	if f := firing(); len(f) != 1 {
		t.Fatalf("面板重启后告警应保持：%+v", f)
	}

	// 不再提醒：以当前为准，告警恢复
	if rec := do(h, "POST", "/api/v1/servers/"+itoa(id)+"/containers/forget", admin, []byte(`{"label":"postgres"}`)); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body)
	}
	report(410, nginx)
	if f := firing(); len(f) != 0 {
		t.Fatalf("不再提醒后告警应恢复：%+v", f)
	}
	if m, _ := s.store.LoadContainerExpect(id); m["postgres"] != nil || m["nginx"] == nil {
		t.Fatalf("记录不对：%+v", m)
	}
	// 全部停止（上报中没有 containers 字段）
	report(420)
	report(560)
	if f := firing(); len(f) != 1 || !strings.Contains(f[0].Message, "nginx") {
		t.Fatalf("全部停止应告警：%+v", f)
	}
	// 不带 label：全部忘记
	if rec := do(h, "POST", "/api/v1/servers/"+itoa(id)+"/containers/forget", admin, nil); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body)
	}
	if m, _ := s.store.LoadContainerExpect(id); len(m) != 0 {
		t.Fatalf("全部忘记后应没有记录：%+v", m)
	}
}

// 从未上报过容器的节点（旧版 Agent）不评估、不写记录。
func TestContainerWatchIgnoresOldAgents(t *testing.T) {
	s, _, _ := testServer(t)
	id, _, _ := s.store.CreateServer("old", 0, 1)
	s.trackContainers(id, protocol.Report{}, time.Now())
	if _, ok := s.containerAlertInput(id); ok {
		t.Fatal("从未上报过容器时不应评估")
	}
	var n int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM container_expect`).Scan(&n)
	if n != 0 {
		t.Fatal("不应写入记录")
	}
}
