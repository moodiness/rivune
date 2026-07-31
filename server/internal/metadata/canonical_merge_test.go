package metadata

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

type canonicalMergeProvider struct {
	movie  ProviderMovie
	series ProviderSeries
	season ProviderSeason
}

func (provider *canonicalMergeProvider) DiscoverMovies(context.Context, QueryOptions) (ProviderMoviePage, error) {
	return ProviderMoviePage{}, nil
}
func (provider *canonicalMergeProvider) SearchMovies(context.Context, SearchOptions) (ProviderMoviePage, error) {
	return ProviderMoviePage{}, nil
}
func (provider *canonicalMergeProvider) MovieDetails(_ context.Context, externalID, _ string) (ProviderMovie, error) {
	if externalID != provider.movie.ExternalID {
		return ProviderMovie{}, ErrProviderNotFound
	}
	return provider.movie, nil
}
func (provider *canonicalMergeProvider) DiscoverSeries(context.Context, QueryOptions) (ProviderSeriesPage, error) {
	return ProviderSeriesPage{}, nil
}
func (provider *canonicalMergeProvider) SearchSeries(context.Context, SearchOptions) (ProviderSeriesPage, error) {
	return ProviderSeriesPage{}, nil
}
func (provider *canonicalMergeProvider) SeriesDetails(_ context.Context, externalID, _ string) (ProviderSeries, error) {
	if externalID != provider.series.ExternalID {
		return ProviderSeries{}, ErrProviderNotFound
	}
	return provider.series, nil
}
func (provider *canonicalMergeProvider) SeasonDetails(_ context.Context, externalID string, seasonNumber int, _ string) (ProviderSeason, error) {
	if externalID != provider.series.ExternalID || seasonNumber != provider.season.SeasonNumber {
		return ProviderSeason{}, ErrProviderNotFound
	}
	return provider.season, nil
}
func (provider *canonicalMergeProvider) ResolveExternalID(_ context.Context, mediaType, externalProvider, externalID string) (string, error) {
	if mediaType == MediaTypeMovie && externalProvider == "imdb" && externalID == "tt0948470" {
		return provider.movie.ExternalID, nil
	}
	if mediaType == MediaTypeSeries && externalProvider == "imdb" && externalID == "tt14688458" {
		return provider.series.ExternalID, nil
	}
	return "", ErrProviderNotFound
}

func TestMovieDetailsConsolidatesResolvedCanonicalTitle(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, display_title) VALUES
			($1::uuid, 'movie', 'Requested Spider-Man'), ($2::uuid, 'movie', 'TMDB Spider-Man');
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($1::uuid, 'imdb', 'movie', 'tt0948470'), ($2::uuid, 'tmdb', 'movie', '1930');
		INSERT INTO profile_library (profile_id, title_id, added_at, updated_at) VALUES
			($3::uuid, $1::uuid, '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z'),
			($3::uuid, $2::uuid, '2024-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`, pgx.QueryExecModeSimpleProtocol, canonicalDestinationMovieID, canonicalSourceMovieID, canonicalProfileID); err != nil {
		t.Fatalf("seed canonical movies: %v", err)
	}
	provider := &canonicalMergeProvider{movie: ProviderMovie{
		ExternalID: "1930", Title: "The Amazing Spider-Man", ReleaseDate: "2012-06-23",
		AdditionalIDs: map[string]string{"imdb": "tt0948470"},
	}}
	service := NewService(pool, provider, nil, time.Hour, nil)
	movie, err := service.MovieDetails(ctx, canonicalMergePrincipal(), canonicalDestinationMovieID, "fr-FR")
	if err != nil {
		t.Fatalf("load IMDb-only movie through resolved TMDB identity: %v", err)
	}
	if movie.ID != canonicalDestinationMovieID || movie.ExternalIDs["tmdb"] != "1930" || movie.ExternalIDs["imdb"] != "tt0948470" {
		t.Fatalf("unexpected consolidated movie: %#v", movie)
	}
	var titleCount, libraryCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM titles`).Scan(&titleCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM profile_library WHERE profile_id = $1::uuid`, canonicalProfileID).Scan(&libraryCount); err != nil {
		t.Fatal(err)
	}
	if titleCount != 1 || libraryCount != 1 {
		t.Fatalf("movie identities were not consolidated: titles=%d library=%d", titleCount, libraryCount)
	}
}

func TestSeriesDetailsConsolidatesResolvedCanonicalTitleHierarchyAndProfileState(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	ctx := context.Background()
	seedCanonicalMergeSuccess(t, pool)
	provider := &canonicalMergeProvider{
		series: ProviderSeries{
			ExternalID: "125988", Name: "Silo", Overview: "People survive underground.", FirstAirDate: "2023-05-04",
			AdditionalIDs: map[string]string{"imdb": "tt14688458"},
			Seasons:       []ProviderSeasonSummary{{ExternalID: "season-125988-1", Name: "Season 1", SeasonNumber: 1, EpisodeCount: 2}},
		},
		season: ProviderSeason{
			ExternalID: "season-125988-1", Name: "Season 1", SeasonNumber: 1,
			Episodes: []ProviderEpisode{
				{ExternalID: "episode-1", Name: "Freedom Day", SeasonNumber: 1, EpisodeNumber: 1},
				{ExternalID: "episode-2", Name: "Holston's Pick", SeasonNumber: 1, EpisodeNumber: 2},
			},
		},
	}
	service := NewService(pool, provider, nil, time.Hour, nil)
	principal := canonicalMergePrincipal()
	series, err := service.SeriesDetails(ctx, principal, canonicalDestinationSeriesID, "fr-FR", "tmdb")
	if err != nil {
		t.Fatalf("load IMDb-only series through resolved TMDB identity: %v", err)
	}
	if series.ID != canonicalDestinationSeriesID || len(series.Seasons) != 1 || series.Seasons[0].ID != canonicalDestinationSeasonID {
		t.Fatalf("unexpected consolidated series: %#v", series)
	}
	season, err := service.SeasonDetails(ctx, principal, canonicalDestinationSeasonID, "fr-FR", "tmdb")
	if err != nil {
		t.Fatalf("load episodes from consolidated season: %v", err)
	}
	if len(season.Episodes) != 2 || season.Episodes[0].ID != canonicalDestinationEpisodeID || season.Episodes[1].ID != canonicalUniqueEpisodeID {
		t.Fatalf("unexpected consolidated episodes: %#v", season.Episodes)
	}
	for providerName, externalID := range map[string]string{"imdb": "tt14688458", "tmdb": "125988"} {
		var mappedTitleID string
		if err := pool.QueryRow(ctx, `SELECT title_id::text FROM title_external_ids WHERE provider = $1 AND namespace = 'series' AND external_id = $2`, providerName, externalID).Scan(&mappedTitleID); err != nil {
			t.Fatalf("query %s alias: %v", providerName, err)
		}
		if mappedTitleID != canonicalDestinationSeriesID {
			t.Fatalf("%s alias maps to %s", providerName, mappedTitleID)
		}
	}
	var remainingIDs []string
	rows, err := pool.Query(ctx, `SELECT id::text FROM titles ORDER BY id`)
	if err != nil {
		t.Fatalf("query remaining titles: %v", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan remaining title: %v", err)
		}
		remainingIDs = append(remainingIDs, id)
	}
	rows.Close()
	expectedIDs := []string{canonicalDestinationSeriesID, canonicalDestinationSeasonID, canonicalDestinationEpisodeID, canonicalUniqueEpisodeID}
	if len(remainingIDs) != len(expectedIDs) {
		t.Fatalf("unexpected title rows after consolidation: %v", remainingIDs)
	}
	for index := range expectedIDs {
		if remainingIDs[index] != expectedIDs[index] {
			t.Fatalf("unexpected title rows after consolidation: %v", remainingIDs)
		}
	}
	var uniqueParentID string
	if err := pool.QueryRow(ctx, `SELECT parent_id::text FROM titles WHERE id = $1::uuid`, canonicalUniqueEpisodeID).Scan(&uniqueParentID); err != nil {
		t.Fatalf("query moved unique episode: %v", err)
	}
	if uniqueParentID != canonicalDestinationSeasonID {
		t.Fatalf("unique episode parent is %s", uniqueParentID)
	}
	var addedAt, updatedAt time.Time
	var libraryCount int
	if err := pool.QueryRow(ctx, `SELECT min(added_at), max(updated_at), count(*) FROM profile_library WHERE profile_id = $1::uuid`, canonicalProfileID).Scan(&addedAt, &updatedAt, &libraryCount); err != nil {
		t.Fatalf("query merged library state: %v", err)
	}
	if libraryCount != 1 || !addedAt.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) || !updatedAt.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected merged library state count=%d added=%s updated=%s", libraryCount, addedAt, updatedAt)
	}
	var position, duration int
	var completed bool
	var version int64
	if err := pool.QueryRow(ctx, `SELECT position_seconds, duration_seconds, completed, version FROM profile_progress WHERE profile_id = $1::uuid AND title_id = $2::uuid`, canonicalProfileID, canonicalDestinationEpisodeID).Scan(&position, &duration, &completed, &version); err != nil {
		t.Fatalf("query merged progress state: %v", err)
	}
	if position != 90 || duration != 100 || !completed || version != 8 {
		t.Fatalf("unexpected merged progress position=%d duration=%d completed=%t version=%d", position, duration, completed, version)
	}
	var uniqueProgressTitleID string
	if err := pool.QueryRow(ctx, `SELECT title_id::text FROM profile_progress WHERE profile_id = $1::uuid AND title_id = $2::uuid`, canonicalOtherProfileID, canonicalUniqueEpisodeID).Scan(&uniqueProgressTitleID); err != nil {
		t.Fatalf("query unique episode progress: %v", err)
	}
}

func TestSeriesDetailsCanonicalConflictRollsBackAtomically(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, display_title) VALUES ($1::uuid, 'series', 'Requested Silo'), ($2::uuid, 'series', 'Conflicting Silo');
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($1::uuid, 'imdb', 'series', 'tt14688458'), ($2::uuid, 'tmdb', 'series', '125988'), ($2::uuid, 'imdb', 'series', 'tt-conflicting');
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at) VALUES ($2::uuid, 'tmdb', 'fr-FR', '{"cached":true}', now() + interval '1 hour');
		INSERT INTO profile_library (profile_id, title_id, added_at, updated_at) VALUES
			($3::uuid, $1::uuid, '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z'), ($3::uuid, $2::uuid, '2024-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`, pgx.QueryExecModeSimpleProtocol, canonicalDestinationSeriesID, canonicalSourceSeriesID, canonicalProfileID); err != nil {
		t.Fatalf("seed conflicting canonical titles: %v", err)
	}
	provider := &canonicalMergeProvider{series: ProviderSeries{ExternalID: "125988", Name: "Provider Silo", Overview: "Conflict fixture", AdditionalIDs: map[string]string{"imdb": "tt14688458"}}}
	service := NewService(pool, provider, nil, time.Hour, nil)
	_, err := service.SeriesDetails(ctx, canonicalMergePrincipal(), canonicalDestinationSeriesID, "fr-FR", "tmdb")
	if err == nil || err.Error() != "metadata provider returned a conflicting external ID" {
		t.Fatalf("expected contradictory identity error, got %v", err)
	}
	var titleCount, libraryCount, metadataCount int
	var destinationTitle string
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM titles`).Scan(&titleCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM profile_library`).Scan(&libraryCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM title_metadata`).Scan(&metadataCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT display_title FROM titles WHERE id = $1::uuid`, canonicalDestinationSeriesID).Scan(&destinationTitle); err != nil {
		t.Fatal(err)
	}
	if titleCount != 2 || libraryCount != 2 || metadataCount != 1 || destinationTitle != "Requested Silo" {
		t.Fatalf("conflicting merge was not atomic: titles=%d library=%d metadata=%d destination=%q", titleCount, libraryCount, metadataCount, destinationTitle)
	}
	var tmdbTitleID string
	if err := pool.QueryRow(ctx, `SELECT title_id::text FROM title_external_ids WHERE provider = 'tmdb' AND namespace = 'series' AND external_id = '125988'`).Scan(&tmdbTitleID); err != nil {
		t.Fatal(err)
	}
	if tmdbTitleID != canonicalSourceSeriesID {
		t.Fatalf("TMDB mapping changed despite rollback: %s", tmdbTitleID)
	}
}

const (
	canonicalDestinationMovieID   = "00000000-0000-4000-8000-000000000010"
	canonicalSourceMovieID        = "00000000-0000-4000-8000-000000000020"
	canonicalDestinationSeriesID  = "00000000-0000-4000-8000-000000000100"
	canonicalDestinationSeasonID  = "00000000-0000-4000-8000-000000000110"
	canonicalDestinationEpisodeID = "00000000-0000-4000-8000-000000000111"
	canonicalSourceSeriesID       = "00000000-0000-4000-8000-000000000200"
	canonicalSourceSeasonID       = "00000000-0000-4000-8000-000000000210"
	canonicalSourceEpisodeID      = "00000000-0000-4000-8000-000000000211"
	canonicalUniqueEpisodeID      = "00000000-0000-4000-8000-000000000212"
	canonicalProfileID            = "11111111-1111-4111-8111-111111111111"
	canonicalOtherProfileID       = "22222222-2222-4222-8222-222222222222"
)

func canonicalMergePrincipal() auth.Principal {
	profileID := canonicalProfileID
	expiresAt := time.Now().UTC().Add(time.Hour)
	return auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt}
}

func newCanonicalMergeTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL canonical metadata test")
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
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), `
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(), media_type text NOT NULL, parent_id uuid REFERENCES titles(id) ON DELETE CASCADE, ordinal integer,
			display_title text, poster_url text, background_url text, release_info text, resource_id text, resource_provider text, release_date date,
			created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), UNIQUE (parent_id, media_type, ordinal));
		CREATE TEMPORARY TABLE title_external_ids (
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE, provider text NOT NULL, namespace text NOT NULL, external_id text NOT NULL,
			PRIMARY KEY (provider, namespace, external_id), UNIQUE (title_id, provider));
		CREATE TEMPORARY TABLE title_metadata (
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE, provider text NOT NULL, language text NOT NULL, payload jsonb NOT NULL,
			expires_at timestamptz NOT NULL, updated_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (title_id, provider, language),
			FOREIGN KEY (title_id, provider) REFERENCES title_external_ids(title_id, provider) ON DELETE CASCADE);
		CREATE TEMPORARY TABLE profile_library (
			profile_id uuid NOT NULL, title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE, added_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
			PRIMARY KEY (profile_id, title_id));
		CREATE TEMPORARY TABLE profile_progress (
			profile_id uuid NOT NULL, title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE, position_seconds integer NOT NULL, duration_seconds integer NOT NULL,
			completed boolean NOT NULL, version bigint NOT NULL, last_watched_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, PRIMARY KEY (profile_id, title_id))
	`); err != nil {
		pool.Close()
		t.Fatalf("create canonical metadata test schema: %v", err)
	}
	return pool
}

func seedCanonicalMergeSuccess(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO titles (id, media_type, parent_id, ordinal, display_title) VALUES
			($1::uuid, 'series', NULL, NULL, 'Requested Silo'), ($2::uuid, 'series', NULL, NULL, 'TMDB Silo'),
			($3::uuid, 'season', $1::uuid, 1, 'Requested Season 1'), ($4::uuid, 'season', $2::uuid, 1, 'TMDB Season 1'),
			($5::uuid, 'episode', $3::uuid, 1, 'Requested Episode 1'), ($6::uuid, 'episode', $4::uuid, 1, 'TMDB Episode 1'),
			($7::uuid, 'episode', $4::uuid, 2, 'TMDB Episode 2');
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($1::uuid, 'imdb', 'series', 'tt14688458'), ($2::uuid, 'tmdb', 'series', '125988'),
			($3::uuid, 'tvdb', 'season', 'silo-season-1'), ($4::uuid, 'tmdb', 'season', 'season-125988-1'),
			($5::uuid, 'tvdb', 'episode', 'silo-episode-1'), ($6::uuid, 'tmdb', 'episode', 'episode-1'), ($7::uuid, 'tmdb', 'episode', 'episode-2');
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at) VALUES
			($1::uuid, 'imdb', 'fr-FR', '{"requested":true}', now() + interval '1 hour'), ($2::uuid, 'tmdb', 'fr-FR', '{"duplicate":true}', now() + interval '1 hour'),
			($3::uuid, 'tvdb', 'fr-FR', '{"requestedSeason":true}', now() + interval '1 hour'), ($4::uuid, 'tmdb', 'fr-FR', '{"duplicateSeason":true}', now() + interval '1 hour'),
			($5::uuid, 'tvdb', 'fr-FR', '{"requestedEpisode":true}', now() + interval '1 hour'), ($6::uuid, 'tmdb', 'fr-FR', '{"duplicateEpisode":true}', now() + interval '1 hour'),
			($7::uuid, 'tmdb', 'fr-FR', '{"movedEpisode":true}', now() + interval '1 hour');
		INSERT INTO profile_library (profile_id, title_id, added_at, updated_at) VALUES
			($8::uuid, $1::uuid, '2025-01-01T00:00:00Z', '2026-01-01T00:00:00Z'), ($8::uuid, $2::uuid, '2024-01-01T00:00:00Z', '2025-06-01T00:00:00Z');
		INSERT INTO profile_progress (profile_id, title_id, position_seconds, duration_seconds, completed, version, last_watched_at, updated_at) VALUES
			($8::uuid, $5::uuid, 20, 100, false, 4, '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z'),
			($8::uuid, $6::uuid, 90, 100, true, 7, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
			($9::uuid, $7::uuid, 10, 100, false, 3, '2026-02-01T00:00:00Z', '2026-02-01T00:00:00Z')
	`, pgx.QueryExecModeSimpleProtocol, canonicalDestinationSeriesID, canonicalSourceSeriesID, canonicalDestinationSeasonID, canonicalSourceSeasonID,
		canonicalDestinationEpisodeID, canonicalSourceEpisodeID, canonicalUniqueEpisodeID, canonicalProfileID, canonicalOtherProfileID); err != nil {
		t.Fatalf("seed canonical metadata hierarchy: %v", err)
	}
}

var _ Provider = (*canonicalMergeProvider)(nil)
var _ ExternalIDResolver = (*canonicalMergeProvider)(nil)
