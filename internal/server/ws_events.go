package server

// 实时事件（设计 20、45.2）：节点状态、上下线与告警。只在有 WebSocket 连接时组装，
// 没有页面或脚本订阅时不增加任何开销。按 API Key 的节点范围过滤（ws.go 的 wsClient.allows）。

import (
	"context"
	"time"
)

// listView 返回节点在列表中的视图：去掉只在详情页使用的每核使用率、监听端口与扩展指标，减小事件体积。
func listView(v serverView) serverView {
	if v.Latest != nil && (v.Latest.CPU.PerCore != nil || v.Latest.Ports != nil || v.Latest.Extra != nil) {
		rep := *v.Latest
		rep.CPU.PerCore = nil
		rep.Ports = nil
		rep.Extra = nil
		v.Latest = &rep
	}
	return v
}

// publishMetrics 在收到一份上报后推送 server.metrics：数据与 GET /servers 的一项相同，页面直接替换。
func (s *Server) publishMetrics(sid int64, now time.Time) {
	if !s.ws.hasClients() {
		return
	}
	row, err := s.store.GetServer(sid)
	if err != nil {
		return
	}
	v, err := s.viewOf(*row, now)
	if err != nil {
		s.log.Warn("build metrics event failed", "component", "ws", "server_id", sid, "err", err)
		return
	}
	s.ws.publish(wsEvent{Type: evServerMetrics, ServerID: sid, TS: now.Unix(), Data: listView(v), group: row.Group})
}

// publishAlert 推送告警触发或恢复。
func (s *Server) publishAlert(kind string, row ServerRow, r AlertRule, eventID int64, v float64, msg string, now time.Time) {
	if !s.ws.hasClients() {
		return
	}
	s.ws.publish(wsEvent{Type: kind, ServerID: row.ID, TS: now.Unix(), group: row.Group, Data: map[string]any{
		"event_id": eventID, "server_name": row.Name, "rule_key": r.RuleKey, "type": r.Type, "severity": r.Severity,
		"value": v, "message": msg,
	}})
}

// statusLoop 每 10 秒检查节点的在线状态，推送 server.online / server.offline。
// 只在有连接时检查；连接建立后第一轮只记录当前状态，不推送（避免把已有状态当作变化）。
func (s *Server) statusLoop(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	last := map[int64]string{}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if !s.ws.hasClients() {
				clear(last)
				continue
			}
			s.scanStatus(now, last)
		}
	}
}

// scanStatus 比较各节点的在线状态与上一轮，推送变化。last 由调用方保存。
func (s *Server) scanStatus(now time.Time, last map[int64]string) {
	rows, err := s.store.ListServers()
	if err != nil {
		return
	}
	first := len(last) == 0
	seen := map[int64]bool{}
	for _, row := range rows {
		if row.EnrollState == enrollPending {
			continue
		}
		seen[row.ID] = true
		st := s.statusOf(row, now)
		prev, had := last[row.ID]
		last[row.ID] = st
		if first || !had || prev == st {
			continue
		}
		switch {
		case st == "online" && prev != "online":
			s.ws.publish(wsEvent{Type: evServerOnline, ServerID: row.ID, TS: now.Unix(), group: row.Group,
				Data: map[string]any{"server_name": row.Name}})
		case st == "offline":
			s.ws.publish(wsEvent{Type: evServerOffline, ServerID: row.ID, TS: now.Unix(), group: row.Group,
				Data: map[string]any{"server_name": row.Name}})
		}
	}
	for id := range last {
		if !seen[id] {
			delete(last, id) // 已删除的节点
		}
	}
}

// statusOf 按最近一次上报的时间判断在线状态（与 viewOf 相同的规则，设计 22）。
func (s *Server) statusOf(row ServerRow, now time.Time) string {
	s.mu.Lock()
	snap := s.latest[row.ID]
	s.mu.Unlock()
	if snap == nil {
		return "offline"
	}
	age := now.Sub(snap.ReceivedAt)
	online, unknown := statusWindows(reportInterval(row))
	switch {
	case age <= online:
		return "online"
	case age <= unknown:
		return "unknown"
	}
	return "offline"
}
