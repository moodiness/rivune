package database

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestJellyfinDisplayPreferencesMigrationContract(t *testing.T) {
	contents, err := migrationFiles.ReadFile("migrations/000068_jellyfin_display_preferences.sql")
	if err != nil {
		t.Fatalf("read display preferences migration: %v", err)
	}
	normalized := strings.Join(strings.Fields(string(contents)), " ")
	for _, contract := range []string{
		"CREATE TABLE jellyfin_display_preferences",
		"user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE",
		"profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE",
		"char_length(client) BETWEEN 1 AND 64",
		"char_length(display_preferences_id) BETWEEN 1 AND 64",
		"display_preferences_id ~ '^[A-Za-z0-9._-]+$'",
		"jsonb_typeof(preferences) = 'object'",
		"octet_length(preferences::text) <= 32768",
		"PRIMARY KEY (user_id, profile_id, client, display_preferences_id)",
	} {
		if !strings.Contains(normalized, contract) {
			t.Fatalf("display preference migration lacks %q: %s", contract, normalized)
		}
	}
}

func TestJellyfinDisplayPreferencesSchemaCreatesAndDropsTransactionally(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run display preference migration schema tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open display preference migration database: %v", err)
	}
	defer pool.Close()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate display preference schema: %v", err)
	}
	contents, err := migrationFiles.ReadFile("migrations/000068_jellyfin_display_preferences.sql")
	if err != nil {
		t.Fatalf("read display preference migration: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin display preference migration verification: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DROP TABLE jellyfin_display_preferences`); err != nil {
		t.Fatalf("drop display preference schema: %v", err)
	}
	if _, err := tx.Exec(ctx, string(contents)); err != nil {
		t.Fatalf("recreate display preference schema: %v", err)
	}
	var created bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass('jellyfin_display_preferences') IS NOT NULL`).Scan(&created); err != nil || !created {
		t.Fatalf("verify recreated display preference schema: created=%t err=%v", created, err)
	}
	if _, err := tx.Exec(ctx, `DROP TABLE jellyfin_display_preferences`); err != nil {
		t.Fatalf("drop recreated display preference schema: %v", err)
	}
	var removed bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass('jellyfin_display_preferences') IS NULL`).Scan(&removed); err != nil || !removed {
		t.Fatalf("verify dropped display preference schema: removed=%t err=%v", removed, err)
	}
}
