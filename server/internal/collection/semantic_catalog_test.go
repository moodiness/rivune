package collection

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/requestwork"
)

type catalogTMDBProvider struct {
	mu         sync.Mutex
	languages  []string
	locales    map[string]SemanticCatalogLocale
	localeErr  error
	localeErrs map[string]error
	localeCall func(context.Context, string) (SemanticCatalogLocale, error)
	calls      map[string]int
}

func (provider *catalogTMDBProvider) ResolveCollectionSource(context.Context, TMDBSource, int, string, string) (SourcePage, error) {
	return SourcePage{}, nil
}

func (provider *catalogTMDBProvider) SearchCollectionTitles(context.Context, string, TMDBSource, int, string, string) (SourcePage, error) {
	return SourcePage{}, nil
}

func (provider *catalogTMDBProvider) LookupCollectionSource(context.Context, string, string, string, int) ([]LookupResult, error) {
	return nil, nil
}

func (provider *catalogTMDBProvider) CollectionGenres(context.Context, string, string) ([]Genre, error) {
	return nil, nil
}

func (provider *catalogTMDBProvider) SemanticCatalogLanguages(context.Context) ([]string, error) {
	return slices.Clone(provider.languages), nil
}

func (provider *catalogTMDBProvider) SemanticCatalogLocale(ctx context.Context, language string) (SemanticCatalogLocale, error) {
	provider.mu.Lock()
	if provider.calls == nil {
		provider.calls = make(map[string]int)
	}
	provider.calls[language]++
	call, locale, err := provider.localeCall, provider.locales[language], provider.localeErr
	if languageErr := provider.localeErrs[language]; languageErr != nil {
		err = languageErr
	}
	provider.mu.Unlock()
	if call != nil {
		return call(ctx, language)
	}
	return locale, err
}

func TestSemanticCatalogMatchesOneLocaleAndLabelsInAnother(t *testing.T) {
	provider := &catalogTMDBProvider{locales: map[string]SemanticCatalogLocale{
		"es-ES": semanticCatalogFixture("Bélica", "Guerra y política", "Alemania"),
		"de-DE": semanticCatalogFixture("Kriegsfilm", "Krieg und Politik", "Deutschland"),
	}}
	catalog := newSemanticCatalog(nil, &staticProviderSource{providers: NewProviderSet(1, provider, nil, nil, nil, nil, nil)})
	ctx := context.Background()
	if _, partial, err := catalog.vocabulary(ctx, provider, "es-ES"); err != nil || partial {
		t.Fatalf("load Spanish vocabulary: partial=%t error=%v", partial, err)
	}
	vocabulary, partial, err := catalog.vocabulary(ctx, provider, "de-DE")
	if err != nil || partial {
		t.Fatalf("load German vocabulary: partial=%t error=%v", partial, err)
	}

	parsed := parseSemanticQueryWithVocabulary("Kriegsfilm Deutschland", "", nil, vocabulary, "es-ES")
	if len(parsed.intents) != 2 || parsed.intents[0].ID != "genre:war" || parsed.intents[0].Label != "Bélica" || parsed.intents[1].ID != "country:de" || parsed.intents[1].Label != "Alemania" {
		t.Fatalf("cross-locale parse = %+v", parsed.intents)
	}
	if !slices.Equal(parsed.genres, []string{"war"}) || !slices.Equal(parsed.countries, []string{"DE"}) || parsed.needsExtension {
		t.Fatalf("cross-locale filters = genres %v countries %v extension=%t", parsed.genres, parsed.countries, parsed.needsExtension)
	}
	if _, err := normalizeSemanticExcludedIntentIDsWithVocabulary([]string{"country:de"}, vocabulary); err != nil {
		t.Fatalf("dynamic country intent could not be excluded: %v", err)
	}
	if _, _, err := catalog.vocabulary(ctx, provider, "de-DE"); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.calls["de-DE"] != 1 {
		t.Fatalf("fresh German locale fetched %d times", provider.calls["de-DE"])
	}
}

func TestSemanticCatalogKeepsStaleVocabularyWhenRefreshFails(t *testing.T) {
	provider := &catalogTMDBProvider{localeErr: errors.New("TMDB unavailable")}
	locale := semanticCatalogFixture("Kriegsfilm", "Krieg und Politik", "Deutschland")
	locales := map[string]semanticCatalogLocaleEntry{
		"de-DE": {locale: locale, expiresAt: time.Now().Add(-time.Hour)},
	}
	catalog := newSemanticCatalog(nil, &staticProviderSource{providers: NewProviderSet(1, provider, nil, nil, nil, nil, nil)})
	catalog.mu.Lock()
	catalog.loaded = true
	catalog.locales = locales
	catalog.current.Store(buildSemanticVocabulary(locales))
	catalog.mu.Unlock()
	vocabulary, partial, err := catalog.vocabulary(context.Background(), provider, "de-DE")
	if err != nil || !partial {
		t.Fatalf("stale vocabulary fallback partial=%t error=%v", partial, err)
	}
	parsed := parseSemanticQueryWithVocabulary("Kriegsfilm", "", nil, vocabulary, "de-DE")
	if len(parsed.intents) != 1 || parsed.intents[0].ID != "genre:war" {
		t.Fatalf("stale vocabulary was discarded: %+v", parsed.intents)
	}
}

func TestSemanticCatalogDropsAmbiguousCountryNames(t *testing.T) {
	locale := semanticCatalogFixture("War", "War & Politics", "United States")
	locale.Countries = []Country{
		{Code: "CD", EnglishName: "Congo", NativeName: "Congo"},
		{Code: "CG", EnglishName: "Congo", NativeName: "Congo"},
	}
	vocabulary := buildSemanticVocabulary(map[string]semanticCatalogLocaleEntry{
		"en-US": {locale: locale},
	})
	parsed := parseSemanticQueryWithVocabulary("Congo", "", nil, vocabulary, "en-US")
	if len(parsed.countries) != 0 || len(parsed.intents) != 0 || !parsed.needsExtension {
		t.Fatalf("ambiguous country produced a deterministic intent: %+v", parsed)
	}
}
func TestSemanticCatalogCompoundGenreWinsOverEmbeddedMediaType(t *testing.T) {
	locale := SemanticCatalogLocale{
		MovieGenres:  []Genre{{ID: 10770, Name: "TV Movie"}},
		SeriesGenres: []Genre{{ID: 18, Name: "Drama"}},
		Countries:    []Country{{Code: "US", EnglishName: "United States", NativeName: "United States"}},
	}
	vocabulary := buildSemanticVocabulary(map[string]semanticCatalogLocaleEntry{"en-US": {locale: locale}})
	parsed := parseSemanticQueryWithVocabulary("TV Movie", "", nil, vocabulary, "en-US")
	if len(parsed.intents) != 1 || parsed.intents[0].ID != "genre:tv_movie" || !slices.Equal(parsed.genres, []string{"tv_movie"}) || len(parsed.mediaTypes) != 0 {
		t.Fatalf("compound TMDB genre was split into a media type: %+v", parsed)
	}
}

func TestSemanticCatalogLanguageOrderIsBoundedCanonicalAndEnglishFirst(t *testing.T) {
	languages := normalizeSemanticCatalogLanguages([]string{"fr-fr", "DE-de", "invalid-tag-extra", "en-us", "fr-FR"})
	if !slices.Equal(languages, []string{"en-US", "de-DE", "fr-FR"}) {
		t.Fatalf("normalized languages = %v", languages)
	}
}

func TestSemanticGlobalGenreConceptsMapToCorrectMediaFilters(t *testing.T) {
	movies, movieOK := semanticGenreIDs([]string{"action_adventure", "tv_movie"}, MediaTypeMovie)
	series, seriesOK := semanticGenreIDs([]string{"action_adventure", "talk"}, MediaTypeSeries)
	if !movieOK || !slices.Equal(movies, []int64{28, 12, 10770}) {
		t.Fatalf("movie global genre IDs = %v, ok=%t", movies, movieOK)
	}
	if !seriesOK || !slices.Equal(series, []int64{10759, 10767}) {
		t.Fatalf("series global genre IDs = %v, ok=%t", series, seriesOK)
	}
}

func TestSemanticCatalogLocaleCacheIsBounded(t *testing.T) {
	locales := make(map[string]semanticCatalogLocaleEntry)
	for index := range maximumSemanticCatalogLocales + 2 {
		language := string(rune('a'+index/26)) + string(rune('a'+index%26))
		locales[language] = semanticCatalogLocaleEntry{expiresAt: time.Unix(int64(index), 0)}
	}
	pruneSemanticCatalogLocales(locales, maximumSemanticCatalogLocales)
	if len(locales) != maximumSemanticCatalogLocales {
		t.Fatalf("catalog locale cache size = %d", len(locales))
	}
	if _, retainedOldest := locales["aa"]; retainedOldest {
		t.Fatal("catalog locale cache retained its oldest entry")
	}
}

func TestSemanticCatalogCanceledWaiterDoesNotCancelSibling(t *testing.T) {
	started, release, providerCanceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	provider := &catalogTMDBProvider{localeCall: func(ctx context.Context, _ string) (SemanticCatalogLocale, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
			return semanticCatalogFixture("War", "War & Politics", "Germany"), nil
		case <-ctx.Done():
			close(providerCanceled)
			return SemanticCatalogLocale{}, ctx.Err()
		}
	}}
	catalog := newSemanticCatalog(nil, nil)
	catalog.loaded = true
	firstContext, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan error, 1)
	secondResult := make(chan error, 1)
	go func() { firstResult <- catalog.refreshLocale(firstContext, provider, "de-DE") }()
	<-started
	go func() { secondResult <- catalog.refreshLocale(context.Background(), provider, "de-DE") }()
	deadline := time.Now().Add(time.Second)
	for {
		catalog.mu.Lock()
		waiters := catalog.flights["locale:de-DE"].waiters
		catalog.mu.Unlock()
		if waiters == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second waiter did not join catalog flight")
		}
		time.Sleep(time.Millisecond)
	}
	cancelFirst()
	if err := <-firstResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("first waiter error = %v", err)
	}
	select {
	case <-providerCanceled:
		t.Fatal("canceling one waiter canceled the shared provider call")
	default:
	}
	close(release)
	if err := <-secondResult; err != nil {
		t.Fatalf("sibling waiter error = %v", err)
	}
}

func TestSemanticCatalogLastWaiterCancelsProvider(t *testing.T) {
	started, providerCanceled := make(chan struct{}), make(chan struct{})
	provider := &catalogTMDBProvider{localeCall: func(ctx context.Context, _ string) (SemanticCatalogLocale, error) {
		close(started)
		<-ctx.Done()
		close(providerCanceled)
		return SemanticCatalogLocale{}, ctx.Err()
	}}
	catalog := newSemanticCatalog(nil, nil)
	catalog.loaded = true
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- catalog.refreshLocale(ctx, provider, "de-DE") }()
	<-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter error = %v", err)
	}
	select {
	case <-providerCanceled:
	case <-time.After(time.Second):
		t.Fatal("last waiter did not cancel the provider")
	}
}

func TestSemanticCatalogFailureDoesNotStarveFollowingLocales(t *testing.T) {
	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	provider := &catalogTMDBProvider{
		languages: []string{"en-US", "de-DE", "fr-FR"},
		locales: map[string]SemanticCatalogLocale{
			"de-DE": semanticCatalogFixture("Krieg", "Krieg und Politik", "Deutschland"),
			"fr-FR": semanticCatalogFixture("Guerre", "Guerre et politique", "Allemagne"),
		},
		localeErrs: map[string]error{"en-US": errors.New("locale unavailable")},
	}
	catalog := newSemanticCatalog(nil, &staticProviderSource{providers: NewProviderSet(1, provider, nil, nil, nil, nil, nil)})
	catalog.loaded = true
	catalog.now = func() time.Time { return now }
	if delay := catalog.synchronize(context.Background()); delay != semanticCatalogRetryInterval {
		t.Fatalf("next synchronization delay = %v", delay)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	for _, language := range provider.languages {
		if provider.calls[language] != 1 {
			t.Fatalf("locale %s calls = %d", language, provider.calls[language])
		}
	}
}

func TestSemanticCatalogBackoffCapsAndResetsAfterSuccess(t *testing.T) {
	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	provider := &catalogTMDBProvider{localeErr: errors.New("unavailable"), locales: map[string]SemanticCatalogLocale{
		"de-DE": semanticCatalogFixture("Krieg", "Krieg und Politik", "Deutschland"),
	}}
	catalog := newSemanticCatalog(nil, nil)
	catalog.loaded = true
	catalog.now = func() time.Time { return now }
	for attempt := 1; attempt <= 12; attempt++ {
		if err := catalog.refreshLocale(context.Background(), provider, "de-DE"); err == nil {
			t.Fatal("failed provider refresh unexpectedly succeeded")
		}
		delay, ok := catalog.localeRetryDelay("de-DE")
		if !ok || delay > semanticCatalogMaximumBackoff {
			t.Fatalf("attempt %d delay = %v, present=%t", attempt, delay, ok)
		}
		if attempt == 1 && delay != semanticCatalogRetryInterval {
			t.Fatalf("initial backoff = %v", delay)
		}
		now = now.Add(delay)
	}
	if delay, _ := catalog.localeRetryDelay("de-DE"); delay != 0 {
		t.Fatalf("matured capped backoff = %v", delay)
	}
	provider.mu.Lock()
	provider.localeErr = nil
	provider.mu.Unlock()
	if err := catalog.refreshLocale(context.Background(), provider, "de-DE"); err != nil {
		t.Fatalf("successful reset refresh: %v", err)
	}
	if _, ok := catalog.localeRetryDelay("de-DE"); ok {
		t.Fatal("successful refresh retained locale backoff")
	}
}

func TestSemanticCatalogLogsStableClassAndCorrelationWithoutProviderDetails(t *testing.T) {
	var logs bytes.Buffer
	catalog := newSemanticCatalog(nil, nil)
	catalog.setLogger(slog.New(slog.NewTextHandler(&logs, nil)))
	ctx, requestID := requestwork.WithRequestID(context.Background(), "semantic-correlation-7")
	catalog.warn(ctx, "refresh TMDB semantic locale", errors.New("GET https://api.example/private/path?query=secret&token=credential: failed body"), "language", "fr-FR")
	logged := logs.String()
	if !strings.Contains(logged, "error_class=provider_failure") || !strings.Contains(logged, "request_id="+requestID) || !strings.Contains(logged, "language=fr-FR") {
		t.Fatalf("semantic catalog log lacks safe correlation: %s", logged)
	}
	for _, secret := range []string{"api.example", "private/path", "query=secret", "token=credential", "failed body"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("semantic catalog log disclosed %q: %s", secret, logged)
		}
	}
}

func semanticCatalogFixture(movieWar, seriesWar, countryName string) SemanticCatalogLocale {
	return SemanticCatalogLocale{
		MovieGenres:  []Genre{{ID: 10752, Name: movieWar}, {ID: 80, Name: "Crime"}},
		SeriesGenres: []Genre{{ID: 10768, Name: seriesWar}, {ID: 80, Name: "Crime"}},
		Countries:    []Country{{Code: "DE", EnglishName: countryName, NativeName: countryName}},
	}
}
