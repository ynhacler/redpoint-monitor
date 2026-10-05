package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ntfy 渠道（设计 31）：校验、脱敏（主题与令牌）、JSON 发布格式、访问令牌、错误说明
func TestNtfyChannel(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	var got map[string]any
	var auth string
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		auth = r.Header.Get("Authorization")
		w.WriteHeader(status)
		if status != 200 {
			w.Write([]byte(`{"code":40301,"http":403,"error":"forbidden"}`))
		}
	}))
	defer srv.Close()

	// 校验：非回环的 http、非法主题
	rec := do(h, "POST", "/api/v1/notification-channels", admin, []byte(`{"type":"ntfy","name":"n","config":{"url":"http://ntfy.example.com","topic":"a b"}}`))
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "config.url") || !strings.Contains(rec.Body.String(), "config.topic") {
		t.Fatalf("校验 %d %s", rec.Code, rec.Body)
	}
	// 默认公共服务器
	rec = do(h, "POST", "/api/v1/notification-channels", admin, []byte(`{"type":"ntfy","name":"pub","config":{"topic":"vpsmon-alerts-x7k9"}}`))
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"url":"https://ntfy.sh"`) {
		t.Fatalf("默认服务器 %d %s", rec.Code, rec.Body)
	}
	// 自建服务器（回环）+ 访问令牌；返回时主题与令牌脱敏
	rec = do(h, "POST", "/api/v1/notification-channels", admin, []byte(`{"type":"ntfy","name":"self","config":{"url":"`+srv.URL+`/","topic":"vpsmon-alerts-x7k9","token":"tk_abcdef123"}}`))
	if rec.Code != 201 || strings.Contains(rec.Body.String(), "tk_abcdef123") || strings.Contains(rec.Body.String(), "alerts-x7k9") ||
		!strings.Contains(rec.Body.String(), `"has_token":true`) {
		t.Fatalf("【安全】主题与令牌应脱敏：%d %s", rec.Code, rec.Body)
	}
	var c channelView
	json.Unmarshal(rec.Body.Bytes(), &c)
	stored, _ := s.store.GetChannel(c.ID)
	if err := s.notify.deliver(t.Context(), stored, notifyMessage{Kind: NotifyFiring, ServerName: "DMIT-HK", Type: "offline",
		Severity: SeverityCritical, Message: "超过 120 秒未收到上报", StartedAt: time.Now(), Link: "https://m.example.com/servers/1"}, nil); err != nil {
		t.Fatal(err)
	}
	if got["topic"] != "vpsmon-alerts-x7k9" || got["priority"].(float64) != 5 || !strings.Contains(got["title"].(string), "DMIT-HK") ||
		got["click"] != "https://m.example.com/servers/1" || auth != "Bearer tk_abcdef123" {
		t.Fatalf("发布内容 %v %q", got, auth)
	}
	// 修改时令牌留空保持原值；clear_token 清除
	do(h, "PUT", "/api/v1/notification-channels/"+itoa(c.ID), admin, []byte(`{"name":"self2","config":{}}`))
	if st, _ := s.store.GetChannel(c.ID); st.Config.Token != "tk_abcdef123" || st.Config.Topic != "vpsmon-alerts-x7k9" {
		t.Fatalf("留空应保持原值：%+v", st.Config)
	}
	do(h, "PUT", "/api/v1/notification-channels/"+itoa(c.ID), admin, []byte(`{"name":"self2","config":{"clear_token":true}}`))
	if st, _ := s.store.GetChannel(c.ID); st.Config.Token != "" {
		t.Fatal("clear_token 应清除令牌")
	}
	// 服务器的错误说明写入投递记录
	status = 403
	stored, _ = s.store.GetChannel(c.ID)
	err := s.notify.deliver(t.Context(), stored, notifyMessage{Kind: NotifyResolved, ServerName: "x", Severity: SeverityWarning,
		StartedAt: time.Now(), ResolvedAt: time.Now()}, nil)
	if err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("错误说明 %v", err)
	}
}
