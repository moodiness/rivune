package demo

import (
	"bytes"
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

func (s *Service) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, cookieErr := r.Cookie(CookieName)
		cookiePresent := cookieErr == nil || requestHasCookieName(r, CookieName)
		demoPath := strings.HasPrefix(r.URL.Path, APIPrefix+"/demo/")
		if !demoPath && (!cookiePresent || !strings.HasPrefix(r.URL.Path, APIPrefix+"/")) {
			next.ServeHTTP(w, r)
			return
		}
		if s.admission == nil {
			writeError(w, 500, "demo_internal_error", "The demo is temporarily unavailable")
			return
		}
		release, err := s.admission.AcquireSetupPending(r.Context())
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
		defer release()
		if !validOrigin(r) {
			writeError(w, 403, "invalid_origin", "The request origin does not match this server")
			return
		}

		if r.URL.Path == APIPrefix+"/demo/sessions" {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, http.MethodPost)
				return
			}
			if cookieErr == nil {
				_, digest := s.session(cookie.Value)
				s.remove(digest)
			}
			s.createSession(w, r)
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
		if s.dispatch(w, r, current, digest) {
			return
		}
		writeError(w, 403, "demo_forbidden", "Demo sessions cannot access this endpoint")
	})
}

func (s *Service) createSession(w http.ResponseWriter, r *http.Request) {
	value, current, err := s.newSession()
	if err != nil {
		writeError(w, 500, "demo_internal_error", "The demo is temporarily unavailable")
		return
	}
	setCookie(w, r, value, current.expiresAt, s.ttl)
	current.mu.Lock()
	account := accountLocked(current)
	current.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"account": account})
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
		current.state = freshState(s.now().UTC())
		account := accountLocked(current)
		current.mu.Unlock()
		writeJSON(w, 200, map[string]any{"account": account})
	case p == APIPrefix+"/demo/session" && r.Method == http.MethodDelete:
		s.remove(digest)
		expireCookie(w, r)
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(p, APIPrefix+"/demo/assets/") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		s.serveAsset(w, r, strings.TrimPrefix(p, APIPrefix+"/demo/assets/"))
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

func (s *Service) serveAsset(w http.ResponseWriter, r *http.Request, name string) {
	if name == "" || path.Base(name) != name || strings.Contains(name, "\\") {
		writeError(w, 404, "demo_asset_not_found", "The demo asset does not exist")
		return
	}
	data, err := embeddedAsset(name)
	if err != nil {
		writeError(w, 404, "demo_asset_not_found", "The demo asset does not exist")
		return
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
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
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
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64*1024))
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
