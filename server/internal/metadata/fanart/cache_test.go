package fanart

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/metadata"
)

type memoryArtworkCacheEntry struct {
	snapshot  artworkSnapshot
	available bool
	expiresAt time.Time
}

type memoryArtworkResponseCache struct {
	mu      sync.Mutex
	entries map[artworkCacheKey]memoryArtworkCacheEntry
}

func newMemoryArtworkResponseCache() *memoryArtworkResponseCache {
	return &memoryArtworkResponseCache{entries: make(map[artworkCacheKey]memoryArtworkCacheEntry)}
}

func (cache *memoryArtworkResponseCache) load(_ context.Context, key artworkCacheKey) (artworkSnapshot, bool, time.Time, bool, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry, ok := cache.entries[key]
	if !ok || !entry.expiresAt.After(time.Now()) {
		return artworkSnapshot{}, false, time.Time{}, false, nil
	}
	return entry.snapshot, entry.available, entry.expiresAt, true, nil
}

func (cache *memoryArtworkResponseCache) store(_ context.Context, key artworkCacheKey, snapshot artworkSnapshot, available bool, expiresAt time.Time) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.entries[key] = memoryArtworkCacheEntry{snapshot: snapshot, available: available, expiresAt: expiresAt}
	return nil
}

func TestMovieArtworkCacheIsSharedAcrossCollectionsFoldersAndClientInstances(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/movies/550" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"movieposter":[{"url":"https://images.example/poster.jpg","lang":"fr"}],
			"moviebackground":[{"url":"https://images.example/background.jpg","lang":"00"}],
			"hdmovielogo":[{"url":"https://images.example/logo.png","lang":"fr"}]
		}`))
	}))
	defer server.Close()

	responseCache := newMemoryArtworkResponseCache()
	firstClient := newWithBaseURL("project-key", "", server.URL, server.Client())
	firstClient.enableResponseCache(responseCache, time.Hour, nil)
	movie, err := firstClient.EnrichMovie(context.Background(), metadata.ProviderMovie{ExternalID: "550"}, "fr-FR")
	if err != nil {
		t.Fatalf("enrich movie: %v", err)
	}
	collection, err := firstClient.EnrichCollection(context.Background(), metadata.ProviderCollection{ExternalID: "550"}, "fr-FR")
	if err != nil {
		t.Fatalf("enrich collection from shared movie identity: %v", err)
	}

	secondClient := newWithBaseURL("project-key", "", server.URL, server.Client())
	secondClient.enableResponseCache(responseCache, time.Hour, nil)
	restored, err := secondClient.EnrichMovie(context.Background(), metadata.ProviderMovie{ExternalID: "550"}, "fr-FR")
	if err != nil {
		t.Fatalf("restore movie artwork: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("Fanart was requested %d times, want 1", requests.Load())
	}
	for name, poster := range map[string]string{
		"movie":      movie.PosterURL,
		"collection": collection.PosterURL,
		"restored":   restored.PosterURL,
	} {
		if poster != "https://images.example/poster.jpg" {
			t.Fatalf("%s did not reuse the cached poster: %q", name, poster)
		}
	}
	if restored.BackdropURL != "https://images.example/background.jpg" || restored.LogoURL != "https://images.example/logo.png" {
		t.Fatalf("cached backdrop and logo were not restored: %+v", restored)
	}
}

func TestFanartCacheCoalescesConcurrentFolderItems(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		time.Sleep(25 * time.Millisecond)
		_, _ = w.Write([]byte(`{"movieposter":[{"url":"https://images.example/poster.jpg","lang":"00"}]}`))
	}))
	defer server.Close()

	client := newWithBaseURL("project-key", "", server.URL, server.Client())
	client.enableResponseCache(newMemoryArtworkResponseCache(), time.Hour, nil)
	var wait sync.WaitGroup
	errorsByRequest := make(chan error, 24)
	for range 24 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := client.EnrichMovie(context.Background(), metadata.ProviderMovie{ExternalID: "550"}, "fr-FR")
			errorsByRequest <- err
		}()
	}
	wait.Wait()
	close(errorsByRequest)
	for err := range errorsByRequest {
		if err != nil {
			t.Fatalf("enrich concurrent item: %v", err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("concurrent folders made %d Fanart requests, want 1", requests.Load())
	}
}

func TestFanartCachePersistsMissingArtwork(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	responseCache := newMemoryArtworkResponseCache()
	for range 2 {
		client := newWithBaseURL("project-key", "", server.URL, server.Client())
		client.enableResponseCache(responseCache, time.Hour, nil)
		_, err := client.EnrichMovie(context.Background(), metadata.ProviderMovie{ExternalID: "999"}, "fr-FR")
		if !errors.Is(err, metadata.ErrProviderNotFound) {
			t.Fatalf("expected cached not-found response, got %v", err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("missing artwork was requested %d times, want 1", requests.Load())
	}
}

func TestSeriesArtworkCacheIsSharedWithSeasonFolders(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/tv/81189" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"tvposter":[{"url":"https://images.example/show.jpg","lang":"00"}],
			"showbackground":[{"url":"https://images.example/background.jpg","lang":"00"}],
			"hdtvlogo":[{"url":"https://images.example/logo.png","lang":"fr"}],
			"seasonposter":[{"url":"https://images.example/season-2.jpg","lang":"fr","season":"2"}],
			"seasonthumb":[{"url":"https://images.example/season-2-background.jpg","lang":"00","season":"2"}]
		}`))
	}))
	defer server.Close()

	client := newWithBaseURL("project-key", "", server.URL, server.Client())
	client.enableResponseCache(newMemoryArtworkResponseCache(), time.Hour, nil)
	series, err := client.EnrichSeries(context.Background(), metadata.ProviderSeries{
		AdditionalIDs: map[string]string{"tvdb": "81189"},
	}, "fr-FR")
	if err != nil {
		t.Fatalf("enrich series: %v", err)
	}
	season, err := client.EnrichSeason(context.Background(), "81189", metadata.ProviderSeason{SeasonNumber: 2}, "fr-FR")
	if err != nil {
		t.Fatalf("enrich season from cached series response: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("series and season folders made %d Fanart requests, want 1", requests.Load())
	}
	if series.PosterURL == "" || series.BackdropURL == "" || series.LogoURL == "" ||
		season.PosterURL != "https://images.example/season-2.jpg" ||
		season.BackdropURL != "https://images.example/season-2-background.jpg" {
		t.Fatalf("incomplete cached series artwork: series=%+v season=%+v", series, season)
	}
}
