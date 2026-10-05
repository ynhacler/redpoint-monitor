package server

// App 原生推送（设计 15、18.11、30）：告警用设备公钥端到端加密后，经 Push Relay 发给 APNs / FCM。
//
// 【安全 / 隐私】
//   - 内容用设备在配对时提交的 X25519 公钥以 HPKE 加密（internal/push），Relay 与 APNs / FCM 只能看到密文（30.3.4）
//   - Push Token 只保存在本面板（push_devices），每次发送时临时交给 Relay（30.2）
//   - 面板以匿名实例密钥（DATA/push.key，Ed25519）签名请求，供 Relay 限流；密钥不含任何用户或服务器信息（30.2.1）
//   - 只推送设备授权范围内节点的告警（17.3）；设备吊销、解除配对时删除 Push Token 与公钥（19.4）
//   - Relay 只接受 HTTPS（回环地址除外，约束 6）；未配置 --push-relay 时不推送

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"vpsmon/internal/push"
)

// ---- 实例密钥 ----

type pushState struct {
	relay string // Push Relay 地址；空表示未启用

	mu  sync.Mutex
	key ed25519.PrivateKey
}

// instanceKey 读取或生成 DATA/push.key（PKCS#8 PEM，0600）。
func (s *Server) instanceKey() (ed25519.PrivateKey, error) {
	p := s.push
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.key != nil {
		return p.key, nil
	}
	path := filepath.Join(s.store.Dir, "push.key")
	if raw, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(raw)
		if block == nil {
			return nil, errors.New("push.key 格式不正确")
		}
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		ek, ok := k.(ed25519.PrivateKey)
		if !ok {
			return nil, errors.New("push.key 不是 Ed25519 私钥")
		}
		p.key = ek
		return ek, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	p.key = k
	return k, nil
}

// centerID 是本面板的匿名标识（实例公钥的短哈希）；未启用推送时为空。
func (s *Server) centerID() string {
	if s.push.relay == "" {
		return ""
	}
	k, err := s.instanceKey()
	if err != nil {
		return ""
	}
	return push.InstanceID(k.Public().(ed25519.PublicKey))
}

// CheckPushRelay 校验 --push-relay：只接受 https，回环地址除外（约束 6）。
func CheckPushRelay(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("--push-relay 地址格式不正确")
	}
	if u.Scheme == "https" || (u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return nil
	}
	return errors.New("--push-relay 只支持 https:// 地址（本机回环地址除外）")
}

func isLoopbackHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// ---- 存储 ----

type pushTarget struct {
	DeviceID   int64
	DeviceName string
	ScopeType  string
	ScopeValue string
	PublicKey  string
	Provider   string
	Token      string
}

// SavePushDevice 登记或更新设备的推送 Token（每台设备一条）；publicKey 非空时同时更新加密公钥。
func (s *Store) SavePushDevice(deviceID int64, provider, token, publicKey string, now time.Time) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if publicKey != "" {
		if _, err := tx.Exec(`UPDATE app_devices SET push_public_key = ? WHERE id = ?`, publicKey, deviceID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO push_devices (app_device_id, provider, push_token, created_at, updated_at) VALUES (?,?,?,?,?)
		ON CONFLICT (app_device_id) DO UPDATE SET provider = excluded.provider, push_token = excluded.push_token,
		updated_at = excluded.updated_at, last_error = ''`, deviceID, provider, token, now.Unix(), now.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// DeletePushDevice 删除设备的推送 Token（关闭推送、Token 失效、设备吊销时）。
func (s *Store) DeletePushDevice(deviceID int64) error {
	_, err := s.DB.Exec(`DELETE FROM push_devices WHERE app_device_id = ?`, deviceID)
	return err
}

// HasPushDevice 判断设备是否已登记推送。
func (s *Store) HasPushDevice(deviceID int64) bool {
	var n int
	return s.DB.QueryRow(`SELECT COUNT(*) FROM push_devices WHERE app_device_id = ?`, deviceID).Scan(&n) == nil && n > 0
}

// PushTargets 列出可以推送的设备：有效（未吊销、未过期）、有公钥、已登记 Token。
func (s *Store) PushTargets(now time.Time) ([]pushTarget, error) {
	rows, err := s.DB.Query(`SELECT d.id, d.name, d.scope_type, d.scope_value, d.push_public_key, p.provider, p.push_token
		FROM push_devices p JOIN app_devices d ON d.id = p.app_device_id
		WHERE d.revoked_at = 0 AND d.refresh_expires_at > ? AND d.push_public_key <> ''`, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pushTarget
	for rows.Next() {
		var t pushTarget
		if err := rows.Scan(&t.DeviceID, &t.DeviceName, &t.ScopeType, &t.ScopeValue, &t.PublicKey, &t.Provider, &t.Token); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---- 接口（App 设备） ----

var (
	apnsTokenFormat = regexp.MustCompile(`^[0-9a-fA-F]{64,200}$`)
	fcmTokenFormat  = regexp.MustCompile(`^[A-Za-z0-9_:\-]+$`)
)

// handleAppPushPut：PUT /api/v1/app/push，app。登记或更新本机的推送 Token 与公钥。
func (s *Server) handleAppPushPut(w http.ResponseWriter, r *http.Request) {
	d := info(r).device
	var b struct {
		Provider  string `json:"provider"`
		Token     string `json:"token"`
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&b); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	var fe []FieldError
	switch b.Provider {
	case "apns":
		if !apnsTokenFormat.MatchString(b.Token) {
			fe = append(fe, FieldError{Field: "token", Message: "APNs Token 应为十六进制"})
		}
	case "fcm":
		if len(b.Token) < 20 || len(b.Token) > 4096 || !fcmTokenFormat.MatchString(b.Token) {
			fe = append(fe, FieldError{Field: "token", Message: "FCM Token 格式不正确"})
		}
	default:
		fe = append(fe, FieldError{Field: "provider", Message: "推送平台只能是 apns 或 fcm"})
	}
	if b.PublicKey != "" {
		if k, err := base64.StdEncoding.DecodeString(b.PublicKey); err != nil || len(k) != 32 {
			fe = append(fe, FieldError{Field: "public_key", Message: "应为 32 字节的 X25519 公钥（标准 Base64）"})
		}
	} else if !s.store.deviceHasPublicKey(d.ID) {
		fe = append(fe, FieldError{Field: "public_key", Message: "请提交用于加密推送的公钥"})
	}
	if len(fe) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	if err := s.store.SavePushDevice(d.ID, b.Provider, b.Token, b.PublicKey, time.Now()); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "app", ActorID: d.Name, Action: "app.push_enable", TargetType: "app_device", TargetID: d.ID,
		Success: true, Details: map[string]any{"provider": b.Provider}})
	w.WriteHeader(http.StatusNoContent)
}

// handleAppPushDelete：DELETE /api/v1/app/push，app。关闭本机推送。
func (s *Server) handleAppPushDelete(w http.ResponseWriter, r *http.Request) {
	d := info(r).device
	if err := s.store.DeletePushDevice(d.ID); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Store) deviceHasPublicKey(id int64) bool {
	var k string
	return s.DB.QueryRow(`SELECT push_public_key FROM app_devices WHERE id = ?`, id).Scan(&k) == nil && k != ""
}

// ---- 发送 ----

// pushPayload 是加密前的明文（设计 30.3.2）；App 解密后显示 title 与 body，点按打开节点详情。
type pushPayload struct {
	CenterID   string `json:"center_id"`
	EventID    int64  `json:"event_id,omitempty"`
	ServerID   int64  `json:"server_id,omitempty"`
	ServerName string `json:"server_name,omitempty"`
	Kind       string `json:"kind"`
	Severity   string `json:"severity"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	TS         int64  `json:"ts"`
}

// pushWants 判断设备是否接收这条通知：警告以上（恢复通知照常）；只推送授权范围内节点的告警，
// 不针对单个节点的通知（批量离线、免打扰汇总、云账户提醒、面板自检）只发给“全部节点”范围的设备（17.3）。
func (s *Server) pushWants(t pushTarget, m notifyMessage) bool {
	if m.Kind == NotifyTest {
		return false
	}
	if m.Kind != NotifyResolved && m.Severity != "" && severityRank(m.Severity) < severityRank(SeverityWarning) {
		return false
	}
	if t.ScopeType == "all" {
		return true
	}
	if m.ServerID == 0 || len(m.Servers) > 0 {
		return false
	}
	row, err := s.store.GetServer(m.ServerID)
	if err != nil {
		return false
	}
	return APIKey{ScopeType: t.ScopeType, ScopeValue: t.ScopeValue}.scope().allows(*row)
}

// buildPushPayload 组装明文：标题为一行摘要，正文为其余说明（截断到推送大小以内）。
func (s *Server) buildPushPayload(m notifyMessage, now time.Time) ([]byte, error) {
	title := m.title()
	body := strings.TrimSpace(strings.TrimPrefix(m.text(now), title))
	for utf8.RuneCountInString(body) > 500 {
		r := []rune(body)
		body = string(r[:480]) + "…"
	}
	return json.Marshal(pushPayload{CenterID: s.centerID(), EventID: m.EventID, ServerID: m.ServerID, ServerName: m.ServerName,
		Kind: m.Kind, Severity: m.Severity, Title: title, Body: body, TS: now.Unix()})
}

// dispatchApp 把通知推送到已登记的 App 设备（异步，与渠道相同的重试与投递记录）。
func (n *notifier) dispatchApp(m notifyMessage) {
	s := n.s
	if s.push.relay == "" {
		return
	}
	targets, err := s.store.PushTargets(time.Now())
	if err != nil {
		s.log.Error("list push targets failed", "component", "push", "err", err)
		return
	}
	for _, t := range targets {
		if !s.pushWants(t, m) {
			continue
		}
		n.wg.Add(1)
		go func(t pushTarget) {
			defer n.wg.Done()
			n.sem <- struct{}{}
			defer func() { <-n.sem }()
			ctx := context.Background()
			d := &Delivery{ChannelType: "app", ChannelName: "App：" + t.DeviceName, EventID: m.EventID, ServerName: m.ServerName,
				Kind: m.Kind, Title: m.title()}
			n.deliverWith(ctx, d, n.backoff, func() error { return n.sendPush(ctx, t, m) })
		}(t)
	}
}

var errPushGone = permanentError{errors.New("设备的推送 Token 已失效，已删除（App 下次打开时会重新登记）")}

// sendPush 加密并发送一次。Relay 返回 410 时删除该设备的 Push Token，不再重试。
func (n *notifier) sendPush(ctx context.Context, t pushTarget, m notifyMessage) error {
	s := n.s
	key, err := s.instanceKey()
	if err != nil {
		return permanentError{fmt.Errorf("读取实例密钥失败：%v", err)}
	}
	raw, err := base64.StdEncoding.DecodeString(t.PublicKey)
	if err != nil {
		return permanentError{errors.New("设备公钥格式不正确")}
	}
	pub, err := push.ParsePublicKey(raw)
	if err != nil {
		return permanentError{errors.New("设备公钥格式不正确")}
	}
	now := time.Now()
	plain, err := s.buildPushPayload(m, now)
	if err != nil {
		return permanentError{err}
	}
	sealed, err := push.Seal(pub, plain)
	if err != nil {
		return permanentError{err}
	}
	body, _ := json.Marshal(push.Request{Provider: t.Provider, Token: t.Token, Ciphertext: base64.StdEncoding.EncodeToString(sealed),
		Critical: m.Severity == SeverityCritical && m.Kind != NotifyResolved})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.push.relay, "/")+"/v1/push", bytes.NewReader(body))
	if err != nil {
		return permanentError{errors.New("Relay 地址不正确")}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "vpsmon-server/"+s.version)
	push.Sign(req, body, key, now)
	res, err := n.http.Do(req)
	if err != nil {
		return errors.New("无法连接 Push Relay")
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusOK:
		return nil
	case res.StatusCode == push.StatusTokenGone:
		if err := s.store.DeletePushDevice(t.DeviceID); err != nil {
			s.log.Error("delete push device failed", "component", "push", "err", err)
		}
		return errPushGone
	case res.StatusCode == http.StatusBadRequest || res.StatusCode == http.StatusUnauthorized:
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&e)
		return permanentError{fmt.Errorf("Relay 拒绝：%s", e.Error)}
	default:
		return fmt.Errorf("Relay 返回 %d", res.StatusCode)
	}
}
