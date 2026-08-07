package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/config"
	"github.com/moodiness/rivune/server/internal/instance"
	"github.com/moodiness/rivune/server/internal/jellyfin"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

func TestJellyfinCompatibilityDisabledSkipsConstructionAndReservesRoutes(t *testing.T) {
	instances := &fakeInstanceService{info: instance.Info{
		PublicID: "11111111-1111-4111-8111-111111111111",
		Name:     "Rivune",
	}}
	api := &API{config: config.Config{JellyfinEnabled: false}}
	api.initializeJellyfinCompatibility(nil, nil, nil, nil, nil, instances)
	if instances.infoCalls != 0 || api.jellyfinCompatibility != nil {
		t.Fatalf("disabled compatibility allocated dependencies: info calls=%d handler=%v", instances.infoCalls, api.jellyfinCompatibility)
	}
	if api.jellyfinCompatibilityBuilder == nil {
		t.Fatal("disabled compatibility did not prepare its activation builder")
	}

	nativeCalls := 0
	native := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		nativeCalls++
		response.WriteHeader(http.StatusOK)
	})
	handler := api.routeJellyfinCompatibility(native)
	for _, path := range []string{"/System/Ping", "/emby/System/Ping", "/Items/item-id"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("disabled reserved path %q status = %d, want 404", path, response.Code)
		}
	}
	if nativeCalls != 0 {
		t.Fatalf("reserved paths reached native/SPA handler %d times", nativeCalls)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil))
	if response.Code != http.StatusOK || nativeCalls != 1 {
		t.Fatalf("native path status=%d calls=%d, want 200 and one call", response.Code, nativeCalls)
	}
}

func TestJellyfinCompatibilityEnabledRoutesExactRootAndEmbyAliasesBeforeSPA(t *testing.T) {
	compatCalls := 0
	compat, err := jellyfin.New(jellyfin.Dependencies{Handlers: map[jellyfin.Route]http.Handler{
		jellyfin.RouteSystemPing: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			compatCalls++
			if request.URL.Path != "/System/Ping" {
				t.Errorf("compat normalized path = %q, want /System/Ping", request.URL.Path)
			}
			response.WriteHeader(http.StatusNoContent)
		}),
	}})
	if err != nil {
		t.Fatalf("construct injected compatibility handler: %v", err)
	}
	api := &API{config: config.Config{JellyfinEnabled: true}, jellyfinCompatibility: compat}
	nativeCalls := 0
	handler := api.routeJellyfinCompatibility(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		nativeCalls++
		response.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{"/System/Ping", "/emby/System/Ping"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("enabled compat path %q status = %d, want 204", path, response.Code)
		}
	}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/System/Info", nil),
		httptest.NewRequest(http.MethodPost, "/System/Ping", nil),
		httptest.NewRequest(http.MethodGet, "/Items/not-a-uuid", nil),
		httptest.NewRequest(http.MethodGet, "/System/Ping/", nil),
		httptest.NewRequest(http.MethodGet, "/system/Ping", nil),
		httptest.NewRequest(http.MethodGet, "/Emby/System/Ping", nil),
		httptest.NewRequest(http.MethodGet, "/emby/emby/System/Ping", nil),
		httptest.NewRequest(http.MethodGet, "/System//Ping", nil),
		httptest.NewRequest(http.MethodGet, "/System/./Ping", nil),
		httptest.NewRequest(http.MethodGet, "/System/../System/Ping", nil),
		httptest.NewRequest(http.MethodGet, "/native/../System/Ping", nil),
		httptest.NewRequest(http.MethodGet, "/native/../emby/System/Ping", nil),
		httptest.NewRequest(http.MethodGet, "/System%2FPing", nil),
		httptest.NewRequest(http.MethodGet, "/System%5CPing", nil),
		httptest.NewRequest(http.MethodGet, `/System\Ping`, nil),
		httptest.NewRequest(http.MethodGet, "//System/Ping", nil),
		httptest.NewRequest(http.MethodGet, "/emby//System/Ping", nil),
		httptest.NewRequest(http.MethodGet, "/emby%2FSystem/Ping", nil),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("invalid reserved request %s %s status = %d, want 404", request.Method, request.URL.Path, response.Code)
		}
	}
	if compatCalls != 2 || nativeCalls != 0 {
		t.Fatalf("compat calls=%d native/SPA calls=%d, want 2 and 0", compatCalls, nativeCalls)
	}
}

func TestJellyfinCompatibilityServerInfoIsStableAcrossCompositionRestarts(t *testing.T) {
	instances := &fakeInstanceService{info: instance.Info{
		PublicID: "11111111-1111-4111-8111-111111111111",
		Name:     "Rivune Home",
	}}
	first, configured, err := jellyfinCompatibilityServerInfo(context.Background(), instances, "1.2.3")
	if err != nil || !configured {
		t.Fatalf("first server info: configured=%t error=%v", configured, err)
	}
	second, configured, err := jellyfinCompatibilityServerInfo(context.Background(), instances, "1.2.3")
	if err != nil || !configured {
		t.Fatalf("second server info: configured=%t error=%v", configured, err)
	}
	if first.ID.String() != second.ID.String() || first.ID.String() != instances.info.PublicID {
		t.Fatalf("restart server IDs = %q and %q, want persisted %q", first.ID.String(), second.ID.String(), instances.info.PublicID)
	}
	if first.Name != "Rivune Home" || first.RuntimeVersion != "1.2.3" {
		t.Fatalf("server info lost persisted/runtime fields: %+v", first)
	}
}

func TestJellyfinProfileLoginUsesOpaqueUsernameAndSharedAdmissionBudgets(t *testing.T) {
	service := &fakeAuthService{jellyfinLoginResult: auth.JellyfinProfileLoginResult{
		ProfileID: "22222222-2222-4222-8222-222222222222",
	}}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	input := auth.JellyfinProfileLoginInput{
		Username: "11111111-1111-4111-8111-111111111111", Password: "application-password",
		LinkedDeviceKey: "infuse-device", DeviceName: "Living Room", Platform: "Infuse",
	}
	for attempt := range credentialUsernameAttempts {
		ctx := auth.WithClientIP(context.Background(), "198.51.100."+strconv.Itoa(attempt+1))
		if _, err := api.loginJellyfinProfile(ctx, input); err != nil {
			t.Fatalf("admitted Jellyfin login %d failed: %v", attempt, err)
		}
	}
	ctx := auth.WithClientIP(context.Background(), "203.0.113.1")
	if _, err := api.loginJellyfinProfile(ctx, input); err == nil {
		t.Fatal("opaque credential username admission budget was bypassed by source rotation")
	} else {
		var admissionErr *credentialLoginAdmissionError
		if !errors.As(err, &admissionErr) {
			t.Fatalf("blocked Jellyfin login error = %T %v", err, err)
		}
	}
	if service.jellyfinLoginCalls != credentialUsernameAttempts || service.jellyfinLoginInput != input || service.loginCalls != 0 {
		t.Fatalf("Jellyfin login calls=%d native password login calls=%d input=%+v", service.jellyfinLoginCalls, service.loginCalls, service.jellyfinLoginInput)
	}
	input.Username = "33333333-3333-4333-8333-333333333333"
	if _, err := api.loginJellyfinProfile(auth.WithClientIP(context.Background(), "203.0.113.2"), input); err != nil {
		t.Fatalf("independent opaque credential was rejected: %v", err)
	}
}

type jellyfinLifecycleAuthentication struct{}

func (jellyfinLifecycleAuthentication) Login(context.Context, jellyfin.CompatLoginInput) (jellyfin.LoginResult, error) {
	return jellyfin.LoginResult{}, errors.New("not used")
}

func (jellyfinLifecycleAuthentication) Authenticate(context.Context, string) (jellyfin.AuthenticatedSession, error) {
	return jellyfin.AuthenticatedSession{}, jellyfin.ErrInvalidCompatCredential
}

func (jellyfinLifecycleAuthentication) Logout(context.Context, jellyfin.AuthenticatedSession) error {
	return nil
}

type jellyfinLifecycleCatalog struct{}

func (jellyfinLifecycleCatalog) GetCatalogTitle(context.Context, auth.Principal, string) (watchstate.CatalogTitle, error) {
	return watchstate.CatalogTitle{}, watchstate.ErrNotFound
}

func (jellyfinLifecycleCatalog) ListCatalogItems(context.Context, auth.Principal, watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
	return watchstate.CatalogPage{}, nil
}

type jellyfinLifecyclePlayback struct{}

func (jellyfinLifecyclePlayback) Sources(context.Context, auth.Principal, playback.SourcesInput) (playback.SourceList, error) {
	return playback.SourceList{}, errors.New("not used")
}

func (jellyfinLifecyclePlayback) Open(context.Context, auth.Principal, playback.ResolveInput) (playback.Delivery, error) {
	return playback.Delivery{}, errors.New("not used")
}

func (jellyfinLifecyclePlayback) Serve(http.ResponseWriter, *http.Request, playback.DeliveryHandle) error {
	return errors.New("not used")
}

func (jellyfinLifecyclePlayback) Close(context.Context, auth.Principal, playback.DeliveryHandle) error {
	return nil
}

func newJellyfinLifecycleHandler(t *testing.T) *jellyfin.Handler {
	t.Helper()
	serverID, err := jellyfin.ParseServerID("11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := jellyfin.New(jellyfin.Dependencies{
		ServerInfo:     jellyfin.ServerInfo{ID: serverID, Name: "Rivune Home", RuntimeVersion: "test"},
		Authentication: jellyfinLifecycleAuthentication{}, Catalog: jellyfinLifecycleCatalog{}, Playback: jellyfinLifecyclePlayback{},
		Handlers: map[jellyfin.Route]http.Handler{
			jellyfin.RouteSystemPing: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(http.StatusNoContent)
			}),
		},
	})
	if err != nil {
		t.Fatalf("construct lifecycle compatibility handler: %v", err)
	}
	return handler
}

func TestJellyfinCompatibilitySupervisorTogglesGenerationsAndShutsDown(t *testing.T) {
	api := testAPI(&fakeInstanceService{})
	first := newJellyfinLifecycleHandler(t)
	second := newJellyfinLifecycleHandler(t)
	handlers := []*jellyfin.Handler{first, second}
	var builds atomic.Int32
	api.jellyfinCompatibilityBuilder = func(context.Context) (*jellyfin.Handler, bool, error) {
		index := int(builds.Add(1)) - 1
		if index >= len(handlers) {
			return nil, true, errors.New("unexpected extra generation")
		}
		return handlers[index], true, nil
	}
	started := make(chan *jellyfin.Handler, 2)
	cleaned := make(chan *jellyfin.Handler, 2)
	api.jellyfinCompatibilityRunner = func(ctx context.Context, handler *jellyfin.Handler) {
		started <- handler
		<-ctx.Done()
		cleaned <- handler
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		api.RunJellyfinCompatibility(ctx)
		close(done)
	}()
	router := api.routeJellyfinCompatibility(http.NotFoundHandler())
	assertJellyfinLifecycleStatus(t, router, http.StatusNotFound)

	api.setJellyfinCompatibilityDesired(true)
	if handler := receiveJellyfinLifecycleHandler(t, started, "first generation start"); handler != first {
		t.Fatalf("first generation handler=%p want=%p", handler, first)
	}
	assertJellyfinLifecycleStatus(t, router, http.StatusNoContent)

	api.setJellyfinCompatibilityDesired(false)
	assertJellyfinLifecycleStatus(t, router, http.StatusNotFound)
	if handler := receiveJellyfinLifecycleHandler(t, cleaned, "first generation cleanup"); handler != first {
		t.Fatalf("cleaned first generation handler=%p want=%p", handler, first)
	}

	api.setJellyfinCompatibilityDesired(true)
	if handler := receiveJellyfinLifecycleHandler(t, started, "second generation start"); handler != second {
		t.Fatalf("second generation handler=%p want=%p", handler, second)
	}
	assertJellyfinLifecycleStatus(t, router, http.StatusNoContent)

	cancel()
	if handler := receiveJellyfinLifecycleHandler(t, cleaned, "shutdown cleanup"); handler != second {
		t.Fatalf("shutdown generation handler=%p want=%p", handler, second)
	}
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("compatibility supervisor did not stop after generation cleanup")
	}
	assertJellyfinLifecycleStatus(t, router, http.StatusNotFound)
	if builds.Load() != 2 {
		t.Fatalf("generation builds=%d want=2", builds.Load())
	}
}

func TestJellyfinCompatibilityBuildFailureStaysUnpublishedAndRetries(t *testing.T) {
	api := testAPI(&fakeInstanceService{})
	handler := newJellyfinLifecycleHandler(t)
	var attempts atomic.Int32
	api.jellyfinCompatibilityBuilder = func(context.Context) (*jellyfin.Handler, bool, error) {
		if attempts.Add(1) == 1 {
			return nil, true, errors.New("temporary construction failure")
		}
		return handler, true, nil
	}
	backoff := make(chan struct{}, 1)
	api.jellyfinCompatibilityBackoff = func(uint32) time.Duration {
		backoff <- struct{}{}
		return time.Hour
	}
	started := make(chan struct{}, 1)
	api.jellyfinCompatibilityRunner = func(ctx context.Context, _ *jellyfin.Handler) {
		started <- struct{}{}
		<-ctx.Done()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		api.RunJellyfinCompatibility(ctx)
		close(done)
	}()
	api.setJellyfinCompatibilityDesired(true)
	select {
	case <-backoff:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("failed construction did not schedule retry")
	}
	assertJellyfinLifecycleStatus(t, api.routeJellyfinCompatibility(http.NotFoundHandler()), http.StatusNotFound)
	api.requestJellyfinCompatibilityActivation()
	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("compatibility generation was not retried")
	}
	if attempts.Load() != 2 {
		t.Fatalf("construction attempts=%d want=2", attempts.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("compatibility supervisor did not stop")
	}
}

func TestJellyfinCompatibilityConcurrentUpdatesHonorLastState(t *testing.T) {
	api := testAPI(&fakeInstanceService{})
	handler := newJellyfinLifecycleHandler(t)
	api.jellyfinCompatibilityBuilder = func(context.Context) (*jellyfin.Handler, bool, error) {
		return handler, true, nil
	}
	started := make(chan struct{}, 1)
	cleaned := make(chan struct{}, 1)
	api.jellyfinCompatibilityRunner = func(ctx context.Context, _ *jellyfin.Handler) {
		started <- struct{}{}
		<-ctx.Done()
		cleaned <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	var updates sync.WaitGroup
	for index := range 32 {
		updates.Add(1)
		go func(enabled bool) {
			defer updates.Done()
			api.setJellyfinCompatibilityDesired(enabled)
		}(index%2 == 0)
	}
	updates.Wait()
	api.setJellyfinCompatibilityDesired(true)
	go func() {
		api.RunJellyfinCompatibility(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()
	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("last enabled state did not start compatibility")
	}
	assertJellyfinLifecycleStatus(t, api.routeJellyfinCompatibility(http.NotFoundHandler()), http.StatusNoContent)
	api.setJellyfinCompatibilityDesired(false)
	assertJellyfinLifecycleStatus(t, api.routeJellyfinCompatibility(http.NotFoundHandler()), http.StatusNotFound)
	select {
	case <-cleaned:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("last disabled state did not clean the active generation")
	}
}

func TestJellyfinCompatibilityReactivationWaitsForRetiredGenerationDrain(t *testing.T) {
	api := testAPI(&fakeInstanceService{})
	first := newJellyfinLifecycleHandler(t)
	second := newJellyfinLifecycleHandler(t)
	handlers := []*jellyfin.Handler{first, second}
	var builds atomic.Int32
	api.jellyfinCompatibilityBuilder = func(context.Context) (*jellyfin.Handler, bool, error) {
		index := int(builds.Add(1)) - 1
		if index >= len(handlers) {
			return nil, true, errors.New("unexpected accumulated generation")
		}
		return handlers[index], true, nil
	}
	started := make(chan *jellyfin.Handler, 2)
	cleanupStarted := make(chan struct{}, 1)
	releaseCleanup := make(chan struct{})
	api.jellyfinCompatibilityRunner = func(ctx context.Context, handler *jellyfin.Handler) {
		started <- handler
		<-ctx.Done()
		if handler == first {
			cleanupStarted <- struct{}{}
			<-releaseCleanup
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		api.RunJellyfinCompatibility(ctx)
		close(done)
	}()
	api.setJellyfinCompatibilityDesired(true)
	if handler := receiveJellyfinLifecycleHandler(t, started, "first generation start"); handler != first {
		t.Fatalf("first generation handler=%p want=%p", handler, first)
	}
	api.setJellyfinCompatibilityDesired(false)
	select {
	case <-cleanupStarted:
	case <-time.After(500 * time.Millisecond):
		cancel()
		close(releaseCleanup)
		t.Fatal("retired generation cleanup did not start")
	}
	api.setJellyfinCompatibilityDesired(true)
	if builds.Load() != 1 {
		cancel()
		close(releaseCleanup)
		t.Fatalf("replacement built before retired cleanup completed: builds=%d", builds.Load())
	}
	select {
	case handler := <-started:
		cancel()
		close(releaseCleanup)
		t.Fatalf("replacement generation %p started before retired cleanup completed", handler)
	default:
	}
	close(releaseCleanup)
	if handler := receiveJellyfinLifecycleHandler(t, started, "replacement generation start"); handler != second {
		t.Fatalf("replacement generation handler=%p want=%p", handler, second)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("supervisor did not stop after replacement generation cleanup")
	}
	if builds.Load() != 2 {
		t.Fatalf("generation builds=%d want=2", builds.Load())
	}
}

func assertJellyfinLifecycleStatus(t *testing.T, handler http.Handler, want int) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/System/Ping", nil))
	if response.Code != want {
		t.Fatalf("Jellyfin lifecycle route status=%d want=%d", response.Code, want)
	}
}

func receiveJellyfinLifecycleHandler(t *testing.T, handlers <-chan *jellyfin.Handler, operation string) *jellyfin.Handler {
	t.Helper()
	select {
	case handler := <-handlers:
		return handler
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for %s", operation)
		return nil
	}
}

func TestJellyfinCompatibilityRetryDelayIsDeterministicJitteredAndBounded(t *testing.T) {
	const seed = uint64(0x123456789abcdef0)
	jittered := false
	base := jellyfinCompatibilityRetryInitial
	for attempt := uint32(0); attempt < 64; attempt++ {
		delay := jellyfinCompatibilityRetryDelay(attempt, seed)
		if delay != jellyfinCompatibilityRetryDelay(attempt, seed) {
			t.Fatalf("retry delay for attempt %d is not deterministic", attempt)
		}
		if delay <= 0 || delay > jellyfinCompatibilityRetryMaximum {
			t.Fatalf("retry delay for attempt %d=%s outside bound", attempt, delay)
		}
		lower := base - base/5
		upper := base + base/5
		if upper > jellyfinCompatibilityRetryMaximum {
			upper = jellyfinCompatibilityRetryMaximum
		}
		if delay < lower || delay > upper {
			t.Fatalf("retry delay for attempt %d=%s outside exponential jitter range [%s,%s]", attempt, delay, lower, upper)
		}
		if base < jellyfinCompatibilityRetryMaximum && delay != base {
			jittered = true
		}
		if base < jellyfinCompatibilityRetryMaximum/2 {
			base *= 2
		} else {
			base = jellyfinCompatibilityRetryMaximum
		}
	}
	if !jittered {
		t.Fatal("retry delays did not include jitter")
	}
}
