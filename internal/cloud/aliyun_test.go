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
		case "ListCdtInternetTraffic":
			// 杭州 BGP 5 GiB + 精品 1 GiB（不计入免费额度）；香港属于非中国内地；东京 BGP 7 GiB
			io.WriteString(w, `{"RequestId":"x","TrafficDetails":[
{"BusinessRegionId":"cn-hangzhou","ISPType":"BGP","Traffic":5368709120},
{"BusinessRegionId":"cn-hangzhou","ISPType":"BGP_PRO","Traffic":1073741824},
{"BusinessRegionId":"cn-hongkong","ISPType":"BGP","Traffic":3221225472},
{"BusinessRegionId":"ap-northeast-1","ISPType":"BGP","Traffic":7516192768}]}`)
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
		{cn.host("ecs", ""), "https://ecs.cn-hangzhou.aliyuncs.com/"},
		{cn.host("swas", ""), "https://swas.cn-hangzhou.aliyuncs.com/"},
		{cn.host("swas", "cn-hongkong"), "https://swas.cn-hongkong.aliyuncs.com/"},
		// 国际站：区域列表等不分区域的调用走新加坡接入点
		{intl.host("ecs", ""), "https://ecs.ap-southeast-1.aliyuncs.com/"},
		{intl.host("swas", ""), "https://swas.ap-southeast-1.aliyuncs.com/"},
		{cn.host("cdt", ""), "https://cdt.aliyuncs.com/"},
		{intl.host("cdt", ""), "https://cdt.aliyuncs.com/"},
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

func TestParseCloudTime(t *testing.T) {
	want := time.Date(2026, 11, 8, 16, 0, 0, 0, time.UTC).Unix()
	for _, s := range []string{"2026-11-08T16:00Z", "2026-11-08T16:00:00Z", "2026-11-08T16:00:00.000+0000", "2026-11-09T00:00:00+08:00"} {
		if got := parseCloudTime(s); got != want {
			t.Errorf("parseCloudTime(%q) = %d", s, got)
		}
	}
	if parseCloudTime("") != 0 {
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

// CDT 流量（修订第 181 条）：按地域分到中国内地 / 非中国内地两个免费额度池（香港属于非中国内地），精品线路不计入
func TestAliyunCDTPools(t *testing.T) {
	f, c := newFakeAliyun(t, ProviderAliyunCN, nil)
	// 北京时间已是 11 月 1 日：周期从 11 月 1 日开始
	pools, err := c.(PoolTrafficer).TrafficPools(context.Background(), time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	want := []TrafficPool{
		{Key: "cdt_cn", Name: "CDT 中国内地", UsedBytes: 5 << 30, LimitBytes: 20 << 30, PeriodStart: "2026-11-01"},
		{Key: "cdt_intl", Name: "CDT 非中国内地", UsedBytes: 10 << 30, LimitBytes: 200 << 30, PeriodStart: "2026-11-01"},
	}
	if len(pools) != len(want) {
		t.Fatalf("池子 %+v", pools)
	}
	for i := range want {
		if pools[i] != want[i] {
			t.Errorf("第 %d 个池子 %+v，期望 %+v", i, pools[i], want[i])
		}
	}
	if len(f.calls) != 1 || f.calls[0] != "cdt  ListCdtInternetTraffic" {
		t.Fatalf("调用 %v", f.calls)
	}
}

func TestCDTMainland(t *testing.T) {
	for region, want := range map[string]bool{"cn-hangzhou": true, "cn-beijing": true, "cn-hongkong": false, "ap-southeast-1": false, "us-west-1": false} {
		if cdtMainland(region) != want {
			t.Errorf("%s 应为 %v", region, want)
		}
	}
}
