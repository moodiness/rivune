package playback

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
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

type recordingResourceFetcher struct {
	fetchAddonID  string
	fetchPath     addon.ResourcePath
	fetchCalls    int
	fetchAllPath  addon.ResourcePath
	fetchAllCalls int
}

func (fetcher *recordingResourceFetcher) Fetch(_ context.Context, _ auth.Principal, addonID string, path addon.ResourcePath) (addon.ResourceResult, error) {
	fetcher.fetchAddonID = addonID
	fetcher.fetchPath = path
	fetcher.fetchCalls++
	return addon.ResourceResult{
		AddonID: addonID, ManifestID: "org.example.live",
		Payload: []byte(`{"streams":[{"name":"Live","url":"https://media.example/live.m3u8"}]}`),
	}, nil
}

func (fetcher *recordingResourceFetcher) FetchAll(_ context.Context, _ auth.Principal, path addon.ResourcePath) (addon.ResourceBatch, error) {
	fetcher.fetchAllPath = path
	fetcher.fetchAllCalls++
	return addon.ResourceBatch{Results: []addon.ResourceResult{{
		AddonID: "fanout-addon", ManifestID: "org.example.streams",
		Payload: []byte(`{"streams":[{"name":"Movie","url":"https://media.example/movie.mp4"}]}`),
	}}}, nil
}

func TestSourcesTargetsRequestedProfileAddon(t *testing.T) {
	profileID := "profile-id"
	current := time.Now()
	grantExpiresAt := current.Add(time.Hour)
	fetcher := &recordingResourceFetcher{}
	service := &Service{
		addons:     fetcher,
		now:        func() time.Time { return current },
		references: newSourceReferenceStore(time.Now),
	}
	list, err := service.Sources(context.Background(), auth.Principal{
		SessionID: "session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
	}, SourcesInput{
		MediaType: "tv", AddonID: "requested-addon", ResourceID: "channel-1",
		Capabilities: Capabilities{StreamingProtocols: []string{"hls"}, Containers: []string{"mpegts"}},
	})
	if err != nil {
		t.Fatalf("targeted sources: %v", err)
	}
	if fetcher.fetchCalls != 1 || fetcher.fetchAllCalls != 0 || fetcher.fetchAddonID != "requested-addon" {
		t.Fatalf("unexpected fetch calls: targeted=%d all=%d addon=%q", fetcher.fetchCalls, fetcher.fetchAllCalls, fetcher.fetchAddonID)
	}
	if fetcher.fetchPath.Resource != "stream" || fetcher.fetchPath.Type != "tv" || fetcher.fetchPath.ID != "channel-1" || len(fetcher.fetchPath.Extra) != 0 {
		t.Fatalf("unexpected targeted resource: %+v", fetcher.fetchPath)
	}
	if len(list.Sources) != 1 || list.Sources[0].AddonID != "requested-addon" || list.Sources[0].SourceRef == "" {
		t.Fatalf("unexpected targeted source list: %+v", list)
	}
}

func TestSourcesWithoutAddonKeepsFanout(t *testing.T) {
	for _, mediaType := range []string{"movie", "series"} {
		t.Run(mediaType, func(t *testing.T) {
			profileID := "profile-id"
			current := time.Now()
			grantExpiresAt := current.Add(time.Hour)
			fetcher := &recordingResourceFetcher{}
			service := &Service{
				addons:     fetcher,
				now:        func() time.Time { return current },
				references: newSourceReferenceStore(time.Now),
			}
			_, err := service.Sources(context.Background(), auth.Principal{
				SessionID: "session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
			}, SourcesInput{
				MediaType: mediaType, ResourceID: "resource-1",
				Capabilities: Capabilities{StreamingProtocols: []string{"http"}, Containers: []string{"mp4"}},
			})
			if err != nil {
				t.Fatalf("fan-out sources: %v", err)
			}
			if fetcher.fetchCalls != 0 || fetcher.fetchAllCalls != 1 {
				t.Fatalf("unexpected fetch calls: targeted=%d all=%d", fetcher.fetchCalls, fetcher.fetchAllCalls)
			}
			if fetcher.fetchAllPath.Resource != "stream" || fetcher.fetchAllPath.Type != mediaType || fetcher.fetchAllPath.ID != "resource-1" || len(fetcher.fetchAllPath.Extra) != 0 {
				t.Fatalf("unexpected fan-out resource: %+v", fetcher.fetchAllPath)
			}
		})
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
	info MediaInspection
	err  error
}

func (processor fakeMediaProcessor) Probe(context.Context, storedAsset) (MediaInspection, error) {
	return processor.info, processor.err
}

type sourceMediaProcessor map[string]MediaInspection

func (processor sourceMediaProcessor) Probe(_ context.Context, asset storedAsset) (MediaInspection, error) {
	return processor[asset.URL], nil
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
		StreamingProtocols: []string{"http", "hls"},
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
				StreamingProtocols: []string{"http", "hls"},
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
				if assets[0].Kind != processingRemux || sources[0].Protocol != "hls" || sources[0].Container != "hls" {
					t.Fatalf("remux decision was not persisted as HLS: source=%+v asset=%+v", sources[0], assets[0])
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
		StreamingProtocols: []string{"http", "hls"},
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

type commitTrackingResponseWriter struct {
	header http.Header
	status int
	writes int
}

func (writer *commitTrackingResponseWriter) Header() http.Header {
	if writer.header == nil {
		writer.header = make(http.Header)
	}
	return writer.header
}

func (writer *commitTrackingResponseWriter) WriteHeader(status int) {
	writer.status = status
}

func (writer *commitTrackingResponseWriter) Write(body []byte) (int, error) {
	writer.writes++
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	return len(body), nil
}

func TestProcessingAssetWithoutHLSFileFailsBeforeStartingFFmpeg(t *testing.T) {
	processor := &FFmpegProcessor{slots: make(chan struct{}, 1)}
	service := &Service{processor: processor}
	response := &commitTrackingResponseWriter{}
	request := httptest.NewRequest(http.MethodGet, "/asset?fallback=1&start=invalid", nil)

	err := service.proxyProcessingAsset(response, request, "session-id", "token", "", "", storedAsset{
		ID: "stream-1", Kind: processingTranscode, URL: "https://media.example/movie.mkv",
	})
	if !errors.Is(err, ErrClientCapabilityMissing) {
		t.Fatalf("missing HLS file returned %v", err)
	}
	if response.status != 0 || response.writes != 0 || len(processor.slots) != 0 {
		t.Fatalf("request started processing or committed a response: status=%d writes=%d active=%d", response.status, response.writes, len(processor.slots))
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

func TestPlaybackDecisionOrdersDirectPlayHLSDirectStreamAndTranscodes(t *testing.T) {
	capabilities := Capabilities{
		StreamingProtocols: []string{"http", "hls"}, Containers: []string{"mp4"},
		VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
		ProcessingModes: []string{processingRemux, processingTranscodeAudio, processingTranscode},
	}
	tests := []struct {
		name       string
		container  string
		videoCodec string
		audioCodec string
		want       string
	}{
		{name: "direct", container: "mp4", videoCodec: "h264", audioCodec: "aac", want: "direct"},
		{name: "HLS Direct Stream", container: "mkv", videoCodec: "h264", audioCodec: "aac", want: processingRemux},
		{name: "audio", container: "mkv", videoCodec: "h264", audioCodec: "dts", want: processingTranscodeAudio},
		{name: "full", container: "mkv", videoCodec: "vp9", audioCodec: "dts", want: processingTranscode},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mode, decision := playbackMode(Source{Mode: "direct", Protocol: "http", Container: test.container}, MediaInspection{
				Container:   test.container,
				VideoTracks: []MediaTrack{{Codec: test.videoCodec, Height: 1080}},
				AudioTracks: []MediaTrack{{Codec: test.audioCodec, Channels: 6}},
			}, capabilities)
			if mode != test.want || decision == nil {
				t.Fatalf("mode=%q decision=%+v, want %q", mode, decision, test.want)
			}
			if mode != "direct" && (decision.Target == nil || decision.Target.Protocol != "hls" || decision.Target.Container != "hls") {
				t.Fatalf("processed mode did not select HLS/fMP4: mode=%q decision=%+v", mode, decision)
			}
		})
	}
}

func TestPlaybackModeRejectsHTTPOnlyProcessingOutput(t *testing.T) {
	mode, decision := playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mkv"}, MediaInspection{
		Container:   "mkv",
		VideoTracks: []MediaTrack{{Codec: "vp9", Height: 1080}},
		AudioTracks: []MediaTrack{{Codec: "dts", Channels: 6}},
	}, Capabilities{
		StreamingProtocols: []string{"http"},
		Containers:         []string{"mp4"},
		VideoCodecs:        []string{"h264"},
		AudioCodecs:        []string{"aac"},
		ProcessingModes:    []string{processingRemux, processingTranscodeAudio, processingTranscode},
	})
	if mode != "" || decision != nil {
		t.Fatalf("HTTP-only client received a processing decision: mode=%q decision=%+v", mode, decision)
	}
}

func TestDecidePlaybackSourceDistinguishesPolicyAndCapabilityFailures(t *testing.T) {
	inspection := MediaInspection{
		Container:   "mkv",
		VideoTracks: []MediaTrack{{Codec: "vp9", Height: 1080}},
		AudioTracks: []MediaTrack{{Codec: "dts", Channels: 6}},
	}
	newInput := func() ([]Source, []storedAsset) {
		return []Source{{ID: "stream-1", Mode: "direct", URL: "https://media.example/movie.mkv", Protocol: "http", Container: "mkv"}},
			[]storedAsset{{ID: "stream-1", Kind: "stream", URL: "https://media.example/movie.mkv"}}
	}
	service := &Service{processor: fakeMediaProcessor{info: inspection}, probes: newMediaProbeCache(time.Now)}
	sources, assets := newInput()
	err := service.decidePlaybackSource(context.Background(), sources, assets, Capabilities{
		StreamingProtocols: []string{"http", "hls"}, Containers: []string{"mp4"},
		VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
		ProcessingModes: []string{processingTranscode},
	}, false)
	if !errors.Is(err, ErrTranscodingDisabled) || assets[0].Kind != "stream" {
		t.Fatalf("disabled policy started processing: err=%v asset=%+v", err, assets[0])
	}
	sources, assets = newInput()
	err = service.decidePlaybackSource(context.Background(), sources, assets, Capabilities{
		StreamingProtocols: []string{"http", "hls"}, Containers: []string{"mp4"},
		VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
	}, true)
	if !errors.Is(err, ErrClientCapabilityMissing) || assets[0].Kind != "stream" {
		t.Fatalf("missing mode was not reported conservatively: err=%v asset=%+v", err, assets[0])
	}
	sources, assets = newInput()
	err = service.decidePlaybackSource(context.Background(), sources, assets, Capabilities{
		StreamingProtocols: []string{"http"}, Containers: []string{"mp4"},
		VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
		ProcessingModes: []string{processingTranscode},
	}, true)
	if !errors.Is(err, ErrClientCapabilityMissing) || assets[0].Kind != "stream" {
		t.Fatalf("HTTP-only processing capability was not rejected before encoding: err=%v asset=%+v", err, assets[0])
	}
}

func TestFullTranscodeDecisionAppliesResolutionHDRAndBitrateLimits(t *testing.T) {
	mode, decision := playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mp4"}, MediaInspection{
		Container: "mp4", HDRFormat: "hdr10",
		VideoTracks: []MediaTrack{{Codec: "h264", Height: 2160, BitrateKbps: 24000}},
		AudioTracks: []MediaTrack{{Codec: "aac", Channels: 6}},
	}, Capabilities{
		StreamingProtocols: []string{"hls"}, Containers: []string{"mp4"},
		VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
		ProcessingModes: []string{processingTranscode},
		MaximumHeight:   1080, MaximumVideoBitrateKbps: 8000, MaximumAudioChannels: 2, TranscodeVideoBitrateKbps: 12000,
	})
	if mode != processingTranscode || decision == nil || !decision.ToneMapping || decision.Target == nil ||
		decision.Target.Height != 1080 || decision.Target.VideoBitrateKbps != 8000 {
		t.Fatalf("limits were not applied to full transcode: mode=%q decision=%+v", mode, decision)
	}
	mode, decision = playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mkv"}, MediaInspection{
		Container:   "mkv",
		VideoTracks: []MediaTrack{{Codec: "vp9", Height: 1080, BitrateKbps: 24000}},
		AudioTracks: []MediaTrack{{Codec: "aac", Channels: 2}},
	}, Capabilities{
		StreamingProtocols: []string{"hls"}, Containers: []string{"mp4"},
		VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
		ProcessingModes:           []string{processingTranscode},
		TranscodeVideoBitrateKbps: 12000,
	})
	if mode != processingTranscode || decision == nil || decision.Target == nil || decision.Target.VideoBitrateKbps != 12000 {
		t.Fatalf("server bitrate limit was not applied to full transcode: mode=%q decision=%+v", mode, decision)
	}
}
func TestPlaybackCapabilitiesDoNotTreatMissingFormatsAsWildcards(t *testing.T) {
	inspection := MediaInspection{
		Container:   "mp4",
		VideoTracks: []MediaTrack{{Codec: "h264", Height: 1080}},
		AudioTracks: []MediaTrack{{Codec: "aac", Channels: 2}},
	}
	if directMediaSupported(inspection, Capabilities{}) {
		t.Fatal("missing client capabilities were treated as support for every format")
	}
	if mediaProfileSupported("", &inspection.VideoTracks[0], &inspection.AudioTracks[0], Capabilities{
		Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
	}) {
		t.Fatal("an unknown container was accepted for direct playback")
	}
}

func TestValidateCapabilitiesAcceptsBoundedAdditiveProcessingLimits(t *testing.T) {
	valid := Capabilities{
		StreamingProtocols: []string{"http", "hls"}, Containers: []string{"mp4"},
		ProcessingModes:         []string{processingRemux, processingTranscodeAudio, processingTranscode},
		SubtitleModes:           []string{"external", "burn"},
		MaximumVideoBitrateKbps: 12000, MaximumAudioChannels: 6, MaximumHeight: 2160,
	}
	if err := validateCapabilities(valid); err != nil {
		t.Fatalf("valid capabilities rejected: %v", err)
	}
	valid.ProcessingModes = []string{processingRemux, processingRemux}
	if !errors.Is(validateCapabilities(valid), ErrInvalidInput) {
		t.Fatal("duplicate processing mode accepted")
	}
	valid.ProcessingModes = []string{processingTranscode}
	valid.MaximumVideoBitrateKbps = 200001
	if !errors.Is(validateCapabilities(valid), ErrInvalidInput) {
		t.Fatal("unbounded video bitrate accepted")
	}
}

func TestEffectivePlaybackMaximumHeightUsesStrictestLimit(t *testing.T) {
	tests := []struct {
		client   int
		settings int
		want     int
	}{
		{client: 2160, settings: 1080, want: 1080},
		{client: 720, settings: 1080, want: 720},
		{client: 720, settings: 0, want: 720},
		{client: 0, settings: 1080, want: 1080},
	}
	for _, test := range tests {
		if got := effectivePlaybackMaximumHeight(test.client, test.settings); got != test.want {
			t.Fatalf("effective height min(%d,%d)=%d, want %d", test.client, test.settings, got, test.want)
		}
	}
}

func TestTranscodeArgumentsApplyScaleBitrateChannelsAndBitmapBurnSafely(t *testing.T) {
	subtitleIndex := 7
	processor := &FFmpegProcessor{threads: 4, encoder: videoEncoder{kind: videoEncoderSoftware}}
	arguments, err := processor.processingArguments(storedAsset{
		Kind: processingTranscode, URL: "https://media.example/movie.mkv",
		SubtitleTrackIndex: &subtitleIndex, TargetHeight: 720, VideoBitrateKbps: 4500, MaximumAudioChannels: 1,
		Decision: &PlaybackDecision{Source: &PlaybackDecisionSource{Height: 2160}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	for _, expected := range []string{
		"-filter_complex [0:v:0][0:7]overlay,scale=-2:720[vout]",
		"-map [vout]", "-b:v 4500k", "-maxrate 4500k", "-bufsize 9000k", "-ac 1",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("arguments missing %q: %v", expected, arguments)
		}
	}
	if strings.Contains(joined, "sh -c") || strings.Contains(joined, "bash -c") {
		t.Fatalf("subtitle burn escaped the argument-array runner: %v", arguments)
	}
}

func TestRemuxArgumentsAlwaysCopyVideo(t *testing.T) {
	processor := &FFmpegProcessor{threads: 4}
	arguments, err := processor.processingArguments(storedAsset{Kind: processingRemux, URL: "https://media.example/movie.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	if !strings.Contains(joined, "-c:v copy") || strings.Contains(joined, "libx264") || strings.Contains(joined, "h264_") {
		t.Fatalf("remux unexpectedly re-encodes video: %v", arguments)
	}
}
