package server

// 更多通知渠道（设计 31 第二阶段，提前完成）：Discord、Bark、企业微信、钉钉、飞书。都是 HTTPS 机器人地址，由面板直接发送。
//
//   - 机器人地址必须是各家官方域名（防止填错成其他地址）；只允许 HTTPS（约束 6），不跟随重定向（与 Webhook 相同）
//   - 钉钉、飞书可选“加签”密钥：按各自文档用 HMAC-SHA256 签名；企业微信、钉钉、飞书即使出错也返回 HTTP 200，
//     按响应中的 errcode / code 判断成败
//   - 【安全】机器人地址中含密钥（key、access_token、hook ID）：返回时只保留协议与主机；签名密钥、Bark 设备密钥只说明是否已设置

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ChannelDiscord  = "discord"
	ChannelBark     = "bark"
	ChannelWeCom    = "wecom"    // 企业微信群机器人
	ChannelDingTalk = "dingtalk" // 钉钉群机器人
	ChannelFeishu   = "feishu"   // 飞书 / Lark 群机器人
)

// defaultBarkServer 是 Bark 的公共服务器；自建 bark-server 时填写自己的地址。
const defaultBarkServer = "https://api.day.app"

// chatHosts 是各渠道机器人地址允许的主机。
var chatHosts = map[string][]string{
	ChannelDiscord:  {"discord.com", "discordapp.com"},
	ChannelWeCom:    {"qyapi.weixin.qq.com"},
	ChannelDingTalk: {"oapi.dingtalk.com"},
	ChannelFeishu:   {"open.feishu.cn", "open.larksuite.com"},
}

var chatNames = map[string]string{ChannelDiscord: "Discord", ChannelWeCom: "企业微信", ChannelDingTalk: "钉钉", ChannelFeishu: "飞书"}

func isChatChannel(t string) bool {
	_, ok := chatHosts[t]
	return ok || t == ChannelBark
}

// validateChat 校验机器人类渠道；返回字段错误。
func (c *NotifyChannel) validateChat() []FieldError {
	var errs []FieldError
	c.Config.URL = strings.TrimSpace(c.Config.URL)
	c.Config.BotToken, c.Config.ChatID, c.Config.Topic = "", "", ""
	if c.Type == ChannelBark {
		c.Config.URL = strings.TrimRight(c.Config.URL, "/")
		if c.Config.URL == "" {
			c.Config.URL = defaultBarkServer
		}
		if msg := checkWebhookURL(c.Config.URL); msg != "" {
			errs = append(errs, FieldError{Field: "config.url", Message: strings.Replace(msg, "Webhook", "Bark 服务器", 1)})
		}
		if !ntfyToken.MatchString(c.Config.Token) {
			errs = append(errs, FieldError{Field: "config.token", Message: "请填写 Bark App 中显示的设备密钥（Device Key）"})
		}
		c.Config.Secret = ""
		return errs
	}
	c.Config.Token = ""
	if msg := checkWebhookURL(c.Config.URL); msg != "" {
		errs = append(errs, FieldError{Field: "config.url", Message: msg})
	} else if u, _ := url.Parse(c.Config.URL); !hostAllowed(u.Hostname(), chatHosts[c.Type]) {
		errs = append(errs, FieldError{Field: "config.url", Message: fmt.Sprintf("请填写%s机器人的地址（%s）", chatNames[c.Type],
			strings.Join(chatHosts[c.Type], " 或 "))})
	}
	if c.Type != ChannelDingTalk && c.Type != ChannelFeishu {
		c.Config.Secret = "" // 只有钉钉、飞书有加签
	}
	if len(c.Config.Secret) > 128 {
		errs = append(errs, FieldError{Field: "config.secret", Message: "加签密钥最长 128 个字符"})
	}
	return errs
}

func hostAllowed(h string, hosts []string) bool {
	for _, x := range hosts {
		if strings.EqualFold(h, x) {
			return true
		}
	}
	return false
}

// chatView 是返回给 Web 的脱敏配置。
func (c NotifyChannel) chatView() map[string]any {
	if c.Type == ChannelBark {
		return map[string]any{"url": c.Config.URL, "has_token": c.Config.Token != ""}
	}
	return map[string]any{"url": maskURL(c.Config.URL), "has_secret": c.Config.Secret != ""}
}

// truncateRunes 截断到 n 个字符（机器人消息有长度限制）。
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// chatRequest 构造一次发送请求。
func chatRequest(ctx context.Context, c NotifyChannel, m notifyMessage, now time.Time) (*http.Request, error) {
	title := m.title()
	text := m.text(now)
	var body any
	target := c.Config.URL
	switch c.Type {
	case ChannelDiscord:
		body = map[string]any{"content": truncateRunes(text, 2000), "allowed_mentions": map[string]any{"parse": []string{}}}
	case ChannelWeCom:
		body = map[string]any{"msgtype": "text", "text": map[string]string{"content": truncateRunes(text, 2000)}}
	case ChannelDingTalk:
		body = map[string]any{"msgtype": "text", "text": map[string]string{"content": truncateRunes(text, 2000)}}
		if c.Config.Secret != "" {
			// 钉钉加签：sign = Base64(HMAC-SHA256(secret, timestamp + "\n" + secret))，放在查询参数中
			ts := strconv.FormatInt(now.UnixMilli(), 10)
			mac := hmac.New(sha256.New, []byte(c.Config.Secret))
			mac.Write([]byte(ts + "\n" + c.Config.Secret))
			u, err := url.Parse(target)
			if err != nil {
				return nil, err
			}
			q := u.Query()
			q.Set("timestamp", ts)
			q.Set("sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
			u.RawQuery = q.Encode()
			target = u.String()
		}
	case ChannelFeishu:
		b := map[string]any{"msg_type": "text", "content": map[string]string{"text": truncateRunes(text, 2000)}}
		if c.Config.Secret != "" {
			// 飞书加签：sign = Base64(HMAC-SHA256(key = timestamp + "\n" + secret, 空消息))，放在请求体中（秒级时间戳）
			ts := strconv.FormatInt(now.Unix(), 10)
			mac := hmac.New(sha256.New, []byte(ts+"\n"+c.Config.Secret))
			b["timestamp"] = ts
			b["sign"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))
		}
		body = b
	case ChannelBark:
		level := "active"
		switch {
		case m.Kind == NotifyResolved:
		case m.Severity == SeverityCritical:
			level = "timeSensitive"
		}
		b := map[string]any{"device_key": c.Config.Token, "title": truncateRunes(title, 100),
			"body": truncateRunes(strings.TrimSpace(strings.TrimPrefix(text, title)), 1000), "group": "VPS Monitor", "level": level}
		if m.Link != "" {
			b["url"] = m.Link
		}
		body = b
		target = c.Config.URL + "/push"
	}
	data, _ := json.Marshal(body)
	return http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(data))
}

// chatResult 检查 HTTP 200 响应中的业务错误码（企业微信 / 钉钉 errcode，飞书 code，Bark code）。
func chatResult(t string, body []byte) error {
	var r struct {
		ErrCode *int   `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		Code    *int   `json:"code"`
		Msg     string `json:"msg"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &r) != nil {
		return nil // Discord 成功时返回 204 无内容
	}
	switch t {
	case ChannelWeCom, ChannelDingTalk:
		if r.ErrCode != nil && *r.ErrCode != 0 {
			return fmt.Errorf("返回错误 %d：%s", *r.ErrCode, r.ErrMsg)
		}
	case ChannelFeishu:
		if r.Code != nil && *r.Code != 0 {
			return fmt.Errorf("返回错误 %d：%s", *r.Code, r.Msg)
		}
	case ChannelBark:
		if r.Code != nil && *r.Code != 200 {
			return fmt.Errorf("返回错误 %d：%s", *r.Code, r.Message)
		}
	}
	return nil
}
