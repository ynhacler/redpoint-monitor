package collector

import (
	"testing"

	"vpsmon/internal/protocol"
)

func TestActivity(t *testing.T) {
	a := parseStatCounters("cpu 1 2 3\nintr 1000 5 6\nctxt 5000\nbtime 1\nprocesses 300\nprocs_running 2\nprocs_blocked 1\n")
	b := parseStatCounters("intr 3000 5 6\nctxt 9000\nprocesses 310\nprocs_blocked 3\n")
	if got := activityFrom(statCounters{}, b, 10); got != nil {
		t.Error("首次采样没有速率")
	}
	got := activityFrom(a, b, 10)
	if got == nil || got.CtxSwitches != 400 || got.Interrupts != 200 || got.Forks != 1 || got.ProcsBlocked != 3 {
		t.Errorf("系统活动：%+v", got)
	}
}

func TestVMStat(t *testing.T) {
	prev := parseKeyValues("pgmajfault 100\npswpin 10\npswpout 0\n")
	cur := parseKeyValues("pgmajfault 150\npswpin 30\npswpout 10\noom_kill 2\n")
	v := vmFrom(prev, cur, 10, 4096)
	if v == nil || v.MajorFaults != 5 || v.SwapIn != 2*4096 || v.SwapOut != 4096 || v.OOMKills == nil || *v.OOMKills != 2 {
		t.Errorf("vmstat：%+v", v)
	}
	if v := vmFrom(prev, parseKeyValues("pgmajfault 150\n"), 10, 4096); v.OOMKills != nil {
		t.Error("旧内核没有 oom_kill 时省略")
	}
}

func TestPSI(t *testing.T) {
	s := "some avg10=1.50 avg60=0.75 avg300=0.10 total=123\nfull avg10=0.20 avg60=0.00 avg300=0.00 total=4\n"
	if s10, s60, f10, ok := parsePSI(s); !ok || s10 != 1.5 || s60 != 0.75 || f10 != 0.2 {
		t.Errorf("PSI：%v %v %v %v", s10, s60, f10, ok)
	}
	if _, _, _, ok := parsePSI(""); ok {
		t.Error("没有 PSI（旧内核）时 ok=false")
	}
}

const snmpA = `Ip: Forwarding DefaultTTL
Ip: 1 64
Tcp: RtoAlgorithm RtoMin RtoMax MaxConn ActiveOpens PassiveOpens AttemptFails EstabResets CurrEstab InSegs OutSegs RetransSegs InErrs OutRsts InCsumErrors
Tcp: 1 200 120000 -1 100 200 3 4 12 5000 10000 50 1 7 0
Udp: InDatagrams NoPorts InErrors OutDatagrams RcvbufErrors SndbufErrors InCsumErrors IgnoredMulti MemErrors
Udp: 10 0 2 20 5 1 0 0 0
`

const snmpB = `Tcp: RtoAlgorithm RtoMin RtoMax MaxConn ActiveOpens PassiveOpens AttemptFails EstabResets CurrEstab InSegs OutSegs RetransSegs InErrs OutRsts InCsumErrors
Tcp: 1 200 120000 -1 120 260 3 4 15 6000 12000 70 2 7 0
Udp: InDatagrams NoPorts InErrors OutDatagrams RcvbufErrors SndbufErrors InCsumErrors IgnoredMulti MemErrors
Udp: 10 0 2 20 6 1 0 0 0
`

func TestNetStack(t *testing.T) {
	a, b := parseSnmp(snmpA), parseSnmp(snmpB)
	if a["Tcp"]["MaxConn"] != 0 || a["Ip"]["DefaultTTL"] != 64 {
		t.Errorf("解析 snmp：%v", a)
	}
	n := netStackFrom(a, b, 10)
	want := protocol.NetStack{TCPEstablished: 15, TCPRetransRate: 1, TCPActiveOpens: 2, TCPPassiveOpens: 6, TCPInErrs: 2,
		TCPAttemptFails: 3, UDPRcvbufErrors: 6, UDPSndbufErrors: 1, UDPInErrors: 2}
	if n == nil || *n != want {
		t.Errorf("TCP / UDP：%+v，应为 %+v", n, want)
	}
	if netStackFrom(nil, b, 10) != nil {
		t.Error("首次采样没有速率")
	}
}

func TestFileNrConntrack(t *testing.T) {
	if f := parseFileNr("1824\t0\t9223372036854775807\n"); f == nil || f.Allocated != 1824 || f.Max != 9223372036854775807 {
		t.Errorf("file-nr：%+v", f)
	}
	if parseFileNr("") != nil {
		t.Error("读不到时为 nil")
	}
	if c := parseConntrack("1234\n", "262144\n"); c == nil || c.Count != 1234 || c.Max != 262144 {
		t.Errorf("conntrack：%+v", c)
	}
	if parseConntrack("", "") != nil {
		t.Error("未加载 conntrack 时为 nil")
	}
}

func TestDetectVirt(t *testing.T) {
	cases := []struct {
		name string
		h    envHints
		want string
	}{
		{"OpenVZ 容器", envHints{openVZ: true, vendor: "QEMU"}, "openvz"},
		{"LXC（systemd 标记）", envHints{containerFile: "lxc", vendor: "QEMU"}, "lxc"},
		{"Docker", envHints{dockerEnv: true}, "docker"},
		{"Podman", envHints{podmanEnv: true}, "podman"},
		{"Xen", envHints{xen: true}, "xen"},
		{"KVM（QEMU）", envHints{vendor: "QEMU", product: "Standard PC (i440FX + PIIX, 1996)"}, "kvm"},
		{"阿里云", envHints{vendor: "Alibaba Cloud", product: "Alibaba Cloud ECS"}, "kvm"},
		{"VMware", envHints{vendor: "VMware, Inc."}, "vmware"},
		{"Hyper-V", envHints{vendor: "Microsoft Corporation", product: "Virtual Machine"}, "hyperv"},
		{"无法识别的虚拟机", envHints{hypervisorCPU: true, vendor: "Example"}, "vm"},
		{"物理机", envHints{vendor: "Dell Inc.", product: "PowerEdge R640"}, "none"},
	}
	for _, c := range cases {
		if got := detectVirt(c.h); got != c.want {
			t.Errorf("%s：%s，应为 %s", c.name, got, c.want)
		}
	}
	if !cpuHasHypervisorFlag("processor : 0\nflags : fpu vme hypervisor lahf_lm\n") || cpuHasHypervisorFlag("flags : fpu vme\n") {
		t.Error("hypervisor 标志")
	}
}

func TestMemoryMore(t *testing.T) {
	m := parseMeminfo("MemTotal: 1000 kB\nShmem: 10 kB\nSUnreclaim: 20 kB\nDirty: 3 kB\nWriteback: 0 kB\nCommitted_AS: 900 kB\nCommitLimit: 1500 kB\n")
	got := memoryMoreFrom(m)
	if got == nil || got.Shmem != 10*1024 || got.SlabUnreclaim != 20*1024 || got.Committed != 900*1024 || got.CommitLimit != 1500*1024 {
		t.Errorf("meminfo 其他字段：%+v", got)
	}
}
