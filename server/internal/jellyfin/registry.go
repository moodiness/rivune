package jellyfin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/playback"
)

var errPlaySessionNotFound = errors.New("compatibility play session not found")

const (
	defaultPlaySessionLimit             = 256
	defaultPlaySessionUserLimit         = 64
	defaultPlaySessionOwnerLimit        = 8
	defaultPlaySessionIdleTTL           = 30 * time.Minute
	defaultPlaySessionAbsoluteTTL       = 2 * time.Hour
	defaultPlaySessionReapPeriod        = time.Minute
	defaultPlaySessionCleanupTimeout    = 5 * time.Second
	defaultPlaySessionCleanupWorkers    = 4
	defaultPlaySessionCleanupRetryBase  = 100 * time.Millisecond
	defaultPlaySessionCleanupRetryMax   = 30 * time.Second
	defaultPlaySessionCleanupRetryTTL   = 10 * time.Minute
	defaultPlaySessionCleanupQueueLimit = 4096
)

type playSessionBinding struct {
	ItemID          string
	PlaySessionID   string
	MediaSourceID   string
	DurationSeconds float64
}

type playSourceDescriptor struct {
	ID        string
	Name      string
	Protocol  string
	Container string
}
type playSourceKey struct {
	StableIdentity string
}

type playSessionSource struct {
	descriptor         playSourceDescriptor
	sourceRef          string
	expiresAt          time.Time
	key                playSourceKey
	handle             playback.DeliveryHandle
	resolvedSession    playback.Session
	duration           float64
	startTimeTicks     int64
	capabilityRevision uint64
	opening            chan struct{}
	openingCancel      context.CancelFunc
}

type sourceReferencePinner interface {
	PinSourceReferences(auth.Principal, []string) error
	UnpinSourceReferences(auth.Principal, []string)
}

type sourceReferenceReserver interface {
	sourceReferencePinner
	SourcesAndPin(context.Context, auth.Principal, playback.SourcesInput) (playback.SourceList, error)
}

type deliveryHandlePruner interface {
	PruneDeliveryHandles()
}

type pendingPlaySessionClose struct {
	principal   auth.Principal
	handle      playback.DeliveryHandle
	attempts    uint32
	nextAttempt time.Time
	deadline    time.Time
	claimed     bool
}

// playbackEventLease holds one play-session event across resolution, persistence, and terminal close.
type playbackEventLease struct {
	token chan struct{}
}

func (lease playbackEventLease) release() {
	<-lease.token
}

type playSessionEntry struct {
	compatSessionID    string
	nativeSessionID    string
	profileID          string
	deviceID           string
	itemID             string
	playSessionID      string
	principal          auth.Principal
	capabilities       playback.Capabilities
	allowTranscode     bool
	capabilityRevision uint64
	createdAt          time.Time
	sequence           uint64
	lastSeenAt         time.Time
	expiresAt          time.Time
	sourceOrder        []string
	sources            map[string]*playSessionSource
	referencesPinned   bool
	eventLease         chan struct{}
}
type registeredDeviceProfile struct {
	session    AuthenticatedSession
	profile    DeviceProfile
	lastSeenAt time.Time
	expiresAt  time.Time
}

type playSessionRegistry struct {
	mu             sync.Mutex
	playback       PlaybackDelivery
	entries        map[string]*playSessionEntry
	deviceProfiles map[string]*registeredDeviceProfile
	nextSequence   uint64
	limit          int
	userLimit      int
	ownerLimit     int
	idleTTL        time.Duration
	absoluteTTL    time.Duration
	reapPeriod     time.Duration
	cleanupTimeout time.Duration
	cleanupWorkers int
	cleanupSlots   chan struct{}

	cleanupMu           sync.Mutex
	cleanupPending      map[playback.DeliveryHandle]*pendingPlaySessionClose
	cleanupOwned        map[playback.DeliveryHandle]struct{}
	cleanupReservations int
	cleanupRetiring     bool
	cleanupAbandoned    bool
	cleanupQueueLimit   int
	cleanupRetryBase    time.Duration
	cleanupRetryMax     time.Duration
	cleanupRetryTTL     time.Duration
	cleanupWake         chan struct{}
	now                 func() time.Time
}

func newPlaySessionRegistry(delivery PlaybackDelivery) *playSessionRegistry {
	if delivery == nil {
		return nil
	}
	return &playSessionRegistry{
		playback: delivery, entries: make(map[string]*playSessionEntry), deviceProfiles: make(map[string]*registeredDeviceProfile), limit: defaultPlaySessionLimit,
		userLimit: defaultPlaySessionUserLimit, ownerLimit: defaultPlaySessionOwnerLimit,
		idleTTL: defaultPlaySessionIdleTTL, absoluteTTL: defaultPlaySessionAbsoluteTTL,
		reapPeriod: defaultPlaySessionReapPeriod, cleanupTimeout: defaultPlaySessionCleanupTimeout,
		cleanupWorkers: defaultPlaySessionCleanupWorkers,
		cleanupSlots:   make(chan struct{}, defaultPlaySessionCleanupWorkers),
		cleanupPending: make(map[playback.DeliveryHandle]*pendingPlaySessionClose), cleanupOwned: make(map[playback.DeliveryHandle]struct{}),
		cleanupQueueLimit: defaultPlaySessionCleanupQueueLimit,
		cleanupRetryBase:  defaultPlaySessionCleanupRetryBase, cleanupRetryMax: defaultPlaySessionCleanupRetryMax,
		cleanupRetryTTL: defaultPlaySessionCleanupRetryTTL,
		cleanupWake:     make(chan struct{}, 1),
		now:             func() time.Time { return time.Now().UTC() },
	}
}

func (registry *playSessionRegistry) register(ctx context.Context, session AuthenticatedSession, itemID string, capabilities playback.Capabilities, allowTranscode bool, options []playback.SourceOption) (string, []playSourceDescriptor, error) {
	if registry == nil || registry.playback == nil || !validPlaySessionOwner(session) || itemID == "" || len(options) == 0 {
		return "", nil, errPlaySessionNotFound
	}
	playID, err := newPlaySessionID()
	if err != nil {
		return "", nil, fmt.Errorf("create compatibility play session: %w", err)
	}
	now := registry.now().UTC()
	expiresAt := now.Add(registry.absoluteTTL)
	if session.ExpiresAt.Before(expiresAt) {
		expiresAt = session.ExpiresAt.UTC()
	}
	entry := &playSessionEntry{
		compatSessionID: session.ID, nativeSessionID: session.Principal.SessionID, profileID: session.ProfileID,
		deviceID: session.Client.DeviceID, itemID: itemID, playSessionID: playID, principal: clonePrincipal(session.Principal),
		capabilities: clonePlaybackCapabilities(capabilities), allowTranscode: allowTranscode, capabilityRevision: 1,
		createdAt: now, lastSeenAt: now, expiresAt: expiresAt, sourceOrder: make([]string, 0, len(options)),
		sources: make(map[string]*playSessionSource, len(options)), eventLease: make(chan struct{}, 1),
	}
	descriptors := make([]playSourceDescriptor, 0, len(options))
	references := make([]string, 0, len(options))
	for _, option := range options {
		if option.SourceRef == "" || option.ExpiresAt.IsZero() || !option.ExpiresAt.After(now) {
			continue
		}
		candidateIndex := len(entry.sourceOrder)
		mediaID := itemID
		if candidateIndex > 0 {
			mediaID = derivedMediaSourceID(playID, candidateIndex)
		}
		descriptor := playSourceDescriptor{ID: mediaID, Name: compatibilitySourceName(candidateIndex+1, option.ReportedHeight), Protocol: option.Protocol, Container: option.Container}
		entry.sourceOrder = append(entry.sourceOrder, mediaID)
		entry.sources[mediaID] = &playSessionSource{descriptor: descriptor, key: playSourceKeyFor(option), sourceRef: option.SourceRef, expiresAt: option.ExpiresAt.UTC()}
		descriptors = append(descriptors, descriptor)
		references = append(references, option.SourceRef)
	}
	if len(entry.sources) == 0 || !entry.expiresAt.After(now) {
		return "", nil, errPlaySessionNotFound
	}
	pinner, pinnable := registry.playback.(sourceReferencePinner)
	if pinnable {
		if err := pinner.PinSourceReferences(entry.principal, references); err != nil {
			return "", nil, err
		}
		entry.referencesPinned = true
	}

	registry.mu.Lock()
	registry.nextSequence++
	entry.sequence = registry.nextSequence
	stale := registry.removeExpiredLocked(now)
	ownerLimit := registry.ownerLimit
	if ownerLimit <= 0 {
		ownerLimit = defaultPlaySessionOwnerLimit
	}
	for registry.ownerCountLocked(session) >= ownerLimit {
		victim := registry.oldestOwnerLocked(session)
		if victim == nil {
			break
		}
		delete(registry.entries, victim.playSessionID)
		stale = append(stale, victim)
	}
	userLimit := registry.userLimit
	if userLimit <= 0 {
		userLimit = defaultPlaySessionUserLimit
	}
	for registry.userCountLocked(session.Principal.UserID) >= userLimit {
		victim := registry.oldestUserLocked(session.Principal.UserID)
		if victim == nil {
			break
		}
		delete(registry.entries, victim.playSessionID)
		stale = append(stale, victim)
	}
	globalLimit := registry.limit
	if globalLimit <= 0 {
		globalLimit = defaultPlaySessionLimit
	}
	if len(registry.entries) >= globalLimit {
		victim := registry.oldestUserLocked(session.Principal.UserID)
		if victim == nil {
			registry.mu.Unlock()
			if entry.referencesPinned {
				pinner.UnpinSourceReferences(entry.principal, references)
				entry.referencesPinned = false
			}
			registry.closeEntries(context.WithoutCancel(ctx), stale)
			return "", nil, playback.ErrMediaCapacityReached
		}
		delete(registry.entries, victim.playSessionID)
		stale = append(stale, victim)
	}
	registry.entries[playID] = entry
	registry.mu.Unlock()
	registry.closeEntries(context.WithoutCancel(ctx), stale)
	return playID, descriptors, nil
}

// reuseCandidate keeps an emitted MediaSourceId bound to its original source
// reference. A capability refresh may transfer that binding only when every
// candidate has a unique, URL-free stable identity.
func (registry *playSessionRegistry) reuseCandidate(session AuthenticatedSession, itemID, mediaID string, capabilities playback.Capabilities, allowTranscode bool, options []playback.SourceOption) (string, []playSourceDescriptor, bool) {
	if registry == nil || !validPlaySessionOwner(session) || itemID == "" || mediaID == "" {
		return "", nil, false
	}
	now := registry.now().UTC()
	freshByKey := make(map[playSourceKey]playback.SourceOption, len(options))
	uniqueFresh := len(options) > 0
	for _, option := range options {
		key := playSourceKeyFor(option)
		if option.SourceRef == "" || option.ExpiresAt.IsZero() || !option.ExpiresAt.After(now) || key.StableIdentity == "" {
			uniqueFresh = false
			continue
		}
		if _, exists := freshByKey[key]; exists {
			uniqueFresh = false
		}
		freshByKey[key] = option
	}
	registry.mu.Lock()
	stale := registry.removeExpiredLocked(now)
	var selected *playSessionEntry
	for _, entry := range registry.entries {
		if !ownerMatches(entry, session) || entry.itemID != itemID || entry.sources[mediaID] == nil {
			continue
		}
		capabilitiesChanged := !playbackCapabilitiesEqual(entry.capabilities, capabilities) || entry.allowTranscode != allowTranscode
		if capabilitiesChanged && (!uniqueFresh || !candidateSetMatches(entry, freshByKey)) {
			continue
		}
		if selected == nil || entry.sequence > selected.sequence {
			selected = entry
		}
	}
	var descriptors []playSourceDescriptor
	var releasePrincipal auth.Principal
	var releaseReferences []string
	if selected != nil {
		capabilitiesChanged := !playbackCapabilitiesEqual(selected.capabilities, capabilities) || selected.allowTranscode != allowTranscode
		if capabilitiesChanged {
			pinner, pinnable := registry.playback.(sourceReferencePinner)
			freshReferences := sourceReferencesFor(selected, freshByKey)
			if !pinnable || len(freshReferences) != len(selected.sourceOrder) || pinner.PinSourceReferences(selected.principal, freshReferences) != nil {
				selected = nil
			} else {
				if selected.referencesPinned {
					releasePrincipal = clonePrincipal(selected.principal)
					releaseReferences = sourceReferences(selected)
				}
				for index, id := range selected.sourceOrder {
					source := selected.sources[id]
					option := freshByKey[source.key]
					source.sourceRef = option.SourceRef
					source.expiresAt = option.ExpiresAt.UTC()
					source.descriptor.Name = compatibilitySourceName(index+1, option.ReportedHeight)
					source.descriptor.Protocol = option.Protocol
					source.descriptor.Container = option.Container
				}
				selected.referencesPinned = true
				selected.capabilities = clonePlaybackCapabilities(capabilities)
				selected.allowTranscode = allowTranscode
				selected.capabilityRevision++
			}
		}
		if selected != nil {
			descriptors = descriptorsFor(selected)
			selected.lastSeenAt = now
		}
	}
	registry.mu.Unlock()
	registry.closeEntries(context.Background(), stale)
	if len(releaseReferences) > 0 {
		registry.playback.(sourceReferencePinner).UnpinSourceReferences(releasePrincipal, releaseReferences)
	}
	if selected == nil {
		return "", nil, false
	}
	return selected.playSessionID, descriptors, true
}

func (registry *playSessionRegistry) setDeviceProfile(session AuthenticatedSession, profile DeviceProfile) bool {
	if registry == nil || !validPlaySessionOwner(session) || !validDeviceProfileBounds(profile) || emptyDeviceProfile(profile) {
		return false
	}
	now := registry.now().UTC()
	expiresAt := session.ExpiresAt.UTC()
	if !expiresAt.After(now) {
		return false
	}
	storedSession := session
	storedSession.Principal = clonePrincipal(session.Principal)
	stored := &registeredDeviceProfile{session: storedSession, profile: cloneDeviceProfile(profile), lastSeenAt: now, expiresAt: expiresAt}
	registry.mu.Lock()
	stale := registry.removeExpiredLocked(now)
	if registry.deviceProfiles[session.ID] == nil {
		ownerLimit := registry.ownerLimit
		if ownerLimit <= 0 {
			ownerLimit = defaultPlaySessionOwnerLimit
		}
		for registry.deviceProfileOwnerCountLocked(session) >= ownerLimit {
			victimID := registry.oldestDeviceProfileLocked(func(candidate *registeredDeviceProfile) bool {
				return deviceProfileQuotaOwnerMatches(candidate, session)
			})
			if victimID == "" {
				break
			}
			delete(registry.deviceProfiles, victimID)
		}
		userLimit := registry.userLimit
		if userLimit <= 0 {
			userLimit = defaultPlaySessionUserLimit
		}
		for registry.deviceProfileUserCountLocked(session.Principal.UserID) >= userLimit {
			victimID := registry.oldestDeviceProfileLocked(func(candidate *registeredDeviceProfile) bool {
				return candidate.session.Principal.UserID == session.Principal.UserID
			})
			if victimID == "" {
				break
			}
			delete(registry.deviceProfiles, victimID)
		}
		globalLimit := registry.limit
		if globalLimit <= 0 {
			globalLimit = defaultPlaySessionLimit
		}
		if len(registry.deviceProfiles) >= globalLimit {
			victimID := registry.oldestDeviceProfileLocked(func(candidate *registeredDeviceProfile) bool {
				return candidate.session.Principal.UserID == session.Principal.UserID
			})
			if victimID == "" {
				registry.mu.Unlock()
				registry.closeEntries(context.Background(), stale)
				return false
			}
			delete(registry.deviceProfiles, victimID)
		}
	}
	registry.deviceProfiles[session.ID] = stored
	registry.mu.Unlock()
	registry.closeEntries(context.Background(), stale)
	return true
}

func (registry *playSessionRegistry) deviceProfile(session AuthenticatedSession) (DeviceProfile, bool) {
	if registry == nil || !validPlaySessionOwner(session) {
		return DeviceProfile{}, false
	}
	now := registry.now().UTC()
	registry.mu.Lock()
	stale := registry.removeExpiredLocked(now)
	stored := registry.deviceProfiles[session.ID]
	ok := deviceProfileOwnerMatches(stored, session) && stored.expiresAt.After(now) && stored.lastSeenAt.Add(registry.idleTTL).After(now)
	var profile DeviceProfile
	if ok {
		stored.lastSeenAt = now
		profile = cloneDeviceProfile(stored.profile)
	}
	registry.mu.Unlock()
	registry.closeEntries(context.Background(), stale)
	return profile, ok
}

func (registry *playSessionRegistry) streamSession(playID, itemID, mediaID string) (AuthenticatedSession, bool) {
	if registry == nil || playID == "" || itemID == "" || mediaID == "" {
		return AuthenticatedSession{}, false
	}
	now := registry.now().UTC()
	registry.mu.Lock()
	stale := registry.removeExpiredLocked(now)
	entry := registry.entries[playID]
	var session AuthenticatedSession
	ok := entry != nil && entry.itemID == itemID && entry.sources[mediaID] != nil && entry.expiresAt.After(now) && entry.lastSeenAt.Add(registry.idleTTL).After(now)
	if ok {
		entry.lastSeenAt = now
		session = AuthenticatedSession{
			ID: entry.compatSessionID, ProfileID: entry.profileID, Client: ClientIdentity{DeviceID: entry.deviceID},
			ExpiresAt: entry.expiresAt, Principal: clonePrincipal(entry.principal),
		}
		ok = validPlaySessionOwner(session)
	}
	registry.mu.Unlock()
	registry.closeEntries(context.Background(), stale)
	return session, ok
}

func (registry *playSessionRegistry) candidateExists(session AuthenticatedSession, itemID, mediaID string) bool {
	if registry == nil {
		return false
	}
	now := registry.now().UTC()
	registry.mu.Lock()
	stale := registry.removeExpiredLocked(now)
	exists := false
	for _, entry := range registry.entries {
		if ownerMatches(entry, session) && entry.itemID == itemID && entry.sources[mediaID] != nil {
			exists = true
			break
		}
	}
	registry.mu.Unlock()
	registry.closeEntries(context.Background(), stale)
	return exists
}

func (registry *playSessionRegistry) openAndTouch(ctx context.Context, session AuthenticatedSession, itemID, playID, mediaID string, startTimeTicks int64) (playSessionBinding, playback.DeliveryHandle, playback.Session, error) {
	const maxBindingChanges = 4
	bindingChanges := 0
	for {
		now := registry.now().UTC()
		registry.mu.Lock()
		stale := registry.removeExpiredLocked(now)
		entry, source, ok := registry.lookupLocked(session, itemID, playID, mediaID, now)
		if !ok {
			registry.mu.Unlock()
			registry.closeEntries(context.WithoutCancel(ctx), stale)
			return playSessionBinding{}, playback.DeliveryHandle{}, playback.Session{}, errPlaySessionNotFound
		}
		resolvedMediaID := source.descriptor.ID
		if source.handle.Valid() && source.startTimeTicks == startTimeTicks && source.capabilityRevision == entry.capabilityRevision {
			entry.lastSeenAt = now
			binding := bindingFor(entry, source)
			handle := source.handle
			resolved := source.resolvedSession
			registry.mu.Unlock()
			registry.closeEntries(context.WithoutCancel(ctx), stale)
			return binding, handle, resolved, nil
		}
		if source.opening != nil {
			waiting := source.opening
			registry.mu.Unlock()
			registry.closeEntries(context.WithoutCancel(ctx), stale)
			select {
			case <-waiting:
				continue
			case <-ctx.Done():
				return playSessionBinding{}, playback.DeliveryHandle{}, playback.Session{}, ctx.Err()
			}
		}
		if !registry.reserveCleanupHandle() {
			registry.mu.Unlock()
			registry.closeEntries(context.WithoutCancel(ctx), stale)
			return playSessionBinding{}, playback.DeliveryHandle{}, playback.Session{}, playback.ErrMediaCapacityReached
		}
		source.opening = make(chan struct{})
		opening := source.opening
		openContext, cancelOpen := context.WithCancel(ctx)
		source.openingCancel = cancelOpen
		openingRevision := entry.capabilityRevision
		openingSourceKey := source.key
		openingDescriptor := source.descriptor
		openingExpiresAt := source.expiresAt
		input := playback.ResolveInput{
			SourceRef: source.sourceRef, TitleID: entry.itemID, StartSeconds: float64(TicksToSeconds(startTimeTicks)),
			Capabilities: clonePlaybackCapabilities(entry.capabilities), AllowTranscoding: entry.allowTranscode,
		}
		principal := clonePrincipal(entry.principal)
		registry.mu.Unlock()
		registry.closeEntries(context.WithoutCancel(ctx), stale)

		delivery, openErr := registry.playback.Open(openContext, principal, input)
		cancelOpen()
		registry.bindCleanupHandle(delivery.Handle)
		var replaced playback.DeliveryHandle
		installed := false
		bindingCurrent := false
		bindingStillExists := false
		var installedBinding playSessionBinding
		registry.mu.Lock()
		currentEntry := registry.entries[playID]
		var currentSource *playSessionSource
		if currentEntry != nil {
			currentSource = currentEntry.sources[resolvedMediaID]
			bindingStillExists = currentSource != nil
		}
		openingCurrent := currentEntry == entry && currentSource == source && currentSource.opening == opening
		if openingCurrent {
			currentSource.openingCancel = nil
			currentSource.opening = nil
		}
		bindingCurrent = openingCurrent &&
			currentEntry.capabilityRevision == openingRevision &&
			currentEntry.itemID == input.TitleID &&
			currentEntry.allowTranscode == input.AllowTranscoding &&
			playbackCapabilitiesEqual(currentEntry.capabilities, input.Capabilities) &&
			currentSource.sourceRef == input.SourceRef &&
			currentSource.key == openingSourceKey &&
			currentSource.descriptor == openingDescriptor &&
			currentSource.expiresAt.Equal(openingExpiresAt)
		if bindingCurrent && openErr == nil && delivery.Handle.Valid() {
			replaced = currentSource.handle
			currentSource.handle = delivery.Handle
			currentSource.resolvedSession = safeDeliverySession(delivery.Session)
			currentSource.duration = deliveryDuration(delivery.Session)
			currentSource.startTimeTicks = startTimeTicks
			currentEntry.lastSeenAt = registry.now().UTC()
			currentSource.capabilityRevision = openingRevision
			installed = true
			installedBinding = bindingFor(currentEntry, currentSource)
		}
		close(opening)
		registry.mu.Unlock()
		if !bindingCurrent {
			if delivery.Handle.Valid() {
				registry.closeHandle(context.WithoutCancel(ctx), principal, delivery.Handle)
			}
			if !bindingStillExists {
				if openErr != nil {
					return playSessionBinding{}, playback.DeliveryHandle{}, playback.Session{}, openErr
				}
				return playSessionBinding{}, playback.DeliveryHandle{}, playback.Session{}, errPlaySessionNotFound
			}
			bindingChanges++
			if bindingChanges >= maxBindingChanges {
				return playSessionBinding{}, playback.DeliveryHandle{}, playback.Session{}, errPlaySessionNotFound
			}
			continue
		}
		if openErr != nil {
			if delivery.Handle.Valid() {
				registry.closeHandle(context.WithoutCancel(ctx), principal, delivery.Handle)
			}
			return playSessionBinding{}, playback.DeliveryHandle{}, playback.Session{}, openErr
		}
		if !installed {
			if delivery.Handle.Valid() {
				registry.closeHandle(context.WithoutCancel(ctx), principal, delivery.Handle)
			}
			return playSessionBinding{}, playback.DeliveryHandle{}, playback.Session{}, errPlaySessionNotFound
		}
		if replaced.Valid() {
			registry.closeHandle(context.WithoutCancel(ctx), principal, replaced)
		}
		return installedBinding, delivery.Handle, delivery.Session, nil
	}
}

func (registry *playSessionRegistry) resolveAndTouch(session AuthenticatedSession, itemID, playID, mediaID string) (playSessionBinding, error) {
	if registry == nil {
		return playSessionBinding{}, errPlaySessionNotFound
	}
	now := registry.now().UTC()
	registry.mu.Lock()
	stale := registry.removeExpiredLocked(now)
	entry, source, ok := registry.lookupLocked(session, itemID, playID, mediaID, now)
	var binding playSessionBinding
	if ok {
		entry.lastSeenAt = now
		binding = bindingFor(entry, source)
	}
	registry.mu.Unlock()
	registry.closeEntries(context.Background(), stale)
	if !ok {
		return playSessionBinding{}, errPlaySessionNotFound
	}
	return binding, nil
}

func (registry *playSessionRegistry) claimPlaybackEvent(ctx context.Context, session AuthenticatedSession, itemID, playID, mediaID string) (playSessionBinding, playbackEventLease, error) {
	if registry == nil {
		return playSessionBinding{}, playbackEventLease{}, errPlaySessionNotFound
	}
	now := registry.now().UTC()
	registry.mu.Lock()
	stale := registry.removeExpiredLocked(now)
	entry, _, ok := registry.lookupLocked(session, itemID, playID, mediaID, now)
	var token chan struct{}
	if ok {
		if entry.eventLease == nil {
			entry.eventLease = make(chan struct{}, 1)
		}
		token = entry.eventLease
	}
	registry.mu.Unlock()
	registry.closeEntries(context.WithoutCancel(ctx), stale)
	if !ok {
		return playSessionBinding{}, playbackEventLease{}, errPlaySessionNotFound
	}
	select {
	case token <- struct{}{}:
	case <-ctx.Done():
		return playSessionBinding{}, playbackEventLease{}, ctx.Err()
	}

	now = registry.now().UTC()
	registry.mu.Lock()
	stale = registry.removeExpiredLocked(now)
	current, source, currentOK := registry.lookupLocked(session, itemID, playID, mediaID, now)
	currentOK = currentOK && current == entry && current.eventLease == token
	var binding playSessionBinding
	if currentOK {
		current.lastSeenAt = now
		binding = bindingFor(current, source)
	}
	registry.mu.Unlock()
	registry.closeEntries(context.WithoutCancel(ctx), stale)
	if !currentOK {
		<-token
		return playSessionBinding{}, playbackEventLease{}, errPlaySessionNotFound
	}
	return binding, playbackEventLease{token: token}, nil
}

func (registry *playSessionRegistry) ping(session AuthenticatedSession, playID string) error {
	if registry == nil {
		return errPlaySessionNotFound
	}
	registry.pruneDeliveryHandles()
	now := registry.now().UTC()
	registry.mu.Lock()
	stale := registry.removeExpiredLocked(now)
	entry := registry.entries[playID]
	ok := entry != nil && ownerMatches(entry, session) && entry.expiresAt.After(now) && entry.lastSeenAt.Add(registry.idleTTL).After(now)
	if ok {
		entry.lastSeenAt = now
	}
	registry.mu.Unlock()
	registry.closeEntries(context.Background(), stale)
	if !ok {
		return errPlaySessionNotFound
	}
	return nil
}

func (registry *playSessionRegistry) close(ctx context.Context, session AuthenticatedSession, itemID, playID, mediaID string) error {
	if registry == nil {
		return errPlaySessionNotFound
	}
	registry.mu.Lock()
	entry, _, ok := registry.lookupLocked(session, itemID, playID, mediaID, registry.now().UTC())
	if ok {
		delete(registry.entries, playID)
	}
	registry.mu.Unlock()
	if !ok {
		return errPlaySessionNotFound
	}
	registry.closeEntries(ctx, []*playSessionEntry{entry})
	return nil
}

func (registry *playSessionRegistry) closeSession(ctx context.Context, session AuthenticatedSession) error {
	if registry == nil {
		return nil
	}
	registry.mu.Lock()
	entries := make([]*playSessionEntry, 0)
	for id, entry := range registry.entries {
		if ownerMatches(entry, session) {
			delete(registry.entries, id)
			entries = append(entries, entry)
		}
	}
	if stored := registry.deviceProfiles[session.ID]; deviceProfileOwnerMatches(stored, session) {
		delete(registry.deviceProfiles, session.ID)
	}
	registry.mu.Unlock()
	registry.closeEntries(ctx, entries)
	return nil
}

func (registry *playSessionRegistry) run(ctx context.Context) {
	if registry == nil {
		return
	}
	period := registry.reapPeriod
	if period <= 0 {
		period = defaultPlaySessionReapPeriod
	}
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		nextAttempt, pending := registry.nextCleanupAttempt()
		if pending && !nextAttempt.IsZero() && !nextAttempt.After(registry.now().UTC()) {
			registry.runCleanup(context.WithoutCancel(ctx), nil, false)
			continue
		}
		var retryTimer *time.Timer
		var retry <-chan time.Time
		if pending && !nextAttempt.IsZero() {
			retryTimer = time.NewTimer(nextAttempt.Sub(registry.now().UTC()))
			retry = retryTimer.C
		}
		select {
		case <-ticker.C:
			if retryTimer != nil {
				retryTimer.Stop()
			}
			registry.reap(ctx)
		case <-registry.cleanupWake:
			if retryTimer != nil {
				retryTimer.Stop()
			}
		case <-retry:
			registry.runCleanup(context.WithoutCancel(ctx), nil, false)
		case <-ctx.Done():
			if retryTimer != nil {
				retryTimer.Stop()
			}
			registry.closeAll(context.WithoutCancel(ctx))
			registry.drainCleanup()
			return
		}
	}
}

func (registry *playSessionRegistry) reap(ctx context.Context) {
	registry.pruneDeliveryHandles()
	registry.mu.Lock()
	entries := registry.removeExpiredLocked(registry.now().UTC())
	registry.mu.Unlock()
	registry.closeEntries(context.WithoutCancel(ctx), entries)
}

func (registry *playSessionRegistry) pruneDeliveryHandles() {
	if pruner, ok := registry.playback.(deliveryHandlePruner); ok {
		pruner.PruneDeliveryHandles()
	}
}

func (registry *playSessionRegistry) closeAll(ctx context.Context) {
	registry.cleanupMu.Lock()
	registry.cleanupRetiring = true
	registry.cleanupMu.Unlock()
	cleanupContext, cancel := registry.cleanupContext(ctx)
	defer cancel()
	registry.mu.Lock()
	entries := make([]*playSessionEntry, 0, len(registry.entries))
	for id, entry := range registry.entries {
		delete(registry.entries, id)
		entries = append(entries, entry)
	}
	registry.mu.Unlock()
	registry.runCleanupContext(cleanupContext, registry.detachEntries(entries), true)
	registry.drainCleanupContext(cleanupContext)
}

// drainCleanup keeps the retired generation alive while its owned delivery
// handles are retried. It returns only after every close succeeds or the
// retry TTL expires, so a replacement generation cannot orphan pending work.
func (registry *playSessionRegistry) drainCleanup() {
	if registry == nil {
		return
	}
	ttl := registry.cleanupRetryTTL
	if ttl <= 0 {
		ttl = defaultPlaySessionCleanupRetryTTL
	}
	drainContext, cancel := context.WithTimeout(context.Background(), ttl)
	defer cancel()
	if registry.drainCleanupContext(drainContext) {
		return
	}
	registry.abandonCleanup()
}

func (registry *playSessionRegistry) drainCleanupContext(ctx context.Context) bool {
	for ctx.Err() == nil {
		nextAttempt, exists := registry.nextCleanupAttempt()
		if !exists {
			return true
		}
		if nextAttempt.IsZero() {
			select {
			case <-registry.cleanupWake:
				continue
			case <-ctx.Done():
				return false
			}
		}
		delay := nextAttempt.Sub(registry.now().UTC())
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-registry.cleanupWake:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				continue
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return false
			}
		}
		attemptContext, cancelAttempt := registry.cleanupAttemptContext(ctx)
		registry.runCleanupContext(attemptContext, nil, false)
		cancelAttempt()
	}
	return false
}

func (registry *playSessionRegistry) abandonCleanup() {
	registry.cleanupMu.Lock()
	defer registry.cleanupMu.Unlock()
	registry.cleanupPending = make(map[playback.DeliveryHandle]*pendingPlaySessionClose)
	registry.cleanupOwned = make(map[playback.DeliveryHandle]struct{})
	registry.cleanupReservations = 0
	registry.cleanupAbandoned = true
}

func (registry *playSessionRegistry) lookupLocked(session AuthenticatedSession, itemID, playID, mediaID string, now time.Time) (*playSessionEntry, *playSessionSource, bool) {
	entry := registry.entries[playID]
	if entry == nil || !ownerMatches(entry, session) || itemID != "" && entry.itemID != itemID || !entry.expiresAt.After(now) || !entry.lastSeenAt.Add(registry.idleTTL).After(now) {
		return nil, nil, false
	}
	if mediaID == "" {
		var selected *playSessionSource
		for _, candidateID := range entry.sourceOrder {
			candidate := entry.sources[candidateID]
			if candidate == nil || !candidate.handle.Valid() {
				continue
			}
			if selected != nil {
				return nil, nil, false
			}
			selected = candidate
		}
		if selected == nil {
			return nil, nil, false
		}
		return entry, selected, true
	}
	source := entry.sources[mediaID]
	if source == nil || !source.handle.Valid() && !entry.referencesPinned && !source.expiresAt.After(now) {
		return nil, nil, false
	}
	return entry, source, true
}

func (registry *playSessionRegistry) removeExpiredLocked(now time.Time) []*playSessionEntry {
	removed := make([]*playSessionEntry, 0)
	for id, entry := range registry.entries {
		if !entry.expiresAt.After(now) || !entry.lastSeenAt.Add(registry.idleTTL).After(now) {
			delete(registry.entries, id)
			removed = append(removed, entry)
		}
	}
	for id, stored := range registry.deviceProfiles {
		if !stored.expiresAt.After(now) || !stored.lastSeenAt.Add(registry.idleTTL).After(now) {
			delete(registry.deviceProfiles, id)
		}
	}
	return removed
}

func (registry *playSessionRegistry) ownerCountLocked(session AuthenticatedSession) int {
	count := 0
	for _, entry := range registry.entries {
		if quotaOwnerMatches(entry, session) {
			count++
		}
	}
	return count
}

func (registry *playSessionRegistry) userCountLocked(userID string) int {
	count := 0
	for _, entry := range registry.entries {
		if entry.principal.UserID == userID {
			count++
		}
	}
	return count
}

func (registry *playSessionRegistry) oldestUserLocked(userID string) *playSessionEntry {
	var oldest *playSessionEntry
	for _, entry := range registry.entries {
		if entry.principal.UserID != userID {
			continue
		}
		if oldest == nil || entry.lastSeenAt.Before(oldest.lastSeenAt) ||
			entry.lastSeenAt.Equal(oldest.lastSeenAt) && (entry.createdAt.Before(oldest.createdAt) ||
				entry.createdAt.Equal(oldest.createdAt) && entry.sequence < oldest.sequence) {
			oldest = entry
		}
	}
	return oldest
}

func (registry *playSessionRegistry) oldestOwnerLocked(session AuthenticatedSession) *playSessionEntry {
	var oldest *playSessionEntry
	for _, entry := range registry.entries {
		if !quotaOwnerMatches(entry, session) {
			continue
		}
		if oldest == nil || entry.lastSeenAt.Before(oldest.lastSeenAt) ||
			entry.lastSeenAt.Equal(oldest.lastSeenAt) && (entry.createdAt.Before(oldest.createdAt) ||
				entry.createdAt.Equal(oldest.createdAt) && entry.sequence < oldest.sequence) {
			oldest = entry
		}
	}
	return oldest
}

func (registry *playSessionRegistry) closeEntries(ctx context.Context, entries []*playSessionEntry) {
	if registry == nil || registry.playback == nil {
		return
	}
	registry.runCleanup(ctx, registry.detachEntries(entries), false)
}

func (registry *playSessionRegistry) detachEntries(entries []*playSessionEntry) []pendingPlaySessionClose {
	if len(entries) == 0 {
		return nil
	}
	pending := make([]pendingPlaySessionClose, 0)
	cancels := make([]context.CancelFunc, 0)
	type pendingRelease struct {
		principal  auth.Principal
		references []string
	}
	releases := make([]pendingRelease, 0, len(entries))
	registry.mu.Lock()
	for _, entry := range entries {
		entry.eventLease = nil
		if entry.referencesPinned {
			releases = append(releases, pendingRelease{principal: clonePrincipal(entry.principal), references: sourceReferences(entry)})
			entry.referencesPinned = false
		}
		for _, mediaID := range entry.sourceOrder {
			source := entry.sources[mediaID]
			if source == nil {
				continue
			}
			source.resolvedSession = playback.Session{}
			if source.openingCancel != nil {
				cancels = append(cancels, source.openingCancel)
				source.openingCancel = nil
			}
			if !source.handle.Valid() {
				continue
			}
			pending = append(pending, pendingPlaySessionClose{principal: clonePrincipal(entry.principal), handle: source.handle})
			source.handle = playback.DeliveryHandle{}
			source.duration = 0
		}
	}
	registry.mu.Unlock()
	if pinner, ok := registry.playback.(sourceReferencePinner); ok {
		for _, release := range releases {
			pinner.UnpinSourceReferences(release.principal, release.references)
		}
	}
	for _, cancel := range cancels {
		cancel()
	}
	return pending
}

func (registry *playSessionRegistry) closeHandle(ctx context.Context, principal auth.Principal, handle playback.DeliveryHandle) {
	if registry == nil || registry.playback == nil || !handle.Valid() {
		return
	}
	registry.runCleanup(ctx, []pendingPlaySessionClose{{principal: clonePrincipal(principal), handle: handle}}, false)
}

func (registry *playSessionRegistry) runCleanup(ctx context.Context, pending []pendingPlaySessionClose, force bool) {
	cleanupContext, cancel := registry.cleanupContext(ctx)
	defer cancel()
	registry.runCleanupContext(cleanupContext, pending, force)
}

func (registry *playSessionRegistry) runCleanupContext(cleanupContext context.Context, pending []pendingPlaySessionClose, force bool) {
	claimed := registry.claimCleanup(pending, registry.now().UTC(), force)
	if len(claimed) == 0 {
		return
	}
	workers := registry.cleanupWorkers
	if workers <= 0 {
		workers = defaultPlaySessionCleanupWorkers
	}
	workers = min(workers, len(claimed))
	var jobsMu sync.Mutex
	next := 0
	completed := make(chan struct{}, workers)
	for range workers {
		go func() {
			defer func() { completed <- struct{}{} }()
			for {
				jobsMu.Lock()
				if next >= len(claimed) {
					jobsMu.Unlock()
					return
				}
				item := claimed[next]
				next++
				jobsMu.Unlock()
				var closeErr error
				if !registry.acquireCleanupSlot(cleanupContext) {
					closeErr = cleanupContext.Err()
					if closeErr == nil {
						closeErr = context.Canceled
					}
				} else {
					closeErr = registry.playback.Close(cleanupContext, item.principal, item.handle)
					<-registry.cleanupSlots
				}
				registry.finishCleanup(item, closeErr, registry.now().UTC())
			}
		}()
	}
	for range workers {
		select {
		case <-completed:
		case <-cleanupContext.Done():
			return
		}
	}
}

func (registry *playSessionRegistry) nextCleanupAttempt() (time.Time, bool) {
	registry.cleanupMu.Lock()
	defer registry.cleanupMu.Unlock()
	var next time.Time
	for _, item := range registry.cleanupPending {
		if item.claimed {
			continue
		}
		if next.IsZero() || item.nextAttempt.Before(next) {
			next = item.nextAttempt
		}
	}
	outstanding := len(registry.cleanupPending) > 0
	if registry.cleanupRetiring && (len(registry.cleanupOwned) > 0 || registry.cleanupReservations > 0) {
		outstanding = true
	}
	return next, outstanding
}

func (registry *playSessionRegistry) reserveCleanupHandle() bool {
	registry.cleanupMu.Lock()
	defer registry.cleanupMu.Unlock()
	if registry.cleanupRetiring {
		return false
	}
	if registry.cleanupOwned == nil {
		registry.cleanupOwned = make(map[playback.DeliveryHandle]struct{})
	}
	if len(registry.cleanupOwned)+registry.cleanupReservations >= registry.cleanupLimitLocked() {
		return false
	}
	registry.cleanupReservations++
	return true
}

func (registry *playSessionRegistry) bindCleanupHandle(handle playback.DeliveryHandle) {
	registry.cleanupMu.Lock()
	if registry.cleanupReservations > 0 {
		registry.cleanupReservations--
	}
	if handle.Valid() {
		if registry.cleanupOwned == nil {
			registry.cleanupOwned = make(map[playback.DeliveryHandle]struct{})
		}
		registry.cleanupOwned[handle] = struct{}{}
	}
	registry.cleanupMu.Unlock()
	if registry.cleanupWake != nil {
		select {
		case registry.cleanupWake <- struct{}{}:
		default:
		}
	}
}

func (registry *playSessionRegistry) cleanupLimitLocked() int {
	if registry.cleanupQueueLimit > 0 {
		return registry.cleanupQueueLimit
	}
	return defaultPlaySessionCleanupQueueLimit
}

func (registry *playSessionRegistry) claimCleanup(pending []pendingPlaySessionClose, now time.Time, force bool) []*pendingPlaySessionClose {
	registry.cleanupMu.Lock()
	defer registry.cleanupMu.Unlock()
	if registry.cleanupPending == nil {
		registry.cleanupPending = make(map[playback.DeliveryHandle]*pendingPlaySessionClose)
	}
	if registry.cleanupOwned == nil {
		registry.cleanupOwned = make(map[playback.DeliveryHandle]struct{})
	}
	limit := registry.cleanupLimitLocked()
	ttl := registry.cleanupRetryTTL
	if ttl <= 0 {
		ttl = defaultPlaySessionCleanupRetryTTL
	}
	for handle, item := range registry.cleanupPending {
		if !item.deadline.After(now) {
			delete(registry.cleanupPending, handle)
			delete(registry.cleanupOwned, handle)
		}
	}
	for index := range pending {
		candidate := &pending[index]
		if !candidate.handle.Valid() {
			continue
		}
		if _, exists := registry.cleanupPending[candidate.handle]; exists {
			continue
		}
		if _, owned := registry.cleanupOwned[candidate.handle]; !owned {
			// Registry-opened handles reserve ownership before native creation. Adopt any
			// pre-attached in-process handle rather than dropping its cleanup capability.
			registry.cleanupOwned[candidate.handle] = struct{}{}
		}
		candidate.nextAttempt = now
		candidate.deadline = now.Add(ttl)
		registry.cleanupPending[candidate.handle] = candidate
	}
	claimed := make([]*pendingPlaySessionClose, 0, min(len(registry.cleanupPending), limit))
	for handle, item := range registry.cleanupPending {
		if !item.deadline.After(now) {
			delete(registry.cleanupPending, handle)
			delete(registry.cleanupOwned, handle)
			continue
		}
		if item.claimed || !force && item.nextAttempt.After(now) {
			continue
		}
		item.claimed = true
		claimed = append(claimed, item)
	}
	return claimed
}

func (registry *playSessionRegistry) finishCleanup(item *pendingPlaySessionClose, closeErr error, now time.Time) {
	registry.cleanupMu.Lock()
	defer func() {
		registry.cleanupMu.Unlock()
		if registry.cleanupWake != nil {
			select {
			case registry.cleanupWake <- struct{}{}:
			default:
			}
		}
	}()
	current := registry.cleanupPending[item.handle]
	if current != item {
		return
	}
	if closeErr == nil || errors.Is(closeErr, playback.ErrSessionNotFound) || registry.cleanupAbandoned || !item.deadline.After(now) {
		delete(registry.cleanupPending, item.handle)
		delete(registry.cleanupOwned, item.handle)
		return
	}
	item.claimed = false
	item.attempts++
	item.nextAttempt = now.Add(registry.cleanupRetryDelay(item.attempts))
	if item.nextAttempt.After(item.deadline) {
		item.nextAttempt = item.deadline
	}
}

func (registry *playSessionRegistry) cleanupRetryDelay(attempt uint32) time.Duration {
	base := registry.cleanupRetryBase
	if base <= 0 {
		base = defaultPlaySessionCleanupRetryBase
	}
	maximum := registry.cleanupRetryMax
	if maximum <= 0 {
		maximum = defaultPlaySessionCleanupRetryMax
	}
	delay := base
	for current := uint32(1); current < attempt && delay < maximum; current++ {
		if delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	return min(delay, maximum)
}

func (registry *playSessionRegistry) cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	timeout := registry.cleanupTimeout
	if timeout <= 0 {
		timeout = defaultPlaySessionCleanupTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func (registry *playSessionRegistry) cleanupAttemptContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := registry.cleanupTimeout
	if timeout <= 0 {
		timeout = defaultPlaySessionCleanupTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func (registry *playSessionRegistry) acquireCleanupSlot(ctx context.Context) bool {
	if registry.cleanupSlots == nil {
		return false
	}
	select {
	case registry.cleanupSlots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func ownerMatches(entry *playSessionEntry, session AuthenticatedSession) bool {
	return entry.compatSessionID == session.ID && entry.nativeSessionID == session.Principal.SessionID &&
		entry.principal.UserID == session.Principal.UserID && entry.principal.DeviceID == session.Principal.DeviceID &&
		entry.profileID == session.ProfileID && entry.deviceID == session.Client.DeviceID &&
		session.Principal.ActiveProfileID != nil && *session.Principal.ActiveProfileID == session.ProfileID
}

func quotaOwnerMatches(entry *playSessionEntry, session AuthenticatedSession) bool {
	return entry != nil && entry.principal.UserID != "" && entry.principal.UserID == session.Principal.UserID &&
		entry.profileID != "" && entry.profileID == session.ProfileID &&
		entry.deviceID != "" && entry.deviceID == session.Client.DeviceID
}

func deviceProfileOwnerMatches(stored *registeredDeviceProfile, session AuthenticatedSession) bool {
	return stored != nil && stored.session.ID == session.ID && stored.session.Principal.SessionID == session.Principal.SessionID &&
		stored.session.Principal.UserID == session.Principal.UserID && stored.session.Principal.DeviceID == session.Principal.DeviceID &&
		stored.session.ProfileID == session.ProfileID && stored.session.Client.DeviceID == session.Client.DeviceID &&
		session.Principal.ActiveProfileID != nil && *session.Principal.ActiveProfileID == session.ProfileID
}

func (registry *playSessionRegistry) deviceProfileOwnerCountLocked(session AuthenticatedSession) int {
	count := 0
	for _, candidate := range registry.deviceProfiles {
		if deviceProfileQuotaOwnerMatches(candidate, session) {
			count++
		}
	}
	return count
}

func (registry *playSessionRegistry) deviceProfileUserCountLocked(userID string) int {
	count := 0
	for _, candidate := range registry.deviceProfiles {
		if candidate.session.Principal.UserID == userID {
			count++
		}
	}
	return count
}

func (registry *playSessionRegistry) oldestDeviceProfileLocked(matches func(*registeredDeviceProfile) bool) string {
	oldestID := ""
	var oldest time.Time
	for id, candidate := range registry.deviceProfiles {
		if !matches(candidate) {
			continue
		}
		if oldestID == "" || candidate.lastSeenAt.Before(oldest) {
			oldestID, oldest = id, candidate.lastSeenAt
		}
	}
	return oldestID
}

func deviceProfileQuotaOwnerMatches(stored *registeredDeviceProfile, session AuthenticatedSession) bool {
	return stored != nil && stored.session.Principal.UserID == session.Principal.UserID &&
		stored.session.ProfileID == session.ProfileID && stored.session.Client.DeviceID == session.Client.DeviceID
}

func cloneDeviceProfile(profile DeviceProfile) DeviceProfile {
	profile.DirectPlayProfiles = append([]DirectPlayProfile(nil), profile.DirectPlayProfiles...)
	profile.TranscodingProfiles = append([]TranscodingProfile(nil), profile.TranscodingProfiles...)
	profile.SubtitleProfiles = append([]SubtitleProfile(nil), profile.SubtitleProfiles...)
	return profile
}

func validPlaySessionOwner(session AuthenticatedSession) bool {
	return session.ID != "" && session.ProfileID != "" && session.Principal.SessionID != "" &&
		session.Principal.ActiveProfileID != nil && *session.Principal.ActiveProfileID == session.ProfileID
}

func bindingFor(entry *playSessionEntry, source *playSessionSource) playSessionBinding {
	return playSessionBinding{ItemID: entry.itemID, PlaySessionID: entry.playSessionID, MediaSourceID: source.descriptor.ID, DurationSeconds: source.duration}
}

func deliveryDuration(session playback.Session) float64 {
	for _, source := range session.Sources {
		if source.ID == session.SelectedSourceID && source.Media != nil && source.Media.DurationSeconds > 0 {
			return source.Media.DurationSeconds
		}
	}
	return 0
}

func compatibilitySourceName(ordinal, reportedHeight int) string {
	name := fmt.Sprintf("Source %d", ordinal)
	switch reportedHeight {
	case 2160, 1080, 720, 480:
		return fmt.Sprintf("%s · %dp", name, reportedHeight)
	default:
		return name
	}
}

func newPlaySessionID() (string, error) {
	var entropy [24]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(entropy[:]), nil
}

func derivedMediaSourceID(playID string, index int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("rivune-jellyfin-media-source\x00%s\x00%d", playID, index)))
	digest[6] = digest[6]&0x0f | 0x50
	digest[8] = digest[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", digest[0:4], digest[4:6], digest[6:8], digest[8:10], digest[10:16])
}

func safeDeliverySession(session playback.Session) playback.Session {
	result := playback.Session{SelectedSourceID: session.SelectedSourceID, SelectedAudioTrack: cloneIntPointer(session.SelectedAudioTrack)}
	for _, source := range session.Sources {
		if source.ID != session.SelectedSourceID {
			continue
		}
		safeSource := playback.Source{ID: source.ID, Mode: source.Mode, Container: source.Container}
		if source.Media != nil {
			media := *source.Media
			media.VideoTracks = append([]playback.MediaTrack(nil), source.Media.VideoTracks...)
			media.AudioTracks = append([]playback.MediaTrack(nil), source.Media.AudioTracks...)
			media.SubtitleTracks = append([]playback.MediaTrack(nil), source.Media.SubtitleTracks...)
			safeSource.Media = &media
		}
		result.Sources = []playback.Source{safeSource}
		break
	}
	return result
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func clonePrincipal(principal auth.Principal) auth.Principal {
	copy := principal
	if principal.ActiveProfileID != nil {
		value := *principal.ActiveProfileID
		copy.ActiveProfileID = &value
	}
	if principal.ProfileGrantExpiresAt != nil {
		value := *principal.ProfileGrantExpiresAt
		copy.ProfileGrantExpiresAt = &value
	}
	copy.ProfileContextHash = append([]byte(nil), principal.ProfileContextHash...)
	return copy
}

func playbackCapabilitiesEqual(left, right playback.Capabilities) bool {
	return slices.Equal(left.StreamingProtocols, right.StreamingProtocols) &&
		slices.Equal(left.Containers, right.Containers) &&
		slices.Equal(left.VideoCodecs, right.VideoCodecs) &&
		slices.Equal(left.AudioCodecs, right.AudioCodecs) &&
		slices.Equal(left.HDRFormats, right.HDRFormats) &&
		slices.Equal(left.ExternalPlayers, right.ExternalPlayers) &&
		slices.Equal(left.ProcessingModes, right.ProcessingModes) &&
		slices.Equal(left.MediaProfiles, right.MediaProfiles) &&
		left.MaximumVideoBitrateKbps == right.MaximumVideoBitrateKbps &&
		left.MaximumAudioChannels == right.MaximumAudioChannels &&
		slices.Equal(left.SubtitleModes, right.SubtitleModes) &&
		left.MaximumHeight == right.MaximumHeight &&
		optionalBoolEqual(left.PreferDirectPlay, right.PreferDirectPlay) &&
		left.TranscodeVideoBitrateKbps == right.TranscodeVideoBitrateKbps
}

func optionalBoolEqual(left, right *bool) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func playSourceKeyFor(option playback.SourceOption) playSourceKey {
	return playSourceKey{StableIdentity: option.StableIdentity}
}

func candidateSetMatches(entry *playSessionEntry, fresh map[playSourceKey]playback.SourceOption) bool {
	if entry == nil || len(entry.sourceOrder) != len(fresh) {
		return false
	}
	seen := make(map[playSourceKey]struct{}, len(entry.sourceOrder))
	for _, id := range entry.sourceOrder {
		source := entry.sources[id]
		if source == nil || source.key.StableIdentity == "" {
			return false
		}
		if _, duplicate := seen[source.key]; duplicate {
			return false
		}
		seen[source.key] = struct{}{}
		if _, exists := fresh[source.key]; !exists {
			return false
		}
	}
	return true
}

func sourceReferences(entry *playSessionEntry) []string {
	references := make([]string, 0, len(entry.sourceOrder))
	for _, id := range entry.sourceOrder {
		if source := entry.sources[id]; source != nil {
			references = append(references, source.sourceRef)
		}
	}
	return references
}

func sourceReferencesFor(entry *playSessionEntry, options map[playSourceKey]playback.SourceOption) []string {
	references := make([]string, 0, len(entry.sourceOrder))
	for _, id := range entry.sourceOrder {
		source := entry.sources[id]
		if source == nil {
			return nil
		}
		option, exists := options[source.key]
		if !exists {
			return nil
		}
		references = append(references, option.SourceRef)
	}
	return references
}

func descriptorsFor(entry *playSessionEntry) []playSourceDescriptor {
	descriptors := make([]playSourceDescriptor, 0, len(entry.sourceOrder))
	for _, id := range entry.sourceOrder {
		if source := entry.sources[id]; source != nil {
			descriptors = append(descriptors, source.descriptor)
		}
	}
	return descriptors
}

func clonePlaybackCapabilities(capabilities playback.Capabilities) playback.Capabilities {
	copy := capabilities
	copy.StreamingProtocols = append([]string(nil), capabilities.StreamingProtocols...)
	copy.Containers = append([]string(nil), capabilities.Containers...)
	copy.VideoCodecs = append([]string(nil), capabilities.VideoCodecs...)
	copy.AudioCodecs = append([]string(nil), capabilities.AudioCodecs...)
	copy.HDRFormats = append([]string(nil), capabilities.HDRFormats...)
	copy.ExternalPlayers = append([]string(nil), capabilities.ExternalPlayers...)
	copy.ProcessingModes = append([]string(nil), capabilities.ProcessingModes...)
	copy.SubtitleModes = append([]string(nil), capabilities.SubtitleModes...)
	copy.MediaProfiles = append([]playback.MediaProfile(nil), capabilities.MediaProfiles...)
	if capabilities.PreferDirectPlay != nil {
		value := *capabilities.PreferDirectPlay
		copy.PreferDirectPlay = &value
	}
	return copy
}
