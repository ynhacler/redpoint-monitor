package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Web 登录与会话（设计 8.1、17.1、17.4、19.1）。
//
// 会话：随机令牌放在 HttpOnly Cookie 中，数据库只存哈希；空闲 12 小时或创建满 7 天失效。
// CSRF：所有修改类请求必须带 X-CSRF-Token，其值由会话令牌派生，服务端无需保存。
// 【安全】页面脚本读不到会话 Cookie（HttpOnly），跨站请求带不上它（SameSite=Strict），
// 即使带上也拿不到 CSRF 值（同源策略），三层叠加。

const (
	sessionCookie = "vpsmon_session"
	sessionIdle   = 12 * time.Hour     // 空闲超时（设计 17.1）
	sessionMax    = 7 * 24 * time.Hour // 最长有效期（设计 17.1）
	reauthValid   = 10 * time.Minute   // 敏感操作重新输入密码后的免验证时长（设计 17.4）
	touchEvery    = time.Minute        // 最近活动时间的写入间隔：避免每个请求都写库
	csrfHeader    = "X-CSRF-Token"
	PrefixSession = "ses_"
)

var errNoUser = errors.New("user not found")

// User 是管理员账号（设计 18.1）。MVP 只有单管理员（设计 35.2）。
type User struct {
	ID                 int64
	Username           string
	PasswordHash       string
	MustChangePassword bool
}

// session 是一次登录会话。
type session struct {
	ID          int64
	User        User
	LastSeenAt  int64
	ExpiresAt   int64
	ReauthUntil int64
}

// csrfFor 由会话令牌派生 CSRF 值：HMAC-SHA256(令牌, "csrf")。
// 【安全】派生值无法反推出会话令牌；数据库中只有令牌哈希，泄露后也无法算出 CSRF 值。
func csrfFor(token string) string {
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte("csrf"))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// ---- 存储 ----

// SetAdminPassword 创建管理员账号或重置其密码，并吊销该账号的全部会话（设计 17.2 本地 CLI 恢复途径）。
func (s *Store) SetAdminPassword(username, hash string, mustChange bool, now time.Time) (created bool, err error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRow(`SELECT id FROM users WHERE username = ?`, username).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.Exec(`INSERT INTO users (username, password_hash, must_change_password, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)`, username, hash, mustChange, now.Unix(), now.Unix())
		created = true
	case err == nil:
		_, err = tx.Exec(`UPDATE users SET password_hash = ?, must_change_password = ?, updated_at = ? WHERE id = ?`,
			hash, mustChange, now.Unix(), id)
		if err == nil {
			_, err = tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, id)
		}
	}
	if err != nil {
		return false, err
	}
	return created, tx.Commit()
}

// UserCount 返回账号数量；为 0 时面板无法登录，启动日志提示执行 reset-password。
func (s *Store) UserCount() (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// UserByName 按用户名（不区分大小写）查找账号。
func (s *Store) UserByName(name string) (*User, error) {
	var u User
	err := s.DB.QueryRow(`SELECT id, username, password_hash, must_change_password FROM users WHERE username = ? AND status = 'active'`,
		name).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.MustChangePassword)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoUser
	}
	return &u, err
}

// CreateSession 新建会话，返回会话令牌明文（只写进 Cookie，不保存）。
func (s *Store) CreateSession(userID int64, ip, ua string, now time.Time) (string, error) {
	tok := NewToken(PrefixSession)
	if len(ua) > 256 {
		ua = ua[:256]
	}
	_, err := s.DB.Exec(`INSERT INTO sessions (token_hash, user_id, created_at, last_seen_at, expires_at, client_ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, HashToken(tok), userID, now.Unix(), now.Unix(), now.Add(sessionMax).Unix(), ip, ua)
	if err != nil {
		return "", err
	}
	_, err = s.DB.Exec(`UPDATE users SET last_login_at = ? WHERE id = ?`, now.Unix(), userID)
	return tok, err
}

// LookupSession 按令牌查找有效会话；不存在、空闲超时或已过期时返回 nil（过期的顺便删除）。
func (s *Store) LookupSession(tok string, now time.Time) (*session, error) {
	var se session
	err := s.DB.QueryRow(`SELECT s.id, s.last_seen_at, s.expires_at, s.reauth_until, u.id, u.username, u.must_change_password
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ? AND u.status = 'active'`, HashToken(tok)).
		Scan(&se.ID, &se.LastSeenAt, &se.ExpiresAt, &se.ReauthUntil, &se.User.ID, &se.User.Username, &se.User.MustChangePassword)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if now.Unix() >= se.ExpiresAt || now.Sub(time.Unix(se.LastSeenAt, 0)) >= sessionIdle {
		_, err := s.DB.Exec(`DELETE FROM sessions WHERE id = ?`, se.ID)
		return nil, err
	}
	if now.Sub(time.Unix(se.LastSeenAt, 0)) >= touchEvery {
		if _, err := s.DB.Exec(`UPDATE sessions SET last_seen_at = ? WHERE id = ?`, now.Unix(), se.ID); err != nil {
			return nil, err
		}
	}
	return &se, nil
}

// DeleteSession 删除一个会话（退出登录）。
func (s *Store) DeleteSession(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// ChangePassword 修改密码，并让该账号的其他会话全部失效（设计 17.4）。
func (s *Store) ChangePassword(userID int64, hash string, keepSession int64, now time.Time) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE users SET password_hash = ?, must_change_password = 0, updated_at = ? WHERE id = ?`,
		hash, now.Unix(), userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ? AND id != ?`, userID, keepSession); err != nil {
		return err
	}
	return tx.Commit()
}

// SetReauth 记录敏感操作的重新验证时间。
func (s *Store) SetReauth(sessionID int64, until time.Time) error {
	_, err := s.DB.Exec(`UPDATE sessions SET reauth_until = ? WHERE id = ?`, until.Unix(), sessionID)
	return err
}

// UpdatePasswordHash 用于登录时把旧参数的哈希升级为当前参数。
func (s *Store) UpdatePasswordHash(userID int64, hash string) error {
	_, err := s.DB.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, userID)
	return err
}

// PruneSessions 删除已过期的会话，由维护任务定期调用。
func (s *Store) PruneSessions(now time.Time) error {
	_, err := s.DB.Exec(`DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at <= ?`,
		now.Unix(), now.Add(-sessionIdle).Unix())
	return err
}

// ---- HTTP ----

// isHTTPS 判断浏览器与面板之间是否为 HTTPS：直接 TLS，或经同机反向代理且代理声明为 https。
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || (fromTrustedProxy(r) && r.Header.Get("X-Forwarded-Proto") == "https")
}

// setSessionCookie 写入会话 Cookie。remember 为 true 时保存 7 天，否则关闭浏览器即失效（设计 8.1 “记住登录”）。
//
// 【安全】HttpOnly、SameSite=Strict；HTTPS 下加 Secure。本地开发（回环 HTTP）时不加 Secure，
// 否则部分浏览器不会保存（设计 17.4、CLAUDE.md 约束 6 允许回环明文）。
func setSessionCookie(w http.ResponseWriter, r *http.Request, tok string, remember bool) {
	c := &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: isHTTPS(r)}
	if remember {
		c.MaxAge = int(sessionMax.Seconds())
	}
	http.SetCookie(w, c)
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: isHTTPS(r)})
}

// admin 用会话 Cookie 认证 Web 管理员（设计 17.1）。
//
// 【安全】
//   - 修改类请求（非 GET / HEAD）必须带正确的 X-CSRF-Token（设计 17.4）
//   - 使用初始 / 重置密码登录时，只允许访问 /api/v1/auth/，其余接口要求先修改密码
//   - 查询出错时返回 500，不放行，也不伪装成 401（设计 43.1）
//
// TODO(C): App Device Token，只读范围（设计 12.5）。
func (s *Server) admin(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || !strings.HasPrefix(c.Value, PrefixSession) {
			s.writeError(w, r, errorf(CodeUnauthorized, ""))
			return
		}
		se, err := s.store.LookupSession(c.Value, time.Now())
		if err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		if se == nil {
			clearSessionCookie(w, r)
			s.writeError(w, r, errorf(CodeUnauthorized, ""))
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			got := r.Header.Get(csrfHeader)
			if subtle.ConstantTimeCompare([]byte(got), []byte(csrfFor(c.Value))) != 1 {
				s.writeError(w, r, errorf(CodeForbidden, "页面已过期，请刷新后重试"))
				return
			}
		}
		if se.User.MustChangePassword && !strings.HasPrefix(r.URL.Path, "/api/v1/auth/") {
			s.writeError(w, r, errorf(CodePasswordChangeRequired, ""))
			return
		}
		ri := info(r)
		ri.principal, ri.principalID, ri.session = "admin", se.User.ID, se
		ri.sessionToken = c.Value
		h(w, r)
	})
}

// meView 是当前登录信息；csrf_token 供页面在修改类请求中放进 X-CSRF-Token。
type meView struct {
	Username           string `json:"username"`
	MustChangePassword bool   `json:"must_change_password"`
	CSRFToken          string `json:"csrf_token"`
}

func meOf(u User, tok string) meView {
	return meView{Username: u.Username, MustChangePassword: u.MustChangePassword, CSRFToken: csrfFor(tok)}
}

// decodeJSON 读取小请求体（上限 4 KB）。
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(v); err != nil {
		return &APIError{Code: CodeBadRequest, Cause: err}
	}
	return nil
}

// handleLogin：POST /api/v1/auth/login，无需认证（设计 8.2、19.1）。
// 成功 200 并写入会话 Cookie；用户名或密码错误 401（不区分哪一项错）；失败过多 429。
//
// 【安全】同一 IP 1 分钟内失败 5 次锁定 15 分钟（设计 17.4）；用户名不存在时也执行一次哈希，
// 避免通过响应时间判断用户名是否存在；成功与失败都写审计日志（设计 24.8）。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if ok, wait := s.loginLimit.allow(ip); !ok {
		s.writeError(w, r, &APIError{Code: CodeRateLimited, Message: "登录失败次数过多，请稍后再试", RetryAfter: wait})
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeError(w, r, err)
		return
	}
	name := strings.TrimSpace(body.Username)
	fail := func(reason string) {
		s.loginLimit.fail(ip)
		s.audit(r, AuditEntry{ActorType: "admin", ActorID: name, Action: "auth.login", Success: false,
			Details: map[string]any{"reason": reason}})
		s.log.Warn("login failed", "component", "auth", "ip", ip, "reason", reason)
		s.writeError(w, r, errorf(CodeUnauthorized, "用户名或密码错误"))
	}
	if len(body.Password) > maxPasswordLen {
		fail("password_too_long")
		return
	}
	u, err := s.store.UserByName(name)
	if errors.Is(err, errNoUser) {
		VerifyPassword(body.Password, dummyHash) // 与正常校验耗时一致
		fail("unknown_user")
		return
	}
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	ok, rehash, err := VerifyPassword(body.Password, u.PasswordHash)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if !ok {
		fail("bad_password")
		return
	}
	if rehash {
		if h, err := HashPassword(body.Password); err == nil {
			_ = s.store.UpdatePasswordHash(u.ID, h)
		}
	}
	tok, err := s.store.CreateSession(u.ID, ip, r.UserAgent(), time.Now())
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	setSessionCookie(w, r, tok, body.Remember)
	info(r).principal, info(r).principalID = "admin", u.ID
	s.audit(r, AuditEntry{ActorType: "admin", ActorID: u.Username, Action: "auth.login", Success: true,
		Details: map[string]any{"remember": body.Remember}})
	writeJSON(w, meOf(*u, tok))
}

// handleMe：GET /api/v1/auth/me，admin。返回当前账号与 CSRF 值；页面刷新后用它恢复登录状态。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	writeJSON(w, meOf(ri.session.User, ri.sessionToken))
}

// handleLogout：POST /api/v1/auth/logout，admin。删除当前会话，成功 204。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	if err := s.store.DeleteSession(ri.session.ID); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	clearSessionCookie(w, r)
	s.audit(r, AuditEntry{ActorType: "admin", ActorID: ri.session.User.Username, Action: "auth.logout", Success: true})
	w.WriteHeader(http.StatusNoContent)
}

// handleChangePassword：POST /api/v1/auth/password，admin。{current_password, new_password}。
// 成功 204；当前密码错误 422；新密码不符合要求 422。修改后其他会话全部失效（设计 17.4）。
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	var body struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeError(w, r, err)
		return
	}
	u, err := s.store.UserByName(ri.session.User.Username)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if ok, _, err := VerifyPassword(body.Current, u.PasswordHash); err != nil || !ok {
		s.loginLimit.fail(clientIP(r)) // 与登录共用失败计数，防止借此暴力尝试密码
		s.writeError(w, r, &APIError{Code: CodeValidationFailed,
			Details: []FieldError{{Field: "current_password", Message: "当前密码不正确"}}})
		return
	}
	if msg := validatePassword(body.New); msg != "" {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "new_password", Message: msg}}})
		return
	}
	if body.New == body.Current {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed,
			Details: []FieldError{{Field: "new_password", Message: "新密码不能与当前密码相同"}}})
		return
	}
	hash, err := HashPassword(body.New)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if err := s.store.ChangePassword(u.ID, hash, ri.session.ID, time.Now()); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", ActorID: u.Username, Action: "auth.password_change", Success: true})
	w.WriteHeader(http.StatusNoContent)
}

// handleReauth：POST /api/v1/auth/reauth，admin。{password}。敏感操作前重新输入密码，
// 之后 10 分钟内不再要求（设计 17.4）。成功 204；密码错误 422。
func (s *Server) handleReauth(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeError(w, r, err)
		return
	}
	ip := clientIP(r)
	if ok, wait := s.loginLimit.allow(ip); !ok {
		s.writeError(w, r, &APIError{Code: CodeRateLimited, Message: "失败次数过多，请稍后再试", RetryAfter: wait})
		return
	}
	u, err := s.store.UserByName(ri.session.User.Username)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if ok, _, err := VerifyPassword(body.Password, u.PasswordHash); err != nil || !ok {
		s.loginLimit.fail(ip)
		s.audit(r, AuditEntry{ActorType: "admin", ActorID: u.Username, Action: "auth.reauth", Success: false})
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "password", Message: "密码不正确"}}})
		return
	}
	if err := s.store.SetReauth(ri.session.ID, time.Now().Add(reauthValid)); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", ActorID: u.Username, Action: "auth.reauth", Success: true})
	w.WriteHeader(http.StatusNoContent)
}

// requireReauth 检查敏感操作的重新验证是否仍在有效期内（设计 17.4）。
func requireReauth(r *http.Request) error {
	se := info(r).session
	if se == nil || time.Now().Unix() >= se.ReauthUntil {
		return errorf(CodeReauthRequired, "")
	}
	return nil
}
