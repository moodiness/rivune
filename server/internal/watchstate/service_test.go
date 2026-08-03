package watchstate

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/tracking"
)

func TestActiveProfileIDRequiresUnexpiredSelection(t *testing.T) {
	profileID := "11111111-1111-4111-8111-111111111111"
	future := time.Now().UTC().Add(time.Hour)
	past := time.Now().UTC().Add(-time.Hour)

	tests := []struct {
		name      string
		principal auth.Principal
		wantErr   bool
	}{
		{name: "selected", principal: auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &future}},
		{name: "missing", principal: auth.Principal{}, wantErr: true},
		{name: "expired", principal: auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &past}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := activeProfileID(test.principal)
			if test.wantErr {
				if !errors.Is(err, ErrProfileRequired) {
					t.Fatalf("expected profile requirement, got %v", err)
				}
				return
			}
			if err != nil || got != profileID {
				t.Fatalf("expected profile %q, got %q error %v", profileID, got, err)
			}
		})
	}
}

func TestNormalizeLibraryQuery(t *testing.T) {
	mediaType, page, pageSize, err := normalizeLibraryQuery(" Series ", 0, 0)
	if err != nil {
		t.Fatalf("normalize defaults: %v", err)
	}
	if mediaType != "series" || page != 1 || pageSize != 20 {
		t.Fatalf("unexpected normalized query: %q %d %d", mediaType, page, pageSize)
	}
	mediaType, page, pageSize, err = normalizeLibraryQuery(" TV ", 1, 40)
	if err != nil || mediaType != "tv" || page != 1 || pageSize != 40 {
		t.Fatalf("unexpected normalized TV query: %q %d %d error %v", mediaType, page, pageSize, err)
	}
	for _, test := range []struct {
		mediaType string
		page      int
		pageSize  int
	}{
		{mediaType: "episode", page: 1, pageSize: 20},
		{mediaType: "movie", page: -1, pageSize: 20},
		{mediaType: "movie", page: 1, pageSize: 101},
	} {
		if _, _, _, err := normalizeLibraryQuery(test.mediaType, test.page, test.pageSize); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid query for %+v, got %v", test, err)
		}
	}
}

func TestValidateProgressInputBoundaries(t *testing.T) {
	valid := []UpdateProgressInput{
		{},
		{PositionSeconds: 10, DurationSeconds: 100, ExpectedVersion: 2},
		{PositionSeconds: 100, DurationSeconds: 100, Completed: true},
	}
	for _, input := range valid {
		if err := validateProgressInput(input); err != nil {
			t.Fatalf("expected valid input %+v, got %v", input, err)
		}
	}

	invalid := []UpdateProgressInput{
		{ExpectedVersion: -1},
		{PositionSeconds: -1, DurationSeconds: 100},
		{PositionSeconds: 1, DurationSeconds: 0},
		{PositionSeconds: 101, DurationSeconds: 100},
	}
	for _, input := range invalid {
		if err := validateProgressInput(input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid input %+v, got %v", input, err)
		}
	}
}

func TestNormalizeTitleID(t *testing.T) {
	got, err := normalizeTitleID(" 550E8400-E29B-41D4-A716-446655440000 ")
	if err != nil || got != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("unexpected normalized title ID %q error %v", got, err)
	}
	if _, err := normalizeTitleID("not-a-uuid"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid UUID error, got %v", err)
	}
}

func TestResolveTitleRejectsNonISOReleaseDate(t *testing.T) {
	profileID := "11111111-1111-4111-8111-111111111111"
	expiresAt := time.Now().UTC().Add(time.Hour)
	service := NewService(nil)
	_, err := service.ResolveTitle(context.Background(), auth.Principal{
		ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt,
	}, ResolveTitleInput{
		MediaType: "movie", Provider: "tmdb", ExternalID: "1", ResourceID: "1",
		Title: "Movie", Released: "2026-8-01",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid release date rejection, got %v", err)
	}
}

func TestResolveTitleRequiresAddonScopedTVIdentity(t *testing.T) {
	profileID := "11111111-1111-4111-8111-111111111111"
	expiresAt := time.Now().UTC().Add(time.Hour)
	principal := auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt}
	service := NewService(nil)

	for _, input := range []ResolveTitleInput{
		{MediaType: "tv", Provider: "tmdb", ResourceID: "channel", Title: "Channel", SourceAddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		{MediaType: "tv", Provider: "addon", ResourceID: "channel", Title: "Channel", SourceAddonID: "not-a-uuid"},
	} {
		if _, err := service.ResolveTitle(context.Background(), principal, input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid TV source identity rejection for %+v, got %v", input, err)
		}
	}
}

func TestResolveTitlePreservesCanonicalMetadataSnapshot(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL title resolution test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			media_type text NOT NULL,
			display_title text,
			poster_url text,
			background_url text,
			release_info text,
			release_date date,
			resource_id text,
			resource_provider text,
			source_addon_id uuid,
			source_catalog_id text,
			source_name text,
			country text,
			language text,
			category text,
			updated_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE TEMPORARY TABLE title_external_ids (
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			provider text NOT NULL,
			namespace text NOT NULL,
			external_id text NOT NULL,
			PRIMARY KEY (provider, namespace, external_id)
		);
		INSERT INTO titles (
			id, media_type, display_title, poster_url, background_url, release_info, release_date, resource_id, resource_provider
		) VALUES (
			'22222222-2222-4222-8222-222222222222', 'movie', 'Canonical Movie',
			'https://assets.fanart.tv/poster.jpg', 'https://assets.fanart.tv/background.jpg', '2025', '2025-04-18', '123', 'tmdb'
		);
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id)
		VALUES ('22222222-2222-4222-8222-222222222222', 'tmdb', 'movie', '123')
	`); err != nil {
		t.Fatalf("seed canonical title snapshot: %v", err)
	}

	profileID := "11111111-1111-4111-8111-111111111111"
	expiresAt := time.Now().UTC().Add(time.Hour)
	if _, err := NewService(pool).ResolveTitle(ctx, auth.Principal{
		ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt,
	}, ResolveTitleInput{
		MediaType:     "movie",
		Provider:      "tmdb",
		ExternalID:    "123",
		ResourceID:    "addon-resource",
		Title:         "Shallow Addon Movie",
		PosterURL:     "https://image.tmdb.org/shallow-poster.jpg",
		BackgroundURL: "https://image.tmdb.org/shallow-background.jpg",
		ReleaseInfo:   "2024",
		Released:      "2024-01-01",
	}); err != nil {
		t.Fatalf("resolve existing title: %v", err)
	}

	var title, posterURL, backgroundURL, releaseInfo, releaseDate, resourceID string
	if err := pool.QueryRow(ctx, `
		SELECT display_title, poster_url, background_url, release_info, release_date::text, resource_id
		FROM titles
		WHERE id = '22222222-2222-4222-8222-222222222222'
	`).Scan(&title, &posterURL, &backgroundURL, &releaseInfo, &releaseDate, &resourceID); err != nil {
		t.Fatalf("query resolved title snapshot: %v", err)
	}
	if title != "Canonical Movie" ||
		posterURL != "https://assets.fanart.tv/poster.jpg" ||
		backgroundURL != "https://assets.fanart.tv/background.jpg" ||
		releaseInfo != "2025" ||
		releaseDate != "2025-04-18" {
		t.Fatalf("shallow title resolution replaced canonical metadata: title=%q poster=%q background=%q releaseInfo=%q releaseDate=%q",
			title, posterURL, backgroundURL, releaseInfo, releaseDate)
	}
	if resourceID != "addon-resource" {
		t.Fatalf("resource identity was not refreshed: %q", resourceID)
	}
}

type recordingTrackingSink struct {
	calls int
}

func (sink *recordingTrackingSink) EnqueueTx(context.Context, pgx.Tx, string, string, string, tracking.Event) error {
	sink.calls++
	return nil
}

func TestTVLibraryIsProfileScopedAndSurvivesAddonRemoval(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL TV library test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			media_type text NOT NULL,
			display_title text,
			poster_url text,
			background_url text,
			release_info text,
			release_date date,
			resource_id text,
			resource_provider text,
			source_addon_id uuid,
			source_catalog_id text,
			source_name text,
			country text,
			language text,
			category text,
			updated_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE TEMPORARY TABLE title_external_ids (
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			provider text NOT NULL,
			namespace text NOT NULL,
			external_id text NOT NULL,
			PRIMARY KEY (provider, namespace, external_id)
		);
		CREATE TEMPORARY TABLE addon_profile_access (
			addon_id uuid NOT NULL,
			profile_id uuid NOT NULL,
			PRIMARY KEY (addon_id, profile_id)
		);
		CREATE TEMPORARY TABLE profile_library (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			added_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
		INSERT INTO addon_profile_access (addon_id, profile_id) VALUES
			('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', '11111111-1111-4111-8111-111111111111'),
			('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', '22222222-2222-4222-8222-222222222222');
	`); err != nil {
		t.Fatalf("create TV library fixtures: %v", err)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	profileOneID := "11111111-1111-4111-8111-111111111111"
	profileTwoID := "22222222-2222-4222-8222-222222222222"
	profileOne := auth.Principal{ActiveProfileID: &profileOneID, ProfileGrantExpiresAt: &expiresAt}
	profileTwo := auth.Principal{ActiveProfileID: &profileTwoID, ProfileGrantExpiresAt: &expiresAt}
	trackingSink := &recordingTrackingSink{}
	service := NewService(pool, trackingSink)

	first, err := service.ResolveTitle(ctx, profileOne, ResolveTitleInput{
		MediaType: "tv", Provider: "addon", ExternalID: "https://stream.invalid/live.m3u8",
		ResourceID: "news", Title: "News", SourceAddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		SourceCatalogID: "live", SourceName: "Provider One", Country: "US", Language: "en", Category: "News",
	})
	if err != nil {
		t.Fatalf("resolve first TV channel: %v", err)
	}
	sameChannel, err := service.ResolveTitle(ctx, profileOne, ResolveTitleInput{
		MediaType: "tv", Provider: "addon", ResourceID: "news", Title: "News",
		SourceAddonID:   "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		SourceCatalogID: "regional", SourceName: "Provider One", Country: "US", Language: "en", Category: "News",
	})
	if err != nil {
		t.Fatalf("resolve same TV channel from another catalog: %v", err)
	}
	if sameChannel.TitleID != first.TitleID || sameChannel.ExternalID != first.ExternalID {
		t.Fatalf("source catalog context changed durable TV identity: first=%+v same=%+v", first, sameChannel)
	}
	second, err := service.ResolveTitle(ctx, profileTwo, ResolveTitleInput{
		MediaType: "tv", Provider: "addon", ResourceID: "news", Title: "News",
		SourceAddonID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", SourceCatalogID: "live",
		SourceName: "Provider Two", Country: "US", Language: "en", Category: "News",
	})
	if err != nil {
		t.Fatalf("resolve homonymous TV channel: %v", err)
	}
	if first.TitleID == second.TitleID || first.ExternalID == second.ExternalID {
		t.Fatalf("homonymous channels from distinct addons collided: first=%+v second=%+v", first, second)
	}
	if first.ExternalID == "https://stream.invalid/live.m3u8" {
		t.Fatal("TV resolution persisted the caller-provided stream URL as identity")
	}
	if _, err := service.ResolveTitle(ctx, profileTwo, ResolveTitleInput{
		MediaType: "tv", Provider: "addon", ResourceID: "news", Title: "News",
		SourceAddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected inaccessible profile addon to be hidden, got %v", err)
	}

	if _, err := service.AddLibrary(ctx, profileOne, first.TitleID); err != nil {
		t.Fatalf("add first profile TV library entry: %v", err)
	}
	if _, err := service.AddLibrary(ctx, profileTwo, second.TitleID); err != nil {
		t.Fatalf("add second profile TV library entry: %v", err)
	}
	firstPage, err := service.Library(ctx, profileOne, "tv", 1, 20)
	if err != nil {
		t.Fatalf("list first profile TV library: %v", err)
	}
	secondPage, err := service.Library(ctx, profileTwo, "tv", 1, 20)
	if err != nil {
		t.Fatalf("list second profile TV library: %v", err)
	}
	if firstPage.TotalResults != 1 || len(firstPage.Items) != 1 || firstPage.Items[0].TitleID != first.TitleID || !firstPage.Items[0].Available {
		t.Fatalf("unexpected first profile TV library: %+v", firstPage)
	}
	if secondPage.TotalResults != 1 || len(secondPage.Items) != 1 || secondPage.Items[0].TitleID != second.TitleID {
		t.Fatalf("unexpected second profile TV library: %+v", secondPage)
	}
	if firstPage.Items[0].SourceAddonID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" ||
		firstPage.Items[0].SourceCatalogID != "regional" || firstPage.Items[0].SourceName != "Provider One" {
		t.Fatalf("TV source snapshots were not returned: %+v", firstPage.Items[0])
	}

	if _, err := pool.Exec(ctx, `
		DELETE FROM addon_profile_access
		WHERE addon_id = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
		  AND profile_id = '11111111-1111-4111-8111-111111111111'
	`); err != nil {
		t.Fatalf("remove first profile addon access: %v", err)
	}
	unavailablePage, err := service.Library(ctx, profileOne, "tv", 1, 20)
	if err != nil {
		t.Fatalf("list unavailable TV library entry: %v", err)
	}
	if unavailablePage.TotalResults != 1 || len(unavailablePage.Items) != 1 || unavailablePage.Items[0].Available {
		t.Fatalf("unavailable TV entry was removed or reported available: %+v", unavailablePage)
	}
	if err := service.RemoveLibrary(ctx, profileOne, first.TitleID); err != nil {
		t.Fatalf("remove unavailable TV library entry: %v", err)
	}
	emptyPage, err := service.Library(ctx, profileOne, "tv", 1, 20)
	if err != nil || emptyPage.TotalResults != 0 || len(emptyPage.Items) != 0 {
		t.Fatalf("removed TV entry remained in profile library: page=%+v err=%v", emptyPage, err)
	}
	if trackingSink.calls != 0 {
		t.Fatalf("TV library mutations were sent to tracking integrations %d times", trackingSink.calls)
	}
}

func TestNextEpisodeQuerySkipsKnownFutureSeasonAfterCompletedSeason(t *testing.T) {
	query := strings.Join(strings.Fields(nextEpisodeQuery), " ")

	for _, clause := range []string{
		"progress.completed AND season.ordinal > 0",
		"(candidate_season.ordinal, candidate_episode.ordinal) > (latest.season_number, latest.episode_number)",
		"(candidate_season.release_date IS NULL OR candidate_season.release_date <= CURRENT_DATE)",
		"(candidate_episode.release_date IS NULL OR candidate_episode.release_date <= CURRENT_DATE)",
	} {
		if !strings.Contains(query, clause) {
			t.Fatalf("next-episode query is missing %q", clause)
		}
	}
}

func TestNextEpisodeQueryKeepsReleasedAndUnknownCandidatesDeterministic(t *testing.T) {
	query := strings.Join(strings.Fields(nextEpisodeQuery), " ")

	if count := strings.Count(query, "release_date IS NULL OR"); count != 2 {
		t.Fatalf("unknown season and episode release dates must remain eligible; found %d nullable predicates", count)
	}
	if count := strings.Count(query, "release_date <= CURRENT_DATE"); count != 2 {
		t.Fatalf("released seasons and episodes must remain eligible; found %d database-date predicates", count)
	}
	if !strings.Contains(query, "ORDER BY candidate_season.ordinal, candidate_episode.ordinal LIMIT 1") {
		t.Fatal("next released or unknown-date episode must be selected in season and episode order")
	}
}

func TestNextEpisodeItemsExcludeKnownFutureReleases(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL next-episode service test")
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY,
			media_type text NOT NULL,
			parent_id uuid,
			ordinal integer,
			release_date date,
			display_title text,
			poster_url text,
			background_url text,
			release_info text,
			resource_id text,
			resource_provider text
		)
	`); err != nil {
		t.Fatalf("create temporary titles: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profile_progress (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL,
			completed boolean NOT NULL,
			last_watched_at timestamptz NOT NULL
		)
	`); err != nil {
		t.Fatalf("create temporary profile progress: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profile_continue_dismissals (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL,
			dismissed_at timestamptz NOT NULL,
			PRIMARY KEY (profile_id, title_id)
		)
	`); err != nil {
		t.Fatalf("create temporary continue dismissals: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (
			id, media_type, parent_id, ordinal, release_date, display_title,
			resource_id, resource_provider
		) VALUES
			('00000000-0000-4000-8000-000000000100', 'series', NULL, NULL, NULL, 'Future Season Show', 'future-season-show', 'tmdb'),
			('00000000-0000-4000-8000-000000000110', 'season', '00000000-0000-4000-8000-000000000100', 11, CURRENT_DATE - 365, 'Season 11', NULL, NULL),
			('00000000-0000-4000-8000-000000000111', 'episode', '00000000-0000-4000-8000-000000000110', 10, CURRENT_DATE - 30, 'Episode 10', NULL, NULL),
			('00000000-0000-4000-8000-000000000120', 'season', '00000000-0000-4000-8000-000000000100', 12, CURRENT_DATE + 30, 'Season 12', NULL, NULL),
			('00000000-0000-4000-8000-000000000121', 'episode', '00000000-0000-4000-8000-000000000120', 1, NULL, 'Episode 1', NULL, NULL),

			('00000000-0000-4000-8000-000000000200', 'series', NULL, NULL, NULL, 'Released Show', 'released-show', 'tmdb'),
			('00000000-0000-4000-8000-000000000210', 'season', '00000000-0000-4000-8000-000000000200', 1, CURRENT_DATE - 90, 'Season 1', NULL, NULL),
			('00000000-0000-4000-8000-000000000211', 'episode', '00000000-0000-4000-8000-000000000210', 1, CURRENT_DATE - 30, 'Episode 1', NULL, NULL),
			('00000000-0000-4000-8000-000000000212', 'episode', '00000000-0000-4000-8000-000000000210', 2, CURRENT_DATE + 1, 'Episode 2', NULL, NULL),
			('00000000-0000-4000-8000-000000000213', 'episode', '00000000-0000-4000-8000-000000000210', 3, CURRENT_DATE, 'Episode 3', NULL, NULL),

			('00000000-0000-4000-8000-000000000300', 'series', NULL, NULL, NULL, 'Legacy Show', 'legacy-show', 'tmdb'),
			('00000000-0000-4000-8000-000000000310', 'season', '00000000-0000-4000-8000-000000000300', 1, NULL, 'Season 1', NULL, NULL),
			('00000000-0000-4000-8000-000000000311', 'episode', '00000000-0000-4000-8000-000000000310', 1, NULL, 'Episode 1', NULL, NULL),
			('00000000-0000-4000-8000-000000000312', 'episode', '00000000-0000-4000-8000-000000000310', 2, NULL, 'Episode 2', NULL, NULL),
			('00000000-0000-4000-8000-000000000313', 'episode', '00000000-0000-4000-8000-000000000310', 3, NULL, 'Episode 3', NULL, NULL)
	`); err != nil {
		t.Fatalf("seed temporary titles: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO profile_progress (profile_id, title_id, completed, last_watched_at) VALUES
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000111', true, '2026-07-03T00:00:00Z'),
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000211', true, '2026-07-02T00:00:00Z'),
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000311', true, '2026-07-01T00:00:00Z')
	`); err != nil {
		t.Fatalf("seed temporary progress: %v", err)
	}

	items, err := NewService(pool).nextEpisodeItems(
		ctx,
		"11111111-1111-4111-8111-111111111111",
		nil,
		10,
	)
	if err != nil {
		t.Fatalf("load next episodes: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected released and legacy candidates only, got %#v", items)
	}

	if items[0].SeriesID != "00000000-0000-4000-8000-000000000200" ||
		items[0].TitleID != "00000000-0000-4000-8000-000000000213" ||
		items[0].EpisodeNumber == nil || *items[0].EpisodeNumber != 3 ||
		items[0].Reason != "next_episode" {
		t.Fatalf("expected the first released candidate after the future episode, got %#v", items[0])
	}
	if items[1].SeriesID != "00000000-0000-4000-8000-000000000300" ||
		items[1].TitleID != "00000000-0000-4000-8000-000000000312" ||
		items[1].EpisodeNumber == nil || *items[1].EpisodeNumber != 2 ||
		items[1].Reason != "next_episode" {
		t.Fatalf("expected the first deterministic unknown-date candidate, got %#v", items[1])
	}
}

func TestDismissContinuePersistsUntilNewWatchActivity(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL continue dismissal test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY,
			media_type text NOT NULL,
			parent_id uuid,
			ordinal integer,
			release_date date,
			display_title text,
			poster_url text,
			background_url text,
			release_info text,
			resource_id text,
			resource_provider text
		);
		CREATE TEMPORARY TABLE profile_progress (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL,
			position_seconds integer NOT NULL,
			duration_seconds integer NOT NULL,
			completed boolean NOT NULL DEFAULT false,
			version bigint NOT NULL DEFAULT 1,
			last_watched_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
		CREATE TEMPORARY TABLE profile_continue_dismissals (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL,
			dismissed_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
		INSERT INTO titles (id, media_type, parent_id, ordinal, display_title, resource_id, resource_provider) VALUES
			('00000000-0000-4000-8000-000000000400', 'series', NULL, NULL, 'Series', 'series', 'tmdb'),
			('00000000-0000-4000-8000-000000000410', 'season', '00000000-0000-4000-8000-000000000400', 1, 'Season 1', NULL, NULL),
			('00000000-0000-4000-8000-000000000411', 'episode', '00000000-0000-4000-8000-000000000410', 1, 'Episode 1', NULL, NULL),
			('00000000-0000-4000-8000-000000000500', 'movie', NULL, NULL, 'Movie', 'movie', 'tmdb');
		INSERT INTO profile_progress (profile_id, title_id, position_seconds, duration_seconds) VALUES
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000411', 200, 1000),
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000500', 300, 1000);
	`); err != nil {
		t.Fatalf("seed continue dismissal state: %v", err)
	}

	profileID := "11111111-1111-4111-8111-111111111111"
	expiresAt := time.Now().UTC().Add(time.Hour)
	principal := auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt}
	service := NewService(pool)

	initial, err := service.ContinueWatching(ctx, principal, 10)
	if err != nil || len(initial.Items) != 2 {
		t.Fatalf("initial continue items = %#v, error %v", initial.Items, err)
	}
	if err := service.DismissContinue(ctx, principal, "00000000-0000-4000-8000-000000000411"); err != nil {
		t.Fatalf("dismiss episode series: %v", err)
	}
	afterEpisode, err := service.ContinueWatching(ctx, principal, 10)
	if err != nil || len(afterEpisode.Items) != 1 || afterEpisode.Items[0].MediaType != "movie" {
		t.Fatalf("continue items after episode dismissal = %#v, error %v", afterEpisode.Items, err)
	}
	if err := service.DismissContinue(ctx, principal, "00000000-0000-4000-8000-000000000500"); err != nil {
		t.Fatalf("dismiss movie: %v", err)
	}
	afterMovie, err := service.ContinueWatching(ctx, principal, 10)
	if err != nil || len(afterMovie.Items) != 0 {
		t.Fatalf("continue items after movie dismissal = %#v, error %v", afterMovie.Items, err)
	}
	if _, err := service.UpdateProgress(ctx, principal, "00000000-0000-4000-8000-000000000411", UpdateProgressInput{
		PositionSeconds: 250, DurationSeconds: 1000, ExpectedVersion: 1,
	}); err != nil {
		t.Fatalf("update dismissed episode progress: %v", err)
	}
	restored, err := service.ContinueWatching(ctx, principal, 10)
	if err != nil || len(restored.Items) != 1 || restored.Items[0].TitleID != "00000000-0000-4000-8000-000000000411" {
		t.Fatalf("restored continue items = %#v, error %v", restored.Items, err)
	}
}
