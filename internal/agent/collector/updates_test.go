package collector

import (
	"fmt"
	"testing"
)

// 系统更新与待重启（修订第 278 条）：reboot-required、更新的内核、Ubuntu update-notifier 的各版本文字。
func TestCollectUpdates(t *testing.T) {
	show := func(p *int) string {
		if p == nil {
			return "?"
		}
		return fmt.Sprint(*p)
	}
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"没有任何信息（Debian / RHEL 未更新）", map[string]string{"lib/modules/6.8.0-50-generic/modules.dep": ""},
			"reboot=false pkgs=[] kernel= pending=? security=?"},
		{"Ubuntu 24.04：有更新、待重启", map[string]string{
			"run/reboot-required":      "*** System restart required ***\n",
			"run/reboot-required.pkgs": "linux-image-6.8.0-51-generic\nlinux-base\nlinux-image-6.8.0-51-generic\n",
			"var/lib/update-notifier/updates-available": "\nExpanded Security Maintenance for Applications is not enabled.\n\n" +
				"5 updates can be applied immediately.\n2 of these updates are standard security updates.\n" +
				"To see these additional updates run: apt list --upgradable\n\n1 additional security update can be applied with ESM Apps.\n",
		}, "reboot=true pkgs=[linux-base linux-image-6.8.0-51-generic] kernel= pending=5 security=2"},
		{"Ubuntu 20.04 文字", map[string]string{
			"var/lib/update-notifier/updates-available": "1 update can be installed immediately.\n1 of these updates is a security update.\n",
		}, "reboot=false pkgs=[] kernel= pending=1 security=1"},
		{"Ubuntu 18.04 文字", map[string]string{
			"var/lib/update-notifier/updates-available": "12 packages can be updated.\n3 updates are security updates.\n",
		}, "reboot=false pkgs=[] kernel= pending=12 security=3"},
		{"没有更新：文件为空", map[string]string{"var/lib/update-notifier/updates-available": ""},
			"reboot=false pkgs=[] kernel= pending=0 security=0"},
		{"装了更新的内核（RHEL 系，不写 reboot-required）", map[string]string{
			"lib/modules/6.8.0-50-generic/modules.dep":    "",
			"lib/modules/6.8.0-49-generic/modules.dep":    "", // 旧内核
			"lib/modules/6.8.0-51-generic/modules.dep":    "",
			"lib/modules/6.8.0-52-generic/README":         "", // 卸载后残留的目录
			"lib/modules/6.8.0-50-lowlatency/modules.dep": "",
		}, "reboot=true pkgs=[] kernel=6.8.0-51-generic pending=? security=?"},
		{"正在运行的内核已被卸载（Arch）", map[string]string{"lib/modules/6.8.1-arch1-1/modules.dep": ""},
			"reboot=true pkgs=[] kernel=6.8.1-arch1-1 pending=? security=?"},
	}
	for _, c := range cases {
		root := t.TempDir()
		writeFiles(t, root, c.files)
		running := "6.8.0-50-generic"
		if c.name == "正在运行的内核已被卸载（Arch）" {
			running = "6.8.0-arch1-1"
		}
		u := collectUpdates(root, running)
		got := fmt.Sprintf("reboot=%v pkgs=%v kernel=%s pending=%s security=%s", u.RebootRequired, u.RebootPkgs, u.NewKernel, show(u.Pending), show(u.Security))
		if got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
		if (u.Pending != nil) != (u.CheckedAt > 0) {
			t.Errorf("%s: 有更新数时应有统计时间", c.name)
		}
	}
}

func TestCompareKernel(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"6.8.0-51-generic", "6.8.0-50-generic", 1},
		{"6.8.0-9-generic", "6.8.0-10-generic", -1},
		{"5.14.0-503.11.1.el9_5.x86_64", "5.14.0-427.13.1.el9_4.x86_64", 1},
		{"6.8.0-50-lowlatency", "6.8.0-50-generic", 0},
		{"6.10.1-arch1-1", "6.9.9-arch1-1", 1},
	} {
		if got := compareKernel(c.a, c.b); got != c.want {
			t.Errorf("compareKernel(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
