package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const tokenEntropyBytes = 32

func newToken(prefix string) (plainText string, digest []byte, err error) {
	entropy := make([]byte, tokenEntropyBytes)
	if _, err := rand.Read(entropy); err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	plainText = prefix + base64.RawURLEncoding.EncodeToString(entropy)
	hash := sha256.Sum256([]byte(plainText))
	return plainText, hash[:], nil
}

func tokenDigest(plainText string) []byte {
	hash := sha256.Sum256([]byte(plainText))
	return hash[:]
}
