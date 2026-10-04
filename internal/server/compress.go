package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// 响应压缩（设计 3.2）：节点列表带着每个节点的最新上报，500 个节点时约 680 KB，Web 每 3 秒轮询一次；
// 面板 VPS 的流量同样计入套餐。浏览器声明 Accept-Encoding: gzip 时压缩 JSON 与文本类静态资源。
//
// 【安全】不压缩 /api/v1/auth/*：这些响应含 CSRF 令牌，压缩后长度可能泄露信息（BREACH）。
// 不压缩 /releases/（二进制下载，需保持 Content-Length 与 Range）。

var gzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed) // 新建约分配 800 KB，复用
	return w
}}

// wantsGzip 判断是否压缩这个请求的响应。
func wantsGzip(r *http.Request) bool {
	if r.Method != http.MethodGet || r.Header.Get("Range") != "" {
		return false
	}
	p := r.URL.Path
	if strings.HasPrefix(p, "/api/v1/auth/") || strings.HasPrefix(p, "/releases/") || p == "/healthz" {
		return false
	}
	return acceptsGzip(r.Header.Values("Accept-Encoding"))
}

// acceptsGzip 判断 Accept-Encoding 是否包含 gzip（q=0 视为不接受）。
func acceptsGzip(values []string) bool {
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
			if strings.EqualFold(strings.TrimSpace(name), "gzip") {
				return strings.ReplaceAll(strings.TrimSpace(params), " ", "") != "q=0"
			}
		}
	}
	return false
}

// compressibleType 判断内容类型是否值得压缩：JSON 与文本；图片、字体等已压缩的类型不再压缩。
func compressibleType(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.HasPrefix(ct, "application/json") || strings.HasPrefix(ct, "text/") ||
		strings.HasPrefix(ct, "application/javascript") || strings.HasPrefix(ct, "image/svg+xml")
}

// gzipWriter 在写出响应头时决定是否压缩：只压缩成功的文本类响应，204 / 304 等没有正文的不压缩。
type gzipWriter struct {
	http.ResponseWriter
	zw      *gzip.Writer
	decided bool
}

func (g *gzipWriter) WriteHeader(code int) {
	if !g.decided {
		g.decided = true
		h := g.Header()
		if code != http.StatusNoContent && code != http.StatusNotModified && code >= 200 &&
			h.Get("Content-Encoding") == "" && compressibleType(h.Get("Content-Type")) {
			h.Set("Content-Encoding", "gzip")
			h.Del("Content-Length")
			g.zw = gzipPool.Get().(*gzip.Writer)
			g.zw.Reset(g.ResponseWriter)
		}
		h.Add("Vary", "Accept-Encoding")
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.decided {
		g.WriteHeader(http.StatusOK)
	}
	if g.zw != nil {
		return g.zw.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

// close 写完压缩流并归还压缩器。
func (g *gzipWriter) close() {
	if g.zw != nil {
		g.zw.Close()
		gzipPool.Put(g.zw)
		g.zw = nil
	}
}

func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }
