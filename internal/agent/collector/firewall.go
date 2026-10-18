package collector

// 防火墙（修订第 168 条）：Agent 不以 root 运行（约束 8），读不到内核中的实时规则（iptables / nft 需要 CAP_NET_ADMIN），
// 只报告不需要特权能看到的部分：
//
//	ufw        /etc/ufw/ufw.conf（ENABLED）、/etc/default/ufw（入站默认策略）；规则 /etc/ufw/user.rules（通常只有 root 可读）
//	firewalld  进程是否在运行；默认区域（/etc/firewalld/firewalld.conf）的服务与端口（zones/*.xml，通常可读）
//	iptables   内核模块是否加载（/proc/modules）；规则 /etc/iptables/rules.v4、/etc/sysconfig/iptables（通常只有 root 可读）
//	nftables   内核模块是否加载；规则 /etc/nftables.conf 中的 dport 放行 / 拒绝与 input 链的默认策略（只做简单识别）
//	fail2ban   进程是否在运行；启用的 jail（jail.local、jail.d/*.conf）
//	csf        进程 lfd 是否在运行；/etc/csf/csf.conf 的 TCP_IN / UDP_IN（通常只有 root 可读）
//
// 配置文件中的规则不一定等于实际生效的规则（可能尚未加载，或被其他程序改写），界面中注明来源。
// 本文件不依赖操作系统：根目录、/proc 与运行中的进程名都作为参数，测试中用临时目录。

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"vpsmon/internal/protocol"
)

const maxFirewallRules = 100

// fwEnv 是采集所需的环境：root 为文件系统根（“/”），modules 为 /proc/modules 的内容，running 为运行中的进程名。
type fwEnv struct {
	root    string
	modules string
	running map[string]bool
}

func (e fwEnv) read(p string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(e.root, p))
	return string(b), err == nil
}

func (e fwEnv) exists(p string) bool {
	_, err := os.Stat(filepath.Join(e.root, p))
	return err == nil
}

func (e fwEnv) module(names ...string) bool {
	for _, line := range strings.Split(e.modules, "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			for _, n := range names {
				if f[0] == n {
					return true
				}
			}
		}
	}
	return false
}

// collectFirewall 汇总各防火墙；没有检测到时 Tools 为空数组。
func collectFirewall(e fwEnv) *protocol.Firewall {
	fw := &protocol.Firewall{Tools: []protocol.FirewallTool{}}
	for _, f := range []func(fwEnv) *protocol.FirewallTool{fwUFW, fwFirewalld, fwNftables, fwIptables, fwFail2ban, fwCSF} {
		if t := f(e); t != nil {
			if len(t.Rules) > maxFirewallRules {
				t.Rules = t.Rules[:maxFirewallRules]
			}
			fw.Tools = append(fw.Tools, *t)
		}
	}
	return fw
}

// keyValue 读取 KEY=value 形式的配置项（忽略引号与注释）。
func keyValue(s, key string) (string, bool) {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(v), `"'`), true
		}
	}
	return "", false
}

func policyOf(v string) string {
	switch strings.ToUpper(v) {
	case "DROP", "REJECT":
		return "deny"
	case "ACCEPT":
		return "allow"
	}
	return ""
}

// ---- ufw ----

func fwUFW(e fwEnv) *protocol.FirewallTool {
	conf, ok := e.read("etc/ufw/ufw.conf")
	if !ok {
		return nil
	}
	t := &protocol.FirewallTool{Name: "ufw", Source: "/etc/ufw/user.rules"}
	if v, ok := keyValue(conf, "ENABLED"); ok {
		t.Active = strings.EqualFold(v, "yes")
	}
	if def, ok := e.read("etc/default/ufw"); ok {
		if v, ok := keyValue(def, "DEFAULT_INPUT_POLICY"); ok {
			t.Policy = policyOf(v)
		}
	}
	seen := map[string]bool{}
	for _, f := range []string{"etc/ufw/user.rules", "etc/ufw/user6.rules"} {
		s, ok := e.read(f)
		if !ok {
			continue
		}
		t.RulesReadable = true
		for _, r := range parseUFWRules(s) {
			if k := r.Action + r.Proto + r.Port + r.From; !seen[k] {
				seen[k] = true
				t.Rules = append(t.Rules, r)
			}
		}
	}
	return t
}

// parseUFWRules 解析 user.rules 中的 “### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in” 行（只取入站）。
func parseUFWRules(s string) []protocol.FirewallRule {
	var out []protocol.FirewallRule
	for _, line := range strings.Split(s, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "### tuple ###")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 7 || f[len(f)-1] != "in" && !strings.HasPrefix(f[len(f)-1], "in_") {
			continue
		}
		r := protocol.FirewallRule{Action: f[0], Proto: f[1], Port: f[2], From: f[5]}
		if strings.Contains(r.Action, "_") { // allow_log 等
			r.Action, _, _ = strings.Cut(r.Action, "_")
		}
		if r.Proto == "any" {
			r.Proto = ""
		}
		if r.Port == "any" {
			r.Port = ""
		}
		if r.From == "0.0.0.0/0" || r.From == "::/0" || r.From == "any" {
			r.From = ""
		}
		out = append(out, r)
	}
	return out
}

// ---- firewalld ----

type fwdZone struct {
	Target   string `xml:"target,attr"`
	Services []struct {
		Name string `xml:"name,attr"`
	} `xml:"service"`
	Ports []struct {
		Port     string `xml:"port,attr"`
		Protocol string `xml:"protocol,attr"`
	} `xml:"port"`
	Sources []struct {
		Address string `xml:"address,attr"`
	} `xml:"source"`
}

func fwFirewalld(e fwEnv) *protocol.FirewallTool {
	if !e.running["firewalld"] && !e.exists("etc/firewalld/firewalld.conf") {
		return nil
	}
	t := &protocol.FirewallTool{Name: "firewalld", Active: e.running["firewalld"]}
	zone := "public"
	if conf, ok := e.read("etc/firewalld/firewalld.conf"); ok {
		if v, ok := keyValue(conf, "DefaultZone"); ok && v != "" {
			zone = v
		}
	}
	for _, p := range []string{"etc/firewalld/zones/" + zone + ".xml", "usr/lib/firewalld/zones/" + zone + ".xml"} {
		s, ok := e.read(p)
		if !ok {
			continue
		}
		var z fwdZone
		if xml.Unmarshal([]byte(s), &z) != nil {
			continue
		}
		t.RulesReadable, t.Source = true, "/"+p+"（默认区域 "+zone+"）"
		if z.Target == "ACCEPT" {
			t.Policy = "allow"
		} else {
			t.Policy = "deny" // firewalld 区域默认拒绝未列出的入站
		}
		from := ""
		if len(z.Sources) == 1 {
			from = z.Sources[0].Address
		}
		for _, sv := range z.Services {
			t.Rules = append(t.Rules, protocol.FirewallRule{Action: "allow", Service: sv.Name, From: from})
		}
		for _, pt := range z.Ports {
			t.Rules = append(t.Rules, protocol.FirewallRule{Action: "allow", Proto: pt.Protocol, Port: strings.ReplaceAll(pt.Port, "-", ":"), From: from})
		}
		break
	}
	return t
}

// ---- iptables ----

func fwIptables(e fwEnv) *protocol.FirewallTool {
	loaded := e.module("ip_tables", "iptable_filter")
	files := []string{"etc/iptables/rules.v4", "etc/sysconfig/iptables", "etc/iptables/rules.v6"}
	present := ""
	for _, f := range files {
		if e.exists(f) {
			present = f
			break
		}
	}
	if !loaded && present == "" {
		return nil
	}
	t := &protocol.FirewallTool{Name: "iptables", Active: loaded}
	for _, f := range files {
		s, ok := e.read(f)
		if !ok {
			continue
		}
		t.RulesReadable, t.Source = true, "/"+f
		policy, rules := parseIptablesSave(s)
		if t.Policy == "" {
			t.Policy = policy
		}
		t.Rules = append(t.Rules, rules...)
	}
	if !t.RulesReadable && present != "" {
		t.Source = "/" + present
	}
	return t
}

// parseIptablesSave 解析 iptables-save 格式：filter 表 INPUT 链的默认策略与 “-A INPUT …” 规则。
func parseIptablesSave(s string) (policy string, rules []protocol.FirewallRule) {
	table := ""
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "*"):
			table = line[1:]
		case table != "filter":
		case strings.HasPrefix(line, ":INPUT "):
			if f := strings.Fields(line); len(f) > 1 {
				policy = policyOf(f[1])
			}
		case strings.HasPrefix(line, "-A INPUT "):
			f := strings.Fields(line)
			r := protocol.FirewallRule{}
			for i := 0; i < len(f)-1; i++ {
				switch f[i] {
				case "-p":
					r.Proto = f[i+1]
				case "--dport", "--dports":
					r.Port = f[i+1]
				case "-s":
					r.From = f[i+1]
				case "-j":
					switch f[i+1] {
					case "ACCEPT":
						r.Action = "allow"
					case "DROP":
						r.Action = "deny"
					case "REJECT":
						r.Action = "reject"
					}
				}
			}
			// 只保留按端口或来源的放行 / 拒绝；回环、已建立连接等通用规则不列出
			if r.Action != "" && (r.Port != "" || r.From != "") && !strings.Contains(line, "-i lo") {
				rules = append(rules, r)
			}
		}
	}
	return
}

// ---- nftables ----

var (
	nftRule   = regexp.MustCompile(`\b(tcp|udp)\s+dport\s+(\{[^}]*\}|[0-9]+(?:-[0-9]+)?)[^\n#]*?\b(accept|drop|reject)\b`)
	nftPolicy = regexp.MustCompile(`type\s+filter\s+hook\s+input[^;]*;[^}]*?policy\s+(accept|drop)`)
)

func fwNftables(e fwEnv) *protocol.FirewallTool {
	loaded := e.module("nf_tables")
	conf, ok := e.read("etc/nftables.conf")
	if !loaded && !ok {
		return nil
	}
	t := &protocol.FirewallTool{Name: "nftables", Active: loaded}
	if ok {
		t.RulesReadable, t.Source = true, "/etc/nftables.conf"
		if m := nftPolicy.FindStringSubmatch(conf); m != nil {
			t.Policy = policyOf(m[1])
		}
		for _, m := range nftRule.FindAllStringSubmatch(conf, -1) {
			action := map[string]string{"accept": "allow", "drop": "deny", "reject": "reject"}[m[3]]
			ports := strings.Trim(m[2], "{} ")
			for _, p := range strings.Split(ports, ",") {
				if p = strings.TrimSpace(p); p != "" {
					t.Rules = append(t.Rules, protocol.FirewallRule{Action: action, Proto: m[1], Port: strings.ReplaceAll(p, "-", ":")})
				}
			}
		}
	}
	return t
}

// ---- fail2ban ----

func fwFail2ban(e fwEnv) *protocol.FirewallTool {
	if !e.running["fail2ban-server"] && !e.exists("etc/fail2ban") {
		return nil
	}
	t := &protocol.FirewallTool{Name: "fail2ban", Active: e.running["fail2ban-server"]}
	files := []string{"etc/fail2ban/jail.local"}
	if ents, err := os.ReadDir(filepath.Join(e.root, "etc/fail2ban/jail.d")); err == nil {
		for _, ent := range ents {
			if strings.HasSuffix(ent.Name(), ".conf") || strings.HasSuffix(ent.Name(), ".local") {
				files = append(files, "etc/fail2ban/jail.d/"+ent.Name())
			}
		}
	}
	enabled := map[string]bool{}
	for _, f := range files {
		s, ok := e.read(f)
		if !ok {
			continue
		}
		t.RulesReadable = true
		for jail, on := range parseJails(s) {
			enabled[jail] = on // 后读的文件覆盖先读的（与 fail2ban 相同）
		}
	}
	names := make([]string, 0, len(enabled))
	for j, on := range enabled {
		if on {
			names = append(names, j)
		}
	}
	sort.Strings(names)
	for _, j := range names {
		t.Rules = append(t.Rules, protocol.FirewallRule{Action: "ban", Service: j})
	}
	if t.RulesReadable {
		t.Source = "/etc/fail2ban/jail.local、jail.d"
	}
	return t
}

// parseJails 返回各 jail 的 enabled 设置（只含写明了 enabled 的 jail；[DEFAULT] 不算）。
func parseJails(s string) map[string]bool {
	out := map[string]bool{}
	section := ""
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && section != "" && section != "DEFAULT" && strings.TrimSpace(k) == "enabled" {
			v = strings.ToLower(strings.TrimSpace(v))
			out[section] = v == "true" || v == "yes" || v == "1"
		}
	}
	return out
}

// ---- CSF ----

func fwCSF(e fwEnv) *protocol.FirewallTool {
	if !e.running["lfd"] && !e.exists("etc/csf") {
		return nil
	}
	t := &protocol.FirewallTool{Name: "csf", Active: e.running["lfd"], Policy: "deny", Source: "/etc/csf/csf.conf"}
	conf, ok := e.read("etc/csf/csf.conf")
	if !ok {
		return t
	}
	t.RulesReadable = true
	for _, k := range []struct{ key, proto string }{{"TCP_IN", "tcp"}, {"UDP_IN", "udp"}} {
		if v, ok := keyValue(conf, k.key); ok {
			for _, p := range strings.Split(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					t.Rules = append(t.Rules, protocol.FirewallRule{Action: "allow", Proto: k.proto, Port: p})
				}
			}
		}
	}
	return t
}
