package server

// SSL 证书到期监控（设计 33.3）：面板定期连接 host:port，记录证书的到期时间、签发者、域名与剩余天数，
// 按 30 / 14 / 7 / 3 / 1 天、当天、已过期提醒（与到期提醒同一套里程碑，设计 1.2.5），证书无效时提醒一次。
//
// 【安全】始终校验证书（约束 6，不使用 InsecureSkipVerify）：校验失败时，从 tls.CertificateVerificationError
// 中读取未通过校验的证书，只用来显示到期时间与失败原因，不在这个连接上收发任何数据。

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	sslCheckEvery = 12 * time.Hour // 每个证书 12 小时检查一次（每小时的提醒循环中挑出到期需要检查的）
	maxSSLMonitor = 200
)

// SSLMonitor 是一行 ssl_monitors。
type SSLMonitor struct {
	ID        int64    `json:"id"`
	Host      string   `json:"host"`
	Port      int      `json:"port"`
	Note      string   `json:"note"`
	NotBefore int64    `json:"not_before"`
	NotAfter  int64    `json:"not_after"` // 0 表示还没有读到证书
	Issuer    string   `json:"issuer"`
	Subject   string   `json:"subject"`
	SANs      []string `json:"sans"`
	Valid     bool     `json:"valid"`      // 证书链、域名与有效期都通过校验
	LastError string   `json:"last_error"` // 无法连接或校验失败的原因
	CheckedAt int64    `json:"checked_at"`
	CreatedAt int64    `json:"created_at"`
}

const sslCols = `id, host, port, note, not_before, not_after, issuer, subject, sans, valid, last_error, checked_at, created_at`

func scanSSL(sc interface{ Scan(...any) error }) (SSLMonitor, error) {
	var m SSLMonitor
	var sans string
	err := sc.Scan(&m.ID, &m.Host, &m.Port, &m.Note, &m.NotBefore, &m.NotAfter, &m.Issuer, &m.Subject, &sans, &m.Valid,
		&m.LastError, &m.CheckedAt, &m.CreatedAt)
	m.SANs = []string{}
	if sans != "" {
		m.SANs = strings.Split(sans, ",")
	}
	return m, err
}

func (s *Store) ListSSLMonitors() ([]SSLMonitor, error) {
	rows, err := s.DB.Query(`SELECT ` + sslCols + ` FROM ssl_monitors ORDER BY not_after = 0, not_after, host`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SSLMonitor{}
	for rows.Next() {
		m, err := scanSSL(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

var errNoSSL = errorf(CodeNotFound, "证书监控不存在或已删除")

func (s *Store) GetSSLMonitor(id int64) (SSLMonitor, error) {
	m, err := scanSSL(s.DB.QueryRow(`SELECT `+sslCols+` FROM ssl_monitors WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return m, errNoSSL
	}
	return m, err
}

func (s *Store) CreateSSLMonitor(m *SSLMonitor, now time.Time) error {
	res, err := s.DB.Exec(`INSERT INTO ssl_monitors (host, port, note, created_at) VALUES (?,?,?,?)`, m.Host, m.Port, m.Note, now.Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return errorf(CodeConflict, "已在监控这个地址")
		}
		return err
	}
	m.ID, _ = res.LastInsertId()
	m.CreatedAt = now.Unix()
	return nil
}

func (s *Store) DeleteSSLMonitor(id int64) error {
	res, err := s.DB.Exec(`DELETE FROM ssl_monitors WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoSSL
	}
	return nil
}

func (s *Store) SaveSSLResult(m SSLMonitor) error {
	_, err := s.DB.Exec(`UPDATE ssl_monitors SET not_before = ?, not_after = ?, issuer = ?, subject = ?, sans = ?, valid = ?,
		last_error = ?, checked_at = ? WHERE id = ?`, m.NotBefore, m.NotAfter, m.Issuer, m.Subject, strings.Join(m.SANs, ","), m.Valid,
		m.LastError, m.CheckedAt, m.ID)
	return err
}

// ---- 检查 ----

// certName 取证书主体或签发者的可读名称：CN，没有时取组织
func certName(n interface {
	String() string
}, cn string, org []string) string {
	if cn != "" {
		return cn
	}
	if len(org) > 0 {
		return org[0]
	}
	return n.String()
}

// verifyErrorText 把证书校验错误转为中文说明。
func verifyErrorText(err error) string {
	var inv x509.CertificateInvalidError
	var ua x509.UnknownAuthorityError
	var hn x509.HostnameError
	switch {
	case errors.As(err, &inv) && inv.Reason == x509.Expired:
		return "证书已过期或尚未生效"
	case errors.As(err, &ua), strings.Contains(err.Error(), "not trusted"): // macOS 系统校验器的说法不同
		return "证书不受信任（自签名或缺少中间证书）"
	case errors.As(err, &hn):
		return "证书中的域名与 " + hn.Host + " 不匹配"
	}
	return "证书校验失败：" + err.Error()
}

// checkSSL 连接 host:port 读取证书。roots 为 nil 时使用系统根证书（测试中替换）。
func checkSSL(ctx context.Context, host string, port int, roots *x509.CertPool, now time.Time) SSLMonitor {
	r := SSLMonitor{Host: host, Port: port, CheckedAt: now.Unix(), SANs: []string{}}
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config: &tls.Config{ServerName: host, RootCAs: roots, MinVersion: tls.VersionTLS12}}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var leaf *x509.Certificate
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err == nil {
		leaf = conn.(*tls.Conn).ConnectionState().PeerCertificates[0]
		conn.Close()
		r.Valid = true
	} else {
		var ve *tls.CertificateVerificationError
		if errors.As(err, &ve) && len(ve.UnverifiedCertificates) > 0 {
			leaf = ve.UnverifiedCertificates[0] // 只读取信息，连接已失败（约束 6）
			r.LastError = verifyErrorText(ve.Err)
		} else {
			var oe *net.OpError
			msg := err.Error()
			if errors.As(err, &oe) && oe.Err != nil {
				msg = oe.Err.Error()
			}
			r.LastError = "无法连接：" + msg
			return r
		}
	}
	r.NotBefore, r.NotAfter = leaf.NotBefore.Unix(), leaf.NotAfter.Unix()
	r.Issuer = certName(leaf.Issuer, leaf.Issuer.CommonName, leaf.Issuer.Organization)
	r.Subject = certName(leaf.Subject, leaf.Subject.CommonName, leaf.Subject.Organization)
	for i, n := range leaf.DNSNames {
		if i >= 20 {
			break
		}
		r.SANs = append(r.SANs, n)
	}
	return r
}

// runSSLCheck 检查一个监控并保存结果。
func (s *Server) runSSLCheck(ctx context.Context, m SSLMonitor, now time.Time) SSLMonitor {
	r := checkSSL(ctx, m.Host, m.Port, s.sslRoots, now)
	r.ID, r.Note, r.CreatedAt = m.ID, m.Note, m.CreatedAt
	if r.NotAfter == 0 && m.NotAfter != 0 { // 暂时连不上：保留上次读到的证书信息
		r.NotBefore, r.NotAfter, r.Issuer, r.Subject, r.SANs = m.NotBefore, m.NotAfter, m.Issuer, m.Subject, m.SANs
	}
	if err := s.store.SaveSSLResult(r); err != nil {
		s.log.Error("save ssl result failed", "component", "ssl", "err", err)
	}
	return r
}

// checkSSLDue 在提醒循环中调用：检查超过 12 小时未检查的证书。
func (s *Server) checkSSLDue(ctx context.Context, now time.Time) {
	list, err := s.store.ListSSLMonitors()
	if err != nil {
		return
	}
	for _, m := range list {
		if now.Unix()-m.CheckedAt >= int64(sslCheckEvery/time.Second) {
			s.runSSLCheck(ctx, m, now)
		}
	}
}

// sslReminders：到期按里程碑提醒（设计 33.3、1.2.5）；证书无效（非过期原因）时每种错误提醒一次。
func (s *Server) sslReminders(now time.Time) {
	list, err := s.store.ListSSLMonitors()
	if err != nil {
		return
	}
	for _, m := range list {
		label := "证书 " + m.Host
		if m.Port != 443 {
			label += ":" + strconv.Itoa(m.Port)
		}
		if m.NotAfter > 0 {
			exp := time.Unix(m.NotAfter, 0).In(time.Local)
			date := exp.Format("2006-01-02")
			days := calendarDays(now.In(time.Local), time.Date(exp.Year(), exp.Month(), exp.Day(), 0, 0, 0, 0, time.Local))
			if b, ok := expireBracket(days); ok {
				s.remind(fmt.Sprintf("ssl:%d:%d:%d", m.ID, m.NotAfter, b), notifyMessage{ServerName: label, Type: "ssl_expire",
					Severity: expireSeverity(b), Message: "SSL 证书" + expireText(date, days)}, now)
			}
		}
		if m.LastError != "" && m.NotAfter > now.Unix() && !strings.HasPrefix(m.LastError, "无法连接") {
			h := sha256.Sum256([]byte(m.LastError))
			s.remind(fmt.Sprintf("ssl_invalid:%d:%s", m.ID, hex.EncodeToString(h[:6])), notifyMessage{ServerName: label,
				Type: "ssl_invalid", Severity: SeverityWarning, Message: m.LastError}, now)
		}
	}
}

// ---- 接口（Web 管理员） ----

var sslHost = regexp.MustCompile(`^(?i)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9-]{2,63}$`)

// parseSSLTarget 接受 example.com、example.com:8443 或粘贴的 https://example.com/path。
func parseSSLTarget(raw string, port int) (string, int, string) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		if u, err := url.Parse(raw); err == nil {
			raw = u.Host
		}
	}
	host := raw
	if h, p, err := net.SplitHostPort(raw); err == nil {
		host = h
		if n, err := strconv.Atoi(p); err == nil && port == 0 {
			port = n
		}
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if port == 0 {
		port = 443
	}
	if net.ParseIP(host) == nil && !sslHost.MatchString(host) {
		return "", 0, "请填写域名，如 example.com（也可以粘贴网址）"
	}
	if port < 1 || port > 65535 {
		return "", 0, "端口为 1～65535"
	}
	return host, port, ""
}

func (s *Server) handleSSLMonitors(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSSLMonitors()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeList(w, list, "", nil)
}

// handleCreateSSLMonitor：POST /api/v1/ssl-monitors，admin。添加后立即检查一次（最多 15 秒）。
func (s *Server) handleCreateSSLMonitor(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Host string `json:"host"`
		Port int    `json:"port"`
		Note string `json:"note"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	host, port, msg := parseSSLTarget(b.Host, b.Port)
	var fe []FieldError
	if msg != "" {
		fe = append(fe, FieldError{Field: "host", Message: msg})
	}
	b.Note = strings.TrimSpace(b.Note)
	if utf8.RuneCountInString(b.Note) > 100 {
		fe = append(fe, FieldError{Field: "note", Message: "备注最多 100 个字符"})
	}
	if fe != nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	if list, err := s.store.ListSSLMonitors(); err == nil && len(list) >= maxSSLMonitor {
		s.writeError(w, r, errorf(CodeConflict, fmt.Sprintf("最多监控 %d 个证书", maxSSLMonitor)))
		return
	}
	now := time.Now()
	m := SSLMonitor{Host: host, Port: port, Note: b.Note}
	if err := s.store.CreateSSLMonitor(&m, now); err != nil {
		s.writeError(w, r, err)
		return
	}
	m = s.runSSLCheck(r.Context(), m, now)
	s.audit(r, AuditEntry{ActorType: "admin", Action: "ssl_monitor.create", Success: true,
		Details: map[string]any{"id": m.ID, "host": host, "port": port}})
	writeJSONStatus(w, http.StatusCreated, m)
}

// handleCheckSSLMonitor：POST /api/v1/ssl-monitors/{id}/check，admin。立即重新检查。
func (s *Server) handleCheckSSLMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	m, err := s.store.GetSSLMonitor(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, s.runSSLCheck(r.Context(), m, time.Now()))
}

// handleDeleteSSLMonitor：DELETE /api/v1/ssl-monitors/{id}，admin。
func (s *Server) handleDeleteSSLMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	m, err := s.store.GetSSLMonitor(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.store.DeleteSSLMonitor(id); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "ssl_monitor.delete", Success: true,
		Details: map[string]any{"id": id, "host": m.Host, "port": m.Port}})
	w.WriteHeader(http.StatusNoContent)
}
