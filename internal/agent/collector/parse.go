package collector

// /proc 文件的纯解析函数。没有 build tag，因此在任何系统上都能做单元测试（见 parse_test.go）。

import (
	"bufio"
	"strconv"
	"strings"
)

// cpuTimes 是开机以来累计 CPU jiffies 的快照（所有核心之和）。
type cpuTimes struct{ idle, total uint64 }

// parseProcStat 读取 /proc/stat 中汇总的 “cpu” 行，并统计 “cpuN” 行数作为核心数（设计 4.4）。
// 按行计数反映的是这台 VPS 实际在线的核心数；runtime.NumCPU 可能受 Agent 自身 cgroup / 亲和性限制。
func parseProcStat(s string) (cpuTimes, int) {
	var t cpuTimes
	cores := 0
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if f[0] == "cpu" {
			for i, v := range f[1:] {
				n, _ := strconv.ParseUint(v, 10, 64)
				// 字段顺序：user nice system idle iowait irq softirq steal guest guest_nice
				if i >= 8 { // guest 时间已经包含在 user / nice 中，不能重复累加
					break
				}
				t.total += n
				// iowait 计为空闲：CPU 本身空闲，只是在等磁盘。steal（i == 7）计为忙碌，
				// 宿主机超售时会表现为使用率偏高，这正是 VPS 用户需要看到的。
				if i == 3 || i == 4 { // idle + iowait
					t.idle += n
				}
			}
		} else if strings.HasPrefix(f[0], "cpu") {
			cores++
		}
	}
	return t, cores
}

// cpuUsage 返回两次采样之间的忙碌百分比（0～100）。
// 首次采样（没有 prev）或计数倒退时返回 0，而不是一个无意义的值。
func cpuUsage(prev, cur cpuTimes) float64 {
	dt := cur.total - prev.total
	if prev.total == 0 || dt == 0 || cur.total < prev.total {
		return 0
	}
	di := cur.idle - prev.idle
	return float64(dt-di) * 100 / float64(dt)
}

// parseMeminfo 返回 /proc/meminfo 各字段的值，单位为字节，键为字段名（MemTotal、MemAvailable …）。
func parseMeminfo(s string) map[string]uint64 {
	m := map[string]uint64{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, _ := strconv.ParseUint(f[0], 10, 64)
		// 内核写的是 “kB”，实际含义是 KiB（1024）。没有单位的行（如 HugePages_Total）是个数。
		if len(f) > 1 && f[1] == "kB" {
			n *= 1024
		}
		m[k] = n
	}
	return m
}

// netCounters 是一块网卡的累计收发字节数。
type netCounters struct{ rx, tx uint64 }

// parseNetDev 把 /proc/net/dev 解析为各网卡的累计字节数。
func parseNetDev(s string) map[string]netCounters {
	out := map[string]netCounters{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue // 表头行
		}
		// 先是 8 个接收字段，再是 8 个发送字段，每组第一个都是字节数，
		// 所以接收字节为 f[0]，发送字节为 f[8]。
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(f[0], 10, 64)
		tx, _ := strconv.ParseUint(f[8], 10, 64)
		out[strings.TrimSpace(name)] = netCounters{rx: rx, tx: tx}
	}
	return out
}

// parseDefaultRouteIfaces 返回持有 IPv4 默认路由的网卡（/proc/net/route）。
func parseDefaultRouteIfaces(s string) []string {
	var out []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		// 列：Iface Destination(十六进制) Gateway …；目的地址 0.0.0.0 即默认路由。
		// 同一网卡可能有多条默认路由（不同 metric），因此去重。
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[1] == "00000000" && !seen[f[0]] {
			seen[f[0]] = true
			out = append(out, f[0])
		}
	}
	return out
}

// parseDefaultRoute6Ifaces 返回持有 IPv6 默认路由的网卡（/proc/net/ipv6_route）。
func parseDefaultRoute6Ifaces(s string) []string {
	var out []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		// 列：目的地址（32 位十六进制）、前缀长度 … 网卡名（最后一列）
		if len(f) >= 10 && f[0] == strings.Repeat("0", 32) && f[1] == "00" {
			name := f[len(f)-1]
			// 内核会在 lo 上列出一条不可达的 ::/0 路由，它不承载真实流量。
			if name != "lo" && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// parseOSRelease 返回 /etc/os-release 中的 ID 与 VERSION_ID（如 “ubuntu”、“24.04”）。
// ID 稳定、便于程序处理，不像 PRETTY_NAME；展示格式由界面负责。
func parseOSRelease(s string) (id, version string) {
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "ID":
			id = v
		case "VERSION_ID":
			version = v
		}
	}
	return
}
