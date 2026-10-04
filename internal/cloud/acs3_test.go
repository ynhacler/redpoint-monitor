package cloud

import (
	"net/http"
	"testing"
	"time"
)

// 阿里云官方文档“V3 版本请求体 & 签名机制”中的示例（固定 nonce 与时间）
func TestSignACS3Example(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://ecs.cn-shanghai.aliyuncs.com/?ImageId=win2019_1809_x64_dtc_zh-cn_40G_alibase_20230811.vhd&RegionId=cn-shanghai", nil)
	c := AliyunCredentials{AccessKeyID: "YourAccessKeyId", AccessKeySecret: "YourAccessKeySecret"}
	SignACS3(req, nil, c, "RunInstances", "2014-05-26", time.Date(2023, 10, 26, 10, 22, 32, 0, time.UTC), "3156853299f313e23d1673dc12e1703d")
	want := "ACS3-HMAC-SHA256 Credential=YourAccessKeyId,SignedHeaders=host;x-acs-action;x-acs-content-sha256;x-acs-date;" +
		"x-acs-signature-nonce;x-acs-version,Signature=06563a9e1b43f5dfe96b81484da74bceab24a1d853912eee15083a6f0f3283c0"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization:\n得到 %s\n期望 %s", got, want)
	}
}

func TestSignACS3RandomNonce(t *testing.T) {
	a, _ := http.NewRequest("POST", "https://ecs.aliyuncs.com/", nil)
	b, _ := http.NewRequest("POST", "https://ecs.aliyuncs.com/", nil)
	c := AliyunCredentials{AccessKeyID: "id", AccessKeySecret: "secret"}
	now := time.Now()
	SignACS3(a, nil, c, "DescribeRegions", "2014-05-26", now, "")
	SignACS3(b, nil, c, "DescribeRegions", "2014-05-26", now, "")
	if a.Header.Get("x-acs-signature-nonce") == b.Header.Get("x-acs-signature-nonce") || len(a.Header.Get("x-acs-signature-nonce")) != 32 {
		t.Fatal("每个请求应使用不同的随机 nonce（防重放）")
	}
}
