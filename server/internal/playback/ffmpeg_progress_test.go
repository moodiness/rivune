package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFFmpegProgressUsesLastCompleteSample(t *testing.T) {
	progress, ok := parseFFmpegProgress([]byte(
		"out_time_us=1250000\nspeed=1.25x\nprogress=continue\n" +
			"out_time_us=2500000\nspeed=2.5x\nprogress=continue\n" +
			"out_time_us=9000000\nspeed=9",
	))
	if !ok || !progress.hasEncodedSeconds || progress.encodedSeconds != 2.5 ||
		!progress.hasSpeed || progress.speed != 2.5 || progress.state != "continue" ||
		!progress.hasStartupDuration || progress.startupDurationSeconds != 1 {
		t.Fatalf("progress = %+v found=%t, want last complete sample with first-sample startup", progress, ok)
	}

	progress, ok = parseFFmpegProgress([]byte("out_time_us=3000000\nspeed=0.75x\nprogress=end\n"))
	if !ok || progress.state != "end" || progress.encodedSeconds != 3 || progress.speed != 0.75 ||
		!progress.hasStartupDuration || progress.startupDurationSeconds != 4 {
		t.Fatalf("terminal progress = %+v found=%t", progress, ok)
	}
}

func TestFFmpegProgressRejectsMalformedNonFiniteAndOverflowValues(t *testing.T) {
	malformed := []string{
		"out_time_us=-1\nout_time_ms=-1\nout_time=00:00:NaN\nspeed=NaNx\nprogress=continue\n",
		"out_time_us=999999999999999999999999\nout_time_ms=999999999999999999999999\nout_time=999999999:00:00\nspeed=+Infx\nprogress=continue\n",
		"out_time_us=1e6\nout_time_ms=1e6\nout_time=00:61:00\nspeed=-1x\nprogress=continue\n",
		"out_time_us=3162240000000001\nout_time_ms=3162240000000001\nout_time=00:00:60\nspeed=1000001x\nprogress=continue\n",
	}
	for _, contents := range malformed {
		progress, ok := parseFFmpegProgress([]byte(contents))
		if !ok || progress.state != "continue" || progress.hasEncodedSeconds || progress.hasSpeed || progress.hasStartupDuration {
			t.Fatalf("malformed progress = %+v found=%t for %q", progress, ok, contents)
		}
	}

	progress, ok := parseFFmpegProgress([]byte(
		"out_time_us=overflow\nout_time_ms=2500000\nout_time=00:00:09.0\nspeed=1.5x\nprogress=continue\n",
	))
	if !ok || !progress.hasEncodedSeconds || progress.encodedSeconds != 2.5 || !progress.hasSpeed || progress.speed != 1.5 {
		t.Fatalf("out_time_ms fallback progress = %+v found=%t", progress, ok)
	}
	progress, ok = parseFFmpegProgress([]byte(
		"out_time_us=overflow\nout_time_ms=overflow\nout_time=01:02:03.5\nprogress=end\n",
	))
	if !ok || progress.encodedSeconds != 3723.5 || progress.state != "end" {
		t.Fatalf("clock fallback progress = %+v found=%t", progress, ok)
	}
	progress, ok = parseFFmpegProgress([]byte("out_time_us=604801000000\nspeed=1x\nprogress=continue\n"))
	if !ok || !progress.hasEncodedSeconds || !progress.hasSpeed || progress.hasStartupDuration {
		t.Fatalf("oversized startup progress = %+v found=%t", progress, ok)
	}
}

func TestReadFFmpegProgressBoundsInputAndIgnoresPartialTail(t *testing.T) {
	directory := t.TempDir()
	contents := append(bytes.Repeat([]byte("ignored=diagnostic\n"), maximumFFmpegProgressBytes/8),
		[]byte("out_time_us=4000000\nspeed=2x\nprogress=continue\nout_time_us=9000000")...)
	if err := os.WriteFile(filepath.Join(directory, ffmpegProgressFilename), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	progress, ok := readFFmpegProgress(directory)
	if !ok || progress.encodedSeconds != 4 || progress.speed != 2 || progress.state != "continue" {
		t.Fatalf("bounded progress = %+v found=%t", progress, ok)
	}
}

func TestReadFFmpegProgressPreservesFirstSampleStartupWhenTailIsBounded(t *testing.T) {
	directory := t.TempDir()
	contents := []byte("out_time_us=2000000\nspeed=2x\nprogress=continue\n")
	contents = append(contents, bytes.Repeat([]byte("ignored=diagnostic\n"), maximumFFmpegProgressBytes/8)...)
	contents = append(contents, []byte("out_time_us=9000000\nspeed=3x\nprogress=continue\n")...)
	if err := os.WriteFile(filepath.Join(directory, ffmpegProgressFilename), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	progress, ok := readFFmpegProgress(directory)
	if !ok || progress.encodedSeconds != 9 || progress.speed != 3 || !progress.hasStartupDuration || progress.startupDurationSeconds != 1 {
		t.Fatalf("bounded head/tail progress = %+v found=%t", progress, ok)
	}
}

func TestProcessHLSAddsControlledProgressDestination(t *testing.T) {
	processor := testFFmpegProcessor()
	processor.maximumReadRate = 2.25
	var captured []string
	processor.commandContext = func(ctx context.Context, path string, arguments ...string) *exec.Cmd {
		captured = append([]string(nil), arguments...)
		return ffmpegHelperCommand(ctx, path, arguments...)
	}
	directory := t.TempDir()
	if err := processor.ProcessHLS(context.Background(), storedAsset{URL: os.Args[0], Kind: processingRemux}, directory); err != nil {
		t.Fatalf("process HLS: %v", err)
	}
	if _, destination := argumentValue(captured, "-progress"); destination != ffmpegProgressFilename || filepath.IsAbs(destination) || strings.ContainsAny(destination, `/\\:`) {
		t.Fatalf("progress destination = %q, want controlled workspace filename; arguments=%v", destination, captured)
	}
	if _, readRate := argumentValue(captured, "-readrate"); readRate != "2.25" {
		t.Fatalf("input read rate = %q, want configured ceiling; arguments=%v", readRate, captured)
	}
	totals := processor.PlaybackDiagnostics().Totals
	if totals.Started != 1 || totals.Succeeded != 1 || totals.Failed != 0 || totals.SoftwareFallbacks != 0 {
		t.Fatalf("successful process totals = %+v", totals)
	}
}

func TestProcessHLSSoftwareRetryResetsProgressBetweenAttempts(t *testing.T) {
	directory := t.TempDir()
	processor := testFFmpegProcessor()
	processor.encoder = videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128"}
	calls := 0
	firstReported := false
	staleRemoved := false
	processor.commandContext = func(ctx context.Context, path string, arguments ...string) *exec.Cmd {
		calls++
		if _, destination := argumentValue(arguments, "-progress"); destination != ffmpegProgressFilename {
			t.Fatalf("attempt %d progress destination = %q", calls, destination)
		}
		command := ffmpegHelperCommand(ctx, path, arguments...)
		switch calls {
		case 1:
			if err := os.WriteFile(filepath.Join(directory, ffmpegProgressFilename), []byte("out_time_us=11000000\nspeed=1x\nprogress=continue\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			first, ok := readFFmpegProgress(directory)
			firstReported = ok && first.encodedSeconds == 11
			command.Env = append(os.Environ(), "RIVUNE_FFMPEG_HELPER_MODE=fail")
		case 2:
			_, err := os.Stat(filepath.Join(directory, ffmpegProgressFilename))
			staleRemoved = os.IsNotExist(err)
			if err := os.WriteFile(filepath.Join(directory, ffmpegProgressFilename), []byte("out_time_us=22000000\nspeed=2x\nprogress=end\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return command
	}
	err := processor.ProcessHLS(context.Background(), storedAsset{
		Kind: processingTranscode, URL: os.Args[0], HLSSegmentContainer: "ts",
		Decision: &PlaybackDecision{Source: &PlaybackDecisionSource{VideoCodec: "h264", Height: 1080}},
	}, directory)
	if err != nil || calls != 2 || !firstReported || !staleRemoved {
		t.Fatalf("software retry err=%v calls=%d firstReported=%t staleRemoved=%t", err, calls, firstReported, staleRemoved)
	}
	progress, ok := readFFmpegProgress(directory)
	if !ok || progress.encodedSeconds != 22 || progress.speed != 2 || progress.state != "end" {
		t.Fatalf("software progress = %+v found=%t", progress, ok)
	}
	totals := processor.PlaybackDiagnostics().Totals
	if totals.Started != 1 || totals.Succeeded != 1 || totals.Failed != 0 || totals.SoftwareFallbacks != 1 {
		t.Fatalf("fallback process totals = %+v", totals)
	}
}

func TestProcessHLSCountsTerminalFailure(t *testing.T) {
	processor := testFFmpegProcessor()
	processor.commandContext = func(ctx context.Context, path string, arguments ...string) *exec.Cmd {
		command := ffmpegHelperCommand(ctx, path, arguments...)
		command.Env = append(os.Environ(), "RIVUNE_FFMPEG_HELPER_MODE=fail")
		return command
	}
	err := processor.ProcessHLS(context.Background(), storedAsset{URL: os.Args[0], Kind: processingRemux}, t.TempDir())
	if err == nil {
		t.Fatal("expected FFmpeg failure")
	}
	totals := processor.PlaybackDiagnostics().Totals
	if totals.Started != 1 || totals.Succeeded != 0 || totals.Failed != 1 || totals.SoftwareFallbacks != 0 {
		t.Fatalf("failed process totals = %+v", totals)
	}
}

func TestProcessHLSSourceFailureDoesNotRetrySoftware(t *testing.T) {
	processor := testFFmpegProcessor()
	processor.encoder = videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128"}
	calls := 0
	processor.commandContext = func(ctx context.Context, path string, arguments ...string) *exec.Cmd {
		calls++
		command := ffmpegHelperCommand(ctx, path, arguments...)
		command.Env = append(os.Environ(), "RIVUNE_FFMPEG_HELPER_MODE=source-fail")
		return command
	}
	err := processor.ProcessHLS(context.Background(), storedAsset{URL: os.Args[0], Kind: processingTranscode}, t.TempDir())
	if !errors.Is(err, ErrMediaSourceFailed) || calls != 1 {
		t.Fatalf("source failure err=%v calls=%d, want one attempt", err, calls)
	}
	totals := processor.PlaybackDiagnostics().Totals
	if totals.Started != 1 || totals.Succeeded != 0 || totals.Failed != 1 || totals.SoftwareFallbacks != 0 {
		t.Fatalf("source failure totals = %+v", totals)
	}
}

func TestProcessHLSCancellationIsNotCountedAsFailure(t *testing.T) {
	processor := testFFmpegProcessor()
	processor.encoder = videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128"}
	started := make(chan struct{})
	processor.commandContext = func(ctx context.Context, path string, arguments ...string) *exec.Cmd {
		close(started)
		command := ffmpegHelperCommand(ctx, path, arguments...)
		command.Env = append(os.Environ(), "RIVUNE_FFMPEG_HELPER_MODE=sleep")
		return command
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- processor.ProcessHLS(ctx, storedAsset{URL: os.Args[0], Kind: processingTranscode}, t.TempDir())
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled process error = %v", err)
	}
	totals := processor.PlaybackDiagnostics().Totals
	if totals.Started != 1 || totals.Succeeded != 0 || totals.Failed != 0 || totals.SoftwareFallbacks != 0 {
		t.Fatalf("cancelled process totals = %+v", totals)
	}
}

func TestActivityJobsPreferNativeProgressAndExposeNoRawData(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte("#EXTM3U\n#EXTINF:10,\nsegment-000000.ts\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := "https://provider.example/private/media?token=secret"
	progressContents := "diagnostic=" + secret + "\nout_time_us=4000000\nspeed=3.5x\nprogress=end\n"
	if err := os.WriteFile(filepath.Join(directory, ffmpegProgressFilename), []byte(progressContents), 0o600); err != nil {
		t.Fatal(err)
	}
	service := &Service{
		now: func() time.Time { return now },
		hlsJobs: map[string]*hlsJob{"job": {
			directory: directory, sessionID: "session-1", assetID: "asset-1", mode: processingTranscode,
			sourceDurationSeconds: 20, createdAt: now.Add(-5 * time.Second), lastAccessed: now,
		}},
	}
	jobs := service.activityJobs()
	if len(jobs) != 1 || jobs[0].ProgressPercent == nil || *jobs[0].ProgressPercent != 20 ||
		jobs[0].Speed == nil || *jobs[0].Speed != 3.5 || jobs[0].StartupDurationSeconds == nil ||
		*jobs[0].StartupDurationSeconds != 4.0/3.5 || jobs[0].State != "complete" {
		t.Fatalf("native activity jobs = %+v", jobs)
	}
	encoded, err := json.Marshal(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "diagnostic") || strings.Contains(string(encoded), progressContents) {
		t.Fatalf("activity exposed raw progress data: %s", encoded)
	}
	if err := os.Remove(filepath.Join(directory, ffmpegProgressFilename)); err != nil {
		t.Fatal(err)
	}
	jobs = service.activityJobs()
	if len(jobs) != 1 || jobs[0].ProgressPercent == nil || *jobs[0].ProgressPercent != 50 ||
		jobs[0].Speed == nil || *jobs[0].Speed != 2 || jobs[0].StartupDurationSeconds != nil || jobs[0].State != "processing" {
		t.Fatalf("playlist fallback activity jobs = %+v", jobs)
	}
}
