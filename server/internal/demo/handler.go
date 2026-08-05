package demo

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/instance"
)

//go:embed assets/demo-720p.mp4 assets/demo-360p.mp4 assets/demo.en.vtt assets/demo.fr.vtt assets/*.svg
var embeddedAssets embed.FS
var assetCache = struct {
	sync.Mutex
	values map[string][]byte
}{values: make(map[string][]byte)}

type errorEnvelope struct {
	Error apiError `json:"error"`
}
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type bufferedResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: make(http.Header)}
}

func (b *bufferedResponse) Header() http.Header {
	return b.header
}

func (b *bufferedResponse) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

func (b *bufferedResponse) Write(data []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(data)
}

func (b *bufferedResponse) flushTo(destination http.ResponseWriter) {
	for name, values := range b.header {
		destination.Header()[name] = append([]string(nil), values...)
	}
	status := b.status
	if status == 0 {
		status = http.StatusOK
	}
	destination.WriteHeader(status)
	if b.body.Len() != 0 {
		_, _ = destination.Write(b.body.Bytes())
	}
}

type preparedAsset struct {
	name        string
	contentType string
	data        []byte
}

func (s *Service) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(destination http.ResponseWriter, r *http.Request) {
		cookie, cookieErr := r.Cookie(CookieName)
		cookiePresent := cookieErr == nil || requestHasCookieName(r, CookieName)
		demoPath := strings.HasPrefix(r.URL.Path, APIPrefix+"/demo/")
		if !demoPath && (!cookiePresent || !strings.HasPrefix(r.URL.Path, APIPrefix+"/")) {
			next.ServeHTTP(destination, r)
			return
		}
		if s.admission == nil {
			writeError(destination, 500, "demo_internal_error", "The demo is temporarily unavailable")
			return
		}

		response := newBufferedResponse()
		var release func()
		var stream func()
		defer func() {
			if release != nil {
				release()
			}
			if stream != nil {
				stream()
				return
			}
			response.flushTo(destination)
		}()
		w := http.ResponseWriter(response)

		if !validOrigin(r) {
			writeError(w, 403, "invalid_origin", "The request origin does not match this server")
			return
		}
		if s.isDisabled() {
			expireCookie(w, r)
			writeError(w, http.StatusGone, "demo_unavailable", "The server setup has been completed. Demo mode is no longer available.")
			return
		}

		if r.URL.Path == APIPrefix+"/demo/sessions" {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, http.MethodPost)
				return
			}
			replacedValue := ""
			if cookieErr == nil {
				replacedValue = cookie.Value
			}
			release = s.createSession(w, r, replacedValue)
			return
		}
		if !cookiePresent || cookieErr != nil || cookie.Value == "" {
			expireCookie(w, r)
			writeError(w, 401, "demo_session_invalid", "A valid demo session is required")
			return
		}
		current, digest := s.session(cookie.Value)
		if current == nil {
			expireCookie(w, r)
			writeError(w, 401, "demo_session_invalid", "A valid demo session is required")
			return
		}
		if r.URL.Path == APIPrefix+"/demo/session" && r.Method == http.MethodDelete {
			var err error
			release, err = s.releaseSession(r.Context(), digest, current)
			if errors.Is(err, instance.ErrAlreadyConfigured) {
				s.Disable()
				expireCookie(w, r)
				writeError(w, http.StatusGone, "demo_unavailable", "The server setup has been completed. Demo mode is no longer available.")
				return
			}
			if err != nil {
				writeError(w, 500, "demo_internal_error", "The demo is temporarily unavailable")
				return
			}
			expireCookie(w, r)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.HasPrefix(r.URL.Path, APIPrefix+"/demo/assets/") &&
			(r.Method == http.MethodGet || r.Method == http.MethodHead) {
			asset, assetErr := prepareAsset(strings.TrimPrefix(r.URL.Path, APIPrefix+"/demo/assets/"))
			if assetErr != nil {
				writeError(w, 404, "demo_asset_not_found", "The demo asset does not exist")
				return
			}
			var err error
			release, err = s.admission.AcquireSetupPending(r.Context())
			if !s.handleAdmissionError(w, r, err) {
				return
			}
			stream = func() {
				servePreparedAsset(destination, r, asset)
			}
			return
		}

		handled, readsBody := classifyDemoRoute(r)
		if !handled {
			writeError(w, 403, "demo_forbidden", "Demo sessions cannot access this endpoint")
			return
		}
		if readsBody {
			if err := snapshotRequestBody(r); err != nil {
				writeError(w, 400, "invalid_request", fmt.Sprintf("invalid JSON body: %v", err))
				return
			}
		}

		var err error
		release, err = s.admission.AcquireSetupPending(r.Context())
		if !s.handleAdmissionError(w, r, err) {
			return
		}
		if s.dispatch(w, r, current, digest) {
			return
		}
		panic("classified demo route was not dispatched")
	})
}

func (s *Service) handleAdmissionError(w http.ResponseWriter, r *http.Request, err error) bool {
	if errors.Is(err, instance.ErrAlreadyConfigured) {
		s.Disable()
		expireCookie(w, r)
		writeError(w, http.StatusGone, "demo_unavailable", "The server setup has been completed. Demo mode is no longer available.")
		return false
	}
	if err != nil {
		writeError(w, 500, "demo_internal_error", "The demo is temporarily unavailable")
		return false
	}
	return true
}

func classifyDemoRoute(r *http.Request) (handled, readsBody bool) {
	p := r.URL.Path
	switch {
	case p == APIPrefix+"/demo/session" && r.Method == http.MethodGet:
		return true, false
	case p == APIPrefix+"/demo/session/reset" && r.Method == http.MethodPost:
		return true, r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0
	case p == APIPrefix+"/auth/me" && r.Method == http.MethodGet:
		return true, false
	case p == APIPrefix+"/auth/sessions" && r.Method == http.MethodGet:
		return true, false
	case p == APIPrefix+"/auth/notifications" && r.Method == http.MethodGet:
		return true, false
	case strings.HasPrefix(p, APIPrefix+"/auth/notifications/") && r.Method == http.MethodDelete:
		return true, false
	}

	p = strings.TrimPrefix(p, APIPrefix)
	switch {
	case p == "/profiles" && r.Method == http.MethodGet:
		return true, false
	case p == "/profiles/selection" && r.Method == http.MethodDelete:
		return true, false
	case strings.HasPrefix(p, "/profiles/") && strings.HasSuffix(p, "/select") && r.Method == http.MethodPost:
		return true, true
	case strings.HasPrefix(p, "/profiles/") && strings.HasSuffix(p, "/settings/effective") && r.Method == http.MethodGet:
		return true, false
	case p == "/collections" && r.Method == http.MethodGet:
		return true, false
	case strings.HasPrefix(p, "/collections/") && strings.Contains(p, "/folders/") && strings.HasSuffix(p, "/items") && r.Method == http.MethodGet:
		return true, false
	case p == "/continue-watching" && r.Method == http.MethodGet:
		return true, false
	case strings.HasPrefix(p, "/continue-watching/") && r.Method == http.MethodDelete:
		return true, false
	case p == "/library" && r.Method == http.MethodGet:
		return true, false
	case strings.HasPrefix(p, "/library/") && (r.Method == http.MethodPut || r.Method == http.MethodDelete):
		return true, false
	case p == "/titles/resolve" && r.Method == http.MethodPost:
		return true, true
	case strings.HasPrefix(p, "/progress/") &&
		(r.Method == http.MethodGet || r.Method == http.MethodPut || r.Method == http.MethodDelete):
		return true, r.Method == http.MethodPut
	case strings.HasPrefix(p, "/titles/") && strings.HasSuffix(p, "/watched") &&
		(r.Method == http.MethodPost || r.Method == http.MethodDelete):
		return true, r.Method == http.MethodPost
	case strings.HasPrefix(p, "/addons/catalogs/search/") && r.Method == http.MethodGet:
		return true, false
	case strings.HasPrefix(p, "/addons/resources/meta/") && r.Method == http.MethodGet:
		return true, false
	case strings.HasPrefix(p, "/metadata/titles/") && r.Method == http.MethodGet:
		return true, false
	case strings.HasPrefix(p, "/metadata/series/") && r.Method == http.MethodGet:
		return true, false
	case strings.HasPrefix(p, "/metadata/seasons/") && r.Method == http.MethodGet:
		return true, false
	case p == "/playback/sources" && r.Method == http.MethodPost:
		return true, true
	case p == "/playback/prepare" && r.Method == http.MethodPost:
		return true, true
	case p == "/playback/resolve" && r.Method == http.MethodPost:
		return true, true
	case p == "/playback/markers" && r.Method == http.MethodGet:
		return true, false
	case strings.HasPrefix(p, "/playback/sessions/") && r.Method == http.MethodDelete:
		return true, false
	case p == "/calendar" && r.Method == http.MethodGet:
		return true, false
	default:
		return false, false
	}
}

const maxJSONBodyBytes = 64 * 1024

func snapshotRequestBody(r *http.Request) error {
	original := r.Body
	if original == nil {
		original = http.NoBody
	}
	data, err := io.ReadAll(io.LimitReader(original, maxJSONBodyBytes+1))
	_ = original.Close()
	if err != nil {
		return err
	}
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	r.Body, _ = r.GetBody()
	return nil
}

func (s *Service) createSession(w http.ResponseWriter, r *http.Request, replacedValue string) func() {
	clientIP := auth.ClientIP(r.Context())
	if clientIP == "" {
		clientIP = "unknown"
	}
	sourceHash := sha256.Sum256([]byte("rivune-demo-client-ip:" + clientIP))
	value, current, release, err := s.newSession(r.Context(), sourceHash, replacedValue)
	if errors.Is(err, instance.ErrAlreadyConfigured) {
		s.Disable()
		expireCookie(w, r)
		writeError(w, http.StatusGone, "demo_unavailable", "The server setup has been completed. Demo mode is no longer available.")
		return nil
	}
	if errors.Is(err, instance.ErrDemoSessionCapacity) {
		writeError(w, http.StatusTooManyRequests, "demo_session_limit", "The demo session limit has been reached")
		return nil
	}
	if err != nil {
		writeError(w, 500, "demo_internal_error", "The demo is temporarily unavailable")
		return nil
	}
	setCookie(w, r, value, current.expiresAt, s.ttl)
	current.mu.Lock()
	account := accountLocked(current)
	current.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"account": account})
	return release
}

func (s *Service) dispatch(w http.ResponseWriter, r *http.Request, current *session, digest [32]byte) bool {
	p := r.URL.Path
	switch {
	case p == APIPrefix+"/demo/session" && r.Method == http.MethodGet:
		current.mu.Lock()
		account := accountLocked(current)
		current.mu.Unlock()
		writeJSON(w, 200, map[string]any{"account": account})
	case p == APIPrefix+"/demo/session/reset" && r.Method == http.MethodPost:
		if err := decodeEmptyJSON(r); err != nil {
			writeError(w, 400, "invalid_request", err.Error())
			return true
		}
		current.mu.Lock()
		s.resetStateLocked(current, s.now().UTC())
		account := accountLocked(current)
		current.mu.Unlock()
		writeJSON(w, 200, map[string]any{"account": account})
	case p == APIPrefix+"/auth/me" && r.Method == http.MethodGet:
		current.mu.Lock()
		account := accountLocked(current)
		current.mu.Unlock()
		writeJSON(w, 200, account)
	case p == APIPrefix+"/auth/sessions" && r.Method == http.MethodGet:
		s.authSessions(w, current)
	case p == APIPrefix+"/auth/notifications" && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{"notifications": []any{}})
	case strings.HasPrefix(p, APIPrefix+"/auth/notifications/") && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		return s.dispatchApplication(w, r, current)
	}
	return true
}

func accountLocked(current *session) map[string]any {
	var active any
	if current.state.activeProfileID != "" {
		active = map[string]any{"id": current.state.activeProfileID, "expiresAt": current.expiresAt}
	}
	return map[string]any{
		"user": map[string]string{"id": DemoUserID, "username": "demo", "role": "demo"},
		"session": map[string]any{
			"id": current.id, "deviceId": DemoDeviceID, "authorizationScope": "category",
			"category": demoCategoryRef(), "activeProfile": active,
		},
		"profiles": profileRecords(), "maintenance": map[string]any{"enabled": false, "message": nil},
	}
}

func (s *Service) authSessions(w http.ResponseWriter, current *session) {
	now := s.now().UTC()
	current.mu.Lock()
	active := current.state.activeProfileID
	current.mu.Unlock()
	grant := current.expiresAt
	sessions := []map[string]any{
		{"id": current.id, "userId": DemoUserID, "username": "demo", "deviceId": DemoDeviceID, "deviceName": "Browser Review", "platform": "web", "ipAddress": nil, "authorizationScope": "category", "category": demoCategoryRef(), "createdAt": current.createdAt, "lastSeenAt": now, "profileGrantExpiresAt": grant, "current": true, "activeProfileId": active},
		{"id": "d5000000-0000-4000-8000-000000000001", "userId": DemoUserID, "username": "demo", "deviceId": "d5100000-0000-4000-8000-000000000001", "deviceName": "Living Room TV", "platform": "tv", "ipAddress": nil, "authorizationScope": "category", "category": demoCategoryRef(), "createdAt": now.Add(-2 * time.Hour), "lastSeenAt": now.Add(-4 * time.Minute), "profileGrantExpiresAt": grant, "current": false},
		{"id": "d5000000-0000-4000-8000-000000000002", "userId": DemoUserID, "username": "demo", "deviceId": "d5100000-0000-4000-8000-000000000002", "deviceName": "Tablet", "platform": "tablet", "ipAddress": nil, "authorizationScope": "category", "category": demoCategoryRef(), "createdAt": now.Add(-24 * time.Hour), "lastSeenAt": now.Add(-30 * time.Minute), "profileGrantExpiresAt": grant, "current": false},
	}
	writeJSON(w, 200, map[string]any{"sessions": sessions})
}

func prepareAsset(name string) (preparedAsset, error) {
	if name == "" || path.Base(name) != name || strings.Contains(name, "\\") {
		return preparedAsset{}, fs.ErrNotExist
	}
	data, err := embeddedAsset(name)
	if err != nil {
		return preparedAsset{}, err
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	switch path.Ext(name) {
	case ".svg":
		contentType = "image/svg+xml"
	case ".mp4":
		contentType = "video/mp4"
	case ".vtt":
		contentType = "text/vtt; charset=utf-8"
	}
	return preparedAsset{name: name, contentType: contentType, data: data}, nil
}

func servePreparedAsset(w http.ResponseWriter, r *http.Request, asset preparedAsset) {
	w.Header().Set("Content-Type", asset.contentType)
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, asset.name, time.Time{}, bytes.NewReader(asset.data))
}
func embeddedAsset(name string) ([]byte, error) {
	assetCache.Lock()
	defer assetCache.Unlock()
	if data, ok := assetCache.values[name]; ok {
		return data, nil
	}
	data, err := fs.ReadFile(embeddedAssets, "assets/"+name)
	if err != nil {
		return nil, err
	}
	assetCache.values[name] = data
	return data, nil
}

func setCookie(w http.ResponseWriter, r *http.Request, value string, expires time.Time, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: value, Path: APIPrefix, HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: int(ttl.Seconds())})
}
func expireCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Path: APIPrefix, HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteStrictMode, Expires: time.Unix(1, 0), MaxAge: -1})
}
func secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(forwarded, "https")
}
func requestHasCookieName(r *http.Request, name string) bool {
	for _, header := range r.Header.Values("Cookie") {
		for _, pair := range strings.Split(header, ";") {
			if cookieName, _, found := strings.Cut(strings.TrimSpace(pair), "="); found && cookieName == name {
				return true
			}
		}
	}
	return false
}
func validOrigin(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	scheme := "http"
	if secureRequest(r) {
		scheme = "https"
	}
	expected, err := url.Parse(scheme + "://" + r.Host)
	if err != nil || !strings.EqualFold(parsed.Scheme, scheme) || !strings.EqualFold(parsed.Hostname(), expected.Hostname()) {
		return false
	}
	return originPort(parsed) == originPort(expected)
}
func originPort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	if strings.EqualFold(value.Scheme, "https") {
		return "443"
	}
	return "80"
}
func decodeStrict(r *http.Request, destination any) error {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		return errors.New("Content-Type must be application/json")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxJSONBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}
func decodeEmptyJSON(r *http.Request) error {
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return nil
	}
	var value struct{}
	return decodeStrict(r, &value)
}
func writeMethodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, 405, "method_not_allowed", "The request method is not allowed")
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
