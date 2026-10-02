// vpsmon-agent：采集本机指标并上报到 vpsmon-server。
//
// 【安全】边界（设计 1.6.8、1.6.9）：
//   - 只采集与上报，不执行面板下发的任何命令
//   - 只走 HTTPS；仅回环地址或显式 --allow-http（本地开发）允许明文
//   - 以非 root 用户运行
//
// 子命令（设计 27.11）：
//
//	vpsmon-agent install --server URL --enroll ENR-…   注册并安装为 systemd 服务（需要 root）
//	vpsmon-agent status                                 服务状态与最近一次上报
//	vpsmon-agent uninstall                              停止并删除，通知面板（需要 root）
//	vpsmon-agent [run] --server URL --token-file F      前台运行（systemd 单元使用）
//
// 不负责：升级（设计 29）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"vpsmon/internal/agent/collector"
	"vpsmon/internal/agent/report"
	"vpsmon/internal/agent/setup"
	"vpsmon/internal/protocol"
)

var version = "0.1.0-dev" // 构建时通过 -ldflags "-X main.version=..." 覆盖为 git describe

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			os.Exit(cmdInstall(os.Args[2:]))
		case "status":
			if err := setup.PrintStatus(setup.Options{Out: os.Stdout}); err != nil {
				os.Exit(1)
			}
			return
		case "uninstall":
			os.Exit(cmdUninstall())
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
	_ = fs.Parse(args)
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "✗ install 只支持 Linux")
		return 1
	}
	self, _ := os.Executable()
	err := setup.Install(context.Background(), setup.Options{Server: *server, EnrollCode: *code, AllowHTTP: *allowHTTP,
		Version: version, Self: self, Out: os.Stdout})
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗ "+err.Error())
		return 1
	}
	return 0
}

// cmdUninstall 执行 vpsmon-agent uninstall（设计 27.11），返回进程退出码。
func cmdUninstall() int {
	if err := setup.Uninstall(context.Background(), setup.Options{Out: os.Stdout}); err != nil {
		fmt.Fprintln(os.Stderr, "✗ "+err.Error())
		return 1
	}
	return 0
}

// run 前台运行：定时采集并上报，直到收到 SIGTERM / SIGINT。
func run() {
	server := flag.String("server", "", "server base URL, e.g. https://monitor.example.com")
	token := flag.String("token", "", "agent token (prefer --token-file or MONITOR_AGENT_TOKEN)")
	tokenFile := flag.String("token-file", "", "file containing the agent token")
	interval := flag.Duration("interval", 10*time.Second, "report interval")
	fake := flag.Bool("fake", false, "send fake metrics (for development)")
	allowHTTP := flag.Bool("allow-http", false, "allow plain HTTP to a non-loopback server (development only)")
	ifaces := flag.String("interfaces", "", "comma-separated interfaces to count; empty = auto")
	stateDir := flag.String("state-dir", "", "directory for status.json read by `vpsmon-agent status` (systemd: /var/lib/vpsmon-agent)")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
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
	// 【安全】只记录上报地址，不记录 Token（设计 24.7）。
	log.Printf("vpsmon-agent %s → %s every %s", version, *server, *interval)

	// CPU 使用率与网速都是两次采样的差值（设计 5.2）。先丢弃一次采样，第一次正式上报就有网速，而不是 0。
	_, _ = col.Collect()

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
			LastSuccess: st.LastSuccess, LastError: st.LastError, Queued: st.Queued})
	}
	// flush 发送缓存的上报，并按需要安排下一次重试
	flush := func(ctx context.Context, force bool) {
		r.Flush(ctx, force)
		writeStatus() // 供 vpsmon-agent status 显示最近一次上报（设计 24.5）
		if next := r.NextAttempt(); !next.IsZero() {
			retry.Reset(time.Until(next))
		}
	}

	// 上报同步执行：面板变慢时推迟下一次，而不是堆积并发请求（延迟受 http.Client 超时约束）。
	for {
		select {
		case <-tick.C:
			if rep, ok := collect(col, false); ok {
				r.Enqueue(rep)
			}
			flush(context.Background(), false)
		case <-retry.C:
			flush(context.Background(), false)
		case <-sig:
			// 退出前补报，避免最后一个周期到停止之间的流量丢失（设计 5.5）。忽略退避，立即尝试。
			// 2 秒超时：停止过久会被 systemd 强制 SIGKILL（单元中 TimeoutStopSec=5）。
			if rep, ok := collect(col, true); ok {
				r.Enqueue(rep)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			flush(ctx, true)
			cancel()
			if q := r.Status().Queued; q > 0 {
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
func collect(col collector.Collector, final bool) (protocol.Report, bool) {
	rep, err := col.Collect()
	if err != nil {
		log.Printf("collect: %v", err)
		return rep, false
	}
	rep.Timestamp = time.Now().Unix()
	rep.AgentVersion = version
	rep.Final = final
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
