package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"vpsmon/internal/logging"
	"vpsmon/internal/protocol"
)

// testServer 创建一个使用临时数据库与内存日志的面板，返回 Server、路由与日志缓冲。
func testServer(t *testing.T) (*Server, http.Handler, *bytes.Buffer) {
	t.Helper()
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	var buf bytes.Buffer
	log, err := logging.New(&buf, "json", slog.LevelDebug)
	if err != nil {
		t.Fatal(err)
	}
	web := fstest.MapFS{"index.html": {Data: []byte("<html>spa</html>")}}
	// 关闭验证码，其他测试直接登录；验证码在 captcha_test.go 中单独测试
	s, err := New(st, web, Options{Logger: log, Version: "v-test", NoLoginCaptcha: true})
	if err != nil {
		t.Fatal(err)
	}
	return s, s.routes(), &buf
}

// do 发送请求；token 非空时带 Authorization 头。
// do 发送请求。token 为会话令牌（ses_）时放进 Cookie 并附带 CSRF（Web 管理员）；
// 其他非空值作为 Bearer（Agent Token 或伪造凭证）。
func do(h http.Handler, method, path, token string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	switch {
	case strings.HasPrefix(token, PrefixSession):
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		req.Header.Set(csrfHeader, csrfFor(token))
	case token != "":
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// decodeError 解析统一错误响应，并校验格式（设计 43.4）。
func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorPayload {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("错误响应的 Content-Type = %q，应为 application/json；body=%s", ct, rec.Body)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("错误响应不是 {\"error\": {...}} 格式：%v；body=%s", err, rec.Body)
	}
	e := body.Error
	if e.Code == "" || e.Message == "" {
		t.Errorf("code 与 message 不能为空：%+v", e)
	}
	if e.RequestID == "" || e.RequestID != rec.Header().Get("X-Request-ID") {
		t.Errorf("request_id %q 应与响应头 X-Request-ID %q 一致", e.RequestID, rec.Header().Get("X-Request-ID"))
	}
	return e
}

// 每个错误码至少一个测试：状态码、code、响应格式（设计 43.10）。
func TestEveryErrorCode(t *testing.T) {
	s, _, _ := testServer(t)
	for code, want := range codeInfo {
		t.Run(string(code), func(t *testing.T) {
			h := s.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s.writeError(w, r, errorf(code, ""))
			}))
			rec := do(h, "GET", "/x", "", nil)
			if rec.Code != want.status {
				t.Errorf("状态码 = %d，应为 %d", rec.Code, want.status)
			}
			e := decodeError(t, rec)
			if e.Code != code {
				t.Errorf("code = %q，应为 %q", e.Code, code)
			}
			if code == CodeInternal && !strings.Contains(e.Message, e.RequestID) {
				t.Errorf("internal 的提示应附带编号，便于对照日志（设计 43.9）：%q", e.Message)
			}
			if code == CodeRateLimited && rec.Header().Get("Retry-After") == "" {
				t.Error("429 应带 Retry-After（设计 43.2）")
			}
		})
	}
}

func TestValidationDetails(t *testing.T) {
	s, _, _ := testServer(t)
	h := s.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed,
			Details: []FieldError{{Field: "expected_ipv4", Message: "IPv4 地址格式不正确"}}})
	}))
	e := decodeError(t, do(h, "POST", "/x", "", nil))
	if len(e.Details) != 1 || e.Details[0].Field != "expected_ipv4" {
		t.Errorf("details 应返回字段级错误：%+v", e.Details)
	}
}

func TestInternalErrorHidesCause(t *testing.T) {
	s, _, logs := testServer(t)
	h := s.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.writeError(w, r, errors.New("SELECT * FROM secret_table: disk I/O error at /var/lib/vpsmon"))
	}))
	rec := do(h, "GET", "/x", "", nil)
	if strings.Contains(rec.Body.String(), "secret_table") || strings.Contains(rec.Body.String(), "/var/lib") {
		t.Errorf("【安全】内部原因不得返回客户端（设计 43.3.1）：%s", rec.Body)
	}
	if e := decodeError(t, rec); e.Code != CodeInternal {
		t.Errorf("未具名的错误应映射为 internal：%q", e.Code)
	}
	if !strings.Contains(logs.String(), "secret_table") {
		t.Error("内部原因应写入服务端日志，便于排查")
	}
}

func TestPanicRecovery(t *testing.T) {
	s, _, logs := testServer(t)
	h := s.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := do(h, "GET", "/x", "", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic 应返回 500，实际 %d", rec.Code)
	}
	if e := decodeError(t, rec); e.Code != CodeInternal {
		t.Errorf("code = %q", e.Code)
	}
	if !strings.Contains(logs.String(), "panic in handler") || !strings.Contains(logs.String(), "stack") {
		t.Error("panic 应记录 ERROR 与堆栈（设计 43.3.1）")
	}
}

func TestHealthzShowsVersion(t *testing.T) {
	_, h, _ := testServer(t)
	rec := do(h, "GET", "/healthz", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"version":"v-test"`) {
		t.Errorf("/healthz 应返回版本号（设计 40.3.2）：%d %s", rec.Code, rec.Body)
	}
	if !strings.HasPrefix(rec.Header().Get("X-Request-ID"), "r_") {
		t.Errorf("每个响应都应带 X-Request-ID：%q", rec.Header().Get("X-Request-ID"))
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	_, h, _ := testServer(t)
	rec := do(h, "GET", "/api/v1/nope", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未定义的接口应返回 404，实际 %d：%s", rec.Code, rec.Body)
	}
	if e := decodeError(t, rec); e.Code != CodeNotFound {
		t.Errorf("code = %q", e.Code)
	}
	// 非 /api/ 路径仍走 SPA 回退
	if rec := do(h, "GET", "/servers/12", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "spa") {
		t.Errorf("前端路由应返回 index.html：%d %s", rec.Code, rec.Body)
	}
}

func TestAdminAuth(t *testing.T) {
	s, h, _ := testServer(t)
	tok := adminToken(t, s)
	for _, c := range []struct {
		name, token string
		status      int
	}{
		{"无 Token", "", 401},
		{"伪造的会话", "ses_wrongwrongwrongwrong", 401},
		{"旧的开发 token 已不再有效", "adm_wrongwrongwrongwrong", 401},
		{"Agent Token 不能当 admin 用（设计 1.6.6）", "agt_aaaaaaaaaaaaaaaa", 401},
		{"有效 Token", tok, 200},
	} {
		rec := do(h, "GET", "/api/v1/servers", c.token, nil)
		if rec.Code != c.status {
			t.Errorf("%s：状态码 %d，应为 %d", c.name, rec.Code, c.status)
		}
		if c.status == 401 {
			if e := decodeError(t, rec); e.Code != CodeUnauthorized {
				t.Errorf("%s：code = %q", c.name, e.Code)
			}
		}
	}
}

// 【安全】数据库故障时鉴权按失败处理，返回 500 而不是放行；也不伪装成 401 误导排查（设计 43.1）。
func TestAuthFailsClosedOnDBError(t *testing.T) {
	s, h, _ := testServer(t)
	tok := adminToken(t, s)
	s.store.DB.Close()
	for _, c := range []struct{ name, method, path, token string }{
		{"admin 接口", "GET", "/api/v1/servers", tok},
		{"Agent 上报", "POST", "/api/v1/agent/report", "agt_aaaaaaaaaaaaaaaa"},
	} {
		rec := do(h, c.method, c.path, c.token, []byte(`{}`))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s：数据库不可用时应返回 500，实际 %d", c.name, rec.Code)
		}
	}
}

func TestAgentReport(t *testing.T) {
	s, h, logs := testServer(t)
	_, agentTok, err := s.store.CreateServer("hk-1", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		token  string
		body   []byte
		status int
		code   Code
	}{
		{"无效 Token", "agt_invalidinvalidinvalid", []byte(`{}`), 401, CodeUnauthorized},
		{"不是 JSON", agentTok, []byte(`not json`), 400, CodeBadRequest},
		{"超过 64 KB（设计 43.2）", agentTok, []byte(`{"system":{"hostname":"` + strings.Repeat("a", maxReportSize) + `"}}`), 413, CodePayloadTooLarge},
		{"正常上报", agentTok, []byte(`{"timestamp":1,"system":{"boot_id":"b"},"network":[{"interface":"eth0","rx_bytes":1,"tx_bytes":1}]}`), 204, ""},
	}
	for _, c := range cases {
		rec := do(h, "POST", "/api/v1/agent/report", c.token, c.body)
		if rec.Code != c.status {
			t.Errorf("%s：状态码 %d，应为 %d；body=%s", c.name, rec.Code, c.status, rec.Body)
			continue
		}
		if c.code != "" {
			if e := decodeError(t, rec); e.Code != c.code {
				t.Errorf("%s：code = %q，应为 %q", c.name, e.Code, c.code)
			}
		}
	}
	// 【安全】请求日志中不得出现完整 Token（设计 24.7）
	if strings.Contains(logs.String(), agentTok) {
		t.Error("日志中出现了完整的 Agent Token")
	}
	if !strings.Contains(logs.String(), `"principal":"agent"`) {
		t.Errorf("请求日志应记录主体类型（设计 24.6）：%s", logs)
	}
}

func TestAsAPIError(t *testing.T) {
	if !isAPIError(asAPIError(&http.MaxBytesError{Limit: 1}), CodePayloadTooLarge) {
		t.Error("MaxBytesError 应映射为 payload_too_large")
	}
	if !isAPIError(asAPIError(errors.New("x")), CodeInternal) {
		t.Error("未知错误应映射为 internal")
	}
	wrapped := errors.Join(errors.New("ctx"), errorf(CodeConflict, ""))
	if !isAPIError(asAPIError(wrapped), CodeConflict) {
		t.Error("被包装的具名错误应保留原错误码（%w 包装，设计 43.3.1）")
	}
}

func TestPointTime(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name   string
		ts     int64
		want   int64
		skewed bool
	}{
		{"旧版 Agent 没有时间戳", 0, now.Unix(), false},
		{"正常上报", now.Unix() - 1, now.Unix() - 1, false},
		{"断网后补发 20 分钟前的数据（设计 1.6.14）", now.Unix() - 1200, now.Unix() - 1200, false},
		{"超过 1 小时的旧数据只计流量，用收到时间", now.Unix() - 7200, now.Unix(), false},
		{"Agent 时钟超前 2 分钟（设计 43.5）", now.Unix() + 120, now.Unix(), true},
		{"超前 30 秒在容忍范围内", now.Unix() + 30, now.Unix() + 30, false},
	}
	for _, c := range cases {
		at, skewed, old := pointTime(protocol.Report{Timestamp: c.ts}, now)
		if at.Unix() != c.want || skewed != c.skewed || old != (c.ts != 0 && c.ts < now.Unix()-3600) {
			t.Errorf("%s：%d skewed=%v old=%v，应为 %d skewed=%v", c.name, at.Unix(), skewed, old, c.want, c.skewed)
		}
	}
}

// Agent 落盘保留的重启前最后一份计数：超过补发期限也要补齐同一次启动内的流量，
// 但不改实时状态、不写指标点；不连续的旧计数直接忽略（设计 1.6.14、5.5）。
func TestCarryOverIngest(t *testing.T) {
	s, h, _ := testServer(t)
	_, tok, _ := s.store.CreateServer("co", 0, 1)
	post := func(ts int64, boot string, cpu int, rx uint64) {
		body := fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":%q},"cpu":{"usage":%d},"network":[{"interface":"eth0","ifindex":2,"rx_bytes":%d,"tx_bytes":0}]}`,
			ts, boot, cpu, rx)
		if rec := do(h, "POST", "/api/v1/agent/report", tok, []byte(body)); rec.Code != 204 {
			t.Fatalf("上报失败：%d", rec.Code)
		}
	}
	now := time.Now().Unix()
	post(now-10, "a", 11, 1000)     // 断网前最后一次成功上报，当时正常送达（首次只建基线）
	post(now-2*3600, "a", 22, 9000) // 重启前落盘的最后一份，2 小时后才送达
	s.mu.Lock()
	cpu := s.latest[1].Report.CPU.Usage
	s.mu.Unlock()
	if cpu != 11 {
		t.Errorf("过期的旧上报不能成为实时状态：cpu=%v", cpu)
	}
	post(now-2*3600, "z", 33, 99999) // 其他启动的旧计数：不连续，忽略
	post(now, "b", 44, 500)          // 重启后的首份上报：新基线，计入开机以来的 500
	s.flush()

	rx, _, _ := s.store.TrafficSince(1, time.Now().AddDate(0, 0, -1))
	if rx != 8000+500 {
		t.Errorf("应补齐重启前的 8000 与重启后的 500，不计入不连续的旧计数：%d", rx)
	}
	var n int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM metrics_raw WHERE server_id = 1`).Scan(&n)
	if n != 2 {
		t.Errorf("过期的旧上报不写指标点，只有两份正常上报：%d 行", n)
	}
}

// 补发的旧数据按采集时间入库，且不覆盖更新的实时状态；重复上报不重复计流量。
func TestBackfillIngest(t *testing.T) {
	s, h, _ := testServer(t)
	_, tok, _ := s.store.CreateServer("bf", 0, 1)
	post := func(ts int64, rx uint64) {
		body := fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"},"cpu":{"usage":%d},"network":[{"interface":"eth0","rx_bytes":%d,"tx_bytes":0}]}`, ts, ts%100, rx)
		if rec := do(h, "POST", "/api/v1/agent/report", tok, []byte(body)); rec.Code != 204 {
			t.Fatalf("上报失败：%d", rec.Code)
		}
	}
	now := time.Now().Unix()
	post(now-600, 1000) // 断网期间缓存的数据，恢复后按顺序补发
	post(now-590, 2000)
	post(now, 5000)
	post(now, 5000) // Agent 以为失败而重发的同一份
	s.flush()

	var n int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM metrics_raw WHERE ts IN (?, ?, ?)`, now-600, now-590, now).Scan(&n)
	if n != 3 {
		t.Errorf("补发的点应按采集时间入库，重复的一份去重：%d 行", n)
	}
	rx, _, _ := s.store.TrafficSince(1, time.Now().AddDate(0, 0, -1))
	if rx != 4000 {
		t.Errorf("流量增量应为 4000（首次只建基线，重复上报增量为 0），实际 %d", rx)
	}

	post(now-300, 3000) // 更旧的补发数据晚到：不能覆盖实时状态
	s.mu.Lock()
	at := s.latest[1].At.Unix()
	s.mu.Unlock()
	if at != now {
		t.Errorf("实时状态应保留最新采集时间 %d，实际 %d", now, at)
	}
	post(now+1, 6000) // 旧报告不能回退计数基线，下一份只应增加 1000 字节
	s.flush()
	rx, _, _ = s.store.TrafficSince(1, time.Now().AddDate(0, 0, -1))
	if rx != 5000 {
		t.Errorf("乱序补发后流量应为 5000（不重复计算），实际 %d", rx)
	}
}

// 数据库写入失败后，flush 必须保留批次供下一轮重试（设计 43.3.2）。
func TestFlushRetriesFailedBatch(t *testing.T) {
	s, h, _ := testServer(t)
	_, tok, _ := s.store.CreateServer("retry", 0, 1)
	now := time.Now().Unix()
	body := []byte(fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"},"network":[{"interface":"eth0","rx_bytes":1000,"tx_bytes":0}]}`, now))
	if rec := do(h, "POST", "/api/v1/agent/report", tok, body); rec.Code != 204 {
		t.Fatalf("上报失败：%d", rec.Code)
	}
	if _, err := s.store.DB.Exec(`CREATE TRIGGER fail_metrics BEFORE INSERT ON metrics_raw BEGIN SELECT RAISE(FAIL, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	s.flush()
	s.mu.Lock()
	queued := len(s.pending)
	s.mu.Unlock()
	if queued != 1 {
		t.Fatalf("数据库写入失败后应保留批次，队列长度=%d", queued)
	}
	if _, err := s.store.DB.Exec(`DROP TRIGGER fail_metrics`); err != nil {
		t.Fatal(err)
	}
	s.flush()
	s.mu.Lock()
	queued = len(s.pending)
	s.mu.Unlock()
	if queued != 0 {
		t.Errorf("重试成功后队列应清空，队列长度=%d", queued)
	}
	var n int
	if err := s.store.DB.QueryRow(`SELECT COUNT(*) FROM metrics_raw WHERE server_id = 1 AND ts = ?`, now).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("重试后应持久化一条指标，实际 %d", n)
	}
}

// 压缩的上报（设计 6.1）：声明 Accept-Encoding，按 Content-Encoding 解压；
// 解压后超过上限（压缩炸弹）返回 413，不认识的编码返回 415（设计 43.2、43.4）。
func TestGzipReport(t *testing.T) {
	s, h, _ := testServer(t)
	_, tok, _ := s.store.CreateServer("gz", 0, 1)
	gz := func(b []byte) []byte {
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		w.Write(b)
		w.Close()
		return buf.Bytes()
	}
	post := func(body []byte, enc string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/agent/report", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		if enc != "" {
			req.Header.Set("Content-Encoding", enc)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := post([]byte(`{"system":{"boot_id":"b"},"cpu":{"usage":12}}`), "")
	if rec.Code != 204 || rec.Header().Get("Accept-Encoding") != "gzip" {
		t.Fatalf("未压缩的上报照常接受，并声明支持 gzip：%d %q", rec.Code, rec.Header().Get("Accept-Encoding"))
	}
	if rec := post(gz([]byte(`{"system":{"boot_id":"b"},"cpu":{"usage":34}}`)), "gzip"); rec.Code != 204 {
		t.Fatalf("压缩的上报应被接受：%d %s", rec.Code, rec.Body)
	}
	s.mu.Lock()
	cpu := s.latest[1].Report.CPU.Usage
	s.mu.Unlock()
	if cpu != 34 {
		t.Errorf("压缩的上报应正确解析：cpu=%v", cpu)
	}

	bomb := gz(append([]byte(`{"system":{"boot_id":"`), bytes.Repeat([]byte("a"), maxReportSize*4)...))
	if len(bomb) > maxReportSize/10 {
		t.Fatalf("测试数据应是高压缩比的“炸弹”：%d 字节", len(bomb))
	}
	if rec := post(bomb, "gzip"); rec.Code != 413 || decodeError(t, rec).Code != "payload_too_large" {
		t.Errorf("解压后超过上限应返回 413：%d %s", rec.Code, rec.Body)
	}
	if rec := post([]byte("not gzip"), "gzip"); rec.Code != 400 {
		t.Errorf("损坏的 gzip 应返回 400：%d", rec.Code)
	}
	if rec := post([]byte(`{}`), "br"); rec.Code != 415 || decodeError(t, rec).Code != "unsupported_encoding" {
		t.Errorf("不认识的编码应返回 415：%d %s", rec.Code, rec.Body)
	}
}

func TestReportInterval(t *testing.T) {
	for s, ok := range map[int]bool{5: true, 10: true, 60: true, 0: false, 7: false, 120: false} {
		if ValidReportInterval(s) != ok {
			t.Errorf("ValidReportInterval(%d) 应为 %v", s, ok)
		}
	}
	cases := []struct {
		iv              time.Duration
		online, unknown time.Duration
		offline         float64
	}{
		{10 * time.Second, 30 * time.Second, 120 * time.Second, 120}, // 默认：与原来一致
		{5 * time.Second, 30 * time.Second, 120 * time.Second, 120},
		{30 * time.Second, 90 * time.Second, 180 * time.Second, 120},
		{60 * time.Second, 180 * time.Second, 360 * time.Second, 180}, // 漏一次上报（120 秒）不算离线
	}
	for _, c := range cases {
		on, unk := statusWindows(c.iv)
		r := ruleForInterval(AlertRule{Type: AlertOffline, Threshold: 120}, c.iv)
		if on != c.online || unk != c.unknown || r.Threshold != c.offline {
			t.Errorf("%s：在线 %s / 未知 %s / 离线阈值 %v，应为 %s / %s / %v", c.iv, on, unk, r.Threshold, c.online, c.unknown, c.offline)
		}
	}
	if r := ruleForInterval(AlertRule{Type: AlertCPU, Threshold: 90}, time.Minute); r.Threshold != 90 {
		t.Error("其他规则不受采样间隔影响")
	}
}

// 采样间隔随上报响应下发；修改节点后下一份上报即生效（设计 6.1）。
func TestReportIntervalHeader(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, n, _ := createNode(t, h, admin, `{"name":"iv-1","report_interval_s":30}`)
	_, res := enroll(h, n.EnrollCode, "iv-host", "iv-machine")
	report := func() string {
		rec := do(h, "POST", "/api/v1/agent/report", res.AgentToken, []byte(`{"system":{"boot_id":"b"}}`))
		return rec.Header().Get("X-Report-Interval")
	}
	if got := report(); got != "30" {
		t.Fatalf("应下发节点的采样间隔 30：%q", got)
	}
	base := "/api/v1/servers/" + itoa(n.ServerID)
	if rec := do(h, "PUT", base, admin, []byte(`{"name":"iv-1","report_interval_s":60}`)); rec.Code != 200 {
		t.Fatalf("修改失败：%d %s", rec.Code, rec.Body)
	}
	if got := report(); got != "60" {
		t.Errorf("修改后下一份上报应下发新间隔：%q", got)
	}
	do(h, "PUT", base, admin, []byte(`{"name":"iv-1"}`))
	if got := report(); got != "10" {
		t.Errorf("恢复默认后应下发 10：%q", got)
	}
	if rec := do(h, "PUT", base, admin, []byte(`{"name":"iv-1","report_interval_s":7}`)); rec.Code != 422 {
		t.Errorf("不在可选范围的间隔应返回 422：%d", rec.Code)
	}
}
