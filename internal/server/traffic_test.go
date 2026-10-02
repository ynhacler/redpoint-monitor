package server

import (
	"testing"
	"time"
)

func TestComputeDelta(t *testing.T) {
	base := Counter{BootID: "a", IfIndex: 2, Rx: 1000, Tx: 500}
	cases := []struct {
		name           string
		prev           *Counter
		cur            Counter
		wantRx, wantTx uint64
		wantReset      bool
	}{
		{"first sighting", nil, base, 0, 0, false},
		{"normal increase", &base, Counter{"a", 2, 1500, 700}, 500, 200, false},
		{"reboot with larger counters still detected", &base, Counter{"b", 2, 5000, 5000}, 5000, 5000, true},
		{"nic recreated", &base, Counter{"a", 7, 10, 20}, 10, 20, true},
		{"counter went backward", &base, Counter{"a", 2, 10, 600}, 10, 600, true},
	}
	for _, c := range cases {
		rx, tx, reset := ComputeDelta(c.prev, c.cur)
		if rx != c.wantRx || tx != c.wantTx || reset != c.wantReset {
			t.Errorf("%s: got (%d,%d,%v) want (%d,%d,%v)", c.name, rx, tx, reset, c.wantRx, c.wantTx, c.wantReset)
		}
	}
}

func TestCycleStart(t *testing.T) {
	loc := time.UTC
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 12, 0, 0, 0, loc) }
	cases := []struct {
		now   time.Time
		reset int
		want  time.Time
	}{
		{d(2026, 10, 2), 1, time.Date(2026, 10, 1, 0, 0, 0, 0, loc)},
		{d(2026, 10, 2), 15, time.Date(2026, 9, 15, 0, 0, 0, 0, loc)},
		{d(2026, 10, 15), 15, time.Date(2026, 10, 15, 0, 0, 0, 0, loc)},
		{d(2026, 3, 1), 31, time.Date(2026, 2, 28, 0, 0, 0, 0, loc)},
		{d(2026, 2, 28), 31, time.Date(2026, 2, 28, 0, 0, 0, 0, loc)},
		{d(2026, 1, 5), 20, time.Date(2025, 12, 20, 0, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		if got := CycleStart(c.now, c.reset); !got.Equal(c.want) {
			t.Errorf("CycleStart(%s, %d) = %s, want %s", c.now.Format("2006-01-02"), c.reset, got.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
	}
}

func TestCountedBytes(t *testing.T) {
	if CountedBytes(ModeSum, 3, 4) != 7 || CountedBytes(ModeMax, 3, 4) != 4 || CountedBytes(ModeRx, 3, 4) != 3 {
		t.Fatal("count modes wrong")
	}
}
