package sdnotify

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestNotify(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	if sent, err := Notify("READY=1"); sent || err != nil {
		t.Fatal("没有 NOTIFY_SOCKET 时应为空操作")
	}
	// macOS 的 unix 套接字路径上限约 104 字节，用短的临时目录
	dir, err := os.MkdirTemp("", "sd")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "n.sock")
	l, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	t.Setenv("NOTIFY_SOCKET", path)
	if sent, err := Notify("WATCHDOG=1"); !sent || err != nil {
		t.Fatalf("应发送成功：%v %v", sent, err)
	}
	buf := make([]byte, 64)
	l.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := l.ReadFromUnix(buf)
	if err != nil || string(buf[:n]) != "WATCHDOG=1" {
		t.Errorf("收到 %q %v", buf[:n], err)
	}
}

func TestWatchdogInterval(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "120000000")
	t.Setenv("WATCHDOG_PID", "")
	if d := WatchdogInterval(); d != 2*time.Minute {
		t.Errorf("应为 2 分钟：%s", d)
	}
	t.Setenv("WATCHDOG_PID", strconv.Itoa(os.Getpid()+1))
	if d := WatchdogInterval(); d != 0 {
		t.Error("WATCHDOG_PID 不是本进程时不启用")
	}
	t.Setenv("WATCHDOG_PID", "")
	t.Setenv("WATCHDOG_USEC", "")
	if d := WatchdogInterval(); d != 0 {
		t.Error("未设置时不启用")
	}
}
