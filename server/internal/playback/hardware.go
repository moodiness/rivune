package playback

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	hardwareProbeTimeout         = 10 * time.Second
	softwareToneMapMaximumHeight = 1080
	softwareToneMapFilter        = "zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p"
	hybridProbeHEVCBase64        = "AAAAAUABDAH//wQIAAADAJ2oAAADAAAeugJAAAAAAUIBAQQIAAADAJ2oAAADAAAeoBAgIE2W6SkwvAWoSIBIIAAAAwAgAAADACEAAAABRAHAcYMSAAABKAGtyElUgMen/8nKmru0w6AS2YZeAoAH4XzEgAqmgfg="
)

type FFmpegOptions struct {
	HardwareAcceleration string
	VideoDevice          string
	MaximumReadRate      float64
}

type videoEncoderKind string

type videoToneMapBackend string

const (
	videoEncoderSoftware videoEncoderKind = "software"
	videoEncoderVAAPI    videoEncoderKind = "vaapi"
	videoEncoderQSV      videoEncoderKind = "qsv"
	videoEncoderNVENC    videoEncoderKind = "nvenc"

	videoToneMapSoftware videoToneMapBackend = "software"
	videoToneMapHybrid   videoToneMapBackend = "hybrid"
	videoToneMapVAAPI    videoToneMapBackend = "vaapi"
	videoToneMapVulkan   videoToneMapBackend = "vulkan"
)

type videoEncoder struct {
	kind           videoEncoderKind
	device         string
	toneMapBackend videoToneMapBackend
}

func detectVideoEncoder(ffmpegPath string, options FFmpegOptions) (videoEncoder, error) {
	mode := strings.ToLower(strings.TrimSpace(options.HardwareAcceleration))
	if mode == "" || mode == "auto" {
		for _, candidate := range automaticVideoEncoders(options.VideoDevice) {
			if err := probeVideoEncoder(ffmpegPath, candidate, false); err == nil {
				return detectHardwareToneMap(ffmpegPath, candidate), nil
			}
		}
		return videoEncoder{kind: videoEncoderSoftware}, nil
	}
	return detectExplicitVideoEncoder(mode, options.VideoDevice, func(candidate videoEncoder, toneMap bool) error {
		return probeVideoEncoder(ffmpegPath, candidate, toneMap)
	}, func(candidate videoEncoder) videoEncoder {
		return detectHardwareToneMap(ffmpegPath, candidate)
	})
}

func detectExplicitVideoEncoder(mode, device string, probe func(videoEncoder, bool) error, detectToneMap func(videoEncoder) videoEncoder) (videoEncoder, error) {
	if mode == string(videoEncoderSoftware) {
		return videoEncoder{kind: videoEncoderSoftware}, nil
	}
	candidate := videoEncoder{device: device}
	switch mode {
	case string(videoToneMapHybrid):
		candidate.kind = videoEncoderVAAPI
		candidate.toneMapBackend = videoToneMapHybrid
	case string(videoEncoderVAAPI):
		candidate.kind = videoEncoderVAAPI
	case string(videoEncoderQSV):
		candidate.kind = videoEncoderQSV
	case string(videoEncoderNVENC):
		candidate.kind = videoEncoderNVENC
		candidate.device = ""
	default:
		return videoEncoder{}, fmt.Errorf("unsupported hardware acceleration mode %q", mode)
	}
	if err := probe(candidate, mode == string(videoToneMapHybrid)); err != nil {
		return videoEncoder{}, fmt.Errorf("initialize %s video encoder: %w", mode, err)
	}
	if mode == string(videoToneMapHybrid) {
		return candidate, nil
	}
	return detectToneMap(candidate), nil
}

func automaticVideoEncoders(device string) []videoEncoder {
	_, nvidiaErr := os.Stat("/dev/nvidiactl")
	_, videoErr := os.Stat(device)
	kinds := automaticVideoEncoderKinds(videoDeviceVendor(device), nvidiaErr == nil, videoErr == nil)
	encoders := make([]videoEncoder, 0, len(kinds))
	for _, kind := range kinds {
		encoder := videoEncoder{kind: kind, device: device}
		if kind == videoEncoderNVENC {
			encoder.device = ""
		}
		encoders = append(encoders, encoder)
	}
	return encoders
}

func automaticVideoEncoderKinds(vendor string, nvidiaAvailable, renderDeviceAvailable bool) []videoEncoderKind {
	kinds := make([]videoEncoderKind, 0, 3)
	if nvidiaAvailable {
		kinds = append(kinds, videoEncoderNVENC)
	}
	if !renderDeviceAvailable {
		return kinds
	}
	switch strings.ToLower(strings.TrimSpace(vendor)) {
	case "0x1002":
		kinds = append(kinds, videoEncoderVAAPI)
	case "0x8086":
		kinds = append(kinds, videoEncoderQSV, videoEncoderVAAPI)
	default:
		kinds = append(kinds, videoEncoderQSV, videoEncoderVAAPI)
	}
	return kinds
}

func videoDeviceVendor(device string) string {
	name := filepath.Base(strings.TrimSpace(device))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return ""
	}
	value, err := os.ReadFile(filepath.Join("/sys/class/drm", name, "device/vendor"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(value))
}

func probeVideoEncoder(ffmpegPath string, encoder videoEncoder, toneMap bool) error {
	if encoder.normalizedKind() == videoEncoderSoftware {
		return nil
	}
	arguments, input, err := videoEncoderProbeArguments(encoder, toneMap)
	if err != nil {
		return err
	}
	probeContext, cancel := context.WithTimeout(context.Background(), hardwareProbeTimeout)
	defer cancel()
	command := exec.CommandContext(probeContext, ffmpegPath, arguments...)
	if len(input) > 0 {
		command.Stdin = bytes.NewReader(input)
	}
	diagnostic := newDiagnosticBuffer()
	command.Stdout = nil
	command.Stderr = diagnostic
	if err := command.Run(); err != nil {
		if errors.Is(probeContext.Err(), context.DeadlineExceeded) {
			return errors.New("hardware encoder probe timed out")
		}
		return fmt.Errorf("hardware encoder probe: %w: %s", err, diagnostic.String())
	}
	return nil
}

func videoEncoderProbeArguments(encoder videoEncoder, toneMap bool) ([]string, []byte, error) {
	arguments := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	arguments = append(arguments, encoder.globalArguments()...)
	var input []byte
	if toneMap && encoder.toneMapBackend == videoToneMapHybrid {
		var err error
		input, err = base64.StdEncoding.DecodeString(hybridProbeHEVCBase64)
		if err != nil {
			return nil, nil, fmt.Errorf("decode hybrid video probe: %w", err)
		}
		asset := storedAsset{
			Kind:          processingTranscode,
			ToneMap:       true,
			VideoBitDepth: 10,
			Decision: &PlaybackDecision{Source: &PlaybackDecisionSource{
				VideoCodec: "h265",
				Height:     128,
			}},
		}
		arguments = append(arguments, encoder.hardwareDecodeArguments(asset)...)
		arguments = append(arguments, "-f", "hevc", "-i", "pipe:0", "-vf", encoder.hybridToneMapFilter(asset))
	} else {
		arguments = append(arguments, "-f", "lavfi", "-i", "color=c=black:s=64x64:r=1")
		if filter := encoder.filter(toneMap); filter != "" {
			arguments = append(arguments, "-vf", filter)
		}
	}
	arguments = append(arguments, encoder.codecArguments(1)...)
	arguments = append(arguments, "-frames:v", "1", "-an", "-f", "null", "-")
	return arguments, input, nil
}

func detectHardwareToneMap(ffmpegPath string, encoder videoEncoder) videoEncoder {
	return detectHardwareToneMapWithProbe(encoder, videoDeviceVendor(encoder.device), func(candidate videoEncoder) error {
		return probeVideoEncoder(ffmpegPath, candidate, true)
	})
}

func detectHardwareToneMapWithProbe(encoder videoEncoder, vendor string, probe func(videoEncoder) error) videoEncoder {
	if encoder.normalizedKind() != videoEncoderVAAPI {
		return encoder
	}
	backends := [...]videoToneMapBackend{videoToneMapVAAPI, videoToneMapVulkan}
	if strings.EqualFold(strings.TrimSpace(vendor), "0x1002") {
		backends[0], backends[1] = backends[1], backends[0]
	}
	for _, backend := range backends {
		candidate := encoder
		candidate.toneMapBackend = backend
		if err := probe(candidate); err == nil {
			return candidate
		}
	}
	return encoder
}

func (encoder videoEncoder) normalizedKind() videoEncoderKind {
	if encoder.kind == "" {
		return videoEncoderSoftware
	}
	return encoder.kind
}

func (encoder videoEncoder) globalArguments() []string {
	switch encoder.normalizedKind() {
	case videoEncoderVAAPI:
		if encoder.toneMapBackend == videoToneMapVulkan {
			return []string{
				"-init_hw_device", "drm=dr:" + encoder.device,
				"-init_hw_device", "vaapi=hw@dr",
				"-init_hw_device", "vulkan=vk@dr",
				"-filter_hw_device", "hw",
			}
		}
		return []string{"-init_hw_device", "vaapi=hw:" + encoder.device, "-filter_hw_device", "hw"}
	case videoEncoderQSV:
		return []string{"-init_hw_device", "vaapi=va:" + encoder.device, "-init_hw_device", "qsv=hw@va", "-filter_hw_device", "hw"}
	default:
		return nil
	}
}

func (encoder videoEncoder) usesHardwareToneMap() bool {
	return encoder.toneMapBackend == videoToneMapVAAPI || encoder.toneMapBackend == videoToneMapVulkan
}

func (encoder videoEncoder) normalizedToneMapBackend() videoToneMapBackend {
	if encoder.toneMapBackend == videoToneMapHybrid || encoder.usesHardwareToneMap() {
		return encoder.toneMapBackend
	}
	return videoToneMapSoftware
}

func (encoder videoEncoder) hardwareDecodeSafe(asset storedAsset) bool {
	if asset.Kind != processingTranscode || asset.SubtitleTrackIndex != nil ||
		encoder.normalizedKind() == videoEncoderSoftware || asset.Decision == nil || asset.Decision.Source == nil {
		return false
	}
	codec := normalizedCodec(asset.Decision.Source.VideoCodec)
	switch codec {
	case "h264", "h265":
	default:
		return false
	}
	if asset.ToneMap && !encoder.usesHardwareToneMap() {
		return encoder.normalizedKind() == videoEncoderVAAPI && codec == "h265" && asset.VideoBitDepth == 10
	}
	needsScale := asset.TargetHeight > 0 && asset.Decision.Source.Height > asset.TargetHeight
	return !needsScale || encoder.normalizedKind() == videoEncoderVAAPI || encoder.normalizedKind() == videoEncoderQSV
}

// hardwareFramesSafe is deliberately conservative: these decoder, filter,
// and encoder paths share a documented hardware frame type. Subtitle
// composition and software tone mapping require CPU frames.
func (encoder videoEncoder) hardwareFramesSafe(asset storedAsset) bool {
	return encoder.hardwareDecodeSafe(asset) && (!asset.ToneMap || encoder.usesHardwareToneMap())
}

func (encoder videoEncoder) hardwareDecodeArguments(asset storedAsset) []string {
	if !encoder.hardwareDecodeSafe(asset) {
		return nil
	}
	switch encoder.normalizedKind() {
	case videoEncoderVAAPI:
		return []string{"-hwaccel", "vaapi", "-hwaccel_device", "hw", "-hwaccel_output_format", "vaapi"}
	case videoEncoderQSV:
		return []string{"-hwaccel", "qsv", "-hwaccel_device", "hw", "-hwaccel_output_format", "qsv"}
	case videoEncoderNVENC:
		return []string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"}
	default:
		return nil
	}
}

func (encoder videoEncoder) hybridToneMapFilter(asset storedAsset) string {
	if !asset.ToneMap || encoder.hardwareFramesSafe(asset) || !encoder.hardwareDecodeSafe(asset) {
		return ""
	}
	filters := []string{"hwdownload", "format=p010le"}
	if asset.TargetHeight > 0 && asset.Decision != nil && asset.Decision.Source != nil && asset.Decision.Source.Height > asset.TargetHeight {
		filters = append(filters, "scale=-2:"+strconv.Itoa(asset.TargetHeight))
	}
	filters = append(filters, softwareToneMapFilter, "format=nv12", "hwupload")
	return strings.Join(filters, ",")
}

func (encoder videoEncoder) hardwareFilter(asset storedAsset) string {
	if !encoder.hardwareFramesSafe(asset) {
		return ""
	}
	if asset.ToneMap && encoder.toneMapBackend == videoToneMapVulkan {
		height := 0
		if asset.TargetHeight > 0 && asset.Decision.Source.Height > asset.TargetHeight {
			height = asset.TargetHeight
		}
		return vulkanToneMapFilter(height)
	}
	filters := make([]string, 0, 2)
	if asset.ToneMap {
		filters = append(filters, "tonemap_vaapi=format=nv12:matrix=bt709:primaries=bt709:transfer=bt709")
	}
	if asset.TargetHeight > 0 && asset.Decision.Source.Height > asset.TargetHeight {
		height := strconv.Itoa(asset.TargetHeight)
		switch encoder.normalizedKind() {
		case videoEncoderVAAPI:
			filters = append(filters, "scale_vaapi=w=-2:h="+height+":format=nv12")
		case videoEncoderQSV:
			filters = append(filters, "scale_qsv=w=-2:h="+height+":format=nv12")
		}
	}
	return strings.Join(filters, ",")
}

func vulkanToneMapFilter(targetHeight int) string {
	libplacebo := "libplacebo=upscaler=none:downscaler=none:format=nv12:tonemapping=bt.2390:peak_detect=false:color_primaries=bt709:color_trc=bt709:colorspace=bt709:range=tv"
	if targetHeight > 0 {
		libplacebo += ":w=-2:h=" + strconv.Itoa(targetHeight)
	}
	return strings.Join([]string{
		"hwmap=derive_device=vulkan:mode=read+direct",
		"format=vulkan",
		libplacebo,
		"hwmap=derive_device=vaapi:mode=read+direct",
		"format=vaapi",
	}, ",")
}

func (encoder videoEncoder) filter(toneMap bool) string {
	if toneMap && encoder.normalizedKind() == videoEncoderVAAPI {
		switch encoder.toneMapBackend {
		case videoToneMapVAAPI:
			return "format=p010,hwupload,tonemap_vaapi=format=nv12:matrix=bt709:primaries=bt709:transfer=bt709"
		case videoToneMapVulkan:
			return "format=p010,setparams=range=tv:color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc,hwupload," + vulkanToneMapFilter(0)
		}
	}
	filter := ""
	if toneMap {
		filter = softwareToneMapFilter
	}
	switch encoder.normalizedKind() {
	case videoEncoderVAAPI:
		if filter != "" {
			filter += ","
		}
		return filter + "format=nv12,hwupload"
	case videoEncoderQSV:
		if filter != "" {
			filter += ","
		}
		return filter + "format=nv12,hwupload=extra_hw_frames=64"
	default:
		return filter
	}
}

func (encoder videoEncoder) codecArguments(threads int) []string {
	switch encoder.normalizedKind() {
	case videoEncoderVAAPI:
		return []string{"-c:v", "h264_vaapi", "-profile:v", "high"}
	case videoEncoderQSV:
		return []string{"-c:v", "h264_qsv", "-profile:v", "high", "-preset", "veryfast", "-look_ahead", "0"}
	case videoEncoderNVENC:
		return []string{"-c:v", "h264_nvenc", "-profile:v", "high", "-preset", "p4", "-tune", "ll", "-rc", "vbr", "-cq", "18", "-b:v", "0", "-spatial_aq", "1", "-zerolatency", "1"}
	default:
		return []string{"-threads", fmt.Sprintf("%d", threads), "-c:v", "libx264", "-preset", "superfast", "-tune", "zerolatency", "-crf", "18", "-pix_fmt", "yuv420p"}
	}
}
