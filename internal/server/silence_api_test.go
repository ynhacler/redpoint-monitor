package server

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

func TestSilences(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, a, _ := createNode(t, h, admin, `{"name":"hk-1","group":"落地"}`)
	enroll(h, a.EnrollCode, "hk-1", "m-1")
	id := a.ServerID
	s.alerts.startedAt = time.Now().Add(-time.Hour)
	report := func(disk float64) {
		s.mu.Lock()
		s.latest[id] = &snapshot{ReceivedAt: time.Now(), At: time.Now(), Report: protocol.Report{
			CPU: protocol.CPU{Cores: 1}, Disk: []protocol.Disk{{Mount: "/", Usage: disk}}}}
		s.mu.Unlock()
		if err := s.evaluateAlerts(time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	view := func() serverView {
		var v serverView
		json.Unmarshal(do(h, "GET", "/api/v1/servers/"+itoa(id), admin, nil).Body.Bytes(), &v)
		return v
	}
	create := func(body string) (int, Silence) {
		rec := do(h, "POST", "/api/v1/silences", admin, []byte(body))
		var x Silence
		json.Unmarshal(rec.Body.Bytes(), &x)
		return rec.Code, x
	}

	report(90) // 磁盘告警
	if v := view(); len(v.Alerts) != 1 || v.Alerts[0].Silenced {
		t.Fatalf("磁盘告警应触发且未静音：%+v", v.Alerts)
	}

	// 分组静音 1 小时：照常记录，只标记为已静音；节点视图带 muted
	code, mute := create(`{"kind":"mute","scope_type":"group","scope_id":"落地","duration":"1h","reason":"迁移"}`)
	if code != 201 || mute.EndsAt == nil || *mute.EndsAt-mute.StartsAt != 3600 {
		t.Fatalf("新增静音：%d %+v", code, mute)
	}
	v := view()
	if len(v.Alerts) != 1 || !v.Alerts[0].Silenced || v.Muted == nil || v.Muted.Reason != "迁移" {
		t.Fatalf("静音后告警应标记已静音：%+v muted=%+v", v.Alerts, v.Muted)
	}
	// 结束静音
	if rec := do(h, "DELETE", "/api/v1/silences/"+strconv.FormatInt(mute.ID, 10), admin, nil); rec.Code != 204 {
		t.Fatalf("结束静音：%d", rec.Code)
	}
	if v := view(); v.Muted != nil || v.Alerts[0].Silenced {
		t.Fatalf("结束后应恢复：%+v", v)
	}
	if rec := do(h, "DELETE", "/api/v1/silences/"+strconv.FormatInt(mute.ID, 10), admin, nil); rec.Code != 404 {
		t.Errorf("重复结束应 404：%d", rec.Code)
	}

	// 只静音某条规则
	create(`{"kind":"mute","scope_type":"rule","scope_id":"disk"}`)
	if v := view(); !v.Alerts[0].Silenced || v.Muted != nil {
		t.Errorf("规则静音只标记该规则的告警，节点本身不算静音：%+v", v)
	}

	// 维护：下一轮评估结束活动告警，之后不再产生告警
	code, mt := create(`{"kind":"maintenance","scope_type":"server","scope_id":"` + itoa(id) + `"}`)
	if code != 201 || mt.EndsAt != nil {
		t.Fatalf("新增维护（直到手动结束）：%d %+v", code, mt)
	}
	report(95)
	if v := view(); len(v.Alerts) != 0 || v.Maintenance == nil {
		t.Fatalf("维护中不应有告警：%+v", v)
	}
	if n, _, _ := s.store.ListAlertEvents(AlertQuery{State: StateFiring, Limit: 10}); len(n) != 0 {
		t.Fatalf("维护开始后活动告警应结束：%+v", n)
	}
	// 再次设置维护：以新设置为准，旧记录结束
	create(`{"kind":"maintenance","scope_type":"server","scope_id":"` + itoa(id) + `","duration":"8h"}`)
	var list struct{ Items []Silence }
	json.Unmarshal(do(h, "GET", "/api/v1/silences", admin, nil).Body.Bytes(), &list)
	maint := 0
	for _, x := range list.Items {
		if x.Kind == SilenceMaintenance {
			maint++
		}
	}
	if maint != 1 {
		t.Errorf("同一节点只应有一条生效的维护：%+v", list.Items)
	}

	// 到期自动失效
	past := time.Now().Add(-time.Minute).Unix()
	s.store.DB.Exec(`UPDATE silences SET ends_at = ?`, past)
	s.refreshSilences(time.Now())
	report(95)
	if v := view(); v.Maintenance != nil || len(v.Alerts) != 1 {
		t.Fatalf("维护到期后应恢复评估：%+v", v)
	}

	cases := []struct {
		name, body string
		status     int
	}{
		{"维护只能针对节点", `{"kind":"maintenance","scope_type":"group","scope_id":"落地"}`, 422},
		{"类型错误", `{"kind":"snooze","scope_type":"global"}`, 422},
		{"时长错误", `{"kind":"mute","scope_type":"global","duration":"3h"}`, 422},
		{"节点不存在", `{"kind":"mute","scope_type":"server","scope_id":"999"}`, 422},
		{"规则不存在", `{"kind":"mute","scope_type":"rule","scope_id":"nope"}`, 422},
		{"未知字段", `{"kind":"mute","scope_type":"global","x":1}`, 400},
		{"全部静音", `{"kind":"mute","scope_type":"global","duration":"24h"}`, 201},
	}
	for _, c := range cases {
		if code, _ := create(c.body); code != c.status {
			t.Errorf("%s：%d，应为 %d", c.name, code, c.status)
		}
	}
}
