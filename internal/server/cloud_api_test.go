package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"vpsmon/internal/cloud"
)

const (
	testAKID   = "AKIAIOSFODNN7EXAMPLE"
	testSecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

// fakeAWSServer 是最小的 AWS 模拟：Cost Explorer、EC2（一个区域、一台实例）、Lightsail（一台带流量包的实例）。
// deny 为真时所有请求返回凭证无效。
// lsDown（可选）为真时只有 Lightsail 的 GetInstances 失败（模拟区域级问题，修订第 141 条）。
// lsDown[0]：Lightsail 返回“令牌无效”（未开通的区域）；lsDown[1]：返回“正在初始化”。
func fakeAWSServer(t *testing.T, deny *atomic.Bool, lsDown ...*atomic.Bool) cloud.Options {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(lsDown) > 0 && lsDown[0].Load() && r.Header.Get("X-Amz-Target") == "Lightsail_20161128.GetInstances" {
			w.WriteHeader(400)
			io.WriteString(w, `{"__type":"UnrecognizedClientException","message":"The security token included in the request is invalid."}`)
			return
		}
		// lsDown[1]：Lightsail 区域正在为账户初始化（暂时失败，修订第 151 条）
		if len(lsDown) > 1 && lsDown[1].Load() && r.Header.Get("X-Amz-Target") == "Lightsail_20161128.GetInstances" {
			w.WriteHeader(400)
			io.WriteString(w, `{"__type":"RegionSetupInProgressException","message":"Region setup in progress."}`)
			return
		}
		if deny.Load() {
			w.WriteHeader(403)
			io.WriteString(w, `{"__type":"UnrecognizedClientException","message":"The security token included in the request is invalid."}`)
			return
		}
		op := r.Header.Get("X-Amz-Target")
		if op == "" {
			op = r.URL.Query().Get("Action")
		}
		switch op {
		case "AWSInsightsIndexService.GetCostAndUsage":
			io.WriteString(w, `{"ResultsByTime":[{"Total":{"UnblendedCost":{"Amount":"3.21","Unit":"USD"}}}]}`)
		case "AWSInsightsIndexService.GetCostForecast":
			io.WriteString(w, `{"Total":{"Amount":"6.79","Unit":"USD"}}`)
		case "DescribeRegions":
			io.WriteString(w, `<R><regionInfo><item><regionName>ap-northeast-1</regionName></item></regionInfo></R>`)
		case "DescribeInstances":
			io.WriteString(w, `<R><reservationSet><item><instancesSet><item><instanceId>i-1</instanceId><instanceType>t3.micro</instanceType>
<instanceState><name>running</name></instanceState><ipAddress>203.0.113.9</ipAddress></item></instancesSet></item></reservationSet></R>`)
		case "Lightsail_20161128.GetRegions":
			io.WriteString(w, `{"regions":[{"name":"ap-northeast-1"}]}`)
		case "Lightsail_20161128.GetInstances":
			io.WriteString(w, `{"instances":[{"name":"ls","bundleId":"micro_3_0","publicIpAddress":"198.51.100.9",
"state":{"name":"running"},"networking":{"monthlyTransfer":{"gbPerMonthAllocated":2048}}}]}`)
		case "Lightsail_20161128.GetInstanceMetricData":
			io.WriteString(w, `{"metricData":[{"sum":1e9}]}`)
		default:
			w.WriteHeader(400)
		}
	}))
	t.Cleanup(srv.Close)
	return cloud.Options{Endpoint: func(service, region string) string { return srv.URL + "/" + service + "/" + region + "/" }}
}

func createCloudAccount(t *testing.T, h http.Handler, admin, body string) (int, cloudAccountView, errorPayload) {
	t.Helper()
	rec := do(h, "POST", "/api/v1/cloud-accounts", admin, []byte(body))
	var v cloudAccountView
	var e errorPayload
	if rec.Code == http.StatusCreated {
		json.Unmarshal(rec.Body.Bytes(), &v)
	} else {
		e = decodeError(t, rec)
	}
	return rec.Code, v, e
}

const awsAccountBody = `{"provider":"aws","name":"主账户","regions":["ap-northeast-1"],"budget":50,
	"credential":{"access_key_id":"` + testAKID + `","secret_access_key":"` + testSecret + `"}}`

func TestCloudAccountCreate(t *testing.T) {
	s, h, logs := testServer(t)
	admin := adminToken(t, s)

	// 【安全】添加账户需要重新验证密码
	u, _ := s.store.UserByName("admin")
	plain, _ := s.store.CreateSession(u.ID, "127.0.0.1", "test", time.Now())
	if code, _, e := createCloudAccount(t, h, plain, awsAccountBody); code != http.StatusForbidden || e.Code != CodeReauthRequired {
		t.Fatalf("未重新验证：%d %s", code, e.Code)
	}

	code, v, e := createCloudAccount(t, h, admin, awsAccountBody)
	if code != http.StatusCreated {
		t.Fatalf("添加失败 %d %+v", code, e)
	}
	if v.CredentialHint != "AKIA…MPLE" || v.BudgetCents != 5000 || v.CostIntervalH != 12 || !v.Enabled || !v.SyncCost ||
		len(v.Regions) != 1 || v.CurrentCost != nil {
		t.Fatalf("账户 %+v", v)
	}
	// 【安全】凭证不回显、不明文入库、不进日志与审计
	list := do(h, "GET", "/api/v1/cloud-accounts", admin, nil).Body.String()
	var enc []byte
	s.store.DB.QueryRow(`SELECT credential_enc FROM cloud_accounts WHERE id = ?`, v.ID).Scan(&enc)
	var audit string
	s.store.DB.QueryRow(`SELECT group_concat(details) FROM audit_logs`).Scan(&audit)
	for name, text := range map[string]string{"列表": list, "数据库": string(enc), "日志": logs.String(), "审计": audit} {
		if strings.Contains(text, testSecret) || strings.Contains(text, testAKID) {
			t.Errorf("【安全】%s中出现了凭证", name)
		}
	}
	fi, err := os.Stat(filepath.Join(s.store.Dir, "secret.key"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("secret.key %v %v", fi, err)
	}
	// 密文能用同一密钥解出原凭证
	sl, _ := s.cloudSealer(false)
	pt, err := sl.Open(enc, credPurpose("aws"))
	if err != nil || !strings.Contains(string(pt), testSecret) {
		t.Fatalf("解密 %v", err)
	}

	// 名称重复
	if code, _, e := createCloudAccount(t, h, admin, awsAccountBody); code != http.StatusConflict || e.Details[0].Field != "name" {
		t.Fatalf("重名 %d %+v", code, e)
	}
}

func TestCloudAccountValidation(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	cred := `"credential":{"access_key_id":"` + testAKID + `","secret_access_key":"` + testSecret + `"}`
	for _, c := range []struct{ body, field string }{
		{`{"provider":"aws","name":"",` + cred + `}`, "name"},
		{`{"provider":"gcp","name":"x",` + cred + `}`, "provider"},
		{`{"provider":"aws","name":"x"}`, "credential"},
		{`{"provider":"aws","name":"x","credential":{"access_key_id":"ASIA0000000000000000","secret_access_key":"` + testSecret + `"}}`, "credential.access_key_id"},
		{`{"provider":"aws","name":"x","credential":{"access_key_id":"` + testAKID + `","secret_access_key":"short"}}`, "credential.secret_access_key"},
		{`{"provider":"aws","name":"x","regions":["Tokyo"],` + cred + `}`, "regions"},
		{`{"provider":"aws","name":"x","cost_interval_h":1,` + cred + `}`, "cost_interval_h"},
		{`{"provider":"aws","name":"x","budget":-1,` + cred + `}`, "budget"},
	} {
		code, _, e := createCloudAccount(t, h, admin, c.body)
		if code != http.StatusUnprocessableEntity || len(e.Details) == 0 || e.Details[0].Field != c.field {
			t.Errorf("%s：%d %+v，期望字段 %s", c.body, code, e, c.field)
		}
	}
	if code, _, _ := createCloudAccount(t, h, admin, `{"provider":"aws","name":"x","unknown":1}`); code != http.StatusBadRequest {
		t.Errorf("未知字段应返回 400，得到 %d", code)
	}
}

func TestCloudSync(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	var deny atomic.Bool
	s.cloud.opts = fakeAWSServer(t, &deny)
	_, v, _ := createCloudAccount(t, h, admin, awsAccountBody)

	a, _ := s.store.GetCloudAccount(v.ID)
	now := time.Now()
	k := dueKinds(a, now)
	if !k.cost || !k.instances || !k.traffic {
		t.Fatalf("新账户应同步全部数据 %+v", k)
	}
	s.syncCloudAccount(context.Background(), a, k, now)

	var list struct{ Items []cloudAccountView }
	json.Unmarshal(do(h, "GET", "/api/v1/cloud-accounts", admin, nil).Body.Bytes(), &list)
	got := list.Items[0]
	if got.LastError != "" || got.InstanceCount != 2 || got.CurrentCost == nil || got.CurrentCost.AmountCents != 321 ||
		*got.CurrentCost.ForecastCents != 1000 || got.CostSyncedAt == 0 || got.TrafficSyncedAt == 0 {
		t.Fatalf("同步后 %+v cost=%+v", got, got.CurrentCost)
	}
	var insts struct{ Items []CloudInstance }
	json.Unmarshal(do(h, "GET", "/api/v1/cloud-instances?account_id="+itoa64(v.ID), admin, nil).Body.Bytes(), &insts)
	if len(insts.Items) != 2 {
		t.Fatalf("实例 %+v", insts.Items)
	}
	for _, in := range insts.Items {
		if in.Kind == "lightsail" && (in.TrafficLimitBytes != 2048e9 || in.TrafficUsedBytes != 2e9 || in.TrafficPeriodStart == "") {
			t.Fatalf("Lightsail 流量 %+v", in)
		}
		if in.Kind == "ec2" && (in.InstanceID != "i-1" || in.PublicIPv4 != "203.0.113.9" || in.AccountName != "主账户") {
			t.Fatalf("EC2 %+v", in)
		}
	}
	var costs struct{ Items []CloudCost }
	json.Unmarshal(do(h, "GET", "/api/v1/cloud-accounts/"+itoa64(v.ID)+"/costs", admin, nil).Body.Bytes(), &costs)
	if len(costs.Items) != 1 || costs.Items[0].Currency != "USD" {
		t.Fatalf("按月费用 %+v", costs.Items)
	}
	// 刚同步过：没有到期的数据
	a, _ = s.store.GetCloudAccount(v.ID)
	if dueKinds(a, now.Add(time.Minute)).any() {
		t.Fatal("刚同步过不应再同步")
	}
	if k := dueKinds(a, now.Add(61*time.Minute)); !k.traffic || k.instances || k.cost {
		t.Fatalf("1 小时后只同步流量 %+v", k)
	}

	// 凭证失效：停止自动同步，账户上显示错误
	deny.Store(true)
	s.syncCloudAccount(context.Background(), a, cloudKinds{cost: true}, now)
	a, _ = s.store.GetCloudAccount(v.ID)
	if !a.AuthFailed || !strings.Contains(a.LastError, "UnrecognizedClientException") || a.FailCount != 1 {
		t.Fatalf("凭证失效后 %+v", a)
	}
	if dueKinds(a, now.Add(24*time.Hour)).any() {
		t.Fatal("凭证失效后不应自动同步")
	}
	// 更换凭证（需重新验证）：清除失效状态
	deny.Store(false)
	rec := do(h, "PUT", "/api/v1/cloud-accounts/"+itoa64(v.ID), admin, []byte(`{"name":"主账户",
		"credential":{"access_key_id":"AKIAIOSFODNN7EXAMPL2","secret_access_key":"`+testSecret+`"}}`))
	if rec.Code != 200 {
		t.Fatalf("更换凭证 %d %s", rec.Code, rec.Body)
	}
	a, _ = s.store.GetCloudAccount(v.ID)
	if a.AuthFailed || a.LastError != "" || a.CredentialHint != "AKIA…MPL2" || len(a.Regions) != 1 {
		t.Fatalf("更换凭证后 %+v", a)
	}
	// 只改设置不需要重新验证，也不改变凭证
	u, _ := s.store.UserByName("admin")
	plain, _ := s.store.CreateSession(u.ID, "127.0.0.1", "test", time.Now())
	rec = do(h, "PUT", "/api/v1/cloud-accounts/"+itoa64(v.ID), plain, []byte(`{"name":"改名","enabled":false,"cost_interval_h":24}`))
	if rec.Code != 200 {
		t.Fatalf("修改设置 %d %s", rec.Code, rec.Body)
	}
	a, _ = s.store.GetCloudAccount(v.ID)
	if a.Name != "改名" || a.Enabled || a.CostIntervalH != 24 || a.CredentialHint != "AKIA…MPL2" {
		t.Fatalf("修改设置后 %+v", a)
	}
	if code := do(h, "PUT", "/api/v1/cloud-accounts/"+itoa64(v.ID), plain, []byte(`{"name":"改名","credential":{"access_key_id":"x"}}`)).Code; code != http.StatusForbidden {
		t.Fatalf("未重新验证更换凭证应 403，得到 %d", code)
	}

	// 手动同步：1 分钟内只允许一次
	if code := do(h, "POST", "/api/v1/cloud-accounts/"+itoa64(v.ID)+"/sync", admin, nil).Code; code != http.StatusAccepted {
		t.Fatalf("手动同步 %d", code)
	}
	if code := do(h, "POST", "/api/v1/cloud-accounts/"+itoa64(v.ID)+"/sync", admin, nil).Code; code != http.StatusTooManyRequests {
		t.Fatalf("重复手动同步应 429，得到 %d", code)
	}

	// 删除：同步的数据一并删除
	if code := do(h, "DELETE", "/api/v1/cloud-accounts/"+itoa64(v.ID), admin, nil).Code; code != http.StatusNoContent {
		t.Fatalf("删除 %d", code)
	}
	var n int
	s.store.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM cloud_instances) + (SELECT COUNT(*) FROM cloud_costs)`).Scan(&n)
	if n != 0 {
		t.Fatalf("删除账户后仍有 %d 行数据", n)
	}
	if code := do(h, "GET", "/api/v1/cloud-accounts/"+itoa64(v.ID)+"/costs", admin, nil).Code; code != http.StatusNotFound {
		t.Fatalf("已删除的账户应 404，得到 %d", code)
	}
}

// secret.key 丢失（如恢复备份时没有一并迁移）：停止自动同步并提示，不反复重试
func TestCloudSyncMissingSecretKey(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, v, _ := createCloudAccount(t, h, admin, awsAccountBody)
	os.Remove(filepath.Join(s.store.Dir, "secret.key"))
	s.cloud.sealer = nil
	a, _ := s.store.GetCloudAccount(v.ID)
	s.syncCloudAccount(context.Background(), a, cloudKinds{instances: true}, time.Now())
	a, _ = s.store.GetCloudAccount(v.ID)
	if !a.AuthFailed || !strings.Contains(a.LastError, "secret.key") {
		t.Fatalf("缺少密钥时 %+v", a)
	}
}

func TestCloudBackoff(t *testing.T) {
	for n, want := range map[int]time.Duration{0: time.Minute, 1: 2 * time.Minute, 3: 8 * time.Minute, 20: 6 * time.Hour} {
		if got := cloudBackoff(n); got != want {
			t.Errorf("cloudBackoff(%d) = %v，期望 %v", n, got, want)
		}
	}
	a := CloudAccount{Enabled: true, SyncCost: true, SyncTraffic: true, CostIntervalH: 12, NextTryAt: 1000}
	if dueKinds(a, time.Unix(999, 0)).any() {
		t.Fatal("退避期间不应同步")
	}
	a.Enabled = false
	if dueKinds(a, time.Unix(5000, 0)).any() {
		t.Fatal("停用的账户不应同步")
	}
}

// 阿里云（国内站）：凭证字段、余额、轻量应用服务器流量包；实例重新同步后流量包额度保留
func TestCloudAliyunSync(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("x-acs-action") {
		case "QueryBillOverview":
			io.WriteString(w, `{"Data":{"Items":{"Item":[{"PretaxAmount":30.5,"Currency":"CNY"}]}}}`)
		case "QueryAccountBalance":
			io.WriteString(w, `{"Data":{"AvailableAmount":"12.00","Currency":"CNY"}}`)
		case "DescribeRegions":
			io.WriteString(w, `{"Regions":{"Region":[{"RegionId":"cn-hongkong"}]}}`)
		case "DescribeInstances":
			io.WriteString(w, `{"Instances":{"Instance":[]}}`)
		case "ListRegions":
			io.WriteString(w, `{"Regions":[{"RegionId":"cn-hongkong"}]}`)
		case "ListInstances":
			io.WriteString(w, `{"TotalCount":1,"Instances":[{"InstanceId":"swas-1","InstanceName":"hk","Status":"Running","PublicIpAddress":"8.0.0.1","ExpiredTime":"2026-11-08T16:00:00Z"}]}`)
		case "ListInstancesTrafficPackages":
			io.WriteString(w, `{"InstanceTrafficPackageUsages":[{"InstanceId":"swas-1","TrafficUsed":5000000000,"TrafficPackageTotal":1000000000000}]}`)
		default:
			w.WriteHeader(400)
		}
	}))
	defer srv.Close()
	s.cloud.opts = cloud.Options{Endpoint: func(service, region string) string { return srv.URL + "/" }}

	// AWS 的字段名不适用于阿里云
	code, _, e := createCloudAccount(t, h, admin, `{"provider":"aliyun_cn","name":"阿里云","credential":{"access_key_id":"LTAI5tExampleExample","secret_access_key":"x"}}`)
	if code != http.StatusUnprocessableEntity || e.Details[0].Field != "credential.access_key_secret" {
		t.Fatalf("字段错误 %d %+v", code, e)
	}
	code, v, e := createCloudAccount(t, h, admin, `{"provider":"aliyun_cn","name":"阿里云","regions":["cn-hongkong"],
		"credential":{"access_key_id":"LTAI5tExampleExample","access_key_secret":"secretsecretsecretsecret"}}`)
	if code != http.StatusCreated || v.CredentialHint != "LTAI…mple" || v.Provider != "aliyun_cn" {
		t.Fatalf("添加 %d %+v %+v", code, v, e)
	}
	a, _ := s.store.GetCloudAccount(v.ID)
	now := time.Now()
	s.syncCloudAccount(context.Background(), a, cloudKinds{cost: true, instances: true, traffic: true}, now)
	// 再同步一次实例：列表中没有流量包额度，不能把已同步的额度清零
	a, _ = s.store.GetCloudAccount(v.ID)
	s.syncCloudAccount(context.Background(), a, cloudKinds{instances: true}, now)
	a, _ = s.store.GetCloudAccount(v.ID)
	if a.LastError != "" {
		t.Fatalf("同步失败 %s", a.LastError)
	}
	insts, _ := s.store.CloudInstances(v.ID)
	if len(insts) != 1 || insts[0].Kind != "swas" || insts[0].TrafficLimitBytes != 1e12 || insts[0].TrafficUsedBytes != 5e9 || insts[0].ExpireAt == 0 {
		t.Fatalf("轻量实例 %+v", insts)
	}
	costs, _ := s.store.CloudCosts(v.ID, 1)
	if len(costs) != 1 || costs[0].AmountCents != 3050 || costs[0].BalanceCents == nil || *costs[0].BalanceCents != 1200 || costs[0].Currency != "CNY" {
		t.Fatalf("费用 %+v", costs)
	}
}

// tencentTestID 是测试用的假 SecretId，分两段书写以免被 GitHub 推送保护当作真实密钥
const tencentTestID = "AKID" + "testidtestidtestidtestidtestid12"

// 腾讯云（国际站）：凭证字段、余额（分）、轻量流量包
func TestCloudTencentSync(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-TC-Action") {
		case "DescribeBillSummaryByProduct":
			io.WriteString(w, `{"Response":{"SummaryTotal":{"RealTotalCost":"9.99"}}}`)
		case "DescribeAccountBalance":
			io.WriteString(w, `{"Response":{"Balance":500}}`)
		case "DescribeRegions":
			io.WriteString(w, `{"Response":{"RegionSet":[{"Region":"ap-singapore","RegionState":"AVAILABLE"}]}}`)
		case "DescribeInstances":
			if strings.Contains(r.URL.Path, "lighthouse") {
				io.WriteString(w, `{"Response":{"TotalCount":1,"InstanceSet":[{"InstanceId":"lhins-1","InstanceState":"RUNNING","PublicAddresses":["1.2.3.4"]}]}}`)
			} else {
				io.WriteString(w, `{"Response":{"TotalCount":0,"InstanceSet":[]}}`)
			}
		case "DescribeInstancesTrafficPackages":
			io.WriteString(w, `{"Response":{"InstanceTrafficPackageSet":[{"InstanceId":"lhins-1","TrafficPackageSet":[{"TrafficUsed":100,"TrafficPackageTotal":1000}]}]}}`)
		}
	}))
	defer srv.Close()
	s.cloud.opts = cloud.Options{Endpoint: func(service, _ string) string { return srv.URL + "/" + service + "/" }}

	code, v, e := createCloudAccount(t, h, admin, `{"provider":"tencent_intl","name":"腾讯云","credential":{"secret_id":"`+tencentTestID+`","secret_key":"testkeytestkeytestkeytestkey1234"}}`)
	if code != http.StatusCreated || v.CredentialHint != "AKID…id12" {
		t.Fatalf("添加 %d %+v %+v", code, v, e)
	}
	a, _ := s.store.GetCloudAccount(v.ID)
	s.syncCloudAccount(context.Background(), a, cloudKinds{cost: true, instances: true, traffic: true}, time.Now())
	a, _ = s.store.GetCloudAccount(v.ID)
	if a.LastError != "" {
		t.Fatalf("同步失败 %s", a.LastError)
	}
	insts, _ := s.store.CloudInstances(v.ID)
	costs, _ := s.store.CloudCosts(v.ID, 1)
	if len(insts) != 1 || insts[0].Kind != "lighthouse" || insts[0].TrafficLimitBytes != 1000 || insts[0].TrafficUsedBytes != 100 {
		t.Fatalf("实例 %+v", insts)
	}
	if len(costs) != 1 || costs[0].AmountCents != 999 || *costs[0].BalanceCents != 500 || costs[0].Currency != "USD" {
		t.Fatalf("费用 %+v", costs)
	}
	if code, _, e := createCloudAccount(t, h, admin, `{"provider":"tencent_cn","name":"x","credential":{"secret_id":"bad","secret_key":"testkeytestkeytestkeytestkey1234"}}`); code != 422 || e.Details[0].Field != "credential.secret_id" {
		t.Fatalf("SecretId 校验 %d %+v", code, e)
	}
}

// 实例与节点关联（设计 44.5）：按公网 IP 建议、确认关联、到期日写入节点、删除节点后取消关联
func TestCloudInstanceLink(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, v, _ := createCloudAccount(t, h, admin, awsAccountBody)
	_, a, _ := createNode(t, h, admin, `{"name":"tokyo","expected_ipv4":"203.0.113.5","traffic_timezone":"Asia/Tokyo"}`)
	_, b, _ := createNode(t, h, admin, `{"name":"dup-1","expected_ipv4":"198.51.100.9"}`)
	_, c, _ := createNode(t, h, admin, `{"name":"dup-2","expected_ipv4":"198.51.100.9"}`)
	_ = b
	_ = c
	exp := time.Date(2026, 12, 1, 16, 0, 0, 0, time.UTC) // 东京时间 12 月 2 日
	if err := s.store.ReplaceCloudInstances(v.ID, []cloud.Instance{
		{ID: "i-tokyo", Kind: "ec2", IPv4: "203.0.113.5", ExpireAt: exp.Unix()},
		{ID: "i-dup", Kind: "ec2", IPv4: "198.51.100.9"}, // 两个节点同 IP：不建议
		{ID: "i-none", Kind: "ec2", IPv4: "192.0.2.1"},
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	list := func(q string) []CloudInstance {
		var l struct{ Items []CloudInstance }
		json.Unmarshal(do(h, "GET", "/api/v1/cloud-instances"+q, admin, nil).Body.Bytes(), &l)
		return l.Items
	}
	by := map[string]CloudInstance{}
	for _, in := range list("") {
		by[in.InstanceID] = in
	}
	if s := by["i-tokyo"].SuggestedServerID; s == nil || *s != int64(a.ServerID) {
		t.Fatalf("应建议关联到 tokyo：%+v", by["i-tokyo"])
	}
	if by["i-dup"].SuggestedServerID != nil || by["i-none"].SuggestedServerID != nil {
		t.Fatal("IP 不唯一或没有匹配时不建议")
	}

	// 尚未关联：不能写入到期日
	iid := itoa(by["i-tokyo"].ID)
	if rec := do(h, "POST", "/api/v1/cloud-instances/"+iid+"/apply-expire", admin, nil); rec.Code != http.StatusConflict {
		t.Fatalf("未关联时应为 409，得到 %d", rec.Code)
	}
	rec := do(h, "PUT", "/api/v1/cloud-instances/"+iid+"/server", admin, []byte(`{"server_id":`+itoa(a.ServerID)+`}`))
	if rec.Code != 200 {
		t.Fatalf("关联 %d %s", rec.Code, rec.Body)
	}
	if got := list("?server_id=" + itoa(a.ServerID)); len(got) != 1 || got[0].InstanceID != "i-tokyo" || got[0].SuggestedServerID != nil {
		t.Fatalf("按节点筛选 %+v", got)
	}
	// 到期日按节点的计费时区（东京）取日期
	rec = do(h, "POST", "/api/v1/cloud-instances/"+iid+"/apply-expire", admin, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "2026-12-02") {
		t.Fatalf("写入到期日 %d %s", rec.Code, rec.Body)
	}
	row, _ := s.store.GetServer(int64(a.ServerID))
	if row.ExpireDate != "2026-12-02" {
		t.Fatalf("节点到期日 %q", row.ExpireDate)
	}
	var n int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action IN ('cloud_instance.link', 'server.update')`).Scan(&n)
	if n < 2 {
		t.Fatalf("关联与写入应记入审计：%d", n)
	}
	// 重新同步实例：保留关联
	s.store.ReplaceCloudInstances(v.ID, []cloud.Instance{{ID: "i-tokyo", Kind: "ec2", IPv4: "203.0.113.5"}}, time.Now())
	if got := list("?server_id=" + itoa(a.ServerID)); len(got) != 1 {
		t.Fatal("重新同步后应保留关联")
	}
	// 关联到不存在的节点
	if rec := do(h, "PUT", "/api/v1/cloud-instances/"+iid+"/server", admin, []byte(`{"server_id":9999}`)); rec.Code != 422 {
		t.Fatalf("不存在的节点应为 422，得到 %d", rec.Code)
	}
	// 删除节点：关联自动取消
	do(h, "DELETE", "/api/v1/servers/"+itoa(a.ServerID), admin, nil)
	if got, _ := s.store.CloudInstanceByID(by["i-tokyo"].ID); got.ServerID != nil {
		t.Fatal("删除节点后应取消关联")
	}
	// 取消关联
	do(h, "PUT", "/api/v1/cloud-instances/"+iid+"/server", admin, []byte(`{"server_id":`+itoa(b.ServerID)+`}`))
	if rec := do(h, "PUT", "/api/v1/cloud-instances/"+iid+"/server", admin, []byte(`{"server_id":null}`)); rec.Code != 200 {
		t.Fatalf("取消关联 %d", rec.Code)
	}
	if got, _ := s.store.CloudInstanceByID(by["i-tokyo"].ID); got.ServerID != nil {
		t.Fatal("应已取消关联")
	}
}

// Oracle Cloud：凭证校验（OCID、指纹、主区域、私钥）；私钥只以密文保存，提示只显示指纹末尾
// ociSampleKey 是 Oracle 官方文档的示例私钥（JSON 转义后的字符串；PEM 头尾拆开写，避免推送保护误判）
const ociSampleKey = "-----BEGIN RSA " + "PRIVATE KEY-----\\n" + `MIICXgIBAAKBgQDCFENGw33yGihy92pDjZQhl0C36rPJj+CvfSC8+q28hxA161QF\nNUd13wuCTUcq0Qd2qsBe/2hFyc2DCJJg0h1L78+6Z4UMR7EOcpfdUE9Hf3m/hs+F\nUR45uBJeDK1HSFHD8bHKD6kv8FPGfJTotc+2xjJwoYi+1hqp1fIekaxsyQIDAQAB\nAoGBAJR8ZkCUvx5kzv+utdl7T5MnordT1TvoXXJGXK7ZZ+UuvMNUCdN2QPc4sBiA\nQWvLw1cSKt5DsKZ8UETpYPy8pPYnnDEz2dDYiaew9+xEpubyeW2oH4Zx71wqBtOK\nkqwrXa/pzdpiucRRjk6vE6YY7EBBs/g7uanVpGibOVAEsqH1AkEA7DkjVH28WDUg\nf1nqvfn2Kj6CT7nIcE3jGJsZZ7zlZmBmHFDONMLUrXR/Zm3pR5m0tCmBqa5RK95u\n412jt1dPIwJBANJT3v8pnkth48bQo/fKel6uEYyboRtA5/uHuHkZ6FQF7OUkGogc\nmSJluOdc5t6hI1VsLn0QZEjQZMEOWr+wKSMCQQCC4kXJEsHAve77oP6HtG/IiEn7\nkpyUXRNvFsDE0czpJJBvL/aRFUJxuRK91jhjC68sA7NsKMGg5OXb5I5Jj36xAkEA\ngIT7aFOYBFwGgQAQkWNKLvySgKbAZRTeLBacpHMuQdl1DfdntvAyqpAZ0lY0RKmW\nG6aFKaqQfOXKCyWoUiVknQJAXrlgySFci/2ueKlIE1QqIiLSZ8V8OlpFLRnb1pzI\n7U1yQXnTAEFYM560yJlzUpOb1V4cScGd365tiSMvxLOvTA==\n` + "-----END RSA " + "PRIVATE KEY-----"

func TestCloudOCIAccount(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	code, _, e := createCloudAccount(t, h, admin, `{"provider": "oci", "name": "Oracle2", "credential": {"tenancy_ocid": "ocid1.tenancy.oc1..aaaa", "user_ocid": "ocid1.user.oc1..bbbb", "fingerprint": "xx", "region": "ap-tokyo-1", "private_key": "-----BEGIN RSA `+"PRIVATE KEY-----\\nAAAA\\n-----END RSA "+`PRIVATE KEY-----"}}`)
	fields := map[string]bool{}
	for _, d := range e.Details {
		fields[d.Field] = true
	}
	if code != 422 || !fields["credential.fingerprint"] || !fields["credential.private_key"] {
		t.Fatalf("应拒绝错误的指纹与私钥：%d %+v", code, e)
	}
	code, v, e := createCloudAccount(t, h, admin, strings.Replace(`{"provider": "oci", "name": "Oracle", "credential": {"tenancy_ocid": "ocid1.tenancy.oc1..aaaa", "user_ocid": "ocid1.user.oc1..bbbb", "fingerprint": "73:61:a2:21:67:e0:df:be:7e:4b:93:1e:15:98:a5:b7", "region": "ap-tokyo-1", "private_key": "PEM"}}`, "PEM", ociSampleKey, 1))
	if code != 201 || v.Provider != "oci" || v.CredentialHint != "…a5:b7" {
		t.Fatalf("添加 %d %+v %+v", code, v, e)
	}
	var enc []byte
	s.store.DB.QueryRow(`SELECT credential_enc FROM cloud_accounts WHERE id = ?`, v.ID).Scan(&enc)
	if strings.Contains(string(enc), "PRIVATE KEY") {
		t.Fatal("【安全】私钥不能明文入库")
	}
	a, _ := s.store.GetCloudAccount(v.ID)
	if _, err := s.cloudClient(a); err != nil {
		t.Fatalf("应能用解密后的凭证创建客户端：%v", err)
	} // 租户出站流量项不是机器：不能关联节点
	now := time.Now()
	s.store.ReplaceCloudInstances(v.ID, []cloud.Instance{{ID: "egress", Name: "出站流量", Kind: "oci_egress", TrafficLimit: 10e12}}, now)
	insts, _ := s.store.CloudInstances(v.ID)
	_, node, _ := createNode(t, h, admin, `{"name":"oci-node"}`)
	rec := do(h, "PUT", "/api/v1/cloud-instances/"+itoa64(insts[0].ID)+"/server", admin, []byte(`{"server_id":`+itoa(node.ServerID)+`}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("出站流量项不应能关联节点：%d %s", rec.Code, rec.Body)
	}
}

// 区域级失败（修订第 141 条）：其他区域照常保存，被跳过区域的实例保留，记为警告，不判为凭证失效、不停止同步。
func TestCloudSyncRegionWarning(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	var deny, lsDown atomic.Bool
	s.cloud.opts = fakeAWSServer(t, &deny, &lsDown)
	_, v, _ := createCloudAccount(t, h, admin, awsAccountBody)
	a, _ := s.store.GetCloudAccount(v.ID)
	now := time.Now()
	s.syncCloudAccount(context.Background(), a, cloudKinds{instances: true}, now)

	lsDown.Store(true)
	a, _ = s.store.GetCloudAccount(v.ID)
	s.syncCloudAccount(context.Background(), a, cloudKinds{instances: true}, now.Add(7*time.Hour))
	a, _ = s.store.GetCloudAccount(v.ID)
	if a.AuthFailed || a.LastError != "" || a.FailCount != 0 {
		t.Fatalf("区域级失败不应停止同步：%+v", a)
	}
	if !strings.Contains(a.LastWarning, "区域 ap-northeast-1 的 Lightsail 不可用（可能未开通该区域），已跳过") {
		t.Errorf("警告：%q", a.LastWarning)
	}
	var insts struct{ Items []CloudInstance }
	json.Unmarshal(do(h, "GET", "/api/v1/cloud-instances?account_id="+itoa64(v.ID), admin, nil).Body.Bytes(), &insts)
	if len(insts.Items) != 2 {
		t.Errorf("被跳过区域的 Lightsail 实例应保留：%+v", insts.Items)
	}
	var list struct{ Items []cloudAccountView }
	json.Unmarshal(do(h, "GET", "/api/v1/cloud-accounts", admin, nil).Body.Bytes(), &list)
	if list.Items[0].LastWarning == "" {
		t.Error("接口应返回警告")
	}

	// 恢复后警告清除
	lsDown.Store(false)
	s.syncCloudAccount(context.Background(), a, cloudKinds{instances: true}, now.Add(14*time.Hour))
	if a, _ = s.store.GetCloudAccount(v.ID); a.LastWarning != "" {
		t.Errorf("恢复后应清除警告：%q", a.LastWarning)
	}
}

// App 云账户摘要（修订第 144 条）：只有“全部节点”授权的设备能看到；不含凭证提示、公网 IP 与实例 ID；Web 会话不能访问。
func TestAppCloud(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	var deny atomic.Bool
	s.cloud.opts = fakeAWSServer(t, &deny)
	_, v, _ := createCloudAccount(t, h, admin, awsAccountBody)
	a, _ := s.store.GetCloudAccount(v.ID)
	s.syncCloudAccount(context.Background(), a, cloudKinds{cost: true, instances: true}, time.Now())

	keyAll, _, _ := createAppKey(t, h, admin, `{"name":"all"}`)
	_, all := pair(t, h, keyAll)
	rec := do(h, "GET", "/api/v1/app/cloud", all.AccessToken, nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	var got appCloudView
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Limited || len(got.Accounts) != 1 || len(got.Instances) != 2 {
		t.Fatalf("摘要：%s", body)
	}
	acc := got.Accounts[0]
	if acc.Provider != "aws" || acc.InstanceCount != 2 || acc.SyncedAt == 0 || acc.CurrentCost == nil || acc.CurrentCost.AmountCents != 321 {
		t.Errorf("账户：%+v", acc)
	}
	// 【安全】最小字段：没有凭证提示、公网 IP、云厂商实例 ID
	for _, bad := range []string{"credential", "hint", "203.0.113.9", "198.51.100.9", "i-1", "public_ip", "instance_id"} {
		if strings.Contains(body, bad) {
			t.Errorf("【安全】不应返回 %q：%s", bad, body)
		}
	}

	// 分组授权的设备：limited，列表为空
	keyGroup, _, _ := createAppKey(t, h, admin, `{"name":"g","scope_type":"group","group":"jp"}`)
	_, g := pair(t, h, keyGroup)
	rec = do(h, "GET", "/api/v1/app/cloud", g.AccessToken, nil)
	var lim appCloudView
	json.Unmarshal(rec.Body.Bytes(), &lim)
	if rec.Code != 200 || !lim.Limited || len(lim.Accounts) != 0 || len(lim.Instances) != 0 {
		t.Errorf("分组授权：%d %s", rec.Code, rec.Body)
	}
	// 只接受设备凭证
	if rec := do(h, "GET", "/api/v1/app/cloud", admin, nil); rec.Code != 401 {
		t.Errorf("Web 会话应被拒绝：%d", rec.Code)
	}
}

// 区域暂时失败时约 30 分钟后提前重新检查实例；恢复后回到常规 6 小时；未开通的区域不提前（修订第 151 条）。
func TestCloudRegionRecheck(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	var deny, lsDown, lsBusy atomic.Bool
	s.cloud.opts = fakeAWSServer(t, &deny, &lsDown, &lsBusy)
	_, v, _ := createCloudAccount(t, h, admin, awsAccountBody)
	now := time.Now()
	sync := func(at time.Time) CloudAccount {
		a, _ := s.store.GetCloudAccount(v.ID)
		s.syncCloudAccount(context.Background(), a, cloudKinds{instances: true}, at)
		a, _ = s.store.GetCloudAccount(v.ID)
		return a
	}

	lsBusy.Store(true)
	a := sync(now)
	if want := now.Add(cloudRecheckAfter).Unix(); a.InstancesRecheckAt != want || a.LastWarning == "" || a.AuthFailed {
		t.Fatalf("初始化中：recheck=%d（应为 %d） warning=%q", a.InstancesRecheckAt, want, a.LastWarning)
	}
	if dueKinds(a, now.Add(29*time.Minute)).instances {
		t.Error("30 分钟内不应重新检查")
	}
	if !dueKinds(a, now.Add(cloudRecheckAfter)).instances {
		t.Error("到了提前检查的时间应同步实例")
	}
	var list struct{ Items []cloudAccountView }
	json.Unmarshal(do(h, "GET", "/api/v1/cloud-accounts", admin, nil).Body.Bytes(), &list)
	if list.Items[0].InstancesRecheck != a.InstancesRecheckAt {
		t.Errorf("接口应返回提前检查的时间：%+v", list.Items[0])
	}

	// 恢复：警告与提前检查都清除，回到常规周期
	lsBusy.Store(false)
	a = sync(now.Add(cloudRecheckAfter))
	if a.InstancesRecheckAt != 0 || a.LastWarning != "" {
		t.Fatalf("恢复后：recheck=%d warning=%q", a.InstancesRecheckAt, a.LastWarning)
	}
	if dueKinds(a, now.Add(2*cloudRecheckAfter)).instances {
		t.Error("恢复后应按常规 6 小时")
	}

	// 未开通的区域：有警告但不提前
	lsDown.Store(true)
	if a = sync(now.Add(7 * time.Hour)); a.LastWarning == "" || a.InstancesRecheckAt != 0 {
		t.Errorf("未开通：recheck=%d warning=%q", a.InstancesRecheckAt, a.LastWarning)
	}
}

// 阿里云 CDT 流量（修订第 181 条）：两个免费额度池随流量同步保存；没有 CDT 权限时只记为提示、保留上次数据，不当作凭证失效
func TestCloudAliyunCDT(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	var deny atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("x-acs-action") {
		case "DescribeRegions":
			io.WriteString(w, `{"Regions":{"Region":[{"RegionId":"cn-hongkong"}]}}`)
		case "DescribeInstances":
			io.WriteString(w, `{"Instances":{"Instance":[{"InstanceId":"i-1","Status":"Running","InstanceChargeType":"PostPaid"}]}}`)
		case "ListRegions":
			io.WriteString(w, `{"Regions":[]}`)
		case "ListCdtInternetTraffic":
			if deny.Load() {
				w.WriteHeader(403)
				io.WriteString(w, `{"Code":"Forbidden.RAM","Message":"User not authorized to operate on the specified resource."}`)
				return
			}
			io.WriteString(w, `{"TrafficDetails":[{"BusinessRegionId":"cn-hongkong","ISPType":"BGP","Traffic":193273528320},
				{"BusinessRegionId":"cn-shanghai","ISPType":"BGP","Traffic":1073741824}]}`)
		default:
			w.WriteHeader(400)
		}
	}))
	defer srv.Close()
	s.cloud.opts = cloud.Options{Endpoint: func(service, region string) string { return srv.URL + "/" }}
	code, v, e := createCloudAccount(t, h, admin, `{"provider":"aliyun_cn","name":"cdt","credential":{"access_key_id":"LTAI5tExampleExample","access_key_secret":"secretsecretsecretsecret"}}`)
	if code != http.StatusCreated {
		t.Fatalf("添加 %d %+v", code, e)
	}
	view := func() cloudAccountView {
		t.Helper()
		var out struct{ Items []cloudAccountView }
		json.Unmarshal(do(h, "GET", "/api/v1/cloud-accounts", admin, nil).Body.Bytes(), &out)
		for _, x := range out.Items {
			if x.ID == v.ID {
				return x
			}
		}
		t.Fatal("没有账户")
		return cloudAccountView{}
	}
	sync := func() {
		a, _ := s.store.GetCloudAccount(v.ID)
		s.syncCloudAccount(context.Background(), a, cloudKinds{instances: true, traffic: true}, time.Now())
	}

	sync()
	x := view()
	if x.LastError != "" || x.PoolWarning != "" || len(x.TrafficPools) != 2 {
		t.Fatalf("同步后 %+v", x)
	}
	cn, intl := x.TrafficPools[0], x.TrafficPools[1]
	if cn.Key != "cdt_cn" || cn.UsedBytes != 1<<30 || cn.LimitBytes != 20<<30 || intl.Key != "cdt_intl" || intl.UsedBytes != 180<<30 {
		t.Fatalf("流量池 %+v", x.TrafficPools)
	}
	// 非中国内地已用 90%：提醒一次
	s.checkReminders(time.Now())
	var n int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM reminders_sent WHERE key LIKE 'cloud_pool:%'`).Scan(&n)
	if n != 1 {
		t.Fatalf("应提醒 1 次，实际 %d", n)
	}

	deny.Store(true)
	sync()
	x = view()
	a, _ := s.store.GetCloudAccount(v.ID)
	if a.AuthFailed || x.LastError != "" || !strings.Contains(x.PoolWarning, "CDT 只读权限") || len(x.TrafficPools) != 2 {
		t.Fatalf("缺少权限时 %+v（auth_failed %v）", x, a.AuthFailed)
	}
}

// 只授予 ECS + CDT 只读的子用户（CDT 用户的常见配置，修订第 184 条）：轻量应用服务器与费用中心被拒绝时只跳过并提示，
// ECS 实例与 CDT 流量照常同步，账户不能被判为凭证失效
func TestCloudAliyunECSOnlyPermissions(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	denied := `{"Code":"Forbidden.RAM","Message":"User not authorized to operate on the specified resource."}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("x-acs-action") {
		case "DescribeRegions":
			io.WriteString(w, `{"Regions":{"Region":[{"RegionId":"cn-hongkong"}]}}`)
		case "DescribeInstances":
			// 按流量计费的走 CDT（修订第 186 条）；固定带宽的不走
			io.WriteString(w, `{"Instances":{"Instance":[{"InstanceId":"i-cdt","InstanceName":"hk-cdt","Status":"Running","InstanceChargeType":"PostPaid",
				"InternetChargeType":"PayByTraffic","PublicIpAddress":{"IpAddress":["47.0.0.9"]}},
				{"InstanceId":"i-bw","Status":"Running","InstanceChargeType":"PostPaid","InternetChargeType":"PayByBandwidth","PublicIpAddress":{"IpAddress":["47.0.0.10"]}}]}}`)
		case "ListCdtInternetTraffic":
			io.WriteString(w, `{"TrafficDetails":[{"BusinessRegionId":"cn-hongkong","ISPType":"BGP","Traffic":10737418240}]}`)
		case "ListRegions", "QueryBillOverview", "QueryAccountBalance":
			w.WriteHeader(403)
			io.WriteString(w, denied)
		default:
			w.WriteHeader(400)
		}
	}))
	defer srv.Close()
	s.cloud.opts = cloud.Options{Endpoint: func(service, region string) string { return srv.URL + "/" }}
	code, v, e := createCloudAccount(t, h, admin, `{"provider":"aliyun_cn","name":"ecs-only","credential":{"access_key_id":"LTAI5tExampleExample","access_key_secret":"secretsecretsecretsecret"}}`)
	if code != http.StatusCreated {
		t.Fatalf("添加 %d %+v", code, e)
	}
	a, _ := s.store.GetCloudAccount(v.ID)
	s.syncCloudAccount(context.Background(), a, cloudKinds{cost: true, instances: true, traffic: true}, time.Now())

	a, _ = s.store.GetCloudAccount(v.ID)
	if a.AuthFailed || a.LastError != "" {
		t.Fatalf("不应判为失败：auth_failed %v，错误 %q", a.AuthFailed, a.LastError)
	}
	if !strings.Contains(a.LastWarning, "轻量应用服务器：子用户没有只读权限") {
		t.Fatalf("应提示轻量应用服务器没有权限：%q", a.LastWarning)
	}
	insts, _ := s.store.CloudInstances(v.ID)
	if len(insts) != 2 || insts[0].InstanceID != "i-bw" || insts[0].CDTPool != "" || insts[1].InstanceID != "i-cdt" ||
		insts[1].PublicIPv4 != "47.0.0.9" || insts[1].CDTPool != "cdt_intl" {
		t.Fatalf("ECS 实例 %+v", insts)
	}
	var out struct{ Items []cloudAccountView }
	json.Unmarshal(do(h, "GET", "/api/v1/cloud-accounts", admin, nil).Body.Bytes(), &out)
	x := out.Items[0]
	if !strings.Contains(x.CostWarning, "AliyunBSSReadOnlyAccess") || x.CurrentCost != nil {
		t.Fatalf("费用应跳过并提示：%+v", x)
	}
	if len(x.TrafficPools) != 1 || x.TrafficPools[0].Key != "cdt_intl" || x.TrafficPools[0].UsedBytes != 10<<30 {
		t.Fatalf("CDT %+v", x.TrafficPools)
	}
	// 费用同步时间已更新：不会每分钟重试被拒绝的接口
	if dueKinds(a, time.Now()).cost {
		t.Fatal("费用被拒绝后不应立即重试")
	}

	// 权限检查（修订第 204 条）：逐项列出，ECS 写明各区域台数与走 CDT 的台数
	rec := do(h, "POST", "/api/v1/cloud-accounts/"+itoa64(v.ID)+"/check", admin, nil)
	var chk struct{ Items []cloud.CheckItem }
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &chk) != nil || len(chk.Items) != 4 {
		t.Fatalf("检查 %d %s", rec.Code, rec.Body)
	}
	if it := chk.Items[0]; it.OK || it.Policy != "AliyunBSSReadOnlyAccess" || !strings.HasPrefix(it.Detail, "没有权限") {
		t.Errorf("费用 %+v", it)
	}
	if it := chk.Items[1]; !it.OK || it.Detail != "查询了 1 个区域，共 2 台：cn-hongkong 2 台（走 CDT 1 台）" {
		t.Errorf("ECS %+v", it)
	}
	if it := chk.Items[2]; it.OK || it.Policy != "AliyunSWASReadOnlyAccess" {
		t.Errorf("轻量 %+v", it)
	}
	if it := chk.Items[3]; !it.OK || it.Detail != "本月已用：CDT 非中国内地 10.00 GiB" {
		t.Errorf("CDT %+v", it)
	}
	if rec := do(h, "POST", "/api/v1/cloud-accounts/"+itoa64(v.ID)+"/check", admin, nil); rec.Code != http.StatusTooManyRequests {
		t.Errorf("10 秒内再次检查应限流：%d", rec.Code)
	}
}
