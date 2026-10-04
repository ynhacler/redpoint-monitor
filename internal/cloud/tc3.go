package cloud

// 腾讯云 API 3.0 签名 TC3-HMAC-SHA256（设计 44.3）：国内站与国际站共用。
// 规范：https://www.tencentcloud.com/document/api/213/33224
//
// 【安全】SecretKey 只在内存中用于计算签名；签名与 Authorization 头不进日志（设计 44.2）。

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"
)

// TencentCredentials 是 CAM 子用户的 API 密钥。
type TencentCredentials struct {
	SecretID  string `json:"secret_id"`
	SecretKey string `json:"secret_key"`
}

// tc3CanonicalRequest 返回规范请求串。面板只发送 POST + JSON，签名头固定为 content-type 与 host。
func tc3CanonicalRequest(contentType, host string, body []byte) string {
	h := sha256.Sum256(body)
	return "POST\n/\n\ncontent-type:" + contentType + "\nhost:" + host + "\n\ncontent-type;host\n" + hex.EncodeToString(h[:])
}

// tc3Signature 按 日期 → 服务 → tc3_request 派生密钥并签名。
func tc3Signature(secretKey, date, service, stringToSign string) string {
	k := hmacSHA256([]byte("TC3"+secretKey), date)
	k = hmacSHA256(k, service)
	k = hmacSHA256(k, "tc3_request")
	return hex.EncodeToString(hmacSHA256(k, stringToSign))
}

// SignTC3 设置 X-TC-* 头并写入 Authorization。service 为接口所属产品（billing、cvm、lighthouse）；
// 日期必须取时间戳的 UTC 日期。
func SignTC3(req *http.Request, body []byte, c TencentCredentials, service, action, version, region string, now time.Time) {
	const contentType = "application/json; charset=utf-8"
	req.Header.Set("Content-Type", contentType)
	ts := now.Unix()
	date := time.Unix(ts, 0).UTC().Format("2006-01-02")
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Version", version)
	req.Header.Set("X-TC-Timestamp", strconv.FormatInt(ts, 10))
	if region != "" {
		req.Header.Set("X-TC-Region", region)
	}
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	h := sha256.Sum256([]byte(tc3CanonicalRequest(contentType, host, body)))
	scope := date + "/" + service + "/tc3_request"
	toSign := "TC3-HMAC-SHA256\n" + strconv.FormatInt(ts, 10) + "\n" + scope + "\n" + hex.EncodeToString(h[:])
	req.Header.Set("Authorization", "TC3-HMAC-SHA256 Credential="+c.SecretID+"/"+scope+
		", SignedHeaders=content-type;host, Signature="+tc3Signature(c.SecretKey, date, service, toSign))
}
