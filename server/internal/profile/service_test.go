package profile

import (
	"errors"
	"testing"

	"github.com/moodiness/rivune/server/internal/password"
)

func TestHashPINUsesPasswordHasher(t *testing.T) {
	pin := "2468"
	hash, err := hashPIN(&pin)
	if err != nil {
		t.Fatalf("hash PIN: %v", err)
	}
	if hash == nil || *hash == pin {
		t.Fatal("PIN was not hashed")
	}
	matches, err := password.Verify(pin, *hash)
	if err != nil {
		t.Fatalf("verify PIN hash: %v", err)
	}
	if !matches {
		t.Fatal("valid PIN did not match its hash")
	}
}

func TestHashPINRejectsInvalidFormats(t *testing.T) {
	for _, pin := range []string{"123", "123456789", "12ab", "１２３４"} {
		t.Run(pin, func(t *testing.T) {
			if _, err := hashPIN(&pin); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected invalid PIN error for %q, got %v", pin, err)
			}
		})
	}
}

func TestHashPINAllowsNoPIN(t *testing.T) {
	hash, err := hashPIN(nil)
	if err != nil || hash != nil {
		t.Fatalf("expected nil PIN to remain unset, got hash %v and error %v", hash, err)
	}
}
