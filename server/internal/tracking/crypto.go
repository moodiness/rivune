package tracking

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

type tokenCipher struct {
	aead cipher.AEAD
}

func newTokenCipher(key []byte) (*tokenCipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("tracking encryption key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize tracking token cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize tracking token AEAD: %w", err)
	}
	return &tokenCipher{aead: aead}, nil
}

func (c *tokenCipher) encrypt(plaintext, associatedData string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate tracking token nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, []byte(plaintext), []byte(associatedData)), nil
}

func (c *tokenCipher) decrypt(ciphertext []byte, associatedData string) (string, error) {
	nonceSize := c.aead.NonceSize()
	if len(ciphertext) < nonceSize+c.aead.Overhead() {
		return "", errorsNewCiphertext()
	}
	plaintext, err := c.aead.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], []byte(associatedData))
	if err != nil {
		return "", fmt.Errorf("decrypt tracking token: %w", err)
	}
	return string(plaintext), nil
}

func errorsNewCiphertext() error {
	return fmt.Errorf("invalid tracking token ciphertext")
}
