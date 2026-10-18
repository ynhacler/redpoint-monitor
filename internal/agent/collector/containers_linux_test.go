//go:build linux

package collector

// 容器采集的集成测试（设计 33.4，修订第 164 条）：主机上有可用的 docker 时（如 CI 的 ubuntu runner）启动一个
// 带内存上限、持续占用 CPU 的容器，确认真实 cgroup 中能读到它；没有 docker 或无法启动时跳过。

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestContainersRealDocker(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("没有 docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "run", "-d", "--memory", "64m", "busybox",
		"sh", "-c", "while :; do :; done").Output()
	if err != nil {
		t.Skipf("无法启动测试容器：%v", err)
	}
	id := strings.TrimSpace(string(out))
	defer exec.Command("docker", "rm", "-f", id).Run()

	st := containerState{proc: "/proc"}
	st.collect("/sys/fs/cgroup", 1, time.Now())
	time.Sleep(1500 * time.Millisecond)
	got := st.collect("/sys/fs/cgroup", 1, time.Now())
	for _, c := range got {
		if c.ID != id[:12] {
			continue
		}
		if c.Runtime != "docker" || c.MemLimit != 64<<20 || c.Memory == 0 || c.CPU == nil || *c.CPU <= 0 || c.Pids < 1 || c.Command != "sh" {
			t.Fatalf("测试容器的读数不对：%+v cpu=%v", c, c.CPU)
		}
		// 重启检测（修订第 167 条）：docker restart 后同一 ID 的重启次数加一
		if err := exec.CommandContext(ctx, "docker", "restart", "-t", "0", id).Run(); err != nil {
			t.Skipf("无法重启测试容器：%v", err)
		}
		time.Sleep(500 * time.Millisecond)
		for _, c2 := range st.collect("/sys/fs/cgroup", 1, time.Now()) {
			if c2.ID == c.ID {
				if c2.Restarts != 1 {
					t.Fatalf("docker restart 后应检测到 1 次重启：%+v", c2)
				}
				return
			}
		}
		t.Fatal("重启后没有找到测试容器")
	}
	t.Fatalf("没有在 cgroup 中找到测试容器 %s：%+v", id[:12], got)
}
