//go:build !linux && !darwin

package netprobe

import "net"

func listenICMPDgram(bool) (net.PacketConn, error) { return nil, ErrICMPUnavailable }
