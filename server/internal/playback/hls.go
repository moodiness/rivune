package playback

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	defaultMediaStorageBytes = int64(20 * 1024 * 1024 * 1024)
	defaultMediaIdleTTL      = 2 * time.Minute
	hlsReadyTimeout          = 45 * time.Second
	hlsRetainedSegments      = 120
)

var localMediaName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type MediaOptions struct {
	TempDirectory   string
	MaxStorageBytes int64
	IdleTTL         time.Duration
}

type HLSProcessor interface {
	ProcessHLS(context.Context, storedAsset, string) error
	ConvertSubtitle(context.Context, storedAsset, io.Writer) error
}

type hlsJob struct {
	directory   string
	fingerprint string
	cancel      context.CancelFunc
	done        chan struct{}
	timer       *time.Timer
	mu          sync.RWMutex
	err         error
}

func normalizeMediaOptions(options MediaOptions) MediaOptions {
	if strings.TrimSpace(options.TempDirectory) == "" {
		options.TempDirectory = os.TempDir()
	}
	options.TempDirectory = filepath.Join(options.TempDirectory, "rivune-media")
	if options.MaxStorageBytes <= 0 {
		options.MaxStorageBytes = defaultMediaStorageBytes
	}
	if options.IdleTTL <= 0 {
		options.IdleTTL = defaultMediaIdleTTL
	}
	return options
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
	var output bytes.Buffer
	if err := processor.ConvertSubtitle(r.Context(), asset, &output); err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(output.Len()))
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(output.Bytes())
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
	job := &hlsJob{
		directory: directory, fingerprint: hlsAssetFingerprint(asset), cancel: cancel, done: make(chan struct{}),
	}
	service.hlsJobs[key] = job
	service.hlsMu.Unlock()

	job.timer = time.AfterFunc(service.mediaOptions.IdleTTL, func() { service.stopHLSJob(key) })
	go service.runHLSJob(jobContext, job, asset, processor)
	go service.monitorHLSStorage(jobContext, job)
	return job, nil
}

func (service *Service) prewarmHLS(ctx context.Context, sourceRef string, source Source, asset *storedAsset) error {
	if asset == nil || source.Protocol != "hls" || source.Mode == "direct" {
		return nil
	}
	processor, ok := service.processor.(HLSProcessor)
	if !ok {
		return ErrMediaProcessingFailed
	}
	job, err := service.hlsJob(prewarmHLSSession(sourceRef), *asset, processor, true)
	if err != nil {
		return err
	}
	if err := waitForMediaFile(ctx, job, filepath.Join(job.directory, "index.m3u8")); err != nil {
		return err
	}
	service.touchHLSJob(job)
	return nil
}

func (service *Service) startSessionHLS(sourceRef, sessionID string, sources []Source, assets []storedAsset) error {
	for _, source := range sources {
		if !source.Compatible || source.Protocol != "hls" || source.Mode == "direct" {
			continue
		}
		assetIndex := storedAssetIndex(assets, source.ID)
		if assetIndex < 0 {
			return ErrMediaProcessingFailed
		}
		asset := assets[assetIndex]
		if service.adoptHLSJob(prewarmHLSSession(sourceRef), sessionID, asset) {
			return nil
		}
		processor, ok := service.processor.(HLSProcessor)
		if !ok {
			return ErrMediaProcessingFailed
		}
		_, err := service.hlsJob(sessionID, asset, processor, true)
		return err
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
	if job.timer != nil {
		job.timer.Stop()
	}
	delete(service.hlsJobs, fromKey)
	service.hlsJobs[toKey] = job
	job.timer = time.AfterFunc(service.mediaOptions.IdleTTL, func() { service.stopHLSJob(toKey) })
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

func prewarmHLSSession(sourceRef string) string {
	return "prewarm-" + sourceRef
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
	return fmt.Sprintf("%s|%s|%t|%d|%s", mediaProbeKey(asset), asset.Kind, asset.ToneMap, audioTrack, hlsStartKey(asset.StartSeconds))
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
	if job.timer != nil {
		job.timer.Stop()
	}
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
	if len(contents) > maximumPlaylistBytes {
		return nil, fmt.Errorf("playlist exceeds %d bytes", maximumPlaylistBytes)
	}
	var output bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			line = playlistURIAttribute.ReplaceAllStringFunc(line, func(match string) string {
				reference := strings.TrimSuffix(strings.TrimPrefix(match, `URI="`), `"`)
				if !localMediaName.MatchString(reference) {
					return match
				}
				return `URI="` + buildURL(reference) + `"`
			})
		} else if strings.TrimSpace(line) != "" {
			reference := strings.TrimSpace(line)
			if !localMediaName.MatchString(reference) {
				return nil, ErrMediaProcessingFailed
			}
			line = buildURL(reference)
		}
		output.WriteString(line)
		output.WriteByte('\n')
		if line == "#EXTM3U" {
			output.WriteString("#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan local playlist: %w", err)
	}
	return output.Bytes(), nil
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
