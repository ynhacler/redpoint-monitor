package cloud

// AWS 客户端（设计 44.3）：只用到下列只读接口。
//
//	Cost Explorer  GetCostAndUsage、GetCostForecast（us-east-1；每次调用 0.01 美元，由同步频率控制次数）
//	EC2            DescribeRegions、DescribeInstances（Query API，XML）
//	Lightsail      GetRegions、GetInstances、GetInstanceMetricData（JSON 1.1）
//
// 最小权限：ce:GetCostAndUsage、ce:GetCostForecast、ec2:DescribeInstances、ec2:DescribeRegions、lightsail:Get*。

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type awsClient struct {
	cred    AWSCredentials
	regions []string // 为空表示全部已启用的区域
	o       Options
}

func (c *awsClient) endpoint(service, region string) string {
	if c.o.Endpoint != nil {
		return c.o.Endpoint(service, region)
	}
	return "https://" + service + "." + region + ".amazonaws.com/"
}

// jsonCall 调用 JSON 1.1 协议的接口（Cost Explorer、Lightsail）。
func (c *awsClient) jsonCall(ctx context.Context, service, region, target string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint(service, region), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", target)
	SignV4(req, body, c.cred, region, service, time.Now())
	res, err := c.o.HTTP.Do(req)
	if err != nil {
		return err
	}
	b, err := readBody(res, awsJSONError)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// queryCall 调用 EC2 的 Query API（GET，参数在查询串中），响应为 XML。
func (c *awsClient) queryCall(ctx context.Context, region string, params url.Values, out any) error {
	params.Set("Version", "2016-11-15")
	req, err := http.NewRequestWithContext(ctx, "GET", c.endpoint("ec2", region)+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	SignV4(req, nil, c.cred, region, "ec2", time.Now())
	res, err := c.o.HTTP.Do(req)
	if err != nil {
		return err
	}
	b, err := readBody(res, awsXMLError)
	if err != nil {
		return err
	}
	return xml.Unmarshal(b, out)
}

// awsJSONError 解析 JSON 协议的错误：{"__type": "com.amazon…#AccessDeniedException", "message": "…"}
func awsJSONError(status int, b []byte) *ProviderError {
	var e struct {
		Type     string `json:"__type"`
		Message  string `json:"message"`
		Message2 string `json:"Message"`
	}
	_ = json.Unmarshal(b, &e)
	code := e.Type
	if i := strings.LastIndexByte(code, '#'); i >= 0 {
		code = code[i+1:]
	}
	if i := strings.IndexByte(code, ':'); i >= 0 {
		code = code[:i]
	}
	msg := e.Message
	if msg == "" {
		msg = e.Message2
	}
	return &ProviderError{Status: status, Code: code, Message: msg}
}

// awsXMLError 解析 EC2 的错误：<Response><Errors><Error><Code>…</Code><Message>…</Message></Error></Errors></Response>
func awsXMLError(status int, b []byte) *ProviderError {
	var e struct {
		Errors []struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		} `xml:"Errors>Error"`
	}
	_ = xml.Unmarshal(b, &e)
	pe := &ProviderError{Status: status}
	if len(e.Errors) > 0 {
		pe.Code, pe.Message = e.Errors[0].Code, e.Errors[0].Message
	}
	return pe
}

// ---- 费用（Cost Explorer） ----

type ceAmount struct {
	Amount string `json:"Amount"`
	Unit   string `json:"Unit"`
}

// Costs 读取本月（UTC）已产生的费用与月末预估。预估在历史数据不足时不可用，此时只返回已产生的费用。
func (c *awsClient) Costs(ctx context.Context, now time.Time) (*Costs, error) {
	start := monthStart(now)
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	const day = "2006-01-02"
	// End 不含当天，取明天才包含今天已产生的部分
	var usage struct {
		ResultsByTime []struct {
			Total map[string]ceAmount `json:"Total"`
		} `json:"ResultsByTime"`
	}
	err := c.jsonCall(ctx, "ce", "us-east-1", "AWSInsightsIndexService.GetCostAndUsage", map[string]any{
		"TimePeriod":  map[string]string{"Start": start.Format(day), "End": today.AddDate(0, 0, 1).Format(day)},
		"Granularity": "MONTHLY",
		"Metrics":     []string{"UnblendedCost"},
	}, &usage)
	if err != nil {
		return nil, err
	}
	out := &Costs{Period: start.Format("2006-01"), Currency: "USD"}
	for _, r := range usage.ResultsByTime {
		a, ok := r.Total["UnblendedCost"]
		if !ok {
			continue
		}
		cents, err := toCents(a.Amount)
		if err != nil {
			return nil, err
		}
		out.AmountCents += cents
		if a.Unit != "" {
			out.Currency = a.Unit
		}
	}

	// 预估：从今天到月末的预测值 + 已产生的费用。月末最后一天之后没有剩余区间，预估即已产生
	next := start.AddDate(0, 1, 0)
	if !today.Before(next.AddDate(0, 0, -1)) {
		v := out.AmountCents
		out.ForecastCents = &v
		return out, nil
	}
	var fc struct {
		Total ceAmount `json:"Total"`
	}
	err = c.jsonCall(ctx, "ce", "us-east-1", "AWSInsightsIndexService.GetCostForecast", map[string]any{
		"TimePeriod":  map[string]string{"Start": today.AddDate(0, 0, 1).Format(day), "End": next.Format(day)},
		"Granularity": "MONTHLY",
		"Metric":      "UNBLENDED_COST",
	}, &fc)
	var pe *ProviderError
	switch {
	case err == nil:
		if cents, err := toCents(fc.Total.Amount); err == nil {
			v := out.AmountCents + cents
			out.ForecastCents = &v
		}
	case errors.As(err, &pe) && pe.Code == "DataUnavailableException":
		// 新账户历史数据不足：没有预估
	default:
		return nil, err
	}
	return out, nil
}

// ---- 实例（EC2 + Lightsail） ----

// Instances 列出全部区域的 EC2 与 Lightsail 实例。区域并发查询（最多 4 个同时进行）。
func (c *awsClient) Instances(ctx context.Context) ([]Instance, error) {
	ec2Regions, err := c.ec2Regions(ctx)
	if err != nil {
		return nil, err
	}
	lsRegions, err := c.lightsailRegions(ctx)
	if err != nil {
		return nil, err
	}
	type job struct {
		region string
		fn     func(context.Context, string) ([]Instance, error)
	}
	var jobs []job
	for _, r := range ec2Regions {
		jobs = append(jobs, job{r, c.ec2Instances})
	}
	for _, r := range lsRegions {
		jobs = append(jobs, job{r, c.lightsailInstances})
	}
	var (
		mu       sync.Mutex
		all      []Instance
		firstErr error
		wg       sync.WaitGroup
		sem      = make(chan struct{}, 4)
	)
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			got, err := j.fn(ctx, j.region)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			all = append(all, got...)
		}(j)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	sort.Slice(all, func(i, k int) bool {
		if all[i].Kind != all[k].Kind {
			return all[i].Kind < all[k].Kind
		}
		return all[i].ID < all[k].ID
	})
	return all, nil
}

func (c *awsClient) wanted(region string) bool {
	if len(c.regions) == 0 {
		return true
	}
	for _, r := range c.regions {
		if r == region {
			return true
		}
	}
	return false
}

func (c *awsClient) ec2Regions(ctx context.Context) ([]string, error) {
	var out struct {
		Regions []string `xml:"regionInfo>item>regionName"`
	}
	if err := c.queryCall(ctx, "us-east-1", url.Values{"Action": {"DescribeRegions"}}, &out); err != nil {
		return nil, err
	}
	var rs []string
	for _, r := range out.Regions {
		if c.wanted(r) {
			rs = append(rs, r)
		}
	}
	return rs, nil
}

type ec2Instance struct {
	ID    string   `xml:"instanceId"`
	Type  string   `xml:"instanceType"`
	State string   `xml:"instanceState>name"`
	IPv4  string   `xml:"ipAddress"`
	IPv6  []string `xml:"networkInterfaceSet>item>ipv6AddressesSet>item>ipv6Address"`
	Tags  []struct {
		Key   string `xml:"key"`
		Value string `xml:"value"`
	} `xml:"tagSet>item"`
}

func (c *awsClient) ec2Instances(ctx context.Context, region string) ([]Instance, error) {
	var all []Instance
	token := ""
	for page := 0; page < 50; page++ {
		p := url.Values{"Action": {"DescribeInstances"}, "MaxResults": {"1000"}}
		if token != "" {
			p.Set("NextToken", token)
		}
		var out struct {
			Instances []ec2Instance `xml:"reservationSet>item>instancesSet>item"`
			NextToken string        `xml:"nextToken"`
		}
		if err := c.queryCall(ctx, region, p, &out); err != nil {
			return nil, err
		}
		for _, x := range out.Instances {
			if x.State == "terminated" {
				continue
			}
			in := Instance{ID: x.ID, Region: region, Kind: "ec2", State: x.State, IPv4: x.IPv4, Plan: x.Type}
			if len(x.IPv6) > 0 {
				in.IPv6 = x.IPv6[0]
			}
			for _, t := range x.Tags {
				if t.Key == "Name" {
					in.Name = t.Value
				}
			}
			all = append(all, in)
		}
		if token = out.NextToken; token == "" {
			break
		}
	}
	return all, nil
}

func (c *awsClient) lightsailRegions(ctx context.Context) ([]string, error) {
	var out struct {
		Regions []struct {
			Name string `json:"name"`
		} `json:"regions"`
	}
	if err := c.jsonCall(ctx, "lightsail", "us-east-1", "Lightsail_20161128.GetRegions", map[string]any{}, &out); err != nil {
		return nil, err
	}
	var rs []string
	for _, r := range out.Regions {
		if c.wanted(r.Name) {
			rs = append(rs, r.Name)
		}
	}
	return rs, nil
}

func (c *awsClient) lightsailInstances(ctx context.Context, region string) ([]Instance, error) {
	var all []Instance
	token := ""
	for page := 0; page < 50; page++ {
		in := map[string]any{}
		if token != "" {
			in["pageToken"] = token
		}
		var out struct {
			Instances []struct {
				Name     string   `json:"name"`
				BundleID string   `json:"bundleId"`
				IPv4     string   `json:"publicIpAddress"`
				IPv6     []string `json:"ipv6Addresses"`
				State    struct {
					Name string `json:"name"`
				} `json:"state"`
				Networking struct {
					MonthlyTransfer struct {
						GBPerMonth float64 `json:"gbPerMonthAllocated"`
					} `json:"monthlyTransfer"`
				} `json:"networking"`
			} `json:"instances"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.jsonCall(ctx, "lightsail", region, "Lightsail_20161128.GetInstances", in, &out); err != nil {
			return nil, err
		}
		for _, x := range out.Instances {
			inst := Instance{ID: region + "/" + x.Name, Name: x.Name, Region: region, Kind: "lightsail",
				State: x.State.Name, IPv4: x.IPv4, Plan: x.BundleID,
				// Lightsail 套餐的流量额度按 GB（10⁹ 字节）计
				TrafficLimit: uint64(x.Networking.MonthlyTransfer.GBPerMonth * 1e9)}
			if len(x.IPv6) > 0 {
				inst.IPv6 = x.IPv6[0]
			}
			all = append(all, inst)
		}
		if token = out.NextPageToken; token == "" {
			break
		}
	}
	return all, nil
}

// Traffic 为 Lightsail 实例补充本月（UTC 自然月）的流量：入站 + 出站都计入套餐额度。
// 数据来自 CloudWatch 指标，有数小时延迟。
func (c *awsClient) Traffic(ctx context.Context, insts []Instance, now time.Time) error {
	start := monthStart(now)
	for i := range insts {
		in := &insts[i]
		if in.Kind != "lightsail" || in.TrafficLimit == 0 {
			continue
		}
		var used float64
		for _, metric := range []string{"NetworkIn", "NetworkOut"} {
			var out struct {
				Data []struct {
					Sum float64 `json:"sum"`
				} `json:"metricData"`
			}
			err := c.jsonCall(ctx, "lightsail", in.Region, "Lightsail_20161128.GetInstanceMetricData", map[string]any{
				"instanceName": in.Name, "metricName": metric, "period": 86400,
				"startTime": start.Unix(), "endTime": now.Unix(), "unit": "Bytes", "statistics": []string{"Sum"},
			}, &out)
			if err != nil {
				return err
			}
			for _, d := range out.Data {
				used += d.Sum
			}
		}
		in.TrafficUsed = uint64(used)
		in.TrafficPeriodStart = start.Format("2006-01-02")
	}
	return nil
}
