package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/operations"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/settings"
)

type fakeOperationsService struct {
	overview    operations.OperationsOverview
	overviewErr error
	schedule    operations.MetadataRefreshSchedule
	scheduleErr error
	run         operations.OperationRun
	runErr      error

	overviewCalls  int
	scheduleCalls  int
	actionCalls    int
	scheduledCalls int
	principal      auth.Principal
	scheduleInput  operations.MetadataRefreshScheduleInput
	action         operations.OperationAction
}

func (f *fakeOperationsService) Overview(_ context.Context, principal auth.Principal) (operations.OperationsOverview, error) {
	f.overviewCalls++
	f.principal = principal
	return f.overview, f.overviewErr
}

func (f *fakeOperationsService) UpdateMetadataRefreshSchedule(_ context.Context, principal auth.Principal, input operations.MetadataRefreshScheduleInput) (operations.MetadataRefreshSchedule, error) {
	f.scheduleCalls++
	f.principal = principal
	f.scheduleInput = input
	return f.schedule, f.scheduleErr
}

func (f *fakeOperationsService) RunAction(_ context.Context, principal auth.Principal, action operations.OperationAction) (operations.OperationRun, error) {
	f.actionCalls++
	f.principal = principal
	f.action = action
	return f.run, f.runErr
}

func (f *fakeOperationsService) RunScheduled(context.Context) error {
	f.scheduledCalls++
	return nil
}

func TestOperationsOverviewReturnsAdministratorStatus(t *testing.T) {
	nextRun := time.Date(2026, time.August, 3, 2, 0, 0, 0, time.UTC)
	lastStarted := nextRun.Add(-24 * time.Hour)
	lastCompleted := lastStarted.Add(time.Minute)
	lastStatus := "partial"
	service := &fakeOperationsService{overview: operations.OperationsOverview{
		MetadataCache: operations.MetadataCacheStatus{
			Entries: 1842, FreshEntries: 1730, ExpiredEntries: 112,
			RootTitles: 920, MissingTitles: 23, ArtworkSnapshots: 917,
		},
		MetadataRefresh: operations.MetadataRefreshSchedule{
			Task: "metadata-refresh", Enabled: true, IntervalHours: 24, Language: "en-US", BatchSize: 50,
			NextRunAt: &nextRun, LastStartedAt: &lastStarted, LastCompletedAt: &lastCompleted,
			LastStatus: &lastStatus, LastResult: &operations.MetadataRefreshResult{Candidates: 50, Refreshed: 48, Failed: 2},
		},
		HousekeepingIntervalMinutes: 5,
	}}
	api := authenticatedOperationsAPI(service)
	request := authenticatedContractRequest(http.MethodGet, "/api/v1/operations", nil)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	var body operations.OperationsOverview
	decodeResponse(t, response, &body)
	if body.MetadataCache != service.overview.MetadataCache || body.HousekeepingIntervalMinutes != 5 {
		t.Fatalf("unexpected overview response %#v", body)
	}
	if body.MetadataRefresh.LastResult == nil || !reflect.DeepEqual(*body.MetadataRefresh.LastResult, *service.overview.MetadataRefresh.LastResult) {
		t.Fatalf("unexpected refresh result %#v", body.MetadataRefresh.LastResult)
	}
	if body.MetadataRefresh.NextRunAt == nil || !body.MetadataRefresh.NextRunAt.Equal(nextRun) {
		t.Fatalf("unexpected next run %#v", body.MetadataRefresh.NextRunAt)
	}
	if service.overviewCalls != 1 || service.principal.Role != "admin" {
		t.Fatalf("unexpected overview call count or principal: %d, %#v", service.overviewCalls, service.principal)
	}
}

func TestUpdateMetadataRefreshSchedulePreservesRequiredValues(t *testing.T) {
	nextRun := time.Date(2026, time.August, 3, 2, 0, 0, 0, time.UTC)
	service := &fakeOperationsService{schedule: operations.MetadataRefreshSchedule{
		Task: "metadata-refresh", Enabled: true, IntervalHours: 168, Language: "fr-FR", BatchSize: 25, NextRunAt: &nextRun,
	}}
	api := authenticatedOperationsAPI(service)
	request := authenticatedContractRequest(http.MethodPut, "/api/v1/operations/schedules/metadata-refresh", bytes.NewBufferString(`{"enabled":true,"intervalHours":168,"language":"fr-FR","batchSize":25}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	wantInput := operations.MetadataRefreshScheduleInput{Enabled: true, IntervalHours: 168, Language: "fr-FR", BatchSize: 25}
	if service.scheduleCalls != 1 || service.scheduleInput != wantInput {
		t.Fatalf("unexpected schedule update: calls=%d input=%#v", service.scheduleCalls, service.scheduleInput)
	}
	var body operations.MetadataRefreshSchedule
	decodeResponse(t, response, &body)
	if body.Task != "metadata-refresh" || body.IntervalHours != 168 || body.Language != "fr-FR" || body.BatchSize != 25 || body.NextRunAt == nil || !body.NextRunAt.Equal(nextRun) {
		t.Fatalf("unexpected schedule response %#v", body)
	}
}

func TestUpdateMetadataRefreshScheduleRequiresEveryField(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "enabled", body: `{"intervalHours":24,"language":"en-US","batchSize":50}`},
		{name: "interval hours", body: `{"enabled":true,"language":"en-US","batchSize":50}`},
		{name: "language", body: `{"enabled":true,"intervalHours":24,"batchSize":50}`},
		{name: "batch size", body: `{"enabled":true,"intervalHours":24,"language":"en-US"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeOperationsService{}
			api := authenticatedOperationsAPI(service)
			request := authenticatedContractRequest(http.MethodPut, "/api/v1/operations/schedules/metadata-refresh", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			assertOperationsError(t, response, http.StatusBadRequest, "invalid_operation_schedule")
			if service.scheduleCalls != 0 {
				t.Fatalf("schedule service called %d times", service.scheduleCalls)
			}
		})
	}
}

func TestUpdateMetadataRefreshScheduleRequiresJSONContentType(t *testing.T) {
	service := &fakeOperationsService{}
	api := authenticatedOperationsAPI(service)
	request := authenticatedContractRequest(http.MethodPut, "/api/v1/operations/schedules/metadata-refresh", bytes.NewBufferString(`{"enabled":true,"intervalHours":24,"language":"en-US","batchSize":50}`))
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	assertOperationsError(t, response, http.StatusUnsupportedMediaType, "unsupported_media_type")
	if service.scheduleCalls != 0 {
		t.Fatalf("schedule service called %d times", service.scheduleCalls)
	}
}

func TestOperationActionsReturnActionSpecificResults(t *testing.T) {
	started := time.Date(2026, time.August, 2, 14, 30, 0, 0, time.UTC)
	completed := started.Add(42 * time.Second)
	tests := []struct {
		name   string
		action operations.OperationAction
		result operations.OperationResult
	}{
		{
			name: "fetch missing metadata", action: operations.ActionFetchMissingMetadata,
			result: operations.OperationResult{Metadata: &operations.MetadataRefreshResult{Candidates: 50, Refreshed: 48, Failed: 2}},
		},
		{
			name: "run housekeeping", action: operations.ActionRunHousekeeping,
			result: operations.OperationResult{Playback: &playback.PurgeResult{SessionsRemoved: 3, JobsStopped: 2, StorageBytes: 1048576}},
		},
		{
			name: "clear metadata cache", action: operations.ActionClearMetadataCache,
			result: operations.OperationResult{MetadataCache: &operations.MetadataCacheClearResult{EntriesDeleted: 1842}},
		},
		{
			name: "clear stream cache", action: operations.ActionClearStreamCache,
			result: operations.OperationResult{Playback: &playback.PurgeResult{SessionsRemoved: 11, JobsStopped: 4, StorageBytes: 0}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeOperationsService{run: operations.OperationRun{
				Action: test.action, StartedAt: started, CompletedAt: completed, Status: "succeeded", Result: test.result,
			}}
			api := authenticatedOperationsAPI(service)
			request := authenticatedContractRequest(http.MethodPost, "/api/v1/operations/actions/"+string(test.action), nil)
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
			}
			var body operations.OperationRun
			decodeResponse(t, response, &body)
			if body.Action != test.action || body.Status != "succeeded" || !body.StartedAt.Equal(started) || !body.CompletedAt.Equal(completed) {
				t.Fatalf("unexpected operation envelope %#v", body)
			}
			if service.actionCalls != 1 || service.action != test.action {
				t.Fatalf("unexpected action call: calls=%d action=%q", service.actionCalls, service.action)
			}
			assertOperationResult(t, body.Result, test.result)
		})
	}
}

func TestOperationActionErrorsUseStableEnvelopes(t *testing.T) {
	tests := []struct {
		name       string
		action     string
		serviceErr error
		wantStatus int
		wantCode   string
	}{
		{name: "unknown action", action: "rebuild-everything", serviceErr: operations.ErrActionNotFound, wantStatus: http.StatusNotFound, wantCode: "operation_not_found"},
		{name: "overlapping refresh", action: string(operations.ActionFetchMissingMetadata), serviceErr: operations.ErrInProgress, wantStatus: http.StatusConflict, wantCode: "operation_in_progress"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeOperationsService{runErr: test.serviceErr}
			api := authenticatedOperationsAPI(service)
			request := authenticatedContractRequest(http.MethodPost, "/api/v1/operations/actions/"+test.action, nil)
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			assertOperationsError(t, response, test.wantStatus, test.wantCode)
			if service.actionCalls != 1 || string(service.action) != test.action {
				t.Fatalf("unexpected action call: calls=%d action=%q", service.actionCalls, service.action)
			}
		})
	}
}

func TestOperationsEndpointsRequireAdministrator(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		path        string
		body        *bytes.Buffer
		contentType string
	}{
		{name: "overview", method: http.MethodGet, path: "/api/v1/operations"},
		{name: "schedule", method: http.MethodPut, path: "/api/v1/operations/schedules/metadata-refresh", body: bytes.NewBufferString(`{"enabled":true,"intervalHours":24,"language":"en-US","batchSize":50}`), contentType: "application/json"},
		{name: "action", method: http.MethodPost, path: "/api/v1/operations/actions/run-housekeeping"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeOperationsService{}
			api := operationsAPI(service, auth.Principal{UserID: "user-id", Role: "member", SessionID: "session-id"})
			request := authenticatedContractRequest(test.method, test.path, test.body)
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			assertOperationsError(t, response, http.StatusForbidden, "admin_required")
			if service.overviewCalls != 0 || service.scheduleCalls != 0 || service.actionCalls != 0 {
				t.Fatalf("operations service was called: overview=%d schedule=%d action=%d", service.overviewCalls, service.scheduleCalls, service.actionCalls)
			}
		})
	}
}

func TestOperationsGuardRejectsCategoryScopedAdministrator(t *testing.T) {
	categoryID := "11111111-1111-4111-8111-111111111111"
	response := httptest.NewRecorder()
	allowed := requireOperationsAdministrator(response, auth.Principal{
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID,
	})
	if allowed || response.Code != http.StatusForbidden {
		t.Fatalf("category-scoped administrator allowed=%v status=%d", allowed, response.Code)
	}
}

func TestOperationsOverviewRemainsReachableDuringMaintenance(t *testing.T) {
	service := &fakeOperationsService{overview: operations.OperationsOverview{
		MetadataRefresh:             operations.MetadataRefreshSchedule{Task: "metadata-refresh", Enabled: false, IntervalHours: 24, Language: "en-US", BatchSize: 50},
		HousekeepingIntervalMinutes: 5,
	}}
	api := authenticatedOperationsAPI(service)
	message := "Upgrading the library"
	api.settings = &fakeSettingsService{maintenance: settings.Maintenance{Enabled: true, Message: &message}}
	request := authenticatedContractRequest(http.MethodGet, "/api/v1/operations", nil)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected Operations to remain reachable, got %d: %s", response.Code, response.Body.String())
	}
	if service.overviewCalls != 1 {
		t.Fatalf("overview service called %d times", service.overviewCalls)
	}
}

func authenticatedOperationsAPI(service operationsService) *API {
	return operationsAPI(service, auth.Principal{
		UserID: "user-id", DeviceID: "device-id", Role: "admin", SessionID: "session-id",
		AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
	})
}

func operationsAPI(service operationsService, principal auth.Principal) *API {
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: principal}
	api.operations = service
	return api
}

func assertOperationsError(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("expected status %d, got %d: %s", wantStatus, response.Code, response.Body.String())
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != wantCode || body.Error.Message == "" {
		t.Fatalf("unexpected error envelope %#v", body)
	}
}

func assertOperationResult(t *testing.T, got, want operations.OperationResult) {
	t.Helper()
	switch {
	case want.Metadata != nil:
		if got.Metadata == nil || !reflect.DeepEqual(*got.Metadata, *want.Metadata) || got.MetadataCache != nil || got.Playback != nil {
			t.Fatalf("unexpected metadata result %#v", got)
		}
	case want.MetadataCache != nil:
		if got.MetadataCache == nil || *got.MetadataCache != *want.MetadataCache || got.Metadata != nil || got.Playback != nil {
			t.Fatalf("unexpected metadata cache result %#v", got)
		}
	case want.Playback != nil:
		if got.Playback == nil || *got.Playback != *want.Playback || got.Metadata != nil || got.MetadataCache != nil {
			t.Fatalf("unexpected playback result %#v", got)
		}
	default:
		t.Fatal("test result has no action-specific payload")
	}
}
