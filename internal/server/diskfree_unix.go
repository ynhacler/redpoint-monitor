//go:build linux || darwin

package server

import "syscall"

// diskFree 返回 dir 所在文件系统对普通用户可用的剩余空间（百分比与字节，设计 16.1）。
func diskFree(dir string) (float64, uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	total := uint64(st.Blocks) * uint64(st.Bsize)
	free := uint64(st.Bavail) * uint64(st.Bsize)
	if total == 0 {
		return 100, free, nil
	}
	return float64(free) / float64(total) * 100, free, nil
}
