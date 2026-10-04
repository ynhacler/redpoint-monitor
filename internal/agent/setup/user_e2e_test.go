//go:build linux

package setup

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestUserModeE2E 在一次性的容器中以普通用户安装、运行、保活、卸载 Agent（设计 27.13）：
// 真实的 crontab、后台启动、运行锁与首次上报。只在 CI 中运行（scripts/user-mode-check.sh）：
//
//	VPSMON_E2E_USER=1 AGENT_BIN=/path/to/vpsmon-agent ./setup.test -test.run UserModeE2E -test.v
func TestUserModeE2E(t *testing.T) {
	if os.Getenv("VPSMON_E2E_USER") == "" {
		t.Skip("只在一次性容器中以普通用户运行（会写入家目录与 crontab）")
	}
	if os.Geteuid() == 0 {
		t.Fatal("应以普通用户运行")
	}
	home, _ := os.UserHomeDir()
	p := UserPaths(home)
	us := RealUserSystem{}
	panel, _, unreg := fakePanel(t, 200, okBody)
	var out bytes.Buffer
	o := Options{Server: panel.URL, EnrollCode: "ENR-AAAA-AAAA-AAAA-AAAA", Version: "e2e", Self: os.Getenv("AGENT_BIN"),
		Paths: p, Out: &out, WaitFirst: 20 * time.Second}
	if err := InstallUser(context.Background(), o, us); err != nil {
		t.Fatalf("安装失败：%v\n%s", err, out.String())
	}
	t.Logf("install 输出：\n%s", out.String())
	if !strings.Contains(out.String(), "首次上报成功") || !strings.Contains(out.String(), "保活：crontab") {
		t.Errorf("应以 crontab 保活并完成首次上报")
	}
	cron, _ := exec.Command("crontab", "-l").CombinedOutput()
	if !strings.Contains(string(cron), p.Bin+" keepalive") {
		t.Fatalf("crontab 中应有保活条目：\n%s", cron)
	}

	// 进程被杀后，keepalive（crontab 每 2 分钟调用）重新拉起；已运行时什么也不做
	pid := runningPID(p)
	if pid <= 0 {
		t.Fatal("Agent 应在运行并持有锁")
	}
	syscall.Kill(pid, syscall.SIGKILL)
	waitUntil(t, func() bool { return runningPID(p) == 0 })
	if started, err := Keepalive(p, us); err != nil || !started {
		t.Fatalf("keepalive 应重新启动：%v %v", started, err)
	}
	waitUntil(t, func() bool { return runningPID(p) > 0 && runningPID(p) != pid })
	if started, _ := Keepalive(p, us); started {
		t.Fatal("运行中时 keepalive 不应再启动")
	}

	out.Reset()
	if err := PrintStatus(Options{Paths: p, UserSys: us, Out: &out}); err != nil || !strings.Contains(out.String(), "运行中") {
		t.Errorf("status：%v\n%s", err, out.String())
	}
	t.Logf("status 输出：\n%s", out.String())

	out.Reset()
	if err := UninstallUser(context.Background(), Options{Paths: p, Out: &out}, us); err != nil {
		t.Fatalf("卸载失败：%v\n%s", err, out.String())
	}
	if *unreg == "" {
		t.Error("卸载时应通知面板")
	}
	cron, _ = exec.Command("crontab", "-l").CombinedOutput()
	if strings.Contains(string(cron), "keepalive") || runningPID(p) != 0 {
		t.Errorf("卸载后应去掉 crontab 条目并停止 Agent：\n%s", cron)
	}
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("等待超时")
}
