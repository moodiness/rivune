package secretcrypto

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestParseKeyringEncryptsWithActiveAndDecryptsRetainedVersion(t *testing.T) {
	active := strings.Repeat("12", 32)
	retained := strings.Repeat("34", 32)
	keyring, err := ParseKeyring("2:" + active + ",1:" + retained)
	if err != nil {
		t.Fatal(err)
	}
	if keyring.ActiveVersion() != 2 {
		t.Fatalf("active version = %d, want 2", keyring.ActiveVersion())
	}
	envelope, err := keyring.Encrypt([]byte("credential"), []byte("instance:aad"))
	if err != nil {
		t.Fatal(err)
	}
	if envelope.CipherVersion != 1 || envelope.KeyVersion != 2 {
		t.Fatalf("unexpected envelope metadata: %+v", envelope)
	}
	plaintext, err := keyring.Decrypt(envelope, []byte("instance:aad"))
	if err != nil || !bytes.Equal(plaintext, []byte("credential")) {
		t.Fatalf("round trip = %q, %v", plaintext, err)
	}
	if _, err := keyring.Decrypt(envelope, []byte("other:aad")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong AAD error = %v, want ErrDecrypt", err)
	}

	oldKeyring, err := ParseKeyring("1:" + retained)
	if err != nil {
		t.Fatal(err)
	}
	oldEnvelope, err := oldKeyring.Encrypt([]byte("legacy"), []byte("legacy-aad"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err = keyring.Decrypt(oldEnvelope, []byte("legacy-aad"))
	if err != nil || string(plaintext) != "legacy" {
		t.Fatalf("retained-version decrypt = %q, %v", plaintext, err)
	}
}

func TestParseKeyringRejectsMalformedOrUnsafeInput(t *testing.T) {
	valid := strings.Repeat("12", 32)
	for _, value := range []string{
		"", " 2:" + valid, "2:" + valid + " ", "2:" + valid + ",",
		"0:" + valid, "02:" + valid, "x:" + valid, "2:",
		"2:" + strings.ToUpper(strings.Repeat("ab", 32)), "2:" + strings.Repeat("0", 64),
		"2:" + valid + ",2:" + strings.Repeat("34", 32),
		"2:" + valid + ",1:" + valid,
	} {
		if _, err := ParseKeyring(value); err == nil {
			t.Fatalf("ParseKeyring(%q) succeeded", value)
		}
	}
}
