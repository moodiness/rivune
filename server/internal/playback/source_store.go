package playback

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

const (
	sourceReferenceTTL      = 4 * time.Hour
	maximumSourceReferences = 4096
)

type sourceReference struct {
	ID                              string
	AuthSessionID                   string
	ProfileID                       string
	MediaType                       string
	AddonMediaType                  string
	ResourceID                      string
	Source                          Source
	Asset                           *storedAsset
	Capabilities                    Capabilities
	PreferredAudioLanguage          string
	PreferredSubtitleLanguage       string
	PreferredForcedSubtitleLanguage string
	ProviderErrors                  []ProviderFailure
	ExpiresAt                       time.Time
}

type sourceReferenceStore struct {
	mu      sync.Mutex
	entries map[string]sourceReference
	now     func() time.Time
}

func newSourceReferenceStore(now func() time.Time) *sourceReferenceStore {
	return &sourceReferenceStore{entries: make(map[string]sourceReference), now: now}
}

func (store *sourceReferenceStore) clear() {
	store.mu.Lock()
	clear(store.entries)
	store.mu.Unlock()
}

func (store *sourceReferenceStore) put(reference sourceReference) (sourceReference, error) {
	references, err := store.putAll([]sourceReference{reference})
	if err != nil {
		return sourceReference{}, err
	}
	return references[0], nil
}

func (store *sourceReferenceStore) putAll(references []sourceReference) ([]sourceReference, error) {
	if len(references) == 0 {
		return nil, nil
	}
	if len(references) > maximumSourceReferences {
		return nil, errors.New("source reference batch exceeds store capacity")
	}
	identifiers := make([]string, len(references))
	for index := range identifiers {
		identifier, err := newOpaqueReference()
		if err != nil {
			return nil, err
		}
		identifiers[index] = identifier
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	store.removeExpiredLocked()
	store.removeEarliestLocked(max(0, len(store.entries)+len(references)-maximumSourceReferences))
	expiresAt := store.now().Add(sourceReferenceTTL)
	stored := make([]sourceReference, len(references))
	for index := range references {
		reference := references[index]
		reference.ID = identifiers[index]
		reference.ExpiresAt = expiresAt
		store.entries[reference.ID] = cloneSourceReference(reference)
		stored[index] = reference
	}
	return stored, nil
}

func (store *sourceReferenceStore) get(identifier string, principal auth.Principal) (sourceReference, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.removeExpiredLocked()
	reference, exists := store.entries[identifier]
	if !exists || principal.ActiveProfileID == nil || reference.AuthSessionID != principal.SessionID || reference.ProfileID != *principal.ActiveProfileID {
		return sourceReference{}, ErrSourceReferenceExpired
	}
	return cloneSourceReference(reference), nil
}

func (store *sourceReferenceStore) removeExpiredLocked() {
	now := store.now()
	for identifier, reference := range store.entries {
		if !reference.ExpiresAt.After(now) {
			delete(store.entries, identifier)
		}
	}
}

func (store *sourceReferenceStore) removeEarliestLocked(count int) {
	if count <= 0 {
		return
	}
	type expiration struct {
		identifier string
		expiresAt  time.Time
	}
	expirations := make([]expiration, 0, len(store.entries))
	for identifier, reference := range store.entries {
		expirations = append(expirations, expiration{identifier: identifier, expiresAt: reference.ExpiresAt})
	}
	sort.Slice(expirations, func(left, right int) bool {
		if expirations[left].expiresAt.Equal(expirations[right].expiresAt) {
			return expirations[left].identifier < expirations[right].identifier
		}
		return expirations[left].expiresAt.Before(expirations[right].expiresAt)
	})
	for index := range min(count, len(expirations)) {
		delete(store.entries, expirations[index].identifier)
	}
}

func newOpaqueReference() (string, error) {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func cloneSourceReference(reference sourceReference) sourceReference {
	reference.Source = cloneSource(reference.Source)
	if reference.Asset != nil {
		asset := cloneStoredAsset(*reference.Asset)
		reference.Asset = &asset
	}
	reference.Capabilities = cloneCapabilities(reference.Capabilities)
	reference.ProviderErrors = append([]ProviderFailure(nil), reference.ProviderErrors...)
	return reference
}

func cloneSource(source Source) Source {
	if source.FileIndex != nil {
		fileIndex := *source.FileIndex
		source.FileIndex = &fileIndex
	}
	if source.Media != nil {
		inspection := cloneMediaInspection(*source.Media)
		source.Media = &inspection
	}
	if source.Decision != nil {
		source.Decision = clonePlaybackDecision(source.Decision)
	}
	return source
}

func cloneStoredAsset(asset storedAsset) storedAsset {
	if asset.Headers != nil {
		headers := asset.Headers
		asset.Headers = make(map[string]string, len(headers))
		for name, value := range headers {
			asset.Headers[name] = value
		}
	}
	if asset.AudioTrackIndex != nil {
		index := *asset.AudioTrackIndex
		asset.AudioTrackIndex = &index
	}
	if asset.SubtitleTrackIndex != nil {
		index := *asset.SubtitleTrackIndex
		asset.SubtitleTrackIndex = &index
	}
	if asset.Decision != nil {
		asset.Decision = clonePlaybackDecision(asset.Decision)
	}
	return asset
}

func cloneMediaInspection(inspection MediaInspection) MediaInspection {
	inspection.VideoTracks = append([]MediaTrack(nil), inspection.VideoTracks...)
	inspection.AudioTracks = append([]MediaTrack(nil), inspection.AudioTracks...)
	inspection.SubtitleTracks = append([]MediaTrack(nil), inspection.SubtitleTracks...)
	return inspection
}

func cloneCapabilities(capabilities Capabilities) Capabilities {
	capabilities.StreamingProtocols = append([]string(nil), capabilities.StreamingProtocols...)
	capabilities.Containers = append([]string(nil), capabilities.Containers...)
	capabilities.VideoCodecs = append([]string(nil), capabilities.VideoCodecs...)
	capabilities.AudioCodecs = append([]string(nil), capabilities.AudioCodecs...)
	capabilities.HDRFormats = append([]string(nil), capabilities.HDRFormats...)
	capabilities.ExternalPlayers = append([]string(nil), capabilities.ExternalPlayers...)
	capabilities.ProcessingModes = append([]string(nil), capabilities.ProcessingModes...)
	capabilities.SubtitleModes = append([]string(nil), capabilities.SubtitleModes...)
	capabilities.MediaProfiles = append([]MediaProfile(nil), capabilities.MediaProfiles...)
	if capabilities.PreferDirectPlay != nil {
		value := *capabilities.PreferDirectPlay
		capabilities.PreferDirectPlay = &value
	}
	return capabilities
}

func clonePlaybackDecision(decision *PlaybackDecision) *PlaybackDecision {
	if decision == nil {
		return nil
	}
	cloned := *decision
	if decision.Source != nil {
		source := *decision.Source
		cloned.Source = &source
	}
	if decision.Target != nil {
		target := *decision.Target
		cloned.Target = &target
	}
	return &cloned

}
