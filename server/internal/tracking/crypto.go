package tracking

import (
	"errors"
	"fmt"

	"github.com/moodiness/rivune/server/internal/secretcrypto"
)

type tokenCipher struct{ keyring *secretcrypto.Keyring }

func newTokenCipher(key any) (*tokenCipher, error) {
	switch value := key.(type) {
	case *secretcrypto.Keyring:
		if value == nil {
			return nil, errors.New("tracking encryption keyring is required")
		}
		return &tokenCipher{keyring: value}, nil
	case []byte:
		keyring, err := secretcrypto.NewKeyring([]secretcrypto.Key{{Version: 1, Bytes: value}})
		if err != nil {
			return nil, fmt.Errorf("initialize tracking token cipher: %w", err)
		}
		return &tokenCipher{keyring: keyring}, nil
	default:
		return nil, errors.New("tracking encryption keyring is required")
	}
}

func (c *tokenCipher) encrypt(plaintext, associatedData string) ([]byte, error) {
	envelope, err := c.keyring.Encrypt([]byte(plaintext), []byte(associatedData))
	if err != nil {
		return nil, fmt.Errorf("encrypt tracking token: %w", err)
	}
	return envelope.Ciphertext, nil
}

func (c *tokenCipher) decrypt(ciphertext []byte, associatedData string, versions ...int) (string, error) {
	cipherVersion := secretcrypto.CipherVersionAES256GCM
	keyVersion := 1
	if len(versions) == 2 {
		cipherVersion, keyVersion = versions[0], versions[1]
	}
	plaintext, err := c.keyring.Decrypt(secretcrypto.Envelope{Ciphertext: ciphertext, CipherVersion: cipherVersion, KeyVersion: keyVersion}, []byte(associatedData))
	if err != nil {
		return "", errorsNewCiphertext()
	}
	return string(plaintext), nil
}

func (c *tokenCipher) cipherVersion() int { return secretcrypto.CipherVersionAES256GCM }
func (c *tokenCipher) keyVersion() int    { return c.keyring.ActiveVersion() }

func errorsNewCiphertext() error { return fmt.Errorf("invalid tracking token ciphertext") }
