package category

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeNameUsesNFKCCaseFoldingAndCollapsedWhitespace(t *testing.T) {
	display, normalized, err := normalizeAndValidateName("  Ｃlients\t  STRASSE  ")
	if err != nil {
		t.Fatalf("normalize category name: %v", err)
	}
	if display != "Clients STRASSE" {
		t.Fatalf("unexpected display name %q", display)
	}
	if normalized != "clients strasse" {
		t.Fatalf("unexpected normalized name %q", normalized)
	}

	_, equivalent, err := normalizeAndValidateName("clients  straße")
	if err != nil {
		t.Fatalf("normalize equivalent category name: %v", err)
	}
	if equivalent != normalized {
		t.Fatalf("equivalent names normalized differently: %q != %q", equivalent, normalized)
	}
}

func TestCategoryValidationRejectsInvalidFields(t *testing.T) {
	if _, _, err := normalizeAndValidateName(strings.Repeat("界", 81)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected overlong Unicode name to be invalid, got %v", err)
	}
	badColor := "red"
	if _, err := normalizeColor(&badColor); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid color error, got %v", err)
	}
	badIcon := "../admin"
	if _, err := normalizeIcon(&badIcon); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected unsafe icon error, got %v", err)
	}
}

func TestNormalizeIconEnforcesCanonicalHyphenSlug(t *testing.T) {
	valid := []string{
		"movie",
		"movie-night",
		strings.Repeat("a", 64),
	}
	for _, value := range valid {
		value := value
		normalized, err := normalizeIcon(&value)
		if err != nil {
			t.Errorf("normalize valid icon %q: %v", value, err)
			continue
		}
		if normalized == nil || *normalized != value {
			t.Errorf("unexpected normalized icon for %q: %v", value, normalized)
		}
	}

	invalid := []string{
		"movie_night",
		"movie--night",
		"movie-",
		strings.Repeat("a", 65),
	}
	for _, value := range invalid {
		value := value
		if _, err := normalizeIcon(&value); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("expected icon %q to be invalid, got %v", value, err)
		}
	}
}

func TestCategoryValidationCanonicalizesColorAndAllowsExplicitClears(t *testing.T) {
	color := " #a1b2c3 "
	normalized, err := normalizeColor(&color)
	if err != nil {
		t.Fatalf("normalize color: %v", err)
	}
	if normalized == nil || *normalized != "#A1B2C3" {
		t.Fatalf("unexpected normalized color: %v", normalized)
	}
	if cleared, err := normalizeOptionalText(nil, 500, "description"); err != nil || cleared != nil {
		t.Fatalf("explicit clear was rejected: %v, %v", cleared, err)
	}
}

func TestMoveValidationRejectsDuplicateAndMalformedIdentifiers(t *testing.T) {
	id := "01234567-89ab-cdef-0123-456789abcdef"
	if err := validateMoveIDs([]string{id, id}, id, "profileIds"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected duplicate move identifiers to be invalid, got %v", err)
	}
	if err := validateMoveIDs([]string{"not-a-uuid"}, id, "profileIds"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected malformed move identifier to be invalid, got %v", err)
	}
}

func TestDeviceCategoryComparisonTreatsUUIDCaseAsEquivalent(t *testing.T) {
	lower := "abcdef01-2345-6789-abcd-ef0123456789"
	upper := "ABCDEF01-2345-6789-ABCD-EF0123456789"
	if !sameUUID(lower, upper) {
		t.Fatal("equivalent UUID spellings were treated as a category move")
	}
	if canonicalUUID(upper) != lower {
		t.Fatalf("UUID was not canonicalized: %q", canonicalUUID(upper))
	}
}
