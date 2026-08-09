package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
)

const maxManifestBytes int64 = 16 << 20

type targetFlags []string

func (values *targetFlags) String() string {
	return fmt.Sprint([]string(*values))
}

func (values *targetFlags) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	os.Exit(runCLI(context.Background(), os.Args[1:], os.Stdout, os.Stderr, os.LookupEnv))
}

func runCLI(ctx context.Context, arguments []string, stdout, stderr io.Writer, getenv func(string) (string, bool)) int {
	if len(arguments) == 0 {
		fmt.Fprintln(stderr, "usage: jellyfin-compat validate|run|compare [flags]")
		return 2
	}
	var err error
	switch arguments[0] {
	case "validate":
		err = validateCommand(arguments[1:], stdout)
	case "run":
		err = runCommand(ctx, arguments[1:], stdout, getenv)
	case "compare":
		err = compareCommand(arguments[1:], stdout)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", arguments[0])
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "jellyfin-compat: %s\n", err)
		return 1
	}
	return 0
}

func validateCommand(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	manifestPath := flags.String("manifest", "", "manifest path")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("validate flags: %w", err)
	}
	if flags.NArg() != 0 || *manifestPath == "" {
		return errorsForFlags("validate requires exactly -manifest PATH")
	}
	if _, err := loadManifest(*manifestPath); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "manifest valid")
	return nil
}

func runCommand(ctx context.Context, arguments []string, stdout io.Writer, getenv func(string) (string, bool)) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	manifestPath := flags.String("manifest", "", "manifest path")
	outputPath := flags.String("out", "", "output directory")
	var rawTargets targetFlags
	flags.Var(&rawTargets, "target", "NAME=URL target (exactly two); optional capture seeds use JFCOMPAT_<TARGET>_CAPTURE_<NAME>")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("run flags: %w", err)
	}
	if flags.NArg() != 0 || *manifestPath == "" || *outputPath == "" {
		return errorsForFlags("run requires -manifest PATH, exactly two -target NAME=URL values, and -out DIR")
	}
	manifest, err := loadManifest(*manifestPath)
	if err != nil {
		return err
	}
	targets := make([]targetSpec, len(rawTargets))
	for index, rawTarget := range rawTargets {
		target, err := parseTarget(rawTarget)
		if err != nil {
			return err
		}
		targets[index] = target
	}
	if err := runManifest(ctx, manifest, targets, *outputPath, getenv); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "run complete")
	return nil
}

func compareCommand(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("compare", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	left := flags.String("left", "", "left snapshot directory")
	right := flags.String("right", "", "right snapshot directory")
	outputPath := flags.String("out", "", "diff output directory")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("compare flags: %w", err)
	}
	if flags.NArg() != 0 {
		return errorsForFlags("compare requires -left DIR, -right DIR, and -out DIR")
	}
	summary, err := compareSnapshots(*left, *right, *outputPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "comparison matched: %d compared, %d skipped\n", summary.Matched, len(summary.Skipped))
	return nil
}

func errorsForFlags(message string) error {
	return fmt.Errorf("invalid arguments: %s", message)
}

func loadManifest(path string) (*Manifest, error) {
	data, err := readBoundedFile(path, maxManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	manifest, err := decodeManifest(data)
	if err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}
	return manifest, nil
}
