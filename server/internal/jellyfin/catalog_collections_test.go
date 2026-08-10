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
	mu          sync.Mutex
	titles      map[string]watchstate.CatalogTitle
	resolve     []watchstate.ResolveTitleInput
	reads       []string
	listPage    *watchstate.CatalogPage
	listQueries []watchstate.CatalogQuery
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

func (store *collectionCompatStore) ListCatalogItems(_ context.Context, _ auth.Principal, query watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
	store.listQueries = append(store.listQueries, query)
	if store.listPage != nil {
		return *store.listPage, nil
	}
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
	listed     []collection.Collection
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
	if service.listed != nil {
		return append([]collection.Collection(nil), service.listed...), nil
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
	promotedID, err := handler.collectionViewID(collectionCompatID)
	if err != nil {
		t.Fatal(err)
	}
	service.authorized.FolderCoverShape = collection.TileShapePoster
	coverURL := "https://provider.invalid/folder.png?token=FOLDER_SECRET"
	logoURL := "https://provider.invalid/logo.png?token=LOGO_SECRET"
	backdropURL := "https://provider.invalid/collection-backdrop.png?token=BACKDROP_SECRET"
	coverTag := strings.Repeat("a", 64)
	hydratedTag := strings.Repeat("b", 64)
	backdropTag := strings.Repeat("c", 64)
	logoTag := strings.Repeat("d", 64)
	localizedCover := localizedArtworkPrefix + coverTag
	localizedHydratedCover := localizedArtworkPrefix + hydratedTag
	localizedBackdrop := localizedArtworkPrefix + backdropTag
	localizedLogo := localizedArtworkPrefix + logoTag
	service.authorized.Folders[0].CoverImageURL = coverURL
	service.authorized.Folders[0].TitleLogoURL = logoURL
	presenter := &searchArtworkPresenter{
		localized:  map[string]string{coverURL: localizedCover, collectionHydratedCoverURL: localizedHydratedCover, backdropURL: localizedBackdrop, logoURL: localizedLogo},
		registered: map[string]string{localizedCover: coverTag, localizedHydratedCover: hydratedTag, localizedBackdrop: backdropTag, localizedLogo: logoTag},
	}
	handler.catalog.(*catalogReader).artwork = presenter
	handler.artwork = presenter
	views, ok := handler.virtualViews()
	if !ok || len(views) != 3 || views[2].Name != "Collections" || views[2].CollectionType != "boxsets" {
		t.Fatalf("unexpected collection view: %+v", views)
	}
	anonymousImage := func(itemID, imageType, tag string) *httptest.ResponseRecorder {
		target := "/Items/" + itemID + "/Images/" + imageType
		if tag != "" {
			target += "?tag=" + tag
		}
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.SetPathValue("id", itemID)
		request.SetPathValue("type", imageType)
		response := httptest.NewRecorder()
		handler.handleImage(response, request)
		return response
	}
	if response := anonymousImage(promotedID, "Primary", ""); response.Code != http.StatusUnauthorized || len(presenter.served) != 0 {
		t.Fatalf("unprojected collection view artwork status=%d served=%v", response.Code, presenter.served)
	}

	viewsRequest := authenticatedCatalogRequest(t, token, "/UserViews?UserId="+catalogTestProfileID+"&Fields=PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb,Logo")
	viewsResponse := httptest.NewRecorder()
	handler.handleViews(viewsResponse, viewsRequest)
	var namedViews QueryResult[BaseItemDto]
	decodeCatalogResponse(t, viewsResponse, &namedViews)
	if viewsResponse.Code != http.StatusOK || namedViews.TotalRecordCount != 1 || len(namedViews.Items) != 1 || namedViews.Items[0].Id != promotedID || namedViews.Items[0].Type != "CollectionFolder" {
		t.Fatalf("promoted collection views status=%d result=%+v", viewsResponse.Code, namedViews)
	}
	for _, view := range namedViews.Items {
		if view.UserData == nil || view.UserData.ItemId != view.Id {
			t.Fatalf("view identity is incomplete: %+v", view)
		}
		if view.ImageTags["Logo"] != logoTag {
			t.Fatalf("view logo projection is incomplete: %+v", view.ImageTags)
		}
	}
	for index, imageType := range []string{"Primary", "Thumb", "Backdrop", "Logo"} {
		wantTag := coverTag
		if imageType == "Logo" {
			wantTag = logoTag
		}
		response := anonymousImage(promotedID, imageType, wantTag)
		if response.Code != http.StatusOK || len(presenter.served) != index+1 || presenter.served[index] != wantTag {
			t.Fatalf("anonymous projected collection view %s artwork status=%d served=%v", imageType, response.Code, presenter.served)
		}
	}
	metadataRequest := authenticatedCatalogRequest(t, token, "/Items/"+promotedID+"/Images")
	metadataResponse := httptest.NewRecorder()
	handler.ServeHTTP(metadataResponse, metadataRequest)
	var metadata []ImageInfo
	decodeCatalogResponse(t, metadataResponse, &metadata)
	if metadataResponse.Code != http.StatusOK || len(metadata) != 4 || metadata[2].ImageType != "Logo" || metadata[2].ImageTag != logoTag {
		t.Fatalf("collection image metadata status=%d result=%+v", metadataResponse.Code, metadata)
	}
	presenter.served = nil
	if response := anonymousImage(service.foreign.ID, "Primary", ""); response.Code != http.StatusUnauthorized || len(presenter.served) != 0 {
		t.Fatalf("foreign collection artwork status=%d served=%v", response.Code, presenter.served)
	}

	userRequest := authenticatedCatalogRequest(t, token, "/Users/Me")
	userResponse := httptest.NewRecorder()
	handler.handleCurrentUser(userResponse, userRequest)
	var user UserDto
	decodeCatalogResponse(t, userResponse, &user)
	if userResponse.Code != http.StatusOK || len(user.Configuration.MyMediaExcludes) != 0 ||
		len(user.Configuration.OrderedViews) != 1 || user.Configuration.OrderedViews[0] != promotedID {
		t.Fatalf("collection view configuration status=%d config=%+v", userResponse.Code, user.Configuration)
	}

	rootRequest := authenticatedCatalogRequest(t, token, "/Items?ParentId="+views[2].Id+"&IncludeItemTypes=BoxSet&Fields=DisplayPreferencesId,Etag,PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb")
	rootResponse := httptest.NewRecorder()
	handler.handleItems(rootResponse, rootRequest)
	var root QueryResult[BaseItemDto]
	decodeCatalogResponse(t, rootResponse, &root)
	if rootResponse.Code != http.StatusOK || root.TotalRecordCount != 1 || len(root.Items) != 1 ||
		root.Items[0].Id != collectionCompatID || root.Items[0].Type != "BoxSet" || root.Items[0].MediaType != "Unknown" || !root.Items[0].IsFolder ||
		root.Items[0].Etag != collectionCompatID || root.Items[0].DisplayPreferencesId != collectionCompatID || root.Items[0].LocationType != "FileSystem" ||
		root.Items[0].PrimaryImageAspectRatio == 16.0/9.0 || root.Items[0].CollectionType != "" || root.Items[0].ImageTags["Primary"] != coverTag ||
		root.Items[0].ImageTags["Thumb"] != "" || len(root.Items[0].BackdropImageTags) != 0 || root.Items[0].UserData == nil ||
		root.Items[0].UserData.Key != collectionCompatID || root.Items[0].UserData.ItemId != collectionCompatID {
		t.Fatalf("unexpected authorized boxsets: status=%d result=%+v", rootResponse.Code, root)
	}
	if root.Items[0].Id == promotedID {
		t.Fatalf("BoxSet and promoted CollectionFolder identities collided: boxset=%q promoted=%q", root.Items[0].Id, promotedID)
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
	fallbackBackdropRequest := authenticatedCatalogRequest(t, token, "/Items/"+collectionCompatID+"/Images/Backdrop")
	fallbackBackdropRequest.SetPathValue("id", collectionCompatID)
	fallbackBackdropRequest.SetPathValue("type", "Backdrop")
	fallbackBackdropResponse := httptest.NewRecorder()
	handler.handleImage(fallbackBackdropResponse, fallbackBackdropRequest)
	if fallbackBackdropResponse.Code != http.StatusOK || len(presenter.served) != 1 || presenter.served[0] != coverTag {
		t.Fatalf("fallback BoxSet backdrop status=%d served=%v", fallbackBackdropResponse.Code, presenter.served)
	}
	boxSetMetadataRequest := authenticatedCatalogRequest(t, token, "/Items/"+collectionCompatID+"/Images")
	boxSetMetadataResponse := httptest.NewRecorder()
	handler.ServeHTTP(boxSetMetadataResponse, boxSetMetadataRequest)
	var boxSetMetadata []ImageInfo
	decodeCatalogResponse(t, boxSetMetadataResponse, &boxSetMetadata)
	foundBackdrop := false
	for _, info := range boxSetMetadata {
		if info.ImageType == "Backdrop" && info.ImageTag == coverTag {
			foundBackdrop = true
		}
	}
	if boxSetMetadataResponse.Code != http.StatusOK || !foundBackdrop {
		t.Fatalf("fallback BoxSet metadata status=%d result=%+v", boxSetMetadataResponse.Code, boxSetMetadata)
	}
	for _, imageType := range []string{"Primary", "Thumb", "Backdrop"} {
		promotedImageRequest := authenticatedCatalogRequest(t, token, "/Items/"+promotedID+"/Images/"+imageType)
		promotedImageRequest.SetPathValue("id", promotedID)
		promotedImageRequest.SetPathValue("type", imageType)
		promotedImageResponse := httptest.NewRecorder()
		handler.handleImage(promotedImageResponse, promotedImageRequest)
		if promotedImageResponse.Code != http.StatusOK || presenter.served[len(presenter.served)-1] != coverTag {
			t.Fatalf("promoted collection %s artwork status=%d served=%v", imageType, promotedImageResponse.Code, presenter.served)
		}
	}
	if len(presenter.served) != 4 {
		t.Fatalf("promoted collection artwork did not serve every advertised form: %v", presenter.served)
	}
	presenter.served = nil
	if strings.Contains(rootResponse.Body.String(), service.foreign.Title) || strings.Contains(rootResponse.Body.String(), service.foreign.ID) {
		t.Fatalf("foreign collection leaked from root: %s", rootResponse.Body.String())
	}
	for _, body := range []string{viewsResponse.Body.String(), rootResponse.Body.String()} {
		if strings.Contains(body, "provider.invalid") || strings.Contains(body, "FOLDER_SECRET") {
			t.Fatalf("collection payload leaked upstream artwork: %s", body)
		}
	}

	latestRequest := authenticatedCatalogRequest(t, token, "/Items/Latest?ParentId="+collectionCompatID+"&Limit=16&Fields=DisplayPreferencesId,Etag,ParentId,PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb")
	latestResponse := httptest.NewRecorder()
	handler.handleLatestItems(latestResponse, latestRequest)
	var latest []BaseItemDto
	decodeCatalogResponse(t, latestResponse, &latest)
	if latestResponse.Code != http.StatusOK || len(latest) != 2 || latest[0].Name != "First" || latest[1].Name != "Second" ||
		latest[0].Type != "CollectionFolder" || latest[0].MediaType != "Unknown" || latest[0].CollectionType != "unknown" || latest[0].ParentId != promotedID ||
		latest[0].PrimaryImageAspectRatio != 16.0/9.0 || latest[0].ImageTags["Primary"] != coverTag || latest[0].ImageTags["Thumb"] != coverTag ||
		len(latest[0].BackdropImageTags) != 1 || latest[0].BackdropImageTags[0] != coverTag || latest[0].Etag != latest[0].Id ||
		latest[0].DisplayPreferencesId != latest[0].Id || latest[0].LocationType != "FileSystem" || latest[0].UserData == nil ||
		latest[0].UserData.ItemId != latest[0].Id || latest[1].ImageTags["Primary"] != hydratedTag || latest[1].ImageTags["Thumb"] != hydratedTag {
		t.Fatalf("collection latest folders status=%d result=%+v", latestResponse.Code, latest)
	}
	for index, imageType := range []string{"Primary", "Thumb", "Backdrop"} {
		response := anonymousImage(latest[0].Id, imageType, coverTag)
		if response.Code != http.StatusOK || len(presenter.served) != index+1 || presenter.served[index] != coverTag {
			t.Fatalf("anonymous projected collection folder %s artwork status=%d served=%v", imageType, response.Code, presenter.served)
		}
	}
	presenter.served = nil
	if len(service.calls) != 1 || service.calls[0].folderID != service.authorized.Folders[1].ID || service.calls[0].limit != 1 {
		t.Fatalf("missing cover hydration calls=%+v", service.calls)
	}
	if strings.Contains(latestResponse.Body.String(), "provider.invalid") || strings.Contains(latestResponse.Body.String(), "FOLDER_SECRET") {
		t.Fatalf("collection folder cover leaked upstream URL: %s", latestResponse.Body.String())
	}
	service.calls = nil

	browseRequest := authenticatedCatalogRequest(t, token, "/Items?ParentId="+promotedID+"&IncludeItemTypes=Movie,Series&StartIndex=0&Limit=2&Fields=PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb")
	browseResponse := httptest.NewRecorder()

	imageRequest := authenticatedCatalogRequest(t, token, "/Items/"+service.authorized.Folders[1].ID+"/Images/Primary?tag="+hydratedTag)
	imageRequest.SetPathValue("id", service.authorized.Folders[1].ID)
	imageRequest.SetPathValue("type", "Primary")
	imageResponse := httptest.NewRecorder()
	handler.handleImage(imageResponse, imageRequest)
	if imageResponse.Code != http.StatusOK || len(presenter.served) != 1 || presenter.served[0] != hydratedTag ||
		len(service.calls) != 1 || service.calls[0].folderID != service.authorized.Folders[1].ID || service.calls[0].limit != 1 {
		t.Fatalf("authenticated folder tag artwork status=%d served=%v calls=%+v", imageResponse.Code, presenter.served, service.calls)
	}
	untaggedImageRequest := authenticatedCatalogRequest(t, token, "/Items/"+service.authorized.Folders[1].ID+"/Images/Primary")
	untaggedImageRequest.SetPathValue("id", service.authorized.Folders[1].ID)
	untaggedImageRequest.SetPathValue("type", "Primary")
	untaggedImageResponse := httptest.NewRecorder()
	handler.handleImage(untaggedImageResponse, untaggedImageRequest)
	if untaggedImageResponse.Code != http.StatusOK || len(presenter.served) != 2 || presenter.served[1] != hydratedTag ||
		len(service.calls) != 2 || service.calls[1].folderID != service.authorized.Folders[1].ID || service.calls[1].limit != 1 {
		t.Fatalf("hydrated folder artwork status=%d served=%v calls=%+v", untaggedImageResponse.Code, presenter.served, service.calls)
	}
	service.calls = nil
	handler.handleItems(browseResponse, browseRequest)
	var browse QueryResult[BaseItemDto]
	decodeCatalogResponse(t, browseResponse, &browse)
	if browseResponse.Code != http.StatusOK || browse.TotalRecordCount != 2 || len(browse.Items) != 2 ||
		browse.Items[0].Id != service.authorized.Folders[0].ID || browse.Items[1].Id != service.authorized.Folders[1].ID ||
		browse.Items[0].Type != "CollectionFolder" || browse.Items[0].CollectionType != "unknown" ||
		browse.Items[0].PrimaryImageAspectRatio != 16.0/9.0 || browse.Items[0].ImageTags["Primary"] != coverTag ||
		browse.Items[0].ImageTags["Thumb"] != coverTag || len(browse.Items[0].BackdropImageTags) != 1 ||
		browse.Items[0].BackdropImageTags[0] != coverTag || browse.Items[1].Type != "CollectionFolder" ||
		browse.Items[1].CollectionType != "unknown" || browse.Items[1].PrimaryImageAspectRatio != 16.0/9.0 ||
		browse.Items[1].ImageTags["Primary"] != hydratedTag || browse.Items[1].ImageTags["Thumb"] != hydratedTag ||
		len(browse.Items[1].BackdropImageTags) != 1 || browse.Items[1].BackdropImageTags[0] != hydratedTag ||
		len(service.calls) != 1 || service.calls[0].folderID != service.authorized.Folders[1].ID || service.calls[0].limit != 1 {
		t.Fatalf("unexpected landscape folder browse: status=%d result=%+v calls=%+v", browseResponse.Code, browse, service.calls)
	}
	service.calls = nil

	folderRequest := authenticatedCatalogRequest(t, token, "/Items?ParentId="+service.authorized.Folders[0].ID+"&SortBy=Name&SortOrder=Ascending&Recursive=false&Limit=50")
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
	folderDetail := authenticatedCatalogRequest(t, token, "/Items/"+latest[1].Id+"?Fields=PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb")
	folderDetail.SetPathValue("id", latest[1].Id)
	folderDetailResponse := httptest.NewRecorder()
	handler.handleItem(folderDetailResponse, folderDetail)
	var folderDetailItem BaseItemDto
	decodeCatalogResponse(t, folderDetailResponse, &folderDetailItem)
	if folderDetailResponse.Code != http.StatusOK || folderDetailItem.Id != latest[1].Id || folderDetailItem.Type != "CollectionFolder" ||
		folderDetailItem.CollectionType != "unknown" || folderDetailItem.PrimaryImageAspectRatio != 16.0/9.0 ||
		folderDetailItem.ImageTags["Primary"] != hydratedTag || folderDetailItem.ImageTags["Thumb"] != hydratedTag ||
		len(folderDetailItem.BackdropImageTags) != 1 || folderDetailItem.BackdropImageTags[0] != hydratedTag ||
		len(service.calls) != 1 || service.calls[0].folderID != service.authorized.Folders[1].ID || service.calls[0].limit != 1 {
		t.Fatalf("folder detail status=%d item=%+v calls=%+v", folderDetailResponse.Code, folderDetailItem, service.calls)
	}

	service.authorized.BackdropImageURL = backdropURL
	detailRequest := authenticatedCatalogRequest(t, token, "/Items/"+promotedID+"?Fields=PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb")
	detailRequest.SetPathValue("id", promotedID)
	detailResponse := httptest.NewRecorder()
	handler.handleItem(detailResponse, detailRequest)
	var detail BaseItemDto
	decodeCatalogResponse(t, detailResponse, &detail)
	if detailResponse.Code != http.StatusOK || detail.Type != "CollectionFolder" || detail.CollectionType != "unknown" ||
		detail.PrimaryImageAspectRatio != 16.0/9.0 || detail.ImageTags["Primary"] != backdropTag || detail.ImageTags["Thumb"] != backdropTag ||
		len(detail.BackdropImageTags) != 1 || detail.BackdropImageTags[0] != backdropTag {
		t.Fatalf("authorized collection view detail status=%d item=%+v body=%s", detailResponse.Code, detail, detailResponse.Body.String())
	}
	legacyDetailRequest := authenticatedCatalogRequest(t, token, "/Items/"+collectionCompatID)
	legacyDetailRequest.SetPathValue("id", collectionCompatID)
	legacyDetailResponse := httptest.NewRecorder()
	handler.handleItem(legacyDetailResponse, legacyDetailRequest)
	var legacyDetail BaseItemDto
	decodeCatalogResponse(t, legacyDetailResponse, &legacyDetail)
	if legacyDetailResponse.Code != http.StatusOK || legacyDetail.Id != promotedID || legacyDetail.Type != "CollectionFolder" {
		t.Fatalf("legacy collection ID no longer resolves to promoted detail: status=%d item=%+v", legacyDetailResponse.Code, legacyDetail)
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
	handler, service, store, token := newCollectionCompatHandler(t)
	promotedID, err := handler.collectionViewID(collectionCompatID)
	if err != nil {
		t.Fatal(err)
	}
	service.authorized.FolderCoverShape = collection.TileShapePoster
	for index := range service.authorized.Folders {
		service.authorized.Folders[index].TileShape = collection.TileShapePoster
	}
	service.authorized.Folders[0].Sources = []collection.Source{{Kind: collection.SourceKindAddonCatalog, AddonCatalog: &collection.AddonCatalogSource{Type: collection.MediaTypeMovie}}}
	service.authorized.Folders[1].Sources = []collection.Source{{Kind: collection.SourceKindAddonCatalog, AddonCatalog: &collection.AddonCatalogSource{Type: collection.MediaTypeBoth}}}
	coverURL := "https://provider.invalid/compatibility-client-poster-cover.png"
	heroURL := "https://provider.invalid/compatibility-client-landscape-hero.png"
	coverTag := strings.Repeat("d", 64)
	heroTag := strings.Repeat("f", 64)
	hydratedTag := strings.Repeat("e", 64)
	localizedCover := localizedArtworkPrefix + coverTag
	localizedHero := localizedArtworkPrefix + heroTag
	localizedHydrated := localizedArtworkPrefix + hydratedTag
	service.authorized.Folders[0].CoverImageURL = coverURL
	service.authorized.Folders[0].HeroBackdropURL = heroURL
	presenter := &searchArtworkPresenter{
		localized:  map[string]string{coverURL: localizedCover, heroURL: localizedHero, collectionHydratedCoverURL: localizedHydrated},
		registered: map[string]string{localizedCover: coverTag, localizedHero: heroTag, localizedHydrated: hydratedTag},
	}
	handler.catalog.(*catalogReader).artwork = presenter
	handler.artwork = presenter
	virtual, ok := handler.virtualViews()
	if !ok {
		t.Fatal("virtual views unavailable")
	}

	viewsRequest := authenticatedCatalogRequest(t, token, "/UserViews?UserId="+catalogTestProfileID+"&Fields=PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb")
	viewsResponse := httptest.NewRecorder()
	handler.handleViews(viewsResponse, viewsRequest)
	var views QueryResult[BaseItemDto]
	decodeCatalogResponse(t, viewsResponse, &views)
	if viewsResponse.Code != http.StatusOK || views.TotalRecordCount != 1 || len(views.Items) != 1 ||
		views.Items[0].Id != promotedID || views.Items[0].Id == collectionCompatID || views.Items[0].Id == virtual[0].Id ||
		views.Items[0].Id == virtual[1].Id || views.Items[0].Id == virtual[2].Id ||
		views.Items[0].Type != "CollectionFolder" || views.Items[0].CollectionType != "movies" ||
		views.Items[0].PrimaryImageAspectRatio != 16.0/9.0 || views.Items[0].ImageTags["Thumb"] != heroTag ||
		len(views.Items[0].BackdropImageTags) != 1 || views.Items[0].BackdropImageTags[0] != heroTag ||
		views.Items[0].UserData == nil || views.Items[0].UserData.ItemId != promotedID {
		t.Fatalf("promoted home views status=%d result=%+v", viewsResponse.Code, views)
	}
	rootRequests := []*http.Request{
		authenticatedCatalogRequest(t, token, "/Items"),
		authenticatedCatalogRequest(t, token, "/Users/"+catalogTestProfileID+"/Items?IncludeItemTypes=CollectionFolder"),
	}
	rootRequests[1].SetPathValue("id", catalogTestProfileID)
	for index, rootRequest := range rootRequests {
		rootResponse := httptest.NewRecorder()
		if index == 0 {
			handler.handleItems(rootResponse, rootRequest)
		} else {
			handler.handleUserItems(rootResponse, rootRequest)
		}
		var root QueryResult[BaseItemDto]
		decodeCatalogResponse(t, rootResponse, &root)
		if rootResponse.Code != http.StatusOK || root.TotalRecordCount != len(views.Items) || len(root.Items) != len(views.Items) {
			t.Fatalf("standard root %d diverged from UserViews: status=%d result=%+v views=%+v", index, rootResponse.Code, root, views)
		}
		for itemIndex := range views.Items {
			if root.Items[itemIndex].Id != views.Items[itemIndex].Id || root.Items[itemIndex].Type != views.Items[itemIndex].Type {
				t.Fatalf("standard root %d identity %d diverged: root=%+v view=%+v", index, itemIndex, root.Items[itemIndex], views.Items[itemIndex])
			}
		}
		if root.Items[0].CollectionType != "unknown" {
			t.Fatalf("standard root %d leaked UserViews collection type: %+v", index, root.Items[0])
		}
	}
	pagedRootRequest := authenticatedCatalogRequest(t, token, "/Items?StartIndex=0&Limit=1")
	pagedRootResponse := httptest.NewRecorder()
	handler.handleItems(pagedRootResponse, pagedRootRequest)
	var pagedRoot QueryResult[BaseItemDto]
	decodeCatalogResponse(t, pagedRootResponse, &pagedRoot)
	if pagedRootResponse.Code != http.StatusOK || pagedRoot.TotalRecordCount != 1 || pagedRoot.StartIndex != 0 || len(pagedRoot.Items) != 1 || pagedRoot.Items[0].Id != promotedID {
		t.Fatalf("paginated standard root status=%d result=%+v", pagedRootResponse.Code, pagedRoot)
	}
	filteredRootRequest := authenticatedCatalogRequest(t, token, "/Items?IncludeItemTypes=CollectionFolder&SearchTerm=Authorized&Ids="+promotedID+"&StartIndex=0&Limit=1")
	filteredRootResponse := httptest.NewRecorder()
	handler.handleItems(filteredRootResponse, filteredRootRequest)
	var filteredRoot QueryResult[BaseItemDto]
	decodeCatalogResponse(t, filteredRootResponse, &filteredRoot)
	if filteredRootResponse.Code != http.StatusOK || filteredRoot.TotalRecordCount != 1 || len(filteredRoot.Items) != 1 || filteredRoot.Items[0].Id != promotedID {
		t.Fatalf("filtered standard root status=%d result=%+v", filteredRootResponse.Code, filteredRoot)
	}
	emptyCatalogPage := watchstate.CatalogPage{Items: []watchstate.CatalogTitle{}, Total: 0, Limit: 20}
	store.listPage = &emptyCatalogPage
	emptyRootRequest := authenticatedCatalogRequest(t, token, "/Items?IncludeItemTypes=Movie,Series")
	emptyRootResponse := httptest.NewRecorder()
	handler.handleItems(emptyRootResponse, emptyRootRequest)
	var emptyRoot QueryResult[BaseItemDto]
	decodeCatalogResponse(t, emptyRootResponse, &emptyRoot)
	if emptyRootResponse.Code != http.StatusOK || emptyRoot.TotalRecordCount != 0 || len(emptyRoot.Items) != 0 {
		t.Fatalf("movie/series leaked at standard root: status=%d result=%+v", emptyRootResponse.Code, emptyRoot)
	}
	if len(store.listQueries) != 1 {
		t.Fatalf("global typed query bypassed canonical catalogue: %+v", store.listQueries)
	}
	boxSetRequest := authenticatedCatalogRequest(t, token, "/Items?IncludeItemTypes=BoxSet")
	boxSetResponse := httptest.NewRecorder()
	handler.handleItems(boxSetResponse, boxSetRequest)
	var boxSets QueryResult[BaseItemDto]
	decodeCatalogResponse(t, boxSetResponse, &boxSets)
	if boxSetResponse.Code != http.StatusOK || len(boxSets.Items) != 1 || boxSets.Items[0].Id != collectionCompatID ||
		boxSets.Items[0].Id == promotedID || boxSets.Items[0].Type != "BoxSet" || boxSets.Items[0].ImageTags["Primary"] != coverTag {
		t.Fatalf("BoxSet identity/poster changed: status=%d result=%+v", boxSetResponse.Code, boxSets)
	}

	boxSetImageRequest := authenticatedCatalogRequest(t, token, "/Items/"+collectionCompatID+"/Images/Primary")
	boxSetImageRequest.SetPathValue("id", collectionCompatID)
	boxSetImageRequest.SetPathValue("type", "Primary")
	boxSetImageResponse := httptest.NewRecorder()
	handler.handleImage(boxSetImageResponse, boxSetImageRequest)
	if boxSetImageResponse.Code != http.StatusOK || len(presenter.served) != 1 || presenter.served[0] != coverTag {
		t.Fatalf("BoxSet poster artwork changed: status=%d served=%v", boxSetImageResponse.Code, presenter.served)
	}
	presenter.served = nil
	userRequest := authenticatedCatalogRequest(t, token, "/Users/Me")
	userResponse := httptest.NewRecorder()
	handler.handleCurrentUser(userResponse, userRequest)
	var user UserDto
	decodeCatalogResponse(t, userResponse, &user)
	if userResponse.Code != http.StatusOK || len(user.Configuration.OrderedViews) != 1 ||
		user.Configuration.OrderedViews[0] != promotedID || len(user.Configuration.MyMediaExcludes) != 0 {
		t.Fatalf("ordered home views status=%d config=%+v", userResponse.Code, user.Configuration)
	}

	latestRequest := authenticatedCatalogRequest(t, token, "/Users/"+catalogTestProfileID+"/Items/Latest?ParentId="+promotedID+"&Fields=ParentId,PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb&Recursive=true&MediaTypes=Video&Limit=20&IsPlayed=false")
	latestResponse := httptest.NewRecorder()
	handler.ServeHTTP(latestResponse, latestRequest)
	var latest []BaseItemDto
	decodeCatalogResponse(t, latestResponse, &latest)
	if latestResponse.Code != http.StatusOK || len(latest) != 2 || latest[0].Name != "First" || latest[1].Name != "Second" {
		t.Fatalf("home collection row status=%d items=%+v body=%s", latestResponse.Code, latest, latestResponse.Body.String())
	}
	for index, item := range latest {
		if item.Type != "CollectionFolder" || item.ParentId != promotedID || item.CollectionType != "unknown" ||
			item.PrimaryImageAspectRatio != 16.0/9.0 || item.ImageTags["Thumb"] == "" || len(item.BackdropImageTags) != 1 ||
			item.BackdropImageTags[0] != item.ImageTags["Thumb"] || item.UserData == nil || item.UserData.ItemId != item.Id {
			t.Fatalf("home row contains an invalid landscape folder: %+v", item)
		}
		if index == 0 && item.ImageTags["Thumb"] != heroTag {
			t.Fatalf("HeroBackdropURL was not preferred for landscape folder: %+v", item)
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
	detailRequest := authenticatedCatalogRequest(t, token, "/Users/"+catalogTestProfileID+"/Items/"+promotedID+"?Fields=PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb")
	detailRequest.SetPathValue("userId", catalogTestProfileID)
	detailRequest.SetPathValue("itemId", promotedID)
	detailResponse := httptest.NewRecorder()
	handler.handleUserItem(detailResponse, detailRequest)
	var detail BaseItemDto
	decodeCatalogResponse(t, detailResponse, &detail)
	if detailResponse.Code != http.StatusOK || detail.Id != promotedID || detail.Type != "CollectionFolder" || detail.CollectionType != "unknown" ||
		detail.PrimaryImageAspectRatio != 16.0/9.0 || detail.ImageTags["Thumb"] != heroTag ||
		len(detail.BackdropImageTags) != 1 || detail.BackdropImageTags[0] != heroTag {
		t.Fatalf("promoted collection detail status=%d item=%+v", detailResponse.Code, detail)
	}
	countRequest := authenticatedCatalogRequest(t, token, "/Users/"+catalogTestProfileID+"/Items?ParentId="+promotedID+"&IncludeItemTypes=Movie&Recursive=true&StartIndex=0&Limit=0")
	countRequest.SetPathValue("id", catalogTestProfileID)
	countResponse := httptest.NewRecorder()
	handler.handleUserItems(countResponse, countRequest)
	var countResult QueryResult[BaseItemDto]
	decodeCatalogResponse(t, countResponse, &countResult)
	if countResponse.Code != http.StatusOK || countResult.TotalRecordCount != 2 || len(countResult.Items) != 0 {
		t.Fatalf("promoted collection count preflight status=%d result=%+v", countResponse.Code, countResult)
	}
	folderBrowseRequest := authenticatedCatalogRequest(t, token, "/Users/"+catalogTestProfileID+"/Items?ParentId="+promotedID+"&Recursive=true&StartIndex=0&Limit=36&SortBy=SortName,SortName&SortOrder=Ascending&Fields=ParentId,PrimaryImageAspectRatio,SortName")
	folderBrowseRequest.SetPathValue("id", catalogTestProfileID)
	folderBrowseResponse := httptest.NewRecorder()
	handler.handleUserItems(folderBrowseResponse, folderBrowseRequest)
	var folderBrowse QueryResult[BaseItemDto]
	decodeCatalogResponse(t, folderBrowseResponse, &folderBrowse)
	if folderBrowseResponse.Code != http.StatusOK || folderBrowse.TotalRecordCount != 2 || len(folderBrowse.Items) != 2 {
		t.Fatalf("promoted folder browse status=%d result=%+v", folderBrowseResponse.Code, folderBrowse)
	}
	for index, item := range folderBrowse.Items {
		if item.Id != service.authorized.Folders[index].ID || item.Type != "CollectionFolder" || item.ParentId != promotedID {
			t.Fatalf("promoted folder %d=%+v", index, item)
		}
	}
	folderDetailRequest := authenticatedCatalogRequest(t, token, "/Items/"+folderBrowse.Items[0].Id+"?UserId="+catalogTestProfileID)
	folderDetailRequest.SetPathValue("id", folderBrowse.Items[0].Id)
	folderDetailResponse := httptest.NewRecorder()
	handler.handleItem(folderDetailResponse, folderDetailRequest)
	var folderDetail BaseItemDto
	decodeCatalogResponse(t, folderDetailResponse, &folderDetail)
	if folderDetailResponse.Code != http.StatusOK || folderDetail.Id != folderBrowse.Items[0].Id || folderDetail.Type != "CollectionFolder" {
		t.Fatalf("folder detail status=%d item=%+v", folderDetailResponse.Code, folderDetail)
	}
	folderItemsRequest := authenticatedCatalogRequest(t, token, "/Items?ParentId="+folderDetail.Id+"&Recursive=true&StartIndex=0&Limit=36&SortBy=SortName,SortName&SortOrder=Ascending")
	folderItemsResponse := httptest.NewRecorder()
	handler.handleItems(folderItemsResponse, folderItemsRequest)
	var folderItems QueryResult[BaseItemDto]
	decodeCatalogResponse(t, folderItemsResponse, &folderItems)
	if folderItemsResponse.Code != http.StatusOK || len(folderItems.Items) != 1 || folderItems.Items[0].Id != collectionMovieID || folderItems.Items[0].Type != "Movie" {
		t.Fatalf("folder contents status=%d result=%+v", folderItemsResponse.Code, folderItems)
	}
	itemsRequest := authenticatedCatalogRequest(t, token, "/Users/"+catalogTestProfileID+"/Items?ParentId="+promotedID+"&IncludeItemTypes=Movie,Series&Recursive=true&Limit=2&Fields=PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb")
	itemsRequest.SetPathValue("id", catalogTestProfileID)
	itemsResponse := httptest.NewRecorder()
	handler.handleUserItems(itemsResponse, itemsRequest)
	var items QueryResult[BaseItemDto]
	decodeCatalogResponse(t, itemsResponse, &items)
	if itemsResponse.Code != http.StatusOK || len(items.Items) != 2 || items.Items[0].Type == "Folder" || items.Items[1].Type == "Folder" {
		t.Fatalf("direct collection view status=%d result=%+v", itemsResponse.Code, items)
	}
}

func TestGlobalTypedItemsUseCanonicalCatalogWithoutResolvingCollections(t *testing.T) {
	base, reader, token := newCatalogHTTPHandler(t)
	service := &collectionCompatService{authorized: collection.Collection{
		ID: collectionCompatID, Title: "Authorized Collection",
		Folders: []collection.Folder{{
			ID: "11111111-1111-4111-8111-111111111110", Title: "First",
			Sources: []collection.Source{{Kind: collection.SourceKindAddonCatalog, AddonCatalog: &collection.AddonCatalogSource{Type: collection.MediaTypeMovie}}},
		}},
	}}
	created := time.Date(2026, 8, 9, 12, 34, 56, 700_000_000, time.UTC)
	reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{{
		ID: collectionMovieID, MediaType: collection.MediaTypeMovie, Title: "Canonical movie", CreatedAt: created,
		Genres: []string{}, ProviderIDs: map[string]string{},
	}}, Total: 1, Limit: 10}
	handler, err := New(Dependencies{
		ServerInfo: base.serverInfo, Authentication: base.authentication, Catalog: reader, Collections: service,
	})
	if err != nil {
		t.Fatal(err)
	}

	target := "/Items?UserId=" + catalogTestProfileID +
		"&Recursive=true&IncludeItemTypes=Movie&Fields=DateCreated,MediaSourceCount" +
		"&SortBy=DateCreated,SortName,ProductionYear&SortOrder=Descending&StartIndex=0&Limit=10"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedCatalogRequest(t, token, target))
	var result QueryResult[BaseItemDto]
	decodeCatalogResponse(t, response, &result)
	if response.Code != http.StatusOK || result.TotalRecordCount != 1 || len(result.Items) != 1 ||
		result.Items[0].Id != collectionMovieID || result.Items[0].DateCreated != "2026-08-09T12:34:56.7000000Z" ||
		result.Items[0].MediaSourceCount == nil || *result.Items[0].MediaSourceCount != 1 {
		t.Fatalf("canonical global catalogue status=%d result=%+v body=%s", response.Code, result, response.Body.String())
	}
	if len(reader.queries) != 1 || reader.queries[0].SortBy != "datecreated,sortname,productionyear" ||
		reader.queries[0].SortOrder != "descending" || len(service.calls) != 0 {
		t.Fatalf("global catalogue escaped canonical reader: queries=%+v collectionCalls=%+v", reader.queries, service.calls)
	}
}

func TestCollectionFolderProjectionIsAlwaysLandscape(t *testing.T) {
	serverID, err := ParseServerID(catalogTestServerID)
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{serverInfo: ServerInfo{ID: serverID}}
	promotedID, err := handler.collectionViewID(collectionCompatID)
	if err != nil {
		t.Fatal(err)
	}
	for _, shape := range []string{collection.TileShapePoster, collection.TileShapeLandscape, collection.TileShapeSquare, "unknown"} {
		item := handler.collectionFolderDTO(collection.Collection{ID: collectionCompatID, FolderCoverShape: shape}, collection.Folder{ID: "folder", Title: "Folder"})
		if item.Type != "CollectionFolder" || item.CollectionType != "unknown" || item.PrimaryImageAspectRatio != 16.0/9.0 ||
			item.ParentId != promotedID {
			t.Fatalf("shape=%q produced a non-landscape promoted folder: %+v", shape, item)
		}
	}

	tags := collectionFolderImageTags("safe-tag")
	backdrops := collectionFolderBackdropImageTags("safe-tag")
	if tags["Primary"] != "safe-tag" || tags["Thumb"] != "safe-tag" || len(backdrops) != 1 || backdrops[0] != "safe-tag" {
		t.Fatalf("promoted folder artwork projection is incomplete: tags=%#v backdrops=%#v", tags, backdrops)
	}
}

func TestCollectionViewProjectionFallsBackTogether(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Handler, *collectionCompatService)
	}{
		{name: "service absent", configure: func(handler *Handler, _ *collectionCompatService) { handler.collections = nil }},
		{name: "list failure", configure: func(_ *Handler, service *collectionCompatService) {
			service.listErr = errors.New("collection store unavailable")
		}},
		{name: "empty list", configure: func(_ *Handler, service *collectionCompatService) { service.listed = []collection.Collection{} }},
		{name: "all invalid", configure: func(_ *Handler, service *collectionCompatService) {
			service.listed = []collection.Collection{{ID: "not-a-uuid", Title: "Invalid Collection"}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, service, _, token := newCollectionCompatHandler(t)
			test.configure(handler, service)

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
				views.Items[0].Name != "Movies" || views.Items[1].Name != "TV Shows" ||
				len(user.Configuration.OrderedViews) != 2 || user.Configuration.OrderedViews[0] != views.Items[0].Id ||
				user.Configuration.OrderedViews[1] != views.Items[1].Id {
				t.Fatalf("collection fallback diverged: viewsStatus=%d views=%+v userStatus=%d config=%+v", viewsResponse.Code, views, userResponse.Code, user.Configuration)
			}
		})
	}
}

func TestRecursiveCollectionBrowseFlattensFolders(t *testing.T) {
	handler, service, _, token := newCollectionCompatHandler(t)
	request := authenticatedCatalogRequest(t, token, "/Items?ParentId="+collectionCompatID+"&IncludeItemTypes=Movie,Series&Recursive=true&EnableUserData=true&StartIndex=0&Limit=2&Fields=PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb")
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

func TestCollectionFolderLatestReturnsCanonicalItems(t *testing.T) {
	routes := []struct {
		name string
		path string
	}{
		{name: "global", path: "/Items/Latest"},
		{name: "user", path: "/Users/" + catalogTestProfileID + "/Items/Latest"},
	}
	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			handler, service, _, token := newCollectionCompatHandler(t)
			targetFolder := service.authorized.Folders[1]
			request := authenticatedCatalogRequest(t, token, route.path+"?ParentId="+targetFolder.ID+"&IncludeItemTypes=Movie,Series&StartIndex=0&Limit=2")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			var items []BaseItemDto
			decodeCatalogResponse(t, response, &items)
			if response.Code != http.StatusOK || len(items) != 2 ||
				items[0].Id != collectionMovieID || items[0].Name != "Canonical movie" || items[0].Type != "Movie" ||
				items[1].Id != collectionSeriesID || items[1].Name != "Add-on series" || items[1].Type != "Series" {
				t.Fatalf("folder latest status=%d items=%+v body=%s", response.Code, items, response.Body.String())
			}
			if len(service.calls) != 1 || service.calls[0].collectionID != service.authorized.ID ||
				service.calls[0].folderID != targetFolder.ID || service.calls[0].limit != maximumCollectionResolveLimit {
				t.Fatalf("folder latest resolved outside its target: %+v", service.calls)
			}
		})
	}
}

func TestVirtualCatalogRootsProjectAuthorizedCollections(t *testing.T) {
	t.Run("items", func(t *testing.T) {
		handler, service, _, token := newCollectionCompatHandler(t)
		views, ok := handler.virtualViews()
		if !ok {
			t.Fatal("virtual views unavailable")
		}
		target := "/Items?ParentId=" + views[0].Id + "&IncludeItemTypes=Movie&Ids=" + collectionMovieID + "," + collectionExtraMovieID + "&SearchTerm=i&SortBy=SortName&SortOrder=Ascending&StartIndex=1&Limit=1"
		response := httptest.NewRecorder()
		handler.handleItems(response, authenticatedCatalogRequest(t, token, target))
		var result QueryResult[BaseItemDto]
		decodeCatalogResponse(t, response, &result)
		if response.Code != http.StatusOK || result.TotalRecordCount != 2 || result.StartIndex != 1 || len(result.Items) != 1 ||
			result.Items[0].Id != collectionExtraMovieID || result.Items[0].Type != "Movie" {
			t.Fatalf("collection-backed movie root status=%d result=%+v", response.Code, result)
		}
		for _, call := range service.calls {
			if call.limit <= 0 || call.limit > maximumCollectionResolveLimit {
				t.Fatalf("unbounded collection resolution: %+v", service.calls)
			}
		}
	})

	t.Run("latest", func(t *testing.T) {
		handler, service, _, token := newCollectionCompatHandler(t)
		service.authorized.Folders[0].Sources = []collection.Source{{Kind: collection.SourceKindAddonCatalog, AddonCatalog: &collection.AddonCatalogSource{Type: collection.MediaTypeMovie}}}
		service.authorized.Folders[1].Sources = []collection.Source{{Kind: collection.SourceKindAddonCatalog, AddonCatalog: &collection.AddonCatalogSource{Type: collection.MediaTypeSeries}}}
		views, ok := handler.virtualViews()
		if !ok {
			t.Fatal("virtual views unavailable")
		}
		request := authenticatedCatalogRequest(t, token, "/Items/Latest?ParentId="+views[1].Id+"&IncludeItemTypes=Series&StartIndex=0&Limit=2")
		response := httptest.NewRecorder()
		handler.handleLatestItems(response, request)
		var items []BaseItemDto
		decodeCatalogResponse(t, response, &items)
		if response.Code != http.StatusOK || len(items) != 1 || items[0].Id != collectionSeriesID || items[0].Type != "Series" {
			t.Fatalf("collection-backed series latest status=%d items=%+v", response.Code, items)
		}
		for _, call := range service.calls {
			if call.folderID != service.authorized.Folders[1].ID {
				t.Fatalf("series projection resolved incompatible folder: %+v", service.calls)
			}
		}
	})

	t.Run("count only", func(t *testing.T) {
		handler, service, _, token := newCollectionCompatHandler(t)
		views, ok := handler.virtualViews()
		if !ok {
			t.Fatal("virtual views unavailable")
		}
		request := authenticatedCatalogRequest(t, token, "/Items?ParentId="+views[0].Id+"&IncludeItemTypes=Movie&Limit=0")
		response := httptest.NewRecorder()
		handler.handleItems(response, request)
		var result QueryResult[BaseItemDto]
		decodeCatalogResponse(t, response, &result)
		if response.Code != http.StatusOK || result.TotalRecordCount != 1 || len(result.Items) != 0 {
			t.Fatalf("collection-backed count status=%d result=%+v", response.Code, result)
		}
		if len(service.calls) != 1 {
			t.Fatalf("count-only projection was not bounded: %+v", service.calls)
		}
	})

	t.Run("collection count only", func(t *testing.T) {
		handler, service, _, token := newCollectionCompatHandler(t)
		viewID, err := handler.collectionViewID(collectionCompatID)
		if err != nil {
			t.Fatal(err)
		}
		request := authenticatedCatalogRequest(t, token, "/Items?ParentId="+viewID+"&Recursive=true&Limit=0")
		response := httptest.NewRecorder()
		handler.handleItems(response, request)
		var result QueryResult[BaseItemDto]
		decodeCatalogResponse(t, response, &result)
		if response.Code != http.StatusOK || result.TotalRecordCount != 2 || len(result.Items) != 0 {
			t.Fatalf("collection count status=%d result=%+v", response.Code, result)
		}
		if len(service.calls) != 0 {
			t.Fatalf("collection folder count resolved remote content: %+v", service.calls)
		}
	})
}

func TestRecursiveCollectionBrowseSortsBeforePagination(t *testing.T) {
	handler, service, _, token := newCollectionCompatHandler(t)
	request := authenticatedCatalogRequest(t, token, "/Items?ParentId="+collectionCompatID+"&IncludeItemTypes=Movie,Series&Recursive=true&SortBy=SortName&SortOrder=Ascending&StartIndex=0&Limit=1")
	response := httptest.NewRecorder()
	handler.handleItems(response, request)

	var result QueryResult[BaseItemDto]
	decodeCatalogResponse(t, response, &result)
	if response.Code != http.StatusOK || result.TotalRecordCount != 2 || len(result.Items) != 1 ||
		result.Items[0].Id != collectionSeriesID || result.Items[0].Name != "Add-on series" {
		t.Fatalf("recursive collection was paginated before sorting: status=%d result=%+v", response.Code, result)
	}
	if len(service.calls) != 2 || service.calls[1].folderID != service.authorized.Folders[1].ID || service.calls[1].page != 1 {
		t.Fatalf("sorted collection resolved beyond the requested window: %+v", service.calls)
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
