package jellyfin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/collection"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

const (
	collectionCompatID         = "55555555-5555-4555-8555-555555555555"
	foreignCollectionCompatID  = "66666666-6666-4666-8666-666666666666"
	collectionMovieID          = "77777777-7777-4777-8777-777777777777"
	collectionSeriesID         = "88888888-8888-4888-8888-888888888888"
	collectionExtraMovieID     = "99999999-9999-4999-8999-999999999999"
	collectionHydratedCoverURL = "https://image.tmdb.org/hydrated-collection-cover.jpg"
)

type collectionCompatStore struct {
	mu      sync.Mutex
	titles  map[string]watchstate.CatalogTitle
	resolve []watchstate.ResolveTitleInput
	reads   []string
}

func (store *collectionCompatStore) GetCatalogTitle(_ context.Context, _ auth.Principal, id string) (watchstate.CatalogTitle, error) {
	store.mu.Lock()
	store.reads = append(store.reads, id)
	store.mu.Unlock()
	title, ok := store.titles[id]
	if !ok {
		return watchstate.CatalogTitle{}, watchstate.ErrNotFound
	}
	return title, nil
}

func (*collectionCompatStore) GetCatalogTitles(context.Context, auth.Principal, []string) ([]watchstate.CatalogTitle, error) {
	return nil, errors.New("unexpected batch read")
}

func (*collectionCompatStore) ListCatalogItems(context.Context, auth.Principal, watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
	return watchstate.CatalogPage{}, errors.New("unexpected ordinary catalog list")
}

func (store *collectionCompatStore) ResolveLinkedCatalogTitle(_ context.Context, _ auth.Principal, input watchstate.ResolveTitleInput) (watchstate.TitleReference, error) {
	store.mu.Lock()
	store.resolve = append(store.resolve, input)
	store.mu.Unlock()
	var id string
	switch input.Title {
	case "Canonical movie", "Duplicate movie":
		id = collectionMovieID
	case "Add-on series":
		id = collectionSeriesID
	case "Window sentinel":
		id = collectionExtraMovieID
	default:
		return watchstate.TitleReference{}, watchstate.ErrInvalidInput
	}
	return watchstate.TitleReference{TitleID: id}, nil
}

func (store *collectionCompatStore) resolvedInputs() []watchstate.ResolveTitleInput {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]watchstate.ResolveTitleInput(nil), store.resolve...)
}

type collectionCompatService struct {
	authorized collection.Collection
	foreign    collection.Collection
	listErr    error
	callsMu    sync.Mutex
	calls      []collectionResolveCall
}

type collectionResolveCall struct {
	collectionID string
	folderID     string
	page         int
	limit        int
}

func (service *collectionCompatService) List(context.Context, auth.Principal) ([]collection.Collection, error) {
	if service.listErr != nil {
		return nil, service.listErr
	}
	return []collection.Collection{service.authorized}, nil
}

func (service *collectionCompatService) Get(_ context.Context, _ auth.Principal, id string) (collection.Collection, error) {
	switch id {
	case service.authorized.ID:
		return service.authorized, nil
	case service.foreign.ID:
		return collection.Collection{}, collection.ErrNotFound
	default:
		return collection.Collection{}, collection.ErrNotFound
	}
}

func (service *collectionCompatService) ResolveFolder(_ context.Context, _ auth.Principal, collectionID, folderID string, page, limit int, _, _ string) (collection.ResolvedFolder, error) {
	service.callsMu.Lock()
	service.calls = append(service.calls, collectionResolveCall{collectionID: collectionID, folderID: folderID, page: page, limit: limit})
	service.callsMu.Unlock()
	if collectionID != service.authorized.ID {
		return collection.ResolvedFolder{}, collection.ErrNotFound
	}
	if page != 1 {
		if folderID == service.authorized.Folders[1].ID && page == 2 {
			return collection.ResolvedFolder{Page: page}, nil
		}
		return collection.ResolvedFolder{}, collection.ErrNotFound
	}
	switch folderID {
	case service.authorized.Folders[0].ID:
		return collection.ResolvedFolder{Items: []collection.Item{
			{ID: "malformed", MediaType: collection.MediaTypeMovie, Title: "Malformed", ExternalIDs: map[string]string{}},
			{ID: "tmdb:42", MediaType: collection.MediaTypeMovie, Title: "Canonical movie", ExternalIDs: map[string]string{"tmdb": "42"}},
		}, Page: 1}, nil
	case service.authorized.Folders[1].ID:
		hydrated := service.authorized.Folders[1]
		hydrated.CoverImageURL = collectionHydratedCoverURL
		return collection.ResolvedFolder{Folder: hydrated, Items: []collection.Item{
			{ID: "tt0000042", MediaType: collection.MediaTypeMovie, Title: "Duplicate movie", ExternalIDs: map[string]string{"imdb": "tt0000042"}},
			{ID: "opaque-series", MediaType: collection.MediaTypeSeries, Title: "Add-on series", ExternalIDs: map[string]string{}, Sources: []collection.SourceReference{{Kind: collection.SourceKindAddonCatalog, AddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", CatalogID: "series", Title: "Trusted Add-on"}}},
			{ID: "tvdb:84", MediaType: collection.MediaTypeMovie, Title: "Window sentinel", ExternalIDs: map[string]string{"tvdb": "84"}},
		}, Page: 1, HasMore: true}, nil
	default:
		return collection.ResolvedFolder{}, collection.ErrNotFound
	}
}

func TestCollectionsExposeRootFoldersAndCanonicalItems(t *testing.T) {
	handler, service, store, token := newCollectionCompatHandler(t)
	coverURL := "https://provider.invalid/folder.png?token=FOLDER_SECRET"
	backdropURL := "https://provider.invalid/collection-backdrop.png?token=BACKDROP_SECRET"
	coverTag := strings.Repeat("a", 64)
	hydratedTag := strings.Repeat("b", 64)
	backdropTag := strings.Repeat("c", 64)
	localizedCover := localizedArtworkPrefix + coverTag
	localizedHydratedCover := localizedArtworkPrefix + hydratedTag
	localizedBackdrop := localizedArtworkPrefix + backdropTag
	service.authorized.Folders[0].CoverImageURL = coverURL
	presenter := &searchArtworkPresenter{
		localized:  map[string]string{coverURL: localizedCover, collectionHydratedCoverURL: localizedHydratedCover, backdropURL: localizedBackdrop},
		registered: map[string]string{localizedCover: coverTag, localizedHydratedCover: hydratedTag, localizedBackdrop: backdropTag},
	}
	handler.catalog.(*catalogReader).artwork = presenter
	handler.artwork = presenter
	views, ok := handler.virtualViews()
	if !ok || len(views) != 3 || views[2].Name != "Collections" || views[2].CollectionType != "boxsets" {
		t.Fatalf("unexpected collection view: %+v", views)
	}

	viewsRequest := authenticatedCatalogRequest(t, token, "/UserViews?UserId="+catalogTestProfileID)
	viewsResponse := httptest.NewRecorder()
	handler.handleViews(viewsResponse, viewsRequest)
	var namedViews QueryResult[BaseItemDto]
	decodeCatalogResponse(t, viewsResponse, &namedViews)
	if viewsResponse.Code != http.StatusOK || namedViews.TotalRecordCount != 3 || len(namedViews.Items) != 3 || namedViews.Items[2].Id != collectionCompatID || namedViews.Items[2].Type != "CollectionFolder" {
		t.Fatalf("promoted collection views status=%d result=%+v", viewsResponse.Code, namedViews)
	}
	for _, view := range namedViews.Items {
		if view.UserData == nil || view.UserData.ItemId != view.Id {
			t.Fatalf("view identity is incomplete: %+v", view)
		}
	}

	userRequest := authenticatedCatalogRequest(t, token, "/Users/Me")
	userResponse := httptest.NewRecorder()
	handler.handleCurrentUser(userResponse, userRequest)
	var user UserDto
	decodeCatalogResponse(t, userResponse, &user)
	if userResponse.Code != http.StatusOK || len(user.Configuration.MyMediaExcludes) != 0 ||
		len(user.Configuration.OrderedViews) != 3 || user.Configuration.OrderedViews[2] != collectionCompatID {
		t.Fatalf("collection view configuration status=%d config=%+v", userResponse.Code, user.Configuration)
	}

	rootRequest := authenticatedCatalogRequest(t, token, "/Items?ParentId="+views[2].Id+"&IncludeItemTypes=BoxSet")
	rootResponse := httptest.NewRecorder()
	handler.handleItems(rootResponse, rootRequest)
	var root QueryResult[BaseItemDto]
	decodeCatalogResponse(t, rootResponse, &root)
	if rootResponse.Code != http.StatusOK || root.TotalRecordCount != 1 || len(root.Items) != 1 ||
		root.Items[0].Id != collectionCompatID || root.Items[0].Type != "BoxSet" || root.Items[0].MediaType != "Unknown" || !root.Items[0].IsFolder ||
		root.Items[0].Etag != collectionCompatID || root.Items[0].DisplayPreferencesId != collectionCompatID || root.Items[0].LocationType != "FileSystem" ||
		root.Items[0].ImageTags["Primary"] != coverTag || root.Items[0].UserData == nil || root.Items[0].UserData.Key != collectionCompatID || root.Items[0].UserData.ItemId != collectionCompatID {
		t.Fatalf("unexpected authorized boxsets: status=%d result=%+v", rootResponse.Code, root)
	}
	boxSetImageRequest := authenticatedCatalogRequest(t, token, "/Items/"+collectionCompatID+"/Images/Primary")
	boxSetImageRequest.SetPathValue("id", collectionCompatID)
	boxSetImageRequest.SetPathValue("type", "Primary")
	boxSetImageResponse := httptest.NewRecorder()
	handler.handleImage(boxSetImageResponse, boxSetImageRequest)
	if boxSetImageResponse.Code != http.StatusOK || len(presenter.served) != 1 || presenter.served[0] != coverTag {
		t.Fatalf("BoxSet artwork status=%d served=%v", boxSetImageResponse.Code, presenter.served)
	}
	presenter.served = nil
	missingBackdropRequest := authenticatedCatalogRequest(t, token, "/Items/"+collectionCompatID+"/Images/Backdrop")
	missingBackdropRequest.SetPathValue("id", collectionCompatID)
	missingBackdropRequest.SetPathValue("type", "Backdrop")
	missingBackdropResponse := httptest.NewRecorder()
	handler.handleImage(missingBackdropResponse, missingBackdropRequest)
	if missingBackdropResponse.Code != http.StatusNotFound || len(presenter.served) != 0 {
		t.Fatalf("missing BoxSet backdrop status=%d served=%v", missingBackdropResponse.Code, presenter.served)
	}
	if strings.Contains(rootResponse.Body.String(), service.foreign.Title) || strings.Contains(rootResponse.Body.String(), service.foreign.ID) {
		t.Fatalf("foreign collection leaked from root: %s", rootResponse.Body.String())
	}
	for _, body := range []string{viewsResponse.Body.String(), rootResponse.Body.String()} {
		if strings.Contains(body, "provider.invalid") || strings.Contains(body, "FOLDER_SECRET") {
			t.Fatalf("collection payload leaked upstream artwork: %s", body)
		}
	}

	latestRequest := authenticatedCatalogRequest(t, token, "/Items/Latest?ParentId="+collectionCompatID+"&Limit=16")
	latestResponse := httptest.NewRecorder()
	handler.handleLatestItems(latestResponse, latestRequest)
	var latest []BaseItemDto
	decodeCatalogResponse(t, latestResponse, &latest)
	if latestResponse.Code != http.StatusOK || len(latest) != 2 || latest[0].Name != "First" || latest[1].Name != "Second" ||
		latest[0].Type != "Folder" || latest[0].MediaType != "Unknown" || latest[0].ParentId != collectionCompatID || latest[0].ImageTags["Primary"] != coverTag ||
		latest[0].Etag != latest[0].Id || latest[0].DisplayPreferencesId != latest[0].Id || latest[0].LocationType != "FileSystem" || latest[0].UserData == nil ||
		latest[0].UserData.ItemId != latest[0].Id || latest[1].ImageTags["Primary"] != hydratedTag {
		t.Fatalf("collection latest folders status=%d result=%+v", latestResponse.Code, latest)
	}
	if len(service.calls) != 1 || service.calls[0].folderID != service.authorized.Folders[1].ID || service.calls[0].limit != 1 {
		t.Fatalf("missing cover hydration calls=%+v", service.calls)
	}
	if strings.Contains(latestResponse.Body.String(), "provider.invalid") || strings.Contains(latestResponse.Body.String(), "FOLDER_SECRET") {
		t.Fatalf("collection folder cover leaked upstream URL: %s", latestResponse.Body.String())
	}
	service.calls = nil

	browseRequest := authenticatedCatalogRequest(t, token, "/Items?ParentId="+collectionCompatID+"&IncludeItemTypes=Movie,Series&StartIndex=0&Limit=2")
	browseResponse := httptest.NewRecorder()

	imageRequest := authenticatedCatalogRequest(t, token, "/Items/"+service.authorized.Folders[1].ID+"/Images/Primary?tag="+hydratedTag)
	imageRequest.SetPathValue("id", service.authorized.Folders[1].ID)
	imageRequest.SetPathValue("type", "Primary")
	imageResponse := httptest.NewRecorder()
	handler.handleImage(imageResponse, imageRequest)
	if imageResponse.Code != http.StatusOK || len(presenter.served) != 1 || presenter.served[0] != hydratedTag || len(service.calls) != 0 {
		t.Fatalf("authenticated folder tag artwork status=%d served=%v calls=%+v", imageResponse.Code, presenter.served, service.calls)
	}
	untaggedImageRequest := authenticatedCatalogRequest(t, token, "/Items/"+service.authorized.Folders[1].ID+"/Images/Primary")
	untaggedImageRequest.SetPathValue("id", service.authorized.Folders[1].ID)
	untaggedImageRequest.SetPathValue("type", "Primary")
	untaggedImageResponse := httptest.NewRecorder()
	handler.handleImage(untaggedImageResponse, untaggedImageRequest)
	if untaggedImageResponse.Code != http.StatusOK || len(presenter.served) != 2 || presenter.served[1] != hydratedTag ||
		len(service.calls) != 1 || service.calls[0].folderID != service.authorized.Folders[1].ID || service.calls[0].limit != 1 {
		t.Fatalf("hydrated folder artwork status=%d served=%v calls=%+v", untaggedImageResponse.Code, presenter.served, service.calls)
	}
	service.calls = nil
	handler.handleItems(browseResponse, browseRequest)
	var browse QueryResult[BaseItemDto]
	decodeCatalogResponse(t, browseResponse, &browse)
	if browseResponse.Code != http.StatusOK || browse.TotalRecordCount != 2 || len(browse.Items) != 2 ||
		browse.Items[0].Id != service.authorized.Folders[0].ID || browse.Items[1].Id != service.authorized.Folders[1].ID ||
		browse.Items[0].Type != "Folder" || len(service.calls) != 1 || service.calls[0].folderID != service.authorized.Folders[1].ID ||
		service.calls[0].limit != 1 {
		t.Fatalf("unexpected folder browse: status=%d result=%+v calls=%+v", browseResponse.Code, browse, service.calls)
	}
	service.calls = nil

	folderRequest := authenticatedCatalogRequest(t, token, "/Items?ParentId="+service.authorized.Folders[0].ID+"&SortBy=IsFolder,Filename&SortOrder=Ascending&Recursive=false&Limit=50")
	folderResponse := httptest.NewRecorder()
	handler.handleItems(folderResponse, folderRequest)
	var folderItems QueryResult[BaseItemDto]
	decodeCatalogResponse(t, folderResponse, &folderItems)
	if folderResponse.Code != http.StatusOK || folderItems.TotalRecordCount != 1 || len(folderItems.Items) != 1 ||
		folderItems.Items[0].Id != collectionMovieID || folderItems.Items[0].Type != "Movie" || len(service.calls) != 1 ||
		service.calls[0].folderID != service.authorized.Folders[0].ID || service.calls[0].limit != maximumCollectionResolveLimit {
		t.Fatalf("unexpected canonical folder items: status=%d result=%+v calls=%+v", folderResponse.Code, folderItems, service.calls)
	}
	if resolved := store.resolvedInputs(); len(resolved) != 1 || resolved[0].ExternalID != "42" || resolved[0].Provider != "tmdb" {
		t.Fatalf("folder identities were not canonicalized safely: %+v", resolved)
	}

	service.calls = nil
	folderDetail := authenticatedCatalogRequest(t, token, "/Items/"+service.authorized.Folders[1].ID)
	folderDetail.SetPathValue("id", service.authorized.Folders[1].ID)
	folderDetailResponse := httptest.NewRecorder()
	handler.handleItem(folderDetailResponse, folderDetail)
	var folderDetailItem BaseItemDto
	decodeCatalogResponse(t, folderDetailResponse, &folderDetailItem)
	if folderDetailResponse.Code != http.StatusOK || folderDetailItem.Type != "Folder" || folderDetailItem.ImageTags["Primary"] != hydratedTag ||
		len(service.calls) != 1 || service.calls[0].folderID != service.authorized.Folders[1].ID || service.calls[0].limit != 1 {
		t.Fatalf("folder detail status=%d item=%+v calls=%+v", folderDetailResponse.Code, folderDetailItem, service.calls)
	}

	service.authorized.BackdropImageURL = backdropURL
	detailRequest := authenticatedCatalogRequest(t, token, "/Items/"+collectionCompatID)
	detailRequest.SetPathValue("id", collectionCompatID)
	detailResponse := httptest.NewRecorder()
	handler.handleItem(detailResponse, detailRequest)
	var detail BaseItemDto
	decodeCatalogResponse(t, detailResponse, &detail)
	if detailResponse.Code != http.StatusOK || detail.Type != "CollectionFolder" || detail.CollectionType != "boxsets" || detail.ImageTags["Primary"] != backdropTag ||
		len(detail.BackdropImageTags) != 1 || detail.BackdropImageTags[0] != backdropTag {
		t.Fatalf("authorized collection view detail status=%d item=%+v body=%s", detailResponse.Code, detail, detailResponse.Body.String())
	}

	foreignRequest := authenticatedCatalogRequest(t, token, "/Items/"+foreignCollectionCompatID)
	foreignRequest.SetPathValue("id", foreignCollectionCompatID)
	foreignResponse := httptest.NewRecorder()
	handler.handleItem(foreignResponse, foreignRequest)
	if foreignResponse.Code != http.StatusNotFound || strings.Contains(foreignResponse.Body.String(), service.foreign.Title) {
		t.Fatalf("foreign boxset detail leaked status=%d body=%s", foreignResponse.Code, foreignResponse.Body.String())
	}
}

func TestCollectionsArePromotedAsDirectHomeViews(t *testing.T) {
	handler, service, _, token := newCollectionCompatHandler(t)
	service.authorized.FolderCoverShape = collection.TileShapeLandscape
	for index := range service.authorized.Folders {
		service.authorized.Folders[index].TileShape = collection.TileShapePoster
	}
	coverURL := "https://provider.invalid/compatibility-client-landscape-cover.png"
	coverTag := strings.Repeat("d", 64)
	hydratedTag := strings.Repeat("e", 64)
	localizedCover := localizedArtworkPrefix + coverTag
	localizedHydrated := localizedArtworkPrefix + hydratedTag
	service.authorized.Folders[0].CoverImageURL = coverURL
	presenter := &searchArtworkPresenter{
		localized:  map[string]string{coverURL: localizedCover, collectionHydratedCoverURL: localizedHydrated},
		registered: map[string]string{localizedCover: coverTag, localizedHydrated: hydratedTag},
	}
	handler.catalog.(*catalogReader).artwork = presenter
	handler.artwork = presenter
	virtual, ok := handler.virtualViews()
	if !ok {
		t.Fatal("virtual views unavailable")
	}

	viewsRequest := authenticatedCatalogRequest(t, token, "/UserViews?UserId="+catalogTestProfileID)
	viewsResponse := httptest.NewRecorder()
	handler.handleViews(viewsResponse, viewsRequest)
	var views QueryResult[BaseItemDto]
	decodeCatalogResponse(t, viewsResponse, &views)
	if viewsResponse.Code != http.StatusOK || views.TotalRecordCount != 3 || len(views.Items) != 3 ||
		views.Items[0].Id != virtual[0].Id || views.Items[1].Id != virtual[1].Id ||
		views.Items[2].Id != collectionCompatID || views.Items[2].Id == virtual[2].Id ||
		views.Items[2].Type != "CollectionFolder" || views.Items[2].CollectionType != "homevideos" ||
		views.Items[2].PrimaryImageAspectRatio != 16.0/9.0 || views.Items[2].ImageTags["Thumb"] != coverTag ||
		len(views.Items[2].BackdropImageTags) != 1 || views.Items[2].BackdropImageTags[0] != coverTag ||
		views.Items[2].UserData == nil || views.Items[2].UserData.ItemId != collectionCompatID {
		t.Fatalf("promoted home views status=%d result=%+v", viewsResponse.Code, views)
	}

	userRequest := authenticatedCatalogRequest(t, token, "/Users/Me")
	userResponse := httptest.NewRecorder()
	handler.handleCurrentUser(userResponse, userRequest)
	var user UserDto
	decodeCatalogResponse(t, userResponse, &user)
	if userResponse.Code != http.StatusOK || len(user.Configuration.OrderedViews) != 3 ||
		user.Configuration.OrderedViews[2] != collectionCompatID || len(user.Configuration.MyMediaExcludes) != 0 {
		t.Fatalf("ordered home views status=%d config=%+v", userResponse.Code, user.Configuration)
	}

	latestRequest := authenticatedCatalogRequest(t, token, "/Users/"+catalogTestProfileID+"/Items/Latest?ParentId="+collectionCompatID+"&Fields=BasicSyncInfo,Overview,CollectionType,UserData&Recursive=true&MediaTypes=Video&Limit=20&IsPlayed=false")
	latestResponse := httptest.NewRecorder()
	handler.ServeHTTP(latestResponse, latestRequest)
	var latest []BaseItemDto
	decodeCatalogResponse(t, latestResponse, &latest)
	if latestResponse.Code != http.StatusOK || len(latest) != 2 || latest[0].Name != "First" || latest[1].Name != "Second" {
		t.Fatalf("home collection row status=%d items=%+v body=%s", latestResponse.Code, latest, latestResponse.Body.String())
	}
	for _, item := range latest {
		if item.Type != "CollectionFolder" || item.ParentId != collectionCompatID || item.CollectionType != "homevideos" ||
			item.PrimaryImageAspectRatio != 16.0/9.0 || item.ImageTags["Thumb"] == "" || len(item.BackdropImageTags) != 1 ||
			item.BackdropImageTags[0] != item.ImageTags["Thumb"] || item.UserData == nil || item.UserData.ItemId != item.Id {
			t.Fatalf("home row contains an invalid landscape folder: %+v", item)
		}
	}
	backdropRequest := authenticatedCatalogRequest(t, token, "/Items/"+latest[0].Id+"/Images/Backdrop/0")
	backdropRequest.SetPathValue("id", latest[0].Id)
	backdropRequest.SetPathValue("type", "Backdrop")
	backdropRequest.SetPathValue("index", "0")
	backdropResponse := httptest.NewRecorder()
	handler.handleIndexedImage(backdropResponse, backdropRequest)
	if backdropResponse.Code != http.StatusOK || len(presenter.served) != 1 || presenter.served[0] != latest[0].BackdropImageTags[0] {
		t.Fatalf("advertised folder backdrop status=%d served=%v item=%+v", backdropResponse.Code, presenter.served, latest[0])
	}
	detailRequest := authenticatedCatalogRequest(t, token, "/Users/"+catalogTestProfileID+"/Items/"+collectionCompatID)
	detailRequest.SetPathValue("userId", catalogTestProfileID)
	detailRequest.SetPathValue("itemId", collectionCompatID)
	detailResponse := httptest.NewRecorder()
	handler.handleUserItem(detailResponse, detailRequest)
	var detail BaseItemDto
	decodeCatalogResponse(t, detailResponse, &detail)
	if detailResponse.Code != http.StatusOK || detail.Type != "CollectionFolder" || detail.CollectionType != "homevideos" ||
		detail.PrimaryImageAspectRatio != 16.0/9.0 || detail.ImageTags["Thumb"] != coverTag ||
		len(detail.BackdropImageTags) != 1 || detail.BackdropImageTags[0] != coverTag {
		t.Fatalf("promoted collection detail status=%d item=%+v", detailResponse.Code, detail)
	}

	itemsRequest := authenticatedCatalogRequest(t, token, "/Users/"+catalogTestProfileID+"/Items?ParentId="+collectionCompatID+"&IncludeItemTypes=Movie,Series&Recursive=true&Limit=2")
	itemsRequest.SetPathValue("id", catalogTestProfileID)
	itemsResponse := httptest.NewRecorder()
	handler.handleUserItems(itemsResponse, itemsRequest)
	var items QueryResult[BaseItemDto]
	decodeCatalogResponse(t, itemsResponse, &items)
	if itemsResponse.Code != http.StatusOK || len(items.Items) != 2 || items.Items[0].Type == "Folder" || items.Items[1].Type == "Folder" {
		t.Fatalf("direct collection view status=%d result=%+v", itemsResponse.Code, items)
	}
}

func TestCollectionFolderAspectRatioUsesCollectionCoverShape(t *testing.T) {
	for _, test := range []struct {
		shape string
		want  float64
	}{
		{shape: collection.TileShapePoster, want: 2.0 / 3.0},
		{shape: collection.TileShapeLandscape, want: 16.0 / 9.0},
		{shape: collection.TileShapeSquare, want: 1},
		{shape: " LANDSCAPE ", want: 16.0 / 9.0},
		{shape: "unknown", want: 0},
	} {
		if got := collectionFolderAspectRatio(test.shape); got != test.want {
			t.Fatalf("shape %q ratio=%v want=%v", test.shape, got, test.want)
		}
	}
}

func TestCollectionFolderTypeFollowsCoverShape(t *testing.T) {
	handler := &Handler{}
	for _, test := range []struct {
		shape    string
		wantType string
	}{
		{shape: collection.TileShapeLandscape, wantType: "CollectionFolder"},
		{shape: collection.TileShapePoster, wantType: "Folder"},
		{shape: collection.TileShapeSquare, wantType: "Folder"},
	} {
		item := handler.collectionFolderDTO(collection.Collection{FolderCoverShape: test.shape}, collection.Folder{})
		if item.Type != test.wantType {
			t.Fatalf("shape=%q type=%q want=%q", test.shape, item.Type, test.wantType)
		}
	}
}

func TestCollectionViewTypeAndArtworkFollowSupportedCoverShape(t *testing.T) {
	for _, test := range []struct {
		shape          string
		collectionType string
		wantThumb      bool
	}{
		{shape: collection.TileShapePoster, collectionType: "boxsets"},
		{shape: collection.TileShapeLandscape, collectionType: "homevideos", wantThumb: true},
		{shape: collection.TileShapeSquare, collectionType: "boxsets"},
	} {
		if got := collectionViewType(test.shape); got != test.collectionType {
			t.Fatalf("collectionViewType(%q) = %q, want %q", test.shape, got, test.collectionType)
		}
		tags := collectionFolderImageTags(test.shape, "safe-tag")
		if tags["Primary"] != "safe-tag" || (tags["Thumb"] != "") != test.wantThumb {
			t.Fatalf("collectionFolderImageTags(%q) = %#v, wantThumb=%t", test.shape, tags, test.wantThumb)
		}
	}
}

func TestCollectionViewProjectionFallsBackTogether(t *testing.T) {
	handler, service, _, token := newCollectionCompatHandler(t)
	service.listErr = errors.New("collection store unavailable")

	viewsRequest := authenticatedCatalogRequest(t, token, "/UserViews?UserId="+catalogTestProfileID)
	viewsResponse := httptest.NewRecorder()
	handler.handleViews(viewsResponse, viewsRequest)
	var views QueryResult[BaseItemDto]
	decodeCatalogResponse(t, viewsResponse, &views)

	userRequest := authenticatedCatalogRequest(t, token, "/Users/Me")
	userResponse := httptest.NewRecorder()
	handler.handleCurrentUser(userResponse, userRequest)
	var user UserDto
	decodeCatalogResponse(t, userResponse, &user)

	if viewsResponse.Code != http.StatusOK || userResponse.Code != http.StatusOK || len(views.Items) != 2 ||
		len(user.Configuration.OrderedViews) != 2 || user.Configuration.OrderedViews[0] != views.Items[0].Id ||
		user.Configuration.OrderedViews[1] != views.Items[1].Id {
		t.Fatalf("collection fallback diverged: viewsStatus=%d views=%+v userStatus=%d config=%+v", viewsResponse.Code, views, userResponse.Code, user.Configuration)
	}
}

func TestRecursiveCollectionBrowseFlattensFolders(t *testing.T) {
	handler, service, _, token := newCollectionCompatHandler(t)
	request := authenticatedCatalogRequest(t, token, "/Items?ParentId="+collectionCompatID+"&IncludeItemTypes=Movie,Series&Recursive=true&EnableUserData=true&StartIndex=0&Limit=2")
	response := httptest.NewRecorder()
	handler.handleItems(response, request)

	var result QueryResult[BaseItemDto]
	decodeCatalogResponse(t, response, &result)
	if response.Code != http.StatusOK || result.TotalRecordCount != 3 || len(result.Items) != 2 ||
		result.Items[0].Id != collectionMovieID || result.Items[0].Type != "Movie" ||
		result.Items[1].Id != collectionSeriesID || result.Items[1].Type != "Series" {
		t.Fatalf("recursive collection browse was not flattened: status=%d result=%+v", response.Code, result)
	}
	for _, item := range result.Items {
		if item.Type == "Folder" || item.UserData == nil || item.UserData.ItemId != item.Id {
			t.Fatalf("recursive collection item is not client-compatible: %+v", item)
		}
	}
	if len(service.calls) != 2 || service.calls[0].folderID != service.authorized.Folders[0].ID ||
		service.calls[1].folderID != service.authorized.Folders[1].ID ||
		service.calls[0].limit != maximumCollectionResolveLimit || service.calls[1].limit != maximumCollectionResolveLimit {
		t.Fatalf("recursive collection did not resolve every folder: %+v", service.calls)
	}
}

func TestRecursiveCollectionBrowseSortsBeforePagination(t *testing.T) {
	handler, service, _, token := newCollectionCompatHandler(t)
	request := authenticatedCatalogRequest(t, token, "/Items?ParentId="+collectionCompatID+"&IncludeItemTypes=Movie,Series&Recursive=true&SortBy=SortName&SortOrder=Ascending&StartIndex=0&Limit=1")
	response := httptest.NewRecorder()
	handler.handleItems(response, request)

	var result QueryResult[BaseItemDto]
	decodeCatalogResponse(t, response, &result)
	if response.Code != http.StatusOK || result.TotalRecordCount != 3 || len(result.Items) != 1 ||
		result.Items[0].Id != collectionSeriesID || result.Items[0].Name != "Add-on series" {
		t.Fatalf("recursive collection was paginated before sorting: status=%d result=%+v", response.Code, result)
	}
	if len(service.calls) != 3 || service.calls[2].folderID != service.authorized.Folders[1].ID || service.calls[2].page != 2 {
		t.Fatalf("sorted collection did not resolve the complete candidate set: %+v", service.calls)
	}
}

func TestBoxSetFilterIsRootOnlyAndIncompatibleFiltersAreEmpty(t *testing.T) {
	handler, _, _, token := newCollectionCompatHandler(t)
	views, _ := handler.virtualViews()

	rootRequest := authenticatedCatalogRequest(t, token, "/Items?IncludeItemTypes=BoxSet")
	rootResponse := httptest.NewRecorder()
	handler.handleItems(rootResponse, rootRequest)
	var root QueryResult[BaseItemDto]
	decodeCatalogResponse(t, rootResponse, &root)
	if rootResponse.Code != http.StatusOK || len(root.Items) != 1 || root.Items[0].Type != "BoxSet" {
		t.Fatalf("BoxSet selector did not list collection root: status=%d result=%+v", rootResponse.Code, root)
	}

	incompatibleRequest := authenticatedCatalogRequest(t, token, "/Items?ParentId="+views[2].Id+"&IncludeItemTypes=Movie")
	incompatibleResponse := httptest.NewRecorder()
	handler.handleItems(incompatibleResponse, incompatibleRequest)
	var incompatible QueryResult[BaseItemDto]
	decodeCatalogResponse(t, incompatibleResponse, &incompatible)
	if incompatibleResponse.Code != http.StatusOK || incompatible.TotalRecordCount != 0 || len(incompatible.Items) != 0 {
		t.Fatalf("incompatible collection filter was not empty: status=%d result=%+v", incompatibleResponse.Code, incompatible)
	}
}

func TestCollectionCompatibilityErrorsAreScrubbedAndMapped(t *testing.T) {
	handler := &Handler{}
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "not found", err: collection.ErrNotFound, status: http.StatusNotFound},
		{name: "invalid", err: collection.ErrInvalidInput, status: http.StatusBadRequest},
		{name: "forbidden", err: collection.ErrForbidden, status: http.StatusUnauthorized},
		{name: "profile required", err: collection.ErrActiveProfileRequired, status: http.StatusUnauthorized},
		{name: "provider", err: collection.ErrProviderUnavailable, status: http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.writeCollectionError(response, test.err)
			if response.Code != test.status || strings.Contains(response.Body.String(), test.err.Error()) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func newCollectionCompatHandler(t *testing.T) (*Handler, *collectionCompatService, *collectionCompatStore, string) {
	t.Helper()
	serverID, err := ParseServerID(catalogTestServerID)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := newCompatCredential()
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	profileID := catalogTestProfileID
	session := AuthenticatedSession{
		ID: "22222222-2222-4222-8222-222222222222", ProfileID: profileID, ProfileName: "Viewer", ExpiresAt: expiresAt,
		Principal: auth.Principal{SessionID: "33333333-3333-4333-8333-333333333333", UserID: "44444444-4444-4444-8444-444444444444", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt},
	}
	service := &collectionCompatService{
		authorized: collection.Collection{ID: collectionCompatID, Title: "Authorized Collection", Folders: []collection.Folder{{ID: "11111111-1111-4111-8111-111111111110", Title: "First"}, {ID: "11111111-1111-4111-8111-111111111120", Title: "Second"}}},
		foreign:    collection.Collection{ID: foreignCollectionCompatID, Title: "FOREIGN COLLECTION SECRET"},
	}
	store := &collectionCompatStore{titles: map[string]watchstate.CatalogTitle{
		collectionMovieID:      {ID: collectionMovieID, MediaType: "movie", Title: "Canonical movie", Genres: []string{}, ProviderIDs: map[string]string{"tmdb": "42"}},
		collectionSeriesID:     {ID: collectionSeriesID, MediaType: "series", Title: "Add-on series", Genres: []string{}, ProviderIDs: map[string]string{}},
		collectionExtraMovieID: {ID: collectionExtraMovieID, MediaType: "movie", Title: "Window sentinel", Genres: []string{}, ProviderIDs: map[string]string{"tvdb": "84"}},
	}}
	reader, err := NewCatalogReader(store, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Dependencies{ServerInfo: ServerInfo{ID: serverID, Name: "Rivune"}, Authentication: &catalogHTTPAuthentication{session: session}, Catalog: reader, Collections: service})
	if err != nil {
		t.Fatal(err)
	}
	return handler, service, store, token
}
