package playback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRewriteLocalPlaylistRewritesInitAndSegments(t *testing.T) {
	playlist := []byte("#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:4.000,\nsegment-000001.m4s\n")

	rewritten, err := rewriteLocalPlaylist(playlist, func(reference string) string {
		return "/media/" + reference
	})
	if err != nil {
		t.Fatal(err)
	}
	result := string(rewritten)
	if !strings.Contains(result, `URI="/media/init.mp4"`) || !strings.Contains(result, "/media/segment-000001.m4s") {
		t.Fatalf("playlist references were not rewritten: %s", result)
	}
}

func TestRewriteLocalPlaylistClassifiesOnlyMediaSegmentsAsExpiring(t *testing.T) {
	playlist := []byte("#EXTM3U\n" +
		"#EXT-X-MAP:URI=\"init.mp4\"\n" +
		"#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n" +
		"#EXTINF:4.000,\n" +
		"#EXT-X-BYTERANGE:1024@0\n" +
		"segment-000001.m4s\n" +
		"#EXT-X-PART:DURATION=0.5,URI=\"part-000002.m4s\"\n" +
		"#EXT-X-PRELOAD-HINT:URI=\"part-000003.m4s\",TYPE=PART\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=1000000\n" +
		"variant.m3u8\n")
	got := make(map[string]bool)
	_, err := rewriteLocalPlaylistWithReferencePolicy(playlist, true, func(reference string, mediaSegment bool) string {
		got[reference] = mediaSegment
		return "/media/" + reference
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"init.mp4": false, "key.bin": false, "segment-000001.m4s": true,
		"part-000002.m4s": true, "part-000003.m4s": true, "variant.m3u8": false,
	}
	if !maps.Equal(got, want) {
		t.Fatalf("media segment classification = %#v, want %#v", got, want)
	}
}

type containerFixtureHLSProcessor struct {
	seen chan string
}

func (processor *containerFixtureHLSProcessor) ProcessHLS(_ context.Context, asset storedAsset, directory string) error {
	container := normalizedHLSSegmentContainer(asset.HLSSegmentContainer)
	processor.seen <- container
	if container == "mp4" {
		files := map[string][]byte{
			"init.mp4":           []byte("fragmented-mp4-init"),
			"segment-000000.m4s": []byte("fragmented-mp4-segment"),
			"index.m3u8":         []byte("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:6.000,\nsegment-000000.m4s\n#EXT-X-ENDLIST\n"),
		}
		for name, contents := range files {
			if err := os.WriteFile(filepath.Join(directory, name), contents, 0o600); err != nil {
				return err
			}
		}
		return nil
	}
	segment := make([]byte, 188)
	segment[0] = 0x47
	if err := os.WriteFile(filepath.Join(directory, "segment-000000.ts"), segment, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:6.000,\nsegment-000000.ts\n#EXT-X-ENDLIST\n"), 0o600)
}

func (*containerFixtureHLSProcessor) ConvertSubtitle(context.Context, storedAsset, io.Writer) error {
	return nil
}

func (*containerFixtureHLSProcessor) Probe(context.Context, storedAsset) (MediaInspection, error) {
	return MediaInspection{}, nil
}

func TestHLSContainerCapabilityMatchesPlaylistSegmentsAndMIME(t *testing.T) {
	tests := []struct {
		name, capability, container, segment, contentType, playlistContains, playlistExcludes string
	}{
		{name: "default transport stream", container: "ts", segment: "segment-000000.ts", contentType: "video/mp2t", playlistContains: ".ts", playlistExcludes: "#EXT-X-MAP"},
		{name: "explicit transport stream", capability: "ts", container: "ts", segment: "segment-000000.ts", contentType: "video/mp2t", playlistContains: ".ts", playlistExcludes: "#EXT-X-MAP"},
		{name: "explicit fragmented mp4", capability: "mp4", container: "mp4", segment: "segment-000000.m4s", contentType: "video/mp4", playlistContains: `#EXT-X-MAP:URI=`, playlistExcludes: ".ts"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			processor := &containerFixtureHLSProcessor{seen: make(chan string, 1)}
			service := &Service{
				mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1024 * 1024, IdleTTL: time.Minute},
				hlsJobs:      make(map[string]*hlsJob), processor: processor,
			}
			asset := storedAsset{ID: "stream-1", Kind: processingRemux, URL: "https://media.example/movie.mkv", HLSSegmentContainer: test.capability}
			playlistResponse := httptest.NewRecorder()
			playlistRequest := httptest.NewRequest(http.MethodGet, "/asset?file=index.m3u8", nil)
			if err := service.serveHLS(playlistResponse, playlistRequest, "session-1", "opaque-token", asset, nil); err != nil {
				t.Fatal(err)
			}
			playlist := playlistResponse.Body.String()
			if playlistResponse.Code != http.StatusOK || !strings.Contains(playlist, test.playlistContains) || strings.Contains(playlist, test.playlistExcludes) {
				t.Fatalf("playlist status/body = %d/%q", playlistResponse.Code, playlist)
			}
			if got := <-processor.seen; got != test.container {
				t.Fatalf("processor container = %q, want %q", got, test.container)
			}
			job := service.hlsJobs[hlsJobKey("session-1", asset)]
			if job == nil || job.segmentContainer != test.container {
				t.Fatalf("job container = %#v, want %q", job, test.container)
			}

			segmentResponse := httptest.NewRecorder()
			segmentRequest := httptest.NewRequest(http.MethodGet, "/asset?file="+test.segment, nil)
			if err := service.serveHLS(segmentResponse, segmentRequest, "session-1", "opaque-token", asset, nil); err != nil {
				t.Fatal(err)
			}
			if got := segmentResponse.Header().Get("Content-Type"); got != test.contentType {
				t.Fatalf("segment Content-Type = %q, want %q", got, test.contentType)
			}
			if test.container == "ts" && (segmentResponse.Body.Len() != 188 || segmentResponse.Body.Bytes()[0] != 0x47) {
				t.Fatalf("transport-stream bytes = %d/%x", segmentResponse.Body.Len(), segmentResponse.Body.Bytes())
			}
			service.stopHLSSession("session-1")
		})
	}
}

func TestFFmpegHLSOutputArgumentsUseBoundedSlidingPlaylist(t *testing.T) {
	arguments := strings.Join(hlsOutputArguments(storedAsset{}, "flags"), " ")
	for _, expected := range []string{
		"-hls_list_size 120",
		"-hls_delete_threshold 1",
		"-hls_flags flags+delete_segments",
		"-hls_segment_type mpegts",
		"segment-%06d.ts",
	} {
		if !strings.Contains(arguments, expected) {
			t.Fatalf("HLS arguments omit %q: %s", expected, arguments)
		}
	}
	if strings.Contains(arguments, "-hls_playlist_type") || strings.Contains(arguments, "init.mp4") {
		t.Fatalf("default TS arguments retain incompatible EVENT/fMP4 options: %s", arguments)
	}
	if maximumPlaylistReferences != 10_000 || hlsRetainedSegments+hlsDeleteThreshold > maximumPlaylistReferences {
		t.Fatalf("HLS window/reference limits = %d+%d/%d", hlsRetainedSegments, hlsDeleteThreshold, maximumPlaylistReferences)
	}

	mp4Arguments := strings.Join(hlsOutputArguments(storedAsset{HLSSegmentContainer: "mp4"}, "flags"), " ")
	if !strings.Contains(mp4Arguments, "-hls_segment_type fmp4") || !strings.Contains(mp4Arguments, "-hls_fmp4_init_filename init.mp4") || !strings.Contains(mp4Arguments, "segment-%06d.m4s") {
		t.Fatalf("explicit fMP4 arguments = %s", mp4Arguments)
	}
}

func TestHLSMasterPlaylistPointsToOpaqueMediaCapability(t *testing.T) {
	tests := []struct {
		name       string
		target     *PlaybackDecisionTarget
		wantCodecs string
	}{
		{name: "known target codecs", target: &PlaybackDecisionTarget{VideoCodec: "h264", AudioCodec: "aac"}, wantCodecs: "avc1,mp4a"},
		{name: "unknown target codec", target: &PlaybackDecisionTarget{VideoCodec: "unknown-video", AudioCodec: "aac"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			processor := &containerFixtureHLSProcessor{seen: make(chan string, 1)}
			service := &Service{
				mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1024 * 1024, IdleTTL: time.Minute},
				hlsJobs:      make(map[string]*hlsJob), processor: processor,
			}
			asset := storedAsset{
				ID: "stream-1", Kind: processingTranscode, URL: "https://provider.example/private.mkv", HLSSegmentContainer: "mp4",
				VideoBitrateKbps: 4500, Decision: &PlaybackDecision{Target: test.target},
			}
			t.Cleanup(func() { service.stopHLSSession("session-1") })
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/Videos/item/master.m3u8?file=master.m3u8", nil)
			buildChildURL := func(state deliveryChildState) (string, error) {
				if state.file != "index.m3u8" || state.assetID != asset.ID {
					t.Fatalf("master child state = %+v", state)
				}
				return "/Videos/item/master.m3u8?RivuneChildId=opaque-media", nil
			}
			if err := service.serveHLS(response, request, "session-1", "native-secret", asset, buildChildURL); err != nil {
				t.Fatal(err)
			}
			playlist := response.Body.String()
			streamInformation := "#EXT-X-STREAM-INF:BANDWIDTH=4756000,AVERAGE-BANDWIDTH=4756000"
			if test.wantCodecs != "" {
				streamInformation += `,CODECS="` + test.wantCodecs + `"`
			}
			if response.Code != http.StatusOK || !strings.Contains(playlist, streamInformation+"\n") || !strings.Contains(playlist, "RivuneChildId=opaque-media") {
				t.Fatalf("master status/body = %d/%q", response.Code, playlist)
			}
			if test.wantCodecs == "" && strings.Contains(playlist, "CODECS=") {
				t.Fatalf("master advertised unknown target codec: %s", playlist)
			}
			for _, forbidden := range []string{"native-secret", "provider.example", "RESOLUTION=", "FRAME-RATE=", "AUDIO=", "#EXT-X-INDEPENDENT-SEGMENTS"} {
				if strings.Contains(playlist, forbidden) {
					t.Fatalf("master playlist exposed or invented %q: %s", forbidden, playlist)
				}
			}
		})
	}
}

func TestHLSPlaylistEncodedSecondsSumsValidDurations(t *testing.T) {
	directory := t.TempDir()
	playlist := "#EXTM3U\n#EXTINF:4.25,First\nsegment-000000.m4s\n#EXTINF:5.75,\nsegment-000001.m4s\n"
	if err := os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte(playlist), 0o600); err != nil {
		t.Fatal(err)
	}

	seconds, ok := hlsPlaylistEncodedSeconds(directory)
	if !ok || seconds != 10 {
		t.Fatalf("encoded duration = %v, %t, want 10, true", seconds, ok)
	}
}

func TestHLSPlaylistEncodedSecondsIgnoresMalformedAndNonFiniteDurations(t *testing.T) {
	directory := t.TempDir()
	playlist := "#EXTM3U\n#EXTINF:invalid,\n#EXTINF:NaN,\n#EXTINF:+Inf,\n#EXTINF:-4,\n#EXTINF:0,\n#EXTINF:9\n#EXTINF:2.5,\nsegment-000000.m4s\n"
	if err := os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte(playlist), 0o600); err != nil {
		t.Fatal(err)
	}

	seconds, ok := hlsPlaylistEncodedSeconds(directory)
	if !ok || seconds != 2.5 {
		t.Fatalf("encoded duration = %v, %t, want 2.5, true", seconds, ok)
	}
}

func TestHLSPlaylistEncodedSecondsToleratesMissingAndPartialPlaylists(t *testing.T) {
	directory := t.TempDir()
	if seconds, ok := hlsPlaylistEncodedSeconds(directory); ok || seconds != 0 {
		t.Fatalf("missing playlist duration = %v, %t, want 0, false", seconds, ok)
	}
	if err := os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte("#EXTM3U\n#EXTINF:"), 0o600); err != nil {
		t.Fatal(err)
	}
	if seconds, ok := hlsPlaylistEncodedSeconds(directory); !ok || seconds != 0 {
		t.Fatalf("partial playlist duration = %v, %t, want 0, true", seconds, ok)
	}
}

func TestWaitForHLSBufferAcceptsCompletedShortMedia(t *testing.T) {
	directory := t.TempDir()
	playlist := "#EXTM3U\n#EXTINF:2.5,\nsegment-000000.m4s\n#EXT-X-ENDLIST\n"
	if err := os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte(playlist), 0o600); err != nil {
		t.Fatal(err)
	}
	job := &hlsJob{directory: directory, done: make(chan struct{})}
	close(job.done)
	if err := waitForHLSBuffer(context.Background(), job, hlsInitialBufferSeconds); err != nil {
		t.Fatalf("completed short media buffer error = %v", err)
	}
}

type blockingHLSProcessor struct {
	ready   chan struct{}
	stopped chan struct{}
}

func (processor *blockingHLSProcessor) ProcessHLS(ctx context.Context, _ storedAsset, directory string) error {
	defer close(processor.stopped)
	files := map[string]string{
		"init.mp4":           "fragmented-mp4-init",
		"segment-000000.m4s": "fragmented-mp4-segment-0",
		"segment-000001.m4s": "fragmented-mp4-segment-1",
		"segment-000002.m4s": "fragmented-mp4-segment-2",
		"segment-000003.m4s": "fragmented-mp4-segment-3",
		"index.m3u8":         "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:3.000,\nsegment-000000.m4s\n#EXTINF:3.000,\nsegment-000001.m4s\n#EXTINF:3.000,\nsegment-000002.m4s\n#EXTINF:3.000,\nsegment-000003.m4s\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
			return err
		}
	}
	close(processor.ready)
	<-ctx.Done()
	return ctx.Err()
}

func (*blockingHLSProcessor) ConvertSubtitle(context.Context, storedAsset, io.Writer) error {
	return nil
}

func (*blockingHLSProcessor) Probe(context.Context, storedAsset) (MediaInspection, error) {
	return MediaInspection{}, nil
}

type failingHLSProcessor struct {
	err error
}

func (processor *failingHLSProcessor) ProcessHLS(context.Context, storedAsset, string) error {
	return processor.err
}

func (*failingHLSProcessor) ConvertSubtitle(context.Context, storedAsset, io.Writer) error {
	return nil
}

func (*failingHLSProcessor) Probe(context.Context, storedAsset) (MediaInspection, error) {
	return MediaInspection{}, nil
}

type excessiveSubtitleProcessor struct{}

func (*excessiveSubtitleProcessor) ProcessHLS(context.Context, storedAsset, string) error {
	return nil
}

func (*excessiveSubtitleProcessor) Probe(context.Context, storedAsset) (MediaInspection, error) {
	return MediaInspection{}, nil
}

func (*excessiveSubtitleProcessor) ConvertSubtitle(_ context.Context, _ storedAsset, destination io.Writer) error {
	chunk := bytes.Repeat([]byte("x"), 64<<10)
	for total := 0; total <= maximumConvertedSubtitleBytes; total += len(chunk) {
		if _, err := destination.Write(chunk); err != nil {
			return nil
		}
	}
	return nil
}

func TestProxyConvertedSubtitleEnforcesOutputLimit(t *testing.T) {
	service := &Service{processor: &excessiveSubtitleProcessor{}}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/subtitle.vtt", nil)
	err := service.proxyConvertedSubtitle(response, request, storedAsset{})
	if !errors.Is(err, ErrMediaProcessingFailed) || !strings.Contains(err.Error(), "converted subtitle exceeds") {
		t.Fatalf("excessive converted subtitle error = %v", err)
	}
	if response.Body.Len() != 0 {
		t.Fatalf("excessive converted subtitle wrote %d response bytes", response.Body.Len())
	}
}

func TestStartSessionHLSWaitsForInitialPlaylistFailure(t *testing.T) {
	processErr := errors.New("processing slot unavailable")
	processor := &failingHLSProcessor{err: processErr}
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1024 * 1024, IdleTTL: time.Minute},
		hlsJobs:      make(map[string]*hlsJob),
		processor:    processor,
	}
	asset := storedAsset{ID: "stream-1", Kind: processingTranscode, URL: "https://media.example/movie.mkv"}
	source := Source{ID: asset.ID, Compatible: true, Protocol: "hls", Mode: processingTranscode}

	err := service.startSessionHLS(context.Background(), "", "session-1", []Source{source}, []storedAsset{asset})
	if !errors.Is(err, processErr) {
		t.Fatalf("start session error = %v, want %v", err, processErr)
	}
	if len(service.hlsJobs) != 0 {
		t.Fatalf("failed session retained HLS jobs: %+v", service.hlsJobs)
	}
}

func TestHLSJobServesBeforeCompletionAndStopsWithSession(t *testing.T) {
	processor := &blockingHLSProcessor{ready: make(chan struct{}), stopped: make(chan struct{})}
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1024 * 1024, IdleTTL: time.Minute},
		hlsJobs:      make(map[string]*hlsJob),
	}
	asset := storedAsset{
		ID: "stream-1", Kind: processingTranscode, URL: "https://media.example/movie.mkv",
		DurationSeconds: 3600, StartSeconds: 120,
	}

	job, err := service.hlsJob("session-1", asset, processor, true)
	if err != nil {
		t.Fatal(err)
	}
	if job.sourceDurationSeconds != asset.DurationSeconds || job.startOffsetSeconds != asset.StartSeconds {
		t.Fatalf(
			"job duration/start = %v/%v, want %v/%v",
			job.sourceDurationSeconds, job.startOffsetSeconds, asset.DurationSeconds, asset.StartSeconds,
		)
	}
	select {
	case <-processor.ready:
	case <-time.After(time.Second):
		t.Fatal("processor did not publish its initial HLS files")
	}
	select {
	case <-job.done:
		t.Fatal("HLS processing completed before the initial playlist was served")
	default:
	}
	if err := waitForMediaFile(context.Background(), job, filepath.Join(job.directory, "index.m3u8")); err != nil {
		t.Fatalf("initial playlist was unavailable: %v", err)
	}
	if err := waitForHLSBuffer(context.Background(), job, hlsInitialBufferSeconds); err != nil {
		t.Fatalf("initial playback buffer was unavailable: %v", err)
	}
	if err := waitForMediaFile(context.Background(), job, filepath.Join(job.directory, "segment-000000.m4s")); err != nil {
		t.Fatalf("initial segment was unavailable: %v", err)
	}

	service.stopHLSSession("session-1")
	select {
	case <-processor.stopped:
	default:
		t.Fatal("stopping the playback session did not cancel HLS processing")
	}
	if _, err := os.Stat(job.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("HLS workspace still exists after session stop: %v", err)
	}
}

func TestHLSJobRejectsExhaustedStorage(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "existing.bin"), []byte("full"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: directory, MaxStorageBytes: 1, IdleTTL: time.Minute},
		hlsJobs:      make(map[string]*hlsJob),
	}
	processor := &blockingHLSProcessor{ready: make(chan struct{}), stopped: make(chan struct{})}

	_, err := service.hlsJob("session-1", storedAsset{ID: "stream-1"}, processor, true)
	if !errors.Is(err, ErrMediaStorageLimit) {
		t.Fatalf("expected storage limit error, got %v", err)
	}
}

func TestPlaybackServiceStartupClearsSaturatedWorkspace(t *testing.T) {
	root := t.TempDir()
	staleDirectory := filepath.Join(root, "rivune-media", "orphaned-session")
	if err := os.MkdirAll(staleDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staleDirectory, "segment.m4s"), []byte("stale media"), 0o600); err != nil {
		t.Fatal(err)
	}
	processor := &blockingHLSProcessor{ready: make(chan struct{}), stopped: make(chan struct{})}
	service, err := NewService(nil, nil, processor, MediaOptions{TempDirectory: root, MaxStorageBytes: 1, IdleTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if size := directorySize(service.mediaOptions.TempDirectory); size != 0 {
		t.Fatalf("startup retained %d bytes of orphaned media", size)
	}
	if _, err := service.hlsJob("session-1", storedAsset{ID: "stream-1"}, processor, true); err != nil {
		t.Fatalf("start media job after startup cleanup: %v", err)
	}
	select {
	case <-processor.ready:
	case <-time.After(time.Second):
		t.Fatal("media job did not start after startup cleanup")
	}
	service.stopHLSSession("session-1")
}

func TestHLSStorageCapacityRecoversAfterSessionStop(t *testing.T) {
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1, IdleTTL: time.Minute},
		hlsJobs:      make(map[string]*hlsJob),
	}
	first := &blockingHLSProcessor{ready: make(chan struct{}), stopped: make(chan struct{})}
	if _, err := service.hlsJob("session-1", storedAsset{ID: "stream-1"}, first, true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.ready:
	case <-time.After(time.Second):
		t.Fatal("first media job did not fill the workspace")
	}
	if _, err := service.hlsJob("session-2", storedAsset{ID: "stream-2"}, first, true); !errors.Is(err, ErrMediaStorageLimit) {
		t.Fatalf("expected saturated workspace, got %v", err)
	}

	service.stopHLSSession("session-1")
	if size := directorySize(service.mediaOptions.TempDirectory); size != 0 {
		t.Fatalf("session cleanup retained %d bytes", size)
	}

	second := &blockingHLSProcessor{ready: make(chan struct{}), stopped: make(chan struct{})}
	if _, err := service.hlsJob("session-2", storedAsset{ID: "stream-2"}, second, true); err != nil {
		t.Fatalf("start media job after reclaiming storage: %v", err)
	}
	select {
	case <-second.ready:
	case <-time.After(time.Second):
		t.Fatal("media job did not start after reclaiming storage")
	}
	service.stopHLSSession("session-2")
}

func TestFFmpegProcessorRejectsConcurrentWorkAtCapacity(t *testing.T) {
	processor := &FFmpegProcessor{slots: make(chan struct{}, 1)}
	if err := processor.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer processor.release()
	if err := processor.acquire(context.Background()); !errors.Is(err, ErrMediaCapacityReached) {
		t.Fatalf("expected capacity error, got %v", err)
	}
}
func TestPrewarmReplacesPreviousAssetForDevice(t *testing.T) {
	first := &blockingHLSProcessor{ready: make(chan struct{}), stopped: make(chan struct{})}
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1024 * 1024, IdleTTL: time.Minute},
		hlsJobs:      make(map[string]*hlsJob),
		processor:    first,
	}
	prewarmID := prewarmHLSSession("auth-session", "profile")
	firstAsset := storedAsset{ID: "stream-1", Kind: processingTranscode, URL: "https://media.example/first.mkv"}
	if _, err := service.hlsJob(prewarmID, firstAsset, first, true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.ready:
	case <-time.After(time.Second):
		t.Fatal("first prewarm did not start")
	}

	second := &blockingHLSProcessor{ready: make(chan struct{}), stopped: make(chan struct{})}
	service.processor = second
	secondAsset := storedAsset{ID: "stream-2", Kind: processingTranscodeAudio, URL: "https://media.example/second.mkv"}
	if err := service.prewarmHLS(context.Background(), prewarmID, Source{Protocol: "hls", Mode: processingTranscodeAudio}, &secondAsset); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.stopped:
	default:
		t.Fatal("replaced prewarm continued consuming a processing slot")
	}
	if len(service.hlsJobs) != 1 || service.hlsJobs[hlsJobKey(prewarmID, secondAsset)] == nil {
		t.Fatalf("unexpected prewarm jobs after replacement: %+v", service.hlsJobs)
	}
	service.stopHLSSession(prewarmID)
}

func TestFFmpegConvertsSRTAndASSToWebVTT(t *testing.T) {
	processor, err := NewFFmpegProcessor("ffmpeg", "ffprobe", 1, 1, FFmpegOptions{HardwareAcceleration: "software"})
	if err != nil {
		t.Skipf("FFmpeg is unavailable: %v", err)
	}
	tests := []struct {
		filename string
		text     string
	}{
		{filename: "sample.srt", text: "Bonjour Rivune"},
		{filename: "sample.ass", text: "Bonjour ASS"},
	}
	for _, test := range tests {
		t.Run(test.filename, func(t *testing.T) {
			source, err := filepath.Abs(filepath.Join("testdata", test.filename))
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := processor.ConvertSubtitle(context.Background(), storedAsset{Kind: assetKindConvertedSubtitle, URL: source}, &output); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(output.String(), "WEBVTT") || !strings.Contains(output.String(), test.text) {
				t.Fatalf("unexpected WebVTT output: %s", output.String())
			}
		})
	}
}

type rotatingSlidingHLSProcessor struct {
	first   int
	rotate  chan struct{}
	updated chan struct{}
}

func (processor *rotatingSlidingHLSProcessor) ProcessHLS(ctx context.Context, _ storedAsset, directory string) error {
	if err := os.WriteFile(filepath.Join(directory, "init.mp4"), []byte("init"), 0o600); err != nil {
		return err
	}
	for index := processor.first; index <= processor.first+hlsRetainedSegments; index++ {
		name := fmt.Sprintf("segment-%06d.m4s", index)
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0o600); err != nil {
			return err
		}
	}
	if err := writeSlidingPlaylist(directory, processor.first); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-processor.rotate:
	}
	if err := writeSlidingPlaylist(directory, processor.first+1); err != nil {
		return err
	}
	close(processor.updated)
	<-ctx.Done()
	return ctx.Err()
}

func (*rotatingSlidingHLSProcessor) ConvertSubtitle(context.Context, storedAsset, io.Writer) error {
	return nil
}

func (*rotatingSlidingHLSProcessor) Probe(context.Context, storedAsset) (MediaInspection, error) {
	return MediaInspection{}, nil
}

func writeSlidingPlaylist(directory string, first int) error {
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXT-X-MEDIA-SEQUENCE:")
	playlist.WriteString(strconv.Itoa(first))
	playlist.WriteByte('\n')
	for index := first; index < first+hlsRetainedSegments; index++ {
		playlist.WriteString("#EXTINF:3.000,\n")
		playlist.WriteString(fmt.Sprintf("segment-%06d.m4s\n", index))
	}
	return os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte(playlist.String()), 0o600)
}

func servedLocalHLSReferences(t *testing.T, playlist string) []string {
	t.Helper()
	references := make([]string, 0, hlsRetainedSegments+1)
	for _, line := range strings.Split(playlist, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			for _, match := range playlistURIAttribute.FindAllStringSubmatch(line, -1) {
				references = append(references, servedLocalHLSFile(t, match[1]))
			}
			continue
		}
		references = append(references, servedLocalHLSFile(t, line))
	}
	return references
}

func servedLocalHLSFile(t *testing.T, reference string) string {
	t.Helper()
	parsed, err := url.Parse(reference)
	if err != nil {
		t.Fatalf("parse served HLS reference %q: %v", reference, err)
	}
	file := parsed.Query().Get("file")
	if !localMediaName.MatchString(file) {
		t.Fatalf("served HLS reference has invalid local file: %q", reference)
	}
	return file
}

func assertHLSFilesExist(t *testing.T, directory string, references []string) {
	t.Helper()
	for _, reference := range references {
		if _, err := os.Stat(filepath.Join(directory, reference)); err != nil {
			t.Fatalf("served playlist reference %q is unavailable: %v", reference, err)
		}
	}
}

func TestSlidingHLSPlaylistKeepsServedReferencesAcrossRotationReconnectAndSeek(t *testing.T) {
	first := maximumPlaylistReferences - hlsRetainedSegments
	processor := &rotatingSlidingHLSProcessor{
		first: first, rotate: make(chan struct{}), updated: make(chan struct{}),
	}
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1024 * 1024, IdleTTL: time.Minute},
		hlsJobs:      make(map[string]*hlsJob), processor: processor,
	}
	asset := storedAsset{ID: "stream-1", Kind: processingTranscode, URL: "https://media.example/movie.mkv", HLSSegmentContainer: "mp4"}
	t.Cleanup(func() { service.stopHLSSession("session-1") })

	childStates := make(map[string]deliveryChildState, hlsRetainedSegments+1)
	buildChildURL := func(state deliveryChildState) (string, error) {
		childStates[state.file] = state
		return hlsAssetURLAt("session-1", state.assetID, "opaque-token", state.file, asset.StartSeconds), nil
	}
	initialResponse := httptest.NewRecorder()
	initialRequest := httptest.NewRequest(http.MethodGet, "/asset?file=index.m3u8", nil)
	if err := service.serveHLS(initialResponse, initialRequest, "session-1", "opaque-token", asset, buildChildURL); err != nil {
		t.Fatal(err)
	}
	initialReferences := servedLocalHLSReferences(t, initialResponse.Body.String())
	if len(initialReferences) != hlsRetainedSegments+1 {
		t.Fatalf("initial playlist references = %d, want %d", len(initialReferences), hlsRetainedSegments+1)
	}
	initialSegment := fmt.Sprintf("segment-%06d.m4s", first)
	if !childStates["init.mp4"].retainWhileActive || childStates[initialSegment].retainWhileActive {
		t.Fatalf("sliding init/segment retention = %t/%t, want true/false", childStates["init.mp4"].retainWhileActive, childStates[initialSegment].retainWhileActive)
	}
	job := service.hlsJobs[hlsJobKey("session-1", asset)]
	if job == nil {
		t.Fatal("initial HLS job is missing")
	}
	assertHLSFilesExist(t, job.directory, initialReferences)

	close(processor.rotate)
	select {
	case <-processor.updated:
	case <-time.After(time.Second):
		t.Fatal("HLS playlist did not rotate")
	}
	latestName := fmt.Sprintf("segment-%06d.m4s", first+hlsRetainedSegments)
	latestResponse := httptest.NewRecorder()
	latestRequest := httptest.NewRequest(http.MethodGet, "/asset?file="+latestName, nil)
	if err := service.serveHLS(latestResponse, latestRequest, "session-1", "opaque-token", asset, nil); err != nil {
		t.Fatal(err)
	}
	if latestResponse.Code != http.StatusOK || latestResponse.Body.String() != latestName {
		t.Fatalf("latest segment response = %d/%q", latestResponse.Code, latestResponse.Body.String())
	}
	assertHLSFilesExist(t, job.directory, initialReferences)

	reconnectResponse := httptest.NewRecorder()
	if err := service.serveHLS(reconnectResponse, initialRequest, "session-1", "opaque-token", asset, nil); err != nil {
		t.Fatal(err)
	}
	reconnectReferences := servedLocalHLSReferences(t, reconnectResponse.Body.String())
	oldestName := fmt.Sprintf("segment-%06d.m4s", first)
	if len(reconnectReferences) != hlsRetainedSegments+1 || !slices.Contains(reconnectReferences, latestName) || slices.Contains(reconnectReferences, oldestName) {
		t.Fatalf("reconnect references = %d, latest %q present=%t, pruned %q present=%t", len(reconnectReferences), latestName, slices.Contains(reconnectReferences, latestName), oldestName, slices.Contains(reconnectReferences, oldestName))
	}
	assertHLSFilesExist(t, job.directory, reconnectReferences)
	entries, err := os.ReadDir(job.directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > hlsRetainedSegments+hlsDeleteThreshold+2 {
		t.Fatalf("rotated HLS storage contains %d files, want at most %d", len(entries), hlsRetainedSegments+hlsDeleteThreshold+2)
	}
	segments := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "segment-") && strings.HasSuffix(entry.Name(), ".m4s") {
			segments++
		}
	}
	if segments != hlsRetainedSegments+hlsDeleteThreshold {
		t.Fatalf("rotated HLS storage contains %d segments, want %d", segments, hlsRetainedSegments+hlsDeleteThreshold)
	}

	seekProcessor := &rotatingSlidingHLSProcessor{
		first: 0, rotate: make(chan struct{}), updated: make(chan struct{}),
	}
	service.processor = seekProcessor
	seekAsset := asset
	seekAsset.StartSeconds = 900
	seekResponse := httptest.NewRecorder()
	seekRequest := httptest.NewRequest(http.MethodGet, "/asset?file=index.m3u8&start=900", nil)
	if err := service.serveHLS(seekResponse, seekRequest, "session-1", "opaque-token", seekAsset, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(job.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("seek retained previous HLS generation: %v", err)
	}
	seekJob := service.hlsJobs[hlsJobKey("session-1", seekAsset)]
	if seekJob == nil {
		t.Fatal("seek HLS job is missing")
	}
	seekReferences := servedLocalHLSReferences(t, seekResponse.Body.String())
	assertHLSFilesExist(t, seekJob.directory, seekReferences)
	if !strings.Contains(seekResponse.Body.String(), "start=900") {
		t.Fatalf("seek playlist omitted generation start: %s", seekResponse.Body.String())
	}
}
