package playback

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

type trickplayProcessorStub struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
	jpeg    []byte
}

func (*trickplayProcessorStub) Probe(context.Context, storedAsset) (MediaInspection, error) {
	return MediaInspection{}, nil
}

func (processor *trickplayProcessorStub) GenerateTrickplayJPEG(ctx context.Context, _ storedAsset, _, _ int) ([]byte, error) {
	processor.mu.Lock()
	processor.calls++
	started, release := processor.started, processor.release
	processor.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return processor.jpeg, nil
}

func (processor *trickplayProcessorStub) callCount() int {
	processor.mu.Lock()
	defer processor.mu.Unlock()
	return processor.calls
}

func TestTrickplayCoalescesAuthorizedGenerationAndFailsClosedAcrossProfilesAndExpiry(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	jpegBytes := testTrickplayJPEG(t, 16)
	processor := &trickplayProcessorStub{jpeg: jpegBytes, started: make(chan struct{}, 1), release: make(chan struct{})}
	store := newSourceReferenceStore(func() time.Time { return now })
	profileID := "profile-a"
	grantExpiresAt := now.Add(24 * time.Hour)
	principal := auth.Principal{SessionID: "session-a", UserID: "user-a", DeviceID: "device-a", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt}
	service := &Service{
		processor: processor, references: store, now: func() time.Time { return now },
		mediaOptions:     MediaOptions{MaxStorageBytes: 1 << 20, IdleTTL: time.Minute},
		profileTxFactory: testPlaybackProfileTxFactory,
	}
	asset := storedAsset{ID: "asset-a", URL: filepath.Join(t.TempDir(), "movie.mp4")}
	reference, err := store.put(principal, sourceReference{
		MediaType: "movie", ResourceID: "movie-resource", Source: Source{ID: "source-a", ManifestID: "manifest-a"}, Asset: &asset,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := TrickplayInput{SourceRef: reference.ID, TitleID: "title-a", Width: 16, Index: 0}

	results := make(chan error, 2)
	for range 2 {
		go func() {
			image, err := service.Trickplay(context.Background(), principal, input)
			if err == nil && !bytes.Equal(image.JPEG, jpegBytes) {
				err = errors.New("generated JPEG changed")
			}
			results <- err
		}()
	}
	select {
	case <-processor.started:
	case <-time.After(time.Second):
		t.Fatal("generation did not start")
	}
	cacheKey := trickplayKey(reference, input)
	deadline := time.Now().Add(time.Second)
	for {
		cache := service.trickplayCache()
		cache.mu.Lock()
		entry := cache.entries[cacheKey]
		waiters := 0
		if entry != nil {
			waiters = entry.waiters
		}
		cache.mu.Unlock()
		if waiters == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("duplicate request did not coalesce behind the active generation")
		}
		runtime.Gosched()
	}
	if calls := processor.callCount(); calls != 1 {
		t.Fatalf("concurrent generation calls = %d, want 1", calls)
	}
	close(processor.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("coalesced trickplay: %v", err)
		}
	}

	foreignProfile := "profile-b"
	foreign := principal
	foreign.ActiveProfileID = &foreignProfile
	if _, err := service.Trickplay(context.Background(), foreign, input); !errors.Is(err, ErrSourceReferenceExpired) {
		t.Fatalf("cross-profile trickplay error = %v, want opaque expiry", err)
	}
	now = now.Add(sourceReferenceTTL + time.Second)
	if _, err := service.Trickplay(context.Background(), principal, input); !errors.Is(err, ErrSourceReferenceExpired) {
		t.Fatalf("stale source trickplay error = %v, want opaque expiry", err)
	}

	service.pruneTrickplayImages(true)
	cache := service.trickplayCache()
	cache.mu.Lock()
	entries, cachedBytes := len(cache.entries), cache.bytes
	cache.mu.Unlock()
	if entries != 0 || cachedBytes != 0 {
		t.Fatalf("cache cleanup retained entries=%d bytes=%d", entries, cachedBytes)
	}
}

func TestFFmpegGeneratesDeterministicJPEGTrickplaySheetWithoutWorkspaceArtifacts(t *testing.T) {
	processor, err := NewFFmpegProcessor("ffmpeg", "ffprobe", 1, 1, FFmpegOptions{HardwareAcceleration: "software"})
	if err != nil {
		t.Skipf("FFmpeg is unavailable: %v", err)
	}
	workspace := t.TempDir()
	source := filepath.Join(workspace, "source.mp4")
	createContext, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(createContext, processor.ffmpegPath,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=2",
		"-t", "2", "-c:v", "mpeg4", "-y", source,
	)
	if output, commandErr := command.CombinedOutput(); commandErr != nil {
		t.Skipf("create real media fixture: %v: %s", commandErr, output)
	}
	contents, err := processor.GenerateTrickplayJPEG(context.Background(), storedAsset{ID: "source", URL: source}, 16, 0)
	if err != nil {
		t.Fatalf("generate trickplay sheet: %v", err)
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(contents))
	if err != nil || config.Width != 16*trickplayColumns || config.Height != TrickplayThumbnailHeight(16)*trickplayRows {
		t.Fatalf("invalid deterministic JPEG geometry: config=%+v err=%v", config, err)
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(source) {
		t.Fatalf("trickplay generation retained workspace artifacts: %v", entries)
	}
}

func testTrickplayJPEG(t *testing.T, tileWidth int) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, tileWidth*trickplayColumns, TrickplayThumbnailHeight(tileWidth)*trickplayRows))
	for y := range canvas.Bounds().Dy() {
		for x := range canvas.Bounds().Dx() {
			canvas.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, canvas, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
