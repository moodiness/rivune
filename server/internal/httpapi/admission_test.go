package httpapi

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

func TestPublicAdmissionBoundsConcurrentWorkBySourceAndGlobally(t *testing.T) {
	admission := newRequestAdmission(2, 1, 10, 10, time.Minute)
	firstRelease, _, admitted := admission.acquire("198.51.100.1")
	if !admitted {
		t.Fatal("first source was not admitted")
	}
	defer firstRelease()

	if _, retryAfter, admitted := admission.acquire("198.51.100.1"); admitted || retryAfter != time.Second {
		t.Fatalf("same-source concurrent admission = %v, retry = %s", admitted, retryAfter)
	}
	secondRelease, _, admitted := admission.acquire("198.51.100.2")
	if !admitted {
		t.Fatal("second source was not admitted within global capacity")
	}
	defer secondRelease()
	if _, retryAfter, admitted := admission.acquire("198.51.100.3"); admitted || retryAfter != time.Second {
		t.Fatalf("global concurrent admission = %v, retry = %s", admitted, retryAfter)
	}
}

func TestCalendarFeedAdmissionEnforcesConcurrencyAndAttemptBudget(t *testing.T) {
	admission := newCalendarFeedAdmission()
	first, _, admitted := admission.acquire("198.51.100.1")
	if !admitted {
		t.Fatal("first same-source feed was not admitted")
	}
	second, _, admitted := admission.acquire("198.51.100.1")
	if !admitted {
		t.Fatal("second same-source feed was not admitted")
	}
	if _, retryAfter, admitted := admission.acquire("198.51.100.1"); admitted || retryAfter != time.Second {
		t.Fatalf("third same-source feed admitted=%v retry=%s", admitted, retryAfter)
	}
	first()
	second()

	admission = newCalendarFeedAdmission()
	releases := make([]func(), 0, calendarFeedGlobalConcurrency)
	for index := range calendarFeedGlobalConcurrency {
		release, _, admitted := admission.acquire("198.51.100." + strconv.Itoa(index+1))
		if !admitted {
			t.Fatalf("feed %d was refused below global concurrency", index+1)
		}
		releases = append(releases, release)
	}
	if _, retryAfter, admitted := admission.acquire("203.0.113.1"); admitted || retryAfter != time.Second {
		t.Fatalf("ninth concurrent feed admitted=%v retry=%s", admitted, retryAfter)
	}
	for _, release := range releases {
		release()
	}

	admission = newCalendarFeedAdmission()
	current := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	admission.now = func() time.Time { return current }
	for attempt := range calendarFeedSourceAttempts {
		release, _, admitted := admission.acquire("192.0.2.1")
		if !admitted {
			t.Fatalf("attempt %d was refused within the feed budget", attempt+1)
		}
		release()
	}
	if _, retryAfter, admitted := admission.acquire("192.0.2.1"); admitted || retryAfter != time.Minute {
		t.Fatalf("attempt above feed budget admitted=%v retry=%s", admitted, retryAfter)
	}
	if admission.maximumSources != calendarFeedTrackedSources {
		t.Fatalf("tracked source capacity=%d want %d", admission.maximumSources, calendarFeedTrackedSources)
	}
}

func TestPublicAdmissionUsesIPv4AndIPv6NetworkGranularity(t *testing.T) {
	admission := newRequestAdmission(2, 1, 10, 10, time.Minute)
	firstRelease, _, admitted := admission.acquire("2001:db8:1:2::1")
	if !admitted {
		t.Fatal("first IPv6 source was not admitted")
	}
	defer firstRelease()
	if _, retryAfter, admitted := admission.acquire("2001:db8:1:2::ffff"); admitted || retryAfter != time.Second {
		t.Fatalf("same-/64 admission = %v, retry = %s; want a shared source limit", admitted, retryAfter)
	}
	secondRelease, _, admitted := admission.acquire("2001:db8:1:3::1")
	if !admitted {
		t.Fatal("distinct IPv6 /64 was not admitted")
	}
	defer secondRelease()
	if got := len(admission.sources); got != 2 {
		t.Fatalf("tracked IPv6 networks = %d, want 2", got)
	}

	if networkAdmissionSource("::ffff:192.0.2.10") != networkAdmissionSource("192.0.2.10") {
		t.Fatal("IPv4 and its IPv4-mapped representation did not share a source key")
	}
	if networkAdmissionSource("") != "unknown" {
		t.Fatal("missing sources did not use the fail-closed sentinel")
	}
}

func TestDeviceCodeAdmissionReadsBodyBeforeTrackingSource(t *testing.T) {
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{deviceAuthorization: auth.DeviceAuthorization{
		DeviceCode: "rivune_dc_test",
		UserCode:   "TEST-CODE",
		ExpiresAt:  time.Now().Add(time.Minute),
		Interval:   5 * time.Second,
	}}
	releaseBody := make(chan struct{})
	body := &gatedRequestBody{
		started: make(chan struct{}),
		release: releaseBody,
		reader:  strings.NewReader(`{"deviceName":"Television","platform":"tv"}`),
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/device-code", nil)
	request.Body = body
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "[2001:db8:1:2::42]:4000"
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		api.Handler().ServeHTTP(response, request)
		close(done)
	}()
	<-body.started

	api.deviceCodeAdmission.mu.Lock()
	inFlight := api.deviceCodeAdmission.inFlight
	tracked := len(api.deviceCodeAdmission.sources)
	api.deviceCodeAdmission.mu.Unlock()
	if inFlight != 0 || tracked != 0 {
		t.Fatalf("blocked device body admission state = in-flight %d, sources %d; want zero", inFlight, tracked)
	}

	close(releaseBody)
	<-done
	if response.Code != http.StatusCreated {
		t.Fatalf("device authorization status = %d, want 201: %s", response.Code, response.Body.String())
	}
	api.deviceCodeAdmission.mu.Lock()
	_, networkTracked := api.deviceCodeAdmission.sources["2001:db8:1:2::/64"]
	_, addressTracked := api.deviceCodeAdmission.sources["2001:db8:1:2::42"]
	api.deviceCodeAdmission.mu.Unlock()
	if !networkTracked || addressTracked {
		t.Fatalf("device admission keys = network %v, exact address %v; want true/false", networkTracked, addressTracked)
	}
}

func TestCredentialAdmissionReadsBlockedBodyBeforeAcquiringSlot(t *testing.T) {
	authService := &fakeAuthService{}
	api := testAPI(&fakeInstanceService{})
	api.auth = authService

	releaseBody := make(chan struct{})
	slowBody := &gatedRequestBody{
		started: make(chan struct{}),
		release: releaseBody,
		reader: strings.NewReader(
			`{"username":"slow","password":"secret","device":{"name":"Phone","platform":"ios"}}`,
		),
	}
	slowRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	slowRequest.Body = slowBody
	slowRequest.Header.Set("Content-Type", "application/json")
	slowRequest.RemoteAddr = "198.51.100.30:4000"
	slowDone := make(chan struct{})
	go func() {
		api.Handler().ServeHTTP(httptest.NewRecorder(), slowRequest)
		close(slowDone)
	}()
	defer func() {
		close(releaseBody)
		<-slowDone
	}()
	<-slowBody.started

	api.credentialAdmission.mu.Lock()
	inFlight := api.credentialAdmission.inFlight
	_, tracked := api.credentialAdmission.sources["198.51.100.30"]
	api.credentialAdmission.mu.Unlock()
	if inFlight != 0 || tracked {
		t.Fatalf("blocked body admission state = in-flight %d, source tracked %v; want zero and false", inFlight, tracked)
	}

	completeRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/login",
		strings.NewReader(`{"username":"ready","password":"secret","device":{"name":"Phone","platform":"ios"}}`),
	)
	completeRequest.Header.Set("Content-Type", "application/json")
	completeRequest.RemoteAddr = "198.51.100.31:4000"
	completeResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(completeResponse, completeRequest)
	if completeResponse.Code != http.StatusOK || authService.loginCalls != 1 {
		t.Fatalf("complete credential operation = status %d, login calls %d; want 200 and one call: %s", completeResponse.Code, authService.loginCalls, completeResponse.Body.String())
	}
}

func TestCredentialAdmissionStillRejectsFifthEnteredOperation(t *testing.T) {
	releaseOperations := make(chan struct{})
	authService := &blockingLoginService{
		fakeAuthService: &fakeAuthService{},
		entered:         make(chan struct{}, credentialAdmissionGlobalConcurrency),
		release:         releaseOperations,
	}
	api := testAPI(&fakeInstanceService{})
	api.auth = authService
	handler := api.Handler()

	var requests sync.WaitGroup
	requests.Add(credentialAdmissionGlobalConcurrency)
	for index := range credentialAdmissionGlobalConcurrency {
		go func() {
			defer requests.Done()
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/auth/login",
				strings.NewReader(`{"username":"user`+strconv.Itoa(index)+`","password":"secret","device":{"name":"Phone","platform":"ios"}}`),
			)
			request.Header.Set("Content-Type", "application/json")
			request.RemoteAddr = "198.51.100." + strconv.Itoa(index+40) + ":4000"
			handler.ServeHTTP(httptest.NewRecorder(), request)
		}()
	}
	defer func() {
		close(releaseOperations)
		requests.Wait()
	}()
	for range credentialAdmissionGlobalConcurrency {
		<-authService.entered
	}

	blockedRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/login",
		strings.NewReader(`{"username":"fifth","password":"secret","device":{"name":"Phone","platform":"ios"}}`),
	)
	blockedRequest.Header.Set("Content-Type", "application/json")
	blockedRequest.RemoteAddr = "203.0.113.50:4000"
	blockedResponse := httptest.NewRecorder()
	handler.ServeHTTP(blockedResponse, blockedRequest)
	if blockedResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("fifth operation status = %d, want 429: %s", blockedResponse.Code, blockedResponse.Body.String())
	}
	var body errorEnvelope
	decodeResponse(t, blockedResponse, &body)
	if body.Error.Code != "rate_limited" {
		t.Fatalf("fifth operation error code = %q, want rate_limited", body.Error.Code)
	}
}

func TestCredentialBodyErrorsPreserveInvalidRequestEnvelope(t *testing.T) {
	authService := &fakeAuthService{}
	instanceService := &fakeInstanceService{}
	api := testAPI(instanceService)
	api.auth = authService

	tests := []struct {
		name    string
		path    string
		body    string
		message string
	}{
		{
			name:    "malformed login",
			path:    "/api/v1/auth/login",
			body:    `{"username":"target"`,
			message: "invalid JSON body: unexpected EOF",
		},
		{
			name:    "oversize setup",
			path:    "/api/v1/setup",
			body:    strings.Repeat(" ", int(defaultJSONMaximumBytes)+1),
			message: "invalid JSON body: http: request body too large",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer must-not-reach-service")
			request.RemoteAddr = "198.51.100.60:4000"
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest || response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("response = status %d, content-type %q; want 400 JSON: %s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
			}
			var body errorEnvelope
			decodeResponse(t, response, &body)
			if body.Error.Code != "invalid_request" || body.Error.Message != test.message {
				t.Fatalf("error = %#v, want invalid_request %q", body.Error, test.message)
			}
		})
	}
	if authService.loginCalls != 0 || instanceService.setupToken != "" {
		t.Fatalf("invalid bodies reached credential services: login calls %d, setup token %q", authService.loginCalls, instanceService.setupToken)
	}
}

func TestPublicAdmissionExpiresRateLimitAndCleansSourcesInBoundedBatches(t *testing.T) {
	current := time.Unix(1_700_000_000, 0)
	admission := newRequestAdmission(100, 1, 1, 200, time.Minute)
	admission.now = func() time.Time { return current }
	for index := range publicAdmissionCleanupLimit + 5 {
		release, _, admitted := admission.acquire("198.51.100." + strconv.Itoa(index+1))
		if !admitted {
			t.Fatalf("seed source %d was not admitted", index)
		}
		release()
	}

	current = current.Add(time.Minute)
	release, _, admitted := admission.acquire("203.0.113.1")
	if !admitted {
		t.Fatal("source remained locked after its finite window")
	}
	if got := len(admission.sources); got != 6 {
		t.Fatalf("one cleanup removed an unbounded or incomplete batch: tracked sources = %d, want 6", got)
	}
	release()

	release, _, admitted = admission.acquire("203.0.113.2")
	if !admitted {
		t.Fatal("admission did not continue while cleaning expired entries")
	}
	release()
	if got := len(admission.sources); got != 2 {
		t.Fatalf("expired sources were not cleaned on subsequent admission: %d", got)
	}
}

func TestUsernameAdmissionTracksOnlyFailuresForgetsSuccessAndEvictsAtCapacity(t *testing.T) {
	current := time.Unix(1_700_000_000, 0)
	admission := newUsernameAdmission(2, 2, time.Minute)
	admission.now = func() time.Time { return current }

	admission.recordFailure("  ÄDMIN  ")
	admission.recordFailure("ädmin")
	admission.recordFailure("ÄdMiN")
	if got := len(admission.subjects); got != 1 {
		t.Fatalf("tracked normalized subjects = %d, want 1", got)
	}
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace("  ÄDMIN  "))))
	state, exists := admission.subjects[digest]
	if !exists || state.attempts != 2 {
		t.Fatalf("hashed username state = %+v, exists %v; want capped two failures", state, exists)
	}

	admission.forget("ädmin")
	if _, exists := admission.subjects[digest]; exists {
		t.Fatal("successful normalized username was not forgotten")
	}

	admission.recordFailure("first")
	admission.recordFailure("second")
	admission.recordFailure("third")
	thirdDigest := sha256.Sum256([]byte("third"))
	if _, exists := admission.subjects[thirdDigest]; !exists {
		t.Fatal("full failure table rejected the new subject instead of evicting")
	}
	if got := len(admission.subjects); got != 2 {
		t.Fatalf("tracked subjects = %d, want bounded cardinality 2", got)
	}

	current = current.Add(time.Minute)
	admission.recordFailure("fourth")
	if got := len(admission.subjects); got != 1 {
		t.Fatalf("expired username subjects were not removed: tracked %d, want 1", got)
	}
}

type blockingLoginService struct {
	*fakeAuthService
	entered chan struct{}
	release <-chan struct{}
}

func (service *blockingLoginService) Login(context.Context, auth.LoginInput) (auth.TokenPair, error) {
	service.entered <- struct{}{}
	<-service.release
	return auth.TokenPair{}, auth.ErrInvalidCredentials
}

type gatedRequestBody struct {
	started chan struct{}
	release <-chan struct{}
	reader  *strings.Reader
	once    sync.Once
}

func (body *gatedRequestBody) Read(destination []byte) (int, error) {
	body.once.Do(func() {
		close(body.started)
		<-body.release
	})
	return body.reader.Read(destination)
}

func (*gatedRequestBody) Close() error {
	return nil
}
