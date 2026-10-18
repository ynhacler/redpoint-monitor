package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

// 检测目标（修订第 171 条）：只用节点自己的公网地址；内网地址不检测。
func TestExposureTarget(t *testing.T) {
	cases := []struct {
		row  ServerRow
		want string
		ok   bool
	}{
		{ServerRow{IPv4: "203.0.113.7"}, "203.0.113.7", true},
		{ServerRow{IPv4: "10.0.0.5", ExpectedIPv4: "198.51.100.9"}, "198.51.100.9", true},
		{ServerRow{IPv4: "192.168.1.2", IPv6: "2606:4700::1111"}, "2606:4700::1111", true},
		{ServerRow{IPv4: "10.0.0.5", IPv6: "fd00::1"}, "", false},
		{ServerRow{}, "", false},
	}
	for _, c := range cases {
		if got, ok := exposureTarget(c.row); got != c.want || ok != c.ok {
			t.Errorf("%+v → %q %v，应为 %q %v", c.row, got, ok, c.want, c.ok)
		}
	}
}

func TestExposureCheck(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	id, _, _ := s.store.CreateServer("ex-host", 0, 1)
	url := "/api/v1/servers/" + itoa(id) + "/exposure-check"

	// 没有公网 IP：422
	if rec := do(h, "POST", url, admin, nil); rec.Code != 422 {
		t.Fatalf("没有公网 IP 应为 422，得到 %d", rec.Code)
	}
	s.store.DB.Exec(`UPDATE servers SET ipv4 = '203.0.113.7' WHERE id = ?`, id)
	// 还没有监听端口：422
	if rec := do(h, "POST", url, admin, nil); rec.Code != 422 {
		t.Fatalf("没有监听端口应为 422，得到 %d", rec.Code)
	}
	s.mu.Lock()
	s.latest[id] = &snapshot{ReceivedAt: time.Now(), At: time.Now(), Report: protocol.Report{Ports: []protocol.ListenPort{
		{Proto: "tcp", Port: 22, Addrs: []string{"0.0.0.0"}},
		{Proto: "tcp", Port: 443, Addrs: []string{"::"}},
		{Proto: "tcp", Port: 8080, Addrs: []string{"0.0.0.0"}},
		{Proto: "tcp", Port: 3306, Addrs: []string{"127.0.0.1"}}, // 本机：不检测
		{Proto: "udp", Port: 51820, Addrs: []string{"0.0.0.0"}},  // UDP：跳过
	}}}
	s.mu.Unlock()
	var mu sync.Mutex
	var dialed []string
	s.exposure.dial = func(ctx context.Context, addr string) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, addr)
		mu.Unlock()
		switch {
		case strings.HasSuffix(addr, ":443"):
			a, b := net.Pipe()
			b.Close()
			return a, nil
		case strings.HasSuffix(addr, ":22"):
			return nil, &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
		default:
			<-ctx.Done()
			return nil, ctx.Err()
		}
	}
	start := time.Now()
	rec := do(h, "POST", url, admin, nil)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	var res portExposure
	json.Unmarshal(rec.Body.Bytes(), &res)
	got := map[string]string{}
	for _, r := range res.Results {
		got[fmt.Sprintf("%d/%s", r.Port, r.Proto)] = r.State
	}
	want := map[string]string{"22/tcp": "refused", "443/tcp": "open", "8080/tcp": "timeout", "51820/udp": "skipped"}
	if res.Target != "203.0.113.7" || len(got) != len(want) {
		t.Fatalf("结果不对：%+v", res)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s：%s，应为 %s", k, got[k], v)
		}
	}
	// 只连接节点自己的地址；并发检测，总用时约为一个超时
	for _, a := range dialed {
		if !strings.HasPrefix(a, "203.0.113.7:") {
			t.Errorf("连接了其他地址：%s", a)
		}
	}
	if len(dialed) != 3 || time.Since(start) > 2*exposureTimeout {
		t.Errorf("应并发连接 3 个 TCP 端口：%v，用时 %v", dialed, time.Since(start))
	}
	// 详情：只给管理员
	detail := "/api/v1/servers/" + itoa(id)
	if body := do(h, "GET", detail, admin, nil).Body.String(); !strings.Contains(body, `"port_exposure"`) {
		t.Fatalf("管理员应看到检测结果：%s", body)
	}
	if body := do(h, "GET", detail, viewerToken(t, s, "viewer-ex"), nil).Body.String(); strings.Contains(body, "port_exposure") {
		t.Fatal("查看者不应看到检测结果")
	}
	if logs := do(h, "GET", "/api/v1/audit-logs", admin, nil).Body.String(); !strings.Contains(logs, "server.exposure_check") {
		t.Fatal("应记入操作日志")
	}
}
