package collector

import (
	"testing"
	"time"
)

func TestCached(t *testing.T) {
	var c cached[int]
	n := 0
	load := func() int { n++; return n }
	t0 := time.Unix(1000, 0)
	if c.get(t0, time.Minute, load) != 1 || c.get(t0.Add(30*time.Second), time.Minute, load) != 1 {
		t.Fatal("有效期内应复用缓存")
	}
	if c.get(t0.Add(time.Minute), time.Minute, load) != 2 {
		t.Fatal("过期后应重新加载")
	}
	c.invalidate()
	if c.get(t0.Add(61*time.Second), time.Minute, load) != 3 {
		t.Fatal("invalidate 后应立即重新加载")
	}
}

func TestSameKeys(t *testing.T) {
	a := map[string]int{"vda": 1, "vdb": 2}
	if !sameKeys(a, map[string]bool{"vda": true, "vdb": false}) || sameKeys(a, map[string]bool{"vda": true}) ||
		sameKeys(a, map[string]bool{"vda": true, "vdc": true}) {
		t.Error("键集合比较错误")
	}
}
