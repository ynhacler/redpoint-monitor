// Package report 负责 Agent 向面板推送上报：有上限的内存缓冲、指数退避重试，
// 以及按面板响应码决定后续行为（设计 1.6.14、43.5）。
//
// 【安全】只推不拉：响应体只用来读取错误码，面板返回的任何内容都不能让 Agent 执行动作（设计 1.6.8）。
// 不负责：采集（collector 包）、本地安装（setup 包）。
package report

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
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
	// 重启前最后一份计数（设计 1.6.14、5.5）：面板不可达期间主机重启时，旧启动的网卡计数一旦丢失，
	// 从最后一次成功上报到重启之间的流量就再也补不回来。每个旧启动保留最后一份，不受 MaxAge 限制，
	// 最多保留最近 3 次启动、7 天以内。
	maxCarryBoots = 3
	maxCarryAge   = 7 * 24 * time.Hour
)

// flushBudget：一轮发送的时间预算。面板响应很慢时不再继续补发，剩余的留到下一轮，
// 保证主循环每轮足够短，不会被看门狗误判为卡死（设计 43.5）。测试中可缩短。
var flushBudget = 20 * time.Second

// Status 是供 vpsmon-agent status 显示的上报状态（设计 24.5）。
type Status struct {
	LastAttempt int64 // Unix 秒
	LastSuccess int64 // 最近一次成功上报的采集时间，Unix 秒
	LastError   string
	Queued      int   // 等待补发的份数
	AuthFailed  bool  // 凭证已失效，已停止上报
	ClockSkew   int64 // 本机时钟与面板的偏差（秒，正数表示本机偏快）；按最近一次响应的 Date 头计算
}

// Reporter 缓存并发送上报。方法可并发调用（由内部锁保护队列与状态）。
type Reporter struct {
	Endpoint string // https://面板/api/v1/agent/report
	Token    string
	Client   *http.Client
	Logf     func(format string, args ...any) // 为空时用标准 log
	Now      func() time.Time                 // 测试中可替换
	// StatePath 是断网缓冲的落盘文件（如 /var/lib/vpsmon-agent/queue.json）；为空时只保存在内存中
	StatePath string

	mu          sync.Mutex // 保护以下字段；发送请求时不持有锁以外的资源
	queue       []protocol.Report
	failures    int
	nextAttempt time.Time
	authFailed  bool
	status      Status
	errLog      dedup

	// 压缩（设计 6.1）：面板在响应中声明 Accept-Encoding: gzip 后才压缩。只在发送 goroutine 中使用，
	// 压缩器与缓冲区复用：每次新建 gzip.Writer 要分配约 800 KB（设计 4.2）
	gzipOK       bool
	gzw          *gzip.Writer
	gzBuf        bytes.Buffer
	interval     time.Duration // 面板下发的采样间隔（X-Report-Interval）；0 表示面板未指定
	savedAt      time.Time     // 最近一次落盘时间
	skewLoggedAt time.Time     // 最近一次记录时钟偏差 WARN 的时间
	onDisk       bool          // 落盘文件存在，队列清空后需要删除
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
	r.queue = trimQueue(append(r.queue, rep), r.now())
}

// trimQueue 按缓存上限裁剪队列（设计 1.6.14），保持时间顺序：
//
//	普通上报      最近 MaxAge 内、最新的 MaxQueue 份
//	旧启动的计数  当前启动（队尾一份的 boot_id）之前的每次启动保留最后一份，最多 maxCarryBoots 次、maxCarryAge 以内，
//	              不受 MaxAge 限制：它是补齐重启前流量的唯一依据
func trimQueue(q []protocol.Report, now time.Time) []protocol.Report {
	if len(q) == 0 {
		return q
	}
	keep := make([]bool, len(q))
	cutoff := now.Add(-MaxAge).Unix()
	for i, n := len(q)-1, 0; i >= 0 && n < MaxQueue; i-- {
		if q[i].Timestamp >= cutoff {
			keep[i] = true
			n++
		}
	}
	seen := map[string]bool{q[len(q)-1].System.BootID: true}
	carryCutoff := now.Add(-maxCarryAge).Unix()
	for i, boots := len(q)-1, 0; i >= 0 && boots < maxCarryBoots; i-- {
		b := q[i].System.BootID
		if b == "" || seen[b] {
			continue
		}
		seen[b] = true
		if q[i].Timestamp >= carryCutoff {
			keep[i] = true
			boots++
		}
	}
	out := q[:0:0]
	for i, k := range keep {
		if k {
			out = append(out, q[i])
		}
	}
	return out
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
	start := time.Now()
	for i := 0; i < maxPerFlush && time.Since(start) < flushBudget; i++ {
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
//
// 面板声明接受 gzip 后压缩正文（约为原来的 45%，Agent 自身流量同样计入用户的套餐）。压缩的上报被拒绝
// （400 / 415，例如面板回退到不支持压缩的旧版本）时关闭压缩并立即改发未压缩的版本，这份上报不会被丢弃。
func (r *Reporter) post(ctx context.Context, rep protocol.Report) (int, time.Duration, error) {
	rep.SentAt = r.now().Unix() // 发送时刻，面板据此计算时钟偏差（设计 43.5）
	body, _ := json.Marshal(rep)
	r.mu.Lock()
	compress := r.gzipOK
	r.mu.Unlock()
	code, ra, err := r.send(ctx, body, compress)
	if compress && err == nil && (code == http.StatusBadRequest || code == http.StatusUnsupportedMediaType) {
		r.mu.Lock()
		r.gzipOK = false
		r.mu.Unlock()
		r.logf("report: panel rejected a compressed report (HTTP %d), sending uncompressed", code)
		return r.send(ctx, body, false)
	}
	return code, ra, err
}

// send 发送一次请求；compress 为 true 时按 gzip 压缩正文。
func (r *Reporter) send(ctx context.Context, body []byte, compress bool) (int, time.Duration, error) {
	payload := body
	if compress {
		r.gzBuf.Reset()
		if r.gzw == nil {
			r.gzw, _ = gzip.NewWriterLevel(&r.gzBuf, gzip.BestSpeed) // 上报很小，最快的级别已压到一半以下
		} else {
			r.gzw.Reset(&r.gzBuf)
		}
		if _, err := r.gzw.Write(body); err == nil && r.gzw.Close() == nil {
			payload = r.gzBuf.Bytes()
		} else {
			compress = false
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Content-Type", "application/json")
	if compress {
		req.Header.Set("Content-Encoding", "gzip")
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // 读完响应体以复用连接；内容不使用
	r.checkClock(resp.Header.Get("Date"))
	if resp.StatusCode/100 == 2 {
		r.setInterval(resp.Header.Get("X-Report-Interval"))
	}
	if !compress && resp.StatusCode/100 == 2 && acceptsGzip(resp.Header.Values("Accept-Encoding")) {
		r.mu.Lock()
		if !r.gzipOK {
			r.gzipOK = true
			r.logf("report: panel accepts gzip, compressing reports")
		}
		r.mu.Unlock()
	}
	var ra time.Duration
	if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
		ra = time.Duration(secs) * time.Second
	}
	return resp.StatusCode, ra, nil
}

// 面板可下发的采样间隔范围（设计 1.6.8 受限配置）：Agent 端的硬性限制，面板给出范围外的值时忽略。
// 下限 2 秒只用于按需实时模式：有人打开节点详情页时面板临时下发，页面关闭后恢复（设计 46.2）。
// 上限不超过 systemd WatchdogSec（120 秒）的一半，否则主循环喂狗不及时会被误判为卡死。
const (
	MinInterval = 2 * time.Second
	MaxInterval = 60 * time.Second
)

// setInterval 记录面板下发的采样间隔（秒）；格式错误或超出范围时忽略，保持当前值。
func (r *Reporter) setInterval(v string) {
	if v == "" {
		return
	}
	secs, err := strconv.Atoi(v)
	iv := time.Duration(secs) * time.Second
	if err != nil || iv < MinInterval || iv > MaxInterval {
		return
	}
	r.mu.Lock()
	r.interval = iv
	r.mu.Unlock()
}

// Interval 返回面板下发的采样间隔；面板未指定（旧版面板）时返回 0。
func (r *Reporter) Interval() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.interval
}

// acceptsGzip 判断 Accept-Encoding 响应头是否包含 gzip（RFC 7694）。
func acceptsGzip(values []string) bool {
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			name, _, _ := strings.Cut(strings.TrimSpace(part), ";")
			if strings.EqualFold(strings.TrimSpace(name), "gzip") {
				return true
			}
		}
	}
	return false
}

// maxClockSkew：与面板时钟相差超过此值时记录 WARN（设计 43.5）。面板同时据 sent_at 产生“Agent 时钟偏差”告警。
const maxClockSkew = 60 * time.Second

// checkClock 用面板响应的 Date 头（秒级精度）比对本机时钟，偏差过大时记录 WARN，每小时最多一次。
// 时钟偏差会让断网补发的数据落在错误的时间点上，也会影响计费日的划分。
func (r *Reporter) checkClock(date string) {
	t, err := http.ParseTime(date)
	if err != nil {
		return
	}
	now := r.now()
	skew := now.Sub(t)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.ClockSkew = int64(skew.Round(time.Second) / time.Second)
	if skew > -maxClockSkew && skew < maxClockSkew {
		return
	}
	if now.Sub(r.skewLoggedAt) < time.Hour {
		return
	}
	r.skewLoggedAt = now
	dir := "ahead of"
	if skew < 0 {
		dir, skew = "behind", -skew
	}
	r.logf("report: WARN local clock is %s %s the panel; check NTP (e.g. timedatectl status)", skew.Round(time.Second), dir)
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
