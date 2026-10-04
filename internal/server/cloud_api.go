package server

// 云账户接口（设计 44.8）。凭证只写不读：响应只有末 4 位提示（credential_hint）。
// 【安全】添加账户、更换凭证、删除账户需在 10 分钟内重新验证过密码（设计 17.4、44.2）；全部记入操作日志，
// 日志中不含凭证。

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"vpsmon/internal/cloud"
)

var (
	awsKeyID        = regexp.MustCompile(`^AKIA[A-Z0-9]{16}$`)
	aliyunKeyID     = regexp.MustCompile(`^LTAI[0-9A-Za-z]{12,28}$`)
	tencentSecretID = regexp.MustCompile(`^AKID[0-9A-Za-z]{20,40}$`)
	cloudRegion     = regexp.MustCompile(`^[a-z]{2}(-[a-z0-9]+){1,3}$`) // ap-northeast-1、cn-hangzhou
	costIntervals   = []int{6, 12, 24}
)

// cloudAccountView 是 GET /cloud-accounts 的一项（api/openapi.yaml CloudAccount）。
type cloudAccountView struct {
	ID                int64      `json:"id"`
	Provider          string     `json:"provider"`
	Name              string     `json:"name"`
	Regions           []string   `json:"regions"`
	CredentialHint    string     `json:"credential_hint"`
	BudgetCents       int64      `json:"budget_cents"`
	CostIntervalH     int        `json:"cost_interval_h"`
	Enabled           bool       `json:"enabled"`
	SyncCost          bool       `json:"sync_cost"`
	SyncTraffic       bool       `json:"sync_traffic"`
	CostSyncedAt      int64      `json:"cost_synced_at"`
	InstancesSyncedAt int64      `json:"instances_synced_at"`
	TrafficSyncedAt   int64      `json:"traffic_synced_at"`
	LastError         string     `json:"last_error"`
	ErrorSince        int64      `json:"error_since"`
	AuthFailed        bool       `json:"auth_failed"`
	NextTryAt         int64      `json:"next_try_at"`
	Syncing           bool       `json:"syncing"`
	InstanceCount     int        `json:"instance_count"`
	CurrentCost       *CloudCost `json:"current_cost"`
	CreatedAt         int64      `json:"created_at"`
	UpdatedAt         int64      `json:"updated_at"`
}

func (s *Server) cloudAccountView(a CloudAccount, count int) (cloudAccountView, error) {
	v := cloudAccountView{ID: a.ID, Provider: a.Provider, Name: a.Name, Regions: a.Regions, CredentialHint: a.CredentialHint,
		BudgetCents: a.BudgetCents, CostIntervalH: a.CostIntervalH, Enabled: a.Enabled, SyncCost: a.SyncCost,
		SyncTraffic: a.SyncTraffic, CostSyncedAt: a.CostSyncedAt, InstancesSyncedAt: a.InstancesSyncedAt,
		TrafficSyncedAt: a.TrafficSyncedAt, LastError: a.LastError, ErrorSince: a.ErrorSince, AuthFailed: a.AuthFailed,
		NextTryAt: a.NextTryAt, InstanceCount: count, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
	if v.Regions == nil {
		v.Regions = []string{}
	}
	s.cloud.mu.Lock()
	v.Syncing = s.cloud.running[a.ID]
	s.cloud.mu.Unlock()
	costs, err := s.store.CloudCosts(a.ID, 1)
	if err != nil {
		return v, err
	}
	// 只显示本月（按服务商的账单时区）的费用：上月的数据不能当作“本月”
	if len(costs) > 0 && costs[0].Period == cloud.BillingMonth(a.Provider, time.Now()) {
		v.CurrentCost = &costs[0]
	}
	return v, nil
}

// cloudAccountBody 是添加 / 修改的请求体（api/openapi.yaml CloudAccountInput）。指针字段为 nil 表示未提供。
type cloudAccountBody struct {
	Provider      string            `json:"provider"`
	Name          string            `json:"name"`
	Regions       []string          `json:"regions"`
	Credential    map[string]string `json:"credential"`
	Budget        *float64          `json:"budget"`
	CostIntervalH *int              `json:"cost_interval_h"`
	Enabled       *bool             `json:"enabled"`
	SyncCost      *bool             `json:"sync_cost"`
	SyncTraffic   *bool             `json:"sync_traffic"`
}

func decodeCloudAccount(w http.ResponseWriter, r *http.Request) (cloudAccountBody, error) {
	var b cloudAccountBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return b, &APIError{Code: CodeBadRequest, Cause: err}
	}
	return b, nil
}

// apply 把请求体写入账户并校验（不含凭证）。
func (b cloudAccountBody) apply(a *CloudAccount) []FieldError {
	var fe []FieldError
	a.Name = strings.TrimSpace(b.Name)
	if a.Name == "" || utf8.RuneCountInString(a.Name) > 64 {
		fe = append(fe, FieldError{Field: "name", Message: "名称为 1～64 个字符"})
	}
	if b.Regions != nil {
		regions := []string{}
		for _, r := range b.Regions {
			r = strings.TrimSpace(strings.ToLower(r))
			if r == "" || slices.Contains(regions, r) {
				continue
			}
			if !cloudRegion.MatchString(r) {
				fe = append(fe, FieldError{Field: "regions", Message: "区域格式不正确：" + r + "（如 ap-northeast-1）"})
				break
			}
			regions = append(regions, r)
		}
		if len(regions) > 40 {
			fe = append(fe, FieldError{Field: "regions", Message: "最多 40 个区域"})
		}
		a.Regions = regions
	}
	if b.Budget != nil {
		if *b.Budget < 0 || *b.Budget > 1e9 || math.IsNaN(*b.Budget) {
			fe = append(fe, FieldError{Field: "budget", Message: "预算应为不小于 0 的金额"})
		} else {
			a.BudgetCents = int64(math.Round(*b.Budget * 100))
		}
	}
	if b.CostIntervalH != nil {
		if !slices.Contains(costIntervals, *b.CostIntervalH) {
			fe = append(fe, FieldError{Field: "cost_interval_h", Message: "费用同步间隔只能是 6、12 或 24 小时"})
		} else {
			a.CostIntervalH = *b.CostIntervalH
		}
	}
	if b.Enabled != nil {
		a.Enabled = *b.Enabled
	}
	if b.SyncCost != nil {
		a.SyncCost = *b.SyncCost
	}
	if b.SyncTraffic != nil {
		a.SyncTraffic = *b.SyncTraffic
	}
	return fe
}

// handleCloudAccounts：GET /api/v1/cloud-accounts，admin。
func (s *Server) handleCloudAccounts(w http.ResponseWriter, r *http.Request) {
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
	items := make([]cloudAccountView, 0, len(accounts))
	for _, a := range accounts {
		v, err := s.cloudAccountView(a, counts[a.ID])
		if err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		items = append(items, v)
	}
	writeList(w, items, "", nil)
}

// handleCreateCloudAccount：POST /api/v1/cloud-accounts，admin + 重新验证。
func (s *Server) handleCreateCloudAccount(w http.ResponseWriter, r *http.Request) {
	if err := requireReauth(r); err != nil {
		s.writeError(w, r, err)
		return
	}
	b, err := decodeCloudAccount(w, r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	a := CloudAccount{Provider: b.Provider, Enabled: true, SyncCost: true, SyncTraffic: true, CostIntervalH: 12}
	fe := b.apply(&a)
	if !slices.Contains(cloud.Providers, a.Provider) {
		fe = append(fe, FieldError{Field: "provider", Message: "不支持的服务商"})
	}
	if len(b.Credential) == 0 {
		fe = append(fe, FieldError{Field: "credential", Message: "请填写凭证"})
	}
	if len(fe) == 0 {
		var cfe []FieldError
		a.CredentialEnc, a.CredentialHint, cfe, err = s.sealCredential(a.Provider, b.Credential)
		if err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		fe = append(fe, cfe...)
	}
	if len(fe) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	if err := s.store.SaveCloudAccount(&a, time.Now()); err != nil {
		s.writeCloudSaveError(w, r, err)
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "cloud_account.create", TargetType: "cloud_account", TargetID: a.ID,
		Success: true, Details: map[string]any{"provider": a.Provider, "name": a.Name, "credential_hint": a.CredentialHint}})
	s.triggerCloudSync(a.ID)
	v, err := s.cloudAccountView(a, 0)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSONStatus(w, http.StatusCreated, v)
}

// handleUpdateCloudAccount：PUT /api/v1/cloud-accounts/{id}，admin；更换凭证时需重新验证。
func (s *Server) handleUpdateCloudAccount(w http.ResponseWriter, r *http.Request) {
	id, err := cloudPathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	a, err := s.store.GetCloudAccount(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	b, err := decodeCloudAccount(w, r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	newCred := len(b.Credential) > 0
	if newCred {
		if err := requireReauth(r); err != nil {
			s.writeError(w, r, err)
			return
		}
	}
	fe := b.apply(&a)
	if b.Provider != "" && b.Provider != a.Provider {
		fe = append(fe, FieldError{Field: "provider", Message: "服务商不可修改，请添加新账户"})
	}
	if newCred && len(fe) == 0 {
		var cfe []FieldError
		a.CredentialEnc, a.CredentialHint, cfe, err = s.sealCredential(a.Provider, b.Credential)
		if err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		fe = append(fe, cfe...)
		// 新凭证：清除“凭证失效”与退避，立即重新同步
		a.AuthFailed, a.FailCount, a.NextTryAt, a.LastError, a.ErrorSince = false, 0, 0, "", 0
	}
	if len(fe) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	if err := s.store.SaveCloudAccount(&a, time.Now()); err != nil {
		s.writeCloudSaveError(w, r, err)
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "cloud_account.update", TargetType: "cloud_account", TargetID: a.ID,
		Success: true, Details: map[string]any{"name": a.Name, "enabled": a.Enabled, "credential_changed": newCred,
			"credential_hint": a.CredentialHint}})
	if newCred {
		s.triggerCloudSync(a.ID)
	}
	counts, _ := s.store.cloudInstanceCounts()
	v, err := s.cloudAccountView(a, counts[a.ID])
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, v)
}

func (s *Server) writeCloudSaveError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errNameTaken) {
		s.writeError(w, r, &APIError{Code: CodeConflict, Message: "名称已被使用",
			Details: []FieldError{{Field: "name", Message: "名称已被使用"}}})
		return
	}
	s.writeError(w, r, internalError(err))
}

// handleDeleteCloudAccount：DELETE /api/v1/cloud-accounts/{id}，admin + 重新验证。同步的费用与实例一并删除。
func (s *Server) handleDeleteCloudAccount(w http.ResponseWriter, r *http.Request) {
	if err := requireReauth(r); err != nil {
		s.writeError(w, r, err)
		return
	}
	id, err := cloudPathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	a, err := s.store.GetCloudAccount(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.store.DeleteCloudAccount(id); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "cloud_account.delete", TargetType: "cloud_account", TargetID: id,
		Success: true, Details: map[string]any{"provider": a.Provider, "name": a.Name}})
	w.WriteHeader(http.StatusNoContent)
}

// handleSyncCloudAccount：POST /api/v1/cloud-accounts/{id}/sync，admin。后台同步，立即返回 202。
func (s *Server) handleSyncCloudAccount(w http.ResponseWriter, r *http.Request) {
	id, err := cloudPathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	a, err := s.store.GetCloudAccount(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	now := time.Now()
	s.cloud.mu.Lock()
	last := s.cloud.lastManual[id]
	if wait := time.Minute - now.Sub(last); wait > 0 {
		s.cloud.mu.Unlock()
		s.writeError(w, r, &APIError{Code: CodeRateLimited, Message: "刚同步过，请稍后再试", RetryAfter: wait})
		return
	}
	s.cloud.lastManual[id] = now
	s.cloud.mu.Unlock()
	s.audit(r, AuditEntry{ActorType: "admin", Action: "cloud_account.sync", TargetType: "cloud_account", TargetID: id,
		Success: true, Details: map[string]any{"name": a.Name}})
	s.triggerCloudSync(id)
	w.WriteHeader(http.StatusAccepted)
}

// triggerCloudSync 请求后台立即同步；队列满时忽略（定时同步会补上）。
func (s *Server) triggerCloudSync(id int64) {
	select {
	case s.cloud.trigger <- id:
	default:
	}
}

// handleCloudCosts：GET /api/v1/cloud-accounts/{id}/costs，admin。
func (s *Server) handleCloudCosts(w http.ResponseWriter, r *http.Request) {
	id, err := cloudPathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if _, err := s.store.GetCloudAccount(id); err != nil {
		s.writeError(w, r, err)
		return
	}
	costs, err := s.store.CloudCosts(id, 24)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeList(w, costs, "", nil)
}

// handleCloudInstances：GET /api/v1/cloud-instances，admin。
func (s *Server) handleCloudInstances(w http.ResponseWriter, r *http.Request) {
	accountID, serverID, err := cloudInstanceQuery(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	insts, err := s.store.CloudInstances(accountID)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	rows, err := s.store.ListServers()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	suggestLinks(insts, rows)
	if serverID > 0 { // 节点详情：只要关联到该节点的实例
		insts = slices.DeleteFunc(insts, func(in CloudInstance) bool { return in.ServerID == nil || *in.ServerID != serverID })
	}
	writeList(w, insts, "", nil)
}

func cloudPathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errorf(CodeBadRequest, "账户编号格式不正确")
	}
	return id, nil
}
