package demo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/instance"
)

type fakeAdmission struct {
	mu         sync.Mutex
	configured bool
	err        error
	acquired   int
	released   int
	nextID     int
	sessions   map[string]fakeDemoAdmission
}

type fakeDemoAdmission struct {
	source    [sha256.Size]byte
	expiresAt time.Time
}

func (f *fakeAdmission) AcquireSetupPending(context.Context) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquired++
	if f.err != nil {
		return nil, f.err
	}
	if f.configured {
		return nil, instance.ErrAlreadyConfigured
	}
	var once sync.Once
	return func() { once.Do(func() { f.mu.Lock(); f.released++; f.mu.Unlock() }) }, nil
}

func (f *fakeAdmission) AdmitDemoSession(
	_ context.Context,
	source [sha256.Size]byte,
	replacedID string,
	now, expiresAt time.Time,
	globalLimit, sourceLimit int,
	prepare func() error,
) (string, func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquired++
	if f.err != nil {
		f.released++
		return "", nil, f.err
	}
	if f.configured {
		f.released++
		return "", nil, instance.ErrAlreadyConfigured
	}
	globalCount := 0
	sourceCount := 0
	for id, admission := range f.sessions {
		if id == replacedID || !admission.expiresAt.After(now) {
			continue
		}
		globalCount++
		if admission.source == source {
			sourceCount++
		}
	}
	if globalCount >= globalLimit || sourceCount >= sourceLimit {
		f.released++
		return "", nil, instance.ErrDemoSessionCapacity
	}
	if err := prepare(); err != nil {
		f.released++
		return "", nil, err
	}
	if f.sessions == nil {
		f.sessions = make(map[string]fakeDemoAdmission)
	}
	for id, admission := range f.sessions {
		if !admission.expiresAt.After(now) {
			delete(f.sessions, id)
		}
	}
	delete(f.sessions, replacedID)
	f.nextID++
	id := strconv.Itoa(f.nextID)
	f.sessions[id] = fakeDemoAdmission{source: source, expiresAt: expiresAt}
	var once sync.Once
	release := func() {
		once.Do(func() {
			f.mu.Lock()
			f.released++
			f.mu.Unlock()
		})
	}
	return id, release, nil
}

func (f *fakeAdmission) ReleaseDemoSession(_ context.Context, admissionID string) (func(), error) {
	f.mu.Lock()
	f.acquired++
	if f.err != nil {
		f.released++
		f.mu.Unlock()
		return nil, f.err
	}
	if f.configured {
		f.released++
		f.mu.Unlock()
		return nil, instance.ErrAlreadyConfigured
	}
	delete(f.sessions, admissionID)
	f.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			f.released++
			f.mu.Unlock()
		})
	}, nil
}

func TestSessionAdmissionCookieAndRole(t *testing.T) {
	admission := &fakeAdmission{}
	service := New(admission, Options{})
	handler := service.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("demo request fell through") }))

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, APIPrefix+"/demo/session", nil))
	assertError(t, missing, http.StatusUnauthorized, "demo_session_invalid")

	request := httptest.NewRequest(http.MethodPost, "https://demo.example"+APIPrefix+"/demo/sessions", nil)
	request.Host = "demo.example"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != CookieName || len(cookie.Value) != 43 || !cookie.HttpOnly || !cookie.Secure || cookie.Path != APIPrefix || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe cookie: %+v", cookie)
	}
	if strings.Contains(response.Body.String(), cookie.Value) {
		t.Fatal("opaque cookie leaked into JSON")
	}
	var body struct {
		Account struct {
			User struct {
				Role string `json:"role"`
			} `json:"user"`
			Session struct {
				AuthorizationScope string `json:"authorizationScope"`
				Category           struct {
					ID    string  `json:"id"`
					Name  string  `json:"name"`
					Color *string `json:"color"`
					Icon  *string `json:"icon"`
				} `json:"category"`
			} `json:"session"`
			Profiles []struct {
				CategoryID string `json:"categoryId"`
				Category   struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"category"`
			} `json:"profiles"`
		} `json:"account"`
	}
	decodeTestJSON(t, response, &body)
	if body.Account.User.Role != "demo" {
		t.Fatalf("role = %q", body.Account.User.Role)
	}
	session := body.Account.Session
	if session.AuthorizationScope != "category" || session.Category.ID != DemoCategoryID ||
		session.Category.Name != "Uncategorized" || session.Category.Color != nil || session.Category.Icon != nil {
		t.Fatalf("unexpected demo session authorization: %+v", session)
	}
	if len(body.Account.Profiles) != 2 {
		t.Fatalf("profiles = %d, want 2", len(body.Account.Profiles))
	}
	for _, profile := range body.Account.Profiles {
		if profile.CategoryID != DemoCategoryID || profile.Category.ID != DemoCategoryID ||
			profile.Category.Name != "Uncategorized" {
			t.Fatalf("unexpected demo profile category: %+v", profile)
		}
	}
	sessionsResponse := httptest.NewRecorder()
	handler.ServeHTTP(
		sessionsResponse,
		demoRequest(http.MethodGet, APIPrefix+"/auth/sessions", nil, cookie),
	)
	if sessionsResponse.Code != http.StatusOK {
		t.Fatalf("sessions status = %d: %s", sessionsResponse.Code, sessionsResponse.Body.String())
	}
	var sessionsBody struct {
		Sessions []struct {
			AuthorizationScope string `json:"authorizationScope"`
			Category           struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"category"`
		} `json:"sessions"`
	}
	decodeTestJSON(t, sessionsResponse, &sessionsBody)
	if len(sessionsBody.Sessions) != 3 {
		t.Fatalf("sessions = %d, want 3", len(sessionsBody.Sessions))
	}
	for _, session := range sessionsBody.Sessions {
		if session.AuthorizationScope != "category" || session.Category.ID != DemoCategoryID ||
			session.Category.Name != "Uncategorized" {
			t.Fatalf("unexpected demo account session authorization: %+v", session)
		}
	}
	if admission.acquired != 2 || admission.released != 2 {
		t.Fatalf("admission counts = %d/%d, want 2/2", admission.acquired, admission.released)
	}
}

func TestPlaybackSourcesExposeStableRecoveryIdentities(t *testing.T) {
	service := New(&fakeAdmission{}, Options{})
	handler := service.Handler(http.NotFoundHandler())
	cookie := createCookie(t, handler)
	body := strings.NewReader(`{"mediaType":"movie","resourceId":"demo-signal-horizon","capabilities":{"streamingProtocols":["http"],"containers":["mp4"]}}`)
	request := demoRequest(http.MethodPost, APIPrefix+"/playback/sources", body, cookie)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("playback sources = %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		Sources []struct {
			SourceRef      string `json:"sourceRef"`
			StableIdentity string `json:"stableIdentity"`
		} `json:"sources"`
	}
	decodeTestJSON(t, response, &result)
	if len(result.Sources) != 2 || result.Sources[0].StableIdentity == "" || result.Sources[1].StableIdentity == "" ||
		result.Sources[0].StableIdentity == result.Sources[1].StableIdentity || result.Sources[0].StableIdentity == result.Sources[0].SourceRef {
		t.Fatalf("unsafe playback recovery identities: %+v", result.Sources)
	}
}

func TestDemoCookieNeverFallsThroughAndOriginIsChecked(t *testing.T) {
	admission := &fakeAdmission{}
	service := New(admission, Options{})
	forwarded := 0
	handler := service.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { forwarded++; w.WriteHeader(299) }))
	cookie := createCookie(t, handler)

	forbidden := httptest.NewRequest(http.MethodPost, "http://demo.example"+APIPrefix+"/setup", strings.NewReader(`{"setup":true}`))
	forbidden.Host = "demo.example"
	forbidden.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, forbidden)
	assertError(t, response, http.StatusForbidden, "demo_forbidden")

	admin := httptest.NewRequest(http.MethodGet, APIPrefix+"/users", nil)
	admin.AddCookie(cookie)
	adminResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminResponse, admin)
	assertError(t, adminResponse, http.StatusForbidden, "demo_forbidden")

	crossOrigin := httptest.NewRequest(http.MethodPost, "http://demo.example"+APIPrefix+"/demo/session/reset", nil)
	crossOrigin.Host = "demo.example"
	crossOrigin.Header.Set("Origin", "https://attacker.example")
	crossOrigin.AddCookie(cookie)
	originResponse := httptest.NewRecorder()
	handler.ServeHTTP(originResponse, crossOrigin)
	assertError(t, originResponse, http.StatusForbidden, "invalid_origin")
	if forwarded != 0 {
		t.Fatalf("real handler called %d times", forwarded)
	}
	admission.mu.Lock()
	acquired := admission.acquired
	admission.mu.Unlock()
	if acquired != 1 {
		t.Fatalf("origin and route rejections attempted setup admission: acquired=%d", acquired)
	}
}

func TestSessionStateIsolationAndReset(t *testing.T) {
	service := New(&fakeAdmission{}, Options{})
	handler := service.Handler(http.NotFoundHandler())
	first := createCookie(t, handler)
	second := createCookie(t, handler)

	remove := demoRequest(http.MethodDelete, APIPrefix+"/library/"+SignalMovieID, nil, first)
	removed := httptest.NewRecorder()
	handler.ServeHTTP(removed, remove)
	if removed.Code != http.StatusNoContent {
		t.Fatalf("remove status = %d: %s", removed.Code, removed.Body.String())
	}
	if libraryContains(t, handler, first, SignalMovieID) {
		t.Fatal("removed title remained in first session")
	}
	if !libraryContains(t, handler, second, SignalMovieID) {
		t.Fatal("first session mutation leaked into second session")
	}

	reset := demoRequest(http.MethodPost, APIPrefix+"/demo/session/reset", nil, first)
	resetResponse := httptest.NewRecorder()
	handler.ServeHTTP(resetResponse, reset)
	if resetResponse.Code != http.StatusOK {
		t.Fatalf("reset status = %d: %s", resetResponse.Code, resetResponse.Body.String())
	}
	if !libraryContains(t, handler, first, SignalMovieID) {
		t.Fatal("reset did not restore seed")
	}
}

func TestProfileSelectionIsSessionLocalAndNeverFallsThrough(t *testing.T) {
	service := New(&fakeAdmission{}, Options{})
	called := false
	handler := service.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	first := createCookie(t, handler)
	second := createCookie(t, handler)

	selectKids := demoRequest(http.MethodPost, APIPrefix+"/profiles/"+KidsProfileID+"/select", strings.NewReader(`{}`), first)
	selectKids.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, selectKids)
	if response.Code != http.StatusOK {
		t.Fatalf("select profile = %d: %s", response.Code, response.Body.String())
	}
	if activeProfile(t, handler, first) != KidsProfileID {
		t.Fatal("first session did not select Kids")
	}
	if activeProfile(t, handler, second) != AlexProfileID {
		t.Fatal("profile selection leaked into second session")
	}
	if called {
		t.Fatal("profile selection reached a real profile service")
	}
}

func TestExitInvalidatesAssetsAndRangeIsSessionBound(t *testing.T) {
	service := New(&fakeAdmission{}, Options{})
	handler := service.Handler(http.NotFoundHandler())
	cookie := createCookie(t, handler)

	rangeRequest := demoRequest(http.MethodGet, APIPrefix+"/demo/assets/demo-720p.mp4", nil, cookie)
	rangeRequest.Header.Set("Range", "bytes=0-15")
	rangeResponse := httptest.NewRecorder()
	handler.ServeHTTP(rangeResponse, rangeRequest)
	if rangeResponse.Code != http.StatusPartialContent || rangeResponse.Header().Get("Content-Type") != "video/mp4" || rangeResponse.Body.Len() != 16 {
		t.Fatalf("range response = %d %q %d", rangeResponse.Code, rangeResponse.Header().Get("Content-Type"), rangeResponse.Body.Len())
	}

	exit := demoRequest(http.MethodDelete, APIPrefix+"/demo/session", nil, cookie)
	exitResponse := httptest.NewRecorder()
	handler.ServeHTTP(exitResponse, exit)
	if exitResponse.Code != http.StatusNoContent {
		t.Fatalf("exit status = %d", exitResponse.Code)
	}
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, demoRequest(http.MethodGet, APIPrefix+"/demo/assets/artwork.svg", nil, cookie))
	assertError(t, invalid, http.StatusUnauthorized, "demo_session_invalid")
}

func TestSessionCapacityRefusesWithoutEvictionAndExpiryReleasesCapacity(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	randomBytes := make([]byte, 48*3)
	for i := range randomBytes {
		randomBytes[i] = byte(i + 1)
	}
	service := New(&fakeAdmission{}, Options{
		TTL: time.Hour, MaxSessions: 2, MaxSessionsPerSource: 2,
		Now: func() time.Time { return now }, Random: bytes.NewReader(randomBytes),
	})
	handler := service.Handler(http.NotFoundHandler())
	first := createCookie(t, handler)
	now = now.Add(time.Minute)
	second := createCookie(t, handler)
	now = now.Add(time.Minute)

	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodPost, APIPrefix+"/demo/sessions", nil))
	assertError(t, denied, http.StatusTooManyRequests, "demo_session_limit")
	if len(denied.Result().Cookies()) != 0 {
		t.Fatal("capacity denial mutated the client cookie")
	}
	assertSessionStatus(t, handler, first, http.StatusOK)
	assertSessionStatus(t, handler, second, http.StatusOK)

	now = now.Add(58 * time.Minute)
	third := createCookie(t, handler)
	assertSessionStatus(t, handler, first, http.StatusUnauthorized)
	assertSessionStatus(t, handler, second, http.StatusOK)
	assertSessionStatus(t, handler, third, http.StatusOK)
}

func TestSessionQuotaIsolatesTrustedSourcesAtExactAndNPlusOne(t *testing.T) {
	randomBytes := make([]byte, 48*4)
	for i := range randomBytes {
		randomBytes[i] = byte(i + 1)
	}
	service := New(&fakeAdmission{}, Options{
		MaxSessions: 3, MaxSessionsPerSource: 2, Random: bytes.NewReader(randomBytes),
	})
	handler := service.Handler(http.NotFoundHandler())
	first := createCookieFrom(t, handler, "198.51.100.10", nil)
	second := createCookieFrom(t, handler, "198.51.100.10", nil)

	spoofed := httptest.NewRequest(http.MethodPost, APIPrefix+"/demo/sessions", nil)
	spoofed = spoofed.WithContext(auth.WithClientIP(spoofed.Context(), "198.51.100.10"))
	spoofed.Header.Set("X-Forwarded-For", "203.0.113.20")
	spoofedResponse := httptest.NewRecorder()
	handler.ServeHTTP(spoofedResponse, spoofed)
	assertError(t, spoofedResponse, http.StatusTooManyRequests, "demo_session_limit")

	other := createCookieFrom(t, handler, "203.0.113.20", nil)
	globalDenied := httptest.NewRecorder()
	globalRequest := httptest.NewRequest(http.MethodPost, APIPrefix+"/demo/sessions", nil)
	globalRequest = globalRequest.WithContext(auth.WithClientIP(globalRequest.Context(), "192.0.2.30"))
	handler.ServeHTTP(globalDenied, globalRequest)
	assertError(t, globalDenied, http.StatusTooManyRequests, "demo_session_limit")
	assertSessionStatus(t, handler, first, http.StatusOK)
	assertSessionStatus(t, handler, second, http.StatusOK)
	assertSessionStatus(t, handler, other, http.StatusOK)

	replacement := createCookieFrom(t, handler, "198.51.100.10", first)
	assertSessionStatus(t, handler, first, http.StatusUnauthorized)
	assertSessionStatus(t, handler, second, http.StatusOK)
	assertSessionStatus(t, handler, other, http.StatusOK)
	assertSessionStatus(t, handler, replacement, http.StatusOK)
}

func TestSetupTransitionPurgesAllSessionsAndDeniesEveryEntry(t *testing.T) {
	admission := &fakeAdmission{}
	service := New(admission, Options{})
	handler := service.Handler(http.NotFoundHandler())
	cookie := createCookie(t, handler)
	admission.mu.Lock()
	admission.configured = true
	admission.mu.Unlock()

	old := httptest.NewRecorder()
	handler.ServeHTTP(old, demoRequest(http.MethodGet, APIPrefix+"/demo/session", nil, cookie))
	assertError(t, old, http.StatusGone, "demo_unavailable")
	fresh := httptest.NewRecorder()
	handler.ServeHTTP(fresh, httptest.NewRequest(http.MethodPost, APIPrefix+"/demo/sessions", nil))
	assertError(t, fresh, http.StatusGone, "demo_unavailable")
	direct := httptest.NewRecorder()
	handler.ServeHTTP(direct, demoRequest(http.MethodGet, APIPrefix+"/demo/assets/artwork.svg", nil, cookie))
	assertError(t, direct, http.StatusGone, "demo_unavailable")
	service.mu.Lock()
	count := len(service.sessions)
	service.mu.Unlock()
	if count != 0 {
		t.Fatalf("sessions after setup = %d", count)
	}
}

func TestPaginationBoundsAreCheckedBeforeOffsetArithmetic(t *testing.T) {
	service := New(&fakeAdmission{}, Options{})
	handler := service.Handler(http.NotFoundHandler())
	cookie := createCookie(t, handler)

	maxPage := maxInt()/100 + 1
	routes := []string{
		APIPrefix + "/library",
		APIPrefix + "/collections/" + HomeCollectionID + "/folders/" + SpotlightFolderID + "/items",
	}
	for _, route := range routes {
		t.Run(route, func(t *testing.T) {
			valid := httptest.NewRecorder()
			handler.ServeHTTP(valid, demoRequest(http.MethodGet, route+"?page="+strconv.Itoa(maxPage)+"&pageSize=100", nil, cookie))
			if valid.Code != http.StatusOK {
				t.Fatalf("maximum valid page = %d: %s", valid.Code, valid.Body.String())
			}

			for _, value := range []string{
				strconv.Itoa(maxPage + 1),
				strconv.Itoa(maxInt()),
				strconv.FormatUint(uint64(maxInt())+1, 10),
				"-" + strconv.FormatUint(uint64(maxInt())+1, 10),
				"-1",
				"not-an-integer",
			} {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, demoRequest(http.MethodGet, route+"?page="+value+"&pageSize=100", nil, cookie))
				assertError(t, response, http.StatusBadRequest, "invalid_request")
			}

			for _, value := range []string{
				"101",
				strconv.Itoa(maxInt()),
				strconv.FormatUint(uint64(maxInt())+1, 10),
				"-1",
				"not-an-integer",
			} {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, demoRequest(http.MethodGet, route+"?page=1&pageSize="+value, nil, cookie))
				assertError(t, response, http.StatusBadRequest, "invalid_request")
			}
		})
	}
}

func TestSearchPaginationBoundsOffsetAndLimit(t *testing.T) {
	service := New(&fakeAdmission{}, Options{})
	handler := service.Handler(http.NotFoundHandler())
	cookie := createCookie(t, handler)
	route := APIPrefix + "/addons/catalogs/search/movie"

	for _, query := range []string{
		"?skip=" + strconv.Itoa(maxInt()) + "&limit=100",
		"?skip=0&limit=100",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, demoRequest(http.MethodGet, route+query, nil, cookie))
		if response.Code != http.StatusOK {
			t.Fatalf("valid pagination %q = %d: %s", query, response.Code, response.Body.String())
		}
	}

	outOfRange := strconv.FormatUint(uint64(maxInt())+1, 10)
	for _, query := range []string{
		"?skip=-1&limit=100",
		"?skip=" + outOfRange + "&limit=100",
		"?skip=invalid&limit=100",
		"?skip=0&limit=101",
		"?skip=0&limit=" + strconv.Itoa(maxInt()),
		"?skip=0&limit=" + outOfRange,
		"?skip=0&limit=-1",
		"?skip=0&limit=invalid",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, demoRequest(http.MethodGet, route+query, nil, cookie))
		assertError(t, response, http.StatusBadRequest, "invalid_request")
	}
}

func TestMalformedMutationsAreRejectedWithoutChangingState(t *testing.T) {
	service := New(&fakeAdmission{}, Options{})
	handler := service.Handler(http.NotFoundHandler())
	cookie := createCookie(t, handler)
	request := demoRequest(http.MethodPut, APIPrefix+"/progress/"+SignalMovieID, strings.NewReader(`{"positionSeconds":1,"durationSeconds":12,"completed":false,"unknown":true}`), cookie)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertError(t, response, http.StatusBadRequest, "invalid_request")

	progress := httptest.NewRecorder()
	handler.ServeHTTP(progress, demoRequest(http.MethodGet, APIPrefix+"/progress/"+SignalMovieID, nil, cookie))
	if !strings.Contains(progress.Body.String(), `"positionSeconds":842`) {
		t.Fatalf("progress changed after malformed request: %s", progress.Body.String())
	}
}

func TestSlowMutationBodyIsReadBeforeSetupAdmission(t *testing.T) {
	admission := &fakeAdmission{}
	service := New(admission, Options{})
	handler := service.Handler(http.NotFoundHandler())
	cookie := createCookie(t, handler)
	current, _ := service.session(cookie.Value)
	if current == nil {
		t.Fatal("created demo session was not retained")
	}

	body := newBlockingRequestBody(`{"positionSeconds":1,"durationSeconds":12,"completed":false}`)
	request := httptest.NewRequest(http.MethodPut, APIPrefix+"/progress/"+SignalMovieID, body)
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()

	select {
	case <-body.blocked:
	case <-time.After(time.Second):
		t.Fatal("mutation did not block while waiting for the end of its body")
	}
	admission.mu.Lock()
	acquiredBeforeSetup := admission.acquired
	admission.configured = true
	admission.mu.Unlock()
	if acquiredBeforeSetup != 1 {
		close(body.continueReading)
		t.Fatalf("admission attempts before body completion = %d, want only session creation", acquiredBeforeSetup)
	}

	close(body.continueReading)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("mutation did not finish after its body completed")
	}
	assertError(t, response, http.StatusGone, "demo_unavailable")
	current.mu.Lock()
	progress := current.state.profiles[AlexProfileID].progress[SignalMovieID]
	current.mu.Unlock()
	if progress.PositionSeconds != 842 {
		t.Fatalf("progress changed after setup won admission: %+v", progress)
	}
}

func TestMutationBodySnapshotPreservesJSONResponses(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		service := New(&fakeAdmission{}, Options{})
		handler := service.Handler(http.NotFoundHandler())
		cookie := createCookie(t, handler)
		request := demoRequest(http.MethodPut, APIPrefix+"/progress/"+SignalMovieID, strings.NewReader(`{"positionSeconds":1,"durationSeconds":12,"completed":false}`), cookie)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("valid JSON status = %d: %s", response.Code, response.Body.String())
		}
		var result struct {
			PositionSeconds int  `json:"positionSeconds"`
			DurationSeconds int  `json:"durationSeconds"`
			Completed       bool `json:"completed"`
		}
		decodeTestJSON(t, response, &result)
		if result.PositionSeconds != 1 || result.DurationSeconds != 12 || result.Completed {
			t.Fatalf("valid JSON response changed: %+v", result)
		}
	})

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "invalid", body: `{"positionSeconds":1,"durationSeconds":12,"completed":false,"unknown":true}`},
		{name: "too large", body: `{"positionSeconds":1,"durationSeconds":12,"completed":false,"padding":"` + strings.Repeat("x", maxJSONBodyBytes) + `"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := New(&fakeAdmission{}, Options{})
			handler := service.Handler(http.NotFoundHandler())
			cookie := createCookie(t, handler)
			request := demoRequest(http.MethodPut, APIPrefix+"/progress/"+SignalMovieID, strings.NewReader(test.body), cookie)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assertError(t, response, http.StatusBadRequest, "invalid_request")

			legacyRequest := httptest.NewRequest(http.MethodPut, APIPrefix+"/progress/"+SignalMovieID, strings.NewReader(test.body))
			legacyRequest.Header.Set("Content-Type", "application/json")
			var input struct {
				PositionSeconds int   `json:"positionSeconds"`
				DurationSeconds int   `json:"durationSeconds"`
				Completed       bool  `json:"completed"`
				ExpectedVersion int64 `json:"expectedVersion"`
			}
			legacyResponse := httptest.NewRecorder()
			if err := decodeStrict(legacyRequest, &input); err != nil {
				writeError(legacyResponse, http.StatusBadRequest, "invalid_request", err.Error())
			}
			if response.Code != legacyResponse.Code || response.Body.String() != legacyResponse.Body.String() {
				t.Fatalf("response changed after body snapshot:\n got: %d %s want: %d %s", response.Code, response.Body.String(), legacyResponse.Code, legacyResponse.Body.String())
			}
		})
	}
}

type blockingRequestBody struct {
	prefix          *strings.Reader
	blocked         chan struct{}
	continueReading chan struct{}
	once            sync.Once
}

func newBlockingRequestBody(prefix string) *blockingRequestBody {
	return &blockingRequestBody{
		prefix:          strings.NewReader(prefix),
		blocked:         make(chan struct{}),
		continueReading: make(chan struct{}),
	}
}

func (b *blockingRequestBody) Read(destination []byte) (int, error) {
	if b.prefix.Len() != 0 {
		return b.prefix.Read(destination)
	}
	b.once.Do(func() { close(b.blocked) })
	<-b.continueReading
	return 0, io.EOF
}

func (*blockingRequestBody) Close() error {
	return nil
}

func TestAdmissionFailureDoesNotReachRealServices(t *testing.T) {
	admission := &fakeAdmission{err: errors.New("database unavailable")}
	called := false
	service := New(admission, Options{})
	handler := service.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, APIPrefix+"/demo/sessions", nil))
	assertError(t, response, http.StatusInternalServerError, "demo_internal_error")
	if called {
		t.Fatal("real service reached after failed admission")
	}
}

func TestPlaybackEntriesHaveStableSessionAndGlobalCaps(t *testing.T) {
	service := New(&fakeAdmission{}, Options{MaxSessionsPerSource: defaultMaxSessions})
	handler := service.Handler(http.NotFoundHandler())
	first := createCookie(t, handler)

	firstIDs := make([]string, 0, maxPlaybackEntriesPerSession)
	for range maxPlaybackEntriesPerSession {
		firstIDs = append(firstIDs, resolvePlayback(t, handler, first, http.StatusCreated))
	}
	limited := resolvePlaybackResponse(t, handler, first)
	assertError(t, limited, http.StatusTooManyRequests, "demo_playback_limit_reached")

	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, demoRequest(http.MethodDelete, APIPrefix+"/playback/sessions/"+firstIDs[0], nil, first))
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete terminal playback = %d: %s", deleted.Code, deleted.Body.String())
	}
	resolvePlayback(t, handler, first, http.StatusCreated)
	assertError(t, resolvePlaybackResponse(t, handler, first), http.StatusTooManyRequests, "demo_playback_limit_reached")

	cookies := []*http.Cookie{first}
	for len(cookies)*maxPlaybackEntriesPerSession < maxPlaybackEntriesAcrossSessions {
		cookie := createCookie(t, handler)
		cookies = append(cookies, cookie)
		for range maxPlaybackEntriesPerSession {
			resolvePlayback(t, handler, cookie, http.StatusCreated)
		}
	}
	extra := createCookie(t, handler)
	assertError(t, resolvePlaybackResponse(t, handler, extra), http.StatusTooManyRequests, "demo_playback_limit_reached")

	freed := httptest.NewRecorder()
	handler.ServeHTTP(freed, demoRequest(http.MethodDelete, APIPrefix+"/playback/sessions/"+firstIDs[1], nil, first))
	if freed.Code != http.StatusNoContent {
		t.Fatalf("free global playback slot = %d: %s", freed.Code, freed.Body.String())
	}
	resolvePlayback(t, handler, extra, http.StatusCreated)
	assertError(t, resolvePlaybackResponse(t, handler, extra), http.StatusTooManyRequests, "demo_playback_limit_reached")
}

func TestOldPlaybackEntriesArePrunedBeforeAllocation(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	service := New(&fakeAdmission{}, Options{Now: func() time.Time { return now }})
	handler := service.Handler(http.NotFoundHandler())
	cookie := createCookie(t, handler)

	ids := make([]string, 0, maxPlaybackEntriesPerSession)
	for range maxPlaybackEntriesPerSession {
		ids = append(ids, resolvePlayback(t, handler, cookie, http.StatusCreated))
	}
	now = now.Add(playbackStateTTL + time.Second)
	resolvePlayback(t, handler, cookie, http.StatusCreated)

	expired := httptest.NewRecorder()
	handler.ServeHTTP(expired, demoRequest(http.MethodDelete, APIPrefix+"/playback/sessions/"+ids[0], nil, cookie))
	assertError(t, expired, http.StatusNotFound, "playback_session_not_found")
	for i := 1; i < maxPlaybackEntriesPerSession; i++ {
		resolvePlayback(t, handler, cookie, http.StatusCreated)
	}
	assertError(t, resolvePlaybackResponse(t, handler, cookie), http.StatusTooManyRequests, "demo_playback_limit_reached")
}

func TestAdmissionIsReleasedBeforeSlowResponseWrite(t *testing.T) {
	admission := &fakeAdmission{}
	service := New(admission, Options{})
	handler := service.Handler(http.NotFoundHandler())
	cookie := createCookie(t, handler)

	for _, test := range []struct {
		name   string
		target string
	}{
		{name: "materialized JSON", target: APIPrefix + "/auth/me"},
		{name: "prepared video range", target: APIPrefix + "/demo/assets/demo-720p.mp4"},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := newSlowResponseWriter()
			request := demoRequest(http.MethodGet, test.target, nil, cookie)
			request.Header.Set("Range", "bytes=0-15")
			done := make(chan struct{})
			go func() {
				handler.ServeHTTP(writer, request)
				close(done)
			}()

			select {
			case <-writer.writing:
			case <-time.After(time.Second):
				t.Fatal("demo response did not begin writing")
			}
			admission.mu.Lock()
			acquired, released := admission.acquired, admission.released
			admission.mu.Unlock()
			if acquired != released {
				close(writer.continueWriting)
				t.Fatalf("setup admission retained during slow write: acquired=%d released=%d", acquired, released)
			}
			close(writer.continueWriting)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("demo response did not finish after writer resumed")
			}
		})
	}
}

type slowResponseWriter struct {
	header          http.Header
	writing         chan struct{}
	continueWriting chan struct{}
	once            sync.Once
}

func newSlowResponseWriter() *slowResponseWriter {
	return &slowResponseWriter{
		header:          make(http.Header),
		writing:         make(chan struct{}),
		continueWriting: make(chan struct{}),
	}
}

func (w *slowResponseWriter) Header() http.Header {
	return w.header
}

func (w *slowResponseWriter) WriteHeader(int) {}

func (w *slowResponseWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.writing) })
	<-w.continueWriting
	return len(data), nil
}

func resolvePlayback(t *testing.T, handler http.Handler, cookie *http.Cookie, status int) string {
	t.Helper()
	response := resolvePlaybackResponse(t, handler, cookie)
	if response.Code != status {
		t.Fatalf("resolve playback = %d, want %d: %s", response.Code, status, response.Body.String())
	}
	var body struct {
		ID string `json:"id"`
	}
	decodeTestJSON(t, response, &body)
	if body.ID == "" {
		t.Fatal("resolved playback id is empty")
	}
	return body.ID
}

func resolvePlaybackResponse(t *testing.T, handler http.Handler, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	body := strings.NewReader(`{"sourceRef":"demo-source-720-` + SignalMovieID + `","titleId":"` + SignalMovieID + `"}`)
	request := demoRequest(http.MethodPost, APIPrefix+"/playback/resolve", body, cookie)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func createCookie(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	return createCookieFrom(t, handler, "", nil)
}

func createCookieFrom(t *testing.T, handler http.Handler, clientIP string, replaced *http.Cookie) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, APIPrefix+"/demo/sessions", nil)
	if clientIP != "" {
		request = request.WithContext(auth.WithClientIP(request.Context(), clientIP))
	}
	if replaced != nil {
		request.AddCookie(replaced)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create session = %d: %s", response.Code, response.Body.String())
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == CookieName {
			return cookie
		}
	}
	t.Fatal("demo cookie missing")
	return nil
}

func demoRequest(method, target string, body *strings.Reader, cookie *http.Cookie) *http.Request {
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, body)
	}
	request.AddCookie(cookie)
	return request
}

func libraryContains(t *testing.T, handler http.Handler, cookie *http.Cookie, id string) bool {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, demoRequest(http.MethodGet, APIPrefix+"/library?page=1&pageSize=100", nil, cookie))
	if response.Code != http.StatusOK {
		t.Fatalf("library = %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Items []struct {
			TitleID string `json:"titleId"`
		} `json:"items"`
	}
	decodeTestJSON(t, response, &body)
	for _, item := range body.Items {
		if item.TitleID == id {
			return true
		}
	}
	return false
}

func activeProfile(t *testing.T, handler http.Handler, cookie *http.Cookie) string {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, demoRequest(http.MethodGet, APIPrefix+"/auth/me", nil, cookie))
	if response.Code != http.StatusOK {
		t.Fatalf("account = %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Session struct {
			ActiveProfile *struct {
				ID string `json:"id"`
			} `json:"activeProfile"`
		} `json:"session"`
	}
	decodeTestJSON(t, response, &body)
	if body.Session.ActiveProfile == nil {
		return ""
	}
	return body.Session.ActiveProfile.ID
}

func assertSessionStatus(t *testing.T, handler http.Handler, cookie *http.Cookie, status int) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, demoRequest(http.MethodGet, APIPrefix+"/demo/session", nil, cookie))
	if response.Code != status {
		t.Fatalf("session status = %d, want %d: %s", response.Code, status, response.Body.String())
	}
}
func assertError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d: %s", response.Code, status, response.Body.String())
	}
	var body errorEnvelope
	decodeTestJSON(t, response, &body)
	if body.Error.Code != code {
		t.Fatalf("code = %q, want %q", body.Error.Code, code)
	}
}
func decodeTestJSON(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), destination); err != nil {
		t.Fatalf("decode response: %v: %s", err, response.Body.String())
	}
}
