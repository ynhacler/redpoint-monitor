package relay

import (
	"bytes"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"vpsmon/internal/push"
)

func verifyES256(t *testing.T, jwt string, pub *ecdsa.PublicKey) map[string]any {
	t.Helper()
	parts := strings.Split(jwt, ".")
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Errorf("APNs JWT 签名不正确")
	}
	var claims map[string]any
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	json.Unmarshal(raw, &claims)
	return claims
}

type fakes struct {
	mu        sync.Mutex
	apnsBody  map[string]any
	apnsHdr   http.Header
	fcmBody   map[string]any
	tokenHits int
}

func setup(t *testing.T, now time.Time) (*Relay, *fakes, ed25519.PrivateKey) {
	t.Helper()
	f := &fakes{}
	apnsKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	fcmKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/3/device/"):
			claims := verifyES256(t, strings.TrimPrefix(r.Header.Get("authorization"), "bearer "), &apnsKey.PublicKey)
			if claims["iss"] != "TEAM123456" {
				t.Errorf("iss %v", claims["iss"])
			}
			f.apnsHdr = r.Header.Clone()
			json.NewDecoder(r.Body).Decode(&f.apnsBody)
			if strings.HasSuffix(r.URL.Path, strings.Repeat("d", 64)) {
				w.WriteHeader(http.StatusGone)
				w.Write([]byte(`{"reason":"Unregistered"}`))
			}
		case r.URL.Path == "/token":
			r.ParseForm()
			parts := strings.Split(r.Form.Get("assertion"), ".")
			sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
			sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
			if rsa.VerifyPKCS1v15(&fcmKey.PublicKey, crypto.SHA256, sum[:], sig) != nil {
				t.Error("FCM 断言签名不正确")
			}
			f.tokenHits++
			w.Write([]byte(`{"access_token":"ya29.test","expires_in":3600}`))
		case r.URL.Path == "/v1/projects/demo/messages:send":
			if r.Header.Get("Authorization") != "Bearer ya29.test" {
				t.Errorf("FCM 授权头 %q", r.Header.Get("Authorization"))
			}
			json.NewDecoder(r.Body).Decode(&f.fcmBody)
			msg := f.fcmBody["message"].(map[string]any)
			if strings.HasPrefix(msg["token"].(string), "dead") {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`))
			}
		default:
			t.Errorf("未预期的请求 %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	r := New(Config{
		APNs: &APNsConfig{KeyID: "KEY1234567", TeamID: "TEAM123456", Topic: "dev.vpsmon.app", Key: apnsKey, Endpoint: srv.URL},
		FCM:  &FCMConfig{ProjectID: "demo", ClientEmail: "relay@demo.iam.gserviceaccount.com", Key: fcmKey, TokenURL: srv.URL + "/token", Endpoint: srv.URL},
		Now:  func() time.Time { return now },
	})
	_, inst, _ := ed25519.GenerateKey(rand.Reader)
	return r, f, inst
}

func sealed(t *testing.T) string {
	dev, _ := ecdh.X25519().GenerateKey(rand.Reader)
	ct, err := push.Seal(dev.PublicKey(), []byte(`{"title":"已离线"}`))
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(ct)
}

func post(r *Relay, key ed25519.PrivateKey, now time.Time, p push.Request) *httptest.ResponseRecorder {
	body, _ := json.Marshal(p)
	req := httptest.NewRequest(http.MethodPost, "/v1/push", bytes.NewReader(body))
	push.Sign(req, body, key, now)
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, req)
	return rec
}

func TestRelayAPNsAndFCM(t *testing.T) {
	now := time.Unix(1790000000, 0)
	r, f, inst := setup(t, now)
	ct := sealed(t)

	rec := post(r, inst, now, push.Request{Provider: "apns", Token: strings.Repeat("a", 64), Ciphertext: ct, Critical: true})
	if rec.Code != 200 {
		t.Fatalf("APNs %d %s", rec.Code, rec.Body)
	}
	aps := f.apnsBody["aps"].(map[string]any)
	if f.apnsBody["c"] != ct || aps["mutable-content"].(float64) != 1 || aps["interruption-level"] != "time-sensitive" ||
		f.apnsHdr.Get("apns-topic") != "dev.vpsmon.app" || f.apnsHdr.Get("apns-push-type") != "alert" {
		t.Fatalf("APNs 请求 %v %v", f.apnsBody, f.apnsHdr)
	}
	// 外层文字是固定的兜底文字，不含任何告警内容（30.3.3）
	if alert := aps["alert"].(map[string]any); alert["title"] != "服务器告警" {
		t.Fatalf("外层文字 %v", alert)
	}

	rec = post(r, inst, now.Add(time.Second), push.Request{Provider: "fcm", Token: "fcm-token-" + strings.Repeat("x", 30), Ciphertext: ct})
	if rec.Code != 200 {
		t.Fatalf("FCM %d %s", rec.Code, rec.Body)
	}
	msg := f.fcmBody["message"].(map[string]any)
	if msg["data"].(map[string]any)["c"] != ct || msg["notification"] != nil {
		t.Fatalf("FCM 应为只有密文的 data message：%v", msg)
	}
	// 访问令牌缓存：第二次不再换取
	post(r, inst, now.Add(2*time.Second), push.Request{Provider: "fcm", Token: "fcm-token-" + strings.Repeat("y", 30), Ciphertext: ct})
	if f.tokenHits != 1 {
		t.Fatalf("访问令牌应缓存：%d", f.tokenHits)
	}

	// 失效的 Token：410，面板据此删除
	if rec := post(r, inst, now.Add(3*time.Second), push.Request{Provider: "apns", Token: strings.Repeat("d", 64), Ciphertext: ct}); rec.Code != 410 {
		t.Fatalf("APNs 失效 Token 应为 410：%d", rec.Code)
	}
	if rec := post(r, inst, now.Add(4*time.Second), push.Request{Provider: "fcm", Token: "dead-" + strings.Repeat("z", 30), Ciphertext: ct}); rec.Code != 410 {
		t.Fatalf("FCM 失效 Token 应为 410：%d", rec.Code)
	}

	// 健康检查只有聚合计数
	h := httptest.NewRecorder()
	r.Handler().ServeHTTP(h, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if !strings.Contains(h.Body.String(), `"sent":3`) || strings.Contains(h.Body.String(), "token") {
		t.Fatalf("healthz %s", h.Body)
	}
}

func TestRelayRejects(t *testing.T) {
	now := time.Unix(1790000000, 0)
	r, _, inst := setup(t, now)
	ct := sealed(t)
	ok := push.Request{Provider: "apns", Token: strings.Repeat("a", 64), Ciphertext: ct}

	// 未签名 / 签名不对
	body, _ := json.Marshal(ok)
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/push", bytes.NewReader(body)))
	if rec.Code != 401 {
		t.Fatalf("未签名应为 401：%d", rec.Code)
	}
	// 重放同一个请求
	req := httptest.NewRequest(http.MethodPost, "/v1/push", bytes.NewReader(body))
	push.Sign(req, body, inst, now)
	r.Handler().ServeHTTP(httptest.NewRecorder(), req)
	again2 := httptest.NewRequest(http.MethodPost, "/v1/push", bytes.NewReader(body))
	again2.Header = req.Header.Clone()
	rec = httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, again2)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), "重复") {
		t.Fatalf("重放应拒绝：%d %s", rec.Code, rec.Body)
	}
	// 格式错误
	for name, p := range map[string]push.Request{
		"未知平台":       {Provider: "wns", Token: strings.Repeat("a", 64), Ciphertext: ct},
		"APNs Token": {Provider: "apns", Token: "not-hex", Ciphertext: ct},
		"密文过短":       {Provider: "apns", Token: strings.Repeat("a", 64), Ciphertext: "AAAA"},
	} {
		now = now.Add(time.Second)
		if rec := post(r, inst, now, p); rec.Code != 400 {
			t.Errorf("%s 应为 400：%d", name, rec.Code)
		}
	}
	// 未启用的平台
	r2 := New(Config{Now: func() time.Time { return now }})
	if rec := post(r2, inst, now, ok); rec.Code != 503 {
		t.Fatalf("未配置的平台应为 503：%d", rec.Code)
	}
}

// 按实例限流：每分钟 30 条；不同实例互不影响
func TestRelayRateLimit(t *testing.T) {
	now := time.Unix(1790000000, 0)
	r, _, inst := setup(t, now)
	ct := sealed(t)
	var last int
	for i := 0; i < 31; i++ {
		// 每条使用不同的 Token，避免签名相同被当作重放
		tok := strings.Repeat("a", 63) + string("0123456789abcdef"[i%16])
		if i >= 16 {
			tok = strings.Repeat("b", 63) + string("0123456789abcdef"[i%16])
		}
		last = post(r, inst, now, push.Request{Provider: "apns", Token: tok, Ciphertext: ct}).Code
	}
	if last != 429 {
		t.Fatalf("第 31 条应限流：%d", last)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if rec := post(r, other, now, push.Request{Provider: "apns", Token: strings.Repeat("c", 64), Ciphertext: ct}); rec.Code != 200 {
		t.Fatalf("其他实例不受影响：%d", rec.Code)
	}
}

func TestParseKeys(t *testing.T) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	p8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if _, err := ParseAPNsKey(p8); err != nil {
		t.Fatal(err)
	}
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	rder, _ := x509.MarshalPKCS8PrivateKey(rk)
	sa, _ := json.Marshal(map[string]string{"project_id": "demo", "client_email": "a@b", "token_uri": "https://oauth2.googleapis.com/token",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rder}))})
	c, err := ParseFCMServiceAccount(sa)
	if err != nil || c.ProjectID != "demo" {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := ParseFCMServiceAccount([]byte(`{}`)); err == nil {
		t.Fatal("缺少字段应报错")
	}
}
