package watchstate

import (
	"context"
	"os"
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


func TestCompatibilityContinuePreservesActiveHierarchy(t *testing.T) {
	fixture := newEpisodeOrderContinueFixture(t)
	ctx := context.Background()
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE profile_progress
		SET position_seconds = 222, completed = false, last_watched_at = '2026-08-03T00:00:00Z'
		WHERE profile_id = $1::uuid AND title_id = $2::uuid
	`, fixture.profileID, fixture.dvdEpisodeOneID); err != nil {
		t.Fatalf("make DVD progress resumable: %v", err)
	}

	resume, err := fixture.service.ListResume(ctx, fixture.principal, 0, 10)
	if err != nil {
		t.Fatalf("list compatibility resume for active DVD hierarchy: %v", err)
	}
	if resume.Total != 1 || len(resume.Items) != 1 {
		t.Fatalf("compatibility resume page = %+v", resume)
	}
	resumeItem := resume.Items[0]
	if resumeItem.TitleID != fixture.dvdEpisodeOneID ||
		resumeItem.SeasonID != fixture.dvdSeasonID ||
		resumeItem.PositionSeconds != 222 || resumeItem.Version != 7 ||
		resumeItem.ResourceID != "tvdb:10357450" || resumeItem.ResourceProvider != "tvdb" {
		t.Fatalf("compatibility DVD resume item = %+v", resumeItem)
	}
	assertContinueEpisodeOrderContext(t, resumeItem, "tvdb", "2", "tvdb:"+fixture.seriesID+":2112814")

	if _, err := fixture.pool.Exec(ctx, `
		UPDATE profile_progress
		SET position_seconds = duration_seconds, completed = true, version = 8,
		    last_watched_at = '2026-08-04T00:00:00Z'
		WHERE profile_id = $1::uuid AND title_id = $2::uuid
	`, fixture.profileID, fixture.dvdEpisodeOneID); err != nil {
		t.Fatalf("complete active DVD episode: %v", err)
	}
	nextUp, err := fixture.service.ListNextUp(ctx, fixture.principal, fixture.seriesID, 0, 10)
	if err != nil {
		t.Fatalf("list compatibility next-up for active DVD hierarchy: %v", err)
	}
	if nextUp.Total != 1 || len(nextUp.Items) != 1 {
		t.Fatalf("compatibility next-up page = %+v", nextUp)
	}
	nextItem := nextUp.Items[0]
	if nextItem.TitleID != fixture.dvdEpisodeTwoID ||
		nextItem.SeasonID != fixture.dvdSeasonID ||
		nextItem.ResourceID != "tvdb:10357451" || nextItem.ResourceProvider != "tvdb" {
		t.Fatalf("compatibility DVD next-up item = %+v", nextItem)
	}
	assertContinueEpisodeOrderContext(t, nextItem, "tvdb", "2", "tvdb:"+fixture.seriesID+":2112814")
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
			hierarchy_variant text NOT NULL DEFAULT '',
			display_title text, poster_url text, background_url text, release_info text,
			resource_id text, resource_provider text, release_date date,
			is_current boolean NOT NULL DEFAULT true, source_addon_id uuid
		);
		CREATE TEMPORARY TABLE title_episode_order_identities (
			title_id uuid PRIMARY KEY, series_title_id uuid NOT NULL, provider text NOT NULL,
			order_id text NOT NULL, namespace text NOT NULL, external_id text NOT NULL
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
		INSERT INTO titles (id, media_type, display_title, poster_url, background_url, release_info, resource_id, resource_provider) VALUES
			('00000000-0000-4000-8000-000000000001', 'movie', 'Older movie', NULL, NULL, NULL, 'older', 'tmdb'),
			('00000000-0000-4000-8000-000000000002', 'movie', 'Newest movie', NULL, NULL, NULL, 'newest', 'tmdb'),
			('00000000-0000-4000-8000-000000000100', 'series', 'Series', 'https://images.example/series-poster.jpg', 'https://images.example/series-background.jpg', '2026', 'series', 'tmdb');
		INSERT INTO titles (id, media_type, parent_id, ordinal, display_title, poster_url, release_date) VALUES
			('00000000-0000-4000-8000-000000000110', 'season', '00000000-0000-4000-8000-000000000100', 1, 'Season 1', NULL, NULL),
			('00000000-0000-4000-8000-000000000111', 'episode', '00000000-0000-4000-8000-000000000110', 1, 'Episode 1', 'https://images.example/episode-1.jpg', '2026-01-01'),
			('00000000-0000-4000-8000-000000000112', 'episode', '00000000-0000-4000-8000-000000000110', 2, 'Episode 2', 'https://images.example/episode-2.jpg', '2026-01-02');
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
	item := items[0]
	if item.Title != "Series" || item.PosterURL != "https://images.example/series-poster.jpg" ||
		item.BackgroundURL != "https://images.example/series-background.jpg" || item.ReleaseInfo != "2026" ||
		item.ResourceID != "series:1:2" || item.ResourceProvider != "tmdb" ||
		item.EpisodeTitle != "Episode 2" || item.EpisodeStillURL != "https://images.example/episode-2.jpg" ||
		item.EpisodeAirDate != "2026-01-02" {
		t.Fatalf("resume snapshot contract mismatch: %+v", item)
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
			hierarchy_variant text NOT NULL DEFAULT '',
			display_title text, poster_url text, background_url text, release_info text,
			resource_id text, resource_provider text, release_date date,
			is_current boolean NOT NULL DEFAULT true, source_addon_id uuid
		);
		CREATE TEMPORARY TABLE title_episode_order_identities (
			title_id uuid PRIMARY KEY, series_title_id uuid NOT NULL, provider text NOT NULL,
			order_id text NOT NULL, namespace text NOT NULL, external_id text NOT NULL
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
		INSERT INTO titles (id, media_type, display_title, poster_url, background_url, release_info, resource_id, resource_provider) VALUES
			('00000000-0000-4000-8000-000000000100', 'series', 'Started', 'https://images.example/started-poster.jpg', 'https://images.example/started-background.jpg', '2026', 'started', 'tmdb'),
			('00000000-0000-4000-8000-000000000200', 'series', 'Never started', NULL, 'https://images.example/never-background.jpg', '2025', 'never', 'tmdb'),
			('00000000-0000-4000-8000-000000000300', 'series', 'Partially started', NULL, NULL, NULL, 'partial', 'tmdb');
		INSERT INTO titles (id, media_type, parent_id, ordinal, display_title, poster_url, background_url, release_date) VALUES
			('00000000-0000-4000-8000-000000000110', 'season', '00000000-0000-4000-8000-000000000100', 1, 'Season 1', NULL, NULL, NULL),
			('00000000-0000-4000-8000-000000000111', 'episode', '00000000-0000-4000-8000-000000000110', 1, 'Episode 1', NULL, NULL, '2026-01-01'),
			('00000000-0000-4000-8000-000000000112', 'episode', '00000000-0000-4000-8000-000000000110', 2, 'Episode 2', 'https://images.example/started-episode-2.jpg', NULL, '2026-01-02'),
			('00000000-0000-4000-8000-000000000210', 'season', '00000000-0000-4000-8000-000000000200', 1, 'Season 1', NULL, NULL, NULL),
			('00000000-0000-4000-8000-000000000211', 'episode', '00000000-0000-4000-8000-000000000210', 1, 'Episode 1', NULL, NULL, '2025-02-03'),
			('00000000-0000-4000-8000-000000000212', 'episode', '00000000-0000-4000-8000-000000000210', 2, 'Episode 2', NULL, NULL, '2025-02-10'),
			('00000000-0000-4000-8000-000000000310', 'season', '00000000-0000-4000-8000-000000000300', 1, 'Season 1', NULL, NULL, NULL),
			('00000000-0000-4000-8000-000000000311', 'episode', '00000000-0000-4000-8000-000000000310', 1, 'Episode 1', NULL, NULL, NULL);
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
	if first[0].Title != "Started" || first[0].EpisodeTitle != "Episode 2" ||
		first[0].EpisodeStillURL != "https://images.example/started-episode-2.jpg" || first[0].EpisodeAirDate != "2026-01-02" {
		t.Fatalf("started next-up snapshot contract mismatch: %+v", first[0])
	}
	neverStarted, err := queryNextUpItems(ctx, tx, "11111111-1111-4111-8111-111111111111",
		"00000000-0000-4000-8000-000000000200", 0, 10)
	if err != nil {
		t.Fatalf("query never-started series: %v", err)
	}
	if len(neverStarted) != 1 || neverStarted[0].TitleID != "00000000-0000-4000-8000-000000000211" {
		t.Fatalf("never-started next-up items=%+v", neverStarted)
	}
	if neverStarted[0].Title != "Never started" || neverStarted[0].EpisodeTitle != "Episode 1" ||
		neverStarted[0].EpisodeStillURL != "https://images.example/never-background.jpg" ||
		neverStarted[0].EpisodeAirDate != "2025-02-03" {
		t.Fatalf("never-started snapshot fallback mismatch: %+v", neverStarted[0])
	}
}

func TestDisableTransactionJITIsLocal(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run transaction JIT test")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer pool.Close()
	ctx := context.Background()
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire database connection: %v", err)
	}
	defer connection.Release()
	defer func() { _, _ = connection.Exec(ctx, `RESET jit`) }()
	if _, err := connection.Exec(ctx, `SET jit = on`); err != nil {
		t.Fatalf("enable session JIT: %v", err)
	}
	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	if err := disableTransactionJIT(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("disable transaction JIT: %v", err)
	}
	var transactionJIT string
	if err := tx.QueryRow(ctx, `SHOW jit`).Scan(&transactionJIT); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("show transaction JIT: %v", err)
	}
	if transactionJIT != "off" {
		_ = tx.Rollback(ctx)
		t.Fatalf("transaction JIT = %q, want off", transactionJIT)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback transaction: %v", err)
	}
	var sessionJIT string
	if err := connection.QueryRow(ctx, `SHOW jit`).Scan(&sessionJIT); err != nil {
		t.Fatalf("show session JIT: %v", err)
	}
	if sessionJIT != "on" {
		t.Fatalf("session JIT = %q after rollback, want on", sessionJIT)
	}
}
