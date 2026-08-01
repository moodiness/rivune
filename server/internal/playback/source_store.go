package playback

import (
	"crypto/rand"
	"encoding/base64"
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

func (store *sourceReferenceStore) put(reference sourceReference) (sourceReference, error) {
	identifier, err := newOpaqueReference()
	if err != nil {
		return sourceReference{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.removeExpiredLocked()
	for len(store.entries) >= maximumSourceReferences {
		store.removeEarliestLocked()
	}
	reference.ID = identifier
	reference.ExpiresAt = store.now().Add(sourceReferenceTTL)
	store.entries[identifier] = cloneSourceReference(reference)
	return reference, nil
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

func (store *sourceReferenceStore) removeEarliestLocked() {
	var earliestID string
	var earliest time.Time
	for identifier, reference := range store.entries {
		if earliestID == "" || reference.ExpiresAt.Before(earliest) {
			earliestID = identifier
			earliest = reference.ExpiresAt
		}
	}
	if earliestID != "" {
		delete(store.entries, earliestID)
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
	if capabilities.PreferDirectPlay != nil {
		value := *capabilities.PreferDirectPlay
		capabilities.PreferDirectPlay = &value
	}
	return capabilities
}
