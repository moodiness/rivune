package playback

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestRewritePlaylistAcceptsCardinalityBoundary(t *testing.T) {
	base, err := url.Parse("https://media.example/master.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	var playlist strings.Builder
	for range maximumPlaylistLines - maximumPlaylistReferences {
		playlist.WriteString("#EXT-X-VERSION:7\n")
	}
	for range maximumPlaylistReferences {
		playlist.WriteString("s\n")
	}

	calls := 0
	rewritten, err := rewritePlaylist([]byte(playlist.String()), base, func(string) string {
		calls++
		return "s"
	})
	if err != nil {
		t.Fatalf("rewrite at cardinality boundary: %v", err)
	}
	if calls != maximumPlaylistReferences {
		t.Fatalf("rewrite calls = %d, want %d", calls, maximumPlaylistReferences)
	}
	if len(rewritten) != playlist.Len() {
		t.Fatalf("rewritten length = %d, want %d", len(rewritten), playlist.Len())
	}
}

func TestRewritePlaylistRejectsLineLimitBeforeRewriting(t *testing.T) {
	playlist := strings.Repeat("#\n", maximumPlaylistLines+1)
	calls := 0
	_, err := rewritePlaylist([]byte(playlist), mustPlaylistBaseURL(t), func(string) string {
		calls++
		return "unused"
	})
	if !errors.Is(err, ErrMediaSourceFailed) || !errors.Is(err, errPlaylistTooManyLines) {
		t.Fatalf("excessive line error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("builder called %d times before line limit rejection", calls)
	}
}

func TestRewritePlaylistRejectsSegmentReferenceLimitBeforeRewriting(t *testing.T) {
	playlist := strings.Repeat("s\n", maximumPlaylistReferences+1)
	calls := 0
	_, err := rewritePlaylist([]byte(playlist), mustPlaylistBaseURL(t), func(string) string {
		calls++
		return "unused"
	})
	if !errors.Is(err, ErrMediaSourceFailed) || !errors.Is(err, errPlaylistTooManyReferences) {
		t.Fatalf("excessive segment reference error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("builder called %d times before reference limit rejection", calls)
	}
}

func TestRewritePlaylistRejectsURIAttributeLimitBeforeRewriting(t *testing.T) {
	playlist := "#EXT-X-SESSION-DATA:" + strings.Repeat(`URI="s",`, maximumPlaylistReferences+1) + "\n"
	calls := 0
	_, err := rewritePlaylist([]byte(playlist), mustPlaylistBaseURL(t), func(string) string {
		calls++
		return "unused"
	})
	if !errors.Is(err, ErrMediaSourceFailed) || !errors.Is(err, errPlaylistTooManyReferences) {
		t.Fatalf("excessive URI attribute error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("builder called %d times before URI limit rejection", calls)
	}
}

func TestRewritePlaylistEnforcesOutputBoundary(t *testing.T) {
	base := mustPlaylistBaseURL(t)
	t.Run("exact limit", func(t *testing.T) {
		rewritten, err := rewritePlaylist([]byte("s\n"), base, func(string) string {
			return strings.Repeat("x", maximumRewrittenPlaylistBytes-1)
		})
		if err != nil {
			t.Fatalf("rewrite at output boundary: %v", err)
		}
		if len(rewritten) != maximumRewrittenPlaylistBytes {
			t.Fatalf("rewritten length = %d, want %d", len(rewritten), maximumRewrittenPlaylistBytes)
		}
	})
	t.Run("over limit", func(t *testing.T) {
		_, err := rewritePlaylist([]byte("s\n"), base, func(string) string {
			return strings.Repeat("x", maximumRewrittenPlaylistBytes)
		})
		if !errors.Is(err, ErrMediaSourceFailed) || !errors.Is(err, errPlaylistOutputTooLarge) {
			t.Fatalf("excessive output error = %v", err)
		}
	})
}

func TestRewriteLocalPlaylistRejectsLimitsWithProcessingError(t *testing.T) {
	t.Run("references", func(t *testing.T) {
		playlist := strings.Repeat("s\n", maximumPlaylistReferences+1)
		calls := 0
		_, err := rewriteLocalPlaylist([]byte(playlist), func(string) string {
			calls++
			return "unused"
		})
		if !errors.Is(err, ErrMediaProcessingFailed) || !errors.Is(err, errPlaylistTooManyReferences) {
			t.Fatalf("excessive local references error = %v", err)
		}
		if calls != 0 {
			t.Fatalf("builder called %d times before local reference limit rejection", calls)
		}
	})
	t.Run("output", func(t *testing.T) {
		_, err := rewriteLocalPlaylist([]byte("s\n"), func(string) string {
			return strings.Repeat("x", maximumRewrittenPlaylistBytes)
		})
		if !errors.Is(err, ErrMediaProcessingFailed) || !errors.Is(err, errPlaylistOutputTooLarge) {
			t.Fatalf("excessive local output error = %v", err)
		}
	})
}

func mustPlaylistBaseURL(t *testing.T) *url.URL {
	t.Helper()
	base, err := url.Parse("https://media.example/master.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	return base
}
