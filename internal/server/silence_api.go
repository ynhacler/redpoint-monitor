package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// 静音与维护接口（设计 16.6、19.9）。App 阶段 C 开放给设备凭证（低风险操作，设计 8.4.1）。

// silenceDurations：可选的时长（设计 1.5.14）；空表示直到手动结束。
var silenceDurations = map[string]time.Duration{"1h": time.Hour, "8h": 8 * time.Hour, "24h": 24 * time.Hour}

// handleSilences：GET /api/v1/silences，admin。返回生效中的静音与维护，最新在前。
func (s *Server) handleSilences(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ActiveSilences(time.Now())
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeList(w, list, "", nil)
}

// handleCreateSilence：POST /api/v1/silences，admin。
// 请求 {"kind": "mute|maintenance", "scope_type": "server|group|rule|global", "scope_id", "duration": "1h|8h|24h|"（空为直到手动结束）, "reason"}。
// 维护只能针对单个节点。同一对象同一类型已有生效记录时以新设置为准。成功 201。
func (s *Server) handleCreateSilence(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Kind      string `json:"kind"`
		ScopeType string `json:"scope_type"`
		ScopeID   string `json:"scope_id"`
		Duration  string `json:"duration"`
		Reason    string `json:"reason"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	var fe []FieldError
	bad := func(f, m string) { fe = append(fe, FieldError{Field: f, Message: m}) }
	b.ScopeID, b.Reason = strings.TrimSpace(b.ScopeID), strings.TrimSpace(b.Reason)
	if b.Kind != SilenceMute && b.Kind != SilenceMaintenance {
		bad("kind", "类型只能是 mute（静音）或 maintenance（维护）")
	}
	switch b.ScopeType {
	case "server":
		id, err := strconv.ParseInt(b.ScopeID, 10, 64)
		if err != nil || id <= 0 {
			bad("scope_id", "节点 ID 无效")
		} else if _, err := s.store.GetServer(id); err != nil {
			bad("scope_id", "节点不存在")
		}
	case "group":
		if b.ScopeID == "" || utf8.RuneCountInString(b.ScopeID) > 32 {
			bad("scope_id", "请填写分组名称（最多 32 个字符）")
		}
	case "rule":
		if _, ok := ruleKeyExists(s, b.ScopeID); !ok {
			bad("scope_id", "没有这条规则")
		}
	case "global":
		b.ScopeID = ""
	default:
		bad("scope_type", "对象只能是 server、group、rule 或 global")
	}
	if b.Kind == SilenceMaintenance && b.ScopeType != "server" {
		bad("scope_type", "维护模式只能针对单个节点")
	}
	d, ok := silenceDurations[b.Duration]
	if !ok && b.Duration != "" {
		bad("duration", "时长只能是 1h、8h、24h，或留空表示直到手动结束")
	}
	if utf8.RuneCountInString(b.Reason) > 200 {
		bad("reason", "原因最多 200 个字符")
	}
	if fe != nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	now := time.Now()
	x := Silence{ScopeType: b.ScopeType, ScopeID: b.ScopeID, Kind: b.Kind, Reason: b.Reason, StartsAt: now.Unix(), CreatedBy: "admin"}
	if se := info(r).session; se != nil {
		x.CreatedBy = "admin:" + se.User.Username
	}
	if d > 0 {
		end := now.Add(d).Unix()
		x.EndsAt = &end
	}
	id, err := s.store.CreateSilence(x, now)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	x.ID = id
	s.refreshSilences(now)
	s.audit(r, AuditEntry{ActorType: "admin", Action: "silence.create", Success: true, Details: map[string]any{
		"silence_id": id, "kind": x.Kind, "scope": x.ScopeType + ":" + x.ScopeID, "duration": b.Duration}})
	writeJSONStatus(w, http.StatusCreated, x)
}

// handleEndSilence：DELETE /api/v1/silences/{id}，admin。立即结束（保留记录）；成功 204。
func (s *Server) handleEndSilence(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	now := time.Now()
	x, err := s.store.EndSilence(id, now)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.refreshSilences(now)
	s.audit(r, AuditEntry{ActorType: "admin", Action: "silence.end", Success: true, Details: map[string]any{
		"silence_id": id, "kind": x.Kind, "scope": x.ScopeType + ":" + x.ScopeID}})
	w.WriteHeader(http.StatusNoContent)
}

// refreshSilences 让修改立即反映到节点视图（维护对告警的影响在下一轮评估生效，10 秒内）。
func (s *Server) refreshSilences(now time.Time) {
	if list, err := s.store.ActiveSilences(now); err == nil {
		s.alerts.setSilences(list)
	}
}

func ruleKeyExists(s *Server, key string) (AlertRule, bool) {
	rules, err := s.store.ListAlertRules()
	if err != nil {
		return AlertRule{}, false
	}
	for _, r := range rules {
		if r.ScopeType == "global" && r.RuleKey == key {
			return r, true
		}
	}
	return AlertRule{}, false
}
