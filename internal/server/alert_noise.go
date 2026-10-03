package server

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// 告警降噪（设计 16.3 FLAPPING、16.4）：所有告警通知先经过这里，再交给 notifier 发送。
//
//   - 抖动：同一节点同一规则 30 分钟内触发 3 次（触发与恢复共 5 次，超过设计的 4 次），只通知一次“状态频繁变化”，
//     之后暂停该告警的通知；连续 30 分钟没有新的触发或恢复即恢复正常，若此时仍在告警则补发一条
//   - 批量合并：“节点离线”的触发与恢复通知先收集最多 1 分钟，期间 ≥ 5 台合并为一条，否则逐条发出
//   - 面板自检：至少 2 台节点在 1 分钟内先后全部停止上报，判定为面板一侧异常（网络、进程），以面板告警通知，
//     期间暂停离线规则评估，避免一次面板故障产生一大批离线告警；任一节点恢复上报即解除
//
// 告警事件照常记录，降噪只影响通知。

const (
	flapWindow      = 30 * time.Minute
	flapFires       = 3 // 窗口内触发次数达到即判定抖动
	flapStable      = 30 * time.Minute
	batchWindow     = time.Minute
	batchMin        = 5
	panelSilence    = time.Minute // 全部节点超过这么久没有上报
	panelSpread     = time.Minute // 且最后一次上报时间相差不超过这么多（“同一时刻”）
	panelMinServers = 2
)

type flapState struct {
	fires      []time.Time // 窗口内的触发时间
	flapping   bool
	lastChange time.Time // 最近一次触发或恢复
	firing     bool      // 当前是否仍在告警（退出抖动时决定是否补发）
	last       notifyMessage
}

type alertNoise struct {
	mu        sync.Mutex
	flaps     map[alertKey]*flapState
	batch     map[string][]notifyMessage // firing / resolved → 待合并的离线通知
	batchAt   map[string]time.Time       // 每批第一条的时间
	panelDown bool
	panelN    int // 判定面板异常时停止上报的节点数
}

func newAlertNoise() *alertNoise {
	return &alertNoise{flaps: map[alertKey]*flapState{}, batch: map[string][]notifyMessage{}, batchAt: map[string]time.Time{}}
}

// emit 接收一条告警通知：抖动检测 → 离线合并 → 发送。
func (s *Server) emit(k alertKey, m notifyMessage, now time.Time) {
	n := s.noise
	n.mu.Lock()
	out := n.filterFlap(k, m, now)
	var send []notifyMessage
	for _, x := range out {
		if x.Type == AlertOffline && (x.Kind == NotifyFiring || x.Kind == NotifyResolved) {
			if len(n.batch[x.Kind]) == 0 {
				n.batchAt[x.Kind] = now
			}
			n.batch[x.Kind] = append(n.batch[x.Kind], x)
			continue
		}
		send = append(send, x)
	}
	n.mu.Unlock()
	for _, x := range send {
		s.notify.dispatch(x)
	}
}

// filterFlap 记录触发与恢复，返回实际要发送的通知（抖动期间为空）。
func (n *alertNoise) filterFlap(k alertKey, m notifyMessage, now time.Time) []notifyMessage {
	st := n.flaps[k]
	if st == nil {
		st = &flapState{}
		n.flaps[k] = st
	}
	switch m.Kind {
	case NotifyFiring:
		st.firing, st.lastChange, st.last = true, now, m
		kept := st.fires[:0]
		for _, t := range st.fires {
			if now.Sub(t) < flapWindow {
				kept = append(kept, t)
			}
		}
		st.fires = append(kept, now)
		if st.flapping {
			return nil
		}
		if len(st.fires) >= flapFires {
			st.flapping = true
			f := m
			f.Kind = NotifyFlapping
			f.Count = len(st.fires)
			return []notifyMessage{f}
		}
	case NotifyResolved:
		st.firing, st.lastChange = false, now
		if st.flapping {
			return nil
		}
	case NotifyRepeat:
		if st.flapping {
			return nil
		}
	}
	return []notifyMessage{m}
}

// tickNoise 在每轮评估结束时调用：结束稳定下来的抖动、发出到期的离线合并。
func (s *Server) tickNoise(now time.Time) {
	n := s.noise
	var send []notifyMessage
	n.mu.Lock()
	for k, st := range n.flaps {
		if now.Sub(st.lastChange) < flapStable {
			continue
		}
		if st.flapping && st.firing {
			m := st.last
			m.Kind = NotifyStillFiring // 稳定下来但仍在告警：补发一条
			send = append(send, m)
		}
		if !st.firing || st.flapping {
			delete(n.flaps, k) // 稳定 30 分钟：恢复正常评估
		}
	}
	for kind, list := range n.batch {
		if len(list) == 0 || now.Sub(n.batchAt[kind]) < batchWindow {
			continue
		}
		if len(list) >= batchMin {
			send = append(send, mergeOffline(kind, list))
		} else {
			send = append(send, list...)
		}
		delete(n.batch, kind)
	}
	n.mu.Unlock()
	for _, m := range send {
		s.notify.dispatch(m)
	}
}

// forget 删除已结束告警的抖动记录（规则失效、节点删除时）。
func (n *alertNoise) forget(k alertKey) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if st := n.flaps[k]; st != nil && !st.flapping {
		delete(n.flaps, k)
	}
}

// mergeOffline 把同一批离线（或恢复）通知合并为一条（设计 16.4）。
func mergeOffline(kind string, list []notifyMessage) notifyMessage {
	m := notifyMessage{Kind: kind, Type: AlertOffline, RuleKey: AlertOffline, Severity: list[0].Severity, StartedAt: list[0].StartedAt}
	for _, x := range list {
		m.Servers = append(m.Servers, x.ServerName)
		if severityRank(x.Severity) > severityRank(m.Severity) {
			m.Severity = x.Severity
		}
	}
	m.Count = len(list)
	if kind == NotifyResolved {
		m.ResolvedAt = list[len(list)-1].ResolvedAt
	}
	return m
}

// namesBrief：HK-1、HK-2、JP-1 等
func namesBrief(names []string) string {
	if len(names) <= 3 {
		return strings.Join(names, "、")
	}
	return strings.Join(names[:3], "、") + " 等"
}

// checkPanel 面板自检（设计 16.4）：返回本轮是否暂停离线规则评估。
func (s *Server) checkPanel(rows []ServerRow, snaps map[int64]snapshot, now time.Time) bool {
	var first, last time.Time
	n := 0
	for _, row := range rows {
		if row.EnrollState == enrollPending || s.alerts.silenceFor(row, SilenceMaintenance, "", now) != nil {
			continue
		}
		sn, ok := snaps[row.ID]
		if !ok {
			continue // 面板启动后还没收到过上报：不参与判断
		}
		if now.Sub(sn.ReceivedAt) <= panelSilence {
			n = -1 // 有节点仍在上报：不是面板的问题
			break
		}
		if first.IsZero() || sn.ReceivedAt.Before(first) {
			first = sn.ReceivedAt
		}
		if sn.ReceivedAt.After(last) {
			last = sn.ReceivedAt
		}
		n++
	}
	down := n >= panelMinServers && last.Sub(first) <= panelSpread
	ns := s.noise
	ns.mu.Lock()
	changed := down != ns.panelDown
	ns.panelDown = down
	if down && changed {
		ns.panelN = n
	}
	count := ns.panelN
	ns.mu.Unlock()
	if changed {
		kind := NotifyPanelUp
		if down {
			kind = NotifyPanelDown
			s.log.Warn("all nodes stopped reporting at once; suspecting the panel side", "component", "alert", "servers", n)
		} else {
			s.log.Info("nodes reporting again", "component", "alert")
		}
		s.notify.dispatch(notifyMessage{Kind: kind, Severity: SeverityCritical, Count: count, StartedAt: now, Link: s.publicURL})
	}
	return down
}

// panelText 是面板自检通知的说明。
func panelText(m notifyMessage) (string, string) {
	if m.Kind == NotifyPanelDown {
		return fmt.Sprintf("⚠️ 全部 %d 台节点同时停止上报", m.Count),
			"优先检查面板一侧：网络、进程、反向代理。期间暂停离线告警，任一节点恢复上报后自动解除。"
	}
	return fmt.Sprintf("✅ 面板恢复接收上报（%d 台节点）", m.Count), ""
}
