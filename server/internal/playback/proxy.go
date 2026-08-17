package playback

import (
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/rivune/server/internal/netguard"
	"github.com/moodiness/rivune/server/internal/requestwork"
)

const (
	maximumPlaylistBytes           = 8 * 1024 * 1024
	maximumPlaylistLines           = 2*maximumPlaylistReferences + 16
	maximumPlaylistReferences      = 10_000
	maximumRewrittenPlaylistBytes  = 16 * 1024 * 1024
	maximumDirectStreamsGlobal     = 64
	maximumDirectStreamsPerOwner   = 4
	maximumDirectStreamsPerHost    = 16
	directStreamReadIdleTimeout    = 45 * time.Second
	directStreamStartupReadTimeout = 15 * time.Second
	maximumTargetCapabilityLength  = 12 * 1024
	directSubtitleAssetKind        = "subtitle"
)

type directStreamAdmission struct {
	mu      sync.Mutex
	active  int
	byOwner map[string]int
}

func (admission *directStreamAdmission) acquire(owner string, globalLimit, ownerLimit int) (func(), error) {
	admission.mu.Lock()
	defer admission.mu.Unlock()
	if admission.active >= globalLimit || admission.byOwner[owner] >= ownerLimit {
		return nil, ErrMediaCapacityReached
	}
	if admission.byOwner == nil {
		admission.byOwner = make(map[string]int)
	}
	admission.active++
	admission.byOwner[owner]++
	var once sync.Once
	return func() {
		once.Do(func() {
			admission.mu.Lock()
			defer admission.mu.Unlock()
			admission.active--
			admission.byOwner[owner]--
			if admission.byOwner[owner] == 0 {
				delete(admission.byOwner, owner)
			}
		})
	}, nil
}

type readIdleBody struct {
	body               io.ReadCloser
	cancel             context.CancelFunc
	release            func()
	startupReadTimeout time.Duration
	idleTimeout        time.Duration
	parent             context.Context
	readStarted        chan struct{}
	readProgress       chan struct{}
	done               chan struct{}
	finishOnce         sync.Once
	closeOnce          sync.Once
	closeErr           error
	errMu              sync.Mutex
	readErr            error
	terminalErr        error
}

func newReadIdleBody(parent context.Context, body io.ReadCloser, cancel context.CancelFunc, release func(), startupReadTimeout, idleTimeout time.Duration) io.ReadCloser {
	stream := &readIdleBody{
		body: body, cancel: cancel, release: release, startupReadTimeout: startupReadTimeout, idleTimeout: idleTimeout,
		parent: parent, readStarted: make(chan struct{}), readProgress: make(chan struct{}), done: make(chan struct{}),
	}
	go stream.watch()
	return stream
}

func (stream *readIdleBody) Read(destination []byte) (int, error) {
	select {
	case stream.readStarted <- struct{}{}:
	case <-stream.done:
		return 0, stream.terminalReadError()
	}
	read, err := stream.body.Read(destination)
	if read > 0 {
		select {
		case stream.readProgress <- struct{}{}:
		case <-stream.done:
		}
	}
	if err != nil {
		stream.errMu.Lock()
		stream.readErr = err
		terminalErr := stream.terminalErr
		stream.errMu.Unlock()
		stream.finish()
		if terminalErr != nil {
			err = terminalErr
		}
	}
	return read, err
}

func (stream *readIdleBody) Close() error {
	err := stream.closeBody()
	stream.finish()
	return err
}

func (stream *readIdleBody) watch() {
	timer := time.NewTimer(stream.startupReadTimeout)
	stopReadIdleTimer(timer)
	defer timer.Stop()
	armed := false
	startup := true
	for {
		select {
		case <-stream.parent.Done():
			stream.expire(stream.parent.Err())
			return
		case <-stream.done:
			return
		case <-stream.readStarted:
			if !armed {
				timeout := stream.idleTimeout
				if startup {
					timeout = stream.startupReadTimeout
				}
				timer.Reset(timeout)
				armed = true
			}
		case <-stream.readProgress:
			startup = false
			if armed {
				stopReadIdleTimer(timer)
				armed = false
			}
		case <-timer.C:
			select {
			case <-stream.readProgress:
				startup = false
				armed = false
				continue
			default:
			}
			stream.expire(ErrMediaSourceTimeout)
			return
		}
	}
}

func stopReadIdleTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func (stream *readIdleBody) expire(err error) {
	stream.errMu.Lock()
	if parentErr := stream.parent.Err(); parentErr != nil {
		err = parentErr
	}
	if stream.terminalErr == nil {
		stream.terminalErr = err
	}
	stream.errMu.Unlock()
	stream.cancel()
	_ = stream.closeBody()
	stream.finish()
}

func (stream *readIdleBody) closeBody() error {
	stream.closeOnce.Do(func() { stream.closeErr = stream.body.Close() })
	return stream.closeErr
}

func (stream *readIdleBody) terminalReadError() error {
	stream.errMu.Lock()
	defer stream.errMu.Unlock()
	if stream.terminalErr != nil {
		return stream.terminalErr
	}
	if stream.readErr != nil {
		return stream.readErr
	}
	return io.ErrClosedPipe
}
func (stream *readIdleBody) finish() {
	stream.finishOnce.Do(func() {
		close(stream.done)
		stream.cancel()
		stream.release()
	})
}

var playlistURIAttribute = regexp.MustCompile(`[,:][ \t]*[A-Z0-9-]*URI="([^"]+)"`)

const maximumPlaybackStartSeconds = 7 * 24 * 60 * 60
const proxyAssetSessionSQL = `
	UPDATE playback_sessions playback
	SET last_seen_at = now(),
		expires_at = GREATEST(playback.expires_at, now() + $4::interval)
	FROM auth_sessions session
	WHERE playback.id::text = $1
	  AND playback.token_hash = $2
	  AND playback.expires_at > now()
	  AND playback.last_seen_at > now() - $3::interval
	  AND session.id = playback.auth_session_id
	  AND session.revoked_at IS NULL
	  AND session.refresh_expires_at > now()
	  AND session.active_profile_id = playback.profile_id
	  AND session.profile_grant_expires_at > now()
	RETURNING playback.assets
`

func processedMediaStart(raw string) (float64, error) {
	if raw == "" {
		return 0, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds < 0 || seconds > maximumPlaybackStartSeconds {
		return 0, ErrSessionNotFound
	}
	return float64(seconds), nil
}

func (service *Service) ProxyAsset(w http.ResponseWriter, r *http.Request, sessionID, assetID, token, child string) error {
	target := ""
	signature := ""
	if child != "" {
		resolved, err := service.openTargetCapability(sessionID, assetID, child)
		if err != nil {
			return ErrSessionNotFound
		}
		target = resolved
		signature = service.signTarget(sessionID, assetID, target)
	}
	return service.proxyAsset(w, r, sessionID, assetID, token, target, signature, nil)
}

func (service *Service) proxyAsset(w http.ResponseWriter, r *http.Request, sessionID, assetID, token, target, signature string, buildChildURL func(deliveryChildState) (string, error)) error {
	if token == "" {
		return ErrSessionNotFound
	}
	digest := sha256.Sum256([]byte(token))
	var encodedAssets []byte
	err := service.pool.QueryRow(r.Context(), proxyAssetSessionSQL,
		sessionID, digest[:], intervalLiteral(playbackSessionIdleTTL), intervalLiteral(sessionTTL),
	).Scan(&encodedAssets)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSessionNotFound
	}
	if err != nil {
		return fmt.Errorf("query playback session: %w", err)
	}
	var assets []storedAsset
	if err := json.Unmarshal(encodedAssets, &assets); err != nil {
		return fmt.Errorf("decode playback assets: %w", err)
	}
	var asset *storedAsset
	for index := range assets {
		if assets[index].ID == assetID {
			asset = &assets[index]
			break
		}
	}
	if asset == nil {
		return ErrSessionNotFound
	}
	switch asset.Kind {
	case processingRemux, processingTranscodeAudio, processingTranscode:
		return service.proxyProcessingAssetWithLinks(w, r, sessionID, token, target, signature, *asset, buildChildURL)
	case assetKindEmbeddedSubtitle, assetKindConvertedSubtitle:
		if target != "" || signature != "" || r.URL.Query().Get("file") != "" {
			return ErrSessionNotFound
		}
		return service.proxyConvertedSubtitle(w, r, *asset)
	case assetKindBitmapSubtitle:
		return ErrSessionNotFound
	}

	upstreamURL := asset.URL
	if target != "" {
		if !service.validTargetSignature(sessionID, assetID, target, signature) || !validMediaURL(target) {
			return ErrSessionNotFound
		}
		upstreamURL = target
	}
	response, err := service.fetchProxyAsset(r.Context(), r, *asset, upstreamURL, sessionID)
	if err != nil {
		return err
	}

	if asset.Kind != directSubtitleAssetKind && response.StatusCode >= 200 && response.StatusCode < 300 && playbackResponseHasBody(r.Method, response) && isHLSPlaylist(response, upstreamURL) {
		defer response.Body.Close()
		prefix, preflightErr := readPlaybackStartupByte(r.Context(), r.Method, response, service.directStartupReadTimeout())
		if preflightErr != nil {
			return classifyMediaStartupReadError(preflightErr)
		}
		body, err := io.ReadAll(io.LimitReader(io.MultiReader(bytes.NewReader(prefix), response.Body), maximumPlaylistBytes+1))
		if err != nil {
			return fmt.Errorf("read HLS playlist: %w", err)
		}
		if len(body) > maximumPlaylistBytes {
			return fmt.Errorf("%w: HLS playlist exceeds %d bytes", ErrMediaSourceFailed, maximumPlaylistBytes)
		}
		retainChildren := playlistChildrenRetainWhileActive(body)
		var childErr error
		rewritten, err := rewritePlaylistWithPolicy(body, response.Request.URL, buildChildURL != nil, func(resolved string) string {
			signed := service.signTarget(sessionID, assetID, resolved)
			if buildChildURL == nil {
				child, sealErr := service.sealTargetCapability(sessionID, assetID, resolved)
				if sealErr != nil {
					childErr = sealErr
					return ""
				}
				return assetURL(sessionID, assetID, token, child)
			}
			if childErr != nil {
				return ""
			}
			var childURL string
			childURL, childErr = buildChildURL(deliveryChildState{
				assetID: assetID, target: resolved, signature: signed, retainWhileActive: retainChildren,
			})
			return childURL
		})
		if childErr != nil {
			return fmt.Errorf("%w: create HLS child capability: %v", ErrMediaSourceFailed, childErr)
		}
		if err != nil {
			return fmt.Errorf("rewrite HLS playlist: %w", err)
		}
		return writeUpstreamHLSPlaylist(w, response.Header, rewritten)
	}

	return writeDirectProxyAssetWithStartupTimeout(w, r, *asset, response, upstreamURL, service.directStartupReadTimeout())
}

func (service *Service) directStartupReadTimeout() time.Duration {
	timeout := service.directStreamStartupReadTimeout
	if timeout <= 0 {
		return directStreamStartupReadTimeout
	}
	return timeout
}

func writeDirectProxyAsset(w http.ResponseWriter, r *http.Request, asset storedAsset, response *http.Response, upstreamURL string) error {
	return writeDirectProxyAssetWithStartupTimeout(w, r, asset, response, upstreamURL, directStreamStartupReadTimeout)
}

func writeDirectProxyAssetWithStartupTimeout(w http.ResponseWriter, r *http.Request, asset storedAsset, response *http.Response, upstreamURL string, startupTimeout time.Duration) error {
	if response.Body != nil {
		defer response.Body.Close()
	}
	successful := response.StatusCode >= 200 && response.StatusCode < 300
	boundedSubtitle := successful && asset.Kind == directSubtitleAssetKind && r.Method != http.MethodHead
	if boundedSubtitle && proxyResponseTotalLength(response) > maximumConvertedSubtitleBytes {
		return fmt.Errorf("%w: subtitle exceeds %d bytes", ErrMediaSourceFailed, maximumConvertedSubtitleBytes)
	}
	hasBody := playbackResponseHasBody(r.Method, response)
	prefix, err := readPlaybackStartupByte(r.Context(), r.Method, response, startupTimeout)
	if err != nil {
		return classifyMediaStartupReadError(err)
	}

	copyAssetHeaders(w.Header(), response.Header, true)
	if contentType := replacementContentType(response.Header.Get("Content-Type"), upstreamURL); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(response.StatusCode)
	if !successful {
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	if r.Method == http.MethodHead || !hasBody {
		return nil
	}
	if boundedSubtitle {
		source := io.Reader(bytes.NewReader(prefix))
		if response.Body != nil {
			source = io.MultiReader(source, response.Body)
		}
		if err = copyBoundedSubtitleAsset(w, source); err != nil {
			panic(http.ErrAbortHandler)
		}
		return nil
	}
	if len(prefix) > 0 {
		if _, err = w.Write(prefix); err != nil {
			panic(http.ErrAbortHandler)
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	if response.Body != nil {
		err = copyPlaybackAsset(w, response.Body)
	}
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	return nil
}

type onceCloseReadCloser struct {
	io.ReadCloser
	once sync.Once
	err  error
}

func (body *onceCloseReadCloser) Close() error {
	body.once.Do(func() { body.err = body.ReadCloser.Close() })
	return body.err
}

type startupReadResult struct {
	prefix []byte
	err    error
}

func readPlaybackStartupByte(ctx context.Context, method string, response *http.Response, timeout time.Duration) ([]byte, error) {
	if response == nil || response.StatusCode < 200 || response.StatusCode >= 300 || !playbackResponseHasBody(method, response) {
		return nil, nil
	}
	if _, ok := response.Body.(*onceCloseReadCloser); !ok {
		response.Body = &onceCloseReadCloser{ReadCloser: response.Body}
	}
	if timeout <= 0 {
		timeout = directStreamStartupReadTimeout
	}
	result := make(chan startupReadResult, 1)
	go func() {
		var first [1]byte
		read, err := io.ReadFull(response.Body, first[:])
		result <- startupReadResult{prefix: first[:read], err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case completed := <-result:
		if errors.Is(completed.err, io.EOF) {
			if playbackResponseDeclaresContent(response) {
				return completed.prefix, io.ErrUnexpectedEOF
			}
			return completed.prefix, nil
		}
		return completed.prefix, completed.err
	case <-ctx.Done():
		_ = response.Body.Close()
		return nil, ctx.Err()
	case <-timer.C:
		if err := ctx.Err(); err != nil {
			_ = response.Body.Close()
			return nil, err
		}
		_ = response.Body.Close()
		return nil, ErrMediaSourceTimeout
	}
}

func playbackResponseDeclaresContent(response *http.Response) bool {
	if response.ContentLength > 0 {
		return true
	}
	raw := strings.TrimSpace(response.Header.Get("Content-Length"))
	length, err := strconv.ParseInt(raw, 10, 64)
	return err == nil && length > 0
}

func playbackResponseHasBody(method string, response *http.Response) bool {
	if response == nil || response.Body == nil || method != http.MethodGet ||
		response.StatusCode >= 100 && response.StatusCode < 200 ||
		response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusResetContent ||
		response.StatusCode == http.StatusNotModified {
		return false
	}
	if raw := strings.TrimSpace(response.Header.Get("Content-Length")); raw != "" {
		length, err := strconv.ParseInt(raw, 10, 64)
		return err != nil || length != 0
	}
	return response.ContentLength != 0
}

func classifyMediaStartupReadError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrMediaSourceTimeout), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return fmt.Errorf("%w: read media source startup byte: %w", ErrMediaSourceFailed, err)
	}
}

func proxyResponseTotalLength(response *http.Response) int64 {
	length := response.ContentLength
	if raw := strings.TrimSpace(response.Header.Get("Content-Length")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed >= 0 {
			length = parsed
		}
	}
	if response.StatusCode != http.StatusPartialContent {
		return length
	}
	rawRange := strings.TrimSpace(response.Header.Get("Content-Range"))
	slash := strings.LastIndexByte(rawRange, '/')
	if slash < 0 || slash == len(rawRange)-1 || rawRange[slash+1:] == "*" {
		return length
	}
	total, err := strconv.ParseInt(rawRange[slash+1:], 10, 64)
	if err == nil && total >= 0 {
		return total
	}
	return length
}

func copyBoundedSubtitleAsset(destination io.Writer, source io.Reader) error {
	output := &maximumWriter{destination: destination, remaining: maximumConvertedSubtitleBytes}
	_, err := io.Copy(output, source)
	if output.exceeded {
		panic(http.ErrAbortHandler)
	}
	if err != nil {
		return fmt.Errorf("copy playback subtitle: %w", err)
	}
	return nil
}
func copyPlaybackAsset(destination io.Writer, source io.Reader) error {
	if _, err := io.Copy(destination, source); err != nil {
		return fmt.Errorf("copy playback asset: %w", err)
	}
	return nil
}

func writeUpstreamHLSPlaylist(w http.ResponseWriter, upstream http.Header, contents []byte) error {
	copyAssetHeaders(w.Header(), upstream, false)
	w.Header().Del("Accept-Ranges")
	w.Header().Del("Content-Range")
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Content-Length", strconv.Itoa(len(contents)))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(contents); err != nil {
		return fmt.Errorf("write HLS playlist: %w", err)
	}
	return nil
}

func (service *Service) proxyProcessingAsset(w http.ResponseWriter, r *http.Request, sessionID, token, target, signature string, asset storedAsset) error {
	return service.proxyProcessingAssetWithLinks(w, r, sessionID, token, target, signature, asset, nil)
}

func (service *Service) fetchProxyAsset(ctx context.Context, incoming *http.Request, asset storedAsset, upstreamURL, owner string) (*http.Response, error) {
	response, err := service.fetchAdmittedAsset(ctx, incoming, asset, upstreamURL, owner)
	if err != nil {
		return nil, fmt.Errorf("fetch playback asset: %w", err)
	}
	if incoming.Method != http.MethodGet || response.StatusCode != http.StatusPartialContent || !isHLSPlaylist(response, upstreamURL) {
		return response, nil
	}
	_ = response.Body.Close()
	fullRequest := incoming.Clone(ctx)
	fullRequest.Header = incoming.Header.Clone()
	fullRequest.Header.Del("Range")
	fullRequest.Header.Del("If-Range")
	response, err = service.fetchAdmittedAsset(ctx, fullRequest, asset, upstreamURL, owner)
	if err != nil {
		return nil, fmt.Errorf("refetch complete HLS playlist: %w", err)
	}
	if response.StatusCode == http.StatusPartialContent && isHLSPlaylist(response, upstreamURL) {
		_ = response.Body.Close()
		return nil, fmt.Errorf("%w: upstream returned a partial HLS playlist", ErrMediaSourceFailed)
	}
	return response, nil
}

func (service *Service) fetchAdmittedAsset(ctx context.Context, incoming *http.Request, asset storedAsset, upstreamURL, owner string) (*http.Response, error) {
	globalLimit := service.directStreamGlobalLimit
	if globalLimit <= 0 {
		globalLimit = maximumDirectStreamsGlobal
	}
	ownerLimit := service.directStreamOwnerLimit
	if ownerLimit <= 0 {
		ownerLimit = maximumDirectStreamsPerOwner
	}
	release, err := service.directStreams.acquire(owner, globalLimit, ownerLimit)
	if err != nil {
		return nil, err
	}
	requestContext, cancel := context.WithCancel(ctx)
	response, err := service.fetchAsset(requestContext, incoming, asset, upstreamURL)
	if err != nil {
		cancel()
		release()
		return nil, err
	}
	if response.Body == nil {
		cancel()
		release()
		return response, nil
	}
	idleTimeout := service.directStreamIdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = directStreamReadIdleTimeout
	}
	response.Body = newReadIdleBody(ctx, response.Body, cancel, release, idleTimeout, idleTimeout)
	return response, nil
}

func (service *Service) proxyProcessingAssetWithLinks(w http.ResponseWriter, r *http.Request, sessionID, token, target, signature string, asset storedAsset, buildChildURL func(deliveryChildState) (string, error)) error {
	if target != "" || signature != "" {
		return ErrSessionNotFound
	}
	if r.URL.Query().Get("file") == "" {
		return ErrClientCapabilityMissing
	}
	startSeconds, err := processedMediaStart(r.URL.Query().Get("start"))
	if err != nil {
		return err
	}
	asset.StartSeconds = startSeconds
	return service.serveHLS(w, r, sessionID, token, asset, buildChildURL)
}

func (service *Service) fetchAsset(ctx context.Context, incoming *http.Request, asset storedAsset, upstreamURL string) (*http.Response, error) {
	method := incoming.Method
	if method == http.MethodHead && !strings.EqualFold(asset.Container, "hls") {
		parsed, parseErr := url.Parse(upstreamURL)
		if parseErr == nil && !strings.EqualFold(pathExtension(parsed.Path), ".m3u8") {
			method = http.MethodGet
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, upstreamURL, nil)
	if err != nil {
		return nil, netguard.SanitizeURLError(err)
	}
	if sameMediaOrigin(asset.URL, upstreamURL) {
		headers, validHeaders := canonicalStoredRequestHeaders(asset.Headers)
		if !validHeaders {
			return nil, ErrMediaSourceFailed
		}
		for name, values := range headers {
			for _, value := range values {
				request.Header.Add(name, value)
			}
		}
	}
	for _, name := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if value := incoming.Header.Get(name); value != "" {
			request.Header.Set(name, value)
		}
	}
	request.Header.Set("User-Agent", "Rivune-Playback/1")
	requestwork.PropagateRequestID(request)
	started := requestwork.Now()
	requestwork.BeginOutbound(ctx, started)
	response, err := service.client.Do(request)
	if err != nil {
		requestwork.EndOutbound(ctx, requestwork.Now(), 0)
		return nil, netguard.SanitizeURLError(err)
	}
	if response.Body == nil {
		requestwork.EndOutbound(ctx, requestwork.Now(), 0)
	} else {
		response.Body = requestwork.ObserveBody(ctx, response.Body)
	}
	return response, nil
}

func sameMediaOrigin(left, right string) bool {
	leftOrigin, leftValid := canonicalMediaOrigin(left)
	rightOrigin, rightValid := canonicalMediaOrigin(right)
	return leftValid && rightValid && leftOrigin == rightOrigin
}

func allowedStoredRequestHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "host", "connection", "proxy-connection", "keep-alive", "transfer-encoding", "upgrade", "content-length", "accept-encoding", "range", "if-range":
		return false
	default:
		return true
	}
}

func copyAssetHeaders(destination, source http.Header, includeLength bool) {
	allowed := []string{
		"Accept-Ranges", "Cache-Control", "Content-Disposition", "Content-Range", "Content-Type",
		"ETag", "Expires", "Last-Modified",
	}
	if includeLength {
		allowed = append(allowed, "Content-Length")
	}
	for _, name := range allowed {
		for _, value := range source.Values(name) {
			destination.Add(name, value)
		}
	}
}

func replacementContentType(current, rawURL string) string {
	mediaType, _, err := mime.ParseMediaType(current)
	if err == nil && mediaType != "" && !strings.EqualFold(mediaType, "application/octet-stream") {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return mime.TypeByExtension(pathExtension(parsed.Path))
}

func isHLSPlaylist(response *http.Response, upstreamURL string) bool {
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "mpegurl") || strings.Contains(contentType, "vnd.apple.mpegurl") {
		return true
	}
	parsed, err := url.Parse(upstreamURL)
	return err == nil && strings.EqualFold(pathExtension(parsed.Path), ".m3u8")
}

func pathExtension(value string) string {
	index := strings.LastIndexByte(value, '.')
	if index < 0 {
		return ""
	}
	return value[index:]
}

func playlistChildrenRetainWhileActive(body []byte) bool {
	return bytes.Contains(body, []byte("#EXT-X-ENDLIST")) ||
		bytes.Contains(body, []byte("#EXT-X-PLAYLIST-TYPE:EVENT")) ||
		bytes.Contains(body, []byte("#EXT-X-PLAYLIST-TYPE:VOD")) ||
		bytes.Contains(body, []byte("#EXT-X-STREAM-INF:")) ||
		bytes.Contains(body, []byte("#EXT-X-I-FRAME-STREAM-INF:")) ||
		bytes.Contains(body, []byte("#EXT-X-MEDIA:"))
}

func rewritePlaylist(body []byte, base *url.URL, buildProxyURL func(string) string) ([]byte, error) {
	return rewritePlaylistWithPolicy(body, base, false, buildProxyURL)
}

func rewritePlaylistWithPolicy(body []byte, base *url.URL, rejectUnresolved bool, buildProxyURL func(string) string) ([]byte, error) {
	if err := validatePlaylistCardinality(body); err != nil {
		return nil, fmt.Errorf("%w: invalid HLS playlist: %w", ErrMediaSourceFailed, err)
	}
	output := newBoundedPlaylistOutput(len(body))
	scanner := playlistScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			if err := output.writeString(line); err != nil {
				return nil, fmt.Errorf("%w: invalid HLS playlist: %w", ErrMediaSourceFailed, err)
			}
		case strings.HasPrefix(trimmed, "#"):
			unresolved := false
			err := writePlaylistURIAttributes(output, line, func(reference string) (string, bool) {
				resolved, ok := resolvePlaylistReference(base, reference)
				if !ok {
					unresolved = true
					return "", false
				}
				return buildProxyURL(resolved), true
			})
			if err != nil {
				return nil, fmt.Errorf("%w: invalid HLS playlist: %w", ErrMediaSourceFailed, err)
			}
			if rejectUnresolved && unresolved {
				return nil, fmt.Errorf("%w: invalid HLS playlist: unproxyable reference", ErrMediaSourceFailed)
			}
		default:
			resolved, ok := resolvePlaylistReference(base, trimmed)
			if ok {
				line = buildProxyURL(resolved)
			} else if rejectUnresolved {
				return nil, fmt.Errorf("%w: invalid HLS playlist: unproxyable reference", ErrMediaSourceFailed)
			}
			if err := output.writeString(line); err != nil {
				return nil, fmt.Errorf("%w: invalid HLS playlist: %w", ErrMediaSourceFailed, err)
			}
		}
		if err := output.writeByte('\n'); err != nil {
			return nil, fmt.Errorf("%w: invalid HLS playlist: %w", ErrMediaSourceFailed, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: invalid HLS playlist: %w", ErrMediaSourceFailed, err)
	}
	return output.bytes(), nil
}

var (
	errPlaylistTooManyLines      = errors.New("playlist contains too many lines")
	errPlaylistTooManyReferences = errors.New("playlist contains too many references")
	errPlaylistOutputTooLarge    = errors.New("rewritten playlist exceeds output limit")
)

func playlistScanner(contents []byte) *bufio.Scanner {
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	scanner.Buffer(make([]byte, 64*1024), maximumPlaylistBytes+1)
	return scanner
}

func validatePlaylistCardinality(contents []byte) error {
	if len(contents) > maximumPlaylistBytes {
		return fmt.Errorf("playlist exceeds %d bytes", maximumPlaylistBytes)
	}
	scanner := playlistScanner(contents)
	lines := 0
	references := 0
	for scanner.Scan() {
		lines++
		if lines > maximumPlaylistLines {
			return errPlaylistTooManyLines
		}
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			for remaining := line; ; {
				match := playlistURIAttribute.FindStringSubmatchIndex(remaining)
				if match == nil {
					break
				}
				references++
				if references > maximumPlaylistReferences {
					return errPlaylistTooManyReferences
				}
				remaining = remaining[match[1]:]
			}
		} else if trimmed != "" {
			references++
			if references > maximumPlaylistReferences {
				return errPlaylistTooManyReferences
			}
		}
	}
	return scanner.Err()
}

type boundedPlaylistOutput struct {
	data []byte
}

func newBoundedPlaylistOutput(initialCapacity int) *boundedPlaylistOutput {
	if initialCapacity > maximumRewrittenPlaylistBytes {
		initialCapacity = maximumRewrittenPlaylistBytes
	}
	return &boundedPlaylistOutput{data: make([]byte, 0, initialCapacity)}
}

func (output *boundedPlaylistOutput) writeString(value string) error {
	if len(value) > maximumRewrittenPlaylistBytes-len(output.data) {
		return errPlaylistOutputTooLarge
	}
	output.grow(len(value))
	copy(output.data[len(output.data)-len(value):], value)
	return nil
}

func (output *boundedPlaylistOutput) writeByte(value byte) error {
	if len(output.data) == maximumRewrittenPlaylistBytes {
		return errPlaylistOutputTooLarge
	}
	output.grow(1)
	output.data[len(output.data)-1] = value
	return nil
}

func (output *boundedPlaylistOutput) grow(additional int) {
	required := len(output.data) + additional
	if required <= cap(output.data) {
		output.data = output.data[:required]
		return
	}
	capacity := cap(output.data) * 2
	if capacity < required {
		capacity = required
	}
	if capacity > maximumRewrittenPlaylistBytes {
		capacity = maximumRewrittenPlaylistBytes
	}
	grown := make([]byte, required, capacity)
	copy(grown, output.data)
	output.data = grown
}

func (output *boundedPlaylistOutput) bytes() []byte {
	return output.data
}

func writePlaylistURIAttributes(output *boundedPlaylistOutput, line string, rewrite func(string) (string, bool)) error {
	remaining := line
	for {
		match := playlistURIAttribute.FindStringSubmatchIndex(remaining)
		if match == nil {
			return output.writeString(remaining)
		}
		if err := output.writeString(remaining[:match[2]]); err != nil {
			return err
		}
		reference := remaining[match[2]:match[3]]
		if replacement, ok := rewrite(reference); ok {
			if err := output.writeString(replacement); err != nil {
				return err
			}
		} else if err := output.writeString(reference); err != nil {
			return err
		}
		remaining = remaining[match[3]:]
	}
}

func resolvePlaylistReference(base *url.URL, reference string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(reference))
	if err != nil {
		return "", false
	}
	resolved := base.ResolveReference(parsed)
	if !validMediaURL(resolved.String()) {
		return "", false
	}
	return resolved.String(), true
}

func (service *Service) sealTargetCapability(sessionID, assetID, target string) (string, error) {
	if service.targetCapabilityKey == ([32]byte{}) || !validMediaURL(target) {
		return "", ErrSessionNotFound
	}
	block, err := aes.NewCipher(service.targetCapabilityKey[:])
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	sealed := make([]byte, aead.NonceSize(), aead.NonceSize()+len(target)+aead.Overhead())
	if _, err := rand.Read(sealed); err != nil {
		return "", err
	}
	sealed = aead.Seal(sealed, sealed, []byte(target), targetCapabilityAAD(sessionID, assetID))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (service *Service) openTargetCapability(sessionID, assetID, capability string) (string, error) {
	if service.targetCapabilityKey == ([32]byte{}) || len(capability) == 0 || len(capability) > maximumTargetCapabilityLength {
		return "", ErrSessionNotFound
	}
	sealed, err := base64.RawURLEncoding.DecodeString(capability)
	if err != nil {
		return "", ErrSessionNotFound
	}
	block, err := aes.NewCipher(service.targetCapabilityKey[:])
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return "", ErrSessionNotFound
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], targetCapabilityAAD(sessionID, assetID))
	if err != nil || !validMediaURL(string(plain)) {
		return "", ErrSessionNotFound
	}
	return string(plain), nil
}

func targetCapabilityAAD(sessionID, assetID string) []byte {
	value := make([]byte, 0, len(sessionID)+len(assetID)+1)
	value = append(value, sessionID...)
	value = append(value, 0)
	return append(value, assetID...)
}

func (service *Service) signTarget(sessionID, assetID, target string) string {
	mac := hmac.New(sha256.New, service.targetSigningKey[:])
	writeTargetSignaturePayload(mac, sessionID, assetID, target)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (service *Service) validTargetSignature(sessionID, assetID, target, signature string) bool {
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || service.targetSigningKey == ([32]byte{}) {
		return false
	}
	mac := hmac.New(sha256.New, service.targetSigningKey[:])
	writeTargetSignaturePayload(mac, sessionID, assetID, target)
	return hmac.Equal(provided, mac.Sum(nil))
}

func writeTargetSignaturePayload(destination io.Writer, sessionID, assetID, target string) {
	_, _ = io.WriteString(destination, sessionID)
	_, _ = destination.Write([]byte{0})
	_, _ = io.WriteString(destination, assetID)
	_, _ = destination.Write([]byte{0})
	_, _ = io.WriteString(destination, target)
}

func assetURL(sessionID, assetID, token, child string) string {
	values := url.Values{"token": []string{token}}
	if child != "" {
		values.Set("child", child)
	}
	return "/api/v1/playback/sessions/" + url.PathEscape(sessionID) + "/assets/" + url.PathEscape(assetID) + "?" + values.Encode()
}
