package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// RFC 6238 附录 B 的 SHA1 测试向量（取后 6 位）。
func TestTOTPVectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037"} {
		if got := totpAt(secret, unix/totpPeriod); got != want {
			t.Errorf("T=%d：%s，应为 %s", unix, got, want)
		}
	}
	now := time.Unix(1111111111, 0)
	cur := now.Unix() / totpPeriod
	// 前后各一个时间片有效，再远无效
	for d, want := range map[int64]bool{-2: false, -1: true, 0: true, 1: true, 2: false} {
		if _, ok := totpMatch(secret, totpAt(secret, cur+d), now, 0); ok != want {
			t.Errorf("偏移 %d 个时间片：%v，应为 %v", d, ok, want)
		}
	}
	// 已用过的时间片不能再用（防重放）
	if _, ok := totpMatch(secret, totpAt(secret, cur), now, cur); ok {
		t.Error("已用过的时间片不应再次通过")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		if _, ok := totpMatch(secret, bad, now, 0); ok {
			t.Errorf("%q 不应通过", bad)
		}
	}
	for in, want := range map[string]string{"ABCD-EFGH": "abcd-efgh", " abcd efgh ": "abcd-efgh", "abcdefgh": "abcd-efgh", "abc": ""} {
		if got := normalizeRecovery(in); got != want {
			t.Errorf("normalizeRecovery(%q) = %q，应为 %q", in, got, want)
		}
	}
	seen := map[string]bool{}
	for range 50 {
		c := newRecoveryCode()
		if len(c) != 9 || c[4] != '-' || normalizeRecovery(c) != c || seen[c] {
			t.Fatalf("恢复码格式不对或重复：%q", c)
		}
		seen[c] = true
	}
}

// loginTOTP 以与 login() 相同的来源 IP 提交第二步。
func loginTOTP(h http.Handler, challenge, code string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]string{"challenge": challenge, "code": code})
	req := httptest.NewRequest("POST", "/api/v1/auth/login/totp", strings.NewReader(string(b)))
	req.RemoteAddr = "198.51.100.20:5555"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	checkContract("POST", "/api/v1/auth/login/totp", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Bytes())
	return rec
}

// 开启（需重新验证密码）→ 登录需要第二步 → 验证码 / 恢复码 / 重放 / 关闭。
func TestTOTPFlow(t *testing.T) {
	s, h, _ := testServer(t)
	tok := adminToken(t, s)
	const pw = "correct horse battery"

	// 未重新验证密码时不能开始设置（adminToken 默认已重新验证，这里先清掉）
	s.store.DB.Exec(`UPDATE sessions SET reauth_until = 0`)
	if rec := do(h, "POST", "/api/v1/auth/totp/setup", tok, nil); rec.Code != 403 {
		t.Fatalf("未重新验证时应为 403，得到 %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/v1/auth/reauth", tok, []byte(`{"password":"`+pw+`"}`)); rec.Code != 204 {
		t.Fatal(rec.Code)
	}
	// 没有开始设置就确认：409
	if rec := do(h, "POST", "/api/v1/auth/totp/enable", tok, []byte(`{"code":"123456"}`)); rec.Code != 409 {
		t.Fatalf("没有进行中的设置应为 409，得到 %d", rec.Code)
	}
	rec := do(h, "POST", "/api/v1/auth/totp/setup", tok, nil)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	var setup struct {
		Secret string `json:"secret"`
		URI    string `json:"otpauth_uri"`
	}
	json.Unmarshal(rec.Body.Bytes(), &setup)
	key, err := b32.DecodeString(setup.Secret)
	if err != nil || len(key) != 20 || !strings.HasPrefix(setup.URI, "otpauth://totp/VPS%20Monitor:admin?") || !strings.Contains(setup.URI, "secret="+setup.Secret) {
		t.Fatalf("密钥或二维码地址不对：%+v", setup)
	}
	// 设置完成前登录不受影响
	if rec := login(h, "admin", pw, false, false); rec.Code != 200 || strings.Contains(rec.Body.String(), "challenge") {
		t.Fatalf("尚未开启时应直接登录：%d %s", rec.Code, rec.Body)
	}
	// 错误的验证码
	if rec := do(h, "POST", "/api/v1/auth/totp/enable", tok, []byte(`{"code":"000000"}`)); rec.Code != 422 {
		t.Fatalf("错误验证码应为 422，得到 %d", rec.Code)
	}
	now := time.Now()
	rec = do(h, "POST", "/api/v1/auth/totp/enable", tok, []byte(`{"code":"`+totpAt(key, now.Unix()/totpPeriod)+`"}`))
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	var enabled struct {
		Codes []string `json:"recovery_codes"`
	}
	json.Unmarshal(rec.Body.Bytes(), &enabled)
	if len(enabled.Codes) != 10 {
		t.Fatalf("应返回 10 个恢复码：%v", enabled.Codes)
	}
	// 恢复码只存哈希
	var n int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM totp_recovery WHERE code_hash = ?`, enabled.Codes[0]).Scan(&n)
	if n != 0 {
		t.Fatal("恢复码不应保存原文")
	}
	var me meView
	json.Unmarshal(do(h, "GET", "/api/v1/auth/me", tok, nil).Body.Bytes(), &me)
	if !me.TOTPEnabled || me.RecoveryCodesLeft != 10 {
		t.Fatalf("登录信息应显示已开启：%+v", me)
	}
	// 已开启时再次设置：409
	if rec := do(h, "POST", "/api/v1/auth/totp/setup", tok, nil); rec.Code != 409 {
		t.Fatalf("已开启时应为 409，得到 %d", rec.Code)
	}

	challenge := func() string {
		t.Helper()
		rec := login(h, "admin", pw, true, false)
		var c struct {
			Required  bool   `json:"totp_required"`
			Challenge string `json:"challenge"`
		}
		json.Unmarshal(rec.Body.Bytes(), &c)
		if rec.Code != 200 || !c.Required || c.Challenge == "" || len(rec.Result().Cookies()) != 0 {
			t.Fatalf("开启后密码正确应只返回 challenge、不写 Cookie：%d %s", rec.Code, rec.Body)
		}
		return c.Challenge
	}

	// 启用时用过的验证码（同一时间片）不能再用来登录
	c := challenge()
	if rec := loginTOTP(h, c, totpAt(key, now.Unix()/totpPeriod)); rec.Code != 401 {
		t.Fatalf("重放启用时的验证码应为 401，得到 %d", rec.Code)
	}
	// 下一个时间片的验证码可以登录
	next := totpAt(key, now.Unix()/totpPeriod+1)
	rec = loginTOTP(h, c, next)
	if rec.Code != 200 || sessionFrom(t, rec) == nil {
		t.Fatalf("正确验证码应登录：%d %s", rec.Code, rec.Body)
	}
	// challenge 只能用一次；同一验证码再登录也不行
	if rec := loginTOTP(h, c, next); rec.Code != 401 {
		t.Fatalf("challenge 用过后应为 401，得到 %d", rec.Code)
	}
	if rec := loginTOTP(h, challenge(), next); rec.Code != 401 {
		t.Fatalf("同一验证码不能登录两次，得到 %d", rec.Code)
	}

	// 恢复码：格式宽松，用过即失效
	rec = loginTOTP(h, challenge(), strings.ToUpper(strings.ReplaceAll(enabled.Codes[0], "-", " ")))
	if rec.Code != 200 {
		t.Fatalf("恢复码应能登录：%d %s", rec.Code, rec.Body)
	}
	if rec := loginTOTP(h, challenge(), enabled.Codes[0]); rec.Code != 401 {
		t.Fatalf("用过的恢复码应为 401，得到 %d", rec.Code)
	}
	json.Unmarshal(do(h, "GET", "/api/v1/auth/me", tok, nil).Body.Bytes(), &me)
	if me.RecoveryCodesLeft != 9 {
		t.Fatalf("剩余恢复码应为 9：%+v", me)
	}

	// challenge 绑定 IP
	c = challenge()
	b, _ := json.Marshal(map[string]string{"challenge": c, "code": enabled.Codes[1]})
	req := httptest.NewRequest("POST", "/api/v1/auth/login/totp", strings.NewReader(string(b)))
	req.RemoteAddr = "203.0.113.9:5555"
	other := httptest.NewRecorder()
	h.ServeHTTP(other, req)
	if other.Code != 401 {
		t.Fatalf("其他 IP 使用 challenge 应为 401，得到 %d", other.Code)
	}

	// 审计：第二步成功记录方式
	if logs := do(h, "GET", "/api/v1/audit-logs", tok, nil).Body.String(); !strings.Contains(logs, `"second_factor":"recovery"`) ||
		!strings.Contains(logs, "auth.totp_enable") {
		t.Fatalf("操作日志缺少两步验证记录：%s", logs)
	}

	// 关闭：需要重新验证（这里仍在 10 分钟内）
	if rec := do(h, "POST", "/api/v1/auth/totp/disable", tok, nil); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body)
	}
	if rec := login(h, "admin", pw, false, false); rec.Code != 200 || strings.Contains(rec.Body.String(), "challenge") {
		t.Fatalf("关闭后应直接登录：%d %s", rec.Code, rec.Body)
	}
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM totp_recovery`).Scan(&n)
	if n != 0 {
		t.Fatal("关闭后应删除恢复码")
	}
}

// 第二步连续失败：每个 challenge 最多 5 次，并计入同一 IP 的登录失败锁定。
func TestTOTPChallengeLimits(t *testing.T) {
	s, h, _ := testServer(t)
	adminToken(t, s)
	key := []byte("12345678901234567890")
	u, _ := s.store.UserByName("admin")
	if err := s.store.EnableTOTP(u.ID, b32.EncodeToString(key), 0, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	var c struct {
		Challenge string `json:"challenge"`
	}
	json.Unmarshal(login(h, "admin", "correct horse battery", false, false).Body.Bytes(), &c)
	codes := 0
	for i := range 6 {
		rec := loginTOTP(h, c.Challenge, "000000")
		codes = rec.Code
		if i < 4 && rec.Code != 401 {
			t.Fatalf("第 %d 次应为 401，得到 %d", i+1, rec.Code)
		}
	}
	// 5 次失败后同一 IP 被锁定（与密码登录共用计数）
	if codes != 429 {
		t.Fatalf("连续失败后应被限流，最后一次为 %d", codes)
	}
	// 即使之后输入正确验证码，这个 challenge 也已作废
	s.loginLimit = newEnrollLimiter()
	s.loginLimit.perMinute, s.loginLimit.maxFails = 100, 100
	if rec := loginTOTP(h, c.Challenge, totpAt(key, time.Now().Unix()/totpPeriod)); rec.Code != 401 {
		t.Fatalf("尝试次数用完后 challenge 应作废，得到 %d", rec.Code)
	}
}
