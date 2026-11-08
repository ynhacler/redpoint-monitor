package server

import (
	"encoding/json"
	"testing"
)

// 告警总开关（修订第 175 条）：新面板默认开启（修订第 316 条）；关闭时节点不带静音与维护、不发送通知，/auth/me 告诉 Web 隐藏告警。
func TestAlertsSwitch(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, v, _ := createNode(t, h, admin, `{"name":"sw-1"}`)
	id := v.ServerID

	me := func() meView {
		var m meView
		json.Unmarshal(do(h, "GET", "/api/v1/auth/me", admin, nil).Body.Bytes(), &m)
		return m
	}
	view := func() serverView {
		var x serverView
		json.Unmarshal(do(h, "GET", "/api/v1/servers/"+itoa(id), admin, nil).Body.Bytes(), &x)
		return x
	}

	// 设置表中没有开关：默认开启
	s.store.SetAlertsEnabled(false)
	s.reloadAlertsSwitch()
	s.store.DB.Exec(`DELETE FROM settings WHERE key = 'alerts'`)
	s.reloadAlertsSwitch()
	if !s.alertsEnabled() || !me().AlertsEnabled {
		t.Fatal("新面板告警应默认开启")
	}
	s.store.SetAlertsEnabled(false)
	s.reloadAlertsSwitch()
	if s.alertsEnabled() {
		t.Fatal("关闭后应为关闭")
	}

	if err := s.store.SetAlertsEnabled(true); err != nil {
		t.Fatal(err)
	}
	s.reloadAlertsSwitch()
	if !me().AlertsEnabled {
		t.Fatal("打开后 /auth/me 应报告开启")
	}
	if rec := do(h, "POST", "/api/v1/silences", admin, []byte(`{"kind":"maintenance","scope_type":"server","scope_id":"`+itoa(id)+`"}`)); rec.Code != 201 && rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	if view().Maintenance == nil {
		t.Fatal("开启时应带维护状态")
	}

	s.store.SetAlertsEnabled(false)
	s.reloadAlertsSwitch()
	if x := view(); x.Maintenance != nil || x.Alerts == nil || len(x.Alerts) != 0 {
		t.Fatalf("关闭时不应带告警、静音与维护：%+v", x)
	}
	// 关闭时 dispatch 直接返回：有启用的渠道也不投递
	if rec := do(h, "POST", "/api/v1/notification-channels", admin,
		[]byte(`{"type":"webhook","name":"hook","min_severity":"info","config":{"url":"https://hooks.example.com/x"}}`)); rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	s.notify.dispatch(notifyMessage{Kind: NotifyPanelIssue, Severity: SeverityCritical})
	s.notify.wg.Wait()
	if n, _ := s.store.ListDeliveries(0, 10); len(n) != 0 {
		t.Fatalf("关闭时不应发送通知：%+v", n)
	}
}

// 迁移 68（修订第 316 条）：新面板不写入开关（默认开启）；已有用户且从没切换过的面板写入“关闭”，切换过的保持不变。
func TestAlertsDefaultMigration(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	var v AlertsSetting
	if ok, _ := st.GetSetting(settingAlerts, &v); ok {
		t.Fatal("新面板不应写入告警开关")
	}
	mig := migrations[67]
	// 已有面板：有用户、没有开关
	if _, err := st.DB.Exec(`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES ('admin', 'x', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(mig); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.GetSetting(settingAlerts, &v); !ok || v.Enabled {
		t.Fatalf("已有面板应保持关闭：%v %+v", ok, v)
	}
	// 已经打开过的面板保持打开
	st.SetAlertsEnabled(true)
	st.DB.Exec(mig)
	if st.GetSetting(settingAlerts, &v); !v.Enabled {
		t.Fatal("已打开的面板不应被改为关闭")
	}
}
