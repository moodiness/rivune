package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	schemaVersion          = 2
	androidApplicationID   = "io.rivune.app"
	githubReleaseURLPrefix = "https://github.com/moodiness/rivune/releases"
	androidAssetFileName   = "Rivune-Android.apk"
	iosAssetFileName       = "Rivune-iOS-unsigned.ipa"
	tvosAssetFileName      = "Rivune-tvOS-unsigned.ipa"
	visionosAssetFileName  = "Rivune-visionOS-unsigned.ipa"
	macosAssetFileName     = "Rivune-macOS.dmg"
	maxAndroidPackageSize  = int64(512 * 1024 * 1024)
	maxApplePackageSize    = int64(512 * 1024 * 1024)
	maxWindowsPackageSize  = int64(2*1024*1024*1024 - 1)
)

var (
	semverPattern       = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
	sha256Pattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	rfc3339Pattern      = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$`)
	decimalPattern      = regexp.MustCompile(`^[1-9][0-9]*$`)
	safeFileNamePattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]*$`)
)

type generateOptions struct {
	apk                       string
	iosArchive                string
	tvosArchive               string
	visionosArchive           string
	macosDiskImage            string
	windowsX64Executable      string
	windowsArm64Executable    string
	output                    string
	channel                   string
	tagName                   string
	publishedAt               string
	releaseURL                string
	apkURL                    string
	iosArchiveURL             string
	tvosArchiveURL            string
	visionosArchiveURL        string
	macosDiskImageURL         string
	applicationID             string
	buildVersion              string
	signingCertificateSHA256  string
	windowsX64ExecutableURL   string
	windowsArm64ExecutableURL string
}

type validateOptions struct {
	apk                       string
	iosArchive                string
	tvosArchive               string
	visionosArchive           string
	macosDiskImage            string
	windowsX64Executable      string
	windowsArm64Executable    string
	channel                   string
	tagName                   string
	publishedAt               string
	releaseURL                string
	apkURL                    string
	iosArchiveURL             string
	tvosArchiveURL            string
	visionosArchiveURL        string
	macosDiskImageURL         string
	applicationID             string
	buildVersion              string
	signingCertificateSHA256  string
	windowsX64ExecutableURL   string
	windowsArm64ExecutableURL string
}

func buildManifest(options generateOptions) (map[string]any, error) {
	apkSize, apkDigest, err := assetMetadata(options.apk, "APK", maxAndroidPackageSize)
	if err != nil {
		return nil, err
	}
	iosSize, iosDigest, err := assetMetadata(options.iosArchive, "iOS archive", maxApplePackageSize)
	if err != nil {
		return nil, err
	}
	tvosSize, tvosDigest, err := assetMetadata(options.tvosArchive, "tvOS archive", maxApplePackageSize)
	if err != nil {
		return nil, err
	}
	visionosSize, visionosDigest, err := assetMetadata(options.visionosArchive, "visionOS archive", maxApplePackageSize)
	if err != nil {
		return nil, err
	}
	macosSize, macosDigest, err := assetMetadata(options.macosDiskImage, "macOS disk image", maxApplePackageSize)
	if err != nil {
		return nil, err
	}
	windowsX64Size, windowsX64Digest, err := assetMetadata(options.windowsX64Executable, "Windows x64 executable", maxWindowsPackageSize)
	if err != nil {
		return nil, err
	}
	windowsArm64Size, windowsArm64Digest, err := assetMetadata(options.windowsArm64Executable, "Windows ARM64 executable", maxWindowsPackageSize)
	if err != nil {
		return nil, err
	}
	manifest := map[string]any{
		"schemaVersion": schemaVersion,
		"channel":       options.channel,
		"version":       strings.TrimPrefix(options.tagName, "v"),
		"tagName":       options.tagName,
		"publishedAt":   options.publishedAt,
		"releaseUrl":    options.releaseURL,
		"packages": map[string]any{
			"android": map[string]any{
				"format":                   "apk",
				"architectures":            []string{"universal"},
				"minimumOsVersion":         "8.0",
				"applicationId":            options.applicationID,
				"buildVersion":             options.buildVersion,
				"signature":                "signed",
				"signingCertificateSha256": options.signingCertificateSHA256,
				"fileName":                 filepath.Base(options.apk),
				"url":                      options.apkURL,
				"size":                     apkSize,
				"sha256":                   apkDigest,
			},
			"ios":      applePackage("ipa", []string{"arm64"}, "15.0", "io.rivune.app", options.iosArchive, options.iosArchiveURL, iosSize, iosDigest),
			"tvos":     applePackage("ipa", []string{"arm64"}, "15.0", "io.rivune.app.tv", options.tvosArchive, options.tvosArchiveURL, tvosSize, tvosDigest),
			"visionos": applePackage("ipa", []string{"arm64"}, "1.0", "io.rivune.app.vision", options.visionosArchive, options.visionosArchiveURL, visionosSize, visionosDigest),
			"macos":    applePackage("dmg", []string{"arm64", "x64"}, "12.0", "io.rivune.app.mac", options.macosDiskImage, options.macosDiskImageURL, macosSize, macosDigest),
			"windowsX64": map[string]any{
				"format":           "exe",
				"architectures":    []string{"x64"},
				"minimumOsVersion": "10.0.19041.0",
				"signature":        "unsigned",
				"fileName":         filepath.Base(options.windowsX64Executable),
				"url":              options.windowsX64ExecutableURL,
				"size":             windowsX64Size,
				"sha256":           windowsX64Digest,
			},
			"windowsArm64": map[string]any{
				"format":           "exe",
				"architectures":    []string{"arm64"},
				"minimumOsVersion": "10.0.19041.0",
				"signature":        "unsigned",
				"fileName":         filepath.Base(options.windowsArm64Executable),
				"url":              options.windowsArm64ExecutableURL,
				"size":             windowsArm64Size,
				"sha256":           windowsArm64Digest,
			},
		},
	}
	if err := validateManifest(manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func applePackage(format string, architectures []string, minimumOSVersion, bundleIdentifier, path, url string, size int64, digest string) map[string]any {
	return map[string]any{
		"format":           format,
		"architectures":    architectures,
		"minimumOsVersion": minimumOSVersion,
		"bundleIdentifier": bundleIdentifier,
		"signature":        "unsigned",
		"fileName":         filepath.Base(path),
		"url":              url,
		"size":             size,
		"sha256":           digest,
	}
}

func validateManifestFile(manifestPath string, options validateOptions) error {
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return err
	}
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if err := validateExpectedValues(manifest, options); err != nil {
		return err
	}
	return nil
}

func readManifest(manifestPath string) (any, error) {
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	return decodeManifest(contents)
}

func decodeManifest(contents []byte) (any, error) {
	duplicateDecoder := json.NewDecoder(bytes.NewReader(contents))
	duplicateDecoder.UseNumber()
	if err := consumeUniqueJSONValue(duplicateDecoder, "manifest"); err != nil {
		return nil, err
	}
	if token, err := duplicateDecoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("manifest must contain exactly one JSON document (unexpected token %v)", token)
		}
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.UseNumber()
	var manifest any
	if err := decoder.Decode(&manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func consumeUniqueJSONValue(decoder *json.Decoder, context string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("%s contains a non-string object key", context)
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("%s contains duplicate key %q", context, key)
			}
			seen[key] = struct{}{}
			if err := consumeUniqueJSONValue(decoder, context+"."+key); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := consumeUniqueJSONValue(decoder, fmt.Sprintf("%s[%d]", context, index)); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func validateManifest(manifest any) error {
	root, err := object(manifest, "manifest")
	if err != nil {
		return err
	}
	if err := requireSchema(root, schemaVersion); err != nil {
		return err
	}
	channel, version, tagName, err := validateCommonRoot(root)
	if err != nil {
		return err
	}
	if err := validateChannelVersion(channel, version); err != nil {
		return err
	}
	packagesValue, err := required(root, "packages", "manifest")
	if err != nil {
		return err
	}
	packages, err := object(packagesValue, "manifest.packages")
	if err != nil {
		return err
	}
	androidPackage, err := requiredPackage(packages, "android")
	if err != nil {
		return err
	}
	if err := validateAndroidPackage(androidPackage, tagName, "manifest.packages.android"); err != nil {
		return err
	}
	applePackages := []struct {
		key              string
		format           string
		architectures    []string
		minimumOSVersion string
		bundleIdentifier string
		fileName         string
	}{
		{"ios", "ipa", []string{"arm64"}, "15.0", "io.rivune.app", iosAssetFileName},
		{"tvos", "ipa", []string{"arm64"}, "15.0", "io.rivune.app.tv", tvosAssetFileName},
		{"visionos", "ipa", []string{"arm64"}, "1.0", "io.rivune.app.vision", visionosAssetFileName},
		{"macos", "dmg", []string{"arm64", "x64"}, "12.0", "io.rivune.app.mac", macosAssetFileName},
	}
	for _, specification := range applePackages {
		packageObject, err := requiredPackage(packages, specification.key)
		if err != nil {
			return err
		}
		if err := validateApplePackage(packageObject, tagName, "manifest.packages."+specification.key, specification.format, specification.architectures, specification.minimumOSVersion, specification.bundleIdentifier, specification.fileName); err != nil {
			return err
		}
	}
	windowsX64Package, err := requiredPackage(packages, "windowsX64")
	if err != nil {
		return err
	}
	if err := validateWindowsPackage(windowsX64Package, tagName, "manifest.packages.windowsX64", "x64", "Rivune-x64.exe"); err != nil {
		return err
	}
	windowsArm64Package, err := requiredPackage(packages, "windowsArm64")
	if err != nil {
		return err
	}
	return validateWindowsPackage(windowsArm64Package, tagName, "manifest.packages.windowsArm64", "arm64", "Rivune-arm64.exe")
}

func requiredPackage(packages map[string]any, key string) (map[string]any, error) {
	value, err := required(packages, key, "manifest.packages")
	if err != nil {
		return nil, err
	}
	return object(value, "manifest.packages."+key)
}

func requireSchema(root map[string]any, expected int) error {
	value, err := required(root, "schemaVersion", "manifest")
	if err != nil {
		return err
	}
	if !exactInteger(value, expected) {
		return fmt.Errorf("unsupported schemaVersion: %v", value)
	}
	return nil
}

func validateCommonRoot(root map[string]any) (string, string, string, error) {
	channel, err := requiredString(root, "channel", "manifest")
	if err != nil {
		return "", "", "", err
	}
	if channel != "stable" && channel != "prerelease" {
		return "", "", "", fmt.Errorf("manifest.channel must be stable or prerelease")
	}
	version, err := requiredString(root, "version", "manifest")
	if err != nil {
		return "", "", "", err
	}
	if !semverPattern.MatchString(version) {
		return "", "", "", fmt.Errorf("manifest.version must be SemVer without a v prefix")
	}
	tagName, err := requiredString(root, "tagName", "manifest")
	if err != nil {
		return "", "", "", err
	}
	if tagName != "v"+version {
		return "", "", "", fmt.Errorf("manifest.tagName must equal v followed by manifest.version")
	}
	publishedAt, err := requiredString(root, "publishedAt", "manifest")
	if err != nil {
		return "", "", "", err
	}
	if !rfc3339Pattern.MatchString(publishedAt) {
		return "", "", "", fmt.Errorf("manifest.publishedAt must be RFC3339")
	}
	if _, err := time.Parse(time.RFC3339Nano, publishedAt); err != nil {
		return "", "", "", fmt.Errorf("manifest.publishedAt must be a valid RFC3339 timestamp")
	}
	releaseURL, err := requiredString(root, "releaseUrl", "manifest")
	if err != nil {
		return "", "", "", err
	}
	if releaseURL != githubReleaseURLPrefix+"/tag/"+tagName {
		return "", "", "", fmt.Errorf("manifest.releaseUrl must be the exact GitHub release URL for manifest.tagName")
	}
	return channel, version, tagName, nil
}

func validateChannelVersion(channel, version string) error {
	expectedChannel := "stable"
	if strings.Contains(strings.SplitN(version, "+", 2)[0], "-") {
		expectedChannel = "prerelease"
	}
	if channel != expectedChannel {
		return fmt.Errorf("manifest.channel must be %s for version %s", expectedChannel, version)
	}
	return nil
}

func validateAndroidPackage(packageObject map[string]any, tagName, context string) error {
	if err := requireExactString(packageObject, "format", "apk", context); err != nil {
		return err
	}
	if err := requireExactString(packageObject, "signature", "signed", context); err != nil {
		return err
	}
	if err := requireArchitectures(packageObject, []string{"universal"}, context); err != nil {
		return err
	}
	if err := requireExactString(packageObject, "minimumOsVersion", "8.0", context); err != nil {
		return err
	}
	if err := requireExactString(packageObject, "applicationId", androidApplicationID, context); err != nil {
		return err
	}
	buildVersion, err := requiredString(packageObject, "buildVersion", context)
	if err != nil {
		return err
	}
	if !decimalPattern.MatchString(buildVersion) {
		return fmt.Errorf("%s.buildVersion must be a positive decimal string", context)
	}
	if _, err := sha256Field(packageObject, "signingCertificateSha256", context); err != nil {
		return err
	}
	return validateCommonPackageFields(packageObject, context, tagName, androidAssetFileName, maxAndroidPackageSize)
}

func validateApplePackage(packageObject map[string]any, tagName, context, format string, architectures []string, minimumOSVersion, bundleIdentifier, fileName string) error {
	if err := requireExactString(packageObject, "format", format, context); err != nil {
		return err
	}
	if err := requireArchitectures(packageObject, architectures, context); err != nil {
		return err
	}
	if err := requireExactString(packageObject, "minimumOsVersion", minimumOSVersion, context); err != nil {
		return err
	}
	if err := requireExactString(packageObject, "bundleIdentifier", bundleIdentifier, context); err != nil {
		return err
	}
	if err := requireExactString(packageObject, "signature", "unsigned", context); err != nil {
		return err
	}
	return validateCommonPackageFields(packageObject, context, tagName, fileName, maxApplePackageSize)
}

func validateWindowsPackage(packageObject map[string]any, tagName, context, architecture, fileName string) error {
	for _, obsoleteField := range []string{"identityName", "publisher", "packageVersion", "signingCertificateSha256"} {
		if _, present := packageObject[obsoleteField]; present {
			return fmt.Errorf("%s.%s is obsolete for portable executables", context, obsoleteField)
		}
	}
	if err := requireExactString(packageObject, "format", "exe", context); err != nil {
		return err
	}
	if err := requireExactString(packageObject, "signature", "unsigned", context); err != nil {
		return err
	}
	if err := requireArchitectures(packageObject, []string{architecture}, context); err != nil {
		return err
	}
	if err := requireExactString(packageObject, "minimumOsVersion", "10.0.19041.0", context); err != nil {
		return err
	}
	return validateCommonPackageFields(packageObject, context, tagName, fileName, maxWindowsPackageSize)
}

func validateCommonPackageFields(packageObject map[string]any, context, tagName, expectedFileName string, maximumSize int64) error {
	fileName, err := requiredString(packageObject, "fileName", context)
	if err != nil {
		return err
	}
	if !safeFileNamePattern.MatchString(fileName) || fileName != expectedFileName {
		return fmt.Errorf("%s.fileName must be %s", context, expectedFileName)
	}
	packageURL, err := requiredString(packageObject, "url", context)
	if err != nil {
		return err
	}
	expectedURL := githubReleaseURLPrefix + "/download/" + tagName + "/" + fileName
	if packageURL != expectedURL {
		return fmt.Errorf("%s.url must be the exact GitHub release asset URL for manifest.tagName and fileName", context)
	}
	size, err := required(packageObject, "size", context)
	if err != nil {
		return err
	}
	if err := validatePackageSize(size, maximumSize, context+".size"); err != nil {
		return err
	}
	_, err = sha256Field(packageObject, "sha256", context)
	return err
}

func validateExpectedValues(manifest any, options validateOptions) error {
	root := manifest.(map[string]any)
	packages := root["packages"].(map[string]any)
	androidPackage := packages["android"].(map[string]any)
	iosPackage := packages["ios"].(map[string]any)
	tvosPackage := packages["tvos"].(map[string]any)
	visionosPackage := packages["visionos"].(map[string]any)
	macosPackage := packages["macos"].(map[string]any)
	windowsX64Package := packages["windowsX64"].(map[string]any)
	windowsArm64Package := packages["windowsArm64"].(map[string]any)
	expected := []struct {
		actual any
		value  string
		label  string
	}{
		{root["channel"], options.channel, "manifest channel"},
		{root["publishedAt"], options.publishedAt, "manifest publishedAt"},
		{root["tagName"], options.tagName, "manifest tagName"},
		{root["releaseUrl"], options.releaseURL, "manifest releaseUrl"},
		{androidPackage["url"], options.apkURL, "Android package URL"},
		{androidPackage["applicationId"], options.applicationID, "Android applicationId"},
		{androidPackage["buildVersion"], options.buildVersion, "Android buildVersion"},
		{androidPackage["signingCertificateSha256"], options.signingCertificateSHA256, "Android signing certificate"},
		{iosPackage["url"], options.iosArchiveURL, "iOS package URL"},
		{tvosPackage["url"], options.tvosArchiveURL, "tvOS package URL"},
		{visionosPackage["url"], options.visionosArchiveURL, "visionOS package URL"},
		{macosPackage["url"], options.macosDiskImageURL, "macOS package URL"},
		{windowsX64Package["url"], options.windowsX64ExecutableURL, "Windows x64 executable URL"},
		{windowsArm64Package["url"], options.windowsArm64ExecutableURL, "Windows ARM64 executable URL"},
	}
	for _, item := range expected {
		if item.value != "" && item.actual != item.value {
			return fmt.Errorf("%s does not match expected value", item.label)
		}
	}
	assets := []struct {
		packageObject map[string]any
		path          string
		label         string
		maximumSize   int64
	}{
		{androidPackage, options.apk, "APK", maxAndroidPackageSize},
		{iosPackage, options.iosArchive, "iOS archive", maxApplePackageSize},
		{tvosPackage, options.tvosArchive, "tvOS archive", maxApplePackageSize},
		{visionosPackage, options.visionosArchive, "visionOS archive", maxApplePackageSize},
		{macosPackage, options.macosDiskImage, "macOS disk image", maxApplePackageSize},
		{windowsX64Package, options.windowsX64Executable, "Windows x64 executable", maxWindowsPackageSize},
		{windowsArm64Package, options.windowsArm64Executable, "Windows ARM64 executable", maxWindowsPackageSize},
	}
	for _, asset := range assets {
		if err := validateExpectedAsset(asset.packageObject, asset.path, asset.label, asset.maximumSize); err != nil {
			return err
		}
	}
	return nil
}

func validateExpectedAsset(packageObject map[string]any, assetPath, label string, maximumSize int64) error {
	if assetPath == "" {
		return nil
	}
	size, digest, err := assetMetadata(assetPath, label, maximumSize)
	if err != nil {
		return err
	}
	if packageObject["fileName"] != filepath.Base(assetPath) {
		return fmt.Errorf("manifest package fileName does not match %s", label)
	}
	if !exactInteger64(packageObject["size"], size) || packageObject["sha256"] != digest {
		return fmt.Errorf("manifest %s size or SHA-256 does not match the release asset", label)
	}
	return nil
}

func assetMetadata(assetPath, label string, maximumSize int64) (int64, string, error) {
	file, err := os.Open(assetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, "", fmt.Errorf("%s does not exist: %s", label, assetPath)
		}
		return 0, "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, "", err
	}
	if !info.Mode().IsRegular() {
		return 0, "", fmt.Errorf("%s does not exist: %s", label, assetPath)
	}
	if err := validatePackageSize(info.Size(), maximumSize, label); err != nil {
		return 0, "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return 0, "", err
	}
	return info.Size(), hex.EncodeToString(hash.Sum(nil)), nil
}
func validatePackageSize(value any, maximum int64, context string) error {
	size, ok := positiveInteger64(value)
	if !ok {
		return fmt.Errorf("%s must be a positive integer", context)
	}
	if size > maximum {
		return fmt.Errorf("%s must not exceed %d bytes", context, maximum)
	}
	return nil
}

func required(object map[string]any, key, context string) (any, error) {
	value, ok := object[key]
	if !ok {
		return nil, fmt.Errorf("%s.%s is required", context, key)
	}
	return value, nil
}

func requiredString(object map[string]any, key, context string) (string, error) {
	value, err := required(object, key, context)
	if err != nil {
		return "", err
	}
	return nonEmptyString(value, context+"."+key)
}

func object(value any, context string) (map[string]any, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", context)
	}
	return object, nil
}

func nonEmptyString(value any, context string) (string, error) {
	text, ok := value.(string)
	if !ok || text == "" {
		return "", fmt.Errorf("%s must be a non-empty string", context)
	}
	return text, nil
}

func requireExactString(object map[string]any, key, expected, context string) error {
	actual, err := requiredString(object, key, context)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("%s.%s must be %s", context, key, expected)
	}
	return nil
}

func requireArchitectures(object map[string]any, expected []string, context string) error {
	value, err := required(object, "architectures", context)
	if err != nil {
		return err
	}
	var actual []string
	switch architectures := value.(type) {
	case []any:
		actual = make([]string, len(architectures))
		for index, architecture := range architectures {
			text, ok := architecture.(string)
			if !ok {
				return fmt.Errorf("%s.architectures must contain strings", context)
			}
			actual[index] = text
		}
	case []string:
		actual = architectures
	default:
		return fmt.Errorf("%s.architectures must be an array", context)
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("%s.architectures must equal %v", context, expected)
	}
	for index := range expected {
		if actual[index] != expected[index] {
			return fmt.Errorf("%s.architectures must equal %v", context, expected)
		}
	}
	return nil
}

func sha256Field(object map[string]any, key, context string) (string, error) {
	text, err := requiredString(object, key, context)
	if err != nil {
		return "", err
	}
	if !sha256Pattern.MatchString(text) {
		return "", fmt.Errorf("%s.%s must be 64 lowercase hexadecimal characters", context, key)
	}
	return text, nil
}

func positiveInteger64(value any) (int64, bool) {
	switch number := value.(type) {
	case json.Number:
		if !decimalPattern.MatchString(number.String()) {
			return 0, false
		}
		parsed, err := number.Int64()
		return parsed, err == nil && parsed > 0
	case int:
		return int64(number), number > 0
	case int64:
		return number, number > 0
	default:
		return 0, false
	}
}

func exactInteger(value any, expected int) bool {
	switch number := value.(type) {
	case json.Number:
		return number.String() == fmt.Sprint(expected)
	case int:
		return number == expected
	case int64:
		return number == int64(expected)
	default:
		return false
	}
}

func exactInteger64(value any, expected int64) bool {
	switch number := value.(type) {
	case json.Number:
		parsed, err := number.Int64()
		return err == nil && parsed == expected
	case int:
		return int64(number) == expected
	case int64:
		return number == expected
	default:
		return false
	}
}
