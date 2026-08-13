package playback

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFFmpegEgressGatewayRecursivelyProxiesPublicHLS(t *testing.T) {
	var origin *httptest.Server
	origin = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		switch request.URL.Path {
		case "/master.m3u8":
			_, _ = io.WriteString(response, "\ufeff#EXTM3U\n#EXT-X-SESSION-DATA:DATA-ID=\"example\",VALUE=\"URI=metadata\"\n#EXT-X-STREAM-INF:BANDWIDTH=1000\nnested/variant\n")
		case "/nested/variant":
			response.Header().Set("Content-Type", "application/octet-stream")
			_, _ = io.WriteString(response, "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nsegment.ts\n#EXT-X-ENDLIST\n")
		case "/nested/segment.ts":
			response.Header().Set("Content-Type", "video/mp2t")
			_, _ = io.WriteString(response, "public segment")
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer origin.Close()

	assetURL := publicTestURL(origin.URL) + "/master.m3u8"
	proxy, err := startFFmpegEgressProxyWithDialAndSource(
		context.Background(), mappedPublicDial(origin.Listener.Addr().String()), storedAsset{URL: assetURL},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	client := guardedGatewayClient()
	master := getGatewayBody(t, client, proxy.InputURL(), http.StatusOK)
	variantURL := onlyPlaylistReference(t, master)
	assertLoopbackGatewayURL(t, proxy, variantURL)
	variant := getGatewayBody(t, client, variantURL, http.StatusOK)
	segmentURL := onlyPlaylistReference(t, variant)
	assertLoopbackGatewayURL(t, proxy, segmentURL)
	if segment := getGatewayBody(t, client, segmentURL, http.StatusOK); segment != "public segment" {
		t.Fatalf("segment body = %q", segment)
	}
}

func TestFFmpegEgressGatewayRefetchesRangedHLSBeforeRewriting(t *testing.T) {
	requestRanges := make(chan string, 2)
	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestRanges <- request.Header.Get("Range")
		response.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		if request.Header.Get("Range") != "" {
			response.Header().Set("Content-Range", "bytes 0-31/64")
			response.WriteHeader(http.StatusPartialContent)
		}
		_, _ = io.WriteString(response, "#EXTM3U\n#EXTINF:1,\nsegment.ts\n")
	}))
	defer origin.Close()
	assetURL := publicTestURL(origin.URL) + "/master.m3u8"
	proxy, err := startFFmpegEgressProxyWithDialAndSource(
		context.Background(), mappedPublicDial(origin.Listener.Addr().String()), storedAsset{URL: assetURL},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	request := mustRequest(t, http.MethodGet, proxy.InputURL())
	request.Header.Set("Range", "bytes=0-31")
	response, err := guardedGatewayClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Range") != "" ||
		len(playlistHTTPReferences(string(body))) != 1 {
		t.Fatalf("rewritten ranged playlist status=%d content-range=%q body=%q", response.StatusCode, response.Header.Get("Content-Range"), body)
	}
	if first, second := <-requestRanges, <-requestRanges; first != "bytes=0-31" || second != "" {
		t.Fatalf("upstream ranges = %q then %q", first, second)
	}
}

func TestFFmpegEgressGatewayRejectsExplicitNestedProtocolsBeforeDial(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var directDials atomic.Int32
	accepted := make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			directDials.Add(1)
			_ = connection.Close()
		}
		close(accepted)
	}()

	tests := []struct {
		name      string
		reference string
		line      func(string) string
	}{
		{name: "tcp", reference: "tcp://" + listener.Addr().String(), line: playlistSegmentLine},
		{name: "tls", reference: "tls://" + listener.Addr().String(), line: playlistSegmentLine},
		{name: "file", reference: "file:///etc/passwd", line: playlistSegmentLine},
		{name: "concat", reference: "concat:http://1.1.1.1/a|http://1.1.1.1/b", line: playlistSegmentLine},
		{name: "data", reference: "data:text/plain,secret", line: playlistSegmentLine},
		{name: "tcp key", reference: "tcp://" + listener.Addr().String(), line: playlistKeyLine},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				_, _ = io.WriteString(response, "#EXTM3U\n"+test.line(test.reference)+"\n")
			}))
			defer origin.Close()
			assetURL := publicTestURL(origin.URL) + "/master.m3u8"
			proxy, startErr := startFFmpegEgressProxyWithDialAndSource(
				context.Background(), mappedPublicDial(origin.Listener.Addr().String()), storedAsset{URL: assetURL},
			)
			if startErr != nil {
				t.Fatal(startErr)
			}
			defer proxy.Close()
			_ = getGatewayBody(t, guardedGatewayClient(), proxy.InputURL(), http.StatusBadGateway)
		})
	}

	_ = listener.Close()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("direct-dial observer did not stop")
	}
	if directDials.Load() != 0 {
		t.Fatalf("nested protocol initiated %d direct dials", directDials.Load())
	}
}

func TestFFmpegEgressGatewayBlocksPrivateChildrenAndRedirects(t *testing.T) {
	var privateRequests atomic.Int32
	private := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		privateRequests.Add(1)
		_, _ = io.WriteString(response, "private")
	}))
	defer private.Close()

	var origin *httptest.Server
	origin = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/master.m3u8":
			response.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(response, "#EXTM3U\n"+private.URL+"/private.m3u8\n")
		case "/redirect":
			http.Redirect(response, request, private.URL+"/metadata", http.StatusPermanentRedirect)
		case "/manifest.mpd":
			response.Header().Set("Content-Type", "application/dash+xml")
			_, _ = io.WriteString(response, `<MPD><Period><BaseURL>`+private.URL+`/</BaseURL></Period></MPD>`)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer origin.Close()
	publicBase := publicTestURL(origin.URL)
	proxy, err := startFFmpegEgressProxyWithDialAndSource(
		context.Background(), mappedPublicDial(origin.Listener.Addr().String()), storedAsset{URL: publicBase + "/master.m3u8"},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	master := getGatewayBody(t, guardedGatewayClient(), proxy.InputURL(), http.StatusOK)
	privateChild := onlyPlaylistReference(t, master)
	assertLoopbackGatewayURL(t, proxy, privateChild)
	_ = getGatewayBody(t, guardedGatewayClient(), privateChild, http.StatusBadGateway)
	redirectURL, ok := proxy.registerTarget(publicBase + "/redirect")
	if !ok {
		t.Fatal("public redirect target was rejected")
	}
	_ = getGatewayBody(t, guardedGatewayClient(), redirectURL, http.StatusBadGateway)
	dashURL, ok := proxy.registerTarget(publicBase + "/manifest.mpd")
	if !ok {
		t.Fatal("public DASH target was rejected before gateway registration")
	}
	_ = getGatewayBody(t, guardedGatewayClient(), dashURL, http.StatusBadGateway)
	if privateRequests.Load() != 0 {
		t.Fatalf("private origin received %d requests", privateRequests.Load())
	}
}

func TestFFmpegEgressGatewayFollowsGuardedRedirectsWithOriginScopedCredentials(t *testing.T) {
	sourceHeaders := map[string]string{
		"Authorization":  "Bearer source-secret",
		"Cookie":         "source_session=secret",
		"X-Provider-Key": "provider-secret",
	}
	crossHeaders := make(chan http.Header, 1)
	cross := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		crossHeaders <- request.Header.Clone()
		_, _ = io.WriteString(response, "redirected media")
	}))
	defer cross.Close()
	crossURL := publicTestURLWithHost(cross.URL, "2.2.2.2")

	sourceHeadersSeen := make(chan http.Header, 2)
	var source *httptest.Server
	source = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		sourceHeadersSeen <- request.Header.Clone()
		if request.URL.Path == "/redirect" {
			http.Redirect(response, request, publicTestURL(source.URL)+"/same-origin", http.StatusFound)
			return
		}
		http.Redirect(response, request, crossURL+"/segment.ts", http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	sourceURL := publicTestURL(source.URL) + "/redirect"

	proxy, err := startFFmpegEgressProxyWithDialAndSource(context.Background(), mappedOriginDial(map[string]string{
		"1.1.1.1": source.Listener.Addr().String(),
		"2.2.2.2": cross.Listener.Addr().String(),
	}), storedAsset{URL: sourceURL, Headers: sourceHeaders})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	if body := getGatewayBody(t, guardedGatewayClient(), proxy.InputURL(), http.StatusOK); body != "redirected media" {
		t.Fatalf("redirected body = %q", body)
	}
	for requestIndex := range 2 {
		capturedSource := <-sourceHeadersSeen
		for name, expected := range sourceHeaders {
			if value := capturedSource.Get(name); value != expected {
				t.Fatalf("same-origin request %d %s = %q, want %q", requestIndex, name, value, expected)
			}
		}
	}
	capturedCross := <-crossHeaders
	for name := range sourceHeaders {
		if value := capturedCross.Get(name); value != "" {
			t.Fatalf("cross-origin redirect retained %s: %q", name, value)
		}
	}
}

func TestFFmpegEgressGatewayRejectsHTTPSDowngradeBeforeTargetRequest(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		targetRequests.Add(1)
		_, _ = io.WriteString(response, "downgraded media")
	}))
	defer target.Close()
	targetURL := publicTestURLWithHost(target.URL, "2.2.2.2")

	source := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, targetURL+"/segment.ts", http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	sourceURL := publicTestURLWithHost(source.URL, "1.1.1.1") + "/master.m3u8"
	proxy, err := startFFmpegEgressProxyWithDialAndSource(context.Background(), mappedOriginDial(map[string]string{
		"1.1.1.1": source.Listener.Addr().String(),
		"2.2.2.2": target.Listener.Addr().String(),
	}), storedAsset{URL: sourceURL})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	proxy.transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // test origin certificate
	_ = getGatewayBody(t, guardedGatewayClient(), proxy.InputURL(), http.StatusBadGateway)
	if requests := targetRequests.Load(); requests != 0 {
		t.Fatalf("downgrade target received %d requests", requests)
	}
}

func TestFFmpegEgressGatewaySupportsPublicHTTPAndHTTPS(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%t", secure), func(t *testing.T) {
			handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/master.m3u8":
					response.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
					_, _ = io.WriteString(response, "#EXTM3U\n#EXTINF:1,\nsegment.ts\n")
				case "/segment.ts":
					_, _ = io.WriteString(response, "media")
				default:
					response.WriteHeader(http.StatusNotFound)
				}
			})
			var origin *httptest.Server
			if secure {
				origin = httptest.NewTLSServer(handler)
			} else {
				origin = httptest.NewServer(handler)
			}
			defer origin.Close()
			assetURL := publicTestURL(origin.URL) + "/master.m3u8"
			proxy, err := startFFmpegEgressProxyWithDialAndSource(
				context.Background(), mappedPublicDial(origin.Listener.Addr().String()), storedAsset{URL: assetURL},
			)
			if err != nil {
				t.Fatal(err)
			}
			defer proxy.Close()
			if secure {
				proxy.transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // test origin certificate
			}
			playlist := getGatewayBody(t, guardedGatewayClient(), proxy.InputURL(), http.StatusOK)
			childURL := onlyPlaylistReference(t, playlist)
			assertLoopbackGatewayURL(t, proxy, childURL)
			if body := getGatewayBody(t, guardedGatewayClient(), childURL, http.StatusOK); body != "media" {
				t.Fatalf("public HLS media body = %q", body)
			}
		})
	}
}

func TestDefaultFFmpegEgressProxyRejectsNonPublicProviderNetworks(t *testing.T) {
	proxy, err := startFFmpegEgressProxy(context.Background(), storedAsset{})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	for _, address := range []string{
		"127.0.0.1:80", "10.0.0.10:80", "172.16.0.10:80", "192.168.1.10:80",
		"169.254.169.254:80", "100.64.0.10:80", "[fc00::10]:80",
	} {
		connection, dialErr := proxy.transport.DialContext(context.Background(), "tcp", address)
		if connection != nil {
			_ = connection.Close()
			t.Fatalf("unexpected non-public connection to %s", address)
		}
		if dialErr == nil || !strings.Contains(dialErr.Error(), "outbound destination is not permitted") {
			t.Fatalf("non-public destination %s error = %v", address, dialErr)
		}
	}
}

func TestFFmpegEgressGatewayRejectsUnsignedAndTunnelRequestsWithoutDial(t *testing.T) {
	var dials atomic.Int32
	proxy, err := startFFmpegEgressProxyWithDial(context.Background(), func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("unexpected dial")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	for _, request := range []*http.Request{
		mustRequest(t, http.MethodGet, proxy.URL()+"/media/unsigned"),
		mustRequest(t, http.MethodConnect, proxy.URL()+"/media/unsigned"),
	} {
		response, requestErr := guardedGatewayClient().Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		_ = response.Body.Close()
		if response.StatusCode < 400 {
			t.Fatalf("unsigned/tunnel request status = %d", response.StatusCode)
		}
	}
	if dials.Load() != 0 {
		t.Fatalf("rejected gateway requests made %d network dials", dials.Load())
	}
}

func TestFFmpegEgressGatewayBoundsRegisteredTargetsAcrossPlaylists(t *testing.T) {
	proxy, err := startFFmpegEgressProxyWithDial(context.Background(), func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("unexpected dial")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	dummy, err := url.Parse("https://public.example/media")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < ffmpegEgressMaxTargets; index++ {
		proxy.targets[fmt.Sprintf("occupied-%d", index)] = dummy
	}
	base, err := url.Parse("https://public.example/master.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := proxy.rewritePlaylist([]byte("#EXTM3U\nsegment.ts\n"), base); err == nil ||
		!strings.Contains(err.Error(), "target limit") {
		t.Fatalf("rewrite at target limit error = %v", err)
	}
	if len(proxy.targets) != ffmpegEgressMaxTargets {
		t.Fatalf("registered targets = %d, want %d", len(proxy.targets), ffmpegEgressMaxTargets)
	}
}

func TestFFmpegEgressGatewayCancelsSilentUpstreamBody(t *testing.T) {
	requestCanceled := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "video/mp4")
		response.WriteHeader(http.StatusOK)
		response.(http.Flusher).Flush()
		<-request.Context().Done()
		close(requestCanceled)
	}))
	defer origin.Close()

	proxy, err := startFFmpegEgressProxyWithReadIdleTimeout(
		context.Background(), mappedPublicDial(origin.Listener.Addr().String()),
		storedAsset{URL: publicTestURL(origin.URL) + "/video.mp4"}, 25*time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	client := &http.Client{Timeout: 500 * time.Millisecond}
	startedAt := time.Now()
	response, streamErr := client.Get(proxy.InputURL())
	if response != nil {
		_, streamErr = io.ReadAll(response.Body)
		_ = response.Body.Close()
	}
	if streamErr == nil {
		t.Fatal("silent upstream body completed without an error")
	}
	if elapsed := time.Since(startedAt); elapsed >= 250*time.Millisecond {
		t.Fatalf("silent upstream body canceled after %s", elapsed)
	}
	select {
	case <-requestCanceled:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("silent upstream request context was not canceled")
	}
}

func TestFFmpegAndFFprobeReceiveOnlyLifecycleBoundGatewayURL(t *testing.T) {
	t.Setenv("RIVUNE_FFMPEG_HELPER_MODE", "probe")
	tests := []struct {
		name string
		run  func(*FFmpegProcessor, storedAsset) error
	}{
		{name: "probe", run: func(processor *FFmpegProcessor, asset storedAsset) error {
			_, err := processor.Probe(context.Background(), asset)
			return err
		}},
		{name: "transcode", run: func(processor *FFmpegProcessor, asset storedAsset) error {
			asset.Kind = processingTranscode
			return processor.ProcessHLS(context.Background(), asset, t.TempDir())
		}},
		{name: "subtitle", run: func(processor *FFmpegProcessor, asset storedAsset) error {
			return processor.ConvertSubtitle(context.Background(), asset, io.Discard)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			asset := storedAsset{URL: "http://1.1.1.1/provider-secret/video.mp4", Headers: map[string]string{"Authorization": "Bearer secret"}}
			processor := testFFmpegProcessor()

			processor.egressProxy = func(ctx context.Context, source storedAsset) (*ffmpegEgressProxy, error) {
				return startFFmpegEgressProxyWithDialAndSource(ctx, mappedPublicDial("127.0.0.1:1"), source)
			}
			var captured []string
			processor.commandContext = func(ctx context.Context, path string, arguments ...string) *exec.Cmd {
				captured = append([]string(nil), arguments...)
				return ffmpegHelperCommand(ctx, path, arguments...)
			}
			if err := test.run(processor, asset); err != nil {
				t.Fatalf("media operation: %v", err)
			}
			var guardedURL string
			for _, argument := range captured {
				if strings.Contains(argument, asset.URL) || strings.Contains(argument, "Bearer secret") {
					t.Fatalf("raw provider URL or credential reached subprocess: %q", argument)
				}
				if strings.HasPrefix(argument, "http://127.0.0.1:") && strings.Contains(argument, "/media/") {
					guardedURL = argument
				}
			}
			if guardedURL == "" || argumentIndex(captured, "-http_proxy") >= 0 {
				t.Fatalf("guarded input arguments = %v", captured)
			}
			_, whitelist := argumentValue(captured, "-protocol_whitelist")
			if whitelist != "crypto,http,tcp" || strings.Contains(whitelist, "tls") || strings.Contains(whitelist, "https") {
				t.Fatalf("network protocol whitelist = %q", whitelist)
			}
			parsed, parseErr := url.Parse(guardedURL)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			connection, dialErr := net.DialTimeout("tcp", parsed.Host, 100*time.Millisecond)
			if connection != nil {
				_ = connection.Close()
				t.Fatal("per-command gateway remained available after subprocess exit")
			}
			if dialErr == nil {
				t.Fatal("expected lifecycle-bound gateway listener to be closed")
			}
		})
	}
}

func TestRealFFprobeStopsAfterSilentUpstreamBody(t *testing.T) {
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("FFprobe executable is not installed")
	}
	requestCanceled := make(chan struct{}, 1)
	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "video/mp4")
		response.Header().Set("Content-Length", "1024")
		response.WriteHeader(http.StatusOK)
		response.(http.Flusher).Flush()
		<-request.Context().Done()
		select {
		case requestCanceled <- struct{}{}:
		default:
		}
	}))
	defer origin.Close()
	processor := &FFmpegProcessor{
		ffprobePath: ffprobePath,
		probeSlots:  make(chan struct{}, 1),
		egressProxy: func(ctx context.Context, asset storedAsset) (*ffmpegEgressProxy, error) {
			return startFFmpegEgressProxyWithReadIdleTimeout(
				ctx, mappedPublicDial(origin.Listener.Addr().String()), asset, 25*time.Millisecond,
			)
		},
	}
	startedAt := time.Now()
	_, probeErr := processor.Probe(context.Background(), storedAsset{URL: publicTestURL(origin.URL) + "/video.mp4", Container: "mp4"})
	if probeErr == nil {
		t.Fatal("FFprobe accepted a silent upstream body")
	}
	if elapsed := time.Since(startedAt); elapsed >= time.Second {
		t.Fatalf("FFprobe stopped after %s", elapsed)
	}
	select {
	case <-requestCanceled:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("FFprobe silent upstream request was not canceled")
	}
}
func TestRealFFprobeCannotDialExplicitNestedTCP(t *testing.T) {
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("FFprobe executable is not installed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var directDials atomic.Int32
	observerDone := make(chan struct{})
	go func() {
		defer close(observerDone)
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			directDials.Add(1)
			_ = connection.Close()
		}
	}()
	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = io.WriteString(response, "#EXTM3U\ntcp://"+listener.Addr().String()+"\n")
	}))
	defer origin.Close()
	processor := &FFmpegProcessor{
		ffprobePath: ffprobePath,
		probeSlots:  make(chan struct{}, 1),
		egressProxy: func(ctx context.Context, asset storedAsset) (*ffmpegEgressProxy, error) {
			return startFFmpegEgressProxyWithDialAndSource(ctx, mappedPublicDial(origin.Listener.Addr().String()), asset)
		},
	}
	if _, probeErr := processor.Probe(context.Background(), storedAsset{URL: publicTestURL(origin.URL) + "/master.m3u8"}); probeErr == nil {
		t.Fatal("FFprobe accepted an HLS manifest with an explicit TCP child")
	}
	_ = listener.Close()
	select {
	case <-observerDone:
	case <-time.After(time.Second):
		t.Fatal("direct-dial observer did not stop")
	}
	if directDials.Load() != 0 {
		t.Fatalf("FFprobe initiated %d direct TCP child dials", directDials.Load())
	}
}

func TestFFmpegSubprocessEnvironmentCannotBypassGateway(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://untrusted-proxy.example")
	t.Setenv("https_proxy", "http://untrusted-proxy.example")
	t.Setenv("ALL_PROXY", "socks5://untrusted-proxy.example")
	t.Setenv("no_proxy", "*")
	command := testFFmpegProcessor().newCommand(context.Background(), os.Args[0])
	for _, entry := range command.Env {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToLower(name) {
		case "http_proxy", "https_proxy", "all_proxy", "no_proxy":
			t.Fatalf("proxy bypass environment reached FFmpeg: %q", entry)
		}
	}
}

func guardedGatewayClient() *http.Client {
	return &http.Client{Timeout: 3 * time.Second}
}

func getGatewayBody(t *testing.T, client *http.Client, rawURL string, expectedStatus int) string {
	t.Helper()
	response, err := client.Get(rawURL)
	if err != nil {
		t.Fatalf("GET guarded media: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != expectedStatus {
		t.Fatalf("guarded media status = %d, want %d: %s", response.StatusCode, expectedStatus, body)
	}
	return string(body)
}

func onlyPlaylistReference(t *testing.T, playlist string) string {
	t.Helper()
	references := playlistHTTPReferences(playlist)
	if len(references) != 1 {
		t.Fatalf("playlist references = %v in %q", references, playlist)
	}
	return references[0]
}

func assertLoopbackGatewayURL(t *testing.T, proxy *ffmpegEgressProxy, rawURL string) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "http" || parsed.Host != proxy.listener.Addr().String() || !strings.HasPrefix(parsed.Path, "/media/") {
		t.Fatalf("child URL is not guarded loopback: %q", rawURL)
	}
}

func playlistSegmentLine(reference string) string {
	return "#EXTINF:1,\n" + reference
}

func playlistKeyLine(reference string) string {
	return "#EXT-X-KEY:METHOD=AES-128,URI=\"" + reference + "\""
}

func mappedPublicDial(destination string) egressDialContext {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if host != "1.1.1.1" {
			return nil, errors.New("outbound destination is not permitted")
		}
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, destination)
	}
}

func mappedOriginDial(destinations map[string]string) egressDialContext {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		destination, ok := destinations[host]
		if !ok {
			return nil, errors.New("outbound destination is not permitted")
		}
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, destination)
	}
}

func publicTestURL(originURL string) string {
	parsed, _ := url.Parse(originURL)
	return parsed.Scheme + "://1.1.1.1:" + parsed.Port()
}

func publicTestURLWithHost(originURL, host string) string {
	parsed, _ := url.Parse(originURL)
	return parsed.Scheme + "://" + net.JoinHostPort(host, parsed.Port())
}

func playlistHTTPReferences(playlist string) []string {
	fields := strings.FieldsFunc(playlist, func(character rune) bool {
		return character == '\n' || character == '\r' || character == '"'
	})
	references := make([]string, 0, len(fields))
	for _, field := range fields {
		if strings.HasPrefix(field, "http://") || strings.HasPrefix(field, "https://") {
			references = append(references, field)
		}
	}
	return references
}

func argumentIndex(arguments []string, target string) int {
	for index, argument := range arguments {
		if argument == target {
			return index
		}
	}
	return -1
}

func argumentValue(arguments []string, target string) (int, string) {
	index := argumentIndex(arguments, target)
	if index < 0 || index+1 >= len(arguments) {
		return -1, ""
	}
	return index, arguments[index+1]
}

func mustRequest(t *testing.T, method, rawURL string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return request
}
