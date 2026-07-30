package playback

import (
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
			excluded: []string{"libx264"},
		},
		{
			name:     "Intel Quick Sync",
			encoder:  videoEncoder{kind: videoEncoderQSV, device: "/dev/dri/renderD128"},
			expected: []string{"-init_hw_device qsv=hw@va", "hwupload=extra_hw_frames=64", "-c:v h264_qsv"},
			excluded: []string{"libx264"},
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
			arguments, err := processor.processingArguments(storedAsset{Kind: processingTranscode, URL: "https://media.example/movie.mkv"})
			if err != nil {
				t.Fatalf("build processing arguments: %v", err)
			}
			joined := strings.Join(arguments, " ")
			for _, expected := range test.expected {
				if !strings.Contains(joined, expected) {
					t.Fatalf("missing %q in arguments: %v", expected, arguments)
				}
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

	hardwareFilter := videoEncoder{kind: videoEncoderVAAPI, hardwareToneMap: true}.filter(true)
	if !strings.Contains(hardwareFilter, "tonemap_vaapi") || strings.Contains(hardwareFilter, "zscale") {
		t.Fatalf("VAAPI tone-map capability should use the hardware filter: %q", hardwareFilter)
	}
}
