package server

import (
	"database/sql"
	"time"
)

// 静音与维护的持久化（设计 16.6、18.14）。

// 静音类型。
const (
	SilenceMute        = "mute"        // 照常评估与记录，不发送通知，不计入“需要关注”
	SilenceMaintenance = "maintenance" // 节点级：继续采集，不产生告警（设计 1.5.15、27.7）
)

// Silence 是一条静音或维护记录。
type Silence struct {
	ID        int64  `json:"id"`
	ScopeType string `json:"scope_type"` // server / group / rule / global
	ScopeID   string `json:"scope_id"`
	Kind      string `json:"kind"`
	Reason    string `json:"reason"`
	StartsAt  int64  `json:"starts_at"`
	EndsAt    *int64 `json:"ends_at"` // nil 表示直到手动结束
	CreatedBy string `json:"created_by"`
}

// activeAt 判断在 now 时是否生效。
func (s Silence) activeAt(now time.Time) bool {
	return s.StartsAt <= now.Unix() && (s.EndsAt == nil || *s.EndsAt > now.Unix())
}

// appliesTo 判断是否作用于某节点的某条规则（ruleKey 为空表示节点整体，如维护、节点静音）。
func (s Silence) appliesTo(serverID int64, group, ruleKey string) bool {
	switch s.ScopeType {
	case "global":
		return true
	case "server":
		return s.ScopeID == itoa64(serverID)
	case "group":
		return group != "" && s.ScopeID == group
	case "rule":
		return ruleKey != "" && s.ScopeID == ruleKey
	}
	return false
}

func itoa64(n int64) string { return cursorOf(n) }

// ActiveSilences 返回 now 时生效的静音与维护，最新在前。
func (s *Store) ActiveSilences(now time.Time) ([]Silence, error) {
	rows, err := s.DB.Query(`SELECT id, scope_type, scope_id, kind, reason, starts_at, ends_at, created_by FROM silences
		WHERE starts_at <= ? AND (ends_at IS NULL OR ends_at > ?) ORDER BY id DESC`, now.Unix(), now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Silence{}
	for rows.Next() {
		var x Silence
		var ends sql.NullInt64
		if err := rows.Scan(&x.ID, &x.ScopeType, &x.ScopeID, &x.Kind, &x.Reason, &x.StartsAt, &ends, &x.CreatedBy); err != nil {
			return nil, err
		}
		if ends.Valid {
			x.EndsAt = &ends.Int64
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// CreateSilence 新增静音或维护；同一对象同一类型已有生效记录时先结束旧的（以新设置为准）。
func (s *Store) CreateSilence(x Silence, now time.Time) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE silences SET ends_at = ? WHERE scope_type = ? AND scope_id = ? AND kind = ?
		AND (ends_at IS NULL OR ends_at > ?)`, now.Unix(), x.ScopeType, x.ScopeID, x.Kind, now.Unix()); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO silences (scope_type, scope_id, kind, reason, starts_at, ends_at, created_by, created_at)
		VALUES (?,?,?,?,?,?,?,?)`, x.ScopeType, x.ScopeID, x.Kind, x.Reason, x.StartsAt, x.EndsAt, x.CreatedBy, now.Unix())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, tx.Commit()
}

// EndSilence 立即结束一条静音或维护（保留记录）；不存在或已结束时返回 errNoSilence。
func (s *Store) EndSilence(id int64, now time.Time) (*Silence, error) {
	var x Silence
	var ends sql.NullInt64
	err := s.DB.QueryRow(`SELECT id, scope_type, scope_id, kind, reason, starts_at, ends_at, created_by FROM silences WHERE id = ?`, id).
		Scan(&x.ID, &x.ScopeType, &x.ScopeID, &x.Kind, &x.Reason, &x.StartsAt, &ends, &x.CreatedBy)
	if err == sql.ErrNoRows {
		return nil, errNoSilence
	} else if err != nil {
		return nil, err
	}
	if ends.Valid {
		x.EndsAt = &ends.Int64
	}
	if !x.activeAt(now) {
		return nil, errNoSilence
	}
	if _, err := s.DB.Exec(`UPDATE silences SET ends_at = ? WHERE id = ?`, now.Unix(), id); err != nil {
		return nil, err
	}
	return &x, nil
}

var errNoSilence = errorf(CodeNotFound, "静音或维护不存在或已结束")
