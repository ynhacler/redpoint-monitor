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

func TestReachTargetID(t *testing.T) {
	for _, c := range []struct {
		sid  int64
		v6   bool
		port int
	}{{1, false, 0}, {1, true, 443}, {123456, false, 65535}, {7, true, 22}} {
		id := reachTargetID(c.sid, c.v6, c.port)
		sid, v6, port, ok := decodeReachID(id)
		if !ok || sid != c.sid || v6 != c.v6 || port != c.port || id < reachBase {
			t.Errorf("%+v → %d → %d %v %d %v", c, id, sid, v6, port, ok)
		}
	}
	if _, _, _, ok := decodeReachID(12); ok {
		t.Error("普通目标 ID 不是探测项")
	}
}

func lat(v float64) *float64 { return &v }

func TestRoundState(t *testing.T) {
	ok := func(port int) reachCheck { return reachCheck{Family: 4, Port: port, Latency: lat(40), Loss: 0} }
	fail := func(port int) reachCheck { return reachCheck{Family: 4, Port: port, Loss: 100, Error: "timeout"} }
	noICMP := reachCheck{Family: 4, Port: 0, Loss: 100, Error: "icmp_unavailable"}
	for _, c := range []struct {
		name   string
		checks []reachCheck
		want   string
		ports  []int
	}{
		{"全部正常", []reachCheck{ok(0), ok(22), ok(443)}, reachOK, nil},
		{"全部失败：IP 疑似被封", []reachCheck{fail(0), fail(22), fail(443)}, reachBlocked, []int{22, 443}},
		{"Ping 正常、443 失败：端口疑似被封", []reachCheck{ok(0), ok(22), fail(443)}, reachPort, []int{443}},
		{"没有 Ping 权限、TCP 全失败：IP 疑似被封", []reachCheck{noICMP, fail(22), fail(443)}, reachBlocked, []int{22, 443}},
		{"没有 Ping 权限、部分端口失败", []reachCheck{noICMP, ok(22), fail(443)}, reachPort, []int{443}},
		{"丢包 > 30%：不稳定", []reachCheck{{Port: 0, Latency: lat(50), Loss: 40}, {Port: 22, Latency: lat(50), Loss: 34}}, reachUnstable, nil},
		{"只有不可用的项：不判定", []reachCheck{noICMP}, "", nil},
	} {
		st, ports := roundState(c.checks)
		if st != c.want || fmt.Sprint(ports) != fmt.Sprint(c.ports) {
			t.Errorf("%s：%s %v，应为 %s %v", c.name, st, ports, c.want, c.ports)
		}
	}
}

func TestTrackReach(t *testing.T) {
	good := []reachCheck{{Family: 4, Port: 22, Latency: lat(30)}}
	bad := []reachCheck{{Family: 4, Port: 22, Loss: 100, Error: "timeout"}}
	rounds := func(states ...[]reachCheck) []reachRound {
		var out []reachRound
		for i, c := range states {
			out = append(out, reachRound{TS: int64(100 + i*300), Checks: c})
		}
		return out
	}
	if tr := trackReach(rounds(good, bad, bad), nil); tr.Status != reachOK {
		t.Errorf("2 轮异常不判定：%+v", tr)
	}
	tr := trackReach(rounds(good, bad, bad, bad), nil)
	if tr.Status != reachBlocked || tr.Since != 1000 {
		t.Errorf("连续 3 轮异常判定，从第 3 轮起算：%+v", tr)
	}
	if tr := trackReach(rounds(bad, bad, bad, good), nil); tr.Status != reachBlocked {
		t.Errorf("1 轮正常不恢复：%+v", tr)
	}
	if tr := trackReach(rounds(bad, bad, bad, good, good), nil); tr.Status != reachOK {
		t.Errorf("连续 2 轮正常恢复：%+v", tr)
	}
	// 探针故障的轮次跳过：中间一轮不算，异常不连续
	r := rounds(bad, bad, good, bad)
	if tr := trackReach(r, map[int64]bool{r[2].TS: true}); tr.Status != reachBlocked {
		t.Errorf("跳过探针故障的轮次后应连续 3 轮异常：%+v", tr)
	}
}

func TestFaultyRounds(t *testing.T) {
	row := func(probe, sid, ts int64, loss float64) reachRow {
		return reachRow{ProbeID: probe, ServerID: sid, TS: ts, reachCheck: reachCheck{Family: 4, Port: 22, Loss: loss, Error: "timeout"}}
	}
	f := faultyRounds([]reachRow{
		row(1, 2, 100, 100), row(1, 3, 100, 100), // 探针 1 这一轮对两个节点都失败：探针故障
		row(1, 2, 400, 100), row(1, 3, 400, 0), // 只有一个节点失败：不是探针故障
		row(9, 2, 100, 100), // 探针 9 只测了一个节点：无法区分
	})
	if !f[1][100] || f[1][400] || f[9][100] {
		t.Errorf("探针故障：%v", f)
	}
}

func TestVerdictFor(t *testing.T) {
	probes := map[int64]ReachProbe{1: {ServerID: 1, ISP: "ct"}, 2: {ServerID: 2, ISP: "cm"}}
	names := map[int64]string{1: "sh-ct", 2: "gz-cm"}
	tr := func(st string, ports ...int) reachTrack { return reachTrack{Status: st, BadPorts: ports} }
	for _, c := range []struct {
		tracks  map[int64]reachTrack
		offline bool
		want    string
		level   float64
	}{
		{map[int64]reachTrack{1: tr(reachOK), 2: tr(reachOK)}, false, "国内可达", 0},
		{map[int64]reachTrack{1: tr(reachBlocked), 2: tr(reachBlocked)}, false, "国内不可达（疑似被墙）", 3},
		{map[int64]reachTrack{1: tr(reachOK), 2: tr(reachBlocked)}, false, "移动不可达", 2},
		{map[int64]reachTrack{1: tr(reachPort, 443), 2: tr(reachOK)}, false, "端口 443 国内不可达", 2},
		{map[int64]reachTrack{1: tr(reachUnstable), 2: tr(reachOK)}, false, "国内线路不稳定", 1},
		{map[int64]reachTrack{1: tr(reachBlocked)}, true, "节点离线（不判定国内可达性）", 0},
	} {
		v := verdictFor(c.tracks, probes, names, c.offline)
		if v.Text != c.want || v.Level != c.level {
			t.Errorf("%+v → %+v，应为 %q %v", c.tracks, v, c.want, c.level)
		}
	}
	if v := verdictFor(nil, probes, names, false); v.Status != "" {
		t.Errorf("没有数据：%+v", v)
	}
}

func TestBuildReachTargets(t *testing.T) {
	rows := []ServerRow{
		{ID: 3, Name: "c", EnrollState: enrollEnrolled, IPv4: "9.9.9.9", IPv6: "2606:4700::1111"},
		{ID: 1, Name: "probe", EnrollState: enrollEnrolled, IPv4: "1.1.1.1"},
		{ID: 2, Name: "lan", EnrollState: enrollEnrolled, IPv4: "10.0.0.2"}, // 内网地址：不探测
		{ID: 4, Name: "new", EnrollState: enrollPending, IPv4: "8.8.4.4"},   // 未安装：不探测
		{ID: 5, Name: "a", EnrollState: enrollEnrolled, IPv4: "8.8.8.8"},
	}
	got := buildReachTargets(rows, ReachProbe{ServerID: 1, ICMP: true, Ports: []int{22, 443}}, 64)
	var ids []string
	for _, x := range got {
		sid, v6, port, _ := decodeReachID(x.ID)
		ids = append(ids, fmt.Sprintf("%d/%v/%d/%s", sid, v6, port, x.Proto))
		if !x.Valid() {
			t.Errorf("不合法的探测项：%+v", x)
		}
	}
	want := "[3/false/0/icmp 3/false/22/tcp 3/false/443/tcp 3/true/0/icmp 3/true/22/tcp 3/true/443/tcp 5/false/0/icmp 5/false/22/tcp 5/false/443/tcp]"
	if fmt.Sprint(ids) != want {
		t.Errorf("探测项：%v", ids)
	}
	if n := len(buildReachTargets(rows, ReachProbe{ServerID: 1, ICMP: true, Ports: []int{22, 443}}, 4)); n != 4 {
		t.Errorf("上限：%d", n)
	}
}

// 端到端：设置探针 → 探针的 Agent 收到探测项 → 带回结果 → 连续 3 轮失败判定为疑似被墙，告警触发；接口校验与权限。
func TestReachEndToEnd(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	var toks [4]string
	for i, n := range []string{"cn-probe", "jp-a", "hk-b"} {
		_, toks[i+1], _ = s.store.CreateServer(n, 0, 1)
	}
	for id, ip := range map[int]string{1: "1.1.1.1", 2: "8.8.8.8", 3: "9.9.9.9"} {
		if _, err := s.store.DB.Exec(`UPDATE servers SET ipv4 = ? WHERE id = ?`, ip, id); err != nil {
			t.Fatal(err)
		}
	}
	report := func(tok string, probes []protocol.ProbeResult) http.Header {
		b, _ := json.Marshal(protocol.Report{System: protocol.System{BootID: "b"}, CPU: protocol.CPU{Cores: 1}, Probes: probes})
		req := httptest.NewRequest("POST", "/api/v1/agent/report", strings.NewReader(string(b)))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header[protocol.HeaderProbeVersion] = []string{""}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code >= 300 {
			t.Fatalf("上报：%d %s", rec.Code, rec.Body)
		}
		return rec.Header()
	}

	// 校验
	for _, bad := range []string{`{"probes":[{"server_id":99,"isp":"ct","ports":[22],"icmp":true}]}`,
		`{"probes":[{"server_id":1,"isp":"xx","ports":[22],"icmp":true}]}`,
		`{"probes":[{"server_id":1,"isp":"ct","ports":[],"icmp":false}]}`,
		`{"probes":[{"server_id":1,"isp":"ct","ports":[1,2,3,4],"icmp":true}]}`} {
		if rec := do(h, "PUT", "/api/v1/reach-probes", admin, []byte(bad)); rec.Code != 422 {
			t.Errorf("应拒绝 %s：%d", bad, rec.Code)
		}
	}
	if rec := do(h, "PUT", "/api/v1/reach-probes", admin, []byte(`{"probes":[{"server_id":1,"isp":"cm","ports":[443,22,22],"icmp":true}]}`)); rec.Code != 200 {
		t.Fatalf("保存探针：%d %s", rec.Code, rec.Body)
	}
	var list struct {
		Items []reachProbeView
	}
	json.Unmarshal(do(h, "GET", "/api/v1/reach-probes", admin, nil).Body.Bytes(), &list)
	if len(list.Items) != 1 || fmt.Sprint(list.Items[0].Ports) != "[22 443]" || list.Items[0].Count != 6 || list.Items[0].Name != "cn-probe" {
		t.Fatalf("探针列表：%+v", list.Items)
	}

	// 探针收到探测项：另外两台节点各 Ping + 2 个端口；普通节点收不到
	targets := protocol.ParseProbeTargets(report(toks[1], nil).Get(protocol.HeaderProbeTargets))
	if len(targets) != 6 {
		t.Fatalf("探针的探测项：%+v", targets)
	}
	if got := report(toks[2], nil).Get(protocol.HeaderProbeTargets); got != "" {
		t.Errorf("普通节点不应收到探测项：%q", got)
	}

	// 3 轮：jp-a 全部失败，hk-b 正常
	now := time.Now()
	for round := 2; round >= 0; round-- {
		at := now.Add(-time.Duration(round) * 5 * time.Minute).Unix()
		var rs []protocol.ProbeResult
		for _, tg := range targets {
			sid, _, _, _ := decodeReachID(tg.ID)
			if sid == 2 {
				rs = append(rs, protocol.ProbeResult{TargetID: tg.ID, At: at, Loss: 100, Error: "timeout"})
			} else {
				rs = append(rs, protocol.ProbeResult{TargetID: tg.ID, At: at, Latency: lat(60), Loss: 0})
			}
		}
		report(toks[1], rs)
	}
	report(toks[2], nil) // 被测节点在线
	report(toks[3], nil)
	s.flush()

	var v struct {
		Configured bool                      `json:"configured"`
		Status     string                    `json:"status"`
		Text       string                    `json:"text"`
		Probes     []reachProbeResult        `json:"probes"`
		Series     map[string][]LatencyPoint `json:"series"`
	}
	rec := do(h, "GET", "/api/v1/servers/2/reach", admin, nil)
	json.Unmarshal(rec.Body.Bytes(), &v)
	if rec.Code != 200 || !v.Configured || v.Status != reachBlocked || v.Text != "国内不可达（疑似被墙）" || len(v.Probes) != 1 ||
		len(v.Probes[0].Checks) != 3 || len(v.Series["1"]) != 3 {
		t.Fatalf("jp-a：%d %s", rec.Code, rec.Body)
	}
	json.Unmarshal(do(h, "GET", "/api/v1/servers/3/reach", admin, nil).Body.Bytes(), &v)
	if v.Status != reachOK || v.Text != "国内可达" {
		t.Errorf("hk-b：%+v", v)
	}
	// 普通 probe_results 中没有探测项
	var n int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM probe_results`).Scan(&n)
	if n != 0 {
		t.Errorf("探测项不应写入三网延迟结果：%d", n)
	}

	// 告警：默认启用的“国内不可达”规则触发
	later := time.Now().Add(time.Hour) // 越过启动宽限期
	for _, sid := range []int64{2, 3} {
		s.mu.Lock()
		s.latest[sid] = &snapshot{ReceivedAt: later, At: later, Report: protocol.Report{CPU: protocol.CPU{Cores: 1}}}
		s.mu.Unlock()
	}
	s.alerts.trafficAt = time.Time{}
	if err := s.evaluateAlerts(later); err != nil {
		t.Fatal(err)
	}
	f := s.alerts.firingFor(ServerRow{ID: 2})
	if len(f) != 1 || !strings.HasPrefix(f[0].Message, "国内不可达（疑似被墙）") || f[0].Severity != SeverityCritical {
		t.Fatalf("应触发疑似被墙告警：%+v", f)
	}
	if f := s.alerts.firingFor(ServerRow{ID: 3}); len(f) != 0 {
		t.Errorf("hk-b 不应告警：%+v", f)
	}

	// 删除节点：作为探针与被测的结果一并删除
	s.store.DeleteServer(2)
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM reach_results WHERE server_id = 2`).Scan(&n)
	if n != 0 {
		t.Errorf("删除节点后应删除它的结果：%d", n)
	}
}
