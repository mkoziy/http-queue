// Package token provides worker bearer token generation, hashing, and verification.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

const tokenBytes = 32

// Generate creates a new random bearer token.
// Returns the plaintext token and its SHA-256 hex hash.
func Generate() (plain, hashed string, err error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(buf)
	hashed = Hash(plain)
	return plain, hashed, nil
}

// Hash returns the SHA-256 hex hash of the plaintext token.
func Hash(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// Verify compares a plaintext token against a stored hash using
// constant-time comparison.
func Verify(plain, hashed string) bool {
	computed := Hash(plain)
	return subtle.ConstantTimeCompare([]byte(computed), []byte(hashed)) == 1
}
