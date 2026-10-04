package server

import (
	"fmt"
	"testing"
	"time"
)

// 计费时区（设计 5.4）：流量按节点时区归入日期，计费周期按节点时区计算；无效时区返回 422。
func TestTrafficTimezone(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	for _, c := range []struct{ tz, want string }{{"Mars/Olympus", "422"}, {"Local", "422"}} {
		rec := do(h, "POST", "/api/v1/servers", admin, []byte(fmt.Sprintf(`{"name":"tz-bad","traffic_timezone":%q}`, c.tz)))
		if fmt.Sprint(rec.Code) != c.want {
			t.Errorf("时区 %q：%d，应为 %s", c.tz, rec.Code, c.want)
		}
	}
	_, n, _ := createNode(t, h, admin, `{"name":"tz-la","traffic_timezone":"America/Los_Angeles"}`)
	row, err := s.store.GetServer(n.ServerID)
	if err != nil || row.TrafficTimezone != "America/Los_Angeles" {
		t.Fatalf("应保存计费时区：%+v %v", row, err)
	}
	la, _ := time.LoadLocation("America/Los_Angeles")

	// 上报的流量按节点时区归入日期
	_, res := enroll(h, n.EnrollCode, "la-host", "la-machine")
	tok := res.AgentToken
	now := time.Now().Unix()
	post := func(rx uint64) {
		body := fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"},"network":[{"interface":"eth0","ifindex":2,"rx_bytes":%d,"tx_bytes":0}]}`, now, rx)
		if rec := do(h, "POST", "/api/v1/agent/report", tok, []byte(body)); rec.Code != 204 {
			t.Fatalf("上报失败：%d", rec.Code)
		}
	}
	post(1000)
	post(5000)
	s.flush()
	var day string
	s.store.DB.QueryRow(`SELECT day FROM traffic_daily WHERE server_id = ?`, row.ID).Scan(&day)
	if want := time.Unix(now, 0).In(la).Format("2006-01-02"); day != want {
		t.Errorf("流量应归入洛杉矶时间的 %s，实际 %s", want, day)
	}

	// UTC 10 月 1 日 03:00 在洛杉矶仍是 9 月 30 日：本周期从 9 月 1 日开始
	v, err := s.trafficOf(*row, time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC))
	if err != nil || v.CycleStart != "2026-09-01" || v.CycleEnd != "2026-10-01" {
		t.Errorf("计费周期应按节点时区：%s～%s %v", v.CycleStart, v.CycleEnd, err)
	}
	if loc := trafficLocation(ServerRow{}); loc != time.Local {
		t.Error("未设置时区时为面板本地时区")
	}
}
