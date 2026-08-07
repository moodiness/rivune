package database

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestJellyfinCompatSessionMigrationKeepsHashedLinkedCredentials(t *testing.T) {
	contents, err := migrationFiles.ReadFile("migrations/000062_jellyfin_compat_sessions.sql")
	if err != nil {
		t.Fatalf("read Jellyfin compatibility migration: %v", err)
	}
	normalized := strings.Join(strings.Fields(string(contents)), " ")
	for _, contract := range []string{
		"CREATE TABLE jellyfin_compat_devices",
		"PRIMARY KEY (user_id, client_device_id)",
		"device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE",
		"auth_session_id uuid NOT NULL REFERENCES auth_sessions(id) ON DELETE CASCADE",
		"profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE",
		"token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32)",
		"client_device_id text NOT NULL",
		"expires_at timestamptz NOT NULL",
		"WHERE revoked_at IS NULL",
		"CREATE TRIGGER auth_sessions_revoke_jellyfin_compat",
	} {
		if !strings.Contains(normalized, contract) {
			t.Fatalf("Jellyfin compatibility migration lacks %q: %s", contract, normalized)
		}
	}
	for _, forbidden := range []string{"token text", "token varchar", "access_token text", "refresh_token"} {
		if strings.Contains(strings.ToLower(normalized), forbidden) {
			t.Fatalf("Jellyfin compatibility migration persists forbidden plaintext field %q", forbidden)
		}
	}
}

func TestJellyfinCompatSessionsCascadeWithNativeSession(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run the Jellyfin compatibility migration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open migrated test database: %v", err)
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin compatibility migration test: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	var userID, profileID, categoryID, deviceID, authSessionID, compatSessionID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-compat-migration-hash', 'member')
		RETURNING id::text
	`, "jf_migration_"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert compatibility migration user: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO profiles (name) VALUES ($1)
		RETURNING id::text, category_id::text
	`, "Jellyfin migration "+suffix).Scan(&profileID, &categoryID); err != nil {
		t.Fatalf("insert compatibility migration profile: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id) VALUES ($1, $2)
	`, userID, profileID); err != nil {
		t.Fatalf("grant compatibility migration profile: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1, $2, 'test', $3, now())
		RETURNING id::text
	`, userID, "Jellyfin migration device "+suffix, categoryID).Scan(&deviceID); err != nil {
		t.Fatalf("insert compatibility migration device: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO jellyfin_compat_devices (user_id, client_device_id, device_id)
		VALUES ($1, 'migration-device', $2)
	`, userID, deviceID); err != nil {
		t.Fatalf("insert compatibility device mapping: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id, active_profile_id, profile_grant_expires_at,
			profile_context_hash
		) VALUES (
			$1, $2, decode(repeat('11', 32), 'hex'), now() + interval '1 hour',
			now() + interval '2 hours', 'category', $3, $4, now() + interval '2 hours',
			decode(repeat('22', 32), 'hex')
		) RETURNING id::text
	`, userID, deviceID, categoryID, profileID).Scan(&authSessionID); err != nil {
		t.Fatalf("insert linked native session: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO jellyfin_compat_sessions (
			auth_session_id, profile_id, token_hash, client_name, device_name,
			client_device_id, client_version, expires_at
		) VALUES (
			$1, $2, decode(repeat('33', 32), 'hex'), 'Infuse', 'Migration client',
			'migration-device', '1.0', now() + interval '2 hours'
		) RETURNING id::text
	`, authSessionID, profileID).Scan(&compatSessionID); err != nil {
		t.Fatalf("insert compatibility session: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions SET revoked_at = now(), revoked_reason = 'logout' WHERE id = $1::uuid
	`, authSessionID); err != nil {
		t.Fatalf("revoke linked native session: %v", err)
	}
	var compatRevokedAt *time.Time
	var compatReason *string
	if err := tx.QueryRow(ctx, `
		SELECT revoked_at, revoked_reason FROM jellyfin_compat_sessions WHERE id = $1::uuid
	`, compatSessionID).Scan(&compatRevokedAt, &compatReason); err != nil {
		t.Fatalf("read propagated compatibility revocation: %v", err)
	}
	if compatRevokedAt == nil || compatReason == nil || *compatReason != "logout" {
		t.Fatalf("compatibility revocation = %v/%v, want linked logout", compatRevokedAt, compatReason)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM auth_sessions WHERE id = $1::uuid", authSessionID); err != nil {
		t.Fatalf("delete linked native session: %v", err)
	}
	var remaining int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM jellyfin_compat_sessions WHERE id = $1::uuid
	`, compatSessionID).Scan(&remaining); err != nil {
		t.Fatalf("count cascaded compatibility session: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("native session deletion retained %d compatibility sessions", remaining)
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM jellyfin_compat_devices
		WHERE user_id = $1::uuid AND client_device_id = 'migration-device' AND device_id = $2::uuid
	`, userID, deviceID).Scan(&remaining); err != nil {
		t.Fatalf("count persistent compatibility device mapping: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("native session deletion removed compatibility device mapping")
	}
}
