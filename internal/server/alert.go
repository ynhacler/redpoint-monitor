package server

import (
	"fmt"
	"math"
	"time"

	"vpsmon/internal/protocol"
)

// 告警规则与状态机（设计 16）。本文件只有纯函数，不读写数据库与内存状态，便于表测试；
// 定时评估、持久化与对外接口见 alert_engine.go、store_alerts.go、alert_api.go。

// 告警类型（设计 16.1、18.7）。国内不可达、三网延迟、即将到期、Agent 异常、面板自身随对应功能加入。
const (
	AlertOffline         = "offline"
	AlertCPU             = "cpu"
	AlertMemory          = "memory"
	AlertDisk            = "disk"
	AlertSwap            = "swap"
	AlertLoad            = "load"
	AlertTraffic         = "traffic"
	AlertTrafficForecast = "traffic_forecast"
)

// 告警级别（设计 16.1）。
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// AlertRule 是一条告警规则（设计 16.2、18.7）。
//
// 规则分三层：全局 → 分组 → 节点，下层按 RuleKey 覆盖上层（如节点规则 “cpu” 覆盖全局 “cpu”）。
// 触发与恢复都是“值越大越严重”：值 Operator Threshold 持续 DurationS 秒后触发；
// 值 < RecoverThreshold 持续 RecoverDurationS 秒后恢复。两个阈值分开即回差，避免在阈值附近反复触发。
type AlertRule struct {
	ID               int64   `json:"id"`
	RuleKey          string  `json:"rule_key"`   // 如 cpu、disk_critical、traffic_80；同一节点同一 RuleKey 只有一个活动告警
	ScopeType        string  `json:"scope_type"` // global / group / server
	ScopeID          string  `json:"scope_id"`   // 分组名或节点 ID；global 时为空
	Type             string  `json:"type"`
	Operator         string  `json:"operator"` // > / >=
	Threshold        float64 `json:"threshold"`
	RecoverThreshold float64 `json:"recover_threshold"`
	DurationS        int     `json:"duration_s"`
	RecoverDurationS int     `json:"recover_duration_s"`
	Severity         string  `json:"severity"`
	RepeatIntervalS  int     `json:"repeat_interval_s"` // 仍未恢复时多久再提醒一次；0 表示不重复（通知随 A5 后续加入）
	Enabled          bool    `json:"enabled"`
}

// EffectiveRules 为一个节点合并三层规则：全局 → 节点所在分组 → 节点自身，下层按 RuleKey 覆盖上层。
// 覆盖后被关闭的规则不返回（例如某台机器关闭 CPU 告警）。
func EffectiveRules(all []AlertRule, serverID int64, group string) []AlertRule {
	byKey := map[string]AlertRule{}
	var order []string
	apply := func(scope, id string) {
		for _, r := range all {
			if r.ScopeType != scope || r.ScopeID != id {
				continue
			}
			if _, ok := byKey[r.RuleKey]; !ok {
				order = append(order, r.RuleKey)
			}
			byKey[r.RuleKey] = r
		}
	}
	apply("global", "")
	if group != "" {
		apply("group", group)
	}
	apply("server", fmt.Sprint(serverID))
	out := make([]AlertRule, 0, len(order))
	for _, k := range order {
		if r := byKey[k]; r.Enabled {
			out = append(out, r)
		}
	}
	return out
}

// alertInput 是评估一个节点所需的数据；字段为空表示该项没有数据（对应规则本轮跳过）。
type alertInput struct {
	OfflineFor time.Duration    // 距最后一次上报的时长
	Report     *protocol.Report // 最新上报；离线时为 nil（资源规则暂停评估，设计 16.3 NODATA）
	Traffic    *trafficView     // 本周期流量；未计算时为 nil
}

// alertValue 取规则对应的当前值与说明；ok 为 false 表示没有数据，本轮不评估。
// 说明用于告警消息，例如磁盘告警指出是哪个挂载点。
func alertValue(typ string, in alertInput) (v float64, detail string, ok bool) {
	r := in.Report
	switch typ {
	case AlertOffline:
		return in.OfflineFor.Seconds(), "", true
	case AlertCPU:
		if r != nil {
			return r.CPU.Usage, "", true
		}
	case AlertMemory:
		if r != nil {
			return r.Memory.Usage, "", true
		}
	case AlertDisk:
		// 取使用率最高的挂载点：快满的数据盘不能被根分区掩盖（设计 1.5.6）
		if r != nil && len(r.Disk) > 0 {
			best := r.Disk[0]
			for _, d := range r.Disk[1:] {
				if d.Usage > best.Usage {
					best = d
				}
			}
			return best.Usage, best.Mount, true
		}
	case AlertSwap:
		if r != nil && r.Swap.Total > 0 {
			return float64(r.Swap.Used) * 100 / float64(r.Swap.Total), "", true
		}
	case AlertLoad:
		// 负载按核数归一：阈值 2 表示 load1 为核数的 2 倍
		if r != nil && r.CPU.Cores > 0 {
			return r.CPU.Load1 / float64(r.CPU.Cores), "", true
		}
	case AlertTraffic:
		if t := in.Traffic; t != nil && t.Limit > 0 {
			return float64(t.Used) * 100 / float64(t.Limit), "", true
		}
	case AlertTrafficForecast:
		if t := in.Traffic; t != nil && t.Limit > 0 && t.Forecast != nil {
			return float64(t.Forecast.Total) * 100 / float64(t.Limit), "", true
		}
	}
	return 0, "", false
}

// alertMessage 生成告警消息（设计 16.5 模板的第一行，不含节点名与图标）。
func alertMessage(r AlertRule, v float64, detail string) string {
	pct := func(x float64) string { return fmt.Sprintf("%.0f%%", math.Round(x)) }
	switch r.Type {
	case AlertOffline:
		return "节点离线，已 " + fmtAge(time.Duration(v)*time.Second) + "未收到上报"
	case AlertCPU:
		return fmt.Sprintf("CPU 使用率 %s（阈值 %s）", pct(v), pct(r.Threshold))
	case AlertMemory:
		return fmt.Sprintf("内存使用率 %s（阈值 %s）", pct(v), pct(r.Threshold))
	case AlertDisk:
		return fmt.Sprintf("磁盘 %s 使用率 %s（阈值 %s）", detail, pct(v), pct(r.Threshold))
	case AlertSwap:
		return fmt.Sprintf("Swap 使用率 %s（阈值 %s）", pct(v), pct(r.Threshold))
	case AlertLoad:
		return fmt.Sprintf("负载为核数的 %.1f 倍（阈值 %.1f 倍）", v, r.Threshold)
	case AlertTraffic:
		return fmt.Sprintf("本周期流量已用 %s（阈值 %s）", pct(v), pct(r.Threshold))
	case AlertTrafficForecast:
		return fmt.Sprintf("预计周期结束用量为额度的 %s", pct(v))
	}
	return r.Type
}

func fmtAge(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%d 小时", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	}
	return fmt.Sprintf("%d 秒", int(d.Seconds()))
}

// 告警状态（设计 16.3）。pending 只保存在内存中：面板重启后重新计时，宁可晚一点也不误报。
const (
	StatePending  = "pending"
	StateFiring   = "firing"
	StateResolved = "resolved"
)

// alertState 是一个节点一条规则的活动告警。
type alertState struct {
	State        string
	EventID      int64 // firing 后写入 alert_events 的记录 ID
	StartedAt    time.Time
	FiredAt      time.Time
	RecoverSince time.Time // 满足恢复条件的起点；零值表示当前不满足
	Value        float64
	Detail       string
}

// alertTransition 是一次评估带来的变化，由调用方据此持久化与通知。
type alertTransition int

const (
	alertNone      alertTransition = iota
	alertFired                     // 进入 firing：写入告警事件（后续发送通知）
	alertResolved                  // firing 满足恢复条件并持续：结束事件（后续发送恢复通知）
	alertCancelled                 // pending 期间条件不再满足：直接丢弃，不留记录
)

// advanceAlert 按设计 16.3 推进状态机：
//
//	无 ──条件满足──► pending ──持续 DurationS──► firing ──低于恢复阈值持续 RecoverDurationS──► 无（resolved）
//	pending 期间条件不再满足 → 无（cancelled）
//	firing 期间值回到恢复阈值与触发阈值之间 → 保持 firing（回差），恢复计时清零
//
// st 为 nil 表示当前没有活动告警；返回的状态为 nil 表示没有活动告警。
func advanceAlert(st *alertState, r AlertRule, v float64, detail string, now time.Time) (*alertState, alertTransition) {
	triggered := v > r.Threshold || (r.Operator == ">=" && v == r.Threshold)
	recovered := v < r.RecoverThreshold
	dur := time.Duration(r.DurationS) * time.Second
	recDur := time.Duration(r.RecoverDurationS) * time.Second

	if st == nil {
		if !triggered {
			return nil, alertNone
		}
		st = &alertState{State: StatePending, StartedAt: now, Value: v, Detail: detail}
		if dur <= 0 {
			st.State, st.FiredAt = StateFiring, now
			return st, alertFired
		}
		return st, alertNone
	}
	next := *st
	next.Value, next.Detail = v, detail
	switch st.State {
	case StatePending:
		if !triggered {
			return nil, alertCancelled
		}
		if now.Sub(st.StartedAt) >= dur {
			next.State, next.FiredAt = StateFiring, now
			return &next, alertFired
		}
	case StateFiring:
		if !recovered {
			next.RecoverSince = time.Time{}
			return &next, alertNone
		}
		if next.RecoverSince.IsZero() {
			next.RecoverSince = now
		}
		if now.Sub(next.RecoverSince) >= recDur {
			return nil, alertResolved
		}
	}
	return &next, alertNone
}
