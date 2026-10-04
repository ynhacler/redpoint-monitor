package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"net/http"
	"strings"
)

// Token prefixes make leaked tokens recognisable and prevent cross-use (design 1.6.6).
const (
	PrefixAgent  = "agt_"
	PrefixAPIKey = "api_" // 只读 API Key（设计 45.2）
)

// NewToken returns a 160-bit random token with a type prefix. Only its hash is stored.
func NewToken(prefix string) string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

// HashToken is used for storage and lookup. Tokens are high-entropy, so a plain
// SHA-256 is sufficient (no need for a slow password hash).
func HashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if t, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(t)
	}
	return ""
}
