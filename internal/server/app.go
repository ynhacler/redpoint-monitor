package server

// App 接入（设计 8.4、12.3～12.7、17.3、19.2～19.4、23.3）：Web 创建 AK → App 配对换取设备凭证 → 只读访问。
//
// 【安全】
//   - AK 只用于首次配对（默认一次性、1 台设备、1 天内有效），不能当作 API 凭证使用
//   - 设备凭证（dev_ Access Token 30 分钟 + rt_ Refresh Token 90 天、每次刷新轮换）与 Web 会话、API Key、
//     Agent Token、注册码互不通用（约束 3）；只能访问 accessRead 路由与（AK 允许时）静音 / 维护（accessOps）
//   - App 永远只读（约束 5）：不能修改节点、规则、AK、设备或触发升级，v1 没有可授予的管理权限
//   - 授权范围外的节点按不存在处理（404）；设备吊销后 REST、WebSocket、刷新立即失效（设计 19.4）
//   - 只接受 Authorization: Bearer 头，不接受查询串；日志按 dev_ / rt_ / MNT- 前缀脱敏（24.7）

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// ---- 认证 ----

// deviceAuth 校验 dev_ Access Token，成功时返回设备；失败时已写出错误响应。
// 过期返回 token_expired（App 应刷新），设备被吊销 / 授权过期返回 token_revoked（App 应回到配对页，设计 12.7）。
func (s *Server) deviceAuth(w http.ResponseWriter, r *http.Request, tok string) *AppDevice {
	now := time.Now()
	d, err := s.store.LookupDeviceByAccess(tok, now)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return nil
	}
	switch {
	case d == nil:
		s.writeError(w, r, errorf(CodeUnauthorized, "设备凭证无效"))
		return nil
	case d.Status != "active":
		s.writeError(w, r, errorf(CodeTokenRevoked, "当前设备授权已失效，请重新通过 Web 管理端生成 AK 配对"))
		return nil
	case d.accessExpiresAt <= now.Unix():
		s.writeError(w, r, errorf(CodeTokenExpired, ""))
		return nil
	}
	ok, wait, touch := s.appLimit.allow(d.ID, now)
	if !ok {
		s.writeError(w, r, &APIError{Code: CodeRateLimited, Message: "请求过于频繁（每分钟 120 次）", RetryAfter: wait})
		return nil
	}
	if touch {
		if err := s.store.TouchAppDevice(d.ID, now); err != nil {
			s.log.Warn("touch app device failed", "component", "auth", "err", err)
		}
	}
	ri := info(r)
	ri.principal, ri.principalID, ri.apiScope, ri.device = "app", d.ID, d.scope(), d
	return d
}

// app 是 accessApp 路由的认证：只接受 App 设备 Access Token。
func (s *Server) app(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if !strings.HasPrefix(tok, PrefixDevice) {
			s.writeError(w, r, errorf(CodeUnauthorized, ""))
			return
		}
		if s.deviceAuth(w, r, tok) != nil {
			h(w, r)
		}
	})
}

// ops 是 accessOps 路由（静音、维护）的认证：Web 管理员，或 AK 允许低风险操作的 App 设备（设计 8.4.1、17.2）。
// 设备只能操作授权范围内的单个节点，由处理函数按 info(r).device 校验。
func (s *Server) ops(h http.HandlerFunc) http.Handler {
	admin := s.admin(h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if !strings.HasPrefix(tok, PrefixDevice) {
			admin.ServeHTTP(w, r)
			return
		}
		d := s.deviceAuth(w, r, tok)
		if d == nil {
			return
		}
		if !d.AllowLowRiskOps {
			s.writeError(w, r, errorf(CodeForbidden, "此设备的授权不包含静音与维护操作"))
			return
		}
		h(w, r)
	})
}

// ---- AK 管理（Web 管理员） ----

type appKeyBody struct {
	Name            string  `json:"name"`
	ScopeType       string  `json:"scope_type"`
	Group           string  `json:"group"`
	ServerIDs       []int64 `json:"server_ids"`
	AllowLowRiskOps *bool   `json:"allow_low_risk_ops"`
	MaxDevices      int     `json:"max_devices"`
	ExpiresInDays   int     `json:"expires_in_days"`
}

// handleAppKeys：GET /api/v1/app-access-keys，admin。
func (s *Server) handleAppKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.store.ListAppAccessKeys(time.Now())
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeList(w, keys, "", nil)
}

// pairURL 是二维码内容（设计 12.4）：monitor://pair?server=…&ak=…
func pairURL(server, key string) string {
	return "monitor://pair?" + url.Values{"server": {server}, "ak": {key}}.Encode()
}

// handleCreateAppKey：POST /api/v1/app-access-keys，admin + 重新验证。响应中的 AK 只返回这一次。
func (s *Server) handleCreateAppKey(w http.ResponseWriter, r *http.Request) {
	if err := requireReauth(r); err != nil {
		s.writeError(w, r, err)
		return
	}
	var b appKeyBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	k := AppAccessKey{Name: strings.TrimSpace(b.Name), AllowLowRiskOps: true, MaxDevices: 1}
	if b.AllowLowRiskOps != nil {
		k.AllowLowRiskOps = *b.AllowLowRiskOps
	}
	if se := info(r).session; se != nil {
		k.CreatedBy = se.User.Username
	}
	var fe []FieldError
	if k.Name == "" || utf8.RuneCountInString(k.Name) > 64 {
		fe = append(fe, FieldError{Field: "name", Message: "名称为 1～64 个字符"})
	}
	var sfe []FieldError
	k.ScopeType, k.ScopeValue, sfe = s.parseScope(b.ScopeType, b.Group, b.ServerIDs)
	fe = append(fe, sfe...)
	if b.MaxDevices != 0 {
		k.MaxDevices = b.MaxDevices
	}
	if k.MaxDevices < 1 || k.MaxDevices > 10 {
		fe = append(fe, FieldError{Field: "max_devices", Message: "最大配对设备数为 1～10"})
	}
	days := b.ExpiresInDays
	if days == 0 {
		days = 1
	}
	if days < 1 || days > 90 {
		fe = append(fe, FieldError{Field: "expires_in_days", Message: "有效期为 1～90 天"})
	}
	if len(fe) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	now := time.Now()
	k.ExpiresAt = now.Add(time.Duration(days) * 24 * time.Hour).Unix()
	key, err := s.store.CreateAppAccessKey(&k, now)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "app_key.create", TargetType: "app_access_key", TargetID: k.ID, Success: true,
		Details: map[string]any{"name": k.Name, "hint": k.Hint, "scope_type": k.ScopeType, "scope_value": k.ScopeValue,
			"allow_low_risk_ops": k.AllowLowRiskOps, "max_devices": k.MaxDevices, "expires_at": k.ExpiresAt}})
	server := s.panelURL(r)
	writeJSONStatus(w, http.StatusCreated, map[string]any{"access_key": key, "server_url": server,
		"pair_url": pairURL(server, key), "app_access_key": k})
}

// handleRevokeAppKey：POST /api/v1/app-access-keys/{id}/revoke，admin。
func (s *Server) handleRevokeAppKey(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var b struct {
		RevokeDevices bool `json:"revoke_devices"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&b); err != nil {
			s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
			return
		}
	}
	k, ids, err := s.store.RevokeAppAccessKey(id, b.RevokeDevices, time.Now())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	closed := map[int64]bool{}
	for _, d := range ids {
		closed[d] = true
	}
	s.ws.closeDevices(closed)
	s.audit(r, AuditEntry{ActorType: "admin", Action: "app_key.revoke", TargetType: "app_access_key", TargetID: id, Success: true,
		Details: map[string]any{"name": k.Name, "hint": k.Hint, "revoked_devices": ids}})
	writeJSON(w, map[string]int{"revoked_devices": len(ids)})
}

// ---- 设备管理（Web 管理员） ----

// handleAppDevices：GET /api/v1/app-devices，admin。
func (s *Server) handleAppDevices(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListAppDevices(time.Now())
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeList(w, list, "", nil)
}

// handleRevokeAppDevice：POST /api/v1/app-devices/{id}/revoke，admin。立即生效（REST、刷新、WebSocket）。
func (s *Server) handleRevokeAppDevice(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	d, err := s.store.RevokeAppDevice(id, "admin", time.Now())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.ws.closeDevices(map[int64]bool{id: true})
	s.audit(r, AuditEntry{ActorType: "admin", Action: "app_device.revoke", TargetType: "app_device", TargetID: id, Success: true,
		Details: map[string]any{"name": d.Name, "platform": d.Platform}})
	w.WriteHeader(http.StatusNoContent)
}

// ---- 配对与刷新（App，凭请求体中的 AK / Refresh Token 认证） ----

type pairBody struct {
	AccessKey string `json:"access_key"`
	Device    struct {
		Name       string `json:"name"`
		Platform   string `json:"platform"`
		AppVersion string `json:"app_version"`
	} `json:"device"`
	PushPublicKey string `json:"push_public_key"`
}

// appScopeView 是返回给 App 的授权范围。
type appScopeView struct {
	Type            string `json:"type"`
	Value           string `json:"value"`
	AllowLowRiskOps bool   `json:"allow_low_risk_ops"`
}

func scopeView(d AppDevice) appScopeView {
	return appScopeView{Type: d.ScopeType, Value: d.ScopeValue, AllowLowRiskOps: d.AllowLowRiskOps}
}

func tokensView(d AppDevice, t appTokens, now time.Time) map[string]any {
	return map[string]any{"device_id": d.ID, "access_token": t.access, "refresh_token": t.refresh,
		"expires_in": t.accessExp - now.Unix(), "refresh_expires_in": t.refreshExp - now.Unix(), "scope": scopeView(d)}
}

// handlePair：POST /api/v1/app/pair，凭 AK 认证（设计 12.3、19.3）。按 IP 限流，失败过多临时封禁。
// AK 无效的各种原因统一返回 access_key_invalid（不帮助猜测）。
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if ok, wait := s.pairLimit.allow(ip); !ok {
		s.writeError(w, r, &APIError{Code: CodeRateLimited, RetryAfter: wait})
		return
	}
	var b pairBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&b); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	key := normalizeAccessKey(b.AccessKey)
	hint := ""
	if accessKeyFormat.MatchString(key) {
		hint = key[:8] + "-****-" + key[len(key)-4:]
	}
	name := strings.TrimSpace(b.Device.Name)
	var fe []FieldError
	if name == "" || utf8.RuneCountInString(name) > 64 {
		fe = append(fe, FieldError{Field: "device.name", Message: "设备名称为 1～64 个字符"})
	}
	if b.Device.Platform != "ios" && b.Device.Platform != "android" {
		fe = append(fe, FieldError{Field: "device.platform", Message: "平台只能是 ios 或 android"})
	}
	if utf8.RuneCountInString(b.Device.AppVersion) > 32 {
		fe = append(fe, FieldError{Field: "device.app_version", Message: "版本号最多 32 个字符"})
	}
	if b.PushPublicKey != "" {
		if k, err := base64.StdEncoding.DecodeString(b.PushPublicKey); err != nil || len(k) != 32 {
			fe = append(fe, FieldError{Field: "push_public_key", Message: "应为 32 字节的 X25519 公钥（标准 Base64）"})
		}
	}
	if len(fe) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	fail := func(reason string) {
		s.pairLimit.fail(ip)
		s.audit(r, AuditEntry{ActorType: "app", Action: "app.pair", Success: false,
			Details: map[string]any{"hint": hint, "reason": reason, "device": name, "platform": b.Device.Platform}})
		s.writeError(w, r, errorf(CodeAccessKeyInvalid, ""))
	}
	if hint == "" {
		fail("malformed")
		return
	}
	now := time.Now()
	d, t, err := s.store.PairDevice(PairRequest{KeyHash: HashToken(key), Name: name, Platform: b.Device.Platform,
		AppVersion: b.Device.AppVersion, PushPublicKey: b.PushPublicKey}, now)
	if errors.Is(err, errAccessKeyInvalid) {
		fail("invalid_used_or_expired")
		return
	}
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	info(r).principal, info(r).principalID = "app", d.ID
	s.audit(r, AuditEntry{ActorType: "app", ActorID: d.Name, Action: "app.pair", TargetType: "app_device", TargetID: d.ID,
		Success: true, Details: map[string]any{"hint": hint, "access_key_id": d.AccessKeyID, "platform": d.Platform,
			"app_version": d.AppVersion, "scope_type": d.ScopeType, "scope_value": d.ScopeValue}})
	s.log.Info("app paired", "component", "app", "device_id", d.ID, "platform", d.Platform, "ip", ip)
	writeJSON(w, tokensView(d, t, now))
}

// handleRefresh：POST /api/v1/app/token/refresh，凭 Refresh Token 认证（设计 12.5）。
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if ok, wait := s.pairLimit.allow(ip); !ok {
		s.writeError(w, r, &APIError{Code: CodeRateLimited, RetryAfter: wait})
		return
	}
	var b struct {
		RefreshToken string `json:"refresh_token"`
		AppVersion   string `json:"app_version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<10)).Decode(&b); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	if !strings.HasPrefix(b.RefreshToken, PrefixRefresh) || utf8.RuneCountInString(b.AppVersion) > 32 {
		s.pairLimit.fail(ip)
		s.writeError(w, r, errorf(CodeTokenRevoked, "当前设备授权已失效，请重新通过 Web 管理端生成 AK 配对"))
		return
	}
	now := time.Now()
	d, t, outcome, err := s.store.RefreshDevice(b.RefreshToken, b.AppVersion, now)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	switch outcome {
	case refreshReused:
		s.ws.closeDevices(map[int64]bool{d.ID: true})
		s.audit(r, AuditEntry{ActorType: "app", Action: "app.refresh_reuse", TargetType: "app_device", TargetID: d.ID, Success: false,
			Details: map[string]any{"reason": "旧 Refresh Token 在轮换后再次使用，已吊销设备"}})
		s.log.Warn("app refresh token reused, device revoked", "component", "app", "ip", ip)
		fallthrough
	case refreshInvalid:
		s.pairLimit.fail(ip)
		s.writeError(w, r, errorf(CodeTokenRevoked, "当前设备授权已失效，请重新通过 Web 管理端生成 AK 配对"))
		return
	}
	info(r).principal, info(r).principalID = "app", d.ID
	writeJSON(w, tokensView(d, t, now))
}

// ---- 设备自身（App） ----

// handleAppMe：GET /api/v1/app/me，app。
func (s *Server) handleAppMe(w http.ResponseWriter, r *http.Request) {
	d := info(r).device
	writeJSON(w, map[string]any{"device_id": d.ID, "name": d.Name, "platform": d.Platform, "scope": scopeView(*d),
		"panel_version": s.version})
}

// handleUnpair：POST /api/v1/app/unpair，app。App 主动解除本机配对。
func (s *Server) handleUnpair(w http.ResponseWriter, r *http.Request) {
	d := info(r).device
	if _, err := s.store.RevokeAppDevice(d.ID, "app", time.Now()); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.ws.closeDevices(map[int64]bool{d.ID: true})
	s.audit(r, AuditEntry{ActorType: "app", ActorID: d.Name, Action: "app.unpair", TargetType: "app_device", TargetID: d.ID,
		Success: true, Details: map[string]any{"platform": d.Platform}})
	w.WriteHeader(http.StatusNoContent)
}
