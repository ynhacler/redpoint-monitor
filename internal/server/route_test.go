package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

func TestClassifyRoute(t *testing.T) {
	cases := []struct {
		isp  string
		hops string
		want string
	}{
		{"ct", "10.0.0.1 59.43.180.9 59.43.246.1", "CN2 GIA"},
		{"ct", "10.0.0.1 59.43.180.9 202.97.50.1", "CN2 GT"},
		{"ct", "203.0.113.1 202.97.12.1 202.97.33.5", "163"},
		{"ct", "1.1.1.1 8.8.8.8", ""},
		{"cu", "219.158.8.5 219.158.3.1", "4837"},
		{"cu", "218.105.2.1 210.51.12.2", "9929"},
		{"cu", "210.51.12.2 219.158.3.1", "9929"},
		{"cm", "223.120.19.1 221.183.2.1", "CMIN2"}, // 223.120.19.0/24 比 223.120.0.0/15 更长：CMIN2，不是 CMI
		{"cm", "223.119.8.1 221.183.2.1", "CMI"},
		{"cm", "221.183.2.1", "CMNET"},
		{"ct", "223.118.2.1 221.183.2.1", "CMI"}, // 到电信的回程经过移动 CMI：按其他运营商的规则识别
		{"ct", "", ""},
	}
	for _, c := range cases {
		var addrs []netip.Addr
		for _, f := range strings.Fields(c.hops) {
			addrs = append(addrs, netip.MustParseAddr(f))
		}
		if got := classifyRoute(c.isp, addrs); got != c.want {
			t.Errorf("classifyRoute(%s, %s) = %q，应为 %q", c.isp, c.hops, got, c.want)
		}
	}
	if n, ok := lookupRouteNet(netip.MustParseAddr("223.120.140.7")); !ok || n.asn != 58807 {
		t.Errorf("最长前缀匹配：%+v", n)
	}
}

func TestPickTraceTargets(t *testing.T) {
	list := []ProbeTargetRow{
		{ID: 1, ISP: "intl", Address: "1.1.1.1"},
		{ID: 2, ISP: "ct", Address: "240e::1"},
		{ID: 3, ISP: "ct", Address: "202.96.1.1"},
		{ID: 4, ISP: "cm", Address: "speed.cm.example.cn"},
		{ID: 5, ISP: "cu", Address: "2408::1"},
		{ID: 6, ISP: "ct", Address: "202.96.1.2"},
	}
	ids := func(l []ProbeTargetRow) (out []int64) {
		for _, x := range l {
			out = append(out, x.ID)
		}
		return
	}
	// 电信优先 IPv4（3）；联通只有 IPv6（5）；移动为域名（4）；境外不追踪
	if got := fmt.Sprint(ids(pickTraceTargets(list, nil))); got != "[3 5 4]" {
		t.Errorf("选择：%s", got)
	}
	// 未下发的目标不选
	if got := fmt.Sprint(ids(pickTraceTargets(list, func(id int64) bool { return id != 3 && id != 4 }))); got != "[6 5]" {
		t.Errorf("只从已下发的目标中选：%s", got)
	}
}

func TestCleanHops(t *testing.T) {
	ms := func(v float64) *float64 { return &v }
	got := cleanHops([]protocol.TraceHop{{TTL: 1, IP: "10.0.0.1", RTT: ms(1)}, {TTL: 1, IP: "10.0.0.2"}, {TTL: 2, IP: "bad", RTT: ms(3)},
		{TTL: 3, IP: "59.43.1.1", RTT: ms(-1)}, {TTL: 31, IP: "1.1.1.1"}})
	if len(got) != 3 || got[1].IP != "" || got[1].RTT != nil || got[2].RTT != nil || *got[0].RTT != 1 {
		t.Errorf("%+v", got)
	}
}

// 面板为每个运营商选一个目标下发追踪；Agent 带回的结果写入数据库，节点的回程线路接口返回识别结果（修订第 282 条）。
func TestRouteTraces(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, tok, _ := s.store.CreateServer("route-1", 0, 1)
	ids := addCNTargets(t, s, 2, 2) // 两个电信目标
	res, err := s.store.DB.Exec(`INSERT INTO probe_targets (isp, province, address, proto, port, enabled, builtin, note, server_id)
		VALUES ('cu', '北京', '123.125.1.1', 'icmp', 0, 1, 0, '', 0)`)
	if err != nil {
		t.Fatal(err)
	}
	cu, _ := res.LastInsertId()
	s.probes.invalidate()
	report := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/agent/report", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header[protocol.HeaderProbeVersion] = []string{""}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 204 {
			t.Fatalf("上报失败：%d %s", rec.Code, rec.Body)
		}
		return rec
	}
	hd := report(`{"system":{"boot_id":"b"}}`).Header()
	if got := hd.Get(protocol.HeaderProbeTrace); got != fmt.Sprintf("%d,%d", ids[0], cu) {
		t.Fatalf("应追踪第一个电信目标与联通目标：%q", got)
	}
	// 追踪前：两个目标都在列表中，尚未收到结果
	var v RoutesView
	rec := do(h, "GET", "/api/v1/servers/1/routes", admin, nil)
	if rec.Code != 200 {
		t.Fatalf("routes：%d %s", rec.Code, rec.Body)
	}
	json.Unmarshal(rec.Body.Bytes(), &v)
	if len(v.Items) != 2 || v.Items[0].At != 0 || len(v.Lines) != 0 || v.Status != "on" {
		t.Fatalf("追踪前：%+v", v)
	}
	now := time.Now().Unix()
	report(fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"},"traces":[
		{"target_id":%d,"at":%d,"reached":true,"hops":[{"ttl":1,"ip":"10.0.0.1","rtt_ms":0.5},{"ttl":2},{"ttl":3,"ip":"59.43.180.9","rtt_ms":40}]},
		{"target_id":%d,"at":%d,"error":"icmp_unavailable"}]}`, now, ids[0], now-30, cu, now-30))
	s.flush()
	rec = do(h, "GET", "/api/v1/servers/1/routes", admin, nil)
	v = RoutesView{}
	json.Unmarshal(rec.Body.Bytes(), &v)
	if len(v.Items) != 2 || v.Items[0].Line != "CN2 GIA" || !v.Items[0].Reached || len(v.Items[0].Hops) != 3 ||
		v.Items[0].Hops[2].ASN != 4809 || v.Items[0].Hops[1].IP != "" || v.Items[1].Error != "icmp_unavailable" ||
		fmt.Sprint(v.Lines) != "[CN2 GIA]" {
		t.Fatalf("追踪后：%s", rec.Body)
	}
	// 旧的结果不覆盖新的
	report(fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"},"traces":[{"target_id":%d,"at":%d,"hops":[{"ttl":1,"ip":"202.97.1.1"}]}]}`,
		now, ids[0], now-3600))
	s.flush()
	v = RoutesView{}
	json.Unmarshal(do(h, "GET", "/api/v1/servers/1/routes", admin, nil).Body.Bytes(), &v)
	if v.Items[0].Line != "CN2 GIA" {
		t.Errorf("旧结果不应覆盖：%+v", v.Items[0])
	}
	// 实时状态中不保留追踪结果
	if strings.Contains(do(h, "GET", "/api/v1/servers/1", admin, nil).Body.String(), `"traces"`) {
		t.Error("节点详情的 latest 中不应包含 traces")
	}
	// 删除目标后记录一起删除
	if rec := do(h, "DELETE", fmt.Sprintf("/api/v1/probe-targets/%d", ids[0]), admin, nil); rec.Code/100 != 2 {
		t.Fatalf("删除目标：%d %s", rec.Code, rec.Body)
	}
	var n int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM route_traces WHERE target_id = ?`, ids[0]).Scan(&n)
	if n != 0 {
		t.Error("删除目标后应删除其追踪记录")
	}
	if rec := do(h, "GET", "/api/v1/servers/99/routes", admin, nil); rec.Code != 404 {
		t.Errorf("不存在的节点：%d", rec.Code)
	}
}

// Agent 报告的公网地址只保留合法的公网地址（修订第 286 条）
func TestCleanIPStack(t *testing.T) {
	got := cleanIPStack(&protocol.IPStack{IPv4: true, IPv6: true, PublicIPv4: "154.17.2.9", PublicIPv6: "2403:18c0:5:2fb0::55a4"})
	if got.PublicIPv4 != "154.17.2.9" || got.PublicIPv6 != "2403:18c0:5:2fb0::55a4" || !got.IPv4 {
		t.Errorf("合法地址应保留：%+v", got)
	}
	for _, bad := range []protocol.IPStack{{PublicIPv4: "10.0.0.1", PublicIPv6: "fd00::1"}, {PublicIPv4: "<b>x</b>", PublicIPv6: "1.1.1.1"}} {
		if c := cleanIPStack(&bad); c.PublicIPv4 != "" || c.PublicIPv6 != "" {
			t.Errorf("应丢弃：%+v", c)
		}
	}
	if cleanIPStack(nil) != nil {
		t.Error("nil 应保持 nil")
	}
}
