package collection

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestParseSemanticQueryUnderstandsFrenchExamples(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantTypes  []string
		wantIntent []string
		wantTitle  string
	}{
		{
			name: "war movie", query: "film de guerre", wantTypes: []string{"movie"},
			wantIntent: []string{"media_type:movie", "genre:war"}, wantTitle: "film de guerre",
		},
		{
			name: "space movie", query: "film dans l'espace", wantTypes: []string{"movie"},
			wantIntent: []string{"media_type:movie", "theme:space"}, wantTitle: "film dans l'espace",
		},
		{
			name: "british crime series", query: "série policière britannique", wantTypes: []string{"series"},
			wantIntent: []string{"media_type:series", "genre:crime", "country:gb"}, wantTitle: "série policière britannique",
		},
		{
			name: "title plus type", query: "film Dune", wantTypes: []string{"movie"},
			wantIntent: []string{"media_type:movie"}, wantTitle: "Dune",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed := parseSemanticQuery(test.query, "", nil)
			if !slices.Equal(parsed.mediaTypes, test.wantTypes) || parsed.titleQuery != test.wantTitle {
				t.Fatalf("parsed query = types %v title %q, want types %v title %q", parsed.mediaTypes, parsed.titleQuery, test.wantTypes, test.wantTitle)
			}
			ids := make([]string, len(parsed.intents))
			for index := range parsed.intents {
				ids[index] = parsed.intents[index].ID
			}
			if !slices.Equal(ids, test.wantIntent) {
				t.Fatalf("intent IDs = %v, want %v", ids, test.wantIntent)
			}
		})
	}
}

func TestParseSemanticQueryHonorsExplicitTypeAndRemovedIntent(t *testing.T) {
	parsed := parseSemanticQuery("film de guerre", "series", map[string]struct{}{"genre:war": {}})
	if !slices.Equal(parsed.mediaTypes, []string{"series"}) {
		t.Fatalf("media types = %v, want explicit series", parsed.mediaTypes)
	}
	if len(parsed.intents) != 0 {
		t.Fatalf("excluded and explicit intents leaked into response: %+v", parsed.intents)
	}
	if parsed.titleQuery != "guerre" {
		t.Fatalf("title query = %q, want excluded semantic term retained for literal search", parsed.titleQuery)
	}
}

func TestParseSemanticQueryReturnsContractSafeEmptyArrays(t *testing.T) {
	parsed := parseSemanticQuery("Dune", "", nil)
	if parsed.intents == nil || parsed.mediaTypes == nil {
		t.Fatalf("empty semantic arrays are nil: intents=%v mediaTypes=%v", parsed.intents, parsed.mediaTypes)
	}
}

func TestSemanticExcludedIntentIDsMustBeKnownAndUnique(t *testing.T) {
	for _, ids := range [][]string{{"genre:war", "GENRE:WAR"}, {"genre:unknown"}} {
		if _, err := normalizeSemanticExcludedIntentIDs(ids); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("intent IDs %v error = %v, want %v", ids, err, ErrInvalidInput)
		}
	}
	excluded, err := normalizeSemanticExcludedIntentIDs([]string{" GENRE:WAR "})
	if err != nil {
		t.Fatalf("normalize known intent ID: %v", err)
	}
	if _, ok := excluded["genre:war"]; !ok {
		t.Fatalf("normalized intent missing: %v", excluded)
	}
}

func TestSemanticGenreIDsMapWarAndCrimePerTMDBMediaType(t *testing.T) {
	movies, movieOK := semanticGenreIDs([]string{"war", "crime"}, MediaTypeMovie)
	series, seriesOK := semanticGenreIDs([]string{"war", "crime"}, MediaTypeSeries)
	if !movieOK || !slices.Equal(movies, []int64{10752, 80}) {
		t.Fatalf("movie genre IDs = %v, ok %t", movies, movieOK)
	}
	if !seriesOK || !slices.Equal(series, []int64{10768, 80}) {
		t.Fatalf("series genre IDs = %v, ok %t", series, seriesOK)
	}
}

func TestSemanticTMDBSourcesComposeGenresThemesCountriesAndAnime(t *testing.T) {
	parsed := parseSemanticQuery("anime policier japonais dans l'espace", "", nil)
	sources := semanticTMDBSources(parsed, []int64{9882})
	if len(sources) != 2 {
		t.Fatalf("sources = %+v, want movie and series", sources)
	}
	for _, source := range sources {
		if source.SourceType != "discover" || source.Sort != "popularity.desc" || source.Filters.OriginalLanguage != "ja" || source.Filters.OriginCountry != "JP" {
			t.Fatalf("source filters = %+v", source)
		}
		if !slices.Equal(source.Filters.Keywords, []int64{9882}) {
			t.Fatalf("keyword IDs = %v", source.Filters.Keywords)
		}
		wantGenre := int64(80)
		if !slices.Contains(source.Filters.Genres, wantGenre) || !slices.Contains(source.Filters.Genres, int64(16)) {
			t.Fatalf("genres for %s = %v, want crime and animation", source.MediaType, source.Filters.Genres)
		}
	}
}

func TestSemanticTMDBSourcesExcludeRemovedGenreButKeepLiteralTitleTerm(t *testing.T) {
	parsed := parseSemanticQuery("film Dune de guerre", "", map[string]struct{}{"genre:war": {}})
	sources := semanticTMDBSources(parsed, nil)
	if parsed.titleQuery != "Dune guerre" {
		t.Fatalf("title query = %q, want removed intent preserved literally", parsed.titleQuery)
	}
	if len(sources) != 0 {
		t.Fatalf("removed genre still produced discover sources: %+v", sources)
	}
}

func TestSemanticTitleFallbackPreservesTitleAndExplicitFilters(t *testing.T) {
	pureTitle := parseSemanticQuery("Alien", "", nil)
	if !semanticShouldSearchTitle(pureTitle, false) {
		t.Fatal("proper title did not enable TMDB title fallback")
	}
	pureSources := semanticTMDBTitleSources(pureTitle)
	if len(pureSources) != 2 || pureSources[0].MediaType != MediaTypeMovie || pureSources[1].MediaType != MediaTypeSeries {
		t.Fatalf("pure title sources = %+v, want movie and series", pureSources)
	}

	filtered := parseSemanticQuery("film d'horreur Alien", "", nil)
	if filtered.titleQuery != "Alien" || !semanticShouldSearchTitle(filtered, false) {
		t.Fatalf("filtered title parse = %+v", filtered)
	}
	filteredSources := semanticTMDBTitleSources(filtered)
	if len(filteredSources) != 1 || filteredSources[0].MediaType != MediaTypeMovie || !slices.Equal(filteredSources[0].Filters.Genres, []int64{27}) {
		t.Fatalf("filtered title sources = %+v", filteredSources)
	}
	if semanticShouldSearchTitle(filtered, true) {
		t.Fatal("residual wording classified by the extension was also treated as a title")
	}

	explicitSeries := parseSemanticQuery("Alien", MediaTypeSeries, nil)
	explicitSources := semanticTMDBTitleSources(explicitSeries)
	if len(explicitSources) != 1 || explicitSources[0].MediaType != MediaTypeSeries {
		t.Fatalf("explicit series title sources = %+v", explicitSources)
	}

	anime := parseSemanticQuery("anime Akira", "", nil)
	animeSources := semanticTMDBTitleSources(anime)
	if len(animeSources) != 2 {
		t.Fatalf("anime title sources = %+v", animeSources)
	}
	for _, source := range animeSources {
		if source.Filters.OriginalLanguage != "ja" || !slices.Contains(source.Filters.Genres, int64(16)) {
			t.Fatalf("anime title source lost language or animation filter: %+v", source)
		}
	}

	unsupported := parseSemanticQuery("direct Alien", MediaTypeTV, nil)
	if sources := semanticTMDBTitleSources(unsupported); len(sources) != 0 {
		t.Fatalf("unsupported live-TV title sources = %+v", sources)
	}
}

func TestSemanticTitlePagesInterleaveMediaTypesWithoutDestroyingProviderRelevance(t *testing.T) {
	items := semanticPageItems([]SourcePage{
		{Items: []Item{{ID: "movie-1"}, {ID: "movie-2"}}},
		{Items: []Item{{ID: "series-1"}, {ID: "series-2"}}},
	}, true)
	ids := make([]string, len(items))
	for index, item := range items {
		ids[index] = item.ID
	}
	if !slices.Equal(ids, []string{"movie-1", "series-1", "movie-2", "series-2"}) {
		t.Fatalf("interleaved title IDs = %v", ids)
	}
}

type semanticLookupProvider struct {
	results []LookupResult
	err     error
}

func (provider semanticLookupProvider) ResolveCollectionSource(context.Context, TMDBSource, int, string, string) (SourcePage, error) {
	return SourcePage{}, nil
}

func (semanticLookupProvider) SearchCollectionTitles(context.Context, string, TMDBSource, int, string, string) (SourcePage, error) {
	return SourcePage{}, nil
}

func (provider semanticLookupProvider) LookupCollectionSource(context.Context, string, string, string, int) ([]LookupResult, error) {
	return provider.results, provider.err
}

func (semanticLookupProvider) CollectionGenres(context.Context, string, string) ([]Genre, error) {
	return nil, nil
}

func (semanticLookupProvider) SemanticCatalogLanguages(context.Context) ([]string, error) {
	return nil, nil
}

func (semanticLookupProvider) SemanticCatalogLocale(context.Context, string) (SemanticCatalogLocale, error) {
	return SemanticCatalogLocale{}, nil
}

func TestSemanticKeywordIDsAcceptOnlyExactThemeNames(t *testing.T) {
	service := &Service{}
	ids, partial, err := service.semanticKeywordIDs(context.Background(), semanticLookupProvider{results: []LookupResult{
		{ID: 42, Name: "Space"},
		{ID: 43, Name: "Space Opera"},
	}}, []string{"space"}, "fr-FR")
	if err != nil || partial || !slices.Equal(ids, []int64{42}) {
		t.Fatalf("keyword resolution = ids %v partial %t error %v", ids, partial, err)
	}
}

func TestSemanticKeywordIDsPropagateCancellation(t *testing.T) {
	service := &Service{}
	_, _, err := service.semanticKeywordIDs(context.Background(), semanticLookupProvider{err: context.Canceled}, []string{"space"}, "fr-FR")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("keyword cancellation error = %v", err)
	}
}
