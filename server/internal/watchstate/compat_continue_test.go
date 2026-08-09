package watchstate

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestContinueWindowBounds(t *testing.T) {
	for _, valid := range [][2]int{{0, 1}, {maximumContinueOffset, maximumPageSize}} {
		if err := validateContinueWindow(valid[0], valid[1]); err != nil {
			t.Fatalf("valid window %v: %v", valid, err)
		}
	}
	for _, invalid := range [][2]int{{-1, 1}, {maximumContinueOffset + 1, 1}, {0, 0}, {0, maximumPageSize + 1}} {
		if err := validateContinueWindow(invalid[0], invalid[1]); err == nil {
			t.Fatalf("invalid window %v was accepted", invalid)
		}
	}
}

func TestCompatContinueQueriesAreDeterministicAndProfileScoped(t *testing.T) {
	resume := strings.Join(strings.Fields(resumeItemsCTE), " ")
	for _, clause := range []string{
		"progress.profile_id = $1::uuid",
		"PARTITION BY CASE WHEN title.media_type = 'episode' THEN series.id ELSE title.id END",
		"ORDER BY progress.last_watched_at DESC, title.id",
	} {
		if !strings.Contains(resume, clause) {
			t.Fatalf("resume query missing %q", clause)
		}
	}
	next := strings.Join(strings.Fields(nextUpItemsCTE), " ")
	for _, clause := range []string{
		"progress.profile_id = $1::uuid",
		"($2::uuid IS NULL OR series.id = $2::uuid)",
		"ORDER BY candidate_season.ordinal, candidate_episode.ordinal",
		"existing.profile_id = $1::uuid",
	} {
		if !strings.Contains(next, clause) {
			t.Fatalf("next-up query missing %q", clause)
		}
	}
}

func TestQueryResumeItemsOrdersDeduplicatesAndPaginates(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run resume pagination test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer pool.Close()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY, media_type text NOT NULL, parent_id uuid, ordinal integer,
			display_title text, poster_url text, background_url text, release_info text,
			resource_id text, resource_provider text, is_current boolean NOT NULL DEFAULT true,
			source_addon_id uuid
		);
		CREATE TEMPORARY TABLE profiles (id uuid PRIMARY KEY, category_id uuid);
		CREATE TEMPORARY TABLE profile_title_external_ids (profile_id uuid, title_id uuid, provider text, namespace text, external_id text);
		CREATE TEMPORARY TABLE profile_addons (id uuid PRIMARY KEY, enabled boolean NOT NULL DEFAULT true);
		CREATE TEMPORARY TABLE addon_profile_access (addon_id uuid, profile_id uuid);
		CREATE TEMPORARY TABLE addon_category_access (addon_id uuid, category_id uuid);
		CREATE TEMPORARY TABLE profile_continue_dismissals (profile_id uuid, title_id uuid, dismissed_at timestamptz);
		CREATE TEMPORARY TABLE profile_progress (
			profile_id uuid, title_id uuid, position_seconds integer, duration_seconds integer,
			completed boolean, version bigint, last_watched_at timestamptz
		);
	`); err != nil {
		t.Fatalf("create temporary resume schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO profiles (id) VALUES ('11111111-1111-4111-8111-111111111111');
		INSERT INTO titles (id, media_type, display_title, resource_id, resource_provider) VALUES
			('00000000-0000-4000-8000-000000000001', 'movie', 'Older movie', 'older', 'tmdb'),
			('00000000-0000-4000-8000-000000000002', 'movie', 'Newest movie', 'newest', 'tmdb'),
			('00000000-0000-4000-8000-000000000100', 'series', 'Series', 'series', 'tmdb');
		INSERT INTO titles (id, media_type, parent_id, ordinal, display_title) VALUES
			('00000000-0000-4000-8000-000000000110', 'season', '00000000-0000-4000-8000-000000000100', 1, 'Season 1'),
			('00000000-0000-4000-8000-000000000111', 'episode', '00000000-0000-4000-8000-000000000110', 1, 'Episode 1'),
			('00000000-0000-4000-8000-000000000112', 'episode', '00000000-0000-4000-8000-000000000110', 2, 'Episode 2');
		INSERT INTO profile_progress (profile_id, title_id, position_seconds, duration_seconds, completed, version, last_watched_at) VALUES
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000001', 10, 100, false, 1, '2026-01-01T00:00:00Z'),
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000002', 20, 100, false, 1, '2026-01-04T00:00:00Z'),
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000111', 30, 100, false, 1, '2026-01-02T00:00:00Z'),
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000112', 40, 100, false, 1, '2026-01-03T00:00:00Z'),
			('22222222-2222-4222-8222-222222222222', '00000000-0000-4000-8000-000000000002', 99, 100, false, 1, '2026-01-05T00:00:00Z');
	`); err != nil {
		t.Fatalf("seed resume data: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin resume query: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var total int
	if err := tx.QueryRow(ctx, resumeItemsCTE+`SELECT count(*)::int FROM selected`, "11111111-1111-4111-8111-111111111111").Scan(&total); err != nil {
		t.Fatalf("count resume items: %v", err)
	}
	items, err := queryResumeItems(ctx, tx, "11111111-1111-4111-8111-111111111111", 1, 1)
	if err != nil {
		t.Fatalf("query resume page: %v", err)
	}
	if total != 3 || len(items) != 1 || items[0].TitleID != "00000000-0000-4000-8000-000000000112" {
		t.Fatalf("resume page total=%d items=%+v", total, items)
	}
}

func TestQueryNextUpIncludesNeverStartedSeriesOnceAndPaginates(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run next-up pagination test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer pool.Close()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY, media_type text NOT NULL, parent_id uuid, ordinal integer,
			display_title text, poster_url text, background_url text, release_info text,
			resource_id text, resource_provider text, release_date date,
			is_current boolean NOT NULL DEFAULT true, source_addon_id uuid
		);
		CREATE TEMPORARY TABLE profiles (id uuid PRIMARY KEY, category_id uuid);
		CREATE TEMPORARY TABLE profile_title_external_ids (profile_id uuid, title_id uuid, provider text, namespace text, external_id text);
		CREATE TEMPORARY TABLE profile_addons (id uuid PRIMARY KEY, enabled boolean NOT NULL DEFAULT true);
		CREATE TEMPORARY TABLE addon_profile_access (addon_id uuid, profile_id uuid);
		CREATE TEMPORARY TABLE addon_category_access (addon_id uuid, category_id uuid);
		CREATE TEMPORARY TABLE profile_continue_dismissals (profile_id uuid, title_id uuid, dismissed_at timestamptz);
		CREATE TEMPORARY TABLE profile_progress (
			profile_id uuid, title_id uuid, position_seconds integer, duration_seconds integer,
			completed boolean, version bigint, last_watched_at timestamptz
		);
	`); err != nil {
		t.Fatalf("create temporary next-up schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO profiles (id) VALUES
			('11111111-1111-4111-8111-111111111111'),
			('22222222-2222-4222-8222-222222222222');
		INSERT INTO titles (id, media_type, display_title, resource_id, resource_provider) VALUES
			('00000000-0000-4000-8000-000000000100', 'series', 'Started', 'started', 'tmdb'),
			('00000000-0000-4000-8000-000000000200', 'series', 'Never started', 'never', 'tmdb'),
			('00000000-0000-4000-8000-000000000300', 'series', 'Partially started', 'partial', 'tmdb');
		INSERT INTO titles (id, media_type, parent_id, ordinal, display_title) VALUES
			('00000000-0000-4000-8000-000000000110', 'season', '00000000-0000-4000-8000-000000000100', 1, 'Season 1'),
			('00000000-0000-4000-8000-000000000111', 'episode', '00000000-0000-4000-8000-000000000110', 1, 'Episode 1'),
			('00000000-0000-4000-8000-000000000112', 'episode', '00000000-0000-4000-8000-000000000110', 2, 'Episode 2'),
			('00000000-0000-4000-8000-000000000210', 'season', '00000000-0000-4000-8000-000000000200', 1, 'Season 1'),
			('00000000-0000-4000-8000-000000000211', 'episode', '00000000-0000-4000-8000-000000000210', 1, 'Episode 1'),
			('00000000-0000-4000-8000-000000000212', 'episode', '00000000-0000-4000-8000-000000000210', 2, 'Episode 2'),
			('00000000-0000-4000-8000-000000000310', 'season', '00000000-0000-4000-8000-000000000300', 1, 'Season 1'),
			('00000000-0000-4000-8000-000000000311', 'episode', '00000000-0000-4000-8000-000000000310', 1, 'Episode 1');
		INSERT INTO profile_progress (profile_id, title_id, position_seconds, duration_seconds, completed, version, last_watched_at) VALUES
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000111', 100, 100, true, 1, '2026-01-04T00:00:00Z'),
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000311', 20, 100, false, 1, '2026-01-03T00:00:00Z'),
			('22222222-2222-4222-8222-222222222222', '00000000-0000-4000-8000-000000000211', 100, 100, true, 1, '2026-01-05T00:00:00Z');
	`); err != nil {
		t.Fatalf("seed next-up data: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin next-up query: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var total int
	if err := tx.QueryRow(ctx, nextUpItemsCTE+`SELECT count(*)::int FROM selected`,
		"11111111-1111-4111-8111-111111111111", nil).Scan(&total); err != nil {
		t.Fatalf("count next-up items: %v", err)
	}
	first, err := queryNextUpItems(ctx, tx, "11111111-1111-4111-8111-111111111111", nil, 0, 1)
	if err != nil {
		t.Fatalf("query first next-up page: %v", err)
	}
	second, err := queryNextUpItems(ctx, tx, "11111111-1111-4111-8111-111111111111", nil, 1, 1)
	if err != nil {
		t.Fatalf("query second next-up page: %v", err)
	}
	if total != 2 || len(first) != 1 || first[0].TitleID != "00000000-0000-4000-8000-000000000112" ||
		len(second) != 1 || second[0].TitleID != "00000000-0000-4000-8000-000000000211" {
		t.Fatalf("next-up total=%d first=%+v second=%+v", total, first, second)
	}
	neverStarted, err := queryNextUpItems(ctx, tx, "11111111-1111-4111-8111-111111111111",
		"00000000-0000-4000-8000-000000000200", 0, 10)
	if err != nil {
		t.Fatalf("query never-started series: %v", err)
	}
	if len(neverStarted) != 1 || neverStarted[0].TitleID != "00000000-0000-4000-8000-000000000211" {
		t.Fatalf("never-started next-up items=%+v", neverStarted)
	}
}
