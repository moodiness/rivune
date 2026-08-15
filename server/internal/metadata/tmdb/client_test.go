package tmdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/requestwork"
)

func TestProductionClientRejectsCrossOriginRedirect(t *testing.T) {
	client := New("test-token", nil)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://127.0.0.1/latest/meta-data", nil)
	if err != nil {
		t.Fatalf("create redirect request: %v", err)
	}
	if err := client.httpClient.CheckRedirect(request, nil); err == nil {
		t.Fatal("TMDB production client accepted a loopback redirect")
	}
}

func TestNormalizeCastRetainsUpToOneHundredUniqueMembers(t *testing.T) {
	cast := make([]castMemberResponse, 0, 104)
	cast = append(cast,
		castMemberResponse{ID: 0, Name: "Invalid"},
		castMemberResponse{ID: 1, Name: "First"},
		castMemberResponse{ID: 1, Name: "Duplicate"},
	)
	for id := int64(2); id <= 102; id++ {
		cast = append(cast, castMemberResponse{ID: id, Name: fmt.Sprintf("Person %d", id)})
	}

	members := normalizeCast(cast)

	if len(members) != 100 {
		t.Fatalf("normalized cast length = %d, want 100", len(members))
	}
	if members[0].ID != "1" || members[0].Name != "First" || members[99].ID != "100" {
		t.Fatalf("normalized cast did not preserve the first 100 unique valid members: first=%+v last=%+v", members[0], members[99])
	}
}

func TestSearchMoviesSendsBearerTokenAndNormalizesResults(t *testing.T) {
	const responsePayload = `{"page":2,"results":[{"id":78,"title":"Blade Runner","original_title":"Blade Runner","original_language":"en","overview":"A replicant hunter.","release_date":"1982-06-25","poster_path":"/poster.jpg","backdrop_path":"/backdrop.jpg","vote_average":7.9,"vote_count":14000}],"total_pages":3,"total_results":41}`
	const requestID = "provider-correlation-7"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(requestwork.RequestIDHeader) != requestID {
			t.Fatalf("outbound request ID = %q, want %q", r.Header.Get(requestwork.RequestIDHeader), requestID)
		}
		if r.URL.Path != "/search/movie" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer api-read-token" {
			t.Fatalf("unexpected authorization header %q", r.Header.Get("Authorization"))
		}
		query := r.URL.Query()
		if query.Get("query") != "Blade Runner" || query.Get("language") != "fr-FR" || query.Get("region") != "FR" || query.Get("include_adult") != "false" {
			t.Fatalf("unexpected query: %v", query)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responsePayload))
	}))
	defer server.Close()

	client := newWithBaseURL("Bearer api-read-token", server.URL, server.Client())
	ctx, counters := requestwork.WithCounters(context.Background())
	ctx, _ = requestwork.WithRequestID(ctx, requestID)
	page, err := client.SearchMovies(ctx, metadata.SearchOptions{
		QueryOptions: metadata.QueryOptions{Page: 2, Language: "fr-FR", Region: "FR"},
		Query:        "Blade Runner",
	})
	if err != nil {
		t.Fatalf("search movies: %v", err)
	}
	if page.Page != 2 || page.TotalPages != 3 || page.TotalResults != 41 || len(page.Items) != 1 {
		t.Fatalf("unexpected page: %+v", page)
	}
	movie := page.Items[0]
	if movie.ExternalID != "78" || movie.PosterURL != imageBaseURL+"/w780/poster.jpg" || movie.BackdropURL != imageBaseURL+"/original/backdrop.jpg" {
		t.Fatalf("unexpected normalized movie: %+v", movie)
	}
	snapshot := counters.Snapshot()
	if snapshot.OutboundCalls != 1 || snapshot.UpstreamBytes != int64(len(responsePayload)) || snapshot.OutboundDuration <= 0 {
		t.Fatalf("outbound snapshot = %+v, want one completed call and %d bytes", snapshot, len(responsePayload))
	}
}

func TestMovieDetailsNormalizesArtworkCastAndIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movie/900201" || r.URL.Query().Get("language") != "en-US" || r.URL.Query().Get("append_to_response") != "credits" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"id":900201,"title":"Fixture Movie","original_title":"Fixture Movie","original_language":"en","overview":"Fixture overview","release_date":"2024-01-01","poster_path":"/fixture-movie-poster.jpg","backdrop_path":"/fixture-movie-backdrop.jpg","tagline":"Fixture tagline.","runtime":120,"genres":[{"id":18,"name":"Drama"}],"vote_average":8.4,"vote_count":30000,"imdb_id":"tt9000201","credits":{"cast":[{"id":9301,"name":"Fixture Performer","character":"Fixture Character","profile_path":"/fixture-performer.jpg"}]}}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	movie, err := client.MovieDetails(context.Background(), "900201", "en-US")
	if err != nil {
		t.Fatalf("movie details: %v", err)
	}
	if movie.RuntimeMinutes != 120 || len(movie.Genres) != 1 || movie.Genres[0].Name != "Drama" || movie.AdditionalIDs["imdb"] != "tt9000201" || movie.PosterURL != imageBaseURL+"/w780/fixture-movie-poster.jpg" || movie.BackdropURL != imageBaseURL+"/original/fixture-movie-backdrop.jpg" || len(movie.Cast) != 1 || movie.Cast[0].Character != "Fixture Character" || movie.Cast[0].ProfileURL != imageBaseURL+"/w185/fixture-performer.jpg" {
		t.Fatalf("unexpected details: %+v", movie)
	}
}

func TestProviderStatusErrorsAreTyped(t *testing.T) {
	tests := []struct {
		status        int
		want          error
		wantTemporary bool
	}{
		{status: http.StatusUnauthorized, want: metadata.ErrProviderUnauthorized},
		{status: http.StatusNotFound, want: metadata.ErrProviderNotFound},
		{status: http.StatusTooManyRequests, want: metadata.ErrProviderRateLimited, wantTemporary: true},
		{status: http.StatusInternalServerError, want: metadata.ErrProviderFailure, wantTemporary: true},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer server.Close()
			client := newWithBaseURL("token", server.URL, server.Client())
			_, err := client.DiscoverMovies(context.Background(), metadata.QueryOptions{Page: 1, Language: "en-US"})
			if !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
			status, temporary, resource := metadata.ProviderErrorDetails(err)
			if status != test.status || temporary != test.wantTemporary || resource != "/discover/movie?include_adult=false&include_video=false&language=en-US&page=1&sort_by=popularity.desc" {
				t.Fatalf("unexpected provider error details: status=%d temporary=%t resource=%q", status, temporary, resource)
			}
		})
	}
}

func TestSearchSeriesNormalizesTelevisionResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/tv" || r.URL.Query().Get("query") != "Fixture Series" || r.URL.Query().Get("include_adult") != "false" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"page":1,"results":[{"id":92001,"name":"Fixture Series","original_name":"Fixture Series","original_language":"en","overview":"Fixture series overview.","first_air_date":"2024-01-07","poster_path":"/poster.jpg","backdrop_path":"/backdrop.jpg","vote_average":7.5,"vote_count":42}],"total_pages":1,"total_results":1}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	page, err := client.SearchSeries(context.Background(), metadata.SearchOptions{
		QueryOptions: metadata.QueryOptions{Page: 1, Language: "en-US"},
		Query:        "Fixture Series",
	})
	if err != nil {
		t.Fatalf("search series: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ExternalID != "92001" || page.Items[0].Name != "Fixture Series" || page.Items[0].PosterURL != imageBaseURL+"/w780/poster.jpg" || page.Items[0].BackdropURL != imageBaseURL+"/original/backdrop.jpg" {
		t.Fatalf("unexpected series page: %+v", page)
	}
}

func TestResolveExternalIDFindsSeriesByIMDBID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/find/tt9002001" || r.URL.Query().Get("external_source") != "imdb_id" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"movie_results":[],"tv_results":[{"id":92001,"name":"Fixture Series"}]}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	resolved, err := client.ResolveExternalID(context.Background(), metadata.MediaTypeSeries, "imdb", "tt9002001")
	if err != nil {
		t.Fatalf("resolve external ID: %v", err)
	}
	if resolved != "92001" {
		t.Fatalf("unexpected resolved ID %q", resolved)
	}
}

func TestResolveExternalIDClassifiesEmptySuccessfulLookupAsPermanentNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"movie_results":[],"tv_results":[]}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	_, err := client.ResolveExternalID(context.Background(), metadata.MediaTypeSeries, "imdb", "tt0000000")
	if !errors.Is(err, metadata.ErrProviderNotFound) {
		t.Fatalf("expected provider not found, got %v", err)
	}
	status, temporary, resource := metadata.ProviderErrorDetails(err)
	if status != http.StatusOK || temporary || resource != "/find/tt0000000?external_source=imdb_id" {
		t.Fatalf("unexpected empty lookup classification: status=%d temporary=%t resource=%q", status, temporary, resource)
	}
}

func TestSeriesDetailsNormalizesArtworkSeasonsCastAndIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/92001" || r.URL.Query().Get("append_to_response") != "external_ids,credits" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"id":92001,"name":"Fixture Series","original_name":"Fixture Series","original_language":"en","overview":"Fixture series overview.","first_air_date":"2024-01-07","last_air_date":"2025-02-02","poster_path":"/fixture-series-poster.jpg","backdrop_path":"/fixture-series-backdrop.jpg","number_of_seasons":2,"number_of_episodes":12,"genres":[{"id":18,"name":"Drama"}],"seasons":[{"id":92011,"name":"Season 1","season_number":1,"episode_count":6,"air_date":"2024-01-07","poster_path":"/season-one-poster.jpg","backdrop_path":"/season-one-backdrop.jpg","vote_average":7.3}],"external_ids":{"imdb_id":"tt9002001","tvdb_id":93001,"wikidata_id":"Q9002001"},"credits":{"cast":[{"id":94001,"name":"Fixture Performer","profile_path":"/fixture-performer.jpg","character":"Fixture Performer"}]}}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	series, err := client.SeriesDetails(context.Background(), "92001", "en-US")
	if err != nil {
		t.Fatalf("series details: %v", err)
	}
	if series.NumberOfEpisodes != 12 || series.PosterURL != imageBaseURL+"/w780/fixture-series-poster.jpg" || series.BackdropURL != imageBaseURL+"/original/fixture-series-backdrop.jpg" || len(series.Seasons) != 1 || series.Seasons[0].ExternalID != "92011" || series.Seasons[0].PosterURL != imageBaseURL+"/w780/season-one-poster.jpg" || series.Seasons[0].BackdropURL != imageBaseURL+"/original/season-one-backdrop.jpg" || series.AdditionalIDs["tvdb"] != "93001" || len(series.Cast) != 1 || series.Cast[0].Character != "Fixture Performer" || series.Cast[0].ProfileURL != imageBaseURL+"/w185/fixture-performer.jpg" {
		t.Fatalf("unexpected series: %+v", series)
	}
}

func TestSeasonDetailsNormalizesOriginalEpisodeArtwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/92001/season/1" || r.URL.Query().Get("language") != "en-US" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"id":92011,"name":"Season 1","season_number":1,"air_date":"2024-01-07","poster_path":"/season-one-poster.jpg","backdrop_path":"/season-one-backdrop.jpg","episodes":[{"id":9201101,"name":"Fixture Episode","overview":"Fixture episode overview.","season_number":1,"episode_number":1,"air_date":"2024-01-07","still_path":"/still.jpg","runtime":59,"vote_average":8.2,"vote_count":40}]}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	season, err := client.SeasonDetails(context.Background(), "92001", 1, "en-US")
	if err != nil {
		t.Fatalf("season details: %v", err)
	}
	if season.ExternalID != "92011" || season.PosterURL != imageBaseURL+"/w780/season-one-poster.jpg" || season.BackdropURL != imageBaseURL+"/original/season-one-backdrop.jpg" || len(season.Episodes) != 1 || season.Episodes[0].ExternalID != "9201101" || season.Episodes[0].StillURL != imageBaseURL+"/original/still.jpg" || season.Episodes[0].BackdropURL != imageBaseURL+"/original/still.jpg" {
		t.Fatalf("unexpected season: %+v", season)
	}
}

func TestSeasonDetailsPreservesSpecialSeasonZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/92001/season/0" || r.URL.Query().Get("language") != "en-US" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"id":92010,"name":"Specials","season_number":0,"episodes":[{"id":9201001,"name":"Fixture Episode","season_number":0,"episode_number":1}]}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	season, err := client.SeasonDetails(context.Background(), "92001", 0, "en-US")
	if err != nil {
		t.Fatalf("season zero details: %v", err)
	}
	if season.SeasonNumber != 0 || len(season.Episodes) != 1 || season.Episodes[0].SeasonNumber != 0 {
		t.Fatalf("season zero hierarchy was not preserved: %+v", season)
	}
}

func TestSeasonDetailsRejectsMismatchedSeasonHierarchy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/900202/season/9" || r.URL.Query().Get("language") != "fr-FR" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"id":910209,"name":"Season 9","season_number":2,"episodes":[{"id":920201,"name":"Fixture Episode 2021","season_number":2,"episode_number":2021},{"id":920202,"name":"Fixture Episode 2022","season_number":2,"episode_number":2022}]}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	season, err := client.SeasonDetails(context.Background(), "900202", 9, "fr-FR")
	if !errors.Is(err, metadata.ErrProviderFailure) {
		t.Fatalf("expected provider failure for mismatched season, got season=%+v err=%v", season, err)
	}
}

func TestTrailersUsesMediaSpecificVideosPathAndLanguage(t *testing.T) {
	tests := []struct {
		name       string
		mediaType  string
		externalID string
		wantPath   string
	}{
		{name: "movie", mediaType: metadata.MediaTypeMovie, externalID: "900201", wantPath: "/movie/900201/videos"},
		{name: "series", mediaType: metadata.MediaTypeSeries, externalID: "92001", wantPath: "/tv/92001/videos"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.wantPath || r.URL.Query().Get("language") != "fr-FR" {
					t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
				}
				_, _ = w.Write([]byte(`{"results":[{"iso_639_1":"fr","key":"youtube-id","name":"Bande-annonce","site":"YouTube","type":"Trailer","official":true,"published_at":"2024-03-02T12:30:00.000Z"}]}`))
			}))
			defer server.Close()

			client := newWithBaseURL("token", server.URL, server.Client())
			trailers, err := client.Trailers(context.Background(), test.mediaType, test.externalID, "fr-FR", nil)
			if err != nil {
				t.Fatalf("trailers: %v", err)
			}
			if len(trailers) != 1 || trailers[0].YouTubeID != "youtube-id" || !trailers[0].Official || trailers[0].PublishedAt.IsZero() {
				t.Fatalf("unexpected trailers: %+v", trailers)
			}
		})
	}
}

func TestTrailersUsesSeriesSeasonVideosPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/92001/season/3/videos" || r.URL.Query().Get("language") != "fr-FR" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"results":[{"key":"season-three","site":"YouTube","type":"Trailer"}]}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	seasonNumber := 3
	trailers, err := client.Trailers(context.Background(), metadata.MediaTypeSeries, "92001", "fr-FR", &seasonNumber)
	if err != nil {
		t.Fatalf("season trailers: %v", err)
	}
	if len(trailers) != 1 || trailers[0].YouTubeID != "season-three" {
		t.Fatalf("unexpected season trailers: %+v", trailers)
	}
}
