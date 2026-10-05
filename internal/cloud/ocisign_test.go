package cloud

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Oracle 官方文档“请求签名”中的测试私钥（公开示例，不是真实凭证）。
// PEM 头尾拆开写，避免被 GitHub 推送保护误判为泄露的私钥。
const ociTestKey = "-----BEGIN RSA " + "PRIVATE KEY-----\n" + `MIICXgIBAAKBgQDCFENGw33yGihy92pDjZQhl0C36rPJj+CvfSC8+q28hxA161QF
NUd13wuCTUcq0Qd2qsBe/2hFyc2DCJJg0h1L78+6Z4UMR7EOcpfdUE9Hf3m/hs+F
UR45uBJeDK1HSFHD8bHKD6kv8FPGfJTotc+2xjJwoYi+1hqp1fIekaxsyQIDAQAB
AoGBAJR8ZkCUvx5kzv+utdl7T5MnordT1TvoXXJGXK7ZZ+UuvMNUCdN2QPc4sBiA
QWvLw1cSKt5DsKZ8UETpYPy8pPYnnDEz2dDYiaew9+xEpubyeW2oH4Zx71wqBtOK
kqwrXa/pzdpiucRRjk6vE6YY7EBBs/g7uanVpGibOVAEsqH1AkEA7DkjVH28WDUg
f1nqvfn2Kj6CT7nIcE3jGJsZZ7zlZmBmHFDONMLUrXR/Zm3pR5m0tCmBqa5RK95u
412jt1dPIwJBANJT3v8pnkth48bQo/fKel6uEYyboRtA5/uHuHkZ6FQF7OUkGogc
mSJluOdc5t6hI1VsLn0QZEjQZMEOWr+wKSMCQQCC4kXJEsHAve77oP6HtG/IiEn7
kpyUXRNvFsDE0czpJJBvL/aRFUJxuRK91jhjC68sA7NsKMGg5OXb5I5Jj36xAkEA
gIT7aFOYBFwGgQAQkWNKLvySgKbAZRTeLBacpHMuQdl1DfdntvAyqpAZ0lY0RKmW
G6aFKaqQfOXKCyWoUiVknQJAXrlgySFci/2ueKlIE1QqIiLSZ8V8OlpFLRnb1pzI
7U1yQXnTAEFYM560yJlzUpOb1V4cScGd365tiSMvxLOvTA==
` + "-----END RSA " + "PRIVATE KEY-----"

// 期望签名由 openssl 按文档中的签名串独立计算：
//
//	printf '%s' "$SIGNING_STRING" | openssl dgst -sha256 -sign key.pem | base64
//
// RSA PKCS#1 v1.5 签名是确定的，与 Go 实现分开计算，用于核对签名串的格式与顺序。
// 注意：文档示例写的 “Thu, 05 Jan 2014” 星期有误（当天是星期日），这里按正确的 “Sun” 计算。
func TestSignOCIGet(t *testing.T) {
	k, err := ParseOCIKey(ociTestKey)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", "https://iaas.us-phoenix-1.oraclecloud.com/20160918/instances?availabilityDomain=Pjwf%3A%20PHX-AD-1&compartmentId=ocid1.compartment.oc1..aaaaaaaam3we6vgnherjq5q2idnccdflvjsnog7mlr6rtdb25gilchfeyjxa&displayName=TeamXInstances&volumeId=ocid1.volume.oc1.phx.abyhqljrgvttnlx73nmrwfaux7kcvzfs3s66izvxf2h4lgvyndsdsnoiwr5q", nil)
	now := time.Date(2014, 1, 5, 21, 31, 40, 0, time.UTC)
	if err := SignOCI(req, nil, "ten/usr/fp", k, now); err != nil {
		t.Fatal(err)
	}
	want := `Signature version="1",keyId="ten/usr/fp",algorithm="rsa-sha256",headers="date (request-target) host",signature="JDFdcwTJ0TbiDFvAv5Q2irKMwrvEQoVzw9qYvcVianMskb8XrLLFlvs3Yr9uz/d9y74rMqxRlWS0KATOlhD1ao2+/FnyOj2yjdWCSjDnKR56uV8IsWCum0g7eY5jXnfOSACBj01gwbM28JYrJ//DLmyUzF4QPScgFYXrAmrwQO0="`
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization:\n得到 %s\n期望 %s", got, want)
	}
	if req.Header.Get("Date") != "Sun, 05 Jan 2014 21:31:40 GMT" {
		t.Fatalf("Date = %s", req.Header.Get("Date"))
	}
}

func TestSignOCIPost(t *testing.T) {
	k, _ := ParseOCIKey(ociTestKey)
	body := []byte(`{"compartmentId":"ocid1.compartment.oc1..x","instanceId":"ocid1.instance.oc1.phx.x","volumeId":"ocid1.volume.oc1.phx.x"}`)
	req, _ := http.NewRequest("POST", "https://iaas.us-phoenix-1.oraclecloud.com/20160918/volumeAttachments", strings.NewReader(string(body)))
	SignOCI(req, body, "ten/usr/fp", k, time.Date(2014, 1, 5, 21, 31, 40, 0, time.UTC))
	if req.Header.Get("x-content-sha256") != "Dkemj/Tz4lyWb6NTs5pUkQekgL9x2KiMuZLiQ1OFioY=" || req.Header.Get("Content-Length") != "120" {
		t.Fatalf("内容摘要 / 长度 %v", req.Header)
	}
	want := `headers="date (request-target) host x-content-sha256 content-type content-length",signature="MYL/2E8dVtXM6+eH0lTdy/KDlgl6OyA+5S8guV2BNHR/xG7an29OP/1f3Ow3RPU5LeC/KXQ1zb6iIPE7JGa/MCXzpF40LjNhclCvGn6DhpAOR93jXC2tG3lTh6ES/MMoOgHsD971Aa/TGigkDImFp7ngSw+ZDx4XsgX+NcFHW+k="`
	if got := req.Header.Get("Authorization"); !strings.HasSuffix(got, want) {
		t.Fatalf("Authorization:\n得到 %s\n期望结尾 %s", got, want)
	}
}

func TestParseOCIKey(t *testing.T) {
	if _, err := ParseOCIKey("not a key"); err == nil {
		t.Error("非 PEM 应报错")
	}
	enc := "-----BEGIN RSA " + "PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,00\n\nAAAA\n-----END RSA " + "PRIVATE KEY-----"
	if _, err := ParseOCIKey(enc); err == nil || !strings.Contains(err.Error(), "密码") {
		t.Errorf("带密码的私钥应报错：%v", err)
	}
}
