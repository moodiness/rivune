package watchstate

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

type catalogQueryCounter struct {
	pages  atomic.Int64
	titles atomic.Int64
}

func (counter *catalogQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "watchstate.catalog_items") {
		counter.pages.Add(1)
	}
	if strings.Contains(data.SQL, "watchstate.catalog_title") {
		counter.titles.Add(1)
	}
	return ctx
}

func (*catalogQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {
}

func TestNormalizeCatalogQueryBoundsAndTypes(t *testing.T) {
	query, err := normalizeCatalogQuery(CatalogQuery{
		ParentID:   " 550E8400-E29B-41D4-A716-446655440000 ",
		MediaTypes: []string{" Series ", "episode", "series"},
		SearchTerm: "  Éclair  ",
		IDs: []string{
			"11111111-1111-4111-8111-111111111111",
			"11111111-1111-4111-8111-111111111111",
		},
		Recursive: true, Offset: 7, Limit: MaximumCatalogPageSize,
		SortBy: " DateCreated, SortName, ProductionYear ", SortOrder: "Descending",
	})
	if err != nil {
		t.Fatalf("normalize catalog query: %v", err)
	}
	if query.ParentID != "550e8400-e29b-41d4-a716-446655440000" || query.SearchTerm != "Éclair" || !query.Recursive ||
		!reflect.DeepEqual(query.MediaTypes, []string{"episode", "series"}) ||
		!reflect.DeepEqual(query.IDs, []string{"11111111-1111-4111-8111-111111111111"}) ||
		query.Offset != 7 || query.Limit != MaximumCatalogPageSize ||
		query.SortBy != "datecreated,sortname,productionyear" || query.SortOrder != "descending" {
		t.Fatalf("unexpected normalized query: %+v", query)
	}
	defaults, err := normalizeCatalogQuery(CatalogQuery{Limit: 20})
	if err != nil || !reflect.DeepEqual(defaults.MediaTypes, []string{"episode", "movie", "season", "series", "video"}) {
		t.Fatalf("unexpected default catalog media types: %+v error %v", defaults, err)
	}
	for _, invalid := range []CatalogQuery{
		{Limit: 0},
		{Limit: MaximumCatalogPageSize + 1},
		{Offset: -1, Limit: 20},
		{Offset: maximumCatalogOffset + 1, Limit: 20},
		{ParentID: "not-a-uuid", Limit: 20},
		{MediaTypes: []string{"tv"}, Limit: 20},
		{MinCommunityRating: float64Pointer(-0.1), Limit: 20},
		{MinCommunityRating: float64Pointer(10.1), Limit: 20},
		{SortBy: "communityrating", SortOrder: "ascending", Limit: 20},
		{SortBy: "sortname,productionyear,datecreated,sortname", SortOrder: "ascending", Limit: 20},
	} {
		if _, err := normalizeCatalogQuery(invalid); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid catalog query %+v, got %v", invalid, err)
		}
	}
}

func TestCatalogTitleBatchRejectsUnboundedOrMalformedIDsBeforeDatabase(t *testing.T) {
	service := &Service{}
	tooMany := make([]string, maximumCatalogIDs+1)
	for index := range tooMany {
		tooMany[index] = "11111111-1111-4111-8111-111111111111"
	}
	if _, err := service.GetCatalogTitles(context.Background(), auth.Principal{}, tooMany); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unbounded batch error = %v, want ErrInvalidInput", err)
	}
	if _, err := service.GetCatalogTitles(context.Background(), auth.Principal{}, []string{"not-a-uuid"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("malformed batch error = %v, want ErrInvalidInput", err)
	}
}

func TestCatalogReaderScopesHierarchyPaginationAndProviderIDs(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the catalog reader test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	counter := &catalogQueryCounter{}
	config.ConnConfig.Tracer = counter
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profiles (
			id uuid PRIMARY KEY, category_id uuid
		);
		CREATE TEMPORARY TABLE user_profile_access (
			user_id uuid NOT NULL, profile_id uuid NOT NULL, can_manage boolean NOT NULL DEFAULT false,
			PRIMARY KEY (user_id, profile_id)
		);
		CREATE TEMPORARY TABLE profile_addons (
			id uuid PRIMARY KEY, enabled boolean NOT NULL DEFAULT true
		);
		CREATE TEMPORARY TABLE addon_profile_access (
			addon_id uuid NOT NULL, profile_id uuid NOT NULL,
			PRIMARY KEY (addon_id, profile_id)
		);
		CREATE TEMPORARY TABLE addon_category_access (
			addon_id uuid NOT NULL, category_id uuid NOT NULL,
			PRIMARY KEY (addon_id, category_id)
		);
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY, media_type text NOT NULL,
			parent_id uuid REFERENCES titles(id) ON DELETE CASCADE, ordinal integer,
			display_title text, poster_url text, background_url text, release_info text,
			release_date date, resource_id text, resource_provider text,
			source_addon_id uuid, source_catalog_id text, source_name text,
			country text, language text, category text,
			is_current boolean NOT NULL DEFAULT true,
			created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE TEMPORARY TABLE title_external_ids (
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			provider text NOT NULL, namespace text NOT NULL, external_id text NOT NULL,
			PRIMARY KEY (provider, namespace, external_id)
		);
		CREATE TEMPORARY TABLE profile_title_external_ids (
			profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			provider text NOT NULL, namespace text NOT NULL, external_id text NOT NULL,
			PRIMARY KEY (profile_id, provider, namespace, external_id), UNIQUE (title_id)
		);
		CREATE TEMPORARY TABLE profile_library (
			profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			added_at timestamptz NOT NULL, updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
		CREATE TEMPORARY TABLE profile_favorites (
			profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			created_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
		CREATE TEMPORARY TABLE profile_user_data (
			profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			rating double precision, rating_set boolean NOT NULL DEFAULT false,
			played_percentage double precision, played_percentage_set boolean NOT NULL DEFAULT false,
			unplayed_item_count integer, unplayed_item_count_set boolean NOT NULL DEFAULT false,
			play_count integer, play_count_set boolean NOT NULL DEFAULT false,
			likes boolean, likes_set boolean NOT NULL DEFAULT false,
			last_played_date timestamptz, last_played_date_submicrosecond smallint,
			last_played_date_set boolean NOT NULL DEFAULT false,
			PRIMARY KEY (profile_id, title_id)
		);
		CREATE TEMPORARY TABLE profile_progress (
			profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			position_seconds integer NOT NULL, duration_seconds integer NOT NULL,
			completed boolean NOT NULL DEFAULT false, version bigint NOT NULL DEFAULT 1,
			last_watched_at timestamptz NOT NULL, updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
		CREATE TEMPORARY TABLE title_metadata (
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			provider text NOT NULL, language text NOT NULL, payload jsonb NOT NULL,
			expires_at timestamptz NOT NULL, updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (title_id, provider, language)
		);

		INSERT INTO profiles (id, category_id) VALUES
			('11111111-1111-4111-8111-111111111111', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'),
			('22222222-2222-4222-8222-222222222222', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb');
		INSERT INTO profile_addons (id, enabled)
		VALUES ('99999999-9999-4999-8999-999999999999', true);
		INSERT INTO addon_profile_access (addon_id, profile_id) VALUES
			('99999999-9999-4999-8999-999999999999', '11111111-1111-4111-8111-111111111111'),
			('99999999-9999-4999-8999-999999999999', '22222222-2222-4222-8222-222222222222');

		INSERT INTO titles (
			id, media_type, parent_id, ordinal, display_title, poster_url, background_url,
			release_info, release_date, resource_id, resource_provider,
			source_addon_id, source_catalog_id, source_name, country, language, category
		) VALUES
			('00000000-0000-4000-8000-000000000100', 'movie', NULL, NULL, 'ÉCLAIR Movie', '/api/v1/artwork/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', '/api/v1/artwork/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', '2025', '2025-01-02', 'movie-100', 'tmdb', NULL, NULL, NULL, NULL, NULL, NULL),
			('00000000-0000-4000-8000-000000000101', 'movie', NULL, NULL, 'Metadata Only Movie', NULL, NULL, '2026', '2026-02-03', 'movie-101', 'tmdb', NULL, NULL, NULL, NULL, NULL, NULL),
			('00000000-0000-4000-8000-000000000200', 'series', NULL, NULL, 'Canonical Series', NULL, NULL, '2020-', '2020-03-04', 'series-200', 'tmdb', NULL, NULL, NULL, NULL, NULL, NULL),
			('00000000-0000-4000-8000-000000000210', 'season', '00000000-0000-4000-8000-000000000200', 0, 'Specials', NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL),
			('00000000-0000-4000-8000-000000000211', 'episode', '00000000-0000-4000-8000-000000000210', 1, 'Pilot Special', '/api/v1/artwork/cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc', NULL, 'S00E01', '2020-03-04', 'episode-211', 'tvdb', NULL, NULL, NULL, NULL, NULL, NULL),
			('00000000-0000-4000-8000-000000000220', 'season', '00000000-0000-4000-8000-000000000200', 1, 'Season 1', NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL),
			('00000000-0000-4000-8000-000000000221', 'episode', '00000000-0000-4000-8000-000000000220', 2, 'Second Episode', NULL, NULL, 'S01E02', '2020-03-11', 'episode-221', 'tvdb', NULL, NULL, NULL, NULL, NULL, NULL),
			('00000000-0000-4000-8000-000000000300', 'series', NULL, NULL, 'Profile One Custom', '/api/v1/artwork/dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd', NULL, 'Custom', NULL, 'custom-series-one', 'addon', '99999999-9999-4999-8999-999999999999', 'custom', 'Addon One', 'US', 'en', 'Drama'),
			('00000000-0000-4000-8000-000000000310', 'season', '00000000-0000-4000-8000-000000000300', 0, 'Specials', NULL, NULL, NULL, NULL, NULL, NULL, '99999999-9999-4999-8999-999999999999', NULL, NULL, NULL, NULL, NULL),
			('00000000-0000-4000-8000-000000000311', 'episode', '00000000-0000-4000-8000-000000000310', 4, 'Custom Special', NULL, NULL, 'S00E04', '2026-01-01', 'custom-episode-one', 'addon', '99999999-9999-4999-8999-999999999999', NULL, NULL, NULL, NULL, NULL),
			('00000000-0000-4000-8000-000000000320', 'series', NULL, NULL, 'Profile One Search Result', NULL, NULL, 'Custom', NULL, 'custom-series-search', 'addon', '99999999-9999-4999-8999-999999999999', 'search', 'Addon One', 'US', 'en', 'Drama'),
			('00000000-0000-4000-8000-000000000321', 'season', '00000000-0000-4000-8000-000000000320', 1, 'One', NULL, NULL, NULL, NULL, NULL, NULL, '99999999-9999-4999-8999-999999999999', NULL, NULL, NULL, NULL, NULL),
			('00000000-0000-4000-8000-000000000400', 'series', NULL, NULL, 'Éclair Hidden', NULL, NULL, 'Custom', NULL, 'custom-series-two', 'addon', '99999999-9999-4999-8999-999999999999', 'custom', 'Addon Two', 'GB', 'en', 'Comedy');
		UPDATE titles
		SET created_at = CASE id
		        WHEN '00000000-0000-4000-8000-000000000100'::uuid THEN '2026-08-06T12:03:00Z'::timestamptz
		        WHEN '00000000-0000-4000-8000-000000000200'::uuid THEN '2026-08-06T12:01:00Z'::timestamptz
		        WHEN '00000000-0000-4000-8000-000000000300'::uuid THEN '2026-08-06T12:02:00Z'::timestamptz
		        ELSE '2026-08-06T12:00:00Z'::timestamptz
		    END,
		    updated_at = CASE id
		        WHEN '00000000-0000-4000-8000-000000000100'::uuid THEN '2026-08-06T12:03:00Z'::timestamptz
		        WHEN '00000000-0000-4000-8000-000000000221'::uuid THEN '2026-08-06T12:05:00Z'::timestamptz
		        WHEN '00000000-0000-4000-8000-000000000311'::uuid THEN '2026-08-06T12:04:00Z'::timestamptz
		        ELSE '2026-08-06T12:00:00Z'::timestamptz
		    END;

		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			('00000000-0000-4000-8000-000000000100', 'tmdb', 'movie', '100'),
			('00000000-0000-4000-8000-000000000100', 'imdb', 'movie', 'tt0000100'),
			('00000000-0000-4000-8000-000000000101', 'imdb', 'movie', 'tt0000101'),
			('00000000-0000-4000-8000-000000000101', 'url', 'movie', 'https://provider.invalid/title/101'),
			('00000000-0000-4000-8000-000000000200', 'tmdb', 'series', '200'),
			('00000000-0000-4000-8000-000000000200', 'tvdb', 'series', '2000'),
			('00000000-0000-4000-8000-000000000210', 'tmdb', 'season', '200-season-0'),
			('00000000-0000-4000-8000-000000000211', 'tvdb', 'episode', '2110'),
			('00000000-0000-4000-8000-000000000220', 'tmdb', 'season', '200-season-1'),
			('00000000-0000-4000-8000-000000000221', 'tvdb', 'episode', '2210'),
			('00000000-0000-4000-8000-000000000321', 'tmdb', 'season', '320-season-1');
		INSERT INTO profile_title_external_ids (profile_id, title_id, provider, namespace, external_id) VALUES
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000300', 'addon', 'series', 'series-profile-one'),
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000310', 'addon', 'season', 'season-profile-one-0'),
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000311', 'addon', 'episode', 'episode-profile-one-4'),
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000320', 'addon', 'series', 'series-profile-one-search'),
			('22222222-2222-4222-8222-222222222222', '00000000-0000-4000-8000-000000000400', 'addon', 'series', 'series-profile-two');
		INSERT INTO profile_library (profile_id, title_id, added_at) VALUES
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000100', '2026-08-06T12:03:00Z'),
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000200', '2026-08-06T12:02:00Z'),
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000300', '2026-08-06T12:01:00Z'),
			('22222222-2222-4222-8222-222222222222', '00000000-0000-4000-8000-000000000400', '2026-08-06T12:04:00Z');
		INSERT INTO profile_favorites (profile_id, title_id) VALUES
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000101');
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at, updated_at) VALUES
			('00000000-0000-4000-8000-000000000100', 'tmdb', 'fr-FR',
			 '{"overview":"Résumé matérialisé","runtimeMinutes":123,"genres":[{"id":18,"name":"Drama"}],"studios":[{"name":"Éclair Films"},{"name":"éCLAIR FILMS"},{"name":"Beta Studio"}],"voteAverage":8.25,"hasSubtitles":true,"officialRating":"PG-13","tags":["Featured"],"cast":[{"id":"9301","name":"Alice Actor","character":"Lead"}]}',
			 now() + interval '1 hour', '2026-08-06T12:05:00Z'),
			('00000000-0000-4000-8000-000000000101', 'tmdb', 'fr-FR',
			 '{"overview":"Résultat metadata autorisé","runtimeMinutes":95,"genres":[{"id":12,"name":"Adventure"}],"voteAverage":7.5}',
			 now() + interval '1 hour', '2026-08-06T12:05:30Z');
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at, updated_at) VALUES
			('00000000-0000-4000-8000-000000000400', 'addon', 'en',
			 '{"studios":[{"name":"Profile Two Secret Studio"}]}',
			 now() + interval '1 hour', '2026-08-06T12:05:45Z');
		INSERT INTO profile_progress (profile_id, title_id, position_seconds, duration_seconds, completed, last_watched_at) VALUES
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000100', 61, 7380, true, '2026-08-06T12:06:00Z'),
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000101', 120, 5700, false, '2026-08-06T12:07:00Z');
	`); err != nil {
		t.Fatalf("create catalog fixtures: %v", err)
	}

	profileOneID := "11111111-1111-4111-8111-111111111111"
	profileTwoID := "22222222-2222-4222-8222-222222222222"
	expiresAt := time.Now().UTC().Add(time.Hour)
	profileOne := auth.Principal{
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: &profileOneID, ProfileGrantExpiresAt: &expiresAt,
	}
	profileTwo := auth.Principal{
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: &profileTwoID, ProfileGrantExpiresAt: &expiresAt,
	}
	service := NewService(pool, time.UTC)

	var beforeCount int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM titles`).Scan(&beforeCount); err != nil {
		t.Fatalf("count titles before reads: %v", err)
	}
	roots, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{Offset: 1, Limit: 2})
	if err != nil {
		t.Fatalf("list catalog roots: %v", err)
	}
	if roots.Total != 3 || roots.Offset != 1 || roots.Limit != 2 || len(roots.Items) != 2 ||
		roots.Items[0].ID != "00000000-0000-4000-8000-000000000200" ||
		roots.Items[1].ID != "00000000-0000-4000-8000-000000000300" {
		t.Fatalf("unexpected exact root page: %+v", roots)
	}
	if roots.Items[1].ProviderIDs["addon"] != "series-profile-one" ||
		roots.Items[1].SourceAddonID != "99999999-9999-4999-8999-999999999999" ||
		roots.Items[1].ResourceID != "custom-series-one" || roots.Items[1].SourceName != "Addon One" {
		t.Fatalf("custom root lost scoped identity or provenance snapshot: %+v", roots.Items[1])
	}
	if counter.pages.Load() != 1 {
		t.Fatalf("root page emitted %d catalog page queries, want 1", counter.pages.Load())
	}

	seasons, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{
		ParentID: "00000000-0000-4000-8000-000000000200", MediaTypes: []string{"season"}, Limit: 20,
	})
	if err != nil {
		t.Fatalf("list series seasons: %v", err)
	}
	if seasons.Total != 2 || len(seasons.Items) != 2 || seasons.Items[0].Ordinal == nil || *seasons.Items[0].Ordinal != 0 ||
		seasons.Items[0].Title != "Specials" || seasons.Items[1].Ordinal == nil || *seasons.Items[1].Ordinal != 1 ||
		seasons.Items[0].SeriesID != "00000000-0000-4000-8000-000000000200" || seasons.Items[0].SeriesTitle != "Canonical Series" {
		t.Fatalf("season zero or ordered hierarchy missing: %+v", seasons)
	}
	if counter.pages.Load() != 2 {
		t.Fatalf("season children emitted %d catalog page queries, want 2 total", counter.pages.Load())
	}

	episodes, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{
		ParentID: "00000000-0000-4000-8000-000000000210", MediaTypes: []string{"episode"}, Limit: 20,
	})
	if err != nil || episodes.Total != 1 || len(episodes.Items) != 1 ||
		episodes.Items[0].SeriesID != "00000000-0000-4000-8000-000000000200" || episodes.Items[0].SeriesTitle != "Canonical Series" ||
		episodes.Items[0].SeasonID != "00000000-0000-4000-8000-000000000210" || episodes.Items[0].SeasonTitle != "Specials" {
		t.Fatalf("list season-zero episodes lost authorized hierarchy: %+v error %v", episodes, err)
	}
	if counter.pages.Load() != 3 {
		t.Fatalf("episode children emitted %d catalog page queries, want 3 total", counter.pages.Load())
	}

	episode, err := service.GetCatalogTitle(ctx, profileOne, "00000000-0000-4000-8000-000000000211")
	if err != nil {
		t.Fatalf("get individual episode: %v", err)
	}
	if episode.MediaType != "episode" || episode.ParentID != "00000000-0000-4000-8000-000000000210" ||
		episode.SeasonID != "00000000-0000-4000-8000-000000000210" ||
		episode.SeriesID != "00000000-0000-4000-8000-000000000200" ||
		episode.ParentOrdinal == nil || *episode.ParentOrdinal != 0 || episode.Ordinal == nil || *episode.Ordinal != 1 ||
		episode.SeriesTitle != "Canonical Series" || episode.SeasonTitle != "Specials" ||
		episode.ProviderIDs["tvdb"] != "2110" || episode.Released != "2020-03-04" {
		t.Fatalf("individual episode snapshot incomplete: %+v", episode)
	}
	movie, err := service.GetCatalogTitle(ctx, profileOne, "00000000-0000-4000-8000-000000000100")
	if err != nil || !reflect.DeepEqual(movie.ProviderIDs, map[string]string{"imdb": "tt0000100", "tmdb": "100"}) {
		t.Fatalf("global provider IDs missing: %+v error %v", movie, err)
	}
	if movie.Title != "ÉCLAIR Movie" || movie.Overview != "Résumé matérialisé" || movie.RuntimeMinutes == nil || *movie.RuntimeMinutes != 123 ||
		!reflect.DeepEqual(movie.Genres, []string{"Drama"}) || !reflect.DeepEqual(movie.Studios, []string{"Beta Studio", "Éclair Films"}) ||
		movie.CommunityRating == nil || *movie.CommunityRating != 8.25 || !movie.HasSubtitles || !movie.InLibrary ||
		movie.Progress == nil || movie.Progress.PositionSeconds != 61 || !movie.Progress.Completed || movie.Progress.LastWatchedAt == nil {
		t.Fatalf("materialized metadata or user state missing: %+v", movie)
	}
	if counter.titles.Load() != 2 {
		t.Fatalf("two details emitted %d catalog title queries", counter.titles.Load())
	}
	nonLibrary, err := service.GetCatalogTitle(ctx, profileOne, "00000000-0000-4000-8000-000000000101")
	if err != nil {
		t.Fatalf("get metadata-only title: %v", err)
	}
	if nonLibrary.Title != "Metadata Only Movie" || nonLibrary.InLibrary || nonLibrary.ResourceID != "movie-101" ||
		nonLibrary.ResourceProvider != "tmdb" || nonLibrary.Overview != "Résultat metadata autorisé" ||
		nonLibrary.RuntimeMinutes == nil || *nonLibrary.RuntimeMinutes != 95 || nonLibrary.ProviderIDs["imdb"] != "tt0000101" ||
		nonLibrary.Progress == nil || nonLibrary.Progress.PositionSeconds != 120 || nonLibrary.Progress.Completed {
		t.Fatalf("metadata-only canonical projection incomplete: %+v", nonLibrary)
	}
	if len(nonLibrary.Studios) != 0 {
		t.Fatalf("title without studio metadata synthesized studios: %+v", nonLibrary.Studios)
	}
	batch, err := service.GetCatalogTitles(ctx, profileOne, []string{
		nonLibrary.ID,
		"00000000-0000-4000-8000-000000000400",
		nonLibrary.ID,
	})
	if err != nil || len(batch) != 1 || batch[0].ID != nonLibrary.ID || batch[0].Progress == nil || batch[0].InLibrary {
		t.Fatalf("non-library resume batch projection = %+v error %v", batch, err)
	}
	if counter.titles.Load() != 4 {
		t.Fatalf("detail and batch emitted %d catalog title queries, want 4", counter.titles.Load())
	}

	profileOneCustom, err := service.GetCatalogTitle(ctx, profileOne, "00000000-0000-4000-8000-000000000300")
	if err != nil || profileOneCustom.ProviderIDs["addon"] != "series-profile-one" {
		t.Fatalf("profile-one scoped provider ID missing: %+v error %v", profileOneCustom, err)
	}
	profileOneSearchResult, err := service.GetCatalogTitle(ctx, profileOne, "00000000-0000-4000-8000-000000000320")
	if err != nil || profileOneSearchResult.InLibrary || profileOneSearchResult.ProviderIDs["addon"] != "series-profile-one-search" {
		t.Fatalf("profile-one non-library add-on title missing: %+v error %v", profileOneSearchResult, err)
	}
	if _, err := service.GetCatalogTitle(ctx, profileTwo, profileOneSearchResult.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("profile-one non-library add-on title visible to profile two: %v", err)
	}
	if _, err := service.GetCatalogTitle(ctx, profileTwo, profileOneCustom.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("profile-one custom title visible to profile two: %v", err)
	}
	if _, err := service.GetCatalogTitle(ctx, profileOne, "00000000-0000-4000-8000-000000000400"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("profile-two custom title visible to profile one: %v", err)
	}
	profileTwoCustom, err := service.GetCatalogTitle(ctx, profileTwo, "00000000-0000-4000-8000-000000000400")
	if err != nil || !reflect.DeepEqual(profileTwoCustom.ProviderIDs, map[string]string{"addon": "series-profile-two"}) ||
		!reflect.DeepEqual(profileTwoCustom.Studios, []string{"Profile Two Secret Studio"}) {
		t.Fatalf("profile-two scoped provider ID or studio metadata missing or leaked: %+v error %v", profileTwoCustom, err)
	}

	unicodeSearch, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{
		SearchTerm: "éclair", Recursive: true, Offset: 0, Limit: 20,
	})
	if err != nil || unicodeSearch.Total != 1 || len(unicodeSearch.Items) != 1 || unicodeSearch.Items[0].ID != movie.ID {
		t.Fatalf("case-insensitive Unicode search leaked or missed titles: %+v error %v", unicodeSearch, err)
	}
	paginatedSearch, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{
		SearchTerm: "S", Recursive: true, Offset: 1, Limit: 2,
	})
	if err != nil || paginatedSearch.Total != 8 || paginatedSearch.Offset != 1 || len(paginatedSearch.Items) != 2 {
		t.Fatalf("search pagination lost exact total: %+v error %v", paginatedSearch, err)
	}
	if counter.pages.Load() != 5 {
		t.Fatalf("five list operations emitted %d catalog page queries, want 5", counter.pages.Load())
	}
	favorites, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{Favorite: boolPointer(true), Limit: 20})
	if err != nil || favorites.Total != 1 || len(favorites.Items) != 1 || favorites.Items[0].ID != nonLibrary.ID || !favorites.Items[0].Favorite || favorites.Items[0].InLibrary {
		t.Fatalf("independent favorite filter = %+v error %v", favorites, err)
	}
	played, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{Played: boolPointer(true), Limit: 20})
	if err != nil || played.Total != 1 || len(played.Items) != 1 || played.Items[0].ID != movie.ID {
		t.Fatalf("played filter = %+v error %v", played, err)
	}
	resumable, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{Resumable: boolPointer(true), Limit: 20})
	if err != nil || resumable.Total != 1 || len(resumable.Items) != 1 || resumable.Items[0].ID != nonLibrary.ID {
		t.Fatalf("resumable filter = %+v error %v", resumable, err)
	}
	combined, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{
		Played: boolPointer(true), MinCommunityRating: float64Pointer(8), HasSubtitles: boolPointer(true),
		Genres: []string{"dRaMa"}, GenreIDs: []string{"18"}, Years: []int{2025}, Studios: []string{"éclair films"},
		IDs: []string{movie.ID}, OfficialRatings: []string{"pg-13"}, Tags: []string{"FEATURED"}, PersonIDs: []string{"9301"}, Limit: 20, IncludePeople: true,
	})
	if err != nil || combined.Total != 1 || len(combined.Items) != 1 || combined.Items[0].ID != movie.ID ||
		len(combined.Items[0].People) != 1 || combined.Items[0].People[0].ID != "9301" {
		t.Fatalf("combined metadata filters = %+v error %v", combined, err)
	}
	withoutSubtitles, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{
		IDs: []string{nonLibrary.ID}, HasSubtitles: boolPointer(false), MinCommunityRating: float64Pointer(7.5), Limit: 20,
	})
	if err != nil || withoutSubtitles.Total != 1 || len(withoutSubtitles.Items) != 1 || withoutSubtitles.Items[0].ID != nonLibrary.ID || withoutSubtitles.Items[0].HasSubtitles {
		t.Fatalf("subtitle absence and minimum rating filters = %+v error %v", withoutSubtitles, err)
	}
	withoutTotal, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{Genres: []string{"Drama"}, Offset: 0, Limit: 1, OmitTotal: true})
	if err != nil || withoutTotal.Total != 0 || len(withoutTotal.Items) != 1 {
		t.Fatalf("disabled total filter = %+v error %v", withoutTotal, err)
	}
	unsupported, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{UnavailableDataFilter: true, Limit: 20})
	if err != nil || unsupported.Total != 0 || len(unsupported.Items) != 0 {
		t.Fatalf("unsupported data filter should be honestly empty: %+v error %v", unsupported, err)
	}
	empty, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{
		ParentID: "00000000-0000-4000-8000-000000000220", MediaTypes: []string{"episode"}, Offset: 10, Limit: 20,
	})
	if err != nil || empty.Total != 1 || len(empty.Items) != 0 {
		t.Fatalf("empty offset page lost exact total: %+v error %v", empty, err)
	}
	inaccessibleChildren, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{
		ParentID: "00000000-0000-4000-8000-000000000400", Limit: 20,
	})
	if err != nil || inaccessibleChildren.Total != 0 || len(inaccessibleChildren.Items) != 0 {
		t.Fatalf("inaccessible parent leaked children: %+v error %v", inaccessibleChildren, err)
	}
	linkedSeasons, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{
		ParentID: "00000000-0000-4000-8000-000000000320", MediaTypes: []string{"season"}, Limit: 20,
	})
	if err != nil || linkedSeasons.Total != 1 || len(linkedSeasons.Items) != 1 || linkedSeasons.Items[0].ID != "00000000-0000-4000-8000-000000000321" {
		t.Fatalf("accessible non-library series lost materialized seasons: %+v error %v", linkedSeasons, err)
	}
	dateCreated, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{
		SortBy: "datecreated,sortname,productionyear", SortOrder: "descending", Limit: 20,
	})
	if err != nil || len(dateCreated.Items) != 3 ||
		dateCreated.Items[0].ID != "00000000-0000-4000-8000-000000000100" ||
		dateCreated.Items[1].ID != "00000000-0000-4000-8000-000000000300" ||
		dateCreated.Items[2].ID != "00000000-0000-4000-8000-000000000200" ||
		dateCreated.Items[0].CreatedAt.IsZero() || dateCreated.Items[0].LastContentAddedAt.IsZero() {
		t.Fatalf("DateCreated ordering or projection = %+v error %v", dateCreated, err)
	}
	lastContent, err := service.ListCatalogItems(ctx, profileOne, CatalogQuery{
		SortBy: "datelastcontentadded,datecreated,sortname", SortOrder: "descending", Limit: 20,
	})
	if err != nil || len(lastContent.Items) != 3 ||
		lastContent.Items[0].ID != "00000000-0000-4000-8000-000000000200" ||
		lastContent.Items[1].ID != "00000000-0000-4000-8000-000000000300" ||
		lastContent.Items[2].ID != "00000000-0000-4000-8000-000000000100" {
		t.Fatalf("DateLastContentAdded ordering = %+v error %v", lastContent, err)
	}

	expiredAt := time.Now().UTC().Add(-time.Minute)
	expired := profileOne
	expired.ProfileGrantExpiresAt = &expiredAt
	if _, err := service.GetCatalogTitle(ctx, expired, movie.ID); !errors.Is(err, ErrProfileRequired) {
		t.Fatalf("expired profile grant read error = %v, want ErrProfileRequired", err)
	}
	if _, err := service.ListCatalogItems(ctx, auth.Principal{}, CatalogQuery{Limit: 20}); !errors.Is(err, ErrProfileRequired) {
		t.Fatalf("missing active profile page error = %v, want ErrProfileRequired", err)
	}
	var afterCount int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM titles`).Scan(&afterCount); err != nil {
		t.Fatalf("count titles after reads: %v", err)
	}
	if afterCount != beforeCount {
		t.Fatalf("read-only catalog mutated title count from %d to %d", beforeCount, afterCount)
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func float64Pointer(value float64) *float64 {
	return &value
}
