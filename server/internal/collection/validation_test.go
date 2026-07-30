package collection

import (
	"errors"
	"testing"
)

func TestNormalizeAndValidateAcceptsEveryTMDBSourceType(t *testing.T) {
	id := int64(42)
	tests := []struct {
		name          string
		sourceType    string
		mediaType     string
		needsID       bool
		wantMediaType string
	}{
		{name: "public list", sourceType: "list", mediaType: MediaTypeMovie, needsID: true, wantMediaType: MediaTypeMovie},
		{name: "production company", sourceType: "company", mediaType: MediaTypeMovie, needsID: true, wantMediaType: MediaTypeMovie},
		{name: "network", sourceType: "network", mediaType: MediaTypeSeries, needsID: true, wantMediaType: MediaTypeSeries},
		{name: "movie collection", sourceType: "collection", mediaType: MediaTypeMovie, needsID: true, wantMediaType: MediaTypeMovie},
		{name: "person credits", sourceType: "person", mediaType: MediaTypeSeries, needsID: true, wantMediaType: MediaTypeSeries},
		{name: "director credits", sourceType: "director", mediaType: MediaTypeMovie, needsID: true, wantMediaType: MediaTypeMovie},
		{name: "custom discover", sourceType: "discover", mediaType: MediaTypeMovie, wantMediaType: MediaTypeMovie},
		{name: "public list forces movie", sourceType: "list", mediaType: MediaTypeBoth, needsID: true, wantMediaType: MediaTypeMovie},
		{name: "production company both", sourceType: "company", mediaType: MediaTypeBoth, needsID: true, wantMediaType: MediaTypeBoth},
		{name: "network forces series", sourceType: "network", mediaType: MediaTypeBoth, needsID: true, wantMediaType: MediaTypeSeries},
		{name: "movie collection forces movie", sourceType: "collection", mediaType: MediaTypeBoth, needsID: true, wantMediaType: MediaTypeMovie},
		{name: "person credits both", sourceType: "person", mediaType: MediaTypeBoth, needsID: true, wantMediaType: MediaTypeBoth},
		{name: "director credits both", sourceType: "director", mediaType: MediaTypeBoth, needsID: true, wantMediaType: MediaTypeBoth},
		{name: "custom discover both", sourceType: "discover", mediaType: MediaTypeBoth, wantMediaType: MediaTypeBoth},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := TMDBSource{SourceType: test.sourceType, MediaType: test.mediaType, Sort: "popularity.desc"}
			if test.needsID {
				source.TMDBID = &id
			}
			input := SaveInput{
				Title: "Curated",
				Folders: []Folder{{
					Title:   "Featured",
					Sources: []Source{{Kind: SourceKindTMDB, Title: test.name, TMDB: &source}},
				}},
			}

			normalized, err := normalizeAndValidate(input, false)
			if err != nil {
				t.Fatalf("normalize source: %v", err)
			}
			got := normalized.Folders[0].Sources[0].TMDB
			if got.SourceType != test.sourceType || got.MediaType != test.wantMediaType {
				t.Fatalf("unexpected normalized TMDB source: %+v", *got)
			}
			if normalized.Folders[0].ID == "" || normalized.Folders[0].Sources[0].ID == "" {
				t.Fatal("folder and source IDs were not generated")
			}
		})
	}
}

func TestNormalizeAndValidateFolderSourceView(t *testing.T) {
	validSource := Source{
		Kind: SourceKindTMDB, Title: "Discover",
		TMDB: &TMDBSource{SourceType: "discover", MediaType: MediaTypeMovie, Sort: "popularity.desc"},
	}
	input := SaveInput{Title: "Curated", Folders: []Folder{{Title: "Featured", Sources: []Source{validSource}}}}

	normalized, err := normalizeAndValidate(input, false)
	if err != nil {
		t.Fatalf("normalize default source view: %v", err)
	}
	if normalized.Folders[0].SourceView != SourceViewMerged {
		t.Fatalf("expected merged default, got %q", normalized.Folders[0].SourceView)
	}

	for _, sourceView := range []string{SourceViewMerged, SourceViewCategories, SourceViewFolders} {
		input.Folders[0].SourceView = sourceView
		normalized, err := normalizeAndValidate(input, false)
		if err != nil {
			t.Fatalf("normalize %q source view: %v", sourceView, err)
		}
		if normalized.Folders[0].SourceView != sourceView {
			t.Fatalf("expected %q source view, got %q", sourceView, normalized.Folders[0].SourceView)
		}
	}

	input.Folders[0].SourceView = "unsupported"
	if _, err := normalizeAndValidate(input, false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid source view error, got %v", err)
	}
}

func TestNormalizeAndValidateFolderCoverShape(t *testing.T) {
	input := SaveInput{
		Title: "Curated",
		Folders: []Folder{{
			Title: "Featured",
			Sources: []Source{{
				Kind: SourceKindTMDB, Title: "Discover",
				TMDB: &TMDBSource{SourceType: "discover", MediaType: MediaTypeMovie, Sort: "popularity.desc"},
			}},
		}},
	}

	normalized, err := normalizeAndValidate(input, false)
	if err != nil {
		t.Fatalf("normalize default folder cover shape: %v", err)
	}
	if normalized.FolderCoverShape != TileShapePoster {
		t.Fatalf("expected poster default, got %q", normalized.FolderCoverShape)
	}

	for _, shape := range []string{TileShapePoster, TileShapeLandscape, TileShapeSquare} {
		input.FolderCoverShape = shape
		normalized, err := normalizeAndValidate(input, false)
		if err != nil {
			t.Fatalf("normalize %q folder cover shape: %v", shape, err)
		}
		if normalized.FolderCoverShape != shape {
			t.Fatalf("expected %q folder cover shape, got %q", shape, normalized.FolderCoverShape)
		}
	}

	input.FolderCoverShape = "unsupported"
	if _, err := normalizeAndValidate(input, false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid folder cover shape error, got %v", err)
	}
}

func TestNormalizeAndValidateRejectsMismatchedSourceConfiguration(t *testing.T) {
	input := SaveInput{
		Title: "Invalid",
		Folders: []Folder{{
			Title: "Folder",
			Sources: []Source{{
				Kind: SourceKindTMDB, Title: "Mixed",
				TMDB:  &TMDBSource{SourceType: "discover"},
				Trakt: &TraktSource{ListID: 1, MediaType: MediaTypeMovie},
			}},
		}},
	}

	_, err := normalizeAndValidate(input, false)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}

func TestMergeItemsUsesFirstSourcePrecedenceAndPreservesProvenance(t *testing.T) {
	first := Item{
		ID: "movie:42", MediaType: MediaTypeMovie, Title: "First title", PosterURL: "https://first.example/poster.jpg",
		ExternalIDs: map[string]string{"tmdb": "42"}, Sources: []SourceReference{{ID: "source-one", Kind: SourceKindTMDB}},
	}
	second := Item{
		ID: "tt0000042", MediaType: MediaTypeMovie, Title: "Second title", PosterURL: "https://second.example/poster.jpg",
		BackgroundURL: "https://second.example/background.jpg", ExternalIDs: map[string]string{"tmdb": "42", "imdb": "tt0000042"},
		Sources: []SourceReference{{ID: "source-two", Kind: SourceKindAddonCatalog}},
	}

	merged := mergeItems([]Item{first, second})
	if len(merged) != 1 {
		t.Fatalf("expected one deduplicated item, got %d", len(merged))
	}
	item := merged[0]
	if item.Title != "First title" || item.PosterURL != first.PosterURL {
		t.Fatalf("first source did not retain precedence: %+v", item)
	}
	if item.BackgroundURL != second.BackgroundURL || item.ExternalIDs["imdb"] != "tt0000042" {
		t.Fatalf("missing fields were not enriched: %+v", item)
	}
	if len(item.Sources) != 2 || item.Sources[0].ID != "source-one" || item.Sources[1].ID != "source-two" {
		t.Fatalf("source provenance was not preserved: %+v", item.Sources)
	}
}
