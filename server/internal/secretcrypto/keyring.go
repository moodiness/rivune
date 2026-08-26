package secretcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const CipherVersionAES256GCM = 1

var ErrDecrypt = errors.New("secret ciphertext could not be decrypted")

type Key struct {
	Version int
	Bytes   []byte
}

func (Key) String() string   { return "[REDACTED encryption key]" }
func (Key) GoString() string { return "[REDACTED encryption key]" }

type Envelope struct {
	Ciphertext    []byte
	CipherVersion int
	KeyVersion    int
}

type BlindIndex struct {
	Version int
	Digest  [sha256.Size]byte
}

type Keyring struct {
	active         int
	aeads          map[int]cipher.AEAD
	activeBlindKey [sha256.Size]byte
}

func (*Keyring) MarshalJSON() ([]byte, error) {
	return nil, errors.New("encryption keyring cannot be serialized")
}
func (*Keyring) String() string   { return "[REDACTED encryption keyring]" }
func (*Keyring) GoString() string { return "[REDACTED encryption keyring]" }

func ParseKeyring(value string) (*Keyring, error) {
	if value == "" {
		return nil, errors.New("RIVUNE_ENCRYPTION_KEYS is required; for a new installation set it to 1:<64-lowercase-hex> using a newly generated 32-byte key; for a legacy upgrade restore the existing RIVUNE_TRACKING_ENCRYPTION_KEY instead")
	}
	if strings.TrimSpace(value) != value {
		return nil, errors.New("RIVUNE_ENCRYPTION_KEYS must not contain whitespace; use comma-separated version:64-lowercase-hex entries")
	}
	parts := strings.Split(value, ",")
	keys := make([]Key, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, errors.New("RIVUNE_ENCRYPTION_KEYS contains an empty entry; remove leading, trailing, or repeated commas")
		}
		versionText, encoded, ok := strings.Cut(part, ":")
		if !ok || versionText == "" || encoded == "" || strings.Contains(encoded, ":") {
			return nil, errors.New("RIVUNE_ENCRYPTION_KEYS entries must use comma-separated version:64-lowercase-hex pairs; for example 1:<64 lowercase hexadecimal characters>")
		}
		version, err := strconv.Atoi(versionText)
		if err != nil || version <= 0 || version > 2147483647 || strconv.Itoa(version) != versionText {
			return nil, errors.New("RIVUNE_ENCRYPTION_KEYS versions must be canonical integers from 1 to 2147483647")
		}
		if len(encoded) != 64 || strings.ToLower(encoded) != encoded {
			return nil, errors.New("RIVUNE_ENCRYPTION_KEYS keys must be exactly 64 lowercase hexadecimal characters (32 bytes)")
		}
		decoded, err := hex.DecodeString(encoded)
		if err != nil {
			return nil, errors.New("RIVUNE_ENCRYPTION_KEYS keys must be exactly 64 lowercase hexadecimal characters (32 bytes)")
		}
		allZero := true
		for _, b := range decoded {
			allZero = allZero && b == 0
		}
		if allZero {
			return nil, errors.New("RIVUNE_ENCRYPTION_KEYS keys must be independently generated and must not be all zero")
		}
		keys = append(keys, Key{Version: version, Bytes: decoded})
	}
	return NewKeyring(keys)
}

func NewKeyring(keys []Key) (*Keyring, error) {
	if len(keys) == 0 {
		return nil, errors.New("encryption keyring must not be empty")
	}
	keyring := &Keyring{active: keys[0].Version, aeads: make(map[int]cipher.AEAD, len(keys))}
	keyMaterial := make(map[[sha256.Size]byte]struct{}, len(keys))
	for _, key := range keys {
		if key.Version <= 0 {
			return nil, errors.New("encryption key version must be positive")
		}
		if _, duplicate := keyring.aeads[key.Version]; duplicate {
			return nil, fmt.Errorf("RIVUNE_ENCRYPTION_KEYS key version %d is duplicated; include each version only once", key.Version)
		}
		if len(key.Bytes) != 32 {
			return nil, fmt.Errorf("encryption key version %d must contain exactly 32 bytes", key.Version)
		}
		allZero := true
		for _, b := range key.Bytes {
			allZero = allZero && b == 0
		}
		if allZero {
			return nil, fmt.Errorf("encryption key version %d must be independently generated and must not be all zero", key.Version)
		}
		material := sha256.Sum256(key.Bytes)
		if _, duplicate := keyMaterial[material]; duplicate {
			return nil, errors.New("RIVUNE_ENCRYPTION_KEYS must use different key material for each version")
		}
		keyMaterial[material] = struct{}{}
		block, err := aes.NewCipher(key.Bytes)
		if err != nil {
			return nil, fmt.Errorf("initialize encryption key version %d: %w", key.Version, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("initialize encryption key version %d AEAD: %w", key.Version, err)
		}
		keyring.aeads[key.Version] = aead
		if key.Version == keyring.active {
			derivation := hmac.New(sha256.New, key.Bytes)
			_, _ = derivation.Write([]byte("rivune:blind-index:key:v1"))
			_ = derivation.Sum(keyring.activeBlindKey[:0])
		}
	}
	return keyring, nil
}

func (k *Keyring) ActiveVersion() int {
	if k == nil {
		return 0
	}
	return k.active
}

func (k *Keyring) BlindIndex(domain string, value []byte) (BlindIndex, error) {
	if k == nil {
		return BlindIndex{}, errors.New("encryption keyring is not configured")
	}
	if domain == "" || strings.TrimSpace(domain) != domain {
		return BlindIndex{}, errors.New("blind index domain must be non-empty and contain no surrounding whitespace")
	}
	digest := hmac.New(sha256.New, k.activeBlindKey[:])
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(domain)))
	_, _ = digest.Write(size[:])
	_, _ = digest.Write([]byte(domain))
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = digest.Write(size[:])
	_, _ = digest.Write(value)
	result := BlindIndex{Version: k.active}
	_ = digest.Sum(result.Digest[:0])
	return result, nil
}

func (k *Keyring) Encrypt(plaintext, associatedData []byte) (Envelope, error) {
	if k == nil {
		return Envelope{}, errors.New("encryption keyring is not configured")
	}
	aead := k.aeads[k.active]
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Envelope{}, fmt.Errorf("generate secret nonce: %w", err)
	}
	ciphertext := aead.Seal(nonce, nonce, plaintext, associatedData)
	return Envelope{Ciphertext: ciphertext, CipherVersion: CipherVersionAES256GCM, KeyVersion: k.active}, nil
}

func (k *Keyring) Decrypt(envelope Envelope, associatedData []byte) ([]byte, error) {
	if k == nil || envelope.CipherVersion != CipherVersionAES256GCM {
		return nil, ErrDecrypt
	}
	aead, ok := k.aeads[envelope.KeyVersion]
	if !ok || len(envelope.Ciphertext) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrDecrypt
	}
	plaintext, err := aead.Open(nil, envelope.Ciphertext[:aead.NonceSize()], envelope.Ciphertext[aead.NonceSize():], associatedData)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}
