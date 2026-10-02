package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"
)

// HTTP 中间件：request_id、panic 恢复、请求日志，以及统一的 JSON / 错误响应（设计 24.6、43.3、43.4）。

// slowRequest：耗时超过 1 秒的请求记 WARN（设计 24.6）。
const slowRequest = time.Second

// reqInfo 随请求在中间件与处理函数之间传递。处理函数在鉴权后填写主体信息，
// 请求日志据此记录“谁”发起了请求（设计 24.6）。
type reqInfo struct {
	id          string // request_id，同时出现在响应头、错误响应与日志中（设计 43.4）
	principal   string // admin / agent；未认证时为空
	principalID int64  // agent 为节点 ID
}

type ctxKey struct{}

// info 返回当前请求的 reqInfo；不经过 withRequestInfo 的请求（如单元测试直接调用）返回零值。
func info(r *http.Request) *reqInfo {
	if ri, ok := r.Context().Value(ctxKey{}).(*reqInfo); ok {
		return ri
	}
	return &reqInfo{}
}

// newRequestID 生成 “r_” + 12 位十六进制的请求编号，用户可从界面复制并对照日志（设计 43.6）。
func newRequestID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "r_" + hex.EncodeToString(b)
}

// statusRecorder 记录处理函数写出的状态码，供请求日志使用。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap 让 http.ResponseController 能访问底层连接（以后的 WebSocket 需要，设计 20）。
func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// middleware 包装所有路由：分配 request_id → 捕获 panic → 记录请求日志。
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ri := &reqInfo{id: newRequestID()}
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, ri))
		w.Header().Set("X-Request-ID", ri.id)
		rec := &statusRecorder{ResponseWriter: w}

		defer func() {
			if v := recover(); v != nil {
				// 客户端断开时 net/http 用 ErrAbortHandler 中止处理，属于正常情况，交回给 net/http
				if v == http.ErrAbortHandler {
					panic(v)
				}
				// 单个请求出错不影响整体运行：记录堆栈，返回 500 与 request_id（设计 43.3.1）
				s.log.Error("panic in handler", "component", "http", "request_id", ri.id,
					"panic", v, "stack", string(debug.Stack()))
				if rec.status == 0 {
					s.writeError(rec, r, internalError(nil))
				}
			}
			s.logRequest(r, ri, rec.status, time.Since(start))
		}()
		next.ServeHTTP(rec, r)
	})
}

// logRequest 写一行请求日志（设计 24.6）。
//
// 【安全】只记录路由模板（/api/v1/servers/{id}）与方法，不记录请求体、查询参数、Cookie、
// Authorization 请求头（设计 24.7）。
func (s *Server) logRequest(r *http.Request, ri *reqInfo, status int, dur time.Duration) {
	if status == 0 {
		status = http.StatusOK
	}
	route := r.Pattern // 例如 "GET /api/v1/servers/{id}/metrics"；未匹配时为空
	if route == "" {
		route = r.Method + " (unmatched)"
	}
	level := slog.LevelInfo
	switch {
	case status >= 500:
		level = slog.LevelError
	case status == http.StatusUnauthorized || dur > slowRequest:
		// 认证失败可能是攻击或配置错误；慢请求说明面板有压力（设计 24.4、24.6）
		level = slog.LevelWarn
	case ri.principal == "agent" && status < 300:
		// Agent 上报量大，成功请求只在 DEBUG 级别记录（设计 24.6）
		level = slog.LevelDebug
	}
	// TODO(A6): 内置 HTTPS 之前面板在 Caddy 后面，这里是代理地址；
	// 需要在“只信任回环代理”的前提下读取 X-Forwarded-For（设计 25、26）。
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	attrs := []any{"component", "http", "request_id", ri.id, "route", route, "status", status,
		"dur_ms", dur.Milliseconds(), "ip", ip}
	if ri.principal != "" {
		attrs = append(attrs, "principal", ri.principal)
		if ri.principalID != 0 {
			attrs = append(attrs, "principal_id", ri.principalID)
		}
	}
	s.log.Log(r.Context(), level, "request", attrs...)
}

// writeJSON 以 200 返回 JSON。
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// writeError 把错误映射为统一的错误响应（设计 43.4），是业务代码返回错误的唯一出口。
//
// 内部错误（500）记录 ERROR 与原因，响应中只给出 request_id，便于用户反馈时对照日志；
// 【安全】原因（SQL、路径等）绝不返回客户端（设计 43.3.1）。
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	ae := asAPIError(err)
	ri := info(r)
	msg := ae.Message
	if msg == "" {
		msg = codeInfo[ae.Code].message
	}
	if ae.Code == CodeInternal {
		msg += "（编号 " + ri.id + "）" // 设计 43.9：附上编号，方便用户反馈
		if ae.Cause != nil {
			s.log.Error("internal error", "component", "http", "request_id", ri.id, "err", ae.Cause)
		}
	} else if ae.Cause != nil {
		s.log.Debug("request rejected", "component", "http", "request_id", ri.id, "code", ae.Code, "err", ae.Cause)
	}
	if ae.Code == CodeRateLimited {
		// TODO(A1): 限流器给出具体的 Retry-After 秒数（设计 27.6.5、43.2）
		w.Header().Set("Retry-After", "60")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ae.Status())
	_ = json.NewEncoder(w).Encode(errorBody{Error: errorPayload{
		Code: ae.Code, Message: msg, RequestID: ri.id, Details: ae.Details,
	}})
}

// errNotFound 用于 /api/ 下未定义的路径：返回 JSON 404，而不是 SPA 的 index.html。
var errNotFound = errorf(CodeNotFound, "")

// isAPIError 判断 err 是否为指定错误码（测试与调用方使用）。
func isAPIError(err error, code Code) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == code
}
