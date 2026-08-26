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

func TestKeyringBlindIndexIsDomainSeparatedVersionedAndRotationCold(t *testing.T) {
	material := bytes.Repeat([]byte{0x42}, 32)
	keyring, err := NewKeyring([]Key{{Version: 7, Bytes: material}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := keyring.BlindIndex("semantic-extension", []byte("normalized input"))
	if err != nil {
		t.Fatal(err)
	}
	repeated, _ := keyring.BlindIndex("semantic-extension", []byte("normalized input"))
	otherDomain, _ := keyring.BlindIndex("tracking", []byte("normalized input"))
	otherValue, _ := keyring.BlindIndex("semantic-extension", []byte("other input"))
	if first.Version != 7 || first != repeated || first.Digest == otherDomain.Digest || first.Digest == otherValue.Digest {
		t.Fatalf("blind indexes are not stable and domain separated: first=%v repeated=%v domain=%v value=%v", first, repeated, otherDomain, otherValue)
	}
	rotated, err := NewKeyring([]Key{{Version: 8, Bytes: bytes.Repeat([]byte{0x43}, 32)}, {Version: 7, Bytes: material}})
	if err != nil {
		t.Fatal(err)
	}
	afterRotation, _ := rotated.BlindIndex("semantic-extension", []byte("normalized input"))
	if afterRotation.Version != 8 || afterRotation.Digest == first.Digest {
		t.Fatalf("rotation did not produce a cold blind index: before=%v after=%v", first, afterRotation)
	}
	if _, err := (*Keyring)(nil).BlindIndex("semantic-extension", nil); err == nil {
		t.Fatal("nil keyring produced a blind index")
	}
	if _, err := keyring.BlindIndex(" ", nil); err == nil {
		t.Fatal("empty blind-index domain was accepted")
	}
}

func TestParseKeyringRejectsMalformedOrUnsafeInput(t *testing.T) {
	valid := strings.Repeat("12", 32)
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "missing", want: "for a new installation set it to 1:<64-lowercase-hex>"},
		{name: "whitespace", value: " 2:" + valid, want: "must not contain whitespace"},
		{name: "empty entry", value: "2:" + valid + ",", want: "remove leading, trailing, or repeated commas"},
		{name: "missing separator", value: "2" + valid, want: "comma-separated version:64-lowercase-hex pairs"},
		{name: "invalid version", value: "02:" + valid, want: "canonical integers from 1 to 2147483647"},
		{name: "uppercase", value: "2:" + strings.ToUpper(strings.Repeat("ab", 32)), want: "exactly 64 lowercase hexadecimal characters (32 bytes)"},
		{name: "non hexadecimal", value: "2:" + strings.Repeat("gg", 32), want: "exactly 64 lowercase hexadecimal characters (32 bytes)"},
		{name: "all zero", value: "2:" + strings.Repeat("0", 64), want: "independently generated and must not be all zero"},
		{name: "duplicate version", value: "2:" + valid + ",2:" + strings.Repeat("34", 32), want: "key version 2 is duplicated"},
		{name: "duplicate material", value: "2:" + valid + ",1:" + valid, want: "different key material for each version"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseKeyring(test.value)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseKeyring error = %v, want text %q", err, test.want)
			}
			if strings.Contains(err.Error(), valid) {
				t.Fatal("ParseKeyring error exposed key material")
			}
		})
	}
}
