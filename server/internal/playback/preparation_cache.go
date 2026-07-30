package playback

import (
	"context"
	"sync"
	"time"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
)

const playbackPreparationTimeout = 30 * time.Second

type preparedPlayback struct {
	source         Source
	asset          *storedAsset
	subtitles      []Subtitle
	subtitleAssets []storedAsset
	providerErrors []ProviderFailure
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
	mu       sync.Mutex
	entries  map[string]playbackPreparationEntry
	inFlight map[string]*playbackPreparationCall
	now      func() time.Time
}

func newPlaybackPreparationCache(now func() time.Time) *playbackPreparationCache {
	return &playbackPreparationCache{
		entries: make(map[string]playbackPreparationEntry), inFlight: make(map[string]*playbackPreparationCall), now: now,
	}
}

func (service *Service) preparedPlayback(ctx context.Context, principal auth.Principal, reference sourceReference) (preparedPlayback, error) {
	cache := service.preparations
	cache.mu.Lock()
	cache.removeExpiredLocked()
	if entry, exists := cache.entries[reference.ID]; exists {
		playback := clonePreparedPlayback(entry.playback)
		cache.mu.Unlock()
		return playback, nil
	}
	if call, exists := cache.inFlight[reference.ID]; exists {
		cache.mu.Unlock()
		select {
		case <-ctx.Done():
			return preparedPlayback{}, ctx.Err()
		case <-call.done:
			return clonePreparedPlayback(call.playback), call.err
		}
	}
	call := &playbackPreparationCall{done: make(chan struct{})}
	cache.inFlight[reference.ID] = call
	cache.mu.Unlock()

	go func() {
		preparationContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), playbackPreparationTimeout)
		defer cancel()
		playback, err := service.buildPreparedPlayback(preparationContext, principal, reference)
		cache.mu.Lock()
		call.playback = clonePreparedPlayback(playback)
		call.err = err
		if err == nil {
			cache.entries[reference.ID] = playbackPreparationEntry{playback: clonePreparedPlayback(playback), expiresAt: reference.ExpiresAt}
		}
		delete(cache.inFlight, reference.ID)
		close(call.done)
		cache.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return preparedPlayback{}, ctx.Err()
	case <-call.done:
		return clonePreparedPlayback(call.playback), call.err
	}
}

func (service *Service) buildPreparedPlayback(ctx context.Context, principal auth.Principal, reference sourceReference) (preparedPlayback, error) {
	type subtitleResult struct {
		batch addon.ResourceBatch
		err   error
	}
	subtitlesChannel := make(chan subtitleResult, 1)
	go func() {
		batch, err := service.addons.FetchAll(ctx, principal, addon.ResourcePath{Resource: "subtitles", Type: reference.AddonMediaType, ID: reference.ResourceID})
		subtitlesChannel <- subtitleResult{batch: batch, err: err}
	}()

	sources := []Source{cloneSource(reference.Source)}
	assets := make([]storedAsset, 0, 1)
	if reference.Asset != nil {
		assets = append(assets, cloneStoredAsset(*reference.Asset))
	}
	service.decidePlaybackSource(ctx, sources, assets, reference.Capabilities)
	if !hasCompatibleSource(sources) {
		return preparedPlayback{}, ErrNoPlayableSource
	}

	subtitleResources := <-subtitlesChannel
	subtitles := make([]Subtitle, 0)
	subtitleAssets := make([]storedAsset, 0)
	providerErrors := append([]ProviderFailure(nil), reference.ProviderErrors...)
	if subtitleResources.err == nil {
		subtitles, subtitleAssets = normalizeSubtitles(subtitleResources.batch)
		providerErrors = append(providerErrors, providerFailures(subtitleResources.batch.Errors)...)
	}
	embedded, embeddedAssets := embeddedSubtitles(sources, assets)
	subtitles = append(subtitles, embedded...)
	subtitleAssets = append(subtitleAssets, embeddedAssets...)

	var asset *storedAsset
	if len(assets) > 0 {
		value := cloneStoredAsset(assets[0])
		asset = &value
	}
	return preparedPlayback{
		source: sources[0], asset: asset, subtitles: subtitles, subtitleAssets: subtitleAssets, providerErrors: providerErrors,
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
	return playback
}
