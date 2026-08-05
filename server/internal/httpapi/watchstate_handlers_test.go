package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

type fakeWatchstateService struct {
	resolveInput          watchstate.ResolveTitleInput
	resolveValue          watchstate.TitleReference
	resolveErr            error
	libraryMediaType      string
	libraryPage           int
	libraryPageSize       int
	libraryValue          watchstate.LibraryPage
	libraryErr            error
	membershipInput       []watchstate.TVLibraryIdentity
	membershipValue       watchstate.TVLibraryMembershipResult
	membershipErr         error
	addTitleID            string
	addValue              watchstate.LibraryItem
	addErr                error
	removeTitleID         string
	removeErr             error
	progressTitleID       string
	progressInput         watchstate.UpdateProgressInput
	progressValue         watchstate.Progress
	progressErr           error
	progressBatchInput    []string
	progressBatchValue    watchstate.ProgressBatch
	progressBatchErr      error
	completionID          string
	completionValue       bool
	completionInput       watchstate.CompletionInput
	completionResult      watchstate.Progress
	completionErr         error
	completionBatchInput  []watchstate.SetWatchedBatchItem
	completionBatchResult watchstate.ProgressBatch
	completionBatchErr    error
	clearTitleID          string
	clearVersion          int64
	clearErr              error
	continueLimit         int
	continueValue         watchstate.ContinuePage
	continueErr           error
	dismissTitleID        string
	dismissErr            error
}

func (f *fakeWatchstateService) ResolveTitle(_ context.Context, _ auth.Principal, input watchstate.ResolveTitleInput) (watchstate.TitleReference, error) {
	f.resolveInput = input
	return f.resolveValue, f.resolveErr
}

func (f *fakeWatchstateService) AddLibrary(_ context.Context, _ auth.Principal, titleID string) (watchstate.LibraryItem, error) {
	f.addTitleID = titleID
	return f.addValue, f.addErr
}

func (f *fakeWatchstateService) RemoveLibrary(_ context.Context, _ auth.Principal, titleID string) error {
	f.removeTitleID = titleID
	return f.removeErr
}

func (f *fakeWatchstateService) Library(_ context.Context, _ auth.Principal, mediaType string, page, pageSize int) (watchstate.LibraryPage, error) {
	f.libraryMediaType, f.libraryPage, f.libraryPageSize = mediaType, page, pageSize
	return f.libraryValue, f.libraryErr
}

func (f *fakeWatchstateService) TVLibraryMembership(_ context.Context, _ auth.Principal, identities []watchstate.TVLibraryIdentity) (watchstate.TVLibraryMembershipResult, error) {
	f.membershipInput = identities
	return f.membershipValue, f.membershipErr
}

func (f *fakeWatchstateService) GetProgress(_ context.Context, _ auth.Principal, titleID string) (watchstate.Progress, error) {
	f.progressTitleID = titleID
	return f.progressValue, f.progressErr
}

func (f *fakeWatchstateService) GetProgressBatch(_ context.Context, _ auth.Principal, titleIDs []string) (watchstate.ProgressBatch, error) {
	f.progressBatchInput = titleIDs
	return f.progressBatchValue, f.progressBatchErr
}

func (f *fakeWatchstateService) UpdateProgress(_ context.Context, _ auth.Principal, titleID string, input watchstate.UpdateProgressInput) (watchstate.Progress, error) {
	f.progressTitleID, f.progressInput = titleID, input
	return f.progressValue, f.progressErr
}

func (f *fakeWatchstateService) SetWatched(_ context.Context, _ auth.Principal, titleID string, completed bool, input watchstate.CompletionInput) (watchstate.Progress, error) {
	f.completionID, f.completionValue, f.completionInput = titleID, completed, input
	return f.completionResult, f.completionErr
}

func (f *fakeWatchstateService) SetWatchedBatch(_ context.Context, _ auth.Principal, input []watchstate.SetWatchedBatchItem) (watchstate.ProgressBatch, error) {
	f.completionBatchInput = input
	return f.completionBatchResult, f.completionBatchErr
}

func (f *fakeWatchstateService) ClearProgress(_ context.Context, _ auth.Principal, titleID string, expectedVersion int64) error {
	f.clearTitleID, f.clearVersion = titleID, expectedVersion
	return f.clearErr
}

func (f *fakeWatchstateService) ContinueWatching(_ context.Context, _ auth.Principal, limit int) (watchstate.ContinuePage, error) {
	f.continueLimit = limit
	return f.continueValue, f.continueErr
}

func (f *fakeWatchstateService) DismissContinue(_ context.Context, _ auth.Principal, titleID string) error {
	f.dismissTitleID = titleID
	return f.dismissErr
}

func TestResolveTitlePassesProviderSnapshot(t *testing.T) {
	service := &fakeWatchstateService{resolveValue: watchstate.TitleReference{
		TitleID: "550e8400-e29b-41d4-a716-446655440000", MediaType: "movie", Provider: "imdb",
		ExternalID: "tt123", ResourceID: "tt123", Title: "Example",
	}}
	api := watchstateAPI(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/titles/resolve", strings.NewReader(`{
		"mediaType":"movie",
		"provider":"imdb",
		"externalId":"tt123",
		"resourceId":"tt123",
		"title":"Example",
		"posterUrl":"https://example.com/poster.jpg",
		"released":"2026-08-14"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.resolveTitle(response, request, auth.Principal{})

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.resolveInput.Provider != "imdb" || service.resolveInput.ResourceID != "tt123" || service.resolveInput.Title != "Example" || service.resolveInput.Released != "2026-08-14" {
		t.Fatalf("unexpected resolve input: %+v", service.resolveInput)
	}
}

func TestResolveTitlePassesTVSourceIdentityWithoutStreamURL(t *testing.T) {
	service := &fakeWatchstateService{resolveValue: watchstate.TitleReference{
		TitleID: "550e8400-e29b-41d4-a716-446655440000", MediaType: "tv", Provider: "addon",
		ExternalID: "computed", ResourceID: "channel-1", Title: "News",
	}}
	api := watchstateAPI(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/titles/resolve", strings.NewReader(`{
		"mediaType":"tv",
		"provider":"addon",
		"resourceId":"channel-1",
		"title":"News",
		"sourceAddonId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"sourceCatalogId":"live",
		"sourceName":"Provider",
		"country":"US",
		"language":"en",
		"category":"News"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.resolveTitle(response, request, auth.Principal{})

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	input := service.resolveInput
	if input.SourceAddonID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" ||
		input.SourceCatalogID != "live" || input.SourceName != "Provider" ||
		input.ResourceID != "channel-1" || input.Country != "US" ||
		input.Language != "en" || input.Category != "News" {
		t.Fatalf("unexpected TV resolve input: %+v", input)
	}
}

func TestResolveTitleRejectsTVStreamURLField(t *testing.T) {
	service := &fakeWatchstateService{}
	api := watchstateAPI(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/titles/resolve", strings.NewReader(`{
		"mediaType":"tv",
		"provider":"addon",
		"resourceId":"channel-1",
		"title":"News",
		"sourceAddonId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"streamUrl":"https://stream.invalid/live.m3u8"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.resolveTitle(response, request, auth.Principal{})

	if response.Code != http.StatusBadRequest || service.resolveInput.MediaType != "" {
		t.Fatalf("expected unknown stream URL field rejection, got %d: %s", response.Code, response.Body.String())
	}
}

func TestLibraryHandlerPassesFilters(t *testing.T) {
	service := &fakeWatchstateService{libraryValue: watchstate.LibraryPage{Items: []watchstate.LibraryItem{}, Page: 2}}
	api := watchstateAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/library?mediaType=tv&page=2&pageSize=40", nil)
	response := httptest.NewRecorder()

	api.library(response, request, auth.Principal{})

	if response.Code != http.StatusOK || service.libraryMediaType != "tv" || service.libraryPage != 2 || service.libraryPageSize != 40 {
		t.Fatalf("unexpected library request status=%d mediaType=%q page=%d pageSize=%d", response.Code, service.libraryMediaType, service.libraryPage, service.libraryPageSize)
	}
}

func TestTVLibraryMembershipHandlerPassesBoundedIdentities(t *testing.T) {
	service := &fakeWatchstateService{membershipValue: watchstate.TVLibraryMembershipResult{Items: []watchstate.TVLibraryMembership{{
		SourceAddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		ResourceID:    "channel-1",
		TitleID:       "550e8400-e29b-41d4-a716-446655440000",
	}}}}
	api := watchstateAPI(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/library/membership", strings.NewReader(`{"identities":[{"sourceAddonId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","resourceId":"channel-1"}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.tvLibraryMembership(response, request, auth.Principal{})

	if response.Code != http.StatusOK || len(service.membershipInput) != 1 || service.membershipInput[0].ResourceID != "channel-1" {
		t.Fatalf("unexpected membership request status=%d input=%+v", response.Code, service.membershipInput)
	}
	if response.Body.String() != `{"items":[{"sourceAddonId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","resourceId":"channel-1","titleId":"550e8400-e29b-41d4-a716-446655440000"}]}`+"\n" {
		t.Fatalf("unexpected membership response: %s", response.Body.String())
	}
}

func TestTVLibraryMembershipHandlerRejectsOversizedBatch(t *testing.T) {
	service := &fakeWatchstateService{}
	api := watchstateAPI(service)
	var body strings.Builder
	body.WriteString(`{"identities":[`)
	for index := 0; index <= watchstate.MaximumTVLibraryMembershipIdentities; index++ {
		if index > 0 {
			body.WriteByte(',')
		}
		body.WriteString(`{"sourceAddonId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","resourceId":"channel"}`)
	}
	body.WriteString(`]}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/library/membership", strings.NewReader(body.String()))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.tvLibraryMembership(response, request, auth.Principal{})

	if response.Code != http.StatusUnprocessableEntity || service.membershipInput != nil {
		t.Fatalf("expected oversized batch rejection, got status=%d input=%+v", response.Code, service.membershipInput)
	}
}

func TestGetProgressBatchPreservesOrderAndMissingStates(t *testing.T) {
	progress := watchstate.Progress{TitleID: "first", MediaType: "episode", Version: 4}
	service := &fakeWatchstateService{progressBatchValue: watchstate.ProgressBatch{Items: []watchstate.ProgressBatchItem{
		{TitleID: "first", Progress: &progress},
		{TitleID: "second", Progress: nil},
	}}}
	api := watchstateAPI(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/progress/batch", strings.NewReader(`{"titleIds":["first","second"]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.getProgressBatch(response, request, auth.Principal{})

	if response.Code != http.StatusOK || len(service.progressBatchInput) != 2 || service.progressBatchInput[0] != "first" || service.progressBatchInput[1] != "second" {
		t.Fatalf("unexpected progress batch request status=%d input=%v", response.Code, service.progressBatchInput)
	}
	if !strings.Contains(response.Body.String(), `"titleId":"second","progress":null`) {
		t.Fatalf("missing progress state was not preserved: %s", response.Body.String())
	}
}

func TestProgressBatchHandlersRejectEmptyInputs(t *testing.T) {
	service := &fakeWatchstateService{}
	api := watchstateAPI(service)
	for _, test := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request, auth.Principal)
		path    string
		body    string
	}{
		{name: "read", handler: api.getProgressBatch, path: "/api/v1/progress/batch", body: `{"titleIds":[]}`},
		{name: "write", handler: api.setWatchedBatch, path: "/api/v1/titles/watched/batch", body: `{"items":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			test.handler(response, request, auth.Principal{})
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("expected bounded input rejection, got %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestSetWatchedBatchPassesVersionsAndCompletionStates(t *testing.T) {
	first := watchstate.Progress{TitleID: "first", MediaType: "episode", Completed: true, Version: 5}
	second := watchstate.Progress{TitleID: "second", MediaType: "episode", Completed: false, Version: 3}
	service := &fakeWatchstateService{completionBatchResult: watchstate.ProgressBatch{Items: []watchstate.ProgressBatchItem{
		{TitleID: "first", Progress: &first},
		{TitleID: "second", Progress: &second},
	}}}
	api := watchstateAPI(service)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/titles/watched/batch", strings.NewReader(`{"items":[{"titleId":"first","completed":true,"expectedVersion":4},{"titleId":"second","completed":false,"expectedVersion":2}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.setWatchedBatch(response, request, auth.Principal{})

	if response.Code != http.StatusOK || len(service.completionBatchInput) != 2 {
		t.Fatalf("unexpected watched batch request status=%d input=%+v", response.Code, service.completionBatchInput)
	}
	if service.completionBatchInput[0].ExpectedVersion != 4 || !service.completionBatchInput[0].Completed ||
		service.completionBatchInput[1].ExpectedVersion != 2 || service.completionBatchInput[1].Completed {
		t.Fatalf("watched batch lost version/completion contract: %+v", service.completionBatchInput)
	}
}

func TestGetProgressReturnsNoContentWhenUnset(t *testing.T) {
	service := &fakeWatchstateService{progressErr: watchstate.ErrProgressNotFound}
	api := watchstateAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/progress/title-id", nil)
	request.SetPathValue("titleId", "title-id")
	response := httptest.NewRecorder()

	api.getProgress(response, request, auth.Principal{})

	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("expected empty status 204, got %d: %s", response.Code, response.Body.String())
	}
}

func TestUpdateProgressPassesOptimisticVersion(t *testing.T) {
	service := &fakeWatchstateService{progressValue: watchstate.Progress{TitleID: "title-id", MediaType: "movie", Version: 8}}
	api := watchstateAPI(service)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/progress/title-id", strings.NewReader(`{"positionSeconds":120,"durationSeconds":3600,"completed":false,"expectedVersion":7}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("titleId", "title-id")
	response := httptest.NewRecorder()

	api.updateProgress(response, request, auth.Principal{})

	if response.Code != http.StatusOK || service.progressTitleID != "title-id" || service.progressInput.ExpectedVersion != 7 || service.progressInput.PositionSeconds != 120 {
		t.Fatalf("unexpected progress request status=%d id=%q input=%+v", response.Code, service.progressTitleID, service.progressInput)
	}
}

func TestWatchedAndClearHandlersPassVersions(t *testing.T) {
	service := &fakeWatchstateService{completionResult: watchstate.Progress{TitleID: "episode-id", MediaType: "episode", Completed: true, Version: 4}}
	api := watchstateAPI(service)

	watchedRequest := httptest.NewRequest(http.MethodPost, "/api/v1/titles/episode-id/watched", strings.NewReader(`{"expectedVersion":3}`))
	watchedRequest.Header.Set("Content-Type", "application/json")
	watchedRequest.SetPathValue("titleId", "episode-id")
	watchedResponse := httptest.NewRecorder()
	api.markWatched(watchedResponse, watchedRequest, auth.Principal{})
	if watchedResponse.Code != http.StatusOK || service.completionID != "episode-id" || !service.completionValue || service.completionInput.ExpectedVersion != 3 {
		t.Fatalf("unexpected watched request status=%d value=%v input=%+v", watchedResponse.Code, service.completionValue, service.completionInput)
	}

	clearRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/progress/episode-id?expectedVersion=4", nil)
	clearRequest.SetPathValue("titleId", "episode-id")
	clearResponse := httptest.NewRecorder()
	api.clearProgress(clearResponse, clearRequest, auth.Principal{})
	if clearResponse.Code != http.StatusNoContent || service.clearTitleID != "episode-id" || service.clearVersion != 4 {
		t.Fatalf("unexpected clear request status=%d id=%q version=%d", clearResponse.Code, service.clearTitleID, service.clearVersion)
	}
}

func TestWatchstateErrorsHaveStableHTTPContracts(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{err: watchstate.ErrInvalidInput, status: http.StatusUnprocessableEntity, code: "invalid_watch_state"},
		{err: watchstate.ErrProfileRequired, status: http.StatusConflict, code: "profile_selection_required"},
		{err: watchstate.ErrForbidden, status: http.StatusForbidden, code: "watch_state_forbidden"},
		{err: watchstate.ErrNotFound, status: http.StatusNotFound, code: "title_not_found"},
		{err: watchstate.ErrConflict, status: http.StatusConflict, code: "watch_state_conflict"},
		{err: watchstate.ErrOutboxCapacity, status: http.StatusServiceUnavailable, code: "tracking_sync_capacity"},
	}
	for _, test := range tests {
		service := &fakeWatchstateService{continueErr: errors.Join(errors.New("wrapped"), test.err)}
		api := watchstateAPI(service)
		request := httptest.NewRequest(http.MethodGet, "/api/v1/continue-watching", nil)
		response := httptest.NewRecorder()
		api.continueWatching(response, request, auth.Principal{})
		if response.Code != test.status {
			t.Fatalf("expected status %d, got %d", test.status, response.Code)
		}
		var body errorEnvelope
		decodeResponse(t, response, &body)
		if body.Error.Code != test.code {
			t.Fatalf("expected code %q, got %q", test.code, body.Error.Code)
		}
		if test.err == watchstate.ErrOutboxCapacity && response.Header().Get("Retry-After") != "5" {
			t.Fatalf("expected Retry-After 5, got %q", response.Header().Get("Retry-After"))
		}
	}
}

func watchstateAPI(service watchstateService) *API {
	return &API{
		watchstate: service,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestDismissContinuePassesTitle(t *testing.T) {
	service := &fakeWatchstateService{}
	api := watchstateAPI(service)
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/continue-watching/title-id", nil)
	request.SetPathValue("titleId", "title-id")
	response := httptest.NewRecorder()

	api.dismissContinue(response, request, auth.Principal{})

	if response.Code != http.StatusNoContent || service.dismissTitleID != "title-id" {
		t.Fatalf("unexpected dismissal status=%d title=%q", response.Code, service.dismissTitleID)
	}
}
