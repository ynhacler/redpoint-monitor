package server

// 汇率（修订第 223 条）：把订阅的外币价格换算成人民币显示与统计。
//
// 来源为 ExchangeRate-API 的免费接口（open.er-api.com，无需 Key，每日更新，覆盖订阅用到的全部币种）。Google 的汇率没有公开接口，
// 抓取网页既不稳定也违反其条款，所以不用。面板每 12 小时最多请求一次，只在有非人民币订阅时请求；请求不带任何用户数据。
// 结果存入 fx_rates（1 单位外币折合多少人民币），面板重启后沿用；请求失败时保留上次的汇率并在接口中给出原因。
//
// 汇率历史（修订第 295 条）：来源没有免费的历史接口，所以每次刷新时把订阅用到的外币记入 fx_history（按来源日期每天一条，保留两年），
// 记录从升级或开始使用该币种起，不补更早的数据。历史只用于查看走势，订阅金额的换算仍用最新汇率。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	fxSource     = "ExchangeRate-API（open.er-api.com）"
	fxMaxAge     = 12 * time.Hour
	fxMaxBody    = 256 << 10
	fxRetryAfter = time.Hour // 请求失败后至少隔这么久再试
	fxKeepDays   = 730       // 汇率历史保留的天数
)

// fxState 是内存中的汇率与最近一次请求的结果。
type fxState struct {
	mu        sync.Mutex
	rates     map[string]float64 // 币种 → 1 单位折合人民币
	asOf      int64              // 来源数据的时间（Unix 秒）
	fetchedAt int64
	lastTry   time.Time
	lastErr   string
	running   bool
}

// FxView 是 GET /api/v1/fx-rates 的响应。
type FxView struct {
	Base      string             `json:"base"`
	Source    string             `json:"source"`
	AsOf      int64              `json:"as_of"`
	FetchedAt int64              `json:"fetched_at"`
	Rates     map[string]float64 `json:"rates"`
	Error     string             `json:"error"`
}

// FxDay 是某币种某天的汇率。
type FxDay struct {
	Day string  `json:"day"` // YYYY-MM-DD（UTC，来源数据的日期）
	CNY float64 `json:"cny"` // 1 单位折合多少人民币
}

// loadFx 从数据库读出上次的汇率（启动时）。
func (s *Server) loadFx() {
	rows, err := s.store.DB.Query(`SELECT currency, cny, as_of, fetched_at FROM fx_rates`)
	if err != nil {
		return
	}
	defer rows.Close()
	s.fx.mu.Lock()
	defer s.fx.mu.Unlock()
	s.fx.rates = map[string]float64{}
	for rows.Next() {
		var c string
		var v float64
		var asOf, fetched int64
		if rows.Scan(&c, &v, &asOf, &fetched) == nil {
			s.fx.rates[c] = v
			s.fx.asOf, s.fx.fetchedAt = asOf, fetched
		}
	}
}

// fxNeeded 判断是否有非人民币的订阅（没有时不请求汇率）。
func (s *Server) fxNeeded() bool {
	var n int
	_ = s.store.DB.QueryRow(`SELECT COUNT(*) FROM subscriptions WHERE currency <> 'CNY'`).Scan(&n)
	return n > 0
}

// fxDue 判断汇率是否需要刷新：过期（12 小时）且距上次失败超过 1 小时。
func (s *Server) fxDue(now time.Time) bool {
	s.fx.mu.Lock()
	defer s.fx.mu.Unlock()
	if s.fx.running || now.Sub(time.Unix(s.fx.fetchedAt, 0)) < fxMaxAge {
		return false
	}
	return s.fx.lastErr == "" || now.Sub(s.fx.lastTry) >= fxRetryAfter
}

// refreshFx 请求一次汇率并保存；失败时保留原来的汇率，记录原因。
func (s *Server) refreshFx(ctx context.Context, now time.Time) {
	s.fx.mu.Lock()
	if s.fx.running {
		s.fx.mu.Unlock()
		return
	}
	s.fx.running, s.fx.lastTry = true, now
	s.fx.mu.Unlock()
	rates, asOf, err := s.fetchFx(ctx)
	if err == nil {
		err = s.saveFx(rates, asOf, now)
	}
	s.fx.mu.Lock()
	defer s.fx.mu.Unlock()
	s.fx.running = false
	if err != nil {
		s.fx.lastErr = err.Error()
		s.log.Warn("fx rates refresh failed", "component", "fx", "err", err)
		return
	}
	s.fx.rates, s.fx.asOf, s.fx.fetchedAt, s.fx.lastErr = rates, asOf, now.Unix(), ""
}

// fetchFx 请求以人民币为基准的汇率，返回 1 单位外币折合多少人民币。
func (s *Server) fetchFx(ctx context.Context) (map[string]float64, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.fxURL, nil)
	if err != nil {
		return nil, 0, err
	}
	res, err := s.fxHTTP.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("无法连接汇率服务：%w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("汇率服务返回 HTTP %d", res.StatusCode)
	}
	var b struct {
		Result   string             `json:"result"`
		Base     string             `json:"base_code"`
		UpdateAt int64              `json:"time_last_update_unix"`
		Rates    map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, fxMaxBody)).Decode(&b); err != nil {
		return nil, 0, fmt.Errorf("汇率服务返回的数据无法识别：%w", err)
	}
	if b.Result != "success" || b.Base != "CNY" || len(b.Rates) == 0 {
		return nil, 0, fmt.Errorf("汇率服务返回的数据无法识别（result=%q base=%q）", b.Result, b.Base)
	}
	out := map[string]float64{"CNY": 1}
	for c, perCNY := range b.Rates {
		if currencyCode.MatchString(c) && perCNY > 0 {
			out[c] = 1 / perCNY // 接口给的是 1 元人民币折合多少外币
		}
	}
	return out, b.UpdateAt, nil
}

// fxUsedCurrencies 返回订阅用到的外币。
func (s *Server) fxUsedCurrencies() ([]string, error) {
	rows, err := s.store.DB.Query(`SELECT DISTINCT currency FROM subscriptions WHERE currency <> 'CNY'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// saveFx 整体替换 fx_rates，并把订阅用到的外币记入 fx_history（同一天以最后一次为准，修订第 295 条）。
func (s *Server) saveFx(rates map[string]float64, asOf int64, now time.Time) error {
	used, err := s.fxUsedCurrencies()
	if err != nil {
		return err
	}
	tx, err := s.store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM fx_rates`); err != nil {
		return err
	}
	for c, v := range rates {
		if _, err := tx.Exec(`INSERT INTO fx_rates (currency, cny, as_of, fetched_at) VALUES (?,?,?,?)`, c, v, asOf, now.Unix()); err != nil {
			return err
		}
	}
	day := now.UTC()
	if asOf > 0 {
		day = time.Unix(asOf, 0).UTC()
	}
	for _, c := range used {
		v, ok := rates[c]
		if !ok {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO fx_history (day, currency, cny) VALUES (?,?,?)
			ON CONFLICT (currency, day) DO UPDATE SET cny = excluded.cny`, day.Format(time.DateOnly), c, v); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM fx_history WHERE day < ?`, now.UTC().AddDate(0, 0, -fxKeepDays).Format(time.DateOnly)); err != nil {
		return err
	}
	return tx.Commit()
}

// fxLoop 每小时检查一次：有非人民币订阅且汇率过期时刷新。
func (s *Server) fxLoop(ctx context.Context) {
	wait := time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = time.Hour
		if now := time.Now(); s.fxNeeded() && s.fxDue(now) {
			s.refreshFx(ctx, now)
		}
	}
}

// handleFxRates：GET /api/v1/fx-rates，读取类。汇率过期且需要时在后台刷新，本次先返回已有的汇率。
func (s *Server) handleFxRates(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	if s.fxNeeded() && s.fxDue(now) {
		go s.refreshFx(context.WithoutCancel(r.Context()), now)
	}
	s.fx.mu.Lock()
	v := FxView{Base: "CNY", Source: fxSource, AsOf: s.fx.asOf, FetchedAt: s.fx.fetchedAt, Rates: map[string]float64{"CNY": 1}, Error: s.fx.lastErr}
	for c, x := range s.fx.rates {
		v.Rates[c] = x
	}
	s.fx.mu.Unlock()
	writeJSON(w, v)
}

// handleFxHistory：GET /api/v1/fx-rates/history?currency=USD，读取类（修订第 295 条）。按日期从早到晚返回，不含用户数据。
func (s *Server) handleFxHistory(w http.ResponseWriter, r *http.Request) {
	c := r.URL.Query().Get("currency")
	if !currencyCode.MatchString(c) {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "currency", Message: "请选择币种"}}})
		return
	}
	rows, err := s.store.DB.Query(`SELECT day, cny FROM fx_history WHERE currency = ? ORDER BY day`, c)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	defer rows.Close()
	items := []FxDay{}
	for rows.Next() {
		var d FxDay
		if err := rows.Scan(&d.Day, &d.CNY); err != nil {
			s.writeError(w, r, err)
			return
		}
		items = append(items, d)
	}
	if err := rows.Err(); err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"items": items, "next_cursor": ""})
}
