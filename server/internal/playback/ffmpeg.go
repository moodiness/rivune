package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const mediaProbeTimeout = 15 * time.Second

var ErrMediaProcessingFailed = errors.New("media processing failed")

type MediaProcessor interface {
	Probe(context.Context, storedAsset) (MediaInspection, error)
	Process(context.Context, storedAsset, io.Writer) error
}

type FFmpegProcessor struct {
	ffmpegPath    string
	ffprobePath   string
	slots         chan struct{}
	subtitleSlots chan struct{}
	threads       int
	encoder       videoEncoder
}

func NewFFmpegProcessor(ffmpegPath, ffprobePath string, maximumConcurrent, threads int, options FFmpegOptions) (*FFmpegProcessor, error) {
	ffmpegPath = strings.TrimSpace(ffmpegPath)
	ffprobePath = strings.TrimSpace(ffprobePath)
	if ffmpegPath == "" || ffprobePath == "" || maximumConcurrent < 1 || threads < 1 {
		return nil, errors.New("FFmpeg paths, positive concurrency, and positive thread count are required")
	}
	resolvedFFmpeg, err := exec.LookPath(ffmpegPath)
	if err != nil {
		return nil, fmt.Errorf("find FFmpeg executable: %w", err)
	}
	resolvedFFprobe, err := exec.LookPath(ffprobePath)
	if err != nil {
		return nil, fmt.Errorf("find FFprobe executable: %w", err)
	}
	encoder, err := detectVideoEncoder(resolvedFFmpeg, options)
	if err != nil {
		return nil, err
	}
	return &FFmpegProcessor{
		ffmpegPath: resolvedFFmpeg, ffprobePath: resolvedFFprobe, encoder: encoder,
		slots: make(chan struct{}, maximumConcurrent), subtitleSlots: make(chan struct{}, maximumConcurrent), threads: threads,
	}, nil
}

func (processor *FFmpegProcessor) Probe(ctx context.Context, asset storedAsset) (MediaInspection, error) {
	probeContext, cancel := context.WithTimeout(ctx, mediaProbeTimeout)
	defer cancel()

	arguments := []string{"-v", "error", "-analyzeduration", "1000000", "-probesize", "1000000"}
	arguments = append(arguments, ffmpegInputArguments(asset)...)
	arguments = append(arguments,
		"-show_entries", "stream=index,codec_type,codec_name,profile,width,height,channels,color_transfer,codec_tag_string:stream_tags=language,title:stream_disposition=attached_pic:stream_side_data=side_data_type:format=format_name,duration",
		"-of", "json",
		asset.URL,
	)
	command := exec.CommandContext(probeContext, processor.ffprobePath, arguments...)
	var output bytes.Buffer
	var diagnostic bytes.Buffer
	command.Stdout = &output
	command.Stderr = &diagnostic
	if err := command.Run(); err != nil {
		return MediaInspection{}, fmt.Errorf("probe media: %w: %s", err, boundedDiagnostic(diagnostic.String()))
	}
	var result struct {
		Streams []struct {
			Index          int    `json:"index"`
			CodecType      string `json:"codec_type"`
			CodecName      string `json:"codec_name"`
			Profile        string `json:"profile"`
			Width          int    `json:"width"`
			Height         int    `json:"height"`
			Channels       int    `json:"channels"`
			ColorTransfer  string `json:"color_transfer"`
			CodecTagString string `json:"codec_tag_string"`
			Tags           struct {
				Language string `json:"language"`
				Title    string `json:"title"`
			} `json:"tags"`
			Disposition struct {
				AttachedPicture int `json:"attached_pic"`
			} `json:"disposition"`
			SideData []struct {
				Type string `json:"side_data_type"`
			} `json:"side_data_list"`
		} `json:"streams"`
		Format struct {
			Name     string `json:"format_name"`
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		return MediaInspection{}, fmt.Errorf("decode FFprobe response: %w", err)
	}
	inspection := MediaInspection{
		Container:      inspectedContainer(result.Format.Name, asset.Container),
		VideoTracks:    make([]MediaTrack, 0),
		AudioTracks:    make([]MediaTrack, 0),
		SubtitleTracks: make([]MediaTrack, 0),
	}
	inspection.DurationSeconds, _ = strconv.ParseFloat(result.Format.Duration, 64)
	for _, stream := range result.Streams {
		track := MediaTrack{
			Index: stream.Index, Type: strings.ToLower(stream.CodecType),
			Codec: normalizedCodec(stream.CodecName), Profile: strings.TrimSpace(stream.Profile),
			Language: strings.TrimSpace(stream.Tags.Language), Title: strings.TrimSpace(stream.Tags.Title),
			Width: stream.Width, Height: stream.Height, Channels: stream.Channels,
		}
		switch track.Type {
		case "video":
			if stream.Disposition.AttachedPicture != 0 {
				continue
			}
			inspection.VideoTracks = append(inspection.VideoTracks, track)
			if inspection.HDRFormat == "" {
				inspection.HDRFormat = inspectedHDRFormat(stream.CodecTagString, stream.ColorTransfer, stream.SideData)
			}
		case "audio":
			inspection.AudioTracks = append(inspection.AudioTracks, track)
		case "subtitle":
			inspection.SubtitleTracks = append(inspection.SubtitleTracks, track)
		}
	}
	if len(inspection.VideoTracks) == 0 {
		return MediaInspection{}, errors.New("FFprobe returned no video stream")
	}
	return inspection, nil
}

func (processor *FFmpegProcessor) Process(ctx context.Context, asset storedAsset, destination io.Writer) error {
	if err := processor.acquire(ctx); err != nil {
		return err
	}
	defer processor.release()
	arguments, err := processor.progressiveArguments(asset)
	if err != nil {
		return err
	}
	return processor.run(ctx, arguments, destination)
}

func (processor *FFmpegProcessor) progressiveArguments(asset storedAsset) ([]string, error) {
	arguments, err := processor.processingArguments(asset)
	if err != nil {
		return nil, err
	}
	if asset.Kind == processingTranscode {
		arguments = append(arguments, "-force_key_frames", "expr:gte(t,n_forced*1)")
	}
	arguments = append(arguments,
		"-movflags", "frag_keyframe+empty_moov+default_base_moof",
		"-frag_duration", "1000000",
		"-flush_packets", "1",
		"-f", "mp4", "pipe:1",
	)
	return arguments, nil
}

func (processor *FFmpegProcessor) ProcessHLS(ctx context.Context, asset storedAsset, directory string) error {
	if err := processor.acquire(ctx); err != nil {
		return err
	}
	defer processor.release()
	asset.ReadRate = 1.05
	err := processor.processHLS(ctx, asset, directory, processor.encoder)
	if err == nil || asset.Kind != processingTranscode || processor.encoder.normalizedKind() == videoEncoderSoftware || hlsOutputStarted(directory) {
		return err
	}
	if resetErr := resetHLSDirectory(directory); resetErr != nil {
		return fmt.Errorf("reset HLS output for software fallback: %w", resetErr)
	}
	if fallbackErr := processor.processHLS(ctx, asset, directory, videoEncoder{kind: videoEncoderSoftware}); fallbackErr != nil {
		return fmt.Errorf("hardware encoding failed: %v; software fallback failed: %w", err, fallbackErr)
	}
	return nil
}

func (processor *FFmpegProcessor) processHLS(ctx context.Context, asset storedAsset, directory string, encoder videoEncoder) error {
	arguments, err := processor.processingArgumentsWithEncoder(asset, encoder)
	if err != nil {
		return err
	}
	if asset.Kind == processingTranscode {
		arguments = append(arguments, "-force_key_frames", "expr:gte(t,n_forced*1)")
	}
	hlsFlags := "independent_segments+temp_file"
	if asset.Kind != processingTranscode {
		hlsFlags = "split_by_time+temp_file"
	}
	arguments = append(arguments,
		"-f", "hls", "-hls_time", "1", "-hls_list_size", "0", "-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(directory, "segment-%06d.m4s"),
		"-hls_flags", hlsFlags, filepath.Join(directory, "index.m3u8"),
	)
	return processor.run(ctx, arguments, nil)
}

func hlsOutputStarted(directory string) bool {
	info, err := os.Stat(filepath.Join(directory, "index.m3u8"))
	return err == nil && info.Size() > 0
}

func resetHLSDirectory(directory string) error {
	if err := os.RemoveAll(directory); err != nil {
		return err
	}
	return os.MkdirAll(directory, 0o700)
}

func (processor *FFmpegProcessor) ConvertSubtitle(ctx context.Context, asset storedAsset, destination io.Writer) error {
	if err := acquireSlot(ctx, processor.subtitleSlots); err != nil {
		return err
	}
	defer releaseSlot(processor.subtitleSlots)
	arguments := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-analyzeduration", "1000000", "-probesize", "1000000"}
	arguments = append(arguments, ffmpegInputArguments(asset)...)
	if asset.StartSeconds > 0 {
		arguments = append(arguments, "-ss", strconv.FormatFloat(asset.StartSeconds, 'f', -1, 64))
	}
	arguments = append(arguments, "-i", asset.URL, "-map")
	if asset.SubtitleTrackIndex != nil {
		arguments = append(arguments, fmt.Sprintf("0:%d", *asset.SubtitleTrackIndex))
	} else {
		arguments = append(arguments, "0:0")
	}
	arguments = append(arguments, "-c:s", "webvtt", "-f", "webvtt", "pipe:1")
	return processor.run(ctx, arguments, destination)
}

func (processor *FFmpegProcessor) processingArguments(asset storedAsset) ([]string, error) {
	return processor.processingArgumentsWithEncoder(asset, processor.encoder)
}

func (processor *FFmpegProcessor) processingArgumentsWithEncoder(asset storedAsset, encoder videoEncoder) ([]string, error) {
	arguments := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-analyzeduration", "1000000", "-probesize", "1000000"}
	if asset.Kind == processingTranscode {
		arguments = append(arguments, encoder.globalArguments()...)
	}
	arguments = append(arguments, ffmpegInputArguments(asset)...)
	if asset.ReadRate > 0 {
		arguments = append(arguments, "-readrate", strconv.FormatFloat(asset.ReadRate, 'f', 2, 64))
	}
	if asset.StartSeconds > 0 {
		arguments = append(arguments, "-ss", strconv.FormatFloat(asset.StartSeconds, 'f', -1, 64))
	}
	arguments = append(arguments, "-i", asset.URL, "-map", "0:v:0", "-map")
	if asset.AudioTrackIndex != nil {
		arguments = append(arguments, fmt.Sprintf("0:%d?", *asset.AudioTrackIndex))
	} else {
		arguments = append(arguments, "0:a:0?")
	}
	arguments = append(arguments, "-sn", "-dn")
	switch asset.Kind {
	case processingRemux:
		arguments = append(arguments, "-c:v", "copy", "-c:a", "copy")
	case processingTranscodeAudio:
		arguments = append(arguments, "-c:v", "copy", "-c:a", "aac", "-ac", "2", "-b:a", "192k")
	case processingTranscode:
		if filter := encoder.filter(asset.ToneMap); filter != "" {
			arguments = append(arguments, "-vf", filter)
		}
		arguments = append(arguments, encoder.codecArguments(processor.threads)...)
		arguments = append(arguments, "-c:a", "aac", "-ac", "2", "-b:a", "256k")
	default:
		return nil, fmt.Errorf("%w: unsupported mode %q", ErrMediaProcessingFailed, asset.Kind)
	}
	return arguments, nil
}

func (processor *FFmpegProcessor) VideoEncoder() string {
	return string(processor.encoder.normalizedKind())
}

func (processor *FFmpegProcessor) HardwareToneMap() bool {
	return processor.encoder.hardwareToneMap
}

func (processor *FFmpegProcessor) ActiveProcesses() int {
	return len(processor.slots)
}

func (processor *FFmpegProcessor) ProcessLimit() int {
	return cap(processor.slots)
}

func (processor *FFmpegProcessor) acquire(ctx context.Context) error {
	return acquireSlot(ctx, processor.slots)
}

func (processor *FFmpegProcessor) release() {
	releaseSlot(processor.slots)
}

func acquireSlot(ctx context.Context, slots chan struct{}) error {
	select {
	case slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrMediaCapacityReached
	}
}

func releaseSlot(slots chan struct{}) {
	<-slots
}

func (processor *FFmpegProcessor) run(ctx context.Context, arguments []string, destination io.Writer) error {
	command := exec.CommandContext(ctx, processor.ffmpegPath, arguments...)
	var diagnostic bytes.Buffer
	command.Stdout = destination
	command.Stderr = &diagnostic
	if err := command.Run(); err != nil {
		message := boundedDiagnostic(diagnostic.String())
		normalized := strings.ToLower(message)
		if strings.Contains(normalized, "invalid data") ||
			strings.Contains(normalized, "input/output error") ||
			strings.Contains(normalized, "http error") ||
			strings.Contains(normalized, "server returned") {
			return fmt.Errorf("%w: %v: %s", ErrMediaSourceFailed, err, message)
		}
		return fmt.Errorf("%w: %v: %s", ErrMediaProcessingFailed, err, message)
	}
	return nil
}

func browserFallbackAsset(asset storedAsset) storedAsset {
	if asset.Kind == processingRemux || asset.Kind == processingTranscodeAudio {
		asset.Kind = processingTranscode
	}
	return asset
}

func (service *Service) proxyProcessedMedia(w http.ResponseWriter, r *http.Request, asset storedAsset) error {
	if service.processor == nil {
		return ErrMediaProcessingFailed
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return nil
	}
	if err := http.NewResponseController(w).Flush(); err != nil {
		return fmt.Errorf("%w: flush response: %v", ErrMediaProcessingFailed, err)
	}
	return service.processor.Process(r.Context(), browserFallbackAsset(asset), w)
}

func inspectedContainer(formatName, hint string) string {
	formatName = strings.ToLower(formatName)
	switch {
	case strings.Contains(formatName, "matroska") && strings.EqualFold(hint, "webm"):
		return "webm"
	case strings.Contains(formatName, "matroska"):
		return "mkv"
	case strings.Contains(formatName, "mov") || strings.Contains(formatName, "mp4"):
		return "mp4"
	case strings.Contains(formatName, "mpegts"):
		return "ts"
	default:
		return strings.TrimSpace(strings.Split(formatName, ",")[0])
	}
}

func inspectedHDRFormat(codecTag, colorTransfer string, sideData []struct {
	Type string `json:"side_data_type"`
}) string {
	tag := strings.ToLower(codecTag)
	if strings.HasPrefix(tag, "dvh") || strings.HasPrefix(tag, "dva") {
		return "dolby_vision"
	}
	for _, data := range sideData {
		if strings.Contains(strings.ToLower(data.Type), "dovi") {
			return "dolby_vision"
		}
	}
	switch strings.ToLower(colorTransfer) {
	case "smpte2084":
		return "hdr10"
	case "arib-std-b67":
		return "hlg"
	default:
		return ""
	}
}

func ffmpegInputArguments(asset storedAsset) []string {
	if !strings.HasPrefix(strings.ToLower(asset.URL), "http://") && !strings.HasPrefix(strings.ToLower(asset.URL), "https://") {
		return nil
	}
	arguments := []string{"-user_agent", "Rivune-Playback/1"}
	if headers := ffmpegHeaders(asset.Headers); headers != "" {
		arguments = append(arguments, "-headers", headers)
	}
	return arguments
}

func ffmpegHeaders(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var result strings.Builder
	for _, name := range names {
		value := headers[name]
		if !allowedStoredRequestHeader(name) || strings.ContainsAny(name, "\r\n:") || strings.ContainsAny(value, "\r\n") {
			continue
		}
		result.WriteString(name)
		result.WriteString(": ")
		result.WriteString(value)
		result.WriteString("\r\n")
	}
	return result.String()
}

func boundedDiagnostic(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 2048 {
		return value
	}
	return value[len(value)-2048:]
}
