package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
)

type preparationResourceFetcher struct {
	streamCalls       atomic.Int32
	txValidationCalls atomic.Int32
	subtitleCalls     atomic.Int32
	validationCalls   atomic.Int32
	validationErr     error
	validationAddonID string
	validationBatches [][]string
	subtitleBatch     addon.ResourceBatch
	subtitleGate      <-chan struct{}
	validation        func([]string) error
	sourceValidation  func(string) error
	streamResponse    func(int32) (addon.ResourceBatch, error)
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
	if fetcher.sourceValidation != nil {
		return fetcher.sourceValidation(addonID)
	}
	return fetcher.validationErr
}

func (fetcher *preparationResourceFetcher) ValidatePlaybackAccesses(_ context.Context, _ auth.Principal, addonIDs []string) error {
	return fetcher.validatePlaybackAccesses(addonIDs)
}

func (fetcher *preparationResourceFetcher) ValidatePlaybackAccessesTx(_ context.Context, _ pgx.Tx, _ auth.Principal, addonIDs []string) error {
	fetcher.txValidationCalls.Add(1)
	return fetcher.validatePlaybackAccesses(addonIDs)
}

func (fetcher *preparationResourceFetcher) validatePlaybackAccesses(addonIDs []string) error {
	fetcher.validationCalls.Add(1)
	batch := append([]string(nil), addonIDs...)
	fetcher.validationBatches = append(fetcher.validationBatches, batch)
	if len(batch) > 0 {
		fetcher.validationAddonID = batch[0]
	}
	if fetcher.validation != nil {
		return fetcher.validation(batch)
	}
	return fetcher.validationErr
}

func (fetcher *preparationResourceFetcher) FetchAllPlaybackResources(ctx context.Context, _ auth.Principal, resource addon.ResourcePath) (addon.ResourceBatch, error) {
	switch resource.Resource {
	case "stream":
		call := fetcher.streamCalls.Add(1)
		if fetcher.streamResponse != nil {
			return fetcher.streamResponse(call)
		}
		return addon.ResourceBatch{Results: []addon.ResourceResult{{
			AddonID: "addon-id", ManifestID: "manifest-id",
			Payload: []byte(`{"streams":[
				{"name":"First provider text","title":"First provider title","url":"https://media.example/first.mkv?signature=first-provider-url-secret","behaviorHints":{"filename":"first-provider-filename-secret"}},
				{"name":"Bearer provider-name-secret; Cookie=provider-name-cookie; X-Header=provider-name-header","title":"Bearer provider-title-secret; Cookie=provider-title-cookie; X-Header=provider-title-header","description":"provider-description-secret","url":"https://media.example/selected.mkv?signature=provider-signed-url-secret","behaviorHints":{"filename":"Bearer provider-filename-secret; Cookie=provider-filename-cookie; X-Header=provider-filename-header","proxyHeaders":{"request":{"Authorization":"Bearer provider-auth-header-secret","Cookie":"provider-header-cookie-secret","X-Provider-Key":"provider-header-key-secret"}}}}
			]}`),
		}}}, nil
	case "subtitles":
		fetcher.subtitleCalls.Add(1)
		if fetcher.subtitleGate != nil {
			select {
			case <-ctx.Done():
				return addon.ResourceBatch{}, ctx.Err()
			case <-fetcher.subtitleGate:
			}
		}
		return fetcher.subtitleBatch, nil
	default:
		return addon.ResourceBatch{}, nil
	}
}

type concurrentRefreshFetcher struct {
	calls      atomic.Int32
	firstReady chan struct{}
	release    chan struct{}
	first      addon.ResourceResult
	second     addon.ResourceResult
	firstErr   error
}

func (fetcher *concurrentRefreshFetcher) FetchPlaybackResource(ctx context.Context, _ auth.Principal, _ string, _ addon.ResourcePath) (addon.ResourceResult, error) {
	if fetcher.calls.Add(1) == 1 {
		close(fetcher.firstReady)
		select {
		case <-ctx.Done():
			return addon.ResourceResult{}, ctx.Err()
		case <-fetcher.release:
		}
		return fetcher.first, fetcher.firstErr
	}
	return fetcher.second, nil
}

func (fetcher *concurrentRefreshFetcher) FetchAllPlaybackResources(context.Context, auth.Principal, addon.ResourcePath) (addon.ResourceBatch, error) {
	return addon.ResourceBatch{}, nil
}

func (fetcher *concurrentRefreshFetcher) ValidatePlaybackAccess(context.Context, auth.Principal, string) error {
	return nil
}

func (fetcher *concurrentRefreshFetcher) ValidatePlaybackAccesses(context.Context, auth.Principal, []string) error {
	return nil
}

func (fetcher *concurrentRefreshFetcher) ValidatePlaybackAccessesTx(context.Context, pgx.Tx, auth.Principal, []string) error {
	return nil
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

	input := SourcesInput{
		MediaType: "movie", ResourceID: "tt1234567",
		Capabilities: Capabilities{
			StreamingProtocols: []string{"http"}, Containers: []string{"mp4"},
			VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
			ProcessingModes: []string{processingRemux},
		},
	}
	list, err := service.Sources(context.Background(), principal, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Sources) != 2 || processor.calls.Load() != 0 {
		t.Fatalf("listing sources unexpectedly inspected media: sources=%d probes=%d", len(list.Sources), processor.calls.Load())
	}
	if list.Sources[0].Name != "First provider text" || list.Sources[0].Description != "First provider title" || list.Sources[0].Filename != "first-provider-filename-secret" {
		t.Fatalf("first source display metadata was not preserved: %+v", list.Sources[0])
	}
	if list.Sources[1].Name != "Bearer provider-name-secret; Cookie=provider-name-cookie; X-Header=provider-name-header" || list.Sources[1].Description != "provider-description-secret" || list.Sources[1].Filename != "Bearer provider-filename-secret; Cookie=provider-filename-cookie; X-Header=provider-filename-header" {
		t.Fatalf("second source display metadata was not preserved: %+v", list.Sources[1])
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	formatted := fmt.Sprintf("%+v", list)
	for _, private := range []string{
		"media.example", "first-provider-url-secret", "provider-signed-url-secret",
		"provider-auth-header-secret", "provider-header-cookie-secret", "provider-header-key-secret",
	} {
		if strings.Contains(string(encoded), private) || strings.Contains(formatted, private) {
			t.Fatalf("source DTO or log formatting leaked private transport data %q: json=%s formatted=%s", private, encoded, formatted)
		}
	}
	for _, display := range []string{"First provider text", "First provider title", "provider-description-secret"} {
		if !strings.Contains(string(encoded), display) {
			t.Fatalf("source DTO omitted display metadata %q: %s", display, encoded)
		}
		if strings.Contains(formatted, display) {
			t.Fatalf("source log formatting exposed display metadata %q: %s", display, formatted)
		}
	}
	repeated, err := service.Sources(context.Background(), principal, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(repeated.Sources) != len(list.Sources) || repeated.Sources[0].Name != list.Sources[0].Name || repeated.Sources[1].Name != list.Sources[1].Name {
		t.Fatalf("source labels changed across equivalent listings: first=%+v repeated=%+v", list.Sources, repeated.Sources)
	}

	selected := list.Sources[1]
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
	cachedPlayback := service.preparations.entries[playbackPreparationCacheKey(selected.SourceRef, 1, playbackPolicy{})].playback
	service.preparations.mu.Unlock()
	if cachedPlayback.asset == nil {
		t.Fatal("prepared playback did not retain its stream asset")
	}
	if cachedPlayback.asset.Headers["Authorization"] != "Bearer provider-auth-header-secret" || cachedPlayback.asset.Headers["Cookie"] != "provider-header-cookie-secret" || cachedPlayback.asset.Headers["X-Provider-Key"] != "provider-header-key-secret" {
		t.Fatal("prepared playback lost private provider headers")
	}
	cachedDuration := cachedPlayback.asset.DurationSeconds
	if cachedDuration != 1320 {
		t.Fatalf("prepared playback asset duration = %v, want 1320", cachedDuration)
	}
	processor.mu.Lock()
	lastURL := processor.lastURL
	processor.mu.Unlock()
	if lastURL != "https://media.example/selected.mkv?signature=provider-signed-url-secret" {
		t.Fatalf("prepared the wrong provider source: %q", lastURL)
	}

	if _, err := service.Prepare(context.Background(), principal, PrepareInput{SourceRef: selected.SourceRef}); err != nil {
		t.Fatal(err)
	}
	if processor.calls.Load() != 1 || fetcher.streamCalls.Load() != 4 || fetcher.subtitleCalls.Load() != 1 || fetcher.validationCalls.Load() != 4 || fetcher.validationAddonID != "addon-id" {
		t.Fatalf("cached preparation skipped refresh or repeated probe work: probes=%d streams=%d subtitles=%d validations=%d addon=%q", processor.calls.Load(), fetcher.streamCalls.Load(), fetcher.subtitleCalls.Load(), fetcher.validationCalls.Load(), fetcher.validationAddonID)
	}
}

func TestResolveRefreshesSelectedProviderTransportAndFailsClosedWhenItDisappears(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	profileID := "profile-id"
	grantExpiresAt := now.Add(time.Hour)
	principal := auth.Principal{
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		SessionID: "auth-session-id", UserID: "user-id", DeviceID: "device-id",
		ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
	}
	oldURL := "https://media.example/old.mkv?token=old-url-secret"
	rotatedURL := "https://media.example/new.mp4?token=rotated-url-secret"
	missingURL := "https://media.example/missing.mp4?token=missing-url-secret"
	streamBatch := func(rawURL, authorization, filename string) addon.ResourceBatch {
		payload := fmt.Sprintf(`{"streams":[{"name":"Stable source","url":%q,"behaviorHints":{"filename":%q,"proxyHeaders":{"request":{"Authorization":%q}}}}]}`, rawURL, filename, authorization)
		return addon.ResourceBatch{Results: []addon.ResourceResult{{
			AddonID: "addon-id", ManifestID: "manifest-id", Payload: []byte(payload),
		}}}
	}
	fetcher := &preparationResourceFetcher{streamResponse: func(call int32) (addon.ResourceBatch, error) {
		switch call {
		case 1:
			return streamBatch(oldURL, "Bearer old-authorization-secret", "stable-source"), nil
		case 2:
			return streamBatch(rotatedURL, "Bearer rotated-authorization-secret", "stable-source"), nil
		default:
			return streamBatch(missingURL, "Bearer missing-authorization-secret", "different-source"), nil
		}
	}}
	processor := &countingProbeProcessor{inspection: MediaInspection{
		Container: "mp4", VideoTracks: []MediaTrack{{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720}},
		AudioTracks: []MediaTrack{{Index: 1, Type: "audio", Codec: "aac", Channels: 2}},
	}}
	sessionTransaction := &playbackTransactionStub{
		query: func(string, ...any) (pgx.Rows, error) { return emptyPlaybackRows{}, nil },
		row:   playbackSessionIDRow{id: "playback-session-id"},
	}
	transactionCalls := 0
	service := &Service{
		addons: fetcher, processor: processor, now: func() time.Time { return now },
		references: newSourceReferenceStore(func() time.Time { return now }),
		probes:     newMediaProbeCache(func() time.Time { return now }), preparations: newPlaybackPreparationCache(func() time.Time { return now }),
		hlsJobs: make(map[string]*hlsJob),
		profileTxFactory: func(context.Context, auth.Principal) (playbackProfileTransaction, error) {
			transactionCalls++
			if transactionCalls == 4 {
				return sessionTransaction, nil
			}
			return testPlaybackProfileTransaction{}, nil
		},
	}
	listed, err := service.Sources(context.Background(), principal, SourcesInput{
		MediaType: "movie", ResourceID: "resource-id",
		Capabilities: Capabilities{
			StreamingProtocols: []string{"http"}, Containers: []string{"mkv", "mp4"},
			VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
		},
	})
	if err != nil || len(listed.Sources) != 1 {
		t.Fatalf("list source: sources=%d err=%v", len(listed.Sources), err)
	}
	option := listed.Sources[0]
	staleReference, err := service.references.get(option.SourceRef, principal)
	if err != nil || staleReference.Asset == nil {
		t.Fatalf("load listed source capability: asset=%+v err=%v", staleReference.Asset, err)
	}
	staleAsset := cloneStoredAsset(*staleReference.Asset)
	stalePlayback := preparedPlayback{source: cloneSource(staleReference.Source), asset: &staleAsset, addonIDs: []string{"addon-id"}}
	service.preparations.entries[playbackPreparationCacheKey(option.SourceRef, staleReference.TransportRevision, playbackPolicy{})] = playbackPreparationEntry{
		playback: stalePlayback, expiresAt: staleReference.ExpiresAt,
	}
	staleProbeKey := mediaProbeKey(staleAsset)
	service.probes.entries[staleProbeKey] = mediaProbeCacheEntry{inspection: MediaInspection{Container: "stale"}, expiresAt: now.Add(time.Hour)}
	if _, err := service.Resolve(context.Background(), principal, ResolveInput{SourceRef: option.SourceRef}); err != nil {
		t.Fatalf("resolve refreshed source: %v", err)
	}
	refreshed, err := service.references.get(option.SourceRef, principal)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Owner.UserID != principal.UserID || refreshed.Owner.DeviceID != principal.DeviceID || refreshed.Source.ID != option.ID ||
		refreshed.Source.URL != rotatedURL || refreshed.Source.Container != "mp4" || refreshed.Asset == nil ||
		refreshed.Asset.URL != rotatedURL || refreshed.Asset.Headers["Authorization"] != "Bearer rotated-authorization-secret" {
		t.Fatalf("refreshed capability changed owner or retained stale transport: %+v asset=%+v", refreshed.Source, refreshed.Asset)
	}
	processor.mu.Lock()
	probedURL := processor.lastURL
	processor.mu.Unlock()
	if probedURL != rotatedURL {
		t.Fatalf("probe used stale provider URL %q", probedURL)
	}
	if _, exists := service.probes.entries[staleProbeKey]; exists {
		t.Fatal("rotated source retained stale probe cache entry")
	}
	_, err = service.Resolve(context.Background(), principal, ResolveInput{SourceRef: option.SourceRef})
	if err != ErrSourceReferenceExpired {
		t.Fatalf("disappeared selection error = %v, want opaque expiry", err)
	}
	if _, lookupErr := service.references.get(option.SourceRef, principal); lookupErr != ErrSourceReferenceExpired {
		t.Fatalf("disappeared selection capability remained stored: %v", lookupErr)
	}
	for _, secret := range []string{oldURL, rotatedURL, missingURL, "missing-url-secret"} {
		if strings.Contains(fmt.Sprint(err), secret) {
			t.Fatalf("disappearance error leaked provider transport %q: %v", secret, err)
		}
	}
}

func TestConcurrentRefreshCASCannotEvictOrCacheOverNewerTransport(t *testing.T) {
	now := time.Date(2026, time.August, 10, 14, 0, 0, 0, time.UTC)
	profileID := "profile-id"
	principal := auth.Principal{
		SessionID: "auth-session-id", UserID: "user-id", DeviceID: "device-id", ActiveProfileID: &profileID,
	}
	resource := func(rawURL, authorization string) addon.ResourceResult {
		payload := fmt.Sprintf(`{"streams":[{"name":"Stable source","url":%q,"behaviorHints":{"filename":"stable-source.mp4","proxyHeaders":{"request":{"Authorization":%q}}}}]}`, rawURL, authorization)
		return addon.ResourceResult{AddonID: "addon-id", ManifestID: "manifest-id", Payload: []byte(payload)}
	}
	oldResult := resource("https://media.example/old.mp4?token=old", "Bearer old")
	firstResult := resource("https://media.example/u1.mp4?token=u1", "Bearer u1")
	secondResult := resource("https://media.example/u2.mp4?token=u2", "Bearer u2")
	capabilities := Capabilities{StreamingProtocols: []string{"http"}, Containers: []string{"mp4"}}
	sources, assets, err := normalizeStreams(addon.ResourceBatch{Results: []addon.ResourceResult{oldResult}}, capabilities)
	if err != nil || len(sources) != 1 || len(assets) != 1 {
		t.Fatalf("normalize initial selection: sources=%d assets=%d err=%v", len(sources), len(assets), err)
	}
	oldAsset := cloneStoredAsset(assets[0])
	store := newSourceReferenceStore(func() time.Time { return now })
	initial, err := store.put(principal, sourceReference{
		MediaType: "movie", AddonMediaType: "movie", ResourceID: "resource-id",
		Source: sources[0], Asset: &oldAsset, Capabilities: capabilities,
	})
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &concurrentRefreshFetcher{
		firstReady: make(chan struct{}), release: make(chan struct{}), first: firstResult, second: secondResult,
	}
	service := &Service{
		addons: fetcher, now: func() time.Time { return now }, references: store,
		preparations: newPlaybackPreparationCache(func() time.Time { return now }),
		probes:       newMediaProbeCache(func() time.Time { return now }),
	}
	type refreshResult struct {
		reference sourceReference
		err       error
	}
	firstDone := make(chan refreshResult, 1)
	go func() {
		reference, refreshErr := service.refreshSourceReference(context.Background(), principal, initial)
		firstDone <- refreshResult{reference: reference, err: refreshErr}
	}()
	select {
	case <-fetcher.firstReady:
	case <-time.After(time.Second):
		t.Fatal("first refresh did not reach its provider fetch")
	}

	oldKey := playbackPreparationCacheKey(initial.ID, initial.TransportRevision, playbackPolicy{})
	staleCall := &playbackPreparationCall{done: make(chan struct{})}
	service.preparations.inFlight[oldKey] = staleCall
	newer, err := service.refreshSourceReference(context.Background(), principal, initial)
	if err != nil {
		t.Fatalf("newer refresh: %v", err)
	}
	if newer.SelectionRevision != initial.SelectionRevision+1 || newer.TransportRevision != initial.TransportRevision+1 || newer.Asset == nil || newer.Asset.URL != "https://media.example/u2.mp4?token=u2" {
		t.Fatalf("newer refresh did not install U2: selectionRevision=%d transportRevision=%d asset=%+v", newer.SelectionRevision, newer.TransportRevision, newer.Asset)
	}
	newerAsset := cloneStoredAsset(*newer.Asset)
	newerPlayback := preparedPlayback{source: cloneSource(newer.Source), asset: &newerAsset}
	newerKey := playbackPreparationCacheKey(newer.ID, newer.TransportRevision, playbackPolicy{})
	service.preparations.entries[newerKey] = playbackPreparationEntry{playback: newerPlayback, expiresAt: newer.ExpiresAt}
	newerProbeKey := mediaProbeKey(newerAsset)
	service.probes.entries[newerProbeKey] = mediaProbeCacheEntry{inspection: MediaInspection{Container: "u2"}, expiresAt: newer.ExpiresAt}

	close(fetcher.release)
	var first refreshResult
	select {
	case first = <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first refresh did not finish after release")
	}
	if first.err != nil || first.reference.SelectionRevision != newer.SelectionRevision || first.reference.TransportRevision != newer.TransportRevision || first.reference.Asset == nil || first.reference.Asset.URL != newerAsset.URL || first.reference.Asset.Headers["Authorization"] != "Bearer u2" {
		t.Fatalf("lost CAS did not converge on U2: reference=%+v asset=%+v err=%v", first.reference, first.reference.Asset, first.err)
	}
	staleAsset := cloneStoredAsset(oldAsset)
	staleAsset.URL = "https://media.example/u1.mp4?token=u1"
	staleAsset.Headers = map[string]string{"Authorization": "Bearer u1"}
	staleSource := cloneSource(initial.Source)
	staleSource.URL = staleAsset.URL
	service.preparations.complete(oldKey, staleCall, service.preparations.generation, preparedPlayback{
		source: staleSource, asset: &staleAsset,
	}, nil, initial.ExpiresAt)

	service.preparations.mu.Lock()
	cachedNewer, newerCached := service.preparations.entries[newerKey]
	_, staleCached := service.preparations.entries[oldKey]
	service.preparations.mu.Unlock()
	if !newerCached || cachedNewer.playback.asset == nil || cachedNewer.playback.asset.URL != newerAsset.URL || cachedNewer.playback.asset.Headers["Authorization"] != "Bearer u2" || staleCached {
		t.Fatalf("stale completion changed revisioned cache: newer=%+v newerCached=%v staleCached=%v", cachedNewer.playback.asset, newerCached, staleCached)
	}
	service.probes.mu.Lock()
	_, newerProbeCached := service.probes.entries[newerProbeKey]
	service.probes.mu.Unlock()
	if !newerProbeCached {
		t.Fatal("lost CAS evicted U2 probe cache entry")
	}
	stored, err := store.get(initial.ID, principal)
	if err != nil || stored.SelectionRevision != newer.SelectionRevision || stored.TransportRevision != newer.TransportRevision || stored.Asset == nil || stored.Asset.URL != newerAsset.URL || stored.Asset.Headers["Authorization"] != "Bearer u2" {
		t.Fatalf("lost CAS restored U1 in the store: selectionRevision=%d transportRevision=%d asset=%+v err=%v", stored.SelectionRevision, stored.TransportRevision, stored.Asset, err)
	}

	fetcher.calls.Store(0)
	fetcher.firstReady = make(chan struct{})
	fetcher.release = make(chan struct{})
	fetcher.firstErr = addon.ErrNotFound
	terminalDone := make(chan refreshResult, 1)
	go func() {
		reference, refreshErr := service.refreshSourceReference(context.Background(), principal, newer)
		terminalDone <- refreshResult{reference: reference, err: refreshErr}
	}()
	select {
	case <-fetcher.firstReady:
	case <-time.After(time.Second):
		t.Fatal("terminal refresh did not reach its provider fetch")
	}
	latest, err := service.refreshSourceReference(context.Background(), principal, newer)
	if err != nil || latest.SelectionRevision != newer.SelectionRevision+1 || latest.TransportRevision != newer.TransportRevision {
		t.Fatalf("same-transport winner: selectionRevision=%d transportRevision=%d err=%v", latest.SelectionRevision, latest.TransportRevision, err)
	}
	close(fetcher.release)
	var terminal refreshResult
	select {
	case terminal = <-terminalDone:
	case <-time.After(time.Second):
		t.Fatal("terminal refresh did not finish after release")
	}
	if terminal.err != nil || terminal.reference.SelectionRevision != latest.SelectionRevision || terminal.reference.TransportRevision != latest.TransportRevision || terminal.reference.Asset == nil || terminal.reference.Asset.URL != newerAsset.URL {
		t.Fatalf("stale terminal refresh did not converge: reference=%+v asset=%+v err=%v", terminal.reference, terminal.reference.Asset, terminal.err)
	}
	service.preparations.mu.Lock()
	_, newerCached = service.preparations.entries[newerKey]
	service.preparations.mu.Unlock()
	service.probes.mu.Lock()
	_, newerProbeCached = service.probes.entries[newerProbeKey]
	service.probes.mu.Unlock()
	if !newerCached || !newerProbeCached {
		t.Fatalf("stale terminal refresh evicted U2 caches: preparation=%v probe=%v", newerCached, newerProbeCached)
	}
}

func TestPrepareAndResolveRejectRevokedSourceBeforePreparation(t *testing.T) {
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
			processor := &countingProbeProcessor{inspection: MediaInspection{Container: "mp4"}}
			references := newSourceReferenceStore(func() time.Time { return now })
			reference, err := references.put(principal, sourceReference{
				MediaType: "movie", AddonMediaType: "movie", ResourceID: "tt1234567",
				Source: Source{ID: "source-id", AddonID: "addon-id", ManifestID: "manifest-id", Mode: "direct", URL: "https://media.example/movie.mp4", Protocol: "http", Container: "mp4", Compatible: true},
				Asset:  &storedAsset{ID: "source-id", Kind: "stream", URL: "https://media.example/movie.mp4", Container: "mp4"},
			})
			if err != nil {
				t.Fatal(err)
			}
			authorizationCalls := 0
			preparations := newPlaybackPreparationCache(func() time.Time { return now })
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
				t.Fatalf("revoked source error = %v, want opaque expiry", err)
			}
			if fetcher.validationCalls.Load() != 1 || fetcher.validationAddonID != "addon-id" || fetcher.subtitleCalls.Load() != 0 || processor.calls.Load() != 0 {
				t.Fatalf("revoked source reached preparation: validations=%d addon=%q subtitles=%d probes=%d", fetcher.validationCalls.Load(), fetcher.validationAddonID, fetcher.subtitleCalls.Load(), processor.calls.Load())
			}
			if authorizationCalls != 1 || len(service.hlsJobs) != 0 || len(preparations.entries) != 0 {
				t.Fatalf("revoked source crossed preparation boundary: authorizations=%d hlsJobs=%d cacheEntries=%d", authorizationCalls, len(service.hlsJobs), len(preparations.entries))
			}
		})
	}
}

func TestCachedSubtitleProviderRevocationBlocksResolveBeforeSessionCreation(t *testing.T) {
	now := time.Date(2026, time.August, 6, 13, 0, 0, 0, time.UTC)
	profileID := "profile-id"
	grantExpiresAt := now.Add(time.Hour)
	principal := auth.Principal{
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		SessionID: "auth-session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
	}
	revoked := false
	fetcher := &preparationResourceFetcher{
		subtitleBatch: addon.ResourceBatch{Results: []addon.ResourceResult{{
			AddonID: "subtitle-addon", ManifestID: "subtitle-manifest",
			Payload: []byte(`{"subtitles":[{"id":"english","url":"https://subtitles.example/english.srt","lang":"en"}]}`),
		}}},
		streamResponse: func(int32) (addon.ResourceBatch, error) {
			return addon.ResourceBatch{Results: []addon.ResourceResult{{
				AddonID: "source-addon", ManifestID: "source-manifest",
				Payload: []byte(`{"streams":[{"url":"https://media.example/movie.mp4"}]}`),
			}}}, nil
		},
		validation: func(addonIDs []string) error {
			if revoked && strings.Join(addonIDs, ",") == "source-addon,subtitle-addon" {
				return addon.ErrNotFound
			}
			return nil
		},
	}
	processor := &countingProbeProcessor{inspection: MediaInspection{
		Container: "mp4", VideoTracks: []MediaTrack{{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080}},
		AudioTracks: []MediaTrack{{Index: 1, Type: "audio", Codec: "aac", Channels: 2}},
	}}
	references := newSourceReferenceStore(func() time.Time { return now })
	reference, err := references.put(principal, sourceReference{
		MediaType: "movie", AddonMediaType: "movie", ResourceID: "resource-id",
		Source:       Source{ID: "source-id", AddonID: "source-addon", ManifestID: "source-manifest", Mode: "direct", URL: "https://media.example/movie.mp4", Protocol: "http", Container: "mp4", Compatible: true},
		Asset:        &storedAsset{ID: "source-id", Kind: "stream", URL: "https://media.example/movie.mp4", Container: "mp4"},
		Capabilities: Capabilities{StreamingProtocols: []string{"http"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SubtitleModes: []string{"external"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationCalls := 0
	preparations := newPlaybackPreparationCache(func() time.Time { return now })
	service := &Service{
		addons: fetcher, processor: processor, now: func() time.Time { return now }, references: references,
		probes: newMediaProbeCache(func() time.Time { return now }), preparations: preparations, hlsJobs: make(map[string]*hlsJob),
		profileTxFactory: func(context.Context, auth.Principal) (playbackProfileTransaction, error) {
			authorizationCalls++
			return testPlaybackProfileTransaction{}, nil
		},
	}
	if _, err := service.Prepare(context.Background(), principal, PrepareInput{SourceRef: reference.ID}); err != nil {
		t.Fatalf("prime prepared playback cache: %v", err)
	}
	if len(fetcher.validationBatches) != 1 || strings.Join(fetcher.validationBatches[0], ",") != "source-addon,subtitle-addon" {
		t.Fatalf("prepared provider provenance = %#v", fetcher.validationBatches)
	}
	revoked = true
	if _, err := service.Resolve(context.Background(), principal, ResolveInput{SourceRef: reference.ID}); err != ErrSourceReferenceExpired {
		t.Fatalf("resolve after subtitle provider revocation = %v, want opaque expiry", err)
	}
	if processor.calls.Load() != 1 || fetcher.subtitleCalls.Load() != 1 {
		t.Fatalf("cached resolve repeated preparation work: probes=%d subtitles=%d", processor.calls.Load(), fetcher.subtitleCalls.Load())
	}
	if authorizationCalls != 4 {
		t.Fatalf("revoked cached subtitle authorization transactions=%d, want 4 including denied session transaction", authorizationCalls)
	}
	if len(preparations.entries) != 0 {
		t.Fatalf("revoked prepared playback remained cached: %d entries", len(preparations.entries))
	}
}

func TestLateSourceRevocationBlocksPreparedPlaybackAtFinalBoundary(t *testing.T) {
	now := time.Date(2026, time.August, 6, 14, 0, 0, 0, time.UTC)
	profileID := "profile-id"
	grantExpiresAt := now.Add(time.Hour)
	principal := auth.Principal{
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		SessionID: "auth-session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
	}
	tests := []struct {
		name                   string
		wantAuthorizationCalls int
		invoke                 func(*Service, string) error
	}{
		{name: "prepare", wantAuthorizationCalls: 2, invoke: func(service *Service, sourceRef string) error {
			_, err := service.Prepare(context.Background(), principal, PrepareInput{SourceRef: sourceRef})
			return err
		}},
		{name: "resolve", wantAuthorizationCalls: 2, invoke: func(service *Service, sourceRef string) error {
			_, err := service.Resolve(context.Background(), principal, ResolveInput{SourceRef: sourceRef})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fetcher := &preparationResourceFetcher{
				validationErr:    addon.ErrNotFound,
				sourceValidation: func(string) error { return nil },
			}
			processor := &countingProbeProcessor{inspection: MediaInspection{
				Container: "mp4", VideoTracks: []MediaTrack{{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720}},
				AudioTracks: []MediaTrack{{Index: 1, Type: "audio", Codec: "aac", Channels: 2}},
			}}
			references := newSourceReferenceStore(func() time.Time { return now })
			reference, err := references.put(principal, sourceReference{
				MediaType: "movie", AddonMediaType: "movie", ResourceID: "resource-id",
				Source:       Source{ID: "source-id", AddonID: "addon-id", ManifestID: "manifest-id", Mode: "direct", URL: "https://media.example/movie.mp4", Protocol: "http", Container: "mp4", Compatible: true},
				Asset:        &storedAsset{ID: "source-id", Kind: "stream", URL: "https://media.example/movie.mp4", Container: "mp4"},
				Capabilities: Capabilities{StreamingProtocols: []string{"http"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			authorizationCalls := 0
			preparations := newPlaybackPreparationCache(func() time.Time { return now })
			service := &Service{
				addons: fetcher, processor: processor, now: func() time.Time { return now }, references: references,
				probes: newMediaProbeCache(func() time.Time { return now }), preparations: preparations, hlsJobs: make(map[string]*hlsJob),
				profileTxFactory: func(context.Context, auth.Principal) (playbackProfileTransaction, error) {
					authorizationCalls++
					return testPlaybackProfileTransaction{}, nil
				},
			}
			if err := test.invoke(service, reference.ID); err != ErrSourceReferenceExpired {
				t.Fatalf("late source revocation error = %v, want opaque expiry", err)
			}
			if processor.calls.Load() != 1 || fetcher.subtitleCalls.Load() != 1 || fetcher.validationCalls.Load() != 2 {
				t.Fatalf("source validation boundaries = probes=%d subtitles=%d validations=%d, want completed preparation then final denial", processor.calls.Load(), fetcher.subtitleCalls.Load(), fetcher.validationCalls.Load())
			}
			if authorizationCalls != test.wantAuthorizationCalls {
				t.Fatalf("late source revocation crossed session boundary: authorization transactions=%d want=%d", authorizationCalls, test.wantAuthorizationCalls)
			}
			if len(preparations.entries) != 0 {
				t.Fatalf("revoked preparation remained cached: %d entries", len(preparations.entries))
			}
		})
	}
}

func TestPrepareAllowsPlaybackWithoutAddonProvenance(t *testing.T) {
	now := time.Date(2026, time.August, 6, 15, 0, 0, 0, time.UTC)
	profileID := "profile-id"
	grantExpiresAt := now.Add(time.Hour)
	principal := auth.Principal{
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		SessionID: "auth-session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
	}
	references := newSourceReferenceStore(func() time.Time { return now })
	reference, err := references.put(principal, sourceReference{
		MediaType: "movie", ResourceID: "local-resource",
		Source:       Source{ID: "local-source", Mode: "youtube", YTID: "local-video", Protocol: "youtube", Compatible: true},
		Capabilities: Capabilities{StreamingProtocols: []string{"youtube"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &preparationResourceFetcher{}
	service := &Service{
		addons: fetcher, now: func() time.Time { return now }, references: references,
		probes: newMediaProbeCache(func() time.Time { return now }), preparations: newPlaybackPreparationCache(func() time.Time { return now }),
		hlsJobs: make(map[string]*hlsJob), profileTxFactory: testPlaybackProfileTxFactory,
	}
	prepared, err := service.Prepare(context.Background(), principal, PrepareInput{SourceRef: reference.ID})
	if err != nil {
		t.Fatalf("prepare local playback: %v", err)
	}
	if prepared.Mode != "youtube" || fetcher.streamCalls.Load() != 0 || len(fetcher.validationBatches) != 1 || len(fetcher.validationBatches[0]) != 0 {
		t.Fatalf("local preparation refetched provider or changed validation: preparation=%+v streamCalls=%d batches=%#v", prepared, fetcher.streamCalls.Load(), fetcher.validationBatches)
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

func TestBuildPreparedPlaybackSkipsExternalSubtitleFetchForBurnOnlyClient(t *testing.T) {
	blocked := make(chan struct{})
	fetcher := &preparationResourceFetcher{subtitleGate: blocked}
	service := &Service{addons: fetcher}
	reference := sourceReference{
		AddonMediaType: "movie", ResourceID: "tt1234567",
		Source: Source{
			ID: "stream-1", Mode: "direct", URL: "https://media.example/movie.mkv", Compatible: true,
			Media: &MediaInspection{SubtitleTracks: []MediaTrack{{Index: 2, Type: "subtitle", Codec: "subrip", Language: "fr"}}},
		},
		Asset:          &storedAsset{ID: "stream-1", Kind: "stream", URL: "https://media.example/movie.mkv"},
		Capabilities:   Capabilities{SubtitleModes: []string{"burn"}},
		ProviderErrors: []ProviderFailure{{AddonID: "stream-addon", Code: "stream_warning", Message: "partial stream failure"}},
	}

	done := make(chan struct{})
	var playback preparedPlayback
	var err error
	go func() {
		playback, err = service.buildPreparedPlayback(context.Background(), auth.Principal{}, reference)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		close(blocked)
		t.Fatal("burn-only playback waited for the blocked external subtitle provider")
	}
	if err != nil {
		t.Fatal(err)
	}
	if calls := fetcher.subtitleCalls.Load(); calls != 0 {
		t.Fatalf("external subtitle fetch calls = %d, want 0", calls)
	}
	if len(playback.subtitles) != 1 || playback.subtitles[0].Delivery != "burn" || playback.subtitles[0].Language != "fr" {
		t.Fatalf("embedded burn subtitle was not inspected: %+v", playback.subtitles)
	}
	if len(playback.providerErrors) != 1 || playback.providerErrors[0].Code != "stream_warning" {
		t.Fatalf("relevant provider errors were not preserved: %+v", playback.providerErrors)
	}
}

func TestBuildPreparedPlaybackFetchesExternalSubtitlesForNegotiatedAndLegacyClients(t *testing.T) {
	for _, modes := range [][]string{nil, []string{"external"}, []string{"external", "burn"}} {
		fetcher := &preparationResourceFetcher{}
		service := &Service{addons: fetcher}
		_, err := service.buildPreparedPlayback(context.Background(), auth.Principal{}, sourceReference{
			AddonMediaType: "movie", ResourceID: "tt1234567",
			Source:       Source{ID: "stream-1", Mode: "direct", URL: "https://media.example/movie.mkv", Compatible: true},
			Asset:        &storedAsset{ID: "stream-1", Kind: "stream", URL: "https://media.example/movie.mkv"},
			Capabilities: Capabilities{SubtitleModes: modes},
		})
		if err != nil {
			t.Fatalf("subtitle modes %v: %v", modes, err)
		}
		if calls := fetcher.subtitleCalls.Load(); calls != 1 {
			t.Fatalf("subtitle modes %v fetch calls = %d, want 1", modes, calls)
		}
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
	reference, err := store.put(principal, sourceReference{
		MediaType: "movie", ResourceID: "tt1234567",
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

func TestPreparedPlaybackClonePreservesAndIsolatesProviderData(t *testing.T) {
	trackIndex := 7
	original := preparedPlayback{
		addonIDs: []string{"source-addon", "subtitle-addon"},
		subtitleAssets: []storedAsset{{
			ID: "embedded-subtitle-7", URL: "https://media.example/movie.mkv",
			Headers: map[string]string{"Authorization": "Bearer secret"}, SubtitleTrackIndex: &trackIndex,
		}},
	}

	cloned := clonePreparedPlayback(original)
	if len(cloned.subtitleAssets) != 1 || cloned.subtitleAssets[0].ID != "embedded-subtitle-7" || cloned.subtitleAssets[0].SubtitleTrackIndex == nil || *cloned.subtitleAssets[0].SubtitleTrackIndex != 7 {
		t.Fatalf("subtitle asset was not preserved by clone: %+v", cloned.subtitleAssets)
	}
	if strings.Join(cloned.addonIDs, ",") != "source-addon,subtitle-addon" {
		t.Fatalf("provider provenance was not preserved by clone: %#v", cloned.addonIDs)
	}
	cloned.subtitleAssets[0].Headers["Authorization"] = "Changed"
	*cloned.subtitleAssets[0].SubtitleTrackIndex = 8
	cloned.addonIDs[0] = "changed-addon"
	if original.subtitleAssets[0].Headers["Authorization"] != "Bearer secret" || *original.subtitleAssets[0].SubtitleTrackIndex != 7 || original.addonIDs[0] != "source-addon" {
		t.Fatalf("prepared playback clone shares mutable state: %+v", original)
	}
}

func TestPreparedPlaybackAddonIDsAreDistinctAndOmitLocalAssets(t *testing.T) {
	addonIDs := preparedPlaybackAddonIDs(Source{AddonID: " source-addon "}, []Subtitle{
		{AddonID: ""}, {AddonID: "source-addon"}, {AddonID: "subtitle-addon"}, {AddonID: " subtitle-addon "},
	})
	if strings.Join(addonIDs, ",") != "source-addon,subtitle-addon" {
		t.Fatalf("prepared playback provider IDs = %#v", addonIDs)
	}
}

func TestPreparationCacheIdentityIncludesTransportRevisionAndPolicy(t *testing.T) {
	allowed := playbackPreparationCacheKey("source-reference", 1, playbackPolicy{allowTranscoding: true, maximumHeight: 1080})
	disabled := playbackPreparationCacheKey("source-reference", 1, playbackPolicy{allowTranscoding: false, maximumHeight: 1080})
	lowerResolution := playbackPreparationCacheKey("source-reference", 1, playbackPolicy{allowTranscoding: true, maximumHeight: 720})
	newerRevision := playbackPreparationCacheKey("source-reference", 2, playbackPolicy{allowTranscoding: true, maximumHeight: 1080})
	if allowed == disabled || allowed == lowerResolution || allowed == newerRevision || disabled == lowerResolution {
		t.Fatalf("revision/policy-sensitive cache identities collided: allowed=%q disabled=%q lower=%q newer=%q", allowed, disabled, lowerResolution, newerRevision)
	}
}

func TestCloneCapabilitiesIsolatesAdditiveModeAndContainerProfileSlices(t *testing.T) {
	original := Capabilities{
		ProcessingModes:         []string{processingRemux, processingTranscode},
		SubtitleModes:           []string{"external", "burn"},
		ContainerProfiles:       []ContainerProfile{{ContainersCSV: "mp4", Conditions: []ProfileCondition{{Condition: "lessthanequal", Property: "height", Value: "1080"}}}},
		HLSSegmentContainer:     "mp4",
		MaximumVideoBitrateKbps: 8000, MaximumAudioChannels: 2, MaximumHeight: 1080, TranscodeVideoBitrateKbps: 12000,
	}
	cloned := cloneCapabilities(original)
	cloned.ProcessingModes[0] = processingTranscodeAudio
	cloned.SubtitleModes[0] = "burn"
	cloned.ContainerProfiles[0].ContainersCSV = "webm"
	cloned.ContainerProfiles[0].Conditions[0].Value = "1"
	if original.ProcessingModes[0] != processingRemux || original.SubtitleModes[0] != "external" ||
		original.ContainerProfiles[0].ContainersCSV != "mp4" || original.ContainerProfiles[0].Conditions[0].Value != "1080" {
		t.Fatalf("capability clone shares additive slices: original=%+v clone=%+v", original, cloned)
	}
	if cloned.HLSSegmentContainer != "mp4" || cloned.MaximumVideoBitrateKbps != 8000 || cloned.MaximumAudioChannels != 2 || cloned.MaximumHeight != 1080 || cloned.TranscodeVideoBitrateKbps != 12000 {
		t.Fatalf("capability clone lost additive limits: %+v", cloned)
	}
}

func TestPlaybackPreparationCacheBoundEvictsDeterministically(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	cache := newPlaybackPreparationCache(func() time.Time { return now })
	expiresAt := now.Add(time.Hour)
	for index := range maximumPlaybackPreparations {
		key := fmt.Sprintf("reference-%04d", index)
		call := &playbackPreparationCall{done: make(chan struct{})}
		cache.inFlight[key] = call
		cache.complete(key, call, cache.generation, preparedPlayback{}, nil, expiresAt)
	}
	newKey := "reference-new"
	call := &playbackPreparationCall{done: make(chan struct{})}
	cache.inFlight[newKey] = call
	cache.complete(newKey, call, cache.generation, preparedPlayback{}, nil, expiresAt)
	if len(cache.entries) != maximumPlaybackPreparations {
		t.Fatalf("preparation cache size = %d, want %d", len(cache.entries), maximumPlaybackPreparations)
	}
	if _, exists := cache.entries["reference-0000"]; exists {
		t.Fatal("deterministic earliest preparation survived overflow")
	}
	if _, exists := cache.entries[newKey]; !exists {
		t.Fatal("new preparation was not admitted after deterministic eviction")
	}
}

func TestPlaybackPreparationCacheClearDetachesInFlightGeneration(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	cache := newPlaybackPreparationCache(func() time.Time { return now })
	key := "reference|true|1080"
	oldCall := &playbackPreparationCall{done: make(chan struct{})}
	oldGeneration := cache.generation
	cache.inFlight[key] = oldCall

	cache.clear()
	newCall := &playbackPreparationCall{done: make(chan struct{})}
	newGeneration := cache.generation
	cache.inFlight[key] = newCall
	cache.complete(key, oldCall, oldGeneration, preparedPlayback{}, nil, now.Add(time.Hour))
	if cache.inFlight[key] != newCall {
		t.Fatal("detached old preparation removed the replacement in-flight call")
	}
	if len(cache.entries) != 0 {
		t.Fatal("detached old preparation repopulated a cleared cache")
	}
	select {
	case <-oldCall.done:
	default:
		t.Fatal("detached old preparation did not notify its original waiter")
	}

	cache.complete(key, newCall, newGeneration, preparedPlayback{}, nil, now.Add(time.Hour))
	if len(cache.inFlight) != 0 || len(cache.entries) != 1 {
		t.Fatalf("replacement completion state: inFlight=%d entries=%d", len(cache.inFlight), len(cache.entries))
	}
}
