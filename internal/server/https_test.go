package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"
)

func TestParseDomains(t *testing.T) {
	if d, err := ParseDomains(" Monitor.Example.com , panel.example.org "); err != nil || len(d) != 2 || d[0] != "monitor.example.com" {
		t.Errorf("应规范化为小写并去空格：%v %v", d, err)
	}
	for _, bad := range []string{"", "1.2.3.4", "*.example.com", "example.com:443", "localhost", "-bad.example.com", "a b.com"} {
		if _, err := ParseDomains(bad); err == nil {
			t.Errorf("%q 应被拒绝", bad)
		}
	}
}

func TestCertManager(t *testing.T) {
	m := NewCertManager(ACMEOptions{Domains: []string{"monitor.example.com"}, CacheDir: t.TempDir(), Email: "ops@example.com"})
	ctx := context.Background()
	if m.HostPolicy(ctx, "monitor.example.com") != nil || m.HostPolicy(ctx, "evil.example.com") == nil {
		t.Error("只为 --domain 指定的域名申请证书")
	}
	cfg := TLSConfigFor(m)
	if cfg.MinVersion != tls.VersionTLS12 || !slices.Contains(cfg.NextProtos, "acme-tls/1") || cfg.GetCertificate == nil {
		t.Errorf("TLS 1.2 起，支持 TLS-ALPN-01：%+v", cfg.NextProtos)
	}
}

// selfSigned 生成 127.0.0.1 的自签名证书，测试客户端把它加入信任的根证书（不跳过证书校验）。
func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func freePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}

func TestRunHTTPS(t *testing.T) {
	s, _, _ := testServer(t)
	cert, pool := selfSigned(t)
	tlsPort, httpPort := freePort(t), freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- s.RunHTTPS(ctx, HTTPSOptions{TLSListen: "127.0.0.1:" + tlsPort, HTTPListen: "127.0.0.1:" + httpPort,
			TLS: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}})
	}()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var resp *http.Response
	var err error
	for i := 0; i < 50; i++ {
		if resp, err = client.Get("https://127.0.0.1:" + tlsPort + "/healthz"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("Strict-Transport-Security") == "" {
		t.Fatalf("HTTPS 应可访问并带 HSTS：%v %v", err, resp)
	}
	resp.Body.Close()

	// TLS 1.1 及以下被拒绝（设计 23.1）
	old := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MaxVersion: tls.VersionTLS11}}}
	if _, err := old.Get("https://127.0.0.1:" + tlsPort + "/healthz"); err == nil {
		t.Error("TLS 1.1 不应握手成功")
	}

	// HTTP 一律重定向到 HTTPS，保留路径与查询参数
	resp, err = client.Get("http://127.0.0.1:" + httpPort + "/servers/1?tab=traffic")
	if err != nil || resp.StatusCode != http.StatusPermanentRedirect || resp.Header.Get("Location") != "https://127.0.0.1/servers/1?tab=traffic" {
		t.Fatalf("HTTP 应 308 重定向到 HTTPS：%v %v", err, resp)
	}
	resp.Body.Close()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("关闭时不应报错：%v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("关闭超时")
	}
}
