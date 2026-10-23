package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 监听地址的校验（设计 51）：反向代理模式只允许本机回环地址，内置 HTTPS 模式允许所有地址
func TestNormalizeAddr(t *testing.T) {
	for _, c := range []struct {
		in    string
		https bool
		want  string // 空表示应被拒绝
	}{
		{"9090", false, "127.0.0.1:9090"},
		{"127.0.0.1:8080", false, "127.0.0.1:8080"},
		{"[::1]:8080", false, "[::1]:8080"},
		{"localhost:8080", false, "localhost:8080"},
		{"0.0.0.0:8080", false, ""},
		{":8080", false, ""},
		{"203.0.113.5:8080", false, ""},
		{"example.com:8080", false, ""},
		{"8443", true, ":8443"},
		{"0.0.0.0:8443", true, "0.0.0.0:8443"},
		{"0", false, ""},
		{"70000", true, ""},
		{"abc", false, ""},
	} {
		got, msg := normalizeAddr(c.in, c.https)
		if got != c.want || (c.want == "") != (msg != "") {
			t.Errorf("%q https=%v：%q %q，期望 %q", c.in, c.https, got, msg, c.want)
		}
	}
}

// getStatus 请求 addr 上的 path，返回状态码；连不上时返回 0。
func getStatus(addr, path string) int {
	c := &http.Client{Timeout: 2 * time.Second}
	res, err := c.Get("http://" + addr + path)
	if err != nil {
		return 0
	}
	res.Body.Close()
	return res.StatusCode
}

// 运行中修改 Web 端口：新端口立即可用，旧端口保留到确认；单独监听 Agent 时只放行 Agent 接口，安装命令用 Agent 地址
func TestListenSwitch(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	web0 := "127.0.0.1:" + freePort(t)
	errc := make(chan error, 4)
	if err := s.startListens(web0, nil, errc); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.lis.closeAll(t.Context()) })
	if getStatus(web0, "/healthz") != 200 {
		t.Fatal("启动后应能访问")
	}

	// 改 Web 端口（只写端口）
	p1 := freePort(t)
	rec := do(h, "PUT", "/api/v1/settings/listen", admin, []byte(`{"web":"`+p1+`","agent":"","agent_url":""}`))
	var v listenView
	json.Unmarshal(rec.Body.Bytes(), &v)
	web1 := "127.0.0.1:" + p1
	if rec.Code != 200 || v.Web != web1 || v.OldWeb != web0 || v.Mode != "proxy" || v.WebDefault != web0 {
		t.Fatalf("修改 Web：%d %s", rec.Code, rec.Body)
	}
	if getStatus(web1, "/healthz") != 200 || getStatus(web0, "/healthz") != 200 {
		t.Fatal("新旧端口都应能访问")
	}
	var saved ListenSetting
	if ok, _ := s.store.GetSetting(settingListen, &saved); !ok || saved.Web != web1 {
		t.Fatalf("应保存设置：%+v", saved)
	}

	// 端口被占用：409，什么都不改
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	rec = do(h, "PUT", "/api/v1/settings/listen", admin, []byte(`{"web":"`+busy.Addr().String()+`","agent":"","agent_url":""}`))
	if rec.Code != 409 || s.listenView().Web != web1 {
		t.Fatalf("端口被占用应 409 且不修改：%d %s", rec.Code, rec.Body)
	}

	// 确认后关闭旧端口
	if rec = do(h, "POST", "/api/v1/settings/listen/close-old", admin, nil); rec.Code != 200 || s.listenView().OldWeb != "" {
		t.Fatalf("关闭旧端口：%d %s", rec.Code, rec.Body)
	}
	waitFor(t, func() bool { return getStatus(web0, "/healthz") == 0 })

	// Agent 专用端口：反向代理模式必须填写对外地址
	pa := freePort(t)
	if rec = do(h, "PUT", "/api/v1/settings/listen", admin, []byte(`{"web":"`+web1+`","agent":"`+pa+`","agent_url":""}`)); rec.Code != 422 {
		t.Fatalf("缺少 Agent 地址应被拒绝：%d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{"http://agent.example.com", "https://", "https://a.example.com/?x=1"} {
		if rec = do(h, "PUT", "/api/v1/settings/listen", admin, []byte(`{"web":"`+web1+`","agent":"`+pa+`","agent_url":"`+bad+`"}`)); rec.Code != 422 {
			t.Errorf("Agent 地址 %s 应被拒绝：%d", bad, rec.Code)
		}
	}
	if rec = do(h, "PUT", "/api/v1/settings/listen", admin, []byte(`{"web":"`+web1+`","agent":"`+p1+`","agent_url":"https://a.example.com"}`)); rec.Code != 422 {
		t.Fatalf("Agent 端口与 Web 相同应被拒绝：%d", rec.Code)
	}
	rec = do(h, "PUT", "/api/v1/settings/listen", admin, []byte(`{"web":"`+web1+`","agent":"`+pa+`","agent_url":"https://agent.example.com:8443/"}`))
	json.Unmarshal(rec.Body.Bytes(), &v)
	agent := "127.0.0.1:" + pa
	if rec.Code != 200 || v.Agent != agent || v.EffectiveAgentURL != "https://agent.example.com:8443" || v.OldWeb != "" {
		t.Fatalf("Agent 端口：%d %s", rec.Code, rec.Body)
	}
	// Agent 端口只放行 Agent 接口（未带凭证为 401），其他地址 404；Web 端口仍接收 Agent
	if getStatus(agent, "/api/v1/agent/whoami") != 401 || getStatus(agent, "/") != 404 || getStatus(agent, "/api/v1/servers") != 404 {
		t.Fatal("Agent 端口应只放行 Agent 接口")
	}
	if getStatus(web1, "/api/v1/agent/whoami") != 401 {
		t.Fatal("Web 端口应仍接收 Agent")
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "panel.example.com"
	if cmd := s.installCommand(r, "ENR-TEST").ManualCommand; !strings.Contains(cmd, "--server https://agent.example.com:8443 ") {
		t.Fatalf("安装命令应使用 Agent 地址：%s", cmd)
	}

	// 取消 Agent 端口：关闭监听，安装命令回到面板地址
	rec = do(h, "PUT", "/api/v1/settings/listen", admin, []byte(`{"web":"`+web1+`","agent":"","agent_url":"https://x.example.com"}`))
	json.Unmarshal(rec.Body.Bytes(), &v)
	if rec.Code != 200 || v.Agent != "" || v.AgentURL != "" {
		t.Fatalf("取消 Agent 端口：%d %s", rec.Code, rec.Body)
	}
	waitFor(t, func() bool { return getStatus(agent, "/api/v1/agent/whoami") == 0 })
	if cmd := s.installCommand(r, "ENR-TEST").ManualCommand; strings.Contains(cmd, "agent.example.com") {
		t.Fatalf("安装命令应回到面板地址：%s", cmd)
	}
}

// 启动时保存的 Web 地址无法监听：退回启动参数，面板照常启动
func TestListenStartupFallback(t *testing.T) {
	s, _, _ := testServer(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	if err := s.store.PutSetting(settingListen, ListenSetting{Web: busy.Addr().String()}, time.Now()); err != nil {
		t.Fatal(err)
	}
	web0 := "127.0.0.1:" + freePort(t)
	if err := s.startListens(web0, nil, make(chan error, 4)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.lis.closeAll(t.Context()) })
	if v := s.listenView(); v.Web != web0 || getStatus(web0, "/healthz") != 200 {
		t.Fatalf("应退回启动参数：%+v", v)
	}
	// reset-listen 清除设置
	if err := s.store.ResetListen(); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.store.GetSetting(settingListen, &ListenSetting{}); ok {
		t.Fatal("设置应已清除")
	}
}
