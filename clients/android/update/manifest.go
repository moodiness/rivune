package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	schemaVersion = 1
	applicationID = "io.rivune.app"
)

var (
	semverPattern  = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
	sha256Pattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	rfc3339Pattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$`)
	decimalPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
)

type generateOptions struct {
	apk                      string
	output                   string
	channel                  string
	tagName                  string
	publishedAt              string
	releaseURL               string
	apkURL                   string
	applicationID            string
	buildVersion             string
	signingCertificateSHA256 string
}

type validateOptions struct {
	apk        string
	channel    string
	tagName    string
	releaseURL string
	apkURL     string
}

func buildManifest(options generateOptions) (map[string]any, error) {
	size, digest, err := apkMetadata(options.apk)
	if err != nil {
		return nil, err
	}
	fileName := filepath.Base(options.apk)
	manifest := map[string]any{
		"schemaVersion": schemaVersion,
		"channel":       options.channel,
		"version":       strings.TrimPrefix(options.tagName, "v"),
		"tagName":       options.tagName,
		"publishedAt":   options.publishedAt,
		"releaseUrl":    options.releaseURL,
		"package": map[string]any{
			"format":                   "apk",
			"architectures":            []string{"universal"},
			"applicationId":            options.applicationID,
			"buildVersion":             options.buildVersion,
			"minimumOsVersion":         "8.0",
			"fileName":                 fileName,
			"url":                      options.apkURL,
			"size":                     size,
			"sha256":                   digest,
			"signingCertificateSha256": options.signingCertificateSHA256,
		},
	}
	if err := validateManifest(manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func validateManifestFile(manifestPath string, options validateOptions) error {
	file, err := os.Open(manifestPath)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.UseNumber()
	var manifest any
	if err := decoder.Decode(&manifest); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("manifest must contain exactly one JSON document")
		}
		return err
	}
	if err := validateManifest(manifest); err != nil {
		return err
	}
	return validateExpectedValues(manifest, options)
}

func validateManifest(manifest any) error {
	root, err := object(manifest, "manifest")
	if err != nil {
		return err
	}

	value, err := required(root, "schemaVersion", "manifest")
	if err != nil {
		return err
	}
	if !exactInteger(value, schemaVersion) {
		return fmt.Errorf("unsupported schemaVersion: %v", value)
	}

	channelValue, err := required(root, "channel", "manifest")
	if err != nil {
		return err
	}
	channel, err := nonEmptyString(channelValue, "manifest.channel")
	if err != nil {
		return err
	}
	if channel != "stable" && channel != "prerelease" {
		return fmt.Errorf("manifest.channel must be stable or prerelease")
	}

	versionValue, err := required(root, "version", "manifest")
	if err != nil {
		return err
	}
	version, err := nonEmptyString(versionValue, "manifest.version")
	if err != nil {
		return err
	}
	if !semverPattern.MatchString(version) {
		return fmt.Errorf("manifest.version must be SemVer without a v prefix")
	}
	versionWithoutBuild := strings.SplitN(version, "+", 2)[0]
	expectedChannel := "stable"
	if strings.Contains(versionWithoutBuild, "-") {
		expectedChannel = "prerelease"
	}
	if channel != expectedChannel {
		return fmt.Errorf("manifest.channel must be %s for version %s", expectedChannel, version)
	}

	tagValue, err := required(root, "tagName", "manifest")
	if err != nil {
		return err
	}
	tagName, err := nonEmptyString(tagValue, "manifest.tagName")
	if err != nil {
		return err
	}
	if tagName != "v"+version {
		return fmt.Errorf("manifest.tagName must equal v followed by manifest.version")
	}

	publishedValue, err := required(root, "publishedAt", "manifest")
	if err != nil {
		return err
	}
	publishedAt, err := nonEmptyString(publishedValue, "manifest.publishedAt")
	if err != nil {
		return err
	}
	if !rfc3339Pattern.MatchString(publishedAt) {
		return fmt.Errorf("manifest.publishedAt must be RFC3339")
	}
	if _, err := time.Parse(time.RFC3339Nano, publishedAt); err != nil {
		return fmt.Errorf("manifest.publishedAt must be a valid RFC3339 timestamp")
	}

	releaseValue, err := required(root, "releaseUrl", "manifest")
	if err != nil {
		return err
	}
	if _, err := httpsURL(releaseValue, "manifest.releaseUrl"); err != nil {
		return err
	}

	packageValue, err := required(root, "package", "manifest")
	if err != nil {
		return err
	}
	packageObject, err := object(packageValue, "manifest.package")
	if err != nil {
		return err
	}
	return validatePackage(packageObject)
}

func validatePackage(packageObject map[string]any) error {
	const context = "manifest.package"
	format, err := required(packageObject, "format", context)
	if err != nil {
		return err
	}
	if format != "apk" {
		return fmt.Errorf("%s.format must be apk", context)
	}

	architectures, err := required(packageObject, "architectures", context)
	if err != nil {
		return err
	}
	if !isUniversalArchitecture(architectures) {
		return fmt.Errorf("%s.architectures must equal ['universal']", context)
	}

	packageApplicationID, err := required(packageObject, "applicationId", context)
	if err != nil {
		return err
	}
	if packageApplicationID != applicationID {
		return fmt.Errorf("%s.applicationId must be %s", context, applicationID)
	}

	buildValue, err := required(packageObject, "buildVersion", context)
	if err != nil {
		return err
	}
	buildVersion, err := nonEmptyString(buildValue, context+".buildVersion")
	if err != nil {
		return err
	}
	if !decimalPattern.MatchString(buildVersion) {
		return fmt.Errorf("%s.buildVersion must be a positive decimal string", context)
	}

	minimumOSVersion, err := required(packageObject, "minimumOsVersion", context)
	if err != nil {
		return err
	}
	if minimumOSVersion != "8.0" {
		return fmt.Errorf("%s.minimumOsVersion must be 8.0", context)
	}

	fileNameValue, err := required(packageObject, "fileName", context)
	if err != nil {
		return err
	}
	fileName, err := nonEmptyString(fileNameValue, context+".fileName")
	if err != nil {
		return err
	}
	if path.Base(fileName) != fileName {
		return fmt.Errorf("%s.fileName must not contain a path", context)
	}

	packageURLValue, err := required(packageObject, "url", context)
	if err != nil {
		return err
	}
	packageURL, err := httpsURL(packageURLValue, context+".url")
	if err != nil {
		return err
	}
	if path.Base(packageURL.EscapedPath()) != fileName {
		return fmt.Errorf("%s.url must end with fileName", context)
	}

	size, err := required(packageObject, "size", context)
	if err != nil {
		return err
	}
	if !positiveInteger(size) {
		return fmt.Errorf("%s.size must be a positive integer", context)
	}

	digest, err := required(packageObject, "sha256", context)
	if err != nil {
		return err
	}
	if _, err := sha256String(digest, context+".sha256"); err != nil {
		return err
	}
	certificate, err := required(packageObject, "signingCertificateSha256", context)
	if err != nil {
		return err
	}
	_, err = sha256String(certificate, context+".signingCertificateSha256")
	return err
}

func validateExpectedValues(manifest any, options validateOptions) error {
	root := manifest.(map[string]any)
	packageObject := root["package"].(map[string]any)
	expected := []struct {
		actual any
		value  string
		label  string
	}{
		{root["channel"], options.channel, "manifest channel"},
		{root["tagName"], options.tagName, "manifest tagName"},
		{root["releaseUrl"], options.releaseURL, "manifest releaseUrl"},
		{packageObject["url"], options.apkURL, "manifest package URL"},
	}
	for _, item := range expected {
		if item.value != "" && item.actual != item.value {
			return fmt.Errorf("%s does not match expected value", item.label)
		}
	}
	if options.apk == "" {
		return nil
	}
	size, digest, err := apkMetadata(options.apk)
	if err != nil {
		return err
	}
	if packageObject["fileName"] != filepath.Base(options.apk) {
		return fmt.Errorf("manifest package fileName does not match APK")
	}
	if !exactInteger64(packageObject["size"], size) || packageObject["sha256"] != digest {
		return fmt.Errorf("manifest APK size or SHA-256 does not match the release asset")
	}
	return nil
}

func apkMetadata(apkPath string) (int64, string, error) {
	file, err := os.Open(apkPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, "", fmt.Errorf("APK does not exist: %s", apkPath)
		}
		return 0, "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, "", err
	}
	if !info.Mode().IsRegular() {
		return 0, "", fmt.Errorf("APK does not exist: %s", apkPath)
	}
	if info.Size() <= 0 {
		return 0, "", fmt.Errorf("APK must not be empty")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return 0, "", err
	}
	return info.Size(), hex.EncodeToString(hash.Sum(nil)), nil
}

func required(object map[string]any, key, context string) (any, error) {
	value, ok := object[key]
	if !ok {
		return nil, fmt.Errorf("%s.%s is required", context, key)
	}
	return value, nil
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

func httpsURL(value any, context string) (*url.URL, error) {
	text, err := nonEmptyString(value, context)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(text)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("%s must be an HTTPS URL without credentials", context)
	}
	return parsed, nil
}

func sha256String(value any, context string) (string, error) {
	text, err := nonEmptyString(value, context)
	if err != nil {
		return "", err
	}
	if !sha256Pattern.MatchString(text) {
		return "", fmt.Errorf("%s must be 64 lowercase hexadecimal characters", context)
	}
	return text, nil
}

func isUniversalArchitecture(value any) bool {
	switch architectures := value.(type) {
	case []any:
		return len(architectures) == 1 && architectures[0] == "universal"
	case []string:
		return len(architectures) == 1 && architectures[0] == "universal"
	default:
		return false
	}
}

func positiveInteger(value any) bool {
	switch number := value.(type) {
	case json.Number:
		return decimalPattern.MatchString(number.String())
	case int:
		return number > 0
	case int64:
		return number > 0
	default:
		return false
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
