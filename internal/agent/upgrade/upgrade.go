// Package upgrade 实现节点本地的 vpsmon-agent upgrade（设计 29、29.6～29.12）。
//
// 流程：下载签名的发布清单 → 用内置官方公钥验签 → 防降级与最低版本检查 → 下载本机架构的构建
// → 校验大小与 SHA256 → 试运行并核对版本 → 备份当前版本 → 原子替换 → 重启服务
// → 等待新版本成功上报一次（健康检查）→ 失败则从本机备份回滚并重启。
//
// 【安全】只会安装官方签名、版本更高的 vpsmon-agent；不执行任何其他程序或命令（设计 29.21）。
// 唯一的降级途径是在本机显式加 --allow-downgrade（设计 29.7.4）。
// 远程升级（设计 29.13）复用同一流程：Agent 把文件暂存到 update/ 目录，特权 updater 用 DirSource 从暂存目录读取，
// 不联网、不解析面板的任何指令，按同样的规则独立复验后再替换（见 remote.go、updater.go）。
package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"vpsmon/internal/release"
)

// RootWorkDir 是以 root 运行（本机 upgrade、特权 updater）时的工作目录，只有 root 可读写。
const RootWorkDir = "/var/lib/vpsmon-agent-updater"

// OfficialReleases 是官方发布地址（GitHub Releases，公开，任何人都可以核对签名，设计 29.7.5）。
const OfficialReleases = "https://github.com/ynhacler/redpoint-monitor/releases"

// Options 是一次升级的参数。
type Options struct {
	Current        string // 当前版本（main.version）
	Target         string // 目标版本，如 v0.3.0；空表示最新正式版
	AllowDowngrade bool   // 只能在本机显式指定（设计 29.7.4）
	Mirror         string // 面板镜像地址，如 https://monitor.example.com/releases；空表示官方地址
	Keys           []release.PublicKey

	Bin      string // 要替换的程序：/usr/local/bin/vpsmon-agent
	StateDir string // /var/lib/vpsmon-agent：Agent 的状态目录（status.json）
	// WorkDir 存放下载中的新版本与备份（update/、backup/），默认同 StateDir。
	// 【安全】以 root 运行时必须是只有 root 可写的目录（RootWorkDir）：Agent 可写的目录中可能被预先放置
	// 符号链接或伪造的“备份”，root 写入或回滚时会被利用（设计 29.13）。
	WorkDir string

	// Source 提供清单、签名与二进制；nil 时从 Mirror 或官方地址下载（HTTPSource）
	Source Source

	HTTP          *http.Client
	Restart       func() error // 重启 Agent 服务；nil 表示不重启（例如没有安装服务）
	ReadStatus    func() (version string, lastSuccess int64, err error)
	HealthTimeout time.Duration // 默认 60 秒（设计 29.11）
	Now           func() time.Time
	Sleep         func(time.Duration)
	Out           io.Writer
}

const (
	maxManifest  = 64 << 10
	maxSignature = 4 << 10
	maxBinary    = 200 << 20
)

// Run 执行一次升级。成功返回 nil；任何一步失败都不会留下半替换的程序。
func Run(ctx context.Context, o Options) error {
	o.defaults()
	say := func(f string, a ...any) { fmt.Fprintf(o.Out, f+"\n", a...) }

	// 1. 发布清单与签名
	data, sig, err := o.Source.Manifest(ctx)
	if err != nil {
		return err
	}
	m, err := release.VerifyManifest(o.Keys, data, sig)
	if err != nil {
		return err
	}
	say("✓ 发布清单签名有效：%s %s（%s）", m.Product, m.Version, m.Channel)
	if o.Target != "" {
		want, err := release.ParseVersion(o.Target)
		if err != nil {
			return err
		}
		if got, _ := release.ParseVersion(m.Version); got.Compare(want) != 0 {
			return fmt.Errorf("清单版本 %s 与请求的 %s 不一致", m.Version, o.Target)
		}
	}
	if err := m.CheckUpgrade(o.Current, o.AllowDowngrade); err != nil {
		return err
	}

	// 2. 本机构建
	arch := ArchLabel()
	art, ok := m.ArtifactFor(runtime.GOOS, arch)
	if !ok {
		return fmt.Errorf("版本 %s 没有 %s/%s 的构建", m.Version, runtime.GOOS, arch)
	}
	updDir := filepath.Join(o.WorkDir, "work")
	if err := os.MkdirAll(updDir, 0o700); err != nil {
		return err
	}
	newPath := filepath.Join(updDir, "vpsmon-agent.new")
	defer os.Remove(newPath)
	if err := o.Source.Binary(ctx, m.Version, art.File, newPath, art.Size); err != nil {
		return err
	}
	if got, err := fileSHA256(newPath); err != nil || got != art.SHA256 {
		return fmt.Errorf("SHA256 校验失败，已中止（期望 %s，实际 %s）", art.SHA256, got)
	}
	say("✓ 大小与 SHA256 校验通过")

	// 3. 试运行：确认能在本机执行，且自报版本与清单一致（防止拿到别的版本的构建）
	if err := os.Chmod(newPath, 0o755); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, newPath, "version").Output()
	if err != nil {
		return fmt.Errorf("新版本无法在本机运行：%w", err)
	}
	runV, err1 := release.ParseVersion(strings.TrimSpace(string(out)))
	want, _ := release.ParseVersion(m.Version)
	if err1 != nil || runV.Compare(want) != 0 {
		return fmt.Errorf("新程序自报版本 %q 与清单 %s 不一致", strings.TrimSpace(string(out)), m.Version)
	}
	say("✓ 新版本可以在本机运行")

	// 4. 备份当前版本 → 原子替换（设计 29.8、29.9）
	backup, err := o.backupCurrent()
	if err != nil {
		return fmt.Errorf("备份当前版本失败：%w", err)
	}
	if err := replaceAtomic(newPath, o.Bin); err != nil {
		return fmt.Errorf("替换失败（当前版本未改动）：%w", err)
	}
	say("✓ 已替换 %s（旧版本备份在 %s）", o.Bin, backup)

	// 5. 重启并健康检查（设计 29.11）；失败自动回滚（设计 29.12）
	if o.Restart == nil {
		say("· 未安装服务，未重启；下次启动时使用新版本")
		return nil
	}
	started := o.Now()
	if err := o.Restart(); err != nil {
		return o.rollback(backup, "重启失败："+err.Error())
	}
	if err := o.waitHealthy(m.Version, started); err != nil {
		return o.rollback(backup, err.Error())
	}
	say("✓ 已升级到 %s，新版本已成功上报", m.Version)
	return nil
}

func (o *Options) defaults() {
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 5 * time.Minute}
	}
	if o.HealthTimeout == 0 {
		o.HealthTimeout = 60 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	o.Mirror = strings.TrimRight(o.Mirror, "/")
	if o.Source == nil {
		o.Source = &httpSource{o: o}
	}
	if o.WorkDir == "" {
		o.WorkDir = o.StateDir
	}
}

// Source 提供一次升级所需的文件。实现只负责取文件，校验全部在 Run 中完成：来源不可信也不影响安全性。
type Source interface {
	// Manifest 返回清单原文与 minisign 签名。
	Manifest(ctx context.Context) (data, sig []byte, err error)
	// Binary 把指定版本的构建写到 dst，大小必须恰好为 size。
	Binary(ctx context.Context, version, file, dst string, size int64) error
}

// httpSource 从面板镜像或官方地址下载。
type httpSource struct{ o *Options }

func (h *httpSource) Manifest(ctx context.Context) ([]byte, []byte, error) {
	mURL, sURL := h.o.manifestURLs()
	data, err := h.o.get(ctx, mURL, maxManifest)
	if err != nil {
		return nil, nil, fmt.Errorf("下载发布清单失败：%w", err)
	}
	sig, err := h.o.get(ctx, sURL, maxSignature)
	if err != nil {
		return nil, nil, fmt.Errorf("下载清单签名失败：%w", err)
	}
	return data, sig, nil
}

func (h *httpSource) Binary(ctx context.Context, version, file, dst string, size int64) error {
	url := h.o.fileURL(version, file)
	fmt.Fprintf(h.o.Out, "下载 %s\n", url)
	return h.o.download(ctx, url, dst, size)
}

// DirSource 从暂存目录读取（特权 updater 使用，不联网，设计 29.13）。目录中的内容视为不可信，照常完整复验。
type DirSource struct{ Dir string }

func (d DirSource) Manifest(context.Context) ([]byte, []byte, error) {
	data, err := readLimited(filepath.Join(d.Dir, "manifest.json"), maxManifest)
	if err != nil {
		return nil, nil, err
	}
	sig, err := readLimited(filepath.Join(d.Dir, "manifest.json.minisig"), maxSignature)
	if err != nil {
		return nil, nil, err
	}
	return data, sig, nil
}

func (d DirSource) Binary(_ context.Context, _, file, dst string, size int64) error {
	if file != filepath.Base(file) || strings.HasPrefix(file, ".") {
		return fmt.Errorf("文件名无效：%q", file)
	}
	b, err := readLimited(filepath.Join(d.Dir, file), maxBinary)
	if err != nil {
		return err
	}
	if int64(len(b)) != size {
		return fmt.Errorf("暂存的 %s 大小不符", file)
	}
	return os.WriteFile(dst, b, 0o600)
}

// readLimited 读取暂存目录中的文件：不跟随符号链接，只接受普通文件，限制大小。
// 先打开再对已打开的文件 fstat，避免检查与读取之间被替换（TOCTOU）。
func readLimited(path string, max int64) ([]byte, error) {
	f, err := openNoFollow(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败（不接受符号链接）：%w", filepath.Base(path), err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > max {
		return nil, fmt.Errorf("%s 不是普通文件或超过大小上限", filepath.Base(path))
	}
	return io.ReadAll(io.LimitReader(f, max))
}

// RollbackError 表示新版本健康检查失败、已从本机备份回滚（设计 29.12）。
type RollbackError struct{ Reason string }

func (e *RollbackError) Error() string { return "升级失败，已回滚：" + e.Reason }

// manifestURLs 返回清单与签名地址：官方最新版用 releases/latest/download，指定版本用 download/vX；镜像为 {mirror}/vX/。
func (o *Options) manifestURLs() (string, string) {
	var base string
	switch {
	case o.Mirror != "":
		base = o.Mirror + "/" + tag(o.Target)
	case o.Target == "":
		base = OfficialReleases + "/latest/download"
	default:
		base = OfficialReleases + "/download/" + tag(o.Target)
	}
	return base + "/manifest.json", base + "/manifest.json.minisig"
}

func (o *Options) fileURL(version, file string) string {
	if o.Mirror != "" {
		return o.Mirror + "/v" + version + "/" + file
	}
	return OfficialReleases + "/download/v" + version + "/" + file
}

func tag(v string) string {
	if v == "" {
		return "latest"
	}
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

func (o *Options) get(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s 返回 %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s 超过大小上限", url)
	}
	return b, nil
}

// download 下载到 path，大小必须与清单一致（多一个字节也拒绝）。
func (o *Options) download(ctx context.Context, url, path string, size int64) error {
	if size <= 0 || size > maxBinary {
		return errors.New("清单中的文件大小无效")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败：%s 返回 %d", url, resp.StatusCode)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("下载失败：%w", err)
	}
	if n != size {
		return fmt.Errorf("文件大小不符（期望 %d，实际 %d），已中止", size, n)
	}
	return nil
}

// backupCurrent 把当前程序复制到 backup/vpsmon-agent-<版本>，返回备份路径。
func (o *Options) backupCurrent() (string, error) {
	dir := filepath.Join(o.WorkDir, "backup")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "vpsmon-agent-"+strings.TrimPrefix(o.Current, "v"))
	return dst, copyFile(o.Bin, dst, 0o755)
}

// replaceAtomic 先把新文件复制到目标同目录的临时文件，再 rename 覆盖：
// rename 在同一文件系统上是原子的，正在运行的旧进程不受影响（设计 29.9）。
func replaceAtomic(src, dst string) error {
	tmp := dst + ".new"
	if err := copyFile(src, tmp, 0o755); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func (o *Options) waitHealthy(version string, since time.Time) error {
	want, _ := release.ParseVersion(version)
	deadline := since.Add(o.HealthTimeout)
	for {
		if o.ReadStatus != nil {
			if v, last, err := o.ReadStatus(); err == nil && last >= since.Unix() {
				if got, err := release.ParseVersion(v); err == nil && got.Compare(want) == 0 {
					return nil
				}
			}
		}
		if !o.Now().Before(deadline) {
			return fmt.Errorf("健康检查超时：%s 内新版本没有成功上报", o.HealthTimeout)
		}
		o.Sleep(2 * time.Second)
	}
}

// rollback 从本机备份恢复（不经过网络，设计 29.7.4 唯一允许的“降级”）并重启。
func (o *Options) rollback(backup, reason string) error {
	fmt.Fprintf(o.Out, "✗ %s，回滚到 %s\n", reason, o.Current)
	if err := replaceAtomic(backup, o.Bin); err != nil {
		return fmt.Errorf("%s；回滚失败：%v（备份在 %s，请手动恢复）", reason, err, backup)
	}
	if o.Restart != nil {
		if err := o.Restart(); err != nil {
			return fmt.Errorf("%s；已恢复旧版本，但重启失败：%v", reason, err)
		}
	}
	return &RollbackError{Reason: reason}
}

// ArchLabel 返回本程序的构建架构名，与发布文件名一致：arm 区分 armv7 / armv6（设计 27.5.4）。
func ArchLabel() string {
	if runtime.GOARCH != "arm" {
		return runtime.GOARCH
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "GOARM" && strings.HasPrefix(s.Value, "6") {
				return "armv6"
			}
		}
	}
	return "armv7"
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}
