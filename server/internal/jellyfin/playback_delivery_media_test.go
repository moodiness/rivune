package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/moodiness/rivune/server/internal/auth"
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

type seekPathCompatDelivery struct {
	*fakeCompatPlaybackDelivery
	activeJob bool
	modified  time.Time
}

func (delivery *seekPathCompatDelivery) Serve(response http.ResponseWriter, request *http.Request, handle playback.DeliveryHandle) error {
	if !handle.Valid() {
		return playback.ErrSessionNotFound
	}
	delivery.mu.Lock()
	delivery.serveCalls++
	delivery.servedPath = request.URL.Path
	delivery.servedRange = request.Header.Get("Range")
	delivery.servedChildID = request.URL.Query().Get("RivuneChildId")
	delivery.mu.Unlock()

	values := request.URL.Query()
	switch {
	case strings.HasSuffix(request.URL.Path, "/master.m3u8"):
		delivery.mu.Lock()
		delivery.activeJob = true
		delivery.mu.Unlock()
		values.Set("RivuneChildId", "main-capability-0000000000000001")
		payload := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-STREAM-INF:BANDWIDTH=1256000,CODECS=\"avc1,mp4a\"\n/Videos/" + request.PathValue("id") + "/main.m3u8?" + values.Encode() + "\n"
		writeSeekPathPlaylist(response, payload)
		return nil
	case strings.HasSuffix(request.URL.Path, "/main.m3u8"):
		var payload strings.Builder
		payload.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-TARGETDURATION:3\n#EXT-X-MEDIA-SEQUENCE:0\n")
		for index := range 4 {
			childID := fmt.Sprintf("hlsseek-%06d-AAAAAAAAAAAAAAAAAAAAAA", index)
			childValues := url.Values{
				"MediaSourceId": {values.Get("MediaSourceId")}, "PlaySessionId": {values.Get("PlaySessionId")},
				"RivuneChildId": {childID}, "api_key": {values.Get("PlaySessionId")},
			}
			_, _ = fmt.Fprintf(&payload, "#EXTINF:3.000000,\n/Videos/%s/hls1/%s/%s.ts?%s\n", request.PathValue("id"), values.Get("PlaySessionId"), childID, childValues.Encode())
		}
		payload.WriteString("#EXT-X-ENDLIST\n")
		writeSeekPathPlaylist(response, payload.String())
		return nil
	case request.URL.Query().Get("RivuneChildId") != "":
		segment := bytes.Repeat([]byte{0x47}, 2*188)
		http.ServeContent(response, request, request.URL.Query().Get("RivuneChildId")+".ts", delivery.modified, bytes.NewReader(segment))
		return nil
	default:
		return playback.ErrSessionNotFound
	}
}

func (delivery *seekPathCompatDelivery) Close(ctx context.Context, principal auth.Principal, handle playback.DeliveryHandle) error {
	delivery.mu.Lock()
	delivery.activeJob = false
	delivery.mu.Unlock()
	return delivery.fakeCompatPlaybackDelivery.Close(ctx, principal, handle)
}

func writeSeekPathPlaylist(response http.ResponseWriter, payload string) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	response.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte(payload))
}

func (delivery *seekPathCompatDelivery) hasActiveJob() bool {
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	return delivery.activeJob
}

func TestPlaybackAdapterTraversesStatelessSeekChildrenOutOfOrderAndTearsDown(t *testing.T) {
	fixture := newPlaybackFixture(t)
	delivery := &seekPathCompatDelivery{
		fakeCompatPlaybackDelivery: fixture.delivery,
		modified:                   time.Unix(1_700_000_000, 0).UTC(),
	}
	fixture.handler.playback = delivery
	fixture.handler.playSessions = newPlaySessionRegistry(delivery)
	fixture.handler.playSessions.now = func() time.Time { return fixture.now }
	fixture.handler.watchstate = newMemoryWatchstate()
	fixture.authentication.sessions = map[string]AuthenticatedSession{fixture.token: fixture.authentication.session}
	delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{
		SourceRef: "seek-provider-source-reference-secret", StableIdentity: "seek-source", Protocol: "http", Container: "mkv", ExpiresAt: fixture.now.Add(time.Hour),
	}}}
	delivery.resolvedSession = playback.Session{SelectedSourceID: "seek-selected", Sources: []playback.Source{{
		ID: "seek-selected", Mode: "transcode", Protocol: "hls", Container: "hls",
		Media: &playback.MediaInspection{Container: "mkv", DurationSeconds: 12,
			VideoTracks: []playback.MediaTrack{{Index: 0, Codec: "hevc", Width: 640, Height: 360}},
			AudioTracks: []playback.MediaTrack{{Index: 1, Codec: "eac3", Channels: 6}}},
	}}}

	info := openBytePathPlayback(t, fixture)
	if delivery.openCalls != 1 || delivery.sourceCalls != 1 {
		t.Fatalf("PlaybackInfo opened sources=%d deliveries=%d, want one each", delivery.sourceCalls, delivery.openCalls)
	}
	master := serveSeekPathURL(t, fixture, info.MediaSources[0].TranscodingUrl, "")
	masterReferences := seekPathPlaylistReferences(master.Body.String())
	if master.Code != http.StatusOK || len(masterReferences) != 1 {
		t.Fatalf("master status=%d references=%v body=%q", master.Code, masterReferences, master.Body.String())
	}
	media := serveSeekPathURL(t, fixture, masterReferences[0], "")
	segmentReferences := seekPathPlaylistReferences(media.Body.String())
	if media.Code != http.StatusOK || len(segmentReferences) != 4 {
		t.Fatalf("media status=%d references=%v body=%q", media.Code, segmentReferences, media.Body.String())
	}
	far := serveSeekPathURL(t, fixture, segmentReferences[3], "bytes=0-187")
	earlier := serveSeekPathURL(t, fixture, segmentReferences[0], "")
	if far.Code != http.StatusPartialContent || far.Body.Len() != 188 || earlier.Code != http.StatusOK || earlier.Body.Len() != 2*188 {
		t.Fatalf("out-of-order segments far=%d/%d earlier=%d/%d", far.Code, far.Body.Len(), earlier.Code, earlier.Body.Len())
	}
	for _, exposed := range append([]string{info.MediaSources[0].TranscodingUrl, master.Body.String(), media.Body.String()}, segmentReferences...) {
		if strings.Contains(exposed, fixture.token) || strings.Contains(exposed, "seek-provider-source-reference-secret") || strings.Contains(exposed, "provider") {
			t.Fatalf("playback transport exposed native secret: %s", exposed)
		}
	}
	if !strings.Contains(segmentReferences[3], "/hls1/"+info.PlaySessionId+"/hlsseek-000003-") || !strings.Contains(segmentReferences[0], "/hls1/"+info.PlaySessionId+"/hlsseek-000000-") ||
		delivery.openCalls != 1 || delivery.sourceCalls != 1 || !delivery.hasActiveJob() {
		t.Fatalf("seek children lost stateless/session binding: far=%q earlier=%q opens=%d sources=%d active=%t", segmentReferences[3], segmentReferences[0], delivery.openCalls, delivery.sourceCalls, delivery.hasActiveJob())
	}

	stopped := serveStateRequest(fixture.handler.handlePlayingStopped, fixture.token, fmt.Sprintf(`{"ItemId":%q,"PlaySessionId":%q,"MediaSourceId":%q,"PositionTicks":10000000}`, fixture.item.ID, info.PlaySessionId, info.MediaSources[0].Id))
	if stopped.Code != http.StatusNoContent || delivery.closeCalls != 1 || delivery.hasActiveJob() || fixture.handler.playSessions.entries[info.PlaySessionId] != nil {
		t.Fatalf("teardown status=%d closes=%d active=%t registered=%t body=%s", stopped.Code, delivery.closeCalls, delivery.hasActiveJob(), fixture.handler.playSessions.entries[info.PlaySessionId] != nil, stopped.Body.String())
	}
	stale := serveSeekPathURL(t, fixture, segmentReferences[0], "")
	if stale.Code != http.StatusUnauthorized || delivery.serveCalls != 4 {
		t.Fatalf("closed session served stale child: status=%d serves=%d body=%s", stale.Code, delivery.serveCalls, stale.Body.String())
	}
}

func serveSeekPathURL(t *testing.T, fixture *playbackFixture, target, byteRange string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.SetPathValue("id", fixture.item.ID)
	if strings.Contains(request.URL.Path, "/hls1/") {
		parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
		if len(parts) != 5 {
			t.Fatalf("invalid HLS child path %q", request.URL.Path)
		}
		request.SetPathValue("playlistId", parts[3])
		request.SetPathValue("segmentId", strings.TrimSuffix(parts[4], ".ts"))
		request.SetPathValue("container", "ts")
	}
	if byteRange != "" {
		request.Header.Set("Range", byteRange)
	}
	response := httptest.NewRecorder()
	fixture.handler.handleStream(response, request)
	return response
}

func seekPathPlaylistReferences(playlist string) []string {
	var references []string
	for _, line := range strings.Split(playlist, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			references = append(references, line)
		}
	}
	return references
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

func TestLegacyStreamReusesOpenedProcessingNegotiationWithStaleOrMissingMediaSourceID(t *testing.T) {
	fixture := newPlaybackFixture(t)
	delivery := &bytePathCompatDelivery{
		fakeCompatPlaybackDelivery: fixture.delivery,
		segment:                    generatedTransportStreamFixture(t),
		modified:                   time.Unix(1_700_000_000, 0).UTC(),
	}
	fixture.handler.playback = delivery
	fixture.handler.playSessions = newPlaySessionRegistry(delivery)
	fixture.handler.playSessions.now = func() time.Time { return fixture.now }
	fixture.authentication.sessions = map[string]AuthenticatedSession{fixture.token: fixture.authentication.session}
	delivery.sources = playback.SourceList{Sources: []playback.SourceOption{
		{SourceRef: "legacy-hls-source-a", StableIdentity: "legacy-hls-a", Protocol: "http", Container: "mkv", ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "legacy-hls-source-b", StableIdentity: "legacy-hls-b", Protocol: "http", Container: "mkv", ExpiresAt: fixture.now.Add(time.Hour)},
	}}
	delivery.resolvedSession = playback.Session{SelectedSourceID: "native-selected", Sources: []playback.Source{{
		ID: "native-selected", Mode: "transcode", Protocol: "hls", Container: "hls",
		Media: &playback.MediaInspection{Container: "mkv", DurationSeconds: 3,
			VideoTracks: []playback.MediaTrack{{Index: 0, Codec: "h264", Width: 160, Height: 90}},
			AudioTracks: []playback.MediaTrack{{Index: 1, Codec: "aac", Channels: 2}}},
	}}}
	_, initialDescriptors, err := fixture.handler.playSessions.register(context.Background(), fixture.authentication.session, fixture.item.ID, playback.Capabilities{}, true, delivery.sources.Sources)
	if err != nil || len(initialDescriptors) != 2 {
		t.Fatalf("register initial legacy sources descriptors=%+v err=%v", initialDescriptors, err)
	}
	body := `{"MediaSourceId":` + strconv.Quote(initialDescriptors[1].ID) + `,"AudioStreamIndex":1,"SubtitleStreamIndex":-1,"DeviceProfile":{"TranscodingProfiles":[{"Container":"ts","VideoCodec":"h264","AudioCodec":"aac","Protocol":"hls","Type":"Video"}]}}`
	openResponse := fixture.playbackInfo(http.MethodPost, body)
	var info PlaybackInfoResponse
	if err := json.Unmarshal(openResponse.Body.Bytes(), &info); err != nil || openResponse.Code != http.StatusOK ||
		len(info.MediaSources) != 2 || info.MediaSources[0].Id != initialDescriptors[1].ID {
		t.Fatalf("open selected legacy source status=%d result=%+v err=%v body=%s", openResponse.Code, info, err, openResponse.Body.String())
	}
	opensBeforeStream := delivery.openCalls
	_, refreshedDescriptors, err := fixture.handler.playSessions.register(context.Background(), fixture.authentication.session, fixture.item.ID, playback.Capabilities{}, true, delivery.sources.Sources)
	if err != nil || len(refreshedDescriptors) != 2 || refreshedDescriptors[1].ID == initialDescriptors[1].ID {
		t.Fatalf("register refreshed legacy sources descriptors=%+v initial=%+v err=%v", refreshedDescriptors, initialDescriptors, err)
	}
	for _, test := range []struct {
		name   string
		target string
	}{
		{name: "stale media source id", target: "/Videos/" + fixture.item.ID + "/stream?MediaSourceId=" + refreshedDescriptors[1].ID},
		{name: "missing media source id", target: "/Videos/" + fixture.item.ID + "/stream"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			request.SetPathValue("id", fixture.item.ID)
			request.Header.Set("X-Emby-Token", fixture.token)
			request.Header.Set("Range", "bytes=0-187")
			response := httptest.NewRecorder()
			fixture.handler.handleStream(response, request)
			if response.Code != http.StatusOK || response.Header().Get("Location") != "" || response.Header().Get("Content-Type") != "application/vnd.apple.mpegurl" ||
				!bytes.Contains(response.Body.Bytes(), []byte("#EXTINF:3.000")) || delivery.openCalls != opensBeforeStream {
				t.Fatalf("legacy stream status=%d location=%q type=%q opens=%d/%d body=%q", response.Code, response.Header().Get("Location"), response.Header().Get("Content-Type"), delivery.openCalls, opensBeforeStream, response.Body.String())
			}
		})
	}
}

func TestReuseNegotiatedCandidateDoesNotCrossAmbiguousStableIdentity(t *testing.T) {
	fixture := newPlaybackFixture(t)
	options := []playback.SourceOption{
		{SourceRef: "ambiguous-source-a", StableIdentity: "duplicate-stable-identity", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "ambiguous-source-b", StableIdentity: "duplicate-stable-identity", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
	}
	initialPlayID, initialDescriptors, err := fixture.handler.playSessions.register(
		context.Background(), fixture.authentication.session, fixture.item.ID, playback.Capabilities{}, true, options,
	)
	if err != nil || len(initialDescriptors) != 2 {
		t.Fatalf("register initial ambiguous sources descriptors=%+v err=%v", initialDescriptors, err)
	}
	if _, _, _, err := fixture.handler.playSessions.openAndTouch(
		context.Background(), fixture.authentication.session, fixture.item.ID, initialPlayID, initialDescriptors[1].ID, 0,
	); err != nil {
		t.Fatalf("open initial ambiguous source: %v", err)
	}
	refreshedPlayID, refreshedDescriptors, err := fixture.handler.playSessions.register(
		context.Background(), fixture.authentication.session, fixture.item.ID, playback.Capabilities{}, true, options,
	)
	if err != nil || len(refreshedDescriptors) != 2 || refreshedDescriptors[1].ID == initialDescriptors[1].ID {
		t.Fatalf("register refreshed ambiguous sources descriptors=%+v initial=%+v err=%v", refreshedDescriptors, initialDescriptors, err)
	}
	playID, mediaID, _, _, reused := fixture.handler.playSessions.reuseNegotiatedCandidate(
		fixture.authentication.session, fixture.item.ID, refreshedDescriptors[1].ID, 0, true,
	)
	if !reused || playID != refreshedPlayID || mediaID != refreshedDescriptors[1].ID {
		t.Fatalf("ambiguous identity reused play=%q media=%q want play=%q media=%q reused=%t", playID, mediaID, refreshedPlayID, refreshedDescriptors[1].ID, reused)
	}
}

func TestReuseNegotiatedCandidatePrefersLatestEntryFallbackOverOlderStableSource(t *testing.T) {
	fixture := newPlaybackFixture(t)
	options := []playback.SourceOption{
		{SourceRef: "source-a", StableIdentity: "stable-a", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "source-b", StableIdentity: "stable-b", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
	}
	initialPlayID, initialDescriptors, err := fixture.handler.playSessions.register(
		context.Background(), fixture.authentication.session, fixture.item.ID, playback.Capabilities{}, true, options,
	)
	if err != nil || len(initialDescriptors) != 2 {
		t.Fatalf("register initial sources descriptors=%+v err=%v", initialDescriptors, err)
	}
	if _, _, _, err := fixture.handler.playSessions.openAndTouch(
		context.Background(), fixture.authentication.session, fixture.item.ID, initialPlayID, initialDescriptors[0].ID, 0,
	); err != nil {
		t.Fatalf("open initial source: %v", err)
	}
	refreshedPlayID, refreshedDescriptors, err := fixture.handler.playSessions.register(
		context.Background(), fixture.authentication.session, fixture.item.ID, playback.Capabilities{}, true, options,
	)
	if err != nil || len(refreshedDescriptors) != 2 {
		t.Fatalf("register refreshed sources descriptors=%+v err=%v", refreshedDescriptors, err)
	}
	if _, _, _, err := fixture.handler.playSessions.openAndTouch(
		context.Background(), fixture.authentication.session, fixture.item.ID, refreshedPlayID, refreshedDescriptors[1].ID, 0,
	); err != nil {
		t.Fatalf("open refreshed fallback: %v", err)
	}

	playID, mediaID, _, _, reused := fixture.handler.playSessions.reuseNegotiatedCandidate(
		fixture.authentication.session, fixture.item.ID, refreshedDescriptors[0].ID, 0, true,
	)
	if !reused || playID != refreshedPlayID || mediaID != refreshedDescriptors[1].ID {
		t.Fatalf("reused play=%q media=%q want play=%q media=%q reused=%t", playID, mediaID, refreshedPlayID, refreshedDescriptors[1].ID, reused)
	}
}

func TestReuseNegotiatedCandidateUsesSoleOpenedFallbackAcrossEntries(t *testing.T) {
	fixture := newPlaybackFixture(t)
	options := []playback.SourceOption{
		{SourceRef: "source-a", StableIdentity: "stable-a", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "source-b", StableIdentity: "stable-b", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
	}
	initialPlayID, initialDescriptors, err := fixture.handler.playSessions.register(
		context.Background(), fixture.authentication.session, fixture.item.ID, playback.Capabilities{}, true, options,
	)
	if err != nil || len(initialDescriptors) != 2 {
		t.Fatalf("register initial sources descriptors=%+v err=%v", initialDescriptors, err)
	}
	refreshedPlayID, refreshedDescriptors, err := fixture.handler.playSessions.register(
		context.Background(), fixture.authentication.session, fixture.item.ID, playback.Capabilities{}, true, options,
	)
	if err != nil || len(refreshedDescriptors) != 2 {
		t.Fatalf("register refreshed sources descriptors=%+v err=%v", refreshedDescriptors, err)
	}
	if _, _, _, err := fixture.handler.playSessions.openAndTouch(
		context.Background(), fixture.authentication.session, fixture.item.ID, refreshedPlayID, refreshedDescriptors[0].ID, 900000000,
	); err != nil {
		t.Fatalf("open refreshed fallback: %v", err)
	}

	playID, mediaID, startTimeTicks, _, reused := fixture.handler.playSessions.reuseNegotiatedCandidate(
		fixture.authentication.session, fixture.item.ID, initialDescriptors[1].ID, 0, false,
	)
	if !reused || playID != refreshedPlayID || mediaID != refreshedDescriptors[0].ID || startTimeTicks != 900000000 {
		t.Fatalf("reused play=%q media=%q start=%d want play=%q media=%q start=%d reused=%t initialPlay=%q", playID, mediaID, startTimeTicks, refreshedPlayID, refreshedDescriptors[0].ID, 900000000, reused, initialPlayID)
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
