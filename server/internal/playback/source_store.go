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
	sourceReferenceTTL              = 4 * time.Hour
	maximumSourceReferences         = 4096
	maximumSourceReferencesPerUser  = maximumSourceReferences / 2
	maximumSourceReferencesPerOwner = maximumAggregateProviderStreams
)

type sourceReferenceOwner struct {
	UserID    string
	ProfileID string
	DeviceID  string
}

type sourceReference struct {
	ID                              string
	AuthSessionID                   string
	ProfileID                       string
	Owner                           sourceReferenceOwner
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
	mu            sync.Mutex
	entries       map[string]sourceReference
	pins          map[string]int
	now           func() time.Time
	newIdentifier func() (string, error)
}

func newSourceReferenceStore(now func() time.Time) *sourceReferenceStore {
	return &sourceReferenceStore{entries: make(map[string]sourceReference), pins: make(map[string]int), now: now, newIdentifier: newOpaqueReference}
}

func (store *sourceReferenceStore) clear() {
	store.mu.Lock()
	clear(store.entries)
	clear(store.pins)
	store.mu.Unlock()
}

func (store *sourceReferenceStore) put(principal auth.Principal, reference sourceReference) (sourceReference, error) {
	references, err := store.putAll(principal, []sourceReference{reference})
	if err != nil {
		return sourceReference{}, err
	}
	return references[0], nil
}

func (store *sourceReferenceStore) putAll(principal auth.Principal, references []sourceReference) ([]sourceReference, error) {
	return store.putAllReserved(principal, references, false)
}

func (store *sourceReferenceStore) putAllPinned(principal auth.Principal, references []sourceReference) ([]sourceReference, error) {
	return store.putAllReserved(principal, references, true)
}

func (store *sourceReferenceStore) putAllReserved(principal auth.Principal, references []sourceReference, pin bool) ([]sourceReference, error) {
	if len(references) == 0 {
		return nil, nil
	}
	if principal.ActiveProfileID == nil {
		return nil, errors.New("source reference owner requires an active profile")
	}
	if len(references) > maximumSourceReferencesPerOwner {
		return nil, errors.Join(ErrMediaCapacityReached, errors.New("source reference batch exceeds owner capacity"))
	}
	identifiers := make([]string, len(references))
	uniqueIdentifiers := make(map[string]struct{}, len(references))
	for index := range identifiers {
		identifier, err := store.newIdentifier()
		if err != nil {
			return nil, err
		}
		if _, exists := uniqueIdentifiers[identifier]; exists {
			return nil, errors.New("duplicate source reference identifier")
		}
		uniqueIdentifiers[identifier] = struct{}{}
		identifiers[index] = identifier
	}

	profileID := *principal.ActiveProfileID
	owner := sourceReferenceOwner{UserID: principal.UserID, ProfileID: profileID, DeviceID: principal.DeviceID}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.removeExpiredLocked()
	for _, identifier := range identifiers {
		if _, exists := store.entries[identifier]; exists {
			return nil, errors.New("duplicate source reference identifier")
		}
	}

	victims := make(map[string]struct{})
	ownerOverflow := max(0, store.ownerCountLocked(owner)+len(references)-maximumSourceReferencesPerOwner)
	if !store.selectOwnerVictimsLocked(owner, ownerOverflow, victims) {
		return nil, errors.Join(ErrMediaCapacityReached, errors.New("source reference owner capacity is pinned"))
	}
	userOverflow := max(0, store.userCountLocked(owner.UserID)-len(victims)+len(references)-maximumSourceReferencesPerUser)
	if !store.selectUserVictimsLocked(owner.UserID, userOverflow, victims) {
		return nil, errors.Join(ErrMediaCapacityReached, errors.New("source reference user capacity is pinned"))
	}
	globalOverflow := max(0, len(store.entries)-len(victims)+len(references)-maximumSourceReferences)
	if !store.selectUserVictimsLocked(owner.UserID, globalOverflow, victims) {
		return nil, errors.Join(ErrMediaCapacityReached, errors.New("source reference store capacity reached"))
	}
	if len(store.entries)-len(victims)+len(references) > maximumSourceReferences {
		return nil, errors.Join(ErrMediaCapacityReached, errors.New("source reference store capacity reached"))
	}

	expiresAt := store.now().Add(sourceReferenceTTL)
	stored := make([]sourceReference, len(references))
	for identifier := range victims {
		delete(store.entries, identifier)
		delete(store.pins, identifier)
	}
	for index := range references {
		reference := references[index]
		reference.ID = identifiers[index]
		reference.AuthSessionID = principal.SessionID
		reference.ProfileID = profileID
		reference.Owner = owner
		reference.ExpiresAt = expiresAt
		store.entries[reference.ID] = cloneSourceReference(reference)
		if pin {
			store.pins[reference.ID]++
		}
		stored[index] = reference
	}
	return stored, nil
}

func (store *sourceReferenceStore) get(identifier string, principal auth.Principal) (sourceReference, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.removeExpiredLocked()
	reference, exists := store.entries[identifier]
	if !exists || !sourceReferenceOwnedBy(reference, principal) {
		return sourceReference{}, ErrSourceReferenceExpired
	}
	return cloneSourceReference(reference), nil
}

func (store *sourceReferenceStore) pin(principal auth.Principal, identifiers []string) error {
	if len(identifiers) == 0 || principal.ActiveProfileID == nil {
		return ErrSourceReferenceExpired
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.removeExpiredLocked()
	unique := make(map[string]struct{}, len(identifiers))
	for _, identifier := range identifiers {
		if _, duplicate := unique[identifier]; duplicate {
			continue
		}
		unique[identifier] = struct{}{}
		reference, exists := store.entries[identifier]
		if !exists || !sourceReferenceOwnedBy(reference, principal) {
			return ErrSourceReferenceExpired
		}
	}
	for identifier := range unique {
		store.pins[identifier]++
	}
	return nil
}

func (store *sourceReferenceStore) unpin(principal auth.Principal, identifiers []string) {
	if len(identifiers) == 0 || principal.ActiveProfileID == nil {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	unique := make(map[string]struct{}, len(identifiers))
	for _, identifier := range identifiers {
		if _, duplicate := unique[identifier]; duplicate {
			continue
		}
		unique[identifier] = struct{}{}
		reference, exists := store.entries[identifier]
		if !exists || !sourceReferenceOwnedBy(reference, principal) {
			continue
		}
		if store.pins[identifier] <= 1 {
			delete(store.pins, identifier)
			if !reference.ExpiresAt.After(store.now()) {
				delete(store.entries, identifier)
			}
		} else {
			store.pins[identifier]--
		}
	}
}

func sourceReferenceOwnedBy(reference sourceReference, principal auth.Principal) bool {
	return principal.ActiveProfileID != nil && reference.AuthSessionID == principal.SessionID &&
		reference.ProfileID == *principal.ActiveProfileID && reference.Owner.ProfileID == *principal.ActiveProfileID &&
		reference.Owner.UserID == principal.UserID && reference.Owner.DeviceID == principal.DeviceID
}

func (store *sourceReferenceStore) removeExpiredLocked() {
	now := store.now()
	for identifier, reference := range store.entries {
		if store.pins[identifier] == 0 && !reference.ExpiresAt.After(now) {
			delete(store.entries, identifier)
		}
	}
}

func (store *sourceReferenceStore) ownerCountLocked(owner sourceReferenceOwner) int {
	count := 0
	for _, reference := range store.entries {
		if reference.Owner == owner {
			count++
		}
	}
	return count
}

func (store *sourceReferenceStore) userCountLocked(userID string) int {
	count := 0
	for _, reference := range store.entries {
		if reference.Owner.UserID == userID {
			count++
		}
	}
	return count
}

func (store *sourceReferenceStore) selectOwnerVictimsLocked(owner sourceReferenceOwner, count int, selected map[string]struct{}) bool {
	return store.selectVictimsLocked(count, selected, func(reference sourceReference) bool { return reference.Owner == owner })
}

func (store *sourceReferenceStore) selectUserVictimsLocked(userID string, count int, selected map[string]struct{}) bool {
	return store.selectVictimsLocked(count, selected, func(reference sourceReference) bool { return reference.Owner.UserID == userID })
}

func (store *sourceReferenceStore) selectVictimsLocked(count int, selected map[string]struct{}, matches func(sourceReference) bool) bool {
	if count <= 0 {
		return true
	}
	type expiration struct {
		identifier string
		expiresAt  time.Time
	}
	expirations := make([]expiration, 0)
	for identifier, reference := range store.entries {
		if _, exists := selected[identifier]; exists || store.pins[identifier] > 0 || !matches(reference) {
			continue
		}
		expirations = append(expirations, expiration{identifier: identifier, expiresAt: reference.ExpiresAt})
	}
	sort.Slice(expirations, func(left, right int) bool {
		if expirations[left].expiresAt.Equal(expirations[right].expiresAt) {
			return expirations[left].identifier < expirations[right].identifier
		}
		return expirations[left].expiresAt.Before(expirations[right].expiresAt)
	})
	if len(expirations) < count {
		return false
	}
	for index := range count {
		selected[expirations[index].identifier] = struct{}{}
	}
	return true
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
