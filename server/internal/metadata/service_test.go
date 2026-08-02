package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

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

func TestRefreshMissingSkipsTitlesWithoutResolvableTMDBIdentity(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	const titleID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO titles (id, media_type, display_title)
		VALUES ($1::uuid, 'movie', 'Addon-only title');
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id)
		VALUES ($1::uuid, 'addon', 'movie', 'demo:flower')
	`, pgx.QueryExecModeSimpleProtocol, titleID); err != nil {
		t.Fatalf("seed addon-only title: %v", err)
	}

	service := &Service{pool: pool}
	result, err := service.RefreshMissing(context.Background(), RefreshMissingOptions{Language: "fr-FR", BatchSize: 1})
	if err != nil {
		t.Fatalf("refresh missing metadata: %v", err)
	}
	if result != (RefreshResult{}) {
		t.Fatalf("addon-only title became an unrefreshable candidate: %+v", result)
	}
}

func TestSeasonHierarchyValidationRejectsDemainNousAppartientSeasonMismatch(t *testing.T) {
	mismatched := ProviderSeason{
		ExternalID:   "475463",
		Name:         "Saison 9",
		SeasonNumber: 2,
		Episodes: []ProviderEpisode{{
			ExternalID: "500001", Name: "Épisode 2021", SeasonNumber: 2, EpisodeNumber: 2021,
		}},
	}
	if err := validateProviderSeasonHierarchy(mismatched, "475463", 9); !errors.Is(err, ErrProviderFailure) {
		t.Fatalf("expected provider failure for season 9 response numbered as season 2, got %v", err)
	}
	poisonedCache := Season{
		ID: "season-id", SeriesID: "series-id", SeasonNumber: 2,
		Episodes: []Episode{{SeasonID: "season-id", SeasonNumber: 2, EpisodeNumber: 2021}},
	}
	if cachedSeasonMatchesHierarchy(poisonedCache, "season-id", "series-id", 9) {
		t.Fatal("poisoned season-2 cache matched canonical season 9")
	}
}

func TestSeasonHierarchyValidationPreservesNormalSeason(t *testing.T) {
	season := ProviderSeason{
		ExternalID: "475463", Name: "Saison 9", SeasonNumber: 9,
		Episodes: []ProviderEpisode{
			{ExternalID: "500001", Name: "Épisode 2021", SeasonNumber: 9, EpisodeNumber: 2021},
			{ExternalID: "500002", Name: "Épisode 2022", SeasonNumber: 9, EpisodeNumber: 2022},
		},
	}
	if err := validateProviderSeasonHierarchy(season, "475463", 9); err != nil {
		t.Fatalf("valid season hierarchy rejected: %v", err)
	}
	cached := Season{
		ID: "season-id", SeriesID: "series-id", SeasonNumber: 9,
		Episodes: []Episode{
			{SeasonID: "season-id", SeasonNumber: 9, EpisodeNumber: 2021},
			{SeasonID: "season-id", SeasonNumber: 9, EpisodeNumber: 2022},
		},
	}
	if !cachedSeasonMatchesHierarchy(cached, "season-id", "series-id", 9) {
		t.Fatal("valid season 9 cache was rejected")
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
		VALUES ($1::uuid, 'series', 'Series'), ($2::uuid, 'movie', 'Movie')
	`, seriesID, movieID); err != nil {
		t.Fatalf("seed trailer titles: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id)
		VALUES ($1::uuid, 'tmdb', 'series', '1396'), ($2::uuid, 'tmdb', 'movie', '550')
	`, seriesID, movieID); err != nil {
		t.Fatalf("seed trailer external IDs: %v", err)
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

func TestCachedSeriesMetadataBackfillsCalendarDatesAndSeasonSnapshots(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	ctx := context.Background()
	const (
		seriesID       = "11111111-1111-4111-8111-111111111111"
		seasonID       = "22222222-2222-4222-8222-222222222222"
		episodeID      = "5810d584-af52-4ba3-8cef-17a98bc19f77"
		seriesPoster   = "https://assets.fanart.tv/futurama-poster.jpg"
		seriesBackdrop = "https://assets.fanart.tv/futurama-background.jpg"
		seasonPoster   = "https://image.tmdb.org/t/p/w500/futurama-season-5.jpg"
		episodeStill   = "https://image.tmdb.org/t/p/w500/asteroique.jpg"
	)
	series := Series{
		ID:           seriesID,
		MediaType:    MediaTypeSeries,
		Name:         "Futurama",
		FirstAirDate: "1999-03-28",
		PosterURL:    seriesPoster,
		BackdropURL:  seriesBackdrop,
		Seasons: []SeasonSummary{{
			ID:           seasonID,
			MediaType:    MediaTypeSeason,
			SeriesID:     seriesID,
			Name:         "Saison 5",
			SeasonNumber: 5,
			EpisodeCount: 1,
			AirDate:      "2002-02-10",
			PosterURL:    seasonPoster,
		}},
	}
	season := Season{
		ID:           seasonID,
		MediaType:    MediaTypeSeason,
		SeriesID:     seriesID,
		Name:         "Saison 5",
		SeasonNumber: 5,
		AirDate:      "2002-02-10",
		PosterURL:    seasonPoster,
		Episodes: []Episode{{
			ID:            episodeID,
			MediaType:     MediaTypeEpisode,
			SeasonID:      seasonID,
			Name:          "Astéroïque",
			SeasonNumber:  5,
			EpisodeNumber: 6,
			AirDate:       "2002-03-17",
			StillURL:      episodeStill,
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
		INSERT INTO titles (id, media_type, parent_id, ordinal, display_title, poster_url, background_url) VALUES
			($1::uuid, 'series', NULL, NULL, 'Stale Futurama', 'https://image.tmdb.org/stale-poster.jpg', 'https://image.tmdb.org/stale-background.jpg'),
			($2::uuid, 'season', $1::uuid, 5, NULL, NULL, NULL),
			($3::uuid, 'episode', $2::uuid, 6, '   ', NULL, NULL)
	`, seriesID, seasonID, episodeID); err != nil {
		t.Fatalf("seed cached titles: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($1::uuid, 'tmdb', 'series', '615'),
			($2::uuid, 'tmdb', 'season', '516338'),
			($3::uuid, 'tmdb', 'episode', '5810d584-af52-4ba3-8cef-17a98bc19f77')
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
	if _, err := service.SeasonDetails(ctx, principal, seasonID, "fr-FR", "tmdb"); err != nil {
		t.Fatalf("load cached season details: %v", err)
	}
	if _, err := service.SeriesDetails(ctx, principal, seriesID, SeriesDetailsOptions{Language: "fr-FR", MappingProvider: "tmdb"}); err != nil {
		t.Fatalf("load cached series details: %v", err)
	}

	var seriesTitle, seriesPosterURL, seriesBackgroundURL, seriesDate, seasonTitle, seasonPosterURL, seasonDate, episodeTitle, episodePosterURL, episodeDate string
	if err := pool.QueryRow(ctx, `
		SELECT series.display_title,
		       series.poster_url,
		       series.background_url,
		       series.release_date::text,
		       season.display_title,
		       season.poster_url,
		       season.release_date::text,
		       episode.display_title,
		       episode.poster_url,
		       episode.release_date::text
		FROM titles AS series
		JOIN titles AS season ON season.parent_id = series.id
		JOIN titles AS episode ON episode.parent_id = season.id
		WHERE series.id = $1::uuid
	`, seriesID).Scan(
		&seriesTitle,
		&seriesPosterURL,
		&seriesBackgroundURL,
		&seriesDate,
		&seasonTitle,
		&seasonPosterURL,
		&seasonDate,
		&episodeTitle,
		&episodePosterURL,
		&episodeDate,
	); err != nil {
		t.Fatalf("query backfilled title snapshots: %v", err)
	}
	if seriesDate != "1999-03-28" || seasonDate != "2002-02-10" || episodeDate != "2002-03-17" {
		t.Fatalf("unexpected backfilled dates: series=%q season=%q episode=%q", seriesDate, seasonDate, episodeDate)
	}
	if seriesTitle != "Futurama" || seriesPosterURL != seriesPoster || seriesBackgroundURL != seriesBackdrop {
		t.Fatalf("unexpected cached series snapshot: title=%q poster=%q background=%q", seriesTitle, seriesPosterURL, seriesBackgroundURL)
	}
	if seasonTitle != "Saison 5" || seasonPosterURL != seasonPoster {
		t.Fatalf("unexpected cached season snapshot: title=%q poster=%q", seasonTitle, seasonPosterURL)
	}
	if episodeTitle != "Astéroïque" || episodePosterURL != episodeStill {
		t.Fatalf("unexpected cached episode snapshot: title=%q still=%q", episodeTitle, episodePosterURL)
	}
}

func TestCachedMovieMetadataRestoresCanonicalSnapshot(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	ctx := context.Background()
	const (
		movieID         = "33333333-3333-4333-8333-333333333333"
		canonicalPoster = "https://assets.fanart.tv/movie-poster.jpg"
		canonicalArt    = "https://assets.fanart.tv/movie-background.jpg"
	)
	movie := Movie{
		ID:            movieID,
		MediaType:     MediaTypeMovie,
		Title:         "Canonical Movie",
		ReleaseDate:   "2025-04-18",
		PosterURL:     canonicalPoster,
		BackdropURL:   canonicalArt,
		ExternalIDs:   map[string]string{"tmdb": "123"},
		Genres:        []Genre{},
		VoteAverage:   8.1,
		VoteCount:     500,
		OriginalTitle: "Canonical Movie",
	}
	payload, err := json.Marshal(movie)
	if err != nil {
		t.Fatalf("encode cached movie: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, display_title, poster_url, background_url)
		VALUES ($1::uuid, 'movie', 'Stale Movie', 'https://image.tmdb.org/stale-poster.jpg', 'https://image.tmdb.org/stale-background.jpg');
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id)
		VALUES ($1::uuid, 'tmdb', 'movie', '123');
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at)
		VALUES ($1::uuid, 'tmdb', 'fr-FR', $2::jsonb, now() + interval '1 hour')
	`, pgx.QueryExecModeSimpleProtocol, movieID, string(payload)); err != nil {
		t.Fatalf("seed cached movie: %v", err)
	}

	profileID := "44444444-4444-4444-8444-444444444444"
	expiresAt := time.Now().UTC().Add(time.Hour)
	principal := auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt}
	service := &Service{pool: pool}
	if _, err := service.MovieDetails(ctx, principal, movieID, "fr-FR"); err != nil {
		t.Fatalf("load cached movie details: %v", err)
	}

	var title, posterURL, backgroundURL, releaseDate string
	if err := pool.QueryRow(ctx, `
		SELECT display_title, poster_url, background_url, release_date::text
		FROM titles
		WHERE id = $1::uuid
	`, movieID).Scan(&title, &posterURL, &backgroundURL, &releaseDate); err != nil {
		t.Fatalf("query restored movie snapshot: %v", err)
	}
	if title != movie.Title || posterURL != canonicalPoster || backgroundURL != canonicalArt || releaseDate != movie.ReleaseDate {
		t.Fatalf("unexpected cached movie snapshot: title=%q poster=%q background=%q release=%q", title, posterURL, backgroundURL, releaseDate)
	}

	if _, err := service.persistMoviePage(ctx, ProviderMoviePage{
		Items: []ProviderMovie{{
			ExternalID:  "123",
			Title:       "Shallow Search Result",
			PosterURL:   "https://image.tmdb.org/search-poster.jpg",
			BackdropURL: "https://image.tmdb.org/search-background.jpg",
			ReleaseDate: "2025-04-18",
		}},
		Page: 1, TotalPages: 1, TotalResults: 1,
	}); err != nil {
		t.Fatalf("persist shallow movie page: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT display_title, poster_url, background_url, release_date::text
		FROM titles
		WHERE id = $1::uuid
	`, movieID).Scan(&title, &posterURL, &backgroundURL, &releaseDate); err != nil {
		t.Fatalf("query movie snapshot after shallow search: %v", err)
	}
	if title != movie.Title || posterURL != canonicalPoster || backgroundURL != canonicalArt || releaseDate != movie.ReleaseDate {
		t.Fatalf("shallow search replaced canonical movie snapshot: title=%q poster=%q background=%q release=%q", title, posterURL, backgroundURL, releaseDate)
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

func TestMatchMappedEpisodesKeepsMatchedEpisodesWhenTVDBHasUnmatchedTail(t *testing.T) {
	episodes, links, err := matchMappedEpisodes(
		"tvdb:series:1002",
		[]ProviderEpisode{
			{ExternalID: "1201", Name: "Episode 1", SeasonNumber: 2, EpisodeNumber: 1, AirDate: "2025-01-05"},
			{ExternalID: "1202", Name: "Episode 2", SeasonNumber: 2, EpisodeNumber: 2, AirDate: "2025-01-12"},
		},
		[]Episode{{ID: "tmdb-episode", Name: "Episode 1", SeasonNumber: 1, EpisodeNumber: 13, AirDate: "2025-01-05", ExternalIDs: map[string]string{"tmdb": "513"}}},
	)
	if err != nil {
		t.Fatalf("match partial TVDB hierarchy: %v", err)
	}
	if len(episodes) != 1 || episodes[0].ID != "tmdb-episode" || episodes[0].ExternalIDs["tvdb"] != "1201" || links["tmdb-episode"] != "1201" {
		t.Fatalf("unexpected partial mapping: episodes=%+v links=%+v", episodes, links)
	}
}

func TestMatchMappedEpisodesRejectsCompletelyUnmatchedTVDBSeason(t *testing.T) {
	_, _, err := matchMappedEpisodes(
		"tvdb:series:1002",
		[]ProviderEpisode{{ExternalID: "1201", Name: "Unknown", SeasonNumber: 2, EpisodeNumber: 1, AirDate: "2025-01-05"}},
		[]Episode{{ID: "tmdb-episode", Name: "Different", SeasonNumber: 1, EpisodeNumber: 3, AirDate: "2024-01-21", ExternalIDs: map[string]string{"tmdb": "503"}}},
	)
	if !errors.Is(err, ErrProviderFailure) {
		t.Fatalf("expected provider failure for completely unmatched season, got %v", err)
	}
}

func TestReplaceTVDBEpisodeIDRepairsStaleNumberBasedLink(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	ctx := context.Background()
	var episodeID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO titles (media_type)
		VALUES ('episode')
		RETURNING id::text
	`).Scan(&episodeID); err != nil {
		t.Fatalf("create episode: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id)
		VALUES ($1::uuid, 'tvdb', 'episode', 'stale-season-number-match')
	`, episodeID); err != nil {
		t.Fatalf("seed stale TVDB identity: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin identity repair: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := replaceTVDBEpisodeID(ctx, tx, episodeID, "official-air-date-match"); err != nil {
		t.Fatalf("replace TVDB identity: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit TVDB identity repair: %v", err)
	}
	var externalID string
	if err := pool.QueryRow(ctx, `
		SELECT external_id
		FROM title_external_ids
		WHERE title_id = $1::uuid AND provider = 'tvdb' AND namespace = 'episode'
	`, episodeID).Scan(&externalID); err != nil {
		t.Fatalf("query repaired TVDB identity: %v", err)
	}
	if externalID != "official-air-date-match" {
		t.Fatalf("unexpected repaired TVDB identity %q", externalID)
	}
}

func TestSeriesDetailsValidatesEpisodeOrderSelection(t *testing.T) {
	profileID := "profile-id"
	expiresAt := time.Now().UTC().Add(time.Hour)
	principal := auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt}
	service := &Service{}

	_, err := service.SeriesDetails(context.Background(), principal, "series-id", SeriesDetailsOptions{
		MappingProvider: "tmdb",
		EpisodeOrderID:  "2",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected TMDB episode order rejection, got %v", err)
	}

	_, err = service.SeriesDetails(context.Background(), principal, "series-id", SeriesDetailsOptions{
		MappingProvider: "tvdb",
		EpisodeOrderID:  "not-an-id",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid TVDB episode order rejection, got %v", err)
	}
}

func TestNormalizeEpisodeOrdersUsesCanonicalTVDBLabels(t *testing.T) {
	orders := normalizeEpisodeOrders([]EpisodeOrder{
		{ID: "7", Name: "Streaming Order", Type: "alttwo"},
		{ID: "1", Name: "Official", Type: "official", IsDefault: true},
		{ID: "4", Name: "Story Order", Type: "alternate"},
		{ID: "3", Name: "Absolute", Type: "absolute"},
		{ID: "2", Name: "Disc", Type: "dvd"},
	})
	wantNames := []string{"Aired Order", "DVD Order", "Absolute Order", "Story Order", "Streaming Order"}
	if len(orders) != len(wantNames) {
		t.Fatalf("unexpected normalized orders: %+v", orders)
	}
	for index, wantName := range wantNames {
		if orders[index].Name != wantName {
			t.Fatalf("unexpected order %d: got %+v want name %q", index, orders[index], wantName)
		}
	}
	if defaultEpisodeOrderID(orders) != "1" {
		t.Fatalf("unexpected default order: %+v", orders)
	}
}
