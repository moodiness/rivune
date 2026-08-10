package playback

import (
	"encoding/json"
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

func TestAudioTrackFullTranscodeUsesVideoHDRProfileEvidence(t *testing.T) {
	selected := 2
	for _, test := range []struct {
		name, hdr string
		video     MediaTrack
		wantTone  bool
	}{
		{name: "HDR10 profile support", hdr: "hdr10", video: MediaTrack{Index: 0, Type: "video", Codec: "h265", Height: 1080}},
		{name: "Dolby Vision compatible base", hdr: "dolby_vision", video: MediaTrack{Index: 0, Type: "video", Codec: "h265", Height: 1080, DolbyVisionBLPresent: true, DolbyVisionCompatibilityID: 1}, wantTone: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			inspection := &MediaInspection{
				Container: "mkv", HDRFormat: test.hdr, VideoTracks: []MediaTrack{test.video},
				AudioTracks: []MediaTrack{{Index: 1, Type: "audio", Codec: "aac", Channels: 2}, {Index: selected, Type: "audio", Codec: "dts", Channels: 6}},
			}
			sources := []Source{{ID: "stream-1", Mode: "direct", Protocol: "http", Container: "mkv", Compatible: true, Media: inspection}}
			assets := []storedAsset{{ID: "stream-1", Kind: "stream"}}
			err := applyPlaybackPreferences(sources, assets, ResolveInput{
				PreferredAudioTrack: &selected, AllowTranscoding: true,
				Capabilities: Capabilities{
					StreamingProtocols: []string{"hls"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
					ProcessingModes: []string{processingTranscode},
					MediaProfiles: []MediaProfile{
						{Container: "mp4", VideoCodec: "h265", AudioCodec: "aac", DirectPlay: true, SupportsNonDolbyVisionHDR: true},
						{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Transcoding: true},
					},
				},
			})
			if err != nil || assets[0].Kind != processingTranscode || assets[0].ToneMap != test.wantTone {
				t.Fatalf("audio selection err=%v asset=%+v want tone-map=%t", err, assets[0], test.wantTone)
			}
		})
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

func TestBurnOnlySubtitleCodecsRetainSafeTypeAndOrdinal(t *testing.T) {
	tracks := []MediaTrack{
		{Index: 3, Type: "subtitle", Codec: "ass"},
		{Index: 5, Type: "subtitle", Codec: "srt"},
		{Index: 7, Type: "subtitle", Codec: "hdmv_pgs_subtitle"},
		{Index: 9, Type: "subtitle", Codec: "dvb_subtitle"},
		{Index: 11, Type: "subtitle", Codec: "xsub"},
	}
	sources := []Source{{ID: "stream-1", Compatible: true, Media: &MediaInspection{SubtitleTracks: tracks}}}
	assets := []storedAsset{{ID: "stream-1", URL: "https://media.example/movie.mkv"}}
	subtitles, subtitleAssets := embeddedSubtitles(sources, assets, Capabilities{SubtitleModes: []string{"burn"}})
	if len(subtitles) != len(tracks) || len(subtitleAssets) != len(tracks) {
		t.Fatalf("burn-only tracks omitted: subtitles=%+v assets=%+v", subtitles, subtitleAssets)
	}
	for index, asset := range subtitleAssets {
		wantType := subtitleBurnBitmap
		if index < 2 {
			wantType = subtitleBurnText
		}
		if subtitles[index].Delivery != "burn" || asset.SubtitleTrackType != wantType || asset.SubtitleTrackOrdinal != index {
			t.Fatalf("track %d burn metadata = subtitle=%+v asset=%+v", index, subtitles[index], asset)
		}
	}
	encoded, err := json.Marshal(subtitleAssets)
	if err != nil {
		t.Fatal(err)
	}
	var restored []storedAsset
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored) != len(subtitleAssets) || restored[0].SubtitleTrackType != subtitleBurnText || restored[4].SubtitleTrackOrdinal != 4 {
		t.Fatalf("private session payload lost burn metadata: %+v", restored)
	}
}

func TestSubtitleBurnRejectsDolbyVisionWithoutCompatibleBase(t *testing.T) {
	capabilities := Capabilities{
		StreamingProtocols: []string{"hls"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
		ProcessingModes: []string{processingTranscode}, SubtitleModes: []string{"burn"},
		MediaProfiles: []MediaProfile{
			{Container: "mp4", VideoCodec: "h265", AudioCodec: "aac", DirectPlay: true, SupportsNonDolbyVisionHDR: true},
			{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Transcoding: true},
		},
	}
	newState := func(video MediaTrack, hdr string) ([]Source, []storedAsset, []Subtitle, []storedAsset) {
		inspection := &MediaInspection{
			Container: "mkv", HDRFormat: hdr, VideoTracks: []MediaTrack{video},
			AudioTracks:    []MediaTrack{{Index: 1, Type: "audio", Codec: "aac", Channels: 2}},
			SubtitleTracks: []MediaTrack{{Index: 2, Type: "subtitle", Codec: "ass"}},
		}
		sources := []Source{{ID: "stream-1", Mode: "direct", Compatible: true, Media: inspection, Decision: directDecision(*inspection)}}
		assets := []storedAsset{{ID: "stream-1", URL: "https://media.example/movie.mkv"}}
		subtitles, subtitleAssets := embeddedSubtitles(sources, assets, capabilities)
		subtitles[0].Default = true
		return sources, assets, subtitles, subtitleAssets
	}
	sources, assets, subtitles, subtitleAssets := newState(MediaTrack{Index: 0, Type: "video", Codec: "h265", Height: 1080}, "dolby_vision")
	if err := applySubtitleDecision(sources, assets, subtitles, subtitleAssets, capabilities, true); !errors.Is(err, ErrUnsupportedSource) {
		t.Fatalf("Dolby Vision profile 5/unknown base burn error = %v", err)
	}
	sources, assets, subtitles, subtitleAssets = newState(MediaTrack{
		Index: 0, Type: "video", Codec: "h265", Height: 1080,
		DolbyVisionBLPresent: true, DolbyVisionCompatibilityID: 1,
	}, "dolby_vision")
	if err := applySubtitleDecision(sources, assets, subtitles, subtitleAssets, capabilities, true); err != nil || !assets[0].ToneMap || !assets[0].DolbyVisionToneMapSafe {
		t.Fatalf("Dolby Vision compatible-base burn failed: err=%v asset=%+v", err, assets[0])
	}
	sources, assets, subtitles, subtitleAssets = newState(MediaTrack{Index: 0, Type: "video", Codec: "h265", Height: 1080}, "hdr10")
	if err := applySubtitleDecision(sources, assets, subtitles, subtitleAssets, capabilities, true); err != nil || assets[0].ToneMap {
		t.Fatalf("HDR10 profile-supported burn regressed: err=%v asset=%+v", err, assets[0])
	}
}
func cloneMediaInspectionPointer(value *MediaInspection) *MediaInspection {
	cloned := cloneMediaInspection(*value)
	return &cloned
}
