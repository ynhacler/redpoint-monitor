package setup

// vpsmon-agent doctor（设计 27.11、43.9）：兼容性与连通性诊断。只读，不修改任何文件或服务。
// 逐项输出 ✓（正常）、!（提醒）、✗（问题）与处理建议；有 ✗ 时返回非零退出码。
//
// 【安全】不输出 Token；连接面板时与上报相同，始终校验证书，不提供跳过的选项（约束 6）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Doctor 执行诊断，返回发现的问题（✗）数。
func Doctor(ctx context.Context, o Options) int {
	o.defaults()
	p := o.Paths
	root := o.HostInfoRoot
	if root == "" {
		root = "/"
	}
	problems := 0
	ok := func(f string, a ...any) { fmt.Fprintf(o.Out, "  ✓ "+f+"\n", a...) }
	warn := func(f string, a ...any) { fmt.Fprintf(o.Out, "  ! "+f+"\n", a...) }
	bad := func(f string, a ...any) { problems++; fmt.Fprintf(o.Out, "  ✗ "+f+"\n", a...) }
	section := func(s string) { fmt.Fprintf(o.Out, "\n%s\n", s) }

	// ---- 系统 ----
	section("系统")
	host := readHostInfo(o.HostInfoRoot)
	ok("%s %s，%s/%s，版本 %s", orUnknown(host.OS), host.OSVersion, runtime.GOOS, goarch, orUnknown(o.Version))
	if runtime.GOOS != "linux" {
		warn("非 Linux 系统：采集器使用模拟数据，只用于开发")
	}

	// ---- 安装 ----
	section("安装")
	user := o.UserSys != nil
	if _, err := os.Stat(p.Bin); err != nil {
		bad("没有找到 %s：请用面板中的安装命令安装", p.Bin)
	} else {
		ok("程序 %s", p.Bin)
	}
	env := readEnv(p.envFile())
	server := env["VPSMON_SERVER"]
	if server == "" {
		bad("没有读到面板地址（%s）：请重新安装，或用 re-enroll --server 指定", p.envFile())
	} else {
		ok("面板 %s，节点 %s（编号 %s）", server, orUnknown(env["VPSMON_NODE_NAME"]), orUnknown(env["VPSMON_SERVER_ID"]))
	}
	token := ""
	if fi, err := os.Stat(p.tokenFile()); err != nil {
		bad("没有 Token 文件 %s：请重新安装", p.tokenFile())
	} else {
		want := os.FileMode(0o640)
		if user {
			want = 0o600
		}
		if fi.Mode().Perm()&^want != 0 {
			bad("【安全】Token 文件权限为 %v，应为 %v：sudo chmod %o %s", fi.Mode().Perm(), want, want, p.tokenFile())
		} else {
			ok("Token 文件权限 %v", fi.Mode().Perm())
		}
		if b, err := os.ReadFile(p.tokenFile()); err == nil {
			token = strings.TrimSpace(string(b))
		} else {
			warn("没有权限读取 Token：用 sudo 运行 doctor 才能检查 Token 是否有效")
		}
	}

	// ---- 服务 ----
	section("服务")
	switch k := installedInit(p); {
	case user:
		ok("用户模式（设计 27.13）：保活方式见 vpsmon-agent status")
	case k == initSystemd:
		if out, err := o.Sys.Run("systemctl", "is-active", serviceName); err != nil || strings.TrimSpace(out) != "active" {
			bad("systemd 服务未运行（%s）：sudo systemctl restart %s，日志见 journalctl -u %s -n 50", strings.TrimSpace(out), serviceName, serviceName)
		} else {
			ok("systemd 服务运行中")
		}
	case k == initOpenRC:
		if out, err := o.Sys.Run("rc-service", serviceName, "status"); err != nil {
			bad("OpenRC 服务未运行：sudo rc-service %s restart（%s）", serviceName, strings.TrimSpace(out))
		} else {
			ok("OpenRC 服务运行中")
		}
	default:
		bad("没有找到服务文件：请用面板中的安装命令重新安装")
	}
	if st, err := ReadStatus(p.statusFile()); err != nil {
		warn("还没有运行状态（%s）：服务刚启动时稍等片刻", p.statusFile())
	} else {
		switch age := time.Since(time.Unix(st.LastSuccess, 0)); {
		case st.LastSuccess == 0:
			bad("从未上报成功：%s", orUnknown(st.LastError))
		case age > 3*time.Minute:
			bad("最近一次成功上报在 %s 前：%s", age.Round(time.Second), orUnknown(st.LastError))
		default:
			ok("最近一次上报在 %s 前", age.Round(time.Second))
		}
		if st.Queued > 0 {
			warn("有 %d 份数据等待补发（断网期间缓存，恢复后自动补发）", st.Queued)
		}
	}

	// ---- 网络与面板 ----
	section("面板连通")
	if v := firstEnv("HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"); v != "" {
		warn("设置了代理 %s：Agent 会经代理连接面板", redactProxy(v))
	}
	panelUp := false
	if server != "" {
		u, err := url.Parse(server)
		if err != nil || u.Host == "" {
			bad("面板地址格式不正确：%s", server)
		} else {
			if addrs, err := net.DefaultResolver.LookupHost(ctx, u.Hostname()); err != nil {
				bad("无法解析 %s：%v（检查 /etc/resolv.conf 或 DNS）", u.Hostname(), err)
			} else {
				ok("DNS：%s → %s", u.Hostname(), strings.Join(addrs, ", "))
			}
			ver, skew, err := panelHealth(ctx, o, server)
			switch {
			case err != nil:
				bad("无法访问面板：%v", err)
			default:
				panelUp = true
				ok("HTTPS 正常（证书有效），面板版本 %s", ver)
				if skew > 60*time.Second || skew < -60*time.Second {
					bad("本机时钟与面板相差 %s：请开启时间同步（如 timedatectl set-ntp true）", skew.Round(time.Second))
				}
			}
		}
	}
	if panelUp && token != "" {
		name, sid, err := whoami(ctx, o, server, token)
		switch {
		case errors.Is(err, errTokenRejected):
			bad("Token 无效或已吊销：在面板的节点详情中重新生成注册码，执行 sudo vpsmon-agent rotate-token --enroll ENR-…")
		case err != nil:
			warn("无法确认 Token：%v（面板版本较旧时没有这个检查）", err)
		case env["VPSMON_SERVER_ID"] != "" && fmt.Sprint(sid) != env["VPSMON_SERVER_ID"]:
			bad("Token 属于节点 %s（编号 %d），与本机配置的编号 %s 不一致：请重新安装或 re-enroll", name, sid, env["VPSMON_SERVER_ID"])
		default:
			ok("Token 有效，属于节点 %s", name)
		}
	}

	// ---- 采集 ----
	section("采集")
	if runtime.GOOS == "linux" || o.HostInfoRoot != "" {
		before := problems
		for _, f := range []string{"proc/stat", "proc/meminfo", "proc/loadavg", "proc/net/dev", "proc/diskstats", "proc/mounts"} {
			if _, err := os.ReadFile(filepath.Join(root, f)); err != nil {
				bad("无法读取 /%s：%v", f, err)
			}
		}
		if _, err := os.Stat(filepath.Join(root, "sys/class/net")); err != nil {
			warn("没有 /sys/class/net：网卡信息可能不完整")
		}
		if problems == before {
			ok("/proc 与 /sys 可读")
		}
	}

	fmt.Fprintln(o.Out)
	if problems == 0 {
		fmt.Fprintln(o.Out, "没有发现问题。")
	} else {
		fmt.Fprintf(o.Out, "发现 %d 个问题，请按上面的提示处理。\n", problems)
	}
	return problems
}

var errTokenRejected = errors.New("token rejected")

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// redactProxy 去掉代理地址中的用户名密码
func redactProxy(v string) string {
	if u, err := url.Parse(v); err == nil && u.User != nil {
		u.User = url.User("…")
		return u.String()
	}
	return v
}

// panelHealth 访问 /healthz：确认 HTTPS 与证书正常，返回面板版本与时钟偏差（本机减面板）。
func panelHealth(ctx context.Context, o Options, server string) (string, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/healthz", nil)
	if err != nil {
		return "", 0, err
	}
	res, err := o.HTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return "", 0, err
	}
	defer res.Body.Close()
	var h struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 4<<10)).Decode(&h) != nil || h.Status != "ok" {
		return "", 0, fmt.Errorf("HTTP %d，不像是 VPS Monitor 面板（检查地址或反向代理）", res.StatusCode)
	}
	var skew time.Duration
	if d, err := http.ParseTime(res.Header.Get("Date")); err == nil {
		skew = time.Since(d)
	}
	return h.Version, skew, nil
}

// whoami 用 Token 访问 GET /api/v1/agent/whoami（只读）。
func whoami(ctx context.Context, o Options, server, token string) (string, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/v1/agent/whoami", nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := o.HTTP.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return "", 0, errTokenRejected
	default:
		return "", 0, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	var w struct {
		ServerID   int64  `json:"server_id"`
		ServerName string `json:"server_name"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<10)).Decode(&w); err != nil {
		return "", 0, err
	}
	return w.ServerName, w.ServerID, nil
}
