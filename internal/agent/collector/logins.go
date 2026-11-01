package collector

// 登录记录（修订第 276 条，设计 4.9.5）：读 /var/log/wtmp（多数发行版为 root:utmp 0664，普通用户可读）中的 USER_PROCESS
// 记录，只取时间、账号、来源地址与终端。每分钟从上次读到的位置继续读；文件被轮转（变小或换了 inode）时从头读新文件。
// Agent 启动后先带上文件末尾最近 loginBackfill 条，面板按内容去重。
//
// 记录为 glibc 的 struct utmp（各架构都是 384 字节，32 位时间），按本机字节序解析（mips 为大端）。
// 改用 wtmpdb（SQLite）而不再写 wtmp 的系统（如 Debian 13）读不到，省略本项。失败的登录在 btmp 中，只有 root 可读，不采集。

import (
	"encoding/binary"
	"io"
	"net/netip"
	"os"
	"strings"
	"syscall"

	"vpsmon/internal/protocol"
)

const (
	utmpSize      = 384
	utUserProcess = 7
	loginBackfill = 20  // 启动后先带上的最近登录条数
	maxLoginBatch = 200 // 每次最多上报的条数（文件暴涨时只取最新的）
)

// loginReader 记住 wtmp 读到的位置。
type loginReader struct {
	path    string
	ino     uint64
	offset  int64
	started bool
}

// parseUtmp 解析一条记录；不是登录（USER_PROCESS）或没有账号时返回 false。
func parseUtmp(b []byte) (protocol.Login, bool) {
	if len(b) < utmpSize {
		return protocol.Login{}, false
	}
	e := binary.NativeEndian
	if int16(e.Uint16(b[0:2])) != utUserProcess {
		return protocol.Login{}, false
	}
	l := protocol.Login{Line: cstr(b[8:40]), User: cstr(b[44:76]), Host: cstr(b[76:332]), Time: int64(int32(e.Uint32(b[340:344])))}
	if l.User == "" {
		return protocol.Login{}, false
	}
	if l.Host == "" { // 没有主机名时用 ut_addr_v6（IPv4 只用第一个字）
		var a [16]byte
		copy(a[:], b[348:364])
		if a != [16]byte{} {
			if [12]byte(a[4:]) == [12]byte{} {
				l.Host = netip.AddrFrom4([4]byte(a[:4])).String()
			} else {
				l.Host = netip.AddrFrom16(a).String()
			}
		}
	}
	return l, true
}

func cstr(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	return strings.ToValidUTF8(strings.TrimSpace(string(b)), "")
}

// next 返回上次以来新增的登录；文件不存在或读不到时返回 nil（本轮省略）。
func (r *loginReader) next() []protocol.Login {
	f, err := os.Open(r.path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	var ino uint64
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		ino = uint64(sys.Ino)
	}
	size := st.Size() - st.Size()%utmpSize
	switch {
	case !r.started:
		r.offset = max(0, size-loginBackfill*utmpSize) // 启动：只带最近的若干条
	case ino != r.ino || size < r.offset:
		r.offset = 0 // 轮转：从头读新文件
	}
	r.started, r.ino = true, ino
	if size-r.offset > maxLoginBatch*utmpSize {
		r.offset = size - maxLoginBatch*utmpSize
	}
	if size <= r.offset {
		return nil
	}
	buf := make([]byte, size-r.offset)
	if _, err := f.ReadAt(buf, r.offset); err != nil && err != io.EOF {
		return nil
	}
	r.offset = size
	var out []protocol.Login
	for i := 0; i+utmpSize <= len(buf); i += utmpSize {
		if l, ok := parseUtmp(buf[i : i+utmpSize]); ok {
			out = append(out, l)
		}
	}
	return out
}
