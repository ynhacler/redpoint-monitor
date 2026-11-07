package server

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// reqFrom 从指定来源地址发请求；token 为会话时带上 Cookie 与 CSRF。
func reqFrom(h http.Handler, addr, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.RemoteAddr = addr
	if token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		req.Header.Set(csrfHeader, csrfFor(token))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLimitKey(t *testing.T) {
	cases := map[string]string{
		"203.0.113.9":          "203.0.113.9",
		"::ffff:203.0.113.9":   "::ffff:203.0.113.9", // IPv4 映射地址按 IPv4 对待，原样保留
		"2001:db8:1:2::1":      "2001:db8:1:2::/64",
		"2001:db8:1:2:aa:bb::": "2001:db8:1:2::/64",
		"2001:db8:1:3::1":      "2001:db8:1:3::/64",
		"user:admin":           "user:admin",
		"":                     "",
	}
	for in, want := range cases {
		if got := limitKey(in); got != want {
			t.Errorf("limitKey(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// 【安全】同一 IPv6 /64 内轮换地址不能绕过登录锁定（修订第 306 条）。
func TestLoginLimitIPv6Prefix(t *testing.T) {
	s, h, _ := testServer(t)
	s.captcha = nil
	adminToken(t, s)
	got429 := 0
	for i := 0; i < 12; i++ {
		rec := reqFrom(h, fmt.Sprintf("[2001:db8:1:2::%x]:1", i+1), "POST", "/api/v1/auth/login", "",
			`{"username":"admin","password":"wrong-password-x"}`)
		if rec.Code == http.StatusTooManyRequests {
			got429++
		}
	}
	if got429 == 0 {
		t.Fatal("同一 /64 轮换地址 12 次错误登录没有被限流")
	}
	// 另一个 /64 不受影响
	if rec := reqFrom(h, "[2001:db8:9:9::1]:1", "POST", "/api/v1/auth/login", "",
		`{"username":"admin","password":"wrong-password-x"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("其他 /64 应照常返回 401，得到 %d", rec.Code)
	}
}

// 【安全】换 IP 分散猜密码：同一账号失败过多后，陌生来源即使密码正确也被拒绝；
// 曾成功登录过的来源照常登录，攻击者无法把管理员锁在外面（修订第 306 条）。
func TestLoginAccountLock(t *testing.T) {
	s, h, _ := testServer(t)
	s.captcha = nil
	adminToken(t, s) // 创建账号；会话来源为 127.0.0.1
	good := `{"username":"admin","password":"correct horse battery"}`
	// 先从一个地址成功登录，记入审计日志
	if rec := reqFrom(h, "198.51.100.1:1", "POST", "/api/v1/auth/login", "", good); rec.Code != http.StatusOK {
		t.Fatalf("首次登录 %d：%s", rec.Code, rec.Body)
	}
	for i := 0; i < 10; i++ { // 每个地址只错 1 次，不触发 IP 锁定
		rec := reqFrom(h, fmt.Sprintf("203.0.113.%d:1", i+1), "POST", "/api/v1/auth/login", "",
			`{"username":"admin","password":"wrong-password-x"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误登录 %d", i+1, rec.Code)
		}
	}
	if rec := reqFrom(h, "192.0.2.50:1", "POST", "/api/v1/auth/login", "", good); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("账号锁定后陌生来源应 429，得到 %d", rec.Code)
	}
	if rec := reqFrom(h, "198.51.100.1:1", "POST", "/api/v1/auth/login", "", good); rec.Code != http.StatusOK {
		t.Fatalf("曾成功登录的来源应照常登录，得到 %d：%s", rec.Code, rec.Body)
	}
	// 用户名不存在同样计数并锁定，响应不暴露账号是否存在
	for i := 0; i < 10; i++ {
		reqFrom(h, fmt.Sprintf("203.0.114.%d:1", i+1), "POST", "/api/v1/auth/login", "",
			`{"username":"nobody","password":"wrong-password-x"}`)
	}
	if rec := reqFrom(h, "192.0.2.51:1", "POST", "/api/v1/auth/login", "",
		`{"username":"nobody","password":"wrong-password-x"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("不存在的用户名也应锁定，得到 %d", rec.Code)
	}
	// 锁定 15 分钟后解除
	now := time.Now().Add(16 * time.Minute)
	s.accountLimit.now = func() time.Time { return now }
	if rec := reqFrom(h, "192.0.2.52:1", "POST", "/api/v1/auth/login", "", good); rec.Code != http.StatusOK {
		t.Fatalf("锁定到期后应可登录，得到 %d", rec.Code)
	}
}

// 【安全】改密码接口不能用来无限次猜当前密码（修订第 306 条）。
func TestChangePasswordRateLimited(t *testing.T) {
	s, h, _ := testServer(t)
	tok := adminToken(t, s)
	got429 := 0
	for i := 0; i < 12; i++ {
		rec := reqFrom(h, "198.51.100.77:1", "POST", "/api/v1/auth/password", tok,
			fmt.Sprintf(`{"current_password":"guess-%d","new_password":"new-password-12345"}`, i))
		if rec.Code == http.StatusTooManyRequests {
			got429++
		}
	}
	if got429 == 0 {
		t.Fatal("改密码猜当前密码 12 次没有被限流")
	}
}

// 【安全】重新生成注册码、新建通知渠道、修改渠道连接参数需重新验证密码；只切换开关不需要（修订第 306 条）。
func TestReauthForEnrollCodeAndChannels(t *testing.T) {
	s, h, _ := testServer(t)
	tok := adminToken(t, s)
	id, _, err := s.store.CreateServer("node-a", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	ch := []byte(`{"type":"webhook","name":"hook","config":{"url":"https://example.com/hook"}}`)
	created := do(h, "POST", "/api/v1/notification-channels", tok, ch)
	if created.Code != http.StatusCreated {
		t.Fatalf("重新验证后新建渠道 %d：%s", created.Code, created.Body)
	}
	var chID int64
	if _, err := fmt.Sscanf(strings.SplitN(strings.SplitN(created.Body.String(), `"id":`, 2)[1], ",", 2)[0], "%d", &chID); err != nil {
		t.Fatal(err)
	}
	se, _ := s.store.LookupSession(tok, time.Now())
	s.store.SetReauth(se.ID, time.Time{}) // 重新验证过期
	chPath := fmt.Sprintf("/api/v1/notification-channels/%d", chID)
	cases := []struct {
		method, path, body string
		want               int
	}{
		{"POST", fmt.Sprintf("/api/v1/servers/%d/enroll-code", id), `{}`, http.StatusForbidden},
		{"POST", "/api/v1/notification-channels", string(ch), http.StatusForbidden},
		{"PUT", chPath, `{"name":"hook","config":{"url":"https://evil.example/x"}}`, http.StatusForbidden},
		{"PUT", chPath, `{"name":"hook","enabled":false,"config":{}}`, http.StatusOK},
		{"PUT", chPath, `{"name":"hook2","config":{"url":"https://example.com/hook"}}`, http.StatusOK},
	}
	for _, c := range cases {
		rec := do(h, c.method, c.path, tok, []byte(c.body))
		if rec.Code != c.want {
			t.Errorf("%s %s %s：%d，期望 %d（%s）", c.method, c.path, c.body, rec.Code, c.want, rec.Body)
		}
		if c.want == http.StatusForbidden && decodeError(t, rec).Code != CodeReauthRequired {
			t.Errorf("%s %s 应返回 reauth_required", c.method, c.path)
		}
	}
}
