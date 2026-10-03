package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 免打扰时段（设计 16.5）：
//
//	严重   默认仍然通知；可改为“汇总”（与警告一样，结束后汇总）
//	警告   暂存，免打扰结束后汇总为一条
//	提示   不发送
//
// 面板自检（panel_down / panel_up）与测试通知不受影响。暂存只在内存中（最多 quietMaxHeld 条，超出只计数），
// 面板在免打扰期间重启会丢失尚未汇总的通知——这些告警仍完整记录在告警事件中。
// 汇总按渠道各自的最低级别过滤：只收严重告警的渠道不会收到警告的汇总。

const (
	settingQuietHours = "quiet_hours"
	quietMaxHeld      = 200
	quietSummaryLines = 10
)

// QuietHours 是免打扰设置。时间为 HH:MM，按 Timezone（IANA 名称，空为面板本地时区）计算；开始晚于结束表示跨午夜。
type QuietHours struct {
	Enabled  bool   `json:"enabled"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Timezone string `json:"timezone"`
	Critical string `json:"critical"` // notify：仍然通知；summary：与警告一起汇总
}

func defaultQuietHours() QuietHours {
	return QuietHours{Start: "23:00", End: "08:00", Critical: "notify"}
}

func parseHM(s string) (int, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

func (q *QuietHours) validate() []FieldError {
	var errs []FieldError
	q.Start, q.End, q.Timezone = strings.TrimSpace(q.Start), strings.TrimSpace(q.End), strings.TrimSpace(q.Timezone)
	a, ok1 := parseHM(q.Start)
	b, ok2 := parseHM(q.End)
	if !ok1 {
		errs = append(errs, FieldError{Field: "start", Message: "时间格式应为 HH:MM"})
	}
	if !ok2 {
		errs = append(errs, FieldError{Field: "end", Message: "时间格式应为 HH:MM"})
	}
	if ok1 && ok2 && a == b {
		errs = append(errs, FieldError{Field: "end", Message: "结束时间不能与开始时间相同"})
	}
	if q.Timezone != "" {
		if _, err := time.LoadLocation(q.Timezone); err != nil || q.Timezone == "Local" {
			errs = append(errs, FieldError{Field: "timezone", Message: "时区无效，应为 IANA 名称，如 Asia/Shanghai"})
		}
	}
	if q.Critical != "notify" && q.Critical != "summary" {
		errs = append(errs, FieldError{Field: "critical", Message: "应为 notify 或 summary"})
	}
	return errs
}

func (q QuietHours) location() *time.Location {
	if q.Timezone != "" {
		if loc, err := time.LoadLocation(q.Timezone); err == nil {
			return loc
		}
	}
	return time.Local
}

// active 判断 now 是否处于免打扰时段（含开始、不含结束；跨午夜时分两段）。
func (q QuietHours) active(now time.Time) bool {
	if !q.Enabled {
		return false
	}
	a, ok1 := parseHM(q.Start)
	b, ok2 := parseHM(q.End)
	if !ok1 || !ok2 || a == b {
		return false
	}
	t := now.In(q.location())
	m := t.Hour()*60 + t.Minute()
	if a < b {
		return m >= a && m < b
	}
	return m >= a || m < b
}

// ---- 设置存储 ----

// GetSetting 读取设置到 v；不存在时返回 false，v 不变。
func (s *Store) GetSetting(key string, v any) (bool, error) {
	var raw string
	err := s.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), v)
}

func (s *Store) PutSetting(key string, v any, now time.Time) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?,?,?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, key, string(b), now.Unix())
	return err
}

// ---- 通知时的判断与汇总 ----

type quietState struct {
	mu      sync.Mutex
	cfg     QuietHours
	held    []notifyMessage
	dropped int // 超过上限未保存的条数
	since   time.Time
}

func newQuietState(st *Store) (*quietState, error) {
	q := &quietState{cfg: defaultQuietHours()}
	if _, err := st.GetSetting(settingQuietHours, &q.cfg); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *quietState) get() QuietHours {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.cfg
}

func (q *quietState) set(c QuietHours) {
	q.mu.Lock()
	q.cfg = c
	q.mu.Unlock()
}

// send 发送一条告警通知，免打扰期间按级别暂存或丢弃（设计 16.5）。降噪之后、notifier 之前调用。
func (s *Server) send(m notifyMessage, now time.Time) {
	q := s.quiet
	q.mu.Lock()
	cfg := q.cfg
	if !cfg.active(now) || (m.Severity == SeverityCritical && cfg.Critical == "notify") {
		q.mu.Unlock()
		s.notify.dispatch(m)
		return
	}
	if m.Severity != SeverityInfo { // 提示级不发送，也不汇总
		if len(q.held) == 0 && q.dropped == 0 {
			q.since = now
		}
		if len(q.held) < quietMaxHeld {
			q.held = append(q.held, m)
		} else {
			q.dropped++
		}
	}
	q.mu.Unlock()
}

// flushQuiet 在免打扰结束后发出汇总（每轮评估调用）。
func (s *Server) flushQuiet(now time.Time) {
	q := s.quiet
	q.mu.Lock()
	if len(q.held) == 0 || q.cfg.active(now) {
		q.mu.Unlock()
		return
	}
	m := notifyMessage{Kind: NotifyQuietSummary, Items: q.held, Count: len(q.held) + q.dropped, StartedAt: q.since, ResolvedAt: now}
	for _, x := range q.held {
		if severityRank(x.Severity) > severityRank(m.Severity) || m.Severity == "" {
			m.Severity = x.Severity
		}
	}
	q.held, q.dropped = nil, 0
	q.mu.Unlock()
	s.notify.dispatch(m)
}

// forChannel 把汇总裁剪为该渠道会接收的条目；没有可发的返回 false。
func (m notifyMessage) forChannel(c NotifyChannel) (notifyMessage, bool) {
	out := m
	out.Items = nil
	for _, x := range m.Items {
		if c.wants(x) {
			out.Items = append(out.Items, x)
		}
	}
	if len(out.Items) == 0 {
		return out, false
	}
	extra := m.Count - len(m.Items) // 超出上限未保存的条数，所有渠道都提示
	out.Count = len(out.Items) + extra
	return out, true
}

func (m notifyMessage) quietText() []string {
	lines := []string{fmt.Sprintf("免打扰时段（%s～%s）内的告警：", m.StartedAt.Format("01-02 15:04"), m.ResolvedAt.Format("15:04"))}
	for i, x := range m.Items {
		if i == quietSummaryLines {
			lines = append(lines, fmt.Sprintf("……另有 %d 条，详见告警页", m.Count-quietSummaryLines))
			break
		}
		lines = append(lines, "· "+x.title())
	}
	if m.Count > len(m.Items) && len(m.Items) <= quietSummaryLines {
		lines = append(lines, fmt.Sprintf("……另有 %d 条未保存，详见告警页", m.Count-len(m.Items)))
	}
	return lines
}

// ---- 接口 ----

type quietView struct {
	QuietHours
	Active            bool   `json:"active"`             // 当前是否处于免打扰
	EffectiveTimezone string `json:"effective_timezone"` // 实际使用的时区
	Held              int    `json:"held"`               // 已暂存、等待汇总的条数
}

func (s *Server) quietView(now time.Time) quietView {
	s.quiet.mu.Lock()
	defer s.quiet.mu.Unlock()
	c := s.quiet.cfg
	tz := c.Timezone
	if tz == "" {
		// 面板本地时区的名称在多数部署中是 “Local”，显示 UTC 偏移更有用
		_, off := now.In(time.Local).Zone()
		sign := "+"
		if off < 0 {
			sign, off = "-", -off
		}
		tz = fmt.Sprintf("UTC%s%02d:%02d", sign, off/3600, off%3600/60)
	}
	return quietView{QuietHours: c, Active: c.active(now), EffectiveTimezone: tz, Held: len(s.quiet.held) + s.quiet.dropped}
}

// handleGetQuietHours：GET /api/v1/settings/quiet-hours，admin（设计 16.5）。
func (s *Server) handleGetQuietHours(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.quietView(time.Now()))
}

// handlePutQuietHours：PUT /api/v1/settings/quiet-hours，admin。修改记入操作日志 setting.update。
func (s *Server) handlePutQuietHours(w http.ResponseWriter, r *http.Request) {
	var q QuietHours
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&q); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	if errs := q.validate(); len(errs) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: errs})
		return
	}
	now := time.Now()
	if err := s.store.PutSetting(settingQuietHours, q, now); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.quiet.set(q)
	s.audit(r, AuditEntry{ActorType: "admin", Action: "setting.update", Success: true,
		Details: map[string]any{"key": settingQuietHours, "enabled": q.Enabled, "start": q.Start, "end": q.End,
			"timezone": q.Timezone, "critical": q.Critical}})
	writeJSON(w, s.quietView(now))
}
