package artwork

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/calendar"
	"github.com/moodiness/rivune/server/internal/collection"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

var titleUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

const (
	maximumAddonArtworkObjectsPerResponse = 256
	maximumAddonArtworkURLsPerResponse    = 256
	maximumCollectionRestorationURLs      = 4096
)

type canonicalArtwork struct {
	poster     string
	background string
	logo       string
}

type artworkIdentity struct {
	provider   string
	externalID string
}

type artworkCandidate struct {
	ordinal    int
	mediaType  string
	identities []artworkIdentity
}

type payloadState struct {
	resultIndex     int
	root            any
	changed         bool
	artworkURLCount int
}

type mapCandidate struct {
	artworkCandidate
	object map[string]any
	state  *payloadState
}

type urlAssignment struct {
	upstream string
	assign   func(string)
}

func (service *Service) PresentAddonResources(ctx context.Context, results []addon.ResourceResult) {
	states := make([]*payloadState, 0, len(results))
	candidates := make([]mapCandidate, 0)
	assignments := make([]urlAssignment, 0)
	for resultIndex := range results {
		if !presentedAddonResource(results[resultIndex].Resource) {
			continue
		}
		root, ok := decodeJSON(results[resultIndex].Payload)
		if !ok {
			continue
		}
		state := &payloadState{resultIndex: resultIndex, root: root}
		states = append(states, state)
		for _, object := range addonArtworkObjects(root) {
			mediaType := normalizedMediaType(textValue(object["type"]))
			if mediaType == "" {
				mediaType = normalizedMediaType(results[resultIndex].Type)
			}
			candidates = append(candidates, mapCandidate{
				artworkCandidate: artworkCandidate{
					ordinal:    len(candidates),
					mediaType:  mediaType,
					identities: identitiesForObject(object),
				},
				object: object,
				state:  state,
			})
		}
	}

	lookup := make([]artworkCandidate, len(candidates))
	for index := range candidates {
		lookup[index] = candidates[index].artworkCandidate
	}
	for ordinal, value := range service.canonicalArtwork(ctx, lookup) {
		if ordinal < 0 || ordinal >= len(candidates) {
			continue
		}
		if applyCanonicalArtwork(candidates[ordinal].object, value) {
			candidates[ordinal].state.changed = true
		}
	}
	for _, state := range states {
		collectMapArtworkAssignments(
			state.root,
			state,
			&assignments,
			&state.artworkURLCount,
		)
	}
	service.applyURLAssignments(ctx, assignments)
	for _, state := range states {
		if !state.changed {
			continue
		}
		encoded, err := json.Marshal(state.root)
		if err != nil {
			continue
		}
		results[state.resultIndex].Payload = encoded
	}
}

func (service *Service) PresentInstalledAddons(ctx context.Context, values []addon.InstalledAddon) {
	type manifestState struct {
		index    int
		manifest map[string]any
		changed  bool
	}
	states := make([]*manifestState, 0, len(values))
	assignments := make([]urlAssignment, 0, len(values)*2)
	for index := range values {
		root, ok := decodeJSON(values[index].Manifest)
		if !ok {
			continue
		}
		manifest, ok := root.(map[string]any)
		if !ok {
			continue
		}
		state := &manifestState{index: index, manifest: manifest}
		states = append(states, state)
		for _, field := range []string{"logo", "background"} {
			upstream, ok := manifest[field].(string)
			if !ok || strings.TrimSpace(upstream) == "" {
				continue
			}
			field := field
			original := upstream
			assignments = append(assignments, urlAssignment{
				upstream: upstream,
				assign: func(localized string) {
					if localized != original {
						state.manifest[field] = localized
						state.changed = true
					}
				},
			})
		}
	}
	service.applyURLAssignments(ctx, assignments)
	for _, state := range states {
		if !state.changed {
			continue
		}
		encoded, err := json.Marshal(state.manifest)
		if err == nil {
			values[state.index].Manifest = encoded
		}
	}
}

func (service *Service) LocalizeCatalogDescriptors(ctx context.Context, values []addon.CatalogDescriptor) {
	assignments := make([]urlAssignment, 0, len(values))
	for index := range values {
		addStringAssignment(&assignments, &values[index].AddonLogoURL)
	}
	service.applyURLAssignments(ctx, assignments)
}

func (service *Service) LocalizeCollectionLookupResults(ctx context.Context, values []collection.LookupResult) {
	assignments := make([]urlAssignment, 0, len(values))
	for index := range values {
		addStringAssignment(&assignments, &values[index].ImageURL)
	}
	service.applyURLAssignments(ctx, assignments)
}

func (service *Service) PresentCollections(ctx context.Context, values []collection.Collection) {
	assignments := make([]urlAssignment, 0)
	for index := range values {
		addStringAssignment(&assignments, &values[index].BackdropImageURL)
		for folderIndex := range values[index].Folders {
			folder := &values[index].Folders[folderIndex]
			addStringAssignment(&assignments, &folder.CoverImageURL)
			addStringAssignment(&assignments, &folder.TitleLogoURL)
			addStringAssignment(&assignments, &folder.HeroBackdropURL)
		}
	}
	service.applyURLAssignments(ctx, assignments)
}

func (service *Service) RestoreCollectionSaveInput(ctx context.Context, input *collection.SaveInput) {
	if input == nil {
		return
	}
	values := []*string{&input.BackdropImageURL}
	for index := range input.Folders {
		folder := &input.Folders[index]
		values = append(values, &folder.CoverImageURL, &folder.TitleLogoURL, &folder.HeroBackdropURL)
	}
	service.restoreSourceURLs(ctx, values)
}

func (service *Service) RestoreCollectionSaveInputs(ctx context.Context, inputs []collection.SaveInput) {
	if len(inputs) > 100 {
		return
	}
	valueCount := len(inputs)
	for index := range inputs {
		folderCount := len(inputs[index].Folders)
		if folderCount > (maximumCollectionRestorationURLs-valueCount)/3 {
			return
		}
		valueCount += folderCount * 3
	}
	values := make([]*string, 0, valueCount)
	for index := range inputs {
		input := &inputs[index]
		values = append(values, &input.BackdropImageURL)
		for folderIndex := range input.Folders {
			folder := &input.Folders[folderIndex]
			values = append(values, &folder.CoverImageURL, &folder.TitleLogoURL, &folder.HeroBackdropURL)
		}
	}
	service.restoreSourceURLs(ctx, values)
}

func (service *Service) PresentSemanticSearchPage(ctx context.Context, page *collection.SemanticSearchPage) {
	if page == nil {
		return
	}
	resolved := collection.ResolvedFolder{Items: page.Items, SourcePosterURLs: map[string]string{}}
	service.PresentResolvedFolder(ctx, &resolved)
	page.Items = resolved.Items
}

func (service *Service) PresentResolvedFolder(ctx context.Context, resolved *collection.ResolvedFolder) {
	if resolved == nil {
		return
	}
	originalArtwork := make([]canonicalArtwork, len(resolved.Items))
	for index := range resolved.Items {
		item := &resolved.Items[index]
		originalArtwork[index] = canonicalArtwork{poster: item.PosterURL, background: item.BackgroundURL, logo: item.LogoURL}
	}
	candidates := make([]artworkCandidate, len(resolved.Items))
	for index := range resolved.Items {
		item := &resolved.Items[index]
		if item.FanartResolved {
			continue
		}
		candidates[index] = artworkCandidate{
			ordinal: index, mediaType: normalizedMediaType(item.MediaType),
			identities: identitiesForItem(item.ID, item.ExternalIDs),
		}
	}
	canonical := service.canonicalArtwork(ctx, candidates)
	if canonical == nil {
		canonical = make(map[int]canonicalArtwork)
	}
	for index := range resolved.Items {
		item := &resolved.Items[index]
		if item.FanartResolved {
			canonical[index] = canonicalArtwork{poster: item.PosterURL, background: item.BackgroundURL, logo: item.LogoURL}
			continue
		}
		value, exists := canonical[index]
		if !exists {
			continue
		}
		if value.poster != "" {
			item.PosterURL = value.poster
		}
		if value.background != "" {
			item.BackgroundURL = value.background
		}
		if value.logo != "" {
			item.LogoURL = value.logo
		}
	}

	sourcePosterIDs := make([]string, 0, len(resolved.SourcePosterURLs))
	sourcePosterURLs := make([]string, 0, len(resolved.SourcePosterURLs))
	for sourceID, posterURL := range resolved.SourcePosterURLs {
		sourcePosterIDs = append(sourcePosterIDs, sourceID)
		sourcePosterURLs = append(sourcePosterURLs, posterURL)
	}
	assignments := make([]urlAssignment, 0, len(resolved.Items)*3+3+len(sourcePosterURLs))
	for index := range sourcePosterURLs {
		addStringAssignment(&assignments, &sourcePosterURLs[index])
	}
	addStringAssignment(&assignments, &resolved.Folder.CoverImageURL)
	addStringAssignment(&assignments, &resolved.Folder.TitleLogoURL)
	addStringAssignment(&assignments, &resolved.Folder.HeroBackdropURL)
	rawStates := make([]*payloadState, 0, len(resolved.Items))
	for index := range resolved.Items {
		item := &resolved.Items[index]
		addStringAssignment(&assignments, &item.PosterURL)
		addStringAssignment(&assignments, &item.BackgroundURL)
		addStringAssignment(&assignments, &item.LogoURL)
		root, ok := decodeJSON(item.Raw)
		if !ok {
			continue
		}
		state := &payloadState{resultIndex: index, root: root}
		rawStates = append(rawStates, state)
		if value, exists := canonical[index]; exists {
			if object, objectOK := root.(map[string]any); objectOK && applyCanonicalArtwork(object, value) {
				state.changed = true
			}
		}
		collectMapArtworkAssignments(root, state, &assignments, &state.artworkURLCount)
	}
	service.applyURLAssignments(ctx, assignments)
	for index, sourceID := range sourcePosterIDs {
		resolved.SourcePosterURLs[sourceID] = sourcePosterURLs[index]
	}
	fallbackAssignments := make([]urlAssignment, 0, len(resolved.Items)*3)
	for index := range resolved.Items {
		item := &resolved.Items[index]
		fallback := &originalArtwork[index]
		if item.PosterURL == "" {
			addStringAssignment(&fallbackAssignments, &fallback.poster)
		}
		if item.BackgroundURL == "" {
			addStringAssignment(&fallbackAssignments, &fallback.background)
		}
		if item.LogoURL == "" {
			addStringAssignment(&fallbackAssignments, &fallback.logo)
		}
	}
	service.applyURLAssignments(ctx, fallbackAssignments)
	for index := range resolved.Items {
		item := &resolved.Items[index]
		fallback := originalArtwork[index]
		if item.PosterURL == "" {
			item.PosterURL = fallback.poster
		}
		if item.BackgroundURL == "" {
			item.BackgroundURL = fallback.background
		}
		if item.LogoURL == "" {
			item.LogoURL = fallback.logo
		}
	}
	for _, state := range rawStates {
		item := &resolved.Items[state.resultIndex]
		if object, ok := state.root.(map[string]any); ok && applyCanonicalArtwork(object, canonicalArtwork{
			poster: item.PosterURL, background: item.BackgroundURL, logo: item.LogoURL,
		}) {
			state.changed = true
		}
		if !state.changed {
			continue
		}
		encoded, err := json.Marshal(state.root)
		if err == nil {
			resolved.Items[state.resultIndex].Raw = encoded
		}
	}
}

func (service *Service) LocalizeMovie(ctx context.Context, value *metadata.Movie) {
	if value == nil {
		return
	}
	values := make([]*string, 0, len(value.Cast)+5)
	values = append(values, &value.PosterURL, &value.BackdropURL, &value.LogoURL, &value.BannerURL, &value.ArtURL)
	for index := range value.Cast {
		values = append(values, &value.Cast[index].ProfileURL)
	}
	service.localizeStrings(ctx, values...)
}

func (service *Service) LocalizeMoviePage(ctx context.Context, value *metadata.MoviePage) {
	if value == nil {
		return
	}
	artworkCount := len(value.Items) * 5
	for index := range value.Items {
		artworkCount += len(value.Items[index].Cast)
	}
	values := make([]*string, 0, artworkCount)
	for index := range value.Items {
		item := &value.Items[index]
		values = append(values, &item.PosterURL, &item.BackdropURL, &item.LogoURL, &item.BannerURL, &item.ArtURL)
		for castIndex := range item.Cast {
			values = append(values, &item.Cast[castIndex].ProfileURL)
		}
	}
	service.localizeStrings(ctx, values...)
}

func (service *Service) LocalizeSeries(ctx context.Context, value *metadata.Series) {
	if value == nil {
		return
	}
	values := make([]*string, 0, len(value.Cast)+len(value.Seasons)*2+5)
	values = append(values, &value.PosterURL, &value.BackdropURL, &value.LogoURL, &value.BannerURL, &value.ArtURL)
	for index := range value.Cast {
		values = append(values, &value.Cast[index].ProfileURL)
	}
	for index := range value.Seasons {
		values = append(values, &value.Seasons[index].PosterURL, &value.Seasons[index].BackdropURL)
	}
	service.localizeStrings(ctx, values...)
}

func (service *Service) LocalizeSeriesPage(ctx context.Context, value *metadata.SeriesPage) {
	if value == nil {
		return
	}
	artworkCount := len(value.Items) * 5
	for index := range value.Items {
		artworkCount += len(value.Items[index].Cast) + len(value.Items[index].Seasons)*2
	}
	values := make([]*string, 0, artworkCount)
	for index := range value.Items {
		item := &value.Items[index]
		values = append(values, &item.PosterURL, &item.BackdropURL, &item.LogoURL, &item.BannerURL, &item.ArtURL)
		for castIndex := range item.Cast {
			values = append(values, &item.Cast[castIndex].ProfileURL)
		}
		for seasonIndex := range item.Seasons {
			values = append(values, &item.Seasons[seasonIndex].PosterURL, &item.Seasons[seasonIndex].BackdropURL)
		}
	}
	service.localizeStrings(ctx, values...)
}

func (service *Service) LocalizeSeason(ctx context.Context, value *metadata.Season) {
	if value == nil {
		return
	}
	values := make([]*string, 0, len(value.Episodes)*2+2)
	values = append(values, &value.PosterURL, &value.BackdropURL)
	for index := range value.Episodes {
		values = append(values, &value.Episodes[index].StillURL, &value.Episodes[index].BackdropURL)
	}
	service.localizeStrings(ctx, values...)
}

func (service *Service) LocalizeTitleReference(ctx context.Context, value *watchstate.TitleReference) {
	if value != nil {
		service.localizeStrings(ctx, &value.PosterURL, &value.BackgroundURL)
	}
}

func (service *Service) LocalizeLibraryItem(ctx context.Context, value *watchstate.LibraryItem) {
	if value != nil {
		service.localizeStrings(ctx, &value.PosterURL, &value.BackgroundURL)
	}
}

func (service *Service) LocalizeLibraryPage(ctx context.Context, value *watchstate.LibraryPage) {
	if value == nil {
		return
	}
	values := make([]*string, 0, len(value.Items)*2)
	for index := range value.Items {
		values = append(values, &value.Items[index].PosterURL, &value.Items[index].BackgroundURL)
	}
	service.localizeStrings(ctx, values...)
}

func (service *Service) LocalizeContinuePage(ctx context.Context, value *watchstate.ContinuePage) {
	if value == nil {
		return
	}
	values := make([]*string, 0, len(value.Items)*3)
	for index := range value.Items {
		values = append(values, &value.Items[index].PosterURL, &value.Items[index].BackgroundURL, &value.Items[index].EpisodeStillURL)
	}
	service.localizeStrings(ctx, values...)
}

func (service *Service) LocalizeRecommendationPage(ctx context.Context, value *watchstate.RecommendationPage) {
	if value == nil {
		return
	}
	values := make([]*string, 0, len(value.Items)*2)
	for index := range value.Items {
		values = append(values, &value.Items[index].Item.PosterURL, &value.Items[index].Item.BackgroundURL)
	}
	service.localizeStrings(ctx, values...)
}

func (service *Service) LocalizeCalendar(ctx context.Context, value *calendar.Result) {
	if value == nil {
		return
	}
	values := make([]*string, 0, len(value.Events))
	for index := range value.Events {
		values = append(values, &value.Events[index].PosterURL)
	}
	service.localizeStrings(ctx, values...)
}

func (service *Service) LocalizePlaybackActivity(ctx context.Context, value *playback.Activity) {
	if value == nil {
		return
	}
	values := make([]*string, 0, len(value.Sessions))
	for index := range value.Sessions {
		values = append(values, &value.Sessions[index].ArtworkURL)
	}
	service.localizeStrings(ctx, values...)
}

func (service *Service) canonicalArtwork(ctx context.Context, candidates []artworkCandidate) map[int]canonicalArtwork {
	candidateIndexes := make([]int32, 0)
	providers := make([]string, 0)
	externalIDs := make([]string, 0)
	mediaTypes := make([]string, 0)
	for _, candidate := range candidates {
		if candidate.mediaType == "" {
			continue
		}
		for _, identity := range candidate.identities {
			if identity.provider == "" || identity.externalID == "" {
				continue
			}
			candidateIndexes = append(candidateIndexes, int32(candidate.ordinal))
			providers = append(providers, identity.provider)
			externalIDs = append(externalIDs, identity.externalID)
			mediaTypes = append(mediaTypes, candidate.mediaType)
		}
	}
	if len(candidateIndexes) == 0 {
		return nil
	}
	rows, err := service.pool.Query(ctx, `
		WITH requested AS (
			SELECT candidate, provider, external_id, media_type
			FROM unnest($1::integer[], $2::text[], $3::text[], $4::text[])
				AS input(candidate, provider, external_id, media_type)
		), matches AS (
			SELECT requested.candidate, titles.id AS title_id,
				CASE requested.provider WHEN 'imdb' THEN 1 WHEN 'tmdb' THEN 2 WHEN 'tvdb' THEN 3 ELSE 4 END AS priority
			FROM requested
			JOIN title_external_ids external
				ON external.provider = requested.provider AND external.external_id = requested.external_id
			JOIN titles ON titles.id = external.title_id AND titles.media_type = requested.media_type
			WHERE requested.provider <> 'rivune'
			UNION ALL
			SELECT requested.candidate, titles.id AS title_id, 0 AS priority
			FROM requested
			JOIN titles ON titles.id::text = requested.external_id AND titles.media_type = requested.media_type
			WHERE requested.provider = 'rivune'
		), selected AS (
			SELECT DISTINCT ON (candidate) candidate, title_id
			FROM matches
			ORDER BY candidate, priority, title_id
		)
		SELECT selected.candidate,
			COALESCE(NULLIF(cached.payload->>'posterUrl', ''), titles.poster_url, ''),
			COALESCE(NULLIF(cached.payload->>'backdropUrl', ''), titles.background_url, ''),
			COALESCE(NULLIF(cached.payload->>'logoUrl', ''), '')
		FROM selected
		JOIN titles ON titles.id = selected.title_id
		LEFT JOIN LATERAL (
			SELECT metadata.payload
			FROM title_metadata metadata
			WHERE metadata.title_id = selected.title_id
			ORDER BY
				(NULLIF(metadata.payload->>'logoUrl', '') IS NOT NULL) DESC,
				(NULLIF(metadata.payload->>'posterUrl', '') IS NOT NULL) DESC,
				metadata.updated_at DESC
			LIMIT 1
		) cached ON true
	`, candidateIndexes, providers, externalIDs, mediaTypes)
	if err != nil {
		service.logger.WarnContext(ctx, "resolve canonical artwork", "error", err)
		return nil
	}
	defer rows.Close()
	resolved := make(map[int]canonicalArtwork)
	for rows.Next() {
		var index int
		var value canonicalArtwork
		if err := rows.Scan(&index, &value.poster, &value.background, &value.logo); err != nil {
			service.logger.WarnContext(ctx, "scan canonical artwork", "error", err)
			return nil
		}
		resolved[index] = value
	}
	if err := rows.Err(); err != nil {
		service.logger.WarnContext(ctx, "read canonical artwork", "error", err)
		return nil
	}
	return resolved
}

func (service *Service) localizeStrings(ctx context.Context, values ...*string) {
	assignments := make([]urlAssignment, 0, len(values))
	for _, value := range values {
		addStringAssignment(&assignments, value)
	}
	service.applyURLAssignments(ctx, assignments)
}

func (service *Service) restoreSourceURLs(ctx context.Context, values []*string) {
	type restoration struct {
		value *string
		key   string
	}
	restorations := make([]restoration, 0, len(values))
	keys := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == nil || !strings.HasPrefix(*value, publicPrefix) {
			continue
		}
		key := strings.TrimPrefix(*value, publicPrefix)
		if !validKey(key) {
			continue
		}
		restorations = append(restorations, restoration{value: value, key: key})
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return
	}
	rows, err := service.pool.Query(ctx, `
		SELECT key, source_url
		FROM artwork_cache
		WHERE key = ANY($1::text[])
	`, keys)
	if err != nil {
		service.logger.WarnContext(ctx, "restore artwork source URLs", "error", err)
		return
	}
	defer rows.Close()
	sources := make(map[string]string, len(keys))
	for rows.Next() {
		var key, source string
		if err := rows.Scan(&key, &source); err != nil {
			service.logger.WarnContext(ctx, "scan artwork source URL", "error", err)
			return
		}
		sources[key] = source
	}
	if err := rows.Err(); err != nil {
		service.logger.WarnContext(ctx, "read artwork source URLs", "error", err)
		return
	}
	for _, restoration := range restorations {
		if source := sources[restoration.key]; source != "" {
			*restoration.value = source
		}
	}
}

func (service *Service) applyURLAssignments(ctx context.Context, assignments []urlAssignment) {
	if len(assignments) == 0 {
		return
	}
	upstream := make([]string, len(assignments))
	for index := range assignments {
		upstream[index] = assignments[index].upstream
	}
	localized := service.LocalURLs(ctx, upstream)
	for index := range assignments {
		assignments[index].assign(localized[index])
	}
}

func addStringAssignment(assignments *[]urlAssignment, value *string) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return
	}
	assignmentsValue := value
	*assignments = append(*assignments, urlAssignment{
		upstream: *value,
		assign: func(localized string) {
			*assignmentsValue = localized
		},
	})
}

func collectMapArtworkAssignments(
	value any,
	state *payloadState,
	assignments *[]urlAssignment,
	count *int,
) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if isArtworkKey(key) || isCastProfileKey(key, typed) {
				if upstream, ok := child.(string); ok && strings.TrimSpace(upstream) != "" {
					if *count >= maximumAddonArtworkURLsPerResponse {
						typed[key] = ""
						state.changed = true
					} else {
						object := typed
						field := key
						original := upstream
						*assignments = append(*assignments, urlAssignment{
							upstream: upstream,
							assign: func(localized string) {
								if localized != original {
									object[field] = localized
									state.changed = true
								}
							},
						})
						(*count)++
					}
				}
			}
			collectMapArtworkAssignments(child, state, assignments, count)
		}
	case []any:
		for _, child := range typed {
			collectMapArtworkAssignments(child, state, assignments, count)
		}
	}
}

func presentedAddonResource(resource string) bool {
	switch strings.ToLower(strings.TrimSpace(resource)) {
	case "catalog", "addon_catalog", "meta":
		return true
	default:
		return false
	}
}

func addonArtworkObjects(root any) []map[string]any {
	envelope, ok := root.(map[string]any)
	if !ok {
		return nil
	}
	objects := make([]map[string]any, 0, maximumAddonArtworkObjectsPerResponse)
	for _, key := range []string{"metas", "metasDetailed", "meta"} {
		switch value := envelope[key].(type) {
		case map[string]any:
			objects = append(objects, value)
		case []any:
			for _, child := range value {
				if object, objectOK := child.(map[string]any); objectOK {
					objects = append(objects, object)
				}
				if len(objects) == maximumAddonArtworkObjectsPerResponse {
					return objects
				}
			}
		}
		if len(objects) == maximumAddonArtworkObjectsPerResponse {
			return objects
		}
	}
	return objects
}

func applyCanonicalArtwork(object map[string]any, value canonicalArtwork) bool {
	changed := false
	if value.poster != "" {
		changed = setArtworkValue(object, "poster", []string{"posterUrl"}, value.poster) || changed
	}
	if value.background != "" {
		changed = setArtworkValue(object, "background", []string{"backgroundUrl", "backdrop", "backdropUrl"}, value.background) || changed
	}
	if value.logo != "" {
		changed = setArtworkValue(object, "logo", []string{"logoUrl"}, value.logo) || changed
	}
	return changed
}

func setArtworkValue(object map[string]any, primary string, aliases []string, value string) bool {
	changed := textValue(object[primary]) != value
	object[primary] = value
	for _, alias := range aliases {
		if _, exists := object[alias]; exists {
			if textValue(object[alias]) != value {
				changed = true
			}
			object[alias] = value
		}
	}
	return changed
}

func identitiesForObject(object map[string]any) []artworkIdentity {
	external := make(map[string]string)
	for _, key := range []string{"externalIds", "external_ids"} {
		if values, ok := object[key].(map[string]any); ok {
			for provider, value := range values {
				external[provider] = textValue(value)
			}
		}
	}
	for _, key := range []string{"imdb", "imdbId", "imdb_id", "tmdb", "tmdbId", "tmdb_id", "tvdb", "tvdbId", "tvdb_id"} {
		if value := textValue(object[key]); value != "" {
			external[key] = value
		}
	}
	return identitiesForItem(textValue(object["id"]), external)
}

func identitiesForItem(id string, external map[string]string) []artworkIdentity {
	identities := make([]artworkIdentity, 0, len(external)+2)
	seen := make(map[artworkIdentity]struct{}, len(external)+2)
	appendIdentity := func(provider, value string) {
		provider = normalizedProvider(provider)
		value = strings.TrimSpace(value)
		identity := artworkIdentity{provider: provider, externalID: value}
		if provider == "" || value == "" {
			return
		}
		if _, exists := seen[identity]; exists {
			return
		}
		seen[identity] = struct{}{}
		identities = append(identities, identity)
	}
	id = strings.TrimSpace(id)
	if titleUUIDPattern.MatchString(id) {
		appendIdentity("rivune", id)
	}
	if strings.HasPrefix(strings.ToLower(id), "tt") {
		appendIdentity("imdb", id)
	}
	if parts := strings.SplitN(id, ":", 2); len(parts) == 2 {
		appendIdentity(parts[0], parts[1])
	}
	for provider, value := range external {
		appendIdentity(provider, value)
	}
	return identities
}

func normalizedProvider(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch strings.ReplaceAll(value, "_", "") {
	case "imdb", "imdbid":
		return "imdb"
	case "tmdb", "tmdbid":
		return "tmdb"
	case "tvdb", "tvdbid":
		return "tvdb"
	case "trakt", "traktid":
		return "trakt"
	case "mdblist", "mdblistid":
		return "mdblist"
	case "rivune":
		return "rivune"
	default:
		return ""
	}
}

func normalizedMediaType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "movie", "film":
		return "movie"
	case "series", "show":
		return "series"
	default:
		return ""
	}
}

func isArtworkKey(value string) bool {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "")) {
	case "poster", "posterurl", "background", "backgroundurl", "backdrop", "backdropurl", "logo", "logourl", "still", "stillurl", "image", "imageurl", "thumbnail", "thumbnailurl", "profileurl", "photo":
		return true
	default:
		return false
	}
}

func isCastProfileKey(value string, object map[string]any) bool {
	if strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "")) != "profile" {
		return false
	}
	for _, field := range []string{"name", "actor"} {
		if text, ok := object[field].(string); ok && strings.TrimSpace(text) != "" {
			return true
		}
	}
	person, ok := object["person"].(map[string]any)
	if !ok {
		return false
	}
	name, _ := person["name"].(string)
	return strings.TrimSpace(name) != ""
}

func decodeJSON(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	return value, true
}

func textValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}
