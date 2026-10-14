package server

// 测速目标的健康（设计 42.3.2：“目标失效（长期不可达）时在 Web 中提示更换”，修订第 134 条）：
// 汇总全部节点最近 24 小时到每个目标的结果，在“监测 → 测速设置”的目标列表中显示，长期不可达的标为“疑似失效”。

import (
	"database/sql"
	"time"
)

const (
	probeHealthWindow  = 24 * time.Hour
	probeDeadMinRounds = 3  // 至少这么多轮结果才下结论，刚添加的目标不会马上标为失效
	probeDeadLoss      = 99 // 平均丢包率 ≥ 99%：几乎没有一次成功
	probeDegradedLoss  = 20
)

// ProbeTargetHealth 是一个目标最近 24 小时在全部节点上的汇总。
type ProbeTargetHealth struct {
	State   string   `json:"state"`      // ok / degraded（丢包偏高）/ dead（疑似失效）/ nodata（还没有结果）
	Nodes   int      `json:"nodes"`      // 测过它的节点数
	Rounds  int      `json:"rounds"`     // 结果条数（每个节点每轮一条）
	Loss    *float64 `json:"loss"`       // 平均丢包率；没有结果时为 null
	Latency *float64 `json:"latency_ms"` // 成功轮次的平均延迟；没有成功时为 null
	LastAt  int64    `json:"last_at"`
}

// probeHealthState 判断目标状态（纯函数）。
func probeHealthState(h ProbeTargetHealth) string {
	switch {
	case h.Rounds == 0 || h.Loss == nil:
		return "nodata"
	case h.Rounds >= probeDeadMinRounds && *h.Loss >= probeDeadLoss:
		return "dead"
	case *h.Loss >= probeDegradedLoss:
		return "degraded"
	}
	return "ok"
}

// probeHealth 汇总 since 以来每个目标的结果。
func (s *Store) probeHealth(since time.Time) (map[int64]ProbeTargetHealth, error) {
	rows, err := s.DB.Query(`SELECT target_id, COUNT(DISTINCT server_id), COUNT(*), AVG(loss), AVG(latency_ms), MAX(ts)
		FROM probe_results WHERE ts >= ? GROUP BY target_id`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]ProbeTargetHealth{}
	for rows.Next() {
		var id int64
		var h ProbeTargetHealth
		var loss, lat sql.NullFloat64
		if err := rows.Scan(&id, &h.Nodes, &h.Rounds, &loss, &lat, &h.LastAt); err != nil {
			return nil, err
		}
		if loss.Valid {
			v := round1(loss.Float64)
			h.Loss = &v
		}
		if lat.Valid {
			v := round1(lat.Float64)
			h.Latency = &v
		}
		out[id] = h
	}
	return out, rows.Err()
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }
