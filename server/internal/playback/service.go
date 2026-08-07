package playback

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/netguard"
)

type ResourceFetcher interface {
	FetchPlaybackResource(context.Context, auth.Principal, string, addon.ResourcePath) (addon.ResourceResult, error)
	FetchAllPlaybackResources(context.Context, auth.Principal, addon.ResourcePath) (addon.ResourceBatch, error)
	ValidatePlaybackAccess(context.Context, auth.Principal, string) error
	ValidatePlaybackAccesses(context.Context, auth.Principal, []string) error
	ValidatePlaybackAccessesTx(context.Context, pgx.Tx, auth.Principal, []string) error
}
type playbackProfileTransaction interface {
	pgx.Tx
}

type Service struct {
	pool                    *pgxpool.Pool
	addons                  ResourceFetcher
	client                  *http.Client
	introDBClient           *http.Client
	introDBBaseURL          string
	processor               MediaProcessor
	now                     func() time.Time
	mediaOptions            MediaOptions
	references              *sourceReferenceStore
	probes                  *mediaProbeCache
	preparations            *playbackPreparationCache
	targetSigningKey        [32]byte
	hlsMu                   sync.Mutex
	introDBCacheStores      atomic.Uint64
	deliveryChildrenMu      sync.Mutex
	deliveryChildren        *deliveryChildBudget
	profileTxFactory        func(context.Context, auth.Principal) (playbackProfileTransaction, error)
	sessionCleanupTxFactory func(context.Context) (playbackProfileTransaction, error)
	hlsJobs                 map[string]*hlsJob
}

const (
	maximumAggregateProviderStreams   = 512
	maximumAggregateProviderSubtitles = 1024
)

type fetchedResources struct {
	batch addon.ResourceBatch
	err   error
}

func NewService(pool *pgxpool.Pool, addons ResourceFetcher, processor MediaProcessor, options MediaOptions) (*Service, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = netguard.DialContextPublic
	transport.MaxResponseHeaderBytes = 64 << 10
	transport.ResponseHeaderTimeout = 15 * time.Second
	transport.MaxIdleConnsPerHost = 8
	options = normalizeMediaOptions(options)
	if err := os.RemoveAll(options.TempDirectory); err != nil {
		return nil, fmt.Errorf("clear media workspace: %w", err)
	}
	if err := os.MkdirAll(options.TempDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create media workspace: %w", err)
	}
	var targetSigningKey [32]byte
	if _, err := rand.Read(targetSigningKey[:]); err != nil {
		return nil, fmt.Errorf("create HLS target signing key: %w", err)
	}
	now := func() time.Time { return time.Now().UTC() }
	return &Service{
		pool: pool, addons: addons, client: &http.Client{Transport: transport, CheckRedirect: playbackRedirectPolicy}, processor: processor,
		introDBClient: &http.Client{Transport: transport.Clone(), Timeout: 8 * time.Second}, introDBBaseURL: introDBDefaultBaseURL,
		now: now, mediaOptions: options, hlsJobs: make(map[string]*hlsJob), targetSigningKey: targetSigningKey,
		deliveryChildren: newDeliveryChildBudget(maximumDeliveryChildrenGlobal, maximumDeliveryChildrenPerUser),
		references:       newSourceReferenceStore(now), probes: newMediaProbeCache(now), preparations: newPlaybackPreparationCache(now),
	}, nil
}

func playbackRedirectPolicy(request *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("too many playback redirects")
	}
	if request.URL.Scheme != "http" && request.URL.Scheme != "https" {
		return errors.New("unsupported playback redirect scheme")
	}
	previous := via[len(via)-1]
	if previous.URL.Scheme == "https" && request.URL.Scheme != "https" {
		return errors.New("playback HTTPS redirect downgrade refused")
	}
	if !strings.EqualFold(previous.URL.Host, request.URL.Host) {
		request.Header = make(http.Header)
		request.Header.Set("User-Agent", "Rivune-Playback/1")
	}
	return nil
}

func (service *Service) Sources(ctx context.Context, principal auth.Principal, input SourcesInput) (SourceList, error) {
	return service.sources(ctx, principal, input, false)
}

// SourcesAndPin creates and retains the returned source capabilities in one
// store transaction for adapters that register them after this call returns.
func (service *Service) SourcesAndPin(ctx context.Context, principal auth.Principal, input SourcesInput) (SourceList, error) {
	return service.sources(ctx, principal, input, true)
}

func (service *Service) sources(ctx context.Context, principal auth.Principal, input SourcesInput, pin bool) (SourceList, error) {
	input.MediaType = strings.TrimSpace(input.MediaType)
	input.AddonID = strings.TrimSpace(input.AddonID)
	input.ResourceID = strings.TrimSpace(input.ResourceID)
	input.PreferredAudioLanguage = strings.TrimSpace(input.PreferredAudioLanguage)
	input.PreferredSubtitleLanguage = strings.TrimSpace(input.PreferredSubtitleLanguage)
	input.PreferredForcedSubtitleLanguage = strings.TrimSpace(input.PreferredForcedSubtitleLanguage)
	if err := service.authorizeActiveProfile(ctx, principal); err != nil {
		return SourceList{}, err
	}
	if err := validateSourcesInput(input); err != nil {
		return SourceList{}, err
	}
	addonMediaType := input.MediaType
	if addonMediaType == "episode" {
		addonMediaType = "series"
	}
	resourcePath := addon.ResourcePath{Resource: "stream", Type: addonMediaType, ID: input.ResourceID}
	var batch addon.ResourceBatch
	var err error
	if input.AddonID != "" {
		var result addon.ResourceResult
		result, err = service.addons.FetchPlaybackResource(ctx, principal, input.AddonID, resourcePath)
		if err == nil {
			batch.Results = []addon.ResourceResult{result}
		}
	} else {
		batch, err = service.addons.FetchAllPlaybackResources(ctx, principal, resourcePath)
	}
	if err != nil {
		return SourceList{}, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	sources, assets, err := normalizeStreams(batch, input.Capabilities)
	if err != nil {
		return SourceList{}, ErrProviderUnavailable
	}
	if len(sources) == 0 && len(batch.Errors) > 0 {
		return SourceList{}, ErrProviderUnavailable
	}
	providerErrors := providerFailures(batch.Errors)
	tx, err := service.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return SourceList{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	references := make([]sourceReference, 0, len(sources))
	for index := range sources {
		source := sources[index]
		var asset *storedAsset
		if assetIndex := storedAssetIndex(assets, source.ID); assetIndex >= 0 {
			value := cloneStoredAsset(assets[assetIndex])
			asset = &value
		}
		references = append(references, sourceReference{
			MediaType: input.MediaType, AddonMediaType: addonMediaType, ResourceID: input.ResourceID,
			Source: source, Asset: asset, Capabilities: input.Capabilities,
			PreferredAudioLanguage: input.PreferredAudioLanguage, PreferredSubtitleLanguage: input.PreferredSubtitleLanguage,
			PreferredForcedSubtitleLanguage: input.PreferredForcedSubtitleLanguage,
			ProviderErrors:                  providerErrors,
		})
	}
	var storedReferences []sourceReference
	if pin {
		storedReferences, err = service.references.putAllPinned(principal, references)
	} else {
		storedReferences, err = service.references.putAll(principal, references)
	}
	if err != nil {
		return SourceList{}, fmt.Errorf("create source references: %w", err)
	}
	var pinnedIdentifiers []string
	if pin {
		pinnedIdentifiers = make([]string, 0, len(storedReferences))
	}
	options := make([]SourceOption, 0, len(storedReferences))
	for index := range storedReferences {
		reference := storedReferences[index]
		source := reference.Source
		options = append(options, SourceOption{
			ID: source.ID, SourceRef: reference.ID, AddonID: source.AddonID, ManifestID: source.ManifestID, AddonName: source.AddonName,
			StreamIndex: source.StreamIndex, Name: sourceOptionLabel(index + 1), Protocol: source.Protocol, Container: source.Container, ExpiresAt: reference.ExpiresAt,
			StableIdentity: stableSourceIdentity(source),
		})
		if pin {
			pinnedIdentifiers = append(pinnedIdentifiers, reference.ID)
		}
	}
	result := SourceList{Sources: options, ProviderErrors: providerErrors}
	if err := tx.Commit(ctx); err != nil {
		if pin {
			service.references.unpin(principal, pinnedIdentifiers)
		}
		return SourceList{}, fmt.Errorf("commit active playback profile authorization: %w", err)
	}
	return result, nil
}

// PinSourceReferences retains exact, profile-bound source capabilities while
// an adapter-owned play session can still open them.
func (service *Service) PinSourceReferences(principal auth.Principal, identifiers []string) error {
	if service == nil || service.references == nil {
		return ErrSourceReferenceExpired
	}
	return service.references.pin(principal, identifiers)
}

// UnpinSourceReferences releases a prior PinSourceReferences call.
func (service *Service) UnpinSourceReferences(principal auth.Principal, identifiers []string) {
	if service == nil || service.references == nil {
		return
	}
	service.references.unpin(principal, identifiers)
}

func (service *Service) Prepare(ctx context.Context, principal auth.Principal, input PrepareInput) (Preparation, error) {
	input.SourceRef = strings.TrimSpace(input.SourceRef)
	tx, err := service.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return Preparation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if len(input.SourceRef) < 16 || len(input.SourceRef) > 128 || !validPlaybackStart(input.StartSeconds) {
		return Preparation{}, ErrInvalidInput
	}
	reference, err := service.references.get(input.SourceRef, principal)
	if err != nil {
		return Preparation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Preparation{}, fmt.Errorf("commit active playback profile authorization: %w", err)
	}
	if strings.TrimSpace(reference.Source.AddonID) != "" {
		if err := service.validateSourceReferenceAccess(ctx, principal, reference); err != nil {
			return Preparation{}, err
		}
	}
	maximumHeight := effectivePlaybackMaximumHeight(reference.Capabilities.MaximumHeight, input.MaximumHeight)
	policy := playbackPolicy{allowTranscoding: input.AllowTranscoding, maximumHeight: maximumHeight}
	prepared, err := service.preparedPlayback(ctx, principal, reference, policy)
	if err != nil {
		if err == ErrTranscodingDisabled {
			service.stopHLSSession(prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID))
		}
		return Preparation{}, err
	}
	sources := []Source{cloneSource(prepared.source)}
	assets := make([]storedAsset, 0, 1)
	if prepared.asset != nil {
		assets = append(assets, cloneStoredAsset(*prepared.asset))
		assets[len(assets)-1].StartSeconds = input.StartSeconds
	}
	capabilities := service.playbackCapabilities(reference.Capabilities, maximumHeight)
	preferences := ResolveInput{
		Capabilities: capabilities, AllowTranscoding: input.AllowTranscoding, MaximumHeight: input.MaximumHeight,
		PreferredAudioLanguage:          reference.PreferredAudioLanguage,
		PreferredSubtitleLanguage:       reference.PreferredSubtitleLanguage,
		PreferredForcedSubtitleLanguage: reference.PreferredForcedSubtitleLanguage,
	}
	if err := applyPlaybackPreferences(sources, assets, preferences); err != nil {
		if err == ErrTranscodingDisabled {
			service.stopHLSSession(prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID))
		}
		return Preparation{}, err
	}
	source := sources[0]
	if assetIndex := storedAssetIndex(assets, source.ID); assetIndex >= 0 {
		if err := service.prewarmHLS(ctx, prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID), source, &assets[assetIndex]); err != nil {
			return Preparation{}, err
		}
	}
	result := Preparation{
		SourceRef: reference.ID, Mode: source.Mode, Protocol: source.Protocol, Container: source.Container,
		Media: source.Media, Decision: clonePlaybackDecision(source.Decision),
		SubtitleCount: len(prepared.subtitles), ExpiresAt: reference.ExpiresAt,
	}
	if err := service.commitAuthorizedProfileBoundary(ctx, principal); err != nil {
		service.stopHLSSession(prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID))
		return Preparation{}, err
	}
	if err := service.validatePreparedPlaybackAccess(ctx, principal, prepared.addonIDs); err != nil {
		service.preparations.evict(reference.ID, policy)
		service.stopHLSSession(prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID))
		return Preparation{}, err
	}
	return result, nil
}

func (service *Service) Resolve(ctx context.Context, principal auth.Principal, input ResolveInput) (Session, error) {
	return service.resolve(ctx, principal, input, nil)
}

func (service *Service) resolve(ctx context.Context, principal auth.Principal, input ResolveInput, deliveryHandle *DeliveryHandle) (Session, error) {
	input.SourceRef = strings.TrimSpace(input.SourceRef)
	input.TitleID = strings.TrimSpace(input.TitleID)
	input.PreferredSubtitleID = strings.TrimSpace(input.PreferredSubtitleID)
	tx, err := service.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := validateResolveInput(input); err != nil {
		return Session{}, err
	}
	reference, err := service.references.get(input.SourceRef, principal)
	if err != nil {
		return Session{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("commit active playback profile authorization: %w", err)
	}
	if strings.TrimSpace(reference.Source.AddonID) != "" {
		if err := service.validateSourceReferenceAccess(ctx, principal, reference); err != nil {
			return Session{}, err
		}
	}
	maximumHeight := effectivePlaybackMaximumHeight(reference.Capabilities.MaximumHeight, input.MaximumHeight)
	policy := playbackPolicy{allowTranscoding: input.AllowTranscoding, maximumHeight: maximumHeight}
	prepared, err := service.preparedPlayback(ctx, principal, reference, policy)
	if err != nil {
		if err == ErrTranscodingDisabled {
			service.stopHLSSession(prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID))
		}
		return Session{}, err
	}
	sources := []Source{cloneSource(prepared.source)}
	streamAssets := make([]storedAsset, 0, 1)
	if prepared.asset != nil {
		streamAssets = append(streamAssets, cloneStoredAsset(*prepared.asset))
		streamAssets[len(streamAssets)-1].StartSeconds = input.StartSeconds
	}
	input.Capabilities = service.playbackCapabilities(reference.Capabilities, maximumHeight)
	input.PreferredAudioLanguage = reference.PreferredAudioLanguage
	input.PreferredSubtitleLanguage = reference.PreferredSubtitleLanguage
	input.PreferredForcedSubtitleLanguage = reference.PreferredForcedSubtitleLanguage
	if err := applyPlaybackPreferences(sources, streamAssets, input); err != nil {
		if err == ErrTranscodingDisabled {
			service.stopHLSSession(prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID))
		}
		return Session{}, err
	}
	subtitles := append([]Subtitle(nil), prepared.subtitles...)
	if err := applySubtitlePreference(subtitles, input.PreferredSubtitleID, input.PreferredForcedSubtitleLanguage, input.PreferredSubtitleLanguage); err != nil {
		return Session{}, err
	}
	subtitleAssets := make([]storedAsset, len(prepared.subtitleAssets))
	for index := range prepared.subtitleAssets {
		subtitleAssets[index] = cloneStoredAsset(prepared.subtitleAssets[index])
		subtitleAssets[index].StartSeconds = input.StartSeconds
	}
	if err := applySubtitleDecision(sources, streamAssets, subtitles, subtitleAssets, input.Capabilities, input.AllowTranscoding); err != nil {
		if err == ErrTranscodingDisabled {
			service.stopHLSSession(prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID))
		}
		return Session{}, err
	}
	assets := append(streamAssets, subtitleAssets...)
	session, err := service.createSession(ctx, principal, reference, input.TitleID, sources, subtitles, assets, prepared.addonIDs, prepared.providerErrors, deliveryHandle)
	if err == ErrSourceReferenceExpired {
		service.preparations.evict(reference.ID, policy)
	}
	return session, err
}

func (service *Service) validateSourceReferenceAccess(ctx context.Context, principal auth.Principal, reference sourceReference) error {
	addonID := strings.TrimSpace(reference.Source.AddonID)
	if addonID == "" {
		return ErrSourceReferenceExpired
	}
	if err := service.addons.ValidatePlaybackAccess(ctx, principal, addonID); err != nil {
		if errors.Is(err, addon.ErrNotFound) || errors.Is(err, addon.ErrForbidden) ||
			errors.Is(err, addon.ErrActiveProfileRequired) || errors.Is(err, addon.ErrInvalidInput) {
			return ErrSourceReferenceExpired
		}
		return fmt.Errorf("validate playback source access: %w", err)
	}
	return nil
}

func (service *Service) validatePreparedPlaybackAccess(ctx context.Context, principal auth.Principal, addonIDs []string) error {
	if err := service.addons.ValidatePlaybackAccesses(ctx, principal, addonIDs); err != nil {
		if errors.Is(err, addon.ErrNotFound) || errors.Is(err, addon.ErrForbidden) ||
			errors.Is(err, addon.ErrActiveProfileRequired) || errors.Is(err, addon.ErrInvalidInput) {
			return ErrSourceReferenceExpired
		}
		return fmt.Errorf("validate playback provider access: %w", err)
	}
	return nil
}

func (service *Service) validatePreparedPlaybackAccessTx(ctx context.Context, tx pgx.Tx, principal auth.Principal, addonIDs []string) error {
	if len(addonIDs) == 0 {
		return nil
	}
	if err := service.addons.ValidatePlaybackAccessesTx(ctx, tx, principal, addonIDs); err != nil {
		if errors.Is(err, addon.ErrNotFound) || errors.Is(err, addon.ErrForbidden) ||
			errors.Is(err, addon.ErrActiveProfileRequired) || errors.Is(err, addon.ErrInvalidInput) {
			return ErrSourceReferenceExpired
		}
		return fmt.Errorf("validate playback provider access: %w", err)
	}
	return nil
}

func (service *Service) createSession(ctx context.Context, principal auth.Principal, reference sourceReference, titleID string, sources []Source, subtitles []Subtitle, assets []storedAsset, addonIDs []string, providerErrors []ProviderFailure, deliveryHandle *DeliveryHandle) (Session, error) {
	token, tokenHash, err := newSessionToken()
	if err != nil {
		return Session{}, fmt.Errorf("create playback token: %w", err)
	}
	assetsJSON, err := json.Marshal(assets)
	if err != nil {
		return Session{}, fmt.Errorf("encode playback assets: %w", err)
	}
	now := service.now()
	expiresAt := now.Add(sessionTTL)
	inactiveSessionIDs := make([]string, 0)
	tx, err := service.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := service.validatePreparedPlaybackAccessTx(ctx, tx, principal, addonIDs); err != nil {
		return Session{}, err
	}
	rows, err := tx.Query(ctx, `
		DELETE FROM playback_sessions
		WHERE profile_id = $1::uuid
		  AND (expires_at <= now() OR last_seen_at <= now() - $2::interval)
		RETURNING id::text
	`, *principal.ActiveProfileID, intervalLiteral(playbackSessionIdleTTL))
	if err != nil {
		return Session{}, fmt.Errorf("clean inactive playback sessions: %w", err)
	}
	for rows.Next() {
		var identifier string
		if err := rows.Scan(&identifier); err != nil {
			rows.Close()
			return Session{}, fmt.Errorf("scan inactive playback session: %w", err)
		}
		inactiveSessionIDs = append(inactiveSessionIDs, identifier)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Session{}, fmt.Errorf("iterate inactive playback sessions: %w", err)
	}
	rows.Close()
	var sessionID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO playback_sessions (
			auth_session_id, profile_id, title_id, media_type, resource_id, token_hash, assets, expires_at
		) VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8)
		RETURNING id::text
	`, principal.SessionID, *principal.ActiveProfileID, titleID, reference.MediaType, reference.ResourceID, tokenHash, assetsJSON, expiresAt).Scan(&sessionID); err != nil {
		return Session{}, fmt.Errorf("store playback session: %w", err)
	}
	for index := range sources {
		sources[index].URL = sessionSourceURL(sources[index], assets, sessionID, token)
	}
	for index := range subtitles {
		if subtitles[index].Delivery != "burn" {
			subtitles[index].URL = assetURL(sessionID, subtitles[index].ID, token, "", "")
		}
	}
	result := Session{
		ID: sessionID, SelectedSourceID: firstCompatibleSource(sources), SelectedAudioTrack: selectedAudioTrack(sources, assets),
		SelectedSubtitleID: selectedSubtitle(subtitles), Sources: sources, Subtitles: subtitles,
		ProviderErrors: providerErrors, ExpiresAt: expiresAt,
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("commit playback session: %w", err)
	}
	for _, identifier := range inactiveSessionIDs {
		service.stopHLSSession(identifier)
	}
	if err := service.startSessionHLS(ctx, prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID), sessionID, sources, assets); err != nil {
		_ = service.deleteCreatedSession(ctx, principal.SessionID, sessionID)
		return Session{}, err
	}
	if err := service.commitAuthorizedProfileBoundary(ctx, principal); err != nil {
		service.stopHLSSession(sessionID)
		_ = service.deleteCreatedSession(ctx, principal.SessionID, sessionID)
		return Session{}, err
	}
	if deliveryHandle != nil {
		*deliveryHandle = deliveryHandleForSession(sessionID, token, sources, assets)
		if deliveryHandle.children != nil {
			deliveryHandle.children.bindBudget(service.deliveryChildBudget(), principal.UserID, service.now)
		}
	}
	return result, nil
}

func sessionSourceURL(source Source, assets []storedAsset, sessionID, token string) string {
	if source.URL == "" || source.Mode == "external" {
		return source.URL
	}
	if source.Protocol == "hls" && (source.Mode == processingRemux || source.Mode == processingTranscodeAudio || source.Mode == processingTranscode) {
		startSeconds := float64(0)
		if assetIndex := storedAssetIndex(assets, source.ID); assetIndex >= 0 {
			startSeconds = assets[assetIndex].StartSeconds
		}
		return hlsAssetURLAt(sessionID, source.ID, token, "index.m3u8", startSeconds)
	}
	return assetURL(sessionID, source.ID, token, "", "")
}

func (service *Service) Stop(ctx context.Context, principal auth.Principal, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || principal.ActiveProfileID == nil {
		return ErrSessionNotFound
	}
	tx, err := service.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		if errors.Is(err, ErrActiveProfileRequired) {
			return ErrSessionNotFound
		}
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
		DELETE FROM playback_sessions
		WHERE id::text = $1 AND auth_session_id = $2 AND profile_id = $3
	`, sessionID, principal.SessionID, *principal.ActiveProfileID)
	if err != nil {
		return fmt.Errorf("delete playback session: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrSessionNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit playback session deletion: %w", err)
	}
	service.stopHLSSession(sessionID)
	return nil
}

func validateSourcesInput(input SourcesInput) error {
	if len(input.MediaType) < 1 || len(input.MediaType) > 64 || len(input.ResourceID) < 1 || len(input.ResourceID) > 2048 || len(input.AddonID) > 128 {
		return ErrInvalidInput
	}
	if len(input.PreferredAudioLanguage) > 64 || len(input.PreferredSubtitleLanguage) > 64 || len(input.PreferredForcedSubtitleLanguage) > 64 {
		return ErrInvalidInput
	}
	return validateCapabilities(input.Capabilities)
}

func validateResolveInput(input ResolveInput) error {
	if len(input.SourceRef) < 16 || len(input.SourceRef) > 128 || len(input.TitleID) > 128 || len(input.PreferredSubtitleID) > 128 {
		return ErrInvalidInput
	}
	if input.PreferredAudioTrack != nil && (*input.PreferredAudioTrack < 0 || *input.PreferredAudioTrack > 10000) {
		return ErrInvalidInput
	}
	if !validPlaybackStart(input.StartSeconds) {
		return ErrInvalidInput
	}
	return nil
}

func validPlaybackStart(seconds float64) bool {
	return seconds >= 0 && seconds <= maximumPlaybackStartSeconds && seconds == float64(int64(seconds))
}

func validateCapabilities(capabilities Capabilities) error {
	groups := [][]string{
		capabilities.StreamingProtocols, capabilities.Containers, capabilities.VideoCodecs,
		capabilities.AudioCodecs, capabilities.HDRFormats, capabilities.ExternalPlayers,
		capabilities.ProcessingModes, capabilities.SubtitleModes,
	}
	for _, values := range groups {
		if len(values) > 32 {
			return ErrInvalidInput
		}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if len(value) < 1 || len(value) > 64 {
				return ErrInvalidInput
			}
		}
	}
	if len(capabilities.MediaProfiles) > 32 {
		return ErrInvalidInput
	}
	for _, profile := range capabilities.MediaProfiles {
		values := []string{profile.Container, profile.VideoCodec}
		if profile.AudioCodec != "" {
			values = append(values, profile.AudioCodec)
		}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if len(value) < 1 || len(value) > 64 {
				return ErrInvalidInput
			}
		}
	}
	if !validUniqueModes(capabilities.ProcessingModes, []string{processingRemux, processingTranscodeAudio, processingTranscode}, 3) ||
		!validUniqueModes(capabilities.SubtitleModes, []string{"external", "burn"}, 2) {
		return ErrInvalidInput
	}
	if capabilities.MaximumVideoBitrateKbps != 0 &&
		(capabilities.MaximumVideoBitrateKbps < 64 || capabilities.MaximumVideoBitrateKbps > 200000) {
		return ErrInvalidInput
	}
	if capabilities.MaximumAudioChannels != 0 &&
		(capabilities.MaximumAudioChannels < 1 || capabilities.MaximumAudioChannels > 32) {
		return ErrInvalidInput
	}
	if capabilities.MaximumHeight != 0 &&
		(capabilities.MaximumHeight < 144 || capabilities.MaximumHeight > 4320) {
		return ErrInvalidInput
	}
	return nil
}

func validUniqueModes(values, allowed []string, maximum int) bool {
	if len(values) > maximum {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
		valid := false
		for _, candidate := range allowed {
			if value == candidate {
				valid = true
				break
			}
		}
		if !valid {
			return false
		}
	}
	return true
}
func (service *Service) beginAuthorizedProfileTx(ctx context.Context, principal auth.Principal) (playbackProfileTransaction, error) {
	if principal.ActiveProfileID == nil || principal.ProfileGrantExpiresAt == nil || !principal.ProfileGrantExpiresAt.After(service.now()) {
		return nil, ErrActiveProfileRequired
	}
	if service.profileTxFactory != nil {
		return service.profileTxFactory(ctx, principal)
	}
	if service.pool == nil {
		return nil, ErrActiveProfileRequired
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin active playback profile authorization: %w", err)
	}
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{*principal.ActiveProfileID}, false)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("authorize active playback profile: %w", err)
	}
	if !authorized {
		_ = tx.Rollback(ctx)
		return nil, ErrActiveProfileRequired
	}
	return tx, nil
}

func (service *Service) authorizeActiveProfile(ctx context.Context, principal auth.Principal) error {
	return service.commitAuthorizedProfileBoundary(ctx, principal)
}

func (service *Service) commitAuthorizedProfileBoundary(ctx context.Context, principal auth.Principal) error {
	tx, err := service.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit active playback profile authorization: %w", err)
	}
	return nil
}

// closeDeliverySession consumes the opaque server-side handle without
// re-authorizing the linked login. Compatibility logout revokes that login
// before asynchronous delivery cleanup, so the handle token and its original
// session/profile owner form the cleanup authority.
func (service *Service) closeDeliverySession(ctx context.Context, principal auth.Principal, handle DeliveryHandle) error {
	if service == nil || principal.SessionID == "" || principal.ActiveProfileID == nil || *principal.ActiveProfileID == "" || !handle.Valid() {
		return ErrSessionNotFound
	}
	var tx playbackProfileTransaction
	var err error
	if service.sessionCleanupTxFactory != nil {
		tx, err = service.sessionCleanupTxFactory(ctx)
	} else {
		if service.pool == nil {
			return errors.New("begin playback delivery cleanup: pool is unavailable")
		}
		tx, err = service.pool.Begin(ctx)
	}
	if err != nil {
		return fmt.Errorf("begin playback delivery cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tokenHash := sha256.Sum256([]byte(handle.token))
	command, err := tx.Exec(ctx, `
		DELETE FROM playback_sessions
		WHERE id::text = $1
		  AND auth_session_id = $2
		  AND profile_id = $3::uuid
		  AND token_hash = $4
	`, handle.sessionID, principal.SessionID, *principal.ActiveProfileID, tokenHash[:])
	if err != nil {
		return fmt.Errorf("delete playback delivery session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit playback delivery cleanup: %w", err)
	}
	service.stopHLSSession(handle.sessionID)
	if command.RowsAffected() == 0 {
		return ErrSessionNotFound
	}
	return nil
}

func (service *Service) deleteCreatedSession(ctx context.Context, authSessionID, sessionID string) error {
	var tx playbackProfileTransaction
	var err error
	if service.sessionCleanupTxFactory != nil {
		tx, err = service.sessionCleanupTxFactory(ctx)
	} else {
		if service.pool == nil {
			return errors.New("begin playback session cleanup: pool is unavailable")
		}
		tx, err = service.pool.Begin(ctx)
	}
	if err != nil {
		return fmt.Errorf("begin playback session cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		DELETE FROM playback_sessions
		WHERE id::text = $1 AND auth_session_id = $2
	`, sessionID, authSessionID); err != nil {
		return fmt.Errorf("delete created playback session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit created playback session deletion: %w", err)
	}
	return nil
}

func effectivePlaybackMaximumHeight(clientMaximum, settingsMaximum int) int {
	if clientMaximum <= 0 {
		return settingsMaximum
	}
	if settingsMaximum <= 0 || clientMaximum < settingsMaximum {
		return clientMaximum
	}
	return settingsMaximum
}
func (service *Service) playbackCapabilities(client Capabilities, maximumHeight int) Capabilities {
	capabilities := cloneCapabilities(client)
	capabilities.MaximumHeight = maximumHeight
	capabilities.TranscodeVideoBitrateKbps = service.mediaOptions.TranscodeVideoBitrateKbps
	return capabilities
}

func sourceOptionLabel(ordinal int) string {
	return fmt.Sprintf("Source %d", ordinal)
}

func stableSourceIdentity(source Source) string {
	addonID := strings.TrimSpace(source.AddonID)
	manifestID := strings.TrimSpace(source.ManifestID)
	if addonID == "" || manifestID == "" {
		return ""
	}
	identity := ""
	switch {
	case strings.TrimSpace(source.YTID) != "":
		identity = "youtube\x00" + strings.TrimSpace(source.YTID)
	case strings.TrimSpace(source.InfoHash) != "":
		identity = "torrent\x00" + strings.ToLower(strings.TrimSpace(source.InfoHash))
		if source.FileIndex != nil {
			identity += fmt.Sprintf("\x00%d", *source.FileIndex)
		}
	case strings.TrimSpace(source.Filename) != "":
		identity = "filename\x00" + strings.TrimSpace(source.Filename)
	case strings.TrimSpace(source.Name) != "" || strings.TrimSpace(source.Title) != "" || strings.TrimSpace(source.Description) != "":
		identity = "metadata\x00" + strings.TrimSpace(source.Name) + "\x00" + strings.TrimSpace(source.Title) + "\x00" + strings.TrimSpace(source.Description)
	default:
		return ""
	}
	digest := sha256.Sum256([]byte(addonID + "\x00" + manifestID + "\x00" + identity))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func normalizeStreams(batch addon.ResourceBatch, capabilities Capabilities) ([]Source, []storedAsset, error) {
	type decodedResponse struct {
		result   addon.ResourceResult
		response addon.ProviderStreamResponse
	}
	if len(batch.Results) > maximumAggregateProviderStreams {
		return nil, nil, fmt.Errorf("%w: stream provider responses exceed %d items", addon.ErrInvalidResponse, maximumAggregateProviderStreams)
	}
	decoded := make([]decodedResponse, 0, len(batch.Results))
	total := 0
	for _, result := range batch.Results {
		response, err := addon.ParseProviderStreamResponse(result.Payload)
		if err != nil {
			return nil, nil, err
		}
		if len(response.Streams) > maximumAggregateProviderStreams-total {
			return nil, nil, fmt.Errorf("%w: aggregate streams exceed %d items", addon.ErrInvalidResponse, maximumAggregateProviderStreams)
		}
		total += len(response.Streams)
		decoded = append(decoded, decodedResponse{result: result, response: response})
	}

	sources := make([]Source, 0, total)
	assets := make([]storedAsset, 0, total)
	for _, decodedResult := range decoded {
		for streamIndex, stream := range decodedResult.response.Streams {
			streamURL := strings.TrimSpace(stream.URL)
			externalURL := strings.TrimSpace(stream.ExternalURL)
			ytID := strings.TrimSpace(stream.YTID)
			infoHash := strings.TrimSpace(stream.InfoHash)
			hints := stream.BehaviorHints
			headers := requestHeaders(hints)
			id := fmt.Sprintf("stream-%d", len(sources)+1)
			source := Source{
				ID: id, AddonID: decodedResult.result.AddonID, ManifestID: decodedResult.result.ManifestID, AddonName: strings.TrimSpace(decodedResult.result.AddonName),
				Name: strings.TrimSpace(stream.Name), Title: strings.TrimSpace(stream.Title), Description: strings.TrimSpace(stream.Description),
				Hint:     strings.TrimSpace(stream.Name + " " + stream.Title + " " + stream.Description + " " + hints.Filename),
				Filename: strings.TrimSpace(hints.Filename), StreamIndex: streamIndex, FileIndex: stream.FileIndex,
			}
			switch {
			case validMediaURL(streamURL):
				source.Mode = "direct"
				source.URL = streamURL
				source.Protocol = protocolFor(streamURL)
				source.Container = containerFor(streamURL)
				source.Compatible = supports(capabilities.StreamingProtocols, source.Protocol) && supportsContainer(capabilities.Containers, source.Container)
				assets = append(assets, storedAsset{ID: id, Kind: "stream", URL: streamURL, Headers: headers, Container: source.Container})
			case validYouTubeID(ytID):
				source.Mode = "youtube"
				source.YTID = ytID
				source.Protocol = "youtube"
				source.Compatible = supports(capabilities.StreamingProtocols, source.Protocol)
			case validMediaURL(externalURL) && len(capabilities.ExternalPlayers) > 0:
				source.Mode = "external"
				source.URL = externalURL
				source.Protocol = protocolFor(externalURL)
				source.Container = containerFor(externalURL)
				source.Compatible = true
				assets = append(assets, storedAsset{ID: id, Kind: "stream", URL: externalURL, Headers: headers})
			case infoHash != "" && len(infoHash) <= 128 && len(capabilities.ExternalPlayers) > 0:
				source.Mode = "external"
				source.InfoHash = infoHash
				source.Protocol = "external"
				source.Compatible = true
			default:
				continue
			}
			sources = append(sources, source)
		}
	}
	sortSources(sources)
	return sources, assets, nil
}

func normalizeSubtitles(batch addon.ResourceBatch) ([]Subtitle, []storedAsset, error) {
	type decodedResponse struct {
		result   addon.ResourceResult
		response addon.ProviderSubtitleResponse
	}
	if len(batch.Results) > maximumAggregateProviderSubtitles {
		return nil, nil, fmt.Errorf("%w: subtitle provider responses exceed %d items", addon.ErrInvalidResponse, maximumAggregateProviderSubtitles)
	}
	decoded := make([]decodedResponse, 0, len(batch.Results))
	total := 0
	for _, result := range batch.Results {
		response, err := addon.ParseProviderSubtitleResponse(result.Payload)
		if err != nil {
			return nil, nil, err
		}
		if len(response.Subtitles) > maximumAggregateProviderSubtitles-total {
			return nil, nil, fmt.Errorf("%w: aggregate subtitles exceed %d items", addon.ErrInvalidResponse, maximumAggregateProviderSubtitles)
		}
		total += len(response.Subtitles)
		decoded = append(decoded, decodedResponse{result: result, response: response})
	}

	subtitles := make([]Subtitle, 0, total)
	assets := make([]storedAsset, 0, total)
	for _, decodedResult := range decoded {
		for _, subtitle := range decodedResult.response.Subtitles {
			if !validMediaURL(subtitle.URL) {
				continue
			}
			id := fmt.Sprintf("subtitle-%d", len(subtitles)+1)
			kind := "subtitle"
			extension := strings.ToLower(pathExtension(subtitle.URL))
			if extension == ".srt" || extension == ".ass" || extension == ".ssa" {
				kind = assetKindConvertedSubtitle
			}
			subtitles = append(subtitles, Subtitle{
				ID: id, AddonID: decodedResult.result.AddonID, ManifestID: decodedResult.result.ManifestID,
				Language: strings.TrimSpace(subtitle.Language), URL: subtitle.URL,
				Delivery: "external", Forced: subtitle.Forced,
			})
			assets = append(assets, storedAsset{ID: id, Kind: kind, URL: subtitle.URL})
		}
	}
	return subtitles, assets, nil
}

func requestHeaders(hints addon.ProviderStreamBehaviorHints) map[string]string {
	if len(hints.ProxyHeaders.Request) == 0 {
		return nil
	}
	headers := make(map[string]string, len(hints.ProxyHeaders.Request))
	for name, value := range hints.ProxyHeaders.Request {
		if value != "" {
			headers[name] = value
		}
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func providerFailures(values []addon.ResourceFailure) []ProviderFailure {
	failures := make([]ProviderFailure, 0, len(values))
	for _, value := range values {
		failures = append(failures, ProviderFailure{
			AddonID: value.AddonID, ManifestID: value.ManifestID, Code: value.Code, Message: value.Message,
		})
	}
	return failures
}

func hasCompatibleSource(sources []Source) bool {
	return firstCompatibleSource(sources) != ""
}

func firstCompatibleSource(sources []Source) string {
	for _, source := range sources {
		if source.Compatible {
			return source.ID
		}
	}
	return ""
}

func modeRank(mode string) int {
	switch mode {
	case "direct":
		return 0
	case "youtube":
		return 1
	case processingRemux:
		return 2
	case processingTranscodeAudio:
		return 3
	case processingTranscode:
		return 4
	default:
		return 5
	}
}

func sortSources(sources []Source) {
	sort.SliceStable(sources, func(left, right int) bool {
		if sources[left].Compatible != sources[right].Compatible {
			return sources[left].Compatible
		}
		return modeRank(sources[left].Mode) < modeRank(sources[right].Mode)
	})
}

func protocolFor(rawURL string) string {
	parsed, _ := url.Parse(rawURL)
	if strings.EqualFold(path.Ext(parsed.Path), ".m3u8") {
		return "hls"
	}
	return "http"
}

func containerFor(rawURL string) string {
	parsed, _ := url.Parse(rawURL)
	extension := strings.ToLower(strings.TrimPrefix(path.Ext(parsed.Path), "."))
	switch extension {
	case "mp4", "m4v", "webm", "mkv", "mov", "ts":
		return extension
	default:
		return ""
	}
}

func supports(values []string, candidate string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), candidate) {
			return true
		}
	}
	return false
}

func supportsContainer(values []string, candidate string) bool {
	return candidate == "" || supports(values, candidate)
}

func validMediaURL(raw string) bool {
	if len(raw) < 1 || len(raw) > 8192 {
		return false
	}
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}

func validYouTubeID(value string) bool {
	if len(value) < 1 || len(value) > 100 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func newSessionToken() (string, []byte, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(value)
	digest := sha256.Sum256([]byte(token))
	return token, digest[:], nil
}
