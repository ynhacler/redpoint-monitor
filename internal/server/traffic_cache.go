package server

import (
	"fmt"
	"sync"
	"time"
)

// 本周期流量的短期缓存（设计 3.2）：节点列表每 3 秒轮询一次，每个节点的流量要查 4 次数据库；
// 500 个节点时列表接口约 50 ms。流量本身每 3 秒才批量写入一次，因此列表、详情与告警引擎共用 10 秒内的结果。
// 校准后立即写入新结果；缓存键包含节点的计费设置，修改设置后自然失效，不会显示旧口径。

const trafficTTL = 10 * time.Second

type trafficEntry struct {
	v   trafficView
	at  time.Time
	key string
}

type trafficCache struct {
	mu sync.Mutex
	m  map[int64]trafficEntry
}

// trafficKey 是影响流量计算结果的节点设置。
func trafficKey(row ServerRow) string {
	return fmt.Sprintf("%d|%s|%g|%d|%s", row.ResetDay, row.CountMode, row.TrafficFactor, row.LimitBytes, row.TrafficUnit)
}

// trafficCached 返回 10 秒内计算过的本周期流量；没有、过期或节点设置已变时重新计算。
func (s *Server) trafficCached(row ServerRow, now time.Time) (trafficView, error) {
	key := trafficKey(row)
	s.traffic.mu.Lock()
	e, ok := s.traffic.m[row.ID]
	s.traffic.mu.Unlock()
	if ok && e.key == key && now.Sub(e.at) >= 0 && now.Sub(e.at) < trafficTTL {
		return e.v, nil
	}
	v, err := s.trafficOf(row, now)
	if err != nil {
		return v, err
	}
	s.storeTraffic(row, v, now)
	return v, nil
}

// storeTraffic 写入（或在校准后覆盖）一个节点的流量缓存。
func (s *Server) storeTraffic(row ServerRow, v trafficView, now time.Time) {
	s.traffic.mu.Lock()
	if s.traffic.m == nil {
		s.traffic.m = map[int64]trafficEntry{}
	}
	s.traffic.m[row.ID] = trafficEntry{v: v, at: now, key: trafficKey(row)}
	s.traffic.mu.Unlock()
}

// forgetTraffic 在删除节点后清除缓存。
func (s *Server) forgetTraffic(id int64) {
	s.traffic.mu.Lock()
	delete(s.traffic.m, id)
	s.traffic.mu.Unlock()
}
