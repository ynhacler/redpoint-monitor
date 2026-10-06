package server

// 服务监控（设计 33.2）：面板按间隔检查 HTTP / HTTPS、TCP 端口与 DNS 解析；
// 连续失败达到阈值判为异常并通知，恢复时再通知一次（经 emit：抖动检测、免打扰、全部渠道与 App 推送）。
//
// 【安全】HTTPS 始终校验证书（约束 6），不提供跳过校验的选项；网址中不允许带用户名密码（不在面板中保存凭证）。
// 检查由面板发起，只有 Web 管理员能配置；Agent 不参与（Agent 端的网络探测见设计 42，另有硬性上限）。
//
// 状态判断在纯函数 nextServiceState 中（表格测试）。

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxServiceMonitors = 200
	serviceKeep        = 7 * 24 * time.Hour
	serviceBodyLimit   = 64 << 10
	serviceConcurrency = 8
)

// 服务状态
const (
	ServiceUp      = "up"
	ServiceDown    = "down"
	ServiceUnknown = "unknown"
)

// ServiceMonitor 是一个服务监控。
type ServiceMonitor struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Target        string   `json:"target"`
	ServerID      int64    `json:"server_id"`
	IntervalS     int      `json:"interval_s"`
	TimeoutS      int      `json:"timeout_s"`
	FailThreshold int      `json:"fail_threshold"`
	Severity      string   `json:"severity"`
	ExpectStatus  string   `json:"expect_status"`
	Keyword       string   `json:"keyword"`
	DNSType       string   `json:"dns_type"`
	DNSExpect     string   `json:"dns_expect"`
	Enabled       bool     `json:"enabled"`
	Status        string   `json:"status"`
	StatusSince   int64    `json:"status_since"`
	Fails         int      `json:"fails"`
	LastLatencyMs *int64   `json:"last_latency_ms"`
	LastError     string   `json:"last_error"`
	CheckedAt     int64    `json:"checked_at"`
	Uptime24h     *float64 `json:"uptime_24h"`
	Uptime7d      *float64 `json:"uptime_7d"`
	CreatedAt     int64    `json:"created_at"`

	eventStarted int64 // 当前异常开始的时间（通知中的“已持续”）
}

// serviceInput 是创建与修改的请求体。
type serviceInput struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Target        string `json:"target"`
	ServerID      int64  `json:"server_id"`
	IntervalS     *int   `json:"interval_s"`
	TimeoutS      *int   `json:"timeout_s"`
	FailThreshold *int   `json:"fail_threshold"`
	Severity      string `json:"severity"`
	ExpectStatus  string `json:"expect_status"`
	Keyword       string `json:"keyword"`
	DNSType       string `json:"dns_type"`
	DNSExpect     string `json:"dns_expect"`
	Enabled       *bool  `json:"enabled"`
}

// apply 校验并写入 m（保留 m 的状态字段）。
func (b serviceInput) apply(m *ServiceMonitor) []FieldError {
	var errs []FieldError
	bad := func(f, msg string) { errs = append(errs, FieldError{Field: f, Message: msg}) }
	m.Name = strings.TrimSpace(b.Name)
	if m.Name == "" || utf8.RuneCountInString(m.Name) > 64 {
		bad("name", "请填写名称（最多 64 个字符）")
	}
	m.Kind = b.Kind
	target, msg := normalizeServiceTarget(b.Kind, b.Target)
	if msg != "" {
		bad("target", msg)
	}
	m.Target = target
	m.ServerID = max(b.ServerID, 0)
	m.IntervalS, m.TimeoutS, m.FailThreshold = 60, 10, 2
	if b.IntervalS != nil {
		m.IntervalS = *b.IntervalS
	}
	if b.TimeoutS != nil {
		m.TimeoutS = *b.TimeoutS
	}
	if b.FailThreshold != nil {
		m.FailThreshold = *b.FailThreshold
	}
	if m.IntervalS < 30 || m.IntervalS > 3600 {
		bad("interval_s", "检查间隔为 30～3600 秒")
	}
	if m.TimeoutS < 1 || m.TimeoutS > 30 {
		bad("timeout_s", "超时为 1～30 秒")
	} else if m.TimeoutS >= m.IntervalS {
		bad("timeout_s", "超时应短于检查间隔")
	}
	if m.FailThreshold < 1 || m.FailThreshold > 10 {
		bad("fail_threshold", "连续失败次数为 1～10")
	}
	m.Severity = b.Severity
	if m.Severity == "" {
		m.Severity = SeverityCritical
	}
	if m.Severity != SeverityCritical && m.Severity != SeverityWarning {
		bad("severity", "级别只能是 critical 或 warning")
	}
	m.ExpectStatus, m.Keyword, m.DNSType, m.DNSExpect = "", "", "A", ""
	switch m.Kind {
	case "http":
		m.ExpectStatus = strings.ReplaceAll(strings.TrimSpace(b.ExpectStatus), " ", "")
		if _, err := parseExpectStatus(m.ExpectStatus); err != nil {
			bad("expect_status", "状态码格式如 200、200-299、200,301")
		}
		m.Keyword = strings.TrimSpace(b.Keyword)
		if utf8.RuneCountInString(m.Keyword) > 100 {
			bad("keyword", "关键字最多 100 个字符")
		}
	case "dns":
		if b.DNSType != "" {
			m.DNSType = b.DNSType
		}
		if m.DNSType != "A" && m.DNSType != "AAAA" {
			bad("dns_type", "查询类型只能是 A 或 AAAA")
		}
		m.DNSExpect = strings.TrimSpace(b.DNSExpect)
		if m.DNSExpect != "" && net.ParseIP(m.DNSExpect) == nil {
			bad("dns_expect", "请填写 IP 地址")
		}
	}
	m.Enabled = b.Enabled == nil || *b.Enabled
	return errs
}

// normalizeServiceTarget 校验目标：http 为不带用户名密码的 http(s) 网址；tcp 为 主机:端口；dns 为域名。
func normalizeServiceTarget(kind, raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 500 || strings.ContainsAny(raw, " \t\r\n") {
		return "", "请填写目标"
	}
	switch kind {
	case "http":
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return "", "网址格式如 https://example.com/health"
		}
		if u.User != nil {
			return "", "【安全】网址中不能包含用户名或密码"
		}
		u.Fragment = ""
		return u.String(), ""
	case "tcp":
		host, port, err := net.SplitHostPort(raw)
		p, perr := strconv.Atoi(port)
		if err != nil || host == "" || perr != nil || p < 1 || p > 65535 {
			return "", "格式如 db.example.com:5432 或 [2001:db8::1]:22"
		}
		return net.JoinHostPort(strings.ToLower(host), port), ""
	case "dns":
		h := strings.TrimSuffix(strings.ToLower(raw), ".")
		if len(h) > 253 || !strings.Contains(h, ".") || strings.ContainsAny(h, "/:@") {
			return "", "请填写域名，如 example.com"
		}
		return h, ""
	}
	return "", "类型只能是 http、tcp 或 dns"
}

// parseExpectStatus 解析期望的状态码：“200”“200-299”“200,301”；空为 200-399。
func parseExpectStatus(spec string) (func(int) bool, error) {
	if spec == "" {
		return func(c int) bool { return c >= 200 && c < 400 }, nil
	}
	type rng struct{ lo, hi int }
	var rs []rng
	for _, part := range strings.Split(spec, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err1 := strconv.Atoi(lo)
		b := a
		var err2 error
		if isRange {
			b, err2 = strconv.Atoi(hi)
		}
		if err1 != nil || err2 != nil || a < 100 || b > 599 || a > b {
			return nil, errors.New("invalid")
		}
		rs = append(rs, rng{a, b})
	}
	if len(rs) > 10 {
		return nil, errors.New("too many")
	}
	return func(c int) bool {
		for _, r := range rs {
			if c >= r.lo && c <= r.hi {
				return true
			}
		}
		return false
	}, nil
}

// nextServiceState 根据一次检查结果计算新状态（纯函数）：
// 成功清零连续失败，原来异常则恢复（resolved）；失败累计，达到阈值且原来不是异常则进入异常（firing）。
// 未达到阈值的失败不改变 up，刚添加时保持 unknown。
func nextServiceState(status string, fails, threshold int, ok bool) (newStatus string, newFails int, event string) {
	if ok {
		if status == ServiceDown {
			return ServiceUp, 0, NotifyResolved
		}
		return ServiceUp, 0, ""
	}
	fails++
	if fails >= threshold && status != ServiceDown {
		return ServiceDown, fails, NotifyFiring
	}
	return status, fails, ""
}

// ---- 检查 ----

type serviceResult struct {
	ok      bool
	latency time.Duration
	err     string
}

// checkService 执行一次检查。roots 为 nil 时使用系统根证书（测试中替换）。
func checkService(ctx context.Context, m ServiceMonitor, roots *x509.CertPool) serviceResult {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(m.TimeoutS)*time.Second)
	defer cancel()
	start := time.Now()
	var err error
	switch m.Kind {
	case "http":
		err = checkHTTP(ctx, m, roots)
	case "tcp":
		var c net.Conn
		c, err = (&net.Dialer{}).DialContext(ctx, "tcp", m.Target)
		if err == nil {
			c.Close()
		}
	case "dns":
		err = checkDNS(ctx, m)
	default:
		err = errors.New("未知的检查类型")
	}
	r := serviceResult{ok: err == nil, latency: time.Since(start)}
	if err != nil {
		r.err = serviceErrorText(ctx, err)
	}
	return r
}

func checkHTTP(ctx context.Context, m ServiceMonitor, roots *x509.CertPool) error {
	want, err := parseExpectStatus(m.ExpectStatus)
	if err != nil {
		return err
	}
	tr := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		TLSClientConfig:   &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, // 【安全】始终校验证书
		DisableKeepAlives: true,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("重定向次数过多")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "vpsmon-service-check/1")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, serviceBodyLimit))
	if !want(resp.StatusCode) {
		exp := m.ExpectStatus
		if exp == "" {
			exp = "200-399"
		}
		return fmt.Errorf("状态码 %d（期望 %s）", resp.StatusCode, exp)
	}
	if m.Keyword != "" && !strings.Contains(string(body), m.Keyword) {
		return fmt.Errorf("响应中没有找到“%s”", m.Keyword)
	}
	return nil
}

func checkDNS(ctx context.Context, m ServiceMonitor) error {
	network := "ip4"
	if m.DNSType == "AAAA" {
		network = "ip6"
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, network, m.Target)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return errors.New("没有 " + m.DNSType + " 记录")
	}
	if m.DNSExpect != "" {
		want := net.ParseIP(m.DNSExpect)
		for _, ip := range ips {
			if ip.Equal(want) {
				return nil
			}
		}
		got := make([]string, 0, len(ips))
		for _, ip := range ips {
			got = append(got, ip.String())
		}
		return fmt.Errorf("解析结果 %s 不包含 %s", strings.Join(got, ", "), m.DNSExpect)
	}
	return nil
}

// serviceErrorText 把常见错误转成简短的中文说明；其余保留原文（不含凭证：目标中不允许用户名密码）。
func serviceErrorText(ctx context.Context, err error) string {
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var opErr *net.OpError
	msg := err.Error()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
		return "超时"
	case errors.As(err, &certErr):
		return verifyErrorText(certErr.Err)
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return "域名不存在或没有记录"
	case errors.As(err, &dnsErr):
		return "DNS 解析失败：" + dnsErr.Err
	case strings.Contains(msg, "connection refused"):
		return "连接被拒绝（端口未开放或服务未运行）"
	case strings.Contains(msg, "no route to host"), strings.Contains(msg, "network is unreachable"):
		return "网络不可达"
	case errors.As(err, &opErr) && opErr.Timeout():
		return "超时"
	}
	// http.Client 的错误形如 Get "https://…": 原因；只保留原因部分
	if i := strings.LastIndex(msg, "\": "); i > 0 {
		msg = msg[i+3:]
	}
	if utf8.RuneCountInString(msg) > 200 {
		msg = string([]rune(msg)[:200])
	}
	return msg
}

// ---- 存储 ----

const serviceColumns = `id, name, kind, target, server_id, interval_s, timeout_s, fail_threshold, severity, expect_status, keyword,
	dns_type, dns_expect, enabled, status, status_since, fails, last_latency_ms, last_error, checked_at, event_started, created_at`

func scanService(sc interface{ Scan(...any) error }) (ServiceMonitor, error) {
	var m ServiceMonitor
	var lat sql.NullInt64
	err := sc.Scan(&m.ID, &m.Name, &m.Kind, &m.Target, &m.ServerID, &m.IntervalS, &m.TimeoutS, &m.FailThreshold, &m.Severity,
		&m.ExpectStatus, &m.Keyword, &m.DNSType, &m.DNSExpect, &m.Enabled, &m.Status, &m.StatusSince, &m.Fails, &lat,
		&m.LastError, &m.CheckedAt, &m.eventStarted, &m.CreatedAt)
	if lat.Valid {
		m.LastLatencyMs = &lat.Int64
	}
	return m, err
}

func (s *Store) ListServiceMonitors() ([]ServiceMonitor, error) {
	rows, err := s.DB.Query(`SELECT ` + serviceColumns + ` FROM service_monitors ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServiceMonitor{}
	for rows.Next() {
		m, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetServiceMonitor(id int64) (ServiceMonitor, error) {
	m, err := scanService(s.DB.QueryRow(`SELECT `+serviceColumns+` FROM service_monitors WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return m, errorf(CodeNotFound, "服务监控不存在")
	}
	return m, err
}

func (s *Store) saveServiceConfig(m *ServiceMonitor, now time.Time) error {
	if m.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO service_monitors (name, kind, target, server_id, interval_s, timeout_s, fail_threshold, severity,
			expect_status, keyword, dns_type, dns_expect, enabled, status, status_since, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			m.Name, m.Kind, m.Target, m.ServerID, m.IntervalS, m.TimeoutS, m.FailThreshold, m.Severity, m.ExpectStatus, m.Keyword,
			m.DNSType, m.DNSExpect, m.Enabled, ServiceUnknown, now.Unix(), now.Unix())
		if err != nil {
			return err
		}
		m.ID, _ = res.LastInsertId()
		m.Status, m.StatusSince, m.CreatedAt = ServiceUnknown, now.Unix(), now.Unix()
		return nil
	}
	_, err := s.DB.Exec(`UPDATE service_monitors SET name = ?, kind = ?, target = ?, server_id = ?, interval_s = ?, timeout_s = ?,
		fail_threshold = ?, severity = ?, expect_status = ?, keyword = ?, dns_type = ?, dns_expect = ?, enabled = ? WHERE id = ?`,
		m.Name, m.Kind, m.Target, m.ServerID, m.IntervalS, m.TimeoutS, m.FailThreshold, m.Severity, m.ExpectStatus, m.Keyword,
		m.DNSType, m.DNSExpect, m.Enabled, m.ID)
	return err
}

// saveServiceResult 记录一次检查与新状态。
func (s *Store) saveServiceResult(m ServiceMonitor, r serviceResult, now time.Time) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO service_checks (monitor_id, ts, ok, latency_ms) VALUES (?,?,?,?)`,
		m.ID, now.Unix(), r.ok, r.latency.Milliseconds()); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE service_monitors SET status = ?, status_since = ?, fails = ?, last_latency_ms = ?, last_error = ?,
		checked_at = ?, event_started = ? WHERE id = ?`, m.Status, m.StatusSince, m.Fails, m.LastLatencyMs, m.LastError, m.CheckedAt,
		m.eventStarted, m.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteServiceMonitor(id int64) error {
	res, err := s.DB.Exec(`DELETE FROM service_monitors WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errorf(CodeNotFound, "服务监控不存在")
	}
	_, err = s.DB.Exec(`DELETE FROM service_checks WHERE monitor_id = ?`, id)
	return err
}

// PruneServiceChecks 删除 7 天前的检查记录。
func (s *Store) PruneServiceChecks(now time.Time) (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM service_checks WHERE ts < ?`, now.Add(-serviceKeep).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// serviceUptime 返回各监控在 since 之后的成功率（0～100）。
func (s *Store) serviceUptime(since time.Time) (map[int64]float64, error) {
	rows, err := s.DB.Query(`SELECT monitor_id, SUM(ok), COUNT(*) FROM service_checks WHERE ts >= ? GROUP BY monitor_id`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]float64{}
	for rows.Next() {
		var id, ok, n int64
		if err := rows.Scan(&id, &ok, &n); err != nil {
			return nil, err
		}
		if n > 0 {
			out[id] = float64(ok) * 100 / float64(n)
		}
	}
	return out, rows.Err()
}

type serviceCheckPoint struct {
	TS        int64  `json:"ts"`
	OK        int64  `json:"ok"`
	Total     int64  `json:"total"`
	LatencyMs *int64 `json:"latency_ms"`
}

// serviceChecks 返回 since 之后的检查；bucket > 0 时按 bucket 秒汇总。
func (s *Store) serviceChecks(id int64, since time.Time, bucket int64) ([]serviceCheckPoint, error) {
	if bucket <= 0 {
		bucket = 1
	}
	rows, err := s.DB.Query(`SELECT (ts / ?) * ?, SUM(ok), COUNT(*), CAST(AVG(CASE WHEN ok = 1 THEN latency_ms END) AS INTEGER)
		FROM service_checks WHERE monitor_id = ? AND ts >= ? GROUP BY ts / ? ORDER BY 1`, bucket, bucket, id, since.Unix(), bucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []serviceCheckPoint{}
	for rows.Next() {
		var p serviceCheckPoint
		var lat sql.NullInt64
		if err := rows.Scan(&p.TS, &p.OK, &p.Total, &lat); err != nil {
			return nil, err
		}
		if lat.Valid {
			p.LatencyMs = &lat.Int64
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- 调度 ----

// serviceRunner 防止同一监控的检查重叠（检查可能比调度周期长）。
type serviceRunner struct {
	mu      sync.Mutex
	running map[int64]bool
}

// serviceLoop 每 5 秒找出到期的监控并检查，最多同时 8 个。
func (s *Server) serviceLoop(ctx context.Context) {
	sem := make(chan struct{}, serviceConcurrency)
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now()
		list, err := s.store.ListServiceMonitors()
		if err != nil {
			s.log.Error("list service monitors failed", "component", "service", "err", err)
			continue
		}
		for _, m := range list {
			if !m.Enabled || now.Unix() < m.CheckedAt+int64(m.IntervalS) || !s.services.claim(m.ID) {
				continue
			}
			sem <- struct{}{}
			go func(m ServiceMonitor) {
				defer func() { <-sem; s.services.release(m.ID) }()
				s.runServiceCheck(ctx, m)
			}(m)
		}
	}
}

func (r *serviceRunner) claim(id int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running == nil {
		r.running = map[int64]bool{}
	}
	if r.running[id] {
		return false
	}
	r.running[id] = true
	return true
}

func (r *serviceRunner) release(id int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.running, id)
}

// runServiceCheck 检查一次、保存结果，并在状态变化时通知。
func (s *Server) runServiceCheck(ctx context.Context, m ServiceMonitor) ServiceMonitor {
	r := checkService(ctx, m, s.sslRoots)
	return s.applyServiceResult(m, r, time.Now())
}

func (s *Server) applyServiceResult(m ServiceMonitor, r serviceResult, now time.Time) ServiceMonitor {
	prev := m.Status
	status, fails, event := nextServiceState(m.Status, m.Fails, m.FailThreshold, r.ok)
	m.Status, m.Fails, m.CheckedAt = status, fails, now.Unix()
	if status != prev {
		m.StatusSince = now.Unix()
	}
	if r.ok {
		lat := r.latency.Milliseconds()
		m.LastLatencyMs, m.LastError = &lat, ""
	} else {
		m.LastLatencyMs, m.LastError = nil, r.err
	}
	if event == NotifyFiring {
		m.eventStarted = now.Unix()
	}
	if err := s.store.saveServiceResult(m, r, now); err != nil {
		s.log.Error("save service check failed", "component", "service", "monitor", m.ID, "err", err)
		return m
	}
	if event != "" {
		s.log.Info("service "+status, "component", "service", "monitor", m.ID, "name", m.Name, "error", m.LastError)
		s.emit(alertKey{ruleKey: "service:" + strconv.FormatInt(m.ID, 10)}, s.serviceNotice(m, event, r, now), now)
	}
	return m
}

// serviceNotice 组装通知：标题为“🔴 名称 无法访问：原因”，恢复时“✅ 名称 已恢复：访问正常（123 ms）”。
func (s *Server) serviceNotice(m ServiceMonitor, kind string, r serviceResult, now time.Time) notifyMessage {
	n := notifyMessage{Kind: kind, ServerID: m.ServerID, ServerName: m.Name, RuleKey: "service:" + strconv.FormatInt(m.ID, 10),
		Type: "service", Severity: m.Severity, StartedAt: time.Unix(m.eventStarted, 0)}
	kindName := map[string]string{"http": "网址", "tcp": "端口", "dns": "域名解析"}[m.Kind]
	if kind == NotifyResolved {
		n.ResolvedAt = now
		n.Message = fmt.Sprintf("%s访问正常（%d ms）", kindName, r.latency.Milliseconds())
	} else {
		n.Message = fmt.Sprintf("%s无法访问：%s（%s）", kindName, r.err, m.Target)
	}
	if s.publicURL != "" {
		n.Link = s.publicURL + "/alerts?tab=services"
	}
	return n
}

// ---- 接口 ----

func (s *Server) withUptime(list []ServiceMonitor, now time.Time) ([]ServiceMonitor, error) {
	d, err := s.store.serviceUptime(now.Add(-24 * time.Hour))
	if err != nil {
		return nil, err
	}
	w, err := s.store.serviceUptime(now.Add(-serviceKeep))
	if err != nil {
		return nil, err
	}
	for i := range list {
		if v, ok := d[list[i].ID]; ok {
			list[i].Uptime24h = &v
		}
		if v, ok := w[list[i].ID]; ok {
			list[i].Uptime7d = &v
		}
	}
	return list, nil
}

// handleServiceMonitors：GET /api/v1/service-monitors，admin。异常在前，其次未知，然后按名称。
func (s *Server) handleServiceMonitors(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListServiceMonitors()
	if err == nil {
		list, err = s.withUptime(list, time.Now())
	}
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	rank := map[string]int{ServiceDown: 0, ServiceUnknown: 1, ServiceUp: 2}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Enabled != b.Enabled {
			return a.Enabled
		}
		if rank[a.Status] != rank[b.Status] {
			return rank[a.Status] < rank[b.Status]
		}
		return a.Name < b.Name
	})
	writeList(w, list, "", nil)
}

func decodeServiceInput(w http.ResponseWriter, r *http.Request) (serviceInput, error) {
	var b serviceInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return b, &APIError{Code: CodeBadRequest, Cause: err}
	}
	return b, nil
}

// checkServiceServer 确认关联的节点存在。
func (s *Server) checkServiceServer(id int64) []FieldError {
	if id == 0 {
		return nil
	}
	if _, err := s.store.GetServer(id); err != nil {
		return []FieldError{{Field: "server_id", Message: "节点不存在"}}
	}
	return nil
}

// handleCreateServiceMonitor：POST /api/v1/service-monitors，admin。添加后立即检查一次。
func (s *Server) handleCreateServiceMonitor(w http.ResponseWriter, r *http.Request) {
	b, err := decodeServiceInput(w, r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var m ServiceMonitor
	errs := append(b.apply(&m), s.checkServiceServer(m.ServerID)...)
	if len(errs) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: errs})
		return
	}
	var n int
	if err := s.store.DB.QueryRow(`SELECT COUNT(*) FROM service_monitors`).Scan(&n); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if n >= maxServiceMonitors {
		s.writeError(w, r, errorf(CodeConflict, fmt.Sprintf("最多监控 %d 个服务", maxServiceMonitors)))
		return
	}
	now := time.Now()
	if err := s.store.saveServiceConfig(&m, now); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if m.Enabled && s.services.claim(m.ID) {
		m = s.runServiceCheck(r.Context(), m)
		s.services.release(m.ID)
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "service_monitor.create", Success: true,
		Details: map[string]any{"id": m.ID, "kind": m.Kind, "target": m.Target}})
	list, _ := s.withUptime([]ServiceMonitor{m}, now)
	writeJSONStatus(w, http.StatusCreated, list[0])
}

// handleUpdateServiceMonitor：PUT /api/v1/service-monitors/{id}，admin。状态与历史保留。
func (s *Server) handleUpdateServiceMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	m, err := s.store.GetServiceMonitor(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	b, err := decodeServiceInput(w, r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	oldKind, oldTarget := m.Kind, m.Target
	errs := append(b.apply(&m), s.checkServiceServer(m.ServerID)...)
	if len(errs) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: errs})
		return
	}
	if m.Kind != oldKind || m.Target != oldTarget {
		m.CheckedAt = 0 // 目标变了：尽快检查一次
	}
	if err := s.store.saveServiceConfig(&m, time.Now()); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if m.CheckedAt == 0 {
		s.store.DB.Exec(`UPDATE service_monitors SET checked_at = 0 WHERE id = ?`, m.ID)
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "service_monitor.update", Success: true,
		Details: map[string]any{"id": m.ID, "kind": m.Kind, "target": m.Target, "enabled": m.Enabled}})
	list, _ := s.withUptime([]ServiceMonitor{m}, time.Now())
	writeJSON(w, list[0])
}

// handleDeleteServiceMonitor：DELETE /api/v1/service-monitors/{id}，admin。
func (s *Server) handleDeleteServiceMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.store.DeleteServiceMonitor(id); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "service_monitor.delete", Success: true, Details: map[string]any{"id": id}})
	w.WriteHeader(http.StatusNoContent)
}

// handleCheckServiceMonitor：POST /api/v1/service-monitors/{id}/check，admin。立即检查一次。
func (s *Server) handleCheckServiceMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	m, err := s.store.GetServiceMonitor(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if !s.services.claim(m.ID) {
		s.writeError(w, r, errorf(CodeConflict, "正在检查，请稍后"))
		return
	}
	m = s.runServiceCheck(r.Context(), m)
	s.services.release(m.ID)
	list, _ := s.withUptime([]ServiceMonitor{m}, time.Now())
	writeJSON(w, list[0])
}

// handleServiceChecks：GET /api/v1/service-monitors/{id}/checks?range=24h|7d，admin。
func (s *Server) handleServiceChecks(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if _, err := s.store.GetServiceMonitor(id); err != nil {
		s.writeError(w, r, err)
		return
	}
	now := time.Now()
	since, bucket := now.Add(-24*time.Hour), int64(0)
	switch r.URL.Query().Get("range") {
	case "", "24h":
	case "7d":
		since, bucket = now.Add(-serviceKeep), 600
	default:
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "range", Message: "只能是 24h 或 7d"}}})
		return
	}
	pts, err := s.store.serviceChecks(id, since, bucket)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeList(w, pts, "", nil)
}
