package server

import (
	"testing"
	"time"
)

// 写事务先读后写时，另一个写入在中间提交：deferred 事务会直接得到 “database is locked”（压测中降采样与批量写入同时进行时出现）。
// _txlock=immediate 让事务在 BEGIN 时取得写锁，另一个写入按 busy_timeout 等待，两者都成功（设计 21、43.3）。
func TestWriteTxDoesNotFailOnConcurrentWrite(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()

	tx, err := st.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&n); err != nil { // 先读
		t.Fatal(err)
	}
	other := make(chan error, 1)
	go func() {
		_, err := st.DB.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('other', '1', 0)`)
		other <- err
	}()
	time.Sleep(100 * time.Millisecond) // 让另一个写入先尝试
	if _, err := tx.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('mine', '1', 0)`); err != nil {
		t.Fatalf("事务内的写入不应因并发写入失败：%v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	select {
	case err := <-other:
		if err != nil {
			t.Fatalf("并发写入应等待后成功：%v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("并发写入没有在 busy_timeout 内完成")
	}
	st.DB.QueryRow(`SELECT COUNT(*) FROM settings WHERE key IN ('mine', 'other')`).Scan(&n)
	if n != 2 {
		t.Errorf("两次写入都应成功：%d", n)
	}
}
