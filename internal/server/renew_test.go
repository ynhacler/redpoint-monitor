package server

import (
	"encoding/json"
	"testing"
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
