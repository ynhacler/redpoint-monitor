package cloud

import (
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"
)

// AWS 官方签名示例与 aws-sig-v4-test-suite 中的用例（凭证均为文档中的示例值）。
var exampleCreds = AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"}

func TestSigningKeyV4(t *testing.T) {
	// IAM 用户指南“派生签名密钥”示例
	got := hex.EncodeToString(signingKeyV4(exampleCreds.SecretAccessKey, "20150830", "us-east-1", "iam"))
	if got != "c4afb1cc5771d871763a393e44b703571b55cc28424d1a5e86da6ed3c154a4b9" {
		t.Fatalf("签名密钥 = %s", got)
	}
}

func TestSignV4Examples(t *testing.T) {
	now := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	for _, c := range []struct {
		name, url, service, contentType, signed, sig string
	}{
		{"get-vanilla", "https://example.amazonaws.com/", "service", "", "host;x-amz-date",
			"5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"},
		{"get-vanilla-query-order-key-case", "https://example.amazonaws.com/?Param2=value2&Param1=value1", "service", "", "host;x-amz-date",
			"b97d918cfa904a5beff61c982a1b6f458b799221646efd99d3219ec94cdf2500"},
		{"iam ListUsers", "https://iam.amazonaws.com/?Action=ListUsers&Version=2010-05-08", "iam",
			"application/x-www-form-urlencoded; charset=utf-8", "content-type;host;x-amz-date",
			"5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7"},
	} {
		req, _ := http.NewRequest("GET", c.url, nil)
		if c.contentType != "" {
			req.Header.Set("Content-Type", c.contentType)
		}
		SignV4(req, nil, exampleCreds, "us-east-1", c.service, now)
		auth := req.Header.Get("Authorization")
		want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/" + c.service + "/aws4_request, SignedHeaders=" +
			c.signed + ", Signature=" + c.sig
		if auth != want {
			t.Errorf("%s:\n得到 %s\n期望 %s", c.name, auth, want)
		}
		if req.Header.Get("X-Amz-Date") != "20150830T123600Z" {
			t.Errorf("%s: X-Amz-Date = %s", c.name, req.Header.Get("X-Amz-Date"))
		}
	}
}

func TestSignV4SessionTokenSigned(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://ce.us-east-1.amazonaws.com/", nil)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AWSInsightsIndexService.GetCostAndUsage")
	c := exampleCreds
	c.SessionToken = "tok"
	SignV4(req, []byte(`{}`), c, "us-east-1", "ce", time.Now())
	if req.Header.Get("X-Amz-Security-Token") != "tok" ||
		!strings.Contains(req.Header.Get("Authorization"), "SignedHeaders=content-type;host;x-amz-date;x-amz-security-token;x-amz-target,") {
		t.Fatalf("Authorization = %s", req.Header.Get("Authorization"))
	}
}

func TestAWSEscape(t *testing.T) {
	if got := awsEscape("a b+c/~*"); got != "a%20b%2Bc%2F~%2A" {
		t.Fatalf("awsEscape = %s", got)
	}
}
