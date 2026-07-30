package playback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const hardwareProbeTimeout = 10 * time.Second

const softwareToneMapFilter = "zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p"

type FFmpegOptions struct {
	HardwareAcceleration string
	VideoDevice          string
}

type videoEncoderKind string

const (
	videoEncoderSoftware videoEncoderKind = "software"
	videoEncoderVAAPI    videoEncoderKind = "vaapi"
	videoEncoderQSV      videoEncoderKind = "qsv"
	videoEncoderNVENC    videoEncoderKind = "nvenc"
)

type videoEncoder struct {
	kind            videoEncoderKind
	device          string
	hardwareToneMap bool
}

func detectVideoEncoder(ffmpegPath string, options FFmpegOptions) (videoEncoder, error) {
	mode := videoEncoderKind(strings.ToLower(strings.TrimSpace(options.HardwareAcceleration)))
	if mode == "" || mode == "auto" {
		for _, candidate := range automaticVideoEncoders(options.VideoDevice) {
			if err := probeVideoEncoder(ffmpegPath, candidate, false); err == nil {
				return detectHardwareToneMap(ffmpegPath, candidate), nil
			}
		}
		return videoEncoder{kind: videoEncoderSoftware}, nil
	}
	if mode == videoEncoderSoftware {
		return videoEncoder{kind: videoEncoderSoftware}, nil
	}
	candidate := videoEncoder{kind: mode, device: options.VideoDevice}
	if mode == videoEncoderNVENC {
		candidate.device = ""
	}
	if err := probeVideoEncoder(ffmpegPath, candidate, false); err != nil {
		return videoEncoder{}, fmt.Errorf("initialize %s video encoder: %w", mode, err)
	}
	return detectHardwareToneMap(ffmpegPath, candidate), nil
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
	probeContext, cancel := context.WithTimeout(context.Background(), hardwareProbeTimeout)
	defer cancel()
	arguments := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	arguments = append(arguments, encoder.globalArguments()...)
	arguments = append(arguments, "-f", "lavfi", "-i", "color=c=black:s=64x64:r=1")
	if filter := encoder.filter(toneMap); filter != "" {
		arguments = append(arguments, "-vf", filter)
	}
	arguments = append(arguments, encoder.codecArguments(1)...)
	arguments = append(arguments, "-frames:v", "1", "-an", "-f", "null", "-")
	command := exec.CommandContext(probeContext, ffmpegPath, arguments...)
	var diagnostic bytes.Buffer
	command.Stdout = nil
	command.Stderr = &diagnostic
	if err := command.Run(); err != nil {
		if errors.Is(probeContext.Err(), context.DeadlineExceeded) {
			return errors.New("hardware encoder probe timed out")
		}
		return fmt.Errorf("hardware encoder probe: %w: %s", err, boundedDiagnostic(diagnostic.String()))
	}
	return nil
}

func detectHardwareToneMap(ffmpegPath string, encoder videoEncoder) videoEncoder {
	if encoder.normalizedKind() != videoEncoderVAAPI {
		return encoder
	}
	candidate := encoder
	candidate.hardwareToneMap = true
	if err := probeVideoEncoder(ffmpegPath, candidate, true); err == nil {
		return candidate
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
		return []string{"-init_hw_device", "vaapi=hw:" + encoder.device, "-filter_hw_device", "hw"}
	case videoEncoderQSV:
		return []string{"-init_hw_device", "vaapi=va:" + encoder.device, "-init_hw_device", "qsv=hw@va", "-filter_hw_device", "hw"}
	default:
		return nil
	}
}

func (encoder videoEncoder) filter(toneMap bool) string {
	if toneMap && encoder.normalizedKind() == videoEncoderVAAPI && encoder.hardwareToneMap {
		return "format=p010,hwupload,tonemap_vaapi=format=nv12:matrix=bt709:primaries=bt709:transfer=bt709"
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
		return []string{"-c:v", "h264_vaapi", "-profile:v", "high", "-rc_mode", "CQP", "-qp", "18"}
	case videoEncoderQSV:
		return []string{"-c:v", "h264_qsv", "-profile:v", "high", "-preset", "veryfast", "-global_quality", "18", "-look_ahead", "0"}
	case videoEncoderNVENC:
		return []string{"-c:v", "h264_nvenc", "-profile:v", "high", "-preset", "p4", "-tune", "ll", "-rc", "vbr", "-cq", "18", "-b:v", "0", "-spatial_aq", "1", "-zerolatency", "1"}
	default:
		return []string{"-threads", fmt.Sprintf("%d", threads), "-c:v", "libx264", "-preset", "superfast", "-tune", "zerolatency", "-crf", "18", "-pix_fmt", "yuv420p"}
	}
}
