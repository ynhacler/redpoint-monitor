package server

// 内置 HTTPS（设计 25、23.1）：vpsmon-server run --domain monitor.example.com 时由面板自己通过 ACME（默认 Let's Encrypt）
// 申请与续期证书，不再必须依赖 Caddy 等反向代理。不加 --domain 时行为不变（监听本地端口，由反向代理提供 HTTPS）。
//
//	:443  HTTPS，TLS 1.2 起；也完成 TLS-ALPN-01 验证
//	:80   完成 HTTP-01 验证；其他请求 308 重定向到 HTTPS
//	证书  缓存在 DATA/certs（0700），重启后不重复申请

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// domainPattern 是可申请证书的域名：至少两段，不含通配符与端口；IP 地址不能申请（Let's Encrypt 不签发）。
var domainPattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// ParseDomains 解析 --domain（逗号分隔），返回规范化的小写域名。
func ParseDomains(s string) ([]string, error) {
	var out []string
	for _, d := range strings.Split(s, ",") {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if !domainPattern.MatchString(d) || net.ParseIP(d) != nil {
			return nil, errors.New("--domain 只接受域名（如 monitor.example.com），不支持 IP、端口与通配符：" + d)
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, errors.New("--domain 不能为空")
	}
	return out, nil
}

// ACMEOptions 是证书申请的设置。
type ACMEOptions struct {
	Domains   []string
	Email     string // 可选：证书即将过期等通知的联系邮箱
	CacheDir  string // 证书缓存目录，DATA/certs
	Directory string // ACME 目录地址；空为 Let's Encrypt 正式环境（测试时可用 staging）
}

// NewCertManager 创建证书管理器：只为指定的域名申请证书（其他 SNI 一律拒绝，避免被利用来耗尽申请配额）。
// 使用 --domain 即表示同意 ACME 服务商（默认 Let's Encrypt）的服务条款。
func NewCertManager(o ACMEOptions) *autocert.Manager {
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(o.Domains...),
		Cache:      autocert.DirCache(o.CacheDir),
		Email:      o.Email,
	}
	if o.Directory != "" {
		m.Client = &acme.Client{DirectoryURL: o.Directory}
	}
	return m
}

// TLSConfigFor 返回 HTTPS 使用的 TLS 设置：证书由管理器提供，最低 TLS 1.2（设计 23.1）。
func TLSConfigFor(m *autocert.Manager) *tls.Config {
	cfg := m.TLSConfig() // 含 TLS-ALPN-01 所需的 acme-tls/1 与 h2
	cfg.MinVersion = tls.VersionTLS12
	return cfg
}

// HTTPSOptions 是 RunHTTPS 的监听设置。
type HTTPSOptions struct {
	TLSListen  string      // 如 :443
	HTTPListen string      // 如 :80；空则不监听（只能用 TLS-ALPN-01 验证，没有 HTTP 到 HTTPS 的重定向）
	TLS        *tls.Config // 证书来源；正式运行为 TLSConfigFor(manager)，测试时可传入静态证书
	// HTTPHandler 处理 :80 的请求：正式运行为 manager.HTTPHandler(重定向)，完成 HTTP-01 验证后其余请求重定向
	HTTPHandler func(fallback http.Handler) http.Handler
}

// redirectToHTTPS 把 HTTP 请求 308 重定向到同一主机的 HTTPS 地址（保留路径与查询参数）。
func redirectToHTTPS(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusPermanentRedirect)
}

// RunHTTPS 以内置 HTTPS 运行面板，直到 ctx 结束。
func (s *Server) RunHTTPS(ctx context.Context, o HTTPSOptions) error {
	s.startBackground(ctx)
	tlsSrv := &http.Server{Addr: o.TLSListen, Handler: s.routes(), TLSConfig: o.TLS, ReadHeaderTimeout: 5 * time.Second}
	servers := []*http.Server{tlsSrv}
	errc := make(chan error, 2)
	if o.HTTPListen != "" {
		h := http.Handler(http.HandlerFunc(redirectToHTTPS))
		if o.HTTPHandler != nil {
			h = o.HTTPHandler(h)
		}
		httpSrv := &http.Server{Addr: o.HTTPListen, Handler: h, ReadHeaderTimeout: 5 * time.Second}
		servers = append(servers, httpSrv)
		go func() {
			s.log.Info("listening", "component", "http", "addr", o.HTTPListen, "purpose", "acme http-01 + redirect to https")
			errc <- httpSrv.ListenAndServe()
		}()
	}
	go func() {
		s.log.Info("listening", "component", "http", "addr", o.TLSListen, "tls", true)
		errc <- tlsSrv.ListenAndServeTLS("", "") // 证书由 TLSConfig.GetCertificate 提供
	}()
	return s.serveUntil(ctx, errc, servers...)
}

// serveUntil 等待 ctx 结束或某个监听出错，然后关闭全部监听并写出缓存的数据。
func (s *Server) serveUntil(ctx context.Context, errc chan error, servers ...*http.Server) error {
	var err error
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.ws.closeAll() // Shutdown 不管已接管的连接：WebSocket 单独关闭，页面会自动重连
	for _, srv := range servers {
		_ = srv.Shutdown(shutdown)
	}
	s.flush() // persist what is buffered
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
