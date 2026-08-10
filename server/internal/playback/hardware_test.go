package playback

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestAutomaticVideoEncoderKindsUseAvailableHardware(t *testing.T) {
	tests := []struct {
		name     string
		vendor   string
		nvidia   bool
		render   bool
		expected []videoEncoderKind
	}{
		{name: "AMD render node", vendor: "0x1002", render: true, expected: []videoEncoderKind{videoEncoderVAAPI}},
		{name: "Intel render node", vendor: "0x8086", render: true, expected: []videoEncoderKind{videoEncoderQSV, videoEncoderVAAPI}},
		{name: "NVIDIA devices", nvidia: true, expected: []videoEncoderKind{videoEncoderNVENC}},
		{name: "NVIDIA and AMD", vendor: "0x1002", nvidia: true, render: true, expected: []videoEncoderKind{videoEncoderNVENC, videoEncoderVAAPI}},
		{name: "no mapped device", vendor: "0x1002", expected: []videoEncoderKind{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := automaticVideoEncoderKinds(test.vendor, test.nvidia, test.render)
			if !reflect.DeepEqual(actual, test.expected) {
				t.Fatalf("unexpected encoder candidates: got %v, want %v", actual, test.expected)
			}
		})
	}
}

func TestTranscodeArgumentsMatchSelectedEncoder(t *testing.T) {
	tests := []struct {
		name     string
		encoder  videoEncoder
		expected []string
		excluded []string
	}{
		{
			name:     "software",
			encoder:  videoEncoder{kind: videoEncoderSoftware},
			expected: []string{"-c:v libx264", "-threads 6", "-pix_fmt yuv420p"},
			excluded: []string{"-init_hw_device", "hwupload"},
		},
		{
			name:     "AMD VAAPI",
			encoder:  videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128"},
			expected: []string{"-init_hw_device vaapi=hw:/dev/dri/renderD128", "-vf format=nv12,hwupload", "-c:v h264_vaapi"},
			excluded: []string{"libx264", "-rc_mode", "-qp"},
		},
		{
			name:     "Intel Quick Sync",
			encoder:  videoEncoder{kind: videoEncoderQSV, device: "/dev/dri/renderD128"},
			expected: []string{"-init_hw_device qsv=hw@va", "hwupload=extra_hw_frames=64", "-c:v h264_qsv"},
			excluded: []string{"libx264", "-global_quality"},
		},
		{
			name:     "NVIDIA NVENC",
			encoder:  videoEncoder{kind: videoEncoderNVENC},
			expected: []string{"-c:v h264_nvenc", "-preset p4", "-tune ll"},
			excluded: []string{"-init_hw_device", "libx264"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			processor := &FFmpegProcessor{threads: 6, encoder: test.encoder}
			arguments, err := processor.processingArguments(storedAsset{
				Kind: processingTranscode, URL: "https://media.example/movie.mkv", VideoBitrateKbps: 4500,
			})
			if err != nil {
				t.Fatalf("build processing arguments: %v", err)
			}
			joined := strings.Join(arguments, " ")
			for _, expected := range test.expected {
				if !strings.Contains(joined, expected) {
					t.Fatalf("missing %q in arguments: %v", expected, arguments)
				}
			}
			if !strings.Contains(joined, "-b:v 4500k -maxrate 4500k -bufsize 9000k") {
				t.Fatalf("selected encoder did not honor the absolute bitrate ceiling: %v", arguments)
			}
			if !strings.Contains(joined, "-c:a aac -ac 2") {
				t.Fatalf("transcoded audio was not normalized to browser-compatible stereo: %v", arguments)
			}
			for _, excluded := range test.excluded {
				if strings.Contains(joined, excluded) {
					t.Fatalf("unexpected %q in arguments: %v", excluded, arguments)
				}
			}
		})
	}
}

func TestVAAPIUsesHardwareToneMappingOnlyAfterSuccessfulProbe(t *testing.T) {
	softwareFilter := videoEncoder{kind: videoEncoderVAAPI}.filter(true)
	if !strings.Contains(softwareFilter, softwareToneMapFilter) || strings.Contains(softwareFilter, "tonemap_vaapi") {
		t.Fatalf("VAAPI without tone-map capability should retain the software filter: %q", softwareFilter)
	}

	hardwareFilter := videoEncoder{kind: videoEncoderVAAPI, toneMapBackend: videoToneMapVAAPI}.filter(true)
	if !strings.Contains(hardwareFilter, "tonemap_vaapi") || strings.Contains(hardwareFilter, "zscale") {
		t.Fatalf("VAAPI tone-map capability should use the hardware filter: %q", hardwareFilter)
	}
	vulkanFilter := videoEncoder{kind: videoEncoderVAAPI, toneMapBackend: videoToneMapVulkan}.filter(true)
	if !strings.Contains(vulkanFilter, "libplacebo=") || !strings.Contains(vulkanFilter, "hwmap=derive_device=vulkan") || strings.Contains(vulkanFilter, "zscale") {
		t.Fatalf("Vulkan tone-map capability should use libplacebo surfaces: %q", vulkanFilter)
	}
}

func TestHardwareToneMapProbeOrderUsesVulkanFirstOnlyForAMD(t *testing.T) {
	tests := []struct {
		name       string
		vendor     string
		fail       map[videoToneMapBackend]bool
		want       videoToneMapBackend
		wantProbes []videoToneMapBackend
	}{
		{name: "AMD selects Vulkan when both succeed", vendor: "0x1002", want: videoToneMapVulkan, wantProbes: []videoToneMapBackend{videoToneMapVulkan}},
		{name: "AMD falls back to VAAPI", vendor: "0x1002", fail: map[videoToneMapBackend]bool{videoToneMapVulkan: true}, want: videoToneMapVAAPI, wantProbes: []videoToneMapBackend{videoToneMapVulkan, videoToneMapVAAPI}},
		{name: "other vendors retain VAAPI priority", vendor: "0x8086", want: videoToneMapVAAPI, wantProbes: []videoToneMapBackend{videoToneMapVAAPI}},
		{name: "failed hardware probes retain software fallback", vendor: "0x1002", fail: map[videoToneMapBackend]bool{videoToneMapVulkan: true, videoToneMapVAAPI: true}, wantProbes: []videoToneMapBackend{videoToneMapVulkan, videoToneMapVAAPI}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var probes []videoToneMapBackend
			selected := detectHardwareToneMapWithProbe(videoEncoder{kind: videoEncoderVAAPI}, test.vendor, func(candidate videoEncoder) error {
				probes = append(probes, candidate.toneMapBackend)
				if test.fail[candidate.toneMapBackend] {
					return errors.New("probe failed")
				}
				return nil
			})
			if selected.toneMapBackend != test.want || !reflect.DeepEqual(probes, test.wantProbes) {
				t.Fatalf("selected backend/probes = %q/%v, want %q/%v", selected.toneMapBackend, probes, test.want, test.wantProbes)
			}
		})
	}
}

func TestDetectVideoEncoderRejectsUnknownModeBeforeProbe(t *testing.T) {
	_, err := detectVideoEncoder("unused-ffmpeg", FFmpegOptions{HardwareAcceleration: "vaapi;rm -rf /"})
	if err == nil || !strings.Contains(err.Error(), "unsupported hardware acceleration mode") {
		t.Fatalf("unknown encoder mode error = %v", err)
	}
}

func TestHardwareDecodeAndFiltersUseZeroCopyOnlyWhenSafe(t *testing.T) {
	baseAsset := storedAsset{
		Kind: processingTranscode, URL: "https://media.example/movie.mkv",
		Decision: &PlaybackDecision{Source: &PlaybackDecisionSource{VideoCodec: "h264", Height: 2160}},
	}
	tests := []struct {
		name       string
		encoder    videoEncoder
		mutate     func(*storedAsset)
		expected   []string
		unexpected []string
	}{
		{name: "VAAPI direct surfaces", encoder: videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128"}, expected: []string{"-hwaccel vaapi -hwaccel_device hw -hwaccel_output_format vaapi"}, unexpected: []string{"hwupload"}},
		{name: "QSV scale surfaces", encoder: videoEncoder{kind: videoEncoderQSV, device: "/dev/dri/renderD128"}, mutate: func(asset *storedAsset) { asset.TargetHeight = 1080 }, expected: []string{"-hwaccel qsv -hwaccel_device hw -hwaccel_output_format qsv", "-vf scale_qsv=w=-2:h=1080:format=nv12"}, unexpected: []string{"hwupload", "scale=-2:1080"}},
		{name: "NVENC direct CUDA surfaces", encoder: videoEncoder{kind: videoEncoderNVENC}, expected: []string{"-hwaccel cuda -hwaccel_output_format cuda"}, unexpected: []string{"hwupload"}},
		{name: "NVENC scaling stays on CPU", encoder: videoEncoder{kind: videoEncoderNVENC}, mutate: func(asset *storedAsset) { asset.TargetHeight = 1080 }, expected: []string{"-vf scale=-2:1080"}, unexpected: []string{"-hwaccel cuda", "scale_cuda"}},
		{name: "software tone map downloads ten-bit VAAPI frames", encoder: videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128"}, mutate: func(asset *storedAsset) {
			asset.Decision.Source.VideoCodec = "h265"
			asset.ToneMap = true
			asset.VideoBitDepth = 10
		}, expected: []string{"-hwaccel vaapi -hwaccel_device hw -hwaccel_output_format vaapi", "-vf hwdownload,format=p010le," + softwareToneMapFilter + ",format=nv12,hwupload"}, unexpected: []string{"tonemap_vaapi", "libplacebo"}},
		{name: "unknown tone-map bit depth stays on CPU", encoder: videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128"}, mutate: func(asset *storedAsset) { asset.Decision.Source.VideoCodec = "h265"; asset.ToneMap = true }, expected: []string{softwareToneMapFilter + ",format=nv12,hwupload"}, unexpected: []string{"-hwaccel vaapi", "hwdownload", "tonemap_vaapi"}},
		{name: "hybrid tone map scales after download", encoder: videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128"}, mutate: func(asset *storedAsset) {
			asset.Decision.Source.VideoCodec = "h265"
			asset.ToneMap = true
			asset.VideoBitDepth = 10
			asset.TargetHeight = 1080
		}, expected: []string{"hwdownload,format=p010le,scale=-2:1080," + softwareToneMapFilter + ",format=nv12,hwupload"}, unexpected: []string{"scale_vaapi", "tonemap_vaapi"}},
		{name: "subtitle burn stays CPU then uploads", encoder: videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128"}, mutate: func(asset *storedAsset) {
			index := 3
			asset.SubtitleTrackIndex = &index
			asset.SubtitleTrackType = subtitleBurnBitmap
			asset.SubtitleTrackOrdinal = 0
		}, expected: []string{"overlay=eof_action=pass:repeatlast=0,format=nv12,hwupload"}, unexpected: []string{"-hwaccel vaapi"}},
		{name: "probed VAAPI tone map stays on surfaces", encoder: videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128", toneMapBackend: videoToneMapVAAPI}, mutate: func(asset *storedAsset) { asset.ToneMap = true; asset.TargetHeight = 1080 }, expected: []string{"-hwaccel vaapi", "-vf tonemap_vaapi=format=nv12:matrix=bt709:primaries=bt709:transfer=bt709,scale_vaapi=w=-2:h=1080:format=nv12"}, unexpected: []string{"hwupload", "zscale"}},
		{name: "probed Vulkan tone map crosses shared DRM surfaces", encoder: videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128", toneMapBackend: videoToneMapVulkan}, mutate: func(asset *storedAsset) { asset.ToneMap = true; asset.TargetHeight = 1080 }, expected: []string{"-init_hw_device drm=dr:/dev/dri/renderD128", "-init_hw_device vaapi=hw@dr", "-init_hw_device vulkan=vk@dr", "-hwaccel vaapi", "hwmap=derive_device=vulkan", "libplacebo=", "hwmap=derive_device=vaapi", "scale_vaapi=w=-2:h=1080:format=nv12"}, unexpected: []string{"hwdownload", "zscale"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			asset := baseAsset
			if test.mutate != nil {
				test.mutate(&asset)
			}
			arguments, err := (&FFmpegProcessor{threads: 4, encoder: test.encoder}).processingArguments(asset)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(arguments, " ")
			for _, expected := range test.expected {
				if !strings.Contains(joined, expected) {
					t.Fatalf("arguments missing %q: %v", expected, arguments)
				}
			}
			for _, unexpected := range test.unexpected {
				if strings.Contains(joined, unexpected) {
					t.Fatalf("arguments unexpectedly contain %q: %v", unexpected, arguments)
				}
			}
		})
	}
}
