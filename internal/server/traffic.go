package server

import "time"

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
