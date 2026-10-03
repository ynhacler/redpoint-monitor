package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestQuietHoursWindow(t *testing.T) {
	utc := func(h, m int) time.Time { return time.Date(2026, 10, 3, h, m, 0, 0, time.UTC) }
	cases := []struct {
		name string
		q    QuietHours
		at   time.Time
		want bool
	}{
		{"跨午夜：深夜", QuietHours{Enabled: true, Start: "23:00", End: "08:00", Timezone: "UTC"}, utc(23, 30), true},
		{"跨午夜：凌晨", QuietHours{Enabled: true, Start: "23:00", End: "08:00", Timezone: "UTC"}, utc(7, 59), true},
		{"跨午夜：结束时刻不含", QuietHours{Enabled: true, Start: "23:00", End: "08:00", Timezone: "UTC"}, utc(8, 0), false},
		{"跨午夜：白天", QuietHours{Enabled: true, Start: "23:00", End: "08:00", Timezone: "UTC"}, utc(12, 0), false},
		{"同一天：开始时刻包含", QuietHours{Enabled: true, Start: "12:00", End: "14:00", Timezone: "UTC"}, utc(12, 0), true},
		{"同一天：之外", QuietHours{Enabled: true, Start: "12:00", End: "14:00", Timezone: "UTC"}, utc(14, 30), false},
		{"未启用", QuietHours{Enabled: false, Start: "00:00", End: "23:59", Timezone: "UTC"}, utc(12, 0), false},
		// UTC 15:30 = 上海 23:30
		{"按设置的时区计算", QuietHours{Enabled: true, Start: "23:00", End: "08:00", Timezone: "Asia/Shanghai"}, utc(15, 30), true},
		{"按设置的时区计算（白天）", QuietHours{Enabled: true, Start: "23:00", End: "08:00", Timezone: "Asia/Shanghai"}, utc(3, 0), false},
	}
	for _, c := range cases {
		if got := c.q.active(c.at); got != c.want {
			t.Errorf("%s：%v，应为 %v", c.name, got, c.want)
		}
	}

	bad := []QuietHours{
		{Start: "25:00", End: "08:00", Critical: "notify"},
		{Start: "08:00", End: "08:00", Critical: "notify"},
		{Start: "23:00", End: "08:00", Timezone: "Mars/Base", Critical: "notify"},
		{Start: "23:00", End: "08:00", Critical: "maybe"},
	}
	for _, q := range bad {
		if len(q.validate()) == 0 {
			t.Errorf("应拒绝：%+v", q)
		}
	}
}

// 免打扰期间：严重按设置、警告汇总、提示不发；汇总按渠道最低级别裁剪；结束后发出（设计 16.5）。
func TestQuietHoursNotify(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	hook := newReceiver(t)
	for _, b := range []string{
		`{"type":"webhook","name":"all","min_severity":"info","config":{"url":"` + hook.srv.URL + `/all"}}`,
		`{"type":"webhook","name":"crit","min_severity":"critical","config":{"url":"` + hook.srv.URL + `/crit"}}`,
	} {
		if rec := do(h, "POST", "/api/v1/notification-channels", admin, []byte(b)); rec.Code != 201 {
			t.Fatal(rec.Body)
		}
	}
	night := time.Date(2026, 10, 3, 23, 30, 0, 0, time.UTC)
	morning := time.Date(2026, 10, 4, 8, 0, 10, 0, time.UTC)
	set := func(critical string) {
		rec := do(h, "PUT", "/api/v1/settings/quiet-hours", admin,
			[]byte(`{"enabled":true,"start":"23:00","end":"08:00","timezone":"UTC","critical":"`+critical+`"}`))
		if rec.Code != 200 {
			t.Fatalf("保存免打扰：%d %s", rec.Code, rec.Body)
		}
	}
	msg := func(sev, server string) notifyMessage {
		return notifyMessage{Kind: NotifyFiring, Severity: sev, ServerName: server, Message: "CPU 使用率 95%（阈值 90%）", StartedAt: night}
	}
	sendAll := func() {
		s.send(msg(SeverityCritical, "c-1"), night)
		s.send(msg(SeverityWarning, "w-1"), night)
		s.send(msg(SeverityWarning, "w-2"), night)
		s.send(msg(SeverityInfo, "i-1"), night)
		s.notify.wg.Wait()
	}
	paths := func() map[string][]string {
		hook.mu.Lock()
		defer hook.mu.Unlock()
		out := map[string][]string{}
		for i, p := range hook.paths {
			var b map[string]any
			json.Unmarshal([]byte(hook.bodies[i]), &b)
			out[p] = append(out[p], b["kind"].(string)+"|"+b["text"].(string))
		}
		return out
	}

	// 严重仍通知：两个渠道立即收到严重告警，警告与提示不发
	set("notify")
	sendAll()
	got := paths()
	if len(got["/all"]) != 1 || len(got["/crit"]) != 1 || !strings.Contains(got["/all"][0], "c-1") {
		t.Fatalf("免打扰期间只应立即发送严重告警：%v", got)
	}
	// 仍在免打扰：不汇总
	s.flushQuiet(night.Add(time.Hour))
	s.notify.wg.Wait()
	if len(paths()["/all"]) != 1 {
		t.Fatal("免打扰尚未结束不应发出汇总")
	}
	// 结束：汇总只发给接收警告的渠道，只含两条警告，不含提示
	s.flushQuiet(morning)
	s.notify.wg.Wait()
	got = paths()
	if len(got["/all"]) != 2 || len(got["/crit"]) != 1 {
		t.Fatalf("汇总应只发给 min_severity ≤ 警告的渠道：%v", got)
	}
	sum := got["/all"][1]
	if !strings.HasPrefix(sum, "quiet_summary|") || !strings.Contains(sum, "2 条") || !strings.Contains(sum, "w-1") ||
		!strings.Contains(sum, "w-2") || strings.Contains(sum, "i-1") || strings.Contains(sum, "c-1") {
		t.Errorf("汇总内容：%s", sum)
	}

	// 严重也汇总：只收严重的渠道收到只含严重条目的汇总
	set("summary")
	sendAll()
	if n := len(paths()["/crit"]); n != 1 {
		t.Fatalf("严重设为汇总时不应立即发送：%d", n)
	}
	s.flushQuiet(morning)
	s.notify.wg.Wait()
	got = paths()
	crit := got["/crit"][len(got["/crit"])-1]
	if len(got["/crit"]) != 2 || !strings.Contains(crit, "1 条") || !strings.Contains(crit, "c-1") || strings.Contains(crit, "w-1") {
		t.Errorf("只收严重的渠道的汇总：%v", got["/crit"])
	}
	if all := got["/all"][len(got["/all"])-1]; !strings.Contains(all, "3 条") {
		t.Errorf("全部级别渠道的汇总应含 3 条（不含提示）：%s", all)
	}

	// 面板自检不受免打扰影响
	before := len(paths()["/crit"])
	s.notify.dispatch(notifyMessage{Kind: NotifyPanelDown, Severity: SeverityCritical, Count: 2, StartedAt: night})
	s.notify.wg.Wait()
	if len(paths()["/crit"]) != before+1 {
		t.Error("面板自检通知应直接发送")
	}

	// 接口：读取、审计、校验
	var v quietView
	json.Unmarshal(do(h, "GET", "/api/v1/settings/quiet-hours", admin, nil).Body.Bytes(), &v)
	if !v.Enabled || v.Start != "23:00" || v.Critical != "summary" || v.EffectiveTimezone != "UTC" {
		t.Errorf("读取设置：%+v", v)
	}
	if rec := do(h, "PUT", "/api/v1/settings/quiet-hours", admin, []byte(`{"enabled":true,"start":"9","end":"08:00","critical":"notify"}`)); rec.Code != 422 {
		t.Errorf("格式错误应 422：%d", rec.Code)
	}
	if logs, _, _ := s.store.ListAudit(AuditQuery{Action: "setting.update", Limit: 10}); len(logs) != 2 {
		t.Errorf("修改应记入审计：%d", len(logs))
	}
	// 重启后从数据库读取
	q, err := newQuietState(s.store)
	if err != nil || q.get().Critical != "summary" || !q.get().Enabled {
		t.Errorf("设置应持久化：%+v %v", q.get(), err)
	}
}
