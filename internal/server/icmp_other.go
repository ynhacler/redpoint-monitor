//go:build !linux && !darwin

package server

import "net"

func listenICMPDgram(bool) (net.PacketConn, error) { return nil, errICMPUnavailable }
