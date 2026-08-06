package database

import (
	"strings"
	"testing"
)

func TestProfileCalendarLinksMigrationKeepsOnlyHashedOneToOneCredentials(t *testing.T) {
	contents, err := migrationFiles.ReadFile("migrations/000059_profile_calendar_links.sql")
	if err != nil {
		t.Fatalf("read profile calendar links migration: %v", err)
	}
	normalized := strings.Join(strings.Fields(string(contents)), " ")
	for _, contract := range []string{
		"profile_id uuid PRIMARY KEY REFERENCES profiles(id) ON DELETE CASCADE",
		"token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32)",
		"created_at timestamptz NOT NULL DEFAULT now()",
		"rotated_at timestamptz NOT NULL DEFAULT now()",
	} {
		if !strings.Contains(normalized, contract) {
			t.Fatalf("calendar link migration lacks %q: %s", contract, normalized)
		}
	}
	for _, forbidden := range []string{"token text", "token varchar", "token_plain", "url text"} {
		if strings.Contains(strings.ToLower(normalized), forbidden) {
			t.Fatalf("calendar link migration persists forbidden plaintext field %q: %s", forbidden, normalized)
		}
	}
}
