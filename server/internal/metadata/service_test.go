package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

func TestNormalizeQueryOptionsDefaultsAndCanonicalizes(t *testing.T) {
	options, err := normalizeQueryOptions(QueryOptions{Language: "FR-fr", Region: "fr"})
	if err != nil {
		t.Fatalf("normalize options: %v", err)
	}
	if options.Page != 1 || options.Language != "fr-FR" || options.Region != "FR" {
		t.Fatalf("unexpected normalized options: %+v", options)
	}
}

func TestNormalizeQueryOptionsRejectsUnsafeValues(t *testing.T) {
	tests := []QueryOptions{
		{Page: -1},
		{Page: 501},
		{Language: "fr_FR"},
		{Region: "FRA"},
	}
	for _, options := range tests {
		if _, err := normalizeQueryOptions(options); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid input for %+v, got %v", options, err)
		}
	}
}

func TestRequireActiveProfileRejectsMissingAndExpiredGrants(t *testing.T) {
	profileID := "profile-id"
	expired := time.Now().UTC().Add(-time.Minute)
	if err := requireActiveProfile(auth.Principal{}); !errors.Is(err, ErrProfileRequired) {
		t.Fatalf("expected missing profile rejection, got %v", err)
	}
	if err := requireActiveProfile(auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expired}); !errors.Is(err, ErrProfileRequired) {
		t.Fatalf("expected expired grant rejection, got %v", err)
	}
	active := time.Now().UTC().Add(time.Minute)
	if err := requireActiveProfile(auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &active}); err != nil {
		t.Fatalf("expected active grant, got %v", err)
	}
}

type fakeTrailerProvider struct {
	responses       map[string][]ProviderTrailer
	errors          map[string]error
	responsesByCall map[string][]ProviderTrailer
	errorsByCall    map[string]error
	calls           []string
}

func (f *fakeTrailerProvider) Trailers(_ context.Context, mediaType, externalID, language string, seasonNumber *int) ([]ProviderTrailer, error) {
	call := mediaType + ":" + externalID + ":" + language
	if seasonNumber != nil {
		call += ":" + strconv.Itoa(*seasonNumber)
	}
	f.calls = append(f.calls, call)
	if err, ok := f.errorsByCall[call]; ok {
		return nil, err
	}
	if response, ok := f.responsesByCall[call]; ok {
		return response, nil
	}
	return f.responses[language], f.errors[language]
}

func TestTrailersUsesSeriesIdentityForSeasonAndRejectsMovieSeason(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	ctx := context.Background()
	const (
		seriesID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		movieID  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, display_title)
		VALUES ($1::uuid, 'series', 'Series'), ($2::uuid, 'movie', 'Movie');
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id)
		VALUES ($1::uuid, 'tmdb', 'series', '1396'), ($2::uuid, 'tmdb', 'movie', '550')
	`, seriesID, movieID); err != nil {
		t.Fatalf("seed trailer titles: %v", err)
	}
	provider := &fakeTrailerProvider{responsesByCall: map[string][]ProviderTrailer{
		"series:1396:en-US:3": {{YouTubeID: "season-video", Name: "Season 3 Trailer", Site: "YouTube", Type: "Trailer"}},
		"movie:550:en-US":     {{YouTubeID: "movie-video", Site: "YouTube", Type: "Trailer"}},
	}}
	service := &Service{pool: pool, trailerProvider: provider}
	seasonNumber := 3
	result, err := service.Trailers(ctx, canonicalMergePrincipal(), seriesID, "en-US", "", &seasonNumber)
	if err != nil || len(result.Trailers) != 1 || result.Trailers[0].YouTubeID != "season-video" {
		t.Fatalf("season trailers=%+v err=%v", result, err)
	}
	if len(provider.calls) < 1 || provider.calls[0] != "series:1396:en-US:3" {
		t.Fatalf("unexpected season provider calls: %+v", provider.calls)
	}

	callCount := len(provider.calls)
	_, err = service.Trailers(ctx, canonicalMergePrincipal(), movieID, "en-US", "", &seasonNumber)
	if !errors.Is(err, ErrInvalidInput) || len(provider.calls) != callCount {
		t.Fatalf("movie season must be rejected before provider call, err=%v calls=%+v", err, provider.calls)
	}

	titleTrailers, err := service.Trailers(ctx, canonicalMergePrincipal(), movieID, "en-US", "", nil)
	if err != nil || len(titleTrailers.Trailers) != 1 || titleTrailers.Trailers[0].YouTubeID != "movie-video" {
		t.Fatalf("title trailers=%+v err=%v", titleTrailers, err)
	}
}

func TestTrailersRejectsNegativeSeasonNumber(t *testing.T) {
	service := &Service{}
	seasonNumber := -1
	_, err := service.Trailers(context.Background(), canonicalMergePrincipal(), "title-id", "en-US", "", &seasonNumber)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid season input, got %v", err)
	}
}

func TestTrailersRejectInvalidCaptionLanguage(t *testing.T) {
	service := &Service{}
	_, err := service.Trailers(context.Background(), canonicalMergePrincipal(), "title-id", "en-US", "not a language", nil)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid caption language, got %v", err)
	}
}

func TestChooseTrailersPrioritizesPreferredLanguageAndKeepsEnglishOption(t *testing.T) {
	provider := &fakeTrailerProvider{responses: map[string][]ProviderTrailer{
		"fr-FR": {
			{YouTubeID: "vimeo", Name: "Unsupported", Site: "Vimeo", Type: "Trailer", Official: true},
			{YouTubeID: "clip", Name: "Unsupported", Site: "YouTube", Type: "Clip", Official: true},
			{YouTubeID: "teaser", Name: "Localized Teaser", Site: "YouTube", Type: "Teaser", Official: true, PublishedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
			{YouTubeID: "unofficial", Name: "Localized Trailer", Site: "YouTube", Type: "Trailer", PublishedAt: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)},
			{YouTubeID: "official-old", Name: "Old Localized Trailer", Site: "YouTube", Type: "Trailer", Official: true, PublishedAt: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)},
			{YouTubeID: "official-mid", Name: "Localized Trailer 2", Site: "YouTube", Type: "Trailer", Official: true, PublishedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
			{YouTubeID: "official-new", Name: "Localized Trailer 3", Site: "YouTube", Type: "Trailer", Official: true, PublishedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		},
		"en-US": {
			{YouTubeID: "english-new", Name: "English Trailer", Site: "YouTube", Type: "Trailer", Official: true, PublishedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
			{YouTubeID: "english-old", Name: "English Trailer 2", Site: "YouTube", Type: "Trailer", Official: true, PublishedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		},
	}}
	result, err := chooseTrailers(context.Background(), provider, MediaTypeMovie, "550", "fr-FR", "fr-FR", nil)
	if err != nil {
		t.Fatalf("choose trailers: %v", err)
	}
	if len(result.Trailers) != maxTrailerOptions {
		t.Fatalf("trailer count=%d, want %d: %+v", len(result.Trailers), maxTrailerOptions, result)
	}
	want := []string{"official-new", "official-mid", "official-old", "unofficial", "english-new"}
	for index, youtubeID := range want {
		if result.Trailers[index].YouTubeID != youtubeID {
			t.Fatalf("trailer %d=%q, want %q: %+v", index, result.Trailers[index].YouTubeID, youtubeID, result)
		}
	}
	fallback := result.Trailers[4]
	if fallback.Language != "en-US" || !fallback.IsFallback || fallback.CaptionPreference != "fr" {
		t.Fatalf("unexpected English fallback: %+v", fallback)
	}
	if len(provider.calls) != 2 || provider.calls[0] != "movie:550:fr-FR" || provider.calls[1] != "movie:550:en-US" {
		t.Fatalf("unexpected provider calls: %+v", provider.calls)
	}
}

func TestChooseTrailersDeduplicatesVideosAcrossLanguages(t *testing.T) {
	provider := &fakeTrailerProvider{responses: map[string][]ProviderTrailer{
		"fr-FR": {
			{YouTubeID: "shared", Name: "Shared Localized Trailer", Site: "YouTube", Type: "Trailer", Official: true},
			{YouTubeID: "localized", Name: "Localized Trailer", Site: "YouTube", Type: "Trailer", Official: true},
		},
		"en-US": {
			{YouTubeID: "shared", Name: "Shared English Trailer", Site: "YouTube", Type: "Trailer", Official: true},
			{YouTubeID: "english", Name: "English Trailer", Site: "YouTube", Type: "Trailer", Official: true},
		},
	}}
	result, err := chooseTrailers(context.Background(), provider, MediaTypeMovie, "550", "fr-FR", "fr-FR", nil)
	if err != nil || len(result.Trailers) != 3 {
		t.Fatalf("deduplicated trailers=%+v err=%v", result, err)
	}
	seen := map[string]bool{}
	for _, trailer := range result.Trailers {
		if seen[trailer.YouTubeID] {
			t.Fatalf("duplicate trailer returned: %+v", result)
		}
		seen[trailer.YouTubeID] = true
	}
	if !seen["shared"] || !seen["localized"] || !seen["english"] {
		t.Fatalf("missing curated trailer: %+v", result)
	}
}

func TestChooseTrailersDoesNotLeakAnotherSeasonFromRequestedBucket(t *testing.T) {
	provider := &fakeTrailerProvider{responsesByCall: map[string][]ProviderTrailer{
		"series:127532:en-US:1": {
			{YouTubeID: "season-two", Name: "Season 2 Hype Trailer", Site: "YouTube", Type: "Trailer", Official: true, PublishedAt: time.Date(2025, 3, 22, 0, 0, 0, 0, time.UTC)},
		},
		"series:127532:en-US": {
			{YouTubeID: "season-one-a", Name: "Official Trailer 4", Site: "YouTube", Type: "Trailer", Official: true, PublishedAt: time.Date(2023, 12, 11, 0, 0, 0, 0, time.UTC)},
			{YouTubeID: "season-one-b", Name: "Official Trailer 3", Site: "YouTube", Type: "Trailer", Official: true, PublishedAt: time.Date(2023, 9, 10, 0, 0, 0, 0, time.UTC)},
		},
	}}
	seasonNumber := 1
	result, err := chooseTrailers(context.Background(), provider, MediaTypeSeries, "127532", "en-US", "", &seasonNumber)
	if err != nil || len(result.Trailers) != 2 {
		t.Fatalf("season one trailers=%+v err=%v", result, err)
	}
	for _, trailer := range result.Trailers {
		if trailer.YouTubeID == "season-two" {
			t.Fatalf("season two trailer leaked into season one: %+v", result)
		}
	}
}

func TestChooseTrailersRecoversAllExplicitSeasonVideosFromCollapsedFirstSeason(t *testing.T) {
	provider := &fakeTrailerProvider{
		responsesByCall: map[string][]ProviderTrailer{
			"series:127532:en-US": {
				{YouTubeID: "generic", Name: "Official Trailer 4", Site: "YouTube", Type: "Trailer", Official: true},
			},
			"series:127532:en-US:1": {
				{YouTubeID: "season-three", Name: "Season 3 Trailer", Site: "YouTube", Type: "Trailer", Official: true, PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
				{YouTubeID: "season-two-new", Name: "Season 2 Hype Trailer", Site: "YouTube", Type: "Trailer", Official: true, PublishedAt: time.Date(2025, 3, 22, 0, 0, 0, 0, time.UTC)},
				{YouTubeID: "season-two-old", Name: "Season 2 Official Trailer", Site: "YouTube", Type: "Trailer", Official: true, PublishedAt: time.Date(2024, 12, 21, 0, 0, 0, 0, time.UTC)},
			},
		},
		errorsByCall: map[string]error{"series:127532:en-US:2": ErrProviderNotFound},
	}
	seasonNumber := 2
	result, err := chooseTrailers(context.Background(), provider, MediaTypeSeries, "127532", "en-US", "", &seasonNumber)
	if err != nil || len(result.Trailers) != 2 {
		t.Fatalf("season two trailers=%+v err=%v", result, err)
	}
	if result.Trailers[0].YouTubeID != "season-two-new" || result.Trailers[1].YouTubeID != "season-two-old" {
		t.Fatalf("unexpected season two ranking: %+v", result)
	}
}

func TestChooseTrailersPreservesSeasonAcrossLocalizedAndEnglishRequests(t *testing.T) {
	provider := &fakeTrailerProvider{responsesByCall: map[string][]ProviderTrailer{
		"series:1396:en-US:3": {{YouTubeID: "season-three", Name: "Season 3 Trailer", Site: "YouTube", Type: "Trailer"}},
	}}
	seasonNumber := 3
	result, err := chooseTrailers(context.Background(), provider, MediaTypeSeries, "1396", "fr-FR", "fr-FR", &seasonNumber)
	if err != nil || len(result.Trailers) != 1 || result.Trailers[0].YouTubeID != "season-three" {
		t.Fatalf("season fallback trailers=%+v err=%v", result, err)
	}
	if !result.Trailers[0].IsFallback || result.Trailers[0].CaptionPreference != "fr" {
		t.Fatalf("unexpected localized fallback metadata: %+v", result.Trailers[0])
	}
}

func TestTrailerSeasonReferenceRecognizesCommonLabels(t *testing.T) {
	tests := map[string]int{
		"Season 2 Trailer":     2,
		"Saison 03 Teaser":     3,
		"S04 Official Trailer": 4,
		"5th Season Trailer":   5,
		"第6期 Trailer":          6,
	}
	for name, want := range tests {
		got, ok := trailerSeasonReference(name)
		if !ok || got != want {
			t.Fatalf("season reference %q = %d, %v; want %d, true", name, got, ok, want)
		}
	}
}

func TestChooseTrailersNonFrenchFallbackDoesNotRequestCaptions(t *testing.T) {
	provider := &fakeTrailerProvider{responses: map[string][]ProviderTrailer{
		"en-US": {{YouTubeID: "english", Site: "YouTube", Type: "Trailer"}},
	}}
	result, err := chooseTrailers(context.Background(), provider, MediaTypeMovie, "550", "de-DE", "", nil)
	if err != nil || len(result.Trailers) != 1 {
		t.Fatalf("fallback trailers=%+v err=%v", result, err)
	}
	if !result.Trailers[0].IsFallback || result.Trailers[0].CaptionPreference != "" {
		t.Fatalf("unexpected non-French fallback: %+v", result.Trailers[0])
	}
}

func TestChooseTrailersReturnsNotFoundWhenNoEligibleVideoExists(t *testing.T) {
	provider := &fakeTrailerProvider{responses: map[string][]ProviderTrailer{
		"fr-FR": {{YouTubeID: "clip", Site: "YouTube", Type: "Clip"}},
		"en-US": {{YouTubeID: "vimeo", Site: "Vimeo", Type: "Trailer"}},
	}}
	_, err := chooseTrailers(context.Background(), provider, MediaTypeMovie, "550", "fr-FR", "", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestChooseTrailersPropagatesProviderErrors(t *testing.T) {
	provider := &fakeTrailerProvider{
		responses: map[string][]ProviderTrailer{},
		errors:    map[string]error{"fr-FR": ErrProviderRateLimited},
	}
	_, err := chooseTrailers(context.Background(), provider, MediaTypeMovie, "550", "fr-FR", "", nil)
	if !errors.Is(err, ErrProviderRateLimited) {
		t.Fatalf("expected provider error, got %v", err)
	}
	if len(provider.calls) != 1 {
		t.Fatalf("provider error must not trigger fallback: %+v", provider.calls)
	}
}

func TestCachedSeriesMetadataBackfillsCalendarReleaseDates(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	ctx := context.Background()
	const (
		seriesID  = "11111111-1111-4111-8111-111111111111"
		seasonID  = "22222222-2222-4222-8222-222222222222"
		episodeID = "33333333-3333-4333-8333-333333333333"
	)
	series := Series{
		ID:           seriesID,
		MediaType:    MediaTypeSeries,
		Name:         "Futurama",
		FirstAirDate: "1999-03-28",
		Seasons: []SeasonSummary{{
			ID:           seasonID,
			MediaType:    MediaTypeSeason,
			SeriesID:     seriesID,
			Name:         "Season 11",
			SeasonNumber: 11,
			EpisodeCount: 1,
			AirDate:      "2026-08-03",
		}},
	}
	season := Season{
		ID:           seasonID,
		MediaType:    MediaTypeSeason,
		SeriesID:     seriesID,
		Name:         "Season 11",
		SeasonNumber: 11,
		AirDate:      "2026-08-03",
		Episodes: []Episode{{
			ID:            episodeID,
			MediaType:     MediaTypeEpisode,
			SeasonID:      seasonID,
			Name:          "Episode 1",
			SeasonNumber:  11,
			EpisodeNumber: 1,
			AirDate:       "2026-08-03",
		}},
	}
	seriesPayload, err := json.Marshal(series)
	if err != nil {
		t.Fatalf("encode cached series: %v", err)
	}
	seasonPayload, err := json.Marshal(season)
	if err != nil {
		t.Fatalf("encode cached season: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, parent_id, ordinal, display_title) VALUES
			($1::uuid, 'series', NULL, NULL, 'Futurama'),
			($2::uuid, 'season', $1::uuid, 11, 'Season 11'),
			($3::uuid, 'episode', $2::uuid, 1, 'Episode 1')
	`, seriesID, seasonID, episodeID); err != nil {
		t.Fatalf("seed cached titles: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($1::uuid, 'tmdb', 'series', '615'),
			($2::uuid, 'tmdb', 'season', '516338'),
			($3::uuid, 'tmdb', 'episode', 'episode-1')
	`, seriesID, seasonID, episodeID); err != nil {
		t.Fatalf("seed cached external IDs: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at) VALUES
			($1::uuid, 'tmdb', 'fr-FR', $3::jsonb, now() + interval '1 hour'),
			($2::uuid, 'tmdb', 'fr-FR', $4::jsonb, now() + interval '1 hour')
	`, seriesID, seasonID, seriesPayload, seasonPayload); err != nil {
		t.Fatalf("seed cached metadata: %v", err)
	}

	profileID := "44444444-4444-4444-8444-444444444444"
	expiresAt := time.Now().UTC().Add(time.Hour)
	principal := auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt}
	service := &Service{pool: pool}
	if _, err := service.SeriesDetails(ctx, principal, seriesID, "fr-FR", "tmdb"); err != nil {
		t.Fatalf("load cached series details: %v", err)
	}
	if _, err := service.SeasonDetails(ctx, principal, seasonID, "fr-FR", "tmdb"); err != nil {
		t.Fatalf("load cached season details: %v", err)
	}

	var seriesDate, seasonDate, episodeDate string
	if err := pool.QueryRow(ctx, `
		SELECT series.release_date::text, season.release_date::text, episode.release_date::text
		FROM titles AS series
		JOIN titles AS season ON season.parent_id = series.id
		JOIN titles AS episode ON episode.parent_id = season.id
		WHERE series.id = $1::uuid
	`, seriesID).Scan(&seriesDate, &seasonDate, &episodeDate); err != nil {
		t.Fatalf("query backfilled release dates: %v", err)
	}
	if seriesDate != "1999-03-28" || seasonDate != "2026-08-03" || episodeDate != "2026-08-03" {
		t.Fatalf("unexpected backfilled dates: series=%q season=%q episode=%q", seriesDate, seasonDate, episodeDate)
	}
}

func TestMatchMappedEpisodesRegroupsCanonicalEpisodesByTVDBAirDate(t *testing.T) {
	canonical := []Episode{
		{ID: "tmdb-episode-1", Name: "I'm Used to It", SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2024-01-07", ExternalIDs: map[string]string{"tmdb": "501"}},
		{ID: "tmdb-episode-13", Name: "You Aren't E-Rank, Are You?", SeasonNumber: 1, EpisodeNumber: 13, AirDate: "2025-01-05", ExternalIDs: map[string]string{"tmdb": "513"}},
	}
	provided := []ProviderEpisode{
		{ExternalID: "1201", Name: "You Aren't E-Rank, Are You?", SeasonNumber: 2, EpisodeNumber: 1, AirDate: "2025-01-05"},
	}
	episodes, links, err := matchMappedEpisodes("tvdb:series:1002", provided, canonical)
	if err != nil {
		t.Fatalf("match TVDB hierarchy: %v", err)
	}
	if len(episodes) != 1 || episodes[0].ID != "tmdb-episode-13" || episodes[0].SeasonNumber != 2 || episodes[0].EpisodeNumber != 1 ||
		episodes[0].ExternalIDs["tmdb"] != "513" || episodes[0].ExternalIDs["tvdb"] != "1201" || links["tmdb-episode-13"] != "1201" {
		t.Fatalf("unexpected mapped episode: episodes=%+v links=%+v", episodes, links)
	}
}

func TestMatchMappedEpisodesRejectsUnmatchedTVDBEpisode(t *testing.T) {
	_, _, err := matchMappedEpisodes(
		"tvdb:series:1002",
		[]ProviderEpisode{{ExternalID: "1201", Name: "Unknown", SeasonNumber: 2, EpisodeNumber: 1, AirDate: "2025-01-05"}},
		[]Episode{{ID: "tmdb-episode", Name: "Different", SeasonNumber: 1, EpisodeNumber: 3, AirDate: "2024-01-21", ExternalIDs: map[string]string{"tmdb": "503"}}},
	)
	if !errors.Is(err, ErrProviderFailure) {
		t.Fatalf("expected provider failure for unmatched episode, got %v", err)
	}
}
