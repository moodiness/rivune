package runtimesettings

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"
)

const (
	DefaultTimezone                = "UTC"
	DefaultHardwareAcceleration    = "auto"
	DefaultTranscodeMaxBitrateKbps = 12000
	DefaultMediaMaxStorageMB       = 20480
	DefaultArtworkMaxStorageMB     = 20480
)

var ErrInvalidValues = errors.New("invalid runtime settings")

// Values is the complete persisted runtime configuration. A Source publishes
// Values as one indivisible generation; callers must never patch a loaded
// Snapshot in place.
type Values struct {
	Revision                int64
	Timezone                string
	JellyfinEnabled         bool
	JellyfinDebug           bool
	HardwareAcceleration    string
	TranscodeMaxBitrateKbps int
	MediaMaxStorageMB       int
	ArtworkMaxStorageMB     int
	AllowTranscoding        bool
}

// Snapshot is an immutable runtime generation. Location is safe to share:
// time.Location values are immutable after construction.
type Snapshot struct {
	Revision                      int64
	Timezone                      string
	Location                      *time.Location
	JellyfinEnabled               bool
	JellyfinDebug                 bool
	HardwareAcceleration          string
	RequestedHardwareAcceleration string
	TranscodeMaxBitrateKbps       int
	MediaMaxStorageBytes          int64
	ArtworkMaxStorageBytes        int64
	AllowTranscoding              bool
}

type Source struct {
	current                  atomic.Pointer[Snapshot]
	bootHardwareAcceleration string
}

type snapshotContextKey struct{}

// Pin loads at most one generation into ctx. HTTP composition should pin once
// at request admission; background workers should pin once per turn.
func Pin(ctx context.Context, source *Source) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if source == nil {
		return ctx
	}
	if _, ok := ctx.Value(snapshotContextKey{}).(Snapshot); ok {
		return ctx
	}
	return context.WithValue(ctx, snapshotContextKey{}, source.Load())
}

// Load returns the request-pinned generation when present, otherwise it makes
// one atomic source load for callers that are already a logical boundary.
func Load(ctx context.Context, source *Source) Snapshot {
	if ctx != nil {
		if snapshot, ok := ctx.Value(snapshotContextKey{}).(Snapshot); ok {
			return snapshot
		}
	}
	return source.Load()
}

// New validates and publishes the initial generation. Hardware acceleration is
// captured as boot-active and remains unchanged by later publications.
func New(initial Values) (*Source, error) {
	source := &Source{}
	snapshot, err := source.build(initial, "")
	if err != nil {
		return nil, err
	}
	source.bootHardwareAcceleration = snapshot.HardwareAcceleration
	source.current.Store(snapshot)
	return source, nil
}

// Load performs one atomic read. The returned value is detached from the
// publication pointer so consumers can retain it for an entire operation.
func (source *Source) Load() Snapshot {
	if source == nil {
		return Snapshot{}
	}
	current := source.current.Load()
	if current == nil {
		return Snapshot{}
	}
	return *current
}

// Publish validates and atomically replaces the complete generation. The
// boot-active hardware acceleration remains fixed; its requested value is
// retained so composition can report pending restart truthfully.
func (source *Source) Publish(values Values) error {
	if source == nil {
		return ErrInvalidValues
	}
	snapshot, err := source.build(values, source.bootHardwareAcceleration)
	if err != nil {
		return err
	}
	for {
		current := source.current.Load()
		if current == nil || snapshot.Revision <= current.Revision {
			return ErrInvalidValues
		}
		if source.current.CompareAndSwap(current, snapshot) {
			return nil
		}
	}
}

func (source *Source) build(values Values, bootHardwareAcceleration string) (*Snapshot, error) {
	if values.Revision < 0 || strings.TrimSpace(values.Timezone) != values.Timezone || values.Timezone == "" || values.Timezone == "Local" {
		return nil, ErrInvalidValues
	}
	location, err := time.LoadLocation(values.Timezone)
	if err != nil {
		return nil, ErrInvalidValues
	}
	if values.TranscodeMaxBitrateKbps < 64 || values.TranscodeMaxBitrateKbps > 200000 ||
		values.MediaMaxStorageMB < 512 || values.MediaMaxStorageMB > 102400 ||
		values.ArtworkMaxStorageMB < 256 || values.ArtworkMaxStorageMB > 102400 {
		return nil, ErrInvalidValues
	}
	requestedHardwareAcceleration := strings.ToLower(strings.TrimSpace(values.HardwareAcceleration))
	switch requestedHardwareAcceleration {
	case "auto", "software", "vaapi", "qsv", "nvenc":
	default:
		return nil, ErrInvalidValues
	}
	activeHardwareAcceleration := requestedHardwareAcceleration
	if bootHardwareAcceleration != "" {
		activeHardwareAcceleration = bootHardwareAcceleration
	}
	return &Snapshot{
		Revision:                      values.Revision,
		Timezone:                      values.Timezone,
		Location:                      location,
		JellyfinEnabled:               values.JellyfinEnabled,
		JellyfinDebug:                 values.JellyfinDebug,
		HardwareAcceleration:          activeHardwareAcceleration,
		RequestedHardwareAcceleration: requestedHardwareAcceleration,
		TranscodeMaxBitrateKbps:       values.TranscodeMaxBitrateKbps,
		MediaMaxStorageBytes:          int64(values.MediaMaxStorageMB) << 20,
		ArtworkMaxStorageBytes:        int64(values.ArtworkMaxStorageMB) << 20,
		AllowTranscoding:              values.AllowTranscoding,
	}, nil
}
