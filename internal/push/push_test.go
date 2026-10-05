package push

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSealOpen(t *testing.T) {
	dev, _ := ecdh.X25519().GenerateKey(rand.Reader)
	msg := []byte(`{"server_name":"DMIT-HK","title":"已离线"}`)
	sealed, err := Seal(dev.PublicKey(), msg)
	if err != nil {
		t.Fatal(err)
	}
	// enc（32）‖ 补齐到 256 的明文 ‖ Poly1305 标签（16）
	if len(sealed) != 32+256+16 {
		t.Fatalf("密文长度 %d", len(sealed))
	}
	got, err := Open(dev, sealed)
	if err != nil || !bytes.Equal(got, msg) {
		t.Fatalf("解密 %q %v", got, err)
	}
	// 每次加密使用新的临时密钥：同一明文两次密文不同
	again, _ := Seal(dev.PublicKey(), msg)
	if bytes.Equal(again, sealed) {
		t.Fatal("两次加密的密文不应相同")
	}
	// 篡改、错误的私钥都无法解密
	bad := bytes.Clone(sealed)
	bad[len(bad)-1] ^= 1
	if _, err := Open(dev, bad); err == nil {
		t.Fatal("篡改后应解密失败")
	}
	other, _ := ecdh.X25519().GenerateKey(rand.Reader)
	if _, err := Open(other, sealed); err == nil {
		t.Fatal("错误的私钥应解密失败")
	}
}

// 长度只泄露所在档位（30.3.4）
func TestPaddingBuckets(t *testing.T) {
	dev, _ := ecdh.X25519().GenerateKey(rand.Reader)
	for _, c := range []struct{ n, want int }{{0, 256}, {254, 256}, {255, 512}, {1000, 1024}, {MaxPlaintext, 2048}} {
		sealed, err := Seal(dev.PublicKey(), bytes.Repeat([]byte("x"), c.n))
		if err != nil {
			t.Fatal(err)
		}
		if len(sealed)-48 != c.want {
			t.Errorf("%d 字节应补齐到 %d，实际 %d", c.n, c.want, len(sealed)-48)
		}
	}
	if _, err := Seal(dev.PublicKey(), make([]byte, MaxPlaintext+1)); err == nil {
		t.Fatal("超过最大长度应拒绝")
	}
}

func TestSignVerify(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Unix(1790000000, 0)
	body := []byte(`{"provider":"apns"}`)
	req, _ := http.NewRequest(http.MethodPost, "https://relay.example/v1/push", nil)
	Sign(req, body, key, now)
	pub, err := Verify(req.Header, body, now.Add(time.Minute))
	if err != nil || !pub.Equal(key.Public()) {
		t.Fatalf("校验 %v", err)
	}
	if len(InstanceID(pub)) != 16 {
		t.Fatal("实例标识应为 16 个十六进制字符")
	}
	for name, c := range map[string]struct {
		body []byte
		at   time.Time
		want string
	}{
		"请求体被修改":   {[]byte(`{"provider":"fcm"}`), now, "签名不正确"},
		"时间过早":     {body, now.Add(-6 * time.Minute), "时间戳"},
		"时间过晚（重放）": {body, now.Add(6 * time.Minute), "时间戳"},
	} {
		if _, err := Verify(req.Header, c.body, c.at); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s：%v", name, err)
		}
	}
	h := req.Header.Clone()
	h.Set(HeaderInstance, "AAAA")
	if _, err := Verify(h, body, now); err == nil {
		t.Error("公钥格式错误应拒绝")
	}
}
