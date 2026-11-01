package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"vpsmon/internal/protocol"
)

// 定期报告的区间与发送时间（修订第 277 条）：周报为周一之前的 7 天，月报为上个月；当天到设定时间后才发送。
func TestDigestSchedule(t *testing.T) {
	sh, _ := time.LoadLocation("Asia/Shanghai")
	at := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, sh) }
	w := Digest{Enabled: true, Period: "weekly", Time: "09:00", Timezone: "Asia/Shanghai"}
	mo := Digest{Enabled: true, Period: "monthly", Time: "09:00", Timezone: "Asia/Shanghai"}
	cases := []struct {
		name string
		d    Digest
		now  time.Time
		due  bool
		span string
	}{
		{"周一 9 点", w, at(2026, 11, 2, 9, 0), true, "10-26～11-02 digest:weekly:2026-W45"},
		{"周一 8 点", w, at(2026, 11, 2, 8, 59), false, "10-26～11-02 digest:weekly:2026-W45"},
		{"周二", w, at(2026, 11, 3, 10, 0), false, "10-27～11-03 digest:weekly:2026-W45"},
		{"1 日", mo, at(2026, 11, 1, 23, 0), true, "10-01～11-01 digest:monthly:2026-10"},
		{"1 月 1 日", mo, at(2027, 1, 1, 9, 30), true, "12-01～01-01 digest:monthly:2026-12"},
		{"未启用", Digest{Period: "weekly", Time: "09:00"}, at(2026, 11, 2, 10, 0), false, ""},
	}
	for _, c := range cases {
		if got := c.d.due(c.now); got != c.due {
			t.Errorf("%s：due = %v", c.name, got)
		}
		if c.span == "" {
			continue
		}
		from, to, key := c.d.span(c.now)
		if got := from.Format("01-02") + "～" + to.Format("01-02") + " " + key; got != c.span {
			t.Errorf("%s：span = %s，期望 %s", c.name, got, c.span)
		}
	}
	bad := Digest{Period: "daily", Time: "9", Timezone: "Mars/Base"}
	if errs := bad.validate(); len(errs) != 3 {
		t.Errorf("应有 3 处错误：%+v", errs)
	}
}

// 报告内容：节点、告警次数与离线时长、流量、到期、订阅；发到只收严重告警的渠道；按区间只发一次。
func TestDigestContent(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	hook := newReceiver(t)
	if rec := do(h, "POST", "/api/v1/notification-channels", admin,
		[]byte(`{"type":"webhook","name":"crit","min_severity":"critical","config":{"url":"`+hook.srv.URL+`/crit"}}`)); rec.Code != 201 {
		t.Fatal(rec.Body)
	}
	now := time.Now()
	id, _, _ := s.store.CreateServer("hk-1", 1000e9, 1)
	jp, _, _ := s.store.CreateServer("jp-1", 0, 1)
	sec := 2
	s.mu.Lock()
	s.latest[jp] = &snapshot{ReceivedAt: now, At: now, Report: protocol.Report{Updates: &protocol.Updates{RebootRequired: true, Security: &sec}}}
	s.mu.Unlock()
	exp := now.AddDate(0, 0, 3).Format("2006-01-02")
	if _, err := s.store.DB.Exec(`UPDATE servers SET expire_date = ? WHERE id = ?`, exp, id); err != nil {
		t.Fatal(err)
	}
	start := now.Add(-50 * time.Hour).Unix()
	s.store.InsertAlertEvent(AlertEvent{RuleKey: "offline", ServerID: id, Type: AlertOffline, Severity: SeverityCritical,
		State: "resolved", Message: "离线", StartedAt: start, FiredAt: start + 60})
	s.store.DB.Exec(`UPDATE alert_events SET resolved_at = ?`, start+2*3600)
	sub := Subscription{Name: "Netflix", PriceCents: 1599, Currency: "USD", Cycle: "monthly", Category: "streaming",
		StartDate: now.AddDate(0, 0, 2).Format("2006-01-02")}
	if err := s.store.SaveSubscription(&sub, now); err != nil {
		t.Fatal(err)
	}
	rec := do(h, "PUT", "/api/v1/settings/digest", admin, []byte(`{"enabled":true,"period":"weekly","time":"00:00","timezone":""}`))
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	var v digestView
	json.Unmarshal(rec.Body.Bytes(), &v)
	for _, want := range []string{"VPS Monitor 周报", "节点：共 2 台", "系统：需要重启 1 台（jp-1），有安全更新 1 台（jp-1 2）", "告警：触发 1 次（严重 1 次），最多：hk-1 1 次", "离线累计：hk-1 2 小时",
		"本周期流量用量最高：", "hk-1 0%（0 B / 1 TB", "7 天内到期的节点：hk-1", "7 天内扣费的订阅 1 笔：Netflix 15.99 USD"} {
		if !strings.Contains(v.Preview, want) {
			t.Errorf("预览缺少 %q：\n%s", want, v.Preview)
		}
	}
	if rec := do(h, "POST", "/api/v1/settings/digest/send", admin, nil); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body)
	}
	s.notify.wg.Wait()
	if hook.count() != 1 {
		t.Fatalf("只收严重告警的渠道也应收到报告：%d", hook.count())
	}
	if _, body, _ := hook.last(); !strings.Contains(body, `"kind":"digest"`) {
		t.Errorf("webhook kind：%s", body)
	}
	// 定时发送：到时间发一次，同一区间不重复
	d := s.digestCfg()
	monday := now
	for monday.Weekday() != time.Monday {
		monday = monday.AddDate(0, 0, 1)
	}
	monday = time.Date(monday.Year(), monday.Month(), monday.Day(), 12, 0, 0, 0, time.Local)
	if !d.due(monday) {
		t.Fatal("周一 12 点应发送")
	}
	s.checkDigest(monday)
	s.checkDigest(monday.Add(time.Hour))
	s.notify.wg.Wait()
	if hook.count() != 2 {
		t.Errorf("同一周只应定时发送一次：%d", hook.count())
	}
}
