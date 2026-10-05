// Package push 是面板与 Push Relay 共用的推送协议（设计 30）：端到端加密与实例签名。
//
// 加密（30.3）：HPKE（RFC 9180）Base 模式，DHKEM(X25519, HKDF-SHA256) / HKDF-SHA256 / ChaCha20-Poly1305，
// 使用 Go 标准库 crypto/hpke，不自行拼装。密文 = 封装密钥 enc（32 字节）‖ AEAD 密文；info 固定为 Info。
// 明文先补齐到固定长度档位（2 字节大端长度 ‖ 内容 ‖ 0 填充），减少长度泄露（30.3.4）。
// App 端（iOS CryptoKit HPKE、Android）按同一套参数解密。
//
// 签名（30.2.1）：面板的匿名实例密钥（Ed25519）对 “vpsmon-push-v1\n时间戳\nSHA256(请求体)” 签名，
// Relay 校验签名与时间（±5 分钟），并按实例公钥在内存中限流。实例密钥不含任何用户或服务器信息。
package push

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hpke"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Info 是 HPKE 的 info 参数，版本变化时更换，旧版 App 解密失败时显示兜底文字。
const Info = "vpsmon push v1"

// 明文补齐的档位（字节）；超过最大档位的消息拒绝发送。
var buckets = []int{256, 512, 1024, 2048}

// MaxPlaintext 是可发送的最大明文长度。
var MaxPlaintext = buckets[len(buckets)-1] - 2

// pad 补齐到档位：2 字节长度 ‖ 内容 ‖ 0 填充。
func pad(p []byte) ([]byte, error) {
	if len(p) > MaxPlaintext {
		return nil, fmt.Errorf("推送内容过长（%d 字节）", len(p))
	}
	size := buckets[0]
	for _, b := range buckets {
		if len(p)+2 <= b {
			size = b
			break
		}
	}
	out := make([]byte, size)
	binary.BigEndian.PutUint16(out, uint16(len(p)))
	copy(out[2:], p)
	return out, nil
}

func unpad(p []byte) ([]byte, error) {
	if len(p) < 2 {
		return nil, errors.New("明文格式不正确")
	}
	n := int(binary.BigEndian.Uint16(p))
	if n > len(p)-2 {
		return nil, errors.New("明文格式不正确")
	}
	return p[2 : 2+n], nil
}

// ParsePublicKey 解析设备的 X25519 公钥（32 字节）。
func ParsePublicKey(raw []byte) (*ecdh.PublicKey, error) {
	return ecdh.X25519().NewPublicKey(raw)
}

// Seal 用设备公钥加密明文，返回 enc ‖ 密文。
func Seal(device *ecdh.PublicKey, plaintext []byte) ([]byte, error) {
	padded, err := pad(plaintext)
	if err != nil {
		return nil, err
	}
	pk, err := hpke.NewDHKEMPublicKey(device)
	if err != nil {
		return nil, err
	}
	return hpke.Seal(pk, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), []byte(Info), padded)
}

// Open 是 Seal 的逆运算（App 端的参考实现与测试用）。
func Open(device *ecdh.PrivateKey, sealed []byte) ([]byte, error) {
	sk, err := hpke.NewDHKEMPrivateKey(device)
	if err != nil {
		return nil, err
	}
	padded, err := hpke.Open(sk, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), []byte(Info), sealed)
	if err != nil {
		return nil, err
	}
	return unpad(padded)
}

// ---- 面板 → Relay 的请求 ----

// Request 是 POST /v1/push 的请求体。Relay 只在内存中转发，不保存、不记录（30.5）。
type Request struct {
	Provider   string `json:"provider"`   // apns / fcm
	Token      string `json:"token"`      // 设备的 APNs / FCM Push Token
	Ciphertext string `json:"ciphertext"` // 标准 Base64 的 enc ‖ 密文
	Critical   bool   `json:"critical"`   // 严重告警：iOS 以 time-sensitive 级别提醒
}

// 签名相关的请求头。
const (
	HeaderInstance  = "X-Vpsmon-Instance"  // 实例公钥，标准 Base64
	HeaderTimestamp = "X-Vpsmon-Timestamp" // Unix 秒
	HeaderSignature = "X-Vpsmon-Signature" // Ed25519 签名，标准 Base64
)

// MaxClockSkew 是 Relay 接受的时间偏差。
const MaxClockSkew = 5 * time.Minute

// StatusTokenGone 是 Relay 告知面板“该 Push Token 已失效”的状态码，面板应删除这个 Token。
const StatusTokenGone = http.StatusGone

func signedMessage(ts string, body []byte) []byte {
	sum := sha256.Sum256(body)
	return []byte("vpsmon-push-v1\n" + ts + "\n" + hex.EncodeToString(sum[:]))
}

// Sign 为请求设置实例签名头。
func Sign(req *http.Request, body []byte, key ed25519.PrivateKey, now time.Time) {
	ts := strconv.FormatInt(now.Unix(), 10)
	req.Header.Set(HeaderInstance, base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)))
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderSignature, base64.StdEncoding.EncodeToString(ed25519.Sign(key, signedMessage(ts, body))))
}

// Verify 校验请求签名与时间，返回实例公钥。
func Verify(h http.Header, body []byte, now time.Time) (ed25519.PublicKey, error) {
	pub, err := base64.StdEncoding.DecodeString(h.Get(HeaderInstance))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("实例公钥格式不正确")
	}
	sig, err := base64.StdEncoding.DecodeString(h.Get(HeaderSignature))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, errors.New("签名格式不正确")
	}
	ts := h.Get(HeaderTimestamp)
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, errors.New("时间戳格式不正确")
	}
	if d := now.Sub(time.Unix(sec, 0)); d > MaxClockSkew || d < -MaxClockSkew {
		return nil, errors.New("时间戳超出允许范围，请检查面板主机的时钟")
	}
	if !ed25519.Verify(pub, signedMessage(ts, body), sig) {
		return nil, errors.New("签名不正确")
	}
	return ed25519.PublicKey(bytes.Clone(pub)), nil
}

// InstanceID 是实例公钥的短标识（SHA-256 前 8 字节的十六进制），用于 App 区分多个监控中心（payload 的 center_id）。
func InstanceID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}
