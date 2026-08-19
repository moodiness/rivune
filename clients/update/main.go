package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
		{"windows-executable", options.windowsExecutable},
		{"output", options.output},
		{"legacy-android-output", options.legacyAndroidOutput},
		{"channel", options.channel},
		{"tag-name", options.tagName},
		{"published-at", options.publishedAt},
		{"release-url", options.releaseURL},
		{"apk-url", options.apkURL},
		{"application-id", options.applicationID},
		{"build-version", options.buildVersion},
		{"signing-certificate-sha256", options.signingCertificateSHA256},
		{"windows-executable-url", options.windowsExecutableURL},
	}
	for _, option := range required {
		if option.value == "" {
			return fmt.Errorf("--%s is required", option.name)
		}
	}
	aliased, err := outputPathsAlias(options.output, options.legacyAndroidOutput)
	if err != nil {
		return err
	}
	if aliased {
		return fmt.Errorf("--output and --legacy-android-output must be different files")
	}
	manifest, err := buildManifest(options)
	if err != nil {
		return err
	}
	if err := writeManifest(options.output, manifest); err != nil {
		return err
	}
	return writeManifest(options.legacyAndroidOutput, buildLegacyAndroidManifest(manifest))
}

func addGenerateFlags(flags *flag.FlagSet, options *generateOptions) {
	flags.StringVar(&options.apk, "apk", "", "path to the signed universal APK")
	flags.StringVar(&options.windowsExecutable, "windows-executable", "", "path to the Windows x64 executable")
	flags.StringVar(&options.output, "output", "", "path for the generated global manifest")
	flags.StringVar(&options.legacyAndroidOutput, "legacy-android-output", "", "path for the generated legacy Android manifest")
	flags.StringVar(&options.channel, "channel", "", "release channel: stable or prerelease")
	flags.StringVar(&options.tagName, "tag-name", "", "release tag including the v prefix")
	flags.StringVar(&options.publishedAt, "published-at", "", "RFC3339 release timestamp")
	flags.StringVar(&options.releaseURL, "release-url", "", "exact HTTPS GitHub release URL")
	flags.StringVar(&options.apkURL, "apk-url", "", "exact HTTPS APK release asset URL")
	flags.StringVar(&options.applicationID, "application-id", "", "Android application ID")
	flags.StringVar(&options.buildVersion, "build-version", "", "positive Android versionCode")
	flags.StringVar(&options.signingCertificateSHA256, "signing-certificate-sha256", "", "lowercase Android signing certificate SHA-256")
	flags.StringVar(&options.windowsExecutableURL, "windows-executable-url", "", "exact HTTPS Windows executable release asset URL")
}

func outputPathsAlias(left, right string) (bool, error) {
	leftPath, err := canonicalOutputPath(left)
	if err != nil {
		return false, err
	}
	rightPath, err := canonicalOutputPath(right)
	if err != nil {
		return false, err
	}
	if runtime.GOOS == "windows" {
		if strings.EqualFold(leftPath, rightPath) {
			return true, nil
		}
	} else if leftPath == rightPath {
		return true, nil
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo) {
		return true, nil
	}
	if leftErr != nil && !os.IsNotExist(leftErr) {
		return false, leftErr
	}
	if rightErr != nil && !os.IsNotExist(rightErr) {
		return false, rightErr
	}
	return false, nil
}

func canonicalOutputPath(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return filepath.Clean(resolved), nil
	}
	ancestor := filepath.Dir(absolute)
	tail := []string{filepath.Base(absolute)}
	for {
		if resolved, err := filepath.EvalSymlinks(ancestor); err == nil {
			parts := append([]string{resolved}, tail...)
			return filepath.Clean(filepath.Join(parts...)), nil
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return absolute, nil
		}
		tail = append([]string{filepath.Base(ancestor)}, tail...)
		ancestor = parent
	}
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
	if options.windowsExecutable == "" {
		return fmt.Errorf("--windows-executable is required")
	}
	if options.windowsExecutableURL == "" {
		return fmt.Errorf("--windows-executable-url is required")
	}
	if options.legacyAndroidManifest == "" {
		return fmt.Errorf("--legacy-android-manifest is required")
	}
	return validateManifestFile(flags.Arg(0), options)
}

func addValidateFlags(flags *flag.FlagSet, options *validateOptions) {
	flags.StringVar(&options.apk, "apk", "", "APK whose file name, size, and SHA-256 must match")
	flags.StringVar(&options.windowsExecutable, "windows-executable", "", "Windows executable whose file name, size, and SHA-256 must match")
	flags.StringVar(&options.legacyAndroidManifest, "legacy-android-manifest", "", "legacy Android manifest that must match the global Android package")
	flags.StringVar(&options.channel, "channel", "", "expected release channel")
	flags.StringVar(&options.tagName, "tag-name", "", "expected release tag")
	flags.StringVar(&options.publishedAt, "published-at", "", "expected RFC3339 publication timestamp")
	flags.StringVar(&options.releaseURL, "release-url", "", "expected release URL")
	flags.StringVar(&options.apkURL, "apk-url", "", "expected APK URL")
	flags.StringVar(&options.applicationID, "application-id", "", "expected Android application ID")
	flags.StringVar(&options.buildVersion, "build-version", "", "expected Android versionCode")
	flags.StringVar(&options.signingCertificateSHA256, "signing-certificate-sha256", "", "expected Android signing certificate SHA-256")
	flags.StringVar(&options.windowsExecutableURL, "windows-executable-url", "", "expected Windows executable URL")
}

func printUsage(output *os.File) {
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  go run . generate [options]")
	fmt.Fprintln(output, "  go run . validate [options] <global-manifest>")
}

func printGenerateUsage(output anyWriter) {
	fmt.Fprintln(output, "Usage: go run . generate --apk <path> --windows-executable <path> --output <path> --legacy-android-output <path> --channel <channel> --tag-name <tag> --published-at <timestamp> --release-url <url> --apk-url <url> --application-id <id> --build-version <version> --signing-certificate-sha256 <digest> --windows-executable-url <url>")
}

func printValidateUsage(output anyWriter) {
	fmt.Fprintln(output, "Usage: go run . validate --apk <path> --windows-executable <path> --windows-executable-url <url> --legacy-android-manifest <path> [expected-value options] <global-manifest>")
}

type anyWriter interface {
	Write([]byte) (int, error)
}
