package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// 通知渠道接口（设计 16.5、19.9）。只有 Web 管理员可以访问；渠道修改记入操作日志（设计 24.8）。

// channelBody 是创建 / 修改渠道的请求。修改时凭证字段留空表示保持原值（界面不回显完整凭证）。
type channelBody struct {
	Type           string `json:"type"`
	Name           string `json:"name"`
	Enabled        *bool  `json:"enabled"`
	MinSeverity    string `json:"min_severity"`
	NotifyResolved *bool  `json:"notify_resolved"`
	Config         struct {
		BotToken    string `json:"bot_token"`
		ChatID      string `json:"chat_id"`
		URL         string `json:"url"`
		Secret      string `json:"secret"`
		ClearSecret bool   `json:"clear_secret"`
	} `json:"config"`
}

func decodeChannel(w http.ResponseWriter, r *http.Request) (*channelBody, error) {
	var b channelBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return nil, &APIError{Code: CodeBadRequest, Cause: err}
	}
	return &b, nil
}

// apply 把请求合并到渠道上（c 为已有渠道或新渠道）。
func (b *channelBody) apply(c *NotifyChannel) {
	c.Name = b.Name
	if b.Enabled != nil {
		c.Enabled = *b.Enabled
	}
	if b.MinSeverity != "" {
		c.MinSeverity = b.MinSeverity
	}
	if b.NotifyResolved != nil {
		c.NotifyResolved = *b.NotifyResolved
	}
	if b.Config.BotToken != "" {
		c.Config.BotToken = b.Config.BotToken
	}
	if b.Config.ChatID != "" {
		c.Config.ChatID = b.Config.ChatID
	}
	if b.Config.URL != "" {
		c.Config.URL = b.Config.URL
	}
	if b.Config.Secret != "" {
		c.Config.Secret = b.Config.Secret
	} else if b.Config.ClearSecret {
		c.Config.Secret = ""
	}
}

// handleChannels：GET /api/v1/notification-channels，admin。凭证已脱敏。
func (s *Server) handleChannels(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListChannels()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	out := make([]channelView, 0, len(list))
	for _, c := range list {
		out = append(out, c.view())
	}
	writeJSON(w, map[string]any{"items": out})
}

// handleCreateChannel：POST /api/v1/notification-channels，admin。成功 201，返回脱敏后的渠道。
func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	b, err := decodeChannel(w, r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	c := NotifyChannel{Type: b.Type, Enabled: true, MinSeverity: "warning", NotifyResolved: true}
	b.apply(&c)
	if errs := c.validate(); len(errs) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: errs})
		return
	}
	if err := s.store.SaveChannel(&c, time.Now()); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "notification_channel.create", Success: true,
		Details: map[string]any{"id": c.ID, "type": c.Type, "name": c.Name}})
	writeJSONStatus(w, http.StatusCreated, c.view())
}

// handleUpdateChannel：PUT /api/v1/notification-channels/{id}，admin。类型不可修改；凭证留空保持原值。
func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.store.GetChannel(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	b, err := decodeChannel(w, r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if b.Type != "" && b.Type != c.Type {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "type", Message: "渠道类型不可修改"}}})
		return
	}
	b.apply(&c)
	if errs := c.validate(); len(errs) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: errs})
		return
	}
	if err := s.store.SaveChannel(&c, time.Now()); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "notification_channel.update", Success: true,
		Details: map[string]any{"id": c.ID, "type": c.Type, "name": c.Name, "enabled": c.Enabled}})
	writeJSON(w, c.view())
}

// handleDeleteChannel：DELETE /api/v1/notification-channels/{id}，admin。成功 204；投递记录保留。
func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.store.GetChannel(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.store.DeleteChannel(id); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "notification_channel.delete", Success: true,
		Details: map[string]any{"id": id, "type": c.Type, "name": c.Name}})
	w.WriteHeader(http.StatusNoContent)
}

// handleTestChannel：POST /api/v1/notification-channels/{id}/test，admin（设计 16.5“发送测试通知”）。
// 同步发送一次（不重试），返回 {"ok": true} 或 {"ok": false, "error": "…"}；结果记入投递记录。
// 渠道已停用也可以测试。
func (s *Server) handleTestChannel(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.store.GetChannel(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	m := notifyMessage{Kind: NotifyTest}
	if s.publicURL != "" {
		m.Link = s.publicURL
	}
	err = s.notify.deliver(ctx, c, m, nil)
	s.audit(r, AuditEntry{ActorType: "admin", Action: "notification_channel.test", Success: err == nil,
		Details: map[string]any{"id": c.ID, "type": c.Type, "name": c.Name, "error": errString(err)}})
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleDeliveries：GET /api/v1/notification-deliveries?event_id=&limit=，admin。最近的投递记录，最新在前。
func (s *Server) handleDeliveries(w http.ResponseWriter, r *http.Request) {
	var eventID int64
	if v := r.URL.Query().Get("event_id"); v != "" {
		n, err := parsePositive(v)
		if err != nil {
			s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "event_id", Message: "event_id 无效"}}})
			return
		}
		eventID = n
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "limit", Message: "limit 为 1～200"}}})
			return
		}
		limit = n
	}
	list, err := s.store.ListDeliveries(eventID, limit)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, map[string]any{"items": list})
}
