package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 机器人渠道（设计 31）：校验只接受官方域名；发送格式、钉钉 / 飞书加签、200 中的业务错误码、脱敏
func TestChatChannelsValidate(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	cases := []struct {
		body, field string
		ok          bool
	}{
		{`{"type":"discord","name":"d","config":{"url":"https://discord.com/api/webhooks/1/abc"}}`, "", true},
		{`{"type":"discord","name":"d","config":{"url":"https://evil.example.com/api/webhooks/1/abc"}}`, "config.url", false},
		{`{"type":"wecom","name":"w","config":{"url":"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=k"}}`, "", true},
		{`{"type":"dingtalk","name":"x","config":{"url":"http://oapi.dingtalk.com/robot/send?access_token=t"}}`, "config.url", false},
		{`{"type":"feishu","name":"f","config":{"url":"https://open.feishu.cn/open-apis/bot/v2/hook/x","secret":"s"}}`, "", true},
		{`{"type":"bark","name":"b","config":{"token":"abcDEF123"}}`, "", true},
		{`{"type":"bark","name":"b","config":{}}`, "config.token", false},
	}
	for _, c := range cases {
		rec := do(h, "POST", "/api/v1/notification-channels", admin, []byte(c.body))
		if c.ok != (rec.Code == 201) || (!c.ok && !strings.Contains(rec.Body.String(), c.field)) {
			t.Errorf("%s：%d %s", c.body, rec.Code, rec.Body)
		}
		if c.ok && (strings.Contains(rec.Body.String(), "key=k") || strings.Contains(rec.Body.String(), "abcDEF123") ||
			strings.Contains(rec.Body.String(), "hook/x")) {
			t.Errorf("【安全】机器人地址中的密钥与设备密钥应脱敏：%s", rec.Body)
		}
	}
	rec := do(h, "POST", "/api/v1/notification-channels", admin, []byte(`{"type":"bark","name":"b2","config":{"token":"k1"}}`))
	if !strings.Contains(rec.Body.String(), `"url":"https://api.day.app"`) {
		t.Errorf("Bark 默认服务器：%s", rec.Body)
	}
}

func TestChatChannelsSend(t *testing.T) {
	s, _, _ := testServer(t)
	type got struct {
		path, query string
		body        map[string]any
	}
	var last got
	reply := `{"errcode":0,"errmsg":"ok"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		last = got{r.URL.Path, r.URL.RawQuery, nil}
		json.Unmarshal(b, &last.body)
		w.Write([]byte(reply))
	}))
	defer srv.Close()
	m := notifyMessage{Kind: NotifyFiring, ServerName: "DMIT-HK", Type: "offline", Severity: SeverityCritical,
		Message: "超过 120 秒未收到上报", StartedAt: time.Now(), Link: "https://m.example.com/servers/1"}
	send := func(c NotifyChannel) error { return s.notify.send(t.Context(), c, m) }

	// 钉钉：加签在查询参数中
	if err := send(NotifyChannel{Type: ChannelDingTalk, Config: channelConfig{URL: srv.URL + "/robot/send?access_token=t", Secret: "SEC"}}); err != nil {
		t.Fatal(err)
	}
	q, _ := urlQuery(last.query)
	ts, sign := q.Get("timestamp"), q.Get("sign")
	mac := hmac.New(sha256.New, []byte("SEC"))
	mac.Write([]byte(ts + "\nSEC"))
	if sign != base64.StdEncoding.EncodeToString(mac.Sum(nil)) || q.Get("access_token") != "t" || last.body["msgtype"] != "text" {
		t.Fatalf("钉钉 %+v", last)
	}
	// 飞书：加签在请求体中（秒级时间戳）
	reply = `{"code":0,"msg":"success"}`
	if err := send(NotifyChannel{Type: ChannelFeishu, Config: channelConfig{URL: srv.URL + "/hook/x", Secret: "SEC"}}); err != nil {
		t.Fatal(err)
	}
	fts := last.body["timestamp"].(string)
	fmac := hmac.New(sha256.New, []byte(fts+"\nSEC"))
	if _, err := strconv.ParseInt(fts, 10, 64); err != nil || last.body["sign"] != base64.StdEncoding.EncodeToString(fmac.Sum(nil)) ||
		!strings.Contains(last.body["content"].(map[string]any)["text"].(string), "DMIT-HK") {
		t.Fatalf("飞书 %+v", last.body)
	}
	// 业务错误码（HTTP 200）判为失败
	reply = `{"code":19021,"msg":"sign match fail"}`
	if err := send(NotifyChannel{Type: ChannelFeishu, Config: channelConfig{URL: srv.URL + "/hook/x"}}); err == nil || !strings.Contains(err.Error(), "19021") {
		t.Fatalf("飞书错误码 %v", err)
	}
	reply = `{"errcode":93000,"errmsg":"invalid webhook url"}`
	if err := send(NotifyChannel{Type: ChannelWeCom, Config: channelConfig{URL: srv.URL + "/send?key=k"}}); err == nil || !strings.Contains(err.Error(), "93000") {
		t.Fatalf("企业微信错误码 %v", err)
	}
	// Discord：content，禁止 @ 提及
	reply = ``
	if err := send(NotifyChannel{Type: ChannelDiscord, Config: channelConfig{URL: srv.URL + "/api/webhooks/1/abc"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(last.body["content"].(string), "超过 120 秒") || last.body["allowed_mentions"] == nil {
		t.Fatalf("Discord %+v", last.body)
	}
	// Bark：POST /push，严重为 timeSensitive，点击打开节点
	reply = `{"code":200,"message":"success"}`
	if err := send(NotifyChannel{Type: ChannelBark, Config: channelConfig{URL: srv.URL, Token: "dev123"}}); err != nil {
		t.Fatal(err)
	}
	if last.path != "/push" || last.body["device_key"] != "dev123" || last.body["level"] != "timeSensitive" ||
		last.body["url"] != "https://m.example.com/servers/1" || last.body["group"] != "VPS Monitor" {
		t.Fatalf("Bark %+v", last)
	}
}

func urlQuery(raw string) (url.Values, error) { return url.ParseQuery(raw) }
