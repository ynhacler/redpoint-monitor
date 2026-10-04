package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func countServers(t *testing.T, dataDir string) int {
	t.Helper()
	st, err := OpenStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM servers`).Scan(&n)
	return n
}

// 在线备份：面板（Store）仍打开、WAL 中有未合并的数据时，备份包含最新数据，权限 0600（设计 25）。
func TestBackupOnline(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	for _, n := range []string{"a", "b", "c"} {
		if _, _, err := st.CreateServer(n, 0, 1); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "b", "backup.db")
	info, err := Backup(dir, out)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(out)
	if info.Servers != 3 || info.SchemaVersion != len(migrations) || fi.Mode().Perm() != 0o600 {
		t.Errorf("备份应含全部数据、权限 0600：%+v %v", info, fi.Mode())
	}
	if _, err := Backup(dir, out); err == nil {
		t.Error("不应覆盖已有文件")
	}
}

func TestRestore(t *testing.T) {
	dir := t.TempDir()
	st, _ := OpenStore(dir)
	st.CreateServer("before", 0, 1)
	backup := filepath.Join(t.TempDir(), "x.db")
	if _, err := Backup(dir, backup); err != nil {
		t.Fatal(err)
	}
	st.CreateServer("after-backup", 0, 1) // 备份之后的修改，恢复后应不存在

	// 面板运行中（持有数据目录锁）：拒绝恢复
	unlock, err := LockDataDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(dir, backup, time.Now()); !errors.Is(err, ErrDataDirLocked) {
		t.Errorf("面板运行中应拒绝恢复：%v", err)
	}
	unlock()
	st.DB.Close()

	prev, err := Restore(dir, backup, time.Date(2026, 10, 4, 1, 2, 3, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(prev, "monitor.db.before-restore-20261004-010203") {
		t.Errorf("应保留当前数据库：%s", prev)
	}
	if n := countServers(t, dir); n != 1 {
		t.Errorf("恢复后应为备份时的 1 个节点：%d", n)
	}
	// 保留下来的旧库是完整的（WAL 已合并）：含备份之后新增的节点
	old := t.TempDir()
	os.Rename(prev, DBFile(old))
	if n := countServers(t, old); n != 2 {
		t.Errorf("保留的旧库应含 2 个节点：%d", n)
	}
}

func TestCheckBackupRejects(t *testing.T) {
	junk := filepath.Join(t.TempDir(), "junk.db")
	os.WriteFile(junk, []byte("not a database"), 0o600)
	if _, err := CheckBackup(junk); err == nil {
		t.Error("应拒绝非 SQLite 文件")
	}

	dir := t.TempDir()
	st, _ := OpenStore(dir)
	st.DB.Exec(`INSERT INTO schema_version (v) VALUES (?)`, len(migrations)+5)
	st.DB.Close()
	if _, err := CheckBackup(DBFile(dir)); err == nil || !strings.Contains(err.Error(), "更新版本") {
		t.Errorf("应拒绝来自更新版本面板的备份：%v", err)
	}
}

func TestPruneBackups(t *testing.T) {
	dir := t.TempDir()
	for _, ts := range []string{"20261001-000000", "20261002-000000", "20261003-000000", "20261004-000000"} {
		os.WriteFile(filepath.Join(dir, "monitor-"+ts+".db"), nil, 0o600)
	}
	os.WriteFile(filepath.Join(dir, "other.db"), nil, 0o600)
	removed, err := PruneBackups(dir, 2)
	if err != nil || len(removed) != 2 || !strings.Contains(removed[0], "20261001") {
		t.Errorf("应删除最旧的 2 份：%v %v", removed, err)
	}
	if left := BackupsIn(dir); len(left) != 2 || !strings.Contains(left[0], "20261004") {
		t.Errorf("剩余最新的 2 份，最新在前：%v", left)
	}
	if _, err := os.Stat(filepath.Join(dir, "other.db")); err != nil {
		t.Error("不应删除其他文件")
	}
}
