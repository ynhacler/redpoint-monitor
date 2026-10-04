package server

// 云账户同步（设计 44.4）：面板进程内的后台任务（runTask，崩溃后重启，不影响上报）。
//
//	实例  每 6 小时        流量  每小时（只查有流量包的实例）
//	费用  按账户设置 6 / 12 / 24 小时（AWS Cost Explorer 每次调用 0.01 美元，每次同步 2 次调用）
//	失败  退避重试：1 分钟起，每次翻倍，最长 6 小时
//	凭证失效 / 权限不足（401 / 403 等）：停止自动同步，账户上显示错误，更新凭证后恢复
//	手动同步  忽略间隔与退避，立即同步全部数据；同一账户 1 分钟内只允许一次（cloud_api.go）
//
// 【安全】凭证只在同步时解密到内存，用完即弃；错误信息只含云厂商的错误码与说明（设计 44.2）。

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"time"

	"vpsmon/internal/cloud"
)

const (
	cloudInstancesEvery = 6 * time.Hour
	cloudTrafficEvery   = time.Hour
	cloudBackoffMax     = 6 * time.Hour
	cloudSyncTimeout    = 5 * time.Minute
)

// cloudState 是云同步的运行状态。
type cloudState struct {
	opts    cloud.Options // 测试中替换接口地址
	trigger chan int64    // 手动同步

	mu         sync.Mutex
	sealer     *cloud.Sealer
	lastManual map[int64]time.Time
	running    map[int64]bool
}

func newCloudState() *cloudState {
	return &cloudState{trigger: make(chan int64, 16), lastManual: map[int64]time.Time{}, running: map[int64]bool{}}
}

// cloudSealer 加载凭证密钥；create 为真时（添加 / 修改凭证）不存在则生成。
func (s *Server) cloudSealer(create bool) (*cloud.Sealer, error) {
	s.cloud.mu.Lock()
	defer s.cloud.mu.Unlock()
	if s.cloud.sealer != nil {
		return s.cloud.sealer, nil
	}
	sl, err := cloud.LoadSealer(filepath.Join(s.store.Dir, "secret.key"), create)
	if err != nil {
		return nil, err
	}
	s.cloud.sealer = sl
	return sl, nil
}

// credPurpose 是凭证密文的附加数据：同一份密文不能被当作另一家服务商的凭证使用。
func credPurpose(provider string) string { return "cloud-credential:" + provider }

// cloudLoop 每分钟检查一次到期的同步，并处理手动同步请求。
func (s *Server) cloudLoop(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	s.cloudSyncDue(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.cloudSyncDue(ctx, time.Now())
		case id := <-s.cloud.trigger:
			a, err := s.store.GetCloudAccount(id)
			if err == nil {
				s.syncCloudAccount(ctx, a, cloudKinds{cost: a.SyncCost, instances: true, traffic: a.SyncTraffic}, time.Now())
			}
		}
	}
}

type cloudKinds struct{ cost, instances, traffic bool }

func (k cloudKinds) any() bool { return k.cost || k.instances || k.traffic }

// dueKinds 判断账户此刻需要同步的数据。停用、凭证失效、退避中的账户不同步。
func dueKinds(a CloudAccount, now time.Time) cloudKinds {
	t := now.Unix()
	if !a.Enabled || a.AuthFailed || t < a.NextTryAt {
		return cloudKinds{}
	}
	interval := time.Duration(a.CostIntervalH) * time.Hour
	if interval <= 0 {
		interval = 12 * time.Hour
	}
	return cloudKinds{
		cost:      a.SyncCost && t-a.CostSyncedAt >= int64(interval/time.Second),
		instances: t-a.InstancesSyncedAt >= int64(cloudInstancesEvery/time.Second),
		traffic:   a.SyncTraffic && t-a.TrafficSyncedAt >= int64(cloudTrafficEvery/time.Second),
	}
}

func (s *Server) cloudSyncDue(ctx context.Context, now time.Time) {
	accounts, err := s.store.ListCloudAccounts()
	if err != nil {
		s.log.Error("list cloud accounts failed", "component", "cloud", "err", err)
		return
	}
	for _, a := range accounts {
		if ctx.Err() != nil {
			return
		}
		if k := dueKinds(a, now); k.any() {
			s.syncCloudAccount(ctx, a, k, now)
		}
	}
}

// cloudBackoff 返回第 n 次（从 0 开始）连续失败后的等待时间。
func cloudBackoff(n int) time.Duration {
	d := time.Minute
	for i := 0; i < n && d < cloudBackoffMax; i++ {
		d *= 2
	}
	return min(d, cloudBackoffMax)
}

// syncCloudAccount 同步一个账户的指定数据并记录结果。顺序：实例 → 流量 → 费用；任何一步失败即停止。
func (s *Server) syncCloudAccount(ctx context.Context, a CloudAccount, k cloudKinds, now time.Time) {
	s.cloud.mu.Lock()
	if s.cloud.running[a.ID] {
		s.cloud.mu.Unlock()
		return
	}
	s.cloud.running[a.ID] = true
	s.cloud.mu.Unlock()
	defer func() {
		s.cloud.mu.Lock()
		delete(s.cloud.running, a.ID)
		s.cloud.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(ctx, cloudSyncTimeout)
	defer cancel()
	res := cloudSyncResult{}
	err := s.doCloudSync(ctx, a, k, now, &res)
	if err != nil {
		res.Err = err.Error()
		res.AuthFailed = cloud.IsAuthError(err) || isCredentialUnusable(err)
		res.NextTryAt = now.Add(cloudBackoff(a.FailCount)).Unix()
		s.log.Warn("cloud sync failed", "component", "cloud", "account_id", a.ID, "provider", a.Provider,
			"auth_failed", res.AuthFailed, "fail_count", a.FailCount+1, "err", res.Err)
	} else {
		s.log.Info("cloud sync done", "component", "cloud", "account_id", a.ID, "provider", a.Provider,
			"cost", res.CostSynced, "instances", res.InstancesSynced, "traffic", res.TrafficSynced)
	}
	if err := s.store.RecordCloudSync(a.ID, res, now); err != nil {
		s.log.Error("record cloud sync failed", "component", "cloud", "account_id", a.ID, "err", err)
	}
}

// errCredentialUnusable 表示凭证无法解密或格式不对：重试没有意义，与凭证失效一样停止自动同步。
type errCredentialUnusable struct{ err error }

func (e errCredentialUnusable) Error() string { return e.err.Error() }

func isCredentialUnusable(err error) bool {
	_, ok := err.(errCredentialUnusable)
	return ok
}

func (s *Server) cloudClient(a CloudAccount) (cloud.Client, error) {
	sl, err := s.cloudSealer(false)
	if err != nil {
		return nil, errCredentialUnusable{err}
	}
	cred, err := sl.Open(a.CredentialEnc, credPurpose(a.Provider))
	if err != nil {
		return nil, errCredentialUnusable{err}
	}
	c, err := cloud.NewClient(a.Provider, cred, a.Regions, s.cloud.opts)
	clear(cred)
	if err != nil {
		return nil, errCredentialUnusable{err}
	}
	return c, nil
}

func (s *Server) doCloudSync(ctx context.Context, a CloudAccount, k cloudKinds, now time.Time, res *cloudSyncResult) error {
	c, err := s.cloudClient(a)
	if err != nil {
		return err
	}
	if k.instances {
		insts, err := c.Instances(ctx)
		if err != nil {
			return err
		}
		if err := s.store.ReplaceCloudInstances(a.ID, insts, now); err != nil {
			return err
		}
		res.InstancesSynced = true
	}
	if k.traffic {
		rows, err := s.store.CloudInstances(a.ID)
		if err != nil {
			return err
		}
		// 传入全部实例，由各家客户端挑选有流量包的（阿里云轻量的额度只能从流量接口得到）
		insts := make([]cloud.Instance, 0, len(rows))
		for _, r := range rows {
			insts = append(insts, cloud.Instance{ID: r.InstanceID, Name: r.Name, Region: r.Region, Kind: r.Kind,
				TrafficLimit: uint64(r.TrafficLimitBytes)})
		}
		if len(insts) > 0 {
			if err := c.Traffic(ctx, insts, now); err != nil {
				return err
			}
			if err := s.store.SaveCloudTraffic(a.ID, insts, now); err != nil {
				return err
			}
		}
		res.TrafficSynced = true
	}
	if k.cost {
		costs, err := c.Costs(ctx, now)
		if err != nil {
			return err
		}
		if err := s.store.SaveCloudCost(a.ID, *costs, now); err != nil {
			return err
		}
		res.CostSynced = true
	}
	return nil
}

// sealCredential 校验并加密凭证，返回密文与末 4 位提示。
func (s *Server) sealCredential(provider string, cred map[string]string) ([]byte, string, []FieldError, error) {
	var fe []FieldError
	var hint string
	switch provider {
	case cloud.ProviderAliyunCN, cloud.ProviderAliyunIntl:
		id, secret := cred["access_key_id"], cred["access_key_secret"]
		if !aliyunKeyID.MatchString(id) {
			fe = append(fe, FieldError{Field: "credential.access_key_id", Message: "AccessKey ID 格式不正确（如 LTAI…）"})
		}
		if len(secret) < 20 || len(secret) > 64 {
			fe = append(fe, FieldError{Field: "credential.access_key_secret", Message: "AccessKey Secret 格式不正确"})
		}
		if len(fe) == 0 {
			hint = id[:4] + "…" + id[len(id)-4:]
		}
		cred = map[string]string{"access_key_id": id, "access_key_secret": secret}
	case cloud.ProviderAWS:
		id, secret := cred["access_key_id"], cred["secret_access_key"]
		if !awsKeyID.MatchString(id) {
			fe = append(fe, FieldError{Field: "credential.access_key_id", Message: "Access Key ID 格式不正确（如 AKIA…，20 位）"})
		}
		if len(secret) < 30 || len(secret) > 128 {
			fe = append(fe, FieldError{Field: "credential.secret_access_key", Message: "Secret Access Key 格式不正确"})
		}
		if len(fe) == 0 {
			hint = id[:4] + "…" + id[len(id)-4:]
		}
		cred = map[string]string{"access_key_id": id, "secret_access_key": secret}
	}
	if len(fe) > 0 {
		return nil, "", fe, nil
	}
	plain, err := json.Marshal(cred)
	if err != nil {
		return nil, "", nil, err
	}
	defer clear(plain)
	sl, err := s.cloudSealer(true)
	if err != nil {
		return nil, "", nil, err
	}
	enc, err := sl.Seal(plain, credPurpose(provider))
	return enc, hint, nil, err
}
