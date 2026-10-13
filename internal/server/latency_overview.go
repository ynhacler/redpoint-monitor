package server

// 测速对比（设计 42.3.5，修订第 137 条）：全部节点到各目标最近 1 小时的延迟与丢包放在一张表中，
// 方便比较哪台 VPS 到哪个运营商线路最好。只读，与节点详情的“测速”使用同样的口径（中位数与平均丢包）。

import (
	"database/sql"
	"net/http"
	"slices"
	"time"
)

// LatencyOverview 是测速对比的数据。
type LatencyOverview struct {
	Servers []LatencyOverviewServer `json:"servers"` // 已安装的节点，按名称排序
	Targets []ProbeTargetRow        `json:"targets"` // 最近 1 小时有结果的目标，按目标列表的顺序
	Cells   []LatencyOverviewCell   `json:"cells"`   // 有结果的（节点, 目标）组合
}

// LatencyOverviewServer 是测速对比中的一台节点。
type LatencyOverviewServer struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Group string `json:"group"`
}

// LatencyOverviewCell 是测速对比的一格：节点到一个目标最近 1 小时的延迟与丢包。
type LatencyOverviewCell struct {
	ServerID int64    `json:"server_id"`
	TargetID int64    `json:"target_id"`
	Latency  *float64 `json:"latency_ms"` // 最近 1 小时成功轮次的中位数；全部失败时为 null
	Loss     float64  `json:"loss"`
}

// buildLatencyOverview 由节点、目标与结果组装对比表（纯函数）：只保留有结果的目标与组合。
func buildLatencyOverview(servers []LatencyOverviewServer, targets []ProbeTargetRow, pts map[[2]int64][]LatencyPoint) LatencyOverview {
	out := LatencyOverview{Servers: servers, Targets: []ProbeTargetRow{}, Cells: []LatencyOverviewCell{}}
	used := map[int64]bool{}
	for _, sv := range servers {
		for _, t := range targets {
			p := pts[[2]int64{sv.ID, t.ID}]
			if len(p) == 0 {
				continue
			}
			lat, loss := summarizeLatency(p)
			out.Cells = append(out.Cells, LatencyOverviewCell{ServerID: sv.ID, TargetID: t.ID, Latency: lat, Loss: round1(*loss)})
			used[t.ID] = true
		}
	}
	for _, t := range targets {
		if used[t.ID] {
			out.Targets = append(out.Targets, t)
		}
	}
	return out
}

// handleLatencyOverview：GET /api/v1/latency-overview，读取类。API 密钥只看到其范围内的节点。
func (s *Server) handleLatencyOverview(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListServers()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	rows = slices.DeleteFunc(rows, func(row ServerRow) bool { return row.EnrollState == enrollPending || !scopeAllows(r, row) })
	servers := make([]LatencyOverviewServer, 0, len(rows))
	allowed := map[int64]bool{}
	for _, row := range rows {
		servers = append(servers, LatencyOverviewServer{ID: row.ID, Name: row.Name, Group: row.Group})
		allowed[row.ID] = true
	}
	targets, err := s.store.ListProbeTargets()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	pts, err := s.store.recentProbePoints(time.Now().Add(-time.Hour), allowed)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, buildLatencyOverview(servers, targets, pts))
}

// recentProbePoints 读取 since 以来的结果，按（节点, 目标）分组；只保留 allowed 中的节点。
func (s *Store) recentProbePoints(since time.Time, allowed map[int64]bool) (map[[2]int64][]LatencyPoint, error) {
	rows, err := s.DB.Query(`SELECT server_id, target_id, ts, latency_ms, loss FROM probe_results WHERE ts >= ?`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[[2]int64][]LatencyPoint{}
	for rows.Next() {
		var sid, tid int64
		var p LatencyPoint
		var lat sql.NullFloat64
		if err := rows.Scan(&sid, &tid, &p.TS, &lat, &p.Loss); err != nil {
			return nil, err
		}
		if !allowed[sid] {
			continue
		}
		if lat.Valid {
			v := lat.Float64
			p.Latency = &v
		}
		k := [2]int64{sid, tid}
		out[k] = append(out[k], p)
	}
	return out, rows.Err()
}
