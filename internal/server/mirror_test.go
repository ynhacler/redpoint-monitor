package server

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/blake2b"

	"vpsmon/internal/release"
)

// minisig 用测试密钥生成 minisign 签名文件（与 releases_test.go 的格式相同）。
func (f *fakeReleases) minisig(msg []byte) []byte {
	h := blake2b.Sum512(msg)
	sig := ed25519.Sign(f.priv, h[:])
	global := ed25519.Sign(f.priv, append(append([]byte(nil), sig...), "t"...))
	return []byte("untrusted comment: t\n" + base64.StdEncoding.EncodeToString(append(append([]byte("ED"), f.key.ID[:]...), sig...)) +
		"\ntrusted comment: t\n" + base64.StdEncoding.EncodeToString(global) + "\n")
}

// publishFull 发布一个带真实文件的版本：清单中的大小与 SHA256 与文件一致，同时提供签名的 SHA256SUMS。
// 返回 文件名 → 内容（含清单与签名），并注册到 /latest/download/ 与 /download/v{版本}/。
func (f *fakeReleases) publishFull(version string) map[string][]byte {
	files := map[string][]byte{
		"vpsmon-agent-linux-amd64": []byte("binary-amd64-" + version),
		"vpsmon-agent-linux-arm64": []byte("binary-arm64-" + version),
		"agent-" + version + ".sh": []byte("#!/bin/sh\necho installer " + version + "\n"),
	}
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	inst := "agent-" + version + ".sh"
	m := fmt.Sprintf(`{"product":"vpsmon-agent","version":%q,"channel":"stable","released_at":"2026-10-03T00:00:00Z","min_upgradable_from":"",
"artifacts":[{"os":"linux","arch":"amd64","file":"vpsmon-agent-linux-amd64","size":%d,"sha256":%q},
{"os":"linux","arch":"arm64","file":"vpsmon-agent-linux-arm64","size":%d,"sha256":%q}],
"installer":{"file":%q,"size":%d,"sha256":%q}}`, version,
		len(files["vpsmon-agent-linux-amd64"]), sum(files["vpsmon-agent-linux-amd64"]),
		len(files["vpsmon-agent-linux-arm64"]), sum(files["vpsmon-agent-linux-arm64"]),
		inst, len(files[inst]), sum(files[inst]))
	files["manifest.json"] = []byte(m)
	files["manifest.json.minisig"] = f.minisig([]byte(m))
	sums := ""
	for _, n := range []string{"vpsmon-agent-linux-amd64", "vpsmon-agent-linux-arm64", inst} {
		sums += sum(files[n]) + "  " + n + "\n"
	}
	files["SHA256SUMS"] = []byte(sums)
	files["SHA256SUMS.minisig"] = f.minisig([]byte(sums))
	files[inst+".minisig"] = f.minisig(files[inst])
	for n, b := range files {
		f.files["/download/v"+version+"/"+n] = b
	}
	f.files["/latest/download/manifest.json"] = files["manifest.json"]
	f.files["/latest/download/manifest.json.minisig"] = files["manifest.json.minisig"]
	return files
}

func mirrorServer(t *testing.T) (*Server, *fakeReleases, string) {
	s, _, _ := testServer(t)
	f := newFakeReleases(t)
	s.releaseBase, s.releaseKeys = f.srv.URL, []release.PublicKey{f.key}
	s.mirrorRoot, s.releaseMirror, s.mirrorHTTP = filepath.Join(t.TempDir(), "releases"), true, f.srv.Client()
	return s, f, adminToken(t, s)
}

// 同步并镜像 → 安装命令改为从本面板下载 → /releases 提供文件 → 远程升级任务带 mirror_path（设计 27.5.3）。
func TestReleaseMirror(t *testing.T) {
	s, f, admin := mirrorServer(t)
	h := s.routes()
	files := f.publishFull("0.3.0")
	if rec := do(h, "POST", "/api/v1/agent-releases/sync", admin, nil); rec.Code != 200 {
		t.Fatalf("同步：%d %s", rec.Code, rec.Body)
	}
	if rel := s.latestStable(); rel == nil || !rel.Mirrored {
		t.Fatalf("同步后应已镜像：%+v", rel)
	}

	// 安装命令：脚本与构建都从本面板下载，哈希仍来自已验签的清单
	_, v, _ := createNode(t, h, admin, `{"name":"hk-1"}`)
	cmd := v.Install.Command
	if !strings.Contains(cmd, "/releases/v0.3.0/agent-0.3.0.sh") || !strings.HasSuffix(cmd, "--mirror "+v.Install.Server+"/releases") ||
		strings.Contains(cmd, f.srv.URL) {
		t.Errorf("已镜像时应从本面板下载：%s", cmd)
	}

	// 无需凭证即可下载清单列出的文件及签名文件
	for _, n := range []string{"vpsmon-agent-linux-amd64", "agent-0.3.0.sh", "manifest.json", "manifest.json.minisig",
		"SHA256SUMS", "SHA256SUMS.minisig", "agent-0.3.0.sh.minisig"} {
		rec := do(h, "GET", "/releases/v0.3.0/"+n, "", nil)
		if rec.Code != 200 || rec.Body.String() != string(files[n]) {
			t.Errorf("%s：%d", n, rec.Code)
		}
	}
	// 不在清单中的文件、未镜像的版本、路径穿越：404
	for _, p := range []string{"/releases/v0.3.0/other", "/releases/v0.2.0/manifest.json", "/releases/0.3.0/manifest.json",
		"/releases/v0.3.0/..%2fmanifest.json"} {
		if rec := do(h, "GET", p, "", nil); rec.Code != 404 {
			t.Errorf("%s 应 404：%d", p, rec.Code)
		}
	}

	// 【安全】磁盘上的构建被替换：大小相同但内容不同，也不会被发出
	path := filepath.Join(s.mirrorRoot, "v0.3.0", "vpsmon-agent-linux-arm64")
	bad := []byte(strings.Repeat("x", len(files["vpsmon-agent-linux-arm64"])))
	os.WriteFile(path, bad, 0o644)
	os.Chtimes(path, time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	if rec := do(h, "GET", "/releases/v0.3.0/vpsmon-agent-linux-arm64", "", nil); rec.Code != 404 {
		t.Errorf("被篡改的构建不应发出：%d", rec.Code)
	}
	// 被篡改的 SHA256SUMS 签名不符：不发出
	os.WriteFile(filepath.Join(s.mirrorRoot, "v0.3.0", "SHA256SUMS"), []byte("evil"), 0o644)
	if rec := do(h, "GET", "/releases/v0.3.0/SHA256SUMS", "", nil); rec.Code != 404 {
		t.Errorf("签名不符的 SHA256SUMS 不应发出：%d", rec.Code)
	}

	// 远程升级：已镜像的版本带 mirror_path
	_, res := enroll(h, v.EnrollCode, "hk-1", "m1")
	do(h, "POST", "/api/v1/agent/report", res.AgentToken, []byte(`{"agent_version":"0.2.0","system":{"boot_id":"b"}}`))
	if rec := do(h, "POST", "/api/v1/upgrade-tasks", admin, []byte(`{"server_ids":[`+itoa(v.ServerID)+`],"version":"0.3.0"}`)); rec.Code != 201 {
		t.Fatalf("创建任务：%d %s", rec.Code, rec.Body)
	}
	var task map[string]any
	json.Unmarshal(do(h, "GET", "/api/v1/agent/upgrade", res.AgentToken, nil).Body.Bytes(), &task)
	if task["mirror_path"] != "/releases" {
		t.Errorf("已镜像的版本应让 Agent 从本面板下载：%v", task)
	}
}

// 镜像失败（官方地址上的构建与清单不符）：版本照常记录，但不标记为已镜像，安装命令仍指向官方地址。
func TestReleaseMirrorRejectsMismatch(t *testing.T) {
	s, f, admin := mirrorServer(t)
	h := s.routes()
	f.publishFull("0.3.0")
	f.files["/download/v0.3.0/vpsmon-agent-linux-amd64"] = []byte("tampered-amd64-0.3.0")
	rec := do(h, "POST", "/api/v1/agent-releases/sync", admin, nil)
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "镜像失败") {
		t.Fatalf("镜像失败应报告：%d %s", rec.Code, rec.Body)
	}
	rel := s.latestStable()
	if rel == nil || rel.Mirrored {
		t.Fatalf("清单应记录，但不应标记为已镜像：%+v", rel)
	}
	if got := mirroredVersions(s.mirrorRoot); len(got) != 0 {
		t.Errorf("校验失败时不应留下文件：%v", got)
	}
	_, v, _ := createNode(t, h, admin, `{"name":"hk-1"}`)
	if !strings.Contains(v.Install.Command, f.srv.URL) {
		t.Errorf("未镜像时安装命令应指向官方地址：%s", v.Install.Command)
	}
}

// 离线导入：目录中是 GitHub Release 的全部文件；验签并逐个校验，缺文件或被改动时拒绝（设计 29.1）。
func TestImportRelease(t *testing.T) {
	s, f, _ := mirrorServer(t)
	files := f.publishFull("0.4.0")
	write := func() string {
		dir := t.TempDir()
		for n, b := range files {
			os.WriteFile(filepath.Join(dir, n), b, 0o644)
		}
		return dir
	}
	keys := []release.PublicKey{f.key}

	dir := write()
	os.WriteFile(filepath.Join(dir, "vpsmon-agent-linux-arm64"), []byte("evil"), 0o644)
	if _, err := importRelease(t.Context(), s.store, keys, s.mirrorRoot, dir, time.Now()); err == nil {
		t.Error("被改动的构建应拒绝导入")
	}
	dir = write()
	os.Remove(filepath.Join(dir, "vpsmon-agent-linux-amd64"))
	if _, err := importRelease(t.Context(), s.store, keys, s.mirrorRoot, dir, time.Now()); err == nil {
		t.Error("缺少构建应拒绝导入")
	}
	dir = write()
	os.WriteFile(filepath.Join(dir, "manifest.json.minisig"), []byte("bad"), 0o644)
	if _, err := importRelease(t.Context(), s.store, keys, s.mirrorRoot, dir, time.Now()); err == nil {
		t.Error("签名无效应拒绝导入")
	}
	if s.latestStable() != nil {
		t.Fatal("导入失败时不应记录版本")
	}

	m, err := importRelease(t.Context(), s.store, keys, s.mirrorRoot, write(), time.Now())
	if err != nil || m.Version != "0.4.0" {
		t.Fatalf("导入：%v", err)
	}
	if rel := s.latestStable(); rel == nil || !rel.Mirrored || rel.Version != "0.4.0" {
		t.Fatalf("导入后应记录并标记已镜像：%+v", rel)
	}
	if rec := do(s.routes(), "GET", "/releases/v0.4.0/vpsmon-agent-linux-amd64", "", nil); rec.Code != 200 {
		t.Errorf("导入的构建应可下载：%d", rec.Code)
	}
}

// 镜像只保留最新的几个版本。
func TestMirrorPrune(t *testing.T) {
	s, f, _ := mirrorServer(t)
	keys := []release.PublicKey{f.key}
	for _, v := range []string{"0.1.0", "0.2.0", "0.3.0", "0.4.0"} {
		files := f.publishFull(v)
		dir := t.TempDir()
		for n, b := range files {
			os.WriteFile(filepath.Join(dir, n), b, 0o644)
		}
		if _, err := importRelease(t.Context(), s.store, keys, s.mirrorRoot, dir, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(mirroredVersions(s.mirrorRoot), ","); got != "v0.2.0,v0.3.0,v0.4.0" {
		t.Errorf("应保留最新 %d 个版本：%s", keepMirrored, got)
	}
	if rec := do(s.routes(), "GET", "/releases/v0.1.0/manifest.json", "", nil); rec.Code != 404 {
		t.Errorf("已清理的版本不应再提供：%d", rec.Code)
	}
}
