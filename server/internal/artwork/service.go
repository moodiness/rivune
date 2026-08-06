package artwork

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/netguard"
)

const (
	maxObjectBytes             int64 = 12 << 20
	maxImageDimension                = 16384
	maxImagePixels             int64 = 40_000_000
	publicPrefix                     = "/api/v1/artwork/"
	pruneLockID                int64 = 0x617274776f726b
	warmupConcurrency                = 6
	warmupQueueCapacity              = 32_768
	maxArtworkRegistrations          = 32_768
	registrationPruneBatchSize       = 256
	uncachedRegistrationTTL          = 24 * time.Hour
)

var errObjectExceedsCache = errors.New("artwork object exceeds cache capacity")

type Options struct {
	Directory         string
	MaxBytes          int64
	HTTPClient        *http.Client
	LANArtworkOrigins []string
	Logger            *slog.Logger
}

type Service struct {
	pool            *pgxpool.Pool
	directory       string
	maxBytes        int64
	httpClient      *http.Client
	logger          *slog.Logger
	allowLocal      bool
	transportPolicy transportPolicy

	registrationLimit int
	registrationTTL   time.Duration

	flightsMu sync.Mutex
	flights   map[string]*flight

	warmupMu     sync.Mutex
	warmupQueue  chan string
	queuedWarmup map[string]struct{}
}

type flight struct {
	done chan struct{}
	err  error
}

type cacheRecord struct {
	key         string
	sourceURL   string
	contentType string
	byteSize    int64
}

func New(pool *pgxpool.Pool, options Options) (*Service, error) {
	if pool == nil {
		return nil, errors.New("artwork database pool is required")
	}
	directory := strings.TrimSpace(options.Directory)
	if directory == "" {
		return nil, errors.New("artwork cache directory is required")
	}
	if options.MaxBytes <= 0 {
		return nil, errors.New("artwork cache byte limit must be positive")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create artwork cache directory: %w", err)
	}
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve artwork cache directory: %w", err)
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	policy, err := newTransportPolicy(options.LANArtworkOrigins)
	if err != nil {
		return nil, err
	}
	client := options.HTTPClient
	allowLocal := client != nil
	if client == nil {
		client = newProductionHTTPClient(policy)
	}
	service := &Service{
		pool: pool, directory: absoluteDirectory, maxBytes: options.MaxBytes,
		httpClient: client, logger: logger, allowLocal: allowLocal, transportPolicy: policy,
		registrationLimit: maxArtworkRegistrations,
		registrationTTL:   uncachedRegistrationTTL,
		flights:           make(map[string]*flight),
		warmupQueue:       make(chan string, warmupQueueCapacity),
		queuedWarmup:      make(map[string]struct{}),
	}
	if err := service.pruneRegistrationBacklog(context.Background()); err != nil {
		return nil, fmt.Errorf("bound artwork registrations during initialization: %w", err)
	}
	if err := service.Prune(context.Background()); err != nil {
		return nil, fmt.Errorf("prune artwork cache during initialization: %w", err)
	}
	return service, nil
}

func (service *Service) LocalURL(ctx context.Context, upstream string) string {
	return service.LocalURLs(ctx, []string{upstream})[0]
}

func (service *Service) LocalURLs(ctx context.Context, upstream []string) []string {
	localized := make([]string, len(upstream))
	if len(upstream) == 0 {
		return localized
	}

	keys := make([]string, 0, len(upstream))
	registrationKeys := make([]string, 0, len(upstream))
	urls := make([]string, 0, len(upstream))
	indexes := make(map[string][]int, len(upstream))
	referenceIndexes := make(map[string][]int)
	lookupSeen := make(map[string]struct{}, len(upstream))
	registrationSeen := make(map[string]struct{}, len(upstream))
	expected := make(map[string]string, len(upstream))
	for index, candidate := range upstream {
		trimmed := strings.TrimSpace(candidate)
		if strings.HasPrefix(trimmed, publicPrefix) {
			key := strings.TrimPrefix(trimmed, publicPrefix)
			if validKey(key) {
				referenceIndexes[key] = append(referenceIndexes[key], index)
				if _, exists := lookupSeen[key]; !exists {
					lookupSeen[key] = struct{}{}
					keys = append(keys, key)
				}
			}
			continue
		}
		normalized, err := normalizeURLWithPolicy(trimmed, !service.allowLocal, service.transportPolicy)
		if err != nil {
			continue
		}
		key := artworkKey(normalized)
		indexes[key] = append(indexes[key], index)
		expected[key] = normalized
		if _, exists := lookupSeen[key]; !exists {
			lookupSeen[key] = struct{}{}
			keys = append(keys, key)
		}
		if _, exists := registrationSeen[key]; exists {
			continue
		}
		registrationSeen[key] = struct{}{}
		registrationKeys = append(registrationKeys, key)
		urls = append(urls, normalized)
	}
	if len(keys) == 0 {
		return localized
	}

	for start := 0; start < len(registrationKeys); start += registrationPruneBatchSize {
		end := min(start+registrationPruneBatchSize, len(registrationKeys))
		if !service.registerBatch(ctx, registrationKeys[start:end], urls[start:end]) {
			return localized
		}
	}
	registered := make(map[string]string, len(keys))
	pendingKeys := make([]string, 0, len(keys))
	for start := 0; start < len(keys); start += registrationPruneBatchSize {
		end := min(start+registrationPruneBatchSize, len(keys))
		batchRegistered, batchPending, ok := service.lookupRegistrationBatch(ctx, keys[start:end])
		if !ok {
			return localized
		}
		for key, sourceURL := range batchRegistered {
			registered[key] = sourceURL
		}
		pendingKeys = append(pendingKeys, batchPending...)
	}

	for key, positions := range indexes {
		sourceURL, exists := registered[key]
		if !exists || sourceURL != expected[key] {
			continue
		}
		for _, position := range positions {
			localized[position] = publicPrefix + key
		}
	}
	for key, positions := range referenceIndexes {
		if _, exists := registered[key]; !exists {
			continue
		}
		for _, position := range positions {
			localized[position] = publicPrefix + key
		}
	}
	service.enqueueWarmups(pendingKeys)
	return localized
}

func (service *Service) registerBatch(ctx context.Context, keys, urls []string) bool {
	transaction, err := service.pool.Begin(ctx)
	if err != nil {
		return false
	}
	defer transaction.Rollback(ctx)
	if err := lockArtworkCache(ctx, transaction); err != nil {
		return false
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO artwork_cache (key, source_url)
		SELECT input.key, input.source_url
		FROM unnest($1::text[], $2::text[]) AS input(key, source_url)
		ON CONFLICT (key) DO UPDATE
		SET registered_at = now()
	`, keys, urls); err != nil {
		return false
	}
	if _, err := service.pruneRegistrations(ctx, transaction); err != nil {
		return false
	}
	return transaction.Commit(ctx) == nil
}

func (service *Service) lookupRegistrationBatch(ctx context.Context, keys []string) (map[string]string, []string, bool) {
	rows, err := service.pool.Query(ctx, `
		SELECT key, source_url, byte_size
		FROM artwork_cache
		WHERE key = ANY($1::text[])
	`, keys)
	if err != nil {
		return nil, nil, false
	}
	registered := make(map[string]string, len(keys))
	pendingKeys := make([]string, 0, len(keys))
	for rows.Next() {
		var key, sourceURL string
		var byteSize *int64
		if err := rows.Scan(&key, &sourceURL, &byteSize); err != nil {
			rows.Close()
			return nil, nil, false
		}
		registered[key] = sourceURL
		if byteSize == nil {
			pendingKeys = append(pendingKeys, key)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, false
	}
	rows.Close()
	return registered, pendingKeys, true
}

func (service *Service) RunWarmup(ctx context.Context) {
	var wait sync.WaitGroup
	wait.Add(warmupConcurrency)
	for range warmupConcurrency {
		go func() {
			defer wait.Done()
			service.runWarmupWorker(ctx)
		}()
	}
	<-ctx.Done()
	wait.Wait()
}

func (service *Service) runWarmupWorker(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case key := <-service.warmupQueue:
			service.warmArtwork(ctx, key)
			service.completeWarmup(key)
		}
	}
}

func (service *Service) warmArtwork(ctx context.Context, key string) {
	record, found, err := service.lookup(ctx, key)
	if err == nil && found {
		err = service.fetchCoalesced(ctx, record)
	}
	if err != nil && ctx.Err() == nil {
		service.logger.WarnContext(ctx, "warm artwork", "key", key, "error", err)
	}
}

func (service *Service) enqueueWarmups(keys []string) {
	for _, key := range keys {
		if !validKey(key) {
			continue
		}
		service.warmupMu.Lock()
		if _, exists := service.queuedWarmup[key]; exists {
			service.warmupMu.Unlock()
			continue
		}
		select {
		case service.warmupQueue <- key:
			service.queuedWarmup[key] = struct{}{}
		default:
		}
		service.warmupMu.Unlock()
	}
}

func (service *Service) completeWarmup(key string) {
	service.warmupMu.Lock()
	delete(service.queuedWarmup, key)
	service.warmupMu.Unlock()
}

func (service *Service) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		response.Header().Set("Allow", "GET, HEAD")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	key := request.PathValue("key")
	if !validKey(key) {
		http.Error(response, "invalid artwork key", http.StatusBadRequest)
		return
	}

	record, file, found, err := service.load(request.Context(), key)
	if err != nil {
		service.logger.WarnContext(request.Context(), "load artwork", "key", key, "error", err)
		respondArtworkUnavailable(response)
		return
	}
	if !found {
		http.Error(response, "artwork not found", http.StatusNotFound)
		return
	}
	if file == nil {
		if err := service.fetchCoalesced(request.Context(), record); err != nil {
			service.logger.WarnContext(request.Context(), "fetch artwork", "key", key, "error", err)
			respondArtworkUnavailable(response)
			return
		}
		record, file, found, err = service.load(request.Context(), key)
		if err != nil || !found || file == nil {
			if err == nil {
				err = errors.New("downloaded artwork was not durable")
			}
			service.logger.WarnContext(request.Context(), "reopen artwork", "key", key, "error", err)
			respondArtworkUnavailable(response)
			return
		}
	}
	defer file.Close()

	response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	response.Header().Set("Content-Type", record.contentType)
	response.Header().Set("Content-Length", strconv.FormatInt(record.byteSize, 10))
	response.Header().Set("ETag", `"`+key+`"`)
	response.WriteHeader(http.StatusOK)
	if request.Method == http.MethodGet {
		if _, err := io.CopyN(response, file, record.byteSize); err != nil {
			service.logger.WarnContext(request.Context(), "serve artwork", "key", key, "error", err)
		}
	}
	if _, err := service.pool.Exec(request.Context(), `
		UPDATE artwork_cache SET last_accessed_at = now()
		WHERE key = $1 AND byte_size IS NOT NULL
	`, key); err != nil {
		service.logger.WarnContext(request.Context(), "touch artwork", "key", key, "error", err)
	}
}

func respondArtworkUnavailable(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
	http.Error(response, "artwork unavailable", http.StatusBadGateway)
}

func (service *Service) Prune(ctx context.Context) error {
	transaction, err := service.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin artwork pruning: %w", err)
	}
	defer transaction.Rollback(ctx)
	if err := lockArtworkCache(ctx, transaction); err != nil {
		return err
	}
	if _, err := service.pruneTransaction(ctx, transaction); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit artwork pruning: %w", err)
	}
	return nil
}

func (service *Service) pruneRegistrationBacklog(ctx context.Context) error {
	for {
		transaction, err := service.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin artwork registration pruning: %w", err)
		}
		if err := lockArtworkCache(ctx, transaction); err != nil {
			transaction.Rollback(ctx)
			return err
		}
		if _, err := service.pruneRegistrations(ctx, transaction); err != nil {
			transaction.Rollback(ctx)
			return err
		}
		if err := transaction.Commit(ctx); err != nil {
			return fmt.Errorf("commit artwork registration pruning: %w", err)
		}

		var overflow bool
		if err := service.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM artwork_cache
				ORDER BY registered_at DESC, key DESC
				LIMIT 1
				OFFSET $1
			)
		`, service.registrationLimit).Scan(&overflow); err != nil {
			return fmt.Errorf("check artwork registration budget: %w", err)
		}
		if !overflow {
			return nil
		}
	}
}

func artworkKey(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func validKey(key string) bool {
	if len(key) != sha256.Size*2 {
		return false
	}
	for _, character := range key {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func (service *Service) lookup(ctx context.Context, key string) (cacheRecord, bool, error) {
	record := cacheRecord{key: key}
	err := service.pool.QueryRow(ctx, `
		SELECT source_url, COALESCE(content_type, ''), COALESCE(byte_size, 0)
		FROM artwork_cache WHERE key = $1
	`, key).Scan(&record.sourceURL, &record.contentType, &record.byteSize)
	if errors.Is(err, pgx.ErrNoRows) {
		return cacheRecord{}, false, nil
	}
	if err != nil {
		return cacheRecord{}, false, fmt.Errorf("query artwork registration: %w", err)
	}
	return record, true, nil
}

func (service *Service) load(ctx context.Context, key string) (cacheRecord, *os.File, bool, error) {
	record, found, err := service.lookup(ctx, key)
	if err != nil || !found || record.byteSize == 0 {
		return record, nil, found, err
	}
	if file, valid := service.openCached(record); valid {
		return record, file, true, nil
	}
	return service.repair(ctx, key)
}

func (service *Service) repair(ctx context.Context, key string) (cacheRecord, *os.File, bool, error) {
	transaction, err := service.pool.Begin(ctx)
	if err != nil {
		return cacheRecord{}, nil, false, fmt.Errorf("begin artwork repair: %w", err)
	}
	defer transaction.Rollback(ctx)
	if err := lockArtworkCache(ctx, transaction); err != nil {
		return cacheRecord{}, nil, false, err
	}
	record := cacheRecord{key: key}
	err = transaction.QueryRow(ctx, `
		SELECT source_url, COALESCE(content_type, ''), COALESCE(byte_size, 0)
		FROM artwork_cache WHERE key = $1 FOR UPDATE
	`, key).Scan(&record.sourceURL, &record.contentType, &record.byteSize)
	if errors.Is(err, pgx.ErrNoRows) {
		return cacheRecord{}, nil, false, nil
	}
	if err != nil {
		return cacheRecord{}, nil, false, fmt.Errorf("query artwork during repair: %w", err)
	}
	if record.byteSize > 0 {
		if file, valid := service.openCached(record); valid {
			if err := transaction.Commit(ctx); err != nil {
				file.Close()
				return cacheRecord{}, nil, false, fmt.Errorf("commit artwork repair: %w", err)
			}
			return record, file, true, nil
		}
		if err := removeIfPresent(service.path(key)); err != nil {
			return cacheRecord{}, nil, false, fmt.Errorf("remove corrupt artwork: %w", err)
		}
		if _, err := transaction.Exec(ctx, `
			UPDATE artwork_cache
			SET content_type = NULL, byte_size = NULL, cached_at = NULL, last_accessed_at = NULL
			WHERE key = $1
		`, key); err != nil {
			return cacheRecord{}, nil, false, fmt.Errorf("clear corrupt artwork metadata: %w", err)
		}
		record.contentType = ""
		record.byteSize = 0
	}
	if err := transaction.Commit(ctx); err != nil {
		return cacheRecord{}, nil, false, fmt.Errorf("commit artwork repair: %w", err)
	}
	return record, nil, true, nil
}

func (service *Service) openCached(record cacheRecord) (*os.File, bool) {
	file, err := os.Open(service.path(record.key))
	if err != nil {
		return nil, false
	}
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() != record.byteSize || record.byteSize <= 0 || record.byteSize > maxObjectBytes {
		file.Close()
		return nil, false
	}
	if !validImageFile(file, record.contentType, record.byteSize) {
		file.Close()
		return nil, false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return nil, false
	}
	return file, true
}

func (service *Service) fetchCoalesced(ctx context.Context, record cacheRecord) error {
	service.flightsMu.Lock()
	if existing := service.flights[record.key]; existing != nil {
		service.flightsMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-existing.done:
			return existing.err
		}
	}
	current := &flight{done: make(chan struct{})}
	service.flights[record.key] = current
	service.flightsMu.Unlock()

	current.err = service.fetch(ctx, record)
	close(current.done)
	service.flightsMu.Lock()
	delete(service.flights, record.key)
	service.flightsMu.Unlock()
	return current.err
}

func (service *Service) fetch(ctx context.Context, record cacheRecord) error {
	if current, file, found, err := service.load(ctx, record.key); err != nil {
		return err
	} else if !found {
		return pgx.ErrNoRows
	} else if file != nil {
		file.Close()
		return nil
	} else {
		record = current
	}

	if !service.allowLocal {
		normalized, err := normalizeURLWithPolicy(record.sourceURL, true, service.transportPolicy)
		if err != nil {
			return fmt.Errorf("validate registered artwork URL: %w", err)
		}
		record.sourceURL = normalized
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, record.sourceURL, nil)
	if err != nil {
		return fmt.Errorf("create artwork request: %w", netguard.SanitizeURLError(err))
	}
	request.Header.Set("Accept", "image/webp,image/png,image/jpeg;q=0.9")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "Rivune-Artwork-Cache/1")
	upstream, err := service.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("download artwork: %w", netguard.SanitizeURLError(err))
	}
	defer upstream.Body.Close()
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		return fmt.Errorf("download artwork: upstream returned %s", upstream.Status)
	}
	if upstream.ContentLength > maxObjectBytes {
		return fmt.Errorf("download artwork: object exceeds %d bytes", maxObjectBytes)
	}

	temporary, err := os.CreateTemp(service.directory, ".artwork-*")
	if err != nil {
		return fmt.Errorf("create artwork temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			temporary.Close()
		}
		os.Remove(temporaryPath)
	}()

	limited := &io.LimitedReader{R: upstream.Body, N: maxObjectBytes + 1}
	var prefix [512]byte
	prefixSize, readErr := io.ReadFull(limited, prefix[:])
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return fmt.Errorf("read artwork header: %w", readErr)
	}
	contentType := detectedContentType(prefix[:prefixSize])
	if contentType == "" {
		return errors.New("download artwork: unsupported image content")
	}
	written, err := temporary.Write(prefix[:prefixSize])
	if err != nil || written != prefixSize {
		if err == nil {
			err = io.ErrShortWrite
		}
		return fmt.Errorf("write artwork header: %w", err)
	}
	copied, err := io.Copy(temporary, limited)
	if err != nil {
		return fmt.Errorf("write artwork body: %w", err)
	}
	byteSize := int64(prefixSize) + copied
	if byteSize > maxObjectBytes {
		return fmt.Errorf("download artwork: object exceeds %d bytes", maxObjectBytes)
	}
	if !validImageFile(temporary, contentType, byteSize) {
		return errors.New("download artwork: invalid image content")
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync artwork temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close artwork temporary file: %w", err)
	}
	closed = true
	if err := service.publish(ctx, record.key, temporaryPath, contentType, byteSize); err != nil {
		return err
	}
	return nil
}

func (service *Service) publish(ctx context.Context, key, temporaryPath, contentType string, byteSize int64) error {
	transaction, err := service.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin artwork publication: %w", err)
	}
	defer transaction.Rollback(ctx)
	if err := lockArtworkCache(ctx, transaction); err != nil {
		return err
	}

	existing := cacheRecord{key: key}
	err = transaction.QueryRow(ctx, `
		SELECT source_url, COALESCE(content_type, ''), COALESCE(byte_size, 0)
		FROM artwork_cache WHERE key = $1 FOR UPDATE
	`, key).Scan(&existing.sourceURL, &existing.contentType, &existing.byteSize)
	if err != nil {
		return fmt.Errorf("lock artwork registration: %w", err)
	}
	if existing.byteSize > 0 {
		if file, valid := service.openCached(existing); valid {
			file.Close()
			if err := transaction.Commit(ctx); err != nil {
				return fmt.Errorf("commit concurrent artwork publication: %w", err)
			}
			return nil
		}
		if err := removeIfPresent(service.path(key)); err != nil {
			return fmt.Errorf("remove replaced artwork: %w", err)
		}
	}
	finalPath := service.path(key)
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return fmt.Errorf("publish artwork file: %w", err)
	}
	published := true
	defer func() {
		if published {
			os.Remove(finalPath)
		}
	}()
	if _, err := transaction.Exec(ctx, `
		UPDATE artwork_cache
		SET content_type = $2, byte_size = $3, cached_at = now(), last_accessed_at = now()
		WHERE key = $1
	`, key, contentType, byteSize); err != nil {
		return fmt.Errorf("publish artwork metadata: %w", err)
	}
	evicted, err := service.pruneTransaction(ctx, transaction)
	if err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit artwork publication: %w", err)
	}
	published = false
	for _, evictedKey := range evicted {
		if evictedKey == key {
			return errObjectExceedsCache
		}
	}
	return nil
}

func (service *Service) pruneRegistrations(ctx context.Context, transaction pgx.Tx) ([]string, error) {
	cutoff := time.Now().Add(-service.registrationTTL)
	stale, err := service.deleteRegistrations(ctx, transaction, `
		WITH candidates AS (
			SELECT key
			FROM artwork_cache
			WHERE byte_size IS NULL AND registered_at < $1
			ORDER BY registered_at ASC, key ASC
			LIMIT $2
			FOR UPDATE
		)
		DELETE FROM artwork_cache AS cache
		USING candidates
		WHERE cache.key = candidates.key
		RETURNING cache.key
	`, cutoff, registrationPruneBatchSize)
	if err != nil {
		return nil, fmt.Errorf("delete expired artwork registrations: %w", err)
	}
	remaining := registrationPruneBatchSize - len(stale)
	if remaining == 0 {
		return stale, nil
	}
	var overflow int
	if err := transaction.QueryRow(ctx, `
		SELECT count(*)
		FROM (
			SELECT 1
			FROM artwork_cache
			ORDER BY registered_at DESC, key DESC
			LIMIT $2
			OFFSET $1
		) AS excess
	`, service.registrationLimit, remaining).Scan(&overflow); err != nil {
		return nil, fmt.Errorf("count excess artwork registrations: %w", err)
	}
	if overflow == 0 {
		return stale, nil
	}
	uncached, err := service.deleteRegistrations(ctx, transaction, `
		WITH candidates AS (
			SELECT key
			FROM artwork_cache
			WHERE byte_size IS NULL
			ORDER BY registered_at ASC, key ASC
			LIMIT $1
			FOR UPDATE
		)
		DELETE FROM artwork_cache AS cache
		USING candidates
		WHERE cache.key = candidates.key
		RETURNING cache.key
	`, overflow)
	if err != nil {
		return nil, fmt.Errorf("delete excess uncached artwork registrations: %w", err)
	}
	stale = append(stale, uncached...)
	overflow -= len(uncached)
	if overflow == 0 {
		return stale, nil
	}
	cached, err := service.deleteRegistrations(ctx, transaction, `
		WITH candidates AS (
			SELECT key
			FROM artwork_cache
			WHERE byte_size IS NOT NULL
			ORDER BY last_accessed_at ASC, key ASC
			LIMIT $1
			FOR UPDATE
		)
		DELETE FROM artwork_cache AS cache
		USING candidates
		WHERE cache.key = candidates.key
		RETURNING cache.key
	`, overflow)
	if err != nil {
		return nil, fmt.Errorf("delete excess cached artwork registrations: %w", err)
	}
	return append(stale, cached...), nil
}

func (service *Service) deleteRegistrations(ctx context.Context, transaction pgx.Tx, query string, arguments ...any) ([]string, error) {
	rows, err := transaction.Query(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	var deleted []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		deleted = append(deleted, key)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, key := range deleted {
		if err := removeIfPresent(service.path(key)); err != nil {
			return nil, fmt.Errorf("remove artwork object %s: %w", key, err)
		}
	}
	return deleted, nil
}

func (service *Service) pruneTransaction(ctx context.Context, transaction pgx.Tx) ([]string, error) {
	evicted, err := service.pruneRegistrations(ctx, transaction)
	if err != nil {
		return nil, err
	}
	rows, err := transaction.Query(ctx, `
		SELECT key, byte_size
		FROM artwork_cache
		WHERE byte_size IS NOT NULL
		ORDER BY last_accessed_at ASC, key ASC
		FOR UPDATE
	`)
	if err != nil {
		return nil, fmt.Errorf("query artwork LRU: %w", err)
	}
	type candidate struct {
		key  string
		size int64
	}
	var candidates []candidate
	var total int64
	for rows.Next() {
		var entry candidate
		if err := rows.Scan(&entry.key, &entry.size); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan artwork LRU: %w", err)
		}
		candidates = append(candidates, entry)
		total += entry.size
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read artwork LRU: %w", err)
	}
	rows.Close()

	var lruEvicted []string
	for _, entry := range candidates {
		if total <= service.maxBytes {
			break
		}
		if err := removeIfPresent(service.path(entry.key)); err != nil {
			return nil, fmt.Errorf("remove pruned artwork %s: %w", entry.key, err)
		}
		lruEvicted = append(lruEvicted, entry.key)
		total -= entry.size
	}
	if len(lruEvicted) == 0 {
		return evicted, nil
	}
	if _, err := transaction.Exec(ctx, `
		DELETE FROM artwork_cache
		WHERE key = ANY($1::text[])
	`, lruEvicted); err != nil {
		return nil, fmt.Errorf("delete pruned artwork registrations: %w", err)
	}
	return append(evicted, lruEvicted...), nil
}

func lockArtworkCache(ctx context.Context, transaction pgx.Tx) error {
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, pruneLockID); err != nil {
		return fmt.Errorf("lock artwork cache: %w", err)
	}
	return nil
}

func (service *Service) path(key string) string {
	return filepath.Join(service.directory, key)
}

func removeIfPresent(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func validImageFile(file *os.File, contentType string, byteSize int64) bool {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false
	}
	if contentType == "image/jpeg" || contentType == "image/png" {
		configuration, format, err := image.DecodeConfig(file)
		if err != nil || !validImageDimensions(configuration.Width, configuration.Height) {
			return false
		}
		return contentType == "image/jpeg" && format == "jpeg" || contentType == "image/png" && format == "png"
	}
	if contentType != "image/webp" || byteSize < 25 || byteSize > int64(^uint32(0))+8 {
		return false
	}
	var header [30]byte
	read, err := io.ReadFull(file, header[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	if read < 20 || detectedContentType(header[:read]) != contentType {
		return false
	}
	if int64(binary.LittleEndian.Uint32(header[4:8]))+8 != byteSize {
		return false
	}
	chunkSize := int64(binary.LittleEndian.Uint32(header[16:20]))
	paddedChunkSize := chunkSize + chunkSize&1
	if chunkSize <= 0 || paddedChunkSize > byteSize-20 {
		return false
	}
	var width, height int
	switch string(header[12:16]) {
	case "VP8 ":
		if chunkSize < 10 || read < 30 || header[23] != 0x9d || header[24] != 0x01 || header[25] != 0x2a {
			return false
		}
		width = int(binary.LittleEndian.Uint16(header[26:28]) & 0x3fff)
		height = int(binary.LittleEndian.Uint16(header[28:30]) & 0x3fff)
	case "VP8L":
		if chunkSize < 5 || read < 25 || header[20] != 0x2f || header[24]&0xe0 != 0 {
			return false
		}
		dimensions := binary.LittleEndian.Uint32(header[21:25])
		width = int(dimensions&0x3fff) + 1
		height = int(dimensions>>14&0x3fff) + 1
	case "VP8X":
		if chunkSize < 10 || read < 30 || header[20]&0xc1 != 0 || header[21] != 0 || header[22] != 0 || header[23] != 0 {
			return false
		}
		width = int(header[24]) | int(header[25])<<8 | int(header[26])<<16
		height = int(header[27]) | int(header[28])<<8 | int(header[29])<<16
		width++
		height++
	default:
		return false
	}
	return validImageDimensions(width, height)
}

func validImageDimensions(width, height int) bool {
	return width > 0 && height > 0 &&
		width <= maxImageDimension && height <= maxImageDimension &&
		int64(width)*int64(height) <= maxImagePixels
}

func detectedContentType(prefix []byte) string {
	if len(prefix) >= 3 && prefix[0] == 0xff && prefix[1] == 0xd8 && prefix[2] == 0xff {
		return "image/jpeg"
	}
	if len(prefix) >= 8 && string(prefix[:8]) == "\x89PNG\r\n\x1a\n" {
		return "image/png"
	}
	if len(prefix) >= 16 && string(prefix[:4]) == "RIFF" && string(prefix[8:12]) == "WEBP" {
		chunk := string(prefix[12:16])
		if chunk == "VP8 " || chunk == "VP8L" || chunk == "VP8X" {
			return "image/webp"
		}
	}
	return ""
}
