package server

// 面板自身告警（设计 16.1“面板自身”，修订第 162 条）：面板自己的故障按问题分别发一条告警、恢复时发一条恢复通知。
// 与面板自检（设计 16.4）相同：发给所有渠道，不受级别与免打扰限制，不可关闭。状态只在内存中，面板重启后重新判断。
//
//	task:<名称>  后台任务 10 分钟内崩溃 3 次（runTask 已自动重启）；之后连续运行 10 分钟没有再崩溃为恢复
//	disk        数据目录所在磁盘剩余空间 < 10%；回到 ≥ 12% 为恢复（留出余量，避免在阈值附近反复通知）
//	db          批量写入连续失败 3 次；下一次写入成功为恢复
//	push        App 推送连续 5 次无法送达 Push Relay（连接失败、5xx、签名被拒）；下一次送达为恢复

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	taskFailWindow   = 10 * time.Minute // 统计任务崩溃次数的窗口
	taskFailAlertAt  = 3                // 窗口内崩溃几次后告警
	taskStableAfter  = 10 * time.Minute // 告警后连续运行多久算恢复
	diskLowPct       = 10.0             // 剩余空间低于该百分比告警
	diskOKPct        = 12.0             // 回到该百分比以上恢复
	diskCheckEvery   = 5 * time.Minute
	dbFailAlertAt    = 3
	pushFailAlertAt  = 5
	panelIssueDisk   = "disk"
	panelIssueDB     = "db"
	panelIssuePush   = "push"
	panelIssueTaskPf = "task:"
)

// taskNames 是后台任务的中文名，用于通知。
var taskNames = map[string]string{
	"maintenance": "数据维护", "alerts": "告警评估", "cloud": "云账户同步", "ws-status": "实时推送",
	"reminders": "到期提醒", "services": "服务监控", "heartbeat": "面板心跳", "release-sync": "版本同步", "panel-health": "面板自身检查",
}

// panelHealth 记录各问题是否正在告警，以及判断所需的计数。
type panelHealth struct {
	mu        sync.Mutex
	active    map[string]bool
	taskFails map[string][]time.Time
	taskLast  map[string]time.Time // 最近一次崩溃，用于判断恢复
	dbFails   int
	pushFails int
	// after 安排延迟检查；statDisk 读取数据目录的剩余空间（百分比、字节）。测试中替换
	after    func(time.Duration, func())
	statDisk func() (freePct float64, freeBytes uint64, err error)
}

func newPanelHealth(dataDir string) *panelHealth {
	return &panelHealth{active: map[string]bool{}, taskFails: map[string][]time.Time{}, taskLast: map[string]time.Time{},
		after:    func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		statDisk: func() (float64, uint64, error) { return diskFree(dataDir) }}
}

// set 更新问题 key 的状态；状态改变时返回 true（调用方发通知）。
func (h *panelHealth) set(key string, on bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active[key] == on {
		return false
	}
	h.active[key] = on
	return true
}

// panelIssue 在问题 key 出现或消失时发通知；detail 写在通知正文中。
func (s *Server) panelIssue(key string, on bool, detail string, count int) {
	if !s.health.set(key, on) {
		return
	}
	kind := NotifyPanelIssueResolved
	if on {
		kind = NotifyPanelIssue
		s.log.Error("panel issue", "component", "health", "issue", key, "detail", detail)
	} else {
		s.log.Info("panel issue resolved", "component", "health", "issue", key)
	}
	s.notify.dispatch(notifyMessage{Kind: kind, Severity: SeverityCritical, Type: key, Message: detail, Count: count,
		StartedAt: time.Now(), Link: s.publicURL})
}

// ---- 后台任务 ----

// taskCrashed 由 runTask 在任务 panic 后调用：10 分钟内第 3 次时告警，并安排恢复检查。
func (s *Server) taskCrashed(name string, now time.Time) {
	h := s.health
	h.mu.Lock()
	keep := h.taskFails[name][:0]
	for _, t := range h.taskFails[name] {
		if now.Sub(t) < taskFailWindow {
			keep = append(keep, t)
		}
	}
	h.taskFails[name] = append(keep, now)
	h.taskLast[name] = now
	n := len(h.taskFails[name])
	h.mu.Unlock()
	if n < taskFailAlertAt {
		return
	}
	key := panelIssueTaskPf + name
	if !s.health.isActive(key) {
		s.panelIssue(key, true, fmt.Sprintf("10 分钟内崩溃 %d 次，已自动重启", n), n)
		s.scheduleTaskCheck(name, now)
	}
}

func (h *panelHealth) isActive(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.active[key]
}

// scheduleTaskCheck 在 taskStableAfter 后检查：自 since 起没有再崩溃则恢复，否则从最近一次崩溃起继续等待。
func (s *Server) scheduleTaskCheck(name string, since time.Time) {
	s.health.after(taskStableAfter, func() {
		h := s.health
		h.mu.Lock()
		last := h.taskLast[name]
		stable := !last.After(since)
		if stable {
			h.taskFails[name] = nil
		}
		h.mu.Unlock()
		if stable {
			s.panelIssue(panelIssueTaskPf+name, false, "", 0)
			return
		}
		s.scheduleTaskCheck(name, last)
	})
}

// ---- 数据库写入 / 推送 ----

// dbWriteResult 由批量写入调用：连续失败 3 次告警，成功即恢复。
func (s *Server) dbWriteResult(ok bool) {
	h := s.health
	h.mu.Lock()
	if ok {
		h.dbFails = 0
	} else {
		h.dbFails++
	}
	n := h.dbFails
	h.mu.Unlock()
	switch {
	case ok:
		s.panelIssue(panelIssueDB, false, "", 0)
	case n >= dbFailAlertAt:
		s.panelIssue(panelIssueDB, true, fmt.Sprintf("连续 %d 次写入失败，上报暂存在内存中", n), n)
	}
}

// pushResult 由 App 推送调用：连续 5 次送不到 Relay 告警，送达即恢复。设备 Token 失效等单台设备的问题不计入。
func (s *Server) pushResult(ok bool) {
	h := s.health
	h.mu.Lock()
	if ok {
		h.pushFails = 0
	} else {
		h.pushFails++
	}
	n := h.pushFails
	h.mu.Unlock()
	switch {
	case ok:
		s.panelIssue(panelIssuePush, false, "", 0)
	case n >= pushFailAlertAt:
		s.panelIssue(panelIssuePush, true, fmt.Sprintf("连续 %d 次无法送达 Push Relay，App 收不到推送", n), n)
	}
}

// ---- 磁盘空间 ----

// panelHealthLoop 每 5 分钟检查数据目录的剩余空间（启动后 1 分钟第一次）。
func (s *Server) panelHealthLoop(ctx context.Context) {
	wait := time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = diskCheckEvery
		s.checkDisk()
	}
}

// checkDisk 剩余 < 10% 告警，回到 ≥ 12% 恢复；读取失败时只记日志，不改变状态。
func (s *Server) checkDisk() {
	pct, free, err := s.health.statDisk()
	if err != nil {
		s.log.Warn("stat data dir failed", "component", "health", "err", err)
		return
	}
	switch {
	case pct < diskLowPct:
		s.panelIssue(panelIssueDisk, true, fmt.Sprintf("数据目录所在磁盘剩余 %.1f%%（%s）", pct, fmtGB(free)), 0)
	case pct >= diskOKPct:
		s.panelIssue(panelIssueDisk, false, "", 0)
	}
}

func fmtGB(b uint64) string {
	if b >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	}
	return fmt.Sprintf("%d MB", b>>20)
}

// ---- 通知文本 ----

// panelIssueText 是面板自身告警的标题与说明。
func panelIssueText(m notifyMessage) (string, string) {
	what, okText, hint := m.Type, m.Type, ""
	switch {
	case strings.HasPrefix(m.Type, panelIssueTaskPf):
		name := strings.TrimPrefix(m.Type, panelIssueTaskPf)
		if n := taskNames[name]; n != "" {
			name = n
		}
		what, okText = "后台任务反复出错："+name, "后台任务运行正常："+name
		hint = "相关功能可能暂时失效；在面板主机上执行 vpsmon-server diag 生成诊断包查看原因。"
	case m.Type == panelIssueDisk:
		what, okText, hint = "磁盘空间不足", "磁盘空间已恢复", "清理磁盘或缩短指标保留时间；空间耗尽后面板无法写入数据。"
	case m.Type == panelIssueDB:
		what, okText, hint = "数据库写入失败", "数据库写入已恢复", "检查磁盘空间与数据目录权限；面板重启前未写入的上报会丢失。"
	case m.Type == panelIssuePush:
		what, okText, hint = "App 推送失败", "App 推送已恢复", "检查面板到 Push Relay 的网络与 --push-relay 设置；告警仍会通过其他渠道发送。"
	}
	if m.Kind == NotifyPanelIssueResolved {
		return "✅ 面板" + okText, ""
	}
	body := m.Message
	if hint != "" {
		body += "。" + hint
	}
	return "⚠️ 面板异常：" + what, body
}
