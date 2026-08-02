package playback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	mediaProbeCacheTTL     = 15 * time.Minute
	maximumMediaProbeCache = 512
)

type mediaProbeCacheEntry struct {
	inspection MediaInspection
	expiresAt  time.Time
}

type mediaProbeCall struct {
	done       chan struct{}
	inspection MediaInspection
	err        error
}

type mediaProbeCache struct {
	mu         sync.Mutex
	entries    map[string]mediaProbeCacheEntry
	inFlight   map[string]*mediaProbeCall
	generation uint64
	now        func() time.Time
}

func newMediaProbeCache(now func() time.Time) *mediaProbeCache {
	return &mediaProbeCache{
		entries: make(map[string]mediaProbeCacheEntry), inFlight: make(map[string]*mediaProbeCall), now: now,
	}
}

func (cache *mediaProbeCache) clear() {
	cache.mu.Lock()
	cache.generation++
	clear(cache.entries)
	cache.mu.Unlock()
}

func (service *Service) probeMedia(ctx context.Context, asset storedAsset) (MediaInspection, error) {
	if service.processor == nil {
		return MediaInspection{}, ErrMediaProcessingFailed
	}
	key := mediaProbeKey(asset)
	cache := service.probes
	cache.mu.Lock()
	cache.removeExpiredLocked()
	if entry, exists := cache.entries[key]; exists {
		inspection := cloneMediaInspection(entry.inspection)
		cache.mu.Unlock()
		return inspection, nil
	}
	if call, exists := cache.inFlight[key]; exists {
		cache.mu.Unlock()
		select {
		case <-ctx.Done():
			return MediaInspection{}, ctx.Err()
		case <-call.done:
			return cloneMediaInspection(call.inspection), call.err
		}
	}
	call := &mediaProbeCall{done: make(chan struct{})}
	generation := cache.generation
	cache.inFlight[key] = call
	cache.mu.Unlock()

	go func() {
		probeContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), playbackPreparationTimeout)
		defer cancel()
		inspection, err := service.processor.Probe(probeContext, cloneStoredAsset(asset))
		cache.mu.Lock()
		call.inspection = cloneMediaInspection(inspection)
		call.err = err
		if err == nil && cache.generation == generation {
			cache.removeExpiredLocked()
			for len(cache.entries) >= maximumMediaProbeCache {
				cache.removeEarliestLocked()
			}
			cache.entries[key] = mediaProbeCacheEntry{inspection: cloneMediaInspection(inspection), expiresAt: cache.now().Add(mediaProbeCacheTTL)}
		}
		delete(cache.inFlight, key)
		close(call.done)
		cache.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return MediaInspection{}, ctx.Err()
	case <-call.done:
		return cloneMediaInspection(call.inspection), call.err
	}
}

func (cache *mediaProbeCache) removeExpiredLocked() {
	now := cache.now()
	for key, entry := range cache.entries {
		if !entry.expiresAt.After(now) {
			delete(cache.entries, key)
		}
	}
}

func (cache *mediaProbeCache) removeEarliestLocked() {
	var earliestKey string
	var earliest time.Time
	for key, entry := range cache.entries {
		if earliestKey == "" || entry.expiresAt.Before(earliest) {
			earliestKey = key
			earliest = entry.expiresAt
		}
	}
	if earliestKey != "" {
		delete(cache.entries, earliestKey)
	}
}

func mediaProbeKey(asset storedAsset) string {
	names := make([]string, 0, len(asset.Headers))
	for name := range asset.Headers {
		names = append(names, strings.ToLower(strings.TrimSpace(name)))
	}
	sort.Strings(names)
	var value strings.Builder
	value.Grow(len(asset.URL) + len(asset.Container) + len(names)*32)
	value.WriteString(asset.URL)
	value.WriteByte('\n')
	value.WriteString(asset.Container)
	for _, name := range names {
		value.WriteByte('\n')
		value.WriteString(name)
		value.WriteByte(':')
		for originalName, headerValue := range asset.Headers {
			if strings.EqualFold(strings.TrimSpace(originalName), name) {
				value.WriteString(headerValue)
				break
			}
		}
	}
	digest := sha256.Sum256([]byte(value.String()))
	return hex.EncodeToString(digest[:])
}
