package jellyfin

import (
	"bytes"
	"context"
	"errors"
	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/watchstate"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	segmentEpisodeID = "00000000-0000-4000-8000-000000000701"
	segmentSeriesID  = "00000000-0000-4000-8000-000000000700"
)

type mediaSegmentReaderStub struct {
	result    playback.MarkerList
	err       error
	calls     int
	principal auth.Principal
	input     playback.MarkerInput
}

func (stub *mediaSegmentReaderStub) Markers(_ context.Context, principal auth.Principal, input playback.MarkerInput) (playback.MarkerList, error) {
	stub.calls++
	stub.principal = principal
	stub.input = input
	return stub.result, stub.err
}

func TestMediaSegmentsProjectsAuthorizedRealMarkersWithTicksAndStableIDs(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	markerReader := &mediaSegmentReaderStub{result: playback.MarkerList{Markers: []playback.Marker{
		{Type: playback.MarkerTypeIntro, StartSeconds: 10.25, EndSeconds: 70.5},
		{Type: playback.MarkerTypeRecap, StartSeconds: 1, EndSeconds: 5},
		{Type: playback.MarkerTypeOutro, StartSeconds: 120, EndSeconds: 130},
	}}}
	handler.mediaSegments = markerReader
	setSegmentEpisodeCatalog(reader)

	request := authenticatedCatalogRequest(t, token, "/MediaSegments/"+segmentEpisodeID+"?includeSegmentTypes=Intro,Outro")
	request.SetPathValue("itemId", segmentEpisodeID)
	response := httptest.NewRecorder()
	handler.handleMediaSegments(response, request)

	var result QueryResult[MediaSegmentDto]
	decodeCatalogResponse(t, response, &result)
	if response.Code != http.StatusOK || result.StartIndex != 0 || result.TotalRecordCount != 2 || len(result.Items) != 2 {
		t.Fatalf("unexpected media segments: status=%d result=%+v", response.Code, result)
	}
	if result.Items[0].Type != "Intro" || result.Items[0].ItemId != segmentEpisodeID || result.Items[0].StartTicks != 102_500_000 || result.Items[0].EndTicks != 705_000_000 || !validCompatUUID(result.Items[0].Id) {
		t.Fatalf("intro segment projection is invalid: %+v", result.Items[0])
	}
	if result.Items[1].Type != "Outro" || result.Items[1].StartTicks != 1_200_000_000 || result.Items[1].EndTicks != 1_300_000_000 || !validCompatUUID(result.Items[1].Id) || result.Items[1].Id == result.Items[0].Id {
		t.Fatalf("outro segment projection is invalid: %+v", result.Items[1])
	}
	if markerReader.calls != 1 || markerReader.input.IMDBID != "tt1234567" || markerReader.input.Season != 1 || markerReader.input.Episode != 2 ||
		!markerReader.input.IncludeIntro || !markerReader.input.IncludeRecap || !markerReader.input.IncludeOutro || markerReader.principal.ActiveProfileID == nil {
		t.Fatalf("marker lookup did not use authorized catalog coordinates: calls=%d input=%+v principal=%+v", markerReader.calls, markerReader.input, markerReader.principal)
	}
	for _, secret := range []string{"provider.invalid", "private-addon-id", "upstream-secret"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("media segment response leaked %q: %s", secret, response.Body.String())
		}
	}
}

func TestMediaSegmentsAcceptsRepeatedBracketedArrayQuery(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	markers := &mediaSegmentReaderStub{result: playback.MarkerList{Markers: []playback.Marker{
		{Type: playback.MarkerTypeIntro, StartSeconds: 10, EndSeconds: 20},
		{Type: playback.MarkerTypeRecap, StartSeconds: 1, EndSeconds: 5},
		{Type: playback.MarkerTypeOutro, StartSeconds: 90, EndSeconds: 100},
	}}}
	handler.mediaSegments = markers
	setSegmentEpisodeCatalog(reader)

	request := authenticatedCatalogRequest(t, token, "/MediaSegments/"+segmentEpisodeID+"?includeSegmentTypes%5B%5D=Intro&includeSegmentTypes%5B%5D=Outro")
	request.SetPathValue("itemId", segmentEpisodeID)
	response := httptest.NewRecorder()
	handler.handleMediaSegments(response, request)

	var result QueryResult[MediaSegmentDto]
	decodeCatalogResponse(t, response, &result)
	if response.Code != http.StatusOK || result.TotalRecordCount != 2 || len(result.Items) != 2 || result.Items[0].Type != "Intro" || result.Items[1].Type != "Outro" || markers.calls != 1 {
		t.Fatalf("bracketed media segment query status=%d result=%+v calls=%d", response.Code, result, markers.calls)
	}
}

func TestMediaSegmentsDistinguishesKnownEmptyFilterFromUnavailableData(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	markerReader := &mediaSegmentReaderStub{result: playback.MarkerList{Markers: []playback.Marker{{Type: playback.MarkerTypeIntro, StartSeconds: 4, EndSeconds: 12}}}}
	handler.mediaSegments = markerReader
	setSegmentEpisodeCatalog(reader)

	knownEmpty := authenticatedCatalogRequest(t, token, "/MediaSegments/"+segmentEpisodeID+"?includeSegmentTypes=Outro")
	knownEmpty.SetPathValue("itemId", segmentEpisodeID)
	knownEmptyResponse := httptest.NewRecorder()
	handler.handleMediaSegments(knownEmptyResponse, knownEmpty)
	var empty QueryResult[MediaSegmentDto]
	decodeCatalogResponse(t, knownEmptyResponse, &empty)
	if knownEmptyResponse.Code != http.StatusOK || empty.Items == nil || len(empty.Items) != 0 || empty.TotalRecordCount != 0 {
		t.Fatalf("known empty filter result is not a typed success: status=%d result=%+v", knownEmptyResponse.Code, empty)
	}

	markerReader.result = playback.MarkerList{Markers: []playback.Marker{}}
	unavailable := authenticatedCatalogRequest(t, token, "/MediaSegments/"+segmentEpisodeID)
	unavailable.SetPathValue("itemId", segmentEpisodeID)
	unavailableResponse := httptest.NewRecorder()
	handler.handleMediaSegments(unavailableResponse, unavailable)
	if unavailableResponse.Code != http.StatusNotFound || !strings.Contains(unavailableResponse.Body.String(), "MediaSegmentsUnavailable") {
		t.Fatalf("absent marker data looked like known empty data: status=%d body=%s", unavailableResponse.Code, unavailableResponse.Body.String())
	}
}

func TestMediaSegmentsRejectsUnauthorizedMalformedUnsupportedAndInvalidMarkers(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		pathID     string
		authorized bool
		configure  func(*catalogHTTPReader, *mediaSegmentReaderStub)
		wantStatus int
		wantCalls  int
	}{
		{name: "unauthorized", target: "/MediaSegments/" + segmentEpisodeID, pathID: segmentEpisodeID, wantStatus: http.StatusUnauthorized},
		{name: "malformed type", target: "/MediaSegments/" + segmentEpisodeID + "?includeSegmentTypes=Intro,NotAType", pathID: segmentEpisodeID, authorized: true, wantStatus: http.StatusBadRequest},
		{name: "unknown query", target: "/MediaSegments/" + segmentEpisodeID + "?StartIndex=0", pathID: segmentEpisodeID, authorized: true, wantStatus: http.StatusBadRequest},
		{name: "malformed path", target: "/MediaSegments/not-a-uuid", pathID: "not-a-uuid", authorized: true, wantStatus: http.StatusNotFound},
		{name: "unsupported movie", target: "/MediaSegments/00000000-0000-4000-8000-000000000702", pathID: "00000000-0000-4000-8000-000000000702", authorized: true, configure: func(reader *catalogHTTPReader, _ *mediaSegmentReaderStub) {
			reader.title = watchstate.CatalogTitle{ID: "00000000-0000-4000-8000-000000000702", MediaType: "movie"}
		}, wantStatus: http.StatusNotFound},
		{name: "out of range marker", target: "/MediaSegments/" + segmentEpisodeID, pathID: segmentEpisodeID, authorized: true, configure: func(reader *catalogHTTPReader, markers *mediaSegmentReaderStub) {
			setSegmentEpisodeCatalog(reader)
			markers.result = playback.MarkerList{Markers: []playback.Marker{{Type: playback.MarkerTypeIntro, StartSeconds: 1, EndSeconds: maximumMarkerSeconds + 1}}}
		}, wantStatus: http.StatusNotFound, wantCalls: 1},
		{name: "revoked catalog item", target: "/MediaSegments/" + segmentEpisodeID, pathID: segmentEpisodeID, authorized: true, configure: func(reader *catalogHTTPReader, _ *mediaSegmentReaderStub) {
			reader.titleErr = watchstate.ErrNotFound
		}, wantStatus: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, reader, token := newCatalogHTTPHandler(t)
			markers := &mediaSegmentReaderStub{}
			handler.mediaSegments = markers
			if test.configure != nil {
				test.configure(reader, markers)
			}
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			if test.authorized {
				request.Header.Set("X-Emby-Token", token)
			}
			request.SetPathValue("itemId", test.pathID)
			response := httptest.NewRecorder()
			handler.handleMediaSegments(response, request)
			if response.Code != test.wantStatus || markers.calls != test.wantCalls {
				t.Fatalf("status=%d marker calls=%d body=%s", response.Code, markers.calls, response.Body.String())
			}
		})
	}
}

func TestMediaSegmentServiceErrorsFailClosedWithoutDetails(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	setSegmentEpisodeCatalog(reader)
	secret := "https://provider.invalid/segments?token=upstream-secret"
	markers := &mediaSegmentReaderStub{err: errors.New(secret)}
	handler.mediaSegments = markers
	request := authenticatedCatalogRequest(t, token, "/MediaSegments/"+segmentEpisodeID)
	request.SetPathValue("itemId", segmentEpisodeID)
	response := httptest.NewRecorder()
	handler.handleMediaSegments(response, request)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "provider.invalid") || strings.Contains(response.Body.String(), "upstream-secret") {
		t.Fatalf("segment failure leaked provider details: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestTrickplayImageIsAuthorizedValidatedAndUnavailableWithoutGenerator(t *testing.T) {
	movieID := "00000000-0000-4000-8000-000000000703"
	tests := []struct {
		name       string
		target     string
		itemID     string
		width      string
		index      string
		authorized bool
		wantStatus int
		wantCode   string
		wantReads  int
	}{
		{name: "unavailable", target: "/Videos/" + movieID + "/Trickplay/320/0.jpg", itemID: movieID, width: "320", index: "0", authorized: true, wantStatus: http.StatusNotFound, wantCode: "TrickplayUnavailable", wantReads: 1},
		{name: "unauthorized", target: "/Videos/" + movieID + "/Trickplay/320/0.jpg", itemID: movieID, width: "320", index: "0", wantStatus: http.StatusUnauthorized},
		{name: "malformed media source", target: "/Videos/" + movieID + "/Trickplay/320/0.jpg?mediaSourceId=provider-secret", itemID: movieID, width: "320", index: "0", authorized: true, wantStatus: http.StatusBadRequest},
		{name: "unknown query", target: "/Videos/" + movieID + "/Trickplay/320/0.jpg?quality=90", itemID: movieID, width: "320", index: "0", authorized: true, wantStatus: http.StatusBadRequest},
		{name: "width out of range", target: "/Videos/" + movieID + "/Trickplay/1025/0.jpg", itemID: movieID, width: "1025", index: "0", authorized: true, wantStatus: http.StatusNotFound},
		{name: "index out of range", target: "/Videos/" + movieID + "/Trickplay/320/1000001.jpg", itemID: movieID, width: "320", index: "1000001", authorized: true, wantStatus: http.StatusNotFound},
		{name: "noncanonical index", target: "/Videos/" + movieID + "/Trickplay/320/00.jpg", itemID: movieID, width: "320", index: "00", authorized: true, wantStatus: http.StatusNotFound},
		{name: "alternate media source unavailable", target: "/Videos/" + movieID + "/Trickplay/320/0.jpg?mediaSourceId=00000000-0000-4000-8000-000000000799", itemID: movieID, width: "320", index: "0", authorized: true, wantStatus: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, reader, token := newCatalogHTTPHandler(t)
			reader.title = watchstate.CatalogTitle{ID: movieID, MediaType: "movie"}
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			if test.authorized {
				request.Header.Set("X-Emby-Token", token)
			}
			request.SetPathValue("itemId", test.itemID)
			request.SetPathValue("width", test.width)
			request.SetPathValue("index", test.index)
			response := httptest.NewRecorder()
			handler.handleTrickplayImage(response, request)
			if response.Code != test.wantStatus || len(reader.titleIDs) != test.wantReads || test.wantCode != "" && !strings.Contains(response.Body.String(), test.wantCode) {
				t.Fatalf("status=%d reads=%v body=%s", response.Code, reader.titleIDs, response.Body.String())
			}
		})
	}
}

func TestTrickplayRouteMatcherEnforcesJPEGSuffixAndBounds(t *testing.T) {
	definition := RouteSpec{Route: RouteTrickplayImage, Method: http.MethodGet, Pattern: "/Videos/{itemId}/Trickplay/{width}/{index}.jpg"}
	valid := "/Videos/00000000-0000-4000-8000-000000000703/Trickplay/320/7.jpg"
	values, ok := matchRoutePath(definition, valid)
	if !ok || values["width"] != "320" || values["index"] != "7" {
		t.Fatalf("valid trickplay route did not match: ok=%v values=%v", ok, values)
	}
	for _, path := range []string{
		"/Videos/00000000-0000-4000-8000-000000000703/Trickplay/0/7.jpg",
		"/Videos/00000000-0000-4000-8000-000000000703/Trickplay/1025/7.jpg",
		"/Videos/00000000-0000-4000-8000-000000000703/Trickplay/320/-1.jpg",
		"/Videos/00000000-0000-4000-8000-000000000703/Trickplay/320/1000001.jpg",
		"/Videos/00000000-0000-4000-8000-000000000703/Trickplay/320/7.png",
	} {
		if _, matched := matchRoutePath(definition, path); matched {
			t.Fatalf("invalid trickplay path matched: %s", path)
		}
	}
}

type trickplayDeliveryHTTPStub struct {
	*fakeCompatPlaybackDelivery
	image     playback.TrickplayImage
	err       error
	calls     int
	principal auth.Principal
	input     playback.TrickplayInput
	generate  func(context.Context, playback.TrickplayInput) (playback.TrickplayImage, error)
}

func (*trickplayDeliveryHTTPStub) TrickplayAvailable() bool {
	return true
}

func (delivery *trickplayDeliveryHTTPStub) Trickplay(ctx context.Context, principal auth.Principal, input playback.TrickplayInput) (playback.TrickplayImage, error) {
	delivery.calls++
	delivery.principal = principal
	delivery.input = input
	if delivery.generate != nil {
		return delivery.generate(ctx, input)
	}
	return delivery.image, delivery.err
}

func TestTrickplayImageServesAuthenticatedJPEGRangesAndHEAD(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	movieID := "00000000-0000-4000-8000-000000000703"
	reader.title = watchstate.CatalogTitle{ID: movieID, MediaType: "movie", ResourceID: "tt1234567"}
	var encoded bytes.Buffer
	canvas := image.NewRGBA(image.Rect(0, 0, 32, 18))
	for y := range canvas.Bounds().Dy() {
		for x := range canvas.Bounds().Dx() {
			canvas.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}
	if err := jpeg.Encode(&encoded, canvas, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	native := &fakeCompatPlaybackDelivery{sources: playback.SourceList{Sources: []playback.SourceOption{{
		SourceRef: "opaque-trickplay-source-reference", Protocol: "http", Container: "mp4", ExpiresAt: time.Now().Add(time.Hour),
	}}}}
	delivery := &trickplayDeliveryHTTPStub{
		fakeCompatPlaybackDelivery: native,
		image:                      playback.TrickplayImage{JPEG: encoded.Bytes(), LastModified: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)},
	}
	handler.playback = delivery

	request := authenticatedCatalogRequest(t, token, "/Videos/"+movieID+"/Trickplay/320/0.jpg?mediaSourceId="+movieID)
	request.SetPathValue("itemId", movieID)
	request.SetPathValue("width", "320")
	request.SetPathValue("index", "0")
	response := httptest.NewRecorder()
	handler.handleTrickplayImage(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/jpeg" ||
		response.Header().Get("ETag") == "" || !bytes.Equal(response.Body.Bytes(), encoded.Bytes()) {
		t.Fatalf("JPEG response status=%d headers=%v bytes=%d", response.Code, response.Header(), response.Body.Len())
	}
	if delivery.calls != 1 || delivery.input.SourceRef != "opaque-trickplay-source-reference" ||
		delivery.input.TitleID != movieID || delivery.input.Width != 320 || delivery.input.Index != 0 ||
		delivery.principal.ActiveProfileID == nil || *delivery.principal.ActiveProfileID != catalogTestProfileID ||
		native.pinCalls != 1 || native.unpinCalls != 1 {
		t.Fatalf("trickplay binding calls=%d input=%+v principal=%+v pins=%d/%d", delivery.calls, delivery.input, delivery.principal, native.pinCalls, native.unpinCalls)
	}

	head := authenticatedCatalogRequest(t, token, "/Videos/"+movieID+"/Trickplay/320/0.jpg")
	head.Method = http.MethodHead
	head.Header.Set("Range", "bytes=0-9")
	head.SetPathValue("itemId", movieID)
	head.SetPathValue("width", "320")
	head.SetPathValue("index", "0")
	headResponse := httptest.NewRecorder()
	handler.handleTrickplayImage(headResponse, head)
	if headResponse.Code != http.StatusPartialContent || headResponse.Body.Len() != 0 ||
		headResponse.Header().Get("Content-Range") != "bytes 0-9/"+strconv.Itoa(encoded.Len()) ||
		headResponse.Header().Get("Content-Length") != "10" {
		t.Fatalf("HEAD range status=%d headers=%v body=%q", headResponse.Code, headResponse.Header(), headResponse.Body.String())
	}

	delivery.err = playback.ErrSourceReferenceExpired
	stale := authenticatedCatalogRequest(t, token, "/Videos/"+movieID+"/Trickplay/320/0.jpg")
	stale.SetPathValue("itemId", movieID)
	stale.SetPathValue("width", "320")
	stale.SetPathValue("index", "0")
	staleResponse := httptest.NewRecorder()
	handler.handleTrickplayImage(staleResponse, stale)
	if staleResponse.Code != http.StatusNotFound || strings.Contains(staleResponse.Body.String(), delivery.input.SourceRef) {
		t.Fatalf("stale source status=%d body=%s", staleResponse.Code, staleResponse.Body.String())
	}
}

func TestTrickplayMetadataRequiresCanonicalPlaybackIdentityWithoutEnumeration(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	movieID := "00000000-0000-4000-8000-000000000705"
	reader.title = watchstate.CatalogTitle{
		ID: movieID, MediaType: "movie",
		Progress: &watchstate.CatalogProgress{DurationSeconds: 120},
	}
	native := &fakeCompatPlaybackDelivery{sources: playback.SourceList{Sources: []playback.SourceOption{{
		SourceRef: "must-not-be-enumerated", Protocol: "http", Container: "mp4", ExpiresAt: time.Now().Add(time.Hour),
	}}}}
	delivery := &trickplayDeliveryHTTPStub{fakeCompatPlaybackDelivery: native}
	handler.playback = delivery
	handler.playSessions = newPlaySessionRegistry(delivery)

	request := authenticatedCatalogRequest(t, token, "/Items/"+movieID+"?Fields=Trickplay&EnableImages=false")
	request.SetPathValue("id", movieID)
	response := httptest.NewRecorder()
	handler.handleItem(response, request)
	var detail BaseItemDto
	decodeCatalogResponse(t, response, &detail)
	if response.Code != http.StatusOK || len(detail.Trickplay) != 0 || native.sourceCalls != 0 || delivery.calls != 0 {
		t.Fatalf("unbound trickplay metadata status=%d trickplay=%+v sources=%d generations=%d", response.Code, detail.Trickplay, native.sourceCalls, delivery.calls)
	}
}

func TestTrickplayMetadataUsesAuthorizedEpisodeIdentityFallbackWithoutSourceEnumeration(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	episodeID := "00000000-0000-4000-8000-000000000706"
	seriesID := "00000000-0000-4000-8000-000000000707"
	season, episode := 1, 2
	reader.titles = map[string]watchstate.CatalogTitle{
		episodeID: {
			ID: episodeID, MediaType: "episode", SeriesID: seriesID,
			ParentOrdinal: &season, Ordinal: &episode,
			Progress: &watchstate.CatalogProgress{DurationSeconds: 120},
		},
		seriesID: {
			ID: seriesID, MediaType: "series", ResourceID: "tt7654321", SourceAddonID: "authorized-series-addon",
		},
	}
	native := &fakeCompatPlaybackDelivery{sources: playback.SourceList{Sources: []playback.SourceOption{{
		SourceRef: "must-not-be-enumerated", Protocol: "http", Container: "mp4", ExpiresAt: time.Now().Add(time.Hour),
	}}}}
	delivery := &trickplayDeliveryHTTPStub{fakeCompatPlaybackDelivery: native}
	handler.playback = delivery
	handler.playSessions = newPlaySessionRegistry(delivery)

	request := authenticatedCatalogRequest(t, token, "/Items/"+episodeID+"?Fields=Trickplay&EnableImages=false")
	request.SetPathValue("id", episodeID)
	response := httptest.NewRecorder()
	handler.handleItem(response, request)
	var detail BaseItemDto
	decodeCatalogResponse(t, response, &detail)
	_, advertised := detail.Trickplay[episodeID][defaultTrickplayWidth]
	if response.Code != http.StatusOK || !advertised || native.sourceCalls != 0 || delivery.calls != 0 ||
		!reflect.DeepEqual(reader.titleIDs, []string{episodeID, seriesID}) {
		t.Fatalf("episode trickplay fallback status=%d trickplay=%+v catalog=%v sources=%d generations=%d", response.Code, detail.Trickplay, reader.titleIDs, native.sourceCalls, delivery.calls)
	}
}

func TestTrickplayObservedRouteGeneratesAndRetrievesRealMediaSheet(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skipf("FFmpeg is unavailable: %v", err)
	}
	workspace := t.TempDir()
	source := filepath.Join(workspace, "source.mp4")
	createContext, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	create := exec.CommandContext(createContext, ffmpegPath,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=2",
		"-t", "2", "-c:v", "mpeg4", "-y", source,
	)
	if output, createErr := create.CombinedOutput(); createErr != nil {
		t.Skipf("create real media fixture: %v: %s", createErr, output)
	}

	handler, reader, token := newCatalogHTTPHandler(t)
	movieID := "00000000-0000-4000-8000-000000000704"
	reader.title = watchstate.CatalogTitle{ID: movieID, MediaType: "movie", ResourceID: "tt1234568", Progress: &watchstate.CatalogProgress{DurationSeconds: 2}}
	native := &fakeCompatPlaybackDelivery{sources: playback.SourceList{Sources: []playback.SourceOption{{
		SourceRef: "opaque-real-media-source-reference", Protocol: "http", Container: "mp4", ExpiresAt: time.Now().Add(time.Hour),
	}}}}
	delivery := &trickplayDeliveryHTTPStub{fakeCompatPlaybackDelivery: native}
	delivery.generate = func(ctx context.Context, input playback.TrickplayInput) (playback.TrickplayImage, error) {
		height := playback.TrickplayThumbnailHeight(input.Width)
		filter := "select='isnan(prev_selected_t)+gte(t-prev_selected_t," + strconv.Itoa(playback.TrickplayIntervalSeconds) + ")',scale=" +
			strconv.Itoa(input.Width) + ":" + strconv.Itoa(height) + ":force_original_aspect_ratio=decrease,pad=" +
			strconv.Itoa(input.Width) + ":" + strconv.Itoa(height) + ":(ow-iw)/2:(oh-ih)/2,tile=" +
			strconv.Itoa(playback.TrickplayTileColumns) + "x" + strconv.Itoa(playback.TrickplayTileRows) +
			":nb_frames=" + strconv.Itoa(playback.TrickplayTileColumns*playback.TrickplayTileRows) + ":padding=0:margin=0,format=yuvj420p"
		command := exec.CommandContext(ctx, ffmpegPath,
			"-nostdin", "-hide_banner", "-loglevel", "error", "-i", source, "-map", "0:v:0", "-an", "-sn", "-dn",
			"-vf", filter, "-frames:v", "1", "-c:v", "mjpeg", "-pix_fmt", "yuvj420p", "-strict", "unofficial", "-q:v", "4", "-f", "image2pipe", "pipe:1",
		)
		contents, commandErr := command.Output()
		return playback.TrickplayImage{JPEG: contents, LastModified: time.Now().UTC()}, commandErr
	}
	handler.playback = delivery
	handler.playSessions = newPlaySessionRegistry(delivery)

	detailRequest := authenticatedCatalogRequest(t, token, "/Items/"+movieID+"?Fields=Trickplay&EnableImages=false")
	detailRequest.SetPathValue("id", movieID)
	detailResponse := httptest.NewRecorder()
	handler.handleItem(detailResponse, detailRequest)
	var detail BaseItemDto
	decodeCatalogResponse(t, detailResponse, &detail)
	resolutions := detail.Trickplay[movieID]
	metadata, advertised := resolutions[defaultTrickplayWidth]
	if detailResponse.Code != http.StatusOK || !advertised || metadata.Width != defaultTrickplayWidth ||
		metadata.Height != playback.TrickplayThumbnailHeight(defaultTrickplayWidth) ||
		metadata.TileWidth != playback.TrickplayTileColumns || metadata.TileHeight != playback.TrickplayTileRows ||
		metadata.ThumbnailCount != 1 || metadata.Interval != playback.TrickplayIntervalSeconds*1000 {
		t.Fatalf("dishonest trickplay metadata status=%d trickplay=%+v", detailResponse.Code, detail.Trickplay)
	}
	for _, forbidden := range []string{source, "opaque-real-media-source-reference"} {
		if strings.Contains(detailResponse.Body.String(), forbidden) {
			t.Fatalf("trickplay metadata exposed private source material %q", forbidden)
		}
	}
	if native.sourceCalls != 0 || native.pinCalls != 0 || native.unpinCalls != 0 || delivery.calls != 0 {
		t.Fatalf("detail metadata reopened playback sources: sources=%d pins=%d/%d generations=%d", native.sourceCalls, native.pinCalls, native.unpinCalls, delivery.calls)
	}

	target := "/Videos/" + movieID + "/Trickplay/" + strconv.Itoa(metadata.Width) + "/0.jpg?MediaSourceId=" + movieID
	request := authenticatedCatalogRequest(t, token, target)
	request.SetPathValue("itemId", movieID)
	request.SetPathValue("width", strconv.Itoa(metadata.Width))
	request.SetPathValue("index", "0")
	response := httptest.NewRecorder()
	handler.handleTrickplayImage(response, request)
	config, decodeErr := jpeg.DecodeConfig(bytes.NewReader(response.Body.Bytes()))
	if response.Code != http.StatusOK || decodeErr != nil ||
		config.Width != metadata.Width*metadata.TileWidth || config.Height != metadata.Height*metadata.TileHeight {
		t.Fatalf("metadata-derived real-media trickplay status=%d metadata=%+v config=%+v decode=%v body=%q", response.Code, metadata, config, decodeErr, response.Body.String())
	}
	if native.sourceCalls != 1 || native.pinCalls != 1 || native.unpinCalls != 1 || delivery.calls != 1 {
		t.Fatalf("metadata-derived GET repeated source work: sources=%d pins=%d/%d generations=%d", native.sourceCalls, native.pinCalls, native.unpinCalls, delivery.calls)
	}
	head := authenticatedCatalogRequest(t, token, target)
	head.Method = http.MethodHead
	head.SetPathValue("itemId", movieID)
	head.SetPathValue("width", strconv.Itoa(metadata.Width))
	head.SetPathValue("index", "0")
	headResponse := httptest.NewRecorder()
	handler.handleTrickplayImage(headResponse, head)
	if headResponse.Code != http.StatusOK || headResponse.Body.Len() != 0 ||
		headResponse.Header().Get("Content-Length") != strconv.Itoa(response.Body.Len()) ||
		headResponse.Header().Get("ETag") != response.Header().Get("ETag") {
		t.Fatalf("metadata-derived HEAD status=%d headers=%v body=%q", headResponse.Code, headResponse.Header(), headResponse.Body.String())
	}
	if native.sourceCalls != 2 || native.pinCalls != 2 || native.unpinCalls != 2 || delivery.calls != 2 {
		t.Fatalf("metadata-derived HEAD repeated source work: sources=%d pins=%d/%d generations=%d", native.sourceCalls, native.pinCalls, native.unpinCalls, delivery.calls)
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(source) {
		t.Fatalf("route generation retained temporary artifacts: %v", entries)
	}
}

func setSegmentEpisodeCatalog(reader *catalogHTTPReader) {
	season, episode := 1, 2
	reader.titles = map[string]watchstate.CatalogTitle{
		segmentEpisodeID: {
			ID: segmentEpisodeID, MediaType: "episode", SeriesID: segmentSeriesID,
			ParentOrdinal: &season, Ordinal: &episode,
			ProviderIDs: map[string]string{"url": "https://provider.invalid/episode?token=upstream-secret"},
		},
		segmentSeriesID: {
			ID: segmentSeriesID, MediaType: "series",
			ProviderIDs: map[string]string{"imdb": "tt1234567", "addon": "private-addon-id", "url": "https://provider.invalid/series?token=upstream-secret"},
		},
	}
}
