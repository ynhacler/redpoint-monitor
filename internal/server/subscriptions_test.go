package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 扣费日规则（设计 47）：首次扣费 = 开始 + 试用；按月类以首次扣费日为锚点、小月取月末；结束日当天及之后不再扣费
func TestNextCharge(t *testing.T) {
	day := func(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
	cases := []struct {
		name  string
		sub   Subscription
		today string
		want  string
	}{
		{"每月，本月还没到", Subscription{Cycle: "monthly", StartDate: "2024-09-05"}, "2026-10-03", "2026-10-05"},
		{"每月，今天就是扣费日", Subscription{Cycle: "monthly", StartDate: "2024-09-05"}, "2026-10-05", "2026-10-05"},
		{"每月，本月已过", Subscription{Cycle: "monthly", StartDate: "2024-09-05"}, "2026-10-06", "2026-11-05"},
		{"每月 31 日，小月取月末", Subscription{Cycle: "monthly", StartDate: "2026-01-31"}, "2026-02-10", "2026-02-28"},
		{"每月 31 日，锚点不漂移", Subscription{Cycle: "monthly", StartDate: "2026-01-31"}, "2026-03-01", "2026-03-31"},
		{"每年", Subscription{Cycle: "annually", StartDate: "2024-10-26"}, "2026-10-27", "2027-10-26"},
		{"每季度", Subscription{Cycle: "quarterly", StartDate: "2026-01-15"}, "2026-05-01", "2026-07-15"},
		{"每周", Subscription{Cycle: "weekly", StartDate: "2026-10-01"}, "2026-10-09", "2026-10-15"},
		{"每周，今天", Subscription{Cycle: "weekly", StartDate: "2026-10-01"}, "2026-10-08", "2026-10-08"},
		{"试用 7 天：首次扣费延后", Subscription{Cycle: "monthly", StartDate: "2026-10-01", TrialDays: 7}, "2026-10-03", "2026-10-08"},
		{"试用后按首次扣费日递推", Subscription{Cycle: "monthly", StartDate: "2026-10-01", TrialDays: 7}, "2026-10-09", "2026-11-08"},
		{"还没开始", Subscription{Cycle: "monthly", StartDate: "2026-12-01"}, "2026-10-01", "2026-12-01"},
		{"一次性，已扣过", Subscription{Cycle: "once", StartDate: "2026-01-01"}, "2026-10-01", ""},
		{"一次性，还没到", Subscription{Cycle: "once", StartDate: "2026-11-01"}, "2026-10-01", "2026-11-01"},
		{"结束日之前", Subscription{Cycle: "monthly", StartDate: "2026-01-05", EndDate: "2026-12-01"}, "2026-10-06", "2026-11-05"},
		{"结束日当天及之后不扣费", Subscription{Cycle: "monthly", StartDate: "2026-01-05", EndDate: "2026-11-05"}, "2026-10-06", ""},
	}
	for _, c := range cases {
		got := nextCharge(c.sub, day(c.today))
		s := ""
		if !got.IsZero() {
			s = got.Format("2006-01-02")
		}
		if s != c.want {
			t.Errorf("%s：%s，期望 %s", c.name, s, c.want)
		}
	}
}

func TestSubscriptionsAPI(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)

	// 校验
	rec := do(h, "POST", "/api/v1/subscriptions", admin, []byte(`{"name":"","category":"x","price_cents":-1,"currency":"rmbb","cycle":"daily","start_date":"2026/1/1","remind_days":5}`))
	if rec.Code != 422 {
		t.Fatalf("应拒绝：%d %s", rec.Code, rec.Body)
	}
	var e struct{ Error errorPayload }
	json.Unmarshal(rec.Body.Bytes(), &e)
	fields := map[string]bool{}
	for _, d := range e.Error.Details {
		fields[d.Field] = true
	}
	for _, f := range []string{"name", "category", "price_cents", "currency", "cycle", "start_date", "remind_days"} {
		if !fields[f] {
			t.Errorf("缺少字段错误 %s：%s", f, rec.Body)
		}
	}

	// 添加：明天扣费、提前 1 天提醒
	start := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	rec = do(h, "POST", "/api/v1/subscriptions", admin, []byte(`{"name":"Netflix","brand":"netflix","category":"video","price_cents":6800,
		"currency":"cny","cycle":"monthly","start_date":"`+start+`","remind_days":1,"note":"家庭"}`))
	if rec.Code != 201 {
		t.Fatalf("添加：%d %s", rec.Code, rec.Body)
	}
	var x Subscription
	json.Unmarshal(rec.Body.Bytes(), &x)
	if x.ID == 0 || x.Currency != "CNY" || x.NextCharge != start || x.Ended {
		t.Fatalf("添加结果 %+v", x)
	}

	// 修改：设置结束日期为昨天 → 已结束、没有下次扣费
	end := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	rec = do(h, "PUT", "/api/v1/subscriptions/"+itoa64(x.ID), admin, []byte(`{"name":"Netflix","category":"video","price_cents":6800,
		"currency":"CNY","cycle":"monthly","start_date":"2026-01-01","end_date":"`+end+`"}`))
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 200 || !x.Ended || x.NextCharge != "" {
		t.Fatalf("修改：%d %+v", rec.Code, x)
	}
	if rec := do(h, "PUT", "/api/v1/subscriptions/9999", admin, []byte(`{"name":"a","category":"video","price_cents":1,"currency":"CNY","cycle":"monthly","start_date":"2026-01-01"}`)); rec.Code != 404 {
		t.Fatalf("不存在应为 404：%d", rec.Code)
	}

	// 提醒：再添加一个明天扣费、提前 1 天提醒的，只提醒一次
	do(h, "POST", "/api/v1/subscriptions", admin, []byte(`{"name":"iCloud","category":"cloud","price_cents":600,"currency":"CNY","cycle":"monthly","start_date":"`+start+`","remind_days":1}`))
	s.subscriptionReminders(time.Now())
	s.subscriptionReminders(time.Now())
	var n int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM reminders_sent WHERE key LIKE 'subscription:%'`).Scan(&n)
	if n != 1 {
		t.Fatalf("应提醒 1 次，实际 %d", n)
	}

	// 列表：有下次扣费的在前
	var list struct{ Items []Subscription }
	json.Unmarshal(do(h, "GET", "/api/v1/subscriptions", admin, nil).Body.Bytes(), &list)
	if len(list.Items) != 2 || list.Items[0].Name != "iCloud" || list.Items[1].Name != "Netflix" {
		t.Fatalf("列表 %+v", list.Items)
	}
	// 查看者不能读取
	if rec := do(h, "GET", "/api/v1/subscriptions", viewerToken(t, s, "viewer-sub"), nil); rec.Code != 403 {
		t.Fatalf("查看者应为 403：%d", rec.Code)
	}
	// 删除并记入操作日志
	if rec := do(h, "DELETE", "/api/v1/subscriptions/"+itoa64(x.ID), admin, nil); rec.Code != 204 {
		t.Fatalf("删除：%d", rec.Code)
	}
	if logs := do(h, "GET", "/api/v1/audit-logs?category=operation", admin, nil).Body.String(); !strings.Contains(logs, "subscription.delete") {
		t.Fatal("应记入操作日志")
	}
}

// 上传图标（修订第 189 条）：只接受 PNG、限制尺寸，重新编码后保存；SVG 等被拒绝；删除订阅时一起删除
func TestSubscriptionLogo(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	rec := do(h, "POST", "/api/v1/subscriptions", admin, []byte(`{"name":"Tello","category":"sim","price_cents":500,"currency":"USD","cycle":"monthly","start_date":"2026-01-01"}`))
	var x Subscription
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 201 || x.LogoVersion != 0 {
		t.Fatalf("添加：%d %s", rec.Code, rec.Body)
	}
	url := "/api/v1/subscriptions/" + itoa64(x.ID) + "/logo"
	pngOf := func(w, hgt int) []byte {
		img := image.NewNRGBA(image.Rect(0, 0, w, hgt))
		for i := range img.Pix {
			img.Pix[i] = 200
		}
		var b bytes.Buffer
		png.Encode(&b, img)
		return b.Bytes()
	}
	for name, body := range map[string][]byte{
		"SVG": []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"乱码":  []byte("not an image"),
		"太小":  pngOf(8, 8),
		"太大":  pngOf(600, 600),
	} {
		if rec := do(h, "PUT", url, admin, body); rec.Code != 422 {
			t.Errorf("%s 应被拒绝：%d %s", name, rec.Code, rec.Body)
		}
	}
	// 附加在 PNG 末尾的内容在重新编码后不再存在
	body := append(pngOf(128, 128), []byte("<script>trailing</script>")...)
	rec = do(h, "PUT", url, admin, body)
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 200 || x.LogoVersion == 0 {
		t.Fatalf("上传：%d %s", rec.Code, rec.Body)
	}
	rec = do(h, "GET", url, admin, nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
		bytes.Contains(rec.Body.Bytes(), []byte("trailing")) {
		t.Fatalf("读取：%d %v", rec.Code, rec.Header())
	}
	if _, err := png.Decode(bytes.NewReader(rec.Body.Bytes())); err != nil {
		t.Fatal("应为有效 PNG：", err)
	}
	var list struct{ Items []Subscription }
	json.Unmarshal(do(h, "GET", "/api/v1/subscriptions", admin, nil).Body.Bytes(), &list)
	if list.Items[0].LogoVersion == 0 || list.Items[0].Category != "sim" {
		t.Fatalf("列表应带图标版本：%+v", list.Items[0])
	}
	if rec := do(h, "GET", url, viewerToken(t, s, "viewer-logo"), nil); rec.Code != 403 {
		t.Fatalf("查看者应为 403：%d", rec.Code)
	}
	// 删除图标、再删除订阅
	rec = do(h, "DELETE", url, admin, nil)
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 200 || x.LogoVersion != 0 || do(h, "GET", url, admin, nil).Code != 404 {
		t.Fatalf("删除图标：%d %s", rec.Code, rec.Body)
	}
	do(h, "PUT", url, admin, pngOf(64, 64))
	do(h, "DELETE", "/api/v1/subscriptions/"+itoa64(x.ID), admin, nil)
	var n int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM subscription_logos`).Scan(&n)
	if n != 0 {
		t.Fatal("删除订阅时应删除图标")
	}
}

// SIM 电话卡的号码（修订第 189 条）：去掉空行与分隔符，校验区号与位数，最多 10 个
func TestSubscriptionPhones(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	body := `{"name":"Tello","brand":"sim-us-tello","category":"sim","price_cents":500,"currency":"USD","cycle":"monthly","start_date":"2026-01-01",
		"phones":[{"cc":"+1","number":"(650) 555-1234"},{"cc":"+1","number":""},{"cc":"+86","number":"138 0013 8000"}]}`
	rec := do(h, "POST", "/api/v1/subscriptions", admin, []byte(body))
	var x Subscription
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 201 || len(x.Phones) != 2 || x.Phones[0] != (SubPhone{"+1", "6505551234"}) || x.Phones[1] != (SubPhone{"+86", "13800138000"}) {
		t.Fatalf("号码：%d %s", rec.Code, rec.Body)
	}
	got, _ := s.store.GetSubscription(x.ID)
	if len(got.Phones) != 2 {
		t.Fatalf("应保存号码：%+v", got.Phones)
	}
	for name, phones := range map[string]string{
		"区号": `[{"cc":"86","number":"13800138000"}]`,
		"字母": `[{"cc":"+1","number":"650-CALL-NOW"}]`,
		"太短": `[{"cc":"+1","number":"12"}]`,
		"太多": "[" + strings.Repeat(`{"cc":"+1","number":"6505551234"},`, 10) + `{"cc":"+1","number":"6505551234"}]`,
	} {
		b := strings.Replace(body, body[strings.Index(body, `"phones":`):len(body)-1], `"phones":`+phones, 1)
		if rec := do(h, "POST", "/api/v1/subscriptions", admin, []byte(b)); rec.Code != 422 {
			t.Errorf("%s 应被拒绝：%d %s", name, rec.Code, rec.Body)
		}
	}
	// 不带号码的旧请求体：号码为空数组
	rec = do(h, "POST", "/api/v1/subscriptions", admin, []byte(`{"name":"Netflix","category":"video","price_cents":1,"currency":"USD","cycle":"monthly","start_date":"2026-01-01"}`))
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 201 || x.Phones == nil || len(x.Phones) != 0 {
		t.Fatalf("没有号码时应为空数组：%s", rec.Body)
	}
}

// VPS（修订第 198 条）：地址去空行、去重并规范化，配置随订阅保存；其他类别不保存 VPS 字段
func TestSubscriptionVPS(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	body := `{"name":"DMIT 东京","brand":"vps-dmit","category":"vps","price_cents":3699,"currency":"USD","cycle":"annually","start_date":"2026-03-01",
		"vps":{"ips":["203.0.113.7"," ","2001:DB8::1","203.0.113.7","::ffff:198.51.100.2"],"cpu":2,"memory_gb":0.5,"disk_gb":20,
		"bandwidth_mbps":500,"traffic_gb":1024,"location":" 东京 "}}`
	rec := do(h, "POST", "/api/v1/subscriptions", admin, []byte(body))
	var x Subscription
	json.Unmarshal(rec.Body.Bytes(), &x)
	want := SubVPS{IPs: []string{"203.0.113.7", "2001:db8::1", "198.51.100.2"}, CPU: 2, MemoryGB: 0.5, DiskGB: 20, BandwidthMbps: 500,
		TrafficGB: 1024, Location: "东京"}
	if rec.Code != 201 || !reflect.DeepEqual(x.VPS, want) {
		t.Fatalf("VPS：%d %s", rec.Code, rec.Body)
	}
	if got, _ := s.store.GetSubscription(x.ID); !reflect.DeepEqual(got.VPS, want) {
		t.Fatalf("应保存 VPS：%+v", got.VPS)
	}
	for name, vps := range map[string]string{
		"地址":  `{"ips":["203.0.113.300"]}`,
		"带区域": `{"ips":["fe80::1%eth0"]}`,
		"负数":  `{"cpu":-1}`,
		"太多": `{"ips":[` + strings.Join(func() []string {
			var a []string
			for i := 0; i < 17; i++ {
				a = append(a, fmt.Sprintf(`"10.0.0.%d"`, i+1))
			}
			return a
		}(), ",") + `]}`,
	} {
		b := body[:strings.Index(body, `"vps":`)] + `"vps":` + vps + `}`
		if rec := do(h, "POST", "/api/v1/subscriptions", admin, []byte(b)); rec.Code != 422 {
			t.Errorf("%s 应被拒绝：%d %s", name, rec.Code, rec.Body)
		}
	}
	// 其他类别：VPS 字段清空
	b := strings.Replace(body, `"category":"vps"`, `"category":"cloud"`, 1)
	rec = do(h, "POST", "/api/v1/subscriptions", admin, []byte(b))
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 201 || len(x.VPS.IPs) != 0 || x.VPS.CPU != 0 {
		t.Fatalf("其他类别不应保存 VPS：%s", rec.Body)
	}
}
