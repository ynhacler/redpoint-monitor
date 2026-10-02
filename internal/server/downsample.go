package server

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

// 降采样与数据保留（设计 18.4、21）。
//
//	metrics_raw  10 秒   保留 24 小时
//	metrics_1m   1 分钟  保留 7 天    ← 由 raw 聚合（avg / max / min / cpu p95）
//	metrics_5m   5 分钟  保留 30 天   ← 由 1m 按点数加权聚合
//	metrics_1h   1 小时  保留 1 年    ← 由 5m 按点数加权聚合
//
// 每一级记录 done_until（此前的桶已完成），聚合只处理 [done_until, 可完成的最晚桶)，可以中断后继续。
// 【数据可靠】源数据在被下一级聚合之前不会被删除：面板停机或聚合落后时，保留期清理会等待聚合追上（设计 1.6.14）。

// aggLevel 描述一级聚合。
type aggLevel struct {
	table     string
	src       string
	step      int64         // 桶宽，秒
	retention time.Duration // 本级保留时长
	fromRaw   bool
}

var aggLevels = []aggLevel{
	{"metrics_1m", "metrics_raw", 60, 7 * 24 * time.Hour, true},
	{"metrics_5m", "metrics_1m", 300, 30 * 24 * time.Hour, false},
	{"metrics_1h", "metrics_5m", 3600, 365 * 24 * time.Hour, false},
}

const (
	// rawSettle：原始点写库有批量延迟（flushEvery）与 Agent 上报间隔，最近 30 秒内的桶可能还不完整，暂不聚合
	rawSettle = 30 * time.Second
	// maxCatchUp：单次最多聚合 6 小时的桶，避免停机后首次运行时出现长事务阻塞写入
	maxCatchUp = 6 * time.Hour
	// deleteBatch：过期数据按批删除，每批一个短事务（设计 21）
	deleteBatch = 5000
)

func floorTo(t, step int64) int64 { return t - ((t%step)+step)%step }

// doneUntil 读取某一级的聚合进度；尚无记录时返回 0。
func doneUntil(q interface {
	QueryRow(string, ...any) *sql.Row
}, level string) (int64, error) {
	var v int64
	err := q.QueryRow(`SELECT done_until FROM downsample_state WHERE level = ?`, level).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

// Downsample 执行一轮逐级聚合，返回各级新写入的桶数（用于日志与测试）。
func (s *Store) Downsample(now time.Time) (map[string]int64, error) {
	out := map[string]int64{}
	srcDone := now.Add(-rawSettle).Unix() // raw 视为已“完成”到 now-30s
	for _, lv := range aggLevels {
		n, done, err := s.downsampleLevel(lv, srcDone)
		if err != nil {
			return out, fmt.Errorf("%s: %w", lv.table, err)
		}
		out[lv.table] = n
		srcDone = done // 下一级只能聚合本级已完成的部分
	}
	return out, nil
}

// downsampleLevel 聚合一级，返回写入的桶数与本级新的 done_until。
func (s *Store) downsampleLevel(lv aggLevel, srcDone int64) (int64, int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	from, err := doneUntil(tx, lv.table)
	if err != nil {
		return 0, 0, err
	}
	if from == 0 {
		// 首次运行：从源表最早的数据开始
		var min sql.NullInt64
		if err := tx.QueryRow(`SELECT MIN(ts) FROM ` + lv.src).Scan(&min); err != nil {
			return 0, 0, err
		}
		if !min.Valid {
			return 0, 0, nil
		}
		from = floorTo(min.Int64, lv.step)
	}
	to := floorTo(srcDone, lv.step)
	if limit := from + int64(maxCatchUp.Seconds()); to > limit {
		to = floorTo(limit, lv.step)
	}
	if to <= from {
		return 0, from, nil
	}

	var q string
	if lv.fromRaw {
		q = `INSERT OR REPLACE INTO ` + lv.table + `
			SELECT server_id, (ts / ?) * ? AS b, COUNT(*),
				AVG(cpu), MAX(cpu), MIN(cpu), MAX(cpu),
				AVG(load1), MAX(load1),
				CAST(AVG(mem_used) AS INTEGER), MAX(mem_used), MAX(mem_total),
				CAST(AVG(swap_used) AS INTEGER), MAX(swap_used),
				CAST(AVG(disk_used) AS INTEGER), MAX(disk_used), MAX(disk_total),
				CAST(AVG(rx_speed) AS INTEGER), MAX(rx_speed), MIN(rx_speed),
				CAST(AVG(tx_speed) AS INTEGER), MAX(tx_speed), MIN(tx_speed)
			FROM metrics_raw WHERE ts >= ? AND ts < ? GROUP BY server_id, b`
	} else {
		// 平均值按原始点数 n 加权，保证逐级聚合后的平均值与直接对原始点求平均一致
		q = `INSERT OR REPLACE INTO ` + lv.table + `
			SELECT server_id, (ts / ?) * ? AS b, SUM(n),
				SUM(cpu * n) / SUM(n), MAX(cpu_max), MIN(cpu_min), MAX(cpu_p95),
				SUM(load1 * n) / SUM(n), MAX(load1_max),
				CAST(SUM(mem_used * n) / SUM(n) AS INTEGER), MAX(mem_used_max), MAX(mem_total),
				CAST(SUM(swap_used * n) / SUM(n) AS INTEGER), MAX(swap_used_max),
				CAST(SUM(disk_used * n) / SUM(n) AS INTEGER), MAX(disk_used_max), MAX(disk_total),
				CAST(SUM(rx_speed * n) / SUM(n) AS INTEGER), MAX(rx_speed_max), MIN(rx_speed_min),
				CAST(SUM(tx_speed * n) / SUM(n) AS INTEGER), MAX(tx_speed_max), MIN(tx_speed_min)
			FROM ` + lv.src + ` WHERE ts >= ? AND ts < ? GROUP BY server_id, b`
	}
	res, err := tx.Exec(q, lv.step, lv.step, from, to)
	if err != nil {
		return 0, 0, err
	}
	n, _ := res.RowsAffected()

	if lv.fromRaw {
		if err := fillP95(tx, lv, from, to); err != nil {
			return 0, 0, err
		}
	}
	if _, err := tx.Exec(`INSERT INTO downsample_state (level, done_until) VALUES (?, ?)
		ON CONFLICT(level) DO UPDATE SET done_until = excluded.done_until`, lv.table, to); err != nil {
		return 0, 0, err
	}
	return n, to, tx.Commit()
}

// fillP95 用原始点计算 1 分钟桶的 CPU p95（设计 21）。SQLite 没有分位数函数，在 Go 中计算。
func fillP95(tx *sql.Tx, lv aggLevel, from, to int64) error {
	rows, err := tx.Query(`SELECT server_id, (ts / ?) * ? AS b, cpu FROM metrics_raw
		WHERE ts >= ? AND ts < ? AND cpu IS NOT NULL ORDER BY server_id, b`, lv.step, lv.step, from, to)
	if err != nil {
		return err
	}
	type key struct{ sid, b int64 }
	vals := map[key][]float64{}
	var order []key
	for rows.Next() {
		var k key
		var v float64
		if err := rows.Scan(&k.sid, &k.b, &v); err != nil {
			rows.Close()
			return err
		}
		if _, ok := vals[k]; !ok {
			order = append(order, k)
		}
		vals[k] = append(vals[k], v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range order {
		if _, err := tx.Exec(`UPDATE `+lv.table+` SET cpu_p95 = ? WHERE server_id = ? AND ts = ?`,
			percentile(vals[k], 0.95), k.sid, k.b); err != nil {
			return err
		}
	}
	return nil
}

// percentile 使用最近秩（nearest-rank）法计算分位数；空切片返回 0。
func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	i := int(math.Ceil(p*float64(len(s)))) - 1
	if i < 0 {
		i = 0
	}
	return s[i]
}

// PruneExpired 按保留期删除过期数据，返回删除的行数（设计 21）。
//
// 【数据可靠】每一级的删除截止时间取“保留期”与“下一级聚合进度”中较早者：
// 尚未被聚合的数据即使超过保留期也先保留，等聚合追上后再删。
func (s *Store) PruneExpired(now time.Time) (int64, error) {
	type rule struct {
		table   string
		keep    time.Duration
		guarded string // 下一级的表名；为空表示没有下一级
	}
	rules := []rule{
		{"metrics_raw", rawRetention, "metrics_1m"},
		{"metrics_1m", aggLevels[0].retention, "metrics_5m"},
		{"metrics_5m", aggLevels[1].retention, "metrics_1h"},
		{"metrics_1h", aggLevels[2].retention, ""},
	}
	var total int64
	for _, r := range rules {
		cutoff := now.Add(-r.keep).Unix()
		if r.guarded != "" {
			done, err := doneUntil(s.DB, r.guarded)
			if err != nil {
				return total, err
			}
			if done < cutoff {
				cutoff = done
			}
		}
		for {
			// 每批一个短事务，避免长时间持有写锁阻塞上报写入
			res, err := s.DB.Exec(`DELETE FROM `+r.table+` WHERE (server_id, ts) IN
				(SELECT server_id, ts FROM `+r.table+` WHERE ts < ? LIMIT ?)`, cutoff, deleteBatch)
			if err != nil {
				return total, fmt.Errorf("%s: %w", r.table, err)
			}
			n, _ := res.RowsAffected()
			total += n
			if n < deleteBatch {
				break
			}
		}
	}
	return total, nil
}

// Vacuum 归还已删除数据占用的页（需要 auto_vacuum = INCREMENTAL，见 OpenStore）。
func (s *Store) Vacuum() error {
	_, err := s.DB.Exec(`PRAGMA incremental_vacuum`)
	return err
}
