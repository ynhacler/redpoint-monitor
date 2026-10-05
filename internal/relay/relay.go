// Package relay 是官方 Push Relay（设计 30.2）：无状态地把面板的加密推送转发给 APNs / FCM。
//
// 【安全 / 隐私】
//   - 只在内存中转发，立即丢弃；不保存、不记录请求体、Push Token、实例公钥与来源 IP（30.5）
//   - 只看得到目标 Token 与密文；内容由面板用设备公钥加密（30.3），Relay 无法解密
//   - 面板以匿名实例密钥（Ed25519）签名；按实例公钥在内存中限流（每分钟 30 条、每天 2000 条），重启清零（30.2.1）
//   - 签名中的时间戳 ±5 分钟有效，期间同一签名只接受一次（防重放）
//   - 只保留聚合计数（总请求、成功、失败），不能关联到实例或设备
package relay

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"vpsmon/internal/push"
)

// Config 是 Relay 的配置；APNs 或 FCM 为 nil 表示未启用该平台。
type Config struct {
	APNs      *APNsConfig
	FCM       *FCMConfig
	PerMinute int // 每个实例每分钟的条数上限，默认 30
	PerDay    int // 每个实例每天的条数上限，默认 2000
	HTTP      *http.Client
	Now       func() time.Time
}

// Relay 处理 POST /v1/push 与 GET /healthz。
type Relay struct {
	cfg   Config
	apns  *apnsClient
	fcm   *fcmClient
	limit *limiter

	replayMu sync.Mutex
	replay   map[string]time.Time // 签名 → 收到时间（MaxClockSkew × 2 后清除）

	requests, sent, failed atomic.Int64
}

func New(cfg Config) *Relay {
	if cfg.PerMinute <= 0 {
		cfg.PerMinute = 30
	}
	if cfg.PerDay <= 0 {
		cfg.PerDay = 2000
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 15 * time.Second} // TLS 时自动使用 HTTP/2（APNs 要求）
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	r := &Relay{cfg: cfg, limit: newLimiter(cfg.PerMinute, cfg.PerDay), replay: map[string]time.Time{}}
	if cfg.APNs != nil {
		r.apns = &apnsClient{cfg: *cfg.APNs, http: cfg.HTTP}
	}
	if cfg.FCM != nil {
		r.fcm = &fcmClient{cfg: *cfg.FCM, http: cfg.HTTP}
	}
	return r
}

// Stats 是聚合计数（只用于运行日志），不能关联到实例或设备（设计 30.5）。
func (r *Relay) Stats() string {
	return fmt.Sprintf("requests=%d sent=%d failed=%d", r.requests.Load(), r.sent.Load(), r.failed.Load())
}

// Handler 返回 Relay 的 HTTP 处理器。【隐私】不包含任何访问日志。
func (r *Relay) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/push", r.handlePush)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "apns": r.apns != nil, "fcm": r.fcm != nil,
			"requests": r.requests.Load(), "sent": r.sent.Load(), "failed": r.failed.Load()})
	})
	return mux
}

// 设备 Token 的格式：APNs 为十六进制；FCM 为 Base64URL 字符与 : - _
var (
	apnsToken = regexp.MustCompile(`^[0-9a-fA-F]{64,200}$`)
	fcmToken  = regexp.MustCompile(`^[A-Za-z0-9_:\-]+$`) // 长度另行检查（20～4096），regexp 的重复次数上限为 1000
)

// errTokenGone 表示设备 Token 已失效（App 卸载、重装等），面板应删除它。
var errTokenGone = errors.New("token gone")

// errThrottled 表示 APNs / FCM 暂时限流，面板稍后重试。
var errThrottled = errors.New("throttled")

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func (r *Relay) handlePush(w http.ResponseWriter, req *http.Request) {
	r.requests.Add(1)
	now := r.cfg.Now()
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 8<<10))
	if err != nil {
		r.failed.Add(1)
		fail(w, http.StatusRequestEntityTooLarge, "请求过大")
		return
	}
	pub, err := push.Verify(req.Header, body, now)
	if err != nil {
		r.failed.Add(1)
		fail(w, http.StatusUnauthorized, err.Error())
		return
	}
	if !r.firstUse(req.Header.Get(push.HeaderSignature), now) {
		r.failed.Add(1)
		fail(w, http.StatusUnauthorized, "重复的请求")
		return
	}
	key := sha256.Sum256(pub)
	if ok, wait := r.limit.allow(key, now); !ok {
		r.failed.Add(1)
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		fail(w, http.StatusTooManyRequests, "发送过于频繁")
		return
	}
	var p push.Request
	if err := json.Unmarshal(body, &p); err != nil {
		r.failed.Add(1)
		fail(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	ct, err := base64.StdEncoding.DecodeString(p.Ciphertext)
	if err != nil || len(ct) < 48 || len(ct) > 4096 {
		r.failed.Add(1)
		fail(w, http.StatusBadRequest, "密文格式不正确")
		return
	}
	switch {
	case p.Provider == "apns" && apnsToken.MatchString(p.Token):
		if r.apns == nil {
			err = errNotConfigured
		} else {
			err = r.apns.send(req.Context(), p, now)
		}
	case p.Provider == "fcm" && len(p.Token) >= 20 && len(p.Token) <= 4096 && fcmToken.MatchString(p.Token):
		if r.fcm == nil {
			err = errNotConfigured
		} else {
			err = r.fcm.send(req.Context(), p, now)
		}
	default:
		r.failed.Add(1)
		fail(w, http.StatusBadRequest, "推送平台或 Token 格式不正确")
		return
	}
	switch {
	case err == nil:
		r.sent.Add(1)
		writeJSON(w, http.StatusOK, map[string]any{"status": "sent"})
	case errors.Is(err, errTokenGone):
		r.failed.Add(1)
		fail(w, push.StatusTokenGone, "设备的推送 Token 已失效")
	case errors.Is(err, errThrottled):
		r.failed.Add(1)
		w.Header().Set("Retry-After", "60")
		fail(w, http.StatusServiceUnavailable, "推送平台暂时限流，请稍后重试")
	case errors.Is(err, errNotConfigured):
		r.failed.Add(1)
		fail(w, http.StatusServiceUnavailable, "此 Relay 未启用该推送平台")
	default:
		r.failed.Add(1)
		fail(w, http.StatusBadGateway, "推送平台返回错误")
	}
}

var errNotConfigured = errors.New("provider not configured")

// firstUse 记录签名；时间窗口内重复出现时返回 false（防重放）。过期的记录顺带清除。
func (r *Relay) firstUse(sig string, now time.Time) bool {
	r.replayMu.Lock()
	defer r.replayMu.Unlock()
	if len(r.replay) > 1000 {
		for k, t := range r.replay {
			if now.Sub(t) > 2*push.MaxClockSkew {
				delete(r.replay, k)
			}
		}
	}
	if _, seen := r.replay[sig]; seen {
		return false
	}
	r.replay[sig] = now
	return true
}

// ---- 限流：按实例公钥哈希，内存中计数（30.2.1） ----

type limiter struct {
	mu        sync.Mutex
	perMinute int
	perDay    int
	m         map[[32]byte]*counter
}

type counter struct {
	minute, day       time.Time
	minuteN, dayCount int
}

func newLimiter(perMinute, perDay int) *limiter {
	return &limiter{perMinute: perMinute, perDay: perDay, m: map[[32]byte]*counter{}}
}

func (l *limiter) allow(key [32]byte, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.m) > 100000 { // 内存上限：清除一天以上没有请求的实例
		for k, c := range l.m {
			if now.Sub(c.day) > 24*time.Hour {
				delete(l.m, k)
			}
		}
	}
	c := l.m[key]
	if c == nil {
		c = &counter{minute: now, day: now}
		l.m[key] = c
	}
	if now.Sub(c.minute) >= time.Minute {
		c.minute, c.minuteN = now, 0
	}
	if now.Sub(c.day) >= 24*time.Hour {
		c.day, c.dayCount = now, 0
	}
	if c.dayCount >= l.perDay {
		return false, 24*time.Hour - now.Sub(c.day)
	}
	if c.minuteN >= l.perMinute {
		return false, time.Minute - now.Sub(c.minute)
	}
	c.minuteN++
	c.dayCount++
	return true, 0
}
