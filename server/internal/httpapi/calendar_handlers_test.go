package httpapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/calendar"
)

type fakeCalendarService struct {
	from           string
	to             string
	language       string
	profileID      string
	token          string
	principal      auth.Principal
	result         calendar.Result
	link           calendar.Link
	credential     calendar.Credential
	feed           []byte
	includePayload bool
	feedCalls      int
	err            error
}

func (f *fakeCalendarService) List(_ context.Context, principal auth.Principal, from, to, language string) (calendar.Result, error) {
	f.principal, f.from, f.to, f.language = principal, from, to, language
	return f.result, f.err
}

func (f *fakeCalendarService) LinkStatus(_ context.Context, principal auth.Principal, profileID string) (calendar.Link, error) {
	f.principal, f.profileID = principal, profileID
	return f.link, f.err
}

func (f *fakeCalendarService) CreateLink(_ context.Context, principal auth.Principal, profileID string) (calendar.Credential, error) {
	f.principal, f.profileID = principal, profileID
	return f.credential, f.err
}

func (f *fakeCalendarService) RotateLink(_ context.Context, principal auth.Principal, profileID string) (calendar.Credential, error) {
	f.principal, f.profileID = principal, profileID
	return f.credential, f.err
}

func (f *fakeCalendarService) RevokeLink(_ context.Context, principal auth.Principal, profileID string) error {
	f.principal, f.profileID = principal, profileID
	return f.err
}

func (f *fakeCalendarService) Feed(_ context.Context, token string, includePayload bool) ([]byte, error) {
	f.feedCalls++
	f.token, f.includePayload = token, includePayload
	return f.feed, f.err
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
		{name: "capacity", err: calendar.ErrCapacity, status: http.StatusServiceUnavailable, code: "calendar_capacity_exceeded"},
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
			if test.err == calendar.ErrCapacity && response.Header().Get("Retry-After") != strconv.Itoa(calendarCapacityRetryAfterSeconds) {
				t.Fatalf("capacity Retry-After = %q", response.Header().Get("Retry-After"))
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

func TestCalendarLinkStatusOmitsCredentialURL(t *testing.T) {
	created := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
	service := &fakeCalendarService{link: calendar.Link{Active: true, CreatedAt: created, RotatedAt: created}}
	api := &API{calendar: service, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/profiles/profile-id/calendar-link", nil)
	request.SetPathValue("profileId", "profile-id")
	response := httptest.NewRecorder()

	api.calendarLinkStatus(response, request, auth.Principal{UserID: "manager"})

	if response.Code != http.StatusOK || service.profileID != "profile-id" || strings.Contains(response.Body.String(), "url") {
		t.Fatalf("calendar status leaked a credential URL or lost scope: %d %s profile=%q", response.Code, response.Body.String(), service.profileID)
	}
	if strings.Contains(response.Body.String(), "0001-01-01") {
		t.Fatalf("calendar status rendered zero timestamps: %s", response.Body.String())
	}

	service.link = calendar.Link{Active: false}
	response = httptest.NewRecorder()
	api.calendarLinkStatus(response, request, auth.Principal{})
	if response.Body.String() != "{\"active\":false}\n" {
		t.Fatalf("inactive calendar status = %s, want active-only response", response.Body.String())
	}
}

func TestCreateCalendarLinkBuildsOneTimeCredentialFromConfiguredPublicURL(t *testing.T) {
	created := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
	service := &fakeCalendarService{credential: calendar.Credential{
		Link:  calendar.Link{Active: true, CreatedAt: created, RotatedAt: created},
		Token: "rivune_cal_secret",
	}}
	api := &API{calendar: service, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	api.config.PublicURL = "https://media.example"
	request := httptest.NewRequest(http.MethodPost, "/api/v1/profiles/profile-id/calendar-link", nil)
	request.SetPathValue("profileId", "profile-id")
	response := httptest.NewRecorder()

	api.createCalendarLink(response, request, auth.Principal{UserID: "manager"})

	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"url":"https://media.example/api/v1/calendar.ics?token=rivune_cal_secret"`) {
		t.Fatalf("created calendar credential = %d %s", response.Code, response.Body.String())
	}
	service.err = calendar.ErrLinkExists
	response = httptest.NewRecorder()
	api.createCalendarLink(response, request, auth.Principal{})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"calendar_link_exists"`) {
		t.Fatalf("duplicate calendar credential = %d %s", response.Code, response.Body.String())
	}
}

func TestCalendarLinkCreationRequiresConfiguredPublicURLBeforeIssuingToken(t *testing.T) {
	service := &fakeCalendarService{credential: calendar.Credential{Token: "must-not-be-returned"}}
	api := &API{calendar: service, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/profiles/profile-id/calendar-link", nil)
	request.SetPathValue("profileId", "profile-id")
	response := httptest.NewRecorder()

	api.createCalendarLink(response, request, auth.Principal{})

	if response.Code != http.StatusServiceUnavailable || service.profileID != "" || strings.Contains(response.Body.String(), service.credential.Token) {
		t.Fatalf("missing public URL response = %d %s service profile=%q", response.Code, response.Body.String(), service.profileID)
	}
}

func TestCalendarFeedGETAndHEADArePublicAndReturnPrivateICS(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			service := &fakeCalendarService{feed: []byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")}
			api := testAPI(&fakeInstanceService{})
			api.auth = &fakeAuthService{authenticateErr: auth.ErrInvalidToken}
			api.calendar = service
			request := httptest.NewRequest(method, "/api/v1/calendar.ics?token=opaque", nil)
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusOK || service.token != "opaque" || service.includePayload != (method == http.MethodGet) {
				t.Fatalf("public feed status=%d token=%q includePayload=%v body=%q", response.Code, service.token, service.includePayload, response.Body.String())
			}
			if response.Header().Get("Content-Type") != "text/calendar; charset=utf-8" ||
				response.Header().Get("Content-Disposition") != `inline; filename="rivune-calendar.ics"` ||
				response.Header().Get("Cache-Control") != "private, no-store" ||
				response.Header().Get("X-Robots-Tag") != "noindex, nofollow" {
				t.Fatalf("public feed headers = %#v", response.Header())
			}
			if method == http.MethodGet && response.Body.Len() == 0 {
				t.Fatal("GET calendar feed has no body")
			}
			if method == http.MethodHead && response.Body.Len() != 0 {
				t.Fatalf("HEAD calendar feed body = %q", response.Body.String())
			}
			if method == http.MethodHead && response.Header().Get("Content-Length") != "" {
				t.Fatalf("HEAD calendar feed unexpectedly serialized a payload of length %q", response.Header().Get("Content-Length"))
			}
		})
	}
}

func TestCalendarFeedRejectsNonUniqueQueryUniformly(t *testing.T) {
	for _, target := range []string{
		"/api/v1/calendar.ics",
		"/api/v1/calendar.ics?token=one&token=two",
		"/api/v1/calendar.ics?token=one&extra=value",
	} {
		service := &fakeCalendarService{}
		api := testAPI(&fakeInstanceService{})
		api.calendar = service
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusNotFound || service.token != "" || !strings.Contains(response.Body.String(), `"code":"calendar_feed_not_found"`) {
			t.Fatalf("invalid feed query %q = %d %s token=%q", target, response.Code, response.Body.String(), service.token)
		}
	}
}

func TestCalendarFeedMapsCapacityAndAdmissionWithSecretSafeHeaders(t *testing.T) {
	const token = "rivune_cal_must_not_leak"
	for _, test := range []struct {
		name       string
		configure  func(*API, *fakeCalendarService)
		wantStatus int
		wantCode   string
		wantCalls  int
	}{
		{
			name: "capacity", wantStatus: http.StatusServiceUnavailable,
			wantCode: "calendar_capacity_exceeded", wantCalls: 1,
			configure: func(_ *API, service *fakeCalendarService) {
				service.err = fmt.Errorf("%w: %s", calendar.ErrCapacity, token)
			},
		},
		{
			name: "admission", wantStatus: http.StatusTooManyRequests,
			wantCode: "rate_limited", wantCalls: 0,
			configure: func(api *API, _ *fakeCalendarService) {
				api.calendarFeedAdmission = newRequestAdmission(0, 2, 120, 4_096, time.Minute)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeCalendarService{}
			api := testAPI(&fakeInstanceService{})
			api.calendar = service
			test.configure(api, service)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/calendar.ics?token="+token, nil)
			request.RemoteAddr = "198.51.100.80:4000"
			response := httptest.NewRecorder()
			api.Handler().ServeHTTP(response, request)

			if response.Code != test.wantStatus || service.feedCalls != test.wantCalls ||
				!strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("feed error response=%d %s calls=%d", response.Code, response.Body.String(), service.feedCalls)
			}
			if response.Header().Get("Retry-After") == "" || response.Header().Get("Cache-Control") != "private, no-store" ||
				response.Header().Get("X-Robots-Tag") != "noindex, nofollow" || strings.Contains(response.Body.String(), token) {
				t.Fatalf("feed error leaked a token or lost retry/security headers: headers=%#v body=%s", response.Header(), response.Body.String())
			}
		})
	}
}
