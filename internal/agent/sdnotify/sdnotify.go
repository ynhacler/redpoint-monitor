// Package sdnotify 实现 systemd 的通知协议（sd_notify），只用标准库（设计 43.5 存活）。
//
// systemd 以 Type=notify 启动 Agent 时设置 NOTIFY_SOCKET；单元配置 WatchdogSec 时再设置 WATCHDOG_USEC。
// Agent 启动完成后发送 READY=1，主循环每轮发送 WATCHDOG=1：主循环卡住（而不是退出）超过 WatchdogSec，
// systemd 会杀掉并重启 Agent。不在 systemd 下运行（开发、手动运行）时所有函数都是空操作。
package sdnotify

import (
	"net"
	"os"
	"strconv"
	"time"
)

// Notify 向 systemd 发送一条状态（如 "READY=1"、"WATCHDOG=1"、"STOPPING=1"）。
// 没有 NOTIFY_SOCKET 时返回 false, nil。
func Notify(state string) (bool, error) {
	sock := os.Getenv("NOTIFY_SOCKET")
	if sock == "" {
		return false, nil
	}
	// “@” 开头表示 Linux 抽象命名空间的套接字
	if sock[0] == '@' {
		sock = "\x00" + sock[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: sock, Net: "unixgram"})
	if err != nil {
		return false, err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(state)); err != nil {
		return false, err
	}
	return true, nil
}

// WatchdogInterval 返回 systemd 要求的看门狗超时（WATCHDOG_USEC）；未启用时返回 0。
// WATCHDOG_PID 存在且不是本进程时也返回 0（通知是发给其他进程的）。
func WatchdogInterval() time.Duration {
	if pid := os.Getenv("WATCHDOG_PID"); pid != "" && pid != strconv.Itoa(os.Getpid()) {
		return 0
	}
	us, err := strconv.ParseInt(os.Getenv("WATCHDOG_USEC"), 10, 64)
	if err != nil || us <= 0 {
		return 0
	}
	return time.Duration(us) * time.Microsecond
}
