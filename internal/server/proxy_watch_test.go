package server

import (
	"strings"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

// 记录与问题（修订第 169 条）：运行过的代理停了算问题；运行中但对外端口没在监听算问题；回环入站不算；只有配置、从未运行的不算。
func TestUpdateProxySeen(t *testing.T) {
	m := map[string]int64{}
	xray := protocol.Proxy{Name: "xray", Running: true, Inbounds: []protocol.ProxyInbound{
		{Protocol: "vless", Port: 443, Network: "tcp", Listening: true},
		{Protocol: "dokodemo-door", Port: 62789, Network: "tcp", Local: true}, // 回环：不算
	}}
	hy := protocol.Proxy{Name: "hysteria"} // 只有配置、没有运行：不算
	changed, problems := updateProxySeen(m, []protocol.Proxy{xray, hy}, 100)
	if !changed || len(problems) != 0 || m["xray"] != 0 || len(m) != 1 {
		t.Fatalf("首次：%v %v %v", changed, problems, m)
	}
	// 端口没在监听
	xray.Inbounds[0].Listening = false
	_, problems = updateProxySeen(m, []protocol.Proxy{xray}, 200)
	if len(problems) != 1 || problems[0] != "xray 端口 443/tcp 没有在监听" {
		t.Fatalf("端口没在监听：%v", problems)
	}
	// 停了
	changed, problems = updateProxySeen(m, nil, 300)
	if !changed || m["xray"] != 300 || len(problems) != 1 || problems[0] != "xray 没有在运行" {
		t.Fatalf("停了：%v %v %v", changed, problems, m)
	}
	if changed, _ = updateProxySeen(m, nil, 400); changed || m["xray"] != 300 {
		t.Fatal("仍未运行时不应变化")
	}
	// 回来了
	xray.Inbounds[0].Listening = true
	if _, problems = updateProxySeen(m, []protocol.Proxy{xray}, 500); len(problems) != 0 || m["xray"] != 0 {
		t.Fatalf("回来后应没有问题：%v %v", problems, m)
	}
}

// 端到端：上报 → 持续 2 分钟告警 → 详情列出 → 不再提醒后恢复；只给管理员。
func TestProxyAlert(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	id, _, _ := s.store.CreateServer("proxy-host", 0, 1)
	t0 := time.Now().Add(time.Hour)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	report := func(sec int, ps ...protocol.Proxy) {
		t.Helper()
		rep := protocol.Report{System: protocol.System{BootID: "b"}, Proxies: ps}
		s.mu.Lock()
		s.latest[id] = &snapshot{ReceivedAt: at(sec), At: at(sec), Report: rep}
		s.mu.Unlock()
		s.store.DB.Exec(`UPDATE servers SET last_seen_at = ?, enroll_state = 'enrolled' WHERE id = ?`, at(sec).Unix(), id)
		s.trackProxies(id, rep, at(sec))
		if err := s.evaluateAlerts(at(sec)); err != nil {
			t.Fatal(err)
		}
	}
	firing := func() []AlertEvent {
		items, _, _ := s.store.ListAlertEvents(AlertQuery{State: StateFiring, Limit: 10})
		var out []AlertEvent
		for _, e := range items {
			if e.Type == AlertProxy {
				out = append(out, e)
			}
		}
		return out
	}
	xray := protocol.Proxy{Name: "xray", Running: true, Inbounds: []protocol.ProxyInbound{{Protocol: "vless", Port: 443, Network: "tcp", Listening: true}}}
	report(0, xray)
	report(60)
	if len(firing()) != 0 {
		t.Fatal("不满 2 分钟不应告警")
	}
	report(200)
	if f := firing(); len(f) != 1 || !strings.Contains(f[0].Message, "代理异常：xray 没有在运行") {
		t.Fatalf("应告警：%+v", f)
	}
	// 面板重启：记录从数据库读回，告警保持
	s.pwatch = newProxyWatch()
	report(210)
	if len(firing()) != 1 {
		t.Fatal("面板重启后告警应保持")
	}

	// 只给管理员：管理员能看到 proxies_missing；查看者看不到代理与 proxies_missing
	path := "/api/v1/servers/" + itoa(id)
	if got := do(h, "GET", path, admin, nil).Body.String(); !strings.Contains(got, `"proxies_missing":[{"name":"xray"`) {
		t.Fatalf("管理员应看到没有运行的代理：%s", got)
	}
	report(300, protocol.Proxy{Name: "sing-box", Running: true, Inbounds: []protocol.ProxyInbound{{Protocol: "vless", Port: 2053, Network: "tcp", Listening: true}}})
	if got := do(h, "GET", path, admin, nil).Body.String(); !strings.Contains(got, `"sing-box"`) {
		t.Fatalf("管理员应看到代理：%s", got)
	}
	viewer := viewerToken(t, s, "viewer-px")
	if got := do(h, "GET", path, viewer, nil).Body.String(); strings.Contains(got, "proxies") || strings.Contains(got, "sing-box") {
		t.Fatalf("查看者不应看到代理：%s", got)
	}
	if got := do(h, "GET", "/api/v1/servers", admin, nil).Body.String(); strings.Contains(got, `"proxies"`) {
		t.Fatalf("节点列表不应带代理：%s", got)
	}

	// 不再提醒：告警恢复
	if rec := do(h, "POST", "/api/v1/servers/"+itoa(id)+"/proxies/forget", admin, []byte(`{"name":"xray"}`)); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body)
	}
	report(400, protocol.Proxy{Name: "sing-box", Running: true})
	if f := firing(); len(f) != 0 {
		t.Fatalf("不再提醒后应恢复：%+v", f)
	}
	if m, _ := s.store.LoadProxySeen(id); m["xray"] != 0 || len(m) != 1 {
		t.Fatalf("记录不对：%v", m)
	}
}
