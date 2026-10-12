package server

// App 的云账户摘要（设计 44.8，修订第 144 条）：GET /api/v1/app/cloud，只接受 App 设备凭证。
//
// 【安全】
//   - 只读（约束 5）：App 不能添加、修改或同步云账户，这里只有一个读取接口
//   - 云账户覆盖整个面板，不按节点划分：只有授权范围为“全部节点”的设备能看到，其他范围返回 limited
//   - 只返回展示所需的最小字段：不含凭证提示（末 4 位）、公网 IP 与云厂商实例 ID（44.2）

import (
	"net/http"
	"time"

	"vpsmon/internal/cloud"
)

type appCloudCost struct {
	Period        string `json:"period"`
	AmountCents   int64  `json:"amount_cents"`
	ForecastCents *int64 `json:"forecast_cents"`
	BalanceCents  *int64 `json:"balance_cents"`
	Currency      string `json:"currency"`
}

type appCloudAccount struct {
	ID            int64         `json:"id"`
	Provider      string        `json:"provider"`
	Name          string        `json:"name"`
	Enabled       bool          `json:"enabled"`
	Syncing       bool          `json:"syncing"`
	AuthFailed    bool          `json:"auth_failed"`
	LastError     string        `json:"last_error"`
	LastWarning   string        `json:"last_warning"`
	NextTryAt     int64         `json:"next_try_at"`
	SyncedAt      int64         `json:"synced_at"`
	InstanceCount int           `json:"instance_count"`
	BudgetCents   int64         `json:"budget_cents"`
	CurrentCost   *appCloudCost `json:"current_cost"`
}

type appCloudInstance struct {
	ID                int64  `json:"id"`
	AccountID         int64  `json:"account_id"`
	Name              string `json:"name"`
	Region            string `json:"region"`
	Kind              string `json:"kind"`
	State             string `json:"state"`
	Plan              string `json:"plan"`
	ExpireAt          int64  `json:"expire_at"`
	TrafficUsedBytes  int64  `json:"traffic_used_bytes"`
	TrafficLimitBytes int64  `json:"traffic_limit_bytes"`
	ServerID          *int64 `json:"server_id"`
}

type appCloudView struct {
	Limited   bool               `json:"limited"`
	Accounts  []appCloudAccount  `json:"accounts"`
	Instances []appCloudInstance `json:"instances"`
}

// handleAppCloud：GET /api/v1/app/cloud，app。
func (s *Server) handleAppCloud(w http.ResponseWriter, r *http.Request) {
	out := appCloudView{Accounts: []appCloudAccount{}, Instances: []appCloudInstance{}}
	if d := info(r).device; d == nil || d.ScopeType != "all" {
		out.Limited = true
		writeJSON(w, out)
		return
	}
	accounts, err := s.store.ListCloudAccounts()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	counts, err := s.store.cloudInstanceCounts()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	now := time.Now()
	for _, a := range accounts {
		v := appCloudAccount{ID: a.ID, Provider: a.Provider, Name: a.Name, Enabled: a.Enabled, AuthFailed: a.AuthFailed,
			LastError: a.LastError, LastWarning: a.LastWarning, NextTryAt: a.NextTryAt,
			SyncedAt: max(a.CostSyncedAt, a.InstancesSyncedAt, a.TrafficSyncedAt), InstanceCount: counts[a.ID], BudgetCents: a.BudgetCents}
		s.cloud.mu.Lock()
		v.Syncing = s.cloud.running[a.ID]
		s.cloud.mu.Unlock()
		costs, err := s.store.CloudCosts(a.ID, 1)
		if err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		// 与 Web 相同：只显示本月（按服务商的账单时区）的费用
		if len(costs) > 0 && costs[0].Period == cloud.BillingMonth(a.Provider, now) {
			c := costs[0]
			v.CurrentCost = &appCloudCost{Period: c.Period, AmountCents: c.AmountCents, ForecastCents: c.ForecastCents,
				BalanceCents: c.BalanceCents, Currency: c.Currency}
		}
		out.Accounts = append(out.Accounts, v)
	}
	insts, err := s.store.CloudInstances(0)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	for _, in := range insts {
		out.Instances = append(out.Instances, appCloudInstance{ID: in.ID, AccountID: in.AccountID, Name: in.Name, Region: in.Region,
			Kind: in.Kind, State: in.State, Plan: in.Plan, ExpireAt: in.ExpireAt, TrafficUsedBytes: in.TrafficUsedBytes,
			TrafficLimitBytes: in.TrafficLimitBytes, ServerID: in.ServerID})
	}
	writeJSON(w, out)
}
