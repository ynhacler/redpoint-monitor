package server

// 回程线路识别（设计 42.5，修订第 282 条）：面板在节点的三网延迟目标中为电信、联通、移动各选一个，
// 随目标列表下发给 Agent 做路由追踪（X-Probe-Trace）；Agent 每 6 小时追踪一次，面板按经过的骨干网地址段判断线路类型。
// 识别规则是随面板发布的离线地址段（下面的 routeNets），不调用任何外部接口；识别结果只作为建议，
// 节点上的线路标签仍由用户决定（可在节点详情中一键采用）。

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"slices"
	"time"

	"vpsmon/internal/protocol"
)

// routeNet 是一个用于识别线路的骨干网地址段。
type routeNet struct {
	prefix netip.Prefix
	asn    int
	name   string // 显示在逐跳列表中的网络名称
}

func mustPrefix(s string) netip.Prefix { return netip.MustParsePrefix(s) }

// routeNets 是三网骨干与国际出口的典型地址段（设计 42.5 的表格）；同一地址按最长前缀匹配。
// 规则随面板版本更新，不需要升级 Agent。
var routeNets = []routeNet{
	{mustPrefix("59.43.0.0/16"), 4809, "电信 CN2"},
	{mustPrefix("202.97.0.0/16"), 4134, "电信 163 骨干"},
	{mustPrefix("219.158.0.0/16"), 4837, "联通 4837 骨干"},
	{mustPrefix("218.105.0.0/16"), 9929, "联通 9929"},
	{mustPrefix("210.51.0.0/16"), 9929, "联通 9929"},
	{mustPrefix("223.120.16.0/24"), 58807, "移动 CMIN2"},
	{mustPrefix("223.120.17.0/24"), 58807, "移动 CMIN2"},
	{mustPrefix("223.120.19.0/24"), 58807, "移动 CMIN2"},
	{mustPrefix("223.120.130.0/24"), 58807, "移动 CMIN2"},
	{mustPrefix("223.120.131.0/24"), 58807, "移动 CMIN2"},
	{mustPrefix("223.120.140.0/24"), 58807, "移动 CMIN2"},
	{mustPrefix("223.120.141.0/24"), 58807, "移动 CMIN2"},
	{mustPrefix("223.118.0.0/15"), 58453, "移动 CMI"},
	{mustPrefix("223.120.0.0/15"), 58453, "移动 CMI"},
	{mustPrefix("221.183.0.0/16"), 9808, "移动骨干"},
}

// lookupRouteNet 返回地址所属的骨干网地址段（最长前缀匹配）；不属于任何一段时返回 false。
func lookupRouteNet(a netip.Addr) (routeNet, bool) {
	var best routeNet
	found := false
	for _, n := range routeNets {
		if n.prefix.Contains(a) && (!found || n.prefix.Bits() > best.prefix.Bits()) {
			best, found = n, true
		}
	}
	return best, found
}

// classifyByISP 按某个运营商的规则判断线路；不符合时返回空。
func classifyByISP(isp string, seen map[int]bool) string {
	switch isp {
	case "ct":
		switch {
		case seen[4809] && !seen[4134]:
			return "CN2 GIA"
		case seen[4809]:
			return "CN2 GT"
		case seen[4134]:
			return "163"
		}
	case "cu":
		switch {
		case seen[9929]:
			return "9929"
		case seen[4837]:
			return "4837"
		}
	case "cm":
		switch {
		case seen[58807]:
			return "CMIN2"
		case seen[58453]:
			return "CMI"
		case seen[9808]:
			return "CMNET"
		}
	}
	return ""
}

// classifyRoute 根据逐跳地址判断到某运营商目标的回程线路（纯函数）：先按目标运营商的规则，
// 不符合时再按其他运营商的规则（如到电信的回程经过移动 CMI 出境）；都不符合时返回空（未识别）。
func classifyRoute(isp string, hops []netip.Addr) string {
	seen := map[int]bool{}
	for _, a := range hops {
		if n, ok := lookupRouteNet(a); ok {
			seen[n.asn] = true
		}
	}
	if line := classifyByISP(isp, seen); line != "" {
		return line
	}
	for _, other := range []string{"ct", "cu", "cm"} {
		if other != isp {
			if line := classifyByISP(other, seen); line != "" {
				return line
			}
		}
	}
	return ""
}

// traceISPs 是要追踪的运营商，依次为电信、联通、移动。
var traceISPs = []string{"ct", "cu", "cm"}

// pickTraceTargets 从节点实际下发的目标中为每个运营商选一个做路由追踪（纯函数）：
// 按目标顺序取第一个 IPv4 或域名目标，没有时取 IPv6 目标；ok 为空时不检查是否已下发。
func pickTraceTargets(targets []ProbeTargetRow, ok func(id int64) bool) []ProbeTargetRow {
	var out []ProbeTargetRow
	for _, isp := range traceISPs {
		var v6 *ProbeTargetRow
		var pick *ProbeTargetRow
		for i := range targets {
			t := &targets[i]
			if t.ISP != isp || (ok != nil && !ok(t.ID)) {
				continue
			}
			if a, err := netip.ParseAddr(t.Address); err == nil && a.Is6() && !a.Is4In6() {
				if v6 == nil {
					v6 = t
				}
				continue
			}
			pick = t
			break
		}
		if pick == nil {
			pick = v6
		}
		if pick != nil {
			out = append(out, *pick)
		}
	}
	return out
}

// ---- 存储 ----

// 上报中最多保存几条追踪结果（Agent 每次最多 3 条，补发时可能多一些）
const maxTracesRep = 2 * protocol.MaxTraceTargets

// saveRouteTraces 在批量写入的事务中保存一份上报带回的路由追踪，每个目标只保留最近一次。
// 时间超前过多（时钟偏差）时用收到时间；跳数、地址与耗时不合法的部分丢弃。
func saveRouteTraces(tx *sql.Tx, serverID int64, list []protocol.TraceResult, seen time.Time) error {
	if len(list) > maxTracesRep {
		list = list[:maxTracesRep]
	}
	for _, r := range list {
		if r.TargetID <= 0 {
			continue
		}
		ts := r.At
		if ts > seen.Add(maxSkew).Unix() || ts <= 0 {
			ts = seen.Unix()
		}
		hops := cleanHops(r.Hops)
		raw, _ := json.Marshal(hops)
		code := r.Error
		if len(code) > 32 {
			code = code[:32]
		}
		if _, err := tx.Exec(`INSERT INTO route_traces (server_id, target_id, ts, reached, hops, error) VALUES (?,?,?,?,?,?)
			ON CONFLICT(server_id, target_id) DO UPDATE SET ts = excluded.ts, reached = excluded.reached,
			hops = excluded.hops, error = excluded.error WHERE excluded.ts >= route_traces.ts`,
			serverID, r.TargetID, ts, r.Reached, string(raw), code); err != nil {
			return err
		}
	}
	return nil
}

// cleanHops 只保留合法的跳：TTL 1～30 且递增，地址可解析，耗时 0～60 秒。
func cleanHops(in []protocol.TraceHop) []protocol.TraceHop {
	out := []protocol.TraceHop{}
	last := 0
	for _, h := range in {
		if h.TTL <= last || h.TTL > protocol.MaxTraceHops {
			continue
		}
		last = h.TTL
		c := protocol.TraceHop{TTL: h.TTL}
		if a, err := netip.ParseAddr(h.IP); err == nil {
			c.IP = a.Unmap().String()
			if h.RTT != nil && *h.RTT >= 0 && *h.RTT < 60000 {
				v := *h.RTT
				c.RTT = &v
			}
		}
		out = append(out, c)
	}
	return out
}

// routeTraceRow 是保存的一次追踪。
type routeTraceRow struct {
	TargetID int64
	TS       int64
	Reached  bool
	Hops     []protocol.TraceHop
	Error    string
}

func (s *Store) routeTraces(serverID int64) (map[int64]routeTraceRow, error) {
	rows, err := s.DB.Query(`SELECT target_id, ts, reached, hops, error FROM route_traces WHERE server_id = ?`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]routeTraceRow{}
	for rows.Next() {
		var r routeTraceRow
		var raw string
		if err := rows.Scan(&r.TargetID, &r.TS, &r.Reached, &raw, &r.Error); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(raw), &r.Hops)
		out[r.TargetID] = r
	}
	return out, rows.Err()
}

// ---- 接口 ----

// RouteHopView 是逐跳列表中的一跳；Net 为所属骨干网（识别规则中的地址段）。
type RouteHopView struct {
	TTL int      `json:"ttl"`
	IP  string   `json:"ip,omitempty"`
	RTT *float64 `json:"rtt_ms,omitempty"`
	ASN int      `json:"asn,omitempty"`
	Net string   `json:"net,omitempty"`
}

// RouteView 是到一个运营商目标的回程线路。At 为 0 表示尚未收到追踪结果。
type RouteView struct {
	TargetID int64          `json:"target_id"`
	ISP      string         `json:"isp"`
	Province string         `json:"province"`
	Address  string         `json:"address"`
	At       int64          `json:"at"`
	Reached  bool           `json:"reached"`
	Line     string         `json:"line"`
	Error    string         `json:"error"`
	Hops     []RouteHopView `json:"hops"`
}

// RoutesView 是 GET /servers/{id}/routes 的响应：Lines 为识别出的线路（去重，按电信、联通、移动排列）。
type RoutesView struct {
	Status string      `json:"status"` // 同三网延迟：on / off / unsupported
	Items  []RouteView `json:"items"`
	Lines  []string    `json:"lines"`
}

func (s *Server) handleRoutes(w http.ResponseWriter, r *http.Request) {
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
	all, global, err := s.probeBase()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	saved, err := s.store.routeTraces(id)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	active, _ := effectiveProbe(all, global.IntervalS, row.Probe, id)
	v := RoutesView{Status: s.probeStatus(id), Items: []RouteView{}, Lines: []string{}}
	picked := pickTraceTargets(active, nil)
	// 已不再追踪、但还有记录的目标也显示（如改了三网延迟目标），排在后面
	for _, t := range all {
		if _, ok := saved[t.ID]; ok && ispNames[t.ISP] != "" && t.ISP != "intl" &&
			!slices.ContainsFunc(picked, func(p ProbeTargetRow) bool { return p.ID == t.ID }) {
			picked = append(picked, t)
		}
	}
	for _, t := range picked {
		rv := RouteView{TargetID: t.ID, ISP: t.ISP, Province: t.Province, Address: t.Address, Hops: []RouteHopView{}}
		if tr, ok := saved[t.ID]; ok {
			rv.At, rv.Reached, rv.Error = tr.TS, tr.Reached, tr.Error
			var addrs []netip.Addr
			for _, h := range tr.Hops {
				hv := RouteHopView{TTL: h.TTL, IP: h.IP, RTT: h.RTT}
				if a, err := netip.ParseAddr(h.IP); err == nil {
					addrs = append(addrs, a)
					if n, ok := lookupRouteNet(a); ok {
						hv.ASN, hv.Net = n.asn, n.name
					}
				}
				rv.Hops = append(rv.Hops, hv)
			}
			rv.Line = classifyRoute(t.ISP, addrs)
			if rv.Line != "" && !slices.Contains(v.Lines, rv.Line) {
				v.Lines = append(v.Lines, rv.Line)
			}
		}
		v.Items = append(v.Items, rv)
	}
	writeJSON(w, v)
}
