package server

// 三网延迟告警（设计 42.3.4）：告警引擎每分钟从 probe_results 汇总各节点最近 30 分钟到国内目标的结果，
// 作为“三网延迟”“三网丢包”两条规则的值。境外目标不参与（它们反映的不是到国内的回程质量）。

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"
)

// probeAlertWindow 是汇总的时间窗口：默认 5 分钟一轮时约 6 轮，单轮波动不会直接触发
const probeAlertWindow = 30 * time.Minute

// probeAlertView 是一个节点最近一段时间到国内目标的汇总。
type probeAlertView struct {
	// Latency 是各目标延迟中位数的中位数（典型延迟）；所有目标都不可达时为 nil
	Latency       *float64
	LatencyDetail string // 如 “最慢：上海电信 320 ms”
	// Loss 是各目标平均丢包率的平均值（%）：个别目标失效只拉高一部分，不会单独让整台节点告警
	Loss       float64
	LossDetail string // 如 “丢包最多：上海电信 100%”
}

// probeAlertRow 是窗口内的一条探测结果。
type probeAlertRow struct {
	ServerID int64
	TargetID int64
	Name     string // 如 “上海电信”
	LatencyPoint
}

// summarizeProbeAlerts 按节点汇总窗口内的结果（纯函数，便于表测试）。
func summarizeProbeAlerts(rows []probeAlertRow) map[int64]probeAlertView {
	type target struct {
		name string
		pts  []LatencyPoint
	}
	byNode := map[int64]map[int64]*target{}
	for _, r := range rows {
		ts := byNode[r.ServerID]
		if ts == nil {
			ts = map[int64]*target{}
			byNode[r.ServerID] = ts
		}
		t := ts[r.TargetID]
		if t == nil {
			t = &target{name: r.Name}
			ts[r.TargetID] = t
		}
		t.pts = append(t.pts, r.LatencyPoint)
	}
	out := make(map[int64]probeAlertView, len(byNode))
	for sid, ts := range byNode {
		// 按目标 ID 排序，同样的数据得到同样的说明
		ids := make([]int64, 0, len(ts))
		for id := range ts {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

		var v probeAlertView
		var lats []LatencyPoint // 借用 summarizeLatency 求中位数：每个目标一个点
		slowest, slowestName := -1.0, ""
		lossSum, worstLoss, worstName := 0.0, -1.0, ""
		for _, id := range ids {
			t := ts[id]
			lat, loss := summarizeLatency(t.pts)
			lossSum += *loss
			if *loss > worstLoss {
				worstLoss, worstName = *loss, t.name
			}
			if lat != nil {
				lats = append(lats, LatencyPoint{Latency: lat})
				if *lat > slowest {
					slowest, slowestName = *lat, t.name
				}
			}
		}
		v.Loss = lossSum / float64(len(ids))
		v.LossDetail = fmt.Sprintf("丢包最多：%s %.0f%%", worstName, math.Round(worstLoss))
		if m, _ := summarizeLatency(lats); m != nil {
			v.Latency = m
			v.LatencyDetail = fmt.Sprintf("最慢：%s %.0f ms", slowestName, slowest)
		}
		out[sid] = v
	}
	return out
}

// probeAlertRows 读取 since 以来全部节点到国内目标的结果。
func (s *Store) probeAlertRows(since time.Time) ([]probeAlertRow, error) {
	rows, err := s.DB.Query(`SELECT r.server_id, r.target_id, t.isp, t.province, r.ts, r.latency_ms, r.loss
		FROM probe_results r JOIN probe_targets t ON t.id = r.target_id
		WHERE r.ts >= ? AND t.isp != 'intl'`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []probeAlertRow
	for rows.Next() {
		var r probeAlertRow
		var isp, province string
		var lat sql.NullFloat64
		if err := rows.Scan(&r.ServerID, &r.TargetID, &isp, &province, &r.TS, &lat, &r.Loss); err != nil {
			return nil, err
		}
		if lat.Valid {
			v := lat.Float64
			r.Latency = &v
		}
		r.Name = province + ispNames[isp]
		out = append(out, r)
	}
	return out, rows.Err()
}
