package server

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// ErrDataDirLocked 表示数据目录正被另一个进程使用（通常是运行中的面板）。
var ErrDataDirLocked = errors.New("数据目录正在被使用：请先停止 vpsmon-server（如 systemctl stop vpsmon-server）")

// LockDataDir 对数据目录加排他锁（DATA/.lock，flock）。vpsmon-server run 运行期间持有，
// restore 据此拒绝在面板运行时替换数据库；进程退出（包括崩溃）时锁由内核自动释放，不会残留。
func LockDataDir(dir string) (unlock func(), err error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrDataDirLocked
		}
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
