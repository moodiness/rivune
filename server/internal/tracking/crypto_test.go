package tracking

import (
	"bytes"
	"testing"
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
