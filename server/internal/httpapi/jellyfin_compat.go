package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/addon"
	artworkcache "github.com/moodiness/rivune/server/internal/artwork"
	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/jellyfin"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

const (
	jellyfinCompatibilityStartupTimeout  = 5 * time.Second
	jellyfinCompatibilityRetryInitial    = 200 * time.Millisecond
	jellyfinCompatibilityRetryMaximum    = 30 * time.Second
	jellyfinCompatibilityPollInterval    = 5 * time.Second
	jellyfinCompatibilityIdleWaitMaximum = time.Duration(1 << 62)
)

type credentialLoginAdmissionError struct {
	retryAfter time.Duration
}

func (err *credentialLoginAdmissionError) Error() string {
	return "credential login admission denied"
}

func (a *API) loginCredentials(ctx context.Context, input auth.LoginInput) (auth.TokenPair, error) {
	release, retryAfter, admitted := a.credentialAdmission.acquire(auth.ClientIP(ctx))
	if !admitted {
		return auth.TokenPair{}, &credentialLoginAdmissionError{retryAfter: retryAfter}
	}
	defer release()

	retryAfter, admitted = a.usernameAdmission.acquire(input.Username)
	if !admitted {
		return auth.TokenPair{}, &credentialLoginAdmissionError{retryAfter: retryAfter}
	}
	return a.auth.Login(ctx, input)
}

func (a *API) loginJellyfinProfile(ctx context.Context, input auth.JellyfinProfileLoginInput) (auth.JellyfinProfileLoginResult, error) {
	release, retryAfter, admitted := a.credentialAdmission.acquire(auth.ClientIP(ctx))
	if !admitted {
		return auth.JellyfinProfileLoginResult{}, &credentialLoginAdmissionError{retryAfter: retryAfter}
	}
	defer release()

	retryAfter, admitted = a.usernameAdmission.acquire(input.Username)
	if !admitted {
		return auth.JellyfinProfileLoginResult{}, &credentialLoginAdmissionError{retryAfter: retryAfter}
	}
	return a.auth.LoginJellyfinProfile(ctx, input)
}

func (a *API) initializeJellyfinCompatibility(
	pool *pgxpool.Pool,
	nativeAuthentication *auth.Service,
	catalog *watchstate.Service,
	artwork *artworkcache.Service,
	playbackDelivery *playback.Service,
	instances instanceService,
	searchDependencies ...any,
) {
	if a == nil {
		return
	}
	a.jellyfinCompatibilityMu.Lock()
	defer a.jellyfinCompatibilityMu.Unlock()
	if a.jellyfinCompatibilityBuilder == nil {
		a.jellyfinCompatibilityBuilder = func(ctx context.Context) (*jellyfin.Handler, bool, error) {
			return a.buildJellyfinCompatibility(ctx, pool, nativeAuthentication, catalog, artwork, playbackDelivery, instances, searchDependencies...)
		}
	}
	if a.jellyfinCompatibilitySignal == nil {
		a.jellyfinCompatibilitySignal = make(chan struct{}, 1)
	}
	if a.jellyfinCompatibilityRetrySeed == 0 {
		a.jellyfinCompatibilityRetrySeed = uint64(time.Now().UnixNano())
	}
}

func (a *API) buildJellyfinCompatibility(
	ctx context.Context,
	pool *pgxpool.Pool,
	nativeAuthentication *auth.Service,
	catalog *watchstate.Service,
	artwork *artworkcache.Service,
	playbackDelivery *playback.Service,
	instances instanceService,
	searchDependencies ...any,
) (*jellyfin.Handler, bool, error) {
	serverInfo, configured, err := jellyfinCompatibilityServerInfo(ctx, instances, a.version)
	if err != nil || !configured {
		return nil, configured, err
	}

	sessions, err := jellyfin.NewSessionStore(pool, nativeAuthentication)
	if err != nil {
		return nil, false, fmt.Errorf("initialize Jellyfin compatibility sessions: %w", err)
	}
	compatAuthentication, err := jellyfin.NewAuthenticationService(
		func(ctx context.Context, input auth.JellyfinProfileLoginInput) (auth.JellyfinProfileLoginResult, error) {
			result, loginErr := a.loginJellyfinProfile(ctx, input)
			var admissionErr *credentialLoginAdmissionError
			if errors.As(loginErr, &admissionErr) {
				return auth.JellyfinProfileLoginResult{}, auth.ErrInvalidCredentials
			}
			return result, loginErr
		},
		nativeAuthentication,
		sessions,
	)
	if err != nil {
		return nil, false, fmt.Errorf("initialize Jellyfin compatibility authentication: %w", err)
	}
	var metadataSearch *metadata.Service
	var addonSearch *addon.Service
	for _, dependency := range searchDependencies {
		switch typed := dependency.(type) {
		case *metadata.Service:
			metadataSearch = typed
		case *addon.Service:
			addonSearch = typed
		}
	}
	var catalogArtwork jellyfin.CatalogArtworkPresenter
	if artwork != nil {
		catalogArtwork = artwork
	}
	var compatCatalog jellyfin.CatalogReader
	switch {
	case metadataSearch != nil && addonSearch != nil:
		compatCatalog, err = jellyfin.NewCatalogReader(catalog, metadataSearch, addonSearch, catalogArtwork)
	case metadataSearch != nil:
		compatCatalog, err = jellyfin.NewCatalogReader(catalog, metadataSearch, nil, catalogArtwork)
	case addonSearch != nil:
		compatCatalog, err = jellyfin.NewCatalogReader(catalog, nil, addonSearch, catalogArtwork)
	default:
		compatCatalog, err = jellyfin.NewCatalogReader(catalog, nil, nil, catalogArtwork)
	}
	if err != nil {
		return nil, false, fmt.Errorf("initialize Jellyfin compatibility catalog: %w", err)
	}

	compatHandler, err := jellyfin.New(jellyfin.Dependencies{
		ServerInfo: serverInfo, Authentication: compatAuthentication, Catalog: compatCatalog,
		Artwork: artwork, Playback: playbackDelivery, Watchstate: catalog, Logger: a.logger,
	})
	if err != nil {
		return nil, false, fmt.Errorf("initialize Jellyfin compatibility HTTP adapter: %w", err)
	}
	return compatHandler, true, nil
}
func jellyfinCompatibilityServerInfo(ctx context.Context, instances instanceService, runtimeVersion string) (jellyfin.ServerInfo, bool, error) {
	info, err := instances.Info(ctx)
	if err != nil {
		return jellyfin.ServerInfo{}, false, fmt.Errorf("read Jellyfin compatibility server identity: %w", err)
	}
	if info.SetupRequired {
		return jellyfin.ServerInfo{}, false, nil
	}
	serverID, err := jellyfin.ParseServerID(info.PublicID)
	if err != nil {
		return jellyfin.ServerInfo{}, false, fmt.Errorf("validate Jellyfin compatibility server identity: %w", err)
	}
	return jellyfin.ServerInfo{ID: serverID, Name: info.Name, RuntimeVersion: runtimeVersion}, true, nil
}

func (a *API) routeJellyfinCompatibility(native http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !jellyfin.IsReservedPath(request.URL.Path) {
			native.ServeHTTP(response, request)
			return
		}
		compatHTTP := a.currentJellyfinCompatibility()
		if compatHTTP == nil {
			http.NotFound(response, request)
			return
		}
		ctx := auth.WithClientIP(request.Context(), requestClientIP(request, a.config.TrustedProxies))
		compatHTTP.ServeHTTP(response, request.WithContext(ctx))
	})
}

func (a *API) currentJellyfinCompatibility() *jellyfin.Handler {
	if a == nil {
		return nil
	}
	a.jellyfinCompatibilityMu.Lock()
	defer a.jellyfinCompatibilityMu.Unlock()
	return a.jellyfinCompatibility
}

func (a *API) setJellyfinCompatibilityDesired(enabled bool) {
	if a == nil {
		return
	}
	a.jellyfinCompatibilityMu.Lock()
	if a.jellyfinCompatibilityDesired != enabled {
		a.jellyfinCompatibilityDesired = enabled
		a.jellyfinCompatibilityRevision++
	}
	if a.jellyfinCompatibilitySignal == nil {
		a.jellyfinCompatibilitySignal = make(chan struct{}, 1)
	}
	signal := a.jellyfinCompatibilitySignal
	var cancel context.CancelFunc
	if !enabled {
		a.jellyfinCompatibility = nil
		cancel = a.jellyfinCompatibilityCancel
	}
	a.jellyfinCompatibilityMu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case signal <- struct{}{}:
	default:
	}
}

func (a *API) requestJellyfinCompatibilityReconciliation() {
	if a == nil {
		return
	}
	a.jellyfinCompatibilityMu.Lock()
	a.jellyfinCompatibilityReconcileRequested = true
	if a.jellyfinCompatibilitySignal == nil {
		a.jellyfinCompatibilitySignal = make(chan struct{}, 1)
	}
	signal := a.jellyfinCompatibilitySignal
	a.jellyfinCompatibilityMu.Unlock()
	select {
	case signal <- struct{}{}:
	default:
	}
}

func (a *API) applyCanonicalJellyfinCompatibilityDesired(enabled bool) {
	a.jellyfinCompatibilityMu.Lock()
	if a.jellyfinCompatibilityDesired == enabled {
		a.jellyfinCompatibilityMu.Unlock()
		return
	}
	a.jellyfinCompatibilityDesired = enabled
	a.jellyfinCompatibilityRevision++
	if a.jellyfinCompatibilitySignal == nil {
		a.jellyfinCompatibilitySignal = make(chan struct{}, 1)
	}
	signal := a.jellyfinCompatibilitySignal
	var cancel context.CancelFunc
	if !enabled {
		a.jellyfinCompatibility = nil
		cancel = a.jellyfinCompatibilityCancel
	}
	a.jellyfinCompatibilityMu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case signal <- struct{}{}:
	default:
	}
}

func (a *API) requestJellyfinCompatibilityActivation() {
	if a == nil {
		return
	}
	a.jellyfinCompatibilityMu.Lock()
	if a.jellyfinCompatibilitySignal == nil {
		a.jellyfinCompatibilitySignal = make(chan struct{}, 1)
	}
	signal := a.jellyfinCompatibilitySignal
	a.jellyfinCompatibilityMu.Unlock()
	select {
	case signal <- struct{}{}:
	default:
	}
}

func (a *API) jellyfinCompatibilityActivationSignal() <-chan struct{} {
	a.jellyfinCompatibilityMu.Lock()
	defer a.jellyfinCompatibilityMu.Unlock()
	if a.jellyfinCompatibilitySignal == nil {
		a.jellyfinCompatibilitySignal = make(chan struct{}, 1)
	}
	return a.jellyfinCompatibilitySignal
}

func (a *API) jellyfinCompatibilityRetryDelay(attempt uint32) time.Duration {
	if a.jellyfinCompatibilityBackoff != nil {
		return a.jellyfinCompatibilityBackoff(attempt)
	}
	return jellyfinCompatibilityRetryDelay(attempt, a.jellyfinCompatibilityRetrySeed)
}

func jellyfinCompatibilityRetryDelay(attempt uint32, seed uint64) time.Duration {
	backoff := jellyfinCompatibilityRetryInitial
	remaining := attempt
	for remaining > 0 && backoff < jellyfinCompatibilityRetryMaximum/2 {
		backoff *= 2
		remaining--
	}
	if remaining > 0 {
		backoff = jellyfinCompatibilityRetryMaximum
	}

	value := seed + uint64(attempt)*0x9e3779b97f4a7c15
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	value ^= value >> 31
	window := backoff / 5
	delay := backoff - window + time.Duration(value%uint64(2*window+1))
	if delay > jellyfinCompatibilityRetryMaximum {
		return jellyfinCompatibilityRetryMaximum
	}
	return delay
}

func waitForJellyfinCompatibilityRetry(ctx context.Context, signal <-chan struct{}, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-signal:
	case <-timer.C:
	}
}

func (a *API) HasJellyfinCompatibility() bool {
	if a == nil {
		return false
	}
	a.jellyfinCompatibilityMu.Lock()
	defer a.jellyfinCompatibilityMu.Unlock()
	return a.jellyfinCompatibilityDesired
}

func (a *API) RunJellyfinCompatibility(ctx context.Context) {
	if a == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.jellyfinCompatibilityRunOnce.Do(func() {
		a.runJellyfinCompatibility(ctx)
	})
}

func (a *API) runJellyfinCompatibility(ctx context.Context) {
	var failedAttempts uint32
	var reconcileAttempts uint32
	var nextReconcile time.Time
	for {
		now := time.Now()
		a.jellyfinCompatibilityMu.Lock()
		reconciler := a.jellyfinCompatibilityReconciler
		reconcileRequested := a.jellyfinCompatibilityReconcileRequested
		reconcileDue := reconciler != nil && (nextReconcile.IsZero() || !nextReconcile.After(now) || reconcileRequested && reconcileAttempts == 0)
		if reconcileDue {
			a.jellyfinCompatibilityReconcileRequested = false
		}
		a.jellyfinCompatibilityMu.Unlock()
		if reconcileDue {
			reconcileContext, cancelReconcile := context.WithTimeout(ctx, jellyfinCompatibilityStartupTimeout)
			a.jellyfinCompatibilitySettingsMu.Lock()
			enabled, reconcileErr := reconciler(reconcileContext)
			if reconcileErr == nil {
				a.applyCanonicalJellyfinCompatibilityDesired(enabled)
			}
			a.jellyfinCompatibilitySettingsMu.Unlock()
			cancelReconcile()
			if ctx.Err() != nil {
				continue
			}
			if reconcileErr != nil {
				if a.logger != nil {
					a.logger.Error("reconcile Jellyfin compatibility setting", "error", "instance settings read failed")
				}
				nextReconcile = time.Now().Add(a.jellyfinCompatibilityRetryDelay(reconcileAttempts))
				if reconcileAttempts < ^uint32(0) {
					reconcileAttempts++
				}
			} else {
				reconcileAttempts = 0
				interval := a.jellyfinCompatibilityPollInterval
				if interval <= 0 {
					interval = jellyfinCompatibilityPollInterval
				}
				nextReconcile = time.Now().Add(interval)
			}
		}

		a.jellyfinCompatibilityMu.Lock()
		desired := a.jellyfinCompatibilityDesired
		revision := a.jellyfinCompatibilityRevision
		active := a.jellyfinCompatibility
		cancel := a.jellyfinCompatibilityCancel
		done := a.jellyfinCompatibilityDone
		builder := a.jellyfinCompatibilityBuilder
		reconcileAvailable := a.jellyfinCompatibilityReconciler != nil
		a.jellyfinCompatibilityMu.Unlock()
		pollDelay := jellyfinCompatibilityIdleWaitMaximum
		if reconcileAvailable {
			pollDelay = time.Until(nextReconcile)
			if pollDelay < 0 {
				pollDelay = 0
			}
		}

		if ctx.Err() != nil {
			a.stopJellyfinCompatibilityGeneration(cancel, done)
			return
		}
		if active == nil && done != nil {
			a.stopJellyfinCompatibilityGeneration(cancel, done)
			continue
		}
		if !desired {
			a.stopJellyfinCompatibilityGeneration(cancel, done)
			failedAttempts = 0
			waitForJellyfinCompatibilityRetry(ctx, a.jellyfinCompatibilityActivationSignal(), pollDelay)
			continue
		}
		if active != nil && done != nil {
			timer := time.NewTimer(pollDelay)
			select {
			case <-ctx.Done():
			case <-a.jellyfinCompatibilityActivationSignal():
			case <-timer.C:
			case <-done:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				a.clearJellyfinCompatibilityGeneration(done)
				waitForJellyfinCompatibilityRetry(ctx, a.jellyfinCompatibilityActivationSignal(), min(a.jellyfinCompatibilityRetryDelay(failedAttempts), pollDelay))
				if failedAttempts < ^uint32(0) {
					failedAttempts++
				}
				continue
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			continue
		}
		if builder == nil {
			waitForJellyfinCompatibilityRetry(ctx, a.jellyfinCompatibilityActivationSignal(), pollDelay)
			continue
		}
		select {
		case <-a.jellyfinCompatibilityActivationSignal():
		default:
		}

		buildContext, cancelBuild := context.WithTimeout(ctx, jellyfinCompatibilityStartupTimeout)
		handler, configured, err := builder(buildContext)
		cancelBuild()
		if ctx.Err() != nil {
			continue
		}
		if err == nil && configured && handler == nil {
			err = errors.New("compatibility builder returned no handler")
		}
		if err != nil {
			if a.logger != nil {
				a.logger.Error("initialize Jellyfin compatibility", "error", "compatibility initialization failed")
			}
			delay := min(a.jellyfinCompatibilityRetryDelay(failedAttempts), pollDelay)
			if failedAttempts < ^uint32(0) {
				failedAttempts++
			}
			waitForJellyfinCompatibilityRetry(ctx, a.jellyfinCompatibilityActivationSignal(), delay)
			continue
		}
		if !configured {
			failedAttempts = 0
			waitForJellyfinCompatibilityRetry(ctx, a.jellyfinCompatibilityActivationSignal(), pollDelay)
			continue
		}
		if a.startJellyfinCompatibilityGeneration(ctx, revision, handler) {
			failedAttempts = 0
		}
	}
}

func (a *API) startJellyfinCompatibilityGeneration(parent context.Context, revision uint64, handler *jellyfin.Handler) bool {
	generationContext, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	a.jellyfinCompatibilityMu.Lock()
	if !a.jellyfinCompatibilityDesired || a.jellyfinCompatibilityRevision != revision || a.jellyfinCompatibility != nil {
		a.jellyfinCompatibilityMu.Unlock()
		cancel()
		return false
	}
	a.jellyfinCompatibility = handler
	a.jellyfinCompatibilityCancel = cancel
	a.jellyfinCompatibilityDone = done
	runner := a.jellyfinCompatibilityRunner
	a.jellyfinCompatibilityMu.Unlock()
	go func() {
		defer close(done)
		if runner != nil {
			runner(generationContext, handler)
			return
		}
		handler.Run(generationContext)
	}()
	return true
}

func (a *API) stopJellyfinCompatibilityGeneration(cancel context.CancelFunc, done <-chan struct{}) {
	if cancel == nil || done == nil {
		return
	}
	cancel()
	<-done
	a.clearJellyfinCompatibilityGeneration(done)
}

func (a *API) clearJellyfinCompatibilityGeneration(done <-chan struct{}) {
	a.jellyfinCompatibilityMu.Lock()
	defer a.jellyfinCompatibilityMu.Unlock()
	if a.jellyfinCompatibilityDone != done {
		return
	}
	a.jellyfinCompatibility = nil
	a.jellyfinCompatibilityCancel = nil
	a.jellyfinCompatibilityDone = nil
}
