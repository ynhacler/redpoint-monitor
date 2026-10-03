package server

import (
	"database/sql"
	"strconv"
	"strings"
	"time"
)

// 告警规则与告警事件的持久化（设计 18.7、18.8）。评估逻辑见 alert.go、alert_engine.go。

// alertEventRetention：告警事件保留 180 天（设计 16.7、24.9）。
const alertEventRetention = 180 * 24 * time.Hour

const alertRuleColumns = `id, rule_key, scope_type, scope_id, type, operator, threshold, recover_threshold,
	duration_s, recover_duration_s, severity, repeat_interval_s, enabled`

// ListAlertRules 返回全部规则（三层），按层级与 rule_key 排序。
func (s *Store) ListAlertRules() ([]AlertRule, error) {
	rows, err := s.DB.Query(`SELECT ` + alertRuleColumns + ` FROM alert_rules
		ORDER BY CASE scope_type WHEN 'global' THEN 0 WHEN 'group' THEN 1 ELSE 2 END, scope_id, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertRule{}
	for rows.Next() {
		var r AlertRule
		if err := rows.Scan(&r.ID, &r.RuleKey, &r.ScopeType, &r.ScopeID, &r.Type, &r.Operator, &r.Threshold,
			&r.RecoverThreshold, &r.DurationS, &r.RecoverDurationS, &r.Severity, &r.RepeatIntervalS, &r.Enabled); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AlertEvent 是一条告警事件（设计 18.8）。
type AlertEvent struct {
	ID            int64    `json:"id"`
	RuleID        int64    `json:"rule_id"`
	RuleKey       string   `json:"rule_key"`
	ServerID      int64    `json:"server_id"`
	ServerName    string   `json:"server_name"`
	Type          string   `json:"type"`
	Severity      string   `json:"severity"`
	State         string   `json:"state"` // firing / resolved
	Value         float64  `json:"value"`
	Threshold     float64  `json:"threshold"`
	Message       string   `json:"message"`
	StartedAt     int64    `json:"started_at"`
	FiredAt       int64    `json:"fired_at"`
	ResolvedAt    int64    `json:"resolved_at,omitempty"`
	ResolvedValue *float64 `json:"resolved_value,omitempty"`
}

// InsertAlertEvent 记录一次触发，返回事件 ID。
func (s *Store) InsertAlertEvent(e AlertEvent) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO alert_events (rule_id, rule_key, server_id, type, severity, state, value, threshold,
		message, started_at, fired_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		e.RuleID, e.RuleKey, e.ServerID, e.Type, e.Severity, StateFiring, e.Value, e.Threshold, e.Message, e.StartedAt, e.FiredAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ResolveAlertEvent 结束一次告警，记录恢复时间与恢复时的值。
func (s *Store) ResolveAlertEvent(id int64, at time.Time, value float64) error {
	_, err := s.DB.Exec(`UPDATE alert_events SET state = ?, resolved_at = ?, resolved_value = ? WHERE id = ? AND state = ?`,
		StateResolved, at.Unix(), value, id, StateFiring)
	return err
}

// AlertQuery 是告警事件的筛选条件。
type AlertQuery struct {
	State    string // firing（活动）/ resolved / 空表示全部
	ServerID int64  // 0 表示全部节点
	Before   int64  // 游标：只返回 id 小于它的记录
	Limit    int
}

// ListAlertEvents 按时间倒序返回告警事件；多取一条判断是否还有下一页。
func (s *Store) ListAlertEvents(q AlertQuery) ([]AlertEvent, int64, error) {
	where := []string{"1=1"}
	args := []any{}
	if q.State != "" {
		where = append(where, "e.state = ?")
		args = append(args, q.State)
	}
	if q.ServerID > 0 {
		where = append(where, "e.server_id = ?")
		args = append(args, q.ServerID)
	}
	if q.Before > 0 {
		where = append(where, "e.id < ?")
		args = append(args, q.Before)
	}
	args = append(args, q.Limit+1)
	rows, err := s.DB.Query(`SELECT e.id, e.rule_id, e.rule_key, e.server_id, COALESCE(sv.name, ''), e.type, e.severity, e.state,
		e.value, e.threshold, e.message, e.started_at, e.fired_at, e.resolved_at, e.resolved_value
		FROM alert_events e LEFT JOIN servers sv ON sv.id = e.server_id
		WHERE `+strings.Join(where, " AND ")+` ORDER BY e.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AlertEvent{}
	for rows.Next() {
		var e AlertEvent
		var rv sql.NullFloat64
		if err := rows.Scan(&e.ID, &e.RuleID, &e.RuleKey, &e.ServerID, &e.ServerName, &e.Type, &e.Severity, &e.State,
			&e.Value, &e.Threshold, &e.Message, &e.StartedAt, &e.FiredAt, &e.ResolvedAt, &rv); err != nil {
			return nil, 0, err
		}
		if rv.Valid {
			e.ResolvedValue = &rv.Float64
		}
		out = append(out, e)
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

// FiringAlertEvents 返回全部活动告警，面板启动时据此恢复状态，不重复记录（设计 16.7）。
func (s *Store) FiringAlertEvents() ([]AlertEvent, error) {
	items, _, err := s.ListAlertEvents(AlertQuery{State: StateFiring, Limit: 100000})
	return items, err
}

// PruneAlertEvents 删除超过保留期且已恢复的事件；活动告警不删。
func (s *Store) PruneAlertEvents(now time.Time) (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM alert_events WHERE state = ? AND resolved_at < ?`,
		StateResolved, now.Add(-alertEventRetention).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func cursorOf(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

// errNoRule 表示规则不存在。
var errNoRule = errorf(CodeNotFound, "规则不存在或已删除")

// errRuleExists 表示同一层级、同一对象已有同名覆盖规则。
var errRuleExists = errorf(CodeConflict, "该分组或节点已有这条规则的覆盖，请直接修改")

// GetAlertRule 按 ID 读取规则。
func (s *Store) GetAlertRule(id int64) (*AlertRule, error) {
	var r AlertRule
	err := s.DB.QueryRow(`SELECT `+alertRuleColumns+` FROM alert_rules WHERE id = ?`, id).Scan(&r.ID, &r.RuleKey,
		&r.ScopeType, &r.ScopeID, &r.Type, &r.Operator, &r.Threshold, &r.RecoverThreshold, &r.DurationS,
		&r.RecoverDurationS, &r.Severity, &r.RepeatIntervalS, &r.Enabled)
	if err == sql.ErrNoRows {
		return nil, errNoRule
	}
	return &r, err
}

// UpdateAlertRule 修改规则的可编辑字段；类型、rule_key、层级不可改。
func (s *Store) UpdateAlertRule(r AlertRule, now time.Time) error {
	res, err := s.DB.Exec(`UPDATE alert_rules SET threshold = ?, recover_threshold = ?, duration_s = ?, recover_duration_s = ?,
		severity = ?, repeat_interval_s = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		r.Threshold, r.RecoverThreshold, r.DurationS, r.RecoverDurationS, r.Severity, r.RepeatIntervalS, r.Enabled, now.Unix(), r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoRule
	}
	return nil
}

// CreateAlertRule 新增分组或节点层的覆盖规则，返回 ID。
func (s *Store) CreateAlertRule(r AlertRule, now time.Time) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO alert_rules (rule_key, scope_type, scope_id, type, operator, threshold, recover_threshold,
		duration_s, recover_duration_s, severity, repeat_interval_s, enabled, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.RuleKey, r.ScopeType, r.ScopeID, r.Type, r.Operator, r.Threshold, r.RecoverThreshold, r.DurationS,
		r.RecoverDurationS, r.Severity, r.RepeatIntervalS, r.Enabled, now.Unix(), now.Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, errRuleExists
		}
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteAlertRule 删除覆盖规则；全局规则不能删除（只能关闭）。
func (s *Store) DeleteAlertRule(id int64) error {
	res, err := s.DB.Exec(`DELETE FROM alert_rules WHERE id = ? AND scope_type <> 'global'`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoRule
	}
	return nil
}
