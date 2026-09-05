//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadP256PrivateKeyRejectsGroupOrOtherPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.pem")
	writeTestKey(t, path)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}

	_, err := readP256PrivateKey(path)
	if err == nil {
		t.Fatal("private key readable by the group was accepted")
	}
	if !strings.Contains(err.Error(), "group or other access") {
		t.Fatalf("error is not actionable: %v", err)
	}
}
