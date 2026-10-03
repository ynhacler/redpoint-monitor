package server

import (
	"database/sql"
	"errors"
	"time"
)

// 流量的持久化查询：手动校准记录（设计 5.7、18.12）、按日与按周期汇总（设计 19.8）。
// 计算规则（系数、校准、预测）在 traffic.go 的纯函数中。

// Adjustment 是一条手动校准记录。
type Adjustment struct {
	ID         int64  `json:"id"`
	CycleStart string `json:"cycle_start"`
	Measured   int64  `json:"measured_bytes"`
	Reported   int64  `json:"reported_bytes"`
	Adjustment int64  `json:"adjustment_bytes"`
	// 校准时本周期的原始收发字节（未乘系数）；迁移 17 之前的记录没有（设计 5.7）
	RawRx     *int64 `json:"raw_rx,omitempty"`
	RawTx     *int64 `json:"raw_tx,omitempty"`
	Note      string `json:"note"`
	CreatedAt int64  `json:"created_at"`
}

const adjustmentColumns = `id, cycle_start, measured_bytes, reported_bytes, adjustment_bytes, raw_rx, raw_tx, note, created_at`

func scanAdjustment(sc interface{ Scan(...any) error }) (Adjustment, error) {
	var a Adjustment
	err := sc.Scan(&a.ID, &a.CycleStart, &a.Measured, &a.Reported, &a.Adjustment, &a.RawRx, &a.RawTx, &a.Note, &a.CreatedAt)
	return a, err
}

// AddAdjustment 写入一条校准记录。
func (s *Store) AddAdjustment(serverID int64, a Adjustment) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO traffic_adjustments (server_id, cycle_start, measured_bytes, reported_bytes,
		adjustment_bytes, raw_rx, raw_tx, note, created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		serverID, a.CycleStart, a.Measured, a.Reported, a.Adjustment, a.RawRx, a.RawTx, a.Note, a.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// LatestAdjustment 返回某计费周期最近一次校准；没有时返回 nil。
// 每次校准都以服务商数值为准重新锚定，因此只需最近一条，不累加（设计 5.7）。
func (s *Store) LatestAdjustment(serverID int64, cycleStart string) (*Adjustment, error) {
	a, err := scanAdjustment(s.DB.QueryRow(`SELECT `+adjustmentColumns+`
		FROM traffic_adjustments WHERE server_id = ? AND cycle_start = ? ORDER BY id DESC LIMIT 1`, serverID, cycleStart))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &a, err
}

// ListAdjustments 返回节点的校准历史，最新在前（设计 5.7：可查看历史）。
func (s *Store) ListAdjustments(serverID int64, limit int) ([]Adjustment, error) {
	rows, err := s.DB.Query(`SELECT `+adjustmentColumns+`
		FROM traffic_adjustments WHERE server_id = ? ORDER BY id DESC LIMIT ?`, serverID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Adjustment{}
	for rows.Next() {
		a, err := scanAdjustment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DayTraffic 是一天的原始收发字节（未乘系数）。
type DayTraffic struct {
	Day string `json:"day"` // YYYY-MM-DD，面板本地时区
	Rx  uint64 `json:"rx"`
	Tx  uint64 `json:"tx"`
}

// DailyTraffic 返回 [from, to) 内每天的流量，按日期升序；没有数据的日期不返回。
func (s *Store) DailyTraffic(serverID int64, from, to time.Time) ([]DayTraffic, error) {
	rows, err := s.DB.Query(`SELECT day, rx, tx FROM traffic_daily WHERE server_id = ? AND day >= ? AND day < ? ORDER BY day`,
		serverID, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DayTraffic{}
	for rows.Next() {
		var d DayTraffic
		if err := rows.Scan(&d.Day, &d.Rx, &d.Tx); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// TrafficBetween 汇总 [from, to) 的收发字节。
func (s *Store) TrafficBetween(serverID int64, from, to time.Time) (rx, tx uint64, err error) {
	err = s.DB.QueryRow(`SELECT COALESCE(SUM(rx),0), COALESCE(SUM(tx),0) FROM traffic_daily
		WHERE server_id = ? AND day >= ? AND day < ?`, serverID, from.Format("2006-01-02"), to.Format("2006-01-02")).Scan(&rx, &tx)
	return
}
