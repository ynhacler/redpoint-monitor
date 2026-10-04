// Package protocol defines the wire format between monitor-agent and monitor-server.
// Keep it backward compatible: only add optional fields, never rename or repurpose.
// See docs/design.md chapter 6.
package protocol

// Report is sent by the agent every report interval via POST /api/v1/agent/report.
// The server identifies the node from the Agent Token, never from fields in the body.
type Report struct {
	Timestamp    int64  `json:"timestamp"` // unix seconds, agent clock
	AgentVersion string `json:"agent_version"`
	Final        bool   `json:"final,omitempty"` // last report sent on SIGTERM
	// 可选：发送时刻的 Agent 时钟（Unix 秒）。与采集时间不同，补发的旧数据也带当前发送时间，
	// 面板据此计算时钟偏差（设计 16.1、43.5）
	SentAt    int64        `json:"sent_at,omitempty"`
	System    System       `json:"system"`
	CPU       CPU          `json:"cpu"`
	Memory    Memory       `json:"memory"`
	Swap      Swap         `json:"swap"`
	Disk      []Disk       `json:"disk"`
	Network   []NetIface   `json:"network"`
	DiskIO    []DiskIO     `json:"disk_io,omitempty"`   // 可选：旧版 Agent 不带（设计 4.7）
	Processes *Processes   `json:"processes,omitempty"` // 可选（设计 4.9）
	Conns     *Conns       `json:"conns,omitempty"`     // 可选（设计 4.9）
	Ports     []ListenPort `json:"ports,omitempty"`     // 可选：本机监听端口（设计 4.9.1）
	// 可选：本轮失败的采集项及原因；对应字段留空，其余照常上报（设计 43.5）
	CollectErrors []CollectError `json:"collect_errors,omitempty"`
	// 可选：扩展指标（设计 4.10）。面板保存在实时状态中、随接口返回，暂不入库也不展示
	Extra *Extra `json:"extra,omitempty"`
}

// Extra 是扩展指标（设计 4.10）：都来自无需特权的只读来源，读不到的项省略。
// 速率为与上一次采样之间的平均值，首次采样时省略。
type Extra struct {
	Activity    *Activity    `json:"activity,omitempty"`
	VM          *VMStat      `json:"vm,omitempty"`
	Pressure    *Pressure    `json:"pressure,omitempty"`
	MemoryMore  *MemoryMore  `json:"memory_more,omitempty"`
	Net         *NetStack    `json:"net,omitempty"`
	FileHandles *FileHandles `json:"file_handles,omitempty"`
	Conntrack   *Conntrack   `json:"conntrack,omitempty"`
	Clock       *ClockSync   `json:"clock,omitempty"`
	Env         *Environment `json:"env,omitempty"`
}

// Activity 取自 /proc/stat：上下文切换、中断、新建进程的每秒速率，以及阻塞在 IO 上的进程数。
type Activity struct {
	CtxSwitches  float64 `json:"ctx_switches"` // 次/秒
	Interrupts   float64 `json:"interrupts"`   // 次/秒
	Forks        float64 `json:"forks"`        // 新建进程/秒
	ProcsBlocked int     `json:"procs_blocked"`
}

// VMStat 取自 /proc/vmstat：主缺页与换入换出速率，以及开机以来的 OOM kill 次数（内核 4.13+）。
type VMStat struct {
	MajorFaults float64 `json:"major_faults"` // 次/秒；持续偏高说明内存不足、频繁从磁盘读回
	SwapIn      float64 `json:"swap_in"`      // 字节/秒
	SwapOut     float64 `json:"swap_out"`     // 字节/秒
	OOMKills    *uint64 `json:"oom_kills,omitempty"`
}

// Pressure 是 PSI（/proc/pressure，内核 4.20+）：资源不足而等待的时间占比，0～100。
// some：至少一个任务在等；full：所有非空闲任务都在等（CPU 的 full 在多数内核上无意义，不采集）。
type Pressure struct {
	CPUSome10    float64 `json:"cpu_some_avg10"`
	CPUSome60    float64 `json:"cpu_some_avg60"`
	MemorySome10 float64 `json:"memory_some_avg10"`
	MemoryFull10 float64 `json:"memory_full_avg10"`
	IOSome10     float64 `json:"io_some_avg10"`
	IOFull10     float64 `json:"io_full_avg10"`
}

// MemoryMore 是 /proc/meminfo 的其他字段，单位字节。
type MemoryMore struct {
	Shmem         uint64 `json:"shmem"`          // 共享内存与 tmpfs
	SlabUnreclaim uint64 `json:"slab_unreclaim"` // 不可回收的内核 slab，持续增长多为内核侧泄漏
	Dirty         uint64 `json:"dirty"`          // 等待写回磁盘
	Writeback     uint64 `json:"writeback"`
	Committed     uint64 `json:"committed"`    // Committed_AS：已承诺的虚拟内存
	CommitLimit   uint64 `json:"commit_limit"` // 超过后（严格超额承诺模式下）分配会失败
}

// NetStack 取自 /proc/net/snmp：TCP 重传率、建连速率与错误计数。累计值为开机以来。
type NetStack struct {
	TCPEstablished  uint64  `json:"tcp_established"`
	TCPRetransRate  float64 `json:"tcp_retrans_rate"`  // 重传报文占发送报文的百分比；线路质量差时升高
	TCPActiveOpens  float64 `json:"tcp_active_opens"`  // 主动建连/秒
	TCPPassiveOpens float64 `json:"tcp_passive_opens"` // 被动建连/秒
	TCPInErrs       uint64  `json:"tcp_in_errs"`       // 累计
	TCPAttemptFails uint64  `json:"tcp_attempt_fails"` // 累计
	UDPRcvbufErrors uint64  `json:"udp_rcvbuf_errors"` // 累计：接收缓冲区满而丢弃
	UDPSndbufErrors uint64  `json:"udp_sndbuf_errors"` // 累计
	UDPInErrors     uint64  `json:"udp_in_errors"`     // 累计
}

// FileHandles 取自 /proc/sys/fs/file-nr。
type FileHandles struct {
	Allocated uint64 `json:"allocated"`
	Max       uint64 `json:"max"`
}

// Conntrack 取自 /proc/sys/net/netfilter/nf_conntrack_{count,max}；未加载 conntrack 时省略。
// 表满后新连接被丢弃，代理 / NAT 机器常见。
type Conntrack struct {
	Count uint64 `json:"count"`
	Max   uint64 `json:"max"`
}

// ClockSync 取自 adjtimex（只读调用，无需特权）：内核是否认为时钟已同步，及最大误差。
type ClockSync struct {
	Synced     bool  `json:"synced"`
	MaxErrorUs int64 `json:"max_error_us"`
}

// Environment 是运行环境（静态信息，Agent 每 10 分钟刷新）。
type Environment struct {
	Virt       string `json:"virt,omitempty"`        // kvm / xen / vmware / hyperv / openvz / lxc / docker / podman / none
	DMIVendor  string `json:"dmi_vendor,omitempty"`  // /sys/class/dmi/id/sys_vendor，如 QEMU、Alibaba Cloud
	DMIProduct string `json:"dmi_product,omitempty"` // /sys/class/dmi/id/product_name
}

// CollectError 是一个采集项本轮失败的原因（设计 43.5）。只含采集项名称与简短原因，不含凭证或文件内容。
type CollectError struct {
	Item    string `json:"item"`    // cpu / memory / disk / disk_io / network / system / processes / conns / ports
	Message string `json:"message"` // 如 “/proc/meminfo 读取失败”、“statfs /data 超时”
}

// Processes 是进程数（设计 4.9）：/proc 下的进程目录数与 /proc/stat 的 procs_running。
type Processes struct {
	Total   int `json:"total"`
	Running int `json:"running"`
}

// ListenPort 是一个监听端口（设计 4.9.1）。同一协议与端口的多个监听地址合并为一项。
// 只有本机监听的端口，不含连接明细、对端地址与进程信息。
type ListenPort struct {
	Proto string   `json:"proto"` // tcp / udp
	Port  int      `json:"port"`
	Addrs []string `json:"addrs"` // 监听地址，如 0.0.0.0、::、127.0.0.1
}

// Conns 是套接字数（设计 4.9），取自 /proc/net/sockstat 与 sockstat6 的 inuse（IPv4 + IPv6）。
// TCP 不含 TIME_WAIT，TIME_WAIT 单独列出，便于发现短连接风暴。
type Conns struct {
	TCP      int `json:"tcp"`
	UDP      int `json:"udp"`
	TimeWait int `json:"time_wait"`
}

type System struct {
	Hostname  string `json:"hostname"`
	OS        string `json:"os"`
	OSVersion string `json:"os_version"`
	Kernel    string `json:"kernel"`
	Arch      string `json:"arch"`
	Uptime    uint64 `json:"uptime"`              // seconds
	BootID    string `json:"boot_id"`             // /proc/sys/kernel/random/boot_id, used for traffic reset detection
	CPUModel  string `json:"cpu_model,omitempty"` // 可选：/proc/cpuinfo 的 model name（设计 4.4）
	// 可选：内核网卡计数器的位数（32 / 64）；32 位内核上计数器约 4 GiB 回绕，面板据此补算（设计 5.5）。
	// 省略表示未知，按 64 位处理（回退一律视为重置）。
	CounterBits int `json:"counter_bits,omitempty"`
	// 可选：本机是否启用了远程升级（装有 updater 且没有 no-remote-upgrade，设计 29.13）。
	// 省略表示旧版 Agent、未知；面板据此在创建升级任务时直接说明原因，而不是让任务一直等待。
	RemoteUpgrade *bool `json:"remote_upgrade,omitempty"`
}

type CPU struct {
	Usage  float64 `json:"usage"` // percent 0-100
	Cores  int     `json:"cores"`
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
	// 以下均为可选（设计 4.4）：旧版 Agent 不带，界面不显示对应区块
	Breakdown *CPUBreakdown `json:"breakdown,omitempty"`
	PerCore   []float64     `json:"per_core,omitempty"` // 每个核心的使用率 0～100，按 cpu0、cpu1… 顺序
	TempC     float64       `json:"temp_c,omitempty"`   // CPU 温度（摄氏度）；读不到时省略，VPS 上通常没有
}

// CPUBreakdown 是两次采样之间各类 CPU 时间的占比（0～100，合计约 100）。
// steal 高说明宿主机超售；iowait 高说明在等磁盘（设计 4.4）。
type CPUBreakdown struct {
	User    float64 `json:"user"`
	Nice    float64 `json:"nice"`
	System  float64 `json:"system"`
	IOWait  float64 `json:"iowait"`
	IRQ     float64 `json:"irq"`
	SoftIRQ float64 `json:"softirq"`
	Steal   float64 `json:"steal"`
	Idle    float64 `json:"idle"`
}

type Memory struct {
	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	Available uint64  `json:"available"`
	Usage     float64 `json:"usage"`
	// 可选（设计 4.5）：Free 为完全空闲；Buffers 与 Cached（含可回收 slab）可被回收，不计入 Used
	Free    uint64 `json:"free,omitempty"`
	Buffers uint64 `json:"buffers,omitempty"`
	Cached  uint64 `json:"cached,omitempty"`
}

type Swap struct {
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
}

// Disk 是一个挂载点的容量（设计 4.6）。字节；Usage 为 0～100，口径与 df 的 Use% 一致。
type Disk struct {
	Mount     string  `json:"mount"`
	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	Usage     float64 `json:"usage"`
	FSType    string  `json:"fstype,omitempty"`    // 可选，如 ext4
	Device    string  `json:"device,omitempty"`    // 可选，如 /dev/vda1
	Available uint64  `json:"available,omitempty"` // 可选：普通用户可用的字节（不含 root 保留块）
	// 可选（设计 4.10）：inode 总数与已用；inode 耗尽时即使还有空间也无法创建文件。btrfs 等不限 inode 的文件系统为 0
	InodesTotal uint64 `json:"inodes_total,omitempty"`
	InodesUsed  uint64 `json:"inodes_used,omitempty"`
}

// DiskIO 是一块磁盘的 IO 计数（设计 4.7）。累计值来自 /proc/diskstats；速率由 Agent 用两次采样计算。
type DiskIO struct {
	Device     string `json:"device"`      // 如 vda、nvme0n1
	ReadBytes  uint64 `json:"read_bytes"`  // 累计
	WriteBytes uint64 `json:"write_bytes"` // 累计
	ReadOps    uint64 `json:"read_ops"`    // 累计
	WriteOps   uint64 `json:"write_ops"`   // 累计
	IOTimeMs   uint64 `json:"io_time_ms"`  // 累计，设备忙碌的毫秒数
	ReadSpeed  uint64 `json:"read_speed"`  // 字节/秒
	WriteSpeed uint64 `json:"write_speed"` // 字节/秒
	// 可选（设计 4.7）：与上一次采样之间的平均值
	ReadIOPS  float64 `json:"read_iops,omitempty"`
	WriteIOPS float64 `json:"write_iops,omitempty"`
	AwaitMs   float64 `json:"await_ms,omitempty"`  // 每次 IO 的平均耗时（含排队），毫秒
	Util      float64 `json:"util,omitempty"`      // 设备忙碌时间占比 0～100
	InFlight  uint64  `json:"in_flight,omitempty"` // 可选（设计 4.10）：采样时正在处理的 IO 请求数（队列深度）
}

// NetIface carries cumulative kernel counters. The server computes traffic deltas;
// speeds are computed by the agent for display only.
type NetIface struct {
	Interface string `json:"interface"`
	IfIndex   int    `json:"ifindex"`
	RxBytes   uint64 `json:"rx_bytes"`
	TxBytes   uint64 `json:"tx_bytes"`
	RxSpeed   uint64 `json:"rx_speed"` // bytes/s
	TxSpeed   uint64 `json:"tx_speed"` // bytes/s
	// 可选（设计 4.10）：包速率与开机以来的错误、丢包累计
	RxPPS     float64 `json:"rx_pps,omitempty"`
	TxPPS     float64 `json:"tx_pps,omitempty"`
	RxErrors  uint64  `json:"rx_errors,omitempty"`
	TxErrors  uint64  `json:"tx_errors,omitempty"`
	RxDropped uint64  `json:"rx_dropped,omitempty"`
	TxDropped uint64  `json:"tx_dropped,omitempty"`
}

// maxWrapDelta：按 32 位回绕补算时，单次增量的上限（2 GiB）。超过说明更可能是计数器被重置，
// 而不是回绕（两次上报之间 10 秒，2 GiB 相当于 1.6 Gbps 持续满速）。
const maxWrapDelta = 1 << 31

// CounterDelta 返回累计计数从 prev 到 cur 的增量（设计 5.5）。cur ≥ prev 时为差值；
// 计数回退时，若计数器是 32 位、prev 在 32 位范围内且回绕后的增量不超过 2 GiB，按回绕补算；
// 否则返回 ok=false，由调用方按重置处理。Agent 计算网速与面板计算流量共用这一规则。
func CounterDelta(prev, cur uint64, bits int) (delta uint64, ok bool) {
	if cur >= prev {
		return cur - prev, true
	}
	const max32 = 1<<32 - 1
	if bits == 32 && prev <= max32 {
		if d := max32 - prev + 1 + cur; d <= maxWrapDelta {
			return d, true
		}
	}
	return 0, false
}
