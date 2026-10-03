package server

import (
	"fmt"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

func TestAdvanceAlert(t *testing.T) {
	cpu := AlertRule{RuleKey: "cpu", Type: AlertCPU, Operator: ">", Threshold: 90, RecoverThreshold: 80,
		DurationS: 300, RecoverDurationS: 120, Severity: SeverityWarning, Enabled: true}
	t0 := time.Unix(1_000_000, 0)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

	type step struct {
		sec   int
		value float64
		state string // 期望的状态；空表示没有活动告警
		tr    alertTransition
	}
	cases := []struct {
		name  string
		rule  AlertRule
		steps []step
	}{
		{"持续 5 分钟才触发", cpu, []step{
			{0, 95, StatePending, alertNone},
			{290, 96, StatePending, alertNone},
			{300, 97, StateFiring, alertFired},
			{310, 97, StateFiring, alertNone},
		}},
		{"尖峰：未持续够就回落，不留记录", cpu, []step{
			{0, 95, StatePending, alertNone},
			{60, 50, "", alertCancelled},
			{70, 50, "", alertNone},
		}},
		{"等于阈值不触发（运算符为 >）", cpu, []step{{0, 90, "", alertNone}}},
		{"回差：回到 80～90 之间保持 firing，恢复计时清零", cpu, []step{
			{0, 95, StatePending, alertNone},
			{300, 95, StateFiring, alertFired},
			{310, 70, StateFiring, alertNone}, // 开始恢复计时
			{400, 85, StateFiring, alertNone}, // 回到回差区间：计时清零
			{410, 70, StateFiring, alertNone}, // 重新开始计时
			{520, 70, StateFiring, alertNone}, // 110 秒，未满 120 秒
			{530, 70, "", alertResolved},
		}},
		{"持续时间为 0：立即触发与恢复", AlertRule{Type: AlertDisk, Operator: ">", Threshold: 85, RecoverThreshold: 80}, []step{
			{0, 86, StateFiring, alertFired},
			{10, 82, StateFiring, alertNone},
			{20, 79, "", alertResolved},
		}},
		{">= 包含阈值本身（流量 80%）", AlertRule{Type: AlertTraffic, Operator: ">=", Threshold: 80, RecoverThreshold: 80}, []step{
			{0, 79.9, "", alertNone},
			{10, 80, StateFiring, alertFired},
			{20, 81, StateFiring, alertNone},
			{30, 1, "", alertResolved}, // 新周期开始，用量回落
		}},
	}
	for _, c := range cases {
		var st *alertState
		for i, s := range c.steps {
			var tr alertTransition
			st, tr = advanceAlert(st, c.rule, s.value, "", at(s.sec))
			got := ""
			if st != nil {
				got = st.State
			}
			if got != s.state || tr != s.tr {
				t.Fatalf("%s 第 %d 步（%d 秒，值 %v）：状态 %q 变化 %d，应为 %q %d", c.name, i, s.sec, s.value, got, tr, s.state, s.tr)
			}
		}
	}
}

func TestEffectiveRules(t *testing.T) {
	all := []AlertRule{
		{ID: 1, RuleKey: "cpu", ScopeType: "global", Threshold: 90, Enabled: true},
		{ID: 2, RuleKey: "disk", ScopeType: "global", Threshold: 85, Enabled: true},
		{ID: 3, RuleKey: "swap", ScopeType: "global", Threshold: 50, Enabled: false},
		{ID: 4, RuleKey: "cpu", ScopeType: "group", ScopeID: "落地", Threshold: 95, Enabled: true},
		{ID: 5, RuleKey: "cpu", ScopeType: "server", ScopeID: "7", Enabled: false}, // 节点 7 关闭 CPU 告警
		{ID: 6, RuleKey: "swap", ScopeType: "server", ScopeID: "8", Threshold: 30, Enabled: true},
	}
	ids := func(rs []AlertRule) (out []int64) {
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return
	}
	cases := []struct {
		name  string
		id    int64
		group string
		want  []int64
	}{
		{"只有全局规则；关闭的不返回", 1, "", []int64{1, 2}},
		{"分组覆盖全局", 1, "落地", []int64{4, 2}},
		{"节点关闭覆盖分组与全局", 7, "落地", []int64{2}},
		{"节点打开全局关闭的规则", 8, "", []int64{1, 2, 6}},
	}
	for _, c := range cases {
		got := ids(EffectiveRules(all, c.id, c.group))
		if len(got) != len(c.want) {
			t.Errorf("%s：%v，应为 %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s：%v，应为 %v", c.name, got, c.want)
				break
			}
		}
	}
}

func TestAlertValue(t *testing.T) {
	rep := &protocol.Report{
		CPU:    protocol.CPU{Usage: 42, Cores: 4, Load1: 10},
		Memory: protocol.Memory{Usage: 61},
		Swap:   protocol.Swap{Total: 1000, Used: 250},
		Disk:   []protocol.Disk{{Mount: "/", Usage: 40}, {Mount: "/data", Usage: 88}},
	}
	tv := &trafficView{Used: 850, Limit: 1000, Forecast: &forecastView{Total: 1200}}
	in := alertInput{OfflineFor: 3 * time.Minute, Report: rep, Traffic: tv}
	cases := []struct {
		typ    string
		in     alertInput
		v      float64
		detail string
		ok     bool
	}{
		{AlertOffline, in, 180, "", true},
		{AlertCPU, in, 42, "", true},
		{AlertMemory, in, 61, "", true},
		{AlertDisk, in, 88, "/data", true}, // 使用率最高的挂载点
		{AlertSwap, in, 25, "", true},
		{AlertLoad, in, 2.5, "", true},
		{AlertTraffic, in, 85, "", true},
		{AlertTrafficForecast, in, 120, "", true},
		{AlertCPU, alertInput{}, 0, "", false},                                                     // 离线：没有上报，暂停评估
		{AlertSwap, alertInput{Report: &protocol.Report{}}, 0, "", false},                          // 未启用 Swap
		{AlertTraffic, alertInput{Traffic: &trafficView{Used: 1}}, 0, "", false},                   // 不限流量
		{AlertTrafficForecast, alertInput{Traffic: &trafficView{Used: 1, Limit: 9}}, 0, "", false}, // 周期不足 3 天，没有预测
		{AlertAgentClock, alertInput{ClockSkew: ptrF(-90)}, 90, "慢", true},                         // 快慢都按绝对值比较
		{AlertAgentClock, alertInput{ClockSkew: ptrF(75)}, 75, "快", true},
		{AlertAgentClock, alertInput{Report: rep}, 0, "", false}, // 无法测量（旧版 Agent 且时钟不超前）
	}
	for _, c := range cases {
		v, d, ok := alertValue(c.typ, c.in)
		if v != c.v || d != c.detail || ok != c.ok {
			t.Errorf("%s：(%v, %q, %v)，应为 (%v, %q, %v)", c.typ, v, d, ok, c.v, c.detail, c.ok)
		}
	}
}

func ptrF(v float64) *float64 { return &v }

func TestClockSkew(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name string
		rep  protocol.Report
		want float64
		ok   bool
	}{
		{"新版 Agent：按发送时刻，偏快", protocol.Report{Timestamp: now.Unix() - 600, SentAt: now.Unix() + 90}, 90, true},
		{"新版 Agent：偏慢（补发的旧数据也能测）", protocol.Report{Timestamp: now.Unix() - 900, SentAt: now.Unix() - 120}, -120, true},
		{"旧版 Agent：采集时间超前", protocol.Report{Timestamp: now.Unix() + 100}, 100, true},
		{"旧版 Agent：采集时间落后，无法区分偏慢与补发", protocol.Report{Timestamp: now.Unix() - 100}, 0, false},
	}
	for _, c := range cases {
		got, ok := clockSkew(c.rep, now)
		if got != c.want || ok != c.ok {
			t.Errorf("%s：(%v, %v)，应为 (%v, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
	r := AlertRule{Type: AlertAgentClock}
	if m := alertMessage(r, 120, "慢"); m != "Agent 时钟比面板慢 120 秒，请检查主机的时间同步（NTP）" {
		t.Errorf("告警消息：%s", m)
	}
}

// 上报中的 sent_at 进入实时状态，供告警引擎评估（设计 16.1、43.5）。
func TestClockSkewIngest(t *testing.T) {
	s, h, _ := testServer(t)
	_, tok, _ := s.store.CreateServer("clk", 0, 1)
	now := time.Now().Unix()
	body := fmt.Sprintf(`{"timestamp":%d,"sent_at":%d,"system":{"boot_id":"b"}}`, now, now-300)
	if rec := do(h, "POST", "/api/v1/agent/report", tok, []byte(body)); rec.Code != 204 {
		t.Fatalf("上报失败：%d", rec.Code)
	}
	snaps := s.snapshots()
	sk := snaps[1].ClockSkew
	if sk == nil || *sk > -299 || *sk < -301 {
		t.Fatalf("应记录 Agent 偏慢约 300 秒：%v", sk)
	}
	row, _ := s.store.GetServer(1)
	if in := s.alertInputFor(*row, snaps, time.Now()); in.ClockSkew == nil {
		t.Error("告警输入应带时钟偏差")
	}
	// 下一份旧版格式的上报测不到偏差：沿用上次的值，而不是清空后让告警误以为恢复
	body = fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"}}`, now+1)
	do(h, "POST", "/api/v1/agent/report", tok, []byte(body))
	if sk := s.snapshots()[1].ClockSkew; sk == nil {
		t.Error("测不到时应沿用上次的偏差")
	}
}
