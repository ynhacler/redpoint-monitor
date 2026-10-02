package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// insertRaw 写入一个原始点。
func insertRaw(t *testing.T, st *Store, sid, ts int64, cpu float64, rx uint64) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO metrics_raw (server_id, ts, cpu, load1, mem_used, mem_total, swap_used,
		disk_used, disk_total, rx_speed, tx_speed) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		sid, ts, cpu, 0.5, 1000, 4000, 0, 10, 100, rx, rx/2); err != nil {
		t.Fatal(err)
	}
}

func TestPercentile(t *testing.T) {
	cases := []struct {
		name string
		v    []float64
		want float64
	}{
		{"空", nil, 0},
		{"单个值", []float64{7}, 7},
		{"6 个点（1 分钟 10 秒粒度）取最大值附近", []float64{1, 2, 3, 4, 5, 100}, 100},
		{"20 个点取第 19 个", func() []float64 {
			v := make([]float64, 20)
			for i := range v {
				v[i] = float64(20 - i)
			}
			return v
		}(), 19},
	}
	for _, c := range cases {
		if got := percentile(c.v, 0.95); got != c.want {
			t.Errorf("%s：%v，应为 %v", c.name, got, c.want)
		}
	}
}

func TestDownsampleLevels(t *testing.T) {
	s, _, _ := testServer(t)
	st := s.store
	base := int64(1_790_000_000) - int64(1_790_000_000)%3600 // 整点
	// 两个节点，两小时、每 10 秒一个点。节点 1 的 CPU：每分钟前 5 个点为 10，最后一个点为 70
	for ts := base; ts < base+7200; ts += 10 {
		cpu := 10.0
		if (ts-base)%60 == 50 {
			cpu = 70
		}
		insertRaw(t, st, 1, ts, cpu, uint64(ts-base))
		insertRaw(t, st, 2, ts, 50, 1000)
	}
	now := time.Unix(base+7200+60, 0)
	n, err := st.Downsample(now)
	if err != nil {
		t.Fatal(err)
	}
	if n["metrics_1m"] != 240 || n["metrics_5m"] != 48 || n["metrics_1h"] != 4 {
		t.Errorf("各级桶数：%v，应为 1m=240 5m=48 1h=4", n)
	}

	var cpu, cpuMax, cpuMin, p95 float64
	var cnt int64
	st.DB.QueryRow(`SELECT n, cpu, cpu_max, cpu_min, cpu_p95 FROM metrics_1m WHERE server_id = 1 AND ts = ?`, base).
		Scan(&cnt, &cpu, &cpuMax, &cpuMin, &p95)
	if cnt != 6 || cpu != 20 || cpuMax != 70 || cpuMin != 10 || p95 != 70 {
		t.Errorf("1 分钟桶：n=%d avg=%v max=%v min=%v p95=%v，应为 6 / 20 / 70 / 10 / 70", cnt, cpu, cpuMax, cpuMin, p95)
	}
	// 逐级按点数加权：1 小时平均值必须等于直接对原始点求平均（设计 21）
	var hourAvg, rawAvg float64
	st.DB.QueryRow(`SELECT cpu FROM metrics_1h WHERE server_id = 1 AND ts = ?`, base).Scan(&hourAvg)
	st.DB.QueryRow(`SELECT AVG(cpu) FROM metrics_raw WHERE server_id = 1 AND ts >= ? AND ts < ?`, base, base+3600).Scan(&rawAvg)
	if math.Abs(hourAvg-rawAvg) > 1e-9 {
		t.Errorf("1 小时平均 %v 与原始点平均 %v 不一致", hourAvg, rawAvg)
	}
	var hourMax uint64
	st.DB.QueryRow(`SELECT rx_speed_max FROM metrics_1h WHERE server_id = 1 AND ts = ?`, base).Scan(&hourMax)
	if hourMax != 3590 {
		t.Errorf("1 小时最大网速 %d，应为 3590", hourMax)
	}

	// 再跑一次不重复写入；进度保存在 downsample_state
	if n, _ := st.Downsample(now); n["metrics_1m"] != 0 || n["metrics_5m"] != 0 || n["metrics_1h"] != 0 {
		t.Errorf("重复运行不应再写入：%v", n)
	}
}

func TestDownsampleSettleAndCatchUp(t *testing.T) {
	s, _, _ := testServer(t)
	st := s.store
	base := int64(1_790_000_000) - int64(1_790_000_000)%60
	for ts := base; ts < base+120; ts += 10 {
		insertRaw(t, st, 1, ts, 1, 1)
	}
	// 第一分钟结束 20 秒：仍在 30 秒的写入等待期内，不聚合（rawSettle）
	if n, _ := st.Downsample(time.Unix(base+80, 0)); n["metrics_1m"] != 0 {
		t.Errorf("刚结束不足 30 秒的分钟不应聚合：%v", n)
	}
	// 第一分钟结束 35 秒：聚合第一分钟；第二分钟还在进行中，不聚合
	if n, _ := st.Downsample(time.Unix(base+95, 0)); n["metrics_1m"] != 1 {
		t.Errorf("应只聚合第一分钟：%v", n)
	}

	// 停机 10 小时后恢复：单次最多追 6 小时，避免长事务（maxCatchUp）
	for ts := base + 120; ts < base+10*3600; ts += 60 {
		insertRaw(t, st, 1, ts, 1, 1)
	}
	st.Downsample(time.Unix(base+10*3600+60, 0))
	done, _ := doneUntil(st.DB, "metrics_1m")
	if done-base > int64(maxCatchUp.Seconds())+60 {
		t.Errorf("单次追赶超过上限：%d 秒", done-base)
	}
	st.Downsample(time.Unix(base+10*3600+60, 0))
	if done, _ = doneUntil(st.DB, "metrics_1m"); done != base+10*3600 {
		t.Errorf("第二次应追上：done_until=%d", done-base)
	}
}

// 【数据可靠】超过保留期但尚未聚合的原始数据不能删除（设计 1.6.14、21）。
func TestPruneWaitsForAggregation(t *testing.T) {
	s, _, _ := testServer(t)
	st := s.store
	old := time.Now().Add(-30 * time.Hour).Unix()
	old -= old % 60
	for ts := old; ts < old+600; ts += 10 {
		insertRaw(t, st, 1, ts, 1, 1)
	}
	if n, err := st.PruneExpired(time.Now()); err != nil || n != 0 {
		t.Fatalf("未聚合前不应删除：删除 %d 行，%v", n, err)
	}
	st.Downsample(time.Now())
	n, err := st.PruneExpired(time.Now())
	if err != nil || n != 60 {
		t.Errorf("聚合后应删除 60 行过期原始数据，实际 %d，%v", n, err)
	}
	var left int
	st.DB.QueryRow(`SELECT COUNT(*) FROM metrics_1m`).Scan(&left)
	if left != 10 {
		t.Errorf("1 分钟数据在保留期内，应保留 10 行：%d", left)
	}
	if err := st.Vacuum(); err != nil {
		t.Errorf("incremental_vacuum：%v", err)
	}
	var mode int
	st.DB.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode)
	if mode != 2 {
		t.Errorf("数据库应为 auto_vacuum = INCREMENTAL：%d", mode)
	}
}

func TestHistoryEndpoint(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	id, _, _ := s.store.CreateServer("hist", 0, 1)
	now := time.Now().Unix()
	for ts := now - 3*3600; ts < now; ts += 10 {
		insertRaw(t, s.store, id, ts-ts%10, 5, 100)
	}
	s.store.Downsample(time.Now())

	cases := []struct {
		rng  string
		res  int64
		minN int
	}{
		{"1h", 10, 300}, {"6h", 10, 1000}, {"24h", 60, 170}, {"7d", 300, 30}, {"", 10, 300},
	}
	for _, c := range cases {
		rec := do(h, "GET", "/api/v1/servers/"+itoa(id)+"/metrics/history?range="+c.rng, admin, nil)
		var v historyView
		json.Unmarshal(rec.Body.Bytes(), &v)
		if rec.Code != 200 || v.Resolution != c.res || len(v.Items) < c.minN {
			t.Errorf("range=%q：%d 粒度 %d 点数 %d，应为粒度 %d、至少 %d 点", c.rng, rec.Code, v.Resolution, len(v.Items), c.res, c.minN)
		}
	}
	if rec := do(h, "GET", "/api/v1/servers/"+itoa(id)+"/metrics/history?range=2h", admin, nil); rec.Code != 422 {
		t.Errorf("不支持的范围应返回 422：%d", rec.Code)
	}
	if rec := do(h, "GET", "/api/v1/servers/999/metrics/history", admin, nil); rec.Code != 404 {
		t.Errorf("不存在的节点应返回 404：%d", rec.Code)
	}
}

// 后台任务 panic 后自动重启（设计 43.3.1、43.10）。
func TestRunTaskRestartsAfterPanic(t *testing.T) {
	s, _, logs := testServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	calls := 0
	s.runTask(ctx, "test", func(context.Context) {
		calls++
		if calls == 1 {
			panic("boom")
		}
	})
	if calls != 2 {
		t.Errorf("panic 后应重启一次并正常结束，实际调用 %d 次", calls)
	}
	if !strings.Contains(logs.String(), "background task panicked") {
		t.Error("panic 应记录 ERROR 与堆栈")
	}
}

// 磁盘 IO：旧版 Agent 的时段为 NULL，聚合时只用有值的点，历史接口返回 null（设计 4.7）。
func TestDiskIOAggregation(t *testing.T) {
	s, _, _ := testServer(t)
	st := s.store
	base := int64(1_790_000_000) - int64(1_790_000_000)%3600
	for ts := base; ts < base+600; ts += 10 {
		insertRaw(t, st, 1, ts, 1, 1)
		if ts >= base+300 { // 后 5 分钟升级为新版 Agent，开始上报 IO
			st.DB.Exec(`UPDATE metrics_raw SET disk_read = ?, disk_write = 2000 WHERE server_id = 1 AND ts = ?`, (ts-base)%60*10, ts)
		}
	}
	st.Downsample(time.Unix(base+3600+120, 0))

	var oldRead, newRead, newMax, fiveMinRead sql.NullInt64
	st.DB.QueryRow(`SELECT disk_read FROM metrics_1m WHERE server_id = 1 AND ts = ?`, base).Scan(&oldRead)
	st.DB.QueryRow(`SELECT disk_read, disk_read_max FROM metrics_1m WHERE server_id = 1 AND ts = ?`, base+300).Scan(&newRead, &newMax)
	st.DB.QueryRow(`SELECT disk_read FROM metrics_5m WHERE server_id = 1 AND ts = ?`, base+300).Scan(&fiveMinRead)
	if oldRead.Valid {
		t.Errorf("旧版 Agent 时段的 IO 应为 NULL，实际 %v", oldRead.Int64)
	}
	if newRead.Int64 != 250 || newMax.Int64 != 500 {
		t.Errorf("1 分钟 IO：avg=%d max=%d，应为 250 / 500", newRead.Int64, newMax.Int64)
	}
	if fiveMinRead.Int64 != 250 {
		t.Errorf("5 分钟 IO 应为 250（只用有值的桶加权），实际 %d", fiveMinRead.Int64)
	}

	pts, err := st.MetricsHistory(1, "metrics_1m", time.Unix(base, 0))
	if err != nil || len(pts) < 10 {
		t.Fatalf("history：%v %d", err, len(pts))
	}
	if pts[0].DiskRead != nil || pts[5].DiskRead == nil || *pts[5].DiskWrite != 2000 {
		t.Errorf("历史接口：无数据时段应为 null，有数据时段有值")
	}
}
