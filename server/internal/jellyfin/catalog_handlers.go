package jellyfin

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/watchstate"
)

const localizedArtworkPrefix = "/api/v1/artwork/"

type virtualFolderInfo struct {
	Name           string   `json:"Name"`
	Locations      []string `json:"Locations"`
	CollectionType string   `json:"CollectionType"`
	ItemId         string   `json:"ItemId"`
	Id             string   `json:"Id"`
}

func (handler *Handler) handleUserViews(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok || !handler.requireBoundUser(response, request.PathValue("id"), session) {
		return
	}
	handler.writeViews(response)
}

func (handler *Handler) handleViews(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok || !handler.requireOptionalQueryUser(response, request, session) {
		return
	}
	handler.writeViews(response)
}

func (handler *Handler) handleVirtualFolders(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok || !handler.requireOptionalQueryUser(response, request, session) {
		return
	}
	views, valid := handler.virtualViews()
	if !valid {
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
		return
	}
	folders := make([]virtualFolderInfo, 0, len(views))
	for _, view := range views {
		folders = append(folders, virtualFolderInfo{
			Name: view.Name, Locations: make([]string, 0), CollectionType: view.CollectionType,
			ItemId: view.Id, Id: view.Id,
		})
	}
	handler.writeJSON(response, http.StatusOK, folders)
}

func (handler *Handler) handleSelectableMediaFolders(response http.ResponseWriter, request *http.Request) {
	handler.handleVirtualFolders(response, request)
}

func (handler *Handler) handleItems(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok || !handler.requireOptionalQueryUser(response, request, session) {
		return
	}
	handler.writeItems(response, request, session, nil)
}

func (handler *Handler) handleUserItems(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok || !handler.requireBoundUser(response, request.PathValue("id"), session) {
		return
	}
	handler.writeItems(response, request, session, nil)
}

func (handler *Handler) handleItem(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok || !handler.requireOptionalQueryUser(response, request, session) {
		return
	}
	handler.writeItem(response, request, session, request.PathValue("id"))
}

func (handler *Handler) handleUserItem(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok || !handler.requireBoundUser(response, request.PathValue("userId"), session) {
		return
	}
	handler.writeItem(response, request, session, request.PathValue("itemId"))
}

func (handler *Handler) handleSeasons(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok || !handler.requireOptionalQueryUser(response, request, session) {
		return
	}
	types := []string{"season"}
	handler.writeItems(response, request, session, &catalogHierarchy{parentID: request.PathValue("seriesId"), mediaTypes: types})
}

func (handler *Handler) handleEpisodes(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok || !handler.requireOptionalQueryUser(response, request, session) {
		return
	}
	seriesID, err := ParseItemID(request.PathValue("seriesId"))
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid series id")
		return
	}
	seasonID, err := boundedString(request.URL.Query(), "SeasonId", MaximumQueryValueBytes)
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid SeasonId")
		return
	}
	hierarchy := &catalogHierarchy{parentID: seriesID.String(), mediaTypes: []string{"episode"}, recursive: true}
	if seasonID != "" {
		parsedSeason, parseErr := ParseItemID(seasonID)
		if parseErr != nil {
			handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid SeasonId")
			return
		}
		season, readErr := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, parsedSeason.String())
		if readErr != nil {
			handler.writeCatalogError(response, readErr)
			return
		}
		if season.MediaType != "season" || !strings.EqualFold(season.SeriesID, seriesID.String()) {
			handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
			return
		}
		hierarchy.parentID = parsedSeason.String()
		hierarchy.recursive = false
	}
	handler.writeItems(response, request, session, hierarchy)
}

func (handler *Handler) handleSearchHints(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok || !handler.requireOptionalQueryUser(response, request, session) {
		return
	}
	handler.writeSearchHints(response, request, session)
}

func (handler *Handler) handleUserSearchHints(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok || !handler.requireBoundUser(response, request.PathValue("id"), session) {
		return
	}
	handler.writeSearchHints(response, request, session)
}

type catalogHierarchy struct {
	parentID   string
	mediaTypes []string
	recursive  bool
}

func (handler *Handler) catalogSession(response http.ResponseWriter, request *http.Request) (AuthenticatedSession, bool) {
	if handler == nil || handler.catalog == nil || handler.authentication == nil {
		if handler != nil {
			handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
		} else {
			http.NotFound(response, request)
		}
		return AuthenticatedSession{}, false
	}
	return handler.authenticateRequest(response, request, false)
}

func (handler *Handler) requireBoundUser(response http.ResponseWriter, supplied string, session AuthenticatedSession) bool {
	value, err := parseUUID(supplied)
	if err != nil || !strings.EqualFold(formatUUID(value), session.ProfileID) {
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
		return false
	}
	return true
}

func (handler *Handler) requireOptionalQueryUser(response http.ResponseWriter, request *http.Request, session AuthenticatedSession) bool {
	value, found, err := queryScalar(request.URL.Query(), "UserId")
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return false
	}
	if !found || value == "" {
		return true
	}
	return handler.requireBoundUser(response, value, session)
}

func (handler *Handler) writeViews(response http.ResponseWriter) {
	views, ok := handler.virtualViews()
	if !ok {
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
		return
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: views, TotalRecordCount: len(views), StartIndex: 0})
}

func (handler *Handler) virtualViews() ([]BaseItemDto, bool) {
	moviesID, moviesErr := DeriveVirtualItemID(handler.serverInfo.ID, VirtualMoviesView)
	tvID, tvErr := DeriveVirtualItemID(handler.serverInfo.ID, VirtualTVShowsView)
	if moviesErr != nil || tvErr != nil {
		return nil, false
	}
	serverID := handler.serverInfo.ID.String()
	return []BaseItemDto{
		{Id: moviesID.String(), ServerId: serverID, Name: "Movies", SortName: "Movies", Type: "CollectionFolder", CollectionType: "movies", IsFolder: true, Genres: []string{}, BackdropImageTags: []string{}},
		{Id: tvID.String(), ServerId: serverID, Name: "TV Shows", SortName: "TV Shows", Type: "CollectionFolder", CollectionType: "tvshows", IsFolder: true, Genres: []string{}, BackdropImageTags: []string{}},
	}, true
}

func (handler *Handler) writeItem(response http.ResponseWriter, request *http.Request, session AuthenticatedSession, rawID string) {
	query, err := ParseItemQuery(request.URL.Query())
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	itemID, err := ParseItemID(rawID)
	if err != nil {
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
		return
	}
	title, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, itemID.String())
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	handler.writeJSON(response, http.StatusOK, handler.baseItemDTO(title, query.EnableUserData))
}

func (handler *Handler) writeItems(response http.ResponseWriter, request *http.Request, session AuthenticatedSession, fixed *catalogHierarchy) {
	parsed, err := ParseItemQuery(request.URL.Query())
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	mediaTypes, err := catalogMediaTypes(parsed.IncludeItemTypes)
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid IncludeItemTypes")
		return
	}
	parentID := parsed.ParentId
	recursive := parsed.Recursive
	if fixed != nil {
		parentID, mediaTypes, recursive = fixed.parentID, fixed.mediaTypes, fixed.recursive
	}
	if parentID != "" {
		if fixed != nil {
			itemID, parseErr := ParseItemID(parentID)
			if parseErr != nil {
				handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid ParentId")
				return
			}
			parentID = itemID.String()
		} else {
			parentID, mediaTypes, recursive, err = handler.translateVirtualParent(parentID, mediaTypes, recursive)
			if err != nil {
				handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid ParentId")
				return
			}
		}
	}
	if noCatalogMediaTypes(mediaTypes) {
		handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: []BaseItemDto{}, TotalRecordCount: 0, StartIndex: parsed.StartIndex})
		return
	}
	sortBy, sortOrder, err := catalogSort(parsed, request)
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid sort")
		return
	}
	page, err := handler.catalog.ListCatalogItems(request.Context(), session.Principal, watchstate.CatalogQuery{
		ParentID: parentID, MediaTypes: mediaTypes, SearchTerm: parsed.SearchTerm, IDs: parsed.Ids,
		Recursive: recursive, Offset: parsed.StartIndex, Limit: parsed.Limit,
		SortBy: sortBy, SortOrder: sortOrder,
	})
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	items := make([]BaseItemDto, 0, len(page.Items))
	for _, title := range page.Items {
		items = append(items, handler.baseItemDTO(title, parsed.EnableUserData))
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: items, TotalRecordCount: page.Total, StartIndex: page.Offset})
}

func (handler *Handler) writeSearchHints(response http.ResponseWriter, request *http.Request, session AuthenticatedSession) {
	parsed, err := ParseItemQuery(request.URL.Query())
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	if parsed.SearchTerm == "" {
		handler.writeJSON(response, http.StatusOK, SearchHintResult{SearchHints: []SearchHintDto{}, TotalRecordCount: 0})
		return
	}
	mediaTypes, err := catalogMediaTypes(parsed.IncludeItemTypes)
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid IncludeItemTypes")
		return
	}
	parentID := parsed.ParentId
	if parentID != "" {
		parentID, mediaTypes, _, err = handler.translateVirtualParent(parentID, mediaTypes, true)
		if err != nil {
			handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid ParentId")
			return
		}
	}
	if noCatalogMediaTypes(mediaTypes) {
		handler.writeJSON(response, http.StatusOK, SearchHintResult{SearchHints: []SearchHintDto{}, TotalRecordCount: 0})
		return
	}
	sortBy, sortOrder, err := catalogSort(parsed, request)
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid sort")
		return
	}
	var page CatalogSearchPage
	searcher, hasSearcher := handler.catalog.(catalogSearcher)
	if parentID != "" || !hasSearcher {
		local, readErr := handler.catalog.ListCatalogItems(request.Context(), session.Principal, watchstate.CatalogQuery{
			ParentID: parentID, MediaTypes: mediaTypes, SearchTerm: parsed.SearchTerm, IDs: parsed.Ids,
			Recursive: true, Offset: parsed.StartIndex, Limit: parsed.Limit,
			SortBy: sortBy, SortOrder: sortOrder,
		})
		if readErr != nil {
			handler.writeCatalogError(response, readErr)
			return
		}
		page = CatalogSearchPage{Items: local.Items, Offset: local.Offset, Limit: local.Limit, Total: local.Total, ExactTotal: true}
	} else {
		page, err = searcher.SearchCatalog(request.Context(), session.Principal, CatalogSearchQuery{
			SearchTerm: parsed.SearchTerm, MediaTypes: mediaTypes, IDs: parsed.Ids,
			Offset: parsed.StartIndex, Limit: parsed.Limit, SortBy: sortBy, SortOrder: sortOrder,
		})
		if err != nil {
			handler.writeCatalogError(response, err)
			return
		}
	}
	hints := make([]SearchHintDto, 0, len(page.Items))
	for _, title := range page.Items {
		item := handler.baseItemDTO(title, false)
		hint := SearchHintDto{
			ItemId: item.Id, Id: item.Id, Name: item.Name, Type: item.Type, MediaType: item.MediaType,
			ProductionYear: item.ProductionYear, RunTimeTicks: item.RunTimeTicks, ProviderIds: item.ProviderIds,
		}
		if item.ImageTags != nil {
			hint.PrimaryImageTag = item.ImageTags["Primary"]
		}
		if len(item.BackdropImageTags) != 0 {
			hint.BackdropImageTag = item.BackdropImageTags[0]
		}
		hints = append(hints, hint)
	}
	total := 0
	if page.ExactTotal {
		total = page.Total
	}
	handler.writeJSON(response, http.StatusOK, SearchHintResult{SearchHints: hints, TotalRecordCount: total})
}

func (handler *Handler) translateVirtualParent(parentID string, mediaTypes []string, recursive bool) (string, []string, bool, error) {
	views, ok := handler.virtualViews()
	if !ok {
		return "", nil, false, ErrInvalidID
	}
	if strings.EqualFold(parentID, views[0].Id) {
		return "", intersectMediaTypes(mediaTypes, []string{"movie"}), recursive, nil
	}
	if strings.EqualFold(parentID, views[1].Id) {
		allowed := []string{"series"}
		if recursive {
			allowed = []string{"series", "season", "episode"}
		}
		return "", intersectMediaTypes(mediaTypes, allowed), recursive, nil
	}
	itemID, err := ParseItemID(parentID)
	if err != nil {
		return "", nil, false, err
	}
	return itemID.String(), mediaTypes, recursive, nil
}

func catalogMediaTypes(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		var mediaType string
		switch {
		case strings.EqualFold(value, "Movie"):
			mediaType = "movie"
		case strings.EqualFold(value, "Series"):
			mediaType = "series"
		case strings.EqualFold(value, "Season"):
			mediaType = "season"
		case strings.EqualFold(value, "Episode"):
			mediaType = "episode"
		default:
			return nil, ErrInvalidQuery
		}
		if _, duplicate := seen[mediaType]; !duplicate {
			seen[mediaType] = struct{}{}
			result = append(result, mediaType)
		}
	}
	sort.Strings(result)
	return result, nil
}

func intersectMediaTypes(requested, allowed []string) []string {
	if len(requested) == 0 {
		return append([]string(nil), allowed...)
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = struct{}{}
	}
	result := make([]string, 0, len(requested))
	for _, value := range requested {
		if _, ok := allowedSet[value]; ok {
			result = append(result, value)
		}
	}
	if len(result) == 0 {
		// A valid but disjoint Jellyfin filter has an empty result, represented by
		// an impossible canonical type rather than widening the query.
		return []string{"__none__"}
	}
	return result
}

func noCatalogMediaTypes(values []string) bool {
	return len(values) == 1 && values[0] == "__none__"
}

func catalogSort(query ItemQuery, request *http.Request) (string, string, error) {
	if len(query.SortBy) == 0 {
		if _, supplied, err := queryScalar(request.URL.Query(), "SortOrder"); err != nil || supplied {
			return "", "", ErrInvalidQuery
		}
		return "", "", nil
	}
	if len(query.SortBy) != 1 || !strings.EqualFold(query.SortBy[0], "SortName") {
		return "", "", ErrInvalidQuery
	}
	return "sortname", strings.ToLower(query.SortOrder), nil
}

func (handler *Handler) baseItemDTO(title watchstate.CatalogTitle, includeUserData bool) BaseItemDto {
	item := BaseItemDto{
		Id: title.ID, ServerId: handler.serverInfo.ID.String(), Name: title.Title, SortName: title.Title,
		ParentId: title.ParentID, SeriesId: title.SeriesID, SeasonId: title.SeasonID,
		SeriesName: title.SeriesTitle, SeasonName: title.SeasonTitle,
		IndexNumber: title.Ordinal, ParentIndexNumber: title.ParentOrdinal, Overview: title.Overview,
		Genres: append([]string(nil), title.Genres...), CommunityRating: title.CommunityRating,
		ProviderIds: jellyfinProviderIDs(title.ProviderIDs), ImageTags: make(map[string]string),
		BackdropImageTags: make([]string, 0),
	}
	if item.Genres == nil {
		item.Genres = make([]string, 0)
	}
	switch title.MediaType {
	case "movie":
		item.Type, item.MediaType, item.IsPlayable = "Movie", "Video", true
	case "series":
		item.Type, item.IsFolder = "Series", true
	case "season":
		item.Type, item.IsFolder = "Season", true
	case "episode":
		item.Type, item.MediaType, item.IsPlayable = "Episode", "Video", true
	}
	if released, err := time.Parse(time.DateOnly, title.Released); err == nil {
		premiere := time.Date(released.Year(), released.Month(), released.Day(), 0, 0, 0, 0, time.UTC)
		item.PremiereDate = premiere.Format(time.RFC3339)
		year := released.Year()
		item.ProductionYear = &year
	} else if len(title.ReleaseInfo) >= 4 {
		if year, parseErr := strconv.Atoi(title.ReleaseInfo[:4]); parseErr == nil && year > 0 && year <= 9999 {
			item.ProductionYear = &year
		}
	}
	if title.RuntimeMinutes != nil && *title.RuntimeMinutes > 0 {
		ticks := MinutesToTicks(int64(*title.RuntimeMinutes))
		item.RunTimeTicks = &ticks
	} else if title.Progress != nil && title.Progress.DurationSeconds > 0 {
		ticks := SecondsToTicks(int64(title.Progress.DurationSeconds))
		item.RunTimeTicks = &ticks
	}
	if tag, ok := localizedArtworkTag(title.PosterURL); ok {
		item.ImageTags["Primary"] = tag
	}
	if tag, ok := localizedArtworkTag(title.BackgroundURL); ok {
		item.BackdropImageTags = append(item.BackdropImageTags, tag)
	}
	if includeUserData {
		userData := &UserItemDataDto{IsFavorite: title.InLibrary, Key: title.ID}
		if title.Progress != nil {
			userData.PlaybackPositionTicks = SecondsToTicks(int64(title.Progress.PositionSeconds))
			userData.Played = title.Progress.Completed
			if title.Progress.Completed {
				userData.PlayCount = 1
			}
			if title.Progress.LastWatchedAt != nil && !title.Progress.LastWatchedAt.IsZero() {
				userData.LastPlayedDate = title.Progress.LastWatchedAt.UTC().Format(time.RFC3339Nano)
			}
		}
		item.UserData = userData
	}
	return item
}

func jellyfinProviderIDs(values map[string]string) map[string]string {
	result := make(map[string]string, 3)
	for rawProvider, rawValue := range values {
		provider, value, valid := validatedProviderID(rawProvider, rawValue)
		if !valid {
			continue
		}
		switch provider {
		case "imdb":
			result["Imdb"] = value
		case "tmdb":
			result["Tmdb"] = value
		case "tvdb":
			result["Tvdb"] = value
		}
	}
	return result
}

func localizedArtworkTag(value string) (string, bool) {
	if !strings.HasPrefix(value, localizedArtworkPrefix) {
		return "", false
	}
	key := strings.TrimPrefix(value, localizedArtworkPrefix)
	if len(key) != 64 || strings.ContainsRune(key, '/') {
		return "", false
	}
	for _, character := range key {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return "", false
			}
		}
	}
	return key, true
}

func (handler *Handler) writeCatalogError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, watchstate.ErrNotFound):
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
	case errors.Is(err, watchstate.ErrInvalidInput):
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
	case errors.Is(err, watchstate.ErrForbidden), errors.Is(err, watchstate.ErrProfileRequired):
		handler.writeCompatError(response, http.StatusUnauthorized, "Unauthorized", "A valid compatibility token is required")
	default:
		handler.writeCompatError(response, http.StatusInternalServerError, "InternalServerError", "Internal server error")
	}
}
