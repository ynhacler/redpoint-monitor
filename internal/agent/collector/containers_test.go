package collector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, v := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

var (
	idA = strings.Repeat("a", 64)
	idB = strings.Repeat("b", 63) + "c"
	idP = strings.Repeat("1234", 16)
)

// cgroup v2：Docker（systemd 与 cgroupfs 两种驱动）与 Podman；CPU 占比按两次采样之间计算（设计 33.4）。
func TestContainersV2(t *testing.T) {
	root := t.TempDir()
	a := "system.slice/docker-" + idA + ".scope/"
	b := "docker/" + idB + "/"
	p := "machine.slice/libpod-" + idP + ".scope/"
	writeFiles(t, root, map[string]string{
		"cgroup.controllers": "cpu memory pids",
		a + "cpu.stat":       "usage_usec 1000000\nuser_usec 800000\n",
		a + "memory.current": "300000000",
		a + "memory.stat":    "anon 200000000\ninactive_file 50000000\nfile 90000000\n",
		a + "memory.max":     "536870912",
		a + "pids.current":   "23",
		b + "cpu.stat":       "usage_usec 5000\n",
		b + "memory.current": "1000",
		b + "memory.max":     "max",
		p + "cpu.stat":       "usage_usec 0\n",
		p + "memory.current": "2000",
		// 不是容器：普通服务与 ID 不完整的目录
		"system.slice/sshd.service/cpu.stat":    "usage_usec 1\n",
		"system.slice/docker-abc.scope/cpu.stat": "usage_usec 1\n",
	})
	var st containerState
	t0 := time.Unix(1_000_000, 0)
	first := st.collect(root, 2, t0)
	if len(first) != 3 {
		t.Fatalf("应识别 3 个容器：%+v", first)
	}
	for _, c := range first {
		if c.CPU != nil {
			t.Fatalf("首次采样不应有 CPU 占比：%+v", c)
		}
	}
	// 10 秒后：A 用了 4 秒 CPU（2 核 → 20%）
	writeFiles(t, root, map[string]string{a + "cpu.stat": "usage_usec 5000000\n"})
	got := st.collect(root, 2, t0.Add(10*time.Second))
	c := got[0]
	if c.ID != idA[:12] || c.Runtime != "docker" || c.CPU == nil || *c.CPU < 19.99 || *c.CPU > 20.01 {
		t.Fatalf("A 应排第一、CPU 20%%：%+v", c)
	}
	if c.Memory != 250000000 || c.MemLimit != 536870912 || c.Pids != 23 {
		t.Fatalf("A 的内存或进程数不对：%+v", c)
	}
	byID := map[string]string{}
	for _, x := range got {
		byID[x.ID] = x.Runtime
	}
	if byID[idB[:12]] != "docker" || byID[idP[:12]] != "podman" {
		t.Fatalf("运行时不对：%v", byID)
	}
	for _, x := range got {
		if x.ID == idB[:12] && x.MemLimit != 0 {
			t.Fatalf("memory.max 为 max 时应为不限：%+v", x)
		}
	}
}

// cgroup v1：memory、cpuacct、pids 三个层级分开。
func TestContainersV1(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"memory/docker/" + idA + "/memory.usage_in_bytes": "1048576",
		"memory/docker/" + idA + "/memory.limit_in_bytes": "9223372036854771712",
		"memory/docker/" + idA + "/memory.stat":           "total_inactive_file 48576\n",
		"cpuacct/docker/" + idA + "/cpuacct.usage":        "1000000000",
		"pids/docker/" + idA + "/pids.current":            "7",
	})
	var st containerState
	t0 := time.Unix(1_000_000, 0)
	st.collect(root, 1, t0)
	writeFiles(t, root, map[string]string{"cpuacct/docker/" + idA + "/cpuacct.usage": "1500000000"})
	got := st.collect(root, 1, t0.Add(5*time.Second))
	if len(got) != 1 {
		t.Fatalf("应识别 1 个容器：%+v", got)
	}
	c := got[0]
	if c.Memory != 1000000 || c.MemLimit != 0 || c.Pids != 7 || c.CPU == nil || *c.CPU < 9.99 || *c.CPU > 10.01 {
		t.Fatalf("v1 读数不对：%+v cpu=%v", c, c.CPU)
	}
}

// 没有容器（或 cgroup 不可读）时不上报，状态清空。
func TestContainersNone(t *testing.T) {
	var st containerState
	if got := st.collect(t.TempDir(), 2, time.Now()); got != nil {
		t.Fatalf("没有容器时应为 nil：%+v", got)
	}
	if got := st.collect(filepath.Join(t.TempDir(), "missing"), 2, time.Now()); got != nil {
		t.Fatalf("目录不存在时应为 nil：%+v", got)
	}
}

// 超过上限时只保留 CPU / 内存最高的 maxContainers 个。
func TestContainersCap(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"cgroup.controllers": "memory"}
	for i := range maxContainers + 5 {
		id := strings.Repeat("0", 60) + strings.Repeat(string("0123456789abcdef"[i%16]), 2) + string("0123456789abcdef"[i/16]) + "f"
		files["docker/"+id+"/memory.current"] = string(rune('0'+i%10)) + "000"
	}
	writeFiles(t, root, files)
	var st containerState
	if got := st.collect(root, 1, time.Now()); len(got) != maxContainers {
		t.Fatalf("应只保留 %d 个，实际 %d", maxContainers, len(got))
	}
}
