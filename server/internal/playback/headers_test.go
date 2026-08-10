package playback

import (
	"context"
	"testing"
)

func TestCanonicalStoredRequestHeadersRejectsCaseDuplicates(t *testing.T) {
	if _, ok := canonicalStoredRequestHeaders(map[string]string{
		"Authorization": "Bearer first",
		"authorization": "Bearer second",
	}); ok {
		t.Fatal("case-insensitive duplicate request headers were accepted")
	}
}

func TestCanonicalStoredRequestHeadersNormalizesOneRepresentation(t *testing.T) {
	headers, ok := canonicalStoredRequestHeaders(map[string]string{
		"authorization": "Bearer token",
		"referer":       "https://media.example/",
	})
	if !ok || len(headers) != 2 || headers.Get("Authorization") != "Bearer token" || headers.Get("Referer") != "https://media.example/" {
		t.Fatalf("canonical headers = %#v valid=%t", headers, ok)
	}
	first := mediaProbeKey(storedAsset{URL: "https://media.example/movie.mkv", Headers: map[string]string{"authorization": "Bearer token"}})
	second := mediaProbeKey(storedAsset{URL: "https://media.example/movie.mkv", Headers: map[string]string{"Authorization": "Bearer token"}})
	if first != second {
		t.Fatalf("equivalent HTTP headers produced different probe keys: %s != %s", first, second)
	}
}

func TestFFmpegEgressRejectsLegacyAmbiguousHeaders(t *testing.T) {
	proxy, err := startFFmpegEgressProxy(context.Background(), storedAsset{
		URL: "https://media.example/movie.mkv",
		Headers: map[string]string{
			"Authorization": "Bearer first",
			"authorization": "Bearer second",
		},
	})
	if err == nil {
		if proxy != nil {
			_ = proxy.Close()
		}
		t.Fatal("ambiguous legacy headers started an egress proxy")
	}
}
