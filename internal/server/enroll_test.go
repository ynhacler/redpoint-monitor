package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// createNode 通过接口新建节点，返回响应。
func createNode(t *testing.T, h http.Handler, admin string, body string) (int, enrollCodeView, errorPayload) {
	t.Helper()
	rec := do(h, "POST", "/api/v1/servers", admin, []byte(body))
	var v enrollCodeView
	var e errorPayload
	if rec.Code == http.StatusCreated {
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
	} else {
		e = decodeError(t, rec)
	}
	return rec.Code, v, e
}

// enroll 模拟 Agent 注册，返回状态码与响应。
func enroll(h http.Handler, code, hostname, machine string) (*httptest.ResponseRecorder, enrollResponse) {
	b, _ := json.Marshal(enrollBody{EnrollCode: code, Hostname: hostname, MachineIDHash: machine,
		OS: "ubuntu", OSVersion: "24.04", Arch: "amd64", AgentVersion: "v-test"})
	req := httptest.NewRequest("POST", "/api/v1/agent/enroll", strings.NewReader(string(b)))
	req.RemoteAddr = "203.0.113.7:4000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var res enrollResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	return rec, res
}

// adminToken 创建管理员账号（如尚无）并登录，返回会话令牌；已通过重新验证，可执行删除等敏感操作。
func adminToken(t *testing.T, s *Server) string {
	t.Helper()
	if n, _ := s.store.UserCount(); n == 0 {
		h, _ := HashPassword("correct horse battery")
		if _, err := s.store.SetAdminPassword("admin", h, false, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	u, err := s.store.UserByName("admin")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := s.store.CreateSession(u.ID, "127.0.0.1", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	se, _ := s.store.LookupSession(tok, time.Now())
	s.store.SetReauth(se.ID, time.Now().Add(reauthValid))
	return tok
}

func TestCreateServerValidation(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	cases := []struct {
		name, body string
		status     int
		field      string
	}{
		{"缺少名称", `{}`, 422, "name"},
		{"IPv4 格式错误", `{"name":"a","expected_ipv4":"300.1.1.1"}`, 422, "expected_ipv4"},
		{"IPv6 填成了 IPv4", `{"name":"a","expected_ipv6":"1.2.3.4"}`, 422, "expected_ipv6"},
		{"重置日超出范围", `{"name":"a","traffic_reset_day":32}`, 422, "traffic_reset_day"},
		{"计费模式错误", `{"name":"a","traffic_count_mode":"both"}`, 422, "traffic_count_mode"},
		{"带宽为负", `{"name":"a","bandwidth_mbps":-1}`, 422, "bandwidth_mbps"},
		{"有价格没币种", `{"name":"a","price":4.99}`, 422, "currency"},
		{"到期日期格式错误", `{"name":"a","expire_date":"2026/12/31"}`, 422, "expire_date"},
		{"续费周期错误", `{"name":"a","billing_period":"weekly"}`, 422, "billing_period"},
		{"注册码有效期错误", `{"name":"a","enroll_ttl":"30d"}`, 422, "enroll_ttl"},
		{"严格程度错误", `{"name":"a","verify_mode":"block"}`, 422, "verify_mode"},
		{"未知字段（写错字段名）", `{"name":"a","hostnmae":"x"}`, 400, ""},
		{"主机名含非法字符", `{"name":"a","expected_hostname":"a;rm -rf /"}`, 422, "expected_hostname"},
	}
	for _, c := range cases {
		code, _, e := createNode(t, h, admin, c.body)
		if code != c.status {
			t.Errorf("%s：状态码 %d，应为 %d", c.name, code, c.status)
			continue
		}
		if c.field != "" && (len(e.Details) == 0 || e.Details[0].Field != c.field) {
			t.Errorf("%s：details 应指向字段 %s：%+v", c.name, c.field, e.Details)
		}
	}
	if code, _, _ := createNode(t, h, admin, `{"name":"dup"}`); code != 201 {
		t.Fatalf("创建失败：%d", code)
	}
	if code, _, e := createNode(t, h, admin, `{"name":"dup"}`); code != 409 || e.Code != CodeConflict {
		t.Errorf("名称重复应返回 409 conflict：%d %s", code, e.Code)
	}
}

func TestCreateServerAndInstallCommand(t *testing.T) {
	s, h, _ := testServer(t)
	s.publicURL = "https://panel.example.com"
	admin := adminToken(t, s)
	code, v, _ := createNode(t, h, admin, `{"name":"DMIT-HK","expected_hostname":"dmit-hk","expected_ipv4":"103.1.2.3",
		"group":"香港","provider":"DMIT","plan":"PVM.HKG.Pro","region":"HK","traffic_limit_gb":1000,"traffic_reset_day":15,
		"traffic_count_mode":"max","price":36.9,"currency":"usd","billing_period":"quarterly","expire_date":"2026-12-31"}`)
	if code != 201 {
		t.Fatalf("新建节点应返回 201，实际 %d", code)
	}
	if !enrollCodeFormat.MatchString(v.EnrollCode) {
		t.Errorf("注册码格式不正确：%q", v.EnrollCode)
	}
	want := "sudo vpsmon-agent install --server https://panel.example.com --enroll " + v.EnrollCode
	if v.Install.Command != want {
		t.Errorf("安装命令 = %q\n应为       %q", v.Install.Command, want)
	}
	if strings.Contains(v.Install.Command, "|") || v.Install.Mode != "manual" {
		t.Errorf("【安全】没有已验签版本时只提供手动方式，且不得使用管道（设计 27.3）：%+v", v.Install)
	}
	if exp := time.Unix(v.EnrollExpiresAt, 0); exp.Before(time.Now().Add(23*time.Hour)) || exp.After(time.Now().Add(25*time.Hour)) {
		t.Errorf("默认有效期应为 24 小时：%v", exp)
	}

	n, err := s.store.GetServer(v.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	if n.EnrollState != enrollPending || n.LimitBytes != 1_000_000_000_000 || n.ResetDay != 15 || n.CountMode != "max" ||
		n.PriceCents != 3690 || n.Currency != "USD" || n.BillingPeriod != "quarterly" || n.Group != "香港" {
		t.Errorf("节点信息保存不正确：%+v", n)
	}

	// 列表中显示为“待安装”（设计 27.7）
	rec := do(h, "GET", "/api/v1/servers", admin, nil)
	if !strings.Contains(rec.Body.String(), `"status":"pending"`) {
		t.Errorf("新节点应显示为 pending：%s", rec.Body)
	}

	// 再次查看安装命令：不返回完整注册码（设计 19.11）
	rec = do(h, "GET", "/api/v1/servers/"+itoa(v.ServerID)+"/install-command", admin, nil)
	if strings.Contains(rec.Body.String(), v.EnrollCode) {
		t.Errorf("【安全】install-command 不得返回已生成过的完整注册码：%s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"enroll_status":"ACTIVE"`) || !strings.Contains(rec.Body.String(), v.EnrollCodeHint) {
		t.Errorf("应返回注册码提示与状态：%s", rec.Body)
	}
	if rec := do(h, "GET", "/api/v1/servers/999/install-command", admin, nil); rec.Code != 404 {
		t.Errorf("不存在的节点应返回 404：%d", rec.Code)
	}
}

func TestEnrollFlow(t *testing.T) {
	s, h, logs := testServer(t)
	admin := adminToken(t, s)
	_, v, _ := createNode(t, h, admin, `{"name":"jp-store","expected_hostname":"jp-store"}`)

	// 1. 正常注册：签发 Token，节点变为已注册
	rec, res := enroll(h, strings.ToLower(v.EnrollCode), "jp-store", "sha256:aaa") // 小写输入也接受
	if rec.Code != 200 || !strings.HasPrefix(res.AgentToken, PrefixAgent) || res.ServerID != v.ServerID {
		t.Fatalf("注册应成功：%d %s", rec.Code, rec.Body)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("信息一致时不应有警告：%v", res.Warnings)
	}
	if rec := do(h, "POST", "/api/v1/agent/report", res.AgentToken, []byte(`{"system":{"boot_id":"b"}}`)); rec.Code != 204 {
		t.Errorf("签发的 Token 应能上报：%d", rec.Code)
	}
	if n, _ := s.store.GetServer(v.ServerID); n.EnrollState != enrollEnrolled || n.Hostname != "jp-store" || n.IPv4 != "203.0.113.7" {
		t.Errorf("注册后应记录实际主机名与来源 IP：%+v", n)
	}

	// 2. 另一台主机拿同一个注册码：一次性，拒绝（设计 27.4）
	if rec, _ := enroll(h, v.EnrollCode, "evil", "sha256:bbb"); rec.Code != 400 {
		t.Errorf("已使用的注册码应被拒绝：%d", rec.Code)
	} else if e := decodeError(t, rec); e.Code != CodeEnrollCodeInvalid {
		t.Errorf("code = %q", e.Code)
	}

	// 3. 同一主机 10 分钟内重试：视为重试，签发新 Token 并吊销上一个（设计 27.6.4）
	rec, res2 := enroll(h, v.EnrollCode, "jp-store", "sha256:aaa")
	if rec.Code != 200 || res2.AgentToken == res.AgentToken {
		t.Fatalf("重试应成功并签发新 Token：%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/v1/agent/report", res.AgentToken, []byte(`{}`)); rec.Code != 401 {
		t.Errorf("重试后旧 Token 应被吊销：%d", rec.Code)
	}

	// 4. 【安全】日志与审计中都不得出现完整注册码或 Token（设计 24.7）
	var details string
	rows, _ := s.store.DB.Query(`SELECT action || ' ' || result || ' ' || details FROM audit_logs`)
	for rows.Next() {
		var d string
		rows.Scan(&d)
		details += d + "\n"
	}
	rows.Close()
	for _, secret := range []string{v.EnrollCode, res.AgentToken, res2.AgentToken} {
		if strings.Contains(details, secret) || strings.Contains(logs.String(), secret) {
			t.Errorf("审计或日志中出现了完整凭证 %s", secret[:8])
		}
	}
	for _, want := range []string{"server.create success", "agent.enroll success", "agent.enroll failure"} {
		if !strings.Contains(details, want) {
			t.Errorf("审计日志缺少 %q：\n%s", want, details)
		}
	}
}

func TestEnrollRejections(t *testing.T) {
	s, h, _ := testServer(t)
	s.enrollLimit.perMinute = 1000 // 本组用例都来自同一 IP；限流在 TestEnrollRateLimit 中单独测试
	admin := adminToken(t, s)

	t.Run("格式错误与不存在的注册码返回同一个错误（设计 27.6.2）", func(t *testing.T) {
		for _, c := range []string{"", "hello", "ENR-0000-0000-0000-0000"} {
			rec, _ := enroll(h, c, "x", "m")
			if rec.Code != 400 || decodeError(t, rec).Code != CodeEnrollCodeInvalid {
				t.Errorf("%q：%d %s", c, rec.Code, rec.Body)
			}
		}
	})

	t.Run("过期的注册码", func(t *testing.T) {
		_, v, _ := createNode(t, h, admin, `{"name":"expired"}`)
		s.store.DB.Exec(`UPDATE enroll_codes SET expires_at = ? WHERE server_id = ?`, time.Now().Add(-time.Second).Unix(), v.ServerID)
		if rec, _ := enroll(h, v.EnrollCode, "x", "m"); rec.Code != 400 {
			t.Errorf("过期的注册码应被拒绝：%d", rec.Code)
		}
		rec := do(h, "GET", "/api/v1/servers/"+itoa(v.ServerID)+"/install-command", admin, nil)
		if !strings.Contains(rec.Body.String(), `"enroll_status":"EXPIRED"`) {
			t.Errorf("过期后应显示 EXPIRED：%s", rec.Body)
		}
	})

	t.Run("重新生成后旧码失效，新码可用（设计 27.4）", func(t *testing.T) {
		_, v, _ := createNode(t, h, admin, `{"name":"regen"}`)
		rec := do(h, "POST", "/api/v1/servers/"+itoa(v.ServerID)+"/enroll-code", admin, []byte(`{"enroll_ttl":"1h"}`))
		var nv enrollCodeView
		json.Unmarshal(rec.Body.Bytes(), &nv)
		if rec.Code != 200 || nv.EnrollCode == "" || nv.EnrollCode == v.EnrollCode {
			t.Fatalf("重新生成失败：%d %s", rec.Code, rec.Body)
		}
		if rec, _ := enroll(h, v.EnrollCode, "x", "m"); rec.Code != 400 {
			t.Errorf("旧注册码应失效：%d", rec.Code)
		}
		if rec, _ := enroll(h, nv.EnrollCode, "x", "m"); rec.Code != 200 {
			t.Errorf("新注册码应可用：%d %s", rec.Code, rec.Body)
		}
	})

	t.Run("撤销后不可用", func(t *testing.T) {
		_, v, _ := createNode(t, h, admin, `{"name":"revoke"}`)
		if rec := do(h, "DELETE", "/api/v1/servers/"+itoa(v.ServerID)+"/enroll-code", admin, nil); rec.Code != 204 {
			t.Fatalf("撤销应返回 204：%d", rec.Code)
		}
		if rec, _ := enroll(h, v.EnrollCode, "x", "m"); rec.Code != 400 {
			t.Errorf("撤销后的注册码应被拒绝：%d", rec.Code)
		}
	})

	t.Run("仅提示模式：不一致时注册成功并给出警告（设计 27.6.3）", func(t *testing.T) {
		_, v, _ := createNode(t, h, admin, `{"name":"warn","expected_hostname":"hk-1","expected_ipv4":"198.51.100.1"}`)
		rec, res := enroll(h, v.EnrollCode, "other-host", "m")
		if rec.Code != 200 || len(res.Warnings) != 2 {
			t.Errorf("应注册成功并给出主机名与 IP 两条警告：%d %v", rec.Code, res.Warnings)
		}
	})

	t.Run("严格模式：不一致时拒绝，注册码保持可用（设计 27.6.3）", func(t *testing.T) {
		_, v, _ := createNode(t, h, admin, `{"name":"strict","expected_hostname":"hk-1","verify_mode":"strict"}`)
		rec, _ := enroll(h, v.EnrollCode, "other-host", "m")
		if rec.Code != 403 || !strings.Contains(decodeError(t, rec).Message, "主机名不一致") {
			t.Errorf("严格模式应拒绝并说明原因：%d %s", rec.Code, rec.Body)
		}
		if rec, _ := enroll(h, v.EnrollCode, "HK-1", "m"); rec.Code != 200 {
			t.Errorf("主机名一致（忽略大小写）后应能用同一注册码注册：%d %s", rec.Code, rec.Body)
		}
	})

	t.Run("重装 / 更换主机：新主机注册后吊销旧 Token（设计 27.8）", func(t *testing.T) {
		_, v, _ := createNode(t, h, admin, `{"name":"reinstall"}`)
		_, old := enroll(h, v.EnrollCode, "a", "sha256:old")
		rec := do(h, "POST", "/api/v1/servers/"+itoa(v.ServerID)+"/enroll-code", admin, nil)
		var nv enrollCodeView
		json.Unmarshal(rec.Body.Bytes(), &nv)
		if nv.EnrollState != enrollEnrolled {
			t.Errorf("已注册节点重新生成注册码后仍为 enrolled：%q", nv.EnrollState)
		}
		if rec, _ := enroll(h, nv.EnrollCode, "b", "sha256:new"); rec.Code != 200 {
			t.Fatalf("新主机注册失败：%d", rec.Code)
		}
		if rec := do(h, "POST", "/api/v1/agent/report", old.AgentToken, []byte(`{}`)); rec.Code != 401 {
			t.Errorf("旧主机的 Token 应被吊销：%d", rec.Code)
		}
	})
}

func TestUnregister(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, v, _ := createNode(t, h, admin, `{"name":"bye"}`)
	_, res := enroll(h, v.EnrollCode, "bye", "m")
	if rec := do(h, "POST", "/api/v1/agent/unregister", res.AgentToken, nil); rec.Code != 204 {
		t.Fatalf("注销应返回 204：%d %s", rec.Code, rec.Body)
	}
	if n, _ := s.store.GetServer(v.ServerID); n.EnrollState != enrollPending {
		t.Errorf("卸载后节点应回到待安装（设计 27.11）：%q", n.EnrollState)
	}
	if rec := do(h, "POST", "/api/v1/agent/report", res.AgentToken, []byte(`{}`)); rec.Code != 401 {
		t.Errorf("卸载后 Token 应被吊销：%d", rec.Code)
	}
}

func TestEnrollRateLimit(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	l := newEnrollLimiter()
	l.now = func() time.Time { return now }
	for i := 0; i < 10; i++ {
		if ok, _ := l.allow("1.1.1.1"); !ok {
			t.Fatalf("第 %d 次应放行", i+1)
		}
	}
	if ok, wait := l.allow("1.1.1.1"); ok || wait <= 0 {
		t.Error("每分钟第 11 次应被限流，并给出等待时间（设计 27.6.5）")
	}
	if ok, _ := l.allow("2.2.2.2"); !ok {
		t.Error("限流按 IP 区分")
	}
	now = now.Add(time.Minute)
	if ok, _ := l.allow("1.1.1.1"); !ok {
		t.Error("下一分钟应恢复")
	}
	for i := 0; i < 20; i++ {
		l.fail("3.3.3.3")
	}
	if ok, wait := l.allow("3.3.3.3"); ok || wait < 29*time.Minute {
		t.Errorf("失败 20 次后应封禁 30 分钟：ok=%v wait=%v", ok, wait)
	}
	now = now.Add(31 * time.Minute)
	if ok, _ := l.allow("3.3.3.3"); !ok {
		t.Error("封禁到期后应恢复")
	}

	// 接口层：第 11 次返回 429 与 Retry-After
	_, h, _ := testServer(t)
	var rec *httptest.ResponseRecorder
	for i := 0; i < 11; i++ {
		rec, _ = enroll(h, "ENR-0000-0000-0000-0000", "x", "m")
	}
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" {
		t.Errorf("超过限流应返回 429 与 Retry-After：%d %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestClientIPAndPanelURL(t *testing.T) {
	cases := []struct {
		name, remote, xff, want string
	}{
		{"直连", "203.0.113.7:1234", "", "203.0.113.7"},
		{"直连时忽略伪造的 X-Forwarded-For", "203.0.113.7:1234", "1.2.3.4", "203.0.113.7"},
		{"经回环代理：取代理追加的最后一项", "127.0.0.1:5555", "6.6.6.6, 198.51.100.9", "198.51.100.9"},
		{"IPv6 回环代理", "[::1]:5555", "2001:db8::1", "2001:db8::1"},
		{"代理传来的值不是 IP 时退回对端地址", "127.0.0.1:5555", "garbage", "127.0.0.1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientIP(r); got != c.want {
			t.Errorf("%s：clientIP = %q，应为 %q", c.name, got, c.want)
		}
	}

	s, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr, r.Host = "127.0.0.1:5555", "45-66-129-102.sslip.io"
	r.Header.Set("X-Forwarded-For", "198.51.100.9")
	r.Header.Set("X-Forwarded-Proto", "https")
	if got := s.panelURL(r); got != "https://45-66-129-102.sslip.io" {
		t.Errorf("Caddy 后面应推断出 https 对外地址：%q", got)
	}
	r.Host = "evil.com;rm -rf /"
	if got := s.panelURL(r); strings.ContainsAny(got, "; ") {
		t.Errorf("【安全】Host 头中的特殊字符不得进入安装命令：%q", got)
	}
	s.publicURL = "https://panel.example.com"
	if got := s.panelURL(r); got != "https://panel.example.com" {
		t.Errorf("配置了 --public-url 时优先使用：%q", got)
	}
}

// 权限矩阵：枚举所有路由 × 所有凭证类型，校验允许与拒绝与设计 17.2 一致（设计 17.5）。
func TestPermissionMatrix(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, agentTok, _ := s.store.CreateServer("matrix", 0, 1)
	apiKey, err := s.store.CreateAPIKey(&APIKey{Name: "matrix", ScopeType: "all"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	creds := map[string]string{"无凭证": "", "伪造": "agt_forgedforgedforged", "伪造 API Key": "api_forgedforgedforged",
		"伪造设备": "dev_forgedforgedforged", "Refresh Token": "rt_forgedforgedforged",
		"admin": admin, "agent": agentTok, "apikey": apiKey, "app": "", "app 无低风险操作": ""}
	everyone := []string{"无凭证", "伪造", "伪造 API Key", "伪造设备", "Refresh Token", "admin", "agent", "apikey", "app", "app 无低风险操作"}
	allowed := map[access][]string{
		accessPublic: everyone,
		accessEnroll: everyone, // 凭请求体中的注册码认证
		accessPair:   everyone, // 凭请求体中的 AK / Refresh Token 认证
		accessAdmin:  {"admin"},
		// 【安全】API Key 与 App 设备只能访问读取类接口（设计 45.2、8.4.1）
		accessRead:  {"admin", "apikey", "app", "app 无低风险操作"},
		accessAgent: {"agent"},
		accessApp:   {"app", "app 无低风险操作"},
		// 【安全】静音与维护：只有 AK 允许低风险操作的设备（设计 17.2）
		accessOps: {"admin", "app"},
	}
	// 期望表独立于实现，按设计 17.2 手写：路由声明的主体必须与之完全一致。
	// 新增路由时必须同时在这里登记，否则测试失败——防止误把管理接口声明为公开。
	want := map[string]access{
		"GET /healthz":                                    accessPublic,
		"GET /releases/{version}/{file}":                  accessPublic,
		"POST /api/v1/auth/login":                         accessPublic,
		"GET /api/v1/auth/captcha":                        accessPublic,
		"GET /api/v1/auth/me":                             accessAdmin,
		"POST /api/v1/auth/logout":                        accessAdmin,
		"POST /api/v1/auth/password":                      accessAdmin,
		"POST /api/v1/auth/reauth":                        accessAdmin,
		"GET /api/v1/auth/sessions":                       accessAdmin,
		"DELETE /api/v1/auth/sessions/{id}":               accessAdmin,
		"GET /ws":                                         accessRead,
		"GET /api/v1/api-keys":                            accessAdmin,
		"POST /api/v1/api-keys":                           accessAdmin,
		"DELETE /api/v1/api-keys/{id}":                    accessAdmin,
		"GET /api/v1/version":                             accessRead,
		"GET /api/v1/app-access-keys":                     accessAdmin,
		"POST /api/v1/app-access-keys":                    accessAdmin,
		"POST /api/v1/app-access-keys/{id}/revoke":        accessAdmin,
		"GET /api/v1/app-devices":                         accessAdmin,
		"POST /api/v1/app-devices/{id}/revoke":            accessAdmin,
		"POST /api/v1/app/pair":                           accessPair,
		"POST /api/v1/app/token/refresh":                  accessPair,
		"GET /api/v1/app/me":                              accessApp,
		"PUT /api/v1/app/push":                            accessApp,
		"DELETE /api/v1/app/push":                         accessApp,
		"POST /api/v1/app/unpair":                         accessApp,
		"GET /api/v1/cloud-accounts":                      accessAdmin,
		"POST /api/v1/cloud-accounts":                     accessAdmin,
		"PUT /api/v1/cloud-accounts/{id}":                 accessAdmin,
		"DELETE /api/v1/cloud-accounts/{id}":              accessAdmin,
		"POST /api/v1/cloud-accounts/{id}/sync":           accessAdmin,
		"GET /api/v1/cloud-accounts/{id}/costs":           accessAdmin,
		"GET /api/v1/cloud-instances":                     accessAdmin,
		"PUT /api/v1/cloud-instances/{id}/server":         accessAdmin,
		"POST /api/v1/cloud-instances/{id}/apply-expire":  accessAdmin,
		"PUT /api/v1/cloud-instances/{id}/auto-calibrate": accessAdmin,
		"POST /api/v1/agent/enroll":                       accessEnroll,
		"POST /api/v1/agent/report":                       accessAgent,
		"POST /api/v1/agent/unregister":                   accessAgent,
		"GET /api/v1/agent/upgrade":                       accessAgent,
		"POST /api/v1/agent/upgrade/status":               accessAgent,
		"GET /api/v1/audit-logs":                          accessAdmin,
		"GET /api/v1/audit-logs/export":                   accessAdmin,
		"GET /api/v1/alerts":                              accessRead,
		"GET /api/v1/alert-rules":                         accessAdmin,
		"POST /api/v1/alert-rules":                        accessAdmin,
		"POST /api/v1/alert-rules/preview":                accessAdmin,
		"PUT /api/v1/alert-rules/{id}":                    accessAdmin,
		"DELETE /api/v1/alert-rules/{id}":                 accessAdmin,
		"GET /api/v1/agent-releases":                      accessAdmin,
		"GET /api/v1/upgrade-tasks":                       accessAdmin,
		"POST /api/v1/upgrade-tasks":                      accessAdmin,
		"POST /api/v1/upgrade-tasks/{id}/cancel":          accessAdmin,
		"POST /api/v1/agent-releases/sync":                accessAdmin,
		"GET /api/v1/settings/quiet-hours":                accessAdmin,
		"PUT /api/v1/settings/quiet-hours":                accessAdmin,
		"GET /api/v1/notification-channels":               accessAdmin,
		"POST /api/v1/notification-channels":              accessAdmin,
		"PUT /api/v1/notification-channels/{id}":          accessAdmin,
		"DELETE /api/v1/notification-channels/{id}":       accessAdmin,
		"POST /api/v1/notification-channels/{id}/test":    accessAdmin,
		"GET /api/v1/notification-deliveries":             accessAdmin,
		"GET /api/v1/silences":                            accessOps,
		"POST /api/v1/silences":                           accessOps,
		"DELETE /api/v1/silences/{id}":                    accessOps,
		"GET /api/v1/servers":                             accessRead,
		"POST /api/v1/servers":                            accessAdmin,
		"GET /api/v1/servers/{id}":                        accessRead,
		"PUT /api/v1/servers/{id}":                        accessAdmin,
		"DELETE /api/v1/servers/{id}":                     accessAdmin,
		"POST /api/v1/servers/{id}/revoke-agent-token":    accessAdmin,
		"GET /api/v1/servers/{id}/metrics/history":        accessRead,
		"GET /api/v1/servers/{id}/health":                 accessRead,
		"GET /api/v1/servers/{id}/install-command":        accessAdmin,
		"POST /api/v1/servers/{id}/enroll-code":           accessAdmin,
		"DELETE /api/v1/servers/{id}/enroll-code":         accessAdmin,
		"GET /api/v1/servers/{id}/traffic/current":        accessRead,
		"GET /api/v1/servers/{id}/traffic/daily":          accessRead,
		"GET /api/v1/servers/{id}/traffic/monthly":        accessRead,
		"GET /api/v1/servers/{id}/traffic/adjustments":    accessAdmin,
		"POST /api/v1/servers/{id}/traffic/calibrate":     accessAdmin,
		"/api/": accessPublic,
		"/":     accessPublic,
	}
	got := map[string]access{}
	for _, rt := range s.routeTable {
		got[rt.pattern] = rt.access
	}
	for p, a := range want {
		if got[p] != a {
			t.Errorf("【安全】路由 %q 声明的主体为 %q，设计要求 %q（设计 17.2）", p, got[p], a)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("新路由 %q 未登记到权限矩阵期望表中", p)
		}
	}
	for _, rt := range s.routeTable {
		method, path, ok := strings.Cut(rt.pattern, " ")
		if !ok {
			method, path = "GET", rt.pattern
		}
		path = strings.ReplaceAll(path, "{id}", "1")
		for name, tok := range creds {
			if name == "admin" {
				tok = adminToken(t, s) // 每次用新会话：矩阵中的 /auth/logout 会注销当前会话
			}
			if name == "apikey" {
				// 每次用新 Key：矩阵中的 DELETE /api-keys/{id} 会吊销编号为 1 的 Key
				tok, _ = s.store.CreateAPIKey(&APIKey{Name: "matrix", ScopeType: "all"}, time.Now())
			}
			if name == "app" || name == "app 无低风险操作" {
				// 每次用新设备：矩阵中的 /app/unpair、/app-devices/{id}/revoke 会吊销设备
				tok = pairTestDevice(t, s, name == "app", "all", "").access
			}
			rec := do(h, method, path, tok, []byte(`{}`))
			if rt.pattern == "POST /api/v1/auth/login" {
				// 登录接口本身就是校验凭证：空请求体返回 401（用户名或密码错误）是业务结果，
				// 这里只要求它在期望表中声明为 public，不按状态码判断放行与否
				continue
			}
			want := contains(allowed[rt.access], name)
			if got := rec.Code != 401 && rec.Code != 403; got != want {
				t.Errorf("%s 用 %s 凭证：状态码 %d，应%s", rt.pattern, name, rec.Code, map[bool]string{true: "放行", false: "拒绝"}[want])
			}
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
