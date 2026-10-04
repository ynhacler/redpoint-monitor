package server

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// 流量接口（设计 19.8）与手动校准（设计 5.7）。计算规则都在 traffic.go 的纯函数中，这里只负责查询与组装。

// trafficView 是节点本计费周期的流量。字节数均为整数；GB 换算由客户端按 unit 口径完成（设计 5.8）。
type trafficView struct {
	CycleStart   string        `json:"cycle_start"` // YYYY-MM-DD，面板本地时区
	CycleEnd     string        `json:"cycle_end"`   // 下一周期开始日（不含）
	Rx           uint64        `json:"rx"`
	Tx           uint64        `json:"tx"`
	Measured     uint64        `json:"measured"`   // 统计值：按计费模式取值后乘以系数，未含校准
	Adjustment   int64         `json:"adjustment"` // 本周期最近一次校准在当前系数与模式下的偏差，可为负
	CalibratedAt int64         `json:"calibrated_at,omitempty"`
	Used         uint64        `json:"used"`  // 展示值 = 统计值 + 校准偏差
	Limit        int64         `json:"limit"` // 0 = 不限
	Unit         string        `json:"unit"`
	Factor       float64       `json:"factor"`
	Forecast     *forecastView `json:"forecast,omitempty"` // 周期开始不足 3 天时不返回（设计 32）
	// 多次校准显示稳定的比例偏差时，建议设置的统计系数；没有建议时不返回（设计 5.7）
	FactorSuggestion float64 `json:"factor_suggestion,omitempty"`
}

type forecastView struct {
	Daily uint64 `json:"daily"`
	Total uint64 `json:"total"`
	Over  bool   `json:"over"`
}

// trafficOf 计算节点本周期的流量、校准与预测。
func (s *Server) trafficOf(row ServerRow, now time.Time) (trafficView, error) {
	start := CycleStart(now, row.ResetDay)
	end := CycleEnd(start, row.ResetDay)
	rx, tx, err := s.store.TrafficSince(row.ID, start)
	if err != nil {
		return trafficView{}, err
	}
	adj, err := s.store.LatestAdjustment(row.ID, start.Format("2006-01-02"))
	if err != nil {
		return trafficView{}, err
	}
	v := trafficView{CycleStart: start.Format("2006-01-02"), CycleEnd: end.Format("2006-01-02"), Rx: rx, Tx: tx,
		Measured: EffectiveUsed(CountedBytes(row.CountMode, rx, tx), row.TrafficFactor, 0),
		Limit:    row.LimitBytes, Unit: row.TrafficUnit, Factor: row.TrafficFactor}
	if adj != nil {
		v.Adjustment, v.CalibratedAt = AdjustmentNow(*adj, row.CountMode, row.TrafficFactor), adj.CreatedAt
	}
	v.Used = EffectiveUsed(CountedBytes(row.CountMode, rx, tx), row.TrafficFactor, v.Adjustment)
	if v.FactorSuggestion, err = s.factorSuggestion(row); err != nil {
		return trafficView{}, err
	}

	// 最近 7 个完整天（不含今天），用于周期过半后的日均（设计 32）
	today := dayStart(now)
	l7rx, l7tx, err := s.store.TrafficBetween(row.ID, today.AddDate(0, 0, -7), today)
	if err != nil {
		return trafficView{}, err
	}
	last7 := EffectiveUsed(CountedBytes(row.CountMode, l7rx, l7tx), row.TrafficFactor, 0)
	limit := uint64(0)
	if row.LimitBytes > 0 {
		limit = uint64(row.LimitBytes)
	}
	if f := ForecastUsage(v.Used, last7, limit, start, end, now); f.Available {
		v.Forecast = &forecastView{Daily: f.Daily, Total: f.Total, Over: f.Over}
	}
	return v, nil
}

// factorSuggestion 用最近的校准记录判断是否建议设置统计系数（设计 5.7）；没有建议时返回 0。
func (s *Server) factorSuggestion(row ServerRow) (float64, error) {
	list, err := s.store.ListAdjustments(row.ID, 20)
	if err != nil || len(list) == 0 {
		return 0, err
	}
	samples := make([]FactorSample, 0, len(list))
	for _, a := range list {
		if a.RawRx == nil || a.RawTx == nil || *a.RawRx < 0 || *a.RawTx < 0 {
			continue // 迁移 17 之前的记录没有原始字节，算不出比例
		}
		samples = append(samples, FactorSample{At: time.Unix(a.CreatedAt, 0),
			Raw: CountedBytes(row.CountMode, uint64(*a.RawRx), uint64(*a.RawTx)), Reported: a.Reported})
	}
	f, ok := SuggestFactor(samples, row.TrafficFactor)
	if !ok {
		return 0, nil
	}
	return f, nil
}

func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// nodeOr404 读取路径中的节点；不存在时写 404 并返回 nil。
func (s *Server) nodeOr404(w http.ResponseWriter, r *http.Request) *ServerRow {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return nil
	}
	row, err := s.store.GetServer(id)
	if errors.Is(err, errNoServer) {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return nil
	}
	if err != nil {
		s.writeError(w, r, internalError(err))
		return nil
	}
	return row
}

// handleTrafficCurrent：GET /api/v1/servers/{id}/traffic/current，admin。返回 trafficView（与节点详情中的 traffic 相同）。
func (s *Server) handleTrafficCurrent(w http.ResponseWriter, r *http.Request) {
	row := s.nodeOr404(w, r)
	if row == nil {
		return
	}
	v, err := s.trafficOf(*row, time.Now())
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, v)
}

type dailyItem struct {
	Day  string `json:"day"`
	Rx   uint64 `json:"rx"`
	Tx   uint64 `json:"tx"`
	Used uint64 `json:"used"` // 按计费模式取值并乘以系数；校准只作用于整个周期，不分摊到天
}

// handleTrafficDaily：GET /api/v1/servers/{id}/traffic/daily?days=30，admin。
// 返回最近 days 天（含今天，1～400，默认 30）的每日流量，按日期升序，没有数据的日期补 0，便于直接画柱状图。
func (s *Server) handleTrafficDaily(w http.ResponseWriter, r *http.Request) {
	days, ok := s.intQuery(w, r, "days", 30, 1, 400)
	if !ok {
		return
	}
	row := s.nodeOr404(w, r)
	if row == nil {
		return
	}
	today := dayStart(time.Now())
	from := today.AddDate(0, 0, -(days - 1))
	rows, err := s.store.DailyTraffic(row.ID, from, today.AddDate(0, 0, 1))
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	byDay := make(map[string]DayTraffic, len(rows))
	for _, d := range rows {
		byDay[d.Day] = d
	}
	items := make([]dailyItem, 0, days)
	for d := from; !d.After(today); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		t := byDay[key]
		items = append(items, dailyItem{Day: key, Rx: t.Rx, Tx: t.Tx,
			Used: EffectiveUsed(CountedBytes(row.CountMode, t.Rx, t.Tx), row.TrafficFactor, 0)})
	}
	writeJSON(w, map[string]any{"items": items})
}

type cycleItem struct {
	CycleStart string `json:"cycle_start"`
	CycleEnd   string `json:"cycle_end"`
	Rx         uint64 `json:"rx"`
	Tx         uint64 `json:"tx"`
	Adjustment int64  `json:"adjustment"`
	Used       uint64 `json:"used"`
	Limit      int64  `json:"limit"`
}

// handleTrafficMonthly：GET /api/v1/servers/{id}/traffic/monthly?cycles=12，admin。
// 返回最近 cycles 个计费周期（含当前，1～36，默认 12），最新在前。
// 历史周期按当前的计费模式、系数与额度计算：这些设置没有保存历史版本。
func (s *Server) handleTrafficMonthly(w http.ResponseWriter, r *http.Request) {
	n, ok := s.intQuery(w, r, "cycles", 12, 1, 36)
	if !ok {
		return
	}
	row := s.nodeOr404(w, r)
	if row == nil {
		return
	}
	items := make([]cycleItem, 0, n)
	start := CycleStart(time.Now(), row.ResetDay)
	for i := 0; i < n; i++ {
		end := CycleEnd(start, row.ResetDay)
		rx, tx, err := s.store.TrafficBetween(row.ID, start, end)
		if err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		key := start.Format("2006-01-02")
		adj, err := s.store.LatestAdjustment(row.ID, key)
		if err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		it := cycleItem{CycleStart: key, CycleEnd: end.Format("2006-01-02"), Rx: rx, Tx: tx, Limit: row.LimitBytes}
		if adj != nil {
			it.Adjustment = AdjustmentNow(*adj, row.CountMode, row.TrafficFactor)
		}
		it.Used = EffectiveUsed(CountedBytes(row.CountMode, rx, tx), row.TrafficFactor, it.Adjustment)
		items = append(items, it)
		start = CycleStart(start.AddDate(0, 0, -1), row.ResetDay)
	}
	writeJSON(w, map[string]any{"items": items})
}

// handleCalibrate：POST /api/v1/servers/{id}/traffic/calibrate，admin（设计 5.7）。
// 请求 {"used_gb": 642.3, "note": "..."}：服务商面板显示的本周期已用量，按节点的单位口径。
// 偏差 = 填写值 − 当前统计值，只作用于当前计费周期；成功 200 返回最新 trafficView。
func (s *Server) handleCalibrate(w http.ResponseWriter, r *http.Request) {
	row := s.nodeOr404(w, r)
	if row == nil {
		return
	}
	var body struct {
		UsedGB *float64 `json:"used_gb"`
		Note   string   `json:"note"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	var fe []FieldError
	if body.UsedGB == nil || *body.UsedGB < 0 || *body.UsedGB > 1e7 || math.IsNaN(*body.UsedGB) {
		fe = append(fe, FieldError{Field: "used_gb", Message: "请填写服务商面板显示的已用流量（0～10000000）"})
	}
	body.Note = strings.TrimSpace(body.Note)
	if utf8.RuneCountInString(body.Note) > 200 {
		fe = append(fe, FieldError{Field: "note", Message: "备注最多 200 个字符"})
	}
	if fe != nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}

	now := time.Now()
	cur, err := s.trafficOf(*row, now)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	reported := GBToBytes(*body.UsedGB, row.TrafficUnit)
	// 每次校准都相对当前统计值重新锚定，覆盖本周期之前的校准，而不是累加；
	// 同时记下原始收发字节，之后修改系数或计费模式时按新设置重算偏差（设计 5.7）
	rawRx, rawTx := int64(cur.Rx), int64(cur.Tx)
	a := Adjustment{CycleStart: cur.CycleStart, Measured: int64(cur.Measured), Reported: reported,
		Adjustment: reported - int64(cur.Measured), RawRx: &rawRx, RawTx: &rawTx, Note: body.Note, CreatedAt: now.Unix()}
	if _, err := s.store.AddAdjustment(row.ID, a); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "traffic.calibrate", TargetType: "server", TargetID: row.ID,
		Success: true, Details: map[string]any{"cycle_start": a.CycleStart, "measured": a.Measured,
			"reported": a.Reported, "adjustment": a.Adjustment}})
	v, err := s.trafficOf(*row, now)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.storeTraffic(*row, v, now) // 校准后列表与详情立即显示新值
	writeJSON(w, v)
}

// handleAdjustments：GET /api/v1/servers/{id}/traffic/adjustments，admin。最近 50 条校准记录，最新在前。
func (s *Server) handleAdjustments(w http.ResponseWriter, r *http.Request) {
	row := s.nodeOr404(w, r)
	if row == nil {
		return
	}
	items, err := s.store.ListAdjustments(row.ID, 50)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, map[string]any{"items": items})
}

// intQuery 读取整数查询参数；缺省时用 def，超出 [min, max] 或格式错误时写 422 并返回 false。
func (s *Server) intQuery(w http.ResponseWriter, r *http.Request, name string, def, min, max int) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed,
			Details: []FieldError{{Field: name, Message: name + " 应为 " + strconv.Itoa(min) + "～" + strconv.Itoa(max) + " 的整数"}}})
		return 0, false
	}
	return n, true
}
