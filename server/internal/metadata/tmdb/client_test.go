package tmdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moodiness/rivune/server/internal/metadata"
)

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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		_, _ = w.Write([]byte(`{"page":2,"results":[{"id":78,"title":"Blade Runner","original_title":"Blade Runner","original_language":"en","overview":"A replicant hunter.","release_date":"1982-06-25","poster_path":"/poster.jpg","backdrop_path":"/backdrop.jpg","vote_average":7.9,"vote_count":14000}],"total_pages":3,"total_results":41}`))
	}))
	defer server.Close()

	client := newWithBaseURL("Bearer api-read-token", server.URL, server.Client())
	page, err := client.SearchMovies(context.Background(), metadata.SearchOptions{
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
}

func TestMovieDetailsNormalizesArtworkCastAndIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movie/550" || r.URL.Query().Get("language") != "en-US" || r.URL.Query().Get("append_to_response") != "credits" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"id":550,"title":"Fight Club","original_title":"Fight Club","original_language":"en","overview":"Overview","release_date":"1999-10-15","poster_path":"/fight-club-poster.jpg","backdrop_path":"/fight-club-backdrop.jpg","tagline":"Mischief. Mayhem. Soap.","runtime":139,"genres":[{"id":18,"name":"Drama"}],"vote_average":8.4,"vote_count":30000,"imdb_id":"tt0137523","credits":{"cast":[{"id":819,"name":"Edward Norton","character":"The Narrator","profile_path":"/norton.jpg"}]}}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	movie, err := client.MovieDetails(context.Background(), "550", "en-US")
	if err != nil {
		t.Fatalf("movie details: %v", err)
	}
	if movie.RuntimeMinutes != 139 || len(movie.Genres) != 1 || movie.Genres[0].Name != "Drama" || movie.AdditionalIDs["imdb"] != "tt0137523" || movie.PosterURL != imageBaseURL+"/w780/fight-club-poster.jpg" || movie.BackdropURL != imageBaseURL+"/original/fight-club-backdrop.jpg" || len(movie.Cast) != 1 || movie.Cast[0].Character != "The Narrator" || movie.Cast[0].ProfileURL != imageBaseURL+"/w185/norton.jpg" {
		t.Fatalf("unexpected details: %+v", movie)
	}
}

func TestProviderStatusErrorsAreTyped(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{status: http.StatusUnauthorized, want: metadata.ErrProviderUnauthorized},
		{status: http.StatusNotFound, want: metadata.ErrProviderNotFound},
		{status: http.StatusTooManyRequests, want: metadata.ErrProviderRateLimited},
		{status: http.StatusInternalServerError, want: metadata.ErrProviderFailure},
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
		})
	}
}

func TestSearchSeriesNormalizesTelevisionResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/tv" || r.URL.Query().Get("query") != "Breaking Bad" || r.URL.Query().Get("include_adult") != "false" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"page":1,"results":[{"id":1396,"name":"Breaking Bad","original_name":"Breaking Bad","original_language":"en","overview":"A chemistry teacher.","first_air_date":"2008-01-20","poster_path":"/poster.jpg","backdrop_path":"/backdrop.jpg","vote_average":8.9,"vote_count":16000}],"total_pages":1,"total_results":1}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	page, err := client.SearchSeries(context.Background(), metadata.SearchOptions{
		QueryOptions: metadata.QueryOptions{Page: 1, Language: "en-US"},
		Query:        "Breaking Bad",
	})
	if err != nil {
		t.Fatalf("search series: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ExternalID != "1396" || page.Items[0].Name != "Breaking Bad" || page.Items[0].PosterURL != imageBaseURL+"/w780/poster.jpg" || page.Items[0].BackdropURL != imageBaseURL+"/original/backdrop.jpg" {
		t.Fatalf("unexpected series page: %+v", page)
	}
}

func TestResolveExternalIDFindsSeriesByIMDBID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/find/tt0903747" || r.URL.Query().Get("external_source") != "imdb_id" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"movie_results":[],"tv_results":[{"id":1396,"name":"Breaking Bad"}]}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	resolved, err := client.ResolveExternalID(context.Background(), metadata.MediaTypeSeries, "imdb", "tt0903747")
	if err != nil {
		t.Fatalf("resolve external ID: %v", err)
	}
	if resolved != "1396" {
		t.Fatalf("unexpected resolved ID %q", resolved)
	}
}

func TestSeriesDetailsNormalizesArtworkSeasonsCastAndIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/1396" || r.URL.Query().Get("append_to_response") != "external_ids,credits" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"id":1396,"name":"Breaking Bad","original_name":"Breaking Bad","original_language":"en","overview":"A chemistry teacher.","first_air_date":"2008-01-20","last_air_date":"2013-09-29","poster_path":"/breaking-bad-poster.jpg","backdrop_path":"/breaking-bad-backdrop.jpg","number_of_seasons":5,"number_of_episodes":62,"genres":[{"id":18,"name":"Drama"}],"seasons":[{"id":3572,"name":"Season 1","season_number":1,"episode_count":7,"air_date":"2008-01-20","poster_path":"/season-one-poster.jpg","backdrop_path":"/season-one-backdrop.jpg","vote_average":8.3}],"external_ids":{"imdb_id":"tt0903747","tvdb_id":81189,"wikidata_id":"Q1079"},"credits":{"cast":[{"id":17419,"name":"Bryan Cranston","profile_path":"/cranston.jpg","character":"Walter White"}]}}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	series, err := client.SeriesDetails(context.Background(), "1396", "en-US")
	if err != nil {
		t.Fatalf("series details: %v", err)
	}
	if series.NumberOfEpisodes != 62 || series.PosterURL != imageBaseURL+"/w780/breaking-bad-poster.jpg" || series.BackdropURL != imageBaseURL+"/original/breaking-bad-backdrop.jpg" || len(series.Seasons) != 1 || series.Seasons[0].ExternalID != "3572" || series.Seasons[0].PosterURL != imageBaseURL+"/w780/season-one-poster.jpg" || series.Seasons[0].BackdropURL != imageBaseURL+"/original/season-one-backdrop.jpg" || series.AdditionalIDs["tvdb"] != "81189" || len(series.Cast) != 1 || series.Cast[0].Character != "Walter White" || series.Cast[0].ProfileURL != imageBaseURL+"/w185/cranston.jpg" {
		t.Fatalf("unexpected series: %+v", series)
	}
}

func TestSeasonDetailsNormalizesOriginalEpisodeArtwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/1396/season/1" || r.URL.Query().Get("language") != "en-US" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"id":3572,"name":"Season 1","season_number":1,"air_date":"2008-01-20","poster_path":"/season-one-poster.jpg","backdrop_path":"/season-one-backdrop.jpg","episodes":[{"id":62085,"name":"Pilot","overview":"Walter receives a diagnosis.","season_number":1,"episode_number":1,"air_date":"2008-01-20","still_path":"/still.jpg","runtime":59,"vote_average":8.2,"vote_count":4000}]}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	season, err := client.SeasonDetails(context.Background(), "1396", 1, "en-US")
	if err != nil {
		t.Fatalf("season details: %v", err)
	}
	if season.ExternalID != "3572" || season.PosterURL != imageBaseURL+"/w780/season-one-poster.jpg" || season.BackdropURL != imageBaseURL+"/original/season-one-backdrop.jpg" || len(season.Episodes) != 1 || season.Episodes[0].ExternalID != "62085" || season.Episodes[0].StillURL != imageBaseURL+"/original/still.jpg" || season.Episodes[0].BackdropURL != imageBaseURL+"/original/still.jpg" {
		t.Fatalf("unexpected season: %+v", season)
	}
}

func TestSeasonDetailsPreservesSpecialSeasonZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/1396/season/0" || r.URL.Query().Get("language") != "en-US" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"id":3571,"name":"Specials","season_number":0,"episodes":[{"id":62084,"name":"Behind the Scenes","season_number":0,"episode_number":1}]}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	season, err := client.SeasonDetails(context.Background(), "1396", 0, "en-US")
	if err != nil {
		t.Fatalf("season zero details: %v", err)
	}
	if season.SeasonNumber != 0 || len(season.Episodes) != 1 || season.Episodes[0].SeasonNumber != 0 {
		t.Fatalf("season zero hierarchy was not preserved: %+v", season)
	}
}

func TestSeasonDetailsRejectsMismatchedSeasonHierarchy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/72879/season/9" || r.URL.Query().Get("language") != "fr-FR" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"id":475463,"name":"Saison 9","season_number":2,"episodes":[{"id":500001,"name":"Épisode 2021","season_number":2,"episode_number":2021},{"id":500002,"name":"Épisode 2022","season_number":2,"episode_number":2022}]}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	season, err := client.SeasonDetails(context.Background(), "72879", 9, "fr-FR")
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
		{name: "movie", mediaType: metadata.MediaTypeMovie, externalID: "550", wantPath: "/movie/550/videos"},
		{name: "series", mediaType: metadata.MediaTypeSeries, externalID: "1396", wantPath: "/tv/1396/videos"},
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
		if r.URL.Path != "/tv/1396/season/3/videos" || r.URL.Query().Get("language") != "fr-FR" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"results":[{"key":"season-three","site":"YouTube","type":"Trailer"}]}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	seasonNumber := 3
	trailers, err := client.Trailers(context.Background(), metadata.MediaTypeSeries, "1396", "fr-FR", &seasonNumber)
	if err != nil {
		t.Fatalf("season trailers: %v", err)
	}
	if len(trailers) != 1 || trailers[0].YouTubeID != "season-three" {
		t.Fatalf("unexpected season trailers: %+v", trailers)
	}
}
