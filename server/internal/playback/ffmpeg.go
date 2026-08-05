package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/netguard"
)

const (
	mediaProbeTimeout                   = 15 * time.Second
	subtitleConversionTimeout           = 30 * time.Second
	maximumMediaProbeOutputBytes        = 4 << 20
	maximumConvertedSubtitleBytes       = 16 << 20
	maximumMediaDiagnosticBytes         = 2048
	ffmpegNetworkInputProtocolWhitelist = "crypto,http,tcp"
	ffmpegLocalInputProtocolWhitelist   = "file"
)

var (
	ErrMediaProcessingFailed = errors.New("media processing failed")
	errMediaOutputLimit      = errors.New("media output limit reached")
)

type MediaProcessor interface {
	Probe(context.Context, storedAsset) (MediaInspection, error)
}

type FFmpegProcessor struct {
	ffmpegPath      string
	ffprobePath     string
	slots           chan struct{}
	probeSlots      chan struct{}
	subtitleSlots   chan struct{}
	threads         int
	encoder         videoEncoder
	subtitleTimeout time.Duration
	commandContext  func(context.Context, string, ...string) *exec.Cmd
	egressProxy     func(context.Context, storedAsset) (*ffmpegEgressProxy, error)
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
		ffmpegPath: resolvedFFmpeg, ffprobePath: resolvedFFprobe, encoder: encoder, threads: threads,
		slots: make(chan struct{}, maximumConcurrent), probeSlots: make(chan struct{}, maximumConcurrent),
		subtitleSlots: make(chan struct{}, maximumConcurrent), subtitleTimeout: subtitleConversionTimeout,
	}, nil
}

func (processor *FFmpegProcessor) Probe(ctx context.Context, asset storedAsset) (MediaInspection, error) {
	probeContext, cancel := context.WithTimeout(ctx, mediaProbeTimeout)
	defer cancel()
	if err := validateMediaSource(probeContext, asset.URL); err != nil {
		return MediaInspection{}, err
	}
	if err := acquireSlot(probeContext, processor.probeSlots); err != nil {
		return MediaInspection{}, err
	}
	defer releaseSlot(processor.probeSlots)
	egress, err := processor.startInputEgress(probeContext, asset)
	if err != nil {
		return MediaInspection{}, err
	}
	if egress != nil {
		defer egress.Close()
	}

	commandAsset, err := guardedCommandAsset(asset, egress)
	if err != nil {
		return MediaInspection{}, err
	}
	arguments := []string{"-v", "error", "-protocol_whitelist", inputProtocolWhitelist(commandAsset.URL), "-analyzeduration", "1000000", "-probesize", "1000000"}
	arguments = append(arguments, ffmpegInputArguments(commandAsset)...)
	arguments = append(arguments,
		"-show_entries", "stream=index,codec_type,codec_name,profile,width,height,channels,bit_rate,color_transfer,codec_tag_string:stream_tags=language,title:stream_disposition=attached_pic,forced:stream_side_data=side_data_type:format=format_name,duration,bit_rate",
		"-of", "json",
		commandAsset.URL,
	)
	command := processor.newCommand(probeContext, processor.ffprobePath, arguments...)
	output := newCappedBuffer(maximumMediaProbeOutputBytes)
	diagnostic := newDiagnosticBuffer()
	command.Stdout = output
	command.Stderr = diagnostic
	runErr := command.Run()
	if output.exceeded {
		return MediaInspection{}, fmt.Errorf("%w: media probe output exceeds %d bytes", ErrMediaProcessingFailed, maximumMediaProbeOutputBytes)
	}
	if runErr != nil {
		if timeoutErr := probeContext.Err(); timeoutErr != nil {
			return MediaInspection{}, fmt.Errorf("probe media: %w", timeoutErr)
		}
		return MediaInspection{}, fmt.Errorf("probe media: %w: %s", runErr, diagnostic.String())
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
			BitRate        string `json:"bit_rate"`
			ColorTransfer  string `json:"color_transfer"`
			CodecTagString string `json:"codec_tag_string"`
			Tags           struct {
				Language string `json:"language"`
				Title    string `json:"title"`
			} `json:"tags"`
			Disposition struct {
				AttachedPicture int `json:"attached_pic"`
				Forced          int `json:"forced"`
			} `json:"disposition"`
			SideData []struct {
				Type string `json:"side_data_type"`
			} `json:"side_data_list"`
		} `json:"streams"`
		Format struct {
			Name     string `json:"format_name"`
			Duration string `json:"duration"`
			BitRate  string `json:"bit_rate"`
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
		bitRate, _ := strconv.ParseInt(stream.BitRate, 10, 64)
		track := MediaTrack{
			Index: stream.Index, Type: strings.ToLower(stream.CodecType),
			Codec: normalizedCodec(stream.CodecName), Profile: strings.TrimSpace(stream.Profile),
			Language: strings.TrimSpace(stream.Tags.Language), Title: strings.TrimSpace(stream.Tags.Title),
			Width: stream.Width, Height: stream.Height, Channels: stream.Channels, BitrateKbps: int(bitRate / 1000),
			Forced: stream.Disposition.Forced != 0,
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
	if len(inspection.VideoTracks) > 0 && inspection.VideoTracks[0].BitrateKbps == 0 {
		bitRate, _ := strconv.ParseInt(result.Format.BitRate, 10, 64)
		inspection.VideoTracks[0].BitrateKbps = int(bitRate / 1000)
	}
	if len(inspection.VideoTracks) == 0 {
		return MediaInspection{}, errors.New("FFprobe returned no video stream")
	}
	return inspection, nil
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
	if err := validateMediaSource(ctx, asset.URL); err != nil {
		return err
	}
	egress, err := processor.startInputEgress(ctx, asset)
	if err != nil {
		return err
	}
	if egress != nil {
		defer egress.Close()
	}
	commandAsset, err := guardedCommandAsset(asset, egress)
	if err != nil {
		return err
	}
	arguments, err := processor.processingArgumentsWithEncoder(commandAsset, encoder)
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
		"-hls_segment_filename", "segment-%06d.m4s",
		"-hls_flags", hlsFlags, "index.m3u8",
	)
	return processor.runInDirectory(ctx, arguments, nil, directory)
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
	timeout := processor.subtitleTimeout
	if timeout <= 0 {
		timeout = subtitleConversionTimeout
	}
	conversionContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := validateMediaSource(conversionContext, asset.URL); err != nil {
		return err
	}
	if err := acquireSlot(conversionContext, processor.subtitleSlots); err != nil {
		return err
	}
	defer releaseSlot(processor.subtitleSlots)
	egress, err := processor.startInputEgress(conversionContext, asset)
	if err != nil {
		return err
	}
	if egress != nil {
		defer egress.Close()
	}
	commandAsset, err := guardedCommandAsset(asset, egress)
	if err != nil {
		return err
	}
	arguments := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", inputProtocolWhitelist(commandAsset.URL),
		"-analyzeduration", "1000000", "-probesize", "1000000",
	}
	arguments = append(arguments, ffmpegInputArguments(commandAsset)...)
	if commandAsset.StartSeconds > 0 {
		arguments = append(arguments, "-ss", strconv.FormatFloat(commandAsset.StartSeconds, 'f', -1, 64))
	}
	arguments = append(arguments, "-i", commandAsset.URL, "-map")
	if asset.SubtitleTrackIndex != nil {
		arguments = append(arguments, fmt.Sprintf("0:%d", *asset.SubtitleTrackIndex))
	} else {
		arguments = append(arguments, "0:0")
	}
	arguments = append(arguments, "-c:s", "webvtt", "-f", "webvtt", "pipe:1")
	output := &maximumWriter{destination: destination, remaining: maximumConvertedSubtitleBytes}
	runErr := processor.run(conversionContext, arguments, output)
	if output.exceeded {
		return fmt.Errorf("%w: converted subtitle exceeds %d bytes", ErrMediaProcessingFailed, maximumConvertedSubtitleBytes)
	}
	return runErr
}

func (processor *FFmpegProcessor) processingArguments(asset storedAsset) ([]string, error) {
	return processor.processingArgumentsWithEncoder(asset, processor.encoder)
}

func (processor *FFmpegProcessor) processingArgumentsWithEncoder(asset storedAsset, encoder videoEncoder) ([]string, error) {
	arguments := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", inputProtocolWhitelist(asset.URL),
		"-analyzeduration", "1000000", "-probesize", "1000000",
	}
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
	arguments = append(arguments, "-i", asset.URL)
	if asset.Kind == processingTranscode && asset.SubtitleTrackIndex != nil {
		filter := processingVideoFilter(asset, encoder)
		complexFilter := fmt.Sprintf("[0:v:0][0:%d]overlay", *asset.SubtitleTrackIndex)
		if filter != "" {
			complexFilter += "," + filter
		}
		complexFilter += "[vout]"
		arguments = append(arguments, "-filter_complex", complexFilter, "-map", "[vout]")
	} else {
		arguments = append(arguments, "-map", "0:v:0")
		if asset.Kind == processingTranscode {
			if filter := processingVideoFilter(asset, encoder); filter != "" {
				arguments = append(arguments, "-vf", filter)
			}
		}
	}
	arguments = append(arguments, "-map")
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
		arguments = append(arguments, "-c:v", "copy", "-c:a", "aac", "-ac", strconv.Itoa(outputAudioChannels(asset)), "-b:a", "192k")
	case processingTranscode:
		arguments = append(arguments, encoder.codecArguments(processor.threads)...)
		if asset.VideoBitrateKbps > 0 {
			bitrate := strconv.Itoa(asset.VideoBitrateKbps) + "k"
			arguments = append(arguments, "-b:v", bitrate, "-maxrate", bitrate, "-bufsize", strconv.Itoa(asset.VideoBitrateKbps*2)+"k")
		}
		arguments = append(arguments, "-c:a", "aac", "-ac", strconv.Itoa(outputAudioChannels(asset)), "-b:a", "256k")
	default:
		return nil, fmt.Errorf("%w: unsupported mode %q", ErrMediaProcessingFailed, asset.Kind)
	}
	return arguments, nil
}

func processingVideoFilter(asset storedAsset, encoder videoEncoder) string {
	filters := make([]string, 0, 3)
	if asset.TargetHeight > 0 && asset.Decision != nil && asset.Decision.Source != nil &&
		asset.Decision.Source.Height > asset.TargetHeight {
		filters = append(filters, "scale=-2:"+strconv.Itoa(asset.TargetHeight))
	}
	if filter := encoder.filter(asset.ToneMap); filter != "" {
		filters = append(filters, filter)
	}
	return strings.Join(filters, ",")
}

func outputAudioChannels(asset storedAsset) int {
	if asset.MaximumAudioChannels == 1 {
		return 1
	}
	return 2
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
	return processor.runInDirectory(ctx, arguments, destination, "")
}

func (processor *FFmpegProcessor) runInDirectory(ctx context.Context, arguments []string, destination io.Writer, directory string) error {
	command := processor.newCommand(ctx, processor.ffmpegPath, arguments...)
	command.Dir = directory
	diagnostic := newDiagnosticBuffer()
	command.Stdout = destination
	command.Stderr = diagnostic
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrMediaProcessingFailed, err)
	}
	if err := command.Run(); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return fmt.Errorf("%w: %w", ErrMediaProcessingFailed, contextErr)
		}
		message := diagnostic.String()
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

func (processor *FFmpegProcessor) newCommand(ctx context.Context, path string, arguments ...string) *exec.Cmd {
	var command *exec.Cmd
	if processor.commandContext != nil {
		command = processor.commandContext(ctx, path, arguments...)
	} else {
		command = exec.CommandContext(ctx, path, arguments...)
	}
	command.Env = environmentWithoutProxyBypass(command.Env)
	return command
}

func environmentWithoutProxyBypass(environment []string) []string {
	if environment == nil {
		environment = os.Environ()
	}
	filtered := environment[:0]
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToLower(name) {
		case "http_proxy", "https_proxy", "all_proxy", "no_proxy":
			continue
		default:
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func (processor *FFmpegProcessor) startInputEgress(ctx context.Context, asset storedAsset) (*ffmpegEgressProxy, error) {
	if filepath.IsAbs(asset.URL) {
		return nil, nil
	}
	start := processor.egressProxy
	if start == nil {
		start = startFFmpegEgressProxy
	}
	egress, err := start(ctx, asset)
	if err != nil {
		return nil, fmt.Errorf("%w: start guarded media egress: %w", ErrMediaSourceFailed, err)
	}
	if egress == nil {
		return nil, fmt.Errorf("%w: start guarded media egress: proxy unavailable", ErrMediaSourceFailed)
	}
	return egress, nil
}

func guardedCommandAsset(asset storedAsset, egress *ffmpegEgressProxy) (storedAsset, error) {
	if egress == nil {
		return asset, nil
	}
	if egress.InputURL() == "" {
		return storedAsset{}, fmt.Errorf("%w: guarded media input unavailable", ErrMediaSourceFailed)
	}
	asset.URL = egress.InputURL()
	asset.Headers = nil
	return asset, nil
}

func validateMediaSource(ctx context.Context, rawURL string) error {
	if filepath.IsAbs(rawURL) {
		return nil
	}
	if !validMediaURL(rawURL) {
		return fmt.Errorf("%w: outbound media source rejected", ErrMediaSourceFailed)
	}
	if err := netguard.ValidateURL(ctx, rawURL); err != nil {
		return fmt.Errorf("%w: outbound media source rejected: %v", ErrMediaSourceFailed, err)
	}
	return nil
}

func inputProtocolWhitelist(rawURL string) string {
	if filepath.IsAbs(rawURL) {
		return ffmpegLocalInputProtocolWhitelist
	}
	return ffmpegNetworkInputProtocolWhitelist
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
		if !validFFmpegStoredHeader(name, value) {
			continue
		}
		result.WriteString(name)
		result.WriteString(": ")
		result.WriteString(value)
		result.WriteString("\r\n")
	}
	return result.String()
}

func validFFmpegStoredHeader(name, value string) bool {
	return allowedStoredRequestHeader(name) &&
		!strings.EqualFold(strings.TrimSpace(name), "Proxy-Authorization") &&
		!strings.ContainsAny(name, "\r\n:") &&
		!strings.ContainsAny(value, "\r\n")
}

type cappedBuffer struct {
	data     []byte
	maximum  int
	exceeded bool
}

func newCappedBuffer(maximum int) *cappedBuffer {
	return &cappedBuffer{data: make([]byte, 0, maximum), maximum: maximum}
}

func (buffer *cappedBuffer) Write(value []byte) (int, error) {
	total := len(value)
	remaining := buffer.maximum - len(buffer.data)
	if remaining > 0 {
		if remaining > total {
			remaining = total
		}
		buffer.data = append(buffer.data, value[:remaining]...)
	}
	if remaining < total {
		buffer.exceeded = true
	}
	return total, nil
}

func (buffer *cappedBuffer) Bytes() []byte {
	return buffer.data
}

type diagnosticBuffer struct {
	data []byte
}

func newDiagnosticBuffer() *diagnosticBuffer {
	return &diagnosticBuffer{data: make([]byte, 0, maximumMediaDiagnosticBytes)}
}

func (buffer *diagnosticBuffer) Write(value []byte) (int, error) {
	total := len(value)
	if total >= maximumMediaDiagnosticBytes {
		buffer.data = buffer.data[:maximumMediaDiagnosticBytes]
		copy(buffer.data, value[total-maximumMediaDiagnosticBytes:])
		return total, nil
	}
	if overflow := len(buffer.data) + total - maximumMediaDiagnosticBytes; overflow > 0 {
		copy(buffer.data, buffer.data[overflow:])
		buffer.data = buffer.data[:len(buffer.data)-overflow]
	}
	buffer.data = append(buffer.data, value...)
	return total, nil
}

func (buffer *diagnosticBuffer) String() string {
	return strings.TrimSpace(string(buffer.data))
}

type maximumWriter struct {
	destination io.Writer
	remaining   int
	exceeded    bool
}

func (writer *maximumWriter) Write(value []byte) (int, error) {
	if len(value) == 0 {
		return 0, nil
	}
	if writer.remaining <= 0 {
		writer.exceeded = true
		return 0, errMediaOutputLimit
	}
	candidate := value
	if len(candidate) > writer.remaining {
		candidate = candidate[:writer.remaining]
		writer.exceeded = true
	}
	written, err := writer.destination.Write(candidate)
	writer.remaining -= written
	if err != nil {
		return written, err
	}
	if written != len(candidate) {
		return written, io.ErrShortWrite
	}
	if len(candidate) != len(value) {
		return written, errMediaOutputLimit
	}
	return written, nil
}
