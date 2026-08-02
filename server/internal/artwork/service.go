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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxObjectBytes    int64 = 12 << 20
	maxImageDimension       = 16384
	maxImagePixels    int64 = 40_000_000
	publicPrefix            = "/api/v1/artwork/"
	pruneLockID       int64 = 0x617274776f726b
)

var errObjectExceedsCache = errors.New("artwork object exceeds cache capacity")

type Options struct {
	Directory  string
	MaxBytes   int64
	HTTPClient *http.Client
	Logger     *slog.Logger
}

type Service struct {
	pool       *pgxpool.Pool
	directory  string
	maxBytes   int64
	httpClient *http.Client
	logger     *slog.Logger
	allowLocal bool

	flightsMu sync.Mutex
	flights   map[string]*flight
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
	client := options.HTTPClient
	allowLocal := client != nil
	if client == nil {
		client = newProductionHTTPClient()
	}
	service := &Service{
		pool: pool, directory: absoluteDirectory, maxBytes: options.MaxBytes,
		httpClient: client, logger: logger, allowLocal: allowLocal,
		flights: make(map[string]*flight),
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
	localized := append([]string(nil), upstream...)
	if len(upstream) == 0 {
		return localized
	}

	keys := make([]string, 0, len(upstream))
	urls := make([]string, 0, len(upstream))
	indexes := make(map[string][]int, len(upstream))
	seen := make(map[string]struct{}, len(upstream))
	expected := make(map[string]string, len(upstream))
	for index, candidate := range upstream {
		normalized, err := normalizeURL(candidate, !service.allowLocal)
		if err != nil {
			continue
		}
		key := artworkKey(normalized)
		indexes[key] = append(indexes[key], index)
		expected[key] = normalized
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
		urls = append(urls, normalized)
	}
	if len(keys) == 0 {
		return localized
	}

	rows, err := service.pool.Query(ctx, `
		INSERT INTO artwork_cache (key, source_url)
		SELECT input.key, input.source_url
		FROM unnest($1::text[], $2::text[]) AS input(key, source_url)
		ON CONFLICT (key) DO UPDATE SET source_url = artwork_cache.source_url
		RETURNING key, source_url
	`, keys, urls)
	if err != nil {
		return localized
	}
	defer rows.Close()
	registered := make(map[string]string, len(keys))
	for rows.Next() {
		var key, sourceURL string
		if err := rows.Scan(&key, &sourceURL); err != nil {
			return append([]string(nil), upstream...)
		}
		registered[key] = sourceURL
	}
	if rows.Err() != nil {
		return append([]string(nil), upstream...)
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
	return localized
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
		http.Error(response, "artwork unavailable", http.StatusBadGateway)
		return
	}
	if !found {
		http.Error(response, "artwork not found", http.StatusNotFound)
		return
	}
	if file == nil {
		if err := service.fetchCoalesced(request.Context(), record); err != nil {
			service.logger.WarnContext(request.Context(), "fetch artwork", "key", key, "error", err)
			http.Error(response, "artwork unavailable", http.StatusBadGateway)
			return
		}
		record, file, found, err = service.load(request.Context(), key)
		if err != nil || !found || file == nil {
			if err == nil {
				err = errors.New("downloaded artwork was not durable")
			}
			service.logger.WarnContext(request.Context(), "reopen artwork", "key", key, "error", err)
			http.Error(response, "artwork unavailable", http.StatusBadGateway)
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
		normalized, err := normalizeURL(record.sourceURL, true)
		if err != nil {
			return fmt.Errorf("validate registered artwork URL: %w", err)
		}
		record.sourceURL = normalized
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, record.sourceURL, nil)
	if err != nil {
		return fmt.Errorf("create artwork request: %w", err)
	}
	request.Header.Set("Accept", "image/webp,image/png,image/jpeg;q=0.9")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "Rivune-Artwork-Cache/1")
	upstream, err := service.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("download artwork: %w", err)
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

func (service *Service) pruneTransaction(ctx context.Context, transaction pgx.Tx) ([]string, error) {
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

	var evicted []string
	for _, entry := range candidates {
		if total <= service.maxBytes {
			break
		}
		if err := removeIfPresent(service.path(entry.key)); err != nil {
			return nil, fmt.Errorf("remove pruned artwork %s: %w", entry.key, err)
		}
		evicted = append(evicted, entry.key)
		total -= entry.size
	}
	if len(evicted) == 0 {
		return nil, nil
	}
	batch := &pgx.Batch{}
	for _, key := range evicted {
		batch.Queue(`
			UPDATE artwork_cache
			SET content_type = NULL, byte_size = NULL, cached_at = NULL, last_accessed_at = NULL
			WHERE key = $1
		`, key)
	}
	results := transaction.SendBatch(ctx, batch)
	for range evicted {
		if _, err := results.Exec(); err != nil {
			results.Close()
			return nil, fmt.Errorf("clear pruned artwork metadata: %w", err)
		}
	}
	if err := results.Close(); err != nil {
		return nil, fmt.Errorf("finish artwork pruning batch: %w", err)
	}
	return evicted, nil
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
