package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/collection"
)

type fakeCollectionService struct {
	listValue          []collection.Collection
	listErr            error
	exportValue        collection.ExportDocument
	importCalls        int
	exportErr          error
	importInput        collection.ExportDocument
	importValue        collection.ImportResult
	importErr          error
	getID              string
	getValue           collection.Collection
	getErr             error
	managementID       string
	managementValue    collection.Collection
	managementErr      error
	saveID             string
	saveInput          collection.SaveInput
	saveValue          collection.Collection
	saveErr            error
	deleteID           string
	deleteErr          error
	reorder            collection.ReorderInput
	folderCollectionID string
	folderID           string
	resolvePage        int
	resolveLimit       int
	resolveLanguage    string
	resolveRegion      string
	resolved           collection.ResolvedFolder
	resolveErr         error
	lookupKind         string
	lookupQuery        string
	lookupLanguage     string
	lookupPage         int
	lookupValues       []collection.LookupResult
	lookupErr          error
	genreMediaType     string
	genreLanguage      string
	genreValues        []collection.Genre
	genreErr           error
	semanticInput      collection.SemanticSearchInput
	semanticValue      collection.SemanticSearchPage
	semanticErr        error
}

func (fake *fakeCollectionService) List(context.Context, auth.Principal) ([]collection.Collection, error) {
	return fake.listValue, fake.listErr
}

func (fake *fakeCollectionService) Export(context.Context, auth.Principal) (collection.ExportDocument, error) {
	return fake.exportValue, fake.exportErr
}

func (fake *fakeCollectionService) Get(_ context.Context, _ auth.Principal, id string) (collection.Collection, error) {
	fake.getID = id
	return fake.getValue, fake.getErr
}

func (fake *fakeCollectionService) Management(_ context.Context, _ auth.Principal, id string) (collection.Collection, error) {
	fake.managementID = id
	return fake.managementValue, fake.managementErr
}

func (fake *fakeCollectionService) Create(_ context.Context, _ auth.Principal, input collection.SaveInput) (collection.Collection, error) {
	fake.saveInput = input
	return fake.saveValue, fake.saveErr
}

func (fake *fakeCollectionService) Import(_ context.Context, _ auth.Principal, input collection.ExportDocument) (collection.ImportResult, error) {
	fake.importCalls++
	fake.importInput = input
	return fake.importValue, fake.importErr
}

func (fake *fakeCollectionService) Update(_ context.Context, _ auth.Principal, id string, input collection.SaveInput) (collection.Collection, error) {
	fake.saveID = id
	fake.saveInput = input
	return fake.saveValue, fake.saveErr
}

func (fake *fakeCollectionService) Delete(_ context.Context, _ auth.Principal, id string) error {
	fake.deleteID = id
	return fake.deleteErr
}

func (fake *fakeCollectionService) Reorder(_ context.Context, _ auth.Principal, input collection.ReorderInput) ([]collection.Collection, error) {
	fake.reorder = input
	return fake.listValue, fake.listErr
}

func (fake *fakeCollectionService) ResolveFolder(_ context.Context, _ auth.Principal, collectionID, folderID string, page, limit int, language, region string) (collection.ResolvedFolder, error) {
	fake.folderCollectionID, fake.folderID = collectionID, folderID
	fake.resolvePage, fake.resolveLimit = page, limit
	fake.resolveLanguage, fake.resolveRegion = language, region
	return fake.resolved, fake.resolveErr
}

func (fake *fakeCollectionService) LookupTMDB(_ context.Context, _ auth.Principal, kind, query, language string, page int) ([]collection.LookupResult, error) {
	fake.lookupKind, fake.lookupQuery = kind, query
	fake.lookupLanguage, fake.lookupPage = language, page
	return fake.lookupValues, fake.lookupErr
}

func (fake *fakeCollectionService) TMDBGenres(_ context.Context, _ auth.Principal, mediaType, language string) ([]collection.Genre, error) {
	fake.genreMediaType, fake.genreLanguage = mediaType, language
	return fake.genreValues, fake.genreErr
}
func (fake *fakeCollectionService) SemanticSearch(_ context.Context, _ auth.Principal, input collection.SemanticSearchInput) (collection.SemanticSearchPage, error) {
	fake.semanticInput = input
	return fake.semanticValue, fake.semanticErr
}

type fakeCollectionArtworkPresenter struct {
	presentCalls int
}

func (fake *fakeCollectionArtworkPresenter) PresentCollections(_ context.Context, values []collection.Collection) {
	fake.presentCalls++
	for index := range values {
		values[index].BackdropImageURL = "/api/v1/artwork/collection-backdrop"
		for folderIndex := range values[index].Folders {
			folder := &values[index].Folders[folderIndex]
			folder.CoverImageURL = "/api/v1/artwork/folder-cover"
			folder.TitleLogoURL = "/api/v1/artwork/folder-logo"
			folder.HeroBackdropURL = "/api/v1/artwork/folder-backdrop"
		}
	}
}

func (*fakeCollectionArtworkPresenter) RestoreCollectionSaveInput(context.Context, *collection.SaveInput) {
}

func (*fakeCollectionArtworkPresenter) LocalizeCollectionLookupResults(context.Context, []collection.LookupResult) {
}

func (*fakeCollectionArtworkPresenter) PresentSemanticSearchPage(_ context.Context, page *collection.SemanticSearchPage) {
	for index := range page.Items {
		page.Items[index].PosterURL = "/api/v1/artwork/semantic-poster"
	}
}

func TestCollectionExportAndImportRoutes(t *testing.T) {
	service := &fakeCollectionService{
		exportValue: collection.ExportDocument{SchemaVersion: collection.ExportSchemaVersion, Collections: []collection.SaveInput{{ProfileIDs: []string{"22222222-2222-4222-8222-222222222222"}, CategoryIDs: []string{"33333333-3333-4333-8333-333333333333"}}}},
		importValue: collection.ImportResult{Imported: 1, Collections: []collection.Collection{{Title: "Imported"}}},
	}
	api := collectionAPI(service)

	exportRequest := httptest.NewRequest(http.MethodGet, "/api/v1/collections/export", nil)
	exportRequest.Header.Set("Authorization", "Bearer access")
	exportResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("expected export status 200, got %d: %s", exportResponse.Code, exportResponse.Body.String())
	}
	if disposition := exportResponse.Header().Get("Content-Disposition"); disposition != `attachment; filename="rivune-collections.json"` {
		t.Fatalf("unexpected export content disposition %q", disposition)
	}
	if strings.Contains(exportResponse.Body.String(), "profileIds") || strings.Contains(exportResponse.Body.String(), "categoryIds") {
		t.Fatalf("portable export exposed assignment policy: %s", exportResponse.Body.String())
	}

	importRequest := httptest.NewRequest(http.MethodPost, "/api/v1/collections/import", bytes.NewBufferString(`{"schemaVersion":1,"collections":[]}`))
	importRequest.Header.Set("Authorization", "Bearer access")
	importRequest.Header.Set("Content-Type", "application/json")
	importResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(importResponse, importRequest)
	if importResponse.Code != http.StatusCreated {
		t.Fatalf("expected import status 201, got %d: %s", importResponse.Code, importResponse.Body.String())
	}
	if service.importInput.SchemaVersion != collection.ExportSchemaVersion {
		t.Fatalf("unexpected imported schema version %d", service.importInput.SchemaVersion)
	}
	if service.importCalls != 1 {
		t.Fatalf("valid portable import calls = %d, want 1", service.importCalls)
	}

	invalidImports := []struct {
		name string
		body string
	}{
		{name: "explicit empty assignments", body: `{"schemaVersion":1,"collections":[{"profileIds":[],"categoryIds":[]}]}`},
		{name: "null profileIds", body: `{"schemaVersion":1,"collections":[{"profileIds":null}]}`},
		{name: "null categoryIds", body: `{"schemaVersion":1,"collections":[{"categoryIds":null}]}`},
		{name: "unknown member", body: `{"schemaVersion":1,"collections":[{"unexpectedAssignment":[]}]}`},
	}
	for _, invalid := range invalidImports {
		t.Run(invalid.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/collections/import", bytes.NewBufferString(invalid.body))
			request.Header.Set("Authorization", "Bearer access")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			api.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("portable import status = %d, want 400: %s", response.Code, response.Body.String())
			}
			if service.importCalls != 1 {
				t.Fatalf("invalid portable import reached service; calls = %d", service.importCalls)
			}
		})
	}
}

func TestCreateCollectionForwardsCompleteTMDBConfiguration(t *testing.T) {
	service := &fakeCollectionService{saveValue: collection.Collection{ID: "11111111-1111-4111-8111-111111111111", Title: "Networks"}}
	api := collectionAPI(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/collections", bytes.NewBufferString(`{
		"title":"Networks","pinToTop":true,"focusGlowEnabled":true,"viewMode":"rows",
		"folders":[{"title":"HBO","tileShape":"landscape","focusGifEnabled":false,"hideTitle":false,
		"sources":[{"kind":"tmdb","title":"HBO Network","tmdb":{"sourceType":"network","tmdbId":49,"mediaType":"series","sort":"popularity.desc","filters":{}}}]}]
	}`))
	request.Header.Set("Authorization", "Bearer access")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if service.saveInput.Title != "Networks" || len(service.saveInput.Folders) != 1 || len(service.saveInput.Folders[0].Sources) != 1 {
		t.Fatalf("collection input was not forwarded: %+v", service.saveInput)
	}
	source := service.saveInput.Folders[0].Sources[0]
	if source.TMDB == nil || source.TMDB.SourceType != "network" || source.TMDB.TMDBID == nil || *source.TMDB.TMDBID != 49 {
		t.Fatalf("TMDB source configuration changed: %+v", source)
	}
}

func TestCollectionAssignmentDimensionsForwardedExactly(t *testing.T) {
	const (
		collectionID = "11111111-1111-4111-8111-111111111111"
		profileID    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		categoryID   = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	)
	tests := []struct {
		name        string
		body        string
		profileIDs  []string
		categoryIDs []string
	}{
		{name: "omitted", body: `{}`},
		{name: "explicit empty", body: `{"profileIds":[],"categoryIds":[]}`, profileIDs: []string{}, categoryIDs: []string{}},
		{name: "nonempty", body: `{"profileIds":["` + profileID + `"],"categoryIds":["` + categoryID + `"]}`, profileIDs: []string{profileID}, categoryIDs: []string{categoryID}},
	}
	for _, endpoint := range []string{"create", "update"} {
		for _, test := range tests {
			t.Run(endpoint+"/"+test.name, func(t *testing.T) {
				service := &fakeCollectionService{saveValue: collection.Collection{ProfileIDs: []string{}, CategoryIDs: []string{}}}
				api := collectionAPI(service)
				method, path, wantStatus := http.MethodPost, "/api/v1/collections", http.StatusCreated
				if endpoint == "update" {
					method, path, wantStatus = http.MethodPut, "/api/v1/collections/"+collectionID, http.StatusOK
				}
				request := httptest.NewRequest(method, path, strings.NewReader(test.body))
				request.Header.Set("Authorization", "Bearer access")
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				api.Handler().ServeHTTP(response, request)
				if response.Code != wantStatus {
					t.Fatalf("status = %d, want %d: %s", response.Code, wantStatus, response.Body.String())
				}
				if !reflect.DeepEqual(service.saveInput.ProfileIDs, test.profileIDs) || !reflect.DeepEqual(service.saveInput.CategoryIDs, test.categoryIDs) {
					t.Fatalf("assignments = profileIds:%#v categoryIds:%#v, want profileIds:%#v categoryIds:%#v", service.saveInput.ProfileIDs, service.saveInput.CategoryIDs, test.profileIDs, test.categoryIDs)
				}
			})
		}
	}
}

func TestCollectionAssignmentNullAndUnknownMembersAreRejected(t *testing.T) {
	const collectionID = "11111111-1111-4111-8111-111111111111"
	invalidMembers := []struct {
		name string
		body string
	}{
		{name: "null profileIds", body: `{"profileIds":null}`},
		{name: "null categoryIds", body: `{"categoryIds":null}`},
		{name: "unknown member", body: `{"unexpectedAssignment":[]}`},
	}
	for _, endpoint := range []string{"create", "update"} {
		for _, invalid := range invalidMembers {
			t.Run(endpoint+"/"+invalid.name, func(t *testing.T) {
				service := &fakeCollectionService{}
				api := collectionAPI(service)
				method, path := http.MethodPost, "/api/v1/collections"
				if endpoint == "update" {
					method, path = http.MethodPut, "/api/v1/collections/"+collectionID
				}
				request := httptest.NewRequest(method, path, strings.NewReader(invalid.body))
				request.Header.Set("Authorization", "Bearer access")
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				api.Handler().ServeHTTP(response, request)
				if response.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
				}
			})
		}
	}
}

func TestResolveCollectionFolderForwardsPathAndPagination(t *testing.T) {
	service := &fakeCollectionService{resolved: collection.ResolvedFolder{Items: []collection.Item{}, Errors: []collection.SourceFailure{}, Page: 3}}
	api := collectionAPI(service)
	collectionID := "11111111-1111-4111-8111-111111111111"
	folderID := "22222222-2222-4222-8222-222222222222"
	request := httptest.NewRequest(http.MethodGet, "/api/v1/collections/"+collectionID+"/folders/"+folderID+"/items?page=3&limit=40&language=fr-FR&region=FR", nil)
	request.Header.Set("Authorization", "Bearer access")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.folderCollectionID != collectionID || service.folderID != folderID || service.resolvePage != 3 || service.resolveLimit != 40 || service.resolveLanguage != "fr-FR" || service.resolveRegion != "FR" {
		t.Fatalf("resolution options changed: %+v", service)
	}
}

func TestCollectionTMDBEditorRoutesForwardLookupAndGenres(t *testing.T) {
	service := &fakeCollectionService{
		lookupValues: []collection.LookupResult{{ID: 4, Name: "Pixar"}},
		genreValues:  []collection.Genre{{ID: 16, Name: "Animation"}},
	}
	api := collectionAPI(service)

	lookup := httptest.NewRequest(http.MethodGet, "/api/v1/collections/tmdb/lookup?kind=company&query=Pixar&language=fr-FR", nil)
	lookup.Header.Set("Authorization", "Bearer access")
	lookupResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(lookupResponse, lookup)
	if lookupResponse.Code != http.StatusOK || service.lookupKind != "company" || service.lookupQuery != "Pixar" || service.lookupLanguage != "fr-FR" || service.lookupPage != 1 {
		t.Fatalf("unexpected lookup result status=%d service=%+v", lookupResponse.Code, service)
	}

	genres := httptest.NewRequest(http.MethodGet, "/api/v1/collections/tmdb/genres?mediaType=movie&language=fr-FR", nil)
	genres.Header.Set("Authorization", "Bearer access")
	genreResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(genreResponse, genres)
	if genreResponse.Code != http.StatusOK || service.genreMediaType != "movie" || service.genreLanguage != "fr-FR" {
		t.Fatalf("unexpected genre result status=%d service=%+v", genreResponse.Code, service)
	}
}

func TestCollectionManagementReturnsSourcesWhileOrdinaryReadsRemainLocalized(t *testing.T) {
	const (
		collectionID = "11111111-1111-4111-8111-111111111111"
		profileID    = "22222222-2222-4222-8222-222222222222"
		categoryID   = "33333333-3333-4333-8333-333333333333"
		backdrop     = "https://images.example/private/collection.jpg?token=collection-secret"
		cover        = "https://images.example/private/cover.jpg?token=cover-secret"
		titleLogo    = "https://images.example/private/logo.png?token=logo-secret"
		heroBackdrop = "https://images.example/private/hero.jpg?token=hero-secret"
	)
	stored := collection.Collection{
		ID: collectionID, Title: "Private art", BackdropImageURL: backdrop,
		Folders: []collection.Folder{{
			Title: "Featured", CoverImageURL: cover, TitleLogoURL: titleLogo, HeroBackdropURL: heroBackdrop,
			Sources: []collection.Source{},
		}},
		ProfileIDs: []string{profileID}, CategoryIDs: []string{categoryID},
	}
	service := &fakeCollectionService{managementValue: stored, listValue: []collection.Collection{stored}, getValue: stored}
	presenter := &fakeCollectionArtworkPresenter{}
	api := collectionAPI(service)
	api.collectionArtwork = presenter

	managementRequest := httptest.NewRequest(http.MethodGet, "/api/v1/collections/"+collectionID+"/management", nil)
	managementRequest.Header.Set("Authorization", "Bearer access")
	managementResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(managementResponse, managementRequest)
	if managementResponse.Code != http.StatusOK {
		t.Fatalf("expected management status 200, got %d: %s", managementResponse.Code, managementResponse.Body.String())
	}
	var managed collection.Collection
	decodeResponse(t, managementResponse, &managed)
	if service.managementID != collectionID || managed.BackdropImageURL != backdrop || managed.Folders[0].CoverImageURL != cover ||
		managed.Folders[0].TitleLogoURL != titleLogo || managed.Folders[0].HeroBackdropURL != heroBackdrop {
		t.Fatalf("management response changed stored artwork sources: %+v", managed)
	}
	if !reflect.DeepEqual(managed.ProfileIDs, []string{profileID}) || !reflect.DeepEqual(managed.CategoryIDs, []string{categoryID}) {
		t.Fatalf("management assignments were not serialized separately: profileIds:%#v categoryIds:%#v", managed.ProfileIDs, managed.CategoryIDs)
	}
	if presenter.presentCalls != 0 {
		t.Fatal("management response was passed through the public artwork presenter")
	}

	for _, path := range []string{"/api/v1/collections", "/api/v1/collections/" + collectionID} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer access")
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("ordinary read %s returned %d: %s", path, response.Code, response.Body.String())
		}
		body := response.Body.String()
		if !strings.Contains(body, `"profileIds":["`+profileID+`"]`) || !strings.Contains(body, `"categoryIds":["`+categoryID+`"]`) {
			t.Fatalf("ordinary read %s did not serialize separate assignment arrays: %s", path, body)
		}
		for _, source := range []string{backdrop, cover, titleLogo, heroBackdrop} {
			if strings.Contains(body, source) {
				t.Fatalf("ordinary read %s exposed source URL %q", path, source)
			}
		}
		if !strings.Contains(body, "/api/v1/artwork/") {
			t.Fatalf("ordinary read %s did not contain localized artwork URLs: %s", path, body)
		}
	}
}

func TestCollectionManagementForbiddenResponseIsOpaque(t *testing.T) {
	const source = "https://images.example/private/cover.jpg?token=must-not-leak"
	service := &fakeCollectionService{managementErr: errors.Join(errors.New(source), collection.ErrForbidden)}
	api := collectionAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/collections/11111111-1111-4111-8111-111111111111/management", nil)
	request.Header.Set("Authorization", "Bearer access")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected management status 403, got %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), source) || strings.Contains(response.Body.String(), "must-not-leak") {
		t.Fatalf("forbidden management response exposed internal details: %s", response.Body.String())
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != "collection_forbidden" {
		t.Fatalf("management error code = %q, want collection_forbidden", body.Error.Code)
	}
}

func TestReorderCollectionsReturnsStableForbiddenResponse(t *testing.T) {
	const collectionID = "11111111-1111-4111-8111-111111111111"
	service := &fakeCollectionService{listErr: errors.Join(errors.New("wrapped"), collection.ErrForbidden)}
	api := collectionAPI(service)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/collections/order", bytes.NewBufferString(`{"collectionIds":["`+collectionID+`"]}`))
	request.Header.Set("Authorization", "Bearer access")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", response.Code, response.Body.String())
	}
	if len(service.reorder.CollectionIDs) != 1 || service.reorder.CollectionIDs[0] != collectionID {
		t.Fatalf("reorder input = %+v, want collection %q", service.reorder, collectionID)
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != "collection_forbidden" {
		t.Fatalf("reorder error code = %q, want collection_forbidden", body.Error.Code)
	}
}

func TestSemanticSearchForwardsIntentAndReturnsArtworkLocalizedItems(t *testing.T) {
	service := &fakeCollectionService{semanticValue: collection.SemanticSearchPage{
		Intents:    []collection.SemanticSearchIntent{{ID: "genre:war", Kind: "genre", Value: "war", Label: "War"}},
		TitleQuery: "film de guerre", MediaTypes: []string{"movie"}, Page: 1,
		Items: []collection.Item{{ID: "tmdb:42", MediaType: "movie", Title: "War Movie", PosterURL: "https://images.example/poster.jpg", ExternalIDs: map[string]string{"tmdb": "42"}, Sources: []collection.SourceReference{}}},
	}}
	api := collectionAPI(service)
	presenter := &fakeCollectionArtworkPresenter{}
	api.collectionArtwork = presenter
	request := httptest.NewRequest(http.MethodPost, "/api/v1/search/semantic", strings.NewReader(`{"query":"film de guerre","mediaType":"movie","language":"fr-FR","region":"FR","page":1,"limit":24,"excludedIntentIds":["theme:space"]}`))
	request.Header.Set("Authorization", "Bearer access")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("semantic search status = %d: %s", response.Code, response.Body.String())
	}
	if service.semanticInput.Query != "film de guerre" || service.semanticInput.MediaType != "movie" || service.semanticInput.Language != "fr-FR" ||
		service.semanticInput.Region != "FR" || service.semanticInput.Page != 1 || service.semanticInput.Limit != 24 || !reflect.DeepEqual(service.semanticInput.ExcludedIntentIDs, []string{"theme:space"}) {
		t.Fatalf("semantic search input mismatch: %+v", service.semanticInput)
	}
	var page collection.SemanticSearchPage
	decodeResponse(t, response, &page)
	if len(page.Items) != 1 || page.Items[0].PosterURL != "/api/v1/artwork/semantic-poster" {
		t.Fatalf("semantic search response was not localized: %+v", page)
	}
	validateContractResponse(t, loadOpenAPIContract(t), "/search/semantic", nil, request, response)
}

func TestSemanticSearchCancellationDoesNotWriteServerError(t *testing.T) {
	service := &fakeCollectionService{semanticErr: context.Canceled}
	api := collectionAPI(service)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/search/semantic", strings.NewReader(`{"query":"space films"}`)).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer access")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusInternalServerError || response.Body.Len() != 0 {
		t.Fatalf("canceled semantic request wrote status %d body %q", response.Code, response.Body.String())
	}
}

func TestCollectionErrorsHaveStableHTTPContracts(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "invalid", err: collection.ErrInvalidInput, status: http.StatusUnprocessableEntity, code: "invalid_collection"},
		{name: "profile", err: collection.ErrActiveProfileRequired, status: http.StatusConflict, code: "profile_selection_required"},
		{name: "missing", err: collection.ErrNotFound, status: http.StatusNotFound, code: "collection_not_found"},
		{name: "conflict", err: collection.ErrConflict, status: http.StatusConflict, code: "collection_conflict"},
		{name: "provider", err: collection.ErrProviderUnavailable, status: http.StatusServiceUnavailable, code: "collection_provider_unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeCollectionService{listErr: errors.Join(errors.New("wrapped"), test.err)}
			api := collectionAPI(service)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/collections", nil)
			request.Header.Set("Authorization", "Bearer access")
			response := httptest.NewRecorder()
			api.Handler().ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("expected status %d, got %d: %s", test.status, response.Code, response.Body.String())
			}
			var body errorEnvelope
			decodeResponse(t, response, &body)
			if body.Error.Code != test.code {
				t.Fatalf("expected code %q, got %q", test.code, body.Error.Code)
			}
		})
	}
}

func collectionAPI(service collectionService) *API {
	future := time.Now().UTC().Add(time.Hour)
	profileID := "22222222-2222-4222-8222-222222222222"
	return &API{
		collections: service,
		auth: &fakeAuthService{principal: auth.Principal{
			SessionID: "session", UserID: "user", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &future,
		}},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}
