package artwork

import (
	"bytes"
	"container/list"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
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

func TestLookupKeyRequiresExistingMatchingRegistration(t *testing.T) {
	pool := openArtworkTestPool(t)
	fixture := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("LookupKey must not fetch artwork")
	}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)

	source := fixture.URL + "/private/poster.png?token=provider-secret"
	local := service.LocalURL(context.Background(), source)
	wantKey := strings.TrimPrefix(local, publicPrefix)
	for _, materialized := range []string{source, local} {
		if key, ok := service.LookupKey(context.Background(), materialized); !ok || key != wantKey {
			t.Fatalf("LookupKey(%q) = %q, %t; want %q, true", materialized, key, ok, wantKey)
		}
	}
	for _, invalid := range []string{"", publicPrefix + "not-a-key", publicPrefix + "../" + strings.Repeat("a", 64), "https://user:secret@example.com/poster.png"} {
		if key, ok := service.LookupKey(context.Background(), invalid); ok || key != "" {
			t.Fatalf("invalid lookup %q = %q, %t", invalid, key, ok)
		}
	}
	if key, ok := service.LookupKey(context.Background(), fixture.URL+"/unregistered"); ok || key != "" {
		t.Fatalf("unregistered lookup = %q, %t", key, ok)
	}
	if key, ok := service.LookupKey(context.Background(), publicPrefix+strings.Repeat("f", 64)); ok || key != "" {
		t.Fatalf("unknown local lookup = %q, %t", key, ok)
	}
	if key, ok := service.LookupKey(context.Background(), source+"&different=true"); ok || key != "" {
		t.Fatalf("different source lookup = %q, %t", key, ok)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM artwork_cache WHERE key = $1`, wantKey); err != nil {
		t.Fatalf("remove registration: %v", err)
	}
	if key, ok := service.LookupKey(context.Background(), local); ok || key != "" {
		t.Fatalf("stale local lookup = %q, %t", key, ok)
	}
}

func TestLocalURLsRegistersWithoutFetchingUntilServeKey(t *testing.T) {
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

	localized := service.LocalURLs(context.Background(), []string{fixture.URL + "/lazy-poster.png"})
	if len(localized) != 1 || !strings.HasPrefix(localized[0], publicPrefix) {
		t.Fatalf("LocalURLs returned %#v", localized)
	}
	localURL := localized[0]
	key := strings.TrimPrefix(localURL, publicPrefix)
	if requests.Load() != 0 {
		t.Fatalf("LocalURLs fetched artwork %d times", requests.Load())
	}
	var byteSize *int64
	if err := pool.QueryRow(context.Background(), `SELECT byte_size FROM artwork_cache WHERE key = $1`, key).Scan(&byteSize); err != nil {
		t.Fatalf("query lazy artwork registration: %v", err)
	}
	if byteSize != nil {
		t.Fatalf("LocalURLs published %d artwork bytes before ServeKey", *byteSize)
	}
	if _, err := os.Stat(service.path(key)); !os.IsNotExist(err) {
		t.Fatalf("LocalURLs created cache file before ServeKey: %v", err)
	}
	if metadata, found := service.DescribeKey(context.Background(), key); found || metadata != (ImageMetadata{}) || requests.Load() != 0 {
		t.Fatalf("uncached DescribeKey metadata=%+v found=%t upstream=%d", metadata, found, requests.Load())
	}

	response := serveArtwork(service, http.MethodGet, localURL)
	assertArtworkResponse(t, response, imageBytes, true)
	if requests.Load() != 1 {
		t.Fatalf("ServeKey made %d lazy upstream requests, want 1", requests.Load())
	}
	if err := pool.QueryRow(context.Background(), `SELECT byte_size FROM artwork_cache WHERE key = $1`, key).Scan(&byteSize); err != nil {
		t.Fatalf("query lazily published artwork: %v", err)
	}
	if byteSize == nil || *byteSize != int64(len(imageBytes)) {
		t.Fatalf("ServeKey published byte size %v, want %d", byteSize, len(imageBytes))
	}
	metadata, found := service.DescribeKey(context.Background(), key)
	if !found || metadata.Width != 1 || metadata.Height != 1 || metadata.Size != int64(len(imageBytes)) || requests.Load() != 1 {
		t.Fatalf("cached DescribeKey metadata=%+v found=%t upstream=%d", metadata, found, requests.Load())
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
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	service.negativeNow = func() time.Time { return now }
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
	if retry.Code != http.StatusBadGateway || retry.Header().Get("Location") != "" || retry.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("negative-cache response = %d location=%q cache=%q", retry.Code, retry.Header().Get("Location"), retry.Header().Get("Cache-Control"))
	}
	if requests.Load() != requestsAfterFailure {
		t.Fatalf("negative cache repeated failed upstream request: requests=%d want %d", requests.Load(), requestsAfterFailure)
	}
	if key, ok := service.LookupKey(context.Background(), fallbackLocalURL); ok || key != "" {
		t.Fatalf("negative cached registration remained projectable: key=%q ok=%t", key, ok)
	}
	if projected := service.LocalURL(context.Background(), fallbackURL); projected != "" {
		t.Fatalf("negative cached artwork remained localizable as %q", projected)
	}
	now = now.Add(negativeCacheTTL)
	retry = serveArtwork(service, http.MethodGet, fallbackLocalURL)
	if retry.Code != http.StatusBadGateway || requests.Load() != requestsAfterFailure+1 {
		t.Fatalf("expired negative cache status=%d requests=%d want %d", retry.Code, requests.Load(), requestsAfterFailure+1)
	}
}

func TestServeKeyTransformsClientImageQueriesWithStableConditionalHeaders(t *testing.T) {
	pool := openArtworkTestPool(t)
	imageBytes := testSizedPNG(t, 4, 2)
	var requests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		response.Header().Set("Content-Type", "image/png")
		_, _ = response.Write(imageBytes)
	}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)
	localURL := service.LocalURL(context.Background(), fixture.URL+"/transform")
	key := strings.TrimPrefix(localURL, publicPrefix)

	request := httptest.NewRequest(http.MethodGet, localURL+"?fillWidth=2&fillHeight=2&quality=80", nil)
	response := httptest.NewRecorder()
	service.ServeKey(response, request, key)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" || response.Header().Get("ETag") == `"`+key+`"` ||
		response.Header().Get("Content-Length") != strconv.Itoa(response.Body.Len()) || requests.Load() != 1 {
		t.Fatalf("transformed GET status=%d headers=%v bytes=%d upstream=%d", response.Code, response.Header(), response.Body.Len(), requests.Load())
	}
	configuration, format, err := image.DecodeConfig(bytes.NewReader(response.Body.Bytes()))
	if err != nil || format != "png" || configuration.Width != 2 || configuration.Height != 2 {
		t.Fatalf("transformed dimensions=%+v format=%q error=%v", configuration, format, err)
	}
	etag := response.Header().Get("ETag")
	length := response.Header().Get("Content-Length")

	headRequest := httptest.NewRequest(http.MethodHead, localURL+"?fillWidth=2&fillHeight=2&quality=80", nil)
	headResponse := httptest.NewRecorder()
	service.ServeKey(headResponse, headRequest, key)
	if headResponse.Code != http.StatusOK || headResponse.Body.Len() != 0 || headResponse.Header().Get("ETag") != etag || headResponse.Header().Get("Content-Length") != length || requests.Load() != 1 {
		t.Fatalf("transformed HEAD status=%d headers=%v body=%d upstream=%d", headResponse.Code, headResponse.Header(), headResponse.Body.Len(), requests.Load())
	}

	lastAccessed := time.Date(2026, time.August, 8, 10, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(context.Background(), `UPDATE artwork_cache SET last_accessed_at = $2 WHERE key = $1`, key, lastAccessed); err != nil {
		t.Fatalf("set transformed access sentinel: %v", err)
	}
	if err := os.Remove(service.path(key)); err != nil {
		t.Fatalf("remove transformed source cache: %v", err)
	}
	conditionalRequest := httptest.NewRequest(http.MethodGet, localURL+"?fillWidth=2&fillHeight=2&quality=80", nil)
	conditionalRequest.Header.Set("If-None-Match", "W/"+etag)
	conditionalResponse := httptest.NewRecorder()
	service.ServeKey(conditionalResponse, conditionalRequest, key)
	if conditionalResponse.Code != http.StatusNotModified || conditionalResponse.Body.Len() != 0 || conditionalResponse.Header().Get("ETag") != etag || requests.Load() != 1 {
		t.Fatalf("transformed conditional status=%d headers=%v body=%d upstream=%d", conditionalResponse.Code, conditionalResponse.Header(), conditionalResponse.Body.Len(), requests.Load())
	}
	var persistedAccess time.Time
	if err := pool.QueryRow(context.Background(), `SELECT last_accessed_at FROM artwork_cache WHERE key = $1`, key).Scan(&persistedAccess); err != nil || !persistedAccess.Equal(lastAccessed) {
		t.Fatalf("conditional transformed request touched cache: access=%v error=%v", persistedAccess, err)
	}
}

func TestServeKeyBoundsTransformationsAndQueuesBurst(t *testing.T) {
	pool := openArtworkTestPool(t)
	imageBytes := testSizedPNG(t, 4, 2)
	fixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "image/png")
		_, _ = response.Write(imageBytes)
	}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)
	service.transformMaxWaiters = 1
	localURL := service.LocalURL(context.Background(), fixture.URL+"/bounded-transform")
	key := strings.TrimPrefix(localURL, publicPrefix)
	assertArtworkResponse(t, serveArtwork(service, http.MethodGet, localURL), imageBytes, true)

	var active atomic.Int32
	var maximum atomic.Int32
	var calls [6]atomic.Int32
	started := make(chan int, maximumConcurrentImageTransforms)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseTransforms := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseTransforms)
	service.imageTransformer = func(_ *os.File, _ string, transform imageTransform) ([]byte, string, error) {
		calls[transform.width].Add(1)
		current := active.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- transform.width
		<-release
		active.Add(-1)
		return bytes.Repeat([]byte{byte(transform.width)}, transform.width), "image/png", nil
	}
	type served struct {
		method   string
		width    int
		response *httptest.ResponseRecorder
	}
	responses := make(chan served, maximumConcurrentImageTransforms+1)
	serveTransform := func(method string, width int) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, localURL+"?width="+strconv.Itoa(width), nil)
		response := httptest.NewRecorder()
		service.ServeKey(response, request, key)
		return response
	}
	go func() {
		responses <- served{method: http.MethodGet, width: 2, response: serveTransform(http.MethodGet, 2)}
	}()
	go func() {
		responses <- served{method: http.MethodHead, width: 3, response: serveTransform(http.MethodHead, 3)}
	}()
	for range maximumConcurrentImageTransforms {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for bounded HTTP transformations")
		}
	}
	go func() {
		responses <- served{method: http.MethodGet, width: 4, response: serveTransform(http.MethodGet, 4)}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		service.transformMu.Lock()
		waiters := service.transformWaiters
		service.transformMu.Unlock()
		if waiters == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("HTTP transformation did not enter the bounded queue")
		}
		time.Sleep(time.Millisecond)
	}
	overloaded := serveTransform(http.MethodGet, 5)
	if overloaded.Code != http.StatusServiceUnavailable || overloaded.Header().Get("Cache-Control") != "no-store" || overloaded.Header().Get("Retry-After") != "1" {
		t.Fatalf("overloaded transform status=%d headers=%v body=%q", overloaded.Code, overloaded.Header(), overloaded.Body.String())
	}
	releaseTransforms()
	for range maximumConcurrentImageTransforms + 1 {
		select {
		case result := <-responses:
			wantLength := strconv.Itoa(result.width)
			if result.response.Code != http.StatusOK || result.response.Header().Get("Content-Length") != wantLength {
				t.Fatalf("%s width=%d status=%d headers=%v", result.method, result.width, result.response.Code, result.response.Header())
			}
			if result.method == http.MethodHead && result.response.Body.Len() != 0 {
				t.Fatalf("transformed HEAD wrote %d body bytes", result.response.Body.Len())
			}
			if result.method == http.MethodGet && result.response.Body.Len() != result.width {
				t.Fatalf("transformed GET wrote %d bytes, want %d", result.response.Body.Len(), result.width)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for queued HTTP transformations")
		}
	}
	if maximum.Load() != maximumConcurrentImageTransforms || calls[2].Load() != 1 || calls[3].Load() != 1 || calls[4].Load() != 1 || calls[5].Load() != 0 {
		t.Fatalf("transform maximum=%d calls=[width2:%d width3:%d width4:%d width5:%d]", maximum.Load(), calls[2].Load(), calls[3].Load(), calls[4].Load(), calls[5].Load())
	}
	service.transformMu.Lock()
	remainingFlights, remainingWaiters := len(service.transformFlights), service.transformWaiters
	service.transformMu.Unlock()
	if remainingFlights != 0 || remainingWaiters != 0 || len(service.transformSlots) != 0 {
		t.Fatalf("HTTP transform cleanup flights=%d waiters=%d slots=%d", remainingFlights, remainingWaiters, len(service.transformSlots))
	}
}

func TestTransformCoalescedBoundsSharesQueuesCancelsAndCleansUp(t *testing.T) {
	service := &Service{
		transformFlights:    make(map[string]*imageTransformFlight),
		transformSlots:      make(chan struct{}, maximumConcurrentImageTransforms),
		transformMaxWaiters: 2,
	}
	var active atomic.Int32
	var maximum atomic.Int32
	var calls [6]atomic.Int32
	started := make(chan int, maximumConcurrentImageTransforms)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseTransforms := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseTransforms)
	service.imageTransformer = func(_ *os.File, _ string, transform imageTransform) ([]byte, string, error) {
		calls[transform.width].Add(1)
		current := active.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- transform.width
		<-release
		active.Add(-1)
		return bytes.Repeat([]byte{byte(transform.width)}, transform.width), "image/png", nil
	}

	type outcome struct {
		content     []byte
		contentType string
		err         error
	}
	results := make(chan outcome, 5)
	run := func(ctx context.Context, etag string, width int) {
		content, contentType, err := service.transformCoalesced(ctx, etag, nil, "image/png", imageTransform{width: width, requested: true})
		results <- outcome{content: content, contentType: contentType, err: err}
	}
	go run(context.Background(), `"first"`, 1)
	go run(context.Background(), `"second"`, 2)
	for range maximumConcurrentImageTransforms {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for admitted transformations")
		}
	}
	waitForWaiters := func(want int) {
		deadline := time.Now().Add(time.Second)
		for {
			service.transformMu.Lock()
			got := service.transformWaiters
			service.transformMu.Unlock()
			if got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("transform waiters=%d want=%d", got, want)
			}
			time.Sleep(time.Millisecond)
		}
	}
	queuedOwnerContext, cancelQueuedOwner := context.WithCancel(context.Background())
	queuedOwnerResult := make(chan error, 1)
	go func() {
		_, _, err := service.transformCoalesced(queuedOwnerContext, `"third"`, nil, "image/png", imageTransform{width: 3, requested: true})
		queuedOwnerResult <- err
	}()
	waitForWaiters(1)
	sharedContext := &observedDoneContext{Context: context.Background(), entered: make(chan struct{})}
	go run(sharedContext, `"third"`, 3)
	select {
	case <-sharedContext.entered:
	case <-time.After(time.Second):
		t.Fatal("same-key transformation did not join the queued flight")
	}
	cancelQueuedOwner()
	select {
	case err := <-queuedOwnerResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled queued owner error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled queued owner did not return")
	}
	waitForWaiters(1)

	canceledContext, cancel := context.WithCancel(context.Background())
	canceledResult := make(chan error, 1)
	go func() {
		_, _, err := service.transformCoalesced(canceledContext, `"canceled"`, nil, "image/png", imageTransform{width: 4, requested: true})
		canceledResult <- err
	}()
	waitForWaiters(2)
	cancel()
	select {
	case err := <-canceledResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled queued transformation error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled queued transformation did not return")
	}
	waitForWaiters(1)
	go run(context.Background(), `"fourth"`, 4)
	waitForWaiters(2)
	if _, _, err := service.transformCoalesced(context.Background(), `"overload"`, nil, "image/png", imageTransform{width: 5, requested: true}); !errors.Is(err, errImageTransformSaturated) {
		t.Fatalf("beyond-cap transformation error=%v", err)
	}

	releaseTransforms()
	for range 4 {
		select {
		case result := <-results:
			if result.err != nil || result.contentType != "image/png" || len(result.content) == 0 {
				t.Fatalf("transformation result=%v type=%q error=%v", result.content, result.contentType, result.err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for queued transformations")
		}
	}
	if maximum.Load() != maximumConcurrentImageTransforms || calls[1].Load() != 1 || calls[2].Load() != 1 || calls[3].Load() != 1 || calls[4].Load() != 1 || calls[5].Load() != 0 {
		t.Fatalf("transform maximum=%d calls=%d,%d,%d,%d,%d", maximum.Load(), calls[1].Load(), calls[2].Load(), calls[3].Load(), calls[4].Load(), calls[5].Load())
	}
	service.transformMu.Lock()
	remainingFlights, remainingWaiters := len(service.transformFlights), service.transformWaiters
	service.transformMu.Unlock()
	if remainingFlights != 0 || remainingWaiters != 0 || len(service.transformSlots) != 0 || active.Load() != 0 {
		t.Fatalf("transform cleanup flights=%d waiters=%d slots=%d active=%d", remainingFlights, remainingWaiters, len(service.transformSlots), active.Load())
	}
}

func TestServeKeyCachesSequentialImageTransformations(t *testing.T) {
	pool := openArtworkTestPool(t)
	imageBytes := testSizedPNG(t, 4, 2)
	fixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "image/png")
		_, _ = response.Write(imageBytes)
	}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)
	localURL := service.LocalURL(context.Background(), fixture.URL+"/sequential-transform")
	key := strings.TrimPrefix(localURL, publicPrefix)
	assertArtworkResponse(t, serveArtwork(service, http.MethodGet, localURL), imageBytes, true)
	service.transformMu.Lock()
	untransformedEntries := len(service.transformCache)
	service.transformMu.Unlock()
	if untransformedEntries != 0 {
		t.Fatalf("untransformed artwork populated %d transformed cache entries", untransformedEntries)
	}

	var calls atomic.Int32
	service.imageTransformer = func(_ *os.File, _ string, transform imageTransform) ([]byte, string, error) {
		calls.Add(1)
		return []byte{byte(transform.width), byte(transform.quality), 0x5a}, "image/png", nil
	}
	serve := func(rawQuery string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, localURL+"?"+rawQuery, nil)
		response := httptest.NewRecorder()
		service.ServeKey(response, request, key)
		return response
	}
	first := serve("width=2&quality=80")
	second := serve("width=2&quality=80")
	if calls.Load() != 1 {
		t.Fatalf("two identical sequential requests transformed %d times, want 1", calls.Load())
	}
	for _, header := range []string{"Cache-Control", "Content-Length", "Content-Type", "ETag"} {
		if first.Header().Get(header) != second.Header().Get(header) {
			t.Fatalf("sequential %s headers differ: %q != %q", header, first.Header().Get(header), second.Header().Get(header))
		}
	}
	if first.Code != http.StatusOK || second.Code != http.StatusOK || !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Fatalf("sequential responses first=(%d,%v) second=(%d,%v)", first.Code, first.Body.Bytes(), second.Code, second.Body.Bytes())
	}

	different := serve("width=3&quality=80")
	if calls.Load() != 2 {
		t.Fatalf("different parameters produced %d transformations, want 2", calls.Load())
	}
	if different.Header().Get("ETag") == first.Header().Get("ETag") || bytes.Equal(different.Body.Bytes(), first.Body.Bytes()) {
		t.Fatalf("different parameters reused result: first ETag=%q body=%v different ETag=%q body=%v", first.Header().Get("ETag"), first.Body.Bytes(), different.Header().Get("ETag"), different.Body.Bytes())
	}
}

func TestTransformCacheEvictsByEntryAndByteLimits(t *testing.T) {
	newService := func(maxBytes int64, maxEntries int, calls *[8]atomic.Int32) *Service {
		service := &Service{
			transformFlights:         make(map[string]*imageTransformFlight),
			transformSlots:           make(chan struct{}, maximumConcurrentImageTransforms),
			transformCache:           make(map[string]*list.Element),
			transformCacheMaxBytes:   maxBytes,
			transformCacheMaxEntries: maxEntries,
		}
		service.imageTransformer = func(_ *os.File, _ string, transform imageTransform) ([]byte, string, error) {
			calls[transform.width].Add(1)
			return bytes.Repeat([]byte{byte(transform.width)}, transform.width), "image/png", nil
		}
		return service
	}
	transform := func(t *testing.T, service *Service, etag string, width int) []byte {
		t.Helper()
		content, contentType, err := service.transformCoalesced(context.Background(), etag, nil, "image/png", imageTransform{width: width, requested: true})
		if err != nil || contentType != "image/png" {
			t.Fatalf("transform %s returned type=%q error=%v", etag, contentType, err)
		}
		return content
	}

	t.Run("entries use least recently used order", func(t *testing.T) {
		var calls [8]atomic.Int32
		service := newService(100, 2, &calls)
		transform(t, service, `"a"`, 3)
		transform(t, service, `"b"`, 4)
		transform(t, service, `"a"`, 3)
		transform(t, service, `"c"`, 5)
		transform(t, service, `"b"`, 4)
		if calls[3].Load() != 1 || calls[4].Load() != 2 || calls[5].Load() != 1 {
			t.Fatalf("entry eviction calls a=%d b=%d c=%d", calls[3].Load(), calls[4].Load(), calls[5].Load())
		}
		service.transformMu.Lock()
		entries, cacheBytes := len(service.transformCache), service.transformCacheBytes
		service.transformMu.Unlock()
		if entries != 2 || cacheBytes != 9 {
			t.Fatalf("entry-bounded cache entries=%d bytes=%d, want 2 and 9", entries, cacheBytes)
		}
	})

	t.Run("bytes evict and oversized results are not admitted", func(t *testing.T) {
		var calls [8]atomic.Int32
		service := newService(5, 3, &calls)
		transform(t, service, `"a"`, 3)
		transform(t, service, `"b"`, 3)
		transform(t, service, `"a"`, 3)
		transform(t, service, `"oversized"`, 6)
		transform(t, service, `"oversized"`, 6)
		if calls[3].Load() != 3 || calls[6].Load() != 2 {
			t.Fatalf("byte eviction calls cached-size=%d oversized=%d", calls[3].Load(), calls[6].Load())
		}
		service.transformMu.Lock()
		entries, cacheBytes := len(service.transformCache), service.transformCacheBytes
		service.transformMu.Unlock()
		if entries != 1 || cacheBytes != 3 {
			t.Fatalf("byte-bounded cache entries=%d bytes=%d, want 1 and 3", entries, cacheBytes)
		}
	})
}

func TestTransformCacheRejectsErrorsSaturationAndCancellation(t *testing.T) {
	t.Run("error", func(t *testing.T) {
		service := newTransformCacheTestService(100, 4)
		var calls atomic.Int32
		service.imageTransformer = func(_ *os.File, _ string, _ imageTransform) ([]byte, string, error) {
			if calls.Add(1) == 1 {
				return nil, "", errors.New("decode failed")
			}
			return []byte("success"), "image/png", nil
		}
		if _, _, err := service.transformCoalesced(context.Background(), `"error"`, nil, "image/png", imageTransform{width: 1, requested: true}); err == nil {
			t.Fatal("failed transformation returned no error")
		}
		for range 2 {
			if _, _, err := service.transformCoalesced(context.Background(), `"error"`, nil, "image/png", imageTransform{width: 1, requested: true}); err != nil {
				t.Fatalf("successful transformation: %v", err)
			}
		}
		if calls.Load() != 2 {
			t.Fatalf("failed result was cached: transformer calls=%d, want 2", calls.Load())
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		service := newTransformCacheTestService(100, 4)
		var calls atomic.Int32
		started := make(chan struct{})
		release := make(chan struct{})
		service.imageTransformer = func(_ *os.File, _ string, _ imageTransform) ([]byte, string, error) {
			if calls.Add(1) == 1 {
				close(started)
				<-release
			}
			return []byte("success"), "image/png", nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, _, err := service.transformCoalesced(ctx, `"canceled"`, nil, "image/png", imageTransform{width: 1, requested: true})
			result <- err
		}()
		<-started
		cancel()
		close(release)
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled transformation error=%v", err)
		}
		for range 2 {
			if _, _, err := service.transformCoalesced(context.Background(), `"canceled"`, nil, "image/png", imageTransform{width: 1, requested: true}); err != nil {
				t.Fatalf("retry canceled transformation: %v", err)
			}
		}
		if calls.Load() != 2 {
			t.Fatalf("canceled result was cached: transformer calls=%d, want 2", calls.Load())
		}
	})

	t.Run("saturation", func(t *testing.T) {
		service := newTransformCacheTestService(100, 4)
		service.transformSlots = make(chan struct{}, 1)
		var targetCalls atomic.Int32
		started := make(chan struct{})
		release := make(chan struct{})
		service.imageTransformer = func(_ *os.File, _ string, transform imageTransform) ([]byte, string, error) {
			if transform.width == 1 {
				close(started)
				<-release
			} else {
				targetCalls.Add(1)
			}
			return []byte{byte(transform.width)}, "image/png", nil
		}
		blocker := make(chan error, 1)
		go func() {
			_, _, err := service.transformCoalesced(context.Background(), `"blocker"`, nil, "image/png", imageTransform{width: 1, requested: true})
			blocker <- err
		}()
		<-started
		if _, _, err := service.transformCoalesced(context.Background(), `"target"`, nil, "image/png", imageTransform{width: 2, requested: true}); !errors.Is(err, errImageTransformSaturated) {
			t.Fatalf("saturated transformation error=%v", err)
		}
		close(release)
		if err := <-blocker; err != nil {
			t.Fatalf("blocking transformation: %v", err)
		}
		for range 2 {
			if _, _, err := service.transformCoalesced(context.Background(), `"target"`, nil, "image/png", imageTransform{width: 2, requested: true}); err != nil {
				t.Fatalf("retry saturated transformation: %v", err)
			}
		}
		if targetCalls.Load() != 1 {
			t.Fatalf("saturated target transformer calls=%d, want 1 after retry", targetCalls.Load())
		}
	})
}

func TestTransformCacheConcurrentAccessSharesImmutablePayload(t *testing.T) {
	service := newTransformCacheTestService(1<<20, 64)
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	payload := []byte("shared immutable transformed payload")
	service.imageTransformer = func(_ *os.File, _ string, _ imageTransform) ([]byte, string, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return payload, "image/png", nil
	}
	const requestCount = 32
	results := make(chan []byte, requestCount)
	start := make(chan struct{})
	for range requestCount {
		go func() {
			<-start
			content, _, err := service.transformCoalesced(context.Background(), `"concurrent"`, nil, "image/png", imageTransform{width: 1, requested: true})
			if err != nil {
				results <- nil
				return
			}
			results <- content
		}()
	}
	close(start)
	<-started
	close(release)
	for range requestCount {
		content := <-results
		if !bytes.Equal(content, payload) || &content[0] != &payload[0] {
			t.Fatalf("concurrent cache returned copied or changed payload %q", content)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent identical requests transformed %d times, want 1", calls.Load())
	}
}

func newTransformCacheTestService(maxBytes int64, maxEntries int) *Service {
	return &Service{
		transformFlights:         make(map[string]*imageTransformFlight),
		transformSlots:           make(chan struct{}, maximumConcurrentImageTransforms),
		transformCache:           make(map[string]*list.Element),
		transformCacheMaxBytes:   maxBytes,
		transformCacheMaxEntries: maxEntries,
	}
}

func TestTransformCoalescedCleansUpAfterError(t *testing.T) {
	service := &Service{
		transformFlights: make(map[string]*imageTransformFlight),
		transformSlots:   make(chan struct{}, maximumConcurrentImageTransforms),
	}
	transformErr := errors.New("transform failed")
	service.imageTransformer = func(*os.File, string, imageTransform) ([]byte, string, error) {
		return nil, "", transformErr
	}
	transform := imageTransform{width: 1, requested: true}
	if _, _, err := service.transformCoalesced(context.Background(), `"failed"`, nil, "image/png", transform); !errors.Is(err, transformErr) {
		t.Fatalf("transformation error = %v, want %v", err, transformErr)
	}
	service.transformMu.Lock()
	remainingFlights := len(service.transformFlights)
	service.transformMu.Unlock()
	if remainingFlights != 0 || len(service.transformSlots) != 0 {
		t.Fatalf("failed transform cleanup flights=%d slots=%d", remainingFlights, len(service.transformSlots))
	}

	service.imageTransformer = func(*os.File, string, imageTransform) ([]byte, string, error) {
		return []byte("retry"), "image/png", nil
	}
	content, _, err := service.transformCoalesced(context.Background(), `"failed"`, nil, "image/png", transform)
	if err != nil || string(content) != "retry" {
		t.Fatalf("retry after failed flight content=%q error=%v", content, err)
	}
}

func TestServeKeyRejectsUnsupportedCachedTransformWithoutRefetch(t *testing.T) {
	pool := openArtworkTestPool(t)
	var requests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)
	localURL := service.LocalURL(context.Background(), fixture.URL+"/cached.webp")
	key := strings.TrimPrefix(localURL, publicPrefix)
	if _, err := pool.Exec(context.Background(), `
		UPDATE artwork_cache
		SET content_type = 'image/webp', byte_size = 30, cached_at = now(), last_accessed_at = now()
		WHERE key = $1
	`, key); err != nil {
		t.Fatalf("prepare unsupported cached artwork: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, localURL+"?quality=80", nil)
	response := httptest.NewRecorder()
	service.ServeKey(response, request, key)
	if response.Code != http.StatusBadRequest || response.Header().Get("Cache-Control") != "no-store" || requests.Load() != 0 {
		t.Fatalf("unsupported transform status=%d headers=%v upstream=%d", response.Code, response.Header(), requests.Load())
	}
}

func TestImageTransformParserAndNegativeCacheAreBounded(t *testing.T) {
	for _, values := range []url.Values{
		{"width": {"0"}},
		{"width": {"0400"}},
		{"quality": {"101"}},
		{"width": {"400"}, "maxWidth": {"400"}},
		{"fillWidth": {"10000"}, "fillHeight": {"10000"}},
		{"maxWidth": {"400", "401"}},
	} {
		if _, err := parseImageTransform(values); err == nil {
			t.Fatalf("invalid transform accepted: %#v", values)
		}
	}
	transform, err := parseImageTransform(url.Values{"maxWidth": {"600"}, "maxHeight": {"600"}, "quality": {"90"}})
	if err != nil || !transform.requested || transform.maxWidth != 600 || transform.maxHeight != 600 || transform.quality != 90 {
		t.Fatalf("valid transform=%+v error=%v", transform, err)
	}
	for _, test := range []struct {
		transform imageTransform
		width     int
		height    int
		cover     bool
	}{
		{transform: imageTransform{width: 400}, width: 400, height: 200},
		{transform: imageTransform{maxWidth: 600, maxHeight: 600}, width: 600, height: 300},
		{transform: imageTransform{fillHeight: 400}, width: 800, height: 400},
		{transform: imageTransform{fillWidth: 280, fillHeight: 280}, width: 280, height: 280, cover: true},
	} {
		width, height, cover := transformedDimensions(800, 400, test.transform)
		if width != test.width || height != test.height || cover != test.cover {
			t.Fatalf("transform %+v dimensions=%dx%d cover=%t", test.transform, width, height, cover)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if cacheableArtworkFailure(canceled, errors.New("transport failure")) || cacheableArtworkFailure(context.Background(), context.Canceled) ||
		cacheableArtworkFailure(context.Background(), context.DeadlineExceeded) || !cacheableArtworkFailure(context.Background(), errors.New("upstream failure")) {
		t.Fatal("negative-cache cancellation classification is incorrect")
	}

	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	service := &Service{negative: make(map[string]time.Time), negativeNow: func() time.Time { return now }}
	for index := 0; index <= maximumNegativeCacheEntries; index++ {
		service.markNegative(fmt.Sprintf("%064x", index+1))
	}
	if len(service.negative) != maximumNegativeCacheEntries {
		t.Fatalf("negative cache size=%d want %d", len(service.negative), maximumNegativeCacheEntries)
	}
	newest := fmt.Sprintf("%064x", maximumNegativeCacheEntries+1)
	if !service.negativeCached(newest) {
		t.Fatal("negative cache evicted newest entry")
	}
	now = now.Add(negativeCacheTTL)
	service.markNegative(strings.Repeat("f", 64))
	if len(service.negative) != 1 {
		t.Fatalf("expired negative cache entries retained: %d", len(service.negative))
	}
}

func TestFetchAdmissionCoalescesBeforeBoundedGlobalQueue(t *testing.T) {
	service := &Service{
		flights: make(map[string]*flight),
		fetchAdmission: fetchAdmission{
			maxInFlight:       maximumConcurrentArtworkFetches,
			maxTemporaryFiles: maximumReservedArtworkTemporaryFiles,
			maxTemporaryBytes: maximumReservedArtworkTemporaryBytes,
			maxWaiters:        1,
		},
		negative:    make(map[string]time.Time),
		negativeNow: time.Now,
	}
	for range maximumConcurrentArtworkFetches {
		if !service.fetchAdmission.tryReserve() {
			t.Fatal("fill fetch admission")
		}
	}
	sharedErr := errors.New("shared fetch failed")
	shared := &flight{done: make(chan struct{}), err: sharedErr}
	close(shared.done)
	const sharedKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	service.flights[sharedKey] = shared
	if err := service.fetchCoalesced(context.Background(), cacheRecord{key: sharedKey}); !errors.Is(err, sharedErr) {
		t.Fatalf("same-key saturated join error=%v want=%v", err, sharedErr)
	}

	const queuedKey = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	queuedContext, cancelQueued := context.WithCancel(context.Background())
	queuedResult := make(chan error, 1)
	go func() { queuedResult <- service.fetchCoalesced(queuedContext, cacheRecord{key: queuedKey}) }()
	deadline := time.Now().Add(time.Second)
	for service.fetchAdmission.waiting() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("distinct fetch did not enter bounded queue")
		}
		time.Sleep(time.Millisecond)
	}
	const overloadKey = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if err := service.fetchCoalesced(context.Background(), cacheRecord{key: overloadKey}); !errors.Is(err, errArtworkFetchSaturated) {
		t.Fatalf("beyond-cap fetch error=%v", err)
	}
	if service.flights[overloadKey] != nil {
		t.Fatal("beyond-cap fetch leaked a flight")
	}
	cancelQueued()
	select {
	case err := <-queuedResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled queued fetch error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled queued fetch did not return")
	}
	inFlight, temporaryFiles, temporaryBytes := service.fetchAdmission.usage()
	if inFlight != maximumConcurrentArtworkFetches || temporaryFiles != maximumReservedArtworkTemporaryFiles ||
		temporaryBytes != maximumReservedArtworkTemporaryBytes || service.fetchAdmission.waiting() != 0 || service.flights[queuedKey] != nil {
		t.Fatalf("queued cancellation usage=(%d,%d,%d) waiters=%d flight-created=%t", inFlight, temporaryFiles, temporaryBytes, service.fetchAdmission.waiting(), service.flights[queuedKey] != nil)
	}

	service.fetchAdmission.release()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.fetchCoalesced(canceled, cacheRecord{key: queuedKey}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled fetch error=%v", err)
	}
	inFlight, temporaryFiles, temporaryBytes = service.fetchAdmission.usage()
	if inFlight != maximumConcurrentArtworkFetches-1 || temporaryFiles != maximumReservedArtworkTemporaryFiles-1 ||
		temporaryBytes != maximumReservedArtworkTemporaryBytes-maxObjectBytes || service.fetchAdmission.waiting() != 0 || service.flights[queuedKey] != nil {
		t.Fatalf("pre-canceled admission usage=(%d,%d,%d) waiters=%d flight-created=%t", inFlight, temporaryFiles, temporaryBytes, service.fetchAdmission.waiting(), service.flights[queuedKey] != nil)
	}
}

func TestFetchAdmissionEnforcesEveryConfiguredCeilingAndRecovers(t *testing.T) {
	tests := []struct {
		name      string
		admission *fetchAdmission
		wantFiles int
		wantBytes int64
	}{
		{
			name: "in flight",
			admission: &fetchAdmission{
				maxInFlight:       1,
				maxTemporaryFiles: 2,
				maxTemporaryBytes: 2 * maxObjectBytes,
			},
			wantFiles: 1,
			wantBytes: maxObjectBytes,
		},
		{
			name: "temporary files",
			admission: &fetchAdmission{
				maxInFlight:       2,
				maxTemporaryFiles: 1,
				maxTemporaryBytes: 2 * maxObjectBytes,
			},
			wantFiles: 1,
			wantBytes: maxObjectBytes,
		},
		{
			name: "temporary bytes",
			admission: &fetchAdmission{
				maxInFlight:       2,
				maxTemporaryFiles: 2,
				maxTemporaryBytes: maxObjectBytes,
			},
			wantFiles: 1,
			wantBytes: maxObjectBytes,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !test.admission.tryReserve() {
				t.Fatal("first fetch was not admitted")
			}
			if test.admission.tryReserve() {
				t.Fatal("fetch exceeding configured ceiling was admitted")
			}
			inFlight, temporaryFiles, temporaryBytes := test.admission.usage()
			if inFlight != 1 || temporaryFiles != test.wantFiles || temporaryBytes != test.wantBytes {
				t.Fatalf("saturated usage=(%d,%d,%d), want (1,%d,%d)", inFlight, temporaryFiles, temporaryBytes, test.wantFiles, test.wantBytes)
			}
			test.admission.release()
			if !test.admission.tryReserve() {
				t.Fatal("released capacity was not reusable")
			}
			test.admission.release()
			inFlight, temporaryFiles, temporaryBytes = test.admission.usage()
			if inFlight != 0 || temporaryFiles != 0 || temporaryBytes != 0 {
				t.Fatalf("released usage=(%d,%d,%d)", inFlight, temporaryFiles, temporaryBytes)
			}
		})
	}
}

func TestFetchAdmissionBoundsQueuesAndAlwaysReleases(t *testing.T) {
	pool := openArtworkTestPool(t)
	imageBytes := testPNG(t, color.NRGBA{R: 45, G: 92, B: 138, A: 255})
	started := make(chan string, maximumConcurrentArtworkFetches)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseFetches := func() { releaseOnce.Do(func() { close(release) }) }
	cancelStarted := make(chan struct{}, 1)
	var upstreamRequests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		upstreamRequests.Add(1)
		response.Header().Set("Content-Type", "image/png")
		switch request.URL.Path {
		case "/invalid":
			_, _ = response.Write([]byte("not an image"))
			return
		case "/cancel":
			response.WriteHeader(http.StatusOK)
			response.(http.Flusher).Flush()
			cancelStarted <- struct{}{}
			<-request.Context().Done()
			return
		}
		response.WriteHeader(http.StatusOK)
		response.(http.Flusher).Flush()
		started <- request.URL.Path
		<-release
		_, _ = response.Write(imageBytes)
	}))
	defer func() {
		releaseFetches()
		fixture.Close()
	}()
	service := newArtworkTestService(t, pool, fixture.Client(), int64(maximumConcurrentArtworkFetches)*(maxObjectBytes+1))
	service.fetchAdmission.maxWaiters = 4

	localURLs := make([]string, maximumConcurrentArtworkFetches+6)
	for index := range localURLs {
		localURLs[index] = service.LocalURL(context.Background(), fmt.Sprintf("%s/distinct-%d", fixture.URL, index))
		if localURLs[index] == "" {
			t.Fatalf("register distinct artwork %d", index)
		}
	}
	responses := make(chan *httptest.ResponseRecorder, maximumConcurrentArtworkFetches+4)
	for index := range maximumConcurrentArtworkFetches {
		localURL := localURLs[index]
		go func() { responses <- serveArtwork(service, http.MethodGet, localURL) }()
	}
	for range maximumConcurrentArtworkFetches {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("admitted artwork fetch did not reach upstream")
		}
	}

	countTemporary := func() int {
		entries, err := os.ReadDir(service.directory)
		if err != nil {
			t.Fatalf("read artwork directory: %v", err)
		}
		count := 0
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".artwork-") {
				count++
			}
		}
		return count
	}
	deadline := time.Now().Add(time.Second)
	for countTemporary() != maximumConcurrentArtworkFetches && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if temporary := countTemporary(); temporary != maximumConcurrentArtworkFetches {
		t.Fatalf("live temporary files=%d want=%d", temporary, maximumConcurrentArtworkFetches)
	}

	waitForFetchWaiters := func(want int) {
		deadline := time.Now().Add(time.Second)
		for {
			if got := service.fetchAdmission.waiting(); got == want {
				return
			} else if time.Now().After(deadline) {
				t.Fatalf("fetch waiters=%d want=%d", got, want)
			}
			time.Sleep(time.Millisecond)
		}
	}
	queuedOwnerContext, cancelQueuedOwner := context.WithCancel(context.Background())
	queuedOwnerRequest := httptest.NewRequest(http.MethodGet, localURLs[4], nil).WithContext(queuedOwnerContext)
	queuedOwnerRequest.SetPathValue("key", strings.TrimPrefix(localURLs[4], publicPrefix))
	queuedOwnerDone := make(chan struct{})
	go func() {
		service.ServeHTTP(httptest.NewRecorder(), queuedOwnerRequest)
		close(queuedOwnerDone)
	}()
	for _, index := range []int{5, 6} {
		localURL := localURLs[index]
		go func() { responses <- serveArtwork(service, http.MethodGet, localURL) }()
	}
	waitForFetchWaiters(3)
	queuedKey := strings.TrimPrefix(localURLs[4], publicPrefix)
	queuedRecord, found, err := service.lookup(context.Background(), queuedKey)
	if err != nil || !found {
		t.Fatalf("lookup queued record found=%t error=%v", found, err)
	}
	sharedContext := &observedDoneContext{Context: context.Background(), entered: make(chan struct{})}
	sharedResult := make(chan error, 1)
	go func() { sharedResult <- service.fetchCoalesced(sharedContext, queuedRecord) }()
	select {
	case <-sharedContext.entered:
	case <-time.After(time.Second):
		t.Fatal("same-key request did not join the queued flight")
	}
	if got := service.fetchAdmission.waiting(); got != 3 {
		t.Fatalf("same-key queued join changed waiters=%d", got)
	}
	cancelQueuedOwner()
	select {
	case <-queuedOwnerDone:
	case <-time.After(time.Second):
		t.Fatal("canceled queued fetch owner did not return")
	}
	waitForFetchWaiters(3)

	canceledContext, cancelQueued := context.WithCancel(context.Background())
	canceledRequest := httptest.NewRequest(http.MethodGet, localURLs[7], nil).WithContext(canceledContext)
	canceledRequest.SetPathValue("key", strings.TrimPrefix(localURLs[7], publicPrefix))
	canceledResponse := httptest.NewRecorder()
	canceledDone := make(chan struct{})
	go func() {
		service.ServeHTTP(canceledResponse, canceledRequest)
		close(canceledDone)
	}()
	waitForFetchWaiters(4)
	cancelQueued()
	select {
	case <-canceledDone:
	case <-time.After(time.Second):
		t.Fatal("canceled queued fetch did not return")
	}
	waitForFetchWaiters(3)
	canceledKey := strings.TrimPrefix(localURLs[7], publicPrefix)
	service.flightsMu.Lock()
	canceledFlight := service.flights[canceledKey]
	service.flightsMu.Unlock()
	if canceledFlight != nil {
		t.Fatal("canceled queued fetch leaked its flight")
	}

	go func() { responses <- serveArtwork(service, http.MethodGet, localURLs[8]) }()
	waitForFetchWaiters(4)
	overloadResult := make(chan *httptest.ResponseRecorder, 1)
	go func() { overloadResult <- serveArtwork(service, http.MethodHead, localURLs[9]) }()
	select {
	case response := <-overloadResult:
		if response.Code != http.StatusServiceUnavailable || response.Header().Get("Cache-Control") != "no-store" ||
			response.Header().Get("Retry-After") != "1" || response.Header().Get("Location") != "" {
			t.Fatalf("beyond-cap response status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("beyond-cap fetch did not fail promptly")
	}
	if upstreamRequests.Load() != maximumConcurrentArtworkFetches || countTemporary() != maximumConcurrentArtworkFetches {
		t.Fatalf("queued burst created work early: upstream=%d temporary=%d", upstreamRequests.Load(), countTemporary())
	}

	durableKey := strings.TrimPrefix(localURLs[6], publicPrefix)
	if err := os.WriteFile(service.path(durableKey), imageBytes, 0o600); err != nil {
		t.Fatalf("materialize queued cache file: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE artwork_cache SET content_type = 'image/png', byte_size = $2, cached_at = now(), last_accessed_at = now() WHERE key = $1
	`, durableKey, len(imageBytes)); err != nil {
		t.Fatalf("materialize queued cache row: %v", err)
	}
	releaseFetches()
	for range maximumConcurrentArtworkFetches + 3 {
		select {
		case response := <-responses:
			assertArtworkResponse(t, response, imageBytes, true)
		case <-time.After(time.Second):
			t.Fatal("queued artwork fetch did not finish")
		}
	}
	select {
	case err := <-sharedResult:
		if err != nil {
			t.Fatalf("same-key queued join error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("same-key queued join did not finish")
	}
	inFlight, reservedFiles, reservedBytes := service.fetchAdmission.usage()
	if inFlight != 0 || reservedFiles != 0 || reservedBytes != 0 || service.fetchAdmission.waiting() != 0 || len(service.flights) != 0 || countTemporary() != 0 {
		t.Fatalf("successful cleanup admission=(%d,%d,%d) waiters=%d flights=%d temporary=%d", inFlight, reservedFiles, reservedBytes, service.fetchAdmission.waiting(), len(service.flights), countTemporary())
	}
	if upstreamRequests.Load() != maximumConcurrentArtworkFetches+3 {
		t.Fatalf("durable-cache recheck upstream=%d want=%d", upstreamRequests.Load(), maximumConcurrentArtworkFetches+3)
	}

	invalidURL := service.LocalURL(context.Background(), fixture.URL+"/invalid")
	if response := serveArtwork(service, http.MethodGet, invalidURL); response.Code != http.StatusBadGateway {
		t.Fatalf("invalid upstream status=%d body=%q", response.Code, response.Body.String())
	}
	inFlight, reservedFiles, reservedBytes = service.fetchAdmission.usage()
	if inFlight != 0 || reservedFiles != 0 || reservedBytes != 0 || service.fetchAdmission.waiting() != 0 || len(service.flights) != 0 || countTemporary() != 0 {
		t.Fatalf("error cleanup admission=(%d,%d,%d) waiters=%d flights=%d temporary=%d", inFlight, reservedFiles, reservedBytes, service.fetchAdmission.waiting(), len(service.flights), countTemporary())
	}

	cancelURL := service.LocalURL(context.Background(), fixture.URL+"/cancel")
	cancelContext, cancel := context.WithCancel(context.Background())
	cancelRequest := httptest.NewRequest(http.MethodGet, cancelURL, nil).WithContext(cancelContext)
	cancelRequest.SetPathValue("key", strings.TrimPrefix(cancelURL, publicPrefix))
	cancelResponse := httptest.NewRecorder()
	canceledResult := make(chan struct{})
	go func() {
		service.ServeHTTP(cancelResponse, cancelRequest)
		close(canceledResult)
	}()
	select {
	case <-cancelStarted:
	case <-time.After(time.Second):
		t.Fatal("cancelable artwork fetch did not reach upstream")
	}
	cancel()
	select {
	case <-canceledResult:
	case <-time.After(time.Second):
		t.Fatal("canceled artwork fetch did not return")
	}
	inFlight, reservedFiles, reservedBytes = service.fetchAdmission.usage()
	if inFlight != 0 || reservedFiles != 0 || reservedBytes != 0 || service.fetchAdmission.waiting() != 0 || len(service.flights) != 0 || countTemporary() != 0 {
		t.Fatalf("canceled cleanup admission=(%d,%d,%d) waiters=%d flights=%d temporary=%d", inFlight, reservedFiles, reservedBytes, service.fetchAdmission.waiting(), len(service.flights), countTemporary())
	}
}

func TestServeKeyDirectPreservesDeliveryAndFailsClosed(t *testing.T) {
	pool := openArtworkTestPool(t)
	imageBytes := testPNG(t, color.NRGBA{R: 36, G: 90, B: 140, A: 255})
	var requests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		response.Header().Set("Content-Type", "image/png")
		response.Write(imageBytes)
	}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)
	localURL := service.LocalURL(context.Background(), fixture.URL+"/private/poster.png?token=provider-secret")
	key := strings.TrimPrefix(localURL, publicPrefix)

	getRequest := httptest.NewRequest(http.MethodGet, "/Items/item-id/Images/Primary", nil)
	getRequest.SetPathValue("key", "path-value-must-not-be-used")
	getResponse := httptest.NewRecorder()
	service.ServeKey(getResponse, getRequest, key)
	assertArtworkResponse(t, getResponse, imageBytes, true)
	if getResponse.Header().Get("ETag") != `"`+key+`"` || getResponse.Header().Get("Location") != "" {
		t.Fatalf("direct response etag=%q location=%q", getResponse.Header().Get("ETag"), getResponse.Header().Get("Location"))
	}
	if strings.Contains(getResponse.Body.String(), "provider-secret") || strings.Contains(getResponse.Body.String(), fixture.URL) {
		t.Fatal("direct response exposed the registered provider URL")
	}

	headRequest := httptest.NewRequest(http.MethodHead, "/Items/item-id/Images/Primary", nil)
	headResponse := httptest.NewRecorder()
	service.ServeKey(headResponse, headRequest, key)
	assertArtworkResponse(t, headResponse, imageBytes, false)
	if headResponse.Header().Get("ETag") != `"`+key+`"` {
		t.Fatalf("HEAD etag = %q", headResponse.Header().Get("ETag"))
	}

	assertArtworkResponse(t, serveArtwork(service, http.MethodGet, localURL), imageBytes, true)
	if requests.Load() != 1 {
		t.Fatalf("direct and native cache delivery made %d upstream requests, want 1", requests.Load())
	}

	for _, invalid := range []string{"", "bad", strings.Repeat("A", 64), "../" + strings.Repeat("a", 64)} {
		response := httptest.NewRecorder()
		service.ServeKey(response, httptest.NewRequest(http.MethodGet, "/Items/item-id/Images/Primary", nil), invalid)
		if response.Code != http.StatusBadRequest || response.Body.String() != "invalid artwork key\n" || response.Header().Get("Location") != "" {
			t.Fatalf("invalid key %q response = %d %q location=%q", invalid, response.Code, response.Body.String(), response.Header().Get("Location"))
		}
	}

	unknown := artworkKey(t.TempDir())
	missingResponse := httptest.NewRecorder()
	service.ServeKey(missingResponse, httptest.NewRequest(http.MethodGet, "/Items/item-id/Images/Primary", nil), unknown)
	if missingResponse.Code != http.StatusNotFound || missingResponse.Body.String() != "artwork not found\n" || missingResponse.Header().Get("Location") != "" {
		t.Fatalf("unknown key response = %d %q location=%q", missingResponse.Code, missingResponse.Body.String(), missingResponse.Header().Get("Location"))
	}

	methodResponse := httptest.NewRecorder()
	service.ServeKey(methodResponse, httptest.NewRequest(http.MethodPost, "/Items/item-id/Images/Primary", nil), key)
	if methodResponse.Code != http.StatusMethodNotAllowed || methodResponse.Header().Get("Allow") != "GET, HEAD" || requests.Load() != 1 {
		t.Fatalf("unsupported method response = %d allow=%q upstream=%d", methodResponse.Code, methodResponse.Header().Get("Allow"), requests.Load())
	}
}

func TestServeKeyIfNoneMatchShortCircuitsBeforeOpenFetchAndTouch(t *testing.T) {
	pool := openArtworkTestPool(t)
	imageBytes := testPNG(t, color.NRGBA{R: 72, G: 101, B: 144, A: 255})
	var requests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		response.Header().Set("Content-Type", "image/png")
		_, _ = response.Write(imageBytes)
	}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)
	localURL := service.LocalURL(context.Background(), fixture.URL+"/conditional")
	key := strings.TrimPrefix(localURL, publicPrefix)
	assertArtworkResponse(t, serveArtwork(service, http.MethodGet, localURL), imageBytes, true)

	lastAccessed := time.Date(2026, time.August, 8, 12, 34, 56, 0, time.UTC)
	if _, err := pool.Exec(context.Background(), `
		UPDATE artwork_cache SET last_accessed_at = $2 WHERE key = $1
	`, key, lastAccessed); err != nil {
		t.Fatalf("set artwork access sentinel: %v", err)
	}
	if err := os.Remove(service.path(key)); err != nil {
		t.Fatalf("remove cached artwork before conditional requests: %v", err)
	}

	for _, test := range []struct {
		name   string
		method string
		value  string
	}{
		{name: "strong GET", method: http.MethodGet, value: `"` + key + `"`},
		{name: "weak GET", method: http.MethodGet, value: `"other", W/"` + key + `"`},
		{name: "wildcard GET", method: http.MethodGet, value: `*`},
		{name: "exact HEAD", method: http.MethodHead, value: `"` + key + `"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, localURL, nil)
			request.Header.Set("If-None-Match", test.value)
			response := httptest.NewRecorder()
			service.ServeKey(response, request, key)
			if response.Code != http.StatusNotModified || response.Body.Len() != 0 ||
				response.Header().Get("ETag") != `"`+key+`"` ||
				response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
				t.Fatalf("conditional response status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
			}
		})
	}
	if requests.Load() != 1 {
		t.Fatalf("conditional requests fetched artwork: requests=%d", requests.Load())
	}
	var persistedAccess time.Time
	var byteSize int64
	if err := pool.QueryRow(context.Background(), `
		SELECT last_accessed_at, byte_size FROM artwork_cache WHERE key = $1
	`, key).Scan(&persistedAccess, &byteSize); err != nil {
		t.Fatalf("read conditional artwork state: %v", err)
	}
	if !persistedAccess.Equal(lastAccessed) || byteSize != int64(len(imageBytes)) {
		t.Fatalf("conditional request mutated cache state: last_accessed_at=%v byte_size=%d", persistedAccess, byteSize)
	}
}

func TestIfNoneMatchArtworkBounds(t *testing.T) {
	etag := `"` + strings.Repeat("a", 64) + `"`
	for _, values := range [][]string{
		{strings.Repeat("x", maxIfNoneMatchBytes), etag},
		{etag, strings.Repeat("x", maxIfNoneMatchBytes)},
	} {
		if ifNoneMatchArtwork(values, etag) {
			t.Fatalf("oversized If-None-Match was accepted: lengths=%d,%d", len(values[0]), len(values[1]))
		}
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
	service.maxBytes.Store(int64(len(secondImage)))
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

type observedDoneContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (ctx *observedDoneContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.entered) })
	return ctx.Context.Done()
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
