package server

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(h http.Handler, path, token, accept string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}
	if accept != "" {
		req.Header.Set("Accept-Encoding", accept)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestResponseCompression(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	for i := 0; i < 20; i++ {
		createNode(t, h, admin, `{"name":"gz-`+itoa(int64(i))+`","group":"香港"}`)
	}
	plain := get(h, "/api/v1/servers", admin, "")
	rec := get(h, "/api/v1/servers", admin, "gzip, deflate, br")
	if rec.Code != 200 || rec.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("节点列表应压缩：%d %v", rec.Code, rec.Header())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	var a, b []serverView
	if json.Unmarshal(body, &a) != nil || json.Unmarshal(plain.Body.Bytes(), &b) != nil || len(a) != 20 || len(a) != len(b) {
		t.Fatalf("解压后应与未压缩的响应一致：%d / %d", len(a), len(b))
	}
	if rec.Body.Len()*2 > plain.Body.Len() {
		t.Errorf("压缩效果不明显：%d → %d 字节", plain.Body.Len(), rec.Body.Len())
	}
	if plain.Header().Get("Content-Encoding") != "" {
		t.Error("没有 Accept-Encoding 时不应压缩")
	}

	cases := []struct {
		name, path, accept string
	}{
		{"【安全】认证接口含 CSRF 令牌，不压缩（BREACH）", "/api/v1/auth/me", "gzip"},
		{"q=0 表示不接受", "/api/v1/servers", "gzip;q=0"},
		{"健康检查", "/healthz", "gzip"},
	}
	for _, c := range cases {
		if rec := get(h, c.path, admin, c.accept); rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s：不应压缩", c.name)
		}
	}
	req := httptest.NewRequest("GET", "/api/v1/servers", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Range", "bytes=0-10")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Header().Get("Content-Encoding") != "" {
		t.Error("带 Range 的请求不压缩")
	}
}

func TestGzipWriterSkipsEmptyAndBinary(t *testing.T) {
	for _, c := range []struct {
		name, ct string
		code     int
		want     string
	}{
		{"204 没有正文", "application/json", http.StatusNoContent, ""},
		{"已压缩的图片", "image/png", 200, ""},
		{"JSON", "application/json", 200, "gzip"},
		{"脚本", "text/javascript; charset=utf-8", 200, "gzip"},
	} {
		rec := httptest.NewRecorder()
		g := &gzipWriter{ResponseWriter: rec}
		g.Header().Set("Content-Type", c.ct)
		g.WriteHeader(c.code)
		if c.code != http.StatusNoContent {
			g.Write([]byte(strings.Repeat("x", 100)))
		}
		g.close()
		if got := rec.Header().Get("Content-Encoding"); got != c.want {
			t.Errorf("%s：Content-Encoding=%q，应为 %q", c.name, got, c.want)
		}
	}
}
