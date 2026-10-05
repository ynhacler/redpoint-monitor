package cloud

// Oracle Cloud（OCI）请求签名（设计 44.3）：HTTP Signatures（draft-cavage），RSA-SHA256。
// 规范：https://docs.oracle.com/en-us/iaas/Content/API/Concepts/signingrequests.htm
//
//	GET / DELETE   签名头：date (request-target) host
//	POST / PUT     另加：x-content-sha256 content-type content-length
//	keyId          <租户 OCID>/<用户 OCID>/<公钥指纹>
//
// 【安全】API 私钥只在内存中用于签名，以加密形式保存（设计 44.2）；签名与 Authorization 头不进日志。

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// OCICredentials 是 API 签名密钥：租户、用户、指纹与 PEM 格式的 RSA 私钥（不支持带密码的私钥）。
type OCICredentials struct {
	TenancyOCID string `json:"tenancy_ocid"`
	UserOCID    string `json:"user_ocid"`
	Fingerprint string `json:"fingerprint"`
	PrivateKey  string `json:"private_key"`
	Region      string `json:"region"` // 主区域（home region）：费用与身份接口只在主区域调用
}

// ParseOCIKey 解析 PEM 私钥（PKCS#1 或 PKCS#8 的 RSA 私钥）。
func ParseOCIKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(pemText)))
	if block == nil {
		return nil, errors.New("私钥不是 PEM 格式（应以 -----BEGIN 开头）")
	}
	if strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") || block.Type == "ENCRYPTED PRIVATE KEY" {
		return nil, errors.New("私钥带有密码，请使用不带密码的 API 签名密钥")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("无法解析私钥：应为 RSA 私钥")
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("私钥不是 RSA 类型")
	}
	return rk, nil
}

// SignOCI 为 req 签名：设置 Date（与 POST / PUT 的 x-content-sha256、Content-Type、Content-Length）与 Authorization。
func SignOCI(req *http.Request, body []byte, keyID string, key *rsa.PrivateKey, now time.Time) error {
	req.Header.Set("Date", now.UTC().Format(http.TimeFormat))
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	target := strings.ToLower(req.Method) + " " + req.URL.EscapedPath()
	if req.URL.RawQuery != "" {
		target += "?" + req.URL.RawQuery
	}
	names := []string{"date", "(request-target)", "host"}
	values := map[string]string{"date": req.Header.Get("Date"), "(request-target)": target, "host": host}
	if req.Method == http.MethodPost || req.Method == http.MethodPut {
		h := sha256.Sum256(body)
		sum := base64.StdEncoding.EncodeToString(h[:])
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("x-content-sha256", sum)
		req.Header.Set("Content-Length", strconv.Itoa(len(body)))
		req.ContentLength = int64(len(body))
		names = append(names, "x-content-sha256", "content-type", "content-length")
		values["x-content-sha256"] = sum
		values["content-type"] = req.Header.Get("Content-Type")
		values["content-length"] = strconv.Itoa(len(body))
	}
	lines := make([]string, len(names))
	for i, n := range names {
		lines[i] = n + ": " + values[n]
	}
	digest := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", `Signature version="1",keyId="`+keyID+`",algorithm="rsa-sha256",headers="`+
		strings.Join(names, " ")+`",signature="`+base64.StdEncoding.EncodeToString(sig)+`"`)
	return nil
}
