package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// 推送设置接口（修订第 316 条）：告警推送开关、订阅推送；打开推送或换渠道需重新验证。
func TestPushSettingsAPI(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	noReauth := func() { s.store.DB.Exec(`UPDATE sessions SET reauth_until = 0`) }

	// 告警推送：关闭不需要重新验证，打开需要
	noReauth()
	if rec := do(h, "PUT", "/api/v1/settings/alerts-push", admin, []byte(`{"enabled":false}`)); rec.Code != 200 || s.alertsEnabled() {
		t.Fatalf("关闭告警推送：%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "PUT", "/api/v1/settings/alerts-push", admin, []byte(`{"enabled":true}`)); rec.Code != 403 || s.alertsEnabled() {
		t.Fatalf("【安全】打开推送应要求重新验证：%d %s", rec.Code, rec.Body)
	}
	admin = adminToken(t, s)
	if rec := do(h, "PUT", "/api/v1/settings/alerts-push", admin, []byte(`{"enabled":true}`)); rec.Code != 200 || !s.alertsEnabled() {
		t.Fatalf("打开告警推送：%d %s", rec.Code, rec.Body)
	}

	// 订阅推送：默认关闭、三类都开、全部渠道、App 推送、默认提前 1 天
	var p SubPush
	json.Unmarshal(do(h, "GET", "/api/v1/settings/subscription-push", admin, nil).Body.Bytes(), &p)
	if p.Enabled || !p.Charge || !p.End || !p.Budget || !p.App || p.DefaultRemindDays != 1 || p.ChannelIDs == nil {
		t.Fatalf("默认值：%+v", p)
	}
	if rec := do(h, "PUT", "/api/v1/settings/subscription-push", admin, []byte(`{"default_remind_days":5}`)); rec.Code != 422 {
		t.Fatalf("默认提醒天数只能是可选值：%d", rec.Code)
	}
	if rec := do(h, "PUT", "/api/v1/settings/subscription-push", admin, []byte(`{"channel_ids":[999]}`)); rec.Code != 422 {
		t.Fatalf("不存在的渠道应 422：%d", rec.Code)
	}
	noReauth()
	if rec := do(h, "PUT", "/api/v1/settings/subscription-push", admin, []byte(`{"enabled":true}`)); rec.Code != 403 {
		t.Fatalf("【安全】打开订阅推送应要求重新验证：%d", rec.Code)
	}
	if rec := do(h, "PUT", "/api/v1/settings/subscription-push", admin, []byte(`{"default_remind_days":3}`)); rec.Code != 200 {
		t.Fatalf("只改默认天数不需要重新验证：%d %s", rec.Code, rec.Body)
	}
	if got := s.subPush(); got.DefaultRemindDays != 3 || got.Enabled {
		t.Fatalf("保存：%+v", got)
	}
}

// 订阅提醒按订阅推送设置发送（修订第 316 条）：不受告警推送开关影响；只发到选中的渠道；关闭的种类只记录不发送。
func TestSubPushRouting(t *testing.T) {
	s, _, _ := testServer(t)
	a, b := newReceiver(t), newReceiver(t)
	ca := NotifyChannel{Type: "webhook", Name: "a", Enabled: true, MinSeverity: "critical", Config: channelConfig{URL: a.srv.URL + "/hook"}}
	cb := NotifyChannel{Type: "webhook", Name: "b", Enabled: true, MinSeverity: "warning", Config: channelConfig{URL: b.srv.URL + "/hook"}}
	now := time.Now()
	s.store.SaveChannel(&ca, now)
	s.store.SaveChannel(&cb, now)
	msg := func(sub string) notifyMessage {
		return notifyMessage{ServerName: "订阅“X”", Type: "subscription", Sub: sub, Severity: SeverityWarning, Message: "m"}
	}
	send := func(key, sub string) {
		s.remind(key, msg(sub), now)
		s.notify.wg.Wait()
	}

	// 默认关闭：只记录
	send("k1", subPushCharge)
	if a.count()+b.count() != 0 {
		t.Fatal("订阅推送关闭时不应发送")
	}
	// 打开、只发渠道 a；告警推送关闭也照常发送；渠道的最低级别不影响订阅提醒
	s.alertsOn.on.Store(false)
	p := defaultSubPush()
	p.Enabled, p.ChannelIDs, p.End = true, []int64{ca.ID}, false
	s.store.PutSetting(settingSubPush, p, now)
	send("k2", subPushCharge)
	if a.count() != 1 || b.count() != 0 {
		t.Fatalf("应只发到选中的渠道：a=%d b=%d", a.count(), b.count())
	}
	send("k2", subPushCharge) // 去重
	send("k3", subPushEnd)    // 结束提醒已关闭
	if a.count() != 1 {
		t.Fatalf("去重与关闭的种类不应再发送：%d", a.count())
	}
	// 服务器告警仍受告警推送开关控制
	s.notify.dispatch(notifyMessage{Kind: NotifyFiring, Severity: SeverityCritical, ServerName: "n"})
	s.notify.wg.Wait()
	if a.count()+b.count() != 1 {
		t.Fatal("告警推送关闭时服务器告警不应发送")
	}
	// 免打扰时段内先不记录，结束后再发
	s.quiet.set(QuietHours{Enabled: true, Start: "00:00", End: "23:59", Critical: "hold"})
	send("k4", subPushBudget)
	if n := a.count(); n != 1 {
		t.Fatalf("免打扰时段内不应发送：%d", n)
	}
	s.quiet.set(QuietHours{})
	send("k4", subPushBudget)
	if a.count() != 2 {
		t.Fatalf("免打扰结束后应补发：%d", a.count())
	}
}

// 迁移 68（修订第 316 条）：告警推送已打开的面板，订阅推送写入“开启”；没打开的与新面板不写入（默认关闭）。
func TestSubPushMigration(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	mig := migrations[67]
	var p SubPush
	if ok, _ := st.GetSetting(settingSubPush, &p); ok {
		t.Fatal("新面板不应写入订阅推送")
	}
	st.SetAlertsEnabled(false)
	st.DB.Exec(mig)
	if ok, _ := st.GetSetting(settingSubPush, &p); ok {
		t.Fatal("告警推送关闭的面板不应写入")
	}
	st.SetAlertsEnabled(true)
	if _, err := st.DB.Exec(mig); err != nil {
		t.Fatal(err)
	}
	p = defaultSubPush()
	if ok, _ := st.GetSetting(settingSubPush, &p); !ok || !p.Enabled || !p.Charge || !p.App {
		t.Fatalf("告警推送已打开的面板应保持推送订阅提醒：%v %+v", ok, p)
	}
}

// 订阅推送预览（修订第 317 条）：与实际提醒规则一致；已发过的不再列出；关闭的种类标为不推送。
func TestUpcomingSubReminders(t *testing.T) {
	today := time.Date(2026, 11, 8, 0, 0, 0, 0, time.Local)
	list := []Subscription{
		// 每月 10 日扣费，提前 3 天：11-07 已进入区间（今天补发），12-10 在 14 天外
		{ID: 1, Name: "A", Cycle: "monthly", StartDate: "2026-01-10", RemindDays: 3},
		// 每月 20 日扣费，提前 1 天：11-19
		{ID: 2, Name: "B", Cycle: "monthly", StartDate: "2026-01-20", RemindDays: 1},
		// 不提醒扣费；11-12 结束：剩 4 天在 7 天区间（今天补发 7 天的），之后 3 天（11-09）、1 天（11-11）、当天（11-12）
		{ID: 3, Name: "C", Cycle: "monthly", StartDate: "2026-01-12", EndDate: "2026-11-12"},
	}
	p := defaultSubPush()
	p.Enabled, p.End = true, false
	got := upcomingSubReminders(list, today, 14, p, nil)
	var b strings.Builder
	for _, x := range got {
		fmt.Fprintf(&b, "%s %s %s %v; ", x.Date, x.Name, x.Kind, x.Push)
	}
	want := "2026-11-08 A charge true; 2026-11-08 C end false; 2026-11-09 C end false; 2026-11-11 C end false; 2026-11-12 C end false; 2026-11-19 B charge true; "
	if b.String() != want {
		t.Fatalf("预览\n得到 %s\n期望 %s", b.String(), want)
	}
	// 已发过的不再列出
	sent := map[string]bool{"subscription:1:2026-11-10": true, "subscription_end:3:2026-11-12:7": true}
	got = upcomingSubReminders(list, today, 14, p, func(k string) bool { return sent[k] })
	if len(got) != 4 || got[0].Date != "2026-11-09" {
		t.Fatalf("去掉已发过的：%+v", got)
	}
}

// 发送测试（修订第 317 条）：只发到选中的渠道，返回每个渠道的结果；总开关关闭也能测试。
func TestSubPushTest(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	a, b := newReceiver(t), newReceiver(t)
	ca := NotifyChannel{Type: "webhook", Name: "a", Enabled: true, MinSeverity: "critical", Config: channelConfig{URL: a.srv.URL + "/hook"}}
	cb := NotifyChannel{Type: "webhook", Name: "b", Enabled: true, MinSeverity: "warning", Config: channelConfig{URL: b.srv.URL + "/hook"}}
	s.store.SaveChannel(&ca, time.Now())
	s.store.SaveChannel(&cb, time.Now())
	p := defaultSubPush()
	p.ChannelIDs = []int64{cb.ID}
	s.store.PutSetting(settingSubPush, p, time.Now())
	rec := do(h, "POST", "/api/v1/settings/subscription-push/test", admin, nil)
	var res struct {
		Channels []struct {
			ID int64 `json:"id"`
			OK bool  `json:"ok"`
		} `json:"channels"`
		AppDevices int `json:"app_devices"`
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	// 没有配置推送中转：App 设备数为 0，不显示“已发出”
	if rec.Code != 200 || len(res.Channels) != 1 || res.Channels[0].ID != cb.ID || !res.Channels[0].OK || res.AppDevices != 0 {
		t.Fatalf("测试结果：%d %s", rec.Code, rec.Body)
	}
	if a.count() != 0 || b.count() != 1 {
		t.Fatalf("应只发到 b：a=%d b=%d", a.count(), b.count())
	}
	if _, body, _ := b.last(); !strings.Contains(body, "示例订阅") {
		t.Fatalf("内容 %s", body)
	}
}
