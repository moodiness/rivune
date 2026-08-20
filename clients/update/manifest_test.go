package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const x64FixtureContents = "Windows x64 executable fixture"

func fixture(t *testing.T) (generateOptions, map[string]any) {
	t.Helper()
	directory := t.TempDir()
	apk := filepath.Join(directory, "Rivune-Android.apk")
	x64Executable := filepath.Join(directory, "Rivune-x64.exe")
	arm64Executable := filepath.Join(directory, "Rivune-arm64.exe")
	if err := os.WriteFile(apk, []byte("signed apk fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	const x64Contents = x64FixtureContents
	if err := os.WriteFile(x64Executable, []byte(x64Contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(arm64Executable, []byte("Windows ARM64 executable fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := generateOptions{
		apk:                       apk,
		windowsX64Executable:      x64Executable,
		windowsArm64Executable:    arm64Executable,
		output:                    filepath.Join(directory, "rivune-update.json"),
		channel:                   "stable",
		tagName:                   "v1.2.3",
		publishedAt:               "2026-08-14T12:34:56Z",
		releaseURL:                "https://github.com/moodiness/rivune/releases/tag/v1.2.3",
		apkURL:                    "https://github.com/moodiness/rivune/releases/download/v1.2.3/Rivune-Android.apk",
		applicationID:             androidApplicationID,
		buildVersion:              "123",
		signingCertificateSHA256:  repeatHex("ab", 32),
		windowsX64ExecutableURL:   "https://github.com/moodiness/rivune/releases/download/v1.2.3/Rivune-x64.exe",
		windowsArm64ExecutableURL: "https://github.com/moodiness/rivune/releases/download/v1.2.3/Rivune-arm64.exe",
	}
	manifest, err := buildManifest(options)
	if err != nil {
		t.Fatal(err)
	}
	return options, manifest
}

func TestGeneratesExactThreePackageContract(t *testing.T) {
	options, manifest := fixture(t)
	expectedRootFields := []string{"schemaVersion", "channel", "version", "tagName", "publishedAt", "releaseUrl", "packages"}
	for _, field := range expectedRootFields {
		if _, ok := manifest[field]; !ok {
			t.Fatalf("missing root field %q", field)
		}
	}
	if len(manifest) != len(expectedRootFields) {
		t.Fatalf("unexpected root fields: %#v", manifest)
	}
	if manifest["schemaVersion"] != 2 || manifest["version"] != "1.2.3" {
		t.Fatalf("wrong root contract: %#v", manifest)
	}

	apkDigest := sha256.Sum256([]byte("signed apk fixture"))
	x64ExecutableDigest := sha256.Sum256([]byte(x64FixtureContents))
	arm64ExecutableDigest := sha256.Sum256([]byte("Windows ARM64 executable fixture"))
	packages := manifest["packages"].(map[string]any)
	androidExpected := map[string]any{
		"format": "apk", "architectures": []string{"universal"}, "minimumOsVersion": "8.0",
		"applicationId": androidApplicationID, "buildVersion": "123",
		"signingCertificateSha256": repeatHex("ab", 32), "fileName": filepath.Base(options.apk),
		"url": options.apkURL, "size": int64(len("signed apk fixture")), "sha256": hex.EncodeToString(apkDigest[:]),
	}
	windowsX64Expected := map[string]any{
		"format": "exe", "architectures": []string{"x64"}, "minimumOsVersion": "10.0.19041.0",
		"fileName": "Rivune-x64.exe", "url": options.windowsX64ExecutableURL,
		"size": int64(len(x64FixtureContents)), "sha256": hex.EncodeToString(x64ExecutableDigest[:]),
	}
	windowsArm64Expected := map[string]any{
		"format": "exe", "architectures": []string{"arm64"}, "minimumOsVersion": "10.0.19041.0",
		"fileName": "Rivune-arm64.exe", "url": options.windowsArm64ExecutableURL,
		"size": int64(len("Windows ARM64 executable fixture")), "sha256": hex.EncodeToString(arm64ExecutableDigest[:]),
	}
	if !reflect.DeepEqual(packages["android"], androidExpected) {
		t.Fatalf("Android package mismatch\ngot:  %#v\nwant: %#v", packages["android"], androidExpected)
	}
	if !reflect.DeepEqual(packages["windowsX64"], windowsX64Expected) {
		t.Fatalf("Windows x64 package mismatch\ngot:  %#v\nwant: %#v", packages["windowsX64"], windowsX64Expected)
	}
	if len(packages) != 3 {
		t.Fatalf("unexpected package contract: %#v", packages)
	}
	if !reflect.DeepEqual(packages["windowsArm64"], windowsArm64Expected) {
		t.Fatalf("Windows ARM64 package mismatch\ngot:  %#v\nwant: %#v", packages["windowsArm64"], windowsArm64Expected)
	}
}

func TestAcceptsAdditionalRootPackageAndPlatformFields(t *testing.T) {
	_, manifest := fixture(t)
	manifest["futureRootField"] = true
	packages := manifest["packages"].(map[string]any)
	packages["linux"] = map[string]any{"format": "future"}
	packages["android"].(map[string]any)["futureAndroidField"] = map[string]any{"value": 1}
	packages["windowsX64"].(map[string]any)["futureWindowsX64Field"] = true
	packages["windowsArm64"].(map[string]any)["futureWindowsArm64Field"] = true
	if err := validateManifest(manifest); err != nil {
		t.Fatal(err)
	}
}

func TestPrereleaseChannelUsesSemverVersion(t *testing.T) {
	options, _ := fixture(t)
	options.tagName = "v2.0.0-rc.1"
	options.channel = "prerelease"
	options.releaseURL = "https://github.com/moodiness/rivune/releases/tag/v2.0.0-rc.1"
	options.apkURL = "https://github.com/moodiness/rivune/releases/download/v2.0.0-rc.1/Rivune-Android.apk"
	options.windowsX64ExecutableURL = "https://github.com/moodiness/rivune/releases/download/v2.0.0-rc.1/Rivune-x64.exe"
	options.windowsArm64ExecutableURL = "https://github.com/moodiness/rivune/releases/download/v2.0.0-rc.1/Rivune-arm64.exe"
	manifest, err := buildManifest(options)
	if err != nil {
		t.Fatal(err)
	}
	if manifest["version"] != "2.0.0-rc.1" {
		t.Fatalf("version = %v", manifest["version"])
	}
}

func TestRejectsEachMissingRequiredRootAndPlatform(t *testing.T) {
	_, manifest := fixture(t)
	for _, field := range []string{"schemaVersion", "channel", "version", "tagName", "publishedAt", "releaseUrl", "packages"} {
		t.Run("root_"+field, func(t *testing.T) {
			invalid := cloneManifest(t, manifest)
			delete(invalid, field)
			assertInvalid(t, invalid)
		})
	}
	for _, platform := range []string{"android", "windowsX64", "windowsArm64"} {
		t.Run("platform_"+platform, func(t *testing.T) {
			invalid := cloneManifest(t, manifest)
			delete(invalid["packages"].(map[string]any), platform)
			assertInvalid(t, invalid)
		})
	}
}

func TestRejectsEachMissingRequiredKnownPackageField(t *testing.T) {
	_, manifest := fixture(t)
	packages := manifest["packages"].(map[string]any)
	for _, platform := range []string{"android", "windowsX64", "windowsArm64"} {
		for field := range packages[platform].(map[string]any) {
			t.Run(platform+"_"+field, func(t *testing.T) {
				invalid := cloneManifest(t, manifest)
				delete(invalid["packages"].(map[string]any)[platform].(map[string]any), field)
				assertInvalid(t, invalid)
			})
		}
	}
}

func TestRejectsObsoleteWindowsPackageFields(t *testing.T) {
	_, manifest := fixture(t)
	for _, platform := range []string{"windowsX64", "windowsArm64"} {
		for _, field := range []string{"identityName", "publisher", "packageVersion", "signingCertificateSha256"} {
			t.Run(platform+"_"+field, func(t *testing.T) {
				invalid := cloneManifest(t, manifest)
				invalid["packages"].(map[string]any)[platform].(map[string]any)[field] = "obsolete"
				assertInvalid(t, invalid)
			})
		}
	}
}

func TestRejectsWrongFixedPlatformContracts(t *testing.T) {
	_, manifest := fixture(t)
	cases := []struct {
		platform string
		field    string
		value    any
	}{
		{"android", "format", "aab"},
		{"android", "architectures", []string{"arm64"}},

		{"android", "minimumOsVersion", "7.0"},
		{"android", "applicationId", "io.example.other"},
		{"windowsX64", "format", "zip"},
		{"windowsX64", "architectures", []string{"arm64"}},
		{"windowsX64", "minimumOsVersion", "10.0.17763.0"},
		{"windowsArm64", "format", "zip"},
		{"windowsArm64", "architectures", []string{"x64"}},
		{"windowsArm64", "minimumOsVersion", "10.0.17763.0"},
	}
	for _, testCase := range cases {
		t.Run(testCase.platform+"_"+testCase.field, func(t *testing.T) {
			invalid := cloneManifest(t, manifest)
			invalid["packages"].(map[string]any)[testCase.platform].(map[string]any)[testCase.field] = testCase.value
			assertInvalid(t, invalid)
		})
	}
}

func TestPackageSizeBoundaries(t *testing.T) {
	_, manifest := fixture(t)
	cases := []struct {
		platform string
		maximum  int64
	}{
		{"android", maxAndroidPackageSize},
		{"windowsX64", maxWindowsPackageSize},
		{"windowsArm64", maxWindowsPackageSize},
	}
	setPackageSize := func(root map[string]any, platform string, size int64) {
		packages := root["packages"].(map[string]any)
		packages[platform].(map[string]any)["size"] = size
	}
	for _, testCase := range cases {
		t.Run(testCase.platform+"_exact_limit", func(t *testing.T) {
			valid := cloneManifest(t, manifest)
			setPackageSize(valid, testCase.platform, testCase.maximum)
			if err := validateManifest(valid); err != nil {
				t.Fatal(err)
			}
			if err := validatePackageSize(testCase.maximum, testCase.maximum, testCase.platform); err != nil {
				t.Fatal(err)
			}
		})
		t.Run(testCase.platform+"_limit_plus_one", func(t *testing.T) {
			invalid := cloneManifest(t, manifest)
			setPackageSize(invalid, testCase.platform, testCase.maximum+1)
			assertInvalid(t, invalid)
			if err := validatePackageSize(testCase.maximum+1, testCase.maximum, testCase.platform); err == nil {
				t.Fatal("oversized generated asset metadata was accepted")
			}
		})
	}
}

func TestRejectsUnsafeAndWrongTagAssetURLs(t *testing.T) {
	_, manifest := fixture(t)
	cases := []struct {
		platform string
		field    string
		value    string
	}{
		{"android", "url", "https://evil.example/Rivune-Android.apk"},
		{"android", "url", "https://github.com/moodiness/rivune/releases/download/v1.2.4/Rivune-Android.apk"},
		{"android", "fileName", "rivune-android-1.2.3.apk"},
		{"android", "fileName", "../Rivune-Android.apk"},
		{"windowsX64", "url", "https://github.com/moodiness/rivune/releases/download/v1.2.4/Rivune-x64.exe"},
		{"windowsX64", "url", "https://evil.example/Rivune-x64.exe"},
		{"windowsX64", "fileName", "other.exe"},
		{"windowsArm64", "url", "https://github.com/moodiness/rivune/releases/download/v1.2.4/Rivune-arm64.exe"},
		{"windowsArm64", "url", "https://evil.example/Rivune-arm64.exe"},
		{"windowsArm64", "fileName", "rivune-arm64.exe"},
	}
	for _, testCase := range cases {
		t.Run(testCase.platform+"_"+testCase.field, func(t *testing.T) {
			invalid := cloneManifest(t, manifest)
			invalid["packages"].(map[string]any)[testCase.platform].(map[string]any)[testCase.field] = testCase.value
			assertInvalid(t, invalid)
		})
	}
	invalid := cloneManifest(t, manifest)
	invalid["releaseUrl"] = "https://github.com/other/rivune/releases/tag/v1.2.3"
	assertInvalid(t, invalid)
}

func TestRejectsMalformedVersionBuildCertificateAndDigestFields(t *testing.T) {
	_, manifest := fixture(t)
	mutations := []func(map[string]any){
		func(root map[string]any) { root["version"] = "v1.2.3" },
		func(root map[string]any) { root["tagName"] = "v1.2.4" },
		func(root map[string]any) { root["channel"] = "prerelease" },
		func(root map[string]any) { root["publishedAt"] = "2026-13-99" },
		func(root map[string]any) {
			root["packages"].(map[string]any)["android"].(map[string]any)["buildVersion"] = "01"
		},
		func(root map[string]any) {
			root["packages"].(map[string]any)["android"].(map[string]any)["signingCertificateSha256"] = strings.Repeat("A", 64)
		},
		func(root map[string]any) {
			root["packages"].(map[string]any)["windowsX64"].(map[string]any)["sha256"] = strings.Repeat("F", 64)
		},
		func(root map[string]any) {
			root["packages"].(map[string]any)["windowsArm64"].(map[string]any)["sha256"] = strings.Repeat("F", 64)
		},
	}
	for index, mutate := range mutations {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			invalid := cloneManifest(t, manifest)
			mutate(invalid)
			assertInvalid(t, invalid)
		})
	}
}

func TestRejectsMismatchedLocalAssetMetadataAndExpectedFields(t *testing.T) {
	options, manifest := fixture(t)
	validate := validateOptions{
		apk: options.apk, windowsX64Executable: options.windowsX64Executable,
		windowsArm64Executable: options.windowsArm64Executable, channel: options.channel, tagName: options.tagName,
		publishedAt: options.publishedAt, releaseURL: options.releaseURL, apkURL: options.apkURL,
		applicationID: options.applicationID, buildVersion: options.buildVersion,
		signingCertificateSHA256: options.signingCertificateSHA256,
		windowsX64ExecutableURL:  options.windowsX64ExecutableURL, windowsArm64ExecutableURL: options.windowsArm64ExecutableURL,
	}
	if err := validateExpectedValues(manifest, validate); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(options.apk, []byte("different APK"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateExpectedValues(manifest, validate); err == nil {
		t.Fatal("mismatched local APK was accepted")
	}
	validate.apk = ""
	if err := os.WriteFile(options.windowsX64Executable, []byte("different Windows x64 executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateExpectedValues(manifest, validate); err == nil {
		t.Fatal("mismatched local Windows x64 executable was accepted")
	}
	validate.windowsX64Executable = ""
	validate.windowsX64ExecutableURL = "https://github.com/moodiness/rivune/releases/download/v1.2.4/Rivune-x64.exe"
	if err := validateExpectedValues(manifest, validate); err == nil {
		t.Fatal("mismatched expected Windows x64 executable URL was accepted")
	}
	validate.windowsX64ExecutableURL = ""
	if err := os.WriteFile(options.windowsArm64Executable, []byte("different Windows ARM64 executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateExpectedValues(manifest, validate); err == nil {
		t.Fatal("mismatched local Windows ARM64 executable was accepted")
	}
	validate.windowsArm64Executable = ""
	validate.windowsArm64ExecutableURL = "https://github.com/moodiness/rivune/releases/download/v1.2.4/Rivune-arm64.exe"
	if err := validateExpectedValues(manifest, validate); err == nil {
		t.Fatal("mismatched expected Windows ARM64 executable URL was accepted")
	}
}

func TestGenerateAndValidateGlobalManifest(t *testing.T) {
	options, _ := fixture(t)
	generateArguments := []string{
		"--apk", options.apk,
		"--windows-x64-executable", options.windowsX64Executable,
		"--windows-arm64-executable", options.windowsArm64Executable,
		"--output", options.output,
		"--channel", options.channel,
		"--tag-name", options.tagName,
		"--published-at", options.publishedAt,
		"--release-url", options.releaseURL,
		"--apk-url", options.apkURL,
		"--application-id", options.applicationID,
		"--build-version", options.buildVersion,
		"--signing-certificate-sha256", options.signingCertificateSHA256,
		"--windows-x64-executable-url", options.windowsX64ExecutableURL,
		"--windows-arm64-executable-url", options.windowsArm64ExecutableURL,
	}
	for _, flagName := range []string{"--windows-x64-executable", "--windows-x64-executable-url", "--windows-arm64-executable", "--windows-arm64-executable-url"} {
		err := runGenerate(argumentsWithoutFlag(generateArguments, flagName))
		if err == nil || !strings.Contains(err.Error(), flagName+" is required") {
			t.Fatalf("generate without %s returned %v", flagName, err)
		}
	}
	if err := runGenerate(generateArguments); err != nil {
		t.Fatal(err)
	}
	validateArguments := []string{
		"--apk", options.apk,
		"--windows-x64-executable", options.windowsX64Executable,
		"--windows-arm64-executable", options.windowsArm64Executable,
		"--channel", options.channel,
		"--tag-name", options.tagName,
		"--published-at", options.publishedAt,
		"--release-url", options.releaseURL,
		"--apk-url", options.apkURL,
		"--application-id", options.applicationID,
		"--build-version", options.buildVersion,
		"--signing-certificate-sha256", options.signingCertificateSHA256,
		"--windows-x64-executable-url", options.windowsX64ExecutableURL,
		"--windows-arm64-executable-url", options.windowsArm64ExecutableURL,
		options.output,
	}
	for _, flagName := range []string{"--windows-x64-executable", "--windows-x64-executable-url", "--windows-arm64-executable", "--windows-arm64-executable-url"} {
		err := runValidate(argumentsWithoutFlag(validateArguments, flagName))
		if err == nil || !strings.Contains(err.Error(), flagName+" is required") {
			t.Fatalf("validate without %s returned %v", flagName, err)
		}
	}
	if err := runValidate(validateArguments); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsDuplicateKeysAndMultipleDocuments(t *testing.T) {
	if _, err := decodeManifest([]byte(`{"schemaVersion":2,"packages":{"android":{},"android":{}}}`)); err == nil {
		t.Fatal("duplicate known package key was accepted")
	}
	if _, err := decodeManifest([]byte(`{} {}`)); err == nil {
		t.Fatal("multiple JSON documents were accepted")
	}
}

func argumentsWithoutFlag(arguments []string, omitted string) []string {
	filtered := make([]string, 0, len(arguments)-2)
	for index := 0; index < len(arguments); index++ {
		if arguments[index] == omitted {
			index++
			continue
		}
		filtered = append(filtered, arguments[index])
	}
	return filtered
}

func assertInvalid(t *testing.T, manifest map[string]any) {
	t.Helper()
	if err := validateManifest(manifest); err == nil {
		t.Fatal("invalid manifest was accepted")
	}
}

func cloneManifest(t *testing.T, manifest map[string]any) map[string]any {
	t.Helper()
	contents, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.UseNumber()
	if err := decoder.Decode(&clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func repeatHex(pair string, count int) string {
	return strings.Repeat(pair, count)
}
