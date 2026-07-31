package metadata

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/rivune/server/internal/auth"
)

const trailerFallbackLanguage = "en-US"

func (s *Service) Trailer(ctx context.Context, principal auth.Principal, titleID, language string, seasonNumber *int) (Trailer, error) {
	if err := requireActiveProfile(principal); err != nil {
		return Trailer{}, err
	}
	normalizedLanguage, err := normalizeLanguage(language)
	if err != nil {
		return Trailer{}, err
	}
	if seasonNumber != nil && *seasonNumber < 0 {
		return Trailer{}, fmt.Errorf("%w: seasonNumber must be at least 0", ErrInvalidInput)
	}

	var mediaType string
	var externalID *string
	err = s.pool.QueryRow(ctx, `
		SELECT title.media_type, external.external_id
		FROM titles AS title
		LEFT JOIN title_external_ids AS external
		  ON external.title_id = title.id
		 AND external.provider = $2
		 AND external.namespace = title.media_type
		WHERE title.id::text = $1
		  AND title.media_type IN ('movie', 'series')
	`, strings.TrimSpace(titleID), providerName).Scan(&mediaType, &externalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Trailer{}, ErrNotFound
	}
	if err != nil {
		return Trailer{}, fmt.Errorf("query trailer title identity: %w", err)
	}
	if seasonNumber != nil && mediaType != MediaTypeSeries {
		return Trailer{}, fmt.Errorf("%w: seasonNumber is only valid for series", ErrInvalidInput)
	}
	if s.trailerProvider == nil {
		return Trailer{}, ErrProviderUnavailable
	}

	resolvedExternalID := ""
	if externalID != nil {
		resolvedExternalID = strings.TrimSpace(*externalID)
	}
	if resolvedExternalID == "" {
		resolvedExternalID, err = s.resolveProviderExternalID(ctx, titleID, mediaType)
		if err != nil {
			return Trailer{}, err
		}
	}
	return chooseTrailer(ctx, s.trailerProvider, mediaType, resolvedExternalID, normalizedLanguage, seasonNumber)
}

func chooseTrailer(ctx context.Context, provider TrailerProvider, mediaType, externalID, language string, seasonNumber *int) (Trailer, error) {
	localized, err := provider.Trailers(ctx, mediaType, externalID, language, seasonNumber)
	if err != nil && !errors.Is(err, ErrProviderNotFound) {
		return Trailer{}, err
	}
	if selected, ok := selectProviderTrailer(localized); ok {
		return normalizeTrailer(selected, language, false, ""), nil
	}
	if language == trailerFallbackLanguage {
		return Trailer{}, ErrNotFound
	}

	english, err := provider.Trailers(ctx, mediaType, externalID, trailerFallbackLanguage, seasonNumber)
	if err != nil && !errors.Is(err, ErrProviderNotFound) {
		return Trailer{}, err
	}
	selected, ok := selectProviderTrailer(english)
	if !ok {
		return Trailer{}, ErrNotFound
	}
	captionPreference := ""
	baseLanguage, _, _ := strings.Cut(language, "-")
	if strings.EqualFold(baseLanguage, "fr") {
		captionPreference = "fr"
	}
	return normalizeTrailer(selected, trailerFallbackLanguage, true, captionPreference), nil
}

func selectProviderTrailer(trailers []ProviderTrailer) (ProviderTrailer, bool) {
	var selected ProviderTrailer
	found := false
	for _, candidate := range trailers {
		candidate.YouTubeID = strings.TrimSpace(candidate.YouTubeID)
		candidate.Name = strings.TrimSpace(candidate.Name)
		candidate.Type = strings.TrimSpace(candidate.Type)
		candidate.Site = strings.TrimSpace(candidate.Site)
		if candidate.YouTubeID == "" || !strings.EqualFold(candidate.Site, "YouTube") ||
			(!strings.EqualFold(candidate.Type, "Trailer") && !strings.EqualFold(candidate.Type, "Teaser")) {
			continue
		}
		if !found || trailerRanksBefore(candidate, selected) {
			selected = candidate
			found = true
		}
	}
	return selected, found
}

func trailerRanksBefore(left, right ProviderTrailer) bool {
	leftIsTrailer := strings.EqualFold(left.Type, "Trailer")
	rightIsTrailer := strings.EqualFold(right.Type, "Trailer")
	if leftIsTrailer != rightIsTrailer {
		return leftIsTrailer
	}
	if left.Official != right.Official {
		return left.Official
	}
	if !left.PublishedAt.Equal(right.PublishedAt) {
		return left.PublishedAt.After(right.PublishedAt)
	}
	if left.YouTubeID != right.YouTubeID {
		return left.YouTubeID < right.YouTubeID
	}
	return left.Name < right.Name
}

func normalizeTrailer(provided ProviderTrailer, language string, fallback bool, captionPreference string) Trailer {
	return Trailer{
		YouTubeID: strings.TrimSpace(provided.YouTubeID), Name: strings.TrimSpace(provided.Name),
		Language: language, IsFallback: fallback, CaptionPreference: captionPreference,
	}
}
