package server

// 系统事件（修订第 163 条）：Agent 升级结果、到期提醒、面板自身告警与自检。与告警事件分开保存——它们没有触发 / 恢复两个状态，
// 只是一条记录；通知照常经渠道发送，这里另存一份，供 App 事件中心与 API 查看。保留 180 天。
//
// 【安全】只读 API Key 与 App 设备按授权范围过滤（设计 17.3、45.2）：不属于某个节点的事件（证书、云账户、面板）
// 只有“全部节点”范围才能看到。

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	systemEventRetention = 180 * 24 * time.Hour
	eventUpgrade         = "upgrade"
	eventReminder        = "reminder"
	eventPanel           = "panel"
)

// SystemEvent 是一条系统事件；ServerID 为 0 表示不属于某个节点。
type SystemEvent struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	Severity   string `json:"severity"`
	ServerID   int64  `json:"server_id"`
	ServerName string `json:"server_name"`
	Title      string `json:"title"`
	Message    string `json:"message"`
	CreatedAt  int64  `json:"created_at"`
}

// AddSystemEvent 保存一条系统事件。
func (s *Store) AddSystemEvent(e SystemEvent) error {
	_, err := s.DB.Exec(`INSERT INTO system_events (kind, severity, server_id, title, message, created_at) VALUES (?,?,?,?,?,?)`,
		e.Kind, e.Severity, e.ServerID, e.Title, e.Message, e.CreatedAt)
	return err
}

// ListSystemEvents 按 ID 倒序列出 before 之前（0 为最新）的事件；keep 为 nil 表示全部可见。
// 按范围过滤后不足一页时继续往前读，返回的 next 为下一页游标（0 表示没有更多）。
func (s *Store) ListSystemEvents(before int64, limit int, keep func(SystemEvent) bool) ([]SystemEvent, int64, error) {
	out := []SystemEvent{}
	cur := before
	for range 20 { // 最多读 20 批，避免授权范围很窄时扫描全表
		q := `SELECT e.id, e.kind, e.severity, e.server_id, COALESCE(sv.name, ''), e.title, e.message, e.created_at
			FROM system_events e LEFT JOIN servers sv ON sv.id = e.server_id`
		args := []any{}
		if cur > 0 {
			q += ` WHERE e.id < ?`
			args = append(args, cur)
		}
		q += ` ORDER BY e.id DESC LIMIT ?`
		args = append(args, limit+1)
		rows, err := s.DB.Query(q, args...)
		if err != nil {
			return nil, 0, err
		}
		n := 0
		for rows.Next() {
			var e SystemEvent
			if err := rows.Scan(&e.ID, &e.Kind, &e.Severity, &e.ServerID, &e.ServerName, &e.Title, &e.Message, &e.CreatedAt); err != nil {
				rows.Close()
				return nil, 0, err
			}
			n++
			cur = e.ID
			if keep != nil && !keep(e) {
				continue
			}
			if len(out) == limit {
				rows.Close()
				return out, out[limit-1].ID, nil
			}
			out = append(out, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, 0, err
		}
		if n <= limit { // 已读到最早的一条
			return out, 0, nil
		}
	}
	return out, cur, nil
}

// PruneSystemEvents 删除 180 天前的系统事件。
func (s *Store) PruneSystemEvents(now time.Time) (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM system_events WHERE created_at < ?`, now.Add(-systemEventRetention).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// recordEvent 保存一条系统事件；失败只记日志，不影响通知与主流程。
func (s *Server) recordEvent(kind, severity string, serverID int64, title, message string, now time.Time) {
	if err := s.store.AddSystemEvent(SystemEvent{Kind: kind, Severity: severity, ServerID: serverID, Title: title,
		Message: message, CreatedAt: now.Unix()}); err != nil {
		s.log.Error("record system event failed", "component", "events", "kind", kind, "err", err)
	}
}

// recordNotice 把一条通知另存为系统事件：标题与通知相同，正文去掉标题与链接。
func (s *Server) recordNotice(kind string, m notifyMessage, now time.Time) {
	title := m.title()
	var lines []string
	for _, l := range strings.Split(strings.TrimPrefix(m.text(now), title), "\n") {
		if l = strings.TrimSpace(l); l != "" && l != m.Link {
			lines = append(lines, l)
		}
	}
	s.recordEvent(kind, m.Severity, m.ServerID, title, strings.Join(lines, "\n"), now)
}

// recordUpgrade 记录 Agent 升级的最终结果（成功、失败、回滚）。
func (s *Server) recordUpgrade(serverID int64, target, status, reason string, now time.Time) {
	name := ""
	if row, err := s.store.GetServer(serverID); err == nil {
		name = row.Name
	}
	v := "v" + strings.TrimPrefix(target, "v")
	title, sev := "✅ "+name+" Agent 已升级到 "+v, SeverityInfo
	switch status {
	case UpgradeFailed:
		title, sev = "❌ "+name+" Agent 升级到 "+v+" 失败", SeverityWarning
	case UpgradeRolledBack:
		title, sev = "↩️ "+name+" Agent 升级到 "+v+" 后健康检查失败，已回滚", SeverityWarning
	}
	s.recordEvent(eventUpgrade, sev, serverID, title, reason, now)
}

// handleSystemEvents：GET /api/v1/system-events?cursor=&limit=，read。按调用方的授权范围过滤。
func (s *Server) handleSystemEvents(w http.ResponseWriter, r *http.Request) {
	var before int64
	if c := r.URL.Query().Get("cursor"); c != "" {
		n, err := strconv.ParseInt(c, 10, 64)
		if err != nil || n <= 0 {
			s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "cursor", Message: "cursor 无效"}}})
			return
		}
		before = n
	}
	limit, ok := s.intQuery(w, r, "limit", 50, 1, 200)
	if !ok {
		return
	}
	var keep func(SystemEvent) bool
	if sc := info(r).apiScope; sc != nil && sc.Type != "all" {
		rows, err := s.store.ListServers()
		if err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		allowed := map[int64]bool{}
		for _, row := range rows {
			allowed[row.ID] = sc.allows(row)
		}
		// 【安全】范围受限时只看到范围内节点的事件；不属于节点的事件（ServerID 0）一律不返回
		keep = func(e SystemEvent) bool { return e.ServerID != 0 && allowed[e.ServerID] }
	}
	items, next, err := s.store.ListSystemEvents(before, limit, keep)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeList(w, items, cursorOf(next), nil)
}
