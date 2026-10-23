package main

import (
	"os"
	"os/exec"
	"path/filepath"
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
