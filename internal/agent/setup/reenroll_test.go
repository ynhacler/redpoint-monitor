package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingPanel 记录注册与注销请求；enroll 返回给定的节点。
type recordingPanel struct {
	mu         sync.Mutex
	srv        *httptest.Server
	enrolled   []enrollRequest
	unregister []string
}

func newRecordingPanel(t *testing.T, sid int64, name, token string) *recordingPanel {
	p := &recordingPanel{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/agent/enroll":
			var req enrollRequest
			json.NewDecoder(r.Body).Decode(&req)
			p.enrolled = append(p.enrolled, req)
			json.NewEncoder(w).Encode(map[string]any{"server_id": sid, "server_name": name, "agent_token": token})
		case "/api/v1/agent/unregister":
			p.unregister = append(p.unregister, r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusNoContent)
		case "/healthz":
			json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "v-test"})
		case "/api/v1/agent/whoami":
			if r.Header.Get("Authorization") != "Bearer "+token {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"server_id": sid, "server_name": name, "panel_version": "v-test", "time": time.Now().Unix()})
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func installedAt(t *testing.T, panel string, sid int) (Paths, string, *fakeSystem) {
	p, dir := testPaths(t)
	sys := &fakeSystem{root: true, systemd: true, users: map[string]bool{userName: true}}
	os.MkdirAll(p.ConfDir, 0o750)
	os.WriteFile(p.Unit, []byte("[Unit]"), 0o644)
	os.WriteFile(p.Bin, []byte("#!bin"), 0o755)
	writeFile(sys, p.tokenFile(), "agt_old_token_value\n", 0o640, 0)
	writeFile(sys, p.envFile(), "VPSMON_SERVER="+panel+"\nVPSMON_SERVER_ID="+strconv.Itoa(sid)+"\nVPSMON_NODE_NAME=old\n", 0o640, 0)
	return p, dir, sys
}

// 换到另一个面板：先在新面板注册，再用旧 Token 注销旧节点，最后写入新配置并重启（设计 27.11）
func TestReEnrollNewPanel(t *testing.T) {
	oldP := newRecordingPanel(t, 7, "old", "agt_unused_for_old_panel")
	newP := newRecordingPanel(t, 3, "hk-1", "agt_new_token_value_123")
	p, dir, sys := installedAt(t, oldP.srv.URL, 7)
	var out bytes.Buffer
	err := ReEnroll(context.Background(), Options{Server: newP.srv.URL + "/", EnrollCode: "enr-aaaa-bbbb-cccc-dddd", Paths: p, Sys: sys,
		Out: &out, WaitFirst: 100 * time.Millisecond, HostInfoRoot: filepath.Join(dir, "host"), AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(newP.enrolled) != 1 || newP.enrolled[0].ServerID != 0 || newP.enrolled[0].EnrollCode != "ENR-AAAA-BBBB-CCCC-DDDD" {
		t.Fatalf("应向新面板注册（不带 server_id）：%+v", newP.enrolled)
	}
	if len(oldP.unregister) != 1 || oldP.unregister[0] != "Bearer agt_old_token_value" {
		t.Fatalf("应用旧 Token 注销旧节点：%v", oldP.unregister)
	}
	tok, _ := os.ReadFile(p.tokenFile())
	env, _ := os.ReadFile(p.envFile())
	if strings.TrimSpace(string(tok)) != "agt_new_token_value_123" || !strings.Contains(string(env), "VPSMON_SERVER="+newP.srv.URL+"\n") ||
		!strings.Contains(string(env), "VPSMON_SERVER_ID=3") {
		t.Fatalf("新配置 %q %q", tok, env)
	}
	if fi, _ := os.Stat(p.tokenFile()); fi.Mode().Perm() != 0o640 {
		t.Fatal("【安全】Token 文件应为 0640")
	}
	if !strings.Contains(strings.Join(sys.cmds, "\n"), "systemctl restart vpsmon-agent") {
		t.Fatalf("应重启服务：%v", sys.cmds)
	}
	if strings.Contains(out.String(), "agt_") {
		t.Fatal("【安全】输出中不得出现 Token")
	}
}

// 同一面板同一节点：不注销（否则会把刚注册的节点打回“待安装”）；注册失败时不改任何文件
func TestReEnrollSameNodeAndFailure(t *testing.T) {
	panel := newRecordingPanel(t, 7, "same", "agt_same_node_new_token")
	p, dir, sys := installedAt(t, panel.srv.URL, 7)
	o := Options{EnrollCode: "ENR-AAAA-BBBB-CCCC-DDDD", Paths: p, Sys: sys, Out: &bytes.Buffer{}, WaitFirst: 50 * time.Millisecond,
		HostInfoRoot: filepath.Join(dir, "host"), AllowHTTP: true}
	if err := ReEnroll(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if len(panel.unregister) != 0 {
		t.Fatal("同一节点不应注销")
	}

	bad, _, _ := fakePanel(t, 400, `{"error":{"code":"enroll_code_invalid","message":"x"}}`)
	writeFile(sys, p.envFile(), "VPSMON_SERVER="+panel.srv.URL+"\nVPSMON_SERVER_ID=7\n", 0o640, 0)
	before, _ := os.ReadFile(p.tokenFile())
	o.Server = bad.URL
	if err := ReEnroll(context.Background(), o); err == nil {
		t.Fatal("注册码无效时应失败")
	}
	if after, _ := os.ReadFile(p.tokenFile()); !bytes.Equal(before, after) {
		t.Fatal("注册失败时不应修改 Token")
	}
	sys.root = false
	if err := ReEnroll(context.Background(), o); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("非 root 应拒绝：%v", err)
	}
}

func TestDoctor(t *testing.T) {
	panel := newRecordingPanel(t, 7, "jp-store", "agt_valid_token_value")
	p, dir, sys := installedAt(t, panel.srv.URL, 7)
	writeFile(sys, p.tokenFile(), "agt_valid_token_value\n", 0o640, 0)
	WriteStatus(p.StateDir, Status{LastSuccess: time.Now().Unix()})
	host := filepath.Join(dir, "host")
	for _, f := range []string{"proc/stat", "proc/meminfo", "proc/loadavg", "proc/net/dev", "proc/diskstats", "proc/mounts"} {
		os.MkdirAll(filepath.Dir(filepath.Join(host, f)), 0o755)
		os.WriteFile(filepath.Join(host, f), []byte("x"), 0o644)
	}
	os.MkdirAll(filepath.Join(host, "sys/class/net"), 0o755)
	var out bytes.Buffer
	o := Options{Paths: p, Sys: &activeSys{sys}, Out: &out, HostInfoRoot: host}
	if n := Doctor(context.Background(), o); n != 0 {
		t.Fatalf("正常时不应有问题：%d\n%s", n, out.String())
	}
	for _, want := range []string{"HTTPS 正常", "面板版本 v-test", "Token 有效，属于节点 jp-store", "systemd 服务运行中", "没有发现问题"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("缺少 %q：\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "agt_valid") {
		t.Fatal("【安全】输出中不得出现 Token")
	}

	// Token 被吊销、权限过宽、服务未运行、从未上报
	writeFile(sys, p.tokenFile(), "agt_revoked_token_x\n", 0o644, 0)
	os.Chmod(p.tokenFile(), 0o644)
	WriteStatus(p.StateDir, Status{LastError: "HTTP 401"})
	out.Reset()
	o.Sys = sys // is-active 返回空：未运行
	n := Doctor(context.Background(), o)
	for _, want := range []string{"Token 无效或已吊销", "Token 文件权限", "systemd 服务未运行", "从未上报成功"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("缺少 %q：\n%s", want, out.String())
		}
	}
	if n < 4 {
		t.Fatalf("应发现至少 4 个问题：%d", n)
	}
}

// activeSys 让 systemctl is-active 返回 active
type activeSys struct{ *fakeSystem }

func (a *activeSys) Run(name string, args ...string) (string, error) {
	if name == "systemctl" && len(args) > 0 && args[0] == "is-active" {
		return "active\n", nil
	}
	return a.fakeSystem.Run(name, args...)
}
