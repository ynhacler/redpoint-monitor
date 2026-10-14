package server

// 面板心跳（设计 1.10.3，修订第 159 条）：按设置的间隔向外部监控服务（Healthchecks.io、Uptime Kuma 推送监控、
// Better Stack 等）的地址发 GET 请求。面板自己停止、断网或卡死时心跳中断，由外部服务告警——面板自己的告警此时发不出去。
//
// 【安全】地址由管理员填写，只支持 HTTPS（回环地址除外，CLAUDE.md 约束 6）、不跟随重定向；地址中常含令牌，
// 接口与日志只显示协议与主机（maskURL）。请求不带任何节点数据，只有 User-Agent。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	settingHeartbeat         = "heartbeat"
	heartbeatDefaultInterval = 300
	heartbeatMinInterval     = 60
	heartbeatMaxInterval     = 3600
	heartbeatTimeout         = 10 * time.Second
)

// HeartbeatConfig 是保存在 settings 表中的心跳设置。
type HeartbeatConfig struct {
	Enabled  bool   `json:"enabled"`
	URL      string `json:"url"`
	Interval int    `json:"interval"` // 秒
}

// heartbeatInput 是 PUT 的请求体；URL 为空表示保留原地址。
type heartbeatInput struct {
	Enabled  bool   `json:"enabled"`
	URL      string `json:"url"`
	Interval int    `json:"interval"`
}

// heartbeatView 是返回给 Web 的设置与最近一次结果（地址脱敏）。
type heartbeatView struct {
	Enabled    bool   `json:"enabled"`
	URL        string `json:"url"`
	URLSet     bool   `json:"url_set"`
	Interval   int    `json:"interval"`
	LastAt     int64  `json:"last_at"`
	LastOK     bool   `json:"last_ok"`
	LastStatus int    `json:"last_status"`
	LastError  string `json:"last_error"`
}

// heartbeatState 保存当前设置与最近一次结果（结果只在内存中，重启后清空）。
type heartbeatState struct {
	mu      sync.Mutex
	cfg     HeartbeatConfig
	lastAt  int64
	lastOK  bool
	status  int
	lastErr string
	wake    chan struct{} // 设置变化时唤醒循环，按新的间隔重新计时
	http    *http.Client
}

func newHeartbeatState(store *Store) (*heartbeatState, error) {
	h := &heartbeatState{wake: make(chan struct{}, 1), cfg: HeartbeatConfig{Interval: heartbeatDefaultInterval},
		http: &http.Client{Timeout: heartbeatTimeout,
			// 【安全】不跟随重定向：地址由管理员填写，不被引导到其他地址
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if _, err := store.GetSetting(settingHeartbeat, &h.cfg); err != nil {
		return nil, err
	}
	if h.cfg.Interval == 0 {
		h.cfg.Interval = heartbeatDefaultInterval
	}
	return h, nil
}

func (h *heartbeatState) config() HeartbeatConfig {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg
}

func (h *heartbeatState) set(c HeartbeatConfig) {
	h.mu.Lock()
	h.cfg = c
	h.mu.Unlock()
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func (h *heartbeatState) view() heartbeatView {
	h.mu.Lock()
	defer h.mu.Unlock()
	v := heartbeatView{Enabled: h.cfg.Enabled, URLSet: h.cfg.URL != "", Interval: h.cfg.Interval,
		LastAt: h.lastAt, LastOK: h.lastOK, LastStatus: h.status, LastError: h.lastErr}
	if v.URLSet {
		v.URL = maskURL(h.cfg.URL)
	}
	return v
}

// validate 校验输入并与已保存的设置合并（URL 为空时保留原地址）。
func (in heartbeatInput) validate(cur HeartbeatConfig) (HeartbeatConfig, []FieldError) {
	var errs []FieldError
	c := HeartbeatConfig{Enabled: in.Enabled, URL: strings.TrimSpace(in.URL), Interval: in.Interval}
	if c.URL == "" {
		c.URL = cur.URL
	} else if msg := checkWebhookURL(c.URL); msg != "" {
		if msg == "请填写 Webhook 地址" {
			msg = "地址过长"
		}
		errs = append(errs, FieldError{Field: "url", Message: msg})
	}
	if c.Interval < heartbeatMinInterval || c.Interval > heartbeatMaxInterval {
		errs = append(errs, FieldError{Field: "interval", Message: "间隔应在 1～60 分钟之间"})
	}
	if c.Enabled && c.URL == "" {
		errs = append(errs, FieldError{Field: "url", Message: "启用前请填写地址"})
	}
	return c, errs
}

// heartbeatLoop 按间隔发送心跳；设置变化时立即发送一次并重新计时。面板启动后约 30 秒发送第一次。
func (s *Server) heartbeatLoop(ctx context.Context) {
	wait := 30 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-s.heartbeat.wake:
		}
		c := s.heartbeat.config()
		wait = time.Duration(c.Interval) * time.Second
		if c.Enabled && c.URL != "" {
			s.sendHeartbeat(ctx, c.URL)
		}
	}
}

// sendHeartbeat 发送一次并记录结果；2xx 为成功。失败只记日志：外部服务会因为收不到心跳而告警。
func (s *Server) sendHeartbeat(ctx context.Context, raw string) {
	h := s.heartbeat
	ctx, cancel := context.WithTimeout(ctx, heartbeatTimeout)
	defer cancel()
	status, errMsg := 0, ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err == nil {
		req.Header.Set("User-Agent", "vpsmon-heartbeat/"+s.version)
		var resp *http.Response
		if resp, err = h.http.Do(req); err == nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			status = resp.StatusCode
			if status < 200 || status > 299 {
				errMsg = fmt.Sprintf("对方返回 HTTP %d", status)
			}
		}
	}
	if err != nil {
		errMsg = "无法连接：请检查地址与面板的出站网络"
		s.log.Warn("heartbeat failed", "component", "heartbeat", "url", maskURL(raw), "err", err)
	} else if errMsg != "" {
		s.log.Warn("heartbeat rejected", "component", "heartbeat", "url", maskURL(raw), "status", status)
	}
	h.mu.Lock()
	h.lastAt, h.lastOK, h.status, h.lastErr = time.Now().Unix(), errMsg == "", status, errMsg
	h.mu.Unlock()
}

// handleGetHeartbeat：GET /api/v1/settings/heartbeat，admin。
func (s *Server) handleGetHeartbeat(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.heartbeat.view())
}

// handlePutHeartbeat：PUT /api/v1/settings/heartbeat，admin。修改记入操作日志 setting.update（地址脱敏）。
func (s *Server) handlePutHeartbeat(w http.ResponseWriter, r *http.Request) {
	var in heartbeatInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	c, errs := in.validate(s.heartbeat.config())
	if len(errs) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: errs})
		return
	}
	if err := s.store.PutSetting(settingHeartbeat, c, time.Now()); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.heartbeat.set(c)
	s.audit(r, AuditEntry{ActorType: "admin", Action: "setting.update", Success: true,
		Details: map[string]any{"key": settingHeartbeat, "enabled": c.Enabled, "url": maskURL(c.URL), "interval": c.Interval}})
	writeJSON(w, s.heartbeat.view())
}

// handleTestHeartbeat：POST /api/v1/settings/heartbeat/test，admin。立即发送一次（不论是否启用）并返回结果。
func (s *Server) handleTestHeartbeat(w http.ResponseWriter, r *http.Request) {
	c := s.heartbeat.config()
	if c.URL == "" {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "url", Message: "请先保存地址"}}})
		return
	}
	s.sendHeartbeat(r.Context(), c.URL)
	writeJSON(w, s.heartbeat.view())
}
