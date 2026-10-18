package collector

import (
	"testing"

	"vpsmon/internal/protocol"
)

func toolOf(fw *protocol.Firewall, name string) *protocol.FirewallTool {
	for i := range fw.Tools {
		if fw.Tools[i].Name == name {
			return &fw.Tools[i]
		}
	}
	return nil
}

// 没有任何防火墙：tools 为空数组（界面显示“未检测到防火墙”）。
func TestFirewallNone(t *testing.T) {
	fw := collectFirewall(fwEnv{root: t.TempDir()})
	if fw == nil || fw.Tools == nil || len(fw.Tools) != 0 {
		t.Fatalf("应为空数组：%+v", fw)
	}
}

// ufw：启用状态、默认策略、user.rules 中的入站规则（v4 / v6 合并去重）；规则不可读时只报状态（修订第 168 条）。
func TestFirewallUFW(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"etc/ufw/ufw.conf":    "# comment\nENABLED=yes\nLOGLEVEL=low\n",
		"etc/default/ufw":     "IPV6=yes\nDEFAULT_INPUT_POLICY=\"DROP\"\n",
		"etc/ufw/user.rules":  "### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in\n-A ufw-user-input -p tcp --dport 22 -j ACCEPT\n### tuple ### allow any 443 0.0.0.0/0 any 0.0.0.0/0 in\n### tuple ### limit tcp 2222 0.0.0.0/0 any 203.0.113.0/24 in\n### tuple ### allow tcp 9000 0.0.0.0/0 any 0.0.0.0/0 out\n",
		"etc/ufw/user6.rules": "### tuple ### allow tcp 22 ::/0 any ::/0 in\n",
	})
	u := toolOf(collectFirewall(fwEnv{root: root}), "ufw")
	if u == nil || !u.Active || u.Policy != "deny" || !u.RulesReadable {
		t.Fatalf("ufw 状态不对：%+v", u)
	}
	want := []protocol.FirewallRule{{Action: "allow", Proto: "tcp", Port: "22"}, {Action: "allow", Port: "443"}, {Action: "limit", Proto: "tcp", Port: "2222", From: "203.0.113.0/24"}}
	if len(u.Rules) != len(want) {
		t.Fatalf("ufw 规则不对：%+v", u.Rules)
	}
	for i := range want {
		if u.Rules[i] != want[i] {
			t.Errorf("第 %d 条：%+v，应为 %+v", i, u.Rules[i], want[i])
		}
	}
	// 规则文件不可读（不存在）：只报状态
	root2 := t.TempDir()
	writeFiles(t, root2, map[string]string{"etc/ufw/ufw.conf": "ENABLED=no\n"})
	u2 := toolOf(collectFirewall(fwEnv{root: root2}), "ufw")
	if u2 == nil || u2.Active || u2.RulesReadable || len(u2.Rules) != 0 {
		t.Fatalf("规则不可读时：%+v", u2)
	}
}

// firewalld：进程在运行、默认区域的服务与端口。
func TestFirewallFirewalld(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"etc/firewalld/firewalld.conf": "DefaultZone=public\n",
		"usr/lib/firewalld/zones/public.xml": `<?xml version="1.0" encoding="utf-8"?>
<zone><short>Public</short><service name="ssh"/><service name="dhcpv6-client"/><port protocol="tcp" port="8000-8100"/></zone>`,
	})
	f := toolOf(collectFirewall(fwEnv{root: root, running: map[string]bool{"firewalld": true}}), "firewalld")
	if f == nil || !f.Active || f.Policy != "deny" || len(f.Rules) != 3 || f.Rules[0].Service != "ssh" || f.Rules[2].Port != "8000:8100" || f.Rules[2].Proto != "tcp" {
		t.Fatalf("firewalld 不对：%+v", f)
	}
}

// iptables-save 与 nftables.conf：默认策略、按端口 / 来源的规则；回环与通用规则不列出。
func TestFirewallIptablesNftables(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"etc/iptables/rules.v4": `*nat
:PREROUTING ACCEPT [0:0]
-A PREROUTING -p tcp --dport 80 -j REDIRECT --to-ports 8080
COMMIT
*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
-A INPUT -i lo -j ACCEPT
-A INPUT -m state --state RELATED,ESTABLISHED -j ACCEPT
-A INPUT -p tcp -m tcp --dport 22 -j ACCEPT
-A INPUT -s 198.51.100.7/32 -j DROP
-A INPUT -p tcp -m multiport --dports 80,443 -j ACCEPT
COMMIT
`,
		"etc/nftables.conf": `table inet filter {
	chain input {
		type filter hook input priority 0; policy drop;
		iif lo accept
		tcp dport { 22, 443 } accept
		udp dport 51820 accept
		tcp dport 8000-8100 drop # 测试
	}
}`,
	})
	fw := collectFirewall(fwEnv{root: root, modules: "nf_tables 1 0 - Live\nip_tables 2 0 - Live\n"})
	ipt := toolOf(fw, "iptables")
	if ipt == nil || !ipt.Active || ipt.Policy != "deny" || len(ipt.Rules) != 3 {
		t.Fatalf("iptables 不对：%+v", ipt)
	}
	if ipt.Rules[0] != (protocol.FirewallRule{Action: "allow", Proto: "tcp", Port: "22"}) || ipt.Rules[1] != (protocol.FirewallRule{Action: "deny", From: "198.51.100.7/32"}) ||
		ipt.Rules[2].Port != "80,443" {
		t.Fatalf("iptables 规则不对：%+v", ipt.Rules)
	}
	nft := toolOf(fw, "nftables")
	if nft == nil || !nft.Active || nft.Policy != "deny" || len(nft.Rules) != 4 {
		t.Fatalf("nftables 不对：%+v", nft)
	}
	if nft.Rules[2] != (protocol.FirewallRule{Action: "allow", Proto: "udp", Port: "51820"}) || nft.Rules[3] != (protocol.FirewallRule{Action: "deny", Proto: "tcp", Port: "8000:8100"}) {
		t.Fatalf("nftables 规则不对：%+v", nft.Rules)
	}
	// 只加载了模块、没有配置文件：报告存在但规则不可读
	nftOnly := toolOf(collectFirewall(fwEnv{root: t.TempDir(), modules: "nf_tables 1 0 - Live\n"}), "nftables")
	if nftOnly == nil || !nftOnly.Active || nftOnly.RulesReadable {
		t.Fatalf("只有模块时：%+v", nftOnly)
	}
}

// fail2ban：启用的 jail（jail.d 覆盖 jail.local）；CSF：TCP_IN / UDP_IN。
func TestFirewallFail2banCSF(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"etc/fail2ban/jail.local":           "[DEFAULT]\nenabled = true\n[sshd]\nenabled = true\n[nginx-http-auth]\nenabled = true\n",
		"etc/fail2ban/jail.d/override.conf": "[nginx-http-auth]\nenabled = false\n[recidive]\nenabled=yes\n",
		"etc/csf/csf.conf":                  "TESTING = \"0\"\nTCP_IN = \"22,80,443,30000:35000\"\nUDP_IN = \"53\"\n",
	})
	fw := collectFirewall(fwEnv{root: root, running: map[string]bool{"fail2ban-server": true, "lfd": true}})
	f2b := toolOf(fw, "fail2ban")
	if f2b == nil || !f2b.Active || len(f2b.Rules) != 2 || f2b.Rules[0].Service != "recidive" || f2b.Rules[1].Service != "sshd" {
		t.Fatalf("fail2ban 不对：%+v", f2b)
	}
	csf := toolOf(fw, "csf")
	if csf == nil || !csf.Active || len(csf.Rules) != 5 || csf.Rules[3].Port != "30000:35000" || csf.Rules[4] != (protocol.FirewallRule{Action: "allow", Proto: "udp", Port: "53"}) {
		t.Fatalf("csf 不对：%+v", csf)
	}
}

// 进程扫描只读 comm。
func TestScanProcs(t *testing.T) {
	proc := t.TempDir()
	writeFiles(t, proc, map[string]string{"1/comm": "systemd\n", "42/comm": "firewalld\n", "self/comm": "x\n", "43/stat": "x"})
	names := procNames(scanProcs(proc))
	if !names["firewalld"] || !names["systemd"] || len(names) != 2 {
		t.Fatalf("进程名不对：%v", names)
	}
}
