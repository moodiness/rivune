package playback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestFFmpegHLSOutputArgumentsMatchSegmentContainer(t *testing.T) {
	tsArguments := strings.Join(hlsOutputArguments(storedAsset{}, "flags"), " ")
	if !strings.Contains(tsArguments, "-hls_segment_type mpegts") || !strings.Contains(tsArguments, "segment-%06d.ts") || strings.Contains(tsArguments, "init.mp4") {
		t.Fatalf("default TS arguments = %s", tsArguments)
	}
	mp4Arguments := strings.Join(hlsOutputArguments(storedAsset{HLSSegmentContainer: "mp4"}, "flags"), " ")
	if !strings.Contains(mp4Arguments, "-hls_segment_type fmp4") || !strings.Contains(mp4Arguments, "-hls_fmp4_init_filename init.mp4") || !strings.Contains(mp4Arguments, "segment-%06d.m4s") {
		t.Fatalf("explicit fMP4 arguments = %s", mp4Arguments)
	}
}

func TestHLSMasterPlaylistPointsToOpaqueMediaCapability(t *testing.T) {
	processor := &containerFixtureHLSProcessor{seen: make(chan string, 1)}
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1024 * 1024, IdleTTL: time.Minute},
		hlsJobs:      make(map[string]*hlsJob), processor: processor,
	}
	asset := storedAsset{ID: "stream-1", Kind: processingTranscode, URL: "https://provider.example/private.mkv", HLSSegmentContainer: "mp4"}
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
	if response.Code != http.StatusOK || !strings.Contains(playlist, "#EXT-X-STREAM-INF:") || !strings.Contains(playlist, "RivuneChildId=opaque-media") {
		t.Fatalf("master status/body = %d/%q", response.Code, playlist)
	}
	for _, secret := range []string{"native-secret", "provider.example"} {
		if strings.Contains(playlist, secret) {
			t.Fatalf("master playlist exposed %q: %s", secret, playlist)
		}
	}
	service.stopHLSSession("session-1")
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
		"index.m3u8":         "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:3.000,\nsegment-000000.m4s\n#EXTINF:3.000,\nsegment-000001.m4s\n",
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

func TestPruneHLSSegmentsRetainsBoundedTail(t *testing.T) {
	directory := t.TempDir()
	for index := 0; index <= hlsRetainedSegments+5; index++ {
		name := fmt.Sprintf("segment-%06d.m4s", index)
		if err := os.WriteFile(filepath.Join(directory, name), []byte("segment"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	current := fmt.Sprintf("segment-%06d.m4s", hlsRetainedSegments+5)
	pruneHLSSegments(directory, current)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != hlsRetainedSegments {
		t.Fatalf("expected %d retained segments, got %d", hlsRetainedSegments, len(entries))
	}
	if _, err := os.Stat(filepath.Join(directory, "segment-000005.m4s")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old segment was retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, current)); err != nil {
		t.Fatalf("current segment was pruned: %v", err)
	}
}
