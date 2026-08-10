package playback

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
)

const (
	playbackPreparationTimeout  = 30 * time.Second
	maximumPlaybackPreparations = 512
)

type preparedPlayback struct {
	source         Source
	asset          *storedAsset
	subtitles      []Subtitle
	subtitleAssets []storedAsset
	providerErrors []ProviderFailure
	addonIDs       []string
}

type playbackPolicy struct {
	allowTranscoding bool
	maximumHeight    int
	bitrateKbps      int
}

type playbackPreparationEntry struct {
	playback  preparedPlayback
	expiresAt time.Time
}

type playbackPreparationCall struct {
	done     chan struct{}
	playback preparedPlayback
	err      error
}

type playbackPreparationCache struct {
	mu         sync.Mutex
	entries    map[string]playbackPreparationEntry
	inFlight   map[string]*playbackPreparationCall
	generation uint64
	now        func() time.Time
}

func newPlaybackPreparationCache(now func() time.Time) *playbackPreparationCache {
	return &playbackPreparationCache{
		entries: make(map[string]playbackPreparationEntry), inFlight: make(map[string]*playbackPreparationCall), now: now,
	}
}

func (cache *playbackPreparationCache) clear() {
	cache.mu.Lock()
	cache.generation++
	clear(cache.entries)
	cache.inFlight = make(map[string]*playbackPreparationCall)
	cache.mu.Unlock()
}

func (cache *playbackPreparationCache) evict(reference sourceReference, policy playbackPolicy) {
	cacheKey := playbackPreparationCacheKey(reference.ID, reference.TransportRevision, policy)
	cache.mu.Lock()
	delete(cache.entries, cacheKey)
	delete(cache.inFlight, cacheKey)
	cache.mu.Unlock()
}

func (cache *playbackPreparationCache) evictRevision(referenceID string, revision uint64) {
	prefix := referenceID + "|" + strconv.FormatUint(revision, 10) + "|"
	cache.mu.Lock()
	for key := range cache.entries {
		if strings.HasPrefix(key, prefix) {
			delete(cache.entries, key)
		}
	}
	for key := range cache.inFlight {
		if strings.HasPrefix(key, prefix) {
			delete(cache.inFlight, key)
		}
	}
	cache.mu.Unlock()
}

func playbackPreparationCacheKey(referenceID string, revision uint64, policy playbackPolicy) string {
	return referenceID + "|" + strconv.FormatUint(revision, 10) + "|" + strconv.FormatBool(policy.allowTranscoding) + "|" + strconv.Itoa(policy.maximumHeight) + "|" + strconv.Itoa(policy.bitrateKbps)
}

func (service *Service) preparedPlayback(ctx context.Context, principal auth.Principal, reference sourceReference, policies ...playbackPolicy) (preparedPlayback, error) {
	policy := playbackPolicy{allowTranscoding: true, maximumHeight: reference.Capabilities.MaximumHeight, bitrateKbps: service.mediaOptions.TranscodeVideoBitrateKbps}
	if len(policies) > 0 {
		policy = policies[0]
	}
	cacheKey := playbackPreparationCacheKey(reference.ID, reference.TransportRevision, policy)
	cache := service.preparations
	cache.mu.Lock()
	cache.removeExpiredLocked()
	if entry, exists := cache.entries[cacheKey]; exists {
		playback := clonePreparedPlayback(entry.playback)
		cache.mu.Unlock()
		return playback, nil
	}
	if call, exists := cache.inFlight[cacheKey]; exists {
		cache.mu.Unlock()
		select {
		case <-ctx.Done():
			return preparedPlayback{}, ctx.Err()
		case <-call.done:
			return clonePreparedPlayback(call.playback), call.err
		}
	}
	call := &playbackPreparationCall{done: make(chan struct{})}
	generation := cache.generation
	cache.inFlight[cacheKey] = call
	cache.mu.Unlock()

	go func() {
		preparationContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), playbackPreparationTimeout)
		defer cancel()
		playback, err := service.buildPreparedPlayback(preparationContext, principal, reference, policy)
		cache.complete(cacheKey, call, generation, playback, err, reference.ExpiresAt)
	}()

	select {
	case <-ctx.Done():
		return preparedPlayback{}, ctx.Err()
	case <-call.done:
		return clonePreparedPlayback(call.playback), call.err
	}
}

func (cache *playbackPreparationCache) complete(cacheKey string, call *playbackPreparationCall, generation uint64, playback preparedPlayback, err error, expiresAt time.Time) {
	cache.mu.Lock()
	call.playback = clonePreparedPlayback(playback)
	call.err = err
	if err == nil && cache.generation == generation && cache.inFlight[cacheKey] == call {
		for len(cache.entries) >= maximumPlaybackPreparations {
			cache.removeEarliestLocked()
		}
		cache.entries[cacheKey] = playbackPreparationEntry{playback: clonePreparedPlayback(playback), expiresAt: expiresAt}
	}
	if cache.inFlight[cacheKey] == call {
		delete(cache.inFlight, cacheKey)
	}
	close(call.done)
	cache.mu.Unlock()
}

func (service *Service) buildPreparedPlayback(ctx context.Context, principal auth.Principal, reference sourceReference, policies ...playbackPolicy) (preparedPlayback, error) {
	type subtitleResult struct {
		batch addon.ResourceBatch
		err   error
	}
	subtitlesChannel := make(chan subtitleResult, 1)
	go func() {
		batch, err := service.addons.FetchAllPlaybackResources(ctx, principal, addon.ResourcePath{Resource: "subtitles", Type: reference.AddonMediaType, ID: reference.ResourceID})
		subtitlesChannel <- subtitleResult{batch: batch, err: err}
	}()

	policy := playbackPolicy{allowTranscoding: true, maximumHeight: reference.Capabilities.MaximumHeight, bitrateKbps: service.mediaOptions.TranscodeVideoBitrateKbps}
	if len(policies) > 0 {
		policy = policies[0]
	}
	capabilities := service.playbackCapabilities(reference.Capabilities, policy.maximumHeight, policy.bitrateKbps)
	sources := []Source{cloneSource(reference.Source)}
	assets := make([]storedAsset, 0, 1)
	if reference.Asset != nil {
		assets = append(assets, cloneStoredAsset(*reference.Asset))
	}
	if err := service.decidePlaybackSource(ctx, sources, assets, capabilities, policy.allowTranscoding); err != nil {
		return preparedPlayback{}, err
	}
	if sources[0].Media != nil {
		if assetIndex := storedAssetIndex(assets, sources[0].ID); assetIndex >= 0 {
			assets[assetIndex].DurationSeconds = sources[0].Media.DurationSeconds
		}
	}
	if !hasCompatibleSource(sources) {
		if sources[0].Media != nil {
			return preparedPlayback{}, ErrUnsupportedSource
		}
		return preparedPlayback{}, ErrNoPlayableSource
	}

	subtitleResources := <-subtitlesChannel
	subtitles := make([]Subtitle, 0)
	subtitleAssets := make([]storedAsset, 0)
	providerErrors := append([]ProviderFailure(nil), reference.ProviderErrors...)
	if subtitleResources.err == nil &&
		(len(capabilities.SubtitleModes) == 0 || requestedProcessingMode(capabilities.SubtitleModes, "external")) {
		normalizedSubtitles, normalizedAssets, normalizationErr := normalizeSubtitles(subtitleResources.batch)
		if normalizationErr == nil {
			subtitles = normalizedSubtitles
			subtitleAssets = normalizedAssets
		}
		providerErrors = append(providerErrors, providerFailures(subtitleResources.batch.Errors)...)
	}
	embedded, embeddedAssets := embeddedSubtitles(sources, assets, capabilities)
	subtitles = append(subtitles, embedded...)
	subtitleAssets = append(subtitleAssets, embeddedAssets...)

	var asset *storedAsset
	if len(assets) > 0 {
		value := cloneStoredAsset(assets[0])
		asset = &value
	}
	return preparedPlayback{
		source: sources[0], asset: asset, subtitles: subtitles, subtitleAssets: subtitleAssets,
		providerErrors: providerErrors, addonIDs: preparedPlaybackAddonIDs(sources[0], subtitles),
	}, nil
}

func (cache *playbackPreparationCache) removeExpiredLocked() {
	now := cache.now()
	for identifier, entry := range cache.entries {
		if !entry.expiresAt.After(now) {
			delete(cache.entries, identifier)
		}
	}
}

func (cache *playbackPreparationCache) removeEarliestLocked() {
	earliestKey := ""
	var earliest time.Time
	for key, entry := range cache.entries {
		if earliestKey == "" || entry.expiresAt.Before(earliest) || entry.expiresAt.Equal(earliest) && key < earliestKey {
			earliestKey = key
			earliest = entry.expiresAt
		}
	}
	if earliestKey != "" {
		delete(cache.entries, earliestKey)
	}
}

func clonePreparedPlayback(playback preparedPlayback) preparedPlayback {
	playback.source = cloneSource(playback.source)
	if playback.asset != nil {
		asset := cloneStoredAsset(*playback.asset)
		playback.asset = &asset
	}
	playback.subtitles = append([]Subtitle(nil), playback.subtitles...)
	subtitleAssets := playback.subtitleAssets
	playback.subtitleAssets = make([]storedAsset, len(subtitleAssets))
	for index := range subtitleAssets {
		playback.subtitleAssets[index] = cloneStoredAsset(subtitleAssets[index])
	}
	playback.providerErrors = append([]ProviderFailure(nil), playback.providerErrors...)
	playback.addonIDs = append([]string(nil), playback.addonIDs...)
	return playback
}

func preparedPlaybackAddonIDs(source Source, subtitles []Subtitle) []string {
	var values []string
	var seen map[string]struct{}
	appendID := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		if seen == nil {
			seen = make(map[string]struct{})
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	appendID(source.AddonID)
	for _, subtitle := range subtitles {
		appendID(subtitle.AddonID)
	}
	return values
}
