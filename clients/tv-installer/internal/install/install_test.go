package install

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/moodiness/rivune/clients/tv-installer/internal/release"
)

func TestReadWebOSDevicesReturnsEmptyArrayWhenConfigurationIsMissing(t *testing.T) {
	devices := readWebOSDevices(t.TempDir())
	if devices == nil || len(devices) != 0 {
		t.Fatalf("devices = %#v, want non-nil empty array", devices)
	}
}

func TestWebOSInstallConfiguresKeyAndInstallsVerifiedPackage(t *testing.T) {
	runner := &fakeRunner{}
	service := testService(t, runner)
	err := service.Execute(context.Background(), Request{Platform: "webos", Action: "install", IP: "192.168.1.42", DeviceName: "living-room", Passphrase: "secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.commands, "\n")
	for _, expected := range []string{"ares-setup-device --add living-room", "ares-novacom --device living-room --getkey --passphrase secret", "ares-install --device living-room"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing command %q in %s", expected, joined)
		}
	}
	if runner.secretIndex != 4 {
		t.Fatalf("passphrase secret index = %d", runner.secretIndex)
	}
}

func TestWebOSInstallUpdatesExistingDeviceProfile(t *testing.T) {
	runner := &fakeRunner{}
	service := testService(t, runner)
	directory := filepath.Join(service.UserHome, ".webos", "tv")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "novacom-devices.json"), []byte(`[{"name":"living-room","host":"192.168.1.10"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(context.Background(), Request{Platform: "webos", Action: "install", IP: "192.168.1.42", DeviceName: "living-room", Passphrase: "secret"}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(runner.commands, "\n"), "ares-setup-device --modify living-room") {
		t.Fatalf("existing profile was not modified: %v", runner.commands)
	}
}

func TestTizenInstallSignsWithSelectedProfileBeforeInstall(t *testing.T) {
	runner := &fakeRunner{devices: "192.168.1.42:26101 device tv"}
	service := testService(t, runner)
	err := service.Execute(context.Background(), Request{Platform: "tizen", Action: "install", IP: "192.168.1.42", Profile: "RivuneTV"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.commands, "\n")
	for _, expected := range []string{"sdb connect 192.168.1.42", "sdb devices", "tizen package -t wgt -s RivuneTV", "tizen install -n"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing command %q in %s", expected, joined)
		}
	}
}

func TestInstallerRejectsNonPrivateTarget(t *testing.T) {
	service := testService(t, &fakeRunner{})
	if err := service.Execute(context.Background(), Request{Platform: "webos", Action: "launch", IP: "203.0.113.4"}, nil); err == nil {
		t.Fatal("public address accepted")
	}
}

func TestTizenExtractionRejectsTraversalAndDuplicates(t *testing.T) {
	for _, paths := range [][]string{{"../escape", "config.xml"}, {"config.xml", "config.xml"}} {
		path := filepath.Join(t.TempDir(), "unsafe.wgt")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		archive := zip.NewWriter(file)
		for _, name := range paths {
			entry, createErr := archive.Create(name)
			if createErr != nil {
				t.Fatal(createErr)
			}
			_, _ = entry.Write([]byte("unsafe"))
		}
		_ = archive.Close()
		_ = file.Close()
		if _, _, _, err := prepareTizen(path, "2.0.0"); err == nil {
			t.Fatalf("unsafe archive paths accepted: %v", paths)
		}
	}
}

func TestExecRunnerRedactsSecretsFromCommandsAndOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is POSIX-only")
	}
	script := filepath.Join(t.TempDir(), "echo-secret")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$1\"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var logs []string
	output, err := (ExecRunner{Log: func(message string) { logs = append(logs, message) }}).Run(context.Background(), script, []string{"private-passphrase"}, map[int]struct{}{0: {}})
	if err == nil {
		t.Fatal("failing command succeeded")
	}
	if strings.Contains(output, "private-passphrase") || strings.Contains(strings.Join(logs, "\n"), "private-passphrase") {
		t.Fatal("command secret was exposed")
	}
}

type fakeSource struct{ t *testing.T }

func (source fakeSource) Latest(context.Context) (release.Release, error) {
	return release.Release{Version: "2.0.0", TagName: "v2.0.0", WebOS: release.TVPackage{Asset: release.Asset{Name: release.WebOSPackageName, Size: 1, SHA256: strings.Repeat("a", 64)}}, Tizen: release.TVPackage{Asset: release.Asset{Name: release.TizenPackageName, Size: 1, SHA256: strings.Repeat("b", 64)}}}, nil
}
func (source fakeSource) Download(_ context.Context, pkg release.TVPackage, destination string) error {
	if pkg.Name == release.TizenPackageName {
		return writeWGT(destination)
	}
	return os.WriteFile(destination, []byte("ipk"), 0o600)
}

type fakeRunner struct {
	commands    []string
	devices     string
	secretIndex int
}

func (runner *fakeRunner) Run(_ context.Context, command string, args []string, secrets map[int]struct{}) (string, error) {
	runner.commands = append(runner.commands, filepath.Base(command)+" "+strings.Join(args, " "))
	for index := range secrets {
		runner.secretIndex = index
	}
	if filepath.Base(command) == "sdb" && len(args) == 1 && args[0] == "devices" {
		return runner.devices, nil
	}
	if filepath.Base(command) == "tizen" && len(args) > 0 && args[0] == "package" {
		for index, arg := range args {
			if arg == "-o" && index+1 < len(args) {
				_ = os.MkdirAll(args[index+1], 0o700)
				_ = os.WriteFile(filepath.Join(args[index+1], "signed.wgt"), []byte("signed"), 0o600)
			}
		}
	}
	return "", nil
}

func testService(t *testing.T, runner Runner) *Service {
	t.Helper()
	tools := t.TempDir()
	for _, name := range []string{"ares-setup-device", "ares-novacom", "ares-install", "ares-launch", "sdb", "tizen"} {
		if runtime.GOOS == "windows" {
			name += ".bat"
		}
		if err := os.WriteFile(filepath.Join(tools, name), []byte("tool"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return &Service{Source: fakeSource{t}, Runner: runner, Home: tools, UserHome: t.TempDir(), Temp: t.TempDir()}
}

func writeWGT(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(file)
	entry, err := archive.Create("config.xml")
	if err == nil {
		_, err = entry.Write([]byte(`<?xml version="1.0"?><widget xmlns:tizen="http://tizen.org/ns/widgets" version="2.0.0"><tizen:application id="RivuneTV01.Rivune" package="RivuneTV01"/></widget>`))
	}
	if closeErr := archive.Close(); err == nil {
		err = closeErr
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}
