package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/collection"
)

func TestResolveCollectionSourceSupportsEditorSourceTypes(t *testing.T) {
	id := int64(42)
	tests := []struct {
		name         string
		source       collection.TMDBSource
		wantPath     string
		wantFilter   string
		response     string
		wantTitle    string
		wantCover    string
		wantBackdrop string
	}{
		{
			name: "public list", source: collection.TMDBSource{SourceType: "list", TMDBID: &id, MediaType: collection.MediaTypeMovie, Sort: "original"},
			wantPath: "/list/42", response: `{"items":[{"id":1,"title":"Listed movie","media_type":"movie","overview":"Résumé localisé"}]}`, wantTitle: "Listed movie",
		},
		{
			name: "production company", source: collection.TMDBSource{SourceType: "company", TMDBID: &id, MediaType: collection.MediaTypeMovie, Sort: "popularity.desc"},
			wantPath: "/discover/movie", wantFilter: "with_companies", response: `{"page":1,"total_pages":1,"results":[{"id":2,"title":"Company movie","overview":"Résumé localisé"}]}`, wantTitle: "Company movie",
		},
		{
			name: "network", source: collection.TMDBSource{SourceType: "network", TMDBID: &id, MediaType: collection.MediaTypeSeries, Sort: "popularity.desc"},
			wantPath: "/discover/tv", wantFilter: "with_networks", response: `{"page":1,"total_pages":1,"results":[{"id":3,"name":"Network series","overview":"Résumé localisé"}]}`, wantTitle: "Network series",
		},
		{
			name: "movie collection", source: collection.TMDBSource{SourceType: "collection", TMDBID: &id, MediaType: collection.MediaTypeMovie, Sort: "original"},
			wantPath: "/collection/42", response: `{"poster_path":"/collection-poster.jpg","backdrop_path":"/collection-backdrop.jpg","parts":[{"id":4,"title":"Collection movie","overview":"Résumé localisé"}]}`, wantTitle: "Collection movie",
			wantCover: "https://image.tmdb.org/t/p/w500/collection-poster.jpg", wantBackdrop: "https://image.tmdb.org/t/p/w1280/collection-backdrop.jpg",
		},
		{
			name: "person credits", source: collection.TMDBSource{SourceType: "person", TMDBID: &id, MediaType: collection.MediaTypeMovie, Sort: "original"},
			wantPath: "/person/42/combined_credits", response: `{"cast":[{"id":5,"title":"Person movie","media_type":"movie","overview":"Résumé localisé"}]}`, wantTitle: "Person movie",
		},
		{
			name: "director credits", source: collection.TMDBSource{SourceType: "director", TMDBID: &id, MediaType: collection.MediaTypeMovie, Sort: "original"},
			wantPath: "/person/42/combined_credits", response: `{"crew":[{"id":6,"title":"Directed movie","media_type":"movie","job":"Director","overview":"Résumé localisé"},{"id":7,"title":"Written movie","media_type":"movie","job":"Writer","overview":"Résumé localisé"}]}`, wantTitle: "Directed movie",
		},
		{
			name: "custom discover", source: collection.TMDBSource{SourceType: "discover", MediaType: collection.MediaTypeMovie, Sort: "vote_count.desc", Filters: collection.TMDBFilters{Genres: []int64{18}}},
			wantPath: "/discover/movie", wantFilter: "with_genres", response: `{"page":1,"total_pages":2,"results":[{"id":8,"title":"Discovered movie","overview":"Résumé localisé"}]}`, wantTitle: "Discovered movie",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.wantPath {
					t.Fatalf("path = %q, want %q", r.URL.Path, test.wantPath)
				}
				if r.URL.Query().Get("language") != "fr-FR" {
					t.Fatalf("language was not forwarded: %v", r.URL.Query())
				}
				if test.wantFilter != "" && r.URL.Query().Get(test.wantFilter) == "" {
					t.Fatalf("expected filter %q in %v", test.wantFilter, r.URL.Query())
				}
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()

			client := newWithBaseURL("token", server.URL, server.Client())
			page, err := client.ResolveCollectionSource(context.Background(), test.source, 1, "fr-FR", "FR")
			if err != nil {
				t.Fatalf("resolve source: %v", err)
			}
			if len(page.Items) != 1 || page.Items[0].Title != test.wantTitle {
				t.Fatalf("unexpected resolved page: %+v", page)
			}
			if page.CoverImageURL != test.wantCover || page.HeroBackdropURL != test.wantBackdrop {
				t.Fatalf("artwork = (%q, %q), want (%q, %q)", page.CoverImageURL, page.HeroBackdropURL, test.wantCover, test.wantBackdrop)
			}
		})
	}
}

func TestCollectionEditorLookupsUseTMDBSearchEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/company" || r.URL.Query().Get("query") != "Pixar" || r.URL.Query().Get("page") != "2" {
			t.Fatalf("unexpected lookup request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"results":[{"id":3,"name":"Pixar","logo_path":"/pixar.png"}]}`))
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	values, err := client.LookupCollectionSource(context.Background(), "company", "Pixar", "fr-FR", 2)
	if err != nil {
		t.Fatalf("lookup collection source: %v", err)
	}
	if len(values) != 1 || values[0].ID != 3 || values[0].ImageURL != "https://image.tmdb.org/t/p/w500/pixar.png" {
		t.Fatalf("unexpected lookup values: %+v", values)
	}
}

func TestResolveCustomDiscoverBothCombinesMoviesAndSeriesWithFilters(t *testing.T) {
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Path
		query := r.URL.Query()
		for key, want := range map[string]string{
			"with_genres":            "28|12",
			"with_keywords":          "9715",
			"with_companies":         "420",
			"vote_average.gte":       "7",
			"vote_average.lte":       "10",
			"vote_count.gte":         "100",
			"with_original_language": "en",
			"with_origin_country":    "US",
		} {
			if query.Get(key) != want {
				t.Errorf("%s = %q, want %q", key, query.Get(key), want)
			}
		}
		switch r.URL.Path {
		case "/discover/movie":
			if query.Get("primary_release_date.gte") != "2020-01-01" || query.Get("primary_release_date.lte") != "2024-12-31" || query.Get("year") != "2024" {
				t.Errorf("movie date filters were not forwarded: %v", query)
			}
			_, _ = w.Write([]byte(`{"page":1,"total_pages":2,"results":[{"id":42,"title":"Filtered movie","popularity":20}]}`))
		case "/discover/tv":
			if query.Get("first_air_date.gte") != "2020-01-01" || query.Get("first_air_date.lte") != "2024-12-31" || query.Get("first_air_date_year") != "2024" || query.Get("with_networks") != "213" {
				t.Errorf("series filters were not forwarded: %v", query)
			}
			_, _ = w.Write([]byte(`{"page":1,"total_pages":1,"results":[{"id":42,"name":"Filtered series","popularity":10}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ratingMin, ratingMax, votes, year := 7.0, 10.0, 100, 2024
	client := newWithBaseURL("token", server.URL, server.Client())
	page, err := client.ResolveCollectionSource(context.Background(), collection.TMDBSource{
		SourceType: "discover", MediaType: collection.MediaTypeBoth, Sort: "popularity.desc",
		Filters: collection.TMDBFilters{
			Genres: []int64{28, 12}, ReleaseDateFrom: "2020-01-01", ReleaseDateTo: "2024-12-31",
			VoteAverageMin: &ratingMin, VoteAverageMax: &ratingMax, VoteCountMin: &votes,
			OriginalLanguage: "en", OriginCountry: "US", Keywords: []int64{9715},
			Companies: []int64{420}, Networks: []int64{213}, Year: &year,
		},
	}, 1, "en-US", "US")
	if err != nil {
		t.Fatalf("resolve both discover: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("TMDB request count = %d, want 2", len(requests))
	}
	if len(page.Items) != 2 || page.Items[0].MediaType != collection.MediaTypeMovie || page.Items[1].MediaType != collection.MediaTypeSeries || !page.HasMore {
		t.Fatalf("unexpected combined page: %+v", page)
	}
}

func TestResolveBothSupportsMixedTMDBSourceTypes(t *testing.T) {
	id := int64(42)
	tests := []struct {
		name       string
		sourceType string
		wantPaths  map[string]bool
		wantMovie  bool
		wantSeries bool
	}{
		{name: "production company", sourceType: "company", wantPaths: map[string]bool{"/discover/movie": true, "/discover/tv": true}, wantMovie: true, wantSeries: true},
		{name: "person credits", sourceType: "person", wantPaths: map[string]bool{"/person/42/combined_credits": true}, wantMovie: true, wantSeries: true},
		{name: "director credits", sourceType: "director", wantPaths: map[string]bool{"/person/42/combined_credits": true}, wantMovie: true, wantSeries: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := make(chan string, len(test.wantPaths))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !test.wantPaths[r.URL.Path] {
					t.Errorf("unexpected TMDB path %q", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				requests <- r.URL.Path
				switch r.URL.Path {
				case "/discover/movie":
					_, _ = w.Write([]byte(`{"page":1,"total_pages":1,"results":[{"id":1,"title":"Movie"}]}`))
				case "/discover/tv":
					_, _ = w.Write([]byte(`{"page":1,"total_pages":1,"results":[{"id":2,"name":"Series"}]}`))
				case "/person/42/combined_credits":
					_, _ = w.Write([]byte(`{"cast":[{"id":1,"title":"Movie","media_type":"movie"},{"id":2,"name":"Series","media_type":"tv"}],"crew":[{"id":1,"title":"Movie","media_type":"movie","job":"Director"},{"id":2,"name":"Series","media_type":"tv","job":"Director"}]}`))
				}
			}))
			defer server.Close()

			client := newWithBaseURL("token", server.URL, server.Client())
			page, err := client.ResolveCollectionSource(context.Background(), collection.TMDBSource{
				SourceType: test.sourceType, TMDBID: &id, MediaType: collection.MediaTypeBoth, Sort: "popularity.desc",
			}, 1, "en-US", "US")
			if err != nil {
				t.Fatalf("resolve both: %v", err)
			}
			if len(requests) != len(test.wantPaths) {
				t.Fatalf("TMDB request count = %d, want %d", len(requests), len(test.wantPaths))
			}
			gotMovie, gotSeries := false, false
			for _, item := range page.Items {
				gotMovie = gotMovie || item.MediaType == collection.MediaTypeMovie
				gotSeries = gotSeries || item.MediaType == collection.MediaTypeSeries
			}
			if gotMovie != test.wantMovie || gotSeries != test.wantSeries {
				t.Fatalf("resolved media types movie=%t series=%t, want movie=%t series=%t: %+v", gotMovie, gotSeries, test.wantMovie, test.wantSeries, page.Items)
			}
		})
	}
}

func TestResolveCollectionSourceFillsOnlyBlankLocalizedDescriptions(t *testing.T) {
	requests := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		if r.URL.Path != "/discover/movie" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		switch r.URL.Query().Get("language") {
		case "fr-FR":
			_, _ = w.Write([]byte(`{"page":1,"total_pages":1,"results":[{"id":1,"title":"Film un","overview":"  "},{"id":2,"title":"Film deux","overview":"Résumé français"}]}`))
		case englishCollectionLanguage:
			_, _ = w.Write([]byte(`{"page":1,"total_pages":1,"results":[{"id":2,"title":"Movie two","overview":"English two"},{"id":1,"title":"Movie one","overview":"English one"}]}`))
		default:
			t.Fatalf("unexpected language %q", r.URL.Query().Get("language"))
		}
	}))
	defer server.Close()

	client := newWithBaseURL("token", server.URL, server.Client())
	page, err := client.ResolveCollectionSource(context.Background(), collection.TMDBSource{
		SourceType: "discover", MediaType: collection.MediaTypeMovie, Sort: "original",
	}, 1, "fr-FR", "FR")
	if err != nil {
		t.Fatalf("resolve source: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("TMDB request count = %d, want 2", len(requests))
	}
	if len(page.Items) != 2 || page.Items[0].Title != "Film un" || page.Items[0].Description != "English one" {
		t.Fatalf("blank localized description was not filled by stable identity: %+v", page.Items)
	}
	if page.Items[1].Title != "Film deux" || page.Items[1].Description != "Résumé français" {
		t.Fatalf("localized item fields were replaced: %+v", page.Items[1])
	}
}

func TestResolveCollectionSourceKeepsLocalizedPageWhenEnglishFallbackFails(t *testing.T) {
	requests := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		if r.URL.Query().Get("language") == englishCollectionLanguage {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":1,"title":"Film français","media_type":"movie","overview":""}]}`))
	}))
	defer server.Close()

	id := int64(42)
	client := newWithBaseURL("token", server.URL, server.Client())
	page, err := client.ResolveCollectionSource(context.Background(), collection.TMDBSource{
		SourceType: "list", TMDBID: &id, MediaType: collection.MediaTypeMovie, Sort: "original",
	}, 1, "fr-FR", "FR")
	if err != nil {
		t.Fatalf("localized result should remain usable: %v", err)
	}
	if len(requests) != 2 || len(page.Items) != 1 || page.Items[0].Title != "Film français" || page.Items[0].Description != "" {
		t.Fatalf("requests = %d, page = %+v", len(requests), page)
	}
}

func TestResolveCollectionSourceAccountsTMDBPayloadBeforeNormalization(t *testing.T) {
	id := int64(42)
	source := collection.TMDBSource{
		SourceType: "list", TMDBID: &id, MediaType: collection.MediaTypeMovie, Sort: "original",
	}
	tests := []struct {
		name      string
		byteLimit int64
		itemLimit int64
		response  string
	}{
		{
			name: "bytes", byteLimit: 8, itemLimit: 10,
			response: `{"items":[{"id":1,"title":"First","media_type":"movie"}]}`,
		},
		{
			name: "items", byteLimit: 1 << 20, itemLimit: 1,
			response: `{"items":[{"id":1,"title":"First","media_type":"movie"},{"id":2,"title":"Second","media_type":"movie"}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()

			ctx, budget := addon.WithPayloadBudget(context.Background(), test.byteLimit, test.itemLimit)
			defer budget.Cancel()
			sourceCtx := addon.WithPayloadBudgetSource(ctx)
			client := newWithBaseURL("token", server.URL, server.Client())
			if _, err := client.ResolveCollectionSource(sourceCtx, source, 1, "en-US", "US"); err == nil {
				t.Fatal("payload over the request budget was accepted")
			}
			if !budget.Exceeded() {
				t.Fatal("payload over the request budget did not mark it exceeded")
			}
			select {
			case <-ctx.Done():
			default:
				t.Fatal("payload over the request budget did not cancel the source context")
			}
		})
	}
}
