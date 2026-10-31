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
		Processes:  &protocol.Processes{Total: 120 + int(5*math.Sin(t/30)), Running: 1 + rand.Intn(3)},
		Containers: fakeContainers(t),
		Firewall:   fakeFirewall(),
		Proxies:    fakeProxies(),
		Accounts: &protocol.Accounts{
			Users:  []protocol.AccountUser{{Name: "deploy", UID: 1000, Shell: "/bin/bash"}, {Name: "root", UID: 0, Shell: "/bin/bash"}},
			Groups: []protocol.AccountGroup{{Name: "sudo", Members: []string{"deploy"}}},
		},
		ConfigFiles: []protocol.ConfigFile{
			{Path: "/etc/crontab", Size: 1042, MTime: 1700000000, SHA256: "4f0e4b0d6f6c1a3a5bde1b4e6f0f6a2d9f1c3b7a8e2d4c6b0a9f8e7d6c5b4a39"},
			{Path: "/etc/ssh/sshd_config", Size: 3253, MTime: 1700000000, SHA256: "9b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c"},
			{Path: "/etc/sudoers", Size: 1800, MTime: 1700000000},
			{Path: "/etc/sudoers.d", Dir: true, MTime: 1700000000},
		},
		Ports: []protocol.ListenPort{
			{Proto: "tcp", Port: 22, Addrs: []string{"0.0.0.0", "::"}},
			{Proto: "tcp", Port: 80, Addrs: []string{"0.0.0.0", "::"}},
			{Proto: "tcp", Port: 443, Addrs: []string{"0.0.0.0", "::"}},
			{Proto: "tcp", Port: 3306, Addrs: []string{"127.0.0.1"}},
			{Proto: "tcp", Port: 6379, Addrs: []string{"0.0.0.0"}},
			{Proto: "tcp", Port: 8080, Addrs: []string{"127.0.0.1"}},
			{Proto: "udp", Port: 53, Addrs: []string{"127.0.0.53"}},
			{Proto: "udp", Port: 443, Addrs: []string{"::"}},
		},
		Conns: &protocol.Conns{TCP: 40 + int(10*math.Sin(t/50)), UDP: 6, TimeWait: 12 + rand.Intn(6)},
		Swap:  protocol.Swap{Total: 1 << 30, Used: 64 << 20},
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

// fakeContainers 是开发用的示例容器（设计 33.4）：三个容器，CPU 与内存缓慢变化。
func fakeContainers(t float64) []protocol.Container {
	pct := func(base, amp, period float64) *float64 { v := base + amp*math.Sin(t/period); return &v }
	cs := []protocol.Container{
		{ID: "3f9a1c2b7d4e", Runtime: "docker", CPU: pct(12, 8, 25), Memory: uint64(310e6 + 40e6*math.Sin(t/90)), MemLimit: 512 << 20, Pids: 23, Command: "nginx"},
		{ID: "a81d0e55c6f2", Runtime: "docker", CPU: pct(2, 1, 40), Memory: 96 << 20, Pids: 4, Command: "node"},
		{ID: "c04be1f7a993", Runtime: "podman", CPU: pct(0.4, 0.2, 60), Memory: 18 << 20, MemLimit: 64 << 20, Pids: 1, Command: "redis-server", Restarts: 2},
	}
	sortContainers(cs)
	return cs
}

// fakeFirewall 是开发用的示例防火墙（修订第 168 条）：ufw 已启用、默认拒绝；fail2ban 保护 sshd；nftables 规则只有 root 可读。
func fakeFirewall() *protocol.Firewall {
	return &protocol.Firewall{Tools: []protocol.FirewallTool{
		{Name: "ufw", Active: true, Policy: "deny", RulesReadable: true, Source: "/etc/ufw/user.rules", Rules: []protocol.FirewallRule{
			{Action: "allow", Proto: "tcp", Port: "22"}, {Action: "allow", Port: "443"}, {Action: "limit", Proto: "tcp", Port: "2222", From: "203.0.113.0/24"},
		}},
		{Name: "nftables", Active: true},
		{Name: "fail2ban", Active: true, RulesReadable: true, Source: "/etc/fail2ban/jail.local、jail.d", Rules: []protocol.FirewallRule{
			{Action: "ban", Service: "sshd"},
		}},
	}}
}

// fakeProxies 是开发用的示例代理（修订第 169 条）：xray 的 Reality 与回环 API、sing-box 的 Hysteria 2（端口没在监听）、未运行的 Trojan-Go。
func fakeProxies() []protocol.Proxy {
	return []protocol.Proxy{
		{Name: "xray", Running: true, Procs: 1, Config: "/usr/local/etc/xray/config.json", Inbounds: []protocol.ProxyInbound{
			{Protocol: "vless", Port: 443, Network: "tcp", Security: "reality", Listening: true},
			{Protocol: "dokodemo-door", Port: 10085, Network: "tcp", Local: true, Listening: true},
		}},
		{Name: "sing-box", Running: true, Procs: 1, Config: "/etc/sing-box/config.json", Inbounds: []protocol.ProxyInbound{
			{Protocol: "hysteria2", Port: 8443, Network: "udp", Security: "tls"},
		}},
		{Name: "trojan-go", Config: "/etc/trojan-go/config.json", Inbounds: []protocol.ProxyInbound{
			{Protocol: "trojan", Port: 4443, Network: "tcp", Transport: "ws", Security: "tls"},
		}},
	}
}
