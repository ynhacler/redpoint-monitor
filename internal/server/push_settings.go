package server

// 推送设置（修订第 316 条）：Web 的“推送”页（原“告警”页）常显示，告警照常判定与记录；
//   - 告警推送：原告警总开关（设置项 alerts，修订第 175 条）改为只管推送，默认关闭，可在 Web 打开（需重新验证）
//   - 订阅推送：订阅的扣费、结束、预算提醒单独开关，可选发送渠道与 App 推送，另有新增订阅时默认提前几天提醒
//
// 【安全】打开推送、改变发送渠道会把通知发到外部服务，修改时要求重新输入登录密码（设计 17.4）。

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
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

// ---- 预览与测试（修订第 317 条） ----

// SubPushPreviewItem 是接下来会推送的一条订阅提醒。
type SubPushPreviewItem struct {
	Date           string `json:"date"` // 推送日期 YYYY-MM-DD（面板本地时区）
	Kind           string `json:"kind"` // charge / end
	SubscriptionID int64  `json:"subscription_id"`
	Name           string `json:"name"`
	Target         string `json:"target"` // 扣费日或结束日期
	Push           bool   `json:"push"`   // 按当前设置是否会推送（订阅推送关闭或该种类关闭时为 false，只记录）
}

// subPushPreviewDays 是预览的天数。
const subPushPreviewDays = 14

// upcomingSubReminders 按与 subscriptionReminders 相同的规则，算出 today 起 days 天内会发出的扣费与结束提醒（按日期排序）。
// 已发过的（reminders_sent 中有记录）不再列出。sent 为 nil 时不检查。
func upcomingSubReminders(list []Subscription, today time.Time, days int, p SubPush, sent func(key string) bool) []SubPushPreviewItem {
	last := today.AddDate(0, 0, days-1)
	out := []SubPushPreviewItem{}
	add := func(date time.Time, kind, key string, x Subscription, target string) {
		if date.Before(today) {
			date = today // 已进入提醒区间但还没发：今天（下一次检查）发
		}
		if date.After(last) || (sent != nil && sent(key)) {
			return
		}
		out = append(out, SubPushPreviewItem{Date: date.Format("2006-01-02"), Kind: kind, SubscriptionID: x.ID, Name: x.Name,
			Target: target, Push: p.wants(kind)})
	}
	for _, x := range list {
		// 扣费前提醒：每个扣费日在提前 remind_days 天时提醒一次
		if x.RemindDays > 0 {
			for t, n := nextCharge(x, today), 0; !t.IsZero() && n < 20; t, n = nextCharge(x, t.AddDate(0, 0, 1)), n+1 {
				fire := t.AddDate(0, 0, -x.RemindDays)
				if fire.After(last) {
					break
				}
				add(fire, subPushCharge, fmt.Sprintf("subscription:%d:%s", x.ID, t.Format("2006-01-02")), x, t.Format("2006-01-02"))
			}
		}
		// 结束提醒：结束前 7 / 3 / 1 天与当天
		if x.EndDate == "" {
			continue
		}
		end, err := time.ParseInLocation("2006-01-02", x.EndDate, today.Location())
		if err != nil {
			continue
		}
		cur, inBracket := subEndBracket(calendarDays(today, end))
		if inBracket && cur == -1 { // 刚结束 3 天内：补发“已结束”
			add(today, subPushEnd, fmt.Sprintf("subscription_end:%d:%s:%d", x.ID, x.EndDate, -1), x, x.EndDate)
		}
		for _, m := range subEndMilestones {
			fire := end.AddDate(0, 0, -m)
			if fire.Before(today) && !(inBracket && m == cur) {
				continue // 已经过去的里程碑：只有当前所在区间还会补发
			}
			add(fire, subPushEnd, fmt.Sprintf("subscription_end:%d:%s:%d", x.ID, x.EndDate, m), x, x.EndDate)
		}
	}
	slices.SortStableFunc(out, func(a, b SubPushPreviewItem) int { return strings.Compare(a.Date, b.Date) })
	return out
}

// ReminderSent 报告去重键是否已经发过。
func (s *Store) ReminderSent(key string) bool {
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM reminders_sent WHERE key = ?`, key).Scan(&n)
	return n > 0
}

// handleSubPushPreview：GET /api/v1/settings/subscription-push/preview，admin。接下来 14 天会推送的订阅提醒。
func (s *Server) handleSubPushPreview(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSubscriptions()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	writeJSON(w, map[string]any{"days": subPushPreviewDays, "next_cursor": "",
		"items": upcomingSubReminders(list, today, subPushPreviewDays, s.subPush(), s.store.ReminderSent)})
}

// handleSubPushTest：POST /api/v1/settings/subscription-push/test，admin。
// 按已保存的渠道设置同步发送一条订阅提醒样例（不看总开关与种类开关，方便打开前先试），返回每个渠道的结果；
// 勾选了 App 推送时另外异步发给 App，返回会收到的设备数。结果记入投递记录。
func (s *Server) handleSubPushTest(w http.ResponseWriter, r *http.Request) {
	p := s.subPush()
	channels, err := s.store.ListChannels()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	l := s.lang()
	m := notifyMessage{Kind: NotifyReminder, Type: "subscription", Sub: subPushCharge, Severity: SeverityWarning,
		ServerName: l.f("订阅“%s”", l.t("示例订阅")), Message: l.t("这是一条测试：订阅提醒会这样推送"), Lang: l}
	if s.publicURL != "" {
		m.Link = s.publicURL + "/subscriptions"
	}
	type result struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	out := []result{}
	for _, c := range channels {
		if !c.Enabled || (len(p.ChannelIDs) > 0 && !slices.Contains(p.ChannelIDs, c.ID)) {
			continue
		}
		err := s.notify.deliver(ctx, c, m, nil)
		out = append(out, result{ID: c.ID, Name: c.Name, OK: err == nil, Error: errString(err)})
	}
	// App 推送：只在配置了推送中转时发送，返回会收到的设备数（pushWants 对全部节点授权的设备放行）
	devices := 0
	if p.App && s.push.relay != "" {
		if targets, err := s.store.PushTargets(time.Now()); err == nil {
			for _, t := range targets {
				if s.pushWants(t, m) {
					devices++
				}
			}
		}
		s.notify.dispatchApp(m)
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "subscription.push_test", Success: true,
		Details: map[string]any{"channels": len(out), "app": p.App, "app_devices": devices}})
	writeJSON(w, map[string]any{"channels": out, "app": p.App, "app_devices": devices})
}
