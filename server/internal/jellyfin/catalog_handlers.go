package jellyfin

import (
	"context"
	"errors"
	"log/slog"
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
	maximumCollectionResolveConcurrency = 8
	maximumCollectionCoverConcurrency   = 4
	compatCatalogRejectedMessage        = "jellyfin compatibility catalog query rejected"
	compatCatalogFailedMessage          = "jellyfin compatibility catalog request failed"
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
	views, err := handler.sessionViews()
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
	parentID := query.ParentId
	if parentID != "" && handler.collections != nil {
		value, collectionErr := handler.collections.Get(request.Context(), session.Principal, parentID)
		if collectionErr == nil {
			items, _ := handler.collectionFolderPage(request.Context(), session.Principal, value, query)
			handler.writeJSON(response, http.StatusOK, items)
			return
		}
		if !errors.Is(collectionErr, collection.ErrNotFound) {
			handler.writeCollectionError(response, collectionErr)
			return
		}
	}
	if parentID != "" {
		parentID, mediaTypes, _, err = handler.translateVirtualParent(parentID, mediaTypes, false)
		if err != nil {
			handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid ParentId")
			return
		}
	}
	mediaTypes = projectedCatalogMediaTypes(mediaTypes)
	if noCatalogMediaTypes(mediaTypes) || containsString(mediaTypes, "boxset") {
		handler.writeJSON(response, http.StatusOK, []BaseItemDto{})
		return
	}
	page, err := handler.catalog.ListCatalogItems(request.Context(), session.Principal, watchstate.CatalogQuery{
		ParentID: parentID, MediaTypes: mediaTypes, Offset: query.StartIndex, Limit: query.Limit,
	})
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	items := make([]BaseItemDto, 0, len(page.Items))
	for _, title := range page.Items {
		items = append(items, handler.baseItemDTO(title, query.EnableUserData))
	}
	handler.writeJSON(response, http.StatusOK, items)
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

func (handler *Handler) writeViews(response http.ResponseWriter, request *http.Request, session AuthenticatedSession) {
	views, err := handler.sessionViews()
	if err != nil {
		handler.writeCollectionError(response, err)
		return
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: views, TotalRecordCount: len(views), StartIndex: 0})
}

func (handler *Handler) sessionViews() ([]BaseItemDto, error) {
	views, ok := handler.virtualViews()
	if !ok {
		return nil, collection.ErrNotFound
	}
	return views, nil
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
		{Id: moviesID.String(), ServerId: serverID, Name: "Movies", SortName: "Movies", Etag: moviesID.String(), DisplayPreferencesId: moviesID.String(), LocationType: "FileSystem", Type: "CollectionFolder", MediaType: "Unknown", CollectionType: "movies", IsFolder: true, Genres: []string{}, ImageTags: map[string]string{}, BackdropImageTags: []string{}, UserData: &UserItemDataDto{Key: moviesID.String(), ItemId: moviesID.String()}},
		{Id: tvID.String(), ServerId: serverID, Name: "TV Shows", SortName: "TV Shows", Etag: tvID.String(), DisplayPreferencesId: tvID.String(), LocationType: "FileSystem", Type: "CollectionFolder", MediaType: "Unknown", CollectionType: "tvshows", IsFolder: true, Genres: []string{}, ImageTags: map[string]string{}, BackdropImageTags: []string{}, UserData: &UserItemDataDto{Key: tvID.String(), ItemId: tvID.String()}},
		{Id: collectionsID.String(), ServerId: serverID, Name: "Collections", SortName: "Collections", Etag: collectionsID.String(), DisplayPreferencesId: collectionsID.String(), LocationType: "FileSystem", Type: "CollectionFolder", MediaType: "Unknown", CollectionType: "boxsets", IsFolder: true, Genres: []string{}, ImageTags: map[string]string{}, BackdropImageTags: []string{}, UserData: &UserItemDataDto{Key: collectionsID.String(), ItemId: collectionsID.String()}},
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
	if handler.collections != nil {
		value, collectionErr := handler.collections.Get(request.Context(), session.Principal, itemID.String())
		if collectionErr == nil {
			handler.writeJSON(response, http.StatusOK, handler.collectionDTO(request.Context(), session.Principal, value))
			return
		}
		if !errors.Is(collectionErr, collection.ErrNotFound) {
			handler.writeCollectionError(response, collectionErr)
			return
		}
		value, folder, folderErr := handler.findCollectionFolder(request.Context(), session.Principal, itemID.String())
		if folderErr == nil {
			handler.writeJSON(response, http.StatusOK, handler.collectionFolderDetailDTO(request.Context(), session.Principal, value, folder))
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
	handler.writeJSON(response, http.StatusOK, handler.baseItemDTO(title, query.EnableUserData))
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
	if fixed == nil {
		views, valid := handler.virtualViews()
		if !valid {
			handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
			return
		}
		if parentID == "" && containsString(mediaTypes, "boxset") {
			handler.writeCollectionRoot(response, request, session, parsed, mediaTypes, sortBy, sortOrder)
			return
		}
		if parentID != "" && strings.EqualFold(parentID, views[2].Id) {
			handler.writeCollectionRoot(response, request, session, parsed, mediaTypes, sortBy, sortOrder)
			return
		}
		if parentID != "" && !strings.EqualFold(parentID, views[0].Id) && !strings.EqualFold(parentID, views[1].Id) && handler.collections != nil {
			value, collectionErr := handler.collections.Get(request.Context(), session.Principal, parentID)
			if collectionErr == nil {
				if parsed.Recursive {
					handler.writeCollectionItems(response, request, session, parsed, mediaTypes, value, sortBy, sortOrder)
				} else {
					handler.writeCollectionFolders(response, request.Context(), session.Principal, parsed, value)
				}
				return
			}
			if !errors.Is(collectionErr, collection.ErrNotFound) {
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
	page, err := handler.catalog.ListCatalogItems(request.Context(), session.Principal, watchstate.CatalogQuery{
		ParentID: parentID, MediaTypes: mediaTypes, SearchTerm: parsed.SearchTerm, IDs: parsed.Ids,
		Recursive: recursive, Offset: parsed.StartIndex, Limit: parsed.Limit,
		SortBy: sortBy, SortOrder: sortOrder,
	})
	if err != nil {
		handler.logCompatCatalogEvent(compatCatalogFailedMessage, "ordinary_catalog:"+compatCatalogErrorClass(err))
		handler.writeCatalogError(response, err)
		return
	}
	items := make([]BaseItemDto, 0, len(page.Items))
	for _, title := range page.Items {
		items = append(items, handler.baseItemDTO(title, parsed.EnableUserData))
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: items, TotalRecordCount: page.Total, StartIndex: page.Offset})
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
			less := strings.ToLower(filtered[left].Title) < strings.ToLower(filtered[right].Title)
			if sortOrder == "descending" {
				return !less && !strings.EqualFold(filtered[left].Title, filtered[right].Title)
			}
			return less
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
		items = append(items, handler.collectionDTO(request.Context(), session.Principal, value))
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: items, TotalRecordCount: total, StartIndex: query.StartIndex})
}

func (handler *Handler) writeCollectionFolders(response http.ResponseWriter, ctx context.Context, principal auth.Principal, query ItemQuery, value collection.Collection) {
	items, total := handler.collectionFolderPage(ctx, principal, value, query)
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: items, TotalRecordCount: total, StartIndex: query.StartIndex})
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
	total := len(filtered)
	start := min(query.StartIndex, total)
	end := min(start+query.Limit, total)
	selected := append([]collection.Folder(nil), filtered[start:end]...)
	handler.hydrateCollectionFolderCovers(ctx, principal, value.ID, selected)
	items := make([]BaseItemDto, 0, len(selected))
	for _, folder := range selected {
		items = append(items, handler.collectionFolderDTO(value, folder))
	}
	if localizer, ok := handler.catalog.(catalogArtworkLocalizer); ok && len(selected) != 0 {
		upstream := make([]string, len(selected))
		for index := range selected {
			upstream[index] = selected[index].CoverImageURL
		}
		localized := localizer.LocalizeArtworkURLs(ctx, upstream)
		if len(localized) == len(items) {
			for index, materialized := range localized {
				if tag, valid := localizedArtworkTag(materialized); valid {
					items[index].ImageTags = map[string]string{"Primary": tag}
				}
			}
		}
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
		if strings.TrimSpace(folders[index].CoverImageURL) != "" {
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
			cover := strings.TrimSpace(resolved.Folder.CoverImageURL)
			if cover == "" {
				cover = strings.TrimSpace(resolved.Folder.HeroBackdropURL)
			}
			if cover == "" {
				for _, item := range resolved.Items {
					cover = strings.TrimSpace(item.PosterURL)
					if cover != "" {
						break
					}
				}
			}
			folders[index].CoverImageURL = cover
		}()
	}
	wait.Wait()
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
	allowed := intersectMediaTypes(mediaTypes, []string{collection.MediaTypeMovie, collection.MediaTypeSeries})
	if noCatalogMediaTypes(allowed) {
		handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: []BaseItemDto{}, TotalRecordCount: 0, StartIndex: query.StartIndex})
		return
	}
	resolver, ok := handler.catalog.(collectionItemResolver)
	if !ok {
		handler.logCompatCatalogEvent(compatCatalogFailedMessage, "collection_resolver_unavailable")
		handler.writeCompatError(response, http.StatusInternalServerError, "InternalServerError", "Internal server error")
		return
	}
	titles, more, err := handler.resolveCollectionWindow(request.Context(), session.Principal, resolver, value, allowed, query, sortBy, sortOrder)
	if err != nil {
		handler.logCompatCatalogEvent(compatCatalogFailedMessage, "collection_items:"+compatCatalogErrorClass(err))
		if errors.Is(err, collection.ErrActiveProfileRequired) || errors.Is(err, collection.ErrForbidden) ||
			errors.Is(err, collection.ErrNotFound) || errors.Is(err, collection.ErrInvalidInput) || errors.Is(err, collection.ErrProviderUnavailable) {
			handler.writeCollectionError(response, err)
		} else {
			handler.writeCatalogError(response, err)
		}
		return
	}
	start := query.StartIndex
	if start > len(titles) {
		start = len(titles)
	}
	end := start + query.Limit
	if end > len(titles) {
		end = len(titles)
	}
	items := make([]BaseItemDto, 0, end-start)
	for _, title := range titles[start:end] {
		items = append(items, handler.baseItemDTO(title, query.EnableUserData))
	}
	total := len(titles)
	if more {
		minimum := query.StartIndex + len(items) + 1
		if total < minimum {
			total = minimum
		}
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: items, TotalRecordCount: total, StartIndex: query.StartIndex})
}

func (handler *Handler) resolveCollectionWindow(ctx context.Context, principal auth.Principal, resolver collectionItemResolver, value collection.Collection, mediaTypes []string, query ItemQuery, sortBy, sortOrder string) ([]watchstate.CatalogTitle, bool, error) {
	target := query.StartIndex + query.Limit + 1
	allowed := stringSet(mediaTypes)
	idFilter := stringSet(query.Ids)
	search := strings.ToLower(query.SearchTerm)
	capacity := target
	if capacity > maximumCollectionResolveLimit {
		capacity = maximumCollectionResolveLimit
	}
	seen := make(map[string]struct{}, capacity)
	titles := make([]watchstate.CatalogTitle, 0, capacity)
	providerFailed := false
	for _, folder := range value.Folders {
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
					titles = append(titles, outcome.title)
					if sortBy != "sortname" && len(titles) >= target {
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
	if sortBy == "sortname" {
		sort.SliceStable(titles, func(left, right int) bool {
			leftTitle := strings.ToLower(titles[left].Title)
			rightTitle := strings.ToLower(titles[right].Title)
			if sortOrder == "descending" {
				return leftTitle > rightTitle
			}
			return leftTitle < rightTitle
		})
	}
	return titles, false, nil
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

func (handler *Handler) collectionFolderDTO(value collection.Collection, folder collection.Folder) BaseItemDto {
	return BaseItemDto{
		Id: folder.ID, ServerId: handler.serverInfo.ID.String(), Name: folder.Title, SortName: folder.Title,
		Etag: folder.ID, DisplayPreferencesId: folder.ID, LocationType: "FileSystem",
		Type: "Folder", MediaType: "Unknown", IsFolder: true, ParentId: value.ID,
		Genres: []string{}, ImageTags: map[string]string{}, BackdropImageTags: []string{}, UserData: &UserItemDataDto{Key: folder.ID, ItemId: folder.ID},
	}
}

func (handler *Handler) collectionFolderDetailDTO(ctx context.Context, principal auth.Principal, value collection.Collection, folder collection.Folder) BaseItemDto {
	folders := []collection.Folder{folder}
	handler.hydrateCollectionFolderCovers(ctx, principal, value.ID, folders)
	item := handler.collectionFolderDTO(value, folders[0])
	localizer, ok := handler.catalog.(catalogArtworkLocalizer)
	if !ok || strings.TrimSpace(folders[0].CoverImageURL) == "" {
		return item
	}
	localized := localizer.LocalizeArtworkURLs(ctx, []string{folders[0].CoverImageURL})
	if len(localized) == 1 {
		if tag, valid := localizedArtworkTag(localized[0]); valid {
			item.ImageTags["Primary"] = tag
		}
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
	if upstream == "" && len(value.Folders) != 0 {
		folder := []collection.Folder{value.Folders[0]}
		handler.hydrateCollectionFolderCovers(ctx, principal, value.ID, folder)
		upstream = strings.TrimSpace(folder[0].CoverImageURL)
	}
	if upstream == "" {
		return imageTags, backdropImageTags
	}
	localized := localizer.LocalizeArtworkURLs(ctx, []string{upstream})
	if len(localized) != 1 {
		return imageTags, backdropImageTags
	}
	tag, valid := localizedArtworkTag(localized[0])
	if !valid {
		return imageTags, backdropImageTags
	}
	imageTags["Primary"] = tag
	if fromBackdrop {
		backdropImageTags = append(backdropImageTags, tag)
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
			strings.EqualFold(value, "UserView"), strings.EqualFold(value, "Video"),
			strings.EqualFold(value, "Year"):
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

func catalogSort(query ItemQuery, request *http.Request) (string, string, error) {
	if len(query.SortBy) == 0 {
		if _, _, err := queryScalar(request.URL.Query(), "SortOrder"); err != nil {
			return "", "", ErrInvalidQuery
		}
		return "", "", nil
	}
	onlySortName := true
	for _, value := range query.SortBy {
		switch {
		case strings.EqualFold(value, "SortName"), strings.EqualFold(value, "Name"):
		case standardJellyfinSort(value):
			onlySortName = false
		default:
			return "", "", ErrInvalidQuery
		}
	}
	if onlySortName {
		return "sortname", strings.ToLower(query.SortOrder), nil
	}
	return "", "", nil
}

func standardJellyfinSort(value string) bool {
	switch {
	case strings.EqualFold(value, "Default"), strings.EqualFold(value, "AiredEpisodeOrder"),
		strings.EqualFold(value, "Album"), strings.EqualFold(value, "AlbumArtist"),
		strings.EqualFold(value, "Artist"), strings.EqualFold(value, "Budget"),
		strings.EqualFold(value, "CommunityRating"), strings.EqualFold(value, "CriticRating"),
		strings.EqualFold(value, "DateCreated"), strings.EqualFold(value, "DateLastContentAdded"),
		strings.EqualFold(value, "DatePlayed"), strings.EqualFold(value, "DigitalReleaseDate"),
		strings.EqualFold(value, "IsFavoriteOrLiked"), strings.EqualFold(value, "IsFolder"),
		strings.EqualFold(value, "IsPlayed"), strings.EqualFold(value, "IsUnplayed"),
		strings.EqualFold(value, "OfficialRating"), strings.EqualFold(value, "PlayCount"),
		strings.EqualFold(value, "PremiereDate"), strings.EqualFold(value, "ProductionYear"),
		strings.EqualFold(value, "Random"), strings.EqualFold(value, "Revenue"),
		strings.EqualFold(value, "Runtime"), strings.EqualFold(value, "SeriesDatePlayed"),
		strings.EqualFold(value, "SeriesSortName"), strings.EqualFold(value, "StartDate"),
		strings.EqualFold(value, "VideoBitRate"), strings.EqualFold(value, "AirTime"),
		strings.EqualFold(value, "Studio"), strings.EqualFold(value, "ParentIndexNumber"),
		strings.EqualFold(value, "IndexNumber"), strings.EqualFold(value, "SimilarityScore"),
		strings.EqualFold(value, "SearchScore"), strings.EqualFold(value, "ChannelOrder"),
		strings.EqualFold(value, "CatalogOrder"), strings.EqualFold(value, "DisplayOrder"),
		strings.EqualFold(value, "PopularityAllTime"), strings.EqualFold(value, "PopularityDay"),
		strings.EqualFold(value, "PopularityWeek"), strings.EqualFold(value, "PopularityMonth"),
		strings.EqualFold(value, "TrendingWeek"), strings.EqualFold(value, "TrendingMonth"):
		return true
	default:
		return false
	}
}

func (handler *Handler) baseItemDTO(title watchstate.CatalogTitle, includeUserData bool) BaseItemDto {
	item := BaseItemDto{
		Id: title.ID, ServerId: handler.serverInfo.ID.String(), Name: title.Title, SortName: title.Title,
		Etag: title.ID, LocationType: "FileSystem",
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
		item.Type, item.MediaType, item.IsFolder = "Series", "Unknown", true
	case "season":
		item.Type, item.MediaType, item.IsFolder = "Season", "Unknown", true
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
		userData := &UserItemDataDto{IsFavorite: title.InLibrary, Key: title.ID, ItemId: title.ID}
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
	if item.Type == "Movie" || item.Type == "Episode" {
		streamPath := "/Videos/" + url.PathEscape(item.Id) + "/stream"
		item.Path = streamPath + ".strm"
		item.MediaSources = []MediaSourceInfo{{
			Id: item.Id, Name: item.Name, Path: streamPath, Protocol: "File", Type: "Default",
			IsRemote: false, SupportsDirectPlay: true, SupportsDirectStream: true, SupportsTranscoding: true,
			RunTimeTicks: item.RunTimeTicks,
		}}
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
