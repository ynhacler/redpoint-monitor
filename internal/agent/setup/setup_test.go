package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSystem 记录执行的命令，不修改真实主机。
type fakeSystem struct {
	root, systemd bool
	users         map[string]bool
	cmds          []string
	failOn        string // 执行到包含该字符串的命令时返回错误
	onEnable      func() // 模拟服务启动后的行为（写 status.json）
}

func (f *fakeSystem) IsRoot() bool                 { return f.root }
func (f *fakeSystem) HasSystemd() bool             { return f.systemd }
func (f *fakeSystem) UserExists(n string) bool     { return f.users[n] }
func (f *fakeSystem) IDs(string) (int, int, error) { return 990, 990, nil }
func (f *fakeSystem) Chown(string, int, int) error { return nil }
func (f *fakeSystem) Run(name string, args ...string) (string, error) {
	cmd := strings.Join(append([]string{name}, args...), " ")
	f.cmds = append(f.cmds, cmd)
	if f.failOn != "" && strings.Contains(cmd, f.failOn) {
		return "boom", errors.New("exit status 1")
	}
	switch {
	case strings.HasPrefix(cmd, "useradd"):
		f.users[userName] = true
	case strings.HasPrefix(cmd, "userdel"):
		delete(f.users, userName)
	case strings.HasPrefix(cmd, "systemctl enable") && f.onEnable != nil:
		f.onEnable()
	}
	return "", nil
}

// fakePanel 模拟面板的注册与注销接口。
func fakePanel(t *testing.T, status int, body string) (*httptest.Server, *enrollRequest, *string) {
	t.Helper()
	var got enrollRequest
	var unregisterToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/enroll":
			json.NewDecoder(r.Body).Decode(&got)
		case "/api/v1/agent/unregister":
			unregisterToken = r.Header.Get("Authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &got, &unregisterToken
}

func testPaths(t *testing.T) (Paths, string) {
	dir := t.TempDir()
	p := Paths{Bin: filepath.Join(dir, "bin/vpsmon-agent"), ConfDir: filepath.Join(dir, "etc/vpsmon-agent"),
		StateDir: filepath.Join(dir, "var/lib/vpsmon-agent"), Unit: filepath.Join(dir, "systemd/vpsmon-agent.service"),
		Updater:        filepath.Join(dir, "lib/vpsmon-agent/updater"),
		UpdaterPath:    filepath.Join(dir, "systemd/vpsmon-agent-updater.path"),
		UpdaterService: filepath.Join(dir, "systemd/vpsmon-agent-updater.service")}
	for _, d := range []string{filepath.Dir(p.Bin), filepath.Dir(p.Unit), p.StateDir, filepath.Join(dir, "host/etc")} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(dir, "host/etc/machine-id"), []byte("0123456789abcdef\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "host/etc/os-release"), []byte("ID=ubuntu\nVERSION_ID=\"24.04\"\n"), 0o644)
	self := filepath.Join(dir, "downloaded-agent")
	os.WriteFile(self, []byte("#!binary"), 0o755)
	return p, dir
}

const okBody = `{"server_id":7,"server_name":"jp-store","agent_token":"agt_abcdefghijklmnop","warnings":["IPv4 不一致"]}`

func TestInstallSuccess(t *testing.T) {
	p, dir := testPaths(t)
	panel, got, _ := fakePanel(t, 200, okBody)
	sys := &fakeSystem{root: true, systemd: true, users: map[string]bool{}}
	sys.onEnable = func() { WriteStatus(p.StateDir, Status{LastSuccess: time.Now().Unix() + 1}) }
	var out bytes.Buffer
	err := Install(context.Background(), Options{Server: panel.URL + "/", EnrollCode: "enr-7kq2-9xpa-m4td-h3wc",
		Version: "v-test", Self: filepath.Join(dir, "downloaded-agent"), Paths: p, Sys: sys, Out: &out,
		WaitFirst: 2 * time.Second, HostInfoRoot: filepath.Join(dir, "host")})
	if err != nil {
		t.Fatalf("安装应成功：%v\n%s", err, out.String())
	}

	if got.EnrollCode != "ENR-7KQ2-9XPA-M4TD-H3WC" || got.OS != "ubuntu" || got.OSVersion != "24.04" ||
		!strings.HasPrefix(got.MachineIDHash, "sha256:") || strings.Contains(got.MachineIDHash, "0123456789abcdef") {
		t.Errorf("注册请求不正确（注册码应规范化为大写，machine-id 只传哈希）：%+v", got)
	}
	tok, _ := os.ReadFile(p.tokenFile())
	if strings.TrimSpace(string(tok)) != "agt_abcdefghijklmnop" {
		t.Errorf("token 文件内容不正确：%q", tok)
	}
	if fi, _ := os.Stat(p.tokenFile()); fi.Mode().Perm() != 0o640 {
		t.Errorf("【安全】token 文件权限应为 0640，实际 %v（设计 27.10）", fi.Mode().Perm())
	}
	env, _ := os.ReadFile(p.envFile())
	if !strings.Contains(string(env), "VPSMON_SERVER="+panel.URL+"\n") || !strings.Contains(string(env), "VPSMON_NODE_NAME=jp-store") {
		t.Errorf("env 文件不正确：%s", env)
	}
	if strings.Contains(string(env), "agt_") {
		t.Error("【安全】Token 不能写进 env 文件（单元文件中的环境变量可能被其他进程看到）")
	}
	if b, _ := os.ReadFile(p.Bin); string(b) != "#!binary" {
		t.Error("应把当前二进制复制到 /usr/local/bin")
	}
	if b, _ := os.ReadFile(p.Unit); string(b) != unitFile {
		t.Error("应写入内嵌的 systemd 单元")
	}
	if b, _ := os.ReadFile(p.Updater); string(b) != "#!binary" {
		t.Error("应安装 updater 的独立副本（设计 29.13）")
	}
	if b, _ := os.ReadFile(p.UpdaterPath); string(b) != updaterPathFile {
		t.Error("应写入远程升级的 path 单元")
	}
	want := []string{"useradd --system --no-create-home --shell /usr/sbin/nologin vpsmon-agent",
		"systemctl daemon-reload", "systemctl daemon-reload", "systemctl enable --now vpsmon-agent-updater.path",
		"systemctl enable --now vpsmon-agent"}
	if strings.Join(sys.cmds, "\n") != strings.Join(want, "\n") {
		t.Errorf("执行的命令：\n%s\n应为：\n%s", strings.Join(sys.cmds, "\n"), strings.Join(want, "\n"))
	}
	for _, s := range []string{"已注册到", "jp-store", "IPv4 不一致", "首次上报成功"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("输出缺少 %q：\n%s", s, out.String())
		}
	}
	if strings.Contains(out.String(), "agt_") {
		t.Error("【安全】输出中不得出现 Token")
	}

	// 已安装时再次安装应拒绝，提示先卸载
	err = Install(context.Background(), Options{Server: panel.URL, EnrollCode: "ENR-7KQ2-9XPA-M4TD-H3WC", Paths: p, Sys: sys})
	if err == nil || !strings.Contains(err.Error(), "uninstall") {
		t.Errorf("重复安装应提示先卸载：%v", err)
	}
}

func TestInstallPreflightAndEnrollErrors(t *testing.T) {
	p, _ := testPaths(t)
	cases := []struct {
		name    string
		sys     *fakeSystem
		server  string
		code    string
		status  int
		body    string
		wantErr string
	}{
		{"非 root", &fakeSystem{systemd: true}, "https://x", "ENR-AAAA-AAAA-AAAA-AAAA", 0, "", "root"},
		{"没有 systemd", &fakeSystem{root: true}, "https://x", "ENR-AAAA-AAAA-AAAA-AAAA", 0, "", "systemd"},
		{"【安全】拒绝明文 HTTP 的远程面板", &fakeSystem{root: true, systemd: true}, "http://203.0.113.1", "ENR-AAAA-AAAA-AAAA-AAAA", 0, "", "HTTPS"},
		{"注册码格式错误", &fakeSystem{root: true, systemd: true}, "https://x", "ENR-123", 0, "", "格式"},
		{"注册码无效", &fakeSystem{root: true, systemd: true}, "", "ENR-AAAA-AAAA-AAAA-AAAA", 400,
			`{"error":{"code":"enroll_code_invalid","message":"x","request_id":"r_1"}}`, "重新生成"},
		{"严格核对被拒", &fakeSystem{root: true, systemd: true}, "", "ENR-AAAA-AAAA-AAAA-AAAA", 403,
			`{"error":{"code":"forbidden","message":"主机名不一致","request_id":"r_2"}}`, "主机名不一致"},
	}
	for _, c := range cases {
		c.sys.users = map[string]bool{}
		server := c.server
		if server == "" {
			panel, _, _ := fakePanel(t, c.status, c.body)
			server = panel.URL
		}
		err := Install(context.Background(), Options{Server: server, EnrollCode: c.code, Paths: p, Sys: c.sys})
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s：错误应包含 %q，实际 %v", c.name, c.wantErr, err)
		}
		if len(c.sys.cmds) != 0 {
			t.Errorf("%s：注册成功之前不得修改主机，实际执行了 %v", c.name, c.sys.cmds)
		}
		if _, err := os.Stat(p.ConfDir); err == nil {
			t.Errorf("%s：注册失败时不应创建 %s", c.name, p.ConfDir)
		}
	}
}

// 注册之后的步骤失败：回滚已创建的用户、文件与单元，主机不留下半安装状态（设计 43.8）。
func TestInstallRollback(t *testing.T) {
	p, dir := testPaths(t)
	panel, _, _ := fakePanel(t, 200, okBody)
	sys := &fakeSystem{root: true, systemd: true, users: map[string]bool{}, failOn: "enable --now"}
	err := Install(context.Background(), Options{Server: panel.URL, EnrollCode: "ENR-AAAA-AAAA-AAAA-AAAA",
		Self: filepath.Join(dir, "downloaded-agent"), Paths: p, Sys: sys, HostInfoRoot: filepath.Join(dir, "host")})
	if err == nil || !strings.Contains(err.Error(), "已回滚") || !strings.Contains(err.Error(), "10 分钟") {
		t.Fatalf("应返回回滚提示：%v", err)
	}
	for _, f := range []string{p.ConfDir, p.Unit, p.Bin} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("回滚后 %s 不应存在", f)
		}
	}
	if sys.users[userName] {
		t.Error("回滚后应删除本次创建的用户")
	}
}

func TestUninstall(t *testing.T) {
	p, dir := testPaths(t)
	panel, _, unregToken := fakePanel(t, 200, okBody)
	sys := &fakeSystem{root: true, systemd: true, users: map[string]bool{}}
	sys.onEnable = func() { WriteStatus(p.StateDir, Status{LastSuccess: time.Now().Unix() + 1}) }
	if err := Install(context.Background(), Options{Server: panel.URL, EnrollCode: "ENR-AAAA-AAAA-AAAA-AAAA",
		Self: filepath.Join(dir, "downloaded-agent"), Paths: p, Sys: sys, WaitFirst: time.Second,
		HostInfoRoot: filepath.Join(dir, "host")}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Uninstall(context.Background(), Options{Paths: p, Sys: sys, Out: &out}); err != nil {
		t.Fatal(err)
	}
	if *unregToken != "Bearer agt_abcdefghijklmnop" {
		t.Errorf("卸载时应用 Agent Token 通知面板（设计 27.11）：%q", *unregToken)
	}
	for _, f := range []string{p.ConfDir, p.StateDir, p.Unit, p.Bin, p.Updater, p.UpdaterPath, p.UpdaterService} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("卸载后 %s 不应存在", f)
		}
	}
	if sys.users[userName] {
		t.Error("卸载后应删除用户")
	}
}

func TestStatusFile(t *testing.T) {
	dir := t.TempDir()
	WriteStatus(dir, Status{Version: "v1", LastSuccess: 100, LastError: ""})
	st, err := ReadStatus(filepath.Join(dir, "status.json"))
	if err != nil || st.Version != "v1" || st.LastSuccess != 100 {
		t.Errorf("状态文件读写不一致：%+v %v", st, err)
	}
	WriteStatus(filepath.Join(dir, "missing"), Status{}) // 目录不存在时静默忽略，不能 panic
}

// 内嵌的单元必须与 deploy/systemd 中的一致，避免两份配置漂移。
func TestEmbeddedUnitMatchesDeployFile(t *testing.T) {
	for name, embedded := range map[string]string{"vpsmon-agent.service": unitFile,
		"vpsmon-agent-updater.path": updaterPathFile, "vpsmon-agent-updater.service": updaterServiceFile} {
		b, err := os.ReadFile("../../../deploy/systemd/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != embedded {
			t.Errorf("internal/agent/setup/%s 与 deploy/systemd/%s 不一致，请同步修改", name, name)
		}
	}
}

// --no-remote-upgrade：不安装 updater，写下禁止文件；之后 enable-remote-upgrade 可以打开（设计 29.13）。
func TestInstallWithoutRemoteUpgrade(t *testing.T) {
	p, dir := testPaths(t)
	panel, _, _ := fakePanel(t, 200, okBody)
	sys := &fakeSystem{root: true, systemd: true, users: map[string]bool{}}
	sys.onEnable = func() { WriteStatus(p.StateDir, Status{LastSuccess: time.Now().Unix() + 1}) }
	o := Options{Server: panel.URL, EnrollCode: "ENR-AAAA-AAAA-AAAA-AAAA", NoRemoteUpgrade: true,
		Self: filepath.Join(dir, "downloaded-agent"), Paths: p, Sys: sys, WaitFirst: time.Second,
		HostInfoRoot: filepath.Join(dir, "host")}
	if err := Install(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Updater); err == nil {
		t.Error("--no-remote-upgrade 时不应安装 updater")
	}
	if _, err := os.Stat(p.NoRemoteUpgradeFile()); err != nil {
		t.Error("--no-remote-upgrade 时应写下禁止文件")
	}
	if err := EnableRemoteUpgrade(Options{Paths: p, Sys: sys, Self: p.Bin}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.NoRemoteUpgradeFile()); err == nil {
		t.Error("启用后应删除禁止文件")
	}
	if _, err := os.Stat(p.Updater); err != nil {
		t.Error("启用后应安装 updater")
	}
}
