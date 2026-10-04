package collector

// 容器的内存口径（设计 4.5）：LXC / Docker 型 VPS 没有挂载 lxcfs 时，/proc/meminfo 显示的是宿主机内存，
// 例如分配 512 MB 的容器显示为 64 GB、使用率极低，内存告警形同虚设。此时改用容器根 cgroup 的上限与用量。
// 本文件只有纯函数，读取文件见 linux.go 的 readCgroupMemory。

import (
	"strconv"
	"strings"

	"vpsmon/internal/protocol"
)

// cgroupMemory 是容器根 cgroup 的内存数据，单位字节。Limit 为 0 表示不限（或读不到）。
type cgroupMemory struct {
	Limit    uint64
	Usage    uint64 // 含页缓存
	Inactive uint64 // 可回收的非活跃文件页：v2 inactive_file，v1 total_inactive_file
	Cache    uint64 // 文件页缓存：v2 file，v1 total_cache
}

// unlimitedV1 以上视为不限：cgroup v1 未设限时 limit_in_bytes 为接近 2^63 的页对齐值。
const unlimitedV1 = 1 << 62

// parseCgroupLimit 解析 memory.max（v2，“max” 表示不限）或 memory.limit_in_bytes（v1）。
func parseCgroupLimit(s string) uint64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "max" {
		return 0
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || v >= unlimitedV1 {
		return 0
	}
	return v
}

// parseCgroupStat 从 memory.stat 取可回收的非活跃文件页与文件缓存；v2 与 v1 的字段名不同，都识别。
func parseCgroupStat(s string) (inactive, cache uint64) {
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "inactive_file", "total_inactive_file":
			inactive = n
		case "file", "total_cache":
			cache = n
		}
	}
	return
}

// applyCgroupMemory 在容器的内存上限小于 /proc/meminfo 的总量时，改用 cgroup 的口径（设计 4.5）：
//
//	总量 = 上限；已用 = 用量 − 非活跃文件页（与 docker stats / kubectl 的 working set 一致：页缓存可回收，不算已用）
//
// 挂载了 lxcfs 时 /proc/meminfo 已经是容器视角（总量等于上限），不做修改；物理机与普通 VM 的根 cgroup 不设上限，也不修改。
// Free / Buffers 无法从 cgroup 得到，置 0；Cached 取 cgroup 的文件缓存。
func applyCgroupMemory(mem protocol.Memory, cg cgroupMemory) (protocol.Memory, bool) {
	if cg.Limit == 0 || cg.Limit >= mem.Total {
		return mem, false
	}
	used := cg.Usage - min(cg.Inactive, cg.Usage)
	used = min(used, cg.Limit)
	return protocol.Memory{Total: cg.Limit, Used: used, Available: cg.Limit - used, Usage: pct(used, cg.Limit),
		Cached: min(cg.Cache, cg.Limit)}, true
}
