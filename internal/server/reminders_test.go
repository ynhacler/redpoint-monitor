package server

import (
	"strings"
	"testing"
	"time"

	"vpsmon/internal/cloud"
)

func TestExpireBracket(t *testing.T) {
	for days, want := range map[int]int{31: -99, 30: 30, 29: 30, 15: 30, 14: 14, 8: 14, 7: 7, 5: 7, 3: 3, 2: 3, 1: 1, 0: 0, -1: -1, -40: -1} {
		m, ok := expireBracket(days)
		if want == -99 {
			if ok {
				t.Errorf("剩 %d 天不应提醒", days)
			}
			continue
		}
		if !ok || m != want {
			t.Errorf("剩 %d 天：区间 %d（%v），应为 %d", days, m, ok, want)
		}
	}
}

// 到期提醒：每个区间一次；停机错过的里程碑按所在区间补发；已过期一次（设计 1.2.5）
func TestExpireReminders(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	rcv := newReceiver(t)
	c := NotifyChannel{Type: "webhook", Name: "wh", Enabled: true, MinSeverity: "warning", NotifyResolved: true,
		Config: channelConfig{URL: rcv.srv.URL + "/hook"}}
	if err := s.store.SaveChannel(&c, time.Now()); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Tokyo")
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, loc)
	_, n, _ := createNode(t, h, admin, `{"name":"tokyo","traffic_timezone":"Asia/Tokyo","expire_date":"2026-10-10"}`)
	_ = n

	check := func(at time.Time) {
		s.checkReminders(at)
		s.notify.wg.Wait()
	}
	check(now) // 剩 5 天：属于 7 天区间
	if rcv.count() != 1 {
		t.Fatalf("应发送 1 条，实际 %d", rcv.count())
	}
	_, body, _ := rcv.last()
	if !strings.Contains(body, "tokyo") || !strings.Contains(body, "2026-10-10") || !strings.Contains(body, "5 天后") {
		t.Fatalf("内容 %s", body)
	}
	check(now.Add(time.Hour)) // 同一区间：不重复
	check(now.AddDate(0, 0, 1))
	if rcv.count() != 1 {
		t.Fatalf("同一区间不应重复，实际 %d", rcv.count())
	}
	check(now.AddDate(0, 0, 3)) // 剩 2 天：3 天区间
	check(now.AddDate(0, 0, 5)) // 当天
	check(now.AddDate(0, 0, 7)) // 已过期
	check(now.AddDate(0, 0, 9)) // 已过期：不重复
	if rcv.count() != 4 {
		t.Fatalf("7 天、3 天、当天、已过期各一次，实际 %d", rcv.count())
	}
	if _, body, _ := rcv.last(); !strings.Contains(body, "已于 2026-10-10 到期") {
		t.Fatalf("过期提醒 %s", body)
	}
}

// 云账户提醒：超预算（每月一次）、流量包 90% / 95%、凭证失效；已由节点提醒的实例到期不重复（设计 44.6）
func TestCloudReminders(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	rcv := newReceiver(t)
	c := NotifyChannel{Type: "webhook", Name: "wh", Enabled: true, MinSeverity: "warning", NotifyResolved: true,
		Config: channelConfig{URL: rcv.srv.URL + "/hook"}}
	s.store.SaveChannel(&c, time.Now())
	_, v, _ := createCloudAccount(t, h, admin, awsAccountBody) // 预算 50 美元
	now := time.Now()
	forecast := int64(6000)
	s.store.SaveCloudCost(v.ID, cloud.Costs{Period: cloud.BillingMonth("aws", now), AmountCents: 3000, ForecastCents: &forecast, Currency: "USD"}, now)
	_, node, _ := createNode(t, h, admin, `{"name":"linked","expire_date":"2099-01-01"}`)
	s.store.ReplaceCloudInstances(v.ID, []cloud.Instance{
		{ID: "ls-1", Name: "ls-1", Kind: "lightsail", TrafficLimit: 1000},
		{ID: "i-exp", Name: "exp", Kind: "ec2", ExpireAt: now.Add(48 * time.Hour).Unix()},    // 未关联：提醒
		{ID: "i-linked", Name: "lnk", Kind: "ec2", ExpireAt: now.Add(48 * time.Hour).Unix()}, // 关联到有到期日的节点：不提醒
	}, now)
	s.store.SaveCloudTraffic(v.ID, []cloud.Instance{{ID: "ls-1", TrafficUsed: 920, TrafficLimit: 1000, TrafficPeriodStart: "2026-10-01"}}, now)
	insts, _ := s.store.CloudInstances(v.ID)
	for _, in := range insts {
		if in.InstanceID == "i-linked" {
			id := int64(node.ServerID)
			s.store.SetCloudInstanceServer(in.ID, &id)
		}
	}
	s.store.RecordCloudSync(v.ID, cloudSyncResult{Err: "AuthFailure（HTTP 401）", AuthFailed: true, NextTryAt: now.Unix()}, now)

	run := func() []string {
		s.checkReminders(now)
		s.notify.wg.Wait()
		rcv.mu.Lock()
		defer rcv.mu.Unlock()
		return append([]string(nil), rcv.bodies...)
	}
	bodies := strings.Join(run(), "\n")
	for _, want := range []string{"超过预算 USD 50.00", "本月预估 USD 60.00", "流量包已用 92%", "凭证失效", "云实例将于"} {
		if !strings.Contains(bodies, want) {
			t.Errorf("缺少提醒 %q：\n%s", want, bodies)
		}
	}
	if strings.Contains(bodies, "lnk") {
		t.Error("已关联且节点有到期日的实例不应重复提醒")
	}
	n := rcv.count()
	run()
	if rcv.count() != n {
		t.Fatal("再次检查不应重复发送")
	}
	// 流量包到 95%：再提醒一次（严重）
	s.store.SaveCloudTraffic(v.ID, []cloud.Instance{{ID: "ls-1", TrafficUsed: 960, TrafficLimit: 1000, TrafficPeriodStart: "2026-10-01"}}, now)
	run()
	if rcv.count() != n+1 {
		t.Fatalf("95%% 应再提醒一次：%d → %d", n, rcv.count())
	}
}
