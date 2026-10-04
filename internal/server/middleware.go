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
	"reflect"
	"runtime/debug"
	"strconv"
	"strings"
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
	principalID int64  // agent 为节点 ID；admin 为账号 ID
	// Web 管理员的会话与令牌（仅 admin 主体）；令牌只用于派生 CSRF，不记录日志
	session      *session
	sessionToken string
	// hijacked：连接已被接管（WebSocket），日志记为 101，持续时间是连接时长而不是慢请求
	hijacked bool
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
		if r.TLS != nil {
			// 面板直接提供 HTTPS（内置 HTTPS，设计 25）时要求浏览器以后只用 HTTPS 访问；
			// 在反向代理后面时由代理决定，这里不加
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if wantsGzip(r) {
			gz := &gzipWriter{ResponseWriter: w}
			defer gz.close() // 最后执行：panic 时下面写出的错误响应也会被完整压缩
			w = gz
		}
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
	if ri.hijacked {
		status = http.StatusSwitchingProtocols
	}
	route := r.Pattern // 例如 "GET /api/v1/servers/{id}/metrics"；未匹配时为空
	if route == "" {
		route = r.Method + " (unmatched)"
	}
	level := slog.LevelInfo
	switch {
	case status >= 500:
		level = slog.LevelError
	case status == http.StatusUnauthorized || (dur > slowRequest && !ri.hijacked):
		// 认证失败可能是攻击或配置错误；慢请求说明面板有压力（设计 24.4、24.6）
		level = slog.LevelWarn
	case ri.principal == "agent" && status < 300:
		// Agent 上报量大，成功请求只在 DEBUG 级别记录（设计 24.6）
		level = slog.LevelDebug
	}
	attrs := []any{"component", "http", "request_id", ri.id, "route", route, "status", status,
		"dur_ms", dur.Milliseconds(), "ip", clientIP(r)}
	if ri.principal != "" {
		attrs = append(attrs, "principal", ri.principal)
		if ri.principalID != 0 {
			attrs = append(attrs, "principal_id", ri.principalID)
		}
	}
	s.log.Log(r.Context(), level, "request", attrs...)
}

// clientIP 返回客户端地址，用于限流、注册核对与日志（设计 24.6、27.6.3）。
//
// 【安全】只有直接连接来自回环地址（同机的 Caddy 反向代理，设计 26）时才读取 X-Forwarded-For，
// 并且只取最后一项——那是代理自己追加的真实来源；前面的项由客户端控制，可以伪造。
// 面板直接对外时忽略该头，否则任何人都能伪造来源 IP 绕过限流。
func clientIP(r *http.Request) string {
	host := remoteHost(r)
	if !fromTrustedProxy(r) {
		return host
	}
	xff := r.Header.Values("X-Forwarded-For")
	parts := strings.Split(xff[len(xff)-1], ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	if p := net.ParseIP(last); p != nil {
		return p.String()
	}
	return host
}

// fromTrustedProxy 判断请求是否经由同机反向代理转发：直接对端是回环地址且带有 X-Forwarded-For。
func fromTrustedProxy(r *http.Request) bool {
	ip := net.ParseIP(remoteHost(r))
	return ip != nil && ip.IsLoopback() && len(r.Header.Values("X-Forwarded-For")) > 0
}

// remoteHost 返回直接连接的对端地址（不考虑 X-Forwarded-For）。
func remoteHost(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// audit 写一条审计记录，自动带上来源 IP 与 User-Agent。写入失败只记日志，不影响请求本身。
// 管理员操作未指明主体 ID 时，取当前会话的用户名，操作日志据此显示“谁”做的。
func (s *Server) audit(r *http.Request, e AuditEntry) {
	e.ClientIP, e.UserAgent = clientIP(r), r.UserAgent()
	if e.ActorType == "admin" && e.ActorID == "" {
		if se := info(r).session; se != nil {
			e.ActorID = se.User.Username
		}
	}
	if err := s.store.Audit(e, time.Now()); err != nil {
		s.log.Error("audit write failed", "component", "audit", "action", e.Action, "err", err)
	}
}

// writeJSON 以 200 返回 JSON。
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// writeList 写出列表响应（设计 19.0.2）：{"items": [...], "next_cursor": "..."}，next_cursor 为空表示没有更多。
// items 为 nil 时输出空数组而不是 null；extra 是同一响应中的其他字段（如版本列表的同步设置），可为 nil。
func writeList(w http.ResponseWriter, items any, next string, extra map[string]any) {
	if v := reflect.ValueOf(items); !v.IsValid() || (v.Kind() == reflect.Slice && v.IsNil()) {
		items = []struct{}{}
	}
	body := map[string]any{"items": items, "next_cursor": next}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, body)
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
		secs := int(ae.RetryAfter.Seconds() + 0.999) // 向上取整，至少 1 秒
		if secs < 1 {
			secs = 60
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
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
