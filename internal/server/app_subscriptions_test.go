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
		`{"name":"iCloud","category":"cloud","price_cents":600,"currency":"CNY","country":"US","payment":"credit","cycle":"monthly","start_date":"2026-01-05"}`,
		`{"name":"Netflix","brand":"netflix","category":"video","price_cents":6800,"currency":"HKD","country":"US","payment":"credit","cycle":"monthly","start_date":"2026-01-02"}`,
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

// App 记录订阅（修订第 217、218 条）：只有 AK 勾选“允许记录订阅”且授权全部节点的设备可以新增、修改、删除；记审计日志。
func TestAppCreateSubscription(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	body := []byte(`{"name":"Netflix","brand":"netflix","category":"video","price_cents":6800,"currency":"HKD","country":"US","payment":"credit","cycle":"monthly","start_date":"2026-01-05"}`)

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
	// 【安全】Web 管理员的订阅接口不接受设备凭证；上传图标仍只有 Web
	if rec := do(h, "PUT", "/api/v1/subscriptions/"+itoa64(x.ID), d.AccessToken, body); rec.Code != 401 && rec.Code != 403 {
		t.Errorf("设备不应能调用 Web 的修改接口：%d", rec.Code)
	}
	if rec := do(h, "PUT", "/api/v1/subscriptions/"+itoa64(x.ID)+"/logo", d.AccessToken, nil); rec.Code != 401 && rec.Code != 403 {
		t.Errorf("设备不应能上传图标：%d", rec.Code)
	}
	// 修改与删除（修订第 218 条）：同样只有允许记录订阅的设备
	upd := []byte(`{"name":"Netflix 高级","brand":"netflix","category":"video","price_cents":9800,"currency":"HKD","country":"US","payment":"credit","cycle":"monthly","start_date":"2026-01-05"}`)
	if rec := do(h, "PUT", "/api/v1/app/subscriptions/"+itoa64(x.ID), def.AccessToken, upd); rec.Code != 403 {
		t.Errorf("未允许的设备不能修改：%d", rec.Code)
	}
	rec = do(h, "PUT", "/api/v1/app/subscriptions/"+itoa64(x.ID), d.AccessToken, upd)
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 200 || x.Name != "Netflix 高级" || x.PriceCents != 9800 {
		t.Fatalf("修改：%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "DELETE", "/api/v1/app/subscriptions/"+itoa64(x.ID), def.AccessToken, nil); rec.Code != 403 {
		t.Errorf("未允许的设备不能删除：%d", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/v1/app/subscriptions/"+itoa64(x.ID), d.AccessToken, nil); rec.Code != 204 {
		t.Fatalf("删除：%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "DELETE", "/api/v1/app/subscriptions/"+itoa64(x.ID), d.AccessToken, nil); rec.Code != 404 {
		t.Errorf("再次删除应为 404：%d", rec.Code)
	}
	// 审计：操作者为 App 设备
	for _, act := range []string{"subscription.create", "subscription.update", "subscription.delete"} {
		logs, _, _ := s.store.ListAudit(AuditQuery{Action: act, Limit: 10})
		if len(logs) != 1 || logs[0].ActorType != "app" || logs[0].ActorID == "" {
			t.Errorf("审计 %s：%+v", act, logs)
		}
	}
}

// 旧版 App 不认识自定义类别（修订第 315 条）：不带 ?categories=1 时看到“其他”，原样保存不覆盖原类别；新版 App 看到原键。
func TestAppLegacyCustomCategory(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	rec := do(h, "POST", "/api/v1/subscription-categories", admin, []byte(`{"name":"保险","color":"health"}`))
	var c SubCategoryItem
	json.Unmarshal(rec.Body.Bytes(), &c)
	base := `"name":"车险","price_cents":300000,"currency":"CNY","country":"CN","payment":"bank","cycle":"annually","start_date":"2026-01-01"`
	rec = do(h, "POST", "/api/v1/subscriptions", admin, []byte(`{`+base+`,"category":"`+c.Key+`"}`))
	var x Subscription
	json.Unmarshal(rec.Body.Bytes(), &x)
	key, _, _ := createAppKey(t, h, admin, `{"name":"subs","allow_subscriptions":true}`)
	_, d := pair(t, h, key)

	var old, cur appSubscriptionsView
	json.Unmarshal(do(h, "GET", "/api/v1/app/subscriptions", d.AccessToken, nil).Body.Bytes(), &old)
	if len(old.Items) != 1 || old.Items[0].Category != "other" || len(old.Categories) != 0 {
		t.Fatalf("旧版 App 应看到“其他”且没有自定义类别：%+v", old)
	}
	json.Unmarshal(do(h, "GET", "/api/v1/app/subscriptions?categories=1", d.AccessToken, nil).Body.Bytes(), &cur)
	if len(cur.Items) != 1 || cur.Items[0].Category != c.Key || len(cur.Categories) != 1 {
		t.Fatalf("新版 App 应看到自定义类别：%+v", cur)
	}

	// 旧版 App 改名并原样提交“其他”：类别保持不变，返回的仍是“其他”
	rec = do(h, "PUT", "/api/v1/app/subscriptions/"+itoa64(x.ID), d.AccessToken, []byte(`{`+base+`,"name":"车险 2027","category":"other"}`))
	var y Subscription
	json.Unmarshal(rec.Body.Bytes(), &y)
	if rec.Code != 200 || y.Category != "other" {
		t.Fatalf("旧版 App 修改：%d %s", rec.Code, rec.Body)
	}
	if got, _ := s.store.GetSubscription(x.ID); got.Category != c.Key || got.Name != "车险 2027" {
		t.Fatalf("旧版 App 保存不应覆盖自定义类别：%+v", got)
	}
	// 新版 App 明确改为“其他”时生效
	rec = do(h, "PUT", "/api/v1/app/subscriptions/"+itoa64(x.ID)+"?categories=1", d.AccessToken, []byte(`{`+base+`,"category":"other"}`))
	if got, _ := s.store.GetSubscription(x.ID); rec.Code != 200 || got.Category != "other" {
		t.Fatalf("新版 App 改为其他：%d %+v", rec.Code, got)
	}
	// Web 管理员不受影响
	do(h, "PUT", "/api/v1/subscriptions/"+itoa64(x.ID), admin, []byte(`{`+base+`,"category":"`+c.Key+`"}`))
	rec = do(h, "PUT", "/api/v1/subscriptions/"+itoa64(x.ID), admin, []byte(`{`+base+`,"category":"other"}`))
	if got, _ := s.store.GetSubscription(x.ID); rec.Code != 200 || got.Category != "other" {
		t.Fatalf("Web 改为其他：%d %+v", rec.Code, got)
	}
}
