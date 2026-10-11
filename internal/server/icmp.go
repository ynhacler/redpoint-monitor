package server

// 服务监控的 ICMP 检查（设计 33.2.1）：回显请求见 internal/netprobe；面板不会为此申请特权。

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"vpsmon/internal/netprobe"
)

const icmpAttempts = 3 // 一次检查最多发 3 个请求，收到任一回复即为正常

// resolvePingTarget 解析目标：IP 直接使用；域名优先 IPv4。
func resolvePingTarget(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip, nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip, nil
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("域名没有 A / AAAA 记录")
	}
	return ips[0], nil
}

// checkICMP 依次发送回显请求（各等 1 秒，不超过检查的超时），返回第一个回复的往返时间。
func checkICMP(ctx context.Context, host string) (time.Duration, error) {
	ip, err := resolvePingTarget(ctx, host)
	if err != nil {
		return 0, err
	}
	for attempt := 0; attempt < icmpAttempts && ctx.Err() == nil; attempt++ {
		actx, cancel := context.WithTimeout(ctx, time.Second)
		rtt, err := netprobe.Echo(actx, ip)
		cancel()
		if err == nil {
			return rtt, nil
		}
		if !errors.Is(err, netprobe.ErrNoReply) && !errors.Is(err, context.DeadlineExceeded) {
			return 0, err
		}
	}
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return 0, fmt.Errorf("%d 个请求均无回复", icmpAttempts)
}
