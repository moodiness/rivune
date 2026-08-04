package collection

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/metadata"
)

type artworkTMDBProvider struct {
	page     SourcePage
	resolved map[string]string
	series   metadata.ProviderSeries
}

func (provider artworkTMDBProvider) ResolveCollectionSource(context.Context, TMDBSource, int, string, string) (SourcePage, error) {
	return provider.page, nil
}

func (artworkTMDBProvider) LookupCollectionSource(context.Context, string, string, string, int) ([]LookupResult, error) {
	return nil, nil
}

func (artworkTMDBProvider) CollectionGenres(context.Context, string, string) ([]Genre, error) {
	return nil, nil
}

func (provider artworkTMDBProvider) ResolveExternalID(_ context.Context, _ string, source, externalID string) (string, error) {
	return provider.resolved[source+":"+externalID], nil
}

func (provider artworkTMDBProvider) SeriesDetails(context.Context, string, string) (metadata.ProviderSeries, error) {
	return provider.series, nil
}

type recordingFanartEnricher struct {
	mu                sync.Mutex
	collectionArtwork map[string]metadata.ProviderCollection
	collections       []string
	movies            []string
	series            []string
}

func (enricher *recordingFanartEnricher) EnrichCollection(_ context.Context, collection metadata.ProviderCollection, _ string) (metadata.ProviderCollection, error) {
	enricher.mu.Lock()
	enricher.collections = append(enricher.collections, collection.ExternalID)
	enricher.mu.Unlock()
	if artwork, configured := enricher.collectionArtwork[collection.ExternalID]; configured {
		artwork.ExternalID = collection.ExternalID
		return artwork, nil
	}
	collection.PosterURL = "https://assets.fanart.tv/collection-" + collection.ExternalID + "-poster.jpg"
	collection.BackdropURL = "https://assets.fanart.tv/collection-" + collection.ExternalID + "-background.jpg"
	collection.LogoURL = "https://assets.fanart.tv/collection-" + collection.ExternalID + "-logo.png"
	return collection, nil
}

func (enricher *recordingFanartEnricher) EnrichMovie(_ context.Context, movie metadata.ProviderMovie, _ string) (metadata.ProviderMovie, error) {
	enricher.mu.Lock()
	enricher.movies = append(enricher.movies, movie.AdditionalIDs["tmdb"])
	enricher.mu.Unlock()
	movie.PosterURL = "https://assets.fanart.tv/movie-" + movie.AdditionalIDs["tmdb"] + "-poster.jpg"
	movie.BackdropURL = "https://assets.fanart.tv/movie-" + movie.AdditionalIDs["tmdb"] + "-background.jpg"
	movie.LogoURL = "https://assets.fanart.tv/movie-" + movie.AdditionalIDs["tmdb"] + "-logo.png"
	return movie, nil
}

func (enricher *recordingFanartEnricher) EnrichSeries(_ context.Context, series metadata.ProviderSeries, _ string) (metadata.ProviderSeries, error) {
	enricher.mu.Lock()
	enricher.series = append(enricher.series, series.AdditionalIDs["tvdb"])
	enricher.mu.Unlock()
	series.PosterURL = "https://assets.fanart.tv/series-" + series.AdditionalIDs["tvdb"] + "-poster.jpg"
	series.BackdropURL = "https://assets.fanart.tv/series-" + series.AdditionalIDs["tvdb"] + "-background.jpg"
	series.LogoURL = "https://assets.fanart.tv/series-" + series.AdditionalIDs["tvdb"] + "-logo.png"
	return series, nil
}

type stubMDBListProvider struct {
	source MDBListSource
	page   int
}

func (provider *stubMDBListProvider) ResolveCollectionSource(_ context.Context, source MDBListSource, page int) (SourcePage, error) {
	provider.source = source
	provider.page = page
	return SourcePage{Items: []Item{{ID: "tmdb:42", MediaType: MediaTypeMovie, Title: "Movie"}}}, nil
}

type paginatedAddonProvider struct {
	paths []addon.ResourcePath
}

func (provider *paginatedAddonProvider) Fetch(_ context.Context, _ auth.Principal, _ string, path addon.ResourcePath) (addon.ResourceResult, error) {
	provider.paths = append(provider.paths, path)
	skip := ""
	for _, extra := range path.Extra {
		if extra.Name == "skip" {
			skip = extra.Value
			break
		}
	}
	count := map[string]int{"": 20, "20": 2, "40": 0}[skip]
	metas := make([]map[string]string, count)
	for index := range metas {
		metas[index] = map[string]string{"id": skip + "-item", "type": "movie", "name": "Movie"}
	}
	payload, err := json.Marshal(map[string]any{"metas": metas})
	if err != nil {
		return addon.ResourceResult{}, err
	}
	return addon.ResourceResult{Payload: payload}, nil
}

func TestResolveAddonCatalogPaginatesShortResponses(t *testing.T) {
	provider := &paginatedAddonProvider{}
	service := NewService(nil, provider, nil, nil, nil)
	source := Source{
		Kind: SourceKindAddonCatalog,
		AddonCatalog: &AddonCatalogSource{
			AddonID: "addon-id", Type: MediaTypeMovie, CatalogID: "popular",
		},
	}
	wantCounts := []int{20, 2, 0}
	wantMore := []bool{true, true, false}
	for pageNumber := 1; pageNumber <= 3; pageNumber++ {
		page, err := service.resolveSource(context.Background(), auth.Principal{}, source, pageNumber, "fr-FR", "FR")
		if err != nil {
			t.Fatalf("resolve addon page %d: %v", pageNumber, err)
		}
		if len(page.Items) != wantCounts[pageNumber-1] || page.HasMore != wantMore[pageNumber-1] {
			t.Fatalf("page %d = %d items, hasMore=%t", pageNumber, len(page.Items), page.HasMore)
		}
	}
	for index, want := range []string{"", "20", "40"} {
		got := ""
		for _, extra := range provider.paths[index].Extra {
			if extra.Name == "skip" {
				got = extra.Value
			}
		}
		if got != want {
			t.Fatalf("page %d skip = %q, want %q", index+1, got, want)
		}
	}
}

func TestResolveHydratesFolderArtworkWithoutOverridingConfiguration(t *testing.T) {
	tests := []struct {
		name         string
		folder       Folder
		wantCover    string
		wantBackdrop string
	}{
		{
			name:      "provider artwork",
			folder:    Folder{Title: "James Bond"},
			wantCover: "https://image.tmdb.org/collection-poster.jpg", wantBackdrop: "https://image.tmdb.org/collection-backdrop.jpg",
		},
		{
			name:      "configured artwork wins",
			folder:    Folder{Title: "James Bond", CoverImageURL: "https://example.com/poster.jpg", HeroBackdropURL: "https://example.com/backdrop.jpg"},
			wantCover: "https://example.com/poster.jpg", wantBackdrop: "https://example.com/backdrop.jpg",
		},
	}

	provider := artworkTMDBProvider{page: SourcePage{
		CoverImageURL:   "https://image.tmdb.org/collection-poster.jpg",
		HeroBackdropURL: "https://image.tmdb.org/collection-backdrop.jpg",
	}}
	service := NewService(nil, nil, provider, nil, nil)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := TMDBSource{SourceType: "collection", MediaType: MediaTypeMovie}
			test.folder.Sources = []Source{{Kind: SourceKindTMDB, Title: "Movie collection", TMDB: &source}}
			resolved, err := service.resolve(context.Background(), auth.Principal{}, "collection-id", test.folder, 1, 100, "en-US", "US")
			if err != nil {
				t.Fatalf("resolve folder: %v", err)
			}
			if resolved.Folder.CoverImageURL != test.wantCover || resolved.Folder.HeroBackdropURL != test.wantBackdrop {
				t.Fatalf("folder artwork = (%q, %q), want (%q, %q)", resolved.Folder.CoverImageURL, resolved.Folder.HeroBackdropURL, test.wantCover, test.wantBackdrop)
			}
		})
	}
}

func TestResolveFetchesFanartForItemsAndAutomaticFolderArtwork(t *testing.T) {
	tests := []struct {
		name         string
		folder       Folder
		wantCover    string
		wantBackdrop string
	}{
		{
			name:         "automatic folder artwork uses Fanart",
			folder:       Folder{Title: "Fanart collection"},
			wantCover:    "https://assets.fanart.tv/movie-550-poster.jpg",
			wantBackdrop: "https://assets.fanart.tv/movie-550-background.jpg",
		},
		{
			name: "configured folder artwork wins",
			folder: Folder{
				Title: "Configured collection", CoverImageURL: "https://example.com/custom-cover.jpg",
				HeroBackdropURL: "https://example.com/custom-background.jpg",
			},
			wantCover:    "https://example.com/custom-cover.jpg",
			wantBackdrop: "https://example.com/custom-background.jpg",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := artworkTMDBProvider{
				page: SourcePage{
					CoverImageURL:   "https://image.tmdb.org/source-cover.jpg",
					HeroBackdropURL: "https://image.tmdb.org/source-background.jpg",
					Items: []Item{
						{
							ID: "tt0137523", MediaType: MediaTypeMovie, Title: "Fight Club",
							PosterURL: "https://image.tmdb.org/movie-poster.jpg", ExternalIDs: map[string]string{"imdb": "tt0137523"},
						},
						{
							ID: "tmdb:1396", MediaType: MediaTypeSeries, Title: "Breaking Bad",
							PosterURL: "https://image.tmdb.org/series-poster.jpg", ExternalIDs: map[string]string{"tmdb": "1396"},
						},
					},
				},
				resolved: map[string]string{"imdb:tt0137523": "550"},
				series:   metadata.ProviderSeries{AdditionalIDs: map[string]string{"tvdb": "81189"}},
			}
			enricher := &recordingFanartEnricher{}
			service := NewService(nil, nil, provider, nil, nil)
			service.SetFanartEnricher(provider, provider, enricher, nil)
			source := TMDBSource{SourceType: "collection", MediaType: MediaTypeBoth}
			test.folder.Sources = []Source{{Kind: SourceKindTMDB, Title: "Mixed collection", TMDB: &source}}

			resolved, err := service.resolve(context.Background(), auth.Principal{}, "collection-id", test.folder, 1, 100, "fr-FR", "FR")
			if err != nil {
				t.Fatalf("resolve folder: %v", err)
			}
			if resolved.Folder.CoverImageURL != test.wantCover || resolved.Folder.HeroBackdropURL != test.wantBackdrop {
				t.Fatalf("folder artwork = (%q, %q), want (%q, %q)",
					resolved.Folder.CoverImageURL, resolved.Folder.HeroBackdropURL, test.wantCover, test.wantBackdrop)
			}
			if len(resolved.Items) != 2 ||
				resolved.Items[0].PosterURL != "https://assets.fanart.tv/movie-550-poster.jpg" ||
				resolved.Items[0].BackgroundURL != "https://assets.fanart.tv/movie-550-background.jpg" ||
				resolved.Items[0].LogoURL != "https://assets.fanart.tv/movie-550-logo.png" ||
				resolved.Items[1].PosterURL != "https://assets.fanart.tv/series-81189-poster.jpg" ||
				resolved.Items[1].BackgroundURL != "https://assets.fanart.tv/series-81189-background.jpg" ||
				resolved.Items[1].LogoURL != "https://assets.fanart.tv/series-81189-logo.png" ||
				!resolved.Items[0].FanartResolved || !resolved.Items[1].FanartResolved {
				t.Fatalf("items did not use direct Fanart artwork: %+v", resolved.Items)
			}
			if len(enricher.movies) != 1 || enricher.movies[0] != "550" || len(enricher.series) != 1 || enricher.series[0] != "81189" {
				t.Fatalf("unexpected Fanart requests: movies=%v series=%v", enricher.movies, enricher.series)
			}
		})
	}
}

func TestResolveUsesTMDBCollectionFanartBeforeMovieArtwork(t *testing.T) {
	collectionTMDBID := int64(87096)
	provider := artworkTMDBProvider{
		page: SourcePage{
			CoverImageURL:   "https://image.tmdb.org/collection-poster.jpg",
			HeroBackdropURL: "https://image.tmdb.org/collection-background.jpg",
			Items: []Item{{
				ID:          "tmdb:19995",
				MediaType:   MediaTypeMovie,
				Title:       "Avatar",
				ExternalIDs: map[string]string{"tmdb": "19995"},
			}},
		},
	}
	enricher := &recordingFanartEnricher{}
	service := NewService(nil, nil, provider, nil, nil)
	service.SetFanartEnricher(provider, provider, enricher, nil)
	folder := Folder{
		Title: "Avatar",
		Sources: []Source{{
			ID:    "avatar-source",
			Kind:  SourceKindTMDB,
			Title: "Avatar collection",
			TMDB: &TMDBSource{
				SourceType: "collection",
				TMDBID:     &collectionTMDBID,
				MediaType:  MediaTypeMovie,
			},
		}},
	}

	resolved, err := service.resolve(context.Background(), auth.Principal{}, "collection-id", folder, 1, 100, "fr-FR", "FR")
	if err != nil {
		t.Fatalf("resolve Avatar collection folder: %v", err)
	}
	if resolved.Folder.CoverImageURL != "https://assets.fanart.tv/collection-87096-poster.jpg" ||
		resolved.Folder.HeroBackdropURL != "https://assets.fanart.tv/collection-87096-background.jpg" ||
		resolved.Folder.TitleLogoURL != "https://assets.fanart.tv/collection-87096-logo.png" {
		t.Fatalf("folder did not use collection-level Fanart: %+v", resolved.Folder)
	}
	if resolved.SourcePosterURLs["avatar-source"] != "https://assets.fanart.tv/collection-87096-poster.jpg" {
		t.Fatalf("source folder did not use its collection-level Fanart poster: %+v", resolved.SourcePosterURLs)
	}
	if len(resolved.Items) != 1 ||
		resolved.Items[0].PosterURL != "https://assets.fanart.tv/movie-19995-poster.jpg" {
		t.Fatalf("collection item did not retain its own Fanart artwork: %+v", resolved.Items)
	}
	if len(enricher.collections) != 1 || enricher.collections[0] != "87096" ||
		len(enricher.movies) != 1 || enricher.movies[0] != "19995" {
		t.Fatalf("unexpected Fanart identities: collections=%v movies=%v", enricher.collections, enricher.movies)
	}
}

func TestResolveTriesEveryTMDBCollectionForFolderFanart(t *testing.T) {
	firstCollectionID := int64(948485)
	secondCollectionID := int64(263)
	provider := artworkTMDBProvider{
		page: SourcePage{
			CoverImageURL:   "https://image.tmdb.org/movie-poster.jpg",
			HeroBackdropURL: "https://image.tmdb.org/movie-background.jpg",
		},
	}
	enricher := &recordingFanartEnricher{
		collectionArtwork: map[string]metadata.ProviderCollection{
			"948485": {},
			"263": {
				PosterURL:   "https://assets.fanart.tv/dark-knight-collection-poster.jpg",
				BackdropURL: "https://assets.fanart.tv/dark-knight-collection-background.jpg",
				LogoURL:     "https://assets.fanart.tv/dark-knight-collection-logo.png",
			},
		},
	}
	service := NewService(nil, nil, provider, nil, nil)
	service.SetFanartEnricher(provider, provider, enricher, nil)
	folder := Folder{
		Title: "Batman",
		Sources: []Source{
			{
				ID:    "the-batman-source",
				Kind:  SourceKindTMDB,
				Title: "The Batman",
				TMDB: &TMDBSource{
					SourceType: "collection",
					TMDBID:     &firstCollectionID,
					MediaType:  MediaTypeMovie,
				},
			},
			{
				ID:    "dark-knight-source",
				Kind:  SourceKindTMDB,
				Title: "The Dark Knight",
				TMDB: &TMDBSource{
					SourceType: "collection",
					TMDBID:     &secondCollectionID,
					MediaType:  MediaTypeMovie,
				},
			},
		},
	}

	resolved, err := service.resolve(context.Background(), auth.Principal{}, "collection-id", folder, 1, 100, "fr-FR", "FR")
	if err != nil {
		t.Fatalf("resolve multi-collection folder: %v", err)
	}
	if resolved.Folder.CoverImageURL != "https://assets.fanart.tv/dark-knight-collection-poster.jpg" ||
		resolved.Folder.HeroBackdropURL != "https://assets.fanart.tv/dark-knight-collection-background.jpg" ||
		resolved.Folder.TitleLogoURL != "https://assets.fanart.tv/dark-knight-collection-logo.png" {
		t.Fatalf("folder did not use the next collection with Fanart: %+v", resolved.Folder)
	}
	if _, exists := resolved.SourcePosterURLs["the-batman-source"]; exists ||
		resolved.SourcePosterURLs["dark-knight-source"] != "https://assets.fanart.tv/dark-knight-collection-poster.jpg" {
		t.Fatalf("source posters did not preserve collection identities: %+v", resolved.SourcePosterURLs)
	}
	requested := make(map[string]bool, len(enricher.collections))
	for _, id := range enricher.collections {
		requested[id] = true
	}
	if len(requested) != 2 || !requested["948485"] || !requested["263"] {
		t.Fatalf("did not try every configured TMDB collection: %v", enricher.collections)
	}
}

func TestEnrichFanartArtworkSkipsLiveTVOnlySources(t *testing.T) {
	enricher := &recordingFanartEnricher{}
	service := NewService(nil, nil, nil, nil, nil)
	service.SetFanartEnricher(nil, nil, enricher, nil)
	sources := []Source{{
		Kind: SourceKindAddonCatalog,
		AddonCatalog: &AddonCatalogSource{
			Type:      MediaTypeTV,
			AddonID:   "addon-id",
			CatalogID: "live",
		},
	}}
	items := []Item{{
		ID:          "tmdb:550",
		MediaType:   MediaTypeMovie,
		Title:       "Live channel",
		PosterURL:   "https://addon.example/channel.png",
		ExternalIDs: map[string]string{"tmdb": "550"},
	}}

	artwork, folderArtwork, sourcePosterURLs := service.enrichFanartArtwork(context.Background(), sources, items, "fr-FR")

	if artwork != nil || folderArtwork.available() || sourcePosterURLs != nil {
		t.Fatalf("live TV folder unexpectedly resolved Fanart: items=%+v folder=%+v", artwork, folderArtwork)
	}
	if len(enricher.movies) != 0 || len(enricher.series) != 0 || len(enricher.collections) != 0 {
		t.Fatalf("live TV folder called Fanart: movies=%v series=%v collections=%v", enricher.movies, enricher.series, enricher.collections)
	}
	if items[0].PosterURL != "https://addon.example/channel.png" || items[0].FanartResolved {
		t.Fatalf("live TV addon artwork was replaced: %+v", items[0])
	}
}

func TestResolveUsesMDBListProvider(t *testing.T) {
	provider := &stubMDBListProvider{}
	service := NewService(nil, nil, nil, nil, provider)
	settings := MDBListSource{ListID: 12, MediaType: MediaTypeMovie, Sort: "rank", Order: "asc"}
	folder := Folder{Sources: []Source{{ID: "source-id", Kind: SourceKindMDBList, Title: "MDBList", MDBList: &settings}}}

	resolved, err := service.resolve(context.Background(), auth.Principal{}, "collection-id", folder, 3, 100, "en-US", "US")
	if err != nil {
		t.Fatalf("resolve MDBList folder: %v", err)
	}
	if provider.source != settings || provider.page != 3 || len(resolved.Items) != 1 ||
		len(resolved.Items[0].Sources) != 1 || resolved.Items[0].Sources[0].Kind != SourceKindMDBList {
		t.Fatalf("unexpected MDBList resolution: provider=%+v resolved=%+v", provider, resolved)
	}
}

func TestMergeItemsKeepsMovieAndSeriesWithSameTMDBID(t *testing.T) {
	items := mergeItems([]Item{
		{ID: "tmdb:42", MediaType: MediaTypeMovie, Title: "Movie", ExternalIDs: map[string]string{"tmdb": "42"}},
		{ID: "tmdb:42", MediaType: MediaTypeSeries, Title: "Series", ExternalIDs: map[string]string{"tmdb": "42"}},
	})
	if len(items) != 2 {
		t.Fatalf("merged movie and series with the same TMDB ID: %+v", items)
	}
}

type staticAddonProvider struct {
	result addon.ResourceResult
}

func (provider staticAddonProvider) Fetch(context.Context, auth.Principal, string, addon.ResourcePath) (addon.ResourceResult, error) {
	return provider.result, nil
}

func TestResolveProjectsAddonCatalogIdentity(t *testing.T) {
	service := NewService(nil, staticAddonProvider{result: addon.ResourceResult{
		Payload: []byte(`{"metas":[{"id":"channel-1","type":"tv","name":"News"}]}`),
	}}, nil, nil, nil)
	folder := Folder{Sources: []Source{{
		ID: "source-id", Kind: SourceKindAddonCatalog, Title: "Live",
		AddonCatalog: &AddonCatalogSource{
			AddonID: "addon-id", ManifestID: "org.example.live", Type: MediaTypeTV, CatalogID: "news",
		},
	}}}

	resolved, err := service.resolve(context.Background(), auth.Principal{}, "collection-id", folder, 1, 100, "en-US", "US")
	if err != nil {
		t.Fatalf("resolve addon catalog identity: %v", err)
	}
	if len(resolved.Items) != 1 || len(resolved.Items[0].Sources) != 1 {
		t.Fatalf("unexpected resolved items: %+v", resolved.Items)
	}
	reference := resolved.Items[0].Sources[0]
	if reference.AddonID != "addon-id" || reference.ManifestID != "org.example.live" || reference.CatalogID != "news" {
		t.Fatalf("addon catalog identity was not projected: %+v", reference)
	}
}

func TestMergeItemsScopesLiveTVIdentityToAddonAndResource(t *testing.T) {
	items := mergeItems([]Item{
		{
			ID: "channel-1", MediaType: MediaTypeTV, Title: "News",
			ExternalIDs: map[string]string{"tvdb": "same-metadata"},
			Sources:     []SourceReference{{AddonID: "addon-a", CatalogID: "news"}},
		},
		{
			ID: "channel-1", MediaType: MediaTypeTV, Title: "News",
			ExternalIDs: map[string]string{"tvdb": "same-metadata"},
			Sources:     []SourceReference{{AddonID: "addon-a", CatalogID: "all"}},
		},
		{
			ID: "channel-1", MediaType: MediaTypeTV, Title: "News",
			ExternalIDs: map[string]string{"tvdb": "same-metadata"},
			Sources:     []SourceReference{{AddonID: "addon-b", CatalogID: "news"}},
		},
	})

	if len(items) != 2 {
		t.Fatalf("unexpected live TV merge result: %+v", items)
	}
	if len(items[0].Sources) != 2 || items[0].Sources[0].AddonID != "addon-a" || items[0].Sources[1].CatalogID != "all" {
		t.Fatalf("same addon channel was not deduplicated across catalogs: %+v", items[0])
	}
	if len(items[1].Sources) != 1 || items[1].Sources[0].AddonID != "addon-b" {
		t.Fatalf("distinct addon channel was merged: %+v", items[1])
	}
}

func TestNormalizeMediaTypePreservesLiveTV(t *testing.T) {
	if got := normalizeMediaType("tv"); got != MediaTypeTV {
		t.Fatalf("live TV type normalized to %q", got)
	}
	if got := normalizeMediaType("show"); got != MediaTypeSeries {
		t.Fatalf("show type normalized to %q", got)
	}
}
