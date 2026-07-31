package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/calendar"
)

type fakeCalendarService struct {
	from      string
	to        string
	language  string
	principal auth.Principal
	result    calendar.Result
	err       error
}

func (f *fakeCalendarService) List(_ context.Context, principal auth.Principal, from, to, language string) (calendar.Result, error) {
	f.principal, f.from, f.to, f.language = principal, from, to, language
	return f.result, f.err
}

func TestCalendarHandlerForwardsRangeAndPreservesOrdering(t *testing.T) {
	service := &fakeCalendarService{result: calendar.Result{Events: []calendar.Event{
		{ID: "first", TitleID: "11111111-1111-4111-8111-111111111111", MediaType: "movie", Title: "First", ReleaseDate: "2026-07-01"},
		{ID: "second", TitleID: "22222222-2222-4222-8222-222222222222", MediaType: "episode", Title: "Second", ReleaseDate: "2026-07-02"},
	}}}
	api := &API{calendar: service, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/calendar?from=2026-07-01&to=2026-07-31&language=fr-FR", nil)
	response := httptest.NewRecorder()

	api.calendarEvents(response, request, auth.Principal{UserID: "user"})

	if response.Code != http.StatusOK || service.from != "2026-07-01" || service.to != "2026-07-31" || service.language != "fr-FR" || service.principal.UserID != "user" {
		t.Fatalf("unexpected calendar request status=%d from=%q to=%q language=%q principal=%+v", response.Code, service.from, service.to, service.language, service.principal)
	}
	var body calendar.Result
	decodeResponse(t, response, &body)
	if len(body.Events) != 2 || body.Events[0].ID != "first" || body.Events[1].ID != "second" {
		t.Fatalf("unexpected calendar event order: %+v", body.Events)
	}
}

func TestCalendarHandlerReturnsEmptyEventArray(t *testing.T) {
	service := &fakeCalendarService{result: calendar.Result{Events: []calendar.Event{}}}
	api := &API{calendar: service, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()

	api.calendarEvents(response, httptest.NewRequest(http.MethodGet, "/api/v1/calendar?from=2026-07-01&to=2026-07-01", nil), auth.Principal{})

	if response.Code != http.StatusOK || response.Body.String() != "{\"events\":[]}\n" {
		t.Fatalf("expected empty calendar response, got %d: %s", response.Code, response.Body.String())
	}
}

func TestCalendarHandlerMapsRangeAndAuthorizationErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "invalid range", err: calendar.ErrInvalidInput, status: http.StatusBadRequest, code: "invalid_calendar_range"},
		{name: "inactive profile", err: calendar.ErrProfileRequired, status: http.StatusConflict, code: "profile_selection_required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := &API{calendar: &fakeCalendarService{err: test.err}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			response := httptest.NewRecorder()
			api.calendarEvents(response, httptest.NewRequest(http.MethodGet, "/api/v1/calendar?from=bad&to=bad", nil), auth.Principal{})
			if response.Code != test.status {
				t.Fatalf("expected status %d, got %d", test.status, response.Code)
			}
			var body errorEnvelope
			decodeResponse(t, response, &body)
			if body.Error.Code != test.code {
				t.Fatalf("expected code %q, got %q", test.code, body.Error.Code)
			}
		})
	}
}

func TestCalendarRouteRequiresAuthentication(t *testing.T) {
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{authenticateErr: auth.ErrInvalidToken}
	api.calendar = &fakeCalendarService{}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/calendar?from=2026-07-01&to=2026-07-31", nil)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("expected bearer 401, got %d and header %q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
}
