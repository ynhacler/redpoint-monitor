package server

// 诊断包（设计 24.10）：版本、系统信息、数据库统计、节点与配置概况、最近的日志。
// 保存在本地，由用户决定是否附加到 GitHub Issue，程序不上传。
//
// 【安全】诊断包可能被公开：只含汇总数字，不含节点名、主机名、IP、通知渠道地址与任何凭证；
// 日志经 logging.Redact 去掉凭证，并把 IP 地址掩码为前两段。

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"vpsmon/internal/logging"
)

// DiagFile 是诊断包中的一个文件。
type DiagFile struct {
	Name string
	Data []byte
}

// Diagnose 生成诊断包的内容。只读打开数据库，不运行迁移；logs 为最近的日志原文（由调用方读取），此处脱敏。
func Diagnose(dataDir, version, logs string, now time.Time) []DiagFile {
	var files []DiagFile
	add := func(name, s string) { files = append(files, DiagFile{name, []byte(s)}) }

	add("README.txt", fmt.Sprintf(`vpsmon-server 诊断包，生成于 %s。

内容：version.txt（版本与系统）、database.txt（数据库统计）、summary.txt（节点与配置概况）、logs.txt（最近的日志）。
不含节点名、主机名、IP、通知渠道地址与任何凭证；日志已去除凭证，IP 已掩码。
本文件只保存在本地，程序不会上传；是否附加到 GitHub Issue 由你决定，附加前请再检查一遍内容。
`, now.Format(time.RFC3339)))

	add("version.txt", fmt.Sprintf("vpsmon-server %s\ngo %s\n%s/%s, %d CPU\ntime %s\n",
		version, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), now.Format(time.RFC3339)))

	add("database.txt", dbStats(dataDir))
	add("summary.txt", summary(dataDir, now))
	if strings.TrimSpace(logs) == "" {
		logs = "（未读取到日志：没有 journalctl 或 vpsmon-server 单元；可用 --log-file 指定日志文件）\n"
	}
	add("logs.txt", MaskIPs(logging.Redact(logs)))
	return files
}

func dbStats(dataDir string) string {
	var b bytes.Buffer
	path := DBFile(dataDir)
	for _, f := range []string{path, path + "-wal"} {
		if fi, err := os.Stat(f); err == nil {
			fmt.Fprintf(&b, "%-12s %d bytes\n", f[len(dataDir)+1:], fi.Size())
		}
	}
	db, err := openRaw(path, true)
	if err != nil {
		fmt.Fprintf(&b, "无法打开数据库：%v\n", err)
		return b.String()
	}
	defer db.Close()
	q := func(sql string) string {
		var v string
		if err := db.QueryRow(sql).Scan(&v); err != nil {
			return "（" + err.Error() + "）"
		}
		return v
	}
	fmt.Fprintf(&b, "schema       %s（本程序 %d）\n", q(`SELECT COALESCE(MAX(v),0) FROM schema_version`), len(migrations))
	fmt.Fprintf(&b, "page_size    %s\npage_count   %s\nfreelist     %s\nquick_check  %s\n\n",
		q(`PRAGMA page_size`), q(`PRAGMA page_count`), q(`PRAGMA freelist_count`), q(`PRAGMA quick_check`))
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return b.String()
	}
	var tables []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		tables = append(tables, n)
	}
	rows.Close()
	fmt.Fprintln(&b, "行数：")
	for _, t := range tables {
		fmt.Fprintf(&b, "  %-24s %s\n", t, q(`SELECT COUNT(*) FROM "`+strings.ReplaceAll(t, `"`, `""`)+`"`))
	}
	return b.String()
}

func summary(dataDir string, now time.Time) string {
	var b bytes.Buffer
	db, err := openRaw(DBFile(dataDir), true)
	if err != nil {
		return "无法打开数据库：" + err.Error() + "\n"
	}
	defer db.Close()
	count := func(label, sql string, args ...any) {
		var n int
		if err := db.QueryRow(sql, args...).Scan(&n); err != nil {
			fmt.Fprintf(&b, "%-28s （%v）\n", label, err)
			return
		}
		fmt.Fprintf(&b, "%-28s %d\n", label, n)
	}
	count("节点", `SELECT COUNT(*) FROM servers`)
	count("  待安装", `SELECT COUNT(*) FROM servers WHERE enroll_state = 'pending'`)
	count("  5 分钟内有上报", `SELECT COUNT(*) FROM servers WHERE last_seen_at >= ?`, now.Add(-5*time.Minute).Unix())
	count("  非默认采样间隔", `SELECT COUNT(*) FROM servers WHERE report_interval_s > 0 AND report_interval_s <> 10`)
	count("管理员账号", `SELECT COUNT(*) FROM users`)
	count("活动告警", `SELECT COUNT(*) FROM alert_events WHERE state = 'firing'`)
	count("自定义告警规则", `SELECT COUNT(*) FROM alert_rules WHERE scope_type <> 'global'`)
	count("生效中的静音 / 维护", `SELECT COUNT(*) FROM silences WHERE ends_at IS NULL OR ends_at > ?`, now.Unix())
	// 通知渠道只统计类型与启用状态：地址与 Token 属于凭证（设计 24.7）
	if rows, err := db.Query(`SELECT type, enabled, COUNT(*) FROM notification_channels GROUP BY type, enabled ORDER BY type`); err == nil {
		for rows.Next() {
			var typ string
			var enabled, n int
			rows.Scan(&typ, &enabled, &n)
			fmt.Fprintf(&b, "%-28s %d\n", fmt.Sprintf("通知渠道 %s（%s）", typ, map[int]string{1: "启用", 0: "停用"}[enabled]), n)
		}
		rows.Close()
	}
	if rows, err := db.Query(`SELECT version FROM agent_releases ORDER BY synced_at DESC LIMIT 5`); err == nil {
		var vs []string
		for rows.Next() {
			var v string
			rows.Scan(&v)
			vs = append(vs, v)
		}
		rows.Close()
		fmt.Fprintf(&b, "%-28s %s\n", "已同步的 Agent 版本", strings.Join(vs, " "))
	}
	if rows, err := db.Query(`SELECT key FROM settings ORDER BY key`); err == nil {
		var ks []string
		for rows.Next() {
			var k string
			rows.Scan(&k)
			ks = append(ks, k)
		}
		rows.Close()
		fmt.Fprintf(&b, "%-28s %s\n", "已修改的系统设置", strings.Join(ks, " "))
	}
	return b.String()
}

var (
	ipv4Pattern = regexp.MustCompile(`\b(\d{1,3})\.(\d{1,3})\.\d{1,3}\.\d{1,3}\b`)
	// 至少 4 组或含 “::”，避免把 01:20:47 这类时间当成 IPv6
	ipv6Pattern = regexp.MustCompile(`\b([0-9a-fA-F]{1,4})(?::[0-9a-fA-F]{0,4}){3,7}\b|\b([0-9a-fA-F]{1,4})?::[0-9a-fA-F:]*[0-9a-fA-F]\b`)
)

// MaskIPs 把 IPv4 掩码为前两段（203.0.x.x），IPv6 只保留第一组（2001:…）。
func MaskIPs(s string) string {
	s = ipv4Pattern.ReplaceAllString(s, "$1.$2.x.x")
	return ipv6Pattern.ReplaceAllStringFunc(s, func(m string) string {
		first, _, _ := strings.Cut(m, ":")
		return first + ":…"
	})
}
