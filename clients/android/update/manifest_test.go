package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func fixture(t *testing.T) (generateOptions, map[string]any) {
	t.Helper()
	directory := t.TempDir()
	apk := filepath.Join(directory, "rivune-android-1.2.3.apk")
	if err := os.WriteFile(apk, []byte("signed apk fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := generateOptions{
		apk:                      apk,
		output:                   filepath.Join(directory, "rivune-android-update.json"),
		channel:                  "stable",
		tagName:                  "v1.2.3",
		publishedAt:              "2026-08-14T12:34:56Z",
		releaseURL:               "https://github.com/moodiness/rivune/releases/tag/v1.2.3",
		apkURL:                   "https://github.com/moodiness/rivune/releases/download/v1.2.3/rivune-android-1.2.3.apk",
		applicationID:            applicationID,
		buildVersion:             "123",
		signingCertificateSHA256: repeatHex("ab", 32),
	}
	manifest, err := buildManifest(options)
	if err != nil {
		t.Fatal(err)
	}
	return options, manifest
}

func TestGeneratesExactAndroidContract(t *testing.T) {
	options, manifest := fixture(t)
	expectedRootFields := []string{"schemaVersion", "channel", "version", "tagName", "publishedAt", "releaseUrl", "package"}
	for _, field := range expectedRootFields {
		if _, ok := manifest[field]; !ok {
			t.Fatalf("missing root field %q", field)
		}
	}
	if len(manifest) != len(expectedRootFields) {
		t.Fatalf("unexpected root fields: %#v", manifest)
	}

	contents := []byte("signed apk fixture")
	digest := sha256.Sum256(contents)
	expectedPackage := map[string]any{
		"format":                   "apk",
		"architectures":            []string{"universal"},
		"applicationId":            applicationID,
		"buildVersion":             "123",
		"minimumOsVersion":         "8.0",
		"fileName":                 filepath.Base(options.apk),
		"url":                      options.apkURL,
		"size":                     int64(len(contents)),
		"sha256":                   hex.EncodeToString(digest[:]),
		"signingCertificateSha256": repeatHex("ab", 32),
	}
	if !reflect.DeepEqual(manifest["package"], expectedPackage) {
		t.Fatalf("package mismatch\ngot:  %#v\nwant: %#v", manifest["package"], expectedPackage)
	}
}

func TestAcceptsAdditionalFields(t *testing.T) {
	_, manifest := fixture(t)
	manifest["futureRootField"] = true
	manifest["package"].(map[string]any)["futurePackageField"] = map[string]any{"value": 1}
	if err := validateManifest(manifest); err != nil {
		t.Fatal(err)
	}
}

func TestPrereleaseChannelMatchesSemver(t *testing.T) {
	options, _ := fixture(t)
	options.tagName = "v2.0.0-rc.1"
	options.channel = "prerelease"
	manifest, err := buildManifest(options)
	if err != nil {
		t.Fatal(err)
	}
	if manifest["version"] != "2.0.0-rc.1" {
		t.Fatalf("version = %v", manifest["version"])
	}
}

func TestRejectsUnknownSchemaVersion(t *testing.T) {
	_, manifest := fixture(t)
	manifest["schemaVersion"] = 2
	assertInvalid(t, manifest)
}

func TestRejectsEachMissingRequiredRootField(t *testing.T) {
	_, manifest := fixture(t)
	for field := range manifest {
		t.Run(field, func(t *testing.T) {
			invalid := cloneManifest(manifest)
			delete(invalid, field)
			assertInvalid(t, invalid)
		})
	}
}

func TestRejectsEachMissingRequiredPackageField(t *testing.T) {
	_, manifest := fixture(t)
	for field := range manifest["package"].(map[string]any) {
		t.Run(field, func(t *testing.T) {
			invalid := cloneManifest(manifest)
			delete(invalid["package"].(map[string]any), field)
			assertInvalid(t, invalid)
		})
	}
}

func TestRejectsChannelThatDisagreesWithVersion(t *testing.T) {
	_, manifest := fixture(t)
	manifest["channel"] = "prerelease"
	assertInvalid(t, manifest)
}

func TestRejectsNoncanonicalDigestAndNonHTTPSURL(t *testing.T) {
	_, manifest := fixture(t)
	packageObject := manifest["package"].(map[string]any)
	packageObject["sha256"] = repeatHex("AB", 32)
	packageObject["url"] = "http://example.test/rivune.apk"
	assertInvalid(t, manifest)
}

func TestRejectsInvalidSizeAndBuildVersion(t *testing.T) {
	_, manifest := fixture(t)
	packageObject := manifest["package"].(map[string]any)
	packageObject["size"] = 0
	packageObject["buildVersion"] = "01"
	assertInvalid(t, manifest)
}

func TestRejectsTagVersionMismatch(t *testing.T) {
	_, manifest := fixture(t)
	manifest["tagName"] = "v1.2.4"
	assertInvalid(t, manifest)
}

func TestRejectsWrongAndroidApplicationID(t *testing.T) {
	_, manifest := fixture(t)
	manifest["package"].(map[string]any)["applicationId"] = "io.example.other"
	assertInvalid(t, manifest)
}

func assertInvalid(t *testing.T, manifest map[string]any) {
	t.Helper()
	if err := validateManifest(manifest); err == nil {
		t.Fatal("invalid manifest was accepted")
	}
}

func cloneManifest(manifest map[string]any) map[string]any {
	clone := make(map[string]any, len(manifest))
	for key, value := range manifest {
		clone[key] = value
	}
	packageClone := make(map[string]any, len(manifest["package"].(map[string]any)))
	for key, value := range manifest["package"].(map[string]any) {
		packageClone[key] = value
	}
	clone["package"] = packageClone
	return clone
}

func repeatHex(pair string, count int) string {
	result := ""
	for range count {
		result += pair
	}
	return result
}
