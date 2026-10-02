// Package collector 负责在 Agent 端采集系统指标（设计 4、5）。
//
// 只读取 /proc 与 /sys，不执行外部命令，不需要 root（设计 1.6.9）。
// 真实采集只支持 Linux；其他平台（开发用的 Mac）上 New() 返回假数据采集器，
// 便于在本地联调 Agent → 面板 → Web / App 全链路。NewFake() 可在 Linux 上强制使用假数据。
package collector

import (
	"path/filepath"

	"vpsmon/internal/protocol"
)

// Collector 是有状态的：CPU 使用率与网速依赖上一次调用的采样，
// 因此 Agent 运行期间必须复用同一个实例，且不能并发调用。
type Collector interface {
	// Collect 返回一份上报，不含 Timestamp / AgentVersion（由上报方填写）。
	Collect() (protocol.Report, error)
}

// Options 决定哪些网卡参与流量统计（设计 5.6）。
type Options struct {
	Interfaces []string // 显式指定的网卡；为空表示自动选择（默认路由网卡）
	Exclude    []string // glob 模式，只在自动选择时生效
}

// DefaultExclude 排除虚拟网卡，避免容器、网桥、VPN 流量被重复计算（设计 5.6）。
var DefaultExclude = []string{"lo", "docker*", "veth*", "br-*", "virbr*", "tun*", "wg*"}

// excluded 判断网卡名是否匹配任一 glob 模式（如 “veth*”）。
func excluded(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}

// pct 返回 used/total 的百分比（0～100）；total 为 0 时返回 0 而不是 NaN。
func pct(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) * 100 / float64(total)
}
