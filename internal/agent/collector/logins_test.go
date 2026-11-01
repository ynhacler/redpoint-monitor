package collector

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// utmpRecord 按 glibc struct utmp 的布局生成一条记录。
func utmpRecord(typ int16, line, user, host string, sec int32, addr []byte) []byte {
	b := make([]byte, utmpSize)
	e := binary.NativeEndian
	e.PutUint16(b[0:2], uint16(typ))
	copy(b[8:40], line)
	copy(b[44:76], user)
	copy(b[76:332], host)
	e.PutUint32(b[340:344], uint32(sec))
	copy(b[348:364], addr)
	return b
}

// wtmp：只取登录记录；没有主机名时用 ut_addr_v6；从上次的位置继续读，轮转后从头读（修订第 276 条）。
func TestLoginReader(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wtmp")
	var data []byte
	data = append(data, utmpRecord(2, "~", "reboot", "6.8.0", 100, nil)...)                 // BOOT_TIME
	data = append(data, utmpRecord(7, "pts/0", "bob", "198.51.100.20", 200, nil)...)        // 登录
	data = append(data, utmpRecord(8, "pts/0", "", "", 300, nil)...)                        // 退出
	data = append(data, utmpRecord(7, "pts/1", "root", "", 400, []byte{203, 0, 113, 7})...) // 只有地址
	data = append(data, utmpRecord(7, "tty1", "alice", "", 500, nil)...)                    // 本地控制台
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	r := &loginReader{path: p}
	if got := fmt.Sprint(r.next()); got != "[{200 bob 198.51.100.20 pts/0} {400 root 203.0.113.7 pts/1} {500 alice  tty1}]" {
		t.Fatalf("首次读取 = %s", got)
	}
	if got := r.next(); got != nil {
		t.Fatalf("没有新记录：%v", got)
	}
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.Write(utmpRecord(7, "pts/2", "eve", "2001:db8::1", 600, nil))
	f.Write(make([]byte, 100)) // 写了一半的记录：下次再读
	f.Close()
	if got := fmt.Sprint(r.next()); got != "[{600 eve 2001:db8::1 pts/2}]" {
		t.Fatalf("新增 = %s", got)
	}
	// 轮转：新文件更小，从头读
	os.Remove(p)
	os.WriteFile(p, utmpRecord(7, "pts/0", "bob", "192.0.2.1", 700, nil), 0o644)
	if got := fmt.Sprint(r.next()); got != "[{700 bob 192.0.2.1 pts/0}]" {
		t.Fatalf("轮转后 = %s", got)
	}
	if got := (&loginReader{path: filepath.Join(t.TempDir(), "none")}).next(); got != nil {
		t.Errorf("文件不存在应为 nil：%v", got)
	}
}
