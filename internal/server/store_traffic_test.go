package server

import (
	"testing"
	"time"
)

// 每日上报小时数（修订第 235 条）：metrics_1h 与 metrics_5m 按小时合并去重，按流量时区的日期统计
func TestReportedHours(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	loc := time.FixedZone("UTC+8", 8*3600)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, loc)
	ins := func(table string, ts time.Time) {
		if _, err := st.DB.Exec(`INSERT INTO `+table+` (server_id, ts, n) VALUES (1, ?, 1)`, ts.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	// 10-20：3 个小时（其中 02 点在 1h 与 5m 中都有，只算一次）；10-21：1 个小时（只在 5m 中）；10-22：没有
	ins("metrics_1h", day.Add(1*time.Hour))
	ins("metrics_1h", day.Add(2*time.Hour))
	ins("metrics_5m", day.Add(2*time.Hour+10*time.Minute))
	ins("metrics_5m", day.Add(23*time.Hour+55*time.Minute))
	ins("metrics_5m", day.Add(24*time.Hour+5*time.Minute))
	got, err := st.ReportedHours(1, day, day.AddDate(0, 0, 3), loc)
	if err != nil {
		t.Fatal(err)
	}
	if got["2026-10-20"] != 3 || got["2026-10-21"] != 1 || got["2026-10-22"] != 0 {
		t.Fatalf("ReportedHours = %v", got)
	}
}
