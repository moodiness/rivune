//go:build !windows

package playback

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const windowsMediaPlatform = false

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

func platformVideoEncoderProbeError(kind videoEncoderKind) error {
	return videoEncoderPlatformProbeError(kind, false)
}

func configureMediaCommand(_ *exec.Cmd) {}
