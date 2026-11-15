package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// 三网延迟的组装（修订第 328 条）：默认第一个地区、指定地区、找不到时退回、不显示；每小时一格。
func TestBuildPublicLatency(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	f := func(v float64) *float64 { return &v }
	targets := []ProbeTargetRow{
		{ID: 1, ISP: "intl", Province: "Tokyo"}, // 境外不算
		{ID: 2, ISP: "ct", Province: "上海"},
		{ID: 3, ISP: "ct", Province: "北京"},
		{ID: 4, ISP: "cu", Province: "北京"},
		{ID: 5, ISP: "cm", Province: "北京"},
		{ID: 6, ISP: "cu", Province: "上海"},
	}
	pts := map[int64][]LatencyPoint{
		1: {{TS: now.Unix() - 60, Latency: f(50)}},
		2: {{TS: now.Unix() - 60, Latency: f(30)}},
		3: {{TS: now.Unix() - 60, Latency: f(40)}, {TS: now.Unix() - 120, Latency: f(60)}, {TS: now.Unix() - 5*3600, Latency: f(300), Loss: 10}},
		4: {{TS: now.Unix() - 60, Loss: 100}},           // 全部失败：不可达
		6: {{TS: now.Unix() - 25*3600, Latency: f(10)}}, // 24 小时以外，不进任何一格
	}
	if got := latencyRegions(targets, pts); strings.Join(got, ",") != "上海,北京" {
		t.Fatalf("地区：%v", got)
	}
	l := buildPublicLatency(targets, pts, "", now)
	if l == nil || l.Region != "上海" || len(l.Rows) != 2 || l.Rows[0].ISP != "ct" || l.Rows[1].ISP != "cu" {
		t.Fatalf("默认第一个地区：%+v", l)
	}
	if l.Rows[1].Latency != nil || l.Rows[1].Loss != nil {
		t.Fatalf("超过 24 小时的结果不显示：%+v", l.Rows[1])
	}
	l = buildPublicLatency(targets, pts, "北京", now)
	if l == nil || l.Region != "北京" || len(l.Rows) != 2 {
		t.Fatalf("指定地区：%+v", l)
	}
	ct := l.Rows[0]
	if len(ct.Blocks) != 24 || ct.Latency == nil || *ct.Latency != 50 || ct.Loss == nil || *ct.Loss != 0 {
		t.Fatalf("最近 1 小时：%+v", ct)
	}
	if b := ct.Blocks[18]; b.Latency == nil || *b.Latency != 300 || *b.Loss != 10 {
		t.Fatalf("5 小时前在倒数第 6 格：%+v", ct.Blocks)
	}
	if b := ct.Blocks[0]; b.Latency != nil || b.Loss != nil {
		t.Fatalf("没有结果的小时为空：%+v", b)
	}
	if cu := l.Rows[1]; cu.ISP != "cu" || cu.Latency != nil || cu.Loss == nil || *cu.Loss != 100 {
		t.Fatalf("不可达：%+v", cu)
	}
	if l := buildPublicLatency(targets, pts, "广州", now); l == nil || l.Region != "上海" {
		t.Fatalf("没有该地区时退回第一个：%+v", l)
	}
	if buildPublicLatency(targets, pts, publicLatencyOff, now) != nil {
		t.Fatal("off 不显示")
	}
	if buildPublicLatency(targets, map[int64][]LatencyPoint{1: pts[1]}, "", now) != nil {
		t.Fatal("只有境外结果时不显示")
	}
}

// 接口：展示页带出三网延迟，不含目标地址；设置页列出可选地区，保存后生效，非法值拒绝。
func TestStatusPageLatencyAPI(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	id, _, _ := s.store.CreateServer("hk", 0, 1)
	now := time.Now()
	s.latest[id] = &snapshot{ReceivedAt: now, At: now}
	addTarget := func(isp, prov, addr string) int64 {
		res, err := s.store.DB.Exec(`INSERT INTO probe_targets (isp, province, address, proto, port, enabled, builtin, note, server_id)
			VALUES (?, ?, ?, 'icmp', 0, 1, 0, '', 0)`, isp, prov, addr)
		if err != nil {
			t.Fatal(err)
		}
		n, _ := res.LastInsertId()
		return n
	}
	sh := addTarget("ct", "上海", "198.51.100.11")
	bj := addTarget("cm", "北京", "198.51.100.12")
	for _, tid := range []int64{sh, bj} {
		if _, err := s.store.DB.Exec(`INSERT INTO probe_results (server_id, target_id, ts, latency_ms, loss, error) VALUES (?, ?, ?, 42, 0, '')`,
			id, tid, now.Unix()-30); err != nil {
			t.Fatal(err)
		}
	}
	put := func(body string) int {
		return do(h, "PUT", "/api/v1/settings/status-page", admin, []byte(body)).Code
	}
	if c := put(fmt.Sprintf(`{"enabled":true,"subscriptions":false,"server_ids":[%d]}`, id)); c != 200 {
		t.Fatalf("开启：%d", c)
	}
	public := func() (string, *publicLatency) {
		s.statusPage.clear()
		rec := do(h, "GET", "/api/v1/public/servers", "", nil)
		var v struct {
			Items []publicServer `json:"items"`
		}
		json.Unmarshal(rec.Body.Bytes(), &v)
		if len(v.Items) != 1 {
			t.Fatalf("公开节点：%s", rec.Body)
		}
		return rec.Body.String(), v.Items[0].Latency
	}
	body, l := public()
	if l == nil || l.Region != "上海" || len(l.Rows) != 1 || *l.Rows[0].Latency != 42 {
		t.Fatalf("默认第一个地区：%s", body)
	}
	if strings.Contains(body, "198.51.100") || strings.Contains(body, "icmp") {
		t.Fatalf("不能公开目标地址：%s", body)
	}

	var view statusPageView
	json.Unmarshal(do(h, "GET", "/api/v1/settings/status-page", admin, nil).Body.Bytes(), &view)
	if len(view.Latency) != 1 || view.Latency[0].ServerID != id || strings.Join(view.Latency[0].Regions, ",") != "上海,北京" {
		t.Fatalf("可选地区：%+v", view.Latency)
	}

	if c := put(fmt.Sprintf(`{"enabled":true,"subscriptions":false,"server_ids":[%d],"latency":[{"server_id":%d,"region":"北京"}]}`, id, id)); c != 200 {
		t.Fatalf("指定地区：%d", c)
	}
	if _, l := public(); l == nil || l.Region != "北京" || l.Rows[0].ISP != "cm" {
		t.Fatalf("指定北京：%+v", l)
	}
	// 省略 latency：设置不变
	if c := put(fmt.Sprintf(`{"enabled":true,"subscriptions":false,"server_ids":[%d]}`, id)); c != 200 {
		t.Fatalf("保存：%d", c)
	}
	if _, l := public(); l == nil || l.Region != "北京" {
		t.Fatalf("省略时不变：%+v", l)
	}
	if c := put(fmt.Sprintf(`{"enabled":true,"subscriptions":false,"server_ids":[%d],"latency":[{"server_id":%d,"region":"off"}]}`, id, id)); c != 200 {
		t.Fatalf("不显示：%d", c)
	}
	if body, l := public(); l != nil || strings.Contains(body, `"latency"`) {
		t.Fatalf("off 时不显示：%s", body)
	}
	if c := put(fmt.Sprintf(`{"enabled":true,"subscriptions":false,"server_ids":[%d],"latency":[{"server_id":9999,"region":""}]}`, id)); c != 422 {
		t.Fatalf("不存在的节点：%d", c)
	}
	if c := put(fmt.Sprintf(`{"enabled":true,"subscriptions":false,"server_ids":[%d],"latency":[{"server_id":%d,"region":"%s"}]}`, id, id, strings.Repeat("长", 41))); c != 422 {
		t.Fatalf("过长的地区名：%d", c)
	}
}
