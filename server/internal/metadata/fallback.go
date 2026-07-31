package metadata

import (
	"context"
	"strings"
)

const englishOverviewLanguage = "en-US"

type englishOverviewFallbackProvider struct {
	Provider
}

type englishOverviewFallbackProviderWithResolver struct {
	englishOverviewFallbackProvider
	ExternalIDResolver
}

func withEnglishOverviewFallback(provider Provider) Provider {
	if provider == nil {
		return nil
	}
	switch provider.(type) {
	case englishOverviewFallbackProvider, englishOverviewFallbackProviderWithResolver:
		return provider
	}
	fallback := englishOverviewFallbackProvider{Provider: provider}
	if resolver, ok := provider.(ExternalIDResolver); ok {
		return englishOverviewFallbackProviderWithResolver{
			englishOverviewFallbackProvider: fallback,
			ExternalIDResolver:              resolver,
		}
	}
	return fallback
}

func (provider englishOverviewFallbackProvider) DiscoverMovies(ctx context.Context, options QueryOptions) (ProviderMoviePage, error) {
	localized, err := provider.Provider.DiscoverMovies(ctx, options)
	if err != nil || !shouldFallbackOverview(options.Language) || !moviePageNeedsOverview(localized) {
		return localized, err
	}
	options.Language = englishOverviewLanguage
	english, englishErr := provider.Provider.DiscoverMovies(ctx, options)
	if englishErr == nil {
		mergeMoviePageOverviews(&localized, english)
	}
	return localized, nil
}

func (provider englishOverviewFallbackProvider) SearchMovies(ctx context.Context, options SearchOptions) (ProviderMoviePage, error) {
	localized, err := provider.Provider.SearchMovies(ctx, options)
	if err != nil || !shouldFallbackOverview(options.Language) || !moviePageNeedsOverview(localized) {
		return localized, err
	}
	options.Language = englishOverviewLanguage
	english, englishErr := provider.Provider.SearchMovies(ctx, options)
	if englishErr == nil {
		mergeMoviePageOverviews(&localized, english)
	}
	return localized, nil
}

func (provider englishOverviewFallbackProvider) MovieDetails(ctx context.Context, externalID, language string) (ProviderMovie, error) {
	localized, err := provider.Provider.MovieDetails(ctx, externalID, language)
	if err != nil || !shouldFallbackOverview(language) || !blankOverview(localized.Overview) {
		return localized, err
	}
	english, englishErr := provider.Provider.MovieDetails(ctx, externalID, englishOverviewLanguage)
	if englishErr == nil && sameStableID(localized.ExternalID, english.ExternalID) && !blankOverview(english.Overview) {
		localized.Overview = english.Overview
	}
	return localized, nil
}

func (provider englishOverviewFallbackProvider) DiscoverSeries(ctx context.Context, options QueryOptions) (ProviderSeriesPage, error) {
	localized, err := provider.Provider.DiscoverSeries(ctx, options)
	if err != nil || !shouldFallbackOverview(options.Language) || !seriesPageNeedsOverview(localized) {
		return localized, err
	}
	options.Language = englishOverviewLanguage
	english, englishErr := provider.Provider.DiscoverSeries(ctx, options)
	if englishErr == nil {
		mergeSeriesPageOverviews(&localized, english)
	}
	return localized, nil
}

func (provider englishOverviewFallbackProvider) SearchSeries(ctx context.Context, options SearchOptions) (ProviderSeriesPage, error) {
	localized, err := provider.Provider.SearchSeries(ctx, options)
	if err != nil || !shouldFallbackOverview(options.Language) || !seriesPageNeedsOverview(localized) {
		return localized, err
	}
	options.Language = englishOverviewLanguage
	english, englishErr := provider.Provider.SearchSeries(ctx, options)
	if englishErr == nil {
		mergeSeriesPageOverviews(&localized, english)
	}
	return localized, nil
}

func (provider englishOverviewFallbackProvider) SeriesDetails(ctx context.Context, externalID, language string) (ProviderSeries, error) {
	localized, err := provider.Provider.SeriesDetails(ctx, externalID, language)
	if err != nil || !shouldFallbackOverview(language) || !seriesNeedsOverview(localized) {
		return localized, err
	}
	english, englishErr := provider.Provider.SeriesDetails(ctx, externalID, englishOverviewLanguage)
	if englishErr == nil {
		mergeSeriesOverviews(&localized, english)
	}
	return localized, nil
}

func (provider englishOverviewFallbackProvider) SeasonDetails(ctx context.Context, seriesExternalID string, seasonNumber int, language string) (ProviderSeason, error) {
	localized, err := provider.Provider.SeasonDetails(ctx, seriesExternalID, seasonNumber, language)
	if err != nil || !shouldFallbackOverview(language) || !seasonNeedsOverview(localized) {
		return localized, err
	}
	english, englishErr := provider.Provider.SeasonDetails(ctx, seriesExternalID, seasonNumber, englishOverviewLanguage)
	if englishErr == nil {
		mergeSeasonOverviews(&localized, english)
	}
	return localized, nil
}

func shouldFallbackOverview(language string) bool {
	base, _, _ := strings.Cut(strings.TrimSpace(language), "-")
	return !strings.EqualFold(base, "en")
}

func blankOverview(value string) bool {
	return strings.TrimSpace(value) == ""
}

func sameStableID(localized, english string) bool {
	localized = strings.TrimSpace(localized)
	english = strings.TrimSpace(english)
	return localized != "" && localized == english
}

func moviePageNeedsOverview(page ProviderMoviePage) bool {
	for _, movie := range page.Items {
		if blankOverview(movie.Overview) {
			return true
		}
	}
	return false
}

func mergeMoviePageOverviews(localized *ProviderMoviePage, english ProviderMoviePage) {
	byID := make(map[string]string, len(english.Items))
	for _, movie := range english.Items {
		if id := strings.TrimSpace(movie.ExternalID); id != "" && !blankOverview(movie.Overview) {
			byID[id] = movie.Overview
		}
	}
	for index := range localized.Items {
		movie := &localized.Items[index]
		if blankOverview(movie.Overview) {
			movie.Overview = byID[strings.TrimSpace(movie.ExternalID)]
		}
	}
}

func seriesPageNeedsOverview(page ProviderSeriesPage) bool {
	for _, series := range page.Items {
		if blankOverview(series.Overview) {
			return true
		}
	}
	return false
}

func mergeSeriesPageOverviews(localized *ProviderSeriesPage, english ProviderSeriesPage) {
	byID := make(map[string]string, len(english.Items))
	for _, series := range english.Items {
		if id := strings.TrimSpace(series.ExternalID); id != "" && !blankOverview(series.Overview) {
			byID[id] = series.Overview
		}
	}
	for index := range localized.Items {
		series := &localized.Items[index]
		if blankOverview(series.Overview) {
			series.Overview = byID[strings.TrimSpace(series.ExternalID)]
		}
	}
}

func seriesNeedsOverview(series ProviderSeries) bool {
	if blankOverview(series.Overview) {
		return true
	}
	for _, season := range series.Seasons {
		if blankOverview(season.Overview) {
			return true
		}
	}
	return false
}

func mergeSeriesOverviews(localized *ProviderSeries, english ProviderSeries) {
	if !sameStableID(localized.ExternalID, english.ExternalID) {
		return
	}
	if blankOverview(localized.Overview) && !blankOverview(english.Overview) {
		localized.Overview = english.Overview
	}
	byID := make(map[string]string, len(english.Seasons))
	byNumber := make(map[int]string, len(english.Seasons))
	for _, season := range english.Seasons {
		if blankOverview(season.Overview) {
			continue
		}
		if id := strings.TrimSpace(season.ExternalID); id != "" {
			byID[id] = season.Overview
		}
		byNumber[season.SeasonNumber] = season.Overview
	}
	for index := range localized.Seasons {
		season := &localized.Seasons[index]
		if !blankOverview(season.Overview) {
			continue
		}
		overview := byID[strings.TrimSpace(season.ExternalID)]
		if blankOverview(overview) {
			overview = byNumber[season.SeasonNumber]
		}
		if !blankOverview(overview) {
			season.Overview = overview
		}
	}
}

func seasonNeedsOverview(season ProviderSeason) bool {
	if blankOverview(season.Overview) {
		return true
	}
	for _, episode := range season.Episodes {
		if blankOverview(episode.Overview) {
			return true
		}
	}
	return false
}

func mergeSeasonOverviews(localized *ProviderSeason, english ProviderSeason) {
	if localized.SeasonNumber != english.SeasonNumber || !sameStableID(localized.ExternalID, english.ExternalID) {
		return
	}
	if blankOverview(localized.Overview) && !blankOverview(english.Overview) {
		localized.Overview = english.Overview
	}
	type episodeNumber struct {
		season  int
		episode int
	}
	byID := make(map[string]string, len(english.Episodes))
	byNumber := make(map[episodeNumber]string, len(english.Episodes))
	for _, episode := range english.Episodes {
		if blankOverview(episode.Overview) {
			continue
		}
		if id := strings.TrimSpace(episode.ExternalID); id != "" {
			byID[id] = episode.Overview
		}
		byNumber[episodeNumber{season: episode.SeasonNumber, episode: episode.EpisodeNumber}] = episode.Overview
	}
	for index := range localized.Episodes {
		episode := &localized.Episodes[index]
		if !blankOverview(episode.Overview) {
			continue
		}
		overview := byID[strings.TrimSpace(episode.ExternalID)]
		if blankOverview(overview) {
			overview = byNumber[episodeNumber{season: episode.SeasonNumber, episode: episode.EpisodeNumber}]
		}
		if !blankOverview(overview) {
			episode.Overview = overview
		}
	}
}
