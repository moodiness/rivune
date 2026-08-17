package playback

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDirectStreamAdmissionBoundsOwnerAndGlobalBeforeRoundTrip(t *testing.T) {
	var calls atomic.Int32
	service := &Service{
		client: &http.Client{Transport: playbackRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"video/mp4"}},
				Body:       io.NopCloser(strings.NewReader("media")),
				Request:    request,
			}, nil
		})},
		directStreamGlobalLimit: 2,
		directStreamOwnerLimit:  1,
		directStreamIdleTimeout: time.Hour,
	}
	incoming := httptest.NewRequest(http.MethodGet, "http://rivune.test/asset", nil)
	asset := storedAsset{URL: "https://provider.example/media.mp4", Container: "mp4"}

	first, err := service.fetchProxyAsset(context.Background(), incoming, asset, asset.URL, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.fetchProxyAsset(context.Background(), incoming, asset, asset.URL, "session-a"); !errors.Is(err, ErrMediaCapacityReached) {
		t.Fatalf("same-session concurrent stream error = %v, want capacity", err)
	}
	second, err := service.fetchProxyAsset(context.Background(), incoming, asset, asset.URL, "session-b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.fetchProxyAsset(context.Background(), incoming, asset, asset.URL, "session-c"); !errors.Is(err, ErrMediaCapacityReached) {
		t.Fatalf("global concurrent stream error = %v, want capacity", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("capacity rejection reached upstream: round trips = %d, want 2", calls.Load())
	}

	if err := first.Body.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := service.fetchProxyAsset(context.Background(), incoming, asset, asset.URL, "session-c")
	if err != nil {
		t.Fatalf("released capacity was not reusable: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("readmitted stream round trips = %d, want 3", calls.Load())
	}
	_ = second.Body.Close()
	_ = third.Body.Close()
}

func TestDirectStreamAdmissionPreservesRangeHeadAndEOFRelease(t *testing.T) {
	var calls atomic.Int32
	service := &Service{
		client: &http.Client{Transport: playbackRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			switch calls.Add(1) {
			case 1:
				if request.Method != http.MethodGet || request.Header.Get("Range") != "bytes=8-12" {
					t.Fatalf("ranged upstream request method=%s range=%q", request.Method, request.Header.Get("Range"))
				}
				return &http.Response{
					StatusCode: http.StatusPartialContent,
					Header: http.Header{
						"Content-Type":  []string{"video/mp4"},
						"Content-Range": []string{"bytes 8-12/20"},
					},
					Body: io.NopCloser(strings.NewReader("range")), Request: request,
				}, nil
			default:
				if request.Method != http.MethodGet {
					t.Fatalf("HEAD compatibility upstream method = %s, want GET", request.Method)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"video/mp4"}},
					Body:       io.NopCloser(strings.NewReader("complete")),
					Request:    request,
				}, nil
			}
		})},
		directStreamGlobalLimit: 1,
		directStreamOwnerLimit:  1,
		directStreamIdleTimeout: time.Hour,
	}
	asset := storedAsset{URL: "https://provider.example/media.mp4", Container: "mp4"}
	ranged := httptest.NewRequest(http.MethodGet, "http://rivune.test/asset", nil)
	ranged.Header.Set("Range", "bytes=8-12")
	response, err := service.fetchProxyAsset(context.Background(), ranged, asset, asset.URL, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusPartialContent || response.Header.Get("Content-Range") != "bytes 8-12/20" || string(body) != "range" {
		t.Fatalf("ranged response status=%d content-range=%q body=%q", response.StatusCode, response.Header.Get("Content-Range"), body)
	}

	head := httptest.NewRequest(http.MethodHead, "http://rivune.test/asset", nil)
	headResponse, err := service.fetchProxyAsset(context.Background(), head, asset, asset.URL, "session-a")
	if err != nil {
		t.Fatalf("EOF did not release the session lease: %v", err)
	}
	if err := headResponse.Body.Close(); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}

type blockingDirectStreamBody struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingDirectStreamBody() *blockingDirectStreamBody {
	return &blockingDirectStreamBody{closed: make(chan struct{})}
}

func (body *blockingDirectStreamBody) Read([]byte) (int, error) {
	<-body.closed
	return 0, io.ErrClosedPipe
}

func (body *blockingDirectStreamBody) Close() error {
	body.once.Do(func() { close(body.closed) })
	return nil
}

func TestDirectStreamIdleBodyCancellationReleasesLease(t *testing.T) {
	stalled := newBlockingDirectStreamBody()
	var calls atomic.Int32
	var stalledContext context.Context
	service := &Service{
		client: &http.Client{Transport: playbackRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				stalledContext = request.Context()
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"video/mp4"}},
					Body:       stalled,
					Request:    request,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"video/mp4"}},
				Body:       io.NopCloser(strings.NewReader("retry")),
				Request:    request,
			}, nil
		})},
		directStreamGlobalLimit: 1,
		directStreamOwnerLimit:  1,
		directStreamIdleTimeout: 25 * time.Millisecond,
	}
	incoming := httptest.NewRequest(http.MethodGet, "http://rivune.test/asset", nil)
	asset := storedAsset{URL: "https://provider.example/media.mp4", Container: "mp4"}
	response, err := service.fetchProxyAsset(context.Background(), incoming, asset, asset.URL, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	readResult := make(chan error, 1)
	go func() {
		_, readErr := response.Body.Read(make([]byte, 1))
		readResult <- readErr
	}()
	select {
	case readErr := <-readResult:
		if readErr == nil {
			t.Fatal("stalled body cancellation returned no read error")
		}
	case <-time.After(time.Second):
		t.Fatal("stalled body read was not canceled")
	}
	select {
	case <-stalledContext.Done():
	case <-time.After(time.Second):
		t.Fatal("stalled upstream request context was not canceled")
	}

	deadline := time.Now().Add(time.Second)
	for {
		retry, retryErr := service.fetchProxyAsset(context.Background(), incoming, asset, asset.URL, "session-a")
		if retryErr == nil {
			_ = retry.Body.Close()
			break
		}
		if !errors.Is(retryErr, ErrMediaCapacityReached) {
			t.Fatalf("retry after idle cancellation failed: %v", retryErr)
		}
		if time.Now().After(deadline) {
			t.Fatal("idle-canceled stream did not release its lease")
		}
		time.Sleep(time.Millisecond)
	}
	_ = response.Body.Close()
}

func TestReadIdleBodyPreservesTimeoutAndParentCancellation(t *testing.T) {
	t.Run("idle timeout", func(t *testing.T) {
		body := newBlockingDirectStreamBody()
		stream := newReadIdleBody(context.Background(), body, func() {}, func() {}, 20*time.Millisecond, 20*time.Millisecond)
		_, err := stream.Read(make([]byte, 1))
		if !errors.Is(err, ErrMediaSourceTimeout) || !errors.Is(err, ErrMediaSourceFailed) {
			t.Fatalf("idle read error = %v, want timeout and source failure identities", err)
		}
	})
	t.Run("parent cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		body := newBlockingDirectStreamBody()
		stream := newReadIdleBody(ctx, body, func() {}, func() {}, time.Hour, time.Hour)
		cancel()
		_, err := stream.Read(make([]byte, 1))
		if !errors.Is(err, context.Canceled) || errors.Is(err, ErrMediaSourceTimeout) {
			t.Fatalf("canceled read error = %v, want context cancellation", err)
		}
	})
}

func TestDirectStartupTimeoutDoesNotCommitAndReleasesAdmission(t *testing.T) {
	stalled := newBlockingDirectStreamBody()
	var calls atomic.Int32
	service := &Service{
		client: &http.Client{Transport: playbackRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			body := io.ReadCloser(io.NopCloser(strings.NewReader("retry")))
			if calls.Add(1) == 1 {
				body = stalled
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Length": []string{"5"}}, Body: body, ContentLength: 5, Request: request}, nil
		})},
		directStreamGlobalLimit: 1, directStreamOwnerLimit: 1,
		directStreamIdleTimeout: time.Hour, directStreamStartupReadTimeout: 20 * time.Millisecond,
	}
	request := httptest.NewRequest(http.MethodGet, "http://rivune.test/asset", nil)
	asset := storedAsset{URL: "https://provider.example/media.mp4", Container: "mp4"}
	upstream, err := service.fetchProxyAsset(request.Context(), request, asset, asset.URL, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	writer := &countingProxyResponseWriter{}
	err = writeDirectProxyAssetWithStartupTimeout(writer, request, asset, upstream, asset.URL, service.directStartupReadTimeout())
	if !errors.Is(err, ErrMediaSourceTimeout) || writer.status != 0 || len(writer.Header()) != 0 {
		t.Fatalf("startup result error=%v status=%d headers=%v", err, writer.status, writer.Header())
	}
	retry, err := service.fetchProxyAsset(request.Context(), request, asset, asset.URL, "session-a")
	if err != nil {
		t.Fatalf("startup failure did not release admission: %v", err)
	}
	_ = retry.Body.Close()
}

type pacedDirectStreamBody struct {
	remaining int
	delay     time.Duration
}

func (body *pacedDirectStreamBody) Read(destination []byte) (int, error) {
	if body.remaining == 0 {
		return 0, io.EOF
	}
	time.Sleep(body.delay)
	destination[0] = 'x'
	body.remaining--
	return 1, nil
}

func (*pacedDirectStreamBody) Close() error { return nil }

func TestDirectStreamIdleDeadlineResetsOnProgress(t *testing.T) {
	service := &Service{
		client: &http.Client{Transport: playbackRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"video/mp4"}},
				Body:       &pacedDirectStreamBody{remaining: 20, delay: 10 * time.Millisecond},
				Request:    request,
			}, nil
		})},
		directStreamGlobalLimit: 1,
		directStreamOwnerLimit:  1,
		directStreamIdleTimeout: 100 * time.Millisecond,
	}
	incoming := httptest.NewRequest(http.MethodGet, "http://rivune.test/asset", nil)
	asset := storedAsset{URL: "https://provider.example/media.mp4", Container: "mp4"}
	response, err := service.fetchProxyAsset(context.Background(), incoming, asset, asset.URL, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("active long stream was canceled: %v", err)
	}
	if len(body) != 20 {
		t.Fatalf("active long stream bytes = %d, want 20", len(body))
	}
	_ = response.Body.Close()
}

func TestDefaultPlaybackTransportBoundsPerHostConnections(t *testing.T) {
	service, err := NewService(nil, nil, nil, MediaOptions{TempDirectory: filepath.Join(t.TempDir(), "workspace")})
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := service.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("playback transport type = %T", service.client.Transport)
	}
	if transport.ForceAttemptHTTP2 || transport.Protocols == nil || !transport.Protocols.HTTP1() ||
		!transport.Protocols.HTTP2() || transport.Protocols.UnencryptedHTTP2() ||
		transport.MaxConnsPerHost != maximumDirectStreamsPerHost ||
		transport.MaxIdleConnsPerHost != maximumDirectStreamsPerHost ||
		transport.MaxIdleConns != maximumDirectStreamsGlobal {
		t.Fatalf("playback transport configuration: protocols=%v max=%d per-host=%d idle-per-host=%d", transport.Protocols, transport.MaxIdleConns, transport.MaxConnsPerHost, transport.MaxIdleConnsPerHost)
	}
}

func TestDefaultPlaybackTransportNegotiatesTLSHTTP2(t *testing.T) {
	protocol := make(chan int, 1)
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		protocol <- request.ProtoMajor
		_, _ = io.WriteString(response, "media")
	}))
	origin.EnableHTTP2 = true
	origin.StartTLS()
	defer origin.Close()

	service, err := NewService(nil, nil, nil, MediaOptions{TempDirectory: filepath.Join(t.TempDir(), "workspace")})
	if err != nil {
		t.Fatal(err)
	}
	transport := service.client.Transport.(*http.Transport)
	transport.DialContext = (&net.Dialer{}).DialContext
	tlsConfig := origin.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	tlsConfig.NextProtos = []string{"h2", "http/1.1"}
	transport.TLSClientConfig = tlsConfig
	response, err := service.client.Get(origin.URL + "/media.mkv")
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.ProtoMajor != 2 || <-protocol != 2 {
		t.Fatalf("playback transport protocol = %s, want HTTP/2", response.Proto)
	}
}

type zeroProxyReader struct{}

func (zeroProxyReader) Read(destination []byte) (int, error) {
	for index := range destination {
		destination[index] = 's'
	}
	return len(destination), nil
}

type trackedProxyBody struct {
	reader io.Reader
	closed atomic.Bool
	reads  atomic.Int64
}

func (body *trackedProxyBody) Read(destination []byte) (int, error) {
	body.reads.Add(1)
	return body.reader.Read(destination)
}

func (body *trackedProxyBody) Close() error {
	body.closed.Store(true)
	return nil
}

type countingProxyResponseWriter struct {
	header      http.Header
	status      int
	written     int64
	wrote       chan struct{}
	headerWrote chan struct{}
	once        sync.Once
	headerOnce  sync.Once
}

func (writer *countingProxyResponseWriter) Header() http.Header {
	if writer.header == nil {
		writer.header = make(http.Header)
	}
	return writer.header
}

func (writer *countingProxyResponseWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
		if writer.headerWrote != nil {
			writer.headerOnce.Do(func() { close(writer.headerWrote) })
		}
	}
}

func (writer *countingProxyResponseWriter) Write(contents []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	writer.written += int64(len(contents))
	if writer.wrote != nil {
		writer.once.Do(func() { close(writer.wrote) })
	}
	return len(contents), nil
}

func directProxyResponse(body io.ReadCloser) *http.Response {
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        make(http.Header),
		Body:          body,
		ContentLength: -1,
	}
}

func TestDirectStartupPreflightSkipsBodylessResponsesAndAcceptsCleanEOF(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		status     int
		length     int64
		header     http.Header
		wantReads  int64
		wantStatus int
	}{
		{name: "HEAD", method: http.MethodHead, status: http.StatusOK, length: -1, wantStatus: http.StatusOK},
		{name: "no content", method: http.MethodGet, status: http.StatusNoContent, length: -1, wantStatus: http.StatusNoContent},
		{name: "known zero length", method: http.MethodGet, status: http.StatusOK, length: 0, wantStatus: http.StatusOK},
		{name: "explicit zero length", method: http.MethodGet, status: http.StatusOK, length: -1, header: http.Header{"Content-Length": []string{"0"}}, wantStatus: http.StatusOK},
		{name: "clean EOF", method: http.MethodGet, status: http.StatusOK, length: -1, wantReads: 2, wantStatus: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &trackedProxyBody{reader: strings.NewReader("")}
			response := directProxyResponse(body)
			response.StatusCode = test.status
			response.ContentLength = test.length
			response.Header = test.header
			if response.Header == nil {
				response.Header = make(http.Header)
			}
			writer := &countingProxyResponseWriter{}
			err := writeDirectProxyAssetWithStartupTimeout(writer, httptest.NewRequest(test.method, "/movie.mp4", nil), storedAsset{Kind: "stream"}, response, "https://provider.example/movie.mp4", 10*time.Millisecond)
			if err != nil || writer.status != test.wantStatus || body.reads.Load() != test.wantReads || !body.closed.Load() {
				t.Fatalf("bodyless result error=%v status=%d reads=%d closed=%t", err, writer.status, body.reads.Load(), body.closed.Load())
			}
		})
	}
}
func TestDirectStartupEOFWithDeclaredContentFailsBeforeCommit(t *testing.T) {
	for _, test := range []struct {
		name   string
		length int64
		header string
	}{
		{name: "response length", length: 1},
		{name: "valid header", length: -1, header: "1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &trackedProxyBody{reader: strings.NewReader("")}
			response := directProxyResponse(body)
			response.ContentLength = test.length
			if test.header != "" {
				response.Header.Set("Content-Length", test.header)
			}
			writer := &countingProxyResponseWriter{}
			err := writeDirectProxyAssetWithStartupTimeout(writer, httptest.NewRequest(http.MethodGet, "/movie.mp4", nil), storedAsset{Kind: "stream"}, response, "https://provider.example/movie.mp4", time.Second)
			if !errors.Is(err, ErrMediaSourceFailed) || writer.status != 0 || len(writer.Header()) != 0 || !body.closed.Load() {
				t.Fatalf("declared empty source error=%v status=%d headers=%v closed=%t", err, writer.status, writer.Header(), body.closed.Load())
			}
		})
	}
}

func recoveredProxyPanic(run func()) (recovered any) {
	defer func() { recovered = recover() }()
	run()
	return nil
}

func TestDirectSubtitleProxyAcceptsExactlyMaximumBytes(t *testing.T) {
	body := &trackedProxyBody{reader: io.LimitReader(zeroProxyReader{}, maximumConvertedSubtitleBytes)}
	response := directProxyResponse(body)
	response.ContentLength = maximumConvertedSubtitleBytes
	response.Header.Set("Content-Length", strconv.Itoa(maximumConvertedSubtitleBytes))
	response.Header.Set("Content-Type", "text/vtt; charset=utf-8")
	writer := &countingProxyResponseWriter{}
	request := httptest.NewRequest(http.MethodGet, "/external.vtt", nil)

	if err := writeDirectProxyAsset(writer, request, storedAsset{Kind: "subtitle"}, response, "https://provider.example/external.vtt"); err != nil {
		t.Fatalf("proxy subtitle at limit: %v", err)
	}
	if writer.status != http.StatusOK || writer.written != maximumConvertedSubtitleBytes || !body.closed.Load() {
		t.Fatalf("subtitle boundary status=%d bytes=%d closed=%t", writer.status, writer.written, body.closed.Load())
	}
}

func TestDirectSubtitleProxyAbortsBeforeWritingBytePastMaximum(t *testing.T) {
	body := &trackedProxyBody{reader: io.LimitReader(zeroProxyReader{}, maximumConvertedSubtitleBytes+1)}
	writer := &countingProxyResponseWriter{}
	request := httptest.NewRequest(http.MethodGet, "/external.vtt", nil)

	recovered := recoveredProxyPanic(func() {
		_ = writeDirectProxyAsset(writer, request, storedAsset{Kind: "subtitle"}, directProxyResponse(body), "https://provider.example/external.vtt")
	})
	if recovered != http.ErrAbortHandler {
		t.Fatalf("overflow panic = %v, want http.ErrAbortHandler", recovered)
	}
	if writer.written != maximumConvertedSubtitleBytes || !body.closed.Load() {
		t.Fatalf("overflow bytes=%d closed=%t, want %d/true", writer.written, body.closed.Load(), maximumConvertedSubtitleBytes)
	}
}

func TestDirectSubtitleProxyStopsNeverEndingUpstream(t *testing.T) {
	upstream := &trackedProxyBody{reader: zeroProxyReader{}}
	requestContext, cancel := context.WithCancel(context.Background())
	var released atomic.Bool
	body := newReadIdleBody(context.Background(), upstream, cancel, func() { released.Store(true) }, time.Hour, time.Hour)
	writer := &countingProxyResponseWriter{}
	request := httptest.NewRequest(http.MethodGet, "/external.vtt", nil)

	done := make(chan any, 1)
	go func() {
		done <- recoveredProxyPanic(func() {
			_ = writeDirectProxyAsset(writer, request, storedAsset{Kind: "subtitle"}, directProxyResponse(body), "https://provider.example/external.vtt")
		})
	}()
	select {
	case recovered := <-done:
		if recovered != http.ErrAbortHandler {
			t.Fatalf("never-ending upstream panic = %v, want http.ErrAbortHandler", recovered)
		}
	case <-time.After(time.Second):
		t.Fatal("never-ending subtitle upstream was not terminated")
	}
	select {
	case <-requestContext.Done():
	default:
		t.Fatal("overflow did not cancel the upstream request context")
	}
	if writer.written != maximumConvertedSubtitleBytes || !upstream.closed.Load() || !released.Load() {
		t.Fatalf("never-ending bytes=%d closed=%t released=%t, want %d/true/true", writer.written, upstream.closed.Load(), released.Load(), maximumConvertedSubtitleBytes)
	}
}

func TestDirectSubtitleProxyRejectsKnownOversizeBeforeCommit(t *testing.T) {
	tests := []struct {
		name   string
		status int
		header http.Header
		length int64
	}{
		{name: "content length", status: http.StatusOK, header: http.Header{"Content-Length": []string{"16777217"}}, length: maximumConvertedSubtitleBytes + 1},
		{name: "content range total", status: http.StatusPartialContent, header: http.Header{"Content-Length": []string{"1"}, "Content-Range": []string{"bytes 0-0/16777217"}}, length: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &trackedProxyBody{reader: zeroProxyReader{}}
			response := directProxyResponse(body)
			response.StatusCode = test.status
			response.Header = test.header
			response.ContentLength = test.length
			writer := &countingProxyResponseWriter{}
			err := writeDirectProxyAsset(writer, httptest.NewRequest(http.MethodGet, "/external.vtt", nil), storedAsset{Kind: "subtitle"}, response, "https://provider.example/external.vtt")
			if !errors.Is(err, ErrMediaSourceFailed) {
				t.Fatalf("known oversize error = %v, want ErrMediaSourceFailed", err)
			}
			if writer.status != 0 || writer.written != 0 || body.reads.Load() != 0 || !body.closed.Load() {
				t.Fatalf("known oversize committed=%d bytes=%d reads=%d closed=%t", writer.status, writer.written, body.reads.Load(), body.closed.Load())
			}
		})
	}
}

func TestDirectSubtitleProxyPreservesValidRange(t *testing.T) {
	body := &trackedProxyBody{reader: strings.NewReader("cue")}
	response := directProxyResponse(body)
	response.StatusCode = http.StatusPartialContent
	response.ContentLength = 3
	response.Header.Set("Content-Length", "3")
	response.Header.Set("Content-Range", "bytes 4-6/7")
	writer := &countingProxyResponseWriter{}

	err := writeDirectProxyAsset(writer, httptest.NewRequest(http.MethodGet, "/external.vtt", nil), storedAsset{Kind: "subtitle"}, response, "https://provider.example/external.vtt")
	if err != nil {
		t.Fatal(err)
	}
	if writer.status != http.StatusPartialContent || writer.written != 3 || writer.Header().Get("Content-Range") != "bytes 4-6/7" {
		t.Fatalf("range response status=%d bytes=%d headers=%v", writer.status, writer.written, writer.Header())
	}
}

func TestDirectProxyLimitUsesStoredAssetKind(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/misleading.vtt", nil)
	body := &trackedProxyBody{reader: io.LimitReader(zeroProxyReader{}, maximumConvertedSubtitleBytes+1)}
	response := directProxyResponse(body)
	response.Header.Set("Content-Type", "text/vtt")
	writer := &countingProxyResponseWriter{}

	if err := writeDirectProxyAsset(writer, request, storedAsset{Kind: "stream"}, response, "https://provider.example/misleading.vtt"); err != nil {
		t.Fatalf("media asset with subtitle metadata was bounded: %v", err)
	}
	if writer.written != maximumConvertedSubtitleBytes+1 || !body.closed.Load() {
		t.Fatalf("media bytes=%d closed=%t, want %d/true", writer.written, body.closed.Load(), maximumConvertedSubtitleBytes+1)
	}

	boundedBody := &trackedProxyBody{reader: io.LimitReader(zeroProxyReader{}, maximumConvertedSubtitleBytes+1)}
	boundedResponse := directProxyResponse(boundedBody)
	boundedResponse.Header.Set("Content-Type", "video/mp4")
	boundedWriter := &countingProxyResponseWriter{}
	recovered := recoveredProxyPanic(func() {
		_ = writeDirectProxyAsset(boundedWriter, request, storedAsset{Kind: "subtitle"}, boundedResponse, "https://provider.example/misleading.mp4")
	})
	if recovered != http.ErrAbortHandler || boundedWriter.written != maximumConvertedSubtitleBytes {
		t.Fatalf("stored subtitle with media metadata panic=%v bytes=%d", recovered, boundedWriter.written)
	}
}

func TestDirectMediaProxyCommitsFirstByteThenAbortsIdleStream(t *testing.T) {
	upstream := &firstByteThenBlockingBody{closed: make(chan struct{})}
	requestContext, cancel := context.WithCancel(context.Background())
	var released atomic.Bool
	body := newReadIdleBody(context.Background(), upstream, cancel, func() { released.Store(true) }, 20*time.Millisecond, 20*time.Millisecond)
	writer := &countingProxyResponseWriter{}
	recovered := recoveredProxyPanic(func() {
		_ = writeDirectProxyAssetWithStartupTimeout(writer, httptest.NewRequest(http.MethodGet, "/movie.mp4", nil), storedAsset{Kind: "stream"}, directProxyResponse(body), "https://provider.example/movie.mp4", time.Second)
	})
	if recovered != http.ErrAbortHandler || writer.status != http.StatusOK || writer.written != 1 {
		t.Fatalf("post-commit stall panic=%v status=%d bytes=%d", recovered, writer.status, writer.written)
	}
	select {
	case <-requestContext.Done():
	default:
		t.Fatal("post-commit timeout did not cancel upstream context")
	}
	if !released.Load() {
		t.Fatal("post-commit timeout did not release admission")
	}
}

type firstByteThenBlockingBody struct {
	closed chan struct{}
	once   sync.Once
	read   atomic.Int32
}

func (body *firstByteThenBlockingBody) Read(destination []byte) (int, error) {
	if body.read.Add(1) == 1 {
		destination[0] = 'x'
		return 1, nil
	}
	<-body.closed
	return 0, io.ErrClosedPipe
}

func (body *firstByteThenBlockingBody) Close() error {
	body.once.Do(func() { close(body.closed) })
	return nil
}

type gatedMediaProxyBody struct {
	release chan struct{}
	read    int
	closed  atomic.Bool
}

func (body *gatedMediaProxyBody) Read(destination []byte) (int, error) {
	body.read++
	if body.read == 1 {
		destination[0] = 'f'
		return 1, nil
	}
	<-body.release
	return copy(destination, "irstsecond"), io.EOF
}

func (body *gatedMediaProxyBody) Close() error {
	body.closed.Store(true)
	return nil
}

func TestDirectMediaProxyStillStreamsBeforeUpstreamEOF(t *testing.T) {
	body := &gatedMediaProxyBody{release: make(chan struct{})}
	writer := &countingProxyResponseWriter{wrote: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- writeDirectProxyAsset(writer, httptest.NewRequest(http.MethodGet, "/movie.mp4", nil), storedAsset{Kind: "stream"}, directProxyResponse(body), "https://provider.example/movie.mp4")
	}()
	select {
	case <-writer.wrote:
		if writer.written != 1 {
			t.Fatalf("media bytes before upstream continuation = %d, want first byte only", writer.written)
		}
	case <-time.After(time.Second):
		t.Fatal("media proxy buffered instead of streaming the first byte")
	}
	close(body.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if writer.written != int64(len("firstsecond")) || !body.closed.Load() {
		t.Fatalf("media result bytes=%d closed=%t", writer.written, body.closed.Load())
	}
}

func TestDirectMediaProxyPreservesUpstreamErrorBody(t *testing.T) {
	body := &trackedProxyBody{reader: strings.NewReader("missing")}
	response := directProxyResponse(body)
	response.StatusCode = http.StatusNotFound
	response.ContentLength = int64(len("missing"))
	response.Header.Set("Content-Length", strconv.Itoa(len("missing")))
	writer := &countingProxyResponseWriter{}

	if err := writeDirectProxyAsset(writer, httptest.NewRequest(http.MethodGet, "/movie.mp4", nil), storedAsset{Kind: "stream"}, response, "https://provider.example/movie.mp4"); err != nil {
		t.Fatal(err)
	}
	if writer.status != http.StatusNotFound || writer.written != int64(len("missing")) || !body.closed.Load() {
		t.Fatalf("upstream error status=%d bytes=%d closed=%t", writer.status, writer.written, body.closed.Load())
	}
}

func TestDirectMediaProxyCommitsFinalErrorBeforeReadingBody(t *testing.T) {
	upstream := newBlockingDirectStreamBody()
	response := directProxyResponse(newReadIdleBody(context.Background(), upstream, func() {}, func() {}, 25*time.Millisecond, 25*time.Millisecond))
	response.StatusCode = http.StatusServiceUnavailable
	writer := &countingProxyResponseWriter{headerWrote: make(chan struct{})}
	done := make(chan any, 1)
	go func() {
		done <- recoveredProxyPanic(func() {
			_ = writeDirectProxyAssetWithStartupTimeout(writer, httptest.NewRequest(http.MethodGet, "/movie.mp4", nil), storedAsset{Kind: "stream"}, response, "https://provider.example/movie.mp4", time.Second)
		})
	}()
	select {
	case <-writer.headerWrote:
		if writer.status != http.StatusServiceUnavailable {
			t.Fatalf("final upstream status = %d", writer.status)
		}
	case recovered := <-done:
		t.Fatalf("final upstream response ended before status commit: panic=%v status=%d", recovered, writer.status)
	case <-time.After(time.Second):
		t.Fatal("final upstream status waited for body preflight")
	}
	if recovered := <-done; recovered != http.ErrAbortHandler {
		t.Fatalf("stalled final error panic = %v", recovered)
	}
}
