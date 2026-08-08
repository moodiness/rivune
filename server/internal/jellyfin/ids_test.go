package jellyfin

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestVirtualItemIDsAreStableAndTypeSeparated(t *testing.T) {
	instance, err := ParseServerID("12345678-1234-4234-8234-123456789abc")
	if err != nil {
		t.Fatalf("parse instance ID: %v", err)
	}
	movies, err := DeriveVirtualItemID(instance, VirtualMoviesView)
	if err != nil {
		t.Fatalf("derive movies view: %v", err)
	}
	moviesAgain, err := DeriveVirtualItemID(instance, VirtualMoviesView)
	if err != nil {
		t.Fatalf("derive movies view again: %v", err)
	}
	television, err := DeriveVirtualItemID(instance, VirtualTVShowsView)
	if err != nil {
		t.Fatalf("derive TV view: %v", err)
	}
	collections, err := DeriveVirtualItemID(instance, VirtualCollectionsView)
	if err != nil {
		t.Fatalf("derive Collections view: %v", err)
	}
	if got, want := movies.String(), "49d9c749-cb99-58f8-ad4b-29749e36aa8e"; got != want {
		t.Fatalf("movies ID = %q, want %q", got, want)
	}
	if movies != moviesAgain {
		t.Fatal("same namespace and semantic key produced different IDs")
	}
	if movies == television || movies == collections || television == collections {
		t.Fatal("different virtual item types produced the same ID")
	}
	item, err := ParseItemID(movies.String())
	if err != nil {
		t.Fatalf("parse canonical item UUID: %v", err)
	}
	if reflect.TypeOf(item) == reflect.TypeOf(movies) {
		t.Fatal("canonical and virtual item IDs share a Go type")
	}
	if movies.String()[14] != '5' || !strings.ContainsAny(movies.String()[19:20], "89ab") {
		t.Fatalf("derived ID is not an RFC 4122 UUIDv5: %s", movies.String())
	}
}

func TestCanonicalIDsNormalizeAndRejectInvalidValues(t *testing.T) {
	for _, raw := range []string{
		"A0B1C2D3-E4F5-4678-89AB-0123456789AB",
		"A0B1C2D3E4F5467889AB0123456789AB",
	} {
		item, err := ParseItemID(raw)
		if err != nil {
			t.Fatalf("ParseItemID(%q): %v", raw, err)
		}
		if got, want := item.String(), "a0b1c2d3-e4f5-4678-89ab-0123456789ab"; got != want {
			t.Fatalf("ParseItemID(%q) = %q, want %q", raw, got, want)
		}
	}
	for _, raw := range []string{
		"",
		"not-a-uuid",
		strings.Repeat("a", 31),
		strings.Repeat("a", 33),
		strings.Repeat("a", 31) + "g",
		"a0b1c2d3-e4f5-4678-89ab-0123456789ag",
		"a0b1c2d3e4f5-4678-89ab-0123456789ab",
		"{a0b1c2d3-e4f5-4678-89ab-0123456789ab}",
		"00000000000000000000000000000000",
		"00000000-0000-0000-0000-000000000000",
	} {
		if _, err := ParseItemID(raw); !errors.Is(err, ErrInvalidID) {
			t.Fatalf("ParseItemID(%q) error = %v, want ErrInvalidID", raw, err)
		}
	}
}

func TestVirtualItemKeyIsBounded(t *testing.T) {
	instance, err := ParseServerID("12345678-1234-4234-8234-123456789abc")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []VirtualItemKey{"", VirtualItemKey(strings.Repeat("x", maximumVirtualKeyBytes+1))} {
		if _, err := DeriveVirtualItemID(instance, key); !errors.Is(err, ErrInvalidID) {
			t.Fatalf("DeriveVirtualItemID key length %d error = %v", len(key), err)
		}
	}
}
