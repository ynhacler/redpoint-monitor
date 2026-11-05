package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vpsmon/internal/release"
)

// fakePanel 模拟面板的 /api/v1/agent/upgrade 与 /status，记录上报的状态。
type fakePanel struct {
	mu          sync.Mutex
	task        map[string]any // GET 的响应；nil 表示没有任务
	statuses    []string
	mirror      map[string][]byte // 面板镜像 /releases/... 提供的文件
	mirrorProxy string            // 非空时 /releases/... 转给这个地址（用于计数）
	srv         *httptest.Server
}

func newFakePanel(t *testing.T) *fakePanel {
	p := &fakePanel{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.mirrorProxy != "" && strings.HasPrefix(r.URL.Path, "/releases/") {
			resp, err := http.Get(p.mirrorProxy + r.URL.Path)
			if err != nil {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			io.Copy(w, resp.Body)
			return
		}
		if b, ok := p.mirror[r.URL.Path]; ok { // 镜像无需凭证
			w.Write(b)
			return
		}
		if r.Header.Get("Authorization") != "Bearer agt_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/agent/upgrade":
			if p.task == nil {
				json.NewEncoder(w).Encode(map[string]any{"upgrade": false})
				return
			}
			json.NewEncoder(w).Encode(p.task)
		case "/api/v1/agent/upgrade/status":
			var b struct {
				TaskID int64  `json:"task_id"`
				Status string `json:"status"`
				Reason string `json:"reason"`
			}
			json.NewDecoder(r.Body).Decode(&b)
			p.statuses = append(p.statuses, b.Status+":"+b.Reason)
			if b.Status != "staged" {
				p.task = nil // 任务结束
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

// remoteFixture 发布 0.3.0，并让面板下发升级到该版本的任务。
func remoteFixture(t *testing.T) (*fixture, *fakePanel, RemoteOptions) {
	f := newFixture(t)
	f.publish("0.3.0", nil, "0.1.0")
	for k, v := range f.files { // 构建按官方 Releases 的路径下载：/download/v0.3.0/<file>
		f.files["/download"+k] = v
	}
	panel := newFakePanel(t)
	panel.task = map[string]any{"upgrade": true, "task_id": 7, "version": "0.3.0",
		"manifest": f.files["/v0.3.0/manifest.json"], "manifest_signature": string(f.files["/v0.3.0/manifest.json.minisig"])}
	o := RemoteOptions{Server: panel.srv.URL, Token: "agt_test", Current: "0.2.0", Keys: []release.PublicKey{f.pub},
		StageDir: filepath.Join(filepath.Dir(f.bin), "update"), Downloads: f.srv.URL}
	return f, panel, o
}

func (f *fixture) updater(o RemoteOptions, mod func(*UpdaterOptions)) error {
	u := UpdaterOptions{StageDir: o.StageDir, WorkDir: filepath.Join(filepath.Dir(f.bin), "root"), StateDir: f.state,
		Bin: f.bin, Keys: o.Keys, HealthTimeout: 10 * time.Second,
		Restart: func() error { f.restart++; return nil },
		ReadStatus: func() (string, int64, error) {
			if f.status != nil {
				return f.status()
			}
			return "", 0, os.ErrNotExist
		},
		Now: func() time.Time { return f.now }, Sleep: func(d time.Duration) { f.now = f.now.Add(d) }}
	if mod != nil {
		mod(&u)
	}
	return RunUpdater(context.Background(), u)
}

// 完整流程：Agent 暂存 → updater 复验并替换 → Agent 上报结果（设计 29.13）。
func TestRemoteUpgradeFlow(t *testing.T) {
	f, panel, o := remoteFixture(t)
	if err := CheckRemote(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(o.StageDir, "request.json")); err != nil {
		t.Fatal("应写入 request.json")
	}
	if f.installed() != string(script("0.2.0")) {
		t.Fatal("Agent 自身不能替换程序")
	}
	// 同一任务再次查询不应重复下载
	if err := CheckRemote(context.Background(), o); err != nil {
		t.Fatal(err)
	}

	f.status = func() (string, int64, error) { return "0.3.0", f.now.Unix(), nil }
	if err := f.updater(o, nil); err != nil {
		t.Fatal(err)
	}
	if f.installed() != string(script("0.3.0")) || f.restart != 1 {
		t.Fatalf("updater 应替换并重启：%q restart=%d", f.installed(), f.restart)
	}
	if _, err := os.Stat(filepath.Join(o.StageDir, "request.json")); err == nil {
		t.Error("updater 应删除请求")
	}

	o.Current = "0.3.0"
	if err := CheckRemote(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(panel.statuses, ","); got != "staged:,success:" {
		t.Errorf("上报的状态：%s", got)
	}
	if _, err := os.Stat(filepath.Join(o.StageDir, "result.json")); err == nil {
		t.Error("结果送达后应删除 result.json")
	}
}

// 面板已镜像：从 Agent 配置的面板地址 + mirror_path 下载；指向其他主机的路径被忽略（设计 27.5.3）。
func TestRemoteUpgradeFromMirror(t *testing.T) {
	f, panel, o := remoteFixture(t)
	name := "vpsmon-agent-" + runtime.GOOS + "-" + ArchLabel()
	panel.mirror = map[string][]byte{"/releases/v0.3.0/" + name: f.files["/v0.3.0/"+name]}
	panel.task["mirror_path"] = "/releases"
	o.Downloads = "http://127.0.0.1:1" // 官方地址不可达：必须从镜像下载
	if err := CheckRemote(context.Background(), o); err != nil {
		t.Fatalf("应从面板镜像下载：%v", err)
	}
	if _, err := os.Stat(filepath.Join(o.StageDir, name)); err != nil {
		t.Error("应暂存从镜像下载的构建")
	}

	// 协议相对路径（//evil.example）不被当作镜像，仍走官方地址（此处不可达而失败）
	f2, panel2, o2 := remoteFixture(t)
	_ = f2
	panel2.task["mirror_path"] = "//evil.example/releases"
	o2.Downloads = "http://127.0.0.1:1"
	if err := CheckRemote(context.Background(), o2); err == nil {
		t.Error("指向其他主机的镜像路径应被忽略")
	}
}

// 官方地址优先，失败 2 次后改从面板镜像下载；校验不通过也算失败，镜像的内容同样要校验（设计 27.5.3）。
func TestRemoteUpgradeFallback(t *testing.T) {
	name := "vpsmon-agent-" + runtime.GOOS + "-" + ArchLabel()
	cases := []struct {
		name         string
		official     func(good []byte, w http.ResponseWriter) // 官方地址的响应
		panelBad     bool                                     // 镜像返回被篡改的内容
		wantOfficial int
		wantPanel    int
		wantStaged   bool
	}{
		{"官方可用时不碰面板", func(g []byte, w http.ResponseWriter) { w.Write(g) }, false, 1, 0, true},
		{"官方失败 2 次后用面板", func(_ []byte, w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) }, false, 2, 1, true},
		{"官方内容被篡改时用面板", func(g []byte, w http.ResponseWriter) { w.Write(bytes.Repeat([]byte("x"), len(g))) }, false, 2, 1, true},
		{"面板内容被篡改时失败", func(_ []byte, w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) }, true, 2, 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, panel, o := remoteFixture(t)
			good := f.files["/v0.3.0/"+name]
			var official, mirrored atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				official.Add(1)
				c.official(good, w)
			}))
			t.Cleanup(srv.Close)
			o.Downloads = srv.URL
			mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mirrored.Add(1)
				if c.panelBad {
					w.Write(bytes.Repeat([]byte("y"), len(good)))
					return
				}
				w.Write(good)
			}))
			t.Cleanup(mirror.Close)
			// 镜像路径拼在面板地址后：把面板上的这条路径转给上面的计数服务
			panel.task["mirror_path"] = "/releases"
			panel.mirrorProxy = mirror.URL
			err := CheckRemote(context.Background(), o)
			if (err == nil) != c.wantStaged {
				t.Fatalf("CheckRemote：%v", err)
			}
			if int(official.Load()) != c.wantOfficial || int(mirrored.Load()) != c.wantPanel {
				t.Errorf("官方 %d 次、面板 %d 次，应为 %d、%d", official.Load(), mirrored.Load(), c.wantOfficial, c.wantPanel)
			}
			if _, err := os.Stat(filepath.Join(o.StageDir, name)); (err == nil) != c.wantStaged {
				t.Errorf("暂存结果不符：%v", err)
			}
		})
	}
}

// 官方地址卡住时，单次超时后换面板，而不是等到 HTTP 客户端的总超时。
func TestRemoteUpgradeOfficialHangs(t *testing.T) {
	old := officialAttemptTimeout
	officialAttemptTimeout = 50 * time.Millisecond
	t.Cleanup(func() { officialAttemptTimeout = old })
	f, panel, o := remoteFixture(t)
	name := "vpsmon-agent-" + runtime.GOOS + "-" + ArchLabel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	o.Downloads = srv.URL
	panel.mirror = map[string][]byte{"/releases/v0.3.0/" + name: f.files["/v0.3.0/"+name]}
	panel.task["mirror_path"] = "/releases"
	if err := CheckRemote(context.Background(), o); err != nil {
		t.Fatalf("应改从面板下载：%v", err)
	}
}

// 【安全】updater 独立复验：暂存目录中的内容被篡改时拒绝安装（设计 29.13）。
func TestUpdaterRejectsTamperedStage(t *testing.T) {
	cases := map[string]func(stage string){
		"替换构建": func(stage string) {
			name := filepath.Join(stage, "vpsmon-agent-"+runtime.GOOS+"-"+ArchLabel())
			os.WriteFile(name, script("6.6.6"), 0o644)
		},
		"伪造清单": func(stage string) {
			os.WriteFile(filepath.Join(stage, "manifest.json"), []byte(`{"product":"vpsmon-agent","version":"9.9.9"}`), 0o644)
		},
		"请求降级": func(stage string) {
			os.WriteFile(filepath.Join(stage, "request.json"), []byte(`{"task_id":7,"version":"0.1.0"}`), 0o644)
		},
		"符号链接": func(stage string) {
			name := filepath.Join(stage, "manifest.json")
			os.Rename(name, name+".real")
			os.Symlink(name+".real", name)
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			f, _, o := remoteFixture(t)
			if err := CheckRemote(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			tamper(o.StageDir)
			if err := f.updater(o, nil); err != nil {
				t.Fatal(err)
			}
			if f.installed() != string(script("0.2.0")) || f.restart != 0 {
				t.Fatalf("不应替换程序：%q", f.installed())
			}
			r, ok := readResult(o.StageDir)
			if !ok || r.Status != "failed" {
				t.Errorf("应写下失败结果：%+v", r)
			}
		})
	}
}

func TestUpdaterDisabledAndStageSymlink(t *testing.T) {
	f, _, o := remoteFixture(t)
	if err := CheckRemote(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	disable := filepath.Join(filepath.Dir(f.bin), "no-remote-upgrade")
	os.WriteFile(disable, nil, 0o644)
	if err := f.updater(o, func(u *UpdaterOptions) { u.DisableFile = disable }); err != nil {
		t.Fatal(err)
	}
	if r, _ := readResult(o.StageDir); r.Status != "failed" || !strings.Contains(r.Reason, "禁止远程升级") || f.installed() != string(script("0.2.0")) {
		t.Errorf("禁止远程升级时应拒绝：%+v", r)
	}

	// 【安全】暂存目录被换成符号链接时，root updater 不在链接目标中删除或写入任何文件
	target := t.TempDir()
	keep := filepath.Join(target, "manifest.json")
	os.WriteFile(keep, []byte("keep"), 0o644)
	os.WriteFile(filepath.Join(target, "request.json"), []byte(`{"task_id":7,"version":"0.3.0"}`), 0o644)
	os.RemoveAll(o.StageDir)
	os.Symlink(target, o.StageDir)
	if err := f.updater(o, nil); err == nil {
		t.Error("暂存目录是符号链接时应拒绝")
	}
	if b, _ := os.ReadFile(keep); string(b) != "keep" {
		t.Error("不得删除链接目标中的文件")
	}
	if _, err := os.Stat(filepath.Join(target, "result.json")); err == nil {
		t.Error("不得在链接目标中写入文件")
	}
}

// 面板转交的清单签名无效、版本不一致或不是升级时，Agent 不暂存，并上报失败。
func TestCheckRemoteRejects(t *testing.T) {
	cases := map[string]func(task map[string]any, o *RemoteOptions){
		"签名无效":  func(task map[string]any, _ *RemoteOptions) { task["manifest_signature"] = "bad" },
		"版本不一致": func(task map[string]any, _ *RemoteOptions) { task["version"] = "0.4.0" },
		"降级":    func(_ map[string]any, o *RemoteOptions) { o.Current = "0.4.0" }, // 【安全】拒绝降级
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			_, panel, o := remoteFixture(t)
			mod(panel.task, &o)
			if err := CheckRemote(context.Background(), o); err == nil {
				t.Fatal("应失败")
			}
			if _, err := os.Stat(filepath.Join(o.StageDir, "request.json")); err == nil {
				t.Error("失败时不应写入 request.json")
			}
			if len(panel.statuses) != 1 || !strings.HasPrefix(panel.statuses[0], "failed:") {
				t.Errorf("应上报失败：%v", panel.statuses)
			}
		})
	}
}

// 本机已是目标版本（已在本机升级过）：报告成功，不下载、不写入 request.json。
func TestCheckRemoteAlreadyCurrent(t *testing.T) {
	_, panel, o := remoteFixture(t)
	o.Current = "0.3.0"
	if err := CheckRemote(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(o.StageDir, "request.json")); err == nil {
		t.Error("不应写入 request.json")
	}
	if len(panel.statuses) != 1 || !strings.HasPrefix(panel.statuses[0], "success:") {
		t.Errorf("应上报成功：%v", panel.statuses)
	}
}

// 暂存目录不可写（例如曾以 root 创建）：上报失败并说明修复方法，而不是只报 permission denied。
func TestCheckRemoteStageNotWritable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root 可以写入任何目录")
	}
	_, panel, o := remoteFixture(t)
	if err := os.MkdirAll(o.StageDir, 0o500); err != nil {
		t.Fatal(err)
	}
	os.Chmod(o.StageDir, 0o500)
	t.Cleanup(func() { os.Chmod(o.StageDir, 0o750) })
	err := CheckRemote(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "不可写") {
		t.Fatalf("应报告暂存目录不可写：%v", err)
	}
	if len(panel.statuses) != 1 || !strings.Contains(panel.statuses[0], "enable-remote-upgrade") {
		t.Errorf("上报的原因应包含修复方法：%v", panel.statuses)
	}
}

// 旧版面板没有升级接口（404）：不算错误。
func TestCheckRemoteOldPanel(t *testing.T) {
	_, _, o := remoteFixture(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	o.Server = srv.URL
	if err := CheckRemote(context.Background(), o); err != nil {
		t.Fatalf("旧版面板应安静跳过：%v", err)
	}
}
