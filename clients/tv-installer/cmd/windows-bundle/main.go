package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) < 5 {
		fmt.Fprintln(os.Stderr, "usage: windows-bundle <launcher.exe> <output.exe> <payload.exe> <payload.exe> [payload.exe ...]")
		os.Exit(2)
	}
	if err := bundle(os.Args[1], os.Args[2], os.Args[3:]...); err != nil {
		fmt.Fprintln(os.Stderr, "windows-bundle:", err)
		os.Exit(1)
	}
}

func bundle(launcherPath, outputPath string, payloadPaths ...string) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	temporaryPath := outputPath + ".tmp"
	output, err := os.OpenFile(temporaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		_ = output.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()

	launcher, err := os.Open(launcherPath)
	if err != nil {
		return err
	}
	launcherSize, err := io.Copy(output, launcher)
	closeErr := launcher.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if launcherSize <= 0 {
		return fmt.Errorf("launcher is empty")
	}

	archive := zip.NewWriter(output)
	archive.SetOffset(launcherSize)
	names := make(map[string]struct{}, len(payloadPaths))
	for _, path := range payloadPaths {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 {
			return fmt.Errorf("payload is empty: %s", path)
		}
		name := filepath.Base(path)
		if _, duplicate := names[name]; duplicate {
			return fmt.Errorf("payload name is duplicated: %s", name)
		}
		names[name] = struct{}{}
		header := &zip.FileHeader{
			Name:   name,
			Method: zip.Deflate,
		}
		header.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		header.SetMode(0o755)
		entry, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		payload, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(entry, payload)
		closeErr := payload.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		return err
	}
	committed = true
	return nil
}
