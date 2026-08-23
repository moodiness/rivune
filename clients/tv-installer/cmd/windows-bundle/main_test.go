package main

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBundlePreservesLauncherAndEmbedsEveryPayload(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	launcher := writeFixture(t, directory, "launcher.exe", []byte("launcher-prefix"))
	x64 := writeFixture(t, directory, "Rivune-x64.exe", []byte("x64-payload"))
	arm64 := writeFixture(t, directory, "Rivune-arm64.exe", []byte("arm64-payload"))
	uninstaller := writeFixture(t, directory, "Rivune-Uninstall.exe", []byte("uninstaller-payload"))
	output := filepath.Join(directory, "output", "Rivune-Windows.exe")

	if err := bundle(launcher, output, x64, arm64, uninstaller); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents[:len("launcher-prefix")]) != "launcher-prefix" {
		t.Fatal("bundle does not preserve the launcher prefix")
	}
	archive, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	actual := make(map[string]string, len(archive.File))
	for _, entry := range archive.File {
		reader, openErr := entry.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		buffer, readErr := io.ReadAll(reader)
		if readErr != nil {
			reader.Close()
			t.Fatal(readErr)
		}
		if closeErr := reader.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		actual[entry.Name] = string(buffer)
	}
	expected := map[string]string{
		"Rivune-x64.exe":       "x64-payload",
		"Rivune-arm64.exe":     "arm64-payload",
		"Rivune-Uninstall.exe": "uninstaller-payload",
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("wrong embedded payloads: %#v", actual)
	}
}

func TestBundleRejectsDuplicatePayloadNames(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	launcher := writeFixture(t, directory, "launcher.exe", []byte("launcher"))
	first := writeFixture(t, filepath.Join(directory, "first"), "payload.exe", []byte("first"))
	second := writeFixture(t, filepath.Join(directory, "second"), "payload.exe", []byte("second"))

	if err := bundle(launcher, filepath.Join(directory, "output.exe"), first, second); err == nil {
		t.Fatal("duplicate payload names were accepted")
	}
}

func writeFixture(t *testing.T, directory, name string, contents []byte) string {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, contents, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
