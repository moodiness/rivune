package tracking

import (
	"bytes"
	"testing"

	"github.com/moodiness/rivune/server/internal/secretcrypto"
)

func TestTokenCipherAuthenticatesProfileAndProvider(t *testing.T) {
	cipher, err := newTokenCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	first, err := cipher.encrypt("provider-token", "profile-a:trakt:access")
	if err != nil {
		t.Fatal(err)
	}
	second, err := cipher.encrypt("provider-token", "profile-a:trakt:access")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("random nonce was not applied")
	}
	plaintext, err := cipher.decrypt(first, "profile-a:trakt:access")
	if err != nil || plaintext != "provider-token" {
		t.Fatalf("unexpected round trip: %q, %v", plaintext, err)
	}
	if _, err := cipher.decrypt(first, "profile-b:trakt:access"); err == nil {
		t.Fatal("ciphertext was accepted for another profile")
	}
}

func TestTokenCipherDecryptsLegacyVersionOneWithRotatedKeyring(t *testing.T) {
	oldKey := bytes.Repeat([]byte{0x42}, 32)
	oldCipher, err := newTokenCipher(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := oldCipher.encrypt("legacy-token", "profile-a:trakt:access")
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := secretcrypto.NewKeyring([]secretcrypto.Key{
		{Version: 2, Bytes: bytes.Repeat([]byte{0x24}, 32)},
		{Version: 1, Bytes: oldKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := newTokenCipher(keyring)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := rotated.decrypt(ciphertext, "profile-a:trakt:access", 1, 1)
	if err != nil || plaintext != "legacy-token" {
		t.Fatalf("legacy decrypt = %q, %v", plaintext, err)
	}
}
