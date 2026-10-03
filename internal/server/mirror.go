package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"vpsmon/internal/release"
)

// 面板镜像与离线导入（设计 27.5.3、29.1）。
//
// 部分主机访问 GitHub 不稳定，内网 / 离线面板则完全无法访问。面板可以把已验签的官方版本连同全部构建
// 保存到数据目录，在 /releases/v{版本}/ 下提供下载；安装命令与远程升级随后从本面板获取。
//
// 【安全】镜像只是“搬运”官方签名的文件（CLAUDE.md 约束 2）：
//   - 来源只有两个：从官方发布地址同步，或在面板主机上用命令行导入官方发布包；Web 不接受上传
//   - 写入前逐个按已验签清单中的大小与 SHA256 校验，不在清单中的文件不保存、不提供
//   - 提供下载时再次核对（按大小与修改时间缓存校验结果），磁盘上的文件被替换也不会被发出
//   - 主机端仍按命令中的哈希校验脚本、按脚本内置的 SHA256 校验构建，镜像被篡改也无法执行恶意内容

const (
	maxArtifactBytes = 128 << 20 // 单个构建的大小上限
	keepMirrored     = 3         // 镜像保留最新的几个版本
)

// mirrorSource 读取发布中的一个文件：同步时从官方地址下载，导入时从本地目录读取。
type mirrorSource func(ctx context.Context, name string) (io.ReadCloser, error)

// storeMirror 校验并保存一个版本的全部文件到 root/v{版本}/。先写入临时目录，全部通过后整体替换。
func storeMirror(ctx context.Context, keys []release.PublicKey, root string, m *release.Manifest, manifest, sig []byte, src mirrorSource) error {
	if root == "" {
		return errors.New("未配置镜像目录")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	dir := filepath.Join(root, "v"+m.Version)
	tmp := dir + ".tmp"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	for _, a := range append([]release.Artifact{m.Installer}, m.Artifacts...) {
		if err := copyVerified(ctx, src, a, tmp); err != nil {
			return fmt.Errorf("%s：%w", a.File, err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "manifest.json"), manifest, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "manifest.json.minisig"), sig, 0o644); err != nil {
		return err
	}
	// 可选：签名的 SHA256SUMS（安装脚本在主机装有 minisign 时额外校验）与脚本签名，验签通过才保存
	if sums, sumsSig, ok := readSigned(ctx, keys, src, "SHA256SUMS", 64<<10); ok {
		os.WriteFile(filepath.Join(tmp, "SHA256SUMS"), sums, 0o644)
		os.WriteFile(filepath.Join(tmp, "SHA256SUMS.minisig"), sumsSig, 0o644)
	}
	if inst, err := os.ReadFile(filepath.Join(tmp, m.Installer.File)); err == nil {
		if s, err := readAll(ctx, src, m.Installer.File+".minisig", maxSignatureBytes); err == nil {
			if _, err := release.Verify(keys, inst, s); err == nil {
				os.WriteFile(filepath.Join(tmp, m.Installer.File+".minisig"), s, 0o644)
			}
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.Rename(tmp, dir)
}

// copyVerified 复制一个清单中列出的文件，边写边计算 SHA256，大小与哈希都与清单一致才保留。
func copyVerified(ctx context.Context, src mirrorSource, a release.Artifact, dir string) error {
	if a.File != filepath.Base(a.File) || strings.HasPrefix(a.File, ".") {
		return errors.New("文件名无效")
	}
	if a.Size > maxArtifactBytes {
		return errors.New("文件超过大小上限")
	}
	in, err := src(ctx, a.File)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(filepath.Join(dir, a.File), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(in, a.Size+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return errors.New("与签名清单中的大小或 SHA256 不一致")
	}
	return nil
}

func readAll(ctx context.Context, src mirrorSource, name string, max int64) ([]byte, error) {
	in, err := src(ctx, name)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	b, err := io.ReadAll(io.LimitReader(in, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errors.New("文件超过大小上限")
	}
	return b, nil
}

// readSigned 读取一个文件及其 .minisig，验签通过才返回。
func readSigned(ctx context.Context, keys []release.PublicKey, src mirrorSource, name string, max int64) ([]byte, []byte, bool) {
	data, err := readAll(ctx, src, name, max)
	if err != nil {
		return nil, nil, false
	}
	sig, err := readAll(ctx, src, name+".minisig", maxSignatureBytes)
	if err != nil {
		return nil, nil, false
	}
	if _, err := release.Verify(keys, data, sig); err != nil {
		return nil, nil, false
	}
	return data, sig, true
}

// SetMirrored 记录版本已完整镜像（mirroredAt 为 0 表示镜像已删除）。
func (s *Store) SetMirrored(version string, mirroredAt int64) error {
	_, err := s.DB.Exec(`UPDATE agent_releases SET mirrored_at = ? WHERE version = ?`, mirroredAt, version)
	return err
}

// pruneMirror 只保留最新的 keepMirrored 个已镜像版本，删除更早版本的文件。
func pruneMirror(st *Store, keys []release.PublicKey, root string) {
	list, err := st.ListReleases(keys)
	if err != nil {
		return
	}
	kept := 0
	for _, r := range list {
		if !r.Mirrored {
			continue
		}
		if kept++; kept > keepMirrored {
			os.RemoveAll(filepath.Join(root, "v"+r.Version))
			st.SetMirrored(r.Version, 0)
		}
	}
}

// mirrorRelease 把刚同步的版本镜像到本面板（--release-mirror）。已镜像的版本跳过。
func (s *Server) mirrorRelease(ctx context.Context, m *release.Manifest, manifest, sig []byte) error {
	if rel, _, _, err := s.releaseByVersion(m.Version); err == nil && rel.Mirrored {
		return nil
	}
	base := strings.TrimRight(s.releaseBase, "/") + "/download/v" + m.Version + "/"
	src := func(ctx context.Context, name string) (io.ReadCloser, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+name, nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.mirrorHTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("无法访问官方发布地址：%w", err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("官方发布地址返回 %d", resp.StatusCode)
		}
		return resp.Body, nil
	}
	if err := storeMirror(ctx, s.releaseKeys, s.mirrorRoot, m, manifest, sig, src); err != nil {
		return err
	}
	if err := s.store.SetMirrored(m.Version, time.Now().Unix()); err != nil {
		return err
	}
	pruneMirror(s.store, s.releaseKeys, s.mirrorRoot)
	return nil
}

// ImportRelease 从本地目录导入官方发布包（vpsmon-server release import，设计 29.1）：
// 目录中需有 manifest.json、manifest.json.minisig、安装脚本与清单列出的全部构建（即 GitHub Release 的全部文件）。
// 用编译进面板的官方公钥验签，逐个校验后记录版本并保存到镜像目录。
func ImportRelease(ctx context.Context, st *Store, root, srcDir string, now time.Time) (*release.Manifest, error) {
	return importRelease(ctx, st, release.TrustedKeys(), root, srcDir, now)
}

func importRelease(ctx context.Context, st *Store, keys []release.PublicKey, root, srcDir string, now time.Time) (*release.Manifest, error) {
	src := func(_ context.Context, name string) (io.ReadCloser, error) {
		if name != filepath.Base(name) {
			return nil, errors.New("文件名无效")
		}
		return os.Open(filepath.Join(srcDir, name))
	}
	manifest, err := readAll(ctx, src, "manifest.json", maxManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("读取 manifest.json 失败：%w", err)
	}
	sig, err := readAll(ctx, src, "manifest.json.minisig", maxSignatureBytes)
	if err != nil {
		return nil, fmt.Errorf("读取 manifest.json.minisig 失败：%w", err)
	}
	m, err := release.VerifyManifest(keys, manifest, sig)
	if err != nil {
		return nil, fmt.Errorf("官方签名校验失败：%w", err)
	}
	if err := storeMirror(ctx, keys, root, m, manifest, sig, src); err != nil {
		return nil, err
	}
	if err := st.SaveRelease(m, manifest, sig, keyIDOf(keys, sig), now); err != nil {
		return nil, err
	}
	if err := st.SetMirrored(m.Version, now.Unix()); err != nil {
		return nil, err
	}
	pruneMirror(st, keys, root)
	return m, nil
}

func keyIDOf(keys []release.PublicKey, sig []byte) string {
	parsed, err := release.ParseSignature(sig)
	if err != nil {
		return ""
	}
	for _, k := range keys {
		if k.ID == parsed.KeyID {
			return k.IDHex()
		}
	}
	return ""
}

// mirrorCache 记录已核对过 SHA256 的镜像文件（按大小与修改时间），避免每次下载都重新计算。
type mirrorCache struct {
	mu sync.Mutex
	ok map[string]fileStamp
}

type fileStamp struct {
	size  int64
	mtime time.Time
	sha   string
}

// handleMirror：GET /releases/{version}/{file}，无需凭证（设计 27.5.3）。只提供已完整镜像、且仍能验签的版本中，
// 清单列出的文件及其签名文件；构建与脚本发出前核对 SHA256。
func (s *Server) handleMirror(w http.ResponseWriter, r *http.Request) {
	notFound := func() { http.NotFound(w, r) }
	ver := r.PathValue("version")
	if !strings.HasPrefix(ver, "v") || s.mirrorRoot == "" {
		notFound()
		return
	}
	rel, manifest, sig, err := s.releaseByVersion(strings.TrimPrefix(ver, "v"))
	if err != nil || !rel.Mirrored {
		notFound()
		return
	}
	m, err := release.VerifyManifest(s.releaseKeys, manifest, sig)
	if err != nil {
		notFound()
		return
	}
	dir := filepath.Join(s.mirrorRoot, "v"+m.Version)
	file := r.PathValue("file")
	serveBytes := func(b []byte) {
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, file, time.Time{}, bytes.NewReader(b))
	}
	switch file {
	case "manifest.json":
		serveBytes(manifest)
		return
	case "manifest.json.minisig":
		serveBytes(sig)
		return
	case "SHA256SUMS", "SHA256SUMS.minisig", m.Installer.File + ".minisig":
		// 小文件：每次读取并验签
		signed := strings.TrimSuffix(file, ".minisig")
		data, err1 := os.ReadFile(filepath.Join(dir, signed))
		ssig, err2 := os.ReadFile(filepath.Join(dir, signed+".minisig"))
		if err1 != nil || err2 != nil {
			notFound()
			return
		}
		if signed == m.Installer.File {
			if s.verifyMirrorFile(filepath.Join(dir, signed), m.Installer) != nil {
				notFound()
				return
			}
		}
		if _, err := release.Verify(s.releaseKeys, data, ssig); err != nil {
			s.log.Error("mirror signature mismatch", "component", "release", "file", file)
			notFound()
			return
		}
		if file == signed {
			serveBytes(data)
		} else {
			serveBytes(ssig)
		}
		return
	}
	for _, a := range append([]release.Artifact{m.Installer}, m.Artifacts...) {
		if a.File != file {
			continue
		}
		path := filepath.Join(dir, a.File)
		if err := s.verifyMirrorFile(path, a); err != nil {
			// 【安全】文件与签名清单不一致：不发出，记录错误（设计 43.1，失败即关闭）
			s.log.Error("mirror file mismatch", "component", "release", "file", file, "err", err)
			notFound()
			return
		}
		f, err := os.Open(path)
		if err != nil {
			notFound()
			return
		}
		defer f.Close()
		fi, _ := f.Stat()
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, a.File, fi.ModTime(), f)
		return
	}
	notFound()
}

// verifyMirrorFile 核对镜像文件的大小与 SHA256；大小与修改时间未变时使用缓存的结果。
func (s *Server) verifyMirrorFile(path string, a release.Artifact) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() || fi.Size() != a.Size {
		return errors.New("大小不一致")
	}
	s.mirrorCache.mu.Lock()
	st, ok := s.mirrorCache.ok[path]
	s.mirrorCache.mu.Unlock()
	if ok && st.size == fi.Size() && st.mtime.Equal(fi.ModTime()) && st.sha == a.SHA256 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, a.Size+1)); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return errors.New("SHA256 不一致")
	}
	s.mirrorCache.mu.Lock()
	if s.mirrorCache.ok == nil {
		s.mirrorCache.ok = map[string]fileStamp{}
	}
	s.mirrorCache.ok[path] = fileStamp{size: fi.Size(), mtime: fi.ModTime(), sha: a.SHA256}
	s.mirrorCache.mu.Unlock()
	return nil
}

// mirroredVersions 返回镜像目录中实际存在的版本目录（调试与测试用）。
func mirroredVersions(root string) []string {
	entries, _ := os.ReadDir(root)
	var out []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "v") && !strings.HasSuffix(e.Name(), ".tmp") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}
