package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/addon"
	artworkcache "github.com/moodiness/rivune/server/internal/artwork"
	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/calendar"
	"github.com/moodiness/rivune/server/internal/category"
	"github.com/moodiness/rivune/server/internal/collection"

	"github.com/moodiness/rivune/server/internal/config"
	"github.com/moodiness/rivune/server/internal/demo"
	"github.com/moodiness/rivune/server/internal/instance"
	"github.com/moodiness/rivune/server/internal/jellyfin"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/netguard"
	"github.com/moodiness/rivune/server/internal/operations"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/portable"
	"github.com/moodiness/rivune/server/internal/profile"
	"github.com/moodiness/rivune/server/internal/providers"
	"github.com/moodiness/rivune/server/internal/requestwork"
	"github.com/moodiness/rivune/server/internal/runtimesettings"
	"github.com/moodiness/rivune/server/internal/settings"
	"github.com/moodiness/rivune/server/internal/tracking"
	"github.com/moodiness/rivune/server/internal/user"
	"github.com/moodiness/rivune/server/internal/watchstate"
	"github.com/moodiness/rivune/server/internal/webui"
)

const protocolVersion = 20

var nativeCapabilities = [...]string{
	"bounded-aggregate-resources",
	"profile-archives-v1",
	"request-correlation",
}

type instanceService interface {
	Info(context.Context) (instance.Info, error)
	Setup(context.Context, string, instance.SetupInput) (instance.SetupResult, error)
	AcquireSetupPending(context.Context) (func(), error)
	AdmitDemoSession(context.Context, [sha256.Size]byte, string, time.Time, time.Time, int, int, func() error) (string, func(), error)
	ReleaseDemoSession(context.Context, string) (func(), error)
}

type authService interface {
	Login(context.Context, auth.LoginInput) (auth.TokenPair, error)
	LoginJellyfinProfile(context.Context, auth.JellyfinProfileLoginInput) (auth.JellyfinProfileLoginResult, error)
	Refresh(context.Context, string) (auth.TokenPair, error)
	Authenticate(context.Context, string) (auth.Principal, error)
	Account(context.Context, auth.Principal) (auth.Account, error)
	Logout(context.Context, auth.Principal) error
	Sessions(context.Context, auth.Principal) ([]auth.Session, error)
	RevokeSession(context.Context, auth.Principal, string) error
	ProfileSessions(context.Context, auth.Principal, string) ([]auth.Session, error)
	RevokeProfileSession(context.Context, auth.Principal, string, string) error
	SessionNotifications(context.Context, auth.Principal, int64) ([]auth.SessionNotification, error)
	AcknowledgeSessionNotification(context.Context, auth.Principal, int64) error
	BroadcastSessionNotification(context.Context, auth.Principal, string, string) (auth.NotificationBroadcast, error)
	SendProfileSessionNotification(context.Context, auth.Principal, string, string, string) (auth.SessionNotification, error)
	BeginDeviceAuthorization(context.Context, string, string) (auth.DeviceAuthorization, error)
	ApproveDeviceAuthorization(context.Context, auth.Principal, auth.DeviceAuthorizationApproval) error
	ExchangeDeviceAuthorization(context.Context, string) (auth.TokenPair, error)
}

type profileService interface {
	List(context.Context, auth.Principal) ([]profile.Profile, error)
	Create(context.Context, auth.Principal, profile.CreateInput) (profile.Profile, error)
	Update(context.Context, auth.Principal, string, profile.UpdateInput) (profile.Profile, error)
	Delete(context.Context, auth.Principal, string) error
	Select(context.Context, auth.Principal, string, *string, bool) (profile.Selection, error)
	ClearSelection(context.Context, auth.Principal) error
	SetAvatarPreset(context.Context, auth.Principal, string, string) (profile.Profile, error)
	AuthorizeAvatarUpload(context.Context, auth.Principal, string) error
	SetAvatarImage(context.Context, auth.Principal, string, []byte) (profile.Profile, error)
	AvatarImage(context.Context, auth.Principal, string) (profile.AvatarImage, error)
}

type categoryService interface {
	List(context.Context, category.Actor) ([]category.Category, error)
	Create(context.Context, category.Actor, category.CreateInput) (category.Category, error)
	Update(context.Context, category.Actor, string, category.UpdateInput) (category.Category, error)
	Delete(context.Context, category.Actor, string, *string) error
	Reorder(context.Context, category.Actor, []string) ([]category.Category, error)
	MoveProfile(context.Context, category.Actor, string, string) error
	MoveProfiles(context.Context, category.Actor, []string, string) error
	ListDevices(context.Context, category.Actor, *string) ([]category.Device, error)
	UpdateDevice(context.Context, category.Actor, string, category.DeviceUpdateInput) (category.Device, error)
	DeleteDevice(context.Context, category.Actor, string) error
	MoveDevice(context.Context, category.Actor, string, string) error
	MoveDevices(context.Context, category.Actor, []string, string) error
}

type settingsService interface {
	Instance(context.Context) (settings.Layer, error)
	Maintenance(context.Context) (settings.Maintenance, error)
	UpdateMaintenance(context.Context, auth.Principal, settings.Maintenance) (settings.Maintenance, error)
	UpdateInstance(context.Context, auth.Principal, settings.Patch) (settings.Layer, error)
	Profile(context.Context, auth.Principal, string) (settings.Layer, error)
	UpdateProfile(context.Context, auth.Principal, string, settings.Patch) (settings.Layer, error)
	Effective(context.Context, auth.Principal, string) (settings.Effective, error)
}

type integrationSettingsService interface {
	IntegrationStatus(context.Context, auth.Principal) (settings.IntegrationStatus, error)
	UpdateIntegrationCredentials(context.Context, auth.Principal, settings.IntegrationCredentialsPatch) (settings.IntegrationStatus, error)
	ListAuditEvents(context.Context, auth.Principal, *int64, int) (settings.AuditPage, error)
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
	Install(context.Context, auth.Principal, addon.InstallInput) (addon.ManagedAddon, error)
	Preview(context.Context, auth.Principal, addon.InstallInput) (addon.AddonPreview, error)
	List(context.Context, auth.Principal) ([]addon.InstalledAddon, error)
	Diagnostics(context.Context, auth.Principal) (addon.Diagnostics, error)
	Management(context.Context, auth.Principal, string) (addon.ManagedAddon, error)
	Remove(context.Context, auth.Principal, string) error
	Reorder(context.Context, auth.Principal, addon.ReorderInput) ([]addon.InstalledAddon, error)
	Refresh(context.Context, auth.Principal, string) (addon.InstalledAddon, error)
	Update(context.Context, auth.Principal, string, addon.UpdateAddonInput) (addon.ManagedAddon, error)
	Catalogs(context.Context, auth.Principal) ([]addon.CatalogDescriptor, error)
	Fetch(context.Context, auth.Principal, string, addon.ResourcePath) (addon.ResourceResult, error)
	FetchAll(context.Context, auth.Principal, addon.ResourcePath) (addon.ResourceBatch, error)
	SearchCatalogs(context.Context, auth.Principal, string, addon.CatalogSearchInput) (addon.ResourceBatch, error)
	FetchCatalogs(context.Context, auth.Principal, string, []addon.ExtraValue, bool) (addon.ResourceBatch, error)
}

type collectionService interface {
	List(context.Context, auth.Principal) ([]collection.Collection, error)
	Export(context.Context, auth.Principal) (collection.ExportDocument, error)
	Get(context.Context, auth.Principal, string) (collection.Collection, error)
	Management(context.Context, auth.Principal, string) (collection.Collection, error)
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
	LinkStatus(context.Context, auth.Principal, string) (calendar.Link, error)
	CreateLink(context.Context, auth.Principal, string) (calendar.Credential, error)
	RotateLink(context.Context, auth.Principal, string) (calendar.Credential, error)
	RevokeLink(context.Context, auth.Principal, string) error
	Feed(context.Context, string, bool) ([]byte, error)
}

type jellyfinCredentialService interface {
	Status(context.Context, auth.Principal, string) (jellyfin.CredentialStatus, error)
	Create(context.Context, auth.Principal, string) (jellyfin.ProfileCredential, error)
	Rotate(context.Context, auth.Principal, string) (jellyfin.ProfileCredential, error)
	Revoke(context.Context, auth.Principal, string) error
}

type calendarRefreshWorker interface {
	Run(context.Context)
}

type metadataService interface {
	DiscoverMovies(context.Context, auth.Principal, metadata.QueryOptions) (metadata.MoviePage, error)
	SearchMovies(context.Context, auth.Principal, metadata.SearchOptions) (metadata.MoviePage, error)
	MovieDetails(context.Context, auth.Principal, string, string) (metadata.Movie, error)
	DiscoverSeries(context.Context, auth.Principal, metadata.QueryOptions) (metadata.SeriesPage, error)
	SearchSeries(context.Context, auth.Principal, metadata.SearchOptions) (metadata.SeriesPage, error)
	SeriesDetails(context.Context, auth.Principal, string, metadata.SeriesDetailsOptions) (metadata.Series, error)
	SeasonDetails(context.Context, auth.Principal, string, string, string) (metadata.Season, error)
	Trailers(context.Context, auth.Principal, string, string, string, *int) (metadata.TrailerList, error)
}

type watchstateService interface {
	ResolveTitle(context.Context, auth.Principal, watchstate.ResolveTitleInput) (watchstate.TitleReference, error)
	ResolveCustomSeries(context.Context, auth.Principal, watchstate.ResolveCustomSeriesInput) (watchstate.ResolveCustomSeriesResult, error)
	AddLibrary(context.Context, auth.Principal, string) (watchstate.LibraryItem, error)
	RemoveLibrary(context.Context, auth.Principal, string) error
	Library(context.Context, auth.Principal, string, int, int) (watchstate.LibraryPage, error)
	TVLibraryMembership(context.Context, auth.Principal, []watchstate.TVLibraryIdentity) (watchstate.TVLibraryMembershipResult, error)
	GetProgress(context.Context, auth.Principal, string) (watchstate.Progress, error)
	GetProgressBatch(context.Context, auth.Principal, []string) (watchstate.ProgressBatch, error)
	UpdateProgress(context.Context, auth.Principal, string, watchstate.UpdateProgressInput) (watchstate.Progress, error)
	SetWatched(context.Context, auth.Principal, string, bool, watchstate.CompletionInput) (watchstate.Progress, error)
	SetWatchedBatch(context.Context, auth.Principal, []watchstate.SetWatchedBatchItem) (watchstate.ProgressBatch, error)
	ClearProgress(context.Context, auth.Principal, string, int64) error
	ContinueWatching(context.Context, auth.Principal, string, int) (watchstate.ContinuePage, error)
	DismissContinue(context.Context, auth.Principal, string) error
}

type trackingService interface {
	Statuses(context.Context, auth.Principal, string) ([]tracking.Status, error)
	BeginDeviceAuthorization(context.Context, auth.Principal, string, string) (tracking.DeviceAuthorization, error)
	CompleteDeviceAuthorization(context.Context, auth.Principal, string, string, string) (tracking.Status, error)
	UpdatePreferences(context.Context, auth.Principal, string, string, tracking.PreferencesInput) (tracking.Status, error)
	Disconnect(context.Context, auth.Principal, string, string) error
	Run(context.Context)
}

type playbackService interface {
	Sources(context.Context, auth.Principal, playback.SourcesInput) (playback.SourceList, error)
	Markers(context.Context, auth.Principal, playback.MarkerInput) (playback.MarkerList, error)
	Prepare(context.Context, auth.Principal, playback.PrepareInput) (playback.Preparation, error)
	Resolve(context.Context, auth.Principal, playback.ResolveInput) (playback.Session, error)
	Stop(context.Context, auth.Principal, string) error
	Activity(context.Context, auth.Principal) (playback.Activity, error)
	StopActivitySession(context.Context, auth.Principal, string) error
	PurgeActivity(context.Context, auth.Principal) (playback.PurgeResult, error)
	ProxyAsset(http.ResponseWriter, *http.Request, string, string, string, string) error
}

type operationsService interface {
	Overview(context.Context, auth.Principal) (operations.OperationsOverview, error)
	UpdateMetadataRefreshSchedule(context.Context, auth.Principal, operations.MetadataRefreshScheduleInput) (operations.MetadataRefreshSchedule, error)
	RunAction(context.Context, auth.Principal, operations.OperationAction) (operations.OperationRun, error)
	RunScheduled(context.Context) error
}

type collectionArtworkPresenter interface {
	PresentCollections(context.Context, []collection.Collection)
	RestoreCollectionSaveInput(context.Context, *collection.SaveInput)
	LocalizeCollectionLookupResults(context.Context, []collection.LookupResult)
}

type catalogArtworkPresenter interface {
	LocalizeCatalogDescriptors(context.Context, []addon.CatalogDescriptor)
}

type databasePinger interface {
	Ping(context.Context) error
}

type portableService interface {
	Export(context.Context, auth.Principal, string) (portable.Document, error)
	Import(context.Context, auth.Principal, string, portable.Document) (portable.ImportReport, error)
}

type API struct {
	config                                  config.Config
	addons                                  addonService
	artwork                                 *artworkcache.Service
	catalogArtwork                          catalogArtworkPresenter
	collectionArtwork                       collectionArtworkPresenter
	calendar                                calendarService
	calendarRefresh                         calendarRefreshWorker
	categories                              categoryService
	pool                                    databasePinger
	instances                               instanceService
	demo                                    *demo.Service
	jellyfinCredentials                     jellyfinCredentialService
	jellyfinCompatibility                   *jellyfin.Handler
	jellyfinCompatibilityGeneration         *jellyfin.Handler
	jellyfinCompatibilityMu                 sync.Mutex
	jellyfinCompatibilityDesired            bool
	jellyfinCompatibilityRevision           uint64
	jellyfinCompatibilitySettingsMu         sync.Mutex
	jellyfinCompatibilityRevocationPending  bool
	jellyfinCompatibilityRevoker            func(context.Context, string) error
	jellyfinCompatibilityCancel             context.CancelFunc
	jellyfinCompatibilityDone               <-chan struct{}
	jellyfinCompatibilityRunOnce            sync.Once
	jellyfinCompatibilitySignal             chan struct{}
	jellyfinCompatibilityBuilder            func(context.Context) (*jellyfin.Handler, bool, error)
	jellyfinCompatibilityRunner             func(context.Context, *jellyfin.Handler)
	jellyfinCompatibilityBackoff            func(uint32) time.Duration
	jellyfinCompatibilityRetrySeed          uint64
	jellyfinCompatibilityReconciler         func(context.Context) (bool, error)
	jellyfinCompatibilityReconcileRequested bool
	jellyfinCompatibilityPollInterval       time.Duration
	collections                             collectionService
	auth                                    authService
	authMaintenance                         authMaintenanceService
	profiles                                profileService
	playback                                playbackService
	playbackMaintenance                     playbackMaintenanceService
	operations                              operationsService
	portable                                portableService
	settings                                settingsService
	integrationConfiguration                integrationSettingsService
	runtimeSettings                         *runtimeSettingsCoordinator
	users                                   userService
	metadata                                metadataService
	logger                                  *slog.Logger
	version                                 string
	watchstate                              watchstateService
	tracking                                trackingService
	credentialAdmission                     *requestAdmission
	usernameAdmission                       *usernameAdmission
	deviceCodeAdmission                     *requestAdmission
	calendarFeedAdmission                   *requestAdmission
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code          string  `json:"code"`
	Message       string  `json:"message"`
	PublicMessage *string `json:"publicMessage,omitempty"`
}

func New(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger, version string) (*API, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg.EncryptionKeys == nil {
		return nil, errors.New("RIVUNE_ENCRYPTION_KEYS is required")
	}
	settingsManager := settings.NewService(pool, cfg.EncryptionKeys)
	legacyEnvironment, err := config.LoadLegacyEnvironment()
	if err != nil {
		return nil, fmt.Errorf("load legacy environment import: %w", err)
	}
	if _, err := settingsManager.ImportLegacyEnvironment(ctx, legacyEnvironment); err != nil {
		return nil, fmt.Errorf("import legacy environment: %w", err)
	}
	instanceLayer, err := settingsManager.Instance(ctx)
	var runtimeValues runtimesettings.Values
	if errors.Is(err, pgx.ErrNoRows) {
		runtimeValues = defaultRuntimeValues()
	} else if err != nil {
		return nil, fmt.Errorf("load canonical instance settings: %w", err)
	} else {
		runtimeValues, err = runtimeValuesFromLayer(instanceLayer)
		if err != nil {
			return nil, fmt.Errorf("load canonical runtime settings: %w", err)
		}
	}
	integrationCredentials, err := settingsManager.LoadIntegrationCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("load integration credentials: %w", err)
	}
	const metadataCacheTTL = 24 * time.Hour
	providerOptions := providers.BuildOptions{Pool: pool, MetadataCacheTTL: metadataCacheTTL, Logger: logger}
	initialProviders, err := providers.Build(providerCredentials(integrationCredentials), providerOptions)
	if err != nil {
		return nil, fmt.Errorf("build integration providers: %w", err)
	}
	integrationCredentials = settings.IntegrationCredentials{}
	providerRuntime := providers.NewRuntime(initialProviders, providerOptions)
	settingsManager.SetIntegrationPublisher(newIntegrationRuntimeCoordinator(providerRuntime))

	mediaProcessor, err := playback.NewFFmpegProcessor(cfg.FFmpegPath, cfg.FFprobePath, runtimeValues.TranscodeConcurrency, cfg.TranscodeThreads, playback.FFmpegOptions{
		HardwareAcceleration: runtimeValues.HardwareAcceleration,
		PreferredVideoCodec:  runtimeValues.PreferredTranscodeVideoCodec,
		QualityPreset:        runtimeValues.TranscodeQualityPreset,
		VideoDevice:          cfg.VideoDevice,
		MaximumReadRate:      cfg.TranscodeMaxReadRate,
		Logger:               logger,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize media processor: %w", err)
	}
	mediaDiagnostics := mediaProcessor.PlaybackDiagnostics()
	runtimeValues.HardwareAcceleration = mediaDiagnostics.HardwareAcceleration
	runtimeSource, err := runtimesettings.New(runtimeValues)
	if err != nil {
		return nil, fmt.Errorf("initialize runtime settings: %w", err)
	}
	runtimeSnapshot := runtimeSource.Load()
	logger.Info("media processor initialized",
		"ffmpegVersion", mediaDiagnostics.FFmpegVersion,
		"ffprobeVersion", mediaDiagnostics.FFprobeVersion,
		"hardwareAcceleration", mediaDiagnostics.HardwareAcceleration,
		"videoEncoder", mediaDiagnostics.VideoEncoder,
		"preferredVideoCodec", mediaDiagnostics.PreferredVideoCodec,
		"encodeCodecs", mediaDiagnostics.EncodeCodecs,
		"decodeCodecs", mediaDiagnostics.DecodeCodecs,
		"hevcMain10", mediaDiagnostics.HEVCMain10,
		"qualityPreset", mediaDiagnostics.QualityPreset,
		"hardwareToneMap", mediaDiagnostics.HardwareToneMap,
		"toneMapBackend", mediaDiagnostics.ToneMapBackend,
		"transcodeThreads", mediaDiagnostics.TranscodeThreads,
		"transcodeConcurrency", runtimeSnapshot.TranscodeConcurrency,
		"processLimit", mediaDiagnostics.Pools.Process.Limit,
		"probeLimit", mediaDiagnostics.Pools.Probe.Limit,
		"subtitleLimit", mediaDiagnostics.Pools.Subtitle.Limit,
		"trickplayLimit", mediaDiagnostics.Pools.Trickplay.Limit,
		"maximumVideoBitrateKbps", runtimeSnapshot.TranscodeMaxBitrateKbps,
		"maximumReadRate", cfg.TranscodeMaxReadRate,
		"initialHLSBufferSeconds", cfg.HLSInitialBufferSeconds,
		"maximumMediaStorageBytes", runtimeSnapshot.MediaMaxStorageBytes,
	)

	addonService := addon.NewService(pool, nil, logger)
	artworkService, err := artworkcache.New(pool, artworkcache.Options{
		Directory:         cfg.ArtworkCacheDir,
		MaxBytes:          runtimeSnapshot.ArtworkMaxStorageBytes,
		LANArtworkOrigins: cfg.LANArtworkOrigins,
		Logger:            logger,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize artwork cache: %w", err)
	}
	playbackService, err := playback.NewServiceWithRuntimeSettings(pool, addonService, mediaProcessor, playback.MediaOptions{
		TempDirectory:             cfg.MediaTempDir,
		MaxStorageBytes:           runtimeSnapshot.MediaMaxStorageBytes,
		TranscodeVideoBitrateKbps: runtimeSnapshot.TranscodeMaxBitrateKbps,
		InitialBufferSeconds:      cfg.HLSInitialBufferSeconds,
	}, runtimeSource)
	if err != nil {
		return nil, fmt.Errorf("initialize playback service: %w", err)
	}
	trackingService, err := tracking.NewServiceWithProviderSource(pool, cfg.EncryptionKeys, providerRuntime, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize tracking integrations: %w", err)
	}
	metadataService := metadata.NewServiceWithRuntimeSettings(pool, providerRuntime, metadataCacheTTL, logger, runtimeSource)
	authService, err := auth.NewServiceWithRuntimeSettings(pool, cfg.AccessTokenTTL, cfg.RefreshTokenTTL, runtimeSource)
	if err != nil {
		return nil, err
	}
	calendarService, err := calendar.NewServiceWithRuntimeSettings(pool, metadataService, runtimeSource, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize calendar service: %w", err)
	}
	collectionService := collection.NewServiceWithProviderSource(pool, addonService, providerRuntime)
	collectionService.SetArtworkPresenter(artworkService)
	watchstateService := watchstate.NewServiceWithRuntimeSettings(pool, runtimeSource, providerRuntime, trackingService)
	profileManager := profile.NewServiceWithRuntimeSettings(pool, cfg.ProfileGrantTTL, runtimeSource)
	instanceManager := instance.NewServiceWithRuntimeSettings(pool, cfg.SetupToken, runtimeSource)
	portableService := portable.NewService(pool, runtimeSource)
	userManager := user.NewService(pool)
	runtimeCoordinator := newRuntimeSettingsCoordinator(runtimeSource, settingsManager, artworkService, playbackService)
	operationsService := operations.NewService(
		pool, metadataService, authService, playbackService, maintenanceInterval, logger,
	)
	jellyfinCredentials, err := jellyfin.NewCredentialStore(pool, runtimeSource)
	if err != nil {
		return nil, fmt.Errorf("initialize Jellyfin profile credentials: %w", err)
	}
	api := &API{
		artwork:                  artworkService,
		catalogArtwork:           artworkService,
		collectionArtwork:        artworkService,
		addons:                   addonService,
		config:                   cfg,
		calendar:                 calendarService,
		calendarRefresh:          calendarService,
		categories:               category.NewService(pool),
		collections:              collectionService,
		pool:                     pool,
		instances:                instanceManager,
		demo:                     demo.New(instanceManager, demo.Options{}),
		jellyfinCredentials:      jellyfinCredentials,
		auth:                     authService,
		authMaintenance:          authService,
		profiles:                 profileManager,
		playback:                 playbackService,
		playbackMaintenance:      playbackService,
		logger:                   logger,
		settings:                 settingsManager,
		integrationConfiguration: settingsManager,
		runtimeSettings:          runtimeCoordinator,
		users:                    userManager,
		metadata:                 metadataService,
		operations:               operationsService,
		portable:                 portableService,
		version:                  version,
		tracking:                 trackingService,
		watchstate:               watchstateService,
		credentialAdmission:      newCredentialAdmission(),
		usernameAdmission:        newCredentialUsernameAdmission(),
		deviceCodeAdmission:      newDeviceCodeAdmission(),
		calendarFeedAdmission:    newCalendarFeedAdmission(),
	}
	compatibilitySessionRevoked := api.forgetJellyfinCompatibilitySessions
	jellyfinCredentials.SetCompatibilitySessionRevocationNotifier(compatibilitySessionRevoked)
	profileManager.SetCompatibilitySessionRevocationNotifier(compatibilitySessionRevoked)
	authService.SetCompatibilitySessionRevocationNotifier(compatibilitySessionRevoked)
	userManager.SetCompatibilitySessionRevocationNotifier(compatibilitySessionRevoked)
	api.jellyfinCompatibilityDesired = runtimeSnapshot.JellyfinEnabled
	api.jellyfinCompatibilityReconciler = func(reconcileContext context.Context) (bool, error) {
		layer, readErr := settingsManager.Instance(reconcileContext)
		if readErr != nil {
			return false, readErr
		}
		if layer.Values.JellyfinEnabled == nil {
			return false, errors.New("instance Jellyfin setting is missing")
		}
		return *layer.Values.JellyfinEnabled, nil
	}
	api.initializeJellyfinCompatibility(pool, authService, watchstateService, collectionService, artworkService, playbackService, instanceManager, metadataService, addonService, runtimeSource)
	runtimeCoordinator.onReconciled = func(previous, current runtimesettings.Snapshot) {
		if previous.JellyfinEnabled != current.JellyfinEnabled {
			if err := api.applyCanonicalJellyfinCompatibilityDesired(context.Background(), current.JellyfinEnabled); err != nil {
				api.logger.Error("reconcile Jellyfin compatibility after runtime publication", "error", err)
				api.requestJellyfinCompatibilityReconciliation()
			}
			return
		}
		if previous.JellyfinDebug != current.JellyfinDebug {
			api.RequestJellyfinCompatibilityReplacement()
		}
	}
	return api, nil
}

func (a *API) forgetJellyfinCompatibilitySessions(sessionIDs []string) {
	if a == nil || len(sessionIDs) == 0 {
		return
	}
	a.jellyfinCompatibilityMu.Lock()
	defer a.jellyfinCompatibilityMu.Unlock()
	active := a.jellyfinCompatibility
	generation := a.jellyfinCompatibilityGeneration
	if active != nil {
		active.ForgetCompatibilitySessions(sessionIDs)
	}
	if generation != nil && generation != active {
		generation.ForgetCompatibilitySessions(sessionIDs)
	}
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", a.readiness)
	mux.HandleFunc("GET /live", a.liveness)
	mux.HandleFunc("GET /ready", a.readiness)
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
	mux.Handle("DELETE /api/v1/auth/notifications/{notificationId}", a.requireAuthentication(a.acknowledgeSessionNotification))
	mux.Handle("POST /api/v1/auth/notifications/broadcast", a.requireAuthentication(a.broadcastSessionNotification))
	mux.Handle("GET /api/v1/profiles/{profileId}/sessions", a.requireAuthentication(a.profileSessions))
	mux.Handle("DELETE /api/v1/profiles/{profileId}/sessions/{sessionId}", a.requireAuthentication(a.revokeProfileSession))
	mux.Handle("POST /api/v1/profiles/{profileId}/sessions/{sessionId}/notifications", a.requireAuthentication(a.sendProfileSessionNotification))
	mux.HandleFunc("GET /api/v1/profile-avatars/{presetId}", a.profileAvatarPresetImage)
	mux.Handle("GET /api/v1/profile-avatars", a.requireAuthentication(a.listProfileAvatarPresets))
	mux.Handle("GET /api/v1/categories", a.requireAuthentication(a.listCategories))
	mux.Handle("POST /api/v1/categories", a.requireAuthentication(a.createCategory))
	mux.Handle("PUT /api/v1/categories/order", a.requireAuthentication(a.reorderCategories))
	mux.Handle("PATCH /api/v1/categories/{categoryId}", a.requireAuthentication(a.updateCategory))
	mux.Handle("DELETE /api/v1/categories/{categoryId}", a.requireAuthentication(a.deleteCategory))
	mux.Handle("GET /api/v1/devices", a.requireAuthentication(a.listDevices))
	mux.Handle("PATCH /api/v1/devices/{deviceId}", a.requireAuthentication(a.updateDevice))
	mux.Handle("DELETE /api/v1/devices/{deviceId}", a.requireAuthentication(a.deleteDevice))
	mux.Handle("POST /api/v1/profiles/category-moves", a.requireAuthentication(a.moveProfilesToCategory))
	mux.Handle("POST /api/v1/devices/category-moves", a.requireAuthentication(a.moveDevicesToCategory))
	mux.Handle("GET /api/v1/profiles", a.requireAuthentication(a.listProfiles))
	mux.Handle("POST /api/v1/profiles", a.requireAuthentication(a.createProfile))
	mux.Handle("PATCH /api/v1/profiles/{profileId}", a.requireAuthentication(a.updateProfile))
	mux.Handle("DELETE /api/v1/profiles/{profileId}", a.requireAuthentication(a.deleteProfile))
	mux.Handle("POST /api/v1/profiles/{profileId}/select", a.requireAuthentication(a.selectProfile))
	mux.Handle("DELETE /api/v1/profiles/selection", a.requireAuthentication(a.clearProfileSelection))
	mux.Handle("GET /api/v1/profiles/{profileId}/avatar", a.requireAuthentication(a.customProfileAvatar))
	mux.Handle("PUT /api/v1/profiles/{profileId}/avatar", a.requireAuthentication(a.uploadProfileAvatar))
	mux.Handle("GET /api/v1/profiles/{profileId}/jellyfin-credential", a.requireAuthentication(a.jellyfinCredentialStatus))
	mux.Handle("POST /api/v1/profiles/{profileId}/jellyfin-credential", a.requireAuthentication(a.createJellyfinCredential))
	mux.Handle("POST /api/v1/profiles/{profileId}/jellyfin-credential/rotate", a.requireAuthentication(a.rotateJellyfinCredential))
	mux.Handle("DELETE /api/v1/profiles/{profileId}/jellyfin-credential", a.requireAuthentication(a.revokeJellyfinCredential))
	mux.Handle("PUT /api/v1/profiles/{profileId}/avatar/preset", a.requireAuthentication(a.setProfileAvatarPreset))
	mux.Handle("GET /api/v1/profiles/{profileId}/archive", a.requireAuthentication(a.exportProfileArchive))
	mux.Handle("POST /api/v1/profiles/{profileId}/archive/import", a.requireAuthentication(a.importProfileArchive))
	mux.Handle("GET /api/v1/settings", a.requireAuthentication(a.instanceSettings))
	mux.Handle("PATCH /api/v1/settings", a.requireAuthentication(a.updateInstanceSettings))
	mux.Handle("GET /api/v1/settings/integrations", a.requireAuthentication(a.integrationSettings))
	mux.Handle("PATCH /api/v1/settings/integrations", a.requireAuthentication(a.updateIntegrationSettings))
	mux.Handle("GET /api/v1/settings/audit", a.requireAuthentication(a.configurationAudit))
	mux.Handle("GET /api/v1/settings/maintenance", a.requireAuthentication(a.maintenanceSettings))
	mux.Handle("PUT /api/v1/settings/maintenance", a.requireAuthentication(a.updateMaintenanceSettings))
	mux.Handle("GET /api/v1/operations", a.requireAuthentication(a.operationsOverview))
	mux.Handle("PUT /api/v1/operations/schedules/metadata-refresh", a.requireAuthentication(a.updateMetadataRefreshSchedule))
	mux.Handle("POST /api/v1/operations/actions/{action}", a.requireAuthentication(a.runOperationAction))
	mux.HandleFunc("GET /api/v1/artwork/{key}", a.artworkAsset)
	mux.HandleFunc("HEAD /api/v1/artwork/{key}", a.artworkAsset)
	mux.Handle("GET /api/v1/profiles/{profileId}/settings", a.requireAuthentication(a.profileSettings))
	mux.Handle("PATCH /api/v1/profiles/{profileId}/settings", a.requireAuthentication(a.updateProfileSettings))
	mux.Handle("GET /api/v1/profiles/{profileId}/settings/effective", a.requireAuthentication(a.effectiveSettings))
	mux.Handle("GET /api/v1/profiles/{profileId}/tracking", a.requireAuthentication(a.trackingStatuses))
	mux.Handle("POST /api/v1/profiles/{profileId}/tracking/{provider}/device-code", a.requireAuthentication(a.beginTrackingAuthorization))
	mux.Handle("POST /api/v1/profiles/{profileId}/tracking/{provider}/device-code/{authorizationId}/token", a.requireAuthentication(a.completeTrackingAuthorization))
	mux.Handle("PATCH /api/v1/profiles/{profileId}/tracking/{provider}", a.requireAuthentication(a.updateTrackingPreferences))
	mux.Handle("DELETE /api/v1/profiles/{profileId}/tracking/{provider}", a.requireAuthentication(a.disconnectTracking))
	mux.Handle("GET /api/v1/users", a.requireAuthentication(a.listUsers))
	mux.Handle("GET /api/v1/metadata/titles/{titleId}", a.requireAuthentication(a.movieDetails))
	mux.Handle("GET /api/v1/metadata/series/{titleId}", a.requireAuthentication(a.seriesDetails))
	mux.Handle("GET /api/v1/metadata/seasons/{seasonId}", a.requireAuthentication(a.seasonDetails))
	mux.Handle("GET /api/v1/metadata/titles/{titleId}/trailers", a.requireAuthentication(a.titleTrailers))
	mux.Handle("POST /api/v1/users", a.requireAuthentication(a.createUser))
	mux.Handle("PATCH /api/v1/users/{userId}", a.requireAuthentication(a.updateUser))
	mux.Handle("DELETE /api/v1/users/{userId}", a.requireAuthentication(a.deleteUser))
	mux.Handle("GET /api/v1/users/{userId}/profiles", a.requireAuthentication(a.userProfileAccess))
	mux.Handle("PUT /api/v1/users/{userId}/profiles/{profileId}", a.requireAuthentication(a.grantUserProfileAccess))
	mux.Handle("DELETE /api/v1/users/{userId}/profiles/{profileId}", a.requireAuthentication(a.revokeUserProfileAccess))
	mux.Handle("GET /api/v1/addons", a.requireAuthentication(a.listAddons))
	mux.Handle("GET /api/v1/addons/diagnostics", a.requireAuthentication(a.addonDiagnostics))
	mux.Handle("GET /api/v1/addons/{addonId}/management", a.requireAuthentication(a.addonManagement))
	mux.Handle("POST /api/v1/addons", a.requireAuthentication(a.installAddon))
	mux.Handle("POST /api/v1/addons/preview", a.requireAuthentication(a.previewAddon))
	mux.Handle("PUT /api/v1/addons/order", a.requireAuthentication(a.reorderAddons))
	mux.Handle("DELETE /api/v1/addons/{addonId}", a.requireAuthentication(a.removeAddon))
	mux.Handle("POST /api/v1/addons/{addonId}/refresh", a.requireAuthentication(a.refreshAddon))
	mux.Handle("PUT /api/v1/addons/{addonId}", a.requireAuthentication(a.updateAddon))
	mux.Handle("GET /api/v1/addons/catalogs", a.requireAuthentication(a.addonCatalogDescriptors))
	mux.Handle("GET /api/v1/addons/{addonId}/resource/{resource}/{type}/{id}", a.requireAuthentication(a.fetchAddonResource))
	mux.Handle("GET /api/v1/addons/resources/{resource}/{type}/{id}", a.requireAuthentication(a.fetchAllAddonResources))
	mux.Handle("GET /api/v1/addons/catalogs/search/{type}", a.requireAuthentication(a.searchAddonCatalogs))
	mux.Handle("GET /api/v1/addons/discover", a.requireAuthentication(a.discoverAddonCatalogs))
	mux.Handle("GET /api/v1/collections", a.requireAuthentication(a.listCollections))
	mux.Handle("POST /api/v1/collections", a.requireAuthentication(a.createCollection))
	mux.Handle("PUT /api/v1/collections/order", a.requireAuthentication(a.reorderCollections))
	mux.Handle("GET /api/v1/collections/export", a.requireAuthentication(a.exportCollections))
	mux.Handle("POST /api/v1/collections/import", a.requireAuthentication(a.importCollections))
	mux.Handle("GET /api/v1/collections/tmdb/lookup", a.requireAuthentication(a.lookupCollectionTMDB))
	mux.Handle("GET /api/v1/collections/tmdb/genres", a.requireAuthentication(a.collectionTMDBGenres))
	mux.Handle("GET /api/v1/collections/{collectionId}", a.requireAuthentication(a.getCollection))
	mux.Handle("GET /api/v1/collections/{collectionId}/management", a.requireAuthentication(a.collectionManagement))
	mux.Handle("PUT /api/v1/collections/{collectionId}", a.requireAuthentication(a.updateCollection))
	mux.Handle("DELETE /api/v1/collections/{collectionId}", a.requireAuthentication(a.deleteCollection))
	mux.Handle("GET /api/v1/collections/{collectionId}/folders/{folderId}/items", a.requireAuthentication(a.resolveCollectionFolder))
	mux.Handle("POST /api/v1/titles/resolve", a.requireAuthentication(a.resolveTitle))
	mux.Handle("POST /api/v1/titles/custom-series/resolve", a.requireAuthentication(a.resolveCustomSeries))
	mux.Handle("POST /api/v1/playback/sources", a.requireAuthentication(a.playbackSources))
	mux.Handle("GET /api/v1/playback/markers", a.requireAuthentication(a.playbackMarkers))
	mux.Handle("POST /api/v1/playback/prepare", a.requireAuthentication(a.preparePlayback))
	mux.Handle("POST /api/v1/playback/resolve", a.requireAuthentication(a.resolvePlayback))
	mux.Handle("DELETE /api/v1/playback/sessions/{sessionId}", a.requireAuthentication(a.stopPlayback))
	mux.Handle("GET /api/v1/playback/activity", a.requireAuthentication(a.playbackActivity))
	mux.Handle("DELETE /api/v1/playback/activity/sessions/{sessionId}", a.requireAuthentication(a.stopPlaybackActivitySession))
	mux.Handle("POST /api/v1/playback/activity/purge", a.requireAuthentication(a.purgePlaybackActivity))
	mux.HandleFunc("GET /api/v1/playback/sessions/{sessionId}/assets/{assetId}", a.playbackAsset)
	mux.HandleFunc("HEAD /api/v1/playback/sessions/{sessionId}/assets/{assetId}", a.playbackAsset)
	mux.Handle("GET /api/v1/calendar", a.requireAuthentication(a.calendarEvents))
	mux.Handle("GET /api/v1/profiles/{profileId}/calendar-link", a.requireAuthentication(a.calendarLinkStatus))
	mux.Handle("POST /api/v1/profiles/{profileId}/calendar-link", a.requireAuthentication(a.createCalendarLink))
	mux.Handle("POST /api/v1/profiles/{profileId}/calendar-link/rotate", a.requireAuthentication(a.rotateCalendarLink))
	mux.Handle("DELETE /api/v1/profiles/{profileId}/calendar-link", a.requireAuthentication(a.revokeCalendarLink))
	mux.HandleFunc("GET /api/v1/calendar.ics", a.calendarFeed)
	mux.HandleFunc("HEAD /api/v1/calendar.ics", a.calendarFeed)
	mux.Handle("GET /api/v1/library", a.requireAuthentication(a.library))
	mux.Handle("POST /api/v1/library/membership", a.requireAuthentication(a.tvLibraryMembership))
	mux.Handle("PUT /api/v1/library/{titleId}", a.requireAuthentication(a.addLibrary))
	mux.Handle("DELETE /api/v1/library/{titleId}", a.requireAuthentication(a.removeLibrary))
	mux.Handle("POST /api/v1/progress/batch", a.requireAuthentication(a.getProgressBatch))
	mux.Handle("GET /api/v1/progress/{titleId}", a.requireAuthentication(a.getProgress))
	mux.Handle("PUT /api/v1/progress/{titleId}", a.requireAuthentication(a.updateProgress))
	mux.Handle("DELETE /api/v1/progress/{titleId}", a.requireAuthentication(a.clearProgress))
	mux.Handle("POST /api/v1/titles/{titleId}/watched", a.requireAuthentication(a.markWatched))
	mux.Handle("DELETE /api/v1/titles/{titleId}/watched", a.requireAuthentication(a.markUnwatched))
	mux.Handle("PUT /api/v1/titles/watched/batch", a.requireAuthentication(a.setWatchedBatch))
	mux.Handle("GET /api/v1/continue-watching", a.requireAuthentication(a.continueWatching))
	mux.Handle("DELETE /api/v1/continue-watching/{titleId}", a.requireAuthentication(a.dismissContinue))
	mux.HandleFunc("GET /", webui.Handler)
	nativeHandler := http.Handler(mux)
	if a.demo != nil {
		nativeHandler = a.demo.Handler(nativeHandler)
	}
	routed := a.routeJellyfinCompatibility(a.middleware(nativeHandler))
	if a.runtimeSettings == nil {
		return routed
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := runtimesettings.Pin(r.Context(), a.runtimeSettings.source)
		routed.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *API) liveness(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": a.version,
	})
}

func (a *API) readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := a.pool.Ping(ctx); err != nil {
		a.logger.Error("database readiness check failed", "error", err)
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
	interfaceLanguage := settings.DefaultInterfaceLanguage
	timezone := settings.DefaultTimezone
	if a.runtimeSettings != nil {
		timezone = a.runtimeSettings.source.Load().Timezone
	}
	if !info.SetupRequired {
		instanceSettings, err := a.settings.Instance(r.Context())
		if err != nil {
			a.internalError(w, "read instance discovery settings", err)
			return
		}
		if instanceSettings.Values.InterfaceLanguage != nil {
			interfaceLanguage = *instanceSettings.Values.InterfaceLanguage
		}
		if a.runtimeSettings == nil && instanceSettings.Values.Timezone != nil {
			timezone = *instanceSettings.Values.Timezone
		}
	}

	apiBaseURL := "/api/v1"
	if a.config.PublicURL != "" {
		apiBaseURL = a.config.PublicURL + apiBaseURL
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":              info.Name,
		"serverVersion":     a.version,
		"protocolVersion":   protocolVersion,
		"apiBaseUrl":        apiBaseURL,
		"capabilities":      nativeCapabilities,
		"setupRequired":     info.SetupRequired,
		"setupCompleted":    !info.SetupRequired,
		"demoAvailable":     info.SetupRequired,
		"timezone":          timezone,
		"interfaceLanguage": interfaceLanguage,
	})
}

func (a *API) setupStatus(w http.ResponseWriter, r *http.Request) {
	info, err := a.instances.Info(r.Context())
	if err != nil {
		a.internalError(w, "read setup state", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{
		"setupRequired":  info.SetupRequired,
		"setupCompleted": !info.SetupRequired,
		"demoAvailable":  info.SetupRequired,
	})
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
	release, retryAfter, admitted := a.credentialAdmission.acquire(requestClientIP(r, a.config.TrustedProxies))
	if !admitted {
		writeAdmissionDenied(w, retryAfter)
		return
	}
	defer release()

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
		a.requestJellyfinCompatibilityActivation()
		if a.demo != nil {
			a.demo.Disable()
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"instance": map[string]string{"id": result.InstanceID},
			"admin":    map[string]string{"id": result.UserID},
			"profile":  map[string]string{"id": result.ProfileID},
		})
	}
}

func (a *API) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		suppliedRequestID := ""
		if values := r.Header.Values(requestwork.RequestIDHeader); len(values) == 1 {
			suppliedRequestID = values[0]
		}
		requestContext, requestID := requestwork.WithRequestID(r.Context(), suppliedRequestID)
		requestContext = auth.WithClientIP(requestContext, requestClientIP(r, a.config.TrustedProxies))
		if a.runtimeSettings != nil {
			requestContext = runtimesettings.Pin(requestContext, a.runtimeSettings.source)
		}
		requestContext, counters := requestwork.WithCounters(requestContext)
		r = r.WithContext(requestContext)
		started := time.Now()
		observed := &nativeObservedResponseWriter{ResponseWriter: w}
		observed.Header().Set(requestwork.RequestIDHeader, requestID)
		observed.Header().Set("Cache-Control", "no-store")
		observed.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		observed.Header().Set("Referrer-Policy", "no-referrer")
		observed.Header().Set("X-Content-Type-Options", "nosniff")

		defer func() {
			if recovered := recover(); recovered != nil {
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				a.logger.Error("panic serving request", "request_id", requestID, "method", r.Method, "route", nativeRequestRoute(r), "committed", observed.Committed())
				if !observed.Committed() {
					writeError(observed, http.StatusInternalServerError, "internal_error", "An internal error occurred")
				}
			}
			status := observed.status
			if status == 0 {
				status = http.StatusOK
			}
			work := counters.Snapshot()
			a.logger.LogAttrs(context.Background(), slog.LevelInfo, "request completed",
				slog.String("request_id", requestID),
				slog.String("route", nativeRequestRoute(r)),
				slog.String("method", r.Method),
				slog.Int("status", status),
				slog.Duration("duration", time.Since(started)),
				slog.Int64("db_call_count", work.DBCalls),
				slog.Duration("db_duration", work.DBDuration),
				slog.Int64("outbound_call_count", work.OutboundCalls),
				slog.Duration("outbound_duration", work.OutboundDuration),
				slog.Int64("upstream_bytes", work.UpstreamBytes),
				slog.Int64("bytes", observed.bytes),
			)
		}()
		next.ServeHTTP(observed, r)
	})
}

func nativeRequestRoute(r *http.Request) string {
	if r == nil || strings.TrimSpace(r.Pattern) == "" {
		return "unmatched"
	}
	return r.Pattern
}

type nativeObservedResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (response *nativeObservedResponseWriter) WriteHeader(status int) {
	if response.status != 0 {
		return
	}
	response.status = status
	response.ResponseWriter.WriteHeader(status)
}

func (response *nativeObservedResponseWriter) Write(payload []byte) (int, error) {
	if response.status == 0 {
		response.WriteHeader(http.StatusOK)
	}
	written, err := response.ResponseWriter.Write(payload)
	response.bytes += int64(written)
	return written, err
}

func (response *nativeObservedResponseWriter) ReadFrom(source io.Reader) (int64, error) {
	if response.status == 0 {
		response.WriteHeader(http.StatusOK)
	}
	var written int64
	var err error
	if readerFrom, ok := response.ResponseWriter.(io.ReaderFrom); ok {
		written, err = readerFrom.ReadFrom(source)
	} else {
		written, err = io.Copy(response.ResponseWriter, source)
	}
	response.bytes += written
	return written, err
}

func (response *nativeObservedResponseWriter) Committed() bool {
	return response.status != 0
}

func (response *nativeObservedResponseWriter) Unwrap() http.ResponseWriter {
	return response.ResponseWriter
}

func (response *nativeObservedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(response.ResponseWriter).Hijack()
}

func (response *nativeObservedResponseWriter) Flush() {
	if response.status == 0 {
		response.status = http.StatusOK
	}
	_ = http.NewResponseController(response.ResponseWriter).Flush()
}

func responseCommitted(w http.ResponseWriter) bool {
	committed, ok := w.(interface{ Committed() bool })
	return ok && committed.Committed()
}

func (a *API) internalError(w http.ResponseWriter, operation string, err error) {
	a.logger.Error(operation, "error", netguard.SanitizeURLError(err))
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

const defaultJSONMaximumBytes int64 = 64 * 1024

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	return decodeJSONLimit(w, r, destination, defaultJSONMaximumBytes)
}

func decodeJSONLimit(w http.ResponseWriter, r *http.Request, destination any, maximumBytes int64) error {
	body, err := readJSONBody(w, r, maximumBytes)
	if err != nil {
		return err
	}
	return decodeJSONBody(body, destination)
}

func decodeAssignmentJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	return decodeAssignmentJSONLimit(w, r, destination, defaultJSONMaximumBytes)
}

func decodeAssignmentJSONLimit(w http.ResponseWriter, r *http.Request, destination any, maximumBytes int64) error {
	body, err := readJSONBody(w, r, maximumBytes)
	if err != nil {
		return err
	}
	if err := decodeJSONBody(body, destination); err != nil {
		return err
	}
	return rejectNullAssignmentMembers(body)
}

func decodeJSONBody(body []byte, destination any) error {
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

func rejectNullAssignmentMembers(value json.RawMessage) error {
	value = bytes.TrimSpace(value)
	if len(value) == 0 {
		return nil
	}
	switch value[0] {
	case '{':
		var members map[string]json.RawMessage
		if err := json.Unmarshal(value, &members); err != nil {
			return fmt.Errorf("invalid JSON body: %w", err)
		}
		for name, member := range members {
			if (name == "profileIds" || name == "categoryIds") && bytes.Equal(bytes.TrimSpace(member), []byte("null")) {
				return fmt.Errorf("invalid JSON body: %s must not be null", name)
			}
			if err := rejectNullAssignmentMembers(member); err != nil {
				return err
			}
		}
	case '[':
		var elements []json.RawMessage
		if err := json.Unmarshal(value, &elements); err != nil {
			return fmt.Errorf("invalid JSON body: %w", err)
		}
		for _, element := range elements {
			if err := rejectNullAssignmentMembers(element); err != nil {
				return err
			}
		}
	}
	return nil
}

func readJSONBody(w http.ResponseWriter, r *http.Request, maximumBytes int64) ([]byte, error) {
	limited := http.MaxBytesReader(w, r.Body, maximumBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	return body, nil
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
