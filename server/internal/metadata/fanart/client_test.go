package fanart

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moodiness/rivune/server/internal/metadata"
)

func TestEnrichMovieAuthenticatesAndSelectsHighestQualityArtwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movies/550" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("api-key") != "project-key" || r.Header.Get("client-key") != "personal-key" {
			t.Fatalf("unexpected Fanart credentials: api=%q client=%q", r.Header.Get("api-key"), r.Header.Get("client-key"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"movieposter":[
				{"url":"http://images.example/insecure.jpg","lang":"fr","likes":"999","width":"1000","height":"1500"},
				{"url":"https://images.example/poster-neutral.jpg","lang":"00","likes":"8","width":"1000","height":"1500"}
			],
			"moviebackground":[{"url":"https://images.example/background.jpg","lang":"00","likes":"12","width":"1920","height":"1080"}],
			"hdmovielogo":[
				{"url":"https://images.example/logo-en.png","lang":"en","likes":"100","width":"800","height":"310"},
				{"url":"https://images.example/logo-fr.png","lang":"fr","likes":"1","width":"800","height":"310"}
			],
			"movielogo":[{"url":"https://images.example/logo-fr.png","lang":"fr","likes":"1","width":"400","height":"155"}]
		}`))
	}))
	defer server.Close()

	client := newWithBaseURL(" project-key ", " personal-key ", server.URL, server.Client())
	enriched, err := client.EnrichMovie(context.Background(), metadata.ProviderMovie{
		ExternalID: "550",
		PosterURL:  "https://image.tmdb.org/original-poster.jpg",
	}, "fr-FR")
	if err != nil {
		t.Fatalf("enrich movie: %v", err)
	}
	if enriched.PosterURL != "https://images.example/poster-neutral.jpg" ||
		enriched.BackdropURL != "https://images.example/background.jpg" ||
		enriched.LogoURL != "https://images.example/logo-fr.png" {
		t.Fatalf("unexpected enriched movie: %+v", enriched)
	}
}

func TestEnrichCollectionUsesTMDBCollectionIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movies/87096" {
			t.Fatalf("unexpected collection path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"movieposter":[{"url":"https://images.example/avatar-collection-poster.jpg","lang":"00","likes":"8"}],
			"moviebackground":[{"url":"https://images.example/avatar-collection-background.jpg","lang":"00","likes":"12"}],
			"hdmovielogo":[{"url":"https://images.example/avatar-collection-logo.png","lang":"fr","likes":"7"}]
		}`))
	}))
	defer server.Close()

	client := newWithBaseURL("project-key", "", server.URL, server.Client())
	enriched, err := client.EnrichCollection(context.Background(), metadata.ProviderCollection{
		ExternalID: "87096",
	}, "fr-FR")
	if err != nil {
		t.Fatalf("enrich collection: %v", err)
	}
	if enriched.PosterURL != "https://images.example/avatar-collection-poster.jpg" ||
		enriched.BackdropURL != "https://images.example/avatar-collection-background.jpg" ||
		enriched.LogoURL != "https://images.example/avatar-collection-logo.png" {
		t.Fatalf("unexpected enriched collection: %+v", enriched)
	}
}

func TestBestImagePrioritizesQualityBeforeLocalization(t *testing.T) {
	selected := bestImage("fr-FR", []image{
		{URL: "https://images.example/french.jpg", Lang: "fr", Likes: "5", Width: "1000", Height: "1426"},
		{URL: "https://images.example/neutral.jpg", Lang: "00", Likes: "17", Width: "1000", Height: "1426"},
		{URL: "https://images.example/english.jpg", Lang: "en", Likes: "27", Width: "1000", Height: "1426"},
		{URL: "https://images.example/russian.jpg", Lang: "ru", Likes: "999", Width: "1000", Height: "1426"},
	})
	if selected != "https://images.example/english.jpg" {
		t.Fatalf("selected lower-rated localized artwork %q", selected)
	}

	selected = bestImage("ru-RU", []image{
		{URL: "https://images.example/russian.jpg", Lang: "ru", Likes: "4", Width: "1000", Height: "1426"},
		{URL: "https://images.example/neutral.jpg", Lang: "00", Likes: "3", Width: "1000", Height: "1426"},
	})
	if selected != "https://images.example/russian.jpg" {
		t.Fatalf("rejected requested-language artwork %q", selected)
	}

	selected = bestImage("fr-FR", []image{
		{URL: "https://images.example/english.jpg", Lang: "en", Likes: "2", Width: "1000", Height: "1426"},
		{URL: "https://images.example/neutral.jpg", Lang: "00", Likes: "3", Width: "1000", Height: "1426"},
		{URL: "https://images.example/russian.jpg", Lang: "ru", Likes: "4", Width: "1000", Height: "1426"},
	})
	if selected != "https://images.example/english.jpg" {
		t.Fatalf("did not fall back to English artwork %q", selected)
	}

	selected = bestImage("fr-FR",
		[]image{{URL: "https://images.example/hd.png", Lang: "en", Likes: "1", Width: "800", Height: "310"}},
		[]image{{URL: "https://images.example/legacy.png", Lang: "fr", Likes: "99", Width: "400", Height: "155"}},
	)
	if selected != "https://images.example/hd.png" {
		t.Fatalf("selected lower-tier localized artwork %q", selected)
	}
}

func TestBestLocalizedImagePrioritizesLanguageWithinQualityTier(t *testing.T) {
	selected := bestLocalizedImage("fr-FR", []image{
		{URL: "https://images.example/french.png", Lang: "fr", Likes: "5", Width: "800", Height: "310"},
		{URL: "https://images.example/neutral.png", Lang: "00", Likes: "17", Width: "800", Height: "310"},
		{URL: "https://images.example/english.png", Lang: "en", Likes: "27", Width: "800", Height: "310"},
	})
	if selected != "https://images.example/french.png" {
		t.Fatalf("selected non-localized title artwork %q", selected)
	}

	selected = bestLocalizedImage("fr-FR", []image{
		{URL: "https://images.example/english.png", Lang: "en", Likes: "1", Width: "800", Height: "310"},
		{URL: "https://images.example/neutral.png", Lang: "00", Likes: "99", Width: "800", Height: "310"},
	})
	if selected != "https://images.example/english.png" {
		t.Fatalf("did not fall back to English title artwork %q", selected)
	}

	selected = bestLocalizedImage("fr-FR",
		[]image{{URL: "https://images.example/hd.png", Lang: "en", Likes: "1", Width: "800", Height: "310"}},
		[]image{{URL: "https://images.example/legacy.png", Lang: "fr", Likes: "99", Width: "400", Height: "155"}},
	)
	if selected != "https://images.example/hd.png" {
		t.Fatalf("selected lower-tier localized title artwork %q", selected)
	}
}

func TestEnrichSeriesUsesTVDBIdentityAndUpdatesSeasonArtwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/81189" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"tvposter":[{"url":"https://images.example/show-poster.jpg","lang":"00","likes":"2"}],
			"showbackground":[{"url":"https://images.example/show-background.jpg","lang":"00","likes":"4"}],
			"hdtvlogo":[{"url":"https://images.example/show-logo.png","lang":"en","likes":"9"}],
			"seasonposter":[
				{"url":"https://images.example/season-1-en.jpg","lang":"en","likes":"20","season":"1"},
				{"url":"https://images.example/season-1-fr.jpg","lang":"fr","likes":"1","season":"1"},
				{"url":"https://images.example/season-2.jpg","lang":"00","likes":"3","season":"2"}
			],
			"seasonthumb":[
				{"url":"https://images.example/season-1-background.jpg","lang":"00","likes":"5","season":"1"}
			]
		}`))
	}))
	defer server.Close()

	client := newWithBaseURL("project-key", "", server.URL, server.Client())
	enriched, err := client.EnrichSeries(context.Background(), metadata.ProviderSeries{
		ExternalID:    "1396",
		AdditionalIDs: map[string]string{"tvdb": "81189"},
		Seasons: []metadata.ProviderSeasonSummary{
			{ExternalID: "3572", SeasonNumber: 1, PosterURL: "https://image.tmdb.org/season-1.jpg"},
			{ExternalID: "3573", SeasonNumber: 2},
		},
	}, "fr-FR")
	if err != nil {
		t.Fatalf("enrich series: %v", err)
	}
	if enriched.PosterURL != "https://images.example/show-poster.jpg" ||
		enriched.BackdropURL != "https://images.example/show-background.jpg" ||
		enriched.LogoURL != "https://images.example/show-logo.png" {
		t.Fatalf("unexpected series artwork: %+v", enriched)
	}
	if enriched.Seasons[0].PosterURL != "https://images.example/season-1-en.jpg" ||
		enriched.Seasons[0].BackdropURL != "https://images.example/season-1-background.jpg" ||
		enriched.Seasons[1].PosterURL != "https://images.example/season-2.jpg" ||
		enriched.Seasons[1].BackdropURL != "" {
		t.Fatalf("unexpected season artwork: %+v", enriched.Seasons)
	}
}

func TestEnrichSeasonSelectsOnlyRequestedSeason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"seasonposter":[
				{"url":"https://images.example/season-1.jpg","lang":"00","season":"1"},
				{"url":"https://images.example/season-2.jpg","lang":"00","season":"2"}
			],
			"seasonthumb":[
				{"url":"https://images.example/season-2-background.jpg","lang":"00","season":"2"}
			]
		}`))
	}))
	defer server.Close()

	client := newWithBaseURL("project-key", "", server.URL, server.Client())
	enriched, err := client.EnrichSeason(context.Background(), "81189", metadata.ProviderSeason{SeasonNumber: 2}, "en-US")
	if err != nil {
		t.Fatalf("enrich season: %v", err)
	}
	if enriched.PosterURL != "https://images.example/season-2.jpg" ||
		enriched.BackdropURL != "https://images.example/season-2-background.jpg" {
		t.Fatalf("unexpected season artwork: %+v", enriched)
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
			client := newWithBaseURL("project-key", "", server.URL, server.Client())
			_, err := client.EnrichMovie(context.Background(), metadata.ProviderMovie{ExternalID: "550"}, "en-US")
			if !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
		})
	}
}

func TestMissingOptionalIdentifiersDoNotCallFanart(t *testing.T) {
	client := newWithBaseURL("project-key", "", "http://127.0.0.1:1", nil)
	movie, err := client.EnrichMovie(context.Background(), metadata.ProviderMovie{}, "en-US")
	if err != nil || movie.ExternalID != "" {
		t.Fatalf("unexpected movie no-op: movie=%+v err=%v", movie, err)
	}
	series, err := client.EnrichSeries(context.Background(), metadata.ProviderSeries{}, "en-US")
	if err != nil || series.ExternalID != "" {
		t.Fatalf("unexpected series no-op: series=%+v err=%v", series, err)
	}
}
