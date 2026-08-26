package collection

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/requestwork"
)

type orchestrationExtension struct {
	started      chan struct{}
	canceled     chan struct{}
	once         sync.Once
	canceledOnce sync.Once
	matches      []string
	wait         bool
}

func (extension *orchestrationExtension) Resolve(ctx context.Context, _ SemanticExtensionRequest) ([]string, error) {
	extension.once.Do(func() {
		if extension.started != nil {
			close(extension.started)
		}
	})
	if extension.wait {
		<-ctx.Done()
		extension.canceledOnce.Do(func() {
			if extension.canceled != nil {
				close(extension.canceled)
			}
		})
		return nil, ctx.Err()
	}
	return slices.Clone(extension.matches), nil
}

type orchestrationTMDBProvider struct {
	titleCalls atomic.Int32
	title      func(context.Context, string, TMDBSource) (SourcePage, error)
}

func (provider *orchestrationTMDBProvider) ResolveCollectionSource(context.Context, TMDBSource, int, string, string) (SourcePage, error) {
	return SourcePage{}, nil
}

func (provider *orchestrationTMDBProvider) SearchCollectionTitles(ctx context.Context, title string, source TMDBSource, _ int, _, _ string) (SourcePage, error) {
	provider.titleCalls.Add(1)
	if provider.title == nil {
		return SourcePage{}, nil
	}
	return provider.title(ctx, title, source)
}

func (*orchestrationTMDBProvider) LookupCollectionSource(context.Context, string, string, string, int) ([]LookupResult, error) {
	return nil, nil
}

func (*orchestrationTMDBProvider) CollectionGenres(context.Context, string, string) ([]Genre, error) {
	return nil, nil
}

func (*orchestrationTMDBProvider) SemanticCatalogLanguages(context.Context) ([]string, error) {
	return nil, nil
}

func (*orchestrationTMDBProvider) SemanticCatalogLocale(context.Context, string) (SemanticCatalogLocale, error) {
	return SemanticCatalogLocale{}, nil
}

func TestSemanticExactTitleCancelsOnlyExtensionAndDrainsSibling(t *testing.T) {
	extensionStarted := make(chan struct{})
	extensionCanceled := make(chan struct{})
	siblingStarted := make(chan struct{})
	siblingContext := make(chan context.Context, 1)
	releaseSibling := make(chan struct{})
	extension := &orchestrationExtension{started: extensionStarted, canceled: extensionCanceled, wait: true}
	provider := &orchestrationTMDBProvider{title: func(ctx context.Context, _ string, source TMDBSource) (SourcePage, error) {
		if source.MediaType == MediaTypeSeries {
			siblingContext <- ctx
			close(siblingStarted)
			<-releaseSibling
			return SourcePage{HasMore: true, Items: []Item{{ID: "series-sibling", MediaType: MediaTypeSeries}}}, nil
		}
		<-extensionStarted
		<-siblingStarted
		return SourcePage{ExactTitleMatch: true, Items: []Item{{ID: "movie-exact", MediaType: MediaTypeMovie}}}, nil
	}}
	service := &Service{semanticExtension: extension, semanticMemo: newSemanticExtensionMemo()}
	vocabulary := buildSemanticVocabulary(nil)
	parsed := parseSemanticQueryWithVocabulary("Alien", "", nil, vocabulary, "en-US")

	completed := make(chan semanticAmbiguityResult, 1)
	failed := make(chan error, 1)
	go func() {
		result, err := service.resolveSemanticAmbiguity(context.Background(), provider, parsed, vocabulary, nil, 1, "en-US", "US")
		if err != nil {
			failed <- err
			return
		}
		completed <- result
	}()

	probeContext := <-siblingContext
	<-extensionCanceled
	if err := probeContext.Err(); err != nil {
		t.Fatalf("exact title canceled sibling probe: %v", err)
	}
	close(releaseSibling)
	select {
	case err := <-failed:
		t.Fatalf("resolve exact title: %v", err)
	case result := <-completed:
		if !result.searchByTitle || result.partial || !result.hasMore || len(result.pages) != 2 ||
			!result.pages[0].ExactTitleMatch || len(result.pages[1].Items) != 1 {
			t.Fatalf("exact-title result = %+v", result)
		}
	}
	if !errors.Is(probeContext.Err(), context.Canceled) {
		t.Fatalf("title probes were not cleaned up after collection: %v", probeContext.Err())
	}
}

func TestSemanticTitleProbeOrderHasStableResults(t *testing.T) {
	movieItems := make([]Item, 24)
	for index := range movieItems {
		movieItems[index] = Item{ID: fmt.Sprintf("movie-%02d", index), MediaType: MediaTypeMovie}
	}
	seriesItems := make([]Item, 30)
	for index := range seriesItems {
		seriesItems[index] = Item{ID: fmt.Sprintf("series-%02d", index), MediaType: MediaTypeSeries}
	}
	seriesItems[7] = movieItems[7]
	pages := []SourcePage{
		{ExactTitleMatch: true, Items: movieItems},
		{HasMore: true, Items: seriesItems},
	}

	resolve := func(order []int, limit int) ([]string, bool) {
		t.Helper()
		outcomes := make(chan semanticSourceOutcome)
		go func() {
			for _, index := range order {
				outcomes <- semanticSourceOutcome{index: index, page: pages[index]}
			}
		}()
		resolved, hasMore, partial, exact, err := (&Service{}).collectSemanticSourceResolution(context.Background(), outcomes, len(pages), nil, true)
		if err != nil || partial || !exact {
			t.Fatalf("collect order %v: exact=%t partial=%t error=%v", order, exact, partial, err)
		}
		items := deduplicateSemanticItems(semanticPageItems(resolved, true))
		if len(items) > limit {
			items = items[:limit]
			hasMore = true
		}
		ids := make([]string, len(items))
		for index := range items {
			ids[index] = items[index].MediaType + ":" + items[index].ID
		}
		return ids, hasMore
	}

	for _, limit := range []int{24, 30} {
		exactFirstIDs, exactFirstHasMore := resolve([]int{0, 1}, limit)
		exactLastIDs, exactLastHasMore := resolve([]int{1, 0}, limit)
		if !slices.Equal(exactFirstIDs, exactLastIDs) || len(exactFirstIDs) != limit {
			t.Fatalf("resolution order changed limit %d: first=%v last=%v", limit, exactFirstIDs, exactLastIDs)
		}
		if !exactFirstHasMore || exactFirstHasMore != exactLastHasMore {
			t.Fatalf("resolution order changed hasMore at limit %d: first=%t last=%t", limit, exactFirstHasMore, exactLastHasMore)
		}
		duplicateCount := 0
		for _, id := range exactFirstIDs {
			if id == MediaTypeMovie+":movie-07" {
				duplicateCount++
			}
		}
		if duplicateCount != 1 {
			t.Fatalf("duplicate exact identity count at limit %d = %d, want 1 in %v", limit, duplicateCount, exactFirstIDs)
		}
	}
}

func TestSemanticExactTitleKeepsSiblingFailurePartial(t *testing.T) {
	for _, siblingErr := range []error{errors.New("series probe failed"), context.DeadlineExceeded} {
		for _, order := range [][]int{{0, 1}, {1, 0}} {
			outcomes := make(chan semanticSourceOutcome)
			go func() {
				resolved := []semanticSourceOutcome{
					{index: 0, page: SourcePage{ExactTitleMatch: true, Items: []Item{{ID: "movie-exact"}}}},
					{index: 1, err: siblingErr},
				}
				for _, index := range order {
					outcomes <- resolved[index]
				}
			}()
			pages, hasMore, partial, exact, err := (&Service{}).collectSemanticSourceResolution(context.Background(), outcomes, 2, nil, true)
			if err != nil || !exact || !partial || hasMore || len(pages[0].Items) != 1 {
				t.Fatalf("sibling error %v order %v: pages=%+v hasMore=%t partial=%t exact=%t error=%v", siblingErr, order, pages, hasMore, partial, exact, err)
			}
		}
	}
}

func TestSemanticExactTitlePropagatesSiblingFailure(t *testing.T) {
	provider := &orchestrationTMDBProvider{title: func(_ context.Context, _ string, source TMDBSource) (SourcePage, error) {
		if source.MediaType == MediaTypeSeries {
			return SourcePage{}, errors.New("series probe failed")
		}
		return SourcePage{ExactTitleMatch: true, Items: []Item{{ID: "movie-exact"}}}, nil
	}}
	service := &Service{semanticMemo: newSemanticExtensionMemo()}
	vocabulary := buildSemanticVocabulary(nil)
	parsed := parseSemanticQueryWithVocabulary("Alien", "", nil, vocabulary, "en-US")

	result, err := service.resolveSemanticAmbiguity(context.Background(), provider, parsed, vocabulary, nil, 1, "en-US", "US")
	if err != nil {
		t.Fatal(err)
	}
	if !result.searchByTitle || !result.partial || len(result.pages) != 2 || !result.pages[0].ExactTitleMatch || len(result.pages[0].Items) != 1 {
		t.Fatalf("exact title with failed sibling = %+v", result)
	}
}

func TestSemanticModelClassificationUsesDiscoverAfterConcurrentTitleProbe(t *testing.T) {
	extension := &orchestrationExtension{matches: []string{"genre:horror"}}
	provider := &orchestrationTMDBProvider{title: func(context.Context, string, TMDBSource) (SourcePage, error) {
		return SourcePage{Items: []Item{{ID: "literal-fallback"}}}, nil
	}}
	service := &Service{semanticExtension: extension, semanticMemo: newSemanticExtensionMemo()}
	vocabulary := buildSemanticVocabulary(nil)
	parsed := parseSemanticQueryWithVocabulary("something sinister", MediaTypeMovie, nil, vocabulary, "en-US")

	result, err := service.resolveSemanticAmbiguity(context.Background(), provider, parsed, vocabulary, nil, 1, "en-US", "US")
	if err != nil {
		t.Fatal(err)
	}
	if result.searchByTitle || result.partial || !slices.Contains(result.parsed.genres, "horror") {
		t.Fatalf("classified ambiguity = %+v", result)
	}
	sources := semanticTMDBSources(result.parsed, nil)
	if len(sources) != 1 || sources[0].SourceType != "discover" {
		t.Fatalf("classified sources = %+v", sources)
	}
}

func TestSemanticExtensionTimeoutIsPartialAndReusesPrefetchedTitlePages(t *testing.T) {
	extension := &orchestrationExtension{wait: true}
	provider := &orchestrationTMDBProvider{title: func(context.Context, string, TMDBSource) (SourcePage, error) {
		return SourcePage{Items: []Item{{ID: "fallback"}}}, nil
	}}
	memo := newSemanticExtensionMemo()
	memo.budget = 20 * time.Millisecond
	service := &Service{semanticExtension: extension, semanticMemo: memo}
	vocabulary := buildSemanticVocabulary(nil)
	parsed := parseSemanticQueryWithVocabulary("unknown wording", MediaTypeMovie, nil, vocabulary, "en-US")

	result, err := service.resolveSemanticAmbiguity(context.Background(), provider, parsed, vocabulary, nil, 1, "en-US", "US")
	if err != nil {
		t.Fatal(err)
	}
	if !result.searchByTitle || !result.partial || len(result.pages) != 1 || len(result.pages[0].Items) != 1 {
		t.Fatalf("timeout fallback = %+v", result)
	}
	if calls := provider.titleCalls.Load(); calls != 1 {
		t.Fatalf("prefetched title pages fetched %d times, want once", calls)
	}
}

func TestSemanticExplicitMediaTypeProbesOnlyThatType(t *testing.T) {
	var probed []string
	var mu sync.Mutex
	provider := &orchestrationTMDBProvider{title: func(_ context.Context, _ string, source TMDBSource) (SourcePage, error) {
		mu.Lock()
		probed = append(probed, source.MediaType)
		mu.Unlock()
		return SourcePage{}, nil
	}}
	service := &Service{semanticMemo: newSemanticExtensionMemo()}
	vocabulary := buildSemanticVocabulary(nil)
	parsed := parseSemanticQueryWithVocabulary("Alien", MediaTypeSeries, nil, vocabulary, "en-US")

	result, err := service.resolveSemanticAmbiguity(context.Background(), provider, parsed, vocabulary, nil, 1, "en-US", "US")
	if err != nil {
		t.Fatal(err)
	}
	if !result.searchByTitle || !slices.Equal(probed, []string{MediaTypeSeries}) {
		t.Fatalf("probed media types = %v, result = %+v", probed, result)
	}
}

func TestSemanticConstraintsReachDiscoverAndTitleSources(t *testing.T) {
	minimumRating := 7.5
	minimumVotes := 100
	minimumRuntime, maximumRuntime := 70, 110
	parsed := parsedSemanticQuery{
		mediaTypes: []string{MediaTypeMovie},
		genres:     []string{"comedy"},
		constraints: semanticQueryConstraints{
			releaseDateFrom: "2020-01-01", releaseDateTo: "2020-12-31", voteAverageMin: &minimumRating, voteCountMin: &minimumVotes,
			runtimeMin: &minimumRuntime, runtimeMax: &maximumRuntime, excludedGenres: []string{"horror"}, sort: "vote_average.desc",
		},
	}
	assertSource := func(name string, source TMDBSource) {
		t.Helper()
		filters := source.Filters
		if source.Sort != "vote_average.desc" || filters.ReleaseDateFrom != "2020-01-01" || filters.ReleaseDateTo != "2020-12-31" ||
			filters.VoteAverageMin != &minimumRating || filters.VoteCountMin != &minimumVotes || filters.RuntimeMin != &minimumRuntime || filters.RuntimeMax != &maximumRuntime ||
			!slices.Equal(filters.Genres, []int64{35}) || !slices.Equal(filters.ExcludedGenres, []int64{27}) {
			t.Fatalf("%s source lost constraints: %+v", name, source)
		}
	}
	discover := semanticTMDBSources(parsed, nil)
	titles := semanticTMDBTitleSources(parsed)
	if len(discover) != 1 || len(titles) != 1 {
		t.Fatalf("constraint sources: discover=%+v titles=%+v", discover, titles)
	}
	assertSource("discover", discover[0])
	assertSource("title", titles[0])
}

func TestSemanticHighConfidenceColloquialGenresStayDeterministic(t *testing.T) {
	for _, test := range []struct {
		query string
		genre string
	}{
		{query: "un film qui fait peur", genre: "horror"},
		{query: "something spooky", genre: "horror"},
		{query: "une série qui fait rire", genre: "comedy"},
		{query: "a funny movie", genre: "comedy"},
	} {
		parsed := parseSemanticQuery(test.query, "", nil)
		if parsed.needsExtension || !slices.Equal(parsed.genres, []string{test.genre}) {
			t.Errorf("parse %q = genres %v needsExtension %t", test.query, parsed.genres, parsed.needsExtension)
		}
	}
}

func TestSemanticAmbiguityPreservesRequestCancellationAfterExact(t *testing.T) {
	extensionStarted := make(chan struct{})
	extensionCanceled := make(chan struct{})
	siblingStarted := make(chan struct{})
	extension := &orchestrationExtension{started: extensionStarted, canceled: extensionCanceled, wait: true}
	provider := &orchestrationTMDBProvider{title: func(ctx context.Context, _ string, source TMDBSource) (SourcePage, error) {
		if source.MediaType == MediaTypeSeries {
			close(siblingStarted)
			<-ctx.Done()
			return SourcePage{}, ctx.Err()
		}
		<-extensionStarted
		<-siblingStarted
		return SourcePage{ExactTitleMatch: true, Items: []Item{{ID: "movie-exact"}}}, nil
	}}
	service := &Service{semanticExtension: extension, semanticMemo: newSemanticExtensionMemo()}
	vocabulary := buildSemanticVocabulary(nil)
	parsed := parseSemanticQueryWithVocabulary("Alien", "", nil, vocabulary, "en-US")
	ctx, cancel := context.WithCancel(context.Background())
	failed := make(chan error, 1)
	go func() {
		_, err := service.resolveSemanticAmbiguity(ctx, provider, parsed, vocabulary, nil, 1, "en-US", "US")
		failed <- err
	}()

	<-extensionCanceled
	cancel()
	if err := <-failed; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ambiguity error = %v", err)
	}
}

func TestSemanticConstraintIntentExclusionsRemainKnownWithVocabulary(t *testing.T) {
	vocabulary := buildSemanticVocabulary(nil)
	excluded, err := normalizeSemanticExcludedIntentIDsWithVocabulary([]string{"runtime_max:90", "exclude_genre:horror"}, vocabulary)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := excluded["runtime_max:90"]; !ok {
		t.Fatalf("runtime constraint exclusion missing: %v", excluded)
	}
	if _, ok := excluded["exclude_genre:horror"]; !ok {
		t.Fatalf("genre exclusion constraint missing: %v", excluded)
	}
}

func TestSemanticFullyDeterministicConstraintsSkipTitleProbes(t *testing.T) {
	provider := &orchestrationTMDBProvider{}
	service := &Service{semanticMemo: newSemanticExtensionMemo()}
	vocabulary := buildSemanticVocabulary(nil)
	parsed := parseSemanticQueryWithVocabulary("latest movies", "", nil, vocabulary, "en-US")
	applySemanticConstraints("latest movies", "en-US", time.Date(2026, time.August, 26, 0, 0, 0, 0, time.UTC), &parsed, nil)

	result, err := service.resolveSemanticAmbiguity(context.Background(), provider, parsed, vocabulary, nil, 1, "en-US", "US")
	if err != nil {
		t.Fatal(err)
	}
	if result.searchByTitle || provider.titleCalls.Load() != 0 || parsed.needsExtension {
		t.Fatalf("deterministic query probed titles: result=%+v calls=%d parsed=%+v", result, provider.titleCalls.Load(), parsed)
	}
}

func TestSemanticTMDBFailureLogOmitsQueryURLAndCause(t *testing.T) {
	var logs bytes.Buffer
	service := &Service{logger: slog.New(slog.NewTextHandler(&logs, nil))}
	hostileURL := "https://api.example/search/movie?query=Private+Title&token=credential"
	providerErr := metadata.NewProviderError(
		metadata.ErrProviderFailure,
		&url.Error{Op: "Get", URL: hostileURL, Err: errors.New("response body secret")},
		502,
		"/search/movie?query=Private+Title&token=credential",
	)
	outcomes := make(chan semanticSourceOutcome, 1)
	outcomes <- semanticSourceOutcome{err: providerErr}
	ctx, requestID := requestwork.WithRequestID(context.Background(), "semantic-private-7")
	_, _, partial, _, err := service.collectSemanticSourceResolution(ctx, outcomes, 1, nil, true)
	if err != nil || !partial {
		t.Fatalf("semantic failure result partial=%t err=%v", partial, err)
	}
	logged := logs.String()
	if !strings.Contains(logged, "error_code=provider_failure") || !strings.Contains(logged, "request_id="+requestID) {
		t.Fatalf("semantic failure lacks safe code/correlation: %s", logged)
	}
	for _, secret := range []string{"Private", "api.example", "/search/movie", "token", "credential", "response body secret"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("semantic failure log disclosed %q: %s", secret, logged)
		}
	}
}
