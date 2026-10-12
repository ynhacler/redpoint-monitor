package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAWS 模拟 Cost Explorer、EC2 与 Lightsail（路径为 /服务/区域/）。
type fakeAWS struct {
	mu       sync.Mutex
	calls    []string // “服务 区域 操作”
	bodies   map[string]map[string]any
	forecast int // 0 正常；1 DataUnavailableException
	deny     bool
	// fail 按“服务 区域 操作”依次返回的错误：500 为服务端错误，403 为 UnauthorizedOperation；用完后正常响应
	fail map[string][]int
}

func (f *fakeAWS) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		service, region := parts[0], parts[1]
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/") || !strings.Contains(auth, "/"+region+"/"+service+"/aws4_request") {
			t.Errorf("签名范围不对：%s", auth)
		}
		op := r.Header.Get("X-Amz-Target")
		if op == "" {
			op = r.URL.Query().Get("Action")
		}
		f.mu.Lock()
		f.calls = append(f.calls, service+" "+region+" "+op)
		var body map[string]any
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			json.Unmarshal(b, &body)
			if f.bodies == nil {
				f.bodies = map[string]map[string]any{}
			}
			f.bodies[op] = body
		}
		deny, forecast := f.deny, f.forecast
		key := service + " " + region + " " + op
		var failWith int
		if q := f.fail[key]; len(q) > 0 {
			failWith, f.fail[key] = q[0], q[1:]
		}
		f.mu.Unlock()
		switch failWith {
		case 500:
			w.WriteHeader(500)
			io.WriteString(w, `<Response><Errors><Error><Code>InternalError</Code><Message>An internal error has occurred</Message></Error></Errors></Response>`)
			return
		case 403:
			w.WriteHeader(403)
			io.WriteString(w, `<Response><Errors><Error><Code>UnauthorizedOperation</Code><Message>You are not authorized to perform this operation.</Message></Error></Errors></Response>`)
			return
		}
		if deny {
			w.WriteHeader(400)
			io.WriteString(w, `{"__type":"com.amazon.coral.service#UnrecognizedClientException","message":"The security token included in the request is invalid."}`)
			return
		}
		switch op {
		case "AWSInsightsIndexService.GetCostAndUsage":
			io.WriteString(w, `{"ResultsByTime":[{"Total":{"UnblendedCost":{"Amount":"12.3456","Unit":"USD"}}}]}`)
		case "AWSInsightsIndexService.GetCostForecast":
			if forecast == 1 {
				w.WriteHeader(400)
				io.WriteString(w, `{"__type":"DataUnavailableException","Message":"Insufficient amount of historical data"}`)
				return
			}
			io.WriteString(w, `{"Total":{"Amount":"20.004","Unit":"USD"}}`)
		case "DescribeRegions":
			io.WriteString(w, `<DescribeRegionsResponse><regionInfo><item><regionName>us-east-1</regionName></item><item><regionName>ap-northeast-1</regionName></item></regionInfo></DescribeRegionsResponse>`)
		case "DescribeInstances":
			if region == "ap-northeast-1" && r.URL.Query().Get("NextToken") == "" {
				io.WriteString(w, `<DescribeInstancesResponse><reservationSet><item><instancesSet><item>
<instanceId>i-0abc</instanceId><instanceType>t3.micro</instanceType><instanceState><name>running</name></instanceState>
<ipAddress>203.0.113.5</ipAddress><networkInterfaceSet><item><ipv6AddressesSet><item><ipv6Address>2001:db8::5</ipv6Address></item></ipv6AddressesSet></item></networkInterfaceSet>
<tagSet><item><key>env</key><value>x</value></item><item><key>Name</key><value>tokyo-web</value></item></tagSet></item>
<item><instanceId>i-0dead</instanceId><instanceState><name>terminated</name></instanceState></item></instancesSet></item></reservationSet>
<nextToken>page2</nextToken></DescribeInstancesResponse>`)
				return
			}
			if region == "ap-northeast-1" {
				io.WriteString(w, `<DescribeInstancesResponse><reservationSet><item><instancesSet><item><instanceId>i-0def</instanceId><instanceType>t3.small</instanceType><instanceState><name>stopped</name></instanceState></item></instancesSet></item></reservationSet></DescribeInstancesResponse>`)
				return
			}
			io.WriteString(w, `<DescribeInstancesResponse><reservationSet/></DescribeInstancesResponse>`)
		case "Lightsail_20161128.GetRegions":
			// ap-southeast-3（雅加达）是账户没有开通的选择加入区域：不在 DescribeRegions 中，调用会被拒绝
			io.WriteString(w, `{"regions":[{"name":"ap-northeast-1"},{"name":"ap-southeast-3"}]}`)
		case "Lightsail_20161128.GetInstances":
			if region == "ap-southeast-3" {
				w.WriteHeader(400)
				io.WriteString(w, `{"__type":"com.amazon.coral.service#UnrecognizedClientException","message":"The security token included in the request is invalid."}`)
				return
			}
			io.WriteString(w, `{"instances":[{"name":"ls-1","bundleId":"nano_3_0","publicIpAddress":"198.51.100.7","ipv6Addresses":["2001:db8::7"],
"state":{"name":"running"},"networking":{"monthlyTransfer":{"gbPerMonthAllocated":1024}}}]}`)
		case "Lightsail_20161128.GetInstanceMetricData":
			if body["metricName"] == "NetworkOut" {
				io.WriteString(w, `{"metricData":[{"sum":1e9},{"sum":5e8}]}`)
			} else {
				io.WriteString(w, `{"metricData":[{"sum":2.5e8}]}`)
			}
		default:
			t.Errorf("未预期的调用 %s %s %s", service, region, op)
			w.WriteHeader(400)
		}
	}
}

func newFakeAWS(t *testing.T, regions []string) (*fakeAWS, Client) {
	f := &fakeAWS{}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	cred, _ := json.Marshal(exampleCreds)
	c, err := NewClient(ProviderAWS, cred, regions, Options{
		Endpoint: func(service, region string) string { return srv.URL + "/" + service + "/" + region + "/" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

func TestAWSCosts(t *testing.T) {
	f, c := newFakeAWS(t, nil)
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	got, err := c.Costs(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Period != "2026-10" || got.AmountCents != 1235 || got.Currency != "USD" || got.ForecastCents == nil || *got.ForecastCents != 1235+2000 {
		t.Fatalf("费用 = %+v forecast=%v", got, got.ForecastCents)
	}
	// 时间范围：本月 1 日到明天（不含）；预测从明天到下月 1 日
	tp := f.bodies["AWSInsightsIndexService.GetCostAndUsage"]["TimePeriod"].(map[string]any)
	if tp["Start"] != "2026-10-01" || tp["End"] != "2026-10-05" {
		t.Fatalf("费用区间 %v", tp)
	}
	tp = f.bodies["AWSInsightsIndexService.GetCostForecast"]["TimePeriod"].(map[string]any)
	if tp["Start"] != "2026-10-05" || tp["End"] != "2026-11-01" {
		t.Fatalf("预测区间 %v", tp)
	}

	// 新账户没有足够历史：只有已产生的费用
	f.forecast = 1
	got, err = c.Costs(context.Background(), now)
	if err != nil || got.ForecastCents != nil || got.AmountCents != 1235 {
		t.Fatalf("没有预测时 = %+v, %v", got, err)
	}
	// 月末最后一天：不再调用预测，预估即已产生
	f.calls = nil
	got, err = c.Costs(context.Background(), time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC))
	if err != nil || got.ForecastCents == nil || *got.ForecastCents != 1235 || len(f.calls) != 1 {
		t.Fatalf("月末 = %+v, %v, 调用 %v", got, err, f.calls)
	}
}

func TestAWSInstancesAndTraffic(t *testing.T) {
	f, c := newFakeAWS(t, []string{"ap-northeast-1"})
	insts, err := c.Instances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(insts) != 3 {
		t.Fatalf("实例 %+v", insts)
	}
	ec2, ec2b, ls := insts[0], insts[1], insts[2]
	if ec2.ID != "i-0abc" || ec2.Name != "tokyo-web" || ec2.IPv4 != "203.0.113.5" || ec2.IPv6 != "2001:db8::5" ||
		ec2.Plan != "t3.micro" || ec2.State != "running" || ec2.Region != "ap-northeast-1" {
		t.Fatalf("EC2 %+v", ec2)
	}
	if ec2b.ID != "i-0def" || ec2b.State != "stopped" {
		t.Fatalf("第二页的实例 %+v", ec2b)
	}
	if ls.Kind != "lightsail" || ls.ID != "ap-northeast-1/ls-1" || ls.TrafficLimit != 1024e9 || ls.IPv6 != "2001:db8::7" {
		t.Fatalf("Lightsail %+v", ls)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "us-east-1 DescribeInstances") {
			t.Fatal("只应查询账户设置的区域")
		}
	}

	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	if err := c.Traffic(context.Background(), insts, now); err != nil {
		t.Fatal(err)
	}
	if insts[2].TrafficUsed != 1.75e9 || insts[2].TrafficPeriodStart != "2026-10-01" {
		t.Fatalf("流量 %+v", insts[2])
	}
	if insts[0].TrafficUsed != 0 {
		t.Fatal("EC2 没有流量包，不应查询")
	}
	md := f.bodies["Lightsail_20161128.GetInstanceMetricData"]
	if md["instanceName"] != "ls-1" || md["startTime"] != float64(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix()) {
		t.Fatalf("指标请求 %v", md)
	}
}

func TestAWSAuthError(t *testing.T) {
	f, c := newFakeAWS(t, nil)
	f.deny = true
	_, err := c.Costs(context.Background(), time.Now())
	if !IsAuthError(err) {
		t.Fatalf("凭证无效应识别为认证错误：%v", err)
	}
	if !strings.Contains(err.Error(), "UnrecognizedClientException") || strings.Contains(err.Error(), "AKIDEXAMPLE") {
		t.Fatalf("错误信息 %v", err)
	}
	if IsAuthError(&ProviderError{Status: 400, Code: "ThrottlingException"}) {
		t.Fatal("限流不是认证错误")
	}
}

func TestToCents(t *testing.T) {
	for in, want := range map[string]int64{"0": 0, "12.3449": 1234, "12.345": 1235, "0.004": 0, "-1.5": -150, "1e2": 10000} {
		if got, err := toCents(in); err != nil || got != want {
			t.Errorf("toCents(%q) = %d, %v", in, got, err)
		}
	}
	if _, err := toCents("NaN"); err == nil {
		t.Error("NaN 应报错")
	}
}

// 账户没有开通的选择加入区域（修订第 140 条）：Lightsail 的 GetRegions 会列出它，但在那里调用返回
// UnrecognizedClientException；只查 DescribeRegions（已开通）中也有的区域，整轮同步不因此失败。
func TestAWSSkipsUnopenedLightsailRegions(t *testing.T) {
	f, c := newFakeAWS(t, nil)
	insts, err := c.Instances(context.Background())
	if err != nil {
		t.Fatalf("没开通的区域不应让同步失败：%v", err)
	}
	if len(insts) != 3 {
		t.Fatalf("实例 %+v", insts)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "ap-southeast-3") {
			t.Errorf("不应查询没开通的区域：%s", call)
		}
	}
}

// 区域级失败的隔离（修订第 141 条）。
func TestAWSRegionFailures(t *testing.T) {
	old := retryDelay
	retryDelay = 0
	t.Cleanup(func() { retryDelay = old })
	count := func(f *fakeAWS, key string) int {
		n := 0
		for _, c := range f.calls {
			if c == key {
				n++
			}
		}
		return n
	}

	// 偶发服务端错误：重试一次后成功，整轮正常
	f, c := newFakeAWS(t, nil)
	f.fail = map[string][]int{"ec2 ap-northeast-1 DescribeInstances": {500}}
	insts, err := c.Instances(context.Background())
	if err != nil || len(insts) != 3 || count(f, "ec2 ap-northeast-1 DescribeInstances") != 3 { // 失败 1 次 + 重试后两页
		t.Fatalf("偶发错误应重试后成功：%v %d %v", err, len(insts), f.calls)
	}

	// 一个区域持续失败：跳过它，其他区域照常返回，错误为区域级（不判为凭证失效）
	f, c = newFakeAWS(t, nil)
	f.fail = map[string][]int{"ec2 ap-northeast-1 DescribeInstances": {500, 500}}
	insts, err = c.Instances(context.Background())
	var pe *PartialError
	if !errors.As(err, &pe) || len(pe.Skipped) != 1 || pe.Skipped[0].Region != "ap-northeast-1" || pe.Skipped[0].Kind != "ec2" {
		t.Fatalf("应返回部分结果与区域级错误：%v", err)
	}
	if len(insts) != 1 || insts[0].Kind != "lightsail" {
		t.Errorf("其他区域的实例照常返回：%+v", insts)
	}
	if IsAuthError(err) || !strings.Contains(err.Error(), "区域 ap-northeast-1 的 EC2 调用失败，已跳过") {
		t.Errorf("区域级错误不是凭证失效，说明指出区域：%v", err)
	}

	// 区域内返回凭证类错误码，也只跳过该区域
	f, c = newFakeAWS(t, nil)
	f.fail = map[string][]int{"ec2 ap-northeast-1 DescribeInstances": {403}}
	if _, err := c.Instances(context.Background()); !errors.As(err, &pe) || IsAuthError(err) {
		t.Errorf("只有一个区域被拒绝时是区域级：%v", err)
	}

	// 所有区域都被拒绝：IAM 缺少 ec2:DescribeInstances，是权限问题（账户级）
	f, c = newFakeAWS(t, nil)
	f.fail = map[string][]int{"ec2 us-east-1 DescribeInstances": {403}, "ec2 ap-northeast-1 DescribeInstances": {403}}
	if _, err := c.Instances(context.Background()); !IsAuthError(err) || errors.As(err, &pe) {
		t.Errorf("所有区域都被拒绝应判为权限问题：%v", err)
	}

	// 主区域列出区域失败：账户级
	f, c = newFakeAWS(t, nil)
	f.fail = map[string][]int{"ec2 us-east-1 DescribeRegions": {403}}
	if _, err := c.Instances(context.Background()); !IsAuthError(err) || errors.As(err, &pe) {
		t.Errorf("主区域失败应判为凭证问题：%v", err)
	}
}
