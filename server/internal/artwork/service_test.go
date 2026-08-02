package artwork

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/database"
)

func TestArtworkKeyUsesNormalizedURL(t *testing.T) {
	first, err := normalizeURL(" https://EXAMPLE.com:443/poster.png#ignored ", true)
	if err != nil {
		t.Fatalf("normalize first URL: %v", err)
	}
	second, err := normalizeURL("https://example.com/poster.png", true)
	if err != nil {
		t.Fatalf("normalize second URL: %v", err)
	}
	if first != second {
		t.Fatalf("equivalent URLs normalized differently: %q != %q", first, second)
	}
	firstKey := artworkKey(first)
	if firstKey != artworkKey(second) || firstKey != artworkKey(first) {
		t.Fatal("artwork key was not deterministic")
	}
	if !validKey(firstKey) {
		t.Fatalf("generated key is invalid: %q", firstKey)
	}
	if firstKey == artworkKey("https://example.com/other.png") {
		t.Fatal("different normalized URLs produced the same key")
	}
}

func TestProductionURLValidationRejectsUnsafeTargets(t *testing.T) {
	invalid := []string{
		"http://example.com/poster.jpg",
		"ftp://example.com/poster.jpg",
		"https://user:secret@example.com/poster.jpg",
		"https://example.com:8443/poster.jpg",
		"https://localhost/poster.jpg",
		"https://127.0.0.1/poster.jpg",
		"https://10.1.2.3/poster.jpg",
		"https://169.254.169.254/latest/meta-data",
		"https://[::1]/poster.jpg",
		"https://[fe80::1]/poster.jpg",
		"/relative/poster.jpg",
	}
	for _, candidate := range invalid {
		t.Run(candidate, func(t *testing.T) {
			if normalized, err := normalizeURL(candidate, true); err == nil {
				t.Fatalf("unsafe URL accepted as %q", normalized)
			}
		})
	}
	if normalized, err := normalizeURL("https://Example.COM:443/poster.jpg?q=1", true); err != nil {
		t.Fatalf("safe URL rejected: %v", err)
	} else if normalized != "https://example.com/poster.jpg?q=1" {
		t.Fatalf("unexpected normalized URL: %q", normalized)
	}
}

func TestLocalURLsPreservesOrderDuplicatesAndFallbacks(t *testing.T) {
	pool := openArtworkTestPool(t)
	fixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)

	inputs := []string{fixture.URL + "/one", "not a URL", fixture.URL + "/two", fixture.URL + "/one"}
	localized := service.LocalURLs(context.Background(), inputs)
	if len(localized) != len(inputs) {
		t.Fatalf("localized length = %d, want %d", len(localized), len(inputs))
	}
	if localized[1] != inputs[1] {
		t.Fatalf("invalid URL changed to %q", localized[1])
	}
	if localized[0] != localized[3] || !strings.HasPrefix(localized[0], publicPrefix) || !strings.HasPrefix(localized[2], publicPrefix) {
		t.Fatalf("unexpected localized URLs: %#v", localized)
	}
	if localized[0] == localized[2] {
		t.Fatal("distinct upstream URLs received the same local URL")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	failed := service.LocalURLs(canceled, []string{fixture.URL + "/failed-one", fixture.URL + "/failed-two"})
	if failed[0] != fixture.URL+"/failed-one" || failed[1] != fixture.URL+"/failed-two" {
		t.Fatalf("failed registration did not preserve inputs: %#v", failed)
	}
}

func TestServeHTTPMissHitHeadAndUpstreamFailure(t *testing.T) {
	pool := openArtworkTestPool(t)
	imageBytes := testPNG(t, color.NRGBA{R: 18, G: 52, B: 86, A: 255})
	var requests atomic.Int32
	var fail atomic.Bool
	fixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if fail.Load() {
			http.Error(response, "upstream failed", http.StatusBadGateway)
			return
		}
		response.Header().Set("Content-Type", "text/plain")
		response.Write(imageBytes)
	}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)
	localURL := service.LocalURL(context.Background(), fixture.URL+"/poster")
	if !strings.HasPrefix(localURL, publicPrefix) {
		t.Fatalf("registration returned %q", localURL)
	}

	first := serveArtwork(service, http.MethodGet, localURL)
	assertArtworkResponse(t, first, imageBytes, true)
	if requests.Load() != 1 {
		t.Fatalf("upstream requests after miss = %d, want 1", requests.Load())
	}

	fail.Store(true)
	second := serveArtwork(service, http.MethodGet, localURL)
	assertArtworkResponse(t, second, imageBytes, true)
	head := serveArtwork(service, http.MethodHead, localURL)
	assertArtworkResponse(t, head, imageBytes, false)
	if requests.Load() != 1 {
		t.Fatalf("cached requests contacted failed upstream %d times", requests.Load())
	}

	fallbackURL := fixture.URL + "/fallback"
	fallbackLocalURL := service.LocalURL(context.Background(), fallbackURL)
	fallback := serveArtwork(service, http.MethodGet, fallbackLocalURL)
	if fallback.Code != http.StatusTemporaryRedirect ||
		fallback.Header().Get("Location") != fallbackURL ||
		fallback.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("fallback response = %d location=%q cache=%q", fallback.Code, fallback.Header().Get("Location"), fallback.Header().Get("Cache-Control"))
	}
}

func TestServeHTTPRepairsMissingAndCorruptFile(t *testing.T) {
	pool := openArtworkTestPool(t)
	imageBytes := testPNG(t, color.NRGBA{R: 220, G: 80, B: 40, A: 255})
	var requests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		response.Write(imageBytes)
	}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)
	localURL := service.LocalURL(context.Background(), fixture.URL+"/repair")
	first := serveArtwork(service, http.MethodGet, localURL)
	assertArtworkResponse(t, first, imageBytes, true)

	key := strings.TrimPrefix(localURL, publicPrefix)
	if err := os.Remove(service.path(key)); err != nil {
		t.Fatalf("remove cached file: %v", err)
	}
	second := serveArtwork(service, http.MethodGet, localURL)
	assertArtworkResponse(t, second, imageBytes, true)
	if requests.Load() != 2 {
		t.Fatalf("upstream requests after missing-file repair = %d, want 2", requests.Load())
	}

	corrupt := make([]byte, len(imageBytes))
	if err := os.WriteFile(service.path(key), corrupt, 0o600); err != nil {
		t.Fatalf("corrupt cached file: %v", err)
	}
	third := serveArtwork(service, http.MethodGet, localURL)
	assertArtworkResponse(t, third, imageBytes, true)
	if requests.Load() != 3 {
		t.Fatalf("upstream requests after corrupt-file repair = %d, want 3", requests.Load())
	}
}

func TestServeHTTPRejectsUnsafeContentAndFallsBackToSource(t *testing.T) {
	pool := openArtworkTestPool(t)
	wideImage := testSizedPNG(t, maxImageDimension+1, 1)
	fixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/wrong":
			response.Header().Set("Content-Type", "image/png")
			response.Write([]byte("this is not an image"))
		case "/oversize":
			response.Header().Set("Content-Type", "image/png")
			response.Write([]byte("\x89PNG\r\n\x1a\n"))
			io.CopyN(response, zeroReader{}, maxObjectBytes)
		case "/dimensions":
			response.Write(wideImage)
		default:
			http.NotFound(response, request)
		}
	}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 2*maxObjectBytes)

	for _, path := range []string{"/wrong", "/oversize", "/dimensions"} {
		localURL := service.LocalURL(context.Background(), fixture.URL+path)
		result := serveArtwork(service, http.MethodGet, localURL)
		if result.Code != http.StatusTemporaryRedirect ||
			result.Header().Get("Location") != fixture.URL+path ||
			result.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s fallback = %d location=%q cache=%q", path, result.Code, result.Header().Get("Location"), result.Header().Get("Cache-Control"))
		}
		key := strings.TrimPrefix(localURL, publicPrefix)
		if _, err := os.Stat(service.path(key)); !os.IsNotExist(err) {
			t.Fatalf("%s left a cache file, stat error = %v", path, err)
		}
	}
}

func TestPruneEnforcesLRUByteCeiling(t *testing.T) {
	pool := openArtworkTestPool(t)
	firstImage := testPNG(t, color.NRGBA{R: 1, G: 2, B: 3, A: 255})
	secondImage := testPNG(t, color.NRGBA{R: 4, G: 5, B: 6, A: 255})
	fixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/first" {
			response.Write(firstImage)
			return
		}
		response.Write(secondImage)
	}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)
	firstURL := service.LocalURL(context.Background(), fixture.URL+"/first")
	secondURL := service.LocalURL(context.Background(), fixture.URL+"/second")
	assertArtworkResponse(t, serveArtwork(service, http.MethodGet, firstURL), firstImage, true)
	assertArtworkResponse(t, serveArtwork(service, http.MethodGet, secondURL), secondImage, true)

	firstKey := strings.TrimPrefix(firstURL, publicPrefix)
	secondKey := strings.TrimPrefix(secondURL, publicPrefix)
	if _, err := pool.Exec(context.Background(), `
		UPDATE artwork_cache
		SET last_accessed_at = CASE key WHEN $1 THEN now() - interval '1 hour' ELSE now() END
		WHERE key IN ($1, $2)
	`, firstKey, secondKey); err != nil {
		t.Fatalf("order artwork LRU: %v", err)
	}
	service.maxBytes = int64(len(secondImage))
	if err := service.Prune(context.Background()); err != nil {
		t.Fatalf("prune artwork: %v", err)
	}

	var firstSize, secondSize *int64
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT byte_size FROM artwork_cache WHERE key = $1),
			(SELECT byte_size FROM artwork_cache WHERE key = $2)
	`, firstKey, secondKey).Scan(&firstSize, &secondSize); err != nil {
		t.Fatalf("query pruned metadata: %v", err)
	}
	if firstSize != nil || secondSize == nil || *secondSize != int64(len(secondImage)) {
		t.Fatalf("unexpected sizes after prune: first=%v second=%v", firstSize, secondSize)
	}
	if _, err := os.Stat(service.path(firstKey)); !os.IsNotExist(err) {
		t.Fatalf("LRU file was not removed: %v", err)
	}
	if _, err := os.Stat(service.path(secondKey)); err != nil {
		t.Fatalf("newest file was removed: %v", err)
	}
}

func TestServeHTTPStableBadKeyAndMissingResponses(t *testing.T) {
	pool := openArtworkTestPool(t)
	service := newArtworkTestService(t, pool, http.DefaultClient, 1<<20)
	bad := serveArtwork(service, http.MethodGet, publicPrefix+"bad")
	if bad.Code != http.StatusBadRequest || bad.Body.String() != "invalid artwork key\n" {
		t.Fatalf("bad-key response = %d %q", bad.Code, bad.Body.String())
	}
	missing := serveArtwork(service, http.MethodGet, publicPrefix+strings.Repeat("a", 64))
	if missing.Code != http.StatusNotFound || missing.Body.String() != "artwork not found\n" {
		t.Fatalf("missing response = %d %q", missing.Code, missing.Body.String())
	}
}

func openArtworkTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run PostgreSQL artwork cache tests")
	}
	configuration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	configuration.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(context.Background(), configuration)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return pool
}

func newArtworkTestService(t *testing.T, pool *pgxpool.Pool, client *http.Client, maxBytes int64) *Service {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service, err := New(pool, Options{Directory: t.TempDir(), MaxBytes: maxBytes, HTTPClient: client, Logger: logger})
	if err != nil {
		t.Fatalf("create artwork service: %v", err)
	}
	return service
}

func serveArtwork(service *Service, method, localURL string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, localURL, nil)
	request.SetPathValue("key", strings.TrimPrefix(localURL, publicPrefix))
	response := httptest.NewRecorder()
	service.ServeHTTP(response, request)
	return response
}

func assertArtworkResponse(t *testing.T, response *httptest.ResponseRecorder, expected []byte, expectBody bool) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("content type = %q", response.Header().Get("Content-Type"))
	}
	if response.Header().Get("Content-Length") != stringInt(len(expected)) {
		t.Fatalf("content length = %q, want %d", response.Header().Get("Content-Length"), len(expected))
	}
	if response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("cache control = %q", response.Header().Get("Cache-Control"))
	}
	if expectBody {
		if !bytes.Equal(response.Body.Bytes(), expected) {
			t.Fatalf("body did not match exact upstream bytes")
		}
	} else if response.Body.Len() != 0 {
		t.Fatalf("HEAD body = %d bytes, want zero", response.Body.Len())
	}
}

func testPNG(t *testing.T, pixel color.NRGBA) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	picture.SetNRGBA(0, 0, pixel)
	return encodePNG(t, picture)
}

func testSizedPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	return encodePNG(t, image.NewNRGBA(image.Rect(0, 0, width, height)))
}

func encodePNG(t *testing.T, picture image.Image) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return encoded.Bytes()
}

func stringInt(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	position := len(buffer)
	for value > 0 {
		position--
		buffer[position] = digits[value%10]
		value /= 10
	}
	return string(buffer[position:])
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 0
	}
	return len(buffer), nil
}
