package playback

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
)

type preparationResourceFetcher struct {
	streamCalls       atomic.Int32
	subtitleCalls     atomic.Int32
	validationCalls   atomic.Int32
	validationErr     error
	validationAddonID string
}

func (fetcher *preparationResourceFetcher) FetchPlaybackResource(ctx context.Context, principal auth.Principal, _ string, resource addon.ResourcePath) (addon.ResourceResult, error) {
	batch, err := fetcher.FetchAllPlaybackResources(ctx, principal, resource)
	if err != nil || len(batch.Results) == 0 {
		return addon.ResourceResult{}, err
	}
	return batch.Results[0], nil
}

func (fetcher *preparationResourceFetcher) ValidatePlaybackAccess(_ context.Context, _ auth.Principal, addonID string) error {
	fetcher.validationCalls.Add(1)
	fetcher.validationAddonID = addonID
	return fetcher.validationErr
}

func (fetcher *preparationResourceFetcher) FetchAllPlaybackResources(_ context.Context, _ auth.Principal, resource addon.ResourcePath) (addon.ResourceBatch, error) {
	switch resource.Resource {
	case "stream":
		fetcher.streamCalls.Add(1)
		return addon.ResourceBatch{Results: []addon.ResourceResult{{
			AddonID: "addon-id", ManifestID: "manifest-id",
			Payload: []byte(`{"streams":[
				{"name":"First source","url":"https://media.example/first.mkv","behaviorHints":{"filename":"first.mkv"}},
				{"name":"Selected source","url":"https://media.example/selected.mkv","behaviorHints":{"filename":"selected.mkv"}}
			]}`),
		}}}, nil
	case "subtitles":
		fetcher.subtitleCalls.Add(1)
		return addon.ResourceBatch{}, nil
	default:
		return addon.ResourceBatch{}, nil
	}
}

type countingProbeProcessor struct {
	calls      atomic.Int32
	gate       <-chan struct{}
	inspection MediaInspection
	mu         sync.Mutex
	lastURL    string
}

func (processor *countingProbeProcessor) Probe(ctx context.Context, asset storedAsset) (MediaInspection, error) {
	processor.calls.Add(1)
	processor.mu.Lock()
	processor.lastURL = asset.URL
	processor.mu.Unlock()
	if processor.gate != nil {
		select {
		case <-ctx.Done():
			return MediaInspection{}, ctx.Err()
		case <-processor.gate:
		}
	}
	return processor.inspection, nil
}

func TestSourcesAndPrepareKeepProviderURLsOpaqueAndInspectOnlySelection(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	profileID := "profile-id"
	profileGrantExpiresAt := now.Add(time.Hour)
	principal := auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, SessionID: "auth-session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &profileGrantExpiresAt}
	fetcher := &preparationResourceFetcher{}
	processor := &countingProbeProcessor{inspection: MediaInspection{
		DurationSeconds: 1320,
		Container:       "mp4",
		VideoTracks:     []MediaTrack{{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080}},
		AudioTracks:     []MediaTrack{{Index: 1, Type: "audio", Codec: "aac", Channels: 2}},
	}}
	service := &Service{
		addons: fetcher, processor: processor, now: func() time.Time { return now },
		references:       newSourceReferenceStore(func() time.Time { return now }),
		probes:           newMediaProbeCache(func() time.Time { return now }),
		preparations:     newPlaybackPreparationCache(func() time.Time { return now }),
		profileTxFactory: testPlaybackProfileTxFactory,
	}

	list, err := service.Sources(context.Background(), principal, SourcesInput{
		MediaType: "movie", ResourceID: "tt1234567",
		Capabilities: Capabilities{
			StreamingProtocols: []string{"http"}, Containers: []string{"mp4"},
			VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
			ProcessingModes: []string{processingRemux},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Sources) != 2 || processor.calls.Load() != 0 {
		t.Fatalf("listing sources unexpectedly inspected media: sources=%d probes=%d", len(list.Sources), processor.calls.Load())
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "media.example") {
		t.Fatalf("source list leaked a provider URL: %s", encoded)
	}

	var selected SourceOption
	for _, source := range list.Sources {
		if source.Name == "Selected source" {
			selected = source
			break
		}
	}
	if len(selected.SourceRef) < 16 {
		t.Fatalf("selected source has no opaque reference: %+v", selected)
	}
	prepared, err := service.Prepare(context.Background(), principal, PrepareInput{SourceRef: selected.SourceRef})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.SourceRef != selected.SourceRef || prepared.Mode != "direct" || processor.calls.Load() != 1 {
		t.Fatalf("unexpected preparation: preparation=%+v probes=%d", prepared, processor.calls.Load())
	}
	service.preparations.mu.Lock()
	cachedPlayback := service.preparations.entries[playbackPreparationCacheKey(selected.SourceRef, playbackPolicy{})].playback
	service.preparations.mu.Unlock()
	if cachedPlayback.asset == nil {
		t.Fatal("prepared playback did not retain its stream asset")
	}
	cachedDuration := cachedPlayback.asset.DurationSeconds
	if cachedDuration != 1320 {
		t.Fatalf("prepared playback asset duration = %v, want 1320", cachedDuration)
	}
	processor.mu.Lock()
	lastURL := processor.lastURL
	processor.mu.Unlock()
	if lastURL != "https://media.example/selected.mkv" {
		t.Fatalf("prepared the wrong provider source: %q", lastURL)
	}

	if _, err := service.Prepare(context.Background(), principal, PrepareInput{SourceRef: selected.SourceRef}); err != nil {
		t.Fatal(err)
	}
	if processor.calls.Load() != 1 || fetcher.streamCalls.Load() != 1 || fetcher.subtitleCalls.Load() != 1 || fetcher.validationCalls.Load() != 2 || fetcher.validationAddonID != "addon-id" {
		t.Fatalf("cached preparation repeated remote work or validated the wrong addon: probes=%d streams=%d subtitles=%d validations=%d addon=%q", processor.calls.Load(), fetcher.streamCalls.Load(), fetcher.subtitleCalls.Load(), fetcher.validationCalls.Load(), fetcher.validationAddonID)
	}
}

func TestPrepareAndResolveReauthorizeCachedSourceReferences(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	profileID := "profile-id"
	grantExpiresAt := now.Add(time.Hour)
	principal := auth.Principal{
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		SessionID: "auth-session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
	}
	tests := []struct {
		name          string
		validationErr error
		invoke        func(*Service, string) error
	}{
		{
			name: "prepare after disable", validationErr: addon.ErrNotFound,
			invoke: func(service *Service, sourceRef string) error {
				_, err := service.Prepare(context.Background(), principal, PrepareInput{SourceRef: sourceRef})
				return err
			},
		},
		{
			name: "resolve after profile access loss", validationErr: addon.ErrForbidden,
			invoke: func(service *Service, sourceRef string) error {
				_, err := service.Resolve(context.Background(), principal, ResolveInput{SourceRef: sourceRef})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fetcher := &preparationResourceFetcher{validationErr: test.validationErr}
			processor := &countingProbeProcessor{}
			references := newSourceReferenceStore(func() time.Time { return now })
			reference, err := references.put(sourceReference{
				AuthSessionID: principal.SessionID, ProfileID: profileID, MediaType: "movie", AddonMediaType: "movie", ResourceID: "tt1234567",
				Source: Source{ID: "source-id", AddonID: "addon-id", ManifestID: "manifest-id", Mode: "direct", Protocol: "http", Container: "mp4", Compatible: true},
			})
			if err != nil {
				t.Fatal(err)
			}
			preparations := newPlaybackPreparationCache(func() time.Time { return now })
			preparations.entries[playbackPreparationCacheKey(reference.ID, playbackPolicy{})] = playbackPreparationEntry{
				playback: preparedPlayback{source: cloneSource(reference.Source)}, expiresAt: reference.ExpiresAt,
			}
			authorizationCalls := 0
			service := &Service{
				addons: fetcher, processor: processor, now: func() time.Time { return now },
				references: references, probes: newMediaProbeCache(func() time.Time { return now }), preparations: preparations,
				hlsJobs: make(map[string]*hlsJob),
				profileTxFactory: func(context.Context, auth.Principal) (playbackProfileTransaction, error) {
					authorizationCalls++
					return testPlaybackProfileTransaction{}, nil
				},
			}

			if err := test.invoke(service, reference.ID); err != ErrSourceReferenceExpired {
				t.Fatalf("cached source rejection error = %v, want opaque expiry", err)
			}
			if fetcher.validationCalls.Load() != 1 || fetcher.validationAddonID != "addon-id" || fetcher.streamCalls.Load() != 0 || fetcher.subtitleCalls.Load() != 0 || processor.calls.Load() != 0 {
				t.Fatalf("rejected cached source did work or validated the wrong addon: validations=%d addon=%q streams=%d subtitles=%d probes=%d", fetcher.validationCalls.Load(), fetcher.validationAddonID, fetcher.streamCalls.Load(), fetcher.subtitleCalls.Load(), processor.calls.Load())
			}
			if authorizationCalls != 1 || len(service.hlsJobs) != 0 {
				t.Fatalf("rejected cached source reached prewarm/session work: authorizations=%d hlsJobs=%d", authorizationCalls, len(service.hlsJobs))
			}
		})
	}
}

func TestSourceReferenceAccessValidationDistinguishesInfrastructureFailure(t *testing.T) {
	storageErr := errors.New("addon authorization storage unavailable")
	fetcher := &preparationResourceFetcher{validationErr: storageErr}
	service := &Service{addons: fetcher}
	err := service.validateSourceReferenceAccess(context.Background(), auth.Principal{}, sourceReference{Source: Source{AddonID: "addon-id"}})
	if !errors.Is(err, storageErr) || errors.Is(err, ErrSourceReferenceExpired) {
		t.Fatalf("validator infrastructure error = %v, want preserved non-expiry failure", err)
	}
	if fetcher.validationCalls.Load() != 1 {
		t.Fatalf("validator calls = %d, want 1", fetcher.validationCalls.Load())
	}
	if err := service.validateSourceReferenceAccess(context.Background(), auth.Principal{}, sourceReference{}); err != ErrSourceReferenceExpired {
		t.Fatalf("empty addon identity error = %v, want opaque expiry", err)
	}
	if fetcher.validationCalls.Load() != 1 {
		t.Fatalf("empty addon identity reached validator: calls=%d", fetcher.validationCalls.Load())
	}
}

func TestBuildPreparedPlaybackReportsMissingConversionCapabilityWithoutEncoding(t *testing.T) {
	fetcher := &preparationResourceFetcher{}
	processor := &countingProbeProcessor{inspection: MediaInspection{
		Container:   "matroska",
		VideoTracks: []MediaTrack{{Index: 0, Type: "video", Codec: "vp9", Width: 1920, Height: 1080}},
		AudioTracks: []MediaTrack{{Index: 1, Type: "audio", Codec: "aac", Channels: 2}},
	}}
	service := &Service{
		addons: fetcher, processor: processor,
		probes: newMediaProbeCache(time.Now),
	}
	playback, err := service.buildPreparedPlayback(context.Background(), auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator}, sourceReference{
		AddonMediaType: "movie", ResourceID: "tt1234567",
		Source: Source{
			ID: "stream-1", Mode: "direct", URL: "https://media.example/movie.mkv",
			Protocol: "http", Container: "mkv",
		},
		Asset: &storedAsset{ID: "stream-1", Kind: "stream", URL: "https://media.example/movie.mkv"},
		Capabilities: Capabilities{
			StreamingProtocols: []string{"http"},
			Containers:         []string{"mp4"},
			VideoCodecs:        []string{"h264"},
			AudioCodecs:        []string{"aac"},
			ProcessingModes:    []string{processingRemux},
		},
	})
	if !errors.Is(err, ErrClientCapabilityMissing) || playback.source.Mode != "" || processor.calls.Load() != 1 {
		t.Fatalf("missing client mode did not fail before encoding: playback=%+v probes=%d err=%v", playback, processor.calls.Load(), err)
	}
}

func TestMediaProbeCacheDeduplicatesConcurrentInspection(t *testing.T) {
	gate := make(chan struct{})
	processor := &countingProbeProcessor{
		gate:       gate,
		inspection: MediaInspection{Container: "mp4", VideoTracks: []MediaTrack{}, AudioTracks: []MediaTrack{}, SubtitleTracks: []MediaTrack{}},
	}
	service := &Service{processor: processor, probes: newMediaProbeCache(time.Now)}
	asset := storedAsset{URL: "https://media.example/movie.mp4", Headers: map[string]string{"Authorization": "Bearer secret"}}

	const callers = 8
	results := make(chan error, callers)
	for range callers {
		go func() {
			_, err := service.probeMedia(context.Background(), asset)
			results <- err
		}()
	}
	deadline := time.Now().Add(time.Second)
	for processor.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(gate)
	for range callers {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if processor.calls.Load() != 1 {
		t.Fatalf("concurrent cache miss ran %d probes, want 1", processor.calls.Load())
	}
	if _, err := service.probeMedia(context.Background(), asset); err != nil {
		t.Fatal(err)
	}
	if processor.calls.Load() != 1 {
		t.Fatalf("cache hit ran another probe: %d", processor.calls.Load())
	}
}

func TestSourceReferenceIsSessionBoundClonedAndExpired(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	store := newSourceReferenceStore(func() time.Time { return now })
	profileID := "profile-id"
	principal := auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, SessionID: "session-id", ActiveProfileID: &profileID}
	reference, err := store.put(sourceReference{
		AuthSessionID: principal.SessionID, ProfileID: profileID, MediaType: "movie", ResourceID: "tt1234567",
		Source: Source{Name: "Original"}, Asset: &storedAsset{URL: "https://media.example/movie.mkv", Headers: map[string]string{"Authorization": "Bearer secret"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.get(reference.ID, principal)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Source.Name = "Changed"
	loaded.Asset.Headers["Authorization"] = "Changed"
	reloaded, err := store.get(reference.ID, principal)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Source.Name != "Original" || reloaded.Asset.Headers["Authorization"] != "Bearer secret" {
		t.Fatalf("stored reference was mutated through a clone: %+v", reloaded)
	}
	if _, err := store.get(reference.ID, auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, SessionID: "other-session", ActiveProfileID: &profileID}); err != ErrSourceReferenceExpired {
		t.Fatalf("cross-session reference lookup returned %v", err)
	}
	now = now.Add(sourceReferenceTTL)
	if _, err := store.get(reference.ID, principal); err != ErrSourceReferenceExpired || len(store.entries) != 0 {
		t.Fatalf("expired reference remained usable: err=%v entries=%d", err, len(store.entries))
	}
}

func TestPreparedPlaybackClonePreservesAndIsolatesSubtitleAssets(t *testing.T) {
	trackIndex := 7
	original := preparedPlayback{subtitleAssets: []storedAsset{{
		ID: "embedded-subtitle-7", URL: "https://media.example/movie.mkv",
		Headers: map[string]string{"Authorization": "Bearer secret"}, SubtitleTrackIndex: &trackIndex,
	}}}

	cloned := clonePreparedPlayback(original)
	if len(cloned.subtitleAssets) != 1 || cloned.subtitleAssets[0].ID != "embedded-subtitle-7" || cloned.subtitleAssets[0].SubtitleTrackIndex == nil || *cloned.subtitleAssets[0].SubtitleTrackIndex != 7 {
		t.Fatalf("subtitle asset was not preserved by clone: %+v", cloned.subtitleAssets)
	}
	cloned.subtitleAssets[0].Headers["Authorization"] = "Changed"
	*cloned.subtitleAssets[0].SubtitleTrackIndex = 8
	if original.subtitleAssets[0].Headers["Authorization"] != "Bearer secret" || *original.subtitleAssets[0].SubtitleTrackIndex != 7 {
		t.Fatalf("subtitle asset clone shares mutable state: %+v", original.subtitleAssets[0])
	}
}

func TestPreparationCacheIdentityIncludesEffectiveTranscodingPolicy(t *testing.T) {
	allowed := playbackPreparationCacheKey("source-reference", playbackPolicy{allowTranscoding: true, maximumHeight: 1080})
	disabled := playbackPreparationCacheKey("source-reference", playbackPolicy{allowTranscoding: false, maximumHeight: 1080})
	lowerResolution := playbackPreparationCacheKey("source-reference", playbackPolicy{allowTranscoding: true, maximumHeight: 720})
	if allowed == disabled || allowed == lowerResolution || disabled == lowerResolution {
		t.Fatalf("policy-sensitive cache identities collided: allowed=%q disabled=%q lower=%q", allowed, disabled, lowerResolution)
	}
}

func TestCloneCapabilitiesIsolatesAdditiveModeSlices(t *testing.T) {
	original := Capabilities{
		ProcessingModes:         []string{processingRemux, processingTranscode},
		SubtitleModes:           []string{"external", "burn"},
		MaximumVideoBitrateKbps: 8000, MaximumAudioChannels: 2, MaximumHeight: 1080, TranscodeVideoBitrateKbps: 12000,
	}
	cloned := cloneCapabilities(original)
	cloned.ProcessingModes[0] = processingTranscodeAudio
	cloned.SubtitleModes[0] = "burn"
	if original.ProcessingModes[0] != processingRemux || original.SubtitleModes[0] != "external" {
		t.Fatalf("capability clone shares additive slices: original=%+v clone=%+v", original, cloned)
	}
	if cloned.MaximumVideoBitrateKbps != 8000 || cloned.MaximumAudioChannels != 2 || cloned.MaximumHeight != 1080 || cloned.TranscodeVideoBitrateKbps != 12000 {
		t.Fatalf("capability clone lost additive limits: %+v", cloned)
	}
}
