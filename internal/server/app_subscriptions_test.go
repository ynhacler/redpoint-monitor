package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"testing"
)

// App 订阅列表（修订第 213 条）：只有“全部节点”授权的设备能看到列表与图标；只读；Web 会话不能访问 App 接口。
func TestAppSubscriptions(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	for _, b := range []string{
		`{"name":"iCloud","category":"cloud","price_cents":600,"currency":"CNY","cycle":"monthly","start_date":"2026-01-05"}`,
		`{"name":"Netflix","brand":"netflix","category":"video","price_cents":6800,"currency":"HKD","cycle":"monthly","start_date":"2026-01-02"}`,
	} {
		if rec := do(h, "POST", "/api/v1/subscriptions", admin, []byte(b)); rec.Code != 201 {
			t.Fatalf("添加：%d %s", rec.Code, rec.Body)
		}
	}
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	var pngBody bytes.Buffer
	png.Encode(&pngBody, img)
	if rec := do(h, "PUT", "/api/v1/subscriptions/1/logo", admin, pngBody.Bytes()); rec.Code != 200 {
		t.Fatalf("上传图标：%d %s", rec.Code, rec.Body)
	}

	keyAll, _, _ := createAppKey(t, h, admin, `{"name":"all"}`)
	_, all := pair(t, h, keyAll)
	rec := do(h, "GET", "/api/v1/app/subscriptions", all.AccessToken, nil)
	var got appSubscriptionsView
	json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != 200 || got.Limited || len(got.Items) != 2 {
		t.Fatalf("列表：%d %s", rec.Code, rec.Body)
	}
	if got.Items[0].NextCharge == "" || got.Items[0].NextCharge > got.Items[1].NextCharge {
		t.Errorf("应按下次扣费日排序：%s", rec.Body)
	}
	if rec := do(h, "GET", "/api/v1/app/subscriptions/1/logo", all.AccessToken, nil); rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("图标：%d", rec.Code)
	}

	// 分组授权：limited，列表为空，图标 404
	keyGroup, _, _ := createAppKey(t, h, admin, `{"name":"g","scope_type":"group","group":"jp"}`)
	_, g := pair(t, h, keyGroup)
	rec = do(h, "GET", "/api/v1/app/subscriptions", g.AccessToken, nil)
	var lim appSubscriptionsView
	json.Unmarshal(rec.Body.Bytes(), &lim)
	if rec.Code != 200 || !lim.Limited || len(lim.Items) != 0 {
		t.Errorf("分组授权：%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/v1/app/subscriptions/1/logo", g.AccessToken, nil); rec.Code != 404 {
		t.Errorf("分组授权读取图标应为 404：%d", rec.Code)
	}
	// 【安全】只读：设备凭证不能写订阅；Web 会话不能访问 App 接口
	if rec := do(h, "POST", "/api/v1/subscriptions", all.AccessToken, []byte(`{"name":"x"}`)); rec.Code != 401 && rec.Code != 403 {
		t.Errorf("设备凭证不应能添加订阅：%d", rec.Code)
	}
	if rec := do(h, "GET", "/api/v1/app/subscriptions", admin, nil); rec.Code != 401 {
		t.Errorf("Web 会话应被拒绝：%d", rec.Code)
	}
}

// App 新增订阅（修订第 217 条）：只有 AK 勾选“允许记录订阅”且授权全部节点的设备可以新增；不能修改、删除；记审计日志。
func TestAppCreateSubscription(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	body := []byte(`{"name":"Netflix","brand":"netflix","category":"video","price_cents":6800,"currency":"HKD","cycle":"monthly","start_date":"2026-01-05"}`)

	// 默认不允许
	keyDefault, _, k := createAppKey(t, h, admin, `{"name":"default"}`)
	if k.AllowSubscriptions {
		t.Fatal("AK 默认不应允许记录订阅")
	}
	_, def := pair(t, h, keyDefault)
	if rec := do(h, "POST", "/api/v1/app/subscriptions", def.AccessToken, body); rec.Code != 403 {
		t.Errorf("未允许的设备应被拒绝：%d", rec.Code)
	}
	// 允许记录订阅，但只授权一个分组：订阅属于整个面板，仍拒绝
	keyGroup, _, _ := createAppKey(t, h, admin, `{"name":"g","scope_type":"group","group":"jp","allow_subscriptions":true}`)
	_, g := pair(t, h, keyGroup)
	if g.Scope.AllowSubscriptions {
		t.Error("分组授权的设备 scope.allow_subscriptions 应为 false")
	}
	if rec := do(h, "POST", "/api/v1/app/subscriptions", g.AccessToken, body); rec.Code != 403 {
		t.Errorf("分组授权的设备应被拒绝：%d", rec.Code)
	}

	keySubs, _, _ := createAppKey(t, h, admin, `{"name":"subs","allow_subscriptions":true}`)
	_, d := pair(t, h, keySubs)
	if !d.Scope.AllowSubscriptions {
		t.Fatal("配对结果应带 scope.allow_subscriptions")
	}
	if rec := do(h, "POST", "/api/v1/app/subscriptions", d.AccessToken, []byte(`{"name":"","category":"x"}`)); rec.Code != 422 {
		t.Errorf("校验与 Web 相同：%d %s", rec.Code, rec.Body)
	}
	rec := do(h, "POST", "/api/v1/app/subscriptions", d.AccessToken, body)
	var x Subscription
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 201 || x.ID == 0 || x.NextCharge == "" {
		t.Fatalf("新增：%d %s", rec.Code, rec.Body)
	}
	rec = do(h, "GET", "/api/v1/app/subscriptions", d.AccessToken, nil)
	var got appSubscriptionsView
	json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Items) != 1 || got.Items[0].Name != "Netflix" {
		t.Errorf("列表：%s", rec.Body)
	}
	// 【安全】只能新增：修改、删除仍只有 Web 管理员
	if rec := do(h, "PUT", "/api/v1/subscriptions/"+itoa64(x.ID), d.AccessToken, body); rec.Code != 401 && rec.Code != 403 {
		t.Errorf("设备不应能修改订阅：%d", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/v1/subscriptions/"+itoa64(x.ID), d.AccessToken, nil); rec.Code != 401 && rec.Code != 403 {
		t.Errorf("设备不应能删除订阅：%d", rec.Code)
	}
	// 审计：操作者为 App 设备
	logs, _, _ := s.store.ListAudit(AuditQuery{Action: "subscription.create", Limit: 10})
	if len(logs) != 1 || logs[0].ActorType != "app" || logs[0].ActorID == "" {
		t.Errorf("审计：%+v", logs)
	}
}
