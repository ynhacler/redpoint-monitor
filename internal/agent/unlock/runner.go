package unlock

import (
	"context"
	"net/http"
	"sync"
	"time"

	"vpsmon/internal/protocol"
)

// Runner 管理面板的检测请求（设计 50）：每个请求号只检测一次，在后台进行，结果只随一份上报带回。
// 方法可并发调用：Request 由上报协程调用，Drain 由主循环调用。
type Runner struct {
	Client *http.Client // 为空时使用默认客户端
	Logf   func(string, ...any)
	Now    func() time.Time // 测试中可替换

	mu      sync.Mutex
	last    string // 最近一次处理的请求号
	running bool
	result  *protocol.UnlockResult
	run     func(context.Context, *http.Client) []protocol.UnlockItem // 测试中替换
}

// Supported 是请求头 X-Unlock 的值。
func (r *Runner) Supported() string { return protocol.UnlockVersion }

// Request 收到面板下发的请求号：新的请求号且没有检测在进行时，在后台开始检测。
func (r *Runner) Request(id string) {
	if id == "" || len(id) > 64 {
		return
	}
	r.mu.Lock()
	if id == r.last || r.running {
		r.mu.Unlock()
		return
	}
	r.last, r.running = id, true
	run := r.run
	r.mu.Unlock()
	if run == nil {
		run = Run
	}
	if r.Logf != nil {
		r.Logf("unlock: check requested by panel (%s)", id)
	}
	go func() {
		items := run(context.Background(), r.Client)
		now := time.Now
		if r.Now != nil {
			now = r.Now
		}
		r.mu.Lock()
		r.result = &protocol.UnlockResult{Request: id, CheckedAt: now().Unix(), Items: items}
		r.running = false
		r.mu.Unlock()
	}()
}

// Drain 取出已完成的结果（只返回一次）；没有时返回 nil。上报失败时结果随这份上报一起补发。
func (r *Runner) Drain() *protocol.UnlockResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := r.result
	r.result = nil
	return res
}
