package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

const maximumStateBodyBytes = 1 << 20

// Watchstate is the direct domain boundary for compatibility state. It avoids
// internal HTTP and always receives the principal reloaded from the linked
// compatibility credential.
type Watchstate interface {
	GetProgress(context.Context, auth.Principal, string) (watchstate.Progress, error)
	ApplyPlaybackEventForLinkedSession(context.Context, auth.Principal, string, watchstate.UpdateProgressInput) (watchstate.Progress, error)
	SetWatchedForLinkedSession(context.Context, auth.Principal, string, bool, watchstate.CompletionInput) (watchstate.Progress, error)
	ListResume(context.Context, auth.Principal, int, int) (watchstate.ContinueItemsPage, error)
	ListNextUp(context.Context, auth.Principal, string, int, int) (watchstate.ContinueItemsPage, error)
}

func (handler *Handler) handlePlaying(response http.ResponseWriter, request *http.Request) {
	handler.handlePlaybackEvent(response, request, playbackEventPlaying)
}

func (handler *Handler) handlePlayingProgress(response http.ResponseWriter, request *http.Request) {
	handler.handlePlaybackEvent(response, request, playbackEventProgress)
}

func (handler *Handler) handlePlayingStopped(response http.ResponseWriter, request *http.Request) {
	handler.handlePlaybackEvent(response, request, playbackEventStopped)
}

func (handler *Handler) handlePlayingPing(response http.ResponseWriter, request *http.Request) {
	if !handler.hasPlaybackStateDependencies() {
		http.NotFound(response, request)
		return
	}
	session, ok := handler.authenticateRequest(response, request, false)
	if !ok {
		return
	}
	var input struct {
		PlaySessionID string `json:"PlaySessionId"`
	}
	if err := decodeStateBody(request, &input); err != nil && !errors.Is(err, io.EOF) {
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The playback event is invalid")
		return
	}
	queryID, _, err := queryScalar(request.URL.Query(), "PlaySessionId")
	if err != nil || queryID != "" && input.PlaySessionID != "" && queryID != input.PlaySessionID {
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The playback event is invalid")
		return
	}
	playSessionID := strings.TrimSpace(input.PlaySessionID)
	if playSessionID == "" {
		playSessionID = strings.TrimSpace(queryID)
	}
	if !boundedStateSelector(playSessionID) || handler.playSessions.ping(session, playSessionID) != nil {
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "The playback session was not found")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

type playbackEventKind uint8

const (
	playbackEventPlaying playbackEventKind = iota
	playbackEventProgress
	playbackEventStopped
)

func (handler *Handler) handlePlaybackEvent(response http.ResponseWriter, request *http.Request, kind playbackEventKind) {
	if !handler.hasPlaybackStateDependencies() {
		http.NotFound(response, request)
		return
	}
	session, ok := handler.authenticateRequest(response, request, false)
	if !ok {
		return
	}
	var input PlaybackProgressInfo
	if err := decodeStateBody(request, &input); err != nil ||
		input.PositionTicks < 0 || input.PlaybackStartTimeTicks < 0 ||
		!boundedStateSelector(input.ItemId) || !boundedStateSelector(input.PlaySessionId) ||
		input.MediaSourceId != "" && !boundedStateSelector(input.MediaSourceId) {
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The playback event is invalid")
		return
	}
	if input.UserId != "" && !strings.EqualFold(strings.TrimSpace(input.UserId), session.ProfileID) {
		http.NotFound(response, request)
		return
	}
	binding, lease, err := handler.playSessions.claimPlaybackEvent(
		request.Context(), session,
		strings.TrimSpace(input.ItemId),
		strings.TrimSpace(input.PlaySessionId),
		strings.TrimSpace(input.MediaSourceId),
	)
	if err != nil {
		if !errors.Is(err, errPlaySessionNotFound) {
			handler.writeStateError(response, err)
			return
		}
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "The playback session was not found")
		return
	}
	defer lease.release()

	position := ticksToStateSeconds(input.PositionTicks)
	duration := durationToStateSeconds(binding.DurationSeconds)
	_, progressErr := applyPlaybackProgress(
		request.Context(), handler.watchstate, session.Principal, binding.ItemID,
		position, duration, kind == playbackEventPlaying && (duration == 0 || position < duration), kind == playbackEventStopped,
	)
	if progressErr != nil {
		handler.writeStateError(response, progressErr)
		return
	}
	var closeErr error
	if kind == playbackEventStopped {
		closeErr = handler.playSessions.close(
			request.Context(), session, binding.ItemID, binding.PlaySessionID, binding.MediaSourceID,
		)
	}
	if closeErr != nil {
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "The playback session was not found")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) handlePlayedItem(response http.ResponseWriter, request *http.Request) {
	handler.handlePlayedState(response, request, false)
}

func (handler *Handler) handleUserPlayedItem(response http.ResponseWriter, request *http.Request) {
	handler.handlePlayedState(response, request, true)
}

func (handler *Handler) handlePlayedState(response http.ResponseWriter, request *http.Request, userless bool) {
	if !handler.hasStateDependencies() {
		http.NotFound(response, request)
		return
	}
	session, ok := handler.authenticateRequest(response, request, false)
	if !ok {
		return
	}
	if !userless && !strings.EqualFold(strings.TrimSpace(request.PathValue("userId")), session.ProfileID) {
		http.NotFound(response, request)
		return
	}
	itemID := strings.TrimSpace(request.PathValue("itemId"))
	item, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, itemID)
	if err != nil || !strings.EqualFold(item.ID, itemID) {
		http.NotFound(response, request)
		return
	}
	completed := request.Method == http.MethodPost
	progress, err := setWatchedIdempotent(request.Context(), handler.watchstate, session.Principal, item.ID, completed)
	if err != nil {
		handler.writeStateError(response, err)
		return
	}
	handler.writeJSON(response, http.StatusOK, userDataFromProgress(progress, item.InLibrary))
}

func (handler *Handler) handleResumeItems(response http.ResponseWriter, request *http.Request) {
	handler.handleResume(response, request, false)
}

func (handler *Handler) handleUserResumeItems(response http.ResponseWriter, request *http.Request) {
	handler.handleResume(response, request, true)
}

func (handler *Handler) handleResume(response http.ResponseWriter, request *http.Request, userless bool) {
	if !handler.hasStateDependencies() {
		http.NotFound(response, request)
		return
	}
	session, ok := handler.authenticateRequest(response, request, false)
	if !ok {
		return
	}
	if !userless && !strings.EqualFold(strings.TrimSpace(request.PathValue("id")), session.ProfileID) {
		http.NotFound(response, request)
		return
	}
	query, err := ParseItemQuery(request.URL.Query())
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The item query is invalid")
		return
	}
	mediaTypes, err := commaSeparated(request.URL.Query(), "MediaTypes")
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The item query is invalid")
		return
	}
	if len(mediaTypes) != 0 && !containsFold(mediaTypes, "Video") {
		handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{Items: []BaseItemDto{}, TotalRecordCount: 0, StartIndex: query.StartIndex})
		return
	}
	if !feedOrderOnly(request, query) {
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "Resume sorting is not supported")
		return
	}
	page, err := handler.watchstate.ListResume(request.Context(), session.Principal, query.StartIndex, query.Limit)
	if err != nil {
		handler.writeStateError(response, err)
		return
	}
	handler.writeContinueItems(response, request, session.Principal, page, query.EnableUserData)
}

func (handler *Handler) handleNextUp(response http.ResponseWriter, request *http.Request) {
	if !handler.hasStateDependencies() {
		http.NotFound(response, request)
		return
	}
	session, ok := handler.authenticateRequest(response, request, false)
	if !ok {
		return
	}
	query, err := ParseItemQuery(request.URL.Query())
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The item query is invalid")
		return
	}
	if !feedOrderOnly(request, query) {
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "Next-up sorting is not supported")
		return
	}
	seriesID, err := boundedString(request.URL.Query(), "SeriesId", MaximumQueryValueBytes)
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The item query is invalid")
		return
	}
	if seriesID != "" {
		parsed, parseErr := parseUUID(seriesID)
		if parseErr != nil {
			handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The item query is invalid")
			return
		}
		seriesID = formatUUID(parsed)
	}
	page, err := handler.watchstate.ListNextUp(request.Context(), session.Principal, seriesID, query.StartIndex, query.Limit)
	if err != nil {
		handler.writeStateError(response, err)
		return
	}
	handler.writeContinueItems(response, request, session.Principal, page, query.EnableUserData)
}

func (handler *Handler) writeContinueItems(response http.ResponseWriter, request *http.Request, principal auth.Principal, page watchstate.ContinueItemsPage, includeUserData bool) {
	ids := make([]string, 0, len(page.Items))
	seen := make(map[string]struct{}, len(page.Items))
	for _, entry := range page.Items {
		id := strings.ToLower(strings.TrimSpace(entry.TitleID))
		if _, duplicate := seen[id]; !duplicate {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	projected := make(map[string]watchstate.CatalogTitle, len(ids))
	if batch, ok := handler.catalog.(catalogBatchReader); ok {
		titles, err := batch.GetCatalogTitles(request.Context(), principal, ids)
		if err != nil {
			handler.writeCatalogError(response, err)
			return
		}
		for _, title := range titles {
			projected[strings.ToLower(title.ID)] = title
		}
	} else {
		for _, id := range ids {
			title, err := handler.catalog.GetCatalogTitle(request.Context(), principal, id)
			if err != nil {
				handler.writeCatalogError(response, err)
				return
			}
			projected[strings.ToLower(title.ID)] = title
		}
	}
	items := make([]BaseItemDto, 0, len(page.Items))
	for _, entry := range page.Items {
		title, ok := projected[strings.ToLower(strings.TrimSpace(entry.TitleID))]
		if !ok || !strings.EqualFold(title.ID, entry.TitleID) {
			handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "The item was not found")
			return
		}
		items = append(items, handler.baseItemDTO(title, includeUserData))
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[BaseItemDto]{
		Items: items, TotalRecordCount: page.Total, StartIndex: page.Offset,
	})
}

func feedOrderOnly(request *http.Request, query ItemQuery) bool {
	if len(query.SortBy) != 0 {
		return false
	}
	_, supplied, err := queryScalar(request.URL.Query(), "SortOrder")
	return err == nil && !supplied
}

func (handler *Handler) hasStateDependencies() bool {
	return handler != nil && handler.authentication != nil && handler.catalog != nil && handler.watchstate != nil
}

func (handler *Handler) hasPlaybackStateDependencies() bool {
	return handler.hasStateDependencies() && handler.playSessions != nil
}

func decodeStateBody(request *http.Request, target any) error {
	if request.Body == nil {
		return io.EOF
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, maximumStateBodyBytes+1))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("playback event body must contain one JSON value")
	}
	return nil
}

func boundedStateSelector(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256
}

func ticksToStateSeconds(ticks int64) int {
	seconds := TicksToSeconds(ticks)
	if seconds > int64(maximumStateInt()) {
		return maximumStateInt()
	}
	return int(seconds)
}

func durationToStateSeconds(seconds float64) int {
	if seconds <= 0 || math.IsNaN(seconds) {
		return 0
	}
	maximum := maximumStateInt()
	if math.IsInf(seconds, 1) || seconds >= float64(maximum) {
		return maximum
	}
	return int(seconds)
}

func maximumStateInt() int {
	return int(^uint(0) >> 1)
}

func applyPlaybackProgress(ctx context.Context, service Watchstate, principal auth.Principal, itemID string, position, duration int, replay, final bool) (watchstate.Progress, error) {
	current, exists, err := loadProgress(ctx, service, principal, itemID)
	if err != nil {
		return watchstate.Progress{}, err
	}
	return updateProgressWithReload(ctx, service, principal, itemID, current, exists, position, duration, replay, final)
}

func loadProgress(ctx context.Context, service Watchstate, principal auth.Principal, itemID string) (watchstate.Progress, bool, error) {
	progress, err := service.GetProgress(ctx, principal, itemID)
	if errors.Is(err, watchstate.ErrProgressNotFound) {
		return watchstate.Progress{}, false, nil
	}
	return progress, err == nil, err
}

func updateProgressWithReload(ctx context.Context, service Watchstate, principal auth.Principal, itemID string, current watchstate.Progress, exists bool, position, duration int, replay, final bool) (watchstate.Progress, error) {
	for attempt := range 2 {
		input, changed, err := mergedProgressInput(current, exists, position, duration, replay, final)
		if err != nil {
			return watchstate.Progress{}, err
		}
		if !changed {
			return current, nil
		}
		updated, err := service.ApplyPlaybackEventForLinkedSession(ctx, principal, itemID, input)
		if !errors.Is(err, watchstate.ErrConflict) || attempt == 1 {
			return updated, err
		}
		current, exists, err = loadProgress(ctx, service, principal, itemID)
		if err != nil {
			return watchstate.Progress{}, err
		}
	}
	return watchstate.Progress{}, watchstate.ErrConflict
}

func mergedProgressInput(current watchstate.Progress, exists bool, position, duration int, replay, final bool) (watchstate.UpdateProgressInput, bool, error) {
	if duration == 0 && exists {
		duration = current.DurationSeconds
	}
	if duration == 0 && position > 0 {
		return watchstate.UpdateProgressInput{}, false, fmtStateInvalid("playback duration is unavailable")
	}
	replayingCompleted := exists && current.Completed && replay
	if exists && current.Completed && !replayingCompleted && !final {
		return watchstate.UpdateProgressInput{}, false, nil
	}
	if exists && duration > 0 && duration < current.PositionSeconds && !replayingCompleted && !final {
		duration = current.DurationSeconds
	}
	if exists && position < current.PositionSeconds && !replayingCompleted && !final {
		position = current.PositionSeconds
	}
	if duration > 0 && position > duration {
		position = duration
	}
	completed := duration > 0 && position >= duration
	input := watchstate.UpdateProgressInput{
		PositionSeconds: position,
		DurationSeconds: duration,
		Completed:       completed,
	}
	if exists {
		input.ExpectedVersion = current.Version
		changed := position != current.PositionSeconds || duration != current.DurationSeconds || completed != current.Completed
		return input, changed, nil
	}
	return input, true, nil
}

func setWatchedIdempotent(ctx context.Context, service Watchstate, principal auth.Principal, itemID string, completed bool) (watchstate.Progress, error) {
	current, exists, err := loadProgress(ctx, service, principal, itemID)
	if err != nil {
		return watchstate.Progress{}, err
	}
	return updateWatchedWithReload(ctx, service, principal, itemID, completed, current, exists)
}

func updateWatchedWithReload(ctx context.Context, service Watchstate, principal auth.Principal, itemID string, completed bool, current watchstate.Progress, exists bool) (watchstate.Progress, error) {
	for attempt := range 2 {
		if exists && current.Completed == completed {
			return current, nil
		}
		expected := int64(0)
		if exists {
			expected = current.Version
		}
		updated, err := service.SetWatchedForLinkedSession(ctx, principal, itemID, completed, watchstate.CompletionInput{ExpectedVersion: expected})
		if !errors.Is(err, watchstate.ErrConflict) || attempt == 1 {
			return updated, err
		}
		current, exists, err = loadProgress(ctx, service, principal, itemID)
		if err != nil {
			return watchstate.Progress{}, err
		}
	}
	return watchstate.Progress{}, watchstate.ErrConflict
}

func userDataFromProgress(progress watchstate.Progress, inLibrary bool) UserItemDataDto {
	value := UserItemDataDto{
		PlaybackPositionTicks: SecondsToTicks(int64(progress.PositionSeconds)),
		IsFavorite:            inLibrary,
		Played:                progress.Completed,
		Key:                   progress.TitleID,
		ItemId:                progress.TitleID,
	}
	if progress.Completed {
		value.PlayCount = 1
	}
	if !progress.LastWatchedAt.IsZero() {
		value.LastPlayedDate = progress.LastWatchedAt.UTC().Format("2006-01-02T15:04:05.0000000Z")
	}
	return value
}

func fmtStateInvalid(message string) error {
	return fmt.Errorf("%w: %s", watchstate.ErrInvalidInput, message)
}

func (handler *Handler) writeStateError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, watchstate.ErrConflict):
		handler.writeCompatError(response, http.StatusConflict, "Conflict", "The watch state changed on another device")
	case errors.Is(err, watchstate.ErrInvalidInput):
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The watch state is invalid")
	case errors.Is(err, watchstate.ErrForbidden), errors.Is(err, watchstate.ErrProfileRequired):
		handler.writeCompatError(response, http.StatusUnauthorized, "Unauthorized", "A valid compatibility token is required")
	case errors.Is(err, watchstate.ErrNotFound), errors.Is(err, watchstate.ErrProgressNotFound):
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "The item was not found")
	case errors.Is(err, watchstate.ErrOutboxCapacity):
		response.Header().Set("Retry-After", "5")
		handler.writeCompatError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "Watch synchronization is temporarily unavailable")
	default:
		handler.writeCompatError(response, http.StatusInternalServerError, "InternalServerError", "The request could not be completed")
	}
}
