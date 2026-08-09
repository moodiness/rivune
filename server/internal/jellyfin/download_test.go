package jellyfin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/playback"
)

type downloadByteDelivery struct {
	*fakeCompatPlaybackDelivery
	body                []byte
	contentType         string
	upstreamDisposition string
	upstreamETag        string
	status              int
	serveStarted        chan<- struct{}
	serveGate           <-chan struct{}
}

func (delivery *downloadByteDelivery) Serve(response http.ResponseWriter, request *http.Request, handle playback.DeliveryHandle) error {
	delivery.mu.Lock()
	if !handle.Valid() {
		delivery.mu.Unlock()
		return playback.ErrSessionNotFound
	}
	delivery.serveCalls++
	delivery.servedMethod = request.Method
	delivery.servedPath = request.URL.Path
	delivery.servedRange = request.Header.Get("Range")
	delivery.servedAPIKey = request.URL.Query().Get("api_key")
	for _, name := range []string{"X-Emby-Token", "X-MediaBrowser-Token", "X-Emby-Authorization", "X-MediaBrowser-Authorization", "Authorization"} {
		if len(request.Header.Values(name)) != 0 {
			delivery.servedProfileHeaderCredential = true
		}
	}
	body := append([]byte(nil), delivery.body...)
	contentType := delivery.contentType
	disposition := delivery.upstreamDisposition
	etag := delivery.upstreamETag
	status := delivery.status
	started, gate := delivery.serveStarted, delivery.serveGate
	delivery.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		select {
		case <-gate:
		case <-request.Context().Done():
			return request.Context().Err()
		}
	}
	if contentType != "" {
		response.Header().Set("Content-Type", contentType)
	}
	if disposition != "" {
		response.Header().Set("Content-Disposition", disposition)
	}
	if etag != "" {
		response.Header().Set("ETag", etag)
	}
	if status != 0 && status != http.StatusOK {
		response.Header().Set("Content-Length", strconv.Itoa(len(body)))
		response.WriteHeader(status)
		if request.Method == http.MethodGet {
			_, _ = response.Write(body)
		}
		return nil
	}
	http.ServeContent(response, request, "media.mp4", time.Time{}, bytes.NewReader(body))
	return nil
}

func (delivery *downloadByteDelivery) ServeAsset(response http.ResponseWriter, request *http.Request, handle playback.DeliveryHandle, assetID string) error {
	delivery.mu.Lock()
	delivery.serveAssetCalls++
	delivery.servedAssetID = assetID
	delivery.mu.Unlock()
	return delivery.Serve(response, request, handle)
}

func newDownloadFixture(t *testing.T, body []byte) (*playbackFixture, *downloadByteDelivery) {
	t.Helper()
	fixture := newPlaybackFixture(t)
	fixture.item.Title = "Safe Movie"
	fixture.handler.catalog.(*fakeCompatPlaybackCatalog).item = fixture.item
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{
		SourceRef: "download-source-reference", StableIdentity: "download-source", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour),
	}}}
	delivery := &downloadByteDelivery{fakeCompatPlaybackDelivery: fixture.delivery, body: append([]byte(nil), body...), contentType: "video/mp4"}
	fixture.handler.playback = delivery
	fixture.handler.playSessions = newPlaySessionRegistry(delivery)
	fixture.handler.playSessions.now = func() time.Time { return fixture.now }
	return fixture, delivery
}

func downloadRequest(fixture *playbackFixture, method, target string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.SetPathValue("id", fixture.item.ID)
	request.Header.Set("X-Emby-Token", fixture.token)
	return request
}

func TestItemDownloadDeliversExactBytesHEADAndRange(t *testing.T) {
	media := []byte("0123456789-download-fixture")
	fixture, delivery := newDownloadFixture(t, media)

	full := httptest.NewRecorder()
	fixture.handler.handleDownload(full, downloadRequest(fixture, http.MethodGet, "/Items/"+fixture.item.ID+"/Download"))
	if full.Code != http.StatusOK || !bytes.Equal(full.Body.Bytes(), media) {
		t.Fatalf("full download status=%d body=%q", full.Code, full.Body.Bytes())
	}
	if full.Header().Get("Content-Type") != "video/mp4" || full.Header().Get("Content-Length") != strconv.Itoa(len(media)) ||
		full.Header().Get("Accept-Ranges") != "bytes" || full.Header().Get("Content-Disposition") != `attachment; filename="Safe Movie.mp4"` ||
		full.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("full download headers=%v", full.Header())
	}

	head := httptest.NewRecorder()
	fixture.handler.handleDownload(head, downloadRequest(fixture, http.MethodHead, "/Items/"+fixture.item.ID+"/Download"))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != strconv.Itoa(len(media)) ||
		head.Header().Get("Content-Disposition") != full.Header().Get("Content-Disposition") {
		t.Fatalf("HEAD status=%d body=%q headers=%v", head.Code, head.Body.Bytes(), head.Header())
	}

	rangeRequest := downloadRequest(fixture, http.MethodGet, "/Items/"+fixture.item.ID+"/Download")
	rangeRequest.Header.Set("Range", "bytes=3-8")
	partial := httptest.NewRecorder()
	fixture.handler.handleDownload(partial, rangeRequest)
	if partial.Code != http.StatusPartialContent || partial.Body.String() != string(media[3:9]) ||
		partial.Header().Get("Content-Range") != "bytes 3-8/27" || partial.Header().Get("Content-Length") != "6" ||
		partial.Header().Get("Content-Disposition") != full.Header().Get("Content-Disposition") {
		t.Fatalf("range status=%d body=%q headers=%v", partial.Code, partial.Body.String(), partial.Header())
	}
	if delivery.serveAssetCalls != 3 || delivery.servedAssetID == "" || delivery.servedAPIKey == "" || delivery.servedAPIKey == fixture.token ||
		delivery.servedProfileHeaderCredential || delivery.closeCalls != 3 || len(delivery.pinned) != 0 {
		t.Fatalf("delivery assetCalls=%d asset=%q credential/cleanup key=%q headerCredential=%t closes=%d pins=%v",
			delivery.serveAssetCalls, delivery.servedAssetID, delivery.servedAPIKey, delivery.servedProfileHeaderCredential, delivery.closeCalls, delivery.pinned)
	}
}

func TestItemDownloadFallsBackAcrossBoundedResolvedSources(t *testing.T) {
	fixture, delivery := newDownloadFixture(t, []byte("fallback-media"))
	delivery.sources = playback.SourceList{Sources: []playback.SourceOption{
		{SourceRef: "failed-primary", StableIdentity: "failed-primary", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "resolved-secondary", StableIdentity: "resolved-secondary", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
	}}
	delivery.openErrors = []error{playback.ErrMediaSourceFailed, nil}
	response := httptest.NewRecorder()
	fixture.handler.handleDownload(response, downloadRequest(fixture, http.MethodGet, "/Items/"+fixture.item.ID+"/Download"))
	if response.Code != http.StatusOK || response.Body.String() != "fallback-media" || delivery.openCalls != 2 || delivery.serveCalls != 1 || delivery.closeCalls != 2 {
		t.Fatalf("fallback status=%d body=%q opens=%d serves=%d closes=%d", response.Code, response.Body.String(), delivery.openCalls, delivery.serveCalls, delivery.closeCalls)
	}
}

func TestItemDownloadAcceptsContainerlessSourceOnlyAfterSafeResolution(t *testing.T) {
	fixture, delivery := newDownloadFixture(t, []byte("resolved-media"))
	delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{
		SourceRef: "extensionless-source", StableIdentity: "extensionless-source", Protocol: "http", ExpiresAt: fixture.now.Add(time.Hour),
	}}}
	response := httptest.NewRecorder()
	fixture.handler.handleDownload(response, downloadRequest(fixture, http.MethodGet, "/Items/"+fixture.item.ID+"/Download"))
	if response.Code != http.StatusOK || response.Body.String() != "resolved-media" || response.Header().Get("Content-Type") != "video/mp4" ||
		response.Header().Get("Content-Disposition") != `attachment; filename="Safe Movie.mp4"` {
		t.Fatalf("resolved extensionless status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
}

func TestItemDownloadRejectsMalformedQueryBeforeAuthenticationOrLookup(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  func(*playbackFixture) string
	}{
		{
			name: "semicolon separator",
			raw: func(fixture *playbackFixture) string {
				return "MediaSourceId=" + fixture.item.ID + ";UserId=" + fixture.authentication.session.ProfileID
			},
		},
		{name: "malformed escape", raw: func(*playbackFixture) string { return "MediaSourceId=%zz" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, delivery := newDownloadFixture(t, []byte("media"))
			request := downloadRequest(fixture, http.MethodGet, "/Items/"+fixture.item.ID+"/Download")
			request.URL.RawQuery = test.raw(fixture)
			response := httptest.NewRecorder()

			fixture.handler.handleDownload(response, request)

			catalog := fixture.handler.catalog.(*fakeCompatPlaybackCatalog)
			if response.Code != http.StatusBadRequest || fixture.authentication.authenticateCalls != 0 || catalog.calls != 0 ||
				delivery.sourceCalls != 0 || delivery.openCalls != 0 {
				t.Fatalf("malformed download status=%d auth=%d catalog=%d sources=%d opens=%d body=%s",
					response.Code, fixture.authentication.authenticateCalls, catalog.calls, delivery.sourceCalls, delivery.openCalls, response.Body.String())
			}
		})
	}
}

func TestItemDownloadFailsClosedForForbiddenMissingAndUnsafeSources(t *testing.T) {
	t.Run("foreign user", func(t *testing.T) {
		fixture, delivery := newDownloadFixture(t, []byte("media"))
		response := httptest.NewRecorder()
		fixture.handler.handleDownload(response, downloadRequest(fixture, http.MethodGet, "/Items/"+fixture.item.ID+"/Download?UserId=aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"))
		if response.Code != http.StatusNotFound || delivery.sourceCalls != 0 || delivery.openCalls != 0 {
			t.Fatalf("foreign download status=%d sources=%d opens=%d body=%s", response.Code, delivery.sourceCalls, delivery.openCalls, response.Body.String())
		}
	})

	t.Run("missing item", func(t *testing.T) {
		fixture, delivery := newDownloadFixture(t, []byte("media"))
		missing := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		request := downloadRequest(fixture, http.MethodGet, "/Items/"+missing+"/Download")
		request.SetPathValue("id", missing)
		response := httptest.NewRecorder()
		fixture.handler.handleDownload(response, request)
		if response.Code != http.StatusNotFound || delivery.sourceCalls != 0 || strings.Contains(response.Body.String(), fixture.item.ResourceID) {
			t.Fatalf("missing download status=%d sources=%d body=%s", response.Code, delivery.sourceCalls, response.Body.String())
		}
	})

	for _, source := range []playback.SourceOption{
		{SourceRef: "provider-page", Protocol: "external", ExpiresAt: time.Now().Add(time.Hour)},
		{SourceRef: "youtube-only", Protocol: "youtube", ExpiresAt: time.Now().Add(time.Hour)},
		{SourceRef: "playlist-only", Protocol: "hls", Container: "hls", ExpiresAt: time.Now().Add(time.Hour)},
	} {
		t.Run(source.SourceRef, func(t *testing.T) {
			fixture, delivery := newDownloadFixture(t, []byte("media"))
			source.ExpiresAt = fixture.now.Add(time.Hour)
			delivery.sources = playback.SourceList{Sources: []playback.SourceOption{source}}
			response := httptest.NewRecorder()
			fixture.handler.handleDownload(response, downloadRequest(fixture, http.MethodGet, "/Items/"+fixture.item.ID+"/Download"))
			if response.Code != http.StatusUnprocessableEntity || delivery.openCalls != 0 || delivery.serveCalls != 0 ||
				strings.Contains(response.Body.String(), source.SourceRef) {
				t.Fatalf("unsafe source status=%d opens=%d serves=%d body=%s", response.Code, delivery.openCalls, delivery.serveCalls, response.Body.String())
			}
		})
	}
}

func TestItemDownloadCancellationStopsResolutionAndReleasesCapabilities(t *testing.T) {
	fixture, delivery := newDownloadFixture(t, []byte("media"))
	delivery.openGate = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := downloadRequest(fixture, http.MethodGet, "/Items/"+fixture.item.ID+"/Download").WithContext(ctx)
	request.SetPathValue("id", fixture.item.ID)
	response := httptest.NewRecorder()
	fixture.handler.handleDownload(response, request)
	if response.Body.Len() != 0 || delivery.openCalls != 1 || delivery.serveCalls != 0 || len(fixture.handler.playSessions.entries) != 0 || len(delivery.pinned) != 0 {
		t.Fatalf("canceled download body=%q opens=%d serves=%d sessions=%d pins=%v", response.Body.String(), delivery.openCalls, delivery.serveCalls, len(fixture.handler.playSessions.entries), delivery.pinned)
	}
}

func TestItemDownloadSanitizesHeadersAndScrubsUpstreamFailures(t *testing.T) {
	t.Run("successful response", func(t *testing.T) {
		fixture, delivery := newDownloadFixture(t, []byte("media"))
		fixture.item.Title = "..\\provider-secret\r\nX-Leak: yes/../../safe\"movie"
		fixture.handler.catalog.(*fakeCompatPlaybackCatalog).item = fixture.item
		delivery.contentType = "text/html; charset=utf-8"
		delivery.upstreamDisposition = `attachment; filename="https://provider.invalid/file?token=provider-secret"`
		delivery.upstreamETag = `"provider-secret"`
		response := httptest.NewRecorder()
		fixture.handler.handleDownload(response, downloadRequest(fixture, http.MethodGet, "/Items/"+fixture.item.ID+"/Download"))
		headers := response.Header().Values("Content-Disposition")
		joined := strings.Join(headers, "\n")
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "video/mp4" || len(headers) != 1 ||
			strings.ContainsAny(joined, "\r\n") || strings.Contains(joined, "provider-secret") || strings.Contains(joined, "..") ||
			response.Header().Get("ETag") != "" || strings.Contains(response.Body.String(), "provider.invalid") {
			t.Fatalf("sanitized response status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
		}
	})

	t.Run("upstream failure", func(t *testing.T) {
		fixture, delivery := newDownloadFixture(t, []byte("https://provider.invalid/private?token=provider-secret"))
		delivery.status = http.StatusForbidden
		delivery.upstreamDisposition = `attachment; filename="provider-secret"`
		delivery.upstreamETag = `"provider-secret"`
		response := httptest.NewRecorder()
		fixture.handler.handleDownload(response, downloadRequest(fixture, http.MethodGet, "/Items/"+fixture.item.ID+"/Download"))
		serialized := response.Body.String() + response.Header().Get("Content-Disposition") + response.Header().Get("ETag")
		if response.Code != http.StatusBadGateway || strings.Contains(serialized, "provider.invalid") || strings.Contains(serialized, "provider-secret") || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("upstream failure status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
		}
	})
}
