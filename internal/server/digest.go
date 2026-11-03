package server

// 定期报告（修订第 277 条，设计 16.8）：每周一或每月 1 日在设定时间，把一段时间的概况发到全部已启用的通知渠道与 App
// （授权范围为全部节点的设备）。不受渠道最低级别与免打扰限制（时间由用户设定）；告警总开关关闭时与其他通知一样不发送。
//
//	节点    共几台、在线 / 离线（离线的列出名称）
//	告警    期间触发次数（严重几次）、触发最多的节点
//	离线    期间离线累计最长的节点（按离线告警计算）
//	流量    本计费周期用量最高的节点（设了流量上限的，≥ 80% 的都列出）
//	到期    之后 7 天（周报）/ 30 天（月报）内到期的节点
//	订阅    之后 7 / 30 天内将扣费的订阅
//	云账户  各账户本月费用（有预估时附上）
//
// 只有本面板已有的数据，不访问外部服务；内容与其他通知一样经各渠道发出（App 推送端到端加密，设计 30）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// NotifyDigest 是定期报告的通知种类（修订第 277 条）。
const NotifyDigest = "digest"

const (
	settingDigest  = "digest"
	digestTopN     = 3  // 每一项最多列出的节点数
	digestMaxLines = 40 // 正文最多行数
)

// Digest 是定期报告的设置。Time 为 HH:MM，按 Timezone（IANA 名称，空为面板本地时区）计算。
type Digest struct {
	Enabled  bool   `json:"enabled"`
	Period   string `json:"period"` // weekly：每周一，报告前 7 天；monthly：每月 1 日，报告上个月
	Time     string `json:"time"`
	Timezone string `json:"timezone"`
}

func defaultDigest() Digest { return Digest{Period: "weekly", Time: "09:00"} }

func (d *Digest) validate() []FieldError {
	var errs []FieldError
	d.Time, d.Timezone = strings.TrimSpace(d.Time), strings.TrimSpace(d.Timezone)
	if d.Period != "weekly" && d.Period != "monthly" {
		errs = append(errs, FieldError{Field: "period", Message: "应为 weekly 或 monthly"})
	}
	if _, ok := parseHM(d.Time); !ok {
		errs = append(errs, FieldError{Field: "time", Message: "时间格式应为 HH:MM"})
	}
	if d.Timezone != "" {
		if _, err := time.LoadLocation(d.Timezone); err != nil || d.Timezone == "Local" {
			errs = append(errs, FieldError{Field: "timezone", Message: "时区无效，应为 IANA 名称，如 Asia/Shanghai"})
		}
	}
	return errs
}

func (d Digest) location() *time.Location {
	if d.Timezone != "" {
		if loc, err := time.LoadLocation(d.Timezone); err == nil {
			return loc
		}
	}
	return time.Local
}

// span 返回 now 所在的报告区间 [from, to) 与去重键：周报为今天 0 点之前的 7 天，月报为上个月。
func (d Digest) span(now time.Time) (from, to time.Time, key string) {
	t := now.In(d.location())
	today := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	if d.Period == "monthly" {
		to = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
		from = to.AddDate(0, -1, 0)
		return from, to, "digest:monthly:" + from.Format("2006-01")
	}
	y, w := today.ISOWeek()
	return today.AddDate(0, 0, -7), today, fmt.Sprintf("digest:weekly:%d-W%02d", y, w)
}

// due 判断现在是否该发送：周一（或 1 日）且已到设定时间。当天面板停机错过时，当天之内启动后补发；过了当天不再补发。
func (d Digest) due(now time.Time) bool {
	if !d.Enabled {
		return false
	}
	t := now.In(d.location())
	if d.Period == "monthly" && t.Day() != 1 || d.Period != "monthly" && t.Weekday() != time.Monday {
		return false
	}
	m, ok := parseHM(d.Time)
	return ok && t.Hour()*60+t.Minute() >= m
}

func (s *Server) digestCfg() Digest {
	d := defaultDigest()
	if _, err := s.store.GetSetting(settingDigest, &d); err != nil {
		s.log.Error("read digest setting failed", "component", "digest", "err", err)
	}
	return d
}

// digestLoop 每 5 分钟检查一次是否该发送。
func (s *Server) digestLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Minute):
		}
		s.checkDigest(time.Now())
	}
}

// checkDigest 到时间时发送一次（按区间去重，面板重启不会重复发送）。
func (s *Server) checkDigest(now time.Time) {
	d := s.digestCfg()
	if !d.due(now) {
		return
	}
	_, _, key := d.span(now)
	first, err := s.store.MarkReminder(key, now)
	if err != nil {
		s.log.Error("record digest failed", "component", "digest", "err", err)
		return
	}
	if !first {
		return
	}
	m, err := s.buildDigest(d, now)
	if err != nil {
		s.log.Error("build digest failed", "component", "digest", "err", err)
		return
	}
	s.log.Info("digest sent", "component", "digest", "key", key)
	s.notify.dispatch(m)
}

// buildDigest 汇总报告区间内的概况。
func (s *Server) buildDigest(d Digest, now time.Time) (notifyMessage, error) {
	from, to, _ := d.span(now)
	label := "周报"
	ahead := 7
	if d.Period == "monthly" {
		label, ahead = "月报", 30
	}
	m := notifyMessage{Kind: NotifyDigest, StartedAt: from, ResolvedAt: to,
		Message: fmt.Sprintf("📊 VPS Monitor %s（%s～%s）", label, from.Format("01-02"), to.Add(-time.Second).Format("01-02"))}
	if s.publicURL != "" {
		m.Link = s.publicURL + "/"
	}
	rows, err := s.store.ListServers()
	if err != nil {
		return m, err
	}
	rows = slices.DeleteFunc(rows, func(r ServerRow) bool { return r.EnrollState == enrollPending })
	names := map[int64]string{}
	var lines []string

	// 节点与流量
	var offline []string
	type usage struct {
		name, text string
		pct        float64
	}
	var traffic []usage
	var reboot, security []string // 系统更新（修订第 278 条）
	for _, row := range rows {
		names[row.ID] = row.Name
		v, err := s.viewOf(row, now)
		if err != nil {
			return m, err
		}
		if v.Status == "offline" {
			offline = append(offline, row.Name)
		}
		if v.Latest != nil && v.Latest.Updates != nil {
			if u := v.Latest.Updates; u.RebootRequired {
				reboot = append(reboot, row.Name)
			}
			if u := v.Latest.Updates; u.Security != nil && *u.Security > 0 {
				security = append(security, fmt.Sprintf("%s %d", row.Name, *u.Security))
			}
		}
		if t := v.Traffic; t.Limit > 0 {
			pct := float64(t.Used) / float64(t.Limit) * 100
			traffic = append(traffic, usage{row.Name, fmt.Sprintf("%s %.0f%%（%s / %s，%s 重置）", row.Name, pct,
				fmtTrafficUnit(int64(t.Used), t.Unit), fmtTrafficUnit(t.Limit, t.Unit), digestDate(t.CycleEnd)), pct})
		}
	}
	node := fmt.Sprintf("节点：共 %d 台，在线 %d", len(rows), len(rows)-len(offline))
	if len(offline) > 0 {
		node += fmt.Sprintf("，离线 %d（%s）", len(offline), namesBrief(offline))
	}
	lines = append(lines, node)
	if len(reboot) > 0 || len(security) > 0 {
		var parts []string
		if len(reboot) > 0 {
			parts = append(parts, fmt.Sprintf("需要重启 %d 台（%s）", len(reboot), namesBrief(reboot)))
		}
		if len(security) > 0 {
			parts = append(parts, fmt.Sprintf("有安全更新 %d 台（%s）", len(security), namesBrief(security)))
		}
		lines = append(lines, "系统："+strings.Join(parts, "，"))
	}

	// 告警与离线时长
	evs, err := s.store.AlertEventsBetween(from.Unix(), to.Unix())
	if err != nil {
		return m, err
	}
	fired, critical := 0, 0
	perNode := map[int64]int{}
	down := map[int64]int64{}
	for _, e := range evs {
		if e.FiredAt >= from.Unix() && e.FiredAt < to.Unix() {
			fired++
			perNode[e.ServerID]++
			if e.Severity == SeverityCritical {
				critical++
			}
		}
		if e.Type == AlertOffline {
			end := to.Unix()
			if e.ResolvedAt > 0 && e.ResolvedAt < end {
				end = e.ResolvedAt
			}
			if dur := end - max(e.StartedAt, from.Unix()); dur > 0 {
				down[e.ServerID] += dur
			}
		}
	}
	if fired == 0 {
		lines = append(lines, "告警：没有触发")
	} else {
		line := fmt.Sprintf("告警：触发 %d 次", fired)
		if critical > 0 {
			line += fmt.Sprintf("（严重 %d 次）", critical)
		}
		lines = append(lines, line+"，最多："+topCounts(perNode, names, "%s %d 次"))
	}
	if len(down) > 0 {
		var parts []string
		for _, id := range topKeys(down) {
			parts = append(parts, names[id]+" "+fmtSpan(time.Duration(down[id])*time.Second))
		}
		lines = append(lines, "离线累计："+strings.Join(parts, "、"))
	}

	// 流量：≥ 80% 的全部列出，否则列出最高的几台
	if len(traffic) > 0 {
		slices.SortFunc(traffic, func(a, b usage) int {
			if a.pct > b.pct {
				return -1
			}
			if a.pct < b.pct {
				return 1
			}
			return strings.Compare(a.name, b.name)
		})
		n := digestTopN
		for n < len(traffic) && traffic[n].pct >= 80 {
			n++
		}
		lines = append(lines, "本周期流量用量最高：")
		for _, u := range traffic[:min(n, len(traffic))] {
			lines = append(lines, "  "+u.text)
		}
	}

	// 到期的节点
	var expiring []string
	for _, row := range rows {
		if row.ExpireDate == "" {
			continue
		}
		loc := trafficLocation(row)
		exp, err := time.ParseInLocation("2006-01-02", row.ExpireDate, loc)
		if err != nil {
			continue
		}
		if days := calendarDays(now.In(loc), exp); days <= ahead {
			expiring = append(expiring, fmt.Sprintf("%s %s", row.Name, digestDays(days, row.ExpireDate)))
		}
	}
	if len(expiring) > 0 {
		lines = append(lines, fmt.Sprintf("%d 天内到期的节点：%s", ahead, strings.Join(expiring, "、")))
	}

	// 将扣费的订阅
	subs, err := s.store.ListSubscriptions()
	if err != nil {
		return m, err
	}
	var charges []string
	today := now.In(d.location())
	for _, x := range subs {
		x = withSchedule(x, now)
		if x.Ended || x.NextCharge == "" {
			continue
		}
		at, err := time.ParseInLocation("2006-01-02", x.NextCharge, today.Location())
		if err != nil {
			continue
		}
		if days := calendarDays(today, at); days >= 0 && days <= ahead {
			charges = append(charges, fmt.Sprintf("%s %s（%s）", x.Name, fmtCents(x.PriceCents, x.Currency), digestDate(x.NextCharge)))
		}
	}
	if len(charges) > 0 {
		lines = append(lines, fmt.Sprintf("%d 天内扣费的订阅 %d 笔：%s", ahead, len(charges), strings.Join(charges, "、")))
	}

	// 云账户本月费用
	accts, err := s.store.ListCloudAccounts()
	if err != nil {
		return m, err
	}
	month := now.Format("2006-01")
	var costs []string
	for _, a := range accts {
		if !a.Enabled || !a.SyncCost {
			continue
		}
		cs, err := s.store.CloudCosts(a.ID, 1)
		if err != nil || len(cs) == 0 || cs[0].Period != month {
			continue
		}
		c := a.Name + " " + fmtCents(cs[0].AmountCents, cs[0].Currency)
		if cs[0].ForecastCents != nil {
			c += "（预估 " + fmtCents(*cs[0].ForecastCents, cs[0].Currency) + "）"
		}
		costs = append(costs, c)
	}
	if len(costs) > 0 {
		lines = append(lines, "云账户本月费用："+strings.Join(costs, "、"))
	}
	if len(lines) > digestMaxLines {
		lines = append(lines[:digestMaxLines], "…")
	}
	m.Lines = lines
	return m, nil
}

// topKeys 返回值最大的前 digestTopN 个键（值相同时按键）。
func topKeys(m map[int64]int64) []int64 {
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b int64) int {
		if m[a] != m[b] {
			if m[a] > m[b] {
				return -1
			}
			return 1
		}
		return int(a - b)
	})
	return keys[:min(digestTopN, len(keys))]
}

func topCounts(m map[int64]int, names map[int64]string, format string) string {
	wide := make(map[int64]int64, len(m))
	for k, v := range m {
		wide[k] = int64(v)
	}
	var parts []string
	for _, id := range topKeys(wide) {
		parts = append(parts, fmt.Sprintf(format, orText(names[id], "已删除的节点"), m[id]))
	}
	return strings.Join(parts, "、")
}

// digestDate：2026-11-05 → 11-05
func digestDate(s string) string {
	if len(s) == 10 {
		return s[5:]
	}
	return s
}

func digestDays(days int, date string) string {
	switch {
	case days < 0:
		return fmt.Sprintf("已过期 %d 天", -days)
	case days == 0:
		return "今天到期"
	}
	return fmt.Sprintf("%s（%d 天后）", digestDate(date), days)
}

// fmtCents：1599 USD → 15.99 USD
func fmtCents(cents int64, currency string) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s%d.%02d %s", sign, cents/100, cents%100, currency)
}

// AlertEventsBetween 返回与 [from, to) 有重叠的告警事件（触发于 to 之前、未恢复或恢复于 from 之后）。
func (s *Store) AlertEventsBetween(from, to int64) ([]AlertEvent, error) {
	rows, err := s.DB.Query(`SELECT server_id, type, severity, started_at, fired_at, resolved_at FROM alert_events
		WHERE fired_at < ? AND (resolved_at = 0 OR resolved_at >= ?)`, to, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertEvent
	for rows.Next() {
		var e AlertEvent
		if err := rows.Scan(&e.ServerID, &e.Type, &e.Severity, &e.StartedAt, &e.FiredAt, &e.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// digestView 是 GET /settings/digest 的响应：设置与按当前时间生成的预览。
type digestView struct {
	Digest
	EffectiveTimezone string `json:"effective_timezone"`
	Preview           string `json:"preview"`
}

func (s *Server) digestView(d Digest, now time.Time) (digestView, error) {
	m, err := s.buildDigest(d, now)
	if err != nil {
		return digestView{}, err
	}
	return digestView{Digest: d, EffectiveTimezone: effectiveTZ(d.Timezone, now), Preview: m.text(now)}, nil
}

// handleGetDigest：GET /api/v1/settings/digest，admin（修订第 277 条）。
func (s *Server) handleGetDigest(w http.ResponseWriter, r *http.Request) {
	v, err := s.digestView(s.digestCfg(), time.Now())
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, v)
}

// handlePutDigest：PUT /api/v1/settings/digest，admin。修改记入操作日志 setting.update。
func (s *Server) handlePutDigest(w http.ResponseWriter, r *http.Request) {
	var d Digest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	if errs := d.validate(); len(errs) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: errs})
		return
	}
	now := time.Now()
	if err := s.store.PutSetting(settingDigest, d, now); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "setting.update", Success: true,
		Details: map[string]any{"key": settingDigest, "enabled": d.Enabled, "period": d.Period, "time": d.Time, "timezone": d.Timezone}})
	v, err := s.digestView(d, now)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, v)
}

// handleSendDigest：POST /api/v1/settings/digest/send，admin。按当前设置立即发送一次（不影响定时发送），成功 204。
func (s *Server) handleSendDigest(w http.ResponseWriter, r *http.Request) {
	if !s.alertsEnabled() {
		s.writeError(w, r, errorf(CodeConflict, "告警已关闭，不发送任何通知"))
		return
	}
	now := time.Now()
	m, err := s.buildDigest(s.digestCfg(), now)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.notify.dispatch(m)
	s.audit(r, AuditEntry{ActorType: "admin", Action: "digest.send", Success: true})
	w.WriteHeader(http.StatusNoContent)
}
