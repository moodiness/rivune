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
	"github.com/moodiness/rivune/server/internal/runtimesettings"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

const (
	jellyfinCompatibilityStartupTimeout  = 5 * time.Second
	jellyfinCompatibilityRetryInitial    = 200 * time.Millisecond
	jellyfinCompatibilityRetryMaximum    = 30 * time.Second
	jellyfinCompatibilityPollInterval    = 5 * time.Second
	jellyfinCompatibilityIdleWaitMaximum = time.Duration(1 << 62)
)

const jellyfinCompatibilityDisabledRevocationReason = "compatibility_disabled"

type jellyfinMaintenancePolicy struct {
	settings settingsService
}

func (policy jellyfinMaintenancePolicy) Authorize(ctx context.Context, principal auth.Principal) (jellyfin.AuthenticatedRequestPolicyResult, error) {
	if principal.IsGlobalAdministrator() || principal.ActiveProfileCanManage {
		return jellyfin.AuthenticatedRequestPolicyResult{Allowed: true}, nil
	}
	if policy.settings == nil {
		return jellyfin.AuthenticatedRequestPolicyResult{}, errors.New("maintenance settings are unavailable")
	}
	state, err := policy.settings.Maintenance(ctx)
	if err != nil {
		return jellyfin.AuthenticatedRequestPolicyResult{}, err
	}
	return jellyfin.AuthenticatedRequestPolicyResult{Allowed: !state.Enabled, PublicMessage: state.Message}, nil
}

type credentialLoginAdmissionError struct {
	retryAfter time.Duration
}

func (err *credentialLoginAdmissionError) Error() string {
	return "credential login admission denied"
}

func (err *credentialLoginAdmissionError) RetryAfter() time.Duration {
	if err == nil {
		return 0
	}
	return err.retryAfter
}

func (a *API) loginCredentials(ctx context.Context, input auth.LoginInput) (auth.TokenPair, error) {
	release, retryAfter, admitted := a.credentialAdmission.acquire(auth.ClientIP(ctx))
	if !admitted {
		return auth.TokenPair{}, &credentialLoginAdmissionError{retryAfter: retryAfter}
	}
	defer release()

	tokens, err := a.auth.Login(ctx, input)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		a.usernameAdmission.recordFailure(input.Username)
	} else if err == nil {
		a.usernameAdmission.forget(input.Username)
	}
	return tokens, err
}

func (a *API) loginJellyfinProfile(ctx context.Context, input auth.JellyfinProfileLoginInput) (auth.JellyfinProfileLoginResult, error) {
	release, retryAfter, admitted := a.credentialAdmission.acquire(auth.ClientIP(ctx))
	if !admitted {
		return auth.JellyfinProfileLoginResult{}, &credentialLoginAdmissionError{retryAfter: retryAfter}
	}
	defer release()

	result, err := a.auth.LoginJellyfinProfile(ctx, input)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		a.usernameAdmission.recordFailure(input.Username)
	} else if err == nil {
		a.usernameAdmission.forget(input.Username)
	}
	return result, err
}

func (a *API) initializeJellyfinCompatibility(
	pool *pgxpool.Pool,
	nativeAuthentication *auth.Service,
	catalog *watchstate.Service,
	collections jellyfin.CollectionReader,
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
			return a.buildJellyfinCompatibility(ctx, pool, nativeAuthentication, catalog, collections, artwork, playbackDelivery, instances, searchDependencies...)
		}
	}
	if a.jellyfinCompatibilityRevoker == nil {
		a.jellyfinCompatibilityRevoker = func(ctx context.Context, reason string) error {
			sessions, err := jellyfin.NewSessionStore(pool, nativeAuthentication)
			if err != nil {
				return err
			}
			return sessions.RevokeAllActive(ctx, reason)
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
	collections jellyfin.CollectionReader,
	artwork *artworkcache.Service,
	playbackDelivery *playback.Service,
	instances instanceService,
	searchDependencies ...any,
) (*jellyfin.Handler, bool, error) {
	serverInfo, configured, err := jellyfinCompatibilityServerInfo(ctx, instances, a.version)
	if err != nil || !configured {
		return nil, configured, err
	}
	serverInfo.LocalAddress = a.config.PublicURL

	sessions, err := jellyfin.NewSessionStore(pool, nativeAuthentication)
	if err != nil {
		return nil, false, fmt.Errorf("initialize Jellyfin compatibility sessions: %w", err)
	}
	compatAuthentication, err := jellyfin.NewAuthenticationService(
		a.loginJellyfinProfile,
		nativeAuthentication,
		sessions,
	)
	if err != nil {
		return nil, false, fmt.Errorf("initialize Jellyfin compatibility authentication: %w", err)
	}
	displayPreferenceRepository, err := jellyfin.NewDisplayPreferenceRepository(pool)
	if err != nil {
		return nil, false, fmt.Errorf("initialize Jellyfin display preference repository: %w", err)
	}
	displayPreferences, err := jellyfin.NewDisplayPreferenceService(displayPreferenceRepository)
	if err != nil {
		return nil, false, fmt.Errorf("initialize Jellyfin display preference service: %w", err)
	}
	var metadataSearch *metadata.Service
	var addonSearch *addon.Service
	var runtimeSettings *runtimesettings.Source
	for _, dependency := range searchDependencies {
		switch typed := dependency.(type) {
		case *metadata.Service:
			metadataSearch = typed
		case *addon.Service:
			addonSearch = typed
		case *runtimesettings.Source:
			runtimeSettings = typed
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
	var compatMediaSegments jellyfin.MediaSegmentReader
	if playbackDelivery != nil {
		compatMediaSegments = playbackDelivery
	}

	debug := false
	if runtimeSettings != nil {
		debug = runtimeSettings.Load().JellyfinDebug
	}
	compatHandler, err := jellyfin.New(jellyfin.Dependencies{
		ServerInfo: serverInfo, Authentication: compatAuthentication, QuickConnect: compatAuthentication,
		AuthenticatedPolicy: jellyfinMaintenancePolicy{settings: a.settings},
		Catalog:             compatCatalog, Collections: collections, Artwork: artwork, Playback: playbackDelivery, MediaSegments: compatMediaSegments,
		Watchstate: catalog, DisplayPreferences: displayPreferences, Logger: a.logger, Debug: debug,
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
		if a.runtimeSettings != nil {
			ctx = runtimesettings.Pin(ctx, a.runtimeSettings.source)
		}
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
	if enabled && a.jellyfinCompatibilityRevocationPending {
		a.jellyfinCompatibilityMu.Unlock()
		return
	}
	if a.jellyfinCompatibilityDesired != enabled {
		a.jellyfinCompatibilityDesired = enabled
		a.jellyfinCompatibilityRevision++
	}
	if a.jellyfinCompatibilitySignal == nil {
		a.jellyfinCompatibilitySignal = make(chan struct{}, 1)
	}
	signal := a.jellyfinCompatibilitySignal
	var cancel context.CancelFunc
	var generation *jellyfin.Handler
	if !enabled {
		a.jellyfinCompatibility = nil
		cancel = a.jellyfinCompatibilityCancel
		generation = a.jellyfinCompatibilityGeneration
	}
	a.jellyfinCompatibilityMu.Unlock()
	if generation != nil {
		generation.CloseCompatibilitySockets()
	}
	if cancel != nil {
		cancel()
	}
	select {
	case signal <- struct{}{}:
	default:
	}
}

// RequestJellyfinCompatibilityReplacement retires the active handler
// generation and builds a replacement without revoking compatibility or
// native sessions. It is used for live handler-scoped settings such as debug
// route tracing.
func (a *API) RequestJellyfinCompatibilityReplacement() {
	if a == nil {
		return
	}
	a.jellyfinCompatibilityMu.Lock()
	if !a.jellyfinCompatibilityDesired {
		a.jellyfinCompatibilityMu.Unlock()
		return
	}
	a.jellyfinCompatibilityRevision++
	a.jellyfinCompatibility = nil
	generation := a.jellyfinCompatibilityGeneration
	cancel := a.jellyfinCompatibilityCancel
	if a.jellyfinCompatibilitySignal == nil {
		a.jellyfinCompatibilitySignal = make(chan struct{}, 1)
	}
	signal := a.jellyfinCompatibilitySignal
	a.jellyfinCompatibilityMu.Unlock()
	if generation != nil {
		generation.CloseCompatibilitySockets()
	}
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

func (a *API) revokeJellyfinCompatibilitySessions(ctx context.Context) error {
	if a == nil {
		return errors.New("Jellyfin compatibility lifecycle is unavailable")
	}
	a.jellyfinCompatibilityMu.Lock()
	revoker := a.jellyfinCompatibilityRevoker
	a.jellyfinCompatibilityRevocationPending = true
	a.jellyfinCompatibilityMu.Unlock()
	if revoker == nil {
		return errors.New("Jellyfin compatibility session revocation is unavailable")
	}
	if err := revoker(ctx, jellyfinCompatibilityDisabledRevocationReason); err != nil {
		return fmt.Errorf("revoke previous Jellyfin compatibility generation: %w", err)
	}
	a.jellyfinCompatibilityMu.Lock()
	a.jellyfinCompatibilityRevocationPending = false
	a.jellyfinCompatibilityMu.Unlock()
	return nil
}

func (a *API) applyCanonicalJellyfinCompatibilityDesired(ctx context.Context, enabled bool) error {
	a.jellyfinCompatibilityMu.Lock()
	current := a.jellyfinCompatibilityDesired
	pending := a.jellyfinCompatibilityRevocationPending
	a.jellyfinCompatibilityMu.Unlock()
	if current == enabled && !pending {
		return nil
	}
	if enabled {
		if err := a.revokeJellyfinCompatibilitySessions(ctx); err != nil {
			return err
		}
		a.setJellyfinCompatibilityDesired(true)
		return nil
	}

	a.setJellyfinCompatibilityDesired(false)
	if err := a.revokeJellyfinCompatibilitySessions(ctx); err != nil {
		return fmt.Errorf("disable Jellyfin compatibility: %w", err)
	}
	return nil
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
				reconcileErr = a.applyCanonicalJellyfinCompatibilityDesired(reconcileContext, enabled)
			}
			a.jellyfinCompatibilitySettingsMu.Unlock()
			cancelReconcile()
			if ctx.Err() != nil {
				a.jellyfinCompatibilityMu.Lock()
				cancel := a.jellyfinCompatibilityCancel
				done := a.jellyfinCompatibilityDone
				a.jellyfinCompatibilityMu.Unlock()
				a.stopJellyfinCompatibilityGeneration(cancel, done)
				return
			}
			if reconcileErr != nil {
				if a.logger != nil {
					a.logger.Error("reconcile Jellyfin compatibility setting", "error", "canonical compatibility transition failed")
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
		revocationPending := a.jellyfinCompatibilityRevocationPending
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
		if !desired || revocationPending {
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
	if !a.jellyfinCompatibilityDesired || a.jellyfinCompatibilityRevocationPending || a.jellyfinCompatibilityRevision != revision || a.jellyfinCompatibility != nil {
		a.jellyfinCompatibilityMu.Unlock()
		cancel()
		return false
	}
	a.jellyfinCompatibility = handler
	a.jellyfinCompatibilityGeneration = handler
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
	a.jellyfinCompatibilityMu.Lock()
	generation := a.jellyfinCompatibilityGeneration
	a.jellyfinCompatibilityMu.Unlock()
	if generation != nil {
		generation.CloseCompatibilitySockets()
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
	a.jellyfinCompatibilityGeneration = nil
	a.jellyfinCompatibilityCancel = nil
	a.jellyfinCompatibilityDone = nil
}
