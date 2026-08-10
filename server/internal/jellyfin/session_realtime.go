package jellyfin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/websocket"

	"github.com/moodiness/rivune/server/internal/watchstate"
)

const (
	minimumCompatSessionInterval = time.Second
	maximumCompatSessionInterval = time.Minute
	maximumCompatSessionDelay    = time.Minute
)

type compatSocketCommand uint8

const (
	compatSocketKeepAlive compatSocketCommand = iota
	compatSocketSessionsStart
	compatSocketSessionsStop
)

type compatSocketInput struct {
	command      compatSocketCommand
	initialDelay time.Duration
	interval     time.Duration
}

func (handler *Handler) handleViewing(response http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.authentication == nil || handler.catalog == nil || handler.bootstrap == nil {
		http.NotFound(response, request)
		return
	}
	session, ok := handler.authenticateRequest(response, request, false)
	if !ok {
		return
	}
	if err := validateQueryBudget(request.URL.Query()); err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The viewing report is invalid")
		return
	}
	rawItemID, itemFound, itemErr := queryScalar(request.URL.Query(), "ItemId")
	rawSessionID, sessionFound, sessionErr := queryScalar(request.URL.Query(), "SessionId")
	itemID, validItemID := canonicalCompatUUID(strings.TrimSpace(rawItemID))
	if itemErr != nil || sessionErr != nil || !itemFound || !validItemID ||
		sessionFound && strings.TrimSpace(rawSessionID) != "" && !sameCompatUUID(rawSessionID, session.ID) {
		if sessionErr == nil && sessionFound && strings.TrimSpace(rawSessionID) != "" && !sameCompatUUID(rawSessionID, session.ID) {
			handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "The requested session was not found")
		} else {
			handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The viewing report is invalid")
		}
		return
	}
	if _, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, itemID); err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	if !handler.bootstrap.setViewing(session, itemID) {
		handler.writeCompatError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "The viewing report could not be stored")
		return
	}
	handler.publishSessionUpdate(request.Context(), session)
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) sessionInfos(ctx context.Context, owner AuthenticatedSession) []SessionInfoDto {
	if handler == nil || handler.bootstrap == nil {
		return []SessionInfoDto{}
	}
	sessions := handler.bootstrap.ownerSessions(owner, "", 0)
	result := make([]SessionInfoDto, 0, len(sessions))
	for _, session := range sessions {
		result = append(result, handler.sessionInfo(ctx, session))
	}
	return result
}

func (registry *bootstrapRegistry) hasSubscribers(owner AuthenticatedSession, messageTypes ...string) bool {
	if registry == nil || len(messageTypes) == 0 {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.pruneLocked(registry.now().UTC())
	for _, lease := range registry.sockets {
		state := registry.sessions[lease.sessionID]
		if state == nil || !sameBootstrapVisibilityOwner(state.session, owner) {
			continue
		}
		for _, messageType := range messageTypes {
			switch messageType {
			case "UserDataChanged":
				return true
			case "Sessions":
				if lease.sessionsSubscribed {
					return true
				}
			}
		}
	}
	return false
}

func (handler *Handler) publishSessionUpdate(ctx context.Context, owner AuthenticatedSession) {
	if handler == nil || handler.bootstrap == nil {
		return
	}
	snapshotContext, cancel := context.WithTimeout(ctx, compatSocketRevalidateTimeout)
	defer cancel()
	message, ok := newCompatSocketMessage("Sessions", handler.sessionInfos(snapshotContext, owner))
	if !ok {
		return
	}
	handler.bootstrap.publish(owner, message, true)
}

func (handler *Handler) publishItemUserData(ctx context.Context, owner AuthenticatedSession, itemID string, progress watchstate.Progress) {
	if handler == nil || handler.bootstrap == nil || handler.catalog == nil || itemID == "" {
		return
	}
	title, err := handler.catalog.GetCatalogTitle(ctx, owner.Principal, itemID)
	if err != nil || !strings.EqualFold(title.ID, itemID) {
		return
	}
	handler.publishUserDataChange(owner, userDataFromState(title, progress, title.Favorite, title.UserData))
}

func (handler *Handler) publishUserDataChange(owner AuthenticatedSession, userData UserItemDataDto) {
	if handler == nil || handler.bootstrap == nil || userData.ItemId == "" {
		return
	}
	message, ok := newCompatSocketMessage("UserDataChanged", UserDataChangeInfo{UserId: owner.ProfileID, UserDataList: []UserItemDataDto{userData}})
	if !ok {
		return
	}
	handler.bootstrap.publish(owner, message, false)
}

func (handler *Handler) publishMutationUserDataChange(owner AuthenticatedSession, userData UserItemDataDto) {
	handler.publishUserDataChange(owner, userData)
	if handler == nil || handler.bootstrap == nil || userData.ItemId == "" {
		return
	}
	itemID, valid := canonicalCompatUUID(userData.ItemId)
	if !valid {
		return
	}
	message, ok := newCompatSocketMessage("LibraryChanged", LibraryUpdateInfo{
		FoldersAddedTo: []string{}, FoldersRemovedFrom: []string{},
		ItemsAdded: []string{}, ItemsRemoved: []string{},
		ItemsUpdated: []string{itemID}, CollectionFolders: []string{},
	})
	if !ok {
		return
	}
	handler.bootstrap.publish(owner, message, false)
}

func (handler *Handler) sessionInfo(ctx context.Context, session AuthenticatedSession) SessionInfoDto {
	playableMediaTypes := []string{}
	if handler != nil && handler.catalog != nil && handler.playSessions != nil {
		playableMediaTypes = []string{"Video"}
	}
	capabilities := ClientCapabilitiesDto{PlayableMediaTypes: playableMediaTypes, SupportedCommands: []string{}, SupportsPersistentIdentifier: true}
	if handler != nil && handler.bootstrap != nil {
		if reported, ok := handler.bootstrap.clientCapabilities(session); ok {
			capabilities = reported
			playableMediaTypes = append([]string{}, reported.PlayableMediaTypes...)
		}
		if capabilities.DeviceProfile == nil {
			if profile, ok := handler.bootstrap.deviceProfile(session); ok {
				capabilities.DeviceProfile = &profile
			}
		}
	}
	if handler != nil && handler.playSessions != nil && capabilities.DeviceProfile == nil {
		if profile, ok := handler.playSessions.deviceProfile(session); ok {
			capabilities.DeviceProfile = &profile
		}
	}
	lastActivity := time.Now().UTC()
	if handler != nil && handler.bootstrap != nil {
		if observed, ok := handler.bootstrap.lastActivity(session); ok {
			lastActivity = observed
		}
	}
	result := SessionInfoDto{
		Id: session.ID, ServerId: handler.serverInfo.ID.String(), IsActive: true, UserId: session.ProfileID, UserName: session.ProfileName,
		Client: session.Client.Client, DeviceName: session.Client.Device, DeviceId: session.Client.DeviceID,
		ApplicationVersion: session.Client.Version, LastActivityDate: lastActivity,
		SupportsMediaControl: capabilities.SupportsMediaControl, SupportsRemoteControl: false,
		PlayableMediaTypes: playableMediaTypes, SupportedCommands: append([]string{}, capabilities.SupportedCommands...), Capabilities: capabilities,
		NowPlayingQueue: []QueueItemDto{}, NowPlayingQueueFullItems: []BaseItemDto{}, AdditionalUsers: []SessionUserInfoDto{},
	}
	if handler == nil || handler.bootstrap == nil || handler.catalog == nil {
		return result
	}
	snapshot, ok := handler.bootstrap.sessionSnapshot(session)
	if !ok {
		return result
	}
	if snapshot.viewingItemID != "" {
		if item, found := handler.sessionItem(ctx, session, snapshot.viewingItemID); found {
			result.NowViewingItem = &item
		}
	}
	if snapshot.playback != nil {
		playback := snapshot.playback
		result.LastPlaybackCheckIn = &playback.lastCheckIn
		if item, found := handler.sessionItem(ctx, session, playback.itemID); found {
			position := playback.positionTicks
			if item.UserData != nil {
				item.UserData.PlaybackPositionTicks = position
			}
			result.NowPlayingItem = &item
			result.PlayState = &PlayerStateInfo{
				PositionTicks: &position, CanSeek: playback.canSeek, IsPaused: playback.isPaused, IsMuted: playback.isMuted,
				AudioStreamIndex: cloneIntPointer(playback.audioStreamIndex), SubtitleStreamIndex: cloneIntPointer(playback.subtitleStreamIndex),
				MediaSourceId: playback.mediaSourceID, PlayMethod: playback.playMethod,
			}
		}
	}
	return result
}

func (handler *Handler) sessionItem(ctx context.Context, session AuthenticatedSession, itemID string) (BaseItemDto, bool) {
	title, err := handler.catalog.GetCatalogTitle(ctx, session.Principal, itemID)
	if err != nil {
		return BaseItemDto{}, false
	}
	item := handler.baseItemDTO(title, true)
	item.Path = ""
	item.MediaSources = nil
	return item, true
}

func (handler *Handler) serveRealtimeSocket(ctx context.Context, connection *websocket.Conn, session AuthenticatedSession, lease *compatSocketLease) {
	if connection == nil || lease == nil || handler == nil || handler.bootstrap == nil {
		return
	}
	defer connection.Close()
	connection.MaxPayloadBytes = maximumCompatSocketMessageBytes
	now := time.Now().UTC()
	deadline := now.Add(maximumCompatSocketLifetime)
	if session.ExpiresAt.Before(deadline) {
		deadline = session.ExpiresAt
	}
	if !deadline.After(now) {
		return
	}
	_ = connection.SetDeadline(deadline)
	initialMessage, ok := newCompatSocketMessage("ForceKeepAlive", compatSocketLostTimeoutSeconds)
	if !ok || websocket.JSON.Send(connection, initialMessage) != nil {
		return
	}

	readResult := make(chan error, 1)
	commands := make(chan compatSocketInput, 8)
	go handler.readCompatSocket(ctx, connection, lease, commands, readResult)

	keepalive := time.NewTicker(compatSocketKeepalivePeriod)
	revalidate := time.NewTicker(compatSocketRevalidatePeriod)
	lifetime := time.NewTimer(time.Until(deadline))
	lost := time.NewTimer(time.Duration(compatSocketLostTimeoutSeconds) * time.Second)
	sessionsTimer := time.NewTimer(time.Hour)
	if !sessionsTimer.Stop() {
		<-sessionsTimer.C
	}
	var sessionsTimerC <-chan time.Time
	var sessionsInterval time.Duration
	defer keepalive.Stop()
	defer revalidate.Stop()
	defer lifetime.Stop()
	defer lost.Stop()
	defer sessionsTimer.Stop()

	sendSessions := func() bool {
		snapshotContext, cancel := context.WithTimeout(ctx, compatSocketRevalidateTimeout)
		defer cancel()
		message, created := newCompatSocketMessage("Sessions", handler.sessionInfos(snapshotContext, session))
		return created && websocket.JSON.Send(connection, message) == nil
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-lease.closed:
			return
		case <-lifetime.C:
			return
		case <-lost.C:
			return
		case <-readResult:
			return
		case command := <-commands:
			handler.bootstrap.observe(session)
			resetCompatSocketTimer(lost, time.Duration(compatSocketLostTimeoutSeconds)*time.Second)
			switch command.command {
			case compatSocketKeepAlive:
				message, created := newCompatSocketMessage("KeepAlive", nil)
				if !created || websocket.JSON.Send(connection, message) != nil {
					return
				}
			case compatSocketSessionsStart:
				if !handler.bootstrap.subscribeSessions(lease, true) {
					return
				}
				sessionsInterval = command.interval
				if command.initialDelay == 0 {
					if !sendSessions() {
						return
					}
					resetCompatSocketTimer(sessionsTimer, sessionsInterval)
				} else {
					resetCompatSocketTimer(sessionsTimer, command.initialDelay)
				}
				sessionsTimerC = sessionsTimer.C
			case compatSocketSessionsStop:
				handler.bootstrap.subscribeSessions(lease, false)
				stopCompatSocketTimer(sessionsTimer)
				sessionsTimerC = nil
			}
		case <-sessionsTimerC:
			if !sendSessions() {
				return
			}
			resetCompatSocketTimer(sessionsTimer, sessionsInterval)
		case message := <-lease.outbound:
			if err := websocket.JSON.Send(connection, message); err != nil {
				return
			}
		case <-keepalive.C:
			message, created := newCompatSocketMessage("ForceKeepAlive", compatSocketLostTimeoutSeconds)
			if !created || websocket.JSON.Send(connection, message) != nil {
				return
			}
		case <-revalidate.C:
			revalidationContext, cancel := context.WithTimeout(ctx, compatSocketRevalidateTimeout)
			current, err := handler.authentication.Revalidate(revalidationContext, session)
			cancel()
			if err != nil || !sameAuthenticatedSessionOwner(session, current) {
				return
			}
			handler.bootstrap.observe(current)
		}
	}
}

func (handler *Handler) readCompatSocket(ctx context.Context, connection *websocket.Conn, lease *compatSocketLease, commands chan<- compatSocketInput, result chan<- error) {
	for range maximumCompatSocketMessages {
		var payload []byte
		if err := websocket.Message.Receive(connection, &payload); err != nil {
			result <- err
			return
		}
		command, err := decodeCompatSocketInput(payload)
		if err != nil {
			result <- err
			return
		}
		select {
		case commands <- command:
		case <-ctx.Done():
			return
		case <-lease.closed:
			return
		}
	}
	result <- errors.New("compatibility socket message limit reached")
}

func decodeCompatSocketInput(payload []byte) (compatSocketInput, error) {
	var message struct {
		MessageType string          `json:"MessageType"`
		MessageId   string          `json:"MessageId,omitempty"`
		Data        json.RawMessage `json:"Data,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil {
		return compatSocketInput{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return compatSocketInput{}, errors.New("multiple websocket JSON values")
	}
	messageType := strings.TrimSpace(message.MessageType)
	if messageType != message.MessageType || !boundedUTF8(messageType, 1, 64) ||
		message.MessageId != "" && !boundedUTF8(message.MessageId, 1, 128) {
		return compatSocketInput{}, errors.New("invalid websocket message")
	}
	switch messageType {
	case "KeepAlive":
		if !emptyJSONValue(message.Data) {
			return compatSocketInput{}, errors.New("invalid keepalive payload")
		}
		return compatSocketInput{command: compatSocketKeepAlive}, nil
	case "SessionsStop":
		if !emptyJSONValue(message.Data) {
			return compatSocketInput{}, errors.New("invalid session stop payload")
		}
		return compatSocketInput{command: compatSocketSessionsStop}, nil
	case "SessionsStart":
		delay, interval, err := parseCompatSessionSubscription(message.Data)
		if err != nil {
			return compatSocketInput{}, err
		}
		return compatSocketInput{command: compatSocketSessionsStart, initialDelay: delay, interval: interval}, nil
	default:
		return compatSocketInput{}, errors.New("unsupported websocket message type")
	}
}

func emptyJSONValue(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func parseCompatSessionSubscription(value json.RawMessage) (time.Duration, time.Duration, error) {
	if emptyJSONValue(value) {
		return 0, 5 * time.Second, nil
	}
	var parameters string
	if err := json.Unmarshal(value, &parameters); err != nil {
		return 0, 0, errors.New("invalid session subscription")
	}
	parts := strings.Split(parameters, ",")
	if len(parts) != 2 {
		return 0, 0, errors.New("invalid session subscription")
	}
	delayMilliseconds, delayErr := strconv.ParseInt(parts[0], 10, 64)
	intervalMilliseconds, intervalErr := strconv.ParseInt(parts[1], 10, 64)
	if delayErr != nil || intervalErr != nil || delayMilliseconds < 0 ||
		delayMilliseconds > int64(maximumCompatSessionDelay/time.Millisecond) ||
		intervalMilliseconds < int64(minimumCompatSessionInterval/time.Millisecond) ||
		intervalMilliseconds > int64(maximumCompatSessionInterval/time.Millisecond) {
		return 0, 0, fmt.Errorf("invalid session subscription interval")
	}
	return time.Duration(delayMilliseconds) * time.Millisecond, time.Duration(intervalMilliseconds) * time.Millisecond, nil
}

func validPlaybackStreamIndex(value *int) bool {
	if value == nil {
		return true
	}
	return *value >= -maximumCompatibilitySubtitleIndex && *value <= maximumCompatibilitySubtitleIndex
}

func validCompatPlayMethod(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "directplay", "directstream", "transcode":
		return true
	default:
		return false
	}
}

func canonicalCompatPlayMethod(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "directplay":
		return "DirectPlay"
	case "directstream":
		return "DirectStream"
	case "transcode":
		return "Transcode"
	default:
		return ""
	}
}

func stopCompatSocketTimer(timer *time.Timer) {
	if timer == nil || timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

func resetCompatSocketTimer(timer *time.Timer, duration time.Duration) {
	stopCompatSocketTimer(timer)
	timer.Reset(duration)
}

func newCompatSocketMessage(messageType string, data any) (WebSocketMessageDto, bool) {
	var identifier [16]byte
	if _, err := rand.Read(identifier[:]); err != nil {
		return WebSocketMessageDto{}, false
	}
	identifier[6] = identifier[6]&0x0f | 0x40
	identifier[8] = identifier[8]&0x3f | 0x80
	return WebSocketMessageDto{
		MessageType: messageType,
		MessageId:   fmt.Sprintf("%x-%x-%x-%x-%x", identifier[0:4], identifier[4:6], identifier[6:8], identifier[8:10], identifier[10:16]),
		Data:        data,
	}, true
}
