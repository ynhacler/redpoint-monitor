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
	DiskIO       []DiskIO   `json:"disk_io,omitempty"` // 可选：旧版 Agent 不带（设计 4.7）
}

type System struct {
	Hostname  string `json:"hostname"`
	OS        string `json:"os"`
	OSVersion string `json:"os_version"`
	Kernel    string `json:"kernel"`
	Arch      string `json:"arch"`
	Uptime    uint64 `json:"uptime"`  // seconds
	BootID    string `json:"boot_id"` // /proc/sys/kernel/random/boot_id, used for traffic reset detection
}

type CPU struct {
	Usage  float64 `json:"usage"` // percent 0-100
	Cores  int     `json:"cores"`
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

type Memory struct {
	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	Available uint64  `json:"available"`
	Usage     float64 `json:"usage"`
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
