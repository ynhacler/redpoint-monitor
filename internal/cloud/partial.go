package cloud

// 单个区域失败的隔离（设计 44.4，修订第 141 条）。错误分两级：
//
//	账户级  主区域上的第一个调用（如 AWS 在 us-east-1 列出区域）失败：凭证或权限问题，整轮同步失败，
//	        按 IsAuthError 判断是否停止自动同步
//	区域级  某个区域的调用失败（多为账户没有开通该区域，或偶发错误）：重试一次后仍失败则跳过该区域，
//	        其他区域照常同步，返回部分结果与 *PartialError；面板保留被跳过区域原有的实例，并把说明显示为警告
//
// 例外：同一服务在所有区域都因“权限不足”被拒绝（如 IAM 缺少 ec2:DescribeInstances，返回 UnauthorizedOperation /
// AccessDenied），这是权限问题，按账户级处理。区域内的“令牌无效”（UnrecognizedClientException 等）不算：
// 主区域已用同一把密钥调用成功，区域内的令牌无效说明该区域没有开通，不是凭证问题。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// RegionError 是一个区域、一类实例的调用失败。
type RegionError struct {
	Kind   string // 实例类型，与 Instance.Kind 相同（如 ec2、lightsail）
	Label  string // 显示名，如 “Lightsail”
	Region string
	Err    error
}

// PartialError 表示部分区域被跳过：同时返回的实例列表是其他区域的完整结果。
// 【注意】不实现 Unwrap：区域内的凭证类错误（如未开通区域返回的 UnrecognizedClientException）
// 不能让 IsAuthError 把整个账户判成凭证失效。
type PartialError struct {
	Skipped []RegionError
}

func (e *PartialError) Error() string {
	parts := make([]string, 0, len(e.Skipped))
	for _, r := range e.Skipped {
		parts = append(parts, r.describe())
	}
	msg := strings.Join(parts, "；")
	if len(msg) > 900 {
		msg = msg[:900] + "…"
	}
	return msg
}

// 区域级错误的几种原因（修订第 141 条），说明分别给出，避免把排查带偏。
var (
	// 账户正在开通该区域（AWS Lightsail 首次使用某区域时）：过一会儿自然恢复
	settingUpCodes = map[string]bool{"RegionSetupInProgressException": true}
	// 账户没有开通该区域：选择加入区域在未开通时返回“令牌无效”或 OptInRequired
	notOpenedCodes = map[string]bool{"UnrecognizedClientException": true, "InvalidClientTokenId": true, "AuthFailure": true,
		"OptInRequired": true}
)

// describe 按原因说明一个被跳过的区域。
func (r RegionError) describe() string {
	var pe *ProviderError
	errors.As(r.Err, &pe)
	switch {
	case pe != nil && settingUpCodes[pe.Code]:
		return fmt.Sprintf("区域 %s 的 %s 正在为账户初始化，本轮跳过，下一轮再试", r.Region, r.Label)
	case pe != nil && notOpenedCodes[pe.Code]:
		return fmt.Sprintf("区域 %s 的 %s 不可用（可能未开通该区域），已跳过：%v", r.Region, r.Label, r.Err)
	}
	return fmt.Sprintf("区域 %s 的 %s 调用失败，已跳过：%v", r.Region, r.Label, r.Err)
}

// deniedCodes 是“凭证有效、但没有这项权限”的错误码（各家）。
var deniedCodes = map[string]bool{
	"AccessDenied": true, "AccessDeniedException": true, "UnauthorizedOperation": true,
	"Forbidden.RAM": true, "NoPermission": true, "NotAuthorizedOrNotFound": true,
	"AuthFailure.UnauthorizedOperation": true, "UnauthorizedOperation.PermissionDenied": true,
}

// permissionDenied 判断错误是否为权限不足（而非令牌无效）。
func permissionDenied(err error) bool {
	var pe *ProviderError
	return errors.As(err, &pe) && deniedCodes[pe.Code]
}

// regionJob 是一个区域、一类实例的查询。
type regionJob struct {
	kind, label, region string
	fn                  func(context.Context, string) ([]Instance, error)
}

// retryDelay 是区域调用失败后重试前的等待（测试中改小）。
var retryDelay = 2 * time.Second

// retryable 判断错误是否值得重试一次：服务端错误、限流与网络错误；凭证、权限、参数类错误重试也一样。
func retryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var pe *ProviderError
	if !errors.As(err, &pe) {
		return true // 网络错误
	}
	code := strings.ToLower(pe.Code)
	return pe.Status >= 500 || pe.Status == http.StatusTooManyRequests ||
		strings.Contains(code, "throttl") || strings.Contains(code, "requestlimitexceeded") || strings.Contains(code, "unavailable")
}

// callWithRetry 调用一次，可重试的错误等待 retryDelay 后再试一次。
func callWithRetry(ctx context.Context, fn func(context.Context, string) ([]Instance, error), region string) ([]Instance, error) {
	got, err := fn(ctx, region)
	if err == nil || !retryable(err) {
		return got, err
	}
	t := time.NewTimer(retryDelay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return nil, err
	case <-t.C:
	}
	return fn(ctx, region)
}

// runRegionJobs 并发执行各区域的查询（最多 4 个同时进行），每个失败可重试一次（各家客户端共用，修订第 141 条）。
// 返回成功区域的实例（未排序）与 regionOutcome 的结论：nil、账户级错误或 *PartialError。
func runRegionJobs(ctx context.Context, jobs []regionJob) ([]Instance, error) {
	var (
		mu   sync.Mutex
		all  []Instance
		errs = make([]error, len(jobs))
		wg   sync.WaitGroup
		sem  = make(chan struct{}, 4)
	)
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j regionJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			got, err := callWithRetry(ctx, j.fn, j.region)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[i] = err
				return
			}
			all = append(all, got...)
		}(i, j)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return all, regionOutcome(jobs, errs)
}

// partialOrFail 把 runRegionJobs 的结论转成 Instances 的返回值：部分失败时保留实例，账户级错误时不返回实例。
func partialOrFail(all []Instance, err error) ([]Instance, error) {
	var pe *PartialError
	if err == nil || errors.As(err, &pe) {
		return all, err
	}
	return nil, err
}

// regionOutcome 汇总各区域的结果：同一类实例在所有区域都因权限被拒绝时返回账户级错误；
// 其他失败返回 *PartialError（实例仍是其他区域的结果）。
func regionOutcome(jobs []regionJob, errs []error) error {
	var skipped []RegionError
	total, authFailed := map[string]int{}, map[string]int{}
	firstAuth := map[string]error{}
	for i, j := range jobs {
		total[j.kind]++
		if errs[i] == nil {
			continue
		}
		if permissionDenied(errs[i]) {
			authFailed[j.kind]++
			if firstAuth[j.kind] == nil {
				firstAuth[j.kind] = errs[i]
			}
		}
		if !slices.ContainsFunc(skipped, func(r RegionError) bool { return r.Kind == j.kind && r.Region == j.region }) {
			skipped = append(skipped, RegionError{Kind: j.kind, Label: j.label, Region: j.region, Err: errs[i]})
		}
	}
	for kind, n := range total {
		if n > 0 && authFailed[kind] == n {
			return firstAuth[kind] // 所有区域都被拒绝：权限问题
		}
	}
	if len(skipped) > 0 {
		return &PartialError{Skipped: skipped}
	}
	return nil
}

// sortInstances 按类型、再按 ID 排序，同样的数据得到同样的顺序。
func sortInstances(all []Instance) {
	slices.SortFunc(all, func(a, b Instance) int {
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return strings.Compare(a.ID, b.ID)
	})
}
