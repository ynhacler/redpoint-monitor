package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeUserSystem 记录用户模式对主机的操作。
type fakeUserSystem struct {
	systemdUser, linger bool
	cronOK              bool
	cron                string
	cronErr             error
	runs                []string
	started             [][]string
	onStart             func()
}

func (f *fakeUserSystem) SystemdUser() (bool, bool) { return f.systemdUser, f.linger }
func (f *fakeUserSystem) Crontab() (string, bool)   { return f.cron, f.cronOK }
func (f *fakeUserSystem) SetCrontab(c string) error {
	if f.cronErr != nil {
		return f.cronErr
	}
	f.cron = c
	return nil
}
func (f *fakeUserSystem) Run(name string, args ...string) (string, error) {
	f.runs = append(f.runs, name+" "+strings.Join(args, " "))
	return "", nil
}
func (f *fakeUserSystem) StartDetached(bin string, args []string, logPath string) error {
	f.started = append(f.started, append([]string{bin}, args...))
	if f.onStart != nil {
		f.onStart()
	}
	return nil
}

func userTestSetup(t *testing.T) (Paths, string, Options) {
	_, dir := testPaths(t) // 主机信息文件与“下载的程序”
	home := filepath.Join(dir, "home", "alice")
	os.MkdirAll(home, 0o755)
	p := UserPaths(home)
	panel, _, _ := fakePanel(t, 200, okBody)
	o := Options{Server: panel.URL, EnrollCode: "ENR-7KQ2-9XPA-M4TD-H3WC", Version: "v-test",
		Self: filepath.Join(dir, "downloaded-agent"), Paths: p, WaitFirst: time.Second, HostInfoRoot: filepath.Join(dir, "host")}
	return p, dir, o
}

func TestInstallUserCron(t *testing.T) {
	p, _, o := userTestSetup(t)
	us := &fakeUserSystem{systemdUser: true, linger: false, cronOK: true, cron: "0 3 * * * /home/alice/backup.sh\n"}
	us.onStart = func() { WriteStatus(p.StateDir, Status{LastSuccess: time.Now().Unix() + 1}) }
	var out bytes.Buffer
	o.Out = &out
	if err := InstallUser(context.Background(), o, us); err != nil {
		t.Fatalf("安装失败：%v\n%s", err, out.String())
	}
	// 【安全】目录 0700、Token 0600，只有当前用户可读
	for path, want := range map[string]os.FileMode{p.ConfDir: 0o700, p.StateDir: 0o700, p.tokenFile(): 0o600, p.envFile(): 0o600} {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s 权限应为 %o：%v %v", path, want, fi, err)
		}
	}
	if b, _ := os.ReadFile(p.Bin); string(b) != "#!binary" {
		t.Error("程序应复制到 ~/.local/bin")
	}
	// 没有开启 linger：不用 systemd 用户服务，改用 crontab；保留用户原有的条目
	if !strings.Contains(us.cron, "0 3 * * * /home/alice/backup.sh") ||
		!strings.Contains(us.cron, "@reboot "+p.Bin+" keepalive") || !strings.Contains(us.cron, "*/2 * * * * "+p.Bin+" keepalive") {
		t.Fatalf("crontab：\n%s", us.cron)
	}
	if strings.Contains(us.cron, "agt_") {
		t.Fatal("【安全】Token 不能写进 crontab")
	}
	if _, err := os.Stat(p.Unit); err == nil {
		t.Error("未开启 linger 时不应写 systemd 用户单元")
	}
	if len(us.started) != 1 || !strings.Contains(strings.Join(us.started[0], " "), "--lock-file "+p.lockFile()) {
		t.Fatalf("应在后台启动一次（带运行锁）：%v", us.started)
	}
	if !strings.Contains(out.String(), "enable-linger") || !strings.Contains(out.String(), "首次上报成功") {
		t.Errorf("输出：%s", out.String())
	}

	// 卸载：去掉本程序的 crontab 行，保留用户自己的；删除文件
	us.onStart = nil
	if err := UninstallUser(context.Background(), Options{Paths: p, Out: &out}, us); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(us.cron, "keepalive") || !strings.Contains(us.cron, "backup.sh") {
		t.Fatalf("卸载后的 crontab：\n%s", us.cron)
	}
	for _, f := range []string{p.Bin, p.ConfDir, p.StateDir} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("卸载后 %s 仍存在", f)
		}
	}
}

func TestInstallUserSystemd(t *testing.T) {
	p, _, o := userTestSetup(t)
	us := &fakeUserSystem{systemdUser: true, linger: true, cronOK: true}
	o.Out = &bytes.Buffer{}
	if err := InstallUser(context.Background(), o, us); err != nil {
		t.Fatal(err)
	}
	unit, err := os.ReadFile(p.Unit)
	if err != nil {
		t.Fatal("开启 linger 时应写 systemd 用户单元")
	}
	for _, want := range []string{"Type=notify", "Restart=always", "WatchdogSec=120", "WantedBy=default.target",
		"--token-file " + p.tokenFile(), "--state-dir " + p.StateDir} {
		if !strings.Contains(string(unit), want) {
			t.Errorf("用户单元缺少 %q：\n%s", want, unit)
		}
	}
	if strings.Contains(string(unit), "agt_") || strings.Contains(string(unit), "--lock-file") {
		t.Error("单元中不应有 Token；systemd 自身保证单实例，不需要运行锁")
	}
	if !strings.Contains(strings.Join(us.runs, "\n"), "systemctl --user enable --now vpsmon-agent") || us.cron != "" {
		t.Fatalf("命令：%v，crontab：%q", us.runs, us.cron)
	}
	if UserKeepMethod(p, us) != keepSystemdUser {
		t.Fatal("应识别为 systemd 用户服务")
	}
}

func TestInstallUserNoKeepalive(t *testing.T) {
	_, _, o := userTestSetup(t)
	us := &fakeUserSystem{}
	var out bytes.Buffer
	o.Out = &out
	if err := InstallUser(context.Background(), o, us); err != nil {
		t.Fatal(err)
	}
	if len(us.started) != 1 || !strings.Contains(out.String(), "重启后需手动执行") {
		t.Fatalf("没有保活方式时应启动一次并说明：%v\n%s", us.started, out.String())
	}
}

// crontab 写入失败：回滚已写入的文件
func TestInstallUserRollback(t *testing.T) {
	p, _, o := userTestSetup(t)
	us := &fakeUserSystem{cronOK: true, cronErr: errors.New("crontab: not allowed")}
	o.Out = &bytes.Buffer{}
	err := InstallUser(context.Background(), o, us)
	if err == nil || !strings.Contains(err.Error(), "已回滚") {
		t.Fatalf("应失败并回滚：%v", err)
	}
	for _, f := range []string{p.Bin, p.ConfDir, p.StateDir} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("回滚后 %s 仍存在", f)
		}
	}
}

// 运行锁：同一时间只有一个实例；keepalive 在 Agent 运行时什么也不做
func TestRunLockAndKeepalive(t *testing.T) {
	p, _, o := userTestSetup(t)
	os.MkdirAll(p.ConfDir, 0o700)
	os.WriteFile(p.tokenFile(), []byte("agt_x\n"), 0o600)
	us := &fakeUserSystem{}

	release, ok, err := AcquireRunLock(p.lockFile())
	if err != nil || !ok {
		t.Fatalf("第一次加锁：%v %v", ok, err)
	}
	if _, ok2, _ := AcquireRunLock(p.lockFile()); ok2 {
		t.Fatal("已有实例时不应再拿到锁")
	}
	if pid := runningPID(p); pid != os.Getpid() {
		t.Fatalf("runningPID = %d", pid)
	}
	if started, err := Keepalive(p, us); err != nil || started || len(us.started) != 0 {
		t.Fatalf("运行中时 keepalive 不应启动：%v %v", started, err)
	}
	release()
	if runningPID(p) != 0 {
		t.Fatal("释放后应视为未运行")
	}
	if started, err := Keepalive(p, us); err != nil || !started || len(us.started) != 1 {
		t.Fatalf("未运行时 keepalive 应启动：%v %v %v", started, err, us.started)
	}
	_ = o
}

func TestCronHelpers(t *testing.T) {
	lines := cronLines("/home/a b/.local/bin/vpsmon-agent")
	if !strings.Contains(lines, "'/home/a b/.local/bin/vpsmon-agent' keepalive") {
		t.Fatalf("含空格的路径应加引号：%s", lines)
	}
	if got := withoutCron("x\n" + lines + "y\n"); got != "x\ny" {
		t.Fatalf("withoutCron = %q", got)
	}
}
