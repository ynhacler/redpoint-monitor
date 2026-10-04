package server

// 云厂商账户、费用与实例的存储（设计 44.7）。凭证只以密文进出这一层，解密在 cloud_sync.go。

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"vpsmon/internal/cloud"
)

// CloudAccount 是一行 cloud_accounts（不含凭证密文以外的明文凭证）。
type CloudAccount struct {
	ID             int64
	Provider       string
	Name           string
	Regions        []string
	CredentialEnc  []byte
	CredentialHint string
	BudgetCents    int64
	CostIntervalH  int
	Enabled        bool
	SyncCost       bool
	SyncTraffic    bool
	// 同步状态
	CostSyncedAt      int64
	InstancesSyncedAt int64
	TrafficSyncedAt   int64
	LastError         string
	ErrorSince        int64
	FailCount         int
	NextTryAt         int64
	AuthFailed        bool
	CreatedAt         int64
	UpdatedAt         int64
}

var errNoCloudAccount = errorf(CodeNotFound, "云账户不存在或已删除")

const cloudAccountCols = `id, provider, name, regions, credential_enc, credential_hint, budget_cents, cost_interval_h,
	enabled, sync_cost, sync_traffic, cost_synced_at, instances_synced_at, traffic_synced_at, last_error, error_since,
	fail_count, next_try_at, auth_failed, created_at, updated_at`

func scanCloudAccount(sc interface{ Scan(...any) error }) (CloudAccount, error) {
	var a CloudAccount
	var regions string
	err := sc.Scan(&a.ID, &a.Provider, &a.Name, &regions, &a.CredentialEnc, &a.CredentialHint, &a.BudgetCents,
		&a.CostIntervalH, &a.Enabled, &a.SyncCost, &a.SyncTraffic, &a.CostSyncedAt, &a.InstancesSyncedAt,
		&a.TrafficSyncedAt, &a.LastError, &a.ErrorSince, &a.FailCount, &a.NextTryAt, &a.AuthFailed, &a.CreatedAt, &a.UpdatedAt)
	if regions != "" {
		a.Regions = strings.Split(regions, ",")
	}
	return a, err
}

func (s *Store) ListCloudAccounts() ([]CloudAccount, error) {
	rows, err := s.DB.Query(`SELECT ` + cloudAccountCols + ` FROM cloud_accounts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CloudAccount
	for rows.Next() {
		a, err := scanCloudAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetCloudAccount(id int64) (CloudAccount, error) {
	a, err := scanCloudAccount(s.DB.QueryRow(`SELECT `+cloudAccountCols+` FROM cloud_accounts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, errNoCloudAccount
	}
	return a, err
}

// SaveCloudAccount 新增或修改账户的设置与凭证；同步状态只由 cloud_sync 更新。名称重复时返回 errNameTaken。
func (s *Store) SaveCloudAccount(a *CloudAccount, now time.Time) error {
	a.UpdatedAt = now.Unix()
	regions := strings.Join(a.Regions, ",")
	var err error
	if a.ID == 0 {
		a.CreatedAt = a.UpdatedAt
		var res sql.Result
		res, err = s.DB.Exec(`INSERT INTO cloud_accounts (provider, name, regions, credential_enc, credential_hint,
			budget_cents, cost_interval_h, enabled, sync_cost, sync_traffic, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, a.Provider, a.Name, regions, a.CredentialEnc, a.CredentialHint,
			a.BudgetCents, a.CostIntervalH, a.Enabled, a.SyncCost, a.SyncTraffic, a.CreatedAt, a.UpdatedAt)
		if err == nil {
			a.ID, _ = res.LastInsertId()
		}
	} else {
		// 凭证更换后清除“凭证失效”与退避，立即重新同步
		_, err = s.DB.Exec(`UPDATE cloud_accounts SET name = ?, regions = ?, credential_enc = ?, credential_hint = ?,
			budget_cents = ?, cost_interval_h = ?, enabled = ?, sync_cost = ?, sync_traffic = ?, updated_at = ?,
			auth_failed = ?, fail_count = ?, next_try_at = ?, last_error = ?, error_since = ? WHERE id = ?`,
			a.Name, regions, a.CredentialEnc, a.CredentialHint, a.BudgetCents, a.CostIntervalH, a.Enabled, a.SyncCost,
			a.SyncTraffic, a.UpdatedAt, a.AuthFailed, a.FailCount, a.NextTryAt, a.LastError, a.ErrorSince, a.ID)
	}
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return errNameTaken
	}
	return err
}

func (s *Store) DeleteCloudAccount(id int64) error {
	res, err := s.DB.Exec(`DELETE FROM cloud_accounts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoCloudAccount
	}
	return nil
}

// cloudSyncResult 是一次同步的结果：成功的部分更新对应的时间，失败时记录错误与下次重试时间。
type cloudSyncResult struct {
	CostSynced, InstancesSynced, TrafficSynced bool
	Err                                        string
	AuthFailed                                 bool
	NextTryAt                                  int64
}

// RecordCloudSync 写入同步状态。
func (s *Store) RecordCloudSync(id int64, r cloudSyncResult, now time.Time) error {
	t := now.Unix()
	set := []string{}
	args := []any{}
	if r.CostSynced {
		set, args = append(set, "cost_synced_at = ?"), append(args, t)
	}
	if r.InstancesSynced {
		set, args = append(set, "instances_synced_at = ?"), append(args, t)
	}
	if r.TrafficSynced {
		set, args = append(set, "traffic_synced_at = ?"), append(args, t)
	}
	if r.Err == "" {
		set = append(set, "last_error = ''", "error_since = 0", "fail_count = 0", "next_try_at = 0", "auth_failed = 0")
	} else {
		set = append(set, "last_error = ?", "error_since = CASE WHEN error_since = 0 THEN ? ELSE error_since END",
			"fail_count = fail_count + 1", "next_try_at = ?", "auth_failed = ?")
		args = append(args, r.Err, t, r.NextTryAt, r.AuthFailed)
	}
	args = append(args, id)
	_, err := s.DB.Exec(`UPDATE cloud_accounts SET `+strings.Join(set, ", ")+` WHERE id = ?`, args...)
	return err
}

// CloudCost 是一行 cloud_costs。
type CloudCost struct {
	Period        string `json:"period"`
	AmountCents   int64  `json:"amount_cents"`
	ForecastCents *int64 `json:"forecast_cents"`
	BalanceCents  *int64 `json:"balance_cents"`
	Currency      string `json:"currency"`
	UpdatedAt     int64  `json:"updated_at"`
}

func (s *Store) SaveCloudCost(accountID int64, c cloud.Costs, now time.Time) error {
	_, err := s.DB.Exec(`INSERT INTO cloud_costs (account_id, period, amount_cents, forecast_cents, balance_cents, currency, updated_at)
		VALUES (?,?,?,?,?,?,?) ON CONFLICT (account_id, period) DO UPDATE SET amount_cents = excluded.amount_cents,
		forecast_cents = excluded.forecast_cents, balance_cents = excluded.balance_cents, currency = excluded.currency,
		updated_at = excluded.updated_at`, accountID, c.Period, c.AmountCents, c.ForecastCents, c.BalanceCents, c.Currency, now.Unix())
	return err
}

// CloudCosts 返回账户最近 limit 个月的费用，最新在前。
func (s *Store) CloudCosts(accountID int64, limit int) ([]CloudCost, error) {
	rows, err := s.DB.Query(`SELECT period, amount_cents, forecast_cents, balance_cents, currency, updated_at
		FROM cloud_costs WHERE account_id = ? ORDER BY period DESC LIMIT ?`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CloudCost{}
	for rows.Next() {
		var c CloudCost
		if err := rows.Scan(&c.Period, &c.AmountCents, &c.ForecastCents, &c.BalanceCents, &c.Currency, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CloudInstance 是一行 cloud_instances。
type CloudInstance struct {
	ID                 int64  `json:"id"`
	AccountID          int64  `json:"account_id"`
	AccountName        string `json:"account_name"`
	Provider           string `json:"provider"`
	InstanceID         string `json:"instance_id"`
	Name               string `json:"name"`
	Region             string `json:"region"`
	Kind               string `json:"kind"`
	State              string `json:"state"`
	PublicIPv4         string `json:"public_ipv4"`
	PublicIPv6         string `json:"public_ipv6"`
	Plan               string `json:"plan"`
	ExpireAt           int64  `json:"expire_at"`
	RenewPriceCents    int64  `json:"renew_price_cents"`
	TrafficLimitBytes  int64  `json:"traffic_limit_bytes"`
	TrafficUsedBytes   int64  `json:"traffic_used_bytes"`
	TrafficPeriodStart string `json:"traffic_period_start"`
	ServerID           *int64 `json:"server_id"`
	UpdatedAt          int64  `json:"updated_at"`
}

// ReplaceCloudInstances 用最新列表替换账户的实例：新增、更新，已不存在的删除；保留已有的节点关联与流量数据。
func (s *Store) ReplaceCloudInstances(accountID int64, insts []cloud.Instance, now time.Time) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	keep := make([]any, 0, len(insts)+1)
	keep = append(keep, accountID)
	for _, in := range insts {
		if _, err := tx.Exec(`INSERT INTO cloud_instances (account_id, instance_id, name, region, kind, state, public_ipv4,
			public_ipv6, plan, expire_at, traffic_limit_bytes, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT (account_id, instance_id) DO UPDATE SET name = excluded.name, region = excluded.region,
			kind = excluded.kind, state = excluded.state, public_ipv4 = excluded.public_ipv4, public_ipv6 = excluded.public_ipv6,
			plan = excluded.plan, expire_at = excluded.expire_at, traffic_limit_bytes = excluded.traffic_limit_bytes,
			updated_at = excluded.updated_at`, accountID, in.ID, in.Name, in.Region, in.Kind, in.State, in.IPv4, in.IPv6,
			in.Plan, in.ExpireAt, int64(in.TrafficLimit), now.Unix()); err != nil {
			return err
		}
		keep = append(keep, in.ID)
	}
	q := `DELETE FROM cloud_instances WHERE account_id = ?`
	if len(keep) > 1 {
		q += ` AND instance_id NOT IN (?` + strings.Repeat(",?", len(keep)-2) + `)`
	}
	if _, err := tx.Exec(q, keep...); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveCloudTraffic 写入实例的本周期流量。
func (s *Store) SaveCloudTraffic(accountID int64, insts []cloud.Instance, now time.Time) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, in := range insts {
		if in.TrafficPeriodStart == "" {
			continue
		}
		if _, err := tx.Exec(`UPDATE cloud_instances SET traffic_used_bytes = ?, traffic_period_start = ?, updated_at = ?
			WHERE account_id = ? AND instance_id = ?`, int64(in.TrafficUsed), in.TrafficPeriodStart, now.Unix(),
			accountID, in.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CloudInstances 列出实例；accountID 为 0 表示全部账户。
func (s *Store) CloudInstances(accountID int64) ([]CloudInstance, error) {
	rows, err := s.DB.Query(`SELECT i.id, i.account_id, a.name, a.provider, i.instance_id, i.name, i.region, i.kind, i.state,
		i.public_ipv4, i.public_ipv6, i.plan, i.expire_at, i.renew_price_cents, i.traffic_limit_bytes, i.traffic_used_bytes,
		i.traffic_period_start, i.server_id, i.updated_at
		FROM cloud_instances i JOIN cloud_accounts a ON a.id = i.account_id
		WHERE ? = 0 OR i.account_id = ? ORDER BY a.name, i.kind, i.region, i.name, i.instance_id`, accountID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CloudInstance{}
	for rows.Next() {
		var c CloudInstance
		if err := rows.Scan(&c.ID, &c.AccountID, &c.AccountName, &c.Provider, &c.InstanceID, &c.Name, &c.Region, &c.Kind,
			&c.State, &c.PublicIPv4, &c.PublicIPv6, &c.Plan, &c.ExpireAt, &c.RenewPriceCents, &c.TrafficLimitBytes,
			&c.TrafficUsedBytes, &c.TrafficPeriodStart, &c.ServerID, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// cloudInstanceCounts 返回各账户的实例数。
func (s *Store) cloudInstanceCounts() (map[int64]int, error) {
	rows, err := s.DB.Query(`SELECT account_id, COUNT(*) FROM cloud_instances GROUP BY account_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
