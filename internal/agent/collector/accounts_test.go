package collector

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// 账号：能登录或 UID 为 0 的账号；管理员组成员包括以它为主组的账号（修订第 274 条）。
func TestParseAccounts(t *testing.T) {
	passwd := `root:x:0:0:root:/root:/bin/bash
daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
toor:x:0:0::/root:/usr/sbin/nologin
sync:x:4:65534:sync:/bin:/bin/sync
bob:x:1001:27::/home/bob:/bin/bash
alice:x:1000:1000::/home/alice:
`
	group := "root:x:0:\nsudo:x:27:alice,bob\nwheel:x:10:\ndocker:x:999:alice\n"
	a := parseAccounts(passwd, group)
	if got := fmt.Sprint(a.Users); got != "[{alice 1000 } {bob 1001 /bin/bash} {root 0 /bin/bash} {toor 0 /usr/sbin/nologin}]" {
		t.Errorf("users = %s", got)
	}
	if got := fmt.Sprint(a.Groups); got != "[{sudo [alice bob]} {wheel []}]" {
		t.Errorf("groups = %s", got)
	}
}

// 重要文件：可读的有摘要；可读目录逐个列出文件；读不到的目录只记目录本身；不存在的省略。
func TestCollectConfigFiles(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"etc/ssh/sshd_config":            "PermitRootLogin no\n",
		"etc/ssh/sshd_config.d/10.conf":  "PasswordAuthentication no\n",
		"etc/cron.d/backup":              "0 3 * * * root /usr/local/bin/backup\n",
		"etc/sudoers.d/README":           "x",
		"etc/systemd/system/a.service":   "[Unit]\n",
		"etc/systemd/system/x.wants/a.s": "",
	})
	if os.Getuid() != 0 { // root 能读任何目录，测不到“读不到”
		if err := os.Chmod(filepath.Join(root, "etc/sudoers.d"), 0o300); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(filepath.Join(root, "etc/sudoers.d"), 0o755)
	}
	var got []string
	for _, f := range collectConfigFiles(root) {
		got = append(got, fmt.Sprintf("%s dir=%v hash=%v", f.Path, f.Dir, f.SHA256 != ""))
	}
	want := "[/etc/cron.d/backup dir=false hash=true /etc/ssh/sshd_config dir=false hash=true /etc/ssh/sshd_config.d/10.conf dir=false hash=true " +
		"/etc/sudoers.d dir=true hash=false /etc/systemd/system/a.service dir=false hash=true]"
	if os.Getuid() != 0 && fmt.Sprint(got) != want {
		t.Errorf("files = %v", got)
	}
}
