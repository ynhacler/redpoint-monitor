package server

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Agent 注册：一次性注册码换取长期 Agent Token（设计 23.5、27.4、27.6）。

// crockford 是 Crockford Base32 字母表：去掉了易混淆的 I、L、O、U（设计 27.4）。
// 32 个字符，取随机字节的低 5 位即可均匀分布。
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// enrollCodeFormat 匹配规范化后的注册码 ENR-XXXX-XXXX-XXXX-XXXX。
var enrollCodeFormat = regexp.MustCompile(`^ENR-[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}$`)

// enrollTTLs 是注册码可选的有效期，默认 24 小时（设计 27.2）。
var enrollTTLs = map[string]time.Duration{"1h": time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour}

// newEnrollCode 生成注册码：16 个字符 × 5 bit = 80 bit 随机熵，满足设计 27.4 的最低要求。
// 返回明文（只交给调用方一次）与待入库的哈希 / 脱敏提示。
func newEnrollCode(ttl time.Duration, now time.Time) (string, newCode) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // 系统随机源不可用时无法安全生成凭证，宁可失败
	}
	var sb strings.Builder
	sb.WriteString("ENR")
	for i, c := range b {
		if i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(crockford[c&31])
	}
	code := sb.String()
	return code, newCode{hash: HashToken(code), hint: code[:8] + "-****", expiresAt: now.Add(ttl).Unix()}
}

// normalizeEnrollCode 去掉首尾空白并转为大写，容忍用户手工输入时的大小写差异。
func normalizeEnrollCode(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// enrollBody 是 POST /api/v1/agent/enroll 的请求体（设计 27.6.2）。
// 【兼容】不拒绝未知字段：新版 Agent 可能多带字段，旧面板必须照常处理（CLAUDE.md 协议约定）。
type enrollBody struct {
	EnrollCode    string `json:"enroll_code"`
	Hostname      string `json:"hostname"`
	MachineIDHash string `json:"machine_id_hash"`
	OS            string `json:"os"`
	OSVersion     string `json:"os_version"`
	Arch          string `json:"arch"`
	AgentVersion  string `json:"agent_version"`
}

// enrollResponse 是注册成功的响应。agent_token 只在这里出现一次，Agent 写入本地文件后不再传输。
type enrollResponse struct {
	ServerID   int64    `json:"server_id"`
	ServerName string   `json:"server_name"`
	AgentToken string   `json:"agent_token"`
	Warnings   []string `json:"warnings"`
}

// handleEnroll：POST /api/v1/agent/enroll，无需 Token，凭注册码认证（设计 27.6.2）。
// 成功 200；注册码无效 / 已用 / 过期 400 enroll_code_invalid；严格核对不一致 403；限流 429。
//
// 【安全】单独限流，失败过多临时封禁来源 IP（设计 27.6.5）；注册码只记录脱敏提示（设计 24.7）。
func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if ok, wait := s.enrollLimit.allow(ip); !ok {
		s.writeError(w, r, &APIError{Code: CodeRateLimited, RetryAfter: wait})
		return
	}
	var body enrollBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	code := normalizeEnrollCode(body.EnrollCode)
	hint := ""
	if len(code) >= 8 {
		hint = code[:8] + "-****"
	}
	fail := func(reason string) {
		s.enrollLimit.fail(ip)
		s.audit(r, AuditEntry{ActorType: "agent", Action: "agent.enroll", Success: false,
			Details: map[string]any{"code_hint": hint, "reason": reason, "hostname": body.Hostname}})
		s.writeError(w, r, errorf(CodeEnrollCodeInvalid, ""))
	}
	if !enrollCodeFormat.MatchString(code) {
		fail("malformed")
		return
	}

	req := EnrollRequest{CodeHash: HashToken(code), Hostname: trimTo(body.Hostname, 253),
		MachineIDHash: trimTo(body.MachineIDHash, 80), OS: trimTo(body.OS, 64), OSVersion: trimTo(body.OSVersion, 64),
		Arch: trimTo(body.Arch, 32), AgentVersion: trimTo(body.AgentVersion, 64), SourceIP: ip}
	res, err := s.store.Enroll(req, verifyEnroll, time.Now())
	switch {
	case errors.Is(err, errEnrollInvalid):
		fail("invalid_or_expired")
		return
	case isAPIError(err, CodeForbidden):
		// 严格模式下核对不一致：注册码仍然有效，原因写入审计并返回给安装命令（设计 27.6.3）
		s.audit(r, AuditEntry{ActorType: "agent", Action: "agent.enroll", Success: false,
			Details: map[string]any{"code_hint": hint, "reason": asAPIError(err).Message, "hostname": body.Hostname}})
		s.writeError(w, r, err)
		return
	case err != nil:
		s.writeError(w, r, internalError(err))
		return
	}

	info(r).principal, info(r).principalID = "enroll", res.ServerID
	s.audit(r, AuditEntry{ActorType: "agent", ActorID: res.ServerName, Action: "agent.enroll",
		TargetType: "server", TargetID: res.ServerID, Success: true, Details: map[string]any{
			"code_hint": hint, "hostname": req.Hostname, "os": req.OS, "arch": req.Arch,
			"agent_version": req.AgentVersion, "warnings": res.Warnings, "retry": res.Retry,
			"host_changed": res.HostChanged, "revoked_tokens": res.RevokedTokens,
		}})
	s.log.Info("node enrolled", "component", "enroll", "server_id", res.ServerID, "hostname", req.Hostname,
		"ip", ip, "warnings", len(res.Warnings), "retry", res.Retry, "host_changed", res.HostChanged)
	if res.Warnings == nil {
		res.Warnings = []string{}
	}
	writeJSON(w, enrollResponse{ServerID: res.ServerID, ServerName: res.ServerName, AgentToken: res.Token, Warnings: res.Warnings})
}

// handleUnregister：POST /api/v1/agent/unregister，Agent Token 认证（设计 19.10、27.11）。
// 本地卸载时调用：吊销 Token，节点回到“待安装”，成功 204。
func (s *Server) handleUnregister(w http.ResponseWriter, r *http.Request) {
	sid := info(r).principalID
	if err := s.store.Unregister(sid, time.Now()); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.mu.Lock()
	delete(s.latest, sid) // 待安装节点不再显示旧的实时数据
	s.mu.Unlock()
	s.audit(r, AuditEntry{ActorType: "agent", ActorID: strconv.FormatInt(sid, 10), Action: "agent.unregister",
		TargetType: "server", TargetID: sid, Success: true})
	s.log.Info("node unregistered", "component", "enroll", "server_id", sid)
	w.WriteHeader(http.StatusNoContent)
}

// verifyEnroll 对比新建节点时填写的信息与注册请求（设计 27.6.3）。
// 默认只提示：NAT VPS、IPv6-only、经代理出网的主机来源地址经常与填写值不同；
// 节点设为 strict 时任一项不一致即拒绝。
func verifyEnroll(n *ServerRow, req EnrollRequest) ([]string, error) {
	var mismatch []string
	if n.ExpectedHostname != "" && !strings.EqualFold(n.ExpectedHostname, req.Hostname) {
		mismatch = append(mismatch, "主机名不一致：填写 "+n.ExpectedHostname+"，实际 "+orDash(req.Hostname))
	}
	ipv4, ipv6 := splitIP(req.SourceIP)
	if n.ExpectedIPv4 != "" && ipv4 != "" && n.ExpectedIPv4 != ipv4 {
		mismatch = append(mismatch, "IPv4 不一致：填写 "+n.ExpectedIPv4+"，实际 "+ipv4)
	}
	if n.ExpectedIPv6 != "" && ipv6 != "" && !sameIP(n.ExpectedIPv6, ipv6) {
		mismatch = append(mismatch, "IPv6 不一致：填写 "+n.ExpectedIPv6+"，实际 "+ipv6)
	}
	if len(mismatch) > 0 && n.VerifyMode == "strict" {
		return nil, errorf(CodeForbidden, "主机信息与节点设置不一致，已拒绝注册："+strings.Join(mismatch, "；"))
	}
	return mismatch, nil
}

func sameIP(a, b string) bool {
	x, y := net.ParseIP(a), net.ParseIP(b)
	return x != nil && y != nil && x.Equal(y)
}

func orDash(s string) string {
	if s == "" {
		return "（空）"
	}
	return s
}

// trimTo 截断过长的字符串，防止异常请求撑大数据库字段。
func trimTo(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// enrollLimiter 是注册接口的按 IP 限流（设计 27.6.5）：
// 每个 IP 每分钟最多 perMinute 次请求；failWindow 内失败达到 maxFails 次则封禁 ban 时长。
// 只保存在内存中，面板重启后清零，这对暴力尝试 80 bit 的注册码没有实际帮助。
type enrollLimiter struct {
	mu         sync.Mutex
	perMinute  int
	maxFails   int
	failWindow time.Duration
	ban        time.Duration
	now        func() time.Time // 测试中可替换
	ips        map[string]*ipState
}

type ipState struct {
	winStart, failStart, bannedUntil, lastSeen time.Time
	count, fails                               int
}

// newEnrollLimiter：每 IP 每分钟 10 次（设计 27.6.5）；10 分钟内失败 20 次封禁 30 分钟。
func newEnrollLimiter() *enrollLimiter {
	return &enrollLimiter{perMinute: 10, maxFails: 20, failWindow: 10 * time.Minute, ban: 30 * time.Minute,
		now: time.Now, ips: map[string]*ipState{}}
}

// allow 判断该 IP 本次请求是否放行；不放行时返回建议的等待时长（用于 Retry-After）。
func (l *enrollLimiter) allow(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	st := l.state(ip, now)
	if now.Before(st.bannedUntil) {
		return false, st.bannedUntil.Sub(now)
	}
	if now.Sub(st.winStart) >= time.Minute {
		st.winStart, st.count = now, 0
	}
	if st.count >= l.perMinute {
		return false, st.winStart.Add(time.Minute).Sub(now)
	}
	st.count++
	return true, 0
}

// fail 记录一次注册失败，达到阈值时封禁该 IP。
func (l *enrollLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	st := l.state(ip, now)
	if now.Sub(st.failStart) >= l.failWindow {
		st.failStart, st.fails = now, 0
	}
	st.fails++
	if st.fails >= l.maxFails {
		st.bannedUntil = now.Add(l.ban)
	}
}

// state 取出或创建 IP 的计数；记录过多时清理 1 小时未活动的条目，保证内存有上限。
func (l *enrollLimiter) state(ip string, now time.Time) *ipState {
	if len(l.ips) > 10000 {
		for k, v := range l.ips {
			if now.Sub(v.lastSeen) > time.Hour && now.After(v.bannedUntil) {
				delete(l.ips, k)
			}
		}
	}
	st := l.ips[ip]
	if st == nil {
		st = &ipState{winStart: now, failStart: now}
		l.ips[ip] = st
	}
	st.lastSeen = now
	return st
}
