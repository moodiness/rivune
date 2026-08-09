package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIValidateAndRejectsTrailingArguments(t *testing.T) {
	t.Parallel()
	manifestPath := filepath.Join(t.TempDir(), "requests.json")
	if err := os.WriteFile(manifestPath, []byte(minimalManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), []string{"validate", "-manifest", manifestPath}, &stdout, &stderr, os.LookupEnv)
	if code != 0 || stdout.String() != "manifest valid\n" || stderr.Len() != 0 {
		t.Fatalf("validate exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = runCLI(context.Background(), []string{"validate", "-manifest", manifestPath, "extra"}, &stdout, &stderr, os.LookupEnv)
	if code == 0 || !strings.Contains(stderr.String(), "validate requires exactly -manifest PATH") {
		t.Fatalf("trailing argument exit=%d stderr=%q", code, stderr.String())
	}
}

func TestCLIRunRequiresExactlyTwoTargets(t *testing.T) {
	t.Parallel()
	manifestPath := filepath.Join(t.TempDir(), "requests.json")
	if err := os.WriteFile(manifestPath, []byte(minimalManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code := runCLI(
		context.Background(),
		[]string{"run", "-manifest", manifestPath, "-target", "only=http://127.0.0.1", "-out", t.TempDir()},
		&bytes.Buffer{},
		&stderr,
		os.LookupEnv,
	)
	if code == 0 || !strings.Contains(stderr.String(), "exactly two -target values") {
		t.Fatalf("run exit=%d stderr=%q", code, stderr.String())
	}
}
