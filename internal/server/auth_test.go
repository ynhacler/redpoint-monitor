package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"
)

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil || !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("PHC 格式不正确：%q %v", h, err)
	}
	if h2, _ := HashPassword("correct horse battery"); h2 == h {
		t.Error("每次哈希应使用不同的随机盐")
	}
	if ok, rehash, err := VerifyPassword("correct horse battery", h); !ok || rehash || err != nil {
		t.Errorf("正确密码应通过且不需要升级：%v %v %v", ok, rehash, err)
	}
	if ok, _, _ := VerifyPassword("wrong", h); ok {
		t.Error("错误密码不应通过")
	}
	// 旧参数（内存更低）的哈希仍能验证，并提示升级
	salt := []byte("0123456789abcdef")
	weak := "$argon2id$v=19$m=8192,t=1,p=1$" + b64(salt) + "$" + b64(argon2.IDKey([]byte("pw"), salt, 1, 8192, 1, 32))
	if ok, rehash, _ := VerifyPassword("pw", weak); !ok || !rehash {
		t.Errorf("旧参数哈希应通过并提示升级：%v %v", ok, rehash)
	}
	for _, bad := range []string{"", "plain", "$2a$10$bcrypt", "$argon2id$v=19$m=x$a$b"} {
		if _, _, err := VerifyPassword("pw", bad); err == nil {
			t.Errorf("无法识别的哈希应返回错误：%q", bad)
		}
	}
}

func TestPasswordRulesAndRandom(t *testing.T) {
	cases := map[string]bool{"short": false, "exactly12chr": true, " padded password ": false, strings.Repeat("a", 300): false}
	for pw, ok := range cases {
		if (validatePassword(pw) == "") != ok {
			t.Errorf("validatePassword(%.20q) 应%v", pw, map[bool]string{true: "通过", false: "拒绝"}[ok])
		}
	}
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p := RandomPassword()
		if len(p) != 20 || strings.ContainsAny(p, "0O1lI") || seen[p] {
			t.Fatalf("随机密码不符合要求：%q", p)
		}
		seen[p] = true
	}
}

func b64(b []byte) string { return base64.RawStdEncoding.EncodeToString(b) }

// login 发送登录请求；proxyHTTPS 为 true 时模拟经 Caddy 的 HTTPS 访问。
func login(h http.Handler, user, pw string, remember, proxyHTTPS bool) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]any{"username": user, "password": pw, "remember": remember})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(string(b)))
	req.RemoteAddr = "198.51.100.20:5555"
	if proxyHTTPS {
		req.RemoteAddr = "127.0.0.1:5555"
		req.Header.Set("X-Forwarded-For", "198.51.100.21")
		req.Header.Set("X-Forwarded-Proto", "https")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func sessionFrom(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return c
		}
	}
	t.Fatalf("没有设置会话 Cookie：%d %s", rec.Code, rec.Body)
	return nil
}

func newAdmin(t *testing.T, s *Server, pw string, mustChange bool) {
	t.Helper()
	h, _ := HashPassword(pw)
	if _, err := s.store.SetAdminPassword("admin", h, mustChange, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestLoginAndCookie(t *testing.T) {
	s, h, logs := testServer(t)
	newAdmin(t, s, "correct horse battery", false)

	rec := login(h, "ADMIN", "correct horse battery", false, false) // 用户名不区分大小写
	if rec.Code != 200 {
		t.Fatalf("登录应成功：%d %s", rec.Code, rec.Body)
	}
	c := sessionFrom(t, rec)
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Secure || c.MaxAge != 0 || !strings.HasPrefix(c.Value, PrefixSession) {
		t.Errorf("【安全】Cookie 属性不正确（HttpOnly、SameSite=Strict；本地 HTTP 不加 Secure；未勾选记住登录为会话 Cookie）：%+v", c)
	}
	var me meView
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me.Username != "admin" || me.CSRFToken != csrfFor(c.Value) {
		t.Errorf("登录响应应包含用户名与 CSRF：%+v", me)
	}

	// 经 Caddy 的 HTTPS：加 Secure；记住登录：保存 7 天
	c2 := sessionFrom(t, login(h, "admin", "correct horse battery", true, true))
	if !c2.Secure || c2.MaxAge != int(sessionMax.Seconds()) {
		t.Errorf("HTTPS 下应加 Secure，记住登录应保存 7 天：%+v", c2)
	}

	// 用会话访问；/auth/me 返回同一个 CSRF
	if rec := do(h, "GET", "/api/v1/auth/me", c.Value, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), csrfFor(c.Value)) {
		t.Errorf("/auth/me：%d %s", rec.Code, rec.Body)
	}

	// 【安全】日志与审计中不得出现密码或会话令牌（设计 24.7）
	var audit string
	s.store.DB.QueryRow(`SELECT group_concat(action || ' ' || result || ' ' || details, '\n') FROM audit_logs`).Scan(&audit)
	for _, secret := range []string{"correct horse battery", c.Value, c2.Value} {
		if strings.Contains(logs.String(), secret) || strings.Contains(audit, secret) {
			t.Errorf("日志或审计中出现了敏感值 %.8s…", secret)
		}
	}
	if !strings.Contains(audit, "auth.login success") {
		t.Errorf("登录应写审计日志：%s", audit)
	}
}

func TestLoginFailuresAndRateLimit(t *testing.T) {
	s, h, _ := testServer(t)
	newAdmin(t, s, "correct horse battery", false)
	a := login(h, "admin", "wrong password!", false, false)
	b := login(h, "nobody", "wrong password!", false, false)
	if a.Code != 401 || b.Code != 401 || decodeError(t, a).Message != decodeError(t, b).Message {
		t.Errorf("【安全】用户名不存在与密码错误应返回相同结果：%d %d", a.Code, b.Code)
	}
	for i := 0; i < 3; i++ {
		login(h, "admin", "wrong password!", false, false)
	}
	// 已失败 5 次：即使密码正确也被锁定（设计 17.4）
	rec := login(h, "admin", "correct horse battery", false, false)
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" {
		t.Errorf("1 分钟失败 5 次后应锁定并返回 Retry-After：%d", rec.Code)
	}
	// 其他 IP 不受影响
	if rec := login(h, "admin", "correct horse battery", false, true); rec.Code != 200 {
		t.Errorf("锁定按 IP 区分：%d", rec.Code)
	}
}

func TestCSRF(t *testing.T) {
	s, h, _ := testServer(t)
	tok := adminToken(t, s)
	req := httptest.NewRequest("POST", "/api/v1/servers", strings.NewReader(`{"name":"x"}`))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("【安全】修改类请求缺少 CSRF 应返回 403：%d", rec.Code)
	}
	req.Header.Set(csrfHeader, csrfFor("ses_someothersession"))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("【安全】其他会话的 CSRF 不能使用：%d", rec.Code)
	}
	get := httptest.NewRequest("GET", "/api/v1/servers", nil)
	get.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, get)
	if rec.Code != 200 {
		t.Errorf("读取请求不需要 CSRF：%d", rec.Code)
	}
}

func TestMustChangePassword(t *testing.T) {
	s, h, _ := testServer(t)
	newAdmin(t, s, "initial-random-pw", true)
	tok := sessionFrom(t, login(h, "admin", "initial-random-pw", false, false)).Value
	other := sessionFrom(t, login(h, "admin", "initial-random-pw", false, true)).Value

	if rec := do(h, "GET", "/api/v1/servers", tok, nil); rec.Code != 403 || decodeError(t, rec).Code != CodePasswordChangeRequired {
		t.Errorf("使用初始密码时其他接口应要求先修改密码：%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/v1/auth/me", tok, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"must_change_password":true`) {
		t.Errorf("/auth/me 应可访问并提示修改密码：%d %s", rec.Code, rec.Body)
	}
	for _, c := range []struct {
		name, body, field string
	}{
		{"当前密码错误", `{"current_password":"nope","new_password":"a brand new password"}`, "current_password"},
		{"新密码太短", `{"current_password":"initial-random-pw","new_password":"short"}`, "new_password"},
		{"新旧相同", `{"current_password":"initial-random-pw","new_password":"initial-random-pw"}`, "new_password"},
	} {
		rec := do(h, "POST", "/api/v1/auth/password", tok, []byte(c.body))
		if rec.Code != 422 || decodeError(t, rec).Details[0].Field != c.field {
			t.Errorf("%s：%d %s", c.name, rec.Code, rec.Body)
		}
	}
	if rec := do(h, "POST", "/api/v1/auth/password", tok, []byte(`{"current_password":"initial-random-pw","new_password":"a brand new password"}`)); rec.Code != 204 {
		t.Fatalf("修改密码应成功：%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/v1/servers", tok, nil); rec.Code != 200 {
		t.Errorf("修改后当前会话可正常使用：%d", rec.Code)
	}
	if rec := do(h, "GET", "/api/v1/auth/me", other, nil); rec.Code != 401 {
		t.Errorf("【安全】修改密码后其他会话应全部失效（设计 17.4）：%d", rec.Code)
	}
	if rec := login(h, "admin", "a brand new password", false, false); rec.Code != 200 {
		t.Errorf("新密码应能登录：%d", rec.Code)
	}
}

func TestSessionExpiryAndLogout(t *testing.T) {
	s, h, _ := testServer(t)
	tok := adminToken(t, s)
	s.store.DB.Exec(`UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?`, time.Now().Add(-13*time.Hour).Unix(), HashToken(tok))
	if rec := do(h, "GET", "/api/v1/servers", tok, nil); rec.Code != 401 {
		t.Errorf("空闲超过 12 小时应失效（设计 17.1）：%d", rec.Code)
	}
	tok = adminToken(t, s)
	s.store.DB.Exec(`UPDATE sessions SET expires_at = ? WHERE token_hash = ?`, time.Now().Add(-time.Second).Unix(), HashToken(tok))
	if rec := do(h, "GET", "/api/v1/servers", tok, nil); rec.Code != 401 {
		t.Errorf("超过最长有效期应失效：%d", rec.Code)
	}
	tok = adminToken(t, s)
	if rec := do(h, "POST", "/api/v1/auth/logout", tok, nil); rec.Code != 204 {
		t.Fatalf("退出应返回 204：%d", rec.Code)
	}
	if rec := do(h, "GET", "/api/v1/servers", tok, nil); rec.Code != 401 {
		t.Errorf("退出后会话应失效：%d", rec.Code)
	}
	var n int
	s.store.PruneSessions(time.Now())
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	if n != 0 {
		t.Errorf("过期会话应被清理：%d", n)
	}
}

func TestReauthForDelete(t *testing.T) {
	s, h, _ := testServer(t)
	newAdmin(t, s, "correct horse battery", false)
	tok := sessionFrom(t, login(h, "admin", "correct horse battery", false, false)).Value
	_, v, _ := createNode(t, h, tok, `{"name":"to-delete"}`)
	path := "/api/v1/servers/" + itoa(v.ServerID)

	if rec := do(h, "DELETE", path, tok, nil); rec.Code != 403 || decodeError(t, rec).Code != CodeReauthRequired {
		t.Fatalf("【安全】未重新验证时删除应返回 reauth_required（设计 17.4）：%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/v1/auth/reauth", tok, []byte(`{"password":"wrong"}`)); rec.Code != 422 {
		t.Errorf("密码错误应返回 422：%d", rec.Code)
	}
	if rec := do(h, "POST", "/api/v1/auth/reauth", tok, []byte(`{"password":"correct horse battery"}`)); rec.Code != 204 {
		t.Fatalf("重新验证应成功：%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "DELETE", path, tok, nil); rec.Code != 204 {
		t.Errorf("重新验证后 10 分钟内可删除：%d %s", rec.Code, rec.Body)
	}
}

// 本地 CLI 重置密码：吊销该账号全部会话，并要求首次登录修改（设计 17.2）。
func TestResetPasswordRevokesSessions(t *testing.T) {
	s, h, _ := testServer(t)
	tok := adminToken(t, s)
	hash, _ := HashPassword("reset-random-pw")
	created, err := s.store.SetAdminPassword("admin", hash, true, time.Now())
	if err != nil || created {
		t.Fatalf("应重置已有账号：%v %v", created, err)
	}
	if rec := do(h, "GET", "/api/v1/auth/me", tok, nil); rec.Code != 401 {
		t.Errorf("重置后旧会话应失效：%d", rec.Code)
	}
	if rec := login(h, "admin", "reset-random-pw", false, false); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"must_change_password":true`) {
		t.Errorf("重置后应可登录并要求修改密码：%d %s", rec.Code, rec.Body)
	}
}
