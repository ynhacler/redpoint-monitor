package report

// 断网缓冲落盘（设计 1.6.14）：面板不可达时，未发出的上报定期写入状态目录，Agent 重启（升级、主机重启）后继续补发。
//
// 设计 4.2 要求本地磁盘写入极少：队列为空时不写；有积压时最多每分钟写一次，退出时再写一次；
// 补发完成后删除文件。文件只含指标，不含 Token（设计 24.7）。

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"vpsmon/internal/protocol"
)

const (
	saveEvery    = time.Minute
	maxQueueFile = 4 << 20 // 读取上限：正常不超过 MaxQueue × 几 KB，超出视为损坏
	queueVersion = 1
)

type queueFile struct {
	Version int               `json:"version"`
	Reports []protocol.Report `json:"reports"`
}

// Load 读取上次退出时落盘的队列，放在当前队列之前并按缓存上限裁剪。没有文件时不做任何事；
// 文件损坏时删除并返回错误（只需记录，不影响运行）。StatePath 为空时不落盘。
func (r *Reporter) Load() error {
	if r.StatePath == "" {
		return nil
	}
	f, err := os.Open(r.StatePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	b, err := io.ReadAll(io.LimitReader(f, maxQueueFile+1))
	f.Close()
	var qf queueFile
	if err == nil && len(b) > maxQueueFile {
		err = errors.New("queue file too large")
	}
	if err == nil {
		err = json.Unmarshal(b, &qf)
	}
	if err == nil && qf.Version != queueVersion {
		err = errors.New("unknown queue file version")
	}
	if err != nil {
		_ = os.Remove(r.StatePath)
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queue = trimQueue(append(qf.Reports, r.queue...), r.now())
	r.onDisk = true
	return nil
}

// Save 按需把队列写入 StatePath：队列为空时删除已有文件；否则 force 为 true 或距上次写入超过 saveEvery 时写入。
// 写入先写临时文件再改名，进程中途被杀也不会留下半个文件。
func (r *Reporter) Save(force bool) error {
	if r.StatePath == "" {
		return nil
	}
	r.mu.Lock()
	now := r.now()
	if len(r.queue) == 0 || r.authFailed {
		// 凭证失效期间不落盘：重新注册后旧 Token 的积压没有意义
		had := r.onDisk
		r.onDisk = false
		r.mu.Unlock()
		if had {
			if err := os.Remove(r.StatePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		return nil
	}
	if !force && now.Sub(r.savedAt) < saveEvery {
		r.mu.Unlock()
		return nil
	}
	b, err := json.Marshal(queueFile{Version: queueVersion, Reports: r.queue})
	r.savedAt = now
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if err := writeAtomic(r.StatePath, b); err != nil {
		return err
	}
	r.mu.Lock()
	r.onDisk = true
	r.mu.Unlock()
	return nil
}

// writeAtomic 写入同目录的临时文件（0600）并改名覆盖目标文件。
func writeAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".queue-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err = tmp.Write(b); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}
