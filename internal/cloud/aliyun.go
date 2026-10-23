package cloud

// 阿里云客户端（设计 44.3）：国内站（aliyun.com）与国际站（alibabacloud.com）只是费用中心的接入点与币种不同，
// ECS、轻量应用服务器的接口地址两站相同。只用到下列只读接口（RPC 风格，参数在查询串中，V3 签名）：
//
//	费用中心 BSS 2017-12-14   QueryAccountBalance、QueryBillOverview
//	ECS 2014-05-26            DescribeRegions、DescribeInstances（ExpiredTime 为包年包月到期时间）
//	轻量应用服务器 2020-06-01  ListRegions、ListInstances、ListInstancesTrafficPackages
//	云数据传输 CDT 2021-08-13 ListCdtInternetTraffic（本月公网流量，按地域，修订第 181 条）
//
// 最小权限：AliyunBSSReadOnlyAccess、AliyunECSReadOnlyAccess、AliyunSWASReadOnlyAccess；
// CDT 流量另需 AliyunCDTReadOnlyAccess（或 cdt:ListCdtInternetTraffic），没有时只是不显示 CDT 流量。
// 走 CDT 的实例本身是 ECS 实例：没有 AliyunECSReadOnlyAccess 时列不出来（修订第 202 条）。
// 账单月份按北京时间（UTC+8）划分。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// 阿里云国内站与国际站的账户类型
const (
	ProviderAliyunCN   = "aliyun_cn"
	ProviderAliyunIntl = "aliyun_intl"
)

// 阿里云账单与流量包按北京时间的自然月计算
var aliyunTZ = time.FixedZone("UTC+8", 8*3600)

type aliyunClient struct {
	cred    AliyunCredentials
	intl    bool
	regions []string
	known   map[string]bool // 本轮区域列表中出现过的区域 ID：找出账户设置中不存在的区域（修订第 209 条）
	o       Options
}

// host 返回接口地址。service：business（费用中心）、ecs、swas；region 为空时用中心接入点。
func (c *aliyunClient) host(service, region string) string {
	if c.o.Endpoint != nil {
		return c.o.Endpoint(service, region)
	}
	switch service {
	case "business":
		if c.intl {
			return "https://business.ap-southeast-1.aliyuncs.com/"
		}
		return "https://business.aliyuncs.com/"
	case "ecs":
		if region == "" {
			region = c.homeRegion()
		}
		return "https://ecs." + region + ".aliyuncs.com/"
	case "cdt":
		return "https://cdt.aliyuncs.com/"
	default: // swas
		if region == "" {
			region = c.homeRegion()
		}
		return "https://swas." + region + ".aliyuncs.com/"
	}
}

// homeRegion 是查询区域列表等不分区域的调用所用的接入点：国内站用杭州，国际站用新加坡。
// 国际站账号在国内站的接入点（如 swas.cn-hangzhou）上调用会失败，之前因此读不到国际站的实例。
func (c *aliyunClient) homeRegion() string {
	if c.intl {
		return "ap-southeast-1"
	}
	return "cn-hangzhou"
}

var aliyunVersions = map[string]string{"business": "2017-12-14", "ecs": "2014-05-26", "swas": "2020-06-01", "cdt": "2021-08-13"}

// call 调用 RPC 风格接口：POST，参数在查询串中，响应为 JSON。
func (c *aliyunClient) call(ctx context.Context, service, region, action string, params url.Values, out any) error {
	u := c.host(service, region)
	if len(params) > 0 {
		u += "?" + strings.ReplaceAll(params.Encode(), "+", "%20")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", u, nil)
	if err != nil {
		return err
	}
	SignACS3(req, nil, c.cred, action, aliyunVersions[service], time.Now(), "")
	res, err := c.o.HTTP.Do(req)
	if err != nil {
		return err
	}
	b, err := readBody(res, aliyunError)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// aliyunError 解析错误响应：{"RequestId": "…", "Code": "InvalidAccessKeyId.NotFound", "Message": "…"}
func aliyunError(status int, b []byte) *ProviderError {
	var e struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	_ = json.Unmarshal(b, &e)
	return &ProviderError{Status: status, Code: e.Code, Message: e.Message}
}

// ---- 费用 ----

// Costs 读取本月（北京时间）账单概览的应付金额（税前，已扣优惠）与账户可用余额。阿里云没有费用预测接口。
func (c *aliyunClient) Costs(ctx context.Context, now time.Time) (*Costs, error) {
	period := now.In(aliyunTZ).Format("2006-01")
	var bill struct {
		Data struct {
			Items struct {
				Item []struct {
					PretaxAmount float64 `json:"PretaxAmount"`
					Currency     string  `json:"Currency"`
				} `json:"Item"`
			} `json:"Items"`
		} `json:"Data"`
	}
	if err := c.call(ctx, "business", "", "QueryBillOverview", url.Values{"BillingCycle": {period}}, &bill); err != nil {
		return nil, err
	}
	out := &Costs{Period: period, Currency: "CNY"}
	if c.intl {
		out.Currency = "USD"
	}
	var sum float64
	for _, it := range bill.Data.Items.Item {
		sum += it.PretaxAmount
		if it.Currency != "" {
			out.Currency = it.Currency
		}
	}
	out.AmountCents, _ = toCents(strconv.FormatFloat(sum, 'f', 4, 64))

	var bal struct {
		Data struct {
			AvailableAmount string `json:"AvailableAmount"`
			Currency        string `json:"Currency"`
		} `json:"Data"`
	}
	if err := c.call(ctx, "business", "", "QueryAccountBalance", nil, &bal); err != nil {
		return nil, err
	}
	if bal.Data.AvailableAmount != "" {
		v, err := toCents(strings.ReplaceAll(bal.Data.AvailableAmount, ",", ""))
		if err != nil {
			return nil, err
		}
		out.BalanceCents = &v
		if bal.Data.Currency != "" && len(bill.Data.Items.Item) == 0 {
			out.Currency = bal.Data.Currency
		}
	}
	return out, nil
}

// ---- 实例 ----

// Instances 列出各区域的 ECS 与轻量应用服务器实例，区域并发查询（最多 4 个同时进行）。
// 子用户只授予了其中一项服务的只读权限时（如只用 ECS + CDT、没有轻量应用服务器权限），另一项整体跳过并给出提示，
// 不当作凭证失效（修订第 184 条）；两项都没有权限时才是账户级错误。
func (c *aliyunClient) Instances(ctx context.Context) ([]Instance, error) {
	ecsRegions, ecsErr := c.ecsRegions(ctx)
	if ecsErr != nil && !permissionDenied(ecsErr) {
		return nil, ecsErr
	}
	swasRegions, swasErr := c.swasRegions(ctx)
	if swasErr != nil && !permissionDenied(swasErr) {
		return nil, swasErr
	}
	if ecsErr != nil && swasErr != nil {
		return nil, ecsErr
	}
	var denied []RegionError
	if bad := c.unknownRegions(); len(bad) > 0 {
		denied = append(denied, unknownRegionError(bad, "cn-hongkong"))
	}
	if ecsErr != nil {
		denied = append(denied, RegionError{Kind: "ecs", Label: "ECS", Err: ecsErr,
			Hint: "授予 AliyunECSReadOnlyAccess 后才能列出 ECS 实例，包括按流量计费、走 CDT 的实例"})
	}
	if swasErr != nil {
		denied = append(denied, RegionError{Kind: "swas", Label: "轻量应用服务器", Err: swasErr,
			Hint: "需要时授予 AliyunSWASReadOnlyAccess（不用轻量应用服务器可以忽略）"})
	}
	var jobs []regionJob
	for _, r := range ecsRegions {
		jobs = append(jobs, regionJob{"ecs", "ECS", r, c.ecsInstances})
	}
	for _, r := range swasRegions {
		jobs = append(jobs, regionJob{"swas", "轻量应用服务器", r, c.swasInstances})
	}
	// 单个区域失败只跳过该区域（修订第 141 条）
	all, err := runRegionJobs(ctx, jobs)
	sortInstances(all)
	if len(denied) > 0 {
		var pe *PartialError
		switch {
		case err == nil:
			err = &PartialError{Skipped: denied}
		case errors.As(err, &pe):
			pe.Skipped = append(denied, pe.Skipped...)
		}
	}
	return partialOrFail(all, err)
}

func (c *aliyunClient) wanted(region string) bool {
	if c.known == nil {
		c.known = map[string]bool{}
	}
	c.known[region] = true
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

func (c *aliyunClient) ecsRegions(ctx context.Context) ([]string, error) {
	var out struct {
		Regions struct {
			Region []struct {
				RegionID string `json:"RegionId"`
			} `json:"Region"`
		} `json:"Regions"`
	}
	if err := c.call(ctx, "ecs", "", "DescribeRegions", nil, &out); err != nil {
		return nil, err
	}
	var rs []string
	for _, r := range out.Regions.Region {
		if c.wanted(r.RegionID) {
			rs = append(rs, r.RegionID)
		}
	}
	return rs, nil
}

// unknownRegions 返回账户设置的区域中，本轮区域列表里没有的（如误填了实例名称）；没有取得区域列表时返回空。
func (c *aliyunClient) unknownRegions() []string {
	if len(c.known) == 0 {
		return nil
	}
	var bad []string
	for _, r := range c.regions {
		if !c.known[r] {
			bad = append(bad, r)
		}
	}
	return bad
}

// parseCloudTime 解析云厂商的时间（阿里云、腾讯云）：ECS 为 “2017-12-10T04:04Z”，轻量应用服务器为 ISO 8601（可能带毫秒、时区不带冒号）。
func parseCloudTime(s string) int64 {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04Z", "2006-01-02T15:04:05.000-0700",
		"2006-01-02T15:04:05-0700", "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Unix()
		}
	}
	return 0
}

func (c *aliyunClient) ecsInstances(ctx context.Context, region string) ([]Instance, error) {
	var all []Instance
	token := ""
	for page := 0; page < 100; page++ {
		p := url.Values{"RegionId": {region}, "MaxResults": {"100"}}
		if token != "" {
			p.Set("NextToken", token)
		}
		var out struct {
			Instances struct {
				Instance []struct {
					ID          string `json:"InstanceId"`
					Name        string `json:"InstanceName"`
					Type        string `json:"InstanceType"`
					Status      string `json:"Status"`
					ChargeType  string `json:"InstanceChargeType"`
					ExpiredTime string `json:"ExpiredTime"`
					// 公网带宽计费方式：PayByTraffic（按流量，走 CDT）/ PayByBandwidth（固定带宽）
					NetCharge string `json:"InternetChargeType"`
					PublicIP  struct {
						IP []string `json:"IpAddress"`
					} `json:"PublicIpAddress"`
					EIP struct {
						IP         string `json:"IpAddress"`
						ChargeType string `json:"InternetChargeType"`
					} `json:"EipAddress"`
					NICs struct {
						NIC []struct {
							IPv6 struct {
								Set []struct {
									Addr string `json:"Ipv6Address"`
								} `json:"Ipv6Set"`
							} `json:"Ipv6Sets"`
						} `json:"NetworkInterface"`
					} `json:"NetworkInterfaces"`
				} `json:"Instance"`
			} `json:"Instances"`
			NextToken string `json:"NextToken"`
		}
		if err := c.call(ctx, "ecs", region, "DescribeInstances", p, &out); err != nil {
			return nil, err
		}
		for _, x := range out.Instances.Instance {
			in := Instance{ID: x.ID, Name: x.Name, Region: region, Kind: "ecs", State: x.Status, Plan: x.Type}
			if len(x.PublicIP.IP) > 0 {
				in.IPv4 = x.PublicIP.IP[0]
			} else {
				in.IPv4 = x.EIP.IP
			}
			for _, nic := range x.NICs.NIC {
				if len(nic.IPv6.Set) > 0 && in.IPv6 == "" {
					in.IPv6 = nic.IPv6.Set[0].Addr
				}
			}
			// CDT（修订第 186 条）：固定公网 IP 按流量计费，或绑定的 EIP 按流量计费
			if (len(x.PublicIP.IP) > 0 && x.NetCharge == "PayByTraffic") || (x.EIP.IP != "" && x.EIP.ChargeType == "PayByTraffic") {
				in.CDTPool = cdtPoolKey(region)
			}
			// 只有包年包月有到期时间；按量付费的 ExpiredTime 是很远的占位值
			if x.ChargeType == "PrePaid" {
				in.ExpireAt = parseCloudTime(x.ExpiredTime)
			}
			all = append(all, in)
		}
		if token = out.NextToken; token == "" {
			break
		}
	}
	return all, nil
}

func (c *aliyunClient) swasRegions(ctx context.Context) ([]string, error) {
	var out struct {
		Regions []struct {
			RegionID string `json:"RegionId"`
		} `json:"Regions"`
	}
	if err := c.call(ctx, "swas", "", "ListRegions", nil, &out); err != nil {
		return nil, err
	}
	var rs []string
	for _, r := range out.Regions {
		if c.wanted(r.RegionID) {
			rs = append(rs, r.RegionID)
		}
	}
	return rs, nil
}

func (c *aliyunClient) swasInstances(ctx context.Context, region string) ([]Instance, error) {
	var all []Instance
	for page := 1; page <= 100; page++ {
		var out struct {
			Instances []struct {
				ID          string `json:"InstanceId"`
				Name        string `json:"InstanceName"`
				Status      string `json:"Status"`
				PublicIP    string `json:"PublicIpAddress"`
				ExpiredTime string `json:"ExpiredTime"`
				PlanID      string `json:"PlanId"`
			} `json:"Instances"`
			TotalCount int `json:"TotalCount"`
		}
		p := url.Values{"RegionId": {region}, "PageSize": {"100"}, "PageNumber": {strconv.Itoa(page)}}
		if err := c.call(ctx, "swas", region, "ListInstances", p, &out); err != nil {
			return nil, err
		}
		for _, x := range out.Instances {
			all = append(all, Instance{ID: x.ID, Name: x.Name, Region: region, Kind: "swas", State: x.Status,
				IPv4: x.PublicIP, Plan: x.PlanID, ExpireAt: parseCloudTime(x.ExpiredTime)})
		}
		if len(out.Instances) < 100 || len(all) >= out.TotalCount {
			break
		}
	}
	return all, nil
}

// Traffic 为轻量应用服务器补充本月流量包的额度与用量（字节，北京时间自然月）。按区域每次最多 100 台查询。
func (c *aliyunClient) Traffic(ctx context.Context, insts []Instance, now time.Time) error {
	byRegion := map[string][]int{}
	for i := range insts {
		if insts[i].Kind == "swas" {
			byRegion[insts[i].Region] = append(byRegion[insts[i].Region], i)
		}
	}
	start := now.In(aliyunTZ)
	period := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, aliyunTZ).Format("2006-01-02")
	for region, idx := range byRegion {
		for len(idx) > 0 {
			n := min(100, len(idx))
			batch := idx[:n]
			idx = idx[n:]
			ids := make([]string, len(batch))
			for k, i := range batch {
				ids[k] = insts[i].ID
			}
			idsJSON, _ := json.Marshal(ids)
			var out struct {
				Usages []struct {
					ID    string `json:"InstanceId"`
					Used  uint64 `json:"TrafficUsed"`
					Total uint64 `json:"TrafficPackageTotal"`
				} `json:"InstanceTrafficPackageUsages"`
			}
			if err := c.call(ctx, "swas", region, "ListInstancesTrafficPackages",
				url.Values{"RegionId": {region}, "InstanceIds": {string(idsJSON)}}, &out); err != nil {
				return err
			}
			for _, u := range out.Usages {
				for _, i := range batch {
					if insts[i].ID == u.ID {
						insts[i].TrafficUsed, insts[i].TrafficLimit, insts[i].TrafficPeriodStart = u.Used, u.Total, period
					}
				}
			}
		}
	}
	return nil
}

// ---- CDT 流量（修订第 181 条） ----

// CDT 公网流量免费额度（自 2025-06-01 起，按阿里云账号每自然月计算，不结转）：中国内地 20 GB、非中国内地 200 GB。
// 只有按流量计费的 BGP（多线）流量享受，BGP（多线）_精品不享受。
const (
	cdtFreeCN   uint64 = 20 << 30
	cdtFreeIntl uint64 = 200 << 30
)

// cdtPoolKey 返回地域所属的 CDT 免费额度池（cdt_cn / cdt_intl）。
func cdtPoolKey(region string) string {
	if cdtMainland(region) {
		return "cdt_cn"
	}
	return "cdt_intl"
}

// cdtMainland 判断地域是否属于中国内地：cn- 开头且不是中国香港（CDT 口径中香港属于非中国内地）。
func cdtMainland(region string) bool {
	return strings.HasPrefix(region, "cn-") && region != "cn-hongkong"
}

// TrafficPools 读取本月 CDT 公网流量（ListCdtInternetTraffic，按地域与线路类型），汇总为中国内地与非中国内地两个免费额度池。
// 本月没有任何 CDT 流量（未开通 CDT 或实例为固定带宽计费）时返回空。
func (c *aliyunClient) TrafficPools(ctx context.Context, now time.Time) ([]TrafficPool, error) {
	var out struct {
		TrafficDetails []struct {
			Region  string `json:"BusinessRegionId"`
			ISPType string `json:"ISPType"`
			Traffic uint64 `json:"Traffic"`
		} `json:"TrafficDetails"`
	}
	if err := c.call(ctx, "cdt", "", "ListCdtInternetTraffic", nil, &out); err != nil {
		return nil, err
	}
	if len(out.TrafficDetails) == 0 {
		return nil, nil
	}
	start := now.In(aliyunTZ)
	period := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, aliyunTZ).Format("2006-01-02")
	cn := TrafficPool{Key: "cdt_cn", Name: "CDT 中国内地", LimitBytes: cdtFreeCN, PeriodStart: period}
	intl := TrafficPool{Key: "cdt_intl", Name: "CDT 非中国内地", LimitBytes: cdtFreeIntl, PeriodStart: period}
	var hasCN, hasIntl bool
	for _, d := range out.TrafficDetails {
		p, has := &intl, &hasIntl
		if cdtMainland(d.Region) {
			p, has = &cn, &hasCN
		}
		*has = true
		// BGP_PRO（精品线路）不享受免费额度，不计入用量；线路类型为空时按 BGP 计
		if d.ISPType == "" || strings.EqualFold(d.ISPType, "BGP") {
			p.UsedBytes += d.Traffic
		}
	}
	var pools []TrafficPool
	if hasCN {
		pools = append(pools, cn)
	}
	if hasIntl {
		pools = append(pools, intl)
	}
	return pools, nil
}
