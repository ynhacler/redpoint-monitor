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
func fakeAWSServer(t *testing.T, deny *atomic.Bool) cloud.Options {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
