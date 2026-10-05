package server

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// pairTestDevice 直接经 Store 创建 AK 并配对，返回设备凭证（权限矩阵等测试用）。
func pairTestDevice(t *testing.T, s *Server, lowRisk bool, scopeType, scopeValue string) appTokens {
	t.Helper()
	now := time.Now()
	k := AppAccessKey{Name: "test", ScopeType: scopeType, ScopeValue: scopeValue, AllowLowRiskOps: lowRisk, MaxDevices: 1,
		ExpiresAt: now.Add(time.Hour).Unix()}
	key, err := s.store.CreateAppAccessKey(&k, now)
	if err != nil {
		t.Fatal(err)
	}
	_, tok, err := s.store.PairDevice(PairRequest{KeyHash: HashToken(key), Name: "phone", Platform: "ios"}, now)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

type pairResp struct {
	DeviceID         int64  `json:"device_id"`
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
	Scope            struct {
		Type            string `json:"type"`
		Value           string `json:"value"`
		AllowLowRiskOps bool   `json:"allow_low_risk_ops"`
	} `json:"scope"`
}

func createAppKey(t *testing.T, h http.Handler, admin, body string) (string, string, AppAccessKey) {
	t.Helper()
	rec := do(h, "POST", "/api/v1/app-access-keys", admin, []byte(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("创建 AK %d %s", rec.Code, rec.Body)
	}
	var v struct {
		AccessKey string       `json:"access_key"`
		PairURL   string       `json:"pair_url"`
		Key       AppAccessKey `json:"app_access_key"`
	}
	json.Unmarshal(rec.Body.Bytes(), &v)
	return v.AccessKey, v.PairURL, v.Key
}

func pair(t *testing.T, h http.Handler, key string) (*httptest.ResponseRecorder, pairResp) {
	t.Helper()
	rec := do(h, "POST", "/api/v1/app/pair", "", []byte(`{"access_key":"`+key+`","device":{"name":"Tom's iPhone","platform":"ios","app_version":"1.0.0"}}`))
	var v pairResp
	json.Unmarshal(rec.Body.Bytes(), &v)
	return rec, v
}

// 完整流程：创建 AK → 配对 → 只读访问（范围内）→ 刷新轮换 → 吊销（设计 8.4、12.3～12.7、19.2～19.4）
func TestAppPairingFlow(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, a, _ := createNode(t, h, admin, `{"name":"tokyo","group":"jp"}`)
	_, b, _ := createNode(t, h, admin, `{"name":"sg","group":"sg"}`)

	// 校验
	if rec := do(h, "POST", "/api/v1/app-access-keys", admin, []byte(`{"name":"","max_devices":11,"expires_in_days":91}`)); rec.Code != 422 {
		t.Fatalf("应校验名称、设备数与有效期：%d", rec.Code)
	}
	key, url, k := createAppKey(t, h, admin, `{"name":"Tom iPhone","scope_type":"group","group":"jp"}`)
	if !accessKeyFormat.MatchString(key) || k.Status != "active" || k.MaxDevices != 1 || !k.AllowLowRiskOps ||
		!strings.HasPrefix(url, "monitor://pair?ak=MNT-") || !strings.Contains(url, "&server=http") {
		t.Fatalf("AK %s %s %+v", key, url, k)
	}
	if k.Hint != key[:8]+"-****-"+key[len(key)-4:] {
		t.Fatalf("脱敏 %s", k.Hint)
	}
	var n int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM app_access_keys WHERE key_hash = ? AND hint NOT LIKE ?`, HashToken(key), "%"+key[9:13]+"%").Scan(&n)
	if n != 1 {
		t.Fatal("【安全】只应保存 AK 的哈希与脱敏提示")
	}

	// 错误的 AK：统一返回 access_key_invalid
	if rec, _ := pair(t, h, "MNT-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA"); rec.Code != 400 || decodeError(t, rec).Code != "access_key_invalid" {
		t.Fatalf("错误 AK %d %s", rec.Code, rec.Body)
	}
	// 配对（大小写与空格不敏感）
	rec, p := pair(t, h, " "+strings.ToLower(key)+" ")
	if rec.Code != 200 || !strings.HasPrefix(p.AccessToken, PrefixDevice) || !strings.HasPrefix(p.RefreshToken, PrefixRefresh) ||
		p.ExpiresIn != 1800 || p.Scope.Type != "group" || p.Scope.Value != "jp" || !p.Scope.AllowLowRiskOps {
		t.Fatalf("配对 %d %s", rec.Code, rec.Body)
	}
	// AK 默认一次性
	if rec, _ := pair(t, h, key); rec.Code != 400 {
		t.Fatalf("一次性 AK 不能再次配对：%d", rec.Code)
	}

	// 只读访问：只看到范围内的节点；范围外 404；管理接口一律 401
	dev := p.AccessToken
	var list struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	json.Unmarshal(do(h, "GET", "/api/v1/servers", dev, nil).Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].ID != a.ServerID {
		t.Fatalf("范围内只有 tokyo：%+v", list.Items)
	}
	if rec := do(h, "GET", "/api/v1/servers/"+itoa(b.ServerID), dev, nil); rec.Code != 404 {
		t.Fatalf("范围外节点应为 404：%d", rec.Code)
	}
	for _, rt := range []struct{ m, p string }{{"POST", "/api/v1/servers"}, {"GET", "/api/v1/app-devices"},
		{"PUT", "/api/v1/servers/" + itoa(a.ServerID)}, {"GET", "/api/v1/auth/me"}, {"POST", "/api/v1/upgrade-tasks"}} {
		if rec := do(h, rt.m, rt.p, dev, []byte(`{}`)); rec.Code != 401 {
			t.Errorf("【安全】设备凭证不能访问 %s %s：%d", rt.m, rt.p, rec.Code)
		}
	}
	var me struct {
		Name  string `json:"name"`
		Scope struct {
			Type string `json:"type"`
		} `json:"scope"`
	}
	json.Unmarshal(do(h, "GET", "/api/v1/app/me", dev, nil).Body.Bytes(), &me)
	if me.Name != "Tom's iPhone" || me.Scope.Type != "group" {
		t.Fatalf("me %+v", me)
	}

	// Access Token 过期：token_expired（应刷新）
	s.store.DB.Exec(`UPDATE app_devices SET access_expires_at = 1 WHERE id = ?`, p.DeviceID)
	if rec := do(h, "GET", "/api/v1/servers", dev, nil); rec.Code != 401 || decodeError(t, rec).Code != "token_expired" {
		t.Fatalf("过期 %d %s", rec.Code, rec.Body)
	}
	// 刷新：轮换，旧 Access Token 失效
	refresh := func(rt string) (*httptest.ResponseRecorder, pairResp) {
		rec := do(h, "POST", "/api/v1/app/token/refresh", "", []byte(`{"refresh_token":"`+rt+`","app_version":"1.0.1"}`))
		var v pairResp
		json.Unmarshal(rec.Body.Bytes(), &v)
		return rec, v
	}
	rec, p2 := refresh(p.RefreshToken)
	if rec.Code != 200 || p2.AccessToken == dev || p2.RefreshToken == p.RefreshToken || p2.DeviceID != p.DeviceID {
		t.Fatalf("刷新 %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/v1/servers", p2.AccessToken, nil); rec.Code != 200 {
		t.Fatalf("新 Access Token %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/v1/servers", dev, nil); rec.Code != 401 {
		t.Fatalf("旧 Access Token 应失效：%d", rec.Code)
	}
	// 宽限期内重复使用旧 Refresh Token（上次响应丢失）：重新签发，上一对作废
	rec, p3 := refresh(p.RefreshToken)
	if rec.Code != 200 {
		t.Fatalf("宽限期内重试 %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/v1/servers", p2.AccessToken, nil); rec.Code != 401 {
		t.Fatalf("被取代的 Access Token 应失效：%d", rec.Code)
	}
	if rec, _ := refresh(p2.RefreshToken); rec.Code != 401 {
		t.Fatalf("被取代的 Refresh Token 应失效：%d", rec.Code)
	}
	// 宽限期后再次使用旧 Refresh Token：视为泄露，吊销设备
	s.store.DB.Exec(`UPDATE app_devices SET rotated_at = rotated_at - 120 WHERE id = ?`, p.DeviceID)
	if rec, _ := refresh(p.RefreshToken); rec.Code != 401 || decodeError(t, rec).Code != "token_revoked" {
		t.Fatalf("重复使用 %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/v1/servers", p3.AccessToken, nil); rec.Code != 401 || decodeError(t, rec).Code != "token_revoked" {
		t.Fatalf("【安全】重复使用后设备应被吊销：%d %s", rec.Code, rec.Body)
	}
	var devs struct {
		Items []AppDevice `json:"items"`
	}
	json.Unmarshal(do(h, "GET", "/api/v1/app-devices", admin, nil).Body.Bytes(), &devs)
	if len(devs.Items) != 1 || devs.Items[0].Status != "revoked" || devs.Items[0].RevokedBy != "refresh_reuse" ||
		devs.Items[0].AppVersion != "1.0.1" || devs.Items[0].AccessKeyName != "Tom iPhone" {
		t.Fatalf("设备列表 %+v", devs.Items)
	}
	var ks struct {
		Items []AppAccessKey `json:"items"`
	}
	json.Unmarshal(do(h, "GET", "/api/v1/app-access-keys", admin, nil).Body.Bytes(), &ks)
	if len(ks.Items) != 1 || ks.Items[0].Status != "used" || ks.Items[0].PairedDevices != 1 {
		t.Fatalf("AK 列表 %+v", ks.Items)
	}
}

// 管理员吊销设备、App 解除配对、吊销 AK 时连带吊销设备（设计 12.7、19.4）
func TestAppRevoke(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	key, _, k := createAppKey(t, h, admin, `{"name":"family","max_devices":3,"expires_in_days":7}`)
	_, p1 := pair(t, h, key)
	_, p2 := pair(t, h, key)
	_, p3 := pair(t, h, key)
	if rec, _ := pair(t, h, key); rec.Code != 400 {
		t.Fatalf("超过最大设备数 %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/v1/app-devices/"+itoa(p1.DeviceID)+"/revoke", admin, nil); rec.Code != 204 {
		t.Fatalf("吊销设备 %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/v1/servers", p1.AccessToken, nil); rec.Code != 401 || decodeError(t, rec).Code != "token_revoked" {
		t.Fatalf("吊销后 %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/v1/app/token/refresh", "", []byte(`{"refresh_token":"`+p1.RefreshToken+`"}`)); rec.Code != 401 {
		t.Fatalf("【安全】吊销后不能刷新：%d", rec.Code)
	}
	// App 主动解除
	if rec := do(h, "POST", "/api/v1/app/unpair", p2.AccessToken, nil); rec.Code != 204 {
		t.Fatalf("解除 %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/v1/app/me", p2.AccessToken, nil); rec.Code != 401 {
		t.Fatalf("解除后 %d", rec.Code)
	}
	// 吊销 AK，不连带设备：p3 照常
	rec := do(h, "POST", "/api/v1/app-access-keys/"+itoa(k.ID)+"/revoke", admin, []byte(`{}`))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"revoked_devices":0`) {
		t.Fatalf("吊销 AK %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/v1/servers", p3.AccessToken, nil); rec.Code != 200 {
		t.Fatalf("只吊销 AK 时设备照常：%d", rec.Code)
	}
	if rec := do(h, "POST", "/api/v1/app-access-keys/"+itoa(k.ID)+"/revoke", admin, nil); rec.Code != 404 {
		t.Fatalf("重复吊销 %d", rec.Code)
	}
	// 连带吊销
	key2, _, k2 := createAppKey(t, h, admin, `{"name":"x"}`)
	_, p4 := pair(t, h, key2)
	rec = do(h, "POST", "/api/v1/app-access-keys/"+itoa(k2.ID)+"/revoke", admin, []byte(`{"revoke_devices":true}`))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"revoked_devices":1`) {
		t.Fatalf("连带吊销 %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/v1/servers", p4.AccessToken, nil); rec.Code != 401 {
		t.Fatalf("连带吊销后 %d", rec.Code)
	}
	// 过期的 AK 不能配对
	key3, _, k3 := createAppKey(t, h, admin, `{"name":"y"}`)
	s.store.DB.Exec(`UPDATE app_access_keys SET expires_at = 1 WHERE id = ?`, k3.ID)
	if rec, _ := pair(t, h, key3); rec.Code != 400 {
		t.Fatalf("过期 AK %d", rec.Code)
	}
}

// 低风险操作：只能静音 / 维护授权范围内的单个节点（设计 8.4.1、17.3）
func TestAppSilences(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, a, _ := createNode(t, h, admin, `{"name":"tokyo","group":"jp"}`)
	_, b, _ := createNode(t, h, admin, `{"name":"sg","group":"sg"}`)
	dev := pairTestDevice(t, s, true, "servers", itoa(a.ServerID)).access
	readonly := pairTestDevice(t, s, false, "all", "").access

	body := func(id int64) []byte {
		return []byte(`{"kind":"maintenance","scope_type":"server","scope_id":"` + itoa(id) + `","duration":"1h"}`)
	}
	rec := do(h, "POST", "/api/v1/silences", dev, body(a.ServerID))
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"created_by":"app:phone"`) {
		t.Fatalf("范围内维护 %d %s", rec.Code, rec.Body)
	}
	var x Silence
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec := do(h, "POST", "/api/v1/silences", dev, body(b.ServerID)); rec.Code != 404 {
		t.Fatalf("范围外应为 404：%d", rec.Code)
	}
	if rec := do(h, "POST", "/api/v1/silences", dev, []byte(`{"kind":"mute","scope_type":"global"}`)); rec.Code != 422 {
		t.Fatalf("设备不能全局静音：%d", rec.Code)
	}
	if rec := do(h, "POST", "/api/v1/silences", readonly, body(a.ServerID)); rec.Code != 403 {
		t.Fatalf("不允许低风险操作的设备应为 403：%d", rec.Code)
	}
	// 管理员在范围外节点上的维护：设备看不到、也不能结束
	rec = do(h, "POST", "/api/v1/silences", admin, body(b.ServerID))
	var other Silence
	json.Unmarshal(rec.Body.Bytes(), &other)
	var list struct {
		Items []Silence `json:"items"`
	}
	json.Unmarshal(do(h, "GET", "/api/v1/silences", dev, nil).Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].ID != x.ID {
		t.Fatalf("设备只看到范围内的记录：%+v", list.Items)
	}
	if rec := do(h, "DELETE", "/api/v1/silences/"+itoa(other.ID), dev, nil); rec.Code != 404 {
		t.Fatalf("不能结束范围外的记录：%d", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/v1/silences/"+itoa(x.ID), dev, nil); rec.Code != 204 {
		t.Fatalf("结束范围内的记录 %d", rec.Code)
	}
}

// 设备的 WebSocket：只收到范围内的事件；吊销后以 1008 关闭，且不能再连接（设计 19.4）
func TestAppWebSocketRevoked(t *testing.T) {
	old := wsPingInterval
	wsPingInterval = 50 * time.Millisecond
	defer func() { wsPingInterval = old }()
	s, h, _ := testServer(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	tok := pairTestDevice(t, s, true, "all", "")
	conn, br, res := wsDial(t, srv, tok.access, "")
	if res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("握手 %d", res.StatusCode)
	}
	d, _ := s.store.LookupDeviceByAccess(tok.access, time.Now())
	s.store.RevokeAppDevice(d.ID, "admin", time.Now())
	for {
		op, p := wsServerFrame(t, conn, br)
		if op == 0x9 {
			continue
		}
		if op != 0x8 || binary.BigEndian.Uint16(p) != 1008 {
			t.Fatalf("期望 1008，得到 op=%d %v", op, p)
		}
		break
	}
	if _, _, res := wsDial(t, srv, tok.access, ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("吊销后握手应为 401，得到 %d", res.StatusCode)
	}
}

// 配对按 IP 限流：失败过多临时封禁（设计 23.6）
func TestAppPairRateLimit(t *testing.T) {
	_, h, _ := testServer(t)
	var last int
	for i := 0; i < 30; i++ {
		rec, _ := pair(t, h, "MNT-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA")
		last = rec.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("应限流，最后一次 %d", last)
	}
}
