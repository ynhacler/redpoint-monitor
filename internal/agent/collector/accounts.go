package collector

// 账号与重要配置文件（修订第 274 条，设计 4.9.5）：供面板记录变更。都来自不需要 root 的只读来源：
//
//	账号      /etc/passwd：能登录（shell 不是 nologin / false 等）或 UID 为 0 的账号；/etc/group：sudo / wheel / admin 组成员
//	          （包括以它为主组的账号）。【安全】不读 /etc/shadow，不上报密码哈希等任何凭据
//	重要文件  sshd_config 与 sshd_config.d、sudoers 与 sudoers.d、crontab 与 cron.d、用户 crontab 目录、ld.so.preload、
//	          sshd 的 PAM 配置、/etc/systemd/system。【安全】只上报大小、修改时间与 SHA-256 摘要，不上报内容；
//	          读不到内容（sudoers 只有 root 可读）时只有大小与修改时间；读不到的目录只看目录的修改时间（增删文件会改变它）
//
// 不存在的文件省略。本文件不依赖操作系统：根目录作为参数，测试中用临时目录。

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"vpsmon/internal/protocol"
)

const (
	maxAccounts   = 200     // 账号与每组成员的上限
	maxDirFiles   = 50      // 每个目录最多列出的文件
	maxHashedSize = 1 << 20 // 超过 1 MiB 的文件不算摘要
)

// adminGroups 是能取得 root 权限的组。
var adminGroups = []string{"admin", "sudo", "wheel"}

// configPaths 是要记录的文件（dir 为目录：可读时逐个列出其中的文件，不可读时只记目录本身）。
var configPaths = []struct {
	path string
	dir  bool
}{
	{"etc/ssh/sshd_config", false},
	{"etc/ssh/sshd_config.d", true},
	{"etc/sudoers", false},
	{"etc/sudoers.d", true},
	{"etc/crontab", false},
	{"etc/cron.d", true},
	{"var/spool/cron/crontabs", true}, // Debian / Ubuntu 的用户 crontab
	{"var/spool/cron", true},          // RHEL 系的用户 crontab（Debian 上只有子目录，列不出文件时记目录）
	{"etc/ld.so.preload", false},
	{"etc/pam.d/sshd", false},
	{"etc/systemd/system", true},
}

// loginCapable 判断登录 shell 是否能登录（空表示 /bin/sh）。
func loginCapable(shell string) bool {
	switch filepath.Base(shell) {
	case "nologin", "false", "sync", "shutdown", "halt", "true":
		return false
	}
	return true
}

// parseAccounts 解析 /etc/passwd 与 /etc/group 的内容。
func parseAccounts(passwd, group string) *protocol.Accounts {
	a := &protocol.Accounts{Users: []protocol.AccountUser{}, Groups: []protocol.AccountGroup{}}
	primary := map[string][]string{} // gid → 以它为主组的账号
	for _, line := range strings.Split(passwd, "\n") {
		f := strings.Split(strings.TrimSpace(line), ":")
		if len(f) < 7 || strings.HasPrefix(f[0], "#") || f[0] == "" {
			continue
		}
		uid, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		primary[f[3]] = append(primary[f[3]], f[0])
		if (uid == 0 || loginCapable(f[6])) && len(a.Users) < maxAccounts {
			a.Users = append(a.Users, protocol.AccountUser{Name: f[0], UID: uid, Shell: f[6]})
		}
	}
	for _, line := range strings.Split(group, "\n") {
		f := strings.Split(strings.TrimSpace(line), ":")
		if len(f) < 4 || !slices.Contains(adminGroups, f[0]) {
			continue
		}
		members := slices.Clone(primary[f[2]])
		for _, m := range strings.Split(f[3], ",") {
			if m = strings.TrimSpace(m); m != "" {
				members = append(members, m)
			}
		}
		slices.Sort(members)
		members = slices.Compact(members)
		if len(members) > maxAccounts {
			members = members[:maxAccounts]
		}
		if members == nil {
			members = []string{}
		}
		a.Groups = append(a.Groups, protocol.AccountGroup{Name: f[0], Members: members})
	}
	slices.SortFunc(a.Users, func(x, y protocol.AccountUser) int { return strings.Compare(x.Name, y.Name) })
	slices.SortFunc(a.Groups, func(x, y protocol.AccountGroup) int { return strings.Compare(x.Name, y.Name) })
	return a
}

// collectAccounts 读取 root 下的 /etc/passwd 与 /etc/group；passwd 读不到时返回 nil（本轮不比较）。
func collectAccounts(root string) *protocol.Accounts {
	passwd, err := os.ReadFile(filepath.Join(root, "etc/passwd"))
	if err != nil {
		return nil
	}
	group, _ := os.ReadFile(filepath.Join(root, "etc/group"))
	return parseAccounts(string(passwd), string(group))
}

// collectConfigFiles 返回 root 下重要文件的元数据，按路径排序；都不存在时为空数组。
func collectConfigFiles(root string) []protocol.ConfigFile {
	out := []protocol.ConfigFile{}
	for _, c := range configPaths {
		full := filepath.Join(root, c.path)
		st, err := os.Stat(full)
		if err != nil {
			continue
		}
		if !c.dir || !st.IsDir() {
			if st.Mode().IsRegular() {
				out = append(out, fileMeta("/"+c.path, full, st))
			}
			continue
		}
		entries, err := os.ReadDir(full)
		if err != nil {
			out = append(out, protocol.ConfigFile{Path: "/" + c.path, Dir: true, MTime: st.ModTime().Unix()})
			continue
		}
		n := 0
		for _, e := range entries {
			if n >= maxDirFiles {
				break
			}
			p := filepath.Join(full, e.Name())
			fst, err := os.Stat(p)
			if err != nil || !fst.Mode().IsRegular() {
				continue // 子目录、systemd 的 .wants 链接目录等不展开
			}
			out = append(out, fileMeta("/"+c.path+"/"+e.Name(), p, fst))
			n++
		}
	}
	slices.SortFunc(out, func(x, y protocol.ConfigFile) int { return strings.Compare(x.Path, y.Path) })
	return out
}

// fileMeta 返回一个文件的大小、修改时间与摘要（读不到或太大时没有摘要）。
func fileMeta(path, full string, st os.FileInfo) protocol.ConfigFile {
	f := protocol.ConfigFile{Path: path, Size: st.Size(), MTime: st.ModTime().Unix()}
	if st.Size() > maxHashedSize {
		return f
	}
	fh, err := os.Open(full)
	if err != nil {
		return f
	}
	defer fh.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(fh, maxHashedSize+1)); err == nil {
		f.SHA256 = hex.EncodeToString(h.Sum(nil))
	}
	return f
}
