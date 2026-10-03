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
		{"normal increase", &base, Counter{"a", 2, 1500, 700, 0}, 500, 200, false},
		{"reboot with larger counters still detected", &base, Counter{"b", 2, 5000, 5000, 0}, 5000, 5000, true},
		{"nic recreated", &base, Counter{"a", 7, 10, 20, 0}, 10, 20, true},
		{"counter went backward", &base, Counter{"a", 2, 10, 600, 0}, 10, 600, true},
		{"32 位内核：接收计数回绕，按回绕补算", &Counter{"a", 2, 1<<32 - 1000, 500, 0}, Counter{"a", 2, 24, 700, 32}, 1024, 200, false},
		{"位数未知时回退仍按重置", &Counter{"a", 2, 1<<32 - 1000, 500, 0}, Counter{"a", 2, 24, 700, 0}, 24, 700, true},
		{"32 位但另一方向异常回退：整体重置", &Counter{"a", 2, 1<<32 - 1000, 1 << 31, 0}, Counter{"a", 2, 24, 10, 32}, 24, 10, true},
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

func TestAdjustmentNow(t *testing.T) {
	i64 := func(v int64) *int64 { return &v }
	// 校准时：rx 600、tx 400，系数 1，sum 模式，服务商显示 1030 → 偏差 30
	a := Adjustment{Reported: 1030, Adjustment: 30, RawRx: i64(600), RawTx: i64(400)}
	cases := []struct {
		name   string
		a      Adjustment
		mode   string
		factor float64
		want   int64
	}{
		{"设置未变", a, ModeSum, 1, 30},
		{"之后设置系数 1.03：偏差归零，不重复修正", a, ModeSum, 1.03, 0},
		{"之后改为仅 TX：校准时的 TX 为 400，偏差 630", a, ModeTx, 1, 630},
		{"旧记录没有原始字节：沿用固定偏差", Adjustment{Reported: 1030, Adjustment: 30}, ModeSum, 1.03, 30},
	}
	for _, c := range cases {
		if got := AdjustmentNow(c.a, c.mode, c.factor); got != c.want {
			t.Errorf("%s：%d，应为 %d", c.name, got, c.want)
		}
	}
}

func TestSuggestFactor(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	gb := uint64(1e9)
	at := func(days int) time.Time { return t0.AddDate(0, 0, days) }
	s := func(days int, raw uint64, pct int64) FactorSample {
		return FactorSample{At: at(days), Raw: raw, Reported: int64(raw) * pct / 1000}
	}
	cases := []struct {
		name    string
		samples []FactorSample
		current float64
		want    float64
		ok      bool
	}{
		{"稳定偏低 3%：建议 1.03", []FactorSample{s(0, 100*gb, 1030), s(5, 200*gb, 1031), s(10, 300*gb, 1029)}, 1, 1.03, true},
		{"样本不足 3 个", []FactorSample{s(0, 100*gb, 1030), s(10, 300*gb, 1030)}, 1, 0, false},
		{"跨度不足 7 天", []FactorSample{s(0, 100*gb, 1030), s(2, 200*gb, 1030), s(4, 300*gb, 1030)}, 1, 0, false},
		{"同一天多次校准只算一次", []FactorSample{s(0, 100*gb, 1030), s(0, 110*gb, 1030), s(10, 300*gb, 1030)}, 1, 0, false},
		{"比例不稳定（极差 2%）", []FactorSample{s(0, 100*gb, 1020), s(5, 200*gb, 1040), s(10, 300*gb, 1030)}, 1, 0, false},
		{"固定差额而非比例：比例逐渐变小，不提示", []FactorSample{
			{At: at(0), Raw: 50 * gb, Reported: 60e9}, {At: at(5), Raw: 200 * gb, Reported: 210e9}, {At: at(10), Raw: 400 * gb, Reported: 410e9}}, 1, 0, false},
		{"统计值不足 1 GB 的样本忽略", []FactorSample{s(0, gb/2, 1500), s(1, 100*gb, 1030), s(5, 200*gb, 1030), s(10, 300*gb, 1030)}, 1, 1.03, true},
		{"已设置相同系数：不提示", []FactorSample{s(0, 100*gb, 1030), s(5, 200*gb, 1030), s(10, 300*gb, 1030)}, 1.03, 0, false},
		{"只看最近 5 个：早期口径不同的样本被挤出", []FactorSample{s(0, 100*gb, 1100), s(1, 100*gb, 1100),
			s(2, 100*gb, 980), s(4, 100*gb, 980), s(6, 100*gb, 980), s(8, 100*gb, 980), s(10, 100*gb, 980)}, 1, 0.98, true},
		{"建议值超出 0.5～2 不提示", []FactorSample{s(0, 100*gb, 2500), s(5, 200*gb, 2500), s(10, 300*gb, 2500)}, 1, 0, false},
		{"顺序无关", []FactorSample{s(10, 300*gb, 1030), s(0, 100*gb, 1030), s(5, 200*gb, 1030)}, 1, 1.03, true},
	}
	for _, c := range cases {
		got, ok := SuggestFactor(c.samples, c.current)
		if ok != c.ok || got != c.want {
			t.Errorf("%s：(%v, %v)，应为 (%v, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}
