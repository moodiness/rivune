package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	memory      = 64 * 1024
	iterations  = 3
	parallelism = 2
	saltLength  = 16
	keyLength   = 32
)

var (
	versionField    = fmt.Sprintf("v=%d", argon2.Version)
	parametersField = fmt.Sprintf("m=%d,t=%d,p=%d", memory, iterations, parallelism)
)

var ErrInvalidHash = errors.New("invalid password hash")

func Hash(plainText string) (string, error) {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}

	digest := argon2.IDKey([]byte(plainText), salt, iterations, memory, parallelism, keyLength)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		memory,
		iterations,
		parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest),
	), nil
}

func Verify(plainText, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}

	if parts[2] != versionField || parts[3] != parametersField {
		return false, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != saltLength {
		return false, ErrInvalidHash
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != keyLength {
		return false, ErrInvalidHash
	}

	actual := argon2.IDKey([]byte(plainText), salt, iterations, memory, parallelism, keyLength)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}
