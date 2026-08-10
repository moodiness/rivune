package httpapi

import (
	"context"
	"errors"
	"testing"

	"github.com/moodiness/rivune/server/internal/runtimesettings"
	"github.com/moodiness/rivune/server/internal/settings"
)

type fakeRuntimeStorageLimiter struct {
	limits []int64
	err    error
}

func (limiter *fakeRuntimeStorageLimiter) ApplyStorageLimit(_ context.Context, limit int64) error {
	limiter.limits = append(limiter.limits, limit)
	return limiter.err
}

func (limiter *fakeRuntimeStorageLimiter) ApplyMediaStorageLimit(_ context.Context, limit int64) error {
	limiter.limits = append(limiter.limits, limit)
	return limiter.err
}

func TestRuntimeSettingsCoordinatorPublishesLiveValuesButKeepsBootHardwareActive(t *testing.T) {
	initial := runtimesettings.Values{
		Revision: 1, Timezone: "UTC", HardwareAcceleration: "software",
		TranscodeMaxBitrateKbps: 12000, MediaMaxStorageMB: 20480,
		ArtworkMaxStorageMB: 20480, AllowTranscoding: true,
	}
	source, err := runtimesettings.New(initial)
	if err != nil {
		t.Fatal(err)
	}
	media := &fakeRuntimeStorageLimiter{}
	artwork := &fakeRuntimeStorageLimiter{}
	coordinator := newRuntimeSettingsCoordinator(source, nil, artwork, media)
	layer := materializedRuntimeLayer(2, "America/New_York", "nvenc", 24000, 1024, 512, false)
	if err := coordinator.publish(context.Background(), layer); err != nil {
		t.Fatal(err)
	}
	snapshot := source.Load()
	if snapshot.Revision != 2 || snapshot.Timezone != "America/New_York" || snapshot.TranscodeMaxBitrateKbps != 24000 || snapshot.AllowTranscoding {
		t.Fatalf("unexpected active runtime snapshot: %+v", snapshot)
	}
	if snapshot.HardwareAcceleration != "software" || snapshot.RequestedHardwareAcceleration != "nvenc" {
		t.Fatalf("hardware active/requested = %q/%q", snapshot.HardwareAcceleration, snapshot.RequestedHardwareAcceleration)
	}
	if len(media.limits) != 1 || media.limits[0] != int64(1024)<<20 || len(artwork.limits) != 1 || artwork.limits[0] != int64(512)<<20 {
		t.Fatalf("quota applications media=%v artwork=%v", media.limits, artwork.limits)
	}
	application := runtimeSettingsApplication(source)
	pending := application["pendingRestart"].([]string)
	if len(pending) != 1 || pending[0] != "hardwareAcceleration" {
		t.Fatalf("pending restart = %v", pending)
	}
}

func TestRuntimeSettingsCoordinatorRollsBackEarlierQuotaWhenLaterApplicationFails(t *testing.T) {
	initial := runtimesettings.Values{
		Revision: 5, Timezone: "UTC", HardwareAcceleration: "auto",
		TranscodeMaxBitrateKbps: 12000, MediaMaxStorageMB: 20480,
		ArtworkMaxStorageMB: 20480, AllowTranscoding: true,
	}
	source, err := runtimesettings.New(initial)
	if err != nil {
		t.Fatal(err)
	}
	media := &fakeRuntimeStorageLimiter{}
	artwork := &fakeRuntimeStorageLimiter{err: errors.New("prune failed")}
	coordinator := newRuntimeSettingsCoordinator(source, nil, artwork, media)
	if err := coordinator.publish(context.Background(), materializedRuntimeLayer(6, "UTC", "auto", 12000, 1024, 512, true)); err == nil {
		t.Fatal("quota failure did not fail publication")
	}
	if source.Load().Revision != 5 {
		t.Fatalf("failed publication advanced revision to %d", source.Load().Revision)
	}
	if len(media.limits) != 2 || media.limits[0] != int64(1024)<<20 || media.limits[1] != int64(20480)<<20 {
		t.Fatalf("media rollback limits = %v", media.limits)
	}
}

func materializedRuntimeLayer(revision int64, timezone, hardware string, bitrate, mediaMB, artworkMB int, allow bool) settings.Layer {
	enabled := true
	debug := false
	return settings.Layer{Revision: revision, SchemaVersion: 2, Values: settings.Values{
		Timezone: &timezone, JellyfinEnabled: &enabled, JellyfinDebug: &debug,
		HardwareAcceleration: &hardware, TranscodeMaxBitrateKbps: &bitrate,
		MediaMaxStorageMB: &mediaMB, ArtworkMaxStorageMB: &artworkMB, AllowTranscoding: &allow,
	}}
}
