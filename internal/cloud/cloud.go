package cloud

// 各家客户端的公共部分：账户、费用与实例的数据结构，HTTP 请求与错误（设计 44.3、44.9）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 服务商（cloud_accounts.provider）
const (
	ProviderAWS = "aws"
	// 阿里云见 aliyun.go；腾讯云、Oracle Cloud 随第三、四步实现（设计 44.10）
)

// Providers 是当前已实现的服务商。
var Providers = []string{ProviderAWS, ProviderAliyunCN, ProviderAliyunIntl}

// Costs 是一个账户本月的费用（设计 44.7 cloud_costs）。金额以“分”为整数；没有的项为 nil。
type Costs struct {
	Period        string // YYYY-MM（服务商账单的月份，AWS 为 UTC）
	AmountCents   int64  // 本月已产生
	ForecastCents *int64 // 本月预估（已产生 + 预测的剩余部分）；数据不足时没有
	BalanceCents  *int64 // 账户余额（预付费账户）；AWS 没有
	Currency      string
}

// Instance 是一台云主机（设计 44.7 cloud_instances）。
type Instance struct {
	ID       string // 服务商内唯一：EC2 为 i-…，Lightsail 为实例名（同一区域内唯一）加区域
	Name     string
	Region   string
	Kind     string // ec2 / lightsail / …
	State    string // running / stopped / …（服务商原样）
	IPv4     string
	IPv6     string
	Plan     string // 规格：EC2 实例类型、Lightsail 套餐
	ExpireAt int64  // 到期时间，Unix 秒；按需付费为 0
	// 流量包（Lightsail 等）：额度为 0 表示没有流量包
	TrafficLimit       uint64
	TrafficUsed        uint64
	TrafficPeriodStart string // YYYY-MM-DD
}

// Client 读取一个账户的数据；每个方法都只调用只读接口。
type Client interface {
	Costs(ctx context.Context, now time.Time) (*Costs, error)
	Instances(ctx context.Context) ([]Instance, error)
	// Traffic 为有流量包的实例补充本周期额度与用量（原地修改）；传入账户的全部实例，由各家自行挑选
	Traffic(ctx context.Context, insts []Instance, now time.Time) error
}

// Options 用于测试替换 HTTP 客户端与接口地址。
type Options struct {
	HTTP     *http.Client
	Endpoint func(service, region string) string // 为 nil 时用官方地址
}

// NewClient 按服务商与凭证（JSON）创建客户端。
func NewClient(provider string, cred []byte, regions []string, o Options) (Client, error) {
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	switch provider {
	case ProviderAWS:
		var c AWSCredentials
		if err := json.Unmarshal(cred, &c); err != nil || c.AccessKeyID == "" || c.SecretAccessKey == "" {
			return nil, errors.New("AWS 凭证不完整")
		}
		return &awsClient{cred: c, regions: regions, o: o}, nil
	case ProviderAliyunCN, ProviderAliyunIntl:
		var c AliyunCredentials
		if err := json.Unmarshal(cred, &c); err != nil || c.AccessKeyID == "" || c.AccessKeySecret == "" {
			return nil, errors.New("阿里云凭证不完整")
		}
		return &aliyunClient{cred: c, intl: provider == ProviderAliyunIntl, regions: regions, o: o}, nil
	}
	return nil, fmt.Errorf("不支持的服务商 %q", provider)
}

// ProviderError 是云厂商返回的错误。只保留状态码、错误码与说明，不含请求签名（设计 44.2）。
type ProviderError struct {
	Status  int
	Code    string
	Message string
}

func (e *ProviderError) Error() string {
	msg := e.Message
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	if e.Code != "" {
		return fmt.Sprintf("%s（HTTP %d）：%s", e.Code, e.Status, msg)
	}
	return fmt.Sprintf("HTTP %d：%s", e.Status, msg)
}

// authCodes 是表示凭证无效或权限不足的错误码（各家）；遇到时停止自动同步，提示更新凭证（设计 44.4）。
var authCodes = map[string]bool{
	"UnrecognizedClientException": true, "InvalidClientTokenId": true, "AuthFailure": true,
	"SignatureDoesNotMatch": true, "ExpiredToken": true, "ExpiredTokenException": true,
	"AccessDenied": true, "AccessDeniedException": true, "UnauthorizedOperation": true,
	"InvalidSignatureException": true, "IncompleteSignature": true, "MissingAuthenticationToken": true,
	// 阿里云
	"InvalidAccessKeyId.NotFound": true, "InvalidAccessKeyId.Inactive": true, "InvalidAccessKeyId": true,
	"Forbidden.RAM": true, "Forbidden.AccessKeyDisabled": true, "NoPermission": true, "Forbidden": true,
}

// IsAuthError 判断错误是否为凭证失效 / 权限不足。
func IsAuthError(err error) bool {
	var pe *ProviderError
	if !errors.As(err, &pe) {
		return false
	}
	return pe.Status == http.StatusUnauthorized || authCodes[pe.Code] ||
		(pe.Status == http.StatusForbidden && !strings.Contains(strings.ToLower(pe.Code), "throttl"))
}

const maxResponse = 8 << 20 // 响应体上限：实例很多的账户 DescribeInstances 也在此范围内

// readBody 读取响应体（有上限）；非 2xx 时交给 parseErr 解析为 ProviderError。
func readBody(res *http.Response, parseErr func(status int, body []byte) *ProviderError) ([]byte, error) {
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxResponse {
		return nil, errors.New("云厂商响应过大")
	}
	if res.StatusCode/100 != 2 {
		return nil, parseErr(res.StatusCode, b)
	}
	return b, nil
}

// toCents 把十进制金额字符串（如 "12.3456"）四舍五入为分。
func toCents(s string) (int64, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("金额格式不正确：%q", s)
	}
	return int64(math.Round(f * 100)), nil
}

// BillingMonth 返回服务商账单口径下 now 所在的月份（YYYY-MM）：AWS 为 UTC，阿里云为北京时间。
func BillingMonth(provider string, now time.Time) string {
	switch provider {
	case ProviderAliyunCN, ProviderAliyunIntl:
		return now.In(aliyunTZ).Format("2006-01")
	}
	return now.UTC().Format("2006-01")
}

// monthStart 返回 t 所在月（UTC）的第一天。
func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
