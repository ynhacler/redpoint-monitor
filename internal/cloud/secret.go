// Package cloud 接入用户自己的云厂商账户，读取费用、流量与实例（设计 44）。
// 只调用各家只读接口；签名用标准库实现，不引入 SDK。
package cloud

// 凭证加密（设计 44.2）。
//
// 【安全】云账户凭证以 AES-256-GCM 加密后存入数据库，密钥在 DATA/secret.key（32 字节随机数，0600，首次使用时生成）。
// 数据库或备份单独泄露时凭证仍是密文；secret.key 不进备份文件，迁移面板时需要单独复制，否则需重新填写凭证。
// 密文格式：版本字节 0x01 ‖ 12 字节随机 nonce ‖ GCM 密文与标签。附加数据（AAD）为账户的用途标记，
// 密文不能被挪到另一个用途下解密。

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const secretVersion = 0x01

// ErrSecretKeyMissing 表示数据库中已有加密凭证，但 secret.key 不存在（例如恢复备份时没有一并迁移）。
var ErrSecretKeyMissing = errors.New("找不到 secret.key：云账户凭证无法解密，请复制原面板的 DATA/secret.key，或重新填写凭证")

// Sealer 加密与解密凭证。
type Sealer struct {
	aead cipher.AEAD
}

// LoadSealer 读取 path 处的密钥；create 为真且文件不存在时生成新密钥（0600）。
// 文件存在但长度不对、或权限对组 / 其他用户可读时拒绝使用（失败即关闭，设计 43.1）。
func LoadSealer(path string, create bool) (*Sealer, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if !create {
			return nil, ErrSecretKeyMissing
		}
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		// O_EXCL：两个进程同时生成时不互相覆盖
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			return LoadSealer(path, false)
		}
		if err != nil {
			return nil, err
		}
		if _, err := f.Write(key); err != nil {
			f.Close()
			os.Remove(path)
			return nil, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else {
		fi, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if fi.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("%s 的权限为 %o，只允许所有者读写：chmod 600 %s", path, fi.Mode().Perm(), path)
		}
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%s 长度不正确（应为 32 字节）", path)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal 加密 plaintext；purpose 作为附加数据，解密时必须相同。
func (s *Sealer) Seal(plaintext []byte, purpose string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{secretVersion}, nonce...)
	return s.aead.Seal(out, nonce, plaintext, []byte(purpose)), nil
}

// ErrSecretCorrupt 表示密文无法解密：被篡改、密钥不匹配（换了 secret.key）或用途不符。
var ErrSecretCorrupt = errors.New("云账户凭证无法解密（secret.key 不匹配或数据已损坏），请重新填写凭证")

// Open 解密 Seal 的结果。
func (s *Sealer) Open(sealed []byte, purpose string) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < 1+n+s.aead.Overhead() || sealed[0] != secretVersion {
		return nil, ErrSecretCorrupt
	}
	pt, err := s.aead.Open(nil, sealed[1:1+n], sealed[1+n:], []byte(purpose))
	if err != nil {
		return nil, ErrSecretCorrupt
	}
	return pt, nil
}
