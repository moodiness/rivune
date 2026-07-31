package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type cleanupServiceStub struct {
	calls chan struct{}
	err   error
}

func (service *cleanupServiceStub) Cleanup(ctx context.Context) error {
	select {
	case service.calls <- struct{}{}:
		return service.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestMaintenanceRunsImmediatelyContinuesAfterFailureAndStopsOnCancellation(t *testing.T) {
	authService := &cleanupServiceStub{calls: make(chan struct{}), err: errors.New("auth unavailable")}
	playbackService := &cleanupServiceStub{calls: make(chan struct{})}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		runMaintenance(ctx, logger, authService, playbackService, time.Hour)
		close(done)
	}()

	awaitCleanupCall(t, authService.calls, "authentication")
	awaitCleanupCall(t, playbackService.calls, "playback")
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance runner did not stop after cancellation")
	}
}

func TestMaintenanceIntervalIsFiveMinutes(t *testing.T) {
	if maintenanceInterval != 5*time.Minute {
		t.Fatalf("maintenance interval = %s, want 5m", maintenanceInterval)
	}
}

func awaitCleanupCall(t *testing.T, calls <-chan struct{}, service string) {
	t.Helper()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatalf("%s cleanup was not called", service)
	}
}
