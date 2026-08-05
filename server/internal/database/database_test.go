package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrationAndLedgerRollbackTogetherAndResume(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run migration atomicity tests")
	}

	ctx := context.Background()
	basePool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open migration test database: %v", err)
	}
	t.Cleanup(basePool.Close)

	schema := fmt.Sprintf("migration_atomicity_%d", time.Now().UnixNano())
	if _, err := basePool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create isolated migration schema: %v", err)
	}
	t.Cleanup(func() { _, _ = basePool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })

	configuration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse migration test database URL: %v", err)
	}
	configuration.MaxConns = 2
	configuration.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		t.Fatalf("open isolated migration pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("establish migration 44 fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		ALTER TABLE profiles
			DROP CONSTRAINT profiles_description_check,
			DROP COLUMN description;
		DELETE FROM schema_migrations WHERE version = 44;
	`); err != nil {
		t.Fatalf("restore fixture to migration 43: %v", err)
	}

	injectedFailure := errors.New("injected failure before migration ledger")
	err = migrate(ctx, pool, func(version int64) error {
		if version == 44 {
			return injectedFailure
		}
		return nil
	})
	if !errors.Is(err, injectedFailure) {
		t.Fatalf("migration failure = %v, want injected pre-ledger failure", err)
	}
	if migrationDescriptionColumnExists(t, ctx, pool, schema) {
		t.Fatal("migration DDL survived rollback after the pre-ledger failure")
	}
	if migrationVersionExists(t, ctx, pool, 44) {
		t.Fatal("migration ledger recorded a migration whose transaction failed")
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("resume migration after atomic rollback: %v", err)
	}
	if !migrationDescriptionColumnExists(t, ctx, pool, schema) || !migrationVersionExists(t, ctx, pool, 44) {
		t.Fatal("migration retry did not commit its DDL and ledger together")
	}

	if _, err := pool.Exec(ctx, "DELETE FROM schema_migrations WHERE version = 44"); err != nil {
		t.Fatalf("simulate legacy post-DDL pre-ledger interruption: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("resume legacy partially recorded migration: %v", err)
	}
	if !migrationDescriptionColumnExists(t, ctx, pool, schema) || !migrationVersionExists(t, ctx, pool, 44) {
		t.Fatal("legacy partial migration recovery did not preserve DDL and restore its ledger")
	}

	legacyTransactionVersions := []int64{
		7, 8, 9, 10, 11, 12, 17, 18, 19, 20, 21, 22, 24, 27,
		29, 30, 31, 32, 33, 34, 35, 36, 38, 39, 40, 42, 43, 44,
	}
	if _, err := pool.Exec(ctx, "DELETE FROM schema_migrations WHERE version = ANY($1::bigint[])", legacyTransactionVersions); err != nil {
		t.Fatalf("simulate legacy transaction migrations committed before their ledger: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("replay legacy transaction migrations idempotently: %v", err)
	}
	var restoredVersions int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM schema_migrations
		WHERE version = ANY($1::bigint[])
	`, legacyTransactionVersions).Scan(&restoredVersions); err != nil {
		t.Fatalf("count restored legacy migration ledgers: %v", err)
	}
	if restoredVersions != len(legacyTransactionVersions) {
		t.Fatalf("restored legacy migration ledgers = %d, want %d", restoredVersions, len(legacyTransactionVersions))
	}
}

func migrationDescriptionColumnExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = $1 AND table_name = 'profiles' AND column_name = 'description'
		)
	`, schema).Scan(&exists); err != nil {
		t.Fatalf("query migrated description column: %v", err)
	}
	return exists
}

func migrationVersionExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, version int64) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", version).Scan(&exists); err != nil {
		t.Fatalf("query migration ledger version %d: %v", version, err)
	}
	return exists
}
