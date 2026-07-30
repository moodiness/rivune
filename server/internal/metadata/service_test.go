package metadata

import (
	"errors"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

func TestNormalizeQueryOptionsDefaultsAndCanonicalizes(t *testing.T) {
	options, err := normalizeQueryOptions(QueryOptions{Language: "FR-fr", Region: "fr"})
	if err != nil {
		t.Fatalf("normalize options: %v", err)
	}
	if options.Page != 1 || options.Language != "fr-FR" || options.Region != "FR" {
		t.Fatalf("unexpected normalized options: %+v", options)
	}
}

func TestNormalizeQueryOptionsRejectsUnsafeValues(t *testing.T) {
	tests := []QueryOptions{
		{Page: -1},
		{Page: 501},
		{Language: "fr_FR"},
		{Region: "FRA"},
	}
	for _, options := range tests {
		if _, err := normalizeQueryOptions(options); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid input for %+v, got %v", options, err)
		}
	}
}

func TestRequireActiveProfileRejectsMissingAndExpiredGrants(t *testing.T) {
	profileID := "profile-id"
	expired := time.Now().UTC().Add(-time.Minute)
	if err := requireActiveProfile(auth.Principal{}); !errors.Is(err, ErrProfileRequired) {
		t.Fatalf("expected missing profile rejection, got %v", err)
	}
	if err := requireActiveProfile(auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expired}); !errors.Is(err, ErrProfileRequired) {
		t.Fatalf("expected expired grant rejection, got %v", err)
	}
	active := time.Now().UTC().Add(time.Minute)
	if err := requireActiveProfile(auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &active}); err != nil {
		t.Fatalf("expected active grant, got %v", err)
	}
}
