package netprobe

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"
	"time"
)

// fakeTracer 按 TTL 返回预设的回复。
type fakeTracer struct {
	hops map[int]hopKind
	err  map[int]error
}

func (f *fakeTracer) Hop(_ context.Context, ttl int) (netip.Addr, time.Duration, hopKind, error) {
	if e := f.err[ttl]; e != nil {
		return netip.Addr{}, 0, hopNone, e
	}
	k := f.hops[ttl]
	if k == hopNone {
		return netip.Addr{}, 0, hopNone, nil
	}
	return netip.AddrFrom4([4]byte{10, 0, 0, byte(ttl)}), time.Duration(ttl) * time.Millisecond, k, nil
}
func (f *fakeTracer) Close() error { return nil }

func TestTraceLoop(t *testing.T) {
	ctx := context.Background()
	// 到达目标：中间没有回复的跳保留为空
	hops, reached, err := traceLoop(ctx, &fakeTracer{hops: map[int]hopKind{1: hopExceeded, 3: hopExceeded, 4: hopReply}}, 30, time.Second)
	if err != nil || !reached || len(hops) != 4 || hops[1].Addr.IsValid() || hops[3].TTL != 4 || hops[2].RTT != 3*time.Millisecond {
		t.Fatalf("到达目标：%v %v %+v", err, reached, hops)
	}
	// 连续 5 跳没有回复时放弃，末尾没有回复的跳去掉
	hops, reached, _ = traceLoop(ctx, &fakeTracer{hops: map[int]hopKind{1: hopExceeded, 2: hopExceeded}}, 30, time.Second)
	if reached || len(hops) != 2 {
		t.Fatalf("放弃：%v %+v", reached, hops)
	}
	// 不可达时结束
	hops, _, _ = traceLoop(ctx, &fakeTracer{hops: map[int]hopKind{1: hopExceeded, 2: hopUnreach, 3: hopReply}}, 30, time.Second)
	if len(hops) != 2 {
		t.Fatalf("不可达：%+v", hops)
	}
	// 最多 maxHops 跳
	all := map[int]hopKind{}
	for i := 1; i <= 40; i++ {
		all[i] = hopExceeded
	}
	if hops, _, _ = traceLoop(ctx, &fakeTracer{hops: all}, 30, time.Second); len(hops) != 30 {
		t.Fatalf("最多 30 跳：%d", len(hops))
	}
	// 第一跳就发送失败：返回错误；之后失败：返回已有的跳
	boom := errors.New("network is unreachable")
	if _, _, err = traceLoop(ctx, &fakeTracer{err: map[int]error{1: boom}}, 30, time.Second); !errors.Is(err, boom) {
		t.Fatalf("第一跳失败：%v", err)
	}
	if hops, _, err = traceLoop(ctx, &fakeTracer{hops: map[int]hopKind{1: hopExceeded}, err: map[int]error{2: boom}}, 30, time.Second); err != nil || len(hops) != 1 {
		t.Fatalf("中途失败：%v %+v", err, hops)
	}
}

func TestParseExtErr(t *testing.T) {
	v4 := make([]byte, 16+16)
	v4[4], v4[5] = 2, 11
	copy(v4[20:], []byte{59, 43, 1, 2})
	if a, k, ok := parseExtErr(false, v4); !ok || k != hopExceeded || a != netip.MustParseAddr("59.43.1.2") {
		t.Fatalf("IPv4 TTL 超时：%v %v %v", a, k, ok)
	}
	v4[5] = 3
	if _, k, ok := parseExtErr(false, v4); !ok || k != hopUnreach {
		t.Fatalf("IPv4 不可达：%v %v", k, ok)
	}
	v4[4] = 1 // 本机产生的错误（SO_EE_ORIGIN_LOCAL）不是某一跳的回复
	if _, _, ok := parseExtErr(false, v4); ok {
		t.Fatal("本机错误应忽略")
	}
	v6 := make([]byte, 16+28)
	v6[4], v6[5] = 3, 3
	want := netip.MustParseAddr("2400:da00::6666")
	b := want.As16()
	copy(v6[24:], b[:])
	if a, k, ok := parseExtErr(true, v6); !ok || k != hopExceeded || a != want {
		t.Fatalf("IPv6 TTL 超时：%v %v %v", a, k, ok)
	}
	if _, _, ok := parseExtErr(true, v6[:30]); ok {
		t.Fatal("太短的消息应忽略")
	}
}

func TestErrSeq(t *testing.T) {
	b := icmpPacket(false, 1, 42, []byte("vpsmon"))
	if !errSeq(false, b, 42) || errSeq(false, b, 43) || !errSeq(false, b[:4], 7) {
		t.Fatal("IPv4 序号判断错误")
	}
	b6 := icmpPacket(true, 1, 9, nil)
	binary.BigEndian.PutUint16(b6[6:], 9)
	if !errSeq(true, b6, 9) || errSeq(false, b6, 9) {
		t.Fatal("IPv6 序号判断错误")
	}
}
