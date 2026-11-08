package server

// 推送设置（修订第 316 条）：Web 的“推送”页（原“告警”页）常显示，告警照常判定与记录；
//   - 告警推送：原告警总开关（设置项 alerts，修订第 175 条）改为只管推送，默认关闭，可在 Web 打开（需重新验证）
//   - 订阅推送：订阅的扣费、结束、预算提醒单独开关，可选发送渠道与 App 推送，另有新增订阅时默认提前几天提醒
//
// 【安全】打开推送、改变发送渠道会把通知发到外部服务，修改时要求重新输入登录密码（设计 17.4）。

import (
	"context"
	"net/http"
	"slices"
	"time"
)

const settingSubPush = "subscription_push"

// SubPush 是订阅推送设置（修订第 316 条）。
type SubPush struct {
	Enabled bool `json:"enabled"` // 订阅推送总开关，默认关闭
	Charge  bool `json:"charge"`  // 扣费前提醒
	End     bool `json:"end"`     // 结束提醒
	Budget  bool `json:"budget"`  // 超出每月预算提醒
	// ChannelIDs 是发送到的通知渠道，空为全部启用的渠道
	ChannelIDs []int64 `json:"channel_ids"`
	App        bool    `json:"app"` // 同时发送 App 推送
	// DefaultRemindDays 是新增订阅时“提醒”的默认值（天，0 为不提醒）
	DefaultRemindDays int `json:"default_remind_days"`
}

// 订阅提醒的种类（notifyMessage.Sub）
const (
	subPushCharge = "charge"
	subPushEnd    = "end"
	subPushBudget = "budget"
)

// subRemindDays 是“提醒”可选的天数，与 Web / App 的选项一致。
var subRemindDays = []int{0, 1, 2, 3, 7}

func defaultSubPush() SubPush {
	return SubPush{Charge: true, End: true, Budget: true, ChannelIDs: []int64{}, App: true, DefaultRemindDays: 1}
}

// wants 判断某种订阅提醒是否推送。
func (p SubPush) wants(kind string) bool {
	if !p.Enabled {
		return false
	}
	switch kind {
	case subPushCharge:
		return p.Charge
	case subPushEnd:
		return p.End
	case subPushBudget:
		return p.Budget
	}
	return false
}

// subPush 读取订阅推送设置；没有设置项时为默认值（关闭）。
func (s *Server) subPush() SubPush {
	p := defaultSubPush()
	if _, err := s.store.GetSetting(settingSubPush, &p); err != nil {
		s.log.Warn("read subscription push failed", "component", "subscription", "err", err)
		return defaultSubPush()
	}
	if p.ChannelIDs == nil {
		p.ChannelIDs = []int64{}
	}
	return p
}

// ---- 告警推送开关 ----

// handleGetAlertsPush：GET /api/v1/settings/alerts-push，admin。
func (s *Server) handleGetAlertsPush(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, AlertsSetting{Enabled: s.alertsEnabled()})
}

// handlePutAlertsPush：PUT /api/v1/settings/alerts-push，admin，打开时需重新验证。{enabled}
func (s *Server) handlePutAlertsPush(w http.ResponseWriter, r *http.Request) {
	var b AlertsSetting
	if err := decodeJSON(w, r, &b); err != nil {
		s.writeError(w, r, err)
		return
	}
	if b.Enabled && !s.alertsEnabled() { // 【安全】打开推送需重新验证，关闭不需要
		if err := requireReauth(r); err != nil {
			s.writeError(w, r, err)
			return
		}
	}
	if err := s.store.SetAlertsEnabled(b.Enabled); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.reloadAlertsSwitch()
	s.audit(r, AuditEntry{ActorType: "admin", Action: "alerts.push", Success: true, Details: map[string]any{"enabled": b.Enabled}})
	writeJSON(w, b)
}

// ---- 订阅推送 ----

// handleGetSubPush：GET /api/v1/settings/subscription-push，admin。
func (s *Server) handleGetSubPush(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.subPush())
}

// handlePutSubPush：PUT /api/v1/settings/subscription-push，admin。打开推送或改变发送渠道时需重新验证。
func (s *Server) handlePutSubPush(w http.ResponseWriter, r *http.Request) {
	b := defaultSubPush()
	if err := decodeJSON(w, r, &b); err != nil {
		s.writeError(w, r, err)
		return
	}
	if b.ChannelIDs == nil {
		b.ChannelIDs = []int64{}
	}
	var fe []FieldError
	if !slices.Contains(subRemindDays, b.DefaultRemindDays) {
		fe = append(fe, FieldError{Field: "default_remind_days", Message: "请选择默认提醒天数"})
	}
	if len(b.ChannelIDs) > 100 {
		fe = append(fe, FieldError{Field: "channel_ids", Message: "通知渠道过多"})
	}
	channels, err := s.store.ListChannels()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	slices.Sort(b.ChannelIDs)
	b.ChannelIDs = slices.Compact(b.ChannelIDs)
	for _, id := range b.ChannelIDs {
		if !slices.ContainsFunc(channels, func(c NotifyChannel) bool { return c.ID == id }) {
			fe = append(fe, FieldError{Field: "channel_ids", Message: "通知渠道不存在或已删除"})
			break
		}
	}
	if fe != nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	old := s.subPush()
	// 【安全】打开推送、换发送渠道、打开 App 推送会把订阅信息发到别处，需重新验证；关闭与其他修改不需要
	if (b.Enabled && !old.Enabled) || (b.App && !old.App) || !slices.Equal(b.ChannelIDs, old.ChannelIDs) {
		if err := requireReauth(r); err != nil {
			s.writeError(w, r, err)
			return
		}
	}
	if err := s.store.PutSetting(settingSubPush, b, time.Now()); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "subscription.push", Success: true,
		Details: map[string]any{"enabled": b.Enabled, "charge": b.Charge, "end": b.End, "budget": b.Budget, "channel_ids": b.ChannelIDs, "app": b.App}})
	writeJSON(w, b)
}

// remindSub 发送订阅提醒（修订第 316 条）：按订阅推送设置决定是否推送与发到哪些渠道，不受告警推送开关影响。
// 免打扰时段内不记录、不发送，下次检查（每小时）再发；严重级按免打扰的“严重告警照常通知”设置。
func (s *Server) remindSub(key string, m notifyMessage, now time.Time) {
	p := s.subPush()
	on := p.wants(m.Sub)
	if q := s.quiet.get(); on && q.active(now) && !(m.Severity == SeverityCritical && q.Critical == "notify") {
		return
	}
	first, err := s.store.MarkReminder(key, now)
	if err != nil {
		s.log.Error("record reminder failed", "component", "reminder", "err", err)
		return
	}
	if !first {
		return
	}
	m.Kind = NotifyReminder
	s.log.Info("reminder", "component", "reminder", "key", key, "server", m.ServerName, "push", on)
	s.recordNotice(eventReminder, m, now)
	if on {
		s.notify.dispatchSub(m, p)
	}
}

// dispatchSub 把订阅提醒发到订阅推送选中的渠道（空为全部启用的渠道）与 App 推送。
func (n *notifier) dispatchSub(m notifyMessage, p SubPush) {
	m = m.withLang(n.s.lang())
	channels, err := n.s.store.ListChannels()
	if err != nil {
		n.s.log.Error("list notification channels failed", "component", "notify", "err", err)
		return
	}
	for _, c := range channels {
		if !c.Enabled || (len(p.ChannelIDs) > 0 && !slices.Contains(p.ChannelIDs, c.ID)) {
			continue
		}
		n.wg.Add(1)
		go func(c NotifyChannel) {
			defer n.wg.Done()
			n.sem <- struct{}{}
			defer func() { <-n.sem }()
			n.deliver(context.Background(), c, m, n.backoff)
		}(c)
	}
	if p.App {
		n.dispatchApp(m)
	}
}
