package playback

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	if transport.MaxConnsPerHost != maximumDirectStreamsPerHost ||
		transport.MaxIdleConnsPerHost != maximumDirectStreamsPerHost ||
		transport.MaxIdleConns != maximumDirectStreamsGlobal {
		t.Fatalf("playback transport connection bounds: max=%d per-host=%d idle-per-host=%d", transport.MaxIdleConns, transport.MaxConnsPerHost, transport.MaxIdleConnsPerHost)
	}
}
