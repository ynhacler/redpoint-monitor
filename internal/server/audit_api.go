package server

import (
	"net/http"
	"strconv"
)

// handleAuditLogs：GET /api/v1/audit-logs，admin（设计 24.8）。
// 参数：category=login|operation（空为全部）、result=success|failure、cursor（上一页的 next_cursor）、limit 1～200（默认 50）。
// 返回 {"items", "next_cursor"}，按时间倒序；next_cursor 为空表示没有更多。
// 【安全】审计日志只读：不提供修改或删除接口，过期记录由面板自动清理。
func (s *Server) handleAuditLogs(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	q := AuditQuery{Category: qs.Get("category"), Result: qs.Get("result")}
	var fe []FieldError
	if q.Category != "" && q.Category != "login" && q.Category != "operation" {
		fe = append(fe, FieldError{Field: "category", Message: "category 只能是 login 或 operation"})
	}
	if q.Result != "" && q.Result != "success" && q.Result != "failure" {
		fe = append(fe, FieldError{Field: "result", Message: "result 只能是 success 或 failure"})
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
	items, next, err := s.store.ListAudit(q)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, map[string]any{"items": items, "next_cursor": formatCursor(next)})
}
