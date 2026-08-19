package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func fixture(t *testing.T) (generateOptions, map[string]any) {
	t.Helper()
	directory := t.TempDir()
	apk := filepath.Join(directory, "rivune-android-1.2.3.apk")
	executable := filepath.Join(directory, "Rivune.exe")
	if err := os.WriteFile(apk, []byte("signed apk fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("Windows executable fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := generateOptions{
		apk:                      apk,
		windowsExecutable:        executable,
		output:                   filepath.Join(directory, "rivune-update.json"),
		legacyAndroidOutput:      filepath.Join(directory, "rivune-android-update.json"),
		channel:                  "stable",
		tagName:                  "v1.2.3",
		publishedAt:              "2026-08-14T12:34:56Z",
		releaseURL:               "https://github.com/moodiness/rivune/releases/tag/v1.2.3",
		apkURL:                   "https://github.com/moodiness/rivune/releases/download/v1.2.3/rivune-android-1.2.3.apk",
		applicationID:            androidApplicationID,
		buildVersion:             "123",
		signingCertificateSHA256: repeatHex("ab", 32),
		windowsExecutableURL:     "https://github.com/moodiness/rivune/releases/download/v1.2.3/Rivune.exe",
	}
	manifest, err := buildManifest(options)
	if err != nil {
		t.Fatal(err)
	}
	return options, manifest
}

func TestGeneratesExactTwoPlatformContract(t *testing.T) {
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
	executableDigest := sha256.Sum256([]byte("Windows executable fixture"))
	packages := manifest["packages"].(map[string]any)
	androidExpected := map[string]any{
		"format": "apk", "architectures": []string{"universal"}, "minimumOsVersion": "8.0",
		"applicationId": androidApplicationID, "buildVersion": "123",
		"signingCertificateSha256": repeatHex("ab", 32), "fileName": filepath.Base(options.apk),
		"url": options.apkURL, "size": int64(len("signed apk fixture")), "sha256": hex.EncodeToString(apkDigest[:]),
	}
	windowsExpected := map[string]any{
		"format": "exe", "architectures": []string{"x64"}, "minimumOsVersion": "10.0.19041.0",
		"fileName": "Rivune.exe", "url": options.windowsExecutableURL,
		"size": int64(len("Windows executable fixture")), "sha256": hex.EncodeToString(executableDigest[:]),
	}
	if !reflect.DeepEqual(packages["android"], androidExpected) {
		t.Fatalf("Android package mismatch\ngot:  %#v\nwant: %#v", packages["android"], androidExpected)
	}
	if !reflect.DeepEqual(packages["windows"], windowsExpected) {
		t.Fatalf("Windows package mismatch\ngot:  %#v\nwant: %#v", packages["windows"], windowsExpected)
	}
}

func TestLegacyManifestExactlyMirrorsGlobalAndroidEntry(t *testing.T) {
	_, manifest := fixture(t)
	legacy := buildLegacyAndroidManifest(manifest)
	if err := validateLegacyAndroidManifest(legacy); err != nil {
		t.Fatal(err)
	}
	if err := validateLegacyMatchesGlobal(legacy, manifest); err != nil {
		t.Fatal(err)
	}
	packages := manifest["packages"].(map[string]any)
	if !reflect.DeepEqual(legacy["package"], packages["android"]) {
		t.Fatal("legacy package is not identical to the global Android entry")
	}
	legacy["package"].(map[string]any)["buildVersion"] = "124"
	if err := validateLegacyMatchesGlobal(legacy, manifest); err == nil {
		t.Fatal("mismatched legacy Android package was accepted")
	}
}

func TestAcceptsAdditionalRootPackageAndPlatformFields(t *testing.T) {
	_, manifest := fixture(t)
	manifest["futureRootField"] = true
	packages := manifest["packages"].(map[string]any)
	packages["linux"] = map[string]any{"format": "future"}
	packages["android"].(map[string]any)["futureAndroidField"] = map[string]any{"value": 1}
	packages["windows"].(map[string]any)["futureWindowsField"] = true
	if err := validateManifest(manifest); err != nil {
		t.Fatal(err)
	}
}

func TestPrereleaseChannelUsesSemverVersion(t *testing.T) {
	options, _ := fixture(t)
	options.tagName = "v2.0.0-rc.1"
	options.channel = "prerelease"
	options.releaseURL = "https://github.com/moodiness/rivune/releases/tag/v2.0.0-rc.1"
	directory := filepath.Dir(options.apk)
	options.apk = filepath.Join(directory, "rivune-android-2.0.0-rc.1.apk")
	if err := os.WriteFile(options.apk, []byte("signed apk fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	options.apkURL = "https://github.com/moodiness/rivune/releases/download/v2.0.0-rc.1/rivune-android-2.0.0-rc.1.apk"
	options.windowsExecutableURL = "https://github.com/moodiness/rivune/releases/download/v2.0.0-rc.1/Rivune.exe"
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
	for _, platform := range []string{"android", "windows"} {
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
	for _, platform := range []string{"android", "windows"} {
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
	for _, field := range []string{"identityName", "publisher", "packageVersion", "signingCertificateSha256"} {
		t.Run(field, func(t *testing.T) {
			invalid := cloneManifest(t, manifest)
			invalid["packages"].(map[string]any)["windows"].(map[string]any)[field] = "obsolete"
			assertInvalid(t, invalid)
		})
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
		{"windows", "format", "zip"},
		{"windows", "architectures", []string{"arm64"}},
		{"windows", "minimumOsVersion", "10.0.17763.0"},
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
		{"windows", maxWindowsPackageSize},
	}
	for _, testCase := range cases {
		t.Run(testCase.platform+"_exact_limit", func(t *testing.T) {
			valid := cloneManifest(t, manifest)
			valid["packages"].(map[string]any)[testCase.platform].(map[string]any)["size"] = testCase.maximum
			if err := validateManifest(valid); err != nil {
				t.Fatal(err)
			}
			if err := validatePackageSize(testCase.maximum, testCase.maximum, testCase.platform); err != nil {
				t.Fatal(err)
			}
		})
		t.Run(testCase.platform+"_limit_plus_one", func(t *testing.T) {
			invalid := cloneManifest(t, manifest)
			invalid["packages"].(map[string]any)[testCase.platform].(map[string]any)["size"] = testCase.maximum + 1
			assertInvalid(t, invalid)
			if err := validatePackageSize(testCase.maximum+1, testCase.maximum, testCase.platform); err == nil {
				t.Fatal("oversized generated asset metadata was accepted")
			}
		})
	}
}

func TestOutputPathAliases(t *testing.T) {
	directory := t.TempDir()
	absolute := filepath.Join(directory, "manifest.json")
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(workingDirectory, absolute)
	if err != nil {
		t.Fatal(err)
	}
	aliased, err := outputPathsAlias(relative, absolute)
	if err != nil {
		t.Fatal(err)
	}
	if !aliased {
		t.Fatal("relative and absolute paths to the same output were not detected")
	}

	if runtime.GOOS == "windows" {
		aliased, err := outputPathsAlias(absolute, strings.ToUpper(absolute))
		if err != nil {
			t.Fatal(err)
		}
		if !aliased {
			t.Fatal("case-insensitive Windows output alias was not detected")
		}
	}
	t.Run("symlinked_parent", func(t *testing.T) {
		realDirectory := t.TempDir()
		linkDirectory := filepath.Join(t.TempDir(), "linked")
		if err := os.Symlink(realDirectory, linkDirectory); err != nil {
			t.Skipf("directory symlinks unavailable: %v", err)
		}
		aliased, err := outputPathsAlias(
			filepath.Join(realDirectory, "manifest.json"),
			filepath.Join(linkDirectory, "manifest.json"),
		)
		if err != nil {
			t.Fatal(err)
		}
		if !aliased {
			t.Fatal("symlinked output parent was not resolved")
		}
	})

	t.Run("existing_hard_link", func(t *testing.T) {
		first := filepath.Join(t.TempDir(), "first.json")
		second := filepath.Join(filepath.Dir(first), "second.json")
		if err := os.WriteFile(first, []byte("manifest"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(first, second); err != nil {
			t.Skipf("hard links unavailable: %v", err)
		}
		aliased, err := outputPathsAlias(first, second)
		if err != nil {
			t.Fatal(err)
		}
		if !aliased {
			t.Fatal("hard-linked output files were not detected")
		}
	})
}

func TestRejectsUnsafeAndWrongTagAssetURLs(t *testing.T) {
	_, manifest := fixture(t)
	cases := []struct {
		platform string
		field    string
		value    string
	}{
		{"android", "url", "https://evil.example/rivune.apk"},
		{"android", "url", "https://github.com/moodiness/rivune/releases/download/v1.2.4/rivune-android-1.2.3.apk"},
		{"android", "fileName", "../rivune.apk"},
		{"windows", "url", "http://github.com/moodiness/rivune/releases/download/v1.2.3/Rivune.exe"},
		{"windows", "url", "https://github.com/other/rivune/releases/download/v1.2.3/Rivune.exe"},
		{"windows", "fileName", "rivune.exe"},
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
			root["packages"].(map[string]any)["windows"].(map[string]any)["sha256"] = strings.Repeat("F", 64)
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
		apk: options.apk, windowsExecutable: options.windowsExecutable, channel: options.channel, tagName: options.tagName,
		publishedAt: options.publishedAt, releaseURL: options.releaseURL, apkURL: options.apkURL, applicationID: options.applicationID,
		buildVersion: options.buildVersion, signingCertificateSHA256: options.signingCertificateSHA256,
		windowsExecutableURL: options.windowsExecutableURL,
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
	if err := os.WriteFile(options.windowsExecutable, []byte("different Windows executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateExpectedValues(manifest, validate); err == nil {
		t.Fatal("mismatched local Windows executable was accepted")
	}
	validate.windowsExecutable = ""
	validate.windowsExecutableURL = "https://github.com/moodiness/rivune/releases/download/v1.2.4/Rivune.exe"
	if err := validateExpectedValues(manifest, validate); err == nil {
		t.Fatal("mismatched expected Windows executable URL was accepted")
	}
}

func TestGenerateAndValidateGlobalAndLegacyFilesTogether(t *testing.T) {
	options, _ := fixture(t)
	generateArguments := []string{
		"--apk", options.apk,
		"--windows-executable", options.windowsExecutable,
		"--output", options.output,
		"--legacy-android-output", options.legacyAndroidOutput,
		"--channel", options.channel,
		"--tag-name", options.tagName,
		"--published-at", options.publishedAt,
		"--release-url", options.releaseURL,
		"--apk-url", options.apkURL,
		"--application-id", options.applicationID,
		"--build-version", options.buildVersion,
		"--signing-certificate-sha256", options.signingCertificateSHA256,
		"--windows-executable-url", options.windowsExecutableURL,
	}
	if err := runGenerate(generateArguments); err != nil {
		t.Fatal(err)
	}
	validateArguments := []string{
		"--apk", options.apk,
		"--windows-executable", options.windowsExecutable,
		"--legacy-android-manifest", options.legacyAndroidOutput,
		"--channel", options.channel,
		"--tag-name", options.tagName,
		"--published-at", options.publishedAt,
		"--release-url", options.releaseURL,
		"--apk-url", options.apkURL,
		"--application-id", options.applicationID,
		"--build-version", options.buildVersion,
		"--signing-certificate-sha256", options.signingCertificateSHA256,
		"--windows-executable-url", options.windowsExecutableURL,
		options.output,
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
