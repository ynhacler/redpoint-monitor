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
	ID                int64
	Provider          string
	Name              string
	Regions           []string
	CredentialEnc     []byte
	CredentialHint    string
	BudgetCents       int64
	BalanceAlertCents int64 // 余额低于此值时提醒（设计 44.6）；0 表示不提醒
	CostIntervalH     int
	Enabled           bool
	SyncCost          bool
	SyncTraffic       bool
	// 同步状态
	CostSyncedAt       int64
	InstancesSyncedAt  int64
	TrafficSyncedAt    int64
	LastError          string
	LastWarning        string // 最近一次同步跳过的区域及原因（修订第 141 条）；空表示没有
	InstancesRecheckAt int64  // 提前重新检查实例的时间（修订第 151 条）；0 表示按常规周期
	ErrorSince         int64
	FailCount          int
	NextTryAt          int64
	AuthFailed         bool
	CreatedAt          int64
	UpdatedAt          int64
}

var errNoCloudAccount = errorf(CodeNotFound, "云账户不存在或已删除")

const cloudAccountCols = `id, provider, name, regions, credential_enc, credential_hint, budget_cents, balance_alert_cents, cost_interval_h,
	enabled, sync_cost, sync_traffic, cost_synced_at, instances_synced_at, traffic_synced_at, last_error, error_since,
	fail_count, next_try_at, auth_failed, created_at, updated_at, last_warning, instances_recheck_at`

func scanCloudAccount(sc interface{ Scan(...any) error }) (CloudAccount, error) {
	var a CloudAccount
	var regions string
	err := sc.Scan(&a.ID, &a.Provider, &a.Name, &regions, &a.CredentialEnc, &a.CredentialHint, &a.BudgetCents,
		&a.BalanceAlertCents, &a.CostIntervalH, &a.Enabled, &a.SyncCost, &a.SyncTraffic, &a.CostSyncedAt, &a.InstancesSyncedAt,
		&a.TrafficSyncedAt, &a.LastError, &a.ErrorSince, &a.FailCount, &a.NextTryAt, &a.AuthFailed, &a.CreatedAt, &a.UpdatedAt, &a.LastWarning, &a.InstancesRecheckAt)
	if regions != "" {
		a.Regions = strings.Split(regions, ",")
	}
	return a, err
}

// ListCloudAccounts 返回全部云账户（含加密的凭证，不对外返回）。
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

// GetCloudAccount 按 ID 读取云账户。
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
			budget_cents, balance_alert_cents, cost_interval_h, enabled, sync_cost, sync_traffic, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.Provider, a.Name, regions, a.CredentialEnc, a.CredentialHint,
			a.BudgetCents, a.BalanceAlertCents, a.CostIntervalH, a.Enabled, a.SyncCost, a.SyncTraffic, a.CreatedAt, a.UpdatedAt)
		if err == nil {
			a.ID, _ = res.LastInsertId()
		}
	} else {
		// 凭证更换后清除“凭证失效”与退避，立即重新同步
		_, err = s.DB.Exec(`UPDATE cloud_accounts SET name = ?, regions = ?, credential_enc = ?, credential_hint = ?,
			budget_cents = ?, balance_alert_cents = ?, cost_interval_h = ?, enabled = ?, sync_cost = ?, sync_traffic = ?, updated_at = ?,
			auth_failed = ?, fail_count = ?, next_try_at = ?, last_error = ?, error_since = ? WHERE id = ?`,
			a.Name, regions, a.CredentialEnc, a.CredentialHint, a.BudgetCents, a.BalanceAlertCents, a.CostIntervalH, a.Enabled,
			a.SyncCost, a.SyncTraffic, a.UpdatedAt, a.AuthFailed, a.FailCount, a.NextTryAt, a.LastError, a.ErrorSince, a.ID)
	}
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return errNameTaken
	}
	return err
}

// DeleteCloudAccount 删除云账户；不存在时返回 errNoCloudAccount。
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
	CostWarning                                string // 费用因权限不足未读取时的提示（只在 CostSynced 时写入，修订第 184 条）
	Err                                        string
	Warning                                    string // 实例同步时跳过的区域（只在 InstancesSynced 时写入）
	RecheckAt                                  int64  // 提前重新检查实例的时间；0 表示按常规周期（只在 InstancesSynced 时写入）
	AuthFailed                                 bool
	NextTryAt                                  int64
}

// RecordCloudSync 写入同步状态。
func (s *Store) RecordCloudSync(id int64, r cloudSyncResult, now time.Time) error {
	t := now.Unix()
	set := []string{}
	args := []any{}
	if r.CostSynced {
		set, args = append(set, "cost_synced_at = ?", "cost_warning = ?"), append(args, t, r.CostWarning)
	}
	if r.InstancesSynced {
		set, args = append(set, "instances_synced_at = ?", "last_warning = ?", "instances_recheck_at = ?"), append(args, t, r.Warning, r.RecheckAt)
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

// SaveCloudCost 保存（覆盖）一个账户某月的费用。
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
	CDTPool            string `json:"cdt_pool"` // 阿里云 ECS 走 CDT 时所属的免费额度池（账号内共用，修订第 186 条）；空表示不走 CDT
	ServerID           *int64 `json:"server_id"`
	UpdatedAt          int64  `json:"updated_at"`
	// 自动流量校准（设计 44.5）：开关、上次执行时间与结果说明
	AutoCalibrate   bool   `json:"auto_calibrate"`
	CalibratedAt    int64  `json:"calibrated_at"`
	CalibrateStatus string `json:"calibrate_status"`
	// SuggestedServerID：未关联时，公网 IP 与之唯一匹配的节点（建议关联，设计 44.5）；不入库
	SuggestedServerID *int64 `json:"suggested_server_id"`
}

// ReplaceCloudInstances 用最新列表替换账户的实例：新增、更新，已不存在的删除；保留已有的节点关联与流量数据。
func (s *Store) ReplaceCloudInstances(accountID int64, insts []cloud.Instance, now time.Time) error {
	return s.ReplaceCloudInstancesExcept(accountID, insts, nil, now)
}

// ReplaceCloudInstancesExcept 与 ReplaceCloudInstances 相同，但保留 skipped 中（类型, 区域）原有的实例：
// 这些区域本轮没有查到（修订第 141 条），不能当成实例已删除。
func (s *Store) ReplaceCloudInstancesExcept(accountID int64, insts []cloud.Instance, skipped []cloud.RegionError, now time.Time) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	keep := make([]any, 0, len(insts)+1)
	keep = append(keep, accountID)
	for _, in := range insts {
		if _, err := tx.Exec(`INSERT INTO cloud_instances (account_id, instance_id, name, region, kind, state, public_ipv4,
			public_ipv6, plan, expire_at, traffic_limit_bytes, cdt_pool, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT (account_id, instance_id) DO UPDATE SET name = excluded.name, region = excluded.region,
			kind = excluded.kind, state = excluded.state, public_ipv4 = excluded.public_ipv4, public_ipv6 = excluded.public_ipv6,
			plan = excluded.plan, expire_at = excluded.expire_at, cdt_pool = excluded.cdt_pool,
			-- 实例列表不含流量包额度时（阿里云轻量）保留流量同步得到的值
			traffic_limit_bytes = CASE WHEN excluded.traffic_limit_bytes > 0 THEN excluded.traffic_limit_bytes ELSE cloud_instances.traffic_limit_bytes END,
			updated_at = excluded.updated_at`, accountID, in.ID, in.Name, in.Region, in.Kind, in.State, in.IPv4, in.IPv6,
			in.Plan, in.ExpireAt, int64(in.TrafficLimit), in.CDTPool, now.Unix()); err != nil {
			return err
		}
		keep = append(keep, in.ID)
	}
	q := `DELETE FROM cloud_instances WHERE account_id = ?`
	if len(keep) > 1 {
		q += ` AND instance_id NOT IN (?` + strings.Repeat(",?", len(keep)-2) + `)`
	}
	for _, r := range skipped {
		q += ` AND NOT (kind = ? AND region = ?)`
		keep = append(keep, r.Kind, r.Region)
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
		if _, err := tx.Exec(`UPDATE cloud_instances SET traffic_used_bytes = ?, traffic_limit_bytes = ?, traffic_period_start = ?,
			updated_at = ? WHERE account_id = ? AND instance_id = ?`, int64(in.TrafficUsed), int64(in.TrafficLimit),
			in.TrafficPeriodStart, now.Unix(), accountID, in.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CloudInstances 列出实例；accountID 为 0 表示全部账户。
func (s *Store) CloudInstances(accountID int64) ([]CloudInstance, error) {
	rows, err := s.DB.Query(`SELECT i.id, i.account_id, a.name, a.provider, i.instance_id, i.name, i.region, i.kind, i.state,
		i.public_ipv4, i.public_ipv6, i.plan, i.expire_at, i.renew_price_cents, i.traffic_limit_bytes, i.traffic_used_bytes,
		i.traffic_period_start, i.server_id, i.updated_at, i.auto_calibrate, i.calibrated_at, i.calibrate_status, i.cdt_pool
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
			&c.TrafficUsedBytes, &c.TrafficPeriodStart, &c.ServerID, &c.UpdatedAt, &c.AutoCalibrate, &c.CalibratedAt,
			&c.CalibrateStatus, &c.CDTPool); err != nil {
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

// ---- 账户级流量池（修订第 181 条） ----

// CloudPool 是一行 cloud_traffic_pools（api/openapi.yaml CloudTrafficPool）。
type CloudPool struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	UsedBytes   int64  `json:"used_bytes"`
	LimitBytes  int64  `json:"limit_bytes"`
	PeriodStart string `json:"period_start"`
	UpdatedAt   int64  `json:"updated_at"`
}

// SaveCloudPools 保存一次流量池同步：warning 为空时整体替换为 pools；不为空（读取失败）时保留上次的数据，只记录原因。
func (s *Store) SaveCloudPools(accountID int64, pools []cloud.TrafficPool, warning string, now time.Time) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if warning == "" {
		if _, err := tx.Exec(`DELETE FROM cloud_traffic_pools WHERE account_id = ?`, accountID); err != nil {
			return err
		}
		for _, p := range pools {
			if _, err := tx.Exec(`INSERT INTO cloud_traffic_pools (account_id, pool, name, used_bytes, limit_bytes, period_start, updated_at)
				VALUES (?,?,?,?,?,?,?)`, accountID, p.Key, p.Name, int64(p.UsedBytes), int64(p.LimitBytes), p.PeriodStart, now.Unix()); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`UPDATE cloud_accounts SET pool_warning = ? WHERE id = ?`, warning, accountID); err != nil {
		return err
	}
	return tx.Commit()
}

// CloudCostWarning 返回账户费用因权限不足未读取时的提示（修订第 184 条）。
func (s *Store) CloudCostWarning(accountID int64) (string, error) {
	var w string
	err := s.DB.QueryRow(`SELECT cost_warning FROM cloud_accounts WHERE id = ?`, accountID).Scan(&w)
	return w, err
}

// CloudPools 返回账户的流量池（按 pool 排序）与最近一次读取失败的原因。
func (s *Store) CloudPools(accountID int64) ([]CloudPool, string, error) {
	var warning string
	if err := s.DB.QueryRow(`SELECT pool_warning FROM cloud_accounts WHERE id = ?`, accountID).Scan(&warning); err != nil {
		return nil, "", err
	}
	rows, err := s.DB.Query(`SELECT pool, name, used_bytes, limit_bytes, period_start, updated_at
		FROM cloud_traffic_pools WHERE account_id = ? ORDER BY pool`, accountID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []CloudPool{}
	for rows.Next() {
		var p CloudPool
		if err := rows.Scan(&p.Key, &p.Name, &p.UsedBytes, &p.LimitBytes, &p.PeriodStart, &p.UpdatedAt); err != nil {
			return nil, "", err
		}
		out = append(out, p)
	}
	return out, warning, rows.Err()
}
