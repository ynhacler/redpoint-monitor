package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

// noiseFixture：若干已注册节点 + 一个记录通知的 Webhook 渠道。
type noiseFixture struct {
	t    *testing.T
	s    *Server
	hook *fakeReceiver
	ids  []int64
	t0   time.Time
}

func newNoiseFixture(t *testing.T, nodes int) *noiseFixture {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	f := &noiseFixture{t: t, s: s, hook: newReceiver(t), t0: time.Now().Add(time.Hour)}
	for i := 0; i < nodes; i++ {
		_, v, _ := createNode(t, h, admin, fmt.Sprintf(`{"name":"n-%d"}`, i))
		enroll(h, v.EnrollCode, fmt.Sprintf("n-%d", i), fmt.Sprintf("m-%d", i))
		f.ids = append(f.ids, v.ServerID)
	}
	if rec := do(h, "POST", "/api/v1/notification-channels", admin,
		[]byte(`{"type":"webhook","name":"all","min_severity":"info","config":{"url":"`+f.hook.srv.URL+`/all"}}`)); rec.Code != 201 {
		t.Fatalf("创建渠道：%d %s", rec.Code, rec.Body)
	}
	return f
}

func (f *noiseFixture) at(sec int) time.Time { return f.t0.Add(time.Duration(sec) * time.Second) }

func (f *noiseFixture) report(sec int, cpu float64, ids ...int64) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	for _, id := range ids {
		f.s.latest[id] = &snapshot{ReceivedAt: f.at(sec), At: f.at(sec), Report: protocol.Report{
			CPU: protocol.CPU{Usage: cpu, Cores: 2}, Memory: protocol.Memory{Usage: 30}, Disk: []protocol.Disk{{Mount: "/", Usage: 40}}}}
	}
}

func (f *noiseFixture) eval(sec int) {
	f.t.Helper()
	if err := f.s.evaluateAlerts(f.at(sec)); err != nil {
		f.t.Fatal(err)
	}
	f.s.notify.wg.Wait()
}

// kinds 返回收到的通知种类与标题。
func (f *noiseFixture) received() []string {
	f.hook.mu.Lock()
	defer f.hook.mu.Unlock()
	var out []string
	for _, b := range f.hook.bodies {
		var p map[string]any
		json.Unmarshal([]byte(b), &p)
		out = append(out, fmt.Sprint(p["kind"], "|", p["title"]))
	}
	return out
}

// 抖动：30 分钟内第 3 次触发时只通知一次“状态频繁变化”，之后静默；稳定 30 分钟后若仍在告警补发一条（设计 16.3）。
func TestAlertFlapping(t *testing.T) {
	f := newNoiseFixture(t, 1)
	id := f.ids[0]
	f.s.store.DB.Exec(`UPDATE alert_rules SET duration_s = 0, recover_duration_s = 0 WHERE rule_key = 'cpu'`)
	cpu := []float64{95, 40, 95, 40, 95, 40, 95} // 触发、恢复交替
	for i, v := range cpu {
		f.report(i*10, v, id)
		f.eval(i * 10)
	}
	got := f.received()
	want := []string{"firing", "resolved", "firing", "resolved", "flapping"}
	if len(got) != len(want) {
		t.Fatalf("通知：%v", got)
	}
	for i, k := range want {
		if !strings.HasPrefix(got[i], k+"|") {
			t.Errorf("第 %d 条应为 %s：%s", i+1, k, got[i])
		}
	}
	if !strings.Contains(got[4], "状态频繁变化") {
		t.Errorf("抖动通知：%s", got[4])
	}
	// 稳定 30 分钟（仍在告警）：补发一条，之后恢复正常通知
	f.report(60+1801, 95, id)
	f.eval(60 + 1801)
	got = f.received()
	if len(got) != 6 || !strings.HasPrefix(got[5], "still_firing|") {
		t.Fatalf("稳定后仍在告警应补发：%v", got)
	}
	f.report(60+1811, 40, id)
	f.eval(60 + 1811)
	if got = f.received(); len(got) != 7 || !strings.HasPrefix(got[6], "resolved|") {
		t.Errorf("抖动结束后恢复正常通知：%v", got)
	}
}

// 批量合并：1 分钟内 ≥ 5 台节点离线合并为一条，恢复同样合并；不足 5 台时逐条发出（设计 16.4）。
func TestBatchOffline(t *testing.T) {
	f := newNoiseFixture(t, 8)
	down, alive := f.ids[:6], f.ids[6:]
	for sec := 0; sec <= 400; sec += 10 {
		f.report(sec, 10, alive...) // 有节点持续上报：不是面板的问题
		if sec == 0 {
			f.report(sec, 10, down...)
		}
		f.eval(sec)
	}
	got := f.received()
	if len(got) != 1 || !strings.HasPrefix(got[0], "firing|") || !strings.Contains(got[0], "6 台节点同时离线") {
		t.Fatalf("6 台离线应合并为一条：%v", got)
	}
	_, body, _ := f.hook.last()
	if !strings.Contains(body, `"count":6`) || !strings.Contains(body, "同一服务商") {
		t.Errorf("合并通知内容：%s", body)
	}
	// 恢复：同样合并
	for sec := 410; sec <= 600; sec += 10 {
		f.report(sec, 10, f.ids...)
		f.eval(sec)
	}
	got = f.received()
	if len(got) != 2 || !strings.Contains(got[1], "6 台节点恢复上报") {
		t.Fatalf("6 台恢复应合并为一条：%v", got)
	}

	// 只有 2 台离线：逐条发出（最多延迟 1 分钟）
	for sec := 610; sec <= 1000; sec += 10 {
		f.report(sec, 10, f.ids[2:]...)
		f.eval(sec)
	}
	got = f.received()
	if len(got) != 4 || !strings.Contains(got[2], "n-") || strings.Contains(got[2], "台节点") || !strings.Contains(got[3], "节点离线") {
		t.Errorf("不足 5 台时应逐条通知：%v", got)
	}
}

// 面板自检：全部节点在同一时刻停止上报，以面板告警通知并暂停离线告警；任一节点恢复即解除（设计 16.4）。
func TestPanelSelfCheck(t *testing.T) {
	f := newNoiseFixture(t, 3)
	f.report(0, 10, f.ids...)
	f.eval(0)
	for sec := 10; sec <= 400; sec += 10 {
		f.eval(sec) // 无人上报
	}
	got := f.received()
	if len(got) != 1 || !strings.HasPrefix(got[0], "panel_down|") || !strings.Contains(got[0], "全部 3 台节点同时停止上报") {
		t.Fatalf("应只发一条面板告警、不发离线告警：%v", got)
	}
	if ev, _, _ := f.s.store.ListAlertEvents(AlertQuery{State: StateFiring, Limit: 10}); len(ev) != 0 {
		t.Errorf("面板异常期间不应产生离线告警：%+v", ev)
	}
	f.report(410, 10, f.ids[0])
	f.eval(410)
	if got = f.received(); len(got) != 2 || !strings.HasPrefix(got[1], "panel_up|") {
		t.Errorf("任一节点恢复上报应解除：%v", got)
	}

	// 只有 1 台节点：无法区分，按普通离线处理
	g := newNoiseFixture(t, 1)
	g.report(0, 10, g.ids...)
	for sec := 0; sec <= 300; sec += 10 {
		g.eval(sec)
	}
	for sec := 310; sec <= 400; sec += 10 {
		g.eval(sec) // 等离线合并窗口到期
	}
	if got := g.received(); len(got) != 1 || !strings.HasPrefix(got[0], "firing|") {
		t.Errorf("单节点应按普通离线告警：%v", got)
	}

	// 先后停止（相差超过 1 分钟）：不是同一时刻，按普通离线处理
	h := newNoiseFixture(t, 2)
	h.report(0, 10, h.ids...)
	h.eval(0)
	for sec := 10; sec <= 500; sec += 10 {
		if sec <= 100 {
			h.report(sec, 10, h.ids[1])
		}
		h.eval(sec)
	}
	for _, r := range h.received() {
		if strings.HasPrefix(r, "panel_down|") {
			t.Errorf("先后停止上报不应判定为面板异常：%v", h.received())
		}
	}
}
