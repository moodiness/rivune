package runtimesettings

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"
)

const (
	DefaultTimezone                     = "UTC"
	DefaultHardwareAcceleration         = "auto"
	DefaultPreferredTranscodeVideoCodec = "auto"
	DefaultTranscodeQualityPreset       = "balanced"
	DefaultTranscodeConcurrency         = 4
	DefaultTranscodeMaxBitrateKbps      = 12000
	DefaultMediaMaxStorageMB            = 20480
	DefaultArtworkMaxStorageMB          = 20480
)

var ErrInvalidValues = errors.New("invalid runtime settings")

// Values is the complete persisted runtime configuration. A Source publishes
// Values as one indivisible generation; callers must never patch a loaded
// Snapshot in place.
type Values struct {
	Revision                     int64
	Timezone                     string
	JellyfinEnabled              bool
	JellyfinDebug                bool
	HardwareAcceleration         string
	PreferredTranscodeVideoCodec string
	TranscodeQualityPreset       string
	TranscodeConcurrency         int
	TranscodeMaxBitrateKbps      int
	MediaMaxStorageMB            int
	ArtworkMaxStorageMB          int
	AllowTranscoding             bool
}

// Snapshot is an immutable runtime generation. Location is safe to share:
// time.Location values are immutable after construction.
type Snapshot struct {
	Revision                              int64
	Timezone                              string
	Location                              *time.Location
	JellyfinEnabled                       bool
	JellyfinDebug                         bool
	HardwareAcceleration                  string
	RequestedHardwareAcceleration         string
	PreferredTranscodeVideoCodec          string
	RequestedPreferredTranscodeVideoCodec string
	TranscodeQualityPreset                string
	RequestedTranscodeQualityPreset       string
	TranscodeConcurrency                  int
	RequestedTranscodeConcurrency         int
	TranscodeMaxBitrateKbps               int
	MediaMaxStorageBytes                  int64
	ArtworkMaxStorageBytes                int64
	AllowTranscoding                      bool
}

type Source struct {
	current                          atomic.Pointer[Snapshot]
	bootHardwareAcceleration         string
	bootPreferredTranscodeVideoCodec string
	bootTranscodeQualityPreset       string
	bootTranscodeConcurrency         int
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

// New validates and publishes the initial generation. Restart-bound settings
// are captured as boot-active and remain unchanged by later publications.
func New(initial Values) (*Source, error) {
	source := &Source{}
	snapshot, err := source.build(initial, Snapshot{})
	if err != nil {
		return nil, err
	}
	source.bootHardwareAcceleration = snapshot.HardwareAcceleration
	source.bootPreferredTranscodeVideoCodec = snapshot.PreferredTranscodeVideoCodec
	source.bootTranscodeQualityPreset = snapshot.TranscodeQualityPreset
	source.bootTranscodeConcurrency = snapshot.TranscodeConcurrency
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

// Publish validates and atomically replaces the complete generation. Boot-active
// settings remain fixed; requested values are retained for restart reporting.
func (source *Source) Publish(values Values) error {
	if source == nil {
		return ErrInvalidValues
	}
	boot := Snapshot{
		HardwareAcceleration:         source.bootHardwareAcceleration,
		PreferredTranscodeVideoCodec: source.bootPreferredTranscodeVideoCodec,
		TranscodeQualityPreset:       source.bootTranscodeQualityPreset,
		TranscodeConcurrency:         source.bootTranscodeConcurrency,
	}
	snapshot, err := source.build(values, boot)
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

func (source *Source) build(values Values, boot Snapshot) (*Snapshot, error) {
	if values.Revision < 0 || strings.TrimSpace(values.Timezone) != values.Timezone || values.Timezone == "" || values.Timezone == "Local" {
		return nil, ErrInvalidValues
	}
	location, err := time.LoadLocation(values.Timezone)
	if err != nil {
		return nil, ErrInvalidValues
	}
	if values.TranscodeMaxBitrateKbps < 64 || values.TranscodeMaxBitrateKbps > 200000 ||
		values.TranscodeConcurrency < 1 || values.TranscodeConcurrency > 32 ||
		values.MediaMaxStorageMB < 512 || values.MediaMaxStorageMB > 102400 ||
		values.ArtworkMaxStorageMB < 256 || values.ArtworkMaxStorageMB > 102400 {
		return nil, ErrInvalidValues
	}
	requestedHardwareAcceleration := strings.ToLower(strings.TrimSpace(values.HardwareAcceleration))
	switch requestedHardwareAcceleration {
	case "auto", "software", "hybrid", "vaapi", "qsv", "nvenc", "amf":
	default:
		return nil, ErrInvalidValues
	}
	requestedPreferredTranscodeVideoCodec := strings.ToLower(strings.TrimSpace(values.PreferredTranscodeVideoCodec))
	switch requestedPreferredTranscodeVideoCodec {
	case "auto", "h264", "hevc", "av1":
	default:
		return nil, ErrInvalidValues
	}
	requestedTranscodeQualityPreset := strings.ToLower(strings.TrimSpace(values.TranscodeQualityPreset))
	switch requestedTranscodeQualityPreset {
	case "speed", "balanced", "quality":
	default:
		return nil, ErrInvalidValues
	}
	activeHardwareAcceleration := requestedHardwareAcceleration
	activePreferredTranscodeVideoCodec := requestedPreferredTranscodeVideoCodec
	activeTranscodeQualityPreset := requestedTranscodeQualityPreset
	activeTranscodeConcurrency := values.TranscodeConcurrency
	if boot.HardwareAcceleration != "" {
		activeHardwareAcceleration = boot.HardwareAcceleration
		activePreferredTranscodeVideoCodec = boot.PreferredTranscodeVideoCodec
		activeTranscodeQualityPreset = boot.TranscodeQualityPreset
		activeTranscodeConcurrency = boot.TranscodeConcurrency
	}
	return &Snapshot{
		Revision:                              values.Revision,
		Timezone:                              values.Timezone,
		Location:                              location,
		JellyfinEnabled:                       values.JellyfinEnabled,
		JellyfinDebug:                         values.JellyfinDebug,
		HardwareAcceleration:                  activeHardwareAcceleration,
		RequestedHardwareAcceleration:         requestedHardwareAcceleration,
		PreferredTranscodeVideoCodec:          activePreferredTranscodeVideoCodec,
		RequestedPreferredTranscodeVideoCodec: requestedPreferredTranscodeVideoCodec,
		TranscodeQualityPreset:                activeTranscodeQualityPreset,
		RequestedTranscodeQualityPreset:       requestedTranscodeQualityPreset,
		TranscodeConcurrency:                  activeTranscodeConcurrency,
		RequestedTranscodeConcurrency:         values.TranscodeConcurrency,
		TranscodeMaxBitrateKbps:               values.TranscodeMaxBitrateKbps,
		MediaMaxStorageBytes:                  int64(values.MediaMaxStorageMB) << 20,
		ArtworkMaxStorageBytes:                int64(values.ArtworkMaxStorageMB) << 20,
		AllowTranscoding:                      values.AllowTranscoding,
	}, nil
}
