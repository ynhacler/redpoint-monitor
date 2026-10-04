package cloud

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"
)

// tencentTestID 是测试用的假 SecretId。分成两段书写：完整的 AKID… 字面量会被 GitHub 推送保护当作真实密钥拦截
var tencentTestID = "AKID" + "testidtestidtestidtestidtestid12"

// 腾讯云官方文档的示例（密钥在文档中已打码，只能核对与密钥无关的中间值）：
// 请求体哈希与规范请求串哈希必须与文档一致。
func TestTC3CanonicalRequestOfficial(t *testing.T) {
	body := []byte(`{"Limit": 1, "Filters": [{"Values": ["unnamed"], "Name": "instance-name"}]}`)
	h := sha256.Sum256(body)
	if hex.EncodeToString(h[:]) != "99d58dfbc6745f6747f36bfca17dee5e6881dc0428a0a36f96199342bc5b4907" {
		t.Fatal("请求体哈希与文档不一致")
	}
	c := sha256.Sum256([]byte(tc3CanonicalRequest("application/json; charset=utf-8", "cvm.tencentcloudapi.com", body)))
	if got := hex.EncodeToString(c[:]); got != "2815843035062fffda5fd6f2a44ea8a34818b0dc46f024b8b3786976a3adda7a" {
		t.Fatalf("规范请求串哈希 %s 与文档不一致", got)
	}
}

// 完整签名：期望值由按文档算法独立计算的脚本得出（文档的密钥打码，无法直接复现官方签名）
func TestSignTC3(t *testing.T) {
	body := []byte(`{"Limit": 1, "Filters": [{"Values": ["unnamed"], "Name": "instance-name"}]}`)
	req, _ := http.NewRequest("POST", "https://cvm.tencentcloudapi.com/", strings.NewReader(string(body)))
	c := TencentCredentials{SecretID: tencentTestID, SecretKey: "testkeytestkeytestkeytestkey1234"}
	SignTC3(req, body, c, "cvm", "DescribeInstances", "2017-03-12", "ap-guangzhou", time.Unix(1551113065, 0))
	want := "TC3-HMAC-SHA256 Credential=" + tencentTestID + "/2019-02-25/cvm/tc3_request, " +
		"SignedHeaders=content-type;host, Signature=df97aa41642ad7ee67de51af833a7af94395250d684415e408f01529c63dc929"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization:\n得到 %s\n期望 %s", got, want)
	}
	if req.Header.Get("X-TC-Timestamp") != "1551113065" || req.Header.Get("X-TC-Region") != "ap-guangzhou" ||
		req.Header.Get("X-TC-Action") != "DescribeInstances" || req.Header.Get("X-TC-Version") != "2017-03-12" {
		t.Fatalf("X-TC-* 头 %v", req.Header)
	}
}

// 签名日期取 UTC：北京时间 2 月 26 日 01:00 仍是 UTC 2 月 25 日
func TestSignTC3UTCDate(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://billing.tencentcloudapi.com/", nil)
	SignTC3(req, nil, TencentCredentials{SecretID: "AKIDx", SecretKey: "k"}, "billing", "DescribeAccountBalance", "2018-07-09", "",
		time.Date(2019, 2, 26, 1, 0, 0, 0, time.FixedZone("UTC+8", 8*3600)))
	if !strings.Contains(req.Header.Get("Authorization"), "/2019-02-25/billing/tc3_request") || req.Header.Get("X-TC-Region") != "" {
		t.Fatalf("Authorization = %s", req.Header.Get("Authorization"))
	}
}
