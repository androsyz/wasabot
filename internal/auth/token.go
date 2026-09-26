package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

const (
	tokenBytes    = 32
	maxTokenChars = 128
)

// NewToken returns a random token for a cookie, and its hash, the only form that is stored.
func NewToken() (token, hash string, err error) {
	token, err = randomString()
	if err != nil {
		return "", "", err
	}
	return token, HashToken(token), nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewCSRFToken returns a random value for a CSRF token.
func NewCSRFToken() (string, error) {
	return randomString()
}

func randomString() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
