package setup

// 非 root 安装（用户模式，设计 27.13）：没有 root 权限的主机（共享主机、受限账号、容器内的普通用户）
// 也能安装、运行与保活 Agent。全部文件在当前用户的家目录下，不创建系统用户、不写 /etc：
//
//	程序   ~/.local/bin/vpsmon-agent
//	配置   ~/.config/vpsmon-agent/{token,env}（目录 0700，文件 0600）
//	状态   ~/.local/state/vpsmon-agent/{status.json,queue.json,agent.lock,agent.log}
//
// 保活方式按顺序自动选择：
//  1. systemd 用户服务（~/.config/systemd/user/vpsmon-agent.service）：只在用户已开启 linger 时使用——
//     未开启时用户退出登录后服务即停止、开机也不会启动
//  2. crontab：@reboot 与每 2 分钟一次的 vpsmon-agent keepalive；keepalive 发现 Agent 未运行时在后台启动它，
//     Agent 运行期间持有 agent.lock（flock），保证只有一个实例
//  3. 两者都不可用：立即在后台启动一次，并说明重启后需手动再次启动
//
// 【安全】与 root 安装相同：安装脚本仍是 下载 → 校验 → 执行（设计 27.5）；只走 HTTPS；Token 只写入 0600 文件，
// 不出现在命令行或 crontab 中（设计 27.1）。用户模式不启用远程升级（没有特权 updater）：升级在本机执行
// vpsmon-agent upgrade。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// UserPaths 返回用户模式在 home 下的文件布局。
func UserPaths(home string) Paths {
	return Paths{
		Bin:      filepath.Join(home, ".local", "bin", "vpsmon-agent"),
		ConfDir:  filepath.Join(home, ".config", "vpsmon-agent"),
		StateDir: filepath.Join(home, ".local", "state", "vpsmon-agent"),
		Unit:     filepath.Join(home, ".config", "systemd", "user", "vpsmon-agent.service"),
	}
}

func (p Paths) lockFile() string { return filepath.Join(p.StateDir, "agent.lock") }
func (p Paths) logFile() string  { return filepath.Join(p.StateDir, "agent.log") }

// IsUserInstall 判断 p（用户模式路径）下是否已安装：家目录下有 token。
func IsUserInstall(p Paths) bool {
	_, err := os.Stat(p.tokenFile())
	return err == nil
}

// CurrentPaths 返回当前用户应使用的路径：root 用系统路径；普通用户用家目录下的用户模式路径。
func CurrentPaths() (Paths, bool) {
	if os.Geteuid() == 0 {
		return DefaultPaths, false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return DefaultPaths, false
	}
	return UserPaths(home), true
}

// keepMethod 是用户模式的保活方式。
type keepMethod string

const (
	keepSystemdUser keepMethod = "systemd-user"
	keepCron        keepMethod = "cron"
	keepNone        keepMethod = "none"
)

// UserSystem 抽象用户模式对主机的操作，便于测试。
type UserSystem interface {
	// SystemdUser 判断 systemctl --user 可用，且当前用户已开启 linger（退出登录后用户服务仍运行、开机启动）
	SystemdUser() (available, linger bool)
	// Crontab 读取当前用户的 crontab；ok 为 false 表示没有 crontab 命令或不允许使用
	Crontab() (content string, ok bool)
	SetCrontab(content string) error
	Run(name string, args ...string) (string, error)
	// StartDetached 在新会话中后台启动程序，标准输出与错误追加到 logPath
	StartDetached(bin string, args []string, logPath string) error
}

const cronMarker = "# vpsmon-agent keepalive（设计 27.13）"

// cronLines 返回保活的 crontab 行：开机启动 + 每 2 分钟检查一次。
func cronLines(bin string) string {
	q := shellQuote(bin)
	return "@reboot " + q + " keepalive >/dev/null 2>&1 " + cronMarker + "\n" +
		"*/2 * * * * " + q + " keepalive >/dev/null 2>&1 " + cronMarker + "\n"
}

// withoutCron 去掉本程序写入的 crontab 行，保留用户自己的内容。
func withoutCron(content string) string {
	var keep []string
	for _, l := range strings.Split(content, "\n") {
		if !strings.Contains(l, cronMarker) {
			keep = append(keep, l)
		}
	}
	return strings.TrimRight(strings.Join(keep, "\n"), "\n")
}

func shellQuote(s string) string {
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r == '/' || r == '.' || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runArgs 是用户模式下 Agent 前台运行的参数（systemd 用户单元与 keepalive 共用）。
func runArgs(p Paths, lock bool) []string {
	a := []string{"run", "--env-file", p.envFile(), "--token-file", p.tokenFile(), "--state-dir", p.StateDir}
	if lock {
		a = append(a, "--lock-file", p.lockFile())
	}
	return a
}

// userUnit 是用户模式的 systemd 单元。用户单元不能使用需要特权的加固选项，只保留不需要特权的部分。
func userUnit(p Paths) string {
	args := make([]string, 0, 8)
	for _, a := range append([]string{p.Bin}, runArgs(p, false)...) {
		args = append(args, shellQuote(a))
	}
	return `# 由 vpsmon-agent install 生成（用户模式，设计 27.13）
[Unit]
Description=vpsmon-agent (user mode)
After=network-online.target
StartLimitIntervalSec=0

[Service]
Type=notify
ExecStart=` + strings.Join(args, " ") + `
Restart=always
RestartSec=5
WatchdogSec=120
NoNewPrivileges=yes

[Install]
WantedBy=default.target
`
}

func pickKeepMethod(us UserSystem) keepMethod {
	if ok, linger := us.SystemdUser(); ok && linger {
		return keepSystemdUser
	}
	if _, ok := us.Crontab(); ok {
		return keepCron
	}
	return keepNone
}

// InstallUser 执行用户模式的 vpsmon-agent install --user（设计 27.13）。流程与 root 安装相同：
// 先预检与注册，再修改本机文件；注册之后的步骤失败时回滚。
func InstallUser(ctx context.Context, o Options, us UserSystem) error {
	o.defaults()
	p := o.Paths
	say := func(format string, a ...any) { fmt.Fprintf(o.Out, format+"\n", a...) }

	// 1. 预检
	o.Server = strings.TrimRight(strings.TrimSpace(o.Server), "/")
	if err := ValidateServerURL(o.Server, o.AllowHTTP); err != nil {
		return err
	}
	code := strings.ToUpper(strings.TrimSpace(o.EnrollCode))
	if !enrollCodePattern.MatchString(code) {
		return errors.New("注册码格式不正确，应为 ENR-XXXX-XXXX-XXXX-XXXX，请从面板复制完整命令")
	}
	if _, err := os.Stat(p.tokenFile()); err == nil {
		return fmt.Errorf("本用户已安装 Agent（%s 已存在）。重装请先执行 vpsmon-agent uninstall", p.tokenFile())
	}
	method := pickKeepMethod(us)

	// 2. 注册
	host := readHostInfo(o.HostInfoRoot)
	say("✓ 系统 %s %s · linux/%s · 非 root（用户模式）", orUnknown(host.OS), host.OSVersion, host.Arch)
	res, err := enroll(ctx, o.HTTP, o.Server, enrollRequest{EnrollCode: code, Hostname: host.Hostname,
		MachineIDHash: host.MachineIDHash, OS: host.OS, OSVersion: host.OSVersion, Arch: host.Arch, AgentVersion: o.Version})
	if err != nil {
		return err
	}
	say("✓ 已注册到 %s ，节点：%s", o.Server, res.ServerName)
	for _, w := range res.Warnings {
		say("  ! %s", w)
	}

	// 3. 写入文件；失败时回滚
	var undo []func()
	rollback := func(cause error) error {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		return fmt.Errorf("%w\n已回滚本次安装。注册码在 10 分钟内仍可在本机重试同一命令", cause)
	}
	for _, d := range []string{p.ConfDir, p.StateDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return rollback(err)
		}
		d := d
		undo = append(undo, func() { os.RemoveAll(d) })
	}
	// 【安全】Token 只有当前用户可读（0600），不出现在命令行或 crontab 中（设计 27.1）
	if err := os.WriteFile(p.tokenFile(), []byte(res.AgentToken+"\n"), 0o600); err != nil {
		return rollback(err)
	}
	env := fmt.Sprintf("# 由 vpsmon-agent install --user 生成（设计 27.13）\nVPSMON_SERVER=%s\nVPSMON_SERVER_ID=%d\nVPSMON_NODE_NAME=%s\n",
		o.Server, res.ServerID, shellSafe(res.ServerName))
	if err := os.WriteFile(p.envFile(), []byte(env), 0o600); err != nil {
		return rollback(err)
	}
	if o.Self != "" && o.Self != p.Bin {
		if err := os.MkdirAll(filepath.Dir(p.Bin), 0o755); err != nil {
			return rollback(err)
		}
		if err := copyFile(o.Self, p.Bin, 0o755); err != nil {
			return rollback(fmt.Errorf("安装二进制到 %s 失败：%w", p.Bin, err))
		}
		undo = append(undo, func() { os.Remove(p.Bin) })
	}
	say("✓ 已安装 %s", p.Bin)

	started := time.Now()
	switch method {
	case keepSystemdUser:
		if err := os.MkdirAll(filepath.Dir(p.Unit), 0o755); err != nil {
			return rollback(err)
		}
		if err := os.WriteFile(p.Unit, []byte(userUnit(p)), 0o644); err != nil {
			return rollback(err)
		}
		undo = append(undo, func() {
			us.Run("systemctl", "--user", "disable", "--now", serviceName)
			os.Remove(p.Unit)
			us.Run("systemctl", "--user", "daemon-reload")
		})
		us.Run("systemctl", "--user", "daemon-reload")
		if out, err := us.Run("systemctl", "--user", "enable", "--now", serviceName); err != nil {
			return rollback(fmt.Errorf("启动用户服务失败：%v %s", err, out))
		}
		say("✓ 保活：systemd 用户服务（已开启 linger，退出登录与重启后仍运行）")
	case keepCron:
		cur, _ := us.Crontab()
		next := withoutCron(cur)
		if next != "" {
			next += "\n"
		}
		if err := us.SetCrontab(next + cronLines(p.Bin)); err != nil {
			return rollback(fmt.Errorf("写入 crontab 失败：%w", err))
		}
		undo = append(undo, func() { us.SetCrontab(withoutCron(cur) + "\n") })
		if err := us.StartDetached(p.Bin, runArgs(p, true), p.logFile()); err != nil {
			return rollback(fmt.Errorf("启动 Agent 失败：%w", err))
		}
		say("✓ 保活：crontab（开机启动，每 2 分钟检查一次，停止后自动拉起）")
		if ok, _ := us.SystemdUser(); ok {
			say("  提示：如需 systemd 用户服务，请让管理员执行 sudo loginctl enable-linger %s 后重装", currentUser())
		}
	default:
		if err := us.StartDetached(p.Bin, runArgs(p, true), p.logFile()); err != nil {
			return rollback(fmt.Errorf("启动 Agent 失败：%w", err))
		}
		say("! 本机没有可用的 crontab 或 systemd 用户服务：Agent 已在后台运行，但重启后需手动执行")
		say("    %s keepalive", p.Bin)
	}

	// 4. 等待首次上报
	if st, ok := waitFirstReport(p.statusFile(), started, o.WaitFirst); ok {
		say("✓ Agent 已启动，首次上报成功")
	} else if st != nil && st.LastError != "" {
		say("! Agent 已启动，但上报失败：%s", st.LastError)
		say("  查看日志：%s", userLogHint(method, p))
	} else {
		say("! Agent 已启动，尚未确认首次上报；稍后用 vpsmon-agent status 查看")
	}
	say("")
	say("查看状态：vpsmon-agent status")
	say("升级：    vpsmon-agent upgrade（用户模式不支持从面板远程升级）")
	say("卸载：    vpsmon-agent uninstall")
	return nil
}

func userLogHint(m keepMethod, p Paths) string {
	if m == keepSystemdUser {
		return "journalctl --user -u " + serviceName + " -n 50"
	}
	return "tail -n 50 " + p.logFile()
}

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return strconv.Itoa(os.Getuid())
}

// UserKeepMethod 返回已安装的用户模式所用的保活方式。
func UserKeepMethod(p Paths, us UserSystem) keepMethod {
	if _, err := os.Stat(p.Unit); err == nil {
		return keepSystemdUser
	}
	if c, ok := us.Crontab(); ok && strings.Contains(c, cronMarker) {
		return keepCron
	}
	return keepNone
}

// runningPID 返回持有 agent.lock 的 Agent 进程号；没有运行时为 0。
func runningPID(p Paths) int {
	f, err := os.OpenFile(p.lockFile(), os.O_RDWR, 0)
	if err != nil {
		return 0
	}
	defer f.Close()
	// 能拿到锁说明没有进程持有它
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return 0
	}
	b := make([]byte, 32)
	n, _ := f.ReadAt(b, 0)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b[:n])))
	return pid
}

// AcquireRunLock 在 Agent 运行期间持有 lockPath 的排他锁并写入进程号；已有实例在运行时返回 ok=false。
// 进程退出（包括崩溃）时锁由内核自动释放。
func AcquireRunLock(lockPath string) (release func(), ok bool, err error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	f.Truncate(0)
	f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, true, nil
}

const maxUserLog = 5 << 20 // agent.log 超过 5 MB 时轮转为 agent.log.1

// Keepalive 执行 vpsmon-agent keepalive：Agent 未运行时在后台启动它（crontab 每 2 分钟调用一次）。
func Keepalive(p Paths, us UserSystem) (started bool, err error) {
	if _, err := os.Stat(p.tokenFile()); err != nil {
		return false, fmt.Errorf("未找到用户模式安装（%s）", p.tokenFile())
	}
	if runningPID(p) != 0 {
		return false, nil
	}
	if fi, err := os.Stat(p.logFile()); err == nil && fi.Size() > maxUserLog {
		os.Rename(p.logFile(), p.logFile()+".1")
	}
	return true, us.StartDetached(p.Bin, runArgs(p, true), p.logFile())
}

// RestartUser 重启用户模式的 Agent（本机升级后使用）。
func RestartUser(p Paths, us UserSystem) error {
	switch UserKeepMethod(p, us) {
	case keepSystemdUser:
		if out, err := us.Run("systemctl", "--user", "restart", serviceName); err != nil {
			return fmt.Errorf("systemctl --user restart 失败：%v %s", err, out)
		}
		return nil
	default:
		stopUserAgent(p)
		_, err := Keepalive(p, us)
		return err
	}
}

// stopUserAgent 结束持有锁的 Agent 进程，最多等待 10 秒。
func stopUserAgent(p Paths) {
	pid := runningPID(p)
	if pid <= 0 {
		return
	}
	syscall.Kill(pid, syscall.SIGTERM)
	for i := 0; i < 100 && runningPID(p) != 0; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if runningPID(p) != 0 {
		syscall.Kill(pid, syscall.SIGKILL)
	}
}

// UninstallUser 执行用户模式的 vpsmon-agent uninstall：停止、去掉保活、通知面板、删除文件。
func UninstallUser(ctx context.Context, o Options, us UserSystem) error {
	o.defaults()
	p := o.Paths
	say := func(format string, a ...any) { fmt.Fprintf(o.Out, format+"\n", a...) }
	switch UserKeepMethod(p, us) {
	case keepSystemdUser:
		us.Run("systemctl", "--user", "disable", "--now", serviceName)
		os.Remove(p.Unit)
		us.Run("systemctl", "--user", "daemon-reload")
	case keepCron:
		if c, ok := us.Crontab(); ok {
			us.SetCrontab(withoutCron(c) + "\n")
		}
	}
	stopUserAgent(p)
	say("✓ 已停止 Agent 并去掉保活")

	env := readEnv(p.envFile())
	if tok, err := os.ReadFile(p.tokenFile()); err == nil && env["VPSMON_SERVER"] != "" {
		if err := unregister(ctx, o.HTTP, env["VPSMON_SERVER"], strings.TrimSpace(string(tok))); err != nil {
			say("! 未能通知面板（%v），请在面板中手动处理该节点", err)
		} else {
			say("✓ 已通知面板，节点回到“待安装”")
		}
	}
	os.Remove(p.Bin)
	for _, d := range []string{p.ConfDir, p.StateDir} {
		if err := os.RemoveAll(d); err != nil {
			say("! 删除 %s 失败：%v", d, err)
		}
	}
	say("✓ 已删除程序与配置")
	return nil
}

// RealUserSystem 是真实主机上的 UserSystem。
type RealUserSystem struct{}

func (RealUserSystem) SystemdUser() (bool, bool) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false, false
	}
	if err := exec.Command("systemctl", "--user", "show-environment").Run(); err != nil {
		return false, false
	}
	out, err := exec.Command("loginctl", "show-user", strconv.Itoa(os.Getuid()), "-p", "Linger", "--value").Output()
	return true, err == nil && strings.TrimSpace(string(out)) == "yes"
}

func (RealUserSystem) Crontab() (string, bool) {
	if _, err := exec.LookPath("crontab"); err != nil {
		return "", false
	}
	out, err := exec.Command("crontab", "-l").CombinedOutput()
	if err != nil {
		// 没有 crontab 时 crontab -l 也会返回非 0（“no crontab for user”），这种情况仍可写入
		if strings.Contains(strings.ToLower(string(out)), "no crontab") {
			return "", true
		}
		return "", false
	}
	return string(out), true
}

func (RealUserSystem) SetCrontab(content string) error {
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v %s", err, out)
	}
	return nil
}

func (RealUserSystem) Run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

func (RealUserSystem) StartDetached(bin string, args []string, logPath string) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // 脱离当前会话：SSH 断开或 cron 结束后继续运行
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
