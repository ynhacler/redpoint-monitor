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

// 账号与权限组（修订第 274 条）：新增 UID 0 账号与管理员组新增成员为警告；只有顺序变化不算。
func TestDiffAccounts(t *testing.T) {
	st := func(users []protocol.AccountUser, groups ...protocol.AccountGroup) acctState {
		return accountState(&protocol.Accounts{Users: users, Groups: groups})
	}
	root := protocol.AccountUser{Name: "root", UID: 0, Shell: "/bin/bash"}
	bob := protocol.AccountUser{Name: "bob", UID: 1001, Shell: "/bin/bash"}
	base := st([]protocol.AccountUser{root, bob}, protocol.AccountGroup{Name: "sudo", Members: []string{"bob"}})
	cases := []struct {
		name string
		cur  acctState
		want string
		sev  string
	}{
		{"没有变化", st([]protocol.AccountUser{bob, root}, protocol.AccountGroup{Name: "sudo", Members: []string{"bob"}}), "", SeverityInfo},
		{"新增普通账号", st([]protocol.AccountUser{root, bob, {Name: "eve", UID: 1002}}, protocol.AccountGroup{Name: "sudo", Members: []string{"bob"}}),
			"新增可登录账号 eve（UID 1002）", SeverityInfo},
		{"新增 UID 0", st([]protocol.AccountUser{root, bob, {Name: "toor", UID: 0}}, protocol.AccountGroup{Name: "sudo", Members: []string{"bob"}}),
			"新增可登录账号 toor（UID 0）", SeverityWarning},
		{"加入 sudo 并删除账号", st([]protocol.AccountUser{root}, protocol.AccountGroup{Name: "sudo", Members: []string{"bob", "eve"}}),
			"账号 bob 已删除或不能再登录|sudo 组新增成员：eve", SeverityWarning},
		{"移出 sudo、换 shell", st([]protocol.AccountUser{root, {Name: "bob", UID: 1001, Shell: "/bin/zsh"}}, protocol.AccountGroup{Name: "sudo"}),
			"账号 bob 的登录 shell /bin/bash → /bin/zsh|sudo 组移除成员：bob", SeverityInfo},
	}
	for _, c := range cases {
		lines, sev := diffAccounts(base, c.cur)
		if got := strings.Join(lines, "|"); got != c.want || sev != c.sev {
			t.Errorf("%s：%q %s，期望 %q %s", c.name, got, sev, c.want, c.sev)
		}
	}
}

// 重要文件：有摘要时只看内容（只改修改时间不算）；sudoers 与 ld.so.preload 的变化为警告。
func TestDiffFiles(t *testing.T) {
	st := func(fs ...protocol.ConfigFile) map[string]fileMeta { return fileState(fs) }
	sshd := protocol.ConfigFile{Path: "/etc/ssh/sshd_config", Size: 10, MTime: 1, SHA256: "aa"}
	sudo := protocol.ConfigFile{Path: "/etc/sudoers", Size: 10, MTime: 1}
	base := st(sshd, sudo)
	touched := sshd
	touched.MTime = 2
	edited := sshd
	edited.SHA256 = "bb"
	sudo2 := sudo
	sudo2.MTime = 5
	cases := []struct {
		name string
		cur  map[string]fileMeta
		want string
		sev  string
	}{
		{"只改修改时间", st(touched, sudo), "", SeverityInfo},
		{"内容变化", st(edited, sudo), "/etc/ssh/sshd_config 已修改", SeverityInfo},
		{"sudoers 变化（无摘要）", st(sshd, sudo2), "/etc/sudoers 已修改", SeverityWarning},
		{"出现 ld.so.preload", st(sshd, sudo, protocol.ConfigFile{Path: "/etc/ld.so.preload", Size: 20}), "新出现 /etc/ld.so.preload", SeverityWarning},
		{"删除", st(sudo), "已删除 /etc/ssh/sshd_config", SeverityInfo},
	}
	for _, c := range cases {
		lines, sev := diffFiles(base, c.cur)
		if got := strings.Join(lines, "|"); got != c.want || sev != c.sev {
			t.Errorf("%s：%q %s，期望 %q %s", c.name, got, sev, c.want, c.sev)
		}
	}
}

// 账号与重要文件只用来记录变更：省略未变化时面板补全（不会被当作删除），任何接口都不返回（修订第 274 条）。
func TestAccountChangesNotExposed(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	id, tok, _ := s.store.CreateServer("acct-host", 0, 1)
	post := func(body string) {
		t.Helper()
		if rec := do(h, "POST", "/api/v1/agent/report", tok, []byte(body)); rec.Code != 204 {
			t.Fatal(rec.Code, rec.Body)
		}
	}
	acct := func(members string) string {
		return fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"},"accounts":{"users":[{"name":"root","uid":0}],"groups":[{"name":"sudo","members":[%s]}]},
			"config_files":[{"path":"/etc/sudoers","size":1,"mtime":1}]}`, time.Now().Unix(), members)
	}
	post(acct(`"bob"`))
	post(fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"},"unchanged":["accounts","config_files"]}`, time.Now().Unix()))
	post(acct(`"bob","eve"`))
	var out struct{ Items []SystemEvent }
	rec := do(h, "GET", "/api/v1/system-events?kind=change&server_id="+itoa64(id), admin, nil)
	json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Items) != 1 || out.Items[0].Title != "acct-host 账号与权限组有 1 项变化" || out.Items[0].Message != "sudo 组新增成员：eve" {
		t.Fatalf("应只记一条账号变更：%+v", out.Items)
	}
	for _, path := range []string{"/api/v1/servers", "/api/v1/servers/" + itoa64(id)} {
		body := do(h, "GET", path, admin, nil).Body.String()
		if strings.Contains(body, "accounts") || strings.Contains(body, "config_files") || strings.Contains(body, "sudoers") {
			t.Errorf("%s 不应返回账号与重要文件：%s", path, body)
		}
	}
}
