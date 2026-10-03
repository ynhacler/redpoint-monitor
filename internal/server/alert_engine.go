package server

import (
	"context"
	"sort"
	"sync"
	"time"
)

// 告警引擎（设计 16.7）：在面板进程内每 10 秒评估一次。
//
//   - 实时指标取自内存中的最新上报（Server.latest），不查询指标表
//   - 以面板收到数据的时间判断离线，不信任 Agent 时钟
//   - 每个节点、每条规则独立评估；单条出错只记录日志
//   - firing 写入 alert_events，面板重启后据此恢复，不会重复记录；pending 只在内存中
//   - 待安装节点不产生告警；离线节点的资源规则暂停评估，只保留“节点离线”（设计 16.3 NODATA）
//
// 通知（Telegram / Webhook / 推送）、静音与维护、抖动检测、批量合并随 A5 后续加入（TODO(A5)）。

const (
	alertInterval        = 10 * time.Second
	alertTrafficInterval = time.Minute // 流量需要汇总数据库，频率低于实时指标
	// 面板刚启动时内存中还没有任何上报，所有节点看起来都“很久没上报”。等一个“未知”窗口，
	// 让在线的 Agent 先报上来，避免重启面板就对全部节点发出离线告警（设计 16.4 面板自检）。
	alertStartupGrace = unknownWithin + alertInterval
)

type alertKey struct {
	serverID int64
	ruleKey  string
}

type alertEngine struct {
	mu        sync.Mutex
	active    map[alertKey]*alertState // pending 与 firing
	meta      map[alertKey]AlertRule   // 活动告警对应的规则（级别、类型），供列表展示
	traffic   map[int64]*trafficView   // 各节点最近一次计算的本周期流量
	trafficAt time.Time
	startedAt time.Time
}

// newAlertEngine 从数据库恢复活动告警（设计 16.7）。
func newAlertEngine(store *Store, now time.Time) (*alertEngine, error) {
	e := &alertEngine{active: map[alertKey]*alertState{}, meta: map[alertKey]AlertRule{},
		traffic: map[int64]*trafficView{}, startedAt: now}
	firing, err := store.FiringAlertEvents()
	if err != nil {
		return nil, err
	}
	for _, ev := range firing {
		k := alertKey{ev.ServerID, ev.RuleKey}
		e.active[k] = &alertState{State: StateFiring, EventID: ev.ID, StartedAt: time.Unix(ev.StartedAt, 0),
			FiredAt: time.Unix(ev.FiredAt, 0), Value: ev.Value}
		e.meta[k] = AlertRule{ID: ev.RuleID, RuleKey: ev.RuleKey, Type: ev.Type, Severity: ev.Severity, Threshold: ev.Threshold}
	}
	return e, nil
}

// alertBrief 是节点当前的活动告警摘要，随节点列表返回，“需要关注”据此判断（设计 9）。
type alertBrief struct {
	EventID  int64   `json:"event_id"`
	RuleKey  string  `json:"rule_key"`
	Type     string  `json:"type"`
	Severity string  `json:"severity"`
	Message  string  `json:"message"`
	Value    float64 `json:"value"` // 当前值（每次评估更新）
	FiredAt  int64   `json:"fired_at"`
}

// firingFor 返回节点的活动告警，严重在前；同一类型只保留最严重的一条
// （例如磁盘 96% 同时满足 85% 与 95% 两条规则，只显示严重那条）。
// 依赖抑制（设计 16.4）：节点离线时只返回离线告警，资源告警仍保留记录，重新上报后照常显示。
func (e *alertEngine) firingFor(serverID int64) []alertBrief {
	e.mu.Lock()
	defer e.mu.Unlock()
	best := map[string]alertBrief{}
	for k, st := range e.active {
		if k.serverID != serverID || st.State != StateFiring {
			continue
		}
		r := e.meta[k]
		b := alertBrief{EventID: st.EventID, RuleKey: k.ruleKey, Type: r.Type, Severity: r.Severity,
			Message: alertMessage(r, st.Value, st.Detail), Value: st.Value, FiredAt: st.FiredAt.Unix()}
		if cur, ok := best[r.Type]; !ok || severityRank(b.Severity) > severityRank(cur.Severity) ||
			(b.Severity == cur.Severity && r.Threshold > e.meta[alertKey{serverID, cur.RuleKey}].Threshold) {
			best[r.Type] = b
		}
	}
	if off, ok := best[AlertOffline]; ok {
		return []alertBrief{off}
	}
	out := make([]alertBrief, 0, len(best))
	for _, b := range best {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := severityRank(out[i].Severity), severityRank(out[j].Severity); a != b {
			return a > b
		}
		return out[i].FiredAt < out[j].FiredAt
	})
	return out
}

func severityRank(s string) int {
	switch s {
	case SeverityCritical:
		return 2
	case SeverityWarning:
		return 1
	}
	return 0
}

// alertLoop 每 10 秒评估一次，直到面板退出。
func (s *Server) alertLoop(ctx context.Context) {
	t := time.NewTicker(alertInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if err := s.evaluateAlerts(now); err != nil {
				s.log.Error("alert evaluation failed", "component", "alert", "err", err)
			}
		}
	}
}

// evaluateAlerts 评估一轮全部节点与规则。
func (s *Server) evaluateAlerts(now time.Time) error {
	e := s.alerts
	rows, err := s.store.ListServers()
	if err != nil {
		return err
	}
	rules, err := s.store.ListAlertRules()
	if err != nil {
		return err
	}
	s.mu.Lock()
	snaps := make(map[int64]snapshot, len(s.latest))
	for id, sn := range s.latest {
		snaps[id] = *sn
	}
	s.mu.Unlock()

	refreshTraffic := now.Sub(e.trafficAt) >= alertTrafficInterval
	if refreshTraffic {
		e.trafficAt = now
	}
	seen := map[alertKey]bool{}
	seenServers := map[int64]bool{}
	for _, row := range rows {
		if row.EnrollState == enrollPending {
			continue // 待安装节点不产生告警（设计 27.7）
		}
		seenServers[row.ID] = true
		sn, hasSnap := snaps[row.ID]
		last := time.Unix(row.LastSeenAt, 0)
		if hasSnap {
			last = sn.ReceivedAt
		} else if row.LastSeenAt == 0 {
			last = time.Unix(row.EnrolledAt, 0) // 注册后从未上报：从注册时间算起
		}
		in := alertInput{OfflineFor: now.Sub(last)}
		if hasSnap && in.OfflineFor <= unknownWithin {
			rep := sn.Report
			in.Report = &rep
		}
		if refreshTraffic {
			if tv, err := s.trafficOf(row, now); err == nil {
				e.traffic[row.ID] = &tv
			} else {
				s.log.Error("alert traffic failed", "component", "alert", "server_id", row.ID, "err", err)
			}
		}
		in.Traffic = e.traffic[row.ID]

		for _, r := range EffectiveRules(rules, row.ID, row.Group) {
			k := alertKey{row.ID, r.RuleKey}
			seen[k] = true
			if r.Type == AlertOffline && !hasSnap && now.Sub(e.startedAt) < alertStartupGrace {
				continue
			}
			v, detail, ok := alertValue(r.Type, in)
			if !ok {
				continue // 没有数据：保持现状（离线期间资源告警既不触发也不恢复）
			}
			s.applyAlert(k, row, r, v, detail, now)
		}
	}
	// 规则被关闭或删除、节点被删除或重新变为待安装：结束对应的活动告警
	e.mu.Lock()
	var stale []alertKey
	for k := range e.active {
		if !seen[k] {
			stale = append(stale, k)
		}
	}
	e.mu.Unlock()
	for _, k := range stale {
		s.endAlert(k, now)
	}
	for id := range e.traffic {
		if !seenServers[id] {
			delete(e.traffic, id)
		}
	}
	return nil
}

// applyAlert 推进一个节点一条规则的状态，并持久化 firing / resolved。
func (s *Server) applyAlert(k alertKey, row ServerRow, r AlertRule, v float64, detail string, now time.Time) {
	e := s.alerts
	e.mu.Lock()
	st := e.active[k]
	e.mu.Unlock()

	next, tr := advanceAlert(st, r, v, detail, now)
	switch tr {
	case alertFired:
		ev := AlertEvent{RuleID: r.ID, RuleKey: r.RuleKey, ServerID: row.ID, Type: r.Type, Severity: r.Severity,
			Value: v, Threshold: r.Threshold, Message: alertMessage(r, v, detail),
			StartedAt: next.StartedAt.Unix(), FiredAt: now.Unix()}
		id, err := s.store.InsertAlertEvent(ev)
		if err != nil {
			// 写库失败不进入 firing，下一轮重试；避免内存与数据库不一致
			s.log.Error("alert event insert failed", "component", "alert", "server_id", row.ID, "rule", r.RuleKey, "err", err)
			return
		}
		next.EventID = id
		s.log.Info("alert firing", "component", "alert", "server_id", row.ID, "server", row.Name, "rule", r.RuleKey,
			"severity", r.Severity, "value", v)
	case alertResolved:
		if err := s.store.ResolveAlertEvent(st.EventID, now, v); err != nil {
			s.log.Error("alert event resolve failed", "component", "alert", "event_id", st.EventID, "err", err)
		}
		s.log.Info("alert resolved", "component", "alert", "server_id", row.ID, "server", row.Name, "rule", r.RuleKey, "value", v)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if next == nil {
		delete(e.active, k)
		delete(e.meta, k)
		return
	}
	e.active[k], e.meta[k] = next, r
}

// endAlert 在规则失效时结束活动告警（不是因为指标恢复，因此不记录恢复时的值以外的信息）。
func (s *Server) endAlert(k alertKey, now time.Time) {
	e := s.alerts
	e.mu.Lock()
	st := e.active[k]
	delete(e.active, k)
	delete(e.meta, k)
	e.mu.Unlock()
	if st != nil && st.State == StateFiring && st.EventID > 0 {
		if err := s.store.ResolveAlertEvent(st.EventID, now, st.Value); err != nil {
			s.log.Error("alert event resolve failed", "component", "alert", "event_id", st.EventID, "err", err)
		}
	}
}
