//go:build unix

package setup

import (
	"os"
	"syscall"
)

// fileOwner 返回文件属主的 uid。
func fileOwner(fi os.FileInfo) (int, bool) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), true
	}
	return 0, false
}
