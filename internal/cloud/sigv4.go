package cloud

// AWS Signature Version 4（设计 44.3、44.9）：只实现面板所用接口需要的部分（Query API 与 JSON 1.1 协议）。
// 规范：https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html
//
// 【安全】签名与 Authorization 头不进日志（设计 44.2）；Secret Access Key 只在内存中用于计算签名。

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// AWSCredentials 是 IAM 用户的访问密钥。SessionToken 用于临时凭证（STS），一般为空。
type AWSCredentials struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token,omitempty"`
}

const amzDateFormat = "20060102T150405Z"

// signedHeaderNames 是参与签名的请求头（小写）；存在时才签。host 与 x-amz-date 总是签。
var signedHeaderNames = []string{"content-type", "host", "x-amz-date", "x-amz-security-token", "x-amz-target"}

// SignV4 为 req 计算签名并写入 X-Amz-Date、Authorization（与临时凭证的 X-Amz-Security-Token）。
// body 为完整请求体（GET 为空）；req.Body 由调用方另行设置。
func SignV4(req *http.Request, body []byte, c AWSCredentials, region, service string, now time.Time) {
	t := now.UTC()
	amzDate := t.Format(amzDateFormat)
	date := t.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	if c.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", c.SessionToken)
	}
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	var names []string
	var canonHeaders strings.Builder
	for _, name := range signedHeaderNames {
		var v string
		if name == "host" {
			v = host
		} else if v = req.Header.Get(name); v == "" {
			continue
		}
		names = append(names, name)
		canonHeaders.WriteString(name + ":" + strings.Join(strings.Fields(v), " ") + "\n")
	}
	signed := strings.Join(names, ";")

	payloadHash := sha256.Sum256(body)
	canonical := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQuery(req.URL.Query()),
		canonHeaders.String(),
		signed,
		hex.EncodeToString(payloadHash[:]),
	}, "\n")
	reqHash := sha256.Sum256([]byte(canonical))
	scope := date + "/" + region + "/" + service + "/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(reqHash[:])

	sig := hex.EncodeToString(hmacSHA256(signingKeyV4(c.SecretAccessKey, date, region, service), toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.AccessKeyID+"/"+scope+
		", SignedHeaders="+signed+", Signature="+sig)
}

// signingKeyV4 按 日期 → 区域 → 服务 → aws4_request 逐级派生签名密钥。
func signingKeyV4(secret, date, region, service string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), date)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	return hmacSHA256(k, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// canonicalURI：路径各段按 RFC 3986 编码；空路径为 “/”。面板调用的接口路径都是 “/”。
func canonicalURI(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" {
		return "/"
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if d, err := url.PathUnescape(s); err == nil {
			s = d
		}
		segs[i] = awsEscape(s)
	}
	return strings.Join(segs, "/")
}

// canonicalQuery：参数按名称、再按值排序，名称与值按 RFC 3986 编码（空格为 %20）。
func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, awsEscape(k)+"="+awsEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// awsEscape 按 RFC 3986 编码：只保留 A-Z a-z 0-9 - _ . ~。
func awsEscape(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&15])
		}
	}
	return b.String()
}
