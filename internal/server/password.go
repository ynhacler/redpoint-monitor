package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// 管理员密码（设计 17.4、23.4）。
//
// 【安全】使用 Argon2id，以 PHC 字符串保存（$argon2id$v=19$m=…,t=…,p=…$salt$hash），
// 参数随哈希一起存储：以后调高参数时旧密码仍可验证，并在下次登录时自动升级。

// argon2 参数：OWASP 推荐的低内存档（19 MiB、2 次迭代、1 线程）。
// 面板常跑在 512 MB 的小 VPS 上，登录又有限流（设计 17.4），这一档在安全与资源占用之间合适。
const (
	argonMemory  = 19 * 1024 // KiB
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

// 密码长度：最少 12 位（设计 17.4）；上限防止超长输入消耗 CPU。
const (
	minPasswordLen = 12
	maxPasswordLen = 256
)

var errBadHash = errors.New("unsupported password hash")

// HashPassword 生成密码的 Argon2id PHC 字符串。
func HashPassword(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword 校验密码。needsRehash 为 true 表示哈希使用的参数低于当前设置，调用方应重新哈希保存。
//
// 【安全】比较使用常数时间，避免通过响应时间推测哈希内容。
func VerifyPassword(pw, encoded string) (ok, needsRehash bool, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, false, errBadHash
	}
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || t == 0 || p == 0 {
		return false, false, errBadHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, false, errBadHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, false, errBadHash
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	ok = subtle.ConstantTimeCompare(got, want) == 1
	return ok, ok && (m < argonMemory || t < argonTime), nil
}

// dummyHash 用于用户名不存在时仍执行一次同等耗时的哈希计算，
// 【安全】避免通过响应时间判断用户名是否存在。
var dummyHash, _ = HashPassword("dummy-password-for-timing")

// validatePassword 检查新密码是否符合要求，返回中文提示；符合时返回空字符串。
func validatePassword(pw string) string {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < minPasswordLen:
		return fmt.Sprintf("密码至少 %d 位", minPasswordLen)
	case len(pw) > maxPasswordLen:
		return "密码过长"
	case strings.TrimSpace(pw) != pw:
		return "密码首尾不能有空格"
	}
	return ""
}

// RandomPassword 生成 20 位随机初始密码（约 119 bit），用于 init 与 reset-password，
// 只输出一次，并要求首次登录后修改（设计 17.4）。去掉了易混淆的 0/O、1/l/I。
func RandomPassword() string {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	// 拒绝采样：只接受小于 len*4 的字节，避免取模带来的分布偏差
	limit := byte(len(alphabet) * (256 / len(alphabet)))
	out := make([]byte, 0, 20)
	buf := make([]byte, 32)
	for len(out) < 20 {
		if _, err := rand.Read(buf); err != nil {
			panic(err) // 系统随机源不可用时无法安全生成凭证
		}
		for _, c := range buf {
			if c < limit && len(out) < 20 {
				out = append(out, alphabet[int(c)%len(alphabet)])
			}
		}
	}
	return string(out)
}
