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

// addCNTargets 添加 n 个共用的国内目标（内置国内目标已删除，修订第 128 条），前 enabled 个启用；返回 ID。
func addCNTargets(t *testing.T, s *Server, n, enabled int) []int64 {
	t.Helper()
	var ids []int64
	for i := 0; i < n; i++ {
		res, err := s.store.DB.Exec(`INSERT INTO probe_targets (isp, province, address, proto, port, enabled, builtin, note, server_id)
			VALUES ('ct', ?, ?, 'tcp', 80, ?, 0, '', 0)`, fmt.Sprintf("地区%d", i), fmt.Sprintf("1.0.%d.%d", i/250, i%250+1), i < enabled)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		ids = append(ids, id)
	}
	s.probes.invalidate()
	return ids
}

// 目标随上报响应下发：版本相同不重复下发；旧版 Agent 不下发；本机关闭时记录状态；修改目标后版本变化（设计 42.3.2）。
func TestProbeSync(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, tok, _ := s.store.CreateServer("probe-1", 0, 1)
	addCNTargets(t, s, 12, 12)
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
		t.Fatalf("应下发启用的 12 个目标，间隔 300 秒：%s %d %s", ver, len(targets), hd.Get(protocol.HeaderProbeInterval))
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
	if len(list.Items) != 0 {
		t.Fatalf("内置目标已全部删除，目标都由用户添加：%d", len(list.Items))
	}
	for _, bad := range []string{
		`{"isp":"ct","province":"家","address":"192.168.1.1"}`, // 【安全】内网
		`{"isp":"ct","province":"家","address":"127.0.0.1"}`,
		`{"isp":"ct","province":"家","address":"169.254.169.254"}`, // 云厂商元数据地址
		`{"isp":"ct","province":"家","address":"nas.lan"}`,          // 局域网名称
		`{"isp":"ct","province":"家","address":"localhost"}`,
		`{"isp":"ct","province":"家","address":"a.cn:8080"}`, // 端口单独填写
		`{"isp":"ct","province":"家","address":"fd00::1"}`,   // IPv6 内网
		`{"isp":"xx","province":"家","address":"1.1.1.1"}`,
		`{"isp":"ct","province":"家","address":"1.1.1.1","proto":"udp"}`,
	} {
		if rec := do(h, "POST", "/api/v1/probe-targets", admin, []byte(bad)); rec.Code != 422 {
			t.Errorf("%s 应返回 422：%d", bad, rec.Code)
		}
	}
	// 公网 IPv6 与域名（修订第 132 条）：粘贴的网址只保留域名
	for in, want := range map[string]string{`[2400:da00::6666]`: "2400:da00::6666", `https://Speed.Test.CN/`: "speed.test.cn"} {
		rec := do(h, "POST", "/api/v1/probe-targets", admin, []byte(fmt.Sprintf(`{"isp":"cu","province":"北京","address":%q}`, in)))
		var x ProbeTargetRow
		json.Unmarshal(rec.Body.Bytes(), &x)
		if rec.Code != 201 || x.Address != want {
			t.Errorf("添加 %s：%d %+v", in, rec.Code, x)
		}
		do(h, "DELETE", fmt.Sprintf("/api/v1/probe-targets/%d", x.ID), admin, nil)
	}
	if rec := do(h, "POST", "/api/v1/probe-targets", admin, []byte(`{"isp":"intl","province":"台北","address":"1.1.1.1","note":"Cloudflare"}`)); rec.Code != 201 {
		t.Errorf("可以添加境外自定义目标：%d %s", rec.Code, rec.Body)
	}
	rec := do(h, "POST", "/api/v1/probe-targets", admin, []byte(`{"isp":"cm","province":"杭州","address":"::ffff:117.147.213.41","proto":"icmp","port":22}`))
	var c ProbeTargetRow
	json.Unmarshal(rec.Body.Bytes(), &c)
	if rec.Code != 201 || c.Address != "117.147.213.41" || c.Port != 0 || c.Builtin {
		t.Fatalf("添加自定义目标：%d %+v", rec.Code, c)
	}
	// 内置目标（现在没有，保留规则以备以后）：地址等不可改，不能删除
	if _, err := s.store.DB.Exec(`INSERT INTO probe_targets (isp, province, address, proto, port, enabled, builtin, note, server_id)
		VALUES ('intl', '香港', '64.120.117.34', 'tcp', 80, 0, 1, 'Leaseweb', 0), ('intl', '东京', '108.61.201.151', 'tcp', 80, 0, 1, 'Vultr', 0)`); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(do(h, "GET", "/api/v1/probe-targets", admin, nil).Body.Bytes(), &list)
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
	// 只改“启用”：其他字段保持不变（自定义目标的地址、境外目标的提供商都不丢）
	for _, x := range []ProbeTargetRow{c, list.Items[1]} { // Items[1] 是东京（内置），Items[0] 上面改过协议
		rec = do(h, "PUT", fmt.Sprintf("/api/v1/probe-targets/%d", x.ID), admin, []byte(`{"enabled":true}`))
		var got ProbeTargetRow
		json.Unmarshal(rec.Body.Bytes(), &got)
		if rec.Code != 200 || !got.Enabled || got.Address != x.Address || got.Province != x.Province || got.Note != x.Note || got.Proto != x.Proto {
			t.Errorf("只改启用：%d %+v", rec.Code, got)
		}
	}
	if rec := do(h, "DELETE", fmt.Sprintf("/api/v1/probe-targets/%d", c.ID), admin, nil); rec.Code != 204 {
		t.Errorf("删除自定义目标：%d", rec.Code)
	}
	// 启用数不超过 64
	ids := addCNTargets(t, s, 65, 64)
	if rec := do(h, "PUT", fmt.Sprintf("/api/v1/probe-targets/%d", ids[64]), admin, []byte(`{"enabled":true}`)); rec.Code != 422 {
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
	ids := addCNTargets(t, s, 13, 12)
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
	// 自定义：选一个全局未启用的目标与第一个目标，频率 10 分钟
	if rec := do(h, "PUT", "/api/v1/servers/1/probe", admin, []byte(fmt.Sprintf(`{"mode":"custom","target_ids":[%d,%d],"interval_s":600}`, ids[12], ids[0]))); rec.Code != 200 {
		t.Fatalf("保存自定义：%d %s", rec.Code, rec.Body)
	}
	hd := report(tok1, "")
	got := protocol.ParseProbeTargets(hd.Get(protocol.HeaderProbeTargets))
	if len(got) != 2 || got[0].ID != ids[0] || got[1].ID != ids[12] || hd.Get(protocol.HeaderProbeInterval) != "600" {
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
	a := ids[0]
	for _, bad := range []string{
		`{"mode":"custom","target_ids":[],"interval_s":0}`,
		`{"mode":"custom","target_ids":[99999],"interval_s":0}`,
		fmt.Sprintf(`{"mode":"custom","target_ids":[%d,%d],"interval_s":0}`, a, a),
		fmt.Sprintf(`{"mode":"custom","target_ids":[%d],"interval_s":45}`, a),
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
	if ts, iv := effectiveProbe(all, 300, parseNodeProbe(""), 1); fmt.Sprint(ids(ts)) != "[1 3]" || iv != 300 {
		t.Errorf("跟随全局：%v %d", ids(ts), iv)
	}
	if ts, iv := effectiveProbe(all, 300, parseNodeProbe(`{"mode":"custom","target_ids":[2,9],"interval_s":60}`), 1); fmt.Sprint(ids(ts)) != "[2]" || iv != 60 {
		t.Errorf("自定义：已删除的目标忽略，未全局启用的也可以选：%v %d", ids(ts), iv)
	}
	if _, iv := effectiveProbe(all, 300, parseNodeProbe(`{"mode":"custom","target_ids":[1],"interval_s":0}`), 1); iv != 300 {
		t.Errorf("频率 0 跟随全局：%d", iv)
	}
	if ts, _ := effectiveProbe(all, 300, parseNodeProbe(`{"mode":"off"}`), 1); len(ts) != 0 {
		t.Error("关闭时没有目标")
	}
	// 【安全】节点自己的目标只下发给该节点
	own := append(all, ProbeTargetRow{ID: 4, ServerID: 1}, ProbeTargetRow{ID: 5, ServerID: 2, Enabled: true})
	if ts, _ := effectiveProbe(own, 300, parseNodeProbe(`{"mode":"custom","target_ids":[4,5],"interval_s":0}`), 1); fmt.Sprint(ids(ts)) != "[4]" {
		t.Errorf("只能使用自己的目标：%v", ids(ts))
	}
	if ts, _ := effectiveProbe(own, 300, parseNodeProbe(""), 1); fmt.Sprint(ids(ts)) != "[1 3]" {
		t.Errorf("跟随全局时不含任何节点自己的目标：%v", ids(ts))
	}
	if np := parseNodeProbe("{bad"); np.Mode != "default" {
		t.Error("无法解析时跟随全局")
	}
}

// 节点自己的测试目标：只在该节点的列表与下发中出现；其他节点不能选；节点删除时一并删除（修订第 123 条）。
func TestNodeOwnTargets(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, tok1, _ := s.store.CreateServer("own-1", 0, 1)
	s.store.CreateServer("own-2", 0, 1)
	shared := addCNTargets(t, s, 1, 1)[0]
	rec := do(h, "POST", "/api/v1/probe-targets", admin, []byte(`{"isp":"ct","province":"杭州","address":"115.220.14.91","server_id":1}`))
	var own ProbeTargetRow
	json.Unmarshal(rec.Body.Bytes(), &own)
	if rec.Code != 201 || own.ServerID != 1 || own.Enabled {
		t.Fatalf("添加节点自己的目标：%d %+v", rec.Code, own)
	}
	count := func(q string) int {
		var l struct{ Items []ProbeTargetRow }
		json.Unmarshal(do(h, "GET", "/api/v1/probe-targets"+q, admin, nil).Body.Bytes(), &l)
		return len(l.Items)
	}
	if count("") != 1 || count("?server_id=1") != 2 || count("?server_id=2") != 1 {
		t.Errorf("共用列表不含节点自己的目标；节点 1 的列表含它：%d %d %d", count(""), count("?server_id=1"), count("?server_id=2"))
	}
	if rec := do(h, "PUT", "/api/v1/servers/2/probe", admin, []byte(fmt.Sprintf(`{"mode":"custom","target_ids":[%d],"interval_s":0}`, own.ID))); rec.Code != 422 {
		t.Errorf("【安全】其他节点不能选这个目标：%d", rec.Code)
	}
	if rec := do(h, "PUT", "/api/v1/servers/1/probe", admin, []byte(fmt.Sprintf(`{"mode":"custom","target_ids":[%d,%d],"interval_s":0}`, shared, own.ID))); rec.Code != 200 {
		t.Fatalf("节点 1 选择自己的目标：%d %s", rec.Code, rec.Body)
	}
	req := httptest.NewRequest("POST", "/api/v1/agent/report", strings.NewReader(`{"system":{"boot_id":"b"}}`))
	req.Header.Set("Authorization", "Bearer "+tok1)
	req.Header[protocol.HeaderProbeVersion] = []string{""}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if got := protocol.ParseProbeTargets(w.Header().Get(protocol.HeaderProbeTargets)); len(got) != 2 || got[1].ID != own.ID {
		t.Errorf("节点 1 应测共用的目标与自己的目标：%+v", got)
	}
	if rec := do(h, "POST", "/api/v1/probe-targets", admin, []byte(`{"isp":"ct","province":"x","address":"1.1.1.1","server_id":999}`)); rec.Code != 422 {
		t.Errorf("节点不存在应返回 422：%d", rec.Code)
	}
	if _, err := s.store.DeleteServer(1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.GetProbeTarget(own.ID); err == nil {
		t.Error("节点删除时应删除它自己的目标")
	}
}

// 域名目标随下发编码为域名，由 Agent 解析（修订第 132 条）。
func TestProbeWireHost(t *testing.T) {
	pt, ok := ProbeTargetRow{ID: 5, Proto: "tcp", Port: 80, Address: "speed.test.cn"}.wire()
	if !ok || pt.Host != "speed.test.cn" || pt.Addr.IsValid() || protocol.EncodeProbeTargets([]protocol.ProbeTarget{pt}) != "5,tcp,speed.test.cn,80" {
		t.Errorf("域名目标：%+v %v", pt, ok)
	}
	if _, ok := (ProbeTargetRow{ID: 6, Proto: "icmp", Address: "nas.lan"}).wire(); ok {
		t.Error("【安全】局域网名称不应下发")
	}
}

// 超时与样本间隔（修订第 133 条）：节点自定义的非 0 值优先，其次全局，最后默认 4 秒、1 秒。
func TestEffectiveTiming(t *testing.T) {
	custom := func(to, gap int) NodeProbe { return NodeProbe{Mode: probeModeCustom, TimeoutS: to, GapS: gap} }
	for _, c := range []struct {
		g    ProbeSettings
		np   NodeProbe
		want protocol.ProbeTiming
	}{
		{ProbeSettings{}, NodeProbe{Mode: probeModeDefault}, protocol.ProbeTiming{Timeout: 4, Gap: 1}},
		{ProbeSettings{TimeoutS: 6}, NodeProbe{Mode: probeModeDefault, TimeoutS: 9}, protocol.ProbeTiming{Timeout: 6, Gap: 1}}, // 跟随全局时节点的值不生效
		{ProbeSettings{TimeoutS: 6, GapS: 2}, custom(0, 3), protocol.ProbeTiming{Timeout: 6, Gap: 3}},
		{ProbeSettings{}, custom(10, 0), protocol.ProbeTiming{Timeout: 10, Gap: 1}},
		{ProbeSettings{TimeoutS: 99}, custom(0, 0), protocol.ProbeTiming{Timeout: 4, Gap: 1}},
	} {
		if got := effectiveTiming(c.g, c.np); got != c.want {
			t.Errorf("%+v %+v → %+v，应为 %+v", c.g, c.np, got, c.want)
		}
	}
}

func TestProbeTimingAPI(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, tok, _ := s.store.CreateServer("tm-1", 0, 1)
	addCNTargets(t, s, 2, 2)
	report := func(have string) http.Header {
		req := httptest.NewRequest("POST", "/api/v1/agent/report", strings.NewReader(`{"system":{"boot_id":"b"}}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header[protocol.HeaderProbeVersion] = []string{have}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Header()
	}
	hd := report("")
	ver := hd.Get(protocol.HeaderProbeVersion)
	if hd.Get(protocol.HeaderProbeTiming) != "4,1" {
		t.Errorf("默认下发 4 秒、1 秒：%q", hd.Get(protocol.HeaderProbeTiming))
	}
	for _, bad := range []string{`{"interval_s":300,"timeout_s":11,"gap_s":0}`, `{"interval_s":300,"timeout_s":0,"gap_s":-1}`} {
		if rec := do(h, "PUT", "/api/v1/settings/probe", admin, []byte(bad)); rec.Code != 422 {
			t.Errorf("%s 应返回 422：%d", bad, rec.Code)
		}
	}
	if rec := do(h, "PUT", "/api/v1/settings/probe", admin, []byte(`{"interval_s":300,"timeout_s":6,"gap_s":2}`)); rec.Code != 200 {
		t.Fatalf("保存：%d %s", rec.Code, rec.Body)
	}
	hd = report(ver)
	if hd.Get(protocol.HeaderProbeTiming) != "6,2" || hd.Get(protocol.HeaderProbeVersion) == ver {
		t.Errorf("修改超时后应下发新版本：%q %q", hd.Get(protocol.HeaderProbeVersion), hd.Get(protocol.HeaderProbeTiming))
	}
	var ps ProbeSettings
	json.Unmarshal(do(h, "GET", "/api/v1/settings/probe", admin, nil).Body.Bytes(), &ps)
	if ps.TimeoutS != 6 || ps.GapS != 2 {
		t.Errorf("读取：%+v", ps)
	}
	// 节点自定义：超时 10 秒、间隔跟随全局
	var list struct{ Items []ProbeTargetRow }
	json.Unmarshal(do(h, "GET", "/api/v1/probe-targets", admin, nil).Body.Bytes(), &list)
	body := fmt.Sprintf(`{"mode":"custom","target_ids":[%d],"interval_s":0,"timeout_s":10,"gap_s":0}`, list.Items[0].ID)
	if rec := do(h, "PUT", "/api/v1/servers/1/probe", admin, []byte(body)); rec.Code != 200 {
		t.Fatalf("节点自定义：%d %s", rec.Code, rec.Body)
	}
	if got := report("").Get(protocol.HeaderProbeTiming); got != "10,2" {
		t.Errorf("节点的超时优先、间隔跟随全局：%q", got)
	}
	if rec := do(h, "PUT", "/api/v1/servers/1/probe", admin, []byte(strings.Replace(body, `"timeout_s":10`, `"timeout_s":11`, 1))); rec.Code != 422 {
		t.Errorf("节点超时超出范围应返回 422：%d", rec.Code)
	}
}
