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
	resolveInput     watchstate.ResolveTitleInput
	resolveValue     watchstate.TitleReference
	resolveErr       error
	libraryMediaType string
	libraryPage      int
	libraryPageSize  int
	libraryValue     watchstate.LibraryPage
	libraryErr       error
	addTitleID       string
	addValue         watchstate.LibraryItem
	addErr           error
	removeTitleID    string
	removeErr        error
	progressTitleID  string
	progressInput    watchstate.UpdateProgressInput
	progressValue    watchstate.Progress
	progressErr      error
	completionID     string
	completionValue  bool
	completionInput  watchstate.CompletionInput
	completionResult watchstate.Progress
	completionErr    error
	clearTitleID     string
	clearVersion     int64
	clearErr         error
	continueLimit    int
	continueValue    watchstate.ContinuePage
	continueErr      error
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

func (f *fakeWatchstateService) GetProgress(_ context.Context, _ auth.Principal, titleID string) (watchstate.Progress, error) {
	f.progressTitleID = titleID
	return f.progressValue, f.progressErr
}

func (f *fakeWatchstateService) UpdateProgress(_ context.Context, _ auth.Principal, titleID string, input watchstate.UpdateProgressInput) (watchstate.Progress, error) {
	f.progressTitleID, f.progressInput = titleID, input
	return f.progressValue, f.progressErr
}

func (f *fakeWatchstateService) SetWatched(_ context.Context, _ auth.Principal, titleID string, completed bool, input watchstate.CompletionInput) (watchstate.Progress, error) {
	f.completionID, f.completionValue, f.completionInput = titleID, completed, input
	return f.completionResult, f.completionErr
}

func (f *fakeWatchstateService) ClearProgress(_ context.Context, _ auth.Principal, titleID string, expectedVersion int64) error {
	f.clearTitleID, f.clearVersion = titleID, expectedVersion
	return f.clearErr
}

func (f *fakeWatchstateService) ContinueWatching(_ context.Context, _ auth.Principal, limit int) (watchstate.ContinuePage, error) {
	f.continueLimit = limit
	return f.continueValue, f.continueErr
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
		"posterUrl":"https://example.com/poster.jpg"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.resolveTitle(response, request, auth.Principal{})

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.resolveInput.Provider != "imdb" || service.resolveInput.ResourceID != "tt123" || service.resolveInput.Title != "Example" {
		t.Fatalf("unexpected resolve input: %+v", service.resolveInput)
	}
}

func TestLibraryHandlerPassesFilters(t *testing.T) {
	service := &fakeWatchstateService{libraryValue: watchstate.LibraryPage{Items: []watchstate.LibraryItem{}, Page: 2}}
	api := watchstateAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/library?mediaType=series&page=2&pageSize=40", nil)
	response := httptest.NewRecorder()

	api.library(response, request, auth.Principal{})

	if response.Code != http.StatusOK || service.libraryMediaType != "series" || service.libraryPage != 2 || service.libraryPageSize != 40 {
		t.Fatalf("unexpected library request status=%d mediaType=%q page=%d pageSize=%d", response.Code, service.libraryMediaType, service.libraryPage, service.libraryPageSize)
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
		{err: watchstate.ErrNotFound, status: http.StatusNotFound, code: "title_not_found"},
		{err: watchstate.ErrConflict, status: http.StatusConflict, code: "watch_state_conflict"},
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
	}
}

func watchstateAPI(service watchstateService) *API {
	return &API{
		watchstate: service,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}
