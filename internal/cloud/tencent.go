package cloud

// 腾讯云客户端（设计 44.3）：国内站（cloud.tencent.com）接口地址为 <产品>.tencentcloudapi.com，
// 国际站（tencentcloud.com）为 <产品>.intl.tencentcloudapi.com；区域由 X-TC-Region 头指定。
// 只用到下列只读接口（API 3.0，POST JSON，TC3 签名）：
//
//	计费 billing 2018-07-09      DescribeBillSummaryByProduct（本月总费用）、DescribeAccountBalance（可用余额，单位分）
//	云服务器 cvm 2017-03-12      DescribeRegions、DescribeInstances（包年包月有 ExpiredTime）
//	轻量 lighthouse 2020-03-24   DescribeRegions、DescribeInstances、DescribeInstancesTrafficPackages（字节）
//
// 最小权限：账单只读 QcloudFinanceBillReadOnlyAccess、余额只读 DescribeAccountBalance、QcloudCVMReadOnlyAccess、
// QcloudLighthouseReadOnlyAccess；也可用 Web 中给出的自定义策略（只列出上述接口）。
// 错误以 HTTP 200 返回在 Response.Error 中。账单月份按北京时间。

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ProviderTencentCN   = "tencent_cn"
	ProviderTencentIntl = "tencent_intl"
)

type tencentClient struct {
	cred    TencentCredentials
	intl    bool
	regions []string
	o       Options
}

var tencentVersions = map[string]string{"billing": "2018-07-09", "cvm": "2017-03-12", "lighthouse": "2020-03-24"}

func (c *tencentClient) endpoint(service string) string {
	if c.o.Endpoint != nil {
		return c.o.Endpoint(service, "")
	}
	if c.intl {
		return "https://" + service + ".intl.tencentcloudapi.com/"
	}
	return "https://" + service + ".tencentcloudapi.com/"
}

// call 调用接口；region 为空表示不区分区域的接口（计费、地域列表）。out 对应 Response 中的内容。
func (c *tencentClient) call(ctx context.Context, service, region, action string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint(service), bytes.NewReader(body))
	if err != nil {
		return err
	}
	SignTC3(req, body, c.cred, service, action, tencentVersions[service], region, time.Now())
	res, err := c.o.HTTP.Do(req)
	if err != nil {
		return err
	}
	b, err := readBody(res, func(status int, b []byte) *ProviderError {
		if pe := tencentError(b); pe != nil {
			pe.Status = status
			return pe
		}
		return &ProviderError{Status: status}
	})
	if err != nil {
		return err
	}
	if pe := tencentError(b); pe != nil {
		return pe
	}
	var wrap struct {
		Response json.RawMessage `json:"Response"`
	}
	if err := json.Unmarshal(b, &wrap); err != nil {
		return err
	}
	return json.Unmarshal(wrap.Response, out)
}

// tencentError 解析 {"Response": {"Error": {"Code", "Message"}}}；凭证或权限错误记为 401，停止自动同步。
func tencentError(b []byte) *ProviderError {
	var e struct {
		Response struct {
			Error *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"Response"`
	}
	if json.Unmarshal(b, &e) != nil || e.Response.Error == nil {
		return nil
	}
	pe := &ProviderError{Status: http.StatusBadRequest, Code: e.Response.Error.Code, Message: e.Response.Error.Message}
	if strings.HasPrefix(pe.Code, "AuthFailure") || strings.HasPrefix(pe.Code, "UnauthorizedOperation") {
		pe.Status = http.StatusUnauthorized
	}
	return pe
}

// ---- 费用 ----

// Costs 读取本月（北京时间）的总费用（RealTotalCost，折扣后）与可用余额。腾讯云没有费用预测接口。
func (c *tencentClient) Costs(ctx context.Context, now time.Time) (*Costs, error) {
	period := now.In(aliyunTZ).Format("2006-01")
	out := &Costs{Period: period, Currency: "CNY"}
	if c.intl {
		out.Currency = "USD"
	}
	var bill struct {
		SummaryTotal *struct {
			RealTotalCost string `json:"RealTotalCost"`
		} `json:"SummaryTotal"`
	}
	if err := c.call(ctx, "billing", "", "DescribeBillSummaryByProduct",
		map[string]string{"BeginTime": period, "EndTime": period}, &bill); err != nil {
		return nil, err
	}
	if bill.SummaryTotal != nil && bill.SummaryTotal.RealTotalCost != "" {
		v, err := toCents(bill.SummaryTotal.RealTotalCost)
		if err != nil {
			return nil, err
		}
		out.AmountCents = v
	}
	var bal struct {
		RealBalance *float64 `json:"RealBalance"`
		Balance     int64    `json:"Balance"`
	}
	if err := c.call(ctx, "billing", "", "DescribeAccountBalance", map[string]any{}, &bal); err != nil {
		// 余额需要单独授权：只是余额没有权限时照常返回费用，不把账户判为凭证失效（账单接口已验证过凭证）
		if IsAuthError(err) {
			return out, nil
		}
		return nil, err
	}
	// 余额单位为分
	v := bal.Balance
	if bal.RealBalance != nil {
		v = int64(*bal.RealBalance + 0.5)
		if *bal.RealBalance < 0 {
			v = int64(*bal.RealBalance - 0.5)
		}
	}
	out.BalanceCents = &v
	return out, nil
}

// ---- 实例 ----

func (c *tencentClient) wanted(region string) bool {
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

func (c *tencentClient) regionsOf(ctx context.Context, service string) ([]string, error) {
	var out struct {
		RegionSet []struct {
			Region      string `json:"Region"`
			RegionState string `json:"RegionState"`
		} `json:"RegionSet"`
	}
	if err := c.call(ctx, service, "", "DescribeRegions", map[string]any{}, &out); err != nil {
		return nil, err
	}
	var rs []string
	for _, r := range out.RegionSet {
		if (r.RegionState == "" || r.RegionState == "AVAILABLE") && c.wanted(r.Region) {
			rs = append(rs, r.Region)
		}
	}
	return rs, nil
}

// Instances 列出各区域的云服务器（CVM）与轻量应用服务器，区域并发查询（最多 4 个同时进行）。
func (c *tencentClient) Instances(ctx context.Context) ([]Instance, error) {
	cvmRegions, err := c.regionsOf(ctx, "cvm")
	if err != nil {
		return nil, err
	}
	lhRegions, err := c.regionsOf(ctx, "lighthouse")
	if err != nil {
		return nil, err
	}
	type job struct {
		region string
		fn     func(context.Context, string) ([]Instance, error)
	}
	var jobs []job
	for _, r := range cvmRegions {
		jobs = append(jobs, job{r, c.cvmInstances})
	}
	for _, r := range lhRegions {
		jobs = append(jobs, job{r, c.lighthouseInstances})
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

func (c *tencentClient) cvmInstances(ctx context.Context, region string) ([]Instance, error) {
	var all []Instance
	for offset := 0; offset < 100*100; offset += 100 {
		var out struct {
			TotalCount  int `json:"TotalCount"`
			InstanceSet []struct {
				ID         string   `json:"InstanceId"`
				Name       string   `json:"InstanceName"`
				Type       string   `json:"InstanceType"`
				State      string   `json:"InstanceState"`
				ChargeType string   `json:"InstanceChargeType"`
				Expired    string   `json:"ExpiredTime"`
				PublicIPs  []string `json:"PublicIpAddresses"`
				IPv6       []string `json:"IPv6Addresses"`
			} `json:"InstanceSet"`
		}
		if err := c.call(ctx, "cvm", region, "DescribeInstances", map[string]int{"Offset": offset, "Limit": 100}, &out); err != nil {
			return nil, err
		}
		for _, x := range out.InstanceSet {
			in := Instance{ID: x.ID, Name: x.Name, Region: region, Kind: "cvm", State: x.State, Plan: x.Type}
			if len(x.PublicIPs) > 0 {
				in.IPv4 = x.PublicIPs[0]
			}
			if len(x.IPv6) > 0 {
				in.IPv6 = x.IPv6[0]
			}
			if x.ChargeType == "PREPAID" {
				in.ExpireAt = parseCloudTime(x.Expired)
			}
			all = append(all, in)
		}
		if len(out.InstanceSet) < 100 || len(all) >= out.TotalCount {
			break
		}
	}
	return all, nil
}

func (c *tencentClient) lighthouseInstances(ctx context.Context, region string) ([]Instance, error) {
	var all []Instance
	for offset := 0; offset < 100*100; offset += 100 {
		var out struct {
			TotalCount  int `json:"TotalCount"`
			InstanceSet []struct {
				ID       string   `json:"InstanceId"`
				Name     string   `json:"InstanceName"`
				State    string   `json:"InstanceState"`
				Bundle   string   `json:"BundleId"`
				Expired  string   `json:"ExpiredTime"`
				Public   []string `json:"PublicAddresses"`
				PublicV6 []string `json:"PublicIpv6Addresses"`
			} `json:"InstanceSet"`
		}
		if err := c.call(ctx, "lighthouse", region, "DescribeInstances", map[string]int{"Offset": offset, "Limit": 100}, &out); err != nil {
			return nil, err
		}
		for _, x := range out.InstanceSet {
			in := Instance{ID: x.ID, Name: x.Name, Region: region, Kind: "lighthouse", State: x.State, Plan: x.Bundle,
				ExpireAt: parseCloudTime(x.Expired)}
			if len(x.Public) > 0 {
				in.IPv4 = x.Public[0]
			}
			if len(x.PublicV6) > 0 {
				in.IPv6 = x.PublicV6[0]
			}
			all = append(all, in)
		}
		if len(out.InstanceSet) < 100 || len(all) >= out.TotalCount {
			break
		}
	}
	return all, nil
}

// Traffic 为轻量应用服务器补充流量包的额度与用量（字节）。一台实例有多个流量包时合计；
// 周期起点取流量包的开始时间（北京时间日期），没有时取本月 1 日。
func (c *tencentClient) Traffic(ctx context.Context, insts []Instance, now time.Time) error {
	byRegion := map[string][]int{}
	for i := range insts {
		if insts[i].Kind == "lighthouse" {
			byRegion[insts[i].Region] = append(byRegion[insts[i].Region], i)
		}
	}
	local := now.In(aliyunTZ)
	monthStart := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, aliyunTZ).Format("2006-01-02")
	for region, idx := range byRegion {
		for len(idx) > 0 {
			n := min(100, len(idx))
			batch := idx[:n]
			idx = idx[n:]
			ids := make([]string, len(batch))
			for k, i := range batch {
				ids[k] = insts[i].ID
			}
			var out struct {
				Set []struct {
					ID       string `json:"InstanceId"`
					Packages []struct {
						Used      uint64 `json:"TrafficUsed"`
						Total     uint64 `json:"TrafficPackageTotal"`
						StartTime string `json:"StartTime"`
					} `json:"TrafficPackageSet"`
				} `json:"InstanceTrafficPackageSet"`
			}
			if err := c.call(ctx, "lighthouse", region, "DescribeInstancesTrafficPackages",
				map[string]any{"InstanceIds": ids, "Limit": 100}, &out); err != nil {
				return err
			}
			for _, s := range out.Set {
				var used, total uint64
				start := ""
				for _, p := range s.Packages {
					used += p.Used
					total += p.Total
					if t := parseCloudTime(p.StartTime); t > 0 && start == "" {
						start = time.Unix(t, 0).In(aliyunTZ).Format("2006-01-02")
					}
				}
				if start == "" {
					start = monthStart
				}
				for _, i := range batch {
					if insts[i].ID == s.ID {
						insts[i].TrafficUsed, insts[i].TrafficLimit, insts[i].TrafficPeriodStart = used, total, start
					}
				}
			}
		}
	}
	return nil
}
