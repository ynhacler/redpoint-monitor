package collector

// 运行中的进程（修订第 168 条）：防火墙与代理程序的识别共用一次 /proc 扫描，只读各进程的 comm（进程名），
// 不读命令行与环境变量。proc 为 /proc 的位置（测试中用临时目录）。

import (
	"os"
	"path/filepath"
	"strings"
)

// procInfo 是一个进程：PID 与进程名。
type procInfo struct {
	pid  string
	comm string
}

// scanProcs 列出全部进程；读不到 /proc 时返回空。
func scanProcs(proc string) []procInfo {
	ents, err := os.ReadDir(proc)
	if err != nil {
		return nil
	}
	out := make([]procInfo, 0, len(ents))
	for _, e := range ents {
		name := e.Name()
		if name == "" || name[0] < '0' || name[0] > '9' {
			continue
		}
		b, err := os.ReadFile(filepath.Join(proc, name, "comm"))
		if err != nil {
			continue // 进程已退出
		}
		if comm := strings.TrimSpace(string(b)); comm != "" {
			out = append(out, procInfo{pid: name, comm: comm})
		}
	}
	return out
}

// procNames 返回进程名集合。
func procNames(ps []procInfo) map[string]bool {
	out := make(map[string]bool, len(ps))
	for _, p := range ps {
		out[p.comm] = true
	}
	return out
}
