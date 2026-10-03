package server

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

func TestAlertRuleEditing(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, a, _ := createNode(t, h, admin, `{"name":"hk-1","group":"落地"}`)
	_, b, _ := createNode(t, h, admin, `{"name":"jp-1"}`)
	enroll(h, a.EnrollCode, "hk-1", "m-1")
	enroll(h, b.EnrollCode, "jp-1", "m-2")
	now := time.Now()
	setCPU := func(id int64, cpu float64) {
		s.mu.Lock()
		s.latest[id] = &snapshot{ReceivedAt: now, At: now, Report: protocol.Report{CPU: protocol.CPU{Usage: cpu, Cores: 1}}}
		s.mu.Unlock()
	}
	setCPU(a.ServerID, 75)
	setCPU(b.ServerID, 50)

	var list struct{ Items []AlertRule }
	json.Unmarshal(do(h, "GET", "/api/v1/alert-rules", admin, nil).Body.Bytes(), &list)
	var cpu AlertRule
	for _, r := range list.Items {
		if r.RuleKey == "cpu" {
			cpu = r
		}
	}
	base := "/api/v1/alert-rules/" + strconv.FormatInt(cpu.ID, 10)

	// 预览：默认 90% 没有节点触发；改为 70% 时 hk-1 会触发
	type preview struct {
		Matching int           `json:"matching"`
		Total    int           `json:"total"`
		Items    []previewItem `json:"items"`
	}
	var p preview
	json.Unmarshal(do(h, "POST", "/api/v1/alert-rules/preview", admin, []byte(`{"id":`+strconv.FormatInt(cpu.ID, 10)+`,"threshold":70}`)).Body.Bytes(), &p)
	if p.Matching != 1 || p.Total != 2 || p.Items[0].Name != "hk-1" {
		t.Fatalf("预览 70%%：%+v", p)
	}

	// 修改全局规则：部分字段，其他保持不变
	rec := do(h, "PUT", base, admin, []byte(`{"threshold":70,"recover_threshold":60}`))
	var got AlertRule
	json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != 200 || got.Threshold != 70 || got.RecoverThreshold != 60 || got.DurationS != 300 || !got.Enabled {
		t.Fatalf("修改全局规则：%d %s", rec.Code, rec.Body)
	}

	// 分组覆盖：“落地”分组关闭 CPU 告警 → 预览只剩 jp-1 在范围内
	rec = do(h, "POST", "/api/v1/alert-rules", admin, []byte(`{"rule_key":"cpu","scope_type":"group","scope_id":"落地","enabled":false}`))
	var ov AlertRule
	json.Unmarshal(rec.Body.Bytes(), &ov)
	if rec.Code != 201 || ov.ScopeType != "group" || ov.Enabled || ov.Threshold != 70 {
		t.Fatalf("新增分组覆盖（应以全局规则为基础）：%d %s", rec.Code, rec.Body)
	}
	json.Unmarshal(do(h, "POST", "/api/v1/alert-rules/preview", admin, []byte(`{"id":`+strconv.FormatInt(cpu.ID, 10)+`}`)).Body.Bytes(), &p)
	if p.Total != 1 || p.Matching != 0 {
		t.Errorf("被分组覆盖的节点不应计入全局规则的预览：%+v", p)
	}
	if rec := do(h, "POST", "/api/v1/alert-rules", admin, []byte(`{"rule_key":"cpu","scope_type":"group","scope_id":"落地"}`)); rec.Code != 409 {
		t.Errorf("重复覆盖应 409：%d", rec.Code)
	}

	// 节点覆盖 + 删除覆盖
	rec = do(h, "POST", "/api/v1/alert-rules", admin, []byte(`{"rule_key":"disk","scope_type":"server","scope_id":"`+itoa(b.ServerID)+`","threshold":95,"recover_threshold":90}`))
	var nov AlertRule
	json.Unmarshal(rec.Body.Bytes(), &nov)
	if rec.Code != 201 || nov.Threshold != 95 {
		t.Fatalf("新增节点覆盖：%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "DELETE", "/api/v1/alert-rules/"+strconv.FormatInt(nov.ID, 10), admin, nil); rec.Code != 204 {
		t.Errorf("删除覆盖应 204：%d", rec.Code)
	}
	if rec := do(h, "DELETE", base, admin, nil); rec.Code != 422 {
		t.Errorf("默认规则不能删除：%d", rec.Code)
	}

	// 校验
	cases := []struct {
		name, method, path, body string
		status                   int
	}{
		{"阈值超范围", "PUT", base, `{"threshold":150}`, 422},
		{"恢复阈值高于触发阈值", "PUT", base, `{"recover_threshold":99}`, 422},
		{"级别错误", "PUT", base, `{"severity":"urgent"}`, 422},
		{"持续时间为负", "PUT", base, `{"duration_s":-1}`, 422},
		{"未知字段", "PUT", base, `{"type":"disk"}`, 400},
		{"规则不存在", "PUT", "/api/v1/alert-rules/9999", `{}`, 404},
		{"没有这条规则", "POST", "/api/v1/alert-rules", `{"rule_key":"nope","scope_type":"group","scope_id":"x"}`, 422},
		{"不能新增全局", "POST", "/api/v1/alert-rules", `{"rule_key":"cpu","scope_type":"global"}`, 422},
		{"节点不存在", "POST", "/api/v1/alert-rules", `{"rule_key":"cpu","scope_type":"server","scope_id":"999"}`, 422},
		{"离线阈值过小", "PUT", "/api/v1/alert-rules/1", `{"threshold":10}`, 422},
	}
	for _, c := range cases {
		if rec := do(h, c.method, c.path, admin, []byte(c.body)); rec.Code != c.status {
			t.Errorf("%s：%d，应为 %d；%s", c.name, rec.Code, c.status, rec.Body)
		}
	}

	// 修改后的规则立即用于评估：hk-1 被分组覆盖关闭，jp-1 的 CPU 50% 低于 70%，都不触发
	setCPU(b.ServerID, 80)
	s.alerts.startedAt = now.Add(-time.Hour)
	s.evaluateAlerts(now)
	if len(s.alerts.active) != 1 {
		t.Fatalf("jp-1 的 CPU 80%% 应按新阈值 70%% 进入 pending：%d", len(s.alerts.active))
	}
	// 审计
	var logs struct{ Items []AuditLog }
	json.Unmarshal(do(h, "GET", "/api/v1/audit-logs?category=operation", admin, nil).Body.Bytes(), &logs)
	n := 0
	for _, l := range logs.Items {
		if l.Action == "alert_rule.update" || l.Action == "alert_rule.create" || l.Action == "alert_rule.delete" {
			n++
		}
	}
	if n != 4 {
		t.Errorf("规则的修改、新增（2 次）、删除都应记录审计：%d", n)
	}
}
