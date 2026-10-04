package server

import (
	"net/http"
	"time"
)

// 当前登录会话（设计 24.8、17.4）：查看当前账号在哪些浏览器登录着，并可踢出。

// SessionView 是一个登录会话的展示形式；不含令牌（只保存哈希）。
type SessionView struct {
	ID         int64  `json:"id"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt int64  `json:"last_seen_at"`
	ExpiresAt  int64  `json:"expires_at"`
	ClientIP   string `json:"client_ip"`
	UserAgent  string `json:"user_agent"`
	Current    bool   `json:"current"`
}

// ListSessions 返回账号仍有效的会话（未过期、未空闲超时），最近活动在前。
func (s *Store) ListSessions(userID int64, now time.Time) ([]SessionView, error) {
	rows, err := s.DB.Query(`SELECT id, created_at, last_seen_at, expires_at, client_ip, user_agent FROM sessions
		WHERE user_id = ? AND expires_at > ? AND last_seen_at > ? ORDER BY last_seen_at DESC, id DESC`,
		userID, now.Unix(), now.Add(-sessionIdle).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionView{}
	for rows.Next() {
		var v SessionView
		if err := rows.Scan(&v.ID, &v.CreatedAt, &v.LastSeenAt, &v.ExpiresAt, &v.ClientIP, &v.UserAgent); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DeleteUserSession 删除账号的一个会话；不属于该账号时返回 false（【安全】不能踢出其他账号的会话）。
func (s *Store) DeleteUserSession(userID, id int64) (bool, error) {
	res, err := s.DB.Exec(`DELETE FROM sessions WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// handleSessions：GET /api/v1/auth/sessions，admin。当前账号的会话，标出发出本次请求的会话。
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	list, err := s.store.ListSessions(ri.session.User.ID, time.Now())
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	for i := range list {
		list[i].Current = list[i].ID == ri.session.ID
	}
	writeList(w, list, "", nil)
}

// handleRevokeSession：DELETE /api/v1/auth/sessions/{id}，admin。踢出一个会话，该浏览器需要重新登录；
// 踢出当前会话等同退出登录（同时清除 Cookie）。成功 204；不存在或不属于当前账号 404。记入审计日志（登录日志）。
func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	ok, err := s.store.DeleteUserSession(ri.session.User.ID, id)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if !ok {
		s.writeError(w, r, errorf(CodeNotFound, "会话不存在或已失效"))
		return
	}
	if id == ri.session.ID {
		clearSessionCookie(w, r)
	}
	s.audit(r, AuditEntry{ActorType: "admin", ActorID: ri.session.User.Username, Action: "auth.session_revoke",
		TargetType: "session", TargetID: id, Success: true, Details: map[string]any{"current": id == ri.session.ID}})
	w.WriteHeader(http.StatusNoContent)
}
