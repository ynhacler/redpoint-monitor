// vpsmon-release 生成发布文件（设计 27.5.5、29.7.2）：在 CI 构建完各平台二进制后运行。
//
//	vpsmon-release prepare --version 0.2.0 [--channel stable] [--min-upgradable-from 0.1.0] [--dist dist]
//
// 生成：
//   - dist/agent-0.2.0.sh   安装脚本（由 scripts/agent.sh.in 渲染，内置各构建的 SHA256 与官方公钥）
//   - dist/manifest.json    发布清单（版本、通道、可升级的最低版本、每个文件的大小与 SHA256）
//   - dist/SHA256SUMS       所有二进制与安装脚本的 SHA256
//
// 【安全】本工具不签名，也不接触私钥。签名由开发者在离线机器上用 minisign 完成（scripts/sign-release.sh）。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"vpsmon/internal/release"
)

// agentArchs 与 Makefile AGENT_ARCHS、安装脚本的架构识别一致（设计 27.5.4）。
var agentArchs = []string{"amd64", "arm64", "armv7", "armv6", "386", "riscv64"}

func main() {
	if len(os.Args) < 2 || os.Args[1] != "prepare" {
		fmt.Fprintln(os.Stderr, "usage: vpsmon-release prepare --version X.Y.Z [--channel stable] [--min-upgradable-from X.Y.Z] [--dist dist]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("prepare", flag.ExitOnError)
	version := fs.String("version", "", "release version, e.g. 0.2.0 (leading v is stripped)")
	channel := fs.String("channel", "stable", "release channel: stable / beta")
	minFrom := fs.String("min-upgradable-from", "", "oldest version that may upgrade directly to this one")
	dist := fs.String("dist", "dist", "directory with built binaries; outputs are written here")
	tmpl := fs.String("template", "scripts/agent.sh.in", "installer template")
	notes := fs.String("notes", "", "short release notes")
	_ = fs.Parse(os.Args[2:])
	if err := prepare(*version, *channel, *minFrom, *dist, *tmpl, *notes, time.Now().UTC()); err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		os.Exit(1)
	}
}

func prepare(version, channel, minFrom, dist, tmplPath, notes string, now time.Time) error {
	v, err := release.ParseVersion(version)
	if err != nil {
		return err
	}
	version = v.String()
	if channel != "stable" && channel != "beta" {
		return fmt.Errorf("通道只能是 stable 或 beta：%q", channel)
	}
	if minFrom != "" {
		mv, err := release.ParseVersion(minFrom)
		if err != nil {
			return err
		}
		minFrom = mv.String()
	}
	m := release.Manifest{Product: release.Product, Version: version, Channel: channel,
		ReleasedAt: now.Format(time.RFC3339), MinUpgradableFrom: minFrom, Notes: notes}
	var hashes []string
	for _, arch := range agentArchs {
		name := "vpsmon-agent-linux-" + arch
		a, err := describe(filepath.Join(dist, name))
		if err != nil {
			return fmt.Errorf("缺少构建 %s：%w", name, err)
		}
		a.OS, a.Arch = "linux", arch
		m.Artifacts = append(m.Artifacts, a)
		hashes = append(hashes, a.SHA256+"  "+name)
	}

	tmpl, err := os.ReadFile(tmplPath)
	if err != nil {
		return err
	}
	pub := ""
	if keys := release.TrustedKeys(); len(keys) > 0 {
		pub = keys[0].String()
	}
	script := strings.NewReplacer("@VERSION@", version, "@HASHES@", strings.Join(hashes, "\n"), "@PUBKEY@", pub).Replace(string(tmpl))
	scriptName := "agent-" + version + ".sh"
	if err := os.WriteFile(filepath.Join(dist, scriptName), []byte(script), 0o755); err != nil {
		return err
	}
	if m.Installer, err = describe(filepath.Join(dist, scriptName)); err != nil {
		return err
	}

	// SHA256SUMS：Agent 与面板的全部二进制 + 安装脚本，供人工核对与安装脚本的可选签名校验
	sums := map[string]string{}
	entries, _ := os.ReadDir(dist)
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, "vpsmon-") || n == scriptName {
			a, err := describe(filepath.Join(dist, n))
			if err != nil {
				return err
			}
			sums[n] = a.SHA256
		}
	}
	names := make([]string, 0, len(sums))
	for n := range sums {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s  %s\n", sums[n], n)
	}
	if err := os.WriteFile(filepath.Join(dist, "SHA256SUMS"), []byte(b.String()), 0o644); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(dist, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	if pub == "" {
		fmt.Println("! 程序中还没有官方发布公钥（internal/release/keys.go），安装脚本不会做可选的签名校验")
	}
	fmt.Printf("✓ %s、manifest.json、SHA256SUMS 已生成（版本 %s，通道 %s）\n", scriptName, version, channel)
	fmt.Println("  下一步：在离线机器上运行 scripts/sign-release.sh v" + version + " 签名并发布")
	return nil
}

func describe(path string) (release.Artifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return release.Artifact{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return release.Artifact{}, err
	}
	return release.Artifact{File: filepath.Base(path), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}
