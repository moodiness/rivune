package metadata

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/rivune/server/internal/auth"
)

const (
	trailerFallbackLanguage      = "en-US"
	maxTrailerOptions            = 5
	maxPreferredLanguageTrailers = 4
)

var trailerSeasonPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bseason[\s._-]*(\d+)\b`),
	regexp.MustCompile(`(?i)\bsaison[\s._-]*(\d+)\b`),
	regexp.MustCompile(`(?i)\b(\d+)(st|nd|rd|th)[\s._-]*season\b`),
	regexp.MustCompile(`(?i)\bs[\s._-]?(\d+)\b`),
	regexp.MustCompile(`第?\s*(\d+)\s*期`),
}

func (s *Service) Trailers(ctx context.Context, principal auth.Principal, titleID, language, captionLanguage string, seasonNumber *int) (TrailerList, error) {
	if err := requireActiveProfile(principal); err != nil {
		return TrailerList{}, err
	}
	normalizedLanguage, err := normalizeLanguage(language)
	if err != nil {
		return TrailerList{}, err
	}
	normalizedCaptionLanguage := ""
	if strings.TrimSpace(captionLanguage) != "" {
		normalizedCaptionLanguage, err = normalizeLanguage(captionLanguage)
		if err != nil {
			return TrailerList{}, fmt.Errorf("%w: invalid captionLanguage", err)
		}
	}
	if seasonNumber != nil && *seasonNumber < 0 {
		return TrailerList{}, fmt.Errorf("%w: seasonNumber must be at least 0", ErrInvalidInput)
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
		return TrailerList{}, ErrNotFound
	}
	if err != nil {
		return TrailerList{}, fmt.Errorf("query trailer title identity: %w", err)
	}
	if seasonNumber != nil && mediaType != MediaTypeSeries {
		return TrailerList{}, fmt.Errorf("%w: seasonNumber is only valid for series", ErrInvalidInput)
	}
	if s.trailerProvider == nil {
		return TrailerList{}, ErrProviderUnavailable
	}

	resolvedExternalID := ""
	if externalID != nil {
		resolvedExternalID = strings.TrimSpace(*externalID)
	}
	if resolvedExternalID == "" {
		resolvedExternalID, err = s.resolveProviderExternalID(ctx, titleID, mediaType)
		if err != nil {
			return TrailerList{}, err
		}
	}
	return chooseTrailers(ctx, s.trailerProvider, mediaType, resolvedExternalID, normalizedLanguage, normalizedCaptionLanguage, seasonNumber)
}

func chooseTrailers(ctx context.Context, provider TrailerProvider, mediaType, externalID, language, captionLanguage string, seasonNumber *int) (TrailerList, error) {
	localized, err := providerTrailersForLanguage(ctx, provider, mediaType, externalID, language, seasonNumber)
	if err != nil {
		return TrailerList{}, err
	}

	var english []ProviderTrailer
	if language != trailerFallbackLanguage {
		english, err = providerTrailersForLanguage(ctx, provider, mediaType, externalID, trailerFallbackLanguage, seasonNumber)
		if err != nil && len(localized) == 0 {
			return TrailerList{}, err
		}
		if err != nil {
			english = nil
		}
	}

	trailers := curateTrailers(localized, english, language, captionLanguage)
	if len(trailers) == 0 {
		return TrailerList{}, ErrNotFound
	}
	return TrailerList{Trailers: trailers}, nil
}

func providerTrailersForLanguage(ctx context.Context, provider TrailerProvider, mediaType, externalID, language string, seasonNumber *int) ([]ProviderTrailer, error) {
	provided, err := provider.Trailers(ctx, mediaType, externalID, language, seasonNumber)
	if err != nil && !errors.Is(err, ErrProviderNotFound) {
		return nil, err
	}
	if seasonNumber == nil {
		return selectProviderTrailers(provided), nil
	}

	requestedSeason := *seasonNumber
	candidates := selectProviderTrailersForSeason(provided, requestedSeason, true)
	if len(candidates) >= maxTrailerOptions {
		return candidates, nil
	}

	titleTrailers, titleErr := provider.Trailers(ctx, mediaType, externalID, language, nil)
	if titleErr != nil && !errors.Is(titleErr, ErrProviderNotFound) {
		if len(candidates) == 0 {
			return nil, titleErr
		}
		return candidates, nil
	}
	candidates = append(candidates, selectProviderTrailersForSeason(titleTrailers, requestedSeason, requestedSeason == 1)...)

	if requestedSeason > 1 && len(candidates) < maxTrailerOptions {
		firstSeason := 1
		firstSeasonTrailers, firstSeasonErr := provider.Trailers(ctx, mediaType, externalID, language, &firstSeason)
		if firstSeasonErr != nil && !errors.Is(firstSeasonErr, ErrProviderNotFound) {
			if len(candidates) == 0 {
				return nil, firstSeasonErr
			}
			return selectProviderTrailers(candidates), nil
		}
		candidates = append(candidates, selectProviderTrailersForSeason(firstSeasonTrailers, requestedSeason, false)...)
	}
	return selectProviderTrailers(candidates), nil
}

func selectProviderTrailersForSeason(trailers []ProviderTrailer, requestedSeason int, allowUnmarked bool) []ProviderTrailer {
	return selectProviderTrailersWhere(trailers, func(candidate ProviderTrailer) bool {
		referencedSeason, explicit := trailerSeasonReference(candidate.Name)
		if explicit {
			return referencedSeason == requestedSeason
		}
		return allowUnmarked
	})
}

func trailerSeasonReference(name string) (int, bool) {
	for _, pattern := range trailerSeasonPatterns {
		matches := pattern.FindStringSubmatch(name)
		if len(matches) < 2 {
			continue
		}
		seasonNumber, err := strconv.Atoi(matches[1])
		if err == nil {
			return seasonNumber, true
		}
	}
	return 0, false
}

func selectProviderTrailers(trailers []ProviderTrailer) []ProviderTrailer {
	return selectProviderTrailersWhere(trailers, nil)
}

func selectProviderTrailersWhere(trailers []ProviderTrailer, eligible func(ProviderTrailer) bool) []ProviderTrailer {
	selected := make([]ProviderTrailer, 0, len(trailers))
	for _, candidate := range trailers {
		candidate.YouTubeID = strings.TrimSpace(candidate.YouTubeID)
		candidate.Name = strings.TrimSpace(candidate.Name)
		candidate.Type = strings.TrimSpace(candidate.Type)
		candidate.Site = strings.TrimSpace(candidate.Site)
		if candidate.YouTubeID == "" || !strings.EqualFold(candidate.Site, "YouTube") ||
			(!strings.EqualFold(candidate.Type, "Trailer") && !strings.EqualFold(candidate.Type, "Teaser")) ||
			eligible != nil && !eligible(candidate) {
			continue
		}
		selected = append(selected, candidate)
	}
	sort.SliceStable(selected, func(left, right int) bool {
		return trailerRanksBefore(selected[left], selected[right])
	})
	seen := make(map[string]struct{}, len(selected))
	unique := selected[:0]
	for _, candidate := range selected {
		if _, duplicate := seen[candidate.YouTubeID]; duplicate {
			continue
		}
		seen[candidate.YouTubeID] = struct{}{}
		unique = append(unique, candidate)
	}
	return unique
}

func curateTrailers(localized, english []ProviderTrailer, language, captionLanguage string) []Trailer {
	trailers := make([]Trailer, 0, maxTrailerOptions)
	seen := make(map[string]struct{}, maxTrailerOptions)
	captionPreference, _, _ := strings.Cut(captionLanguage, "-")
	appendCandidates := func(candidates []ProviderTrailer, candidateLanguage string, fallback bool, limit int) {
		added := 0
		for _, candidate := range candidates {
			if len(trailers) >= maxTrailerOptions || added >= limit {
				return
			}
			if _, duplicate := seen[candidate.YouTubeID]; duplicate {
				continue
			}
			seen[candidate.YouTubeID] = struct{}{}
			trailers = append(trailers, normalizeTrailer(candidate, candidateLanguage, fallback, captionPreference))
			added++
		}
	}

	preferredLimit := maxTrailerOptions
	if len(localized) > 0 && len(english) > 0 {
		preferredLimit = maxPreferredLanguageTrailers
	}
	appendCandidates(localized, language, false, preferredLimit)
	appendCandidates(english, trailerFallbackLanguage, true, maxTrailerOptions)
	appendCandidates(localized, language, false, maxTrailerOptions)
	return trailers
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
