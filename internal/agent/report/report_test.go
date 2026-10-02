package report

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

// fakePanel 按脚本依次返回状态码，并记录收到的上报时间戳。
type fakePanel struct {
	mu       sync.Mutex
	codes    []int // 依次使用；用完后返回 204
	received []int64
	headers  map[int]string // 状态码 → Retry-After
}

func (p *fakePanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	code := http.StatusNoContent
	if len(p.codes) > 0 {
		code, p.codes = p.codes[0], p.codes[1:]
	}
	if ra := p.headers[code]; ra != "" {
		w.Header().Set("Retry-After", ra)
	}
	if code/100 == 2 {
		var rep protocol.Report
		json.NewDecoder(r.Body).Decode(&rep)
		p.received = append(p.received, rep.Timestamp)
	}
	w.WriteHeader(code)
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

// rep 生成采集时间为 base+offset 秒的上报；base 与 newReporter 的起始时钟一致
func rep(offset int64) protocol.Report { return protocol.Report{Timestamp: base + offset} }

const base = 1_800_000_000

func newReporter(t *testing.T, p *fakePanel) (*Reporter, *clock, *[]string) {
	t.Helper()
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	c := &clock{t: time.Unix(base, 0)}
	var logs []string
	r := &Reporter{Endpoint: srv.URL, Token: "agt_test", Client: srv.Client(), Now: c.now,
		Logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }}
	return r, c, &logs
}

func TestSendInOrder(t *testing.T) {
	p := &fakePanel{}
	r, _, _ := newReporter(t, p)
	for i := int64(0); i < 3; i++ {
		r.Enqueue(rep(i))
	}
	r.Flush(context.Background(), false)
	if len(p.received) != 3 || p.received[0] > p.received[2] {
		t.Errorf("应按采集时间顺序全部发送：%v", p.received)
	}
	if st := r.Status(); st.Queued != 0 || st.LastSuccess != base+2 || st.LastError != "" {
		t.Errorf("状态不正确：%+v", st)
	}
}

// 断网 / 5xx：保留在队列中，指数退避后按顺序补发（设计 1.6.14、43.5）。
func TestOutageBufferAndBackoff(t *testing.T) {
	p := &fakePanel{codes: []int{503, 503, 503}}
	r, c, logs := newReporter(t, p)
	start := c.now()
	var waits []time.Duration
	for i := 0; i < 3; i++ {
		r.Enqueue(rep(c.now().Unix() - base))
		r.Flush(context.Background(), false)
		waits = append(waits, r.NextAttempt().Sub(c.now()))
		c.add(r.NextAttempt().Sub(c.now()))
	}
	if r.Status().Queued != 3 {
		t.Fatalf("失败的上报应留在队列中：%d", r.Status().Queued)
	}
	for i, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		if waits[i] < want*8/10 || waits[i] > want*12/10 {
			t.Errorf("第 %d 次失败后应等待约 %s（±20%%），实际 %s", i+1, want, waits[i])
		}
	}
	// 退避期内即使有新数据也不发送
	r.Enqueue(rep(c.now().Unix() - base))
	before := len(p.received)
	r.Flush(context.Background(), false)
	if len(p.received) != before+4 {
		t.Errorf("恢复后应一次补发全部 4 份：%d", len(p.received)-before)
	}
	if p.received[0] != start.Unix() {
		t.Errorf("应从最早的一份开始补发：%v", p.received)
	}
	if len(*logs) == 0 || !strings.Contains((*logs)[0], "503") {
		t.Errorf("失败应记录日志：%v", *logs)
	}
	if backoff(20) > backoffMax*12/10 {
		t.Error("退避上限应为 60 秒")
	}
}

// 缓存必须有大小上限与时间上限（设计 1.6.14）。
func TestQueueLimits(t *testing.T) {
	r, c, _ := newReporter(t, &fakePanel{})
	for i := 0; i < MaxQueue+20; i++ {
		r.Enqueue(rep(c.now().Unix() - base))
	}
	if q := r.Status().Queued; q != MaxQueue {
		t.Errorf("超过数量上限应丢弃最旧的：%d", q)
	}
	r2, c2, _ := newReporter(t, &fakePanel{})
	r2.Enqueue(rep(c2.now().Unix() - base))
	c2.add(MaxAge + time.Minute)
	r2.Enqueue(rep(c2.now().Unix() - base))
	if q := r2.Status().Queued; q != 1 {
		t.Errorf("超过时间上限的应被丢弃：%d", q)
	}
}

// 401：停止上报，只保留最新一份，每小时试探一次；恢复后继续（设计 43.5）。
func TestUnauthorizedPausesReporting(t *testing.T) {
	p := &fakePanel{codes: []int{401}}
	r, c, logs := newReporter(t, p)
	r.Enqueue(rep(1))
	r.Enqueue(rep(2))
	r.Flush(context.Background(), false)
	st := r.Status()
	if !st.AuthFailed || !strings.Contains(st.LastError, "401") {
		t.Fatalf("401 后应标记凭证失效：%+v", st)
	}
	if !strings.Contains(strings.Join(*logs, "\n"), "ERROR") {
		t.Error("应记录 ERROR")
	}
	for i := 0; i < 50; i++ {
		r.Enqueue(rep(int64(10 + i)))
	}
	if q := r.Status().Queued; q != 1 {
		t.Errorf("凭证失效期间只保留最新一份：%d", q)
	}
	c.add(30 * time.Minute)
	r.Flush(context.Background(), false)
	if len(p.received) != 0 {
		t.Error("一小时内不应再次尝试")
	}
	c.add(31 * time.Minute)
	r.Flush(context.Background(), false)
	if len(p.received) != 1 || r.Status().AuthFailed {
		t.Errorf("一小时后试探成功应恢复上报：%v %+v", p.received, r.Status())
	}
}

func TestRetryAfterAndOther4xx(t *testing.T) {
	p := &fakePanel{codes: []int{429}, headers: map[int]string{429: "17"}}
	r, c, _ := newReporter(t, p)
	r.Enqueue(rep(1))
	r.Flush(context.Background(), false)
	if wait := r.NextAttempt().Sub(c.now()); wait != 17*time.Second {
		t.Errorf("429 应按 Retry-After 等待：%s", wait)
	}

	p2 := &fakePanel{codes: []int{400}}
	r2, _, _ := newReporter(t, p2)
	r2.Enqueue(rep(1))
	r2.Enqueue(rep(2))
	r2.Flush(context.Background(), false)
	if len(p2.received) != 1 || p2.received[0] != base+2 || r2.Status().Queued != 0 {
		t.Errorf("其他 4xx 应丢弃该份并继续发送后面的：%v", p2.received)
	}
}

// 退出前补报忽略退避时间（设计 5.5）。
func TestForceFlush(t *testing.T) {
	p := &fakePanel{codes: []int{503}}
	r, _, _ := newReporter(t, p)
	r.Enqueue(rep(1))
	r.Flush(context.Background(), false)
	r.Enqueue(rep(2))
	r.Flush(context.Background(), true)
	if len(p.received) != 2 {
		t.Errorf("force 应立即补发：%v", p.received)
	}
}

// 同类错误 1 分钟内只记录一次，之后附带重复次数（设计 24.5）。
func TestLogDedup(t *testing.T) {
	var out []string
	logf := func(f string, a ...any) { out = append(out, fmt.Sprintf(f, a...)) }
	var d dedup
	t0 := time.Unix(0, 0)
	for i := 0; i < 6; i++ {
		d.log(logf, t0.Add(time.Duration(i)*10*time.Second), "refused", fmt.Sprintf("failed (queued %d)", i))
	}
	d.log(logf, t0.Add(70*time.Second), "refused", "failed (queued 7)")
	if len(out) != 2 || !strings.HasSuffix(out[1], "(x6)") {
		t.Errorf("应只记录两行，第二行带重复次数：%q", out)
	}
}
