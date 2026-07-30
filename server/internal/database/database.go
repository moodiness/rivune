package database

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	poolConfig.MaxConns = 10
	poolConfig.MinConns = 1
	poolConfig.MaxConnLifetime = time.Hour
	poolConfig.MaxConnIdleTime = 15 * time.Minute
	poolConfig.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return pool, nil
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version bigint PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		versionText, _, found := strings.Cut(entry.Name(), "_")
		if !found {
			return fmt.Errorf("migration %q has no numeric prefix", entry.Name())
		}
		version, parseErr := strconv.ParseInt(versionText, 10, 64)
		if parseErr != nil {
			return fmt.Errorf("parse migration %q: %w", entry.Name(), parseErr)
		}

		var applied bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", version).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %q: %w", entry.Name(), err)
		}
		if applied {
			continue
		}

		contents, readErr := migrationFiles.ReadFile("migrations/" + entry.Name())
		if readErr != nil {
			return fmt.Errorf("read migration %q: %w", entry.Name(), readErr)
		}

		tx, beginErr := pool.Begin(ctx)
		if beginErr != nil {
			return fmt.Errorf("begin migration %q: %w", entry.Name(), beginErr)
		}
		if _, execErr := tx.Exec(ctx, string(contents)); execErr != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %q: %w", entry.Name(), execErr)
		}
		if _, execErr := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version); execErr != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %q: %w", entry.Name(), execErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return fmt.Errorf("commit migration %q: %w", entry.Name(), commitErr)
		}
	}

	return nil
}
