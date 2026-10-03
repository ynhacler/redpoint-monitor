package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// 告警通知（设计 16.5、18.15、31）：Telegram 与 Webhook，由用户自己的面板直接发送。
//
//   - 告警进入 firing、恢复、仍未恢复到达重复间隔时，按渠道的最低级别发送；已静音的告警照常记录、不发送
//   - 每次投递记录到 notification_deliveries（保留 30 天），失败后按 2s / 8s / 30s 重试 3 次
//   - 【安全】渠道凭证（Bot Token、Webhook 地址与签名密钥）只用于发送：接口返回时脱敏，不写日志，
//     错误信息中不含请求地址（Telegram 的地址包含 Bot Token）（设计 24.7）
//   - Webhook 只允许 HTTPS（回环地址除外，CLAUDE.md 约束 6），不跟随重定向；设置了密钥时附 HMAC-SHA256 签名

// 渠道类型
const (
	ChannelTelegram = "telegram"
	ChannelWebhook  = "webhook"
)

// 通知种类
const (
	NotifyFiring   = "firing"
	NotifyResolved = "resolved"
	NotifyRepeat   = "repeat"
	NotifyTest     = "test"
	// 降噪（设计 16.3、16.4，alert_noise.go）
	NotifyFlapping    = "flapping"     // 状态频繁变化：之后暂停该告警的通知
	NotifyStillFiring = "still_firing" // 抖动结束时仍在告警
	NotifyPanelDown   = "panel_down"   // 全部节点同时停止上报，疑似面板一侧异常
	NotifyPanelUp     = "panel_up"
)

// 投递状态
const (
	DeliverySending = "retrying" // 发送中或等待重试
	DeliverySent    = "sent"
	DeliveryFailed  = "failed"
)

const deliveryRetention = 30 * 24 * time.Hour

// NotifyChannel 是一个通知渠道。Config 中的凭证只在发送时使用，接口返回前经 maskedConfig 脱敏。
type NotifyChannel struct {
	ID             int64         `json:"id"`
	Type           string        `json:"type"`
	Name           string        `json:"name"`
	Enabled        bool          `json:"enabled"`
	MinSeverity    string        `json:"min_severity"`    // critical / warning / info：达到该级别才发送
	NotifyResolved bool          `json:"notify_resolved"` // 恢复时是否通知
	Config         channelConfig `json:"-"`
	CreatedAt      int64         `json:"created_at"`
	UpdatedAt      int64         `json:"updated_at"`
}

// channelConfig 是渠道的连接参数，以 JSON 保存在 notification_channels.config。
type channelConfig struct {
	BotToken string `json:"bot_token,omitempty"` // Telegram
	ChatID   string `json:"chat_id,omitempty"`   // Telegram：数字 ID 或 @频道名
	URL      string `json:"url,omitempty"`       // Webhook
	Secret   string `json:"secret,omitempty"`    // Webhook：可选，HMAC-SHA256 签名密钥
}

// channelView 是返回给 Web 的渠道：凭证脱敏，签名密钥只说明是否已设置。
type channelView struct {
	NotifyChannel
	Config map[string]any `json:"config"`
}

func (c NotifyChannel) view() channelView {
	cfg := map[string]any{}
	switch c.Type {
	case ChannelTelegram:
		cfg["bot_token"] = maskBotToken(c.Config.BotToken)
		cfg["chat_id"] = c.Config.ChatID
	case ChannelWebhook:
		cfg["url"] = maskURL(c.Config.URL)
		cfg["has_secret"] = c.Config.Secret != ""
	}
	return channelView{NotifyChannel: c, Config: cfg}
}

// maskBotToken：123456789:AA…wxyz
func maskBotToken(t string) string {
	id, secret, ok := strings.Cut(t, ":")
	if !ok || len(secret) < 6 {
		return "…"
	}
	return id + ":" + secret[:2] + "…" + secret[len(secret)-4:]
}

// maskURL 只保留协议与主机：Webhook 地址的路径或参数中常含密钥（如各类机器人地址）
func maskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "…"
	}
	if u.Path == "" && u.RawQuery == "" {
		return u.Scheme + "://" + u.Host
	}
	return u.Scheme + "://" + u.Host + "/…"
}

var (
	botTokenPattern = regexp.MustCompile(`^\d{5,15}:[A-Za-z0-9_-]{30,64}$`)
	chatIDPattern   = regexp.MustCompile(`^(-?\d{1,20}|@[A-Za-z][A-Za-z0-9_]{4,31})$`)
)

// validate 校验渠道字段，返回字段错误。
func (c *NotifyChannel) validate() []FieldError {
	var errs []FieldError
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" || len([]rune(c.Name)) > 40 {
		errs = append(errs, FieldError{Field: "name", Message: "名称为 1～40 个字符"})
	}
	switch c.MinSeverity {
	case SeverityCritical, SeverityWarning, SeverityInfo:
	default:
		errs = append(errs, FieldError{Field: "min_severity", Message: "级别无效"})
	}
	switch c.Type {
	case ChannelTelegram:
		c.Config.BotToken = strings.TrimSpace(c.Config.BotToken)
		c.Config.ChatID = strings.TrimSpace(c.Config.ChatID)
		c.Config.URL, c.Config.Secret = "", ""
		if !botTokenPattern.MatchString(c.Config.BotToken) {
			errs = append(errs, FieldError{Field: "config.bot_token", Message: "Bot Token 格式不正确，应形如 123456789:AA…（从 @BotFather 获取）"})
		}
		if !chatIDPattern.MatchString(c.Config.ChatID) {
			errs = append(errs, FieldError{Field: "config.chat_id", Message: "Chat ID 应为数字（群组为负数）或 @频道名"})
		}
	case ChannelWebhook:
		c.Config.URL = strings.TrimSpace(c.Config.URL)
		c.Config.BotToken, c.Config.ChatID = "", ""
		if msg := checkWebhookURL(c.Config.URL); msg != "" {
			errs = append(errs, FieldError{Field: "config.url", Message: msg})
		}
		if len(c.Config.Secret) > 128 {
			errs = append(errs, FieldError{Field: "config.secret", Message: "签名密钥最长 128 个字符"})
		}
	default:
		errs = append(errs, FieldError{Field: "type", Message: "渠道类型应为 telegram 或 webhook"})
	}
	return errs
}

// checkWebhookURL：只允许 HTTPS；回环地址（本机上的接收程序）允许 HTTP（CLAUDE.md 约束 6）。
func checkWebhookURL(raw string) string {
	if raw == "" || len(raw) > 2048 {
		return "请填写 Webhook 地址"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return "地址格式不正确"
	}
	switch u.Scheme {
	case "https":
		return ""
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return ""
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return ""
		}
		return "只支持 HTTPS 地址（本机回环地址除外）"
	}
	return "只支持 HTTPS 地址"
}

// ---- 存储 ----

const channelColumns = `id, type, name, enabled, min_severity, notify_resolved, config, created_at, updated_at`

func scanChannel(sc interface{ Scan(...any) error }) (NotifyChannel, error) {
	var c NotifyChannel
	var cfg string
	err := sc.Scan(&c.ID, &c.Type, &c.Name, &c.Enabled, &c.MinSeverity, &c.NotifyResolved, &cfg, &c.CreatedAt, &c.UpdatedAt)
	if err == nil {
		err = json.Unmarshal([]byte(cfg), &c.Config)
	}
	return c, err
}

func (s *Store) ListChannels() ([]NotifyChannel, error) {
	rows, err := s.DB.Query(`SELECT ` + channelColumns + ` FROM notification_channels ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NotifyChannel{}
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

var errNoChannel = errorf(CodeNotFound, "通知渠道不存在或已删除")

func (s *Store) GetChannel(id int64) (NotifyChannel, error) {
	c, err := scanChannel(s.DB.QueryRow(`SELECT `+channelColumns+` FROM notification_channels WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, errNoChannel
	}
	return c, err
}

func (s *Store) SaveChannel(c *NotifyChannel, now time.Time) error {
	cfg, _ := json.Marshal(c.Config)
	c.UpdatedAt = now.Unix()
	if c.ID == 0 {
		c.CreatedAt = now.Unix()
		res, err := s.DB.Exec(`INSERT INTO notification_channels (type, name, enabled, min_severity, notify_resolved, config,
			created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)`, c.Type, c.Name, c.Enabled, c.MinSeverity, c.NotifyResolved,
			string(cfg), c.CreatedAt, c.UpdatedAt)
		if err != nil {
			return err
		}
		c.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := s.DB.Exec(`UPDATE notification_channels SET name = ?, enabled = ?, min_severity = ?, notify_resolved = ?,
		config = ?, updated_at = ? WHERE id = ?`, c.Name, c.Enabled, c.MinSeverity, c.NotifyResolved, string(cfg), c.UpdatedAt, c.ID)
	return err
}

func (s *Store) DeleteChannel(id int64) error {
	res, err := s.DB.Exec(`DELETE FROM notification_channels WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoChannel
	}
	return nil
}

// Delivery 是一次通知投递（设计 18.15）。
type Delivery struct {
	ID          int64  `json:"id"`
	ChannelID   int64  `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	ChannelType string `json:"channel_type"`
	EventID     int64  `json:"event_id"` // 测试通知为 0
	ServerName  string `json:"server_name"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Attempts    int    `json:"attempts"`
	LastError   string `json:"last_error"`
	CreatedAt   int64  `json:"created_at"`
	SentAt      int64  `json:"sent_at"`
}

func (s *Store) insertDelivery(d *Delivery) error {
	res, err := s.DB.Exec(`INSERT INTO notification_deliveries (channel_id, channel_name, channel_type, event_id, server_name,
		kind, title, status, attempts, last_error, created_at, sent_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ChannelID, d.ChannelName, d.ChannelType, d.EventID, d.ServerName, d.Kind, d.Title, d.Status, d.Attempts, d.LastError,
		d.CreatedAt, d.SentAt)
	if err == nil {
		d.ID, _ = res.LastInsertId()
	}
	return err
}

func (s *Store) updateDelivery(d *Delivery) error {
	_, err := s.DB.Exec(`UPDATE notification_deliveries SET status = ?, attempts = ?, last_error = ?, sent_at = ? WHERE id = ?`,
		d.Status, d.Attempts, d.LastError, d.SentAt, d.ID)
	return err
}

// ListDeliveries 返回最近的投递记录，最新在前；eventID 非 0 时只返回该告警事件的。
func (s *Store) ListDeliveries(eventID int64, limit int) ([]Delivery, error) {
	q := `SELECT id, channel_id, channel_name, channel_type, event_id, server_name, kind, title, status, attempts, last_error,
		created_at, sent_at FROM notification_deliveries`
	args := []any{}
	if eventID > 0 {
		q += ` WHERE event_id = ?`
		args = append(args, eventID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.ChannelID, &d.ChannelName, &d.ChannelType, &d.EventID, &d.ServerName, &d.Kind, &d.Title,
			&d.Status, &d.Attempts, &d.LastError, &d.CreatedAt, &d.SentAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// PruneDeliveries 删除 30 天前的投递记录（设计 18.15）。
func (s *Store) PruneDeliveries(now time.Time) (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM notification_deliveries WHERE created_at < ?`, now.Add(-deliveryRetention).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---- 消息 ----

// notifyMessage 是一条要发送的通知。
type notifyMessage struct {
	Kind       string
	EventID    int64
	ServerID   int64
	ServerName string
	RuleKey    string
	Type       string
	Severity   string
	Value      float64
	Threshold  float64
	Message    string // 如“磁盘 / 使用率 96%（阈值 95%）”
	StartedAt  time.Time
	ResolvedAt time.Time
	Link       string   // 节点详情地址；未配置 --public-url 时为空
	Servers    []string // 合并通知包含的节点（批量离线）
	Count      int      // 合并的节点数 / 抖动窗口内的触发次数 / 面板自检时的节点数
}

var severityIcons = map[string]string{"critical": "🔴", "warning": "🟠", "info": "🔵"}

// title 是一行摘要，用于投递记录与 Telegram 第一行。
func (m notifyMessage) title() string {
	if len(m.Servers) > 0 { // 批量离线（设计 16.4）
		if m.Kind == NotifyResolved {
			return fmt.Sprintf("✅ %d 台节点恢复上报：%s", m.Count, namesBrief(m.Servers))
		}
		return fmt.Sprintf("%s %d 台节点同时离线：%s", severityIcons[m.Severity], m.Count, namesBrief(m.Servers))
	}
	switch m.Kind {
	case NotifyResolved:
		return "✅ " + m.ServerName + " 已恢复：" + m.Message
	case NotifyRepeat:
		return severityIcons[m.Severity] + " 仍未恢复 · " + m.ServerName + " " + m.Message
	case NotifyFlapping:
		return "〰️ " + m.ServerName + " 状态频繁变化：" + m.Message
	case NotifyStillFiring:
		return severityIcons[m.Severity] + " 状态已稳定，仍在告警 · " + m.ServerName + " " + m.Message
	case NotifyPanelDown, NotifyPanelUp:
		t, _ := panelText(m)
		return t
	case NotifyTest:
		return "🔔 测试通知"
	}
	return severityIcons[m.Severity] + " " + m.ServerName + " " + m.Message
}

// text 是完整的纯文本（设计 16.5 的模板）。
func (m notifyMessage) text(now time.Time) string {
	lines := []string{m.title()}
	if len(m.Servers) > 0 {
		if m.Kind == NotifyFiring {
			lines = append(lines, "可能是面板网络或同一服务商故障")
		}
		if len(m.Servers) > 3 {
			lines = append(lines, "全部："+strings.Join(m.Servers, "、"))
		}
		return strings.Join(lines, "\n")
	}
	switch m.Kind {
	case NotifyFlapping:
		lines = append(lines, fmt.Sprintf("30 分钟内触发 %d 次，暂停这条告警的通知；稳定 30 分钟后恢复。", m.Count))
	case NotifyPanelDown:
		_, d := panelText(m)
		lines = append(lines, d)
	case NotifyFiring, NotifyRepeat, NotifyStillFiring:
		// 刚触发时持续时间不足 1 分钟（离线等规则的时长已写在 Message 里），不显示“已持续”
		if d := now.Sub(m.StartedAt); d >= time.Minute {
			lines = append(lines, "已持续 "+fmtSpan(d))
		}
		lines = append(lines, "开始时间 "+m.StartedAt.Format("2006-01-02 15:04"))
	case NotifyResolved:
		lines = append(lines, "持续时长 "+fmtSpan(m.ResolvedAt.Sub(m.StartedAt)))
	case NotifyTest:
		lines = append(lines, "这是一条来自 VPS Monitor 的测试通知，收到说明渠道配置正确。")
	}
	if m.Link != "" {
		lines = append(lines, m.Link)
	}
	return strings.Join(lines, "\n")
}

// fmtSpan：1 小时 24 分钟 / 12 分钟 / 30 秒
func fmtSpan(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	}
	h, m := int(d.Hours()), int(d.Minutes())%60
	switch {
	case h >= 24:
		return fmt.Sprintf("%d 天 %d 小时", h/24, h%24)
	case h > 0 && m > 0:
		return fmt.Sprintf("%d 小时 %d 分钟", h, m)
	case h > 0:
		return fmt.Sprintf("%d 小时", h)
	}
	return fmt.Sprintf("%d 分钟", m)
}

// webhookPayload 是 Webhook 的请求体（version 1，只增加字段）。
func (m notifyMessage) webhookPayload(now time.Time) []byte {
	p := map[string]any{"version": 1, "kind": m.Kind, "text": m.text(now), "title": m.title(), "sent_at": now.Unix()}
	if len(m.Servers) > 0 {
		p["type"], p["severity"], p["count"], p["servers"] = m.Type, m.Severity, m.Count, m.Servers
	} else if m.Kind == NotifyPanelDown || m.Kind == NotifyPanelUp {
		p["severity"], p["count"] = m.Severity, m.Count
	} else if m.Kind != NotifyTest {
		p["event_id"] = m.EventID
		p["server"] = map[string]any{"id": m.ServerID, "name": m.ServerName}
		p["rule_key"], p["type"], p["severity"] = m.RuleKey, m.Type, m.Severity
		p["value"], p["threshold"], p["message"] = m.Value, m.Threshold, m.Message
		p["started_at"] = m.StartedAt.Unix()
		if !m.ResolvedAt.IsZero() {
			p["resolved_at"] = m.ResolvedAt.Unix()
		}
		if m.Link != "" {
			p["url"] = m.Link
		}
	}
	b, _ := json.Marshal(p)
	return b
}

// wants 判断渠道是否接收这条通知。
func (c NotifyChannel) wants(m notifyMessage) bool {
	if !c.Enabled {
		return false
	}
	if m.Kind == NotifyTest || m.Kind == NotifyPanelDown || m.Kind == NotifyPanelUp {
		return true // 面板自检影响全部节点，所有渠道都发
	}
	if m.Kind == NotifyResolved && !c.NotifyResolved {
		return false
	}
	return severityRank(m.Severity) >= severityRank(c.MinSeverity)
}

// ---- 发送 ----

// notifier 异步发送通知：告警评估不等待网络。并发上限 4，每次投递独立重试。
type notifier struct {
	s       *Server
	sem     chan struct{}
	backoff []time.Duration // 重试前的等待；长度即重试次数（设计 16.5：重试 3 次）
	wg      sync.WaitGroup
	http    *http.Client
	tgAPI   string // Telegram Bot API 地址，测试时替换
}

func newNotifier(s *Server) *notifier {
	return &notifier{s: s, sem: make(chan struct{}, 4), backoff: []time.Duration{2 * time.Second, 8 * time.Second, 30 * time.Second},
		tgAPI: "https://api.telegram.org",
		http: &http.Client{Timeout: 10 * time.Second,
			// 【安全】不跟随重定向：Webhook 地址由管理员填写，不被引导到其他地址
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// dispatch 把通知交给所有接收它的渠道（异步）。
func (n *notifier) dispatch(m notifyMessage) {
	channels, err := n.s.store.ListChannels()
	if err != nil {
		n.s.log.Error("list notification channels failed", "component", "notify", "err", err)
		return
	}
	for _, c := range channels {
		if !c.wants(m) {
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
}

// deliver 发送到一个渠道，失败按 backoff 重试，记录投递结果。返回最终的错误。
func (n *notifier) deliver(ctx context.Context, c NotifyChannel, m notifyMessage, backoff []time.Duration) error {
	now := time.Now()
	d := &Delivery{ChannelID: c.ID, ChannelName: c.Name, ChannelType: c.Type, EventID: m.EventID, ServerName: m.ServerName,
		Kind: m.Kind, Title: m.title(), Status: DeliverySending, CreatedAt: now.Unix()}
	if err := n.s.store.insertDelivery(d); err != nil {
		n.s.log.Error("delivery insert failed", "component", "notify", "err", err)
	}
	var err error
	for attempt := 0; ; attempt++ {
		err = n.send(ctx, c, m)
		d.Attempts = attempt + 1
		if err == nil {
			d.Status, d.LastError, d.SentAt = DeliverySent, "", time.Now().Unix()
			break
		}
		d.LastError = err.Error()
		if attempt >= len(backoff) {
			d.Status = DeliveryFailed
			break
		}
		n.s.store.updateDelivery(d)
		select {
		case <-ctx.Done():
			d.Status = DeliveryFailed
			return ctx.Err()
		case <-time.After(backoff[attempt]):
		}
	}
	if d.ID > 0 {
		n.s.store.updateDelivery(d)
	}
	if err != nil {
		n.s.log.Warn("notification failed", "component", "notify", "channel_id", c.ID, "type", c.Type, "kind", m.Kind,
			"attempts", d.Attempts, "err", err)
	}
	return err
}

// send 发送一次。错误信息不含请求地址与凭证。
func (n *notifier) send(ctx context.Context, c NotifyChannel, m notifyMessage) error {
	now := time.Now()
	var req *http.Request
	var err error
	switch c.Type {
	case ChannelTelegram:
		body, _ := json.Marshal(map[string]any{"chat_id": c.Config.ChatID, "text": m.text(now), "disable_web_page_preview": true})
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, n.tgAPI+"/bot"+c.Config.BotToken+"/sendMessage", bytes.NewReader(body))
	case ChannelWebhook:
		body := m.webhookPayload(now)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.Config.URL, bytes.NewReader(body))
		if err == nil && c.Config.Secret != "" {
			mac := hmac.New(sha256.New, []byte(c.Config.Secret))
			mac.Write(body)
			req.Header.Set("X-Vpsmon-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		}
	default:
		return errors.New("未知的渠道类型")
	}
	if err != nil {
		return errors.New("请求构造失败")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "vpsmon-server/"+n.s.version)
	resp, err := n.http.Do(req)
	if err != nil {
		// url.Error 的文字包含请求地址（Telegram 地址含 Bot Token）：只保留底层原因
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("无法连接：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	msg := fmt.Sprintf("返回 %d", resp.StatusCode)
	if c.Type == ChannelTelegram {
		// Telegram 的错误说明（如 chat not found）有助于排查；不含 Token
		var tg struct {
			Description string `json:"description"`
		}
		if json.Unmarshal(detail, &tg) == nil && tg.Description != "" {
			msg += "：" + tg.Description
		}
	}
	return errors.New(msg)
}
