// vpsmon-agent：采集本机指标并上报到 vpsmon-server。
//
// 【安全】边界（设计 1.6.8、1.6.9）：
//   - 只采集与上报，不执行面板下发的任何命令
//   - 只走 HTTPS；仅回环地址或显式 --allow-http（本地开发）允许明文
//   - 以非 root 用户运行
//
// 子命令（设计 27.11）：
//
//	vpsmon-agent install --server URL --enroll ENR-…   注册并安装为系统服务（root）；非 root 时安装到当前用户（设计 27.13）
//	vpsmon-agent keepalive                              用户模式：Agent 未运行时在后台启动（crontab 调用）
//	vpsmon-agent status                                 服务状态与最近一次上报
//	vpsmon-agent uninstall                              停止并删除，通知面板（需要 root）
//	vpsmon-agent upgrade [--version vX]                 本机升级到官方签名的版本（需要 root，设计 29）
//	vpsmon-agent rotate-token --enroll ENR-…           用面板新生成的注册码更换 Token（需要 root，设计 17.2）
//	vpsmon-agent re-enroll --enroll ENR-… [--server URL] 换绑到其他节点或其他面板（需要 root，设计 27.11）
//	vpsmon-agent doctor                                 兼容性与连通性诊断，只读（设计 27.11）
//	vpsmon-agent enable-remote-upgrade                  为已安装的 Agent 启用远程升级（需要 root，设计 29.13）
//	vpsmon-agent refresh-unit                           把 systemd 单元更新为本版本内嵌的版本（需要 root，设计 43.5）
//	vpsmon-agent updater                                特权 updater，由 vpsmon-agent-updater.service 调用
//	vpsmon-agent [run] --server URL --token-file F      前台运行（systemd 单元使用）
//
// 远程升级（设计 29.13）：运行中的 Agent 只查询任务、校验并暂存官方签名的版本；替换由独立的 root updater 完成，
// updater 不联网、用内置公钥复验并拒绝降级。面板只能选择官方签名的版本，无法让节点执行任何其他内容。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"vpsmon/internal/agent/collector"
	"vpsmon/internal/agent/report"
	"vpsmon/internal/agent/sdnotify"
	"vpsmon/internal/agent/setup"
	"vpsmon/internal/agent/upgrade"
	"vpsmon/internal/protocol"
	"vpsmon/internal/release"
)

var version = "0.1.0-dev" // 构建时通过 -ldflags "-X main.version=..." 覆盖为 git describe

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			os.Exit(cmdInstall(os.Args[2:]))
		case "status":
			o := setup.Options{Out: os.Stdout}
			if p, user := setup.CurrentPaths(); user && setup.IsUserInstall(p) {
				o.Paths, o.UserSys = p, setup.RealUserSystem{}
			}
			if err := setup.PrintStatus(o); err != nil {
				os.Exit(1)
			}
			return
		case "uninstall":
			os.Exit(cmdUninstall())
		case "keepalive":
			os.Exit(cmdKeepalive())
		case "upgrade":
			os.Exit(cmdUpgrade(os.Args[2:]))
		case "updater":
			os.Exit(cmdUpdater())
		case "rotate-token":
			os.Exit(cmdRotateToken(os.Args[2:]))
		case "re-enroll":
			os.Exit(cmdReEnroll(os.Args[2:]))
		case "doctor":
			os.Exit(cmdDoctor())
		case "enable-remote-upgrade":
			os.Exit(cmdEnableRemoteUpgrade())
		case "refresh-unit":
			os.Exit(cmdRefreshUnit())
		case "version":
			fmt.Println(version)
			return
		case "run":
			os.Args = append(os.Args[:1], os.Args[2:]...)
		}
	}
	// 【兼容】不带子命令时直接按参数前台运行：已部署的 systemd 单元就是这样调用的
	run()
}

// cmdInstall 执行 vpsmon-agent install（设计 27.6.1），返回进程退出码。
func cmdInstall(args []string) int {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	server := fs.String("server", "", "panel URL, e.g. https://monitor.example.com")
	code := fs.String("enroll", "", "one-time enroll code from the panel (ENR-XXXX-XXXX-XXXX-XXXX)")
	allowHTTP := fs.Bool("allow-http", false, "allow plain HTTP to a non-loopback panel (development only)")
	noRemote := fs.Bool("no-remote-upgrade", false, "do not enable remote upgrades from the panel (design 29.13)")
	_ = fs.Parse(args)
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "✗ install 只支持 Linux")
		return 1
	}
	self, _ := os.Executable()
	o := setup.Options{Server: *server, EnrollCode: *code, AllowHTTP: *allowHTTP,
		NoRemoteUpgrade: *noRemote, Version: version, Self: self, Out: os.Stdout}
	var err error
	if p, user := setup.CurrentPaths(); user {
		// 非 root：安装到当前用户的家目录（设计 27.13）
		fmt.Println("· 非 root 用户：以用户模式安装到 ~/.local/bin，不创建系统服务（需要系统服务请用 sudo 执行）")
		o.Paths = p
		err = setup.InstallUser(context.Background(), o, setup.RealUserSystem{})
	} else {
		err = setup.Install(context.Background(), o)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗ "+err.Error())
		return 1
	}
	return 0
}

// cmdUpgrade 执行 vpsmon-agent upgrade（设计 29，本机升级）：只安装官方签名、版本更高的 vpsmon-agent。
func cmdUpgrade(args []string) int {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	target := fs.String("version", "", "target version, e.g. v0.3.0 (default: latest stable release)")
	allowDowngrade := fs.Bool("allow-downgrade", false, "allow installing an older signed version (local only, design 29.7.4)")
	mirror := fs.String("mirror", "", "download from a panel mirror instead of GitHub, e.g. https://monitor.example.com/releases")
	_ = fs.Parse(args)
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "✗ upgrade 只支持 Linux")
		return 1
	}
	if os.Geteuid() != 0 {
		// 用户模式（设计 27.13）：程序在自己的家目录下，不需要 root；同样只安装官方签名、版本更高的版本
		if p, user := setup.CurrentPaths(); user && setup.IsUserInstall(p) {
			us := setup.RealUserSystem{}
			o := upgrade.Options{Current: version, Target: *target, AllowDowngrade: *allowDowngrade, Mirror: *mirror,
				Keys: release.TrustedKeys(), Bin: p.Bin, StateDir: p.StateDir, WorkDir: filepath.Join(p.StateDir, "upgrade"),
				Out: os.Stdout, ReadStatus: readStatus(p), Restart: func() error { return setup.RestartUser(p, us) }}
			if err := upgrade.Run(context.Background(), o); err != nil {
				fmt.Fprintln(os.Stderr, "✗ "+err.Error())
				return 1
			}
			return 0
		}
		fmt.Fprintln(os.Stderr, "✗ 需要 root 权限：sudo vpsmon-agent upgrade")
		return 1
	}
	p := setup.DefaultPaths
	o := upgrade.Options{Current: version, Target: *target, AllowDowngrade: *allowDowngrade, Mirror: *mirror,
		Keys: release.TrustedKeys(), Bin: p.Bin, StateDir: p.StateDir, WorkDir: upgrade.RootWorkDir, Out: os.Stdout,
		ReadStatus: readStatus(p)}
	// 只有安装了服务（systemd 或 OpenRC）时才重启并做健康检查；否则替换后由使用者自行重启
	if setup.Installed(setup.Options{}) {
		o.Restart = restartService
	}
	if err := upgrade.Run(context.Background(), o); err != nil {
		fmt.Fprintln(os.Stderr, "✗ "+err.Error())
		return 1
	}
	// 本机升级由管理员发起，顺带刷新 updater 副本（远程升级不会替换 updater，设计 29.13）
	if err := setup.RefreshUpdater(setup.Options{}); err != nil {
		fmt.Fprintln(os.Stderr, "! 刷新 updater 失败："+err.Error())
	}
	// 服务文件内嵌在二进制中：由刚安装的新版本写入它自己的版本（如新增的 watchdog，设计 43.5）
	if setup.Installed(setup.Options{}) {
		cmd := exec.Command(p.Bin, "refresh-unit")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "! 更新 systemd 单元失败（可稍后执行 sudo vpsmon-agent refresh-unit）："+err.Error())
		}
	}
	return 0
}

// cmdRefreshUnit 执行 vpsmon-agent refresh-unit：把 systemd 单元更新为本版本内嵌的版本。
func cmdRefreshUnit() int {
	changed, err := setup.RefreshUnit(setup.Options{Out: os.Stdout})
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗ "+err.Error())
		return 1
	}
	if !changed {
		fmt.Println("✓ systemd 单元已是最新")
	}
	return 0
}

// cmdUpdater 是特权 updater（设计 29.13）：由 vpsmon-agent-updater.service 以 root 调用，处理一次暂存的升级请求。
func cmdUpdater() int {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "✗ updater 只能由 vpsmon-agent-updater.service 以 root 运行")
		return 1
	}
	p := setup.DefaultPaths
	err := upgrade.RunUpdater(context.Background(), upgrade.UpdaterOptions{
		StageDir: filepath.Join(p.StateDir, "update"), WorkDir: upgrade.RootWorkDir, StateDir: p.StateDir,
		Bin: p.Bin, DisableFile: p.NoRemoteUpgradeFile(), Keys: release.TrustedKeys(),
		Restart: restartService, ReadStatus: readStatus(p), Out: os.Stdout})
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗ "+err.Error())
		return 1
	}
	return 0
}

// cmdRotateToken 执行 vpsmon-agent rotate-token（设计 17.2）。
func cmdRotateToken(args []string) int {
	fs := flag.NewFlagSet("rotate-token", flag.ExitOnError)
	code := fs.String("enroll", "", "one-time enroll code generated for this node in the panel")
	allowHTTP := fs.Bool("allow-http", false, "allow plain HTTP to a non-loopback panel (development only)")
	_ = fs.Parse(args)
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "✗ rotate-token 只支持 Linux")
		return 1
	}
	err := setup.RotateToken(context.Background(), setup.Options{EnrollCode: *code, AllowHTTP: *allowHTTP,
		Version: version, Out: os.Stdout})
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗ "+err.Error())
		return 1
	}
	return 0
}

// cmdReEnroll 执行 vpsmon-agent re-enroll（设计 27.11）：换绑到同一面板的其他节点，或用 --server 换到另一个面板。
func cmdReEnroll(args []string) int {
	fs := flag.NewFlagSet("re-enroll", flag.ExitOnError)
	code := fs.String("enroll", "", "one-time enroll code of the target node")
	server := fs.String("server", "", "new panel URL (default: keep the current panel)")
	allowHTTP := fs.Bool("allow-http", false, "allow plain HTTP to a non-loopback panel (development only)")
	_ = fs.Parse(args)
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "✗ re-enroll 只支持 Linux")
		return 1
	}
	if err := setup.ReEnroll(context.Background(), setup.Options{Server: *server, EnrollCode: *code, AllowHTTP: *allowHTTP,
		Version: version, Out: os.Stdout}); err != nil {
		fmt.Fprintln(os.Stderr, "✗ "+err.Error())
		return 1
	}
	return 0
}

// cmdDoctor 执行 vpsmon-agent doctor（设计 27.11）：只读诊断，有问题时退出码为 1。
func cmdDoctor() int {
	o := setup.Options{Version: version, Out: os.Stdout}
	if p, user := setup.CurrentPaths(); user && setup.IsUserInstall(p) {
		o.Paths, o.UserSys = p, setup.RealUserSystem{}
	}
	if setup.Doctor(context.Background(), o) > 0 {
		return 1
	}
	return 0
}

func cmdEnableRemoteUpgrade() int {
	self, _ := os.Executable()
	if err := setup.EnableRemoteUpgrade(setup.Options{Self: self, Out: os.Stdout}); err != nil {
		fmt.Fprintln(os.Stderr, "✗ "+err.Error())
		return 1
	}
	return 0
}

func restartService() error { return setup.RestartInstalled(setup.Options{}) }

func readStatus(p setup.Paths) func() (string, int64, error) {
	return func() (string, int64, error) {
		st, err := setup.ReadStatus(filepath.Join(p.StateDir, "status.json"))
		if err != nil {
			return "", 0, err
		}
		return st.Version, st.LastSuccess, nil
	}
}

// remoteUpgradeEnabled：主机安装了 updater 且未禁止时才查询升级任务（设计 29.13）。
func remoteUpgradeEnabled() bool {
	p := setup.DefaultPaths
	if _, err := os.Stat(p.UpdaterPath); err != nil {
		return false
	}
	_, err := os.Stat(p.NoRemoteUpgradeFile())
	return errors.Is(err, os.ErrNotExist)
}

// pollUpgrades 定期查询升级任务：启动 30 秒后一次，之后每 5 分钟（设计 29.13）。在独立 goroutine 中运行，下载不阻塞上报。
func pollUpgrades(server, token, stateDir string) {
	o := upgrade.RemoteOptions{Server: server, Token: token, Current: version, Keys: release.TrustedKeys(),
		StageDir: filepath.Join(stateDir, "update"), Log: log.Printf}
	time.Sleep(30 * time.Second)
	for {
		checkUpgradeOnce(o)
		time.Sleep(5 * time.Minute)
	}
}

// checkUpgradeOnce 执行一次升级查询。后台 goroutine 中的 panic 会让整个进程退出、中断上报，
// 因此在这里捕获，下一轮照常查询（设计 43.5）。
func checkUpgradeOnce(o upgrade.RemoteOptions) {
	defer func() {
		if v := recover(); v != nil {
			log.Printf("upgrade check panicked: %v", v)
		}
	}()
	if !remoteUpgradeEnabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := upgrade.CheckRemote(ctx, o); err != nil {
		log.Printf("upgrade check: %v", err)
	}
}

// cmdUninstall 执行 vpsmon-agent uninstall（设计 27.11），返回进程退出码。
func cmdUninstall() int {
	if p, user := setup.CurrentPaths(); user && setup.IsUserInstall(p) {
		if err := setup.UninstallUser(context.Background(), setup.Options{Paths: p, Out: os.Stdout}, setup.RealUserSystem{}); err != nil {
			fmt.Fprintln(os.Stderr, "✗ "+err.Error())
			return 1
		}
		return 0
	}
	if err := setup.Uninstall(context.Background(), setup.Options{Out: os.Stdout}); err != nil {
		fmt.Fprintln(os.Stderr, "✗ "+err.Error())
		return 1
	}
	return 0
}

// cmdKeepalive 执行 vpsmon-agent keepalive（用户模式，设计 27.13）：Agent 未运行时在后台启动它。
// 由 crontab 每 2 分钟与开机时调用；已在运行时什么也不做。
func cmdKeepalive() int {
	p, user := setup.CurrentPaths()
	if !user {
		fmt.Fprintln(os.Stderr, "✗ keepalive 用于非 root 的用户模式安装；root 安装由 systemd / OpenRC 保活")
		return 1
	}
	started, err := setup.Keepalive(p, setup.RealUserSystem{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗ "+err.Error())
		return 1
	}
	if started {
		fmt.Println("✓ 已在后台启动 Agent")
	}
	return 0
}

// firstReportDelay：预采样与首次上报之间的间隔，足以得到有意义的 CPU 使用率与网速。
const firstReportDelay = time.Second

// stallLimit：主循环超过这么久没有转动视为卡死。主循环每轮最长约为采样间隔上限（60 秒）
// 加一次发送的时间预算与请求超时；取 3 分钟，systemd 下 WatchdogSec=120 会先生效。
const stallLimit = 3 * time.Minute

// stallGuard 每 15 秒检查一次主循环；卡住超过 limit 时记录原因并退出，由 systemd / supervise-daemon 重启（设计 43.5）。
// 未发出的上报已按设计 1.6.14 定期落盘，重启后继续补发。
func stallGuard(last *atomic.Int64, limit time.Duration) {
	for range time.Tick(15 * time.Second) {
		if d := time.Since(time.Unix(0, last.Load())); d > limit {
			log.Printf("ERROR main loop stalled for %s; exiting so the service manager restarts the agent", d.Round(time.Second))
			os.Exit(2)
		}
	}
}

// agentMemoryLimit 是 Go 运行时的软内存上限（只约束堆与运行时内存，不含程序代码段）。
const agentMemoryLimit = 20 << 20

// run 前台运行：定时采集并上报，直到收到 SIGTERM / SIGINT。
func run() {
	server := flag.String("server", "", "server base URL, e.g. https://monitor.example.com")
	token := flag.String("token", "", "agent token (prefer --token-file or MONITOR_AGENT_TOKEN)")
	tokenFile := flag.String("token-file", "", "file containing the agent token")
	envFile := flag.String("env-file", "", "read VPSMON_SERVER from this KEY=VALUE file when --server is empty (OpenRC service, design 28)")
	interval := flag.Duration("interval", 10*time.Second, "report interval")
	fake := flag.Bool("fake", false, "send fake metrics (for development)")
	allowHTTP := flag.Bool("allow-http", false, "allow plain HTTP to a non-loopback server (development only)")
	ifaces := flag.String("interfaces", "", "comma-separated interfaces to count; empty = auto")
	stateDir := flag.String("state-dir", "", "directory for status.json read by `vpsmon-agent status` (systemd: /var/lib/vpsmon-agent)")
	lockFile := flag.String("lock-file", "", "hold an exclusive lock on this file while running; exit if another instance holds it (user mode, design 27.13)")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	// 软内存上限（设计 4.2：常驻 10～30 MB）。平时堆只有几 MB；断网积压后补发等短时高峰过后，
	// 接近上限时 GC 会更积极地回收并归还内存，常驻内存不会停在高峰值。可用 GOMEMLIMIT 环境变量覆盖。
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(agentMemoryLimit)
	}
	// 采集与上报都是串行的，一个 P 足够：少建线程、少占每个 P 的缓存（设计 4.2）。
	// 阻塞在系统调用中的 goroutine（如 statfs）会让出 P，不影响主循环。可用 GOMAXPROCS 环境变量覆盖。
	if os.Getenv("GOMAXPROCS") == "" {
		runtime.GOMAXPROCS(1)
	}

	// 用户模式由 crontab 保活（设计 27.13）：运行期间持有锁，保证只有一个实例；已有实例时安静退出
	if *lockFile != "" {
		release, ok, err := setup.AcquireRunLock(*lockFile)
		if err != nil {
			log.Fatalf("lock %s: %v", *lockFile, err)
		}
		if !ok {
			log.Printf("another vpsmon-agent is already running (%s); exiting", *lockFile)
			return
		}
		defer release()
	}

	// OpenRC 服务脚本的参数是固定的，面板地址由这里从 env 文件读取，而不是由 shell source（设计 28）
	if *server == "" && *envFile != "" {
		*server = setup.ReadEnv(*envFile)["VPSMON_SERVER"]
	}

	// 配置错误立即退出（设计 43.5）：启动后静默不上报，比让 systemd 显示失败更难发现。
	tok, err := loadToken(*token, *tokenFile)
	if err != nil {
		log.Fatal(err)
	}
	if err := setup.ValidateServerURL(*server, *allowHTTP); err != nil {
		log.Fatal(err)
	}

	// --fake 在 Linux 上也强制使用假数据；macOS 上 collector.New 会自动退回假数据，不需要虚拟机即可联调。
	var col collector.Collector
	if *fake {
		col = collector.NewFake()
	} else {
		opts := collector.Options{}
		if *ifaces != "" {
			opts.Interfaces = strings.Split(*ifaces, ",")
		}
		col = collector.New(opts)
	}

	r := &report.Reporter{
		Endpoint: strings.TrimRight(*server, "/") + "/api/v1/agent/report",
		Token:    tok,
		// 【安全】使用默认 Transport：系统 CA、始终校验证书（设计 23.1）。
		// 10 秒超时：面板卡住时不让上报循环一直阻塞。
		Client: &http.Client{Timeout: 10 * time.Second},
	}
	// 断网缓冲落盘：Agent 重启后继续补发，主机重启前的最后一份计数不丢（设计 1.6.14、5.5）
	if *stateDir != "" {
		r.StatePath = filepath.Join(*stateDir, "queue.json")
		if err := r.Load(); err != nil {
			log.Printf("discarded unreadable report queue: %v", err)
		} else if q := r.Status().Queued; q > 0 {
			log.Printf("restored %d unsent reports from the previous run", q)
		}
	}
	// 【安全】只记录上报地址，不记录 Token（设计 24.7）。
	log.Printf("vpsmon-agent %s → %s every %s", version, *server, *interval)

	if *stateDir != "" && runtime.GOOS == "linux" {
		go pollUpgrades(*server, tok, *stateDir)
	}

	// CPU 使用率与网速都是两次采样的差值（设计 5.2）。先丢弃一次采样，第一次正式上报就有网速，而不是 0。
	_, _ = collect(col, false)

	// systemd 存活检测（设计 43.5）：Type=notify 启动后报告就绪；主循环每轮喂一次看门狗，
	// 主循环卡住（而不是退出）超过 WatchdogSec 时由 systemd 重启。不在 systemd 下运行时为空操作。
	if _, err := sdnotify.Notify("READY=1"); err != nil {
		log.Printf("sd_notify: %v", err)
	}
	if wd := sdnotify.WatchdogInterval(); wd > 0 && wd < 2**interval {
		log.Printf("WARN systemd WatchdogSec (%s) is shorter than two report intervals (%s); the agent may be restarted spuriously", wd, *interval)
	}
	// 主循环卡死自检：OpenRC 等没有 systemd watchdog 的环境，卡住时由 Agent 自行退出，交给服务管理器重启（设计 43.5）
	var lastLoop atomic.Int64
	lastLoop.Store(time.Now().UnixNano())
	go stallGuard(&lastLoop, stallLimit)
	alive := func() {
		lastLoop.Store(time.Now().UnixNano())
		_, _ = sdnotify.Notify("WATCHDOG=1")
	}

	// systemctl stop 发送 SIGTERM，Ctrl-C 发送 SIGINT，两者都触发下面的补报。
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	tick := time.NewTicker(*interval)
	defer tick.Stop()
	// retry：上报失败后按退避时间（1 秒起）重试，不必等到下一个采集周期（设计 43.5）
	retry := time.NewTimer(time.Hour)
	retry.Stop()

	writeStatus := func() {
		st := r.Status()
		setup.WriteStatus(*stateDir, setup.Status{Version: version, Server: *server, LastAttempt: st.LastAttempt,
			LastSuccess: st.LastSuccess, LastError: st.LastError, Queued: st.Queued, ClockSkew: st.ClockSkew})
	}
	// save 按需落盘；写入失败（状态目录不可写）只在原因变化时记录一次，避免刷屏
	var saveErr string
	save := func(force bool) {
		err := r.Save(force)
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		if msg != "" && msg != saveErr {
			log.Printf("save report queue: %s", msg)
		}
		saveErr = msg
	}
	// flush 发送缓存的上报，并按需要安排下一次重试
	flush := func(ctx context.Context, force bool) {
		r.Flush(ctx, force)
		save(false)
		writeStatus() // 供 vpsmon-agent status 显示最近一次上报（设计 24.5）
		if next := r.NextAttempt(); !next.IsZero() {
			retry.Reset(time.Until(next))
		}
	}

	// 采样间隔：启动时用 --interval，之后以面板按节点下发的为准（设计 4.2、6.1）
	current := *interval
	applyInterval := func() {
		if iv := r.Interval(); iv > 0 && iv != current {
			log.Printf("report interval changed by the panel: %s → %s", current, iv)
			current = iv
			tick.Reset(iv)
		}
	}

	// 首次上报不等满一个采样间隔：预采样 1 秒后即可算出 CPU 与网速，安装或重启后节点尽快显示在线，
	// install 也更快确认首次上报（设计 27.6.1）。之后按定时器上报。
	first := time.NewTimer(firstReportDelay)
	defer first.Stop()

	// 上报同步执行：面板变慢时推迟下一次，而不是堆积并发请求（延迟受 http.Client 超时约束）。
	for {
		select {
		case <-first.C:
			if rep, ok := collect(col, false); ok {
				r.Enqueue(rep)
			}
			flush(context.Background(), false)
			applyInterval()
			alive()
		case <-tick.C:
			if rep, ok := collect(col, false); ok {
				r.Enqueue(rep)
			}
			flush(context.Background(), false)
			applyInterval()
			alive()
		case <-retry.C:
			flush(context.Background(), false)
			alive()
		case <-sig:
			_, _ = sdnotify.Notify("STOPPING=1")
			// 退出前补报，避免最后一个周期到停止之间的流量丢失（设计 5.5）。忽略退避，立即尝试。
			// 2 秒超时：停止过久会被 systemd 强制 SIGKILL（单元中 TimeoutStopSec=5）。
			if rep, ok := collect(col, true); ok {
				r.Enqueue(rep)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			flush(ctx, true)
			cancel()
			save(true) // 未送达的（含本次补报）落盘，下次启动继续补发
			if q := r.Status().Queued; q > 0 && r.StatePath != "" && saveErr == "" {
				log.Printf("stopped with %d unsent reports (saved, will be sent after restart)", q)
			} else if q > 0 {
				log.Printf("stopped with %d unsent reports (traffic counters are cumulative and will catch up)", q)
			} else {
				log.Println("stopped")
			}
			return
		}
	}
}

// collect 采集一份上报并填写时间戳与版本。采集失败只记录，不影响下一个周期。
// Timestamp 是采集时间：断网后补发时，面板按它把数据放回正确的位置（设计 1.6.14）。
// 采集项的 panic 已在采集器内按项捕获；这里再兜底一层，任何意外都不能让 Agent 退出、停止上报（设计 43.5）。
func collect(col collector.Collector, final bool) (rep protocol.Report, ok bool) {
	defer func() {
		if v := recover(); v != nil {
			log.Printf("collect panicked: %v", v)
			rep, ok = protocol.Report{}, false
		}
	}()
	rep, err := col.Collect()
	if err != nil {
		log.Printf("collect: %v", err)
		return rep, false
	}
	rep.Timestamp = time.Now().Unix()
	rep.AgentVersion = version
	rep.Final = final
	// 告诉面板本机是否启用了远程升级，未启用时面板创建任务会直接说明原因（设计 29.13）；只在 Linux 上有意义
	if runtime.GOOS == "linux" {
		on := remoteUpgradeEnabled()
		rep.System.RemoteUpgrade = &on
	}
	return rep, true
}

// loadToken 按优先级读取 Agent Token：--token、--token-file、环境变量 MONITOR_AGENT_TOKEN。
//
// 【安全】--token 会出现在 ps 输出中，本机所有用户都能看到，因此 systemd 单元使用
// --token-file（仅 vpsmon-agent 组可读）；--token 只用于临时手动测试（设计 27.1）。
func loadToken(flagTok, file string) (string, error) {
	switch {
	case flagTok != "":
		return flagTok, nil
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		// Token 文件通常由 “> file” 或编辑器写入，末尾带换行，需要去掉。
		return strings.TrimSpace(string(b)), nil
	case os.Getenv("MONITOR_AGENT_TOKEN") != "":
		return os.Getenv("MONITOR_AGENT_TOKEN"), nil
	}
	return "", errors.New("no agent token: use --token-file or MONITOR_AGENT_TOKEN")
}
