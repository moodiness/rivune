package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/settings"
)

func (a *API) discoverMovies(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	options, err := metadataQueryOptions(r)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_metadata_query", err.Error())
		return
	}
	page, err := a.metadata.DiscoverMovies(r.Context(), principal, options)
	if err != nil {
		a.writeMetadataError(w, "discover movies", err)
		return
	}
	if a.artwork != nil {
		a.artwork.LocalizeMoviePage(r.Context(), &page)
	}
	writeJSON(w, http.StatusOK, page)
}

func (a *API) searchMovies(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	options, err := metadataQueryOptions(r)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_metadata_query", err.Error())
		return
	}
	page, err := a.metadata.SearchMovies(r.Context(), principal, metadata.SearchOptions{
		QueryOptions: options,
		Query:        r.URL.Query().Get("query"),
	})
	if err != nil {
		a.writeMetadataError(w, "search movies", err)
		return
	}
	if a.artwork != nil {
		a.artwork.LocalizeMoviePage(r.Context(), &page)
	}
	writeJSON(w, http.StatusOK, page)
}

func (a *API) movieDetails(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	maximumCastMembers, err := a.maximumCastMembers(r, principal)
	if err != nil {
		a.internalError(w, "resolve movie cast settings", err)
		return
	}
	movie, err := a.metadata.MovieDetails(
		r.Context(),
		principal,
		r.PathValue("titleId"),
		r.URL.Query().Get("language"),
	)
	if err != nil {
		a.writeMetadataError(w, "read movie details", err)
		return
	}
	if len(movie.Cast) > maximumCastMembers {
		movie.Cast = movie.Cast[:maximumCastMembers]
	}
	if a.artwork != nil {
		a.artwork.LocalizeMovie(r.Context(), &movie)
	}
	writeJSON(w, http.StatusOK, movie)
}

func (a *API) discoverSeries(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	options, err := metadataQueryOptions(r)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_metadata_query", err.Error())
		return
	}
	page, err := a.metadata.DiscoverSeries(r.Context(), principal, options)
	if err != nil {
		a.writeMetadataError(w, "discover series", err)
		return
	}
	if a.artwork != nil {
		a.artwork.LocalizeSeriesPage(r.Context(), &page)
	}
	writeJSON(w, http.StatusOK, page)
}

func (a *API) searchSeries(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	options, err := metadataQueryOptions(r)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_metadata_query", err.Error())
		return
	}
	page, err := a.metadata.SearchSeries(r.Context(), principal, metadata.SearchOptions{
		QueryOptions: options,
		Query:        r.URL.Query().Get("query"),
	})
	if err != nil {
		a.writeMetadataError(w, "search series", err)
		return
	}
	if a.artwork != nil {
		a.artwork.LocalizeSeriesPage(r.Context(), &page)
	}
	writeJSON(w, http.StatusOK, page)
}

func (a *API) seriesDetails(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	maximumCastMembers, err := a.maximumCastMembers(r, principal)
	if err != nil {
		a.internalError(w, "resolve series cast settings", err)
		return
	}
	series, err := a.metadata.SeriesDetails(
		r.Context(),
		principal,
		r.PathValue("titleId"),
		metadata.SeriesDetailsOptions{
			Language:        r.URL.Query().Get("language"),
			MappingProvider: r.URL.Query().Get("mappingProvider"),
			EpisodeOrderID:  r.URL.Query().Get("episodeOrder"),
		},
	)
	if err != nil {
		a.writeMetadataError(w, "read series details", err)
		return
	}
	if len(series.Cast) > maximumCastMembers {
		series.Cast = series.Cast[:maximumCastMembers]
	}
	if a.artwork != nil {
		a.artwork.LocalizeSeries(r.Context(), &series)
	}
	writeJSON(w, http.StatusOK, series)
}

func (a *API) maximumCastMembers(r *http.Request, principal auth.Principal) (int, error) {
	if principal.ActiveProfileID != nil && principal.ProfileGrantExpiresAt != nil {
		effective, err := a.settings.Effective(r.Context(), principal, *principal.ActiveProfileID)
		if err != nil {
			return 0, err
		}
		return effective.Values.MaximumCastMembers, nil
	}
	instance, err := a.settings.Instance(r.Context())
	if err != nil {
		return 0, err
	}
	if instance.Values.MaximumCastMembers != nil {
		return *instance.Values.MaximumCastMembers, nil
	}
	return settings.DefaultMaximumCastMembers, nil
}

func (a *API) seasonDetails(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	season, err := a.metadata.SeasonDetails(
		r.Context(),
		principal,
		r.PathValue("seasonId"),
		r.URL.Query().Get("language"),
		r.URL.Query().Get("mappingProvider"),
	)
	if err != nil {
		a.writeMetadataError(w, "read season details", err)
		return
	}
	if a.artwork != nil {
		a.artwork.LocalizeSeason(r.Context(), &season)
	}
	writeJSON(w, http.StatusOK, season)
}

func (a *API) titleTrailers(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	var seasonNumber *int
	if r.URL.Query().Has("seasonNumber") {
		parsed, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("seasonNumber")))
		if err != nil || parsed < 0 {
			a.writeMetadataError(w, "read title trailers", fmt.Errorf("%w: seasonNumber must be an integer greater than or equal to 0", metadata.ErrInvalidInput))
			return
		}
		seasonNumber = &parsed
	}
	trailers, err := a.metadata.Trailers(
		r.Context(),
		principal,
		r.PathValue("titleId"),
		r.URL.Query().Get("language"),
		r.URL.Query().Get("captionLanguage"),
		seasonNumber,
	)
	if err != nil {
		a.writeMetadataError(w, "read title trailers", err)
		return
	}
	writeJSON(w, http.StatusOK, trailers)
}

func metadataQueryOptions(r *http.Request) (metadata.QueryOptions, error) {
	page := 0
	if rawPage := strings.TrimSpace(r.URL.Query().Get("page")); rawPage != "" {
		parsed, err := strconv.Atoi(rawPage)
		if err != nil {
			return metadata.QueryOptions{}, errors.New("page must be an integer")
		}
		page = parsed
	}
	return metadata.QueryOptions{
		Page:     page,
		Language: r.URL.Query().Get("language"),
		Region:   r.URL.Query().Get("region"),
	}, nil
}

func (a *API) writeMetadataError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, metadata.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_metadata_query", strings.TrimPrefix(err.Error(), metadata.ErrInvalidInput.Error()+": "))
	case errors.Is(err, metadata.ErrProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select an active profile before browsing metadata")
	case errors.Is(err, metadata.ErrNotFound), errors.Is(err, metadata.ErrProviderNotFound):
		writeError(w, http.StatusNotFound, "title_not_found", "The title does not exist")
	case errors.Is(err, metadata.ErrProviderUnavailable), errors.Is(err, metadata.ErrProviderUnauthorized):
		a.logMetadataProviderError(operation, err)
		writeError(w, http.StatusServiceUnavailable, "metadata_provider_unavailable", "The server administrator must configure valid TMDB credentials")
	case errors.Is(err, metadata.ErrProviderRateLimited):
		a.logMetadataProviderError(operation, err)
		w.Header().Set("Retry-After", "30")
		writeError(w, http.StatusServiceUnavailable, "metadata_provider_rate_limited", "The metadata provider rate limit was reached")
	case errors.Is(err, metadata.ErrProviderFailure):
		a.logMetadataProviderError(operation, err)
		writeError(w, http.StatusBadGateway, "metadata_provider_error", "The metadata provider request failed")
	default:
		a.internalError(w, operation, err)
	}
}

func (a *API) logMetadataProviderError(operation string, err error) {
	a.logger.Error("metadata provider request failed", "operation", operation, "error", err)
}
