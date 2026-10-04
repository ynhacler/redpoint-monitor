package cloud

// 阿里云 V3 签名 ACS3-HMAC-SHA256（设计 44.3）：国内站与国际站共用。
// 规范：https://www.alibabacloud.com/help/en/sdk/product-overview/v3-request-structure-and-signature
//
// 【安全】AccessKey Secret 只在内存中用于计算签名；签名与 Authorization 头不进日志（设计 44.2）。

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"
)

// AliyunCredentials 是 RAM 用户的 AccessKey。
type AliyunCredentials struct {
	AccessKeyID     string `json:"access_key_id"`
	AccessKeySecret string `json:"access_key_secret"`
}

// SignACS3 设置 x-acs-* 头并写入 Authorization。action、version 为接口名与版本；
// nonce 为空时随机生成（测试中固定）。参与签名的头：host、content-type（存在时）与全部 x-acs-*。
func SignACS3(req *http.Request, body []byte, c AliyunCredentials, action, version string, now time.Time, nonce string) {
	if nonce == "" {
		b := make([]byte, 16)
		rand.Read(b)
		nonce = hex.EncodeToString(b)
	}
	payload := sha256.Sum256(body)
	req.Header.Set("x-acs-action", action)
	req.Header.Set("x-acs-version", version)
	req.Header.Set("x-acs-date", now.UTC().Format("2006-01-02T15:04:05Z"))
	req.Header.Set("x-acs-signature-nonce", nonce)
	req.Header.Set("x-acs-content-sha256", hex.EncodeToString(payload[:]))

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	headers := map[string]string{"host": host}
	for k, vs := range req.Header {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-acs-") || lk == "content-type" {
			headers[lk] = strings.TrimSpace(strings.Join(vs, ","))
		}
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + headers[k] + "\n")
	}
	signed := strings.Join(names, ";")

	canonical := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQuery(req.URL.Query()),
		canonHeaders.String(),
		signed,
		hex.EncodeToString(payload[:]),
	}, "\n")
	h := sha256.Sum256([]byte(canonical))
	sig := hex.EncodeToString(hmacSHA256([]byte(c.AccessKeySecret), "ACS3-HMAC-SHA256\n"+hex.EncodeToString(h[:])))
	req.Header.Set("Authorization", "ACS3-HMAC-SHA256 Credential="+c.AccessKeyID+",SignedHeaders="+signed+",Signature="+sig)
}
