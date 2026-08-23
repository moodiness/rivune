package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

var errUsage = errors.New("invalid command line")

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		printUsage(os.Stderr)
		return errUsage
	}
	switch arguments[0] {
	case "generate":
		return runGenerate(arguments[1:])
	case "validate":
		return runValidate(arguments[1:])
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return nil
	default:
		printUsage(os.Stderr)
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func runGenerate(arguments []string) error {
	options := generateOptions{}
	flags := flag.NewFlagSet("generate", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() { printGenerateUsage(flags.Output()) }
	addGenerateFlags(flags, &options)
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("generate accepts flags only")
	}
	required := []struct {
		name  string
		value string
	}{
		{"apk", options.apk},
		{"ios-archive", options.iosArchive},
		{"tvos-archive", options.tvosArchive},
		{"visionos-archive", options.visionosArchive},
		{"macos-disk-image", options.macosDiskImage},
		{"webos-package", options.webosPackage},
		{"tizen-package", options.tizenPackage},
		{"tv-runtime", options.tvRuntime},
		{"windows-x64-executable", options.windowsX64Executable},
		{"windows-arm64-executable", options.windowsArm64Executable},
		{"output", options.output},
		{"channel", options.channel},
		{"tag-name", options.tagName},
		{"published-at", options.publishedAt},
		{"release-url", options.releaseURL},
		{"apk-url", options.apkURL},
		{"ios-archive-url", options.iosArchiveURL},
		{"tvos-archive-url", options.tvosArchiveURL},
		{"visionos-archive-url", options.visionosArchiveURL},
		{"macos-disk-image-url", options.macosDiskImageURL},
		{"webos-package-url", options.webosPackageURL},
		{"tizen-package-url", options.tizenPackageURL},
		{"tv-runtime-url", options.tvRuntimeURL},
		{"application-id", options.applicationID},
		{"build-version", options.buildVersion},
		{"signing-certificate-sha256", options.signingCertificateSHA256},
		{"windows-x64-executable-url", options.windowsX64ExecutableURL},
		{"windows-arm64-executable-url", options.windowsArm64ExecutableURL},
	}
	for _, option := range required {
		if option.value == "" {
			return fmt.Errorf("--%s is required", option.name)
		}
	}
	manifest, err := buildManifest(options)
	if err != nil {
		return err
	}
	return writeManifest(options.output, manifest)
}

func addGenerateFlags(flags *flag.FlagSet, options *generateOptions) {
	flags.StringVar(&options.apk, "apk", "", "path to the signed universal APK")
	flags.StringVar(&options.iosArchive, "ios-archive", "", "path to the unsigned iOS IPA")
	flags.StringVar(&options.tvosArchive, "tvos-archive", "", "path to the unsigned tvOS IPA")
	flags.StringVar(&options.visionosArchive, "visionos-archive", "", "path to the unsigned visionOS IPA")
	flags.StringVar(&options.macosDiskImage, "macos-disk-image", "", "path to the unsigned universal macOS disk image")
	flags.StringVar(&options.webosPackage, "webos-package", "", "path to the unsigned universal webOS IPK")
	flags.StringVar(&options.tizenPackage, "tizen-package", "", "path to the unsigned universal Tizen WGT")
	flags.StringVar(&options.tvRuntime, "tv-runtime", "", "path to the shared webOS/Tizen runtime JSON")
	flags.StringVar(&options.windowsX64Executable, "windows-x64-executable", "", "path to the Windows x64 executable")
	flags.StringVar(&options.windowsArm64Executable, "windows-arm64-executable", "", "path to the Windows ARM64 executable")
	flags.StringVar(&options.output, "output", "", "path for the generated global manifest")
	flags.StringVar(&options.channel, "channel", "", "release channel: stable or prerelease")
	flags.StringVar(&options.tagName, "tag-name", "", "release tag including the v prefix")
	flags.StringVar(&options.publishedAt, "published-at", "", "RFC3339 release timestamp")
	flags.StringVar(&options.releaseURL, "release-url", "", "exact HTTPS GitHub release URL")
	flags.StringVar(&options.apkURL, "apk-url", "", "exact HTTPS APK release asset URL")
	flags.StringVar(&options.iosArchiveURL, "ios-archive-url", "", "exact HTTPS iOS release asset URL")
	flags.StringVar(&options.tvosArchiveURL, "tvos-archive-url", "", "exact HTTPS tvOS release asset URL")
	flags.StringVar(&options.visionosArchiveURL, "visionos-archive-url", "", "exact HTTPS visionOS release asset URL")
	flags.StringVar(&options.macosDiskImageURL, "macos-disk-image-url", "", "exact HTTPS macOS release asset URL")
	flags.StringVar(&options.webosPackageURL, "webos-package-url", "", "exact HTTPS webOS release asset URL")
	flags.StringVar(&options.tizenPackageURL, "tizen-package-url", "", "exact HTTPS Tizen release asset URL")
	flags.StringVar(&options.tvRuntimeURL, "tv-runtime-url", "", "exact HTTPS TV runtime release asset URL")
	flags.StringVar(&options.applicationID, "application-id", "", "Android application ID")
	flags.StringVar(&options.buildVersion, "build-version", "", "positive Android versionCode")
	flags.StringVar(&options.signingCertificateSHA256, "signing-certificate-sha256", "", "lowercase Android signing certificate SHA-256")
	flags.StringVar(&options.windowsX64ExecutableURL, "windows-x64-executable-url", "", "exact HTTPS Windows x64 executable release asset URL")
	flags.StringVar(&options.windowsArm64ExecutableURL, "windows-arm64-executable-url", "", "exact HTTPS Windows ARM64 executable release asset URL")
}

func writeManifest(output string, manifest map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	file, err := os.Create(output)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func runValidate(arguments []string) error {
	options := validateOptions{}
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() { printValidateUsage(flags.Output()) }
	addValidateFlags(flags, &options)
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("validate requires exactly one global manifest path")
	}
	if options.apk == "" {
		return fmt.Errorf("--apk is required")
	}
	for _, required := range []struct {
		name  string
		value string
	}{
		{"ios-archive", options.iosArchive},
		{"tvos-archive", options.tvosArchive},
		{"visionos-archive", options.visionosArchive},
		{"macos-disk-image", options.macosDiskImage},
		{"webos-package", options.webosPackage},
		{"tizen-package", options.tizenPackage},
		{"tv-runtime", options.tvRuntime},
		{"ios-archive-url", options.iosArchiveURL},
		{"tvos-archive-url", options.tvosArchiveURL},
		{"visionos-archive-url", options.visionosArchiveURL},
		{"macos-disk-image-url", options.macosDiskImageURL},
		{"webos-package-url", options.webosPackageURL},
		{"tizen-package-url", options.tizenPackageURL},
		{"tv-runtime-url", options.tvRuntimeURL},
	} {
		if required.value == "" {
			return fmt.Errorf("--%s is required", required.name)
		}
	}
	if options.windowsX64Executable == "" {
		return fmt.Errorf("--windows-x64-executable is required")
	}
	if options.windowsArm64Executable == "" {
		return fmt.Errorf("--windows-arm64-executable is required")
	}
	if options.windowsX64ExecutableURL == "" {
		return fmt.Errorf("--windows-x64-executable-url is required")
	}
	if options.windowsArm64ExecutableURL == "" {
		return fmt.Errorf("--windows-arm64-executable-url is required")
	}
	return validateManifestFile(flags.Arg(0), options)
}

func addValidateFlags(flags *flag.FlagSet, options *validateOptions) {
	flags.StringVar(&options.apk, "apk", "", "APK whose file name, size, and SHA-256 must match")
	flags.StringVar(&options.iosArchive, "ios-archive", "", "iOS IPA whose file name, size, and SHA-256 must match")
	flags.StringVar(&options.tvosArchive, "tvos-archive", "", "tvOS IPA whose file name, size, and SHA-256 must match")
	flags.StringVar(&options.visionosArchive, "visionos-archive", "", "visionOS IPA whose file name, size, and SHA-256 must match")
	flags.StringVar(&options.macosDiskImage, "macos-disk-image", "", "macOS disk image whose file name, size, and SHA-256 must match")
	flags.StringVar(&options.webosPackage, "webos-package", "", "webOS IPK whose file name, size, and SHA-256 must match")
	flags.StringVar(&options.tizenPackage, "tizen-package", "", "Tizen WGT whose file name, size, and SHA-256 must match")
	flags.StringVar(&options.tvRuntime, "tv-runtime", "", "TV runtime whose file name, size, and SHA-256 must match")
	flags.StringVar(&options.windowsX64Executable, "windows-x64-executable", "", "Windows x64 executable whose file name, size, and SHA-256 must match")
	flags.StringVar(&options.windowsArm64Executable, "windows-arm64-executable", "", "Windows ARM64 executable whose file name, size, and SHA-256 must match")
	flags.StringVar(&options.channel, "channel", "", "expected release channel")
	flags.StringVar(&options.tagName, "tag-name", "", "expected release tag")
	flags.StringVar(&options.publishedAt, "published-at", "", "expected RFC3339 publication timestamp")
	flags.StringVar(&options.releaseURL, "release-url", "", "expected release URL")
	flags.StringVar(&options.apkURL, "apk-url", "", "expected APK URL")
	flags.StringVar(&options.iosArchiveURL, "ios-archive-url", "", "expected iOS asset URL")
	flags.StringVar(&options.tvosArchiveURL, "tvos-archive-url", "", "expected tvOS asset URL")
	flags.StringVar(&options.visionosArchiveURL, "visionos-archive-url", "", "expected visionOS asset URL")
	flags.StringVar(&options.macosDiskImageURL, "macos-disk-image-url", "", "expected macOS asset URL")
	flags.StringVar(&options.webosPackageURL, "webos-package-url", "", "expected webOS asset URL")
	flags.StringVar(&options.tizenPackageURL, "tizen-package-url", "", "expected Tizen asset URL")
	flags.StringVar(&options.tvRuntimeURL, "tv-runtime-url", "", "expected TV runtime asset URL")
	flags.StringVar(&options.applicationID, "application-id", "", "expected Android application ID")
	flags.StringVar(&options.buildVersion, "build-version", "", "expected Android versionCode")
	flags.StringVar(&options.signingCertificateSHA256, "signing-certificate-sha256", "", "expected Android signing certificate SHA-256")
	flags.StringVar(&options.windowsX64ExecutableURL, "windows-x64-executable-url", "", "expected Windows x64 executable URL")
	flags.StringVar(&options.windowsArm64ExecutableURL, "windows-arm64-executable-url", "", "expected Windows ARM64 executable URL")
}

func printUsage(output *os.File) {
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  go run . generate [options]")
	fmt.Fprintln(output, "  go run . validate [options] <global-manifest>")
}

func printGenerateUsage(output anyWriter) {
	fmt.Fprintln(output, "Usage: go run . generate --apk <path> --ios-archive <path> --tvos-archive <path> --visionos-archive <path> --macos-disk-image <path> --webos-package <path> --tizen-package <path> --tv-runtime <path> --windows-x64-executable <path> --windows-arm64-executable <path> --output <path> --channel <channel> --tag-name <tag> --published-at <timestamp> --release-url <url> --apk-url <url> --ios-archive-url <url> --tvos-archive-url <url> --visionos-archive-url <url> --macos-disk-image-url <url> --webos-package-url <url> --tizen-package-url <url> --tv-runtime-url <url> --application-id <id> --build-version <version> --signing-certificate-sha256 <digest> --windows-x64-executable-url <url> --windows-arm64-executable-url <url>")
}

func printValidateUsage(output anyWriter) {
	fmt.Fprintln(output, "Usage: go run . validate --apk <path> --ios-archive <path> --tvos-archive <path> --visionos-archive <path> --macos-disk-image <path> --webos-package <path> --tizen-package <path> --tv-runtime <path> --windows-x64-executable <path> --windows-arm64-executable <path> --ios-archive-url <url> --tvos-archive-url <url> --visionos-archive-url <url> --macos-disk-image-url <url> --webos-package-url <url> --tizen-package-url <url> --tv-runtime-url <url> --windows-x64-executable-url <url> --windows-arm64-executable-url <url> [expected-value options] <global-manifest>")
}

type anyWriter interface {
	Write([]byte) (int, error)
}
