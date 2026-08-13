package playback

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
)

type testPlaybackProfileTransaction struct{}

func (testPlaybackProfileTransaction) Commit(context.Context) error   { return nil }
func (testPlaybackProfileTransaction) Rollback(context.Context) error { return nil }
func (testPlaybackProfileTransaction) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (testPlaybackProfileTransaction) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected profile transaction query")
}
func (testPlaybackProfileTransaction) QueryRow(context.Context, string, ...any) pgx.Row {
	return testPlaybackProfileRow{}
}
func (testPlaybackProfileTransaction) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("unexpected nested playback transaction")
}
func (testPlaybackProfileTransaction) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("unexpected playback transaction copy")
}
func (testPlaybackProfileTransaction) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}
func (testPlaybackProfileTransaction) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }
func (testPlaybackProfileTransaction) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("unexpected playback transaction prepare")
}
func (testPlaybackProfileTransaction) Conn() *pgx.Conn { return nil }

type testPlaybackProfileRow struct{}

func (testPlaybackProfileRow) Scan(...any) error { return pgx.ErrNoRows }

func testPlaybackProfileTxFactory(context.Context, auth.Principal) (playbackProfileTransaction, error) {
	return testPlaybackProfileTransaction{}, nil
}

func TestDefaultPlaybackTransportRejectsNonPublicProviderNetworks(t *testing.T) {
	service, err := NewService(nil, nil, nil, MediaOptions{TempDirectory: filepath.Join(t.TempDir(), "workspace")})
	if err != nil {
		t.Fatal(err)
	}
	incoming := httptest.NewRequest(http.MethodGet, "http://rivune.test/playback", nil)
	for _, target := range []string{
		"http://10.0.0.10/video.mp4",
		"http://172.16.0.10/master.m3u8",
		"http://192.168.1.10/subtitle.vtt",
		"http://100.64.0.10/segment.ts",
		"http://[fc00::10]/key",
		"http://169.254.169.254/latest/meta-data/iam/security-credentials/",
	} {
		t.Run(target, func(t *testing.T) {
			response, fetchErr := service.fetchAsset(
				context.Background(),
				incoming,
				storedAsset{URL: target},
				target,
			)
			if response != nil {
				_ = response.Body.Close()
				t.Fatal("unexpected non-public provider response")
			}
			if fetchErr == nil || !strings.Contains(fetchErr.Error(), "outbound destination is not permitted") {
				t.Fatalf("non-public provider destination error = %v", fetchErr)
			}
		})
	}
}

func TestPlaybackRedirectPolicyStripsProviderHeadersAcrossHosts(t *testing.T) {
	original := httptest.NewRequest(http.MethodGet, "https://provider.example/master.m3u8", nil)
	redirected := httptest.NewRequest(http.MethodGet, "https://cdn.example/segment.ts", nil)
	redirected.Header.Set("Authorization", "Bearer provider-secret")
	redirected.Header.Set("Cookie", "provider_session=secret")
	redirected.Header.Set("X-Provider-Key", "secret")
	safeHeaders := map[string]string{
		"Range":             "bytes=0-1023",
		"If-Range":          `"range-version"`,
		"If-None-Match":     `"cache-version"`,
		"If-Modified-Since": "Tue, 15 Nov 1994 12:45:26 GMT",
	}
	for name, value := range safeHeaders {
		redirected.Header.Set(name, value)
	}

	if err := playbackRedirectPolicy(redirected, []*http.Request{original}); err != nil {
		t.Fatalf("cross-host redirect policy error: %v", err)
	}
	for _, name := range []string{"Authorization", "Cookie", "X-Provider-Key"} {
		if got := redirected.Header.Get(name); got != "" {
			t.Fatalf("cross-host redirect retained %s: %q", name, got)
		}
	}
	for name, want := range safeHeaders {
		if got := redirected.Header.Get(name); got != want {
			t.Fatalf("cross-host redirect %s = %q, want %q", name, got, want)
		}
	}
	if got := redirected.Header.Get("User-Agent"); got != "Rivune-Playback/1" {
		t.Fatalf("cross-host redirect User-Agent = %q", got)
	}
}

func TestPlaybackRedirectPolicyUsesCanonicalMediaOrigins(t *testing.T) {
	tests := []struct {
		name       string
		from       string
		to         string
		wantSecret bool
	}{
		{name: "default HTTPS port", from: "https://media.example:443/master.m3u8", to: "https://media.example/segment.ts", wantSecret: true},
		{name: "terminal hostname point", from: "https://media.example./master.m3u8", to: "https://MEDIA.EXAMPLE/segment.ts", wantSecret: true},
		{name: "different port", from: "https://media.example/master.m3u8", to: "https://media.example:444/segment.ts"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := httptest.NewRequest(http.MethodGet, test.from, nil)
			redirected := httptest.NewRequest(http.MethodGet, test.to, nil)
			redirected.Header.Set("Authorization", "Bearer provider-secret")
			if err := playbackRedirectPolicy(redirected, []*http.Request{original}); err != nil {
				t.Fatal(err)
			}
			if got := redirected.Header.Get("Authorization"); (got != "") != test.wantSecret {
				t.Fatalf("redirect Authorization = %q, want present=%t", got, test.wantSecret)
			}
		})
	}
}

func TestFetchAssetPreservesRangeAcrossCrossOriginRedirect(t *testing.T) {
	type observedRequest struct {
		method string
		header http.Header
	}
	originRequests := make(chan observedRequest, 1)
	redirectedRequests := make(chan observedRequest, 1)
	contents := []byte("cross-origin-direct-play-media")
	wantBytes := contents[7:13]
	wantRange := "bytes=7-12"
	safeHeaders := map[string]string{
		"Range":             wantRange,
		"If-Range":          `"range-version"`,
		"If-None-Match":     `"cache-version"`,
		"If-Modified-Since": "Tue, 15 Nov 1994 12:45:26 GMT",
	}

	cdn := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		redirectedRequests <- observedRequest{method: request.Method, header: request.Header.Clone()}
		if request.Header.Get("Range") != wantRange {
			response.WriteHeader(http.StatusOK)
			_, _ = response.Write(contents)
			return
		}
		response.Header().Set("Content-Range", fmt.Sprintf("bytes 7-12/%d", len(contents)))
		response.WriteHeader(http.StatusPartialContent)
		_, _ = response.Write(wantBytes)
	}))
	defer cdn.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		originRequests <- observedRequest{method: request.Method, header: request.Header.Clone()}
		http.Redirect(response, request, cdn.URL+"/media.mp4", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	service := &Service{client: &http.Client{CheckRedirect: playbackRedirectPolicy}}
	incoming := httptest.NewRequest(http.MethodGet, "http://rivune.test/direct.mp4", nil)
	for name, value := range safeHeaders {
		incoming.Header.Set(name, value)
	}
	asset := storedAsset{
		URL: origin.URL + "/media.mp4",
		Headers: map[string]string{
			"Authorization":  "Bearer provider-secret",
			"Cookie":         "provider_session=secret",
			"X-Provider-Key": "provider-secret",
		},
	}
	upstream, err := service.fetchAsset(incoming.Context(), incoming, asset, asset.URL)
	if err != nil {
		t.Fatalf("fetch cross-origin ranged asset: %v", err)
	}
	defer upstream.Body.Close()
	body, err := io.ReadAll(upstream.Body)
	if err != nil {
		t.Fatalf("read cross-origin ranged asset: %v", err)
	}
	if upstream.StatusCode != http.StatusPartialContent || upstream.Request.Method != http.MethodGet ||
		upstream.Header.Get("Content-Range") != fmt.Sprintf("bytes 7-12/%d", len(contents)) || string(body) != string(wantBytes) {
		t.Fatalf("redirected range response status=%d method=%s content-range=%q body=%q", upstream.StatusCode, upstream.Request.Method, upstream.Header.Get("Content-Range"), body)
	}

	originRequest := <-originRequests
	if originRequest.method != http.MethodGet || originRequest.header.Get("Range") != wantRange ||
		originRequest.header.Get("Authorization") == "" || originRequest.header.Get("Cookie") == "" || originRequest.header.Get("X-Provider-Key") == "" {
		t.Fatalf("origin request method=%s headers=%v", originRequest.method, originRequest.header)
	}
	redirectedRequest := <-redirectedRequests
	if redirectedRequest.method != http.MethodGet {
		t.Fatalf("redirected request method = %s", redirectedRequest.method)
	}
	for name, want := range safeHeaders {
		if got := redirectedRequest.header.Get(name); got != want {
			t.Fatalf("redirected request %s = %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{"Authorization", "Cookie", "X-Provider-Key"} {
		if got := redirectedRequest.header.Get(name); got != "" {
			t.Fatalf("redirected request leaked %s: %q", name, got)
		}
	}
}

type playbackRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip playbackRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestFetchAssetSanitizesRoundTripURLAndPreservesCause(t *testing.T) {
	secretURL := "https://provider.example/private/master.m3u8?target=key.bin&token=playback-secret"
	networkCause := errors.New("connection reset")
	service := &Service{client: &http.Client{Transport: playbackRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, networkCause
	})}}
	incoming := httptest.NewRequest(http.MethodGet, "http://rivune.test/asset", nil)

	response, err := service.fetchAsset(context.Background(), incoming, storedAsset{URL: secretURL}, secretURL)

	if response != nil {
		_ = response.Body.Close()
		t.Fatal("failed RoundTripper returned a response")
	}
	if !errors.Is(err, networkCause) {
		t.Fatalf("sanitized playback error lost network cause: %v", err)
	}
	var requestErr *url.Error
	if !errors.As(err, &requestErr) || requestErr.Op != "Get" || requestErr.URL != "" {
		t.Fatalf("playback URL error was not sanitized with operation intact: %#v", requestErr)
	}
	if strings.Contains(err.Error(), secretURL) ||
		strings.Contains(err.Error(), "/private/") ||
		strings.Contains(err.Error(), "playback-secret") {
		t.Fatalf("playback error exposed upstream request destination: %v", err)
	}
}

func TestFetchAssetSanitizesRedirectDestination(t *testing.T) {
	secretRedirect := "https://cdn.example/private/variant.m3u8?target=key.bin&token=redirect-secret"
	redirectCause := errors.New("redirect refused")
	service := &Service{client: &http.Client{
		Transport: playbackRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{secretRedirect}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    request,
			}, nil
		}),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return redirectCause
		},
	}}
	incoming := httptest.NewRequest(http.MethodGet, "http://rivune.test/asset", nil)

	response, err := service.fetchAsset(
		context.Background(),
		incoming,
		storedAsset{URL: "https://provider.example/master.m3u8"},
		"https://provider.example/master.m3u8",
	)

	if response != nil {
		_ = response.Body.Close()
		t.Fatal("refused redirect returned a response")
	}
	if !errors.Is(err, redirectCause) {
		t.Fatalf("sanitized redirect error lost cause: %v", err)
	}
	if strings.Contains(err.Error(), secretRedirect) ||
		strings.Contains(err.Error(), "/private/") ||
		strings.Contains(err.Error(), "redirect-secret") {
		t.Fatalf("redirect error exposed destination: %v", err)
	}
	var requestErr *url.Error
	if !errors.As(err, &requestErr) || requestErr.URL != "" {
		t.Fatalf("redirect URL error was not sanitized: %#v", requestErr)
	}
}

func TestFetchHLSChildDoesNotForwardProviderHeadersCrossOrigin(t *testing.T) {
	var captured http.Header
	service := &Service{client: &http.Client{Transport: playbackRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		captured = request.Header.Clone()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("segment")),
			Request:    request,
		}, nil
	})}}
	incoming := httptest.NewRequest(http.MethodGet, "http://rivune.test/asset", nil)
	incoming.Header.Set("Range", "bytes=0-1023")
	response, err := service.fetchAsset(context.Background(), incoming, storedAsset{
		URL: "https://provider.example/master.m3u8",
		Headers: map[string]string{
			"Authorization":  "Bearer provider-secret",
			"Cookie":         "provider_session=secret",
			"X-Provider-Key": "secret",
		},
	}, "https://cdn.example/segment.ts")
	if err != nil {
		t.Fatalf("fetch cross-origin HLS child: %v", err)
	}
	_ = response.Body.Close()
	for _, name := range []string{"Authorization", "Cookie", "X-Provider-Key"} {
		if got := captured.Get(name); got != "" {
			t.Fatalf("cross-origin HLS child retained %s: %q", name, got)
		}
	}
	if got := captured.Get("Range"); got != "bytes=0-1023" {
		t.Fatalf("cross-origin HLS child Range = %q", got)
	}
	if got := captured.Get("User-Agent"); got != "Rivune-Playback/1" {
		t.Fatalf("cross-origin HLS child User-Agent = %q", got)
	}
	captured = nil
	response, err = service.fetchAsset(
		context.Background(),
		incoming,
		storedAsset{
			URL:     "https://provider.example/master.m3u8",
			Headers: map[string]string{"Authorization": "Bearer provider-secret"},
		},
		"https://provider.example/segment.ts",
	)
	if err != nil {
		t.Fatalf("fetch same-origin HLS child: %v", err)
	}
	_ = response.Body.Close()
	if got := captured.Get("Authorization"); got != "Bearer provider-secret" {
		t.Fatalf("same-origin HLS child Authorization = %q", got)
	}
}

func TestFetchAssetScopesCredentialsToCanonicalMediaOrigin(t *testing.T) {
	tests := []struct {
		name        string
		assetURL    string
		upstreamURL string
		wantSecret  bool
	}{
		{name: "default HTTPS port", assetURL: "https://media.example:443/master.m3u8", upstreamURL: "https://media.example/segment.ts", wantSecret: true},
		{name: "terminal hostname point", assetURL: "https://media.example./master.m3u8", upstreamURL: "https://MEDIA.EXAMPLE/segment.ts", wantSecret: true},
		{name: "different port", assetURL: "https://media.example/master.m3u8", upstreamURL: "https://media.example:444/segment.ts"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var captured http.Header
			service := &Service{client: &http.Client{Transport: playbackRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				captured = request.Header.Clone()
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("media")), Request: request}, nil
			})}}
			incoming := httptest.NewRequest(http.MethodGet, "http://rivune.test/asset", nil)
			response, err := service.fetchAsset(context.Background(), incoming, storedAsset{
				URL: test.assetURL,
				Headers: map[string]string{
					"Authorization":        "Bearer provider-secret",
					"Proxy-Authorization":  "Basic proxy-secret",
					"X-Injected\r\nHeader": "injected-name",
					"X-Injected":           "value\r\ninjected-value",
				},
			}, test.upstreamURL)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if got := captured.Get("Authorization"); (got != "") != test.wantSecret {
				t.Fatalf("Authorization = %q, want present=%t", got, test.wantSecret)
			}
			for _, forbidden := range []string{"Proxy-Authorization", "X-Injected", "X-Injected\r\nHeader"} {
				if got := captured.Get(forbidden); got != "" {
					t.Fatalf("malformed or proxy header %q forwarded as %q", forbidden, got)
				}
			}
		})
	}
}

func TestFetchProxyAssetRetriesPartialHLSWithoutRange(t *testing.T) {
	requests := make([]http.Header, 0, 2)
	service := &Service{client: &http.Client{Transport: playbackRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Header.Clone())
		status := http.StatusPartialContent
		body := "#EXTM3U\n#EXTINF:6,\npart"
		header := http.Header{"Content-Type": []string{"application/vnd.apple.mpegurl"}, "Content-Range": []string{"bytes 0-23/48"}}
		if len(requests) == 2 {
			status = http.StatusOK
			body = "#EXTM3U\n#EXTINF:6,\nsegment.ts\n#EXT-X-ENDLIST\n"
			header.Del("Content-Range")
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}}
	incoming := httptest.NewRequest(http.MethodGet, "http://rivune.test/asset", nil)
	incoming.Header.Set("Range", "bytes=0-23")
	incoming.Header.Set("If-Range", `"playlist-etag"`)
	asset := storedAsset{URL: "https://provider.example/master.m3u8", Headers: map[string]string{"If-Range": `"provider-etag"`}}

	response, err := service.fetchProxyAsset(context.Background(), incoming, asset, asset.URL, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "segment.ts") || strings.Contains(string(body), "part") {
		t.Fatalf("complete retry response = %d %q", response.StatusCode, body)
	}
	rewritten, err := rewritePlaylist(body, response.Request.URL, func(string) string { return "/opaque/segment.ts" })
	if err != nil {
		t.Fatal(err)
	}
	response.Header.Set("Content-Range", "bytes 0-23/48")
	proxyResponse := httptest.NewRecorder()
	if err := writeUpstreamHLSPlaylist(proxyResponse, response.Header, rewritten); err != nil {
		t.Fatal(err)
	}
	if proxyResponse.Code != http.StatusOK || proxyResponse.Header().Get("Content-Range") != "" || proxyResponse.Header().Get("Content-Length") != fmt.Sprintf("%d", len(rewritten)) {
		t.Fatalf("rewritten response = %d headers=%v", proxyResponse.Code, proxyResponse.Header())
	}
	if len(requests) != 2 || requests[0].Get("Range") != "bytes=0-23" || requests[0].Get("If-Range") == "" || requests[1].Get("Range") != "" || requests[1].Get("If-Range") != "" {
		t.Fatalf("upstream request headers = %#v", requests)
	}
}

func TestHLSSegmentContainerCapabilityIsStrictAndPropagated(t *testing.T) {
	for _, valid := range []string{"", "ts", "mp4"} {
		if err := validateCapabilities(Capabilities{HLSSegmentContainer: valid}); err != nil {
			t.Fatalf("valid HLS segment container %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"mpegts", "fmp4", "TS", " mp4"} {
		if err := validateCapabilities(Capabilities{HLSSegmentContainer: invalid}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid HLS segment container %q error = %v", invalid, err)
		}
	}
	batch := addon.ResourceBatch{Results: []addon.ResourceResult{{
		AddonID: "addon-id", ManifestID: "manifest-id",
		Payload: []byte(`{"streams":[{"url":"https://media.example/movie.mkv"}]}`),
	}}}
	for _, test := range []struct{ capability, want string }{{want: "ts"}, {capability: "ts", want: "ts"}, {capability: "mp4", want: "mp4"}} {
		_, assets, err := normalizeStreams(batch, Capabilities{StreamingProtocols: []string{"http"}, Containers: []string{"mkv"}, HLSSegmentContainer: test.capability})
		if err != nil {
			t.Fatal(err)
		}
		if len(assets) != 1 || assets[0].HLSSegmentContainer != test.want {
			t.Fatalf("capability %q stored assets = %+v, want %q", test.capability, assets, test.want)
		}
	}
}

func TestNormalizeStreamsRanksCompatibleSourcesAndKeepsHeadersPrivate(t *testing.T) {
	batch := addon.ResourceBatch{Results: []addon.ResourceResult{{
		AddonID: "addon-id", ManifestID: "manifest-id", AddonName: "  Header-safe Add-on  ",
		Payload: []byte(`{"streams":[
			{"name":"Unsupported MKV","url":"https://media.example/movie.mkv"},
			{"name":"Playable HLS","url":"https://media.example/master.m3u8","behaviorHints":{"proxyHeaders":{"request":{"Authorization":"Bearer secret"}}}},
			{"name":"YouTube","ytId":"video_123"}
		]}`),
	}}}

	sources, assets, err := normalizeStreams(batch, Capabilities{
		StreamingProtocols: []string{"hls", "youtube"},
		Containers:         []string{"mp4", "webm"},
	})
	if err != nil {
		t.Fatalf("normalize streams: %v", err)
	}

	if len(sources) != 3 || sources[0].Name != "Playable HLS" || sources[0].AddonName != "Header-safe Add-on" || !sources[0].Compatible || sources[1].Mode != "youtube" || sources[2].Compatible {
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

	sources, _, err := normalizeStreams(batch, Capabilities{
		StreamingProtocols: []string{"http"},
		Containers:         []string{"mp4"},
	})
	if err != nil {
		t.Fatalf("normalize streams: %v", err)
	}

	if len(sources) != 1 || !sources[0].Compatible {
		t.Fatalf("expected server-proxied MP4 to be compatible: %+v", sources)
	}
}

func TestNormalizeStreamsProxiesMediaExternalURLAndDropsWebHandoff(t *testing.T) {
	batch := addon.ResourceBatch{Results: []addon.ResourceResult{{
		AddonID: "addon-id", ManifestID: "manifest-id",
		Payload: []byte(`{"streams":[
			{"name":"Downloadable MKV","externalUrl":"https://media.example/movie.mkv"},
			{"name":"Provider page","externalUrl":"https://provider.example/watch"}
		]}`),
	}}}

	sources, assets, err := normalizeStreams(batch, Capabilities{
		StreamingProtocols: []string{"http"}, Containers: []string{"mkv"}, ExternalPlayers: []string{"system"},
	})
	if err != nil {
		t.Fatalf("normalize streams: %v", err)
	}
	if len(sources) != 1 || sources[0].Name != "Downloadable MKV" || sources[0].Mode != "direct" || !sources[0].Compatible || sources[0].Container != "mkv" {
		t.Fatalf("media external URL was not promoted to native playback: %+v", sources)
	}
	if len(assets) != 1 || assets[0].URL != "https://media.example/movie.mkv" || assets[0].Container != "mkv" {
		t.Fatalf("promoted media asset was not retained for the playback proxy: %+v", assets)
	}
}

func TestNormalizeStreamsIntersectsExternalHandoffTransport(t *testing.T) {
	batch := addon.ResourceBatch{Results: []addon.ResourceResult{{
		AddonID: "addon-id", ManifestID: "manifest-id",
		Payload: []byte(`{"streams":[{"name":"Intent","externalUrl":"https://provider.example/watch"},{"name":"Intent Media","externalUrl":"https://provider.example/movie.mkv"},{"name":"Magnet","infoHash":"0123456789abcdef0123456789abcdef01234567"},{"name":"Invalid Magnet","infoHash":"0123456789abcdef"}]}`),
	}}}
	tests := []struct {
		name    string
		players []string
		want    map[string]string
	}{
		{name: "legacy", players: []string{"system"}, want: map[string]string{"Intent Media": "direct", "Magnet": "external"}},
		{name: "android intent", players: []string{"android_intent"}, want: map[string]string{"Intent Media": "direct"}},
		{name: "android magnet", players: []string{"android_magnet"}, want: map[string]string{"Intent Media": "direct", "Magnet": "external"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sources, _, err := normalizeStreams(batch, Capabilities{ExternalPlayers: test.players})
			if err != nil {
				t.Fatal(err)
			}
			got := make(map[string]string, len(sources))
			for index := range sources {
				got[sources[index].Name] = sources[index].Mode
			}
			if len(got) != len(test.want) {
				t.Fatalf("sources = %+v, want %+v", got, test.want)
			}
			for name, mode := range test.want {
				if got[name] != mode {
					t.Fatalf("sources = %+v, want %+v", got, test.want)
				}
			}
		})
	}
}

type recordingResourceFetcher struct {
	fetchAddonID  string
	fetchPath     addon.ResourcePath
	fetchCalls    int
	fetchAllPath  addon.ResourcePath
	fetchAllCalls int
}

func (fetcher *recordingResourceFetcher) ValidatePlaybackAccess(context.Context, auth.Principal, string) error {
	return nil
}

func (fetcher *recordingResourceFetcher) ValidatePlaybackAccesses(context.Context, auth.Principal, []string) error {
	return nil
}

func (fetcher *recordingResourceFetcher) ValidatePlaybackAccessesTx(context.Context, pgx.Tx, auth.Principal, []string) error {
	return nil
}

func (fetcher *recordingResourceFetcher) FetchPlaybackResource(_ context.Context, _ auth.Principal, addonID string, path addon.ResourcePath) (addon.ResourceResult, error) {
	fetcher.fetchAddonID = addonID
	fetcher.fetchPath = path
	fetcher.fetchCalls++
	return addon.ResourceResult{
		AddonID: addonID, ManifestID: "org.example.live", AddonName: "  Live Add-on  ",
		Payload: []byte(`{"streams":[{"name":"Live","url":"https://media.example/live.m3u8"}]}`),
	}, nil
}

func (fetcher *recordingResourceFetcher) FetchAllPlaybackResources(_ context.Context, _ auth.Principal, path addon.ResourcePath) (addon.ResourceBatch, error) {
	fetcher.fetchAllPath = path
	fetcher.fetchAllCalls++
	return addon.ResourceBatch{Results: []addon.ResourceResult{
		{
			AddonID: "fanout-addon-a", ManifestID: "org.example.streams.a", AddonName: "  First Streams  ",
			Payload: []byte(`{"streams":[{"name":"First Movie","url":"https://media.example/first.mp4"}]}`),
		},
		{
			AddonID: "fanout-addon-b", ManifestID: "org.example.streams.b", AddonName: "Second Streams",
			Payload: []byte(`{"streams":[{"name":"Second Movie","url":"https://media.example/second.mp4"}]}`),
		},
	}}, nil
}

func TestSourcesTargetsRequestedProfileAddon(t *testing.T) {
	profileID := "profile-id"
	current := time.Now()
	grantExpiresAt := current.Add(time.Hour)
	fetcher := &recordingResourceFetcher{}
	service := &Service{
		addons:           fetcher,
		now:              func() time.Time { return current },
		references:       newSourceReferenceStore(time.Now),
		profileTxFactory: testPlaybackProfileTxFactory,
	}
	list, err := service.Sources(context.Background(), auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, SessionID: "session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt}, SourcesInput{
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
	if len(list.Sources) != 1 || list.Sources[0].AddonID != "requested-addon" || list.Sources[0].AddonName != "Live Add-on" || list.Sources[0].SourceRef == "" {
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
				addons:           fetcher,
				now:              func() time.Time { return current },
				references:       newSourceReferenceStore(time.Now),
				profileTxFactory: testPlaybackProfileTxFactory,
			}
			list, err := service.Sources(context.Background(), auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, SessionID: "session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt}, SourcesInput{
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
			if len(list.Sources) != 2 ||
				list.Sources[0].AddonID != "fanout-addon-a" || list.Sources[0].ManifestID != "org.example.streams.a" || list.Sources[0].AddonName != "First Streams" || list.Sources[0].Name != "First Movie" || list.Sources[0].SourceRef == "" ||
				list.Sources[1].AddonID != "fanout-addon-b" || list.Sources[1].ManifestID != "org.example.streams.b" || list.Sources[1].AddonName != "Second Streams" || list.Sources[1].Name != "Second Movie" || list.Sources[1].SourceRef == "" {
				t.Fatalf("fan-out stream provenance was not preserved: %+v", list.Sources)
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

func TestTargetSignatureUsesOpaqueServerKey(t *testing.T) {
	var signingKey [32]byte
	copy(signingKey[:], "server-only-target-signing-key")
	service := Service{targetSigningKey: signingKey}
	sessionID := "session-1"
	assetID := "stream-1"
	token := "client-visible-playback-token"
	target := "https://media.example/segment.ts"

	signature := service.signTarget(sessionID, assetID, target)
	if !service.validTargetSignature(sessionID, assetID, target, signature) {
		t.Fatal("server-signed playback target was rejected")
	}

	clientMAC := hmac.New(sha256.New, []byte(token))
	writeTargetSignaturePayload(clientMAC, sessionID, assetID, target)
	forged := base64.RawURLEncoding.EncodeToString(clientMAC.Sum(nil))
	if service.validTargetSignature(sessionID, assetID, target, forged) {
		t.Fatal("client forged a target signature with its playback token")
	}
	if service.validTargetSignature(sessionID, assetID, "http://127.0.0.1/private", signature) {
		t.Fatal("tampered playback target was accepted")
	}
	if service.validTargetSignature("session-2", assetID, target, signature) {
		t.Fatal("target signature was reusable across playback sessions")
	}
}

func TestTargetCapabilityConcealsAndBindsProviderURL(t *testing.T) {
	var capabilityKey [32]byte
	copy(capabilityKey[:], "server-only-capability-key")
	service := Service{targetCapabilityKey: capabilityKey}
	const target = "https://provider.example/private/segment.ts?provider_token=secret"

	capability, err := service.sealTargetCapability("session-1", "stream-1", target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(capability, "provider") || strings.Contains(capability, "secret") {
		t.Fatalf("target capability exposed provider URL: %s", capability)
	}
	opened, err := service.openTargetCapability("session-1", "stream-1", capability)
	if err != nil || opened != target {
		t.Fatalf("opened target = %q, err = %v", opened, err)
	}
	if _, err := service.openTargetCapability("session-2", "stream-1", capability); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("cross-session capability error = %v", err)
	}
	if _, err := service.openTargetCapability("session-1", "stream-2", capability); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("cross-asset capability error = %v", err)
	}

	childURL := assetURL("session-1", "stream-1", "playback-token", capability)
	if strings.Contains(childURL, "provider.example") || strings.Contains(childURL, "provider_token") || strings.Contains(childURL, "target=") || strings.Contains(childURL, "signature=") {
		t.Fatalf("child URL exposed provider state: %s", childURL)
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

func TestDecidePlaybackSourceAllowsDirectPassthroughWithoutCodecCapabilities(t *testing.T) {
	sources := []Source{{
		ID: "stream-1", Mode: "direct", URL: "https://media.example/movie.mp4", Protocol: "http", Container: "mp4",
	}}
	assets := []storedAsset{{ID: "stream-1", Kind: "stream", URL: sources[0].URL}}
	service := &Service{
		processor: fakeMediaProcessor{info: MediaInspection{
			Container:   "mp4",
			VideoTracks: []MediaTrack{{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720}},
			AudioTracks: []MediaTrack{{Index: 1, Type: "audio", Codec: "aac", Channels: 2}},
		}},
		probes: newMediaProbeCache(time.Now),
	}

	err := service.decidePlaybackSource(context.Background(), sources, assets, Capabilities{
		StreamingProtocols:     []string{"http"},
		Containers:             []string{"mp4"},
		AllowDirectPassthrough: true,
	})

	if err != nil || !sources[0].Compatible || sources[0].Mode != "direct" || sources[0].Media == nil || assets[0].Kind != "stream" {
		t.Fatalf("direct passthrough decision = source=%+v asset=%+v err=%v", sources[0], assets[0], err)
	}
}

func TestDecidePlaybackSourceRejectsMissingCodecCapabilities(t *testing.T) {
	inspection := MediaInspection{
		Container:   "mp4",
		VideoTracks: []MediaTrack{{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720}},
		AudioTracks: []MediaTrack{{Index: 1, Type: "audio", Codec: "aac", Channels: 2}},
	}
	for _, test := range []struct {
		name        string
		videoCodecs []string
		audioCodecs []string
	}{
		{name: "video codecs absent", audioCodecs: []string{"aac"}},
		{name: "audio codecs absent", videoCodecs: []string{"h264"}},
		{name: "all codecs absent"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sources := []Source{{
				ID: "stream-1", Mode: "direct", URL: "https://media.example/movie.mp4", Protocol: "http", Container: "mp4",
			}}
			assets := []storedAsset{{ID: "stream-1", Kind: "stream", URL: sources[0].URL}}
			service := &Service{processor: fakeMediaProcessor{info: inspection}, probes: newMediaProbeCache(time.Now)}

			err := service.decidePlaybackSource(context.Background(), sources, assets, Capabilities{
				StreamingProtocols: []string{"http"}, Containers: []string{"mp4"},
				VideoCodecs: test.videoCodecs, AudioCodecs: test.audioCodecs,
			})

			if !errors.Is(err, ErrClientCapabilityMissing) || sources[0].Compatible || assets[0].Kind != "stream" {
				t.Fatalf("missing codec capability was not rejected: err=%v source=%+v asset=%+v", err, sources[0], assets[0])
			}
		})
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

func TestDecidePlaybackSourceKeepsBestMislabeledFallback(t *testing.T) {
	sources := []Source{
		{ID: "low", Hint: "2160p", Mode: "direct", URL: "https://media.example/low.mkv", Protocol: "http", Container: "mkv"},
		{ID: "high", Hint: "2160p", Mode: "direct", URL: "https://media.example/high.mkv", Protocol: "http", Container: "mkv"},
		{ID: "encoding", Hint: "1080p", Mode: "direct", URL: "https://media.example/encoding.mkv", Protocol: "http", Container: "mkv"},
	}
	assets := []storedAsset{
		{ID: "low", Kind: "stream", URL: sources[0].URL},
		{ID: "high", Kind: "stream", URL: sources[1].URL},
		{ID: "encoding", Kind: "stream", URL: sources[2].URL},
	}
	inspection := func(height int) MediaInspection {
		return MediaInspection{
			Container: "mkv", VideoTracks: []MediaTrack{{Codec: "h264", Height: height}},
			AudioTracks: []MediaTrack{{Codec: "aac", Channels: 2}},
		}
	}
	service := &Service{
		processor: sourceMediaProcessor{
			sources[0].URL: inspection(720),
			sources[1].URL: inspection(1080),
			sources[2].URL: {
				Container: "mkv", VideoTracks: []MediaTrack{{Codec: "vp9", Height: 1080}},
				AudioTracks: []MediaTrack{{Codec: "dts", Channels: 6}},
			},
		},
		probes: newMediaProbeCache(time.Now),
	}
	capabilities := Capabilities{
		StreamingProtocols: []string{"http", "hls"}, Containers: []string{"mp4"},
		VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
		ProcessingModes: []string{processingRemux, processingTranscode},
	}

	if err := service.decidePlaybackSource(context.Background(), sources, assets, capabilities); err != nil {
		t.Fatal(err)
	}
	if sources[0].Compatible || !sources[1].Compatible || sources[1].Mode != processingRemux ||
		sources[2].Compatible || assets[2].Kind != "stream" {
		t.Fatalf("best lossless fallback was not selected ahead of encoding: sources=%+v assets=%+v", sources, assets)
	}
}

func TestDecidePlaybackSourceDefersEncodingForDirectOrRemuxSource(t *testing.T) {
	preferQuality := false
	for _, test := range []struct {
		name, container, wantMode string
	}{
		{name: "direct", container: "mp4", wantMode: "direct"},
		{name: "remux", container: "mkv", wantMode: processingRemux},
	} {
		t.Run(test.name, func(t *testing.T) {
			sources := []Source{
				{ID: "uhd", Hint: "2160p", Mode: "direct", URL: "https://media.example/uhd.mkv", Protocol: "http", Container: "mkv"},
				{ID: "hd", Hint: "1080p", Mode: "direct", URL: "https://media.example/hd." + test.container, Protocol: "http", Container: test.container},
			}
			assets := []storedAsset{
				{ID: "uhd", Kind: "stream", URL: sources[0].URL},
				{ID: "hd", Kind: "stream", URL: sources[1].URL},
			}
			service := &Service{
				processor: sourceMediaProcessor{
					sources[0].URL: {
						Container: "mkv", VideoTracks: []MediaTrack{{Codec: "vp9", Height: 2160}},
						AudioTracks: []MediaTrack{{Codec: "dts", Channels: 6}},
					},
					sources[1].URL: {
						Container: test.container, VideoTracks: []MediaTrack{{Codec: "h264", Height: 1080}},
						AudioTracks: []MediaTrack{{Codec: "aac", Channels: 2}},
					},
				},
				probes: newMediaProbeCache(time.Now),
			}
			capabilities := Capabilities{
				StreamingProtocols: []string{"http", "hls"}, Containers: []string{"mp4"},
				VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
				ProcessingModes:  []string{processingRemux, processingTranscodeAudio, processingTranscode},
				PreferDirectPlay: &preferQuality,
			}

			if err := service.decidePlaybackSource(context.Background(), sources, assets, capabilities); err != nil {
				t.Fatal(err)
			}
			if sources[0].Compatible || assets[0].Kind != "stream" || !sources[1].Compatible || sources[1].Mode != test.wantMode {
				t.Fatalf("encoding beat a playable source: sources=%+v assets=%+v", sources, assets)
			}
			if test.wantMode == processingRemux && assets[1].Kind != processingRemux {
				t.Fatalf("remux plan was not persisted: source=%+v asset=%+v", sources[1], assets[1])
			}
		})
	}
}

func TestDecidePlaybackSourceUsesDirectPreferenceAtEqualQuality(t *testing.T) {
	preferDirect := true
	preferQuality := false
	for _, test := range []struct {
		name                  string
		preference            *bool
		remuxHint, directHint string
		remuxHeight           int
		wantIndex             int
	}{
		{name: "nil prefers direct", remuxHint: "1080p", directHint: "1080p", wantIndex: 1},
		{name: "true prefers direct", preference: &preferDirect, remuxHint: "1080p", directHint: "1080p", wantIndex: 1},
		{name: "false preserves equal-quality order", preference: &preferQuality, remuxHint: "1080p", directHint: "1080p", wantIndex: 0},
		{name: "false preserves quality priority", preference: &preferQuality, remuxHint: "2160p", directHint: "1080p", wantIndex: 0},
		{name: "true preserves quality priority", preference: &preferDirect, remuxHint: "2160p", directHint: "1080p", wantIndex: 0},
		{name: "nil preserves inspected quality without hints", remuxHeight: 2160, wantIndex: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			sources := []Source{
				{ID: "remux", Hint: test.remuxHint, Mode: "direct", URL: "https://media.example/remux.mkv", Protocol: "http", Container: "mkv"},
				{ID: "direct", Hint: test.directHint, Mode: "direct", URL: "https://media.example/direct.mp4", Protocol: "http", Container: "mp4"},
			}
			assets := []storedAsset{
				{ID: "remux", Kind: "stream", URL: sources[0].URL},
				{ID: "direct", Kind: "stream", URL: sources[1].URL},
			}
			remuxHeight := test.remuxHeight
			if remuxHeight == 0 {
				remuxHeight = 1080
				if test.remuxHint == "2160p" {
					remuxHeight = 2160
				}
			}
			service := &Service{
				processor: sourceMediaProcessor{
					sources[0].URL: {
						Container: "mkv", VideoTracks: []MediaTrack{{Codec: "h264", Height: remuxHeight}},
						AudioTracks: []MediaTrack{{Codec: "aac", Channels: 2}},
					},
					sources[1].URL: {
						Container: "mp4", VideoTracks: []MediaTrack{{Codec: "h264", Height: 1080}},
						AudioTracks: []MediaTrack{{Codec: "aac", Channels: 2}},
					},
				},
				probes: newMediaProbeCache(time.Now),
			}
			capabilities := Capabilities{
				StreamingProtocols: []string{"http", "hls"}, Containers: []string{"mp4"},
				VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
				ProcessingModes: []string{processingRemux}, PreferDirectPlay: test.preference,
			}

			if err := service.decidePlaybackSource(context.Background(), sources, assets, capabilities); err != nil {
				t.Fatal(err)
			}
			if !sources[test.wantIndex].Compatible || sources[1-test.wantIndex].Compatible {
				t.Fatalf("unexpected source preference: sources=%+v", sources)
			}
			if (test.wantIndex == 0 && sources[0].Mode != processingRemux) || (test.wantIndex == 1 && sources[1].Mode != "direct") {
				t.Fatalf("unexpected preferred mode: sources=%+v assets=%+v", sources, assets)
			}
		})
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

func TestContainerProfileConditionsChangeDirectEligibilityAndTargetSelection(t *testing.T) {
	capabilities := Capabilities{
		StreamingProtocols: []string{"http", "hls"},
		VideoCodecs:        []string{"h264"}, AudioCodecs: []string{"aac"},
		ProcessingModes: []string{processingTranscode},
		MediaProfiles: []MediaProfile{
			{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", DirectPlay: true},
			{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Transcoding: true},
		},
		ContainerProfiles: []ContainerProfile{
			{
				ContainersCSV: "mp4",
				Conditions:    []ProfileCondition{{Condition: "lessthanequal", Property: "height", Value: "1080", Required: true}},
			},
			{
				ContainersCSV: "webm",
				Conditions:    []ProfileCondition{{Condition: "lessthanequal", Property: "height", Value: "1", Required: true}},
			},
		},
	}
	source := Source{Mode: "direct", Protocol: "http", Container: "mp4"}
	inspection := MediaInspection{
		Container:   "mp4",
		VideoTracks: []MediaTrack{{Codec: "h264", Height: 720}},
		AudioTracks: []MediaTrack{{Codec: "aac", Channels: 2}},
	}
	allowed, allowedDecision := playbackMode(source, inspection, capabilities)
	inspection.VideoTracks[0].Height = 2160
	denied, deniedDecision := playbackMode(source, inspection, capabilities)
	if allowed != "direct" || allowedDecision == nil || allowedDecision.Reason != decisionDirectSupported {
		t.Fatalf("satisfied container condition did not allow direct play: mode=%q decision=%+v", allowed, allowedDecision)
	}
	if denied != processingTranscode || deniedDecision == nil || deniedDecision.Target == nil ||
		deniedDecision.Target.Protocol != "hls" || deniedDecision.Target.Container != "hls" || deniedDecision.Target.VideoCodec != "h264" {
		t.Fatalf("failed container condition did not select the advertised transcode target: mode=%q decision=%+v", denied, deniedDecision)
	}
}

func TestCompactMediaProfileListsPreserveContainerAndAudioMembership(t *testing.T) {
	capabilities := Capabilities{MediaProfiles: []MediaProfile{{
		Container: "mp4", ContainersCSV: "mp4,mkv", VideoCodec: "h264",
		AudioCodec: "aac", AudioCodecsCSV: "aac,eac3",
	}}}
	video := &MediaTrack{Codec: "h264"}
	if !mediaProfileSupported("mkv", video, &MediaTrack{Codec: "eac3"}, capabilities) ||
		mediaProfileSupported("webm", video, &MediaTrack{Codec: "eac3"}, capabilities) ||
		mediaProfileSupported("mkv", video, &MediaTrack{Codec: "dts"}, capabilities) {
		t.Fatalf("compact profile membership was widened or narrowed: %+v", capabilities.MediaProfiles)
	}
}

func TestSessionSourceURLProxiesExternalAndDirectMediaTargets(t *testing.T) {
	for name, source := range map[string]Source{
		"external handoff": {ID: "external-1", Mode: "external", URL: "https://external.example/watch?token=opaque"},
		"direct media":     {ID: "direct-1", Mode: "direct", URL: "https://media.example/movie.mp4", Compatible: true},
	} {
		got := sessionSourceURL(source, nil, "session-id", "session-token")
		if got == source.URL || !strings.HasPrefix(got, "/api/v1/playback/sessions/") {
			t.Fatalf("%s URL was not protected by the session proxy: %q", name, got)
		}
	}
}

func TestSessionSourceMediaTimelineMatchesProcessedHLSPlaylist(t *testing.T) {
	tests := []struct {
		name   string
		source Source
		asset  storedAsset
		want   string
	}{
		{name: "seekable transcode TS", source: Source{ID: "absolute", Mode: processingTranscode, Protocol: "hls"}, asset: storedAsset{ID: "absolute", Kind: processingTranscode, HLSSegmentContainer: "ts", DurationSeconds: 3600}, want: "absolute"},
		{name: "transcode fMP4", source: Source{ID: "relative", Mode: processingTranscode, Protocol: "hls"}, asset: storedAsset{ID: "relative", Kind: processingTranscode, HLSSegmentContainer: "mp4", DurationSeconds: 3600}, want: "relative"},
		{name: "remux TS", source: Source{ID: "remux", Mode: processingRemux, Protocol: "hls"}, asset: storedAsset{ID: "remux", Kind: processingRemux, HLSSegmentContainer: "ts", DurationSeconds: 3600}, want: "relative"},
		{name: "direct HLS", source: Source{ID: "direct", Mode: "direct", Protocol: "hls"}, asset: storedAsset{ID: "direct", Kind: "stream"}},
		{name: "external", source: Source{ID: "external", Mode: "external", Protocol: "external"}, asset: storedAsset{ID: "external", Kind: "stream"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.source.MediaTimeline = sessionSourceMediaTimeline(test.source, []storedAsset{test.asset})
			if test.source.MediaTimeline != test.want {
				t.Fatalf("media timeline = %q, want %q", test.source.MediaTimeline, test.want)
			}
			encoded, err := json.Marshal(test.source)
			if err != nil {
				t.Fatal(err)
			}
			field := `"mediaTimeline":"` + test.want + `"`
			if test.want != "" && !strings.Contains(string(encoded), field) {
				t.Fatalf("source JSON = %s, want %s", encoded, field)
			}
			if test.want == "" && strings.Contains(string(encoded), `"mediaTimeline"`) {
				t.Fatalf("source JSON unexpectedly exposes a timeline: %s", encoded)
			}
		})
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
	if !strings.Contains(joined, "-preset superfast") || !strings.Contains(joined, "-tune zerolatency") {
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

func TestInspectedVideoRangeTypeUsesOnlyFFprobeEvidence(t *testing.T) {
	dovi := []ffprobeSideData{{Type: "DOVI configuration record"}}
	for _, test := range []struct {
		name, tag, transfer, wantRange, wantHDR string
		sideData                                []ffprobeSideData
	}{
		{name: "Dolby Vision tag", tag: "dvh1", wantRange: "DOVI", wantHDR: "dolby_vision"},
		{name: "Dolby Vision side data", sideData: dovi, wantRange: "DOVI", wantHDR: "dolby_vision"},
		{name: "HDR10", transfer: "smpte2084", wantRange: "HDR10", wantHDR: "hdr10"},
		{name: "HLG", transfer: "arib-std-b67", wantRange: "HLG", wantHDR: "hlg"},
		{name: "known SDR", transfer: "bt709", wantRange: "SDR"},
		{name: "unknown", wantRange: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := inspectedVideoRangeType(test.tag, test.transfer, test.sideData); got != test.wantRange {
				t.Fatalf("range=%q want=%q", got, test.wantRange)
			}
			if got := inspectedHDRFormat(test.tag, test.transfer, test.sideData); got != test.wantHDR {
				t.Fatalf("HDR=%q want=%q", got, test.wantHDR)
			}
		})
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

func TestHighBitrateHEVCUsesAudioOnlyTranscodeWhenClientDeclaresDecode(t *testing.T) {
	capabilities := Capabilities{
		StreamingProtocols: []string{"hls"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h265", "h264"}, AudioCodecs: []string{"aac"},
		HDRFormats: []string{"hdr10"}, ProcessingModes: []string{processingTranscodeAudio, processingTranscode},
		MediaProfiles: []MediaProfile{
			{Container: "mp4", VideoCodec: "h265", AudioCodec: "aac", Transcoding: true, MaximumVideoBitDepth: 10},
			{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Transcoding: true, MaximumVideoBitDepth: 8},
		},
		transcodeCapabilities: TranscodeCapabilities{EncodeCodecs: []string{"h264"}},
	}
	inspection := MediaInspection{
		Container: "mkv", HDRFormat: "hdr10", BitrateKbps: 60_000,
		VideoTracks: []MediaTrack{{Codec: "h265", Height: 2160, BitDepth: 10, BitrateKbps: 60_000}},
		AudioTracks: []MediaTrack{{Codec: "truehd", Channels: 8}},
	}
	mode, decision := playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mkv"}, inspection, capabilities)
	if mode != processingTranscodeAudio || decision == nil || decision.VideoAction != "copy" || decision.AudioAction != "transcode" || decision.ToneMapping ||
		decision.Target == nil || decision.Target.VideoCodec != "h265" || decision.Target.VideoBitDepth != 10 || decision.Target.AudioCodec != "aac" {
		t.Fatalf("capable client received unnecessary video conversion: mode=%q decision=%+v", mode, decision)
	}
	inspection.HDRFormat = ""
	capabilities.HDRFormats = nil
	capabilities.MediaProfiles[0].MaximumVideoBitDepth = 8
	if mode, _ = playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mkv"}, inspection, capabilities); mode != processingTranscode {
		t.Fatalf("Main10 SDR was copied to a Main-only client: mode=%q", mode)
	}
	inspection.HDRFormat = "hdr10"
	capabilities.HDRFormats = []string{"hdr10"}
	capabilities.MediaProfiles[0].MaximumVideoBitDepth = 10
	capabilities.MaximumVideoBitrateKbps = 25_000
	if mode, _ = playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mkv"}, inspection, capabilities); mode != processingTranscode {
		t.Fatalf("declared client bitrate cap did not force video conversion: mode=%q", mode)
	}
}

func TestDolbyVisionHDR10BaseUsesAudioOnlyTranscode(t *testing.T) {
	capabilities := Capabilities{
		StreamingProtocols: []string{"hls"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h265", "h264"}, AudioCodecs: []string{"aac"},
		HDRFormats: []string{"sdr", "hdr10", "hlg"}, ProcessingModes: []string{processingTranscodeAudio, processingTranscode},
		MediaProfiles: []MediaProfile{
			{Container: "mp4", VideoCodec: "h265", AudioCodec: "aac", Transcoding: true, MaximumVideoBitDepth: 10},
			{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Transcoding: true, MaximumVideoBitDepth: 8},
		},
		transcodeCapabilities: TranscodeCapabilities{EncodeCodecs: []string{"h264"}},
	}
	inspection := MediaInspection{
		Container: "mkv", HDRFormat: "dolby_vision", BitrateKbps: 60_000,
		VideoTracks: []MediaTrack{{
			Codec: "h265", Height: 2160, BitDepth: 10, BitrateKbps: 60_000,
			DolbyVisionProfile: 8, DolbyVisionBLPresent: true, DolbyVisionCompatibilityID: 1,
		}},
		AudioTracks: []MediaTrack{{Codec: "truehd", Channels: 8}},
	}
	mode, decision := playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mkv"}, inspection, capabilities)
	if mode != processingTranscodeAudio || decision == nil || decision.VideoAction != "copy" || decision.AudioAction != "transcode" || decision.ToneMapping ||
		decision.Target == nil || decision.Target.VideoCodec != "h265" || decision.Target.VideoBitDepth != 10 || decision.Target.AudioCodec != "aac" {
		t.Fatalf("HDR10-compatible Dolby Vision base was not copied: mode=%q decision=%+v", mode, decision)
	}
	inspection.VideoTracks[0].DolbyVisionProfile = 5
	inspection.VideoTracks[0].DolbyVisionCompatibilityID = 1
	if mode, decision = playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mkv"}, inspection, capabilities); mode != "" || decision != nil {
		t.Fatalf("Dolby Vision profile 5 without compatible base was accepted: mode=%q decision=%+v", mode, decision)
	}
}

func TestDolbyVisionBaseHDRFormatRecognizesKnownCompatibilityIDs(t *testing.T) {
	tests := []struct {
		profile          int
		baseLayerPresent bool
		compatibilityID  int
		want             string
	}{
		{profile: 8, baseLayerPresent: false, compatibilityID: 1},
		{profile: 8, baseLayerPresent: true, compatibilityID: 0},
		{profile: 8, baseLayerPresent: true, compatibilityID: 1, want: "hdr10"},
		{profile: 8, baseLayerPresent: true, compatibilityID: 2, want: "sdr"},
		{profile: 8, baseLayerPresent: true, compatibilityID: 4, want: "hlg"},
		{profile: 8, baseLayerPresent: true, compatibilityID: 3},
		{profile: 5, baseLayerPresent: true, compatibilityID: 1},
	}
	for _, test := range tests {
		if got := dolbyVisionBaseHDRFormat(test.profile, test.baseLayerPresent, test.compatibilityID); got != test.want {
			t.Errorf("profile=%d base=%t compatibility=%d format=%q want=%q", test.profile, test.baseLayerPresent, test.compatibilityID, got, test.want)
		}
	}
}

func TestCodecProfileConditionsSelectDirectOrHLSFromInspectedMedia(t *testing.T) {
	base := Capabilities{
		StreamingProtocols: []string{"http", "hls"}, Containers: []string{"mp4"},
		VideoCodecs: []string{"h265", "h264", "av1"}, AudioCodecs: []string{"aac"},
		ProcessingModes: []string{processingTranscodeAudio, processingTranscode},
		MediaProfiles: []MediaProfile{
			{Container: "mp4", VideoCodec: "h265", AudioCodec: "aac", DirectPlay: true, SupportsNonDolbyVisionHDR: true, MaximumVideoLevel: 153, ExcludedVideoRange: "DOVI", VideoRangeRequired: true},
			{Container: "mp4", VideoCodec: "av1", AudioCodec: "aac", DirectPlay: true},
			{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", DirectPlay: true},
			{Container: "mp4", VideoCodec: "h265", AudioCodec: "aac", Transcoding: true},
			{Container: "mp4", VideoCodec: "av1", AudioCodec: "aac", Transcoding: true},
			{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Transcoding: true},
		},
	}
	tests := []struct {
		name, codec, videoRange, hdr     string
		level, channels, maximumChannels int
		requiredConditionUnknown         bool
		dolbyVisionBLPresent             bool
		dolbyVisionCompatibilityID       int
		want                             string
	}{
		{name: "HEVC level boundary", codec: "hevc", videoRange: "SDR", level: 153, channels: 6, maximumChannels: 6, want: "direct"},
		{name: "HEVC level above boundary", codec: "hevc", videoRange: "SDR", level: 154, channels: 6, maximumChannels: 6, want: processingTranscode},
		{name: "optional unknown level", codec: "hevc", videoRange: "SDR", channels: 6, maximumChannels: 6, want: "direct"},
		{name: "required unknown range", codec: "hevc", level: 153, channels: 6, maximumChannels: 6, want: processingTranscode},
		{name: "required unknown condition", codec: "hevc", videoRange: "SDR", level: 153, channels: 6, maximumChannels: 6, requiredConditionUnknown: true, want: processingTranscode},
		{name: "Dolby Vision", codec: "hevc", videoRange: "DOVI", hdr: "dolby_vision", level: 153, channels: 6, maximumChannels: 6, dolbyVisionBLPresent: true, dolbyVisionCompatibilityID: 1, want: processingTranscode},
		{name: "HDR10 HEVC", codec: "hevc", videoRange: "HDR10", hdr: "hdr10", level: 153, channels: 6, maximumChannels: 6, want: "direct"},
		{name: "HDR10 unrelated codec", codec: "h264", videoRange: "HDR10", hdr: "hdr10", channels: 2, maximumChannels: 2, want: processingTranscode},
		{name: "AV1", codec: "av1", channels: 2, maximumChannels: 2, want: "direct"},
		{name: "stereo downmix", codec: "h264", channels: 6, maximumChannels: 2, want: processingTranscodeAudio},
		{name: "six channel direct", codec: "h264", channels: 6, maximumChannels: 6, want: "direct"},
		{name: "eight channel direct", codec: "h264", channels: 8, maximumChannels: 8, want: "direct"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capabilities := base
			capabilities.MaximumAudioChannels = test.maximumChannels
			if test.requiredConditionUnknown {
				capabilities.MediaProfiles = append([]MediaProfile(nil), base.MediaProfiles...)
				capabilities.MediaProfiles[0].RequiredConditionUnknown = true
			}
			mode, decision := playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mp4"}, MediaInspection{
				Container: "mp4", HDRFormat: test.hdr,
				VideoTracks: []MediaTrack{{Codec: test.codec, Level: test.level, VideoRangeType: test.videoRange, Height: 1080, DolbyVisionBLPresent: test.dolbyVisionBLPresent, DolbyVisionCompatibilityID: test.dolbyVisionCompatibilityID}},
				AudioTracks: []MediaTrack{{Codec: "aac", Channels: test.channels}},
			}, capabilities)
			if mode != test.want || decision == nil {
				t.Fatalf("mode=%q decision=%+v want=%q", mode, decision, test.want)
			}
			if mode != "direct" && (decision.Target == nil || decision.Target.Protocol != "hls") {
				t.Fatalf("condition failure did not select HLS: %+v", decision)
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

func TestSoftwareToneMapTargetHeightIsBounded(t *testing.T) {
	inspection := MediaInspection{VideoTracks: []MediaTrack{{Codec: "h265", Height: 2160}}}
	capabilities := Capabilities{MaximumHeight: 2160, ToneMapMaximumHeight: softwareToneMapMaximumHeight, TranscodeVideoBitrateKbps: 12000}
	decision := processingDecision(decisionVideoTranscodeRequired, "transcode", "transcode", inspection, capabilities, true)
	if decision.Target == nil || decision.Target.Height != softwareToneMapMaximumHeight {
		t.Fatalf("software tone-map target = %+v, want %dp", decision.Target, softwareToneMapMaximumHeight)
	}
	decision = processingDecision(decisionVideoTranscodeRequired, "transcode", "transcode", inspection, capabilities, false)
	if decision.Target == nil || decision.Target.Height != 2160 {
		t.Fatalf("SDR transcode target was capped by tone-map limit: %+v", decision.Target)
	}
	capabilities.MaximumHeight = 720
	decision = processingDecision(decisionVideoTranscodeRequired, "transcode", "transcode", inspection, capabilities, true)
	if decision.Target == nil || decision.Target.Height != 720 {
		t.Fatalf("client height limit lost precedence: %+v", decision.Target)
	}
}

func TestPlaybackCapabilitiesOnlyApplySoftwareToneMapLimit(t *testing.T) {
	software := (&Service{processor: &FFmpegProcessor{encoder: videoEncoder{kind: videoEncoderVAAPI}}}).playbackCapabilities(Capabilities{}, 2160, 12000)
	if software.ToneMapMaximumHeight != softwareToneMapMaximumHeight {
		t.Fatalf("software tone-map maximum height = %d", software.ToneMapMaximumHeight)
	}
	hardware := (&Service{processor: &FFmpegProcessor{encoder: videoEncoder{kind: videoEncoderVAAPI, toneMapBackend: videoToneMapVulkan}}}).playbackCapabilities(Capabilities{}, 2160, 12000)
	if hardware.ToneMapMaximumHeight != 0 {
		t.Fatalf("hardware tone mapping was capped at %dp", hardware.ToneMapMaximumHeight)
	}
	hybridProcessor := &FFmpegProcessor{encoder: videoEncoder{kind: videoEncoderVAAPI, toneMapBackend: videoToneMapHybrid}}
	hybrid := (&Service{processor: hybridProcessor}).playbackCapabilities(Capabilities{}, 2160, 12000)
	if hybridProcessor.HardwareToneMap() || hybrid.ToneMapMaximumHeight != softwareToneMapMaximumHeight {
		t.Fatalf("hybrid CPU tone mapping reported hardware=%t or cap=%dp, want %dp", hybridProcessor.HardwareToneMap(), hybrid.ToneMapMaximumHeight, softwareToneMapMaximumHeight)
	}
	decision := processingDecision(
		decisionVideoTranscodeRequired,
		"transcode",
		"transcode",
		MediaInspection{VideoTracks: []MediaTrack{{Codec: "h265", Height: 2160}}},
		hybrid,
		true,
	)
	if decision == nil || decision.Target == nil || decision.Target.Height != softwareToneMapMaximumHeight {
		t.Fatalf("hybrid tone-map decision = %+v, want realtime cap %dp", decision, softwareToneMapMaximumHeight)
	}
	explicitSoftwareProcessor := &FFmpegProcessor{
		hardwareAcceleration: "software",
		encoder:              videoEncoder{kind: videoEncoderSoftware},
	}
	explicitSoftware := (&Service{processor: explicitSoftwareProcessor}).playbackCapabilities(Capabilities{}, 2160, 12000)
	if explicitSoftware.ToneMapMaximumHeight != 0 {
		t.Fatalf("explicit software tone mapping was capped at %dp", explicitSoftware.ToneMapMaximumHeight)
	}
	explicitSoftwareDecision := processingDecision(
		decisionVideoTranscodeRequired,
		"transcode",
		"transcode",
		MediaInspection{VideoTracks: []MediaTrack{{Codec: "h265", Height: 2160}}},
		explicitSoftware,
		true,
	)
	if explicitSoftwareDecision == nil || explicitSoftwareDecision.Target == nil || explicitSoftwareDecision.Target.Height != 2160 {
		t.Fatalf("explicit software tone-map decision = %+v, want requested 2160p", explicitSoftwareDecision)
	}
	automaticSoftware := (&Service{processor: &FFmpegProcessor{hardwareAcceleration: "auto"}}).playbackCapabilities(Capabilities{}, 2160, 12000)
	if automaticSoftware.ToneMapMaximumHeight != softwareToneMapMaximumHeight {
		t.Fatalf("automatic software fallback maximum height = %dp, want %dp", automaticSoftware.ToneMapMaximumHeight, softwareToneMapMaximumHeight)
	}
}

func TestApplyPlaybackDecisionPersistsPrivateVideoBitDepth(t *testing.T) {
	sources := []Source{{ID: "source"}}
	assets := []storedAsset{{ID: "source"}}
	inspection := MediaInspection{VideoTracks: []MediaTrack{{Codec: "h265", Height: 2160, BitDepth: 10}}}
	decision := processingDecision(decisionVideoTranscodeRequired, "transcode", "transcode", inspection, Capabilities{}, true)
	applyPlaybackDecision(sources, assets, sourceCandidate{}, inspection, processingTranscode, decision, Capabilities{})
	encoded, err := json.Marshal(assets[0])
	if err != nil {
		t.Fatal(err)
	}
	var restored storedAsset
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.VideoBitDepth != 10 {
		t.Fatalf("restored private video bit depth = %d", restored.VideoBitDepth)
	}
}

func TestFullTranscodeToneMappingUsesVideoHDRProfileSupport(t *testing.T) {
	capabilities := Capabilities{
		StreamingProtocols: []string{"hls"}, Containers: []string{"mp4"},
		VideoCodecs: []string{"h265", "h264"}, AudioCodecs: []string{"aac"},
		ProcessingModes: []string{processingTranscode},
		MediaProfiles: []MediaProfile{
			{Container: "mp4", VideoCodec: "h265", AudioCodec: "aac", DirectPlay: true, SupportsNonDolbyVisionHDR: true},
			{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Transcoding: true},
		},
	}
	for _, test := range []struct {
		name, hdr                  string
		dolbyVisionProfile         int
		dolbyVisionBLPresent       bool
		dolbyVisionCompatibilityID int
		wantTone                   bool
	}{
		{name: "HDR10 profile support still tone maps to Main 8", hdr: "hdr10", wantTone: true},
		{name: "HLG profile support still tone maps to Main 8", hdr: "hlg", wantTone: true},
		{name: "Dolby Vision HDR10 base tone maps to Main 8", hdr: "dolby_vision", dolbyVisionProfile: 8, dolbyVisionBLPresent: true, dolbyVisionCompatibilityID: 1, wantTone: true},
		{name: "Dolby Vision SDR base stays SDR", hdr: "dolby_vision", dolbyVisionProfile: 8, dolbyVisionBLPresent: true, dolbyVisionCompatibilityID: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			mode, decision := playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mkv"}, MediaInspection{
				Container: "mkv", HDRFormat: test.hdr,
				VideoTracks: []MediaTrack{{
					Codec: "h265", Height: 1080, DolbyVisionProfile: test.dolbyVisionProfile, DolbyVisionBLPresent: test.dolbyVisionBLPresent,
					DolbyVisionCompatibilityID: test.dolbyVisionCompatibilityID,
				}},
				AudioTracks: []MediaTrack{{Codec: "aac", Channels: 2}},
			}, capabilities)
			if mode != processingTranscode || decision == nil || decision.ToneMapping != test.wantTone {
				t.Fatalf("mode=%q decision=%+v want tone-map=%t", mode, decision, test.wantTone)
			}
		})
	}
}

func TestDolbyVisionDirectPlayAndProfile5FailClosed(t *testing.T) {
	inspection := MediaInspection{
		Container: "mp4", HDRFormat: "dolby_vision",
		VideoTracks: []MediaTrack{{Codec: "h265", Height: 1080, DolbyVisionProfile: 5, DolbyVisionBLPresent: true}},
		AudioTracks: []MediaTrack{{Codec: "aac", Channels: 2}},
	}
	compatible := Capabilities{
		StreamingProtocols: []string{"http", "hls"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h265", "h264"}, AudioCodecs: []string{"aac"},
		HDRFormats: []string{"dolby_vision"}, ProcessingModes: []string{processingTranscode},
		MediaProfiles: []MediaProfile{
			{Container: "mp4", VideoCodec: "h265", AudioCodec: "aac", DirectPlay: true},
			{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Transcoding: true},
		},
	}
	mode, decision := playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mp4"}, inspection, compatible)
	if mode != "direct" || decision == nil || decision.ToneMapping {
		t.Fatalf("compatible Dolby Vision client did not direct play: mode=%q decision=%+v", mode, decision)
	}
	incompatible := compatible
	incompatible.HDRFormats = nil
	mode, decision = playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mp4"}, inspection, incompatible)
	if mode != "" || decision != nil {
		t.Fatalf("profile 5 without HDR-compatible base promised generic transcode: mode=%q decision=%+v", mode, decision)
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
		ContainerProfiles: []ContainerProfile{{ContainersCSV: "mp4", Conditions: []ProfileCondition{{Condition: "lessthanequal", Property: "height", Value: "1080", Required: true}}}},
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
		SubtitleTrackIndex: &subtitleIndex, SubtitleTrackType: subtitleBurnBitmap, SubtitleTrackOrdinal: 2,
		TargetHeight: 720, VideoBitrateKbps: 4500, MaximumAudioChannels: 1,
		Decision: &PlaybackDecision{Source: &PlaybackDecisionSource{Height: 2160}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	for _, expected := range []string{
		"-filter_complex [0:v:0][0:s:2]overlay=eof_action=pass:repeatlast=0,scale=-2:720[vout]",
		"-map [vout]", "-maxrate 4500k", "-bufsize 9000k", "-ac 1",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("arguments missing %q: %v", expected, arguments)
		}
	}
	if strings.Contains(joined, "-b:v") {
		t.Fatalf("software capped CRF unexpectedly selected target-bitrate mode: %v", arguments)
	}
	if strings.Contains(joined, "sh -c") || strings.Contains(joined, "bash -c") {
		t.Fatalf("subtitle burn escaped the argument-array runner: %v", arguments)
	}
}

func TestSubtitleBurnArgumentsDistinguishTextBitmapAndRejectInjection(t *testing.T) {
	index := 9
	processor := &FFmpegProcessor{threads: 2, encoder: videoEncoder{kind: videoEncoderSoftware}}
	for _, test := range []struct {
		name, burnType, want string
	}{
		{name: "ASS or SRT text", burnType: subtitleBurnText, want: `[0:v:0]subtitles=filename='https\://media.example/movie.mkv':si=1[vout]`},
		{name: "PGS DVB or XSub bitmap", burnType: subtitleBurnBitmap, want: "[0:v:0][0:s:1]overlay=eof_action=pass:repeatlast=0[vout]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			arguments, err := processor.processingArguments(storedAsset{
				Kind: processingTranscode, URL: "https://media.example/movie.mkv", SubtitleTrackIndex: &index,
				SubtitleTrackType: test.burnType, SubtitleTrackOrdinal: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			filterIndex := argumentIndex(arguments, "-filter_complex")
			if filterIndex < 0 || filterIndex+1 >= len(arguments) || arguments[filterIndex+1] != test.want {
				t.Fatalf("filter graph = %v, want %q", arguments, test.want)
			}
		})
	}
	seekArguments, err := processor.processingArguments(storedAsset{
		Kind: processingTranscode, URL: "https://media.example/movie.mkv", StartSeconds: 300,
		SubtitleTrackIndex: &index, SubtitleTrackType: subtitleBurnText, SubtitleTrackOrdinal: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	seekFilter := argumentIndex(seekArguments, "-filter_complex")
	wantSeekFilter := `[0:v:0]setpts=PTS+300/TB,subtitles=filename='https\://media.example/movie.mkv':si=1,setpts=PTS-300/TB[vout]`
	if seekFilter < 0 || seekFilter+1 >= len(seekArguments) || seekArguments[seekFilter+1] != wantSeekFilter {
		t.Fatalf("seeked text burn graph = %v, want %q", seekArguments, wantSeekFilter)
	}
	_, err = processor.processingArguments(storedAsset{
		Kind: processingTranscode, URL: "https://media.example/movie.mkv", SubtitleTrackIndex: &index,
		SubtitleTrackType: "text,scale=1:1", SubtitleTrackOrdinal: 1,
	})
	if !errors.Is(err, ErrMediaProcessingFailed) {
		t.Fatalf("injected subtitle type error = %v", err)
	}
	_, err = processor.processingArguments(storedAsset{
		Kind: processingTranscode, URL: "https://media.example/movie.mkv;scale=1:1", SubtitleTrackIndex: &index,
		SubtitleTrackType: subtitleBurnText, SubtitleTrackOrdinal: 1,
	})
	if !errors.Is(err, ErrMediaProcessingFailed) {
		t.Fatalf("injected subtitle source error = %v", err)
	}
}

func TestTranscodeArgumentsRejectUnprovenDolbyVisionToneMap(t *testing.T) {
	processor := &FFmpegProcessor{threads: 2, encoder: videoEncoder{kind: videoEncoderSoftware}}
	asset := storedAsset{
		Kind: processingTranscode, URL: "https://media.example/movie.mp4", ToneMap: true,
		Decision: &PlaybackDecision{Source: &PlaybackDecisionSource{VideoCodec: "h265", HDRFormat: "dolby_vision"}},
	}
	if _, err := processor.processingArguments(asset); !errors.Is(err, ErrMediaProcessingFailed) {
		t.Fatalf("unproven Dolby Vision tone-map error = %v", err)
	}
	asset.DolbyVisionToneMapSafe = true
	if _, err := processor.processingArguments(asset); err != nil {
		t.Fatalf("proven Dolby Vision compatible base rejected: %v", err)
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

type emptyPlaybackRows struct{}

func (emptyPlaybackRows) Close()                                       {}
func (emptyPlaybackRows) Err() error                                   { return nil }
func (emptyPlaybackRows) CommandTag() pgconn.CommandTag                { return pgconn.NewCommandTag("") }
func (emptyPlaybackRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (emptyPlaybackRows) Next() bool                                   { return false }
func (emptyPlaybackRows) Scan(...any) error                            { return pgx.ErrNoRows }
func (emptyPlaybackRows) Values() ([]any, error)                       { return nil, nil }
func (emptyPlaybackRows) RawValues() [][]byte                          { return nil }
func (emptyPlaybackRows) Conn() *pgx.Conn                              { return nil }

type playbackSessionIDRow struct {
	id string
}

func (row playbackSessionIDRow) Scan(destinations ...any) error {
	if len(destinations) != 1 {
		return errors.New("unexpected playback session scan destination count")
	}
	destination, ok := destinations[0].(*string)
	if !ok {
		return errors.New("unexpected playback session scan destination")
	}
	*destination = row.id
	return nil
}

type playbackSessionCountsRow struct {
	global, authSession, profile int
}

func (row playbackSessionCountsRow) Scan(destinations ...any) error {
	if len(destinations) != 3 {
		return errors.New("unexpected playback session count destination count")
	}
	values := []int{row.global, row.authSession, row.profile}
	for index, destination := range destinations {
		value, ok := destination.(*int)
		if !ok {
			return errors.New("unexpected playback session count destination")
		}
		*value = values[index]
	}
	return nil
}

type playbackTransactionStub struct {
	testPlaybackProfileTransaction
	commitCalled   bool
	commitErr      error
	exec           func(string, ...any) (pgconn.CommandTag, error)
	query          func(string, ...any) (pgx.Rows, error)
	queryRow       func(string, ...any) pgx.Row
	row            pgx.Row
	queryRowCalled bool
}

func (transaction *playbackTransactionStub) Commit(context.Context) error {
	transaction.commitCalled = true
	return transaction.commitErr
}

func (*playbackTransactionStub) Rollback(context.Context) error { return nil }

func (transaction *playbackTransactionStub) Exec(_ context.Context, query string, arguments ...any) (pgconn.CommandTag, error) {
	if transaction.exec != nil {
		return transaction.exec(query, arguments...)
	}
	if strings.Contains(query, "pg_advisory_xact_lock") {
		return pgconn.NewCommandTag("SELECT 1"), nil
	}
	return pgconn.CommandTag{}, errors.New("unexpected playback transaction exec")
}

func (transaction *playbackTransactionStub) Query(_ context.Context, query string, arguments ...any) (pgx.Rows, error) {
	if transaction.query == nil {
		return nil, errors.New("unexpected playback transaction query")
	}
	return transaction.query(query, arguments...)
}

func (transaction *playbackTransactionStub) QueryRow(_ context.Context, query string, arguments ...any) pgx.Row {
	transaction.queryRowCalled = true
	if transaction.queryRow != nil {
		return transaction.queryRow(query, arguments...)
	}
	if strings.Contains(query, "count(*) FILTER") {
		return playbackSessionCountsRow{}
	}
	if transaction.row == nil {
		return testPlaybackProfileRow{}
	}
	return transaction.row
}

func TestCreateSessionValidatesProvidersInsideTransactionBeforeWrite(t *testing.T) {
	now := time.Now().UTC()
	profileID := "profile-id"
	grantExpiresAt := now.Add(time.Hour)
	queryCalled := false
	transaction := &playbackTransactionStub{query: func(string, ...any) (pgx.Rows, error) {
		queryCalled = true
		return emptyPlaybackRows{}, nil
	}}
	fetcher := &preparationResourceFetcher{validationErr: addon.ErrNotFound}
	service := &Service{
		addons: fetcher, now: func() time.Time { return now },
		profileTxFactory: func(context.Context, auth.Principal) (playbackProfileTransaction, error) {
			return transaction, nil
		},
	}
	principal := auth.Principal{
		SessionID: "auth-session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
	}

	_, err := service.createSession(context.Background(), principal, sourceReference{
		MediaType: "movie", ResourceID: "resource-id",
	}, "", nil, nil, nil, []string{"revoked-addon"}, nil, nil)
	if err != ErrSourceReferenceExpired {
		t.Fatalf("transactional provider denial = %v, want opaque expiry", err)
	}
	if fetcher.txValidationCalls.Load() != 1 || fetcher.validationCalls.Load() != 1 {
		t.Fatalf("transactional provider validations = %d total=%d", fetcher.txValidationCalls.Load(), fetcher.validationCalls.Load())
	}
	if queryCalled || transaction.queryRowCalled || transaction.commitCalled {
		t.Fatalf("provider denial reached session storage: cleanupQuery=%t insert=%t commit=%t", queryCalled, transaction.queryRowCalled, transaction.commitCalled)
	}
}

func TestStopPropagatesProfileTransactionFailure(t *testing.T) {
	now := time.Now().UTC()
	profileID := "profile-id"
	grantExpiresAt := now.Add(time.Hour)
	storageErr := errors.New("profile authorization storage unavailable")
	service := &Service{
		now: func() time.Time { return now },
		profileTxFactory: func(context.Context, auth.Principal) (playbackProfileTransaction, error) {
			return nil, storageErr
		},
	}

	err := service.Stop(context.Background(), auth.Principal{
		SessionID: "auth-session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
	}, "playback-session-id")
	if !errors.Is(err, storageErr) {
		t.Fatalf("Stop error = %v, want storage failure", err)
	}
	if errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Stop hid storage failure as ErrSessionNotFound: %v", err)
	}
}

func TestStopHidesActiveProfileAuthorizationDenial(t *testing.T) {
	now := time.Now().UTC()
	profileID := "profile-id"
	grantExpiresAt := now.Add(time.Hour)
	service := &Service{
		now: func() time.Time { return now },
		profileTxFactory: func(context.Context, auth.Principal) (playbackProfileTransaction, error) {
			return nil, ErrActiveProfileRequired
		},
	}

	err := service.Stop(context.Background(), auth.Principal{
		SessionID: "auth-session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
	}, "playback-session-id")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Stop error = %v, want ErrSessionNotFound", err)
	}
}

func TestCreateSessionCleansOnlyCreatedSessionAfterAuthorizationLoss(t *testing.T) {
	now := time.Now().UTC()
	profileID := "profile-id"
	grantExpiresAt := now.Add(time.Hour)
	const authSessionID = "auth-session-id"
	const createdSessionID = "created-playback-session-id"

	createTransaction := &playbackTransactionStub{
		query: func(string, ...any) (pgx.Rows, error) { return emptyPlaybackRows{}, nil },
		row:   playbackSessionIDRow{id: createdSessionID},
	}
	var cleanupQuery string
	var cleanupArguments []any
	cleanupTransaction := &playbackTransactionStub{
		exec: func(query string, arguments ...any) (pgconn.CommandTag, error) {
			cleanupQuery = query
			cleanupArguments = append([]any(nil), arguments...)
			return pgconn.NewCommandTag("DELETE 1"), nil
		},
	}
	authorizationCalls := 0
	hlsStopped := false
	hlsDone := make(chan struct{})
	close(hlsDone)
	hlsDirectory := t.TempDir()
	service := &Service{
		now: func() time.Time { return now },
		hlsJobs: map[string]*hlsJob{
			createdSessionID + "/source-id": {
				directory: hlsDirectory,
				cancel:    func() { hlsStopped = true },
				done:      hlsDone,
			},
		},
		mediaOptions: MediaOptions{TempDirectory: hlsDirectory},
		profileTxFactory: func(context.Context, auth.Principal) (playbackProfileTransaction, error) {
			authorizationCalls++
			switch authorizationCalls {
			case 1:
				return createTransaction, nil
			case 2:
				return nil, ErrActiveProfileRequired
			default:
				t.Fatalf("unexpected profile authorization transaction %d", authorizationCalls)
				return nil, errors.New("unexpected profile authorization transaction")
			}
		},
		sessionCleanupTxFactory: func(context.Context) (playbackProfileTransaction, error) {
			if authorizationCalls != 2 {
				t.Fatalf("cleanup started before final authorization was denied")
			}
			return cleanupTransaction, nil
		},
	}
	principal := auth.Principal{
		SessionID: authSessionID, ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
	}

	_, err := service.createSession(context.Background(), principal, sourceReference{
		MediaType: "movie", ResourceID: "resource-id",
	}, "", nil, nil, nil, nil, nil, nil)
	if !errors.Is(err, ErrActiveProfileRequired) {
		t.Fatalf("createSession error = %v, want original authorization denial", err)
	}
	if !createTransaction.commitCalled {
		t.Fatal("server-created playback session was not committed before final authorization")
	}
	if !cleanupTransaction.commitCalled {
		t.Fatal("created playback session cleanup was not committed")
	}
	if !strings.Contains(cleanupQuery, "WHERE id::text = $1 AND auth_session_id = $2") {
		t.Fatalf("cleanup query is not scoped by playback and auth session IDs: %s", cleanupQuery)
	}
	if len(cleanupArguments) != 2 || cleanupArguments[0] != createdSessionID || cleanupArguments[1] != authSessionID {
		t.Fatalf("cleanup arguments = %#v, want only created session %q and auth session %q", cleanupArguments, createdSessionID, authSessionID)
	}
	if !hlsStopped {
		t.Fatal("authorization-loss cleanup did not stop the created session HLS job")
	}
	if _, exists := service.hlsJobs[createdSessionID+"/source-id"]; exists {
		t.Fatal("authorization-loss cleanup retained the created session HLS job")
	}
}

func TestCloseDeliverySessionUsesOpaqueHandleAfterLinkedLoginRevocation(t *testing.T) {
	profileID := "11111111-1111-4111-8111-111111111111"
	const (
		authSessionID     = "revoked-auth-session"
		playbackSessionID = "playback-session"
		playbackToken     = "opaque-playback-token"
	)
	var cleanupQuery string
	var cleanupArguments []any
	transaction := &playbackTransactionStub{exec: func(query string, arguments ...any) (pgconn.CommandTag, error) {
		cleanupQuery = query
		cleanupArguments = append([]any(nil), arguments...)
		return pgconn.NewCommandTag("DELETE 1"), nil
	}}
	service := &Service{
		profileTxFactory: func(context.Context, auth.Principal) (playbackProfileTransaction, error) {
			t.Fatal("delivery cleanup re-authorized a revoked linked login")
			return nil, ErrActiveProfileRequired
		},
		sessionCleanupTxFactory: func(context.Context) (playbackProfileTransaction, error) {
			return transaction, nil
		},
	}
	handle := DeliveryHandle{
		sessionID: playbackSessionID, assetID: "stream-id", token: playbackToken,
		children: newDeliveryChildTable(),
	}
	principal := auth.Principal{SessionID: authSessionID, ActiveProfileID: &profileID}
	if err := service.Close(context.Background(), principal, handle); err != nil {
		t.Fatalf("close revoked linked delivery: %v", err)
	}
	if !transaction.commitCalled {
		t.Fatal("delivery cleanup transaction was not committed")
	}
	for _, required := range []string{"id::text = $1", "auth_session_id = $2", "profile_id = $3::uuid", "token_hash = $4"} {
		if !strings.Contains(cleanupQuery, required) {
			t.Fatalf("delivery cleanup query missing %q: %s", required, cleanupQuery)
		}
	}
	if len(cleanupArguments) != 4 || cleanupArguments[0] != playbackSessionID || cleanupArguments[1] != authSessionID || cleanupArguments[2] != profileID {
		t.Fatalf("delivery cleanup arguments = %#v", cleanupArguments)
	}
	wantHash := sha256.Sum256([]byte(playbackToken))
	gotHash, ok := cleanupArguments[3].([]byte)
	if !ok || string(gotHash) != string(wantHash[:]) {
		t.Fatalf("delivery cleanup token hash = %#v", cleanupArguments[3])
	}
}

func TestCreateSessionJoinsAuthorizationAndCleanupErrors(t *testing.T) {
	now := time.Now().UTC()
	profileID := "profile-id"
	grantExpiresAt := now.Add(time.Hour)
	cleanupErr := errors.New("cleanup storage unavailable")
	createTransaction := &playbackTransactionStub{
		query: func(string, ...any) (pgx.Rows, error) { return emptyPlaybackRows{}, nil },
		row:   playbackSessionIDRow{id: "created-playback-session-id"},
	}
	authorizationCalls := 0
	service := &Service{
		now: func() time.Time { return now },
		profileTxFactory: func(context.Context, auth.Principal) (playbackProfileTransaction, error) {
			authorizationCalls++
			if authorizationCalls == 1 {
				return createTransaction, nil
			}
			return nil, ErrActiveProfileRequired
		},
		sessionCleanupTxFactory: func(context.Context) (playbackProfileTransaction, error) {
			return nil, cleanupErr
		},
	}
	principal := auth.Principal{
		SessionID: "auth-session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
	}

	_, err := service.createSession(context.Background(), principal, sourceReference{
		MediaType: "movie", ResourceID: "resource-id",
	}, "", nil, nil, nil, nil, nil, nil)
	if !errors.Is(err, ErrActiveProfileRequired) {
		t.Fatalf("createSession error = %v, want original authorization denial", err)
	}
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("createSession error = %v, want joined cleanup failure", err)
	}
}

func TestCreateSessionJoinsHLSAndCleanupErrors(t *testing.T) {
	now := time.Now().UTC()
	profileID := "profile-id"
	grantExpiresAt := now.Add(time.Hour)
	hlsErr := errors.New("HLS startup unavailable")
	cleanupErr := errors.New("cleanup storage unavailable")
	createTransaction := &playbackTransactionStub{
		query: func(string, ...any) (pgx.Rows, error) { return emptyPlaybackRows{}, nil },
		row:   playbackSessionIDRow{id: "created-playback-session-id"},
	}
	service := &Service{
		now: func() time.Time { return now }, processor: &failingHLSProcessor{err: hlsErr},
		mediaOptions: MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 1 << 20, IdleTTL: time.Minute},
		hlsJobs:      make(map[string]*hlsJob),
		profileTxFactory: func(context.Context, auth.Principal) (playbackProfileTransaction, error) {
			return createTransaction, nil
		},
		sessionCleanupTxFactory: func(context.Context) (playbackProfileTransaction, error) {
			return nil, cleanupErr
		},
	}
	principal := auth.Principal{SessionID: "auth-session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt}
	asset := storedAsset{ID: "stream", Kind: processingTranscode, URL: "https://media.example/movie.mkv"}
	source := Source{ID: asset.ID, Compatible: true, Protocol: "hls", Mode: processingTranscode}
	_, err := service.createSession(context.Background(), principal, sourceReference{MediaType: "movie", ResourceID: "resource-id"}, "", []Source{source}, nil, []storedAsset{asset}, nil, nil, nil)
	if !errors.Is(err, hlsErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("createSession HLS compensation error = %v, want both causes", err)
	}
	if len(service.hlsJobs) != 0 || directorySize(service.mediaOptions.TempDirectory) != 0 {
		t.Fatalf("failed HLS compensation left jobs=%d bytes=%d", len(service.hlsJobs), directorySize(service.mediaOptions.TempDirectory))
	}
}

func TestPlaybackMaximumHeightAcceptsOnlyUnsetOrSupportedRange(t *testing.T) {
	for _, height := range []int{-1, 1, 143, 4321} {
		if validPlaybackMaximumHeight(height) {
			t.Fatalf("invalid maximum height %d was accepted", height)
		}
		if err := validateResolveInput(ResolveInput{SourceRef: "1234567890abcdef", MaximumHeight: height}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("resolve maximum height %d error = %v, want invalid input", height, err)
		}
	}
	for _, height := range []int{0, 144, 4320} {
		if !validPlaybackMaximumHeight(height) {
			t.Fatalf("valid maximum height %d was rejected", height)
		}
		if err := validateResolveInput(ResolveInput{SourceRef: "1234567890abcdef", MaximumHeight: height}); err != nil {
			t.Fatalf("resolve maximum height %d error = %v", height, err)
		}
	}
}

type capabilityMediaProcessor struct {
	capabilities TranscodeCapabilities
}

func (processor capabilityMediaProcessor) Probe(context.Context, storedAsset) (MediaInspection, error) {
	return MediaInspection{}, nil
}

func (processor capabilityMediaProcessor) TranscodeCapabilities() TranscodeCapabilities {
	return processor.capabilities
}

func TestPlaybackCapabilitiesInjectEngineTranscodeCapabilities(t *testing.T) {
	processor := capabilityMediaProcessor{capabilities: TranscodeCapabilities{
		HardwareAcceleration: "vaapi", DecodeCodecs: []string{"h264", "hevc"}, EncodeCodecs: []string{"h264", "hevc"}, HEVCMain10: true,
		PreferredVideoCodec: "hevc", QualityPreset: "quality",
	}}
	capabilities := (&Service{processor: processor}).playbackCapabilities(Capabilities{}, 2160, 12000)
	if capabilities.transcodeCapabilities.HardwareAcceleration != "vaapi" ||
		capabilities.transcodeCapabilities.PreferredVideoCodec != "hevc" ||
		capabilities.transcodeCapabilities.QualityPreset != "quality" ||
		!capabilities.transcodeCapabilities.HEVCMain10 ||
		!supportsCodec(capabilities.transcodeCapabilities.DecodeCodecs, "hevc") ||
		!supportsCodec(capabilities.transcodeCapabilities.EncodeCodecs, "hevc") {
		t.Fatalf("engine capabilities were not injected: %+v", capabilities.transcodeCapabilities)
	}
}

func TestTranscodeTargetSelectionRequiresClientEngineAndContainerAgreement(t *testing.T) {
	profiles := []MediaProfile{
		{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Transcoding: true},
		{Container: "mp4", VideoCodec: "hevc", AudioCodec: "aac", Transcoding: true},
		{Container: "mp4", VideoCodec: "av1", AudioCodec: "aac", Transcoding: true},
	}
	for _, test := range []struct {
		name, preferred, segment string
		client, engine           []string
		want                     string
	}{
		{name: "legacy H264", client: []string{"h264"}, want: "h264"},
		{name: "preferred H264", preferred: "h264", segment: "mp4", client: []string{"h264", "hevc", "av1"}, engine: []string{"h264", "hevc", "av1"}, want: "h264"},
		{name: "preferred HEVC fMP4", preferred: "hevc", segment: "mp4", client: []string{"h264", "hevc"}, engine: []string{"h264", "hevc"}, want: "hevc"},
		{name: "preferred AV1 fMP4", preferred: "av1", segment: "mp4", client: []string{"h264", "av1"}, engine: []string{"h264", "av1"}, want: "av1"},
		{name: "HEVC requires fMP4", preferred: "hevc", segment: "ts", client: []string{"h264", "hevc"}, engine: []string{"h264", "hevc"}, want: "h264"},
		{name: "AV1 requires engine", preferred: "av1", segment: "mp4", client: []string{"h264", "av1"}, engine: []string{"h264"}, want: "h264"},
		{name: "HEVC requires client profile", preferred: "hevc", segment: "mp4", client: []string{"h264"}, engine: []string{"h264", "hevc"}, want: "h264"},
	} {
		t.Run(test.name, func(t *testing.T) {
			capabilities := Capabilities{
				StreamingProtocols: []string{"hls"}, Containers: []string{"mp4"}, VideoCodecs: test.client, AudioCodecs: []string{"aac"},
				ProcessingModes: []string{processingTranscode}, MediaProfiles: profiles, HLSSegmentContainer: test.segment,
				transcodeCapabilities: TranscodeCapabilities{EncodeCodecs: test.engine, PreferredVideoCodec: test.preferred, QualityPreset: "quality"},
			}
			if len(test.engine) == 0 {
				capabilities.MediaProfiles = []MediaProfile{{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Transcoding: true}}
			}
			if len(test.client) == 1 && test.client[0] == "h264" {
				capabilities.MediaProfiles = []MediaProfile{{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Transcoding: true}}
			}
			mode, decision := playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mkv"}, MediaInspection{
				Container: "mkv", VideoTracks: []MediaTrack{{Codec: "vp9", Height: 2160}}, AudioTracks: []MediaTrack{{Codec: "aac", Channels: 2}},
			}, capabilities)
			if mode != processingTranscode || decision == nil || decision.Target == nil || decision.Target.VideoCodec != test.want {
				t.Fatalf("mode=%q decision=%+v, want codec %q", mode, decision, test.want)
			}
			sources := []Source{{ID: "source"}}
			assets := []storedAsset{{ID: "source", HLSSegmentContainer: normalizedHLSSegmentContainer(test.segment)}}
			applyPlaybackDecision(sources, assets, sourceCandidate{}, MediaInspection{VideoTracks: []MediaTrack{{Codec: "vp9", Height: 2160}}}, mode, decision, capabilities)
			if assets[0].TargetVideoCodec != test.want || assets[0].QualityPreset != "quality" {
				t.Fatalf("private plan was not persisted: %+v", assets[0])
			}
			encoded, err := json.Marshal(assets[0])
			if err != nil {
				t.Fatal(err)
			}
			var restored storedAsset
			if err := json.Unmarshal(encoded, &restored); err != nil {
				t.Fatal(err)
			}
			if restored.TargetVideoCodec != test.want || restored.QualityPreset != "quality" {
				t.Fatalf("persisted private plan = %+v", restored)
			}
			if (test.want == "hevc" || test.want == "av1") && assets[0].HLSSegmentContainer != "mp4" {
				t.Fatalf("%s target did not retain fMP4: %+v", test.want, assets[0])
			}
		})
	}
}
func TestHEVCMain10TargetRequiresBackendAndMatchingClientProfile(t *testing.T) {
	inspection := MediaInspection{
		Container: "mkv", HDRFormat: "hdr10",
		VideoTracks: []MediaTrack{{Codec: "hevc", Height: 2160, BitDepth: 10}},
		AudioTracks: []MediaTrack{{Codec: "aac", Channels: 2}},
	}
	for _, test := range []struct {
		name          string
		backendMain10 bool
		clientDepth   int
		wantDepth     int
		wantToneMap   bool
	}{
		{name: "Main-only backend and Main10 client", clientDepth: 10, wantDepth: 8, wantToneMap: true},
		{name: "Main10 backend and Main-only client", backendMain10: true, clientDepth: 8, wantDepth: 8, wantToneMap: true},
		{name: "Main10 backend and Main10 client", backendMain10: true, clientDepth: 10, wantDepth: 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			capabilities := Capabilities{
				StreamingProtocols: []string{"hls"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac"},
				HDRFormats: []string{"hdr10"}, ProcessingModes: []string{processingTranscode}, HLSSegmentContainer: "mp4",
				MediaProfiles: []MediaProfile{
					{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Transcoding: true, MaximumVideoBitDepth: 8},
					{Container: "mp4", VideoCodec: "hevc", AudioCodec: "aac", Transcoding: true, MaximumVideoBitDepth: test.clientDepth},
				},
				transcodeCapabilities: TranscodeCapabilities{HardwareAcceleration: "vaapi", DecodeCodecs: []string{"hevc"}, EncodeCodecs: []string{"h264", "hevc"}, HEVCMain10: test.backendMain10, PreferredVideoCodec: "hevc"},
			}
			mode, decision := playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mkv"}, inspection, capabilities)
			if mode != processingTranscode || decision == nil || decision.Target == nil {
				t.Fatalf("mode=%q decision=%+v", mode, decision)
			}
			if decision.Target.VideoCodec != "hevc" || decision.Target.VideoBitDepth != test.wantDepth || decision.ToneMapping != test.wantToneMap {
				t.Fatalf("decision=%+v target=%+v, want depth=%d toneMap=%t", decision, decision.Target, test.wantDepth, test.wantToneMap)
			}
			if test.wantDepth == 10 && (decision.Pipeline == nil || !decision.Pipeline.ZeroCopy) {
				t.Fatalf("probed Main10 plan did not retain hardware frames: %+v", decision.Pipeline)
			}
		})
	}
}

func TestValidateCapabilitiesBoundsMaximumVideoBitDepth(t *testing.T) {
	base := Capabilities{MediaProfiles: []MediaProfile{{Container: "mp4", VideoCodec: "hevc", MaximumVideoBitDepth: 10}}}
	if err := validateCapabilities(base); err != nil {
		t.Fatalf("valid maximum video bit depth rejected: %v", err)
	}
	for _, invalid := range []int{1, 7, 17, 1000} {
		capabilities := cloneCapabilities(base)
		capabilities.MediaProfiles[0].MaximumVideoBitDepth = invalid
		if !errors.Is(validateCapabilities(capabilities), ErrInvalidInput) {
			t.Fatalf("invalid maximum video bit depth %d accepted", invalid)
		}
	}
}

func TestPlaybackDecisionReasonsAndPipelineStayModeAccurate(t *testing.T) {
	capabilities := Capabilities{
		StreamingProtocols: []string{"http", "hls"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
		ProcessingModes: []string{processingRemux, processingTranscode}, MaximumHeight: 1080, MaximumVideoBitrateKbps: 8000,
		MediaProfiles:         []MediaProfile{{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", DirectPlay: true, Transcoding: true}},
		transcodeCapabilities: TranscodeCapabilities{HardwareAcceleration: "vaapi", DecodeCodecs: []string{"hevc"}, EncodeCodecs: []string{"h264"}},
	}
	directMode, direct := playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mp4"}, MediaInspection{
		Container: "mp4", VideoTracks: []MediaTrack{{Codec: "h264", Height: 1080, BitrateKbps: 4000}}, AudioTracks: []MediaTrack{{Codec: "aac", Channels: 2}},
	}, capabilities)
	remuxMode, remux := playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mkv"}, MediaInspection{
		Container: "mkv", VideoTracks: []MediaTrack{{Codec: "h264", Height: 1080, BitrateKbps: 4000}}, AudioTracks: []MediaTrack{{Codec: "aac", Channels: 2}},
	}, capabilities)
	transcodeMode, transcode := playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mkv"}, MediaInspection{
		Container: "mkv", HDRFormat: "hdr10", VideoTracks: []MediaTrack{{Codec: "hevc", Height: 2160, BitrateKbps: 24000}}, AudioTracks: []MediaTrack{{Codec: "dts", Channels: 6}},
	}, capabilities)
	if directMode != "direct" || direct.Pipeline != nil || len(direct.Reasons) != 0 {
		t.Fatalf("direct play received a false pipeline: %+v", direct)
	}
	if remuxMode != processingRemux || remux.Pipeline != nil || !supports(remux.Reasons, reasonContainerNotSupported) {
		t.Fatalf("remux plan/reasons are incoherent: %+v", remux)
	}
	for _, reason := range []string{reasonContainerNotSupported, reasonVideoCodecNotSupported, reasonAudioCodecNotSupported, reasonResolutionLimit, reasonBitrateLimit, reasonHDRNotSupported} {
		if transcodeMode != processingTranscode || transcode.Pipeline == nil || !supports(transcode.Reasons, reason) {
			t.Fatalf("transcode missing %q or pipeline: mode=%q decision=%+v", reason, transcodeMode, transcode)
		}
	}
}

func TestDirectProfileCompatibilityDoesNotCombineDifferentProfiles(t *testing.T) {
	capabilities := Capabilities{StreamingProtocols: []string{"http"}, MediaProfiles: []MediaProfile{
		{Container: "mkv", VideoCodec: "hevc", AudioCodec: "ac3", DirectPlay: true},
		{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", DirectPlay: true},
	}}
	video := &MediaTrack{Codec: "h264"}
	audio := &MediaTrack{Codec: "aac", Channels: 2}
	containerSupported, videoSupported, audioSupported := directProfileCompatibility("mkv", video, audio, capabilities)
	if !containerSupported || videoSupported || audioSupported {
		t.Fatalf("cross-container profiles were combined: container=%t video=%t audio=%t", containerSupported, videoSupported, audioSupported)
	}
	reasons := directIncompatibilityReasons(Source{Protocol: "http", Container: "mkv"}, MediaInspection{
		Container: "mkv", VideoTracks: []MediaTrack{*video}, AudioTracks: []MediaTrack{*audio},
	}, capabilities)
	if supports(reasons, reasonContainerNotSupported) || !supports(reasons, reasonVideoCodecNotSupported) || !supports(reasons, reasonAudioCodecNotSupported) {
		t.Fatalf("cross-container incompatibility reasons = %v", reasons)
	}
}

func TestOmittedDirectProfileBitDepthDefaultsToMain8(t *testing.T) {
	capabilities := Capabilities{
		StreamingProtocols: []string{"http", "hls"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h265", "h264"}, AudioCodecs: []string{"aac"},
		ProcessingModes: []string{processingTranscode},
		MediaProfiles: []MediaProfile{
			{Container: "mp4", VideoCodec: "h265", AudioCodec: "aac", DirectPlay: true},
			{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Transcoding: true},
		},
		transcodeCapabilities: TranscodeCapabilities{EncodeCodecs: []string{"h264"}},
	}
	inspection := MediaInspection{
		Container: "mp4", VideoTracks: []MediaTrack{{Codec: "h265", Height: 1080, BitDepth: 10}}, AudioTracks: []MediaTrack{{Codec: "aac", Channels: 2}},
	}
	mode, decision := playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mp4"}, inspection, capabilities)
	if mode != processingTranscode || decision == nil || decision.Target == nil || decision.Target.VideoBitDepth != 8 {
		t.Fatalf("omitted profile depth accepted Main10 direct play: mode=%q decision=%+v", mode, decision)
	}
	inspection.VideoTracks[0].BitDepth = 8
	if mode, decision = playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mp4"}, inspection, capabilities); mode != "direct" || decision == nil {
		t.Fatalf("omitted profile depth rejected Main 8 direct play: mode=%q decision=%+v", mode, decision)
	}
	inspection.VideoTracks[0].BitDepth = 0
	if mode, decision = playbackMode(Source{Mode: "direct", Protocol: "http", Container: "mp4"}, inspection, capabilities); mode != "direct" || decision == nil {
		t.Fatalf("legacy unknown depth lost backward-compatible direct play: mode=%q decision=%+v", mode, decision)
	}
}
