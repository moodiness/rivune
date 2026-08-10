package jellyfin

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/collection"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

const (
	maximumCollectionResolvePage        = 1000
	maximumCollectionResolveLimit       = 200
	maximumCollectionWindowLimit        = MaximumLatestQueryLimit + 1
	maximumCollectionResolveConcurrency = 8
	maximumCollectionCoverConcurrency   = 4
	compatCatalogRejectedMessage        = "jellyfin compatibility catalog query rejected"
	compatCatalogFailedMessage          = "jellyfin compatibility catalog request failed"
)

const localizedArtworkPrefix = "/api/v1/artwork/"

var errCollectionResolverUnavailable = errors.New("collection resolver unavailable")

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
	handler.writeViews(response, request, session)
}

func (handler *Handler) handleViews(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok || !handler.requireOptionalQueryUser(response, request, session) {
		return
	}
	handler.writeViews(response, request, session)
}

func (handler *Handler) handleVirtualFolders(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok || !handler.requireOptionalQueryUser(response, request, session) {
		return
	}
	views, err := handler.sessionViews(request.Context(), session.Principal, false)
	if err != nil {
		handler.writeCollectionError(response, err)
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

func (handler *Handler) handleLatestItems(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok || !handler.requireOptionalQueryUser(response, request, session) {
		return
	}
	handler.writeLatestItems(response, request, session)
}

func (handler *Handler) handleUserLatestItems(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok || !handler.requireBoundUser(response, request.PathValue("id"), session) {
		return
	}
	handler.writeLatestItems(response, request, session)
}

func (handler *Handler) writeLatestItems(response http.ResponseWriter, request *http.Request, session AuthenticatedSession) {
	query, err := ParseItemQuery(request.URL.Query())
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	mediaTypes, err := catalogMediaTypes(query.IncludeItemTypes)
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid IncludeItemTypes")
		return
	}
	mediaTypes, err = applyItemMediaFilters(mediaTypes, query)
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	sortBy, sortOrder, err := catalogSort(query, request)
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid sort")
		return
	}
	parentID := query.ParentId
	if parentID != "" && handler.collections != nil {
		value, collectionErr := handler.resolveCollectionView(request.Context(), session.Principal, parentID)
		if collectionErr == nil {
			items, _ := handler.collectionFolderPage(request.Context(), session.Principal, value, latestItemQuery(query))
			handler.writeJSON(response, http.StatusOK, items)
			return
		}
		if !errors.Is(collectionErr, collection.ErrNotFound) {
			handler.writeCollectionError(response, collectionErr)
			return
		}
		value, folder, folderErr := handler.findCollectionFolder(request.Context(), session.Principal, parentID)
		if folderErr == nil {
			value.Folders = []collection.Folder{folder}
			page, itemErr := handler.collectionItemPage(request.Context(), session.Principal, latestItemQuery(query), mediaTypes, value, sortBy, sortOrder)
			if itemErr != nil {
				handler.writeCollectionItemError(response, itemErr)
				return
			}
			handler.writeJSON(response, http.StatusOK, page.Items)
			return
		}
		if !errors.Is(folderErr, collection.ErrNotFound) {
			handler.writeCollectionItemError(response, folderErr)
			return
		}
	}
	if parentID != "" {
		if projected, ok := handler.virtualCollectionPage(request.Context(), session.Principal, latestItemQuery(query), parentID, mediaTypes, sortBy, sortOrder); ok {
			handler.writeJSON(response, http.StatusOK, projected.Items)
			return
		}
		parentID, mediaTypes, _, err = handler.translateVirtualParent(parentID, mediaTypes, false)
		if err != nil {
			handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid ParentId")
			return
		}
	}
	mediaTypes = projectedCatalogMediaTypes(mediaTypes)
	if query.Limit == 0 || noCatalogMediaTypes(mediaTypes) || containsString(mediaTypes, "boxset") {
		handler.writeJSON(response, http.StatusOK, []BaseItemDto{})
		return
	}
	catalogQuery := catalogQueryFilters(query)
	catalogQuery.ParentID, catalogQuery.MediaTypes = parentID, mediaTypes
	catalogQuery.SearchTerm, catalogQuery.IDs = query.SearchTerm, query.Ids
	catalogQuery.Recursive, catalogQuery.Offset, catalogQuery.Limit = query.Recursive, query.StartIndex, query.Limit
	catalogQuery.SortBy, catalogQuery.SortOrder = sortBy, sortOrder
	if err := handler.resolveCatalogFacetFilters(request.Context(), session.Principal, parentID, mediaTypes, &catalogQuery); err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	titles, err := handler.listLatestCatalogItems(request.Context(), session.Principal, catalogQuery, query.RequestedLimit)
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	items := make([]BaseItemDto, 0, len(titles))
	for _, title := range titles {
		item := handler.baseItemDTO(title, query.EnableUserData)
		applyItemQueryProjection(&item, query, false)
		items = append(items, item)
	}
	handler.writeJSON(response, http.StatusOK, items)
}

func latestItemQuery(query ItemQuery) ItemQuery {
	query.Limit = query.RequestedLimit
	return query
}

func (handler *Handler) listLatestCatalogItems(ctx context.Context, principal auth.Principal, query watchstate.CatalogQuery, requested int) ([]watchstate.CatalogTitle, error) {
	requested = min(max(requested, 0), MaximumLatestQueryLimit)
	items := make([]watchstate.CatalogTitle, 0, requested)
	initialOffset := query.Offset
	for len(items) < requested {
		query.Offset = initialOffset + len(items)
		query.Limit = min(MaximumQueryLimit, requested-len(items))
		page, err := handler.catalog.ListCatalogItems(ctx, principal, query)
		if err != nil {
			return nil, err
		}
		available := min(len(page.Items), requested-len(items))
		items = append(items, page.Items[:available]...)
		if len(page.Items) < query.Limit {
			break
		}
	}
	return items, nil
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
	if err := validateQueryBudget(request.URL.Query()); err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	seriesID, err := ParseItemID(request.PathValue("seriesId"))
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid series id")
		return
	}
	series, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, seriesID.String())
	if err != nil {
		if request.Context().Err() != nil {
			return
		}
		handler.writeCatalogError(response, err)
		return
	}
	if series.MediaType != "series" || !strings.EqualFold(series.ID, seriesID.String()) {
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
		return
	}
	if detailReader, supported := handler.catalog.(catalogDetailReader); supported {
		if _, detailErr := detailReader.EnrichCatalogTitle(request.Context(), session.Principal, series); detailErr != nil && request.Context().Err() != nil {
			return
		}
	}
	types := []string{"season"}
	handler.writeItems(response, request, session, &catalogHierarchy{parentID: seriesID.String(), mediaTypes: types})
}

func (handler *Handler) handleEpisodes(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok || !handler.requireOptionalQueryUser(response, request, session) {
		return
	}
	if err := validateQueryBudget(request.URL.Query()); err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	seriesID, err := ParseItemID(request.PathValue("seriesId"))
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid series id")
		return
	}
	series, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, seriesID.String())
	if err != nil {
		if request.Context().Err() != nil {
			return
		}
		handler.writeCatalogError(response, err)
		return
	}
	if series.MediaType != "series" || !strings.EqualFold(series.ID, seriesID.String()) {
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
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
			if request.Context().Err() != nil {
				return
			}
			handler.writeCatalogError(response, readErr)
			return
		}
		if season.MediaType != "season" || !strings.EqualFold(season.ID, parsedSeason.String()) || !strings.EqualFold(season.SeriesID, seriesID.String()) {
			handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
			return
		}
		if detailReader, supported := handler.catalog.(catalogDetailReader); supported {
			if _, detailErr := detailReader.EnrichCatalogTitle(request.Context(), session.Principal, season); detailErr != nil && request.Context().Err() != nil {
				return
			}
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
	if !found {
		return true
	}
	return handler.requireBoundUser(response, value, session)
}

func (handler *Handler) writeViews(response http.ResponseWriter, request *http.Request, session AuthenticatedSession) {
	views, err := handler.sessionViews(request.Context(), session.Principal, true)
	if err != nil {
		handler.writeCollectionError(response, err)
		return
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: views, TotalRecordCount: len(views), StartIndex: 0})
}

func (handler *Handler) sessionViews(ctx context.Context, principal auth.Principal, homeCollectionTypes bool) ([]BaseItemDto, error) {
	views, ok := handler.virtualViews()
	if !ok {
		return nil, collection.ErrNotFound
	}
	fallback := append([]BaseItemDto(nil), views[:2]...)
	if handler.collections == nil {
		return fallback, nil
	}
	values, err := handler.collections.List(ctx, principal)
	if err != nil {
		handler.logCompatCatalogEvent(compatCatalogFailedMessage, "collection_views:"+compatCatalogErrorClass(err))
		return fallback, nil
	}
	configured := make([]BaseItemDto, 0, len(values))
	for _, value := range values {
		view, viewErr := handler.collectionViewDTO(ctx, principal, value)
		if viewErr == nil {
			if homeCollectionTypes {
				view.CollectionType = collectionHomeViewType(value)
			}
			configured = append(configured, view)
		}
	}
	if len(configured) == 0 {
		return fallback, nil
	}
	return configured, nil
}

func (handler *Handler) virtualViews() ([]BaseItemDto, bool) {
	moviesID, moviesErr := DeriveVirtualItemID(handler.serverInfo.ID, VirtualMoviesView)
	tvID, tvErr := DeriveVirtualItemID(handler.serverInfo.ID, VirtualTVShowsView)
	collectionsID, collectionsErr := DeriveVirtualItemID(handler.serverInfo.ID, VirtualCollectionsView)
	if moviesErr != nil || tvErr != nil || collectionsErr != nil {
		return nil, false
	}
	serverID := handler.serverInfo.ID.String()
	return []BaseItemDto{
		{Id: moviesID.String(), ServerId: serverID, Name: "Movies", SortName: "Movies", Etag: moviesID.String(), DisplayPreferencesId: moviesID.String(), LocationType: "FileSystem", Type: "CollectionFolder", MediaType: "Unknown", CollectionType: "movies", IsFolder: true, Genres: []string{}, ImageTags: map[string]string{}, BackdropImageTags: []string{}, UserData: &UserItemDataDto{Key: moviesID.String(), ItemId: moviesID.String()}, includeEmptyGenres: true},
		{Id: tvID.String(), ServerId: serverID, Name: "TV Shows", SortName: "TV Shows", Etag: tvID.String(), DisplayPreferencesId: tvID.String(), LocationType: "FileSystem", Type: "CollectionFolder", MediaType: "Unknown", CollectionType: "tvshows", IsFolder: true, Genres: []string{}, ImageTags: map[string]string{}, BackdropImageTags: []string{}, UserData: &UserItemDataDto{Key: tvID.String(), ItemId: tvID.String()}, includeEmptyGenres: true},
		{Id: collectionsID.String(), ServerId: serverID, Name: "Collections", SortName: "Collections", Etag: collectionsID.String(), DisplayPreferencesId: collectionsID.String(), LocationType: "FileSystem", Type: "CollectionFolder", MediaType: "Unknown", CollectionType: "boxsets", IsFolder: true, Genres: []string{}, ImageTags: map[string]string{}, BackdropImageTags: []string{}, UserData: &UserItemDataDto{Key: collectionsID.String(), ItemId: collectionsID.String()}, includeEmptyGenres: true},
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
	if views, ok := handler.virtualViews(); ok {
		for _, view := range views {
			if strings.EqualFold(itemID.String(), view.Id) {
				item := view
				applyItemQueryProjection(&item, query, true)
				handler.writeJSON(response, http.StatusOK, item)
				return
			}
		}
	}
	if handler.collections != nil {
		value, collectionErr := handler.resolveCollectionView(request.Context(), session.Principal, itemID.String())
		if collectionErr == nil {
			item, viewErr := handler.collectionViewDTO(request.Context(), session.Principal, value)
			if viewErr != nil {
				handler.writeCollectionError(response, collection.ErrNotFound)
				return
			}
			applyItemQueryProjection(&item, query, true)
			handler.writeJSON(response, http.StatusOK, item)
			return
		}
		if !errors.Is(collectionErr, collection.ErrNotFound) {
			handler.writeCollectionError(response, collectionErr)
			return
		}
		value, folder, folderErr := handler.findCollectionFolder(request.Context(), session.Principal, itemID.String())
		if folderErr == nil {
			item := handler.collectionFolderDetailDTO(request.Context(), session.Principal, value, folder)
			applyItemQueryProjection(&item, query, true)
			handler.writeJSON(response, http.StatusOK, item)
			return
		}
		if !errors.Is(folderErr, collection.ErrNotFound) {
			handler.writeCollectionError(response, folderErr)
			return
		}
	}
	title, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, itemID.String())
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	item, ok := handler.detailedCatalogItem(request.Context(), session, title, query.EnableUserData,
		itemQueryRequestsMediaSources(query, true), itemQueryRequestsMediaSources(query, false),
		itemQueryRequestsField(query, "Trickplay", true), itemQueryRequestsMetadata(query, true))
	if !ok {
		return
	}
	applyItemQueryProjection(&item, query, true)
	handler.writeJSON(response, http.StatusOK, item)
}

func (handler *Handler) detailedCatalogItem(ctx context.Context, session AuthenticatedSession, title watchstate.CatalogTitle, includeUserData, includeMediaSources, discoverMediaSources, includeTrickplay, enrichMetadata bool) (BaseItemDto, bool) {
	if enrichMetadata {
		if detailReader, ok := handler.catalog.(catalogDetailReader); ok {
			enriched, detailErr := detailReader.EnrichCatalogTitle(ctx, session.Principal, title)
			if detailErr == nil {
				title = enriched
			} else if ctx.Err() != nil {
				return BaseItemDto{}, false
			}
		}
	}
	item := handler.baseItemDTO(title, includeUserData)
	if title.MediaType == "movie" || title.MediaType == "episode" || title.MediaType == "video" {
		hasCanonicalSource := len(item.MediaSources) != 0 &&
			(strings.TrimSpace(title.ResourceID) != "" || preferredPlaybackResource(title.ProviderIDs) != "")
		var cachedSources []MediaSourceInfo
		if includeMediaSources {
			cachedSources = handler.cachedDetailMediaSources(session, title)
			hasCanonicalSource = hasCanonicalSource || len(cachedSources) != 0
		}
		if (includeTrickplay || discoverMediaSources) && !hasCanonicalSource && title.MediaType == "episode" {
			resourceID, addonID := handler.episodePlaybackIdentity(ctx, session, title)
			if resourceID != "" {
				title.ResourceID, title.SourceAddonID = resourceID, addonID
				hasCanonicalSource = true
			}
		}
		if includeMediaSources {
			if len(cachedSources) != 0 {
				item.MediaSources = cachedSources
			} else if discoverMediaSources && hasCanonicalSource {
				if sources := handler.detailMediaSources(ctx, session, title); len(sources) != 0 {
					item.MediaSources = sources
				}
			}
			unspecified := -1
			for index := range item.MediaSources {
				if item.MediaSources[index].DefaultAudioStreamIndex == nil {
					item.MediaSources[index].DefaultAudioStreamIndex = &unspecified
				}
			}
		} else {
			item.MediaSources = nil
		}
		if includeTrickplay && hasCanonicalSource && (title.MediaType == "movie" || title.MediaType == "episode") {
			if delivery, supported := handler.playback.(trickplayDelivery); supported && delivery.TrickplayAvailable() {
				item.Trickplay = jellyfinTrickplayMetadata(item, item.Id)
			}
		}
	}
	return item, true
}

func itemQueryRequestsMediaSources(query ItemQuery, detail bool) bool {
	return itemQueryRequestsField(query, "MediaSources", detail) || itemQueryRequestsField(query, "MediaStreams", detail)
}
func itemQueryRequestsMetadata(query ItemQuery, detail bool) bool {
	if len(query.Fields) == 0 {
		return detail
	}
	for _, field := range [...]string{"Genres", "OriginalTitle", "Overview", "People", "ProviderIds", "Studios", "Taglines", "Trickplay"} {
		if containsItemType(query.Fields, field) {
			return true
		}
	}
	return false
}

func (handler *Handler) writeCatalogIDSelection(response http.ResponseWriter, request *http.Request, session AuthenticatedSession, query ItemQuery, mediaTypes []string, sortBy, sortOrder string) bool {
	requested := stringSet(query.Ids)
	projected := make(map[string]watchstate.CatalogTitle, len(query.Ids))
	if batch, ok := handler.catalog.(catalogBatchReader); ok {
		titles, err := batch.GetCatalogTitles(request.Context(), session.Principal, query.Ids)
		if err != nil {
			handler.writeCatalogError(response, err)
			return true
		}
		for _, title := range titles {
			id := strings.ToLower(strings.TrimSpace(title.ID))
			if _, allowed := requested[id]; allowed {
				projected[id] = title
			}
		}
	} else {
		for _, id := range query.Ids {
			title, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, id)
			if err != nil {
				if errors.Is(err, watchstate.ErrNotFound) {
					continue
				}
				handler.writeCatalogError(response, err)
				return true
			}
			if strings.EqualFold(strings.TrimSpace(title.ID), id) {
				projected[strings.ToLower(id)] = title
			}
		}
	}
	if len(projected) == 0 {
		return false
	}
	allowedTypes := intersectMediaTypes(mediaTypes, []string{"movie", "series", "season", "episode", "video"})
	filterTypes := !noCatalogMediaTypes(allowedTypes)
	typeSet := stringSet(allowedTypes)
	search := strings.ToLower(query.SearchTerm)
	titles := make([]watchstate.CatalogTitle, 0, len(projected))
	for _, id := range query.Ids {
		title, ok := projected[strings.ToLower(id)]
		if !ok {
			continue
		}
		if filterTypes {
			if _, ok := typeSet[strings.ToLower(title.MediaType)]; !ok {
				continue
			}
		}
		if search != "" && !strings.Contains(strings.ToLower(title.Title), search) {
			continue
		}
		if !handler.catalogTitleMatchesQuery(title, query) {
			continue
		}
		titles = append(titles, title)
	}
	if sortBy != "" {
		sortCatalogTitles(titles, sortBy, sortOrder)
	}
	total := len(titles)
	start := min(query.StartIndex, total)
	end := min(start+query.Limit, total)
	items := make([]BaseItemDto, 0, end-start)
	for _, title := range titles[start:end] {
		if total == 1 {
			item, ok := handler.detailedCatalogItem(request.Context(), session, title, query.EnableUserData,
				itemQueryRequestsMediaSources(query, false), itemQueryRequestsMediaSources(query, false),
				itemQueryRequestsField(query, "Trickplay", false), itemQueryRequestsMetadata(query, false))
			if !ok {
				return true
			}
			applyItemQueryProjection(&item, query, false)
			items = append(items, item)
			continue
		}
		item := handler.baseItemDTO(title, query.EnableUserData)
		applyItemQueryProjection(&item, query, false)
		items = append(items, item)
	}
	reportedTotal := total
	if !query.EnableTotalRecordCount {
		reportedTotal = 0
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: items, TotalRecordCount: reportedTotal, StartIndex: query.StartIndex})
	return true
}

func (handler *Handler) catalogTitleMatchesQuery(title watchstate.CatalogTitle, query ItemQuery) bool {
	filters := catalogQueryFilters(query)
	if filters.UnavailableDataFilter || filters.Played != nil && (title.Progress != nil && title.Progress.Completed) != *filters.Played ||
		filters.Favorite != nil && title.Favorite != *filters.Favorite ||
		filters.Resumable != nil && (title.Progress != nil && title.Progress.PositionSeconds > 0 && !title.Progress.Completed) != *filters.Resumable ||
		filters.MinCommunityRating != nil && (title.CommunityRating == nil || float64(*title.CommunityRating) < *filters.MinCommunityRating) ||
		filters.HasSubtitles != nil && title.HasSubtitles != *filters.HasSubtitles {
		return false
	}
	if len(filters.Genres) != 0 {
		matched := false
		for _, genre := range title.Genres {
			for _, wanted := range filters.Genres {
				matched = matched || strings.EqualFold(strings.TrimSpace(genre), strings.TrimSpace(wanted))
			}
		}
		if !matched {
			return false
		}
	}
	if len(filters.Studios) != 0 {
		matched := false
		for _, studio := range title.Studios {
			for _, wanted := range filters.Studios {
				matched = matched || strings.EqualFold(strings.TrimSpace(studio), strings.TrimSpace(wanted))
			}
		}
		if !matched {
			return false
		}
	}
	if len(filters.GenreIDs) != 0 {
		matched := false
		for _, genre := range title.Genres {
			for _, wanted := range filters.GenreIDs {
				matched = matched || strings.EqualFold(handler.catalogFacetID("genre", genre), wanted)
			}
		}
		if !matched {
			return false
		}
	}
	if len(filters.Years) != 0 {
		value := title.Released
		if len(value) < 4 {
			value = title.ReleaseInfo
		}
		year, err := strconv.Atoi(value[:min(4, len(value))])
		matched := false
		if err == nil {
			for _, wanted := range filters.Years {
				matched = matched || year == wanted
			}
		}
		if !matched {
			return false
		}
	}
	if len(filters.PersonIDs) != 0 {
		matched := false
		for _, person := range title.People {
			for _, wanted := range filters.PersonIDs {
				matched = matched || strings.EqualFold(handler.catalogFacetID("person", person.Name), wanted)
			}
		}
		if !matched {
			return false
		}
	}
	return len(filters.OfficialRatings) == 0 && len(filters.Tags) == 0
}

func (handler *Handler) writeItems(response http.ResponseWriter, request *http.Request, session AuthenticatedSession, fixed *catalogHierarchy) {
	parsed, err := ParseItemQuery(request.URL.Query())
	if err != nil {
		handler.logCompatCatalogEvent(compatCatalogRejectedMessage, "query_shape")
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	mediaTypes, err := catalogMediaTypes(parsed.IncludeItemTypes)
	if err != nil {
		handler.logCompatCatalogEvent(compatCatalogRejectedMessage, "include_item_types")
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid IncludeItemTypes")
		return
	}
	sortBy, sortOrder, err := catalogSort(parsed, request)
	if err != nil {
		handler.logCompatCatalogEvent(compatCatalogRejectedMessage, "sort")
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid sort")
		return
	}
	parentID := parsed.ParentId
	recursive := parsed.Recursive
	if fixed != nil {
		parentID, mediaTypes, recursive = fixed.parentID, fixed.mediaTypes, fixed.recursive
	}
	mediaTypes, err = applyItemMediaFilters(mediaTypes, parsed)
	if err != nil {
		handler.logCompatCatalogEvent(compatCatalogRejectedMessage, "media_filters")
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	if fixed == nil {
		idMediaTypes := intersectMediaTypes(mediaTypes, []string{"movie", "series", "season", "episode", "video"})
		if parentID == "" && len(parsed.Ids) != 0 && itemQuerySupportsIDFastPath(parsed) && !noCatalogMediaTypes(idMediaTypes) && handler.writeCatalogIDSelection(response, request, session, parsed, mediaTypes, sortBy, sortOrder) {
			return
		}
		views, valid := handler.virtualViews()
		if !valid {
			handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
			return
		}
		if parentID == "" && containsString(mediaTypes, "boxset") {
			handler.writeCollectionRoot(response, request, session, parsed, mediaTypes, sortBy, sortOrder)
			return
		}
		if parentID == "" && containsItemType(parsed.IncludeItemTypes, "CollectionFolder") && itemQueryAllowsCollectionFolders(parsed) {
			handler.writeSessionViewRoot(response, request.Context(), session.Principal, parsed, sortBy, sortOrder)
			return
		}
		if parentID == "" && len(parsed.IncludeItemTypes) == 0 && !itemQueryHasCatalogFilters(parsed) {
			handler.writeSessionViewRoot(response, request.Context(), session.Principal, parsed, sortBy, sortOrder)
			return
		}
		if strings.EqualFold(parentID, views[2].Id) {
			handler.writeCollectionRoot(response, request, session, parsed, mediaTypes, sortBy, sortOrder)
			return
		}
		if strings.EqualFold(parentID, views[0].Id) || strings.EqualFold(parentID, views[1].Id) {
			if projected, ok := handler.virtualCollectionPage(request.Context(), session.Principal, parsed, parentID, mediaTypes, sortBy, sortOrder); ok {
				handler.writeJSON(response, http.StatusOK, projected)
				return
			}
		}
		if parentID != "" && !strings.EqualFold(parentID, views[0].Id) && !strings.EqualFold(parentID, views[1].Id) && handler.collections != nil {
			value, collectionErr := handler.resolveCollectionView(request.Context(), session.Principal, parentID)
			if collectionErr == nil {
				contentTypes := intersectMediaTypes(mediaTypes, []string{collection.MediaTypeMovie, collection.MediaTypeSeries})
				if parsed.Recursive && parsed.Limit != 0 && len(parsed.IncludeItemTypes) != 0 && !noCatalogMediaTypes(contentTypes) {
					handler.writeCollectionItems(response, request, session, parsed, mediaTypes, value, sortBy, sortOrder)
				} else {
					handler.writeCollectionFolders(response, request.Context(), session.Principal, parsed, value)
				}
				return
			}
			if !errors.Is(collectionErr, collection.ErrNotFound) {
				if request.Context().Err() != nil {
					return
				}
				handler.logCompatCatalogEvent(compatCatalogFailedMessage, "collection_lookup:"+compatCatalogErrorClass(collectionErr))
				handler.writeCollectionError(response, collectionErr)
				return
			}
			value, folder, folderErr := handler.findCollectionFolder(request.Context(), session.Principal, parentID)
			if folderErr == nil {
				value.Folders = []collection.Folder{folder}
				handler.writeCollectionItems(response, request, session, parsed, mediaTypes, value, sortBy, sortOrder)
				return
			}
			if !errors.Is(folderErr, collection.ErrNotFound) {
				if request.Context().Err() != nil {
					return
				}
				handler.logCompatCatalogEvent(compatCatalogFailedMessage, "collection_folder_lookup:"+compatCatalogErrorClass(folderErr))
				handler.writeCollectionError(response, folderErr)
				return
			}
		}
	}
	if parentID != "" {
		if fixed != nil {
			itemID, parseErr := ParseItemID(parentID)
			if parseErr != nil {
				handler.logCompatCatalogEvent(compatCatalogRejectedMessage, "parent")
				handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid ParentId")
				return
			}
			parentID = itemID.String()
		} else {
			parentID, mediaTypes, recursive, err = handler.translateVirtualParent(parentID, mediaTypes, recursive)
			if err != nil {
				handler.logCompatCatalogEvent(compatCatalogRejectedMessage, "parent")
				handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid ParentId")
				return
			}
		}
	}
	mediaTypes = projectedCatalogMediaTypes(mediaTypes)
	if noCatalogMediaTypes(mediaTypes) || containsString(mediaTypes, "boxset") {
		handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: []BaseItemDto{}, TotalRecordCount: 0, StartIndex: parsed.StartIndex})
		return
	}
	catalogLimit := parsed.Limit
	countOnly := catalogLimit == 0
	if countOnly {
		catalogLimit = 1
	}
	catalogQuery := catalogQueryFilters(parsed)
	if fixed != nil {
		catalogQuery.IncludePeople = itemQueryRequestsField(parsed, "People", true)
	}
	catalogQuery.ParentID, catalogQuery.MediaTypes = parentID, mediaTypes
	catalogQuery.SearchTerm, catalogQuery.IDs = parsed.SearchTerm, parsed.Ids
	catalogQuery.Recursive, catalogQuery.Offset, catalogQuery.Limit = recursive || len(parsed.Ids) != 0, parsed.StartIndex, catalogLimit
	catalogQuery.SortBy, catalogQuery.SortOrder = sortBy, sortOrder
	if err := handler.resolveCatalogFacetFilters(request.Context(), session.Principal, parentID, mediaTypes, &catalogQuery); err != nil {
		if request.Context().Err() != nil {
			return
		}
		handler.logCompatCatalogEvent(compatCatalogFailedMessage, "facet_filters:"+compatCatalogErrorClass(err))
		handler.writeCatalogError(response, err)
		return
	}
	page, err := handler.catalog.ListCatalogItems(request.Context(), session.Principal, catalogQuery)
	if err != nil {
		if request.Context().Err() != nil {
			return
		}
		handler.logCompatCatalogEvent(compatCatalogFailedMessage, "ordinary_catalog:"+compatCatalogErrorClass(err))
		handler.writeCatalogError(response, err)
		return
	}
	items := make([]BaseItemDto, 0, len(page.Items))
	if !countOnly {
		for _, title := range page.Items {
			item := handler.baseItemDTO(title, parsed.EnableUserData)
			applyItemQueryProjection(&item, parsed, fixed != nil)
			items = append(items, item)
		}
	}
	startIndex := page.Offset
	if countOnly {
		startIndex = parsed.StartIndex
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: items, TotalRecordCount: itemQueryTotal(parsed, page.Total), StartIndex: startIndex})
}

func (handler *Handler) writeCollectionRoot(response http.ResponseWriter, request *http.Request, session AuthenticatedSession, query ItemQuery, mediaTypes []string, sortBy, sortOrder string) {
	if handler.collections == nil || noCatalogMediaTypes(intersectMediaTypes(mediaTypes, []string{"boxset"})) {
		handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: []BaseItemDto{}, TotalRecordCount: 0, StartIndex: query.StartIndex})
		return
	}
	values, err := handler.collections.List(request.Context(), session.Principal)
	if err != nil {
		handler.logCompatCatalogEvent(compatCatalogFailedMessage, "collection_root:"+compatCatalogErrorClass(err))
		handler.writeCollectionError(response, err)
		return
	}
	idFilter := stringSet(query.Ids)
	search := strings.ToLower(query.SearchTerm)
	filtered := make([]collection.Collection, 0, len(values))
	for _, value := range values {
		if len(idFilter) != 0 {
			if _, ok := idFilter[strings.ToLower(value.ID)]; !ok {
				continue
			}
		}
		if search != "" && !strings.Contains(strings.ToLower(value.Title), search) {
			continue
		}
		filtered = append(filtered, value)
	}
	if sortBy == "sortname" {
		sort.SliceStable(filtered, func(left, right int) bool {
			leftTitle, rightTitle := strings.ToLower(filtered[left].Title), strings.ToLower(filtered[right].Title)
			if leftTitle == rightTitle {
				return strings.ToLower(filtered[left].ID) < strings.ToLower(filtered[right].ID)
			}
			if sortOrder == "descending" {
				return leftTitle > rightTitle
			}
			return leftTitle < rightTitle
		})
	}
	total := len(filtered)
	start := query.StartIndex
	if start > total {
		start = total
	}
	end := start + query.Limit
	if end > total {
		end = total
	}
	items := make([]BaseItemDto, 0, end-start)
	for _, value := range filtered[start:end] {
		item := handler.collectionDTO(request.Context(), session.Principal, value)
		applyItemQueryProjection(&item, query, false)
		items = append(items, item)
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: items, TotalRecordCount: itemQueryTotal(query, total), StartIndex: query.StartIndex})
}

func (handler *Handler) writeSessionViewRoot(response http.ResponseWriter, ctx context.Context, principal auth.Principal, query ItemQuery, sortBy, sortOrder string) {
	views, err := handler.sessionViews(ctx, principal, false)
	if err != nil {
		handler.writeCollectionError(response, err)
		return
	}
	idFilter := stringSet(query.Ids)
	search := strings.ToLower(query.SearchTerm)
	filtered := make([]BaseItemDto, 0, len(views))
	for _, view := range views {
		if len(idFilter) != 0 {
			if _, ok := idFilter[strings.ToLower(view.Id)]; !ok {
				continue
			}
		}
		if search != "" && !strings.Contains(strings.ToLower(view.Name), search) {
			continue
		}
		filtered = append(filtered, view)
	}
	if sortBy == "sortname" {
		sort.SliceStable(filtered, func(left, right int) bool {
			leftTitle, rightTitle := strings.ToLower(filtered[left].SortName), strings.ToLower(filtered[right].SortName)
			if leftTitle == rightTitle {
				return strings.ToLower(filtered[left].Id) < strings.ToLower(filtered[right].Id)
			}
			if sortOrder == "descending" {
				return leftTitle > rightTitle
			}
			return leftTitle < rightTitle
		})
	}
	total := len(filtered)
	start := min(query.StartIndex, total)
	end := min(start+query.Limit, total)
	items := filtered[start:end]
	for index := range items {
		applyItemQueryProjection(&items[index], query, false)
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: items, TotalRecordCount: itemQueryTotal(query, total), StartIndex: query.StartIndex})
}

func (handler *Handler) writeCollectionFolders(response http.ResponseWriter, ctx context.Context, principal auth.Principal, query ItemQuery, value collection.Collection) {
	items, total := handler.collectionFolderPage(ctx, principal, value, query)
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: items, TotalRecordCount: itemQueryTotal(query, total), StartIndex: query.StartIndex})
}

func (handler *Handler) collectionFolderPage(ctx context.Context, principal auth.Principal, value collection.Collection, query ItemQuery) ([]BaseItemDto, int) {
	idFilter := stringSet(query.Ids)
	search := strings.ToLower(query.SearchTerm)
	filtered := make([]collection.Folder, 0, len(value.Folders))
	for _, folder := range value.Folders {
		if len(idFilter) != 0 {
			if _, ok := idFilter[strings.ToLower(folder.ID)]; !ok {
				continue
			}
		}
		if search != "" && !strings.Contains(strings.ToLower(folder.Title), search) {
			continue
		}
		filtered = append(filtered, folder)
	}
	if len(query.SortBy) != 0 {
		sort.SliceStable(filtered, func(left, right int) bool {
			leftTitle, rightTitle := strings.ToLower(filtered[left].Title), strings.ToLower(filtered[right].Title)
			if leftTitle == rightTitle {
				return strings.ToLower(filtered[left].ID) < strings.ToLower(filtered[right].ID)
			}
			if strings.EqualFold(query.SortOrder, "Descending") {
				return leftTitle > rightTitle
			}
			return leftTitle < rightTitle
		})
	}
	total := len(filtered)
	start := min(query.StartIndex, total)
	end := min(start+query.Limit, total)
	selected := append([]collection.Folder(nil), filtered[start:end]...)
	handler.hydrateCollectionFolderCovers(ctx, principal, value.ID, selected)
	items := make([]BaseItemDto, 0, len(selected))
	for _, folder := range selected {
		item := handler.collectionFolderDTO(value, folder)
		applyItemQueryProjection(&item, query, false)
		items = append(items, item)
	}
	if localizer, ok := handler.catalog.(catalogArtworkLocalizer); ok && len(selected) != 0 {
		upstream := make([]string, len(selected))
		for index := range selected {
			upstream[index] = collectionFolderLandscapeURL(selected[index])
		}
		localized := localizer.LocalizeArtworkURLs(ctx, upstream)
		if len(localized) == len(items) {
			for index, materialized := range localized {
				if tag, valid := localizedArtworkTag(materialized); valid {
					items[index].ImageTags = collectionFolderImageTags(tag)
					items[index].BackdropImageTags = collectionFolderBackdropImageTags(tag)
				}
			}
		}
	}
	for index := range items {
		applyItemQueryProjection(&items[index], query, false)
	}
	return items, total
}

func (handler *Handler) hydrateCollectionFolderCovers(ctx context.Context, principal auth.Principal, collectionID string, folders []collection.Folder) {
	if handler.collections == nil || len(folders) == 0 {
		return
	}
	semaphore := make(chan struct{}, maximumCollectionCoverConcurrency)
	var wait sync.WaitGroup
	for index := range folders {
		if collectionFolderLandscapeURL(folders[index]) != "" {
			continue
		}
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			resolved, err := handler.collections.ResolveFolder(ctx, principal, collectionID, folders[index].ID, 1, 1, "", "")
			if err != nil {
				return
			}
			folders[index].HeroBackdropURL = strings.TrimSpace(resolved.Folder.HeroBackdropURL)
			folders[index].CoverImageURL = strings.TrimSpace(resolved.Folder.CoverImageURL)
			if collectionFolderLandscapeURL(folders[index]) == "" {
				for _, item := range resolved.Items {
					folders[index].CoverImageURL = strings.TrimSpace(item.PosterURL)
					if folders[index].CoverImageURL != "" {
						break
					}
				}
			}
		}()
	}
	wait.Wait()
}

func collectionFolderLandscapeURL(folder collection.Folder) string {
	if backdrop := strings.TrimSpace(folder.HeroBackdropURL); backdrop != "" {
		return backdrop
	}
	return strings.TrimSpace(folder.CoverImageURL)
}

func (handler *Handler) collectionViewID(collectionID string) (string, error) {
	itemID, err := ParseItemID(collectionID)
	if err != nil {
		return "", collection.ErrInvalidInput
	}
	viewID, err := DeriveVirtualItemID(handler.serverInfo.ID, VirtualItemKey("collection-view:"+itemID.String()))
	if err != nil {
		return "", collection.ErrInvalidInput
	}
	return viewID.String(), nil
}

func (handler *Handler) resolveCollectionView(ctx context.Context, principal auth.Principal, rawID string) (collection.Collection, error) {
	itemID, err := ParseItemID(rawID)
	if err != nil {
		return collection.Collection{}, collection.ErrNotFound
	}
	canonicalID := itemID.String()
	values, err := handler.collections.List(ctx, principal)
	if err != nil {
		return collection.Collection{}, err
	}
	var matched collection.Collection
	found := false
	for _, candidate := range values {
		realID, realErr := ParseItemID(candidate.ID)
		viewID, viewErr := handler.collectionViewID(candidate.ID)
		matchesReal := realErr == nil && strings.EqualFold(realID.String(), canonicalID)
		matchesView := viewErr == nil && strings.EqualFold(viewID, canonicalID)
		if !matchesReal && !matchesView {
			continue
		}
		if found {
			return collection.Collection{}, collection.ErrInvalidInput
		}
		matched, found = candidate, true
	}
	if !found {
		return collection.Collection{}, collection.ErrNotFound
	}
	return matched, nil
}

func (handler *Handler) findCollectionFolder(ctx context.Context, principal auth.Principal, folderID string) (collection.Collection, collection.Folder, error) {
	if handler.collections == nil {
		return collection.Collection{}, collection.Folder{}, collection.ErrNotFound
	}
	values, err := handler.collections.List(ctx, principal)
	if err != nil {
		return collection.Collection{}, collection.Folder{}, err
	}
	var matchedCollection collection.Collection
	var matchedFolder collection.Folder
	found := false
	for _, value := range values {
		for _, folder := range value.Folders {
			if !strings.EqualFold(strings.TrimSpace(folder.ID), folderID) {
				continue
			}
			if found {
				return collection.Collection{}, collection.Folder{}, collection.ErrInvalidInput
			}
			matchedCollection, matchedFolder, found = value, folder, true
		}
	}
	if !found {
		return collection.Collection{}, collection.Folder{}, collection.ErrNotFound
	}
	return matchedCollection, matchedFolder, nil
}

func (handler *Handler) writeCollectionItems(response http.ResponseWriter, request *http.Request, session AuthenticatedSession, query ItemQuery, mediaTypes []string, value collection.Collection, sortBy, sortOrder string) {
	page, err := handler.collectionItemPage(request.Context(), session.Principal, query, mediaTypes, value, sortBy, sortOrder)
	if err != nil {
		handler.writeCollectionItemError(response, err)
		return
	}
	handler.writeJSON(response, http.StatusOK, page)
}

func (handler *Handler) collectionItemPage(ctx context.Context, principal auth.Principal, query ItemQuery, mediaTypes []string, value collection.Collection, sortBy, sortOrder string) (QueryResult[BaseItemDto], error) {
	allowed := intersectMediaTypes(mediaTypes, []string{collection.MediaTypeMovie, collection.MediaTypeSeries})
	if noCatalogMediaTypes(allowed) {
		return QueryResult[BaseItemDto]{Items: []BaseItemDto{}, TotalRecordCount: 0, StartIndex: query.StartIndex}, nil
	}
	resolver, ok := handler.catalog.(collectionItemResolver)
	if !ok {
		return QueryResult[BaseItemDto]{}, errCollectionResolverUnavailable
	}
	titles, more, err := handler.resolveCollectionWindow(ctx, principal, resolver, value, allowed, query, sortBy, sortOrder)
	if err != nil {
		return QueryResult[BaseItemDto]{}, err
	}
	start := min(query.StartIndex, len(titles))
	end := min(start+query.Limit, len(titles))
	items := make([]BaseItemDto, 0, end-start)
	for _, title := range titles[start:end] {
		item := handler.baseItemDTO(title, query.EnableUserData)
		applyItemQueryProjection(&item, query, false)
		items = append(items, item)
	}
	total := len(titles)
	if more && query.Limit != 0 {
		total = max(total, query.StartIndex+len(items)+1)
	}
	return QueryResult[BaseItemDto]{Items: items, TotalRecordCount: itemQueryTotal(query, total), StartIndex: query.StartIndex}, nil
}

func (handler *Handler) virtualCollectionPage(ctx context.Context, principal auth.Principal, query ItemQuery, parentID string, mediaTypes []string, sortBy, sortOrder string) (QueryResult[BaseItemDto], bool) {
	views, valid := handler.virtualViews()
	if !valid {
		return QueryResult[BaseItemDto]{}, false
	}
	mediaType := ""
	switch {
	case strings.EqualFold(parentID, views[0].Id):
		mediaType = collection.MediaTypeMovie
	case strings.EqualFold(parentID, views[1].Id):
		mediaType = collection.MediaTypeSeries
	default:
		return QueryResult[BaseItemDto]{}, false
	}
	if noCatalogMediaTypes(intersectMediaTypes(mediaTypes, []string{mediaType})) || handler.collections == nil {
		return QueryResult[BaseItemDto]{}, false
	}
	resolver, ok := handler.catalog.(collectionItemResolver)
	if !ok {
		return QueryResult[BaseItemDto]{}, false
	}
	values, err := handler.collections.List(ctx, principal)
	if err != nil || len(values) == 0 {
		return QueryResult[BaseItemDto]{}, false
	}
	if len(values) > maximumCollectionResolveLimit {
		values = values[:maximumCollectionResolveLimit]
	}
	target := query.StartIndex + query.Limit + 1
	if target > maximumCollectionWindowLimit {
		target = maximumCollectionWindowLimit
	}
	scan := query
	scan.StartIndex = 0
	scan.Limit = target - 1
	capacity := target
	if sortBy != "" {
		capacity = maximumCollectionWindowLimit
	}
	seen := make(map[string]struct{}, capacity)
	titles := make([]watchstate.CatalogTitle, 0, capacity)
	more := false
	for _, value := range values {
		resolved, resolvedMore, resolveErr := handler.resolveCollectionWindow(ctx, principal, resolver, value, []string{mediaType}, scan, sortBy, sortOrder)
		if resolveErr != nil {
			return QueryResult[BaseItemDto]{}, false
		}
		more = more || resolvedMore
		for _, title := range resolved {
			canonicalID := strings.ToLower(title.ID)
			if _, duplicate := seen[canonicalID]; duplicate {
				continue
			}
			seen[canonicalID] = struct{}{}
			titles = append(titles, title)
			if len(titles) >= maximumCollectionWindowLimit || sortBy == "" && len(titles) >= target {
				more = true
				break
			}
		}
		if len(titles) >= maximumCollectionWindowLimit || sortBy == "" && len(titles) >= target {
			break
		}
	}
	if len(titles) == 0 {
		return QueryResult[BaseItemDto]{}, false
	}
	if sortBy != "" {
		sortCatalogTitles(titles, sortBy, sortOrder)
	}
	start := min(query.StartIndex, len(titles))
	end := min(start+query.Limit, len(titles))
	items := make([]BaseItemDto, 0, end-start)
	for _, title := range titles[start:end] {
		item := handler.baseItemDTO(title, query.EnableUserData)
		applyItemQueryProjection(&item, query, false)
		items = append(items, item)
	}
	total := len(titles)
	if more {
		total = max(total, query.StartIndex+len(items)+1)
	}
	return QueryResult[BaseItemDto]{Items: items, TotalRecordCount: itemQueryTotal(query, total), StartIndex: query.StartIndex}, true
}

func itemQueryTotal(query ItemQuery, total int) int {
	if !query.EnableTotalRecordCount {
		return 0
	}
	return total
}

func (handler *Handler) writeCollectionItemError(response http.ResponseWriter, err error) {
	if errors.Is(err, errCollectionResolverUnavailable) {
		handler.logCompatCatalogEvent(compatCatalogFailedMessage, "collection_resolver_unavailable")
		handler.writeCompatError(response, http.StatusInternalServerError, "InternalServerError", "Internal server error")
		return
	}
	handler.logCompatCatalogEvent(compatCatalogFailedMessage, "collection_items:"+compatCatalogErrorClass(err))
	if errors.Is(err, collection.ErrActiveProfileRequired) || errors.Is(err, collection.ErrForbidden) ||
		errors.Is(err, collection.ErrNotFound) || errors.Is(err, collection.ErrInvalidInput) || errors.Is(err, collection.ErrProviderUnavailable) {
		handler.writeCollectionError(response, err)
		return
	}
	handler.writeCatalogError(response, err)
}

func (handler *Handler) resolveCollectionWindow(ctx context.Context, principal auth.Principal, resolver collectionItemResolver, value collection.Collection, mediaTypes []string, query ItemQuery, sortBy, sortOrder string) ([]watchstate.CatalogTitle, bool, error) {
	target := query.StartIndex + query.Limit + 1
	if target > maximumCollectionWindowLimit {
		target = maximumCollectionWindowLimit
	}
	allowed := stringSet(mediaTypes)
	idFilter := stringSet(query.Ids)
	search := strings.ToLower(query.SearchTerm)
	capacity := target
	seen := make(map[string]struct{}, capacity)
	titles := make([]watchstate.CatalogTitle, 0, capacity)
	providerFailed := false
	for _, folder := range value.Folders {
		if !collectionFolderMayContainMediaTypes(folder, allowed) {
			continue
		}
		for page := 1; page <= maximumCollectionResolvePage; page++ {
			resolved, err := handler.collections.ResolveFolder(ctx, principal, value.ID, folder.ID, page, maximumCollectionResolveLimit, "", "")
			if err != nil {
				return nil, false, err
			}
			if len(resolved.Errors) != 0 {
				providerFailed = true
			}
			for start := 0; start < len(resolved.Items); start += maximumCollectionResolveConcurrency {
				end := start + maximumCollectionResolveConcurrency
				if end > len(resolved.Items) {
					end = len(resolved.Items)
				}
				outcomes := resolveCollectionBatch(ctx, principal, resolver, resolved.Items[start:end], allowed)
				for _, outcome := range outcomes {
					if !outcome.attempted {
						continue
					}
					if outcome.err != nil {
						if collectionResolutionFatal(outcome.err) {
							return nil, false, outcome.err
						}
						continue
					}
					canonicalID := strings.ToLower(outcome.title.ID)
					if _, duplicate := seen[canonicalID]; duplicate {
						continue
					}
					seen[canonicalID] = struct{}{}
					if len(idFilter) != 0 {
						if _, ok := idFilter[canonicalID]; !ok {
							continue
						}
					}
					if search != "" && !strings.Contains(strings.ToLower(outcome.title.Title), search) {
						continue
					}
					if !handler.catalogTitleMatchesQuery(outcome.title, query) {
						continue
					}
					titles = append(titles, outcome.title)
					if len(titles) >= target {
						if sortBy != "" {
							sortCatalogTitles(titles, sortBy, sortOrder)
						}
						return titles, true, nil
					}
				}
			}
			if !resolved.HasMore {
				break
			}
			if page == maximumCollectionResolvePage {
				return nil, false, collection.ErrProviderUnavailable
			}
		}
	}
	if len(titles) == 0 && providerFailed {
		return nil, false, collection.ErrProviderUnavailable
	}
	if sortBy != "" {
		sortCatalogTitles(titles, sortBy, sortOrder)
	}
	return titles, false, nil
}

func configuredCollectionSourceMediaType(source collection.Source) string {
	mediaType := ""
	switch source.Kind {
	case collection.SourceKindAddonCatalog:
		if source.AddonCatalog != nil {
			mediaType = source.AddonCatalog.Type
		}
	case collection.SourceKindTMDB:
		if source.TMDB != nil {
			mediaType = source.TMDB.MediaType
		}
	case collection.SourceKindTrakt:
		if source.Trakt != nil {
			mediaType = source.Trakt.MediaType
		}
	case collection.SourceKindMDBList:
		if source.MDBList != nil {
			mediaType = source.MDBList.MediaType
		}
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType == "show" {
		return collection.MediaTypeSeries
	}
	return mediaType
}

func collectionHomeViewType(value collection.Collection) string {
	hasMovie := false
	hasSeries := false
	for _, folder := range value.Folders {
		if len(folder.Sources) == 0 {
			return "unknown"
		}
		for _, source := range folder.Sources {
			switch configuredCollectionSourceMediaType(source) {
			case collection.MediaTypeMovie:
				hasMovie = true
			case collection.MediaTypeSeries:
				hasSeries = true
			case collection.MediaTypeBoth:
				hasMovie = true
				hasSeries = true
			default:
				return "unknown"
			}
		}
	}
	if hasMovie {
		return "movies"
	}
	if hasSeries {
		return "tvshows"
	}
	return "unknown"
}

func collectionFolderMayContainMediaTypes(folder collection.Folder, allowed map[string]struct{}) bool {
	if len(folder.Sources) == 0 {
		return true
	}
	for _, source := range folder.Sources {
		mediaType := configuredCollectionSourceMediaType(source)
		if mediaType == "" || mediaType == collection.MediaTypeBoth {
			return true
		}
		if _, ok := allowed[mediaType]; ok {
			return true
		}
	}
	return false
}

type collectionTitleOutcome struct {
	title     watchstate.CatalogTitle
	err       error
	attempted bool
}

func resolveCollectionBatch(ctx context.Context, principal auth.Principal, resolver collectionItemResolver, items []collection.Item, allowed map[string]struct{}) []collectionTitleOutcome {
	outcomes := make([]collectionTitleOutcome, len(items))
	var wait sync.WaitGroup
	for index, item := range items {
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(item.MediaType))]; !ok {
			continue
		}
		outcomes[index].attempted = true
		wait.Add(1)
		go func(index int, item collection.Item) {
			defer wait.Done()
			outcomes[index].title, outcomes[index].err = resolver.ResolveCollectionItem(ctx, principal, item)
		}(index, item)
	}
	wait.Wait()
	return outcomes
}

func (handler *Handler) collectionDTO(ctx context.Context, principal auth.Principal, value collection.Collection) BaseItemDto {
	parentID := ""
	if views, ok := handler.virtualViews(); ok {
		parentID = views[2].Id
	}
	imageTags, backdropImageTags := handler.collectionArtworkTags(ctx, principal, value)
	return BaseItemDto{
		Id: value.ID, ServerId: handler.serverInfo.ID.String(), Name: value.Title, SortName: value.Title,
		Etag: value.ID, DisplayPreferencesId: value.ID, LocationType: "FileSystem",
		Type: "BoxSet", MediaType: "Unknown", IsFolder: true, ParentId: parentID,
		Genres: []string{}, ImageTags: imageTags, BackdropImageTags: backdropImageTags, UserData: &UserItemDataDto{Key: value.ID, ItemId: value.ID},
	}
}

func (handler *Handler) collectionViewDTO(ctx context.Context, principal auth.Principal, value collection.Collection) (BaseItemDto, error) {
	viewID, err := handler.collectionViewID(value.ID)
	if err != nil {
		return BaseItemDto{}, err
	}
	imageTags, backdropImageTags := handler.collectionViewArtworkTags(ctx, principal, value)
	if tag := imageTags["Primary"]; tag != "" {
		imageTags["Thumb"] = tag
		if len(backdropImageTags) == 0 {
			backdropImageTags = append(backdropImageTags, tag)
		}
	}
	return BaseItemDto{
		Id: viewID, ServerId: handler.serverInfo.ID.String(), Name: value.Title, SortName: value.Title,
		Etag: viewID, DisplayPreferencesId: viewID, LocationType: "FileSystem",
		Type: "CollectionFolder", MediaType: "Unknown", CollectionType: "unknown", IsFolder: true,
		PrimaryImageAspectRatio: 16.0 / 9.0,
		Genres:                  []string{}, ImageTags: imageTags, BackdropImageTags: backdropImageTags,
		UserData: &UserItemDataDto{Key: viewID, ItemId: viewID},
	}, nil
}

func (handler *Handler) collectionFolderDTO(value collection.Collection, folder collection.Folder) BaseItemDto {
	parentID, _ := handler.collectionViewID(value.ID)
	return BaseItemDto{
		Id: folder.ID, ServerId: handler.serverInfo.ID.String(), Name: folder.Title, SortName: folder.Title,
		Etag: folder.ID, DisplayPreferencesId: folder.ID, LocationType: "FileSystem",
		Type: "CollectionFolder", MediaType: "Unknown", CollectionType: "unknown", IsFolder: true, ParentId: parentID,
		PrimaryImageAspectRatio: 16.0 / 9.0,
		Genres:                  []string{}, ImageTags: map[string]string{}, BackdropImageTags: []string{}, UserData: &UserItemDataDto{Key: folder.ID, ItemId: folder.ID},
	}
}

func collectionFolderImageTags(tag string) map[string]string {
	return map[string]string{"Primary": tag, "Thumb": tag}
}

func collectionFolderBackdropImageTags(tag string) []string {
	return []string{tag}
}

func (handler *Handler) collectionFolderDetailDTO(ctx context.Context, principal auth.Principal, value collection.Collection, folder collection.Folder) BaseItemDto {
	folders := []collection.Folder{folder}
	handler.hydrateCollectionFolderCovers(ctx, principal, value.ID, folders)
	item := handler.collectionFolderDTO(value, folders[0])

	localizer, ok := handler.catalog.(catalogArtworkLocalizer)
	if !ok {
		return item
	}
	landscape := collectionFolderLandscapeURL(folders[0])
	logo := strings.TrimSpace(folders[0].TitleLogoURL)
	if landscape == "" && logo == "" {
		return item
	}
	localized := localizer.LocalizeArtworkURLs(ctx, []string{landscape, logo})
	if len(localized) != 2 {
		return item
	}
	if tag, valid := localizedArtworkTag(localized[0]); valid {
		item.ImageTags = collectionFolderImageTags(tag)
		item.BackdropImageTags = collectionFolderBackdropImageTags(tag)
	}
	if tag, valid := localizedArtworkTag(localized[1]); valid {
		item.ImageTags["Logo"] = tag
	}
	return item
}

func (handler *Handler) collectionArtworkTags(ctx context.Context, principal auth.Principal, value collection.Collection) (map[string]string, []string) {
	imageTags := make(map[string]string)
	backdropImageTags := make([]string, 0)
	localizer, ok := handler.catalog.(catalogArtworkLocalizer)
	if !ok {
		return imageTags, backdropImageTags
	}
	upstream := strings.TrimSpace(value.BackdropImageURL)
	fromBackdrop := upstream != ""
	logo := ""
	if len(value.Folders) != 0 {
		folder := []collection.Folder{value.Folders[0]}
		if upstream == "" {
			handler.hydrateCollectionFolderCovers(ctx, principal, value.ID, folder)
			upstream = strings.TrimSpace(folder[0].CoverImageURL)
			if upstream == "" {
				upstream = strings.TrimSpace(folder[0].HeroBackdropURL)
			}
		}
		logo = strings.TrimSpace(folder[0].TitleLogoURL)
	}
	localized := localizer.LocalizeArtworkURLs(ctx, []string{upstream, logo})
	if len(localized) != 2 {
		return imageTags, backdropImageTags
	}
	if tag, valid := localizedArtworkTag(localized[0]); valid {
		imageTags["Primary"] = tag
		if fromBackdrop {
			backdropImageTags = append(backdropImageTags, tag)
		}
	}
	if tag, valid := localizedArtworkTag(localized[1]); valid {
		imageTags["Logo"] = tag
	}
	return imageTags, backdropImageTags
}

func (handler *Handler) collectionViewArtworkTags(ctx context.Context, principal auth.Principal, value collection.Collection) (map[string]string, []string) {
	imageTags := make(map[string]string)
	backdropImageTags := make([]string, 0)
	localizer, ok := handler.catalog.(catalogArtworkLocalizer)
	if !ok {
		return imageTags, backdropImageTags
	}
	upstream := strings.TrimSpace(value.BackdropImageURL)
	logo := ""
	if len(value.Folders) != 0 {
		folder := []collection.Folder{value.Folders[0]}
		if upstream == "" {
			handler.hydrateCollectionFolderCovers(ctx, principal, value.ID, folder)
			upstream = collectionFolderLandscapeURL(folder[0])
		}
		logo = strings.TrimSpace(folder[0].TitleLogoURL)
	}
	localized := localizer.LocalizeArtworkURLs(ctx, []string{upstream, logo})
	if len(localized) != 2 {
		return imageTags, backdropImageTags
	}
	if tag, valid := localizedArtworkTag(localized[0]); valid {
		imageTags["Primary"] = tag
		backdropImageTags = append(backdropImageTags, tag)
	}
	if tag, valid := localizedArtworkTag(localized[1]); valid {
		imageTags["Logo"] = tag
	}
	return imageTags, backdropImageTags
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	return result
}

func collectionResolutionFatal(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, watchstate.ErrForbidden) || errors.Is(err, watchstate.ErrProfileRequired) ||
		errors.Is(err, collection.ErrForbidden) || errors.Is(err, collection.ErrActiveProfileRequired)
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
	if containsString(mediaTypes, "boxset") {
		handler.writeJSON(response, http.StatusOK, SearchHintResult{SearchHints: []SearchHintDto{}, TotalRecordCount: 0})
		return
	}
	mediaTypes = projectedCatalogMediaTypes(mediaTypes)
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
			Artists: []string{}, ChannelId: nil, PrimaryImageAspectRatio: item.PrimaryImageAspectRatio,
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
	handler.writeJSON(response, http.StatusOK, SearchHintResult{SearchHints: hints, TotalRecordCount: page.Total})
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

func containsItemType(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
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
		case strings.EqualFold(value, "Video"):
			mediaType = "video"
		case strings.EqualFold(value, "BoxSet"):
			mediaType = "boxset"
		case strings.EqualFold(value, "AggregateFolder"), strings.EqualFold(value, "Audio"),
			strings.EqualFold(value, "AudioBook"), strings.EqualFold(value, "BasePluginFolder"),
			strings.EqualFold(value, "Book"), strings.EqualFold(value, "Channel"),
			strings.EqualFold(value, "ChannelFolderItem"), strings.EqualFold(value, "CollectionFolder"),
			strings.EqualFold(value, "Folder"), strings.EqualFold(value, "Genre"),
			strings.EqualFold(value, "LiveTvChannel"), strings.EqualFold(value, "LiveTvProgram"),
			strings.EqualFold(value, "ManualPlaylistsFolder"), strings.EqualFold(value, "MusicAlbum"),
			strings.EqualFold(value, "MusicArtist"), strings.EqualFold(value, "MusicGenre"),
			strings.EqualFold(value, "MusicVideo"), strings.EqualFold(value, "Person"),
			strings.EqualFold(value, "Photo"), strings.EqualFold(value, "PhotoAlbum"),
			strings.EqualFold(value, "Playlist"), strings.EqualFold(value, "Program"),
			strings.EqualFold(value, "Recording"), strings.EqualFold(value, "Studio"),
			strings.EqualFold(value, "Trailer"), strings.EqualFold(value, "TvChannel"),
			strings.EqualFold(value, "TvProgram"), strings.EqualFold(value, "UserRootFolder"),
			strings.EqualFold(value, "UserView"), strings.EqualFold(value, "Year"):
			mediaType = "__unprojected__"
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

func projectedCatalogMediaTypes(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "__unprojected__" {
			result = append(result, value)
		}
	}
	if len(result) == 0 {
		return []string{"__none__"}
	}
	return result
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

func applyItemMediaFilters(mediaTypes []string, query ItemQuery) ([]string, error) {
	if len(query.MediaTypes) != 0 {
		allowed := make([]string, 0, 5)
		for _, value := range query.MediaTypes {
			switch {
			case strings.EqualFold(value, "Video"):
				allowed = append(allowed, "movie", "episode", "video")
			case strings.EqualFold(value, "Unknown"):
				allowed = append(allowed, "series", "season")
			case strings.EqualFold(value, "Audio"), strings.EqualFold(value, "Photo"), strings.EqualFold(value, "Book"):
			default:
				return nil, ErrInvalidQuery
			}
		}
		if len(allowed) == 0 {
			return []string{"__none__"}, nil
		}
		mediaTypes = intersectMediaTypes(mediaTypes, allowed)
	}
	excluded, err := catalogMediaTypes(query.ExcludeItemTypes)
	if err != nil {
		return nil, err
	}
	if len(excluded) != 0 && !noCatalogMediaTypes(mediaTypes) {
		excludedSet := stringSet(projectedCatalogMediaTypes(excluded))
		base := mediaTypes
		if len(base) == 0 {
			base = []string{"movie", "series", "season", "episode", "video"}
		}
		filtered := make([]string, 0, len(base))
		for _, mediaType := range base {
			if _, remove := excludedSet[mediaType]; !remove {
				filtered = append(filtered, mediaType)
			}
		}
		if len(filtered) == 0 {
			return []string{"__none__"}, nil
		}
		mediaTypes = filtered
	}
	folderFilter := ""
	for _, filter := range query.Filters {
		switch filter {
		case "isfolder", "isnotfolder":
			if folderFilter != "" && folderFilter != filter {
				return []string{"__none__"}, nil
			}
			folderFilter = filter
		}
	}
	if folderFilter == "isfolder" {
		mediaTypes = intersectMediaTypes(mediaTypes, []string{"series", "season"})
	} else if folderFilter == "isnotfolder" {
		mediaTypes = intersectMediaTypes(mediaTypes, []string{"movie", "episode", "video"})
	}
	return mediaTypes, nil
}

func itemQuerySupportsIDFastPath(query ItemQuery) bool {
	return query.MinCommunityRating == nil && len(query.GenreIds) == 0 && len(query.OfficialRatings) == 0 &&
		len(query.Tags) == 0 && len(query.PersonIds) == 0
}

func itemQueryAllowsCollectionFolders(query ItemQuery) bool {
	if len(query.MediaTypes) != 0 && !containsItemType(query.MediaTypes, "Unknown") {
		return false
	}
	if containsItemType(query.ExcludeItemTypes, "CollectionFolder") || containsItemType(query.ExcludeItemTypes, "Folder") {
		return false
	}
	if query.IsPlayed != nil && *query.IsPlayed || query.IsFavorite != nil && *query.IsFavorite ||
		query.IsResumable != nil && *query.IsResumable || query.HasTrailer != nil && *query.HasTrailer ||
		query.MinCommunityRating != nil || query.HasSubtitles != nil {
		return false
	}
	if len(query.Genres) != 0 || len(query.GenreIds) != 0 || len(query.Years) != 0 || len(query.OfficialRatings) != 0 ||
		len(query.Tags) != 0 || len(query.PersonIds) != 0 || len(query.Studios) != 0 {
		return false
	}
	for _, filter := range query.Filters {
		switch filter {
		case "isplayed", "isfavorite", "isresumable", "isnotfolder":
			return false
		}
	}
	return true
}

func catalogQueryFilters(query ItemQuery) watchstate.CatalogQuery {
	result := watchstate.CatalogQuery{
		Played: query.IsPlayed, Favorite: query.IsFavorite, Resumable: query.IsResumable,
		MinCommunityRating: query.MinCommunityRating, HasSubtitles: query.HasSubtitles,
		Genres: query.Genres, GenreIDs: query.GenreIds, Years: query.Years,
		OfficialRatings: query.OfficialRatings, Tags: query.Tags, PersonIDs: query.PersonIds, Studios: query.Studios,
		UnavailableDataFilter: query.HasTrailer != nil,
		OmitTotal:             !query.EnableTotalRecordCount, IncludePeople: containsItemType(query.Fields, "People"),
	}
	for _, filter := range query.Filters {
		var destination **bool
		var value bool
		switch filter {
		case "isplayed":
			destination, value = &result.Played, true
		case "isunplayed":
			destination, value = &result.Played, false
		case "isfavorite":
			destination, value = &result.Favorite, true
		case "isresumable":
			destination, value = &result.Resumable, true
		default:
			continue
		}
		if *destination != nil && **destination != value {
			result.UnavailableDataFilter = true
			continue
		}
		copy := value
		*destination = &copy
	}
	return result
}

func itemQueryHasCatalogFilters(query ItemQuery) bool {
	return query.SearchTerm != "" || len(query.Ids) != 0 || len(query.MediaTypes) != 0 || len(query.ExcludeItemTypes) != 0 ||
		len(query.Filters) != 0 || query.IsPlayed != nil || query.IsFavorite != nil || query.IsResumable != nil ||
		query.MinCommunityRating != nil || query.HasSubtitles != nil ||
		len(query.Genres) != 0 || len(query.GenreIds) != 0 || len(query.Years) != 0 || len(query.OfficialRatings) != 0 ||
		len(query.Tags) != 0 || len(query.PersonIds) != 0 || len(query.Studios) != 0 || query.HasTrailer != nil
}

func (handler *Handler) resolveCatalogFacetFilters(ctx context.Context, principal auth.Principal, parentID string, mediaTypes []string, query *watchstate.CatalogQuery) error {
	if query == nil || len(query.GenreIDs) == 0 && len(query.PersonIDs) == 0 {
		return nil
	}
	titles, err := handler.catalogSurfaceCandidates(ctx, principal, parentID, mediaTypes)
	if err != nil {
		return err
	}
	genreNames := make(map[string][]string)
	personIDs := make(map[string][]string)
	for _, title := range titles {
		for _, genre := range title.Genres {
			key := strings.ToLower(handler.catalogFacetID("genre", genre))
			genreNames[key] = appendUniqueFolded(genreNames[key], genre)
		}
		for _, person := range title.People {
			if strings.TrimSpace(person.ID) == "" {
				continue
			}
			key := strings.ToLower(handler.catalogFacetID("person", person.Name))
			personIDs[key] = appendUniqueFolded(personIDs[key], person.ID)
		}
	}
	remainingGenres := make([]string, 0, len(query.GenreIDs))
	for _, wanted := range query.GenreIDs {
		if names := genreNames[strings.ToLower(wanted)]; len(names) != 0 {
			for _, name := range names {
				query.Genres = appendUniqueFolded(query.Genres, name)
			}
		} else {
			remainingGenres = append(remainingGenres, wanted)
		}
	}
	query.GenreIDs = remainingGenres
	remainingPeople := make([]string, 0, len(query.PersonIDs))
	for _, wanted := range query.PersonIDs {
		if ids := personIDs[strings.ToLower(wanted)]; len(ids) != 0 {
			for _, id := range ids {
				remainingPeople = appendUniqueFolded(remainingPeople, id)
			}
		} else {
			remainingPeople = append(remainingPeople, wanted)
		}
	}
	query.PersonIDs = remainingPeople
	return nil
}

func appendUniqueFolded(values []string, value string) []string {
	for _, current := range values {
		if strings.EqualFold(current, value) {
			return values
		}
	}
	return append(values, value)
}

func itemQueryRequestsField(query ItemQuery, field string, detail bool) bool {
	return len(query.Fields) == 0 && detail || containsItemType(query.Fields, field)
}

func applyItemQueryProjection(item *BaseItemDto, query ItemQuery, detail bool) {
	if !query.EnableUserData {
		item.UserData = nil
	}
	if !itemQueryRequestsField(query, "DateCreated", detail) {
		item.DateCreated = ""
	}
	if !itemQueryRequestsField(query, "DisplayPreferencesId", detail) {
		item.DisplayPreferencesId = ""
	}
	if !itemQueryRequestsField(query, "Etag", detail) {
		item.Etag = ""
	}
	if !itemQueryRequestsField(query, "Genres", detail) {
		item.Genres = nil
	}
	if !itemQueryRequestsField(query, "OriginalTitle", detail) {
		item.OriginalTitle = ""
	}
	if !itemQueryRequestsField(query, "Overview", detail) {
		item.Overview = ""
	}
	if !itemQueryRequestsField(query, "ParentId", detail) {
		item.ParentId = ""
	}
	if !itemQueryRequestsField(query, "Path", detail) {
		item.Path = ""
	}
	if !itemQueryRequestsField(query, "People", detail) {
		item.People = nil
	}
	if !itemQueryRequestsField(query, "PrimaryImageAspectRatio", detail) {
		item.PrimaryImageAspectRatio = 0
	}
	if !itemQueryRequestsField(query, "ProviderIds", detail) {
		item.ProviderIds = nil
	}
	if !itemQueryRequestsField(query, "Studios", detail) {
		item.Studios = nil
	}
	if !itemQueryRequestsField(query, "MediaSourceCount", detail) {
		item.MediaSourceCount = nil
	}
	if !itemQueryRequestsField(query, "SortName", detail) {
		item.SortName = ""
	}
	if !itemQueryRequestsField(query, "Taglines", detail) {
		item.Taglines = nil
	}
	if !itemQueryRequestsField(query, "Trickplay", detail) {
		item.Trickplay = nil
	}
	mediaSources := itemQueryRequestsField(query, "MediaSources", detail) || itemQueryRequestsField(query, "MediaStreams", detail)
	if !mediaSources {
		item.MediaSources = nil
	} else if !itemQueryRequestsField(query, "MediaStreams", detail) {
		for index := range item.MediaSources {
			item.MediaSources[index].MediaStreams = []MediaStreamInfo{}
		}
	}
	if !query.EnableImages || query.ImageTypeLimit == 0 {
		item.ImageTags = map[string]string{}
		item.BackdropImageTags = []string{}
		return
	}
	if len(query.EnableImageTypes) != 0 {
		if !containsItemType(query.EnableImageTypes, "Primary") {
			delete(item.ImageTags, "Primary")
		}
		if !containsItemType(query.EnableImageTypes, "Thumb") {
			delete(item.ImageTags, "Thumb")
		}
		if !containsItemType(query.EnableImageTypes, "Logo") {
			delete(item.ImageTags, "Logo")
		}
		if !containsItemType(query.EnableImageTypes, "Banner") {
			delete(item.ImageTags, "Banner")
		}
		if !containsItemType(query.EnableImageTypes, "Art") {
			delete(item.ImageTags, "Art")
		}
		if !containsItemType(query.EnableImageTypes, "Backdrop") {
			item.BackdropImageTags = []string{}
		}
	}
	if len(item.BackdropImageTags) > query.ImageTypeLimit {
		item.BackdropImageTags = item.BackdropImageTags[:query.ImageTypeLimit]
	}
}

func catalogSort(query ItemQuery, _ *http.Request) (string, string, error) {
	keys := make([]string, 0, min(len(query.SortBy), 3))
	for _, value := range query.SortBy {
		var key string
		switch {
		case strings.EqualFold(value, "Name"), strings.EqualFold(value, "SortName"):
			key = "sortname"
		case strings.EqualFold(value, "DateCreated"):
			key = "datecreated"
		case strings.EqualFold(value, "DateLastContentAdded"):
			key = "datelastcontentadded"
		case strings.EqualFold(value, "ProductionYear"):
			key = "productionyear"
		default:
			continue
		}
		keys = append(keys, key)
		if len(keys) == 3 {
			break
		}
	}
	if len(keys) == 0 {
		return "", "", nil
	}
	return strings.Join(keys, ","), strings.ToLower(query.SortOrder), nil
}

func (handler *Handler) baseItemDTO(title watchstate.CatalogTitle, includeUserData bool) BaseItemDto {
	item := BaseItemDto{
		Id: title.ID, ServerId: handler.serverInfo.ID.String(), Name: title.Title, SortName: title.Title,
		Etag: title.ID, LocationType: "FileSystem", OriginalTitle: title.OriginalTitle,
		ParentId: title.ParentID, SeriesId: title.SeriesID, SeasonId: title.SeasonID,
		SeriesName: title.SeriesTitle, SeasonName: title.SeasonTitle,
		IndexNumber: title.Ordinal, ParentIndexNumber: title.ParentOrdinal, Overview: title.Overview,
		Status: title.Status, Genres: append([]string(nil), title.Genres[:min(len(title.Genres), MaximumQueryListValues)]...), CommunityRating: title.CommunityRating,
		ProviderIds: jellyfinProviderIDs(title.ProviderIDs), ImageTags: make(map[string]string),
		BackdropImageTags: make([]string, 0),
	}
	if !title.CreatedAt.IsZero() {
		item.DateCreated = title.CreatedAt.UTC().Format("2006-01-02T15:04:05.0000000Z")
	}
	if item.Genres == nil {
		item.Genres = make([]string, 0)
	}
	for _, studio := range catalogStudioNames([]watchstate.CatalogTitle{title}) {
		item.Studios = append(item.Studios, NameGuidPair{Name: studio, Id: handler.catalogFacetID("studio", studio)})
	}
	if title.Tagline != "" {
		item.Taglines = []string{title.Tagline}
	}
	switch title.MediaType {
	case "movie":
		item.Type, item.MediaType, item.IsPlayable, item.CanDownload = "Movie", "Video", true, true
		item.PrimaryImageAspectRatio = 2.0 / 3.0
	case "series":
		item.Type, item.MediaType, item.IsFolder = "Series", "Unknown", true
		item.PrimaryImageAspectRatio = 2.0 / 3.0
	case "season":
		item.Type, item.MediaType, item.IsFolder = "Season", "Unknown", true
		item.PrimaryImageAspectRatio = 2.0 / 3.0
	case "episode":
		item.Type, item.MediaType, item.IsPlayable, item.CanDownload = "Episode", "Video", true, true
		item.PrimaryImageAspectRatio = 16.0 / 9.0
	case "video":
		item.Type, item.MediaType, item.IsPlayable, item.CanDownload = "Video", "Video", true, true
		item.PrimaryImageAspectRatio = 16.0 / 9.0
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
	if ended, err := time.Parse(time.DateOnly, title.EndDate); err == nil {
		item.EndDate = time.Date(ended.Year(), ended.Month(), ended.Day(), 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
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
	for imageType, materialized := range map[string]string{
		"Logo": title.LogoURL, "Banner": title.BannerURL, "Art": title.ArtURL,
	} {
		if tag, ok := localizedArtworkTag(materialized); ok {
			item.ImageTags[imageType] = tag
		}
	}
	for _, person := range title.People[:min(len(title.People), MaximumQueryListValues)] {
		if strings.TrimSpace(person.Name) == "" {
			continue
		}
		value := BaseItemPerson{Name: person.Name, Id: handler.catalogFacetID("person", person.Name), Role: person.Role, Type: person.Type}
		if value.Type == "" {
			value.Type = "Actor"
		}
		if tag, ok := localizedArtworkTag(person.ImageURL); ok {
			value.PrimaryImageTag = tag
		}
		item.People = append(item.People, value)
	}
	if includeUserData {
		userData := userDataFromCatalogTitle(title)
		item.UserData = &userData
	}
	if item.Type == "Movie" || item.Type == "Episode" || item.Type == "Video" {
		streamPath := "/Videos/" + url.PathEscape(item.Id) + "/stream"
		mediaPath := "/rivune/" + url.PathEscape(item.Id) + "/" + url.PathEscape(item.Id) + ".strm"
		item.Path = mediaPath
		item.MediaSources = []MediaSourceInfo{{
			Id: item.Id, Name: item.Name, Path: mediaPath, DirectStreamUrl: streamPath + "?MediaSourceId=" + url.QueryEscape(item.Id) + "&Static=true",
			Container: "strm", Protocol: "File", Type: "Default", IsRemote: false,
			SupportsDirectPlay: true, SupportsDirectStream: true, SupportsTranscoding: true, SupportsProbing: true, VideoType: "VideoFile",
			RunTimeTicks: item.RunTimeTicks, ETag: item.Id, Formats: []string{"strm"}, RequiredHttpHeaders: map[string]string{}, MediaAttachments: []any{}, MediaStreams: []MediaStreamInfo{},
		}}
	}
	mediaSourceCount := len(item.MediaSources)
	item.MediaSourceCount = &mediaSourceCount

	return item
}
func userDataFromCatalogTitle(title watchstate.CatalogTitle) UserItemDataDto {
	value := UserItemDataDto{
		IsFavorite: title.Favorite,
		Key:        title.ID,
		ItemId:     title.ID,
	}
	if title.Progress != nil {
		value.PlaybackPositionTicks = SecondsToTicks(int64(title.Progress.PositionSeconds))
		value.Played = title.Progress.Completed
		if title.Progress.DurationSeconds > 0 {
			percentage := math.Min(100, math.Max(0, float64(title.Progress.PositionSeconds)*100/float64(title.Progress.DurationSeconds)))
			value.PlayedPercentage = &percentage
		}
		if title.Progress.Completed {
			value.PlayCount = 1
		}
		if title.Progress.LastWatchedAt != nil && !title.Progress.LastWatchedAt.IsZero() {
			value.LastPlayedDate = title.Progress.LastWatchedAt.UTC().Format("2006-01-02T15:04:05.0000000Z")
		}
	}
	if title.UserData != nil {
		if title.UserData.RatingSet {
			value.Rating = title.UserData.Rating
		}
		if title.UserData.PlayedPercentageSet {
			value.PlayedPercentage = title.UserData.PlayedPercentage
		}
		if title.UserData.UnplayedItemCountSet {
			value.UnplayedItemCount = title.UserData.UnplayedItemCount
		}
		if title.UserData.PlayCountSet && title.UserData.PlayCount != nil {
			value.PlayCount = *title.UserData.PlayCount
		}
		if title.UserData.LikesSet {
			value.Likes = title.UserData.Likes
		}
		if title.UserData.LastPlayedDateSet {
			value.LastPlayedDate = ""
			if title.UserData.LastPlayedDate != nil {
				value.LastPlayedDate = title.UserData.LastPlayedDate.UTC().Format("2006-01-02T15:04:05.0000000Z")
			}
		}
	}
	return value
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

func (handler *Handler) writeCollectionError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, collection.ErrNotFound):
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
	case errors.Is(err, collection.ErrInvalidInput):
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid collection query")
	case errors.Is(err, collection.ErrForbidden), errors.Is(err, collection.ErrActiveProfileRequired):
		handler.writeCompatError(response, http.StatusUnauthorized, "Unauthorized", "A valid compatibility token is required")
	case errors.Is(err, collection.ErrProviderUnavailable):
		handler.writeCompatError(response, http.StatusBadGateway, "ProviderUnavailable", "A collection provider is unavailable")
	default:
		handler.writeCompatError(response, http.StatusInternalServerError, "InternalServerError", "Internal server error")
	}
}

func (handler *Handler) logCompatCatalogEvent(message, stage string) {
	if handler == nil || handler.logger == nil {
		return
	}
	handler.logger.LogAttrs(context.Background(), slog.LevelInfo, message, slog.String("stage", stage))
}

func compatCatalogErrorClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, watchstate.ErrNotFound), errors.Is(err, collection.ErrNotFound):
		return "not_found"
	case errors.Is(err, watchstate.ErrInvalidInput), errors.Is(err, collection.ErrInvalidInput):
		return "invalid_input"
	case errors.Is(err, watchstate.ErrForbidden), errors.Is(err, collection.ErrForbidden):
		return "forbidden"
	case errors.Is(err, watchstate.ErrProfileRequired), errors.Is(err, collection.ErrActiveProfileRequired):
		return "profile_required"
	case errors.Is(err, collection.ErrProviderUnavailable):
		return "provider_unavailable"
	default:
		return "internal"
	}
}

const maximumCatalogSurfaceCandidates = 1000

type catalogPersonFacet struct {
	Name            string
	Type            string
	PrimaryImageTag string
}

func (handler *Handler) catalogSurfaceSession(response http.ResponseWriter, request *http.Request) (AuthenticatedSession, bool) {
	session, ok := handler.catalogSession(response, request)
	if !ok {
		return AuthenticatedSession{}, false
	}
	if userID := request.PathValue("userId"); userID != "" && !handler.requireBoundUser(response, userID, session) {
		return AuthenticatedSession{}, false
	}
	if !handler.requireOptionalQueryUser(response, request, session) {
		return AuthenticatedSession{}, false
	}
	return session, true
}

func (handler *Handler) handleItemsFilters(response http.ResponseWriter, request *http.Request) {
	handler.writeItemsFilters(response, request, false)
}

func (handler *Handler) handleItemsFilters2(response http.ResponseWriter, request *http.Request) {
	handler.writeItemsFilters(response, request, true)
}

func (handler *Handler) writeItemsFilters(response http.ResponseWriter, request *http.Request, modern bool) {
	session, ok := handler.catalogSurfaceSession(response, request)
	if !ok {
		return
	}
	_, mediaTypes, parentID, ok := handler.catalogSurfaceQuery(response, request, true)
	if !ok {
		return
	}
	titles, err := handler.catalogSurfaceCandidates(request.Context(), session.Principal, parentID, mediaTypes)
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	genres := catalogGenreNames(titles)
	if modern {
		pairs := make([]NameGuidPair, 0, len(genres))
		for _, name := range genres {
			pairs = append(pairs, NameGuidPair{Name: name, Id: handler.catalogFacetID("genre", name)})
		}
		handler.writeJSON(response, http.StatusOK, QueryFilters{Genres: pairs, Tags: []string{}})
		return
	}
	handler.writeJSON(response, http.StatusOK, QueryFiltersLegacy{
		Genres: genres, Tags: []string{}, OfficialRatings: []string{}, Years: catalogYears(titles),
	})
}

func (handler *Handler) catalogSurfaceQuery(response http.ResponseWriter, request *http.Request, recursive bool) (ItemQuery, []string, string, bool) {
	query, err := ParseItemQuery(request.URL.Query())
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return ItemQuery{}, nil, "", false
	}
	mediaTypes, err := catalogMediaTypes(query.IncludeItemTypes)
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid IncludeItemTypes")
		return ItemQuery{}, nil, "", false
	}
	mediaTypes = projectedCatalogMediaTypes(mediaTypes)
	rawMediaTypes, err := commaSeparated(request.URL.Query(), "MediaTypes")
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return ItemQuery{}, nil, "", false
	}
	for _, mediaType := range rawMediaTypes {
		if !strings.EqualFold(mediaType, "Video") && !strings.EqualFold(mediaType, "Unknown") {
			mediaTypes = []string{"__none__"}
		}
	}
	positiveKinds := make([]string, 0, 2)
	for _, selector := range []struct {
		Name      string
		MediaType string
	}{{"IsMovie", "movie"}, {"IsSeries", "series"}} {
		_, found, scalarErr := queryScalar(request.URL.Query(), selector.Name)
		if scalarErr != nil {
			err = scalarErr
			break
		}
		if found {
			selected, boolErr := booleanValue(request.URL.Query(), selector.Name, false)
			if boolErr != nil {
				err = boolErr
				break
			}
			if selected {
				positiveKinds = append(positiveKinds, selector.MediaType)
			}
		}
	}
	for _, selector := range []string{"IsAiring", "IsSports", "IsKids", "IsNews"} {
		_, found, scalarErr := queryScalar(request.URL.Query(), selector)
		if scalarErr != nil {
			err = scalarErr
			break
		}
		if found {
			selected, boolErr := booleanValue(request.URL.Query(), selector, false)
			if boolErr != nil {
				err = boolErr
				break
			}
			if selected {
				mediaTypes = []string{"__none__"}
			}
		}
	}
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return ItemQuery{}, nil, "", false
	}
	if len(positiveKinds) != 0 {
		mediaTypes = intersectMediaTypes(mediaTypes, positiveKinds)
	}
	parentID := query.ParentId
	if parentID != "" {
		parentID, mediaTypes, _, err = handler.translateVirtualParent(parentID, mediaTypes, recursive)
		if err != nil {
			handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid ParentId")
			return ItemQuery{}, nil, "", false
		}
	}
	return query, mediaTypes, parentID, true
}

func (handler *Handler) catalogSurfaceCandidates(ctx context.Context, principal auth.Principal, parentID string, mediaTypes []string) ([]watchstate.CatalogTitle, error) {
	if noCatalogMediaTypes(mediaTypes) {
		return []watchstate.CatalogTitle{}, nil
	}
	items := make([]watchstate.CatalogTitle, 0, min(MaximumQueryLimit, maximumCatalogSurfaceCandidates))
	for offset := 0; offset < maximumCatalogSurfaceCandidates; {
		limit := min(MaximumQueryLimit, maximumCatalogSurfaceCandidates-offset)
		page, err := handler.catalog.ListCatalogItems(ctx, principal, watchstate.CatalogQuery{
			ParentID: parentID, MediaTypes: mediaTypes, Recursive: true, Offset: offset, Limit: limit, IncludePeople: true,
		})
		if err != nil {
			return nil, err
		}
		remaining := maximumCatalogSurfaceCandidates - len(items)
		if len(page.Items) > remaining {
			page.Items = page.Items[:remaining]
		}
		items = append(items, page.Items...)
		next := offset + len(page.Items)
		if len(page.Items) == 0 || next <= offset || next >= page.Total || len(page.Items) < limit {
			break
		}
		offset = next
	}
	return items, nil
}

func catalogGenreNames(titles []watchstate.CatalogTitle) []string {
	names := make(map[string]string)
	for _, title := range titles {
		for _, raw := range title.Genres[:min(len(title.Genres), MaximumQueryListValues)] {
			name := strings.TrimSpace(raw)
			if name == "" || len(name) > MaximumQueryValueBytes {
				continue
			}
			key := strings.ToLower(name)
			if _, exists := names[key]; !exists {
				if len(names) == maximumCatalogSurfaceCandidates {
					break
				}
				names[key] = name
			}
		}
		if len(names) == maximumCatalogSurfaceCandidates {
			break
		}
	}
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name)
	}
	sort.Slice(result, func(left, right int) bool {
		leftKey, rightKey := strings.ToLower(result[left]), strings.ToLower(result[right])
		if leftKey == rightKey {
			return result[left] < result[right]
		}
		return leftKey < rightKey
	})
	return result
}

func catalogStudioNames(titles []watchstate.CatalogTitle) []string {
	names := make(map[string]string)
	for _, title := range titles {
		for _, raw := range title.Studios[:min(len(title.Studios), MaximumQueryListValues)] {
			name := strings.TrimSpace(raw)
			if name == "" || len(name) > MaximumQueryValueBytes {
				continue
			}
			key := strings.ToLower(name)
			current, exists := names[key]
			if !exists {
				if len(names) == maximumCatalogSurfaceCandidates {
					break
				}
				names[key] = name
			} else if name < current {
				names[key] = name
			}
		}
		if len(names) == maximumCatalogSurfaceCandidates {
			break
		}
	}
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name)
	}
	sort.Slice(result, func(left, right int) bool {
		leftKey, rightKey := strings.ToLower(result[left]), strings.ToLower(result[right])
		if leftKey == rightKey {
			return result[left] < result[right]
		}
		return leftKey < rightKey
	})
	return result
}

func catalogYears(titles []watchstate.CatalogTitle) []int {
	set := make(map[int]struct{})
	for _, title := range titles {
		value := title.Released
		if len(value) < 4 {
			value = title.ReleaseInfo
		}
		if len(value) < 4 {
			continue
		}
		year, err := strconv.Atoi(value[:4])
		if err == nil && year > 0 && year <= 9999 {
			set[year] = struct{}{}
		}
	}
	result := make([]int, 0, len(set))
	for year := range set {
		result = append(result, year)
	}
	sort.Ints(result)
	return result
}

func catalogPeople(titles []watchstate.CatalogTitle) []catalogPersonFacet {
	people := make(map[string]catalogPersonFacet)
	for _, title := range titles {
		for _, person := range title.People[:min(len(title.People), MaximumQueryListValues)] {
			name := strings.TrimSpace(person.Name)
			if name == "" || len(name) > MaximumQueryValueBytes {
				continue
			}
			key := strings.ToLower(name)
			current, exists := people[key]
			if !exists {
				if len(people) == maximumCatalogSurfaceCandidates {
					break
				}
				current = catalogPersonFacet{Name: name, Type: person.Type}
			}
			if current.Type == "" {
				current.Type = "Actor"
			}
			if current.PrimaryImageTag == "" {
				current.PrimaryImageTag, _ = localizedArtworkTag(person.ImageURL)
			}
			people[key] = current
		}
		if len(people) == maximumCatalogSurfaceCandidates {
			break
		}
	}
	result := make([]catalogPersonFacet, 0, len(people))
	for _, person := range people {
		result = append(result, person)
	}
	sort.Slice(result, func(left, right int) bool {
		leftKey, rightKey := strings.ToLower(result[left].Name), strings.ToLower(result[right].Name)
		if leftKey == rightKey {
			return result[left].Name < result[right].Name
		}
		return leftKey < rightKey
	})
	return result
}

func (handler *Handler) catalogFacetID(kind, value string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(value))))
	id, err := DeriveVirtualItemID(handler.serverInfo.ID, VirtualItemKey(fmt.Sprintf("catalog-%s:%x", kind, digest)))
	if err != nil {
		return ""
	}
	return id.String()
}

func (handler *Handler) handleGenres(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSurfaceSession(response, request)
	if !ok {
		return
	}
	query, mediaTypes, parentID, ok := handler.catalogSurfaceQuery(response, request, true)
	if !ok {
		return
	}
	titles, err := handler.catalogSurfaceCandidates(request.Context(), session.Principal, parentID, mediaTypes)
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	names := filterCatalogNames(catalogGenreNames(titles), query.SearchTerm)
	start, end := catalogSurfaceWindow(len(names), query.StartIndex, query.Limit)
	items := make([]BaseItemDto, 0, end-start)
	for _, name := range names[start:end] {
		items = append(items, handler.catalogFacetDTO("genre", name, "Genre", ""))
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: items, TotalRecordCount: len(names), StartIndex: query.StartIndex})
}

func (handler *Handler) handleGenre(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSurfaceSession(response, request)
	if !ok {
		return
	}
	_, mediaTypes, parentID, ok := handler.catalogSurfaceQuery(response, request, true)
	if !ok {
		return
	}
	titles, err := handler.catalogSurfaceCandidates(request.Context(), session.Principal, parentID, mediaTypes)
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	wanted := request.PathValue("genreName")
	for _, name := range catalogGenreNames(titles) {
		if strings.EqualFold(name, wanted) {
			handler.writeJSON(response, http.StatusOK, handler.catalogFacetDTO("genre", name, "Genre", ""))
			return
		}
	}
	handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
}

func (handler *Handler) handleStudios(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSurfaceSession(response, request)
	if !ok {
		return
	}
	query, mediaTypes, parentID, ok := handler.catalogSurfaceQuery(response, request, true)
	if !ok {
		return
	}
	titles, err := handler.catalogSurfaceCandidates(request.Context(), session.Principal, parentID, mediaTypes)
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	names := filterCatalogNames(catalogStudioNames(titles), query.SearchTerm)
	start, end := catalogSurfaceWindow(len(names), query.StartIndex, query.Limit)
	items := make([]BaseItemDto, 0, end-start)
	for _, name := range names[start:end] {
		items = append(items, handler.catalogFacetDTO("studio", name, "Studio", ""))
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: items, TotalRecordCount: len(names), StartIndex: query.StartIndex})
}

func (handler *Handler) handlePersons(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSurfaceSession(response, request)
	if !ok {
		return
	}
	query, mediaTypes, parentID, ok := handler.catalogSurfaceQuery(response, request, true)
	if !ok {
		return
	}
	var titles []watchstate.CatalogTitle
	appearsInID, appearsFound, appearsErr := queryScalar(request.URL.Query(), "AppearsInItemId")
	if appearsErr != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	if appearsFound {
		itemID, parseErr := ParseItemID(appearsInID)
		if parseErr != nil {
			handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid AppearsInItemId")
			return
		}
		title, readErr := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, itemID.String())
		if readErr != nil {
			handler.writeCatalogError(response, readErr)
			return
		}
		titles = []watchstate.CatalogTitle{title}
	} else {
		listed, readErr := handler.catalogSurfaceCandidates(request.Context(), session.Principal, parentID, mediaTypes)
		if readErr != nil {
			handler.writeCatalogError(response, readErr)
			return
		}
		titles = listed
	}
	includeTypes, err := commaSeparated(request.URL.Query(), "PersonTypes")
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	excludeTypes, err := commaSeparated(request.URL.Query(), "ExcludePersonTypes")
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	people := catalogPeople(titles)
	filtered := make([]catalogPersonFacet, 0, len(people))
	for _, person := range people {
		if query.SearchTerm != "" && !strings.Contains(strings.ToLower(person.Name), strings.ToLower(query.SearchTerm)) {
			continue
		}
		if len(includeTypes) != 0 && !containsFold(includeTypes, person.Type) {
			continue
		}
		if containsFold(excludeTypes, person.Type) {
			continue
		}
		filtered = append(filtered, person)
	}
	start, end := catalogSurfaceWindow(len(filtered), query.StartIndex, query.Limit)
	items := make([]BaseItemDto, 0, end-start)
	for _, person := range filtered[start:end] {
		items = append(items, handler.catalogFacetDTO("person", person.Name, "Person", person.PrimaryImageTag))
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: items, TotalRecordCount: len(filtered), StartIndex: query.StartIndex})
}

func (handler *Handler) handlePerson(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSurfaceSession(response, request)
	if !ok {
		return
	}
	_, mediaTypes, parentID, ok := handler.catalogSurfaceQuery(response, request, true)
	if !ok {
		return
	}
	titles, err := handler.catalogSurfaceCandidates(request.Context(), session.Principal, parentID, mediaTypes)
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	wanted := request.PathValue("name")
	for _, person := range catalogPeople(titles) {
		if strings.EqualFold(person.Name, wanted) {
			handler.writeJSON(response, http.StatusOK, handler.catalogFacetDTO("person", person.Name, "Person", person.PrimaryImageTag))
			return
		}
	}
	handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
}

func filterCatalogNames(names []string, search string) []string {
	if search == "" {
		return names
	}
	search = strings.ToLower(search)
	result := make([]string, 0, len(names))
	for _, name := range names {
		if strings.Contains(strings.ToLower(name), search) {
			result = append(result, name)
		}
	}
	return result
}

func catalogSurfaceWindow(total, offset, limit int) (int, int) {
	start := min(offset, total)
	return start, min(start+limit, total)
}

func (handler *Handler) catalogFacetDTO(kind, name, itemType, imageTag string) BaseItemDto {
	item := BaseItemDto{
		Id: handler.catalogFacetID(kind, name), ServerId: handler.serverInfo.ID.String(), Name: name,
		SortName: name, Type: itemType, IsFolder: true, Genres: []string{}, ImageTags: map[string]string{}, BackdropImageTags: []string{},
	}
	if imageTag != "" {
		item.ImageTags["Primary"] = imageTag
	}
	return item
}

func (handler *Handler) handleSuggestions(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSurfaceSession(response, request)
	if !ok {
		return
	}
	query, err := ParseItemQuery(request.URL.Query())
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	itemTypes, err := commaSeparated(request.URL.Query(), "Type")
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	if len(itemTypes) == 0 {
		itemTypes = query.IncludeItemTypes
	}
	mediaTypes, err := catalogMediaTypes(itemTypes)
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid item type")
		return
	}
	rawMediaTypes, err := commaSeparated(request.URL.Query(), "MediaType")
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	if len(rawMediaTypes) != 0 {
		for _, mediaType := range rawMediaTypes {
			if !strings.EqualFold(mediaType, "Video") && !strings.EqualFold(mediaType, "Unknown") {
				handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: []BaseItemDto{}, StartIndex: query.StartIndex})
				return
			}
		}
	}
	mediaTypes = projectedCatalogMediaTypes(mediaTypes)
	if noCatalogMediaTypes(mediaTypes) {
		handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: []BaseItemDto{}, StartIndex: query.StartIndex})
		return
	}
	page, err := handler.catalog.ListCatalogItems(request.Context(), session.Principal, watchstate.CatalogQuery{
		MediaTypes: mediaTypes, Recursive: true, Offset: query.StartIndex, Limit: query.Limit,
	})
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	items := make([]BaseItemDto, 0, len(page.Items))
	for _, title := range page.Items {
		item := handler.baseItemDTO(title, query.EnableUserData)
		applyItemQueryProjection(&item, query, false)
		items = append(items, item)
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: items, TotalRecordCount: page.Total, StartIndex: page.Offset})
}

func (handler *Handler) handleEmptyCatalogDomain(response http.ResponseWriter, request *http.Request) {
	_, ok := handler.catalogSurfaceSession(response, request)
	if !ok {
		return
	}
	query, err := ParseItemQuery(request.URL.Query())
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: []BaseItemDto{}, TotalRecordCount: 0, StartIndex: query.StartIndex})
}

type scoredCatalogTitle struct {
	Title watchstate.CatalogTitle
	Score int
}

func (handler *Handler) handleSimilarItems(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSurfaceSession(response, request)
	if !ok {
		return
	}
	query, err := ParseItemQuery(request.URL.Query())
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	if _, err = canonicalIDs(request.URL.Query(), "ExcludeArtistIds"); err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid ExcludeArtistIds")
		return
	}
	itemID, err := ParseItemID(request.PathValue("itemId"))
	if err != nil {
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
		return
	}
	baseline, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, itemID.String())
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	mediaTypes := []string{baseline.MediaType}
	candidates, err := handler.catalogSurfaceCandidates(request.Context(), session.Principal, "", mediaTypes)
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	scored := similarCatalogTitles(baseline, candidates)
	limit := min(query.Limit, len(scored))
	items := make([]BaseItemDto, 0, limit)
	for _, candidate := range scored[:limit] {
		items = append(items, handler.baseItemDTO(candidate.Title, query.EnableUserData))
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: items, TotalRecordCount: len(scored), StartIndex: 0})
}

func similarCatalogTitles(baseline watchstate.CatalogTitle, candidates []watchstate.CatalogTitle) []scoredCatalogTitle {
	genres := stringFoldSet(baseline.Genres[:min(len(baseline.Genres), MaximumQueryListValues)])
	peopleLimit := min(len(baseline.People), MaximumQueryListValues)
	people := make([]string, 0, peopleLimit)
	for _, person := range baseline.People[:peopleLimit] {
		people = append(people, person.Name)
	}
	personSet := stringFoldSet(people)
	result := make([]scoredCatalogTitle, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.EqualFold(candidate.ID, baseline.ID) {
			continue
		}
		score := 0
		seenGenres := make(map[string]struct{}, min(len(candidate.Genres), MaximumQueryListValues))
		for _, genre := range candidate.Genres[:min(len(candidate.Genres), MaximumQueryListValues)] {
			key := strings.ToLower(strings.TrimSpace(genre))
			if _, duplicate := seenGenres[key]; duplicate {
				continue
			}
			seenGenres[key] = struct{}{}
			if _, match := genres[key]; match {
				score += 2
			}
		}
		seenPeople := make(map[string]struct{}, min(len(candidate.People), MaximumQueryListValues))
		for _, person := range candidate.People[:min(len(candidate.People), MaximumQueryListValues)] {
			key := strings.ToLower(strings.TrimSpace(person.Name))
			if _, duplicate := seenPeople[key]; duplicate {
				continue
			}
			seenPeople[key] = struct{}{}
			if _, match := personSet[key]; match {
				score++
			}
		}
		if score > 0 {
			result = append(result, scoredCatalogTitle{Title: candidate, Score: score})
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].Score != result[right].Score {
			return result[left].Score > result[right].Score
		}
		leftName, rightName := strings.ToLower(result[left].Title.Title), strings.ToLower(result[right].Title.Title)
		if leftName != rightName {
			return leftName < rightName
		}
		return result[left].Title.ID < result[right].Title.ID
	})
	return result
}

func stringFoldSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

func (handler *Handler) handleMovieRecommendations(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSurfaceSession(response, request)
	if !ok {
		return
	}
	query, mediaTypes, parentID, ok := handler.catalogSurfaceQuery(response, request, true)
	if !ok {
		return
	}
	categoryLimit, err := boundedInteger(request.URL.Query(), "CategoryLimit", 1, 20, 5)
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	itemLimit, err := boundedInteger(request.URL.Query(), "ItemLimit", 1, MaximumQueryLimit, 8)
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	movieTypes := intersectMediaTypes(mediaTypes, []string{"movie"})
	if noCatalogMediaTypes(movieTypes) {
		handler.writeJSON(response, http.StatusOK, []RecommendationDto{})
		return
	}
	titles, err := handler.catalogSurfaceCandidates(request.Context(), session.Principal, parentID, movieTypes)
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	result := make([]RecommendationDto, 0, min(categoryLimit, len(titles)))
	for _, baseline := range titles {
		if !baseline.InLibrary && baseline.Progress == nil {
			continue
		}
		similar := similarCatalogTitles(baseline, titles)
		if len(similar) == 0 {
			continue
		}
		limit := min(itemLimit, len(similar))
		items := make([]BaseItemDto, 0, limit)
		for _, candidate := range similar[:limit] {
			items = append(items, handler.baseItemDTO(candidate.Title, query.EnableUserData))
		}
		recommendationType := "SimilarToLikedItem"
		if baseline.Progress != nil {
			recommendationType = "SimilarToRecentlyPlayed"
		}
		result = append(result, RecommendationDto{
			Items: items, RecommendationType: recommendationType, BaselineItemName: baseline.Title,
			CategoryId: handler.catalogFacetID("recommendation", baseline.ID),
		})
		if len(result) == categoryLimit {
			break
		}
	}
	handler.writeJSON(response, http.StatusOK, result)
}

func (handler *Handler) handleUpcomingShows(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSurfaceSession(response, request)
	if !ok {
		return
	}
	query, mediaTypes, parentID, ok := handler.catalogSurfaceQuery(response, request, true)
	if !ok {
		return
	}
	episodeTypes := intersectMediaTypes(mediaTypes, []string{"episode"})
	if noCatalogMediaTypes(episodeTypes) {
		handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: []BaseItemDto{}, StartIndex: query.StartIndex})
		return
	}
	titles, err := handler.catalogSurfaceCandidates(request.Context(), session.Principal, parentID, episodeTypes)
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	upcoming := make([]watchstate.CatalogTitle, 0, len(titles))
	for _, title := range titles {
		released, parseErr := time.Parse(time.DateOnly, title.Released)
		if parseErr == nil && !released.Before(today) {
			upcoming = append(upcoming, title)
		}
	}
	sort.SliceStable(upcoming, func(left, right int) bool {
		if upcoming[left].Released != upcoming[right].Released {
			return upcoming[left].Released < upcoming[right].Released
		}
		return upcoming[left].ID < upcoming[right].ID
	})
	start, end := catalogSurfaceWindow(len(upcoming), query.StartIndex, query.Limit)
	items := make([]BaseItemDto, 0, end-start)
	for _, title := range upcoming[start:end] {
		item := handler.baseItemDTO(title, query.EnableUserData)
		applyItemQueryProjection(&item, query, false)
		items = append(items, item)
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: items, TotalRecordCount: len(upcoming), StartIndex: query.StartIndex})
}

func (handler *Handler) handleThemeMedia(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSurfaceSession(response, request)
	itemID, validID := canonicalCompatUUID(request.PathValue("itemId"))
	if !ok || !validID || !handler.requireCatalogSurfaceItem(response, request, session, itemID) {
		return
	}
	if _, err := ParseItemQuery(request.URL.Query()); err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	empty := func() *ThemeMediaResult {
		return &ThemeMediaResult{Items: []BaseItemDto{}, TotalRecordCount: 0, StartIndex: 0, OwnerId: itemID}
	}
	handler.writeJSON(response, http.StatusOK, AllThemeMediaResult{
		ThemeVideosResult: empty(), ThemeSongsResult: empty(), SoundtrackSongsResult: empty(),
	})
}

func (handler *Handler) handleThemeSongs(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSurfaceSession(response, request)
	itemID, validID := canonicalCompatUUID(request.PathValue("itemId"))
	if !ok || !validID || !handler.requireCatalogSurfaceItem(response, request, session, itemID) {
		return
	}
	if _, err := ParseItemQuery(request.URL.Query()); err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	handler.writeJSON(response, http.StatusOK, ThemeMediaResult{Items: []BaseItemDto{}, TotalRecordCount: 0, StartIndex: 0, OwnerId: itemID})
}

func (handler *Handler) handleSpecialFeatures(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSurfaceSession(response, request)
	if !ok || !handler.requireCatalogSurfaceItem(response, request, session, request.PathValue("itemId")) {
		return
	}
	if _, err := ParseItemQuery(request.URL.Query()); err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	handler.writeJSON(response, http.StatusOK, []BaseItemDto{})
}

func (handler *Handler) handleIntros(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSurfaceSession(response, request)
	if !ok || !handler.requireCatalogSurfaceItem(response, request, session, request.PathValue("itemId")) {
		return
	}
	if _, err := ParseItemQuery(request.URL.Query()); err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: []BaseItemDto{}, TotalRecordCount: 0, StartIndex: 0})
}

func (handler *Handler) handleLocalTrailers(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSurfaceSession(response, request)
	if !ok || !handler.requireCatalogSurfaceItem(response, request, session, request.PathValue("itemId")) {
		return
	}
	if _, err := ParseItemQuery(request.URL.Query()); err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	handler.writeJSON(response, http.StatusOK, []BaseItemDto{})
}

func (handler *Handler) requireCatalogSurfaceItem(response http.ResponseWriter, request *http.Request, session AuthenticatedSession, rawID string) bool {
	itemID, err := ParseItemID(rawID)
	if err != nil {
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
		return false
	}
	_, err = handler.catalog.GetCatalogTitle(request.Context(), session.Principal, itemID.String())
	if err != nil {
		handler.writeCatalogError(response, err)
		return false
	}
	return true
}
