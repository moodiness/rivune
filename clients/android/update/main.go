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
	flags.StringVar(&options.apk, "apk", "", "path to the signed universal APK")
	flags.StringVar(&options.output, "output", "", "path for the generated manifest")
	flags.StringVar(&options.channel, "channel", "", "release channel: stable or prerelease")
	flags.StringVar(&options.tagName, "tag-name", "", "release tag including the v prefix")
	flags.StringVar(&options.publishedAt, "published-at", "", "RFC3339 release timestamp")
	flags.StringVar(&options.releaseURL, "release-url", "", "HTTPS GitHub release URL")
	flags.StringVar(&options.apkURL, "apk-url", "", "HTTPS APK download URL")
	flags.StringVar(&options.applicationID, "application-id", "", "Android application ID")
	flags.StringVar(&options.buildVersion, "build-version", "", "positive Android versionCode")
	flags.StringVar(&options.signingCertificateSHA256, "signing-certificate-sha256", "", "lowercase signing certificate SHA-256")
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
		{"output", options.output},
		{"channel", options.channel},
		{"tag-name", options.tagName},
		{"published-at", options.publishedAt},
		{"release-url", options.releaseURL},
		{"apk-url", options.apkURL},
		{"application-id", options.applicationID},
		{"build-version", options.buildVersion},
		{"signing-certificate-sha256", options.signingCertificateSHA256},
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
	if err := os.MkdirAll(filepath.Dir(options.output), 0o755); err != nil {
		return err
	}
	file, err := os.Create(options.output)
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
	flags.StringVar(&options.apk, "apk", "", "APK whose size, name, and SHA-256 must match")
	flags.StringVar(&options.channel, "channel", "", "expected release channel")
	flags.StringVar(&options.tagName, "tag-name", "", "expected release tag")
	flags.StringVar(&options.releaseURL, "release-url", "", "expected release URL")
	flags.StringVar(&options.apkURL, "apk-url", "", "expected APK URL")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("validate requires exactly one manifest path")
	}
	return validateManifestFile(flags.Arg(0), options)
}

func printUsage(output *os.File) {
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  go run . generate [options]")
	fmt.Fprintln(output, "  go run . validate [options] <manifest>")
}

func printGenerateUsage(output anyWriter) {
	fmt.Fprintln(output, "Usage: go run . generate --apk <path> --output <path> --channel <channel> --tag-name <tag> --published-at <timestamp> --release-url <url> --apk-url <url> --application-id <id> --build-version <version> --signing-certificate-sha256 <digest>")
}

func printValidateUsage(output anyWriter) {
	fmt.Fprintln(output, "Usage: go run . validate [--apk <path>] [--channel <channel>] [--tag-name <tag>] [--release-url <url>] [--apk-url <url>] <manifest>")
}

type anyWriter interface {
	Write([]byte) (int, error)
}
