package collector

// 代理程序（修订第 169 条）：按进程名识别常见的代理程序，从可读的配置文件中取协议、端口、传输方式与 TLS 类型，
// 并核对端口是否正在监听。不需要 root：配置只有 root 可读时只报告是否在运行。
//
// 【安全】
//   - 配置文件路径从进程命令行中取（-c / -config / --config / -confdir），命令行本身不上报；gost、brook 的端口
//     从 -L / -l 参数中取，只保留协议与端口，丢弃其中的用户名与密码
//   - 配置中只取 protocol / port / network / security 等字段，UUID、密码、密钥、SNI、路径一律不读出
//   - 默认位置有配置文件但没有进程时报告为“未运行”，便于发现停掉的代理
//
// 本文件不依赖操作系统：根目录、/proc、进程列表与监听端口都作为参数，测试中用临时目录。

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"vpsmon/internal/protocol"
)

const maxProxyInbounds = 30

// proxyEnv 是采集所需的环境。listening 的键为 “tcp/443”、“udp/443”。
type proxyEnv struct {
	root      string
	proc      string
	procs     []procInfo
	listening map[string]bool
}

// path 把（绝对或相对根目录的）路径放到根目录下。
func (e proxyEnv) path(p string) string { return filepath.Join(e.root, p) }

// proxyKind 描述一种代理程序：进程名、默认配置位置与解析函数。
type proxyKind struct {
	name     string
	comms    []string // 进程名（完全相同，或以 “名称-” 开头，如 xray-linux-amd 是 x-ui 自带的 xray）
	defaults []string // 默认配置文件（相对根目录）
	parse    func(e proxyEnv, data []byte, path string) ([]protocol.ProxyInbound, bool)
	args     func(args []string) []protocol.ProxyInbound // 从命令行参数取入站（gost、brook）；只保留协议与端口
}

var proxyKinds = []proxyKind{
	{name: "xray", comms: []string{"xray"}, defaults: []string{"usr/local/etc/xray/config.json", "etc/xray/config.json", "usr/local/x-ui/bin/config.json"}, parse: parseV2Config},
	{name: "v2ray", comms: []string{"v2ray"}, defaults: []string{"usr/local/etc/v2ray/config.json", "etc/v2ray/config.json"}, parse: parseV2Config},
	{name: "sing-box", comms: []string{"sing-box"}, defaults: []string{"etc/sing-box/config.json", "usr/local/etc/sing-box/config.json"}, parse: parseSingBox},
	{name: "hysteria", comms: []string{"hysteria"}, defaults: []string{"etc/hysteria/config.yaml", "etc/hysteria/config.yml", "etc/hysteria/config.json"}, parse: parseHysteria},
	{name: "trojan-go", comms: []string{"trojan-go"}, defaults: []string{"etc/trojan-go/config.json", "usr/local/etc/trojan-go/config.json"}, parse: parseTrojan},
	{name: "trojan", comms: []string{"trojan"}, defaults: []string{"usr/local/etc/trojan/config.json", "etc/trojan/config.json"}, parse: parseTrojan},
	{name: "shadowsocks", comms: []string{"ss-server", "ssserver", "ss-manager", "ssmanager"}, defaults: []string{"etc/shadowsocks-libev/config.json", "etc/shadowsocks-rust/config.json", "etc/shadowsocks/config.json"}, parse: parseShadowsocks},
	{name: "tuic", comms: []string{"tuic-server", "tuic"}, defaults: []string{"etc/tuic/config.json"}, parse: parseTUIC},
	{name: "gost", comms: []string{"gost"}, args: gostArgs},
	{name: "brook", comms: []string{"brook"}, args: brookArgs},
	{name: "frps", comms: []string{"frps"}, defaults: []string{"etc/frp/frps.toml", "etc/frp/frps.ini", "usr/local/frp/frps.toml"}, parse: parseFRPS},
	{name: "openvpn", comms: []string{"openvpn"}, defaults: []string{"etc/openvpn/server.conf", "etc/openvpn/server/server.conf"}, parse: parseOpenVPN},
	{name: "x-ui", comms: []string{"x-ui", "3x-ui"}},
}

func commMatches(comm string, names []string) bool {
	for _, n := range names {
		if comm == n || strings.HasPrefix(comm, n+"-") {
			return true
		}
	}
	return false
}

// collectProxies 识别各代理程序；没有检测到时返回 nil。
func collectProxies(e proxyEnv) []protocol.Proxy {
	var out []protocol.Proxy
	for _, k := range proxyKinds {
		var pids []string
		for _, p := range e.procs {
			// trojan 不能匹配 trojan-go；“名称-” 前缀只用于 xray-linux-amd 这类带平台后缀的进程名
			if commMatches(p.comm, k.comms) && !(k.name == "trojan" && strings.HasPrefix(p.comm, "trojan-go")) {
				pids = append(pids, p.pid)
			}
		}
		px := protocol.Proxy{Name: k.name, Running: len(pids) > 0, Procs: len(pids)}
		var cfgs []string
		for _, pid := range pids {
			args := readCmdline(e.path(filepath.Join(e.proc, pid, "cmdline")))
			if k.args != nil {
				px.Inbounds = append(px.Inbounds, k.args(args)...)
			}
			cfgs = append(cfgs, configArgs(args)...)
		}
		if k.parse != nil {
			if len(cfgs) == 0 {
				for _, d := range k.defaults {
					if _, err := os.Stat(e.path(d)); err == nil {
						cfgs = append(cfgs, "/"+d)
						break
					}
				}
			}
			for _, c := range dedupe(cfgs) {
				ins, used, why := readProxyConfig(e, k, c)
				if used != "" && px.Config == "" {
					px.Config = used
				}
				if why != "" && px.ConfigNo == "" {
					px.ConfigNo = why
				}
				px.Inbounds = append(px.Inbounds, ins...)
			}
		}
		if !px.Running && px.Config == "" && px.ConfigNo == "" {
			continue // 既没有进程也没有配置：没有安装
		}
		finishInbounds(&px, e.listening)
		out = append(out, px)
	}
	if wg := wireguardIfaces(e); wg != nil {
		finishInbounds(wg, e.listening)
		out = append(out, *wg)
	}
	return out
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// readCmdline 读取以 NUL 分隔的命令行参数（只在本机使用，不上报）。
func readCmdline(p string) []string {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
}

// configArgs 从命令行参数中取配置文件或目录（-c、-config、--config、-confdir、-C，支持 “-c=路径” 形式）。
// 相对路径无法确定工作目录（读不到别的用户的 /proc/<pid>/cwd），忽略。
func configArgs(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, hasVal := strings.Cut(a, "=")
		switch name {
		case "-c", "-config", "--config", "-confdir", "--confdir", "-C", "--c":
			if !hasVal && i+1 < len(args) {
				val, i = args[i+1], i+1
			}
			for _, v := range strings.Split(val, ",") {
				if filepath.IsAbs(v) {
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// readProxyConfig 读取并解析一个配置文件或目录（目录时读其中的 .json）；返回入站、读到的路径与读不到的原因。
func readProxyConfig(e proxyEnv, k proxyKind, path string) (ins []protocol.ProxyInbound, used, why string) {
	full := e.path(strings.TrimPrefix(path, "/"))
	st, err := os.Stat(full)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return nil, "", "unreadable"
		}
		return nil, "", ""
	}
	files := []string{full}
	if st.IsDir() {
		files = nil
		ents, err := os.ReadDir(full)
		if err != nil {
			return nil, "", "unreadable"
		}
		for _, ent := range ents {
			if strings.HasSuffix(ent.Name(), ".json") {
				files = append(files, filepath.Join(full, ent.Name()))
			}
		}
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			why = "unreadable"
			continue
		}
		got, ok := k.parse(e, data, f)
		if !ok {
			if why == "" {
				why = "invalid"
			}
			continue
		}
		used, why = path, ""
		ins = append(ins, got...)
	}
	return
}

// finishInbounds 去重、排序、核对监听并限制数量。
func finishInbounds(px *protocol.Proxy, listening map[string]bool) {
	seen := map[string]bool{}
	var out []protocol.ProxyInbound
	for _, in := range px.Inbounds {
		if in.Port <= 0 || in.Port > 65535 {
			continue
		}
		if in.Network == "" {
			in.Network = "tcp"
		}
		k := in.Protocol + "/" + in.Network + "/" + strconv.Itoa(in.Port)
		if seen[k] {
			continue
		}
		seen[k] = true
		in.Listening = listening[in.Network+"/"+strconv.Itoa(in.Port)]
		out = append(out, in)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	if len(out) > maxProxyInbounds {
		out = out[:maxProxyInbounds]
	}
	px.Inbounds = out
}

// ---- 解析 ----

// stripJSONComments 去掉 JSON 中的 // 与 /* */ 注释（v2ray / xray 允许），字符串中的内容不受影响。
func stripJSONComments(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			out = append(out, c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			out = append(out, c)
			continue
		}
		if c == '/' && i+1 < len(b) && b[i+1] == '/' {
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out = append(out, '\n')
			continue
		}
		if c == '/' && i+1 < len(b) && b[i+1] == '*' {
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++
			continue
		}
		out = append(out, c)
	}
	return out
}

// portOf 取端口：数字、"443"、"10000-10010"（取起始端口）。
func portOf(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		x, _, _ = strings.Cut(x, "-")
		x, _, _ = strings.Cut(x, ",")
		n, _ := strconv.Atoi(strings.TrimSpace(x))
		return n
	}
	return 0
}

// isLocal 判断监听地址是否只在回环上。
func isLocal(addr string) bool {
	addr = strings.Trim(addr, "[]")
	if addr == "localhost" {
		return true
	}
	ip := net.ParseIP(addr)
	return ip != nil && ip.IsLoopback()
}

// splitHostPort 拆分 “[::]:443”、“:443”、“0.0.0.0:443”；只有端口时 host 为空。
func splitHostPort(s string) (string, int) {
	s = strings.TrimSpace(s)
	if h, p, err := net.SplitHostPort(s); err == nil {
		n, _ := strconv.Atoi(p)
		return h, n
	}
	n, _ := strconv.Atoi(s)
	return "", n
}

func parseV2Config(_ proxyEnv, data []byte, _ string) ([]protocol.ProxyInbound, bool) {
	var c struct {
		Inbounds []struct {
			Port           any    `json:"port"`
			Listen         string `json:"listen"`
			Protocol       string `json:"protocol"`
			StreamSettings struct {
				Network  string `json:"network"`
				Security string `json:"security"`
			} `json:"streamSettings"`
		} `json:"inbounds"`
	}
	if json.Unmarshal(stripJSONComments(data), &c) != nil {
		return nil, false
	}
	var out []protocol.ProxyInbound
	for _, in := range c.Inbounds {
		tr := strings.ToLower(in.StreamSettings.Network)
		in2 := protocol.ProxyInbound{Protocol: in.Protocol, Port: portOf(in.Port), Local: isLocal(in.Listen), Security: in.StreamSettings.Security}
		switch tr {
		case "", "tcp", "raw":
		case "kcp", "mkcp", "quic":
			in2.Transport, in2.Network = tr, "udp"
		default:
			in2.Transport = tr
		}
		if in.Protocol == "wireguard" {
			in2.Network = "udp"
		}
		out = append(out, in2)
	}
	return out, true
}

func parseSingBox(_ proxyEnv, data []byte, _ string) ([]protocol.ProxyInbound, bool) {
	var c struct {
		Inbounds []struct {
			Type       string `json:"type"`
			Listen     string `json:"listen"`
			ListenPort int    `json:"listen_port"`
			TLS        struct {
				Enabled bool `json:"enabled"`
				Reality struct {
					Enabled bool `json:"enabled"`
				} `json:"reality"`
			} `json:"tls"`
			Transport struct {
				Type string `json:"type"`
			} `json:"transport"`
		} `json:"inbounds"`
	}
	if json.Unmarshal(stripJSONComments(data), &c) != nil {
		return nil, false
	}
	var out []protocol.ProxyInbound
	for _, in := range c.Inbounds {
		in2 := protocol.ProxyInbound{Protocol: in.Type, Port: in.ListenPort, Local: isLocal(in.Listen), Transport: in.Transport.Type}
		switch {
		case in.TLS.Reality.Enabled:
			in2.Security = "reality"
		case in.TLS.Enabled:
			in2.Security = "tls"
		}
		switch in.Type {
		case "hysteria", "hysteria2", "tuic", "wireguard":
			in2.Network = "udp"
		}
		out = append(out, in2)
	}
	return out, true
}

var yamlListen = regexp.MustCompile(`(?m)^listen:\s*["']?([^"'\s#]+)`)

// parseHysteria 解析 Hysteria 的 YAML（只找顶层 listen）或 JSON；有 auth 段为 Hysteria 2。
func parseHysteria(_ proxyEnv, data []byte, path string) ([]protocol.ProxyInbound, bool) {
	listen, proto := "", "hysteria"
	if strings.HasSuffix(path, ".json") {
		var c struct {
			Listen string          `json:"listen"`
			Auth   json.RawMessage `json:"auth"`
		}
		if json.Unmarshal(data, &c) != nil {
			return nil, false
		}
		listen = c.Listen
		if len(c.Auth) > 0 {
			proto = "hysteria2"
		}
	} else {
		m := yamlListen.FindSubmatch(data)
		if m == nil {
			listen = ":443" // Hysteria 2 的默认端口
		} else {
			listen = string(m[1])
		}
		if regexp.MustCompile(`(?m)^auth:`).Match(data) {
			proto = "hysteria2"
		}
	}
	h, p := splitHostPort(listen)
	return []protocol.ProxyInbound{{Protocol: proto, Port: p, Network: "udp", Security: "tls", Local: isLocal(h)}}, p > 0
}

func parseTrojan(_ proxyEnv, data []byte, _ string) ([]protocol.ProxyInbound, bool) {
	var c struct {
		RunType   string `json:"run_type"`
		LocalAddr string `json:"local_addr"`
		LocalPort int    `json:"local_port"`
		Websocket struct {
			Enabled bool `json:"enabled"`
		} `json:"websocket"`
	}
	if json.Unmarshal(data, &c) != nil {
		return nil, false
	}
	if c.RunType != "" && c.RunType != "server" {
		return nil, true // 客户端配置：没有入站
	}
	in := protocol.ProxyInbound{Protocol: "trojan", Port: c.LocalPort, Security: "tls", Local: isLocal(c.LocalAddr)}
	if c.Websocket.Enabled {
		in.Transport = "ws"
	}
	return []protocol.ProxyInbound{in}, true
}

func parseShadowsocks(_ proxyEnv, data []byte, _ string) ([]protocol.ProxyInbound, bool) {
	type srv struct {
		Server     any    `json:"server"`
		ServerPort int    `json:"server_port"`
		Mode       string `json:"mode"`
	}
	var c struct {
		srv
		Servers []srv `json:"servers"`
	}
	if json.Unmarshal(data, &c) != nil {
		return nil, false
	}
	var out []protocol.ProxyInbound
	for _, s := range append([]srv{c.srv}, c.Servers...) {
		if s.ServerPort == 0 {
			continue
		}
		host, _ := s.Server.(string)
		network := "tcp"
		if s.Mode == "udp_only" {
			network = "udp"
		}
		out = append(out, protocol.ProxyInbound{Protocol: "shadowsocks", Port: s.ServerPort, Network: network, Local: isLocal(host)})
	}
	return out, true
}

func parseTUIC(_ proxyEnv, data []byte, _ string) ([]protocol.ProxyInbound, bool) {
	var c struct {
		Server string `json:"server"`
		Port   int    `json:"port"`
	}
	if json.Unmarshal(data, &c) != nil {
		return nil, false
	}
	h, p := splitHostPort(c.Server)
	if p == 0 {
		p = c.Port
	}
	return []protocol.ProxyInbound{{Protocol: "tuic", Port: p, Network: "udp", Security: "tls", Local: isLocal(h)}}, p > 0
}

var frpPort = regexp.MustCompile(`(?m)^\s*(bindPort|bind_port)\s*=\s*"?(\d+)`)

func parseFRPS(_ proxyEnv, data []byte, _ string) ([]protocol.ProxyInbound, bool) {
	m := frpPort.FindSubmatch(data)
	if m == nil {
		return []protocol.ProxyInbound{{Protocol: "frp", Port: 7000}}, true // 默认端口
	}
	n, _ := strconv.Atoi(string(m[2]))
	return []protocol.ProxyInbound{{Protocol: "frp", Port: n}}, true
}

func parseOpenVPN(_ proxyEnv, data []byte, _ string) ([]protocol.ProxyInbound, bool) {
	in := protocol.ProxyInbound{Protocol: "openvpn", Port: 1194, Network: "udp"}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 || strings.HasPrefix(f[0], "#") || strings.HasPrefix(f[0], ";") {
			continue
		}
		switch f[0] {
		case "port":
			if n, err := strconv.Atoi(f[1]); err == nil {
				in.Port = n
			}
		case "proto":
			if strings.HasPrefix(f[1], "tcp") {
				in.Network = "tcp"
			}
		case "local":
			in.Local = isLocal(f[1])
		}
	}
	return []protocol.ProxyInbound{in}, true
}

// gostArgs 取 gost 的 -L 参数（如 -L=ss://aes-256-gcm:密码@:8338），只保留协议与端口。
func gostArgs(args []string) []protocol.ProxyInbound {
	var out []protocol.ProxyInbound
	for i := 0; i < len(args); i++ {
		name, val, hasVal := strings.Cut(args[i], "=")
		if name != "-L" {
			continue
		}
		if !hasVal && i+1 < len(args) {
			val, i = args[i+1], i+1
		}
		scheme, rest := "auto", val
		if s, r, ok := strings.Cut(val, "://"); ok {
			scheme, rest = s, r
		}
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			rest = rest[at+1:] // 丢弃用户名与密码
		}
		rest, _, _ = strings.Cut(rest, "/")
		rest, _, _ = strings.Cut(rest, "?")
		h, p := splitHostPort(rest)
		if u, err := url.Parse("x://" + rest); p == 0 && err == nil {
			p, _ = strconv.Atoi(u.Port())
		}
		network := "tcp"
		if strings.Contains(scheme, "udp") || strings.Contains(scheme, "quic") || strings.Contains(scheme, "kcp") {
			network = "udp"
		}
		proto, transport, _ := strings.Cut(scheme, "+")
		out = append(out, protocol.ProxyInbound{Protocol: proto, Transport: transport, Port: p, Network: network, Local: isLocal(h)})
	}
	return out
}

// brookArgs 取 brook 服务端的监听端口（server / wsserver / wssserver / quicserver 的 -l / --listen）。
func brookArgs(args []string) []protocol.ProxyInbound {
	mode := ""
	for _, a := range args {
		switch a {
		case "server", "wsserver", "wssserver", "quicserver":
			mode = a
		}
	}
	if mode == "" {
		return nil
	}
	for i := 0; i < len(args); i++ {
		name, val, hasVal := strings.Cut(args[i], "=")
		if name != "-l" && name != "--listen" {
			continue
		}
		if !hasVal && i+1 < len(args) {
			val = args[i+1]
		}
		h, p := splitHostPort(val)
		in := protocol.ProxyInbound{Protocol: "brook", Port: p, Local: isLocal(h)}
		switch mode {
		case "wsserver":
			in.Transport = "ws"
		case "wssserver":
			in.Transport, in.Security = "ws", "tls"
		case "quicserver":
			in.Transport, in.Network = "quic", "udp"
		}
		return []protocol.ProxyInbound{in}
	}
	return nil
}

// wireguardIfaces 识别内核 WireGuard 接口（/sys/class/net/*/uevent 中 DEVTYPE=wireguard）；
// 端口来自 /etc/wireguard/<接口>.conf 的 ListenPort（通常只有 root 可读）。
func wireguardIfaces(e proxyEnv) *protocol.Proxy {
	ents, err := os.ReadDir(e.path("sys/class/net"))
	if err != nil {
		return nil
	}
	var px *protocol.Proxy
	for _, ent := range ents {
		b, err := os.ReadFile(e.path(filepath.Join("sys/class/net", ent.Name(), "uevent")))
		if err != nil || !strings.Contains(string(b), "DEVTYPE=wireguard") {
			continue
		}
		if px == nil {
			px = &protocol.Proxy{Name: "wireguard", Running: true}
		}
		px.Procs++
		conf := "etc/wireguard/" + ent.Name() + ".conf"
		data, err := os.ReadFile(e.path(conf))
		if err != nil {
			if errors.Is(err, fs.ErrPermission) && px.ConfigNo == "" {
				px.ConfigNo = "unreadable"
			}
			continue
		}
		if px.Config == "" {
			px.Config = "/" + conf
		}
		if m := regexp.MustCompile(`(?mi)^\s*ListenPort\s*=\s*(\d+)`).FindSubmatch(data); m != nil {
			n, _ := strconv.Atoi(string(m[1]))
			px.Inbounds = append(px.Inbounds, protocol.ProxyInbound{Protocol: "wireguard", Port: n, Network: "udp"})
		}
	}
	return px
}
