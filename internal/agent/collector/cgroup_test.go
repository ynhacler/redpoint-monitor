package collector

import (
	"testing"

	"vpsmon/internal/protocol"
)

func TestParseCgroupLimit(t *testing.T) {
	for in, want := range map[string]uint64{
		"536870912\n": 512 << 20, "max\n": 0, "": 0, "9223372036854771712": 0, // v1 不限
		"abc": 0,
	} {
		if got := parseCgroupLimit(in); got != want {
			t.Errorf("parseCgroupLimit(%q) = %d，应为 %d", in, got, want)
		}
	}
}

func TestParseCgroupStat(t *testing.T) {
	v2 := "anon 104857600\nfile 52428800\nactive_file 20971520\ninactive_file 31457280\n"
	if in, c := parseCgroupStat(v2); in != 30<<20 || c != 50<<20 {
		t.Errorf("v2：inactive=%d cache=%d", in, c)
	}
	v1 := "cache 1\nrss 2\ntotal_cache 41943040\ntotal_rss 104857600\ntotal_inactive_file 10485760\n"
	if in, c := parseCgroupStat(v1); in != 10<<20 || c != 40<<20 {
		t.Errorf("v1：inactive=%d cache=%d", in, c)
	}
}

func TestApplyCgroupMemory(t *testing.T) {
	const MB = 1 << 20
	host := protocol.Memory{Total: 64 << 30, Used: 20 << 30, Available: 44 << 30, Usage: 31.25, Free: 1 << 30}
	cases := []struct {
		name    string
		mem     protocol.Memory
		cg      cgroupMemory
		total   uint64
		used    uint64
		applied bool
	}{
		{"LXC 未挂 lxcfs：512 MB 容器显示宿主机 64 GB → 改用 cgroup", host,
			cgroupMemory{Limit: 512 * MB, Usage: 300 * MB, Inactive: 100 * MB, Cache: 150 * MB}, 512 * MB, 200 * MB, true},
		{"挂了 lxcfs：meminfo 已是容器视角，不修改", protocol.Memory{Total: 512 * MB, Used: 200 * MB, Available: 312 * MB},
			cgroupMemory{Limit: 512 * MB, Usage: 300 * MB}, 512 * MB, 200 * MB, false},
		{"VM / 物理机：根 cgroup 不限，不修改", host, cgroupMemory{}, 64 << 30, 20 << 30, false},
		{"上限大于物理内存：不修改", host, cgroupMemory{Limit: 128 << 30, Usage: 1}, 64 << 30, 20 << 30, false},
		{"非活跃文件页大于用量：已用不为负", host, cgroupMemory{Limit: 512 * MB, Usage: 50 * MB, Inactive: 80 * MB}, 512 * MB, 0, true},
		{"用量超过上限（统计瞬时值）：截断", host, cgroupMemory{Limit: 512 * MB, Usage: 600 * MB}, 512 * MB, 512 * MB, true},
	}
	for _, c := range cases {
		got, applied := applyCgroupMemory(c.mem, c.cg)
		if got.Total != c.total || got.Used != c.used || applied != c.applied || got.Available != got.Total-got.Used {
			t.Errorf("%s：%+v applied=%v", c.name, got, applied)
		}
	}
	if got, _ := applyCgroupMemory(host, cgroupMemory{Limit: 512 * MB, Usage: 300 * MB, Inactive: 100 * MB}); got.Usage < 39 || got.Usage > 39.1 {
		t.Errorf("使用率按容器上限计算：%v", got.Usage)
	}
}
