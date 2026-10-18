package collector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vpsmon/internal/protocol"
)

// 测试配置中的密钥：结果中一律不能出现（修订第 169 条）。
var proxySecrets = []string{"b831381d-6324-4d53-ad4f-8cda48b30811", "Sup3rSecret", "PRIVKEY-abc", "gostpass", "brookpw", "hy2-auth-pw", "www.example-sni.com"}

func proxyFixture(t *testing.T) proxyEnv {
	t.Helper()
	root := t.TempDir()
	nul := "\x00"
	writeFiles(t, root, map[string]string{
		// xray：命令行指定配置（带注释）；一个 Reality、一个只监听回环的 ws、一个端口范围
		"proc/101/comm":    "xray\n",
		"proc/101/cmdline": "/usr/local/bin/xray" + nul + "run" + nul + "-c" + nul + "/usr/local/etc/xray/config.json" + nul,
		"usr/local/etc/xray/config.json": `{
  // 注释
  "log": {"loglevel": "warning"},
  "inbounds": [
    {"port": 443, "protocol": "vless", "settings": {"clients": [{"id": "b831381d-6324-4d53-ad4f-8cda48b30811"}]},
     "streamSettings": {"network": "tcp", "security": "reality", "realitySettings": {"privateKey": "PRIVKEY-abc", "serverNames": ["www.example-sni.com"]}}},
    {"port": "8080", "listen": "127.0.0.1", "protocol": "vmess", "streamSettings": {"network": "ws", "wsSettings": {"path": "/s3cret"}}},
    /* 端口范围 */
    {"port": "20000-20010", "protocol": "dokodemo-door", "streamSettings": {"network": "kcp"}}
  ]}`,
		// sing-box：默认位置；hysteria2 与 vless reality
		"proc/102/comm": "sing-box\n",
		"etc/sing-box/config.json": `{"inbounds": [
  {"type": "hysteria2", "listen": "::", "listen_port": 8443, "users": [{"password": "Sup3rSecret"}], "tls": {"enabled": true, "server_name": "www.example-sni.com"}},
  {"type": "vless", "listen_port": 2053, "tls": {"enabled": true, "reality": {"enabled": true, "private_key": "PRIVKEY-abc"}}, "transport": {"type": "grpc"}}]}`,
		// Hysteria：有配置没有进程（未运行）
		"etc/hysteria/config.yaml": "listen: :36712\nauth:\n  type: password\n  password: hy2-auth-pw\n",
		// trojan-go 不能被识别成 trojan
		"proc/103/comm":                    "trojan-go\n",
		"etc/trojan-go/config.json":        `{"run_type": "server", "local_port": 4443, "password": ["Sup3rSecret"], "websocket": {"enabled": true, "path": "/x"}}`,
		"etc/shadowsocks-rust/config.json": `{"servers": [{"server": "::", "server_port": 8388, "password": "Sup3rSecret", "method": "2022-blake3-aes-128-gcm", "mode": "tcp_and_udp"}]}`,
		"proc/104/comm":                    "ssserver\n",
		// gost：命令行带密码，只保留协议与端口
		"proc/105/comm":    "gost\n",
		"proc/105/cmdline": "gost" + nul + "-L=ss://aes-256-gcm:gostpass@:8338" + nul + "-L" + nul + "relay+tls://user:gostpass@127.0.0.1:9443?bind=true" + nul,
		// brook wssserver
		"proc/106/comm":    "brook\n",
		"proc/106/cmdline": "brook" + nul + "wssserver" + nul + "--listen" + nul + ":9999" + nul + "-p" + nul + "brookpw" + nul,
		// WireGuard 内核接口（配置只有 root 可读，测试中用权限 000 模拟）
		"sys/class/net/wg0/uevent":  "DEVTYPE=wireguard\nINTERFACE=wg0\n",
		"sys/class/net/eth0/uevent": "INTERFACE=eth0\n",
		"etc/wireguard/wg0.conf":    "[Interface]\nListenPort = 51820\nPrivateKey = PRIVKEY-abc\n",
		// 无关进程
		"proc/1/comm": "systemd\n",
	})
	if err := os.Chmod(filepath.Join(root, "etc/wireguard/wg0.conf"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(root, "etc/wireguard/wg0.conf"), 0o600) })
	return proxyEnv{root: root, proc: "proc", procs: scanProcs(filepath.Join(root, "proc")),
		listening: map[string]bool{"tcp/443": true, "tcp/8080": true, "udp/8443": true, "tcp/4443": true, "tcp/8388": true, "tcp/8338": true}}
}

func proxyByName(ps []protocol.Proxy, name string) *protocol.Proxy {
	for i := range ps {
		if ps[i].Name == name {
			return &ps[i]
		}
	}
	return nil
}

func TestProxies(t *testing.T) {
	e := proxyFixture(t)
	ps := collectProxies(e)

	x := proxyByName(ps, "xray")
	if x == nil || !x.Running || x.Config != "/usr/local/etc/xray/config.json" || len(x.Inbounds) != 3 {
		t.Fatalf("xray 不对：%+v", x)
	}
	if in := x.Inbounds[0]; in != (protocol.ProxyInbound{Protocol: "vless", Port: 443, Network: "tcp", Security: "reality", Listening: true}) {
		t.Errorf("vless reality：%+v", in)
	}
	if in := x.Inbounds[1]; in.Protocol != "vmess" || in.Transport != "ws" || !in.Local || !in.Listening {
		t.Errorf("回环 vmess ws：%+v", in)
	}
	if in := x.Inbounds[2]; in.Port != 20000 || in.Network != "udp" || in.Transport != "kcp" || in.Listening {
		t.Errorf("端口范围 kcp：%+v", in)
	}

	sb := proxyByName(ps, "sing-box")
	if sb == nil || sb.Config != "/etc/sing-box/config.json" || len(sb.Inbounds) != 2 ||
		sb.Inbounds[0] != (protocol.ProxyInbound{Protocol: "vless", Port: 2053, Network: "tcp", Transport: "grpc", Security: "reality"}) ||
		sb.Inbounds[1] != (protocol.ProxyInbound{Protocol: "hysteria2", Port: 8443, Network: "udp", Security: "tls", Listening: true}) {
		t.Fatalf("sing-box 不对：%+v", sb)
	}

	hy := proxyByName(ps, "hysteria")
	if hy == nil || hy.Running || len(hy.Inbounds) != 1 || hy.Inbounds[0].Protocol != "hysteria2" || hy.Inbounds[0].Port != 36712 || hy.Inbounds[0].Network != "udp" {
		t.Fatalf("hysteria（有配置未运行）不对：%+v", hy)
	}

	if tr := proxyByName(ps, "trojan"); tr != nil {
		t.Fatalf("trojan-go 不应识别为 trojan：%+v", tr)
	}
	tg := proxyByName(ps, "trojan-go")
	if tg == nil || !tg.Running || tg.Inbounds[0] != (protocol.ProxyInbound{Protocol: "trojan", Port: 4443, Network: "tcp", Transport: "ws", Security: "tls", Listening: true}) {
		t.Fatalf("trojan-go 不对：%+v", tg)
	}

	ss := proxyByName(ps, "shadowsocks")
	if ss == nil || !ss.Running || len(ss.Inbounds) != 1 || ss.Inbounds[0].Port != 8388 || !ss.Inbounds[0].Listening {
		t.Fatalf("shadowsocks 不对：%+v", ss)
	}

	g := proxyByName(ps, "gost")
	if g == nil || len(g.Inbounds) != 2 || g.Inbounds[0] != (protocol.ProxyInbound{Protocol: "ss", Port: 8338, Network: "tcp", Listening: true}) ||
		g.Inbounds[1] != (protocol.ProxyInbound{Protocol: "relay", Transport: "tls", Port: 9443, Network: "tcp", Local: true}) {
		t.Fatalf("gost 不对：%+v", g)
	}

	b := proxyByName(ps, "brook")
	if b == nil || len(b.Inbounds) != 1 || b.Inbounds[0] != (protocol.ProxyInbound{Protocol: "brook", Port: 9999, Network: "tcp", Transport: "ws", Security: "tls"}) {
		t.Fatalf("brook 不对：%+v", b)
	}

	wg := proxyByName(ps, "wireguard")
	if wg == nil || !wg.Running || wg.Procs != 1 || len(wg.Inbounds) != 0 {
		t.Fatalf("wireguard 不对：%+v", wg)
	}
	if os.Geteuid() != 0 && wg.ConfigNo != "unreadable" { // root 运行测试时权限 000 仍可读
		t.Fatalf("wireguard 配置只有 root 可读时应说明：%+v", wg)
	}

	// 【安全】结果中不能出现任何密钥、密码、SNI 或路径参数
	out, _ := json.Marshal(ps)
	for _, sec := range append(proxySecrets, "/s3cret") {
		if strings.Contains(string(out), sec) {
			t.Errorf("结果中出现了敏感信息 %q：%s", sec, out)
		}
	}
}

// x-ui：自带的 xray 用相对路径（bin/config.json）启动，读不到工作目录时使用 x-ui 的默认位置。
func TestProxiesXUI(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"proc/200/comm":                  "x-ui\n",
		"proc/201/comm":                  "xray-linux-amd\n",
		"proc/201/cmdline":               "bin/xray-linux-amd64\x00-c\x00bin/config.json\x00",
		"usr/local/x-ui/bin/config.json": `{"inbounds": [{"port": 62789, "listen": "127.0.0.1", "protocol": "dokodemo-door", "tag": "api"}, {"port": 31000, "protocol": "vmess"}]}`,
	})
	ps := collectProxies(proxyEnv{root: root, proc: "proc", procs: scanProcs(filepath.Join(root, "proc")), listening: map[string]bool{"tcp/31000": true}})
	if xui := proxyByName(ps, "x-ui"); xui == nil || !xui.Running {
		t.Fatalf("应识别 x-ui：%+v", ps)
	}
	x := proxyByName(ps, "xray")
	if x == nil || !x.Running || x.Config != "/usr/local/x-ui/bin/config.json" || len(x.Inbounds) != 2 || !x.Inbounds[0].Listening || !x.Inbounds[1].Local {
		t.Fatalf("x-ui 的 xray 不对：%+v", x)
	}
}

// 没有任何代理：返回 nil；配置格式无法识别时说明 invalid。
func TestProxiesNoneAndInvalid(t *testing.T) {
	if ps := collectProxies(proxyEnv{root: t.TempDir(), proc: "proc"}); ps != nil {
		t.Fatalf("没有代理时应为 nil：%+v", ps)
	}
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"proc/7/comm": "v2ray\n", "etc/v2ray/config.json": "not json"})
	ps := collectProxies(proxyEnv{root: root, proc: "proc", procs: scanProcs(filepath.Join(root, "proc"))})
	if v := proxyByName(ps, "v2ray"); v == nil || !v.Running || v.ConfigNo != "invalid" || v.Config != "" {
		t.Fatalf("格式无法识别时：%+v", ps)
	}
}
