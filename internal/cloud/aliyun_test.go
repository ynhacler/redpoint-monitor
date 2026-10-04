package cloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeAliyun struct {
	mu      sync.Mutex
	calls   []string
	queries map[string]map[string]string
	deny    bool
}

func (f *fakeAliyun) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		service, region := parts[0], ""
		if len(parts) > 1 {
			region = parts[1]
		}
		if r.Method != "POST" || !strings.HasPrefix(r.Header.Get("Authorization"), "ACS3-HMAC-SHA256 Credential=LTAI5tExampleExample,") {
			t.Errorf("请求方法或签名不对：%s %s", r.Method, r.Header.Get("Authorization"))
		}
		action := r.Header.Get("x-acs-action")
		q := map[string]string{}
		for k := range r.URL.Query() {
			q[k] = r.URL.Query().Get(k)
		}
		f.mu.Lock()
		f.calls = append(f.calls, service+" "+region+" "+action)
		if f.queries == nil {
			f.queries = map[string]map[string]string{}
		}
		f.queries[action+" "+region] = q
		deny := f.deny
		f.mu.Unlock()
		if deny {
			w.WriteHeader(404)
			io.WriteString(w, `{"RequestId":"x","Code":"InvalidAccessKeyId.NotFound","Message":"Specified access key is not found."}`)
			return
		}
		switch action {
		case "QueryBillOverview":
			io.WriteString(w, `{"Code":"Success","Success":true,"Data":{"BillingCycle":"`+q["BillingCycle"]+`","Items":{"Item":[
{"ProductCode":"ecs","PretaxAmount":12.34,"Currency":"CNY"},{"ProductCode":"swas","PretaxAmount":0.666,"Currency":"CNY"}]}}}`)
		case "QueryAccountBalance":
			io.WriteString(w, `{"Code":"200","Success":true,"Data":{"AvailableAmount":"88.50","Currency":"CNY"}}`)
		case "DescribeRegions":
			io.WriteString(w, `{"Regions":{"Region":[{"RegionId":"cn-hangzhou"},{"RegionId":"cn-hongkong"}]}}`)
		case "DescribeInstances":
			if region != "cn-hongkong" {
				io.WriteString(w, `{"Instances":{"Instance":[]}}`)
				return
			}
			if q["NextToken"] == "" {
				io.WriteString(w, `{"NextToken":"p2","Instances":{"Instance":[{"InstanceId":"i-pre","InstanceName":"hk-web","InstanceType":"ecs.t6-c1m1.large",
"Status":"Running","InstanceChargeType":"PrePaid","ExpiredTime":"2026-12-01T16:00Z","PublicIpAddress":{"IpAddress":["47.0.0.1"]},
"NetworkInterfaces":{"NetworkInterface":[{"Ipv6Sets":{"Ipv6Set":[{"Ipv6Address":"2408::1"}]}}]}}]}}`)
				return
			}
			io.WriteString(w, `{"Instances":{"Instance":[{"InstanceId":"i-post","InstanceType":"ecs.g7.large","Status":"Stopped",
"InstanceChargeType":"PostPaid","ExpiredTime":"2099-12-31T15:59Z","PublicIpAddress":{"IpAddress":[]},"EipAddress":{"IpAddress":"47.0.0.2"}}]}}`)
		case "ListRegions":
			io.WriteString(w, `{"Regions":[{"RegionId":"cn-hongkong"}]}`)
		case "ListInstances":
			io.WriteString(w, `{"TotalCount":1,"Instances":[{"InstanceId":"swas-1","InstanceName":"hk-light","Status":"Running",
"PublicIpAddress":"8.0.0.1","ExpiredTime":"2026-11-08T16:00:00.000+0000","PlanId":"swas.s.c2m1s50b1.linux"}]}`)
		case "ListInstancesTrafficPackages":
			io.WriteString(w, `{"InstanceTrafficPackageUsages":[{"InstanceId":"swas-1","TrafficUsed":123456789,"TrafficPackageTotal":1099511627776,
"TrafficPackageRemaining":1099388171,"TrafficOverflow":0}]}`)
		default:
			t.Errorf("未预期的调用 %s %s %s", service, region, action)
			w.WriteHeader(400)
		}
	}
}

var aliyunExampleCreds, _ = json.Marshal(AliyunCredentials{AccessKeyID: "LTAI5tExampleExample", AccessKeySecret: "secretsecretsecretsecret"})

func newFakeAliyun(t *testing.T, provider string, regions []string) (*fakeAliyun, Client) {
	f := &fakeAliyun{}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	c, err := NewClient(provider, aliyunExampleCreds, regions, Options{
		Endpoint: func(service, region string) string { return srv.URL + "/" + service + "/" + region + "/" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

func TestAliyunEndpoints(t *testing.T) {
	cn := &aliyunClient{}
	intl := &aliyunClient{intl: true}
	for _, c := range []struct{ got, want string }{
		{cn.host("business", ""), "https://business.aliyuncs.com/"},
		{intl.host("business", ""), "https://business.ap-southeast-1.aliyuncs.com/"},
		{intl.host("ecs", "ap-northeast-1"), "https://ecs.ap-northeast-1.aliyuncs.com/"},
		{cn.host("ecs", ""), "https://ecs.aliyuncs.com/"},
		{cn.host("swas", "cn-hongkong"), "https://swas.cn-hongkong.aliyuncs.com/"},
	} {
		if c.got != c.want {
			t.Errorf("接入点 %s，期望 %s", c.got, c.want)
		}
	}
}

func TestAliyunCosts(t *testing.T) {
	f, c := newFakeAliyun(t, ProviderAliyunCN, nil)
	// 北京时间已是 11 月 1 日：账单月份按 UTC+8
	got, err := c.Costs(context.Background(), time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got.Period != "2026-11" || got.AmountCents != 1301 || got.Currency != "CNY" || got.ForecastCents != nil ||
		got.BalanceCents == nil || *got.BalanceCents != 8850 {
		t.Fatalf("费用 %+v", got)
	}
	if f.queries["QueryBillOverview "]["BillingCycle"] != "2026-11" {
		t.Fatalf("账单月份 %v", f.queries)
	}
}

func TestAliyunInstancesAndTraffic(t *testing.T) {
	f, c := newFakeAliyun(t, ProviderAliyunIntl, []string{"cn-hongkong"})
	insts, err := c.Instances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(insts) != 3 {
		t.Fatalf("实例 %+v", insts)
	}
	by := map[string]Instance{}
	for _, in := range insts {
		by[in.ID] = in
	}
	pre, post, light := by["i-pre"], by["i-post"], by["swas-1"]
	if pre.ID != "i-pre" || pre.Kind != "ecs" || pre.IPv4 != "47.0.0.1" || pre.IPv6 != "2408::1" ||
		pre.ExpireAt != time.Date(2026, 12, 1, 16, 0, 0, 0, time.UTC).Unix() || pre.State != "Running" {
		t.Fatalf("包年包月 ECS %+v", pre)
	}
	if post.ID != "i-post" || post.ExpireAt != 0 || post.IPv4 != "47.0.0.2" {
		t.Fatalf("按量 ECS（无到期、弹性 IP）%+v", post)
	}
	if light.Kind != "swas" || light.ExpireAt != time.Date(2026, 11, 8, 16, 0, 0, 0, time.UTC).Unix() || light.TrafficLimit != 0 {
		t.Fatalf("轻量 %+v", light)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "cn-hangzhou DescribeInstances") {
			t.Fatal("只应查询账户设置的区域")
		}
	}
	if err := c.Traffic(context.Background(), insts, time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	for _, in := range insts {
		switch in.Kind {
		case "swas":
			if in.TrafficLimit != 1099511627776 || in.TrafficUsed != 123456789 || in.TrafficPeriodStart != "2026-10-01" {
				t.Fatalf("流量包 %+v", in)
			}
		default:
			if in.TrafficPeriodStart != "" {
				t.Fatal("ECS 没有流量包")
			}
		}
	}
	if ids := f.queries["ListInstancesTrafficPackages cn-hongkong"]["InstanceIds"]; ids != `["swas-1"]` {
		t.Fatalf("InstanceIds = %s", ids)
	}
}

func TestParseAliyunTime(t *testing.T) {
	want := time.Date(2026, 11, 8, 16, 0, 0, 0, time.UTC).Unix()
	for _, s := range []string{"2026-11-08T16:00Z", "2026-11-08T16:00:00Z", "2026-11-08T16:00:00.000+0000", "2026-11-09T00:00:00+08:00"} {
		if got := parseAliyunTime(s); got != want {
			t.Errorf("parseAliyunTime(%q) = %d", s, got)
		}
	}
	if parseAliyunTime("") != 0 {
		t.Error("空值应为 0")
	}
}

func TestAliyunAuthError(t *testing.T) {
	f, c := newFakeAliyun(t, ProviderAliyunCN, nil)
	f.deny = true
	_, err := c.Costs(context.Background(), time.Now())
	if !IsAuthError(err) || !strings.Contains(err.Error(), "InvalidAccessKeyId.NotFound") {
		t.Fatalf("凭证无效应识别为认证错误：%v", err)
	}
}

func TestBillingMonth(t *testing.T) {
	now := time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC) // 北京时间 11 月 1 日 01:00
	if BillingMonth(ProviderAWS, now) != "2026-10" || BillingMonth(ProviderAliyunCN, now) != "2026-11" || BillingMonth(ProviderAliyunIntl, now) != "2026-11" {
		t.Fatal("账单月份时区不对")
	}
}
