package report

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	headers  map[int]string   // 状态码 → Retry-After
	date     func() time.Time // 响应的 Date 头；与 Reporter 使用同一个假时钟，避免误报时钟偏差
	sentAt   []int64
	interval string // X-Report-Interval 响应头
	gzip     string // "accept"：声明并接受 gzip；"reject"：收到 gzip 返回 415（模拟回退到旧版面板）；空：旧版面板
	encoded  []string
}

func (p *fakePanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	code := http.StatusNoContent
	if len(p.codes) > 0 {
		code, p.codes = p.codes[0], p.codes[1:]
	}
	if p.date != nil {
		w.Header().Set("Date", p.date().UTC().Format(http.TimeFormat))
	}
	if ra := p.headers[code]; ra != "" {
		w.Header().Set("Retry-After", ra)
	}
	enc := r.Header.Get("Content-Encoding")
	p.encoded = append(p.encoded, enc)
	if p.gzip == "accept" {
		w.Header().Set("Accept-Encoding", "gzip")
	}
	if p.interval != "" {
		w.Header().Set("X-Report-Interval", p.interval)
	}
	if enc == "gzip" && p.gzip != "accept" {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	if code/100 == 2 {
		var rep protocol.Report
		var body io.Reader = r.Body
		if enc == "gzip" {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			body = zr
		}
		json.NewDecoder(body).Decode(&rep)
		p.received = append(p.received, rep.Timestamp)
		p.sentAt = append(p.sentAt, rep.SentAt)
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
	if p.date == nil {
		p.date = c.now
	}
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

// 时钟偏差（设计 43.5）：上报带发送时刻；本机与面板相差超过 60 秒时记录 WARN，每小时最多一次。
func TestClockSkew(t *testing.T) {
	p := &fakePanel{}
	r, c, logs := newReporter(t, p)
	panelClock := c.now().Add(-5 * time.Minute) // 本机比面板快 5 分钟
	p.date = func() time.Time { return panelClock }
	r.Enqueue(rep(-600)) // 10 分钟前采集的补发数据
	r.Flush(context.Background(), false)
	if len(p.sentAt) != 1 || p.sentAt[0] != c.now().Unix() {
		t.Errorf("sent_at 应为发送时刻而不是采集时间：%v", p.sentAt)
	}
	if st := r.Status(); st.ClockSkew != 300 {
		t.Errorf("状态中应记录偏差 300 秒：%d", st.ClockSkew)
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "5m0s ahead") {
		t.Fatalf("应记录一次 WARN：%v", *logs)
	}
	c.add(10 * time.Second)
	r.Enqueue(rep(10))
	r.Flush(context.Background(), false)
	if len(*logs) != 1 {
		t.Errorf("一小时内不重复记录：%v", *logs)
	}
	p.date = c.now // 时钟恢复
	r.Enqueue(rep(20))
	r.Flush(context.Background(), false)
	if st := r.Status(); st.ClockSkew != 0 {
		t.Errorf("恢复后偏差应为 0：%d", st.ClockSkew)
	}
}

// 压缩（设计 6.1）：面板声明支持后才压缩；面板回退到旧版本时改发未压缩的版本，上报不丢。
func TestGzipNegotiation(t *testing.T) {
	p := &fakePanel{gzip: "accept"}
	r, _, logs := newReporter(t, p)
	for i := int64(0); i < 3; i++ {
		r.Enqueue(rep(i))
	}
	r.Flush(context.Background(), false)
	if strings.Join(p.encoded, ",") != ",gzip,gzip" || len(p.received) != 3 {
		t.Fatalf("第一份未压缩（尚不知面板是否支持），之后压缩：%q，收到 %d 份", p.encoded, len(p.received))
	}

	p.gzip, p.encoded = "reject", nil // 面板回退到旧版本
	r.Enqueue(rep(3))
	r.Enqueue(rep(4))
	r.Flush(context.Background(), false)
	if strings.Join(p.encoded, ",") != "gzip,," || len(p.received) != 5 {
		t.Errorf("被拒绝后应立即改发未压缩的版本，不丢上报：%q，收到 %d 份", p.encoded, len(p.received))
	}
	if !strings.Contains(strings.Join(*logs, "\n"), "sending uncompressed") {
		t.Errorf("应记录改为不压缩：%v", *logs)
	}

	old := &fakePanel{} // 旧版面板：从不声明，Agent 永不压缩
	r2, _, _ := newReporter(t, old)
	r2.Enqueue(rep(0))
	r2.Enqueue(rep(1))
	r2.Flush(context.Background(), false)
	if strings.Join(old.encoded, ",") != "," {
		t.Errorf("旧版面板不应收到压缩的上报：%q", old.encoded)
	}
}

func TestAcceptsGzip(t *testing.T) {
	for v, want := range map[string]bool{"gzip": true, "br, GZIP;q=0.5": true, "identity": false, "": false, "x-gzip2": false} {
		if got := acceptsGzip([]string{v}); got != want {
			t.Errorf("acceptsGzip(%q) = %v", v, got)
		}
	}
}

// 面板按节点下发采样间隔（设计 4.2、6.1）：只接受 5～60 秒，范围外或格式错误时保持当前值（设计 1.6.8 受限配置）。
func TestPanelInterval(t *testing.T) {
	p := &fakePanel{}
	r, _, _ := newReporter(t, p)
	send := func(h string) time.Duration {
		p.interval = h
		r.Enqueue(rep(0))
		r.Flush(context.Background(), false)
		return r.Interval()
	}
	if got := send(""); got != 0 {
		t.Errorf("旧版面板不下发：%s", got)
	}
	for _, c := range []struct {
		h    string
		want time.Duration
	}{
		{"30", 30 * time.Second},
		{"5", 5 * time.Second},
		{"1", 5 * time.Second},   // 低于下限：忽略
		{"300", 5 * time.Second}, // 高于上限（超过 watchdog 的一半）：忽略
		{"abc", 5 * time.Second}, // 格式错误：忽略
		{"60", 60 * time.Second},
	} {
		if got := send(c.h); got != c.want {
			t.Errorf("X-Report-Interval: %s → %s，应为 %s", c.h, got, c.want)
		}
	}
}

// 面板响应很慢时，一轮发送不超过时间预算，剩余的留在队列中（设计 43.5：主循环不能被拖住）。
func TestFlushBudget(t *testing.T) {
	old := flushBudget
	flushBudget = 100 * time.Millisecond
	defer func() { flushBudget = old }()
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(slow)
	defer srv.Close()
	r := &Reporter{Endpoint: srv.URL, Token: "agt_test", Client: srv.Client(), Logf: func(string, ...any) {}}
	for i := int64(0); i < 10; i++ {
		r.Enqueue(protocol.Report{Timestamp: time.Now().Unix() + i})
	}
	start := time.Now()
	r.Flush(context.Background(), false)
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Errorf("一轮发送应在预算内结束：%s", d)
	}
	if q := r.Status().Queued; q == 0 || q == 10 {
		t.Errorf("应发出一部分、其余留在队列中：剩余 %d", q)
	}
}
