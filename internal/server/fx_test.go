package server

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 汇率（修订第 223 条）：解析 ExchangeRate-API 的响应并换算为“1 单位外币折合多少人民币”；失败时保留上次的汇率；
// 只在有非人民币订阅时请求；12 小时内不重复请求，失败后 1 小时内不重试。
func TestFxRates(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	var calls atomic.Int32
	var fail atomic.Bool
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			http.Error(w, "down", 503)
			return
		}
		w.Write([]byte(`{"result":"success","base_code":"CNY","time_last_update_unix":1761177601,
			"rates":{"CNY":1,"USD":0.1404,"HKD":1.0925,"JPY":21.3,"bad":5,"EUR":0}}`))
	}))
	defer fake.Close()
	s.fxURL = fake.URL

	get := func() FxView {
		rec := do(h, "GET", "/api/v1/fx-rates", admin, nil)
		if rec.Code != 200 {
			t.Fatalf("fx-rates：%d %s", rec.Code, rec.Body)
		}
		var v FxView
		json.Unmarshal(rec.Body.Bytes(), &v)
		return v
	}
	// 没有非人民币订阅：不请求
	if v := get(); v.Rates["CNY"] != 1 || len(v.Rates) != 1 || v.AsOf != 0 {
		t.Errorf("初始：%+v", v)
	}
	if !s.fxDue(time.Now()) || s.fxNeeded() {
		t.Error("没有外币订阅时不需要汇率")
	}

	now := time.Now()
	s.refreshFx(context.Background(), now)
	v := get()
	if math.Abs(v.Rates["USD"]-1/0.1404) > 1e-9 || math.Abs(v.Rates["HKD"]-1/1.0925) > 1e-9 || v.AsOf != 1761177601 || v.Error != "" {
		t.Fatalf("汇率：%+v", v)
	}
	if _, ok := v.Rates["bad"]; ok {
		t.Error("非三位大写的币种应忽略")
	}
	if _, ok := v.Rates["EUR"]; ok {
		t.Error("汇率为 0 的币种应忽略")
	}
	if s.fxDue(now.Add(11 * time.Hour)) {
		t.Error("12 小时内不应再请求")
	}

	// 失败：保留上次的汇率，给出原因，1 小时内不重试
	fail.Store(true)
	later := now.Add(13 * time.Hour)
	s.refreshFx(context.Background(), later)
	v = get()
	if v.Error == "" || math.Abs(v.Rates["USD"]-1/0.1404) > 1e-9 {
		t.Errorf("失败后应保留汇率并给出原因：%+v", v)
	}
	if s.fxDue(later.Add(30*time.Minute)) || !s.fxDue(later.Add(61*time.Minute)) {
		t.Error("失败后 1 小时内不重试，之后再试")
	}

	// 重启后从数据库读出
	s.fx = fxState{}
	s.loadFx()
	if math.Abs(get().Rates["JPY"]-1/21.3) > 1e-9 {
		t.Error("重启后应沿用保存的汇率")
	}
	if calls.Load() != 2 {
		t.Errorf("请求次数：%d", calls.Load())
	}
}

// 订阅的国家 / 地区与支付方式必选（修订第 223 条）
func TestSubscriptionCountryPayment(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	base := `"name":"Netflix","category":"video","price_cents":100,"currency":"USD","cycle":"monthly","start_date":"2026-01-01"`
	rec := do(h, "POST", "/api/v1/subscriptions", admin, []byte(`{`+base+`,"country":"usa","payment":"cash"}`))
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "country") || !strings.Contains(rec.Body.String(), "payment") {
		t.Errorf("非法的国家与支付方式应被拒绝：%d %s", rec.Code, rec.Body)
	}
	rec = do(h, "POST", "/api/v1/subscriptions", admin, []byte(`{`+base+`,"country":"us","payment":"paypal"}`))
	var x Subscription
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 201 || x.Country != "US" || x.Payment != "paypal" {
		t.Fatalf("添加：%d %s", rec.Code, rec.Body)
	}
	if got, _ := s.store.GetSubscription(x.ID); got.Country != "US" || got.Payment != "paypal" {
		t.Errorf("保存：%+v", got)
	}
	if !s.fxNeeded() {
		t.Error("有美元订阅时需要汇率")
	}
}
