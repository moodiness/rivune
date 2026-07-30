package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
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

	version, err := parseNumber(parts[2], "v=")
	if err != nil || version != argon2.Version {
		return false, ErrInvalidHash
	}

	var parsedMemory uint32
	var parsedIterations uint32
	var parsedParallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &parsedMemory, &parsedIterations, &parsedParallelism); err != nil {
		return false, ErrInvalidHash
	}
	if parsedMemory == 0 || parsedIterations == 0 || parsedParallelism == 0 {
		return false, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return false, ErrInvalidHash
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) < 16 {
		return false, ErrInvalidHash
	}

	actual := argon2.IDKey([]byte(plainText), salt, parsedIterations, parsedMemory, parsedParallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func parseNumber(value, prefix string) (int, error) {
	if !strings.HasPrefix(value, prefix) {
		return 0, ErrInvalidHash
	}
	return strconv.Atoi(strings.TrimPrefix(value, prefix))
}
