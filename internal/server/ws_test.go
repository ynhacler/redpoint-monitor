package server

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// wsDial 用原始 TCP 完成握手，返回连接与读缓冲；status 为握手响应的状态码。
func wsDial(t *testing.T, srv *httptest.Server, token, origin string) (net.Conn, *bufio.Reader, *http.Response) {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	req := "GET /ws HTTP/1.1\r\nHost: " + strings.TrimPrefix(srv.URL, "http://") + "\r\n" +
		"Upgrade: websocket\r\nConnection: keep-alive, Upgrade\r\nSec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nAccept-Encoding: gzip\r\n"
	if origin != "" {
		req += "Origin: " + origin + "\r\n"
	}
	if strings.HasPrefix(token, PrefixAPIKey) {
		req += "Authorization: Bearer " + token + "\r\n"
	} else if token != "" {
		req += "Cookie: " + sessionCookie + "=" + token + "\r\n"
	}
	if _, err := conn.Write([]byte(req + "\r\n")); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	return conn, br, res
}

// 客户端帧必须加掩码（RFC 6455 5.3）
func wsClientFrame(op byte, payload []byte) []byte {
	b := []byte{0x80 | op, 0x80 | byte(len(payload))}
	var mask [4]byte
	rand.Read(mask[:])
	b = append(b, mask[:]...)
	for i, c := range payload {
		b = append(b, c^mask[i%4])
	}
	return b
}

func wsServerFrame(t *testing.T, conn net.Conn, br *bufio.Reader) (byte, []byte) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var h [2]byte
	if _, err := io.ReadFull(br, h[:]); err != nil {
		t.Fatalf("读取帧：%v", err)
	}
	if h[1]&0x80 != 0 {
		t.Fatal("服务端帧不应加掩码")
	}
	n := int(h[1] & 0x7F)
	if n == 126 {
		var b [2]byte
		io.ReadFull(br, b[:])
		n = int(binary.BigEndian.Uint16(b[:]))
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(br, p); err != nil {
		t.Fatal(err)
	}
	return h[0] & 0x0F, p
}

func TestWebSocketHandshakeAndEvents(t *testing.T) {
	s, h, _ := testServer(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	admin := adminToken(t, s)

	conn, br, res := wsDial(t, srv, admin, srv.URL)
	if res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("握手状态 %d", res.StatusCode)
	}
	// RFC 6455 1.3 的示例：dGhlIHNhbXBsZSBub25jZQ== → s3pPLMBiTxaQ9kYGzzhZRbK+xOo=
	if got := res.Header.Get("Sec-WebSocket-Accept"); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("Sec-WebSocket-Accept = %q", got)
	}
	if res.Header.Get("Content-Encoding") != "" {
		t.Fatal("握手响应不应压缩")
	}

	// 新建节点并注册：页面收到 server.enrolled，事件中不含 Token
	_, n, _ := createNode(t, h, admin, `{"name":"ws-1"}`)
	if rec, _ := enroll(h, n.EnrollCode, "ws-host", "sha256:ws"); rec.Code != 200 {
		t.Fatalf("注册失败 %d %s", rec.Code, rec.Body)
	}
	op, p := wsServerFrame(t, conn, br)
	var ev struct {
		Type     string         `json:"type"`
		ServerID int64          `json:"server_id"`
		TS       int64          `json:"ts"`
		Data     map[string]any `json:"data"`
	}
	if op != 0x1 || json.Unmarshal(p, &ev) != nil {
		t.Fatalf("事件帧 op=%d %s", op, p)
	}
	if ev.Type != evServerEnrolled || ev.ServerID != int64(n.ServerID) || ev.TS == 0 || ev.Data["server_name"] != "ws-1" {
		t.Fatalf("事件 = %+v", ev)
	}
	if strings.Contains(string(p), "agt_") {
		t.Fatal("【安全】事件中出现了 Agent Token")
	}

	// ping → pong（内容原样返回）
	conn.Write(wsClientFrame(0x9, []byte("hi")))
	if op, p := wsServerFrame(t, conn, br); op != 0xA || string(p) != "hi" {
		t.Fatalf("pong op=%d %q", op, p)
	}
	// 关闭：服务端回关闭帧并释放连接
	conn.Write(wsClientFrame(0x8, []byte{0x03, 0xE8}))
	if op, _ := wsServerFrame(t, conn, br); op != 0x8 {
		t.Fatalf("关闭后收到 op=%d", op)
	}
	waitFor(t, func() bool { return s.ws.count() == 0 })
}

// 请求日志：接管后的连接记为 101、INFO，不因连接时长记为慢请求（设计 24.6）
func TestWebSocketRequestLog(t *testing.T) {
	s, _, logs := testServer(t)
	r := httptest.NewRequest("GET", "/ws", nil)
	r.Pattern = "GET /ws"
	logs.Reset()
	s.logRequest(r, &reqInfo{hijacked: true}, 0, 10*time.Minute)
	if out := logs.String(); !strings.Contains(out, `"status":101`) || !strings.Contains(out, `"level":"INFO"`) {
		t.Fatalf("日志 = %s", out)
	}
	logs.Reset()
	s.logRequest(r, &reqInfo{}, 200, 2*time.Second)
	if !strings.Contains(logs.String(), `"level":"WARN"`) {
		t.Fatal("普通慢请求仍应记 WARN")
	}
}

// 【安全】跨站 WebSocket 劫持：Origin 不是面板自身、缺少 Origin、没有会话，都不能建立连接
func TestWebSocketRejects(t *testing.T) {
	s, h, _ := testServer(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	admin := adminToken(t, s)
	for name, c := range map[string]struct {
		token, origin string
		want          int
	}{
		"其他网站":      {admin, "https://evil.example.com", http.StatusForbidden},
		"缺少 Origin": {admin, "", http.StatusForbidden},
		"非 http 来源": {admin, "null", http.StatusForbidden},
		"没有会话":      {"", srv.URL, http.StatusUnauthorized},
		"伪造会话":      {"ses_forgedforgedforged", srv.URL, http.StatusUnauthorized},
	} {
		_, _, res := wsDial(t, srv, c.token, c.origin)
		if res.StatusCode != c.want {
			t.Errorf("%s：状态 %d，期望 %d", name, res.StatusCode, c.want)
		}
	}
	if rec := do(h, "GET", "/ws", admin, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("普通 GET /ws 状态 %d，期望 400", rec.Code)
	}
	if s.ws.count() != 0 {
		t.Fatal("被拒绝的请求不应占用连接")
	}
}

// 【安全】会话被踢出后，已建立的连接在下一次校验时关闭（1008）
func TestWebSocketClosesOnSessionEnd(t *testing.T) {
	old := wsPingInterval
	wsPingInterval = 50 * time.Millisecond
	defer func() { wsPingInterval = old }()
	s, h, _ := testServer(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	admin := adminToken(t, s)
	conn, br, res := wsDial(t, srv, admin, srv.URL)
	if res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("握手状态 %d", res.StatusCode)
	}
	if op, _ := wsServerFrame(t, conn, br); op != 0x9 {
		t.Fatalf("会话有效时应收到 ping，得到 op=%d", op)
	}
	if rec := do(h, "POST", "/api/v1/auth/logout", admin, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("退出 %d", rec.Code)
	}
	for {
		op, p := wsServerFrame(t, conn, br)
		if op == 0x9 {
			continue
		}
		if op != 0x8 || binary.BigEndian.Uint16(p) != 1008 {
			t.Fatalf("期望关闭帧 1008，得到 op=%d %v", op, p)
		}
		break
	}
	waitFor(t, func() bool { return s.ws.count() == 0 })
}

// 慢连接：发送队列满时断开，不阻塞其他连接与推送方
func TestWebSocketSlowClientDropped(t *testing.T) {
	hub := newWSHub()
	slow := hub.add(nil)
	for i := 0; i < wsSendQueue+1; i++ {
		hub.publish(wsEvent{Type: "test"})
	}
	select {
	case <-slow.done:
	default:
		t.Fatal("队列满的连接应被断开")
	}
	if hub.count() != 0 {
		t.Fatal("断开的连接应从列表中移除")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("等待超时")
}

// wsNextEvent 读取下一条事件（跳过 ping）。
func wsNextEvent(t *testing.T, conn net.Conn, br *bufio.Reader) map[string]any {
	t.Helper()
	for {
		op, p := wsServerFrame(t, conn, br)
		if op == 0x9 {
			continue
		}
		if op != 0x1 {
			t.Fatalf("期望文本帧，得到 op=%d", op)
		}
		var ev map[string]any
		json.Unmarshal(p, &ev)
		return ev
	}
}

// 上报后推送 server.metrics（格式同节点列表的一项，不含每核与端口）；API Key 只收到范围内节点的事件（设计 45.2）
func TestWebSocketMetricsAndScope(t *testing.T) {
	s, h, _ := testServer(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	admin := adminToken(t, s)
	_, a, _ := createNode(t, h, admin, `{"name":"hk-1","group":"亚洲"}`)
	_, b, _ := createNode(t, h, admin, `{"name":"us-1","group":"美洲"}`)
	_, ra := enroll(h, a.EnrollCode, "hk-1", "m-a")
	_, rb := enroll(h, b.EnrollCode, "us-1", "m-b")
	key, _ := s.store.CreateAPIKey(&APIKey{Name: "asia", ScopeType: "group", ScopeValue: "亚洲"}, time.Now())

	adminConn, adminBR, res := wsDial(t, srv, admin, srv.URL)
	if res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("管理员握手 %d", res.StatusCode)
	}
	// API Key：Authorization 头认证，不需要 Origin
	keyConn, keyBR, res := wsDial(t, srv, key, "")
	if res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("API Key 握手 %d", res.StatusCode)
	}

	report := func(tok string) {
		body := fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"},"cpu":{"usage":42,"per_core":[40,44]},"ports":[{"proto":"tcp","port":22,"addrs":["0.0.0.0"]}]}`, time.Now().Unix())
		if rec := do(h, "POST", "/api/v1/agent/report", tok, []byte(body)); rec.Code != 204 {
			t.Fatalf("上报 %d", rec.Code)
		}
	}
	report(rb.AgentToken) // 美洲：管理员收到，API Key 收不到
	report(ra.AgentToken) // 亚洲：两者都收到

	ev := wsNextEvent(t, adminConn, adminBR)
	if ev["type"] != evServerMetrics || int64(ev["server_id"].(float64)) != int64(b.ServerID) {
		t.Fatalf("管理员第一条事件 %v", ev)
	}
	data := ev["data"].(map[string]any)
	latest := data["latest"].(map[string]any)
	if data["status"] != "online" || latest["ports"] != nil || latest["cpu"].(map[string]any)["per_core"] != nil {
		t.Fatalf("事件数据应同节点列表（在线，不含每核与端口）：%v", data)
	}
	if ev := wsNextEvent(t, adminConn, adminBR); int64(ev["server_id"].(float64)) != int64(a.ServerID) {
		t.Fatalf("管理员第二条事件 %v", ev)
	}
	// API Key 的第一条就是亚洲节点：美洲节点的事件被过滤
	if ev := wsNextEvent(t, keyConn, keyBR); ev["type"] != evServerMetrics || int64(ev["server_id"].(float64)) != int64(a.ServerID) {
		t.Fatalf("API Key 只应收到范围内节点的事件：%v", ev)
	}
	// 不针对节点的事件（注册）不发给 API Key
	c := &wsClient{scope: &apiScope{Type: "group", Group: "亚洲"}}
	if c.allows(wsEvent{Type: evServerEnrolled}) || !(&wsClient{}).allows(wsEvent{Type: evServerEnrolled}) {
		t.Fatal("注册事件只发给 Web 管理员")
	}
}

// 【安全】API Key 被吊销后，已建立的连接在下一次校验时关闭（1008）
func TestWebSocketAPIKeyRevoked(t *testing.T) {
	old := wsPingInterval
	wsPingInterval = 50 * time.Millisecond
	defer func() { wsPingInterval = old }()
	s, h, _ := testServer(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	k := APIKey{Name: "x", ScopeType: "all"}
	key, _ := s.store.CreateAPIKey(&k, time.Now())
	conn, br, res := wsDial(t, srv, key, "")
	if res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("握手 %d", res.StatusCode)
	}
	s.store.RevokeAPIKey(k.ID, time.Now())
	for {
		op, p := wsServerFrame(t, conn, br)
		if op == 0x9 {
			continue
		}
		if op != 0x8 || binary.BigEndian.Uint16(p) != 1008 {
			t.Fatalf("期望 1008，得到 op=%d %v", op, p)
		}
		break
	}
	// 吊销的 Key 无法再建立连接
	if _, _, res := wsDial(t, srv, key, ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("吊销后握手应为 401，得到 %d", res.StatusCode)
	}
}

// 上下线：第一轮只记录状态；之后状态变化才推送
func TestStatusScan(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, a, _ := createNode(t, h, admin, `{"name":"hk-1"}`)
	_, ra := enroll(h, a.EnrollCode, "hk-1", "m-a")
	do(h, "POST", "/api/v1/agent/report", ra.AgentToken, []byte(fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"}}`, time.Now().Unix())))
	c := s.ws.add(nil)
	defer s.ws.remove(c)
	last := map[int64]string{}
	now := time.Now()
	s.scanStatus(now, last)
	if len(c.send) != 0 || last[int64(a.ServerID)] != "online" {
		t.Fatalf("第一轮不应推送：%d %v", len(c.send), last)
	}
	s.scanStatus(now.Add(10*time.Minute), last) // 很久没有上报：离线
	if len(c.send) != 1 {
		t.Fatalf("应推送一次离线，得到 %d", len(c.send))
	}
	var ev map[string]any
	json.Unmarshal(<-c.send, &ev)
	if ev["type"] != evServerOffline {
		t.Fatalf("事件 %v", ev)
	}
	s.scanStatus(now.Add(10*time.Minute), last) // 没有变化：不再推送
	if len(c.send) != 0 {
		t.Fatal("状态不变时不应推送")
	}
}
