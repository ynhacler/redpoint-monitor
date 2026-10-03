package server

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// 告警接口（设计 19.9）：告警事件查询；规则的查看、修改、分组 / 节点覆盖与预览（设计 16.2）。

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

// alertRuleBody 是修改 / 新增规则的请求体；PUT 中省略的字段保持不变。
type alertRuleBody struct {
	RuleKey          string   `json:"rule_key"`   // 仅新增：要覆盖的规则，如 cpu、disk_critical
	ScopeType        string   `json:"scope_type"` // 仅新增：group / server
	ScopeID          string   `json:"scope_id"`   // 仅新增：分组名或节点 ID
	Threshold        *float64 `json:"threshold"`
	RecoverThreshold *float64 `json:"recover_threshold"`
	DurationS        *int     `json:"duration_s"`
	RecoverDurationS *int     `json:"recover_duration_s"`
	Severity         string   `json:"severity"`
	RepeatIntervalS  *int     `json:"repeat_interval_s"`
	Enabled          *bool    `json:"enabled"`
}

// apply 把请求中提供的字段写到规则上。
func (b alertRuleBody) apply(r *AlertRule) {
	if b.Threshold != nil {
		r.Threshold = *b.Threshold
	}
	if b.RecoverThreshold != nil {
		r.RecoverThreshold = *b.RecoverThreshold
	}
	if b.DurationS != nil {
		r.DurationS = *b.DurationS
	}
	if b.RecoverDurationS != nil {
		r.RecoverDurationS = *b.RecoverDurationS
	}
	if b.Severity != "" {
		r.Severity = b.Severity
	}
	if b.RepeatIntervalS != nil {
		r.RepeatIntervalS = *b.RepeatIntervalS
	}
	if b.Enabled != nil {
		r.Enabled = *b.Enabled
	}
}

// thresholdRange 是各类型阈值的合理范围（设计 16.1）：超出多半是输入错误。
func thresholdRange(typ string) (lo, hi float64, unit string) {
	switch typ {
	case AlertOffline:
		return 30, 86400, "秒"
	case AlertLoad:
		return 0.1, 100, "倍核数"
	case AlertTraffic:
		return 1, 200, "%"
	case AlertTrafficForecast:
		return 50, 1000, "%"
	case AlertAgentClock:
		return 10, 86400, "秒"
	}
	return 1, 100, "%"
}

// validateAlertRule 校验规则的取值；返回字段级错误（设计 43.4）。
func validateAlertRule(r AlertRule) []FieldError {
	var fe []FieldError
	bad := func(f, m string) { fe = append(fe, FieldError{Field: f, Message: m}) }
	lo, hi, unit := thresholdRange(r.Type)
	if math.IsNaN(r.Threshold) || r.Threshold < lo || r.Threshold > hi {
		bad("threshold", fmt.Sprintf("阈值应在 %g～%g %s 之间", lo, hi, unit))
	}
	// 恢复阈值不高于触发阈值，才有回差；离线的“恢复”指重新上报，必须明显小于触发阈值
	if math.IsNaN(r.RecoverThreshold) || r.RecoverThreshold < 0 || r.RecoverThreshold > r.Threshold ||
		(r.Type == AlertOffline && r.RecoverThreshold >= r.Threshold) {
		bad("recover_threshold", "恢复阈值应不小于 0、且不高于触发阈值")
	}
	if r.DurationS < 0 || r.DurationS > 86400 {
		bad("duration_s", "持续时间应在 0～86400 秒之间")
	}
	if r.RecoverDurationS < 0 || r.RecoverDurationS > 86400 {
		bad("recover_duration_s", "恢复持续时间应在 0～86400 秒之间")
	}
	if r.Severity != SeverityInfo && r.Severity != SeverityWarning && r.Severity != SeverityCritical {
		bad("severity", "级别只能是 info、warning 或 critical")
	}
	if r.RepeatIntervalS < 0 || r.RepeatIntervalS > 7*86400 {
		bad("repeat_interval_s", "重复提醒间隔应在 0～604800 秒之间")
	}
	return fe
}

func (s *Server) decodeRuleBody(w http.ResponseWriter, r *http.Request) (alertRuleBody, bool) {
	var b alertRuleBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return b, false
	}
	return b, true
}

// handleUpdateAlertRule：PUT /api/v1/alert-rules/{id}，admin（设计 16.2）。
// 可改阈值、恢复阈值、持续时间、级别、重复提醒间隔与开关；类型、rule_key、层级不可改。成功返回最新规则。
func (s *Server) handleUpdateAlertRule(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	b, ok := s.decodeRuleBody(w, r)
	if !ok {
		return
	}
	rule, err := s.store.GetAlertRule(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	before := *rule
	b.apply(rule)
	if fe := validateAlertRule(*rule); fe != nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	if err := s.store.UpdateAlertRule(*rule, time.Now()); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "alert_rule.update", Success: true, Details: map[string]any{
		"rule_id": id, "rule_key": rule.RuleKey, "scope": rule.ScopeType + ":" + rule.ScopeID,
		"threshold": []float64{before.Threshold, rule.Threshold}, "enabled": []bool{before.Enabled, rule.Enabled}}})
	writeJSON(w, rule)
}

// handleCreateAlertRule：POST /api/v1/alert-rules，admin（设计 16.2）。
// 为分组或节点新增覆盖：以同 rule_key 的全局规则为基础，应用请求中的字段。成功 201；已有覆盖 409。
func (s *Server) handleCreateAlertRule(w http.ResponseWriter, r *http.Request) {
	b, ok := s.decodeRuleBody(w, r)
	if !ok {
		return
	}
	rules, err := s.store.ListAlertRules()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	var base *AlertRule
	for i := range rules {
		if rules[i].ScopeType == "global" && rules[i].RuleKey == b.RuleKey {
			base = &rules[i]
		}
	}
	var fe []FieldError
	if base == nil {
		fe = append(fe, FieldError{Field: "rule_key", Message: "没有这条规则"})
	}
	scopeID := strings.TrimSpace(b.ScopeID)
	switch b.ScopeType {
	case "group":
		if scopeID == "" || utf8.RuneCountInString(scopeID) > 32 {
			fe = append(fe, FieldError{Field: "scope_id", Message: "请填写分组名称（最多 32 个字符）"})
		}
	case "server":
		sid, err := strconv.ParseInt(scopeID, 10, 64)
		if err != nil || sid <= 0 {
			fe = append(fe, FieldError{Field: "scope_id", Message: "节点 ID 无效"})
		} else if _, err := s.store.GetServer(sid); err != nil {
			fe = append(fe, FieldError{Field: "scope_id", Message: "节点不存在"})
		}
	default:
		fe = append(fe, FieldError{Field: "scope_type", Message: "只能为分组（group）或节点（server）新增覆盖"})
	}
	if fe != nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	rule := *base
	rule.ID, rule.ScopeType, rule.ScopeID = 0, b.ScopeType, scopeID
	b.apply(&rule)
	if fe := validateAlertRule(rule); fe != nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	id, err := s.store.CreateAlertRule(rule, time.Now())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	rule.ID = id
	s.audit(r, AuditEntry{ActorType: "admin", Action: "alert_rule.create", Success: true, Details: map[string]any{
		"rule_id": id, "rule_key": rule.RuleKey, "scope": rule.ScopeType + ":" + rule.ScopeID, "enabled": rule.Enabled}})
	writeJSONStatus(w, http.StatusCreated, rule)
}

// handleDeleteAlertRule：DELETE /api/v1/alert-rules/{id}，admin。只能删除覆盖规则，删除后回到上一层的设置；成功 204。
func (s *Server) handleDeleteAlertRule(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	rule, err := s.store.GetAlertRule(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if rule.ScopeType == "global" {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed,
			Details: []FieldError{{Field: "id", Message: "默认规则不能删除，可以关闭"}}})
		return
	}
	if err := s.store.DeleteAlertRule(id); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "alert_rule.delete", Success: true, Details: map[string]any{
		"rule_id": id, "rule_key": rule.RuleKey, "scope": rule.ScopeType + ":" + rule.ScopeID}})
	w.WriteHeader(http.StatusNoContent)
}

// previewItem 是预览中一台会触发的节点。
type previewItem struct {
	ServerID int64   `json:"server_id"`
	Name     string  `json:"name"`
	Value    float64 `json:"value"`
	Detail   string  `json:"detail,omitempty"`
}

// handlePreviewAlertRule：POST /api/v1/alert-rules/preview，admin（设计 16.2）。
// 请求同新增 / 修改（id 可选，表示修改现有规则），返回“按当前数据，此规则会对几台节点触发”。
// 只比较当前值与阈值，不考虑持续时间；节点若被下层规则覆盖，则不计入（以实际生效的规则为准）。
func (s *Server) handlePreviewAlertRule(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID int64 `json:"id"`
		alertRuleBody
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	if err := dec.Decode(&b); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	rules, err := s.store.ListAlertRules()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	var cand *AlertRule
	for i := range rules {
		if (b.ID != 0 && rules[i].ID == b.ID) || (b.ID == 0 && rules[i].ScopeType == "global" && rules[i].RuleKey == b.RuleKey) {
			c := rules[i]
			cand = &c
		}
	}
	if cand == nil {
		s.writeError(w, r, errNoRule)
		return
	}
	if b.ID == 0 {
		cand.ID, cand.ScopeType, cand.ScopeID = -1, b.ScopeType, strings.TrimSpace(b.ScopeID)
		rules = append(rules, *cand)
	}
	b.alertRuleBody.apply(cand)
	for i := range rules {
		if rules[i].ID == cand.ID {
			rules[i] = *cand
		}
	}
	rows, err := s.store.ListServers()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	now := time.Now()
	snaps := s.snapshots()
	items := []previewItem{}
	total := 0
	for _, row := range rows {
		if row.EnrollState == enrollPending {
			continue
		}
		var eff *AlertRule
		for _, er := range EffectiveRules(rules, row.ID, row.Group) {
			er = ruleForInterval(er, reportInterval(row))
			if er.RuleKey == cand.RuleKey {
				e := er
				eff = &e
			}
		}
		// 只统计实际由候选规则决定的节点（被下层覆盖或关闭的不算）
		if eff == nil || eff.ID != cand.ID {
			continue
		}
		total++
		v, detail, ok := alertValue(cand.Type, s.alertInputFor(row, snaps, now))
		if ok && (v > cand.Threshold || (cand.Operator == ">=" && v == cand.Threshold)) {
			items = append(items, previewItem{ServerID: row.ID, Name: row.Name, Value: v, Detail: detail})
		}
	}
	writeJSON(w, map[string]any{"matching": len(items), "total": total, "items": items})
}
