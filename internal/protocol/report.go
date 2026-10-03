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
	AwaitMs   float64 `json:"await_ms,omitempty"` // 每次 IO 的平均耗时（含排队），毫秒
	Util      float64 `json:"util,omitempty"`     // 设备忙碌时间占比 0～100
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
