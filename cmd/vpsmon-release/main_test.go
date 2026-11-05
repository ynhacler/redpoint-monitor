package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestPrepare 用假的构建跑一遍 prepare：生成 Agent 与面板的安装脚本、manifest 与 SHA256SUMS；
// 面板安装脚本内置面板构建的哈希与服务文件、没有残留占位符、能通过 sh -n 语法检查（修订第 208 条）。
func TestPrepare(t *testing.T) {
	dist := t.TempDir()
	for _, a := range agentArchs {
		write(t, filepath.Join(dist, "vpsmon-agent-linux-"+a), "agent "+a)
	}
	for _, a := range serverArchs {
		write(t, filepath.Join(dist, "vpsmon-server-linux-"+a), "server "+a)
	}
	root := filepath.Join("..", "..")
	if err := prepare("v1.2.3", "stable", "", dist, filepath.Join(root, "scripts", "agent.sh.in"), root, "", time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(dist, panelInstaller))
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)
	for _, want := range []string{
		`VERSION="1.2.3"`,
		"  vpsmon-server-linux-amd64", "  vpsmon-server-linux-arm64",
		"ExecStart=/usr/local/bin/vpsmon-server run --data /var/lib/vpsmon", // systemd 单元
		"supervisor=supervise-daemon",                                       // OpenRC 服务
	} {
		if !strings.Contains(script, want) {
			t.Errorf("面板安装脚本中缺少 %q", want)
		}
	}
	if m := placeholder.FindString(script); m != "" {
		t.Errorf("残留占位符 %s", m)
	}
	if out, err := exec.Command("sh", "-n", filepath.Join(dist, panelInstaller)).CombinedOutput(); err != nil {
		t.Fatalf("sh -n：%v\n%s", err, out)
	}

	sums, err := os.ReadFile(filepath.Join(dist, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{panelInstaller, "agent-1.2.3.sh", "vpsmon-server-linux-arm64", "vpsmon-agent-linux-mipsle"} {
		if !strings.Contains(string(sums), "  "+f+"\n") {
			t.Errorf("SHA256SUMS 中缺少 %s", f)
		}
	}
}

// TestInstallerSources 运行渲染后的 Agent 安装脚本（curl 与 uname 用假程序代替）：官方地址失败 2 次后才换 --fallback，
// 每个来源都按内置 SHA256 校验，--mirror 只用镜像（设计 27.5.3）。
func TestInstallerSources(t *testing.T) {
	dist := t.TempDir()
	for _, a := range agentArchs {
		write(t, filepath.Join(dist, "vpsmon-agent-linux-"+a), "#!/bin/sh\necho 1.2.3\n")
	}
	for _, a := range serverArchs {
		write(t, filepath.Join(dist, "vpsmon-server-linux-"+a), "server "+a)
	}
	root := filepath.Join("..", "..")
	if err := prepare("v1.2.3", "stable", "", dist, filepath.Join(root, "scripts", "agent.sh.in"), root, "", time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	writeExec := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExec("uname", "#!/bin/sh\nif [ \"$1\" = -s ]; then echo Linux; else echo x86_64; fi\n")
	// 假 curl：按地址选择模式 good / bad / fail，并记录请求的来源
	writeExec("curl", `#!/bin/sh
all="$*"
while [ $# -gt 1 ]; do
  [ "$1" = -o ] && out="$2"
  shift
done
case "$1" in
  */healthz) # 面板检查：不计入下载记录
    case "$HEALTH" in
      ok) echo '{"status":"ok","version":"v9.9.9"}' > "$out"; printf '200 203.0.113.7 0.120' ;;
      notpanel) echo '<html>nginx</html>' > "$out"; printf '200 203.0.113.7 0.120' ;;
      *) echo "curl: (7) Failed to connect" >&2; exit 7 ;;
    esac
    exit 0 ;;
esac
case "$1" in
  *SHA256SUMS*) exit 22 ;; # 主机装有 minisign 时也跳过签名校验（这里的构建没有签名）
esac
case "$1" in
  *github.com*) src=github; mode="$GITHUB_MODE" ;;
  *) src=panel; mode="$PANEL_MODE" ;;
esac
case "$all" in
  *"--speed-limit 51200"*) echo "$src+strict" >> "$CURL_LOG" ;;
  *) echo "$src" >> "$CURL_LOG" ;;
esac
case "$mode" in
  interrupt) kill -INT "$PPID"; exit 130 ;; # 等同用户按 Ctrl+C
  slow) echo partial > "$out"; exit 28 ;;    # 收到部分数据后速度不够
  timeout) exit 28 ;;                        # 连接超时，没有数据
  good) cp "$GOOD" "$out" ;;
  bad) echo tampered > "$out" ;;
  *) exit 22 ;;
esac
`)
	const panel = "https://panel.example/releases"
	cases := []struct {
		name          string
		args          []string
		github, panel string
		want          string // 依次访问的来源
		ok            bool
	}{
		{"官方可用", []string{"--fallback", panel}, "good", "good", "github+strict", true},
		{"官方失败 2 次后用面板", []string{"--fallback", panel}, "fail", "good", "github+strict github+strict panel", true},
		{"官方内容不对时用面板", []string{"--fallback", panel}, "bad", "good", "github+strict github+strict panel", true},
		{"面板内容不对时失败", []string{"--fallback", panel}, "fail", "bad", "github+strict github+strict panel", false},
		{"全部失败", []string{"--fallback", panel}, "fail", "fail", "github+strict github+strict panel", false},
		{"速度太慢时不重试，直接用面板", []string{"--fallback", panel}, "slow", "good", "github+strict panel", true},
		{"连接超时时仍试 2 次", []string{"--fallback", panel}, "timeout", "good", "github+strict github+strict panel", true},
		{"Ctrl+C 立即退出，不试下一个来源", []string{"--fallback", panel}, "interrupt", "good", "github+strict", false},
		{"没有回退地址", nil, "fail", "good", "github github", false},
		{"--mirror 只用镜像", []string{"--mirror", panel}, "good", "good", "panel", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "curl.log")
			cmd := exec.Command("sh", append([]string{filepath.Join(dist, "agent-1.2.3.sh"), "--download-only"}, c.args...)...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "CURL_LOG="+log,
				"GITHUB_MODE="+c.github, "PANEL_MODE="+c.panel, "GOOD="+filepath.Join(dist, "vpsmon-agent-linux-amd64"))
			out, err := cmd.CombinedOutput()
			if (err == nil) != c.ok {
				t.Fatalf("退出状态：%v\n%s", err, out)
			}
			b, _ := os.ReadFile(log)
			if got := strings.Join(strings.Fields(string(b)), " "); got != c.want {
				t.Errorf("访问顺序 %q，应为 %q\n%s", got, c.want, out)
			}
			if c.ok && !strings.Contains(string(out), "[第 "+strconv.Itoa(len(strings.Fields(c.want)))+" 次] 已从") {
				t.Errorf("应显示第几次成功：\n%s", out)
			}
			if _, err := os.Stat(filepath.Join(dir, "vpsmon-agent")); (err == nil) != c.ok {
				t.Errorf("下载结果不符：%v\n%s", err, out)
			}
		})
	}

	// 安装前先检查面板：显示结果，连不上或不是面板时不下载（设计 27.5.3）
	for _, c := range []struct {
		name, health, want string
		ok                 bool
	}{
		{"面板正常", "ok", "面板可访问：HTTP 200 · IP 203.0.113.7 · 耗时 0.12 秒 · 面板版本 v9.9.9", true},
		{"连接被拒绝", "down", "无法访问面板：连接被拒绝", false},
		{"不是面板", "notpanel", "不是 vpsmon 面板", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "curl.log")
			cmd := exec.Command("sh", filepath.Join(dist, "agent-1.2.3.sh"), "--download-only", "--server", "https://panel.example/")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "CURL_LOG="+log, "HEALTH="+c.health,
				"GITHUB_MODE=good", "PANEL_MODE=good", "GOOD="+filepath.Join(dist, "vpsmon-agent-linux-amd64"))
			out, err := cmd.CombinedOutput()
			if (err == nil) != c.ok || !strings.Contains(string(out), c.want) {
				t.Fatalf("退出状态 %v，应包含 %q：\n%s", err, c.want, out)
			}
			if b, _ := os.ReadFile(log); !c.ok && len(b) > 0 {
				t.Errorf("面板不可用时不应下载：%s", b)
			}
		})
	}
}

// 面板构建缺失时不生成发布文件
func TestPrepareMissingServer(t *testing.T) {
	dist := t.TempDir()
	for _, a := range agentArchs {
		write(t, filepath.Join(dist, "vpsmon-agent-linux-"+a), "agent "+a)
	}
	root := filepath.Join("..", "..")
	err := prepare("1.2.3", "stable", "", dist, filepath.Join(root, "scripts", "agent.sh.in"), root, "", time.Unix(0, 0))
	if err == nil || !strings.Contains(err.Error(), "vpsmon-server-linux-amd64") {
		t.Fatalf("应报告缺少面板构建：%v", err)
	}
	if _, err := os.Stat(filepath.Join(dist, "SHA256SUMS")); err == nil {
		t.Fatal("出错时不应生成 SHA256SUMS")
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
