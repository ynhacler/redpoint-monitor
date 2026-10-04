package server

// 备份与恢复（设计 25、24.2）：面板的全部数据在一个 SQLite 文件中。
//
//	backup   在线执行，不停服务：VACUUM INTO 生成一致的快照（WAL 中已提交的数据也包含在内）。
//	         直接打开数据库文件，不运行迁移：命令行与运行中的面板版本不同也不会修改数据库。
//	restore  离线执行：面板运行时数据目录被锁定，拒绝恢复；先校验备份，再保留当前数据库后原子替换。
//
// 【安全】备份含密码哈希、Token 哈希与通知渠道的密钥（Bot Token、Webhook 地址），文件权限 0600。
// 备份只保存在用户指定的位置，程序不上传（设计 24.2：开发者不接收备份）。

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// DBFile 返回数据目录中的数据库文件路径。
func DBFile(dataDir string) string { return filepath.Join(dataDir, "monitor.db") }

// BackupInfo 描述一份备份。
type BackupInfo struct {
	Path          string
	Size          int64
	SchemaVersion int
	Servers       int
}

func openRaw(path string, readOnly bool) (*sql.DB, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)"
	if readOnly {
		dsn += "&mode=ro"
	}
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// Backup 把 dataDir 中的数据库在线备份到 out（不能已存在），并校验备份可用。
func Backup(dataDir, out string) (BackupInfo, error) {
	src := DBFile(dataDir)
	if _, err := os.Stat(src); err != nil {
		return BackupInfo{}, fmt.Errorf("找不到数据库 %s：%w", src, err)
	}
	if _, err := os.Stat(out); err == nil {
		return BackupInfo{}, fmt.Errorf("%s 已存在，不会覆盖", out)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return BackupInfo{}, err
	}
	db, err := openRaw(src, false) // VACUUM INTO 需要读写方式打开源库，但不修改它
	if err != nil {
		return BackupInfo{}, err
	}
	defer db.Close()
	if _, err := db.Exec(`VACUUM INTO ?`, out); err != nil {
		os.Remove(out)
		return BackupInfo{}, fmt.Errorf("备份失败：%w", err)
	}
	if err := os.Chmod(out, 0o600); err != nil {
		return BackupInfo{}, err
	}
	info, err := CheckBackup(out)
	if err != nil {
		return BackupInfo{}, fmt.Errorf("备份已写入但校验失败：%w", err)
	}
	return info, nil
}

// CheckBackup 校验一份备份：SQLite 完整性检查通过、是本面板的数据库、迁移版本不高于本程序支持的版本。
func CheckBackup(path string) (BackupInfo, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return BackupInfo{}, err
	}
	db, err := openRaw(path, true)
	if err != nil {
		return BackupInfo{}, err
	}
	defer db.Close()
	var check string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil {
		return BackupInfo{}, fmt.Errorf("不是有效的 SQLite 数据库：%w", err)
	}
	if check != "ok" {
		return BackupInfo{}, fmt.Errorf("完整性检查失败：%s", check)
	}
	info := BackupInfo{Path: path, Size: fi.Size()}
	if err := db.QueryRow(`SELECT COALESCE(MAX(v),0) FROM schema_version`).Scan(&info.SchemaVersion); err != nil {
		return BackupInfo{}, errors.New("不是 vpsmon-server 的数据库（没有 schema_version）")
	}
	if info.SchemaVersion > len(migrations) {
		return BackupInfo{}, fmt.Errorf("备份来自更新版本的面板（数据库版本 %d，本程序支持到 %d），请先升级 vpsmon-server",
			info.SchemaVersion, len(migrations))
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM servers`).Scan(&info.Servers); err != nil {
		return BackupInfo{}, errors.New("不是 vpsmon-server 的数据库（没有 servers 表）")
	}
	return info, nil
}

// DefaultBackupPath 返回默认的备份文件名：DATA/backups/monitor-YYYYMMDD-HHMMSS.db。
func DefaultBackupPath(dataDir string, now time.Time) string {
	return filepath.Join(dataDir, "backups", "monitor-"+now.Format("20060102-150405")+".db")
}

// PruneBackups 只保留 dir 中最近的 keep 份默认命名的备份（monitor-*.db），返回删除的文件。keep ≤ 0 时不删除。
func PruneBackups(dir string, keep int) ([]string, error) {
	if keep <= 0 {
		return nil, nil
	}
	files, err := filepath.Glob(filepath.Join(dir, "monitor-*.db"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files) // 文件名含时间戳，按名称即按时间
	var removed []string
	for len(files) > keep {
		if err := os.Remove(files[0]); err != nil {
			return removed, err
		}
		removed, files = append(removed, files[0]), files[1:]
	}
	return removed, nil
}

// Restore 用备份替换 dataDir 中的数据库（面板必须已停止）。当前数据库先合并 WAL，
// 保留为 monitor.db.before-restore-时间戳，返回其路径；没有当前数据库时返回空。
// 恢复较旧版本的备份是安全的：下次启动时自动运行后续迁移。
func Restore(dataDir, from string, now time.Time) (string, error) {
	unlock, err := LockDataDir(dataDir)
	if err != nil {
		return "", err
	}
	defer unlock()
	if _, err := CheckBackup(from); err != nil {
		return "", err
	}
	cur := DBFile(dataDir)
	tmp := cur + ".restore-tmp"
	if err := copyFileSync(from, tmp, 0o600); err != nil {
		os.Remove(tmp)
		return "", err
	}
	// 恢复通常以 root 执行，而面板以 vpsmon-server 用户运行：新数据库沿用原数据库（或数据目录）的属主，
	// 否则面板启动后无法打开 0600 的 root 文件
	if err := chownLike(tmp, cur, dataDir); err != nil {
		os.Remove(tmp)
		return "", err
	}
	var prev string
	if _, err := os.Stat(cur); err == nil {
		// 合并 WAL，使保留下来的旧库是完整的单个文件
		if db, err := openRaw(cur, false); err == nil {
			_, cerr := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
			db.Close()
			if cerr != nil {
				os.Remove(tmp)
				return "", fmt.Errorf("合并当前数据库的 WAL 失败：%w", cerr)
			}
		}
		prev = cur + ".before-restore-" + now.Format("20060102-150405")
		if err := os.Rename(cur, prev); err != nil {
			os.Remove(tmp)
			return "", err
		}
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		os.Remove(cur + suffix)
	}
	if err := os.Rename(tmp, cur); err != nil {
		return prev, err
	}
	return prev, nil
}

// chownLike 把 path 的属主改为 refs 中第一个存在的文件的属主。非 root 运行时无法也无需修改（属主本来就是自己），忽略 EPERM。
func chownLike(path string, refs ...string) error {
	for _, ref := range refs {
		fi, err := os.Stat(ref)
		if err != nil {
			continue
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}
		if err := os.Chown(path, int(st.Uid), int(st.Gid)); err != nil && !errors.Is(err, os.ErrPermission) {
			return err
		}
		return nil
	}
	return nil
}

func copyFileSync(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// BackupsIn 列出目录中默认命名的备份，最新在前。
func BackupsIn(dir string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, "monitor-*.db"))
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	out := files[:0]
	for _, f := range files {
		if !strings.HasSuffix(f, ".restore-tmp") {
			out = append(out, f)
		}
	}
	return out
}
