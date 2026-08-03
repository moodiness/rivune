package metadata

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type refreshTestProvider struct {
	movies      map[string]ProviderMovie
	series      map[string]ProviderSeries
	seasons     map[string]ProviderSeason
	failures    map[string][]error
	resolutions map[string]string
	calls       map[string]int
}

func (provider *refreshTestProvider) call(key string) error {
	if provider.calls == nil {
		provider.calls = make(map[string]int)
	}
	index := provider.calls[key]
	provider.calls[key] = index + 1
	if index < len(provider.failures[key]) {
		return provider.failures[key][index]
	}
	return nil
}

func (provider *refreshTestProvider) DiscoverMovies(context.Context, QueryOptions) (ProviderMoviePage, error) {
	return ProviderMoviePage{}, nil
}

func (provider *refreshTestProvider) SearchMovies(context.Context, SearchOptions) (ProviderMoviePage, error) {
	return ProviderMoviePage{}, nil
}

func (provider *refreshTestProvider) MovieDetails(_ context.Context, externalID, _ string) (ProviderMovie, error) {
	key := "movie:" + externalID
	if err := provider.call(key); err != nil {
		return ProviderMovie{}, err
	}
	movie, ok := provider.movies[externalID]
	if !ok {
		return ProviderMovie{}, ErrProviderNotFound
	}
	return movie, nil
}

func (provider *refreshTestProvider) DiscoverSeries(context.Context, QueryOptions) (ProviderSeriesPage, error) {
	return ProviderSeriesPage{}, nil
}

func (provider *refreshTestProvider) SearchSeries(context.Context, SearchOptions) (ProviderSeriesPage, error) {
	return ProviderSeriesPage{}, nil
}

func (provider *refreshTestProvider) SeriesDetails(_ context.Context, externalID, _ string) (ProviderSeries, error) {
	key := "series:" + externalID
	if err := provider.call(key); err != nil {
		return ProviderSeries{}, err
	}
	series, ok := provider.series[externalID]
	if !ok {
		return ProviderSeries{}, ErrProviderNotFound
	}
	return series, nil
}

func (provider *refreshTestProvider) SeasonDetails(_ context.Context, externalID string, seasonNumber int, _ string) (ProviderSeason, error) {
	key := "season:" + externalID + ":" + strconv.Itoa(seasonNumber)
	if err := provider.call(key); err != nil {
		return ProviderSeason{}, err
	}
	season, ok := provider.seasons[key]
	if !ok {
		return ProviderSeason{}, ErrProviderNotFound
	}
	return season, nil
}

func (provider *refreshTestProvider) ResolveExternalID(_ context.Context, mediaType, externalProvider, externalID string) (string, error) {
	key := "resolve:" + mediaType + ":" + externalProvider + ":" + externalID
	if err := provider.call(key); err != nil {
		return "", err
	}
	resolved, ok := provider.resolutions[key]
	if !ok {
		return "", ErrProviderNotFound
	}
	return resolved, nil
}

func TestRefreshMissingRefreshesAllCanonicalMediaTypesAndDoesNotReselectEpisode(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	ctx := context.Background()
	const (
		movieID   = "10000000-0000-4000-8000-000000000001"
		seriesID  = "20000000-0000-4000-8000-000000000002"
		seasonID  = "30000000-0000-4000-8000-000000000003"
		episodeID = "40000000-0000-4000-8000-000000000004"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, parent_id, ordinal, display_title) VALUES
			($1::uuid, 'movie', NULL, NULL, ''),
			($2::uuid, 'series', NULL, NULL, 'Series snapshot'),
			($3::uuid, 'season', $2::uuid, 1, 'Season snapshot'),
			($4::uuid, 'episode', $3::uuid, 1, 'Episode snapshot');
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($1::uuid, 'tmdb', 'movie', '10'),
			($2::uuid, 'tmdb', 'series', '20'),
			($3::uuid, 'tmdb', 'season', '21'),
			($4::uuid, 'tmdb', 'episode', '22'),
			($4::uuid, 'tvdb', 'episode', 'stale-tvdb-id')
	`, pgx.QueryExecModeSimpleProtocol, movieID, seriesID, seasonID, episodeID); err != nil {
		t.Fatalf("seed refresh hierarchy: %v", err)
	}
	provider := &refreshTestProvider{
		movies: map[string]ProviderMovie{
			"10": {ExternalID: "10", Title: "Canonical Movie", Overview: "Movie overview", Cast: []CastMember{}, AdditionalIDs: map[string]string{}},
		},
		series: map[string]ProviderSeries{
			"20": {
				ExternalID: "20", Name: "Canonical Series", Overview: "Series overview", Cast: []CastMember{}, AdditionalIDs: map[string]string{},
				Seasons: []ProviderSeasonSummary{{ExternalID: "21", Name: "Canonical Season", Overview: "Season summary", SeasonNumber: 1, EpisodeCount: 1}},
			},
		},
		seasons: map[string]ProviderSeason{
			"season:20:1": {
				ExternalID: "21", Name: "Canonical Season", Overview: "Season overview", SeasonNumber: 1,
				Episodes: []ProviderEpisode{{ExternalID: "22", Name: "Canonical Episode", Overview: "Episode overview", SeasonNumber: 1, EpisodeNumber: 1, AdditionalIDs: map[string]string{"tvdb": "current-tvdb-id"}}},
			},
		},
	}
	service := NewService(pool, provider, nil, nil, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	result, err := service.RefreshMissing(ctx, RefreshMissingOptions{Language: "fr-FR", BatchSize: 20})
	if err != nil {
		t.Fatalf("refresh canonical hierarchy: %v", err)
	}
	if result.Candidates != 4 || result.Refreshed != 4 || result.Failed != 0 || len(result.FailedTitles) != 0 {
		t.Fatalf("unexpected hierarchy refresh result: %+v", result)
	}
	if provider.calls["movie:10"] != 1 || provider.calls["series:20"] != 1 || provider.calls["season:20:1"] != 1 {
		t.Fatalf("child refresh bypassed canonical pipelines: calls=%v", provider.calls)
	}

	var cacheCount, episodeCacheCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM title_metadata WHERE language = 'fr-FR'`).Scan(&cacheCount); err != nil {
		t.Fatalf("count persisted metadata: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM title_metadata WHERE title_id = $1::uuid`, episodeID).Scan(&episodeCacheCount); err != nil {
		t.Fatalf("count episode metadata: %v", err)
	}
	if cacheCount != 3 || episodeCacheCount != 0 {
		t.Fatalf("metadata cache was not canonical: total=%d episode=%d", cacheCount, episodeCacheCount)
	}
	var movieTitle, seriesTitle, seasonTitle, episodeTitle string
	if err := pool.QueryRow(ctx, `
		SELECT movie.display_title, series.display_title, season.display_title, episode.display_title
		FROM titles AS movie, titles AS series, titles AS season, titles AS episode
		WHERE movie.id = $1::uuid AND series.id = $2::uuid AND season.id = $3::uuid AND episode.id = $4::uuid
	`, movieID, seriesID, seasonID, episodeID).Scan(&movieTitle, &seriesTitle, &seasonTitle, &episodeTitle); err != nil {
		t.Fatalf("load refreshed snapshots: %v", err)
	}
	if movieTitle != "Canonical Movie" || seriesTitle != "Canonical Series" || seasonTitle != "Canonical Season" || episodeTitle != "Canonical Episode" {
		t.Fatalf("canonical snapshots were not persisted: %q %q %q %q", movieTitle, seriesTitle, seasonTitle, episodeTitle)
	}
	var episodeTVDBID string
	if err := pool.QueryRow(ctx, `
		SELECT external_id
		FROM title_external_ids
		WHERE title_id = $1::uuid AND provider = 'tvdb' AND namespace = 'episode'
	`, episodeID).Scan(&episodeTVDBID); err != nil {
		t.Fatalf("load repaired episode TVDB identity: %v", err)
	}
	if episodeTVDBID != "current-tvdb-id" {
		t.Fatalf("stale episode TVDB identity was not repaired: %q", episodeTVDBID)
	}

	second, err := service.RefreshMissing(ctx, RefreshMissingOptions{Language: "fr-FR", BatchSize: 20})
	if err != nil {
		t.Fatalf("repeat canonical refresh: %v", err)
	}
	if second.Candidates != 0 || second.Refreshed != 0 || second.Failed != 0 {
		t.Fatalf("fresh season payload did not satisfy episode metadata: %+v", second)
	}
}

func TestRefreshMissingRetriesTemporaryFailureOnceAndPreservesMixedSuccesses(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	ctx := context.Background()
	const (
		temporaryID = "50000000-0000-4000-8000-000000000005"
		permanentID = "60000000-0000-4000-8000-000000000006"
		successID   = "70000000-0000-4000-8000-000000000007"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, display_title) VALUES
			($1::uuid, 'movie', 'Temporary Movie'),
			($2::uuid, 'movie', 'Permanent Movie'),
			($3::uuid, 'movie', 'Successful Movie');
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($1::uuid, 'tmdb', 'movie', '31'),
			($2::uuid, 'tmdb', 'movie', '32'),
			($3::uuid, 'tmdb', 'movie', '33')
	`, pgx.QueryExecModeSimpleProtocol, temporaryID, permanentID, successID); err != nil {
		t.Fatalf("seed mixed refresh: %v", err)
	}
	provider := &refreshTestProvider{
		movies: map[string]ProviderMovie{
			"31": {ExternalID: "31", Title: "Temporary Movie Refreshed", Cast: []CastMember{}},
			"33": {ExternalID: "33", Title: "Successful Movie Refreshed", Cast: []CastMember{}},
		},
		failures: map[string][]error{
			"movie:31": {NewProviderError(ErrProviderFailure, errors.New("TMDB returned HTTP 503"), 503, "/movie/31?append_to_response=credits&language=en-US")},
			"movie:32": {NewProviderError(ErrProviderNotFound, errors.New("TMDB returned HTTP 404"), 404, "/movie/32?append_to_response=credits&language=en-US")},
		},
	}
	var logs bytes.Buffer
	service := NewService(pool, provider, nil, nil, time.Hour, slog.New(slog.NewJSONHandler(&logs, nil)))
	result, err := service.RefreshMissing(ctx, RefreshMissingOptions{Language: "en-US", BatchSize: 10})
	if err != nil {
		t.Fatalf("refresh mixed candidates: %v", err)
	}
	if result.Candidates != 3 || result.Refreshed != 2 || result.Failed != 1 || !reflect.DeepEqual(result.FailedTitles, []string{"Permanent Movie"}) {
		t.Fatalf("unexpected mixed result: %+v", result)
	}
	if provider.calls["movie:31"] != 2 || provider.calls["movie:32"] != 1 || provider.calls["movie:33"] != 1 {
		t.Fatalf("retry classification was not enforced: calls=%v", provider.calls)
	}
	logOutput := logs.String()
	for _, expected := range []string{
		`"titleId":"` + permanentID + `"`,
		`"title":"Permanent Movie"`,
		`"mediaType":"movie"`,
		`"provider":"tmdb"`,
		`"requestedResource":"/movie/32?append_to_response=credits\u0026language=en-US"`,
		`"error":"title not found: provider title not found: TMDB returned HTTP 404"`,
		`"temporary":false`,
		`"attempts":1`,
		`"httpStatus":404`,
	} {
		if !strings.Contains(logOutput, expected) {
			t.Fatalf("structured failure log missing %s: %s", expected, logOutput)
		}
	}
	var cached int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM title_metadata WHERE language = 'en-US'`).Scan(&cached); err != nil {
		t.Fatalf("count mixed refresh cache: %v", err)
	}
	if cached != 2 {
		t.Fatalf("successful candidates were not preserved in cache: %d", cached)
	}
}

func TestRefreshMissingSkipsTVDBOnlyRootButResolvesIMDbRoot(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	ctx := context.Background()
	const (
		tvdbOnlyID = "80000000-0000-4000-8000-000000000008"
		imdbOnlyID = "90000000-0000-4000-8000-000000000009"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, display_title) VALUES
			($1::uuid, 'series', 'TVDB-only title'),
			($2::uuid, 'movie', 'IMDb title');
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($1::uuid, 'tvdb', 'series', '123456'),
			($2::uuid, 'imdb', 'movie', 'tt0000001')
	`, pgx.QueryExecModeSimpleProtocol, tvdbOnlyID, imdbOnlyID); err != nil {
		t.Fatalf("seed external identities: %v", err)
	}
	resolveKey := "resolve:movie:imdb:tt0000001"
	provider := &refreshTestProvider{
		movies: map[string]ProviderMovie{
			"41": {ExternalID: "41", Title: "IMDb title refreshed", Cast: []CastMember{}, AdditionalIDs: map[string]string{"imdb": "tt0000001"}},
		},
		resolutions: map[string]string{resolveKey: "41"},
	}
	service := NewService(pool, provider, nil, nil, time.Hour, nil)
	result, err := service.RefreshMissing(ctx, RefreshMissingOptions{Language: "en-US", BatchSize: 10})
	if err != nil {
		t.Fatalf("refresh provider-compatible roots: %v", err)
	}
	if result.Candidates != 1 || result.Refreshed != 1 || result.Failed != 0 {
		t.Fatalf("unexpected provider-compatible selection: %+v", result)
	}
	if provider.calls[resolveKey] != 1 || provider.calls["resolve:series:tvdb:123456"] != 0 {
		t.Fatalf("incompatible TVDB identity reached TMDB resolver: calls=%v", provider.calls)
	}
}

func TestProviderFailureClassificationIsTemporaryOnlyForRetryableCauses(t *testing.T) {
	tests := []struct {
		name      string
		error     error
		temporary bool
	}{
		{name: "context timeout", error: NewProviderError(ErrProviderFailure, context.DeadlineExceeded, 0, "/movie/1"), temporary: true},
		{name: "connection failure", error: NewProviderError(ErrProviderFailure, &net.DNSError{Err: "connection refused", Name: "api.example"}, 0, "/movie/1"), temporary: true},
		{name: "rate limit", error: NewProviderError(ErrProviderRateLimited, nil, 429, "/movie/1"), temporary: true},
		{name: "server failure", error: NewProviderError(ErrProviderFailure, nil, 503, "/movie/1"), temporary: true},
		{name: "not found", error: NewProviderError(ErrProviderNotFound, nil, 404, "/movie/1"), temporary: false},
		{name: "parse failure", error: NewProviderError(ErrProviderFailure, errors.New("decode response"), 200, "/movie/1"), temporary: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, temporary, resource := ProviderErrorDetails(test.error)
			if temporary != test.temporary || resource != "/movie/1" {
				t.Fatalf("unexpected classification status=%d temporary=%t resource=%q", status, temporary, resource)
			}
		})
	}
}

func TestFailedTitlesAreBoundedAndDeduplicated(t *testing.T) {
	var result RefreshResult
	seen := make(map[string]struct{})
	appendFailedTitle(&result, seen, "  Duplicate   Title ")
	appendFailedTitle(&result, seen, "duplicate title")
	for index := range 20 {
		appendFailedTitle(&result, seen, string(rune('A'+index))+strings.Repeat("x", 130))
	}
	if len(result.FailedTitles) != maximumFailedTitles {
		t.Fatalf("failed title list was not bounded: %d", len(result.FailedTitles))
	}
	if result.FailedTitles[0] != "Duplicate Title" {
		t.Fatalf("failed titles were not normalized and deduplicated: %v", result.FailedTitles)
	}
	for _, title := range result.FailedTitles {
		if len([]rune(title)) > maximumFailedTitleRunes {
			t.Fatalf("failed title exceeded safe bound: %q", title)
		}
	}
}
