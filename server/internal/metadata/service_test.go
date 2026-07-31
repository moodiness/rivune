package metadata

import (
	"context"
	"encoding/json"
	"errors"
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
	responses map[string][]ProviderTrailer
	errors    map[string]error
	calls     []string
}

func (f *fakeTrailerProvider) Trailers(_ context.Context, mediaType, externalID, language string) ([]ProviderTrailer, error) {
	f.calls = append(f.calls, mediaType+":"+externalID+":"+language)
	return f.responses[language], f.errors[language]
}

func TestChooseTrailerSelectsLocalizedOfficialTrailerWithoutEnglishRequest(t *testing.T) {
	provider := &fakeTrailerProvider{responses: map[string][]ProviderTrailer{
		"fr-FR": {
			{YouTubeID: "vimeo", Name: "Unsupported", Site: "Vimeo", Type: "Trailer", Official: true},
			{YouTubeID: "clip", Name: "Unsupported", Site: "YouTube", Type: "Clip", Official: true},
			{YouTubeID: "teaser", Name: "Official teaser", Site: "YouTube", Type: "Teaser", Official: true, PublishedAt: time.Now()},
			{YouTubeID: "unofficial", Name: "Unofficial trailer", Site: "YouTube", Type: "Trailer", PublishedAt: time.Now()},
			{YouTubeID: "official-old", Name: "Old official trailer", Site: "YouTube", Type: "Trailer", Official: true, PublishedAt: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)},
			{YouTubeID: "official-new", Name: "New official trailer", Site: "YouTube", Type: "Trailer", Official: true, PublishedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		},
		"en-US": {{YouTubeID: "english", Site: "YouTube", Type: "Trailer"}},
	}}
	trailer, err := chooseTrailer(context.Background(), provider, MediaTypeMovie, "550", "fr-FR")
	if err != nil {
		t.Fatalf("choose localized trailer: %v", err)
	}
	if trailer.YouTubeID != "official-new" || trailer.Language != "fr-FR" || trailer.IsFallback || trailer.CaptionPreference != "" {
		t.Fatalf("unexpected localized trailer: %+v", trailer)
	}
	if len(provider.calls) != 1 || provider.calls[0] != "movie:550:fr-FR" {
		t.Fatalf("unexpected provider calls: %+v", provider.calls)
	}
}

func TestChooseTrailerUsesTeaserOnlyAsLastResort(t *testing.T) {
	provider := &fakeTrailerProvider{responses: map[string][]ProviderTrailer{
		"fr-FR": {{YouTubeID: "teaser", Name: "Teaser", Site: "YouTube", Type: "Teaser"}},
	}}
	trailer, err := chooseTrailer(context.Background(), provider, MediaTypeSeries, "1396", "fr-FR")
	if err != nil || trailer.YouTubeID != "teaser" {
		t.Fatalf("unexpected teaser result trailer=%+v err=%v", trailer, err)
	}
}

func TestChooseTrailerEnglishFallbackRequestsFrenchCaptions(t *testing.T) {
	provider := &fakeTrailerProvider{responses: map[string][]ProviderTrailer{
		"fr-FR": {{YouTubeID: "not-video", Site: "YouTube", Type: "Featurette"}},
		"en-US": {{YouTubeID: "english", Name: "Official Trailer", Site: "YouTube", Type: "Trailer", Official: true}},
	}}
	trailer, err := chooseTrailer(context.Background(), provider, MediaTypeSeries, "1396", "fr-FR")
	if err != nil {
		t.Fatalf("choose fallback trailer: %v", err)
	}
	if trailer.YouTubeID != "english" || trailer.Language != "en-US" || !trailer.IsFallback || trailer.CaptionPreference != "fr" {
		t.Fatalf("unexpected French fallback: %+v", trailer)
	}
	if len(provider.calls) != 2 || provider.calls[1] != "series:1396:en-US" {
		t.Fatalf("unexpected provider calls: %+v", provider.calls)
	}
}

func TestChooseTrailerNonFrenchFallbackDoesNotRequestCaptions(t *testing.T) {
	provider := &fakeTrailerProvider{responses: map[string][]ProviderTrailer{
		"en-US": {{YouTubeID: "english", Site: "YouTube", Type: "Trailer"}},
	}}
	trailer, err := chooseTrailer(context.Background(), provider, MediaTypeMovie, "550", "de-DE")
	if err != nil {
		t.Fatalf("choose fallback trailer: %v", err)
	}
	if !trailer.IsFallback || trailer.CaptionPreference != "" {
		t.Fatalf("unexpected non-French fallback: %+v", trailer)
	}
}

func TestChooseTrailerReturnsNotFoundWhenNoEligibleVideoExists(t *testing.T) {
	provider := &fakeTrailerProvider{responses: map[string][]ProviderTrailer{
		"fr-FR": {{YouTubeID: "clip", Site: "YouTube", Type: "Clip"}},
		"en-US": {{YouTubeID: "vimeo", Site: "Vimeo", Type: "Trailer"}},
	}}
	_, err := chooseTrailer(context.Background(), provider, MediaTypeMovie, "550", "fr-FR")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestChooseTrailerPropagatesProviderErrors(t *testing.T) {
	provider := &fakeTrailerProvider{
		responses: map[string][]ProviderTrailer{},
		errors:    map[string]error{"fr-FR": ErrProviderRateLimited},
	}
	_, err := chooseTrailer(context.Background(), provider, MediaTypeMovie, "550", "fr-FR")
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
	if _, err := service.SeriesDetails(ctx, principal, seriesID, "fr-FR"); err != nil {
		t.Fatalf("load cached series details: %v", err)
	}
	if _, err := service.SeasonDetails(ctx, principal, seasonID, "fr-FR"); err != nil {
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
