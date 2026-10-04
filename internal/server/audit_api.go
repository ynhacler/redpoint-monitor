package server

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// auditQueryFrom 解析审计日志的筛选参数（列表与导出共用）：category、result、actor、action、from、to。
// 参数错误时写 422 并返回 false。
func (s *Server) auditQueryFrom(w http.ResponseWriter, r *http.Request) (AuditQuery, bool) {
	qs := r.URL.Query()
	q := AuditQuery{Category: qs.Get("category"), Result: qs.Get("result"), Actor: qs.Get("actor"),
		Action: strings.TrimSpace(qs.Get("action"))}
	var fe []FieldError
	if q.Category != "" && q.Category != "login" && q.Category != "operation" {
		fe = append(fe, FieldError{Field: "category", Message: "category 只能是 login 或 operation"})
	}
	if q.Result != "" && q.Result != "success" && q.Result != "failure" {
		fe = append(fe, FieldError{Field: "result", Message: "result 只能是 success 或 failure"})
	}
	switch q.Actor {
	case "", "admin", "agent", "apikey", "cli", "system":
	default:
		fe = append(fe, FieldError{Field: "actor", Message: "actor 只能是 admin、agent、cli 或 system"})
	}
	if len(q.Action) > 64 {
		fe = append(fe, FieldError{Field: "action", Message: "action 最多 64 个字符"})
	}
	for _, f := range []struct {
		name string
		dst  *int64
	}{{"from", &q.From}, {"to", &q.To}} {
		if v := qs.Get(f.name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n <= 0 {
				fe = append(fe, FieldError{Field: f.name, Message: f.name + " 应为 Unix 秒"})
			}
			*f.dst = n
		}
	}
	if fe != nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return q, false
	}
	return q, true
}

// handleAuditLogs：GET /api/v1/audit-logs，admin（设计 24.8）。
// 筛选参数见 auditQueryFrom；另有 cursor（上一页的 next_cursor）、limit 1～200（默认 50）。
// 返回 {"items", "next_cursor"}，按时间倒序；next_cursor 为空表示没有更多。
// 【安全】审计日志只读：不提供修改或删除接口，过期记录由面板自动清理。
func (s *Server) handleAuditLogs(w http.ResponseWriter, r *http.Request) {
	q, ok := s.auditQueryFrom(w, r)
	if !ok {
		return
	}
	if c := r.URL.Query().Get("cursor"); c != "" {
		n, err := strconv.ParseInt(c, 10, 64)
		if err != nil || n <= 0 {
			s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "cursor", Message: "cursor 无效"}}})
			return
		}
		q.Before = n
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
	writeList(w, items, formatCursor(next), nil)
}

// maxAuditExport：一次导出的最多条数。更多时缩小时间范围分批导出。
const maxAuditExport = 10000

// handleAuditExport：GET /api/v1/audit-logs/export，admin（设计 24.8）。筛选同 /audit-logs，导出为 CSV。
//
// UTF-8 带 BOM，Excel 可直接打开中文。【安全】以 = + - @（及制表符、回车）开头的单元格前加单引号：
// 节点名、用户名等可能由他人填写，被表格软件当作公式执行就是 CSV 注入。
func (s *Server) handleAuditExport(w http.ResponseWriter, r *http.Request) {
	q, ok := s.auditQueryFrom(w, r)
	if !ok {
		return
	}
	var all []AuditLog
	q.Limit = 1000
	for len(all) < maxAuditExport {
		items, next, err := s.store.ListAudit(q)
		if err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		all = append(all, items...)
		if next == 0 {
			break
		}
		q.Before = next
	}
	if len(all) > maxAuditExport {
		all = all[:maxAuditExport]
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="vpsmon-audit-`+time.Now().Format("20060102-150405")+`.csv"`)
	w.Write([]byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
	cw := csv.NewWriter(w)
	cw.Write([]string{"时间", "结果", "主体", "主体 ID", "操作", "对象类型", "对象 ID", "对象名称", "IP", "浏览器", "详情"})
	for _, l := range all {
		res := "成功"
		if l.Result != "success" {
			res = "失败"
		}
		cw.Write([]string{time.Unix(l.TS, 0).Format("2006-01-02 15:04:05"), res, l.ActorType, csvSafe(l.ActorID),
			l.Action, l.TargetType, csvSafe(l.TargetID), csvSafe(l.TargetName), csvSafe(l.ClientIP), csvSafe(l.UserAgent),
			csvSafe(string(l.Details))})
	}
	cw.Flush()
	s.audit(r, AuditEntry{ActorType: "admin", Action: "audit.export", Success: true,
		Details: map[string]any{"rows": len(all), "category": q.Category, "actor": q.Actor, "action": q.Action}})
}

// csvSafe 防止 CSV 注入：以公式起始字符开头的单元格前加单引号，表格软件将其作为文本显示。
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}
