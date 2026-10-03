package server

import (
	"math"
	"slices"
	"time"
)

// Counter is the last seen cumulative kernel counter for one interface of one server.
type Counter struct {
	BootID  string
	IfIndex int
	Rx, Tx  uint64
}

// ComputeDelta implements the traffic rules from docs/design.md 5.5.
//
//	prev == nil                      → first sighting: baseline only, delta 0
//	                                   (traffic before monitoring started is unknown)
//	boot_id or ifindex changed       → reboot / NIC recreated: delta = cur
//	                                   (counts traffic between boot and first report)
//	same boot, counter increased     → delta = cur - prev
//	same boot, counter went backward → driver reset / overflow: delta = cur, reset=true
func ComputeDelta(prev *Counter, cur Counter) (dRx, dTx uint64, reset bool) {
	if prev == nil {
		return 0, 0, false
	}
	if prev.BootID != cur.BootID || prev.IfIndex != cur.IfIndex {
		return cur.Rx, cur.Tx, true
	}
	if cur.Rx < prev.Rx || cur.Tx < prev.Tx {
		return cur.Rx, cur.Tx, true
	}
	return cur.Rx - prev.Rx, cur.Tx - prev.Tx, false
}

// Count modes (design 1.2.4).
const (
	ModeSum = "sum" // RX + TX
	ModeRx  = "rx"
	ModeTx  = "tx"
	ModeMax = "max" // MAX(RX, TX) over the whole cycle
)

func CountedBytes(mode string, rx, tx uint64) uint64 {
	switch mode {
	case ModeRx:
		return rx
	case ModeTx:
		return tx
	case ModeMax:
		return max(rx, tx)
	default:
		return rx + tx
	}
}

// CycleStart returns the start of the billing cycle containing now, for a monthly
// reset on resetDay (1-31). Short months clamp to their last day, e.g. reset day 31
// starts on Feb 28/29.
func CycleStart(now time.Time, resetDay int) time.Time {
	if resetDay < 1 {
		resetDay = 1
	}
	y, m, d := now.Date()
	start := clampDay(y, m, resetDay, now.Location())
	if d < start.Day() {
		start = clampDay(y, m-1, resetDay, now.Location())
	}
	return start
}

func clampDay(y int, m time.Month, day int, loc *time.Location) time.Time {
	first := time.Date(y, m, 1, 0, 0, 0, 0, loc)
	last := first.AddDate(0, 1, -1).Day()
	if day > last {
		day = last
	}
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, loc)
}

// CycleEnd 返回计费周期的结束时间（即下一次重置的 0 点，不含），与 CycleStart 规则一致（设计 5.4）。
func CycleEnd(start time.Time, resetDay int) time.Time {
	if resetDay < 1 {
		resetDay = 1
	}
	return clampDay(start.Year(), start.Month()+1, resetDay, start.Location())
}

// EffectiveUsed 计算展示用的本周期已用量（设计 5.7）：统计值 × 系数 + 校准偏差，结果不小于 0。
//
// 系数用于长期修正固定比例偏差（如服务商多计 3% 协议开销，系数 1.03）；
// 偏差来自手动校准：用户填写服务商面板的数值后，adjustment = 填写值 − 当时的（统计值 × 系数）。
func EffectiveUsed(measured uint64, factor float64, adjustment int64) uint64 {
	if factor <= 0 {
		factor = 1
	}
	v := float64(measured)*factor + float64(adjustment)
	if v < 0 {
		return 0
	}
	return uint64(v + 0.5)
}

// AdjustmentNow 返回一次校准在当前系数与计费模式下的偏差（设计 5.7）。
//
// 校准的含义是“校准那一刻，服务商面板显示 reported”。记录里保存了当时的原始收发字节，
// 偏差 = reported − 原始字节按当前模式取值 × 当前系数；这样校准后再修改系数或计费模式，
// 校准那一刻的已用量仍等于服务商数值，不会把系数与偏差重复叠加。
// 迁移 17 之前的记录没有原始字节，沿用当时算好的固定偏差。
func AdjustmentNow(a Adjustment, mode string, factor float64) int64 {
	if a.RawRx == nil || a.RawTx == nil || *a.RawRx < 0 || *a.RawTx < 0 {
		return a.Adjustment
	}
	return a.Reported - int64(EffectiveUsed(CountedBytes(mode, uint64(*a.RawRx), uint64(*a.RawTx)), factor, 0))
}

// FactorSample 是一次校准中可用于估算系数的样本。
type FactorSample struct {
	At       time.Time // 校准时间
	Raw      uint64    // 校准时本周期按当前计费模式取值的原始字节（未乘系数）
	Reported int64     // 服务商面板显示的已用量
}

// 系数建议的条件（设计 5.7）。
const (
	factorWindow     = 5                  // 只看最近 5 个样本，反映近期口径
	minFactorSamples = 3                  // 至少 3 次校准
	minFactorSpan    = 7 * 24 * time.Hour // 最早与最近样本至少相隔 7 天，同一天反复校准不算“长期”
	minFactorRaw     = 1_000_000_000      // 统计值不足 1 GB 时比例噪声太大，不作为样本
	maxFactorSpread  = 0.01               // 各样本比例的极差不超过 1 个百分点才算“稳定”
	minFactorChange  = 0.01               // 与当前系数相差不足 1% 时不提示
)

// SuggestFactor 根据多次校准判断统计值与服务商数值是否存在稳定的比例偏差，是则返回建议系数（设计 5.7）。
//
//	每个样本的比例 = 服务商数值 ÷ 原始统计值；同一天多次校准只取最后一次
//	最近 5 个样本中至少 3 个、跨度至少 7 天，且比例极差 ≤ 1 个百分点 → 建议系数 = 比例中位数，保留两位小数
//	建议值超出 0.5～2，或与当前系数相差不足 1% 时不提示
//
// 比例稳定才提示：首个周期从中途开始监控时，服务商多算的是一个固定量而不是比例，
// 各次校准的比例会随用量增长逐渐变小，不满足极差条件，因此不会误导用户设置系数。
func SuggestFactor(samples []FactorSample, current float64) (float64, bool) {
	if current <= 0 {
		current = 1
	}
	sorted := slices.Clone(samples)
	slices.SortStableFunc(sorted, func(a, b FactorSample) int { return a.At.Compare(b.At) })
	var picked []FactorSample // 按时间升序，每天只保留最后一次
	for _, s := range sorted {
		if s.Raw < minFactorRaw || s.Reported <= 0 {
			continue
		}
		if n := len(picked); n > 0 && picked[n-1].At.Format("2006-01-02") == s.At.Format("2006-01-02") {
			picked[n-1] = s
			continue
		}
		picked = append(picked, s)
	}
	if len(picked) > factorWindow {
		picked = picked[len(picked)-factorWindow:]
	}
	if len(picked) < minFactorSamples || picked[len(picked)-1].At.Sub(picked[0].At) < minFactorSpan {
		return 0, false
	}
	ratios := make([]float64, len(picked))
	for i, s := range picked {
		ratios[i] = float64(s.Reported) / float64(s.Raw)
	}
	slices.Sort(ratios)
	if ratios[len(ratios)-1]-ratios[0] > maxFactorSpread {
		return 0, false
	}
	median := ratios[len(ratios)/2]
	if len(ratios)%2 == 0 {
		median = (ratios[len(ratios)/2-1] + median) / 2
	}
	f := math.Round(median*100) / 100
	if f < 0.5 || f > 2 || math.Abs(f-current) < minFactorChange-1e-9 {
		return 0, false
	}
	return f, true
}

// Forecast 是周期结束时的预计用量（设计 32）。
type Forecast struct {
	Available bool   // 周期开始不足 3 天时为 false：样本太少，预测容易误报
	Daily     uint64 // 采用的日均：有满 7 天数据时用最近 7 天，否则用本周期日均
	Total     uint64 // 预计周期结束时的用量
	Over      bool   // 预计超过套餐额度（不限流量时恒为 false）
}

// minForecastDays：周期开始不足 3 天时不显示预测（设计 32）。
const minForecastDays = 3.0

// ForecastUsage 按设计 32 计算预测：
//
//	日均 = 最近 7 天用量 ÷ 7（周期已满 7 天时）；否则 本周期已用 ÷ 已过天数
//	预计 = 已用 + 日均 × 剩余天数
//
// used 为校准后的已用量；last7 为最近 7 个完整天的用量（同样乘以系数），由调用方汇总。
func ForecastUsage(used, last7, limit uint64, start, end, now time.Time) Forecast {
	elapsed := now.Sub(start).Hours() / 24
	if elapsed < minForecastDays || !now.Before(end) {
		return Forecast{}
	}
	remaining := end.Sub(now).Hours() / 24
	daily := float64(used) / elapsed
	if elapsed >= 7 {
		daily = float64(last7) / 7 // 更贴近近期用量变化（设计 32）
	}
	total := uint64(float64(used) + daily*remaining + 0.5)
	return Forecast{Available: true, Daily: uint64(daily + 0.5), Total: total, Over: limit > 0 && total > limit}
}

// 计量单位口径（设计 5.8）：服务商对 “1 TB” 的定义不一致，每个节点可选。
const (
	UnitDecimal = "decimal" // 1 GB = 10^9 Byte（多数服务商，默认）
	UnitBinary  = "binary"  // 1 GiB = 2^30 Byte
)

// GBToBytes 按节点的单位口径把 GB / GiB 换算为字节。
func GBToBytes(gb float64, unit string) int64 {
	if unit == UnitBinary {
		return int64(math.Round(gb * (1 << 30)))
	}
	return int64(math.Round(gb * 1e9))
}
