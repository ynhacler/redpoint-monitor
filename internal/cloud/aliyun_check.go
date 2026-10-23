package cloud

// 阿里云权限检查（设计 44.2，修订第 204 条）：逐项调用一次只读接口，告诉用户哪一项能用、哪一项缺权限、
// 各区域实际列出了几台实例（其中几台走 CDT）。用于排查“同步成功但看不到某台实例”：缺 ECS 只读、
// 账户的“区域”只填了部分区域、实例在别的阿里云账号下，都能从结果里直接看出来。

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// CheckItem 是权限检查的一项。
type CheckItem struct {
	Name   string `json:"name"`
	Policy string `json:"policy"` // 所需的系统只读策略
	OK     bool   `json:"ok"`
	Detail string `json:"detail"` // 成功时为结果摘要，失败时为服务商返回的错误
}

// Checker 由支持权限检查的客户端实现（目前只有阿里云）。
type Checker interface {
	Check(ctx context.Context, now time.Time) []CheckItem
}

// Check 依次检查费用、ECS、轻量应用服务器与 CDT 的只读权限。
func (c *aliyunClient) Check(ctx context.Context, now time.Time) []CheckItem {
	var items []CheckItem
	fail := func(name, policy string, err error) {
		items = append(items, CheckItem{Name: name, Policy: policy, Detail: checkErrText(err)})
	}

	if _, err := c.Costs(ctx, now); err != nil {
		fail("费用与余额", "AliyunBSSReadOnlyAccess", err)
	} else {
		items = append(items, CheckItem{Name: "费用与余额", Policy: "AliyunBSSReadOnlyAccess", OK: true, Detail: "可以读取本月账单与可用余额"})
	}

	items = append(items, c.checkInstances(ctx, "ECS 实例", "AliyunECSReadOnlyAccess", "ecs", c.ecsRegions, c.ecsInstances))
	items = append(items, c.checkInstances(ctx, "轻量应用服务器", "AliyunSWASReadOnlyAccess", "swas", c.swasRegions, c.swasInstances))

	if pools, err := c.TrafficPools(ctx, now); err != nil {
		fail("CDT 流量", "AliyunCDTReadOnlyAccess", err)
	} else {
		d := "可以读取；本月还没有 CDT 流量（实例为固定带宽计费，或本月未产生流量）"
		if len(pools) > 0 {
			parts := make([]string, len(pools))
			for i, p := range pools {
				parts[i] = fmt.Sprintf("%s %.2f GiB", p.Name, float64(p.UsedBytes)/(1<<30))
			}
			d = "本月已用：" + strings.Join(parts, "，")
		}
		items = append(items, CheckItem{Name: "CDT 流量", Policy: "AliyunCDTReadOnlyAccess", OK: true, Detail: d})
	}
	return items
}

// checkInstances 列出区域，再逐区域列出实例，按区域汇总台数（ECS 另计走 CDT 的台数）；部分区域失败时也写明。
func (c *aliyunClient) checkInstances(ctx context.Context, name, policy, kind string,
	regions func(context.Context) ([]string, error), list func(context.Context, string) ([]Instance, error)) CheckItem {
	item := CheckItem{Name: name, Policy: policy}
	rs, err := regions(ctx)
	if err != nil {
		item.Detail = checkErrText(err)
		return item
	}
	jobs := make([]regionJob, len(rs))
	for i, r := range rs {
		jobs[i] = regionJob{kind, name, r, list}
	}
	all, err := runRegionJobs(ctx, jobs)
	var pe *PartialError
	if err != nil && !errors.As(err, &pe) {
		item.Detail = checkErrText(err)
		return item
	}
	item.OK = true
	scope := fmt.Sprintf("查询了 %d 个区域", len(rs))
	if len(c.regions) > 0 {
		scope = fmt.Sprintf("账户设置只查询 %s，其中可用 %d 个", strings.Join(c.regions, "、"), len(rs))
	}
	// 账户设置中不存在的区域（如误填了实例名称，修订第 209 条）：写在最前面，并标为未通过，提醒去修改
	if bad := c.unknownRegions(); len(bad) > 0 {
		item.OK = false
		scope = unknownRegionError(bad, "cn-hongkong").describe() + " " + scope
	}
	count, cdt := map[string]int{}, map[string]int{}
	for _, in := range all {
		count[in.Region]++
		if in.CDTPool != "" {
			cdt[in.Region]++
		}
	}
	if len(all) == 0 {
		item.Detail = scope + "，没有找到实例"
	} else {
		keys := make([]string, 0, len(count))
		for r := range count {
			keys = append(keys, r)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, r := range keys {
			parts[i] = fmt.Sprintf("%s %d 台", r, count[r])
			if cdt[r] > 0 {
				parts[i] += fmt.Sprintf("（走 CDT %d 台）", cdt[r])
			}
		}
		item.Detail = fmt.Sprintf("%s，共 %d 台：%s", scope, len(all), strings.Join(parts, "、"))
	}
	if pe != nil {
		item.Detail += "。" + pe.Error()
	}
	return item
}

// checkErrText 是检查失败时显示的说明：权限不足时写明是缺少策略，其他错误照原样给出（不含凭证）。
func checkErrText(err error) string {
	if permissionDenied(err) {
		return "没有权限：" + err.Error()
	}
	return err.Error()
}
