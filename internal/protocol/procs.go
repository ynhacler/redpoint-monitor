package protocol

import "strings"

// 进程监控（设计 33.5.1）：面板在上报响应头中下发要关注的进程名，Agent 只统计这些名称各有几个进程在运行。
// 【安全】下发的只是用于比较的名称，不是命令；Agent 不执行任何东西，也不上报进程列表（约束 1、设计 4.9）。

// HeaderWatchProcs 是下发进程名的响应头，值为逗号分隔的名称；没有该响应头表示不关注任何进程。
const HeaderWatchProcs = "X-Watch-Procs"

const (
	MaxWatchProcs  = 20 // 每个节点最多关注的进程数
	MaxProcNameLen = 64
	procCommMaxLen = 15 // /proc/PID/comm 最多 15 个字符，更长的名称被内核截断
)

// ValidProcName 判断进程名是否合法：1～64 个字符，只含字母、数字与 . _ - + : @。
func ValidProcName(s string) bool {
	if s == "" || len(s) > MaxProcNameLen {
		return false
	}
	for _, c := range s {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-+:@", c)
		if !ok {
			return false
		}
	}
	return true
}

// ParseWatchProcs 解析逗号分隔的进程名：去空白、去重，跳过不合法的名称，最多 MaxWatchProcs 个。
func ParseWatchProcs(v string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(v, ",") {
		n := strings.TrimSpace(part)
		if !ValidProcName(n) || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
		if len(out) == MaxWatchProcs {
			break
		}
	}
	return out
}

// ProcNameMatches 判断 /proc/PID/comm 是否对应进程名：相同，或名称超过 15 个字符时与其前 15 个字符相同。
func ProcNameMatches(comm, name string) bool {
	if comm == name {
		return true
	}
	return len(name) > procCommMaxLen && comm == name[:procCommMaxLen]
}
