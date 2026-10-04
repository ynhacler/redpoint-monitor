package cloud

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSealerRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	if _, err := LoadSealer(path, false); !errors.Is(err, ErrSecretKeyMissing) {
		t.Fatalf("没有密钥且不创建时应返回 ErrSecretKeyMissing，得到 %v", err)
	}
	s, err := LoadSealer(path, true)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 || fi.Size() != 32 {
		t.Fatalf("密钥文件 %v %v", fi.Mode(), err)
	}
	plain := []byte(`{"access_key_id":"AKIDEXAMPLE","secret_access_key":"wJalr"}`)
	a, _ := s.Seal(plain, "cloud:1")
	b, _ := s.Seal(plain, "cloud:1")
	if bytes.Equal(a, b) {
		t.Fatal("每次加密应使用不同的 nonce")
	}
	if bytes.Contains(a, []byte("AKIDEXAMPLE")) {
		t.Fatal("密文中出现了明文")
	}
	// 重新加载同一密钥文件后仍能解密
	s2, err := LoadSealer(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s2.Open(a, "cloud:1"); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("解密 = %q, %v", got, err)
	}
	// 用途不符、篡改、换了密钥：都拒绝
	if _, err := s2.Open(a, "cloud:2"); !errors.Is(err, ErrSecretCorrupt) {
		t.Fatal("用途不同的密文不应能解密")
	}
	bad := append([]byte(nil), a...)
	bad[len(bad)-1] ^= 1
	if _, err := s2.Open(bad, "cloud:1"); !errors.Is(err, ErrSecretCorrupt) {
		t.Fatal("篡改的密文不应能解密")
	}
	other, _ := LoadSealer(filepath.Join(t.TempDir(), "other.key"), true)
	if _, err := other.Open(a, "cloud:1"); !errors.Is(err, ErrSecretCorrupt) {
		t.Fatal("其他密钥不应能解密")
	}
	if _, err := s2.Open([]byte{1, 2, 3}, "cloud:1"); !errors.Is(err, ErrSecretCorrupt) {
		t.Fatal("过短的密文应拒绝")
	}
}

// 【安全】密钥文件对其他用户可读时拒绝使用
func TestSealerRejectsLoosePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	if _, err := LoadSealer(path, true); err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, 0o644)
	if _, err := LoadSealer(path, true); err == nil {
		t.Fatal("0644 的密钥文件应被拒绝")
	}
}
