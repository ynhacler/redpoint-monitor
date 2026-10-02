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

func TestCycleEnd(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		name  string
		start time.Time
		day   int
		want  time.Time
	}{
		{"每月 1 日", time.Date(2026, 10, 1, 0, 0, 0, 0, loc), 1, time.Date(2026, 11, 1, 0, 0, 0, 0, loc)},
		{"每月 15 日", time.Date(2026, 9, 15, 0, 0, 0, 0, loc), 15, time.Date(2026, 10, 15, 0, 0, 0, 0, loc)},
		{"31 日遇到 2 月，结束于 2 月最后一天（设计 5.4）", time.Date(2027, 1, 31, 0, 0, 0, 0, loc), 31, time.Date(2027, 2, 28, 0, 0, 0, 0, loc)},
		{"跨年", time.Date(2026, 12, 20, 0, 0, 0, 0, loc), 20, time.Date(2027, 1, 20, 0, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		if got := CycleEnd(c.start, c.day); !got.Equal(c.want) {
			t.Errorf("%s：%v，应为 %v", c.name, got, c.want)
		}
	}
}

func TestEffectiveUsed(t *testing.T) {
	cases := []struct {
		name     string
		measured uint64
		factor   float64
		adj      int64
		want     uint64
	}{
		{"无系数无校准", 1000, 1, 0, 1000},
		{"系数 1.03 修正协议开销（设计 5.7）", 1000, 1.03, 0, 1030},
		{"校准为服务商数值：统计偏低", 1000, 1, 250, 1250},
		{"校准为服务商数值：统计偏高", 1000, 1, -300, 700},
		{"校准后结果不会为负", 100, 1, -500, 0},
		{"系数未设置按 1 处理", 1000, 0, 0, 1000},
	}
	for _, c := range cases {
		if got := EffectiveUsed(c.measured, c.factor, c.adj); got != c.want {
			t.Errorf("%s：%d，应为 %d", c.name, got, c.want)
		}
	}
}

func TestForecastUsage(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC) // 30 天周期，便于对照设计 32 的示例
	gb := uint64(1e9)
	cases := []struct {
		name        string
		used, last7 uint64
		limit       uint64
		now         time.Time
		want        Forecast
	}{
		{"周期开始不足 3 天不预测（设计 32）", 50 * gb, 0, 1000 * gb, start.Add(2 * 24 * time.Hour), Forecast{}},
		{"不满 7 天：用周期日均", 100 * gb, 0, 1000 * gb, start.Add(4 * 24 * time.Hour),
			Forecast{Available: true, Daily: 25 * gb, Total: 750 * gb}},
		{"设计 32 示例：20 天用 638 GB，最近 7 天同样速度", 638 * gb, 7 * 31900 * gb / 1000, 1000 * gb, start.Add(20 * 24 * time.Hour),
			Forecast{Available: true, Daily: 31900 * gb / 1000, Total: 957 * gb}},
		{"最近 7 天用量上升：按近期日均预测，提示超额", 500 * gb, 7 * 60 * gb, 1000 * gb, start.Add(20 * 24 * time.Hour),
			Forecast{Available: true, Daily: 60 * gb, Total: 1100 * gb, Over: true}},
		{"不限流量不提示超额", 500 * gb, 7 * 60 * gb, 0, start.Add(20 * 24 * time.Hour),
			Forecast{Available: true, Daily: 60 * gb, Total: 1100 * gb}},
	}
	for _, c := range cases {
		if got := ForecastUsage(c.used, c.last7, c.limit, start, end, c.now); got != c.want {
			t.Errorf("%s：%+v，应为 %+v", c.name, got, c.want)
		}
	}
}

func TestGBToBytes(t *testing.T) {
	if GBToBytes(1000, UnitDecimal) != 1_000_000_000_000 || GBToBytes(1, UnitBinary) != 1<<30 || GBToBytes(1.5, "") != 1_500_000_000 {
		t.Error("单位换算错误（设计 5.8）")
	}
}
