// vpsmon-agent：采集本机指标并上报到 vpsmon-server。
//
// 【安全】边界（设计 1.6.8、1.6.9）：
//   - 只采集与上报，不执行面板下发的任何命令
//   - 只走 HTTPS；仅回环地址或显式 --allow-http（本地开发）允许明文
//   - 以非 root 用户运行
//
// 不负责：注册与安装（TODO(A1)：vpsmon-agent install，设计 27.6）、升级（设计 29）。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"vpsmon/internal/agent/collector"
	"vpsmon/internal/protocol"
)

var version = "0.1.0-dev" // 构建时通过 -ldflags "-X main.version=..." 覆盖为 git describe

func main() {
	server := flag.String("server", "", "server base URL, e.g. https://monitor.example.com")
	token := flag.String("token", "", "agent token (prefer --token-file or MONITOR_AGENT_TOKEN)")
	tokenFile := flag.String("token-file", "", "file containing the agent token")
	interval := flag.Duration("interval", 10*time.Second, "report interval")
	fake := flag.Bool("fake", false, "send fake metrics (for development)")
	allowHTTP := flag.Bool("allow-http", false, "allow plain HTTP to a non-loopback server (development only)")
	ifaces := flag.String("interfaces", "", "comma-separated interfaces to count; empty = auto")
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
	if err := checkServerURL(*server, *allowHTTP); err != nil {
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

	r := &reporter{
		endpoint: strings.TrimRight(*server, "/") + "/api/v1/agent/report",
		token:    tok,
		// 【安全】使用默认 Transport：系统 CA、始终校验证书（设计 23.1）。
		// 10 秒超时：面板卡住时不让上报循环一直阻塞。
		client: &http.Client{Timeout: 10 * time.Second},
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

	// 上报在定时器内同步执行：面板变慢时推迟下一次上报，而不是堆积并发请求（延迟受 http.Client 超时约束）。
	for {
		select {
		case <-tick.C:
			r.send(context.Background(), col, false)
		case <-sig:
			// 退出前补报，避免最后一个周期到停止之间的流量丢失（设计 5.5）。
			// 2 秒超时：停止过久会被 systemd 强制 SIGKILL（单元中 TimeoutStopSec=5）。
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			r.send(ctx, col, true)
			cancel()
			log.Println("stopped")
			return
		}
	}
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

// checkServerURL 校验面板地址，强制 HTTPS。
//
// 【安全】每个请求都携带 Bearer Token，公网明文 HTTP 会泄露 Token（设计 23.1）；
// 回环地址的流量不离开本机，因此允许 HTTP。
func checkServerURL(raw string, allowHTTP bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid --server %q", raw)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" {
		h := u.Hostname()
		if h == "localhost" || h == "127.0.0.1" || h == "::1" || allowHTTP {
			return nil
		}
		return errors.New("refusing plain HTTP to a remote server; use HTTPS (or --allow-http for local dev)")
	}
	return fmt.Errorf("unsupported scheme %q", u.Scheme)
}

// reporter 负责向面板推送上报。
//
// 【安全】只推不拉：忽略响应体，面板返回的任何内容都不能让 Agent 执行动作（设计 1.6.8）。
type reporter struct {
	endpoint string
	token    string
	client   *http.Client
}

// send 采集一次并上报。错误只记录不退出：面板故障不能导致 Agent 退出，下个周期照常上报。
func (r *reporter) send(ctx context.Context, col collector.Collector, final bool) {
	rep, err := col.Collect()
	if err != nil {
		log.Printf("collect: %v", err)
		return
	}
	rep.Timestamp = time.Now().Unix()
	rep.AgentVersion = version
	rep.Final = final
	if err := r.post(ctx, rep); err != nil {
		// TODO(A3): 有上限的内存重试缓冲 + 指数退避（设计 1.6.14、43.5）。
		// 网卡计数是累计值，断网期间流量不会丢，丢的只是这段时间的指标点。
		log.Printf("report: %v", err)
	}
}

// post 发送一次上报，非 2xx 状态视为失败。
func (r *reporter) post(ctx context.Context, rep protocol.Report) error {
	body, _ := json.Marshal(rep)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// 只看状态码。目前 401（Token 被吊销或填错）也会在下个周期继续重试；
	// TODO(A3): 按设计 43.5，401 时停止上报并每小时记录一次 ERROR。
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("server returned %s", resp.Status)
	}
	return nil
}
