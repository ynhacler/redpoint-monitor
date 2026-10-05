package relay

// APNs 与 FCM 的发送（设计 30.3.3）。只用标准库：JWT 自行签名（ES256 / RS256），HTTP/2 由 net/http 在 TLS 上自动协商。
//
//	APNs   alert + mutable-content: 1；外层文字固定为“服务器告警”（解密失败时的兜底），密文放在自定义字段 c，
//	       由 Notification Service Extension 解密后替换标题与正文；严重告警为 time-sensitive
//	FCM    data message（没有 notification 字段），App 的后台处理器解密后创建本地通知

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"vpsmon/internal/push"
)

// ---- JWT ----

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func signJWT(header, claims map[string]any, sign func(digest []byte) ([]byte, error)) (string, error) {
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	unsigned := b64(h) + "." + b64(c)
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := sign(sum[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + b64(sig), nil
}

// ---- APNs ----

// APNsConfig：Apple 开发者账号中创建的 APNs 认证密钥（.p8）。
type APNsConfig struct {
	KeyID    string
	TeamID   string
	Topic    string // App 的 Bundle ID
	Key      *ecdsa.PrivateKey
	Endpoint string // 默认 https://api.push.apple.com；开发构建用 https://api.sandbox.push.apple.com
}

// ParseAPNsKey 解析 .p8 文件（PKCS#8 的 P-256 私钥）。
func ParseAPNsKey(p8 []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(p8)
	if block == nil {
		return nil, errors.New("APNs 密钥不是 PEM 格式")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("无法解析 APNs 密钥：%w", err)
	}
	ek, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("APNs 密钥应为 P-256 私钥")
	}
	return ek, nil
}

type apnsClient struct {
	cfg  APNsConfig
	http *http.Client

	mu    sync.Mutex
	jwt   string
	jwtAt time.Time
}

// token 返回提供者 JWT：Apple 要求 20～60 分钟之间更新，这里每 40 分钟更新一次。
func (c *apnsClient) token(now time.Time, refresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.jwt != "" && !refresh && now.Sub(c.jwtAt) < 40*time.Minute {
		return c.jwt, nil
	}
	tok, err := signJWT(map[string]any{"alg": "ES256", "kid": c.cfg.KeyID}, map[string]any{"iss": c.cfg.TeamID, "iat": now.Unix()},
		func(d []byte) ([]byte, error) {
			r, s, err := ecdsa.Sign(rand.Reader, c.cfg.Key, d)
			if err != nil {
				return nil, err
			}
			out := make([]byte, 64) // JWS 的 ES256 签名是定长的 r ‖ s，不是 ASN.1
			r.FillBytes(out[:32])
			s.FillBytes(out[32:])
			return out, nil
		})
	if err != nil {
		return "", err
	}
	c.jwt, c.jwtAt = tok, now
	return tok, nil
}

func (c *apnsClient) send(ctx context.Context, p push.Request, now time.Time) error {
	aps := map[string]any{
		"alert":           map[string]string{"title": "服务器告警", "body": "打开 App 查看详情"},
		"mutable-content": 1,
		"sound":           "default",
	}
	if p.Critical {
		aps["interruption-level"] = "time-sensitive"
	}
	body, _ := json.Marshal(map[string]any{"aps": aps, "c": p.Ciphertext})
	endpoint := c.cfg.Endpoint
	if endpoint == "" {
		endpoint = "https://api.push.apple.com"
	}
	for attempt := 0; attempt < 2; attempt++ {
		jwt, err := c.token(now, attempt > 0)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/3/device/"+p.Token, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("authorization", "bearer "+jwt)
		req.Header.Set("apns-topic", c.cfg.Topic)
		req.Header.Set("apns-push-type", "alert")
		req.Header.Set("apns-priority", "10")
		req.Header.Set("apns-expiration", fmt.Sprint(now.Add(24*time.Hour).Unix()))
		res, err := c.http.Do(req)
		if err != nil {
			return errors.New("无法连接 APNs")
		}
		var e struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 4<<10)).Decode(&e)
		res.Body.Close()
		switch {
		case res.StatusCode == http.StatusOK:
			return nil
		case res.StatusCode == http.StatusGone, e.Reason == "BadDeviceToken", e.Reason == "DeviceTokenNotForTopic", e.Reason == "Unregistered":
			return errTokenGone
		case res.StatusCode == http.StatusForbidden && e.Reason == "ExpiredProviderToken" && attempt == 0:
			continue // 立即更新 JWT 后重试一次
		case res.StatusCode == http.StatusTooManyRequests || res.StatusCode == http.StatusServiceUnavailable:
			return errThrottled
		default:
			return fmt.Errorf("APNs 返回 %d %s", res.StatusCode, e.Reason)
		}
	}
	return errors.New("APNs 认证失败")
}

// ---- FCM（HTTP v1） ----

// FCMConfig：Firebase 项目的服务账号（只需 Firebase Cloud Messaging 权限）。
type FCMConfig struct {
	ProjectID   string
	ClientEmail string
	Key         *rsa.PrivateKey
	TokenURL    string // 默认 https://oauth2.googleapis.com/token
	Endpoint    string // 默认 https://fcm.googleapis.com
}

// ParseFCMServiceAccount 解析 Firebase 控制台下载的服务账号 JSON。
func ParseFCMServiceAccount(raw []byte) (*FCMConfig, error) {
	var sa struct {
		ProjectID   string `json:"project_id"`
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		TokenURI    string `json:"token_uri"`
	}
	if err := json.Unmarshal(raw, &sa); err != nil {
		return nil, fmt.Errorf("服务账号 JSON 格式不正确：%w", err)
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil || sa.ProjectID == "" || sa.ClientEmail == "" {
		return nil, errors.New("服务账号缺少 project_id、client_email 或 private_key")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("无法解析服务账号私钥：%w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("服务账号私钥应为 RSA")
	}
	return &FCMConfig{ProjectID: sa.ProjectID, ClientEmail: sa.ClientEmail, Key: rk, TokenURL: sa.TokenURI}, nil
}

type fcmClient struct {
	cfg  FCMConfig
	http *http.Client

	mu      sync.Mutex
	access  string
	expires time.Time
}

// accessToken 用服务账号换取 OAuth2 访问令牌（JWT Bearer 授权），到期前 5 分钟更新。
func (c *fcmClient) accessToken(ctx context.Context, now time.Time, refresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.access != "" && !refresh && now.Before(c.expires.Add(-5*time.Minute)) {
		return c.access, nil
	}
	tokenURL := c.cfg.TokenURL
	if tokenURL == "" {
		tokenURL = "https://oauth2.googleapis.com/token"
	}
	assertion, err := signJWT(map[string]any{"alg": "RS256", "typ": "JWT"}, map[string]any{
		"iss": c.cfg.ClientEmail, "scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud": tokenURL, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}, func(d []byte) ([]byte, error) { return rsa.SignPKCS1v15(rand.Reader, c.cfg.Key, crypto.SHA256, d) })
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {assertion}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.http.Do(req)
	if err != nil {
		return "", errors.New("无法连接 Google OAuth")
	}
	defer res.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 16<<10)).Decode(&out) != nil || out.AccessToken == "" {
		return "", fmt.Errorf("获取 FCM 访问令牌失败（HTTP %d）", res.StatusCode)
	}
	c.access, c.expires = out.AccessToken, now.Add(time.Duration(out.ExpiresIn)*time.Second)
	return c.access, nil
}

func (c *fcmClient) send(ctx context.Context, p push.Request, now time.Time) error {
	priority := "HIGH" // 告警需要及时送达：高优先级 data message 会唤醒 App 的后台处理器
	body, _ := json.Marshal(map[string]any{"message": map[string]any{
		"token":   p.Token,
		"data":    map[string]string{"c": p.Ciphertext},
		"android": map[string]any{"priority": priority, "ttl": "86400s"},
	}})
	endpoint := c.cfg.Endpoint
	if endpoint == "" {
		endpoint = "https://fcm.googleapis.com"
	}
	for attempt := 0; attempt < 2; attempt++ {
		access, err := c.accessToken(ctx, now, attempt > 0)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			endpoint+"/v1/projects/"+url.PathEscape(c.cfg.ProjectID)+"/messages:send", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("Content-Type", "application/json")
		res, err := c.http.Do(req)
		if err != nil {
			return errors.New("无法连接 FCM")
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 16<<10))
		res.Body.Close()
		switch {
		case res.StatusCode == http.StatusOK:
			return nil
		case res.StatusCode == http.StatusUnauthorized && attempt == 0:
			continue // 访问令牌失效：更新后重试一次
		case res.StatusCode == http.StatusNotFound || bytes.Contains(raw, []byte("UNREGISTERED")):
			return errTokenGone
		case res.StatusCode == http.StatusTooManyRequests || res.StatusCode == http.StatusServiceUnavailable:
			return errThrottled
		default:
			return fmt.Errorf("FCM 返回 %d", res.StatusCode)
		}
	}
	return errors.New("FCM 认证失败")
}
