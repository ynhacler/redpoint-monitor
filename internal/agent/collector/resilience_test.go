package collector

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGuard(t *testing.T) {
	var errs errorList
	ran := false
	guard(&errs, "cpu", func() { var m map[string]int; m["x"] = 1 })
	guard(&errs, "memory", func() { ran = true })
	if !ran {
		t.Fatal("前一项 panic 不应影响后续采集项")
	}
	if len(errs) != 1 || errs[0].Item != "cpu" || !strings.HasPrefix(errs[0].Message, "panic:") {
		t.Errorf("panic 应记录为该项的失败原因：%+v", errs)
	}
}

func TestErrorListLimits(t *testing.T) {
	var errs errorList
	for i := 0; i < maxCollectErrors+5; i++ {
		errs.add("disk", "%s", strings.Repeat("x", 500))
	}
	if len(errs) != maxCollectErrors || len(errs[0].Message) != maxErrorMessage {
		t.Errorf("失败项数量与原因长度应有上限：%d 条，第一条 %d 字节", len(errs), len(errs[0].Message))
	}
}

func TestStatfsProber(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	calls := map[string]int{}
	p := newStatfsProber(func(path string) (diskStat, error) {
		mu.Lock()
		calls[path]++
		mu.Unlock()
		switch path {
		case "/stuck":
			<-release // 模拟失联的块设备：一直阻塞
		case "/broken":
			return diskStat{}, errors.New("input/output error")
		}
		return diskStat{Blocks: 100, Bfree: 40, Bavail: 30, Bsize: 4096}, nil
	})
	p.timeout = 50 * time.Millisecond

	start := time.Now()
	ok, failed := p.statAll([]string{"/", "/stuck", "/broken"})
	if time.Since(start) > time.Second {
		t.Fatal("卡住的挂载点不应拖住整轮采集")
	}
	if _, has := ok["/"]; !has || failed["/stuck"] != "statfs 超时" || !strings.Contains(failed["/broken"], "input/output") {
		t.Fatalf("正常、超时、失败应分别处理：ok=%v failed=%v", ok, failed)
	}

	// 下一轮：上次的调用仍未返回，直接跳过，不再新开 goroutine
	_, failed = p.statAll([]string{"/", "/stuck"})
	mu.Lock()
	n := calls["/stuck"]
	mu.Unlock()
	if n != 1 || !strings.Contains(failed["/stuck"], "仍未返回") {
		t.Errorf("卡住的挂载点每次最多只有一个阻塞调用：调用 %d 次，%v", n, failed)
	}

	// 恢复后重新采集
	close(release)
	for i := 0; i < 100; i++ {
		ok, failed = p.statAll([]string{"/stuck"})
		if len(failed) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, has := ok["/stuck"]; !has {
		t.Errorf("挂载点恢复后应重新采集：%v", failed)
	}
}

func TestDiskFromStat(t *testing.T) {
	m := mountEntry{Mount: "/", FSType: "ext4", Device: "/dev/vda1"}
	d, ok := diskFromStat(m, diskStat{Blocks: 100, Bfree: 40, Bavail: 30, Bsize: 1000})
	if !ok || d.Total != 100_000 || d.Used != 60_000 || d.Available != 30_000 || d.Usage != 60_000*100.0/90_000 {
		t.Errorf("容量计算与 df 一致：%+v", d)
	}
	// 异常统计：Bfree > Blocks、Bavail > Bfree，不能下溢
	d, ok = diskFromStat(m, diskStat{Blocks: 100, Bfree: 150, Bavail: 200, Bsize: 1000})
	if !ok || d.Used != 0 || d.Available != 100_000 || d.Usage != 0 {
		t.Errorf("异常统计应截断而不是下溢：%+v", d)
	}
	if _, ok := diskFromStat(m, diskStat{}); ok {
		t.Error("容量为 0 的挂载点不上报")
	}
}

func TestPickIfaces(t *testing.T) {
	counters := map[string]netCounters{"eth0": {}, "eth1": {}, "docker0": {}, "lo": {}}
	cases := []struct {
		name             string
		explicit, routed []string
		last             []string
		want, missing    []string
		sticky           bool
	}{
		{"默认路由网卡", nil, []string{"eth0", "eth0"}, nil, []string{"eth0"}, nil, false},
		{"默认路由暂时缺失：沿用上次", nil, nil, []string{"eth0"}, []string{"eth0"}, nil, true},
		{"上次的网卡已不存在：退回所有非虚拟网卡", nil, nil, []string{"ens3"}, []string{"eth0", "eth1"}, nil, false},
		{"从未选择过：所有非虚拟网卡，按名称排序", nil, nil, nil, []string{"eth0", "eth1"}, nil, false},
		{"显式指定：不存在的列出，不擅自改统计其他网卡", []string{"eth9"}, []string{"eth0"}, nil, nil, []string{"eth9"}, false},
		{"显式指定：部分存在", []string{"eth1", "eth9"}, nil, nil, []string{"eth1"}, []string{"eth9"}, false},
		{"默认路由在虚拟网卡上：排除后沿用上次", nil, []string{"docker0"}, []string{"eth1"}, []string{"eth1"}, nil, true},
	}
	for _, c := range cases {
		got, missing, sticky := pickIfaces(c.explicit, c.routed, counters, DefaultExclude, c.last)
		if !reflect.DeepEqual(got, c.want) || !reflect.DeepEqual(missing, c.missing) || sticky != c.sticky {
			t.Errorf("%s：(%v, %v, %v)，应为 (%v, %v, %v)", c.name, got, missing, sticky, c.want, c.missing, c.sticky)
		}
	}
}
