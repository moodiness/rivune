package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/settings"
)

type fakeMetadataService struct {
	discoverOptions         metadata.QueryOptions
	discoverPage            metadata.MoviePage
	discoverErr             error
	searchOptions           metadata.SearchOptions
	searchPage              metadata.MoviePage
	searchErr               error
	detailsID               string
	detailsLanguage         string
	detailsMovie            metadata.Movie
	detailsErr              error
	discoverSeriesOptions   metadata.QueryOptions
	discoverSeriesPage      metadata.SeriesPage
	discoverSeriesErr       error
	searchSeriesOptions     metadata.SearchOptions
	searchSeriesPage        metadata.SeriesPage
	searchSeriesErr         error
	seriesDetailsID         string
	seriesDetailsLanguage   string
	seriesDetailsMapping    string
	seriesDetailsOrder      string
	seriesDetailsValue      metadata.Series
	seriesDetailsErr        error
	seasonDetailsID         string
	seasonDetailsLanguage   string
	seasonDetailsMapping    string
	seasonDetailsValue      metadata.Season
	seasonDetailsErr        error
	trailersID              string
	trailersLanguage        string
	trailersCaptionLanguage string
	trailersSeasonNumber    *int
	trailersValue           metadata.TrailerList
	trailersErr             error
}

func (f *fakeMetadataService) DiscoverMovies(_ context.Context, _ auth.Principal, options metadata.QueryOptions) (metadata.MoviePage, error) {
	f.discoverOptions = options
	return f.discoverPage, f.discoverErr
}

func (f *fakeMetadataService) SearchMovies(_ context.Context, _ auth.Principal, options metadata.SearchOptions) (metadata.MoviePage, error) {
	f.searchOptions = options
	return f.searchPage, f.searchErr
}

func (f *fakeMetadataService) MovieDetails(_ context.Context, _ auth.Principal, titleID, language string) (metadata.Movie, error) {
	f.detailsID = titleID
	f.detailsLanguage = language
	return f.detailsMovie, f.detailsErr
}

func (f *fakeMetadataService) DiscoverSeries(_ context.Context, _ auth.Principal, options metadata.QueryOptions) (metadata.SeriesPage, error) {
	f.discoverSeriesOptions = options
	return f.discoverSeriesPage, f.discoverSeriesErr
}

func (f *fakeMetadataService) SearchSeries(_ context.Context, _ auth.Principal, options metadata.SearchOptions) (metadata.SeriesPage, error) {
	f.searchSeriesOptions = options
	return f.searchSeriesPage, f.searchSeriesErr
}

func (f *fakeMetadataService) SeriesDetails(_ context.Context, _ auth.Principal, titleID string, options metadata.SeriesDetailsOptions) (metadata.Series, error) {
	f.seriesDetailsID = titleID
	f.seriesDetailsLanguage = options.Language
	f.seriesDetailsMapping = options.MappingProvider
	f.seriesDetailsOrder = options.EpisodeOrderID
	return f.seriesDetailsValue, f.seriesDetailsErr
}

func (f *fakeMetadataService) SeasonDetails(_ context.Context, _ auth.Principal, seasonID, language, mappingProvider string) (metadata.Season, error) {
	f.seasonDetailsID = seasonID
	f.seasonDetailsLanguage = language
	f.seasonDetailsMapping = mappingProvider
	return f.seasonDetailsValue, f.seasonDetailsErr
}

func (f *fakeMetadataService) Trailers(_ context.Context, _ auth.Principal, titleID, language, captionLanguage string, seasonNumber *int) (metadata.TrailerList, error) {
	f.trailersID = titleID
	f.trailersLanguage = language
	f.trailersCaptionLanguage = captionLanguage
	f.trailersSeasonNumber = seasonNumber
	return f.trailersValue, f.trailersErr
}

func TestDiscoverMoviesPassesLocaleAndReturnsNormalizedPage(t *testing.T) {
	service := &fakeMetadataService{discoverPage: metadata.MoviePage{
		Items: []metadata.Movie{{ID: "title-id", MediaType: metadata.MediaTypeMovie, Title: "Dune", Genres: []metadata.Genre{}, ExternalIDs: map[string]string{"tmdb": "438631"}}},
		Page:  2, TotalPages: 4, TotalResults: 78,
	}}
	api := metadataAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/movies?page=2&language=fr-FR&region=FR", nil)
	response := httptest.NewRecorder()

	api.discoverMovies(response, request, auth.Principal{})

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.discoverOptions.Page != 2 || service.discoverOptions.Language != "fr-FR" || service.discoverOptions.Region != "FR" {
		t.Fatalf("unexpected options: %+v", service.discoverOptions)
	}
	var body metadata.MoviePage
	decodeResponse(t, response, &body)
	if len(body.Items) != 1 || body.Items[0].ID != "title-id" || body.TotalResults != 78 {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestSearchMoviesPassesQuery(t *testing.T) {
	service := &fakeMetadataService{searchPage: metadata.MoviePage{Items: []metadata.Movie{}, Page: 1}}
	api := metadataAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/search/movies?query=Blade+Runner", nil)
	response := httptest.NewRecorder()

	api.searchMovies(response, request, auth.Principal{})

	if response.Code != http.StatusOK || service.searchOptions.Query != "Blade Runner" {
		t.Fatalf("unexpected search result status=%d options=%+v", response.Code, service.searchOptions)
	}
}

func TestMovieDetailsPassesStableTitleID(t *testing.T) {
	service := &fakeMetadataService{detailsMovie: metadata.Movie{ID: "title-id", MediaType: metadata.MediaTypeMovie, Title: "Fixture Movie", Genres: []metadata.Genre{}, ExternalIDs: map[string]string{"tmdb": "900201"}}}
	api := metadataAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/titles/title-id?language=en-US", nil)
	request.SetPathValue("titleId", "title-id")
	response := httptest.NewRecorder()

	api.movieDetails(response, request, auth.Principal{})

	if response.Code != http.StatusOK || service.detailsID != "title-id" || service.detailsLanguage != "en-US" {
		t.Fatalf("unexpected details status=%d id=%q language=%q", response.Code, service.detailsID, service.detailsLanguage)
	}
}

func TestMovieAndSeriesDetailsTruncateCastToActiveProfileLimit(t *testing.T) {
	profileID := "profile-id"
	expiresAt := time.Now().UTC().Add(time.Hour)
	cast := []metadata.CastMember{
		{ID: "1", Name: "First"},
		{ID: "2", Name: "Second"},
		{ID: "3", Name: "Third"},
	}
	service := &fakeMetadataService{
		detailsMovie:       metadata.Movie{ID: "movie-id", MediaType: metadata.MediaTypeMovie, Title: "Movie", Genres: []metadata.Genre{}, Cast: cast, ExternalIDs: map[string]string{}},
		seriesDetailsValue: metadata.Series{ID: "series-id", MediaType: metadata.MediaTypeSeries, Name: "Series", Genres: []metadata.Genre{}, Cast: cast, Seasons: []metadata.SeasonSummary{}, Aliases: []metadata.Alias{}, EpisodeOrders: []metadata.EpisodeOrder{}, ExternalIDs: map[string]string{}},
	}
	api := metadataAPI(service)
	api.settings = &fakeSettingsService{effective: settings.Effective{Values: settings.EffectiveValues{MaximumCastMembers: 2}}}
	principal := auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt}

	movieRequest := httptest.NewRequest(http.MethodGet, "/api/v1/titles/movie-id", nil)
	movieRequest.SetPathValue("titleId", "movie-id")
	movieResponse := httptest.NewRecorder()
	api.movieDetails(movieResponse, movieRequest, principal)
	if movieResponse.Code != http.StatusOK {
		t.Fatalf("movie details status = %d: %s", movieResponse.Code, movieResponse.Body.String())
	}
	var movie metadata.Movie
	decodeResponse(t, movieResponse, &movie)
	if len(movie.Cast) != 2 || movie.Cast[0].Name != "First" || movie.Cast[1].Name != "Second" {
		t.Fatalf("movie cast was not truncated in provider order: %+v", movie.Cast)
	}

	seriesRequest := httptest.NewRequest(http.MethodGet, "/api/v1/series/series-id", nil)
	seriesRequest.SetPathValue("titleId", "series-id")
	seriesResponse := httptest.NewRecorder()
	api.seriesDetails(seriesResponse, seriesRequest, principal)
	if seriesResponse.Code != http.StatusOK {
		t.Fatalf("series details status = %d: %s", seriesResponse.Code, seriesResponse.Body.String())
	}
	var series metadata.Series
	decodeResponse(t, seriesResponse, &series)
	if len(series.Cast) != 2 || series.Cast[0].Name != "First" || series.Cast[1].Name != "Second" {
		t.Fatalf("series cast was not truncated in provider order: %+v", series.Cast)
	}
	if api.settings.(*fakeSettingsService).requestedProfileID != profileID {
		t.Fatalf("effective settings resolved for %q, want %q", api.settings.(*fakeSettingsService).requestedProfileID, profileID)
	}
}

func TestMovieDetailsWithoutActiveProfileUsesServerCastLimit(t *testing.T) {
	serverLimit := 1
	service := &fakeMetadataService{detailsMovie: metadata.Movie{
		ID: "movie-id", MediaType: metadata.MediaTypeMovie, Title: "Movie", Genres: []metadata.Genre{},
		Cast: []metadata.CastMember{{ID: "1", Name: "First"}, {ID: "2", Name: "Second"}}, ExternalIDs: map[string]string{},
	}}
	api := metadataAPI(service)
	api.settings = &fakeSettingsService{instance: settings.Layer{SchemaVersion: 1, Values: settings.Values{MaximumCastMembers: &serverLimit}}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/titles/movie-id", nil)
	request.SetPathValue("titleId", "movie-id")
	response := httptest.NewRecorder()

	api.movieDetails(response, request, auth.Principal{})

	if response.Code != http.StatusOK {
		t.Fatalf("movie details status = %d: %s", response.Code, response.Body.String())
	}
	var movie metadata.Movie
	decodeResponse(t, response, &movie)
	if len(movie.Cast) != serverLimit || movie.Cast[0].Name != "First" {
		t.Fatalf("server cast limit was not used without an active profile: %+v", movie.Cast)
	}
}

func TestSeriesHandlersPassCanonicalIdentifiers(t *testing.T) {
	service := &fakeMetadataService{
		searchSeriesPage: metadata.SeriesPage{Items: []metadata.Series{}, Page: 1},
		seriesDetailsValue: metadata.Series{
			ID: "series-id", MediaType: metadata.MediaTypeSeries, Name: "Fixture Series",
			Genres: []metadata.Genre{}, Seasons: []metadata.SeasonSummary{}, ExternalIDs: map[string]string{"tmdb": "92001"},
		},
		seasonDetailsValue: metadata.Season{
			ID: "season-id", MediaType: metadata.MediaTypeSeason, SeriesID: "series-id", Name: "Season 1",
			Episodes: []metadata.Episode{}, ExternalIDs: map[string]string{"tmdb": "92011"},
		},
	}
	api := metadataAPI(service)

	searchRequest := httptest.NewRequest(http.MethodGet, "/api/v1/search/series?query=Fixture+Series&language=en-US", nil)
	searchResponse := httptest.NewRecorder()
	api.searchSeries(searchResponse, searchRequest, auth.Principal{})
	if searchResponse.Code != http.StatusOK || service.searchSeriesOptions.Query != "Fixture Series" {
		t.Fatalf("unexpected series search status=%d options=%+v", searchResponse.Code, service.searchSeriesOptions)
	}

	seriesRequest := httptest.NewRequest(http.MethodGet, "/api/v1/series/series-id?language=fr-FR&mappingProvider=tvdb&episodeOrder=4", nil)
	seriesRequest.SetPathValue("titleId", "series-id")
	seriesResponse := httptest.NewRecorder()
	api.seriesDetails(seriesResponse, seriesRequest, auth.Principal{})
	if seriesResponse.Code != http.StatusOK || service.seriesDetailsID != "series-id" || service.seriesDetailsLanguage != "fr-FR" || service.seriesDetailsMapping != "tvdb" || service.seriesDetailsOrder != "4" {
		t.Fatalf("unexpected series details status=%d id=%q language=%q mapping=%q order=%q", seriesResponse.Code, service.seriesDetailsID, service.seriesDetailsLanguage, service.seriesDetailsMapping, service.seriesDetailsOrder)
	}

	seasonRequest := httptest.NewRequest(http.MethodGet, "/api/v1/seasons/season-id?language=fr-FR&mappingProvider=tvdb", nil)
	seasonRequest.SetPathValue("seasonId", "season-id")
	seasonResponse := httptest.NewRecorder()
	api.seasonDetails(seasonResponse, seasonRequest, auth.Principal{})
	if seasonResponse.Code != http.StatusOK || service.seasonDetailsID != "season-id" || service.seasonDetailsLanguage != "fr-FR" || service.seasonDetailsMapping != "tvdb" {
		t.Fatalf("unexpected season details status=%d id=%q language=%q mapping=%q", seasonResponse.Code, service.seasonDetailsID, service.seasonDetailsLanguage, service.seasonDetailsMapping)
	}
}

func TestSeasonDetailsPreservesSpecialsNumberZeroInResponse(t *testing.T) {
	const seasonID = "tvdb:00000000-0000-4000-8000-000000000600:1000"
	service := &fakeMetadataService{seasonDetailsValue: metadata.Season{
		ID: seasonID, MediaType: metadata.MediaTypeSeason,
		SeriesID: "00000000-0000-4000-8000-000000000600",
		Name:     "Specials", SeasonNumber: 0, Episodes: []metadata.Episode{},
		ExternalIDs: map[string]string{"tvdb": "1000"},
	}}
	api := metadataAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/seasons/"+seasonID+"?mappingProvider=tvdb", nil)
	request.SetPathValue("seasonId", seasonID)
	response := httptest.NewRecorder()

	api.seasonDetails(response, request, auth.Principal{})

	if response.Code != http.StatusOK || service.seasonDetailsID != seasonID || service.seasonDetailsMapping != "tvdb" {
		t.Fatalf("unexpected specials route status=%d id=%q mapping=%q", response.Code, service.seasonDetailsID, service.seasonDetailsMapping)
	}
	var body metadata.Season
	decodeResponse(t, response, &body)
	if body.SeasonNumber != 0 || body.Name != "Specials" || body.Episodes == nil || len(body.Episodes) != 0 {
		t.Fatalf("specials route changed numeric season zero: %+v", body)
	}
}

func TestTitleTrailersPassStableTitleIDLanguageAndSeason(t *testing.T) {
	service := &fakeMetadataService{trailersValue: metadata.TrailerList{Trailers: []metadata.Trailer{
		{YouTubeID: "video-id", Name: "Official Trailer", Language: "en-US", IsFallback: true, CaptionPreference: "fr"},
		{YouTubeID: "video-id-2", Name: "Official Trailer 2", Language: "fr-FR"},
	}}}
	api := metadataAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/metadata/titles/title-id/trailers?language=fr-FR&captionLanguage=fr-FR&seasonNumber=3", nil)
	request.SetPathValue("titleId", "title-id")
	response := httptest.NewRecorder()

	api.titleTrailers(response, request, auth.Principal{})

	if response.Code != http.StatusOK || service.trailersID != "title-id" || service.trailersLanguage != "fr-FR" || service.trailersCaptionLanguage != "fr-FR" || service.trailersSeasonNumber == nil || *service.trailersSeasonNumber != 3 {
		t.Fatalf("unexpected trailers response status=%d id=%q language=%q captions=%q season=%v", response.Code, service.trailersID, service.trailersLanguage, service.trailersCaptionLanguage, service.trailersSeasonNumber)
	}
	var body metadata.TrailerList
	decodeResponse(t, response, &body)
	if len(body.Trailers) != 2 || body.Trailers[0].YouTubeID != "video-id" || !body.Trailers[0].IsFallback || body.Trailers[0].CaptionPreference != "fr" {
		t.Fatalf("unexpected trailers body: %+v", body)
	}
}

func TestTitleTrailersOmitSeasonForTitleRequest(t *testing.T) {
	service := &fakeMetadataService{trailersValue: metadata.TrailerList{Trailers: []metadata.Trailer{{YouTubeID: "title-video"}}}}
	api := metadataAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/metadata/titles/title-id/trailers?language=en-US", nil)
	request.SetPathValue("titleId", "title-id")
	response := httptest.NewRecorder()

	api.titleTrailers(response, request, auth.Principal{})

	if response.Code != http.StatusOK || service.trailersSeasonNumber != nil {
		t.Fatalf("unexpected title trailers request status=%d season=%v", response.Code, service.trailersSeasonNumber)
	}
}

func TestTitleTrailersRejectMalformedSeasonNumber(t *testing.T) {
	for _, value := range []string{"", "three", "-1"} {
		t.Run(value, func(t *testing.T) {
			service := &fakeMetadataService{}
			api := metadataAPI(service)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/metadata/titles/title-id/trailers?seasonNumber="+value, nil)
			request.SetPathValue("titleId", "title-id")
			response := httptest.NewRecorder()

			api.titleTrailers(response, request, auth.Principal{})

			if response.Code != http.StatusUnprocessableEntity || service.trailersID != "" {
				t.Fatalf("expected validation rejection, got status=%d service id=%q body=%s", response.Code, service.trailersID, response.Body.String())
			}
		})
	}
}

func TestTitleTrailersReturnNotFound(t *testing.T) {
	service := &fakeMetadataService{trailersErr: metadata.ErrNotFound}
	api := metadataAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/metadata/titles/title-id/trailers", nil)
	request.SetPathValue("titleId", "title-id")
	response := httptest.NewRecorder()

	api.titleTrailers(response, request, auth.Principal{})

	if response.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", response.Code, response.Body.String())
	}
}

func TestTitleTrailersRouteRequiresAuthentication(t *testing.T) {
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{authenticateErr: auth.ErrInvalidToken}
	api.metadata = &fakeMetadataService{}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/metadata/titles/11111111-1111-4111-8111-111111111111/trailers", nil)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("expected bearer 401, got %d and header %q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
}

func TestMetadataErrorsHaveStableHTTPContracts(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "profile required", err: metadata.ErrProfileRequired, status: http.StatusConflict, code: "profile_selection_required"},
		{name: "provider unconfigured", err: metadata.ErrProviderUnavailable, status: http.StatusServiceUnavailable, code: "metadata_provider_unavailable"},
		{name: "provider unauthorized", err: metadata.ErrProviderUnauthorized, status: http.StatusServiceUnavailable, code: "metadata_provider_unavailable"},
		{name: "provider failure", err: metadata.ErrProviderFailure, status: http.StatusBadGateway, code: "metadata_provider_error"},
		{name: "provider rate limited", err: metadata.ErrProviderRateLimited, status: http.StatusServiceUnavailable, code: "metadata_provider_rate_limited"},
		{name: "title absent", err: metadata.ErrNotFound, status: http.StatusNotFound, code: "title_not_found"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeMetadataService{discoverErr: errors.Join(errors.New("wrapped"), test.err)}
			api := metadataAPI(service)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/movies", nil)
			response := httptest.NewRecorder()
			api.discoverMovies(response, request, auth.Principal{})
			if response.Code != test.status {
				t.Fatalf("expected status %d, got %d", test.status, response.Code)
			}
			var body errorEnvelope
			decodeResponse(t, response, &body)
			if body.Error.Code != test.code {
				t.Fatalf("expected code %q, got %q", test.code, body.Error.Code)
			}
		})
	}
}

func TestMetadataProviderErrorsLogWrappedCauseWithoutLeakingIt(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "unavailable", err: metadata.ErrProviderUnavailable, status: http.StatusServiceUnavailable, code: "metadata_provider_unavailable"},
		{name: "unauthorized", err: metadata.ErrProviderUnauthorized, status: http.StatusServiceUnavailable, code: "metadata_provider_unavailable"},
		{name: "rate limited", err: metadata.ErrProviderRateLimited, status: http.StatusServiceUnavailable, code: "metadata_provider_rate_limited"},
		{name: "failure", err: metadata.ErrProviderFailure, status: http.StatusBadGateway, code: "metadata_provider_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			wrapped := fmt.Errorf("TMDB technical cause for %s: %w", test.name, test.err)
			service := &fakeMetadataService{discoverErr: wrapped}
			api := &API{
				metadata: service,
				logger:   slog.New(slog.NewTextHandler(&logs, nil)),
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/movies", nil)
			response := httptest.NewRecorder()

			api.discoverMovies(response, request, auth.Principal{})

			if response.Code != test.status {
				t.Fatalf("expected status %d, got %d: %s", test.status, response.Code, response.Body.String())
			}
			var body errorEnvelope
			decodeResponse(t, response, &body)
			if body.Error.Code != test.code || strings.Contains(body.Error.Message, "technical cause") {
				t.Fatalf("provider details leaked to client: %+v", body)
			}
			output := logs.String()
			if !strings.Contains(output, "metadata provider request failed") ||
				!strings.Contains(output, "operation=\"discover movies\"") ||
				!strings.Contains(output, "TMDB technical cause for "+test.name) {
				t.Fatalf("technical provider cause missing from structured log: %q", output)
			}
		})
	}
}

func metadataAPI(service metadataService) *API {
	return &API{
		metadata: service,
		settings: &fakeSettingsService{instance: settings.Layer{SchemaVersion: 1}},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}
