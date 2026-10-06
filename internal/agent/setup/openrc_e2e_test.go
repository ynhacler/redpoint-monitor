//go:build linux

package setup

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestOpenRCE2E 在真实的 Alpine 容器中安装、运行、卸载 Agent（设计 28）：BusyBox adduser、OpenRC 脚本、
// supervise-daemon 启动、以 vpsmon-agent 用户运行并完成首次上报。会修改主机，只在 CI 的一次性容器中运行：
//
//	VPSMON_E2E_OPENRC=1 AGENT_BIN=/path/to/vpsmon-agent ./setup.test -test.run OpenRCE2E -test.v
func TestOpenRCE2E(t *testing.T) {
	if os.Getenv("VPSMON_E2E_OPENRC") == "" {
		t.Skip("只在一次性的 Alpine 容器中运行（会创建用户、写入 /etc 与 /usr/local/bin）")
	}
	bin := os.Getenv("AGENT_BIN")
	panel, _, unreg := fakePanel(t, 200, okBody) // 注册与上报都返回 2xx
	var out bytes.Buffer
	o := Options{Server: panel.URL, EnrollCode: "ENR-AAAA-AAAA-AAAA-AAAA", Version: "e2e", Self: bin,
		Out: &out, WaitFirst: 20 * time.Second}
	if err := Install(context.Background(), o); err != nil {
		t.Fatalf("安装失败：%v\n%s", err, out.String())
	}
	t.Logf("install 输出：\n%s", out.String())
	if !strings.Contains(out.String(), "首次上报成功") {
		t.Errorf("应在 OpenRC 下启动并完成首次上报")
	}
	st, _ := exec.Command("rc-service", serviceName, "status").CombinedOutput()
	if !strings.Contains(string(st), "started") {
		t.Errorf("rc-service status 应为 started：%s", st)
	}
	ps, _ := exec.Command("ps", "-o", "user,args").CombinedOutput()
	if !strings.Contains(string(ps), "vpsmon-a") || !strings.Contains(string(ps), "--env-file") {
		t.Errorf("Agent 应以 vpsmon-agent 用户、按 OpenRC 脚本的参数运行：\n%s", ps)
	}
	out.Reset()
	if err := PrintStatus(Options{Out: &out}); err != nil {
		t.Errorf("status 失败：%v", err)
	}
	t.Logf("status 输出：\n%s", out.String())

	// 远程升级（设计 27.12）：enable-remote-upgrade 启动 crond 并写入检查脚本，run-parts 会执行它
	out.Reset()
	if err := EnableRemoteUpgrade(Options{Self: bin, Out: &out}); err != nil {
		t.Fatalf("启用远程升级失败：%v\n%s", err, out.String())
	}
	t.Logf("enable-remote-upgrade 输出：\n%s", out.String())
	if !crondRunning(realSystem{}) {
		t.Error("应启动 crond")
	}
	if rp, _ := exec.Command("run-parts", "--test", "/etc/periodic/15min").CombinedOutput(); !strings.Contains(string(rp), "vpsmon-agent-updater") {
		t.Errorf("run-parts 应执行检查脚本：%s", rp)
	}
	if b, err := exec.Command(DefaultPaths.UpdaterCron).CombinedOutput(); err != nil {
		t.Errorf("没有升级请求时检查脚本应直接退出：%v %s", err, b)
	}
	// 暂存目录被 root 占用（远程升级报 permission denied 的情形）：FixStageDir 把属主改回 Agent 用户
	stage := DefaultPaths.StateDir + "/update"
	os.MkdirAll(stage, 0o755)
	os.Chown(stage, 0, 0)
	if fixed, err := FixStageDir(Options{}); err != nil || !fixed {
		t.Errorf("应修复暂存目录：%v %v", fixed, err)
	}
	uid, _, _ := (realSystem{}).IDs(userName)
	if fi, err := os.Stat(stage); err != nil {
		t.Error(err)
	} else if owner, _ := fileOwner(fi); owner != uid {
		t.Errorf("暂存目录属主应为 %s（uid %d），实际 %d", userName, uid, owner)
	}
	out.Reset()
	if n := Doctor(context.Background(), Options{Out: &out, Version: "e2e"}); strings.Contains(out.String(), "暂存目录") {
		t.Errorf("修复后 doctor 不应再报告暂存目录（%d 个问题）：\n%s", n, out.String())
	}

	out.Reset()
	if err := Uninstall(context.Background(), Options{Out: &out}); err != nil {
		t.Fatalf("卸载失败：%v\n%s", err, out.String())
	}
	if *unreg == "" {
		t.Error("卸载时应通知面板")
	}
	if _, err := os.Stat(DefaultPaths.InitScript); err == nil {
		t.Error("卸载后应删除 OpenRC 脚本")
	}
	if _, err := os.Stat(DefaultPaths.UpdaterCron); err == nil {
		t.Error("卸载后应删除 crond 检查脚本")
	}
	if (realSystem{}).UserExists(userName) {
		t.Error("卸载后应删除用户")
	}
}
