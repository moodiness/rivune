package install

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/moodiness/rivune/clients/tv-installer/internal/release"
)

const (
	webOSApplicationID = "io.rivune.app.webos"
	tizenApplicationID = "RivuneTV01.Rivune"
	tizenPackageID     = "RivuneTV01"
)

var safeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type Request struct {
	Platform   string `json:"platform"`
	Action     string `json:"action"`
	IP         string `json:"ip"`
	DeviceName string `json:"deviceName"`
	Passphrase string `json:"passphrase"`
	Profile    string `json:"profile"`
}

type ToolStatus struct {
	WebOS bool `json:"webos"`
	Tizen bool `json:"tizen"`
}

type PackageSource interface {
	Latest(context.Context) (release.Release, error)
	Download(context.Context, release.TVPackage, string) error
}

type Runner interface {
	Run(context.Context, string, []string, map[int]struct{}) (string, error)
}

type Service struct {
	Source   PackageSource
	Runner   Runner
	Home     string
	UserHome string
	Temp     string
}

type ExecRunner struct{ Log func(string) }

func (runner ExecRunner) Run(ctx context.Context, command string, arguments []string, secrets map[int]struct{}) (string, error) {
	display := make([]string, len(arguments))
	copy(display, arguments)
	secretValues := make([]string, 0, len(secrets))
	for index := range secrets {
		if index >= 0 && index < len(display) {
			secretValues = append(secretValues, display[index])
			display[index] = "********"
		}
	}
	if runner.Log != nil {
		runner.Log("$ " + command + " " + strings.Join(display, " "))
	}
	process := exec.CommandContext(ctx, command, arguments...)
	output, err := process.CombinedOutput()
	text := strings.TrimSpace(string(output))
	for _, secret := range secretValues {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "********")
		}
	}
	if text != "" && runner.Log != nil {
		runner.Log(text)
	}
	if err != nil {
		return text, fmt.Errorf("%s failed: %w", filepath.Base(command), err)
	}
	return text, nil
}

func (service *Service) Status() ToolStatus {
	return ToolStatus{
		WebOS: service.tool("ares-setup-device") != "" && service.tool("ares-novacom") != "" && service.tool("ares-install") != "" && service.tool("ares-launch") != "",
		Tizen: service.tool("sdb") != "" && service.tool("tizen") != "",
	}
}

func (service *Service) Execute(ctx context.Context, request Request, log func(string)) error {
	if service.Source == nil {
		return errors.New("release source is unavailable")
	}
	active := *service
	if active.Runner == nil {
		active.Runner = ExecRunner{Log: log}
	}
	service = &active
	ip, err := privateIPv4(request.IP)
	if err != nil {
		return err
	}
	request.IP = ip
	switch request.Action {
	case "install", "launch", "uninstall":
	default:
		return errors.New("unsupported installer action")
	}
	switch request.Platform {
	case "webos":
		return service.webOS(ctx, request, log)
	case "tizen":
		return service.tizen(ctx, request, log)
	default:
		return errors.New("unsupported TV platform")
	}
}

func (service *Service) webOS(ctx context.Context, request Request, log func(string)) error {
	setup, novacom, installTool, launch := service.tool("ares-setup-device"), service.tool("ares-novacom"), service.tool("ares-install"), service.tool("ares-launch")
	if setup == "" || novacom == "" || installTool == "" || launch == "" {
		return errors.New("webOS CLI tools are unavailable; install @webos-tools/cli and restart the companion")
	}
	deviceName := strings.TrimSpace(request.DeviceName)
	if deviceName == "" {
		deviceName = "rivune-lg-" + strings.ReplaceAll(request.IP, ".", "-")
	}
	if !safeNamePattern.MatchString(deviceName) {
		return errors.New("LG device name is invalid")
	}
	if request.Passphrase != "" {
		setupAction := "--add"
		for _, device := range service.webOSDevices() {
			if device["name"] == deviceName {
				setupAction = "--modify"
				break
			}
		}
		arguments := []string{setupAction, deviceName, "--info", "username=prisoner", "--info", "host=" + request.IP, "--info", "port=9922", "--info", "default=true"}
		if _, err := service.Runner.Run(ctx, setup, arguments, nil); err != nil {
			return fmt.Errorf("configure LG device: %w", err)
		}
		keyArgs := []string{"--device", deviceName, "--getkey", "--passphrase", request.Passphrase}
		if _, err := service.Runner.Run(ctx, novacom, keyArgs, map[int]struct{}{4: {}}); err != nil {
			return fmt.Errorf("retrieve LG developer key: %w", err)
		}
	}
	switch request.Action {
	case "launch":
		_, err := service.Runner.Run(ctx, launch, []string{"--device", deviceName, webOSApplicationID}, nil)
		return err
	case "uninstall":
		_, err := service.Runner.Run(ctx, installTool, []string{"--device", deviceName, "--remove", webOSApplicationID}, nil)
		return err
	}
	latest, err := service.Source.Latest(ctx)
	if err != nil {
		return err
	}
	path, cleanup, err := service.download(ctx, latest.WebOS)
	if err != nil {
		return err
	}
	defer cleanup()
	if log != nil {
		log("Verified " + latest.WebOS.Name + " for " + latest.TagName + ".")
	}
	_, err = service.Runner.Run(ctx, installTool, []string{"--device", deviceName, path}, nil)
	return err
}

func (service *Service) tizen(ctx context.Context, request Request, log func(string)) error {
	sdb, tizen := service.tool("sdb"), service.tool("tizen")
	if sdb == "" || tizen == "" {
		return errors.New("Samsung Tizen Studio tools are unavailable; install Tizen Studio and restart the companion")
	}
	if _, err := service.Runner.Run(ctx, sdb, []string{"connect", request.IP}, nil); err != nil {
		return fmt.Errorf("connect Samsung TV: %w", err)
	}
	devices, err := service.Runner.Run(ctx, sdb, []string{"devices"}, nil)
	if err != nil {
		return fmt.Errorf("list Samsung TVs: %w", err)
	}
	target := sdbTarget(devices, request.IP)
	if target == "" {
		return errors.New("Samsung TV is not available through sdb; verify Developer Mode and Host PC IP")
	}
	switch request.Action {
	case "launch":
		_, err := service.Runner.Run(ctx, tizen, []string{"run", "-p", tizenApplicationID, "-t", target}, nil)
		return err
	case "uninstall":
		_, err := service.Runner.Run(ctx, tizen, []string{"uninstall", "-p", tizenPackageID, "-t", target}, nil)
		return err
	}
	profile := strings.TrimSpace(request.Profile)
	if !safeNamePattern.MatchString(profile) {
		return errors.New("a valid Tizen Studio security profile is required")
	}
	latest, err := service.Source.Latest(ctx)
	if err != nil {
		return err
	}
	path, cleanup, err := service.download(ctx, latest.Tizen)
	if err != nil {
		return err
	}
	defer cleanup()
	if log != nil {
		log("Verified " + latest.Tizen.Name + " for " + latest.TagName + ".")
	}
	stage, output, cleanupStage, err := prepareTizen(path, latest.Version)
	if err != nil {
		return err
	}
	defer cleanupStage()
	if _, err := service.Runner.Run(ctx, tizen, []string{"package", "-t", "wgt", "-s", profile, "-o", output, "--", stage}, nil); err != nil {
		return fmt.Errorf("sign Tizen package: %w", err)
	}
	signed, err := singleWGT(output)
	if err != nil {
		return err
	}
	if _, err := service.Runner.Run(ctx, tizen, []string{"install", "-n", signed, "-t", target}, nil); err != nil {
		return fmt.Errorf("install Tizen package: %w", err)
	}
	return nil
}

func (service *Service) download(ctx context.Context, pkg release.TVPackage) (string, func(), error) {
	root := service.Temp
	if root == "" {
		root = os.TempDir()
	}
	directory, err := os.MkdirTemp(root, "rivune-tv-installer-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	path := filepath.Join(directory, pkg.Name)
	if err := service.Source.Download(ctx, pkg, path); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

func (service *Service) tool(name string) string {
	if service.Home != "" {
		candidate := filepath.Join(service.Home, name)
		if runtime.GOOS == "windows" {
			candidate += ".bat"
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if found, err := exec.LookPath(name); err == nil {
		return found
	}
	home, _ := os.UserHomeDir()
	roots := []string{filepath.Join(home, "tizen-studio"), "/opt/tizen-studio"}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		roots = append(roots, filepath.Join(local, "tizen-studio"))
	}
	for _, root := range roots {
		subdirectory := "tools"
		if name == "tizen" {
			subdirectory = filepath.Join("tools", "ide", "bin")
		}
		candidate := filepath.Join(root, subdirectory, name)
		for _, suffix := range []string{"", ".bat", ".exe"} {
			if info, err := os.Stat(candidate + suffix); err == nil && !info.IsDir() {
				return candidate + suffix
			}
		}
	}
	return ""
}

func privateIPv4(value string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil || ip.To4() == nil || !ip.IsPrivate() {
		return "", errors.New("TV address must be a private IPv4 address")
	}
	return ip.To4().String(), nil
}

func sdbTarget(output, ip string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.Contains(fields[0], ip) && fields[1] == "device" {
			return fields[0]
		}
	}
	return ""
}

func prepareTizen(packagePath, expectedVersion string) (string, string, func(), error) {
	archive, err := zip.OpenReader(packagePath)
	if err != nil {
		return "", "", func() {}, errors.New("Tizen package is not a valid WGT archive")
	}
	defer archive.Close()
	root, err := os.MkdirTemp("", "rivune-tizen-sign-")
	if err != nil {
		return "", "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	stage, output := filepath.Join(root, "stage"), filepath.Join(root, "signed")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	var config []byte
	seenPaths := make(map[string]struct{}, len(archive.File))
	var totalSize uint64
	for _, file := range archive.File {
		clean := filepath.Clean(filepath.FromSlash(file.Name))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || file.Mode()&os.ModeSymlink != 0 {
			cleanup()
			return "", "", func() {}, errors.New("Tizen package contains an unsafe path")
		}
		archivePath := filepath.ToSlash(clean)
		if _, duplicate := seenPaths[archivePath]; duplicate {
			cleanup()
			return "", "", func() {}, errors.New("Tizen package contains a duplicate path")
		}
		seenPaths[archivePath] = struct{}{}
		totalSize += file.UncompressedSize64
		if totalSize > 64*1024*1024 {
			cleanup()
			return "", "", func() {}, errors.New("Tizen package expands beyond the safe limit")
		}
		target := filepath.Join(stage, clean)
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				cleanup()
				return "", "", func() {}, err
			}
			continue
		}
		if file.UncompressedSize64 > 32*1024*1024 {
			cleanup()
			return "", "", func() {}, errors.New("Tizen package member is too large")
		}
		reader, err := file.Open()
		if err != nil {
			cleanup()
			return "", "", func() {}, err
		}
		value, readErr := io.ReadAll(io.LimitReader(reader, 32*1024*1024+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || len(value) > 32*1024*1024 {
			cleanup()
			return "", "", func() {}, errors.New("Tizen package member could not be read safely")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			cleanup()
			return "", "", func() {}, err
		}
		if err := os.WriteFile(target, value, 0o600); err != nil {
			cleanup()
			return "", "", func() {}, err
		}
		if filepath.ToSlash(clean) == "config.xml" {
			config = value
		}
	}
	if err := validateTizenConfig(config, expectedVersion); err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	if err := os.MkdirAll(output, 0o700); err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	return stage, output, cleanup, nil
}

func validateTizenConfig(value []byte, expectedVersion string) error {
	if len(value) == 0 {
		return errors.New("Tizen package has no config.xml")
	}
	decoder := xml.NewDecoder(strings.NewReader(string(value)))
	var widgetVersion, applicationID, packageID string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("Tizen config.xml is invalid")
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == "widget" {
			for _, attribute := range start.Attr {
				if attribute.Name.Local == "version" {
					widgetVersion = attribute.Value
				}
			}
		}
		if start.Name.Local == "application" {
			for _, attribute := range start.Attr {
				if attribute.Name.Local == "id" {
					applicationID = attribute.Value
				}
				if attribute.Name.Local == "package" {
					packageID = attribute.Value
				}
			}
		}
	}
	if widgetVersion != expectedVersion || applicationID != tizenApplicationID || packageID != tizenPackageID {
		return errors.New("Tizen package identity does not match the verified release")
	}
	return nil
}

func singleWGT(root string) (string, error) {
	var packages []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".wgt") {
			packages = append(packages, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(packages)
	if len(packages) != 1 {
		return "", fmt.Errorf("Tizen Studio produced %d signed WGT files; expected one", len(packages))
	}
	return packages[0], nil
}

func LocalIPv4Addresses() []string {
	var result []string
	interfaces, _ := net.Interfaces()
	for _, networkInterface := range interfaces {
		addresses, _ := networkInterface.Addrs()
		for _, address := range addresses {
			var ip net.IP
			switch value := address.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			if ip != nil && ip.To4() != nil && ip.IsPrivate() {
				result = append(result, ip.To4().String())
			}
		}
	}
	sort.Strings(result)
	return result
}
func ReadWebOSDevices() []map[string]string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []map[string]string{}
	}
	return readWebOSDevices(home)
}

func (service *Service) webOSDevices() []map[string]string {
	home := service.UserHome
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return readWebOSDevices(home)
}

func readWebOSDevices(home string) []map[string]string {
	devices := make([]map[string]string, 0)
	value, err := os.ReadFile(filepath.Join(home, ".webos", "tv", "novacom-devices.json"))
	if err != nil {
		return devices
	}
	var entries []map[string]any
	if json.Unmarshal(value, &entries) != nil {
		return devices
	}
	for _, entry := range entries {
		name, _ := entry["name"].(string)
		host, _ := entry["host"].(string)
		if safeNamePattern.MatchString(name) {
			devices = append(devices, map[string]string{"name": name, "host": host})
		}
	}
	return devices
}

func OperationContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 5*time.Minute)
}
