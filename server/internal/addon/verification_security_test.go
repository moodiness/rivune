package addon

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moodiness/rivune/server/internal/secretcrypto"
)

func TestVerificationTransportEncryptionRedactsSecretsAndFailedResults(t *testing.T) {
	const (
		verificationID = "11111111-1111-4111-8111-111111111111"
		transportURL   = "https://addon.example/private/manifest.json?token=backup-secret"
	)
	keyring, err := secretcrypto.ParseKeyring("7:" + strings.Repeat("12", 32))
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	service := NewService(nil, nil, slog.New(slog.NewTextHandler(&logs, nil)))
	service.SetVerificationEncryptionKeys(keyring)
	envelope, err := service.sealVerificationTransport(verificationID, "passed", transportURL)
	if err != nil {
		t.Fatal(err)
	}
	if envelope == nil || bytes.Contains(envelope.Ciphertext, []byte(transportURL)) || bytes.Contains(envelope.Ciphertext, []byte("backup-secret")) {
		t.Fatalf("verification transport ciphertext exposed plaintext: %q", envelope.Ciphertext)
	}
	plaintext, err := keyring.Decrypt(*envelope, verificationTransportAAD(verificationID, envelope.KeyVersion))
	if err != nil || string(plaintext) != transportURL {
		t.Fatalf("verification transport round trip = %q, %v", plaintext, err)
	}
	failed, err := service.sealVerificationTransport(verificationID, "failed", transportURL)
	if err != nil || failed != nil {
		t.Fatalf("failed verification retained transport envelope: %+v, %v", failed, err)
	}
	if strings.Contains(logs.String(), transportURL) || strings.Contains(logs.String(), "backup-secret") {
		t.Fatalf("verification transport leaked to logs: %s", logs.String())
	}
}

func TestVerificationConsumeScrubsAndScheduledCleanupIsBounded(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run verification storage tests")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE addon_verifications (
			id uuid PRIMARY KEY,
			expires_at timestamptz NOT NULL,
			created_at timestamptz NOT NULL DEFAULT now(),
			consumed_at timestamptz,
			transport_url_ciphertext bytea,
			transport_url_cipher_version smallint,
			transport_url_key_version integer
		)
	`); err != nil {
		t.Fatal(err)
	}
	const verificationID = "11111111-1111-4111-8111-111111111111"
	if _, err := pool.Exec(ctx, `INSERT INTO addon_verifications (id,expires_at,transport_url_ciphertext,transport_url_cipher_version,transport_url_key_version) VALUES ($1::uuid,now()+interval '10 minutes',$2,1,7)`, verificationID, []byte("opaque-ciphertext")); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := consumeAddonVerification(ctx, tx, verificationID)
	if err != nil || result.RowsAffected() != 1 {
		_ = tx.Rollback(ctx)
		t.Fatalf("consume verification = %d, %v", result.RowsAffected(), err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var consumed bool
	if err := pool.QueryRow(ctx, `SELECT consumed_at IS NOT NULL AND transport_url_ciphertext IS NULL AND transport_url_cipher_version IS NULL AND transport_url_key_version IS NULL FROM addon_verifications WHERE id=$1::uuid`, verificationID).Scan(&consumed); err != nil || !consumed {
		t.Fatalf("consumed verification was not scrubbed: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO addon_verifications (id,expires_at)
		SELECT md5(value::text)::uuid,now()-interval '1 minute'
		FROM generate_series(1,$1) value
	`, verificationCleanupBatch+1); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, nil, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if err := service.RunScheduled(ctx); err != nil {
		t.Fatal(err)
	}
	var expired int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM addon_verifications WHERE expires_at <= now()`).Scan(&expired); err != nil || expired != 1 {
		t.Fatalf("expired rows after bounded cleanup = %d, %v", expired, err)
	}
	if err := service.RunScheduled(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM addon_verifications WHERE expires_at <= now()`).Scan(&expired); err != nil || expired != 0 {
		t.Fatalf("expired rows after second cleanup = %d, %v", expired, err)
	}
}
