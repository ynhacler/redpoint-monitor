package setup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Alpine：OpenRC + BusyBox（设计 28）。用 addgroup / adduser 创建用户，写 OpenRC 脚本并用 rc-update / rc-service 启动；
// 特权 updater 依赖 systemd，OpenRC 上不启用远程升级（设计 27.12）。
func TestInstallOpenRC(t *testing.T) {
	p, dir := testPaths(t)
	panel, _, unregToken := fakePanel(t, 200, okBody)
	sys := &fakeSystem{root: true, openrc: true, busybox: true, users: map[string]bool{}}
	sys.onEnable = func() { WriteStatus(p.StateDir, Status{LastSuccess: time.Now().Unix() + 1}) }
	var out bytes.Buffer
	err := Install(context.Background(), Options{Server: panel.URL, EnrollCode: "ENR-AAAA-AAAA-AAAA-AAAA",
		Self: filepath.Join(dir, "downloaded-agent"), Paths: p, Sys: sys, Out: &out, WaitFirst: 2 * time.Second,
		HostInfoRoot: filepath.Join(dir, "host")})
	if err != nil {
		t.Fatalf("OpenRC 上安装应成功：%v\n%s", err, out.String())
	}
	want := []string{"addgroup -S vpsmon-agent",
		"adduser -S -D -H -h /var/empty -s " + nologinShell() + " -G vpsmon-agent vpsmon-agent",
		"rc-update add vpsmon-agent default", "rc-service vpsmon-agent start"}
	if strings.Join(sys.cmds, "\n") != strings.Join(want, "\n") {
		t.Errorf("执行的命令：\n%s\n应为：\n%s", strings.Join(sys.cmds, "\n"), strings.Join(want, "\n"))
	}
	fi, err := os.Stat(p.InitScript)
	if b, _ := os.ReadFile(p.InitScript); err != nil || string(b) != openrcScript || fi.Mode().Perm() != 0o755 {
		t.Errorf("应写入可执行的 OpenRC 脚本：%v", err)
	}
	for _, f := range []string{p.Unit, p.Updater, p.UpdaterPath} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("OpenRC 上不应写入 %s", f)
		}
	}
	if _, err := os.Stat(p.NoRemoteUpgradeFile()); err != nil {
		t.Error("OpenRC 上应写下禁止远程升级的文件")
	}
	if !strings.Contains(out.String(), "sudo vpsmon-agent upgrade") || strings.Contains(out.String(), "enable-remote-upgrade") {
		t.Errorf("应提示本机手动升级，而不是 enable-remote-upgrade：\n%s", out.String())
	}
	if err := EnableRemoteUpgrade(Options{Paths: p, Sys: sys, Out: &out}); err == nil || !strings.Contains(err.Error(), "OpenRC") {
		t.Errorf("OpenRC 上 enable-remote-upgrade 应说明不支持：%v", err)
	}

	out.Reset()
	if err := PrintStatus(Options{Paths: p, Sys: sys, Out: &out}); err != nil || !strings.Contains(out.String(), "OpenRC") {
		t.Errorf("status 应显示 OpenRC 服务状态：%v\n%s", err, out.String())
	}
	sys.cmds = nil
	if err := RestartInstalled(Options{Paths: p, Sys: sys}); err != nil || strings.Join(sys.cmds, ";") != "rc-service vpsmon-agent restart" {
		t.Errorf("本机升级后应用 rc-service 重启：%v %v", err, sys.cmds)
	}

	sys.cmds = nil
	if err := Uninstall(context.Background(), Options{Paths: p, Sys: sys, Out: &out}); err != nil {
		t.Fatal(err)
	}
	if *unregToken == "" {
		t.Error("卸载时应通知面板")
	}
	wantU := "rc-service vpsmon-agent stop;rc-update del vpsmon-agent default;deluser vpsmon-agent;delgroup vpsmon-agent"
	if strings.Join(sys.cmds, ";") != wantU {
		t.Errorf("卸载命令：%v，应为 %s", sys.cmds, wantU)
	}
	if _, err := os.Stat(p.InitScript); err == nil {
		t.Error("卸载后应删除 OpenRC 脚本")
	}
}

func TestInstallNoSupportedInit(t *testing.T) {
	p, dir := testPaths(t)
	panel, _, _ := fakePanel(t, 200, okBody)
	sys := &fakeSystem{root: true, users: map[string]bool{}}
	err := Install(context.Background(), Options{Server: panel.URL, EnrollCode: "ENR-AAAA-AAAA-AAAA-AAAA",
		Self: filepath.Join(dir, "downloaded-agent"), Paths: p, Sys: sys, Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "OpenRC") || len(sys.cmds) != 0 {
		t.Errorf("既没有 systemd 也没有 OpenRC 时应在修改主机前失败：%v %v", err, sys.cmds)
	}
}

// OpenRC 脚本与 deploy/openrc 一致；【安全】参数固定，不 source env 文件：
// env 中的节点名来自面板，被 shell 执行就等于远程执行（安全约束 1）。面板地址由 Agent 用 --env-file 读取。
func TestOpenRCScript(t *testing.T) {
	b, err := os.ReadFile("../../../deploy/openrc/vpsmon-agent")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != openrcScript {
		t.Error("internal/agent/setup/vpsmon-agent.openrc 与 deploy/openrc/vpsmon-agent 不一致，请同步修改")
	}
	for _, line := range strings.Split(openrcScript, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "#") {
			continue
		}
		if strings.HasPrefix(l, ". ") || strings.HasPrefix(l, "source ") || strings.Contains(l, "$") {
			t.Errorf("OpenRC 脚本不能 source 文件或展开变量：%q", l)
		}
	}
	for _, s := range []string{"supervisor=supervise-daemon", "respawn_max=0", "--env-file /etc/vpsmon-agent/env",
		`command_user="vpsmon-agent:vpsmon-agent"`} {
		if !strings.Contains(openrcScript, s) {
			t.Errorf("OpenRC 脚本缺少 %q", s)
		}
	}
}
