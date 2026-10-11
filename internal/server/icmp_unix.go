//go:build linux || darwin

package server

import (
	"net"
	"os"
	"syscall"
)

// listenICMPDgram 打开非特权 ICMP 数据报套接字（Linux 的 ping socket、macOS 的 SOCK_DGRAM ICMP）。
func listenICMPDgram(v6 bool) (net.PacketConn, error) {
	family, proto := syscall.AF_INET, syscall.IPPROTO_ICMP
	if v6 {
		family, proto = syscall.AF_INET6, syscall.IPPROTO_ICMPV6
	}
	fd, err := syscall.Socket(family, syscall.SOCK_DGRAM, proto)
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fd)
	f := os.NewFile(uintptr(fd), "icmp")
	defer f.Close() // FilePacketConn 复制了描述符
	return net.FilePacketConn(f)
}
