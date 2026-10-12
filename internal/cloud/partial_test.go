package cloud

import (
	"context"
	"errors"
	"testing"
)

// 区域级与账户级的划分（修订第 141 条）。
func TestRegionOutcome(t *testing.T) {
	job := func(kind, region string) regionJob { return regionJob{kind: kind, label: kind, region: region} }
	denied := &ProviderError{Status: 403, Code: "UnauthorizedOperation"}
	token := &ProviderError{Status: 400, Code: "UnrecognizedClientException"}

	// 全部成功
	if err := regionOutcome([]regionJob{job("ec2", "a")}, []error{nil}); err != nil {
		t.Errorf("全部成功：%v", err)
	}
	// 唯一的区域“令牌无效”：区域没有开通，区域级（不是凭证失效）
	err := regionOutcome([]regionJob{job("lightsail", "ap-east-1")}, []error{token})
	var pe *PartialError
	if !errors.As(err, &pe) || IsAuthError(err) {
		t.Errorf("令牌无效应为区域级：%v", err)
	}
	// 某类实例在所有区域都“权限不足”：账户级
	err = regionOutcome([]regionJob{job("ec2", "a"), job("ec2", "b"), job("lightsail", "a")}, []error{denied, denied, nil})
	if !IsAuthError(err) || errors.As(err, &pe) {
		t.Errorf("所有区域都权限不足应为账户级：%v", err)
	}
	// 同一（类型, 区域）多次失败（OCI 每个区间一次）只记一次
	err = regionOutcome([]regionJob{job("oci", "a"), job("oci", "a"), job("oci", "b")}, []error{token, token, nil})
	if !errors.As(err, &pe) || len(pe.Skipped) != 1 {
		t.Errorf("应合并为一条：%v", err)
	}
	// 重试：服务端错误、限流、网络错误重试；凭证、权限、取消不重试
	for err, want := range map[error]bool{
		&ProviderError{Status: 500}: true, &ProviderError{Status: 400, Code: "Throttling"}: true,
		errors.New("connection reset"): true, denied: false, token: false, context.Canceled: false,
	} {
		if retryable(err) != want {
			t.Errorf("retryable(%v) 应为 %v", err, want)
		}
	}
}
