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
	"sync"
	"sync/atomic"
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

func TestRewriteLocalPlaylistPreservesExistingStartDirective(t *testing.T) {
	playlist := []byte("#EXTM3U\n#EXT-X-START:TIME-OFFSET=31.000000,PRECISE=YES\n#EXTINF:3.000000,\nseek-000010.ts\n")
	rewritten, err := rewriteLocalPlaylist(playlist, func(reference string) string { return "/media/" + reference })
	if err != nil {
		t.Fatal(err)
	}
	result := string(rewritten)
	if strings.Count(result, "#EXT-X-START:") != 1 || !strings.Contains(result, "TIME-OFFSET=31.000000") || strings.Contains(result, "TIME-OFFSET=0,") {
		t.Fatalf("start directive was duplicated or replaced: %s", result)
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

type seekableFixtureHLSProcessor struct {
	starts chan float64
}

func (processor *seekableFixtureHLSProcessor) ProcessHLS(_ context.Context, asset storedAsset, directory string) error {
	processor.starts <- asset.StartSeconds
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:3\n#EXT-X-MEDIA-SEQUENCE:0\n")
	for index := range 4 {
		name := fmt.Sprintf("segment-%06d.ts", index)
		contents := []byte(fmt.Sprintf("start=%.0f,index=%d", asset.StartSeconds, index))
		if err := os.WriteFile(filepath.Join(directory, name), contents, 0o600); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(&playlist, "#EXTINF:3.000000,\n%s\n", name)
	}
	playlist.WriteString("#EXT-X-ENDLIST\n")
	return os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte(playlist.String()), 0o600)
}

func (*seekableFixtureHLSProcessor) ConvertSubtitle(context.Context, storedAsset, io.Writer) error {
	return nil
}

func (*seekableFixtureHLSProcessor) Probe(context.Context, storedAsset) (MediaInspection, error) {
	return MediaInspection{}, nil
}

type blockingSeekableFixtureHLSProcessor struct {
	starts   chan float64
	releases map[int]chan struct{}
}

func (processor *blockingSeekableFixtureHLSProcessor) ProcessHLS(ctx context.Context, asset storedAsset, directory string) error {
	processor.starts <- asset.StartSeconds
	release := processor.releases[int(asset.StartSeconds)]
	if release == nil {
		return fmt.Errorf("unexpected HLS generation start %.0f", asset.StartSeconds)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-release:
	}
	contents := []byte(fmt.Sprintf("start=%.0f,index=0", asset.StartSeconds))
	if err := os.WriteFile(filepath.Join(directory, "segment-000000.ts"), contents, 0o600); err != nil {
		return err
	}
	playlist := "#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:9.000000,\nsegment-000000.ts\n#EXT-X-ENDLIST\n"
	return os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte(playlist), 0o600)
}

func (*blockingSeekableFixtureHLSProcessor) ConvertSubtitle(context.Context, storedAsset, io.Writer) error {
	return nil
}

func (*blockingSeekableFixtureHLSProcessor) Probe(context.Context, storedAsset) (MediaInspection, error) {
	return MediaInspection{}, nil
}

type blockingHTTPResponseWriter struct {
	header       http.Header
	status       int
	body         bytes.Buffer
	writeStarted chan struct{}
	writeRelease chan struct{}
}

func newBlockingHTTPResponseWriter() *blockingHTTPResponseWriter {
	return &blockingHTTPResponseWriter{
		header:       make(http.Header),
		writeStarted: make(chan struct{}, 1),
		writeRelease: make(chan struct{}, 1),
	}
}

func (writer *blockingHTTPResponseWriter) Header() http.Header {
	return writer.header
}

func (writer *blockingHTTPResponseWriter) WriteHeader(status int) {
	writer.status = status
}

func (writer *blockingHTTPResponseWriter) Write(contents []byte) (int, error) {
	select {
	case writer.writeStarted <- struct{}{}:
	default:
	}
	<-writer.writeRelease
	return writer.body.Write(contents)
}

func TestTranscodedHLSVODPlaylistRestartsGenerationAtRequestedSeek(t *testing.T) {
	processor := &seekableFixtureHLSProcessor{starts: make(chan float64, 4)}
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1024 * 1024, IdleTTL: time.Minute},
		hlsJobs:      make(map[string]*hlsJob), processor: processor,
	}
	asset := storedAsset{
		ID: "stream-1", Kind: processingTranscode, URL: "https://media.example/movie.mkv",
		HLSSegmentContainer: "ts", DurationSeconds: 120, StartSeconds: 31,
	}
	if got := hlsGenerationStart(asset, 31); got != 30 {
		t.Fatalf("aligned generation start=%v, want 30", got)
	}
	states := make(map[string]deliveryChildState)
	buildChildURL := func(state deliveryChildState) (string, error) {
		states[state.file] = state
		return "/asset?file=" + url.QueryEscape(state.file) + "&start=" + url.QueryEscape(state.start), nil
	}
	playlistResponse := httptest.NewRecorder()
	playlistRequest := httptest.NewRequest(http.MethodGet, "/asset?file=index.m3u8", nil)
	if err := service.serveHLS(playlistResponse, playlistRequest, "session-1", "opaque-token", asset, buildChildURL); err != nil {
		t.Fatal(err)
	}
	playlist := playlistResponse.Body.String()
	references := servedLocalHLSReferences(t, playlist)
	if playlistResponse.Code != http.StatusOK || !strings.Contains(playlist, "#EXT-X-PLAYLIST-TYPE:VOD") || !strings.Contains(playlist, "#EXT-X-START:TIME-OFFSET=31.000000,PRECISE=YES") || !strings.Contains(playlist, "#EXT-X-ENDLIST") || len(references) != 40 {
		t.Fatalf("seekable playlist status=%d references=%d body=%q", playlistResponse.Code, len(references), playlist)
	}
	if strings.Count(playlist, "#EXT-X-START:") != 1 || strings.Contains(playlist, "#EXT-X-START:TIME-OFFSET=0,") {
		t.Fatalf("seekable playlist has ambiguous start directives: %q", playlist)
	}
	seekState := states["seek-000020.ts"]
	if seekState.start != "60" || !seekState.retainWhileActive {
		t.Fatalf("seek child state=%+v", seekState)
	}

	seekAsset := asset
	seekAsset.StartSeconds = 60
	seekResponse := httptest.NewRecorder()
	seekRequest := httptest.NewRequest(http.MethodGet, "/asset?file=seek-000020.ts&start=60", nil)
	if err := service.serveHLS(seekResponse, seekRequest, "session-1", "opaque-token", seekAsset, nil); err != nil {
		t.Fatal(err)
	}
	if seekResponse.Code != http.StatusOK || seekResponse.Header().Get("Cache-Control") != "private, no-store" || seekResponse.Body.String() != "start=60,index=0" {
		t.Fatalf("seek segment response=%d cache=%q body=%q", seekResponse.Code, seekResponse.Header().Get("Cache-Control"), seekResponse.Body.String())
	}

	nextAsset := asset
	nextAsset.StartSeconds = 63
	nextResponse := httptest.NewRecorder()
	nextRequest := httptest.NewRequest(http.MethodGet, "/asset?file=seek-000021.ts&start=63", nil)
	if err := service.serveHLS(nextResponse, nextRequest, "session-1", "opaque-token", nextAsset, nil); err != nil {
		t.Fatal(err)
	}
	if nextResponse.Code != http.StatusOK || nextResponse.Header().Get("Cache-Control") != "private, no-store" || nextResponse.Body.String() != "start=60,index=1" {
		t.Fatalf("sequential segment response=%d cache=%q body=%q", nextResponse.Code, nextResponse.Header().Get("Cache-Control"), nextResponse.Body.String())
	}

	service.hlsMu.Lock()
	seekJob := service.hlsJobs[hlsJobKey("session-1", seekAsset)]
	service.hlsMu.Unlock()
	if seekJob == nil {
		t.Fatal("seek HLS job is missing")
	}
	evictedPlaylist := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-MEDIA-SEQUENCE:1\n#EXTINF:3.000000,\nsegment-000001.ts\n#EXT-X-ENDLIST\n"
	if err := os.WriteFile(filepath.Join(seekJob.directory, "index.m3u8"), []byte(evictedPlaylist), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(seekJob.directory, "segment-000000.ts")); err != nil {
		t.Fatal(err)
	}
	retryResponse := httptest.NewRecorder()
	retryRequest := httptest.NewRequest(http.MethodGet, "/asset?file=seek-000020.ts&start=60", nil)
	if err := service.serveHLS(retryResponse, retryRequest, "session-1", "opaque-token", seekAsset, nil); err != nil {
		t.Fatal(err)
	}
	if retryResponse.Code != http.StatusOK || retryResponse.Header().Get("Cache-Control") != "private, no-store" || retryResponse.Body.String() != "start=60,index=0" {
		t.Fatalf("expired same-start response=%d cache=%q body=%q", retryResponse.Code, retryResponse.Header().Get("Cache-Control"), retryResponse.Body.String())
	}

	starts := []float64{<-processor.starts, <-processor.starts, <-processor.starts}
	if !slices.Equal(starts, []float64{30, 60, 60}) {
		t.Fatalf("HLS generation starts=%v, want [30 60 60]", starts)
	}
	select {
	case unexpected := <-processor.starts:
		t.Fatalf("sequential segment restarted HLS at %v", unexpected)
	default:
	}
	service.stopHLSSession("session-1")
}

func TestSeekableHLSPreloadTenSegmentsWaitsForCurrentGeneration(t *testing.T) {
	if hlsPreloadWindowSegments != 10 || (hlsSeekAheadToleranceSegments+1)*hlsSegmentDurationSeconds != 30 {
		t.Fatalf("preload window = %d segments (%d seconds), want 10 segments (30 seconds)", hlsPreloadWindowSegments, (hlsSeekAheadToleranceSegments+1)*hlsSegmentDurationSeconds)
	}
	directory := t.TempDir()
	playlist := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:3.000000,\nsegment-000000.ts\n"
	if err := os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte(playlist), 0o600); err != nil {
		t.Fatal(err)
	}
	asset := storedAsset{ID: "stream-1", Kind: processingTranscode, URL: "https://media.example/movie.mkv", HLSSegmentContainer: "ts", DurationSeconds: 120}
	done := make(chan struct{})
	job := &hlsJob{directory: directory, startOffsetSeconds: 0, done: done, cancel: func() { close(done) }}
	key := hlsJobKey("session-1", asset)
	processor := &seekableFixtureHLSProcessor{starts: make(chan float64, 1)}
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: directory, MaxStorageBytes: 1024 * 1024, IdleTTL: time.Minute},
		hlsJobs:      map[string]*hlsJob{key: job}, processor: processor,
	}
	t.Cleanup(func() { service.stopHLSSession("session-1") })

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/asset?file=seek-000009.ts", nil)
	result := make(chan error, 1)
	go func() {
		result <- service.serveSeekableHLSSegment(response, request, "session-1", asset, processor, 9)
	}()
	select {
	case err := <-result:
		t.Fatalf("30-second preload did not wait for the current worker: %v", err)
	case start := <-processor.starts:
		t.Fatalf("30-second preload started replacement generation at %.0f", start)
	case <-time.After(50 * time.Millisecond):
	}
	service.hlsMu.Lock()
	registered, jobCount := service.hlsJobs[key], len(service.hlsJobs)
	service.hlsMu.Unlock()
	if registered != job || jobCount != 1 {
		t.Fatalf("30-second preload replaced current generation: registered=%p current=%p jobs=%d", registered, job, jobCount)
	}
	if err := os.WriteFile(filepath.Join(directory, "segment-000009.ts"), []byte("current-generation"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil || response.Code != http.StatusOK || response.Body.String() != "current-generation" {
			t.Fatalf("preloaded segment response = %d/%q, %v", response.Code, response.Body.String(), err)
		}
	case <-time.After(time.Second):
		t.Fatal("30-second preload did not resume when the current worker produced the segment")
	}
	select {
	case start := <-processor.starts:
		t.Fatalf("30-second preload unexpectedly started generation at %.0f", start)
	default:
	}
}

func TestSeekableHLSConcurrentOutOfOrderSegmentsKeepInFlightGeneration(t *testing.T) {
	releaseLater := make(chan struct{}, 1)
	releaseEarlier := make(chan struct{}, 1)
	processor := &blockingSeekableFixtureHLSProcessor{
		starts: make(chan float64, 2),
		releases: map[int]chan struct{}{
			60: releaseEarlier,
			63: releaseLater,
		},
	}
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1024 * 1024, IdleTTL: time.Minute},
		hlsJobs:      make(map[string]*hlsJob), processor: processor,
	}
	t.Cleanup(func() {
		select {
		case releaseLater <- struct{}{}:
		default:
		}
		select {
		case releaseEarlier <- struct{}{}:
		default:
		}
		service.stopHLSSession("session-1")
	})
	asset := storedAsset{
		ID: "stream-1", Kind: processingTranscode, URL: "https://media.example/movie.mkv",
		HLSSegmentContainer: "ts", DurationSeconds: 120,
	}
	type segmentResult struct {
		response *httptest.ResponseRecorder
		err      error
	}
	requestSegment := func(index int) <-chan segmentResult {
		result := make(chan segmentResult, 1)
		go func() {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/asset?file=seek-%06d.ts", index), nil)
			requestedAsset := asset
			requestedAsset.StartSeconds = float64(index * hlsSegmentDurationSeconds)
			result <- segmentResult{
				response: response,
				err:      service.serveHLS(response, request, "session-1", "opaque-token", requestedAsset, nil),
			}
		}()
		return result
	}
	waitStart := func() float64 {
		select {
		case start := <-processor.starts:
			return start
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for HLS generation")
			return 0
		}
	}
	waitResult := func(result <-chan segmentResult) segmentResult {
		select {
		case response := <-result:
			return response
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for HLS segment response")
			return segmentResult{}
		}
	}

	later := requestSegment(21)
	if start := waitStart(); start != 63 {
		t.Fatalf("first HLS generation start=%v, want 63", start)
	}
	earlier := requestSegment(20)
	select {
	case start := <-processor.starts:
		t.Fatalf("out-of-order request replaced an in-flight HLS generation with start %v", start)
	case result := <-later:
		t.Fatalf("in-flight HLS request completed before its processor was released: %v", result.err)
	case <-time.After(100 * time.Millisecond):
	}

	releaseLater <- struct{}{}
	laterResult := waitResult(later)
	if laterResult.err != nil || laterResult.response.Code != http.StatusOK || laterResult.response.Body.String() != "start=63,index=0" {
		t.Fatalf("later segment err=%v status=%d body=%q", laterResult.err, laterResult.response.Code, laterResult.response.Body.String())
	}
	select {
	case start := <-processor.starts:
		if start != 60 {
			t.Fatalf("second HLS generation start=%v, want 60", start)
		}
	case result := <-earlier:
		t.Fatalf("earlier segment failed before its generation started: %v", result.err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second HLS generation")
	}
	releaseEarlier <- struct{}{}
	earlierResult := waitResult(earlier)
	if earlierResult.err != nil || earlierResult.response.Code != http.StatusOK || earlierResult.response.Body.String() != "start=60,index=0" {
		t.Fatalf("earlier segment err=%v status=%d body=%q", earlierResult.err, earlierResult.response.Code, earlierResult.response.Body.String())
	}
}

func TestSeekableHLSPlaylistCompletesBeforeConcurrentSeekReplacement(t *testing.T) {
	releasePlaylist := make(chan struct{}, 1)
	releaseSegment := make(chan struct{}, 1)
	processor := &blockingSeekableFixtureHLSProcessor{
		starts: make(chan float64, 2),
		releases: map[int]chan struct{}{
			30: releasePlaylist,
			60: releaseSegment,
		},
	}
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1024 * 1024, IdleTTL: time.Minute},
		hlsJobs:      make(map[string]*hlsJob), processor: processor,
	}
	t.Cleanup(func() {
		select {
		case releasePlaylist <- struct{}{}:
		default:
		}
		select {
		case releaseSegment <- struct{}{}:
		default:
		}
		service.stopHLSSession("session-1")
	})
	asset := storedAsset{
		ID: "stream-1", Kind: processingTranscode, URL: "https://media.example/movie.mkv",
		HLSSegmentContainer: "ts", DurationSeconds: 120, StartSeconds: 31,
	}
	type responseResult struct {
		response *httptest.ResponseRecorder
		err      error
	}
	playlistResult := make(chan responseResult, 1)
	go func() {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/asset?file=index.m3u8", nil)
		playlistResult <- responseResult{response: response, err: service.serveHLS(response, request, "session-1", "opaque-token", asset, nil)}
	}()
	select {
	case start := <-processor.starts:
		if start != 30 {
			t.Fatalf("playlist HLS generation start=%v, want 30", start)
		}
	case <-time.After(time.Second):
		t.Fatal("playlist HLS generation did not start")
	}

	segmentResult := make(chan responseResult, 1)
	go func() {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/asset?file=seek-000020.ts", nil)
		seekAsset := asset
		seekAsset.StartSeconds = 60
		segmentResult <- responseResult{response: response, err: service.serveHLS(response, request, "session-1", "opaque-token", seekAsset, nil)}
	}()
	select {
	case start := <-processor.starts:
		t.Fatalf("seek replaced in-flight playlist generation with start %v", start)
	case result := <-playlistResult:
		t.Fatalf("playlist completed before its processor was released: %v", result.err)
	case <-time.After(100 * time.Millisecond):
	}

	releasePlaylist <- struct{}{}
	select {
	case result := <-playlistResult:
		if result.err != nil || result.response.Code != http.StatusOK || !strings.Contains(result.response.Body.String(), "#EXT-X-PLAYLIST-TYPE:VOD") {
			t.Fatalf("playlist err=%v status=%d body=%q", result.err, result.response.Code, result.response.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("playlist did not complete")
	}
	select {
	case start := <-processor.starts:
		if start != 60 {
			t.Fatalf("seek HLS generation start=%v, want 60", start)
		}
	case <-time.After(time.Second):
		t.Fatal("seek HLS generation did not start after playlist completed")
	}
	releaseSegment <- struct{}{}
	select {
	case result := <-segmentResult:
		if result.err != nil || result.response.Code != http.StatusOK || result.response.Body.String() != "start=60,index=0" {
			t.Fatalf("segment err=%v status=%d body=%q", result.err, result.response.Code, result.response.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("seek segment did not complete")
	}
}

func TestSeekableHLSReplacementDoesNotWaitForSlowSegmentWriter(t *testing.T) {
	releaseLater := make(chan struct{}, 1)
	releaseEarlier := make(chan struct{}, 1)
	processor := &blockingSeekableFixtureHLSProcessor{
		starts: make(chan float64, 2),
		releases: map[int]chan struct{}{
			60: releaseEarlier,
			63: releaseLater,
		},
	}
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1024 * 1024, IdleTTL: time.Minute},
		hlsJobs:      make(map[string]*hlsJob), processor: processor,
	}
	writer := newBlockingHTTPResponseWriter()
	t.Cleanup(func() {
		select {
		case releaseLater <- struct{}{}:
		default:
		}
		select {
		case releaseEarlier <- struct{}{}:
		default:
		}
		select {
		case writer.writeRelease <- struct{}{}:
		default:
		}
		service.stopHLSSession("session-1")
	})
	asset := storedAsset{
		ID: "stream-1", Kind: processingTranscode, URL: "https://media.example/movie.mkv",
		HLSSegmentContainer: "ts", DurationSeconds: 120,
	}
	laterResult := make(chan error, 1)
	go func() {
		request := httptest.NewRequest(http.MethodGet, "/asset?file=seek-000021.ts", nil)
		laterAsset := asset
		laterAsset.StartSeconds = 63
		laterResult <- service.serveHLS(writer, request, "session-1", "opaque-token", laterAsset, nil)
	}()
	select {
	case start := <-processor.starts:
		if start != 63 {
			t.Fatalf("first HLS generation start=%v, want 63", start)
		}
	case <-time.After(time.Second):
		t.Fatal("first HLS generation did not start")
	}

	earlierResponse := httptest.NewRecorder()
	earlierResult := make(chan error, 1)
	go func() {
		request := httptest.NewRequest(http.MethodGet, "/asset?file=seek-000020.ts", nil)
		earlierAsset := asset
		earlierAsset.StartSeconds = 60
		earlierResult <- service.serveHLS(earlierResponse, request, "session-1", "opaque-token", earlierAsset, nil)
	}()
	select {
	case start := <-processor.starts:
		t.Fatalf("out-of-order request replaced an unready generation with start %v", start)
	case <-time.After(100 * time.Millisecond):
	}

	releaseLater <- struct{}{}
	select {
	case <-writer.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("later segment writer did not start")
	}
	select {
	case start := <-processor.starts:
		if start != 60 {
			t.Fatalf("replacement HLS generation start=%v, want 60", start)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement waited for the slow segment writer")
	}

	releaseEarlier <- struct{}{}
	select {
	case err := <-earlierResult:
		if err != nil || earlierResponse.Code != http.StatusOK || earlierResponse.Body.String() != "start=60,index=0" {
			t.Fatalf("earlier segment err=%v status=%d body=%q", err, earlierResponse.Code, earlierResponse.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("earlier segment did not complete")
	}
	writer.writeRelease <- struct{}{}
	select {
	case err := <-laterResult:
		if err != nil || writer.status != http.StatusOK || writer.body.String() != "start=63,index=0" {
			t.Fatalf("later segment err=%v status=%d body=%q", err, writer.status, writer.body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("later segment did not finish after releasing its writer")
	}
}

func TestSeekableHLSSegmentCountIncludesAdvertisedBoundary(t *testing.T) {
	asset := storedAsset{Kind: processingTranscode, HLSSegmentContainer: "ts", DurationSeconds: maximumPlaylistReferences * hlsSegmentDurationSeconds}
	if count, ok := seekableHLSSegmentCount(asset); !ok || count != maximumPlaylistReferences {
		t.Fatalf("boundary count=%d eligible=%v, want %d/true", count, ok, maximumPlaylistReferences)
	}
	asset.DurationSeconds++
	if count, ok := seekableHLSSegmentCount(asset); ok || count != maximumPlaylistReferences+1 {
		t.Fatalf("over-boundary count=%d eligible=%v, want %d/false", count, ok, maximumPlaylistReferences+1)
	}
}

func TestSeekableHLSPlaylistRewritesAdvertisedBoundary(t *testing.T) {
	asset := storedAsset{
		Kind: processingTranscode, HLSSegmentContainer: "ts",
		DurationSeconds: maximumPlaylistReferences * hlsSegmentDurationSeconds, StartSeconds: 31,
	}
	playlist, err := seekableHLSPlaylist(asset, maximumPlaylistReferences)
	if err != nil {
		t.Fatal(err)
	}
	references := 0
	rewritten, err := rewriteLocalPlaylistWithReferencePolicy(playlist, true, func(reference string, mediaSegment bool) string {
		if !mediaSegment {
			t.Fatalf("boundary reference %q was not classified as a media segment", reference)
		}
		references++
		return "/media/" + reference
	})
	if err != nil {
		t.Fatal(err)
	}
	result := string(rewritten)
	if references != maximumPlaylistReferences || strings.Count(result, "#EXT-X-START:") != 1 ||
		!strings.Contains(result, "/media/seek-009999.ts") {
		t.Fatalf("boundary rewrite references=%d bytes=%d", references, len(rewritten))
	}
}

func TestHLSSeekGatesAreContextAwareAndScoped(t *testing.T) {
	service := &Service{}
	releaseFirst, err := service.acquireHLSSeekGate(context.Background(), "session-a/asset")
	if err != nil {
		t.Fatal(err)
	}
	otherContext, cancelOther := context.WithTimeout(context.Background(), time.Second)
	defer cancelOther()
	releaseOther, err := service.acquireHLSSeekGate(otherContext, "session-b/asset")
	if err != nil {
		t.Fatalf("unrelated gate was blocked: %v", err)
	}
	releaseOther()

	waitContext, cancelWait := context.WithCancel(context.Background())
	waitResult := make(chan error, 1)
	go func() {
		release, waitErr := service.acquireHLSSeekGate(waitContext, "session-a/asset")
		if release != nil {
			release()
		}
		waitResult <- waitErr
	}()
	time.Sleep(10 * time.Millisecond)
	cancelWait()
	if waitErr := <-waitResult; !errors.Is(waitErr, context.Canceled) {
		t.Fatalf("canceled gate wait error=%v", waitErr)
	}
	releaseFirst()
	service.hlsSeekMu.Lock()
	remaining := len(service.hlsSeekGates)
	service.hlsSeekMu.Unlock()
	if remaining != 0 {
		t.Fatalf("released seek gates=%d, want 0", remaining)
	}
}

func TestHLSRequestAdmissionIsAtomicWithStop(t *testing.T) {
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1024 * 1024, IdleTTL: time.Minute},
		hlsJobs:      make(map[string]*hlsJob),
	}
	for index := range 100 {
		key := fmt.Sprintf("session/asset/%d", index)
		directory := filepath.Join(service.mediaOptions.TempDirectory, strconv.Itoa(index))
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		close(done)
		job := &hlsJob{directory: directory, cancel: func() {}, done: done}
		service.hlsMu.Lock()
		service.hlsJobs[key] = job
		service.hlsMu.Unlock()
		start := make(chan struct{})
		retained := make(chan *hlsJobRequest, 1)
		stopped := make(chan struct{})
		go func() {
			<-start
			retained <- service.retainHLSJobRequest(key, job)
		}()
		go func() {
			<-start
			service.stopHLSJobInstance(key, job)
			close(stopped)
		}()
		close(start)
		if request := <-retained; request != nil {
			request.release()
		}
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("HLS stop blocked after admitted request released")
		}
		service.hlsMu.Lock()
		remaining := service.hlsJobs[key]
		service.hlsMu.Unlock()
		if remaining != nil {
			t.Fatal("stopped HLS job remained registered")
		}
	}
}

func TestHLSExpectedStopCannotCancelSameKeyReplacement(t *testing.T) {
	root := t.TempDir()
	oldDirectory := filepath.Join(root, "old")
	newDirectory := filepath.Join(root, "new")
	if err := os.MkdirAll(oldDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	oldDone := make(chan struct{})
	newDone := make(chan struct{})
	close(oldDone)
	close(newDone)
	oldCanceled := make(chan struct{}, 1)
	newCanceled := make(chan struct{}, 1)
	oldJob := &hlsJob{directory: oldDirectory, cancel: func() { oldCanceled <- struct{}{} }, done: oldDone}
	newJob := &hlsJob{directory: newDirectory, cancel: func() { newCanceled <- struct{}{} }, done: newDone}
	key := "session/asset/60"
	service := &Service{mediaOptions: MediaOptions{TempDirectory: root}, hlsJobs: map[string]*hlsJob{key: newJob}}

	service.stopHLSJobInstance(key, oldJob)
	service.hlsMu.Lock()
	registered := service.hlsJobs[key]
	service.hlsMu.Unlock()
	if registered != newJob {
		t.Fatal("stale timer stop removed the same-key replacement")
	}
	select {
	case <-oldCanceled:
		t.Fatal("stale timer stop canceled its already-replaced job")
	case <-newCanceled:
		t.Fatal("stale timer stop canceled the same-key replacement")
	default:
	}
	service.stopHLSJob(key)
	select {
	case <-newCanceled:
	default:
		t.Fatal("current job stop did not cancel the replacement")
	}
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

func TestFFmpegHLSOutputArgumentsPreserveSeekableTimeline(t *testing.T) {
	seekable := storedAsset{
		Kind:                processingTranscode,
		HLSSegmentContainer: "ts",
		DurationSeconds:     3_600,
		StartSeconds:        87,
	}
	arguments := strings.Join(hlsOutputArguments(seekable, "flags"), " ")
	if !strings.Contains(arguments, "-output_ts_offset 87 -f hls") {
		t.Fatalf("seekable HLS arguments do not preserve absolute timestamps: %s", arguments)
	}

	for name, asset := range map[string]storedAsset{
		"initial generation": {Kind: processingTranscode, HLSSegmentContainer: "ts", DurationSeconds: 3_600},
		"relative fMP4":      {Kind: processingTranscode, HLSSegmentContainer: "mp4", DurationSeconds: 3_600, StartSeconds: 87},
		"relative remux":     {Kind: processingRemux, HLSSegmentContainer: "ts", DurationSeconds: 3_600, StartSeconds: 87},
	} {
		if relative := strings.Join(hlsOutputArguments(asset, "flags"), " "); strings.Contains(relative, "-output_ts_offset") {
			t.Fatalf("%s HLS arguments unexpectedly shift timestamps: %s", name, relative)
		}
	}
}

func TestHLSPlaylistSegmentBoundsAcceptTransportStreamAndFragmentedMP4(t *testing.T) {
	for _, suffix := range []string{".ts", ".m4s"} {
		t.Run(suffix, func(t *testing.T) {
			directory := t.TempDir()
			playlist := "#EXTM3U\nsegment-000003" + suffix + "\nsegment-bad" + suffix + "\nsegment-000007" + suffix + "\n"
			if err := os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte(playlist), 0o600); err != nil {
				t.Fatal(err)
			}
			first, last, ok := hlsPlaylistSegmentBounds(directory)
			if !ok || first != 3 || last != 7 {
				t.Fatalf("%s segment bounds = %d..%d found=%t", suffix, first, last, ok)
			}
		})
	}
}

func TestHLSShareableWindowReservesProductionLead(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "segment-000000.ts"), []byte{1}, 0o600); err != nil {
		t.Fatal(err)
	}
	job := &hlsJob{directory: directory, segmentContainer: "ts", done: make(chan struct{})}
	writeSequence := func(sequence int) {
		t.Helper()
		contents := fmt.Sprintf("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:%d\n", sequence)
		if err := os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSequence(hlsSharedJoinSegments)
	if !hlsJobShareable(job) {
		t.Fatalf("worker at shared join boundary %d was rejected", hlsSharedJoinSegments)
	}
	writeSequence(hlsSharedJoinSegments + 1)
	if hlsJobShareable(job) {
		t.Fatalf("worker beyond shared join boundary %d remained shareable", hlsSharedJoinSegments)
	}
}

func TestHLSMasterPlaylistPointsToOpaqueMediaCapability(t *testing.T) {
	tests := []struct {
		name          string
		target        *PlaybackDecisionTarget
		videoBitrate  int
		wantBandwidth int
		wantCodecs    string
	}{
		{name: "H264 target", target: &PlaybackDecisionTarget{VideoCodec: "h264", AudioCodec: "aac"}, videoBitrate: 4500, wantBandwidth: 4756000, wantCodecs: "avc1,mp4a"},
		{name: "HEVC target", target: &PlaybackDecisionTarget{VideoCodec: "hevc", AudioCodec: "aac"}, videoBitrate: 4500, wantBandwidth: 4756000, wantCodecs: "hvc1,mp4a"},
		{name: "AV1 target", target: &PlaybackDecisionTarget{VideoCodec: "av1", AudioCodec: "aac"}, videoBitrate: 4500, wantBandwidth: 4756000, wantCodecs: "av01,mp4a"},
		{name: "unknown target codec", target: &PlaybackDecisionTarget{VideoCodec: "unknown-video", AudioCodec: "aac"}, videoBitrate: 4500, wantBandwidth: 4756000},
		{name: "audio-only target", target: &PlaybackDecisionTarget{AudioCodec: "aac"}, wantBandwidth: 256000, wantCodecs: "mp4a"},
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
				VideoBitrateKbps: test.videoBitrate, Decision: &PlaybackDecision{Target: test.target},
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
			streamInformation := fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,AVERAGE-BANDWIDTH=%d", test.wantBandwidth, test.wantBandwidth)
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

func TestHLSPlaylistEncodedSecondsIncludesSlidingMediaSequence(t *testing.T) {
	directory := t.TempDir()
	playlist := "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:120\n#EXTINF:3.0,\nsegment-000120.m4s\n#EXTINF:2.5,\nsegment-000121.m4s\n"
	if err := os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte(playlist), 0o600); err != nil {
		t.Fatal(err)
	}
	seconds, ok := hlsPlaylistEncodedSeconds(directory)
	if !ok || seconds != 365.5 {
		t.Fatalf("rotated playlist progress = %v, %t, want 365.5, true", seconds, ok)
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

func waitForEmptyHLSWorkspace(t *testing.T, service *Service) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		service.hlsMu.Lock()
		jobCount := len(service.hlsJobs)
		service.hlsMu.Unlock()
		bytes := directorySize(service.mediaOptions.TempDirectory)
		if jobCount == 0 && bytes == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("HLS cleanup left jobs=%d bytes=%d", jobCount, bytes)
		}
		time.Sleep(time.Millisecond)
	}
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

type gatedFailingHLSProcessor struct {
	started chan struct{}
	release chan struct{}
	err     error
}

func (processor *gatedFailingHLSProcessor) ProcessHLS(ctx context.Context, _ storedAsset, _ string) error {
	close(processor.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-processor.release:
		return processor.err
	}
}

func (*gatedFailingHLSProcessor) ConvertSubtitle(context.Context, storedAsset, io.Writer) error {
	return nil
}

func (*gatedFailingHLSProcessor) Probe(context.Context, storedAsset) (MediaInspection, error) {
	return MediaInspection{}, nil
}

type sharedFixtureHLSProcessor struct {
	mu          sync.Mutex
	starts      int
	cancels     int
	directories []string
	started     chan string
	complete    bool
	err         error
}

func (processor *sharedFixtureHLSProcessor) ProcessHLS(ctx context.Context, _ storedAsset, directory string) error {
	processor.mu.Lock()
	processor.starts++
	processor.directories = append(processor.directories, directory)
	processor.mu.Unlock()
	if err := os.WriteFile(filepath.Join(directory, "segment-000000.ts"), []byte("segment"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte("#EXTM3U\n#EXTINF:12.000,\nsegment-000000.ts\n#EXT-X-ENDLIST\n"), 0o600); err != nil {
		return err
	}
	processor.started <- directory
	if processor.err != nil {
		return processor.err
	}
	if processor.complete {
		return nil
	}
	<-ctx.Done()
	processor.mu.Lock()
	processor.cancels++
	processor.mu.Unlock()
	return ctx.Err()
}

func (*sharedFixtureHLSProcessor) ConvertSubtitle(context.Context, storedAsset, io.Writer) error {
	return nil
}
func (*sharedFixtureHLSProcessor) Probe(context.Context, storedAsset) (MediaInspection, error) {
	return MediaInspection{}, nil
}

func (processor *sharedFixtureHLSProcessor) counts() (int, int) {
	processor.mu.Lock()
	defer processor.mu.Unlock()
	return processor.starts, processor.cancels
}

func waitForSharedHLSStart(t *testing.T, processor *sharedFixtureHLSProcessor) string {
	t.Helper()
	select {
	case directory := <-processor.started:
		return directory
	case <-time.After(time.Second):
		t.Fatal("shared HLS processor did not start")
		return ""
	}
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

	job, err := service.hlsJob(context.Background(), "session-1", asset, processor, true)
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
	waitForEmptyHLSWorkspace(t, service)
}

func TestHLSStorageMonitorUsesOneCadenceAcrossJobsAndStops(t *testing.T) {
	ticks := make(chan time.Time)
	tickerCreated := make(chan struct{})
	tickerStopped := make(chan struct{})
	workspaceScanned := make(chan struct{}, 1)
	var tickerCalls atomic.Int32
	var scanCalls atomic.Int32
	var observeScans atomic.Bool
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1024 * 1024, IdleTTL: time.Minute},
		hlsJobs:      make(map[string]*hlsJob),
		hlsStorageTickerFactory: func(time.Duration) hlsStorageTicker {
			if tickerCalls.Add(1) == 1 {
				close(tickerCreated)
			}
			return hlsStorageTicker{ticks: ticks, stop: func() { close(tickerStopped) }}
		},
	}
	service.hlsWorkspaceSize = func(root string) int64 {
		size := directorySize(root)
		if observeScans.Load() {
			scanCalls.Add(1)
			workspaceScanned <- struct{}{}
		}
		return size
	}

	processors := make([]*blockingHLSProcessor, 3)
	for index := range processors {
		processor := &blockingHLSProcessor{ready: make(chan struct{}), stopped: make(chan struct{})}
		processors[index] = processor
		if _, err := service.hlsJob(context.Background(), fmt.Sprintf("session-%d", index), storedAsset{ID: fmt.Sprintf("stream-%d", index)}, processor, true); err != nil {
			t.Fatal(err)
		}
		select {
		case <-processor.ready:
		case <-time.After(time.Second):
			t.Fatal("media processor did not start")
		}
	}
	select {
	case <-tickerCreated:
	case <-time.After(time.Second):
		t.Fatal("storage monitor cadence was not created")
	}
	if calls := tickerCalls.Load(); calls != 1 {
		t.Fatalf("storage monitor cadences = %d, want 1 for three jobs", calls)
	}

	observeScans.Store(true)
	ticks <- time.Now()
	select {
	case <-workspaceScanned:
	case <-time.After(time.Second):
		t.Fatal("storage cadence did not scan the workspace")
	}
	if calls := scanCalls.Load(); calls != 1 {
		t.Fatalf("workspace scans for one cadence = %d, want 1", calls)
	}

	for index, processor := range processors {
		service.stopHLSSession(fmt.Sprintf("session-%d", index))
		select {
		case <-processor.stopped:
		case <-time.After(time.Second):
			t.Fatal("media processor did not stop")
		}
	}
	select {
	case <-tickerStopped:
	case <-time.After(time.Second):
		t.Fatal("storage monitor timer leaked after its last writer stopped")
	}
	select {
	case <-workspaceScanned:
	case <-time.After(time.Second):
		t.Fatal("last writer release did not perform its exact storage scan")
	}
	if calls := scanCalls.Load(); calls != 2 {
		t.Fatalf("workspace scans = %d, want one cadence scan plus one final release scan", calls)
	}
	if calls := tickerCalls.Load(); calls != 1 {
		t.Fatalf("storage monitor restarted while stopping: %d cadences", calls)
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

	_, err := service.hlsJob(context.Background(), "session-1", storedAsset{ID: "stream-1"}, processor, true)
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
	if _, err := service.hlsJob(context.Background(), "session-1", storedAsset{ID: "stream-1"}, processor, true); err != nil {
		t.Fatalf("start media job after startup cleanup: %v", err)
	}
	select {
	case <-processor.ready:
	case <-time.After(time.Second):
		t.Fatal("media job did not start after startup cleanup")
	}
	service.stopHLSSession("session-1")
}
func TestHLSAdmissionSerializesStorageUntilWriterPublication(t *testing.T) {
	publicationObserved := false
	serializationEndedEarly := false
	var service *Service
	service = &Service{
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1},
		hlsJobs:      make(map[string]*hlsJob),
		now: func() time.Time {
			publicationObserved = true
			if service.hlsStorageMu.TryLock() {
				serializationEndedEarly = true
				service.hlsStorageMu.Unlock()
			}
			return time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)
		},
	}
	processor := &blockingHLSProcessor{ready: make(chan struct{}), stopped: make(chan struct{})}
	if _, err := service.hlsJob(context.Background(), "replacement", storedAsset{ID: "stream"}, processor, true); err != nil {
		t.Fatalf("admit replacement writer: %v", err)
	}
	if !publicationObserved {
		t.Fatal("replacement writer publication was not observed")
	}
	if serializationEndedEarly {
		t.Fatal("storage serialization ended before replacement writer publication")
	}
	select {
	case <-processor.ready:
	case <-time.After(time.Second):
		t.Fatal("replacement writer did not start")
	}
	service.stopHLSSession("replacement")
}

func TestHLSStorageCapacityEvictsInactiveJobAndReadmitsImmediately(t *testing.T) {
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1, IdleTTL: time.Minute},
		hlsJobs:      make(map[string]*hlsJob),
	}
	first := &blockingHLSProcessor{ready: make(chan struct{}), stopped: make(chan struct{})}
	firstJob, err := service.hlsJob(context.Background(), "session-1", storedAsset{ID: "stream-1"}, first, true)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.ready:
	case <-time.After(time.Second):
		t.Fatal("first media job did not fill the workspace")
	}

	second := &blockingHLSProcessor{ready: make(chan struct{}), stopped: make(chan struct{})}
	if _, err := service.hlsJob(context.Background(), "session-2", storedAsset{ID: "stream-2"}, second, true); err != nil {
		t.Fatalf("start media job after immediate quota eviction: %v", err)
	}
	select {
	case <-first.stopped:
	default:
		t.Fatal("inactive quota victim continued processing")
	}
	if _, err := os.Stat(firstJob.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inactive quota victim workspace survived: %v", err)
	}
	select {
	case <-second.ready:
	case <-time.After(time.Second):
		t.Fatal("replacement media job did not start")
	}
	service.stopHLSSession("session-2")
	waitForEmptyHLSWorkspace(t, service)
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
func TestFailedPrewarmCleansBindingAndWorkspaceWithoutResolve(t *testing.T) {
	processor := &gatedFailingHLSProcessor{
		started: make(chan struct{}), release: make(chan struct{}), err: errors.New("source failed"),
	}
	service := &Service{
		processor:    processor,
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1 << 20, IdleTTL: time.Hour},
		hlsJobs:      make(map[string]*hlsJob),
	}
	asset := storedAsset{ID: "stream", Kind: processingTranscode, URL: "https://media.example/movie.mkv"}
	prewarmID := prewarmHLSSession("auth-session", "profile")
	if err := service.prewarmHLS(context.Background(), prewarmID, Source{Protocol: "hls", Mode: processingTranscode}, &asset); err != nil {
		t.Fatalf("start prewarm: %v", err)
	}
	select {
	case <-processor.started:
	case <-time.After(time.Second):
		t.Fatal("prewarm processor did not start")
	}
	close(processor.release)
	waitForEmptyHLSWorkspace(t, service)
}

func TestPrewarmHLSReturnsWhileGenerationStarts(t *testing.T) {
	release := make(chan struct{})
	processor := &blockingSeekableFixtureHLSProcessor{
		starts: make(chan float64, 1), releases: map[int]chan struct{}{0: release},
	}
	service := &Service{
		processor:    processor,
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1024 * 1024, IdleTTL: time.Minute},
		hlsJobs:      make(map[string]*hlsJob),
	}
	prewarmID := prewarmHLSSession("auth-session", "profile")
	defer service.stopHLSSession(prewarmID)
	asset := storedAsset{ID: "stream", Kind: processingTranscode, URL: "https://media.example/movie.mkv"}
	result := make(chan error, 1)
	go func() {
		result <- service.prewarmHLS(context.Background(), prewarmID, Source{Protocol: "hls", Mode: processingTranscode}, &asset)
	}()
	select {
	case start := <-processor.starts:
		if start != 0 {
			t.Fatalf("prewarm generation start = %v", start)
		}
	case <-time.After(time.Second):
		t.Fatal("prewarm generation did not start")
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("start asynchronous prewarm: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		close(release)
		<-result
		t.Fatal("prewarm waited for the first HLS playlist")
	}
	close(release)
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
	if _, err := service.hlsJob(context.Background(), prewarmID, firstAsset, first, true); err != nil {
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

type capacityHLSProcessor struct {
	slot chan struct{}
}

func (processor *capacityHLSProcessor) ProcessHLS(ctx context.Context, _ storedAsset, directory string) error {
	select {
	case processor.slot <- struct{}{}:
		defer func() { <-processor.slot }()
	default:
		return ErrMediaCapacityReached
	}
	if err := os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte("#EXTM3U\n#EXTINF:3,\nsegment-000000.ts\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "segment-000000.ts"), []byte("segment"), 0o600); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

func (*capacityHLSProcessor) ConvertSubtitle(context.Context, storedAsset, io.Writer) error {
	return nil
}

func (*capacityHLSProcessor) Probe(context.Context, storedAsset) (MediaInspection, error) {
	return MediaInspection{}, nil
}

func TestHLSStorageEvictsPrewarmButPreservesActiveRequestAndReadmits(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	service := &Service{
		now:          func() time.Time { return now },
		mediaOptions: MediaOptions{TempDirectory: root, MaxStorageBytes: 6, IdleTTL: time.Hour},
		hlsJobs:      make(map[string]*hlsJob),
	}
	register := func(key, sessionID string, prewarming bool, accessed time.Time) *hlsJob {
		directory := filepath.Join(root, key)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "segment.ts"), []byte("four"), 0o600); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		close(done)
		job := &hlsJob{
			directory: directory, sessionID: sessionID, prewarming: prewarming,
			createdAt: accessed, lastAccessed: accessed, cancel: func() {}, done: done,
		}
		service.hlsJobs[key] = job
		return job
	}
	activeKey := "session-active/asset/0"
	active := register(activeKey, "session-active", false, now.Add(-time.Hour))
	request := service.retainHLSJobRequest(activeKey, active)
	prewarmKey := "prewarm-auth-profile/asset/0"
	prewarm := register(prewarmKey, "prewarm-auth-profile", true, now)
	if service.reclaimHLSStorage(false) != true {
		t.Fatal("storage remained over quota after evicting a prewarm")
	}
	if service.hlsJobs[activeKey] != active {
		t.Fatal("storage eviction canceled the actively served job")
	}
	if service.hlsJobs[prewarmKey] != nil {
		t.Fatal("storage eviction retained the prewarm job")
	}
	if _, err := os.Stat(prewarm.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prewarm workspace survived eviction: %v", err)
	}
	if _, err := os.Stat(active.directory); err != nil {
		t.Fatalf("active workspace was removed during its request: %v", err)
	}

	processor := &failingHLSProcessor{}
	admitted, err := service.hlsJob(context.Background(), "session-new", storedAsset{ID: "new"}, processor, true)
	if err != nil {
		t.Fatalf("new job was not admitted after immediate eviction: %v", err)
	}
	service.stopHLSJob(hlsJobKey("session-new", storedAsset{ID: "new"}))
	select {
	case <-admitted.done:
	case <-time.After(time.Second):
		t.Fatal("admitted job goroutine did not finish")
	}
	request.release()
	service.stopHLSJob(activeKey)
	waitForEmptyHLSWorkspace(t, service)
}

func TestHLSStorageVictimFallsBackToInactiveLRU(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	service := &Service{hlsJobs: make(map[string]*hlsJob)}
	newJob := func(accessed time.Time) *hlsJob {
		return &hlsJob{createdAt: accessed, lastAccessed: accessed, done: make(chan struct{})}
	}
	older := newJob(now.Add(-time.Hour))
	newer := newJob(now)
	active := newJob(now.Add(-2 * time.Hour))
	active.activeRequests = 1
	service.hlsJobs["newer"] = newer
	service.hlsJobs["older"] = older
	service.hlsJobs["active"] = active
	key, job := service.hlsStorageVictim()
	if key != "older" || job != older {
		t.Fatalf("inactive LRU victim = %q/%p, want older/%p", key, job, older)
	}
}

func TestHLSStorageVictimFallsBackToDeterministicActiveWriter(t *testing.T) {
	accessed := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	service := &Service{hlsJobs: make(map[string]*hlsJob)}
	newJob := func() *hlsJob {
		return &hlsJob{
			createdAt: accessed, lastAccessed: accessed, activeRequests: 1,
			activeRequestsDone: make(chan struct{}), done: make(chan struct{}),
		}
	}
	first := newJob()
	second := newJob()
	service.hlsJobs["active-b"] = second
	service.hlsJobs["active-a"] = first
	key, job := service.hlsStorageVictim()
	if key != "active-a" || job != first {
		t.Fatalf("active storage victim = %q/%p, want active-a/%p", key, job, first)
	}
}

func TestHLSStorageReclaimCancelsActiveWritersAndDefersCleanupForReaders(t *testing.T) {
	root := t.TempDir()
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: root, MaxStorageBytes: 1, IdleTTL: time.Hour},
		hlsJobs:      make(map[string]*hlsJob),
	}
	type activeJob struct {
		job      *hlsJob
		request  *hlsJobRequest
		canceled chan struct{}
	}
	register := func(key string) activeJob {
		directory := filepath.Join(root, key)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "segment.ts"), []byte("four"), 0o600); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		canceled := make(chan struct{})
		job := &hlsJob{
			directory: directory, createdAt: time.Now(), lastAccessed: time.Now(),
			cancel: func() { close(canceled); close(done) }, done: done,
		}
		service.hlsJobs[key] = job
		request := service.retainHLSJobRequest(key, job)
		if request == nil {
			t.Fatalf("retain active request for %q", key)
		}
		return activeJob{job: job, request: request, canceled: canceled}
	}
	first := register("active-a")
	second := register("active-b")

	if service.reclaimHLSStorage(false) {
		t.Fatal("reclaim reported capacity while active readers still held over-limit files")
	}
	if len(service.hlsJobs) != 0 {
		t.Fatalf("reclaim retained %d active writers", len(service.hlsJobs))
	}
	for _, active := range []activeJob{first, second} {
		select {
		case <-active.canceled:
		default:
			t.Fatalf("over-limit active writer for %q was not canceled", active.job.directory)
		}
		if _, err := os.Stat(active.job.directory); err != nil {
			t.Fatalf("active reader workspace was removed before release: %v", err)
		}
	}

	first.request.release()
	second.request.release()
	deadline := time.Now().Add(time.Second)
	for directorySize(root) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if size := directorySize(root); size != 0 {
		t.Fatalf("released active reader workspaces retained %d bytes", size)
	}
}

func TestStartSessionHLSWaitsForAdoptedPrewarmAndCleansFailure(t *testing.T) {
	processErr := errors.New("prewarm source failed")
	processor := &gatedFailingHLSProcessor{
		started: make(chan struct{}), release: make(chan struct{}), err: processErr,
	}
	service := &Service{
		processor:    processor,
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1 << 20, IdleTTL: time.Hour},
		hlsJobs:      make(map[string]*hlsJob),
	}
	asset := storedAsset{ID: "asset", Kind: processingTranscode, URL: "https://media.example/movie.mkv"}
	source := Source{ID: asset.ID, Compatible: true, Protocol: "hls", Mode: processingTranscode}
	prewarmSession := prewarmHLSSession("auth", "profile")
	if err := service.prewarmHLS(context.Background(), prewarmSession, source, &asset); err != nil {
		t.Fatalf("start prewarm: %v", err)
	}
	select {
	case <-processor.started:
	case <-time.After(time.Second):
		t.Fatal("prewarm processor did not start")
	}
	result := make(chan error, 1)
	go func() {
		result <- service.startSessionHLS(context.Background(), prewarmSession, "session", []Source{source}, []storedAsset{asset})
	}()
	deadline := time.Now().Add(time.Second)
	for {
		service.hlsMu.Lock()
		adopted := service.hlsJobs[hlsJobKey("session", asset)] != nil && service.hlsJobs[hlsJobKey(prewarmSession, asset)] == nil
		service.hlsMu.Unlock()
		if adopted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("prewarm job was not adopted")
		}
		time.Sleep(time.Millisecond)
	}
	close(processor.release)
	select {
	case err := <-result:
		if !errors.Is(err, processErr) {
			t.Fatalf("adopted prewarm error = %v, want %v", err, processErr)
		}
	case <-time.After(time.Second):
		t.Fatal("session start did not observe adopted prewarm failure")
	}
	service.hlsMu.Lock()
	jobs := len(service.hlsJobs)
	service.hlsMu.Unlock()
	if jobs != 0 {
		t.Fatalf("failed adopted prewarm retained %d job bindings", jobs)
	}
}
func TestStartSessionHLSPrioritizesPlaybackOverNonAdoptablePrewarm(t *testing.T) {
	processor := &capacityHLSProcessor{slot: make(chan struct{}, 1)}
	service := &Service{
		processor:    processor,
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1 << 20, IdleTTL: time.Hour},

		hlsJobs: make(map[string]*hlsJob),
	}
	prewarmSession := prewarmHLSSession("auth", "profile")
	prewarmAsset := storedAsset{ID: "asset", Kind: processingTranscode, URL: "https://media.example/prewarm.mkv"}
	prewarm, err := service.hlsJob(context.Background(), prewarmSession, prewarmAsset, processor, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForMediaFile(context.Background(), prewarm, filepath.Join(prewarm.directory, "index.m3u8")); err != nil {
		t.Fatal(err)
	}
	playbackAsset := prewarmAsset
	playbackAsset.URL = "https://media.example/playback.mkv"
	source := Source{ID: playbackAsset.ID, Compatible: true, Protocol: "hls", Mode: processingTranscode}
	if err := service.startSessionHLS(context.Background(), prewarmSession, "session", []Source{source}, []storedAsset{playbackAsset}); err != nil {
		t.Fatalf("real playback did not displace saturated prewarm: %v", err)
	}
	if service.hlsJobs[hlsJobKey(prewarmSession, prewarmAsset)] != nil || service.hlsJobs[hlsJobKey("session", playbackAsset)] == nil {
		t.Fatalf("unexpected jobs after priority retry: %+v", service.hlsJobs)
	}
	service.stopHLSSession("session")
	waitForEmptyHLSWorkspace(t, service)
}

func TestAdoptHLSJobRequiresExactFingerprint(t *testing.T) {
	base := storedAsset{
		ID: "asset", Kind: processingTranscode, URL: "https://media.example/movie.mkv", Container: "mkv",
		HLSSegmentContainer: "mp4", DurationSeconds: 90, StartSeconds: 3,
		Decision: &PlaybackDecision{Target: &PlaybackDecisionTarget{VideoCodec: "h264", AudioCodec: "aac"}},
	}
	track := 1
	base.AudioTrackIndex = &track
	for _, test := range []struct {
		name   string
		mutate func(*storedAsset)
	}{
		{name: "segment container", mutate: func(asset *storedAsset) { asset.HLSSegmentContainer = "ts" }},
		{name: "start offset", mutate: func(asset *storedAsset) { asset.StartSeconds = 6 }},
		{name: "audio track", mutate: func(asset *storedAsset) { value := 2; asset.AudioTrackIndex = &value }},
		{name: "decision", mutate: func(asset *storedAsset) { asset.Decision.Target.AudioCodec = "opus" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &Service{mediaOptions: MediaOptions{TempDirectory: t.TempDir(), IdleTTL: time.Hour}, hlsJobs: make(map[string]*hlsJob)}
			from := "prewarm-auth-profile"
			job := &hlsJob{fingerprint: hlsAssetFingerprint(base), sessionID: from, prewarming: true, cancel: func() {}, done: make(chan struct{})}
			close(job.done)
			service.hlsJobs[hlsJobKey(from, base)] = job
			divergent := cloneStoredAsset(base)
			test.mutate(&divergent)
			if service.adoptHLSJob(from, "session", divergent) != nil {
				t.Fatal("divergent HLS asset was adopted")
			}
			service.stopHLSSession(from)
		})
	}

	service := &Service{mediaOptions: MediaOptions{TempDirectory: t.TempDir(), IdleTTL: time.Hour}, hlsJobs: make(map[string]*hlsJob)}
	job := &hlsJob{fingerprint: hlsAssetFingerprint(base), sessionID: "prewarm", prewarming: true, cancel: func() {}, done: make(chan struct{})}
	close(job.done)
	service.hlsJobs[hlsJobKey("prewarm", base)] = job
	if service.adoptHLSJob("prewarm", "session", cloneStoredAsset(base)) == nil {
		t.Fatal("exact HLS asset fingerprint was not adopted")
	}
	if job.prewarming || job.sessionID != "session" || service.hlsJobs[hlsJobKey("session", base)] != job {
		t.Fatalf("adopted HLS job state = %+v", job)
	}
	service.stopHLSSession("session")
}

func TestSameStartHLSReplacementUsesIsolatedWorkspace(t *testing.T) {
	processor := &blockingHLSProcessor{ready: make(chan struct{}), stopped: make(chan struct{})}
	service := &Service{
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1 << 20, IdleTTL: time.Hour},
		hlsJobs:      make(map[string]*hlsJob),
	}
	asset := storedAsset{ID: "asset", Kind: processingTranscode, URL: "https://media.example/movie.mkv", StartSeconds: 30}
	key := hlsJobKey("session", asset)
	oldJob, err := service.hlsJob(context.Background(), "session", asset, processor, true)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-processor.ready:
	case <-time.After(time.Second):
		t.Fatal("old HLS generation did not start")
	}
	request := service.retainHLSJobRequest(key, oldJob)
	if request == nil {
		t.Fatal("old HLS generation request was not retained")
	}
	service.stopHLSJobInstance(key, oldJob)
	replacement, err := service.hlsJob(context.Background(), "session", asset, &failingHLSProcessor{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if oldJob.directory == replacement.directory {
		t.Fatalf("same-start generations shared workspace %q", oldJob.directory)
	}
	if _, err := os.Stat(replacement.directory); err != nil {
		t.Fatalf("replacement workspace missing before old request release: %v", err)
	}
	request.release()
	deadline := time.Now().Add(time.Second)
	for {
		_, oldErr := os.Stat(oldJob.directory)
		if errors.Is(oldErr, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("old workspace was not cleaned after request release: %v", oldErr)
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := os.Stat(replacement.directory); err != nil {
		t.Fatalf("old cleanup removed live replacement workspace: %v", err)
	}
	service.stopHLSSession("session")
}

func newSharedHLSTestService(t *testing.T, processor *sharedFixtureHLSProcessor) *Service {
	t.Helper()
	return &Service{
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1 << 20, IdleTTL: time.Hour},
		hlsJobs:      make(map[string]*hlsJob),
		processor:    processor,
	}
}

func TestHLSJobsShareExactFingerprintAcrossSessions(t *testing.T) {
	processor := &sharedFixtureHLSProcessor{started: make(chan string, 4)}
	service := newSharedHLSTestService(t, processor)
	asset := storedAsset{ID: "asset", Kind: processingTranscode, URL: "https://media.example/movie.mkv", StartSeconds: 30}
	first, err := service.hlsJob(context.Background(), "session-a", asset, processor, true)
	if err != nil {
		t.Fatal(err)
	}
	waitForSharedHLSStart(t, processor)
	if _, err := service.hlsJob(context.Background(), "session-b", cloneStoredAsset(asset), processor, false); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("unbound session accessed shared worker: %v", err)
	}
	second, err := service.hlsJob(context.Background(), "session-b", cloneStoredAsset(asset), processor, true)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.directory != second.directory {
		t.Fatalf("identical HLS jobs did not share worker/directory: %p/%q and %p/%q", first, first.directory, second, second.directory)
	}
	if starts, _ := processor.counts(); starts != 1 {
		t.Fatalf("identical HLS jobs started %d processors, want 1", starts)
	}
	if jobs := service.activityJobs(); len(jobs) != 1 {
		t.Fatalf("shared worker produced %d activity jobs, want 1", len(jobs))
	}

	service.stopHLSSession("session-a")
	if _, cancels := processor.counts(); cancels != 0 {
		t.Fatalf("first alias detach canceled shared worker %d times", cancels)
	}
	if service.hlsJobs[hlsJobKey("session-b", asset)] != first {
		t.Fatal("first alias detach removed the surviving session binding")
	}
	if _, err := os.Stat(first.directory); err != nil {
		t.Fatalf("first alias detach removed shared workspace: %v", err)
	}

	service.stopHLSSession("session-b")
	if starts, cancels := processor.counts(); starts != 1 || cancels != 1 {
		t.Fatalf("last alias teardown starts/cancels = %d/%d, want 1/1", starts, cancels)
	}
	waitForEmptyHLSWorkspace(t, service)
}

func TestHLSBindingExpiryDetachesOnlyExpiredSession(t *testing.T) {
	processor := &sharedFixtureHLSProcessor{started: make(chan string, 4)}
	service := newSharedHLSTestService(t, processor)
	service.mediaOptions.IdleTTL = 100 * time.Millisecond
	asset := storedAsset{ID: "asset", Kind: processingTranscode, URL: "https://media.example/movie.mkv"}
	job, err := service.hlsJob(context.Background(), "session-a", asset, processor, true)
	if err != nil {
		t.Fatal(err)
	}
	waitForSharedHLSStart(t, processor)
	if _, err := service.hlsJob(context.Background(), "session-b", asset, processor, true); err != nil {
		t.Fatal(err)
	}
	service.hlsMu.Lock()
	service.hlsJobs[hlsJobKey("session-b", asset)].bindings[hlsJobKey("session-b", asset)].timer.Reset(time.Hour)
	service.hlsMu.Unlock()
	deadline := time.Now().Add(time.Second)
	for {
		service.hlsMu.Lock()
		first := service.hlsJobs[hlsJobKey("session-a", asset)]
		second := service.hlsJobs[hlsJobKey("session-b", asset)]
		service.hlsMu.Unlock()
		if first == nil {
			if second != job {
				t.Fatal("expiry removed the unexpired shared binding")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first shared binding did not expire")
		}
		time.Sleep(time.Millisecond)
	}
	if _, cancels := processor.counts(); cancels != 0 {
		t.Fatalf("single binding expiry canceled shared worker %d times", cancels)
	}
	if _, err := os.Stat(job.directory); err != nil {
		t.Fatalf("single binding expiry removed shared workspace: %v", err)
	}
	service.stopHLSSession("session-b")
	if starts, cancels := processor.counts(); starts != 1 || cancels != 1 {
		t.Fatalf("expiry cleanup starts/cancels = %d/%d, want 1/1", starts, cancels)
	}
}

func TestHLSJobsReuseCompletedButNotFailedWorkers(t *testing.T) {
	for _, test := range []struct {
		name      string
		processor *sharedFixtureHLSProcessor
		wantShare bool
	}{
		{name: "completed", processor: &sharedFixtureHLSProcessor{started: make(chan string, 4), complete: true}, wantShare: true},
		{name: "failed", processor: &sharedFixtureHLSProcessor{started: make(chan string, 4), err: errors.New("failed transcode")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newSharedHLSTestService(t, test.processor)
			asset := storedAsset{ID: "asset", Kind: processingRemux, URL: "https://media.example/movie.mkv"}
			first, err := service.hlsJob(context.Background(), "session-a", asset, test.processor, true)
			if err != nil {
				t.Fatal(err)
			}
			waitForSharedHLSStart(t, test.processor)
			<-first.done
			second, err := service.hlsJob(context.Background(), "session-b", asset, test.processor, true)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantShare {
				if second != first {
					t.Fatal("completed identical worker was not reused")
				}
			} else {
				if second == first {
					t.Fatal("failed worker was shared with a new session")
				}
				waitForSharedHLSStart(t, test.processor)
				<-second.done
			}
			service.stopHLSSession("session-a")
			service.stopHLSSession("session-b")
			wantStarts := 1
			if !test.wantShare {
				wantStarts = 2
			}
			if starts, _ := test.processor.counts(); starts != wantStarts {
				t.Fatalf("processor starts = %d, want %d", starts, wantStarts)
			}
		})
	}
}

func TestHLSJobFingerprintDifferencesNeverShare(t *testing.T) {
	base := storedAsset{
		ID: "asset", Kind: processingTranscode, URL: "https://media.example/movie.mkv", Container: "mkv",
		HLSSegmentContainer: "mp4", Headers: map[string]string{"Authorization": "Bearer first"}, ToneMap: true,
		SubtitleTrackType: subtitleBurnText, SubtitleTrackOrdinal: 1, DurationSeconds: 90, ReadRate: 1.25,
		VideoBitDepth: 10, TargetHeight: 720, VideoBitrateKbps: 4000, MaximumAudioChannels: 2,
		TargetVideoCodec: "h264", QualityPreset: "balanced", StartSeconds: 3,
		Decision: &PlaybackDecision{Target: &PlaybackDecisionTarget{VideoCodec: "h264", AudioCodec: "aac"}},
	}
	audio, subtitle := 1, 2
	base.AudioTrackIndex, base.SubtitleTrackIndex = &audio, &subtitle
	tests := []struct {
		name   string
		mutate func(*storedAsset)
	}{
		{name: "source URL", mutate: func(asset *storedAsset) { asset.URL += "?revision=2" }},
		{name: "source header", mutate: func(asset *storedAsset) { asset.Headers["Authorization"] = "Bearer second" }},
		{name: "asset identity", mutate: func(asset *storedAsset) { asset.ID = "other-asset" }},
		{name: "source container", mutate: func(asset *storedAsset) { asset.Container = "avi" }},
		{name: "mode", mutate: func(asset *storedAsset) { asset.Kind = processingRemux }},
		{name: "segment container", mutate: func(asset *storedAsset) { asset.HLSSegmentContainer = "ts" }},
		{name: "audio track", mutate: func(asset *storedAsset) { value := 3; asset.AudioTrackIndex = &value }},
		{name: "subtitle track", mutate: func(asset *storedAsset) { value := 4; asset.SubtitleTrackIndex = &value }},
		{name: "subtitle type", mutate: func(asset *storedAsset) { asset.SubtitleTrackType = subtitleBurnBitmap }},
		{name: "subtitle ordinal", mutate: func(asset *storedAsset) { asset.SubtitleTrackOrdinal++ }},
		{name: "tone map", mutate: func(asset *storedAsset) { asset.ToneMap = false }},
		{name: "tone map safety", mutate: func(asset *storedAsset) { asset.DolbyVisionToneMapSafe = true }},
		{name: "duration", mutate: func(asset *storedAsset) { asset.DurationSeconds++ }},
		{name: "read rate", mutate: func(asset *storedAsset) { asset.ReadRate = 1 }},
		{name: "video bit depth", mutate: func(asset *storedAsset) { asset.VideoBitDepth = 8 }},
		{name: "target height", mutate: func(asset *storedAsset) { asset.TargetHeight = 1080 }},
		{name: "video bitrate", mutate: func(asset *storedAsset) { asset.VideoBitrateKbps++ }},
		{name: "audio channels", mutate: func(asset *storedAsset) { asset.MaximumAudioChannels = 1 }},
		{name: "target video codec", mutate: func(asset *storedAsset) { asset.TargetVideoCodec = "hevc" }},
		{name: "quality preset", mutate: func(asset *storedAsset) { asset.QualityPreset = "quality" }},
		{name: "seek start", mutate: func(asset *storedAsset) { asset.StartSeconds = 6 }},
		{name: "decision", mutate: func(asset *storedAsset) { asset.Decision.Target.AudioCodec = "opus" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			processor := &sharedFixtureHLSProcessor{started: make(chan string, 4)}
			service := newSharedHLSTestService(t, processor)
			first, err := service.hlsJob(context.Background(), "session-a", cloneStoredAsset(base), processor, true)
			if err != nil {
				t.Fatal(err)
			}
			waitForSharedHLSStart(t, processor)
			divergent := cloneStoredAsset(base)
			test.mutate(&divergent)
			second, err := service.hlsJob(context.Background(), "session-b", divergent, processor, true)
			if err != nil {
				t.Fatal(err)
			}
			waitForSharedHLSStart(t, processor)
			if first == second || first.directory == second.directory {
				t.Fatal("divergent fingerprint shared a worker or workspace")
			}
			service.stopHLSSession("session-a")
			service.stopHLSSession("session-b")
		})
	}
}

func TestIdenticalNonProcessingHLSJobsNeverShare(t *testing.T) {
	processor := &sharedFixtureHLSProcessor{started: make(chan string, 4)}
	service := newSharedHLSTestService(t, processor)
	asset := storedAsset{ID: "asset", Kind: "direct", URL: "https://media.example/master.m3u8"}
	first, err := service.hlsJob(context.Background(), "session-a", asset, processor, true)
	if err != nil {
		t.Fatal(err)
	}
	waitForSharedHLSStart(t, processor)
	second, err := service.hlsJob(context.Background(), "session-b", asset, processor, true)
	if err != nil {
		t.Fatal(err)
	}
	waitForSharedHLSStart(t, processor)
	if first == second {
		t.Fatal("identical direct work shared a local processing worker")
	}
	service.stopHLSSession("session-a")
	service.stopHLSSession("session-b")
}

func TestLastHLSBindingWaitsForActiveRequestBeforeCancelAndCleanup(t *testing.T) {
	processor := &sharedFixtureHLSProcessor{started: make(chan string, 4)}
	service := newSharedHLSTestService(t, processor)
	asset := storedAsset{ID: "asset", Kind: processingTranscode, URL: "https://media.example/movie.mkv"}
	job, err := service.hlsJob(context.Background(), "session-a", asset, processor, true)
	if err != nil {
		t.Fatal(err)
	}
	waitForSharedHLSStart(t, processor)
	if _, err := service.hlsJob(context.Background(), "session-b", asset, processor, true); err != nil {
		t.Fatal(err)
	}
	request := service.retainHLSJobRequest(hlsJobKey("session-b", asset), job)
	if request == nil {
		t.Fatal("shared session request was not retained")
	}
	service.stopHLSSession("session-a")
	service.stopHLSSession("session-b")
	if _, cancels := processor.counts(); cancels != 0 {
		t.Fatalf("active request teardown canceled worker %d times before release", cancels)
	}
	if _, err := os.Stat(job.directory); err != nil {
		t.Fatalf("active request teardown removed workspace before release: %v", err)
	}
	request.release()
	deadline := time.Now().Add(time.Second)
	for {
		_, cancels := processor.counts()
		_, statErr := os.Stat(job.directory)
		if cancels == 1 && errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("released request left cancels=%d workspace error=%v", cancels, statErr)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestConcurrentHLSAttachAndStopDoesNotOrphanOrDoubleCancel(t *testing.T) {
	for iteration := range 20 {
		processor := &sharedFixtureHLSProcessor{started: make(chan string, 4)}
		service := newSharedHLSTestService(t, processor)
		asset := storedAsset{ID: "asset", Kind: processingTranscode, URL: "https://media.example/movie.mkv"}
		if _, err := service.hlsJob(context.Background(), "session-a", asset, processor, true); err != nil {
			t.Fatal(err)
		}
		waitForSharedHLSStart(t, processor)
		start := make(chan struct{})
		attached := make(chan error, 1)
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			_, err := service.hlsJob(context.Background(), "session-b", asset, processor, true)
			attached <- err
		}()
		go func() {
			defer wait.Done()
			<-start
			service.stopHLSSession("session-a")
		}()
		close(start)
		wait.Wait()
		if err := <-attached; err != nil {
			t.Fatalf("iteration %d attach failed: %v", iteration, err)
		}
		service.stopHLSSession("session-b")
		starts, cancels := processor.counts()
		if starts != cancels || starts < 1 || starts > 2 {
			t.Fatalf("iteration %d starts/cancels = %d/%d", iteration, starts, cancels)
		}
		waitForEmptyHLSWorkspace(t, service)
	}
}

func TestSharedHLSStorageVictimRemovesAliasesOnce(t *testing.T) {
	processor := &sharedFixtureHLSProcessor{started: make(chan string, 4)}
	service := newSharedHLSTestService(t, processor)
	asset := storedAsset{ID: "asset", Kind: processingTranscode, URL: "https://media.example/movie.mkv"}
	job, err := service.hlsJob(context.Background(), "session-a", asset, processor, true)
	if err != nil {
		t.Fatal(err)
	}
	waitForSharedHLSStart(t, processor)
	if _, err := service.hlsJob(context.Background(), "session-b", asset, processor, true); err != nil {
		t.Fatal(err)
	}
	_, victim := service.hlsStorageVictim()
	if victim != job || len(service.hlsJobs) != 0 {
		t.Fatalf("shared victim = %p bindings=%d, want %p/0", victim, len(service.hlsJobs), job)
	}
	service.stopDetachedHLSJob(victim)
	if starts, cancels := processor.counts(); starts != 1 || cancels != 1 {
		t.Fatalf("shared victim starts/cancels = %d/%d, want 1/1", starts, cancels)
	}
	waitForEmptyHLSWorkspace(t, service)
}

func TestPrewarmHLSAdoptionIsOneToOneUnderConcurrency(t *testing.T) {
	processor := &sharedFixtureHLSProcessor{started: make(chan string, 4)}
	service := newSharedHLSTestService(t, processor)
	asset := storedAsset{ID: "asset", Kind: processingTranscode, URL: "https://media.example/movie.mkv"}
	prewarm := prewarmHLSSession("auth", "profile")
	job, err := service.hlsJob(context.Background(), prewarm, asset, processor, true)
	if err != nil {
		t.Fatal(err)
	}
	waitForSharedHLSStart(t, processor)
	start := make(chan struct{})
	results := make(chan bool, 2)
	var wait sync.WaitGroup
	for _, sessionID := range []string{"session-a", "session-b"} {
		sessionID := sessionID
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- service.adoptHLSJob(prewarm, sessionID, cloneStoredAsset(asset)) != nil
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	successes := 0
	for adopted := range results {
		if adopted {
			successes++
		}
	}
	if successes != 1 || service.hlsJobs[hlsJobKey(prewarm, asset)] != nil || len(service.hlsJobs) != 1 {
		t.Fatalf("concurrent adoption successes=%d bindings=%d prewarm=%p", successes, len(service.hlsJobs), service.hlsJobs[hlsJobKey(prewarm, asset)])
	}
	for _, registered := range service.hlsJobs {
		if registered != job {
			t.Fatal("adoption replaced the prewarmed worker")
		}
	}
	service.stopHLSSession("session-a")
	service.stopHLSSession("session-b")
	if starts, cancels := processor.counts(); starts != 1 || cancels != 1 {
		t.Fatalf("adopted prewarm starts/cancels = %d/%d, want 1/1", starts, cancels)
	}
}

func TestPrewarmAdoptionMovesOnlyItsSharedBinding(t *testing.T) {
	processor := &sharedFixtureHLSProcessor{started: make(chan string, 4)}
	service := newSharedHLSTestService(t, processor)
	asset := storedAsset{ID: "asset", Kind: processingTranscode, URL: "https://media.example/movie.mkv"}
	firstPrewarm := prewarmHLSSession("auth-a", "profile")
	secondPrewarm := prewarmHLSSession("auth-b", "profile")
	job, err := service.hlsJob(context.Background(), firstPrewarm, asset, processor, true)
	if err != nil {
		t.Fatal(err)
	}
	waitForSharedHLSStart(t, processor)
	shared, err := service.hlsJob(context.Background(), secondPrewarm, asset, processor, true)
	if err != nil {
		t.Fatal(err)
	}
	if shared != job {
		t.Fatal("identical prewarms did not share a worker")
	}
	if service.adoptHLSJob(firstPrewarm, "session", cloneStoredAsset(asset)) == nil {
		t.Fatal("first shared prewarm binding was not adopted")
	}
	if service.hlsJobs[hlsJobKey(firstPrewarm, asset)] != nil || service.hlsJobs[hlsJobKey("session", asset)] != job ||
		service.hlsJobs[hlsJobKey(secondPrewarm, asset)] != job {
		t.Fatalf("shared prewarm adoption produced unexpected bindings: %+v", service.hlsJobs)
	}
	service.stopHLSSession("session")
	if _, cancels := processor.counts(); cancels != 0 {
		t.Fatalf("adopted binding stop canceled surviving prewarm %d times", cancels)
	}
	service.stopHLSSession(secondPrewarm)
	if starts, cancels := processor.counts(); starts != 1 || cancels != 1 {
		t.Fatalf("shared prewarm starts/cancels = %d/%d, want 1/1", starts, cancels)
	}
}

func TestStopAllHLSJobsWaitsForActiveRequestLease(t *testing.T) {
	processor := &sharedFixtureHLSProcessor{started: make(chan string, 1)}
	service := newSharedHLSTestService(t, processor)
	asset := storedAsset{ID: "asset", Kind: processingTranscode, URL: "https://media.example/movie.mkv"}
	job, err := service.hlsJob(context.Background(), "session", asset, processor, true)
	if err != nil {
		t.Fatal(err)
	}
	waitForSharedHLSStart(t, processor)
	request := service.retainHLSJobRequest(hlsJobKey("session", asset), job)
	if request == nil {
		t.Fatal("active request lease was not retained")
	}
	type stopResult struct {
		count int
		err   error
	}
	stopped := make(chan stopResult, 1)
	go func() {
		count, stopErr := service.stopAllHLSJobs(context.Background())
		stopped <- stopResult{count: count, err: stopErr}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		service.hlsMu.Lock()
		registered := len(service.hlsJobs)
		service.hlsMu.Unlock()
		if registered == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("global stop did not detach HLS bindings")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case result := <-stopped:
		t.Fatalf("global stop bypassed active request: %+v", result)
	default:
	}
	if _, err := os.Stat(job.directory); err != nil {
		t.Fatalf("leased workspace removed before release: %v", err)
	}
	request.release()
	select {
	case result := <-stopped:
		if result.count != 1 || result.err != nil {
			t.Fatalf("global stop result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("global stop did not finish after request release")
	}
	waitForEmptyHLSWorkspace(t, service)
}

func TestHLSAdmissionWaitsForResetGate(t *testing.T) {
	processor := &sharedFixtureHLSProcessor{started: make(chan string, 1)}
	service := newSharedHLSTestService(t, processor)
	asset := storedAsset{ID: "asset", Kind: processingTranscode, URL: "https://media.example/movie.mkv"}
	service.hlsResetMu.Lock()
	admitted := make(chan error, 1)
	go func() {
		_, err := service.hlsJob(context.Background(), "session", asset, processor, true)
		admitted <- err
	}()
	select {
	case err := <-admitted:
		service.hlsResetMu.Unlock()
		t.Fatalf("job bypassed reset gate: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	service.hlsResetMu.Unlock()
	select {
	case err := <-admitted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("job was not admitted after reset gate opened")
	}
	waitForSharedHLSStart(t, processor)
	service.stopHLSSession("session")
}

func TestHLSJobRejectsCaseAmbiguousLegacyHeaders(t *testing.T) {
	processor := &sharedFixtureHLSProcessor{started: make(chan string, 1)}
	service := newSharedHLSTestService(t, processor)
	_, err := service.hlsJob(context.Background(), "session", storedAsset{
		ID: "asset", Kind: processingTranscode, URL: "https://media.example/movie.mkv",
		Headers: map[string]string{"Authorization": "Bearer first", "authorization": "Bearer second"},
	}, processor, true)
	if !errors.Is(err, ErrMediaSourceFailed) {
		t.Fatalf("ambiguous header job error = %v", err)
	}
	if starts, _ := processor.counts(); starts != 0 {
		t.Fatalf("ambiguous headers started %d workers", starts)
	}
}

func TestHLSJobsDoNotShareAfterInitialSegmentLeavesWindow(t *testing.T) {
	processor := &sharedFixtureHLSProcessor{started: make(chan string, 2)}
	service := newSharedHLSTestService(t, processor)
	asset := storedAsset{ID: "asset", Kind: processingTranscode, URL: "https://media.example/movie.mkv"}
	first, err := service.hlsJob(context.Background(), "session-a", asset, processor, true)
	if err != nil {
		t.Fatal(err)
	}
	waitForSharedHLSStart(t, processor)
	if err := os.Remove(filepath.Join(first.directory, "segment-000000.ts")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first.directory, "index.m3u8"), []byte("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:1\n#EXTINF:3,\nsegment-000001.ts\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := service.hlsJob(context.Background(), "session-b", cloneStoredAsset(asset), processor, true)
	if err != nil {
		t.Fatal(err)
	}
	waitForSharedHLSStart(t, processor)
	if second == first {
		t.Fatal("late session reused a worker without its initial segment")
	}
	if starts, _ := processor.counts(); starts != 2 {
		t.Fatalf("stale-worker replacement starts = %d, want 2", starts)
	}
	service.stopHLSSession("session-a")
	service.stopHLSSession("session-b")
}
