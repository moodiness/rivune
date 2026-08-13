package playback

import (
	"bytes"
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
	"github.com/moodiness/rivune/server/internal/runtimesettings"
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

type hlsStorageTicker struct {
	ticks <-chan time.Time
	stop  func()
}

type Service struct {
	pool                      *pgxpool.Pool
	addons                    ResourceFetcher
	client                    *http.Client
	directStreams             directStreamAdmission
	directStreamGlobalLimit   int
	directStreamOwnerLimit    int
	directStreamIdleTimeout   time.Duration
	introDBClient             *http.Client
	introDBBaseURL            string
	processor                 MediaProcessor
	now                       func() time.Time
	mediaOptions              MediaOptions
	runtimeSettings           *runtimesettings.Source
	mediaStorageBytes         atomic.Int64
	references                *sourceReferenceStore
	probes                    *mediaProbeCache
	preparations              *playbackPreparationCache
	targetSigningKey          [32]byte
	targetCapabilityKey       [32]byte
	hlsResetMu                sync.RWMutex
	hlsMu                     sync.Mutex
	hlsStorageMu              sync.Mutex
	hlsWorkspaceGeneration    atomic.Uint64
	hlsStorageMonitorRunning  bool
	hlsStorageMonitorWorkers  int
	hlsStorageMonitorInterval time.Duration
	hlsStorageTickerFactory   func(time.Duration) hlsStorageTicker
	hlsStorageMonitorWake     chan struct{}
	hlsWorkspaceSize          func(string) int64
	hlsSeekMu                 sync.Mutex
	hlsSeekGates              map[string]*hlsSeekGate
	introDBCacheStores        atomic.Uint64
	deliveryChildrenMu        sync.Mutex
	deliveryChildren          *deliveryChildBudget
	profileTxFactory          func(context.Context, auth.Principal) (playbackProfileTransaction, error)
	sessionCleanupTxFactory   func(context.Context) (playbackProfileTransaction, error)
	hlsJobs                   map[string]*hlsJob
	trickplayMu               sync.Mutex
	trickplayImages           *trickplayCache
}

const (
	maximumAggregateProviderStreams       = 512
	maximumAggregateProviderSubtitles     = 1024
	maximumPlaybackSessionsPerAuthSession = 8
	maximumPlaybackSessionsPerProfile     = 64
	maximumPlaybackSessionsGlobal         = 256
	// One selected 8 KiB stream URL plus 1,024 8 KiB subtitle URLs remains below
	// this bound even when every URL byte needs JSON escaping, with room for headers and probe metadata.
	maximumPlaybackSessionAssetBytes       = 24 << 20
	playbackSessionAdmissionLockID   int64 = 0x524956504c415953
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
	transport.MaxIdleConns = maximumDirectStreamsGlobal
	transport.MaxIdleConnsPerHost = maximumDirectStreamsPerHost
	transport.MaxConnsPerHost = maximumDirectStreamsPerHost
	transport.IdleConnTimeout = 90 * time.Second
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
	var targetCapabilityKey [32]byte
	if _, err := rand.Read(targetCapabilityKey[:]); err != nil {
		return nil, fmt.Errorf("create HLS target capability key: %w", err)
	}
	now := func() time.Time { return time.Now().UTC() }
	service := &Service{
		pool: pool, addons: addons, client: &http.Client{Transport: transport, CheckRedirect: playbackRedirectPolicy}, processor: processor,
		directStreamGlobalLimit: maximumDirectStreamsGlobal, directStreamOwnerLimit: maximumDirectStreamsPerOwner,
		directStreamIdleTimeout: directStreamReadIdleTimeout,
		introDBClient:           &http.Client{Transport: transport.Clone(), Timeout: 8 * time.Second}, introDBBaseURL: introDBDefaultBaseURL,
		now: now, mediaOptions: options, hlsJobs: make(map[string]*hlsJob), targetSigningKey: targetSigningKey, targetCapabilityKey: targetCapabilityKey,
		deliveryChildren: newDeliveryChildBudget(maximumDeliveryChildrenGlobal, maximumDeliveryChildrenPerProfile),
		references:       newSourceReferenceStore(now), probes: newMediaProbeCache(now), preparations: newPlaybackPreparationCache(now),
	}
	service.mediaStorageBytes.Store(options.MaxStorageBytes)
	return service, nil
}

func NewServiceWithRuntimeSettings(pool *pgxpool.Pool, addons ResourceFetcher, processor MediaProcessor, options MediaOptions, runtimeSettings *runtimesettings.Source) (*Service, error) {
	if runtimeSettings == nil {
		return nil, errors.New("playback runtime settings are required")
	}
	snapshot := runtimeSettings.Load()
	options.MaxStorageBytes = snapshot.MediaMaxStorageBytes
	options.TranscodeVideoBitrateKbps = snapshot.TranscodeMaxBitrateKbps
	service, err := NewService(pool, addons, processor, options)
	if err != nil {
		return nil, err
	}
	service.runtimeSettings = runtimeSettings
	return service, nil
}

func (service *Service) runtimePlaybackDecision(ctx context.Context) (int, bool) {
	if service.runtimeSettings != nil {
		snapshot := runtimesettings.Load(ctx, service.runtimeSettings)
		return snapshot.TranscodeMaxBitrateKbps, snapshot.AllowTranscoding
	}
	return service.mediaOptions.TranscodeVideoBitrateKbps, true
}

func (service *Service) mediaStorageLimit() int64 {
	if limit := service.mediaStorageBytes.Load(); limit > 0 {
		return limit
	}
	if service.mediaOptions.MaxStorageBytes > 0 {
		return service.mediaOptions.MaxStorageBytes
	}
	return defaultMediaStorageBytes
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
	previousOrigin, previousOriginValid := canonicalURLOrigin(previous.URL)
	requestOrigin, requestOriginValid := canonicalURLOrigin(request.URL)
	if !previousOriginValid || !requestOriginValid || previousOrigin != requestOrigin {
		redirectHeaders := make(http.Header)
		for _, name := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
			for _, value := range request.Header.Values(name) {
				redirectHeaders.Add(name, value)
			}
		}
		redirectHeaders.Set("User-Agent", "Rivune-Playback/1")
		request.Header = redirectHeaders
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
	if input.MaximumSources > 0 && len(sources) > input.MaximumSources {
		sources = sources[:input.MaximumSources]
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
		name, description, filename := sourceOptionDisplay(source, index+1)
		options = append(options, SourceOption{
			ID: source.ID, SourceRef: reference.ID, AddonID: source.AddonID, ManifestID: source.ManifestID, AddonName: source.AddonName,
			StreamIndex: source.StreamIndex, Name: name, Description: description, Filename: filename,
			Protocol: source.Protocol, Mode: source.Mode, Container: source.Container, ExpiresAt: reference.ExpiresAt, ReportedHeight: sourceResolutionHint(source), StableIdentity: stableSourceIdentity(source),
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
	bitrate, runtimeAllowsTranscoding := service.runtimePlaybackDecision(ctx)
	allowTranscoding := input.AllowTranscoding && runtimeAllowsTranscoding
	if err != nil {
		return Preparation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if len(input.SourceRef) < 16 || len(input.SourceRef) > 128 || !validPlaybackStart(input.StartSeconds) || !validPlaybackMaximumHeight(input.MaximumHeight) {
		return Preparation{}, ErrInvalidInput
	}
	reference, err := service.references.get(input.SourceRef, principal)
	if err != nil {
		return Preparation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Preparation{}, fmt.Errorf("commit active playback profile authorization: %w", err)
	}
	reference, err = service.refreshSourceReference(ctx, principal, reference)
	if err != nil {
		return Preparation{}, err
	}
	maximumHeight := effectivePlaybackMaximumHeight(reference.Capabilities.MaximumHeight, input.MaximumHeight)
	policy := playbackPolicy{allowTranscoding: allowTranscoding, maximumHeight: maximumHeight, bitrateKbps: bitrate, externalPlayer: input.ExternalPlayer}
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
		assets[len(assets)-1].HLSSegmentContainer = normalizedHLSSegmentContainer(reference.Capabilities.HLSSegmentContainer)
	}
	source := sources[0]
	if !input.ExternalPlayer {
		capabilities := service.playbackCapabilities(reference.Capabilities, maximumHeight, bitrate)
		preferences := ResolveInput{
			Capabilities: capabilities, AllowTranscoding: allowTranscoding, MaximumHeight: input.MaximumHeight,
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
		source = sources[0]
		if assetIndex := storedAssetIndex(assets, source.ID); assetIndex >= 0 {
			if err := service.prewarmHLS(ctx, prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID), source, &assets[assetIndex]); err != nil {
				return Preparation{}, err
			}
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
		service.preparations.evict(reference, policy)
		service.stopHLSSession(prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID))
		return Preparation{}, err
	}
	service.preparations.markResolveHandoff(
		prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID), reference, policy, input.StartSeconds,
	)
	return result, nil
}

func (service *Service) Resolve(ctx context.Context, principal auth.Principal, input ResolveInput) (Session, error) {
	return service.resolve(ctx, principal, input, nil)
}

func (service *Service) resolve(ctx context.Context, principal auth.Principal, input ResolveInput, deliveryHandle *DeliveryHandle) (Session, error) {
	input.SourceRef = strings.TrimSpace(input.SourceRef)
	input.TitleID = strings.TrimSpace(input.TitleID)
	input.PreferredSubtitleID = strings.TrimSpace(input.PreferredSubtitleID)
	bitrate, runtimeAllowsTranscoding := service.runtimePlaybackDecision(ctx)
	input.AllowTranscoding = input.AllowTranscoding && runtimeAllowsTranscoding
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
	maximumHeight := effectivePlaybackMaximumHeight(reference.Capabilities.MaximumHeight, input.MaximumHeight)
	policy := playbackPolicy{allowTranscoding: input.AllowTranscoding, maximumHeight: maximumHeight, bitrateKbps: bitrate, externalPlayer: input.ExternalPlayer}
	prewarmSessionID := prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID)
	prepared, preparedHandoff := service.preparations.consumeResolveHandoff(prewarmSessionID, reference, policy, input.StartSeconds)
	if preparedHandoff {
		preparedTransportRevision := reference.TransportRevision
		preparedReference := reference
		refreshedReference, refreshErr := service.refreshSourceReference(ctx, principal, reference)
		if refreshErr != nil {
			service.preparations.evict(preparedReference, policy)
			service.stopHLSSession(prewarmSessionID)
			return Session{}, refreshErr
		}
		reference = refreshedReference
		maximumHeight = effectivePlaybackMaximumHeight(reference.Capabilities.MaximumHeight, input.MaximumHeight)
		policy = playbackPolicy{allowTranscoding: input.AllowTranscoding, maximumHeight: maximumHeight, bitrateKbps: bitrate, externalPlayer: input.ExternalPlayer}
		if reference.TransportRevision != preparedTransportRevision {
			service.stopHLSSession(prewarmSessionID)
			prepared, err = service.preparedPlayback(ctx, principal, reference, policy)
			if err != nil {
				return Session{}, err
			}
		}
	} else {
		reference, err = service.refreshSourceReference(ctx, principal, reference)
		if err != nil {
			return Session{}, err
		}
		maximumHeight = effectivePlaybackMaximumHeight(reference.Capabilities.MaximumHeight, input.MaximumHeight)
		policy = playbackPolicy{allowTranscoding: input.AllowTranscoding, maximumHeight: maximumHeight, bitrateKbps: bitrate, externalPlayer: input.ExternalPlayer}
		prepared, err = service.preparedPlayback(ctx, principal, reference, policy)
		if err != nil {
			if err == ErrTranscodingDisabled {
				service.stopHLSSession(prewarmSessionID)
			}
			return Session{}, err
		}
	}
	sources := []Source{cloneSource(prepared.source)}
	streamAssets := make([]storedAsset, 0, 1)
	if prepared.asset != nil {
		streamAssets = append(streamAssets, cloneStoredAsset(*prepared.asset))
		streamAssets[len(streamAssets)-1].StartSeconds = input.StartSeconds
		streamAssets[len(streamAssets)-1].HLSSegmentContainer = normalizedHLSSegmentContainer(reference.Capabilities.HLSSegmentContainer)
	}
	if !input.ExternalPlayer {
		input.Capabilities = service.playbackCapabilities(reference.Capabilities, maximumHeight, bitrate)
		input.PreferredAudioLanguage = reference.PreferredAudioLanguage
		input.PreferredSubtitleLanguage = reference.PreferredSubtitleLanguage
		input.PreferredForcedSubtitleLanguage = reference.PreferredForcedSubtitleLanguage
		if err := applyPlaybackPreferences(sources, streamAssets, input); err != nil {
			if err == ErrTranscodingDisabled {
				service.stopHLSSession(prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID))
			}
			return Session{}, err
		}
	} else {
		input.PreferredSubtitleLanguage = reference.PreferredSubtitleLanguage
		input.PreferredForcedSubtitleLanguage = reference.PreferredForcedSubtitleLanguage
	}
	subtitles := append([]Subtitle{}, prepared.subtitles...)
	if err := applySubtitlePreference(subtitles, input.PreferredSubtitleID, input.PreferredForcedSubtitleLanguage, input.PreferredSubtitleLanguage); err != nil {
		return Session{}, err
	}
	subtitleAssets := make([]storedAsset, len(prepared.subtitleAssets))
	for index := range prepared.subtitleAssets {
		subtitleAssets[index] = cloneStoredAsset(prepared.subtitleAssets[index])
		subtitleAssets[index].StartSeconds = input.StartSeconds
	}
	if !input.ExternalPlayer {
		if err := applySubtitleDecision(sources, streamAssets, subtitles, subtitleAssets, input.Capabilities, input.AllowTranscoding); err != nil {
			if err == ErrTranscodingDisabled {
				service.stopHLSSession(prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID))
			}
			return Session{}, err
		}
	}
	assets := append(streamAssets, subtitleAssets...)
	session, err := service.createSession(ctx, principal, reference, input.TitleID, sources, subtitles, assets, prepared.addonIDs, prepared.providerErrors, deliveryHandle)
	if err == ErrSourceReferenceExpired {
		service.preparations.evict(reference, policy)
	}
	return session, err
}
func (service *Service) refreshSourceReference(ctx context.Context, principal auth.Principal, reference sourceReference) (sourceReference, error) {
	addonID := strings.TrimSpace(reference.Source.AddonID)
	if addonID == "" {
		return reference, nil
	}
	if err := service.validateSourceReferenceAccess(ctx, principal, reference); err != nil {
		return sourceReference{}, err
	}
	result, err := service.addons.FetchPlaybackResource(ctx, principal, addonID, addon.ResourcePath{
		Resource: "stream", Type: reference.AddonMediaType, ID: reference.ResourceID,
	})
	if err != nil {
		if errors.Is(err, addon.ErrNotFound) || errors.Is(err, addon.ErrForbidden) ||
			errors.Is(err, addon.ErrActiveProfileRequired) || errors.Is(err, addon.ErrInvalidInput) {
			return service.expireSourceReference(principal, reference)
		}
		return sourceReference{}, ErrProviderUnavailable
	}
	candidates, assets, err := normalizeStreams(addon.ResourceBatch{Results: []addon.ResourceResult{result}}, reference.Capabilities)
	if err != nil {
		return sourceReference{}, ErrProviderUnavailable
	}
	snapshotIdentity := stableSourceIdentity(reference.Source)
	selectedIndex := -1
	for index := range candidates {
		candidate := candidates[index]
		matches := snapshotIdentity != "" && stableSourceIdentity(candidate) == snapshotIdentity
		if snapshotIdentity == "" {
			matches = strings.TrimSpace(reference.Source.ManifestID) != "" &&
				strings.TrimSpace(candidate.AddonID) == addonID &&
				strings.TrimSpace(candidate.ManifestID) == strings.TrimSpace(reference.Source.ManifestID) &&
				candidate.StreamIndex == reference.Source.StreamIndex
		}
		if !matches {
			continue
		}
		if selectedIndex >= 0 {
			return service.expireSourceReference(principal, reference)
		}
		selectedIndex = index
	}
	if selectedIndex < 0 {
		return service.expireSourceReference(principal, reference)
	}
	selected := cloneSource(candidates[selectedIndex])
	freshAssetIndex := storedAssetIndex(assets, selected.ID)
	selected.ID = reference.Source.ID
	var selectedAsset *storedAsset
	if freshAssetIndex >= 0 {
		asset := cloneStoredAsset(assets[freshAssetIndex])
		asset.ID = selected.ID
		selectedAsset = &asset
	}
	changed := sourceTransportChanged(reference.Source, reference.Asset, selected, selectedAsset)
	updated, replaced, err := service.references.replaceSelection(reference.ID, principal, reference.SelectionRevision, selected, selectedAsset)
	if err != nil {
		return sourceReference{}, err
	}
	if replaced && changed {
		service.evictStaleSourcePreparation(reference)
	}
	return updated, nil
}

func sourceTransportChanged(previousSource Source, previousAsset *storedAsset, source Source, asset *storedAsset) bool {
	if previousSource.Mode != source.Mode || previousSource.URL != source.URL || previousSource.YTID != source.YTID ||
		previousSource.InfoHash != source.InfoHash || previousSource.Protocol != source.Protocol || previousSource.Container != source.Container {
		return true
	}
	if (previousAsset == nil) != (asset == nil) {
		return true
	}
	if previousAsset == nil {
		return false
	}
	if previousAsset.URL != asset.URL || previousAsset.Container != asset.Container || len(previousAsset.Headers) != len(asset.Headers) {
		return true
	}
	for name, value := range previousAsset.Headers {
		candidateValue, exists := asset.Headers[name]
		if !exists || candidateValue != value {
			return true
		}
	}
	return false
}

func (service *Service) expireSourceReference(principal auth.Principal, reference sourceReference) (sourceReference, error) {
	current, expired, err := service.references.expireSelection(reference.ID, principal, reference.SelectionRevision)
	if err != nil {
		return sourceReference{}, err
	}
	if !expired {
		return current, nil
	}
	service.evictStaleSourcePreparation(reference)
	return sourceReference{}, ErrSourceReferenceExpired
}

func (service *Service) evictStaleSourcePreparation(reference sourceReference) {
	if service.preparations != nil {
		service.preparations.evictRevision(reference.ID, reference.TransportRevision)
	}
	if service.probes == nil || reference.Asset == nil {
		return
	}
	key := mediaProbeKey(*reference.Asset)
	service.probes.mu.Lock()
	service.probes.generation++
	delete(service.probes.entries, key)
	service.probes.mu.Unlock()
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

type playbackSessionLimits struct {
	perAuthSession int
	perProfile     int
	global         int
}

func playbackSessionDefaultLimits() playbackSessionLimits {
	return playbackSessionLimits{
		perAuthSession: maximumPlaybackSessionsPerAuthSession,
		perProfile:     maximumPlaybackSessionsPerProfile,
		global:         maximumPlaybackSessionsGlobal,
	}
}

func encodePlaybackSessionAssets(assets []storedAsset) ([]byte, error) {
	if assets == nil {
		assets = []storedAsset{}
	}
	var encoded bytes.Buffer
	output := &maximumWriter{destination: &encoded, remaining: maximumPlaybackSessionAssetBytes + 1}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(assets); err != nil {
		if output.exceeded || errors.Is(err, errMediaOutputLimit) {
			return nil, fmt.Errorf("%w: playback session assets exceed %d bytes", ErrMediaCapacityReached, maximumPlaybackSessionAssetBytes)
		}
		return nil, fmt.Errorf("encode playback assets: %w", err)
	}
	payload := bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'})
	if len(payload) > maximumPlaybackSessionAssetBytes {
		return nil, fmt.Errorf("%w: playback session assets exceed %d bytes", ErrMediaCapacityReached, maximumPlaybackSessionAssetBytes)
	}
	return payload, nil
}

func (service *Service) createSession(ctx context.Context, principal auth.Principal, reference sourceReference, titleID string, sources []Source, subtitles []Subtitle, assets []storedAsset, addonIDs []string, providerErrors []ProviderFailure, deliveryHandle *DeliveryHandle) (Session, error) {
	return service.createSessionWithLimits(ctx, principal, reference, titleID, sources, subtitles, assets, addonIDs, providerErrors, deliveryHandle, playbackSessionDefaultLimits())
}

func (service *Service) createSessionWithLimits(ctx context.Context, principal auth.Principal, reference sourceReference, titleID string, sources []Source, subtitles []Subtitle, assets []storedAsset, addonIDs []string, providerErrors []ProviderFailure, deliveryHandle *DeliveryHandle, limits playbackSessionLimits) (Session, error) {
	token, tokenHash, err := newSessionToken()
	if err != nil {
		return Session{}, fmt.Errorf("create playback token: %w", err)
	}
	assetsJSON, err := encodePlaybackSessionAssets(assets)
	if err != nil {
		return Session{}, err
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
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", playbackSessionAdmissionLockID); err != nil {
		return Session{}, fmt.Errorf("lock playback session admission: %w", err)
	}
	rows, err := tx.Query(ctx, cleanupInactiveSessionsSQL, intervalLiteral(playbackSessionIdleTTL))
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
	var globalCount, authSessionCount, profileCount int
	if err := tx.QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE auth_session_id = $1::uuid),
			count(*) FILTER (WHERE profile_id = $2::uuid)
		FROM playback_sessions
		WHERE expires_at > now() AND last_seen_at > now() - $3::interval
	`, principal.SessionID, *principal.ActiveProfileID, intervalLiteral(playbackSessionIdleTTL)).Scan(&globalCount, &authSessionCount, &profileCount); err != nil {
		return Session{}, fmt.Errorf("count active playback sessions: %w", err)
	}
	if globalCount >= limits.global || authSessionCount >= limits.perAuthSession || profileCount >= limits.perProfile {
		if err := tx.Commit(ctx); err != nil {
			return Session{}, fmt.Errorf("commit inactive playback session cleanup: %w", err)
		}
		sort.Strings(inactiveSessionIDs)
		for _, identifier := range inactiveSessionIDs {
			service.stopHLSSession(identifier)
		}
		return Session{}, ErrMediaCapacityReached
	}
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
		sources[index].MediaTimeline = sessionSourceMediaTimeline(sources[index], assets)
		sources[index].URL = sessionSourceURL(sources[index], assets, sessionID, token)
	}
	for index := range subtitles {
		if subtitles[index].Delivery != "burn" {
			subtitles[index].URL = assetURL(sessionID, subtitles[index].ID, token, "")
		}
	}
	result := Session{
		ID: sessionID, SelectedSourceID: firstCompatibleSource(sources), SelectedAudioTrack: selectedAudioTrack(sources, assets),
		SelectedSubtitleID: selectedSubtitle(subtitles), Sources: sources, Subtitles: subtitles,
		ProviderErrors: append([]ProviderFailure{}, providerErrors...), ExpiresAt: expiresAt,
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("commit playback session: %w", err)
	}
	sort.Strings(inactiveSessionIDs)
	for _, identifier := range inactiveSessionIDs {
		service.stopHLSSession(identifier)
	}
	if err := service.startSessionHLS(ctx, prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID), sessionID, sources, assets); err != nil {
		service.stopHLSSession(sessionID)
		return Session{}, errors.Join(err, service.deleteCreatedSession(ctx, principal.SessionID, sessionID))
	}
	if err := service.commitAuthorizedProfileBoundary(ctx, principal); err != nil {
		service.stopHLSSession(sessionID)
		return Session{}, errors.Join(err, service.deleteCreatedSession(ctx, principal.SessionID, sessionID))
	}
	if deliveryHandle != nil {
		*deliveryHandle = deliveryHandleForSession(sessionID, token, sources, assets)
		if deliveryHandle.children != nil {
			deliveryHandle.children.bindBudget(service.deliveryChildBudget(), *principal.ActiveProfileID, service.now)
		}
	}
	return result, nil
}

func sessionSourceMediaTimeline(source Source, assets []storedAsset) string {
	if source.Protocol != "hls" || source.Mode != processingRemux && source.Mode != processingTranscodeAudio && source.Mode != processingTranscode {
		return ""
	}
	if assetIndex := storedAssetIndex(assets, source.ID); source.Mode == processingTranscode && assetIndex >= 0 {
		if _, seekable := seekableHLSSegmentCount(assets[assetIndex]); seekable {
			return "absolute"
		}
	}
	return "relative"
}

func sessionSourceURL(source Source, assets []storedAsset, sessionID, token string) string {
	if source.URL == "" {
		return ""
	}
	if source.Protocol == "hls" && (source.Mode == processingRemux || source.Mode == processingTranscodeAudio || source.Mode == processingTranscode) {
		startSeconds := float64(0)
		if assetIndex := storedAssetIndex(assets, source.ID); assetIndex >= 0 {
			startSeconds = assets[assetIndex].StartSeconds
		}
		return hlsAssetURLAt(sessionID, source.ID, token, "index.m3u8", startSeconds)
	}
	return assetURL(sessionID, source.ID, token, "")
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
	if len(input.MediaType) < 1 || len(input.MediaType) > 64 || len(input.ResourceID) < 1 || len(input.ResourceID) > 2048 || len(input.AddonID) > 128 || input.MaximumSources < 0 || input.MaximumSources > maximumAggregateProviderStreams {
		return ErrInvalidInput
	}
	if len(input.PreferredAudioLanguage) > 64 || len(input.PreferredSubtitleLanguage) > 64 || len(input.PreferredForcedSubtitleLanguage) > 64 {
		return ErrInvalidInput
	}
	return validateCapabilities(input.Capabilities)
}

func validateResolveInput(input ResolveInput) error {
	if len(input.SourceRef) < 16 || len(input.SourceRef) > 128 || len(input.TitleID) > 128 || len(input.PreferredSubtitleID) > 128 || !validPlaybackMaximumHeight(input.MaximumHeight) {
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

func validPlaybackMaximumHeight(height int) bool {
	return height == 0 || height >= 144 && height <= 4320
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
		for _, list := range []string{profile.ContainersCSV, profile.AudioCodecsCSV} {
			if list == "" {
				continue
			}
			parts := strings.Split(list, ",")
			if len(list) > 2048 || len(parts) > 32 {
				return ErrInvalidInput
			}
			for _, part := range parts {
				if part = strings.TrimSpace(part); len(part) < 1 || len(part) > 64 {
					return ErrInvalidInput
				}
			}
		}
		if profile.MaximumVideoLevel < 0 || profile.MaximumVideoLevel > 1000 ||
			profile.VideoLevelRequired && profile.MaximumVideoLevel == 0 ||
			profile.MaximumVideoBitDepth != 0 && (profile.MaximumVideoBitDepth < 8 || profile.MaximumVideoBitDepth > 16) ||
			len(profile.ExcludedVideoRange) > 64 ||
			profile.VideoRangeRequired && strings.TrimSpace(profile.ExcludedVideoRange) == "" {
			return ErrInvalidInput
		}

	}
	if len(capabilities.ContainerProfiles) > 256 {
		return ErrInvalidInput
	}
	for _, profile := range capabilities.ContainerProfiles {
		containers := strings.Split(profile.ContainersCSV, ",")
		if len(profile.ContainersCSV) == 0 || len(profile.ContainersCSV) > 2048 || len(containers) > 32 || len(profile.Conditions) > 32 {
			return ErrInvalidInput
		}
		for _, container := range containers {
			if container = strings.TrimSpace(container); len(container) == 0 || len(container) > 64 {
				return ErrInvalidInput
			}
		}
		for _, condition := range profile.Conditions {
			if len(strings.TrimSpace(condition.Condition)) == 0 || len(condition.Condition) > 64 ||
				len(strings.TrimSpace(condition.Property)) == 0 || len(condition.Property) > 64 ||
				len(strings.TrimSpace(condition.Value)) == 0 || len(condition.Value) > 64 {
				return ErrInvalidInput
			}
		}
	}
	if capabilities.HLSSegmentContainer != "" &&
		capabilities.HLSSegmentContainer != "ts" && capabilities.HLSSegmentContainer != "mp4" {
		return ErrInvalidInput
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
func (service *Service) playbackCapabilities(client Capabilities, maximumHeight, bitrateKbps int) Capabilities {
	capabilities := cloneCapabilities(client)
	capabilities.MaximumHeight = maximumHeight
	capabilities.TranscodeVideoBitrateKbps = bitrateKbps
	if processor, ok := service.processor.(interface{ TranscodeCapabilities() TranscodeCapabilities }); ok {
		engine := processor.TranscodeCapabilities()
		engine.DecodeCodecs = append([]string(nil), engine.DecodeCodecs...)
		engine.EncodeCodecs = append([]string(nil), engine.EncodeCodecs...)
		capabilities.transcodeCapabilities = engine
	}
	if processor, ok := service.processor.(interface{ ToneMapBackend() string }); ok {
		capabilities.toneMapBackend = processor.ToneMapBackend()
	}
	if processor, ok := service.processor.(interface{ ToneMapMaximumHeight() int }); ok {
		capabilities.ToneMapMaximumHeight = processor.ToneMapMaximumHeight()
	} else {
		capabilities.ToneMapMaximumHeight = softwareToneMapMaximumHeight
	}
	return capabilities
}
func normalizedHLSSegmentContainer(container string) string {
	if container == "mp4" {
		return "mp4"
	}
	return "ts"
}

func sourceOptionLabel(ordinal int) string {
	return fmt.Sprintf("Source %d", ordinal)
}

func sourceOptionDisplay(source Source, ordinal int) (string, string, string) {
	name := strings.TrimSpace(source.Name)
	title := strings.TrimSpace(source.Title)
	description := strings.TrimSpace(source.Description)
	filename := strings.TrimSpace(source.Filename)
	if description == "" {
		description = title
	}
	if name == "" {
		switch {
		case title != "":
			name = title
		case filename != "":
			name = filename
			filename = ""
		default:
			name = sourceOptionLabel(ordinal)
		}
	}
	if description == name {
		description = ""
	}
	if filename == name || filename == description {
		filename = ""
	}
	return name, description, filename
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
				assets = append(assets, storedAsset{ID: id, Kind: "stream", URL: streamURL, Headers: headers, Container: source.Container, HLSSegmentContainer: normalizedHLSSegmentContainer(capabilities.HLSSegmentContainer)})
			case directMediaExternalURL(externalURL):
				source.Mode = "direct"
				source.URL = externalURL
				source.Protocol = protocolFor(externalURL)
				source.Container = containerFor(externalURL)
				source.Compatible = supports(capabilities.StreamingProtocols, source.Protocol) && supportsContainer(capabilities.Containers, source.Container)
				assets = append(assets, storedAsset{ID: id, Kind: "stream", URL: externalURL, Headers: headers, Container: source.Container, HLSSegmentContainer: normalizedHLSSegmentContainer(capabilities.HLSSegmentContainer)})
			case validYouTubeID(ytID):
				source.Mode = "youtube"
				source.YTID = ytID
				source.Protocol = "youtube"
				source.Compatible = supports(capabilities.StreamingProtocols, source.Protocol)
			case validBTIH(infoHash) && (supports(capabilities.ExternalPlayers, "system") || supports(capabilities.ExternalPlayers, "android_magnet")):
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

func normalizeSubtitles(batch addon.ResourceBatch, preserveOriginalValues ...bool) ([]Subtitle, []storedAsset, error) {
	type decodedResponse struct {
		result   addon.ResourceResult
		response addon.ProviderSubtitleResponse
	}
	preserveOriginal := len(preserveOriginalValues) > 0 && preserveOriginalValues[0]
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
			parsedURL, _ := url.Parse(subtitle.URL)
			extension := strings.ToLower(pathExtension(parsedURL.Path))
			kind := "subtitle"
			clientExtension := clientSubtitleExtension(extension)
			if preserveOriginal {
				clientExtension = originalSubtitleExtension(extension)
			} else if extension == ".srt" || extension == ".ass" || extension == ".ssa" {
				kind = assetKindConvertedSubtitle
				clientExtension = ".vtt"
			}
			id := fmt.Sprintf("subtitle-%d%s", len(subtitles)+1, clientExtension)
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

func clientSubtitleExtension(extension string) string {
	switch extension {
	case ".vtt", ".webvtt", ".ttml", ".dfxp", ".xml":
		return extension
	default:
		return ""
	}
}

func originalSubtitleExtension(extension string) string {
	switch extension {
	case ".srt", ".ass", ".ssa", ".vtt", ".webvtt", ".ttml", ".dfxp", ".xml":
		return extension
	default:
		return ""
	}
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

func directMediaExternalURL(rawURL string) bool {
	return validMediaURL(rawURL) && (protocolFor(rawURL) == "hls" || containerFor(rawURL) != "")
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

func validBTIH(value string) bool {
	if len(value) != 40 && len(value) != 32 {
		return false
	}
	for _, character := range value {
		if len(value) == 40 {
			if (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F') {
				continue
			}
			return false
		}
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '2' && character <= '7') {
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
