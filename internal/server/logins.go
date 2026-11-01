package server

// 登录记录（修订第 276 条，设计 4.9.5）：Agent 从 /var/log/wtmp 读到的登录（时间、账号、来源地址、终端），按内容去重保存 180 天。
// 来自该节点从未出现过的地址的登录记一条变更事件（kind=change），按“变更通知”的“登录”类别通知。
// 第一次收到某节点的登录时只保存为基准（其中的地址都当作已知），不产生事件。
//
// 【安全】账号名与来源地址只给 Web 管理员（GET /servers/{id}/logins 为 admin）；通知只写“有 N 次来自新地址的登录”。

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"vpsmon/internal/protocol"
)

const (
	changeLogins     = "logins" // change_state.item：有记录表示已有基准
	loginRetention   = 180 * 24 * time.Hour
	maxLoginsPerPost = 200
)

// LoginRecord 是保存的一次登录。
type LoginRecord struct {
	ID      int64  `json:"id"`
	At      int64  `json:"at"`
	User    string `json:"user"`
	Host    string `json:"host"`
	Line    string `json:"line"`
	NewHost bool   `json:"new_host"` // 当时是该节点第一次出现的来源地址
}

// trackLogins 保存上报中的登录（补发的旧数据也保存，按内容去重），并对来自新地址的登录记录变更。
func (s *Server) trackLogins(sid int64, list []protocol.Login, now time.Time) {
	if len(list) == 0 {
		return
	}
	if len(list) > maxLoginsPerPost {
		list = list[len(list)-maxLoginsPerPost:]
	}
	_, known, err := s.store.ChangeState(sid, changeLogins)
	if err != nil {
		s.log.Error("load login baseline failed", "component", "logins", "server_id", sid, "err", err)
		return
	}
	fresh, err := s.store.InsertLogins(sid, list, !known, now)
	if err != nil {
		s.log.Error("save logins failed", "component", "logins", "server_id", sid, "err", err)
		return
	}
	if !known {
		if err := s.store.SaveChangeState(sid, changeLogins, "1", now); err != nil {
			s.log.Error("save login baseline failed", "component", "logins", "server_id", sid, "err", err)
		}
		return
	}
	if len(fresh) == 0 {
		return
	}
	lines := make([]string, 0, len(fresh))
	for _, l := range fresh {
		lines = append(lines, fmt.Sprintf("%s 从 %s 登录（%s，%s）", l.User, l.Host, orText(l.Line, "未知终端"),
			time.Unix(l.Time, 0).In(time.Local).Format("01-02 15:04")))
	}
	s.recordChange(sid, changeLogins, fmt.Sprintf("有 %d 次来自新地址的登录", len(fresh)), lines, SeverityWarning, now)
}

func orText(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// InsertLogins 去重保存登录，返回新保存且来源地址此前没有出现过的登录。baseline 为 true 时都不算新地址。
// 太旧（超过保留期）、账号为空或时间无效的记录忽略。
func (s *Store) InsertLogins(sid int64, list []protocol.Login, baseline bool, now time.Time) ([]protocol.Login, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var fresh []protocol.Login
	oldest := now.Add(-loginRetention).Unix()
	for _, l := range list {
		if l.User == "" || l.Time < oldest || l.Time > now.Add(24*time.Hour).Unix() {
			continue
		}
		seen := true
		if l.Host != "" && !baseline {
			var one int
			err := tx.QueryRow(`SELECT 1 FROM logins WHERE server_id = ? AND host = ? LIMIT 1`, sid, l.Host).Scan(&one)
			if errors.Is(err, sql.ErrNoRows) {
				seen = false
			} else if err != nil {
				return nil, err
			}
		}
		res, err := tx.Exec(`INSERT OR IGNORE INTO logins (server_id, at, user, host, line, new_host) VALUES (?,?,?,?,?,?)`,
			sid, l.Time, l.User, l.Host, l.Line, !seen)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 1 && !seen {
			fresh = append(fresh, l)
		}
	}
	return fresh, tx.Commit()
}

// ListLogins 按时间倒序返回登录；cursor 为上一页最后一条的 (at, id)。
func (s *Store) ListLogins(sid int64, beforeAt, beforeID int64, limit int) ([]LoginRecord, error) {
	q := `SELECT id, at, user, host, line, new_host FROM logins WHERE server_id = ?`
	args := []any{sid}
	if beforeAt > 0 {
		q += ` AND (at < ? OR (at = ? AND id < ?))`
		args = append(args, beforeAt, beforeAt, beforeID)
	}
	q += ` ORDER BY at DESC, id DESC LIMIT ?`
	rows, err := s.DB.Query(q, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LoginRecord{}
	for rows.Next() {
		var l LoginRecord
		if err := rows.Scan(&l.ID, &l.At, &l.User, &l.Host, &l.Line, &l.NewHost); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// PruneLogins 删除保留期以前的登录。
func (s *Store) PruneLogins(now time.Time) (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM logins WHERE at < ?`, now.Add(-loginRetention).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// handleListLogins：GET /api/v1/servers/{id}/logins?cursor=&limit=，admin（修订第 276 条）。
func (s *Server) handleListLogins(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if _, err := s.store.GetServer(id); errors.Is(err, errNoServer) {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	} else if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	var beforeAt, beforeID int64
	if c := r.URL.Query().Get("cursor"); c != "" {
		a, b, ok := strings.Cut(c, ".")
		var e1, e2 error
		beforeAt, e1 = strconv.ParseInt(a, 10, 64)
		beforeID, e2 = strconv.ParseInt(b, 10, 64)
		if !ok || e1 != nil || e2 != nil || beforeAt <= 0 {
			s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "cursor", Message: "cursor 无效"}}})
			return
		}
	}
	limit, ok := s.intQuery(w, r, "limit", 50, 1, 200)
	if !ok {
		return
	}
	items, err := s.store.ListLogins(id, beforeAt, beforeID, limit+1)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[limit-1]
		next = fmt.Sprintf("%d.%d", last.At, last.ID)
	}
	writeList(w, items, next, nil)
}
