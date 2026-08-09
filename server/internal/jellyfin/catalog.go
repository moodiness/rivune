package jellyfin

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/collection"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

const (
	maximumCatalogSearchCandidates     = 200
	maximumCatalogMetadataPagesPerType = 10
	maximumCatalogMetadataItemsPerType = 200
)

type CatalogSearchQuery struct {
	SearchTerm string
	MediaTypes []string
	IDs        []string
	Offset     int
	Limit      int
	SortBy     string
	SortOrder  string
}

type CatalogSearchPage struct {
	Items      []watchstate.CatalogTitle
	Offset     int
	Limit      int
	Total      int
	ExactTotal bool
}

type catalogStore interface {
	GetCatalogTitle(context.Context, auth.Principal, string) (watchstate.CatalogTitle, error)
	GetCatalogTitles(context.Context, auth.Principal, []string) ([]watchstate.CatalogTitle, error)
	ListCatalogItems(context.Context, auth.Principal, watchstate.CatalogQuery) (watchstate.CatalogPage, error)
	ResolveLinkedCatalogTitle(context.Context, auth.Principal, watchstate.ResolveTitleInput) (watchstate.TitleReference, error)
}

type catalogMetadata interface {
	SearchLinkedMovies(context.Context, auth.Principal, metadata.SearchOptions) (metadata.MoviePage, error)
	SearchLinkedSeries(context.Context, auth.Principal, metadata.SearchOptions) (metadata.SeriesPage, error)
}

type catalogMetadataDetails interface {
	MovieDetails(context.Context, auth.Principal, string, string) (metadata.Movie, error)
	SeriesDetails(context.Context, auth.Principal, string, metadata.SeriesDetailsOptions) (metadata.Series, error)
	SeasonDetails(context.Context, auth.Principal, string, string, string) (metadata.Season, error)
}

type catalogAddons interface {
	SearchCatalogItems(context.Context, auth.Principal, []string, string, int, addon.CatalogSearchArtworkPresenter) (addon.CatalogSearchPage, error)
}

// CatalogArtworkPresenter materializes provider artwork as registered same-origin references.
type CatalogArtworkPresenter interface {
	addon.CatalogSearchArtworkPresenter
	LocalURLs(context.Context, []string) []string
}

type catalogReader struct {
	store           catalogStore
	metadata        catalogMetadata
	addons          catalogAddons
	metadataDetails catalogMetadataDetails
	artwork         CatalogArtworkPresenter
}

func NewCatalogReader(store catalogStore, metadataService catalogMetadata, addonService catalogAddons, artworkPresenter CatalogArtworkPresenter) (CatalogReader, error) {
	if store == nil {
		return nil, ErrInvalidDependencies
	}
	metadataDetails, _ := metadataService.(catalogMetadataDetails)
	return &catalogReader{store: store, metadata: metadataService, metadataDetails: metadataDetails, addons: addonService, artwork: artworkPresenter}, nil
}

func (reader *catalogReader) GetCatalogTitle(ctx context.Context, principal auth.Principal, id string) (watchstate.CatalogTitle, error) {
	title, err := reader.store.GetCatalogTitle(ctx, principal, id)
	if err != nil {
		return watchstate.CatalogTitle{}, err
	}
	titles := []watchstate.CatalogTitle{title}
	reader.localizeCatalogTitles(ctx, titles)
	return titles[0], nil
}

func (reader *catalogReader) EnrichCatalogTitle(ctx context.Context, principal auth.Principal, title watchstate.CatalogTitle) (watchstate.CatalogTitle, error) {
	if reader == nil || reader.metadataDetails == nil {
		return title, nil
	}
	var detail watchstate.CatalogTitle
	var err error
	switch title.MediaType {
	case "movie":
		var movie metadata.Movie
		movie, err = reader.metadataDetails.MovieDetails(ctx, principal, title.ID, "")
		if err == nil {
			detail = catalogTitleFromMovie(movie)
		}
	case "series":
		var series metadata.Series
		series, err = reader.metadataDetails.SeriesDetails(ctx, principal, title.ID, metadata.SeriesDetailsOptions{})
		if err == nil {
			detail = catalogTitleFromSeries(series)
		}
	case "season":
		var season metadata.Season
		season, err = reader.metadataDetails.SeasonDetails(ctx, principal, title.ID, "", "")
		if err == nil {
			detail = catalogTitleFromSeason(season)
		}
	case "episode":
		if title.SeasonID == "" {
			return title, nil
		}
		var season metadata.Season
		season, err = reader.metadataDetails.SeasonDetails(ctx, principal, title.SeasonID, "", "")
		if err == nil {
			for _, episode := range season.Episodes {
				if episode.ID == title.ID {
					detail = catalogTitleFromEpisode(episode)
					break
				}
			}
		}
	default:
		return title, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return watchstate.CatalogTitle{}, ctx.Err()
		}
		return title, nil
	}
	localized := []watchstate.CatalogTitle{detail}
	reader.localizeCatalogTitles(ctx, localized)
	return mergeCatalogTitleMetadata(title, localized[0]), nil
}

func (reader *catalogReader) GetCatalogTitles(ctx context.Context, principal auth.Principal, ids []string) ([]watchstate.CatalogTitle, error) {
	titles, err := reader.store.GetCatalogTitles(ctx, principal, ids)
	if err != nil {
		return nil, err
	}
	reader.localizeCatalogTitles(ctx, titles)
	return titles, nil
}

func (reader *catalogReader) LocalizeArtworkURLs(ctx context.Context, upstream []string) []string {
	localized := make([]string, len(upstream))
	if reader == nil || reader.artwork == nil || len(upstream) == 0 {
		return localized
	}
	materialized := reader.artwork.LocalURLs(ctx, upstream)
	if len(materialized) != len(upstream) {
		return localized
	}
	for index, value := range materialized {
		if _, valid := localizedArtworkTag(value); valid {
			localized[index] = value
		}
	}
	return localized
}

func (reader *catalogReader) ListCatalogItems(ctx context.Context, principal auth.Principal, query watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
	page, err := reader.store.ListCatalogItems(ctx, principal, query)
	if err != nil {
		return watchstate.CatalogPage{}, err
	}
	reader.localizeCatalogTitles(ctx, page.Items)
	return page, nil
}

// ResolveCollectionItem canonicalizes one authorized collection result through
// the same linked-session title path used by provider search. The returned title
// is then read back through the catalog boundary so playback keeps using Rivune's
// canonical UUID.
func (reader *catalogReader) ResolveCollectionItem(ctx context.Context, principal auth.Principal, item collection.Item) (watchstate.CatalogTitle, error) {
	mediaType := strings.ToLower(strings.TrimSpace(item.MediaType))
	if mediaType != collection.MediaTypeMovie && mediaType != collection.MediaTypeSeries {
		return watchstate.CatalogTitle{}, fmt.Errorf("%w: unsupported collection media type", watchstate.ErrInvalidInput)
	}
	resourceID := strings.TrimSpace(item.ID)
	if resourceID == "" {
		return watchstate.CatalogTitle{}, fmt.Errorf("%w: missing collection resource identifier", watchstate.ErrInvalidInput)
	}
	provider, externalID := collectionProviderIdentity(item)
	if provider == "" || externalID == "" {
		return watchstate.CatalogTitle{}, fmt.Errorf("%w: collection item has no authorized provider identity", watchstate.ErrInvalidInput)
	}
	input := watchstate.ResolveTitleInput{
		MediaType: mediaType, Provider: provider, ExternalID: externalID, ResourceID: resourceID,
		Title: item.Title, PosterURL: item.PosterURL, BackgroundURL: item.BackgroundURL,
		ReleaseInfo: item.ReleaseInfo, Released: item.Released,
	}
	if item.Released != "" {
		if parsed, err := time.Parse(time.DateOnly, item.Released); err != nil || parsed.Format(time.DateOnly) != item.Released {
			input.Released = ""
		}
	}
	if provider == "addon" {
		source, ok := collectionAddonSource(item.Sources)
		if !ok {
			return watchstate.CatalogTitle{}, fmt.Errorf("%w: collection item has no authorized provider identity", watchstate.ErrInvalidInput)
		}
		input.SourceAddonID = source.AddonID
		input.SourceCatalogID = source.CatalogID
		input.SourceName = source.Title
	}
	localized := []watchstate.CatalogTitle{{PosterURL: input.PosterURL, BackgroundURL: input.BackgroundURL}}
	reader.localizeCatalogTitles(ctx, localized)
	input.PosterURL, input.BackgroundURL = localized[0].PosterURL, localized[0].BackgroundURL
	reference, err := reader.store.ResolveLinkedCatalogTitle(ctx, principal, input)
	if err != nil {
		return watchstate.CatalogTitle{}, err
	}
	return reader.GetCatalogTitle(ctx, principal, reference.TitleID)
}

func collectionProviderIdentity(item collection.Item) (string, string) {
	identities := copyProviderIDs(item.ExternalIDs)
	for _, provider := range []string{"tmdb", "imdb", "tvdb"} {
		if externalID := identities[provider]; externalID != "" {
			return provider, externalID
		}
	}
	if source, ok := collectionAddonSource(item.Sources); ok {
		digest := sha256.Sum256([]byte(source.AddonID + "\x00" + strings.ToLower(strings.TrimSpace(item.MediaType)) + "\x00" + strings.TrimSpace(item.ID)))
		return "addon", fmt.Sprintf("sha256:%x", digest)
	}
	return "", ""
}

func collectionAddonSource(sources []collection.SourceReference) (collection.SourceReference, bool) {
	for _, source := range sources {
		addonID := strings.ToLower(strings.TrimSpace(source.AddonID))
		catalogID := strings.TrimSpace(source.CatalogID)
		name := strings.TrimSpace(source.Title)
		if source.Kind != collection.SourceKindAddonCatalog || addonID == "" || catalogID == "" || name == "" {
			continue
		}
		if _, err := ParseItemID(addonID); err != nil {
			continue
		}
		source.AddonID, source.CatalogID, source.Title = addonID, catalogID, name
		return source, true
	}
	return collection.SourceReference{}, false
}

func (reader *catalogReader) localizeCatalogTitles(ctx context.Context, titles []watchstate.CatalogTitle) {
	if len(titles) == 0 {
		return
	}
	const titleArtworkCount = 5
	peopleCount := 0
	for index := range titles {
		peopleCount += len(titles[index].People)
	}
	upstream := make([]string, len(titles)*titleArtworkCount+peopleCount)
	peopleOffset := len(titles) * titleArtworkCount
	peopleIndex := peopleOffset
	for index := range titles {
		offset := index * titleArtworkCount
		upstream[offset] = titles[index].PosterURL
		upstream[offset+1] = titles[index].BackgroundURL
		upstream[offset+2] = titles[index].LogoURL
		upstream[offset+3] = titles[index].BannerURL
		upstream[offset+4] = titles[index].ArtURL
		titles[index].PosterURL = ""
		titles[index].BackgroundURL = ""
		titles[index].LogoURL = ""
		titles[index].BannerURL = ""
		titles[index].ArtURL = ""
		for personIndex := range titles[index].People {
			upstream[peopleIndex] = titles[index].People[personIndex].ImageURL
			titles[index].People[personIndex].ImageURL = ""
			peopleIndex++
		}
	}
	if reader.artwork == nil {
		return
	}
	localized := reader.artwork.LocalURLs(ctx, upstream)
	if len(localized) != len(upstream) {
		return
	}
	peopleIndex = peopleOffset
	for index := range titles {
		offset := index * titleArtworkCount
		destinations := [...]*string{
			&titles[index].PosterURL,
			&titles[index].BackgroundURL,
			&titles[index].LogoURL,
			&titles[index].BannerURL,
			&titles[index].ArtURL,
		}
		for artworkIndex, destination := range destinations {
			if _, valid := localizedArtworkTag(localized[offset+artworkIndex]); valid {
				*destination = localized[offset+artworkIndex]
			}
		}
		for personIndex := range titles[index].People {
			if _, valid := localizedArtworkTag(localized[peopleIndex]); valid {
				titles[index].People[personIndex].ImageURL = localized[peopleIndex]
			}
			peopleIndex++
		}
	}
}

func (reader *catalogReader) SearchCatalog(ctx context.Context, principal auth.Principal, query CatalogSearchQuery) (CatalogSearchPage, error) {
	if query.Offset < 0 || query.Limit < 1 || query.Limit > MaximumQueryLimit || query.Offset > MaximumStartIndex || strings.TrimSpace(query.SearchTerm) == "" {
		return CatalogSearchPage{}, fmt.Errorf("%w: invalid catalog search window", watchstate.ErrInvalidInput)
	}
	if query.Offset > maximumCatalogSearchCandidates || query.Limit > maximumCatalogSearchCandidates-query.Offset {
		return CatalogSearchPage{}, fmt.Errorf("%w: catalog search window exceeds %d candidates", watchstate.ErrInvalidInput, maximumCatalogSearchCandidates)
	}
	target := query.Offset + query.Limit
	local, err := reader.ListCatalogItems(ctx, principal, watchstate.CatalogQuery{
		MediaTypes: query.MediaTypes, SearchTerm: query.SearchTerm, IDs: query.IDs,
		Recursive: true, Limit: maximumCatalogSearchCandidates,
	})
	if err != nil {
		return CatalogSearchPage{}, err
	}
	localItems := local.Items
	complete := local.Total <= len(localItems)
	if len(localItems) > maximumCatalogSearchCandidates {
		localItems = localItems[:maximumCatalogSearchCandidates]
		complete = false
	}
	candidates := append([]watchstate.CatalogTitle(nil), localItems...)
	externalTypes := externalSearchTypes(query.MediaTypes)
	windowFilled := func() bool {
		return len(deduplicateCatalogSearch(candidates, query.IDs)) >= target
	}

	if reader.metadata != nil && containsString(externalTypes, "movie") {
		received := 0
		for requestedPage := 1; requestedPage <= maximumCatalogMetadataPagesPerType; requestedPage++ {
			page, searchErr := reader.metadata.SearchLinkedMovies(ctx, principal, metadata.SearchOptions{
				QueryOptions: metadata.QueryOptions{Page: requestedPage}, Query: query.SearchTerm,
			})
			if searchErr != nil {
				if catalogSearchFatal(searchErr) || !metadataSearchPartial(searchErr) {
					return CatalogSearchPage{}, normalizeCatalogSearchError(searchErr)
				}
				complete = false
				break
			}
			remaining := maximumCatalogMetadataItemsPerType - received
			take := len(page.Items)
			if take > remaining {
				take = remaining
			}
			for _, movie := range page.Items[:take] {
				candidates = append(candidates, catalogTitleFromMovie(movie))
			}
			received += take
			if take != len(page.Items) || page.Page != requestedPage || page.TotalPages < requestedPage || page.TotalResults < received {
				complete = false
				break
			}
			if requestedPage == page.TotalPages {
				if page.TotalResults != received {
					complete = false
				}
				break
			}
			if query.SortBy == "" && windowFilled() {
				complete = false
				break
			}
			if received == maximumCatalogMetadataItemsPerType || requestedPage == maximumCatalogMetadataPagesPerType {
				complete = false
				break
			}
		}
	}
	if reader.metadata != nil && containsString(externalTypes, "series") {
		received := 0
		for requestedPage := 1; requestedPage <= maximumCatalogMetadataPagesPerType; requestedPage++ {
			page, searchErr := reader.metadata.SearchLinkedSeries(ctx, principal, metadata.SearchOptions{
				QueryOptions: metadata.QueryOptions{Page: requestedPage}, Query: query.SearchTerm,
			})
			if searchErr != nil {
				if catalogSearchFatal(searchErr) || !metadataSearchPartial(searchErr) {
					return CatalogSearchPage{}, normalizeCatalogSearchError(searchErr)
				}
				complete = false
				break
			}
			remaining := maximumCatalogMetadataItemsPerType - received
			take := len(page.Items)
			if take > remaining {
				take = remaining
			}
			for _, series := range page.Items[:take] {
				candidates = append(candidates, catalogTitleFromSeries(series))
			}
			received += take
			if take != len(page.Items) || page.Page != requestedPage || page.TotalPages < requestedPage || page.TotalResults < received {
				complete = false
				break
			}
			if requestedPage == page.TotalPages {
				if page.TotalResults != received {
					complete = false
				}
				break
			}
			if query.SortBy == "" && windowFilled() {
				complete = false
				break
			}
			if received == maximumCatalogMetadataItemsPerType || requestedPage == maximumCatalogMetadataPagesPerType {
				complete = false
				break
			}
		}
	}

	if reader.addons != nil && len(externalTypes) != 0 {
		page, searchErr := reader.addons.SearchCatalogItems(ctx, principal, externalTypes, query.SearchTerm, maximumCatalogSearchCandidates, reader.artwork)
		if searchErr != nil {
			if catalogSearchFatal(searchErr) || !addonSearchPartial(searchErr) {
				return CatalogSearchPage{}, normalizeCatalogSearchError(searchErr)
			}
			complete = false
		} else {
			complete = complete && page.Complete
			items := page.Items
			if len(items) > maximumCatalogSearchCandidates {
				items = items[:maximumCatalogSearchCandidates]
				complete = false
			}
			for _, item := range items {
				title, resolveErr := reader.resolveAddonSearchTitle(ctx, principal, item)
				if resolveErr != nil {
					if catalogSearchFatal(resolveErr) || !errors.Is(resolveErr, watchstate.ErrInvalidInput) && !errors.Is(resolveErr, watchstate.ErrNotFound) {
						return CatalogSearchPage{}, normalizeCatalogSearchError(resolveErr)
					}
					complete = false
					continue
				}
				candidates = append(candidates, title)
			}
		}
	}

	if query.SortBy != "" && !complete {
		return CatalogSearchPage{}, fmt.Errorf("%w: sorted provider search requires an exact bounded result set", watchstate.ErrInvalidInput)
	}
	items := deduplicateCatalogSearch(candidates, query.IDs)
	if query.SortBy != "" {
		sortCatalogSearch(items, query.SortOrder)
	}
	if len(items) > maximumCatalogSearchCandidates {
		items = items[:maximumCatalogSearchCandidates]
		complete = false
	}
	result := CatalogSearchPage{Offset: query.Offset, Limit: query.Limit, Total: len(items), ExactTotal: complete}
	if query.Offset < len(items) {
		end := query.Offset + query.Limit
		if end > len(items) {
			end = len(items)
		}
		result.Items = append([]watchstate.CatalogTitle(nil), items[query.Offset:end]...)
	} else {
		result.Items = []watchstate.CatalogTitle{}
	}
	reader.localizeCatalogTitles(ctx, result.Items)
	return result, nil
}

func externalSearchTypes(mediaTypes []string) []string {
	if len(mediaTypes) == 0 {
		return []string{"movie", "series"}
	}
	result := make([]string, 0, 2)
	for _, mediaType := range mediaTypes {
		mediaType = strings.ToLower(strings.TrimSpace(mediaType))
		if (mediaType == "movie" || mediaType == "series") && !containsString(result, mediaType) {
			result = append(result, mediaType)
		}
	}
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func catalogSearchFatal(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, watchstate.ErrProfileRequired) || errors.Is(err, watchstate.ErrForbidden) ||
		errors.Is(err, metadata.ErrProfileRequired) || errors.Is(err, addon.ErrActiveProfileRequired) || errors.Is(err, addon.ErrForbidden)
}

func normalizeCatalogSearchError(err error) error {
	switch {
	case errors.Is(err, metadata.ErrProfileRequired), errors.Is(err, addon.ErrActiveProfileRequired):
		return fmt.Errorf("%w: search authorization changed", watchstate.ErrProfileRequired)
	case errors.Is(err, addon.ErrForbidden):
		return fmt.Errorf("%w: search authorization changed", watchstate.ErrForbidden)
	default:
		return err
	}
}

func metadataSearchPartial(err error) bool {
	return errors.Is(err, metadata.ErrProviderUnavailable) || errors.Is(err, metadata.ErrProviderUnauthorized) ||
		errors.Is(err, metadata.ErrProviderNotFound) || errors.Is(err, metadata.ErrProviderRateLimited) ||
		errors.Is(err, metadata.ErrProviderFailure)
}

func addonSearchPartial(err error) bool {
	return errors.Is(err, addon.ErrProviderUnavailable) || errors.Is(err, addon.ErrInvalidResponse)
}

func catalogTitleFromMovie(movie metadata.Movie) watchstate.CatalogTitle {
	runtime := movie.RuntimeMinutes
	genres := make([]string, 0, len(movie.Genres))
	for _, genre := range movie.Genres {
		genres = append(genres, genre.Name)
	}
	var rating *float32
	if movie.VoteAverage > 0 {
		value := float32(movie.VoteAverage)
		rating = &value
	}
	return watchstate.CatalogTitle{
		ID: movie.ID, MediaType: "movie", Title: movie.Title, OriginalTitle: movie.OriginalTitle, Overview: movie.Overview,
		PosterURL: movie.PosterURL, BackgroundURL: movie.BackdropURL, LogoURL: movie.LogoURL,
		BannerURL: movie.BannerURL, ArtURL: movie.ArtURL, Released: movie.ReleaseDate,
		RuntimeMinutes: &runtime, Genres: genres, CommunityRating: rating, Tagline: movie.Tagline,
		People: catalogPeopleFromCast(movie.Cast), ProviderIDs: copyProviderIDs(movie.ExternalIDs),
	}
}

func catalogTitleFromSeries(series metadata.Series) watchstate.CatalogTitle {
	genres := make([]string, 0, len(series.Genres))
	for _, genre := range series.Genres {
		genres = append(genres, genre.Name)
	}
	var rating *float32
	if series.VoteAverage > 0 {
		value := float32(series.VoteAverage)
		rating = &value
	}
	return watchstate.CatalogTitle{
		ID: series.ID, MediaType: "series", Title: series.Name, OriginalTitle: series.OriginalName, Overview: series.Overview,
		PosterURL: series.PosterURL, BackgroundURL: series.BackdropURL, LogoURL: series.LogoURL,
		BannerURL: series.BannerURL, ArtURL: series.ArtURL, Released: series.FirstAirDate,
		Genres: genres, CommunityRating: rating, Tagline: series.Tagline, Status: series.Status, EndDate: series.LastAirDate,
		People: catalogPeopleFromCast(series.Cast), ProviderIDs: copyProviderIDs(series.ExternalIDs),
	}
}

func catalogTitleFromSeason(season metadata.Season) watchstate.CatalogTitle {
	ordinal := season.SeasonNumber
	var rating *float32
	if season.VoteAverage > 0 {
		value := float32(season.VoteAverage)
		rating = &value
	}
	return watchstate.CatalogTitle{
		ID: season.ID, MediaType: "season", SeriesID: season.SeriesID, Ordinal: &ordinal, Title: season.Name,
		Overview: season.Overview, PosterURL: season.PosterURL, BackgroundURL: season.BackdropURL, Released: season.AirDate,
		Genres: []string{}, CommunityRating: rating, ProviderIDs: copyProviderIDs(season.ExternalIDs),
	}
}

func catalogTitleFromEpisode(episode metadata.Episode) watchstate.CatalogTitle {
	ordinal, parentOrdinal, runtime := episode.EpisodeNumber, episode.SeasonNumber, episode.RuntimeMinutes
	var rating *float32
	if episode.VoteAverage > 0 {
		value := float32(episode.VoteAverage)
		rating = &value
	}
	return watchstate.CatalogTitle{
		ID: episode.ID, MediaType: "episode", SeasonID: episode.SeasonID, Ordinal: &ordinal, ParentOrdinal: &parentOrdinal,
		Title: episode.Name, Overview: episode.Overview, PosterURL: episode.StillURL, BackgroundURL: episode.BackdropURL,
		Released: episode.AirDate, RuntimeMinutes: &runtime, Genres: []string{}, CommunityRating: rating,
		ProviderIDs: copyProviderIDs(episode.ExternalIDs),
	}
}

func catalogPeopleFromCast(cast []metadata.CastMember) []watchstate.CatalogPerson {
	people := make([]watchstate.CatalogPerson, 0, len(cast))
	for _, member := range cast {
		name := strings.TrimSpace(member.Name)
		if name == "" {
			continue
		}
		people = append(people, watchstate.CatalogPerson{
			Name: name, Role: strings.TrimSpace(member.Character), Type: "Actor", ImageURL: strings.TrimSpace(member.ProfileURL),
		})
	}
	return people
}

func mergeCatalogTitleMetadata(title, detail watchstate.CatalogTitle) watchstate.CatalogTitle {
	if detail.Title != "" {
		title.Title = detail.Title
	}
	if detail.OriginalTitle != "" {
		title.OriginalTitle = detail.OriginalTitle
	}
	if detail.Overview != "" {
		title.Overview = detail.Overview
	}
	if detail.Released != "" {
		title.Released = detail.Released
	}
	if detail.RuntimeMinutes != nil && *detail.RuntimeMinutes > 0 {
		title.RuntimeMinutes = detail.RuntimeMinutes
	}
	if len(detail.Genres) != 0 {
		title.Genres = append([]string(nil), detail.Genres...)
	}
	if detail.CommunityRating != nil {
		title.CommunityRating = detail.CommunityRating
	}
	if detail.Tagline != "" {
		title.Tagline = detail.Tagline
	}
	if detail.Status != "" {
		title.Status = detail.Status
	}
	if detail.EndDate != "" {
		title.EndDate = detail.EndDate
	}
	if len(detail.People) != 0 {
		title.People = append([]watchstate.CatalogPerson(nil), detail.People...)
	}
	if title.PosterURL == "" {
		title.PosterURL = detail.PosterURL
	}
	if title.BackgroundURL == "" {
		title.BackgroundURL = detail.BackgroundURL
	}
	if title.LogoURL == "" {
		title.LogoURL = detail.LogoURL
	}
	if title.BannerURL == "" {
		title.BannerURL = detail.BannerURL
	}
	if title.ArtURL == "" {
		title.ArtURL = detail.ArtURL
	}
	providerIDs := make(map[string]string, len(title.ProviderIDs)+len(detail.ProviderIDs))
	for provider, value := range title.ProviderIDs {
		providerIDs[provider] = value
	}
	for provider, value := range detail.ProviderIDs {
		providerIDs[provider] = value
	}
	title.ProviderIDs = providerIDs
	return title
}

func copyProviderIDs(values map[string]string) map[string]string {
	result := make(map[string]string, 3)
	for provider, value := range values {
		provider, value, valid := validatedProviderID(provider, value)
		if valid {
			result[provider] = value
		}
	}
	return result
}

func validatedProviderID(provider, value string) (string, string, bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 18 {
		return "", "", false
	}
	switch provider {
	case "imdb":
		if len(value) < 3 || value[0] != 't' || value[1] != 't' || !asciiDigits(value[2:], true) {
			return "", "", false
		}
	case "tmdb", "tvdb":
		if !asciiDigits(value, false) {
			return "", "", false
		}
	default:
		return "", "", false
	}
	return provider, value, true
}

func asciiDigits(value string, allowLeadingZero bool) bool {
	if value == "" || !allowLeadingZero && value[0] == '0' {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func (reader *catalogReader) resolveAddonSearchTitle(ctx context.Context, principal auth.Principal, item addon.CatalogSearchItem) (watchstate.CatalogTitle, error) {
	provider, externalID := addonSearchIdentity(item)
	released := item.Released
	if released != "" {
		if parsed, err := time.Parse(time.DateOnly, released); err != nil || parsed.Format(time.DateOnly) != released {
			released = ""
		}
	}
	localized := watchstate.CatalogTitle{PosterURL: item.PosterURL, BackgroundURL: item.BackgroundURL}
	_, posterIsLocal := localizedArtworkTag(localized.PosterURL)
	_, backgroundIsLocal := localizedArtworkTag(localized.BackgroundURL)
	if localized.PosterURL != "" && !posterIsLocal || localized.BackgroundURL != "" && !backgroundIsLocal {
		titles := []watchstate.CatalogTitle{localized}
		reader.localizeCatalogTitles(ctx, titles)
		localized = titles[0]
	}
	reference, err := reader.store.ResolveLinkedCatalogTitle(ctx, principal, watchstate.ResolveTitleInput{
		MediaType: item.MediaType, Provider: provider, ExternalID: externalID,
		ResourceID: item.ResourceID, Title: item.Title, PosterURL: localized.PosterURL,
		BackgroundURL: localized.BackgroundURL, ReleaseInfo: item.ReleaseInfo, Released: released,
		SourceAddonID: item.AddonID, SourceCatalogID: item.CatalogID, SourceName: item.AddonName,
	})
	if err != nil {
		return watchstate.CatalogTitle{}, err
	}
	return reader.store.GetCatalogTitle(ctx, principal, reference.TitleID)
}

func addonSearchIdentity(item addon.CatalogSearchItem) (string, string) {
	identities := copyProviderIDs(item.ExternalIDs)
	for _, provider := range []string{"tmdb", "imdb", "tvdb"} {
		if externalID := identities[provider]; externalID != "" {
			return provider, externalID
		}
	}
	digest := sha256.Sum256([]byte(item.AddonID + "\x00" + item.MediaType + "\x00" + item.ResourceID))
	return "addon", fmt.Sprintf("sha256:%x", digest)
}

func deduplicateCatalogSearch(input []watchstate.CatalogTitle, allowedIDs []string) []watchstate.CatalogTitle {
	allowed := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[strings.ToLower(id)] = struct{}{}
	}
	items := make([]watchstate.CatalogTitle, 0, len(input))
	indexes := make(map[string]int, len(input))
	for _, item := range input {
		id := strings.ToLower(strings.TrimSpace(item.ID))
		if id == "" || len(allowed) != 0 && !containsKey(allowed, id) {
			continue
		}
		if index, duplicate := indexes[id]; duplicate {
			items[index] = mergeCatalogSearchTitle(items[index], item)
			continue
		}
		indexes[id] = len(items)
		items = append(items, item)
	}
	return items
}

func containsKey(values map[string]struct{}, key string) bool {
	_, ok := values[key]
	return ok
}

func mergeCatalogSearchTitle(current, incoming watchstate.CatalogTitle) watchstate.CatalogTitle {
	if current.Title == "" {
		current.Title = incoming.Title
	}
	if current.Overview == "" {
		current.Overview = incoming.Overview
	}
	if current.PosterURL == "" {
		current.PosterURL = incoming.PosterURL
	}
	if current.BackgroundURL == "" {
		current.BackgroundURL = incoming.BackgroundURL
	}
	if current.LogoURL == "" {
		current.LogoURL = incoming.LogoURL
	}
	if current.BannerURL == "" {
		current.BannerURL = incoming.BannerURL
	}
	if current.ArtURL == "" {
		current.ArtURL = incoming.ArtURL
	}
	if current.Released == "" {
		current.Released = incoming.Released
	}
	if current.ReleaseInfo == "" {
		current.ReleaseInfo = incoming.ReleaseInfo
	}
	if current.RuntimeMinutes == nil {
		current.RuntimeMinutes = incoming.RuntimeMinutes
	}
	if current.CommunityRating == nil {
		current.CommunityRating = incoming.CommunityRating
	}
	if len(current.Genres) == 0 && len(incoming.Genres) != 0 {
		current.Genres = append([]string(nil), incoming.Genres...)
	}
	if current.ProviderIDs == nil {
		current.ProviderIDs = map[string]string{}
	}
	for provider, id := range incoming.ProviderIDs {
		if current.ProviderIDs[provider] == "" {
			current.ProviderIDs[provider] = id
		}
	}
	return current
}

func sortCatalogSearch(items []watchstate.CatalogTitle, order string) {
	descending := strings.EqualFold(order, "Descending")
	sort.SliceStable(items, func(left, right int) bool {
		leftName := strings.ToLower(items[left].Title)
		rightName := strings.ToLower(items[right].Title)
		if leftName == rightName {
			return strings.ToLower(items[left].ID) < strings.ToLower(items[right].ID)
		}
		if descending {
			return leftName > rightName
		}
		return leftName < rightName
	})
}

var (
	_ CatalogReader          = (*catalogReader)(nil)
	_ catalogBatchReader     = (*catalogReader)(nil)
	_ catalogSearcher        = (*catalogReader)(nil)
	_ collectionItemResolver = (*catalogReader)(nil)
)
