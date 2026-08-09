package playback

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image/jpeg"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

const (
	TrickplayTileColumns       = 10
	TrickplayTileRows          = 10
	TrickplayIntervalSeconds   = 10
	trickplayColumns           = TrickplayTileColumns
	trickplayRows              = TrickplayTileRows
	trickplayIntervalSeconds   = TrickplayIntervalSeconds
	maximumTrickplayTileWidth  = 1024
	maximumTrickplaySheetBytes = 32 << 20
	maximumTrickplayCacheBytes = 64 << 20
	defaultTrickplayCacheTTL   = 15 * time.Minute
	trickplayGenerationTimeout = 2 * time.Minute
)

type TrickplayInput struct {
	SourceRef string
	TitleID   string
	Width     int
	Index     int
}

type TrickplayImage struct {
	JPEG         []byte
	LastModified time.Time
}

type trickplayProcessor interface {
	GenerateTrickplayJPEG(context.Context, storedAsset, int, int) ([]byte, error)
}

type trickplayCacheEntry struct {
	ready           chan struct{}
	jpeg            []byte
	err             error
	generatedAt     time.Time
	lastAccessed    time.Time
	generating      bool
	cancel          context.CancelFunc
	waiters         int
	cacheGeneration uint64
}

type trickplayCache struct {
	mu         sync.Mutex
	entries    map[[32]byte]*trickplayCacheEntry
	bytes      int64
	generation uint64
}

func newTrickplayCache() *trickplayCache {
	return &trickplayCache{entries: make(map[[32]byte]*trickplayCacheEntry)}
}

func (service *Service) TrickplayAvailable() bool {
	if service == nil || service.processor == nil {
		return false
	}
	_, available := service.processor.(trickplayProcessor)
	return available
}

func (service *Service) Trickplay(ctx context.Context, principal auth.Principal, input TrickplayInput) (TrickplayImage, error) {
	if service == nil || service.references == nil || service.processor == nil {
		return TrickplayImage{}, ErrUnsupportedSource
	}
	input.SourceRef = strings.TrimSpace(input.SourceRef)
	input.TitleID = strings.TrimSpace(input.TitleID)
	if len(input.SourceRef) < 16 || len(input.SourceRef) > 128 || input.TitleID == "" || len(input.TitleID) > 128 ||
		input.Width < 1 || input.Width > maximumTrickplayTileWidth || input.Index < 0 || input.Index > 1_000_000 {
		return TrickplayImage{}, ErrInvalidInput
	}
	processor, ok := service.processor.(trickplayProcessor)
	if !ok {
		return TrickplayImage{}, ErrUnsupportedSource
	}
	tx, err := service.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return TrickplayImage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	reference, err := service.references.get(input.SourceRef, principal)
	if err != nil {
		return TrickplayImage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TrickplayImage{}, fmt.Errorf("commit active trickplay profile authorization: %w", err)
	}
	if strings.TrimSpace(reference.Source.AddonID) != "" {
		if err := service.validateSourceReferenceAccess(ctx, principal, reference); err != nil {
			return TrickplayImage{}, err
		}
	}
	if reference.Asset == nil || reference.Asset.URL == "" || reference.MediaType != "movie" && reference.MediaType != "episode" {
		return TrickplayImage{}, ErrUnsupportedSource
	}

	now := service.trickplayNow()
	key := trickplayKey(reference, input)
	cache := service.trickplayCache()
	jpegBytes, modified, err := cache.load(ctx, now, service.trickplayTTL(), service.trickplayCacheLimit(), key, input.Width, func(generationParent context.Context) ([]byte, error) {
		generationContext, cancel := context.WithTimeout(generationParent, trickplayGenerationTimeout)
		defer cancel()
		return processor.GenerateTrickplayJPEG(generationContext, cloneStoredAsset(*reference.Asset), input.Width, input.Index)
	})
	if err != nil {
		return TrickplayImage{}, err
	}
	return TrickplayImage{JPEG: jpegBytes, LastModified: modified}, nil
}

func trickplayKey(reference sourceReference, input TrickplayInput) [32]byte {
	hash := sha256.New()
	for _, value := range []string{
		reference.Owner.UserID, reference.ProfileID, reference.MediaType, reference.ResourceID,
		reference.Source.AddonID, reference.Source.ManifestID, reference.Source.ID, input.TitleID,
		reference.Asset.URL,
	} {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	headerNames := make([]string, 0, len(reference.Asset.Headers))
	for name := range reference.Asset.Headers {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)
	for _, name := range headerNames {
		_, _ = hash.Write([]byte(name))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(reference.Asset.Headers[name]))
		_, _ = hash.Write([]byte{0})
	}
	var coordinates [16]byte
	binary.BigEndian.PutUint64(coordinates[:8], uint64(input.Width))
	binary.BigEndian.PutUint64(coordinates[8:], uint64(input.Index))
	_, _ = hash.Write(coordinates[:])
	var key [32]byte
	copy(key[:], hash.Sum(nil))
	return key
}

func (service *Service) trickplayCache() *trickplayCache {
	service.trickplayMu.Lock()
	defer service.trickplayMu.Unlock()
	if service.trickplayImages == nil {
		service.trickplayImages = newTrickplayCache()
	}
	return service.trickplayImages
}

func (service *Service) trickplayNow() time.Time {
	if service.now != nil {
		return service.now().UTC()
	}
	return time.Now().UTC()
}

func (service *Service) trickplayTTL() time.Duration {
	if service.mediaOptions.IdleTTL > 0 {
		return service.mediaOptions.IdleTTL
	}
	return defaultTrickplayCacheTTL
}

func (service *Service) pruneTrickplayImages(clearAll bool) {
	service.trickplayMu.Lock()
	cache := service.trickplayImages
	service.trickplayMu.Unlock()
	if cache == nil {
		return
	}
	if clearAll {
		cache.clear()
		return
	}
	cache.prune(service.trickplayNow(), service.trickplayTTL())
}

func (service *Service) trickplayCacheLimit() int64 {
	limit := service.mediaOptions.MaxStorageBytes
	if limit <= 0 || limit > maximumTrickplayCacheBytes {
		limit = maximumTrickplayCacheBytes
	}
	return limit
}

func (cache *trickplayCache) load(ctx context.Context, now time.Time, ttl time.Duration, limit int64, key [32]byte, tileWidth int, generate func(context.Context) ([]byte, error)) ([]byte, time.Time, error) {
	cache.mu.Lock()
	cache.pruneLocked(now, ttl)
	if existing := cache.entries[key]; existing != nil {
		existing.lastAccessed = now
		ready := existing.ready
		if existing.generating {
			existing.waiters++
			cache.mu.Unlock()
			select {
			case <-ready:
			case <-ctx.Done():
				cache.mu.Lock()
				existing.waiters--
				cache.mu.Unlock()
				return nil, time.Time{}, ctx.Err()
			}
			cache.mu.Lock()
			existing.waiters--
		}
		jpegBytes, modified, err := existing.jpeg, existing.generatedAt, existing.err
		cache.mu.Unlock()
		return jpegBytes, modified, err
	}
	generationContext, cancel := context.WithCancel(ctx)
	entry := &trickplayCacheEntry{ready: make(chan struct{}), generatedAt: now, lastAccessed: now, generating: true, cancel: cancel, cacheGeneration: cache.generation}
	cache.entries[key] = entry
	cache.mu.Unlock()

	jpegBytes, err := generate(generationContext)
	cancel()
	if err == nil {
		err = validateTrickplayJPEG(jpegBytes, tileWidth)
	}
	cache.mu.Lock()
	entry.generating = false
	entry.err = err
	if entry.cacheGeneration != cache.generation {
		err = ErrSourceReferenceExpired
		entry.err = err
	}
	if err != nil {
		delete(cache.entries, key)
	} else if int64(len(jpegBytes)) > limit {
		entry.err = ErrMediaStorageLimit
		delete(cache.entries, key)
	} else {
		size := int64(len(jpegBytes))
		cache.evictLocked(key, size, limit)
		if cache.bytes+size > limit {
			entry.err = ErrMediaStorageLimit
			delete(cache.entries, key)
		} else {
			entry.jpeg = jpegBytes
			cache.bytes += size
		}
	}
	close(entry.ready)
	modified := entry.generatedAt
	err = entry.err
	cache.mu.Unlock()
	return jpegBytes, modified, err
}

func (cache *trickplayCache) prune(now time.Time, ttl time.Duration) {
	cache.mu.Lock()
	cache.pruneLocked(now, ttl)
	cache.mu.Unlock()
}

func (cache *trickplayCache) clear() {
	cache.mu.Lock()
	cache.generation++
	pending := make([]chan struct{}, 0)
	for key, entry := range cache.entries {
		if entry.generating {
			entry.cancel()
			pending = append(pending, entry.ready)
		} else {
			delete(cache.entries, key)
		}
	}
	cache.bytes = 0
	cache.mu.Unlock()
	for _, ready := range pending {
		<-ready
	}
}

func (cache *trickplayCache) pruneLocked(now time.Time, ttl time.Duration) {
	for key, entry := range cache.entries {
		if !entry.generating && !entry.lastAccessed.Add(ttl).After(now) {
			cache.bytes -= int64(len(entry.jpeg))
			delete(cache.entries, key)
		}
	}
}

func (cache *trickplayCache) evictLocked(keep [32]byte, incoming, limit int64) {
	for cache.bytes+incoming > limit {
		var oldestKey [32]byte
		var oldest *trickplayCacheEntry
		for key, candidate := range cache.entries {
			if key == keep || candidate.generating || len(candidate.jpeg) == 0 {
				continue
			}
			if oldest == nil || candidate.lastAccessed.Before(oldest.lastAccessed) {
				oldestKey, oldest = key, candidate
			}
		}
		if oldest == nil {
			return
		}
		cache.bytes -= int64(len(oldest.jpeg))
		delete(cache.entries, oldestKey)
	}
}

func validateTrickplayJPEG(contents []byte, tileWidth int) error {
	if len(contents) == 0 || len(contents) > maximumTrickplaySheetBytes {
		return ErrMediaProcessingFailed
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(contents))
	if err != nil || config.Width < 1 || config.Height < 1 ||
		config.Width > maximumTrickplayTileWidth*trickplayColumns ||
		config.Height > TrickplayThumbnailHeight(maximumTrickplayTileWidth)*trickplayRows ||
		tileWidth > 0 && (config.Width != tileWidth*trickplayColumns ||
			config.Height != TrickplayThumbnailHeight(tileWidth)*trickplayRows) {
		return ErrMediaProcessingFailed
	}
	return nil
}

func TrickplayThumbnailHeight(width int) int {
	height := width * 9 / 16
	if height < 2 {
		return 2
	}
	return height - height%2
}

func (processor *FFmpegProcessor) GenerateTrickplayJPEG(ctx context.Context, asset storedAsset, width, index int) ([]byte, error) {
	if processor == nil || width < 1 || width > maximumTrickplayTileWidth || index < 0 || index > 1_000_000 {
		return nil, ErrInvalidInput
	}
	if err := validateMediaSource(ctx, asset.URL); err != nil {
		return nil, err
	}
	if err := processor.acquire(ctx); err != nil {
		return nil, err
	}
	defer processor.release()
	egress, err := processor.startInputEgress(ctx, asset)
	if err != nil {
		return nil, err
	}
	if egress != nil {
		defer egress.Close()
	}
	commandAsset, err := guardedCommandAsset(asset, egress)
	if err != nil {
		return nil, err
	}
	startSeconds := index * trickplayColumns * trickplayRows * trickplayIntervalSeconds
	arguments := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", inputProtocolWhitelist(commandAsset.URL),
		"-analyzeduration", "1000000", "-probesize", "1000000",
	}
	arguments = append(arguments, ffmpegInputArguments(commandAsset)...)
	if startSeconds > 0 {
		arguments = append(arguments, "-ss", strconv.Itoa(startSeconds))
	}
	height := TrickplayThumbnailHeight(width)
	filter := "select='isnan(prev_selected_t)+gte(t-prev_selected_t," + strconv.Itoa(trickplayIntervalSeconds) + ")',scale=" +
		strconv.Itoa(width) + ":" + strconv.Itoa(height) + ":force_original_aspect_ratio=decrease,pad=" +
		strconv.Itoa(width) + ":" + strconv.Itoa(height) + ":(ow-iw)/2:(oh-ih)/2,tile=" +
		strconv.Itoa(trickplayColumns) + "x" + strconv.Itoa(trickplayRows) + ":nb_frames=" + strconv.Itoa(trickplayColumns*trickplayRows) + ":padding=0:margin=0,format=yuvj420p"
	arguments = append(arguments,
		"-i", commandAsset.URL, "-map", "0:v:0", "-an", "-sn", "-dn", "-vf", filter,
		"-frames:v", "1", "-c:v", "mjpeg", "-pix_fmt", "yuvj420p", "-strict", "unofficial", "-q:v", "4", "-f", "image2pipe", "pipe:1",
	)
	var output bytes.Buffer
	writer := &maximumWriter{destination: &output, remaining: maximumTrickplaySheetBytes}
	if err := processor.run(ctx, arguments, writer); err != nil {
		return nil, err
	}
	if writer.exceeded {
		return nil, fmt.Errorf("%w: trickplay sheet exceeds output limit", ErrMediaProcessingFailed)
	}
	contents := output.Bytes()
	if len(contents) == 0 {
		return nil, ErrNoPlayableSource
	}
	if err := validateTrickplayJPEG(contents, width); err != nil {
		return nil, err
	}
	return contents, nil
}
