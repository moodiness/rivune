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
			expected: []string{"-init_hw_device", "hwupload=extra_hw_frames=64", "-c:v h264_qsv"},
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
			if !strings.Contains(joined, "-maxrate 4500k -bufsize 9000k") {
				t.Fatalf("selected encoder did not honor the absolute bitrate ceiling: %v", arguments)
			}
			hasTargetBitrate := strings.Contains(joined, "-b:v 4500k")
			if hasTargetBitrate == (test.encoder.normalizedKind() == videoEncoderSoftware) {
				t.Fatalf("target bitrate must select hardware VBR but preserve software capped CRF: %v", arguments)
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
func TestCopyModesNeverInvokeVideoEncoder(t *testing.T) {
	for _, test := range []struct {
		name string
		kind string
		want string
	}{
		{name: "remux copies video and audio", kind: processingRemux, want: "-c:v copy -c:a copy"},
		{name: "audio transcode copies video", kind: processingTranscodeAudio, want: "-c:v copy -c:a aac"},
	} {
		t.Run(test.name, func(t *testing.T) {
			arguments, err := (&FFmpegProcessor{threads: 4, encoder: videoEncoder{kind: videoEncoderAMF}}).processingArguments(storedAsset{
				Kind: test.kind, URL: "https://media.example/movie.mkv",
			})
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(arguments, " ")
			if !strings.Contains(joined, test.want) || strings.Contains(joined, "_amf") || strings.Contains(joined, "-init_hw_device") {
				t.Fatalf("copy arguments = %v, want %q without hardware encoder", arguments, test.want)
			}
		})
	}
}

func TestHEVCMain10TranscodeHonorsAudioCopyAndHVC1SampleEntry(t *testing.T) {
	encoder := videoEncoder{
		kind:         videoEncoderNVENC,
		encodeCodecs: map[string]bool{"hevc": true},
		decodeCodecs: map[string]bool{"hevc": true},
		hevcMain10:   true,
	}
	asset := storedAsset{
		Kind: processingTranscode, URL: "https://media.example/movie.mkv",
		HLSSegmentContainer: "mp4", TargetVideoCodec: "hevc", VideoBitDepth: 10,
		Decision: &PlaybackDecision{
			VideoAction: "transcode", AudioAction: "copy",
			Source: &PlaybackDecisionSource{VideoCodec: "h265", Height: 2160},
			Target: &PlaybackDecisionTarget{VideoCodec: "hevc", AudioCodec: "aac", Height: 2160, VideoBitDepth: 10},
		},
	}
	arguments, err := (&FFmpegProcessor{threads: 4, encoder: encoder}).processingArguments(asset)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	for _, expected := range []string{
		"-hwaccel cuda -hwaccel_output_format cuda",
		"-c:v hevc_nvenc -profile:v main10",
		"-tag:v hvc1",
		"-c:a copy",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("HEVC Main10 arguments missing %q: %v", expected, arguments)
		}
	}
	if strings.Contains(joined, "-c:a aac") || mediaCopyBoundaries(asset) != "audio" {
		t.Fatalf("planned audio copy was not honored: arguments=%v boundaries=%q", arguments, mediaCopyBoundaries(asset))
	}
}

func TestPlatformInventoriesAndDeviceArguments(t *testing.T) {
	if got, want := automaticWindowsVideoEncoderKinds(), []videoEncoderKind{videoEncoderAMF, videoEncoderQSV, videoEncoderNVENC}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Windows automatic backends = %v, want %v", got, want)
	}
	qsvWindows := strings.Join(videoEncoderGlobalArguments(videoEncoder{kind: videoEncoderQSV}, true), " ")
	if qsvWindows != "-init_hw_device qsv=hw:hw,child_device_type=d3d11va -filter_hw_device hw" || strings.Contains(qsvWindows, "vaapi") || strings.Contains(qsvWindows, "/dev/") {
		t.Fatalf("Windows QSV arguments do not force the D3D11VA hardware implementation: %q", qsvWindows)
	}
	qsvLinux := strings.Join(videoEncoderGlobalArguments(videoEncoder{kind: videoEncoderQSV, device: "/dev/dri/renderD128"}, false), " ")
	if !strings.Contains(qsvLinux, "vaapi=va:/dev/dri/renderD128") || !strings.Contains(qsvLinux, "qsv=hw@va") {
		t.Fatalf("Linux QSV arguments lost VAAPI child initialization: %q", qsvLinux)
	}
	amfWindows := strings.Join(videoEncoderGlobalArguments(videoEncoder{kind: videoEncoderAMF}, true), " ")
	if amfWindows != "-init_hw_device d3d11va=hw -filter_hw_device hw" {
		t.Fatalf("Windows AMF initialization = %q", amfWindows)
	}
	if err := videoEncoderPlatformProbeError(videoEncoderAMF, false); err == nil || !strings.Contains(err.Error(), "only available on Windows") {
		t.Fatalf("non-Windows AMF probe error = %v", err)
	}
}

func TestCodecArgumentsCoverBackendsCodecsAndQuality(t *testing.T) {
	all := map[string]bool{"h264": true, "hevc": true, "av1": true}
	backends := []struct {
		kind   videoEncoderKind
		suffix string
	}{
		{kind: videoEncoderSoftware},
		{kind: videoEncoderVAAPI, suffix: "_vaapi"},
		{kind: videoEncoderQSV, suffix: "_qsv"},
		{kind: videoEncoderNVENC, suffix: "_nvenc"},
		{kind: videoEncoderAMF, suffix: "_amf"},
	}
	softwareNames := map[string]string{"h264": "libx264", "hevc": "libx265", "av1": "libsvtav1"}
	for _, backend := range backends {
		for _, codec := range transcodeVideoCodecs {
			t.Run(string(backend.kind)+"/"+codec, func(t *testing.T) {
				encoder := videoEncoder{kind: backend.kind, encodeCodecs: all}
				arguments, err := encoder.codecArguments(codec, "balanced", 6, false)
				if err != nil {
					t.Fatal(err)
				}
				want := codec + backend.suffix
				if backend.kind == videoEncoderSoftware {
					want = softwareNames[codec]
				}
				if joined := strings.Join(arguments, " "); !strings.Contains(joined, "-c:v "+want) {
					t.Fatalf("%s %s arguments = %v", backend.kind, codec, arguments)
				}
			})
		}
	}

	qualityTests := []struct {
		name, quality, want string
		kind                videoEncoderKind
	}{
		{name: "software speed", kind: videoEncoderSoftware, quality: "speed", want: "-preset ultrafast -crf 23"},
		{name: "software balanced", kind: videoEncoderSoftware, quality: "balanced", want: "-preset superfast -crf 18"},
		{name: "software quality", kind: videoEncoderSoftware, quality: "quality", want: "-preset medium -crf 16"},
		{name: "VAAPI speed", kind: videoEncoderVAAPI, quality: "speed", want: "-quality 7"},
		{name: "VAAPI quality", kind: videoEncoderVAAPI, quality: "quality", want: "-quality 1"},
		{name: "QSV quality", kind: videoEncoderQSV, quality: "quality", want: "-preset slow"},
		{name: "NVENC speed", kind: videoEncoderNVENC, quality: "speed", want: "-preset p2"},
		{name: "AMF balanced", kind: videoEncoderAMF, quality: "balanced", want: "-quality balanced"},
	}
	for _, test := range qualityTests {
		t.Run(test.name, func(t *testing.T) {
			arguments, err := (videoEncoder{kind: test.kind, encodeCodecs: all}).codecArguments("h264", test.quality, 4, false)
			if err != nil || !strings.Contains(strings.Join(arguments, " "), test.want) {
				t.Fatalf("quality arguments = %v, error = %v, want %q", arguments, err, test.want)
			}
		})
	}
}

func TestFunctionalCapabilityInventoryAndDefensiveCopies(t *testing.T) {
	var encodeProbes, decodeProbes []string
	encoder := detectVideoEncoderCapabilities(videoEncoder{kind: videoEncoderQSV}, func(_ videoEncoder, codec string, _ bool) error {
		encodeProbes = append(encodeProbes, codec)
		if codec == "av1" {
			return errors.New("missing encoder")
		}
		return nil
	}, func(candidate videoEncoder, codec string) error {
		decodeProbes = append(decodeProbes, codec)
		if !candidate.supportsDecode(codec) {
			return errors.New("probe candidate lacks requested decoder")
		}
		if codec == "h264" {
			return errors.New("missing decoder")
		}
		return nil
	})
	wantProbes := []string{"h264", "hevc", "av1"}
	if !reflect.DeepEqual(encodeProbes, wantProbes) || !reflect.DeepEqual(decodeProbes, wantProbes) {
		t.Fatalf("functional probes = encode %v decode %v", encodeProbes, decodeProbes)
	}
	if !encoder.supportsEncode("h264") || !encoder.supportsEncode("h265") || encoder.supportsEncode("av1") {
		t.Fatalf("functional encode inventory = %v", encoder.supportedEncodeCodecs())
	}
	if encoder.supportsDecode("h264") || !encoder.supportsDecode("h265") || !encoder.supportsDecode("av1") {
		t.Fatalf("functional decode inventory = %v", encoder.supportedDecodeCodecs())
	}

	capabilities := encoder.transcodeCapabilities("h265", "quality")
	if capabilities.HardwareAcceleration != "qsv" || capabilities.PreferredVideoCodec != "hevc" || capabilities.QualityPreset != "quality" ||
		!reflect.DeepEqual(capabilities.EncodeCodecs, []string{"h264", "hevc"}) || !reflect.DeepEqual(capabilities.DecodeCodecs, []string{"hevc", "av1"}) {
		t.Fatalf("transcode capabilities = %+v", capabilities)
	}
	capabilities.EncodeCodecs[0] = "corrupt"
	capabilities.DecodeCodecs[0] = "corrupt"
	fresh := encoder.transcodeCapabilities("h265", "quality")
	if fresh.EncodeCodecs[0] != "h264" || fresh.DecodeCodecs[0] != "hevc" {
		t.Fatalf("capability slices were not defensive copies: %+v", fresh)
	}
}
func TestSoftwareDecodeInventoryPublishesOnlySuccessfulProbes(t *testing.T) {
	var decoded []string
	encoder := detectVideoEncoderCapabilities(videoEncoder{kind: videoEncoderSoftware}, func(videoEncoder, string, bool) error {
		return nil
	}, func(_ videoEncoder, codec string) error {
		decoded = append(decoded, codec)
		if codec != "hevc" {
			return errors.New("decoder unavailable")
		}
		return nil
	})
	if !reflect.DeepEqual(decoded, []string{"h264", "hevc", "av1"}) || !reflect.DeepEqual(encoder.supportedDecodeCodecs(), []string{"hevc"}) {
		t.Fatalf("software decode probes=%v inventory=%v", decoded, encoder.supportedDecodeCodecs())
	}
	arguments, input, err := videoDecoderProbeArguments(videoEncoder{kind: videoEncoderSoftware}.withDecodeCodec("hevc"), "hevc")
	joined := strings.Join(arguments, " ")
	if err != nil || len(input) == 0 || !strings.Contains(joined, "-f hevc -i pipe:0 -map 0:v:0 -frames:v 1 -an -f null -") ||
		strings.Contains(joined, "-hwaccel") || strings.Contains(joined, "-init_hw_device") {
		t.Fatalf("software decoder probe arguments=%v input=%d error=%v", arguments, len(input), err)
	}
}

func TestMain10CapabilityComesOnlyFromCombinedProbe(t *testing.T) {
	calls := 0
	candidate := detectVideoEncoderMain10(videoEncoder{kind: videoEncoderNVENC}, func(probeCandidate videoEncoder) error {
		calls++
		if !probeCandidate.supportsDecode("hevc") || !probeCandidate.supportsEncode("hevc") {
			return errors.New("combined probe did not receive HEVC paths")
		}
		return nil
	})
	failed := detectVideoEncoderMain10(videoEncoder{kind: videoEncoderNVENC}, func(videoEncoder) error {
		return errors.New("Main10 path unavailable")
	})
	software := detectVideoEncoderMain10(videoEncoder{kind: videoEncoderSoftware}, func(probeCandidate videoEncoder) error {
		calls++
		if !probeCandidate.supportsDecode("hevc") || !probeCandidate.supportsEncode("hevc") {
			return errors.New("combined software probe did not receive HEVC paths")
		}
		return nil
	})
	if calls != 2 || !candidate.hevcMain10 || failed.hevcMain10 || !software.hevcMain10 {
		t.Fatalf("Main10 detection calls=%d success=%t failed=%t software=%t", calls, candidate.hevcMain10, failed.hevcMain10, software.hevcMain10)
	}
}

func TestExplicitEncodeOnlyBackendRemainsSelectable(t *testing.T) {
	encoder, err := detectExplicitVideoEncoder("nvenc", "", func(_ videoEncoder, codec string, _ bool) error {
		if codec != "h264" {
			return errors.New("encoder unavailable")
		}
		return nil
	}, func(videoEncoder, string) error {
		return errors.New("decoder unavailable")
	}, func(candidate videoEncoder) videoEncoder { return candidate })
	if err != nil {
		t.Fatal(err)
	}
	if !encoder.supportsEncode("h264") || len(encoder.supportedDecodeCodecs()) != 0 {
		t.Fatalf("encode-only backend inventory = encode %v decode %v", encoder.supportedEncodeCodecs(), encoder.supportedDecodeCodecs())
	}
}

func TestHardwareDecoderProbeArgumentsUseEmbeddedFrameAndRealBackend(t *testing.T) {
	tests := []struct {
		name, codec, format, acceleration string
		encoder                           videoEncoder
	}{
		{name: "VAAPI H264", codec: "h264", format: "h264", acceleration: "-hwaccel vaapi -hwaccel_device hw -hwaccel_output_format vaapi", encoder: videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128"}},
		{name: "QSV H265 alias", codec: "h265", format: "hevc", acceleration: "-hwaccel qsv -hwaccel_device hw -hwaccel_output_format qsv", encoder: videoEncoder{kind: videoEncoderQSV, device: "/dev/dri/renderD128"}},
		{name: "NVENC AV1", codec: "av1", format: "ivf", acceleration: "-hwaccel cuda -hwaccel_output_format cuda", encoder: videoEncoder{kind: videoEncoderNVENC}},
		{name: "AMF H264", codec: "h264", format: "h264", acceleration: "-hwaccel d3d11va -hwaccel_device hw -hwaccel_output_format d3d11", encoder: videoEncoder{kind: videoEncoderAMF}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoder := test.encoder.withDecodeCodec(test.codec)
			arguments, input, err := videoDecoderProbeArguments(encoder, test.codec)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(arguments, " ")
			for _, expected := range []string{test.acceleration, "-f " + test.format + " -i pipe:0", "-map 0:v:0 -frames:v 1 -an -f null -"} {
				if !strings.Contains(joined, expected) {
					t.Fatalf("decoder probe arguments missing %q: %v", expected, arguments)
				}
			}
			if (encoder.normalizedKind() == videoEncoderVAAPI || encoder.normalizedKind() == videoEncoderQSV) && !strings.Contains(joined, "-init_hw_device") {
				t.Fatalf("decoder probe omitted global hardware initialization: %v", arguments)
			}
			if strings.Count(joined, "-frames:v 1") != 1 || strings.Contains(joined, "sh -c") || strings.Contains(joined, "cmd /c") {
				t.Fatalf("decoder probe is not a single direct frame decode: %v", arguments)
			}
			if len(input) == 0 || len(input) > 1024 {
				t.Fatalf("decoder probe fixture size = %d", len(input))
			}
			switch test.format {
			case "h264":
				if len(input) < 5 || !reflect.DeepEqual(input[:5], []byte{0, 0, 0, 1, 0x67}) {
					t.Fatalf("H264 probe is not an Annex B SPS/IDR stream: %x", input)
				}
			case "hevc":
				if len(input) < 11 || !reflect.DeepEqual(input[:4], []byte{0, 0, 0, 1}) || input[10]&0x1f != 1 {
					t.Fatalf("HEVC probe is not a Main profile Annex B stream: %x", input)
				}
			case "ivf":
				if len(input) < 4 || string(input[:4]) != "DKIF" {
					t.Fatalf("AV1 probe is not an IVF stream: %x", input)
				}
			}
		})
	}
}
func TestHEVCMain10ProbeUsesRealHardwareDecodeAndEncodePaths(t *testing.T) {
	encoder := videoEncoder{kind: videoEncoderQSV, device: "/dev/dri/renderD128"}.withDecodeCodec("hevc").withEncodeCodec("hevc")
	arguments, input, err := videoEncoderMain10ProbeArguments(encoder)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	for _, expected := range []string{
		"-init_hw_device", "-hwaccel qsv -hwaccel_device hw -hwaccel_output_format qsv",
		"-f hevc -i pipe:0 -map 0:v:0", "-c:v hevc_qsv -profile:v main10", "-frames:v 1 -an -f null -",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("Main10 probe missing %q: %v", expected, arguments)
		}
	}
	if strings.Count(joined, "-frames:v 1") != 1 || len(input) < 11 || input[10]&0x1f != 2 {
		t.Fatalf("Main10 probe is not exactly one embedded Main10 frame: arguments=%v input=%x", arguments, input)
	}
}

func TestHEVCMain10ProbeUsesRealSoftwareFallbackPath(t *testing.T) {
	encoder := videoEncoder{kind: videoEncoderSoftware}.withDecodeCodec("hevc").withEncodeCodec("hevc")
	arguments, input, err := videoEncoderMain10ProbeArguments(encoder)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	for _, expected := range []string{
		"-f hevc -i pipe:0 -map 0:v:0", "-c:v libx265 -profile:v main10", "-pix_fmt yuv420p10le", "-frames:v 1 -an -f null -",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("software Main10 probe missing %q: %v", expected, arguments)
		}
	}
	if strings.Contains(joined, "-hwaccel") || strings.Contains(joined, "-init_hw_device") || strings.Count(joined, "-frames:v 1") != 1 || len(input) < 11 {
		t.Fatalf("software Main10 probe did not exercise exactly one real frame: arguments=%v input=%x", arguments, input)
	}
}

func TestTenBitSourceToMainTargetConvertsExplicitly(t *testing.T) {
	encoder := videoEncoder{kind: videoEncoderNVENC, hevcMain10: true, encodeCodecs: map[string]bool{"hevc": true}, decodeCodecs: map[string]bool{"hevc": true}}
	asset := storedAsset{
		Kind: processingTranscode, URL: "https://media.example/movie.mkv", HLSSegmentContainer: "mp4", TargetVideoCodec: "hevc", VideoBitDepth: 10,
		Decision: &PlaybackDecision{Source: &PlaybackDecisionSource{VideoCodec: "hevc", Height: 2160}, Target: &PlaybackDecisionTarget{VideoCodec: "hevc", Height: 2160, VideoBitDepth: 8}},
	}
	arguments, err := (&FFmpegProcessor{threads: 4, encoder: encoder}).processingArguments(asset)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	if strings.Contains(joined, "-hwaccel") || strings.Contains(joined, "-profile:v main10") || !strings.Contains(joined, "-vf format=yuv420p") || !strings.Contains(joined, "-profile:v main") {
		t.Fatalf("10-bit to Main conversion is not explicit and safe: %v", arguments)
	}
}

func TestMain10SoftwareFallbackPreservesTargetDepth(t *testing.T) {
	encoder := videoEncoder{kind: videoEncoderSoftware, hevcMain10: true, encodeCodecs: map[string]bool{"hevc": true}, decodeCodecs: map[string]bool{"hevc": true}}
	asset := storedAsset{
		Kind: processingTranscode, URL: "https://media.example/movie.mkv", HLSSegmentContainer: "mp4", TargetVideoCodec: "hevc", VideoBitDepth: 10,
		Decision: &PlaybackDecision{Source: &PlaybackDecisionSource{VideoCodec: "hevc", Height: 2160}, Target: &PlaybackDecisionTarget{VideoCodec: "hevc", Height: 2160, VideoBitDepth: 10}},
	}
	arguments, err := (&FFmpegProcessor{threads: 4}).processingArgumentsWithEncoder(asset, encoder)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	if !strings.Contains(joined, "-c:v libx265 -profile:v main10") || !strings.Contains(joined, "-pix_fmt yuv420p10le") || strings.Contains(joined, "-hwaccel") || strings.Contains(joined, "-vf format=yuv420p") {
		t.Fatalf("Main10 software fallback does not preserve 10-bit output: %v", arguments)
	}
	processor := &FFmpegProcessor{
		encoder:         videoEncoder{kind: videoEncoderNVENC, hevcMain10: true, encodeCodecs: map[string]bool{"hevc": true}, decodeCodecs: map[string]bool{"hevc": true}},
		softwareEncoder: encoder,
	}
	if !processor.TranscodeCapabilities().HEVCMain10 {
		t.Fatal("Main10 was hidden despite functional hardware and software fallback probes")
	}
	processor.softwareEncoder.hevcMain10 = false
	if processor.TranscodeCapabilities().HEVCMain10 {
		t.Fatal("Main10 was advertised without a functional software fallback")
	}
}

func TestAV1HardwareDecodeRequiresProbedCapability(t *testing.T) {
	asset := storedAsset{Kind: processingTranscode, Decision: &PlaybackDecision{Source: &PlaybackDecisionSource{VideoCodec: "av1", Height: 1080}}}
	legacy := videoEncoder{kind: videoEncoderNVENC}
	probed := videoEncoder{kind: videoEncoderNVENC, decodeCodecs: map[string]bool{"av1": true}}
	if legacy.hardwareDecodeSafe(asset) || legacy.supportsDecode("h264") || legacy.supportsDecode("h265") || !probed.hardwareDecodeSafe(asset) {
		t.Fatalf("hardware decode safety legacy=%t inventory=%v probed=%t", legacy.hardwareDecodeSafe(asset), legacy.supportedDecodeCodecs(), probed.hardwareDecodeSafe(asset))
	}
}

func TestExplicitAMFFailureComesFromPlatformProbe(t *testing.T) {
	probes := 0
	_, err := detectExplicitVideoEncoder("amf", "", func(candidate videoEncoder, _ string, _ bool) error {
		probes++
		return videoEncoderPlatformProbeError(candidate.kind, false)
	}, func(candidate videoEncoder, _ string) error {
		return videoEncoderPlatformProbeError(candidate.kind, false)
	}, func(candidate videoEncoder) videoEncoder { return candidate })
	if err == nil || probes != len(transcodeVideoCodecs) || !strings.Contains(err.Error(), "AMF is only available on Windows") {
		t.Fatalf("explicit AMF probe result: probes=%d error=%v", probes, err)
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
	wantVulkanFilter := "format=p010,setparams=range=tv:color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc,hwupload,hwmap=derive_device=vulkan:mode=read+direct,format=vulkan,libplacebo=upscaler=none:downscaler=none:format=nv12:tonemapping=bt.2390:peak_detect=false:color_primaries=bt709:color_trc=bt709:colorspace=bt709:range=tv,hwmap=derive_device=vaapi:mode=read+direct,format=vaapi"
	if vulkanFilter != wantVulkanFilter {
		t.Fatalf("Vulkan tone-map capability filter = %q, want %q", vulkanFilter, wantVulkanFilter)
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

func TestHybridVideoEncoderRequiresVAAPIProbeWithoutHardwareToneMapDetection(t *testing.T) {
	var probed videoEncoder
	var probedToneMap bool
	detectedHardwareToneMap := false
	encoder, err := detectExplicitVideoEncoder("hybrid", "/dev/dri/renderD128", func(candidate videoEncoder, _ string, toneMap bool) error {
		if toneMap {
			probed = candidate
			probedToneMap = true
		}
		return nil
	}, func(videoEncoder, string) error {
		return nil
	}, func(candidate videoEncoder) videoEncoder {
		detectedHardwareToneMap = true
		return candidate
	})
	if err != nil {
		t.Fatal(err)
	}
	if probed.kind != videoEncoderVAAPI || probed.device != "/dev/dri/renderD128" || probed.toneMapBackend != videoToneMapHybrid || !probedToneMap {
		t.Fatalf("hybrid probe = %+v toneMap=%t", probed, probedToneMap)
	}
	if encoder.kind != videoEncoderVAAPI || encoder.toneMapBackend != videoToneMapHybrid || encoder.usesHardwareToneMap() || encoder.normalizedToneMapBackend() != videoToneMapHybrid || detectedHardwareToneMap {
		t.Fatalf("hybrid encoder = %+v hardwareToneMap=%t detected=%t", encoder, encoder.usesHardwareToneMap(), detectedHardwareToneMap)
	}

	_, err = detectExplicitVideoEncoder("hybrid", "/dev/dri/renderD128", func(videoEncoder, string, bool) error {
		return errors.New("VAAPI unavailable")
	}, func(videoEncoder, string) error {
		return nil
	}, func(candidate videoEncoder) videoEncoder {
		detectedHardwareToneMap = true
		return candidate
	})
	if err == nil || !strings.Contains(err.Error(), "initialize hybrid video encoder") || detectedHardwareToneMap {
		t.Fatalf("missing VAAPI hybrid probe error = %v hardware detection=%t", err, detectedHardwareToneMap)
	}
}

func TestHybridInitializationKeepsRequiredH264WhenOptionalCodecProbesFail(t *testing.T) {
	encoder, err := detectExplicitVideoEncoder("hybrid", "/dev/dri/renderD128", func(_ videoEncoder, codec string, toneMap bool) error {
		if codec == "h264" && toneMap {
			return nil
		}
		return errors.New(codec + " unavailable")
	}, func(videoEncoder, string) error {
		return nil
	}, func(candidate videoEncoder) videoEncoder {
		t.Fatal("hybrid mode must not run hardware tone-map backend detection")
		return candidate
	})
	if err != nil {
		t.Fatalf("optional HEVC/AV1 encoder probes made a proven H264 hybrid path fatal: %v", err)
	}
	if !encoder.supportsEncode("h264") || encoder.supportsEncode("hevc") || encoder.supportsEncode("av1") {
		t.Fatalf("hybrid encoder inventory = %v, want only proven H264", encoder.supportedEncodeCodecs())
	}
}

func TestHybridVideoEncoderProbeExercisesMain10DecodeAndReadback(t *testing.T) {
	encoder := videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128", toneMapBackend: videoToneMapHybrid, decodeCodecs: map[string]bool{"hevc": true}}
	arguments, input, err := videoEncoderProbeArguments(encoder, "h264", "balanced", true)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	for _, expected := range []string{
		"-init_hw_device vaapi=hw:/dev/dri/renderD128",
		"-hwaccel vaapi -hwaccel_device hw -hwaccel_output_format vaapi",
		"-f hevc -i pipe:0",
		"-vf hwdownload,format=p010le," + softwareToneMapFilter + ",format=nv12,hwupload",
		"-c:v h264_vaapi",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("hybrid probe arguments missing %q: %v", expected, arguments)
		}
	}
	if strings.Contains(joined, "-f lavfi") {
		t.Fatalf("hybrid probe used a software source instead of HEVC Main10: %v", arguments)
	}
	if len(input) < 11 || input[0] != 0 || input[1] != 0 || input[2] != 0 || input[3] != 1 {
		t.Fatalf("hybrid probe input is not an Annex B HEVC stream: %x", input)
	}
	if profileIDC := input[10] & 0x1f; profileIDC != 2 {
		t.Fatalf("hybrid probe HEVC profile_idc = %d, want Main 10 (2)", profileIDC)
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
		{name: "explicit hybrid tone map preserves requested 4K height", encoder: videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128", toneMapBackend: videoToneMapHybrid}, mutate: func(asset *storedAsset) {
			asset.Decision.Source.VideoCodec = "h265"
			asset.ToneMap = true
			asset.VideoBitDepth = 10
			asset.TargetHeight = 2160
		}, expected: []string{"-hwaccel vaapi -hwaccel_device hw -hwaccel_output_format vaapi", "-vf hwdownload,format=p010le," + softwareToneMapFilter + ",format=nv12,hwupload", "-c:v h264_vaapi"}, unexpected: []string{"scale=-2:1080", "scale_vaapi", "tonemap_vaapi", "libplacebo"}},
		{name: "subtitle burn stays CPU then uploads", encoder: videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128"}, mutate: func(asset *storedAsset) {
			index := 3
			asset.SubtitleTrackIndex = &index
			asset.SubtitleTrackType = subtitleBurnBitmap
			asset.SubtitleTrackOrdinal = 0
		}, expected: []string{"overlay=eof_action=pass:repeatlast=0,format=nv12,hwupload"}, unexpected: []string{"-hwaccel vaapi"}},
		{name: "probed VAAPI tone map stays on surfaces", encoder: videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128", toneMapBackend: videoToneMapVAAPI}, mutate: func(asset *storedAsset) { asset.ToneMap = true; asset.TargetHeight = 1080 }, expected: []string{"-hwaccel vaapi", "-vf tonemap_vaapi=format=nv12:matrix=bt709:primaries=bt709:transfer=bt709,scale_vaapi=w=-2:h=1080:format=nv12"}, unexpected: []string{"hwupload", "zscale"}},
		{name: "probed Vulkan tone map crosses shared DRM surfaces", encoder: videoEncoder{kind: videoEncoderVAAPI, device: "/dev/dri/renderD128", toneMapBackend: videoToneMapVulkan}, mutate: func(asset *storedAsset) { asset.ToneMap = true; asset.TargetHeight = 1080 }, expected: []string{"-init_hw_device drm=dr:/dev/dri/renderD128", "-init_hw_device vaapi=hw@dr", "-init_hw_device vulkan=vk@dr", "-hwaccel vaapi", "-vf hwmap=derive_device=vulkan:mode=read+direct,format=vulkan,libplacebo=upscaler=none:downscaler=none:format=nv12:tonemapping=bt.2390:peak_detect=false:color_primaries=bt709:color_trc=bt709:colorspace=bt709:range=tv:w=-2:h=1080,hwmap=derive_device=vaapi:mode=read+direct,format=vaapi"}, unexpected: []string{"hwdownload", "zscale", "bgra", "scale_vaapi"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.encoder.decodeCodecs == nil {
				test.encoder.decodeCodecs = map[string]bool{"h264": true, "hevc": true}
			}
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
