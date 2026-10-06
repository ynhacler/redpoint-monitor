package collector

// 采集容错（设计 43.5）：单个采集项失败或 panic 时，该项本轮留空并上报原因，其余指标照常上报；
// 磁盘 statfs 加超时，卡住的挂载点不会拖住整份上报。没有 build tag，在任何系统上都能单元测试。

import (
	"fmt"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"vpsmon/internal/agent/alog"
	"vpsmon/internal/protocol"
)

const (
	maxCollectErrors = 16  // 每份上报最多带的失败项，防止异常主机撑大上报体
	maxErrorMessage  = 200 // 每条原因的最大长度（字节）
)

// errorList 收集本轮失败的采集项及原因。
type errorList []protocol.CollectError

func (e *errorList) add(item, format string, args ...any) {
	if len(*e) >= maxCollectErrors {
		return
	}
	msg := fmt.Sprintf(format, args...)
	if len(msg) > maxErrorMessage {
		msg = msg[:maxErrorMessage]
	}
	*e = append(*e, protocol.CollectError{Item: item, Message: msg})
}

// panicLogged 记录已打印过调用栈的采集项：同一个 bug 每轮都会触发，只在第一次打印完整调用栈，避免刷屏。
var panicLogged sync.Map

// guard 运行一个采集项。panic 时捕获并记录原因，该项本轮留空，其余采集项照常进行（设计 43.5）。
// 采集项应先算出结果再一次性写入上报与内部状态，panic 时不会留下写了一半的字段。
func guard(errs *errorList, item string, f func()) {
	defer func() {
		v := recover()
		if v == nil {
			return
		}
		errs.add(item, "panic: %v", v)
		if _, seen := panicLogged.LoadOrStore(item, true); !seen {
			alog.Printf("collect %s panicked: %v\n%s", item, v, debug.Stack())
		} else {
			alog.Printf("collect %s panicked: %v", item, v)
		}
	}()
	f()
}

// diskStat 是 statfs 结果中用到的字段，与平台无关，便于测试。
type diskStat struct {
	Blocks, Bfree, Bavail, Bsize uint64
	Files, Ffree                 uint64 // inode 总数与空闲数（设计 4.10）
}

// statfsTimeout：一轮磁盘采集的总等待时间。本地文件系统的 statfs 通常在微秒级返回；
// 底层块设备失联（iSCSI、故障盘）时可能永久阻塞，不能让它拖住整份上报。
const statfsTimeout = 2 * time.Second

// statfsProber 并发调用 statfs，整体等待不超过 timeout。
//
// 超时的挂载点记为“仍在等待”，直到那次调用返回之前，后续轮次直接跳过它、不再新开 goroutine，
// 因此即使挂载点一直卡住，每个挂载点最多只有一个阻塞的 goroutine，不会越积越多。
type statfsProber struct {
	statfs  func(path string) (diskStat, error)
	timeout time.Duration

	mu      sync.Mutex
	pending map[string]bool // 调用尚未返回的挂载点
}

func newStatfsProber(statfs func(string) (diskStat, error)) *statfsProber {
	return &statfsProber{statfs: statfs, timeout: statfsTimeout, pending: map[string]bool{}}
}

type statResult struct {
	mount string
	st    diskStat
	err   error
}

// statAll 返回各挂载点的 statfs 结果，键为挂载点。失败、超时或上次仍未返回的挂载点放在 errs 中。
func (p *statfsProber) statAll(mounts []string) (map[string]diskStat, map[string]string) {
	ok := map[string]diskStat{}
	failed := map[string]string{}
	ch := make(chan statResult, len(mounts)) // 带缓冲：超时后才返回的 goroutine 也能写入并退出
	waiting := map[string]bool{}
	for _, m := range mounts {
		p.mu.Lock()
		stuck := p.pending[m]
		if !stuck {
			p.pending[m] = true
		}
		p.mu.Unlock()
		if stuck {
			failed[m] = "statfs 仍未返回，跳过"
			continue
		}
		waiting[m] = true
		go func(m string) {
			st, err := p.statfs(m)
			p.mu.Lock()
			delete(p.pending, m)
			p.mu.Unlock()
			ch <- statResult{m, st, err}
		}(m)
	}
	deadline := time.NewTimer(p.timeout)
	defer deadline.Stop()
	for len(waiting) > 0 {
		select {
		case r := <-ch:
			delete(waiting, r.mount)
			if r.err != nil {
				failed[r.mount] = "statfs 失败：" + r.err.Error()
			} else {
				ok[r.mount] = r.st
			}
		case <-deadline.C:
			for m := range waiting {
				failed[m] = "statfs 超时"
			}
			return ok, failed
		}
	}
	return ok, failed
}

// diskFromStat 由 statfs 结果计算一个挂载点的容量（设计 4.6），total 为 0 时返回 false。
//
// 已用 = Blocks − Bfree（root 保留块计为已用）；使用率 = 已用 ÷（已用 + Bavail），与 df 的 Use% 一致：
// 普通用户可用空间耗尽时显示 100%，即使 root 保留块还有剩余。
// 个别文件系统（如部分 FUSE、btrfs 统计未刷新时）会出现 Bfree > Blocks 或 Bavail > Bfree，这里截断，避免无符号下溢。
func diskFromStat(m mountEntry, st diskStat) (protocol.Disk, bool) {
	if st.Blocks == 0 || st.Bsize == 0 {
		return protocol.Disk{}, false
	}
	free, avail := min(st.Bfree, st.Blocks), min(st.Bavail, st.Bfree, st.Blocks)
	total := st.Blocks * st.Bsize
	used := total - free*st.Bsize
	a := avail * st.Bsize
	d := protocol.Disk{Mount: m.Mount, Total: total, Used: used, Available: a,
		Usage: pct(used, used+a), FSType: m.FSType, Device: m.Device}
	if st.Files > 0 { // btrfs 等动态分配 inode 的文件系统报告 0，不上报
		d.InodesTotal, d.InodesUsed = st.Files, st.Files-min(st.Ffree, st.Files)
	}
	return d, true
}

// pickIfaces 选出参与流量统计的网卡（设计 5.6）：
//
//	显式指定        → 只取存在的；全部不存在时返回空并在 missing 中列出（不擅自改统计其他网卡，避免改变计费口径）
//	有默认路由      → 默认路由网卡（IPv4 + IPv6 去重，排除虚拟网卡）
//	默认路由暂时缺失 → 沿用上次选中的网卡（只要它们仍然存在），sticky=true；
//	                  网络服务重启时路由会短暂消失，若此时改为统计所有网卡，内网网卡的流量会被计入
//	从未有过选择    → 所有未被排除的网卡，按名称排序
func pickIfaces(explicit, routed []string, counters map[string]netCounters, exclude, last []string) (out, missing []string, sticky bool) {
	if len(explicit) > 0 {
		for _, n := range explicit {
			if _, ok := counters[n]; ok {
				out = append(out, n)
			} else {
				missing = append(missing, n)
			}
		}
		return out, missing, false
	}
	seen := map[string]bool{}
	for _, n := range routed {
		if _, ok := counters[n]; ok && !seen[n] && !excluded(n, exclude) {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) > 0 {
		return out, nil, false
	}
	if len(last) > 0 {
		all := true
		for _, n := range last {
			if _, ok := counters[n]; !ok {
				all = false
				break
			}
		}
		if all {
			return append([]string(nil), last...), nil, true
		}
	}
	for n := range counters {
		if !excluded(n, exclude) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil, false
}
