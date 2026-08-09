package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/playback"
)

type bytePathCompatDelivery struct {
	*fakeCompatPlaybackDelivery
	mu       sync.Mutex
	primed   bool
	segment  []byte
	modified time.Time
}

func (delivery *bytePathCompatDelivery) Serve(response http.ResponseWriter, request *http.Request, handle playback.DeliveryHandle) error {
	if !handle.Valid() {
		return playback.ErrSessionNotFound
	}
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	childID := request.URL.Query().Get("RivuneChildId")
	if childID != "" {
		if !delivery.primed {
			return playback.ErrSessionNotFound
		}
		reader := bytes.NewReader(delivery.segment)
		response.Header().Set("Content-Type", "video/mp2t")
		http.ServeContent(response, request, childID+".ts", delivery.modified, reader)
		return nil
	}
	if !strings.HasSuffix(request.URL.Path, "/master.m3u8") {
		return playback.ErrSessionNotFound
	}
	delivery.primed = true
	payload := []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:3.000,\nchild.ts\n#EXT-X-ENDLIST\n")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	response.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	response.WriteHeader(http.StatusOK)
	_, err := response.Write(payload)
	return err
}

func TestPlaybackGatewayReadsPlaylistAndChildBytesAndRejectsOutOfOrderChild(t *testing.T) {
	fixture := newPlaybackFixture(t)
	segment := generatedTransportStreamFixture(t)
	delivery := &bytePathCompatDelivery{
		fakeCompatPlaybackDelivery: fixture.delivery,
		segment:                    segment,
		modified:                   time.Unix(1_700_000_000, 0).UTC(),
	}
	fixture.handler.playback = delivery
	fixture.handler.playSessions = newPlaySessionRegistry(delivery)
	fixture.handler.playSessions.now = func() time.Time { return fixture.now }
	delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{
		SourceRef: "gateway-source-reference", StableIdentity: "gateway-source", Protocol: "http", Container: "mkv", ExpiresAt: fixture.now.Add(time.Hour),
	}}}
	delivery.resolvedSession = playback.Session{SelectedSourceID: "gateway-selected", Sources: []playback.Source{{
		ID: "gateway-selected", Mode: "transcode", Protocol: "hls", Container: "hls",
		Media: &playback.MediaInspection{
			Container: "mkv", DurationSeconds: 3,
			VideoTracks: []playback.MediaTrack{{Index: 0, Codec: "h264", Width: 160, Height: 90}},
			AudioTracks: []playback.MediaTrack{{Index: 1, Codec: "aac", Channels: 2}},
		},
	}}}

	first := openBytePathPlayback(t, fixture)
	outOfOrder := bytePathChildRequest(t, fixture.item.ID, first.PlaySessionId, first.MediaSources[0].Id, strings.Repeat("a", 32), "")
	outOfOrderResponse := httptest.NewRecorder()
	fixture.handler.handleStream(outOfOrderResponse, outOfOrder)
	if outOfOrderResponse.Code != http.StatusNotFound || outOfOrderResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("out-of-order child status=%d cache=%q body=%s", outOfOrderResponse.Code, outOfOrderResponse.Header().Get("Cache-Control"), outOfOrderResponse.Body.String())
	}

	delivery.mu.Lock()
	delivery.primed = false
	delivery.mu.Unlock()
	second := openBytePathPlayback(t, fixture)
	masterRequest := httptest.NewRequest(http.MethodGet, second.MediaSources[0].TranscodingUrl, nil)
	masterRequest.SetPathValue("id", fixture.item.ID)
	masterResponse := httptest.NewRecorder()
	fixture.handler.handleStream(masterResponse, masterRequest)
	if masterResponse.Code != http.StatusOK || masterResponse.Header().Get("Content-Type") != "application/vnd.apple.mpegurl" || !bytes.Contains(masterResponse.Body.Bytes(), []byte("#EXTINF:3.000")) || !bytes.Contains(masterResponse.Body.Bytes(), []byte("child.ts")) {
		t.Fatalf("master status=%d type=%q payload=%q", masterResponse.Code, masterResponse.Header().Get("Content-Type"), masterResponse.Body.String())
	}

	childID := strings.Repeat("b", 32)
	for _, test := range []struct {
		name   string
		range_ string
		start  int
		end    int
		status int
	}{
		{name: "whole", start: 0, end: len(segment), status: http.StatusOK},
		{name: "beginning", range_: "bytes=0-187", start: 0, end: 188, status: http.StatusPartialContent},
		{name: "middle", range_: "bytes=188-375", start: 188, end: 376, status: http.StatusPartialContent},
		{name: "end", range_: "bytes=-188", start: len(segment) - 188, end: len(segment), status: http.StatusPartialContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := bytePathChildRequest(t, fixture.item.ID, second.PlaySessionId, second.MediaSources[0].Id, childID, test.range_)
			response := httptest.NewRecorder()
			fixture.handler.handleStream(response, request)
			if response.Code != test.status || response.Header().Get("Content-Type") != "video/mp2t" || !bytes.Equal(response.Body.Bytes(), segment[test.start:test.end]) {
				t.Fatalf("child status=%d want=%d type=%q bytes=%d wantBytes=%d", response.Code, test.status, response.Header().Get("Content-Type"), response.Body.Len(), test.end-test.start)
			}
		})
	}
}

func openBytePathPlayback(t *testing.T, fixture *playbackFixture) PlaybackInfoResponse {
	t.Helper()
	response := fixture.playbackInfo(http.MethodPost, `{"DeviceProfile":{"TranscodingProfiles":[{"Container":"ts","VideoCodec":"h264","AudioCodec":"aac","Protocol":"hls","Type":"Video"}]}}`)
	var result PlaybackInfoResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK || len(result.MediaSources) != 1 || result.MediaSources[0].TranscodingUrl == "" {
		t.Fatalf("open byte-path playback status=%d result=%+v err=%v body=%s", response.Code, result, err, response.Body.String())
	}
	return result
}

func bytePathChildRequest(t *testing.T, itemID, playID, mediaID, childID, byteRange string) *http.Request {
	t.Helper()
	values := url.Values{"MediaSourceId": {mediaID}}
	request := httptest.NewRequest(http.MethodGet, "/Videos/"+itemID+"/hls1/"+playID+"/"+childID+".ts?"+values.Encode(), nil)
	request.SetPathValue("id", itemID)
	request.SetPathValue("playlistId", playID)
	request.SetPathValue("segmentId", childID)
	request.SetPathValue("container", "ts")
	if byteRange != "" {
		request.Header.Set("Range", byteRange)
	}
	return request
}

func generatedTransportStreamFixture(t *testing.T) []byte {
	t.Helper()
	if os.Getenv("RIVUNE_TEST_EXTERNAL_MEDIA") != "1" {
		t.Skip("set RIVUNE_TEST_EXTERNAL_MEDIA=1 to run FFmpeg gateway byte-path tests")
	}
	ffmpegPath := strings.TrimSpace(os.Getenv("RIVUNE_FFMPEG_PATH"))
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	outputPath := filepath.Join(t.TempDir(), "gateway.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, ffmpegPath,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=160x90:rate=10:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000:duration=1",
		"-map", "0:v:0", "-map", "1:a:0",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "10",
		"-c:a", "aac", "-b:a", "96k", "-shortest", "-f", "mpegts", outputPath,
	)
	diagnostic, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generate local MPEG-TS gateway fixture: %v: %s", err, diagnostic)
	}
	fixture, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read local MPEG-TS gateway fixture: %v", err)
	}
	if len(fixture) < 4*188 || fixture[0] != 0x47 {
		t.Fatalf("generated MPEG-TS gateway fixture is invalid: bytes=%d", len(fixture))
	}
	return fixture
}
