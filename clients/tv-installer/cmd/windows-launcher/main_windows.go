package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

const (
	processorArchitectureAMD64 = uint16(9)
	processorArchitectureARM64 = uint16(12)
	maximumPayloadBytes        = uint64(2*1024*1024*1024 - 1)
)

var (
	x64Executable   string
	arm64Executable string
)

type systemInfo struct {
	processorArchitecture uint16
	reserved              uint16
	pageSize              uint32
	minimumAddress        uintptr
	maximumAddress        uintptr
	activeProcessorMask   uintptr
	numberOfProcessors    uint32
	processorType         uint32
	allocationGranularity uint32
	processorLevel        uint16
	processorRevision     uint16
}

func main() {
	if err := run(); err != nil {
		reportError(err)
		os.Exit(1)
	}
}

func run() error {
	if x64Executable == "" || arm64Executable == "" {
		return errors.New("launcher target names were not configured")
	}
	architecture, err := nativeArchitecture()
	if err != nil {
		return err
	}
	var targetName string
	switch architecture {
	case processorArchitectureAMD64:
		targetName = x64Executable
	case processorArchitectureARM64:
		targetName = arm64Executable
	default:
		return fmt.Errorf("unsupported Windows processor architecture: %d", architecture)
	}

	targetPath, cleanup, err := extractTarget(targetName)
	if err != nil {
		return err
	}
	defer cleanup()
	command := exec.Command(targetPath, os.Args[1:]...)
	command.Dir = filepath.Dir(targetPath)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			os.Exit(exitError.ExitCode())
		}
		return fmt.Errorf("run %s: %w", targetName, err)
	}
	return nil
}

func extractTarget(targetName string) (string, func(), error) {
	launcherPath, err := os.Executable()
	if err != nil {
		return "", func() {}, fmt.Errorf("locate launcher: %w", err)
	}
	archive, err := zip.OpenReader(launcherPath)
	if err != nil {
		return "", func() {}, fmt.Errorf("open embedded application payload: %w", err)
	}
	defer archive.Close()
	if len(archive.File) != 2 {
		return "", func() {}, errors.New("embedded application payload has an invalid file set")
	}
	expected := map[string]bool{x64Executable: false, arm64Executable: false}
	var selected *zip.File
	for _, entry := range archive.File {
		if _, ok := expected[entry.Name]; !ok || expected[entry.Name] || entry.FileInfo().IsDir() || entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > maximumPayloadBytes {
			return "", func() {}, errors.New("embedded application payload has an invalid file set")
		}
		expected[entry.Name] = true
		if entry.Name == targetName {
			selected = entry
		}
	}
	if selected == nil {
		return "", func() {}, errors.New("embedded application payload has no compatible executable")
	}

	directory, cleanup, err := extractionDirectory()
	if err != nil {
		return "", func() {}, err
	}
	targetPath := filepath.Join(directory, targetName)

	input, err := selected.Open()
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	temporary, err := os.CreateTemp(directory, ".rivune-extract-*.exe")
	if err != nil {
		input.Close()
		cleanup()
		return "", func() {}, err
	}
	temporaryPath := temporary.Name()
	written, copyErr := io.Copy(temporary, io.LimitReader(input, int64(selected.UncompressedSize64)+1))
	inputCloseErr := input.Close()
	fileCloseErr := temporary.Close()
	if copyErr != nil || inputCloseErr != nil || fileCloseErr != nil || written != int64(selected.UncompressedSize64) {
		_ = os.Remove(temporaryPath)
		cleanup()
		return "", func() {}, errors.New("embedded application payload could not be verified")
	}
	if err := os.Rename(temporaryPath, targetPath); err != nil {
		_ = os.Remove(temporaryPath)
		cleanup()
		return "", func() {}, err
	}
	return targetPath, cleanup, nil
}

func extractionDirectory() (string, func(), error) {
	directory, err := os.MkdirTemp("", "rivune-tv-installer-")
	if err != nil {
		return "", func() {}, err
	}
	return directory, func() { _ = os.RemoveAll(directory) }, nil
}

func nativeArchitecture() (uint16, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	procedure := kernel32.NewProc("GetNativeSystemInfo")
	if err := kernel32.Load(); err != nil {
		return 0, fmt.Errorf("load kernel32: %w", err)
	}
	var info systemInfo
	procedure.Call(uintptr(unsafe.Pointer(&info)))
	return info.processorArchitecture, nil
}

func reportError(err error) {
	fmt.Fprintln(os.Stderr, "Rivune launcher:", err)
}
