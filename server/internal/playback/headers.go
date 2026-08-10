package playback

import (
	"net/http"
	"sort"
	"strings"
)

// canonicalStoredRequestHeaders returns the single HTTP representation used by
// probing, direct proxying, and FFmpeg egress. HTTP field names are
// case-insensitive, so duplicate canonical names are rejected instead of
// allowing map iteration order to choose a credential.
func canonicalStoredRequestHeaders(values map[string]string) (http.Header, bool) {
	if len(values) == 0 {
		return nil, true
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	headers := make(http.Header, len(names))
	for _, originalName := range names {
		value := values[originalName]
		if !validFFmpegStoredHeader(originalName, value) {
			continue
		}
		name := http.CanonicalHeaderKey(strings.TrimSpace(originalName))
		if _, duplicate := headers[name]; duplicate {
			return nil, false
		}
		headers.Set(name, value)
	}
	return headers, true
}
