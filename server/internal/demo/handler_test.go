package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/instance"
)

type fakeAdmission struct {
	mu sync.Mutex
	configured bool
	err error
	acquired int
	released int
}

func (f *fakeAdmission) AcquireSetupPending(context.Context) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquired++
	if f.err != nil { return nil, f.err }
	if f.configured { return nil, instance.ErrAlreadyConfigured }
	var once sync.Once
	return func() { once.Do(func() { f.mu.Lock(); f.released++; f.mu.Unlock() }) }, nil
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
	if response.Code != http.StatusCreated { t.Fatalf("create status = %d: %s", response.Code, response.Body.String()) }
	cookies := response.Result().Cookies()
	if len(cookies) != 1 { t.Fatalf("cookies = %d, want 1", len(cookies)) }
	cookie := cookies[0]
	if cookie.Name != CookieName || len(cookie.Value) != 43 || !cookie.HttpOnly || !cookie.Secure || cookie.Path != APIPrefix || cookie.SameSite != http.SameSiteStrictMode { t.Fatalf("unsafe cookie: %+v", cookie) }
	if strings.Contains(response.Body.String(), cookie.Value) { t.Fatal("opaque cookie leaked into JSON") }
	var body struct { Account struct { User struct { Role string `json:"role"` } `json:"user"` } `json:"account"` }
	decodeTestJSON(t, response, &body)
	if body.Account.User.Role != "demo" { t.Fatalf("role = %q", body.Account.User.Role) }
	if admission.acquired != 2 || admission.released != 2 { t.Fatalf("admission counts = %d/%d", admission.acquired, admission.released) }
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
	if forwarded != 0 { t.Fatalf("real handler called %d times", forwarded) }
}

func TestSessionStateIsolationAndReset(t *testing.T) {
	service := New(&fakeAdmission{}, Options{})
	handler := service.Handler(http.NotFoundHandler())
	first := createCookie(t, handler)
	second := createCookie(t, handler)

	remove := demoRequest(http.MethodDelete, APIPrefix+"/library/"+SignalMovieID, nil, first)
	removed := httptest.NewRecorder(); handler.ServeHTTP(removed, remove)
	if removed.Code != http.StatusNoContent { t.Fatalf("remove status = %d: %s", removed.Code, removed.Body.String()) }
	if libraryContains(t, handler, first, SignalMovieID) { t.Fatal("removed title remained in first session") }
	if !libraryContains(t, handler, second, SignalMovieID) { t.Fatal("first session mutation leaked into second session") }

	reset := demoRequest(http.MethodPost, APIPrefix+"/demo/session/reset", nil, first)
	resetResponse := httptest.NewRecorder(); handler.ServeHTTP(resetResponse, reset)
	if resetResponse.Code != http.StatusOK { t.Fatalf("reset status = %d: %s", resetResponse.Code, resetResponse.Body.String()) }
	if !libraryContains(t, handler, first, SignalMovieID) { t.Fatal("reset did not restore seed") }
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
	if response.Code != http.StatusOK { t.Fatalf("select profile = %d: %s", response.Code, response.Body.String()) }
	if activeProfile(t, handler, first) != KidsProfileID { t.Fatal("first session did not select Kids") }
	if activeProfile(t, handler, second) != AlexProfileID { t.Fatal("profile selection leaked into second session") }
	if called { t.Fatal("profile selection reached a real profile service") }
}

func TestExitInvalidatesAssetsAndRangeIsSessionBound(t *testing.T) {
	service := New(&fakeAdmission{}, Options{})
	handler := service.Handler(http.NotFoundHandler())
	cookie := createCookie(t, handler)

	rangeRequest := demoRequest(http.MethodGet, APIPrefix+"/demo/assets/demo-720p.mp4", nil, cookie)
	rangeRequest.Header.Set("Range", "bytes=0-15")
	rangeResponse := httptest.NewRecorder(); handler.ServeHTTP(rangeResponse, rangeRequest)
	if rangeResponse.Code != http.StatusPartialContent || rangeResponse.Header().Get("Content-Type") != "video/mp4" || rangeResponse.Body.Len() != 16 { t.Fatalf("range response = %d %q %d", rangeResponse.Code, rangeResponse.Header().Get("Content-Type"), rangeResponse.Body.Len()) }

	exit := demoRequest(http.MethodDelete, APIPrefix+"/demo/session", nil, cookie)
	exitResponse := httptest.NewRecorder(); handler.ServeHTTP(exitResponse, exit)
	if exitResponse.Code != http.StatusNoContent { t.Fatalf("exit status = %d", exitResponse.Code) }
	invalid := httptest.NewRecorder(); handler.ServeHTTP(invalid, demoRequest(http.MethodGet, APIPrefix+"/demo/assets/artwork.svg", nil, cookie))
	assertError(t, invalid, http.StatusUnauthorized, "demo_session_invalid")
}

func TestAbsoluteTTLAndOldestSessionEviction(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	randomBytes := make([]byte, 48*4)
	for i := range randomBytes { randomBytes[i] = byte(i + 1) }
	service := New(&fakeAdmission{}, Options{TTL: time.Hour, MaxSessions: 2, Now: func() time.Time { return now }, Random: bytes.NewReader(randomBytes)})
	handler := service.Handler(http.NotFoundHandler())
	first := createCookie(t, handler)
	now = now.Add(time.Minute)
	second := createCookie(t, handler)
	now = now.Add(time.Minute)
	third := createCookie(t, handler)

	assertSessionStatus(t, handler, first, http.StatusUnauthorized)
	assertSessionStatus(t, handler, second, http.StatusOK)
	now = now.Add(59 * time.Minute)
	assertSessionStatus(t, handler, second, http.StatusUnauthorized)
	assertSessionStatus(t, handler, third, http.StatusOK)
}

func TestSetupTransitionPurgesAllSessionsAndDeniesEveryEntry(t *testing.T) {
	admission := &fakeAdmission{}
	service := New(admission, Options{})
	handler := service.Handler(http.NotFoundHandler())
	cookie := createCookie(t, handler)
	admission.mu.Lock(); admission.configured = true; admission.mu.Unlock()

	old := httptest.NewRecorder(); handler.ServeHTTP(old, demoRequest(http.MethodGet, APIPrefix+"/demo/session", nil, cookie))
	assertError(t, old, http.StatusGone, "demo_unavailable")
	fresh := httptest.NewRecorder(); handler.ServeHTTP(fresh, httptest.NewRequest(http.MethodPost, APIPrefix+"/demo/sessions", nil))
	assertError(t, fresh, http.StatusGone, "demo_unavailable")
	direct := httptest.NewRecorder(); handler.ServeHTTP(direct, demoRequest(http.MethodGet, APIPrefix+"/demo/assets/artwork.svg", nil, cookie))
	assertError(t, direct, http.StatusGone, "demo_unavailable")
	service.mu.Lock(); count := len(service.sessions); service.mu.Unlock()
	if count != 0 { t.Fatalf("sessions after setup = %d", count) }
}

func TestMalformedMutationsAreRejectedWithoutChangingState(t *testing.T) {
	service := New(&fakeAdmission{}, Options{})
	handler := service.Handler(http.NotFoundHandler())
	cookie := createCookie(t, handler)
	request := demoRequest(http.MethodPut, APIPrefix+"/progress/"+SignalMovieID, strings.NewReader(`{"positionSeconds":1,"durationSeconds":12,"completed":false,"unknown":true}`), cookie)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder(); handler.ServeHTTP(response, request)
	assertError(t, response, http.StatusBadRequest, "invalid_request")

	progress := httptest.NewRecorder(); handler.ServeHTTP(progress, demoRequest(http.MethodGet, APIPrefix+"/progress/"+SignalMovieID, nil, cookie))
	if !strings.Contains(progress.Body.String(), `"positionSeconds":842`) { t.Fatalf("progress changed after malformed request: %s", progress.Body.String()) }
}

func TestAdmissionFailureDoesNotReachRealServices(t *testing.T) {
	admission := &fakeAdmission{err: errors.New("database unavailable")}
	called := false
	service := New(admission, Options{})
	handler := service.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	response := httptest.NewRecorder(); handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, APIPrefix+"/demo/sessions", nil))
	assertError(t, response, http.StatusInternalServerError, "demo_internal_error")
	if called { t.Fatal("real service reached after failed admission") }
}

func createCookie(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, APIPrefix+"/demo/sessions", nil))
	if response.Code != http.StatusCreated { t.Fatalf("create session = %d: %s", response.Code, response.Body.String()) }
	for _, cookie := range response.Result().Cookies() { if cookie.Name == CookieName { return cookie } }
	t.Fatal("demo cookie missing"); return nil
}

func demoRequest(method, target string, body *strings.Reader, cookie *http.Cookie) *http.Request {
	var request *http.Request
	if body == nil { request = httptest.NewRequest(method, target, nil) } else { request = httptest.NewRequest(method, target, body) }
	request.AddCookie(cookie)
	return request
}

func libraryContains(t *testing.T, handler http.Handler, cookie *http.Cookie, id string) bool {
	t.Helper(); response := httptest.NewRecorder(); handler.ServeHTTP(response, demoRequest(http.MethodGet, APIPrefix+"/library?page=1&pageSize=100", nil, cookie))
	if response.Code != http.StatusOK { t.Fatalf("library = %d: %s", response.Code, response.Body.String()) }
	var body struct { Items []struct { TitleID string `json:"titleId"` } `json:"items"` }; decodeTestJSON(t,response,&body)
	for _, item := range body.Items { if item.TitleID == id { return true } }; return false
}

func activeProfile(t *testing.T, handler http.Handler, cookie *http.Cookie) string {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, demoRequest(http.MethodGet, APIPrefix+"/auth/me", nil, cookie))
	if response.Code != http.StatusOK { t.Fatalf("account = %d: %s", response.Code, response.Body.String()) }
	var body struct { Session struct { ActiveProfile *struct { ID string `json:"id"` } `json:"activeProfile"` } `json:"session"` }
	decodeTestJSON(t, response, &body)
	if body.Session.ActiveProfile == nil { return "" }
	return body.Session.ActiveProfile.ID
}

func assertSessionStatus(t *testing.T, handler http.Handler, cookie *http.Cookie, status int) { t.Helper(); response:=httptest.NewRecorder(); handler.ServeHTTP(response,demoRequest(http.MethodGet,APIPrefix+"/demo/session",nil,cookie)); if response.Code!=status { t.Fatalf("session status = %d, want %d: %s",response.Code,status,response.Body.String()) } }
func assertError(t *testing.T,response *httptest.ResponseRecorder,status int,code string){t.Helper();if response.Code!=status{t.Fatalf("status = %d, want %d: %s",response.Code,status,response.Body.String())};var body errorEnvelope;decodeTestJSON(t,response,&body);if body.Error.Code!=code{t.Fatalf("code = %q, want %q",body.Error.Code,code)}}
func decodeTestJSON(t *testing.T,response *httptest.ResponseRecorder,destination any){t.Helper();if err:=json.Unmarshal(response.Body.Bytes(),destination);err!=nil{t.Fatalf("decode response: %v: %s",err,response.Body.String())}}
