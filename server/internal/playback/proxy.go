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

	"github.com/jackc/pgx/v5"
)

const maximumPlaylistBytes = 8 * 1024 * 1024

var playlistURIAttribute = regexp.MustCompile(`URI="([^"]+)"`)

const maximumPlaybackStartSeconds = 7 * 24 * 60 * 60

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
	if token == "" {
		return ErrSessionNotFound
	}
	digest := sha256.Sum256([]byte(token))
	var encodedAssets []byte
	err := service.pool.QueryRow(r.Context(), `
		UPDATE playback_sessions playback
		SET last_seen_at = now()
		FROM auth_sessions session
		WHERE playback.id::text = $1
		  AND playback.token_hash = $2
		  AND playback.expires_at > now()
		  AND session.id = playback.auth_session_id
		  AND session.revoked_at IS NULL
		  AND session.refresh_expires_at > now()
		  AND session.active_profile_id = playback.profile_id
		  AND session.profile_grant_expires_at > now()
		RETURNING playback.assets
	`, sessionID, digest[:]).Scan(&encodedAssets)
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
		startSeconds, err := processedMediaStart(r.URL.Query().Get("start"))
		if err != nil {
			return err
		}
		asset.StartSeconds = startSeconds
		if target != "" || signature != "" {
			return ErrSessionNotFound
		}
		if r.URL.Query().Get("file") != "" {
			return service.serveHLS(w, r, sessionID, token, *asset)
		}
		if r.URL.Query().Get("fallback") == "1" {
			service.stopOtherHLSGenerations(hlsJobPrefix(sessionID, asset.ID), "")
		}
		return service.proxyProcessedMedia(w, r, *asset)
	case assetKindEmbeddedSubtitle, assetKindConvertedSubtitle:
		if target != "" || signature != "" || r.URL.Query().Get("file") != "" {
			return ErrSessionNotFound
		}
		return service.proxyConvertedSubtitle(w, r, *asset)
	}

	upstreamURL := asset.URL
	if target != "" {
		if !validTargetSignature(token, target, signature) || !validMediaURL(target) {
			return ErrSessionNotFound
		}
		upstreamURL = target
	}
	response, err := service.fetchAsset(r.Context(), r, *asset, upstreamURL)
	if err != nil {
		return fmt.Errorf("fetch playback asset: %w", err)
	}
	defer response.Body.Close()

	if r.Method == http.MethodGet && response.StatusCode >= 200 && response.StatusCode < 300 && isHLSPlaylist(response, upstreamURL) {
		body, err := io.ReadAll(io.LimitReader(response.Body, maximumPlaylistBytes+1))
		if err != nil {
			return fmt.Errorf("read HLS playlist: %w", err)
		}
		if len(body) > maximumPlaylistBytes {
			return fmt.Errorf("HLS playlist exceeds %d bytes", maximumPlaylistBytes)
		}
		rewritten, err := rewritePlaylist(body, response.Request.URL, func(resolved string) string {
			signed := signTarget(token, resolved)
			return assetURL(sessionID, assetID, token, resolved, signed)
		})
		if err != nil {
			return fmt.Errorf("rewrite HLS playlist: %w", err)
		}
		copyAssetHeaders(w.Header(), response.Header, false)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(rewritten)))
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(rewritten)
		return nil
	}

	copyAssetHeaders(w.Header(), response.Header, true)
	if contentType := replacementContentType(response.Header.Get("Content-Type"), upstreamURL); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(response.StatusCode)
	if r.Method == http.MethodHead {
		return nil
	}
	_, _ = io.Copy(w, response.Body)
	return nil
}

func (service *Service) fetchAsset(ctx context.Context, incoming *http.Request, asset storedAsset, upstreamURL string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, incoming.Method, upstreamURL, nil)
	if err != nil {
		return nil, err
	}
	for name, value := range asset.Headers {
		if allowedStoredRequestHeader(name) {
			request.Header.Set(name, value)
		}
	}
	for _, name := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if value := incoming.Header.Get(name); value != "" {
			request.Header.Set(name, value)
		}
	}
	request.Header.Set("User-Agent", "Rivune-Playback/1")
	return service.client.Do(request)
}

func allowedStoredRequestHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "host", "connection", "proxy-connection", "keep-alive", "transfer-encoding", "upgrade", "content-length", "accept-encoding", "range":
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

func rewritePlaylist(body []byte, base *url.URL, buildProxyURL func(string) string) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), maximumPlaylistBytes)
	var rewritten strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
		case strings.HasPrefix(trimmed, "#"):
			line = playlistURIAttribute.ReplaceAllStringFunc(line, func(match string) string {
				parts := playlistURIAttribute.FindStringSubmatch(match)
				if len(parts) != 2 {
					return match
				}
				resolved, ok := resolvePlaylistReference(base, parts[1])
				if !ok {
					return match
				}
				return `URI="` + buildProxyURL(resolved) + `"`
			})
		default:
			if resolved, ok := resolvePlaylistReference(base, trimmed); ok {
				line = buildProxyURL(resolved)
			}
		}
		rewritten.WriteString(line)
		rewritten.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return []byte(rewritten.String()), nil
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

func signTarget(token, target string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte(target))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func validTargetSignature(token, target, signature string) bool {
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte(target))
	return hmac.Equal(provided, mac.Sum(nil))
}

func assetURL(sessionID, assetID, token, target, signature string) string {
	values := url.Values{"token": []string{token}}
	if target != "" {
		values.Set("target", target)
		values.Set("signature", signature)
	}
	return "/api/v1/playback/sessions/" + url.PathEscape(sessionID) + "/assets/" + url.PathEscape(assetID) + "?" + values.Encode()
}
