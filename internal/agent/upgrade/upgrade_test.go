package upgrade

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/blake2b"

	"vpsmon/internal/release"
)

// fixture 是一个假的发布镜像：清单、签名与“二进制”（打印版本号的 shell 脚本）。
type fixture struct {
	srv     *httptest.Server
	files   map[string][]byte
	pub     release.PublicKey
	priv    ed25519.PrivateKey
	bin     string // 当前安装的程序
	state   string
	restart int
	status  func() (string, int64, error)
	now     time.Time
}

func script(version string) []byte { return []byte("#!/bin/sh\necho " + version + "\n") }

func newFixture(t *testing.T) *fixture {
	if runtime.GOOS == "windows" {
		t.Skip("需要 POSIX shell")
	}
	f := &fixture{files: map[string][]byte{}, now: time.Unix(2_000_000_000, 0)}
	var err error
	f.pub.Key, f.priv, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rand.Read(f.pub.ID[:])
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := f.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(f.srv.Close)
	dir := t.TempDir()
	f.bin, f.state = filepath.Join(dir, "vpsmon-agent"), filepath.Join(dir, "state")
	os.WriteFile(f.bin, script("0.2.0"), 0o755)
	return f
}

func (f *fixture) sign(msg []byte) []byte {
	h := blake2b.Sum512(msg)
	sig := ed25519.Sign(f.priv, h[:])
	trusted := "test"
	global := ed25519.Sign(f.priv, append(append([]byte(nil), sig...), trusted...))
	return []byte("untrusted comment: t\n" + base64.StdEncoding.EncodeToString(append(append([]byte("ED"), f.pub.ID[:]...), sig...)) +
		"\ntrusted comment: " + trusted + "\n" + base64.StdEncoding.EncodeToString(global) + "\n")
}

// publish 发布一个版本；binary 为 nil 时用打印该版本号的脚本，min 为最低可升级版本。
func (f *fixture) publish(version string, binary []byte, min string) {
	if binary == nil {
		binary = script(version)
	}
	name := "vpsmon-agent-" + runtime.GOOS + "-" + ArchLabel()
	sum := sha256.Sum256(binary)
	inst := []byte("installer")
	isum := sha256.Sum256(inst)
	m := fmt.Sprintf(`{"product":"vpsmon-agent","version":%q,"channel":"stable","released_at":"2026-10-03T00:00:00Z","min_upgradable_from":%q,
"artifacts":[{"os":%q,"arch":%q,"file":%q,"size":%d,"sha256":%q}],"installer":{"file":"agent.sh","size":%d,"sha256":%q}}`,
		version, min, runtime.GOOS, ArchLabel(), name, len(binary), hex.EncodeToString(sum[:]), len(inst), hex.EncodeToString(isum[:]))
	p := "/v" + version + "/"
	f.files[p+"manifest.json"] = []byte(m)
	f.files[p+"manifest.json.minisig"] = f.sign([]byte(m))
	f.files[p+name] = binary
}

func (f *fixture) run(target string, mod func(*Options)) (string, error) {
	var out bytes.Buffer
	o := Options{Current: "0.2.0", Target: target, Mirror: f.srv.URL, Keys: []release.PublicKey{f.pub},
		Bin: f.bin, StateDir: f.state, Out: &out, HealthTimeout: 10 * time.Second,
		Restart: func() error { f.restart++; return nil },
		ReadStatus: func() (string, int64, error) {
			if f.status != nil {
				return f.status()
			}
			return "", 0, os.ErrNotExist
		},
		Now: func() time.Time { return f.now }, Sleep: func(d time.Duration) { f.now = f.now.Add(d) }}
	if mod != nil {
		mod(&o)
	}
	err := Run(context.Background(), o)
	return out.String(), err
}

func (f *fixture) installed() string { b, _ := os.ReadFile(f.bin); return string(b) }

func TestUpgradeSuccess(t *testing.T) {
	f := newFixture(t)
	f.publish("0.3.0", nil, "0.1.0")
	f.status = func() (string, int64, error) { return "v0.3.0", f.now.Unix(), nil } // 新版本重启后成功上报
	out, err := f.run("0.3.0", nil)
	if err != nil {
		t.Fatalf("升级应成功：%v\n%s", err, out)
	}
	if f.installed() != string(script("0.3.0")) || f.restart != 1 {
		t.Fatalf("应替换为新版本并重启一次：%q restart=%d", f.installed(), f.restart)
	}
	if b, _ := os.ReadFile(filepath.Join(f.state, "backup", "vpsmon-agent-0.2.0")); string(b) != string(script("0.2.0")) {
		t.Error("应备份旧版本")
	}
	if _, err := os.Stat(f.bin + ".new"); !os.IsNotExist(err) {
		t.Error("不应留下临时文件")
	}
	for _, s := range []string{"签名有效", "SHA256 校验通过", "可以在本机运行", "已升级到 0.3.0"} {
		if !strings.Contains(out, s) {
			t.Errorf("输出缺少 %q：\n%s", s, out)
		}
	}
}

func TestUpgradeHealthCheckRollback(t *testing.T) {
	f := newFixture(t)
	f.publish("0.3.0", nil, "")
	// 新版本重启后一直没有成功上报：60 秒（此处 10 秒）后回滚
	_, err := f.run("0.3.0", nil)
	if _, ok := err.(*RollbackError); !ok {
		t.Fatalf("健康检查超时应回滚：%v", err)
	}
	if f.installed() != string(script("0.2.0")) || f.restart != 2 {
		t.Fatalf("应恢复旧版本并再次重启：%q restart=%d", f.installed(), f.restart)
	}
	// 旧版本的上报不算新版本健康
	f2 := newFixture(t)
	f2.publish("0.3.0", nil, "")
	f2.status = func() (string, int64, error) { return "0.2.0", f2.now.Unix(), nil }
	if _, err := f2.run("0.3.0", nil); err == nil {
		t.Error("仍是旧版本在上报时不应判定成功")
	}
}

func TestUpgradeRejections(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(f *fixture)
		target string
		mod    func(*Options)
		want   string
	}{
		{"二进制被篡改", func(f *fixture) {
			f.publish("0.3.0", nil, "")
			f.files["/v0.3.0/vpsmon-agent-"+runtime.GOOS+"-"+ArchLabel()] = script("9.9.9") // 同长度，内容不同
		}, "0.3.0", nil, "SHA256 校验失败"},
		{"二进制多出内容", func(f *fixture) {
			f.publish("0.3.0", nil, "")
			p := "/v0.3.0/vpsmon-agent-" + runtime.GOOS + "-" + ArchLabel()
			f.files[p] = append(f.files[p], '\n')
		}, "0.3.0", nil, "大小不符"},
		{"清单被篡改", func(f *fixture) {
			f.publish("0.3.0", nil, "")
			f.files["/v0.3.0/manifest.json"] = bytes.Replace(f.files["/v0.3.0/manifest.json"], []byte("stable"), []byte("beta!!"), 1)
		}, "0.3.0", nil, "签名校验失败"},
		{"不是官方密钥", func(f *fixture) { f.publish("0.3.0", nil, "") }, "0.3.0",
			func(o *Options) { o.Keys = []release.PublicKey{{ID: [8]byte{1}, Key: make(ed25519.PublicKey, 32)}} }, "官方密钥"},
		{"开发构建没有公钥", func(f *fixture) { f.publish("0.3.0", nil, "") }, "0.3.0",
			func(o *Options) { o.Keys = nil }, "没有官方发布公钥"},
		{"拒绝降级", func(f *fixture) { f.publish("0.1.0", nil, "") }, "0.1.0", nil, "拒绝降级"},
		{"低于最低可升级版本", func(f *fixture) { f.publish("1.0.0", nil, "0.9.0") }, "1.0.0", nil, "低于该版本要求"},
		{"自报版本不一致", func(f *fixture) { f.publish("0.3.0", script("0.4.0"), "") }, "0.3.0", nil, "自报版本"},
		{"清单版本与请求不符", func(f *fixture) {
			f.publish("0.4.0", nil, "")
			f.files["/v0.3.0/manifest.json"] = f.files["/v0.4.0/manifest.json"]
			f.files["/v0.3.0/manifest.json.minisig"] = f.files["/v0.4.0/manifest.json.minisig"]
		}, "0.3.0", nil, "不一致"},
		{"版本不存在", func(f *fixture) {}, "0.3.0", nil, "下载发布清单失败"},
	}
	for _, c := range cases {
		f := newFixture(t)
		c.setup(f)
		_, err := f.run(c.target, c.mod)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s：%v，应包含 %q", c.name, err, c.want)
		}
		if f.installed() != string(script("0.2.0")) || f.restart != 0 {
			t.Errorf("%s：被拒绝时不应改动已安装的程序或重启", c.name)
		}
	}
}

func TestUpgradeAllowDowngradeLocally(t *testing.T) {
	f := newFixture(t)
	f.publish("0.1.0", nil, "")
	f.status = func() (string, int64, error) { return "0.1.0", f.now.Unix(), nil }
	if _, err := f.run("0.1.0", func(o *Options) { o.AllowDowngrade = true }); err != nil {
		t.Fatalf("本机显式允许时可以降级：%v", err)
	}
}

func TestManifestURLs(t *testing.T) {
	cases := []struct {
		target, mirror, want string
	}{
		{"", "", OfficialReleases + "/latest/download/manifest.json"},
		{"0.3.0", "", OfficialReleases + "/download/v0.3.0/manifest.json"},
		{"v0.3.0", "https://m.example.com/releases/", "https://m.example.com/releases/v0.3.0/manifest.json"},
	}
	for _, c := range cases {
		o := Options{Target: c.target, Mirror: c.mirror}
		o.defaults()
		if got, _ := o.manifestURLs(); got != c.want {
			t.Errorf("%+v → %s，应为 %s", c, got, c.want)
		}
	}
}
