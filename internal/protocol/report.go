// Package protocol defines the wire format between monitor-agent and monitor-server.
// Keep it backward compatible: only add optional fields, never rename or repurpose.
// See docs/design.md chapter 6.
package protocol

// Report is sent by the agent every report interval via POST /api/v1/agent/report.
// The server identifies the node from the Agent Token, never from fields in the body.
type Report struct {
	Timestamp    int64      `json:"timestamp"` // unix seconds, agent clock
	AgentVersion string     `json:"agent_version"`
	Final        bool       `json:"final,omitempty"` // last report sent on SIGTERM
	System       System     `json:"system"`
	CPU          CPU        `json:"cpu"`
	Memory       Memory     `json:"memory"`
	Swap         Swap       `json:"swap"`
	Disk         []Disk     `json:"disk"`
	Network      []NetIface `json:"network"`
	DiskIO       []DiskIO   `json:"disk_io,omitempty"`   // 可选：旧版 Agent 不带（设计 4.7）
	Processes    *Processes `json:"processes,omitempty"` // 可选（设计 4.9）
	Conns        *Conns     `json:"conns,omitempty"`     // 可选（设计 4.9）
}

// Processes 是进程数（设计 4.9）：/proc 下的进程目录数与 /proc/stat 的 procs_running。
type Processes struct {
	Total   int `json:"total"`
	Running int `json:"running"`
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
