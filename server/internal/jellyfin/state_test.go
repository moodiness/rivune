package jellyfin

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

type memoryWatchstate struct {
	progress        map[string]watchstate.Progress
	updateCalls     int
	watchedCalls    int
	updateConflict  func(*memoryWatchstate, string)
	updateErrors    []error
	watchedConflict func(*memoryWatchstate, string, bool)
	resumePage      watchstate.ContinueItemsPage
	resumeOffset    int
	resumeLimit     int
	nextPage        watchstate.ContinueItemsPage
	nextSeriesID    string
	nextOffset      int
	nextLimit       int
}

func newMemoryWatchstate() *memoryWatchstate {
	return &memoryWatchstate{progress: make(map[string]watchstate.Progress)}
}

func (service *memoryWatchstate) GetProgress(_ context.Context, _ auth.Principal, itemID string) (watchstate.Progress, error) {
	value, ok := service.progress[itemID]
	if !ok {
		return watchstate.Progress{}, watchstate.ErrProgressNotFound
	}
	return value, nil
}

func (service *memoryWatchstate) UpdateProgress(_ context.Context, _ auth.Principal, itemID string, input watchstate.UpdateProgressInput) (watchstate.Progress, error) {
	service.updateCalls++
	if service.updateConflict != nil {
		hook := service.updateConflict
		service.updateConflict = nil
		hook(service, itemID)
		return watchstate.Progress{}, watchstate.ErrConflict
	}
	current, exists := service.progress[itemID]
	if exists && input.ExpectedVersion != current.Version || !exists && input.ExpectedVersion != 0 {
		return watchstate.Progress{}, watchstate.ErrConflict
	}
	version := int64(1)
	if exists {
		version = current.Version + 1
	}
	value := watchstate.Progress{
		TitleID: itemID, MediaType: "movie", PositionSeconds: input.PositionSeconds,
		DurationSeconds: input.DurationSeconds, Completed: input.Completed, Version: version,
		LastWatchedAt: time.Unix(version, 0).UTC(), UpdatedAt: time.Unix(version, 0).UTC(),
	}
	service.progress[itemID] = value
	return value, nil
}

func (service *memoryWatchstate) ApplyPlaybackEventForLinkedSession(ctx context.Context, principal auth.Principal, itemID string, input watchstate.UpdateProgressInput) (watchstate.Progress, error) {
	if len(service.updateErrors) > 0 {
		err := service.updateErrors[0]
		service.updateErrors = service.updateErrors[1:]
		return watchstate.Progress{}, err
	}
	return service.UpdateProgress(ctx, principal, itemID, input)
}

func (service *memoryWatchstate) SetWatched(_ context.Context, _ auth.Principal, itemID string, completed bool, input watchstate.CompletionInput) (watchstate.Progress, error) {
	service.watchedCalls++
	if service.watchedConflict != nil {
		hook := service.watchedConflict
		service.watchedConflict = nil
		hook(service, itemID, completed)
		return watchstate.Progress{}, watchstate.ErrConflict
	}
	current, exists := service.progress[itemID]
	if exists && input.ExpectedVersion != current.Version || !exists && input.ExpectedVersion != 0 {
		return watchstate.Progress{}, watchstate.ErrConflict
	}
	if !exists {
		current = watchstate.Progress{TitleID: itemID, MediaType: "movie"}
	}
	current.Version++
	current.Completed = completed
	if completed {
		current.PositionSeconds = current.DurationSeconds
	} else {
		current.PositionSeconds = 0
	}
	current.LastWatchedAt = time.Unix(current.Version, 0).UTC()
	current.UpdatedAt = current.LastWatchedAt
	service.progress[itemID] = current
	return current, nil
}

func (service *memoryWatchstate) SetWatchedForLinkedSession(ctx context.Context, principal auth.Principal, itemID string, completed bool, input watchstate.CompletionInput) (watchstate.Progress, error) {
	return service.SetWatched(ctx, principal, itemID, completed, input)
}

func (*memoryWatchstate) ClearProgress(context.Context, auth.Principal, string, int64) error {
	return nil
}
func (service *memoryWatchstate) ListResume(_ context.Context, _ auth.Principal, offset, limit int) (watchstate.ContinueItemsPage, error) {
	service.resumeOffset, service.resumeLimit = offset, limit
	return service.resumePage, nil
}
func (service *memoryWatchstate) ListNextUp(_ context.Context, _ auth.Principal, seriesID string, offset, limit int) (watchstate.ContinueItemsPage, error) {
	service.nextSeriesID, service.nextOffset, service.nextLimit = seriesID, offset, limit
	return service.nextPage, nil
}

func TestPlaybackProgressSequenceIsMonotonicIdempotentAndResetsReplay(t *testing.T) {
	service := newMemoryWatchstate()
	principal := auth.Principal{}
	ctx := context.Background()
	const itemID = "item"

	assertProgress := func(position, duration int, replay bool, wantVersion int64, wantCompleted bool) {
		t.Helper()
		value, err := applyPlaybackProgress(ctx, service, principal, itemID, position, duration, replay, false)
		if err != nil {
			t.Fatalf("apply progress %d/%d replay=%t: %v", position, duration, replay, err)
		}
		if value.PositionSeconds != position && !(wantCompleted && value.PositionSeconds == duration) ||
			value.DurationSeconds != duration || value.Version != wantVersion || value.Completed != wantCompleted {
			t.Fatalf("progress = %+v, want position=%d duration=%d version=%d completed=%t", value, position, duration, wantVersion, wantCompleted)
		}
	}

	assertProgress(0, 100, true, 1, false)
	assertProgress(25, 100, false, 2, false)
	assertProgress(25, 100, false, 2, false) // duplicate
	value, err := applyPlaybackProgress(ctx, service, principal, itemID, 10, 100, false, false)
	if err != nil || value.PositionSeconds != 25 || value.Version != 2 {
		t.Fatalf("out-of-order progress regressed: value=%+v err=%v", value, err)
	}
	assertProgress(25, 120, false, 3, false) // authoritative duration update
	value, err = applyPlaybackProgress(ctx, service, principal, itemID, 20, 20, false, false)
	if err != nil || value.PositionSeconds != 25 || value.DurationSeconds != 120 || value.Version != 3 {
		t.Fatalf("stale duration regressed progress: value=%+v err=%v", value, err)
	}
	assertProgress(120, 120, false, 4, true)
	assertProgress(0, 120, true, 5, false) // replay begins and clears completion
	assertProgress(10, 120, false, 6, false)
	if service.updateCalls != 6 || service.watchedCalls != 0 {
		t.Fatalf("mutation counts update=%d watched=%d", service.updateCalls, service.watchedCalls)
	}
}

func TestCompletedZeroDurationPlayingEventStartsReplay(t *testing.T) {
	service := newMemoryWatchstate()
	service.progress["item"] = watchstate.Progress{
		TitleID: "item", MediaType: "movie", Completed: true, Version: 1,
	}
	value, err := applyPlaybackProgress(context.Background(), service, auth.Principal{}, "item", 0, 100, true, false)
	if err != nil {
		t.Fatalf("start zero-duration replay: %v", err)
	}
	if value.Completed || value.PositionSeconds != 0 || value.DurationSeconds != 100 || value.Version != 2 {
		t.Fatalf("zero-duration replay progress = %+v", value)
	}
	if service.watchedCalls != 0 || service.updateCalls != 1 {
		t.Fatalf("zero-duration replay mutations watched=%d progress=%d", service.watchedCalls, service.updateCalls)
	}
}

func TestUnknownDurationReplayClearsCompletionInOneMutation(t *testing.T) {
	service := newMemoryWatchstate()
	service.progress["item"] = watchstate.Progress{
		TitleID: "item", MediaType: "movie", Completed: true, Version: 7,
	}
	value, err := applyPlaybackProgress(context.Background(), service, auth.Principal{}, "item", 0, 0, true, false)
	if err != nil {
		t.Fatalf("start unknown-duration replay: %v", err)
	}
	if value.Completed || value.PositionSeconds != 0 || value.DurationSeconds != 0 || value.Version != 8 {
		t.Fatalf("unknown-duration replay progress = %+v", value)
	}
	if service.watchedCalls != 0 || service.updateCalls != 1 {
		t.Fatalf("unknown-duration replay mutations watched=%d progress=%d", service.watchedCalls, service.updateCalls)
	}
}

func TestReplayMutationErrorsNeverPartiallyClearCompletion(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "revoked", err: watchstate.ErrForbidden},
		{name: "canceled", err: context.Canceled},
		{name: "outbox", err: watchstate.ErrOutboxCapacity},
		{name: "database", err: errors.New("write failed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newMemoryWatchstate()
			before := watchstate.Progress{
				TitleID: "item", MediaType: "movie", PositionSeconds: 100,
				DurationSeconds: 100, Completed: true, Version: 4,
			}
			service.progress["item"] = before
			service.updateErrors = []error{test.err}

			if _, err := applyPlaybackProgress(context.Background(), service, auth.Principal{}, "item", 10, 100, true, false); !errors.Is(err, test.err) {
				t.Fatalf("replay error = %v, want %v", err, test.err)
			}
			if after := service.progress["item"]; after != before {
				t.Fatalf("failed replay partially changed state: before=%+v after=%+v", before, after)
			}
			if service.watchedCalls != 0 || service.updateCalls != 0 {
				t.Fatalf("failed replay split mutations watched=%d progress=%d", service.watchedCalls, service.updateCalls)
			}
		})
	}
}

func TestReplayConflictReloadReconstructsAtomicMutation(t *testing.T) {
	service := newMemoryWatchstate()
	service.progress["item"] = watchstate.Progress{
		TitleID: "item", MediaType: "movie", PositionSeconds: 100,
		DurationSeconds: 100, Completed: true, Version: 1,
	}
	service.updateConflict = func(service *memoryWatchstate, itemID string) {
		value := service.progress[itemID]
		value.Version++
		service.progress[itemID] = value
	}

	value, err := applyPlaybackProgress(context.Background(), service, auth.Principal{}, "item", 10, 100, true, false)
	if err != nil {
		t.Fatalf("retry replay after conflict: %v", err)
	}
	if value.PositionSeconds != 10 || value.DurationSeconds != 100 || value.Completed || value.Version != 3 {
		t.Fatalf("retried replay progress = %+v", value)
	}
	if service.updateCalls != 2 || service.watchedCalls != 0 {
		t.Fatalf("retried replay mutations watched=%d progress=%d", service.watchedCalls, service.updateCalls)
	}
}

func TestStoppedPositionIsAuthoritativeAfterBackwardSeek(t *testing.T) {
	service := newMemoryWatchstate()
	service.progress["item"] = watchstate.Progress{
		TitleID: "item", MediaType: "movie", PositionSeconds: 3600,
		DurationSeconds: 4000, Version: 1,
	}

	stale, err := applyPlaybackProgress(context.Background(), service, auth.Principal{}, "item", 600, 4000, false, false)
	if err != nil || stale.PositionSeconds != 3600 || stale.Version != 1 {
		t.Fatalf("non-final stale progress = %+v, error %v", stale, err)
	}
	stopped, err := applyPlaybackProgress(context.Background(), service, auth.Principal{}, "item", 600, 4000, false, true)
	if err != nil {
		t.Fatalf("persist stopped seek: %v", err)
	}
	if stopped.PositionSeconds != 600 || stopped.DurationSeconds != 4000 || stopped.Completed || stopped.Version != 2 {
		t.Fatalf("stopped seek progress = %+v", stopped)
	}
}

func TestStaleProgressAndPlayingCannotRegressOrReopenState(t *testing.T) {
	for _, replay := range []bool{false, true} {
		service := newMemoryWatchstate()
		current := watchstate.Progress{
			TitleID: "item", MediaType: "movie", PositionSeconds: 90,
			DurationSeconds: 100, Version: 3,
		}
		service.progress["item"] = current
		value, err := applyPlaybackProgress(context.Background(), service, auth.Principal{}, "item", 80, 100, replay, false)
		if err != nil || value != current || service.updateCalls != 0 {
			t.Fatalf("stale non-final replay=%t regressed progress: value=%+v calls=%d err=%v", replay, value, service.updateCalls, err)
		}
	}

	service := newMemoryWatchstate()
	completed := watchstate.Progress{
		TitleID: "item", MediaType: "movie", PositionSeconds: 100,
		DurationSeconds: 100, Completed: true, Version: 3,
	}
	service.progress["item"] = completed
	value, err := applyPlaybackProgress(context.Background(), service, auth.Principal{}, "item", 90, 100, false, false)
	if err != nil || value != completed || service.updateCalls != 0 {
		t.Fatalf("stale progress reopened completion: value=%+v calls=%d err=%v", value, service.updateCalls, err)
	}
}

func TestPlaybackProgressConflictReloadKeepsNewerPosition(t *testing.T) {
	service := newMemoryWatchstate()
	service.progress["item"] = watchstate.Progress{
		TitleID: "item", MediaType: "movie", PositionSeconds: 10,
		DurationSeconds: 100, Version: 1,
	}
	service.updateConflict = func(service *memoryWatchstate, itemID string) {
		value := service.progress[itemID]
		value.PositionSeconds = 40
		value.Version++
		service.progress[itemID] = value
	}
	value, err := applyPlaybackProgress(context.Background(), service, auth.Principal{}, "item", 20, 100, false, false)
	if err != nil || value.PositionSeconds != 40 || value.Version != 2 || service.updateCalls != 1 {
		t.Fatalf("conflict merge = %+v calls=%d err=%v", value, service.updateCalls, err)
	}
}

func TestPlayedStateIsIdempotentAndRetriesOneStaleVersion(t *testing.T) {
	service := newMemoryWatchstate()
	principal := auth.Principal{}
	ctx := context.Background()

	watched, err := setWatchedIdempotent(ctx, service, principal, "item", true)
	if err != nil || !watched.Completed || watched.Version != 1 {
		t.Fatalf("mark watched = %+v err=%v", watched, err)
	}
	duplicate, err := setWatchedIdempotent(ctx, service, principal, "item", true)
	if err != nil || duplicate.Version != 1 || service.watchedCalls != 1 {
		t.Fatalf("duplicate watched = %+v calls=%d err=%v", duplicate, service.watchedCalls, err)
	}

	service.watchedConflict = func(service *memoryWatchstate, itemID string, _ bool) {
		value := service.progress[itemID]
		value.Version++
		value.Completed = true
		service.progress[itemID] = value
	}
	unwatched, err := setWatchedIdempotent(ctx, service, principal, "item", false)
	if err != nil || unwatched.Completed || unwatched.Version != 3 || service.watchedCalls != 3 {
		t.Fatalf("stale unwatched retry = %+v calls=%d err=%v", unwatched, service.watchedCalls, err)
	}
	duplicate, err = setWatchedIdempotent(ctx, service, principal, "item", false)
	if err != nil || duplicate.Version != 3 || service.watchedCalls != 3 {
		t.Fatalf("duplicate unwatched = %+v calls=%d err=%v", duplicate, service.watchedCalls, err)
	}
}

func TestPlaybackProgressRequiresAuthoritativeDuration(t *testing.T) {
	service := newMemoryWatchstate()
	_, err := applyPlaybackProgress(context.Background(), service, auth.Principal{}, "item", 1, 0, false, false)
	if !errors.Is(err, watchstate.ErrInvalidInput) || len(service.progress) != 0 {
		t.Fatalf("unknown duration error=%v progress=%+v", err, service.progress)
	}
}

func TestStateTickAndDurationConversionSaturates(t *testing.T) {
	if got := ticksToStateSeconds(-1); got != 0 {
		t.Fatalf("negative ticks = %d", got)
	}
	if got := durationToStateSeconds(math.Inf(1)); got != maximumStateInt() {
		t.Fatalf("infinite duration = %d", got)
	}
}
