package cloud

// Oracle Cloud（OCI）客户端（设计 44.3）：只用到下列只读接口（HTTP 签名，ocisign.go）。
//
//	身份 identity 20160918   regionSubscriptions（已订阅区域）、compartments（全部区间）
//	计算 iaas 20160918       instances、vnicAttachments、vnics（公网 IP）
//	用量 usageapi 20200107   usage：本月费用（COST）与出站数据量（USAGE）
//
// 出站流量按租户每月计 10 TB 免费额度，不属于某台实例：以一条 kind=oci_egress 的“实例”表示，
// 复用流量包的显示与 90% / 95% 提醒。最小权限：
//
//	Allow group <组> to read usage-reports in tenancy
//	Allow group <组> to inspect compartments in tenancy
//	Allow group <组> to read instance-family in tenancy
//	Allow group <组> to read virtual-network-family in tenancy

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ProviderOCI = "oci"
	// ociFreeEgress 是 OCI 每月免费的出站流量（10 TB，按 10¹² 字节计）
	ociFreeEgress = 10_000_000_000_000
)

type ociClient struct {
	cred    OCICredentials
	key     *rsa.PrivateKey
	keyID   string
	regions []string
	o       Options
}

func newOCIClient(c OCICredentials, regions []string, o Options) (*ociClient, error) {
	if c.TenancyOCID == "" || c.UserOCID == "" || c.Fingerprint == "" || c.PrivateKey == "" || c.Region == "" {
		return nil, errors.New("Oracle Cloud 凭证不完整")
	}
	k, err := ParseOCIKey(c.PrivateKey)
	if err != nil {
		return nil, err
	}
	return &ociClient{cred: c, key: k, keyID: c.TenancyOCID + "/" + c.UserOCID + "/" + c.Fingerprint, regions: regions, o: o}, nil
}

func (c *ociClient) base(service, region string) string {
	if c.o.Endpoint != nil {
		return c.o.Endpoint(service, region)
	}
	if service == "usageapi" {
		return "https://usageapi." + region + ".oci.oraclecloud.com/20200107/"
	}
	return "https://" + service + "." + region + ".oraclecloud.com/20160918/"
}

// call 发送签名请求并解析 JSON；返回 opc-next-page（分页）。
func (c *ociClient) call(ctx context.Context, method, service, region, path string, q url.Values, in, out any) (string, error) {
	u := c.base(service, region) + path
	if len(q) > 0 {
		u += "?" + strings.ReplaceAll(q.Encode(), "+", "%20")
	}
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return "", err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	if err := SignOCI(req, body, c.keyID, c.key, time.Now()); err != nil {
		return "", err
	}
	res, err := c.o.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	next := res.Header.Get("opc-next-page")
	b, err := readBody(res, ociError)
	if err != nil {
		return "", err
	}
	return next, json.Unmarshal(b, out)
}

// ociError 解析 {"code": "NotAuthenticated", "message": "…"}。
func ociError(status int, b []byte) *ProviderError {
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(b, &e)
	return &ProviderError{Status: status, Code: e.Code, Message: e.Message}
}

// list 读取分页接口的全部结果（最多 50 页）。
func ociList[T any](ctx context.Context, c *ociClient, service, region, path string, q url.Values) ([]T, error) {
	var all []T
	for page := 0; page < 50; page++ {
		var items []T
		next, err := c.call(ctx, http.MethodGet, service, region, path, q, nil, &items)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if next == "" {
			break
		}
		q.Set("page", next)
	}
	return all, nil
}

// ---- 费用与出站流量（Usage API，主区域） ----

type ociUsageItem struct {
	ComputedAmount   *float64 `json:"computedAmount"`
	ComputedQuantity *float64 `json:"computedQuantity"`
	Currency         string   `json:"currency"`
	SkuName          string   `json:"skuName"`
	Unit             string   `json:"unit"`
}

func (c *ociClient) usage(ctx context.Context, now time.Time, queryType string, groupBy []string) ([]ociUsageItem, error) {
	start := monthStart(now)
	in := map[string]any{
		"tenantId":         c.cred.TenancyOCID,
		"timeUsageStarted": start.Format(time.RFC3339),
		"timeUsageEnded":   start.AddDate(0, 1, 0).Format(time.RFC3339),
		"granularity":      "MONTHLY",
		"queryType":        queryType,
	}
	if len(groupBy) > 0 {
		in["groupBy"] = groupBy
	}
	var out struct {
		Items []ociUsageItem `json:"items"`
	}
	if _, err := c.call(ctx, http.MethodPost, "usageapi", c.cred.Region, "usage", nil, in, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// Costs 读取本月（UTC 自然月）已产生的费用。OCI 没有余额（按量计费），这里也不调用费用预测。
func (c *ociClient) Costs(ctx context.Context, now time.Time) (*Costs, error) {
	items, err := c.usage(ctx, now, "COST", nil)
	if err != nil {
		return nil, err
	}
	out := &Costs{Period: monthStart(now).Format("2006-01"), Currency: "USD"}
	var sum float64
	for _, it := range items {
		if it.ComputedAmount != nil {
			sum += *it.ComputedAmount
		}
		if it.Currency != "" {
			out.Currency = it.Currency
		}
	}
	out.AmountCents, _ = toCents(strconvFloat(sum))
	return out, nil
}

// ---- 实例 ----

func (c *ociClient) wanted(region string) bool {
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

// Instances 列出全部已订阅区域、全部区间的计算实例（不含已终止），并附上出站流量的统计项。
func (c *ociClient) Instances(ctx context.Context) ([]Instance, error) {
	subs, err := ociList[struct {
		RegionName string `json:"regionName"`
		Status     string `json:"status"`
	}](ctx, c, "identity", c.cred.Region, "tenancies/"+c.cred.TenancyOCID+"/regionSubscriptions", url.Values{})
	if err != nil {
		return nil, err
	}
	comps, err := ociList[struct {
		ID string `json:"id"`
	}](ctx, c, "identity", c.cred.Region, "compartments", url.Values{"compartmentId": {c.cred.TenancyOCID},
		"compartmentIdInSubtree": {"true"}, "accessLevel": {"ACCESSIBLE"}, "lifecycleState": {"ACTIVE"}, "limit": {"1000"}})
	if err != nil {
		return nil, err
	}
	compIDs := []string{c.cred.TenancyOCID} // 根区间就是租户本身
	for _, x := range comps {
		compIDs = append(compIDs, x.ID)
	}
	var (
		mu       sync.Mutex
		all      []Instance
		firstErr error
		wg       sync.WaitGroup
		sem      = make(chan struct{}, 4)
	)
	for _, sub := range subs {
		if sub.Status != "" && sub.Status != "READY" || !c.wanted(sub.RegionName) {
			continue
		}
		for _, comp := range compIDs {
			wg.Add(1)
			go func(region, comp string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				got, err := c.instancesIn(ctx, region, comp)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					return
				}
				all = append(all, got...)
			}(sub.RegionName, comp)
		}
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	sort.Slice(all, func(i, k int) bool { return all[i].ID < all[k].ID })
	return append(all, Instance{ID: "egress", Name: "出站流量（每月 10 TB 免费）", Region: c.cred.Region,
		Kind: "oci_egress", State: "-", TrafficLimit: ociFreeEgress}), nil
}

func (c *ociClient) instancesIn(ctx context.Context, region, comp string) ([]Instance, error) {
	insts, err := ociList[struct {
		ID    string `json:"id"`
		Name  string `json:"displayName"`
		Shape string `json:"shape"`
		State string `json:"lifecycleState"`
	}](ctx, c, "iaas", region, "instances", url.Values{"compartmentId": {comp}, "limit": {"1000"}})
	if err != nil || len(insts) == 0 {
		return nil, err
	}
	atts, err := ociList[struct {
		InstanceID string `json:"instanceId"`
		VnicID     string `json:"vnicId"`
		State      string `json:"lifecycleState"`
	}](ctx, c, "iaas", region, "vnicAttachments", url.Values{"compartmentId": {comp}, "limit": {"1000"}})
	if err != nil {
		return nil, err
	}
	vnicOf := map[string]string{}
	for _, a := range atts {
		if a.State == "ATTACHED" && vnicOf[a.InstanceID] == "" {
			vnicOf[a.InstanceID] = a.VnicID
		}
	}
	var out []Instance
	for _, x := range insts {
		if x.State == "TERMINATED" {
			continue
		}
		in := Instance{ID: x.ID, Name: x.Name, Region: region, Kind: "oci", State: x.State, Plan: x.Shape}
		if v := vnicOf[x.ID]; v != "" {
			var vnic struct {
				PublicIP string   `json:"publicIp"`
				IPv6     []string `json:"ipv6Addresses"`
			}
			if _, err := c.call(ctx, http.MethodGet, "iaas", region, "vnics/"+v, nil, nil, &vnic); err == nil {
				in.IPv4 = vnic.PublicIP
				if len(vnic.IPv6) > 0 {
					in.IPv6 = vnic.IPv6[0]
				}
			}
		}
		out = append(out, in)
	}
	return out, nil
}

// Traffic 补充本月（UTC）租户的出站数据量：Usage API 中 skuName 含 “Outbound Data Transfer” 的用量（GB）。
func (c *ociClient) Traffic(ctx context.Context, insts []Instance, now time.Time) error {
	idx := -1
	for i := range insts {
		if insts[i].Kind == "oci_egress" {
			idx = i
		}
	}
	if idx < 0 {
		return nil
	}
	items, err := c.usage(ctx, now, "USAGE", []string{"skuName", "unit"})
	if err != nil {
		return err
	}
	var gb float64
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.SkuName), "outbound data transfer") && it.ComputedQuantity != nil {
			gb += *it.ComputedQuantity
		}
	}
	insts[idx].TrafficUsed = uint64(gb * 1e9)
	insts[idx].TrafficLimit = ociFreeEgress
	insts[idx].TrafficPeriodStart = monthStart(now).Format("2006-01-02")
	return nil
}
