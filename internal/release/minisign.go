// Package release 校验官方发布（设计 27.5.5、29.7）：minisign 签名、签名的发布清单、版本比较。
//
// 【安全】发布私钥只由开发者离线保管，不进入仓库、CI 或面板；本包只做校验，不包含任何签名能力。
// Agent 与面板都用编译进程序的官方公钥（keys.go）校验，校验失败一律拒绝（设计 43.1 安全检查失败即拒绝）。
package release

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// minisign 格式（https://jedisct1.github.io/minisign/）：
//
//	公钥：base64( "Ed" | key_id[8] | ed25519 公钥[32] )
//	签名文件：
//	  untrusted comment: …
//	  base64( "ED" | key_id[8] | 签名[64] )     "ED" 表示对文件的 BLAKE2b-512 摘要签名（默认）；"Ed" 为旧格式，直接签文件
//	  trusted comment: …
//	  base64( 全局签名[64] )                    对 “签名[64] | 可信注释” 的签名，防止可信注释被篡改

// PublicKey 是一把 minisign 公钥。
type PublicKey struct {
	ID  [8]byte
	Key ed25519.PublicKey
}

// ParsePublicKey 解析 minisign 公钥：可以是公钥文件的全部内容，也可以只是 base64 那一行。
func ParsePublicKey(s string) (PublicKey, error) {
	var pk PublicKey
	line := ""
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "untrusted comment:") {
			line = l
		}
	}
	b, err := base64.StdEncoding.DecodeString(line)
	if err != nil || len(b) != 42 || string(b[:2]) != "Ed" {
		return pk, errors.New("不是有效的 minisign 公钥")
	}
	copy(pk.ID[:], b[2:10])
	pk.Key = ed25519.PublicKey(append([]byte(nil), b[10:]...))
	return pk, nil
}

// Signature 是解析后的 minisign 签名文件。
type Signature struct {
	Prehashed      bool
	KeyID          [8]byte
	Sig            []byte
	TrustedComment string
	GlobalSig      []byte
}

// ParseSignature 解析 .minisig 文件内容。
func ParseSignature(data []byte) (Signature, error) {
	var s Signature
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(string(data)), "\r\n", "\n"), "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[0], "untrusted comment:") || !strings.HasPrefix(lines[2], "trusted comment: ") {
		return s, errors.New("签名文件格式错误")
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil || len(b) != 74 {
		return s, errors.New("签名文件格式错误")
	}
	switch string(b[:2]) {
	case "ED":
		s.Prehashed = true
	case "Ed":
	default:
		return s, errors.New("不支持的签名算法")
	}
	copy(s.KeyID[:], b[2:10])
	s.Sig = b[10:]
	s.TrustedComment = strings.TrimPrefix(lines[2], "trusted comment: ")
	if s.GlobalSig, err = base64.StdEncoding.DecodeString(strings.TrimSpace(lines[3])); err != nil || len(s.GlobalSig) != 64 {
		return s, errors.New("签名文件格式错误")
	}
	return s, nil
}

// ErrUntrustedKey 表示签名不是由任何受信任的公钥签发。
var ErrUntrustedKey = errors.New("签名不是由官方密钥签发")

// Verify 用受信任公钥中 ID 匹配的那把校验 msg 的签名，返回签名中的可信注释。
// 内容签名与全局签名（覆盖可信注释）都必须有效。
func Verify(keys []PublicKey, msg, sigFile []byte) (trustedComment string, err error) {
	s, err := ParseSignature(sigFile)
	if err != nil {
		return "", err
	}
	var key *PublicKey
	for i := range keys {
		if keys[i].ID == s.KeyID {
			key = &keys[i]
		}
	}
	if key == nil {
		return "", ErrUntrustedKey
	}
	signed := msg
	if s.Prehashed {
		h := blake2b.Sum512(msg)
		signed = h[:]
	}
	if !ed25519.Verify(key.Key, signed, s.Sig) {
		return "", errors.New("签名校验失败：内容被修改或签名不匹配")
	}
	if !ed25519.Verify(key.Key, append(append([]byte(nil), s.Sig...), s.TrustedComment...), s.GlobalSig) {
		return "", errors.New("签名校验失败：可信注释被修改")
	}
	return s.TrustedComment, nil
}

// String 以 minisign 公钥的 base64 形式输出（用于日志与界面展示公钥 ID）。
func (k PublicKey) String() string {
	return base64.StdEncoding.EncodeToString(bytes.Join([][]byte{[]byte("Ed"), k.ID[:], k.Key}, nil))
}

// IDHex 返回公钥 ID 的十六进制（与 minisign 显示的 key ID 一致，大端）。
func (k PublicKey) IDHex() string {
	var b strings.Builder
	for i := len(k.ID) - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "%02X", k.ID[i])
	}
	return b.String()
}
