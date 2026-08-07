package database

import (
	"strings"
	"testing"
)

func TestProfileJellyfinCredentialMigrationContract(t *testing.T) {
	contents, err := migrationFiles.ReadFile("migrations/000064_profile_jellyfin_credentials.sql")
	if err != nil {
		t.Fatalf("read profile Jellyfin credential migration: %v", err)
	}
	normalized := strings.Join(strings.Fields(string(contents)), " ")
	for _, contract := range []string{
		"CREATE TABLE profile_jellyfin_credentials",
		"id uuid PRIMARY KEY DEFAULT gen_random_uuid()",
		"profile_id uuid NOT NULL UNIQUE REFERENCES profiles(id) ON DELETE CASCADE",
		"owner_user_id uuid REFERENCES users(id) ON DELETE SET NULL",
		"password_hash bytea CHECK (password_hash IS NULL OR octet_length(password_hash) = 32)",
		"CREATE UNIQUE INDEX profile_jellyfin_credentials_password_hash_key ON profile_jellyfin_credentials (password_hash) WHERE password_hash IS NOT NULL",
		"generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0)",
		"(password_hash IS NOT NULL AND revoked_at IS NULL AND owner_user_id IS NOT NULL) OR (password_hash IS NULL AND revoked_at IS NOT NULL)",
		"ADD COLUMN jellyfin_credential_id uuid REFERENCES profile_jellyfin_credentials(id) ON DELETE CASCADE",
		"ADD COLUMN jellyfin_credential_generation bigint",
		"auth_sessions_jellyfin_credential_consistent",
		"CREATE TRIGGER users_revoke_owned_jellyfin_credentials BEFORE DELETE ON users",
		"jellyfin_credential_owner_deleted",
		"UPDATE auth_sessions native_session SET revoked_at = COALESCE(native_session.revoked_at, now())",
		"compat_device.device_id = native_session.device_id",
		"UPDATE jellyfin_compat_sessions SET revoked_at = COALESCE(revoked_at, now())",
		"DELETE FROM devices WHERE id IN (SELECT device_id FROM jellyfin_compat_devices)",
		"PRIMARY KEY (user_id, profile_id, client_device_id)",
		"UNIQUE (user_id, profile_id, device_id)",
	} {
		if !strings.Contains(normalized, contract) {
			t.Fatalf("profile Jellyfin credential migration lacks %q: %s", contract, normalized)
		}
	}
	for _, forbidden := range []string{
		"password text",
		"password varchar",
		"password_plaintext",
		"secret text",
	} {
		if strings.Contains(strings.ToLower(normalized), forbidden) {
			t.Fatalf("profile Jellyfin credential migration persists forbidden plaintext field %q", forbidden)
		}
	}
}

func TestProfileJellyfinCredentialOwnerLifecycleMigrationContract(t *testing.T) {
	contents, err := migrationFiles.ReadFile("migrations/000065_profile_jellyfin_credential_owner_lifecycle.sql")
	if err != nil {
		t.Fatalf("read Jellyfin credential owner lifecycle migration: %v", err)
	}
	normalized := strings.Join(strings.Fields(string(contents)), " ")
	for _, contract := range []string{
		"ALTER COLUMN owner_user_id DROP NOT NULL",
		"FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE SET NULL",
		"password_hash IS NOT NULL AND revoked_at IS NULL AND owner_user_id IS NOT NULL",
		"CREATE OR REPLACE FUNCTION revoke_deleted_jellyfin_credential_owner()",
		"UPDATE auth_sessions SET revoked_at = COALESCE(revoked_at, now())",
		"SET password_hash = NULL, generation = generation + 1",
		"CREATE TRIGGER users_revoke_owned_jellyfin_credentials BEFORE DELETE ON users",
	} {
		if !strings.Contains(normalized, contract) {
			t.Fatalf("Jellyfin credential owner lifecycle migration lacks %q: %s", contract, normalized)
		}
	}
}

func TestLegacyJellyfinCompatibilityDeviceCleanupMigrationContract(t *testing.T) {
	contents, err := migrationFiles.ReadFile("migrations/000066_remove_legacy_jellyfin_compat_devices.sql")
	if err != nil {
		t.Fatalf("read legacy Jellyfin compatibility device cleanup migration: %v", err)
	}
	normalized := strings.Join(strings.Fields(string(contents)), " ")
	for _, contract := range []string{
		"DELETE FROM devices legacy_device",
		"cutover_session.revoked_reason = 'jellyfin_profile_credential_cutover'",
		"retained_session.revoked_reason IS DISTINCT FROM 'jellyfin_profile_credential_cutover'",
		"NOT EXISTS ( SELECT 1 FROM jellyfin_compat_devices current_mapping",
	} {
		if !strings.Contains(normalized, contract) {
			t.Fatalf("legacy Jellyfin compatibility device cleanup migration lacks %q: %s", contract, normalized)
		}
	}
}
