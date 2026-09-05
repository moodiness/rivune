package database

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTVDBEpisodeOrderHierarchyMigration(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run TVDB episode-order hierarchy migration tests")
	}

	ctx := context.Background()
	basePool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open TVDB episode-order hierarchy migration database: %v", err)
	}
	t.Cleanup(basePool.Close)

	schema := fmt.Sprintf("tvdb_episode_order_hierarchy_%d", time.Now().UnixNano())
	if _, err := basePool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create isolated migration schema: %v", err)
	}
	t.Cleanup(func() { _, _ = basePool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })

	configuration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse TVDB episode-order hierarchy migration database URL: %v", err)
	}
	configuration.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		t.Fatalf("open isolated TVDB episode-order hierarchy migration pool: %v", err)
	}
	t.Cleanup(pool.Close)

	applyMigrationsThrough94(t, ctx, pool)

	const (
		seriesID           = "95000000-0000-4000-8000-000000000001"
		canonicalSeasonID  = "95000000-0000-4000-8000-000000000002"
		canonicalEpisodeID = "95000000-0000-4000-8000-000000000003"
		variantSeasonID    = "95000000-0000-4000-8000-000000000004"
		variantEpisodeID   = "95000000-0000-4000-8000-000000000005"
		secondEpisodeID    = "95000000-0000-4000-8000-000000000006"
		standaloneTitleID   = "95000000-0000-4000-8000-000000000007"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, parent_id, ordinal)
		VALUES ($1::uuid, 'series', NULL, NULL),
		       ($2::uuid, 'season', $1::uuid, 1),
		       ($3::uuid, 'episode', $2::uuid, 1)
	`, seriesID, canonicalSeasonID, canonicalEpisodeID); err != nil {
		t.Fatalf("seed canonical title hierarchy before migration 95: %v", err)
	}

	assertColumnExists(t, ctx, pool, schema, "titles", "hierarchy_variant", false)
	assertTableExists(t, ctx, pool, schema, "title_episode_order_identities", false)

	contents, err := migrationFiles.ReadFile("migrations/000095_tvdb_episode_order_hierarchies.sql")
	if err != nil {
		t.Fatalf("read TVDB episode-order hierarchy migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(contents)); err != nil {
		t.Fatalf("apply TVDB episode-order hierarchy migration: %v", err)
	}

	assertColumnExists(t, ctx, pool, schema, "titles", "hierarchy_variant", true)
	assertTableExists(t, ctx, pool, schema, "title_episode_order_identities", true)

	var canonicalRows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM titles
		WHERE id = ANY($1::uuid[]) AND hierarchy_variant = ''
	`, []string{seriesID, canonicalSeasonID, canonicalEpisodeID}).Scan(&canonicalRows); err != nil {
		t.Fatalf("query preserved canonical titles: %v", err)
	}
	if canonicalRows != 3 {
		t.Fatalf("preserved canonical titles = %d, want 3 with empty hierarchy variants", canonicalRows)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, parent_id, ordinal, hierarchy_variant)
		VALUES ($1::uuid, 'season', $2::uuid, 1, 'tvdb:2')
	`, variantSeasonID, seriesID); err != nil {
		t.Fatalf("insert TVDB variant at canonical season coordinate: %v", err)
	}
	assertExecPGError(t, ctx, pool, "23505", "titles_parent_ordinal_unique", `
		INSERT INTO titles (media_type, parent_id, ordinal, hierarchy_variant)
		VALUES ('season', $1::uuid, 1, 'tvdb:2')
	`, seriesID)

	assertExecPGError(t, ctx, pool, "23514", "titles_hierarchy_check", `
		INSERT INTO titles (media_type, hierarchy_variant)
		VALUES ('series', 'tvdb:2')
	`)
	assertExecPGError(t, ctx, pool, "23514", "titles_hierarchy_check", `
		INSERT INTO titles (media_type, parent_id, ordinal, hierarchy_variant)
		VALUES ('season', $1::uuid, 2, 'tvdb:0')
	`, seriesID)
	assertExecPGError(t, ctx, pool, "23514", "titles_hierarchy_check", `
		INSERT INTO titles (media_type, parent_id, ordinal, hierarchy_variant)
		VALUES ('season', $1::uuid, 2, 'tvdb:9223372036854775808')
	`, seriesID)

	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, parent_id, ordinal, hierarchy_variant)
		VALUES ($1::uuid, 'episode', $2::uuid, 1, 'tvdb:2'),
		       ($3::uuid, 'episode', $2::uuid, 2, 'tvdb:2')
	`, variantEpisodeID, variantSeasonID, secondEpisodeID); err != nil {
		t.Fatalf("insert TVDB variant episodes: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO title_episode_order_identities
			(title_id, series_title_id, provider, order_id, namespace, external_id)
		VALUES ($1::uuid, $2::uuid, 'tvdb', '2', 'episode', '10357450')
	`, variantEpisodeID, seriesID); err != nil {
		t.Fatalf("insert TVDB episode-order identity: %v", err)
	}
	assertExecPGError(t, ctx, pool, "23505", "title_episode_order_identities_unique", `
		INSERT INTO title_episode_order_identities
			(title_id, series_title_id, provider, order_id, namespace, external_id)
		VALUES ($1::uuid, $2::uuid, 'tvdb', '2', 'episode', '10357450')
	`, secondEpisodeID, seriesID)

	assertExecPGError(t, ctx, pool, "23514", "title_episode_order_identities_order_id_check", `
		INSERT INTO title_episode_order_identities
			(title_id, series_title_id, provider, order_id, namespace, external_id)
		VALUES ($1::uuid, $2::uuid, 'tvdb', '9223372036854775808', 'episode', '10357451')
	`, secondEpisodeID, seriesID)
	assertExecPGError(t, ctx, pool, "23514", "title_episode_order_identities_external_id_check", `
		INSERT INTO title_episode_order_identities
			(title_id, series_title_id, provider, order_id, namespace, external_id)
		VALUES ($1::uuid, $2::uuid, 'tvdb', '3', 'episode', '9223372036854775808')
	`, secondEpisodeID, seriesID)
	assertExecPGError(t, ctx, pool, "23503", "title_episode_order_identities_title_id_fkey", `
		INSERT INTO title_episode_order_identities
			(title_id, series_title_id, provider, order_id, namespace, external_id)
		VALUES ($1::uuid, $2::uuid, 'tvdb', '3', 'episode', '10357451')
	`, "95000000-0000-4000-8000-000000000099", seriesID)
	assertExecPGError(t, ctx, pool, "23503", "title_episode_order_identities_series_title_id_fkey", `
		INSERT INTO title_episode_order_identities
			(title_id, series_title_id, provider, order_id, namespace, external_id)
		VALUES ($1::uuid, $2::uuid, 'tvdb', '3', 'episode', '10357451')
	`, secondEpisodeID, "95000000-0000-4000-8000-000000000099")

	if _, err := pool.Exec(ctx, `DELETE FROM titles WHERE id = $1::uuid`, variantEpisodeID); err != nil {
		t.Fatalf("delete title owning TVDB order identity: %v", err)
	}
	var identityRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM title_episode_order_identities WHERE title_id = $1::uuid`, variantEpisodeID).Scan(&identityRows); err != nil {
		t.Fatalf("query cascaded TVDB order identity: %v", err)
	}
	if identityRows != 0 {
		t.Fatalf("TVDB order identities after title deletion = %d, want 0", identityRows)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO titles (id, media_type) VALUES ($1::uuid, 'movie')`, standaloneTitleID); err != nil {
		t.Fatalf("insert standalone title for series identity cascade: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO title_episode_order_identities
			(title_id, series_title_id, provider, order_id, namespace, external_id)
		VALUES ($1::uuid, $2::uuid, 'tvdb', '9223372036854775807', 'episode', '9223372036854775807')
	`, standaloneTitleID, seriesID); err != nil {
		t.Fatalf("insert maximum signed-int64 TVDB episode-order identity: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM titles WHERE id = $1::uuid`, seriesID); err != nil {
		t.Fatalf("delete series owning TVDB order identity: %v", err)
	}
	var standaloneTitleExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM titles WHERE id = $1::uuid)`, standaloneTitleID).Scan(&standaloneTitleExists); err != nil {
		t.Fatalf("query standalone title after series identity cascade: %v", err)
	}
	if !standaloneTitleExists {
		t.Fatal("series identity cascade deleted the independently referenced title")
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM title_episode_order_identities WHERE title_id = $1::uuid`, standaloneTitleID).Scan(&identityRows); err != nil {
		t.Fatalf("query identity after series deletion: %v", err)
	}
	if identityRows != 0 {
		t.Fatalf("TVDB order identities after series deletion = %d, want 0", identityRows)
	}

	var lookupIndexes int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_indexes
		WHERE schemaname = $1
		  AND tablename = 'title_episode_order_identities'
		  AND indexdef LIKE 'CREATE UNIQUE INDEX%series_title_id, provider, order_id, namespace, external_id)%'
	`, schema).Scan(&lookupIndexes); err != nil {
		t.Fatalf("count TVDB episode-order lookup indexes: %v", err)
	}
	if lookupIndexes != 1 {
		t.Fatalf("TVDB episode-order lookup indexes = %d, want exactly the named unique constraint index", lookupIndexes)
	}
}

func applyMigrationsThrough94(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		versionText, _, found := strings.Cut(entry.Name(), "_")
		if !found {
			t.Fatalf("migration %q has no numeric prefix", entry.Name())
		}
		version, err := strconv.Atoi(versionText)
		if err != nil {
			t.Fatalf("parse migration %q: %v", entry.Name(), err)
		}
		if version > 94 {
			continue
		}
		contents, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatalf("read migration %q: %v", entry.Name(), err)
		}
		if _, err := pool.Exec(ctx, string(contents)); err != nil {
			t.Fatalf("apply migration %q: %v", entry.Name(), err)
		}
	}
}

func assertColumnExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema, table, column string, want bool) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = $1 AND table_name = $2 AND column_name = $3
		)
	`, schema, table, column).Scan(&exists); err != nil {
		t.Fatalf("query %s.%s column existence: %v", table, column, err)
	}
	if exists != want {
		t.Fatalf("column %s.%s existence = %t, want %t", table, column, exists, want)
	}
}

func assertTableExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema, table string, want bool) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = $1 AND table_name = $2
		)
	`, schema, table).Scan(&exists); err != nil {
		t.Fatalf("query %s table existence: %v", table, err)
	}
	if exists != want {
		t.Fatalf("table %s existence = %t, want %t", table, exists, want)
	}
}

func assertExecPGError(t *testing.T, ctx context.Context, pool *pgxpool.Pool, code, constraint, statement string, arguments ...any) {
	t.Helper()
	_, err := pool.Exec(ctx, statement, arguments...)
	if err == nil {
		t.Fatalf("statement succeeded, want PostgreSQL error %s from %s", code, constraint)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("statement error = %v, want PostgreSQL error %s from %s", err, code, constraint)
	}
	if pgErr.Code != code || pgErr.ConstraintName != constraint {
		t.Fatalf("PostgreSQL error = code %s constraint %q, want code %s constraint %q", pgErr.Code, pgErr.ConstraintName, code, constraint)
	}
}
