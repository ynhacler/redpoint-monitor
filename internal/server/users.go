package server

// 查看者账号（修订第 161 条）：管理员可新建只读的 Web 账号，给同事或客户看节点状态。
//
// 【安全】
//   - 查看者能访问的接口与只读 API Key 相同（accessRead）：节点、指标、流量、测速、可达性、告警列表；
//     另可管理自己的账号（accessSelf：改密码、两步验证、登录设备）。管理接口与静音 / 维护一律 403，在鉴权中间件中判断
//   - 新建、重置密码、删除都要求管理员先重新验证密码；初始 / 重置的密码随机生成、只显示一次，首次登录必须修改
//   - 只能新建、重置、删除查看者；管理员账号只能用 CLI 重置（设计 17.2）

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const maxViewers = 20

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,32}$`)

// userView 是账号列表中的一项（不含密码哈希与两步验证密钥）。
type userView struct {
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	Role               string `json:"role"`
	MustChangePassword bool   `json:"must_change_password"`
	TOTPEnabled        bool   `json:"totp_enabled"`
	CreatedAt          int64  `json:"created_at"`
	LastLoginAt        int64  `json:"last_login_at"`
}

// ListUsers 返回全部启用中的账号，管理员在前。
func (s *Store) ListUsers() ([]userView, error) {
	rows, err := s.DB.Query(`SELECT id, username, role, must_change_password, totp_secret != '', created_at, last_login_at
		FROM users WHERE status = 'active' ORDER BY role = 'admin' DESC, username COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []userView{}
	for rows.Next() {
		var u userView
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.MustChangePassword, &u.TOTPEnabled, &u.CreatedAt, &u.LastLoginAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// errUserExists 表示用户名已被使用（不区分大小写）。
var errUserExists = errors.New("username taken")

// errTooManyViewers 表示查看者数量已达上限。
var errTooManyViewers = errors.New("too many viewers")

// CreateViewer 新建查看者，返回新账号。
func (s *Store) CreateViewer(username, hash string, now time.Time) (userView, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return userView{}, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE role = ?`, roleViewer).Scan(&n); err != nil {
		return userView{}, err
	}
	if n >= maxViewers {
		return userView{}, errTooManyViewers
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE username = ?`, username).Scan(&n); err != nil {
		return userView{}, err
	}
	if n > 0 {
		return userView{}, errUserExists
	}
	res, err := tx.Exec(`INSERT INTO users (username, password_hash, role, must_change_password, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?)`, username, hash, roleViewer, now.Unix(), now.Unix())
	if err != nil {
		return userView{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return userView{}, err
	}
	return userView{ID: id, Username: username, Role: roleViewer, MustChangePassword: true, CreatedAt: now.Unix()}, tx.Commit()
}

// viewerByID 读取查看者；不存在或不是查看者时返回 errNoUser（管理员不能通过这些接口修改）。
func (s *Store) viewerByID(id int64) (string, error) {
	var name string
	err := s.DB.QueryRow(`SELECT username FROM users WHERE id = ? AND role = ?`, id, roleViewer).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNoUser
	}
	return name, err
}

// DeleteViewer 删除查看者及其会话与恢复码。
func (s *Store) DeleteViewer(id int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM sessions WHERE user_id = ?`, `DELETE FROM totp_recovery WHERE user_id = ?`,
		`DELETE FROM users WHERE id = ? AND role = 'viewer'`} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ResetViewerPassword 设置新密码（首次登录须修改）并退出其全部会话。
func (s *Store) ResetViewerPassword(id int64, hash string, now time.Time) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE users SET password_hash = ?, must_change_password = 1, updated_at = ? WHERE id = ? AND role = 'viewer'`,
		hash, now.Unix(), id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// handleUsers：GET /api/v1/users，admin。
func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListUsers()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeList(w, list, "", nil)
}

// handleCreateUser：POST /api/v1/users，admin，需重新验证密码。{username}；201 返回账号与初始密码（只显示这一次）。
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if err := requireReauth(r); err != nil {
		s.writeError(w, r, err)
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeError(w, r, err)
		return
	}
	name := strings.TrimSpace(body.Username)
	if !usernamePattern.MatchString(name) {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed,
			Details: []FieldError{{Field: "username", Message: "3～32 位字母、数字、点、下划线或连字符"}}})
		return
	}
	pw := RandomPassword()
	hash, err := HashPassword(pw)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	u, err := s.store.CreateViewer(name, hash, time.Now())
	switch {
	case errors.Is(err, errUserExists):
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "username", Message: "用户名已被使用"}}})
		return
	case errors.Is(err, errTooManyViewers):
		s.writeError(w, r, errorf(CodeConflict, "查看者最多 20 个"))
		return
	case err != nil:
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", ActorID: info(r).session.User.Username, Action: "user.create",
		TargetType: "user", TargetID: u.ID, Success: true, Details: map[string]any{"username": u.Username, "role": u.Role}})
	writeJSONStatus(w, http.StatusCreated, map[string]any{"user": u, "password": pw})
}

// handleDeleteUser：DELETE /api/v1/users/{id}，admin，需重新验证密码。只能删除查看者。
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if err := requireReauth(r); err != nil {
		s.writeError(w, r, err)
		return
	}
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, errorf(CodeNotFound, ""))
		return
	}
	name, err := s.store.viewerByID(id)
	if errors.Is(err, errNoUser) {
		s.writeError(w, r, errorf(CodeNotFound, "查看者不存在"))
		return
	}
	if err == nil {
		err = s.store.DeleteViewer(id)
	}
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", ActorID: info(r).session.User.Username, Action: "user.delete",
		TargetType: "user", TargetID: id, Success: true, Details: map[string]any{"username": name}})
	w.WriteHeader(http.StatusNoContent)
}

// handleResetUserPassword：POST /api/v1/users/{id}/password，admin，需重新验证密码。返回新的随机密码（只显示这一次）。
func (s *Server) handleResetUserPassword(w http.ResponseWriter, r *http.Request) {
	if err := requireReauth(r); err != nil {
		s.writeError(w, r, err)
		return
	}
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, errorf(CodeNotFound, ""))
		return
	}
	name, err := s.store.viewerByID(id)
	if errors.Is(err, errNoUser) {
		s.writeError(w, r, errorf(CodeNotFound, "查看者不存在"))
		return
	}
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	pw := RandomPassword()
	hash, err := HashPassword(pw)
	if err == nil {
		err = s.store.ResetViewerPassword(id, hash, time.Now())
	}
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", ActorID: info(r).session.User.Username, Action: "user.password_reset",
		TargetType: "user", TargetID: id, Success: true, Details: map[string]any{"username": name}})
	writeJSON(w, map[string]string{"password": pw})
}
