package playback

import (
	"errors"
	"testing"
)

func TestApplyPlaybackPreferencesRemuxesSupportedAlternateAudioWithoutEncoding(t *testing.T) {
	sources := []Source{{
		ID: "stream-1", Mode: "direct", Protocol: "http", Container: "mp4", Compatible: true,
		Media: &MediaInspection{VideoTracks: []MediaTrack{{Index: 0, Type: "video", Codec: "h264"}}, AudioTracks: []MediaTrack{
			{Index: 1, Type: "audio", Codec: "aac", Language: "en"},
			{Index: 2, Type: "audio", Codec: "aac", Language: "fr"},
		}},
	}}
	assets := []storedAsset{{ID: "stream-1", Kind: "stream", URL: "https://media.example/movie.mp4"}}

	err := applyPlaybackPreferences(sources, assets, ResolveInput{
		PreferredAudioLanguage: "fr-FR",
		Capabilities: Capabilities{
			StreamingProtocols: []string{"hls"},
			Containers:         []string{"mp4"},
			VideoCodecs:        []string{"h264"},
			AudioCodecs:        []string{"aac"},
			ProcessingModes:    []string{processingRemux},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if assets[0].AudioTrackIndex == nil || *assets[0].AudioTrackIndex != 2 ||
		sources[0].Mode != processingRemux || sources[0].Protocol != "hls" || assets[0].Kind != processingRemux {
		t.Fatalf("supported alternate audio was not remuxed: source=%+v asset=%+v", sources[0], assets[0])
	}
}

func TestApplyPlaybackPreferencesRejectsHTTPOnlyAlternateAudioRemux(t *testing.T) {
	sources := []Source{{
		ID: "stream-1", Mode: "direct", Protocol: "http", Container: "mp4", Compatible: true,
		Media: &MediaInspection{VideoTracks: []MediaTrack{{Index: 0, Type: "video", Codec: "h264"}}, AudioTracks: []MediaTrack{
			{Index: 1, Type: "audio", Codec: "aac", Language: "en"},
			{Index: 2, Type: "audio", Codec: "aac", Language: "fr"},
		}},
	}}
	assets := []storedAsset{{ID: "stream-1", Kind: "stream", URL: "https://media.example/movie.mp4"}}

	err := applyPlaybackPreferences(sources, assets, ResolveInput{
		PreferredAudioLanguage: "fr-FR",
		Capabilities: Capabilities{
			StreamingProtocols: []string{"http"},
			Containers:         []string{"mp4"},
			VideoCodecs:        []string{"h264"},
			AudioCodecs:        []string{"aac"},
			ProcessingModes:    []string{processingRemux},
		},
	})
	if !errors.Is(err, ErrClientCapabilityMissing) || sources[0].Mode != "direct" || assets[0].Kind != "stream" {
		t.Fatalf("HTTP-only alternate audio remux was not rejected: err=%v source=%+v asset=%+v", err, sources[0], assets[0])
	}
}

func TestApplyPlaybackPreferencesReportsMissingAudioConversionMode(t *testing.T) {
	sources := []Source{{
		ID: "stream-1", Mode: "direct", Protocol: "http", Container: "mp4", Compatible: true,
		Media: &MediaInspection{VideoTracks: []MediaTrack{{Index: 0, Type: "video", Codec: "h264"}}, AudioTracks: []MediaTrack{
			{Index: 1, Type: "audio", Codec: "aac", Language: "en"},
			{Index: 2, Type: "audio", Codec: "dts", Language: "fr"},
		}},
	}}
	assets := []storedAsset{{ID: "stream-1", Kind: "stream", URL: "https://media.example/movie.mp4"}}
	unsupportedTrack := 2

	err := applyPlaybackPreferences(sources, assets, ResolveInput{
		PreferredAudioTrack: &unsupportedTrack,
		AllowTranscoding:    true,
		Capabilities: Capabilities{
			StreamingProtocols: []string{"hls"},
			Containers:         []string{"mp4"},
			VideoCodecs:        []string{"h264"},
			AudioCodecs:        []string{"aac"},
			ProcessingModes:    []string{processingRemux},
		},
	})
	if !errors.Is(err, ErrClientCapabilityMissing) || assets[0].Kind != "stream" {
		t.Fatalf("missing audio conversion mode was not reported: err=%v source=%+v asset=%+v", err, sources[0], assets[0])
	}
}

func TestApplyPlaybackPreferencesFallsBackToPlayableAudioForAutomaticLanguage(t *testing.T) {
	sources := []Source{{
		ID: "stream-1", Mode: processingRemux, Protocol: "hls", Container: "hls", Compatible: true,
		Media: &MediaInspection{VideoTracks: []MediaTrack{{Index: 0, Type: "video", Codec: "h264"}}, AudioTracks: []MediaTrack{
			{Index: 1, Type: "audio", Codec: "dts", Language: "fr"},
			{Index: 2, Type: "audio", Codec: "aac", Language: "en"},
		}},
	}}
	assets := []storedAsset{{ID: "stream-1", Kind: processingRemux}}
	err := applyPlaybackPreferences(sources, assets, ResolveInput{
		PreferredAudioLanguage: "fr-FR",
		Capabilities: Capabilities{
			StreamingProtocols: []string{"hls"},
			Containers:         []string{"mp4"},
			VideoCodecs:        []string{"h264"},
			AudioCodecs:        []string{"aac"},
			ProcessingModes:    []string{processingRemux},
		},
	})
	if err != nil || assets[0].AudioTrackIndex == nil || *assets[0].AudioTrackIndex != 2 {
		t.Fatalf("automatic language preference did not fall back to playable audio: err=%v source=%+v asset=%+v", err, sources[0], assets[0])
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
	if err := applySubtitlePreference(subtitles, "", "", "fr-FR"); err != nil {
		t.Fatal(err)
	}
	if !subtitles[1].Default || selectedSubtitle(subtitles) != "fr" {
		t.Fatalf("French subtitle was not selected: %+v", subtitles)
	}

	subtitles = []Subtitle{{ID: "en", Language: "en", Default: false}}
	if err := applySubtitlePreference(subtitles, "none", "", "en"); err != nil || selectedSubtitle(subtitles) != "" {
		t.Fatalf("subtitle off preference was not respected: %+v err=%v", subtitles, err)
	}

	if err := applySubtitlePreference(subtitles, "missing", "", ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid explicit subtitle error, got %v", err)
	}
}

func TestForcedSubtitlePreferenceMatchingFallbackAndExplicitPrecedence(t *testing.T) {
	subtitles := []Subtitle{
		{ID: "ordinary-fr", Language: "fr-FR"},
		{ID: "forced-en", Language: "en", Forced: true},
		{ID: "forced-fr", Language: "fr", Forced: true},
	}
	if err := applySubtitlePreference(subtitles, "", "fr-CA", "en"); err != nil {
		t.Fatal(err)
	}
	if selectedSubtitle(subtitles) != "forced-fr" {
		t.Fatalf("matching forced subtitle was not preferred: %+v", subtitles)
	}

	if err := applySubtitlePreference(subtitles, "", "de-DE", "en-US"); err != nil {
		t.Fatal(err)
	}
	if selectedSubtitle(subtitles) != "forced-en" {
		t.Fatalf("no-match did not preserve ordinary language fallback: %+v", subtitles)
	}

	ordinaryOnly := []Subtitle{{ID: "ordinary-fr", Language: "fr"}, {ID: "ordinary-en", Language: "en"}}
	if err := applySubtitlePreference(ordinaryOnly, "", "fr-FR", ""); err != nil {
		t.Fatal(err)
	}
	if selectedSubtitle(ordinaryOnly) != "" {
		t.Fatalf("ordinary same-language subtitle was treated as forced: %+v", ordinaryOnly)
	}

	if err := applySubtitlePreference(subtitles, "ordinary-fr", "en", "en"); err != nil {
		t.Fatal(err)
	}
	if selectedSubtitle(subtitles) != "ordinary-fr" {
		t.Fatalf("explicit subtitle did not override forced preference: %+v", subtitles)
	}
}

func TestEmbeddedAndTextSubtitlesBecomePlayableAssets(t *testing.T) {
	sources := []Source{{
		ID: "stream-1", AddonID: "addon", ManifestID: "manifest", Compatible: true,
		Media: &MediaInspection{SubtitleTracks: []MediaTrack{
			{Index: 4, Type: "subtitle", Codec: "ass", Language: "fr", Forced: true},
			{Index: 5, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Language: "en"},
		}},
	}}
	assets := []storedAsset{{ID: "stream-1", Kind: "stream", URL: "https://media.example/movie.mkv", Headers: map[string]string{"Authorization": "secret"}}}

	subtitles, subtitleAssets := embeddedSubtitles(sources, assets)
	if len(subtitles) != 1 || !subtitles[0].Forced || len(subtitleAssets) != 1 || subtitleAssets[0].SubtitleTrackIndex == nil || *subtitleAssets[0].SubtitleTrackIndex != 4 {
		t.Fatalf("embedded subtitle was not exposed: subtitles=%+v assets=%+v", subtitles, subtitleAssets)
	}
	if subtitleAssets[0].Kind != assetKindEmbeddedSubtitle || subtitleAssets[0].Headers["Authorization"] != "secret" {
		t.Fatalf("embedded subtitle source was not preserved: %+v", subtitleAssets[0])
	}
}

func TestBitmapSubtitleBurnRequiresAnnouncementAndPolicy(t *testing.T) {
	inspection := &MediaInspection{
		Container:      "mkv",
		VideoTracks:    []MediaTrack{{Index: 0, Type: "video", Codec: "h264", Height: 1080}},
		AudioTracks:    []MediaTrack{{Index: 1, Type: "audio", Codec: "aac", Channels: 2}},
		SubtitleTracks: []MediaTrack{{Index: 5, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Language: "en"}},
	}
	capabilities := Capabilities{
		StreamingProtocols: []string{"hls"}, Containers: []string{"mp4"},
		VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
		ProcessingModes: []string{processingTranscode}, SubtitleModes: []string{"burn"},
	}
	newState := func() ([]Source, []storedAsset, []Subtitle, []storedAsset) {
		sources := []Source{{
			ID: "stream-1", Mode: "direct", Protocol: "http", Container: "mkv", Compatible: true,
			Media: cloneMediaInspectionPointer(inspection), Decision: directDecision(*inspection),
		}}
		assets := []storedAsset{{ID: "stream-1", Kind: "stream", URL: "https://media.example/movie.mkv"}}
		subtitles, subtitleAssets := embeddedSubtitles(sources, assets, capabilities)
		if len(subtitles) != 1 || subtitles[0].Delivery != "burn" {
			t.Fatalf("burn-only client received non-burn subtitles: %+v", subtitles)
		}
		subtitles[0].Default = true
		return sources, assets, subtitles, subtitleAssets
	}
	sources, assets, subtitles, subtitleAssets := newState()
	if err := applySubtitleDecision(sources, assets, subtitles, subtitleAssets, capabilities, false); !errors.Is(err, ErrTranscodingDisabled) {
		t.Fatalf("disabled burn error=%v", err)
	}
	if assets[0].Kind != "stream" || assets[0].SubtitleTrackIndex != nil {
		t.Fatalf("disabled burn mutated stream asset: %+v", assets[0])
	}
	sources, assets, subtitles, subtitleAssets = newState()
	if err := applySubtitleDecision(sources, assets, subtitles, subtitleAssets, capabilities, true); err != nil {
		t.Fatalf("allowed burn failed: %v", err)
	}
	if sources[0].Mode != processingTranscode || assets[0].Kind != processingTranscode ||
		assets[0].SubtitleTrackIndex == nil || *assets[0].SubtitleTrackIndex != 5 ||
		sources[0].Decision == nil || sources[0].Decision.Reason != decisionSubtitleBurnRequired {
		t.Fatalf("burn decision was not persisted: source=%+v asset=%+v", sources[0], assets[0])
	}
}

func cloneMediaInspectionPointer(value *MediaInspection) *MediaInspection {
	cloned := cloneMediaInspection(*value)
	return &cloned
}
