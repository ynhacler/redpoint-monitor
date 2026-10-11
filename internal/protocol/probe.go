package protocol

// 三网延迟（设计 42.3.2）：面板下发探测目标，Agent 每 5 分钟一轮测量到各目标的延迟与丢包，随上报带回。
//
// 下发方式：Agent 在上报请求头 X-Probe-Version 中带上当前目标列表的版本（没有时为空，本机关闭探测时为 off）；
// 版本与面板不同时，面板在响应头 X-Probe-Version 与 X-Probe-Targets 中给出新列表。旧版面板不发送，Agent 不探测；
// 旧版 Agent 不带请求头，面板不下发。
//
// 【安全】目标只能是公网 IP 地址（不接受域名，避免借 DNS 指向内网），类型只有 tcp / icmp，
// 数量、间隔、并发的上限写在 Agent 代码中，面板无法放宽（设计 42.6）。

import (
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"strconv"
	"strings"
)

const (
	HeaderProbeVersion = "X-Probe-Version"
	HeaderProbeTargets = "X-Probe-Targets"
	ProbeOff           = "off" // Agent 本机关闭了网络探测

	MaxProbeTargets = 64 // Agent 端硬性上限（设计 42.6）
)

// ProbeTarget 是一个探测目标。
type ProbeTarget struct {
	ID    int64
	Proto string // tcp / icmp
	Addr  netip.Addr
	Port  int // tcp 时 1～65535；icmp 时为 0
}

// ProbeResult 是一轮探测中一个目标的结果。
type ProbeResult struct {
	TargetID int64    `json:"target_id"`
	At       int64    `json:"at"`                   // 本轮开始时间（unix 秒，Agent 时钟）
	Latency  *float64 `json:"latency_ms,omitempty"` // 成功样本的中位数，毫秒；全部失败时省略
	Loss     float64  `json:"loss"`                 // 丢包率 0～100
	Error    string   `json:"error,omitempty"`      // 全部失败时的原因，如 icmp_unavailable、timeout、refused
}

// PublicProbeAddr 判断地址是否允许探测：只允许公网单播地址。
// 【安全】拒绝回环、内网（RFC 1918、ULA）、链路本地、组播、未指定、运营商级 NAT（100.64/10）与文档 / 保留地址。
func PublicProbeAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsUnspecified() || a.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("64:ff9b::/96"),
}

// Valid 判断目标是否合法（面板保存与 Agent 接收时都校验）。
func (t ProbeTarget) Valid() bool {
	if t.ID <= 0 || !PublicProbeAddr(t.Addr) {
		return false
	}
	switch t.Proto {
	case "tcp":
		return t.Port >= 1 && t.Port <= 65535
	case "icmp":
		return t.Port == 0
	}
	return false
}

// HostPort 返回 tcp 目标的连接地址。
func (t ProbeTarget) HostPort() string {
	return netip.AddrPortFrom(t.Addr.Unmap(), uint16(t.Port)).String()
}

// EncodeProbeTargets 编码目标列表：“id,proto,addr,port” 以分号分隔。
func EncodeProbeTargets(ts []ProbeTarget) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = strconv.FormatInt(t.ID, 10) + "," + t.Proto + "," + t.Addr.Unmap().String() + "," + strconv.Itoa(t.Port)
	}
	return strings.Join(parts, ";")
}

// ParseProbeTargets 解析目标列表：不合法或重复的项跳过，最多 MaxProbeTargets 个。
func ParseProbeTargets(v string) []ProbeTarget {
	var out []ProbeTarget
	seen := map[int64]bool{}
	for _, item := range strings.Split(v, ";") {
		f := strings.Split(strings.TrimSpace(item), ",")
		if len(f) != 4 {
			continue
		}
		id, err1 := strconv.ParseInt(f[0], 10, 64)
		addr, err2 := netip.ParseAddr(f[2])
		port, err3 := strconv.Atoi(f[3])
		t := ProbeTarget{ID: id, Proto: f[1], Addr: addr, Port: port}
		if err1 != nil || err2 != nil || err3 != nil || !t.Valid() || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, t)
		if len(out) == MaxProbeTargets {
			break
		}
	}
	return out
}

// ProbeVersion 是目标列表的版本：编码结果的哈希前 12 位；没有目标时为 "0"。
func ProbeVersion(encoded string) string {
	if encoded == "" {
		return "0"
	}
	sum := sha256.Sum256([]byte(encoded))
	return hex.EncodeToString(sum[:6])
}
