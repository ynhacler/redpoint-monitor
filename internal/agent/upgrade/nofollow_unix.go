//go:build unix

package upgrade

import (
	"os"
	"syscall"
)

// openNoFollow 以只读方式打开文件；路径的最后一段是符号链接时失败（O_NOFOLLOW）。
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
