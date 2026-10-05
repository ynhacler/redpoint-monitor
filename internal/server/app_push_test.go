package server

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"vpsmon/internal/push"
	"vpsmon/internal/relay"
)

// fakeAPNs 记录收到的推送（只有密文）；token 以 d 开头时返回 410（App 已卸载）。
type fakeAPNs struct {
	mu   sync.Mutex
	got  map[string]string // device token → 密文
	srv  *httptest.Server
	hits int
}

func newFakeAPNs(t *testing.T) *fakeAPNs {
	f := &fakeAPNs{got: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			C string `json:"c"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		tok := strings.TrimPrefix(r.URL.Path, "/3/device/")
		f.mu.Lock()
		defer f.mu.Unlock()
		f.hits++
		if strings.HasPrefix(tok, "d") {
			w.WriteHeader(http.StatusGone)
			w.Write([]byte(`{"reason":"Unregistered"}`))
			return
		}
		f.got[tok] = body.C
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// 端到端：面板加密并签名 → 真实的 Relay 处理器校验并转发 → 假 APNs 只收到密文 → 设备私钥解密（设计 30）
func TestAppPushEndToEnd(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	apns := newFakeAPNs(t)
	apnsKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rl := relay.New(relay.Config{APNs: &relay.APNsConfig{KeyID: "K", TeamID: "T", Topic: "dev.vpsmon.app", Key: apnsKey, Endpoint: apns.srv.URL}})
	relaySrv := httptest.NewServer(rl.Handler())
	t.Cleanup(relaySrv.Close)
	s.push.relay = relaySrv.URL
	s.notify.backoff = []time.Duration{time.Millisecond}

	_, jp, _ := createNode(t, h, admin, `{"name":"tokyo","group":"jp"}`)
	_, sg, _ := createNode(t, h, admin, `{"name":"singapore","group":"sg"}`)

	// 两台设备：一台看全部节点，一台只看 jp 分组
	register := func(scope, value, token string) (*ecdh.PrivateKey, string) {
		priv, _ := ecdh.X25519().GenerateKey(rand.Reader)
		tok := pairTestDevice(t, s, true, scope, value).access
		rec := do(h, "PUT", "/api/v1/app/push", tok, []byte(`{"provider":"apns","token":"`+token+`","public_key":"`+
			base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes())+`"}`))
		if rec.Code != 204 {
			t.Fatalf("登记推送 %d %s", rec.Code, rec.Body)
		}
		return priv, tok
	}
	allTok, jpTok := strings.Repeat("a", 64), strings.Repeat("b", 64)
	allKey, allDev := register("all", "", allTok)
	jpKey, _ := register("group", "jp", jpTok)

	var me struct {
		PushAvailable bool   `json:"push_available"`
		PushEnabled   bool   `json:"push_enabled"`
		CenterID      string `json:"center_id"`
	}
	json.Unmarshal(do(h, "GET", "/api/v1/app/me", allDev, nil).Body.Bytes(), &me)
	if !me.PushAvailable || !me.PushEnabled || len(me.CenterID) != 16 {
		t.Fatalf("me %+v", me)
	}

	decrypt := func(priv *ecdh.PrivateKey, ct string) pushPayload {
		raw, _ := base64.StdEncoding.DecodeString(ct)
		plain, err := push.Open(priv, raw)
		if err != nil {
			t.Fatalf("设备无法解密：%v", err)
		}
		var p pushPayload
		json.Unmarshal(plain, &p)
		return p
	}

	// sg 节点的告警：只推给“全部节点”的设备
	s.notify.dispatch(notifyMessage{Kind: NotifyFiring, EventID: 7, ServerID: sg.ServerID, ServerName: "singapore", Type: "offline",
		Severity: SeverityCritical, Message: "超过 120 秒未收到上报", StartedAt: time.Now()})
	s.notify.wg.Wait()
	if _, ok := apns.got[jpTok]; ok {
		t.Fatal("【安全】范围外节点的告警不应推给 jp 设备（设计 17.3）")
	}
	p := decrypt(allKey, apns.got[allTok])
	if p.ServerName != "singapore" || p.Severity != "critical" || p.EventID != 7 || p.CenterID != me.CenterID ||
		!strings.Contains(p.Title, "120 秒") || !strings.Contains(p.Body, "开始时间") {
		t.Fatalf("payload %+v", p)
	}
	// Relay 与 APNs 看到的只有密文：明文中的节点名不出现在转发内容里
	if strings.Contains(apns.got[allTok], "singapore") {
		t.Fatal("【安全】转发内容不应包含明文")
	}

	// jp 节点的告警：两台都收到
	s.notify.dispatch(notifyMessage{Kind: NotifyFiring, EventID: 8, ServerID: jp.ServerID, ServerName: "tokyo", Type: "cpu",
		Severity: SeverityWarning, Message: "CPU 96%", StartedAt: time.Now()})
	s.notify.wg.Wait()
	if p := decrypt(jpKey, apns.got[jpTok]); p.ServerName != "tokyo" {
		t.Fatalf("jp 设备 %+v", p)
	}
	// 提示级不推送；流量阈值与流量预计超额例外（设计 1.5.8）
	before := apns.hits
	s.notify.dispatch(notifyMessage{Kind: NotifyFiring, ServerID: jp.ServerID, Type: "swap", Severity: SeverityInfo, Message: "x", StartedAt: time.Now()})
	s.notify.wg.Wait()
	if apns.hits != before {
		t.Fatal("提示级不应推送")
	}
	s.notify.dispatch(notifyMessage{Kind: NotifyFiring, EventID: 9, ServerID: jp.ServerID, ServerName: "tokyo", Type: "traffic_forecast",
		Severity: SeverityInfo, Message: "预计周期结束 1.2 TB，超过套餐 1 TB", StartedAt: time.Now()})
	s.notify.wg.Wait()
	if p := decrypt(jpKey, apns.got[jpTok]); p.EventID != 9 || !strings.Contains(p.Title, "预计") {
		t.Fatalf("流量预计超额应推送：%+v", p)
	}

	// 投递记录
	var dl struct {
		Items []Delivery `json:"items"`
	}
	json.Unmarshal(do(h, "GET", "/api/v1/notification-deliveries", admin, nil).Body.Bytes(), &dl)
	found := false
	for _, d := range dl.Items {
		if d.ChannelType == "app" && d.Status == DeliverySent {
			found = true
		}
	}
	if !found {
		t.Fatalf("应记录 App 推送的投递：%+v", dl.Items)
	}

	// Token 失效（App 已卸载）：Relay 返回 410，面板删除 Token，不重试
	_, goneDev := register("all", "", "d"+strings.Repeat("0", 63))
	s.notify.dispatch(notifyMessage{Kind: NotifyFiring, ServerID: jp.ServerID, ServerName: "tokyo", Severity: SeverityCritical,
		Message: "x", StartedAt: time.Now()})
	s.notify.wg.Wait()
	json.Unmarshal(do(h, "GET", "/api/v1/app/me", goneDev, nil).Body.Bytes(), &me)
	if me.PushEnabled {
		t.Fatal("失效的 Token 应被删除")
	}

	// 设备吊销：推送 Token 一并删除（设计 19.4）
	d, _ := s.store.LookupDeviceByAccess(allDev, time.Now())
	if rec := do(h, "POST", "/api/v1/app-devices/"+itoa(d.ID)+"/revoke", admin, nil); rec.Code != 204 {
		t.Fatalf("吊销 %d", rec.Code)
	}
	if s.store.HasPushDevice(d.ID) {
		t.Fatal("【安全】吊销后应删除推送 Token")
	}
}

func TestAppPushRegisterValidation(t *testing.T) {
	s, h, _ := testServer(t)
	tok := pairTestDevice(t, s, true, "all", "").access
	for body, field := range map[string]string{
		`{"provider":"wns","token":"x"}`:                               "provider",
		`{"provider":"apns","token":"zz","public_key":"AAAA"}`:         "token",
		`{"provider":"apns","token":"` + strings.Repeat("a", 64) + `"}`: "public_key", // 配对时没有提交公钥
	} {
		rec := do(h, "PUT", "/api/v1/app/push", tok, []byte(body))
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), `"field":"`+field) {
			t.Errorf("%s：%d %s", body, rec.Code, rec.Body)
		}
	}
	if rec := do(h, "DELETE", "/api/v1/app/push", tok, nil); rec.Code != 204 {
		t.Fatalf("关闭推送 %d", rec.Code)
	}
	if err := CheckPushRelay("http://push.example.com"); err == nil {
		t.Fatal("【安全】非回环的 http Relay 应拒绝（约束 6）")
	}
	if CheckPushRelay("https://push.example.com") != nil || CheckPushRelay("http://127.0.0.1:8090") != nil {
		t.Fatal("https 与回环地址应接受")
	}
}
