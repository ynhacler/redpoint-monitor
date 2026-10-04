package server

// 只读 API Key（设计 45.2）：脚本、Grafana、Home Assistant 等读取节点、指标、流量与告警。
//
// 【安全】
//   - 与 Web 会话、Agent Token、注册码互不通用（约束 3）：只能访问 accessRead 路由，不能修改任何配置，
//     不能创建或吊销 Key，不能访问 /agent/* 与 /auth/*
//   - 创建时显示一次，只保存 SHA-256 哈希（约束 4）；日志按 api_ 前缀脱敏（24.7）
//   - 只接受 Authorization: Bearer 头，不接受查询串（27.1）
//   - 节点范围：全部 / 分组 / 指定节点；范围外的节点按不存在处理（404），不泄露是否存在
//   - 每个 Key 每分钟 120 次；创建、吊销、首次使用记入审计日志

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	apiKeyRatePerMinute = 120
	apiKeyTouchEvery    = time.Minute // last_used_at 最多每分钟写一次库
)

// apiScope 是 API Key 可访问的节点范围。
type apiScope struct {
	Type  string // all / group / servers
	Group string
	IDs   map[int64]bool
}

// APIKey 是一行 api_keys（不含哈希）。
type APIKey struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Hint       string `json:"hint"`
	ScopeType  string `json:"scope_type"`
	ScopeValue string `json:"scope_value"`
	ExpiresAt  int64  `json:"expires_at"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt int64  `json:"last_used_at"`
	RevokedAt  int64  `json:"revoked_at"`
}

func (k APIKey) scope() *apiScope {
	sc := &apiScope{Type: k.ScopeType, Group: k.ScopeValue}
	if k.ScopeType == "servers" {
		sc.IDs = map[int64]bool{}
		for _, s := range strings.Split(k.ScopeValue, ",") {
			if id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
				sc.IDs[id] = true
			}
		}
	}
	return sc
}

const apiKeyCols = `id, name, hint, scope_type, scope_value, expires_at, created_by, created_at, last_used_at, revoked_at`

func scanAPIKey(sc interface{ Scan(...any) error }) (APIKey, error) {
	var k APIKey
	err := sc.Scan(&k.ID, &k.Name, &k.Hint, &k.ScopeType, &k.ScopeValue, &k.ExpiresAt, &k.CreatedBy, &k.CreatedAt,
		&k.LastUsedAt, &k.RevokedAt)
	return k, err
}

// CreateAPIKey 生成新 Key 并保存哈希，返回完整 Key（只此一次）。
func (s *Store) CreateAPIKey(k *APIKey, now time.Time) (string, error) {
	tok := NewToken(PrefixAPIKey)
	k.Hint = tok[len(tok)-4:]
	k.CreatedAt = now.Unix()
	res, err := s.DB.Exec(`INSERT INTO api_keys (name, token_hash, hint, scope_type, scope_value, expires_at, created_by, created_at)
		VALUES (?,?,?,?,?,?,?,?)`, k.Name, HashToken(tok), k.Hint, k.ScopeType, k.ScopeValue, k.ExpiresAt, k.CreatedBy, k.CreatedAt)
	if err != nil {
		return "", err
	}
	k.ID, _ = res.LastInsertId()
	return tok, nil
}

func (s *Store) ListAPIKeys() ([]APIKey, error) {
	rows, err := s.DB.Query(`SELECT ` + apiKeyCols + ` FROM api_keys ORDER BY revoked_at <> 0, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

var errNoAPIKey = errorf(CodeNotFound, "API Key 不存在或已吊销")

// RevokeAPIKey 吊销 Key（保留记录，便于审计）。
func (s *Store) RevokeAPIKey(id int64, now time.Time) (APIKey, error) {
	res, err := s.DB.Exec(`UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at = 0`, now.Unix(), id)
	if err != nil {
		return APIKey{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return APIKey{}, errNoAPIKey
	}
	return scanAPIKey(s.DB.QueryRow(`SELECT `+apiKeyCols+` FROM api_keys WHERE id = ?`, id))
}

// LookupAPIKey 按完整 Key 查找有效（未吊销、未过期）的 Key；无效时返回 nil。
func (s *Store) LookupAPIKey(tok string, now time.Time) (*APIKey, error) {
	k, err := scanAPIKey(s.DB.QueryRow(`SELECT `+apiKeyCols+` FROM api_keys
		WHERE token_hash = ? AND revoked_at = 0 AND (expires_at = 0 OR expires_at > ?)`, HashToken(tok), now.Unix()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func (s *Store) TouchAPIKey(id int64, now time.Time) error {
	_, err := s.DB.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, now.Unix(), id)
	return err
}

// apiKeyLimiter 是每个 Key 每分钟的请求计数（固定窗口）与 last_used_at 的写库节流。
type apiKeyLimiter struct {
	mu      sync.Mutex
	windows map[int64]*apiKeyWindow
}

type apiKeyWindow struct {
	start   time.Time
	count   int
	touched time.Time
}

// allow 返回本次请求是否允许，以及是否需要更新 last_used_at。
func (l *apiKeyLimiter) allow(id int64, now time.Time) (ok bool, wait time.Duration, touch bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.windows == nil {
		l.windows = map[int64]*apiKeyWindow{}
	}
	w := l.windows[id]
	if w == nil {
		w = &apiKeyWindow{start: now}
		l.windows[id] = w
	}
	if now.Sub(w.start) >= time.Minute {
		w.start, w.count = now, 0
	}
	if w.count >= apiKeyRatePerMinute {
		return false, time.Minute - now.Sub(w.start), false
	}
	w.count++
	if now.Sub(w.touched) >= apiKeyTouchEvery {
		w.touched, touch = now, true
	}
	return true, 0, touch
}

// read 是 accessRead 路由的认证：Web 管理员会话（与 admin 相同）或只读 API Key（设计 45.2）。
func (s *Server) read(h http.HandlerFunc) http.Handler {
	admin := s.admin(h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if !strings.HasPrefix(tok, PrefixAPIKey) {
			admin.ServeHTTP(w, r) // 会话 Cookie；没有时由 admin 返回 401
			return
		}
		now := time.Now()
		k, err := s.store.LookupAPIKey(tok, now)
		if err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		if k == nil {
			s.writeError(w, r, errorf(CodeUnauthorized, "API Key 无效、已吊销或已过期"))
			return
		}
		ok, wait, touch := s.apiKeys.allow(k.ID, now)
		if !ok {
			s.writeError(w, r, &APIError{Code: CodeRateLimited, Message: "API Key 请求过于频繁（每分钟 120 次）", RetryAfter: wait})
			return
		}
		ri := info(r)
		ri.principal, ri.principalID, ri.apiScope = "apikey", k.ID, k.scope()
		if touch {
			if k.LastUsedAt == 0 {
				s.audit(r, AuditEntry{ActorType: "apikey", ActorID: strconv.FormatInt(k.ID, 10), Action: "api_key.first_use",
					TargetType: "api_key", TargetID: k.ID, Success: true, Details: map[string]any{"name": k.Name}})
			}
			if err := s.store.TouchAPIKey(k.ID, now); err != nil {
				s.log.Warn("touch api key failed", "component", "auth", "err", err)
			}
		}
		h(w, r)
	})
}

// scopeAllows 判断当前请求能否访问节点 row：Web 管理员可访问全部；API Key 按其范围。
func scopeAllows(r *http.Request, row ServerRow) bool {
	sc := info(r).apiScope
	if sc == nil {
		return true
	}
	switch sc.Type {
	case "all":
		return true
	case "group":
		return row.Group == sc.Group
	case "servers":
		return sc.IDs[row.ID]
	}
	return false
}

// requireServerInScope 用于 /servers/{id}/… 的只读接口：节点不存在或不在 Key 的范围内都返回 404。
func (s *Server) requireServerInScope(w http.ResponseWriter, r *http.Request, id int64) bool {
	if info(r).apiScope == nil {
		return true
	}
	row, err := s.store.GetServer(id)
	if err != nil || !scopeAllows(r, *row) {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return false
	}
	return true
}

// ---- 管理接口（Web 管理员） ----

type apiKeyBody struct {
	Name       string  `json:"name"`
	ScopeType  string  `json:"scope_type"`
	Group      string  `json:"group"`
	ServerIDs  []int64 `json:"server_ids"`
	ExpiresInD int     `json:"expires_in_days"` // 0 表示不过期
}

// handleAPIKeys：GET /api/v1/api-keys，admin。
func (s *Server) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.store.ListAPIKeys()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeList(w, keys, "", nil)
}

// handleCreateAPIKey：POST /api/v1/api-keys，admin + 重新验证。响应中的 key 只返回这一次。
func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	if err := requireReauth(r); err != nil {
		s.writeError(w, r, err)
		return
	}
	var b apiKeyBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	k := APIKey{Name: strings.TrimSpace(b.Name), ScopeType: b.ScopeType}
	if se := info(r).session; se != nil {
		k.CreatedBy = se.User.Username
	}
	var fe []FieldError
	if k.Name == "" || utf8.RuneCountInString(k.Name) > 64 {
		fe = append(fe, FieldError{Field: "name", Message: "名称为 1～64 个字符"})
	}
	switch k.ScopeType {
	case "", "all":
		k.ScopeType = "all"
	case "group":
		k.ScopeValue = strings.TrimSpace(b.Group)
		if k.ScopeValue == "" {
			fe = append(fe, FieldError{Field: "group", Message: "请选择分组"})
		}
	case "servers":
		ids := slices.Compact(slices.Sorted(slices.Values(b.ServerIDs)))
		if len(ids) == 0 || len(ids) > 1000 {
			fe = append(fe, FieldError{Field: "server_ids", Message: "请选择 1～1000 个节点"})
		}
		parts := make([]string, 0, len(ids))
		for _, id := range ids {
			if _, err := s.store.GetServer(id); err != nil {
				fe = append(fe, FieldError{Field: "server_ids", Message: "节点 " + strconv.FormatInt(id, 10) + " 不存在"})
				break
			}
			parts = append(parts, strconv.FormatInt(id, 10))
		}
		k.ScopeValue = strings.Join(parts, ",")
	default:
		fe = append(fe, FieldError{Field: "scope_type", Message: "范围只能是 all、group 或 servers"})
	}
	if b.ExpiresInD < 0 || b.ExpiresInD > 3650 {
		fe = append(fe, FieldError{Field: "expires_in_days", Message: "有效期为 0（不过期）～3650 天"})
	}
	if len(fe) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	now := time.Now()
	if b.ExpiresInD > 0 {
		k.ExpiresAt = now.Add(time.Duration(b.ExpiresInD) * 24 * time.Hour).Unix()
	}
	tok, err := s.store.CreateAPIKey(&k, now)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "api_key.create", TargetType: "api_key", TargetID: k.ID, Success: true,
		Details: map[string]any{"name": k.Name, "scope_type": k.ScopeType, "scope_value": k.ScopeValue, "expires_at": k.ExpiresAt}})
	writeJSONStatus(w, http.StatusCreated, map[string]any{"key": tok, "api_key": k})
}

// handleRevokeAPIKey：DELETE /api/v1/api-keys/{id}，admin。立即生效；记录保留。
func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.writeError(w, r, errorf(CodeBadRequest, "编号格式不正确"))
		return
	}
	k, err := s.store.RevokeAPIKey(id, time.Now())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "api_key.revoke", TargetType: "api_key", TargetID: id, Success: true,
		Details: map[string]any{"name": k.Name}})
	w.WriteHeader(http.StatusNoContent)
}

// handleVersion：GET /api/v1/version，Web 会话或 API Key。
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"version": s.version})
}
