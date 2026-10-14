package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// 面板心跳（设计 1.10.3）：校验、地址脱敏、保留原地址、立即发送与失败结果。
func TestHeartbeatAPI(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	var hits atomic.Int32
	var ua atomic.Value
	status := atomic.Int32{}
	status.Store(200)
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		ua.Store(r.Header.Get("User-Agent"))
		w.WriteHeader(int(status.Load()))
	}))
	defer ext.Close()
	view := func(body []byte) heartbeatView {
		var v heartbeatView
		if err := json.Unmarshal(body, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	// 默认：未启用、没有地址、间隔 5 分钟
	if v := view(do(h, "GET", "/api/v1/settings/heartbeat", admin, nil).Body.Bytes()); v.Enabled || v.URLSet || v.Interval != 300 {
		t.Fatalf("默认设置不对：%+v", v)
	}
	// 未保存地址时不能发送测试
	if rec := do(h, "POST", "/api/v1/settings/heartbeat/test", admin, nil); rec.Code != 422 {
		t.Fatalf("没有地址时测试应为 422，得到 %d", rec.Code)
	}
	for _, b := range []string{
		`{"enabled":true,"interval":300}`,                                  // 启用但没有地址
		`{"enabled":true,"url":"http://example.com/ping","interval":300}`,  // 非回环的 HTTP
		`{"enabled":false,"url":"https://hc.example/abc","interval":30}`,   // 间隔过短
		`{"enabled":false,"url":"https://hc.example/abc","interval":7200}`, // 间隔过长
	} {
		if rec := do(h, "PUT", "/api/v1/settings/heartbeat", admin, []byte(b)); rec.Code != 422 {
			t.Errorf("%s 应为 422，得到 %d", b, rec.Code)
		}
	}

	// 保存：返回的地址只保留协议与主机（路径中的令牌不返回）
	rec := do(h, "PUT", "/api/v1/settings/heartbeat", admin, []byte(`{"enabled":true,"url":"`+ext.URL+`/ping/secret-token","interval":120}`))
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	v := view(rec.Body.Bytes())
	if !v.Enabled || !v.URLSet || v.Interval != 120 || strings.Contains(v.URL, "secret-token") || !strings.HasPrefix(v.URL, ext.URL) {
		t.Fatalf("保存结果不对：%+v", v)
	}
	// 操作日志中同样没有令牌
	if logs := do(h, "GET", "/api/v1/audit-logs", admin, nil).Body.String(); !strings.Contains(logs, `"heartbeat"`) || strings.Contains(logs, "secret-token") {
		t.Fatalf("操作日志应记录修改且不含地址中的令牌：%s", logs)
	}
	// 只改间隔、地址留空：保留原地址
	if v := view(do(h, "PUT", "/api/v1/settings/heartbeat", admin, []byte(`{"enabled":true,"url":"","interval":600}`)).Body.Bytes()); !v.URLSet || v.Interval != 600 {
		t.Fatalf("地址留空应保留原地址：%+v", v)
	}
	if got := s.heartbeat.config().URL; got != ext.URL+"/ping/secret-token" {
		t.Fatalf("保存的地址为 %q", got)
	}

	// 立即发送：成功
	v = view(do(h, "POST", "/api/v1/settings/heartbeat/test", admin, nil).Body.Bytes())
	if hits.Load() != 1 || !v.LastOK || v.LastStatus != 200 || v.LastAt == 0 || v.LastError != "" {
		t.Fatalf("发送成功的结果不对：hits=%d %+v", hits.Load(), v)
	}
	if got, _ := ua.Load().(string); !strings.HasPrefix(got, "vpsmon-heartbeat/") {
		t.Fatalf("User-Agent 为 %q", got)
	}
	// 对方返回非 2xx：记为失败并给出状态码
	status.Store(503)
	v = view(do(h, "POST", "/api/v1/settings/heartbeat/test", admin, nil).Body.Bytes())
	if v.LastOK || v.LastStatus != 503 || !strings.Contains(v.LastError, "503") {
		t.Fatalf("503 应记为失败：%+v", v)
	}
	// 连接失败
	ext.Close()
	s.sendHeartbeat(context.Background(), s.heartbeat.config().URL)
	if v := s.heartbeat.view(); v.LastOK || v.LastStatus != 0 || v.LastError == "" {
		t.Fatalf("连接失败应记为失败：%+v", v)
	}
}

// 不跟随重定向：3xx 记为失败，不请求跳转后的地址。
func TestHeartbeatNoRedirect(t *testing.T) {
	s, _, _ := testServer(t)
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Store(true) }))
	defer target.Close()
	src := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer src.Close()
	s.sendHeartbeat(context.Background(), src.URL)
	if v := s.heartbeat.view(); v.LastOK || v.LastStatus != 302 || followed.Load() {
		t.Fatalf("不应跟随重定向：%+v followed=%v", v, followed.Load())
	}
}
