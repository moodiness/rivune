package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

func newProviderTestService(pool *pgxpool.Pool, providers ProviderSet, cacheTTL time.Duration, logger *slog.Logger) *Service {
	return NewServiceWithProviderSource(pool, staticProviderSource{providers: providers}, cacheTTL, logger)
}

func TestNormalizeQueryOptionsDefaultsAndCanonicalizes(t *testing.T) {
	options, err := normalizeQueryOptions(QueryOptions{Language: "FR-fr", Region: "fr"})
	if err != nil {
		t.Fatalf("normalize options: %v", err)
	}
	if options.Page != 1 || options.Language != "fr-FR" || options.Region != "FR" {
		t.Fatalf("unexpected normalized options: %+v", options)
	}
}

func TestSeasonArtworkNormalizationAndSerializationRemainIndependent(t *testing.T) {
	summary := normalizeSeasonSummary("series-id", "season-id", ProviderSeasonSummary{
		ExternalID: "92011", PosterURL: "https://images.example/poster.jpg", BackdropURL: "https://images.example/backdrop.jpg",
	})
	season := normalizeSeason("series-id", "season-id", ProviderSeason{
		ExternalID: "92011", PosterURL: "https://images.example/poster.jpg", BackdropURL: "https://images.example/backdrop.jpg",
	})
	for name, value := range map[string]any{"summary": summary, "season": season} {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("encode %s: %v", name, err)
		}
		if !bytes.Contains(payload, []byte(`"posterUrl":"https://images.example/poster.jpg"`)) ||
			!bytes.Contains(payload, []byte(`"backdropUrl":"https://images.example/backdrop.jpg"`)) {
			t.Fatalf("%s artwork was not serialized independently: %s", name, payload)
		}
	}
	withoutBackdrop := map[string]any{
		"summary": normalizeSeasonSummary("series-id", "season-id", ProviderSeasonSummary{
			ExternalID: "92011", PosterURL: "https://images.example/poster.jpg",
		}),
		"season": normalizeSeason("series-id", "season-id", ProviderSeason{
			ExternalID: "92011", PosterURL: "https://images.example/poster.jpg",
		}),
	}
	for name, value := range withoutBackdrop {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("encode %s without backdrop: %v", name, err)
		}
		if bytes.Contains(payload, []byte(`"backdropUrl"`)) {
			t.Fatalf("absent %s backdrop must remain omitted: %s", name, payload)
		}
	}
}

func TestEpisodeArtworkNormalizationAndSerializationRemainIndependent(t *testing.T) {
	episode := normalizeEpisode("season-id", "episode-id", ProviderEpisode{
		ExternalID:  "9201101",
		StillURL:    "https://images.example/still.jpg",
		BackdropURL: "https://images.example/backdrop.jpg",
	})
	payload, err := json.Marshal(episode)
	if err != nil {
		t.Fatalf("encode episode: %v", err)
	}
	if !bytes.Contains(payload, []byte(`"stillUrl":"https://images.example/still.jpg"`)) ||
		!bytes.Contains(payload, []byte(`"backdropUrl":"https://images.example/backdrop.jpg"`)) {
		t.Fatalf("episode artwork was not serialized independently: %s", payload)
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
	if err := requireActiveProfile(auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator}); !errors.Is(err, ErrProfileRequired) {
		t.Fatalf("expected missing profile rejection, got %v", err)
	}
	if err := requireActiveProfile(auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expired}); !errors.Is(err, ErrProfileRequired) {
		t.Fatalf("expected expired grant rejection, got %v", err)
	}
	active := time.Now().UTC().Add(time.Minute)
	if err := requireActiveProfile(auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileID, ProfileGrantExpiresAt: &active}); err != nil {
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

	service := NewService(pool, nil, nil, nil, 0, nil)
	result, err := service.RefreshMissing(context.Background(), RefreshMissingOptions{Language: "fr-FR", BatchSize: 1})
	if err != nil {
		t.Fatalf("refresh missing metadata: %v", err)
	}
	if result.Candidates != 0 || result.Refreshed != 0 || result.Failed != 0 || len(result.FailedTitles) != 0 {
		t.Fatalf("addon-only title became an unrefreshable candidate: %+v", result)
	}
}

func TestSeasonHierarchyValidationRejectsMismatchedSeason(t *testing.T) {
	mismatched := ProviderSeason{
		ExternalID:   "910209",
		Name:         "Season 9",
		SeasonNumber: 2,
		Episodes: []ProviderEpisode{{
			ExternalID: "920201", Name: "Fixture Episode 2021", SeasonNumber: 2, EpisodeNumber: 2021,
		}},
	}
	if err := validateProviderSeasonHierarchy(mismatched, "910209", 9); !errors.Is(err, ErrProviderFailure) {
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
		ExternalID: "910209", Name: "Season 9", SeasonNumber: 9,
		Episodes: []ProviderEpisode{
			{ExternalID: "920201", Name: "Fixture Episode 2021", SeasonNumber: 9, EpisodeNumber: 2021},
			{ExternalID: "920202", Name: "Fixture Episode 2022", SeasonNumber: 9, EpisodeNumber: 2022},
		},
	}
	if err := validateProviderSeasonHierarchy(season, "910209", 9); err != nil {
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
		VALUES ($1::uuid, 'tmdb', 'series', '92001'), ($2::uuid, 'tmdb', 'movie', '900101')
	`, seriesID, movieID); err != nil {
		t.Fatalf("seed trailer external IDs: %v", err)
	}
	provider := &fakeTrailerProvider{responsesByCall: map[string][]ProviderTrailer{
		"series:92001:en-US:3": {{YouTubeID: "season-video", Name: "Season 3 Trailer", Site: "YouTube", Type: "Trailer"}},
		"movie:900101:en-US":   {{YouTubeID: "movie-video", Site: "YouTube", Type: "Trailer"}},
	}}
	service := newProviderTestService(pool, ProviderSet{Trailer: provider}, 0, nil)
	seasonNumber := 3
	result, err := service.Trailers(ctx, canonicalMergePrincipal(), seriesID, "en-US", "", &seasonNumber)
	if err != nil || len(result.Trailers) != 1 || result.Trailers[0].YouTubeID != "season-video" {
		t.Fatalf("season trailers=%+v err=%v", result, err)
	}
	if len(provider.calls) < 1 || provider.calls[0] != "series:92001:en-US:3" {
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
	service := NewService(nil, nil, nil, nil, 0, nil)
	seasonNumber := -1
	_, err := service.Trailers(context.Background(), canonicalMergePrincipal(), "title-id", "en-US", "", &seasonNumber)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid season input, got %v", err)
	}
}

func TestTrailersRejectInvalidCaptionLanguage(t *testing.T) {
	service := NewService(nil, nil, nil, nil, 0, nil)
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
	result, err := chooseTrailers(context.Background(), provider, MediaTypeMovie, "900101", "fr-FR", "fr-FR", nil)
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
	if len(provider.calls) != 2 || provider.calls[0] != "movie:900101:fr-FR" || provider.calls[1] != "movie:900101:en-US" {
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
	result, err := chooseTrailers(context.Background(), provider, MediaTypeMovie, "900101", "fr-FR", "fr-FR", nil)
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
		"series:92001:en-US:3": {{YouTubeID: "season-three", Name: "Season 3 Trailer", Site: "YouTube", Type: "Trailer"}},
	}}
	seasonNumber := 3
	result, err := chooseTrailers(context.Background(), provider, MediaTypeSeries, "92001", "fr-FR", "fr-FR", &seasonNumber)
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
	result, err := chooseTrailers(context.Background(), provider, MediaTypeMovie, "900101", "de-DE", "", nil)
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
	_, err := chooseTrailers(context.Background(), provider, MediaTypeMovie, "900101", "fr-FR", "", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestChooseTrailersPropagatesProviderErrors(t *testing.T) {
	provider := &fakeTrailerProvider{
		responses: map[string][]ProviderTrailer{},
		errors:    map[string]error{"fr-FR": ErrProviderRateLimited},
	}
	_, err := chooseTrailers(context.Background(), provider, MediaTypeMovie, "900101", "fr-FR", "", nil)
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
		seriesPoster   = "https://images.example.test/fixture-animated-series-poster.jpg"
		seriesBackdrop = "https://images.example.test/fixture-animated-series-background.jpg"
		seasonPoster   = "https://images.example.test/fixture-season-5.jpg"
		episodeStill   = "https://images.example.test/fixture-episode-6.jpg"
	)
	series := Series{
		ID:           seriesID,
		MediaType:    MediaTypeSeries,
		Name:         "Fixture Animated Series",
		FirstAirDate: "1999-03-28",
		PosterURL:    seriesPoster,
		BackdropURL:  seriesBackdrop,
		Cast:         []CastMember{},
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
			Name:          "Fixture Episode",
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
			($1::uuid, 'series', NULL, NULL, 'Stale Fixture Animated Series', 'https://images.example.test/stale-poster.jpg', 'https://images.example.test/stale-background.jpg'),
			($2::uuid, 'season', $1::uuid, 5, NULL, NULL, NULL),
			($3::uuid, 'episode', $2::uuid, 6, '   ', NULL, NULL)
	`, seriesID, seasonID, episodeID); err != nil {
		t.Fatalf("seed cached titles: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($1::uuid, 'tmdb', 'series', '900001'),
			($2::uuid, 'tmdb', 'season', '900005'),
			($3::uuid, 'tmdb', 'episode', '900006')
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
	principal := auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt}
	service := NewService(pool, nil, nil, nil, 0, nil)
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
	if seriesTitle != "Fixture Animated Series" || seriesPosterURL != seriesPoster || seriesBackgroundURL != seriesBackdrop {
		t.Fatalf("unexpected cached series snapshot: title=%q poster=%q background=%q", seriesTitle, seriesPosterURL, seriesBackgroundURL)
	}
	if seasonTitle != "Saison 5" || seasonPosterURL != seasonPoster {
		t.Fatalf("unexpected cached season snapshot: title=%q poster=%q", seasonTitle, seasonPosterURL)
	}
	if episodeTitle != "Fixture Episode" || episodePosterURL != episodeStill {
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
		Cast:          []CastMember{},
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
	principal := auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt}
	service := NewService(pool, nil, nil, nil, 0, nil)
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

	if _, err := service.persistMoviePage(ctx, principal, ProviderMoviePage{
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

func TestMatchMappedEpisodesLeavesCompletelyUnmatchedEpisodesForProviderPersistence(t *testing.T) {
	episodes, links, err := matchMappedEpisodes(
		"tvdb:series:1002",
		[]ProviderEpisode{{ExternalID: "1201", Name: "Unknown", SeasonNumber: 2, EpisodeNumber: 1, AirDate: "2025-01-05"}},
		[]Episode{{ID: "tmdb-episode", Name: "Different", SeasonNumber: 1, EpisodeNumber: 3, AirDate: "2024-01-21", ExternalIDs: map[string]string{"tmdb": "503"}}},
	)
	if err != nil || len(episodes) != 0 || len(links) != 0 {
		t.Fatalf("unmatched TVDB episode was not deferred for provider persistence: episodes=%+v links=%+v err=%v", episodes, links, err)
	}
}

type specialsMapper struct {
	season       ProviderSeason
	seriesTVDBID string
	seasonTVDBID string
}

func (mapper *specialsMapper) SeriesSeasons(context.Context, string, string) ([]ProviderSeasonSummary, error) {
	return nil, errors.New("unexpected series seasons call")
}

func (mapper *specialsMapper) SeriesSeason(_ context.Context, seriesTVDBID, seasonTVDBID string) (ProviderSeason, error) {
	mapper.seriesTVDBID = seriesTVDBID
	mapper.seasonTVDBID = seasonTVDBID
	return mapper.season, nil
}

func TestMappedSpecialsIgnoreUnrelatedCanonicalSeasonFailure(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	seriesID, specialsID, episodeID, missingSeasonID := seedMappedSpecialsCache(t, pool, true)
	mapper := &specialsMapper{season: ProviderSeason{
		ExternalID: "1000", Name: "Specials", SeasonNumber: 0,
		Episodes: []ProviderEpisode{{
			ExternalID: "10001", Name: "Fixture Episode", SeasonNumber: 0, EpisodeNumber: 1, AirDate: "2024-01-01",
		}},
	}}
	var logs bytes.Buffer
	service := newProviderTestService(
		pool,
		ProviderSet{Mapper: mapper},
		time.Hour,
		slog.New(slog.NewTextHandler(&logs, nil)),
	)

	season, err := service.SeasonDetails(
		context.Background(),
		canonicalMergePrincipal(),
		mappedSeasonID(seriesID, "1000"),
		"en-US",
		"tvdb",
	)
	if err != nil {
		t.Fatalf("load mapped specials: %v", err)
	}
	if mapper.seriesTVDBID != "93001" || mapper.seasonTVDBID != "1000" {
		t.Fatalf("unexpected mapped route: series=%q season=%q", mapper.seriesTVDBID, mapper.seasonTVDBID)
	}
	if season.SeasonNumber != 0 || season.ID != mappedSeasonID(seriesID, "1000") || len(season.Episodes) != 1 ||
		season.Episodes[0].ID != episodeID || season.Episodes[0].SeasonID != season.ID ||
		season.Episodes[0].SeasonNumber != 0 || season.Episodes[0].ExternalIDs["tvdb"] != "10001" {
		t.Fatalf("unexpected mapped specials payload: %+v", season)
	}
	if !strings.Contains(logs.String(), "canonical season unavailable while assembling TVDB season") ||
		!strings.Contains(logs.String(), missingSeasonID) ||
		!strings.Contains(logs.String(), ErrProviderUnavailable.Error()) {
		t.Fatalf("unrelated canonical provider failure was not logged: %q", logs.String())
	}
	var ordinal int
	if err := pool.QueryRow(context.Background(), `SELECT ordinal FROM titles WHERE id = $1::uuid`, specialsID).Scan(&ordinal); err != nil {
		t.Fatalf("query specials ordinal: %v", err)
	}
	if ordinal != 0 {
		t.Fatalf("specials ordinal changed to %d", ordinal)
	}
}

func TestMappedSpecialsPersistTVDBOnlyEpisodesWithoutTMDBSeason(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	seriesID, _, _, _ := seedMappedSpecialsCache(t, pool, false)
	mapper := &specialsMapper{season: ProviderSeason{
		ExternalID: "930199", Name: "Specials", SeasonNumber: 0,
		Episodes: []ProviderEpisode{
			{ExternalID: "940101", Name: "Fixture Episode 1", SeasonNumber: 0, EpisodeNumber: 1, AirDate: "2023-06-30"},
			{ExternalID: "940102", Name: "Fixture Episode 2", SeasonNumber: 0, EpisodeNumber: 2, AirDate: "2023-05-05"},
			{ExternalID: "940103", Name: "Fixture Episode 3", SeasonNumber: 0, EpisodeNumber: 3, AirDate: "2024-11-11"},
			{ExternalID: "940104", Name: "Fixture Episode 4", SeasonNumber: 0, EpisodeNumber: 4, AirDate: "2024-11-15"},
		},
	}}
	service := newProviderTestService(pool, ProviderSet{Mapper: mapper}, time.Hour, nil)
	mappedID := mappedSeasonID(seriesID, mapper.season.ExternalID)

	first, err := service.SeasonDetails(context.Background(), canonicalMergePrincipal(), mappedID, "en-US", "tvdb")
	if err != nil {
		t.Fatalf("load TVDB-only specials: %v", err)
	}
	if first.SeasonNumber != 0 || first.ID != mappedID || len(first.Episodes) != 4 {
		t.Fatalf("unexpected TVDB-only specials payload: %+v", first)
	}
	firstIDs := make([]string, len(first.Episodes))
	for index, episode := range first.Episodes {
		firstIDs[index] = episode.ID
		if episode.ID == "" || episode.SeasonID != mappedID || episode.SeasonNumber != 0 ||
			episode.EpisodeNumber != index+1 || episode.ExternalIDs["tvdb"] != mapper.season.Episodes[index].ExternalID {
			t.Fatalf("unexpected persisted special episode %d: %+v", index, episode)
		}
	}

	second, err := service.SeasonDetails(context.Background(), canonicalMergePrincipal(), mappedID, "en-US", "tvdb")
	if err != nil {
		t.Fatalf("reload TVDB-only specials: %v", err)
	}
	if len(second.Episodes) != len(firstIDs) {
		t.Fatalf("reloaded specials changed episode count: %+v", second)
	}
	for index, episode := range second.Episodes {
		if episode.ID != firstIDs[index] {
			t.Fatalf("special episode identity was not stable: first=%q second=%q", firstIDs[index], episode.ID)
		}
	}

	var persistedEpisodes int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM titles AS season
		JOIN title_external_ids AS season_identity
		  ON season_identity.title_id = season.id
		 AND season_identity.provider = 'tvdb'
		 AND season_identity.namespace = 'season'
		 AND season_identity.external_id = '930199'
		JOIN titles AS episode
		  ON episode.parent_id = season.id
		 AND episode.media_type = 'episode'
		JOIN title_external_ids AS episode_identity
		  ON episode_identity.title_id = episode.id
		 AND episode_identity.provider = 'tvdb'
		 AND episode_identity.namespace = 'episode'
		WHERE season.parent_id = $1::uuid
		  AND season.media_type = 'season'
		  AND season.ordinal = 0
	`, seriesID).Scan(&persistedEpisodes); err != nil {
		t.Fatalf("query persisted TVDB-only specials: %v", err)
	}
	if persistedEpisodes != 4 {
		t.Fatalf("persisted %d TVDB-only special episodes, want 4", persistedEpisodes)
	}
}

func TestMappedSeasonPersistsTVDBOnlyEpisodeBeforeTMDBPublishesIt(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	seriesID, _, _, _ := seedMappedSpecialsCache(t, pool, false)
	mapper := &specialsMapper{season: ProviderSeason{
		ExternalID: "2257111", Name: "Season 2", SeasonNumber: 2,
		Episodes: []ProviderEpisode{{
			ExternalID: "11888797", Name: "TBA", SeasonNumber: 2, EpisodeNumber: 1,
		}},
	}}
	service := newProviderTestService(pool, ProviderSet{Mapper: mapper}, time.Hour, nil)
	mappedID := mappedSeasonID(seriesID, mapper.season.ExternalID)

	season, err := service.SeasonDetails(context.Background(), canonicalMergePrincipal(), mappedID, "en-US", "tvdb")
	if err != nil {
		t.Fatalf("load TVDB season before TMDB episode publication: %v", err)
	}
	if season.SeasonNumber != 2 || season.ID != mappedID || len(season.Episodes) != 1 ||
		season.Episodes[0].SeasonID != mappedID || season.Episodes[0].ExternalIDs["tvdb"] != "11888797" {
		t.Fatalf("unexpected TVDB-only season payload: %+v", season)
	}

	var persistedEpisodes int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM titles AS season
		JOIN title_external_ids AS season_identity
		  ON season_identity.title_id = season.id
		 AND season_identity.provider = 'tvdb'
		 AND season_identity.namespace = 'season'
		 AND season_identity.external_id = '2257111'
		JOIN titles AS episode
		  ON episode.parent_id = season.id
		 AND episode.media_type = 'episode'
		 AND episode.ordinal = 1
		JOIN title_external_ids AS episode_identity
		  ON episode_identity.title_id = episode.id
		 AND episode_identity.provider = 'tvdb'
		 AND episode_identity.namespace = 'episode'
		 AND episode_identity.external_id = '11888797'
		WHERE season.parent_id = $1::uuid
		  AND season.media_type = 'season'
		  AND season.ordinal = 2
	`, seriesID).Scan(&persistedEpisodes); err != nil {
		t.Fatalf("query persisted TVDB-only episode: %v", err)
	}
	if persistedEpisodes != 1 {
		t.Fatalf("persisted %d TVDB-only episodes, want 1", persistedEpisodes)
	}
}

func TestMappedEmptySpecialsReturnValidEmptySeason(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	seriesID, _, _, _ := seedMappedSpecialsCache(t, pool, false)
	mapper := &specialsMapper{season: ProviderSeason{
		ExternalID: "1000", Name: "Specials", SeasonNumber: 0, Episodes: []ProviderEpisode{},
	}}
	service := newProviderTestService(pool, ProviderSet{Mapper: mapper}, time.Hour, nil)

	season, err := service.SeasonDetails(
		context.Background(),
		canonicalMergePrincipal(),
		mappedSeasonID(seriesID, "1000"),
		"en-US",
		"tvdb",
	)
	if err != nil {
		t.Fatalf("load empty mapped specials: %v", err)
	}
	if season.SeasonNumber != 0 || season.Name != "Specials" || season.Episodes == nil || len(season.Episodes) != 0 {
		t.Fatalf("empty specials did not remain a valid season: %+v", season)
	}
}

func seedMappedSpecialsCache(t *testing.T, pool *pgxpool.Pool, includeCanonicalSpecials bool) (string, string, string, string) {
	t.Helper()
	const (
		seriesID        = "00000000-0000-4000-8000-000000000600"
		specialsID      = "00000000-0000-4000-8000-000000000601"
		episodeID       = "00000000-0000-4000-8000-000000000602"
		missingSeasonID = "00000000-0000-4000-8000-000000000699"
	)
	base := Series{
		ID: seriesID, MediaType: MediaTypeSeries, Name: "Fixture Series",
		Cast: []CastMember{}, Seasons: []SeasonSummary{{
			ID: missingSeasonID, MediaType: MediaTypeSeason, SeriesID: seriesID,
			Name: "Season 1", SeasonNumber: 1,
		}},
		ExternalIDs: map[string]string{"tmdb": "92001", "tvdb": "93001"},
	}
	var specials Season
	if includeCanonicalSpecials {
		base.Seasons = append([]SeasonSummary{{
			ID: specialsID, MediaType: MediaTypeSeason, SeriesID: seriesID,
			Name: "Specials", SeasonNumber: 0, EpisodeCount: 1,
		}}, base.Seasons...)
		specials = Season{
			ID: specialsID, MediaType: MediaTypeSeason, SeriesID: seriesID,
			Name: "Specials", SeasonNumber: 0,
			Episodes: []Episode{{
				ID: episodeID, MediaType: MediaTypeEpisode, SeasonID: specialsID,
				Name: "Fixture Episode", SeasonNumber: 0, EpisodeNumber: 1, AirDate: "2024-01-01",
				ExternalIDs: map[string]string{"tmdb": "9201001"},
			}},
			ExternalIDs: map[string]string{"tmdb": "92010"},
		}
	}
	basePayload, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("encode canonical series cache: %v", err)
	}
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, display_title)
		VALUES ($1::uuid, 'series', 'Fixture Series');
		INSERT INTO titles (id, media_type, parent_id, ordinal, display_title)
		VALUES ($3::uuid, 'season', $1::uuid, 1, 'Season 1');
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($1::uuid, 'tmdb', 'series', '92001'),
			($1::uuid, 'tvdb', 'series', '93001'),
			($3::uuid, 'tmdb', 'season', '4000');
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at)
		VALUES ($1::uuid, 'tmdb', 'en-US', $2::jsonb, now() + interval '1 hour')
	`, pgx.QueryExecModeSimpleProtocol, seriesID, string(basePayload), missingSeasonID); err != nil {
		t.Fatalf("seed canonical series cache: %v", err)
	}
	if includeCanonicalSpecials {
		specialsPayload, err := json.Marshal(specials)
		if err != nil {
			t.Fatalf("encode canonical specials cache: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO titles (id, media_type, parent_id, ordinal, display_title) VALUES
				($2::uuid, 'season', $1::uuid, 0, 'Specials'),
				($3::uuid, 'episode', $2::uuid, 1, 'Fixture Episode');
			INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
				($2::uuid, 'tmdb', 'season', '92010'),
				($3::uuid, 'tmdb', 'episode', '9201001');
			INSERT INTO title_metadata (title_id, provider, language, payload, expires_at)
			VALUES ($2::uuid, 'tmdb', 'en-US', $4::jsonb, now() + interval '1 hour')
		`, pgx.QueryExecModeSimpleProtocol, seriesID, specialsID, episodeID, string(specialsPayload)); err != nil {
			t.Fatalf("seed canonical specials cache: %v", err)
		}
	}
	return seriesID, specialsID, episodeID, missingSeasonID
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

type cacheOnlyTelevisionMapper struct{}

func (cacheOnlyTelevisionMapper) SeriesSeasons(context.Context, string, string) ([]ProviderSeasonSummary, error) {
	return nil, errors.New("unexpected mapper call")
}

func (cacheOnlyTelevisionMapper) SeriesSeason(context.Context, string, string) (ProviderSeason, error) {
	return ProviderSeason{}, errors.New("unexpected mapper call")
}

type failingTelevisionMapper struct {
	err   error
	calls int
}

func (mapper *failingTelevisionMapper) SeriesSeasons(context.Context, string, string) ([]ProviderSeasonSummary, error) {
	mapper.calls++
	return nil, mapper.err
}

func (mapper *failingTelevisionMapper) SeriesSeason(context.Context, string, string) (ProviderSeason, error) {
	return ProviderSeason{}, mapper.err
}

func TestDefaultTVDBMappingFallsBackToTMDBWhenTVDBIsUnavailable(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	ctx := context.Background()
	const seriesID = "de9bac21-adce-4b01-b11a-f6db8dea61e4"
	base := Series{
		ID: seriesID, MediaType: MediaTypeSeries, Name: "New York Unité Spéciale",
		Cast: []CastMember{}, Seasons: []SeasonSummary{}, Aliases: []Alias{}, EpisodeOrders: []EpisodeOrder{},
		MappingProvider: providerName,
		ExternalIDs:     map[string]string{"tmdb": "2734", "tvdb": "75692", "imdb": "tt0203259"},
	}
	basePayload, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("encode canonical series: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, display_title)
		VALUES ($1::uuid, 'series', 'New York Unité Spéciale');
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($1::uuid, 'tmdb', 'series', '2734'),
			($1::uuid, 'tvdb', 'series', '75692'),
			($1::uuid, 'imdb', 'series', 'tt0203259');
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at)
		VALUES ($1::uuid, 'tmdb', 'fr-FR', $2::jsonb, now() + interval '1 hour')
	`, pgx.QueryExecModeSimpleProtocol, seriesID, string(basePayload)); err != nil {
		t.Fatalf("seed canonical series cache: %v", err)
	}
	profileID := "44444444-4444-4444-8444-444444444444"
	expiresAt := time.Now().UTC().Add(time.Hour)
	principal := auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt}

	withoutTVDB, err := NewService(pool, nil, nil, nil, 0, nil).SeriesDetails(ctx, principal, seriesID, SeriesDetailsOptions{Language: "fr-FR", MappingProvider: "tvdb"})
	if err != nil {
		t.Fatalf("fallback without TVDB configuration: %v", err)
	}
	if withoutTVDB.MappingProvider != providerName || withoutTVDB.Name != base.Name {
		t.Fatalf("unexpected fallback without TVDB: %+v", withoutTVDB)
	}

	mapper := &failingTelevisionMapper{err: ErrProviderUnauthorized}
	withExpiredTVDB, err := newProviderTestService(pool, ProviderSet{Mapper: mapper}, 0, nil).SeriesDetails(ctx, principal, seriesID, SeriesDetailsOptions{Language: "fr-FR", MappingProvider: "tvdb"})
	if err != nil {
		t.Fatalf("fallback with expired TVDB credentials: %v", err)
	}
	if withExpiredTVDB.MappingProvider != providerName || mapper.calls != 1 {
		t.Fatalf("unexpected fallback with expired TVDB: series=%+v calls=%d", withExpiredTVDB, mapper.calls)
	}
}

func TestExplicitTVDBEpisodeOrderDoesNotFallBackToTMDB(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	ctx := context.Background()
	const seriesID = "ee9bac21-adce-4b01-b11a-f6db8dea61e4"
	base := Series{
		ID: seriesID, MediaType: MediaTypeSeries, Name: "Series",
		Cast: []CastMember{}, Seasons: []SeasonSummary{}, Aliases: []Alias{}, EpisodeOrders: []EpisodeOrder{},
		MappingProvider: providerName,
		ExternalIDs:     map[string]string{"tmdb": "2734", "tvdb": "75692"},
	}
	basePayload, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("encode canonical series: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, display_title)
		VALUES ($1::uuid, 'series', 'Series');
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($1::uuid, 'tmdb', 'series', '2734'),
			($1::uuid, 'tvdb', 'series', '75692');
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at)
		VALUES ($1::uuid, 'tmdb', 'fr-FR', $2::jsonb, now() + interval '1 hour')
	`, pgx.QueryExecModeSimpleProtocol, seriesID, string(basePayload)); err != nil {
		t.Fatalf("seed canonical series cache: %v", err)
	}
	profileID := "44444444-4444-4444-8444-444444444444"
	expiresAt := time.Now().UTC().Add(time.Hour)
	mapper := &failingTelevisionMapper{err: ErrProviderUnauthorized}
	_, err = newProviderTestService(pool, ProviderSet{Mapper: mapper}, 0, nil).SeriesDetails(
		ctx,
		auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt},
		seriesID,
		SeriesDetailsOptions{Language: "fr-FR", MappingProvider: "tvdb", EpisodeOrderID: "2"},
	)
	if !errors.Is(err, ErrProviderUnauthorized) || mapper.calls != 1 {
		t.Fatalf("explicit TVDB order must preserve provider error, err=%v calls=%d", err, mapper.calls)
	}
}

func TestCachedTVDBSeriesMappingUsesCanonicalCast(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	ctx := context.Background()
	const seriesID = "7e9bac21-adce-4b01-b11a-f6db8dea61e4"
	base := Series{
		ID: seriesID, MediaType: MediaTypeSeries, Name: "Solo Leveling", OriginalName: "俺だけレベルアップな件",
		OriginalLanguage: "ja", Overview: "An anime fixture.", Genres: []Genre{},
		Cast:    []CastMember{{ID: "123", Name: "Taito Ban", Character: "Sung Jinwoo"}},
		Seasons: []SeasonSummary{}, Aliases: []Alias{},
		EpisodeOrders:   []EpisodeOrder{{ID: "1", Name: "Aired Order", Type: "official", IsDefault: true}},
		MappingProvider: providerName,
		ExternalIDs:     map[string]string{"tmdb": "127532", "tvdb": "389597", "imdb": "tt21209876"},
	}
	mapped := base
	mapped.Cast = []CastMember{}
	mapped.MappingProvider = "tvdb"
	mapped.SelectedEpisodeOrderID = "1"
	basePayload, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("encode canonical series: %v", err)
	}
	mappedPayload, err := json.Marshal(mapped)
	if err != nil {
		t.Fatalf("encode mapped series: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, display_title)
		VALUES ($1::uuid, 'series', 'Solo Leveling');
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($1::uuid, 'tmdb', 'series', '127532'),
			($1::uuid, 'tvdb', 'series', '389597'),
			($1::uuid, 'imdb', 'series', 'tt21209876');
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at) VALUES
			($1::uuid, 'tmdb', 'fr-FR', $2::jsonb, now() + interval '1 hour'),
			($1::uuid, 'tvdb', 'fr-FR', $3::jsonb, now() + interval '1 hour')
	`, pgx.QueryExecModeSimpleProtocol, seriesID, string(basePayload), string(mappedPayload)); err != nil {
		t.Fatalf("seed mapped series cache: %v", err)
	}
	profileID := "44444444-4444-4444-8444-444444444444"
	expiresAt := time.Now().UTC().Add(time.Hour)
	service := newProviderTestService(pool, ProviderSet{Mapper: cacheOnlyTelevisionMapper{}}, 0, nil)
	resolved, err := service.SeriesDetails(ctx, auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt}, seriesID, SeriesDetailsOptions{Language: "fr-FR", MappingProvider: "tvdb"})
	if err != nil {
		t.Fatalf("load cached TVDB mapping: %v", err)
	}
	if len(resolved.Cast) != 1 || resolved.Cast[0].Name != "Taito Ban" {
		t.Fatalf("mapped series lost canonical cast: %+v", resolved.Cast)
	}
}

func TestSeriesDetailsValidatesEpisodeOrderSelection(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	principal := canonicalMergePrincipal()
	service := NewService(pool, nil, nil, nil, 0, nil)

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
