//go:build !linux && !darwin

package server

import "errors"

// diskFree 在不支持 statfs 的系统上不检查（面板只发布 Linux 版本）。
func diskFree(string) (float64, uint64, error) {
	return 0, 0, errors.New("disk space check not supported on this OS")
}
