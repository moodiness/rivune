package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	x64FixtureContents         = "Windows x64 executable fixture"
	arm64FixtureContents       = "Windows ARM64 executable fixture"
	launcherFixtureContents    = "Windows universal launcher fixture"
	uninstallerFixtureContents = "Windows uninstaller fixture"
	iosFixtureContents         = "unsigned iOS archive fixture"
	tvosFixtureContents        = "unsigned tvOS archive fixture"
	visionosFixtureContents    = "unsigned visionOS archive fixture"
	macosFixtureContents       = "unsigned macOS disk image fixture"
	webosFixtureContents       = "unsigned webOS IPK fixture"
	tizenFixtureContents       = "unsigned Tizen WGT fixture"
	tvRuntimeFixtureContents   = "shared webOS and Tizen runtime fixture"
)

func fixture(t *testing.T) (generateOptions, map[string]any) {
	t.Helper()
	directory := t.TempDir()
	asset := func(name, contents string) string {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	options := generateOptions{
		apk:                      asset(androidAssetFileName, "signed apk fixture"),
		iosArchive:               asset(iosAssetFileName, iosFixtureContents),
		tvosArchive:              asset(tvosAssetFileName, tvosFixtureContents),
		visionosArchive:          asset(visionosAssetFileName, visionosFixtureContents),
		macosDiskImage:           asset(macosAssetFileName, macosFixtureContents),
		webosPackage:             asset(webosAssetFileName, webosFixtureContents),
		tizenPackage:             asset(tizenAssetFileName, tizenFixtureContents),
		tvRuntime:                asset(tvRuntimeAssetFileName, tvRuntimeFixtureContents),
		windowsExecutable:        windowsExecutableFixture(t, directory),
		output:                   filepath.Join(directory, "rivune-update.json"),
		channel:                  "stable",
		tagName:                  "v1.12.0",
		publishedAt:              "2026-08-14T12:34:56Z",
		releaseURL:               "https://github.com/moodiness/rivune/releases/tag/v1.12.0",
		apkURL:                   releaseAssetURL("v1.12.0", androidAssetFileName),
		iosArchiveURL:            releaseAssetURL("v1.12.0", iosAssetFileName),
		tvosArchiveURL:           releaseAssetURL("v1.12.0", tvosAssetFileName),
		visionosArchiveURL:       releaseAssetURL("v1.12.0", visionosAssetFileName),
		macosDiskImageURL:        releaseAssetURL("v1.12.0", macosAssetFileName),
		webosPackageURL:          releaseAssetURL("v1.12.0", webosAssetFileName),
		tizenPackageURL:          releaseAssetURL("v1.12.0", tizenAssetFileName),
		tvRuntimeURL:             releaseAssetURL("v1.12.0", tvRuntimeAssetFileName),
		applicationID:            androidApplicationID,
		buildVersion:             "123",
		signingCertificateSHA256: repeatHex("ab", 32),
		windowsExecutableURL:     releaseAssetURL("v1.12.0", windowsAssetFileName),
	}
	manifest, err := buildManifest(options)
	if err != nil {
		t.Fatal(err)
	}
	return options, manifest
}

func windowsExecutableFixture(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, windowsAssetFileName)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(launcherFixtureContents)); err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	archive.SetOffset(int64(len(launcherFixtureContents)))
	for name, contents := range map[string]string{
		windowsX64Name:         x64FixtureContents,
		windowsArm64Name:       arm64FixtureContents,
		windowsUninstallerName: uninstallerFixtureContents,
	} {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		writer, createErr := archive.CreateHeader(header)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := writer.Write([]byte(contents)); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func releaseAssetURL(tag, name string) string {
	return "https://github.com/moodiness/rivune/releases/download/" + tag + "/" + name
}

func assetDigest(contents string) string {
	digest := sha256.Sum256([]byte(contents))
	return hex.EncodeToString(digest[:])
}

func TestGeneratesExactNinePackageContract(t *testing.T) {
	options, manifest := fixture(t)
	expectedRootFields := []string{"schemaVersion", "channel", "version", "tagName", "publishedAt", "releaseUrl", "packages"}
	for _, field := range expectedRootFields {
		if _, ok := manifest[field]; !ok {
			t.Fatalf("missing root field %q", field)
		}
	}
	if len(manifest) != len(expectedRootFields) || manifest["schemaVersion"] != 3 || manifest["version"] != "1.12.0" {
		t.Fatalf("wrong root contract: %#v", manifest)
	}
	packages := manifest["packages"].(map[string]any)
	expected := map[string]map[string]any{
		"android": {
			"format": "apk", "architectures": []string{"universal"}, "minimumOsVersion": "8.0",
			"applicationId": androidApplicationID, "buildVersion": "123", "signature": "signed",
			"signingCertificateSha256": repeatHex("ab", 32), "fileName": androidAssetFileName,
			"url": options.apkURL, "size": int64(len("signed apk fixture")), "sha256": assetDigest("signed apk fixture"),
		},
		"ios":       expectedApplePackage("ipa", []string{"arm64"}, "15.0", "io.rivune.app", iosAssetFileName, options.iosArchiveURL, iosFixtureContents),
		"tvos":      expectedApplePackage("ipa", []string{"arm64"}, "15.0", "io.rivune.app.tv", tvosAssetFileName, options.tvosArchiveURL, tvosFixtureContents),
		"visionos":  expectedApplePackage("ipa", []string{"arm64"}, "1.0", "io.rivune.app.vision", visionosAssetFileName, options.visionosArchiveURL, visionosFixtureContents),
		"macos":     expectedApplePackage("dmg", []string{"arm64", "x64"}, "12.0", "io.rivune.app.mac", macosAssetFileName, options.macosDiskImageURL, macosFixtureContents),
		"webos":     expectedTVPackage("ipk", "4.0", "io.rivune.app.webos", webosAssetFileName, options.webosPackageURL, webosFixtureContents),
		"tizen":     expectedTVPackage("wgt", "5.5", "RivuneTV01.Rivune", tizenAssetFileName, options.tizenPackageURL, tizenFixtureContents),
		"tvRuntime": expectedTVRuntimePackage(options.tvRuntimeURL, tvRuntimeFixtureContents),
		"windows": {
			"format": "exe", "architectures": []string{"arm64", "x64"}, "minimumOsVersion": "10.0.19041.0", "signature": "unsigned",
			"fileName": windowsAssetFileName, "url": options.windowsExecutableURL,
			"size":   manifest["packages"].(map[string]any)["windows"].(map[string]any)["size"],
			"sha256": manifest["packages"].(map[string]any)["windows"].(map[string]any)["sha256"],
			"executables": map[string]any{
				"x64":   map[string]any{"fileName": windowsX64Name, "size": int64(len(x64FixtureContents)), "sha256": assetDigest(x64FixtureContents)},
				"arm64": map[string]any{"fileName": windowsArm64Name, "size": int64(len(arm64FixtureContents)), "sha256": assetDigest(arm64FixtureContents)},
			},
		},
	}
	if len(packages) != len(expected) {
		t.Fatalf("unexpected package contract: %#v", packages)
	}
	for name, want := range expected {
		if !reflect.DeepEqual(packages[name], want) {
			t.Fatalf("%s package mismatch\ngot:  %#v\nwant: %#v", name, packages[name], want)
		}
	}
}

func expectedApplePackage(format string, architectures []string, minimumOSVersion, bundleIdentifier, fileName, url, contents string) map[string]any {
	return map[string]any{
		"format": format, "architectures": architectures, "minimumOsVersion": minimumOSVersion,
		"bundleIdentifier": bundleIdentifier, "signature": "unsigned", "fileName": fileName,
		"url": url, "size": int64(len(contents)), "sha256": assetDigest(contents),
	}
}

func expectedTVPackage(format, minimumOSVersion, applicationID, fileName, url, contents string) map[string]any {
	return map[string]any{
		"format": format, "architectures": []string{"universal"}, "minimumOsVersion": minimumOSVersion,
		"applicationId": applicationID, "signature": "unsigned", "fileName": fileName,
		"url": url, "size": int64(len(contents)), "sha256": assetDigest(contents),
	}
}

func expectedTVRuntimePackage(url, contents string) map[string]any {
	return map[string]any{
		"format": "json", "platforms": []string{"webos", "tizen"}, "fileName": tvRuntimeAssetFileName,
		"url": url, "size": int64(len(contents)), "sha256": assetDigest(contents),
	}
}

func TestRejectsUnknownRootPackageAndPlatformFields(t *testing.T) {
	_, manifest := fixture(t)
	mutations := []func(map[string]any){
		func(root map[string]any) { root["futureRootField"] = true },
		func(root map[string]any) {
			root["packages"].(map[string]any)["linux"] = map[string]any{"format": "future"}
		},
		func(root map[string]any) {
			root["packages"].(map[string]any)["android"].(map[string]any)["futureAndroidField"] = true
		},
		func(root map[string]any) {
			root["packages"].(map[string]any)["webos"].(map[string]any)["futureWebOSField"] = true
		},
		func(root map[string]any) {
			root["packages"].(map[string]any)["tizen"].(map[string]any)["futureTizenField"] = true
		},
		func(root map[string]any) {
			root["packages"].(map[string]any)["tvRuntime"].(map[string]any)["futureRuntimeField"] = true
		},
		func(root map[string]any) {
			root["packages"].(map[string]any)["windows"].(map[string]any)["futureWindowsField"] = true
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

func TestPrereleaseChannelUsesSemverVersion(t *testing.T) {
	options, _ := fixture(t)
	options.tagName = "v2.0.0-rc.1"
	options.channel = "prerelease"
	options.releaseURL = githubReleaseURLPrefix + "/tag/" + options.tagName
	options.apkURL = releaseAssetURL(options.tagName, androidAssetFileName)
	options.iosArchiveURL = releaseAssetURL(options.tagName, iosAssetFileName)
	options.tvosArchiveURL = releaseAssetURL(options.tagName, tvosAssetFileName)
	options.visionosArchiveURL = releaseAssetURL(options.tagName, visionosAssetFileName)
	options.macosDiskImageURL = releaseAssetURL(options.tagName, macosAssetFileName)
	options.webosPackageURL = releaseAssetURL(options.tagName, webosAssetFileName)
	options.tizenPackageURL = releaseAssetURL(options.tagName, tizenAssetFileName)
	options.tvRuntimeURL = releaseAssetURL(options.tagName, tvRuntimeAssetFileName)
	options.windowsExecutableURL = releaseAssetURL(options.tagName, windowsAssetFileName)
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
	for _, platform := range []string{"android", "ios", "tvos", "visionos", "macos", "webos", "tizen", "tvRuntime", "windows"} {
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
	for _, platform := range []string{"android", "ios", "tvos", "visionos", "macos", "webos", "tizen", "tvRuntime", "windows"} {
		for field := range packages[platform].(map[string]any) {
			t.Run(platform+"_"+field, func(t *testing.T) {
				invalid := cloneManifest(t, manifest)
				delete(invalid["packages"].(map[string]any)[platform].(map[string]any), field)
				assertInvalid(t, invalid)
			})
		}
	}
}

func TestRejectsInvalidWindowsExecutableContracts(t *testing.T) {
	_, manifest := fixture(t)
	for _, architecture := range []string{"x64", "arm64"} {
		t.Run(architecture+"_missing", func(t *testing.T) {
			invalid := cloneManifest(t, manifest)
			executables := invalid["packages"].(map[string]any)["windows"].(map[string]any)["executables"].(map[string]any)
			delete(executables, architecture)
			assertInvalid(t, invalid)
		})
		for _, field := range []string{"fileName", "size", "sha256"} {
			t.Run(architecture+"_"+field, func(t *testing.T) {
				invalid := cloneManifest(t, manifest)
				executable := invalid["packages"].(map[string]any)["windows"].(map[string]any)["executables"].(map[string]any)[architecture].(map[string]any)
				delete(executable, field)
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
		{"android", "signature", "unsigned"},
		{"ios", "format", "dmg"},
		{"ios", "bundleIdentifier", "io.example.other"},
		{"ios", "signature", "signed"},
		{"tvos", "architectures", []string{"x64"}},
		{"visionos", "minimumOsVersion", "2.0"},
		{"macos", "architectures", []string{"arm64"}},
		{"webos", "format", "wgt"},
		{"webos", "architectures", []string{"arm64"}},
		{"webos", "minimumOsVersion", "3.0"},
		{"webos", "applicationId", "io.example.other"},
		{"webos", "signature", "signed"},
		{"tizen", "format", "ipk"},
		{"tizen", "architectures", []string{"arm64"}},
		{"tizen", "minimumOsVersion", "5.0"},
		{"tizen", "applicationId", "io.example.other"},
		{"tizen", "signature", "signed"},
		{"tvRuntime", "format", "zip"},
		{"tvRuntime", "platforms", []string{"tizen", "webos"}},
		{"windows", "format", "zip"},
		{"windows", "architectures", []string{"x64"}},
		{"windows", "minimumOsVersion", "10.0.17763.0"},
		{"windows", "signature", "signed"},
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
		{"ios", maxApplePackageSize},
		{"tvos", maxApplePackageSize},
		{"visionos", maxApplePackageSize},
		{"macos", maxApplePackageSize},
		{"webos", maxTVPackageSize},
		{"tizen", maxTVPackageSize},
		{"tvRuntime", maxTVRuntimeSize},
		{"windows", maxWindowsPackageSize},
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
		{"android", "url", "https://github.com/moodiness/rivune/releases/download/v1.12.1/Rivune-Android.apk"},
		{"android", "fileName", "rivune-android-1.12.0.apk"},
		{"android", "fileName", "../Rivune-Android.apk"},
		{"ios", "url", "https://evil.example/Rivune-iOS-unsigned.ipa"},
		{"ios", "fileName", "Rivune-iOS.ipa"},
		{"tvos", "url", "https://github.com/moodiness/rivune/releases/download/v1.12.1/Rivune-tvOS-unsigned.ipa"},
		{"visionos", "fileName", "../Rivune-visionOS-unsigned.ipa"},
		{"macos", "url", "https://evil.example/Rivune-macOS.dmg"},
		{"webos", "url", "https://evil.example/Rivune-webOS.ipk"},
		{"webos", "fileName", "Rivune-webos.ipk"},
		{"tizen", "url", "https://github.com/moodiness/rivune/releases/download/v1.12.1/Rivune-Tizen.wgt"},
		{"tizen", "fileName", "../Rivune-Tizen.wgt"},
		{"tvRuntime", "url", "https://evil.example/Rivune-TV-runtime.json"},
		{"tvRuntime", "fileName", "../Rivune-TV-runtime.json"},
		{"windows", "url", "https://github.com/moodiness/rivune/releases/download/v1.12.1/Rivune-Windows.exe"},
		{"windows", "url", "https://evil.example/Rivune-Windows.exe"},
		{"windows", "fileName", "other.exe"},
	}
	for _, testCase := range cases {
		t.Run(testCase.platform+"_"+testCase.field, func(t *testing.T) {
			invalid := cloneManifest(t, manifest)
			invalid["packages"].(map[string]any)[testCase.platform].(map[string]any)[testCase.field] = testCase.value
			assertInvalid(t, invalid)
		})
	}
	invalid := cloneManifest(t, manifest)
	invalid["releaseUrl"] = "https://github.com/other/rivune/releases/tag/v1.12.0"
	assertInvalid(t, invalid)
}

func TestRejectsMalformedVersionBuildCertificateAndDigestFields(t *testing.T) {
	_, manifest := fixture(t)
	mutations := []func(map[string]any){
		func(root map[string]any) { root["version"] = "v1.12.0" },
		func(root map[string]any) { root["tagName"] = "v1.12.1" },
		func(root map[string]any) { root["channel"] = "prerelease" },
		func(root map[string]any) { root["publishedAt"] = "2026-13-99" },
		func(root map[string]any) {
			root["packages"].(map[string]any)["android"].(map[string]any)["buildVersion"] = "01"
		},
		func(root map[string]any) {
			root["packages"].(map[string]any)["android"].(map[string]any)["signingCertificateSha256"] = strings.Repeat("A", 64)
		},
		func(root map[string]any) {
			root["packages"].(map[string]any)["ios"].(map[string]any)["sha256"] = strings.Repeat("F", 64)
		},
		func(root map[string]any) {
			root["packages"].(map[string]any)["webos"].(map[string]any)["sha256"] = strings.Repeat("F", 64)
		},
		func(root map[string]any) {
			root["packages"].(map[string]any)["tizen"].(map[string]any)["sha256"] = strings.Repeat("F", 64)
		},
		func(root map[string]any) {
			root["packages"].(map[string]any)["tvRuntime"].(map[string]any)["sha256"] = strings.Repeat("F", 64)
		},
		func(root map[string]any) {
			root["packages"].(map[string]any)["windows"].(map[string]any)["sha256"] = strings.Repeat("F", 64)
		},
		func(root map[string]any) {
			root["packages"].(map[string]any)["windows"].(map[string]any)["executables"].(map[string]any)["x64"].(map[string]any)["sha256"] = strings.Repeat("F", 64)
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
		apk: options.apk, iosArchive: options.iosArchive, tvosArchive: options.tvosArchive,
		visionosArchive: options.visionosArchive, macosDiskImage: options.macosDiskImage,
		webosPackage: options.webosPackage, tizenPackage: options.tizenPackage,
		tvRuntime: options.tvRuntime, windowsExecutable: options.windowsExecutable,
		channel: options.channel, tagName: options.tagName, publishedAt: options.publishedAt,
		releaseURL: options.releaseURL, apkURL: options.apkURL,
		iosArchiveURL: options.iosArchiveURL, tvosArchiveURL: options.tvosArchiveURL,
		visionosArchiveURL: options.visionosArchiveURL, macosDiskImageURL: options.macosDiskImageURL,
		webosPackageURL: options.webosPackageURL, tizenPackageURL: options.tizenPackageURL,
		tvRuntimeURL:  options.tvRuntimeURL,
		applicationID: options.applicationID, buildVersion: options.buildVersion,
		signingCertificateSHA256: options.signingCertificateSHA256,
		windowsExecutableURL:     options.windowsExecutableURL,
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
	if err := os.WriteFile(options.iosArchive, []byte("different iOS archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateExpectedValues(manifest, validate); err == nil {
		t.Fatal("mismatched local iOS archive was accepted")
	}
	validate.iosArchive = ""
	if err := os.WriteFile(options.webosPackage, []byte("different webOS package"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateExpectedValues(manifest, validate); err == nil {
		t.Fatal("mismatched local webOS package was accepted")
	}
	validate.webosPackage = ""
	validate.webosPackageURL = releaseAssetURL("v1.12.1", webosAssetFileName)
	if err := validateExpectedValues(manifest, validate); err == nil {
		t.Fatal("mismatched expected webOS package URL was accepted")
	}
	validate.webosPackageURL = ""
	if err := os.WriteFile(options.tizenPackage, []byte("different Tizen package"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateExpectedValues(manifest, validate); err == nil {
		t.Fatal("mismatched local Tizen package was accepted")
	}
	validate.tizenPackage = ""
	validate.tizenPackageURL = releaseAssetURL("v1.12.1", tizenAssetFileName)
	if err := validateExpectedValues(manifest, validate); err == nil {
		t.Fatal("mismatched expected Tizen package URL was accepted")
	}
	validate.tizenPackageURL = ""
	if err := os.WriteFile(options.tvRuntime, []byte("different TV runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateExpectedValues(manifest, validate); err == nil {
		t.Fatal("mismatched local TV runtime was accepted")
	}
	validate.tvRuntime = ""
	validate.tvRuntimeURL = releaseAssetURL("v1.12.1", tvRuntimeAssetFileName)
	if err := validateExpectedValues(manifest, validate); err == nil {
		t.Fatal("mismatched expected TV runtime URL was accepted")
	}
	validate.tvRuntimeURL = ""
	if err := os.WriteFile(options.windowsExecutable, []byte("different Windows executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateExpectedValues(manifest, validate); err == nil {
		t.Fatal("mismatched local Windows executable was accepted")
	}
	validate.windowsExecutable = ""
	validate.windowsExecutableURL = releaseAssetURL("v1.12.1", windowsAssetFileName)
	if err := validateExpectedValues(manifest, validate); err == nil {
		t.Fatal("mismatched expected Windows executable URL was accepted")
	}
}

func TestGenerateAndValidateGlobalManifest(t *testing.T) {
	options, _ := fixture(t)
	generateArguments := []string{
		"--apk", options.apk,
		"--ios-archive", options.iosArchive,
		"--tvos-archive", options.tvosArchive,
		"--visionos-archive", options.visionosArchive,
		"--macos-disk-image", options.macosDiskImage,
		"--webos-package", options.webosPackage,
		"--tizen-package", options.tizenPackage,
		"--tv-runtime", options.tvRuntime,
		"--windows-executable", options.windowsExecutable,
		"--output", options.output,
		"--channel", options.channel,
		"--tag-name", options.tagName,
		"--published-at", options.publishedAt,
		"--release-url", options.releaseURL,
		"--apk-url", options.apkURL,
		"--ios-archive-url", options.iosArchiveURL,
		"--tvos-archive-url", options.tvosArchiveURL,
		"--visionos-archive-url", options.visionosArchiveURL,
		"--macos-disk-image-url", options.macosDiskImageURL,
		"--webos-package-url", options.webosPackageURL,
		"--tizen-package-url", options.tizenPackageURL,
		"--tv-runtime-url", options.tvRuntimeURL,
		"--application-id", options.applicationID,
		"--build-version", options.buildVersion,
		"--signing-certificate-sha256", options.signingCertificateSHA256,
		"--windows-executable-url", options.windowsExecutableURL,
	}
	for _, flagName := range []string{"--ios-archive", "--ios-archive-url", "--tvos-archive", "--tvos-archive-url", "--visionos-archive", "--visionos-archive-url", "--macos-disk-image", "--macos-disk-image-url", "--webos-package", "--webos-package-url", "--tizen-package", "--tizen-package-url", "--tv-runtime", "--tv-runtime-url", "--windows-executable", "--windows-executable-url"} {
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
		"--ios-archive", options.iosArchive,
		"--tvos-archive", options.tvosArchive,
		"--visionos-archive", options.visionosArchive,
		"--macos-disk-image", options.macosDiskImage,
		"--webos-package", options.webosPackage,
		"--tizen-package", options.tizenPackage,
		"--tv-runtime", options.tvRuntime,
		"--windows-executable", options.windowsExecutable,
		"--channel", options.channel,
		"--tag-name", options.tagName,
		"--published-at", options.publishedAt,
		"--release-url", options.releaseURL,
		"--apk-url", options.apkURL,
		"--ios-archive-url", options.iosArchiveURL,
		"--tvos-archive-url", options.tvosArchiveURL,
		"--visionos-archive-url", options.visionosArchiveURL,
		"--macos-disk-image-url", options.macosDiskImageURL,
		"--webos-package-url", options.webosPackageURL,
		"--tizen-package-url", options.tizenPackageURL,
		"--tv-runtime-url", options.tvRuntimeURL,
		"--application-id", options.applicationID,
		"--build-version", options.buildVersion,
		"--signing-certificate-sha256", options.signingCertificateSHA256,
		"--windows-executable-url", options.windowsExecutableURL,
		options.output,
	}
	for _, flagName := range []string{"--ios-archive", "--ios-archive-url", "--tvos-archive", "--tvos-archive-url", "--visionos-archive", "--visionos-archive-url", "--macos-disk-image", "--macos-disk-image-url", "--webos-package", "--webos-package-url", "--tizen-package", "--tizen-package-url", "--tv-runtime", "--tv-runtime-url", "--windows-executable", "--windows-executable-url"} {
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
	if _, err := decodeManifest([]byte(`{"schemaVersion":3,"packages":{"android":{},"android":{}}}`)); err == nil {
		t.Fatal("duplicate known package key was accepted")
	}
	if _, err := decodeManifest([]byte(`{"schemaVersion":3,"packages":{"webos":{},"webos":{}}}`)); err == nil {
		t.Fatal("duplicate webOS package key was accepted")
	}
	if _, err := decodeManifest([]byte(`{"schemaVersion":3,"packages":{"windows":{"url":"a","url":"b"}}}`)); err == nil {
		t.Fatal("duplicate Windows package field was accepted")
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
