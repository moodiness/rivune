package settings

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/secretcrypto"
)

func TestLegacyEnvironmentImportIsOneShotDatabaseFirstAndRedacted(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE instances (
			id smallint PRIMARY KEY, public_id uuid NOT NULL, configuration_revision bigint NOT NULL DEFAULT 0,
			legacy_environment_imported_at timestamptz, legacy_instance_setting_keys text[] NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now());
		CREATE TEMPORARY TABLE instance_settings (instance_id smallint PRIMARY KEY, schema_version integer NOT NULL, settings jsonb NOT NULL, updated_at timestamptz NOT NULL DEFAULT now());
		CREATE TEMPORARY TABLE instance_integration_credentials (
			instance_id smallint NOT NULL, name text NOT NULL, ciphertext bytea, cipher_version smallint,
			encryption_key_version integer, generation bigint NOT NULL, updated_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(instance_id,name));
		CREATE TEMPORARY TABLE instance_configuration_audit_events (
			id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, instance_id smallint NOT NULL, revision bigint NOT NULL,
			actor_user_id uuid, action text NOT NULL, changed_keys text[] NOT NULL, snapshot jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now());
	`); err != nil {
		t.Fatal(err)
	}
	keyring, err := secretcrypto.NewKeyring([]secretcrypto.Key{{Version: 1, Bytes: bytes.Repeat([]byte{0x31}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, keyring)
	imported, err := service.ImportLegacyEnvironment(ctx, LegacyEnvironment{})
	if err != nil || imported {
		t.Fatalf("pre-setup import = %t, %v", imported, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO instances(id,public_id,legacy_instance_setting_keys) VALUES (1,'30000000-0000-4000-8000-000000000003',ARRAY['timezone']);
		INSERT INTO instance_settings(instance_id,schema_version,settings) VALUES (1,3,'{"timezone":"UTC","jellyfinEnabled":false,"jellyfinDebug":false,"hardwareAcceleration":"auto","preferredTranscodeVideoCodec":"auto","transcodeQualityPreset":"balanced","transcodeConcurrency":4,"transcodeMaxBitrateKbps":12000,"mediaMaxStorageMB":20480,"artworkMaxStorageMB":20480,"allowTranscoding":true}');
	`); err != nil {
		t.Fatal(err)
	}
	timezone := "Europe/Paris"
	enabled := true
	legacy := LegacyEnvironment{Timezone: &timezone, JellyfinEnabled: &enabled, TMDBAccessToken: "legacy-private-token"}
	imported, err = service.ImportLegacyEnvironment(ctx, legacy)
	if err != nil || !imported {
		t.Fatalf("first import = %t, %v", imported, err)
	}
	layer, err := service.Instance(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Revision != 1 || layer.Values.Timezone == nil || *layer.Values.Timezone != "UTC" || layer.Values.JellyfinEnabled == nil || !*layer.Values.JellyfinEnabled {
		t.Fatalf("database-first imported layer = %+v", layer)
	}
	credentials, err := service.LoadIntegrationCredentials(ctx)
	if err != nil || credentials.TMDBAccessToken != "legacy-private-token" {
		t.Fatal("legacy credential was not imported")
	}
	otherTimezone := "Asia/Tokyo"
	imported, err = service.ImportLegacyEnvironment(ctx, LegacyEnvironment{Timezone: &otherTimezone})
	if err != nil || imported {
		t.Fatalf("second import = %t, %v", imported, err)
	}
	layer, err = service.Instance(ctx)
	if err != nil || layer.Revision != 1 || *layer.Values.Timezone != "UTC" {
		t.Fatal("second import changed persisted configuration")
	}
	var snapshot []byte
	if err := pool.QueryRow(ctx, `SELECT snapshot FROM instance_configuration_audit_events`).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(snapshot, []byte("legacy-private-token")) {
		t.Fatal("legacy import audit leaked a credential")
	}
}
