package protocol

// 三网延迟（设计 42.3.2）：面板下发探测目标，Agent 每 5 分钟一轮测量到各目标的延迟与丢包，随上报带回。
//
// 下发方式：Agent 在上报请求头 X-Probe-Version 中带上当前目标列表的版本（没有时为空，本机关闭探测时为 off）；
// 版本与面板不同时，面板在响应头 X-Probe-Version、X-Probe-Targets 与 X-Probe-Interval 中给出新列表与间隔。旧版面板不发送，Agent 不探测；
// 旧版 Agent 不带请求头，面板不下发。
//
// 【安全】目标是公网 IP（IPv4 / IPv6）或域名（修订第 132 条）。域名由 Agent 每轮解析，只使用解析结果中的公网地址，
// 解析到内网、回环等地址时拒绝探测，因此不能借 DNS 让 Agent 访问内网。类型只有 tcp / icmp，
// 数量、间隔、并发的上限写在 Agent 代码中，面板无法放宽（设计 42.6）。旧版 Agent 不认识域名目标，跳过它们。

import (
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"strconv"
	"strings"
)

// 三网延迟下发用的请求 / 响应头与上限（设计 42.3.2、42.6）
const (
	HeaderProbeVersion  = "X-Probe-Version"
	HeaderProbeTargets  = "X-Probe-Targets"
	HeaderProbeInterval = "X-Probe-Interval" // 每轮间隔（秒），与目标列表一起下发
	HeaderProbeTiming   = "X-Probe-Timing"   // 每个样本的超时与样本间隔（秒）：“timeout,gap”（修订第 133 条）；旧版 Agent 忽略
	ProbeOff            = "off"              // Agent 本机关闭了网络探测

	MaxProbeTargets = 64 // Agent 端硬性上限（设计 42.6）

	// MinProbeInterval 等是每轮间隔的范围与默认值：面板可在此范围内设置；Agent 端硬性限制，范围外的值按默认处理（设计 42.6）
	MinProbeInterval     = 60
	MaxProbeInterval     = 3600
	DefaultProbeInterval = 300
)

// 每个样本的超时（最大等待）与样本间隔（修订第 133 条）：面板可设 1～10 秒，0 表示默认；
// Agent 端硬性限制，范围外的值按默认处理。
const (
	DefaultProbeTimeout = 4
	DefaultProbeGap     = 1
	MaxProbeTiming      = 10
)

// ProbeTiming 是解析后的超时与样本间隔（秒）。
type ProbeTiming struct{ Timeout, Gap int }

// DefaultTiming 是不设置时的超时 4 秒、样本间隔 1 秒。
var DefaultTiming = ProbeTiming{DefaultProbeTimeout, DefaultProbeGap}

// Encode 编码为响应头的值“timeout,gap”。
func (t ProbeTiming) Encode() string { return strconv.Itoa(t.Timeout) + "," + strconv.Itoa(t.Gap) }

// ParseProbeTiming 解析下发的超时与样本间隔；缺少、不合法或超出 1～10 秒的项按默认值。
func ParseProbeTiming(v string) ProbeTiming {
	out := DefaultTiming
	f := strings.Split(v, ",")
	if len(f) != 2 {
		return out
	}
	if n, err := strconv.Atoi(strings.TrimSpace(f[0])); err == nil && n >= 1 && n <= MaxProbeTiming {
		out.Timeout = n
	}
	if n, err := strconv.Atoi(strings.TrimSpace(f[1])); err == nil && n >= 1 && n <= MaxProbeTiming {
		out.Gap = n
	}
	return out
}

// ProbeIntervals 是面板可选的每轮间隔（秒）。
var ProbeIntervals = []int{60, 120, 300, 600, 900, 1800}

// ParseProbeInterval 解析下发的间隔；不合法或超出 [60, 3600] 秒时返回默认 300 秒。
func ParseProbeInterval(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < MinProbeInterval || n > MaxProbeInterval {
		return DefaultProbeInterval
	}
	return n
}

// ProbeTarget 是一个探测目标：Addr 与 Host 二者只有一个。
type ProbeTarget struct {
	ID    int64
	Proto string     // tcp / icmp
	Addr  netip.Addr // 公网 IP
	Host  string     // 域名（小写，不带末尾的点）；由 Agent 每轮解析
	Port  int        // tcp 时 1～65535；icmp 时为 0
}

// 不能作为探测目标的域名后缀：本机、局域网与保留用途（RFC 6761、6762、8375 等）。
// 【安全】只是第一道过滤；真正的保证是 Agent 只连接解析结果中的公网地址。
var privateSuffixes = []string{"localhost", "local", "lan", "home", "internal", "intranet", "corp", "home.arpa",
	"arpa", "test", "invalid", "example", "onion"}

// ValidProbeHost 判断域名是否可作为目标：2～253 个字符、至少两级，每级 1～63 个字母、数字或连字符（不以连字符开头或结尾），
// 顶级不能全是数字，且不是本机或局域网用的名称。
func ValidProbeHost(h string) bool {
	if len(h) < 3 || len(h) > 253 || h != strings.ToLower(h) {
		return false
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, c := range l {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return false // 像 IP 地址的名称
	}
	for _, suf := range privateSuffixes {
		if h == suf || strings.HasSuffix(h, "."+suf) {
			return false
		}
	}
	return true
}

// NormalizeProbeHost 把输入的域名转成保存的形式：去掉空白、http(s):// 与路径（方便直接粘贴网址）、末尾的点，转小写。
// 带端口的输入保持原样，由 ValidProbeHost 拒绝（端口在单独的字段中填写）。
func NormalizeProbeHost(h string) string {
	h = strings.TrimSpace(h)
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	return strings.ToLower(strings.TrimSuffix(h, "."))
}

// PickProbeAddr 从域名的解析结果中选一个可探测的地址：优先 IPv4（更多 VPS 只有 IPv4 出口），只用公网地址。
// 【安全】没有公网地址时返回 false，Agent 不探测。
func PickProbeAddr(addrs []netip.Addr) (netip.Addr, bool) {
	var v6 netip.Addr
	for _, a := range addrs {
		a = a.Unmap()
		if !PublicProbeAddr(a) {
			continue
		}
		if a.Is4() {
			return a, true
		}
		if !v6.IsValid() {
			v6 = a
		}
	}
	return v6, v6.IsValid()
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
	if t.ID <= 0 {
		return false
	}
	if t.Host != "" {
		if t.Addr.IsValid() || !ValidProbeHost(t.Host) {
			return false
		}
	} else if !PublicProbeAddr(t.Addr) {
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

// HostPort 返回 tcp 目标在地址 a（IP 目标即 Addr，域名目标为本轮解析的地址）上的连接地址。
func (t ProbeTarget) HostPort(a netip.Addr) string {
	return netip.AddrPortFrom(a.Unmap(), uint16(t.Port)).String()
}

// Target 返回目标的显示与编码形式：IP 或域名。
func (t ProbeTarget) Target() string {
	if t.Host != "" {
		return t.Host
	}
	return t.Addr.Unmap().String()
}

// EncodeProbeTargets 编码目标列表：“id,proto,addr,port” 以分号分隔。
func EncodeProbeTargets(ts []ProbeTarget) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = strconv.FormatInt(t.ID, 10) + "," + t.Proto + "," + t.Target() + "," + strconv.Itoa(t.Port)
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
		port, err3 := strconv.Atoi(f[3])
		t := ProbeTarget{ID: id, Proto: f[1], Port: port}
		if a, err := netip.ParseAddr(f[2]); err == nil {
			t.Addr = a
		} else {
			t.Host = f[2]
		}
		if err1 != nil || err3 != nil || !t.Valid() || seen[id] {
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

// ProbeVersion 是目标列表与间隔的版本：哈希前 12 位；没有目标时为 "0"。
func ProbeVersion(encoded string, interval int) string {
	if encoded == "" {
		return "0"
	}
	sum := sha256.Sum256([]byte(encoded + "@" + strconv.Itoa(interval)))
	return hex.EncodeToString(sum[:6])
}
