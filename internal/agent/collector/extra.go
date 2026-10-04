package collector

// 扩展指标的纯解析函数（设计 4.10）。都来自无需特权的只读来源；读不到或内核不提供的项返回 nil / 0，由调用方省略。

import (
	"math"
	"strconv"
	"strings"

	"vpsmon/internal/protocol"
)

// statCounters 是 /proc/stat 中的累计计数：上下文切换、中断总数、开机以来创建的进程数。
type statCounters struct {
	ctxt, intr, forks uint64
	blocked           int
	ok                bool
}

// parseStatCounters 解析 /proc/stat 的 ctxt、intr（第一个数为总数）、processes、procs_blocked。
func parseStatCounters(s string) statCounters {
	var c statCounters
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "ctxt":
			c.ctxt, c.ok = v, true
		case "intr":
			c.intr = v
		case "processes":
			c.forks = v
		case "procs_blocked":
			c.blocked = int(v)
		}
	}
	return c
}

// rate 返回两次累计计数之间的每秒速率；计数回退或间隔无效时返回 0，保留两位小数。
func rate(prev, cur uint64, elapsed float64) float64 {
	if elapsed <= 0 || cur < prev {
		return 0
	}
	return math.Round(float64(cur-prev)/elapsed*100) / 100
}

// activityFrom 由两次 /proc/stat 计数计算系统活动；首次采样（没有上一次）返回 nil。
func activityFrom(prev, cur statCounters, elapsed float64) *protocol.Activity {
	if !prev.ok || !cur.ok || elapsed <= 0 {
		return nil
	}
	return &protocol.Activity{CtxSwitches: rate(prev.ctxt, cur.ctxt, elapsed), Interrupts: rate(prev.intr, cur.intr, elapsed),
		Forks: rate(prev.forks, cur.forks, elapsed), ProcsBlocked: cur.blocked}
}

// parseKeyValues 解析 “名称 数值” 每行一项的文件（/proc/vmstat）。
func parseKeyValues(s string) map[string]uint64 {
	m := map[string]uint64{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64); err == nil {
			m[k] = n
		}
	}
	return m
}

// vmFrom 由两次 /proc/vmstat 计算主缺页与换入换出速率（页数按 pageSize 换算为字节）；首次采样返回 nil。
// oom_kill 从内核 4.13 开始提供，没有时省略。
func vmFrom(prev, cur map[string]uint64, elapsed float64, pageSize uint64) *protocol.VMStat {
	if len(prev) == 0 || len(cur) == 0 || elapsed <= 0 {
		return nil
	}
	v := &protocol.VMStat{MajorFaults: rate(prev["pgmajfault"], cur["pgmajfault"], elapsed),
		SwapIn:  rate(prev["pswpin"], cur["pswpin"], elapsed) * float64(pageSize),
		SwapOut: rate(prev["pswpout"], cur["pswpout"], elapsed) * float64(pageSize)}
	if n, ok := cur["oom_kill"]; ok {
		v.OOMKills = &n
	}
	return v
}

// parsePSI 解析一个 /proc/pressure 文件：
//
//	some avg10=0.12 avg60=0.05 avg300=0.01 total=123
//	full avg10=0.00 avg60=0.00 avg300=0.00 total=0
func parsePSI(s string) (some10, some60, full10 float64, ok bool) {
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		get := func(key string) float64 {
			for _, kv := range f[1:] {
				if k, v, found := strings.Cut(kv, "="); found && k == key {
					n, _ := strconv.ParseFloat(v, 64)
					return n
				}
			}
			return 0
		}
		switch f[0] {
		case "some":
			some10, some60, ok = get("avg10"), get("avg60"), true
		case "full":
			full10 = get("avg10")
		}
	}
	return
}

// memoryMoreFrom 取 /proc/meminfo 中的其他字段；没有 MemTotal（文件读取失败）时返回 nil。
func memoryMoreFrom(m map[string]uint64) *protocol.MemoryMore {
	if m["MemTotal"] == 0 {
		return nil
	}
	return &protocol.MemoryMore{Shmem: m["Shmem"], SlabUnreclaim: m["SUnreclaim"], Dirty: m["Dirty"],
		Writeback: m["Writeback"], Committed: m["Committed_AS"], CommitLimit: m["CommitLimit"]}
}

// parseSnmp 解析 /proc/net/snmp：每个协议两行，第一行为字段名，第二行为数值，如
//
//	Tcp: RtoAlgorithm RtoMin ... RetransSegs InErrs OutRsts
//	Tcp: 1 200 ... 345 0 12
func parseSnmp(s string) map[string]map[string]uint64 {
	out := map[string]map[string]uint64{}
	var names []string
	var proto string
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.HasSuffix(f[0], ":") {
			continue
		}
		p := strings.TrimSuffix(f[0], ":")
		if p != proto || names == nil {
			proto, names = p, f[1:]
			continue
		}
		vals := map[string]uint64{}
		for i, v := range f[1:] {
			if i >= len(names) {
				break
			}
			// CurrEstab 等为 gauge；MaxConn 可能为 -1，按 0 处理
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
				vals[names[i]] = uint64(n)
			}
		}
		out[p] = vals
		names = nil
	}
	return out
}

// netStackFrom 由两次 /proc/net/snmp 计算 TCP 重传率与建连速率；首次采样返回 nil。
// 重传率 = 新增重传报文 ÷ 新增发送报文 × 100；本轮没有发送时为 0。
func netStackFrom(prev, cur map[string]map[string]uint64, elapsed float64) *protocol.NetStack {
	pt, ct, cu := prev["Tcp"], cur["Tcp"], cur["Udp"]
	if len(pt) == 0 || len(ct) == 0 || elapsed <= 0 {
		return nil
	}
	n := &protocol.NetStack{TCPEstablished: ct["CurrEstab"], TCPActiveOpens: rate(pt["ActiveOpens"], ct["ActiveOpens"], elapsed),
		TCPPassiveOpens: rate(pt["PassiveOpens"], ct["PassiveOpens"], elapsed), TCPInErrs: ct["InErrs"],
		TCPAttemptFails: ct["AttemptFails"], UDPRcvbufErrors: cu["RcvbufErrors"], UDPSndbufErrors: cu["SndbufErrors"],
		UDPInErrors: cu["InErrors"]}
	if out := ct["OutSegs"]; out > pt["OutSegs"] && ct["RetransSegs"] >= pt["RetransSegs"] {
		n.TCPRetransRate = math.Round(float64(ct["RetransSegs"]-pt["RetransSegs"])/float64(out-pt["OutSegs"])*10000) / 100
	}
	return n
}

// parseFileNr 解析 /proc/sys/fs/file-nr：“已分配 空闲（恒为 0） 上限”。
func parseFileNr(s string) *protocol.FileHandles {
	f := strings.Fields(s)
	if len(f) < 3 {
		return nil
	}
	a, err1 := strconv.ParseUint(f[0], 10, 64)
	m, err2 := strconv.ParseUint(f[2], 10, 64)
	if err1 != nil || err2 != nil {
		return nil
	}
	return &protocol.FileHandles{Allocated: a, Max: m}
}

// parseConntrack 解析 nf_conntrack_count 与 nf_conntrack_max；未加载 conntrack 时文件不存在，返回 nil。
func parseConntrack(count, max string) *protocol.Conntrack {
	c, err1 := strconv.ParseUint(strings.TrimSpace(count), 10, 64)
	m, err2 := strconv.ParseUint(strings.TrimSpace(max), 10, 64)
	if err1 != nil || err2 != nil || m == 0 {
		return nil
	}
	return &protocol.Conntrack{Count: c, Max: m}
}

// envHints 是判断运行环境所需的线索，由 linux.go 读取。
type envHints struct {
	openVZ        bool   // /proc/vz 存在且 /proc/bc 不存在（容器内）
	containerFile string // /run/systemd/container 的内容（systemd 写入，如 lxc、docker）
	dockerEnv     bool   // /.dockerenv
	podmanEnv     bool   // /run/.containerenv
	xen           bool   // /sys/hypervisor/type 为 xen
	vendor        string // /sys/class/dmi/id/sys_vendor
	product       string // /sys/class/dmi/id/product_name
	hypervisorCPU bool   // /proc/cpuinfo 的 flags 含 hypervisor
}

// detectVirt 判断虚拟化 / 容器类型；容器优先于虚拟机（容器运行在某种虚拟机里时，用户关心的是容器）。
func detectVirt(h envHints) string {
	switch {
	case h.openVZ:
		return "openvz"
	case h.containerFile != "":
		return h.containerFile
	case h.dockerEnv:
		return "docker"
	case h.podmanEnv:
		return "podman"
	case h.xen:
		return "xen"
	}
	v, p := strings.ToLower(h.vendor), strings.ToLower(h.product)
	switch {
	case strings.Contains(v, "vmware"):
		return "vmware"
	case strings.Contains(v, "microsoft") && strings.Contains(p, "virtual"):
		return "hyperv"
	case strings.Contains(v, "xen") || strings.Contains(p, "hvm domu"):
		return "xen"
	case strings.Contains(v, "qemu") || strings.Contains(p, "kvm") || strings.Contains(v, "alibaba") ||
		strings.Contains(v, "tencent") || strings.Contains(p, "openstack") || strings.Contains(v, "google") ||
		strings.Contains(v, "amazon ec2") || strings.Contains(v, "digitalocean") || strings.Contains(v, "hetzner") ||
		strings.Contains(v, "vultr") || strings.Contains(v, "linode") || strings.Contains(p, "standard pc"):
		return "kvm"
	case h.hypervisorCPU:
		return "vm" // 有 hypervisor 标志但无法识别具体类型
	}
	return "none"
}

// cpuHasHypervisorFlag 判断 /proc/cpuinfo 的 flags 是否含 hypervisor（x86 虚拟机）。
func cpuHasHypervisorFlag(cpuinfo string) bool {
	for _, line := range strings.Split(cpuinfo, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "flags" {
			for _, f := range strings.Fields(v) {
				if f == "hypervisor" {
					return true
				}
			}
			return false
		}
	}
	return false
}
