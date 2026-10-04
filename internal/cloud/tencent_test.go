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

type fakeTencent struct {
	mu     sync.Mutex
	bodies map[string]map[string]any
	paths  []string
	deny   bool
}

func (f *fakeTencent) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service := strings.Trim(r.URL.Path, "/")
		action, region := r.Header.Get("X-TC-Action"), r.Header.Get("X-TC-Region")
		if !strings.HasPrefix(r.Header.Get("Authorization"), "TC3-HMAC-SHA256 Credential="+tencentTestID+"/") ||
			!strings.Contains(r.Header.Get("Authorization"), "/"+service+"/tc3_request") {
			t.Errorf("签名范围不对：%s", r.Header.Get("Authorization"))
		}
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		f.mu.Lock()
		if f.bodies == nil {
			f.bodies = map[string]map[string]any{}
		}
		f.bodies[action+" "+region] = body
		f.paths = append(f.paths, service+" "+region+" "+action)
		deny := f.deny
		f.mu.Unlock()
		if deny {
			// 腾讯云以 HTTP 200 返回错误
			io.WriteString(w, `{"Response":{"Error":{"Code":"AuthFailure.SecretIdNotFound","Message":"The SecretId is not found"},"RequestId":"x"}}`)
			return
		}
		switch action {
		case "DescribeBillSummaryByProduct":
			io.WriteString(w, `{"Response":{"SummaryTotal":{"RealTotalCost":"45.67890000","TotalCost":"60.00"},"RequestId":"x"}}`)
		case "DescribeAccountBalance":
			io.WriteString(w, `{"Response":{"Balance":12345,"RealBalance":12345.4,"RequestId":"x"}}`)
		case "DescribeRegions":
			io.WriteString(w, `{"Response":{"RegionSet":[{"Region":"ap-hongkong","RegionState":"AVAILABLE"},{"Region":"ap-guangzhou","RegionState":"AVAILABLE"}],"RequestId":"x"}}`)
		case "DescribeInstances":
			if region != "ap-hongkong" {
				io.WriteString(w, `{"Response":{"TotalCount":0,"InstanceSet":[],"RequestId":"x"}}`)
				return
			}
			if service == "cvm" {
				io.WriteString(w, `{"Response":{"TotalCount":2,"InstanceSet":[
{"InstanceId":"ins-pre","InstanceName":"hk-cvm","InstanceType":"S5.SMALL2","InstanceState":"RUNNING","InstanceChargeType":"PREPAID",
"ExpiredTime":"2026-12-01T16:00:00Z","PublicIpAddresses":["43.0.0.1"],"IPv6Addresses":["240e::1"]},
{"InstanceId":"ins-post","InstanceState":"STOPPED","InstanceChargeType":"POSTPAID_BY_HOUR","ExpiredTime":null,"PublicIpAddresses":null}],"RequestId":"x"}}`)
				return
			}
			io.WriteString(w, `{"Response":{"TotalCount":1,"InstanceSet":[{"InstanceId":"lhins-1","InstanceName":"hk-lh","InstanceState":"RUNNING",
"BundleId":"bundle_2022_gen_01","ExpiredTime":"2026-11-08T16:00:00Z","PublicAddresses":["101.0.0.1"]}],"RequestId":"x"}}`)
		case "DescribeInstancesTrafficPackages":
			io.WriteString(w, `{"Response":{"TotalCount":1,"InstanceTrafficPackageSet":[{"InstanceId":"lhins-1","TrafficPackageSet":[
{"TrafficPackageId":"lhtfp-1","TrafficUsed":5905577,"TrafficPackageTotal":536870912000,"StartTime":"2026-09-30T16:00:00Z","Status":"NETWORK_NORMAL"}]}],"RequestId":"x"}}`)
		default:
			t.Errorf("未预期的调用 %s %s %s", service, region, action)
		}
	}
}

func newFakeTencent(t *testing.T, provider string, regions []string) (*fakeTencent, Client) {
	f := &fakeTencent{}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	cred, _ := json.Marshal(TencentCredentials{SecretID: tencentTestID, SecretKey: "testkeytestkeytestkeytestkey1234"})
	c, err := NewClient(provider, cred, regions, Options{Endpoint: func(service, _ string) string { return srv.URL + "/" + service + "/" }})
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

func TestTencentEndpoints(t *testing.T) {
	if (&tencentClient{}).endpoint("billing") != "https://billing.tencentcloudapi.com/" ||
		(&tencentClient{intl: true}).endpoint("lighthouse") != "https://lighthouse.intl.tencentcloudapi.com/" {
		t.Fatal("接入点不对")
	}
}

func TestTencentCosts(t *testing.T) {
	f, c := newFakeTencent(t, ProviderTencentIntl, nil)
	got, err := c.Costs(context.Background(), time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC)) // 北京时间 11 月 1 日
	if err != nil {
		t.Fatal(err)
	}
	if got.Period != "2026-11" || got.AmountCents != 4568 || got.Currency != "USD" || got.ForecastCents != nil ||
		got.BalanceCents == nil || *got.BalanceCents != 12345 {
		t.Fatalf("费用 %+v", got)
	}
	if b := f.bodies["DescribeBillSummaryByProduct "]; b["BeginTime"] != "2026-11" || b["EndTime"] != "2026-11" {
		t.Fatalf("账单区间 %v", b)
	}
}

func TestTencentInstancesAndTraffic(t *testing.T) {
	f, c := newFakeTencent(t, ProviderTencentCN, []string{"ap-hongkong"})
	insts, err := c.Instances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Instance{}
	for _, in := range insts {
		by[in.ID] = in
	}
	if len(insts) != 3 {
		t.Fatalf("实例 %+v", insts)
	}
	if pre := by["ins-pre"]; pre.Kind != "cvm" || pre.IPv4 != "43.0.0.1" || pre.IPv6 != "240e::1" || pre.Plan != "S5.SMALL2" ||
		pre.ExpireAt != time.Date(2026, 12, 1, 16, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("包年包月 CVM %+v", pre)
	}
	if post := by["ins-post"]; post.ExpireAt != 0 || post.IPv4 != "" || post.State != "STOPPED" {
		t.Fatalf("按量 CVM %+v", post)
	}
	if lh := by["lhins-1"]; lh.Kind != "lighthouse" || lh.IPv4 != "101.0.0.1" || lh.ExpireAt == 0 || lh.Plan != "bundle_2022_gen_01" {
		t.Fatalf("轻量 %+v", lh)
	}
	for _, p := range f.paths {
		if strings.Contains(p, "ap-guangzhou DescribeInstances") {
			t.Fatal("只应查询账户设置的区域")
		}
	}
	if err := c.Traffic(context.Background(), insts, time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	for _, in := range insts {
		if in.Kind == "lighthouse" && (in.TrafficLimit != 536870912000 || in.TrafficUsed != 5905577 || in.TrafficPeriodStart != "2026-10-01") {
			t.Fatalf("流量包 %+v", in)
		}
	}
	if ids, _ := f.bodies["DescribeInstancesTrafficPackages ap-hongkong"]["InstanceIds"].([]any); len(ids) != 1 || ids[0] != "lhins-1" {
		t.Fatalf("InstanceIds %v", f.bodies["DescribeInstancesTrafficPackages ap-hongkong"])
	}
}

// 只有余额没有权限：照常返回费用，余额为空
func TestTencentBalanceDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-TC-Action") == "DescribeAccountBalance" {
			io.WriteString(w, `{"Response":{"Error":{"Code":"UnauthorizedOperation","Message":"no permission"}}}`)
			return
		}
		io.WriteString(w, `{"Response":{"SummaryTotal":{"RealTotalCost":"1.00"}}}`)
	}))
	defer srv.Close()
	cred, _ := json.Marshal(TencentCredentials{SecretID: tencentTestID, SecretKey: "k"})
	c, _ := NewClient(ProviderTencentCN, cred, nil, Options{Endpoint: func(string, string) string { return srv.URL + "/" }})
	got, err := c.Costs(context.Background(), time.Now())
	if err != nil || got.AmountCents != 100 || got.BalanceCents != nil {
		t.Fatalf("费用 %+v, %v", got, err)
	}
}

// 错误在 HTTP 200 的 Response.Error 中；凭证错误要识别为认证失败
func TestTencentAuthError(t *testing.T) {
	f, c := newFakeTencent(t, ProviderTencentCN, nil)
	f.deny = true
	_, err := c.Costs(context.Background(), time.Now())
	if !IsAuthError(err) || !strings.Contains(err.Error(), "AuthFailure.SecretIdNotFound") {
		t.Fatalf("凭证无效应识别为认证错误：%v", err)
	}
	if pe := tencentError([]byte(`{"Response":{"Error":{"Code":"RequestLimitExceeded","Message":"x"}}}`)); pe == nil || IsAuthError(pe) {
		t.Fatal("限流不是认证错误")
	}
	if tencentError([]byte(`{"Response":{"RequestId":"x"}}`)) != nil {
		t.Fatal("正常响应不是错误")
	}
}
