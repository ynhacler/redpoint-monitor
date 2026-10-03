package report

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

func bootRep(offset int64, boot string) protocol.Report {
	r := rep(offset)
	r.System.BootID = boot
	return r
}

// 普通上报受 MaxAge / MaxQueue 限制；每次旧启动的最后一份计数例外（设计 1.6.14、5.5）。
func TestTrimQueueKeepsCarryOver(t *testing.T) {
	now := time.Unix(base, 0)
	old := -int64(2 * time.Hour / time.Second)
	q := []protocol.Report{
		bootRep(old-20, "a"), bootRep(old-10, "a"), // a 的最后一份：2 小时前，超过 MaxAge
		bootRep(old, "b"),                                     // b 的最后一份
		bootRep(-8*24*3600, "x"),                              // 超过 7 天：不保留（这里时间顺序不影响规则）
		bootRep(-60, "c"), bootRep(-50, "c"), bootRep(0, "c"), // 当前启动
	}
	got := trimQueue(q, now)
	var ts []int64
	for _, r := range got {
		ts = append(ts, r.Timestamp-base)
	}
	want := []int64{old - 10, old, -60, -50, 0}
	if len(ts) != len(want) {
		t.Fatalf("保留 %v，应为 %v", ts, want)
	}
	for i := range want {
		if ts[i] != want[i] {
			t.Fatalf("保留 %v，应为 %v（保持时间顺序）", ts, want)
		}
	}

	// 旧启动最多保留 3 次
	var many []protocol.Report
	for i, b := range []string{"p", "q", "r", "s", "t"} {
		many = append(many, bootRep(old+int64(i), b))
	}
	many = append(many, bootRep(0, "now"))
	if got := trimQueue(many, now); len(got) != maxCarryBoots+1 || got[0].System.BootID != "r" {
		t.Errorf("应保留最近 3 次旧启动的最后一份：%d 份，第一份 %q", len(got), got[0].System.BootID)
	}
}

// 断网期间退出：队列落盘，下次启动恢复并补发；补发完成后删除文件（设计 1.6.14）。
func TestPersistAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	down := &fakePanel{codes: []int{503, 503}}
	r1, c1, _ := newReporter(t, down)
	r1.StatePath = path
	r1.Enqueue(bootRep(0, "a"))
	r1.Flush(context.Background(), false)
	if err := r1.Save(false); err != nil {
		t.Fatal(err)
	}
	c1.add(10 * time.Second)
	r1.Enqueue(bootRep(10, "a"))
	r1.Flush(context.Background(), true)
	if err := r1.Save(false); err != nil {
		t.Fatal(err)
	}
	// 第一次落盘只有 1 份；10 秒后的第二次 Save 在 1 分钟内，不重复写
	if n := savedCount(t, path); n != 1 {
		t.Fatalf("1 分钟内不应重复写盘：文件中 %d 份", n)
	}
	if err := r1.Save(true); err != nil { // 退出时强制写入
		t.Fatal(err)
	}
	if n := savedCount(t, path); n != 2 {
		t.Fatalf("退出时应强制写入全部积压：文件中 %d 份", n)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("落盘文件应为 0600：%v %v", fi, err)
	}

	up := &fakePanel{}
	r2, c2, _ := newReporter(t, up)
	c2.add(20 * time.Second)
	r2.StatePath = path
	if err := r2.Load(); err != nil {
		t.Fatal(err)
	}
	if q := r2.Status().Queued; q != 2 {
		t.Fatalf("应恢复 2 份未发出的上报：%d", q)
	}
	r2.Enqueue(bootRep(20, "a"))
	r2.Flush(context.Background(), false)
	if len(up.received) != 3 || up.received[0] != base {
		t.Errorf("恢复的上报应先于新上报按顺序补发：%v", up.received)
	}
	if err := r2.Save(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("补发完成后应删除落盘文件：%v", err)
	}
}

func TestLoadCorruptOrMissing(t *testing.T) {
	dir := t.TempDir()
	r, _, _ := newReporter(t, &fakePanel{})
	r.StatePath = filepath.Join(dir, "queue.json")
	if err := r.Load(); err != nil {
		t.Errorf("没有文件不是错误：%v", err)
	}
	os.WriteFile(r.StatePath, []byte("{not json"), 0o600)
	if err := r.Load(); err == nil {
		t.Error("损坏的文件应返回错误")
	}
	if _, err := os.Stat(r.StatePath); !os.IsNotExist(err) {
		t.Error("损坏的文件应被删除")
	}
	if r.Status().Queued != 0 {
		t.Error("损坏的文件不应影响队列")
	}
}

func TestSaveNoWriteWhenIdle(t *testing.T) {
	r, _, _ := newReporter(t, &fakePanel{})
	r.StatePath = filepath.Join(t.TempDir(), "queue.json")
	r.Enqueue(rep(0))
	r.Flush(context.Background(), false)
	if err := r.Save(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.StatePath); !os.IsNotExist(err) {
		t.Error("队列为空时不应写盘（设计 4.2：本地磁盘写入极少）")
	}
}

func savedCount(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("有积压时应落盘：%v", err)
	}
	var qf queueFile
	if err := json.Unmarshal(b, &qf); err != nil {
		t.Fatal(err)
	}
	return len(qf.Reports)
}
