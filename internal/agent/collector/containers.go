package collector

// 容器（设计 33.4，修订第 164 条）：从 cgroup 读取运行中的 Docker / Podman 容器的 CPU、内存与进程数。
//
// 【安全】只读 /sys/fs/cgroup 下的统计文件（普通用户可读），不连接 Docker / Podman 套接字——
// 能访问 docker.sock 等于拥有 root（约束 8）。因此拿不到容器名、已停止的容器与重启次数；容器名由用户在面板中填写。
//
// 识别的目录（cgroup v2 与 v1 都支持，v1 读 memory、cpuacct、pids 三个层级）：
//
//	system.slice/docker-<64 位十六进制>.scope   Docker，systemd cgroup 驱动
//	docker/<64 位十六进制>                       Docker，cgroupfs 驱动
//	machine.slice/libpod-<64 位十六进制>.scope   Podman（root 运行）
//
// 本文件不依赖操作系统（根目录作为参数，测试中用临时目录），linux.go 以 /sys/fs/cgroup 调用。

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"vpsmon/internal/protocol"
)

// maxContainers 是每份上报最多带的容器数：按 CPU 与内存从高到低保留（设计 4.2 体积）。
const maxContainers = 50

// containerDir 是一个容器的 cgroup 目录；v1 时三个层级各有一个目录（不存在的为空）。
type containerDir struct {
	id, runtime         string
	v2                  string
	memory, cpu, pidsV1 string
}

// containerSample 是一次读取的累计 CPU 时间与当前用量。
type containerSample struct {
	runtime  string
	cpuUsec  uint64
	cpuOK    bool
	memory   uint64
	memLimit uint64
	pids     int
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// matchContainer 识别容器目录名，返回运行时与完整 ID。
func matchContainer(parent, name string) (runtime, id string, ok bool) {
	switch {
	case strings.HasPrefix(name, "docker-") && strings.HasSuffix(name, ".scope"):
		runtime, id = "docker", strings.TrimSuffix(strings.TrimPrefix(name, "docker-"), ".scope")
	case strings.HasPrefix(name, "libpod-") && strings.HasSuffix(name, ".scope"):
		runtime, id = "podman", strings.TrimSuffix(strings.TrimPrefix(name, "libpod-"), ".scope")
	case parent == "docker":
		runtime, id = "docker", name
	default:
		return "", "", false
	}
	return runtime, id, isHex64(id)
}

// findContainers 在 cgroup 根目录下查找容器目录：有 cgroup.controllers 时为 v2，否则按 v1 的层级查找。
func findContainers(root string) []containerDir {
	byID := map[string]*containerDir{}
	scan := func(base string, set func(d *containerDir, path string)) {
		for _, parent := range []string{"system.slice", "docker", "machine.slice"} {
			entries, err := os.ReadDir(filepath.Join(base, parent))
			if err != nil {
				continue
			}
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				rt, id, ok := matchContainer(parent, e.Name())
				if !ok {
					continue
				}
				d := byID[id]
				if d == nil {
					d = &containerDir{id: id, runtime: rt}
					byID[id] = d
				}
				set(d, filepath.Join(base, parent, e.Name()))
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, "cgroup.controllers")); err == nil {
		scan(root, func(d *containerDir, p string) { d.v2 = p })
	} else {
		scan(filepath.Join(root, "memory"), func(d *containerDir, p string) { d.memory = p })
		scan(filepath.Join(root, "cpuacct"), func(d *containerDir, p string) { d.cpu = p })
		scan(filepath.Join(root, "cpu,cpuacct"), func(d *containerDir, p string) { d.cpu = p })
		scan(filepath.Join(root, "pids"), func(d *containerDir, p string) { d.pidsV1 = p })
	}
	out := make([]containerDir, 0, len(byID))
	for _, d := range byID {
		out = append(out, *d)
	}
	return out
}

func readUint(p string) (uint64, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return v, err == nil
}

// parseCPUStatUsec 从 cgroup v2 的 cpu.stat 取 usage_usec。
func parseCPUStatUsec(s string) (uint64, bool) {
	for _, line := range strings.Split(s, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "usage_usec "); ok {
			n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// readContainer 读取一个容器的当前数据；内存不含可回收的非活跃文件页（与整机内存口径一致，设计 4.5）。
func readContainer(d containerDir) containerSample {
	smp := containerSample{runtime: d.runtime}
	memDir, memCur, memMax, statFile := d.memory, "memory.usage_in_bytes", "memory.limit_in_bytes", "memory.stat"
	if d.v2 != "" {
		memDir, memCur, memMax = d.v2, "memory.current", "memory.max"
		if b, err := os.ReadFile(filepath.Join(d.v2, "cpu.stat")); err == nil {
			smp.cpuUsec, smp.cpuOK = parseCPUStatUsec(string(b))
		}
		if n, ok := readUint(filepath.Join(d.v2, "pids.current")); ok {
			smp.pids = int(n)
		}
	} else {
		if d.cpu != "" {
			if ns, ok := readUint(filepath.Join(d.cpu, "cpuacct.usage")); ok {
				smp.cpuUsec, smp.cpuOK = ns/1000, true
			}
		}
		if d.pidsV1 != "" {
			if n, ok := readUint(filepath.Join(d.pidsV1, "pids.current")); ok {
				smp.pids = int(n)
			}
		}
	}
	if memDir != "" {
		used, _ := readUint(filepath.Join(memDir, memCur))
		if b, err := os.ReadFile(filepath.Join(memDir, statFile)); err == nil {
			if inactive, _ := parseCgroupStat(string(b)); inactive < used {
				used -= inactive
			}
		}
		smp.memory = used
		if b, err := os.ReadFile(filepath.Join(memDir, memMax)); err == nil {
			smp.memLimit = parseCgroupLimit(string(b))
		}
	}
	return smp
}

// containerState 保存上一次的 CPU 累计时间，用于计算两次采样之间的 CPU 占比。
type containerState struct {
	prev   map[string]uint64
	prevAt time.Time
}

// collect 读取全部容器并计算 CPU 占比（占整机，cpus 为 CPU 核数）；没有容器时返回 nil。
func (st *containerState) collect(root string, cpus int, now time.Time) []protocol.Container {
	dirs := findContainers(root)
	if len(dirs) == 0 {
		st.prev = nil
		return nil
	}
	dt := now.Sub(st.prevAt).Seconds()
	cur := make(map[string]uint64, len(dirs))
	out := make([]protocol.Container, 0, len(dirs))
	for _, d := range dirs {
		smp := readContainer(d)
		c := protocol.Container{ID: d.id[:12], Runtime: smp.runtime, Memory: smp.memory, MemLimit: smp.memLimit, Pids: smp.pids}
		if smp.cpuOK {
			cur[d.id] = smp.cpuUsec
			if p, ok := st.prev[d.id]; ok && dt > 0 && cpus > 0 && smp.cpuUsec >= p {
				pct := float64(smp.cpuUsec-p) / (dt * 1e6 * float64(cpus)) * 100
				pct = min(max(pct, 0), 100)
				c.CPU = &pct
			}
		}
		out = append(out, c)
	}
	st.prev, st.prevAt = cur, now
	sortContainers(out)
	if len(out) > maxContainers {
		out = out[:maxContainers]
	}
	return out
}

// sortContainers 按 CPU、内存从高到低排序，相同时按 ID，保证顺序稳定。
func sortContainers(cs []protocol.Container) {
	cpu := func(c protocol.Container) float64 {
		if c.CPU == nil {
			return -1
		}
		return *c.CPU
	}
	sort.Slice(cs, func(i, j int) bool {
		if a, b := cpu(cs[i]), cpu(cs[j]); a != b {
			return a > b
		}
		if cs[i].Memory != cs[j].Memory {
			return cs[i].Memory > cs[j].Memory
		}
		return cs[i].ID < cs[j].ID
	})
}
