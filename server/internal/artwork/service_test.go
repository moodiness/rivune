package artwork

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestProductionURLValidationAllowsOnlyConfiguredLANOrigin(t *testing.T) {
	policy, err := newTransportPolicy([]string{"http://192.168.1.48:63113"})
	if err != nil {
		t.Fatalf("create transport policy: %v", err)
	}
	const source = "http://192.168.1.48:63113/poster.jpg?key=private"
	normalized, err := normalizeURLWithPolicy(source, true, policy)
	if err != nil {
		t.Fatalf("configured LAN artwork URL was rejected: %v", err)
	}
	if normalized != source {
		t.Fatalf("LAN artwork URL normalized to %q", normalized)
	}
	if public, publicErr := normalizeURLWithPolicy("https://example.com/poster.jpg", true, policy); publicErr != nil || public != "https://example.com/poster.jpg" {
		t.Fatalf("public HTTPS policy changed: URL=%q error=%v", public, publicErr)
	}
	for _, candidate := range []string{
		"http://192.168.1.49:63113/poster.jpg",
		"http://192.168.1.48:63114/poster.jpg",
		"https://192.168.1.48:63113/poster.jpg",
		"http://user@192.168.1.48:63113/poster.jpg",
		"http://[::ffff:192.168.1.48]:63113/poster.jpg",
		"data:image/png;base64,AAAA",
	} {
		if value, candidateErr := normalizeURLWithPolicy(candidate, true, policy); candidateErr == nil {
			t.Errorf("unconfigured artwork URL was accepted as %q", value)
		}
	}
}

func TestLocalURLHidesAllowedLANArtworkSource(t *testing.T) {
	pool := openArtworkTestPool(t)
	service, err := New(pool, Options{
		Directory: t.TempDir(), MaxBytes: 1 << 20,
		LANArtworkOrigins: []string{"http://192.168.1.48:63113"},
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("create artwork service: %v", err)
	}
	const source = "http://192.168.1.48:63113/poster.jpg?key=private"
	localized := service.LocalURL(context.Background(), source)
	if !strings.HasPrefix(localized, publicPrefix) || strings.Contains(localized, "private") || strings.Contains(localized, "192.168.1.48") {
		t.Fatalf("LAN artwork source was not hidden behind a same-origin reference: %q", localized)
	}
	if rejected := service.LocalURL(context.Background(), "http://192.168.1.49:63113/poster.jpg"); rejected != "" {
		t.Fatalf("unconfigured LAN artwork source was localized as %q", rejected)
	}
	restricted, err := New(pool, Options{
		Directory: t.TempDir(), MaxBytes: 1 << 20,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("create restricted artwork service: %v", err)
	}
	if preserved := restricted.LocalURL(context.Background(), localized); preserved != localized {
		t.Fatalf("registered same-origin LAN artwork = %q, want preserved %q", preserved, localized)
	}
}

func TestLocalURLsPreservesOrderDuplicatesAndFailsClosed(t *testing.T) {
	pool := openArtworkTestPool(t)
	fixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)

	inputs := []string{fixture.URL + "/one", "not a URL", fixture.URL + "/two", fixture.URL + "/one"}
	localized := service.LocalURLs(context.Background(), inputs)
	if len(localized) != len(inputs) {
		t.Fatalf("localized length = %d, want %d", len(localized), len(inputs))
	}
	if localized[1] != "" {
		t.Fatalf("invalid URL was not removed: %q", localized[1])
	}
	if localized[0] != localized[3] || !strings.HasPrefix(localized[0], publicPrefix) || !strings.HasPrefix(localized[2], publicPrefix) {
		t.Fatalf("unexpected localized URLs: %#v", localized)
	}
	if localized[0] == localized[2] {
		t.Fatal("distinct upstream URLs received the same local URL")
	}
	if relocalized := service.LocalURL(context.Background(), localized[0]); relocalized != localized[0] {
		t.Fatalf("same-origin artwork reference = %q, want idempotent %q", relocalized, localized[0])
	}
	missingReference := publicPrefix + strings.Repeat("f", 64)
	if _, err := pool.Exec(context.Background(), `DELETE FROM artwork_cache WHERE key = $1`, strings.TrimPrefix(missingReference, publicPrefix)); err != nil {
		t.Fatalf("clear missing artwork fixture: %v", err)
	}
	if preserved := service.LocalURL(context.Background(), missingReference); preserved != "" {
		t.Fatalf("unregistered same-origin artwork was preserved as %q", preserved)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	failed := service.LocalURLs(canceled, []string{fixture.URL + "/failed-one", fixture.URL + "/failed-two"})
	if failed[0] != "" || failed[1] != "" {
		t.Fatalf("failed registration exposed provider inputs: %#v", failed)
	}
}
func TestRunWarmupCachesLocalizedArtworkBeforeBrowserRequest(t *testing.T) {
	pool := openArtworkTestPool(t)
	imageBytes := testPNG(t, color.NRGBA{R: 38, G: 85, B: 119, A: 255})
	var requests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		response.Header().Set("Content-Type", "image/png")
		_, _ = response.Write(imageBytes)
	}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.RunWarmup(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	localURL := service.LocalURL(context.Background(), fixture.URL+"/warm-poster.png")
	key := strings.TrimPrefix(localURL, publicPrefix)
	waitForArtworkCondition(t, func() bool {
		var byteSize *int64
		if err := pool.QueryRow(context.Background(), `SELECT byte_size FROM artwork_cache WHERE key = $1`, key).Scan(&byteSize); err != nil {
			return false
		}
		return byteSize != nil && *byteSize == int64(len(imageBytes))
	})
	if requests.Load() != 1 {
		t.Fatalf("warmup made %d upstream requests, want 1", requests.Load())
	}

	response := serveArtwork(service, http.MethodGet, localURL)
	assertArtworkResponse(t, response, imageBytes, true)
	if requests.Load() != 1 {
		t.Fatalf("browser request redownloaded warmed artwork: requests=%d", requests.Load())
	}
}

func TestRunWarmupBoundsConcurrentDownloads(t *testing.T) {
	pool := openArtworkTestPool(t)
	imageBytes := testPNG(t, color.NRGBA{R: 85, G: 119, B: 153, A: 255})
	var requests atomic.Int32
	var active atomic.Int32
	var maximum atomic.Int32
	release := make(chan struct{})
	fixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		current := active.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		<-release
		active.Add(-1)
		response.Header().Set("Content-Type", "image/png")
		_, _ = response.Write(imageBytes)
	}))
	t.Cleanup(fixture.Close)
	service := newArtworkTestService(t, pool, fixture.Client(), 16<<20)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.RunWarmup(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		close(release)
		<-done
	})

	upstream := make([]string, warmupConcurrency*2)
	for index := range upstream {
		upstream[index] = fixture.URL + "/bounded-" + stringInt(index) + ".png"
	}
	service.LocalURLs(context.Background(), upstream)
	waitForArtworkCondition(t, func() bool { return requests.Load() >= warmupConcurrency })
	if got := maximum.Load(); got != warmupConcurrency {
		t.Fatalf("maximum concurrent warmups = %d, want %d", got, warmupConcurrency)
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
	if fallback.Code != http.StatusBadGateway ||
		fallback.Header().Get("Location") != "" ||
		fallback.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("failed artwork response = %d location=%q cache=%q", fallback.Code, fallback.Header().Get("Location"), fallback.Header().Get("Cache-Control"))
	}

	fallbackKey := strings.TrimPrefix(fallbackLocalURL, publicPrefix)
	var byteSize *int64
	if err := pool.QueryRow(context.Background(), `SELECT byte_size FROM artwork_cache WHERE key = $1`, fallbackKey).Scan(&byteSize); err != nil {
		t.Fatalf("query failed artwork registration: %v", err)
	}
	if byteSize != nil {
		t.Fatalf("failed artwork was persisted with byte size %d", *byteSize)
	}
	if _, err := os.Stat(service.path(fallbackKey)); !os.IsNotExist(err) {
		t.Fatalf("failed artwork cache file exists or could not be checked: %v", err)
	}

	requestsAfterFailure := requests.Load()
	retry := serveArtwork(service, http.MethodGet, fallbackLocalURL)
	if retry.Code != http.StatusBadGateway ||
		retry.Header().Get("Location") != "" ||
		retry.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("retry response = %d location=%q cache=%q", retry.Code, retry.Header().Get("Location"), retry.Header().Get("Cache-Control"))
	}
	if requests.Load() != requestsAfterFailure+1 {
		t.Fatalf("failed artwork retry made %d new upstream requests, want 1", requests.Load()-requestsAfterFailure)
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

func TestServeHTTPRejectsUnsafeContentWithoutRedirectingToSource(t *testing.T) {
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
		if result.Code != http.StatusBadGateway ||
			result.Header().Get("Location") != "" ||
			result.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s response = %d location=%q cache=%q", path, result.Code, result.Header().Get("Location"), result.Header().Get("Cache-Control"))
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

	var firstExists, secondExists bool
	var secondSize *int64
	if err := pool.QueryRow(context.Background(), `
		SELECT
			EXISTS (SELECT 1 FROM artwork_cache WHERE key = $1),
			EXISTS (SELECT 1 FROM artwork_cache WHERE key = $2),
			(SELECT byte_size FROM artwork_cache WHERE key = $2)
	`, firstKey, secondKey).Scan(&firstExists, &secondExists, &secondSize); err != nil {
		t.Fatalf("query pruned registrations: %v", err)
	}
	if firstExists || !secondExists || secondSize == nil || *secondSize != int64(len(secondImage)) {
		t.Fatalf("unexpected registrations after prune: first_exists=%t second_exists=%t second_size=%v", firstExists, secondExists, secondSize)
	}
	if _, err := os.Stat(service.path(firstKey)); !os.IsNotExist(err) {
		t.Fatalf("LRU file was not removed: %v", err)
	}
	if _, err := os.Stat(service.path(secondKey)); err != nil {
		t.Fatalf("newest file was removed: %v", err)
	}
}

func TestLocalURLsChunksAdmissionAtRegistrationLimit(t *testing.T) {
	pool := openArtworkTestPool(t)
	fixture := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)
	service.registrationLimit = 256
	if _, err := pool.Exec(context.Background(), `DELETE FROM artwork_cache`); err != nil {
		t.Fatalf("clear artwork registrations: %v", err)
	}

	upstream := make([]string, 257)
	for index := range upstream {
		upstream[index] = fixture.URL + "/oversized/" + stringInt(index)
	}
	localized := service.LocalURLs(context.Background(), upstream)
	var localizedCount int
	for _, localURL := range localized {
		if strings.HasPrefix(localURL, publicPrefix) {
			localizedCount++
		}
	}
	var registrationCount int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM artwork_cache`).Scan(&registrationCount); err != nil {
		t.Fatalf("count chunked artwork registrations: %v", err)
	}
	if registrationCount != service.registrationLimit || localizedCount != service.registrationLimit {
		t.Fatalf(
			"chunked admission retained rows=%d localized=%d, want %d",
			registrationCount,
			localizedCount,
			service.registrationLimit,
		)
	}
}

func TestRegistrationBacklogConvergesToGlobalLimit(t *testing.T) {
	pool := openArtworkTestPool(t)
	service := newArtworkTestService(t, pool, http.DefaultClient, 1<<20)
	service.registrationLimit = 128
	if _, err := pool.Exec(context.Background(), `DELETE FROM artwork_cache`); err != nil {
		t.Fatalf("clear artwork registrations: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO artwork_cache (key, source_url)
		SELECT repeat(md5(sequence::text), 2), 'https://example.com/legacy/' || sequence
		FROM generate_series(1, 700) AS sequence
	`); err != nil {
		t.Fatalf("seed legacy artwork registrations: %v", err)
	}

	if err := service.pruneRegistrationBacklog(context.Background()); err != nil {
		t.Fatalf("prune legacy artwork registrations: %v", err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM artwork_cache`).Scan(&count); err != nil {
		t.Fatalf("count legacy artwork registrations: %v", err)
	}
	if count != service.registrationLimit {
		t.Fatalf("legacy artwork registration count = %d, want %d", count, service.registrationLimit)
	}
}

func TestPruneBoundsFailedPendingAndTotalRegistrations(t *testing.T) {
	pool := openArtworkTestPool(t)
	imageBytes := testPNG(t, color.NRGBA{R: 14, G: 82, B: 190, A: 255})
	fixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/active" {
			response.Write(imageBytes)
			return
		}
		http.Error(response, "unavailable", http.StatusBadGateway)
	}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)
	service.registrationLimit = 512
	service.registrationTTL = time.Hour
	if _, err := pool.Exec(context.Background(), `DELETE FROM artwork_cache`); err != nil {
		t.Fatalf("clear artwork registrations: %v", err)
	}

	activeURL := service.LocalURL(context.Background(), fixture.URL+"/active")
	activeKey := strings.TrimPrefix(activeURL, publicPrefix)
	assertArtworkResponse(t, serveArtwork(service, http.MethodGet, activeURL), imageBytes, true)
	recentFailedURL := service.LocalURL(context.Background(), fixture.URL+"/recent-failed")
	recentFailedKey := strings.TrimPrefix(recentFailedURL, publicPrefix)
	if response := serveArtwork(service, http.MethodGet, recentFailedURL); response.Code != http.StatusBadGateway {
		t.Fatalf("recent failed artwork status = %d", response.Code)
	}

	var oldFailedKey string
	for batch := range 3 {
		upstream := make([]string, 256)
		for index := range upstream {
			upstream[index] = fixture.URL + "/failed/" + stringInt(batch*256+index)
		}
		local := service.LocalURLs(context.Background(), upstream)
		for index, localURL := range local {
			if !strings.HasPrefix(localURL, publicPrefix) {
				t.Fatalf("batch %d registration %d was rejected: %q", batch, index, localURL)
			}
			if response := serveArtwork(service, http.MethodGet, localURL); response.Code != http.StatusBadGateway {
				t.Fatalf("batch %d failed artwork %d status = %d", batch, index, response.Code)
			}
		}
		if batch == 2 {
			oldFailedKey = strings.TrimPrefix(local[0], publicPrefix)
		}
		activeURL = service.LocalURL(context.Background(), fixture.URL+"/active")
		recentFailedURL = service.LocalURL(context.Background(), fixture.URL+"/recent-failed")
	}

	oldPendingURL := service.LocalURL(context.Background(), fixture.URL+"/old-pending")
	oldPendingKey := strings.TrimPrefix(oldPendingURL, publicPrefix)
	if _, err := pool.Exec(context.Background(), `
		UPDATE artwork_cache
		SET registered_at = now() - interval '2 hours'
		WHERE key = ANY($1::text[])
	`, []string{oldFailedKey, oldPendingKey, activeKey}); err != nil {
		t.Fatalf("age stale artwork registrations: %v", err)
	}
	if err := service.Prune(context.Background()); err != nil {
		t.Fatalf("prune artwork registrations: %v", err)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM artwork_cache`).Scan(&count); err != nil {
		t.Fatalf("count artwork registrations: %v", err)
	}
	if count > service.registrationLimit {
		t.Fatalf("artwork registration count = %d, limit = %d", count, service.registrationLimit)
	}
	var oldFailedExists, oldPendingExists, activeExists, recentFailedExists bool
	var activeSize, recentFailedSize *int64
	if err := pool.QueryRow(context.Background(), `
		SELECT
			EXISTS (SELECT 1 FROM artwork_cache WHERE key = $1),
			EXISTS (SELECT 1 FROM artwork_cache WHERE key = $2),
			EXISTS (SELECT 1 FROM artwork_cache WHERE key = $3),
			EXISTS (SELECT 1 FROM artwork_cache WHERE key = $4),
			(SELECT byte_size FROM artwork_cache WHERE key = $3),
			(SELECT byte_size FROM artwork_cache WHERE key = $4)
	`, oldFailedKey, oldPendingKey, activeKey, recentFailedKey).Scan(
		&oldFailedExists, &oldPendingExists, &activeExists, &recentFailedExists, &activeSize, &recentFailedSize,
	); err != nil {
		t.Fatalf("query retained artwork registrations: %v", err)
	}
	if oldFailedExists || oldPendingExists {
		t.Fatalf("stale registrations survived: failed=%t pending=%t", oldFailedExists, oldPendingExists)
	}
	if !activeExists || activeSize == nil || *activeSize != int64(len(imageBytes)) {
		t.Fatalf("active artwork was not retained: exists=%t size=%v", activeExists, activeSize)
	}
	if !recentFailedExists || recentFailedSize != nil {
		t.Fatalf("recent failed artwork was not retained as uncached: exists=%t size=%v", recentFailedExists, recentFailedSize)
	}
	if _, err := os.Stat(service.path(activeKey)); err != nil {
		t.Fatalf("active artwork object was removed: %v", err)
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

type artworkRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip artworkRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestServeHTTPRedactsUpstreamURLFromErrorLogAndResponse(t *testing.T) {
	pool := openArtworkTestPool(t)
	secretURL := "https://provider.example/private/poster.png?target=original&token=artwork-secret"
	networkCause := errors.New("connection reset")
	client := &http.Client{Transport: artworkRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, networkCause
	})}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	service, err := New(pool, Options{
		Directory:  t.TempDir(),
		MaxBytes:   1 << 20,
		HTTPClient: client,
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("create artwork service: %v", err)
	}
	localURL := service.LocalURL(context.Background(), secretURL)
	key := strings.TrimPrefix(localURL, publicPrefix)
	record, found, err := service.lookup(context.Background(), key)
	if err != nil || !found {
		t.Fatalf("lookup artwork registration: found=%t err=%v", found, err)
	}

	fetchErr := service.fetch(context.Background(), record)

	if !errors.Is(fetchErr, networkCause) {
		t.Fatalf("sanitized artwork error lost network cause: %v", fetchErr)
	}
	var requestErr *url.Error
	if !errors.As(fetchErr, &requestErr) || requestErr.Op != "Get" || requestErr.URL != "" {
		t.Fatalf("artwork URL error was not sanitized with operation intact: %#v", requestErr)
	}
	if strings.Contains(fetchErr.Error(), secretURL) ||
		strings.Contains(fetchErr.Error(), "/private/") ||
		strings.Contains(fetchErr.Error(), "artwork-secret") {
		t.Fatalf("artwork error exposed upstream request destination: %v", fetchErr)
	}

	response := serveArtwork(service, http.MethodGet, localURL)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("failed artwork status = %d, body=%s", response.Code, response.Body.String())
	}
	combined := logs.String() + response.Body.String()
	for _, secret := range []string{secretURL, "/private/poster.png", "artwork-secret"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("artwork log or response exposed %q: %s", secret, combined)
		}
	}
	if !strings.Contains(logs.String(), "fetch artwork") ||
		!strings.Contains(logs.String(), "download artwork") ||
		!strings.Contains(logs.String(), "connection reset") {
		t.Fatalf("sanitized artwork log lost operation or cause: %s", logs.String())
	}
}

func waitForArtworkCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for artwork condition")
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
