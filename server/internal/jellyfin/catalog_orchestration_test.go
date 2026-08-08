package jellyfin

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

type orchestrationStore struct {
	local         watchstate.CatalogPage
	localErr      error
	resolved      map[string]string
	titles        map[string]watchstate.CatalogTitle
	titleReads    []string
	resolveCalls  int
	resolveErr    error
	resolveInputs []watchstate.ResolveTitleInput
}

func (store *orchestrationStore) GetCatalogTitle(_ context.Context, _ auth.Principal, id string) (watchstate.CatalogTitle, error) {
	store.titleReads = append(store.titleReads, id)
	title, ok := store.titles[id]
	if !ok {
		return watchstate.CatalogTitle{}, watchstate.ErrNotFound
	}
	return title, nil
}
func (store *orchestrationStore) GetCatalogTitles(context.Context, auth.Principal, []string) ([]watchstate.CatalogTitle, error) {
	return nil, nil
}
func (store *orchestrationStore) ListCatalogItems(context.Context, auth.Principal, watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
	return store.local, store.localErr
}
func (store *orchestrationStore) ResolveLinkedCatalogTitle(_ context.Context, _ auth.Principal, input watchstate.ResolveTitleInput) (watchstate.TitleReference, error) {
	store.resolveCalls++
	store.resolveInputs = append(store.resolveInputs, input)
	if store.resolveErr != nil {
		return watchstate.TitleReference{}, store.resolveErr
	}
	id := store.resolved[input.Provider+":"+input.ExternalID]
	if id == "" {
		return watchstate.TitleReference{}, watchstate.ErrInvalidInput
	}
	return watchstate.TitleReference{
		TitleID: id, MediaType: input.MediaType, Provider: input.Provider,
		ExternalID: input.ExternalID, ResourceID: input.ResourceID, Title: input.Title,
		PosterURL: input.PosterURL, BackgroundURL: input.BackgroundURL, ReleaseInfo: input.ReleaseInfo,
	}, nil
}

type orchestrationMetadata struct {
	movies          metadata.MoviePage
	series          metadata.SeriesPage
	movieErr        error
	seriesErr       error
	moviePages      map[int]metadata.MoviePage
	seriesPages     map[int]metadata.SeriesPage
	movieErrors     map[int]error
	seriesErrors    map[int]error
	movieCalls      []int
	seriesCalls     []int
	movieDetail     metadata.Movie
	seriesDetail    metadata.Series
	movieDetailErr  error
	seriesDetailErr error
	detailIDs       []string
}

func (service *orchestrationMetadata) SearchLinkedMovies(_ context.Context, _ auth.Principal, options metadata.SearchOptions) (metadata.MoviePage, error) {
	service.movieCalls = append(service.movieCalls, options.Page)
	if err := service.movieErrors[options.Page]; err != nil {
		return metadata.MoviePage{}, err
	}
	if service.moviePages != nil {
		return service.moviePages[options.Page], nil
	}
	return service.movies, service.movieErr
}
func (service *orchestrationMetadata) SearchLinkedSeries(_ context.Context, _ auth.Principal, options metadata.SearchOptions) (metadata.SeriesPage, error) {
	service.seriesCalls = append(service.seriesCalls, options.Page)
	if err := service.seriesErrors[options.Page]; err != nil {
		return metadata.SeriesPage{}, err
	}
	if service.seriesPages != nil {
		return service.seriesPages[options.Page], nil
	}
	return service.series, service.seriesErr
}

func (service *orchestrationMetadata) MovieDetails(_ context.Context, _ auth.Principal, id, _ string) (metadata.Movie, error) {
	service.detailIDs = append(service.detailIDs, id)
	return service.movieDetail, service.movieDetailErr
}

func (service *orchestrationMetadata) SeriesDetails(_ context.Context, _ auth.Principal, id string, _ metadata.SeriesDetailsOptions) (metadata.Series, error) {
	service.detailIDs = append(service.detailIDs, id)
	return service.seriesDetail, service.seriesDetailErr
}

func (*orchestrationMetadata) SeasonDetails(context.Context, auth.Principal, string, string, string) (metadata.Season, error) {
	return metadata.Season{}, nil
}

type orchestrationAddons struct {
	page    addon.CatalogSearchPage
	err     error
	started chan<- struct{}
	calls   int
	release <-chan struct{}
	artwork addon.CatalogSearchArtworkPresenter
}

func (service *orchestrationAddons) SearchCatalogItems(_ context.Context, _ auth.Principal, _ []string, _ string, _ int, artwork addon.CatalogSearchArtworkPresenter) (addon.CatalogSearchPage, error) {
	service.artwork = artwork
	service.calls++
	if service.started != nil {
		close(service.started)
	}
	if service.release != nil {
		<-service.release
	}
	return service.page, service.err
}

func TestCatalogOrchestrationSearchesNonLibrarySourcesDeduplicatesSortsAndPages(t *testing.T) {
	const (
		movieID  = "10000000-0000-4000-8000-000000000001"
		seriesID = "10000000-0000-4000-8000-000000000002"
		customID = "10000000-0000-4000-8000-000000000003"
	)
	store := &orchestrationStore{
		local: watchstate.CatalogPage{Items: []watchstate.CatalogTitle{}, Total: 0},
		resolved: map[string]string{
			"imdb:tt0000001": movieID,
			"addon:sha256:99dcbea88486a8abc4c4dd1847ee0043a210a61a0096919f0e4622c312e6d2d6": customID,
		},
		titles: map[string]watchstate.CatalogTitle{
			movieID: {
				ID: movieID, MediaType: "movie", Title: "Zeta", Genres: []string{}, ProviderIDs: map[string]string{"imdb": "tt0000001"},
				ResourceID: "movie-1", ResourceProvider: "tmdb",
			},
			customID: {
				ID: customID, MediaType: "series", Title: "Middle", Genres: []string{}, ProviderIDs: map[string]string{"addon": "internal-profile-identity"},
				ResourceID: "custom", ResourceProvider: "addon", SourceAddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
				SourceCatalogID: "search", SourceName: "Trusted Add-on",
			},
		},
	}
	metadataService := &orchestrationMetadata{
		movies: metadata.MoviePage{Items: []metadata.Movie{{
			ID: movieID, MediaType: "movie", Title: "Zeta", ExternalIDs: map[string]string{"imdb": "tt0000001"}, Genres: []metadata.Genre{},
		}}, Page: 1, TotalPages: 1, TotalResults: 1},
		series: metadata.SeriesPage{Items: []metadata.Series{{
			ID: seriesID, MediaType: "series", Name: "Alpha", ExternalIDs: map[string]string{"tmdb": "2"}, Genres: []metadata.Genre{},
		}}, Page: 1, TotalPages: 1, TotalResults: 1},
	}
	addonService := &orchestrationAddons{page: addon.CatalogSearchPage{Complete: true, Items: []addon.CatalogSearchItem{
		{AddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", CatalogID: "search", ResourceID: "tt0000001", MediaType: "movie", Title: "Duplicate Zeta", ExternalIDs: map[string]string{"imdb": "tt0000001", "tvdb": "999999", "url": "https://provider.invalid/secondary"}},
		{AddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", CatalogID: "search", AddonName: "Trusted Add-on", ResourceID: "custom", MediaType: "series", Title: "Middle Untrusted", ExternalIDs: map[string]string{"addon": "untrusted-secondary", "unknown": "opaque"}},
	}}}
	readerValue, err := NewCatalogReader(store, metadataService, addonService, nil)
	if err != nil {
		t.Fatalf("new catalog reader: %v", err)
	}
	reader := readerValue.(catalogSearcher)
	page, err := reader.SearchCatalog(context.Background(), auth.Principal{}, CatalogSearchQuery{
		SearchTerm: "fixture", MediaTypes: []string{"movie", "series"}, Offset: 0, Limit: 2,
		SortBy: "sortname", SortOrder: "Descending",
	})
	if err != nil {
		t.Fatalf("search catalog: %v", err)
	}
	if !page.ExactTotal || page.Total != 3 || len(page.Items) != 2 || page.Items[0].ID != movieID || page.Items[1].ID != customID {
		t.Fatalf("unexpected sorted deduplicated page: %+v", page)
	}
	if !reflect.DeepEqual(store.titleReads, []string{movieID, customID}) {
		t.Fatalf("add-on results were not re-read through the authorized catalog: %+v", store.titleReads)
	}
	if page.Items[0].ProviderIDs["tvdb"] == "999999" || page.Items[1].ProviderIDs["addon"] == "untrusted-secondary" ||
		page.Items[1].ProviderIDs["unknown"] != "" {
		t.Fatalf("add-on secondary identities escaped canonical re-read: %+v", page.Items)
	}
	if page.Items[1].ResourceID != "custom" || page.Items[1].SourceAddonID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" ||
		page.Items[1].SourceCatalogID != "search" || page.Items[1].SourceName != "Trusted Add-on" {
		t.Fatalf("canonical add-on provenance was not preserved internally: %+v", page.Items[1])
	}
	if len(store.resolveInputs) != 2 || store.resolveInputs[1].SourceAddonID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" ||
		store.resolveInputs[1].SourceCatalogID != "search" || store.resolveInputs[1].SourceName != "Trusted Add-on" {
		t.Fatalf("verified producer was not passed to linked title resolution: %+v", store.resolveInputs)
	}
	lookup, err := readerValue.GetCatalogTitle(context.Background(), auth.Principal{}, page.Items[0].ID)
	if err != nil || lookup.ID != page.Items[0].ID || lookup.ResourceID != "movie-1" || lookup.ResourceProvider != "tmdb" {
		t.Fatalf("external search result was not immediately readable/playable: %+v error %v", lookup, err)
	}
}

func TestCatalogDetailEnrichmentPreservesAuthorizedPlaybackIdentity(t *testing.T) {
	const titleID = "10000000-0000-4000-8000-000000000010"
	runtimeMinutes := 99
	rating := float32(7.5)
	snapshot := watchstate.CatalogTitle{
		ID: titleID, MediaType: "movie", Title: "Snapshot", PosterURL: localizedArtworkPrefix + strings.Repeat("a", 64),
		Genres: []string{}, ProviderIDs: map[string]string{"imdb": "tt0000010"}, ResourceID: "opaque-resource",
		ResourceProvider: "addon", SourceAddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", SourceCatalogID: "movie", SourceName: "Trusted",
	}
	store := &orchestrationStore{titles: map[string]watchstate.CatalogTitle{titleID: snapshot}}
	metadataService := &orchestrationMetadata{movieDetail: metadata.Movie{
		ID: titleID, MediaType: "movie", Title: "Enriched", OriginalTitle: "Original", Overview: "Full overview",
		ReleaseDate: "2026-07-24", PosterURL: "https://provider.invalid/poster.jpg?secret=1", BackdropURL: "https://provider.invalid/backdrop.jpg?secret=2",
		Tagline: "The tagline", RuntimeMinutes: runtimeMinutes, Genres: []metadata.Genre{{Name: "Adventure"}},
		Cast:        []metadata.CastMember{{Name: "Performer", Character: "Hero", ProfileURL: "https://provider.invalid/cast.jpg?secret=3"}},
		VoteAverage: float64(rating), ExternalIDs: map[string]string{"tmdb": "10"},
	}}
	readerValue, err := NewCatalogReader(store, metadataService, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	enriched, err := readerValue.(catalogDetailReader).EnrichCatalogTitle(context.Background(), auth.Principal{}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(metadataService.detailIDs, []string{titleID}) || enriched.ID != titleID || enriched.ResourceID != "opaque-resource" ||
		enriched.SourceAddonID != snapshot.SourceAddonID || enriched.SourceCatalogID != snapshot.SourceCatalogID || enriched.Title != "Enriched" ||
		enriched.OriginalTitle != "Original" || enriched.Overview != "Full overview" || enriched.Tagline != "The tagline" ||
		enriched.RuntimeMinutes == nil || *enriched.RuntimeMinutes != runtimeMinutes || enriched.CommunityRating == nil || *enriched.CommunityRating != rating ||
		len(enriched.Genres) != 1 || enriched.Genres[0] != "Adventure" || len(enriched.People) != 1 || enriched.People[0].Role != "Hero" ||
		enriched.People[0].ImageURL != "" || enriched.ProviderIDs["imdb"] != "tt0000010" || enriched.ProviderIDs["tmdb"] != "10" ||
		enriched.PosterURL != snapshot.PosterURL || strings.Contains(fmt.Sprintf("%+v", enriched), "provider.invalid") {
		t.Fatalf("detail enrichment lost identity or leaked provider data: %+v calls=%v", enriched, metadataService.detailIDs)
	}
}

func TestAddonSearchIdentityScopesOpaqueResourceToProducer(t *testing.T) {
	firstProvider, firstIdentity := addonSearchIdentity(addon.CatalogSearchItem{
		AddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", MediaType: "movie", ResourceID: "shared-resource",
	})
	secondProvider, secondIdentity := addonSearchIdentity(addon.CatalogSearchItem{
		AddonID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", MediaType: "movie", ResourceID: "shared-resource",
	})
	if firstProvider != "addon" || secondProvider != "addon" || firstIdentity == secondIdentity {
		t.Fatalf("opaque identities were not producer-scoped: %s/%s %s/%s", firstProvider, firstIdentity, secondProvider, secondIdentity)
	}
}

func TestCatalogOrchestrationDoesNotInventUnknownOrPartialTotal(t *testing.T) {
	store := &orchestrationStore{local: watchstate.CatalogPage{Items: []watchstate.CatalogTitle{}, Total: 0}, resolved: map[string]string{}}
	metadataService := &orchestrationMetadata{movieErr: metadata.ErrProviderUnavailable, series: metadata.SeriesPage{Items: []metadata.Series{}, Page: 1, TotalPages: 1}}
	addonService := &orchestrationAddons{page: addon.CatalogSearchPage{Items: []addon.CatalogSearchItem{}, Complete: false}}
	readerValue, err := NewCatalogReader(store, metadataService, addonService, nil)
	if err != nil {
		t.Fatalf("new catalog reader: %v", err)
	}
	page, err := readerValue.(catalogSearcher).SearchCatalog(context.Background(), auth.Principal{}, CatalogSearchQuery{
		SearchTerm: "fixture", MediaTypes: []string{"movie", "series"}, Limit: 10,
	})
	if err != nil {
		t.Fatalf("partial search should retain successful providers: %v", err)
	}
	if page.ExactTotal || page.Total != 0 {
		t.Fatalf("partial provider search invented total: %+v", page)
	}
}

func TestCatalogOrchestrationRevalidatesLinkedSessionAfterProviderWait(t *testing.T) {
	const titleID = "10000000-0000-4000-8000-000000000004"
	store := &orchestrationStore{
		local:    watchstate.CatalogPage{Items: []watchstate.CatalogTitle{}, Total: 0},
		resolved: map[string]string{"imdb:tt0000004": titleID},
		titles: map[string]watchstate.CatalogTitle{
			titleID: {ID: titleID, MediaType: "movie", Title: "Revoked", Genres: []string{}, ProviderIDs: map[string]string{"imdb": "tt0000004"}},
		},
	}
	started := make(chan struct{})
	release := make(chan struct{})
	addonService := &orchestrationAddons{
		started: started,
		release: release,
		page: addon.CatalogSearchPage{Complete: true, Items: []addon.CatalogSearchItem{{
			AddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ResourceID: "tt0000004",
			MediaType: "movie", Title: "Revoked", ExternalIDs: map[string]string{"imdb": "tt0000004"},
		}}},
	}
	readerValue, err := NewCatalogReader(store, nil, addonService, nil)
	if err != nil {
		t.Fatalf("new catalog reader: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, searchErr := readerValue.(catalogSearcher).SearchCatalog(context.Background(), auth.Principal{SessionID: "linked-session"}, CatalogSearchQuery{
			SearchTerm: "revoked", MediaTypes: []string{"movie"}, Limit: 10,
		})
		result <- searchErr
	}()
	<-started
	store.resolveErr = watchstate.ErrForbidden
	close(release)
	if err := <-result; !errors.Is(err, watchstate.ErrForbidden) {
		t.Fatalf("search after linked-session logout error = %v, want ErrForbidden", err)
	}
	if store.resolveCalls != 1 || len(store.titleReads) != 0 {
		t.Fatalf("revoked result persisted/projected: resolve calls=%d title reads=%+v", store.resolveCalls, store.titleReads)
	}
}

func TestCatalogOrchestrationPropagatesAuthorizationAndOperationalErrors(t *testing.T) {
	for _, failure := range []error{watchstate.ErrProfileRequired, context.Canceled, errors.New("database unavailable")} {
		store := &orchestrationStore{localErr: failure}
		readerValue, err := NewCatalogReader(store, nil, nil, nil)
		if err != nil {
			t.Fatalf("new catalog reader: %v", err)
		}
		_, err = readerValue.(catalogSearcher).SearchCatalog(context.Background(), auth.Principal{}, CatalogSearchQuery{SearchTerm: "fixture", Limit: 10})
		if !errors.Is(err, failure) {
			t.Fatalf("search error=%v want=%v", err, failure)
		}
	}
}

func orchestrationMovies(start, count int) []metadata.Movie {
	items := make([]metadata.Movie, count)
	for index := range items {
		number := start + index
		items[index] = metadata.Movie{
			ID: fmt.Sprintf("20000000-0000-4000-8000-%012d", number), MediaType: "movie",
			Title: fmt.Sprintf("Movie %03d", number), ExternalIDs: map[string]string{}, Genres: []metadata.Genre{},
		}
	}
	return items
}

func TestCatalogOrchestrationSearchReturnsSecondMetadataPage(t *testing.T) {
	metadataService := &orchestrationMetadata{moviePages: map[int]metadata.MoviePage{
		1: {Items: orchestrationMovies(0, 20), Page: 1, TotalPages: 3, TotalResults: 45},
		2: {Items: orchestrationMovies(20, 20), Page: 2, TotalPages: 3, TotalResults: 45},
		3: {Items: orchestrationMovies(40, 5), Page: 3, TotalPages: 3, TotalResults: 45},
	}}
	readerValue, err := NewCatalogReader(&orchestrationStore{}, metadataService, nil, nil)
	if err != nil {
		t.Fatalf("new catalog reader: %v", err)
	}
	page, err := readerValue.(catalogSearcher).SearchCatalog(context.Background(), auth.Principal{}, CatalogSearchQuery{
		SearchTerm: "movie", MediaTypes: []string{"movie"}, Offset: 20, Limit: 20,
	})
	if err != nil {
		t.Fatalf("search second page: %v", err)
	}
	if len(page.Items) != 20 || page.Items[0].ID != orchestrationMovies(20, 1)[0].ID || page.Items[19].ID != orchestrationMovies(39, 1)[0].ID {
		t.Fatalf("unexpected second page: %+v", page)
	}
	if !reflect.DeepEqual(metadataService.movieCalls, []int{1, 2}) {
		t.Fatalf("metadata calls = %v, want [1 2]", metadataService.movieCalls)
	}
}

func TestCatalogOrchestrationMetadataDeduplicationFillsRequestedWindow(t *testing.T) {
	first := orchestrationMovies(0, 20)
	second := append(append([]metadata.Movie(nil), first[:10]...), orchestrationMovies(20, 10)...)
	third := orchestrationMovies(30, 20)
	metadataService := &orchestrationMetadata{moviePages: map[int]metadata.MoviePage{
		1: {Items: first, Page: 1, TotalPages: 3, TotalResults: 60},
		2: {Items: second, Page: 2, TotalPages: 3, TotalResults: 60},
		3: {Items: third, Page: 3, TotalPages: 3, TotalResults: 60},
	}}
	readerValue, err := NewCatalogReader(&orchestrationStore{}, metadataService, nil, nil)
	if err != nil {
		t.Fatalf("new catalog reader: %v", err)
	}
	page, err := readerValue.(catalogSearcher).SearchCatalog(context.Background(), auth.Principal{}, CatalogSearchQuery{
		SearchTerm: "movie", MediaTypes: []string{"movie"}, Offset: 20, Limit: 20,
	})
	if err != nil {
		t.Fatalf("search deduplicated page: %v", err)
	}
	if len(page.Items) != 20 || page.Items[0].ID != orchestrationMovies(20, 1)[0].ID || page.Items[19].ID != orchestrationMovies(39, 1)[0].ID {
		t.Fatalf("deduplicated page did not fill window: %+v", page)
	}
	if !reflect.DeepEqual(metadataService.movieCalls, []int{1, 2, 3}) {
		t.Fatalf("metadata calls = %v, want [1 2 3]", metadataService.movieCalls)
	}
}

func TestCatalogOrchestrationMetadataPagingIsBounded(t *testing.T) {
	pages := make(map[int]metadata.MoviePage, maximumCatalogMetadataPagesPerType)
	duplicate := orchestrationMovies(0, 1)
	for page := 1; page <= maximumCatalogMetadataPagesPerType; page++ {
		pages[page] = metadata.MoviePage{Items: duplicate, Page: page, TotalPages: 100, TotalResults: 100}
	}
	metadataService := &orchestrationMetadata{moviePages: pages}
	readerValue, err := NewCatalogReader(&orchestrationStore{}, metadataService, nil, nil)
	if err != nil {
		t.Fatalf("new catalog reader: %v", err)
	}
	result, err := readerValue.(catalogSearcher).SearchCatalog(context.Background(), auth.Principal{}, CatalogSearchQuery{
		SearchTerm: "duplicate", MediaTypes: []string{"movie"}, Offset: 20, Limit: 20,
	})
	if err != nil {
		t.Fatalf("bounded search: %v", err)
	}
	if len(metadataService.movieCalls) != maximumCatalogMetadataPagesPerType || len(result.Items) != 0 || result.ExactTotal {
		t.Fatalf("unbounded or inexact result: calls=%v page=%+v", metadataService.movieCalls, result)
	}
}

func TestCatalogOrchestrationStopsWhenLaterMetadataPageLosesAuthority(t *testing.T) {
	metadataService := &orchestrationMetadata{
		moviePages: map[int]metadata.MoviePage{
			1: {Items: orchestrationMovies(0, 20), Page: 1, TotalPages: 2, TotalResults: 40},
		},
		movieErrors: map[int]error{2: metadata.ErrProfileRequired},
	}
	addons := &orchestrationAddons{page: addon.CatalogSearchPage{Complete: true}}
	store := &orchestrationStore{}
	readerValue, err := NewCatalogReader(store, metadataService, addons, nil)
	if err != nil {
		t.Fatalf("new catalog reader: %v", err)
	}
	page, err := readerValue.(catalogSearcher).SearchCatalog(context.Background(), auth.Principal{SessionID: "revoked"}, CatalogSearchQuery{
		SearchTerm: "movie", MediaTypes: []string{"movie"}, Offset: 20, Limit: 20,
	})
	if !errors.Is(err, watchstate.ErrProfileRequired) {
		t.Fatalf("later-page revocation error = %v, want ErrProfileRequired", err)
	}
	if len(page.Items) != 0 || addons.calls != 0 || store.resolveCalls != 0 || !reflect.DeepEqual(metadataService.movieCalls, []int{1, 2}) {
		t.Fatalf("revoked search escaped pagination boundary: page=%+v addon calls=%d resolves=%d metadata=%v", page, addons.calls, store.resolveCalls, metadataService.movieCalls)
	}
}
