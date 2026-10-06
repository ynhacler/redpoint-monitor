package server

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNextServiceState(t *testing.T) {
	cases := []struct {
		status     string
		fails, thr int
		ok         bool
		want       string
		wantFails  int
		wantEvent  string
	}{
		{ServiceUnknown, 0, 2, true, ServiceUp, 0, ""},
		{ServiceUnknown, 0, 2, false, ServiceUnknown, 1, ""},        // 未达阈值：刚添加时保持未知
		{ServiceUnknown, 1, 2, false, ServiceDown, 2, NotifyFiring}, // 连续两次失败：异常
		{ServiceUp, 0, 2, false, ServiceUp, 1, ""},                  // 一次失败不告警
		{ServiceUp, 0, 1, false, ServiceDown, 1, NotifyFiring},
		{ServiceDown, 5, 2, false, ServiceDown, 6, ""},          // 已异常：不重复通知
		{ServiceDown, 5, 2, true, ServiceUp, 0, NotifyResolved}, // 恢复
		{ServiceUp, 1, 3, true, ServiceUp, 0, ""},               // 成功清零连续失败
	}
	for _, c := range cases {
		st, f, ev := nextServiceState(c.status, c.fails, c.thr, c.ok)
		if st != c.want || f != c.wantFails || ev != c.wantEvent {
			t.Errorf("%+v → %s %d %q", c, st, f, ev)
		}
	}
}

func TestServiceInputValidation(t *testing.T) {
	ok := []struct{ kind, in, want string }{
		{"http", "example.com/health", "https://example.com/health"},
		{"http", "http://10.0.0.1:8080/ok#x", "http://10.0.0.1:8080/ok"},
		{"tcp", "DB.example.com:5432", "db.example.com:5432"},
		{"tcp", "[2001:db8::1]:22", "[2001:db8::1]:22"},
		{"dns", "Example.COM.", "example.com"},
	}
	for _, c := range ok {
		got, msg := normalizeServiceTarget(c.kind, c.in)
		if msg != "" || got != c.want {
			t.Errorf("%s %q → %q %s", c.kind, c.in, got, msg)
		}
	}
	for _, c := range []struct{ kind, in string }{
		{"http", "https://user:pass@example.com"}, // 【安全】不保存凭证
		{"http", "ftp://example.com"},
		{"tcp", "example.com"},
		{"tcp", "example.com:70000"},
		{"dns", "localhost"},
		{"icmp", "1.1.1.1"},
	} {
		if _, msg := normalizeServiceTarget(c.kind, c.in); msg == "" {
			t.Errorf("应拒绝 %s %q", c.kind, c.in)
		}
	}
	for spec, codes := range map[string][2][]int{
		"":            {{200, 301, 399}, {404, 500}},
		"200":         {{200}, {201, 301}},
		"200-299,301": {{204, 301}, {302, 404}},
	} {
		f, err := parseExpectStatus(spec)
		if err != nil {
			t.Fatal(spec, err)
		}
		for _, c := range codes[0] {
			if !f(c) {
				t.Errorf("%q 应接受 %d", spec, c)
			}
		}
		for _, c := range codes[1] {
			if f(c) {
				t.Errorf("%q 不应接受 %d", spec, c)
			}
		}
	}
	for _, bad := range []string{"abc", "300-200", "99", "200-700"} {
		if _, err := parseExpectStatus(bad); err == nil {
			t.Errorf("应拒绝 %q", bad)
		}
	}
	var m ServiceMonitor
	five, thirty := 5, 30
	if errs := (serviceInput{Name: "x", Kind: "tcp", Target: "a.com:1", IntervalS: &thirty, TimeoutS: &thirty}).apply(&m); len(errs) == 0 {
		t.Error("超时不能不短于间隔")
	}
	if errs := (serviceInput{Name: "x", Kind: "tcp", Target: "a.com:1", IntervalS: &five}).apply(&m); len(errs) == 0 {
		t.Error("间隔至少 30 秒")
	}
}

// HTTPS：始终校验证书；状态码与关键字；连续失败后通知，恢复再通知一次。
func TestServiceMonitorHTTP(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	var healthy atomic.Bool
	healthy.Store(true)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte("status: all good"))
	}))
	t.Cleanup(srv.Close)
	rcv := newReceiver(t)
	c := NotifyChannel{Type: "webhook", Name: "wh", Enabled: true, MinSeverity: "warning", NotifyResolved: true,
		Config: channelConfig{URL: rcv.srv.URL + "/hook"}}
	s.store.SaveChannel(&c, time.Now())

	// 【安全】自签名证书不被信任：失败，而不是跳过校验
	body := `{"name":"API","kind":"http","target":"` + srv.URL + `/health","keyword":"all good","fail_threshold":2}`
	rec := do(h, "POST", "/api/v1/service-monitors", admin, []byte(body))
	var m ServiceMonitor
	json.Unmarshal(rec.Body.Bytes(), &m)
	if rec.Code != 201 || m.Status != ServiceUnknown || !strings.Contains(m.LastError, "证书") {
		t.Fatalf("不受信任的证书应失败：%d %s", rec.Code, rec.Body)
	}

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	s.sslRoots = pool
	check := func() ServiceMonitor {
		var got ServiceMonitor
		json.Unmarshal(do(h, "POST", "/api/v1/service-monitors/"+itoa(m.ID)+"/check", admin, nil).Body.Bytes(), &got)
		return got
	}
	if got := check(); got.Status != ServiceUp || got.LastLatencyMs == nil || got.Fails != 0 {
		t.Fatalf("应恢复为正常：%+v", got)
	}
	healthy.Store(false)
	if got := check(); got.Status != ServiceUp || got.Fails != 1 || !strings.Contains(got.LastError, "503") {
		t.Fatalf("一次失败不应告警：%+v", got)
	}
	got := check()
	s.notify.wg.Wait()
	if got.Status != ServiceDown || rcv.count() != 1 {
		t.Fatalf("连续两次失败应告警：%+v，通知 %d", got, rcv.count())
	}
	if _, b, _ := rcv.last(); !strings.Contains(b, "API") || !strings.Contains(b, "503") {
		t.Errorf("通知内容：%s", b)
	}
	check()
	s.notify.wg.Wait()
	if rcv.count() != 1 {
		t.Error("持续异常不应重复通知")
	}
	healthy.Store(true)
	if got := check(); got.Status != ServiceUp {
		t.Fatalf("应恢复：%+v", got)
	}
	s.notify.wg.Wait()
	if _, b, _ := rcv.last(); rcv.count() != 2 || !strings.Contains(b, "恢复") {
		t.Errorf("恢复应再通知一次：%d %s", rcv.count(), b)
	}

	// 关键字不匹配
	put := `{"name":"API","kind":"http","target":"` + srv.URL + `/health","keyword":"nope"}`
	if rec := do(h, "PUT", "/api/v1/service-monitors/"+itoa(m.ID), admin, []byte(put)); rec.Code != 200 {
		t.Fatalf("修改：%d %s", rec.Code, rec.Body)
	}
	if got := check(); !strings.Contains(got.LastError, "nope") {
		t.Errorf("应说明关键字不匹配：%+v", got)
	}

	// 列表带可用率；检查记录
	var list struct{ Items []ServiceMonitor }
	json.Unmarshal(do(h, "GET", "/api/v1/service-monitors", admin, nil).Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].Uptime24h == nil {
		t.Fatalf("列表：%+v", list)
	}
	var pts struct{ Items []serviceCheckPoint }
	json.Unmarshal(do(h, "GET", "/api/v1/service-monitors/"+itoa(m.ID)+"/checks?range=7d", admin, nil).Body.Bytes(), &pts)
	if len(pts.Items) == 0 || pts.Items[0].Total < 2 {
		t.Errorf("7 天按 10 分钟汇总：%+v", pts.Items)
	}
	if rec := do(h, "DELETE", "/api/v1/service-monitors/"+itoa(m.ID), admin, nil); rec.Code != 204 {
		t.Errorf("删除：%d", rec.Code)
	}
}

func TestServiceMonitorTCPAndDNS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	ctx := context.Background()
	m := ServiceMonitor{Kind: "tcp", Target: "127.0.0.1:" + strconv.Itoa(port), TimeoutS: 3}
	if r := checkService(ctx, m, nil); !r.ok {
		t.Fatalf("端口开放应成功：%s", r.err)
	}
	ln.Close()
	if r := checkService(ctx, m, nil); r.ok || !strings.Contains(r.err, "拒绝") {
		t.Errorf("端口关闭应说明连接被拒绝：%+v", r)
	}
	d := ServiceMonitor{Kind: "dns", Target: "localhost", DNSType: "A", DNSExpect: "127.0.0.1", TimeoutS: 3}
	if r := checkService(ctx, d, nil); !r.ok {
		t.Errorf("localhost 应解析到 127.0.0.1：%s", r.err)
	}
	d.DNSExpect = "192.0.2.1"
	if r := checkService(ctx, d, nil); r.ok || !strings.Contains(r.err, "不包含") {
		t.Errorf("应说明解析结果不包含期望的 IP：%+v", r)
	}
}
