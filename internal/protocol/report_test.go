package protocol

import "testing"

func TestCounterDelta(t *testing.T) {
	const top = 1<<32 - 1
	cases := []struct {
		name      string
		prev, cur uint64
		bits      int
		want      uint64
		wantOK    bool
	}{
		{"正常递增", 100, 250, 64, 150, true},
		{"不变", 100, 100, 32, 0, true},
		{"64 位计数回退：重置", 5000, 10, 64, 0, false},
		{"位数未知：按 64 位处理", top - 99, 50, 0, 0, false},
		{"32 位回绕：补算", top - 99, 50, 32, 150, true},
		{"32 位回绕到 0", top, 0, 32, 1, true},
		{"32 位但回绕增量超过 2 GiB：更像重置", 1 << 30, 10, 32, 0, false},
		{"prev 超出 32 位范围：不可能是 32 位回绕", 1 << 33, 10, 32, 0, false},
	}
	for _, c := range cases {
		got, ok := CounterDelta(c.prev, c.cur, c.bits)
		if got != c.want || ok != c.wantOK {
			t.Errorf("%s：(%d, %v)，应为 (%d, %v)", c.name, got, ok, c.want, c.wantOK)
		}
	}
}
