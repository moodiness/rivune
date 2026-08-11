//go:build windows

package playback

import (
	"os/exec"
	"testing"
)

func TestConfigureMediaCommandHidesWindows(t *testing.T) {
	command := exec.Command("cmd", "/c", "exit", "0")
	configureMediaCommand(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow {
		t.Fatalf("media command is not configured for headless Windows execution: %+v", command.SysProcAttr)
	}
}
