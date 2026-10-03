package server

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

func TestAlertEngine(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, v, _ := createNode(t, h, admin, `{"name":"hk-1"}`)
	if rec, _ := enroll(h, v.EnrollCode, "hk-1", "m-1"); rec.Code != 200 {
		t.Fatalf("注册失败：%d %s", rec.Code, rec.Body)
	}
	id := v.ServerID
	createNode(t, h, admin, `{"name":"待安装"}`) // 待安装节点不应产生任何告警

	t0 := time.Now().Add(time.Hour) // 远离面板启动时间，越过启动宽限期
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	report := func(sec int, cpu float64, disk float64) {
		s.mu.Lock()
		s.latest[id] = &snapshot{ReceivedAt: at(sec), At: at(sec), Report: protocol.Report{
			CPU:    protocol.CPU{Usage: cpu, Cores: 2, Load1: 0.1},
			Memory: protocol.Memory{Usage: 30},
			Disk:   []protocol.Disk{{Mount: "/", Usage: 40}, {Mount: "/data", Usage: disk}},
		}}
		s.mu.Unlock()
	}
	eval := func(sec int) {
		t.Helper()
		if err := s.evaluateAlerts(at(sec)); err != nil {
			t.Fatal(err)
		}
	}
	events := func(state string) []AlertEvent {
		t.Helper()
		items, _, err := s.store.ListAlertEvents(AlertQuery{State: state, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		return items
	}
	firing := func() []alertBrief { return s.alerts.firingFor(id) }

	// CPU 95% 持续 5 分钟才触发
	report(0, 95, 40)
	eval(0)
	if len(events("")) != 0 || len(firing()) != 0 {
		t.Fatal("pending 期间不应写入事件")
	}
	report(300, 95, 40)
	eval(300)
	ev := events(StateFiring)
	if len(ev) != 1 || ev[0].RuleKey != "cpu" || ev[0].Severity != SeverityWarning || ev[0].ServerName != "hk-1" {
		t.Fatalf("CPU 持续 5 分钟后应触发：%+v", ev)
	}
	if f := firing(); len(f) != 1 || f[0].Message != "CPU 使用率 95%（阈值 90%）" {
		t.Fatalf("节点的活动告警：%+v", f)
	}

	// 节点列表带上活动告警
	var list []serverView
	json.Unmarshal(do(h, "GET", "/api/v1/servers", admin, nil).Body.Bytes(), &list)
	for _, sv := range list {
		if sv.ID == id && (len(sv.Alerts) != 1 || sv.Alerts[0].Type != AlertCPU) {
			t.Errorf("节点列表应带活动告警：%+v", sv.Alerts)
		}
		if sv.ID != id && len(sv.Alerts) != 0 {
			t.Errorf("待安装节点不应有告警：%+v", sv.Alerts)
		}
	}

	// 回落到 50%，持续 2 分钟后恢复
	report(310, 50, 40)
	eval(310)
	report(429, 50, 40)
	eval(429)
	if len(firing()) != 1 {
		t.Fatal("恢复未满 2 分钟不应结束")
	}
	report(430, 50, 40)
	eval(430)
	if len(firing()) != 0 || len(events(StateResolved)) != 1 || events(StateResolved)[0].ResolvedAt != at(430).Unix() {
		t.Fatalf("持续 2 分钟后应恢复：%+v", events(""))
	}

	// 磁盘 96% 同时满足 85% 与 95% 两条规则：都记录，列表只显示严重那条
	report(440, 10, 96)
	eval(440)
	if f := firing(); len(f) != 1 || f[0].RuleKey != "disk_critical" || f[0].Severity != SeverityCritical ||
		f[0].Message != "磁盘 /data 使用率 96%（阈值 95%）" {
		t.Fatalf("同类型只显示最严重的一条：%+v", f)
	}
	if len(events(StateFiring)) != 2 {
		t.Fatalf("两条磁盘规则都应记录：%+v", events(StateFiring))
	}

	// 面板重启：从数据库恢复活动告警，不重复记录
	restarted, err := newAlertEngine(s.store, at(445))
	if err != nil || len(restarted.active) != 2 {
		t.Fatalf("重启后应恢复 2 条活动告警：%v %d", err, len(restarted.active))
	}
	s.alerts = restarted
	report(450, 10, 96)
	eval(450)
	if len(events("")) != 3 {
		t.Fatalf("重启后不应重复记录：%d 条", len(events("")))
	}

	// 节点离线：120 秒未上报触发严重告警；离线期间磁盘告警既不触发也不恢复（NODATA），只显示离线（依赖抑制）
	eval(450 + 121)
	f := firing()
	if len(f) != 1 || f[0].Type != AlertOffline || f[0].Severity != SeverityCritical || f[0].Message != "节点离线，已 2 分钟未收到上报" {
		t.Fatalf("离线 120 秒后应只显示离线告警：%+v", f)
	}
	if len(events(StateFiring)) != 3 {
		t.Fatalf("离线期间磁盘告警应保留记录：%+v", events(StateFiring))
	}
	// 重新上报（磁盘已清理）：离线与磁盘告警都恢复
	report(600, 10, 50)
	eval(600)
	if len(firing()) != 0 {
		t.Fatalf("重新上报后应全部恢复：%+v", firing())
	}

	// 节点规则覆盖全局：关闭这台节点的 CPU 告警，活动告警随之结束
	report(610, 99, 50)
	eval(610)
	report(910, 99, 50)
	eval(910)
	if len(firing()) != 1 {
		t.Fatalf("CPU 应再次触发：%+v", firing())
	}
	if _, err := s.store.DB.Exec(`INSERT INTO alert_rules (rule_key, scope_type, scope_id, type, threshold, recover_threshold,
		severity, enabled, created_at, updated_at) VALUES ('cpu', 'server', ?, 'cpu', 90, 80, 'warning', 0, 0, 0)`,
		strconv.FormatInt(id, 10)); err != nil {
		t.Fatal(err)
	}
	eval(920)
	if len(firing()) != 0 || len(events(StateFiring)) != 0 {
		t.Fatalf("规则关闭后活动告警应结束：%+v", events(StateFiring))
	}

	// 接口：活动 / 全部 / 按节点 / 参数校验
	var page struct {
		Items      []AlertEvent `json:"items"`
		NextCursor string       `json:"next_cursor"`
	}
	rec := do(h, "GET", "/api/v1/alerts?state=all&limit=2&server_id="+itoa(id), admin, nil)
	json.Unmarshal(rec.Body.Bytes(), &page)
	if rec.Code != 200 || len(page.Items) != 2 || page.NextCursor == "" {
		t.Errorf("分页：%d %s", rec.Code, rec.Body)
	}
	for _, q := range []string{"?state=x", "?server_id=a", "?cursor=-1", "?limit=0"} {
		if rec := do(h, "GET", "/api/v1/alerts"+q, admin, nil); rec.Code != 422 {
			t.Errorf("%s 应返回 422：%d", q, rec.Code)
		}
	}
	var rules struct{ Items []AlertRule }
	json.Unmarshal(do(h, "GET", "/api/v1/alert-rules", admin, nil).Body.Bytes(), &rules)
	if len(rules.Items) != 13 || rules.Items[0].ScopeType != "global" || rules.Items[12].ScopeType != "server" {
		t.Errorf("规则列表应为 12 条默认 + 1 条节点规则：%d", len(rules.Items))
	}
}

// 面板刚启动、内存中还没有上报时，不对“很久没上报”的节点发离线告警（启动宽限期）。
func TestAlertStartupGrace(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, v, _ := createNode(t, h, admin, `{"name":"hk-1"}`)
	enroll(h, v.EnrollCode, "hk-1", "m-1")
	start := s.alerts.startedAt
	if _, err := s.store.DB.Exec(`UPDATE servers SET last_seen_at = ? WHERE id = ?`, start.Add(-time.Hour).Unix(), v.ServerID); err != nil {
		t.Fatal(err)
	}
	s.evaluateAlerts(start.Add(30 * time.Second))
	if len(s.alerts.firingFor(v.ServerID)) != 0 {
		t.Fatal("启动宽限期内不应发离线告警")
	}
	s.evaluateAlerts(start.Add(alertStartupGrace + time.Second))
	if f := s.alerts.firingFor(v.ServerID); len(f) != 1 || f[0].Type != AlertOffline {
		t.Fatalf("宽限期过后仍未上报应触发离线告警：%+v", f)
	}
}
