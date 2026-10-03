// Package report 负责 Agent 向面板推送上报：有上限的内存缓冲、指数退避重试，
// 以及按面板响应码决定后续行为（设计 1.6.14、43.5）。
//
// 【安全】只推不拉：响应体只用来读取错误码，面板返回的任何内容都不能让 Agent 执行动作（设计 1.6.8）。
// 不负责：采集（collector 包）、本地安装（setup 包）。
package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"

	"vpsmon/internal/protocol"
)

const (
	// MaxQueue / MaxAge：断网时缓存的上限（设计 1.6.14：必须有大小上限与时间上限）。
	// 10 秒间隔下 180 份约为 30 分钟；网卡计数是累计值，超出后丢弃的只是指标点，流量恢复后自动补齐。
	MaxQueue = 180
	MaxAge   = 30 * time.Minute
	// maxPerFlush：恢复连接后每轮最多补发的份数，避免一次性大量请求冲击面板
	maxPerFlush = 30
	// 退避：1 秒起，翻倍，最长 60 秒，带 ±20% 随机抖动，避免大量 Agent 同时重连（设计 43.5）
	backoffMin = time.Second
	backoffMax = time.Minute
	// authRetry：401 后停止上报，每小时用最新一份上报试探一次（凭证可能被管理员恢复）
	authRetry = time.Hour
)

// Status 是供 vpsmon-agent status 显示的上报状态（设计 24.5）。
type Status struct {
	LastAttempt int64 // Unix 秒
	LastSuccess int64 // 最近一次成功上报的采集时间，Unix 秒
	LastError   string
	Queued      int  // 等待补发的份数
	AuthFailed  bool // 凭证已失效，已停止上报
}

// Reporter 缓存并发送上报。方法可并发调用（由内部锁保护队列与状态）。
type Reporter struct {
	Endpoint string // https://面板/api/v1/agent/report
	Token    string
	Client   *http.Client
	Logf     func(format string, args ...any) // 为空时用标准 log
	Now      func() time.Time                 // 测试中可替换

	mu          sync.Mutex // 保护以下字段；发送请求时不持有锁以外的资源
	queue       []protocol.Report
	failures    int
	nextAttempt time.Time
	authFailed  bool
	status      Status
	errLog      dedup
}

func (r *Reporter) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Reporter) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// Enqueue 加入一份上报。超过数量或时间上限时丢弃最旧的；凭证失效期间只保留最新一份用于试探。
func (r *Reporter) Enqueue(rep protocol.Report) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.authFailed {
		r.queue = []protocol.Report{rep}
		return
	}
	r.queue = append(r.queue, rep)
	cutoff := r.now().Add(-MaxAge).Unix()
	drop := 0
	for drop < len(r.queue) && (len(r.queue)-drop > MaxQueue || r.queue[drop].Timestamp < cutoff) {
		drop++
	}
	if drop > 0 {
		r.queue = append([]protocol.Report(nil), r.queue[drop:]...)
	}
}

// NextAttempt 返回下一次允许发送的时间；队列为空时返回零值。
func (r *Reporter) NextAttempt() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.queue) == 0 {
		return time.Time{}
	}
	return r.nextAttempt
}

// Status 返回当前上报状态的副本。
func (r *Reporter) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.status
	st.Queued, st.AuthFailed = len(r.queue), r.authFailed
	return st
}

// Flush 按时间顺序发送缓存的上报，遇到失败即停止。force 为 true 时忽略退避时间（退出前补报，设计 5.5）。
func (r *Reporter) Flush(ctx context.Context, force bool) {
	for i := 0; i < maxPerFlush; i++ {
		r.mu.Lock()
		if len(r.queue) == 0 || (!force && r.now().Before(r.nextAttempt)) {
			r.mu.Unlock()
			return
		}
		rep := r.queue[0]
		r.status.LastAttempt = r.now().Unix()
		r.mu.Unlock()

		code, retryAfter, err := r.post(ctx, rep)

		r.mu.Lock()
		stop := r.handle(rep, code, retryAfter, err)
		r.mu.Unlock()
		if stop {
			return
		}
	}
}

// handle 按结果更新队列与状态，返回是否停止本轮发送（设计 43.5）。调用方持有 r.mu。
func (r *Reporter) handle(rep protocol.Report, code int, retryAfter time.Duration, err error) bool {
	now := r.now()
	switch {
	case err == nil && code/100 == 2:
		r.queue = r.queue[1:]
		r.failures, r.nextAttempt = 0, time.Time{}
		if r.authFailed {
			r.logf("report: credentials accepted again, resuming")
		}
		r.authFailed = false
		r.status.LastSuccess, r.status.LastError = rep.Timestamp, ""
		r.errLog.reset()
		return false

	case code == http.StatusUnauthorized:
		// Token 被吊销或节点已删除：停止上报，每小时记录一次并试探（设计 43.5）
		r.authFailed = true
		r.queue = r.queue[len(r.queue)-1:]
		r.nextAttempt = now.Add(authRetry)
		r.status.LastError = "401: 凭证已失效"
		r.logf("report: ERROR server rejected the agent token (401); reporting paused, will retry in %s. "+
			"Generate an enroll code on the node's install page in the panel, then run: sudo vpsmon-agent rotate-token --enroll ENR-…", authRetry)
		return true

	case code == http.StatusTooManyRequests:
		if retryAfter <= 0 {
			retryAfter = backoffMax
		}
		r.nextAttempt = now.Add(retryAfter)
		r.status.LastError = "429: 面板限流"
		r.errLog.log(r.logf, now, "429", fmt.Sprintf("report: rate limited, waiting %s", retryAfter))
		return true

	case err == nil && code/100 == 4:
		// 其他 4xx（如 400 格式错误、413 过大）重试也不会成功：丢弃这一份，继续发送后面的
		r.queue = r.queue[1:]
		r.status.LastError = fmt.Sprintf("%d: 上报被拒绝", code)
		r.errLog.log(r.logf, now, strconv.Itoa(code), fmt.Sprintf("report: server rejected a report with HTTP %d, dropped", code))
		return false

	default:
		// 网络错误或 5xx：保留在队列中，指数退避后重试
		r.failures++
		d := backoff(r.failures)
		r.nextAttempt = now.Add(d)
		msg := fmt.Sprintf("HTTP %d", code)
		if err != nil {
			msg = err.Error()
		}
		r.status.LastError = msg
		r.errLog.log(r.logf, now, msg, fmt.Sprintf("report failed: %s (queued %d, retry in %s)", msg, len(r.queue), d.Round(time.Second)))
		return true
	}
}

// backoff 返回第 n 次连续失败后的等待时间：1s、2s、4s…最长 60s，±20% 抖动。
func backoff(n int) time.Duration {
	d := backoffMin << min(n-1, 6)
	if d > backoffMax {
		d = backoffMax
	}
	jitter := 0.8 + 0.4*rand.Float64()
	return time.Duration(float64(d) * jitter)
}

// post 发送一份上报，返回状态码与 Retry-After。网络错误时 code 为 0。
func (r *Reporter) post(ctx context.Context, rep protocol.Report) (int, time.Duration, error) {
	body, _ := json.Marshal(rep)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.Client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // 读完响应体以复用连接；内容不使用
	var ra time.Duration
	if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
		ra = time.Duration(secs) * time.Second
	}
	return resp.StatusCode, ra, nil
}

// dedup 实现日志防刷屏：同一类错误（key 相同）1 分钟内只记录一次，下次记录时附带重复次数，
// 例如面板长时间不可达时每分钟一行 “report failed (x6): connection refused”（设计 24.5）。
// msg 中可以带变化的细节（队列长度、等待时间），不影响去重。
type dedup struct {
	last     string
	lastAt   time.Time
	repeated int
}

func (d *dedup) log(logf func(string, ...any), now time.Time, key, msg string) {
	if key == d.last && now.Sub(d.lastAt) < time.Minute {
		d.repeated++
		return
	}
	if key == d.last && d.repeated > 0 {
		logf("%s (x%d)", msg, d.repeated+1)
	} else {
		logf("%s", msg)
	}
	d.last, d.lastAt, d.repeated = key, now, 0
}

func (d *dedup) reset() { *d = dedup{} }
