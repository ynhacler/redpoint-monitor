package server

import (
	"encoding/json"
	"slices"
	"testing"
)

// 告警分组（修订第 318 条）：每种告警类型都有明确的分组；未知类型归“其他”。
func TestAlertGroupOf(t *testing.T) {
	for _, typ := range []string{AlertOffline, AlertCPU, AlertMemory, AlertDisk, AlertSwap, AlertLoad, AlertTraffic, AlertTrafficForecast,
		AlertProcess, AlertProbeLatency, AlertProbeLoss, AlertReach, AlertContainer, AlertContainerLoop, AlertProxy, AlertPortNew, AlertPortGone,
		"expire", "ssl_expire", "ssl_invalid", "service", "cloud_balance", "cloud_cost", "cloud_sync", "cloud_instance", "cloud_traffic"} {
		if g := alertGroupOf(typ); g == "other" || !slices.Contains(alertGroupKeys, g) {
			t.Errorf("%s 应有明确的分组，得到 %q", typ, g)
		}
	}
	if alertGroupOf(AlertAgentClock) != "other" || alertGroupOf("whatever") != "other" {
		t.Error("Agent 时钟与未知类型应归“其他”")
	}
}

// 渠道只接收选中的分组；面板自检、测试、定期报告不受影响；免打扰汇总按条过滤。
func TestChannelAlertGroups(t *testing.T) {
	c := NotifyChannel{Enabled: true, MinSeverity: SeverityInfo, AlertGroups: []string{"expire", "offline"}}
	cases := []struct {
		m    notifyMessage
		want bool
	}{
		{notifyMessage{Kind: NotifyFiring, Type: AlertOffline, Severity: SeverityCritical}, true},
		{notifyMessage{Kind: NotifyReminder, Type: "expire", Severity: SeverityWarning}, true},
		{notifyMessage{Kind: NotifyFiring, Type: AlertCPU, Severity: SeverityCritical}, false},
		{notifyMessage{Kind: NotifyReminder, Type: "cloud_cost", Severity: SeverityWarning}, false},
		{notifyMessage{Kind: NotifyPanelDown, Type: "panel", Severity: SeverityCritical}, true},
		{notifyMessage{Kind: NotifyTest}, true},
		{notifyMessage{Kind: NotifyDigest}, true},
	}
	for _, x := range cases {
		if got := c.wants(x.m); got != x.want {
			t.Errorf("%s/%s：%v，期望 %v", x.m.Kind, x.m.Type, got, x.want)
		}
	}
	all := NotifyChannel{Enabled: true, MinSeverity: SeverityInfo, AlertGroups: []string{}}
	if !all.wants(notifyMessage{Kind: NotifyFiring, Type: AlertCPU, Severity: SeverityWarning}) {
		t.Error("空分组应接收全部")
	}
	sum := notifyMessage{Kind: NotifyQuietSummary, Count: 2, Items: []notifyMessage{
		{Kind: NotifyFiring, Type: AlertOffline, Severity: SeverityCritical}, {Kind: NotifyFiring, Type: AlertDisk, Severity: SeverityWarning}}}
	if out, ok := sum.forChannel(c); !ok || len(out.Items) != 1 || out.Items[0].Type != AlertOffline {
		t.Errorf("免打扰汇总应只留离线：%+v", out.Items)
	}
}

// 接口：新增时可选分组，无效分组 422；修改时省略分组保持原值，[] 改为全部。
func TestChannelAlertGroupsAPI(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	body := `{"type":"webhook","name":"hook","config":{"url":"https://hooks.example.com/x"}`
	if rec := do(h, "POST", "/api/v1/notification-channels", admin, []byte(body+`,"alert_groups":["nope"]}`)); rec.Code != 422 {
		t.Fatalf("无效分组应 422：%d", rec.Code)
	}
	rec := do(h, "POST", "/api/v1/notification-channels", admin, []byte(body+`,"alert_groups":["offline","expire","offline"]}`))
	var c NotifyChannel
	json.Unmarshal(rec.Body.Bytes(), &c)
	if rec.Code != 201 || !slices.Equal(c.AlertGroups, []string{"expire", "offline"}) {
		t.Fatalf("新增：%d %s", rec.Code, rec.Body)
	}
	do(h, "PUT", "/api/v1/notification-channels/"+itoa64(c.ID), admin, []byte(`{"name":"hook2","config":{}}`))
	if got, _ := s.store.GetChannel(c.ID); got.Name != "hook2" || !slices.Equal(got.AlertGroups, []string{"expire", "offline"}) {
		t.Fatalf("省略分组应保持原值：%+v", got)
	}
	do(h, "PUT", "/api/v1/notification-channels/"+itoa64(c.ID), admin, []byte(`{"name":"hook2","config":{},"alert_groups":[]}`))
	if got, _ := s.store.GetChannel(c.ID); len(got.AlertGroups) != 0 {
		t.Fatalf("[] 应改为全部：%+v", got.AlertGroups)
	}
}
