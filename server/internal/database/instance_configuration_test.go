package database

import (
	"strings"
	"testing"
)

func TestInstanceConfigurationMigrationContract(t *testing.T) {
	contents, err := migrationFiles.ReadFile("migrations/000070_instance_configuration_credentials_audit.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(contents)
	for _, required := range []string{
		"configuration_revision", "legacy_environment_imported_at", "legacy_instance_setting_keys",
		"instance_integration_credentials", "instance_configuration_audit_events",
		"cipher_version", "encryption_key_version", "generation",
		"'tmdbAccessToken'", "'traktClientSecret'", "schema_version = 2",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
	for _, forbidden := range []string{"CREATE INDEX CONCURRENTLY", "COMMIT;", "BEGIN;"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("migration contains transaction-unsafe statement %q", forbidden)
		}
	}
}
