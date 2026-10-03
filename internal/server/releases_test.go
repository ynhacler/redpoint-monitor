package server

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/crypto/blake2b"

	"vpsmon/internal/release"
)

// fakeReleases 是一个假的官方发布地址，清单用内存中的测试密钥签名（仓库中不保存私钥）。
type fakeReleases struct {
	srv   *httptest.Server
	key   release.PublicKey
	priv  ed25519.PrivateKey
	files map[string][]byte
}

func newFakeReleases(t *testing.T) *fakeReleases {
	f := &fakeReleases{files: map[string][]byte{}}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	f.key.Key, f.priv = pub, priv
	rand.Read(f.key.ID[:])
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b, ok := f.files[r.URL.Path]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

const releaseSHA = "1111111111111111111111111111111111111111111111111111111111111111"

func (f *fakeReleases) publish(version, channel string) []byte {
	m := []byte(`{"product":"vpsmon-agent","version":"` + version + `","channel":"` + channel + `","released_at":"2026-10-03T00:00:00Z",
"min_upgradable_from":"","artifacts":[{"os":"linux","arch":"amd64","file":"vpsmon-agent-linux-amd64","size":9,"sha256":"` + releaseSHA + `"}],
"installer":{"file":"agent-` + version + `.sh","size":5,"sha256":"` + releaseSHA + `"}}`)
	h := blake2b.Sum512(m)
	sig := ed25519.Sign(f.priv, h[:])
	global := ed25519.Sign(f.priv, append(append([]byte(nil), sig...), "t"...))
	f.files["/latest/download/manifest.json"] = m
	f.files["/latest/download/manifest.json.minisig"] = []byte("untrusted comment: t\n" +
		base64.StdEncoding.EncodeToString(append(append([]byte("ED"), f.key.ID[:]...), sig...)) +
		"\ntrusted comment: t\n" + base64.StdEncoding.EncodeToString(global) + "\n")
	return m
}

func TestReleaseSyncAndInstallCommand(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	f := newFakeReleases(t)
	s.releaseBase, s.releaseKeys = f.srv.URL, []release.PublicKey{f.key}

	// 未同步：只有手动命令
	_, v, _ := createNode(t, h, admin, `{"name":"hk-1"}`)
	if v.Install.Mode != "manual" || v.Install.Release != nil || !strings.HasPrefix(v.Install.Command, "sudo vpsmon-agent install") {
		t.Fatalf("未同步时应为手动命令：%+v", v.Install)
	}

	// 同步官方版本
	f.publish("0.2.0", "stable")
	rec := do(h, "POST", "/api/v1/agent-releases/sync", admin, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"0.2.0"`) {
		t.Fatalf("同步：%d %s", rec.Code, rec.Body)
	}
	var list struct {
		Items []agentRelease `json:"items"`
	}
	json.Unmarshal(do(h, "GET", "/api/v1/agent-releases", admin, nil).Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].KeyID != f.key.IDHex() || list.Items[0].InstallerSHA256 != releaseSHA {
		t.Fatalf("版本列表：%+v", list.Items)
	}

	// 默认命令：下载按版本固定的脚本 → 按已验签的 SHA256 校验 → 执行；不使用管道
	rec = do(h, "GET", "/api/v1/servers/"+itoa(v.ServerID)+"/install-command", admin, nil)
	var ic enrollCodeView
	json.Unmarshal(rec.Body.Bytes(), &ic)
	want := "curl -fsSLo agent.sh " + f.srv.URL + "/download/v0.2.0/agent-0.2.0.sh && echo \"" + releaseSHA +
		"  agent.sh\" | sha256sum -c - && sudo sh agent.sh --server "
	if ic.Install.Mode != "default" || !strings.HasPrefix(ic.Install.Command, want) || !strings.Contains(ic.Install.Command, " --enroll ENR-") {
		t.Fatalf("默认命令：%q", ic.Install.Command)
	}
	if regexp.MustCompile(`\|\s*(sudo\s+)?(ba)?sh\b`).MatchString(ic.Install.Command) {
		t.Fatal("【安全】安装命令不得使用管道执行")
	}
	if !strings.HasPrefix(ic.Install.ManualCommand, "sudo vpsmon-agent install") {
		t.Error("默认命令之外仍应提供手动命令")
	}

	// 篡改的清单：验签失败，不记录，接口 503
	f.files["/latest/download/manifest.json"] = bytes.Replace(f.publish("0.3.0", "stable"), []byte(releaseSHA), []byte(strings.Repeat("2", 64)), 1)
	if rec := do(h, "POST", "/api/v1/agent-releases/sync", admin, nil); rec.Code != 503 || !strings.Contains(rec.Body.String(), "验签失败") {
		t.Errorf("篡改的清单应拒绝：%d %s", rec.Code, rec.Body)
	}
	json.Unmarshal(do(h, "GET", "/api/v1/agent-releases", admin, nil).Body.Bytes(), &list)
	if len(list.Items) != 1 {
		t.Errorf("验签失败的版本不应记录：%+v", list.Items)
	}

	// 数据库中的清单被改动：读取时重新验签，不再使用
	s.store.DB.Exec(`UPDATE agent_releases SET manifest = replace(manifest, '` + releaseSHA + `', '` + strings.Repeat("3", 64) + `')`)
	if s.latestStable() != nil {
		t.Error("数据库中被改动的清单不应被使用")
	}

	// beta 版本不用于安装命令
	s.store.DB.Exec(`DELETE FROM agent_releases`)
	f.publish("0.4.0-rc.1", "beta")
	do(h, "POST", "/api/v1/agent-releases/sync", admin, nil)
	if s.latestStable() != nil {
		t.Error("beta 版本不应用于安装命令")
	}
	// 同步记入操作日志
	var logs struct{ Items []AuditLog }
	json.Unmarshal(do(h, "GET", "/api/v1/audit-logs?category=operation", admin, nil).Body.Bytes(), &logs)
	n := 0
	for _, l := range logs.Items {
		if l.Action == "release.sync" {
			n++
		}
	}
	if n != 3 {
		t.Errorf("每次同步（含失败）都应记录审计：%d", n)
	}
}
