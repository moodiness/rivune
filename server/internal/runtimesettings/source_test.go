package runtimesettings

import "testing"

func TestHybridHardwareAccelerationSurvivesBootAndRemainsRestartBound(t *testing.T) {
	values := Values{
		Revision:                1,
		Timezone:                "UTC",
		HardwareAcceleration:    "hybrid",
		TranscodeMaxBitrateKbps: DefaultTranscodeMaxBitrateKbps,
		MediaMaxStorageMB:       DefaultMediaMaxStorageMB,
		ArtworkMaxStorageMB:     DefaultArtworkMaxStorageMB,
		AllowTranscoding:        true,
	}
	source, err := New(values)
	if err != nil {
		t.Fatalf("initialize persisted hybrid runtime settings: %v", err)
	}
	boot := source.Load()
	if boot.HardwareAcceleration != "hybrid" || boot.RequestedHardwareAcceleration != "hybrid" {
		t.Fatalf("hybrid boot settings = active %q requested %q", boot.HardwareAcceleration, boot.RequestedHardwareAcceleration)
	}

	values.Revision++
	values.HardwareAcceleration = "auto"
	if err := source.Publish(values); err != nil {
		t.Fatalf("publish restart-bound hardware change: %v", err)
	}
	updated := source.Load()
	if updated.HardwareAcceleration != "hybrid" || updated.RequestedHardwareAcceleration != "auto" {
		t.Fatalf("restart-bound settings = active %q requested %q", updated.HardwareAcceleration, updated.RequestedHardwareAcceleration)
	}
}
