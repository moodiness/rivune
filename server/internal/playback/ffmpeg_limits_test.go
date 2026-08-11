package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCappedBufferDiscardsProbeOutputBeyondLimit(t *testing.T) {
	buffer := newCappedBuffer(4)
	if written, err := buffer.Write([]byte("abcdef")); err != nil || written != 6 {
		t.Fatalf("write = %d, %v; want 6, nil", written, err)
	}
	if !buffer.exceeded || string(buffer.Bytes()) != "abcd" {
		t.Fatalf("capped output = %q exceeded=%t", buffer.Bytes(), buffer.exceeded)
	}
}

func TestDiagnosticBufferRetainsOnlyLatestBoundedOutput(t *testing.T) {
	buffer := newDiagnosticBuffer()
	prefix := strings.Repeat("a", maximumMediaDiagnosticBytes)
	if _, err := buffer.Write([]byte(prefix + "tail")); err != nil {
		t.Fatal(err)
	}
	if len(buffer.data) != maximumMediaDiagnosticBytes || !strings.HasSuffix(buffer.String(), "tail") {
		t.Fatalf("diagnostic size=%d suffix=%q", len(buffer.data), buffer.String()[len(buffer.String())-4:])
	}
}

func TestMediaVersionDiagnosticIsBoundedAndScrubbed(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		product string
		want    string
	}{
		{name: "ffmpeg", output: "ffmpeg version 7.1.2-1+deb12u1 Copyright private build path", product: "ffmpeg", want: "7.1.2-1+deb12u1"},
		{name: "ffprobe", output: "ffprobe version N-117000-gabc123\nconfiguration: --extra-secret", product: "ffprobe", want: "N-117000-gabc123"},
		{name: "wrong product", output: "ffmpeg version 7.1", product: "ffprobe", want: "unknown"},
		{name: "path token", output: "ffmpeg version /private/build/token", product: "ffmpeg", want: "unknown"},
		{name: "oversized", output: "ffmpeg version " + strings.Repeat("a", maximumMediaVersionBytes+1), product: "ffmpeg", want: "unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := scrubMediaVersion(test.output, test.product); got != test.want {
				t.Fatalf("scrubbed version = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFFmpegDiagnosticsReportEveryIndependentPool(t *testing.T) {
	processor := &FFmpegProcessor{
		ffmpegVersion: "7.1", ffprobeVersion: "7.1", hardwareAcceleration: "auto", threads: 6,
		encoder: videoEncoder{kind: videoEncoderVAAPI, toneMapBackend: videoToneMapVAAPI},
		slots:   make(chan struct{}, 4), probeSlots: make(chan struct{}, 3),
		subtitleSlots: make(chan struct{}, 2), trickplaySlots: make(chan struct{}, 1),
	}
	processor.slots <- struct{}{}
	processor.probeSlots <- struct{}{}
	processor.probeSlots <- struct{}{}
	processor.subtitleSlots <- struct{}{}
	processor.trickplaySlots <- struct{}{}
	diagnostics := processor.PlaybackDiagnostics()
	if diagnostics.FFmpegVersion != "7.1" || diagnostics.FFprobeVersion != "7.1" ||
		diagnostics.HardwareAcceleration != "auto" || diagnostics.VideoEncoder != "vaapi" ||
		!diagnostics.HardwareToneMap || diagnostics.ToneMapBackend != "vaapi" || diagnostics.TranscodeThreads != 6 ||
		diagnostics.Pools.Process != (MediaDiagnosticPool{Active: 1, Limit: 4}) ||
		diagnostics.Pools.Probe != (MediaDiagnosticPool{Active: 2, Limit: 3}) ||
		diagnostics.Pools.Subtitle != (MediaDiagnosticPool{Active: 1, Limit: 2}) ||
		diagnostics.Pools.Trickplay != (MediaDiagnosticPool{Active: 1, Limit: 1}) {
		t.Fatalf("media diagnostics = %+v", diagnostics)
	}
	encoded, err := json.Marshal(diagnostics)
	if err != nil || !strings.Contains(string(encoded), `"hardwareToneMap":true,"toneMapBackend":"vaapi"`) {
		t.Fatalf("serialized media diagnostics = %s, %v", encoded, err)
	}
	software := (&FFmpegProcessor{}).PlaybackDiagnostics()
	if software.HardwareToneMap || software.ToneMapBackend != "software" {
		t.Fatalf("software tone-map diagnostics = %+v", software)
	}
	hybrid := (&FFmpegProcessor{
		hardwareAcceleration: "hybrid",
		encoder:              videoEncoder{kind: videoEncoderVAAPI, toneMapBackend: videoToneMapHybrid},
	}).PlaybackDiagnostics()
	if hybrid.HardwareAcceleration != "hybrid" || hybrid.VideoEncoder != "vaapi" || hybrid.HardwareToneMap || hybrid.ToneMapBackend != "hybrid" {
		t.Fatalf("hybrid diagnostics = %+v", hybrid)
	}
}

func TestMaximumWriterStopsConvertedSubtitleAtLimit(t *testing.T) {
	var destination bytes.Buffer
	writer := &maximumWriter{destination: &destination, remaining: 4}
	written, err := writer.Write([]byte("abcdef"))
	if written != 4 || !errors.Is(err, errMediaOutputLimit) || !writer.exceeded {
		t.Fatalf("write = %d, %v exceeded=%t", written, err, writer.exceeded)
	}
	if destination.String() != "abcd" {
		t.Fatalf("destination = %q", destination.String())
	}
}

type countingWriter struct {
	written int
}

func (writer *countingWriter) Write(value []byte) (int, error) {
	writer.written += len(value)
	return len(value), nil
}

func TestFFmpegSubprocessHelper(t *testing.T) {
	switch os.Getenv("RIVUNE_FFMPEG_HELPER_MODE") {
	case "":
		return
	case "probe":
		_, _ = io.WriteString(os.Stdout, `{"streams":[{"index":0,"codec_type":"video","codec_name":"h264"}],"format":{"format_name":"mov","duration":"1"}}`)
		os.Exit(0)
	case "probe-profile":
		_, _ = io.WriteString(os.Stdout, `{"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","level":153,"color_transfer":"bt709"}],"format":{"format_name":"mov","duration":"1","bit_rate":"2000000"}}`)
		os.Exit(0)
	case "probe-rich":
		_, _ = io.WriteString(os.Stdout, `{"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","profile":"Main 10","level":153,"width":3840,"height":2160,"pix_fmt":"yuv420p10le","avg_frame_rate":"24000/1001","r_frame_rate":"24/1","color_range":"tv","color_space":"bt2020nc","color_transfer":"smpte2084","color_primaries":"bt2020","bit_rate":"12000000","codec_tag_string":"hvc1","tags":{"language":"eng","title":"Feature"},"disposition":{"attached_pic":0,"forced":0,"default":1}},{"index":1,"codec_type":"audio","codec_name":"aac","channels":6,"channel_layout":"5.1","sample_rate":"48000","bit_rate":"640000","disposition":{"default":1}},{"index":2,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"fra"},"disposition":{"forced":1,"default":0}}],"format":{"format_name":"matroska,webm","duration":"120.5","bit_rate":"12640000","size":"189600000"}}`)
		os.Exit(0)
	case "probe-dovi":
		_, _ = io.WriteString(os.Stdout, `{"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","codec_tag_string":"dvh1","side_data_list":[{"side_data_type":"DOVI configuration record","dv_profile":8,"dv_level":6,"rpu_present_flag":1,"el_present_flag":0,"bl_present_flag":1,"dv_bl_signal_compatibility_id":1}]}],"format":{"format_name":"mov","duration":"1"}}`)
		os.Exit(0)
	case "probe-malformed-optional":
		_, _ = io.WriteString(os.Stdout, `{"streams":[{"index":-2,"codec_type":"video","codec_name":"h264","profile":{},"level":"bad","width":-1920,"height":"bad","pix_fmt":"unknown","bits_per_raw_sample":"NaN","avg_frame_rate":"1/0","r_frame_rate":"-25/1","color_range":"unknown","color_space":{},"color_transfer":"n/a","color_primaries":"-Inf","bit_rate":"-1000","tags":[],"disposition":{"attached_pic":-1,"forced":"bad","default":-1},"side_data_list":{}}],"format":{"format_name":"mov,mp4","duration":"NaN","bit_rate":"-1","size":"Inf"}}`)
		os.Exit(0)
	case "subtitle-output":
		chunk := bytes.Repeat([]byte("x"), 64<<10)
		for total := 0; total <= maximumConvertedSubtitleBytes; total += len(chunk) {
			if _, err := os.Stdout.Write(chunk); err != nil {
				os.Exit(0)
			}
		}
		os.Exit(0)
	case "source-fail":
		_, _ = io.WriteString(os.Stderr, "Invalid data found when processing input")
		os.Exit(2)
	case "sleep":
		time.Sleep(time.Hour)
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

func ffmpegHelperCommand(ctx context.Context, _ string, _ ...string) *exec.Cmd {
	return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFFmpegSubprocessHelper$", "--")
}

func testFFmpegProcessor() *FFmpegProcessor {
	return &FFmpegProcessor{
		ffmpegPath: os.Args[0], ffprobePath: os.Args[0],
		slots: make(chan struct{}, 1), probeSlots: make(chan struct{}, 1), subtitleSlots: make(chan struct{}, 1),
		threads: 1, encoder: videoEncoder{kind: videoEncoderSoftware}, subtitleTimeout: subtitleConversionTimeout,
		commandContext: ffmpegHelperCommand,
	}
}

func TestMediaSubprocessesRejectSensitiveTargetsBeforeStarting(t *testing.T) {
	tests := []struct {
		name string
		run  func(*FFmpegProcessor) error
	}{
		{
			name: "probe metadata",
			run: func(processor *FFmpegProcessor) error {
				_, err := processor.Probe(context.Background(), storedAsset{URL: "http://169.254.169.254/latest/meta-data"})
				return err
			},
		},
		{
			name: "subtitle loopback",
			run: func(processor *FFmpegProcessor) error {
				return processor.ConvertSubtitle(context.Background(), storedAsset{URL: "http://127.0.0.1/subtitle.srt"}, io.Discard)
			},
		},
		{
			name: "HLS metadata",
			run: func(processor *FFmpegProcessor) error {
				return processor.ProcessHLS(
					context.Background(),
					storedAsset{URL: "http://169.254.169.254/video.mp4", Kind: processingRemux},
					t.TempDir(),
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			processor := testFFmpegProcessor()
			started := false
			processor.commandContext = func(ctx context.Context, path string, arguments ...string) *exec.Cmd {
				started = true
				return ffmpegHelperCommand(ctx, path, arguments...)
			}
			err := test.run(processor)
			if !errors.Is(err, ErrMediaSourceFailed) {
				t.Fatalf("sensitive media target error = %v", err)
			}
			if started {
				t.Fatal("media subprocess started for a sensitive network target")
			}
		})
	}
}

func TestProbeConcurrencyIsBoundedBeforeStartingSubprocess(t *testing.T) {
	processor := testFFmpegProcessor()
	processor.probeSlots <- struct{}{}
	started := false
	processor.commandContext = func(ctx context.Context, path string, arguments ...string) *exec.Cmd {
		started = true
		return ffmpegHelperCommand(ctx, path, arguments...)
	}
	_, err := processor.Probe(context.Background(), storedAsset{URL: "https://1.1.1.1/video.mp4"})
	if !errors.Is(err, ErrMediaCapacityReached) {
		t.Fatalf("probe over capacity error = %v", err)
	}
	if started {
		t.Fatal("probe subprocess started after its semaphore was full")
	}
}

func TestProbeRestrictsInputProtocols(t *testing.T) {
	t.Setenv("RIVUNE_FFMPEG_HELPER_MODE", "probe")
	processor := testFFmpegProcessor()
	var captured []string
	processor.commandContext = func(ctx context.Context, path string, arguments ...string) *exec.Cmd {
		captured = append([]string(nil), arguments...)
		return ffmpegHelperCommand(ctx, path, arguments...)
	}
	if _, err := processor.Probe(context.Background(), storedAsset{URL: "https://1.1.1.1/video.mp4"}); err != nil {
		t.Fatalf("probe legitimate media: %v", err)
	}
	for index := 0; index+1 < len(captured); index++ {
		if captured[index] == "-protocol_whitelist" && captured[index+1] == ffmpegNetworkInputProtocolWhitelist {
			return
		}
	}
	t.Fatalf("probe arguments omitted protocol whitelist: %v", captured)
}

func TestProbeCarriesInspectedVideoLevelAndRange(t *testing.T) {
	t.Setenv("RIVUNE_FFMPEG_HELPER_MODE", "probe-profile")
	processor := testFFmpegProcessor()
	var captured []string
	processor.commandContext = func(ctx context.Context, path string, arguments ...string) *exec.Cmd {
		captured = append([]string(nil), arguments...)
		return ffmpegHelperCommand(ctx, path, arguments...)
	}
	inspection, err := processor.Probe(context.Background(), storedAsset{URL: "https://1.1.1.1/video.mp4"})
	if err != nil || len(inspection.VideoTracks) != 1 || inspection.VideoTracks[0].Level != 153 || inspection.VideoTracks[0].VideoRangeType != "SDR" ||
		inspection.BitrateKbps != 2000 || inspection.VideoTracks[0].BitrateKbps != 2000 || !strings.Contains(strings.Join(captured, " "), "profile,level,width") {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
}

func TestProbePopulatesRichInternalMetadata(t *testing.T) {
	t.Setenv("RIVUNE_FFMPEG_HELPER_MODE", "probe-rich")
	processor := testFFmpegProcessor()
	var captured []string
	processor.commandContext = func(ctx context.Context, path string, arguments ...string) *exec.Cmd {
		captured = append([]string(nil), arguments...)
		return ffmpegHelperCommand(ctx, path, arguments...)
	}
	inspection, err := processor.Probe(context.Background(), storedAsset{URL: "https://1.1.1.1/movie.mkv", Container: "mkv"})
	if err != nil {
		t.Fatalf("probe rich metadata: %v", err)
	}
	if inspection.Container != "mkv" || inspection.DurationSeconds != 120.5 || inspection.BitrateKbps != 12640 || inspection.SizeBytes != 189600000 || inspection.HDRFormat != "hdr10" {
		t.Fatalf("format inspection = %+v", inspection)
	}
	if len(inspection.VideoTracks) != 1 {
		t.Fatalf("video tracks = %+v", inspection.VideoTracks)
	}
	video := inspection.VideoTracks[0]
	if video.PixelFormat != "yuv420p10le" || video.BitDepth != 10 || video.FrameRate < 23.975 || video.FrameRate > 23.977 ||
		video.ColorRange != "tv" || video.ColorSpace != "bt2020nc" || video.ColorTransfer != "smpte2084" || video.ColorPrimaries != "bt2020" ||
		video.BitrateKbps != 12000 || video.VideoRangeType != "HDR10" || !video.Default {
		t.Fatalf("video track = %+v", video)
	}
	if len(inspection.AudioTracks) != 1 || inspection.AudioTracks[0].ChannelLayout != "5.1" || inspection.AudioTracks[0].SampleRate != 48000 || !inspection.AudioTracks[0].Default {
		t.Fatalf("audio tracks = %+v", inspection.AudioTracks)
	}
	if len(inspection.SubtitleTracks) != 1 || !inspection.SubtitleTracks[0].Forced || inspection.SubtitleTracks[0].Default {
		t.Fatalf("subtitle tracks = %+v", inspection.SubtitleTracks)
	}
	joined := strings.Join(captured, " ")
	if !strings.Contains(joined, "pix_fmt,bits_per_raw_sample,avg_frame_rate,r_frame_rate") ||
		!strings.Contains(joined, "stream_disposition=attached_pic,forced,default") ||
		!strings.Contains(joined, "format=format_name,duration,bit_rate,size") {
		t.Fatalf("probe arguments omitted rich metadata: %v", captured)
	}
	encoded, err := json.Marshal(inspection)
	if err != nil {
		t.Fatalf("marshal inspection: %v", err)
	}
	for _, internalValue := range []string{"yuv420p10le", "bt2020nc", "189600000", "12640", "5.1", "48000"} {
		if strings.Contains(string(encoded), internalValue) {
			t.Fatalf("public inspection JSON exposed internal value %q: %s", internalValue, encoded)
		}
	}
}

func TestProbeCollectsDolbyVisionEvidenceWithoutPublicExposure(t *testing.T) {
	t.Setenv("RIVUNE_FFMPEG_HELPER_MODE", "probe-dovi")
	processor := testFFmpegProcessor()
	var captured []string
	processor.commandContext = func(ctx context.Context, path string, arguments ...string) *exec.Cmd {
		captured = append([]string(nil), arguments...)
		return ffmpegHelperCommand(ctx, path, arguments...)
	}
	inspection, err := processor.Probe(context.Background(), storedAsset{URL: "https://1.1.1.1/movie.mp4"})
	if err != nil || inspection.HDRFormat != "dolby_vision" || len(inspection.VideoTracks) != 1 {
		t.Fatalf("DOVI inspection=%+v err=%v", inspection, err)
	}
	video := inspection.VideoTracks[0]
	if video.DolbyVisionProfile != 8 || video.DolbyVisionLevel != 6 || !video.DolbyVisionRPUPresent ||
		video.DolbyVisionELPresent || !video.DolbyVisionBLPresent || video.DolbyVisionCompatibilityID != 1 {
		t.Fatalf("DOVI evidence = %+v", video)
	}
	if !strings.Contains(strings.Join(captured, " "), "dv_profile,dv_level,rpu_present_flag,el_present_flag,bl_present_flag,dv_bl_signal_compatibility_id") {
		t.Fatalf("probe arguments omitted DOVI evidence: %v", captured)
	}
	encoded, err := json.Marshal(inspection)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"DolbyVision", "dv_profile", "compatibility"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("public inspection exposed %q: %s", private, encoded)
		}
	}
}

func TestProbeIgnoresMalformedOptionalMetadata(t *testing.T) {
	t.Setenv("RIVUNE_FFMPEG_HELPER_MODE", "probe-malformed-optional")
	inspection, err := testFFmpegProcessor().Probe(context.Background(), storedAsset{URL: "https://1.1.1.1/movie.mp4"})
	if err != nil {
		t.Fatalf("probe with malformed optional metadata: %v", err)
	}
	if len(inspection.VideoTracks) != 1 {
		t.Fatalf("video tracks = %+v", inspection.VideoTracks)
	}
	video := inspection.VideoTracks[0]
	if inspection.DurationSeconds != 0 || inspection.BitrateKbps != 0 || inspection.SizeBytes != 0 ||
		video.Index != 0 || video.Level != 0 || video.Width != 0 || video.Height != 0 || video.BitrateKbps != 0 ||
		video.BitDepth != 0 || video.FrameRate != 0 || video.PixelFormat != "" || video.ColorRange != "" || video.ColorSpace != "" ||
		video.ColorTransfer != "" || video.ColorPrimaries != "" || video.Forced || video.Default {
		t.Fatalf("malformed optional metadata was not normalized: inspection=%+v video=%+v", inspection, video)
	}
}

func TestProcessHLSAppliesReadRate(t *testing.T) {
	processor := testFFmpegProcessor()
	var captured []string
	processor.commandContext = func(ctx context.Context, path string, arguments ...string) *exec.Cmd {
		captured = append([]string(nil), arguments...)
		return ffmpegHelperCommand(ctx, path, arguments...)
	}
	if err := processor.ProcessHLS(
		context.Background(),
		storedAsset{URL: os.Args[0], Kind: processingRemux},
		t.TempDir(),
	); err != nil {
		t.Fatalf("process HLS: %v", err)
	}
	if _, readRate := argumentValue(captured, "-readrate"); readRate != "1.5" {
		t.Fatalf("HLS read rate = %q, want 1.5; arguments=%v", readRate, captured)
	}
	captured = nil
	if err := processor.ProcessHLS(
		context.Background(),
		storedAsset{
			URL: os.Args[0], Kind: processingTranscode, HLSSegmentContainer: "ts", DurationSeconds: 2 * 60 * 60,
		},
		t.TempDir(),
	); err != nil {
		t.Fatalf("process seekable HLS: %v", err)
	}
	wantReadRate := strconv.FormatFloat(seekableTranscodeReadRate(defaultTranscodeMaximumReadRate, 2*60*60), 'f', -1, 64)
	if _, readRate := argumentValue(captured, "-readrate"); readRate != wantReadRate {
		t.Fatalf("seekable HLS read rate = %q, want %s; arguments=%v", readRate, wantReadRate, captured)
	}
	captured = nil
	remainingSeconds := float64(6 * 60 * 60)
	if err := processor.ProcessHLS(
		context.Background(),
		storedAsset{URL: os.Args[0], Kind: processingTranscode, HLSSegmentContainer: "ts", DurationSeconds: remainingSeconds},
		t.TempDir(),
	); err != nil {
		t.Fatalf("process long seekable HLS: %v", err)
	}
	_, serializedReadRate := argumentValue(captured, "-readrate")
	parsedReadRate, err := strconv.ParseFloat(serializedReadRate, 64)
	if err != nil {
		t.Fatalf("parse serialized read rate %q: %v", serializedReadRate, err)
	}
	leadAtCompletion := remainingSeconds - remainingSeconds/parsedReadRate
	windowBudgetSeconds := float64(hlsProductionLeadSegments * hlsSegmentDurationSeconds)
	if parsedReadRate <= 1 || leadAtCompletion > windowBudgetSeconds {
		t.Fatalf("serialized read rate %q leads by %.3fs, budget %.3fs", serializedReadRate, leadAtCompletion, windowBudgetSeconds)
	}
}

func TestProcessHLSFallsBackToSoftwareOnlyBeforePlaylistPublication(t *testing.T) {
	tests := []struct {
		name             string
		publishOnFailure bool
		wantCalls        int
		wantError        bool
	}{
		{name: "startup failure retries software", wantCalls: 2},
		{name: "published playlist forbids encoder switch", publishOnFailure: true, wantCalls: 1, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			processor := testFFmpegProcessor()
			processor.encoder = videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128"}
			var calls [][]string
			processor.commandContext = func(ctx context.Context, path string, arguments ...string) *exec.Cmd {
				calls = append(calls, append([]string(nil), arguments...))
				command := ffmpegHelperCommand(ctx, path, arguments...)
				if len(calls) == 1 {
					if test.publishOnFailure {
						if err := os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte("#EXTM3U\n"), 0o600); err != nil {
							t.Fatalf("publish fixture playlist: %v", err)
						}
					}
					command.Env = append(os.Environ(), "RIVUNE_FFMPEG_HELPER_MODE=fail")
				}
				return command
			}
			err := processor.ProcessHLS(context.Background(), storedAsset{
				Kind: processingTranscode, URL: os.Args[0], HLSSegmentContainer: "ts", ToneMap: true, VideoBitDepth: 10, TargetHeight: 2160,
				Decision: &PlaybackDecision{
					Source: &PlaybackDecisionSource{VideoCodec: "h265", Height: 2160},
					Target: &PlaybackDecisionTarget{VideoCodec: "h264", Height: 2160},
				},
			}, directory)
			if (err != nil) != test.wantError || len(calls) != test.wantCalls {
				t.Fatalf("ProcessHLS err=%v calls=%d want error=%t calls=%d", err, len(calls), test.wantError, test.wantCalls)
			}
			first := strings.Join(calls[0], " ")
			if !strings.Contains(first, "-c:v h264_vaapi") || !strings.Contains(first, "hwdownload,format=p010le") {
				t.Fatalf("first attempt was not hybrid VAAPI: %v", calls[0])
			}
			if len(calls) == 2 {
				fallback := strings.Join(calls[1], " ")
				if !strings.Contains(fallback, "-c:v libx264") || !strings.Contains(fallback, "scale=-2:1080,"+softwareToneMapFilter) ||
					strings.Contains(fallback, "h264_vaapi") || strings.Contains(fallback, "-hwaccel") || strings.Contains(fallback, "hwdownload") {
					t.Fatalf("fallback was not capped clean software argv: %v", calls[1])
				}
			}
		})
	}
}

func TestConvertedSubtitleExcessFailsAtOutputLimit(t *testing.T) {
	t.Setenv("RIVUNE_FFMPEG_HELPER_MODE", "subtitle-output")
	processor := testFFmpegProcessor()
	destination := &countingWriter{}
	err := processor.ConvertSubtitle(
		context.Background(),
		storedAsset{URL: "https://1.1.1.1/subtitle.srt"},
		destination,
	)
	if !errors.Is(err, ErrMediaProcessingFailed) || !strings.Contains(err.Error(), "converted subtitle exceeds") {
		t.Fatalf("excessive subtitle conversion error = %v", err)
	}
	if destination.written != maximumConvertedSubtitleBytes {
		t.Fatalf("converted subtitle output = %d bytes, want %d", destination.written, maximumConvertedSubtitleBytes)
	}
}

func TestConvertedSubtitleHasServerOwnedDeadline(t *testing.T) {
	t.Setenv("RIVUNE_FFMPEG_HELPER_MODE", "sleep")
	processor := testFFmpegProcessor()
	processor.subtitleTimeout = 50 * time.Millisecond
	err := processor.ConvertSubtitle(
		context.Background(),
		storedAsset{URL: "https://1.1.1.1/subtitle.srt"},
		io.Discard,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("subtitle conversion deadline error = %v", err)
	}
}
