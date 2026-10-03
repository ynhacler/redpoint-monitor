package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/blake2b"
)

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustKey(t *testing.T, name string) PublicKey {
	t.Helper()
	k, err := ParsePublicKey(string(mustRead(t, name)))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// 与 minisign 命令行工具生成的签名互通（testdata 由 minisign 0.12 生成，测试密钥，已删除私钥）。
func TestVerifyMinisignCLI(t *testing.T) {
	key := mustKey(t, "test.pub")
	other := mustKey(t, "other.pub")
	msg := mustRead(t, "msg.txt")

	if c, err := Verify([]PublicKey{key}, msg, mustRead(t, "msg.txt.minisig")); err != nil || c != "file:msg.txt" {
		t.Fatalf("默认（预哈希）签名应校验通过：%q %v", c, err)
	}
	if c, err := Verify([]PublicKey{other, key}, mustRead(t, "msg-legacy.txt"), mustRead(t, "msg-legacy.txt.minisig")); err != nil || c != "legacy" {
		t.Fatalf("旧格式签名应校验通过，且按 ID 选择公钥：%q %v", c, err)
	}
	if _, err := Verify([]PublicKey{key}, msg, mustRead(t, "msg.other.minisig")); err != ErrUntrustedKey {
		t.Errorf("其他密钥的签名应拒绝：%v", err)
	}
	if _, err := Verify([]PublicKey{key}, []byte("hello vpsmon release!\n"), mustRead(t, "msg.txt.minisig")); err == nil {
		t.Error("内容被修改应拒绝")
	}
	tampered := bytes.Replace(mustRead(t, "msg.txt.minisig"), []byte("file:msg.txt"), []byte("file:evil.sh"), 1)
	if _, err := Verify([]PublicKey{key}, msg, tampered); err == nil {
		t.Error("可信注释被修改应拒绝")
	}
	if _, err := Verify(nil, msg, mustRead(t, "msg.txt.minisig")); err != ErrUntrustedKey {
		t.Errorf("没有受信任公钥时应拒绝：%v", err)
	}
	for _, bad := range []string{"", "untrusted comment: x\nnot base64", string(msg)} {
		if _, err := Verify([]PublicKey{key}, msg, []byte(bad)); err == nil {
			t.Errorf("格式错误的签名文件应拒绝：%q", bad)
		}
	}
	if _, err := ParsePublicKey("RWQ" + strings.Repeat("A", 10)); err == nil {
		t.Error("格式错误的公钥应拒绝")
	}
}

// testSigner 在内存中生成 minisign 格式的签名，供清单测试使用（仓库中不保存任何私钥）。
type testSigner struct {
	pub  PublicKey
	priv ed25519.PrivateKey
}

func newTestSigner(t *testing.T) testSigner {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var s testSigner
	rand.Read(s.pub.ID[:])
	s.pub.Key, s.priv = pub, priv
	return s
}

func (s testSigner) sign(msg []byte, trusted string) []byte {
	h := blake2b.Sum512(msg)
	sig := ed25519.Sign(s.priv, h[:])
	global := ed25519.Sign(s.priv, append(append([]byte(nil), sig...), trusted...))
	line := base64.StdEncoding.EncodeToString(append(append([]byte("ED"), s.pub.ID[:]...), sig...))
	return []byte("untrusted comment: test\n" + line + "\ntrusted comment: " + trusted + "\n" +
		base64.StdEncoding.EncodeToString(global) + "\n")
}

func TestParsePublicKeyRoundTrip(t *testing.T) {
	s := newTestSigner(t)
	k, err := ParsePublicKey("untrusted comment: minisign public key\n" + s.pub.String() + "\n")
	if err != nil || k.ID != s.pub.ID || !k.Key.Equal(s.pub.Key) {
		t.Fatalf("公钥往返解析：%v", err)
	}
}

const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func manifestJSON(version, min string) []byte {
	return []byte(`{"product":"vpsmon-agent","version":"` + version + `","channel":"stable","released_at":"2026-10-03T00:00:00Z",
"min_upgradable_from":"` + min + `","artifacts":[{"os":"linux","arch":"amd64","file":"vpsmon-agent-linux-amd64","size":100,"sha256":"` + sha + `"}],
"installer":{"file":"agent-` + version + `.sh","size":10,"sha256":"` + sha + `"}}`)
}

func TestVerifyManifest(t *testing.T) {
	s := newTestSigner(t)
	keys := []PublicKey{s.pub}
	data := manifestJSON("0.3.0", "0.1.0")
	m, err := VerifyManifest(keys, data, s.sign(data, "vpsmon-agent 0.3.0"))
	if err != nil || m.Version != "0.3.0" {
		t.Fatalf("签名清单应校验通过：%v", err)
	}
	if a, ok := m.ArtifactFor("linux", "amd64"); !ok || a.Size != 100 {
		t.Errorf("应找到 linux/amd64：%v", a)
	}
	if _, ok := m.ArtifactFor("linux", "mips"); ok {
		t.Error("不支持的架构不应返回")
	}

	evil := bytes.Replace(data, []byte(sha), []byte(strings.Repeat("f", 64)), 1)
	if _, err := VerifyManifest(keys, evil, s.sign(data, "x")); err == nil {
		t.Error("清单被修改（换哈希）应拒绝")
	}
	if _, err := VerifyManifest(nil, data, s.sign(data, "x")); err == nil {
		t.Error("没有官方公钥时应拒绝（开发构建）")
	}
	for name, d := range map[string][]byte{
		"产品名不对":  bytes.Replace(data, []byte(`"vpsmon-agent"`), []byte(`"other"`), 1),
		"未知字段":   bytes.Replace(data, []byte(`"channel"`), []byte(`"exec":"rm -rf /","channel"`), 1),
		"版本号无效":  bytes.Replace(data, []byte(`"0.3.0"`), []byte(`"latest"`), 1),
		"哈希格式不对": bytes.Replace(data, []byte(sha), []byte("abc"), 1),
	} {
		if _, err := VerifyManifest(keys, d, s.sign(d, "x")); err == nil {
			t.Errorf("%s：即使签名有效也应拒绝", name)
		}
	}
}

func TestVersionCompare(t *testing.T) {
	order := []string{"0.1.0-dev", "0.1.0", "v0.1.0-3-gabc1234", "0.1.0-5-gdef5678-dirty", "0.1.1-rc.1", "0.1.1", "0.2.0", "1.0.0"}
	for i := range order {
		for j := range order {
			a, err1 := ParseVersion(order[i])
			b, err2 := ParseVersion(order[j])
			if err1 != nil || err2 != nil {
				t.Fatalf("解析失败：%v %v", err1, err2)
			}
			want := sign(i - j)
			if i != j && (order[i] == "v0.1.0-3-gabc1234" && order[j] == "0.1.0-5-gdef5678-dirty" ||
				order[j] == "v0.1.0-3-gabc1234" && order[i] == "0.1.0-5-gdef5678-dirty") {
				want = 0 // 同一标签之后的不同提交无法比较先后，视为相同
			}
			if got := a.Compare(b); got != want {
				t.Errorf("%s vs %s = %d，应为 %d", order[i], order[j], got, want)
			}
		}
	}
	for _, bad := range []string{"", "dev", "1.2", "v1.2.3.4", "latest"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("%q 应解析失败", bad)
		}
	}
}

func TestCheckUpgrade(t *testing.T) {
	m := &Manifest{Version: "0.3.0", MinUpgradableFrom: "0.2.0"}
	cases := []struct {
		current string
		allow   bool
		ok      bool
	}{
		{"0.2.0", false, true},
		{"v0.2.5-4-gabc1234", false, true},
		{"0.3.0", false, false},         // 已是该版本
		{"0.4.0", false, false},         // 防降级
		{"0.4.0", true, true},           // 本机显式允许降级
		{"0.1.9", false, false},         // 低于 min_upgradable_from
		{"0.1.0-dev", false, false},     // 开发构建低于最低版本
		{"not-a-version", false, false}, // 无法判断是否降级：拒绝
	}
	for _, c := range cases {
		if err := m.CheckUpgrade(c.current, c.allow); (err == nil) != c.ok {
			t.Errorf("从 %s 升级（允许降级 %v）：%v，应 ok=%v", c.current, c.allow, err, c.ok)
		}
	}
}

// TestVerifyReleaseDir 校验一个真实的发布目录（manifest.json + .minisig + 各文件），发布前人工运行：
//
//	VPSMON_RELEASE_DIR=dist VPSMON_RELEASE_PUBKEY="$(tail -1 minisign.pub)" go test ./internal/release -run TestVerifyReleaseDir -v
//
// 未设置环境变量时跳过。
func TestVerifyReleaseDir(t *testing.T) {
	dir, pub := os.Getenv("VPSMON_RELEASE_DIR"), os.Getenv("VPSMON_RELEASE_PUBKEY")
	if dir == "" || pub == "" {
		t.Skip("未设置 VPSMON_RELEASE_DIR / VPSMON_RELEASE_PUBKEY")
	}
	key, err := ParsePublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dir + "/manifest.json")
	sig, _ := os.ReadFile(dir + "/manifest.json.minisig")
	m, err := VerifyManifest([]PublicKey{key}, data, sig)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range append(m.Artifacts, m.Installer) {
		b, err := os.ReadFile(dir + "/" + a.File)
		if err != nil {
			t.Fatal(err)
		}
		if int64(len(b)) != a.Size || sha256Hex(b) != a.SHA256 {
			t.Errorf("%s 的大小或 SHA256 与清单不一致", a.File)
		}
	}
	t.Logf("✓ %s %s：%d 个构建 + 安装脚本，签名与哈希全部一致", m.Product, m.Version, len(m.Artifacts))
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// 官方公钥必须全部能解析，且 ID 互不相同（否则校验时可能选错公钥）。
func TestOfficialKeys(t *testing.T) {
	keys := TrustedKeys()
	if len(keys) != len(officialKeys) || len(keys) < 2 {
		t.Fatalf("官方公钥应为 current + next 两把且全部可解析：%d / %d", len(keys), len(officialKeys))
	}
	if keys[0].ID == keys[1].ID {
		t.Fatal("两把官方公钥的 ID 相同")
	}
	for _, k := range keys {
		t.Logf("官方公钥 ID %s", k.IDHex())
	}
}
