package server

// 监听端口（设计 51，修订第 200 条）：Web 端口与 Agent 专用端口可在“系统 → 监测”中修改，保存后立即生效，不重启面板。
//
//	Web 端口    面板的主监听（反向代理模式为 --listen，内置 HTTPS 为 --https-listen）。修改时先监听新地址，
//	            旧地址继续监听，直到管理员确认新地址可以访问后关闭（或面板重启），避免反向代理还没改好就连不上
//	Agent 端口  可选的第二个监听，只放行 Agent 的接口与安装文件（/api/v1/agent/、/releases/）；安装命令改用 Agent 地址。
//	            Web 端口仍然接收 Agent，已安装的节点不受影响
//
// 【安全】反向代理模式下两个地址都只能是本机回环地址：对外的 HTTPS 由反向代理负责，面板不明文对外（约束 6）；
// 内置 HTTPS 模式下 Agent 端口用同一张证书。设置存在 settings（listen）中，启动时优先于启动参数；
// 保存的地址启动时无法监听则退回启动参数，并记录错误。改坏了可在面板主机上运行 vpsmon-server admin reset-listen 清除。

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const settingListen = "listen"

// ListenSetting 是保存的监听设置；空字符串表示使用启动参数（Web）或不单独监听（Agent）。
type ListenSetting struct {
	Web      string `json:"web"`
	Agent    string `json:"agent"`
	AgentURL string `json:"agent_url"`
}

type listenView struct {
	Mode              string `json:"mode"`
	Web               string `json:"web"`
	WebDefault        string `json:"web_default"`
	OldWeb            string `json:"old_web"`
	Agent             string `json:"agent"`
	AgentURL          string `json:"agent_url"`
	EffectiveAgentURL string `json:"effective_agent_url"`
}

// listener 是一个正在运行的监听。
type listener struct {
	addr string
	srv  *http.Server
}

// listens 管理面板的 Web 与 Agent 监听；方法可并发调用。
type listens struct {
	mu         sync.Mutex
	tls        *tls.Config // 内置 HTTPS 模式的证书；反向代理模式为空
	handler    http.Handler
	webDefault string
	web        *listener
	oldWeb     *listener // 修改前的 Web 监听，确认后关闭
	agent      *listener
	agentURL   string
	errc       chan error
	log        *slog.Logger
}

func (l *listens) https() bool { return l.tls != nil }

// agentOnly 只放行 Agent 的接口与安装文件，其他地址返回 404（Agent 端口上不提供 Web 页面与管理接口）。
func agentOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/agent/") || strings.HasPrefix(r.URL.Path, "/releases/") {
			h.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

// start 先监听 addr（失败时直接返回错误），再在后台处理请求。
func (l *listens) start(addr string, h http.Handler, purpose string) (*listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: h, TLSConfig: l.tls, ReadHeaderTimeout: 5 * time.Second}
	l.log.Info("listening", "component", "http", "addr", addr, "purpose", purpose, "tls", l.https())
	go func() {
		var err error
		if l.https() {
			err = srv.ServeTLS(ln, "", "") // 证书由 TLSConfig.GetCertificate 提供
		} else {
			err = srv.Serve(ln)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) && l.errc != nil {
			select {
			case l.errc <- err:
			default:
			}
		}
	}()
	return &listener{addr: addr, srv: srv}, nil
}

// stop 在后台关闭监听：等进行中的请求结束（最多 5 秒）。当前请求可能正经过这个监听，所以不能同步等待。
func (l *listens) stop(x *listener) {
	if x == nil {
		return
	}
	go func() {
		time.Sleep(300 * time.Millisecond) // 让当前响应先发出
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = x.srv.Shutdown(ctx)
		l.log.Info("listener closed", "component", "http", "addr", x.addr)
	}()
}

// closeAll 在面板退出时关闭全部监听。
func (l *listens) closeAll(ctx context.Context) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, x := range []*listener{l.web, l.oldWeb, l.agent} {
		if x != nil {
			_ = x.srv.Shutdown(ctx)
		}
	}
}

// startListens 按保存的设置（没有时按启动参数 webDefault）启动 Web 与 Agent 监听。
// 保存的 Web 地址无法监听时退回 webDefault，避免面板因为一项设置而无法启动。
func (s *Server) startListens(webDefault string, tlsConf *tls.Config, errc chan error) error {
	l := s.lis
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tls, l.webDefault, l.errc, l.handler = tlsConf, webDefault, errc, s.routes()
	var set ListenSetting
	if _, err := s.store.GetSetting(settingListen, &set); err != nil {
		s.log.Error("read listen setting failed", "component", "http", "err", err)
	}
	web := webDefault
	if set.Web != "" {
		web = set.Web
	}
	x, err := l.start(web, l.handler, "web")
	if err != nil && web != webDefault {
		s.log.Error("saved web listen address unavailable, using the startup flag", "component", "http", "addr", web, "err", err)
		x, err = l.start(webDefault, l.handler, "web")
	}
	if err != nil {
		return err
	}
	l.web = x
	if set.Agent != "" {
		if a, err := l.start(set.Agent, agentOnly(l.handler), "agent"); err != nil {
			s.log.Error("saved agent listen address unavailable; agents keep using the web port", "component", "http",
				"addr", set.Agent, "err", err)
		} else {
			l.agent, l.agentURL = a, set.AgentURL
		}
	}
	return nil
}

func (s *Server) listenView() listenView {
	l := s.lis
	l.mu.Lock()
	defer l.mu.Unlock()
	v := listenView{Mode: "proxy", WebDefault: l.webDefault}
	if l.https() {
		v.Mode = "https"
	}
	if l.web != nil {
		v.Web = l.web.addr
	}
	if l.oldWeb != nil {
		v.OldWeb = l.oldWeb.addr
	}
	if l.agent != nil {
		v.Agent, v.AgentURL, v.EffectiveAgentURL = l.agent.addr, l.agentURL, s.agentURLLocked()
	}
	return v
}

// agentURLLocked 返回安装命令中的 Agent 地址；没有单独监听 Agent 时为空（使用面板地址）。调用方持有 l.mu。
func (s *Server) agentURLLocked() string {
	l := s.lis
	if l.agent == nil {
		return ""
	}
	if l.agentURL != "" {
		return l.agentURL
	}
	// 内置 HTTPS：面板域名 + Agent 端口
	if u, err := url.Parse(s.publicURL); err == nil && u.Hostname() != "" {
		if _, port, err := net.SplitHostPort(l.agent.addr); err == nil {
			return "https://" + net.JoinHostPort(u.Hostname(), port)
		}
	}
	return ""
}

// agentURL 返回安装命令中的 Agent 地址：单独监听 Agent 时为 Agent 地址，否则为 fallback（面板地址）。
func (s *Server) agentURL(fallback string) string {
	s.lis.mu.Lock()
	defer s.lis.mu.Unlock()
	if u := s.agentURLLocked(); u != "" {
		return u
	}
	return fallback
}

var bareNumber = regexp.MustCompile(`^[0-9]+$`)

// normalizeAddr 校验并规范化监听地址：只写端口时补上默认主机（反向代理模式为 127.0.0.1，内置 HTTPS 为所有地址）。
// 【安全】反向代理模式只允许本机回环地址。
func normalizeAddr(v string, https bool) (string, string) {
	v = strings.TrimSpace(v)
	if bareNumber.MatchString(v) {
		if https {
			v = ":" + v
		} else {
			v = "127.0.0.1:" + v
		}
	}
	host, port, err := net.SplitHostPort(v)
	if err != nil {
		return "", "地址形如 127.0.0.1:8080，或只写端口"
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return "", "端口为 1～65535"
	}
	ip := net.ParseIP(host)
	switch {
	case host == "" && https:
	case host == "localhost":
	case ip == nil:
		return "", "主机只能是 IP 地址或 localhost"
	case !https && !ip.IsLoopback():
		return "", "反向代理模式只能监听本机地址（如 127.0.0.1），对外的 HTTPS 由反向代理负责"
	}
	return v, ""
}

// samePort 判断两个监听地址是否会冲突（端口相同且主机相同或其一为所有地址）。
func samePort(a, b string) bool {
	ha, pa, err1 := net.SplitHostPort(a)
	hb, pb, err2 := net.SplitHostPort(b)
	return err1 == nil && err2 == nil && pa == pb && (ha == hb || ha == "" || hb == "")
}

// validateListen 校验输入并规范化；返回字段错误。
func (s *Server) validateListen(in *ListenSetting) []FieldError {
	https := s.lis.https()
	var fe []FieldError
	web, msg := normalizeAddr(in.Web, https)
	if msg != "" {
		fe = append(fe, FieldError{Field: "web", Message: msg})
	}
	in.Web = web
	in.AgentURL = strings.TrimRight(strings.TrimSpace(in.AgentURL), "/")
	if strings.TrimSpace(in.Agent) == "" {
		in.Agent, in.AgentURL = "", ""
		return fe
	}
	agent, msg := normalizeAddr(in.Agent, https)
	switch {
	case msg != "":
		fe = append(fe, FieldError{Field: "agent", Message: msg})
	case web != "" && samePort(web, agent):
		fe = append(fe, FieldError{Field: "agent", Message: "Agent 端口不能与 Web 端口相同"})
	}
	in.Agent = agent
	// 【安全】Agent 地址必须是 HTTPS（Agent 拒绝向非回环地址明文上报，设计 23.1）
	if in.AgentURL != "" {
		u, err := url.Parse(in.AgentURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			fe = append(fe, FieldError{Field: "agent_url", Message: "填写 https:// 开头的地址，如 https://agent.example.com:8443"})
		}
	} else if !https {
		fe = append(fe, FieldError{Field: "agent_url", Message: "反向代理模式下需要填写 Agent 连接的对外地址（反向代理转发到 Agent 端口的 https 地址）"})
	}
	return fe
}

// ResetListen 清除网页中修改的监听端口（vpsmon-server admin reset-listen）；面板重启后按启动参数监听。
func (s *Store) ResetListen() error {
	_, err := s.DB.Exec(`DELETE FROM settings WHERE key = ?`, settingListen)
	return err
}

// handleGetListen：GET /api/v1/settings/listen，admin。
func (s *Server) handleGetListen(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.listenView())
}

// handlePutListen：PUT /api/v1/settings/listen，admin。先监听新地址，成功后才替换并保存；Web 旧地址保留到确认。
func (s *Server) handlePutListen(w http.ResponseWriter, r *http.Request) {
	var in ListenSetting
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	if fe := s.validateListen(&in); fe != nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	l := s.lis
	l.mu.Lock()
	if l.handler == nil { // 面板没有通过 Run 启动（测试中直接调用路由）
		l.handler = s.routes()
	}
	cur := func(x *listener) string {
		if x == nil {
			return ""
		}
		return x.addr
	}
	// 1. 先监听新地址；任何一个失败都不做修改
	var newWeb, newAgent *listener
	webChanged := in.Web != cur(l.web)
	agentChanged := in.Agent != cur(l.agent)
	reuseOld := webChanged && l.oldWeb != nil && in.Web == l.oldWeb.addr // 改回修改前的地址：旧监听还在，直接用它
	fail := func(field, addr string, err error) {
		l.mu.Unlock()
		for _, x := range []*listener{newWeb, newAgent} {
			if x != nil {
				_ = x.srv.Close()
			}
		}
		s.log.Warn("listen failed", "component", "http", "addr", addr, "err", err)
		s.writeError(w, r, &APIError{Code: CodeConflict, Message: "无法监听 " + addr + "（端口可能已被占用）",
			Details: []FieldError{{Field: field, Message: "无法监听这个地址，端口可能已被占用"}}})
	}
	if webChanged && !reuseOld {
		x, err := l.start(in.Web, l.handler, "web")
		if err != nil {
			fail("web", in.Web, err)
			return
		}
		newWeb = x
	}
	if agentChanged && in.Agent != "" {
		x, err := l.start(in.Agent, agentOnly(l.handler), "agent")
		if err != nil {
			fail("agent", in.Agent, err)
			return
		}
		newAgent = x
	}
	// 2. 替换：Web 旧监听保留到确认；Agent 旧监听直接关闭（Web 端口仍接收 Agent）
	if webChanged {
		if reuseOld {
			l.web, l.oldWeb = l.oldWeb, l.web
		} else {
			l.stop(l.oldWeb)
			l.oldWeb, l.web = l.web, newWeb
		}
	}
	if agentChanged {
		l.stop(l.agent)
		l.agent = newAgent
	}
	l.agentURL = in.AgentURL
	set := ListenSetting{Agent: in.Agent, AgentURL: in.AgentURL}
	if in.Web != l.webDefault {
		set.Web = in.Web // 与启动参数相同时不保存，以后改启动参数仍然有效
	}
	l.mu.Unlock()
	if err := s.store.PutSetting(settingListen, set, time.Now()); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "setting.update", Success: true,
		Details: map[string]any{"key": settingListen, "web": in.Web, "agent": in.Agent, "agent_url": in.AgentURL}})
	writeJSON(w, s.listenView())
}

// handleCloseOldListen：POST /api/v1/settings/listen/close-old，admin。关闭修改前的旧 Web 监听。
func (s *Server) handleCloseOldListen(w http.ResponseWriter, r *http.Request) {
	l := s.lis
	l.mu.Lock()
	old := l.oldWeb
	l.oldWeb = nil
	l.mu.Unlock()
	if old != nil {
		l.stop(old)
		s.audit(r, AuditEntry{ActorType: "admin", Action: "setting.update", Success: true,
			Details: map[string]any{"key": settingListen, "closed": old.addr}})
	}
	writeJSON(w, s.listenView())
}
