package collector

import (
	"math"
	"math/rand"
	"runtime"
	"time"

	"vpsmon/internal/protocol"
)

// fake 为本地开发生成合理且缓慢变化的指标。
// 网卡计数单调递增，让面板的流量逻辑以接近真实的方式运行。
type fake struct {
	start  time.Time
	bootID string // 每次进程启动生成新值，重启假数据 Agent 等同于一次“重启”（设计 5.5）
	rx, tx uint64 // 累计字节数，与 /proc/net/dev 一样
	last   time.Time
}

// NewFake 返回假数据采集器（用于 --fake 与非 Linux 平台）。
func NewFake() Collector {
	return &fake{start: time.Now(), bootID: "fake-boot-" + time.Now().Format("150405"), last: time.Now()}
}

// Collect 生成一份假数据上报。
func (f *fake) Collect() (protocol.Report, error) {
	now := time.Now()
	dt := now.Sub(f.last).Seconds()
	if dt <= 0 {
		dt = 1
	}
	f.last = now
	t := now.Sub(f.start).Seconds()

	// 缓慢的正弦波加少量噪声：界面上每隔几秒能看到变化，又不会随机乱跳。
	// 网速始终为正，因此计数只增不减。
	rxSpeed := uint64(2_000_000 + 1_500_000*math.Sin(t/30) + rand.Float64()*300_000)
	txSpeed := uint64(400_000 + 300_000*math.Cos(t/45) + rand.Float64()*100_000)
	f.rx += uint64(float64(rxSpeed) * dt)
	f.tx += uint64(float64(txSpeed) * dt)

	memTotal := uint64(2 << 30)
	memUsed := uint64(float64(memTotal) * (0.45 + 0.05*math.Sin(t/60)))
	diskTotal := uint64(40 << 30)
	diskUsed := uint64(18 << 30)
	cpu := 15 + 10*math.Sin(t/20) + rand.Float64()*5
	// 各类占比按总使用率拆分，合计 100；4 个核心围绕总使用率波动
	steal, iowait := 1.5+rand.Float64(), 0.8+rand.Float64()*0.5
	sys := cpu * 0.3
	user := cpu - sys - steal - 0.4
	perCore := make([]float64, 4)
	for i := range perCore {
		perCore[i] = math.Round(math.Max(0, math.Min(100, cpu+12*math.Sin(t/15+float64(i))))*10) / 10
	}
	r1 := func(v float64) float64 { return math.Round(v*10) / 10 }
	ioRead := uint64(200_000 + 150_000*math.Sin(t/25))
	ioWrite := uint64(800_000 + 600_000*math.Cos(t/35))

	return protocol.Report{
		System: protocol.System{
			Hostname: "fake-node", OS: "fake", OSVersion: "1", Kernel: "fake",
			Arch: runtime.GOARCH, Uptime: uint64(t), BootID: f.bootID,
			CPUModel: "Fake Virtual CPU @ 2.40GHz",
		},
		CPU: protocol.CPU{
			Usage: cpu, Cores: len(perCore), Load1: 0.3, Load5: 0.25, Load15: 0.2,
			Breakdown: &protocol.CPUBreakdown{User: r1(user), System: r1(sys), Steal: r1(steal), IOWait: r1(iowait),
				SoftIRQ: 0.4, Idle: r1(100 - cpu - iowait)},
			PerCore: perCore, TempC: r1(42 + 3*math.Sin(t/40)),
		},
		Memory: protocol.Memory{Total: memTotal, Used: memUsed, Available: memTotal - memUsed, Usage: pct(memUsed, memTotal),
			Buffers: 48 << 20, Cached: 512 << 20, Free: memTotal - memUsed - 560<<20},
		Processes: &protocol.Processes{Total: 120 + int(5*math.Sin(t/30)), Running: 1 + rand.Intn(3)},
		Conns:     &protocol.Conns{TCP: 40 + int(10*math.Sin(t/50)), UDP: 6, TimeWait: 12 + rand.Intn(6)},
		Swap:      protocol.Swap{Total: 1 << 30, Used: 64 << 20},
		Disk: []protocol.Disk{
			{Mount: "/", Total: diskTotal, Used: diskUsed, Usage: pct(diskUsed, diskTotal), FSType: "ext4", Device: "/dev/vda1"},
			{Mount: "/data", Total: 100 << 30, Used: 87 << 30, Usage: 87, FSType: "xfs", Device: "/dev/vdb1"},
		},
		DiskIO: []protocol.DiskIO{
			{Device: "vda", ReadSpeed: ioRead, WriteSpeed: ioWrite, ReadBytes: 387 << 20, WriteBytes: 2200 << 20,
				ReadIOPS: r1(float64(ioRead) / 16384), WriteIOPS: r1(float64(ioWrite) / 16384), AwaitMs: 0.8, Util: r1(3 + 2*math.Sin(t/20))},
			{Device: "vdb", ReadSpeed: ioRead / 4, WriteSpeed: ioWrite / 8, ReadBytes: 33 << 20, WriteBytes: 42 << 30,
				ReadIOPS: 2, WriteIOPS: 5, AwaitMs: 1.2, Util: 1},
		},
		Network: []protocol.NetIface{{
			Interface: "eth0", IfIndex: 2, RxBytes: f.rx, TxBytes: f.tx, RxSpeed: rxSpeed, TxSpeed: txSpeed,
		}},
	}, nil
}
