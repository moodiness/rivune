package playback

import (
	"errors"
	"testing"
)

func TestApplyPlaybackPreferencesSelectsLanguageAndMinimalProcessing(t *testing.T) {
	sources := []Source{{
		ID: "stream-1", Mode: "direct", Protocol: "http", Container: "mp4", Compatible: true,
		Media: &MediaInspection{AudioTracks: []MediaTrack{
			{Index: 1, Type: "audio", Codec: "aac", Language: "en"},
			{Index: 2, Type: "audio", Codec: "dts", Language: "fr"},
		}},
	}}
	assets := []storedAsset{{ID: "stream-1", Kind: "stream", URL: "https://media.example/movie.mp4"}}

	err := applyPlaybackPreferences(sources, assets, ResolveInput{
		PreferredAudioLanguage: "fr-FR",
		Capabilities:           Capabilities{StreamingProtocols: []string{"hls"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if assets[0].AudioTrackIndex == nil || *assets[0].AudioTrackIndex != 2 {
		t.Fatalf("French audio track was not selected: %+v", assets[0])
	}
	if sources[0].Mode != processingTranscodeAudio || sources[0].Protocol != "hls" || assets[0].Kind != processingTranscodeAudio {
		t.Fatalf("minimal audio processing was not selected: source=%+v asset=%+v", sources[0], assets[0])
	}
}

func TestApplyPlaybackPreferencesRejectsUnknownExplicitTrack(t *testing.T) {
	unknown := 7
	sources := []Source{{
		ID: "stream-1", Compatible: true,
		Media: &MediaInspection{AudioTracks: []MediaTrack{{Index: 1, Type: "audio", Codec: "aac"}}},
	}}
	assets := []storedAsset{{ID: "stream-1", Kind: "stream"}}

	if err := applyPlaybackPreferences(sources, assets, ResolveInput{PreferredAudioTrack: &unknown}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid explicit track error, got %v", err)
	}
}

func TestSubtitlePreferencesSupportLanguageExplicitAndOff(t *testing.T) {
	subtitles := []Subtitle{{ID: "en", Language: "en-US"}, {ID: "fr", Language: "fr"}}
	if err := applySubtitlePreference(subtitles, "", "fr-FR"); err != nil {
		t.Fatal(err)
	}
	if !subtitles[1].Default || selectedSubtitle(subtitles) != "fr" {
		t.Fatalf("French subtitle was not selected: %+v", subtitles)
	}

	subtitles = []Subtitle{{ID: "en", Language: "en", Default: false}}
	if err := applySubtitlePreference(subtitles, "none", "en"); err != nil || selectedSubtitle(subtitles) != "" {
		t.Fatalf("subtitle off preference was not respected: %+v err=%v", subtitles, err)
	}

	if err := applySubtitlePreference(subtitles, "missing", ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid explicit subtitle error, got %v", err)
	}
}

func TestEmbeddedAndTextSubtitlesBecomePlayableAssets(t *testing.T) {
	sources := []Source{{
		ID: "stream-1", AddonID: "addon", ManifestID: "manifest", Compatible: true,
		Media: &MediaInspection{SubtitleTracks: []MediaTrack{
			{Index: 4, Type: "subtitle", Codec: "ass", Language: "fr"},
			{Index: 5, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Language: "en"},
		}},
	}}
	assets := []storedAsset{{ID: "stream-1", Kind: "stream", URL: "https://media.example/movie.mkv", Headers: map[string]string{"Authorization": "secret"}}}

	subtitles, subtitleAssets := embeddedSubtitles(sources, assets)
	if len(subtitles) != 1 || len(subtitleAssets) != 1 || subtitleAssets[0].SubtitleTrackIndex == nil || *subtitleAssets[0].SubtitleTrackIndex != 4 {
		t.Fatalf("embedded subtitle was not exposed: subtitles=%+v assets=%+v", subtitles, subtitleAssets)
	}
	if subtitleAssets[0].Kind != assetKindEmbeddedSubtitle || subtitleAssets[0].Headers["Authorization"] != "secret" {
		t.Fatalf("embedded subtitle source was not preserved: %+v", subtitleAssets[0])
	}
}
