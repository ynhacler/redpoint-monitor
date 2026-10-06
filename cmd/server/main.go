// vpsmon-server：单二进制面板，内嵌 Web 与 SQLite（设计 2.1、25）。
//
//	vpsmon-server init        --data DIR               创建数据库与管理员账号，输出初始密码
//	vpsmon-server admin reset-password --data DIR      重置管理员密码（忘记密码时的恢复途径，设计 17.2）
//	vpsmon-server add-server  --data DIR --name NAME   新增节点，输出其 Agent Token（开发自测用）
//	vpsmon-server run         --data DIR --listen ADDR|PORT [--log-format json|text] [--log-level info]（参数也可用环境变量 VPSMON_*）
//	vpsmon-server backup      --data DIR [--out FILE] [--keep N]   在线备份（设计 25）
//	vpsmon-server restore     --data DIR --from FILE               离线恢复，须先停止面板
//	vpsmon-server diag        --data DIR [--out FILE] [--log-file FILE]   生成诊断包（设计 24.10）
//
// 【安全】密码与 Token 只在 stdout 输出一次，数据库只保存哈希（设计 23.2、23.4）。
// 本地 CLI 需要面板主机的 root / 数据目录权限，这是最高信任边界（设计 17.2）。
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
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
  vpsmon-server run        --data DIR [--listen 127.0.0.1:8080 | PORT] [--log-format json|text] [--log-level info]
                           (every run flag can also be set via VPSMON_<FLAG>, e.g. VPSMON_LISTEN=9090)
                           [--public-url https://monitor.example.com] [--push-relay https://push.example.com]
                           [--release-mirror] [--no-release-sync]
                           [--domain monitor.example.com [--acme-email EMAIL] [--https-listen :443] [--http-listen :80]]
  vpsmon-server release import --data DIR PATH   import an official release (all files of a GitHub Release) for offline panels
  vpsmon-server audit      --data DIR [--category login|operation] [--result success|failure] [--action NAME|PREFIX.]
                           [--limit 50] [--json]   view the audit log (newest first)
  vpsmon-server backup     --data DIR [--out FILE] [--keep N]   online backup (default DATA/backups/monitor-TIME.db)
  vpsmon-server restore    --data DIR --from FILE   restore a backup (stop the panel first)
  vpsmon-server diag       --data DIR [--out FILE] [--log-file FILE]   write a local diagnostics bundle (nothing is uploaded)
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

	case "release":
		// 离线导入官方发布包（设计 29.1）：验签并逐个校验后保存到镜像目录，面板运行中也可以执行
		if len(args) == 0 || args[0] != "import" {
			usage()
		}
		_ = fsx.Parse(args[1:])
		if fsx.NArg() != 1 {
			usage()
		}
		st := open(*data)
		m, err := server.ImportRelease(context.Background(), st, filepath.Join(*data, "releases"), fsx.Arg(0), time.Now())
		if err != nil {
			log.Fatal("import failed: ", err)
		}
		fmt.Printf("imported vpsmon-agent %s (%s): signature OK, %d builds verified and mirrored\n", m.Version, m.Channel, len(m.Artifacts))
		fmt.Println("start or keep the panel running; install commands now download from this panel")

	case "audit":
		// 在面板主机上查看审计日志（设计 24.8）：与 Web“日志”页同一数据，只读
		category := fsx.String("category", "", "login or operation (default: all)")
		result := fsx.String("result", "", "success or failure (default: all)")
		action := fsx.String("action", "", "exact action, or a prefix ending with '.', e.g. upgrade_task.")
		limit := fsx.Int("limit", 50, "number of records (max 1000)")
		asJSON := fsx.Bool("json", false, "print one JSON object per line")
		_ = fsx.Parse(args)
		if *limit < 1 || *limit > 1000 {
			log.Fatal("--limit must be 1..1000")
		}
		printAudit(open(*data), server.AuditQuery{Category: *category, Result: *result, Action: *action, Limit: *limit}, *asJSON)

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

	case "backup":
		// 在线备份：不停服务，不运行迁移（设计 25）
		out := fsx.String("out", "", "backup file (default: DATA/backups/monitor-YYYYMMDD-HHMMSS.db)")
		keep := fsx.Int("keep", 0, "with the default location, keep only the newest N backups (0 = keep all)")
		_ = fsx.Parse(args)
		path := *out
		if path == "" {
			path = server.DefaultBackupPath(*data, time.Now())
		}
		info, err := server.Backup(*data, path)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("✓ backup written: %s (%d bytes, schema %d, %d servers)\n", info.Path, info.Size, info.SchemaVersion, info.Servers)
		fmt.Println("  contains password and token hashes and notification secrets: store it like a credential (mode 0600)")
		// 云账户凭证是密文，密钥 DATA/secret.key 不在备份中（设计 44.2）：迁移到新机器时需单独复制
		if _, err := os.Stat(filepath.Join(*data, "secret.key")); err == nil {
			fmt.Println("  cloud account credentials are encrypted with", filepath.Join(*data, "secret.key"),
				"which is NOT in the backup: copy it separately (keep it apart from the backup) or re-enter the credentials after restore")
		}
		if *out == "" && *keep > 0 {
			removed, err := server.PruneBackups(filepath.Join(*data, "backups"), *keep)
			if err != nil {
				log.Fatal(err)
			}
			for _, f := range removed {
				fmt.Println("  removed old backup", f)
			}
		}

	case "restore":
		from := fsx.String("from", "", "backup file to restore")
		_ = fsx.Parse(args)
		if *from == "" {
			fmt.Fprintln(os.Stderr, "--from is required; backups in the default location:")
			for _, f := range server.BackupsIn(filepath.Join(*data, "backups")) {
				fmt.Fprintln(os.Stderr, "  "+f)
			}
			os.Exit(2)
		}
		prev, err := server.Restore(*data, *from, time.Now())
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("✓ restored %s\n", *from)
		if prev != "" {
			fmt.Printf("  previous database kept as %s (delete it once the panel works)\n", prev)
		}
		fmt.Println("  start the panel again, e.g. systemctl start vpsmon-server; newer migrations run automatically")

	case "diag":
		out := fsx.String("out", "", "bundle path (default: ./vpsmon-diag-YYYYMMDD-HHMMSS.tar.gz)")
		logFile := fsx.String("log-file", "", "read logs from this file instead of journalctl")
		_ = fsx.Parse(args)
		now := time.Now()
		path := *out
		if path == "" {
			path = "vpsmon-diag-" + now.Format("20060102-150405") + ".tar.gz"
		}
		if err := writeDiag(path, server.Diagnose(*data, version, recentLogs(*logFile), now)); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("✓ diagnostics bundle written: %s\n", path)
		fmt.Println("  nothing was uploaded; credentials are removed and IPs masked — review it before attaching to an issue")

	case "run":
		listen := fsx.String("listen", "127.0.0.1:8080", "listen address; a bare port means 127.0.0.1:PORT (env VPSMON_LISTEN; every flag also reads VPSMON_<FLAG>)")
		logFormat := fsx.String("log-format", "json", "log format: json or text (design 24.3)")
		logLevel := fsx.String("log-level", "info", "log level: debug, info, warn, error (design 24.4)")
		noCaptcha := fsx.Bool("no-login-captcha", false, "disable the login slider captcha (design 17.4); login rate limiting stays on")
		noReleaseSync := fsx.Bool("no-release-sync", false, "do not sync official agent releases from GitHub automatically (offline panels; manual sync in the Web UI still works)")
		releaseMirror := fsx.Bool("release-mirror", false, "also mirror every file of synced official releases into DATA/releases and serve them at /releases (hosts that cannot reach GitHub)")
		publicURL := fsx.String("public-url", "", "public base URL used in agent install commands, e.g. https://monitor.example.com (default: derived from the request)")
		pushRelay := fsx.String("push-relay", "", "Push Relay URL for encrypted App notifications (design 30), e.g. https://push.example.com; empty disables App push")
		domain := fsx.String("domain", "", "built-in HTTPS: obtain certificates via ACME (Let's Encrypt) for these comma-separated domains; implies accepting the CA's terms (design 25)")
		acmeEmail := fsx.String("acme-email", "", "with --domain: contact email for certificate notices (optional)")
		httpsListen := fsx.String("https-listen", ":443", "with --domain: HTTPS listen address; a bare port listens on all addresses")
		httpListen := fsx.String("http-listen", ":80", "with --domain: HTTP listen address for ACME http-01 and redirects to HTTPS (empty = disabled)")
		acmeDir := fsx.String("acme-directory", "", "with --domain: ACME directory URL (default: Let's Encrypt production)")
		_ = fsx.Parse(args)
		// 参数也可以来自环境变量 VPSMON_*（/etc/vpsmon/server.env，设计 28.1）；监听地址可以只写端口
		if err := applyEnv(fsx, lookupEnv); err != nil {
			log.Fatal(err)
		}
		for _, l := range []struct {
			v    *string
			host string
		}{{listen, "127.0.0.1"}, {httpsListen, ""}, {httpListen, ""}} {
			n, err := normalizeListen(*l.v, l.host)
			if err != nil {
				log.Fatal(err)
			}
			*l.v = n
		}
		if *listen == "" {
			log.Fatal("--listen 不能为空")
		}
		var domains []string
		if *domain != "" {
			var err error
			if domains, err = server.ParseDomains(*domain); err != nil {
				log.Fatal(err)
			}
			if *publicURL == "" {
				*publicURL = "https://" + domains[0] // 安装命令中的面板地址
			}
		}
		level, err := logging.ParseLevel(*logLevel)
		if err != nil {
			log.Fatal(err)
		}
		logger, err := logging.New(os.Stdout, *logFormat, level)
		if err != nil {
			log.Fatal(err)
		}
		slog.SetDefault(logger)
		if domains == nil {
			warnIfPublic(logger, *listen)
		}
		// 运行期间锁定数据目录：restore 据此拒绝在面板运行时替换数据库；同一数据目录也不能启动两个面板
		unlock, err := server.LockDataDir(*data)
		if err != nil {
			fmt.Fprintln(os.Stderr, "✗ "+err.Error()) // 此时 log 已接到 slog（INFO 级别），直接写 stderr 更清楚
			os.Exit(1)
		}
		defer unlock()
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
		httpsMode, listenAddr := "off (reverse proxy)", *listen
		if domains != nil {
			httpsMode, listenAddr = "acme "+strings.Join(domains, ","), *httpsListen
		}
		logger.Info("starting", "component", "server", "version", version, "data", *data, "listen", listenAddr,
			"db", "sqlite", "schema_version", schema, "https", httpsMode)
		// 【安全】安装命令中的面板地址必须是 HTTPS：Agent 会拒绝向非回环地址明文上报（设计 23.1）
		if *publicURL != "" && !strings.HasPrefix(*publicURL, "https://") {
			log.Fatal("--public-url must start with https://")
		}
		if err := server.CheckPushRelay(*pushRelay); err != nil {
			log.Fatal(err)
		}
		srv, err := server.New(st, web.Dist(), server.Options{Logger: logger, Version: version, PublicURL: *publicURL, PushRelay: *pushRelay,
			NoLoginCaptcha: *noCaptcha, NoReleaseSync: *noReleaseSync,
			MirrorDir: filepath.Join(*data, "releases"), ReleaseMirror: *releaseMirror})
		if err != nil {
			log.Fatal(err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if domains != nil {
			// 内置 HTTPS（设计 25）：证书缓存在 DATA/certs，重启后不重复申请
			m := server.NewCertManager(server.ACMEOptions{Domains: domains, Email: *acmeEmail,
				CacheDir: filepath.Join(*data, "certs"), Directory: *acmeDir})
			err = srv.RunHTTPS(ctx, server.HTTPSOptions{TLSListen: *httpsListen, HTTPListen: *httpListen,
				TLS: server.TLSConfigFor(m), HTTPHandler: m.HTTPHandler})
		} else {
			err = srv.Run(ctx, *listen)
		}
		if err != nil {
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

// printAudit 按时间倒序打印审计记录。详情在写入时已脱敏（设计 24.7）。
func printAudit(st *server.Store, q server.AuditQuery, asJSON bool) {
	list, _, err := st.ListAudit(q)
	if err != nil {
		log.Fatal(err)
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		for _, l := range list {
			_ = enc.Encode(l)
		}
		return
	}
	if len(list) == 0 {
		fmt.Println("no records")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tRESULT\tACTOR\tACTION\tTARGET\tIP\tDETAILS")
	for _, l := range list {
		actor := l.ActorType
		if l.ActorID != "" {
			actor += ":" + l.ActorID
		}
		target := ""
		if l.TargetType != "" {
			target = l.TargetType + ":" + l.TargetID
			if l.TargetName != "" {
				target += "(" + l.TargetName + ")"
			}
		}
		details := string(l.Details)
		if details == "{}" || details == "null" {
			details = ""
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", time.Unix(l.TS, 0).Format("2006-01-02 15:04:05"),
			l.Result, actor, l.Action, target, l.ClientIP, details)
	}
	_ = tw.Flush()
}

// recentLogs 返回最近 1000 行日志：指定文件时取其末尾，否则尝试 journalctl（systemd 部署，设计 24.3）。读不到时返回空。
func recentLogs(file string) string {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return ""
		}
		lines := strings.Split(string(b), "\n")
		if len(lines) > 1000 {
			lines = lines[len(lines)-1000:]
		}
		return strings.Join(lines, "\n")
	}
	out, err := exec.Command("journalctl", "-u", "vpsmon-server", "-n", "1000", "--no-pager", "-o", "cat").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// writeDiag 把诊断包写成 tar.gz（0600，不覆盖已有文件）。
func writeDiag(path string, files []server.DiagFile) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	now := time.Now()
	for _, df := range files {
		if err := tw.WriteHeader(&tar.Header{Name: "vpsmon-diag/" + df.Name, Mode: 0o600, Size: int64(len(df.Data)), ModTime: now}); err != nil {
			return err
		}
		if _, err := tw.Write(df.Data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
