package database

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCategoryResourceAccessMigrationDefinesOnlyContractedTablesAndIndexes(t *testing.T) {
	contents, err := migrationFiles.ReadFile("migrations/000058_category_resource_access.sql")
	if err != nil {
		t.Fatalf("read category resource access migration: %v", err)
	}
	normalized := strings.Join(strings.Fields(string(contents)), " ")
	expectedStatements := []string{
		"ALTER TABLE profile_addons DROP CONSTRAINT profile_addons_profile_id_fkey, ALTER COLUMN profile_id DROP NOT NULL, ADD CONSTRAINT profile_addons_profile_id_fkey FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE SET NULL",
		"ALTER TABLE profile_addons DROP CONSTRAINT IF EXISTS profile_addons_profile_id_transport_url_key",
		"ALTER TABLE profile_collections DROP CONSTRAINT profile_collections_profile_id_fkey, ALTER COLUMN profile_id DROP NOT NULL, ADD CONSTRAINT profile_collections_profile_id_fkey FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE SET NULL",
		"CREATE TABLE IF NOT EXISTS addon_category_access ( addon_id uuid NOT NULL REFERENCES profile_addons(id) ON DELETE CASCADE, category_id uuid NOT NULL REFERENCES access_categories(id) ON DELETE CASCADE, position integer NOT NULL CHECK (position >= 0), PRIMARY KEY (addon_id, category_id), UNIQUE (category_id, position) DEFERRABLE INITIALLY DEFERRED )",
		"CREATE INDEX IF NOT EXISTS addon_category_access_category_order_idx ON addon_category_access (category_id, position, addon_id)",
		"CREATE TABLE IF NOT EXISTS addon_profile_order ( addon_id uuid NOT NULL REFERENCES profile_addons(id) ON DELETE CASCADE, profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE, position integer NOT NULL CHECK (position >= 0), PRIMARY KEY (addon_id, profile_id), UNIQUE (profile_id, position) DEFERRABLE INITIALLY DEFERRED )",
		"INSERT INTO addon_profile_order (addon_id, profile_id, position) SELECT addon_id, profile_id, position FROM addon_profile_access ON CONFLICT (addon_id, profile_id) DO NOTHING",
		"CREATE INDEX IF NOT EXISTS addon_profile_order_profile_order_idx ON addon_profile_order (profile_id, position, addon_id)",
		"CREATE TABLE IF NOT EXISTS collection_category_access ( collection_id uuid NOT NULL REFERENCES profile_collections(id) ON DELETE CASCADE, category_id uuid NOT NULL REFERENCES access_categories(id) ON DELETE CASCADE, position integer NOT NULL CHECK (position >= 0), PRIMARY KEY (collection_id, category_id), UNIQUE (category_id, position) DEFERRABLE INITIALLY DEFERRED )",
		"CREATE INDEX IF NOT EXISTS collection_category_access_category_order_idx ON collection_category_access (category_id, position, collection_id)",
		"CREATE TABLE IF NOT EXISTS collection_profile_order ( collection_id uuid NOT NULL REFERENCES profile_collections(id) ON DELETE CASCADE, profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE, position integer NOT NULL CHECK (position >= 0), PRIMARY KEY (collection_id, profile_id), UNIQUE (profile_id, position) DEFERRABLE INITIALLY DEFERRED )",
		"INSERT INTO collection_profile_order (collection_id, profile_id, position) SELECT collection_id, profile_id, position FROM collection_profile_access ON CONFLICT (collection_id, profile_id) DO NOTHING",
		"CREATE INDEX IF NOT EXISTS collection_profile_order_profile_order_idx ON collection_profile_order (profile_id, position, collection_id)",
	}
	actualStatements := strings.Split(strings.TrimSuffix(normalized, ";"), ";")
	if len(actualStatements) != len(expectedStatements) {
		t.Fatalf("migration contains %d statements, want exactly %d", len(actualStatements), len(expectedStatements))
	}
	for index, expected := range expectedStatements {
		if actual := strings.TrimSpace(actualStatements[index]); actual != expected {
			t.Errorf("migration statement %d = %q, want %q", index+1, actual, expected)
		}
	}
}

func TestCategoryResourceAccessMigrationBackfillsSchemaAndReplays(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run category resource access migration tests")
	}

	ctx := context.Background()
	basePool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open category resource access migration database: %v", err)
	}
	t.Cleanup(basePool.Close)

	schema := fmt.Sprintf("category_resource_access_%d", time.Now().UnixNano())
	if _, err := basePool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create isolated migration schema: %v", err)
	}
	t.Cleanup(func() { _, _ = basePool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })

	configuration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse category resource access migration database URL: %v", err)
	}
	configuration.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		t.Fatalf("open isolated category resource access migration pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(ctx, `
		CREATE TABLE profiles (id uuid PRIMARY KEY);
		CREATE TABLE access_categories (id uuid PRIMARY KEY);
		CREATE TABLE profile_addons (
			id uuid PRIMARY KEY,
			profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
			transport_url text NOT NULL,
			position integer NOT NULL,
			UNIQUE (profile_id, transport_url),
			UNIQUE (profile_id, position) DEFERRABLE INITIALLY DEFERRED
		);
		CREATE TABLE profile_collections (
			id uuid PRIMARY KEY,
			profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
			position integer NOT NULL,
			UNIQUE (profile_id, position) DEFERRABLE INITIALLY DEFERRED
		);
		CREATE TABLE addon_profile_access (
			addon_id uuid NOT NULL,
			profile_id uuid NOT NULL,
			position integer NOT NULL,
			PRIMARY KEY (addon_id, profile_id)
		);
		CREATE TABLE collection_profile_access (
			collection_id uuid NOT NULL,
			profile_id uuid NOT NULL,
			position integer NOT NULL,
			PRIMARY KEY (collection_id, profile_id)
		);

		INSERT INTO profiles (id) VALUES
			('00000000-0000-0000-0000-000000000001'),
			('00000000-0000-0000-0000-000000000002');
		INSERT INTO access_categories (id) VALUES
			('00000000-0000-0000-0000-000000000101'),
			('00000000-0000-0000-0000-000000000102');
		INSERT INTO profile_addons (id, profile_id, transport_url, position) VALUES
			('00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-000000000001', 'https://migration.example/addon-1', 0),
			('00000000-0000-0000-0000-000000000202', '00000000-0000-0000-0000-000000000002', 'https://migration.example/addon-2', 0);
		INSERT INTO profile_collections (id, profile_id, position) VALUES
			('00000000-0000-0000-0000-000000000301', '00000000-0000-0000-0000-000000000001', 0),
			('00000000-0000-0000-0000-000000000302', '00000000-0000-0000-0000-000000000002', 0);
		INSERT INTO addon_profile_access (addon_id, profile_id, position) VALUES
			('00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-000000000001', 7),
			('00000000-0000-0000-0000-000000000202', '00000000-0000-0000-0000-000000000001', 2),
			('00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-000000000002', 9);
		INSERT INTO collection_profile_access (collection_id, profile_id, position) VALUES
			('00000000-0000-0000-0000-000000000301', '00000000-0000-0000-0000-000000000001', 5),
			('00000000-0000-0000-0000-000000000302', '00000000-0000-0000-0000-000000000001', 1),
			('00000000-0000-0000-0000-000000000301', '00000000-0000-0000-0000-000000000002', 4);
	`); err != nil {
		t.Fatalf("seed pre-migration resource access: %v", err)
	}

	contents, err := migrationFiles.ReadFile("migrations/000058_category_resource_access.sql")
	if err != nil {
		t.Fatalf("read category resource access migration: %v", err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := pool.Exec(ctx, string(contents)); err != nil {
			t.Fatalf("apply category resource access migration attempt %d: %v", attempt, err)
		}
	}

	assertResourceOrderBackfill(t, ctx, pool, "addon_profile_order", "addon_id", map[string]int{
		"00000000-0000-0000-0000-000000000201/00000000-0000-0000-0000-000000000001": 7,
		"00000000-0000-0000-0000-000000000202/00000000-0000-0000-0000-000000000001": 2,
		"00000000-0000-0000-0000-000000000201/00000000-0000-0000-0000-000000000002": 9,
	})
	assertResourceOrderBackfill(t, ctx, pool, "collection_profile_order", "collection_id", map[string]int{
		"00000000-0000-0000-0000-000000000301/00000000-0000-0000-0000-000000000001": 5,
		"00000000-0000-0000-0000-000000000302/00000000-0000-0000-0000-000000000001": 1,
		"00000000-0000-0000-0000-000000000301/00000000-0000-0000-0000-000000000002": 4,
	})

	assertCategoryResourceAccessTable(t, ctx, pool, "addon_category_access", "addon_id", "profile_addons", "category_id", "access_categories", "addon_category_access_category_order_idx")
	assertCategoryResourceAccessTable(t, ctx, pool, "addon_profile_order", "addon_id", "profile_addons", "profile_id", "profiles", "addon_profile_order_profile_order_idx")
	assertCategoryResourceAccessTable(t, ctx, pool, "collection_category_access", "collection_id", "profile_collections", "category_id", "access_categories", "collection_category_access_category_order_idx")
	assertCategoryResourceAccessTable(t, ctx, pool, "collection_profile_order", "collection_id", "profile_collections", "profile_id", "profiles", "collection_profile_order_profile_order_idx")
	assertLegacyResourceOwnership(t, ctx, pool, "profile_addons", true)
	assertLegacyResourceOwnership(t, ctx, pool, "profile_collections", false)
	if _, err := pool.Exec(ctx, `
		INSERT INTO profile_addons (id, profile_id, transport_url, position)
		VALUES ('00000000-0000-0000-0000-000000000203', '00000000-0000-0000-0000-000000000001', 'https://migration.example/addon-1', 1);
		DELETE FROM profiles WHERE id = '00000000-0000-0000-0000-000000000001';
	`); err != nil {
		t.Fatalf("exercise nullable legacy add-on ownership: %v", err)
	}
	var nullAddonOwners, nullCollectionOwners int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM profile_addons WHERE profile_id IS NULL`).Scan(&nullAddonOwners); err != nil {
		t.Fatalf("count retained add-ons after owner deletion: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM profile_collections WHERE profile_id IS NULL`).Scan(&nullCollectionOwners); err != nil {
		t.Fatalf("count retained collections after owner deletion: %v", err)
	}
	if nullAddonOwners != 2 || nullCollectionOwners != 1 {
		t.Fatalf("retained resources after owner deletion = add-ons %d, collections %d; want 2 and 1", nullAddonOwners, nullCollectionOwners)
	}
}

func assertLegacyResourceOwnership(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, expectTransportConstraintRemoved bool) {
	t.Helper()
	var nullable string
	if err := pool.QueryRow(ctx, `
		SELECT is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = $1 AND column_name = 'profile_id'
	`, table).Scan(&nullable); err != nil {
		t.Fatalf("query %s owner nullability: %v", table, err)
	}
	if nullable != "YES" {
		t.Errorf("%s.profile_id nullability = %q, want YES", table, nullable)
	}
	var constraintName, deleteAction string
	if err := pool.QueryRow(ctx, `
		SELECT conname, confdeltype::text
		FROM pg_constraint
		WHERE conrelid = $1::regclass AND contype = 'f'
		  AND conkey = ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid = $1::regclass AND attname = 'profile_id')]::smallint[]
	`, table).Scan(&constraintName, &deleteAction); err != nil {
		t.Fatalf("query %s owner foreign key: %v", table, err)
	}
	expectedConstraint := table + "_profile_id_fkey"
	if constraintName != expectedConstraint || deleteAction != "n" {
		t.Errorf("%s owner foreign key = %s/%s, want %s/n", table, constraintName, deleteAction, expectedConstraint)
	}
	var ownerPositionConstraints int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_constraint
		WHERE conrelid = $1::regclass AND contype = 'u' AND conname = $2
		  AND condeferrable AND condeferred
	`, table, table+"_profile_id_position_key").Scan(&ownerPositionConstraints); err != nil {
		t.Fatalf("query %s owner position constraint: %v", table, err)
	}
	if ownerPositionConstraints != 1 {
		t.Errorf("%s owner position constraint count = %d, want 1", table, ownerPositionConstraints)
	}
	if expectTransportConstraintRemoved {
		var transportConstraints int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_constraint
			WHERE conrelid = 'profile_addons'::regclass
			  AND conname = 'profile_addons_profile_id_transport_url_key'
		`).Scan(&transportConstraints); err != nil {
			t.Fatalf("query legacy add-on transport constraint: %v", err)
		}
		if transportConstraints != 0 {
			t.Errorf("legacy add-on owner/transport constraint count = %d, want 0", transportConstraints)
		}
	}
}

func assertResourceOrderBackfill(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, resourceColumn string, expected map[string]int) {
	t.Helper()
	rows, err := pool.Query(ctx, fmt.Sprintf("SELECT %s::text, profile_id::text, position FROM %s", resourceColumn, table))
	if err != nil {
		t.Fatalf("query %s backfill: %v", table, err)
	}
	defer rows.Close()
	actual := make(map[string]int, len(expected))
	for rows.Next() {
		var resourceID, profileID string
		var position int
		if err := rows.Scan(&resourceID, &profileID, &position); err != nil {
			t.Fatalf("scan %s backfill: %v", table, err)
		}
		actual[resourceID+"/"+profileID] = position
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s backfill: %v", table, err)
	}
	if fmt.Sprint(actual) != fmt.Sprint(expected) {
		t.Fatalf("%s backfill = %v, want %v", table, actual, expected)
	}
}

func assertCategoryResourceAccessTable(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, resourceColumn, resourceTable, scopeColumn, scopeTable, indexName string) {
	t.Helper()
	var columns string
	if err := pool.QueryRow(ctx, `
		SELECT string_agg(column_name || ':' || data_type || ':' || is_nullable, ',' ORDER BY ordinal_position)
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = $1
	`, table).Scan(&columns); err != nil {
		t.Fatalf("query %s columns: %v", table, err)
	}
	expectedColumns := resourceColumn + ":uuid:NO," + scopeColumn + ":uuid:NO,position:integer:NO"
	if columns != expectedColumns {
		t.Errorf("%s columns = %q, want %q", table, columns, expectedColumns)
	}

	var primaryKeyColumns string
	if err := pool.QueryRow(ctx, `
		SELECT string_agg(attribute.attname, ',' ORDER BY key.ordinality)
		FROM pg_constraint constraint_record
		CROSS JOIN LATERAL unnest(constraint_record.conkey) WITH ORDINALITY AS key(attnum, ordinality)
		JOIN pg_attribute attribute ON attribute.attrelid = constraint_record.conrelid AND attribute.attnum = key.attnum
		WHERE constraint_record.conrelid = $1::regclass AND constraint_record.contype = 'p'
	`, table).Scan(&primaryKeyColumns); err != nil {
		t.Fatalf("query %s primary key: %v", table, err)
	}
	if expected := resourceColumn + "," + scopeColumn; primaryKeyColumns != expected {
		t.Errorf("%s primary key = %q, want %q", table, primaryKeyColumns, expected)
	}

	var uniqueColumns string
	var deferrable, initiallyDeferred bool
	if err := pool.QueryRow(ctx, `
		SELECT string_agg(attribute.attname, ',' ORDER BY key.ordinality), constraint_record.condeferrable, constraint_record.condeferred
		FROM pg_constraint constraint_record
		CROSS JOIN LATERAL unnest(constraint_record.conkey) WITH ORDINALITY AS key(attnum, ordinality)
		JOIN pg_attribute attribute ON attribute.attrelid = constraint_record.conrelid AND attribute.attnum = key.attnum
		WHERE constraint_record.conrelid = $1::regclass AND constraint_record.contype = 'u'
		GROUP BY constraint_record.oid
	`, table).Scan(&uniqueColumns, &deferrable, &initiallyDeferred); err != nil {
		t.Fatalf("query %s ordering constraint: %v", table, err)
	}
	if expected := scopeColumn + ",position"; uniqueColumns != expected || !deferrable || !initiallyDeferred {
		t.Errorf("%s ordering constraint = (%s, deferrable=%t, initially deferred=%t), want (%s, true, true)", table, uniqueColumns, deferrable, initiallyDeferred, expected)
	}

	rows, err := pool.Query(ctx, `
		SELECT source_attribute.attname, target.relname, constraint_record.confdeltype::text
		FROM pg_constraint constraint_record
		CROSS JOIN LATERAL unnest(constraint_record.conkey, constraint_record.confkey) AS key(source_attnum, target_attnum)
		JOIN pg_attribute source_attribute ON source_attribute.attrelid = constraint_record.conrelid AND source_attribute.attnum = key.source_attnum
		JOIN pg_class target ON target.oid = constraint_record.confrelid
		WHERE constraint_record.conrelid = $1::regclass AND constraint_record.contype = 'f'
	`, table)
	if err != nil {
		t.Fatalf("query %s foreign keys: %v", table, err)
	}
	defer rows.Close()
	foreignKeys := make(map[string]string, 2)
	for rows.Next() {
		var column, targetTable, deleteAction string
		if err := rows.Scan(&column, &targetTable, &deleteAction); err != nil {
			t.Fatalf("scan %s foreign key: %v", table, err)
		}
		foreignKeys[column] = targetTable + ":" + deleteAction
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s foreign keys: %v", table, err)
	}
	expectedForeignKeys := map[string]string{resourceColumn: resourceTable + ":c", scopeColumn: scopeTable + ":c"}
	if fmt.Sprint(foreignKeys) != fmt.Sprint(expectedForeignKeys) {
		t.Errorf("%s foreign keys = %v, want %v", table, foreignKeys, expectedForeignKeys)
	}

	var checkDefinition string
	if err := pool.QueryRow(ctx, `
		SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conrelid = $1::regclass AND contype = 'c'
	`, table).Scan(&checkDefinition); err != nil {
		t.Fatalf("query %s position check: %v", table, err)
	}
	normalizedCheckDefinition := strings.NewReplacer(`"`, "", "(", "", ")", "").Replace(checkDefinition)
	if !strings.Contains(normalizedCheckDefinition, "position >= 0") {
		t.Errorf("%s check constraint = %q, want nonnegative position", table, checkDefinition)
	}

	var indexColumns string
	if err := pool.QueryRow(ctx, `
		SELECT string_agg(attribute.attname, ',' ORDER BY key.ordinality)
		FROM pg_class index_relation
		JOIN pg_index index_record ON index_record.indexrelid = index_relation.oid
		CROSS JOIN LATERAL unnest(index_record.indkey) WITH ORDINALITY AS key(attnum, ordinality)
		JOIN pg_attribute attribute ON attribute.attrelid = index_record.indrelid AND attribute.attnum = key.attnum
		WHERE index_record.indrelid = $1::regclass AND index_relation.relname = $2
	`, table, indexName).Scan(&indexColumns); err != nil {
		t.Fatalf("query %s scope/order index: %v", table, err)
	}
	expectedIndexColumns := scopeColumn + ",position," + resourceColumn
	if indexColumns != expectedIndexColumns {
		t.Errorf("%s index columns = %q, want %q", table, indexColumns, expectedIndexColumns)
	}
}
