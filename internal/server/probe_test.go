package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

// 目标随上报响应下发：版本相同不重复下发；旧版 Agent 不下发；本机关闭时记录状态；修改目标后版本变化（设计 42.3.2）。
func TestProbeSync(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, tok, _ := s.store.CreateServer("probe-1", 0, 1)
	report := func(version *string, body string) http.Header {
		req := httptest.NewRequest("POST", "/api/v1/agent/report", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		if version != nil {
			req.Header[protocol.HeaderProbeVersion] = []string{*version}
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 204 {
			t.Fatalf("上报失败：%d %s", rec.Code, rec.Body)
		}
		return rec.Header()
	}
	base := `{"system":{"boot_id":"b"}}`
	if hd := report(nil, base); hd.Get(protocol.HeaderProbeVersion) != "" {
		t.Error("旧版 Agent（不带请求头）不应下发")
	}
	empty := ""
	hd := report(&empty, base)
	ver, enc := hd.Get(protocol.HeaderProbeVersion), hd.Get(protocol.HeaderProbeTargets)
	targets := protocol.ParseProbeTargets(enc)
	if ver == "" || len(targets) != 12 || hd.Get(protocol.HeaderProbeInterval) != "300" {
		t.Fatalf("默认应下发北京、上海、广东、四川三网共 12 个目标，间隔 300 秒：%s %d %s", ver, len(targets), hd.Get(protocol.HeaderProbeInterval))
	}
	if hd := report(&ver, base); hd.Get(protocol.HeaderProbeVersion) != "" {
		t.Error("版本相同时不应重复下发")
	}
	// 修改测试频率：版本变化，下发新间隔；不在可选范围的返回 422
	if rec := do(h, "PUT", "/api/v1/settings/probe", admin, []byte(`{"interval_s":45}`)); rec.Code != 422 {
		t.Errorf("45 秒不在可选范围：%d", rec.Code)
	}
	if rec := do(h, "PUT", "/api/v1/settings/probe", admin, []byte(`{"interval_s":60}`)); rec.Code != 200 {
		t.Fatalf("修改频率：%d %s", rec.Code, rec.Body)
	}
	hd = report(&ver, base)
	if hd.Get(protocol.HeaderProbeInterval) != "60" || hd.Get(protocol.HeaderProbeVersion) == ver {
		t.Errorf("修改频率后应下发新版本与间隔：%q", hd.Get(protocol.HeaderProbeInterval))
	}
	ver = hd.Get(protocol.HeaderProbeVersion)
	var ps ProbeSettings
	json.Unmarshal(do(h, "GET", "/api/v1/settings/probe", admin, nil).Body.Bytes(), &ps)
	if ps.IntervalS != 60 {
		t.Errorf("读取设置：%+v", ps)
	}

	off := protocol.ProbeOff
	report(&off, base)
	if s.probeStatus(1) != "off" {
		t.Errorf("本机关闭时状态应为 off：%s", s.probeStatus(1))
	}

	// 停用一个目标后版本变化
	id := targets[0].ID
	if rec := do(h, "PUT", fmt.Sprintf("/api/v1/probe-targets/%d", id), admin, []byte(`{"enabled":false}`)); rec.Code != 200 {
		t.Fatalf("停用失败：%d %s", rec.Code, rec.Body)
	}
	hd = report(&ver, base)
	if v2 := hd.Get(protocol.HeaderProbeVersion); v2 == "" || v2 == ver || len(protocol.ParseProbeTargets(hd.Get(protocol.HeaderProbeTargets))) != 11 {
		t.Errorf("停用后应下发新版本（11 个目标）：%q", v2)
	}

	// 探测结果写入历史，节点的三网延迟接口返回最近 1 小时汇总与曲线
	now := time.Now().Unix()
	t2 := targets[1].ID
	body := fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"},"probes":[
		{"target_id":%d,"at":%d,"latency_ms":180.5,"loss":0},
		{"target_id":%d,"at":%d,"latency_ms":190,"loss":20},
		{"target_id":%d,"at":%d,"latency_ms":175,"loss":0},
		{"target_id":%d,"at":%d,"loss":100,"error":"timeout"}]}`,
		now, t2, now-600, t2, now-300, t2, now-90000, targets[2].ID, now-60)
	report(&ver, body)
	s.flush()
	var v LatencyView
	rec := do(h, "GET", "/api/v1/servers/1/latency", admin, nil)
	if rec.Code != 200 {
		t.Fatalf("latency：%d %s", rec.Code, rec.Body)
	}
	json.Unmarshal(rec.Body.Bytes(), &v)
	byID := map[int64]LatencyTarget{}
	for _, x := range v.Targets {
		byID[x.ID] = x
	}
	if v.Status != "on" || len(v.Targets) != 11 {
		t.Errorf("状态与目标数：%s %d", v.Status, len(v.Targets))
	}
	if x := byID[t2]; x.Latency == nil || *x.Latency != 185.25 || x.Loss == nil || *x.Loss != 10 || len(v.Series[fmt.Sprint(t2)]) != 2 {
		t.Errorf("最近 1 小时中位数与平均丢包：%+v 曲线 %d 点（超过 24 小时的不在 24h 曲线中）", x, len(v.Series[fmt.Sprint(t2)]))
	}
	if x := byID[targets[2].ID]; x.Latency != nil || x.LastError != "timeout" {
		t.Errorf("全部失败时延迟为空并给出原因：%+v", x)
	}
	if rec := do(h, "GET", "/api/v1/servers/1/latency?range=7d", admin, nil); rec.Code != 200 {
		t.Errorf("7d：%d", rec.Code)
	}
	if rec := do(h, "GET", "/api/v1/servers/1/latency?range=1y", admin, nil); rec.Code != 422 {
		t.Errorf("不支持的范围应返回 422：%d", rec.Code)
	}
	// 实时状态中不保留探测结果
	if strings.Contains(do(h, "GET", "/api/v1/servers/1", admin, nil).Body.String(), `"probes"`) {
		t.Error("节点详情的 latest 中不应包含 probes")
	}
}

func TestProbeTargetsAPI(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	var list struct{ Items []ProbeTargetRow }
	json.Unmarshal(do(h, "GET", "/api/v1/probe-targets", admin, nil).Body.Bytes(), &list)
	if len(list.Items) != 93 || !list.Items[0].Builtin || list.Items[0].Province != "北京" {
		t.Fatalf("应有 93 个内置目标，北京在前：%d %+v", len(list.Items), list.Items[0])
	}
	for _, bad := range []string{
		`{"isp":"ct","province":"家","address":"192.168.1.1"}`, // 【安全】内网
		`{"isp":"ct","province":"家","address":"127.0.0.1"}`,
		`{"isp":"ct","province":"家","address":"169.254.169.254"}`, // 云厂商元数据地址
		`{"isp":"ct","province":"家","address":"example.com"}`,     // 不接受域名
		`{"isp":"xx","province":"家","address":"1.1.1.1"}`,
		`{"isp":"ct","province":"家","address":"1.1.1.1","proto":"udp"}`,
	} {
		if rec := do(h, "POST", "/api/v1/probe-targets", admin, []byte(bad)); rec.Code != 422 {
			t.Errorf("%s 应返回 422：%d", bad, rec.Code)
		}
	}
	rec := do(h, "POST", "/api/v1/probe-targets", admin, []byte(`{"isp":"cm","province":"杭州","address":"::ffff:117.147.213.41","proto":"icmp","port":22}`))
	var c ProbeTargetRow
	json.Unmarshal(rec.Body.Bytes(), &c)
	if rec.Code != 201 || c.Address != "117.147.213.41" || c.Port != 0 || c.Builtin {
		t.Fatalf("添加自定义目标：%d %+v", rec.Code, c)
	}
	// 内置目标：地址等不可改，不能删除
	b := list.Items[0]
	rec = do(h, "PUT", fmt.Sprintf("/api/v1/probe-targets/%d", b.ID), admin, []byte(`{"address":"1.1.1.1","proto":"icmp"}`))
	var b2 ProbeTargetRow
	json.Unmarshal(rec.Body.Bytes(), &b2)
	if rec.Code != 200 || b2.Address != b.Address || b2.Proto != "icmp" {
		t.Errorf("内置目标只能改协议：%d %+v", rec.Code, b2)
	}
	if rec := do(h, "DELETE", fmt.Sprintf("/api/v1/probe-targets/%d", b.ID), admin, nil); rec.Code != 409 {
		t.Errorf("内置目标不能删除：%d", rec.Code)
	}
	if rec := do(h, "DELETE", fmt.Sprintf("/api/v1/probe-targets/%d", c.ID), admin, nil); rec.Code != 204 {
		t.Errorf("删除自定义目标：%d", rec.Code)
	}
	// 启用数不超过 64
	s.store.DB.Exec(`UPDATE probe_targets SET enabled = 1 WHERE id <= 64`)
	s.store.DB.Exec(`UPDATE probe_targets SET enabled = 0 WHERE id > 64`)
	if rec := do(h, "PUT", "/api/v1/probe-targets/70", admin, []byte(`{"enabled":true}`)); rec.Code != 422 {
		t.Errorf("超过 64 个启用目标应返回 422：%d", rec.Code)
	}
}

func TestLatencyMath(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	lat, loss := summarizeLatency([]LatencyPoint{{Latency: f(100), Loss: 0}, {Latency: f(300), Loss: 20}, {Latency: nil, Loss: 100}, {Latency: f(200)}})
	if lat == nil || *lat != 200 || loss == nil || *loss != 30 {
		t.Errorf("中位数 200、平均丢包 30：%v %v", lat, loss)
	}
	if lat, loss := summarizeLatency(nil); lat != nil || loss != nil {
		t.Error("没有数据时为空")
	}
	out := bucketLatency([]LatencyPoint{{TS: 3600, Latency: f(100)}, {TS: 3900, Latency: f(201), Loss: 20}, {TS: 7300, Loss: 100}}, 3600)
	if len(out) != 2 || *out[0].Latency != 150.5 || out[0].Loss != 10 || out[1].Latency != nil || out[1].TS != 7200 {
		t.Errorf("按小时汇总：%+v", out)
	}
}

// 每台节点可以跟随全局、自定义目标与频率，或关闭；修改后这台节点的下一份上报下发新列表（修订第 121 条）。
func TestNodeProbe(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, tok1, _ := s.store.CreateServer("np-1", 0, 1)
	_, tok2, _ := s.store.CreateServer("np-2", 0, 1)
	report := func(tok, have string) http.Header {
		req := httptest.NewRequest("POST", "/api/v1/agent/report", strings.NewReader(`{"system":{"boot_id":"b"}}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header[protocol.HeaderProbeVersion] = []string{have}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Header()
	}
	var np NodeProbe
	json.Unmarshal(do(h, "GET", "/api/v1/servers/1/probe", admin, nil).Body.Bytes(), &np)
	if np.Mode != "default" || np.TargetIDs == nil {
		t.Fatalf("默认跟随全局：%+v", np)
	}
	// 自定义：选一个默认未启用的目标（天津电信，id 13）与北京电信（id 1），频率 10 分钟
	if rec := do(h, "PUT", "/api/v1/servers/1/probe", admin, []byte(`{"mode":"custom","target_ids":[13,1],"interval_s":600}`)); rec.Code != 200 {
		t.Fatalf("保存自定义：%d %s", rec.Code, rec.Body)
	}
	hd := report(tok1, "")
	got := protocol.ParseProbeTargets(hd.Get(protocol.HeaderProbeTargets))
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 13 || hd.Get(protocol.HeaderProbeInterval) != "600" {
		t.Errorf("节点 1 应只测自己选的 2 个目标、10 分钟：%+v %s", got, hd.Get(protocol.HeaderProbeInterval))
	}
	// 其他节点不受影响
	if n := len(protocol.ParseProbeTargets(report(tok2, "").Get(protocol.HeaderProbeTargets))); n != 12 {
		t.Errorf("节点 2 仍跟随全局 12 个：%d", n)
	}
	// 关闭：下发空列表（版本 0）
	do(h, "PUT", "/api/v1/servers/1/probe", admin, []byte(`{"mode":"off","target_ids":[5],"interval_s":60}`))
	hd = report(tok1, hd.Get(protocol.HeaderProbeVersion))
	if hd.Get(protocol.HeaderProbeVersion) != "0" || hd.Get(protocol.HeaderProbeTargets) != "" {
		t.Errorf("关闭后应下发空列表：%q %q", hd.Get(protocol.HeaderProbeVersion), hd.Get(protocol.HeaderProbeTargets))
	}
	json.Unmarshal(do(h, "GET", "/api/v1/servers/1/probe", admin, nil).Body.Bytes(), &np)
	if np.Mode != "off" || len(np.TargetIDs) != 0 || np.IntervalS != 0 {
		t.Errorf("关闭时不保存目标与频率：%+v", np)
	}
	var v LatencyView
	json.Unmarshal(do(h, "GET", "/api/v1/servers/1/latency", admin, nil).Body.Bytes(), &v)
	if v.Mode != "off" || len(v.Targets) != 0 {
		t.Errorf("关闭且没有数据时不列出目标：%s %d", v.Mode, len(v.Targets))
	}
	for _, bad := range []string{
		`{"mode":"custom","target_ids":[],"interval_s":0}`,
		`{"mode":"custom","target_ids":[99999],"interval_s":0}`,
		`{"mode":"custom","target_ids":[1,1],"interval_s":0}`,
		`{"mode":"custom","target_ids":[1],"interval_s":45}`,
		`{"mode":"x","target_ids":[],"interval_s":0}`,
	} {
		if rec := do(h, "PUT", "/api/v1/servers/1/probe", admin, []byte(bad)); rec.Code != 422 {
			t.Errorf("%s 应返回 422：%d", bad, rec.Code)
		}
	}
	if rec := do(h, "PUT", "/api/v1/servers/999/probe", admin, []byte(`{"mode":"off","target_ids":[],"interval_s":0}`)); rec.Code != 404 {
		t.Errorf("节点不存在应返回 404：%d", rec.Code)
	}
}

func TestEffectiveProbe(t *testing.T) {
	all := []ProbeTargetRow{{ID: 1, Enabled: true}, {ID: 2}, {ID: 3, Enabled: true}}
	ids := func(ts []ProbeTargetRow) (out []int64) {
		for _, t := range ts {
			out = append(out, t.ID)
		}
		return
	}
	if ts, iv := effectiveProbe(all, 300, parseNodeProbe("")); fmt.Sprint(ids(ts)) != "[1 3]" || iv != 300 {
		t.Errorf("跟随全局：%v %d", ids(ts), iv)
	}
	if ts, iv := effectiveProbe(all, 300, parseNodeProbe(`{"mode":"custom","target_ids":[2,9],"interval_s":60}`)); fmt.Sprint(ids(ts)) != "[2]" || iv != 60 {
		t.Errorf("自定义：已删除的目标忽略，未全局启用的也可以选：%v %d", ids(ts), iv)
	}
	if _, iv := effectiveProbe(all, 300, parseNodeProbe(`{"mode":"custom","target_ids":[1],"interval_s":0}`)); iv != 300 {
		t.Errorf("频率 0 跟随全局：%d", iv)
	}
	if ts, _ := effectiveProbe(all, 300, parseNodeProbe(`{"mode":"off"}`)); len(ts) != 0 {
		t.Error("关闭时没有目标")
	}
	if np := parseNodeProbe("{bad"); np.Mode != "default" {
		t.Error("无法解析时跟随全局")
	}
}
