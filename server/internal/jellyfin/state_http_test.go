package jellyfin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

type stateAuthentication struct {
	session AuthenticatedSession
}

func (*stateAuthentication) Login(context.Context, CompatLoginInput) (LoginResult, error) {
	return LoginResult{}, ErrInvalidCompatLogin
}
func (authentication *stateAuthentication) Authenticate(context.Context, string) (AuthenticatedSession, error) {
	return authentication.session, nil
}
func (authentication *stateAuthentication) Revalidate(context.Context, AuthenticatedSession) (AuthenticatedSession, error) {
	return authentication.session, nil
}

func (*stateAuthentication) Logout(context.Context, AuthenticatedSession) error { return nil }

type stateCatalog struct {
	items      map[string]watchstate.CatalogTitle
	batchCalls int
	batchErr   error
}

func (catalog *stateCatalog) GetCatalogTitle(_ context.Context, _ auth.Principal, itemID string) (watchstate.CatalogTitle, error) {
	item, ok := catalog.items[itemID]
	if !ok {
		return watchstate.CatalogTitle{}, watchstate.ErrNotFound
	}
	return item, nil
}
func (*stateCatalog) ListCatalogItems(context.Context, auth.Principal, watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
	return watchstate.CatalogPage{}, nil
}
func (catalog *stateCatalog) GetCatalogTitles(_ context.Context, _ auth.Principal, itemIDs []string) ([]watchstate.CatalogTitle, error) {
	catalog.batchCalls++
	if catalog.batchErr != nil {
		return nil, catalog.batchErr
	}
	items := make([]watchstate.CatalogTitle, 0, len(itemIDs))
	for index := len(itemIDs) - 1; index >= 0; index-- {
		if item, ok := catalog.items[itemIDs[index]]; ok {
			items = append(items, item)
		}
	}
	return items, nil
}

type statePlaybackDelivery struct {
	onClose func()
}

func (*statePlaybackDelivery) Sources(context.Context, auth.Principal, playback.SourcesInput) (playback.SourceList, error) {
	return playback.SourceList{}, nil
}
func (*statePlaybackDelivery) Open(context.Context, auth.Principal, playback.ResolveInput) (playback.Delivery, error) {
	return playback.Delivery{}, nil
}
func (*statePlaybackDelivery) Serve(http.ResponseWriter, *http.Request, playback.DeliveryHandle) error {
	return nil
}
func (*statePlaybackDelivery) ServeAsset(http.ResponseWriter, *http.Request, playback.DeliveryHandle, string) error {
	return nil
}
func (delivery *statePlaybackDelivery) Close(context.Context, auth.Principal, playback.DeliveryHandle) error {
	if delivery.onClose != nil {
		delivery.onClose()
	}
	return nil
}

type blockingPlaybackWatchstate struct {
	*memoryWatchstate
	entered chan watchstate.UpdateProgressInput
	release chan struct{}
}

func (service *blockingPlaybackWatchstate) ApplyPlaybackEventForLinkedSession(ctx context.Context, principal auth.Principal, itemID string, input watchstate.UpdateProgressInput) (watchstate.Progress, error) {
	service.entered <- input
	select {
	case <-service.release:
		return service.memoryWatchstate.ApplyPlaybackEventForLinkedSession(ctx, principal, itemID, input)
	case <-ctx.Done():
		return watchstate.Progress{}, ctx.Err()
	}
}

func TestStoppedUpdatesProgressClosesRegistryAndRejectsDuplicates(t *testing.T) {
	handler, authentication, service, registry, token, itemID, playID, mediaID := stateHTTPFixture(t)
	body := `{"ItemId":"` + itemID + `","PlaySessionId":"` + playID + `","MediaSourceId":"` + mediaID + `","PositionTicks":500000000}`
	response := serveStateRequest(handler.handlePlayingStopped, token, body)
	if response.Code != http.StatusNoContent {
		t.Fatalf("stopped status=%d body=%s", response.Code, response.Body.String())
	}
	progress := service.progress[itemID]
	if progress.PositionSeconds != 50 || progress.DurationSeconds != 100 || progress.Completed || progress.Version != 1 {
		t.Fatalf("stopped progress = %+v", progress)
	}
	if _, exists := registry.entries[playID]; exists {
		t.Fatal("stopped playback remained in registry")
	}

	response = serveStateRequest(handler.handlePlayingStopped, token, body)
	if response.Code != http.StatusNoContent || service.progress[itemID].Version != 1 {
		t.Fatalf("duplicate stopped status=%d progress=%+v", response.Code, service.progress[itemID])
	}

	handler, authentication, service, registry, token, itemID, playID, _ = stateHTTPFixture(t)
	authentication.session.ID = "66666666-6666-4666-8666-666666666666"
	crossAccountBody := `{"ItemId":"` + itemID + `","PlaySessionId":"` + playID + `","PositionTicks":500000000}`
	response = serveStateRequest(handler.handlePlayingProgress, token, crossAccountBody)
	if response.Code != http.StatusNoContent {
		t.Fatalf("cross-account progress status=%d body=%s", response.Code, response.Body.String())
	}
	if len(registry.entries) != 1 || len(service.progress) != 0 {
		t.Fatal("cross-account request changed playback or watch state")
	}
}

func TestProgressFallsBackToUniqueNegotiatedItemAndSource(t *testing.T) {
	handler, _, service, registry, token, itemID, playID, mediaID := stateHTTPFixture(t)
	body := `{"ItemId":"` + itemID + `","PlaySessionId":"client-play-session-alias-0001","MediaSourceId":"` + mediaID + `","PositionTicks":250000000}`
	response := serveStateRequest(handler.handlePlayingProgress, token, body)
	progress := service.progress[itemID]
	if response.Code != http.StatusNoContent || progress.PositionSeconds != 25 || progress.Version != 1 || registry.entries[playID] == nil {
		t.Fatalf("fallback progress status=%d progress=%+v sessionPreserved=%t", response.Code, progress, registry.entries[playID] != nil)
	}
}

func TestStoppedPersistsBackwardSeekExactlyBeforeClosingSession(t *testing.T) {
	handler, _, service, registry, token, itemID, playID, mediaID := stateHTTPFixture(t)
	service.progress[itemID] = watchstate.Progress{
		TitleID: itemID, MediaType: "movie", PositionSeconds: 3600,
		DurationSeconds: 4000, Version: 9,
	}
	registry.entries[playID].sources[mediaID].duration = 4000
	var progressAtClose watchstate.Progress
	registry.playback.(*statePlaybackDelivery).onClose = func() {
		progressAtClose = service.progress[itemID]
	}
	body := `{"ItemId":"` + itemID + `","PlaySessionId":"` + playID + `","MediaSourceId":"` + mediaID + `","PositionTicks":6000000000}`
	response := serveStateRequest(handler.handlePlayingStopped, token, body)
	if response.Code != http.StatusNoContent {
		t.Fatalf("stopped seek status=%d body=%s", response.Code, response.Body.String())
	}
	progress := service.progress[itemID]
	if progress.PositionSeconds != 600 || progress.DurationSeconds != 4000 || progress.Completed || progress.Version != 10 {
		t.Fatalf("stopped seek progress = %+v", progress)
	}
	if progressAtClose != progress {
		t.Fatalf("playback closed before final seek persisted: at-close=%+v final=%+v", progressAtClose, progress)
	}
	if _, exists := registry.entries[playID]; exists {
		t.Fatal("stopped seek remained open after exact position was persisted")
	}
}

func TestStoppedSerializesQueuedProgressThroughTerminalClose(t *testing.T) {
	handler, _, service, registry, token, itemID, playID, mediaID := stateHTTPFixture(t)
	registry.entries[playID].sources[mediaID].duration = 4000
	blocking := &blockingPlaybackWatchstate{
		memoryWatchstate: service,
		entered:          make(chan watchstate.UpdateProgressInput, 2),
		release:          make(chan struct{}),
	}
	handler.watchstate = blocking
	stoppedBody := `{"ItemId":"` + itemID + `","PlaySessionId":"` + playID + `","MediaSourceId":"` + mediaID + `","PositionTicks":6000000000}`
	progressBody := `{"ItemId":"` + itemID + `","PlaySessionId":"` + playID + `","MediaSourceId":"` + mediaID + `","PositionTicks":37000000000}`
	stoppedResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		stoppedResult <- serveStateRequest(handler.handlePlayingStopped, token, stoppedBody)
	}()
	if input := <-blocking.entered; input.PositionSeconds != 600 {
		t.Fatalf("blocked stopped position=%d, want 600", input.PositionSeconds)
	}
	progressStarted := make(chan struct{})
	progressResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		close(progressStarted)
		progressResult <- serveStateRequest(handler.handlePlayingProgress, token, progressBody)
	}()
	<-progressStarted
	close(blocking.release)
	stoppedResponse := <-stoppedResult
	progressResponse := <-progressResult
	if stoppedResponse.Code != http.StatusNoContent || progressResponse.Code != http.StatusNoContent {
		t.Fatalf("concurrent statuses stopped=%d progress=%d", stoppedResponse.Code, progressResponse.Code)
	}
	progress := service.progress[itemID]
	if progress.PositionSeconds != 600 || progress.DurationSeconds != 4000 || service.updateCalls != 1 {
		t.Fatalf("terminal progress=%+v updateCalls=%d", progress, service.updateCalls)
	}
}

func TestConcurrentStoppedEventsPersistOnlyFirst(t *testing.T) {
	handler, _, service, registry, token, itemID, playID, mediaID := stateHTTPFixture(t)
	registry.entries[playID].sources[mediaID].duration = 4000
	blocking := &blockingPlaybackWatchstate{
		memoryWatchstate: service,
		entered:          make(chan watchstate.UpdateProgressInput, 2),
		release:          make(chan struct{}),
	}
	handler.watchstate = blocking
	firstBody := `{"ItemId":"` + itemID + `","PlaySessionId":"` + playID + `","MediaSourceId":"` + mediaID + `","PositionTicks":6000000000}`
	secondBody := `{"ItemId":"` + itemID + `","PlaySessionId":"` + playID + `","MediaSourceId":"` + mediaID + `","PositionTicks":7000000000}`
	firstResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstResult <- serveStateRequest(handler.handlePlayingStopped, token, firstBody)
	}()
	if input := <-blocking.entered; input.PositionSeconds != 600 {
		t.Fatalf("blocked stopped position=%d, want 600", input.PositionSeconds)
	}
	secondStarted := make(chan struct{})
	secondResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		close(secondStarted)
		secondResult <- serveStateRequest(handler.handlePlayingStopped, token, secondBody)
	}()
	<-secondStarted
	close(blocking.release)
	firstResponse := <-firstResult
	secondResponse := <-secondResult
	if firstResponse.Code != http.StatusNoContent || secondResponse.Code != http.StatusNoContent {
		t.Fatalf("concurrent stopped statuses first=%d second=%d", firstResponse.Code, secondResponse.Code)
	}
	if progress := service.progress[itemID]; progress.PositionSeconds != 600 || service.updateCalls != 1 {
		t.Fatalf("concurrent stopped progress=%+v updateCalls=%d", progress, service.updateCalls)
	}
}

func TestStoppedPersistenceConflictLeavesRegistryRetryable(t *testing.T) {
	handler, _, service, registry, token, itemID, playID, mediaID := stateHTTPFixture(t)
	service.updateErrors = []error{watchstate.ErrConflict, watchstate.ErrConflict}
	body := `{"ItemId":"` + itemID + `","PlaySessionId":"` + playID + `","MediaSourceId":"` + mediaID + `","PositionTicks":500000000}`
	response := serveStateRequest(handler.handlePlayingStopped, token, body)
	if response.Code != http.StatusConflict || len(service.progress) != 0 {
		t.Fatalf("conflicted stopped status=%d progress=%+v body=%s", response.Code, service.progress, response.Body.String())
	}
	if _, exists := registry.entries[playID]; !exists {
		t.Fatal("conflicted stopped event removed its retryable playback registry entry")
	}

	response = serveStateRequest(handler.handlePlayingStopped, token, body)
	if response.Code != http.StatusNoContent || service.progress[itemID].PositionSeconds != 50 {
		t.Fatalf("retried stopped status=%d progress=%+v body=%s", response.Code, service.progress[itemID], response.Body.String())
	}
	if _, exists := registry.entries[playID]; exists {
		t.Fatal("successful stopped retry left playback registry entry open")
	}
}

func TestStoppedPersistenceFailureReleasesSessionForRetry(t *testing.T) {
	handler, _, service, registry, token, itemID, playID, mediaID := stateHTTPFixture(t)
	service.updateErrors = []error{errors.New("database unavailable")}
	body := `{"ItemId":"` + itemID + `","PlaySessionId":"` + playID + `","MediaSourceId":"` + mediaID + `","PositionTicks":500000000}`
	response := serveStateRequest(handler.handlePlayingStopped, token, body)
	if response.Code != http.StatusInternalServerError || len(service.progress) != 0 {
		t.Fatalf("failed stopped status=%d progress=%+v body=%s", response.Code, service.progress, response.Body.String())
	}
	if registry.entries[playID] == nil {
		t.Fatal("failed stopped event removed its retryable playback session")
	}
	response = serveStateRequest(handler.handlePlayingStopped, token, body)
	if response.Code != http.StatusNoContent || service.progress[itemID].PositionSeconds != 50 || service.updateCalls != 1 {
		t.Fatalf("retried stopped status=%d progress=%+v updateCalls=%d body=%s", response.Code, service.progress[itemID], service.updateCalls, response.Body.String())
	}
}

func TestPlaybackEventsWithoutMediaSourceUseOnlyUniqueOpenSource(t *testing.T) {
	tests := []struct {
		name    string
		event   func(*Handler, http.ResponseWriter, *http.Request)
		stopped bool
	}{
		{name: "playing", event: (*Handler).handlePlaying},
		{name: "progress", event: (*Handler).handlePlayingProgress},
		{name: "stopped", event: (*Handler).handlePlayingStopped, stopped: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, _, service, registry, token, itemID, playID, mediaID := stateHTTPFixture(t)
			secondID := addStateCandidate(t, registry, playID, false)
			entry := registry.entries[playID]
			entry.lastSeenAt = registry.now().UTC().Add(-time.Minute)
			body := `{"ItemId":"` + itemID + `","PlaySessionId":"` + playID + `","PositionTicks":250000000}`
			response := serveStateRequest(func(writer http.ResponseWriter, request *http.Request) {
				test.event(handler, writer, request)
			}, token, body)
			if response.Code != http.StatusNoContent {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			progress := service.progress[itemID]
			if progress.PositionSeconds != 25 || progress.DurationSeconds != 100 {
				t.Fatalf("progress=%+v; active=%s unopened=%s", progress, mediaID, secondID)
			}
			if test.stopped {
				if registry.entries[playID] != nil {
					t.Fatal("stopped event left play session open")
				}
			} else if !entry.lastSeenAt.Equal(registry.now().UTC()) {
				t.Fatalf("event did not touch registry: lastSeen=%v", entry.lastSeenAt)
			}
		})
	}
}

func TestPlaybackEventWithoutMediaSourceRejectsNoOrMultipleOpenSources(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *stateAuthentication, *playSessionRegistry, string)
	}{
		{name: "none open", mutate: func(_ *testing.T, _ *stateAuthentication, registry *playSessionRegistry, playID string) {
			for _, source := range registry.entries[playID].sources {
				source.handle = playback.DeliveryHandle{}
			}
		}},
		{name: "multiple open", mutate: func(t *testing.T, _ *stateAuthentication, registry *playSessionRegistry, playID string) {
			addStateCandidate(t, registry, playID, true)
		}},
		{name: "foreign owner", mutate: func(_ *testing.T, authentication *stateAuthentication, _ *playSessionRegistry, _ string) {
			authentication.session.ID = "66666666-6666-4666-8666-666666666666"
		}},
		{name: "expired session", mutate: func(_ *testing.T, _ *stateAuthentication, registry *playSessionRegistry, playID string) {
			registry.entries[playID].expiresAt = registry.now().UTC()
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, authentication, service, registry, token, itemID, playID, _ := stateHTTPFixture(t)
			test.mutate(t, authentication, registry, playID)
			entry := registry.entries[playID]
			entry.lastSeenAt = registry.now().UTC().Add(-time.Minute)
			lastSeen := entry.lastSeenAt
			body := `{"ItemId":"` + itemID + `","PlaySessionId":"` + playID + `","PositionTicks":250000000}`
			response := serveStateRequest(handler.handlePlayingProgress, token, body)
			if response.Code != http.StatusNoContent || len(service.progress) != 0 {
				t.Fatalf("status=%d progress=%+v body=%s", response.Code, service.progress, response.Body.String())
			}
			if !entry.lastSeenAt.Equal(lastSeen) {
				t.Fatalf("rejected event touched registry: lastSeen=%v want=%v", entry.lastSeenAt, lastSeen)
			}
		})
	}
}

func TestPlayingAndStoppedFirstResetCompletedReplayAtomically(t *testing.T) {
	events := []struct {
		name    string
		handle  func(*Handler, http.ResponseWriter, *http.Request)
		stopped bool
	}{
		{name: "playing", handle: (*Handler).handlePlaying},
		{name: "stopped", handle: (*Handler).handlePlayingStopped, stopped: true},
	}
	for _, event := range events {
		for _, unknownDuration := range []bool{false, true} {
			name := event.name + "/known duration"
			if unknownDuration {
				name = event.name + "/unknown duration"
			}
			t.Run(name, func(t *testing.T) {
				handler, _, service, registry, token, itemID, playID, mediaID := stateHTTPFixture(t)
				if unknownDuration {
					registry.entries[playID].sources[mediaID].duration = 0
				}
				service.progress[itemID] = watchstate.Progress{
					TitleID: itemID, MediaType: "movie", PositionSeconds: 100, DurationSeconds: 100, Completed: true, Version: 1,
				}
				body := `{"ItemId":"` + itemID + `","PlaySessionId":"` + playID + `","PositionTicks":250000000}`
				response := serveStateRequest(func(writer http.ResponseWriter, request *http.Request) {
					event.handle(handler, writer, request)
				}, token, body)
				if response.Code != http.StatusNoContent {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
				progress := service.progress[itemID]
				if progress.Completed || progress.PositionSeconds != 25 || progress.DurationSeconds != 100 || progress.Version != 2 || service.watchedCalls != 0 || service.updateCalls != 1 {
					t.Fatalf("atomic replay progress=%+v watchedCalls=%d updateCalls=%d", progress, service.watchedCalls, service.updateCalls)
				}
				if event.stopped && registry.entries[playID] != nil {
					t.Fatal("stopped-first replay left play session open")
				}
			})
		}
	}
}

func TestProgressFirstDoesNotTreatStaleCompletedEventAsReplay(t *testing.T) {
	handler, _, service, registry, token, itemID, playID, _ := stateHTTPFixture(t)
	completed := watchstate.Progress{
		TitleID: itemID, MediaType: "movie", PositionSeconds: 100,
		DurationSeconds: 100, Completed: true, Version: 4,
	}
	service.progress[itemID] = completed
	body := `{"ItemId":"` + itemID + `","PlaySessionId":"` + playID + `","PositionTicks":250000000}`
	response := serveStateRequest(handler.handlePlayingProgress, token, body)
	if response.Code != http.StatusNoContent {
		t.Fatalf("stale progress status=%d body=%s", response.Code, response.Body.String())
	}
	if progress := service.progress[itemID]; progress != completed || service.updateCalls != 0 || service.watchedCalls != 0 {
		t.Fatalf("stale progress reopened completed state: progress=%+v updateCalls=%d watchedCalls=%d", progress, service.updateCalls, service.watchedCalls)
	}
	if registry.entries[playID] == nil {
		t.Fatal("non-final stale progress closed the active play session")
	}
}

func TestProgressAndStoppedTerminalDuplicatesKeepCompletion(t *testing.T) {
	events := []struct {
		name   string
		handle func(*Handler, http.ResponseWriter, *http.Request)
	}{
		{name: "progress", handle: (*Handler).handlePlayingProgress},
		{name: "stopped", handle: (*Handler).handlePlayingStopped},
	}
	for _, event := range events {
		for _, unknownDuration := range []bool{false, true} {
			name := event.name + "/known duration"
			if unknownDuration {
				name = event.name + "/unknown duration"
			}
			t.Run(name, func(t *testing.T) {
				handler, _, service, registry, token, itemID, playID, mediaID := stateHTTPFixture(t)
				if unknownDuration {
					registry.entries[playID].sources[mediaID].duration = 0
				}
				service.progress[itemID] = watchstate.Progress{
					TitleID: itemID, MediaType: "movie", PositionSeconds: 100, DurationSeconds: 100, Completed: true, Version: 1,
				}
				body := `{"ItemId":"` + itemID + `","PlaySessionId":"` + playID + `","PositionTicks":1000000000}`
				response := serveStateRequest(func(writer http.ResponseWriter, request *http.Request) {
					event.handle(handler, writer, request)
				}, token, body)
				progress := service.progress[itemID]
				if response.Code != http.StatusNoContent || !progress.Completed || progress.PositionSeconds != 100 || progress.DurationSeconds != 100 || progress.Version != 1 || service.watchedCalls != 0 || service.updateCalls != 0 {
					t.Fatalf("status=%d progress=%+v watchedCalls=%d updateCalls=%d body=%s", response.Code, progress, service.watchedCalls, service.updateCalls, response.Body.String())
				}
			})
		}
	}
}

func TestPlaybackEventRejectsNegativeTicksBeforeRegistryTouch(t *testing.T) {
	for _, field := range []string{"PositionTicks", "PlaybackStartTimeTicks"} {
		t.Run(field, func(t *testing.T) {
			handler, _, service, registry, token, itemID, playID, mediaID := stateHTTPFixture(t)
			entry := registry.entries[playID]
			lastSeen := entry.lastSeenAt.Add(-time.Minute)
			entry.lastSeenAt = lastSeen
			body := `{"ItemId":"` + itemID + `","PlaySessionId":"` + playID + `","MediaSourceId":"` + mediaID + `","` + field + `":-1}`
			response := serveStateRequest(handler.handlePlayingProgress, token, body)
			if response.Code != http.StatusBadRequest || len(service.progress) != 0 {
				t.Fatalf("negative %s status=%d progress=%+v body=%s", field, response.Code, service.progress, response.Body.String())
			}
			if entry.lastSeenAt != lastSeen {
				t.Fatalf("negative %s touched registry: lastSeen=%v want=%v", field, entry.lastSeenAt, lastSeen)
			}
		})
	}
}

func TestPlaybackEventRejectsMismatchedSelectorsAndUnknownPing(t *testing.T) {
	handler, _, service, registry, token, itemID, playID, mediaID := stateHTTPFixture(t)
	mismatch := `{"ItemId":"00000000-0000-4000-8000-000000000099","PlaySessionId":"` + playID + `","MediaSourceId":"` + mediaID + `","PositionTicks":100000000}`
	response := serveStateRequest(handler.handlePlayingProgress, token, mismatch)
	if response.Code != http.StatusNoContent || len(service.progress) != 0 || len(registry.entries) != 1 {
		t.Fatalf("mismatched item status=%d progress=%+v entries=%d", response.Code, service.progress, len(registry.entries))
	}
	mismatch = `{"UserId":"22222222-2222-4222-8222-222222222222","ItemId":"` + itemID + `","PlaySessionId":"` + playID + `","MediaSourceId":"` + mediaID + `","PositionTicks":100000000}`
	response = serveStateRequest(handler.handlePlayingProgress, token, mismatch)
	if response.Code != http.StatusNotFound || len(service.progress) != 0 || len(registry.entries) != 1 {
		t.Fatalf("mismatched profile status=%d progress=%+v entries=%d", response.Code, service.progress, len(registry.entries))
	}
	unknownPing := `{"PlaySessionId":"unknown-play-session"}`
	response = serveStateRequest(handler.handlePlayingPing, token, unknownPing)
	if response.Code != http.StatusNoContent {
		t.Fatalf("unknown ping status=%d body=%s", response.Code, response.Body.String())
	}
	validPing := `{"PlaySessionId":"` + playID + `"}`
	response = serveStateRequest(handler.handlePlayingPing, token, validPing)
	if response.Code != http.StatusNoContent {
		t.Fatalf("valid ping status=%d body=%s item=%s", response.Code, response.Body.String(), itemID)
	}
}

func TestPlayedEndpointsBindUserRemainIdempotentAndPreserveChildFavorite(t *testing.T) {
	handler, authentication, service, _, token, itemID, _, _ := stateHTTPFixture(t)
	handler.catalog.(*stateCatalog).items[itemID] = watchstate.CatalogTitle{
		ID: itemID, MediaType: "episode", ParentID: "00000000-0000-4000-8000-000000000010", Title: "Child", InLibrary: false,
	}
	requestPlayed := func(method, userID string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/Users/"+userID+"/PlayedItems/"+itemID, nil)
		request.SetPathValue("userId", userID)
		request.SetPathValue("itemId", itemID)
		request.Header.Set("X-Emby-Token", token)
		response := httptest.NewRecorder()
		handler.handlePlayedItem(response, request)
		return response
	}
	response := requestPlayed(http.MethodPost, authentication.session.ProfileID)
	if response.Code != http.StatusOK || !service.progress[itemID].Completed || service.progress[itemID].Version != 1 {
		t.Fatalf("played status=%d progress=%+v", response.Code, service.progress[itemID])
	}
	if !strings.Contains(response.Body.String(), `"IsFavorite":false`) {
		t.Fatalf("played child response invented library membership: %s", response.Body.String())
	}
	response = requestPlayed(http.MethodPost, authentication.session.ProfileID)
	if response.Code != http.StatusOK || service.progress[itemID].Version != 1 {
		t.Fatalf("duplicate played status=%d progress=%+v", response.Code, service.progress[itemID])
	}
	response = requestPlayed(http.MethodDelete, "22222222-2222-4222-8222-222222222222")
	if response.Code != http.StatusNotFound || service.progress[itemID].Version != 1 {
		t.Fatalf("foreign user status=%d progress=%+v", response.Code, service.progress[itemID])
	}
	response = requestPlayed(http.MethodDelete, authentication.session.ProfileID)
	if response.Code != http.StatusOK || service.progress[itemID].Completed || service.progress[itemID].Version != 2 {
		t.Fatalf("unplayed status=%d progress=%+v", response.Code, service.progress[itemID])
	}
}

func TestUserDataModernAndLegacyApplyAllStateIdempotently(t *testing.T) {
	handler, authentication, service, _, token, itemID, _, _ := stateHTTPFixture(t)
	runtimeMinutes := 2
	catalog := handler.catalog.(*stateCatalog)
	item := catalog.items[itemID]
	item.RuntimeMinutes = &runtimeMinutes
	catalog.items[itemID] = item

	requestUserData := func(method, path, userID, body string, legacy bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetPathValue("itemId", itemID)
		if legacy {
			request.SetPathValue("userId", userID)
		}
		request.Header.Set("X-Emby-Token", token)
		if method == http.MethodPost {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		if legacy {
			handler.handleLegacyUserData(response, request)
		} else {
			handler.handleUserData(response, request)
		}
		return response
	}

	response := requestUserData(http.MethodGet, "/UserItems/"+itemID+"/UserData", "", "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"Key":"`+itemID+`"`) || !strings.Contains(response.Body.String(), `"ItemId":"`+itemID+`"`) {
		t.Fatalf("empty UserData status=%d body=%s", response.Code, response.Body.String())
	}
	body := `{"PlaybackPositionTicks":300000000,"PlayCount":0,"IsFavorite":true,"Played":false,"Key":"` + itemID + `","ItemId":"` + itemID + `"}`
	response = requestUserData(http.MethodPost, "/UserItems/"+itemID+"/UserData", "", body, false)
	progress := service.progress[itemID]
	if response.Code != http.StatusOK || progress.PositionSeconds != 30 || progress.DurationSeconds != 120 || progress.Completed || progress.Version != 1 || !service.library[itemID] || service.addCalls != 1 {
		t.Fatalf("modern update status=%d progress=%+v library=%v addCalls=%d body=%s", response.Code, progress, service.library, service.addCalls, response.Body.String())
	}
	response = requestUserData(http.MethodPost, "/UserItems/"+itemID+"/UserData", "", body, false)
	if response.Code != http.StatusOK || service.progress[itemID].Version != 1 || service.addCalls != 1 {
		t.Fatalf("duplicate update status=%d progress=%+v addCalls=%d", response.Code, service.progress[itemID], service.addCalls)
	}

	body = `{"PlaybackPositionTicks":600000000,"Played":true}`
	response = requestUserData(http.MethodPost, "/Users/"+authentication.session.ProfileID+"/Items/"+itemID+"/UserData", authentication.session.ProfileID, body, true)
	progress = service.progress[itemID]
	if response.Code != http.StatusOK || progress.PositionSeconds != 60 || !progress.Completed || progress.Version != 2 || !service.library[itemID] || service.removeCalls != 0 || !strings.Contains(response.Body.String(), `"PlayedPercentage":50`) {
		t.Fatalf("legacy partial update status=%d progress=%+v library=%v body=%s", response.Code, progress, service.library, response.Body.String())
	}
	body = `{"IsFavorite":false}`
	response = requestUserData(http.MethodPost, "/UserItems/"+itemID+"/UserData", "", body, false)
	if response.Code != http.StatusOK || service.library[itemID] || service.removeCalls != 1 || service.progress[itemID].Version != 2 || !strings.Contains(response.Body.String(), `"PlaybackPositionTicks":600000000`) {
		t.Fatalf("favorite-only update status=%d progress=%+v library=%v removes=%d body=%s", response.Code, service.progress[itemID], service.library, service.removeCalls, response.Body.String())
	}
	response = requestUserData(http.MethodPost, "/UserItems/"+itemID+"/UserData", "", body, false)
	if response.Code != http.StatusOK || service.removeCalls != 1 || service.progress[itemID].Version != 2 {
		t.Fatalf("duplicate favorite-only update status=%d progress=%+v removes=%d", response.Code, service.progress[itemID], service.removeCalls)
	}
	response = requestUserData(http.MethodGet, "/Users/foreign/Items/"+itemID+"/UserData", "22222222-2222-4222-8222-222222222222", "", true)
	if response.Code != http.StatusNotFound || service.progress[itemID].Version != 2 || service.addCalls != 1 {
		t.Fatalf("foreign legacy status=%d progress=%+v addCalls=%d", response.Code, service.progress[itemID], service.addCalls)
	}
}

func TestFavoriteEndpointsMutateLibraryAndPreserveProgress(t *testing.T) {
	handler, authentication, service, _, token, itemID, _, _ := stateHTTPFixture(t)
	service.progress[itemID] = watchstate.Progress{TitleID: itemID, MediaType: "movie", PositionSeconds: 45, DurationSeconds: 90, Version: 7, LastWatchedAt: time.Unix(7, 0).UTC()}
	catalog := handler.catalog.(*stateCatalog)

	requestFavorite := func(method, userID string, legacy bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/favorite/"+itemID, nil)
		request.SetPathValue("itemId", itemID)
		if legacy {
			request.SetPathValue("userId", userID)
		}
		request.Header.Set("X-Emby-Token", token)
		response := httptest.NewRecorder()
		if legacy {
			handler.handleLegacyFavoriteItem(response, request)
		} else {
			handler.handleFavoriteItem(response, request)
		}
		return response
	}

	response := requestFavorite(http.MethodPost, "", false)
	if response.Code != http.StatusOK || !service.library[itemID] || service.addCalls != 1 || service.userDataCalls != 1 || service.progress[itemID].Version != 7 || !strings.Contains(response.Body.String(), `"PlaybackPositionTicks":450000000`) || !strings.Contains(response.Body.String(), `"IsFavorite":true`) {
		t.Fatalf("favorite status=%d progress=%+v library=%v addCalls=%d linkedCalls=%d body=%s", response.Code, service.progress[itemID], service.library, service.addCalls, service.userDataCalls, response.Body.String())
	}
	item := catalog.items[itemID]
	item.InLibrary = true
	catalog.items[itemID] = item
	response = requestFavorite(http.MethodPost, authentication.session.ProfileID, true)
	if response.Code != http.StatusOK || service.addCalls != 1 || service.userDataCalls != 2 || service.progress[itemID].Version != 7 {
		t.Fatalf("duplicate favorite status=%d addCalls=%d linkedCalls=%d progress=%+v", response.Code, service.addCalls, service.userDataCalls, service.progress[itemID])
	}
	response = requestFavorite(http.MethodDelete, authentication.session.ProfileID, true)
	if response.Code != http.StatusOK || service.library[itemID] || service.removeCalls != 1 || service.userDataCalls != 3 || service.progress[itemID].Version != 7 || !strings.Contains(response.Body.String(), `"PlaybackPositionTicks":450000000`) || !strings.Contains(response.Body.String(), `"IsFavorite":false`) {
		t.Fatalf("unfavorite status=%d progress=%+v library=%v removeCalls=%d linkedCalls=%d body=%s", response.Code, service.progress[itemID], service.library, service.removeCalls, service.userDataCalls, response.Body.String())
	}
	item.InLibrary = false
	catalog.items[itemID] = item
	response = requestFavorite(http.MethodDelete, "", false)
	if response.Code != http.StatusOK || service.removeCalls != 1 || service.userDataCalls != 4 || service.progress[itemID].Version != 7 {
		t.Fatalf("duplicate unfavorite status=%d removeCalls=%d linkedCalls=%d progress=%+v", response.Code, service.removeCalls, service.userDataCalls, service.progress[itemID])
	}
	response = requestFavorite(http.MethodPost, "22222222-2222-4222-8222-222222222222", true)
	if response.Code != http.StatusNotFound || service.addCalls != 1 || service.userDataCalls != 4 || service.progress[itemID].Version != 7 {
		t.Fatalf("foreign favorite status=%d addCalls=%d linkedCalls=%d progress=%+v", response.Code, service.addCalls, service.userDataCalls, service.progress[itemID])
	}
}

func TestUserDataUpdateBoundsBodyAndFailsClosedForUnknownItem(t *testing.T) {
	handler, _, service, _, token, itemID, _, _ := stateHTTPFixture(t)
	nullRequest := httptest.NewRequest(http.MethodPost, "/UserItems/"+itemID+"/UserData", strings.NewReader("null"))
	nullRequest.SetPathValue("itemId", itemID)
	nullRequest.Header.Set("X-Emby-Token", token)
	nullRequest.Header.Set("Content-Type", "application/json")
	nullResponse := httptest.NewRecorder()
	handler.handleUserData(nullResponse, nullRequest)
	if nullResponse.Code != http.StatusBadRequest || service.userDataCalls != 0 || len(service.progress) != 0 || len(service.library) != 0 {
		t.Fatalf("null body status=%d linkedCalls=%d progress=%+v library=%+v", nullResponse.Code, service.userDataCalls, service.progress, service.library)
	}
	request := httptest.NewRequest(http.MethodPost, "/UserItems/"+itemID+"/UserData", strings.NewReader(strings.Repeat(" ", int(maximumCompatJSONBodyBytes)+1)))
	request.SetPathValue("itemId", itemID)
	request.Header.Set("X-Emby-Token", token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.handleUserData(response, request)
	if response.Code != http.StatusBadRequest || len(service.progress) != 0 || len(service.library) != 0 {
		t.Fatalf("oversized body status=%d progress=%+v library=%+v", response.Code, service.progress, service.library)
	}

	unknownID := "00000000-0000-4000-8000-000000000099"
	request = httptest.NewRequest(http.MethodGet, "/UserItems/"+unknownID+"/UserData", nil)
	request.SetPathValue("itemId", unknownID)
	request.Header.Set("X-Emby-Token", token)
	response = httptest.NewRecorder()
	handler.handleUserData(response, request)
	if response.Code != http.StatusNotFound || len(service.progress) != 0 || len(service.library) != 0 {
		t.Fatalf("unknown item status=%d progress=%+v library=%+v", response.Code, service.progress, service.library)
	}
}

func TestUserDataCombinedMutationFailureDoesNotCommitPartially(t *testing.T) {
	handler, _, service, _, token, itemID, _, _ := stateHTTPFixture(t)
	runtimeMinutes := 2
	item := handler.catalog.(*stateCatalog).items[itemID]
	item.RuntimeMinutes = &runtimeMinutes
	handler.catalog.(*stateCatalog).items[itemID] = item
	service.userDataErr = watchstate.ErrOutboxCapacity
	body := `{"PlaybackPositionTicks":300000000,"Played":true,"IsFavorite":true}`
	request := httptest.NewRequest(http.MethodPost, "/UserItems/"+itemID+"/UserData", strings.NewReader(body))
	request.SetPathValue("itemId", itemID)
	request.Header.Set("X-Emby-Token", token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.handleUserData(response, request)
	if response.Code != http.StatusServiceUnavailable || len(service.progress) != 0 || len(service.library) != 0 || service.addCalls != 0 || service.updateCalls != 0 {
		t.Fatalf("atomic failure status=%d progress=%+v library=%+v add=%d update=%d", response.Code, service.progress, service.library, service.addCalls, service.updateCalls)
	}
}

func TestResumePaginationAndNextUpProjectionAreDeterministic(t *testing.T) {
	handler, authentication, service, _, token, firstID, _, _ := stateHTTPFixture(t)
	secondID := "00000000-0000-4000-8000-000000000002"
	seriesID := "00000000-0000-4000-8000-000000000100"
	seasonID := "00000000-0000-4000-8000-000000000110"
	episodeID := "00000000-0000-4000-8000-000000000111"
	catalog := handler.catalog.(*stateCatalog)
	catalog.items[secondID] = watchstate.CatalogTitle{ID: secondID, MediaType: "movie", Title: "Second"}
	catalog.items[episodeID] = watchstate.CatalogTitle{ID: episodeID, MediaType: "episode", Title: "Next", SeriesID: seriesID, SeasonID: seasonID}
	service.resumePage = watchstate.ContinueItemsPage{
		Items: []watchstate.ContinueItem{{TitleID: secondID}, {TitleID: firstID}}, Offset: 1, Limit: 2, Total: 3,
	}
	request := httptest.NewRequest(http.MethodGet, "/Users/"+authentication.session.ProfileID+"/Items/Resume?StartIndex=1&Limit=2", nil)
	request.SetPathValue("id", authentication.session.ProfileID)
	request.Header.Set("X-Emby-Token", token)
	response := httptest.NewRecorder()
	handler.handleResumeItems(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || service.resumeOffset != 1 || service.resumeLimit != 2 || catalog.batchCalls != 1 ||
		!strings.Contains(body, `"TotalRecordCount":3,"StartIndex":1`) || strings.Index(body, secondID) > strings.Index(body, firstID) {
		t.Fatalf("resume status=%d offset=%d limit=%d batchCalls=%d body=%s", response.Code, service.resumeOffset, service.resumeLimit, catalog.batchCalls, body)
	}

	service.resumeOffset = 99
	nonVideo := httptest.NewRequest(http.MethodGet, "/UserItems/Resume?MediaTypes=Audio&Limit=12", nil)
	nonVideo.Header.Set("X-Emby-Token", token)
	nonVideoResponse := httptest.NewRecorder()
	handler.handleUserResumeItems(nonVideoResponse, nonVideo)
	if nonVideoResponse.Code != http.StatusOK || service.resumeOffset != 99 || !strings.Contains(nonVideoResponse.Body.String(), `"Items":[]`) {
		t.Fatalf("non-video resume status=%d offset=%d body=%s", nonVideoResponse.Code, service.resumeOffset, nonVideoResponse.Body.String())
	}

	service.nextPage = watchstate.ContinueItemsPage{
		Items: []watchstate.ContinueItem{{TitleID: episodeID, SeriesID: seriesID, SeasonID: seasonID}}, Offset: 0, Limit: 1, Total: 1,
	}
	request = httptest.NewRequest(http.MethodGet, "/Shows/NextUp?SeriesId="+seriesID+"&StartIndex=0&Limit=1", nil)
	request.Header.Set("X-Emby-Token", token)
	response = httptest.NewRecorder()
	handler.handleNextUp(response, request)
	if response.Code != http.StatusOK || service.nextSeriesID != seriesID || service.nextOffset != 0 || service.nextLimit != 1 ||
		!strings.Contains(response.Body.String(), episodeID) {
		t.Fatalf("next-up status=%d series=%s offset=%d limit=%d body=%s", response.Code, service.nextSeriesID, service.nextOffset, service.nextLimit, response.Body.String())
	}
	if catalog.batchCalls != 2 {
		t.Fatalf("resume and next-up projections used %d catalog batches, want 2", catalog.batchCalls)
	}
}

func TestResumeHundredItemsUsesOneCatalogBatchAndRestoresFeedOrder(t *testing.T) {
	handler, authentication, service, _, token, _, _, _ := stateHTTPFixture(t)
	catalog := handler.catalog.(*stateCatalog)
	entries := make([]watchstate.ContinueItem, 0, MaximumQueryLimit)
	for index := 1; index <= MaximumQueryLimit; index++ {
		id := fmt.Sprintf("20000000-0000-4000-8000-%012d", index)
		catalog.items[id] = watchstate.CatalogTitle{ID: id, MediaType: "movie", Title: fmt.Sprintf("Item %03d", index)}
		entries = append(entries, watchstate.ContinueItem{TitleID: id})
	}
	service.resumePage = watchstate.ContinueItemsPage{Items: entries, Limit: MaximumQueryLimit, Total: MaximumQueryLimit}
	request := httptest.NewRequest(http.MethodGet, "/Users/"+authentication.session.ProfileID+"/Items/Resume?Limit=100", nil)
	request.SetPathValue("id", authentication.session.ProfileID)
	request.Header.Set("X-Emby-Token", token)
	response := httptest.NewRecorder()
	handler.handleResumeItems(response, request)
	body := response.Body.String()
	firstID, lastID := entries[0].TitleID, entries[len(entries)-1].TitleID
	if response.Code != http.StatusOK || catalog.batchCalls != 1 || strings.Index(body, firstID) < 0 || strings.Index(body, lastID) < strings.Index(body, firstID) {
		t.Fatalf("status=%d batchCalls=%d first=%d last=%d", response.Code, catalog.batchCalls, strings.Index(body, firstID), strings.Index(body, lastID))
	}
}

func TestResumeBatchProjectionClassifiesOperationalErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "not found", err: watchstate.ErrNotFound, want: http.StatusNotFound},
		{name: "authorization", err: watchstate.ErrProfileRequired, want: http.StatusUnauthorized},
		{name: "canceled", err: context.Canceled, want: http.StatusInternalServerError},
		{name: "database", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, authentication, service, _, token, itemID, _, _ := stateHTTPFixture(t)
			service.resumePage = watchstate.ContinueItemsPage{Items: []watchstate.ContinueItem{{TitleID: itemID}}, Limit: 1, Total: 1}
			catalog := handler.catalog.(*stateCatalog)
			catalog.batchErr = test.err
			request := httptest.NewRequest(http.MethodGet, "/Users/"+authentication.session.ProfileID+"/Items/Resume?Limit=1", nil)
			request.SetPathValue("id", authentication.session.ProfileID)
			request.Header.Set("X-Emby-Token", token)
			response := httptest.NewRecorder()
			handler.handleResumeItems(response, request)
			if response.Code != test.want || catalog.batchCalls != 1 {
				t.Fatalf("status=%d want=%d batchCalls=%d body=%s", response.Code, test.want, catalog.batchCalls, response.Body.String())
			}
		})
	}
}

func stateHTTPFixture(t *testing.T) (*Handler, *stateAuthentication, *memoryWatchstate, *playSessionRegistry, string, string, string, string) {
	t.Helper()
	profileID := "11111111-1111-4111-8111-111111111111"
	itemID := "00000000-0000-4000-8000-000000000001"
	now := time.Now().UTC()
	session := AuthenticatedSession{
		ID: "33333333-3333-4333-8333-333333333333", ProfileID: profileID, ProfileName: "Profile",
		Client:    ClientIdentity{Client: "Generic Client", Device: "Living Room", DeviceID: "device-one", Version: "8.1"},
		ExpiresAt: now.Add(time.Hour),
		Principal: auth.Principal{SessionID: "44444444-4444-4444-8444-444444444444", UserID: "55555555-5555-4555-8555-555555555555", ActiveProfileID: &profileID},
	}
	authentication := &stateAuthentication{session: session}
	state := newMemoryWatchstate()
	delivery := &statePlaybackDelivery{}
	registry := newPlaySessionRegistry(delivery)
	registry.now = func() time.Time { return now }
	playID, sources, err := registry.register(context.Background(), session, itemID, playback.Capabilities{}, false, []playback.SourceOption{{
		SourceRef: "opaque-source", Name: "Source", Protocol: "https", Container: "mp4", ExpiresAt: now.Add(time.Hour),
	}})
	if err != nil || len(sources) != 1 {
		t.Fatalf("register play session: sources=%+v err=%v", sources, err)
	}
	mediaID := sources[0].ID
	registry.entries[playID].sources[mediaID].duration = 100
	registry.entries[playID].sources[mediaID].handle = opaquePlaybackHandleNamed(t, "state")
	handler := &Handler{
		authentication: authentication,
		catalog:        &stateCatalog{items: map[string]watchstate.CatalogTitle{itemID: {ID: itemID, MediaType: "movie", Title: "Movie"}}},
		watchstate:     state,
		playSessions:   registry,
	}
	token, _, err := newCompatCredential()
	if err != nil {
		t.Fatalf("create compat token: %v", err)
	}
	return handler, authentication, state, registry, token, itemID, playID, mediaID
}

func addStateCandidate(t *testing.T, registry *playSessionRegistry, playID string, open bool) string {
	t.Helper()
	entry := registry.entries[playID]
	if entry == nil {
		t.Fatal("play session is missing")
	}
	mediaID := derivedMediaSourceID(playID, len(entry.sourceOrder))
	source := &playSessionSource{
		descriptor: playSourceDescriptor{ID: mediaID, Name: "Second", Protocol: "https", Container: "mp4"},
		sourceRef:  "opaque-source-second",
		expiresAt:  registry.now().UTC().Add(time.Hour),
		duration:   200,
	}
	if open {
		source.handle = opaquePlaybackHandleNamed(t, "state-second")
	}
	entry.sourceOrder = append(entry.sourceOrder, mediaID)
	entry.sources[mediaID] = source
	return mediaID
}

func serveStateRequest(implementation func(http.ResponseWriter, *http.Request), token, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/state", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Emby-Token", token)
	response := httptest.NewRecorder()
	implementation(response, request)
	return response
}
