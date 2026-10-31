package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

// 防火墙变化（修订第 273 条）：停用、默认策略放宽为 allow、启用中的防火墙不再检测到为警告；规则按内容比较，顺序变化不算。
func TestDiffFirewall(t *testing.T) {
	ufw := func(active bool, policy string, rules ...protocol.FirewallRule) protocol.FirewallTool {
		return protocol.FirewallTool{Name: "ufw", Active: active, Policy: policy, RulesReadable: true, Rules: rules}
	}
	r22 := protocol.FirewallRule{Action: "allow", Proto: "tcp", Port: "22"}
	r443 := protocol.FirewallRule{Action: "allow", Proto: "tcp", Port: "443"}
	r8080 := protocol.FirewallRule{Action: "allow", Port: "8080", From: "1.2.3.4"}
	state := func(tools ...protocol.FirewallTool) map[string]fwToolState {
		return firewallState(&protocol.Firewall{Tools: tools})
	}
	base := state(ufw(true, "deny", r22, r443))
	cases := []struct {
		name string
		cur  map[string]fwToolState
		want string
		sev  string
	}{
		{"规则顺序变化", state(ufw(true, "deny", r443, r22)), "", SeverityInfo},
		{"新增与删除规则", state(ufw(true, "deny", r22, r8080)), "ufw 新增规则：allow 8080 来自 1.2.3.4|ufw 删除规则：allow tcp/443", SeverityInfo},
		{"停用", state(ufw(false, "deny", r22, r443)), "ufw 已停用", SeverityWarning},
		{"默认策略放宽", state(ufw(true, "allow", r22, r443)), "ufw 入站默认策略 deny → allow", SeverityWarning},
		{"不再检测到", state(), "ufw 不再检测到（此前已启用）", SeverityWarning},
		{"新检测到", state(ufw(true, "deny", r22, r443), protocol.FirewallTool{Name: "fail2ban", Active: true}), "fail2ban 新检测到（已启用）", SeverityInfo},
	}
	for _, c := range cases {
		lines, sev := diffFirewall(base, c.cur)
		if got := strings.Join(lines, "|"); got != c.want || sev != c.sev {
			t.Errorf("%s：%q %s，期望 %q %s", c.name, got, sev, c.want, c.sev)
		}
	}
	// 规则不可读（需要 root）时不比较规则
	o := state(protocol.FirewallTool{Name: "ufw", Active: true, Rules: []protocol.FirewallRule{r22}, RulesReadable: true})
	n := state(protocol.FirewallTool{Name: "ufw", Active: true})
	if lines, _ := diffFirewall(o, n); len(lines) != 0 {
		t.Errorf("规则变为不可读不算变化：%v", lines)
	}
	many := make([]string, 7)
	for i := range many {
		many[i] = fmt.Sprint(i)
	}
	if got := listBrief(many); got != "0、1、2、3、4 等 7 条" {
		t.Errorf("listBrief = %q", got)
	}
}

// 第一次看到只记录；有变化时记一条变更事件；细节只给管理员；面板重启后（缓存清空）不把现状当作变化。
func TestChangeEvents(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	id, tok, _ := s.store.CreateServer("chg-host", 0, 1)
	report := func(policy string) {
		t.Helper()
		body := fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"},"firewall":{"tools":[{"name":"ufw","active":true,"policy":%q,"rules_readable":true,
			"rules":[{"action":"allow","proto":"tcp","port":"22"}]}]}}`, time.Now().Unix(), policy)
		if rec := do(h, "POST", "/api/v1/agent/report", tok, []byte(body)); rec.Code != 204 {
			t.Fatal(rec.Code, rec.Body)
		}
	}
	list := func(cred string) []SystemEvent {
		t.Helper()
		var out struct{ Items []SystemEvent }
		rec := do(h, "GET", "/api/v1/system-events?kind=change&server_id="+itoa64(id), cred, nil)
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body)
		}
		json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Items
	}
	report("deny")
	report("deny")
	if got := list(admin); len(got) != 0 {
		t.Fatalf("第一次看到与没有变化都不应记事件：%+v", got)
	}
	s.changes = newChangeWatch() // 模拟面板重启：只剩数据库中的状态
	report("deny")
	report("allow")
	got := list(admin)
	if len(got) != 1 || got[0].Severity != SeverityWarning || got[0].Title != "chg-host 防火墙有 1 项变化" ||
		got[0].Message != "ufw 入站默认策略 deny → allow" {
		t.Fatalf("应记一条警告级变更：%+v", got)
	}
	if v := list(viewerToken(t, s, "viewer-chg")); len(v) != 1 || v[0].Message != "" {
		t.Errorf("查看者只看到标题：%+v", v)
	}
	if rec := do(h, "GET", "/api/v1/system-events?server_id=x", admin, nil); rec.Code != 422 {
		t.Errorf("server_id 无效应返回 422：%d", rec.Code)
	}
}
