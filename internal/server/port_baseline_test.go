package server

import (
	"slices"
	"strings"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

// 对公网开放的判断（修订第 170 条）：与 Web 的暴露范围一致。
func TestPublicAddr(t *testing.T) {
	for a, want := range map[string]bool{
		"0.0.0.0": true, "::": true, "*": true, "203.0.113.5": true, "2001:db8::1": true, // 与 Web 一致：不在内网段即视为公网
		"2606:4700::1111": true, "127.0.0.1": false, "::1": false, "10.0.0.5": false, "192.168.1.2": false, "172.20.0.1": false,
		"100.101.102.103": false, "fd7a:115c::1": false, "fe80::1": false, "[::]": true, "::ffff:127.0.0.1": false, "bogus": false,
	} {
		if got := publicAddr(a); got != want {
			t.Errorf("publicAddr(%q) = %v，应为 %v", a, got, want)
		}
	}
	rep := &protocol.Report{Ports: []protocol.ListenPort{
		{Proto: "tcp", Port: 443, Addrs: []string{"0.0.0.0", "::"}},
		{Proto: "tcp", Port: 22, Addrs: []string{"0.0.0.0"}},
		{Proto: "tcp", Port: 3306, Addrs: []string{"127.0.0.1"}}, // 本机：不算
		{Proto: "tcp", Port: 6379, Addrs: []string{"10.0.0.5"}},  // 内网：不算
		{Proto: "udp", Port: 51820, Addrs: []string{"0.0.0.0"}},
	}}
	if got, ok := exposedPorts(rep); !ok || !slices.Equal(got, []string{"22/tcp", "443/tcp", "51820/udp"}) {
		t.Fatalf("对公网开放的端口不对：%v", got)
	}
	if _, ok := exposedPorts(&protocol.Report{}); ok {
		t.Fatal("旧版 Agent（没有 ports）不应评估")
	}
}

func TestNormalizePorts(t *testing.T) {
	got, err := normalizePorts([]string{" 443/TCP ", "22", "51820/udp", "22/tcp"})
	if err != nil || !slices.Equal(got, []string{"22/tcp", "443/tcp", "51820/udp"}) {
		t.Fatalf("整理结果不对：%v %v", got, err)
	}
	for _, bad := range []string{"0", "70000", "22/icmp", "ssh", "22-80"} {
		if _, err := normalizePorts([]string{bad}); err == nil {
			t.Errorf("%q 应报错", bad)
		}
	}
}

// 端到端：设置基线 → 新增 8080 告警（1 分钟）→ 443 不监听告警（2 分钟）→ 加入基线后恢复 → 取消基线；只给管理员。
func TestPortBaselineAlerts(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	id, _, _ := s.store.CreateServer("pb-host", 0, 1)
	t0 := time.Now().Add(time.Hour)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	report := func(sec int, ports ...string) {
		t.Helper()
		var lp []protocol.ListenPort
		for _, p := range ports {
			n, proto, _ := strings.Cut(p, "/")
			port := 0
			for _, c := range n {
				port = port*10 + int(c-'0')
			}
			lp = append(lp, protocol.ListenPort{Proto: proto, Port: port, Addrs: []string{"0.0.0.0"}})
		}
		s.mu.Lock()
		s.latest[id] = &snapshot{ReceivedAt: at(sec), At: at(sec), Report: protocol.Report{System: protocol.System{BootID: "b"}, Ports: lp}}
		s.mu.Unlock()
		s.store.DB.Exec(`UPDATE servers SET last_seen_at = ?, enroll_state = 'enrolled' WHERE id = ?`, at(sec).Unix(), id)
		if err := s.evaluateAlerts(at(sec)); err != nil {
			t.Fatal(err)
		}
	}
	firing := func(typ string) []AlertEvent {
		items, _, _ := s.store.ListAlertEvents(AlertQuery{State: StateFiring, Limit: 20})
		var out []AlertEvent
		for _, e := range items {
			if e.Type == typ {
				out = append(out, e)
			}
		}
		return out
	}
	path := "/api/v1/servers/" + itoa(id) + "/port-baseline"

	// 没有基线：不评估
	report(0, "22/tcp", "8080/tcp")
	report(120, "22/tcp", "8080/tcp")
	if len(firing(AlertPortNew)) != 0 {
		t.Fatal("没有基线时不应告警")
	}
	// 校验
	if rec := do(h, "PUT", path, admin, []byte(`{"ports":["22/icmp"]}`)); rec.Code != 422 {
		t.Fatalf("格式错误应为 422，得到 %d", rec.Code)
	}
	if rec := do(h, "PUT", path, admin, []byte(`{"ports":["22","443/tcp"]}`)); rec.Code != 200 || !strings.Contains(rec.Body.String(), `["22/tcp","443/tcp"]`) {
		t.Fatalf("保存基线：%d %s", rec.Code, rec.Body)
	}
	report(200, "22/tcp", "8080/tcp")
	report(270, "22/tcp", "8080/tcp")
	if f := firing(AlertPortNew); len(f) != 1 || !strings.Contains(f[0].Message, "8080/tcp") {
		t.Fatalf("未知端口应告警：%+v", f)
	}
	report(330, "22/tcp", "8080/tcp")
	if f := firing(AlertPortGone); len(f) != 1 || !strings.Contains(f[0].Message, "443/tcp") {
		t.Fatalf("基线端口未监听应告警：%+v", f)
	}
	// 只给管理员
	detail := "/api/v1/servers/" + itoa(id)
	if got := do(h, "GET", detail, admin, nil).Body.String(); !strings.Contains(got, `"port_baseline"`) {
		t.Fatalf("管理员应看到基线：%s", got)
	}
	if got := do(h, "GET", detail, viewerToken(t, s, "viewer-pb"), nil).Body.String(); strings.Contains(got, "port_baseline") {
		t.Fatalf("查看者不应看到基线：%s", got)
	}
	// 把 8080 加进基线、443 回来：都恢复
	do(h, "PUT", path, admin, []byte(`{"ports":["22/tcp","443/tcp","8080/tcp"]}`))
	report(400, "22/tcp", "443/tcp", "8080/tcp")
	if len(firing(AlertPortNew))+len(firing(AlertPortGone)) != 0 {
		t.Fatal("应全部恢复")
	}
	// 取消基线
	if rec := do(h, "PUT", path, admin, []byte(`{"ports":[]}`)); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if b, _ := s.store.GetPortBaseline(id); b != nil {
		t.Fatalf("取消后不应有基线：%+v", b)
	}
	if b, _ := s.baselineFor(id); b != nil {
		t.Fatal("取消后缓存也应清除")
	}
}
