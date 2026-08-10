package settings

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/secretcrypto"
)

func TestIntegrationCredentialPatchIsAtomicEncryptedAndRedacted(t *testing.T) {
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
		CREATE TEMPORARY TABLE instances (id smallint PRIMARY KEY, public_id uuid NOT NULL, configuration_revision bigint NOT NULL DEFAULT 0, updated_at timestamptz NOT NULL DEFAULT now());
		CREATE TEMPORARY TABLE users (id uuid PRIMARY KEY, role text NOT NULL);
		CREATE TEMPORARY TABLE instance_integration_credentials (
			instance_id smallint NOT NULL, name text NOT NULL, ciphertext bytea,
			cipher_version smallint, encryption_key_version integer,
			generation bigint NOT NULL, updated_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(instance_id,name));
		CREATE TEMPORARY TABLE instance_configuration_audit_events (
			id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, instance_id smallint NOT NULL,
			revision bigint NOT NULL, actor_user_id uuid, action text NOT NULL, changed_keys text[] NOT NULL,
			snapshot jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now());
		INSERT INTO instances(id,public_id) VALUES (1,'10000000-0000-4000-8000-000000000001');
		INSERT INTO users(id,role) VALUES ('20000000-0000-4000-8000-000000000002','admin');
	`); err != nil {
		t.Fatal(err)
	}
	keyring, err := secretcrypto.NewKeyring([]secretcrypto.Key{{Version: 2, Bytes: bytes.Repeat([]byte{0x42}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, keyring)
	principal := auth.Principal{UserID: "20000000-0000-4000-8000-000000000002", Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator}
	pin := "subscriber-pin"
	if _, err := service.UpdateIntegrationCredentials(ctx, principal, IntegrationCredentialsPatch{TVDBPIN: OptionalCredential{Set: true, Value: &pin}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("orphan PIN error = %v", err)
	}
	var revision, rows int
	if err := pool.QueryRow(ctx, `SELECT configuration_revision FROM instances WHERE id=1`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM instance_integration_credentials`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if revision != 0 || rows != 0 {
		t.Fatalf("failed patch persisted revision=%d rows=%d", revision, rows)
	}

	key := "tvdb-private-key"
	status, err := service.UpdateIntegrationCredentials(ctx, principal, IntegrationCredentialsPatch{
		TVDBAPIKey: OptionalCredential{Set: true, Value: &key}, TVDBPIN: OptionalCredential{Set: true, Value: &pin},
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.Revision != 1 || !status.Credentials.TVDBAPIKey.Configured || !status.Credentials.TVDBPIN.Configured {
		t.Fatalf("redacted status = %+v", status)
	}
	credentials, err := service.LoadIntegrationCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.TVDBAPIKey != key || credentials.TVDBPIN != pin || credentials.Revision != 1 {
		t.Fatal("decrypted credentials did not round trip")
	}
	var ciphertext []byte
	if err := pool.QueryRow(ctx, `SELECT ciphertext FROM instance_integration_credentials WHERE name='tvdbApiKey'`).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte(key)) {
		t.Fatal("credential was persisted in plaintext")
	}
	empty := ""
	if _, err := service.UpdateIntegrationCredentials(ctx, principal, IntegrationCredentialsPatch{TMDBAccessToken: OptionalCredential{Set: true, Value: &empty}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty credential error = %v", err)
	}
	cleared, err := service.UpdateIntegrationCredentials(ctx, principal, IntegrationCredentialsPatch{TVDBAPIKey: OptionalCredential{Set: true}, TVDBPIN: OptionalCredential{Set: true}})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Revision != 2 || cleared.Credentials.TVDBAPIKey.Configured || cleared.Credentials.TVDBPIN.Configured || cleared.Credentials.TVDBAPIKey.UpdatedAt == nil {
		t.Fatalf("clear status = %+v", cleared)
	}
	var generation int64
	var clearedCiphertext []byte
	if err := pool.QueryRow(ctx, `SELECT generation,ciphertext FROM instance_integration_credentials WHERE name='tvdbApiKey'`).Scan(&generation, &clearedCiphertext); err != nil {
		t.Fatal(err)
	}
	if generation != 2 || clearedCiphertext != nil {
		t.Fatal("clear did not retain a generation tombstone")
	}
	credentials, err = service.LoadIntegrationCredentials(ctx)
	if err != nil || credentials.TVDBAPIKey != "" || credentials.TVDBPIN != "" {
		t.Fatal("cleared credentials remained loadable")
	}

	page, err := service.ListAuditEvents(ctx, principal, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 {
		t.Fatalf("audit event count = %d, want 2", len(page.Events))
	}
	for _, event := range page.Events {
		if bytes.Contains(event.Snapshot, []byte(key)) || bytes.Contains(event.Snapshot, []byte(pin)) {
			t.Fatalf("audit was not redacted: %+v", page.Events)
		}
	}
}
