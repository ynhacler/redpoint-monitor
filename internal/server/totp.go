package server

// 两步验证（TOTP，设计 17.4，修订第 160 条）：Web 管理员登录时在密码之后再输入认证器 App 的 6 位验证码，
// 或一次性的恢复码。只影响 Web 登录；App、Agent、只读 API Key 各有自己的凭证，不受影响（设计 17.1）。
//
// 【安全】
//   - 算法为 RFC 6238（HMAC-SHA1、30 秒、6 位），容许前后各一个时间片；用过的时间片记在 users.totp_last_step，
//     同一个验证码不能用第二次（重放）
//   - 密钥需要用来计算验证码，只能保存原文（与密码哈希同在数据目录，备份时一并保护）；接口只在设置时返回一次
//   - 恢复码 10 个，只显示一次，只保存 SHA-256；用过即失效
//   - 密码正确后发一次性的 challenge（5 分钟、最多 5 次、绑定 IP），第二步失败与登录共用同一 IP 的失败计数与锁定
//   - 开启前要重新验证密码；关闭同样需要；忘记设备且没有恢复码时在面板主机执行 vpsmon-server admin disable-totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	totpPeriod        = 30
	totpDigits        = 6
	totpSkew          = 1 // 前后各容许的时间片数
	totpChallengeTTL  = 5 * time.Minute
	totpChallengeTry  = 5
	totpSetupTTL      = 10 * time.Minute
	totpRecoveryCount = 10
	totpIssuer        = "VPS Monitor"
)

var (
	totpCodePattern = regexp.MustCompile(`^\d{6}$`)
	b32             = base32.StdEncoding.WithPadding(base32.NoPadding)
)

// totpAt 计算第 step 个时间片的验证码（RFC 4226 动态截断）。
func totpAt(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, v%1000000)
}

// totpMatch 在 now 前后各 totpSkew 个时间片内查找与 code 相符、且晚于 lastStep 的时间片；返回该时间片。
func totpMatch(secret []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	if !totpCodePattern.MatchString(code) {
		return 0, false
	}
	cur := now.Unix() / totpPeriod
	for d := int64(-totpSkew); d <= totpSkew; d++ {
		step := cur + d
		if step <= lastStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpAt(secret, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// newRecoveryCode 生成一个恢复码，形如 k7m2-x9qp（去掉易混淆的 0 / 1 / i / l / o）。
func newRecoveryCode() string {
	const alphabet = "23456789abcdefghjkmnpqrstuvwxyz"
	out := make([]byte, 0, 9)
	var b [1]byte
	for len(out) < 9 {
		if len(out) == 4 {
			out = append(out, '-')
			continue
		}
		if _, err := rand.Read(b[:]); err != nil {
			panic(err)
		}
		// 拒绝采样：只用 0～247（31 的整数倍），各字符概率相同
		if int(b[0]) < 256/len(alphabet)*len(alphabet) {
			out = append(out, alphabet[int(b[0])%len(alphabet)])
		}
	}
	return string(out)
}

// normalizeRecovery 统一大小写、去掉空格与连字符后再加回连字符，容许用户输入时的格式差异。
func normalizeRecovery(s string) string {
	s = strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(s)))
	if len(s) != 8 {
		return ""
	}
	return s[:4] + "-" + s[4:]
}

// ---- 存储 ----

// totpState 读取账号的 TOTP 密钥（Base32，空表示未开启）与最近用过的时间片。
func (s *Store) totpState(userID int64) (secret string, lastStep int64, err error) {
	err = s.DB.QueryRow(`SELECT totp_secret, totp_last_step FROM users WHERE id = ?`, userID).Scan(&secret, &lastStep)
	return
}

// RecoveryCodesLeft 返回未使用的恢复码数量。
func (s *Store) RecoveryCodesLeft(userID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM totp_recovery WHERE user_id = ? AND used_at = 0`, userID).Scan(&n)
	return n, err
}

// EnableTOTP 保存密钥与恢复码哈希（替换旧的），step 记为已用过的时间片。
func (s *Store) EnableTOTP(userID int64, secret string, step int64, codeHashes []string, now time.Time) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE users SET totp_secret = ?, totp_last_step = ?, updated_at = ? WHERE id = ?`,
		secret, step, now.Unix(), userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM totp_recovery WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, h := range codeHashes {
		if _, err := tx.Exec(`INSERT INTO totp_recovery (user_id, code_hash, created_at) VALUES (?, ?, ?)`, userID, h, now.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DisableTOTP 关闭两步验证并删除恢复码；账号不存在时返回 errNoUser。
func (s *Store) DisableTOTP(username string, now time.Time) error {
	var id int64
	err := s.DB.QueryRow(`SELECT id FROM users WHERE username = ?`, username).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return errNoUser
	}
	if err != nil {
		return err
	}
	if _, err := s.DB.Exec(`UPDATE users SET totp_secret = '', totp_last_step = 0, updated_at = ? WHERE id = ?`, now.Unix(), id); err != nil {
		return err
	}
	_, err = s.DB.Exec(`DELETE FROM totp_recovery WHERE user_id = ?`, id)
	return err
}

// useTOTPStep 记录用过的时间片；只有比已记录的更新时才成功（并发提交同一验证码时只有一个成功）。
func (s *Store) useTOTPStep(userID, step int64) (bool, error) {
	res, err := s.DB.Exec(`UPDATE users SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?`, step, userID, step)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// useRecoveryCode 把恢复码标为已用；不存在或已用过时返回 false。
func (s *Store) useRecoveryCode(userID int64, code string, now time.Time) (bool, error) {
	res, err := s.DB.Exec(`UPDATE totp_recovery SET used_at = ? WHERE user_id = ? AND code_hash = ? AND used_at = 0`,
		now.Unix(), userID, HashToken(code))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ---- 进行中的登录与设置（只在内存中，面板重启后需重新开始） ----

type totpChallenge struct {
	userID   int64
	remember bool
	ip       string
	expires  time.Time
	tries    int
}

type totpPending struct {
	secret  []byte
	expires time.Time
	tries   int
}

// totpFlows 保存密码已通过、等待验证码的登录（键为 challenge 的哈希）与进行中的设置（键为会话 ID）。
type totpFlows struct {
	mu         sync.Mutex
	challenges map[string]*totpChallenge
	pending    map[int64]*totpPending
}

func newTOTPFlows() *totpFlows {
	return &totpFlows{challenges: map[string]*totpChallenge{}, pending: map[int64]*totpPending{}}
}

// prune 删除过期的条目（每次使用时顺便清理，数量很小）。
func (f *totpFlows) prune(now time.Time) {
	for k, c := range f.challenges {
		if now.After(c.expires) {
			delete(f.challenges, k)
		}
	}
	for k, p := range f.pending {
		if now.After(p.expires) {
			delete(f.pending, k)
		}
	}
}

func (f *totpFlows) newChallenge(userID int64, remember bool, ip string, now time.Time) string {
	tok := NewToken("tfa_")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prune(now)
	f.challenges[HashToken(tok)] = &totpChallenge{userID: userID, remember: remember, ip: ip, expires: now.Add(totpChallengeTTL)}
	return tok
}

// takeTry 取出一次尝试机会：challenge 不存在、过期、IP 不同或次数用完时返回 nil。
func (f *totpFlows) takeTry(tok, ip string, now time.Time) *totpChallenge {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prune(now)
	key := HashToken(tok)
	c := f.challenges[key]
	if c == nil || c.ip != ip {
		return nil
	}
	c.tries++
	if c.tries >= totpChallengeTry {
		delete(f.challenges, key) // 最后一次机会：之后需重新输入密码
	}
	cp := *c
	return &cp
}

func (f *totpFlows) done(tok string) {
	f.mu.Lock()
	delete(f.challenges, HashToken(tok))
	f.mu.Unlock()
}

// ---- 接口 ----

// meFor 是带两步验证状态的登录信息。
func (s *Server) meFor(u User, tok string) (meView, error) {
	v := meOf(u, tok)
	secret, _, err := s.store.totpState(u.ID)
	if err != nil {
		return v, err
	}
	if v.TOTPEnabled = secret != ""; v.TOTPEnabled {
		v.RecoveryCodesLeft, err = s.store.RecoveryCodesLeft(u.ID)
	}
	return v, err
}

// startSession 新建会话、写 Cookie 与审计，返回登录信息（密码登录与两步验证的第二步共用）。
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u User, remember bool, details map[string]any) {
	tok, err := s.store.CreateSession(u.ID, clientIP(r), r.UserAgent(), time.Now())
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	setSessionCookie(w, r, tok, remember)
	info(r).principal, info(r).principalID = "admin", u.ID
	s.audit(r, AuditEntry{ActorType: "admin", ActorID: u.Username, Action: "auth.login", Success: true, Details: details})
	v, err := s.meFor(u, tok)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, v)
}

// handleLoginTOTP：POST /api/v1/auth/login/totp，无需认证。{challenge, code}；成功同登录。
func (s *Server) handleLoginTOTP(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if ok, wait := s.loginLimit.allow(ip); !ok {
		s.writeError(w, r, &APIError{Code: CodeRateLimited, Message: "登录失败次数过多，请稍后再试", RetryAfter: wait})
		return
	}
	var body struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeError(w, r, err)
		return
	}
	now := time.Now()
	c := s.totp.takeTry(body.Challenge, ip, now)
	if c == nil {
		s.writeError(w, r, errorf(CodeTokenExpired, "登录已过期，请重新输入密码")) // 页面据此回到第一步
		return
	}
	u, err := s.store.userByID(c.userID)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	secret, lastStep, err := s.store.totpState(u.ID)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	code := strings.ReplaceAll(strings.TrimSpace(body.Code), " ", "")
	method, ok := "totp", false
	if key, kerr := b32.DecodeString(secret); kerr == nil && secret != "" {
		if step, match := totpMatch(key, code, now, lastStep); match {
			if ok, err = s.store.useTOTPStep(u.ID, step); err != nil {
				s.writeError(w, r, internalError(err))
				return
			}
		}
	}
	if !ok && secret != "" && !totpCodePattern.MatchString(code) {
		if rc := normalizeRecovery(code); rc != "" {
			method = "recovery"
			if ok, err = s.store.useRecoveryCode(u.ID, rc, now); err != nil {
				s.writeError(w, r, internalError(err))
				return
			}
		}
	}
	if !ok {
		s.loginLimit.fail(ip)
		s.audit(r, AuditEntry{ActorType: "admin", ActorID: u.Username, Action: "auth.login", Success: false,
			Details: map[string]any{"reason": "bad_" + method}})
		s.writeError(w, r, errorf(CodeUnauthorized, "验证码不正确"))
		return
	}
	s.totp.done(body.Challenge)
	s.startSession(w, r, *u, c.remember, map[string]any{"remember": c.remember, "second_factor": method})
}

// handleTOTPSetup：POST /api/v1/auth/totp/setup，admin，需重新验证密码。生成新密钥，10 分钟内用 enable 确认。
func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	if err := requireReauth(r); err != nil {
		s.writeError(w, r, err)
		return
	}
	se := info(r).session
	if secret, _, err := s.store.totpState(se.User.ID); err != nil {
		s.writeError(w, r, internalError(err))
		return
	} else if secret != "" {
		s.writeError(w, r, errorf(CodeConflict, "已开启两步验证；要更换设备请先关闭再重新开启"))
		return
	}
	key := make([]byte, 20)
	if _, err := rand.Read(key); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	now := time.Now()
	s.totp.mu.Lock()
	s.totp.prune(now)
	s.totp.pending[se.ID] = &totpPending{secret: key, expires: now.Add(totpSetupTTL)}
	s.totp.mu.Unlock()
	enc := b32.EncodeToString(key)
	q := url.Values{"secret": {enc}, "issuer": {totpIssuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	label := url.PathEscape(totpIssuer + ":" + se.User.Username)
	writeJSON(w, map[string]string{"secret": enc, "otpauth_uri": "otpauth://totp/" + label + "?" + q.Encode()})
}

// handleTOTPEnable：POST /api/v1/auth/totp/enable，admin。{code}；确认后保存密钥，返回恢复码（只显示这一次）。
func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	se := info(r).session
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeError(w, r, err)
		return
	}
	now := time.Now()
	s.totp.mu.Lock()
	s.totp.prune(now)
	p := s.totp.pending[se.ID]
	var key []byte
	if p != nil {
		key = p.secret
		if p.tries++; p.tries >= totpChallengeTry {
			delete(s.totp.pending, se.ID)
		}
	}
	s.totp.mu.Unlock()
	if key == nil {
		s.writeError(w, r, errorf(CodeConflict, "设置已过期，请重新开始"))
		return
	}
	step, ok := totpMatch(key, strings.ReplaceAll(strings.TrimSpace(body.Code), " ", ""), now, 0)
	if !ok {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed,
			Details: []FieldError{{Field: "code", Message: "验证码不正确：请输入认证器当前显示的 6 位数字，并确认手机时间准确"}}})
		return
	}
	codes := make([]string, totpRecoveryCount)
	hashes := make([]string, totpRecoveryCount)
	for i := range codes {
		codes[i] = newRecoveryCode()
		hashes[i] = HashToken(codes[i])
	}
	if err := s.store.EnableTOTP(se.User.ID, b32.EncodeToString(key), step, hashes, now); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.totp.mu.Lock()
	delete(s.totp.pending, se.ID)
	s.totp.mu.Unlock()
	s.audit(r, AuditEntry{ActorType: "admin", ActorID: se.User.Username, Action: "auth.totp_enable", Success: true})
	writeJSON(w, map[string][]string{"recovery_codes": codes})
}

// handleTOTPDisable：POST /api/v1/auth/totp/disable，admin，需重新验证密码。成功 204。
func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	if err := requireReauth(r); err != nil {
		s.writeError(w, r, err)
		return
	}
	se := info(r).session
	if err := s.store.DisableTOTP(se.User.Username, time.Now()); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", ActorID: se.User.Username, Action: "auth.totp_disable", Success: true})
	w.WriteHeader(http.StatusNoContent)
}
