package server

import (
	"bufio"
	"encoding/base64"
	"mime"
	"net"
	"net/mail"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP 是只实现发信所需命令的 SMTP 服务器；starttls 为 false 时不声明 STARTTLS。
type fakeSMTP struct {
	mu       sync.Mutex
	addr     string
	starttls bool
	auth     string
	from     string
	rcpt     []string
	data     string
}

func newFakeSMTP(t *testing.T, starttls bool) *fakeSMTP {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fakeSMTP{addr: ln.Addr().String(), starttls: starttls}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := func(s string) { c.Write([]byte(s + "\r\n")) }
	w("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimSpace(line)
		up := strings.ToUpper(cmd)
		f.mu.Lock()
		switch {
		case strings.HasPrefix(up, "EHLO"):
			if f.starttls {
				w("250-fake")
				w("250-STARTTLS")
			} else {
				w("250-fake")
			}
			w("250 AUTH PLAIN")
		case strings.HasPrefix(up, "AUTH PLAIN"):
			f.auth = strings.TrimSpace(cmd[len("AUTH PLAIN"):])
			w("235 ok")
		case strings.HasPrefix(up, "MAIL FROM:"):
			f.from = cmd[len("MAIL FROM:"):]
			w("250 ok")
		case strings.HasPrefix(up, "RCPT TO:"):
			f.rcpt = append(f.rcpt, cmd[len("RCPT TO:"):])
			w("250 ok")
		case up == "DATA":
			w("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.data = b.String()
			w("250 queued")
		case up == "QUIT":
			w("221 bye")
			f.mu.Unlock()
			return
		default:
			w("250 ok")
		}
		f.mu.Unlock()
	}
}

func TestEmailValidate(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	for body, field := range map[string]string{
		`{"type":"email","name":"e","config":{"smtp_host":"smtp.example.com","smtp_security":"none","from":"a@example.com","to":"b@example.com"}}`: "config.smtp_security",
		`{"type":"email","name":"e","config":{"smtp_host":"smtp.example.com","from":"bad","to":"b@example.com"}}`:                                  "config.from",
		`{"type":"email","name":"e","config":{"smtp_host":"smtp.example.com","from":"a@example.com","to":""}}`:                                     "config.to",
		`{"type":"email","name":"e","config":{"smtp_host":"smtp.example.com","from":"a@example.com","to":"b@example.com","username":"u"}}`:         "config.password",
	} {
		rec := do(h, "POST", "/api/v1/notification-channels", admin, []byte(body))
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), field) {
			t.Errorf("%s：%d %s", body, rec.Code, rec.Body)
		}
	}
	rec := do(h, "POST", "/api/v1/notification-channels", admin, []byte(`{"type":"email","name":"e","config":{"smtp_host":"smtp.example.com",
		"username":"u@example.com","password":"p4ss","from":"VPS Monitor <a@example.com>","to":"b@example.com, c@example.com"}}`))
	if rec.Code != 201 || strings.Contains(rec.Body.String(), "p4ss") || !strings.Contains(rec.Body.String(), `"smtp_port":587`) ||
		!strings.Contains(rec.Body.String(), `"has_password":true`) {
		t.Fatalf("【安全】密码不应返回；默认 STARTTLS 587：%d %s", rec.Code, rec.Body)
	}
}

func TestEmailSend(t *testing.T) {
	s, _, _ := testServer(t)
	f := newFakeSMTP(t, false)
	host, port, _ := net.SplitHostPort(f.addr)
	p := 0
	for _, ch := range port {
		p = p*10 + int(ch-'0')
	}
	c := NotifyChannel{Type: ChannelEmail, Config: channelConfig{SMTPHost: host, SMTPPort: p, SMTPSecurity: "none",
		Username: "u", Password: "pw", From: "VPS Monitor <alerts@example.com>", To: "ops@example.com, me@example.com"}}
	m := notifyMessage{Kind: NotifyFiring, ServerName: "东京-1", Type: "offline", Severity: SeverityCritical,
		Message: "超过 120 秒未收到上报\r\nBcc: evil@example.com", StartedAt: time.Now()}
	if err := s.notify.send(t.Context(), c, m); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.Contains(f.from, "alerts@example.com") || len(f.rcpt) != 2 {
		t.Fatalf("信封 %q %v", f.from, f.rcpt)
	}
	auth, _ := base64.StdEncoding.DecodeString(f.auth)
	if string(auth) != "\x00u\x00pw" {
		t.Fatalf("AUTH PLAIN %q", auth)
	}
	msg, err := mail.ReadMessage(strings.NewReader(f.data))
	if err != nil {
		t.Fatal(err)
	}
	subj, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if !strings.Contains(subj, "东京-1") || msg.Header.Get("Bcc") != "" {
		t.Fatalf("【安全】标题应正确编码，且不能注入邮件头：%q %v", subj, msg.Header)
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(func() string {
		b := new(strings.Builder)
		buf := make([]byte, 4096)
		n, _ := msg.Body.Read(buf)
		b.Write(buf[:n])
		return b.String()
	}()), "\r\n", ""))
	if !strings.Contains(string(raw), "超过 120 秒") {
		t.Fatalf("正文 %q", raw)
	}
}

// 【安全】STARTTLS 模式下服务器不支持 STARTTLS：拒绝发送，不明文传送密码
func TestEmailRequiresSTARTTLS(t *testing.T) {
	s, _, _ := testServer(t)
	f := newFakeSMTP(t, false)
	host, port, _ := net.SplitHostPort(f.addr)
	p := 0
	for _, ch := range port {
		p = p*10 + int(ch-'0')
	}
	c := NotifyChannel{Type: ChannelEmail, Config: channelConfig{SMTPHost: host, SMTPPort: p, SMTPSecurity: "starttls",
		Username: "u", Password: "pw", From: "a@example.com", To: "b@example.com"}}
	err := s.notify.send(t.Context(), c, notifyMessage{Kind: NotifyTest, Severity: SeverityWarning, StartedAt: time.Now()})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("应拒绝：%v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.auth != "" {
		t.Fatal("【安全】不应在明文连接上发送密码")
	}
}

// 【安全】编辑邮件渠道：密码留空保持原值，但 SMTP 服务器或用户名变了就不沿用旧密码
func TestEmailKeepPassword(t *testing.T) {
	c := NotifyChannel{Type: ChannelEmail, Config: channelConfig{SMTPHost: "smtp.example.com", Username: "u", Password: "pw"}}
	b := channelBody{}
	b.Config.SMTPHost, b.Config.Username = "SMTP.example.com", "u"
	b.apply(&c)
	if c.Config.Password != "pw" {
		t.Fatalf("同一服务器应保持原密码：%q", c.Config.Password)
	}
	b.Config.SMTPHost = "evil.example.net"
	b.apply(&c)
	if c.Config.Password != "" {
		t.Fatal("【安全】换了 SMTP 服务器不应沿用旧密码")
	}
	errs := c.validate()
	if !slices.ContainsFunc(errs, func(e FieldError) bool { return e.Field == "config.password" }) {
		t.Fatalf("应要求重新填写密码：%v", errs)
	}
}
