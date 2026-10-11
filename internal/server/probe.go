package server

// 三网延迟（设计 42.3.2）：面板维护探测目标（内置三大运营商各省地址 + 自定义），随上报响应下发给 Agent；
// Agent 每 5 分钟一轮测延迟与丢包，结果随上报带回，保存 7 天。

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"vpsmon/internal/protocol"
)

const (
	probeKeep          = 7 * 24 * time.Hour
	maxCustomTargets   = 100
	maxProbeResultsRep = protocol.MaxProbeTargets * 3 // 一份上报最多带 3 轮结果（Agent 断网时保留的上限）
)

// 运营商代码 → 名称
var ispNames = map[string]string{"ct": "电信", "cu": "联通", "cm": "移动"}

// ProbeTargetRow 是一个探测目标。
type ProbeTargetRow struct {
	ID       int64  `json:"id"`
	ISP      string `json:"isp"` // ct / cu / cm
	Province string `json:"province"`
	Address  string `json:"address"`
	Proto    string `json:"proto"` // tcp / icmp
	Port     int    `json:"port"`
	Enabled  bool   `json:"enabled"`
	Builtin  bool   `json:"builtin"`
	Note     string `json:"note"`
	sort     int
}

func (t ProbeTargetRow) wire() (protocol.ProbeTarget, bool) {
	a, err := netip.ParseAddr(t.Address)
	pt := protocol.ProbeTarget{ID: t.ID, Proto: t.Proto, Addr: a, Port: t.Port}
	return pt, err == nil && pt.Valid()
}

// ---- 存储 ----

const probeTargetColumns = `id, isp, province, address, proto, port, enabled, builtin, sort, note`

func scanProbeTarget(sc interface{ Scan(...any) error }) (ProbeTargetRow, error) {
	var t ProbeTargetRow
	err := sc.Scan(&t.ID, &t.ISP, &t.Province, &t.Address, &t.Proto, &t.Port, &t.Enabled, &t.Builtin, &t.sort, &t.Note)
	return t, err
}

// ListProbeTargets 按显示顺序返回全部目标（内置在前，自定义按添加顺序）。
func (s *Store) ListProbeTargets() ([]ProbeTargetRow, error) {
	rows, err := s.DB.Query(`SELECT ` + probeTargetColumns + ` FROM probe_targets ORDER BY builtin DESC, sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProbeTargetRow{}
	for rows.Next() {
		t, err := scanProbeTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetProbeTarget(id int64) (ProbeTargetRow, error) {
	t, err := scanProbeTarget(s.DB.QueryRow(`SELECT `+probeTargetColumns+` FROM probe_targets WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, errorf(CodeNotFound, "探测目标不存在")
	}
	return t, err
}

func (s *Store) saveProbeTarget(t *ProbeTargetRow) error {
	if t.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO probe_targets (isp, province, address, proto, port, enabled, builtin, note)
			VALUES (?,?,?,?,?,?,0,?)`, t.ISP, t.Province, t.Address, t.Proto, t.Port, t.Enabled, t.Note)
		if err != nil {
			return err
		}
		t.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := s.DB.Exec(`UPDATE probe_targets SET isp = ?, province = ?, address = ?, proto = ?, port = ?, enabled = ?, note = ?
		WHERE id = ?`, t.ISP, t.Province, t.Address, t.Proto, t.Port, t.Enabled, t.Note, t.ID)
	return err
}

// DeleteProbeTarget 删除自定义目标及其探测记录。
func (s *Store) DeleteProbeTarget(id int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM probe_results WHERE target_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM probe_targets WHERE id = ? AND builtin = 0`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// PruneProbeResults 删除超过 7 天的探测记录。
func (s *Store) PruneProbeResults(now time.Time) (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM probe_results WHERE ts < ?`, now.Add(-probeKeep).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// insertProbeResults 在批量写入的事务中保存一份上报带回的探测结果。
// 时间取 Agent 的本轮开始时间；超前过多（时钟偏差）时用收到时间，超过保留期的丢弃。
func insertProbeResults(tx *sql.Tx, serverID int64, list []protocol.ProbeResult, seen time.Time) error {
	if len(list) > maxProbeResultsRep {
		list = list[:maxProbeResultsRep]
	}
	for _, r := range list {
		ts := r.At
		if ts > seen.Add(maxSkew).Unix() || ts <= 0 {
			ts = seen.Unix()
		}
		if ts < seen.Add(-probeKeep).Unix() || r.TargetID <= 0 || r.Loss < 0 || r.Loss > 100 {
			continue
		}
		var lat any
		if r.Latency != nil && *r.Latency >= 0 && *r.Latency < 60000 {
			lat = *r.Latency
		}
		code := r.Error
		if len(code) > 32 {
			code = code[:32]
		}
		if _, err := tx.Exec(`INSERT INTO probe_results (server_id, target_id, ts, latency_ms, loss, error) VALUES (?,?,?,?,?,?)`,
			serverID, r.TargetID, ts, lat, r.Loss, code); err != nil {
			return err
		}
	}
	return nil
}

// ---- 下发给 Agent ----

// probeState 缓存下发给 Agent 的目标列表，并记录各节点 Agent 的探测状态。
type probeState struct {
	mu       sync.Mutex
	loaded   bool
	version  string
	encoded  string
	interval int
	status   map[int64]string // on / off；没有记录表示 Agent 不支持（旧版）或尚未上报
}

// invalidate 在目标修改后调用：下一份上报重新读取并下发。
func (p *probeState) invalidate() {
	p.mu.Lock()
	p.loaded = false
	p.mu.Unlock()
}

// ProbeSettings 是三网延迟的全局设置（settings 表，键 probe）。
type ProbeSettings struct {
	IntervalS int `json:"interval_s"` // 每轮间隔，秒：60 / 120 / 300 / 600 / 900 / 1800
}

const probeSettingsKey = "probe"

// probeSettings 读取设置；没有保存过时为默认 5 分钟。
func (s *Server) probeSettings() (ProbeSettings, error) {
	v := ProbeSettings{IntervalS: protocol.DefaultProbeInterval}
	_, err := s.store.GetSetting(probeSettingsKey, &v)
	if !slices.Contains(protocol.ProbeIntervals, v.IntervalS) {
		v.IntervalS = protocol.DefaultProbeInterval
	}
	return v, err
}

// probeList 返回当前启用目标的编码、每轮间隔与版本。
func (s *Server) probeList() (version, encoded string, interval int, err error) {
	p := &s.probes
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.loaded {
		return p.version, p.encoded, p.interval, nil
	}
	list, err := s.store.ListProbeTargets()
	if err != nil {
		return "", "", 0, err
	}
	cfg, err := s.probeSettings()
	if err != nil {
		return "", "", 0, err
	}
	var wire []protocol.ProbeTarget
	for _, t := range list {
		if pt, ok := t.wire(); ok && t.Enabled && len(wire) < protocol.MaxProbeTargets {
			wire = append(wire, pt)
		}
	}
	p.encoded = protocol.EncodeProbeTargets(wire)
	p.interval = cfg.IntervalS
	p.version = protocol.ProbeVersion(p.encoded, p.interval)
	p.loaded = true
	return p.version, p.encoded, p.interval, nil
}

// syncProbe 处理上报请求中的 X-Probe-Version：记录 Agent 的探测状态，版本不同时在响应头中下发新列表。
// 旧版 Agent 不带该请求头，面板不下发（设计 42.3.2）。
func (s *Server) syncProbe(w http.ResponseWriter, r *http.Request, sid int64) {
	vals, ok := r.Header[protocol.HeaderProbeVersion]
	if !ok {
		return
	}
	have := ""
	if len(vals) > 0 {
		have = strings.TrimSpace(vals[0])
	}
	st := "on"
	if have == protocol.ProbeOff {
		st = "off"
	}
	s.probes.mu.Lock()
	if s.probes.status == nil {
		s.probes.status = map[int64]string{}
	}
	s.probes.status[sid] = st
	s.probes.mu.Unlock()
	if st == "off" {
		return
	}
	version, encoded, interval, err := s.probeList()
	if err != nil {
		s.log.Error("probe targets failed", "component", "probe", "err", err)
		return
	}
	if have != version {
		w.Header().Set(protocol.HeaderProbeVersion, version)
		w.Header().Set(protocol.HeaderProbeTargets, encoded)
		w.Header().Set(protocol.HeaderProbeInterval, strconv.Itoa(interval))
	}
}

func (s *Server) probeStatus(sid int64) string {
	s.probes.mu.Lock()
	defer s.probes.mu.Unlock()
	if st, ok := s.probes.status[sid]; ok {
		return st
	}
	return "unsupported"
}

// ---- 目标管理接口 ----

type probeTargetInput struct {
	ISP      string `json:"isp"`
	Province string `json:"province"`
	Address  string `json:"address"`
	Proto    string `json:"proto"`
	Port     *int   `json:"port"`
	Enabled  *bool  `json:"enabled"`
	Note     string `json:"note"`
}

// apply 校验并写入 t。内置目标只能修改协议、端口、启用与备注。
func (b probeTargetInput) apply(t *ProbeTargetRow) []FieldError {
	var errs []FieldError
	bad := func(f, msg string) { errs = append(errs, FieldError{Field: f, Message: msg}) }
	if !t.Builtin {
		t.ISP = b.ISP
		if ispNames[t.ISP] == "" {
			bad("isp", "运营商只能是 ct（电信）、cu（联通）或 cm（移动）")
		}
		t.Province = strings.TrimSpace(b.Province)
		if t.Province == "" || utf8.RuneCountInString(t.Province) > 16 {
			bad("province", "请填写地区（最多 16 个字符），如 上海")
		}
		a, err := netip.ParseAddr(strings.TrimSpace(b.Address))
		switch {
		case err != nil:
			bad("address", "请填写 IP 地址（不接受域名）")
		case !protocol.PublicProbeAddr(a):
			bad("address", "【安全】只能探测公网地址，不能是内网、回环或保留地址")
		default:
			t.Address = a.Unmap().String()
		}
	}
	if b.Proto != "" {
		t.Proto = b.Proto
	}
	switch t.Proto {
	case "tcp":
		if b.Port != nil {
			t.Port = *b.Port
		}
		if t.Port == 0 {
			t.Port = 80
		}
		if t.Port < 1 || t.Port > 65535 {
			bad("port", "端口为 1～65535")
		}
	case "icmp":
		t.Port = 0
	default:
		bad("proto", "协议只能是 tcp 或 icmp")
	}
	if b.Enabled != nil {
		t.Enabled = *b.Enabled
	}
	t.Note = strings.TrimSpace(b.Note)
	if utf8.RuneCountInString(t.Note) > 64 {
		bad("note", "备注最多 64 个字符")
	}
	return errs
}

func decodeProbeTarget(w http.ResponseWriter, r *http.Request) (probeTargetInput, error) {
	var b probeTargetInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return b, &APIError{Code: CodeBadRequest, Cause: err}
	}
	return b, nil
}

// checkEnabledLimit：启用的目标不能超过 Agent 的上限（设计 42.6）。
func (s *Server) checkEnabledLimit(t ProbeTargetRow) []FieldError {
	if !t.Enabled {
		return nil
	}
	var n int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM probe_targets WHERE enabled = 1 AND id <> ?`, t.ID).Scan(&n)
	if n >= protocol.MaxProbeTargets {
		return []FieldError{{Field: "enabled", Message: fmt.Sprintf("最多同时启用 %d 个目标（Agent 的上限）", protocol.MaxProbeTargets)}}
	}
	return nil
}

// handleProbeTargets：GET /api/v1/probe-targets，admin。
func (s *Server) handleProbeTargets(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListProbeTargets()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeList(w, list, "", nil)
}

// handleCreateProbeTarget：POST /api/v1/probe-targets，admin。添加自定义目标。
func (s *Server) handleCreateProbeTarget(w http.ResponseWriter, r *http.Request) {
	b, err := decodeProbeTarget(w, r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	t := ProbeTargetRow{Proto: "tcp", Enabled: true}
	errs := b.apply(&t)
	errs = append(errs, s.checkEnabledLimit(t)...)
	if len(errs) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: errs})
		return
	}
	var n int
	if err := s.store.DB.QueryRow(`SELECT COUNT(*) FROM probe_targets WHERE builtin = 0`).Scan(&n); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if n >= maxCustomTargets {
		s.writeError(w, r, errorf(CodeConflict, fmt.Sprintf("最多添加 %d 个自定义目标", maxCustomTargets)))
		return
	}
	if err := s.store.saveProbeTarget(&t); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.probes.invalidate()
	s.audit(r, AuditEntry{ActorType: "admin", Action: "probe_target.create", Success: true,
		Details: map[string]any{"id": t.ID, "address": t.Address, "proto": t.Proto, "port": t.Port}})
	writeJSONStatus(w, http.StatusCreated, t)
}

// handleUpdateProbeTarget：PUT /api/v1/probe-targets/{id}，admin。
func (s *Server) handleUpdateProbeTarget(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	t, err := s.store.GetProbeTarget(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	b, err := decodeProbeTarget(w, r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	errs := b.apply(&t)
	errs = append(errs, s.checkEnabledLimit(t)...)
	if len(errs) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: errs})
		return
	}
	if err := s.store.saveProbeTarget(&t); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.probes.invalidate()
	s.audit(r, AuditEntry{ActorType: "admin", Action: "probe_target.update", Success: true,
		Details: map[string]any{"id": t.ID, "proto": t.Proto, "port": t.Port, "enabled": t.Enabled}})
	writeJSON(w, t)
}

// handleDeleteProbeTarget：DELETE /api/v1/probe-targets/{id}，admin。内置目标不能删除，只能停用。
func (s *Server) handleDeleteProbeTarget(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	t, err := s.store.GetProbeTarget(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if t.Builtin {
		s.writeError(w, r, errorf(CodeConflict, "内置目标不能删除，可以停用"))
		return
	}
	if err := s.store.DeleteProbeTarget(id); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.probes.invalidate()
	s.audit(r, AuditEntry{ActorType: "admin", Action: "probe_target.delete", Success: true, Details: map[string]any{"id": id}})
	w.WriteHeader(http.StatusNoContent)
}

// handleGetProbeSettings：GET /api/v1/settings/probe，admin。
func (s *Server) handleGetProbeSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.probeSettings()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, v)
}

// handlePutProbeSettings：PUT /api/v1/settings/probe，admin。随下一份上报下发给各 Agent。
func (s *Server) handlePutProbeSettings(w http.ResponseWriter, r *http.Request) {
	var b ProbeSettings
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	if !slices.Contains(protocol.ProbeIntervals, b.IntervalS) {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed,
			Details: []FieldError{{Field: "interval_s", Message: "测试频率只能是 1、2、5、10、15 或 30 分钟"}}})
		return
	}
	if err := s.store.PutSetting(probeSettingsKey, b, time.Now()); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.probes.invalidate()
	s.audit(r, AuditEntry{ActorType: "admin", Action: "probe_settings.update", Success: true,
		Details: map[string]any{"interval_s": b.IntervalS}})
	writeJSON(w, b)
}

// ---- 节点的三网延迟 ----

// LatencyTarget 是节点详情中一个目标的最近 1 小时汇总。
type LatencyTarget struct {
	ProbeTargetRow
	Latency   *float64 `json:"latency_ms"` // 最近 1 小时成功轮次延迟的中位数
	Loss      *float64 `json:"loss"`       // 最近 1 小时平均丢包率
	LastAt    int64    `json:"last_at"`
	LastError string   `json:"last_error"` // 最近一轮全部失败时的原因
}

// LatencyPoint 是曲线上的一个点。
type LatencyPoint struct {
	TS      int64    `json:"ts"`
	Latency *float64 `json:"latency_ms"`
	Loss    float64  `json:"loss"`
}

// LatencyView 是 GET /servers/{id}/latency 的响应。
type LatencyView struct {
	Status  string                    `json:"status"` // on / off / unsupported
	Targets []LatencyTarget           `json:"targets"`
	Series  map[string][]LatencyPoint `json:"series"` // 键为目标 ID
}

// summarizeLatency 汇总一个目标最近 1 小时的结果：延迟取各轮（成功的）中位数，丢包率取平均。纯函数。
func summarizeLatency(pts []LatencyPoint) (lat, loss *float64) {
	if len(pts) == 0 {
		return nil, nil
	}
	var lats []float64
	sum := 0.0
	for _, p := range pts {
		sum += p.Loss
		if p.Latency != nil {
			lats = append(lats, *p.Latency)
		}
	}
	l := sum / float64(len(pts))
	loss = &l
	if len(lats) > 0 {
		sort.Float64s(lats)
		m := lats[len(lats)/2]
		if len(lats)%2 == 0 {
			m = (lats[len(lats)/2-1] + m) / 2
		}
		lat = &m
	}
	return lat, loss
}

// handleLatency：GET /api/v1/servers/{id}/latency?range=24h|7d，读取类。7 天按小时汇总。
func (s *Server) handleLatency(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if !s.requireServerInScope(w, r, id) {
		return
	}
	if _, err := s.store.GetServer(id); errors.Is(err, errNoServer) {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	} else if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	now := time.Now()
	since, bucket := now.Add(-24*time.Hour), int64(0)
	switch r.URL.Query().Get("range") {
	case "", "24h":
	case "7d":
		since, bucket = now.Add(-probeKeep), 3600
	default:
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "range", Message: "只能是 24h 或 7d"}}})
		return
	}
	targets, err := s.store.ListProbeTargets()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	raw, err := s.store.probeSeries(id, since)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	v := LatencyView{Status: s.probeStatus(id), Targets: []LatencyTarget{}, Series: map[string][]LatencyPoint{}}
	hour := now.Add(-time.Hour).Unix()
	for _, t := range targets {
		pts := raw[t.ID]
		if !t.Enabled && len(pts) == 0 {
			continue // 停用且范围内没有数据的不显示
		}
		lt := LatencyTarget{ProbeTargetRow: t}
		var recent []LatencyPoint
		for _, p := range pts {
			if p.TS >= hour {
				recent = append(recent, p)
			}
		}
		lt.Latency, lt.Loss = summarizeLatency(recent)
		if n := len(pts); n > 0 {
			lt.LastAt = pts[n-1].TS
			if pts[n-1].Latency == nil {
				lt.LastError = s.store.lastProbeError(id, t.ID)
			}
		}
		v.Targets = append(v.Targets, lt)
		if bucket > 0 {
			pts = bucketLatency(pts, bucket)
		}
		if pts == nil {
			pts = []LatencyPoint{}
		}
		v.Series[fmt.Sprint(t.ID)] = pts
	}
	writeJSON(w, v)
}

// bucketLatency 按 bucket 秒汇总：延迟取成功轮次的平均，丢包率取平均。纯函数。
func bucketLatency(pts []LatencyPoint, bucket int64) []LatencyPoint {
	var out []LatencyPoint
	var cur LatencyPoint
	var latSum float64
	var latN, n int
	flush := func() {
		if n == 0 {
			return
		}
		p := LatencyPoint{TS: cur.TS, Loss: cur.Loss / float64(n)}
		if latN > 0 {
			l := float64(int(latSum/float64(latN)*10+0.5)) / 10
			p.Latency = &l
		}
		out = append(out, p)
	}
	for _, p := range pts {
		b := p.TS / bucket * bucket
		if n > 0 && b != cur.TS {
			flush()
			n, latN, latSum, cur = 0, 0, 0, LatencyPoint{}
		}
		cur.TS = b
		cur.Loss += p.Loss
		n++
		if p.Latency != nil {
			latSum += *p.Latency
			latN++
		}
	}
	flush()
	return out
}

// probeSeries 返回节点各目标自 since 以来的结果（按时间升序）。
func (s *Store) probeSeries(serverID int64, since time.Time) (map[int64][]LatencyPoint, error) {
	rows, err := s.DB.Query(`SELECT target_id, ts, latency_ms, loss FROM probe_results WHERE server_id = ? AND ts >= ? ORDER BY ts`,
		serverID, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]LatencyPoint{}
	for rows.Next() {
		var id int64
		var p LatencyPoint
		var lat sql.NullFloat64
		if err := rows.Scan(&id, &p.TS, &lat, &p.Loss); err != nil {
			return nil, err
		}
		if lat.Valid {
			v := lat.Float64
			p.Latency = &v
		}
		out[id] = append(out[id], p)
	}
	return out, rows.Err()
}

func (s *Store) lastProbeError(serverID, targetID int64) string {
	var e string
	s.DB.QueryRow(`SELECT error FROM probe_results WHERE server_id = ? AND target_id = ? ORDER BY ts DESC LIMIT 1`,
		serverID, targetID).Scan(&e)
	return e
}
