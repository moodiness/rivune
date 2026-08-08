package jellyfin

import (
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultBootstrapSessionLimit       = 256
	defaultBootstrapOwnerSessionLimit  = 16
	defaultBootstrapSessionIdleTTL     = 30 * time.Minute
	defaultDisplayPreferenceLimit      = 4096
	defaultDisplayPreferenceOwnerLimit = 128
	defaultCompatSocketLimit           = 128
	defaultCompatSocketSessionLimit    = 2
)

type bootstrapSessionState struct {
	session       AuthenticatedSession
	lastSeenAt    time.Time
	deviceProfile *DeviceProfile
	capabilities  *ClientCapabilitiesDto
}

type displayPreferenceKey struct {
	profileID string
	client    string
	id        string
}

type storedDisplayPreference struct {
	creatorSessionID string
	value            DisplayPreferencesDto
	expiresAt        time.Time
	lastSeenAt       time.Time
}

type compatSocketLease struct {
	id        uint64
	sessionID string
	closed    chan struct{}
}

type bootstrapRegistry struct {
	mu                   sync.Mutex
	sessions             map[string]*bootstrapSessionState
	preferences          map[displayPreferenceKey]*storedDisplayPreference
	sockets              map[uint64]*compatSocketLease
	nextSocketID         uint64
	sessionLimit         int
	ownerSessionLimit    int
	preferenceLimit      int
	preferenceOwnerLimit int
	socketLimit          int
	socketSessionLimit   int
	sessionIdleTTL       time.Duration
	now                  func() time.Time
}

func newBootstrapRegistry() *bootstrapRegistry {
	return &bootstrapRegistry{
		sessions: make(map[string]*bootstrapSessionState), preferences: make(map[displayPreferenceKey]*storedDisplayPreference),
		sockets: make(map[uint64]*compatSocketLease), sessionLimit: defaultBootstrapSessionLimit,
		ownerSessionLimit: defaultBootstrapOwnerSessionLimit, preferenceLimit: defaultDisplayPreferenceLimit,
		preferenceOwnerLimit: defaultDisplayPreferenceOwnerLimit, socketLimit: defaultCompatSocketLimit,
		socketSessionLimit: defaultCompatSocketSessionLimit, sessionIdleTTL: defaultBootstrapSessionIdleTTL,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (registry *bootstrapRegistry) observe(session AuthenticatedSession) bool {
	if registry == nil || !validAuthenticatedSession(session) {
		return false
	}
	now := registry.now().UTC()
	if !session.ExpiresAt.After(now) {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.pruneLocked(now)
	if current := registry.sessions[session.ID]; current != nil {
		if !sameAuthenticatedSessionOwner(current.session, session) {
			registry.removeSessionLocked(session.ID)
			return false
		}
		current.session = cloneAuthenticatedSession(session)
		current.lastSeenAt = now
		return true
	}
	if len(registry.sessions) >= registry.positiveLimit(registry.sessionLimit, defaultBootstrapSessionLimit) ||
		registry.ownerSessionCountLocked(session) >= registry.positiveLimit(registry.ownerSessionLimit, defaultBootstrapOwnerSessionLimit) {
		return false
	}
	registry.sessions[session.ID] = &bootstrapSessionState{session: cloneAuthenticatedSession(session), lastSeenAt: now}
	return true
}

func (registry *bootstrapRegistry) visibleSessions(session AuthenticatedSession, deviceID string, activeWithin time.Duration) []AuthenticatedSession {
	if registry == nil || !registry.observe(session) {
		return []AuthenticatedSession{}
	}
	deviceID = strings.TrimSpace(deviceID)
	now := registry.now().UTC()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.pruneLocked(now)
	states := make([]*bootstrapSessionState, 0, registry.positiveLimit(registry.ownerSessionLimit, defaultBootstrapOwnerSessionLimit))
	for _, candidate := range registry.sessions {
		if !sameBootstrapVisibilityOwner(candidate.session, session) || deviceID != "" && candidate.session.Client.DeviceID != deviceID ||
			activeWithin > 0 && candidate.lastSeenAt.Add(activeWithin).Before(now) {
			continue
		}
		states = append(states, candidate)
	}
	sort.Slice(states, func(left, right int) bool {
		if states[left].lastSeenAt.Equal(states[right].lastSeenAt) {
			return states[left].session.ID < states[right].session.ID
		}
		return states[left].lastSeenAt.After(states[right].lastSeenAt)
	})
	result := make([]AuthenticatedSession, 0, len(states))
	for _, candidate := range states {
		result = append(result, cloneAuthenticatedSession(candidate.session))
	}
	return result
}

func (registry *bootstrapRegistry) setDeviceProfile(session AuthenticatedSession, profile DeviceProfile) bool {
	if registry == nil || !validDeviceProfileBounds(profile) || !hasDeviceProfileData(profile) || !registry.observe(session) {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	current := registry.sessions[session.ID]
	if current == nil || !sameAuthenticatedSessionOwner(current.session, session) {
		return false
	}
	copy := cloneDeviceProfile(profile)
	current.deviceProfile = &copy
	return true
}

func (registry *bootstrapRegistry) deviceProfile(session AuthenticatedSession) (DeviceProfile, bool) {
	if registry == nil {
		return DeviceProfile{}, false
	}
	now := registry.now().UTC()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.pruneLocked(now)
	current := registry.sessions[session.ID]
	if current == nil || current.deviceProfile == nil || !sameAuthenticatedSessionOwner(current.session, session) {
		return DeviceProfile{}, false
	}
	return cloneDeviceProfile(*current.deviceProfile), true
}

func (registry *bootstrapRegistry) lastActivity(session AuthenticatedSession) (time.Time, bool) {
	if registry == nil {
		return time.Time{}, false
	}
	now := registry.now().UTC()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.pruneLocked(now)
	current := registry.sessions[session.ID]
	if current == nil || !sameAuthenticatedSessionOwner(current.session, session) {
		return time.Time{}, false
	}
	return current.lastSeenAt, true
}

func (registry *bootstrapRegistry) setClientCapabilities(session AuthenticatedSession, capabilities ClientCapabilitiesDto, replace bool) bool {
	if registry == nil || !validClientCapabilities(capabilities) || !registry.observe(session) {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	current := registry.sessions[session.ID]
	if current == nil || !sameAuthenticatedSessionOwner(current.session, session) {
		return false
	}
	if replace || current.capabilities == nil {
		copy := cloneClientCapabilities(capabilities)
		current.capabilities = &copy
	} else if capabilities.DeviceProfile != nil {
		profile := cloneDeviceProfile(*capabilities.DeviceProfile)
		current.capabilities.DeviceProfile = &profile
	}
	return true
}

func (registry *bootstrapRegistry) clientCapabilities(session AuthenticatedSession) (ClientCapabilitiesDto, bool) {
	if registry == nil {
		return ClientCapabilitiesDto{}, false
	}
	now := registry.now().UTC()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.pruneLocked(now)
	current := registry.sessions[session.ID]
	if current == nil || current.capabilities == nil || !sameAuthenticatedSessionOwner(current.session, session) {
		return ClientCapabilitiesDto{}, false
	}
	return cloneClientCapabilities(*current.capabilities), true
}

func (registry *bootstrapRegistry) displayPreference(session AuthenticatedSession, client, id string) (DisplayPreferencesDto, bool) {
	if registry == nil || !registry.observe(session) {
		return DisplayPreferencesDto{}, false
	}
	key := newDisplayPreferenceKey(session.ProfileID, client, id)
	now := registry.now().UTC()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.pruneLocked(now)
	stored := registry.preferences[key]
	if stored == nil || !stored.expiresAt.After(now) {
		return DisplayPreferencesDto{}, false
	}
	stored.lastSeenAt = now
	return cloneDisplayPreferences(stored.value), true
}

func (registry *bootstrapRegistry) setDisplayPreference(session AuthenticatedSession, client, id string, value DisplayPreferencesDto) bool {
	if registry == nil || !registry.observe(session) {
		return false
	}
	key := newDisplayPreferenceKey(session.ProfileID, client, id)
	now := registry.now().UTC()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.pruneLocked(now)
	if registry.preferences[key] == nil {
		if len(registry.preferences) >= registry.positiveLimit(registry.preferenceLimit, defaultDisplayPreferenceLimit) ||
			registry.preferenceOwnerCountLocked(key) >= registry.positiveLimit(registry.preferenceOwnerLimit, defaultDisplayPreferenceOwnerLimit) {
			return false
		}
	}
	registry.preferences[key] = &storedDisplayPreference{
		creatorSessionID: session.ID, value: cloneDisplayPreferences(value), expiresAt: session.ExpiresAt.UTC(), lastSeenAt: now,
	}
	return true
}

func (registry *bootstrapRegistry) acquireSocket(session AuthenticatedSession) (*compatSocketLease, bool) {
	if registry == nil || !registry.observe(session) {
		return nil, false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.pruneLocked(registry.now().UTC())
	if len(registry.sockets) >= registry.positiveLimit(registry.socketLimit, defaultCompatSocketLimit) {
		return nil, false
	}
	count := 0
	for _, socket := range registry.sockets {
		if socket.sessionID == session.ID {
			count++
		}
	}
	if count >= registry.positiveLimit(registry.socketSessionLimit, defaultCompatSocketSessionLimit) {
		return nil, false
	}
	registry.nextSocketID++
	lease := &compatSocketLease{id: registry.nextSocketID, sessionID: session.ID, closed: make(chan struct{})}
	registry.sockets[lease.id] = lease
	return lease, true
}

func (registry *bootstrapRegistry) releaseSocket(lease *compatSocketLease) {
	if registry == nil || lease == nil {
		return
	}
	registry.mu.Lock()
	if registry.sockets[lease.id] == lease {
		delete(registry.sockets, lease.id)
		select {
		case <-lease.closed:
		default:
			close(lease.closed)
		}
	}
	registry.mu.Unlock()
}

func (registry *bootstrapRegistry) forget(session AuthenticatedSession) {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	if current := registry.sessions[session.ID]; current != nil && sameAuthenticatedSessionOwner(current.session, session) {
		registry.removeSessionLocked(session.ID)
	}
	registry.mu.Unlock()
}

func (registry *bootstrapRegistry) removeSessionLocked(sessionID string) {
	delete(registry.sessions, sessionID)
	for key, preference := range registry.preferences {
		if preference.creatorSessionID == sessionID {
			delete(registry.preferences, key)
		}
	}
	for id, socket := range registry.sockets {
		if socket.sessionID != sessionID {
			continue
		}
		delete(registry.sockets, id)
		select {
		case <-socket.closed:
		default:
			close(socket.closed)
		}
	}
}

func (registry *bootstrapRegistry) pruneLocked(now time.Time) {
	idleTTL := registry.sessionIdleTTL
	if idleTTL <= 0 {
		idleTTL = defaultBootstrapSessionIdleTTL
	}
	for id, state := range registry.sessions {
		if !state.session.ExpiresAt.After(now) || !state.lastSeenAt.Add(idleTTL).After(now) {
			registry.removeSessionLocked(id)
		}
	}
	for key, preference := range registry.preferences {
		if !preference.expiresAt.After(now) {
			delete(registry.preferences, key)
		}
	}
}

func (registry *bootstrapRegistry) ownerSessionCountLocked(session AuthenticatedSession) int {
	count := 0
	for _, candidate := range registry.sessions {
		if sameBootstrapVisibilityOwner(candidate.session, session) {
			count++
		}
	}
	return count
}

func (registry *bootstrapRegistry) preferenceOwnerCountLocked(key displayPreferenceKey) int {
	count := 0
	for candidate := range registry.preferences {
		if candidate.profileID == key.profileID && candidate.client == key.client {
			count++
		}
	}
	return count
}

func (*bootstrapRegistry) positiveLimit(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func hasDeviceProfileData(profile DeviceProfile) bool {
	return strings.TrimSpace(profile.Name) != "" || profile.MaxStreamingBitrate != 0 || !emptyDeviceProfile(profile)
}

func validClientCapabilities(capabilities ClientCapabilitiesDto) bool {
	if len(capabilities.PlayableMediaTypes) > 16 || len(capabilities.SupportedCommands) > 64 {
		return false
	}
	for _, value := range capabilities.PlayableMediaTypes {
		if !boundedUTF8(value, 1, 64) {
			return false
		}
	}
	for _, value := range capabilities.SupportedCommands {
		if !boundedUTF8(value, 1, 64) {
			return false
		}
	}
	return capabilities.DeviceProfile == nil || validDeviceProfileBounds(*capabilities.DeviceProfile)
}

func cloneClientCapabilities(capabilities ClientCapabilitiesDto) ClientCapabilitiesDto {
	copy := capabilities
	copy.PlayableMediaTypes = append([]string{}, capabilities.PlayableMediaTypes...)
	copy.SupportedCommands = append([]string{}, capabilities.SupportedCommands...)
	if capabilities.DeviceProfile != nil {
		profile := cloneDeviceProfile(*capabilities.DeviceProfile)
		copy.DeviceProfile = &profile
	}
	return copy
}

func sameBootstrapVisibilityOwner(left, right AuthenticatedSession) bool {
	return left.ProfileID == right.ProfileID && left.Client.DeviceID == right.Client.DeviceID &&
		left.Principal.UserID == right.Principal.UserID && left.Principal.ActiveProfileID != nil &&
		right.Principal.ActiveProfileID != nil && *left.Principal.ActiveProfileID == left.ProfileID && *right.Principal.ActiveProfileID == right.ProfileID
}

func cloneAuthenticatedSession(session AuthenticatedSession) AuthenticatedSession {
	copy := session
	copy.Principal = clonePrincipal(session.Principal)
	return copy
}

func newDisplayPreferenceKey(profileID, client, id string) displayPreferenceKey {
	return displayPreferenceKey{profileID: strings.ToLower(strings.TrimSpace(profileID)), client: strings.ToLower(strings.TrimSpace(client)), id: strings.ToLower(strings.TrimSpace(id))}
}

func cloneDisplayPreferences(value DisplayPreferencesDto) DisplayPreferencesDto {
	copy := value
	copy.CustomPrefs = make(map[string]string, len(value.CustomPrefs))
	for key, preference := range value.CustomPrefs {
		copy.CustomPrefs[key] = preference
	}
	return copy
}
