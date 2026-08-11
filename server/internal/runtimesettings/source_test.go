package runtimesettings

import "testing"

func TestPlannerSettingsSurviveBootAndRemainRestartBound(t *testing.T) {
	values := Values{
		Revision: 1, Timezone: "UTC", HardwareAcceleration: "hybrid",
		PreferredTranscodeVideoCodec: "hevc", TranscodeQualityPreset: "quality", TranscodeConcurrency: 6,
		TranscodeMaxBitrateKbps: DefaultTranscodeMaxBitrateKbps,
		MediaMaxStorageMB:       DefaultMediaMaxStorageMB, ArtworkMaxStorageMB: DefaultArtworkMaxStorageMB,
		AllowTranscoding: true,
	}
	source, err := New(values)
	if err != nil {
		t.Fatalf("initialize persisted runtime settings: %v", err)
	}
	boot := source.Load()
	if boot.HardwareAcceleration != "hybrid" || boot.RequestedHardwareAcceleration != "hybrid" ||
		boot.PreferredTranscodeVideoCodec != "hevc" || boot.RequestedPreferredTranscodeVideoCodec != "hevc" ||
		boot.TranscodeQualityPreset != "quality" || boot.RequestedTranscodeQualityPreset != "quality" ||
		boot.TranscodeConcurrency != 6 || boot.RequestedTranscodeConcurrency != 6 {
		t.Fatalf("boot settings = %+v", boot)
	}

	values.Revision++
	values.HardwareAcceleration = "amf"
	values.PreferredTranscodeVideoCodec = "av1"
	values.TranscodeQualityPreset = "speed"
	values.TranscodeConcurrency = 12
	values.TranscodeMaxBitrateKbps = 24000
	if err := source.Publish(values); err != nil {
		t.Fatalf("publish restart-bound change: %v", err)
	}
	updated := source.Load()
	if updated.HardwareAcceleration != "hybrid" || updated.RequestedHardwareAcceleration != "amf" ||
		updated.PreferredTranscodeVideoCodec != "hevc" || updated.RequestedPreferredTranscodeVideoCodec != "av1" ||
		updated.TranscodeQualityPreset != "quality" || updated.RequestedTranscodeQualityPreset != "speed" ||
		updated.TranscodeConcurrency != 6 || updated.RequestedTranscodeConcurrency != 12 {
		t.Fatalf("restart-bound settings = %+v", updated)
	}
	if updated.TranscodeMaxBitrateKbps != 24000 {
		t.Fatalf("live bitrate did not update: %+v", updated)
	}
}

func TestPlannerSettingsRejectInvalidEnumsAndConcurrency(t *testing.T) {
	valid := Values{
		Timezone: "UTC", HardwareAcceleration: "auto", PreferredTranscodeVideoCodec: "auto",
		TranscodeQualityPreset: "balanced", TranscodeConcurrency: 4,
		TranscodeMaxBitrateKbps: DefaultTranscodeMaxBitrateKbps,
		MediaMaxStorageMB:       DefaultMediaMaxStorageMB, ArtworkMaxStorageMB: DefaultArtworkMaxStorageMB,
		AllowTranscoding: true,
	}
	invalid := []Values{valid, valid, valid, valid}
	invalid[0].HardwareAcceleration = "cuda"
	invalid[1].PreferredTranscodeVideoCodec = "vp9"
	invalid[2].TranscodeQualityPreset = "ultrafast"
	invalid[3].TranscodeConcurrency = 33
	for _, values := range invalid {
		if _, err := New(values); err != ErrInvalidValues {
			t.Fatalf("invalid values accepted: %+v error=%v", values, err)
		}
	}
}
