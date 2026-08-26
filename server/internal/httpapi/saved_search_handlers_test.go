package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/savedsearch"
)

type savedSearchServiceStub struct {
	createInput       savedsearch.SavedSearchInput
	deleteID          string
	deleteRevision    int64
	evaluateID        string
	evaluatePage      int
	evaluatePageSize  int
}

func (stub *savedSearchServiceStub) ListSavedSearches(context.Context, auth.Principal) ([]savedsearch.SavedSearch, error) { return []savedsearch.SavedSearch{}, nil }
func (stub *savedSearchServiceStub) CreateSavedSearch(_ context.Context, _ auth.Principal, input savedsearch.SavedSearchInput) (savedsearch.SavedSearch, error) { stub.createInput = input; return savedsearch.SavedSearch{Name: input.Name, Query: input.Query, Sort: input.Sort}, nil }
func (*savedSearchServiceStub) UpdateSavedSearch(context.Context, auth.Principal, string, savedsearch.SavedSearchInput) (savedsearch.SavedSearch, error) { return savedsearch.SavedSearch{}, nil }
func (stub *savedSearchServiceStub) DeleteSavedSearch(_ context.Context, _ auth.Principal, id string, revision int64) error { stub.deleteID, stub.deleteRevision = id, revision; return nil }
func (*savedSearchServiceStub) ListSmartCollections(context.Context, auth.Principal) ([]savedsearch.SmartCollection, error) { return []savedsearch.SmartCollection{}, nil }
func (*savedSearchServiceStub) CreateSmartCollection(context.Context, auth.Principal, savedsearch.SmartCollectionInput) (savedsearch.SmartCollection, error) { return savedsearch.SmartCollection{}, nil }
func (*savedSearchServiceStub) UpdateSmartCollection(context.Context, auth.Principal, string, savedsearch.SmartCollectionInput) (savedsearch.SmartCollection, error) { return savedsearch.SmartCollection{}, nil }
func (*savedSearchServiceStub) DeleteSmartCollection(context.Context, auth.Principal, string, int64) error { return nil }
func (stub *savedSearchServiceStub) EvaluateSmartCollection(_ context.Context, _ auth.Principal, id string, page, pageSize int) (savedsearch.SmartCollectionPage, error) { stub.evaluateID, stub.evaluatePage, stub.evaluatePageSize = id, page, pageSize; return savedsearch.SmartCollectionPage{Page: page, PageSize: pageSize}, nil }


func TestCreateSavedSearchRejectsUnknownJSONProperties(t *testing.T) {
	stub := &savedSearchServiceStub{}
	api := &API{savedSearches: stub}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/saved-searches", strings.NewReader(`{"name":"Space","query":"space","sort":"relevance","sql":"DROP TABLE titles"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.createSavedSearch(response, request, auth.Principal{})
	if response.Code != http.StatusBadRequest || stub.createInput.Name != "" { t.Fatalf("status=%d input=%+v body=%s", response.Code, stub.createInput, response.Body.String()) }
}

func TestDeleteSavedSearchRequiresAndForwardsPositiveRevision(t *testing.T) {
	stub := &savedSearchServiceStub{}
	api := &API{savedSearches: stub}
	invalid := httptest.NewRequest(http.MethodDelete, "/api/v1/saved-searches/id?expectedRevision=0", nil)
	invalid.SetPathValue("savedSearchId", "id")
	invalidResponse := httptest.NewRecorder()
	api.deleteSavedSearch(invalidResponse, invalid, auth.Principal{})
	if invalidResponse.Code != http.StatusUnprocessableEntity || stub.deleteID != "" { t.Fatalf("invalid revision status=%d call=%q", invalidResponse.Code, stub.deleteID) }

	valid := httptest.NewRequest(http.MethodDelete, "/api/v1/saved-searches/id?expectedRevision=7", nil)
	valid.SetPathValue("savedSearchId", "id")
	validResponse := httptest.NewRecorder()
	api.deleteSavedSearch(validResponse, valid, auth.Principal{})
	if validResponse.Code != http.StatusNoContent || stub.deleteID != "id" || stub.deleteRevision != 7 { t.Fatalf("valid delete status=%d id=%q revision=%d", validResponse.Code, stub.deleteID, stub.deleteRevision) }
}

func TestSmartCollectionItemsAppliesBoundedDefaults(t *testing.T) {
	stub := &savedSearchServiceStub{}
	api := &API{savedSearches: stub}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/smart-collections/smart/items", nil)
	request.SetPathValue("smartCollectionId", "smart")
	response := httptest.NewRecorder()
	api.smartCollectionItems(response, request, auth.Principal{})
	if response.Code != http.StatusOK || stub.evaluateID != "smart" || stub.evaluatePage != 1 || stub.evaluatePageSize != 24 { t.Fatalf("status=%d call=%q page=%d size=%d", response.Code, stub.evaluateID, stub.evaluatePage, stub.evaluatePageSize) }
}
