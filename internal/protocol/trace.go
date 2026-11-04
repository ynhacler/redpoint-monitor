package protocol

// 回程线路识别（设计 42.5，修订第 282 条）：面板在下发三网延迟目标时，用响应头 X-Probe-Trace 指定其中最多 3 个目标
// （电信、联通、移动各一个）做路由追踪；Agent 每 6 小时追踪一次，把逐跳地址与耗时随上报带回，由面板判断线路类型。
//
// 【安全】只能追踪已下发的三网延迟目标（同样只限公网地址，域名按同样规则解析），个数、间隔与跳数上限写在 Agent 代码中，
// 面板无法放宽（设计 42.6）。追踪只发送 ICMP 回显（逐跳递增 TTL），用非特权 ICMP 套接字；本机不允许时报告 icmp_unavailable。
// 旧版面板不发该响应头，Agent 不追踪；旧版 Agent 忽略该响应头。

import (
	"strconv"
	"strings"
)

// 路由追踪的响应头与上限（设计 42.5、42.6）
const (
	HeaderProbeTrace = "X-Probe-Trace" // 要追踪的目标编号，逗号分隔

	MaxTraceTargets = 3  // 每台节点最多追踪几个目标
	MaxTraceHops    = 30 // 最多几跳
)

// TraceHop 是一跳：TTL 从 1 开始；没有回复的跳省略 IP 与耗时。
type TraceHop struct {
	TTL int      `json:"ttl"`
	IP  string   `json:"ip,omitempty"`
	RTT *float64 `json:"rtt_ms,omitempty"` // 毫秒，保留 1 位小数
}

// TraceResult 是对一个目标的一次路由追踪。
type TraceResult struct {
	TargetID int64      `json:"target_id"`
	At       int64      `json:"at"`                // 开始时间（unix 秒，Agent 时钟）
	Reached  bool       `json:"reached,omitempty"` // 是否收到目标本身的回复
	Hops     []TraceHop `json:"hops,omitempty"`
	Error    string     `json:"error,omitempty"` // 无法追踪的原因，如 icmp_unavailable、dns、blocked
}

// ParseTraceIDs 解析 X-Probe-Trace：只取正整数，去重，最多 MaxTraceTargets 个。
func ParseTraceIDs(v string) []int64 {
	var out []int64
	for _, f := range strings.Split(v, ",") {
		n, err := strconv.ParseInt(strings.TrimSpace(f), 10, 64)
		if err != nil || n <= 0 || containsID(out, n) {
			continue
		}
		out = append(out, n)
		if len(out) == MaxTraceTargets {
			break
		}
	}
	return out
}

// EncodeTraceIDs 编码为 X-Probe-Trace 的值。
func EncodeTraceIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

func containsID(list []int64, id int64) bool {
	for _, x := range list {
		if x == id {
			return true
		}
	}
	return false
}
