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
	"github.com/moodiness/rivune/server/internal/metadata"
)

var resolutionLanguagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z]{2})?$`)

const (
	addonCatalogPageSize          = 20
	maximumResolutionPayloadBytes = 16 << 20
	maximumResolutionItems        = 2000
)

func (service *Service) ResolveFolder(ctx context.Context, principal auth.Principal, collectionID, folderID string, page, limit int, language, region string) (ResolvedFolder, error) {
	value, err := service.Get(ctx, principal, collectionID)
	if err != nil {
		return ResolvedFolder{}, err
	}
	if !validUUID(folderID) {
		return ResolvedFolder{}, ErrInvalidInput
	}
	for _, folder := range value.Folders {
		if folder.ID != folderID {
			continue
		}
		resolved, err := service.resolve(ctx, principal, value.ID, folder, page, limit, language, region)
		if err != nil {
			return ResolvedFolder{}, err
		}
		if err := service.revalidateCollectionVersion(ctx, principal, value.ID, value.Version); err != nil {
			return ResolvedFolder{}, err
		}
		return resolved, nil
	}
	return ResolvedFolder{}, ErrNotFound
}

func (service *Service) LookupTMDB(ctx context.Context, principal auth.Principal, kind, query, language string, page int) ([]LookupResult, error) {
	if _, err := service.validateActiveProfile(ctx, principal); err != nil {
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
	results, err := service.tmdb.LookupCollectionSource(ctx, kind, query, language, page)
	if _, err := service.validateActiveProfile(ctx, principal); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (service *Service) TMDBGenres(ctx context.Context, principal auth.Principal, mediaType, language string) ([]Genre, error) {
	if _, err := service.validateActiveProfile(ctx, principal); err != nil {
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
	genres, err := service.tmdb.CollectionGenres(ctx, mediaType, language)
	if _, err := service.validateActiveProfile(ctx, principal); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	return genres, nil
}

func (service *Service) resolve(ctx context.Context, principal auth.Principal, collectionID string, folder Folder, page, limit int, language, region string) (ResolvedFolder, error) {
	language, region, err := normalizeResolutionOptions(language, region)
	if err != nil || page < 1 || page > 1000 || limit < 1 || limit > 200 {
		return ResolvedFolder{}, ErrInvalidInput
	}
	coverConfigured := folder.CoverImageURL != ""
	heroBackdropConfigured := folder.HeroBackdropURL != ""
	titleLogoConfigured := folder.TitleLogoURL != ""
	outcomes := make([]sourceOutcome, len(folder.Sources))
	semaphore := make(chan struct{}, 8)
	resolutionCtx, budget := addon.WithPayloadBudget(ctx, maximumResolutionPayloadBytes, maximumResolutionItems)
	defer budget.Cancel()
	var wait sync.WaitGroup
	for index, source := range folder.Sources {
		index, source := index, source
		wait.Add(1)
		go func() {
			defer wait.Done()
			sourceCtx := addon.WithPayloadBudgetSource(resolutionCtx)
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-resolutionCtx.Done():
				outcomes[index] = sourceOutcome{source: source, err: resolutionCtx.Err()}
				return
			}
			result, err := service.resolveSource(sourceCtx, principal, source, page, language, region)
			outcomes[index] = sourceOutcome{source: source, page: result, err: err}
		}()
	}
	wait.Wait()
	if budget.Exceeded() {
		for index, source := range folder.Sources {
			outcomes[index] = sourceOutcome{source: source, err: resolutionBudgetError()}
		}
	}
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
			reference.ManifestID = outcome.source.AddonCatalog.ManifestID
			reference.CatalogID = outcome.source.AddonCatalog.CatalogID
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
	resolvedFanart, resolvedCollectionFanart, sourcePosterURLs := service.enrichFanartArtwork(ctx, folder.Sources, items, language)
	if !coverConfigured && resolvedCollectionFanart.poster != "" {
		folder.CoverImageURL = resolvedCollectionFanart.poster
	}
	if !heroBackdropConfigured && resolvedCollectionFanart.background != "" {
		folder.HeroBackdropURL = resolvedCollectionFanart.background
	}
	if !titleLogoConfigured && resolvedCollectionFanart.logo != "" {
		folder.TitleLogoURL = resolvedCollectionFanart.logo
	}
	if !coverConfigured && resolvedCollectionFanart.poster == "" {
		for _, value := range resolvedFanart {
			if value.poster != "" {
				folder.CoverImageURL = value.poster
				break
			}
		}
	}
	if !heroBackdropConfigured && resolvedCollectionFanart.background == "" {
		for _, value := range resolvedFanart {
			if value.background != "" {
				folder.HeroBackdropURL = value.background
				break
			}
		}
	}
	if items == nil {
		items = []Item{}
	}
	resolved := ResolvedFolder{
		CollectionID: collectionID, Folder: folder, Items: items, SourcePosterURLs: sourcePosterURLs, Page: page,
		HasMore: hasMore, Errors: errorsList,
	}
	if service.artwork != nil {
		service.artwork.PresentResolvedFolder(ctx, &resolved)
	}
	return resolved, nil
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
		if page > 1 {
			skip := (page - 1) * addonCatalogPageSize
			found := false
			for index := range extra {
				if extra[index].Name != "skip" {
					continue
				}
				if base, parseErr := strconv.Atoi(extra[index].Value); parseErr == nil && base > 0 {
					skip += base
				}
				extra[index].Value = strconv.Itoa(skip)
				found = true
				break
			}
			if !found {
				extra = append(extra, addon.ExtraValue{Name: "skip", Value: strconv.Itoa(skip)})
			}
		}
		result, err := service.addon.Fetch(ctx, principal, settings.AddonID, addon.ResourcePath{
			Resource: "catalog", Type: settings.Type, ID: settings.CatalogID, Extra: extra,
		})
		if err != nil {
			return SourcePage{}, err
		}
		if err := addon.EnsurePayloadBytes(ctx, len(result.Payload)); err != nil {
			return SourcePage{}, resolutionBudgetError()
		}
		return parseAddonCatalog(ctx, result.Payload, page)
	case SourceKindTMDB:
		if service.tmdb == nil {
			return SourcePage{}, ErrProviderUnavailable
		}
		result, err := service.tmdb.ResolveCollectionSource(ctx, *source.TMDB, page, language, region)
		return accountSourcePage(ctx, result, err)
	case SourceKindTrakt:
		if service.trakt == nil {
			return SourcePage{}, ErrProviderUnavailable
		}
		result, err := service.trakt.ResolveCollectionSource(ctx, *source.Trakt, page)
		return accountSourcePage(ctx, result, err)
	case SourceKindMDBList:
		if service.mdblist == nil {
			return SourcePage{}, ErrProviderUnavailable
		}
		result, err := service.mdblist.ResolveCollectionSource(ctx, *source.MDBList, page)
		return accountSourcePage(ctx, result, err)
	default:
		return SourcePage{}, ErrInvalidInput
	}
}

func parseAddonCatalog(ctx context.Context, payload json.RawMessage, page int) (SourcePage, error) {
	safePayload, err := addon.SanitizeExposablePayload(payload)
	if err != nil {
		return SourcePage{}, fmt.Errorf("%w: sanitize addon catalog payload", addon.ErrInvalidResponse)
	}
	var sourceEnvelope, safeEnvelope struct {
		Metas []json.RawMessage `json:"metas"`
	}
	if err := json.Unmarshal(payload, &sourceEnvelope); err != nil || sourceEnvelope.Metas == nil {
		return SourcePage{}, fmt.Errorf("%w: addon catalog payload has no metas", addon.ErrInvalidResponse)
	}
	if err := json.Unmarshal(safePayload, &safeEnvelope); err != nil || len(safeEnvelope.Metas) != len(sourceEnvelope.Metas) {
		return SourcePage{}, fmt.Errorf("%w: sanitized addon catalog payload is inconsistent", addon.ErrInvalidResponse)
	}
	if err := addon.ConsumePayloadItems(ctx, len(safeEnvelope.Metas)); err != nil {
		return SourcePage{}, resolutionBudgetError()
	}
	items := make([]Item, 0, len(safeEnvelope.Metas))
	for index, safeRaw := range safeEnvelope.Metas {
		item, ok := parseAddonItem(safeRaw)
		if !ok {
			continue
		}
		var artwork struct {
			Poster     string `json:"poster"`
			Background string `json:"background"`
			Logo       string `json:"logo"`
		}
		if json.Unmarshal(sourceEnvelope.Metas[index], &artwork) == nil {
			item.PosterURL = artwork.Poster
			item.BackgroundURL = artwork.Background
			item.LogoURL = artwork.Logo
		}
		items = append(items, item)
	}
	return SourcePage{Items: items, Page: page, HasMore: len(items) > 0}, nil
}

func accountSourcePage(ctx context.Context, page SourcePage, err error) (SourcePage, error) {
	if err != nil {
		return SourcePage{}, err
	}
	rawBytes := 0
	for index := range page.Items {
		rawBytes += len(page.Items[index].Raw)
	}
	if err := addon.EnsurePayloadBytes(ctx, rawBytes); err != nil {
		return SourcePage{}, resolutionBudgetError()
	}
	if err := addon.EnsurePayloadItems(ctx, len(page.Items)); err != nil {
		return SourcePage{}, resolutionBudgetError()
	}
	for index := range page.Items {
		if len(page.Items[index].Raw) == 0 {
			continue
		}
		safeRaw, sanitizeErr := addon.SanitizeExposablePayload(page.Items[index].Raw)
		if sanitizeErr != nil {
			return SourcePage{}, fmt.Errorf("%w: sanitize collection source item", addon.ErrInvalidResponse)
		}
		page.Items[index].Raw = safeRaw
	}
	return page, nil
}

type resolutionBudgetExceededError struct{}

func (resolutionBudgetExceededError) Error() string {
	return "collection source response budget exceeded"
}

func (resolutionBudgetExceededError) Is(target error) bool {
	return target == ErrProviderUnavailable
}

func (resolutionBudgetExceededError) Temporary() bool {
	return true
}

func resolutionBudgetError() error {
	return resolutionBudgetExceededError{}
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
		Raw: raw,
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

type fanartArtwork struct {
	poster     string
	background string
	logo       string
}

func (value fanartArtwork) available() bool {
	return value.poster != "" || value.background != "" || value.logo != ""
}

func (service *Service) enrichFanartArtwork(ctx context.Context, sources []Source, items []Item, language string) ([]fanartArtwork, fanartArtwork, map[string]string) {
	if service.fanart == nil || liveTVOnlySources(sources) {
		return nil, fanartArtwork{}, nil
	}
	collectionIDs := fanartCollectionTMDBIDs(sources)
	if len(items) == 0 && len(collectionIDs) == 0 {
		return nil, fanartArtwork{}, nil
	}
	itemArtwork := make([]fanartArtwork, len(items))
	itemErrors := make([]error, len(items))
	collectionArtwork := make([]fanartArtwork, len(collectionIDs))
	collectionErrors := make([]error, len(collectionIDs))
	semaphore := make(chan struct{}, 8)
	var wait sync.WaitGroup
	for index, tmdbID := range collectionIDs {
		index, tmdbID := index, tmdbID
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				collectionErrors[index] = ctx.Err()
				return
			}
			value, err := service.fanart.EnrichCollection(ctx, metadata.ProviderCollection{ExternalID: tmdbID}, language)
			collectionErrors[index] = err
			collectionArtwork[index] = fanartArtwork{
				poster:     value.PosterURL,
				background: value.BackdropURL,
				logo:       value.LogoURL,
			}
		}()
	}
	for index := range items {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				itemErrors[index] = ctx.Err()
				return
			}
			itemArtwork[index], itemErrors[index] = service.fanartArtwork(ctx, items[index], language)
		}()
	}
	wait.Wait()
	for index, value := range itemArtwork {
		if itemErrors[index] != nil {
			if service.logger != nil {
				service.logger.DebugContext(ctx, "Fanart item artwork unavailable",
					"mediaType", items[index].MediaType, "itemID", items[index].ID, "error", itemErrors[index])
			}
			continue
		}
		if !value.available() {
			continue
		}
		if value.poster != "" {
			items[index].PosterURL = value.poster
		}
		if value.background != "" {
			items[index].BackgroundURL = value.background
		}
		if value.logo != "" {
			items[index].LogoURL = value.logo
		}
		items[index].FanartResolved = true
	}
	collectionArtworkByTMDBID := make(map[string]fanartArtwork, len(collectionArtwork))
	var folderArtwork fanartArtwork
	for index, value := range collectionArtwork {
		if collectionErrors[index] != nil {
			if service.logger != nil {
				service.logger.DebugContext(ctx, "Fanart movie collection artwork unavailable",
					"tmdbID", collectionIDs[index], "error", collectionErrors[index])
			}
			continue
		}
		if !value.available() {
			continue
		}
		collectionArtworkByTMDBID[collectionIDs[index]] = value
		if !folderArtwork.available() {
			folderArtwork = value
		}
	}
	sourcePosterURLs := make(map[string]string, len(collectionArtworkByTMDBID))
	for _, source := range sources {
		tmdbID, ok := fanartCollectionTMDBID(source)
		if !ok || source.ID == "" {
			continue
		}
		if poster := collectionArtworkByTMDBID[tmdbID].poster; poster != "" {
			sourcePosterURLs[source.ID] = poster
		}
	}
	if len(sourcePosterURLs) == 0 {
		sourcePosterURLs = nil
	}
	return itemArtwork, folderArtwork, sourcePosterURLs
}

func liveTVOnlySources(sources []Source) bool {
	if len(sources) == 0 {
		return false
	}
	for _, source := range sources {
		if source.Kind != SourceKindAddonCatalog || source.AddonCatalog == nil ||
			normalizeMediaType(source.AddonCatalog.Type) != MediaTypeTV {
			return false
		}
	}
	return true
}

func fanartCollectionTMDBIDs(sources []Source) []string {
	ids := make([]string, 0, len(sources))
	seen := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		id, ok := fanartCollectionTMDBID(source)
		if !ok {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func fanartCollectionTMDBID(source Source) (string, bool) {
	if !strings.EqualFold(strings.TrimSpace(source.Kind), "tmdb") || source.TMDB == nil ||
		!strings.EqualFold(strings.TrimSpace(source.TMDB.SourceType), "collection") ||
		source.TMDB.TMDBID == nil || *source.TMDB.TMDBID <= 0 {
		return "", false
	}
	return strconv.FormatInt(*source.TMDB.TMDBID, 10), true
}

func (service *Service) fanartArtwork(ctx context.Context, item Item, language string) (fanartArtwork, error) {
	switch normalizeMediaType(item.MediaType) {
	case MediaTypeMovie:
		tmdbID, err := service.resolveTMDBArtworkID(ctx, item)
		if err != nil || tmdbID == "" {
			return fanartArtwork{}, err
		}
		enriched, err := service.fanart.EnrichMovie(ctx, metadata.ProviderMovie{
			ExternalID: tmdbID, PosterURL: item.PosterURL, BackdropURL: item.BackgroundURL, LogoURL: item.LogoURL,
			AdditionalIDs: map[string]string{"tmdb": tmdbID},
		}, language)
		if err != nil {
			return fanartArtwork{}, err
		}
		return fanartOverlay(item, enriched.PosterURL, enriched.BackdropURL, enriched.LogoURL), nil
	case MediaTypeSeries:
		tvdbID := strings.TrimSpace(item.ExternalIDs["tvdb"])
		if tvdbID == "" {
			tmdbID, err := service.resolveTMDBArtworkID(ctx, item)
			if err != nil || tmdbID == "" || service.artworkMetadata == nil {
				return fanartArtwork{}, err
			}
			series, detailsErr := service.artworkMetadata.SeriesDetails(ctx, tmdbID, language)
			if detailsErr != nil {
				return fanartArtwork{}, detailsErr
			}
			tvdbID = strings.TrimSpace(series.AdditionalIDs["tvdb"])
		}
		if tvdbID == "" {
			return fanartArtwork{}, nil
		}
		enriched, err := service.fanart.EnrichSeries(ctx, metadata.ProviderSeries{
			PosterURL: item.PosterURL, BackdropURL: item.BackgroundURL, LogoURL: item.LogoURL,
			AdditionalIDs: map[string]string{"tvdb": tvdbID},
		}, language)
		if err != nil {
			return fanartArtwork{}, err
		}
		return fanartOverlay(item, enriched.PosterURL, enriched.BackdropURL, enriched.LogoURL), nil
	default:
		return fanartArtwork{}, nil
	}
}

func fanartOverlay(item Item, poster, background, logo string) fanartArtwork {
	value := fanartArtwork{}
	if poster != "" && poster != item.PosterURL {
		value.poster = poster
	}
	if background != "" && background != item.BackgroundURL {
		value.background = background
	}
	if logo != "" && logo != item.LogoURL {
		value.logo = logo
	}
	return value
}

func (service *Service) resolveTMDBArtworkID(ctx context.Context, item Item) (string, error) {
	if tmdbID := strings.TrimSpace(item.ExternalIDs["tmdb"]); tmdbID != "" {
		return tmdbID, nil
	}
	if service.externalResolver == nil {
		return "", nil
	}
	for _, provider := range []string{"imdb", "tvdb"} {
		externalID := strings.TrimSpace(item.ExternalIDs[provider])
		if externalID == "" {
			continue
		}
		tmdbID, err := service.externalResolver.ResolveExternalID(ctx, normalizeMediaType(item.MediaType), provider, externalID)
		if err != nil {
			return "", err
		}
		if tmdbID = strings.TrimSpace(tmdbID); tmdbID != "" {
			return tmdbID, nil
		}
	}
	return "", nil
}

func itemKey(item Item) string {
	mediaType := strings.ToLower(strings.TrimSpace(item.MediaType))
	if mediaType == MediaTypeTV {
		addonID := ""
		manifestID := ""
		for _, source := range item.Sources {
			if value := strings.TrimSpace(source.AddonID); value != "" {
				addonID = strings.ToLower(value)
				break
			}
			if manifestID == "" {
				manifestID = strings.ToLower(strings.TrimSpace(source.ManifestID))
			}
		}
		if addonID != "" {
			return mediaType + ":addon:" + addonID + ":resource:" + strings.ToLower(strings.TrimSpace(item.ID))
		}
		if manifestID != "" {
			return mediaType + ":manifest:" + manifestID + ":resource:" + strings.ToLower(strings.TrimSpace(item.ID))
		}
		return mediaType + ":resource:" + strings.ToLower(strings.TrimSpace(item.ID))
	}
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
