package playback

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	hlsInitialBufferSeconds          = 6
	hlsSegmentDurationSeconds        = 3
	hlsRetainedSegments              = 120
	hlsDeleteThreshold               = 1
	hlsSharedWorkerSafetySegments    = 10
	hlsSeekAheadToleranceSegments    = 2
	hlsSeekableSegmentPrefix         = "seek-"
)

var localMediaName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type MediaOptions struct {
	TempDirectory             string
	MaxStorageBytes           int64
	IdleTTL                   time.Duration
	TranscodeVideoBitrateKbps int
	InitialBufferSeconds      int
}

type HLSProcessor interface {
	ProcessHLS(context.Context, storedAsset, string) error
	ConvertSubtitle(context.Context, storedAsset, io.Writer) error
}

type hlsJob struct {
	directory              string
	fingerprint            string
	sessionID              string
	assetID                string
	mode                   string
	segmentContainer       string
	prewarming             bool
	sourceDurationSeconds  float64
	startOffsetSeconds     float64
	createdAt              time.Time
	lastAccessed           time.Time
	cancel                 context.CancelFunc
	done                   chan struct{}
	timer                  *time.Timer
	bindings               map[string]*hlsJobBinding
	stopOnce               sync.Once
	activeRequests         int
	activeRequestsDone     chan struct{}
	stopWhenIdle           bool
	activePreparations     int
	activePreparationsDone chan struct{}
	mu                     sync.RWMutex
	err                    error
}

type hlsJobBinding struct {
	prewarming bool
	timer      *time.Timer
}

type hlsSeekGate struct {
	token chan struct{}
	users int
}

type hlsJobRequest struct {
	service  *Service
	job      *hlsJob
	promoted bool
	released bool
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
	if options.InitialBufferSeconds < 3 || options.InitialBufferSeconds > 30 {
		options.InitialBufferSeconds = hlsInitialBufferSeconds
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

// ApplyMediaStorageLimit synchronously reclaims existing HLS victims before
// publishing a lower active ceiling. A failed reclaim leaves the previous
// ceiling active; reclaimed jobs are intentionally not resurrected.
func (service *Service) ApplyMediaStorageLimit(ctx context.Context, limit int64) error {
	if service == nil || limit <= 0 {
		return errors.New("media storage limit must be positive")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	service.hlsResetMu.Lock()
	defer service.hlsResetMu.Unlock()
	service.hlsStorageMu.Lock()
	defer service.hlsStorageMu.Unlock()
	current := service.mediaStorageLimit()
	if limit < current && !service.reclaimHLSStorageLocked(limit, false) {
		return ErrMediaStorageLimit
	}
	service.resizeTrickplayCache(limit)
	service.mediaStorageBytes.Store(limit)
	return nil
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

func (service *Service) serveHLS(w http.ResponseWriter, r *http.Request, sessionID, token string, asset storedAsset, buildChildURL func(deliveryChildState) (string, error)) error {
	processor, ok := service.processor.(HLSProcessor)
	if !ok {
		return ErrMediaProcessingFailed
	}
	name := strings.TrimSpace(r.URL.Query().Get("file"))
	if !localMediaName.MatchString(name) {
		return ErrSessionNotFound
	}
	seekableSegments, seekable := seekableHLSSegmentCount(asset)
	if strings.HasPrefix(name, hlsSeekableSegmentPrefix) {
		index, valid := seekableHLSSegmentIndex(name, seekableSegments)
		if !seekable || !valid {
			return ErrSessionNotFound
		}
		return service.serveSeekableHLSSegment(w, r, sessionID, asset, processor, index)
	}

	isMaster := name == "master.m3u8"
	var job *hlsJob
	var jobRequest *hlsJobRequest
	var err error
	if seekable && (isMaster || name == "index.m3u8") {
		job, jobRequest, err = service.hlsPlaylistJob(r.Context(), sessionID, asset, processor)
	} else {
		job, err = service.hlsJob(r.Context(), sessionID, asset, processor, isMaster || name == "index.m3u8")
	}
	if err != nil {
		return err
	}
	if jobRequest == nil {
		jobRequest = service.retainHLSJobRequest(hlsJobKey(sessionID, asset), job)
		if jobRequest == nil {
			return ErrSessionNotFound
		}
	}
	defer jobRequest.release()
	path := filepath.Join(job.directory, name)
	if isMaster {
		path = filepath.Join(job.directory, "index.m3u8")
	}
	if err := waitForMediaFile(r.Context(), job, path); err != nil {
		return err
	}
	if (isMaster || name == "index.m3u8") && r.Method == http.MethodGet {
		if err := waitForHLSBuffer(r.Context(), job, float64(service.mediaOptions.InitialBufferSeconds)); err != nil {
			return err
		}
	}
	if isMaster {
		childURL := hlsAssetURLAt(sessionID, asset.ID, token, "index.m3u8", asset.StartSeconds)
		if buildChildURL != nil {
			var err error
			childURL, err = buildChildURL(deliveryChildState{
				assetID: asset.ID, file: "index.m3u8", start: hlsStartKey(asset.StartSeconds), retainWhileActive: true,
			})
			if err != nil {
				return fmt.Errorf("%w: create local HLS media capability: %v", ErrMediaProcessingFailed, err)
			}
		}
		version := 3
		if normalizedHLSSegmentContainer(asset.HLSSegmentContainer) == "mp4" {
			version = 7
		}
		bandwidth := asset.VideoBitrateKbps*1000 + 256000
		audioOnly := asset.Decision != nil && asset.Decision.Target != nil &&
			normalizedCodec(asset.Decision.Target.VideoCodec) == "" && normalizedCodec(asset.Decision.Target.AudioCodec) != ""
		if bandwidth <= 256000 && !audioOnly {
			bandwidth = defaultTranscodeVideoBitrateKbps*1000 + 256000
		}
		streamInformation := fmt.Sprintf("BANDWIDTH=%d,AVERAGE-BANDWIDTH=%d", bandwidth, bandwidth)
		if codecs, ok := localHLSRFC6381Codecs(asset.Decision); ok {
			streamInformation += `,CODECS="` + codecs + `"`
		}
		playlist := []byte(fmt.Sprintf("#EXTM3U\n#EXT-X-VERSION:%d\n#EXT-X-STREAM-INF:%s\n%s\n", version, streamInformation, childURL))
		return writeHLSPlaylist(w, r, playlist)
	}

	if strings.HasSuffix(name, ".m3u8") {
		var contents []byte
		var err error
		if seekable && name == "index.m3u8" {
			contents, err = seekableHLSPlaylist(asset, seekableSegments)
		} else {
			contents, err = os.ReadFile(path)
		}
		if err != nil {
			return fmt.Errorf("%w: read playlist: %v", ErrMediaProcessingFailed, err)
		}
		retainMediaSegments := playlistChildrenRetainWhileActive(contents)
		var childErr error
		rewritten, err := rewriteLocalPlaylistWithReferencePolicy(contents, buildChildURL != nil, func(reference string, mediaSegment bool) string {
			if childErr != nil {
				return ""
			}
			startSeconds := asset.StartSeconds
			if seekable && name == "index.m3u8" {
				index, valid := seekableHLSSegmentIndex(reference, seekableSegments)
				if !valid {
					childErr = ErrMediaProcessingFailed
					return ""
				}
				startSeconds = float64(index * hlsSegmentDurationSeconds)
			}
			if buildChildURL == nil {
				return hlsAssetURLAt(sessionID, asset.ID, token, reference, startSeconds)
			}
			var childURL string
			childURL, childErr = buildChildURL(deliveryChildState{
				assetID: asset.ID, file: reference, start: hlsStartKey(startSeconds),
				retainWhileActive: !mediaSegment || retainMediaSegments,
			})
			return childURL
		})
		if childErr != nil {
			return fmt.Errorf("%w: create local HLS child capability: %v", ErrMediaProcessingFailed, childErr)
		}
		if err != nil {
			return err
		}
		return writeHLSPlaylist(w, r, rewritten)
	}
	return serveLocalHLSFile(w, r, path, name, true, jobRequest.promote)
}

func serveLocalHLSFile(w http.ResponseWriter, r *http.Request, path, name string, immutable bool, afterOpen func()) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%w: open media segment: %v", ErrMediaProcessingFailed, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("%w: stat media segment: %v", ErrMediaProcessingFailed, err)
	}
	if afterOpen != nil {
		afterOpen()
	}
	contentType := mime.TypeByExtension(filepath.Ext(name))
	if strings.HasSuffix(name, ".ts") {
		contentType = "video/mp2t"
	} else if strings.HasSuffix(name, ".m4s") || strings.HasSuffix(name, ".mp4") {
		contentType = "video/mp4"
	} else if contentType == "" {
		contentType = "application/octet-stream"
	}
	if immutable {
		w.Header().Set("Cache-Control", "private, max-age=3600, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, info.ModTime(), file)
	return nil
}

func hlsGenerationStart(asset storedAsset, requested float64) float64 {
	if _, seekable := seekableHLSSegmentCount(asset); !seekable || requested <= 0 {
		return requested
	}
	return math.Floor(requested/float64(hlsSegmentDurationSeconds)) * float64(hlsSegmentDurationSeconds)
}

func seekableHLSSegmentCount(asset storedAsset) (int, bool) {
	if asset.Kind != processingTranscode || normalizedHLSSegmentContainer(asset.HLSSegmentContainer) != "ts" ||
		asset.DurationSeconds <= 0 || math.IsNaN(asset.DurationSeconds) || math.IsInf(asset.DurationSeconds, 0) {
		return 0, false
	}
	count := int(math.Ceil(asset.DurationSeconds / float64(hlsSegmentDurationSeconds)))
	return count, count > 0 && count <= maximumPlaylistReferences
}

func seekableHLSPlaylist(asset storedAsset, segments int) ([]byte, error) {
	if expected, ok := seekableHLSSegmentCount(asset); !ok || expected != segments {
		return nil, ErrMediaProcessingFailed
	}
	var playlist strings.Builder
	playlist.Grow(segments*48 + 128)
	playlist.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-TARGETDURATION:3\n#EXT-X-MEDIA-SEQUENCE:0\n")
	if asset.StartSeconds > 0 && asset.StartSeconds < asset.DurationSeconds {
		_, _ = fmt.Fprintf(&playlist, "#EXT-X-START:TIME-OFFSET=%.6f,PRECISE=YES\n", asset.StartSeconds)
	}
	for index := range segments {
		duration := math.Min(float64(hlsSegmentDurationSeconds), asset.DurationSeconds-float64(index*hlsSegmentDurationSeconds))
		if duration <= 0 {
			return nil, ErrMediaProcessingFailed
		}
		_, _ = fmt.Fprintf(&playlist, "#EXTINF:%.6f,\n%s%06d.ts\n", duration, hlsSeekableSegmentPrefix, index)
	}
	playlist.WriteString("#EXT-X-ENDLIST\n")
	return []byte(playlist.String()), nil
}

func seekableHLSSegmentIndex(name string, segments int) (int, bool) {
	if segments <= 0 || !strings.HasPrefix(name, hlsSeekableSegmentPrefix) || !strings.HasSuffix(name, ".ts") {
		return 0, false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(name, hlsSeekableSegmentPrefix), ".ts")
	index, err := strconv.Atoi(raw)
	if err != nil || index < 0 || index >= segments || name != fmt.Sprintf("%s%06d.ts", hlsSeekableSegmentPrefix, index) {
		return 0, false
	}
	return index, true
}

func (service *Service) activeHLSJobRequest(sessionID, assetID string) (*hlsJob, *hlsJobRequest) {
	service.hlsResetMu.RLock()
	defer service.hlsResetMu.RUnlock()
	prefix := hlsJobPrefix(sessionID, assetID)
	service.hlsMu.Lock()
	defer service.hlsMu.Unlock()
	var selected *hlsJob
	selectedKey := ""
	for key, job := range service.hlsJobs {
		if strings.HasPrefix(key, prefix) && (selected == nil || job.createdAt.After(selected.createdAt)) {
			selected = job
			selectedKey = key
		}
	}
	if selected == nil {
		return nil, nil
	}
	return selected, service.retainHLSJobRequestLocked(selectedKey, selected)
}

func (service *Service) hlsPlaylistJob(ctx context.Context, sessionID string, asset storedAsset, processor HLSProcessor) (*hlsJob, *hlsJobRequest, error) {
	release, err := service.acquireHLSSeekGate(ctx, hlsJobPrefix(sessionID, asset.ID))
	if err != nil {
		return nil, nil, err
	}
	defer release()
	for {
		if job, request := service.activeHLSJobRequest(sessionID, asset.ID); job != nil {
			return job, request, nil
		}
		asset.StartSeconds = hlsGenerationStart(asset, asset.StartSeconds)
		job, jobErr := service.hlsJob(ctx, sessionID, asset, processor, true)
		if jobErr != nil {
			return nil, nil, jobErr
		}
		if request := service.retainHLSJobRequest(hlsJobKey(sessionID, asset), job); request != nil {
			return job, request, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
	}
}

func (service *Service) serveSeekableHLSSegment(w http.ResponseWriter, r *http.Request, sessionID string, asset storedAsset, processor HLSProcessor, index int) error {
	targetSeconds := float64(index * hlsSegmentDurationSeconds)
	job, localIndex, jobRequest, err := service.hlsJobForSeekTarget(r.Context(), sessionID, asset, processor, targetSeconds)
	if err != nil {
		return err
	}
	defer jobRequest.release()
	name := fmt.Sprintf("segment-%06d.ts", localIndex)
	path := filepath.Join(job.directory, name)
	if err := waitForMediaFile(r.Context(), job, path); err != nil {
		return err
	}
	return serveLocalHLSFile(w, r, path, name, false, jobRequest.promote)
}

func (service *Service) hlsJobForSeekTarget(ctx context.Context, sessionID string, asset storedAsset, processor HLSProcessor, targetSeconds float64) (*hlsJob, int, *hlsJobRequest, error) {
	gateKey := hlsJobPrefix(sessionID, asset.ID)
	release, err := service.acquireHLSSeekGate(ctx, gateKey)
	if err != nil {
		return nil, 0, nil, err
	}
	defer release()
selectGeneration:

	type registeredHLSJob struct {
		key string
		job *hlsJob
	}
	service.hlsMu.Lock()
	jobs := make([]registeredHLSJob, 0, 1)
	for key, job := range service.hlsJobs {
		if strings.HasPrefix(key, gateKey) {
			jobs = append(jobs, registeredHLSJob{key: key, job: job})
		}
	}
	service.hlsMu.Unlock()

	var selected *hlsJob
	selectedKey := ""
	selectedIndex := 0
	for _, registered := range jobs {
		job := registered.job
		job.mu.RLock()
		startSeconds := job.startOffsetSeconds
		directory := job.directory
		done := job.done
		job.mu.RUnlock()
		delta := targetSeconds - startSeconds
		if delta < 0 {
			continue
		}
		indexValue := delta / float64(hlsSegmentDurationSeconds)
		localIndex := int(math.Round(indexValue))
		if math.Abs(indexValue-float64(localIndex)) > 0.001 {
			continue
		}
		first, last, bounded := hlsPlaylistSegmentBounds(directory)
		finished := false
		select {
		case <-done:
			finished = true
		default:
		}
		if bounded {
			if localIndex < first || localIndex > last+hlsSeekAheadToleranceSegments || finished && localIndex > last {
				continue
			}
		} else if localIndex > hlsSeekAheadToleranceSegments || finished {
			continue
		}
		if selected == nil || startSeconds > selected.startOffsetSeconds {
			selected = job
			selectedIndex = localIndex
			selectedKey = registered.key
		}
	}
	if selected != nil {
		if request := service.retainHLSJobRequest(selectedKey, selected); request != nil {
			return selected, selectedIndex, request, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, 0, nil, err
		}
		goto selectGeneration
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, nil, err
	}

	seekAsset := asset
	seekAsset.StartSeconds = targetSeconds
	replacementKey := hlsJobKey(sessionID, seekAsset)
	// A same-start generation may have permanently deleted the requested segment.
	if err := service.stopHLSJobAfterPreparations(ctx, replacementKey); err != nil {
		return nil, 0, nil, err
	}
	job, err := service.hlsJob(ctx, sessionID, seekAsset, processor, true)
	if err != nil {
		return nil, 0, nil, err
	}
	if err := ctx.Err(); err != nil {
		service.stopHLSJob(hlsJobKey(sessionID, seekAsset))
		return nil, 0, nil, err
	}
	if request := service.retainHLSJobRequest(hlsJobKey(sessionID, seekAsset), job); request != nil {
		return job, 0, request, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, nil, err
	}
	goto selectGeneration
}

func (service *Service) acquireHLSSeekGate(ctx context.Context, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	service.hlsSeekMu.Lock()
	if service.hlsSeekGates == nil {
		service.hlsSeekGates = make(map[string]*hlsSeekGate)
	}
	gate := service.hlsSeekGates[key]
	if gate == nil {
		gate = &hlsSeekGate{token: make(chan struct{}, 1)}
		service.hlsSeekGates[key] = gate
	}
	gate.users++
	service.hlsSeekMu.Unlock()

	select {
	case gate.token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate.token
			service.releaseHLSSeekGate(key, gate)
			return nil, err
		}
	case <-ctx.Done():
		service.releaseHLSSeekGate(key, gate)
		return nil, ctx.Err()
	}
	return func() {
		<-gate.token
		service.releaseHLSSeekGate(key, gate)
	}, nil
}

func (service *Service) releaseHLSSeekGate(key string, gate *hlsSeekGate) {
	service.hlsSeekMu.Lock()
	if current := service.hlsSeekGates[key]; current == gate {
		gate.users--
		if gate.users == 0 {
			delete(service.hlsSeekGates, key)
		}
	}
	service.hlsSeekMu.Unlock()
}
func hlsPlaylistSegmentBounds(directory string) (int, int, bool) {
	file, err := os.Open(filepath.Join(directory, "index.m3u8"))
	if err != nil {
		return 0, 0, false
	}
	defer file.Close()
	first, last := 0, 0
	found := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		name := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(name, "segment-") || !strings.HasSuffix(name, ".ts") {
			continue
		}
		raw := strings.TrimSuffix(strings.TrimPrefix(name, "segment-"), ".ts")
		index, parseErr := strconv.Atoi(raw)
		if parseErr != nil || index < 0 || name != fmt.Sprintf("segment-%06d.ts", index) {
			continue
		}
		if !found || index < first {
			first = index
		}
		if !found || index > last {
			last = index
		}
		found = true
	}
	if scanner.Err() != nil {
		return 0, 0, false
	}
	return first, last, found
}

func localHLSRFC6381Codecs(decision *PlaybackDecision) (string, bool) {
	if decision == nil || decision.Target == nil {
		return "", false
	}
	audio, audioKnown := localHLSRFC6381AudioCodec(decision.Target.AudioCodec)
	if normalizedCodec(decision.Target.VideoCodec) == "" {
		return audio, audioKnown
	}
	video, videoKnown := localHLSRFC6381VideoCodec(decision.Target.VideoCodec)
	if !videoKnown || !audioKnown {
		return "", false
	}
	return video + "," + audio, true
}

func localHLSRFC6381VideoCodec(codec string) (string, bool) {
	switch normalizedCodec(codec) {
	case "h264":
		return "avc1", true
	default:
		return "", false
	}
}

func localHLSRFC6381AudioCodec(codec string) (string, bool) {
	switch normalizedCodec(codec) {
	case "aac":
		return "mp4a", true
	default:
		return "", false
	}
}

func writeHLSPlaylist(w http.ResponseWriter, r *http.Request, contents []byte) error {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Content-Length", strconv.Itoa(len(contents)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return nil
	}
	_, err := w.Write(contents)
	return err
}

func (service *Service) hlsJob(ctx context.Context, sessionID string, asset storedAsset, processor HLSProcessor, mayStart bool) (*hlsJob, error) {
	service.hlsResetMu.RLock()
	defer service.hlsResetMu.RUnlock()
	storageLimit := service.mediaStorageLimit()
	if _, validHeaders := canonicalStoredRequestHeaders(asset.Headers); !validHeaders {
		return nil, ErrMediaSourceFailed
	}
	key := hlsJobKey(sessionID, asset)
	fingerprint := hlsAssetFingerprint(asset)
	if mayStart {
		if err := service.stopOtherHLSGenerations(ctx, hlsJobPrefix(sessionID, asset.ID), key); err != nil {
			return nil, err
		}
	}
	for {
		service.hlsMu.Lock()
		if existing := service.hlsJobs[key]; existing != nil {
			reusable := existing.fingerprint == fingerprint && hlsJobReusable(existing)
			service.hlsMu.Unlock()
			if reusable {
				return existing, nil
			}
			if !mayStart {
				return nil, ErrSessionNotFound
			}
			if err := service.stopHLSJobAfterPreparations(ctx, key); err != nil {
				return nil, err
			}
			continue
		}
		if !mayStart {
			service.hlsMu.Unlock()
			return nil, ErrSessionNotFound
		}
		if shared := service.sharedHLSJobLocked(fingerprint, asset.Kind); shared != nil {
			service.addHLSJobBindingLocked(key, shared, strings.HasPrefix(sessionID, "prewarm-"))
			service.hlsMu.Unlock()
			return shared, nil
		}
		service.hlsMu.Unlock()

		if !service.reclaimHLSStorageTo(storageLimit, true) {
			return nil, ErrMediaStorageLimit
		}
		service.hlsMu.Lock()
		if existing := service.hlsJobs[key]; existing != nil {
			service.hlsMu.Unlock()
			continue
		}
		if shared := service.sharedHLSJobLocked(fingerprint, asset.Kind); shared != nil {
			service.addHLSJobBindingLocked(key, shared, strings.HasPrefix(sessionID, "prewarm-"))
			service.hlsMu.Unlock()
			return shared, nil
		}
		if directorySize(service.mediaOptions.TempDirectory) >= storageLimit {
			service.hlsMu.Unlock()
			return nil, ErrMediaStorageLimit
		}
		directory := filepath.Join(
			service.mediaOptions.TempDirectory, sessionID, asset.ID,
			"start-"+hlsStartKey(asset.StartSeconds)+"-"+strconv.FormatUint(service.hlsWorkspaceGeneration.Add(1), 10),
		)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			service.hlsMu.Unlock()
			return nil, fmt.Errorf("%w: create media workspace: %v", ErrMediaProcessingFailed, err)
		}
		jobContext, cancel := context.WithCancel(context.Background())
		now := service.currentTime()
		job := &hlsJob{
			directory: directory, fingerprint: fingerprint, sessionID: sessionID, assetID: asset.ID,
			mode: asset.Kind, segmentContainer: normalizedHLSSegmentContainer(asset.HLSSegmentContainer), prewarming: strings.HasPrefix(sessionID, "prewarm-"),
			sourceDurationSeconds: asset.DurationSeconds, startOffsetSeconds: asset.StartSeconds,
			createdAt: now, lastAccessed: now, cancel: cancel, done: make(chan struct{}),
		}
		service.addHLSJobBindingLocked(key, job, job.prewarming)
		service.hlsMu.Unlock()
		go service.runHLSJob(jobContext, job, asset, processor)
		go service.monitorHLSStorage(jobContext, job)
		return job, nil
	}
}

func hlsJobReusable(job *hlsJob) bool {
	job.mu.RLock()
	reusable := job.err == nil
	job.mu.RUnlock()
	return reusable
}

func hlsJobShareable(job *hlsJob) bool {
	if !hlsJobReusable(job) {
		return false
	}
	playlistPath := filepath.Join(job.directory, "index.m3u8")
	if _, err := os.Stat(playlistPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return false
		}
		if job.done == nil {
			return false
		}
		select {
		case <-job.done:
			return false
		default:
			return true
		}
	}
	extension := ".ts"
	if job.segmentContainer == "mp4" {
		extension = ".m4s"
	}
	initial, err := os.Stat(filepath.Join(job.directory, "segment-000000"+extension))
	if err != nil || initial.Size() <= 0 {
		return false
	}
	encodedSeconds, ok := hlsPlaylistEncodedSeconds(job.directory)
	if !ok {
		return false
	}
	shareableSegments := hlsRetainedSegments - hlsDeleteThreshold - hlsSharedWorkerSafetySegments
	return shareableSegments > 0 && encodedSeconds <= float64(shareableSegments*hlsSegmentDurationSeconds)
}

func shareableHLSMode(mode string) bool {
	switch mode {
	case processingRemux, processingTranscodeAudio, processingTranscode:
		return true
	default:
		return false
	}
}

// sharedHLSJobLocked returns one physical worker for an exact processing fingerprint.
// Session authorization remains represented by a distinct hlsJobs binding.
func (service *Service) sharedHLSJobLocked(fingerprint, mode string) *hlsJob {
	if !shareableHLSMode(mode) {
		return nil
	}
	for _, job := range service.hlsJobs {
		if job.fingerprint == fingerprint && hlsJobShareable(job) {
			return job
		}
	}
	return nil
}

// addHLSJobBindingLocked publishes a session-specific authorization binding.
func (service *Service) addHLSJobBindingLocked(key string, job *hlsJob, prewarming bool) {
	if job.bindings == nil {
		job.bindings = make(map[string]*hlsJobBinding)
	}
	binding := &hlsJobBinding{prewarming: prewarming}
	binding.timer = time.AfterFunc(service.mediaOptions.IdleTTL, func() { service.stopHLSJobInstance(key, job) })
	job.bindings[key] = binding
	if job.timer == nil {
		job.timer = binding.timer
	}
	service.hlsJobs[key] = job
}

func hlsPlaylistEncodedSeconds(directory string) (float64, bool) {
	file, err := os.Open(filepath.Join(directory, "index.m3u8"))
	if err != nil {
		return 0, false
	}
	defer file.Close()

	const (
		durationPrefix = "#EXTINF:"
		sequencePrefix = "#EXT-X-MEDIA-SEQUENCE:"
	)
	var total float64
	var mediaSequence uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()
		if bytes.HasPrefix(line, []byte(sequencePrefix)) {
			value := bytes.TrimSpace(line[len(sequencePrefix):])
			sequence, parseErr := strconv.ParseUint(string(value), 10, 63)
			if parseErr != nil {
				return 0, false
			}
			mediaSequence = sequence
			continue
		}
		if !bytes.HasPrefix(line, []byte(durationPrefix)) {
			continue
		}
		value := line[len(durationPrefix):]
		comma := bytes.IndexByte(value, ',')
		if comma < 0 {
			continue
		}
		value = value[:comma]
		seconds, parseErr := strconv.ParseFloat(string(bytes.TrimSpace(value)), 64)
		if parseErr != nil || seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
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
	if mediaSequence > ^uint64(0)/uint64(hlsSegmentDurationSeconds) {
		return 0, false
	}
	total += float64(mediaSequence * uint64(hlsSegmentDurationSeconds))
	if math.IsInf(total, 0) {
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
	generation := *asset
	generation.StartSeconds = hlsGenerationStart(generation, generation.StartSeconds)
	if err := service.stopOtherHLSGenerations(ctx, prewarmSessionID+"/", hlsJobKey(prewarmSessionID, generation)); err != nil {
		return err
	}
	job, err := service.hlsJob(ctx, prewarmSessionID, generation, processor, true)
	if err != nil {
		return err
	}
	if err := waitForMediaFile(ctx, job, filepath.Join(job.directory, "index.m3u8")); err != nil {
		service.stopHLSJob(hlsJobKey(prewarmSessionID, generation))
		return err
	}
	service.touchHLSJob(hlsJobKey(prewarmSessionID, generation), job)
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
		asset.StartSeconds = hlsGenerationStart(asset, asset.StartSeconds)
		if service.adoptHLSJob(prewarmSessionID, sessionID, asset) {
			return nil
		}
		processor, ok := service.processor.(HLSProcessor)
		if !ok {
			return ErrMediaProcessingFailed
		}
		start := func() error {
			job, err := service.hlsJob(ctx, sessionID, asset, processor, true)
			if err != nil {
				return err
			}
			if err := waitForMediaFile(ctx, job, filepath.Join(job.directory, "index.m3u8")); err != nil {
				service.stopHLSJob(hlsJobKey(sessionID, asset))
				return err
			}
			service.touchHLSJob(hlsJobKey(sessionID, asset), job)
			return nil
		}
		if err := start(); err != nil {
			if !errors.Is(err, ErrMediaCapacityReached) || prewarmSessionID == "" {
				return err
			}
			service.stopHLSSession(prewarmSessionID)
			if err := start(); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

func (service *Service) adoptHLSJob(fromSessionID, toSessionID string, asset storedAsset) bool {
	service.hlsResetMu.RLock()
	defer service.hlsResetMu.RUnlock()
	fromKey := hlsJobKey(fromSessionID, asset)
	toKey := hlsJobKey(toSessionID, asset)
	service.hlsMu.Lock()
	defer service.hlsMu.Unlock()
	job := service.hlsJobs[fromKey]
	if job == nil || job.fingerprint != hlsAssetFingerprint(asset) || !hlsJobReusable(job) || service.hlsJobs[toKey] != nil {
		return false
	}
	if binding := job.bindings[fromKey]; binding != nil {
		if binding.timer != nil {
			binding.timer.Stop()
		}
		delete(job.bindings, fromKey)
	} else if job.timer != nil {
		job.timer.Stop()
	}
	delete(service.hlsJobs, fromKey)
	service.addHLSJobBindingLocked(toKey, job, false)
	job.mu.Lock()
	job.sessionID = toSessionID
	job.prewarming = false
	job.lastAccessed = service.currentTime()
	job.mu.Unlock()
	return true
}

func (service *Service) stopOtherHLSGenerations(ctx context.Context, prefix, keep string) error {
	service.hlsMu.Lock()
	keys := make([]string, 0)
	for key := range service.hlsJobs {
		if key != keep && strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	service.hlsMu.Unlock()
	for _, key := range keys {
		if err := service.stopHLSJobAfterPreparations(ctx, key); err != nil {
			return err
		}
	}
	return nil
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
	decision, _ := json.Marshal(asset.Decision)
	audioTrack := -1
	if asset.AudioTrackIndex != nil {
		audioTrack = *asset.AudioTrackIndex
	}
	subtitleTrack := -1
	if asset.SubtitleTrackIndex != nil {
		subtitleTrack = *asset.SubtitleTrackIndex
	}
	return fmt.Sprintf(
		"%s|%s|%s|%s|%t|%t|%d|%d|%s|%d|%x|%x|%d|%d|%d|%s|%s",
		mediaProbeKey(asset), asset.ID, asset.Kind, normalizedHLSSegmentContainer(asset.HLSSegmentContainer), asset.ToneMap,
		asset.DolbyVisionToneMapSafe, audioTrack, subtitleTrack, asset.SubtitleTrackType, asset.SubtitleTrackOrdinal,
		math.Float64bits(asset.DurationSeconds), math.Float64bits(asset.ReadRate), asset.TargetHeight, asset.VideoBitrateKbps,
		asset.MaximumAudioChannels, hlsStartKey(asset.StartSeconds), decision,
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
			service.reclaimHLSStorage(false)
		}
	}
}

func (service *Service) reclaimHLSStorage(admission bool) bool {
	return service.reclaimHLSStorageTo(service.mediaStorageLimit(), admission)
}

func (service *Service) reclaimHLSStorageTo(limit int64, admission bool) bool {
	service.hlsStorageMu.Lock()
	defer service.hlsStorageMu.Unlock()
	return service.reclaimHLSStorageLocked(limit, admission)
}

func (service *Service) reclaimHLSStorageLocked(limit int64, admission bool) bool {
	for {
		size := directorySize(service.mediaOptions.TempDirectory)
		if size < limit || !admission && size == limit {
			return true
		}
		_, job := service.hlsStorageVictim()
		if job == nil {
			return false
		}
		job.mu.Lock()
		job.err = ErrMediaStorageLimit
		job.mu.Unlock()
		service.stopDetachedHLSJob(job)
	}
}

func (service *Service) hlsStorageVictim() (string, *hlsJob) {
	service.hlsMu.Lock()
	defer service.hlsMu.Unlock()
	selectedKey := ""
	var selected *hlsJob
	var selectedActive, selectedPrewarming bool
	var selectedLastAccessed, selectedCreatedAt time.Time
	keysByJob := make(map[*hlsJob]string, len(service.hlsJobs))
	for key, job := range service.hlsJobs {
		if existing, found := keysByJob[job]; !found || key < existing {
			keysByJob[job] = key
		}
	}
	// Prefer inactive work, but fall back to an active job so an over-limit writer cannot grow unchecked.
	for job, key := range keysByJob {
		job.mu.RLock()
		active := job.activeRequests > 0
		lastAccessed := job.lastAccessed
		createdAt := job.createdAt
		job.mu.RUnlock()
		prewarming := hlsJobOnlyPrewarming(job)
		if selected == nil {
			selectedKey, selected = key, job
			selectedActive, selectedPrewarming = active, prewarming
			selectedLastAccessed, selectedCreatedAt = lastAccessed, createdAt
			continue
		}
		if active != selectedActive {
			if !active {
				selectedKey, selected = key, job
				selectedActive, selectedPrewarming = active, prewarming
				selectedLastAccessed, selectedCreatedAt = lastAccessed, createdAt
			}
			continue
		}
		if prewarming != selectedPrewarming {
			if prewarming {
				selectedKey, selected = key, job
				selectedActive, selectedPrewarming = active, prewarming
				selectedLastAccessed, selectedCreatedAt = lastAccessed, createdAt
			}
			continue
		}
		if lastAccessed.Before(selectedLastAccessed) || lastAccessed.Equal(selectedLastAccessed) &&
			(createdAt.Before(selectedCreatedAt) || createdAt.Equal(selectedCreatedAt) && key < selectedKey) {
			selectedKey, selected = key, job
			selectedActive, selectedPrewarming = active, prewarming
			selectedLastAccessed, selectedCreatedAt = lastAccessed, createdAt
		}
	}
	if selected != nil {
		for key, job := range service.hlsJobs {
			if job != selected {
				continue
			}
			if binding := selected.bindings[key]; binding != nil && binding.timer != nil {
				binding.timer.Stop()
			}
			delete(selected.bindings, key)
			delete(service.hlsJobs, key)
		}
		if len(selected.bindings) == 0 && selected.timer != nil {
			selected.timer.Stop()
		}
	}
	return selectedKey, selected
}

func hlsJobOnlyPrewarming(job *hlsJob) bool {
	if len(job.bindings) == 0 {
		return job.prewarming
	}
	for _, binding := range job.bindings {
		if !binding.prewarming {
			return false
		}
	}
	return true
}

func (service *Service) touchHLSJob(key string, job *hlsJob) {
	service.hlsMu.Lock()
	defer service.hlsMu.Unlock()
	if service.hlsJobs[key] != job {
		return
	}
	job.mu.Lock()
	job.lastAccessed = service.currentTime()
	job.mu.Unlock()
	if binding := job.bindings[key]; binding != nil && binding.timer != nil {
		binding.timer.Reset(service.mediaOptions.IdleTTL)
	} else if job.timer != nil {
		job.timer.Reset(service.mediaOptions.IdleTTL)
	}
}

func (service *Service) retainHLSJobRequest(key string, job *hlsJob) *hlsJobRequest {
	service.hlsResetMu.RLock()
	defer service.hlsResetMu.RUnlock()
	service.hlsMu.Lock()
	defer service.hlsMu.Unlock()
	if service.hlsJobs[key] != job {
		return nil
	}
	return service.retainHLSJobRequestLocked(key, job)
}

// retainHLSJobRequestLocked requires hlsMu to keep registry membership and
// request admission atomic with stopHLSJobInstance.
func (service *Service) retainHLSJobRequestLocked(key string, job *hlsJob) *hlsJobRequest {
	job.mu.Lock()
	if job.activeRequests == 0 {
		job.activeRequestsDone = make(chan struct{})
	}
	if job.activePreparations == 0 {
		job.activePreparationsDone = make(chan struct{})
	}
	job.activeRequests++
	job.activePreparations++
	job.lastAccessed = service.currentTime()
	job.mu.Unlock()
	if binding := job.bindings[key]; binding != nil && binding.timer != nil {
		binding.timer.Reset(service.mediaOptions.IdleTTL)
	} else if job.timer != nil {
		job.timer.Reset(service.mediaOptions.IdleTTL)
	}
	return &hlsJobRequest{service: service, job: job}
}

func (request *hlsJobRequest) promote() {
	if request == nil || request.released || request.promoted {
		return
	}
	request.promoted = true
	request.job.mu.Lock()
	request.job.activePreparations--
	if request.job.activePreparations == 0 {
		close(request.job.activePreparationsDone)
		request.job.activePreparationsDone = nil
	}
	request.job.mu.Unlock()
}

func (request *hlsJobRequest) release() {
	if request == nil || request.released {
		return
	}
	request.released = true
	stopWhenIdle := false
	request.job.mu.Lock()
	if !request.promoted {
		request.job.activePreparations--
		if request.job.activePreparations == 0 {
			close(request.job.activePreparationsDone)
			request.job.activePreparationsDone = nil
		}
	}
	request.job.activeRequests--
	if request.job.activeRequests == 0 {
		close(request.job.activeRequestsDone)
		request.job.activeRequestsDone = nil
		stopWhenIdle = request.job.stopWhenIdle
		request.job.stopWhenIdle = false
	}
	request.job.mu.Unlock()
	if stopWhenIdle {
		request.service.stopDetachedHLSJob(request.job)
	}
	request.service.reclaimHLSStorage(false)
}

func (service *Service) stopHLSJobAfterPreparations(ctx context.Context, key string) error {
	service.hlsMu.Lock()
	job := service.hlsJobs[key]
	if job != nil {
		if binding := job.bindings[key]; binding != nil && binding.timer != nil {
			binding.timer.Stop()
		} else if job.timer != nil {
			job.timer.Stop()
		}
	}
	service.hlsMu.Unlock()
	if job == nil {
		return nil
	}
	job.mu.Lock()
	preparationsDone := job.activePreparationsDone
	job.mu.Unlock()
	if preparationsDone != nil {
		select {
		case <-preparationsDone:
		case <-ctx.Done():
			service.hlsMu.Lock()
			if service.hlsJobs[key] == job {
				if binding := job.bindings[key]; binding != nil && binding.timer != nil {
					binding.timer.Reset(service.mediaOptions.IdleTTL)
				} else if job.timer != nil {
					job.timer.Reset(service.mediaOptions.IdleTTL)
				}
			}
			service.hlsMu.Unlock()
			return ctx.Err()
		}
	}
	service.stopHLSJobInstance(key, job)
	return nil
}

func (service *Service) stopHLSJob(key string) {
	service.stopHLSJobInstance(key, nil)
}

func (service *Service) stopHLSJobInstance(key string, expected *hlsJob) {
	service.hlsMu.Lock()
	job := service.hlsJobs[key]
	if expected != nil && job != expected {
		job = nil
	}
	lastBinding := false
	if job != nil {
		if binding := job.bindings[key]; binding != nil {
			if binding.timer != nil {
				binding.timer.Stop()
			}
			delete(job.bindings, key)
		} else if job.timer != nil {
			job.timer.Stop()
		}
		delete(service.hlsJobs, key)
		lastBinding = true
		for _, registered := range service.hlsJobs {
			if registered == job {
				lastBinding = false
				break
			}
		}
	}
	service.hlsMu.Unlock()
	if lastBinding {
		service.stopDetachedHLSJobWhenIdle(job)
	}
}

// stopDetachedHLSJobWhenIdle is used for ordinary session/timer detach. A
// retained response keeps the worker and workspace alive through its release.
func (service *Service) stopDetachedHLSJobWhenIdle(job *hlsJob) {
	job.mu.Lock()
	if job.activeRequestsDone != nil {
		job.stopWhenIdle = true
		job.mu.Unlock()
		return
	}
	job.mu.Unlock()
	service.stopDetachedHLSJob(job)
}

// stopDetachedHLSJob force-stops a worker selected for storage reclamation.
// It is idempotent because aliases may have raced with victim selection.
func (service *Service) stopDetachedHLSJob(job *hlsJob) {
	job.stopOnce.Do(func() {
		job.mu.Lock()
		requestsDone := job.activeRequestsDone
		job.mu.Unlock()
		if job.cancel != nil {
			job.cancel()
		}
		if job.done != nil {
			select {
			case <-job.done:
			case <-time.After(5 * time.Second):
			}
		}
		cleanup := func() {
			service.hlsMu.Lock()
			defer service.hlsMu.Unlock()
			_ = os.RemoveAll(job.directory)
			_ = removeEmptyParents(filepath.Dir(job.directory), service.mediaOptions.TempDirectory)
		}
		if requestsDone == nil {
			cleanup()
			return
		}
		go func() {
			<-requestsDone
			cleanup()
		}()
	})
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
			if info, statErr := os.Stat(path); statErr == nil && info.Size() > 0 {
				return nil
			}
			return ErrMediaSourceFailed
		case <-timer.C:
			return ErrMediaSourceFailed
		case <-ticker.C:
		}
	}
}

func waitForHLSBuffer(ctx context.Context, job *hlsJob, minimumSeconds float64) error {
	timer := time.NewTimer(hlsReadyTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		seconds, readable := hlsPlaylistEncodedSeconds(job.directory)
		if readable && seconds >= minimumSeconds {
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
			seconds, readable = hlsPlaylistEncodedSeconds(job.directory)
			if readable && seconds > 0 {
				return nil
			}
			return ErrMediaSourceFailed
		case <-timer.C:
			return ErrMediaSourceFailed
		case <-ticker.C:
		}
	}
}

func rewriteLocalPlaylist(contents []byte, buildURL func(string) string) ([]byte, error) {
	return rewriteLocalPlaylistWithPolicy(contents, false, buildURL)
}

func rewriteLocalPlaylistWithPolicy(contents []byte, rejectUnresolved bool, buildURL func(string) string) ([]byte, error) {
	return rewriteLocalPlaylistWithReferencePolicy(contents, rejectUnresolved, func(reference string, _ bool) string {
		return buildURL(reference)
	})
}

func rewriteLocalPlaylistWithReferencePolicy(contents []byte, rejectUnresolved bool, buildURL func(string, bool) string) ([]byte, error) {
	if err := validatePlaylistCardinality(contents); err != nil {
		return nil, fmt.Errorf("%w: invalid local HLS playlist: %w", ErrMediaProcessingFailed, err)
	}
	output := newBoundedPlaylistOutput(len(contents))
	hasStartDirective := bytes.HasPrefix(contents, []byte("#EXT-X-START:")) || bytes.Contains(contents, []byte("\n#EXT-X-START:"))
	scanner := playlistScanner(contents)
	mediaSegmentPending := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			unresolved := false
			err := writePlaylistURIAttributes(output, line, func(reference string) (string, bool) {
				if !localMediaName.MatchString(reference) {
					unresolved = true
					return "", false
				}
				return buildURL(reference, localHLSURIAttributeIsMediaSegment(line)), true
			})
			if err != nil {
				return nil, fmt.Errorf("%w: invalid local HLS playlist: %w", ErrMediaProcessingFailed, err)
			}
			if rejectUnresolved && unresolved {
				return nil, fmt.Errorf("%w: invalid local HLS playlist: unproxyable reference", ErrMediaProcessingFailed)
			}
			if strings.HasPrefix(line, "#EXTINF:") {
				mediaSegmentPending = true
			}
		} else if strings.TrimSpace(line) != "" {
			reference := strings.TrimSpace(line)
			if !localMediaName.MatchString(reference) {
				return nil, ErrMediaProcessingFailed
			}
			if err := output.writeString(buildURL(reference, mediaSegmentPending)); err != nil {
				return nil, fmt.Errorf("%w: invalid local HLS playlist: %w", ErrMediaProcessingFailed, err)
			}
			mediaSegmentPending = false
		} else if err := output.writeString(line); err != nil {
			return nil, fmt.Errorf("%w: invalid local HLS playlist: %w", ErrMediaProcessingFailed, err)
		}
		if err := output.writeByte('\n'); err != nil {
			return nil, fmt.Errorf("%w: invalid local HLS playlist: %w", ErrMediaProcessingFailed, err)
		}
		if line == "#EXTM3U" && !hasStartDirective {
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

func localHLSURIAttributeIsMediaSegment(line string) bool {
	if strings.HasPrefix(line, "#EXT-X-PART:") {
		return true
	}
	attributes, isPreloadHint := strings.CutPrefix(line, "#EXT-X-PRELOAD-HINT:")
	for isPreloadHint {
		attribute, remaining, found := strings.Cut(attributes, ",")
		if strings.TrimSpace(attribute) == "TYPE=PART" {
			return true
		}
		if !found {
			return false
		}
		attributes = remaining
	}
	return false
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
