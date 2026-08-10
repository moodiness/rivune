package playback

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
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
	maximumPlaylistBytes          = 8 * 1024 * 1024
	maximumPlaylistLines          = 2*maximumPlaylistReferences + 16
	maximumPlaylistReferences     = 10_000
	maximumRewrittenPlaylistBytes = 16 * 1024 * 1024
	maximumDirectStreamsGlobal    = 64
	maximumDirectStreamsPerOwner  = 4
	maximumDirectStreamsPerHost   = 16
	directStreamReadIdleTimeout   = 45 * time.Second
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

type directStreamBody struct {
	body         io.ReadCloser
	cancel       context.CancelFunc
	release      func()
	idleTimeout  time.Duration
	parentDone   <-chan struct{}
	readStarted  chan struct{}
	readProgress chan struct{}
	done         chan struct{}
	finishOnce   sync.Once
	closeOnce    sync.Once
	closeErr     error
}

func newDirectStreamBody(parent context.Context, body io.ReadCloser, cancel context.CancelFunc, release func(), idleTimeout time.Duration) io.ReadCloser {
	stream := &directStreamBody{
		body: body, cancel: cancel, release: release, idleTimeout: idleTimeout,
		parentDone: parent.Done(), readStarted: make(chan struct{}), readProgress: make(chan struct{}), done: make(chan struct{}),
	}
	go stream.watch()
	return stream
}

func (stream *directStreamBody) Read(destination []byte) (int, error) {
	select {
	case stream.readStarted <- struct{}{}:
	case <-stream.done:
		return 0, io.ErrClosedPipe
	}
	read, err := stream.body.Read(destination)
	if read > 0 {
		select {
		case stream.readProgress <- struct{}{}:
		case <-stream.done:
		}
	}
	if err != nil {
		stream.finish()
	}
	return read, err
}

func (stream *directStreamBody) Close() error {
	err := stream.closeBody()
	stream.finish()
	return err
}

func (stream *directStreamBody) watch() {
	timer := time.NewTimer(stream.idleTimeout)
	stopDirectStreamTimer(timer)
	defer timer.Stop()
	armed := false
	for {
		select {
		case <-stream.parentDone:
			stream.expire()
			return
		case <-stream.done:
			return
		case <-stream.readStarted:
			if !armed {
				timer.Reset(stream.idleTimeout)
				armed = true
			}
		case <-stream.readProgress:
			if armed {
				stopDirectStreamTimer(timer)
				armed = false
			}
		case <-timer.C:
			select {
			case <-stream.readProgress:
				armed = false
				continue
			default:
			}
			stream.expire()
			return
		}
	}
}

func stopDirectStreamTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func (stream *directStreamBody) expire() {
	stream.cancel()
	_ = stream.closeBody()
	stream.finish()
}

func (stream *directStreamBody) closeBody() error {
	stream.closeOnce.Do(func() { stream.closeErr = stream.body.Close() })
	return stream.closeErr
}

func (stream *directStreamBody) finish() {
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
	SET last_seen_at = now()
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

func (service *Service) ProxyAsset(w http.ResponseWriter, r *http.Request, sessionID, assetID, token, target, signature string) error {
	return service.proxyAsset(w, r, sessionID, assetID, token, target, signature, nil)
}

func (service *Service) proxyAsset(w http.ResponseWriter, r *http.Request, sessionID, assetID, token, target, signature string, buildChildURL func(deliveryChildState) (string, error)) error {
	if token == "" {
		return ErrSessionNotFound
	}
	digest := sha256.Sum256([]byte(token))
	var encodedAssets []byte
	err := service.pool.QueryRow(r.Context(), proxyAssetSessionSQL,
		sessionID, digest[:], intervalLiteral(playbackSessionIdleTTL),
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
	defer response.Body.Close()

	if r.Method == http.MethodGet && response.StatusCode >= 200 && response.StatusCode < 300 && isHLSPlaylist(response, upstreamURL) {
		body, err := io.ReadAll(io.LimitReader(response.Body, maximumPlaylistBytes+1))
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
				return assetURL(sessionID, assetID, token, resolved, signed)
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

	copyAssetHeaders(w.Header(), response.Header, true)
	if contentType := replacementContentType(response.Header.Get("Content-Type"), upstreamURL); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(response.StatusCode)
	if r.Method == http.MethodHead {
		return nil
	}
	return copyPlaybackAsset(w, response.Body)
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
	response.Body = newDirectStreamBody(ctx, response.Body, cancel, release, idleTimeout)
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

func assetURL(sessionID, assetID, token, target, signature string) string {
	values := url.Values{"token": []string{token}}
	if target != "" {
		values.Set("target", target)
		values.Set("signature", signature)
	}
	return "/api/v1/playback/sessions/" + url.PathEscape(sessionID) + "/assets/" + url.PathEscape(assetID) + "?" + values.Encode()
}
