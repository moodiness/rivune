package collection

import (
	"context"
	"testing"

	"github.com/moodiness/rivune/server/internal/auth"
)

type artworkTMDBProvider struct {
	page SourcePage
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
	service := NewService(nil, nil, provider, nil)

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

func TestMergeItemsKeepsMovieAndSeriesWithSameTMDBID(t *testing.T) {
	items := mergeItems([]Item{
		{ID: "tmdb:42", MediaType: MediaTypeMovie, Title: "Movie", ExternalIDs: map[string]string{"tmdb": "42"}},
		{ID: "tmdb:42", MediaType: MediaTypeSeries, Title: "Series", ExternalIDs: map[string]string{"tmdb": "42"}},
	})
	if len(items) != 2 {
		t.Fatalf("merged movie and series with the same TMDB ID: %+v", items)
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
