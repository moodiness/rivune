package httpapi

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moodiness/rivune/server/internal/runtimesettings"
	"github.com/moodiness/rivune/server/internal/settings"
)

type runtimeSettingsReader interface {
	Instance(context.Context) (settings.Layer, error)
}

type artworkStorageLimiter interface {
	ApplyStorageLimit(context.Context, int64) error
}

type mediaStorageLimiter interface {
	ApplyMediaStorageLimit(context.Context, int64) error
}

type runtimeSettingsCoordinator struct {
	mu           sync.Mutex
	source       *runtimesettings.Source
	settings     runtimeSettingsReader
	artwork      artworkStorageLimiter
	media        mediaStorageLimiter
	reconciling  atomic.Bool
	onReconciled func(runtimesettings.Snapshot, runtimesettings.Snapshot)
}

func newRuntimeSettingsCoordinator(source *runtimesettings.Source, service runtimeSettingsReader, artwork artworkStorageLimiter, media mediaStorageLimiter) *runtimeSettingsCoordinator {
	return &runtimeSettingsCoordinator{source: source, settings: service, artwork: artwork, media: media}
}

func defaultRuntimeValues() runtimesettings.Values {
	return runtimesettings.Values{
		Timezone:                settings.DefaultTimezone,
		HardwareAcceleration:    settings.DefaultHardwareAcceleration,
		TranscodeMaxBitrateKbps: settings.DefaultTranscodeMaxBitrateKbps,
		MediaMaxStorageMB:       settings.DefaultMediaMaxStorageMB,
		ArtworkMaxStorageMB:     settings.DefaultArtworkMaxStorageMB,
		AllowTranscoding:        settings.DefaultAllowTranscoding,
	}
}

func runtimeValuesFromLayer(layer settings.Layer) (runtimesettings.Values, error) {
	values := layer.Values
	if values.Timezone == nil || values.JellyfinEnabled == nil || values.JellyfinDebug == nil ||
		values.HardwareAcceleration == nil || values.TranscodeMaxBitrateKbps == nil ||
		values.MediaMaxStorageMB == nil || values.ArtworkMaxStorageMB == nil || values.AllowTranscoding == nil {
		return runtimesettings.Values{}, errors.New("instance runtime settings are not materialized")
	}
	return runtimesettings.Values{
		Revision:                layer.Revision,
		Timezone:                *values.Timezone,
		JellyfinEnabled:         *values.JellyfinEnabled,
		JellyfinDebug:           *values.JellyfinDebug,
		HardwareAcceleration:    *values.HardwareAcceleration,
		TranscodeMaxBitrateKbps: *values.TranscodeMaxBitrateKbps,
		MediaMaxStorageMB:       *values.MediaMaxStorageMB,
		ArtworkMaxStorageMB:     *values.ArtworkMaxStorageMB,
		AllowTranscoding:        *values.AllowTranscoding,
	}, nil
}

func (coordinator *runtimeSettingsCoordinator) publish(ctx context.Context, layer settings.Layer) error {
	if coordinator == nil || coordinator.source == nil {
		return errors.New("runtime settings coordinator is not configured")
	}
	values, err := runtimeValuesFromLayer(layer)
	if err != nil {
		return err
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	current := coordinator.source.Load()
	if values.Revision <= current.Revision {
		return nil
	}
	mediaChanged := coordinator.media != nil && current.MediaMaxStorageBytes != int64(values.MediaMaxStorageMB)<<20
	artworkChanged := coordinator.artwork != nil && current.ArtworkMaxStorageBytes != int64(values.ArtworkMaxStorageMB)<<20
	if mediaChanged {
		if err := coordinator.media.ApplyMediaStorageLimit(ctx, int64(values.MediaMaxStorageMB)<<20); err != nil {
			return fmt.Errorf("apply media storage limit: %w", err)
		}
	}
	if artworkChanged {
		if err := coordinator.artwork.ApplyStorageLimit(ctx, int64(values.ArtworkMaxStorageMB)<<20); err != nil {
			if mediaChanged {
				_ = coordinator.media.ApplyMediaStorageLimit(ctx, current.MediaMaxStorageBytes)
			}
			return fmt.Errorf("apply artwork storage limit: %w", err)
		}
	}
	if err := coordinator.source.Publish(values); err != nil {
		if artworkChanged {
			_ = coordinator.artwork.ApplyStorageLimit(ctx, current.ArtworkMaxStorageBytes)
		}
		if mediaChanged {
			_ = coordinator.media.ApplyMediaStorageLimit(ctx, current.MediaMaxStorageBytes)
		}
		return fmt.Errorf("publish runtime settings: %w", err)
	}
	if coordinator.onReconciled != nil {
		coordinator.onReconciled(current, coordinator.source.Load())
	}
	return nil
}

func (coordinator *runtimeSettingsCoordinator) reconcile(ctx context.Context) error {
	if coordinator == nil || coordinator.settings == nil {
		return errors.New("runtime settings reconciliation is not configured")
	}
	layer, err := coordinator.settings.Instance(ctx)
	if err != nil {
		return err
	}
	return coordinator.publish(ctx, layer)
}

func (coordinator *runtimeSettingsCoordinator) scheduleReconciliation() {
	if coordinator == nil || !coordinator.reconciling.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer coordinator.reconciling.Store(false)
		for {
			if coordinator.reconcile(context.Background()) == nil {
				return
			}
			timer := time.NewTimer(30 * time.Second)
			<-timer.C
		}
	}()
}

type runtimeSettingsResponseValues struct {
	Timezone                string `json:"timezone"`
	JellyfinEnabled         bool   `json:"jellyfinEnabled"`
	JellyfinDebug           bool   `json:"jellyfinDebug"`
	HardwareAcceleration    string `json:"hardwareAcceleration"`
	TranscodeMaxBitrateKbps int    `json:"transcodeMaxBitrateKbps"`
	MediaMaxStorageMB       int64  `json:"mediaMaxStorageMB"`
	ArtworkMaxStorageMB     int64  `json:"artworkMaxStorageMB"`
	AllowTranscoding        bool   `json:"allowTranscoding"`
}

func runtimeSettingsApplication(source *runtimesettings.Source) map[string]any {
	snapshot := runtimesettings.Snapshot{
		Timezone: "UTC", HardwareAcceleration: "auto", RequestedHardwareAcceleration: "auto",
		TranscodeMaxBitrateKbps: 12000, MediaMaxStorageBytes: 20480 << 20,
		ArtworkMaxStorageBytes: 20480 << 20, AllowTranscoding: true,
	}
	if source != nil {
		snapshot = source.Load()
	}
	pendingRestart := make([]string, 0, 1)
	if snapshot.HardwareAcceleration != snapshot.RequestedHardwareAcceleration {
		pendingRestart = append(pendingRestart, "hardwareAcceleration")
	}
	return map[string]any{
		"active": runtimeSettingsResponseValues{
			Timezone:                snapshot.Timezone,
			JellyfinEnabled:         snapshot.JellyfinEnabled,
			JellyfinDebug:           snapshot.JellyfinDebug,
			HardwareAcceleration:    snapshot.HardwareAcceleration,
			TranscodeMaxBitrateKbps: snapshot.TranscodeMaxBitrateKbps,
			MediaMaxStorageMB:       snapshot.MediaMaxStorageBytes >> 20,
			ArtworkMaxStorageMB:     snapshot.ArtworkMaxStorageBytes >> 20,
			AllowTranscoding:        snapshot.AllowTranscoding,
		},
		"pendingRestart": pendingRestart,
	}
}
