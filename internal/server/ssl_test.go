package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newTestCA(t *testing.T) testCA {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"}, IsCA: true,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(365 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return testCA{cert, key, pool}
}

// serveTLS 启动一个只完成握手的 TLS 服务，证书由 ca 签发，有效期到 notAfter，IP 为 ip。返回端口。
func (ca testCA) serveTLS(t *testing.T, ip string, notAfter time.Time) int {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "leaf"},
		NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter, IPAddresses: []net.IP{net.ParseIP(ip)},
		DNSNames: []string{"a.example.com", "b.example.com"}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, ca.cert, &key.PublicKey, ca.key)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.(*tls.Conn).Handshake()
			c.Close()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestCheckSSL(t *testing.T) {
	ca := newTestCA(t)
	now := time.Now()
	ok := checkSSL(t.Context(), "127.0.0.1", ca.serveTLS(t, "127.0.0.1", now.Add(10*24*time.Hour)), ca.pool, now)
	if !ok.Valid || ok.LastError != "" || ok.Issuer != "Test CA" || ok.NotAfter == 0 || len(ok.SANs) != 2 {
		t.Fatalf("有效证书 %+v", ok)
	}
	exp := checkSSL(t.Context(), "127.0.0.1", ca.serveTLS(t, "127.0.0.1", now.Add(-24*time.Hour)), ca.pool, now)
	if exp.Valid || !strings.Contains(exp.LastError, "过期") || exp.NotAfter == 0 || exp.NotAfter > now.Unix() {
		t.Fatalf("【安全】过期证书应校验失败，但仍读到到期时间：%+v", exp)
	}
	untrusted := checkSSL(t.Context(), "127.0.0.1", ca.serveTLS(t, "127.0.0.1", now.Add(30*24*time.Hour)), nil, now)
	if untrusted.Valid || !strings.Contains(untrusted.LastError, "不受信任") {
		t.Fatalf("【安全】不受信任的证书应校验失败：%+v", untrusted)
	}
	mismatch := checkSSL(t.Context(), "127.0.0.1", ca.serveTLS(t, "10.0.0.1", now.Add(30*24*time.Hour)), ca.pool, now)
	if mismatch.Valid || !strings.Contains(mismatch.LastError, "不匹配") {
		t.Fatalf("域名不匹配应校验失败：%+v", mismatch)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if r := checkSSL(t.Context(), "127.0.0.1", port, ca.pool, now); !strings.HasPrefix(r.LastError, "无法连接") || r.NotAfter != 0 {
		t.Fatalf("连不上 %+v", r)
	}
}

func TestParseSSLTarget(t *testing.T) {
	for in, want := range map[string]string{
		"example.com": "example.com:443", "https://Example.com/path?q=1": "example.com:443", "example.com:8443": "example.com:8443",
		"1.2.3.4": "1.2.3.4:443", "not a host": "", "-bad.com": "",
	} {
		h, p, msg := parseSSLTarget(in, 0)
		got := ""
		if msg == "" {
			got = h + ":" + strconv.Itoa(p)
		}
		if got != want {
			t.Errorf("%q → %q，应为 %q", in, got, want)
		}
	}
}

// 接口与提醒：添加时立即检查；剩 10 天落在 14 天区间，提醒一次；删除
func TestSSLMonitorAPI(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	ca := newTestCA(t)
	s.sslRoots = ca.pool
	rcv := newReceiver(t)
	c := NotifyChannel{Type: "webhook", Name: "wh", Enabled: true, MinSeverity: "warning", NotifyResolved: true,
		Config: channelConfig{URL: rcv.srv.URL + "/hook"}}
	s.store.SaveChannel(&c, time.Now())

	port := ca.serveTLS(t, "127.0.0.1", time.Now().Add(10*24*time.Hour))
	rec := do(h, "POST", "/api/v1/ssl-monitors", admin, []byte(`{"host":"127.0.0.1:`+strconv.Itoa(port)+`","note":"api"}`))
	if rec.Code != 201 {
		t.Fatalf("添加 %d %s", rec.Code, rec.Body)
	}
	var m SSLMonitor
	json.Unmarshal(rec.Body.Bytes(), &m)
	if !m.Valid || m.NotAfter == 0 || m.Note != "api" {
		t.Fatalf("添加后应立即检查 %+v", m)
	}
	if rec := do(h, "POST", "/api/v1/ssl-monitors", admin, []byte(`{"host":"127.0.0.1","port":`+strconv.Itoa(port)+`}`)); rec.Code != 409 {
		t.Fatalf("重复添加应 409：%d", rec.Code)
	}
	s.checkReminders(time.Now())
	s.notify.wg.Wait()
	if rcv.count() != 1 {
		t.Fatalf("剩 10 天应提醒一次：%d", rcv.count())
	}
	if _, body, _ := rcv.last(); !strings.Contains(body, "SSL 证书将于") {
		t.Fatalf("提醒内容 %s", body)
	}
	s.checkReminders(time.Now())
	s.notify.wg.Wait()
	if rcv.count() != 1 {
		t.Fatal("同一区间不应重复")
	}
	if rec := do(h, "POST", "/api/v1/ssl-monitors/"+itoa(m.ID)+"/check", admin, nil); rec.Code != 200 {
		t.Fatalf("重新检查 %d", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/v1/ssl-monitors/"+itoa(m.ID), admin, nil); rec.Code != 204 {
		t.Fatalf("删除 %d", rec.Code)
	}
}
