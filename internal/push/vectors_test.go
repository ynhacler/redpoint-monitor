package push

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"
)

// 跨实现测试向量：App（Dart）用同一份文件校验自己的 HPKE 解密与去补齐（app/test/push_crypto_test.dart 读取它）。
// 重新生成：go test ./internal/push -run TestVectors -update
var update = flag.Bool("update", false, "rewrite testdata/vectors.json")

type vector struct {
	PrivateKey string `json:"private_key"` // 设备 X25519 私钥，Base64
	PublicKey  string `json:"public_key"`
	Sealed     string `json:"sealed"` // enc ‖ 密文，Base64
	Plaintext  string `json:"plaintext"`
}

func TestVectors(t *testing.T) {
	const path = "testdata/vectors.json"
	if *update {
		var vs []vector
		for _, p := range []string{
			`{"title":"🔴 DMIT-HK 已离线","body":"超过 120 秒未收到上报"}`,
			"",
			strings.Repeat("流量", 200), // 跨档位
		} {
			k, _ := ecdh.X25519().GenerateKey(rand.Reader)
			sealed, err := Seal(k.PublicKey(), []byte(p))
			if err != nil {
				t.Fatal(err)
			}
			vs = append(vs, vector{base64.StdEncoding.EncodeToString(k.Bytes()), base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()),
				base64.StdEncoding.EncodeToString(sealed), p})
		}
		b, _ := json.MarshalIndent(vs, "", "  ")
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var vs []vector
	json.Unmarshal(raw, &vs)
	if len(vs) == 0 {
		t.Fatal("没有测试向量")
	}
	for _, v := range vs {
		kb, _ := base64.StdEncoding.DecodeString(v.PrivateKey)
		k, err := ecdh.X25519().NewPrivateKey(kb)
		if err != nil {
			t.Fatal(err)
		}
		sealed, _ := base64.StdEncoding.DecodeString(v.Sealed)
		got, err := Open(k, sealed)
		if err != nil || string(got) != v.Plaintext {
			t.Fatalf("向量解密 %q %v", got, err)
		}
	}
}
