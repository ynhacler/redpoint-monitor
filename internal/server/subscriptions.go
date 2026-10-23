package server

// 订阅（设计 47，修订第 189 条）：记录个人订阅服务（影音、工具、云服务等）的价格、周期、试用与起止日期，
// 面板算出下次扣费日，并在扣费前按设置提醒。只给管理员；数据只存在本面板。
// 扣费日规则（与 web/src/subscriptions.ts 一致）：首次扣费为开始日期加试用天数，之后按周期递推
// （按月类周期以首次扣费日为锚点，小月取月末）；结束日期当天及之后不再扣费；一次性只扣首次。

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const maxSubscriptions = 500

// SubPhone 是 SIM 电话卡的一个号码：区号与号码（只含数字）。
type SubPhone struct {
	CC     string `json:"cc"`
	Number string `json:"number"`
}

const maxSubPhones = 10

// SubVPS 是 VPS 的地址与配置（类别为 vps 时，修订第 198 条）；数值为 0 表示没有填写。
type SubVPS struct {
	IPs           []string `json:"ips"`
	CPU           int      `json:"cpu"`
	MemoryGB      float64  `json:"memory_gb"`
	DiskGB        int      `json:"disk_gb"`
	BandwidthMbps int      `json:"bandwidth_mbps"`
	TrafficGB     int      `json:"traffic_gb"`
	Location      string   `json:"location"`
}

const maxSubIPs = 16

var phoneCC = regexp.MustCompile(`^\+[0-9]{1,4}$`)

// Subscription 是一行 subscriptions（api/openapi.yaml Subscription）。
type Subscription struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Brand       string     `json:"brand"`
	Category    string     `json:"category"`
	PriceCents  int64      `json:"price_cents"`
	Currency    string     `json:"currency"`
	Cycle       string     `json:"cycle"`
	TrialDays   int        `json:"trial_days"`
	StartDate   string     `json:"start_date"`
	EndDate     string     `json:"end_date"`
	RemindDays  int        `json:"remind_days"`
	Note        string     `json:"note"`
	Phones      []SubPhone `json:"phones"`       // SIM 电话卡的号码（修订第 189 条）
	VPS         SubVPS     `json:"vps"`          // VPS 的地址与配置（修订第 198 条）
	NextCharge  string     `json:"next_charge"`  // 计算得到，不入库
	Ended       bool       `json:"ended"`        // 计算得到，不入库
	LogoVersion int64      `json:"logo_version"` // 上传图标的时间，0 表示没有（修订第 189 条）
	CreatedAt   int64      `json:"created_at"`
	UpdatedAt   int64      `json:"updated_at"`
}

var (
	subCategories = []string{"video", "tools", "reading", "social", "health", "game", "cloud", "sim", "vps", "other"}
	subCycles     = []string{"weekly", "monthly", "quarterly", "semiannually", "annually", "once"}
	subRemind     = []int{0, 1, 2, 3, 7}
	currencyCode  = regexp.MustCompile(`^[A-Z]{3}$`)
	errNoSub      = errorf(CodeNotFound, "订阅不存在或已删除")
)

// cycleMonths 返回按月类周期的月数；weekly 与 once 返回 0。
func cycleMonths(cycle string) int {
	return map[string]int{"monthly": 1, "quarterly": 3, "semiannually": 6, "annually": 12}[cycle]
}

// addMonthsClamped 在锚点日期上加 n 个月，目标月没有这一天时取月末（1 月 31 日 + 1 个月 = 2 月 28 / 29 日）。
func addMonthsClamped(anchor time.Time, n int) time.Time {
	y, m, d := anchor.Date()
	first := time.Date(y, m+time.Month(n), 1, 0, 0, 0, 0, anchor.Location())
	last := first.AddDate(0, 1, -1).Day()
	return time.Date(first.Year(), first.Month(), min(d, last), 0, 0, 0, 0, anchor.Location())
}

// nextCharge 返回 today（含）之后的第一个扣费日；没有时（已结束、一次性已扣过）返回零值。
func nextCharge(sub Subscription, today time.Time) time.Time {
	start, err := time.ParseInLocation("2006-01-02", sub.StartDate, today.Location())
	if err != nil {
		return time.Time{}
	}
	var end time.Time
	if sub.EndDate != "" {
		if end, err = time.ParseInLocation("2006-01-02", sub.EndDate, today.Location()); err != nil {
			return time.Time{}
		}
	}
	first := start.AddDate(0, 0, sub.TrialDays)
	ok := func(t time.Time) bool { return end.IsZero() || t.Before(end) }
	if !first.Before(today) {
		if ok(first) {
			return first
		}
		return time.Time{}
	}
	switch sub.Cycle {
	case "once":
		return time.Time{}
	case "weekly":
		weeks := int(today.Sub(first).Hours()/24+6) / 7
		t := first.AddDate(0, 0, 7*weeks)
		if t.Before(today) {
			t = t.AddDate(0, 0, 7)
		}
		if ok(t) {
			return t
		}
		return time.Time{}
	}
	step := cycleMonths(sub.Cycle)
	if step == 0 {
		return time.Time{}
	}
	months := (today.Year()-first.Year())*12 + int(today.Month()-first.Month())
	k := max(0, months/step-1)
	for {
		t := addMonthsClamped(first, k*step)
		if !t.Before(today) {
			if ok(t) {
				return t
			}
			return time.Time{}
		}
		k++
	}
}

// withSchedule 填写下次扣费日与是否已结束（面板本地时区的“今天”）。
func withSchedule(sub Subscription, now time.Time) Subscription {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	sub.Ended = sub.EndDate != "" && sub.EndDate <= today.Format("2006-01-02")
	if t := nextCharge(sub, today); !t.IsZero() {
		sub.NextCharge = t.Format("2006-01-02")
	}
	return sub
}

// ---- 存储 ----

const subCols = `s.id, s.name, s.brand, s.category, s.price_cents, s.currency, s.cycle, s.trial_days, s.start_date, s.end_date,
	s.remind_days, s.note, s.phones, s.vps, s.created_at, s.updated_at, COALESCE(l.updated_at, 0)
	FROM subscriptions s LEFT JOIN subscription_logos l ON l.subscription_id = s.id`

func scanSub(sc interface{ Scan(...any) error }) (Subscription, error) {
	var x Subscription
	var phones, vps string
	err := sc.Scan(&x.ID, &x.Name, &x.Brand, &x.Category, &x.PriceCents, &x.Currency, &x.Cycle, &x.TrialDays, &x.StartDate,
		&x.EndDate, &x.RemindDays, &x.Note, &phones, &vps, &x.CreatedAt, &x.UpdatedAt, &x.LogoVersion)
	if err == nil {
		_ = json.Unmarshal([]byte(phones), &x.Phones)
		_ = json.Unmarshal([]byte(vps), &x.VPS)
	}
	if x.Phones == nil {
		x.Phones = []SubPhone{}
	}
	if x.VPS.IPs == nil {
		x.VPS.IPs = []string{}
	}
	return x, err
}

// ListSubscriptions 返回全部订阅（按 id）。
func (s *Store) ListSubscriptions() ([]Subscription, error) {
	rows, err := s.DB.Query(`SELECT ` + subCols + ` ORDER BY s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subscription{}
	for rows.Next() {
		x, err := scanSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// GetSubscription 读取一个订阅。
func (s *Store) GetSubscription(id int64) (Subscription, error) {
	x, err := scanSub(s.DB.QueryRow(`SELECT `+subCols+` WHERE s.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return x, errNoSub
	}
	return x, err
}

// SaveSubscription 新增（ID 为 0）或整体替换一个订阅。
func (s *Store) SaveSubscription(x *Subscription, now time.Time) error {
	if x.Phones == nil {
		x.Phones = []SubPhone{}
	}
	if x.VPS.IPs == nil {
		x.VPS.IPs = []string{}
	}
	phones, err := json.Marshal(x.Phones)
	if err != nil {
		return err
	}
	vps, err := json.Marshal(x.VPS)
	if err != nil {
		return err
	}
	if x.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO subscriptions (name, brand, category, price_cents, currency, cycle, trial_days, start_date,
			end_date, remind_days, note, phones, vps, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, x.Name, x.Brand, x.Category,
			x.PriceCents, x.Currency, x.Cycle, x.TrialDays, x.StartDate, x.EndDate, x.RemindDays, x.Note, string(phones), string(vps),
			now.Unix(), now.Unix())
		if err != nil {
			return err
		}
		x.ID, _ = res.LastInsertId()
		x.CreatedAt, x.UpdatedAt = now.Unix(), now.Unix()
		return nil
	}
	res, err := s.DB.Exec(`UPDATE subscriptions SET name = ?, brand = ?, category = ?, price_cents = ?, currency = ?, cycle = ?,
		trial_days = ?, start_date = ?, end_date = ?, remind_days = ?, note = ?, phones = ?, vps = ?, updated_at = ? WHERE id = ?`, x.Name,
		x.Brand, x.Category, x.PriceCents, x.Currency, x.Cycle, x.TrialDays, x.StartDate, x.EndDate, x.RemindDays, x.Note, string(phones),
		string(vps), now.Unix(), x.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoSub
	}
	x.UpdatedAt = now.Unix()
	return nil
}

// DeleteSubscription 删除一个订阅。
func (s *Store) DeleteSubscription(id int64) error {
	if _, err := s.DB.Exec(`DELETE FROM subscription_logos WHERE subscription_id = ?`, id); err != nil {
		return err
	}
	res, err := s.DB.Exec(`DELETE FROM subscriptions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoSub
	}
	return nil
}

// ---- 提醒 ----

// subscriptionReminders 在扣费前 remind_days 天内提醒一次（每个扣费日一次，经通知渠道，遵守告警开关与免打扰）。
func (s *Server) subscriptionReminders(now time.Time) {
	list, err := s.store.ListSubscriptions()
	if err != nil {
		return
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	for _, x := range list {
		if x.RemindDays <= 0 {
			continue
		}
		t := nextCharge(x, today)
		if t.IsZero() {
			continue
		}
		days := calendarDays(today, t)
		if days > x.RemindDays {
			continue
		}
		when := fmt.Sprintf("%d 天后（%s）", days, t.Format("1 月 2 日"))
		if days == 0 {
			when = "今天"
		}
		s.remind(fmt.Sprintf("subscription:%d:%s", x.ID, t.Format("2006-01-02")), notifyMessage{ServerName: "订阅“" + x.Name + "”",
			Type: "subscription", Severity: SeverityWarning,
			Message: fmt.Sprintf("将于%s扣费 %s %.2f", when, x.Currency, float64(x.PriceCents)/100)}, now)
	}
}

// ---- 接口（Web 管理员） ----

// validateSubscription 检查并规范化请求体。
func validateSubscription(x *Subscription) []FieldError {
	var fe []FieldError
	x.Name, x.Note, x.Brand = strings.TrimSpace(x.Name), strings.TrimSpace(x.Note), strings.TrimSpace(x.Brand)
	x.Currency = strings.ToUpper(strings.TrimSpace(x.Currency))
	if n := utf8.RuneCountInString(x.Name); n == 0 || n > 64 {
		fe = append(fe, FieldError{Field: "name", Message: "请填写名称（最多 64 个字符）"})
	}
	if utf8.RuneCountInString(x.Brand) > 32 {
		fe = append(fe, FieldError{Field: "brand", Message: "图标键最多 32 个字符"})
	}
	if !slices.Contains(subCategories, x.Category) {
		fe = append(fe, FieldError{Field: "category", Message: "请选择类别"})
	}
	if x.PriceCents < 0 || x.PriceCents > 100000000 {
		fe = append(fe, FieldError{Field: "price_cents", Message: "价格应在 0 ～ 1,000,000 之间"})
	}
	if !currencyCode.MatchString(x.Currency) {
		fe = append(fe, FieldError{Field: "currency", Message: "币种为 3 位大写字母，如 CNY、USD"})
	}
	if !slices.Contains(subCycles, x.Cycle) {
		fe = append(fe, FieldError{Field: "cycle", Message: "请选择订阅周期"})
	}
	if x.TrialDays < 0 || x.TrialDays > 366 {
		fe = append(fe, FieldError{Field: "trial_days", Message: "试用天数应在 0 ～ 366 之间"})
	}
	start, err := time.Parse("2006-01-02", x.StartDate)
	if err != nil {
		fe = append(fe, FieldError{Field: "start_date", Message: "请选择开始日期"})
	}
	if x.EndDate != "" {
		if end, err2 := time.Parse("2006-01-02", x.EndDate); err2 != nil {
			fe = append(fe, FieldError{Field: "end_date", Message: "结束日期格式不正确"})
		} else if err == nil && end.Before(start) {
			fe = append(fe, FieldError{Field: "end_date", Message: "结束日期不能早于开始日期"})
		}
	}
	if !slices.Contains(subRemind, x.RemindDays) {
		fe = append(fe, FieldError{Field: "remind_days", Message: "提醒可选：不提醒，或提前 1 / 2 / 3 / 7 天"})
	}
	if utf8.RuneCountInString(x.Note) > 200 {
		fe = append(fe, FieldError{Field: "note", Message: "描述最多 200 个字符"})
	}
	// 号码：去掉空行，号码只保留数字（可粘贴“650-555 1234”），区号形如 +1、+86
	phones := []SubPhone{}
	for i, p := range x.Phones {
		p.CC = strings.TrimSpace(p.CC)
		digits := strings.Map(func(r rune) rune {
			switch {
			case r >= '0' && r <= '9':
				return r
			case r == ' ' || r == '-' || r == '(' || r == ')' || r == '.':
				return -1
			}
			return 'x'
		}, strings.TrimSpace(p.Number))
		if digits == "" {
			continue
		}
		field := fmt.Sprintf("phones[%d]", i)
		switch {
		case !phoneCC.MatchString(p.CC):
			fe = append(fe, FieldError{Field: field + ".cc", Message: "区号形如 +1、+86"})
		case strings.Contains(digits, "x") || len(digits) < 4 || len(digits) > 15:
			fe = append(fe, FieldError{Field: field + ".number", Message: "号码为 4～15 位数字"})
		}
		phones = append(phones, SubPhone{CC: p.CC, Number: digits})
	}
	if len(phones) > maxSubPhones {
		fe = append(fe, FieldError{Field: "phones", Message: fmt.Sprintf("最多 %d 个号码", maxSubPhones)})
	}
	x.Phones = phones
	return append(fe, validateSubVPS(x)...)
}

// validateSubVPS 校验 VPS 的地址与配置（修订第 198 条）：去掉空行与重复地址，地址须为 IPv4 / IPv6；其他类别清空。
func validateSubVPS(x *Subscription) []FieldError {
	if x.Category != "vps" {
		x.VPS = SubVPS{IPs: []string{}}
		return nil
	}
	var fe []FieldError
	v := &x.VPS
	ips := []string{}
	for i, raw := range v.IPs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		addr, err := netip.ParseAddr(raw)
		if err != nil || addr.Zone() != "" {
			fe = append(fe, FieldError{Field: fmt.Sprintf("vps.ips[%d]", i), Message: "请输入 IPv4 或 IPv6 地址"})
			continue
		}
		if s := addr.Unmap().String(); !slices.Contains(ips, s) {
			ips = append(ips, s)
		}
	}
	if len(ips) > maxSubIPs {
		fe = append(fe, FieldError{Field: "vps.ips", Message: fmt.Sprintf("最多 %d 个地址", maxSubIPs)})
	}
	v.IPs = ips
	for _, c := range []struct {
		field string
		bad   bool
		msg   string
	}{
		{"vps.cpu", v.CPU < 0 || v.CPU > 1024, "CPU 为 0～1024 核"},
		{"vps.memory_gb", v.MemoryGB < 0 || v.MemoryGB > 16384, "内存为 0～16384 GB"},
		{"vps.disk_gb", v.DiskGB < 0 || v.DiskGB > 1000000, "硬盘为 0～1000000 GB"},
		{"vps.bandwidth_mbps", v.BandwidthMbps < 0 || v.BandwidthMbps > 1000000, "带宽为 0～1000000 Mbps"},
		{"vps.traffic_gb", v.TrafficGB < 0 || v.TrafficGB > 100000000, "流量为 0～100000000 GB"},
		{"vps.location", utf8.RuneCountInString(v.Location) > 40, "机房最多 40 个字符"},
	} {
		if c.bad {
			fe = append(fe, FieldError{Field: c.field, Message: c.msg})
		}
	}
	v.Location = strings.TrimSpace(v.Location)
	return fe
}

func (s *Server) decodeSubscription(w http.ResponseWriter, r *http.Request) (Subscription, bool) {
	var x Subscription
	var b struct {
		Name       string     `json:"name"`
		Brand      string     `json:"brand"`
		Category   string     `json:"category"`
		PriceCents int64      `json:"price_cents"`
		Currency   string     `json:"currency"`
		Cycle      string     `json:"cycle"`
		TrialDays  int        `json:"trial_days"`
		StartDate  string     `json:"start_date"`
		EndDate    string     `json:"end_date"`
		RemindDays int        `json:"remind_days"`
		Note       string     `json:"note"`
		Phones     []SubPhone `json:"phones"`
		VPS        SubVPS     `json:"vps"`
	}
	if err := decodeJSON(w, r, &b); err != nil {
		s.writeError(w, r, err)
		return x, false
	}
	x = Subscription{Name: b.Name, Brand: b.Brand, Category: b.Category, PriceCents: b.PriceCents, Currency: b.Currency, Cycle: b.Cycle,
		TrialDays: b.TrialDays, StartDate: b.StartDate, EndDate: b.EndDate, RemindDays: b.RemindDays, Note: b.Note, Phones: b.Phones,
		VPS: b.VPS}
	if fe := validateSubscription(&x); fe != nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return x, false
	}
	return x, true
}

// handleSubscriptions：GET /api/v1/subscriptions，admin。按下次扣费日排序，已结束与没有下次扣费的在后（按名称）。
func (s *Server) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSubscriptions()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	now := time.Now()
	for i := range list {
		list[i] = withSchedule(list[i], now)
	}
	sortSubscriptions(list)
	writeList(w, list, "", nil)
}

// sortSubscriptions 按下次扣费日排序，没有下次扣费日的（已结束）排在后面，再按名称（Web 与 App 共用）。
func sortSubscriptions(list []Subscription) {
	slices.SortStableFunc(list, func(a, b Subscription) int {
		switch {
		case a.NextCharge != "" && b.NextCharge != "":
			return strings.Compare(a.NextCharge, b.NextCharge)
		case a.NextCharge != "":
			return -1
		case b.NextCharge != "":
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
}

// handleCreateSubscription：POST /api/v1/subscriptions，admin。
func (s *Server) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	s.createSubscription(w, r, "admin")
}

// createSubscription 校验并保存一条新订阅（Web 管理员与允许记录订阅的 App 设备共用，修订第 217 条）。
func (s *Server) createSubscription(w http.ResponseWriter, r *http.Request, actor string) {
	x, ok := s.decodeSubscription(w, r)
	if !ok {
		return
	}
	if list, err := s.store.ListSubscriptions(); err == nil && len(list) >= maxSubscriptions {
		s.writeError(w, r, errorf(CodeConflict, fmt.Sprintf("最多 %d 个订阅", maxSubscriptions)))
		return
	}
	now := time.Now()
	if err := s.store.SaveSubscription(&x, now); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: actor, ActorID: appActor(r), Action: "subscription.create", Success: true,
		Details: map[string]any{"id": x.ID, "name": x.Name}})
	writeJSONStatus(w, http.StatusCreated, withSchedule(x, now))
}

// handleUpdateSubscription：PUT /api/v1/subscriptions/{id}，admin。整体替换。
func (s *Server) handleUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	old, err := s.store.GetSubscription(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	x, ok := s.decodeSubscription(w, r)
	if !ok {
		return
	}
	x.ID, x.CreatedAt = id, old.CreatedAt
	now := time.Now()
	if err := s.store.SaveSubscription(&x, now); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "subscription.update", Success: true, Details: map[string]any{"id": id, "name": x.Name}})
	writeJSON(w, withSchedule(x, now))
}

// handleDeleteSubscription：DELETE /api/v1/subscriptions/{id}，admin。
func (s *Server) handleDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	x, err := s.store.GetSubscription(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.store.DeleteSubscription(id); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "subscription.delete", Success: true, Details: map[string]any{"id": id, "name": x.Name}})
	w.WriteHeader(http.StatusNoContent)
}

// ---- 上传的图标（修订第 189 条） ----

const (
	maxLogoBytes = 256 << 10
	maxLogoSide  = 512
	minLogoSide  = 16
)

// SaveSubscriptionLogo 保存（替换）订阅的图标。
func (s *Store) SaveSubscriptionLogo(id int64, png []byte, now time.Time) error {
	_, err := s.DB.Exec(`INSERT INTO subscription_logos (subscription_id, png, updated_at) VALUES (?,?,?)
		ON CONFLICT (subscription_id) DO UPDATE SET png = excluded.png, updated_at = excluded.updated_at`, id, png, now.Unix())
	return err
}

// SubscriptionLogo 读取图标；没有时返回 nil。
func (s *Store) SubscriptionLogo(id int64) ([]byte, error) {
	var b []byte
	err := s.DB.QueryRow(`SELECT png FROM subscription_logos WHERE subscription_id = ?`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

// DeleteSubscriptionLogo 删除图标。
func (s *Store) DeleteSubscriptionLogo(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM subscription_logos WHERE subscription_id = ?`, id)
	return err
}

// sanitizeLogo 解码 PNG、检查尺寸并重新编码：只保存像素，丢弃元数据与任何附加内容。【安全】不接受 SVG 等可含脚本的格式。
func sanitizeLogo(b []byte) ([]byte, *APIError) {
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "logo", Message: "图片需为 PNG"}}}
	}
	r := img.Bounds()
	if r.Dx() < minLogoSide || r.Dy() < minLogoSide || r.Dx() > maxLogoSide || r.Dy() > maxLogoSide {
		return nil, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "logo",
			Message: fmt.Sprintf("图片边长需在 %d～%d 像素之间", minLogoSide, maxLogoSide)}}}
	}
	clean := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(clean, clean.Bounds(), img, r.Min, draw.Src)
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, clean); err != nil {
		return nil, internalError(err)
	}
	return buf.Bytes(), nil
}

// handleSubscriptionLogo：GET /api/v1/subscriptions/{id}/logo，admin。
func (s *Server) handleSubscriptionLogo(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	b, err := s.store.SubscriptionLogo(id)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if b == nil {
		s.writeError(w, r, errorf(CodeNotFound, "没有上传图标"))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable") // 地址带版本号 ?v=
	_, _ = w.Write(b)
}

// handlePutSubscriptionLogo：PUT /api/v1/subscriptions/{id}/logo，admin。请求体为 PNG（≤ 256 KB）。
func (s *Server) handlePutSubscriptionLogo(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	x, err := s.store.GetSubscription(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLogoBytes))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	clean, apiErr := sanitizeLogo(raw)
	if apiErr != nil {
		s.writeError(w, r, apiErr)
		return
	}
	now := time.Now()
	if err := s.store.SaveSubscriptionLogo(id, clean, now); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "subscription.logo", Success: true, Details: map[string]any{"id": id, "name": x.Name}})
	x.LogoVersion = now.Unix()
	writeJSON(w, withSchedule(x, now))
}

// handleDeleteSubscriptionLogo：DELETE /api/v1/subscriptions/{id}/logo，admin。
func (s *Server) handleDeleteSubscriptionLogo(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	x, err := s.store.GetSubscription(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.store.DeleteSubscriptionLogo(id); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	x.LogoVersion = 0
	writeJSON(w, withSchedule(x, time.Now()))
}
