package playback

import (
	"path/filepath"
	"testing"
)

func TestExternalMediaTranscodesWebMAVIMPEGPSIntoPlayableHLS(t *testing.T) {
	fixture := newExternalMediaFixture(t)
	tests := []struct {
		name, filename, container string
		output                    []string
	}{
		{
			name: "WebM VP9 Opus", filename: "source.webm", container: "webm",
			output: []string{"-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-threads", "1", "-c:a", "libopus"},
		},
		{
			name: "AVI MPEG4 PCM", filename: "source.avi", container: "avi",
			output: []string{"-c:v", "mpeg4", "-q:v", "10", "-c:a", "pcm_s16le"},
		},
		{
			name: "MPEG-PS MPEG2 MP2", filename: "source.mpg", container: "mpeg",
			output: []string{"-c:v", "mpeg2video", "-q:v", "10", "-c:a", "mp2", "-f", "mpeg"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), test.filename)
			arguments := []string{
				"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
				"-f", "lavfi", "-i", "testsrc2=size=160x90:rate=10:duration=2",
				"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2",
				"-map", "0:v:0", "-map", "1:a:0",
			}
			arguments = append(arguments, test.output...)
			arguments = append(arguments, "-shortest", source)
			runExternalMediaCommand(t, fixture.processor.ffmpegPath, arguments...)

			inspection, err := fixture.processor.Probe(externalMediaTestContext(t), storedAsset{URL: source, Container: test.container})
			if err != nil || len(inspection.VideoTracks) != 1 || len(inspection.AudioTracks) != 1 {
				t.Fatalf("probe %s: inspection=%+v err=%v", test.name, inspection, err)
			}
			directory := t.TempDir()
			asset := storedAsset{
				ID: test.container, Kind: processingTranscode, URL: source, Container: test.container,
				HLSSegmentContainer: "mp4", TargetHeight: 64, VideoBitrateKbps: 300, MaximumAudioChannels: 2,
				Decision: &PlaybackDecision{
					Source: &PlaybackDecisionSource{VideoCodec: inspection.VideoTracks[0].Codec, Height: inspection.VideoTracks[0].Height},
					Target: &PlaybackDecisionTarget{VideoCodec: "h264", AudioCodec: "aac", Height: 64, VideoBitrateKbps: 300},
				},
			}
			if err := fixture.processor.ProcessHLS(externalMediaTestContext(t), asset, directory); err != nil {
				t.Fatalf("transcode %s: %v", test.name, err)
			}
			probe := probeExternalMedia(t, fixture, filepath.Join(directory, "index.m3u8"))
			var videoCodec, audioCodec string
			var width, height int
			for _, stream := range probe.Streams {
				switch stream.CodecType {
				case "video":
					videoCodec, width, height = stream.CodecName, stream.Width, stream.Height
				case "audio":
					audioCodec = stream.CodecName
				}
			}
			if videoCodec != "h264" || audioCodec != "aac" || width != 114 || height != 64 {
				t.Fatalf("transcoded %s output video=%s audio=%s size=%dx%d probe=%+v", test.name, videoCodec, audioCodec, width, height, probe)
			}
		})
	}
}

func TestExternalMediaTranscodesCommonTheaterAudioCodecsToAAC(t *testing.T) {
	fixture := newExternalMediaFixture(t)
	tests := []struct {
		name, encoder string
		extra         []string
	}{
		{name: "AC-3", encoder: "ac3"},
		{name: "E-AC-3", encoder: "eac3"},
		{name: "TrueHD", encoder: "truehd", extra: []string{"-strict", "experimental"}},
		{name: "DTS", encoder: "dca", extra: []string{"-strict", "experimental"}},
		{name: "FLAC", encoder: "flac"},
		{name: "Opus", encoder: "libopus"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), test.encoder+".mkv")
			arguments := []string{
				"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
				"-f", "lavfi", "-i", "testsrc2=size=160x90:rate=10:duration=2",
				"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2",
				"-map", "0:v:0", "-map", "1:a:0",
				"-c:v", "libx264", "-preset", "ultrafast", "-threads", "1", "-pix_fmt", "yuv420p",
				"-c:a", test.encoder,
			}
			arguments = append(arguments, test.extra...)
			arguments = append(arguments, "-shortest", source)
			runExternalMediaCommand(t, fixture.processor.ffmpegPath, arguments...)

			inspection, err := fixture.processor.Probe(externalMediaTestContext(t), storedAsset{URL: source, Container: "mkv"})
			if err != nil || len(inspection.VideoTracks) != 1 || len(inspection.AudioTracks) != 1 {
				t.Fatalf("probe %s: inspection=%+v err=%v", test.name, inspection, err)
			}
			audioIndex := inspection.AudioTracks[0].Index
			directory := t.TempDir()
			if err := fixture.processor.ProcessHLS(externalMediaTestContext(t), storedAsset{
				ID: test.encoder, Kind: processingTranscodeAudio, URL: source, Container: "mkv",
				HLSSegmentContainer: "ts", AudioTrackIndex: &audioIndex, MaximumAudioChannels: 2,
			}, directory); err != nil {
				t.Fatalf("transcode %s: %v", test.name, err)
			}
			probe := probeExternalMedia(t, fixture, filepath.Join(directory, "index.m3u8"))
			var videoCodec, audioCodec string
			for _, stream := range probe.Streams {
				switch stream.CodecType {
				case "video":
					videoCodec = stream.CodecName
				case "audio":
					audioCodec = stream.CodecName
				}
			}
			if videoCodec != "h264" || audioCodec != "aac" {
				t.Fatalf("transcoded %s output video=%s audio=%s probe=%+v", test.name, videoCodec, audioCodec, probe)
			}
		})
	}
}

func TestExternalMediaHDR10ToneMapProducesPlayableSDR(t *testing.T) {
	fixture := newExternalMediaFixture(t)
	source := filepath.Join(t.TempDir(), "synthetic-hdr10.mkv")
	runExternalMediaCommand(t, fixture.processor.ffmpegPath,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=160x90:rate=10:duration=2",
		"-vf", "format=yuv420p10le", "-c:v", "libx265", "-preset", "ultrafast", "-x265-params", "log-level=error:colorprim=9:transfer=16:colormatrix=9", "-threads", "1",
		source,
	)
	sourceProbe := probeExternalMedia(t, fixture, source)
	if len(sourceProbe.Streams) != 1 || sourceProbe.Streams[0].PixelFormat != "yuv420p10le" ||
		sourceProbe.Streams[0].ColorTransfer != "smpte2084" || sourceProbe.Streams[0].ColorPrimaries != "bt2020" {
		t.Fatalf("synthetic HDR10 source metadata = %+v", sourceProbe.Streams)
	}

	directory := t.TempDir()
	if err := fixture.processor.ProcessHLS(externalMediaTestContext(t), storedAsset{
		ID: "hdr10", Kind: processingTranscode, URL: source, Container: "mkv", HLSSegmentContainer: "mp4",
		ToneMap: true, TargetHeight: 64, VideoBitrateKbps: 300, MaximumAudioChannels: 2,
		Decision: &PlaybackDecision{
			Source: &PlaybackDecisionSource{VideoCodec: "h265", Height: 90, HDRFormat: "hdr10"},
			Target: &PlaybackDecisionTarget{VideoCodec: "h264", Height: 64, VideoBitrateKbps: 300},
		},
	}, directory); err != nil {
		t.Fatalf("tone-map synthetic HDR10: %v", err)
	}
	playlist := filepath.Join(directory, "index.m3u8")
	output := probeExternalMedia(t, fixture, playlist)
	if len(output.Streams) != 1 || output.Streams[0].CodecName != "h264" || output.Streams[0].PixelFormat != "yuv420p" ||
		output.Streams[0].Width != 114 || output.Streams[0].Height != 64 ||
		output.Streams[0].ColorTransfer != "bt709" || output.Streams[0].ColorPrimaries != "bt709" || output.Streams[0].ColorSpace != "bt709" {
		t.Fatalf("tone-mapped SDR output = %+v", output.Streams)
	}
	frame := runExternalMediaCommand(t, fixture.processor.ffmpegPath,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-i", playlist,
		"-map", "0:v:0", "-frames:v", "1", "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1",
	)
	if len(frame) != 114*64*3 {
		t.Fatalf("decoded tone-mapped frame bytes = %d, want %d", len(frame), 114*64*3)
	}
}
