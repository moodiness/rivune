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

func TestHybridHardwareAccelerationMigrationOnlyWidensSchemaV2Check(t *testing.T) {
	contents, err := migrationFiles.ReadFile("migrations/000071_hybrid_hardware_acceleration.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(contents)
	for _, required := range []string{
		"DROP CONSTRAINT IF EXISTS instance_settings_schema_v2_runtime_values",
		"ADD CONSTRAINT instance_settings_schema_v2_runtime_values CHECK",
		"'auto', 'software', 'hybrid', 'vaapi', 'qsv', 'nvenc'",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("hybrid migration missing %q", required)
		}
	}
	for _, forbidden := range []string{"UPDATE instance_settings", "ALTER COLUMN", "jsonb_build_object", "COMMIT;", "BEGIN;"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("hybrid migration changes persisted JSON or transaction control with %q", forbidden)
		}
	}
}

func TestTranscodePlanningSettingsMigrationUpgradesV2AndMaterializesDefaults(t *testing.T) {
	contents, err := migrationFiles.ReadFile("migrations/000072_transcode_planning_settings.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(contents)
	for _, required := range []string{
		"WHERE schema_version = 2", "SET schema_version = 3", "SET DEFAULT 3",
		"'preferredTranscodeVideoCodec', 'auto'", "'transcodeQualityPreset', 'balanced'", "'transcodeConcurrency', 4",
		"instance_settings_schema_v3_runtime_values", "'auto', 'h264', 'hevc', 'av1'",
		"'speed', 'balanced', 'quality'", "BETWEEN 1 AND 32", "'amf'",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("transcode planning migration missing %q", required)
		}
	}
	for _, forbidden := range []string{"COALESCE(settings -> 'preferredTranscodeVideoCodec'", "COMMIT;", "BEGIN;"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("transcode planning migration contains unsafe statement %q", forbidden)
		}
	}
}
