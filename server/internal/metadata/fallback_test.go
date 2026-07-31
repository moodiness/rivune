package metadata

import (
	"context"
	"errors"
	"testing"
)

type fallbackProviderStub struct {
	Provider
	movieDetails  func(string, string) (ProviderMovie, error)
	seriesDetails func(string, string) (ProviderSeries, error)
	seasonDetails func(string, int, string) (ProviderSeason, error)
	searchMovies  func(SearchOptions) (ProviderMoviePage, error)
}

func (provider fallbackProviderStub) MovieDetails(_ context.Context, externalID, language string) (ProviderMovie, error) {
	return provider.movieDetails(externalID, language)
}

func (provider fallbackProviderStub) SeriesDetails(_ context.Context, externalID, language string) (ProviderSeries, error) {
	return provider.seriesDetails(externalID, language)
}

func (provider fallbackProviderStub) SeasonDetails(_ context.Context, externalID string, seasonNumber int, language string) (ProviderSeason, error) {
	return provider.seasonDetails(externalID, seasonNumber, language)
}

func (provider fallbackProviderStub) SearchMovies(_ context.Context, options SearchOptions) (ProviderMoviePage, error) {
	return provider.searchMovies(options)
}

func TestEnglishOverviewFallbackFillsBlankMovieWithoutReplacingLocalizedFields(t *testing.T) {
	calls := make([]string, 0, 2)
	service := NewService(nil, fallbackProviderStub{movieDetails: func(_ string, language string) (ProviderMovie, error) {
		calls = append(calls, language)
		if language == englishOverviewLanguage {
			return ProviderMovie{ExternalID: "42", Title: "English title", Overview: "English overview", Tagline: "English tagline"}, nil
		}
		return ProviderMovie{ExternalID: "42", Title: "Titre français", Overview: "  ", Tagline: "Accroche française"}, nil
	}}, nil, 0, nil)
	provider := service.provider

	movie, err := provider.MovieDetails(context.Background(), "42", "fr-FR")
	if err != nil {
		t.Fatalf("movie details: %v", err)
	}
	if movie.Overview != "English overview" || movie.Title != "Titre français" || movie.Tagline != "Accroche française" {
		t.Fatalf("unexpected merged movie: %+v", movie)
	}
	if len(calls) != 2 || calls[0] != "fr-FR" || calls[1] != englishOverviewLanguage {
		t.Fatalf("languages = %v, want [fr-FR %s]", calls, englishOverviewLanguage)
	}
}

func TestEnglishOverviewFallbackPreservesFrenchMovieOverviewWithoutSecondRequest(t *testing.T) {
	calls := 0
	provider := withEnglishOverviewFallback(fallbackProviderStub{movieDetails: func(_ string, _ string) (ProviderMovie, error) {
		calls++
		return ProviderMovie{ExternalID: "42", Title: "Titre français", Overview: "Résumé français"}, nil
	}})

	movie, err := provider.MovieDetails(context.Background(), "42", "fr-FR")
	if err != nil {
		t.Fatalf("movie details: %v", err)
	}
	if movie.Overview != "Résumé français" || calls != 1 {
		t.Fatalf("movie = %+v, calls = %d", movie, calls)
	}
}

func TestEnglishOverviewFallbackMergesSeriesAndSeasonSummariesByIdentity(t *testing.T) {
	provider := withEnglishOverviewFallback(fallbackProviderStub{seriesDetails: func(_ string, language string) (ProviderSeries, error) {
		if language == englishOverviewLanguage {
			return ProviderSeries{
				ExternalID: "100", Name: "English name", Overview: "English series overview",
				Seasons: []ProviderSeasonSummary{
					{ExternalID: "202", SeasonNumber: 2, Overview: "English season two"},
					{ExternalID: "201", SeasonNumber: 1, Overview: "English season one"},
				},
			}, nil
		}
		return ProviderSeries{
			ExternalID: "100", Name: "Nom français", Overview: "Résumé français",
			Seasons: []ProviderSeasonSummary{
				{ExternalID: "201", Name: "Saison 1", SeasonNumber: 1, Overview: "\t"},
				{ExternalID: "202", Name: "Saison 2", SeasonNumber: 2, Overview: "Résumé français de la saison"},
			},
		}, nil
	}})

	series, err := provider.SeriesDetails(context.Background(), "100", "fr-FR")
	if err != nil {
		t.Fatalf("series details: %v", err)
	}
	if series.Name != "Nom français" || series.Overview != "Résumé français" {
		t.Fatalf("localized series fields were replaced: %+v", series)
	}
	if series.Seasons[0].Name != "Saison 1" || series.Seasons[0].Overview != "English season one" {
		t.Fatalf("blank season overview was not filled by identity: %+v", series.Seasons[0])
	}
	if series.Seasons[1].Overview != "Résumé français de la saison" {
		t.Fatalf("French season overview was replaced: %+v", series.Seasons[1])
	}
}

func TestEnglishOverviewFallbackMergesEpisodesBySeasonAndEpisodeNumber(t *testing.T) {
	provider := withEnglishOverviewFallback(fallbackProviderStub{seasonDetails: func(_ string, _ int, language string) (ProviderSeason, error) {
		if language == englishOverviewLanguage {
			return ProviderSeason{
				ExternalID: "300", SeasonNumber: 1, Overview: "English season overview",
				Episodes: []ProviderEpisode{
					{ExternalID: "402", SeasonNumber: 1, EpisodeNumber: 2, Overview: "English episode two"},
					{ExternalID: "different-id", SeasonNumber: 1, EpisodeNumber: 1, Overview: "English episode one"},
				},
			}, nil
		}
		return ProviderSeason{
			ExternalID: "300", Name: "Saison 1", SeasonNumber: 1, Overview: " ",
			Episodes: []ProviderEpisode{
				{ExternalID: "401", Name: "Épisode un", SeasonNumber: 1, EpisodeNumber: 1, Overview: ""},
				{ExternalID: "402", Name: "Épisode deux", SeasonNumber: 1, EpisodeNumber: 2, Overview: "Résumé français"},
			},
		}, nil
	}})

	season, err := provider.SeasonDetails(context.Background(), "100", 1, "fr-FR")
	if err != nil {
		t.Fatalf("season details: %v", err)
	}
	if season.Name != "Saison 1" || season.Overview != "English season overview" {
		t.Fatalf("unexpected merged season: %+v", season)
	}
	if season.Episodes[0].Name != "Épisode un" || season.Episodes[0].Overview != "English episode one" {
		t.Fatalf("episode fallback did not use its numbers: %+v", season.Episodes[0])
	}
	if season.Episodes[1].Overview != "Résumé français" {
		t.Fatalf("French episode overview was replaced: %+v", season.Episodes[1])
	}
}

func TestEnglishOverviewFallbackMergesSearchResultsByStableID(t *testing.T) {
	provider := withEnglishOverviewFallback(fallbackProviderStub{searchMovies: func(options SearchOptions) (ProviderMoviePage, error) {
		if options.Language == englishOverviewLanguage {
			return ProviderMoviePage{Items: []ProviderMovie{
				{ExternalID: "2", Overview: "English two"},
				{ExternalID: "1", Overview: "English one"},
			}}, nil
		}
		return ProviderMoviePage{Items: []ProviderMovie{
			{ExternalID: "1", Title: "Film un", Overview: " "},
			{ExternalID: "2", Title: "Film deux", Overview: "Résumé français"},
		}}, nil
	}})

	page, err := provider.SearchMovies(context.Background(), SearchOptions{
		QueryOptions: QueryOptions{Language: "fr-FR", Page: 1}, Query: "film",
	})
	if err != nil {
		t.Fatalf("search movies: %v", err)
	}
	if page.Items[0].Title != "Film un" || page.Items[0].Overview != "English one" || page.Items[1].Overview != "Résumé français" {
		t.Fatalf("unexpected merged search page: %+v", page)
	}
}

func TestEnglishOverviewFallbackFailureIsNonfatalAndDoesNotLoop(t *testing.T) {
	calls := 0
	provider := withEnglishOverviewFallback(fallbackProviderStub{movieDetails: func(_ string, language string) (ProviderMovie, error) {
		calls++
		if language == englishOverviewLanguage {
			return ProviderMovie{}, errors.New("English request failed")
		}
		return ProviderMovie{ExternalID: "42", Title: "Titre français", Overview: ""}, nil
	}})

	movie, err := provider.MovieDetails(context.Background(), "42", "fr-FR")
	if err != nil {
		t.Fatalf("localized result should remain usable: %v", err)
	}
	if movie.Title != "Titre français" || movie.Overview != "" || calls != 2 {
		t.Fatalf("movie = %+v, calls = %d", movie, calls)
	}
}
