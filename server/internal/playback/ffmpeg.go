package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/netguard"
	"golang.org/x/net/http/httpguts"
)

const (
	mediaProbeTimeout                   = 15 * time.Second
	subtitleConversionTimeout           = 30 * time.Second
	maximumMediaProbeOutputBytes        = 4 << 20
	maximumConvertedSubtitleBytes       = 16 << 20
	maximumMediaDiagnosticBytes         = 2048
	maximumMediaVersionOutputBytes      = 4 << 10
	maximumMediaVersionBytes            = 64
	mediaVersionTimeout                 = 2 * time.Second
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
	ffmpegPath           string
	ffprobePath          string
	ffmpegVersion        string
	ffprobeVersion       string
	hardwareAcceleration string
	slots                chan struct{}
	probeSlots           chan struct{}
	subtitleSlots        chan struct{}
	trickplaySlots       chan struct{}
	threads              int
	encoder              videoEncoder
	maximumReadRate      float64
	metrics              ffmpegProcessMetrics
	subtitleTimeout      time.Duration
	commandContext       func(context.Context, string, ...string) *exec.Cmd
	egressProxy          func(context.Context, storedAsset) (*ffmpegEgressProxy, error)
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
	hardwareAcceleration := strings.ToLower(strings.TrimSpace(options.HardwareAcceleration))
	if hardwareAcceleration == "" {
		hardwareAcceleration = "auto"
	}
	return &FFmpegProcessor{
		ffmpegPath: resolvedFFmpeg, ffprobePath: resolvedFFprobe,
		ffmpegVersion: executableMediaVersion(resolvedFFmpeg, "ffmpeg"), ffprobeVersion: executableMediaVersion(resolvedFFprobe, "ffprobe"),
		hardwareAcceleration: hardwareAcceleration, encoder: encoder, threads: threads,
		maximumReadRate: options.MaximumReadRate,
		slots:           make(chan struct{}, maximumConcurrent), probeSlots: make(chan struct{}, maximumConcurrent),
		subtitleSlots: make(chan struct{}, maximumConcurrent), trickplaySlots: make(chan struct{}, 1), subtitleTimeout: subtitleConversionTimeout,
	}, nil
}

func executableMediaVersion(path, product string) string {
	ctx, cancel := context.WithTimeout(context.Background(), mediaVersionTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, path, "-version")
	output := newCappedBuffer(maximumMediaVersionOutputBytes)
	command.Stdout = output
	if err := command.Run(); err != nil || output.exceeded {
		return "unknown"
	}
	return scrubMediaVersion(string(output.Bytes()), product)
}

func scrubMediaVersion(output, product string) string {
	line, _, _ := strings.Cut(output, "\n")
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 3 || !strings.EqualFold(fields[0], product) || !strings.EqualFold(fields[1], "version") {
		return "unknown"
	}
	return boundedMediaVersion(fields[2])
}

func boundedMediaVersion(version string) string {
	if len(version) == 0 || len(version) > maximumMediaVersionBytes {
		return "unknown"
	}
	for index := range len(version) {
		character := version[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("._+-", rune(character)) {
			continue
		}
		return "unknown"
	}
	return version
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
		"-show_entries", "stream=index,codec_type,codec_name,profile,level,width,height,pix_fmt,bits_per_raw_sample,avg_frame_rate,r_frame_rate,color_range,color_space,color_transfer,color_primaries,channels,channel_layout,sample_rate,bit_rate,codec_tag_string:stream_tags=language,title:stream_disposition=attached_pic,forced,default:stream_side_data=side_data_type,dv_profile,dv_level,rpu_present_flag,el_present_flag,bl_present_flag,dv_bl_signal_compatibility_id:format=format_name,duration,bit_rate,size",
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
	return parseFFprobeInspection(output.Bytes(), asset.Container)
}

type ffprobeValue string

func (value *ffprobeValue) UnmarshalJSON(data []byte) error {
	*value = ""
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*value = ffprobeValue(text)
		return nil
	}
	text = strings.TrimSpace(string(data))
	if _, err := strconv.ParseFloat(text, 64); err == nil {
		*value = ffprobeValue(text)
	}
	return nil
}

type ffprobeText string

func (value *ffprobeText) UnmarshalJSON(data []byte) error {
	*value = ""
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*value = ffprobeText(text)
	}
	return nil
}

type ffprobeStream struct {
	Index            ffprobeValue    `json:"index"`
	CodecType        ffprobeText     `json:"codec_type"`
	CodecName        ffprobeText     `json:"codec_name"`
	Profile          ffprobeText     `json:"profile"`
	Level            ffprobeValue    `json:"level"`
	Width            ffprobeValue    `json:"width"`
	Height           ffprobeValue    `json:"height"`
	PixelFormat      ffprobeText     `json:"pix_fmt"`
	BitsPerRawSample ffprobeValue    `json:"bits_per_raw_sample"`
	AverageFrameRate ffprobeValue    `json:"avg_frame_rate"`
	RealFrameRate    ffprobeValue    `json:"r_frame_rate"`
	ColorRange       ffprobeText     `json:"color_range"`
	ColorSpace       ffprobeText     `json:"color_space"`
	ColorTransfer    ffprobeText     `json:"color_transfer"`
	ColorPrimaries   ffprobeText     `json:"color_primaries"`
	Channels         ffprobeValue    `json:"channels"`
	ChannelLayout    ffprobeText     `json:"channel_layout"`
	SampleRate       ffprobeValue    `json:"sample_rate"`
	BitRate          ffprobeValue    `json:"bit_rate"`
	CodecTagString   ffprobeText     `json:"codec_tag_string"`
	Tags             json.RawMessage `json:"tags"`
	Disposition      json.RawMessage `json:"disposition"`
	SideData         json.RawMessage `json:"side_data_list"`
}

type ffprobeSideData struct {
	Type                    string       `json:"side_data_type"`
	DolbyVisionProfile      ffprobeValue `json:"dv_profile"`
	DolbyVisionLevel        ffprobeValue `json:"dv_level"`
	RPUPresent              ffprobeValue `json:"rpu_present_flag"`
	ELPresent               ffprobeValue `json:"el_present_flag"`
	BLPresent               ffprobeValue `json:"bl_present_flag"`
	BLSignalCompatibilityID ffprobeValue `json:"dv_bl_signal_compatibility_id"`
}

type ffprobeFormat struct {
	Name     ffprobeText  `json:"format_name"`
	Duration ffprobeValue `json:"duration"`
	BitRate  ffprobeValue `json:"bit_rate"`
	Size     ffprobeValue `json:"size"`
}

func parseFFprobeInspection(data []byte, containerHint string) (MediaInspection, error) {
	var response struct {
		Streams []json.RawMessage `json:"streams"`
		Format  json.RawMessage   `json:"format"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return MediaInspection{}, fmt.Errorf("decode FFprobe response: %w", err)
	}
	var format ffprobeFormat
	_ = json.Unmarshal(response.Format, &format)
	inspection := MediaInspection{
		Container:       inspectedContainer(string(format.Name), containerHint),
		DurationSeconds: ffprobeNonNegativeFloat(format.Duration),
		BitrateKbps:     ffprobeKbps(format.BitRate),
		SizeBytes:       ffprobeNonNegativeInt64(format.Size),
		VideoTracks:     make([]MediaTrack, 0),
		AudioTracks:     make([]MediaTrack, 0),
		SubtitleTracks:  make([]MediaTrack, 0),
	}
	for _, encodedStream := range response.Streams {
		var stream ffprobeStream
		if err := json.Unmarshal(encodedStream, &stream); err != nil {
			continue
		}
		language, title := ffprobeStreamTags(stream.Tags)
		attachedPicture, forced, defaultStream := ffprobeStreamDisposition(stream.Disposition)
		pixelFormat := strings.ToLower(ffprobeMetadata(stream.PixelFormat))
		bitDepth := ffprobeNonNegativeInt(stream.BitsPerRawSample)
		if strings.TrimSpace(string(stream.BitsPerRawSample)) == "" {
			bitDepth = knownPixelFormatBitDepth(pixelFormat)
		}
		frameRate := ffprobeFrameRate(stream.AverageFrameRate)
		if frameRate == 0 {
			frameRate = ffprobeFrameRate(stream.RealFrameRate)
		}
		track := MediaTrack{
			Index: ffprobeNonNegativeInt(stream.Index), Type: strings.ToLower(strings.TrimSpace(string(stream.CodecType))),
			Codec: normalizedCodec(string(stream.CodecName)), Profile: strings.TrimSpace(string(stream.Profile)), Level: ffprobeNonNegativeInt(stream.Level),
			Language: language, Title: title, Forced: forced, Default: defaultStream,
			Width: ffprobeNonNegativeInt(stream.Width), Height: ffprobeNonNegativeInt(stream.Height), Channels: ffprobeNonNegativeInt(stream.Channels),
			BitrateKbps: ffprobeKbps(stream.BitRate), PixelFormat: pixelFormat, BitDepth: bitDepth, FrameRate: frameRate,
			ColorRange: ffprobeMetadata(stream.ColorRange), ColorSpace: ffprobeMetadata(stream.ColorSpace),
			ColorTransfer: ffprobeMetadata(stream.ColorTransfer), ColorPrimaries: ffprobeMetadata(stream.ColorPrimaries),
			ChannelLayout: ffprobeMetadata(stream.ChannelLayout), SampleRate: ffprobeNonNegativeInt(stream.SampleRate),
		}
		sideData := ffprobeStreamSideData(stream.SideData)
		switch track.Type {
		case "video":
			if attachedPicture {
				continue
			}
			track.VideoRangeType = inspectedVideoRangeType(string(stream.CodecTagString), track.ColorTransfer, sideData)
			for _, data := range sideData {
				if !strings.Contains(strings.ToLower(data.Type), "dovi") {
					continue
				}
				track.DolbyVisionProfile = ffprobeNonNegativeInt(data.DolbyVisionProfile)
				track.DolbyVisionLevel = ffprobeNonNegativeInt(data.DolbyVisionLevel)
				track.DolbyVisionRPUPresent = ffprobeNonNegativeInt(data.RPUPresent) == 1
				track.DolbyVisionELPresent = ffprobeNonNegativeInt(data.ELPresent) == 1
				track.DolbyVisionBLPresent = ffprobeNonNegativeInt(data.BLPresent) == 1
				track.DolbyVisionCompatibilityID = ffprobeNonNegativeInt(data.BLSignalCompatibilityID)
				break
			}
			inspection.VideoTracks = append(inspection.VideoTracks, track)
			if inspection.HDRFormat == "" {
				inspection.HDRFormat = hdrFormatForVideoRange(track.VideoRangeType)
			}
		case "audio":
			inspection.AudioTracks = append(inspection.AudioTracks, track)
		case "subtitle":
			inspection.SubtitleTracks = append(inspection.SubtitleTracks, track)
		}
	}
	if len(inspection.VideoTracks) > 0 && inspection.VideoTracks[0].BitrateKbps == 0 {
		inspection.VideoTracks[0].BitrateKbps = inspection.BitrateKbps
	}
	if len(inspection.VideoTracks) == 0 {
		return MediaInspection{}, errors.New("FFprobe returned no video stream")
	}
	return inspection, nil
}

func ffprobeStreamTags(data json.RawMessage) (string, string) {
	var tags struct {
		Language ffprobeText `json:"language"`
		Title    ffprobeText `json:"title"`
	}
	_ = json.Unmarshal(data, &tags)
	return strings.TrimSpace(string(tags.Language)), strings.TrimSpace(string(tags.Title))
}

func ffprobeStreamDisposition(data json.RawMessage) (bool, bool, bool) {
	var disposition struct {
		AttachedPicture ffprobeValue `json:"attached_pic"`
		Forced          ffprobeValue `json:"forced"`
		Default         ffprobeValue `json:"default"`
	}
	_ = json.Unmarshal(data, &disposition)
	return ffprobeNonNegativeInt(disposition.AttachedPicture) != 0,
		ffprobeNonNegativeInt(disposition.Forced) != 0,
		ffprobeNonNegativeInt(disposition.Default) != 0
}

func ffprobeStreamSideData(data json.RawMessage) []ffprobeSideData {
	var encoded []ffprobeSideData
	if err := json.Unmarshal(data, &encoded); err != nil {
		return nil
	}
	for index := range encoded {
		encoded[index].Type = strings.TrimSpace(encoded[index].Type)
	}
	return encoded
}

func ffprobeNonNegativeInt(value ffprobeValue) int {
	number := ffprobeNonNegativeInt64(value)
	if uint64(number) > uint64(^uint(0)>>1) {
		return 0
	}
	return int(number)
}

func ffprobeNonNegativeInt64(value ffprobeValue) int64 {
	number, err := strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
	if err != nil || number < 0 {
		return 0
	}
	return number
}

func ffprobeKbps(value ffprobeValue) int {
	number := ffprobeNonNegativeInt64(value) / 1000
	if uint64(number) > uint64(^uint(0)>>1) {
		return 0
	}
	return int(number)
}

func ffprobeNonNegativeFloat(value ffprobeValue) float64 {
	number, err := strconv.ParseFloat(strings.TrimSpace(string(value)), 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return 0
	}
	return number
}

func ffprobeFrameRate(value ffprobeValue) float64 {
	text := strings.TrimSpace(string(value))
	numeratorText, denominatorText, rational := strings.Cut(text, "/")
	if !rational {
		return ffprobeNonNegativeFloat(value)
	}
	if strings.Contains(denominatorText, "/") {
		return 0
	}
	numerator := ffprobeNonNegativeFloat(ffprobeValue(numeratorText))
	denominator := ffprobeNonNegativeFloat(ffprobeValue(denominatorText))
	if denominator == 0 {
		return 0
	}
	rate := numerator / denominator
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
		return 0
	}
	return rate
}

func ffprobeMetadata(value ffprobeText) string {
	text := strings.TrimSpace(string(value))
	switch strings.ToLower(text) {
	case "", "unknown", "unspecified", "none", "n/a", "nan", "inf", "+inf", "-inf", "infinity", "+infinity", "-infinity":
		return ""
	default:
		return text
	}
}

func knownPixelFormatBitDepth(value string) int {
	format := strings.ToLower(strings.TrimSpace(value))
	format = strings.TrimSuffix(strings.TrimSuffix(format, "le"), "be")
	switch format {
	case "yuv410p", "yuv411p", "yuv420p", "yuv422p", "yuv440p", "yuv444p",
		"yuvj420p", "yuvj422p", "yuvj440p", "yuvj444p", "yuva420p", "yuva422p", "yuva444p",
		"nv12", "nv21", "rgb24", "bgr24", "rgba", "bgra", "argb", "abgr", "gbrp", "gbrap", "gray", "gray8", "ya8":
		return 8
	case "p010", "x2rgb10", "x2bgr10", "y210":
		return 10
	case "p012":
		return 12
	case "p016":
		return 16
	}
	for _, bits := range []int{9, 10, 12, 14, 16} {
		suffix := strconv.Itoa(bits)
		if (strings.HasPrefix(format, "yuv") || strings.HasPrefix(format, "gbrp")) && strings.HasSuffix(format, "p"+suffix) {
			return bits
		}
		if (strings.HasPrefix(format, "gray") || strings.HasPrefix(format, "ya")) && strings.HasSuffix(format, suffix) {
			return bits
		}
	}
	return 0
}

func (processor *FFmpegProcessor) ProcessHLS(ctx context.Context, asset storedAsset, directory string) (resultErr error) {
	if err := processor.acquire(ctx); err != nil {
		return err
	}
	processor.metrics.started.Add(1)
	defer func() {
		processor.release()
		switch {
		case resultErr == nil:
			processor.metrics.succeeded.Add(1)
		case !errors.Is(resultErr, context.Canceled):
			processor.metrics.failed.Add(1)
		}
	}()
	_, seekable := seekableHLSSegmentCount(asset)
	readRate := adaptiveTranscodeReadRate(processor.maximumReadRate, len(processor.slots), cap(processor.slots))
	if seekable {
		readRate = seekableTranscodeReadRate(readRate, asset.DurationSeconds-asset.StartSeconds)
	}
	asset.ReadRate = readRate
	resultErr = processor.processHLS(ctx, asset, directory, processor.encoder)
	if resultErr == nil || asset.Kind != processingTranscode || processor.encoder.normalizedKind() == videoEncoderSoftware || hlsOutputStarted(directory) {
		return resultErr
	}
	if errors.Is(resultErr, ErrMediaSourceFailed) || errors.Is(resultErr, context.Canceled) || errors.Is(resultErr, context.DeadlineExceeded) {
		return resultErr
	}
	if resetErr := resetHLSDirectory(directory); resetErr != nil {
		return fmt.Errorf("reset HLS output for software fallback: %w", resetErr)
	}
	processor.metrics.softwareFallbacks.Add(1)
	fallbackAsset := asset
	if fallbackAsset.ToneMap && (fallbackAsset.TargetHeight == 0 || fallbackAsset.TargetHeight > softwareToneMapMaximumHeight) {
		fallbackAsset.TargetHeight = softwareToneMapMaximumHeight
	}
	if fallbackErr := processor.processHLS(ctx, fallbackAsset, directory, videoEncoder{kind: videoEncoderSoftware}); fallbackErr != nil {
		return fmt.Errorf("hardware encoding failed: %v; software fallback failed: %w", resultErr, fallbackErr)
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
		arguments = append(arguments, "-force_key_frames", "expr:gte(t,n_forced*"+strconv.Itoa(hlsSegmentDurationSeconds)+")")
	}
	arguments = append(arguments, "-progress", ffmpegProgressFilename)
	hlsFlags := "independent_segments+temp_file"
	if asset.Kind != processingTranscode {
		hlsFlags = "split_by_time+temp_file"
	}
	arguments = append(arguments, hlsOutputArguments(asset, hlsFlags)...)
	return processor.runInDirectory(ctx, arguments, nil, directory)
}

func hlsOutputArguments(asset storedAsset, hlsFlags string) []string {
	arguments := []string{
		"-f", "hls", "-hls_time", strconv.Itoa(hlsSegmentDurationSeconds),
		"-hls_list_size", strconv.Itoa(hlsRetainedSegments), "-hls_delete_threshold", strconv.Itoa(hlsDeleteThreshold),
	}
	if normalizedHLSSegmentContainer(asset.HLSSegmentContainer) == "mp4" {
		arguments = append(arguments,
			"-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4",
			"-hls_segment_filename", "segment-%06d.m4s",
		)
	} else {
		arguments = append(arguments,
			"-hls_segment_type", "mpegts", "-hls_segment_filename", "segment-%06d.ts",
		)
	}
	return append(arguments, "-hls_flags", hlsFlags+"+delete_segments", "index.m3u8")
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
	arguments = append(arguments, "-i", commandAsset.URL)
	// Text subtitle demuxers may ignore input seeks. Seek after opening the input,
	// then rebase retained cues onto the requested playback timeline below.
	if commandAsset.StartSeconds > 0 {
		start := strconv.FormatFloat(commandAsset.StartSeconds, 'f', -1, 64)
		arguments = append(arguments, "-ss", start)
	}
	arguments = append(arguments, "-map")
	if asset.SubtitleTrackIndex != nil {
		arguments = append(arguments, fmt.Sprintf("0:%d", *asset.SubtitleTrackIndex))
	} else {
		arguments = append(arguments, "0:0")
	}
	arguments = append(arguments, "-c:s", "webvtt")
	if commandAsset.StartSeconds > 0 {
		arguments = append(arguments, "-output_ts_offset", strconv.FormatFloat(-commandAsset.StartSeconds, 'f', -1, 64))
	}
	arguments = append(arguments, "-f", "webvtt", "pipe:1")
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
	if asset.Kind == processingTranscode && asset.ToneMap && !assetToneMappingSupported(asset) {
		return nil, fmt.Errorf("%w: Dolby Vision source has no proven HDR-compatible base layer", ErrMediaProcessingFailed)
	}
	arguments := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", inputProtocolWhitelist(asset.URL),
		"-analyzeduration", "1000000", "-probesize", "1000000",
	}
	if asset.Kind == processingTranscode {
		arguments = append(arguments, encoder.globalArguments()...)
		arguments = append(arguments, encoder.hardwareDecodeArguments(asset)...)
	}
	arguments = append(arguments, ffmpegInputArguments(asset)...)
	if asset.ReadRate > 0 {
		arguments = append(arguments, "-readrate", strconv.FormatFloat(asset.ReadRate, 'f', -1, 64))
	}
	if asset.StartSeconds > 0 {
		arguments = append(arguments, "-ss", strconv.FormatFloat(asset.StartSeconds, 'f', -1, 64))
	}
	arguments = append(arguments, "-i", asset.URL)
	if asset.Kind == processingTranscode && asset.SubtitleTrackIndex != nil {
		complexFilter, err := subtitleBurnFilter(asset)
		if err != nil {
			return nil, err
		}
		if filter := processingVideoFilter(asset, encoder); filter != "" {
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
	if filter := encoder.hybridToneMapFilter(asset); filter != "" {
		return filter
	}
	if filter := encoder.hardwareFilter(asset); filter != "" || encoder.hardwareFramesSafe(asset) {
		return filter
	}
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

func subtitleBurnFilter(asset storedAsset) (string, error) {
	if asset.SubtitleTrackIndex == nil || asset.SubtitleTrackOrdinal < 0 {
		return "", fmt.Errorf("%w: invalid subtitle burn selection", ErrMediaProcessingFailed)
	}
	switch asset.SubtitleTrackType {
	case subtitleBurnText:
		filename, err := subtitleFilterFilename(asset.URL)
		if err != nil {
			return "", err
		}
		filter := fmt.Sprintf("subtitles=filename='%s':si=%d", filename, asset.SubtitleTrackOrdinal)
		if asset.StartSeconds <= 0 {
			return "[0:v:0]" + filter, nil
		}
		start := strconv.FormatFloat(asset.StartSeconds, 'f', -1, 64)
		return "[0:v:0]setpts=PTS+" + start + "/TB," + filter + ",setpts=PTS-" + start + "/TB", nil
	case subtitleBurnBitmap:
		return fmt.Sprintf("[0:v:0][0:s:%d]overlay=eof_action=pass:repeatlast=0", asset.SubtitleTrackOrdinal), nil
	default:
		return "", fmt.Errorf("%w: unsupported subtitle burn type", ErrMediaProcessingFailed)
	}
}

func subtitleFilterFilename(value string) (string, error) {
	value = filepath.ToSlash(value)
	if value == "" || strings.ContainsAny(value, "\x00\r\n'[],;=") {
		return "", fmt.Errorf("%w: unsafe subtitle filter source", ErrMediaProcessingFailed)
	}
	// FFmpeg receives the filter graph as a direct argv value. Escape the colon
	// once for its option parser; forward slashes keep Windows separators inert.
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, ":", `\:`), nil
}

func assetToneMappingSupported(asset storedAsset) bool {
	if asset.Decision == nil || asset.Decision.Source == nil ||
		!strings.EqualFold(strings.TrimSpace(asset.Decision.Source.HDRFormat), "dolby_vision") {
		return true
	}
	return asset.DolbyVisionToneMapSafe
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
	return processor.encoder.usesHardwareToneMap()
}

func (processor *FFmpegProcessor) ActiveProcesses() int {
	return len(processor.slots)
}

func (processor *FFmpegProcessor) ProcessLimit() int {
	return cap(processor.slots)
}

func (processor *FFmpegProcessor) PlaybackDiagnostics() MediaDiagnostics {
	if processor == nil {
		return MediaDiagnostics{
			FFmpegVersion: "unknown", FFprobeVersion: "unknown", HardwareAcceleration: "unknown", VideoEncoder: "unknown",
			MaximumReadRate: defaultTranscodeMaximumReadRate,
		}
	}
	hardwareAcceleration := processor.hardwareAcceleration
	switch hardwareAcceleration {
	case "auto", "software", "vaapi", "qsv", "nvenc":
	default:
		hardwareAcceleration = "unknown"
	}
	videoEncoder := processor.VideoEncoder()
	if videoEncoder == "" {
		videoEncoder = "unknown"
	}
	threads := processor.threads
	if threads < 0 || threads > 32 {
		threads = 0
	}
	return MediaDiagnostics{
		FFmpegVersion: boundedMediaVersion(processor.ffmpegVersion), FFprobeVersion: boundedMediaVersion(processor.ffprobeVersion),
		HardwareAcceleration: hardwareAcceleration, VideoEncoder: videoEncoder,
		MaximumReadRate: adaptiveTranscodeReadRate(processor.maximumReadRate, 1, 1),
		HardwareToneMap: processor.HardwareToneMap(), TranscodeThreads: threads,
		Pools: MediaDiagnosticPools{
			Process: mediaDiagnosticPool(processor.slots), Probe: mediaDiagnosticPool(processor.probeSlots),
			Subtitle: mediaDiagnosticPool(processor.subtitleSlots), Trickplay: mediaDiagnosticPool(processor.trickplaySlots),
		},
		Totals: processor.metrics.snapshot(),
	}
}

func mediaDiagnosticPool(slots chan struct{}) MediaDiagnosticPool {
	return MediaDiagnosticPool{Active: len(slots), Limit: cap(slots)}
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

func inspectedHDRFormat(codecTag, colorTransfer string, sideData []ffprobeSideData) string {
	return hdrFormatForVideoRange(inspectedVideoRangeType(codecTag, colorTransfer, sideData))
}

func inspectedVideoRangeType(codecTag, colorTransfer string, sideData []ffprobeSideData) string {
	tag := strings.ToLower(strings.TrimSpace(codecTag))
	if strings.HasPrefix(tag, "dvh") || strings.HasPrefix(tag, "dva") {
		return "DOVI"
	}
	for _, data := range sideData {
		if strings.Contains(strings.ToLower(data.Type), "dovi") {
			return "DOVI"
		}
	}
	switch strings.ToLower(strings.TrimSpace(colorTransfer)) {
	case "smpte2084":
		return "HDR10"
	case "arib-std-b67":
		return "HLG"
	case "bt709", "bt470bg", "smpte170m", "gamma22", "gamma28":
		return "SDR"
	default:
		return ""
	}
}

func hdrFormatForVideoRange(videoRange string) string {
	switch strings.ToUpper(strings.TrimSpace(videoRange)) {
	case "DOVI":
		return "dolby_vision"
	case "HDR10":
		return "hdr10"
	case "HLG":
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

func ffmpegHeaders(values map[string]string) string {
	headers, validHeaders := canonicalStoredRequestHeaders(values)
	if !validHeaders || len(headers) == 0 {
		return ""
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var result strings.Builder
	for _, name := range names {
		result.WriteString(name)
		result.WriteString(": ")
		result.WriteString(headers.Get(name))
		result.WriteString("\r\n")
	}
	return result.String()
}

func validFFmpegStoredHeader(name, value string) bool {
	return httpguts.ValidHeaderFieldName(name) && allowedStoredRequestHeader(name) &&
		!strings.EqualFold(strings.TrimSpace(name), "Proxy-Authorization") &&
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
