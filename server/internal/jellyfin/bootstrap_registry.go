package jellyfin

import (
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultBootstrapSessionLimit      = 256
	defaultBootstrapOwnerSessionLimit = 16
	defaultBootstrapSessionIdleTTL    = 30 * time.Minute
	defaultCompatSocketLimit          = 128
	defaultCompatSocketSessionLimit   = 2
	defaultCompatSocketQueueLimit     = 32
)

type bootstrapSessionState struct {
	session       AuthenticatedSession
	lastSeenAt    time.Time
	deviceProfile *DeviceProfile
	capabilities  *ClientCapabilitiesDto
	viewingItemID string
	playback      *sessionPlaybackState
}

type sessionPlaybackState struct {
	itemID              string
	mediaSourceID       string
	playSessionID       string
	positionTicks       int64
	audioStreamIndex    *int
	subtitleStreamIndex *int
	canSeek             bool
	isPaused            bool
	isMuted             bool
	playMethod          string
	lastCheckIn         time.Time
}

type bootstrapSessionSnapshot struct {
	viewingItemID string
	playback      *sessionPlaybackState
}

type compatSocketLease struct {
	id                 uint64
	sessionID          string
	closed             chan struct{}
	outbound           chan WebSocketMessageDto
	sessionsSubscribed bool
}

type bootstrapRegistry struct {
	mu                 sync.Mutex
	sessions           map[string]*bootstrapSessionState
	sockets            map[uint64]*compatSocketLease
	nextSocketID       uint64
	sessionLimit       int
	ownerSessionLimit  int
	socketLimit        int
	socketSessionLimit int
	sessionIdleTTL     time.Duration
	now                func() time.Time
}

func newBootstrapRegistry() *bootstrapRegistry {
	return &bootstrapRegistry{
		sessions: make(map[string]*bootstrapSessionState), sockets: make(map[uint64]*compatSocketLease),
		sessionLimit: defaultBootstrapSessionLimit, ownerSessionLimit: defaultBootstrapOwnerSessionLimit,
		socketLimit: defaultCompatSocketLimit, socketSessionLimit: defaultCompatSocketSessionLimit,
		sessionIdleTTL: defaultBootstrapSessionIdleTTL,
		now:            func() time.Time { return time.Now().UTC() },
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
	return registry.ownerSessions(session, deviceID, activeWithin)
}

func (registry *bootstrapRegistry) ownerSessions(session AuthenticatedSession, deviceID string, activeWithin time.Duration) []AuthenticatedSession {
	if registry == nil {
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
func (registry *bootstrapRegistry) sessionSnapshot(session AuthenticatedSession) (bootstrapSessionSnapshot, bool) {
	if registry == nil {
		return bootstrapSessionSnapshot{}, false
	}
	now := registry.now().UTC()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.pruneLocked(now)
	current := registry.sessions[session.ID]
	if current == nil || !sameAuthenticatedSessionOwner(current.session, session) {
		return bootstrapSessionSnapshot{}, false
	}
	snapshot := bootstrapSessionSnapshot{viewingItemID: current.viewingItemID}
	if current.playback != nil {
		playback := *current.playback
		snapshot.playback = &playback
	}
	return snapshot, true
}

func (registry *bootstrapRegistry) setViewing(session AuthenticatedSession, itemID string) bool {
	if registry == nil || itemID == "" || !registry.observe(session) {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	current := registry.sessions[session.ID]
	if current == nil || !sameAuthenticatedSessionOwner(current.session, session) {
		return false
	}
	current.viewingItemID = itemID
	return true
}

func (registry *bootstrapRegistry) setPlayback(session AuthenticatedSession, binding playSessionBinding, input PlaybackProgressInfo, stopped bool) bool {
	if registry == nil || binding.ItemID == "" || !registry.observe(session) {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	current := registry.sessions[session.ID]
	if current == nil || !sameAuthenticatedSessionOwner(current.session, session) {
		return false
	}
	if stopped {
		current.playback = nil
		return true
	}
	current.viewingItemID = ""
	current.playback = &sessionPlaybackState{
		itemID: binding.ItemID, mediaSourceID: binding.MediaSourceID, playSessionID: binding.PlaySessionID,
		positionTicks: input.PositionTicks, audioStreamIndex: cloneIntPointer(input.AudioStreamIndex),
		subtitleStreamIndex: cloneIntPointer(input.SubtitleStreamIndex), canSeek: input.CanSeek,
		isPaused: input.IsPaused, isMuted: input.IsMuted,
		playMethod: canonicalCompatPlayMethod(input.PlayMethod), lastCheckIn: registry.now().UTC(),
	}
	return true
}
func (registry *bootstrapRegistry) touchPlayback(session AuthenticatedSession, playSessionID string) bool {
	if registry == nil || playSessionID == "" || !registry.observe(session) {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	current := registry.sessions[session.ID]
	if current == nil || current.playback == nil || current.playback.playSessionID != playSessionID ||
		!sameAuthenticatedSessionOwner(current.session, session) {
		return false
	}
	current.playback.lastCheckIn = registry.now().UTC()
	return true
}

func (registry *bootstrapRegistry) subscribeSessions(lease *compatSocketLease, subscribed bool) bool {
	if registry == nil || lease == nil {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.sockets[lease.id] != lease {
		return false
	}
	lease.sessionsSubscribed = subscribed
	return true
}

func (registry *bootstrapRegistry) publish(owner AuthenticatedSession, message WebSocketMessageDto, sessionsOnly bool) {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.pruneLocked(registry.now().UTC())
	for _, lease := range registry.sockets {
		state := registry.sessions[lease.sessionID]
		if state == nil || !sameBootstrapVisibilityOwner(state.session, owner) || sessionsOnly && !lease.sessionsSubscribed {
			continue
		}
		select {
		case lease.outbound <- message:
		default:
			registry.removeSocketLocked(lease)
		}
	}
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
	lease := &compatSocketLease{
		id: registry.nextSocketID, sessionID: session.ID, closed: make(chan struct{}),
		outbound: make(chan WebSocketMessageDto, defaultCompatSocketQueueLimit),
	}
	registry.sockets[lease.id] = lease
	return lease, true
}

func (registry *bootstrapRegistry) releaseSocket(lease *compatSocketLease) {
	if registry == nil || lease == nil {
		return
	}
	registry.mu.Lock()
	registry.removeSocketLocked(lease)
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
	for _, socket := range registry.sockets {
		if socket.sessionID == sessionID {
			registry.removeSocketLocked(socket)
		}
	}
}

func (registry *bootstrapRegistry) removeSocketLocked(lease *compatSocketLease) {
	if lease == nil || registry.sockets[lease.id] != lease {
		return
	}
	delete(registry.sockets, lease.id)
	select {
	case <-lease.closed:
	default:
		close(lease.closed)
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
	return left.ProfileID == right.ProfileID && left.Principal.UserID == right.Principal.UserID &&
		left.Principal.ActiveProfileID != nil && right.Principal.ActiveProfileID != nil &&
		*left.Principal.ActiveProfileID == left.ProfileID && *right.Principal.ActiveProfileID == right.ProfileID
}

func cloneAuthenticatedSession(session AuthenticatedSession) AuthenticatedSession {
	copy := session
	copy.Principal = clonePrincipal(session.Principal)
	return copy
}
