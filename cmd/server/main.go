// vpsmon-server：单二进制面板，内嵌 Web 与 SQLite（设计 2.1、25）。
//
//	vpsmon-server init        --data DIR               创建数据库与管理员账号，输出初始密码
//	vpsmon-server admin reset-password --data DIR      重置管理员密码（忘记密码时的恢复途径，设计 17.2）
//	vpsmon-server add-server  --data DIR --name NAME   新增节点，输出其 Agent Token（开发自测用）
//	vpsmon-server run         --data DIR --listen ADDR [--log-format json|text] [--log-level info]
//
// 【安全】密码与 Token 只在 stdout 输出一次，数据库只保存哈希（设计 23.2、23.4）。
// 本地 CLI 需要面板主机的 root / 数据目录权限，这是最高信任边界（设计 17.2）。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"vpsmon/internal/logging"
	"vpsmon/internal/server"
	"vpsmon/web"
)

var version = "0.1.0-dev" // 构建时通过 -ldflags "-X main.version=..." 覆盖为 git describe（设计 40.3.2）

func usage() {
	fmt.Fprintf(os.Stderr, `vpsmon-server %s

usage:
  vpsmon-server init       --data DIR [--username admin]
  vpsmon-server admin reset-password --data DIR [--username admin]
  vpsmon-server add-server --data DIR --name NAME [--limit-gb N] [--reset-day D]
  vpsmon-server run        --data DIR [--listen 127.0.0.1:8080] [--log-format json|text] [--log-level info]
                           [--public-url https://monitor.example.com]
  vpsmon-server version
`, version)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	fsx := flag.NewFlagSet(cmd, flag.ExitOnError)
	data := fsx.String("data", "./data", "data directory")

	switch cmd {
	case "version":
		fmt.Println(version)

	case "init":
		username := fsx.String("username", "admin", "admin username")
		_ = fsx.Parse(args)
		st := open(*data)
		if n, err := st.UserCount(); err != nil {
			log.Fatal(err)
		} else if n > 0 {
			log.Fatal("already initialised; use `vpsmon-server admin reset-password` to reset the password")
		}
		setPassword(st, *username, "initialised "+*data)

	case "admin":
		if len(args) == 0 || args[0] != "reset-password" {
			usage()
		}
		username := fsx.String("username", "admin", "admin username")
		_ = fsx.Parse(args[1:])
		setPassword(open(*data), *username, "password reset; all sessions of this account were signed out")

	case "add-server":
		name := fsx.String("name", "", "server name")
		limitGB := fsx.Int64("limit-gb", 0, "monthly traffic limit in GB (decimal), 0 = unlimited")
		resetDay := fsx.Int("reset-day", 1, "traffic reset day of month (1-31)")
		_ = fsx.Parse(args)
		if *name == "" {
			log.Fatal("--name is required")
		}
		st := open(*data)
		id, tok, err := st.CreateServer(*name, *limitGB*1_000_000_000, *resetDay)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(os.Stderr, "server %q created (id %d) — agent token (shown once):\n", *name, id)
		fmt.Println(tok)

	case "run":
		listen := fsx.String("listen", "127.0.0.1:8080", "listen address")
		logFormat := fsx.String("log-format", "json", "log format: json or text (design 24.3)")
		logLevel := fsx.String("log-level", "info", "log level: debug, info, warn, error (design 24.4)")
		noCaptcha := fsx.Bool("no-login-captcha", false, "disable the login slider captcha (design 17.4); login rate limiting stays on")
		publicURL := fsx.String("public-url", "", "public base URL used in agent install commands, e.g. https://monitor.example.com (default: derived from the request)")
		_ = fsx.Parse(args)
		level, err := logging.ParseLevel(*logLevel)
		if err != nil {
			log.Fatal(err)
		}
		logger, err := logging.New(os.Stdout, *logFormat, level)
		if err != nil {
			log.Fatal(err)
		}
		slog.SetDefault(logger)
		warnIfPublic(logger, *listen)
		st := open(*data)
		schema, err := st.SchemaVersion()
		if err != nil {
			log.Fatal(err)
		}
		if n, err := st.UserCount(); err == nil && n == 0 {
			// 从开发 token 版本升级时没有账号：提示在面板主机上创建（设计 17.2）
			logger.Warn("no admin account; create one with: vpsmon-server admin reset-password --data "+*data,
				"component", "auth")
		}
		// 启动时记录版本、数据目录、监听地址、数据库与迁移版本、HTTPS 模式（设计 24.6）
		logger.Info("starting", "component", "server", "version", version, "data", *data, "listen", *listen,
			"db", "sqlite", "schema_version", schema, "https", "off (reverse proxy)")
		// 【安全】安装命令中的面板地址必须是 HTTPS：Agent 会拒绝向非回环地址明文上报（设计 23.1）
		if *publicURL != "" && !strings.HasPrefix(*publicURL, "https://") {
			log.Fatal("--public-url must start with https://")
		}
		srv, err := server.New(st, web.Dist(), server.Options{Logger: logger, Version: version, PublicURL: *publicURL,
			NoLoginCaptcha: *noCaptcha})
		if err != nil {
			log.Fatal(err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := srv.Run(ctx, *listen); err != nil {
			log.Fatal(err)
		}

	default:
		usage()
	}
}

// setPassword 为管理员设置随机初始密码（只输出一次，首次登录必须修改，设计 17.4）。
//
// 本地开发可用环境变量 VPSMON_INIT_PASSWORD 指定密码，此时不要求修改（make dev-init 使用）。
// 密码打印到 stdout，说明打印到 stderr，便于脚本把密码重定向到文件。
func setPassword(st *server.Store, username, note string) {
	pw, mustChange := os.Getenv("VPSMON_INIT_PASSWORD"), false
	if pw == "" {
		pw, mustChange = server.RandomPassword(), true
	}
	hash, err := server.HashPassword(pw)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := st.SetAdminPassword(username, hash, mustChange, time.Now()); err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "%s — username: %s, password (shown once%s):\n", note, username,
		map[bool]string{true: ", must be changed at first login", false: ""}[mustChange])
	fmt.Println(pw)
}

func open(dir string) *server.Store {
	st, err := server.OpenStore(dir)
	if err != nil {
		log.Fatal(err)
	}
	return st
}

// warnIfPublic 在面板监听非回环地址时告警。
// 【安全】内置 HTTPS（A6）完成前，面板应只监听回环地址，由 Caddy 或 SSH 隧道对外（设计 23.1、26）。
func warnIfPublic(logger *slog.Logger, listen string) {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return
	}
	ip := net.ParseIP(host)
	if host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return
	}
	logger.Warn("listening on a non-loopback address without TLS; keep the server on 127.0.0.1 behind Caddy or an SSH tunnel",
		"component", "server", "listen", listen)
}
