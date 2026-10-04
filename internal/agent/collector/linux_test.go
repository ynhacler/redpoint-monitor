//go:build linux

package collector

// 真实 Linux 采集器的集成测试与基准（CI 的 ubuntu runner 上运行）：与 /proc、statfs 独立读取的数值对照，
// 确认在真实内核上没有失败项、数值合理（设计 4、43.5）。macOS 开发机上不编译。

import (
	"os"
	"path/filepath"
	"strconv"
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

	// 内存：总量与 /proc/meminfo 一致；容器的内存上限更小时为该上限（设计 4.5），已用不超过总量
	m := parseMeminfo(readFile("/proc/meminfo"))
	wantTotal := m["MemTotal"]
	if lim := readCgroupLimit().limit; lim > 0 && lim < wantTotal {
		wantTotal = lim
	}
	if r.Memory.Total != wantTotal || r.Memory.Used > r.Memory.Total || r.Memory.Usage <= 0 || r.Memory.Usage > 100 {
		t.Errorf("内存不合理：%+v（应为 %d）", r.Memory, wantTotal)
	}
	// CI 在 docker run --memory=… 的容器中运行时指定期望值，确认确实按容器上限而不是宿主机内存计算
	if want := os.Getenv("VPSMON_EXPECT_MEM_LIMIT"); want != "" && strconv.FormatUint(r.Memory.Total, 10) != want {
		t.Errorf("容器内存上限为 %s，采集到的总量为 %d（应按 cgroup 计算）", want, r.Memory.Total)
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

// readFile 复用缓冲区：超过初始容量的文件也要完整读出，连续读取互不影响（设计 4.2）。
func TestReadFileReusesBuffer(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("0123456789abcdef", 3000) // 48 KB，超过 16 KB 初始缓冲
	os.WriteFile(filepath.Join(dir, "big"), []byte(big), 0o600)
	os.WriteFile(filepath.Join(dir, "small"), []byte("x 1\n"), 0o600)
	if got := readFile(filepath.Join(dir, "big")); got != big {
		t.Fatalf("大文件读取不完整：%d 字节", len(got))
	}
	if got := readFile(filepath.Join(dir, "small")); got != "x 1\n" {
		t.Fatalf("复用缓冲后的读取：%q", got)
	}
	if readFile(filepath.Join(dir, "missing")) != "" {
		t.Error("不存在的文件返回空字符串")
	}
	if want, _ := os.ReadFile("/proc/meminfo"); readFile("/proc/meminfo")[:20] != string(want[:20]) {
		t.Error("/proc 文件应与 os.ReadFile 一致")
	}
}
