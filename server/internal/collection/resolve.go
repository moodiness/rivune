package collection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
)

var resolutionLanguagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z]{2})?$`)

func (service *Service) ResolveFolder(ctx context.Context, principal auth.Principal, collectionID, folderID string, page, limit int, language, region string) (ResolvedFolder, error) {
	value, err := service.Get(ctx, principal, collectionID)
	if err != nil {
		return ResolvedFolder{}, err
	}
	if !validUUID(folderID) {
		return ResolvedFolder{}, ErrInvalidInput
	}
	for _, folder := range value.Folders {
		if folder.ID == folderID {
			return service.resolve(ctx, principal, value.ID, folder, page, limit, language, region)
		}
	}
	return ResolvedFolder{}, ErrNotFound
}

func (service *Service) LookupTMDB(ctx context.Context, principal auth.Principal, kind, query, language string, page int) ([]LookupResult, error) {
	if _, err := activeProfileID(principal); err != nil {
		return nil, err
	}
	if service.tmdb == nil {
		return nil, ErrProviderUnavailable
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	query = strings.TrimSpace(query)
	language, _, err := normalizeResolutionOptions(language, "")
	if err != nil || !map[string]bool{"company": true, "collection": true, "person": true, "keyword": true}[kind] || !validText(query, 1, 200) || page < 1 || page > 1000 {
		return nil, ErrInvalidInput
	}
	return service.tmdb.LookupCollectionSource(ctx, kind, query, language, page)
}

func (service *Service) TMDBGenres(ctx context.Context, principal auth.Principal, mediaType, language string) ([]Genre, error) {
	if _, err := activeProfileID(principal); err != nil {
		return nil, err
	}
	if service.tmdb == nil {
		return nil, ErrProviderUnavailable
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	language, _, err := normalizeResolutionOptions(language, "")
	if err != nil || mediaType != MediaTypeMovie && mediaType != MediaTypeSeries {
		return nil, ErrInvalidInput
	}
	return service.tmdb.CollectionGenres(ctx, mediaType, language)
}

func (service *Service) resolve(ctx context.Context, principal auth.Principal, collectionID string, folder Folder, page, limit int, language, region string) (ResolvedFolder, error) {
	language, region, err := normalizeResolutionOptions(language, region)
	if err != nil || page < 1 || page > 1000 || limit < 1 || limit > 200 {
		return ResolvedFolder{}, ErrInvalidInput
	}
	outcomes := make([]sourceOutcome, len(folder.Sources))
	semaphore := make(chan struct{}, 8)
	var wait sync.WaitGroup
	for index, source := range folder.Sources {
		index, source := index, source
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				outcomes[index] = sourceOutcome{source: source, err: ctx.Err()}
				return
			}
			result, err := service.resolveSource(ctx, principal, source, page, language, region)
			outcomes[index] = sourceOutcome{source: source, page: result, err: err}
		}()
	}
	wait.Wait()
	items := make([]Item, 0)
	errorsList := make([]SourceFailure, 0)
	hasMore := false
	for _, outcome := range outcomes {
		if outcome.err != nil {
			errorsList = append(errorsList, SourceFailure{
				SourceID: outcome.source.ID, Kind: outcome.source.Kind,
				Code: sourceErrorCode(outcome.err), Message: "The collection source request failed",
			})
			continue
		}
		if folder.CoverImageURL == "" && outcome.page.CoverImageURL != "" {
			folder.CoverImageURL = outcome.page.CoverImageURL
		}
		if folder.HeroBackdropURL == "" && outcome.page.HeroBackdropURL != "" {
			folder.HeroBackdropURL = outcome.page.HeroBackdropURL
		}
		hasMore = hasMore || outcome.page.HasMore
		reference := SourceReference{ID: outcome.source.ID, Kind: outcome.source.Kind, Title: outcome.source.Title}
		if outcome.source.AddonCatalog != nil {
			reference.AddonID = outcome.source.AddonCatalog.AddonID
		}
		for _, item := range outcome.page.Items {
			item.Sources = []SourceReference{reference}
			if item.ExternalIDs == nil {
				item.ExternalIDs = map[string]string{}
			}
			items = append(items, item)
		}
	}
	items = mergeItems(items)
	if len(items) > limit {
		items = items[:limit]
		hasMore = true
	}
	if items == nil {
		items = []Item{}
	}
	return ResolvedFolder{
		CollectionID: collectionID, Folder: folder, Items: items, Page: page,
		HasMore: hasMore, Errors: errorsList,
	}, nil
}

func (service *Service) resolveSource(ctx context.Context, principal auth.Principal, source Source, page int, language, region string) (SourcePage, error) {
	switch source.Kind {
	case SourceKindAddonCatalog:
		if service.addon == nil {
			return SourcePage{}, ErrProviderUnavailable
		}
		settings := source.AddonCatalog
		extra := make([]addon.ExtraValue, 0, len(settings.Extra)+1)
		for _, value := range settings.Extra {
			extra = append(extra, addon.ExtraValue{Name: value.Name, Value: value.Value})
		}
		if page > 1 && !hasExtra(extra, "skip") {
			extra = append(extra, addon.ExtraValue{Name: "skip", Value: strconv.Itoa((page - 1) * 100)})
		}
		result, err := service.addon.Fetch(ctx, principal, settings.AddonID, addon.ResourcePath{
			Resource: "catalog", Type: settings.Type, ID: settings.CatalogID, Extra: extra,
		})
		if err != nil {
			return SourcePage{}, err
		}
		return parseAddonCatalog(result.Payload, page)
	case SourceKindTMDB:
		if service.tmdb == nil {
			return SourcePage{}, ErrProviderUnavailable
		}
		return service.tmdb.ResolveCollectionSource(ctx, *source.TMDB, page, language, region)
	case SourceKindTrakt:
		if service.trakt == nil {
			return SourcePage{}, ErrProviderUnavailable
		}
		return service.trakt.ResolveCollectionSource(ctx, *source.Trakt, page)
	case SourceKindMDBList:
		if service.mdblist == nil {
			return SourcePage{}, ErrProviderUnavailable
		}
		return service.mdblist.ResolveCollectionSource(ctx, *source.MDBList, page)
	default:
		return SourcePage{}, ErrInvalidInput
	}
}

func parseAddonCatalog(payload json.RawMessage, page int) (SourcePage, error) {
	var envelope struct {
		Metas []json.RawMessage `json:"metas"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Metas == nil {
		return SourcePage{}, fmt.Errorf("%w: addon catalog payload has no metas", addon.ErrInvalidResponse)
	}
	items := make([]Item, 0, len(envelope.Metas))
	for _, raw := range envelope.Metas {
		item, ok := parseAddonItem(raw)
		if ok {
			items = append(items, item)
		}
	}
	return SourcePage{Items: items, Page: page, HasMore: len(envelope.Metas) >= 100}, nil
}

func parseAddonItem(raw json.RawMessage) (Item, bool) {
	var value struct {
		ID          string            `json:"id"`
		Type        string            `json:"type"`
		Name        string            `json:"name"`
		Title       string            `json:"title"`
		Poster      string            `json:"poster"`
		Background  string            `json:"background"`
		Logo        string            `json:"logo"`
		Description string            `json:"description"`
		ReleaseInfo string            `json:"releaseInfo"`
		Released    string            `json:"released"`
		IMDBRating  json.RawMessage   `json:"imdbRating"`
		VoteAverage *float64          `json:"voteAverage"`
		VoteCount   *int              `json:"voteCount"`
		Popularity  *float64          `json:"popularity"`
		ExternalIDs map[string]string `json:"externalIds"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return Item{}, false
	}
	value.ID = strings.TrimSpace(value.ID)
	value.Type = normalizeMediaType(value.Type)
	value.Name = strings.TrimSpace(value.Name)
	if value.Name == "" {
		value.Name = strings.TrimSpace(value.Title)
	}
	if value.ID == "" || value.Name == "" {
		return Item{}, false
	}
	externalIDs := value.ExternalIDs
	if externalIDs == nil {
		externalIDs = make(map[string]string)
	}
	if strings.HasPrefix(value.ID, "tt") {
		externalIDs["imdb"] = value.ID
	}
	if parts := strings.SplitN(value.ID, ":", 2); len(parts) == 2 && (parts[0] == "tmdb" || parts[0] == "tvdb" || parts[0] == "kitsu") {
		externalIDs[parts[0]] = parts[1]
	}
	voteAverage := value.VoteAverage
	if voteAverage == nil && len(value.IMDBRating) > 0 && string(value.IMDBRating) != "null" {
		var number float64
		if err := json.Unmarshal(value.IMDBRating, &number); err == nil {
			voteAverage = &number
		} else {
			var text string
			if json.Unmarshal(value.IMDBRating, &text) == nil {
				if parsed, parseErr := strconv.ParseFloat(text, 64); parseErr == nil {
					voteAverage = &parsed
				}
			}
		}
	}
	return Item{
		ID: value.ID, MediaType: value.Type, Title: value.Name, PosterURL: value.Poster,
		BackgroundURL: value.Background, LogoURL: value.Logo, Description: value.Description,
		ReleaseInfo: value.ReleaseInfo, Released: value.Released, VoteAverage: voteAverage,
		VoteCount: value.VoteCount, Popularity: value.Popularity, ExternalIDs: externalIDs,
		Raw: append(json.RawMessage(nil), raw...),
	}, true
}

func mergeItems(input []Item) []Item {
	output := make([]Item, 0, len(input))
	indexes := make(map[string]int, len(input))
	for _, item := range input {
		key := itemKey(item)
		if index, exists := indexes[key]; exists {
			mergeItem(&output[index], item)
			continue
		}
		indexes[key] = len(output)
		output = append(output, item)
	}
	return output
}

func mergeItem(target *Item, candidate Item) {
	target.Sources = append(target.Sources, candidate.Sources...)
	for provider, value := range candidate.ExternalIDs {
		if target.ExternalIDs[provider] == "" {
			target.ExternalIDs[provider] = value
		}
	}
	if target.PosterURL == "" {
		target.PosterURL = candidate.PosterURL
	}
	if target.BackgroundURL == "" {
		target.BackgroundURL = candidate.BackgroundURL
	}
	if target.LogoURL == "" {
		target.LogoURL = candidate.LogoURL
	}
	if target.Description == "" {
		target.Description = candidate.Description
	}
	if target.ReleaseInfo == "" {
		target.ReleaseInfo = candidate.ReleaseInfo
	}
	if target.Released == "" {
		target.Released = candidate.Released
	}
	if target.VoteAverage == nil {
		target.VoteAverage = candidate.VoteAverage
	}
	if target.VoteCount == nil {
		target.VoteCount = candidate.VoteCount
	}
	if target.Popularity == nil {
		target.Popularity = candidate.Popularity
	}
}

func itemKey(item Item) string {
	mediaType := strings.ToLower(strings.TrimSpace(item.MediaType))
	for _, provider := range []string{"tmdb", "imdb", "tvdb", "kitsu", "trakt", "mdblist"} {
		if value := strings.TrimSpace(item.ExternalIDs[provider]); value != "" {
			return mediaType + ":" + provider + ":" + strings.ToLower(value)
		}
	}
	return mediaType + ":" + strings.ToLower(item.ID)
}

func normalizeMediaType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "tv":
		return MediaTypeTV
	case "show", "series":
		return MediaTypeSeries
	case "movie":
		return MediaTypeMovie
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizeResolutionOptions(language, region string) (string, string, error) {
	language = strings.TrimSpace(language)
	if language == "" {
		language = "en-US"
	}
	region = strings.ToUpper(strings.TrimSpace(region))
	if region == "" {
		region = "US"
	}
	if !resolutionLanguagePattern.MatchString(language) || !regionPattern.MatchString(region) {
		return "", "", ErrInvalidInput
	}
	return language, region, nil
}

func hasExtra(values []addon.ExtraValue, name string) bool {
	for _, value := range values {
		if value.Name == name {
			return true
		}
	}
	return false
}

func sourceErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrProviderUnavailable):
		return "collection_provider_unavailable"
	case errors.Is(err, addon.ErrNotFound):
		return "collection_addon_not_found"
	case errors.Is(err, addon.ErrUnsupportedResource):
		return "collection_source_unsupported"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "collection_source_timeout"
	default:
		return "collection_source_failed"
	}
}

type sourceOutcome struct {
	source Source
	page   SourcePage
	err    error
}
