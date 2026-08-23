package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLatestBindsTVPackagesToExactReleaseAssets(t *testing.T) {
	fixture := newFixture(t)
	defer fixture.server.Close()
	got, err := fixture.client.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.TagName != "v2.0.0" || got.WebOS.SHA256 != digest(fixture.files[WebOSPackageName]) || got.Tizen.ApplicationID != "RivuneTV01.Rivune" {
		t.Fatalf("unexpected release: %#v", got)
	}
	destination := filepath.Join(t.TempDir(), WebOSPackageName)
	if err := fixture.client.Download(context.Background(), got.WebOS, destination); err != nil {
		t.Fatal(err)
	}
	if value, _ := os.ReadFile(destination); string(value) != string(fixture.files[WebOSPackageName]) {
		t.Fatal("downloaded package differs")
	}
}

func TestLatestRejectsMissingAndTamperedAssets(t *testing.T) {
	fixture := newFixture(t)
	defer fixture.server.Close()
	fixture.release.Assets = fixture.release.Assets[:len(fixture.release.Assets)-1]
	fixture.refresh(t)
	if _, err := fixture.client.Latest(context.Background()); err == nil {
		t.Fatal("missing asset accepted")
	}
	fixture = newFixture(t)
	defer fixture.server.Close()
	fixture.files[ManifestName] = append(fixture.files[ManifestName], ' ')
	if _, err := fixture.client.Latest(context.Background()); err == nil {
		t.Fatal("tampered manifest accepted")
	}
}

type releaseFixture struct {
	server  *httptest.Server
	client  *Client
	files   map[string][]byte
	release githubRelease
}

func newFixture(t *testing.T) *releaseFixture {
	t.Helper()
	fixture := &releaseFixture{files: map[string][]byte{}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/api":
			_ = json.NewEncoder(writer).Encode(fixture.release)
		case len(request.URL.Path) > len("/download/v2.0.0/") && request.URL.Path[:len("/download/v2.0.0/")] == "/download/v2.0.0/":
			name := request.URL.Path[len("/download/v2.0.0/"):]
			value, ok := fixture.files[name]
			if !ok {
				http.NotFound(writer, request)
				return
			}
			writer.Write(value)
		default:
			http.NotFound(writer, request)
		}
	}))
	fixture.server = server
	fixture.client = &Client{HTTP: server.Client(), APIEndpoint: server.URL + "/api", ReleasePagePrefix: server.URL + "/release/", DownloadPrefix: server.URL + "/download/"}
	for index, name := range ExpectedAssetNames {
		fixture.files[name] = []byte(name + " fixture")
		fixture.release.Assets = append(fixture.release.Assets, githubAsset{ID: int64(index + 1), Name: name, State: "uploaded"})
	}
	fixture.release.TagName = "v2.0.0"
	fixture.release.Name = "v2.0.0"
	fixture.release.HTMLURL = server.URL + "/release/v2.0.0"
	fixture.release.PublishedAt = "2026-08-23T12:00:00Z"
	fixture.refresh(t)
	return fixture
}

func (fixture *releaseFixture) refresh(t *testing.T) {
	t.Helper()
	webOS := fixture.packageValue("webos", WebOSPackageName, "ipk", "4.0", "io.rivune.app.webos")
	tizen := fixture.packageValue("tizen", TizenPackageName, "wgt", "5.5", "RivuneTV01.Rivune")
	manifest := map[string]any{"schemaVersion": 2, "channel": "stable", "version": "2.0.0", "tagName": "v2.0.0", "publishedAt": fixture.release.PublishedAt, "releaseUrl": fixture.release.HTMLURL, "packages": map[string]any{"android": map[string]any{}, "ios": map[string]any{}, "tvos": map[string]any{}, "visionos": map[string]any{}, "macos": map[string]any{}, "webos": webOS, "tizen": tizen, "tvRuntime": map[string]any{}, "windowsX64": map[string]any{}, "windowsArm64": map[string]any{}}}
	fixture.files[ManifestName], _ = json.Marshal(manifest)
	for index := range fixture.release.Assets {
		asset := &fixture.release.Assets[index]
		value := fixture.files[asset.Name]
		asset.Size = int64(len(value))
		asset.Digest = "sha256:" + digest(value)
		asset.BrowserDownloadURL = fixture.client.DownloadPrefix + fixture.release.TagName + "/" + asset.Name
	}
}

func (fixture *releaseFixture) packageValue(platform, name, format, minimum, applicationID string) map[string]any {
	value := fixture.files[name]
	return map[string]any{"format": format, "architectures": []string{"universal"}, "minimumOsVersion": minimum, "applicationId": applicationID, "signature": "unsigned", "fileName": name, "url": fixture.client.DownloadPrefix + fixture.release.TagName + "/" + name, "size": int64(len(value)), "sha256": digest(value)}
}

func digest(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
