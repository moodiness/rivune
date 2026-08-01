package playback

import (
	"context"
	"io"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/addon"
)

func TestNormalizeStreamsRanksCompatibleSourcesAndKeepsHeadersPrivate(t *testing.T) {
	batch := addon.ResourceBatch{Results: []addon.ResourceResult{{
		AddonID: "addon-id", ManifestID: "manifest-id",
		Payload: []byte(`{"streams":[
			{"name":"Unsupported MKV","url":"https://media.example/movie.mkv"},
			{"name":"Playable HLS","url":"https://media.example/master.m3u8","behaviorHints":{"proxyHeaders":{"request":{"Authorization":"Bearer secret"}}}},
			{"name":"YouTube","ytId":"video_123"}
		]}`),
	}}}

	sources, assets := normalizeStreams(batch, Capabilities{
		StreamingProtocols: []string{"hls", "youtube"},
		Containers:         []string{"mp4", "webm"},
	})

	if len(sources) != 3 || sources[0].Name != "Playable HLS" || !sources[0].Compatible || sources[1].Mode != "youtube" || sources[2].Compatible {
		t.Fatalf("unexpected normalized sources: %+v", sources)
	}
	var hlsAsset *storedAsset
	for index := range assets {
		if assets[index].ID == sources[0].ID {
			hlsAsset = &assets[index]
		}
	}
	if hlsAsset == nil || hlsAsset.Headers["Authorization"] != "Bearer secret" {
		t.Fatalf("expected private playback header in stored asset: %+v", assets)
	}
	if strings.Contains(sources[0].URL, "secret") {
		t.Fatalf("source response leaked a private header: %+v", sources[0])
	}
}

func TestNormalizeStreamsTreatsProxiedWebReadyContainerAsCompatible(t *testing.T) {
	batch := addon.ResourceBatch{Results: []addon.ResourceResult{{
		AddonID: "addon-id", ManifestID: "manifest-id",
		Payload: []byte(`{"streams":[{
			"name":"Proxied MP4",
			"url":"https://media.example/movie.mp4",
			"behaviorHints":{"notWebReady":true}
		}]}`),
	}}}

	sources, _ := normalizeStreams(batch, Capabilities{
		StreamingProtocols: []string{"http"},
		Containers:         []string{"mp4"},
	})

	if len(sources) != 1 || !sources[0].Compatible {
		t.Fatalf("expected server-proxied MP4 to be compatible: %+v", sources)
	}
}

func TestReplacementContentTypeUsesMediaExtensionForOctetStream(t *testing.T) {
	if contentType := replacementContentType("application/octet-stream", "https://media.example/movie.mp4"); contentType != "video/mp4" {
		t.Fatalf("expected video/mp4 replacement, got %q", contentType)
	}
	if contentType := replacementContentType("video/custom", "https://media.example/movie.mp4"); contentType != "" {
		t.Fatalf("expected explicit upstream content type to remain unchanged, got %q", contentType)
	}
}

func TestRewritePlaylistSignsEveryResolvedAsset(t *testing.T) {
	base, err := url.Parse("https://media.example/path/master.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	playlist := []byte("#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\nsegment-1.ts\n")
	rewritten, err := rewritePlaylist(playlist, base, func(target string) string {
		return target + "?signed=yes"
	})
	if err != nil {
		t.Fatal(err)
	}
	result := string(rewritten)
	if !strings.Contains(result, `URI="https://media.example/path/key.bin?signed=yes"`) || !strings.Contains(result, "https://media.example/path/segment-1.ts?signed=yes") {
		t.Fatalf("playlist references were not rewritten: %s", result)
	}
}

func TestTargetSignatureRejectsTampering(t *testing.T) {
	token := "opaque-playback-token"
	target := "https://media.example/segment.ts"
	signature := signTarget(token, target)
	if !validTargetSignature(token, target, signature) {
		t.Fatal("valid playback target signature was rejected")
	}
	if validTargetSignature(token, "http://127.0.0.1/private", signature) {
		t.Fatal("tampered playback target signature was accepted")
	}
}

type fakeMediaProcessor struct {
	info      MediaInspection
	err       error
	output    string
	processed *storedAsset
}

func (processor fakeMediaProcessor) Probe(context.Context, storedAsset) (MediaInspection, error) {
	return processor.info, processor.err
}

func (processor fakeMediaProcessor) Process(_ context.Context, asset storedAsset, destination io.Writer) error {
	if processor.err != nil {
		return processor.err
	}
	if processor.processed != nil {
		*processor.processed = asset
	}
	_, err := io.WriteString(destination, processor.output)
	return err
}

type sourceMediaProcessor map[string]MediaInspection

func (processor sourceMediaProcessor) Probe(_ context.Context, asset storedAsset) (MediaInspection, error) {
	return processor[asset.URL], nil
}

func (sourceMediaProcessor) Process(context.Context, storedAsset, io.Writer) error {
	return nil
}

func TestDecidePlaybackSourceProbesCompatibleHLSDuration(t *testing.T) {
	sources := []Source{{
		ID: "stream-1", Mode: "direct", URL: "https://media.example/movie.m3u8",
		Protocol: "hls", Container: "hls", Compatible: true,
	}}
	assets := []storedAsset{{ID: "stream-1", Kind: "stream", URL: sources[0].URL}}
	service := &Service{
		processor: fakeMediaProcessor{info: MediaInspection{
			Container:       "hls",
			DurationSeconds: 7_200,
			VideoTracks:     []MediaTrack{{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080}},
			AudioTracks:     []MediaTrack{{Index: 1, Type: "audio", Codec: "aac", Channels: 2}},
		}},
		probes: newMediaProbeCache(time.Now),
	}

	service.decidePlaybackSource(context.Background(), sources, assets, Capabilities{
		StreamingProtocols: []string{"hls"},
	})

	if sources[0].Media == nil || sources[0].Media.DurationSeconds != 7_200 {
		t.Fatalf("compatible HLS source duration was not inspected: %+v", sources[0])
	}
}

func TestDecidePlaybackSourceRemuxesHLSWithPlayableAlternateAudio(t *testing.T) {
	sources := []Source{{
		ID: "stream-1", Mode: "direct", URL: "https://media.example/movie.m3u8",
		Protocol: "hls", Container: "hls", Compatible: true,
	}}
	assets := []storedAsset{{ID: "stream-1", Kind: "stream", URL: sources[0].URL}}
	service := &Service{
		processor: fakeMediaProcessor{info: MediaInspection{
			Container:   "hls",
			VideoTracks: []MediaTrack{{Index: 0, Type: "video", Codec: "h264"}},
			AudioTracks: []MediaTrack{
				{Index: 1, Type: "audio", Codec: "dts"},
				{Index: 2, Type: "audio", Codec: "aac"},
			},
		}},
		probes: newMediaProbeCache(time.Now),
	}
	service.decidePlaybackSource(context.Background(), sources, assets, Capabilities{
		StreamingProtocols: []string{"hls"},
		Containers:         []string{"mp4"},
		VideoCodecs:        []string{"h264"},
		AudioCodecs:        []string{"aac"},
		ProcessingModes:    []string{processingRemux},
		MediaProfiles:      []MediaProfile{{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac"}},
	})
	if !sources[0].Compatible || sources[0].Mode != processingRemux || sources[0].Protocol != "hls" || assets[0].Kind != processingRemux {
		t.Fatalf("HLS alternate audio was not remuxed losslessly: source=%+v asset=%+v", sources[0], assets[0])
	}
}

func TestDecidePlaybackSourceSkipsMislabeledLowResolution(t *testing.T) {
	sources := []Source{
		{ID: "stream-1", Name: "Claimed 2160p", Hint: "2160p h264 aac", Mode: "direct", URL: "https://media.example/low.mkv", Protocol: "http", Container: "mkv"},
		{ID: "stream-2", Name: "Actual 2160p", Hint: "2160p h264 aac", Mode: "direct", URL: "https://media.example/uhd.mkv", Protocol: "http", Container: "mkv"},
		{ID: "stream-3", Name: "Direct SD", Mode: "direct", URL: "https://media.example/low.mp4", Protocol: "http", Container: "mp4", Compatible: true},
	}
	assets := []storedAsset{
		{ID: "stream-1", Kind: "stream", URL: sources[0].URL},
		{ID: "stream-2", Kind: "stream", URL: sources[1].URL},
		{ID: "stream-3", Kind: "stream", URL: sources[2].URL},
	}
	inspection := func(width, height int) MediaInspection {
		return MediaInspection{
			Container:      "matroska",
			VideoTracks:    []MediaTrack{{Index: 0, Type: "video", Codec: "h264", Width: width, Height: height}},
			AudioTracks:    []MediaTrack{{Index: 1, Type: "audio", Codec: "aac", Channels: 2}},
			SubtitleTracks: []MediaTrack{},
		}
	}
	service := &Service{
		processor: sourceMediaProcessor{
			sources[0].URL: inspection(1280, 720),
			sources[1].URL: inspection(3840, 2160),
			sources[2].URL: inspection(720, 480),
		},
		probes: newMediaProbeCache(time.Now),
	}
	capabilities := Capabilities{
		StreamingProtocols: []string{"http"},
		Containers:         []string{"mp4"},
		VideoCodecs:        []string{"h264"},
		AudioCodecs:        []string{"aac"},
		ProcessingModes:    []string{processingRemux},
	}

	service.decidePlaybackSource(context.Background(), sources, assets, capabilities)

	if sources[0].Compatible || !sources[1].Compatible || sources[2].Compatible {
		t.Fatalf("unexpected compatible sources: %+v", sources)
	}
	if sources[1].Mode != processingRemux || sources[1].Media == nil || sources[1].Media.VideoTracks[0].Height != 2160 {
		t.Fatalf("expected the verified 2160p source, got source=%+v asset=%+v", sources[1], assets[1])
	}
}

func TestDecidePlaybackSourceHonorsMaximumHeight(t *testing.T) {
	sources := []Source{
		{ID: "stream-1", Name: "HLS quality unknown", Mode: "direct", URL: "https://media.example/full-hd.m3u8", Protocol: "hls", Container: "hls", Compatible: true},
		{ID: "stream-2", Name: "720p", Hint: "720p h264 aac", Mode: "direct", URL: "https://media.example/hd.mp4", Protocol: "http", Container: "mp4", Compatible: true},
	}
	assets := []storedAsset{
		{ID: "stream-1", Kind: "stream", URL: sources[0].URL},
		{ID: "stream-2", Kind: "stream", URL: sources[1].URL},
	}
	service := &Service{
		processor: sourceMediaProcessor{
			sources[0].URL: {
				Container:   "hls",
				VideoTracks: []MediaTrack{{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080}},
				AudioTracks: []MediaTrack{{Index: 1, Type: "audio", Codec: "aac", Channels: 2}},
			},
			sources[1].URL: {
				Container:   "mp4",
				VideoTracks: []MediaTrack{{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720}},
				AudioTracks: []MediaTrack{{Index: 1, Type: "audio", Codec: "aac", Channels: 2}},
			},
		},
		probes: newMediaProbeCache(time.Now),
	}

	service.decidePlaybackSource(context.Background(), sources, assets, Capabilities{
		StreamingProtocols: []string{"http", "hls"},
		Containers:         []string{"mp4"},
		VideoCodecs:        []string{"h264"},
		AudioCodecs:        []string{"aac"},
		MaximumHeight:      720,
	})

	if sources[0].Compatible || !sources[1].Compatible || sources[1].Media == nil || sources[1].Media.VideoTracks[0].Height != 720 {
		t.Fatalf("maximum height was not enforced: %+v", sources)
	}
}

func TestDecidePlaybackSourceAllowsOnlyDirectOrAdvertisedRemux(t *testing.T) {
	tests := []struct {
		name            string
		container       string
		video           string
		audio           string
		processingModes []string
		wantMode        string
		wantCompatible  bool
	}{
		{name: "direct source stays untouched", container: "mp4", video: "h264", audio: "aac", processingModes: []string{processingRemux}, wantMode: "direct", wantCompatible: true},
		{name: "container incompatibility remuxes", container: "mkv", video: "h264", audio: "aac", processingModes: []string{processingRemux}, wantMode: processingRemux, wantCompatible: true},
		{name: "remux must be advertised", container: "mkv", video: "h264", audio: "aac"},
		{name: "unsupported audio does not encode", container: "mkv", video: "h264", audio: "dts", processingModes: []string{processingRemux}},
		{name: "unsupported video does not encode", container: "mkv", video: "vp9", audio: "aac", processingModes: []string{processingRemux}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sources := []Source{{ID: "stream-1", Mode: "direct", URL: "https://media.example/movie.mkv", Protocol: "http", Container: test.container}}
			assets := []storedAsset{{ID: "stream-1", Kind: "stream", URL: sources[0].URL}}
			info := MediaInspection{
				Container:      test.container,
				VideoTracks:    []MediaTrack{{Index: 0, Type: "video", Codec: test.video, Width: 3840, Height: 2160}},
				AudioTracks:    []MediaTrack{{Index: 1, Type: "audio", Codec: test.audio, Channels: 6}},
				SubtitleTracks: []MediaTrack{},
			}
			service := &Service{processor: fakeMediaProcessor{info: info}, probes: newMediaProbeCache(time.Now)}
			legacyPreferDirect := false
			capabilities := Capabilities{
				StreamingProtocols: []string{"http"},
				Containers:         []string{"mp4"},
				VideoCodecs:        []string{"h264"},
				AudioCodecs:        []string{"aac"},
				ProcessingModes:    test.processingModes,
				PreferDirectPlay:   &legacyPreferDirect,
			}

			service.decidePlaybackSource(context.Background(), sources, assets, capabilities)

			if sources[0].Compatible != test.wantCompatible || sources[0].Media == nil {
				t.Fatalf("unexpected decision: source=%+v asset=%+v", sources[0], assets[0])
			}
			if test.wantCompatible && sources[0].Mode != test.wantMode {
				t.Fatalf("expected mode %q, got source=%+v asset=%+v", test.wantMode, sources[0], assets[0])
			}
			if test.wantMode == processingRemux {
				if assets[0].Kind != processingRemux || sources[0].Container != "mp4" {
					t.Fatalf("remux decision was not persisted: source=%+v asset=%+v", sources[0], assets[0])
				}
			} else if assets[0].Kind != "stream" {
				t.Fatalf("source unexpectedly requested media encoding: source=%+v asset=%+v", sources[0], assets[0])
			}
		})
	}
}

func TestPlaybackModeRemuxesWithPlayableAlternateAudio(t *testing.T) {
	mode, _ := playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mkv"}, MediaInspection{
		Container:   "mkv",
		VideoTracks: []MediaTrack{{Index: 0, Type: "video", Codec: "h264"}},
		AudioTracks: []MediaTrack{
			{Index: 1, Type: "audio", Codec: "dts"},
			{Index: 2, Type: "audio", Codec: "aac"},
		},
	}, Capabilities{
		StreamingProtocols: []string{"http"},
		Containers:         []string{"mp4"},
		VideoCodecs:        []string{"h264"},
		AudioCodecs:        []string{"aac"},
		ProcessingModes:    []string{processingRemux},
		MediaProfiles:      []MediaProfile{{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac"}},
	})
	if mode != processingRemux {
		t.Fatalf("playable alternate audio did not keep the source eligible for remux: %q", mode)
	}
}

func TestMediaProfilesDoNotCrossContainerCodecPairs(t *testing.T) {
	capabilities := Capabilities{
		StreamingProtocols: []string{"http"},
		Containers:         []string{"mp4", "webm"},
		VideoCodecs:        []string{"h264", "vp9"},
		AudioCodecs:        []string{"aac", "opus"},
		ProcessingModes:    []string{processingRemux},
		MediaProfiles: []MediaProfile{
			{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac"},
			{Container: "webm", VideoCodec: "vp9", AudioCodec: "opus"},
		},
	}
	directMode, _ := playbackMode(Source{Mode: "direct", Protocol: "http", Container: "webm"}, MediaInspection{
		VideoTracks: []MediaTrack{{Codec: "vp9"}},
		AudioTracks: []MediaTrack{{Codec: "opus"}},
	}, capabilities)
	crossedMode, _ := playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mp4"}, MediaInspection{
		VideoTracks: []MediaTrack{{Codec: "h264"}},
		AudioTracks: []MediaTrack{{Codec: "opus"}},
	}, capabilities)
	if directMode != "direct" || crossedMode != "" {
		t.Fatalf("container/codec profiles were cross-paired: direct=%q crossed=%q", directMode, crossedMode)
	}
}

func TestSessionSourceURLPreservesAuthorizedExternalHandoff(t *testing.T) {
	const externalURL = "https://external.example/watch?token=opaque"
	if got := sessionSourceURL(Source{ID: "external-1", Mode: "external", URL: externalURL}, nil, "session-id", "session-token"); got != externalURL {
		t.Fatalf("external player URL was rewritten through the media proxy: %q", got)
	}
	if got := sessionSourceURL(Source{ID: "direct-1", Mode: "direct", URL: "https://media.example/movie.mp4"}, nil, "session-id", "session-token"); got == "https://media.example/movie.mp4" || !strings.HasPrefix(got, "/api/v1/playback/sessions/") {
		t.Fatalf("web media URL was not protected by the session proxy: %q", got)
	}
}

func TestProxyProcessedMediaStreamsMP4WithoutCaching(t *testing.T) {
	var processed storedAsset
	service := &Service{processor: fakeMediaProcessor{output: "fragmented-mp4", processed: &processed}}
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/asset", nil)

	if err := service.proxyProcessedMedia(response, request, storedAsset{Kind: "remux", URL: "https://media.example/movie.mkv"}); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || response.Header().Get("Content-Type") != "video/mp4" || response.Header().Get("Cache-Control") != "no-store" || response.Body.String() != "fragmented-mp4" {
		t.Fatalf("unexpected processed response: status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	if processed.Kind != processingRemux {
		t.Fatalf("progressive fallback escalated remux into media encoding: %+v", processed)
	}
}

func TestProcessedMediaStartAcceptsBoundedWholeSeconds(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want float64
	}{
		{raw: "", want: 0},
		{raw: "0", want: 0},
		{raw: "300", want: 300},
		{raw: "604800", want: 604800},
	} {
		got, err := processedMediaStart(test.raw)
		if err != nil || got != test.want {
			t.Fatalf("processedMediaStart(%q) = %v, %v; want %v", test.raw, got, err, test.want)
		}
	}
	for _, raw := range []string{"-1", "1.5", "604801", "not-a-number"} {
		if _, err := processedMediaStart(raw); err == nil {
			t.Fatalf("processedMediaStart(%q) accepted an invalid offset", raw)
		}
	}
}

func TestProcessingArgumentsSeekBeforeOpeningInput(t *testing.T) {
	processor := &FFmpegProcessor{threads: 4}
	arguments, err := processor.processingArguments(storedAsset{
		Kind: processingTranscode, URL: "https://media.example/movie.mkv", StartSeconds: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	if !strings.Contains(joined, "-analyzeduration 1000000 -probesize 1000000") {
		t.Fatalf("bounded media analysis settings are missing: %v", arguments)
	}
	if !strings.Contains(joined, "-user_agent Rivune-Playback/1 -ss 300 -i https://media.example/movie.mkv") {
		t.Fatalf("input offset was not applied before the remote input: %v", arguments)
	}
	if !strings.Contains(joined, "-preset superfast -tune zerolatency") {
		t.Fatalf("low-latency video settings are missing: %v", arguments)
	}
}

func TestProgressiveArgumentsFlushOneSecondFragments(t *testing.T) {
	processor := &FFmpegProcessor{threads: 4}
	arguments, err := processor.progressiveArguments(storedAsset{
		Kind: processingTranscode, URL: "https://media.example/movie.mkv",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	if !strings.Contains(joined, "-force_key_frames expr:gte(t,n_forced*1)") {
		t.Fatalf("progressive transcode does not force frequent keyframes: %v", arguments)
	}
	if !strings.Contains(joined, "-frag_duration 1000000 -flush_packets 1 -f mp4 pipe:1") {
		t.Fatalf("progressive output does not flush one-second fragments: %v", arguments)
	}

	arguments, err = processor.progressiveArguments(storedAsset{
		Kind: processingRemux, URL: "https://media.example/movie.mkv",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(arguments, " "), "-force_key_frames") {
		t.Fatalf("remux unexpectedly attempts to create keyframes: %v", arguments)
	}
}

func TestFFmpegHeadersRejectsInjectedLines(t *testing.T) {
	headers := ffmpegHeaders(map[string]string{
		"Authorization": "Bearer safe",
		"X-Unsafe":      "value\r\nInjected: true",
		"Bad:Name":      "value",
	})
	if headers != "Authorization: Bearer safe\r\n" {
		t.Fatalf("unexpected sanitized FFmpeg headers %q", headers)
	}
}

func TestFFmpegInputArgumentsOnlyApplyHTTPOptionsToHTTPMedia(t *testing.T) {
	if arguments := ffmpegInputArguments(storedAsset{URL: "file:///tmp/movie.mp4"}); len(arguments) != 0 {
		t.Fatalf("file input received HTTP-only arguments: %v", arguments)
	}
	arguments := ffmpegInputArguments(storedAsset{URL: "https://media.example/movie.mp4"})
	if len(arguments) != 2 || arguments[0] != "-user_agent" || arguments[1] != "Rivune-Playback/1" {
		t.Fatalf("HTTP input did not receive its user agent: %v", arguments)
	}
}

func TestInspectedContainerUsesSourceHintForMatroskaFamily(t *testing.T) {
	if got := inspectedContainer("matroska,webm", "mkv"); got != "mkv" {
		t.Fatalf("expected MKV hint to win, got %q", got)
	}
	if got := inspectedContainer("matroska,webm", "webm"); got != "webm" {
		t.Fatalf("expected WebM hint to win, got %q", got)
	}
}
