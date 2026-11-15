package server

// 公开展示页的三网延迟（修订第 328 条）：每个公开节点显示一个地区的电信、联通、移动最近 24 小时，每小时一格。
// 地区默认为该节点有结果的第一个地区（按测试目标的顺序），管理员可以为每个节点指定地区或不显示。
//
// 【安全】只公开地区名、运营商与延迟 / 丢包数值，不含测试目标的地址、端口、协议与 ID；
// 地区名来自管理员填写的测试目标（省份或城市）。数据另有 60 秒缓存，访问量再大也只读一次库。

import (
	"slices"
	"strings"
	"time"
)

const (
	publicLatencyBlocks = 24 // 每小时一格，共 24 小时
	publicLatencyTTL    = time.Minute
	publicLatencyOff    = "off" // 节点设置：不显示三网延迟
	publicLatencyMaxLen = 40    // 与测试目标的地区名长度一致
)

// 展示页上三网的顺序
var publicISPs = []string{"ct", "cu", "cm"}

// publicLatencyBlock 是一小时内的汇总；没有结果的小时两个字段都省略。
type publicLatencyBlock struct {
	Latency *float64 `json:"latency_ms,omitempty"` // 成功轮次的中位数；有结果但全部失败时省略（不可达）
	Loss    *float64 `json:"loss,omitempty"`       // 平均丢包率，百分比
}

// publicLatencyRow 是一个运营商的一行。
type publicLatencyRow struct {
	ISP     string               `json:"isp"`                  // ct / cu / cm
	Latency *float64             `json:"latency_ms,omitempty"` // 最近 1 小时中位数
	Loss    *float64             `json:"loss,omitempty"`       // 最近 1 小时平均丢包率
	Blocks  []publicLatencyBlock `json:"blocks"`               // 从早到晚 24 格，最后一格为最近 1 小时
}

// publicLatency 是一个节点在展示页上的三网延迟。
type publicLatency struct {
	Region string             `json:"region"`
	Rows   []publicLatencyRow `json:"rows"`
}

// latencyRegions 返回节点有结果的国内地区，按测试目标的顺序（纯函数）。
func latencyRegions(targets []ProbeTargetRow, pts map[int64][]LatencyPoint) []string {
	out := []string{}
	for _, t := range targets {
		if !slices.Contains(publicISPs, t.ISP) || len(pts[t.ID]) == 0 || t.Province == "" || slices.Contains(out, t.Province) {
			continue
		}
		out = append(out, t.Province)
	}
	return out
}

// buildPublicLatency 组装一个节点的三网延迟（纯函数）。pts 是该节点最近 24 小时的结果，按目标 ID 分组；
// choice 为节点设置：空为第一个地区，off 不显示，其他为地区名（没有该地区的结果时退回第一个地区）。没有数据时返回 nil。
func buildPublicLatency(targets []ProbeTargetRow, pts map[int64][]LatencyPoint, choice string, now time.Time) *publicLatency {
	if choice == publicLatencyOff {
		return nil
	}
	regions := latencyRegions(targets, pts)
	if len(regions) == 0 {
		return nil
	}
	region := regions[0]
	if slices.Contains(regions, choice) {
		region = choice
	}
	out := &publicLatency{Region: region, Rows: []publicLatencyRow{}}
	end := now.Unix()
	for _, isp := range publicISPs {
		i := slices.IndexFunc(targets, func(t ProbeTargetRow) bool { return t.ISP == isp && t.Province == region && len(pts[t.ID]) > 0 })
		if i < 0 {
			continue
		}
		p := pts[targets[i].ID]
		row := publicLatencyRow{ISP: isp, Blocks: make([]publicLatencyBlock, publicLatencyBlocks)}
		buckets := make([][]LatencyPoint, publicLatencyBlocks)
		for _, x := range p {
			age := end - x.TS
			if age < 0 {
				age = 0
			}
			k := publicLatencyBlocks - 1 - int(age/3600)
			if k >= 0 && k < publicLatencyBlocks {
				buckets[k] = append(buckets[k], x)
			}
		}
		for k, b := range buckets {
			lat, loss := summarizeLatency(b)
			row.Blocks[k] = publicLatencyBlock{Latency: roundPtr(lat), Loss: roundPtr(loss)}
		}
		last := row.Blocks[publicLatencyBlocks-1]
		row.Latency, row.Loss = last.Latency, last.Loss
		out.Rows = append(out.Rows, row)
	}
	if len(out.Rows) == 0 {
		return nil
	}
	return out
}

func roundPtr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	r := round1(*v)
	return &r
}

// publicLatencies 返回各公开节点的三网延迟（键为节点 ID）；60 秒缓存。调用方持有 statusPage.mu。
func (s *Server) publicLatencies(rows []ServerRow, now time.Time) (map[int64]*publicLatency, error) {
	c := &s.statusPage
	if !c.latAt.IsZero() && now.Sub(c.latAt) < publicLatencyTTL && c.lat != nil {
		return c.lat, nil
	}
	choices, err := s.store.PublicLatencyChoices()
	if err != nil {
		return nil, err
	}
	allowed := map[int64]bool{}
	for _, row := range rows {
		if choices[row.ID] != publicLatencyOff {
			allowed[row.ID] = true
		}
	}
	out := map[int64]*publicLatency{}
	if len(allowed) > 0 {
		targets, err := s.store.ListProbeTargets()
		if err != nil {
			return nil, err
		}
		pts, err := s.store.recentProbePoints(now.Add(-publicLatencyBlocks*time.Hour), allowed)
		if err != nil {
			return nil, err
		}
		byServer := map[int64]map[int64][]LatencyPoint{}
		for k, v := range pts {
			if byServer[k[0]] == nil {
				byServer[k[0]] = map[int64][]LatencyPoint{}
			}
			byServer[k[0]][k[1]] = v
		}
		for _, row := range rows {
			if !allowed[row.ID] {
				continue
			}
			mine := slices.DeleteFunc(slices.Clone(targets), func(t ProbeTargetRow) bool { return t.ServerID != 0 && t.ServerID != row.ID })
			if l := buildPublicLatency(mine, byServer[row.ID], choices[row.ID], now); l != nil {
				out[row.ID] = l
			}
		}
	}
	c.latAt, c.lat = now, out
	return out, nil
}

// statusLatencyOption 是设置页中一个节点的三网延迟地区：已保存的选择与可选的地区。
type statusLatencyOption struct {
	ServerID int64    `json:"server_id"`
	Region   string   `json:"region"`  // 空为第一个地区，off 为不显示
	Regions  []string `json:"regions"` // 最近 24 小时有结果的国内地区，按测试目标的顺序
}

// statusLatencyOptions 列出全部已安装节点的地区设置与可选地区。
func (s *Server) statusLatencyOptions(now time.Time) ([]statusLatencyOption, error) {
	rows, err := s.store.ListServers()
	if err != nil {
		return nil, err
	}
	rows = slices.DeleteFunc(rows, func(row ServerRow) bool { return row.EnrollState == enrollPending })
	choices, err := s.store.PublicLatencyChoices()
	if err != nil {
		return nil, err
	}
	allowed := map[int64]bool{}
	for _, row := range rows {
		allowed[row.ID] = true
	}
	targets, err := s.store.ListProbeTargets()
	if err != nil {
		return nil, err
	}
	pts, err := s.store.recentProbePoints(now.Add(-publicLatencyBlocks*time.Hour), allowed)
	if err != nil {
		return nil, err
	}
	out := make([]statusLatencyOption, 0, len(rows))
	for _, row := range rows {
		mine := map[int64][]LatencyPoint{}
		for _, t := range targets {
			if p := pts[[2]int64{row.ID, t.ID}]; len(p) > 0 {
				mine[t.ID] = p
			}
		}
		out = append(out, statusLatencyOption{ServerID: row.ID, Region: choices[row.ID], Regions: latencyRegions(targets, mine)})
	}
	return out, nil
}

// validLatencyChoice 检查节点的地区设置：空、off 或不超过 40 个字符的地区名。
func validLatencyChoice(v string) bool {
	return len([]rune(v)) <= publicLatencyMaxLen && strings.TrimSpace(v) == v && !strings.ContainsAny(v, "\r\n\t")
}

// PublicLatencyChoices 读取各节点在展示页上的三网延迟地区设置（只含设置过的节点）。
func (s *Store) PublicLatencyChoices() (map[int64]string, error) {
	rows, err := s.DB.Query(`SELECT id, status_latency FROM servers WHERE status_latency != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var v string
		if err := rows.Scan(&id, &v); err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, rows.Err()
}

// SetPublicLatencyChoices 保存各节点的地区设置；未列出的节点恢复为默认（第一个地区）。
func (s *Store) SetPublicLatencyChoices(choices map[int64]string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE servers SET status_latency = ''`); err != nil {
		return err
	}
	for id, v := range choices {
		if _, err := tx.Exec(`UPDATE servers SET status_latency = ? WHERE id = ?`, v, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
