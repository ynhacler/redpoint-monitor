package collector

import (
	"strings"
	"testing"

	"vpsmon/internal/protocol"
)

func TestParseNetDev(t *testing.T) {
	s := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:  1000      10    0    0    0     0          0         0     1000      10    0    0    0     0       0          0
  eth0: 833721124344 1234 0 0 0 0 0 0 139922812321 4321 0 0 0 0 0 0
`
	m := parseNetDev(s)
	if m["eth0"].rx != 833721124344 || m["eth0"].tx != 139922812321 {
		t.Fatalf("eth0 = %+v", m["eth0"])
	}
	if _, ok := m["lo"]; !ok {
		t.Fatal("lo missing")
	}
}

func TestCPUUsage(t *testing.T) {
	a := parseProcStat("cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 1 1 1 1\ncpu1 1 1 1 1\nprocs_running 3\n")
	b := parseProcStat("cpu  150 0 150 900 0 0 0 0 0 0\n")
	if len(a.cores) != 2 || a.running != 3 {
		t.Fatalf("cores = %d running = %d", len(a.cores), a.running)
	}
	if got := cpuUsage(a.all, b.all); got != 50 {
		t.Fatalf("usage = %v, want 50", got)
	}
	if cpuUsage(cpuTimes{}, b.all) != 0 || cpuBreakdown(cpuTimes{}, b.all) != nil {
		t.Error("首次采样应为 0 / nil")
	}
}

func TestCPUBreakdown(t *testing.T) {
	// 间隔内：user 20 nice 0 system 10 idle 50 iowait 5 irq 1 softirq 4 steal 10，共 100；guest 列不参与
	a := parseProcStat("cpu  100 0 100 800 10 10 10 10 99 99\ncpu0 50 0 50 400 0 0 0 0\ncpu1 50 0 50 400 0 0 0 0\n")
	b := parseProcStat("cpu  120 0 110 850 15 11 14 20 500 500\ncpu0 70 0 60 410 0 0 0 0\ncpu1 50 0 50 500 0 0 0 0\n")
	got := cpuBreakdown(a.all, b.all)
	want := protocol.CPUBreakdown{User: 20, System: 10, Idle: 50, IOWait: 5, IRQ: 1, SoftIRQ: 4, Steal: 10}
	if got == nil || *got != want {
		t.Fatalf("breakdown = %+v, want %+v", got, want)
	}
	if u := cpuUsage(a.all, b.all); u != 45 {
		t.Errorf("usage = %v, want 45（iowait 计为空闲，steal 计为忙碌）", u)
	}
	if pc := perCoreUsage(a.cores, b.cores); len(pc) != 2 || pc[0] != 75 || pc[1] != 0 {
		t.Errorf("per core = %v, want [75 0]", pc)
	}
	if perCoreUsage(a.cores, b.cores[:1]) != nil {
		t.Error("核心数变化时不应返回每核使用率")
	}
	if cpuBreakdown(b.all, a.all) != nil {
		t.Error("计数倒退时应返回 nil")
	}
}

func TestParseCPUModel(t *testing.T) {
	x86 := "processor\t: 0\nmodel name\t: Intel(R) Xeon(R)   CPU E5-2680 v4 @ 2.40GHz\n"
	arm := "Processor\t: AArch64 Processor rev 4 (aarch64)\nHardware\t: BCM2835\n"
	if got := parseCPUModel(x86); got != "Intel(R) Xeon(R) CPU E5-2680 v4 @ 2.40GHz" {
		t.Errorf("x86 = %q", got)
	}
	if got := parseCPUModel(arm); got != "AArch64 Processor rev 4 (aarch64)" {
		t.Errorf("arm = %q", got)
	}
}

func TestParseSockstat(t *testing.T) {
	v4 := "sockets: used 300\nTCP: inuse 25 orphan 0 tw 40 alloc 30 mem 3\nUDP: inuse 4 mem 2\nUDPLITE: inuse 0\n"
	v6 := "TCP6: inuse 6\nUDP6: inuse 2\n"
	t4, u4, tw := parseSockstat(v4)
	t6, u6, _ := parseSockstat(v6)
	if t4+t6 != 31 || u4+u6 != 6 || tw != 40 {
		t.Errorf("tcp=%d udp=%d tw=%d", t4+t6, u4+u6, tw)
	}
}

func TestPickCPUTemp(t *testing.T) {
	cases := []struct {
		in   []tempReading
		want float64
	}{
		{[]tempReading{{"coretemp", 45000}, {"coretemp", 52500}, {"acpitz", 90000}}, 52.5},
		{[]tempReading{{"acpitz", 27800}}, 0}, // 虚拟化环境的假传感器不采用
		{[]tempReading{{"cpu_thermal", 43312}}, 43.3},
		{[]tempReading{{"k10temp", -1000}, {"k10temp", 200000}}, 0},
		{nil, 0},
	}
	for _, c := range cases {
		if got := pickCPUTemp(c.in); got != c.want {
			t.Errorf("%v → %v, want %v", c.in, got, c.want)
		}
	}
}

func TestIORates(t *testing.T) {
	a := ioCounters{readOps: 100, writeOps: 200, readMs: 1000, writeMs: 2000, ioTimeMs: 5000}
	b := ioCounters{readOps: 150, writeOps: 250, readMs: 1100, writeMs: 2300, ioTimeMs: 7500}
	r, w, await, util, ok := ioRates(a, b, 10)
	if !ok || r != 5 || w != 5 || await != 4 || util != 25 {
		t.Errorf("r=%v w=%v await=%v util=%v ok=%v", r, w, await, util, ok)
	}
	if _, _, _, _, ok := ioRates(b, a, 10); ok {
		t.Error("计数倒退时应返回 false")
	}
}

func TestDefaultRoute(t *testing.T) {
	s := "Iface\tDestination\tGateway\nens3\t00000000\t0101A8C0\nens3\t0001A8C0\t00000000\n"
	if got := parseDefaultRouteIfaces(s); len(got) != 1 || got[0] != "ens3" {
		t.Fatalf("got %v", got)
	}
}

func TestExcluded(t *testing.T) {
	for _, n := range []string{"lo", "docker0", "veth12ab", "wg0", "br-abc"} {
		if !excluded(n, DefaultExclude) {
			t.Errorf("%s should be excluded", n)
		}
	}
	if excluded("eth0", DefaultExclude) {
		t.Error("eth0 should not be excluded")
	}
}

func TestSelectMounts(t *testing.T) {
	s := `/dev/vda1 / ext4 rw,relatime 0 0
proc /proc proc rw 0 0
tmpfs /run tmpfs rw 0 0
/dev/vda15 /boot/efi vfat rw 0 0
/dev/vdb1 /data xfs rw 0 0
/dev/vdb1 /var/lib/docker/volumes/x xfs rw 0 0
overlay /var/lib/docker/overlay2/abc/merged overlay rw 0 0
/dev/loop0 /snap/core/1 squashfs ro 0 0
nas:/export /mnt/nas nfs4 rw 0 0
/dev/vdc1 /mnt/my\040disk ext4 rw 0 0
`
	got := selectMounts(parseMounts(s))
	var mounts []string
	for _, m := range got {
		mounts = append(mounts, m.Mount)
	}
	want := "/,/boot/efi,/data,/mnt/my disk"
	if strings.Join(mounts, ",") != want {
		t.Errorf("挂载点 = %q，应为 %q（排除虚拟 / 网络文件系统，同一设备只取一次，/ 在最前，还原 \\040）",
			strings.Join(mounts, ","), want)
	}
}

func TestParseDiskstats(t *testing.T) {
	s := ` 253       0 vda 1000 10 20000 500 2000 20 40000 900 0 1200 1400 0 0 0 0
 253       1 vda1 900 10 18000 450 1900 20 39000 880 0 1100 1330 0 0 0 0
   7       0 loop0 5 0 10 0 0 0 0 0 0 4 0 0 0 0 0
`
	m := parseDiskstats(s)
	v := m["vda"]
	if v.readOps != 1000 || v.readBytes != 20000*512 || v.writeOps != 2000 || v.writeBytes != 40000*512 || v.ioTimeMs != 1200 {
		t.Errorf("vda = %+v", v)
	}
	if !skipIODevice("loop0") || !skipIODevice("zram0") || skipIODevice("nvme0n1") || skipIODevice("vda") {
		t.Error("skipIODevice 判断错误")
	}
}
