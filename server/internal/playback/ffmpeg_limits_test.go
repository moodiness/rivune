package playback

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
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
		_, _ = io.WriteString(os.Stdout, `{"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","level":153,"color_transfer":"bt709"}],"format":{"format_name":"mov","duration":"1"}}`)
		os.Exit(0)
	case "subtitle-output":
		chunk := bytes.Repeat([]byte("x"), 64<<10)
		for total := 0; total <= maximumConvertedSubtitleBytes; total += len(chunk) {
			if _, err := os.Stdout.Write(chunk); err != nil {
				os.Exit(0)
			}
		}
		os.Exit(0)
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
		!strings.Contains(strings.Join(captured, " "), "profile,level,width") {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
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
	if _, readRate := argumentValue(captured, "-readrate"); readRate != "1.50" {
		t.Fatalf("HLS read rate = %q, want 1.50; arguments=%v", readRate, captured)
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
