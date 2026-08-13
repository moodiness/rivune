package playback

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/moodiness/rivune/server/internal/netguard"
	"github.com/moodiness/rivune/server/internal/requestwork"
)

const (
	ffmpegEgressHeaderTimeout   = 5 * time.Second
	ffmpegEgressDialTimeout     = 10 * time.Second
	ffmpegEgressMaxConcurrent   = 32
	ffmpegEgressReadIdleTimeout = 10 * time.Second
	ffmpegEgressMaxRedirects    = 10
	ffmpegEgressMaxTargets      = 20_000
)

type egressDialContext func(context.Context, string, string) (net.Conn, error)

type mediaOrigin struct {
	scheme string
	host   string
	port   string
}

type boundedProxyListener struct {
	net.Listener
	slots     chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

type boundedProxyConnection struct {
	net.Conn
	releaseOnce sync.Once
	release     func()
}

func newBoundedProxyListener(listener net.Listener, maximum int) net.Listener {
	return &boundedProxyListener{
		Listener: listener,
		slots:    make(chan struct{}, maximum),
		done:     make(chan struct{}),
	}
}

func (listener *boundedProxyListener) Accept() (net.Conn, error) {
	select {
	case listener.slots <- struct{}{}:
	case <-listener.done:
		return nil, net.ErrClosed
	}
	connection, err := listener.Listener.Accept()
	if err != nil {
		<-listener.slots
		return nil, err
	}
	return &boundedProxyConnection{
		Conn: connection,
		release: func() {
			<-listener.slots
		},
	}, nil
}

func (listener *boundedProxyListener) Close() error {
	var closeErr error
	listener.closeOnce.Do(func() {
		close(listener.done)
		closeErr = listener.Listener.Close()
	})
	return closeErr
}

func (connection *boundedProxyConnection) Close() error {
	closeErr := connection.Conn.Close()
	connection.releaseOnce.Do(connection.release)
	return closeErr
}

type ffmpegEgressProxy struct {
	listener        net.Listener
	server          *http.Server
	transport       *http.Transport
	readIdleTimeout time.Duration
	cancel          context.CancelFunc
	done            chan struct{}
	slots           chan struct{}

	sourceOrigin  mediaOrigin
	sourceHeaders http.Header
	signingKey    [32]byte
	inputURL      string
	targetsMu     sync.RWMutex
	targets       map[string]*url.URL

	connectionsMu sync.Mutex
	connections   map[net.Conn]struct{}
	closed        bool
	closeOnce     sync.Once
}

func startFFmpegEgressProxy(ctx context.Context, asset storedAsset) (*ffmpegEgressProxy, error) {
	return startFFmpegEgressProxyWithDialAndSource(ctx, netguard.DialContextPublic, asset)
}

func startFFmpegEgressProxyWithDial(ctx context.Context, dial egressDialContext) (*ffmpegEgressProxy, error) {
	return startFFmpegEgressProxyWithDialAndSource(ctx, dial, storedAsset{})
}

func startFFmpegEgressProxyWithDialAndSource(ctx context.Context, dial egressDialContext, asset storedAsset) (*ffmpegEgressProxy, error) {
	return startFFmpegEgressProxyWithReadIdleTimeout(ctx, dial, asset, ffmpegEgressReadIdleTimeout)
}

func startFFmpegEgressProxyWithReadIdleTimeout(ctx context.Context, dial egressDialContext, asset storedAsset, readIdleTimeout time.Duration) (*ffmpegEgressProxy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dial == nil {
		return nil, errors.New("guarded dialer is required")
	}
	var signingKey [32]byte
	if _, err := rand.Read(signingKey[:]); err != nil {
		return nil, fmt.Errorf("create guarded media URL key: %w", err)
	}
	rawListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	listener := newBoundedProxyListener(rawListener, ffmpegEgressMaxConcurrent)
	proxyContext, cancel := context.WithCancel(ctx)
	proxy := &ffmpegEgressProxy{
		listener:        listener,
		cancel:          cancel,
		done:            make(chan struct{}),
		slots:           make(chan struct{}, ffmpegEgressMaxConcurrent),
		readIdleTimeout: readIdleTimeout,
		signingKey:      signingKey,
		targets:         make(map[string]*url.URL),
		connections:     make(map[net.Conn]struct{}),
	}
	boundedDial := func(ctx context.Context, network, address string) (net.Conn, error) {
		dialContext, dialCancel := context.WithTimeout(ctx, ffmpegEgressDialTimeout)
		defer dialCancel()
		return dial(dialContext, network, address)
	}
	proxy.transport = &http.Transport{
		Proxy:                  nil,
		DialContext:            boundedDial,
		ForceAttemptHTTP2:      false,
		MaxConnsPerHost:        ffmpegEgressMaxConcurrent,
		MaxIdleConns:           ffmpegEgressMaxConcurrent,
		MaxIdleConnsPerHost:    ffmpegEgressMaxConcurrent,
		IdleConnTimeout:        30 * time.Second,
		TLSHandshakeTimeout:    ffmpegEgressDialTimeout,
		ResponseHeaderTimeout:  ffmpegEgressDialTimeout,
		MaxResponseHeaderBytes: 64 << 10,
	}
	proxy.server = &http.Server{
		Handler:           proxy,
		ReadHeaderTimeout: ffmpegEgressHeaderTimeout,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          log.New(io.Discard, "", 0),
		ConnState:         proxy.connectionState,
		BaseContext: func(net.Listener) context.Context {
			return proxyContext
		},
	}
	if !proxy.setSourceCredentials(asset) {
		cancel()
		_ = listener.Close()
		return nil, errors.New("invalid guarded media source headers")
	}
	if asset.URL != "" {
		inputURL, valid := proxy.registerTarget(asset.URL)
		if !valid {
			cancel()
			_ = listener.Close()
			return nil, errors.New("invalid guarded media source URL")
		}
		proxy.inputURL = inputURL
	}
	go func() {
		defer close(proxy.done)
		_ = proxy.server.Serve(listener)
	}()
	go func() {
		<-proxyContext.Done()
		_ = proxy.Close()
	}()
	return proxy, nil
}

func (proxy *ffmpegEgressProxy) URL() string {
	return "http://" + proxy.listener.Addr().String()
}

func (proxy *ffmpegEgressProxy) InputURL() string {
	return proxy.inputURL
}

func (proxy *ffmpegEgressProxy) registerTarget(rawURL string) (string, bool) {
	if !validMediaURL(rawURL) {
		return "", false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, proxy.signingKey[:])
	_, _ = io.WriteString(mac, parsed.String())
	token := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	proxy.targetsMu.Lock()
	if _, exists := proxy.targets[token]; !exists && len(proxy.targets) >= ffmpegEgressMaxTargets {
		proxy.targetsMu.Unlock()
		return "", false
	}
	proxy.targets[token] = parsed
	proxy.targetsMu.Unlock()
	return proxy.URL() + "/media/" + token, true
}

func (proxy *ffmpegEgressProxy) target(token string) (*url.URL, bool) {
	proxy.targetsMu.RLock()
	target, ok := proxy.targets[token]
	proxy.targetsMu.RUnlock()
	if !ok {
		return nil, false
	}
	clone := *target
	return &clone, true
}

func (proxy *ffmpegEgressProxy) setSourceCredentials(asset storedAsset) bool {
	headers, validHeaders := canonicalStoredRequestHeaders(asset.Headers)
	if !validHeaders {
		return false
	}
	origin, validOrigin := canonicalMediaOrigin(asset.URL)
	if !validOrigin {
		return true
	}
	proxy.sourceOrigin = origin
	proxy.sourceHeaders = headers
	return true
}

func (proxy *ffmpegEgressProxy) Close() error {
	var closeErr error
	proxy.closeOnce.Do(func() {
		proxy.cancel()
		closeErr = proxy.server.Close()
		proxy.transport.CloseIdleConnections()
		proxy.closeConnections()
		<-proxy.done
	})
	return closeErr
}

func (proxy *ffmpegEgressProxy) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	select {
	case proxy.slots <- struct{}{}:
		defer func() { <-proxy.slots }()
	default:
		http.Error(response, "egress capacity reached", http.StatusServiceUnavailable)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token, validPath := strings.CutPrefix(request.URL.Path, "/media/")
	if !validPath || token == "" || strings.Contains(token, "/") || request.URL.RawQuery != "" {
		http.Error(response, "invalid guarded media request", http.StatusBadRequest)
		return
	}
	target, ok := proxy.target(token)
	if !ok {
		http.Error(response, "unknown guarded media target", http.StatusForbidden)
		return
	}
	proxy.serveTarget(response, request, target)
}

func (proxy *ffmpegEgressProxy) serveTarget(response http.ResponseWriter, incoming *http.Request, target *url.URL) {
	upstream, err := proxy.fetchTarget(incoming.Context(), incoming.Method, incoming.Header, target)
	if err != nil {
		http.Error(response, "upstream unavailable", http.StatusBadGateway)
		return
	}
	reader := bufio.NewReaderSize(upstream.Body, 1024)
	playlist := incoming.Method == http.MethodGet && upstream.StatusCode >= 200 && upstream.StatusCode < 300 &&
		(isHLSPlaylist(upstream, upstream.Request.URL.String()) || readerStartsHLS(reader))
	if playlist && (upstream.StatusCode != http.StatusOK || incoming.Header.Get("Range") != "") {
		_ = upstream.Body.Close()
		fullHeaders := incoming.Header.Clone()
		fullHeaders.Del("Range")
		fullHeaders.Del("If-Range")
		upstream, err = proxy.fetchTarget(incoming.Context(), incoming.Method, fullHeaders, target)
		if err != nil {
			http.Error(response, "upstream unavailable", http.StatusBadGateway)
			return
		}
		reader = bufio.NewReaderSize(upstream.Body, 1024)
		playlist = upstream.StatusCode == http.StatusOK &&
			(isHLSPlaylist(upstream, upstream.Request.URL.String()) || readerStartsHLS(reader))
		if !playlist {
			_ = upstream.Body.Close()
			http.Error(response, "invalid ranged HLS playlist", http.StatusBadGateway)
			return
		}
	}
	defer upstream.Body.Close()

	if playlist {
		body, readErr := io.ReadAll(io.LimitReader(reader, maximumPlaylistBytes+1))
		if readErr != nil || len(body) > maximumPlaylistBytes {
			http.Error(response, "invalid HLS playlist", http.StatusBadGateway)
			return
		}
		rewritten, rewriteErr := proxy.rewritePlaylist(body, upstream.Request.URL)
		if rewriteErr != nil {
			http.Error(response, "invalid HLS playlist", http.StatusBadGateway)
			return
		}
		copyAssetHeaders(response.Header(), upstream.Header, false)
		response.Header().Del("Content-Range")
		response.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		response.Header().Set("Content-Length", fmt.Sprintf("%d", len(rewritten)))
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(rewritten)
		return
	}
	if incoming.Method == http.MethodGet && upstream.StatusCode >= 200 && upstream.StatusCode < 300 &&
		unsupportedNetworkManifest(upstream, reader) {
		http.Error(response, "unsupported network manifest", http.StatusBadGateway)
		return
	}

	copyAssetHeaders(response.Header(), upstream.Header, true)
	response.WriteHeader(upstream.StatusCode)
	if incoming.Method == http.MethodGet {
		if _, err := io.Copy(response, reader); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
}

func readerStartsHLS(reader *bufio.Reader) bool {
	prefix, err := reader.Peek(16)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return false
	}
	prefixText := strings.TrimSpace(strings.TrimPrefix(string(prefix), "\ufeff"))
	return strings.HasPrefix(prefixText, "#EXTM3U")
}

func unsupportedNetworkManifest(response *http.Response, reader *bufio.Reader) bool {
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "dash+xml") || strings.Contains(contentType, "smoothstreaming") ||
		strings.Contains(contentType, "sstr+xml") || strings.Contains(contentType, "f4m") {
		return true
	}
	switch strings.ToLower(pathExtension(response.Request.URL.Path)) {
	case ".mpd", ".f4m", ".ism", ".m3u", ".pls", ".asx", ".xspf", ".sdp", ".cue":
		return true
	}
	if contentType != "" && !strings.Contains(contentType, "xml") &&
		!strings.Contains(contentType, "octet-stream") && !strings.HasPrefix(contentType, "text/") {
		return false
	}
	prefix, err := reader.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return false
	}
	prefixText := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(string(prefix), "\ufeff")))
	return strings.HasPrefix(prefixText, "<") || strings.HasPrefix(prefixText, "[playlist]") ||
		strings.HasPrefix(prefixText, "ffconcat version")
}

func (proxy *ffmpegEgressProxy) rewritePlaylist(body []byte, base *url.URL) ([]byte, error) {
	body = bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})
	if err := validateGuardedPlaylistReferences(body, base); err != nil {
		return nil, err
	}
	var registrationFailed bool
	rewritten, err := rewritePlaylist(body, base, func(resolved string) string {
		guarded, ok := proxy.registerTarget(resolved)
		if !ok {
			registrationFailed = true
		}
		return guarded
	})
	if err != nil {
		return nil, err
	}
	if registrationFailed {
		return nil, errors.New("guarded media target limit exceeded")
	}
	return rewritten, nil
}

func validateGuardedPlaylistReferences(body []byte, base *url.URL) error {
	if err := validatePlaylistCardinality(body); err != nil {
		return err
	}
	scanner := playlistScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "#") {
			if _, ok := resolvePlaylistReference(base, trimmed); !ok {
				return errors.New("HLS child URI must use HTTP or HTTPS")
			}
			continue
		}
		remaining := line
		for {
			match := playlistURIAttribute.FindStringSubmatchIndex(remaining)
			if match == nil {
				break
			}
			if _, ok := resolvePlaylistReference(base, remaining[match[2]:match[3]]); !ok {
				return errors.New("HLS child URI must use HTTP or HTTPS")
			}
			remaining = remaining[match[3]:]
		}
	}
	return scanner.Err()
}

func (proxy *ffmpegEgressProxy) fetchTarget(ctx context.Context, method string, incoming http.Header, target *url.URL) (*http.Response, error) {
	current := target
	for redirects := 0; ; redirects++ {
		requestContext, cancel := context.WithCancel(ctx)
		request, err := http.NewRequestWithContext(requestContext, method, current.String(), nil)
		if err != nil {
			cancel()
			return nil, err
		}
		for _, name := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
			if value := incoming.Get(name); value != "" {
				request.Header.Set(name, value)
			}
		}
		request.Header.Set("User-Agent", "Rivune-Playback/1")
		proxy.applySourceCredentials(request)
		started := requestwork.Now()
		requestwork.BeginOutbound(ctx, started)
		upstream, err := proxy.transport.RoundTrip(request)
		if err != nil {
			cancel()
			requestwork.EndOutbound(ctx, requestwork.Now(), 0)
			return nil, err
		}
		if upstream.Body == nil {
			cancel()
			requestwork.EndOutbound(ctx, requestwork.Now(), 0)
		} else {
			observed := requestwork.ObserveBody(ctx, upstream.Body)
			upstream.Body = newReadIdleBody(ctx, observed, cancel, func() {}, proxy.readIdleTimeout)
		}
		if !isHTTPRedirect(upstream.StatusCode) {
			upstream.Request = request
			return upstream, nil
		}
		if redirects >= ffmpegEgressMaxRedirects {
			_ = upstream.Body.Close()
			return nil, errors.New("too many media redirects")
		}
		location := upstream.Header.Get("Location")
		redirected, err := current.Parse(location)
		_ = upstream.Body.Close()
		if err != nil || !validMediaURL(redirected.String()) {
			return nil, errors.New("invalid media redirect")
		}
		if strings.EqualFold(current.Scheme, "https") && !strings.EqualFold(redirected.Scheme, "https") {
			return nil, errors.New("media HTTPS redirect downgrade refused")
		}
		current = redirected
	}
}

func isHTTPRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func (proxy *ffmpegEgressProxy) applySourceCredentials(request *http.Request) {
	for name := range proxy.sourceHeaders {
		request.Header.Del(name)
	}
	origin, validOrigin := canonicalURLOrigin(request.URL)
	if !validOrigin || origin != proxy.sourceOrigin {
		request.Header.Del("Authorization")
		request.Header.Del("Cookie")
		return
	}
	for name, values := range proxy.sourceHeaders {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
}

func canonicalMediaOrigin(rawURL string) (mediaOrigin, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return mediaOrigin{}, false
	}
	return canonicalURLOrigin(parsed)
}

func canonicalURLOrigin(parsed *url.URL) (mediaOrigin, bool) {
	if parsed == nil {
		return mediaOrigin{}, false
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	port := parsed.Port()
	if port == "" {
		switch scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return mediaOrigin{}, false
		}
	}
	if host == "" {
		return mediaOrigin{}, false
	}
	return mediaOrigin{scheme: scheme, host: host, port: port}, true
}

func (proxy *ffmpegEgressProxy) connectionState(connection net.Conn, state http.ConnState) {
	switch state {
	case http.StateNew:
		proxy.trackConnection(connection)
	case http.StateClosed:
		proxy.untrackConnection(connection)
	}
}

func (proxy *ffmpegEgressProxy) trackConnection(connection net.Conn) {
	proxy.connectionsMu.Lock()
	if proxy.closed {
		proxy.connectionsMu.Unlock()
		_ = connection.Close()
		return
	}
	proxy.connections[connection] = struct{}{}
	proxy.connectionsMu.Unlock()
}

func (proxy *ffmpegEgressProxy) untrackConnection(connection net.Conn) {
	proxy.connectionsMu.Lock()
	delete(proxy.connections, connection)
	proxy.connectionsMu.Unlock()
}

func (proxy *ffmpegEgressProxy) closeConnections() {
	proxy.connectionsMu.Lock()
	proxy.closed = true
	connections := make([]net.Conn, 0, len(proxy.connections))
	for connection := range proxy.connections {
		connections = append(connections, connection)
		delete(proxy.connections, connection)
	}
	proxy.connectionsMu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
}
