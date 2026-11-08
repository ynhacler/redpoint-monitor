package server

import (
	"encoding/json"
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
