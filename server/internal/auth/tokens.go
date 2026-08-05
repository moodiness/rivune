package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
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

// NewProfileContext issues an opaque per-selection capability. Only its digest
// is persisted with the session.
func NewProfileContext() (plainText string, digest []byte, err error) {
	return newToken("rivune_pc_")
}

// MatchesProfileContext verifies that a request holds the capability issued
// for the principal's current profile selection.
func (principal Principal) MatchesProfileContext(plainText string) bool {
	if plainText == "" || len(principal.ProfileContextHash) != sha256.Size {
		return false
	}
	return subtle.ConstantTimeCompare(principal.ProfileContextHash, tokenDigest(plainText)) == 1
}
