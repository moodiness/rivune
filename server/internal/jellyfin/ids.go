package jellyfin

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
)

const maximumVirtualKeyBytes = 256

var ErrInvalidID = errors.New("invalid jellyfin compatibility id")

type uuidValue [16]byte

// ServerID is the canonical UUID Rivune exposes for this server instance.
type ServerID struct {
	value uuidValue
}

// ItemID is a canonical Rivune UUID. It is deliberately distinct from IDs for
// virtual compatibility-only items.
type ItemID struct {
	value uuidValue
}

// VirtualItemID identifies an item synthesized by the compatibility adapter.
type VirtualItemID struct {
	value uuidValue
}

// VirtualItemKey is a stable semantic namespace for a synthesized item.
type VirtualItemKey string

const (
	VirtualMoviesView  VirtualItemKey = "view:movies"
	VirtualTVShowsView VirtualItemKey = "view:tv-shows"
)

func ParseServerID(raw string) (ServerID, error) {
	value, err := parseUUID(raw)
	if err != nil {
		return ServerID{}, err
	}
	return ServerID{value: value}, nil
}

func ParseItemID(raw string) (ItemID, error) {
	value, err := parseUUID(raw)
	if err != nil {
		return ItemID{}, err
	}
	return ItemID{value: value}, nil
}

// DeriveVirtualItemID derives an RFC 4122 version 5 UUID from the instance UUID
// and a bounded semantic key. SHA-1 is required by UUIDv5 and is not used here
// for authentication or secrecy.
func DeriveVirtualItemID(instance ServerID, key VirtualItemKey) (VirtualItemID, error) {
	if instance.value == (uuidValue{}) || len(key) == 0 || len(key) > maximumVirtualKeyBytes {
		return VirtualItemID{}, ErrInvalidID
	}
	digest := sha1.New()
	_, _ = digest.Write(instance.value[:])
	_, _ = digest.Write([]byte(key))
	sum := digest.Sum(nil)
	var value uuidValue
	copy(value[:], sum[:len(value)])
	value[6] = (value[6] & 0x0f) | 0x50
	value[8] = (value[8] & 0x3f) | 0x80
	return VirtualItemID{value: value}, nil
}

func (id ServerID) String() string {
	return formatUUID(id.value)
}

func (id ItemID) String() string {
	return formatUUID(id.value)
}

func (id VirtualItemID) String() string {
	return formatUUID(id.value)
}

func parseUUID(raw string) (uuidValue, error) {
	var value uuidValue
	if len(raw) != 36 || raw[8] != '-' || raw[13] != '-' || raw[18] != '-' || raw[23] != '-' {
		return value, ErrInvalidID
	}
	var encoded [32]byte
	copy(encoded[0:8], raw[0:8])
	copy(encoded[8:12], raw[9:13])
	copy(encoded[12:16], raw[14:18])
	copy(encoded[16:20], raw[19:23])
	copy(encoded[20:32], raw[24:36])
	if _, err := hex.Decode(value[:], encoded[:]); err != nil || value == (uuidValue{}) {
		return uuidValue{}, ErrInvalidID
	}
	return value, nil
}

func formatUUID(value uuidValue) string {
	var encoded [36]byte
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded[:])
}
