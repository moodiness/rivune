package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	Repository                  = "moodiness/rivune"
	WebOSPackageName            = "Rivune-webOS.ipk"
	TizenPackageName            = "Rivune-Tizen.wgt"
	ManifestName                = "rivune-update.json"
	ManifestSignatureName       = ManifestName + ".sig"
	WindowsInstallerName        = "Rivune-TV-Installer-Windows.exe"
	MacOSInstallerName          = "Rivune-TV-Installer-macOS.dmg"
	maximumMetadataBytes  int64 = 512 * 1024
	maximumManifestBytes  int64 = 256 * 1024
	maximumPackageBytes   int64 = 256 * 1024 * 1024
)

var (
	stableTagPattern   = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	hexDigestPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	ExpectedAssetNames = []string{
		"Rivune-Android.apk",
		MacOSInstallerName,
		WindowsInstallerName,
		"Rivune-TV-runtime.json",
		TizenPackageName,
		"Rivune-Windows.exe",
		"Rivune-iOS-unsigned.ipa",
		"Rivune-macOS.dmg",
		"Rivune-tvOS-unsigned.ipa",
		"Rivune-visionOS-unsigned.ipa",
		WebOSPackageName,
		ManifestName,
		ManifestSignatureName,
	}
)

type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type TVPackage struct {
	Asset
	Platform      string `json:"platform"`
	Format        string `json:"format"`
	ApplicationID string `json:"applicationId"`
	PackageID     string `json:"packageId,omitempty"`
}

type Release struct {
	Version     string    `json:"version"`
	TagName     string    `json:"tagName"`
	PublishedAt time.Time `json:"publishedAt"`
	WebOS       TVPackage `json:"webos"`
	Tizen       TVPackage `json:"tizen"`
}

type Client struct {
	HTTP                    *http.Client
	APIEndpoint             string
	ReleasePagePrefix       string
	DownloadPrefix          string
	VerifyManifestSignature func([]byte, []byte) error
}

func NewClient() *Client {
	return &Client{
		HTTP:                    &http.Client{Timeout: 45 * time.Second},
		APIEndpoint:             "https://api.github.com/repos/" + Repository + "/releases/latest",
		ReleasePagePrefix:       "https://github.com/" + Repository + "/releases/tag/",
		DownloadPrefix:          "https://github.com/" + Repository + "/releases/download/",
		VerifyManifestSignature: verifyManifestSignature,
	}
}

type githubAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	State              string `json:"state"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	HTMLURL     string        `json:"html_url"`
	PublishedAt string        `json:"published_at"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	Assets      []githubAsset `json:"assets"`
}

type manifestPackage struct {
	Format        string   `json:"format"`
	Architectures []string `json:"architectures"`
	MinimumOS     string   `json:"minimumOsVersion"`
	ApplicationID string   `json:"applicationId"`
	Signature     string   `json:"signature"`
	FileName      string   `json:"fileName"`
	URL           string   `json:"url"`
	Size          int64    `json:"size"`
	SHA256        string   `json:"sha256"`
}

type updateManifest struct {
	SchemaVersion int                        `json:"schemaVersion"`
	Channel       string                     `json:"channel"`
	Version       string                     `json:"version"`
	TagName       string                     `json:"tagName"`
	PublishedAt   string                     `json:"publishedAt"`
	ReleaseURL    string                     `json:"releaseUrl"`
	Packages      map[string]json.RawMessage `json:"packages"`
}

func (client *Client) Latest(ctx context.Context) (Release, error) {
	if client == nil || client.HTTP == nil {
		return Release{}, errors.New("release client is unavailable")
	}
	metadata, err := client.getBytes(ctx, client.APIEndpoint, "application/vnd.github+json", maximumMetadataBytes)
	if err != nil {
		return Release{}, fmt.Errorf("load latest GitHub release: %w", err)
	}
	var github githubRelease
	if err := json.Unmarshal(metadata, &github); err != nil {
		return Release{}, fmt.Errorf("decode latest GitHub release: %w", err)
	}
	if !stableTagPattern.MatchString(github.TagName) || github.Name != github.TagName || github.Draft || github.Prerelease || github.HTMLURL != client.ReleasePagePrefix+github.TagName {
		return Release{}, errors.New("latest GitHub release identity is invalid")
	}
	publishedAt, err := time.Parse(time.RFC3339Nano, github.PublishedAt)
	if err != nil {
		return Release{}, errors.New("latest GitHub release publication date is invalid")
	}
	assets, err := client.validateAssets(github)
	if err != nil {
		return Release{}, err
	}
	manifestAsset := assets[ManifestName]
	manifestBytes, err := client.verifiedBytes(ctx, manifestAsset, maximumManifestBytes)
	if err != nil {
		return Release{}, fmt.Errorf("load verified update manifest: %w", err)
	}
	signatureBytes, err := client.verifiedBytes(ctx, assets[ManifestSignatureName], maximumManifestSignatureBytes)
	if err != nil {
		return Release{}, fmt.Errorf("load update manifest signature: %w", err)
	}
	verifier := client.VerifyManifestSignature
	if verifier == nil {
		verifier = verifyManifestSignature
	}
	if err := verifier(manifestBytes, signatureBytes); err != nil {
		return Release{}, fmt.Errorf("verify update manifest signature: %w", err)
	}
	var manifest updateManifest
	if err := strictJSON(manifestBytes, &manifest); err != nil {
		return Release{}, fmt.Errorf("decode update manifest: %w", err)
	}
	version := strings.TrimPrefix(github.TagName, "v")
	manifestPublishedAt, publishedAtError := time.Parse(time.RFC3339Nano, manifest.PublishedAt)
	if manifest.SchemaVersion != 3 || manifest.Channel != "stable" || manifest.Version != version || manifest.TagName != github.TagName || publishedAtError != nil || manifestPublishedAt.After(time.Now().Add(5*time.Minute)) || manifest.ReleaseURL != github.HTMLURL {
		return Release{}, errors.New("update manifest release identity is invalid")
	}
	webOS, err := client.parseTVPackage(manifest.Packages["webos"], assets[WebOSPackageName], github.TagName, "webos")
	if err != nil {
		return Release{}, err
	}
	tizen, err := client.parseTVPackage(manifest.Packages["tizen"], assets[TizenPackageName], github.TagName, "tizen")
	if err != nil {
		return Release{}, err
	}
	return Release{Version: version, TagName: github.TagName, PublishedAt: publishedAt, WebOS: webOS, Tizen: tizen}, nil
}

func (client *Client) validateAssets(github githubRelease) (map[string]Asset, error) {
	if len(github.Assets) != len(ExpectedAssetNames) {
		return nil, errors.New("latest GitHub release asset set is incomplete")
	}
	expected := make(map[string]struct{}, len(ExpectedAssetNames))
	for _, name := range ExpectedAssetNames {
		expected[name] = struct{}{}
	}
	assets := make(map[string]Asset, len(github.Assets))
	for _, candidate := range github.Assets {
		if _, ok := expected[candidate.Name]; !ok {
			return nil, fmt.Errorf("unexpected GitHub release asset %q", candidate.Name)
		}
		if _, duplicate := assets[candidate.Name]; duplicate || candidate.ID <= 0 || candidate.State != "uploaded" || candidate.Size <= 0 || candidate.Size > 2_147_483_647 {
			return nil, fmt.Errorf("GitHub release asset %q metadata is invalid", candidate.Name)
		}
		expectedURL := client.DownloadPrefix + github.TagName + "/" + candidate.Name
		digest := strings.TrimPrefix(candidate.Digest, "sha256:")
		if candidate.BrowserDownloadURL != expectedURL || !hexDigestPattern.MatchString(digest) {
			return nil, fmt.Errorf("GitHub release asset %q integrity metadata is invalid", candidate.Name)
		}
		assets[candidate.Name] = Asset{Name: candidate.Name, URL: candidate.BrowserDownloadURL, Size: candidate.Size, SHA256: digest}
	}
	return assets, nil
}

func (client *Client) parseTVPackage(raw json.RawMessage, asset Asset, tagName, platform string) (TVPackage, error) {
	if len(raw) == 0 {
		return TVPackage{}, fmt.Errorf("update manifest has no %s package", platform)
	}
	var value manifestPackage
	if err := strictJSON(raw, &value); err != nil {
		return TVPackage{}, fmt.Errorf("decode update manifest %s package: %w", platform, err)
	}
	expected := manifestPackage{}
	packageID := ""
	switch platform {
	case "webos":
		expected = manifestPackage{Format: "ipk", Architectures: []string{"universal"}, MinimumOS: "4.0", ApplicationID: "io.rivune.app.webos", Signature: "unsigned", FileName: WebOSPackageName}
	case "tizen":
		expected = manifestPackage{Format: "wgt", Architectures: []string{"universal"}, MinimumOS: "5.5", ApplicationID: "RivuneTV01.Rivune", Signature: "unsigned", FileName: TizenPackageName}
		packageID = "RivuneTV01"
	default:
		return TVPackage{}, errors.New("unsupported TV platform")
	}
	expectedURL := client.DownloadPrefix + tagName + "/" + expected.FileName
	if value.Format != expected.Format || len(value.Architectures) != 1 || value.Architectures[0] != "universal" || value.MinimumOS != expected.MinimumOS || value.ApplicationID != expected.ApplicationID || value.Signature != "unsigned" || value.FileName != expected.FileName || value.URL != expectedURL || value.Size != asset.Size || value.SHA256 != asset.SHA256 {
		return TVPackage{}, fmt.Errorf("update manifest %s package does not match the GitHub release asset", platform)
	}
	if value.Size > maximumPackageBytes {
		return TVPackage{}, fmt.Errorf("update manifest %s package is too large", platform)
	}
	return TVPackage{Asset: asset, Platform: platform, Format: value.Format, ApplicationID: value.ApplicationID, PackageID: packageID}, nil
}

func (client *Client) Download(ctx context.Context, pkg TVPackage, destination string) error {
	if pkg.Size <= 0 || pkg.Size > maximumPackageBytes || !hexDigestPattern.MatchString(pkg.SHA256) {
		return errors.New("TV package metadata is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, pkg.URL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/octet-stream")
	response, err := client.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("package download returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > pkg.Size {
		return errors.New("package download is larger than declared")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	temporary := destination + ".part"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, pkg.Size+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written != pkg.Size || hex.EncodeToString(hash.Sum(nil)) != pkg.SHA256 {
		_ = os.Remove(temporary)
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		return errors.New("downloaded TV package does not match its release size and SHA-256")
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (client *Client) verifiedBytes(ctx context.Context, asset Asset, maximum int64) ([]byte, error) {
	if asset.Size > maximum {
		return nil, errors.New("release metadata asset is too large")
	}
	value, err := client.getBytes(ctx, asset.URL, "application/octet-stream", maximum)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(value)
	if int64(len(value)) != asset.Size || hex.EncodeToString(digest[:]) != asset.SHA256 {
		return nil, errors.New("release metadata asset does not match its size and SHA-256")
	}
	return value, nil
}

func (client *Client) getBytes(ctx context.Context, url, accept string, maximum int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("User-Agent", "Rivune-TV-Installer")
	response, err := client.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maximum {
		return nil, errors.New("response is too large")
	}
	value, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(value)) == 0 || int64(len(value)) > maximum {
		return nil, errors.New("response size is invalid")
	}
	return value, nil
}

func strictJSON(value []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("JSON contains trailing data")
	}
	return nil
}
