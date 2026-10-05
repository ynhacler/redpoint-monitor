package server

// 邮件通知渠道（设计 31 第二阶段，提前完成）：面板通过用户自己的 SMTP 服务器发送，只用标准库 net/smtp 与 crypto/tls。
//
// 【安全】
//   - 465 端口为隐式 TLS，587 等为 STARTTLS；都校验证书（约束 6）。服务器不支持 STARTTLS 时拒绝发送，不明文传送密码
//   - 只有发往本机回环地址（本机的邮件中转）时才允许不加密（none）
//   - SMTP 密码只用于发送：接口返回时只说明是否已设置，不写日志（设计 24.7）
//   - 收件人最多 10 个；标题按 RFC 2047 编码，正文 UTF-8 + Base64，防止邮件头注入（换行一律去掉）

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

const ChannelEmail = "email"

// emailSecurity：tls（隐式 TLS，465）/ starttls（587）/ none（只允许回环地址）
var emailSecurity = map[string]bool{"tls": true, "starttls": true, "none": true}

// validateEmail 校验邮件渠道配置。
func (c *NotifyChannel) validateEmail() []FieldError {
	var errs []FieldError
	bad := func(f, m string) { errs = append(errs, FieldError{Field: f, Message: m}) }
	e := &c.Config
	e.URL, e.BotToken, e.ChatID, e.Topic, e.Token, e.Secret = "", "", "", "", "", ""
	e.SMTPHost = strings.ToLower(strings.TrimSpace(e.SMTPHost))
	if e.SMTPHost == "" || len(e.SMTPHost) > 253 || strings.ContainsAny(e.SMTPHost, " /:") {
		bad("config.smtp_host", "请填写 SMTP 服务器，如 smtp.gmail.com")
	}
	if e.SMTPSecurity == "" {
		e.SMTPSecurity = "starttls"
	}
	if !emailSecurity[e.SMTPSecurity] {
		bad("config.smtp_security", "加密方式只能是 tls、starttls 或 none")
	} else if e.SMTPSecurity == "none" && !isLoopbackHost(e.SMTPHost) {
		bad("config.smtp_security", "只有本机回环地址的邮件中转可以不加密（约束 6）")
	}
	if e.SMTPPort == 0 {
		e.SMTPPort = map[string]int{"tls": 465, "starttls": 587, "none": 25}[e.SMTPSecurity]
	}
	if e.SMTPPort < 1 || e.SMTPPort > 65535 {
		bad("config.smtp_port", "端口为 1～65535")
	}
	if len(e.Username) > 128 || len(e.Password) > 256 {
		bad("config.username", "用户名或密码过长")
	}
	if e.Username != "" && e.Password == "" {
		bad("config.password", "请填写 SMTP 密码（或授权码）")
	}
	if a, err := mail.ParseAddress(e.From); err != nil || strings.ContainsAny(e.From, "\r\n") {
		bad("config.from", "发件人格式不正确，如 alerts@example.com 或 VPS Monitor <alerts@example.com>")
	} else {
		e.From = a.String()
	}
	var to []string
	for _, x := range strings.FieldsFunc(e.To, func(r rune) bool { return r == ',' || r == ';' || r == '，' || r == ' ' || r == '\n' }) {
		a, err := mail.ParseAddress(x)
		if err != nil {
			bad("config.to", "收件人格式不正确："+x)
			break
		}
		to = append(to, a.Address)
	}
	if len(to) == 0 || len(to) > 10 {
		bad("config.to", "请填写 1～10 个收件人，用逗号分隔")
	}
	e.To = strings.Join(to, ", ")
	return errs
}

func (c NotifyChannel) emailView() map[string]any {
	e := c.Config
	return map[string]any{"smtp_host": e.SMTPHost, "smtp_port": e.SMTPPort, "smtp_security": e.SMTPSecurity,
		"username": e.Username, "has_password": e.Password != "", "from": e.From, "to": e.To}
}

// oneLine 去掉换行，防止邮件头注入
func oneLine(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

// buildEmail 生成邮件原文（RFC 5322）：标题 RFC 2047 编码，正文 UTF-8 + Base64（每行 76 字符）。
func buildEmail(from string, to []string, subject, body string, now time.Time) []byte {
	id := make([]byte, 12)
	rand.Read(id)
	domain := "vpsmon.local"
	if a, err := mail.ParseAddress(from); err == nil {
		if i := strings.LastIndex(a.Address, "@"); i >= 0 {
			domain = a.Address[i+1:]
		}
	}
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + oneLine(v) + "\r\n") }
	h("From", from)
	h("To", strings.Join(to, ", "))
	h("Subject", mime.QEncoding.Encode("utf-8", oneLine(subject)))
	h("Date", now.Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=utf-8")
	h("Content-Transfer-Encoding", "base64")
	h("X-Mailer", "vpsmon-server")
	b.WriteString("\r\n")
	enc := base64.StdEncoding.EncodeToString([]byte(body))
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return []byte(b.String())
}

// sendEmail 发送一封通知邮件。rootCAs 为 nil 时用系统根证书（测试中替换）。
func (n *notifier) sendEmail(ctx context.Context, c NotifyChannel, m notifyMessage) error {
	e := c.Config
	now := time.Now()
	addr := net.JoinHostPort(e.SMTPHost, strconv.Itoa(e.SMTPPort))
	tlsCfg := &tls.Config{ServerName: e.SMTPHost, RootCAs: n.smtpRoots, MinVersion: tls.VersionTLS12}
	d := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	if e.SMTPSecurity == "tls" {
		conn, err = (&tls.Dialer{NetDialer: d, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		var ve *tls.CertificateVerificationError
		if errors.As(err, &ve) {
			return permanentError{errors.New(verifyErrorText(ve.Err))}
		}
		return fmt.Errorf("无法连接 SMTP 服务器：%v", err)
	}
	conn.SetDeadline(now.Add(30 * time.Second))
	cl, err := smtp.NewClient(conn, e.SMTPHost)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SMTP 握手失败：%v", err)
	}
	defer cl.Close()
	if e.SMTPSecurity == "starttls" {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return permanentError{errors.New("SMTP 服务器不支持 STARTTLS，为保护密码已拒绝发送；请改用 465 端口的 TLS")}
		}
		if err := cl.StartTLS(tlsCfg); err != nil {
			var ve *tls.CertificateVerificationError
			if errors.As(err, &ve) {
				return permanentError{errors.New(verifyErrorText(ve.Err))}
			}
			return fmt.Errorf("STARTTLS 失败：%v", err)
		}
	}
	if e.Username != "" {
		// PlainAuth 在未加密的连接上（回环地址除外）拒绝发送密码
		if err := cl.Auth(smtp.PlainAuth("", e.Username, e.Password, e.SMTPHost)); err != nil {
			return permanentError{fmt.Errorf("SMTP 登录失败（检查用户名与密码 / 授权码）：%v", err)}
		}
	}
	from, _ := mail.ParseAddress(e.From)
	if err := cl.Mail(from.Address); err != nil {
		return fmt.Errorf("发件人被拒绝：%v", err)
	}
	to := strings.Split(e.To, ", ")
	for _, t := range to {
		if err := cl.Rcpt(t); err != nil {
			return fmt.Errorf("收件人 %s 被拒绝：%v", t, err)
		}
	}
	w, err := cl.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA 失败：%v", err)
	}
	title := m.title()
	if _, err := w.Write(buildEmail(e.From, to, title, m.text(now), now)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("邮件未被接受：%v", err)
	}
	return cl.Quit()
}
