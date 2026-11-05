package server

// 公开展示页（设计 45.3，修订第 303 条）：不用登录就能看的大屏，地址 /status，数据来自 GET /api/v1/public/servers。
//
// 【安全】这是面板唯一不需要凭证就能读到节点数据的接口：
//   - 默认关闭；关闭时接口返回 404，与不存在没有区别；节点默认不公开，由管理员逐个选择。
//   - 响应只用 publicServer 的白名单字段组装，不序列化 ServerRow 或 Report，节点新增字段不会自动公开。
//     不含 IP、主机名、服务商、价格、到期、端口、告警内容与任何内部 ID。
//   - 名称是用户自己填的，可能写了 IP 或手机号：输出前遮盖（maskPublicName）。
//   - 按 IP 每分钟 60 次，服务端缓存 5 秒；修改设置时清空缓存，取消公开立即生效。
//   - 订阅单独开启（默认关闭），只读：只有 GET，字段同样走白名单（publicSubscription），
//     不含电话号码、备注、支付方式、VPS 地址、开始日期、上传的图标与内部 ID；已结束的订阅不显示。
//   - 云平台不在展示页中。连通性测试在访客自己的浏览器里运行，不读面板数据，不需要接口。
//   - 公开标记存在 servers 表的列上，不存 ID 列表：节点 ID 删除后会复用，列表会让新节点继承旧节点的公开状态。

import (
	"cmp"
	"math"
	"net/http"
	"regexp"
	"slices"
	"sync"
	"time"
)

const (
	settingStatusPage = "status_page"
	statusPageTTL     = 5 * time.Second
	statusPagePerMin  = 60
)

// StatusPageSetting 是公开展示页的开关（api/openapi.yaml StatusPageSetting 中的 enabled）。
type StatusPageSetting struct {
	Enabled       bool `json:"enabled"`
	Subscriptions bool `json:"subscriptions"` // 是否公开订阅（只读）
}

type statusPageView struct {
	Enabled       bool    `json:"enabled"`
	Subscriptions bool    `json:"subscriptions"`
	ServerIDs     []int64 `json:"server_ids"`
}

// publicSubscription 是访客能看到的全部订阅字段（设计 45.3）。
type publicSubscription struct {
	Name       string `json:"name"`
	Brand      string `json:"brand"`
	Category   string `json:"category"`
	PriceCents int64  `json:"price_cents"`
	Currency   string `json:"currency"`
	Cycle      string `json:"cycle"`
	NextCharge string `json:"next_charge"`
}

// publicServer 是访客能看到的全部节点字段（设计 45.3）。离线节点只有名称、国家、状态与流量比例。
type publicServer struct {
	Name       string   `json:"name"`
	Country    string   `json:"country"`
	Status     string   `json:"status"` // online / unknown / offline
	CPU        *float64 `json:"cpu,omitempty"`
	Memory     *float64 `json:"memory,omitempty"`
	Disk       *float64 `json:"disk,omitempty"`
	RxSpeed    *uint64  `json:"rx_speed,omitempty"`
	TxSpeed    *uint64  `json:"tx_speed,omitempty"`
	Uptime     *uint64  `json:"uptime,omitempty"`
	TrafficPct *float64 `json:"traffic_pct,omitempty"` // 只有比例，不含流量上限；不限流量时省略
}

// statusPageCache 缓存公开接口的结果，访问量再大也只是每 5 秒组装一次。
type statusPageCache struct {
	mu     sync.Mutex
	at     time.Time
	items  []publicServer
	subsAt time.Time
	subs   []publicSubscription
}

func (c *statusPageCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at, c.items, c.subsAt, c.subs = time.Time{}, nil, time.Time{}, nil
}

// 名称中要遮盖的内容，先匹配长的，避免 IP 被拆开。
var publicNameMasks = []*regexp.Regexp{
	regexp.MustCompile(`[^\s@]+@[^\s@]+`),                           // 邮箱
	regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`),               // IPv4
	regexp.MustCompile(`(?:[0-9a-fA-F]{0,4}:){2,}[0-9a-fA-F]{0,4}`), // IPv6
	regexp.MustCompile(`\+?\d[\d\- ]{5,}\d`),                        // 7 位以上的号码
}

// maskPublicName 遮盖名称中的 IP、邮箱与电话号码。
func maskPublicName(name string) string {
	for _, re := range publicNameMasks {
		name = re.ReplaceAllString(name, "***")
	}
	return name
}

// publicDiskUsage 是全部磁盘的合计使用率，口径与 Web 的 diskSummary 一致：同一设备只算一次，有可用空间时按 df 计算。
func publicDiskUsage(v serverView) *float64 {
	seen := map[string]bool{}
	var total, used, avail uint64
	hasAvail := true
	for _, d := range v.Latest.Disk {
		key := cmp.Or(d.Device, d.Mount)
		if seen[key] {
			continue
		}
		seen[key] = true
		total += d.Total
		used += d.Used
		if d.Available == 0 {
			hasAvail = false
		}
		avail += d.Available
	}
	denom := total
	if hasAvail {
		denom = used + avail
	}
	if denom == 0 {
		return nil
	}
	p := round1(float64(used) / float64(denom) * 100)
	return &p
}

// publicView 从节点数据中挑出可以公开的字段。
func publicView(v serverView) publicServer {
	p := publicServer{Name: maskPublicName(v.Name), Country: v.Country, Status: v.Status}
	if v.Traffic.Limit > 0 {
		pct := round1(float64(v.Traffic.Used) / float64(v.Traffic.Limit) * 100)
		p.TrafficPct = &pct
	}
	if v.Latest == nil || v.Status == "offline" {
		return p // 离线时的最后一份数据已经过时，不显示
	}
	cpu, mem := round1(v.Latest.CPU.Usage), round1(v.Latest.Memory.Usage)
	var rx, tx uint64
	for _, n := range v.Latest.Network {
		rx += n.RxSpeed
		tx += n.TxSpeed
	}
	up := v.Latest.System.Uptime
	p.CPU, p.Memory, p.Disk, p.RxSpeed, p.TxSpeed, p.Uptime = &cpu, &mem, publicDiskUsage(v), &rx, &tx, &up
	return p
}

// publicServers 返回公开的节点：缓存有效时直接用；顺序与面板的自定义顺序一致。
func (s *Server) publicServers(now time.Time) ([]publicServer, time.Time, error) {
	c := &s.statusPage
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && now.Sub(c.at) < statusPageTTL {
		return c.items, c.at, nil
	}
	ids, err := s.store.PublicServerIDs()
	if err != nil {
		return nil, time.Time{}, err
	}
	rows, err := s.store.ListServers()
	if err != nil {
		return nil, time.Time{}, err
	}
	rows = slices.DeleteFunc(rows, func(row ServerRow) bool {
		return !slices.Contains(ids, row.ID) || row.EnrollState == enrollPending // 待安装的节点没有数据
	})
	order := func(r ServerRow) int { // 0 表示未排，放在最后
		if r.SortOrder == 0 {
			return math.MaxInt
		}
		return r.SortOrder
	}
	slices.SortStableFunc(rows, func(a, b ServerRow) int { return cmp.Compare(order(a), order(b)) })
	items := make([]publicServer, 0, len(rows))
	for _, row := range rows {
		v, err := s.viewOf(row, now)
		if err != nil {
			return nil, time.Time{}, err
		}
		items = append(items, publicView(v))
	}
	c.at, c.items = now, items
	return items, now, nil
}

// publicSubscriptions 返回公开的订阅：未结束的，按下次扣费日排序。
func (s *Server) publicSubscriptions(now time.Time) ([]publicSubscription, error) {
	c := &s.statusPage
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.subsAt.IsZero() && now.Sub(c.subsAt) < statusPageTTL {
		return c.subs, nil
	}
	list, err := s.store.ListSubscriptions()
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i] = withSchedule(list[i], now)
	}
	sortSubscriptions(list)
	out := make([]publicSubscription, 0, len(list))
	for _, v := range list {
		if v.Ended {
			continue
		}
		out = append(out, publicSubscription{Name: maskPublicName(v.Name), Brand: v.Brand, Category: v.Category,
			PriceCents: v.PriceCents, Currency: v.Currency, Cycle: v.Cycle, NextCharge: v.NextCharge})
	}
	c.subsAt, c.subs = now, out
	return out, nil
}

// publicGate 是公开接口的入口：限流，读设置；未开启时写出 404 并返回 false。
func (s *Server) publicGate(w http.ResponseWriter, r *http.Request) (StatusPageSetting, bool) {
	var st StatusPageSetting
	if ok, wait := s.statusLimit.allow(clientIP(r)); !ok {
		s.writeError(w, r, &APIError{Code: CodeRateLimited, RetryAfter: wait})
		return st, false
	}
	if _, err := s.store.GetSetting(settingStatusPage, &st); err != nil {
		s.writeError(w, r, internalError(err))
		return st, false
	}
	if !st.Enabled {
		s.writeError(w, r, errNotFound)
		return st, false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	return st, true
}

// handlePublicServers：GET /api/v1/public/servers，无需凭证（设计 45.3）。未开启时返回 404。
func (s *Server) handlePublicServers(w http.ResponseWriter, r *http.Request) {
	st, ok := s.publicGate(w, r)
	if !ok {
		return
	}
	items, at, err := s.publicServers(time.Now())
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeList(w, items, "", map[string]any{"updated_at": at.Unix(), "subscriptions": st.Subscriptions})
}

// handlePublicSubscriptions：GET /api/v1/public/subscriptions，无需凭证（设计 45.3）。展示页或订阅未开启时返回 404。
func (s *Server) handlePublicSubscriptions(w http.ResponseWriter, r *http.Request) {
	st, ok := s.publicGate(w, r)
	if !ok {
		return
	}
	if !st.Subscriptions {
		s.writeError(w, r, errNotFound)
		return
	}
	items, err := s.publicSubscriptions(time.Now())
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeList(w, items, "", nil)
}

func (s *Server) statusPageView() (statusPageView, error) {
	var v statusPageView
	var st StatusPageSetting
	if _, err := s.store.GetSetting(settingStatusPage, &st); err != nil {
		return v, err
	}
	ids, err := s.store.PublicServerIDs()
	if err != nil {
		return v, err
	}
	return statusPageView{Enabled: st.Enabled, Subscriptions: st.Subscriptions, ServerIDs: ids}, nil
}

// handleGetStatusPage：GET /api/v1/settings/status-page，admin。
func (s *Server) handleGetStatusPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.statusPageView()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, v)
}

// handlePutStatusPage：PUT /api/v1/settings/status-page，admin。server_ids 是要公开的全部节点。
func (s *Server) handlePutStatusPage(w http.ResponseWriter, r *http.Request) {
	var in statusPageView
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	rows, err := s.store.ListServers()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	for _, id := range in.ServerIDs {
		if !slices.ContainsFunc(rows, func(row ServerRow) bool { return row.ID == id }) {
			s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "server_ids", Message: "节点不存在"}}})
			return
		}
	}
	now := time.Now()
	if err := s.store.SetPublicServers(in.ServerIDs); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if err := s.store.PutSetting(settingStatusPage, StatusPageSetting{Enabled: in.Enabled, Subscriptions: in.Subscriptions}, now); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.statusPage.clear()
	s.audit(r, AuditEntry{ActorType: "admin", Action: "setting.update", Success: true,
		Details: map[string]any{"key": settingStatusPage, "enabled": in.Enabled, "subscriptions": in.Subscriptions, "servers": len(in.ServerIDs)}})
	v, err := s.statusPageView()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, v)
}
