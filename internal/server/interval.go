package server

import (
	"net/http"
	"slices"
	"strconv"
	"time"
)

// 采样（上报）间隔可按节点选择（设计 4.2、6.1）。面板通过上报响应头 X-Report-Interval 告诉 Agent，
// 属于“受限配置”（设计 1.6.8）：只是一个数字，Agent 端另有 5～300 秒的硬性范围，旧版 Agent 忽略该头。
// 间隔变长后，在线判定与离线告警按间隔放宽，避免漏一次上报就显示离线。

// DefaultReportInterval 是未设置时的采样间隔（设计 4.2）。
const DefaultReportInterval = 10 * time.Second

// reportIntervals 是 Web 中可选的采样间隔（秒）。
var reportIntervals = []int{5, 10, 15, 30, 60}

// ValidReportInterval 判断是否为可选的采样间隔。
func ValidReportInterval(s int) bool { return slices.Contains(reportIntervals, s) }

// reportInterval 返回节点的采样间隔；未设置时为默认 10 秒。
func reportInterval(row ServerRow) time.Duration {
	if row.ReportIntervalS > 0 {
		return time.Duration(row.ReportIntervalS) * time.Second
	}
	return DefaultReportInterval
}

// statusWindows 返回在线与“状态未知”的判定窗口（设计 22）：默认 30 秒 / 120 秒，
// 间隔变长时放宽为 3 个 / 6 个周期，取较大者。
func statusWindows(iv time.Duration) (online, unknown time.Duration) {
	return max(onlineWithin, 3*iv), max(unknownWithin, 6*iv)
}

// ruleForInterval 按节点的采样间隔调整规则：离线告警的阈值至少为 3 个周期，
// 例如 60 秒间隔时，偶尔漏一次上报（120 秒）不算离线。其他规则不变。
func ruleForInterval(r AlertRule, iv time.Duration) AlertRule {
	if r.Type == AlertOffline {
		r.Threshold = max(r.Threshold, (3 * iv).Seconds())
	}
	return r
}

// setIntervalHeader 在上报响应中告诉 Agent 采样间隔；节点信息读取失败时不设置（Agent 保持当前间隔）。
func (s *Server) setIntervalHeader(w http.ResponseWriter, sid int64) {
	s.mu.Lock()
	iv, ok := s.intervals[sid]
	s.mu.Unlock()
	if !ok {
		row, err := s.store.GetServer(sid)
		if err != nil {
			return
		}
		iv = reportInterval(*row)
		s.mu.Lock()
		s.intervals[sid] = iv
		s.mu.Unlock()
	}
	w.Header().Set("X-Report-Interval", strconv.Itoa(int(iv/time.Second)))
}

// forgetInterval 在节点修改或删除后清除缓存的采样间隔，下一份上报重新读取。
func (s *Server) forgetInterval(sid int64) {
	s.mu.Lock()
	delete(s.intervals, sid)
	s.mu.Unlock()
}
