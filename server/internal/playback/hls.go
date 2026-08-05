package playback

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultMediaStorageBytes         = int64(20 * 1024 * 1024 * 1024)
	defaultMediaIdleTTL              = 2 * time.Minute
	defaultTranscodeVideoBitrateKbps = 12000
	hlsReadyTimeout                  = 45 * time.Second
	hlsRetainedSegments              = 120
)

var localMediaName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type MediaOptions struct {
	TempDirectory             string
	MaxStorageBytes           int64
	IdleTTL                   time.Duration
	TranscodeVideoBitrateKbps int
}

type HLSProcessor interface {
	ProcessHLS(context.Context, storedAsset, string) error
	ConvertSubtitle(context.Context, storedAsset, io.Writer) error
}

type hlsJob struct {
	directory             string
	fingerprint           string
	sessionID             string
	assetID               string
	mode                  string
	prewarming            bool
	sourceDurationSeconds float64
	startOffsetSeconds    float64
	createdAt             time.Time
	lastAccessed          time.Time
	cancel                context.CancelFunc
	done                  chan struct{}
	timer                 *time.Timer
	mu                    sync.RWMutex
	err                   error
}

func normalizeMediaOptions(options MediaOptions) MediaOptions {
	if strings.TrimSpace(options.TempDirectory) == "" {
		options.TempDirectory = os.TempDir()
	}
	options.TempDirectory = filepath.Join(options.TempDirectory, "rivune-media")
	if options.MaxStorageBytes <= 0 {
		options.MaxStorageBytes = defaultMediaStorageBytes
	}
	if options.TranscodeVideoBitrateKbps <= 0 {
		options.TranscodeVideoBitrateKbps = defaultTranscodeVideoBitrateKbps
	}
	if options.IdleTTL <= 0 {
		options.IdleTTL = defaultMediaIdleTTL
	}
	return options
}

func (service *Service) currentTime() time.Time {
	if service.now != nil {
		return service.now()
	}
	return time.Now().UTC()
}

func (service *Service) proxyConvertedSubtitle(w http.ResponseWriter, r *http.Request, asset storedAsset) error {
	processor, ok := service.processor.(HLSProcessor)
	if !ok {
		return ErrMediaProcessingFailed
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		return nil
	}
	conversionContext, cancel := context.WithTimeout(r.Context(), subtitleConversionTimeout)
	defer cancel()
	var converted bytes.Buffer
	output := &maximumWriter{destination: &converted, remaining: maximumConvertedSubtitleBytes}
	if err := processor.ConvertSubtitle(conversionContext, asset, output); err != nil {
		return err
	}
	if output.exceeded {
		return fmt.Errorf("%w: converted subtitle exceeds %d bytes", ErrMediaProcessingFailed, maximumConvertedSubtitleBytes)
	}
	if err := conversionContext.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrMediaProcessingFailed, err)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(converted.Len()))
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(converted.Bytes())
	return err
}

func (service *Service) serveHLS(w http.ResponseWriter, r *http.Request, sessionID, token string, asset storedAsset) error {
	processor, ok := service.processor.(HLSProcessor)
	if !ok {
		return ErrMediaProcessingFailed
	}
	name := strings.TrimSpace(r.URL.Query().Get("file"))
	if !localMediaName.MatchString(name) {
		return ErrSessionNotFound
	}
	job, err := service.hlsJob(sessionID, asset, processor, name == "index.m3u8")
	if err != nil {
		return err
	}
	service.touchHLSJob(job)
	path := filepath.Join(job.directory, name)
	if err := waitForMediaFile(r.Context(), job, path); err != nil {
		return err
	}
	if strings.HasSuffix(name, ".m3u8") {
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("%w: read playlist: %v", ErrMediaProcessingFailed, err)
		}
		rewritten, err := rewriteLocalPlaylist(contents, func(reference string) string {
			return hlsAssetURLAt(sessionID, asset.ID, token, reference, asset.StartSeconds)
		})
		if err != nil {
			return err
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(rewritten)))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(rewritten)
		}
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%w: open media segment: %v", ErrMediaProcessingFailed, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("%w: stat media segment: %v", ErrMediaProcessingFailed, err)
	}
	contentType := mime.TypeByExtension(filepath.Ext(name))
	if contentType == "" {
		if strings.HasSuffix(name, ".m4s") || strings.HasSuffix(name, ".mp4") {
			contentType = "video/mp4"
		} else {
			contentType = "application/octet-stream"
		}
	}
	w.Header().Set("Cache-Control", "private, max-age=3600, immutable")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, info.ModTime(), file)
	pruneHLSSegments(job.directory, name)
	return nil
}

func (service *Service) hlsJob(sessionID string, asset storedAsset, processor HLSProcessor, mayStart bool) (*hlsJob, error) {
	key := hlsJobKey(sessionID, asset)
	if mayStart {
		service.stopOtherHLSGenerations(hlsJobPrefix(sessionID, asset.ID), key)
	}
	service.hlsMu.Lock()
	if existing := service.hlsJobs[key]; existing != nil {
		service.hlsMu.Unlock()
		return existing, nil
	}
	if !mayStart {
		service.hlsMu.Unlock()
		return nil, ErrSessionNotFound
	}
	if directorySize(service.mediaOptions.TempDirectory) >= service.mediaOptions.MaxStorageBytes {
		service.hlsMu.Unlock()
		return nil, ErrMediaStorageLimit
	}
	directory := filepath.Join(service.mediaOptions.TempDirectory, sessionID, asset.ID, "start-"+hlsStartKey(asset.StartSeconds))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		service.hlsMu.Unlock()
		return nil, fmt.Errorf("%w: create media workspace: %v", ErrMediaProcessingFailed, err)
	}
	jobContext, cancel := context.WithCancel(context.Background())
	now := service.currentTime()
	job := &hlsJob{
		directory: directory, fingerprint: hlsAssetFingerprint(asset), sessionID: sessionID, assetID: asset.ID,
		mode: asset.Kind, prewarming: strings.HasPrefix(sessionID, "prewarm-"), sourceDurationSeconds: asset.DurationSeconds,
		startOffsetSeconds: asset.StartSeconds, createdAt: now, lastAccessed: now, cancel: cancel, done: make(chan struct{}),
	}
	service.hlsJobs[key] = job
	service.hlsMu.Unlock()

	job.timer = time.AfterFunc(service.mediaOptions.IdleTTL, func() { service.stopHLSJob(key) })
	go service.runHLSJob(jobContext, job, asset, processor)
	go service.monitorHLSStorage(jobContext, job)
	return job, nil
}

func hlsPlaylistEncodedSeconds(directory string) (float64, bool) {
	file, err := os.Open(filepath.Join(directory, "index.m3u8"))
	if err != nil {
		return 0, false
	}
	defer file.Close()

	const prefix = "#EXTINF:"
	var total float64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, []byte(prefix)) {
			continue
		}
		value := line[len(prefix):]
		comma := bytes.IndexByte(value, ',')
		if comma < 0 {
			continue
		}
		value = value[:comma]
		seconds, err := strconv.ParseFloat(string(bytes.TrimSpace(value)), 64)
		if err != nil || seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			continue
		}
		total += seconds
		if math.IsInf(total, 0) {
			return 0, false
		}
	}
	if scanner.Err() != nil {
		return 0, false
	}
	return total, true
}

func (service *Service) prewarmHLS(ctx context.Context, prewarmSessionID string, source Source, asset *storedAsset) error {
	if asset == nil || source.Protocol != "hls" || source.Mode == "direct" {
		return nil
	}
	processor, ok := service.processor.(HLSProcessor)
	if !ok {
		return ErrMediaProcessingFailed
	}
	service.stopOtherHLSGenerations(prewarmSessionID+"/", hlsJobKey(prewarmSessionID, *asset))
	job, err := service.hlsJob(prewarmSessionID, *asset, processor, true)
	if err != nil {
		return err
	}
	if err := waitForMediaFile(ctx, job, filepath.Join(job.directory, "index.m3u8")); err != nil {
		service.stopHLSJob(hlsJobKey(prewarmSessionID, *asset))
		return err
	}
	service.touchHLSJob(job)
	return nil
}

func (service *Service) startSessionHLS(ctx context.Context, prewarmSessionID, sessionID string, sources []Source, assets []storedAsset) error {
	for _, source := range sources {
		if !source.Compatible || source.Protocol != "hls" || source.Mode == "direct" {
			continue
		}
		assetIndex := storedAssetIndex(assets, source.ID)
		if assetIndex < 0 {
			return ErrMediaProcessingFailed
		}
		asset := assets[assetIndex]
		if service.adoptHLSJob(prewarmSessionID, sessionID, asset) {
			return nil
		}
		processor, ok := service.processor.(HLSProcessor)
		if !ok {
			return ErrMediaProcessingFailed
		}
		job, err := service.hlsJob(sessionID, asset, processor, true)
		if err != nil {
			return err
		}
		if err := waitForMediaFile(ctx, job, filepath.Join(job.directory, "index.m3u8")); err != nil {
			service.stopHLSJob(hlsJobKey(sessionID, asset))
			return err
		}
		service.touchHLSJob(job)
		return nil
	}
	return nil
}

func (service *Service) adoptHLSJob(fromSessionID, toSessionID string, asset storedAsset) bool {
	fromKey := hlsJobKey(fromSessionID, asset)
	toKey := hlsJobKey(toSessionID, asset)
	service.hlsMu.Lock()
	defer service.hlsMu.Unlock()
	job := service.hlsJobs[fromKey]
	if job == nil || job.fingerprint != hlsAssetFingerprint(asset) || service.hlsJobs[toKey] != nil {
		return false
	}
	job.mu.Lock()
	if job.timer != nil {
		job.timer.Stop()
	}
	job.sessionID = toSessionID
	job.prewarming = false
	job.lastAccessed = service.currentTime()
	delete(service.hlsJobs, fromKey)
	service.hlsJobs[toKey] = job
	job.timer = time.AfterFunc(service.mediaOptions.IdleTTL, func() { service.stopHLSJob(toKey) })
	job.mu.Unlock()
	return true
}

func (service *Service) stopOtherHLSGenerations(prefix, keep string) {
	service.hlsMu.Lock()
	keys := make([]string, 0)
	for key := range service.hlsJobs {
		if key != keep && strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	service.hlsMu.Unlock()
	for _, key := range keys {
		service.stopHLSJob(key)
	}
}

func prewarmHLSSession(authSessionID, profileID string) string {
	return "prewarm-" + authSessionID + "-" + profileID
}

func hlsJobPrefix(sessionID, assetID string) string {
	return sessionID + "/" + assetID + "/"
}

func hlsJobKey(sessionID string, asset storedAsset) string {
	return hlsJobPrefix(sessionID, asset.ID) + hlsStartKey(asset.StartSeconds)
}

func hlsStartKey(startSeconds float64) string {
	return strconv.FormatInt(int64(startSeconds), 10)
}

func hlsAssetFingerprint(asset storedAsset) string {
	audioTrack := -1
	if asset.AudioTrackIndex != nil {
		audioTrack = *asset.AudioTrackIndex
	}
	subtitleTrack := -1
	if asset.SubtitleTrackIndex != nil {
		subtitleTrack = *asset.SubtitleTrackIndex
	}
	return fmt.Sprintf(
		"%s|%s|%t|%d|%d|%d|%d|%d|%s",
		mediaProbeKey(asset), asset.Kind, asset.ToneMap, audioTrack, subtitleTrack,
		asset.TargetHeight, asset.VideoBitrateKbps, asset.MaximumAudioChannels, hlsStartKey(asset.StartSeconds),
	)
}

func (service *Service) runHLSJob(ctx context.Context, job *hlsJob, asset storedAsset, processor HLSProcessor) {
	err := processor.ProcessHLS(ctx, asset, job.directory)
	job.mu.Lock()
	if job.err == nil && err != nil && !errors.Is(err, context.Canceled) {
		job.err = err
	}
	job.mu.Unlock()
	close(job.done)
}

func (service *Service) monitorHLSStorage(ctx context.Context, job *hlsJob) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-job.done:
			return
		case <-ticker.C:
			if directorySize(service.mediaOptions.TempDirectory) <= service.mediaOptions.MaxStorageBytes {
				continue
			}
			job.mu.Lock()
			job.err = ErrMediaStorageLimit
			job.mu.Unlock()
			job.cancel()
			return
		}
	}
}

func (service *Service) touchHLSJob(job *hlsJob) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.lastAccessed = service.currentTime()
	if job.timer != nil {
		job.timer.Reset(service.mediaOptions.IdleTTL)
	}
}

func (service *Service) stopHLSJob(key string) {
	service.hlsMu.Lock()
	job := service.hlsJobs[key]
	if job != nil {
		delete(service.hlsJobs, key)
	}
	service.hlsMu.Unlock()
	if job == nil {
		return
	}
	job.mu.Lock()
	if job.timer != nil {
		job.timer.Stop()
	}
	job.mu.Unlock()
	job.cancel()
	select {
	case <-job.done:
	case <-time.After(5 * time.Second):
	}
	_ = os.RemoveAll(job.directory)
	_ = removeEmptyParents(filepath.Dir(job.directory), service.mediaOptions.TempDirectory)
}

func (service *Service) stopHLSSession(sessionID string) {
	prefix := sessionID + "/"
	service.hlsMu.Lock()
	keys := make([]string, 0)
	for key := range service.hlsJobs {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	service.hlsMu.Unlock()
	for _, key := range keys {
		service.stopHLSJob(key)
	}
}

func waitForMediaFile(ctx context.Context, job *hlsJob, path string) error {
	timer := time.NewTimer(hlsReadyTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return nil
		}
		job.mu.RLock()
		jobErr := job.err
		job.mu.RUnlock()
		if jobErr != nil {
			return jobErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-job.done:
			job.mu.RLock()
			jobErr = job.err
			job.mu.RUnlock()
			if jobErr != nil {
				return jobErr
			}
			return ErrMediaSourceFailed
		case <-timer.C:
			return ErrMediaSourceFailed
		case <-ticker.C:
		}
	}
}

func rewriteLocalPlaylist(contents []byte, buildURL func(string) string) ([]byte, error) {
	if err := validatePlaylistCardinality(contents); err != nil {
		return nil, fmt.Errorf("%w: invalid local HLS playlist: %w", ErrMediaProcessingFailed, err)
	}
	output := newBoundedPlaylistOutput(len(contents))
	scanner := playlistScanner(contents)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			err := writePlaylistURIAttributes(output, line, func(reference string) (string, bool) {
				if !localMediaName.MatchString(reference) {
					return "", false
				}
				return buildURL(reference), true
			})
			if err != nil {
				return nil, fmt.Errorf("%w: invalid local HLS playlist: %w", ErrMediaProcessingFailed, err)
			}
		} else if strings.TrimSpace(line) != "" {
			reference := strings.TrimSpace(line)
			if !localMediaName.MatchString(reference) {
				return nil, ErrMediaProcessingFailed
			}
			if err := output.writeString(buildURL(reference)); err != nil {
				return nil, fmt.Errorf("%w: invalid local HLS playlist: %w", ErrMediaProcessingFailed, err)
			}
		} else if err := output.writeString(line); err != nil {
			return nil, fmt.Errorf("%w: invalid local HLS playlist: %w", ErrMediaProcessingFailed, err)
		}
		if err := output.writeByte('\n'); err != nil {
			return nil, fmt.Errorf("%w: invalid local HLS playlist: %w", ErrMediaProcessingFailed, err)
		}
		if line == "#EXTM3U" {
			if err := output.writeString("#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n"); err != nil {
				return nil, fmt.Errorf("%w: invalid local HLS playlist: %w", ErrMediaProcessingFailed, err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: invalid local HLS playlist: %w", ErrMediaProcessingFailed, err)
	}
	return output.bytes(), nil
}

func hlsAssetURL(sessionID, assetID, token, file string) string {
	return hlsAssetURLAt(sessionID, assetID, token, file, 0)
}

func hlsAssetURLAt(sessionID, assetID, token, file string, startSeconds float64) string {
	values := url.Values{"token": []string{token}, "file": []string{file}}
	if startSeconds > 0 {
		values.Set("start", hlsStartKey(startSeconds))
	}
	return "/api/v1/playback/sessions/" + url.PathEscape(sessionID) + "/assets/" + url.PathEscape(assetID) + "?" + values.Encode()
}
func pruneHLSSegments(directory, currentName string) {
	current, ok := hlsSegmentIndex(currentName)
	if !ok || current < hlsRetainedSegments {
		return
	}
	cutoff := current - hlsRetainedSegments + 1
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		index, segment := hlsSegmentIndex(entry.Name())
		if segment && index < cutoff {
			_ = os.Remove(filepath.Join(directory, entry.Name()))
		}
	}
}

func hlsSegmentIndex(name string) (int, bool) {
	if !strings.HasPrefix(name, "segment-") || !strings.HasSuffix(name, ".m4s") {
		return 0, false
	}
	value := strings.TrimSuffix(strings.TrimPrefix(name, "segment-"), ".m4s")
	index, err := strconv.Atoi(value)
	return index, err == nil && index >= 0
}

func directorySize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if info, infoErr := entry.Info(); infoErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func removeEmptyParents(directory, stop string) error {
	for directory != stop && strings.HasPrefix(directory, stop+string(os.PathSeparator)) {
		err := os.Remove(directory)
		if err != nil {
			return err
		}
		directory = filepath.Dir(directory)
	}
	return nil
}
