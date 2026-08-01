package playback

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
)

type ResourceFetcher interface {
	FetchAll(context.Context, auth.Principal, addon.ResourcePath) (addon.ResourceBatch, error)
}

type Service struct {
	pool           *pgxpool.Pool
	addons         ResourceFetcher
	client         *http.Client
	introDBClient  *http.Client
	introDBBaseURL string
	processor      MediaProcessor
	now            func() time.Time
	mediaOptions   MediaOptions
	references     *sourceReferenceStore
	probes         *mediaProbeCache
	preparations   *playbackPreparationCache
	hlsMu          sync.Mutex
	hlsJobs        map[string]*hlsJob
}

type rawStream struct {
	Name          string          `json:"name"`
	Title         string          `json:"title"`
	URL           string          `json:"url"`
	Description   string          `json:"description"`
	YTID          string          `json:"ytId"`
	ExternalURL   string          `json:"externalUrl"`
	InfoHash      string          `json:"infoHash"`
	FileIndex     *int            `json:"fileIdx"`
	BehaviorHints json.RawMessage `json:"behaviorHints"`
}

type rawSubtitle struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Language string `json:"lang"`
	Forced   bool   `json:"forced"`
}

type behaviorHints struct {
	NotWebReady  bool   `json:"notWebReady"`
	Filename     string `json:"filename"`
	ProxyHeaders struct {
		Request map[string]json.RawMessage `json:"request"`
	} `json:"proxyHeaders"`
}

type fetchedResources struct {
	batch addon.ResourceBatch
	err   error
}

func NewService(pool *pgxpool.Pool, addons ResourceFetcher, processor MediaProcessor, options MediaOptions) (*Service, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 15 * time.Second
	transport.MaxIdleConnsPerHost = 8
	options = normalizeMediaOptions(options)
	if err := os.RemoveAll(options.TempDirectory); err != nil {
		return nil, fmt.Errorf("clear media workspace: %w", err)
	}
	if err := os.MkdirAll(options.TempDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create media workspace: %w", err)
	}
	now := func() time.Time { return time.Now().UTC() }
	return &Service{
		pool: pool, addons: addons, client: &http.Client{Transport: transport}, processor: processor,
		introDBClient: &http.Client{Transport: transport.Clone(), Timeout: 8 * time.Second}, introDBBaseURL: introDBDefaultBaseURL,
		now: now, mediaOptions: options, hlsJobs: make(map[string]*hlsJob),
		references: newSourceReferenceStore(now), probes: newMediaProbeCache(now), preparations: newPlaybackPreparationCache(now),
	}, nil
}

func (service *Service) Sources(ctx context.Context, principal auth.Principal, input SourcesInput) (SourceList, error) {
	input.MediaType = strings.TrimSpace(input.MediaType)
	input.ResourceID = strings.TrimSpace(input.ResourceID)
	input.PreferredAudioLanguage = strings.TrimSpace(input.PreferredAudioLanguage)
	input.PreferredSubtitleLanguage = strings.TrimSpace(input.PreferredSubtitleLanguage)
	input.PreferredForcedSubtitleLanguage = strings.TrimSpace(input.PreferredForcedSubtitleLanguage)
	if !service.hasActiveProfile(principal) {
		return SourceList{}, ErrActiveProfileRequired
	}
	if err := validateSourcesInput(input); err != nil {
		return SourceList{}, err
	}
	addonMediaType := input.MediaType
	if addonMediaType == "episode" {
		addonMediaType = "series"
	}
	batch, err := service.addons.FetchAll(ctx, principal, addon.ResourcePath{Resource: "stream", Type: addonMediaType, ID: input.ResourceID})
	if err != nil {
		return SourceList{}, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	sources, assets := normalizeStreams(batch, input.Capabilities)
	if len(sources) == 0 && len(batch.Errors) > 0 {
		return SourceList{}, ErrProviderUnavailable
	}
	providerErrors := providerFailures(batch.Errors)
	options := make([]SourceOption, 0, len(sources))
	for index := range sources {
		source := sources[index]
		if input.Capabilities.MaximumHeight > 0 && sourceResolutionHint(source) > input.Capabilities.MaximumHeight {
			continue
		}
		var asset *storedAsset
		if assetIndex := storedAssetIndex(assets, source.ID); assetIndex >= 0 {
			value := cloneStoredAsset(assets[assetIndex])
			asset = &value
		}
		reference, referenceErr := service.references.put(sourceReference{
			AuthSessionID: principal.SessionID, ProfileID: *principal.ActiveProfileID,
			MediaType: input.MediaType, AddonMediaType: addonMediaType, ResourceID: input.ResourceID,
			Source: source, Asset: asset, Capabilities: input.Capabilities,
			PreferredAudioLanguage: input.PreferredAudioLanguage, PreferredSubtitleLanguage: input.PreferredSubtitleLanguage,
			PreferredForcedSubtitleLanguage: input.PreferredForcedSubtitleLanguage,
			ProviderErrors:                  providerErrors,
		})
		if referenceErr != nil {
			return SourceList{}, fmt.Errorf("create source reference: %w", referenceErr)
		}
		options = append(options, SourceOption{
			ID: source.ID, SourceRef: reference.ID, AddonID: source.AddonID, ManifestID: source.ManifestID,
			StreamIndex: source.StreamIndex, Name: sourceDisplayName(source), Description: source.Description,
			Filename: source.Filename, Protocol: source.Protocol, Container: source.Container, ExpiresAt: reference.ExpiresAt,
		})
	}
	return SourceList{Sources: options, ProviderErrors: providerErrors}, nil
}

func (service *Service) Prepare(ctx context.Context, principal auth.Principal, input PrepareInput) (Preparation, error) {
	input.SourceRef = strings.TrimSpace(input.SourceRef)
	if !service.hasActiveProfile(principal) {
		return Preparation{}, ErrActiveProfileRequired
	}
	if len(input.SourceRef) < 16 || len(input.SourceRef) > 128 || !validPlaybackStart(input.StartSeconds) {
		return Preparation{}, ErrInvalidInput
	}
	reference, err := service.references.get(input.SourceRef, principal)
	if err != nil {
		return Preparation{}, err
	}
	prepared, err := service.preparedPlayback(ctx, principal, reference)
	if err != nil {
		return Preparation{}, err
	}
	sources := []Source{cloneSource(prepared.source)}
	assets := make([]storedAsset, 0, 1)
	if prepared.asset != nil {
		assets = append(assets, cloneStoredAsset(*prepared.asset))
		assets[len(assets)-1].StartSeconds = input.StartSeconds
	}
	preferences := ResolveInput{
		Capabilities: reference.Capabilities, PreferredAudioLanguage: reference.PreferredAudioLanguage,
		PreferredSubtitleLanguage:       reference.PreferredSubtitleLanguage,
		PreferredForcedSubtitleLanguage: reference.PreferredForcedSubtitleLanguage,
	}
	if err := applyPlaybackPreferences(sources, assets, preferences); err != nil {
		return Preparation{}, err
	}
	source := sources[0]
	if assetIndex := storedAssetIndex(assets, source.ID); assetIndex >= 0 {
		if err := service.prewarmHLS(ctx, prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID), source, &assets[assetIndex]); err != nil {
			return Preparation{}, err
		}
	}
	return Preparation{
		SourceRef: reference.ID, Mode: source.Mode, Protocol: source.Protocol, Container: source.Container,
		Media: source.Media, SubtitleCount: len(prepared.subtitles), ExpiresAt: reference.ExpiresAt,
	}, nil
}

func (service *Service) Resolve(ctx context.Context, principal auth.Principal, input ResolveInput) (Session, error) {
	input.SourceRef = strings.TrimSpace(input.SourceRef)
	input.TitleID = strings.TrimSpace(input.TitleID)
	input.PreferredSubtitleID = strings.TrimSpace(input.PreferredSubtitleID)
	if !service.hasActiveProfile(principal) {
		return Session{}, ErrActiveProfileRequired
	}
	if err := validateResolveInput(input); err != nil {
		return Session{}, err
	}
	reference, err := service.references.get(input.SourceRef, principal)
	if err != nil {
		return Session{}, err
	}
	prepared, err := service.preparedPlayback(ctx, principal, reference)
	if err != nil {
		return Session{}, err
	}
	sources := []Source{cloneSource(prepared.source)}
	streamAssets := make([]storedAsset, 0, 1)
	if prepared.asset != nil {
		streamAssets = append(streamAssets, cloneStoredAsset(*prepared.asset))
		streamAssets[len(streamAssets)-1].StartSeconds = input.StartSeconds
	}
	input.Capabilities = reference.Capabilities
	input.PreferredAudioLanguage = reference.PreferredAudioLanguage
	input.PreferredSubtitleLanguage = reference.PreferredSubtitleLanguage
	input.PreferredForcedSubtitleLanguage = reference.PreferredForcedSubtitleLanguage
	if err := applyPlaybackPreferences(sources, streamAssets, input); err != nil {
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
	assets := append(streamAssets, subtitleAssets...)
	return service.createSession(ctx, principal, reference, input.TitleID, sources, subtitles, assets, prepared.providerErrors)
}

func (service *Service) createSession(ctx context.Context, principal auth.Principal, reference sourceReference, titleID string, sources []Source, subtitles []Subtitle, assets []storedAsset, providerErrors []ProviderFailure) (Session, error) {
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
	if _, err := service.cleanupInactiveSessions(ctx); err != nil {
		return Session{}, err
	}
	var sessionID string
	if err := service.pool.QueryRow(ctx, `
		INSERT INTO playback_sessions (
			auth_session_id, profile_id, title_id, media_type, resource_id, token_hash, assets, expires_at
		) VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8)
		RETURNING id::text
	`, principal.SessionID, *principal.ActiveProfileID, titleID, reference.MediaType, reference.ResourceID, tokenHash, assetsJSON, expiresAt).Scan(&sessionID); err != nil {
		return Session{}, fmt.Errorf("store playback session: %w", err)
	}
	if err := service.startSessionHLS(prewarmHLSSession(principal.SessionID, *principal.ActiveProfileID), sessionID, sources, assets); err != nil {
		_, _ = service.pool.Exec(ctx, "DELETE FROM playback_sessions WHERE id::text = $1", sessionID)
		return Session{}, err
	}
	for index := range sources {
		sources[index].URL = sessionSourceURL(sources[index], assets, sessionID, token)
	}
	for index := range subtitles {
		subtitles[index].URL = assetURL(sessionID, subtitles[index].ID, token, "", "")
	}
	return Session{
		ID: sessionID, SelectedSourceID: firstCompatibleSource(sources), SelectedAudioTrack: selectedAudioTrack(sources, assets),
		SelectedSubtitleID: selectedSubtitle(subtitles), Sources: sources, Subtitles: subtitles,
		ProviderErrors: providerErrors, ExpiresAt: expiresAt,
	}, nil
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
	command, err := service.pool.Exec(ctx, `
		DELETE FROM playback_sessions
		WHERE id::text = $1 AND auth_session_id = $2 AND profile_id = $3
	`, sessionID, principal.SessionID, *principal.ActiveProfileID)
	if err != nil {
		return fmt.Errorf("delete playback session: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrSessionNotFound
	}
	service.stopHLSSession(sessionID)
	return nil
}

func validateSourcesInput(input SourcesInput) error {
	if len(input.MediaType) < 1 || len(input.MediaType) > 64 || len(input.ResourceID) < 1 || len(input.ResourceID) > 2048 {
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
		capabilities.AudioCodecs, capabilities.HDRFormats, capabilities.ExternalPlayers, capabilities.ProcessingModes,
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
	if len(capabilities.ProcessingModes) > 1 ||
		len(capabilities.ProcessingModes) == 1 && !strings.EqualFold(strings.TrimSpace(capabilities.ProcessingModes[0]), processingRemux) {
		return ErrInvalidInput
	}
	return nil
}

func (service *Service) hasActiveProfile(principal auth.Principal) bool {
	return principal.ActiveProfileID != nil && principal.ProfileGrantExpiresAt != nil && principal.ProfileGrantExpiresAt.After(service.now())
}

func sourceDisplayName(source Source) string {
	for _, value := range []string{source.Name, source.Title, source.Filename} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "Stream"
}

func normalizeStreams(batch addon.ResourceBatch, capabilities Capabilities) ([]Source, []storedAsset) {
	sources := make([]Source, 0)
	assets := make([]storedAsset, 0)
	for _, result := range batch.Results {
		var payload struct {
			Streams []rawStream `json:"streams"`
		}
		if json.Unmarshal(result.Payload, &payload) != nil {
			continue
		}
		for streamIndex, stream := range payload.Streams {
			streamURL := strings.TrimSpace(stream.URL)
			externalURL := strings.TrimSpace(stream.ExternalURL)
			ytID := strings.TrimSpace(stream.YTID)
			infoHash := strings.TrimSpace(stream.InfoHash)
			var hints behaviorHints
			_ = json.Unmarshal(stream.BehaviorHints, &hints)
			headers := requestHeaders(hints)
			id := fmt.Sprintf("stream-%d", len(sources)+1)
			source := Source{
				ID: id, AddonID: result.AddonID, ManifestID: result.ManifestID,
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
	return sources, assets
}

func normalizeSubtitles(batch addon.ResourceBatch) ([]Subtitle, []storedAsset) {
	subtitles := make([]Subtitle, 0)
	assets := make([]storedAsset, 0)
	for _, result := range batch.Results {
		var payload struct {
			Subtitles []rawSubtitle `json:"subtitles"`
		}
		if json.Unmarshal(result.Payload, &payload) != nil {
			continue
		}
		for _, subtitle := range payload.Subtitles {
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
				ID: id, AddonID: result.AddonID, ManifestID: result.ManifestID,
				Language: strings.TrimSpace(subtitle.Language), URL: subtitle.URL, Forced: subtitle.Forced,
			})
			assets = append(assets, storedAsset{ID: id, Kind: kind, URL: subtitle.URL})
		}
	}
	return subtitles, assets
}

func requestHeaders(hints behaviorHints) map[string]string {
	headers := make(map[string]string)
	for name, raw := range hints.ProxyHeaders.Request {
		var value string
		if json.Unmarshal(raw, &value) == nil && value != "" {
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
		return true
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
