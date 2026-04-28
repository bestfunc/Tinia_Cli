// PKCE (RFC 7636) helper — code_verifier 32 字节随机，code_challenge 是
// SHA256(code_verifier) 的 base64url（无 padding）。

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// NewPKCE 返回 (verifier, challenge)。
func NewPKCE() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return
}

// RandomString 生成随机 base64url 字符串（state / nonce 用）。
func RandomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
