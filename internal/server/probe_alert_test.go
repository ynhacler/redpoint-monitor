package server

import (
	"strings"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

func TestSummarizeProbeAlerts(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	row := func(sid, tid int64, name string, lat *float64, loss float64) probeAlertRow {
		return probeAlertRow{ServerID: sid, TargetID: tid, Name: name, LatencyPoint: LatencyPoint{Latency: lat, Loss: loss}}
	}
	got := summarizeProbeAlerts([]probeAlertRow{
		// 节点 1：三个目标，上海电信全部不可达（失效的目标只拉高一部分丢包）
		row(1, 1, "北京电信", f(150), 0), row(1, 1, "北京电信", f(170), 0),
		row(1, 2, "广东移动", f(200), 20), row(1, 2, "广东移动", f(220), 0),
		row(1, 3, "上海电信", nil, 100), row(1, 3, "上海电信", nil, 100),
		// 节点 2：唯一的目标不可达，没有延迟
		row(2, 4, "四川联通", nil, 100),
	})
	n1 := got[1]
	if n1.Latency == nil || *n1.Latency != 185 { // 目标中位数 160、210 → 185
		t.Fatalf("节点 1 延迟 = %v，应为 185", n1.Latency)
	}
	if n1.LatencyDetail != "最慢：广东移动 210 ms" {
		t.Errorf("延迟说明 = %q", n1.LatencyDetail)
	}
	if want := (0 + 10 + 100) / 3.0; n1.Loss != want {
		t.Errorf("节点 1 丢包 = %v，应为 %v", n1.Loss, want)
	}
	if n1.LossDetail != "丢包最多：上海电信 100%" {
		t.Errorf("丢包说明 = %q", n1.LossDetail)
	}
	n2 := got[2]
	if n2.Latency != nil || n2.Loss != 100 {
		t.Errorf("节点 2 = %+v，应没有延迟、丢包 100", n2)
	}
	if _, ok := got[3]; ok {
		t.Error("没有结果的节点不应出现")
	}
}

func TestProbeAlertValues(t *testing.T) {
	lat := 320.0
	in := alertInput{Probe: &probeAlertView{Latency: &lat, LatencyDetail: "最慢：上海电信 400 ms", Loss: 25, LossDetail: "丢包最多：上海电信 100%"}}
	if v, d, ok := alertValue(AlertProbeLatency, in); !ok || v != 320 || d == "" {
		t.Errorf("延迟值 = %v %q %v", v, d, ok)
	}
	if v, _, ok := alertValue(AlertProbeLoss, in); !ok || v != 25 {
		t.Errorf("丢包值 = %v %v", v, ok)
	}
	// 全部不可达：延迟没有数据（由丢包规则负责），没有探测结果时两条规则都不评估
	if _, _, ok := alertValue(AlertProbeLatency, alertInput{Probe: &probeAlertView{Loss: 100}}); ok {
		t.Error("没有延迟时不应评估延迟规则")
	}
	if _, _, ok := alertValue(AlertProbeLoss, alertInput{}); ok {
		t.Error("没有探测结果时不应评估")
	}
	msg := alertMessage(AlertRule{Type: AlertProbeLatency, Threshold: 300}, 320, "最慢：上海电信 400 ms")
	if msg != "三网延迟 320 ms（阈值 300 ms），最慢：上海电信 400 ms" {
		t.Errorf("消息 = %q", msg)
	}
}

// 端到端：探测结果写入后，启用的“三网丢包”规则按持续时间触发。
func TestProbeLossAlertFires(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, v, _ := createNode(t, h, admin, `{"name":"jp-1"}`)
	if rec, _ := enroll(h, v.EnrollCode, "jp-1", "m-1"); rec.Code != 200 {
		t.Fatalf("注册失败：%d %s", rec.Code, rec.Body)
	}
	sid := v.ServerID
	now := time.Now().Add(time.Hour) // 越过启动宽限期
	var tid int64
	if err := s.store.DB.QueryRow(`SELECT id FROM probe_targets WHERE isp = 'ct' AND enabled = 1 ORDER BY id LIMIT 1`).Scan(&tid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DB.Exec(`INSERT INTO probe_results (server_id, target_id, ts, latency_ms, loss, error) VALUES (?, ?, ?, NULL, 100, 'timeout')`,
		sid, tid, now.Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.latest[sid] = &snapshot{ReceivedAt: now, At: now, Report: protocol.Report{CPU: protocol.CPU{Cores: 1}}}
	s.mu.Unlock()

	// 默认不启用：不触发
	if err := s.evaluateAlerts(now); err != nil {
		t.Fatal(err)
	}
	if f := s.alerts.firingFor(ServerRow{ID: sid}); len(f) != 0 {
		t.Fatalf("规则默认不启用，不应触发：%+v", f)
	}
	if _, err := s.store.DB.Exec(`UPDATE alert_rules SET enabled = 1, duration_s = 0 WHERE rule_key = 'probe_loss'`); err != nil {
		t.Fatal(err)
	}
	s.alerts.trafficAt = time.Time{} // 下一轮重新汇总
	if err := s.evaluateAlerts(now.Add(alertInterval)); err != nil {
		t.Fatal(err)
	}
	f := s.alerts.firingFor(ServerRow{ID: sid})
	if len(f) != 1 || !strings.HasPrefix(f[0].Message, "三网丢包率 100%（阈值 20%），丢包最多：") {
		t.Fatalf("应触发三网丢包告警：%+v", f)
	}
}
