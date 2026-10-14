package server

// 国内探针与被墙检测（设计 42.2、42.3.1，修订第 154 条）。
//
// 用户把位于国内的一台或几台节点设为“国内探针”（标出运营商），探针的 Agent 每轮探测其他全部节点的公网 IPv4 / IPv6：
// ICMP（可选）与指定的 TCP 端口（默认 22、443）。探测项借用三网延迟的下发通道：编码为特殊的目标 ID
// （reachBase 以上，见 reachTargetID），旧版 Agent 当作普通目标照常探测，结果随上报带回后写入 reach_results。
//
// 判定（设计 42.2.2）：每轮按探测项得出状态（正常 / IP 疑似被封 / 端口疑似被封 / 不稳定）；同一轮探针对所有节点都失败时
// 视为探针故障，这一轮不计；连续 3 轮异常才判定，连续 2 轮正常才恢复；节点离线时不判定为被墙。结论统一表述为“疑似”。
//
// 【安全】探测地址只取节点注册时记录的公网地址（PublicProbeAddr），不接受用户输入的地址；Agent 端的数量与频率上限不变（设计 42.6）。

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"vpsmon/internal/protocol"
)

const (
	// reachBase 以上的目标 ID 是国内探针的探测项：reachBase + 节点 ID << 18 + IPv6 << 17 + 端口（0 为 ICMP）
	reachBase int64 = 1 << 40

	reachSettingsKey = "reach_probes"
	maxReachProbes   = 10 // 最多 10 台探针
	maxReachPorts    = 3  // 每台探针最多 3 个 TCP 端口

	reachBadRounds    = 3             // 连续 3 轮异常才判定（约 15 分钟）
	reachGoodRounds   = 2             // 连续 2 轮正常才恢复
	reachUnstableLoss = 30.0          // 平均丢包率超过 30% 视为线路不稳定（设计 42.2.2）
	reachWindow       = 2 * time.Hour // 判定读取的时间窗口
	reachTargetsTTL   = time.Minute   // 探测项（节点地址）缓存时间
)

// reachTargetID 编码一个探测项。
func reachTargetID(sid int64, v6 bool, port int) int64 {
	id := reachBase + sid<<18 + int64(port)
	if v6 {
		id += 1 << 17
	}
	return id
}

// decodeReachID 解码探测项；不是探测项时 ok 为 false。
func decodeReachID(id int64) (sid int64, v6 bool, port int, ok bool) {
	if id < reachBase {
		return 0, false, 0, false
	}
	x := id - reachBase
	sid, v6, port = x>>18, x&(1<<17) != 0, int(x&0xffff)
	return sid, v6, port, sid > 0 && x&(1<<16) == 0
}

// ReachProbe 是一台国内探针的设置。
type ReachProbe struct {
	ServerID int64  `json:"server_id"`
	ISP      string `json:"isp"`   // ct / cu / cm；空表示未标明
	Ports    []int  `json:"ports"` // TCP 端口，最多 3 个
	ICMP     bool   `json:"icmp"`  // 是否 Ping（Agent 需要 CAP_NET_RAW，没有时这项记为 icmp_unavailable、不参与判定）
}

// reachState 缓存探针设置与各探针的探测项。
type reachState struct {
	mu      sync.Mutex
	loaded  bool
	probes  map[int64]ReachProbe
	targets map[int64][]protocol.ProbeTarget
	builtAt time.Time
}

func (s *Server) reachProbes() (map[int64]ReachProbe, error) {
	p := &s.reach
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.loaded {
		return p.probes, nil
	}
	var list []ReachProbe
	if _, err := s.store.GetSetting(reachSettingsKey, &list); err != nil {
		return nil, err
	}
	p.probes = map[int64]ReachProbe{}
	for _, rp := range list {
		p.probes[rp.ServerID] = rp
	}
	p.loaded, p.targets = true, nil
	return p.probes, nil
}

// invalidateReach 在探针设置或节点变化后调用。
func (s *Server) invalidateReach() {
	s.reach.mu.Lock()
	s.reach.loaded, s.reach.targets = false, nil
	s.reach.mu.Unlock()
}

// buildReachTargets 计算探针 probe 的探测项（纯函数）：其他已安装节点的公网 IPv4 / IPv6 × ICMP 与各端口，
// 按节点 ID 顺序，最多 limit 项。
func buildReachTargets(rows []ServerRow, probe ReachProbe, limit int) []protocol.ProbeTarget {
	var out []protocol.ProbeTarget
	add := func(t protocol.ProbeTarget) bool {
		if len(out) >= limit {
			return false
		}
		out = append(out, t)
		return true
	}
	sorted := slices.Clone(rows)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, row := range sorted {
		if row.ID == probe.ServerID || row.EnrollState == enrollPending {
			continue
		}
		for _, ip := range []string{row.IPv4, row.IPv6} {
			a, err := netip.ParseAddr(ip)
			if err != nil || !protocol.PublicProbeAddr(a) {
				continue
			}
			v6 := a.Unmap().Is6()
			if probe.ICMP && !add(protocol.ProbeTarget{ID: reachTargetID(row.ID, v6, 0), Proto: "icmp", Addr: a}) {
				return out
			}
			for _, port := range probe.Ports {
				if !add(protocol.ProbeTarget{ID: reachTargetID(row.ID, v6, port), Proto: "tcp", Addr: a, Port: port}) {
					return out
				}
			}
		}
	}
	return out
}

// reachTargets 返回节点 sid 作为探针时的探测项；不是探针时为 nil。
func (s *Server) reachTargets(sid int64, now time.Time) ([]protocol.ProbeTarget, error) {
	probes, err := s.reachProbes()
	if err != nil {
		return nil, err
	}
	if _, ok := probes[sid]; !ok {
		return nil, nil
	}
	s.reach.mu.Lock()
	if s.reach.targets != nil && now.Sub(s.reach.builtAt) < reachTargetsTTL {
		t := s.reach.targets[sid]
		s.reach.mu.Unlock()
		return t, nil
	}
	s.reach.mu.Unlock()
	rows, err := s.store.ListServers()
	if err != nil {
		return nil, err
	}
	all := map[int64][]protocol.ProbeTarget{}
	for id, p := range probes {
		all[id] = buildReachTargets(rows, p, protocol.MaxProbeTargets)
	}
	s.reach.mu.Lock()
	s.reach.targets, s.reach.builtAt = all, now
	s.reach.mu.Unlock()
	return all[sid], nil
}

// insertReachResult 保存一项探测结果（在上报的批量写入事务中）。
func insertReachResult(tx *sql.Tx, probeID int64, r protocol.ProbeResult, ts int64, lat any, code string) error {
	sid, v6, port, ok := decodeReachID(r.TargetID)
	if !ok {
		return nil
	}
	fam := 4
	if v6 {
		fam = 6
	}
	_, err := tx.Exec(`INSERT INTO reach_results (probe_id, server_id, family, port, ts, latency_ms, loss, error) VALUES (?,?,?,?,?,?,?,?)`,
		probeID, sid, fam, port, ts, lat, r.Loss, code)
	return err
}

// ---- 判定 ----

// reachCheck 是一轮中一个探测项的结果。
type reachCheck struct {
	Family  int      `json:"family"` // 4 / 6
	Port    int      `json:"port"`   // 0 为 ICMP
	Latency *float64 `json:"latency_ms"`
	Loss    float64  `json:"loss"`
	Error   string   `json:"error"`
}

// 一轮的状态，按严重程度排列
const (
	reachOK       = "ok"       // 国内可达
	reachUnstable = "unstable" // 丢包 > 30%：疑似干扰 / 限速
	reachPort     = "port"     // ICMP 正常、部分 TCP 端口失败：端口疑似被封
	reachBlocked  = "blocked"  // ICMP 与所有 TCP 端口都失败：IP 疑似被封
)

var reachSeverity = map[string]int{reachOK: 0, reachUnstable: 1, reachPort: 2, reachBlocked: 3}

// failed 表示这一项全部失败；Agent 没有 ICMP 权限的项不算失败，也不参与判定。
func (c reachCheck) failed() bool { return c.Loss >= 100 && c.Error != "icmp_unavailable" }

func (c reachCheck) usable() bool { return c.Error != "icmp_unavailable" }

// roundState 判定一轮（纯函数）：返回状态与失败的端口。
func roundState(checks []reachCheck) (string, []int) {
	var usable, failed int
	icmpOK := false
	var lossSum float64
	var badPorts []int
	for _, c := range checks {
		if !c.usable() {
			continue
		}
		usable++
		lossSum += c.Loss
		if c.failed() {
			failed++
			if c.Port > 0 && !slices.Contains(badPorts, c.Port) {
				badPorts = append(badPorts, c.Port)
			}
		} else if c.Port == 0 {
			icmpOK = true
		}
	}
	slices.Sort(badPorts)
	switch {
	case usable == 0:
		return "", nil
	case failed == usable:
		return reachBlocked, badPorts
	case len(badPorts) > 0 && (icmpOK || failed < usable):
		return reachPort, badPorts
	case lossSum/float64(usable) > reachUnstableLoss:
		return reachUnstable, nil
	}
	return reachOK, nil
}

// reachRound 是一台探针对一个节点的一轮。
type reachRound struct {
	TS     int64
	Checks []reachCheck
}

// reachTrack 是一台探针对一个节点的判定结果。
type reachTrack struct {
	Status   string       // 带防抖的状态；还没有足够轮次时为 ok
	Since    int64        // 进入当前状态的时间（轮次开始时间）
	BadPorts []int        // 端口疑似被封时的端口
	Last     []reachCheck // 最近一轮
	LastAt   int64
}

// trackReach 按时间顺序推进防抖（纯函数）：连续 3 轮异常进入异常（取最近一轮的异常类型），连续 2 轮正常恢复。
// faulty 中的轮次（探针故障）跳过。
func trackReach(rounds []reachRound, faulty map[int64]bool) reachTrack {
	t := reachTrack{Status: reachOK}
	bad, good := 0, 0
	for _, r := range rounds {
		if faulty[r.TS] {
			continue
		}
		st, ports := roundState(r.Checks)
		if st == "" {
			continue
		}
		t.Last, t.LastAt = r.Checks, r.TS
		if st == reachOK {
			good++
			bad = 0
			if t.Status != reachOK && good >= reachGoodRounds {
				t.Status, t.Since, t.BadPorts = reachOK, r.TS, nil
			}
			continue
		}
		bad++
		good = 0
		if bad >= reachBadRounds && (t.Status != st || !slices.Equal(t.BadPorts, ports)) {
			if t.Status == reachOK || t.Status != st {
				t.Since = r.TS
			}
			t.Status, t.BadPorts = st, ports
		}
	}
	if t.Since == 0 && len(rounds) > 0 {
		t.Since = rounds[0].TS
	}
	return t
}

// reachRow 是 reach_results 的一行。
type reachRow struct {
	ProbeID, ServerID int64
	reachCheck
	TS int64
}

// faultyRounds 找出探针故障的轮次（纯函数）：同一台探针、同一轮中，对所有节点的可用探测项全部失败。
// 只有一个被测节点时无法区分，不认定为探针故障。
func faultyRounds(rows []reachRow) map[int64]map[int64]bool {
	type key struct{ probe, ts int64 }
	servers := map[key]map[int64]bool{}
	anyOK := map[key]bool{}
	for _, r := range rows {
		k := key{r.ProbeID, r.TS}
		if servers[k] == nil {
			servers[k] = map[int64]bool{}
		}
		servers[k][r.ServerID] = true
		if r.usable() && !r.failed() {
			anyOK[k] = true
		}
	}
	out := map[int64]map[int64]bool{}
	for k, ss := range servers {
		if len(ss) >= 2 && !anyOK[k] {
			if out[k.probe] == nil {
				out[k.probe] = map[int64]bool{}
			}
			out[k.probe][k.ts] = true
		}
	}
	return out
}

// groupReach 把结果按（探针, 节点）分组为按时间排序的轮次（纯函数）。
func groupReach(rows []reachRow) map[[2]int64][]reachRound {
	tmp := map[[2]int64]map[int64][]reachCheck{}
	for _, r := range rows {
		k := [2]int64{r.ProbeID, r.ServerID}
		if tmp[k] == nil {
			tmp[k] = map[int64][]reachCheck{}
		}
		tmp[k][r.TS] = append(tmp[k][r.TS], r.reachCheck)
	}
	out := map[[2]int64][]reachRound{}
	for k, byTS := range tmp {
		rounds := make([]reachRound, 0, len(byTS))
		for ts, cs := range byTS {
			sort.Slice(cs, func(i, j int) bool {
				if cs[i].Family != cs[j].Family {
					return cs[i].Family < cs[j].Family
				}
				return cs[i].Port < cs[j].Port
			})
			rounds = append(rounds, reachRound{ts, cs})
		}
		sort.Slice(rounds, func(i, j int) bool { return rounds[i].TS < rounds[j].TS })
		out[k] = rounds
	}
	return out
}

func (s *Store) reachRows(since time.Time, serverID int64) ([]reachRow, error) {
	q := `SELECT probe_id, server_id, family, port, ts, latency_ms, loss, error FROM reach_results WHERE ts >= ?`
	args := []any{since.Unix()}
	if serverID > 0 {
		q += ` AND server_id = ?`
		args = append(args, serverID)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []reachRow
	for rows.Next() {
		var r reachRow
		var lat sql.NullFloat64
		if err := rows.Scan(&r.ProbeID, &r.ServerID, &r.Family, &r.Port, &r.TS, &lat, &r.Loss, &r.Error); err != nil {
			return nil, err
		}
		if lat.Valid {
			v := lat.Float64
			r.Latency = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// 运营商名称（与三网延迟一致）
var reachISPNames = map[string]string{"ct": "电信", "cu": "联通", "cm": "移动"}

// reachVerdict 是一个节点的国内可达性结论。
type reachVerdict struct {
	Status string // ok / unstable / port / partial / blocked / offline；没有探针数据时为空
	Text   string // 如“国内不可达（疑似被墙）”“移动不可达”“端口 443 国内不可达”
	Level  float64
}

// verdictFor 汇总各探针的结果为一个节点的结论（纯函数）。offline 为节点当前离线（不判定为被墙）。
func verdictFor(tracks map[int64]reachTrack, probes map[int64]ReachProbe, names map[int64]string, offline bool) reachVerdict {
	if len(tracks) == 0 {
		return reachVerdict{}
	}
	if offline {
		return reachVerdict{Status: "offline", Text: "节点离线（不判定国内可达性）"}
	}
	ids := make([]int64, 0, len(tracks))
	for id := range tracks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	label := func(id int64) string {
		if n := reachISPNames[probes[id].ISP]; n != "" {
			return n
		}
		return names[id]
	}
	var blocked, unstable []string
	var ports []int
	for _, id := range ids {
		switch t := tracks[id]; t.Status {
		case reachBlocked:
			blocked = append(blocked, label(id))
		case reachPort:
			for _, p := range t.BadPorts {
				if !slices.Contains(ports, p) {
					ports = append(ports, p)
				}
			}
		case reachUnstable:
			unstable = append(unstable, label(id))
		}
	}
	slices.Sort(ports)
	switch {
	case len(blocked) == len(ids):
		return reachVerdict{Status: reachBlocked, Text: "国内不可达（疑似被墙）", Level: 3}
	case len(blocked) > 0:
		return reachVerdict{Status: "partial", Text: strings.Join(dedupe(blocked), "、") + "不可达", Level: 2}
	case len(ports) > 0:
		ps := make([]string, len(ports))
		for i, p := range ports {
			ps[i] = fmt.Sprint(p)
		}
		return reachVerdict{Status: reachPort, Text: "端口 " + strings.Join(ps, "、") + " 国内不可达", Level: 2}
	case len(unstable) > 0:
		return reachVerdict{Status: reachUnstable, Text: "国内线路不稳定", Level: 1}
	}
	return reachVerdict{Status: reachOK, Text: "国内可达"}
}

func dedupe(xs []string) []string {
	var out []string
	for _, x := range xs {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// reachAll 计算所有节点的探针结果：节点 ID → 探针 ID → 判定。
func (s *Server) reachAll(now time.Time, serverID int64) (map[int64]map[int64]reachTrack, error) {
	probes, err := s.reachProbes()
	if err != nil || len(probes) == 0 {
		return nil, err
	}
	rows, err := s.store.reachRows(now.Add(-reachWindow), serverID)
	if err != nil {
		return nil, err
	}
	// 探针故障要看同一轮对所有节点的结果：只查一个节点时另外读这些探针的全部结果
	faultRows := rows
	if serverID > 0 {
		if faultRows, err = s.store.reachRows(now.Add(-reachWindow), 0); err != nil {
			return nil, err
		}
	}
	faulty := faultyRounds(faultRows)
	out := map[int64]map[int64]reachTrack{}
	for k, rounds := range groupReach(rows) {
		probe, sid := k[0], k[1]
		if _, ok := probes[probe]; !ok {
			continue // 已不再是探针
		}
		if out[sid] == nil {
			out[sid] = map[int64]reachTrack{}
		}
		out[sid][probe] = trackReach(rounds, faulty[probe])
	}
	return out, nil
}

// ---- 接口 ----

type reachProbeView struct {
	ReachProbe
	Name   string `json:"name"`
	Status string `json:"status"` // 探针 Agent 的探测状态：on / off / unsupported
	Count  int    `json:"count"`  // 下发的探测项数
}

// handleReachProbes：GET /api/v1/reach-probes，admin。
func (s *Server) handleReachProbes(w http.ResponseWriter, r *http.Request) {
	probes, err := s.reachProbes()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	rows, err := s.store.ListServers()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	names := map[int64]string{}
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	items := []reachProbeView{}
	for _, p := range probes {
		items = append(items, reachProbeView{ReachProbe: p, Name: names[p.ServerID], Status: s.probeStatus(p.ServerID),
			Count: len(buildReachTargets(rows, p, protocol.MaxProbeTargets))})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ServerID < items[j].ServerID })
	writeList(w, items, "", map[string]any{"max_checks": protocol.MaxProbeTargets})
}

// handlePutReachProbes：PUT /api/v1/reach-probes，admin。整体替换探针列表，随探针的下一份上报下发。
func (s *Server) handlePutReachProbes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Probes []ReachProbe `json:"probes"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	rows, err := s.store.ListServers()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	enrolled := map[int64]bool{}
	for _, row := range rows {
		enrolled[row.ID] = row.EnrollState != enrollPending
	}
	var fe []FieldError
	bad := func(i int, f, m string) {
		fe = append(fe, FieldError{Field: fmt.Sprintf("probes[%d].%s", i, f), Message: m})
	}
	if len(body.Probes) > maxReachProbes {
		fe = append(fe, FieldError{Field: "probes", Message: fmt.Sprintf("最多 %d 台探针", maxReachProbes)})
	}
	seen := map[int64]bool{}
	for i := range body.Probes {
		p := &body.Probes[i]
		if !enrolled[p.ServerID] || seen[p.ServerID] {
			bad(i, "server_id", "节点不存在、未安装或重复")
		}
		seen[p.ServerID] = true
		if _, ok := reachISPNames[p.ISP]; !ok && p.ISP != "" {
			bad(i, "isp", "运营商应为 ct、cu、cm 或留空")
		}
		if p.Ports == nil {
			p.Ports = []int{}
		}
		slices.Sort(p.Ports)
		p.Ports = slices.Compact(p.Ports)
		if len(p.Ports) > maxReachPorts {
			bad(i, "ports", fmt.Sprintf("最多 %d 个端口", maxReachPorts))
		}
		for _, port := range p.Ports {
			if port < 1 || port > 65535 {
				bad(i, "ports", "端口应在 1～65535 之间")
				break
			}
		}
		if !p.ICMP && len(p.Ports) == 0 {
			bad(i, "ports", "至少选择 Ping 或一个端口")
		}
	}
	if len(fe) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	if body.Probes == nil {
		body.Probes = []ReachProbe{}
	}
	if err := s.store.PutSetting(reachSettingsKey, body.Probes, time.Now()); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.invalidateReach()
	s.probes.invalidate() // 探针的下发列表随之变化
	s.audit(r, AuditEntry{ActorType: "admin", Action: "reach.update", TargetType: "settings", Success: true,
		Details: map[string]any{"probes": len(body.Probes)}})
	s.handleReachProbes(w, r)
}

type reachProbeResult struct {
	ProbeID  int64        `json:"probe_id"`
	Name     string       `json:"name"`
	ISP      string       `json:"isp"`
	Status   string       `json:"status"`
	Since    int64        `json:"since"`
	BadPorts []int        `json:"bad_ports"`
	LastAt   int64        `json:"last_at"`
	Checks   []reachCheck `json:"checks"`
}

// handleServerReach：GET /api/v1/servers/{id}/reach，读取类。国内可达性：结论、各探针的状态与最近一轮、24 小时延迟曲线。
func (s *Server) handleServerReach(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	row, err := s.store.GetServer(id)
	if err != nil || !scopeAllows(r, *row) {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	}
	now := time.Now()
	probes, err := s.reachProbes()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	all, err := s.reachAll(now, id)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	rows, err := s.store.ListServers()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	names := map[int64]string{}
	for _, x := range rows {
		names[x.ID] = x.Name
	}
	tracks := all[id]
	v := verdictFor(tracks, probes, names, s.statusOf(*row, now) == "offline")
	items := []reachProbeResult{}
	for pid, t := range tracks {
		items = append(items, reachProbeResult{ProbeID: pid, Name: names[pid], ISP: probes[pid].ISP, Status: t.Status, Since: t.Since,
			BadPorts: nonNil(t.BadPorts), LastAt: t.LastAt, Checks: t.Last})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ProbeID < items[j].ProbeID })
	series, err := s.store.reachSeries(id, now.Add(-24*time.Hour))
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, map[string]any{"configured": len(probes) > 0, "status": v.Status, "text": v.Text, "probes": items, "series": series})
}

func nonNil(xs []int) []int {
	if xs == nil {
		return []int{}
	}
	return xs
}

// reachSeries 返回各探针到节点的延迟曲线（国内方向，设计 42.3.1）：每轮取 IPv4 上第一个成功项（优先 ICMP）的延迟，
// 丢包取本轮可用项的平均。键为探针 ID（字符串，JSON 对象键）。
func (s *Store) reachSeries(serverID int64, since time.Time) (map[string][]LatencyPoint, error) {
	rows, err := s.reachRows(since, serverID)
	if err != nil {
		return nil, err
	}
	out := map[string][]LatencyPoint{}
	for k, rounds := range groupReach(rows) {
		key := fmt.Sprint(k[0])
		for _, rd := range rounds {
			var lat *float64
			var loss float64
			n := 0
			for _, c := range rd.Checks { // 已按协议族、端口排序：IPv4 的 ICMP 在前
				if !c.usable() {
					continue
				}
				n++
				loss += c.Loss
				if lat == nil && c.Latency != nil && c.Family == 4 {
					lat = c.Latency
				}
			}
			if n == 0 {
				continue
			}
			out[key] = append(out[key], LatencyPoint{TS: rd.TS, Latency: lat, Loss: loss / float64(n)})
		}
	}
	return out, nil
}
