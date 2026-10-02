// vpsmon-server：单二进制面板，内嵌 Web 与 SQLite（设计 2.1、25）。
//
//	vpsmon-server init        --data DIR               创建数据库，输出一个开发用 admin token
//	vpsmon-server add-server  --data DIR --name NAME   新增节点，输出其 Agent Token
//	vpsmon-server run         --data DIR --listen ADDR [--log-format json|text] [--log-level info]
//
// 【安全】Token 只在 stdout 输出一次，数据库只保存哈希（设计 23.2）。
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
	"syscall"

	"vpsmon/internal/logging"
	"vpsmon/internal/server"
	"vpsmon/web"
)

var version = "0.1.0-dev" // 构建时通过 -ldflags "-X main.version=..." 覆盖为 git describe（设计 40.3.2）

func usage() {
	fmt.Fprintf(os.Stderr, `vpsmon-server %s

usage:
  vpsmon-server init       --data DIR
  vpsmon-server add-server --data DIR --name NAME [--limit-gb N] [--reset-day D]
  vpsmon-server run        --data DIR [--listen 127.0.0.1:8080] [--log-format json|text] [--log-level info]
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
		_ = fsx.Parse(args)
		st := open(*data)
		tok, err := st.CreateAdminToken()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Fprintln(os.Stderr, "initialised", *data, "— admin token (shown once):")
		fmt.Println(tok)

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
		// 启动时记录版本、数据目录、监听地址、数据库与迁移版本、HTTPS 模式（设计 24.6）
		logger.Info("starting", "component", "server", "version", version, "data", *data, "listen", *listen,
			"db", "sqlite", "schema_version", schema, "https", "off (reverse proxy)")
		srv, err := server.New(st, web.Dist(), server.Options{Logger: logger, Version: version})
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
