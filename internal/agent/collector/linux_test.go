//go:build linux

package collector

// 真实 Linux 采集器的集成测试与基准（CI 的 ubuntu runner 上运行）：与 /proc、statfs 独立读取的数值对照，
// 确认在真实内核上没有失败项、数值合理（设计 4、43.5）。macOS 开发机上不编译。

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLinuxCollect(t *testing.T) {
	c := New(Options{})
	if _, err := c.Collect(); err != nil { // 首轮只建立 CPU / 网速基线
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	r, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.CollectErrors) > 0 {
		t.Errorf("真实内核上不应有失败项：%+v", r.CollectErrors)
	}

	if r.System.BootID == "" || r.System.Kernel == "" || r.System.CounterBits == 0 {
		t.Errorf("系统信息不完整：%+v", r.System)
	}
	if r.CPU.Cores < 1 || r.CPU.Usage < 0 || r.CPU.Usage > 100 || r.CPU.Breakdown == nil || len(r.CPU.PerCore) != r.CPU.Cores {
		t.Errorf("CPU 不合理：%+v", r.CPU)
	}

	// 内存：总量与 /proc/meminfo 一致，已用不超过总量
	m := parseMeminfo(readFile("/proc/meminfo"))
	if r.Memory.Total != m["MemTotal"] || r.Memory.Used > r.Memory.Total || r.Memory.Usage <= 0 || r.Memory.Usage > 100 {
		t.Errorf("内存不合理：%+v（MemTotal %d）", r.Memory, m["MemTotal"])
	}

	// 磁盘：“/” 在最前，总量与 statfs 一致
	if len(r.Disk) == 0 || r.Disk[0].Mount != "/" {
		t.Fatalf("应采集到根分区：%+v", r.Disk)
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err != nil {
		t.Fatal(err)
	}
	if want := uint64(st.Blocks) * uint64(st.Bsize); r.Disk[0].Total != want || r.Disk[0].Used > r.Disk[0].Total {
		t.Errorf("根分区容量 %d，statfs 为 %d：%+v", r.Disk[0].Total, want, r.Disk[0])
	}

	// 网络：选中默认路由网卡，计数与 /proc/net/dev 同量级（两次读取之间可能增长）
	if len(r.Network) == 0 {
		t.Fatal("应选中至少一块网卡")
	}
	dev := parseNetDev(readFile("/proc/net/dev"))
	for _, ni := range r.Network {
		if excluded(ni.Interface, DefaultExclude) || ni.IfIndex <= 0 {
			t.Errorf("不应选中虚拟网卡，ifindex 应有效：%+v", ni)
		}
		if now := dev[ni.Interface]; ni.RxBytes > now.rx || ni.TxBytes > now.tx {
			t.Errorf("%s 计数大于随后读取的 /proc/net/dev：%+v %+v", ni.Interface, ni, now)
		}
	}

	if r.Processes == nil || r.Processes.Total < 1 || r.Conns == nil {
		t.Errorf("进程与连接：%+v %+v", r.Processes, r.Conns)
	}
	for _, d := range r.DiskIO {
		if strings.HasPrefix(d.Device, "loop") || d.Util > 100 {
			t.Errorf("磁盘 IO 不应包含 loop 设备，繁忙不超过 100%%：%+v", d)
		}
	}
	t.Logf("cores=%d mem=%d MiB disks=%d ifaces=%v diskio=%d ports=%d bits=%d",
		r.CPU.Cores, r.Memory.Total>>20, len(r.Disk), r.Network, len(r.DiskIO), len(r.Ports), r.System.CounterBits)
}

// BenchmarkLinuxCollect 衡量每轮采集的耗时与内存分配（设计 4.2：平均 CPU < 0.5%）。
// 运行：go test -run '^$' -bench LinuxCollect -benchmem ./internal/agent/collector
func BenchmarkLinuxCollect(b *testing.B) {
	c := New(Options{})
	c.Collect()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Collect()
	}
}
