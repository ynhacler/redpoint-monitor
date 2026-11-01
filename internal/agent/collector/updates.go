package collector

// 系统更新与待重启（修订第 278 条，设计 4.9.6）：只读文件，不运行 apt / dnf，不需要 root。
//
//	待重启    /run/reboot-required（Debian / Ubuntu 的软件包安装后写入）与 .pkgs 中要求重启的软件包；
//	          或 /lib/modules 中装有比正在运行的内核更新的版本（各发行版通用，包括不写 reboot-required 的 RHEL 系、Arch）
//	可用更新  Ubuntu 的 /var/lib/update-notifier/updates-available（update-notifier 在 apt update 后生成，登录提示用的就是它）：
//	          可安装的更新数与其中的安全更新数；没有这个文件的系统（Debian、RHEL 系等）为未知
//
// 本文件不依赖操作系统：根目录作为参数，测试中用临时目录。

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"vpsmon/internal/protocol"
)

const maxRebootPkgs = 20 // 要求重启的软件包最多列出的个数

var (
	// 18.04：“12 packages can be updated.”；20.04 起：“5 updates can be applied immediately.”（“installed immediately”）。
	// ESM 的“1 additional security update can be applied with ESM Apps.”不算：需要另行订阅，装不上
	reUpdatesPending = regexp.MustCompile(`(?m)^\s*(\d+) (?:updates?|packages?) can be (?:applied|installed|updated)`)
	// “2 of these updates are standard security updates.”、“1 of these updates is a security update.”、“3 updates are security updates.”
	reUpdatesSecurity = regexp.MustCompile(`(?m)^\s*(\d+) (?:of these )?updates? (?:is|are)(?: an?)?(?: standard)? security updates?`)
)

// collectUpdates 汇总 root 下的待重启与可用更新信息；kernel 是正在运行的内核版本（/proc/sys/kernel/osrelease）。
func collectUpdates(root, kernel string) *protocol.Updates {
	u := &protocol.Updates{}
	for _, p := range []string{"run/reboot-required", "var/run/reboot-required"} {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			u.RebootRequired = true
			pkgs, _ := os.ReadFile(filepath.Join(root, p+".pkgs"))
			u.RebootPkgs = rebootPkgs(string(pkgs))
			break
		}
	}
	if k := newerKernel(root, kernel); k != "" {
		u.NewKernel, u.RebootRequired = k, true
	}
	path := filepath.Join(root, "var/lib/update-notifier/updates-available")
	if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
		if b, err := os.ReadFile(path); err == nil {
			u.Pending, u.Security = parseUpdatesAvailable(string(b))
			u.CheckedAt = st.ModTime().Unix()
		}
	}
	return u
}

// rebootPkgs 返回 reboot-required.pkgs 中去重排序后的软件包名。
func rebootPkgs(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if p := strings.TrimSpace(line); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	if len(out) > maxRebootPkgs {
		out = out[:maxRebootPkgs]
	}
	return out
}

// parseUpdatesAvailable 解析 update-notifier 的提示文字，返回可安装的更新数与安全更新数。
// 文件存在但没有更新时内容为空或只有说明，按 0 计。
func parseUpdatesAvailable(s string) (pending, security *int) {
	n, sec := 0, 0
	if m := reUpdatesPending.FindStringSubmatch(s); m != nil {
		n, _ = strconv.Atoi(m[1])
	}
	if m := reUpdatesSecurity.FindStringSubmatch(s); m != nil {
		sec, _ = strconv.Atoi(m[1])
	}
	return &n, &sec
}

// newerKernel 返回 /lib/modules 中比 running 更新的已装内核中最新的一个；没有时为空。
// 只看装全了的内核（有 modules.dep），卸载后残留的目录不算。
func newerKernel(root, running string) string {
	if running == "" {
		return ""
	}
	dir := filepath.Join(root, "lib/modules")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best := ""
	for _, e := range entries {
		name := e.Name()
		if name == running || compareKernel(name, running) <= 0 || (best != "" && compareKernel(name, best) <= 0) {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, name, "modules.dep")); err != nil {
			continue
		}
		best = name
	}
	return best
}

// compareKernel 按数字段比较两个内核版本（6.8.0-51-generic 新于 6.8.0-50-generic，5.14.0-503.el9 新于 5.14.0-427.el9）；
// 数字段相同时为 0（后缀不同的同版本，如 -generic 与 -lowlatency，不算更新）。
func compareKernel(a, b string) int {
	na, nb := kernelNums(a), kernelNums(b)
	for i := 0; i < len(na) && i < len(nb); i++ {
		if na[i] != nb[i] {
			if na[i] < nb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

var reDigits = regexp.MustCompile(`\d+`)

// kernelNums 返回版本中的各数字段。
func kernelNums(s string) []int {
	var out []int
	for _, m := range reDigits.FindAllString(s, -1) {
		n, err := strconv.Atoi(m)
		if err != nil {
			n = 1 << 30
		}
		out = append(out, n)
	}
	return out
}
