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
	maxCustomTargets   = 100                          // 共用的自定义目标
	maxNodeTargets     = 20                           // 每台节点自己的目标
	maxProbeResultsRep = protocol.MaxProbeTargets * 3 // 一份上报最多带 3 轮结果（Agent 断网时保留的上限）
)

// 运营商代码 → 名称
var ispNames = map[string]string{"ct": "电信", "cu": "联通", "cm": "移动", "intl": "境外"}

// ProbeTargetRow 是一个探测目标。
type ProbeTargetRow struct {
	ID       int64  `json:"id"`
	ISP      string `json:"isp"` // ct / cu / cm；intl 为境外（province 为城市，note 为提供商）
	Province string `json:"province"`
	Address  string `json:"address"`
	Proto    string `json:"proto"` // tcp / icmp
	Port     int    `json:"port"`
	Enabled  bool   `json:"enabled"`
	Builtin  bool   `json:"builtin"`
	Note     string `json:"note"`
	ServerID int64  `json:"server_id"` // 0：所有节点共用；否则只属于该节点（修订第 123 条）
	sort     int
}

func (t ProbeTargetRow) wire() (protocol.ProbeTarget, bool) {
	pt := protocol.ProbeTarget{ID: t.ID, Proto: t.Proto, Port: t.Port}
	if a, err := netip.ParseAddr(t.Address); err == nil {
		pt.Addr = a
	} else {
		pt.Host = t.Address // 域名（修订第 132 条），由 Agent 解析
	}
	return pt, pt.Valid()
}

// ---- 存储 ----

const probeTargetColumns = `id, isp, province, address, proto, port, enabled, builtin, sort, note, server_id`

func scanProbeTarget(sc interface{ Scan(...any) error }) (ProbeTargetRow, error) {
	var t ProbeTargetRow
	err := sc.Scan(&t.ID, &t.ISP, &t.Province, &t.Address, &t.Proto, &t.Port, &t.Enabled, &t.Builtin, &t.sort, &t.Note, &t.ServerID)
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
		res, err := s.DB.Exec(`INSERT INTO probe_targets (isp, province, address, proto, port, enabled, builtin, note, server_id)
			VALUES (?,?,?,?,?,?,0,?,?)`, t.ISP, t.Province, t.Address, t.Proto, t.Port, t.Enabled, t.Note, t.ServerID)
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

// probeState 缓存全部目标与全局频率，并记录各节点 Agent 的探测状态。
type probeState struct {
	mu       sync.Mutex
	loaded   bool
	targets  []ProbeTargetRow
	settings ProbeSettings
	status   map[int64]string // on / off；没有记录表示 Agent 不支持（旧版）或尚未上报
}

// 节点的三网延迟模式（修订第 121 条）
const (
	probeModeDefault = "default" // 跟随全局：全局启用的目标与频率
	probeModeCustom  = "custom"  // 自定义：节点自己勾选的目标（不受全局启用影响），频率可单独设置
	probeModeOff     = "off"     // 这台节点不测
)

// NodeProbe 是节点自己的三网延迟设置。
type NodeProbe struct {
	Mode      string  `json:"mode"`
	TargetIDs []int64 `json:"target_ids"` // custom 时有效
	IntervalS int     `json:"interval_s"` // custom 时有效；0 表示跟随全局
	TimeoutS  int     `json:"timeout_s"`  // custom 时有效：每个样本的超时 1～10 秒；0 表示跟随全局（修订第 133 条）
	GapS      int     `json:"gap_s"`      // custom 时有效：样本间隔 1～10 秒；0 表示跟随全局
}

// parseNodeProbe 解析 servers.probe_conf；空或无法解析时为跟随全局。
func parseNodeProbe(raw string) NodeProbe {
	np := NodeProbe{Mode: probeModeDefault, TargetIDs: []int64{}}
	if raw == "" || json.Unmarshal([]byte(raw), &np) != nil || (np.Mode != probeModeCustom && np.Mode != probeModeOff) {
		return NodeProbe{Mode: probeModeDefault, TargetIDs: []int64{}}
	}
	if np.TargetIDs == nil {
		np.TargetIDs = []int64{}
	}
	return np
}

// effectiveProbe 计算节点 sid 实际使用的目标与频率（纯函数）：
// 跟随全局时为全局启用的共用目标；自定义时为节点勾选且仍存在的共用目标或这台节点自己的目标；关闭时没有目标。最多 64 个。
// 【安全】其他节点自己的目标永远不会下发给这台节点。
func effectiveProbe(all []ProbeTargetRow, globalInterval int, np NodeProbe, sid int64) ([]ProbeTargetRow, int) {
	interval := globalInterval
	var out []ProbeTargetRow
	switch np.Mode {
	case probeModeOff:
		return nil, interval
	case probeModeCustom:
		if slices.Contains(protocol.ProbeIntervals, np.IntervalS) {
			interval = np.IntervalS
		}
		for _, t := range all {
			if slices.Contains(np.TargetIDs, t.ID) && (t.ServerID == 0 || t.ServerID == sid) {
				out = append(out, t)
			}
		}
	default:
		for _, t := range all {
			if t.Enabled && t.ServerID == 0 {
				out = append(out, t)
			}
		}
	}
	if len(out) > protocol.MaxProbeTargets {
		out = out[:protocol.MaxProbeTargets]
	}
	return out, interval
}

// effectiveTiming 计算节点实际使用的超时与样本间隔（纯函数）：自定义时节点的非 0 值优先，
// 其次全局设置的非 0 值，最后为默认 4 秒、1 秒（修订第 133 条）。
func effectiveTiming(global ProbeSettings, np NodeProbe) protocol.ProbeTiming {
	t := protocol.DefaultTiming
	pick := func(dst *int, vals ...int) {
		for _, v := range vals {
			if v >= 1 && v <= protocol.MaxProbeTiming {
				*dst = v
				return
			}
		}
	}
	var nodeTimeout, nodeGap int
	if np.Mode == probeModeCustom {
		nodeTimeout, nodeGap = np.TimeoutS, np.GapS
	}
	pick(&t.Timeout, nodeTimeout, global.TimeoutS)
	pick(&t.Gap, nodeGap, global.GapS)
	return t
}

// validTiming 判断超时或样本间隔的取值：0（默认 / 跟随全局）或 1～10 秒。
func validTiming(v int) bool { return v >= 0 && v <= protocol.MaxProbeTiming }

// invalidate 在目标修改后调用：下一份上报重新读取并下发。
func (p *probeState) invalidate() {
	p.mu.Lock()
	p.loaded = false
	p.mu.Unlock()
}

// ProbeSettings 是三网延迟的全局设置（settings 表，键 probe）。
type ProbeSettings struct {
	IntervalS int `json:"interval_s"` // 每轮间隔，秒：60 / 120 / 300 / 600 / 900 / 1800
	TimeoutS  int `json:"timeout_s"`  // 每个样本的超时（最大等待）1～10 秒；0 为默认 4 秒（修订第 133 条）
	GapS      int `json:"gap_s"`      // 样本间隔 1～10 秒；0 为默认 1 秒
}

const probeSettingsKey = "probe"

// probeSettings 读取设置；没有保存过时为默认 5 分钟。
func (s *Server) probeSettings() (ProbeSettings, error) {
	v := ProbeSettings{IntervalS: protocol.DefaultProbeInterval}
	_, err := s.store.GetSetting(probeSettingsKey, &v)
	if !slices.Contains(protocol.ProbeIntervals, v.IntervalS) {
		v.IntervalS = protocol.DefaultProbeInterval
	}
	if !validTiming(v.TimeoutS) {
		v.TimeoutS = 0
	}
	if !validTiming(v.GapS) {
		v.GapS = 0
	}
	return v, err
}

// probeBase 返回全部目标与全局设置（缓存，目标或全局设置修改后重新读取）。
func (s *Server) probeBase() ([]ProbeTargetRow, ProbeSettings, error) {
	p := &s.probes
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.loaded {
		return p.targets, p.settings, nil
	}
	list, err := s.store.ListProbeTargets()
	if err != nil {
		return nil, ProbeSettings{}, err
	}
	cfg, err := s.probeSettings()
	if err != nil {
		return nil, ProbeSettings{}, err
	}
	p.targets, p.settings, p.loaded = list, cfg, true
	return p.targets, p.settings, nil
}

// probeList 返回节点要下发的目标编码、每轮间隔、超时与版本。
func (s *Server) probeList(sid int64, np NodeProbe) (version, encoded string, interval int, timing protocol.ProbeTiming, err error) {
	all, global, err := s.probeBase()
	if err != nil {
		return "", "", 0, timing, err
	}
	targets, interval := effectiveProbe(all, global.IntervalS, np, sid)
	timing = effectiveTiming(global, np)
	var wire []protocol.ProbeTarget
	for _, t := range targets {
		if pt, ok := t.wire(); ok {
			wire = append(wire, pt)
		}
	}
	encoded = protocol.EncodeProbeTargets(wire)
	// 超时为默认值时不参与版本，升级面板后已有的版本不变、不必重新下发
	ver := encoded
	if timing != protocol.DefaultTiming && encoded != "" {
		ver += "@t" + timing.Encode()
	}
	return protocol.ProbeVersion(ver, interval), encoded, interval, timing, nil
}

// syncProbe 处理上报请求中的 X-Probe-Version：记录 Agent 的探测状态，版本不同时在响应头中下发新列表。
// 旧版 Agent 不带该请求头，面板不下发（设计 42.3.2）。
func (s *Server) syncProbe(w http.ResponseWriter, r *http.Request, sid int64, np NodeProbe) {
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
	version, encoded, interval, timing, err := s.probeList(sid, np)
	if err != nil {
		s.log.Error("probe targets failed", "component", "probe", "err", err)
		return
	}
	if have != version {
		w.Header().Set(protocol.HeaderProbeVersion, version)
		w.Header().Set(protocol.HeaderProbeTargets, encoded)
		w.Header().Set(protocol.HeaderProbeInterval, strconv.Itoa(interval))
		w.Header().Set(protocol.HeaderProbeTiming, timing.Encode())
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
	// 修改时省略的字段保持不变（只改“启用”时不必带上其他字段）；添加时运营商、地区、地址必填
	ISP      *string `json:"isp"`
	Province *string `json:"province"`
	Address  *string `json:"address"`
	Proto    string  `json:"proto"`
	Port     *int    `json:"port"`
	Enabled  *bool   `json:"enabled"`
	Note     *string `json:"note"`
	ServerID *int64  `json:"server_id"` // 只在添加时有效：这台节点自己的目标
}

// apply 校验并写入 t。内置目标只能修改协议、端口、启用与备注。
func (b probeTargetInput) apply(t *ProbeTargetRow) []FieldError {
	var errs []FieldError
	bad := func(f, msg string) { errs = append(errs, FieldError{Field: f, Message: msg}) }
	creating := t.ID == 0
	// 内置目标的运营商、地区、地址不可修改
	if !t.Builtin && (creating || b.ISP != nil) {
		t.ISP = deref(b.ISP)
		if ispNames[t.ISP] == "" {
			bad("isp", "运营商只能是 ct（电信）、cu（联通）、cm（移动）或 intl（境外）")
		}
	}
	if !t.Builtin && (creating || b.Province != nil) {
		t.Province = strings.TrimSpace(deref(b.Province))
		if t.Province == "" || utf8.RuneCountInString(t.Province) > 16 {
			bad("province", "请填写地区（最多 16 个字符），如 上海")
		}
	}
	if !t.Builtin && (creating || b.Address != nil) {
		// 公网 IPv4 / IPv6，或域名（修订第 132 条）：面板不解析域名，由 Agent 每轮解析并只连接公网地址
		raw := strings.TrimSpace(deref(b.Address))
		a, err := netip.ParseAddr(strings.Trim(raw, "[]"))
		host := protocol.NormalizeProbeHost(raw)
		switch {
		case raw == "":
			bad("address", "请填写 IP 地址或域名")
		case err == nil && !protocol.PublicProbeAddr(a):
			bad("address", "【安全】只能探测公网地址，不能是内网、回环或保留地址")
		case err == nil:
			t.Address = a.Unmap().String()
		case protocol.ValidProbeHost(host):
			t.Address = host
		default:
			bad("address", "请填写公网 IP（IPv4 / IPv6）或公网域名；不支持 localhost、.lan、.local 等局域网名称，端口请填在“端口”中")
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
	if b.Note != nil {
		t.Note = strings.TrimSpace(*b.Note)
	}
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
	if !t.Enabled || t.ServerID != 0 {
		return nil
	}
	var n int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM probe_targets WHERE enabled = 1 AND server_id = 0 AND id <> ?`, t.ID).Scan(&n)
	if n >= protocol.MaxProbeTargets {
		return []FieldError{{Field: "enabled", Message: fmt.Sprintf("最多同时启用 %d 个目标（Agent 的上限）", protocol.MaxProbeTargets)}}
	}
	return nil
}

// handleProbeTargets：GET /api/v1/probe-targets，admin。
// 默认只列共用目标；带 server_id 时再加上该节点自己的目标（修订第 123 条）。
func (s *Server) handleProbeTargets(w http.ResponseWriter, r *http.Request) {
	var sid int64
	if v := r.URL.Query().Get("server_id"); v != "" {
		n, err := parsePositive(v)
		if err != nil {
			s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "server_id", Message: "server_id 无效"}}})
			return
		}
		sid = n
	}
	list, err := s.store.ListProbeTargets()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	health, err := s.store.probeHealth(time.Now().Add(-probeHealthWindow))
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	// 列表中的每个目标带上最近 24 小时的健康汇总（修订第 134 条）
	type item struct {
		ProbeTargetRow
		Health ProbeTargetHealth `json:"health"`
	}
	out := []item{}
	for _, t := range list {
		if t.ServerID == 0 || (sid > 0 && t.ServerID == sid) {
			h := health[t.ID]
			h.State = probeHealthState(h)
			out = append(out, item{t, h})
		}
	}
	writeList(w, out, "", nil)
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
	if b.ServerID != nil && *b.ServerID > 0 {
		// 节点自己的目标：只在这台节点的自定义设置中使用，“全局启用”对它没有意义
		if _, err := s.store.GetServer(*b.ServerID); err != nil {
			errs = append(errs, FieldError{Field: "server_id", Message: "节点不存在"})
		}
		t.ServerID, t.Enabled = *b.ServerID, false
	}
	errs = append(errs, s.checkEnabledLimit(t)...)
	if len(errs) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: errs})
		return
	}
	limit, q := maxCustomTargets, `SELECT COUNT(*) FROM probe_targets WHERE builtin = 0 AND server_id = ?`
	if t.ServerID > 0 {
		limit = maxNodeTargets
	}
	var n int
	if err := s.store.DB.QueryRow(q, t.ServerID).Scan(&n); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if n >= limit {
		msg := fmt.Sprintf("最多添加 %d 个共用的自定义目标", maxCustomTargets)
		if t.ServerID > 0 {
			msg = fmt.Sprintf("每台节点最多添加 %d 个自己的目标", maxNodeTargets)
		}
		s.writeError(w, r, errorf(CodeConflict, msg))
		return
	}
	if err := s.store.saveProbeTarget(&t); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.probes.invalidate()
	s.audit(r, AuditEntry{ActorType: "admin", Action: "probe_target.create", Success: true,
		Details: map[string]any{"id": t.ID, "address": t.Address, "proto": t.Proto, "port": t.Port, "server_id": t.ServerID}})
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
	if t.ServerID != 0 {
		t.Enabled = false // 节点自己的目标不参与全局启用
	}
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
	var fe []FieldError
	if !slices.Contains(protocol.ProbeIntervals, b.IntervalS) {
		fe = append(fe, FieldError{Field: "interval_s", Message: "测试频率只能是 1、2、5、10、15 或 30 分钟"})
	}
	if !validTiming(b.TimeoutS) {
		fe = append(fe, FieldError{Field: "timeout_s", Message: "超时为 1～10 秒，0 表示默认 4 秒"})
	}
	if !validTiming(b.GapS) {
		fe = append(fe, FieldError{Field: "gap_s", Message: "样本间隔为 1～10 秒，0 表示默认 1 秒"})
	}
	if len(fe) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	if err := s.store.PutSetting(probeSettingsKey, b, time.Now()); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.probes.invalidate()
	s.audit(r, AuditEntry{ActorType: "admin", Action: "probe_settings.update", Success: true,
		Details: map[string]any{"interval_s": b.IntervalS, "timeout_s": b.TimeoutS, "gap_s": b.GapS}})
	writeJSON(w, b)
}

// handleGetNodeProbe：GET /api/v1/servers/{id}/probe，admin。节点自己的三网延迟设置。
func (s *Server) handleGetNodeProbe(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	row, err := s.store.GetServer(id)
	if errors.Is(err, errNoServer) {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	} else if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, row.Probe)
}

// handlePutNodeProbe：PUT /api/v1/servers/{id}/probe，admin。随这台节点的下一份上报下发。
func (s *Server) handlePutNodeProbe(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var b NodeProbe
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	var errs []FieldError
	switch b.Mode {
	case probeModeDefault, probeModeOff:
		b.TargetIDs, b.IntervalS, b.TimeoutS, b.GapS = []int64{}, 0, 0, 0
	case probeModeCustom:
		if b.IntervalS != 0 && !slices.Contains(protocol.ProbeIntervals, b.IntervalS) {
			errs = append(errs, FieldError{Field: "interval_s", Message: "测试频率只能是跟随全局，或 1、2、5、10、15、30 分钟"})
		}
		if !validTiming(b.TimeoutS) {
			errs = append(errs, FieldError{Field: "timeout_s", Message: "超时为 1～10 秒，0 表示跟随全局"})
		}
		if !validTiming(b.GapS) {
			errs = append(errs, FieldError{Field: "gap_s", Message: "样本间隔为 1～10 秒，0 表示跟随全局"})
		}
		all, _, err := s.probeBase()
		if err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		ids := []int64{}
		for _, tid := range b.TargetIDs {
			// 【安全】只能选择共用目标或这台节点自己的目标
			if !slices.Contains(ids, tid) && slices.ContainsFunc(all, func(t ProbeTargetRow) bool { return t.ID == tid && (t.ServerID == 0 || t.ServerID == id) }) {
				ids = append(ids, tid)
			}
		}
		if len(ids) != len(b.TargetIDs) {
			errs = append(errs, FieldError{Field: "target_ids", Message: "有目标不存在或重复，请刷新后重试"})
		} else if len(ids) == 0 {
			errs = append(errs, FieldError{Field: "target_ids", Message: "请至少选择一个目标；不测请选“关闭”"})
		} else if len(ids) > protocol.MaxProbeTargets {
			errs = append(errs, FieldError{Field: "target_ids", Message: fmt.Sprintf("每台节点最多 %d 个目标（Agent 的上限）", protocol.MaxProbeTargets)})
		}
		b.TargetIDs = ids
	default:
		errs = append(errs, FieldError{Field: "mode", Message: "模式只能是 default（跟随全局）、custom（自定义）或 off（关闭）"})
	}
	if len(errs) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: errs})
		return
	}
	if err := s.store.SetNodeProbe(id, b); errors.Is(err, errNoServer) {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	} else if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.forgetInterval(id) // 节点设置缓存：下一份上报重新读取并下发
	s.audit(r, AuditEntry{ActorType: "admin", Action: "server.probe", Success: true,
		Details: map[string]any{"server_id": id, "mode": b.Mode, "targets": len(b.TargetIDs), "interval_s": b.IntervalS,
			"timeout_s": b.TimeoutS, "gap_s": b.GapS}})
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
	Status    string                    `json:"status"`     // on / off / unsupported
	Mode      string                    `json:"mode"`       // 节点设置：default / custom / off
	IntervalS int                       `json:"interval_s"` // 节点实际的测试频率（秒）
	Targets   []LatencyTarget           `json:"targets"`
	Series    map[string][]LatencyPoint `json:"series"` // 键为目标 ID
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
	row, err := s.store.GetServer(id)
	if errors.Is(err, errNoServer) {
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
	targets, global, err := s.probeBase()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	active, interval := effectiveProbe(targets, global.IntervalS, row.Probe, id)
	raw, err := s.store.probeSeries(id, since)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	v := LatencyView{Status: s.probeStatus(id), Mode: row.Probe.Mode, IntervalS: interval,
		Targets: []LatencyTarget{}, Series: map[string][]LatencyPoint{}}
	hour := now.Add(-time.Hour).Unix()
	for _, t := range targets {
		pts := raw[t.ID]
		isActive := slices.ContainsFunc(active, func(a ProbeTargetRow) bool { return a.ID == t.ID })
		if !isActive && len(pts) == 0 {
			continue // 这台节点不测、范围内也没有数据的不显示
		}
		lt := LatencyTarget{ProbeTargetRow: t}
		lt.Enabled = isActive // 对这台节点：是否在测
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
