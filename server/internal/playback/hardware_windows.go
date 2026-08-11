//go:build windows

package playback

import (
	"os/exec"
	"syscall"
)

const windowsMediaPlatform = true

func automaticVideoEncoders(_ string) []videoEncoder {
	kinds := automaticWindowsVideoEncoderKinds()
	encoders := make([]videoEncoder, 0, len(kinds))
	for _, kind := range kinds {
		encoders = append(encoders, videoEncoder{kind: kind})
	}
	return encoders
}

func videoDeviceVendor(_ string) string { return "" }

func platformVideoEncoderProbeError(kind videoEncoderKind) error {
	return videoEncoderPlatformProbeError(kind, true)
}

func configureMediaCommand(command *exec.Cmd) {
	if command == nil {
		return
	}
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
