package server

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// 审计日志查询与清理（设计 24.8、18.16）。写入见 store_nodes.go 的 Audit。

// loginActions 归入“登录日志”的操作；其余都属于“操作日志”。
var loginActions = []string{"auth.login", "auth.logout", "auth.reauth"}

// auditRetention：审计日志保留 1 年（设计 24.8、24.9）。
const auditRetention = 365 * 24 * time.Hour

// AuditLog 是一条审计记录的展示形式。
type AuditLog struct {
	ID         int64           `json:"id"`
	TS         int64           `json:"ts"`
	ActorType  string          `json:"actor_type"`
	ActorID    string          `json:"actor_id"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	TargetName string          `json:"target_name"` // 对象为节点且仍存在时的节点名称
	Result     string          `json:"result"`
	ClientIP   string          `json:"client_ip"`
	UserAgent  string          `json:"user_agent"`
	Details    json.RawMessage `json:"details"`
}

// AuditQuery 是审计日志的筛选条件。
type AuditQuery struct {
	Category string // login / operation / 空表示全部
	Result   string // success / failure / 空
	Before   int64  // 游标：只返回 id 小于它的记录；0 表示从最新开始
	Limit    int
}

// ListAudit 按时间倒序返回审计记录；多取一条用于判断是否还有下一页。
func (s *Store) ListAudit(q AuditQuery) ([]AuditLog, int64, error) {
	where := []string{"1=1"}
	args := []any{}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(loginActions)), ",")
	switch q.Category {
	case "login":
		where = append(where, "a.action IN ("+ph+")")
		for _, a := range loginActions {
			args = append(args, a)
		}
	case "operation":
		where = append(where, "a.action NOT IN ("+ph+")")
		for _, a := range loginActions {
			args = append(args, a)
		}
	}
	if q.Result != "" {
		where = append(where, "a.result = ?")
		args = append(args, q.Result)
	}
	if q.Before > 0 {
		where = append(where, "a.id < ?")
		args = append(args, q.Before)
	}
	args = append(args, q.Limit+1)
	// target_id 以文本保存；节点已删除时 LEFT JOIN 得到空名称，界面显示 “#ID（已删除）”
	rows, err := s.DB.Query(`SELECT a.id, a.ts, a.actor_type, a.actor_id, a.action, a.target_type, a.target_id,
		COALESCE(sv.name, ''), a.result, a.client_ip, a.user_agent, a.details
		FROM audit_logs a LEFT JOIN servers sv ON a.target_type = 'server' AND sv.id = CAST(a.target_id AS INTEGER)
		WHERE `+strings.Join(where, " AND ")+` ORDER BY a.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AuditLog{}
	for rows.Next() {
		var l AuditLog
		var details string
		if err := rows.Scan(&l.ID, &l.TS, &l.ActorType, &l.ActorID, &l.Action, &l.TargetType, &l.TargetID,
			&l.TargetName, &l.Result, &l.ClientIP, &l.UserAgent, &details); err != nil {
			return nil, 0, err
		}
		l.Details = json.RawMessage(details)
		if !json.Valid(l.Details) {
			l.Details = json.RawMessage("{}")
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var next int64
	if len(out) > q.Limit {
		out = out[:q.Limit]
		next = out[len(out)-1].ID
	}
	return out, next, nil
}

// PruneAudit 删除超过保留期的审计记录，返回删除条数。
func (s *Store) PruneAudit(now time.Time) (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM audit_logs WHERE ts < ?`, now.Add(-auditRetention).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func formatCursor(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}
