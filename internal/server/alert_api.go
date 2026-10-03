package server

import (
	"net/http"
	"strconv"
)

// 告警接口（设计 19.9）。规则的修改（分组、节点覆盖）随 A5 后续加入（TODO(A5)）。

// handleAlerts：GET /api/v1/alerts，admin。
// 参数：state=active（默认，正在告警）| resolved | all；server_id；cursor（上一页的 next_cursor）；limit 1～200（默认 50）。
// 返回 {"items", "next_cursor"}，按时间倒序。
func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	q := AlertQuery{}
	var fe []FieldError
	switch qs.Get("state") {
	case "", "active":
		q.State = StateFiring
	case "resolved":
		q.State = StateResolved
	case "all":
	default:
		fe = append(fe, FieldError{Field: "state", Message: "state 只能是 active、resolved 或 all"})
	}
	if v := qs.Get("server_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			fe = append(fe, FieldError{Field: "server_id", Message: "server_id 无效"})
		}
		q.ServerID = n
	}
	if c := qs.Get("cursor"); c != "" {
		n, err := strconv.ParseInt(c, 10, 64)
		if err != nil || n <= 0 {
			fe = append(fe, FieldError{Field: "cursor", Message: "cursor 无效"})
		}
		q.Before = n
	}
	if fe != nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	limit, ok := s.intQuery(w, r, "limit", 50, 1, 200)
	if !ok {
		return
	}
	q.Limit = limit
	items, next, err := s.store.ListAlertEvents(q)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, map[string]any{"items": items, "next_cursor": cursorOf(next)})
}

// handleAlertRules：GET /api/v1/alert-rules，admin。返回全部三层规则（全局 / 分组 / 节点）。
func (s *Server) handleAlertRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.store.ListAlertRules()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, map[string]any{"items": rules})
}
