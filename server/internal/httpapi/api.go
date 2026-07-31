package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/calendar"
	"github.com/moodiness/rivune/server/internal/collection"
	collectiontrakt "github.com/moodiness/rivune/server/internal/collection/trakt"

	"github.com/moodiness/rivune/server/internal/config"
	"github.com/moodiness/rivune/server/internal/instance"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/metadata/tmdb"
	"github.com/moodiness/rivune/server/internal/metadata/tvdb"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/profile"
	"github.com/moodiness/rivune/server/internal/settings"
	"github.com/moodiness/rivune/server/internal/user"
	"github.com/moodiness/rivune/server/internal/watchstate"
	"github.com/moodiness/rivune/server/internal/webui"
)

const protocolVersion = 15

type instanceService interface {
	Info(context.Context) (instance.Info, error)
	Setup(context.Context, string, instance.SetupInput) (instance.SetupResult, error)
}

type authService interface {
	Login(context.Context, auth.LoginInput) (auth.TokenPair, error)
	Refresh(context.Context, string) (auth.TokenPair, error)
	Authenticate(context.Context, string) (auth.Principal, error)
	Account(context.Context, auth.Principal) (auth.Account, error)
	Logout(context.Context, auth.Principal) error
	Sessions(context.Context, auth.Principal) ([]auth.Session, error)
	RevokeSession(context.Context, auth.Principal, string) error
	ProfileSessions(context.Context, auth.Principal, string) ([]auth.Session, error)
	RevokeProfileSession(context.Context, auth.Principal, string, string) error
	SessionNotifications(context.Context, auth.Principal, int64) ([]auth.SessionNotification, error)
	SendProfileSessionNotification(context.Context, auth.Principal, string, string, string) (auth.SessionNotification, error)
	BeginDeviceAuthorization(context.Context, string, string) (auth.DeviceAuthorization, error)
	ApproveDeviceAuthorization(context.Context, auth.Principal, string) error
	ExchangeDeviceAuthorization(context.Context, string) (auth.TokenPair, error)
}

type profileService interface {
	List(context.Context, auth.Principal) ([]profile.Profile, error)
	Create(context.Context, auth.Principal, profile.CreateInput) (profile.Profile, error)
	Update(context.Context, auth.Principal, string, profile.UpdateInput) (profile.Profile, error)
	Delete(context.Context, auth.Principal, string) error
	Select(context.Context, auth.Principal, string, *string) (profile.Selection, error)
	ClearSelection(context.Context, auth.Principal) error
	SetAvatarPreset(context.Context, auth.Principal, string, string) (profile.Profile, error)
	SetAvatarImage(context.Context, auth.Principal, string, []byte) (profile.Profile, error)
	AvatarImage(context.Context, auth.Principal, string) (profile.AvatarImage, error)
}

type settingsService interface {
	Instance(context.Context) (settings.Layer, error)
	UpdateInstance(context.Context, auth.Principal, settings.Patch) (settings.Layer, error)
	Profile(context.Context, auth.Principal, string) (settings.Layer, error)
	UpdateProfile(context.Context, auth.Principal, string, settings.Patch) (settings.Layer, error)
	Effective(context.Context, auth.Principal, string) (settings.Effective, error)
}

type userService interface {
	List(context.Context, auth.Principal) ([]user.User, error)
	Create(context.Context, auth.Principal, user.CreateInput) (user.User, error)
	Update(context.Context, auth.Principal, string, user.UpdateInput) (user.User, error)
	Delete(context.Context, auth.Principal, string) error
	ProfileAccess(context.Context, auth.Principal, string) ([]user.ProfileAccess, error)
	GrantProfileAccess(context.Context, auth.Principal, string, string, bool) (user.ProfileAccess, error)
	RevokeProfileAccess(context.Context, auth.Principal, string, string) error
}

type addonService interface {
	Install(context.Context, auth.Principal, addon.InstallInput) (addon.InstalledAddon, error)
	List(context.Context, auth.Principal) ([]addon.InstalledAddon, error)
	Remove(context.Context, auth.Principal, string) error
	Reorder(context.Context, auth.Principal, addon.ReorderInput) ([]addon.InstalledAddon, error)
	Refresh(context.Context, auth.Principal, string) (addon.InstalledAddon, error)
	Update(context.Context, auth.Principal, string, addon.UpdateAddonInput) (addon.InstalledAddon, error)
	Catalogs(context.Context, auth.Principal) ([]addon.CatalogDescriptor, error)
	Fetch(context.Context, auth.Principal, string, addon.ResourcePath) (addon.ResourceResult, error)
	FetchAll(context.Context, auth.Principal, addon.ResourcePath) (addon.ResourceBatch, error)
	FetchCatalogs(context.Context, auth.Principal, string, []addon.ExtraValue, bool) (addon.ResourceBatch, error)
}

type collectionService interface {
	List(context.Context, auth.Principal) ([]collection.Collection, error)
	Export(context.Context, auth.Principal) (collection.ExportDocument, error)
	Get(context.Context, auth.Principal, string) (collection.Collection, error)
	Create(context.Context, auth.Principal, collection.SaveInput) (collection.Collection, error)
	Import(context.Context, auth.Principal, collection.ExportDocument) (collection.ImportResult, error)
	Update(context.Context, auth.Principal, string, collection.SaveInput) (collection.Collection, error)
	Delete(context.Context, auth.Principal, string) error
	Reorder(context.Context, auth.Principal, collection.ReorderInput) ([]collection.Collection, error)
	ResolveFolder(context.Context, auth.Principal, string, string, int, int, string, string) (collection.ResolvedFolder, error)
	LookupTMDB(context.Context, auth.Principal, string, string, string, int) ([]collection.LookupResult, error)
	TMDBGenres(context.Context, auth.Principal, string, string) ([]collection.Genre, error)
}

type calendarService interface {
	List(context.Context, auth.Principal, string, string, string) (calendar.Result, error)
}

type metadataService interface {
	DiscoverMovies(context.Context, auth.Principal, metadata.QueryOptions) (metadata.MoviePage, error)
	SearchMovies(context.Context, auth.Principal, metadata.SearchOptions) (metadata.MoviePage, error)
	MovieDetails(context.Context, auth.Principal, string, string) (metadata.Movie, error)
	DiscoverSeries(context.Context, auth.Principal, metadata.QueryOptions) (metadata.SeriesPage, error)
	SearchSeries(context.Context, auth.Principal, metadata.SearchOptions) (metadata.SeriesPage, error)
	SeriesDetails(context.Context, auth.Principal, string, string) (metadata.Series, error)
	SeasonDetails(context.Context, auth.Principal, string, string) (metadata.Season, error)
	Trailer(context.Context, auth.Principal, string, string, *int) (metadata.Trailer, error)
}

type watchstateService interface {
	ResolveTitle(context.Context, auth.Principal, watchstate.ResolveTitleInput) (watchstate.TitleReference, error)
	AddLibrary(context.Context, auth.Principal, string) (watchstate.LibraryItem, error)
	RemoveLibrary(context.Context, auth.Principal, string) error
	Library(context.Context, auth.Principal, string, int, int) (watchstate.LibraryPage, error)
	GetProgress(context.Context, auth.Principal, string) (watchstate.Progress, error)
	UpdateProgress(context.Context, auth.Principal, string, watchstate.UpdateProgressInput) (watchstate.Progress, error)
	SetWatched(context.Context, auth.Principal, string, bool, watchstate.CompletionInput) (watchstate.Progress, error)
	ClearProgress(context.Context, auth.Principal, string, int64) error
	ContinueWatching(context.Context, auth.Principal, int) (watchstate.ContinuePage, error)
}

type playbackService interface {
	Sources(context.Context, auth.Principal, playback.SourcesInput) (playback.SourceList, error)
	Prepare(context.Context, auth.Principal, playback.PrepareInput) (playback.Preparation, error)
	Resolve(context.Context, auth.Principal, playback.ResolveInput) (playback.Session, error)
	Stop(context.Context, auth.Principal, string) error
	Activity(context.Context, auth.Principal) (playback.Activity, error)
	StopActivitySession(context.Context, auth.Principal, string) error
	PurgeActivity(context.Context, auth.Principal) (playback.PurgeResult, error)
	ProxyAsset(http.ResponseWriter, *http.Request, string, string, string, string, string) error
}

type API struct {
	config              config.Config
	addons              addonService
	calendar            calendarService
	pool                *pgxpool.Pool
	instances           instanceService
	collections         collectionService
	auth                authService
	authMaintenance     authMaintenanceService
	profiles            profileService
	playback            playbackService
	playbackMaintenance playbackMaintenanceService
	settings            settingsService
	users               userService
	metadata            metadataService
	logger              *slog.Logger
	version             string
	watchstate          watchstateService
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func New(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger, version string) (*API, error) {
	authService, err := auth.NewService(pool, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)
	if err != nil {
		return nil, err
	}
	var metadataProvider metadata.Provider
	var collectionTMDB collection.TMDBProvider
	if cfg.TMDBAccessToken != "" {
		tmdbClient := tmdb.New(cfg.TMDBAccessToken, nil)
		metadataProvider = tmdbClient
		collectionTMDB = tmdbClient
	}
	var televisionEnricher metadata.TelevisionEnricher
	if cfg.TVDBAPIKey != "" {
		televisionEnricher = tvdb.New(cfg.TVDBAPIKey, cfg.TVDBPIN, nil)
	}
	addonService := addon.NewService(pool, nil)
	var collectionTrakt collection.TraktProvider
	if cfg.TraktClientID != "" {
		collectionTrakt = collectiontrakt.New(cfg.TraktClientID, nil)
	}
	mediaProcessor, err := playback.NewFFmpegProcessor(cfg.FFmpegPath, cfg.FFprobePath, cfg.RemuxConcurrency, cfg.TranscodeThreads, playback.FFmpegOptions{
		HardwareAcceleration: cfg.HardwareAcceleration,
		VideoDevice:          cfg.VideoDevice,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize media processor: %w", err)
	}
	logger.Info("media processor initialized", "videoEncoder", mediaProcessor.VideoEncoder(), "hardwareToneMap", mediaProcessor.HardwareToneMap())
	playbackService, err := playback.NewService(pool, addonService, mediaProcessor, playback.MediaOptions{TempDirectory: cfg.MediaTempDir, MaxStorageBytes: cfg.MediaStorageBytes})
	if err != nil {
		return nil, fmt.Errorf("initialize playback service: %w", err)
	}
	metadataService := metadata.NewService(pool, metadataProvider, televisionEnricher, cfg.MetadataCacheTTL, logger)
	return &API{
		addons:              addonService,
		config:              cfg,
		calendar:            calendar.NewService(pool, metadataService, logger),
		collections:         collection.NewService(pool, addonService, collectionTMDB, collectionTrakt),
		pool:                pool,
		instances:           instance.NewService(pool, cfg.SetupToken),
		auth:                authService,
		authMaintenance:     authService,
		profiles:            profile.NewService(pool, cfg.ProfileGrantTTL),
		playback:            playbackService,
		playbackMaintenance: playbackService,
		logger:              logger,
		settings:            settings.NewService(pool),
		users:               user.NewService(pool),
		metadata:            metadataService,
		version:             version,
		watchstate:          watchstate.NewService(pool),
	}, nil
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", a.health)
	mux.HandleFunc("GET /.well-known/rivune", a.discovery)
	mux.HandleFunc("GET /api/v1/setup/status", a.setupStatus)
	mux.HandleFunc("POST /api/v1/setup", a.setup)
	mux.HandleFunc("POST /api/v1/auth/login", a.login)
	mux.HandleFunc("POST /api/v1/auth/refresh", a.refresh)
	mux.HandleFunc("POST /api/v1/auth/device-code", a.beginDeviceAuthorization)
	mux.HandleFunc("POST /api/v1/auth/device-code/token", a.exchangeDeviceAuthorization)
	mux.Handle("POST /api/v1/auth/device-code/approve", a.requireAuthentication(a.approveDeviceAuthorization))
	mux.Handle("POST /api/v1/auth/logout", a.requireAuthentication(a.logout))
	mux.Handle("GET /api/v1/auth/me", a.requireAuthentication(a.me))
	mux.Handle("GET /api/v1/auth/sessions", a.requireAuthentication(a.sessions))
	mux.Handle("DELETE /api/v1/auth/sessions/{sessionId}", a.requireAuthentication(a.revokeSession))
	mux.Handle("GET /api/v1/auth/notifications", a.requireAuthentication(a.sessionNotifications))
	mux.Handle("GET /api/v1/profiles/{profileId}/sessions", a.requireAuthentication(a.profileSessions))
	mux.Handle("DELETE /api/v1/profiles/{profileId}/sessions/{sessionId}", a.requireAuthentication(a.revokeProfileSession))
	mux.Handle("POST /api/v1/profiles/{profileId}/sessions/{sessionId}/notifications", a.requireAuthentication(a.sendProfileSessionNotification))
	mux.HandleFunc("GET /api/v1/profile-avatars/{presetId}", a.profileAvatarPresetImage)
	mux.Handle("GET /api/v1/profile-avatars", a.requireAuthentication(a.listProfileAvatarPresets))
	mux.Handle("GET /api/v1/profiles", a.requireAuthentication(a.listProfiles))
	mux.Handle("POST /api/v1/profiles", a.requireAuthentication(a.createProfile))
	mux.Handle("PATCH /api/v1/profiles/{profileId}", a.requireAuthentication(a.updateProfile))
	mux.Handle("DELETE /api/v1/profiles/{profileId}", a.requireAuthentication(a.deleteProfile))
	mux.Handle("POST /api/v1/profiles/{profileId}/select", a.requireAuthentication(a.selectProfile))
	mux.Handle("DELETE /api/v1/profiles/selection", a.requireAuthentication(a.clearProfileSelection))
	mux.Handle("GET /api/v1/profiles/{profileId}/avatar", a.requireAuthentication(a.customProfileAvatar))
	mux.Handle("PUT /api/v1/profiles/{profileId}/avatar", a.requireAuthentication(a.uploadProfileAvatar))
	mux.Handle("PUT /api/v1/profiles/{profileId}/avatar/preset", a.requireAuthentication(a.setProfileAvatarPreset))
	mux.Handle("GET /api/v1/settings", a.requireAuthentication(a.instanceSettings))
	mux.Handle("PATCH /api/v1/settings", a.requireAuthentication(a.updateInstanceSettings))
	mux.Handle("GET /api/v1/profiles/{profileId}/settings", a.requireAuthentication(a.profileSettings))
	mux.Handle("PATCH /api/v1/profiles/{profileId}/settings", a.requireAuthentication(a.updateProfileSettings))
	mux.Handle("GET /api/v1/profiles/{profileId}/settings/effective", a.requireAuthentication(a.effectiveSettings))
	mux.Handle("GET /api/v1/users", a.requireAuthentication(a.listUsers))
	mux.Handle("GET /api/v1/metadata/titles/{titleId}", a.requireAuthentication(a.movieDetails))
	mux.Handle("GET /api/v1/metadata/series/{titleId}", a.requireAuthentication(a.seriesDetails))
	mux.Handle("GET /api/v1/metadata/seasons/{seasonId}", a.requireAuthentication(a.seasonDetails))
	mux.Handle("GET /api/v1/metadata/titles/{titleId}/trailer", a.requireAuthentication(a.titleTrailer))
	mux.Handle("POST /api/v1/users", a.requireAuthentication(a.createUser))
	mux.Handle("PATCH /api/v1/users/{userId}", a.requireAuthentication(a.updateUser))
	mux.Handle("DELETE /api/v1/users/{userId}", a.requireAuthentication(a.deleteUser))
	mux.Handle("GET /api/v1/users/{userId}/profiles", a.requireAuthentication(a.userProfileAccess))
	mux.Handle("PUT /api/v1/users/{userId}/profiles/{profileId}", a.requireAuthentication(a.grantUserProfileAccess))
	mux.Handle("DELETE /api/v1/users/{userId}/profiles/{profileId}", a.requireAuthentication(a.revokeUserProfileAccess))
	mux.Handle("GET /api/v1/addons", a.requireAuthentication(a.listAddons))
	mux.Handle("POST /api/v1/addons", a.requireAuthentication(a.installAddon))
	mux.Handle("PUT /api/v1/addons/order", a.requireAuthentication(a.reorderAddons))
	mux.Handle("DELETE /api/v1/addons/{addonId}", a.requireAuthentication(a.removeAddon))
	mux.Handle("POST /api/v1/addons/{addonId}/refresh", a.requireAuthentication(a.refreshAddon))
	mux.Handle("PUT /api/v1/addons/{addonId}", a.requireAuthentication(a.updateAddon))
	mux.Handle("GET /api/v1/addons/catalogs", a.requireAuthentication(a.addonCatalogDescriptors))
	mux.Handle("GET /api/v1/addons/{addonId}/resource/{resource}/{type}/{id}", a.requireAuthentication(a.fetchAddonResource))
	mux.Handle("GET /api/v1/addons/resources/{resource}/{type}/{id}", a.requireAuthentication(a.fetchAllAddonResources))
	mux.Handle("GET /api/v1/addons/search/{type}", a.requireAuthentication(a.searchAddonCatalogs))
	mux.Handle("GET /api/v1/addons/discover", a.requireAuthentication(a.discoverAddonCatalogs))
	mux.Handle("GET /api/v1/collections", a.requireAuthentication(a.listCollections))
	mux.Handle("POST /api/v1/collections", a.requireAuthentication(a.createCollection))
	mux.Handle("PUT /api/v1/collections/order", a.requireAuthentication(a.reorderCollections))
	mux.Handle("GET /api/v1/collections/export", a.requireAuthentication(a.exportCollections))
	mux.Handle("POST /api/v1/collections/import", a.requireAuthentication(a.importCollections))
	mux.Handle("GET /api/v1/collections/tmdb/lookup", a.requireAuthentication(a.lookupCollectionTMDB))
	mux.Handle("GET /api/v1/collections/tmdb/genres", a.requireAuthentication(a.collectionTMDBGenres))
	mux.Handle("GET /api/v1/collections/{collectionId}", a.requireAuthentication(a.getCollection))
	mux.Handle("PUT /api/v1/collections/{collectionId}", a.requireAuthentication(a.updateCollection))
	mux.Handle("DELETE /api/v1/collections/{collectionId}", a.requireAuthentication(a.deleteCollection))
	mux.Handle("GET /api/v1/collections/{collectionId}/folders/{folderId}/items", a.requireAuthentication(a.resolveCollectionFolder))
	mux.Handle("POST /api/v1/titles/resolve", a.requireAuthentication(a.resolveTitle))
	mux.Handle("POST /api/v1/playback/sources", a.requireAuthentication(a.playbackSources))
	mux.Handle("POST /api/v1/playback/prepare", a.requireAuthentication(a.preparePlayback))
	mux.Handle("POST /api/v1/playback/resolve", a.requireAuthentication(a.resolvePlayback))
	mux.Handle("DELETE /api/v1/playback/sessions/{sessionId}", a.requireAuthentication(a.stopPlayback))
	mux.Handle("GET /api/v1/playback/activity", a.requireAuthentication(a.playbackActivity))
	mux.Handle("DELETE /api/v1/playback/activity/sessions/{sessionId}", a.requireAuthentication(a.stopPlaybackActivitySession))
	mux.Handle("POST /api/v1/playback/activity/purge", a.requireAuthentication(a.purgePlaybackActivity))
	mux.HandleFunc("GET /api/v1/playback/sessions/{sessionId}/assets/{assetId}", a.playbackAsset)
	mux.HandleFunc("HEAD /api/v1/playback/sessions/{sessionId}/assets/{assetId}", a.playbackAsset)
	mux.Handle("GET /api/v1/calendar", a.requireAuthentication(a.calendarEvents))
	mux.Handle("GET /api/v1/library", a.requireAuthentication(a.library))
	mux.Handle("PUT /api/v1/library/{titleId}", a.requireAuthentication(a.addLibrary))
	mux.Handle("DELETE /api/v1/library/{titleId}", a.requireAuthentication(a.removeLibrary))
	mux.Handle("GET /api/v1/progress/{titleId}", a.requireAuthentication(a.getProgress))
	mux.Handle("PUT /api/v1/progress/{titleId}", a.requireAuthentication(a.updateProgress))
	mux.Handle("DELETE /api/v1/progress/{titleId}", a.requireAuthentication(a.clearProgress))
	mux.Handle("POST /api/v1/titles/{titleId}/watched", a.requireAuthentication(a.markWatched))
	mux.Handle("DELETE /api/v1/titles/{titleId}/watched", a.requireAuthentication(a.markUnwatched))
	mux.Handle("GET /api/v1/continue-watching", a.requireAuthentication(a.continueWatching))
	mux.HandleFunc("GET /", webui.Handler)
	return a.middleware(mux)
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := a.pool.Ping(ctx); err != nil {
		a.logger.Error("database health check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status":   "unavailable",
			"database": "unavailable",
			"version":  a.version,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "ok",
		"database": "ok",
		"version":  a.version,
	})
}

func (a *API) discovery(w http.ResponseWriter, r *http.Request) {
	info, err := a.instances.Info(r.Context())
	if err != nil {
		a.internalError(w, "read instance discovery state", err)
		return
	}

	apiBaseURL := "/api/v1"
	if a.config.PublicURL != "" {
		apiBaseURL = a.config.PublicURL + apiBaseURL
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":            info.Name,
		"serverVersion":   a.version,
		"protocolVersion": protocolVersion,
		"apiBaseUrl":      apiBaseURL,
		"setupRequired":   info.SetupRequired,
	})
}

func (a *API) setupStatus(w http.ResponseWriter, r *http.Request) {
	info, err := a.instances.Info(r.Context())
	if err != nil {
		a.internalError(w, "read setup state", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"setupRequired": info.SetupRequired})
}

func (a *API) setup(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	var request struct {
		InstanceName string `json:"instanceName"`
		Admin        struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"admin"`
		ProfileName string `json:"profileName"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	result, err := a.instances.Setup(r.Context(), bearerToken(r.Header.Get("Authorization")), instance.SetupInput{
		InstanceName: request.InstanceName,
		Username:     request.Admin.Username,
		Password:     request.Admin.Password,
		ProfileName:  request.ProfileName,
	})
	switch {
	case errors.Is(err, instance.ErrInvalidSetupToken):
		writeError(w, http.StatusUnauthorized, "invalid_setup_token", "The setup token is invalid")
	case errors.Is(err, instance.ErrSetupUnavailable):
		writeError(w, http.StatusServiceUnavailable, "setup_unavailable", "The server administrator must configure a setup token")
	case errors.Is(err, instance.ErrAlreadyConfigured):
		writeError(w, http.StatusConflict, "already_configured", "This Rivune instance is already configured")
	case errors.Is(err, instance.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_setup", strings.TrimPrefix(err.Error(), instance.ErrInvalidInput.Error()+": "))
	case err != nil:
		a.internalError(w, "initialize instance", err)
	default:
		writeJSON(w, http.StatusCreated, map[string]any{
			"instance": map[string]string{"id": result.InstanceID},
			"admin":    map[string]string{"id": result.UserID},
			"profile":  map[string]string{"id": result.ProfileID},
		})
	}
}

func (a *API) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.WithClientIP(r.Context(), requestClientIP(r, a.config.TrustedProxies)))
		started := time.Now()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		defer func() {
			if recovered := recover(); recovered != nil {
				a.logger.Error("panic serving request", "panic", recovered, "method", r.Method, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred")
			}
			a.logger.Info("request completed", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
		}()
		next.ServeHTTP(w, r)
	})
}

func (a *API) internalError(w http.ResponseWriter, operation string, err error) {
	a.logger.Error(operation, "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred")
}

func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	return true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	return decodeJSONLimit(w, r, destination, 64*1024)
}

func decodeJSONLimit(w http.ResponseWriter, r *http.Request, destination any, maximumBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maximumBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if !utf8.Valid(body) {
		return errors.New("invalid JSON body: malformed UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func bearerToken(authorization string) string {
	scheme, token, found := strings.Cut(strings.TrimSpace(authorization), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
