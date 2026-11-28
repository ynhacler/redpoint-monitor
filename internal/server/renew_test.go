package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// 已续费（修订第 224 条）：从原日期按周期顺延，月末按目标月的最后一天；一次性、没有日期的不能续费。
func TestRenewDates(t *testing.T) {
	for _, c := range []struct{ expire, period, want string }{
		{"2026-10-28", "monthly", "2026-11-28"},
		{"2026-01-31", "monthly", "2026-02-28"},
		{"2026-10-28", "quarterly", "2027-01-28"},
		{"2026-10-28", "annually", "2027-10-28"},
		{"2024-02-29", "annually", "2025-02-28"},
		{"2026-10-28", "triennially", "2029-10-28"},
	} {
		if got, err := renewServerDate(c.expire, c.period); err != nil || got != c.want {
			t.Errorf("节点 %s %s → %s %v，应为 %s", c.expire, c.period, got, err, c.want)
		}
	}
	for _, c := range []struct{ expire, period string }{{"", "monthly"}, {"2026-10-28", "one_time"}, {"2026-10-28", ""}, {"bad", "monthly"}} {
		if _, err := renewServerDate(c.expire, c.period); err == nil {
			t.Errorf("节点 %q %q 不应能续费", c.expire, c.period)
		}
	}
	for _, c := range []struct{ end, cycle, want string }{
		{"2026-10-28", "weekly", "2026-11-04"},
		{"2026-10-28", "monthly", "2026-11-28"},
		{"2026-10-28", "semiannually", "2027-04-28"},
		{"2026-10-28", "annually", "2027-10-28"},
	} {
		if got, err := renewSubEnd(c.end, c.cycle); err != nil || got != c.want {
			t.Errorf("订阅 %s %s → %s %v，应为 %s", c.end, c.cycle, got, err, c.want)
		}
	}
	if _, err := renewSubEnd("", "monthly"); err == nil {
		t.Error("没有结束日期的订阅不需要续费")
	}
	if _, err := renewSubEnd("2026-10-28", "once"); err == nil {
		t.Error("一次性付费的订阅不能续费")
	}
}

// 接口：Web 管理员与 AK 允许记录订阅的 App 设备都能续费；其他设备不能；记审计日志。
func TestRenewAPI(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, srv, _ := createNode(t, h, admin, `{"name":"tokyo","expire_date":"2026-10-28","billing_period":"monthly"}`)
	_, noExp, _ := createNode(t, h, admin, `{"name":"free"}`)

	rec := do(h, "POST", "/api/v1/servers/"+itoa64(srv.ServerID)+"/renew", admin, nil)
	var got struct {
		ExpireDate string `json:"expire_date"`
		Previous   string `json:"previous"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != 200 || got.ExpireDate != "2026-11-28" || got.Previous != "2026-10-28" {
		t.Fatalf("续费：%d %s", rec.Code, rec.Body)
	}
	if row, _ := s.store.GetServer(srv.ServerID); row.ExpireDate != "2026-11-28" {
		t.Errorf("到期日应已保存：%s", row.ExpireDate)
	}
	if rec := do(h, "POST", "/api/v1/servers/"+itoa64(noExp.ServerID)+"/renew", admin, nil); rec.Code != 409 {
		t.Errorf("没有到期日的节点应返回 409：%d", rec.Code)
	}

	// App：允许记录订阅的设备可以续费；普通设备不能
	keySubs, _, _ := createAppKey(t, h, admin, `{"name":"subs","allow_subscriptions":true}`)
	_, d := pair(t, h, keySubs)
	if rec := do(h, "POST", "/api/v1/app/servers/"+itoa64(srv.ServerID)+"/renew", d.AccessToken, nil); rec.Code != 200 {
		t.Fatalf("App 续费：%d %s", rec.Code, rec.Body)
	}
	if row, _ := s.store.GetServer(srv.ServerID); row.ExpireDate != "2026-12-28" {
		t.Errorf("App 续费后：%s", row.ExpireDate)
	}
	keyPlain, _, _ := createAppKey(t, h, admin, `{"name":"plain"}`)
	_, plain := pair(t, h, keyPlain)
	if rec := do(h, "POST", "/api/v1/app/servers/"+itoa64(srv.ServerID)+"/renew", plain.AccessToken, nil); rec.Code != 403 {
		t.Errorf("未允许的设备应被拒绝：%d", rec.Code)
	}

	// 订阅：有结束日期的顺延一个周期
	rec = do(h, "POST", "/api/v1/subscriptions", admin, []byte(`{"name":"域名","category":"cloud","price_cents":8900,"currency":"CNY",
		"cycle":"annually","start_date":"2025-11-02","end_date":"2026-11-02","country":"CN","payment":"alipay"}`))
	var x Subscription
	json.Unmarshal(rec.Body.Bytes(), &x)
	rec = do(h, "POST", "/api/v1/app/subscriptions/"+itoa64(x.ID)+"/renew", d.AccessToken, nil)
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 200 || x.EndDate != "2027-11-02" || x.Country != "CN" {
		t.Fatalf("订阅续费：%d %s", rec.Code, rec.Body)
	}
	for act, n := range map[string]int{"server.renew": 2, "subscription.renew": 1} {
		if logs, _, _ := s.store.ListAudit(AuditQuery{Action: act, Limit: 10}); len(logs) != n {
			t.Errorf("审计 %s：%d 条", act, len(logs))
		}
	}
}

// 没有结束日期的订阅：“已续费”把下一个扣费日记为已付，下次扣费与提醒顺延；保存表单不清除标记；可以取消（修订第 330 条）。
func TestRenewRecurringSubscription(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	today := time.Now()
	start := today.AddDate(0, -1, 3).Format("2006-01-02") // 3 天后扣费，月付
	body := `{"name":"VPS","category":"vps","price_cents":3850,"currency":"CNY","cycle":"monthly","start_date":"` + start + `","remind_days":7,"country":"CN","payment":"alipay"}`
	rec := do(h, "POST", "/api/v1/subscriptions", admin, []byte(body))
	var x Subscription
	json.Unmarshal(rec.Body.Bytes(), &x)
	due := x.NextCharge
	if rec.Code != 201 || due == "" || x.RenewedThrough != "" {
		t.Fatalf("新增：%d %s", rec.Code, rec.Body)
	}
	dueT, _ := time.ParseInLocation("2006-01-02", due, time.Local)
	want := addMonthsClamped(dueT, 1).Format("2006-01-02")

	rec = do(h, "POST", "/api/v1/subscriptions/"+itoa64(x.ID)+"/renew", admin, nil)
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 200 || x.RenewedThrough != due || x.NextCharge != want || x.EndDate != "" {
		t.Fatalf("标记续费：%d %s（应为 %s → %s）", rec.Code, rec.Body, due, want)
	}
	// 已标记的扣费日不再提醒
	if items := upcomingSubReminders([]Subscription{x}, today, 14, defaultSubPush(), nil); len(items) != 0 {
		t.Fatalf("已续费的扣费日不应预告提醒：%+v", items)
	}
	// 保存表单（PUT）不清除标记
	rec = do(h, "PUT", "/api/v1/subscriptions/"+itoa64(x.ID), admin, []byte(strings.Replace(body, "3850", "4000", 1)))
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 200 || x.RenewedThrough != due || x.NextCharge != want {
		t.Fatalf("修改后应保留标记：%d %s", rec.Code, rec.Body)
	}
	// 列表同样按标记计算
	var list struct{ Items []Subscription }
	json.Unmarshal(do(h, "GET", "/api/v1/subscriptions", admin, nil).Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].NextCharge != want {
		t.Fatalf("列表：%+v", list.Items)
	}
	// 取消标记
	rec = do(h, "DELETE", "/api/v1/subscriptions/"+itoa64(x.ID)+"/renew", admin, nil)
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 200 || x.RenewedThrough != "" || x.NextCharge != due {
		t.Fatalf("取消：%d %s", rec.Code, rec.Body)
	}
	// 一次性付费：409
	rec = do(h, "POST", "/api/v1/subscriptions", admin, []byte(`{"name":"买断","category":"tools","price_cents":100,"currency":"CNY","cycle":"once","start_date":"2026-01-01","country":"CN","payment":"alipay"}`))
	var once Subscription
	json.Unmarshal(rec.Body.Bytes(), &once)
	if rec := do(h, "POST", "/api/v1/subscriptions/"+itoa64(once.ID)+"/renew", admin, nil); rec.Code != 409 {
		t.Errorf("一次性付费应返回 409：%d", rec.Code)
	}
	if logs, _, _ := s.store.ListAudit(AuditQuery{Action: "subscription.unrenew", Limit: 10}); len(logs) != 1 {
		t.Errorf("取消标记应记审计：%d", len(logs))
	}
}

// nextUnpaidCharge：标记之后的第一个扣费日；标记早于下次扣费时不影响（修订第 330 条）。
func TestNextUnpaidCharge(t *testing.T) {
	today := time.Date(2026, 11, 26, 0, 0, 0, 0, time.Local)
	sub := Subscription{Cycle: "monthly", StartDate: "2026-01-28"}
	for _, c := range []struct{ renewed, want string }{
		{"", "2026-11-28"},
		{"2026-10-28", "2026-11-28"}, // 旧的标记
		{"2026-11-28", "2026-12-28"},
		{"2026-12-28", "2027-01-28"}, // 连点两次
	} {
		sub.RenewedThrough = c.renewed
		if got := nextUnpaidCharge(sub, today).Format("2006-01-02"); got != c.want {
			t.Errorf("renewed=%q：%s，应为 %s", c.renewed, got, c.want)
		}
	}
}

// 终止订阅（修订第 343 条）：结束日期设为本期结束，之后不再扣费；撤销恢复原结束日期；在表单中改结束日期或续费后不再是终止。
func TestCancelSubscription(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	today := time.Now()
	start := today.AddDate(0, -1, 5).Format("2006-01-02") // 5 天后扣费，月付
	body := `{"name":"VPS","category":"vps","price_cents":3850,"currency":"CNY","cycle":"monthly","start_date":"` + start + `","remind_days":1,"country":"CN","payment":"alipay"}`
	var x Subscription
	json.Unmarshal(do(h, "POST", "/api/v1/subscriptions", admin, []byte(body)).Body.Bytes(), &x)
	due := x.NextCharge
	rec := do(h, "POST", "/api/v1/subscriptions/"+itoa64(x.ID)+"/cancel", admin, nil)
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 200 || !x.Cancelled || x.EndDate != due || x.NextCharge != "" || x.Ended {
		t.Fatalf("终止：%d %s（本期结束 %s）", rec.Code, rec.Body, due)
	}
	// 再点一次不变
	if rec := do(h, "POST", "/api/v1/subscriptions/"+itoa64(x.ID)+"/cancel", admin, nil); rec.Code != 200 {
		t.Fatalf("重复终止：%d", rec.Code)
	}
	// 撤销：恢复为没有结束日期
	rec = do(h, "DELETE", "/api/v1/subscriptions/"+itoa64(x.ID)+"/cancel", admin, nil)
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 200 || x.Cancelled || x.EndDate != "" || x.NextCharge != due {
		t.Fatalf("撤销：%d %s", rec.Code, rec.Body)
	}
	// 再终止后在表单中改结束日期：不再是终止
	do(h, "POST", "/api/v1/subscriptions/"+itoa64(x.ID)+"/cancel", admin, nil)
	later := today.AddDate(0, 3, 0).Format("2006-01-02")
	rec = do(h, "PUT", "/api/v1/subscriptions/"+itoa64(x.ID), admin, []byte(strings.Replace(body, `"remind_days":1`, `"remind_days":1,"end_date":"`+later+`"`, 1)))
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 200 || x.Cancelled || x.EndDate != later {
		t.Fatalf("改结束日期：%d %s", rec.Code, rec.Body)
	}
	// 有更晚的结束日期时终止：提前到本期结束；撤销恢复原结束日期
	rec = do(h, "POST", "/api/v1/subscriptions/"+itoa64(x.ID)+"/cancel", admin, nil)
	json.Unmarshal(rec.Body.Bytes(), &x)
	if !x.Cancelled || x.EndDate != due {
		t.Fatalf("有结束日期时终止：%s", rec.Body)
	}
	json.Unmarshal(do(h, "DELETE", "/api/v1/subscriptions/"+itoa64(x.ID)+"/cancel", admin, nil).Body.Bytes(), &x)
	if x.EndDate != later {
		t.Fatalf("撤销后应恢复 %s：%s", later, x.EndDate)
	}
	// 续费（有结束日期的顺延）也会清除终止
	do(h, "POST", "/api/v1/subscriptions/"+itoa64(x.ID)+"/cancel", admin, nil)
	json.Unmarshal(do(h, "POST", "/api/v1/subscriptions/"+itoa64(x.ID)+"/renew", admin, nil).Body.Bytes(), &x)
	if x.Cancelled {
		t.Fatalf("续费后不应再是终止：%+v", x)
	}
	// 一次性：409
	rec = do(h, "POST", "/api/v1/subscriptions", admin, []byte(`{"name":"买断","category":"tools","price_cents":100,"currency":"CNY","cycle":"once","start_date":"2026-01-01","country":"CN","payment":"alipay"}`))
	var once Subscription
	json.Unmarshal(rec.Body.Bytes(), &once)
	if rec := do(h, "POST", "/api/v1/subscriptions/"+itoa64(once.ID)+"/cancel", admin, nil); rec.Code != 409 {
		t.Errorf("一次性付费应返回 409：%d", rec.Code)
	}
	if logs, _, _ := s.store.ListAudit(AuditQuery{Action: "subscription.cancel", Limit: 10}); len(logs) < 3 {
		t.Errorf("终止应记审计：%d", len(logs))
	}
}
