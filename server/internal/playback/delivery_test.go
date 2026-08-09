package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

func TestDeliverySessionAndHandleSerializationExposeNoNativeSecrets(t *testing.T) {
	const (
		providerURL  = "https://provider.example/private/movie.mkv?provider_token=secret"
		sessionToken = "native-playback-token-secret"
	)
	session := Session{
		ID:               "session-id",
		SelectedSourceID: "stream-1",
		Sources: []Source{{
			ID: "stream-1", Compatible: true,
			URL: providerURL,
		}},
		Subtitles: []Subtitle{{
			ID: "subtitle-1", URL: "/api/v1/playback/sessions/session-id/assets/subtitle-1?token=" + sessionToken,
		}},
	}
	handle := deliveryHandleForSession(session.ID, sessionToken, session.Sources, []storedAsset{{
		ID: "stream-1", URL: providerURL, Headers: map[string]string{"Authorization": "Bearer provider-secret"},
	}})
	if !handle.Valid() {
		t.Fatal("selected playback asset did not produce a delivery handle")
	}
	delivery := Delivery{Session: deliverySafeSession(session), Handle: handle}

	encoded, err := json.Marshal(delivery)
	if err != nil {
		t.Fatal(err)
	}
	standaloneHandle, err := json.Marshal(handle)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{providerURL, sessionToken, "provider-secret", "/api/v1/playback/"} {
		if strings.Contains(string(encoded), secret) || strings.Contains(string(standaloneHandle), secret) || strings.Contains(fmt.Sprintf("%+v", handle), secret) {
			t.Fatalf("delivery serialization exposed %q: delivery=%s handle=%s", secret, encoded, standaloneHandle)
		}
	}
	if string(standaloneHandle) != "{}" {
		t.Fatalf("opaque handle JSON = %s, want {}", standaloneHandle)
	}
	if delivery.Session.Sources[0].URL != "" || delivery.Session.Subtitles[0].URL != "" {
		t.Fatalf("DTO-safe session retained native URLs: %+v", delivery.Session)
	}
	if session.Sources[0].URL != providerURL || session.Subtitles[0].URL == "" {
		t.Fatal("building the DTO-safe session mutated the native Resolve result")
	}
}

func TestDeliveryRequestPreservesHEADRangeAndSanitizesNativeRouting(t *testing.T) {
	handle := DeliveryHandle{
		sessionID: "session-id", assetID: "stream-1", token: "native-token",
		defaultFile: "index.m3u8", defaultStart: "90", children: newDeliveryChildTable(),
	}
	request := httptest.NewRequest(http.MethodHead, "/emby/Videos/item/stream.m3u8?PlaySessionId=compat-play-session&MediaSourceId=compat-media-source&StartTimeTicks=900000000&target=https%3A%2F%2Fcdn.example%2Fsegment.ts&signature=signed&api_key=compat-token", nil)
	request.Header.Set("Range", "bytes=0-1023")

	delivery, err := requestForDelivery(request, handle)
	if err != nil {
		t.Fatal(err)
	}
	if delivery.request.Method != http.MethodHead || delivery.request.Header.Get("Range") != "bytes=0-1023" {
		t.Fatalf("delivery adaptation changed request semantics: method=%s range=%q", delivery.request.Method, delivery.request.Header.Get("Range"))
	}
	query := delivery.request.URL.Query()
	if query.Get("file") != "index.m3u8" || query.Get("start") != "90" {
		t.Fatalf("delivery adaptation omitted HLS defaults: %s", delivery.request.URL.RawQuery)
	}
	if query.Get("target") != "" || query.Get("signature") != "" || query.Get("api_key") != "" {
		t.Fatalf("delivery adaptation trusted adapter routing values: %s", delivery.request.URL.RawQuery)
	}
	childURL := delivery.template.childURL("child-capability", deliveryChildState{assetID: "stream-1", file: "segment.ts"})
	childQuery := httptest.NewRequest(http.MethodGet, childURL, nil).URL.Query()
	if childQuery.Get("StartTimeTicks") != "900000000" || childQuery.Get("api_key") != "compat-play-session" || childQuery.Get("PlaySessionId") != "compat-play-session" ||
		childQuery.Get("MediaSourceId") != "compat-media-source" || childQuery.Get(deliveryChildQueryName) != "child-capability" || strings.Contains(childURL, "compat-token") {
		t.Fatalf("compat child URL selectors are incomplete or exposed the general credential: %s", childURL)
	}
	if got := httptest.NewRequest(http.MethodGet, childURL, nil).URL.Path; got != "/emby/Videos/item/hls1/compat-play-session/child-capability.ts" {
		t.Fatalf("extension-correct child path = %q", got)
	}
	if request.URL.Query().Get("file") != "" || request.URL.Query().Get("start") != "" {
		t.Fatal("delivery adaptation mutated the adapter request")
	}
}

func TestDeliveryChildURLUsesConventionalRouteOnlyForLocalMainPlaylist(t *testing.T) {
	template, err := newDeliveryLinkTemplate(httptest.NewRequest(http.MethodGet, "/emby/Videos/item/stream.m3u8?PlaySessionId=compat-play-session&MediaSourceId=compat-media-source&StartTimeTicks=900000000&api_key=compat-secret", nil).URL)
	if err != nil {
		t.Fatal(err)
	}

	localMain := template.childURL("local-main-capability", deliveryChildState{
		assetID: "stream-1", file: "index.m3u8", start: "90",
	})
	wantLocalMain := "/emby/Videos/item/main.m3u8?MediaSourceId=compat-media-source&PlaySessionId=compat-play-session&RivuneChildId=local-main-capability&StartTimeTicks=900000000&api_key=compat-play-session"
	if localMain != wantLocalMain {
		t.Fatalf("local main URL = %q, want %q", localMain, wantLocalMain)
	}

	for name, test := range map[string]struct {
		childID string
		state   deliveryChildState
		path    string
	}{
		"local segment": {
			childID: "local-segment-capability",
			state:   deliveryChildState{assetID: "stream-1", file: "segment-000001.m4s", start: "90"},
			path:    "/emby/Videos/item/hls1/compat-play-session/local-segment-capability.m4s",
		},
		"upstream playlist": {
			childID: "upstream-playlist-capability",
			state: deliveryChildState{
				assetID: "stream-1", target: "https://provider.example/private/media.m3u8?provider_token=secret", signature: "native-signature",
			},
			path: "/emby/Videos/item/hls1/compat-play-session/upstream-playlist-capability.m3u8",
		},
	} {
		t.Run(name, func(t *testing.T) {
			childURL := template.childURL(test.childID, test.state)
			parsed := httptest.NewRequest(http.MethodGet, childURL, nil).URL
			if parsed.Path != test.path {
				t.Fatalf("child path = %q, want %q", parsed.Path, test.path)
			}
			if parsed.RawQuery != "MediaSourceId=compat-media-source&PlaySessionId=compat-play-session&RivuneChildId="+test.childID+"&StartTimeTicks=900000000&api_key=compat-play-session" {
				t.Fatalf("child query = %q", parsed.RawQuery)
			}
			for _, secret := range []string{"compat-secret", "provider.example", "provider_token", "native-signature"} {
				if strings.Contains(childURL, secret) {
					t.Fatalf("child URL exposed %q: %s", secret, childURL)
				}
			}
		})
	}
}

func TestDeliveryChildURLsResolveOnlyInsideOwningHandle(t *testing.T) {
	handle := DeliveryHandle{
		sessionID: "session-id", assetID: "stream-1", token: "native-token", children: newDeliveryChildTable(),
	}
	template, err := newDeliveryLinkTemplate(httptest.NewRequest(http.MethodGet, "/Videos/item/stream?PlaySessionId=compat-play-session&MediaSourceId=compat-media-source&StartTimeTicks=900000000&api_key=compat-secret", nil).URL)
	if err != nil {
		t.Fatal(err)
	}
	state := deliveryChildState{
		assetID: "stream-1", target: "https://provider.example/private/segment.ts?provider_token=secret", signature: "native-signature",
	}
	childID, err := handle.children.register(state)
	if err != nil {
		t.Fatal(err)
	}
	duplicateID, err := handle.children.register(state)
	if err != nil || duplicateID != childID {
		t.Fatalf("duplicate child target = %q, %v; want %q", duplicateID, err, childID)
	}
	childURL := template.childURL(childID, state)
	if parsedPath := httptest.NewRequest(http.MethodGet, childURL, nil).URL.Path; !strings.HasSuffix(parsedPath, ".ts") {
		t.Fatalf("target-derived child path = %q", parsedPath)
	}
	for _, forbidden := range []string{"/api/v1", "native-token", "native-signature", "provider.example", "provider_token", "target=", "compat-secret"} {
		if strings.Contains(childURL, forbidden) {
			t.Fatalf("compat child URL exposed %q: %s", forbidden, childURL)
		}
	}
	parsed := httptest.NewRequest(http.MethodGet, childURL, nil)
	query := parsed.URL.Query()
	if len(query) != 5 || query.Get("PlaySessionId") != "compat-play-session" ||
		query.Get("MediaSourceId") != "compat-media-source" || query.Get(deliveryChildQueryName) != childID ||
		query.Get("StartTimeTicks") != "900000000" || query.Get("api_key") != "compat-play-session" {
		t.Fatalf("unexpected compat child selectors: %s", childURL)
	}

	foreign := handle
	foreign.children = newDeliveryChildTable()
	if _, err := requestForDelivery(parsed, foreign); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("cross-handle child resolution error = %v", err)
	}
	for attempt := range 2 {
		resolved, err := requestForDelivery(parsed, handle)
		if err != nil {
			t.Fatalf("legitimate retry %d: %v", attempt, err)
		}
		if !resolved.child || resolved.assetID != state.assetID || resolved.target != state.target || resolved.signature != state.signature {
			t.Fatalf("resolved child state = %#v", resolved)
		}
	}
	unknown := httptest.NewRequest(http.MethodGet, template.childURL("unknown-child", state), nil)
	if _, err := requestForDelivery(unknown, handle); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("unknown child resolution error = %v", err)
	}
	oversized := httptest.NewRequest(http.MethodGet, template.childURL(strings.Repeat("x", maximumDeliveryChildIDLength+1), state), nil)
	if _, err := requestForDelivery(oversized, handle); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("oversized child ID error = %v", err)
	}
}

func TestDeliveryLinkTemplateRejectsAmbiguousOrUnboundedChildContext(t *testing.T) {
	base := "/Videos/item/stream?PlaySessionId=compat-play-session&MediaSourceId=compat-media-source"
	for name, suffix := range map[string]string{
		"duplicate ticks":      "&StartTimeTicks=1&StartTimeTicks=1",
		"negative ticks":       "&StartTimeTicks=-1",
		"overflow ticks":       "&StartTimeTicks=999999999999999999999999999999999",
		"duplicate credential": "&api_key=one&api_key=one",
		"blank credential":     "&api_key=%20",
		"oversized credential": "&api_key=" + strings.Repeat("x", maximumDeliveryCompatTokenLength+1),
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, base+suffix, nil)
			if _, err := newDeliveryLinkTemplate(request.URL); !errors.Is(err, ErrSessionNotFound) {
				t.Fatalf("invalid child context error = %v", err)
			}
		})
	}
}

func TestDeliveryChildTableReclaimsStaleChildrenDuringContinuousPlayback(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	table := newDeliveryChildTable()
	table.now = func() time.Time { return now }
	activeState := deliveryChildState{assetID: "stream-1", target: "https://provider.example/live/segment-active.m4s", signature: "signed"}

	const workers = 32
	ids := make(chan string, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			childID, err := table.register(activeState)
			if err != nil {
				ids <- "error:" + err.Error()
				return
			}
			ids <- childID
		}()
	}
	group.Wait()
	close(ids)
	var activeID string
	for childID := range ids {
		if strings.HasPrefix(childID, "error:") {
			t.Fatal(childID)
		}
		if activeID == "" {
			activeID = childID
		} else if childID != activeID {
			t.Fatalf("deduplicated IDs differ: %q and %q", activeID, childID)
		}
	}
	if len(table.entries) != 1 || len(activeID) > maximumDeliveryChildIDLength {
		t.Fatalf("deduplicated table size/ID = %d/%d", len(table.entries), len(activeID))
	}
	futureID, err := table.register(deliveryChildState{assetID: "stream-1", target: "https://provider.example/live/segment-future.m4s", signature: "signed"})
	if err != nil {
		t.Fatal(err)
	}
	if entry := table.entries[futureID]; !entry.activeAt.Equal(now) || !table.nextExpiry.Equal(now.Add(deliveryChildTTL)) {
		t.Fatalf("child/next expiry timestamps = %s/%s", entry.activeAt, table.nextExpiry)
	}

	for minute := 1; minute <= 6; minute++ {
		now = now.Add(time.Minute)
		if _, ok := table.resolve(activeID); !ok {
			t.Fatalf("active child retry %d failed", minute)
		}
		if _, err := table.register(deliveryChildState{
			assetID: "stream-1", target: fmt.Sprintf("https://provider.example/live/segment-rotating-%06d.m4s", minute), signature: "signed",
		}); err != nil {
			t.Fatalf("continuous playlist activity %d: %v", minute, err)
		}
		if len(table.entries) > maximumDeliveryChildren {
			t.Fatalf("child table exceeded bound: %d", len(table.entries))
		}
	}
	if _, ok := table.resolve(futureID); ok {
		t.Fatal("stale future segment capability survived continuous playback")
	}
	if _, ok := table.resolve(activeID); !ok {
		t.Fatal("active segment capability expired during continuous playback")
	}
	now = now.Add(deliveryChildTTL)
	if _, ok := table.resolve(activeID); ok {
		t.Fatal("child capability survived its idle TTL")
	}
	if len(table.entries) != 0 {
		t.Fatalf("idle table retained %d children", len(table.entries))
	}
	if _, err := table.register(deliveryChildState{assetID: "stream-1", target: strings.Repeat("x", maximumDeliveryTargetLength+1), signature: "signed"}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("oversized child state error = %v", err)
	}
}

func TestDeliveryChildTableRetainsStaticPlaylistTailWhilePlaybackActive(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	table := newDeliveryChildTable()
	table.now = func() time.Time { return now }
	staticState := func(file string) deliveryChildState {
		return deliveryChildState{assetID: "stream-1", file: file, start: "90", retainWhileActive: true}
	}
	activeID, err := table.register(staticState("segment-active.m4s"))
	if err != nil {
		t.Fatal(err)
	}
	tailID, err := table.register(staticState("segment-tail.m4s"))
	if err != nil {
		t.Fatal(err)
	}

	for minute := 1; minute <= 6; minute++ {
		now = now.Add(time.Minute)
		if _, ok := table.resolve(activeID); !ok {
			t.Fatalf("active static segment retry %d failed", minute)
		}
	}
	if _, ok := table.resolve(tailID); !ok {
		t.Fatal("static playlist tail expired during active playback")
	}
	now = now.Add(deliveryChildTTL)
	if _, ok := table.resolve(tailID); ok {
		t.Fatal("static playlist tail survived an idle playback TTL")
	}
}

func TestSlidingLocalPlaylistCapabilitiesExpireHistoricalSegmentsOnly(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	table := newDeliveryChildTable()
	table.now = func() time.Time { return now }
	playlistID, err := table.register(deliveryChildState{
		assetID: "stream-1", file: "index.m3u8", start: "90", retainWhileActive: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	registerSnapshot := func(first int) map[string]string {
		t.Helper()
		var playlist strings.Builder
		fmt.Fprintf(&playlist, "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:%d\n#EXT-X-MAP:URI=\"init.mp4\"\n", first)
		for offset := range hlsRetainedSegments {
			fmt.Fprintf(&playlist, "#EXTINF:%d,\nsegment-%06d.m4s\n", hlsSegmentDurationSeconds, first+offset)
		}
		contents := []byte(playlist.String())
		if playlistChildrenRetainWhileActive(contents) {
			t.Fatal("sliding playlist was classified as static")
		}
		ids := make(map[string]string, hlsRetainedSegments+1)
		var registerErr error
		_, rewriteErr := rewriteLocalPlaylistWithReferencePolicy(contents, true, func(reference string, mediaSegment bool) string {
			if registerErr != nil {
				return ""
			}
			state := deliveryChildState{
				assetID: "stream-1", file: reference, start: "90", retainWhileActive: !mediaSegment,
			}
			ids[reference], registerErr = table.register(state)
			return "/" + ids[reference]
		})
		if rewriteErr != nil {
			t.Fatal(rewriteErr)
		}
		if registerErr != nil {
			t.Fatal(registerErr)
		}
		return ids
	}

	initial := registerSnapshot(0)
	oldestID := initial["segment-000000.m4s"]
	initID := initial["init.mp4"]
	if table.entries[oldestID].state.retainWhileActive || !table.entries[initID].state.retainWhileActive {
		t.Fatal("sliding media/init retention policy was not recorded")
	}

	step := deliveryChildTTL / time.Duration(hlsRetainedSegments)
	current := initial
	for first := 1; first <= 3*hlsRetainedSegments; first++ {
		now = now.Add(step)
		if _, ok := table.resolve(playlistID); !ok {
			t.Fatalf("playlist reconnect failed after rotation %d", first)
		}
		current = registerSnapshot(first)
	}

	if _, ok := table.resolve(oldestID); ok {
		t.Fatal("historical segment survived after leaving the sliding window for its TTL")
	}
	if resolved, ok := table.resolve(playlistID); !ok || resolved.file != "index.m3u8" {
		t.Fatal("playlist capability expired during the active session")
	}
	if current["init.mp4"] != initID {
		t.Fatal("repeated playlist snapshots did not reuse the init capability")
	}
	if _, ok := table.resolve(initID); !ok {
		t.Fatal("init capability expired during the active session")
	}
	for file, childID := range current {
		if file == "init.mp4" {
			continue
		}
		if _, ok := table.resolve(childID); !ok {
			t.Fatalf("segment in the current seek window expired: %s", file)
		}
	}
	if len(table.entries) > 2*hlsRetainedSegments+2 {
		t.Fatalf("sliding capability table retained %d entries after long rotation, want at most %d", len(table.entries), 2*hlsRetainedSegments+2)
	}
}

func TestDeliveryChildTableCapacityRejectsOnlySimultaneouslyLiveChildren(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	table := newDeliveryChildTable()
	table.now = func() time.Time { return now }
	firstGenerationID := ""

	registerGeneration := func(generation int) {
		t.Helper()
		for index := range maximumDeliveryChildren {
			childID, err := table.register(deliveryChildState{
				assetID: "stream-1", file: fmt.Sprintf("segment-%d-%06d.m4s", generation, index), start: "90",
			})
			if err != nil {
				t.Fatalf("generation %d child %d: %v", generation, index, err)
			}
			if generation == 1 && index == 0 {
				firstGenerationID = childID
			}
			if len(table.entries) > maximumDeliveryChildren {
				t.Fatalf("generation %d exceeded child bound: %d", generation, len(table.entries))
			}
		}
	}

	registerGeneration(1)
	if _, err := table.register(deliveryChildState{assetID: "stream-1", file: "simultaneous-overflow.m4s", start: "90"}); !errors.Is(err, errDeliveryChildCapacity) {
		t.Fatalf("simultaneously live overflow error = %v", err)
	}
	if len(table.entries) != maximumDeliveryChildren {
		t.Fatalf("full child table size = %d, want %d", len(table.entries), maximumDeliveryChildren)
	}

	now = now.Add(deliveryChildTTL)
	registerGeneration(2)
	if _, ok := table.resolve(firstGenerationID); ok {
		t.Fatal("expired first-generation child remained resolvable")
	}
	if len(table.entries) != maximumDeliveryChildren {
		t.Fatalf("rotated child table size = %d, want %d", len(table.entries), maximumDeliveryChildren)
	}
	if _, err := table.register(deliveryChildState{assetID: "stream-1", file: "second-overflow.m4s", start: "90"}); !errors.Is(err, errDeliveryChildCapacity) {
		t.Fatalf("second simultaneously live overflow error = %v", err)
	}
}

func TestPlaylistChildrenRetentionPolicySeparatesStaticAndSlidingMedia(t *testing.T) {
	for name, test := range map[string]struct {
		playlist string
		want     bool
	}{
		"completed media": {playlist: "#EXTM3U\n#EXTINF:3,\nsegment.m4s\n#EXT-X-ENDLIST\n", want: true},
		"event media":     {playlist: "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXTINF:3,\nsegment.m4s\n", want: true},
		"explicit VOD":    {playlist: "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:VOD\nsegment.m4s\n", want: true},
		"master":          {playlist: "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nmedia.m3u8\n", want: true},
		"sliding media":   {playlist: "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:42\n#EXTINF:3,\nsegment.m4s\n", want: false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := playlistChildrenRetainWhileActive([]byte(test.playlist)); got != test.want {
				t.Fatalf("retain while active = %t, want %t", got, test.want)
			}
		})
	}
}

func TestCompatPlaylistLinksHideNativeHLSState(t *testing.T) {
	table := newDeliveryChildTable()
	template, err := newDeliveryLinkTemplate(httptest.NewRequest(http.MethodGet, "/emby/Videos/item/stream.m3u8?PlaySessionId=compat-play-session&MediaSourceId=compat-media-source&StartTimeTicks=900000000&api_key=compat-secret", nil).URL)
	if err != nil {
		t.Fatal(err)
	}
	build := func(target string) string {
		state := deliveryChildState{assetID: "stream-1", target: target, signature: "native-signature"}
		childID, registerErr := table.register(state)
		if registerErr != nil {
			t.Fatal(registerErr)
		}
		return template.childURL(childID, state)
	}
	base, _ := new(url.URL).Parse("https://provider.example/private/master.m3u8?native_token=secret")
	master, err := rewritePlaylist([]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=800000\nmedia.m3u8\n"), base, build)
	if err != nil {
		t.Fatal(err)
	}
	mediaBase, _ := new(url.URL).Parse("https://provider.example/private/media.m3u8?native_token=secret")
	media, err := rewritePlaylist([]byte("#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n#EXTINF:4,\nsegment.ts\n"), mediaBase, build)
	if err != nil {
		t.Fatal(err)
	}
	assertCompatPlaylistChildLinks(t, string(master), 1)
	assertCompatPlaylistChildLinks(t, string(media), 2)

	local, err := rewriteLocalPlaylist([]byte("#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\nsegment-000001.m4s\n"), func(file string) string {
		state := deliveryChildState{assetID: "stream-1", file: file, start: "90"}
		childID, registerErr := table.register(state)
		if registerErr != nil {
			t.Fatal(registerErr)
		}
		return template.childURL(childID, state)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCompatPlaylistChildLinks(t, string(local), 2)
}

func TestNativeHLSURLGenerationRemainsUnchanged(t *testing.T) {
	native := hlsAssetURLAt("native-session", "native-asset", "native-token", "segment-000001.m4s", 90)
	if !strings.HasPrefix(native, "/api/v1/playback/sessions/native-session/assets/native-asset?") ||
		!strings.Contains(native, "token=native-token") || !strings.Contains(native, "file=segment-000001.m4s") || !strings.Contains(native, "start=90") {
		t.Fatalf("native HLS URL semantics changed: %s", native)
	}
}

func TestCompatPlaylistRejectsUnproxyableReferences(t *testing.T) {
	base, _ := new(url.URL).Parse("https://provider.example/master.m3u8")
	for name, playlist := range map[string]string{
		"media": "#EXTM3U\nftp://user:provider-secret@example.test/segment.ts\n",
		"key":   "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"ftp://user:provider-secret@example.test/key\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			rewritten, err := rewritePlaylistWithPolicy([]byte(playlist), base, true, func(string) string { return "/compat-child" })
			if !errors.Is(err, ErrMediaSourceFailed) || len(rewritten) != 0 {
				t.Fatalf("unsafe compat playlist rewrite = %q, %v", rewritten, err)
			}
		})
	}
	local, err := rewriteLocalPlaylistWithPolicy([]byte("#EXTM3U\n#EXT-X-MAP:URI=\"../provider-secret\"\n"), true, func(string) string { return "/compat-child" })
	if !errors.Is(err, ErrMediaProcessingFailed) || len(local) != 0 {
		t.Fatalf("unsafe local compat playlist rewrite = %q, %v", local, err)
	}
}

func assertCompatPlaylistChildLinks(t *testing.T, playlist string, want int) {
	t.Helper()
	references := make([]string, 0, want)
	for _, line := range strings.Split(playlist, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "#") {
			references = append(references, trimmed)
			continue
		}
		for _, match := range playlistURIAttribute.FindAllStringSubmatch(line, -1) {
			references = append(references, match[1])
		}
	}
	if len(references) != want {
		t.Fatalf("playlist references = %v, want %d in %s", references, want, playlist)
	}
	for _, reference := range references {
		for _, forbidden := range []string{"/api/v1", "native_token", "native-signature", "provider.example", "target=", "signature=", "compat-secret"} {
			if strings.Contains(reference, forbidden) {
				t.Fatalf("playlist child link exposed %q: %s", forbidden, reference)
			}
		}
		request := httptest.NewRequest(http.MethodGet, reference, nil)
		query := request.URL.Query()
		if len(query) != 5 || query.Get(deliveryChildQueryName) == "" || query.Get("MediaSourceId") != "compat-media-source" || query.Get("StartTimeTicks") != "900000000" || query.Get("PlaySessionId") != "compat-play-session" || query.Get("api_key") != "compat-play-session" {
			t.Fatalf("playlist child selectors = %s", reference)
		}
		if !strings.Contains(request.URL.Path, "/hls1/compat-play-session/") || strings.LastIndex(request.URL.Path, ".") < strings.LastIndex(request.URL.Path, "/") {
			t.Fatalf("playlist child path is not extension-correct: %s", reference)
		}
	}
}

type canceledPlaybackWriter struct{}

func (canceledPlaybackWriter) Write([]byte) (int, error) { return 0, context.Canceled }

func TestCopyPlaybackAssetPropagatesClientCancellation(t *testing.T) {
	err := copyPlaybackAsset(canceledPlaybackWriter{}, strings.NewReader("media bytes"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("copy cancellation error = %v", err)
	}
}

func TestDeliveryCancellationAndChildErrorsKeepHandleRetryable(t *testing.T) {
	handle := DeliveryHandle{
		sessionID: "session-id", assetID: "stream-1", token: "native-token", defaultFile: "master.m3u8", children: newDeliveryChildTable(),
	}
	if IsTerminalDeliveryError(copyPlaybackAsset(canceledPlaybackWriter{}, strings.NewReader("media bytes"))) {
		t.Fatal("downstream cancellation was classified as terminal")
	}
	unknownChild := httptest.NewRequest(http.MethodGet, "/Videos/item/master.m3u8?PlaySessionId=play&MediaSourceId=source&"+deliveryChildQueryName+"=unknown", nil)
	service := &Service{}
	childErr := service.Serve(httptest.NewRecorder(), unknownChild, handle)
	if !errors.Is(childErr, ErrSessionNotFound) || IsTerminalDeliveryError(childErr) {
		t.Fatalf("child error terminal classification = %v/%t", childErr, IsTerminalDeliveryError(childErr))
	}
	retry := httptest.NewRequest(http.MethodGet, "/Videos/item/master.m3u8?PlaySessionId=play&MediaSourceId=source", nil)
	resolved, err := requestForDelivery(retry, handle)
	if err != nil || resolved.assetID != "stream-1" || resolved.request.URL.Query().Get("file") != "master.m3u8" {
		t.Fatalf("retry after child failure = %+v, %v", resolved, err)
	}
	if !IsTerminalDeliveryError(ErrSessionNotFound) || IsTerminalDeliveryError(ErrMediaSourceFailed) {
		t.Fatal("delivery terminal error classification is not conservative")
	}
}

func TestResolvedDeliveryChildMissingNativeSessionIsTerminal(t *testing.T) {
	handle := DeliveryHandle{
		sessionID: "missing-session", assetID: "stream-1", token: "native-token", children: newDeliveryChildTable(),
	}
	state := deliveryChildState{assetID: "stream-1", file: "segment.ts"}
	childID, err := handle.children.register(state)
	if err != nil {
		t.Fatal(err)
	}
	template, err := newDeliveryLinkTemplate(httptest.NewRequest(http.MethodGet, "/Videos/item/master.m3u8?PlaySessionId=play&MediaSourceId=source", nil).URL)
	if err != nil {
		t.Fatal(err)
	}
	childRequest := httptest.NewRequest(http.MethodGet, template.childURL(childID, state), nil)
	childQuery := childRequest.URL.Query()
	childQuery.Set("PlaySessionId", "play")
	childRequest.URL.RawQuery = childQuery.Encode()
	resolved, err := requestForDelivery(childRequest, handle)
	if err != nil || !resolved.child {
		t.Fatalf("valid child resolution = %+v, %v", resolved, err)
	}

	missingSessionErr := classifyDeliveryProxyError(ErrSessionNotFound, resolved.child)
	if !errors.Is(missingSessionErr, ErrSessionNotFound) || !IsTerminalDeliveryError(missingSessionErr) {
		t.Fatalf("resolved child missing-session classification = %v/%t", missingSessionErr, IsTerminalDeliveryError(missingSessionErr))
	}

	httpClientErr := errors.New("HTTP client request failed")
	retryableErr := classifyDeliveryProxyError(httpClientErr, resolved.child)
	var requestErr *deliveryRequestError
	if !errors.Is(retryableErr, httpClientErr) || !errors.As(retryableErr, &requestErr) || IsTerminalDeliveryError(retryableErr) {
		t.Fatalf("resolved child HTTP client classification = %v/%t", retryableErr, IsTerminalDeliveryError(retryableErr))
	}
}

func TestDeliveryHandleSelectsDefaultCompatibleAssetWithoutURLParsing(t *testing.T) {
	handle := deliveryHandleForSession("session-id", "native-token", []Source{
		{ID: "incompatible", Compatible: false, URL: "not a URL"},
		{ID: "selected", Compatible: true, Protocol: "hls", Mode: processingRemux, URL: "also not a URL"},
	}, []storedAsset{
		{ID: "incompatible", URL: "https://provider.example/first"},
		{ID: "selected", URL: "https://provider.example/selected", StartSeconds: 120},
		{ID: "subtitle-1", Kind: assetKindConvertedSubtitle, URL: "https://provider.example/subtitle.srt?token=native"},
	})

	if !handle.Valid() || handle.assetID != "selected" || handle.defaultFile != "master.m3u8" || handle.defaultStart != "120" ||
		handle.assets == nil || !handle.assets.contains("selected") || !handle.assets.contains("subtitle-1") || handle.assets.contains("foreign-asset") {
		t.Fatalf("unexpected selected delivery handle: %+v", handle)
	}
}

func TestDeliveryChildBudgetEnforcesProfileAndGlobalLimitsAcrossHandles(t *testing.T) {
	if maximumDeliveryChildrenPerProfile < maximumPlaylistReferences {
		t.Fatalf("per-profile child limit = %d, below one maximum playlist (%d)", maximumDeliveryChildrenPerProfile, maximumPlaylistReferences)
	}
	if maximumDeliveryChildrenGlobal < maximumDeliveryChildrenPerProfile {
		t.Fatalf("global child limit = %d, below per-profile limit %d", maximumDeliveryChildrenGlobal, maximumDeliveryChildrenPerProfile)
	}
	if maximumDeliveryChildrenGlobal > 8*maximumDeliveryChildrenPerProfile {
		t.Fatalf("global child limit = %d, want a strict small multiple of per-profile limit %d", maximumDeliveryChildrenGlobal, maximumDeliveryChildrenPerProfile)
	}

	budget := newDeliveryChildBudget(4, 2)
	newTable := func(profileID string) *deliveryChildTable {
		table := newDeliveryChildTable()
		table.bindBudget(budget, profileID, nil)
		return table
	}
	register := func(table *deliveryChildTable, file string) error {
		t.Helper()
		_, err := table.register(deliveryChildState{assetID: "stream-1", file: file, start: "90"})
		return err
	}

	firstProfileHandle := newTable("profile-a")
	secondProfileAHandle := newTable("profile-a")
	if err := register(firstProfileHandle, "segment-a-1.m4s"); err != nil {
		t.Fatal(err)
	}
	if err := register(secondProfileAHandle, "segment-a-2.m4s"); err != nil {
		t.Fatal(err)
	}
	if err := register(secondProfileAHandle, "segment-a-overflow.m4s"); !errors.Is(err, errDeliveryChildCapacity) {
		t.Fatalf("cross-handle per-profile overflow error = %v", err)
	}

	secondProfileHandle := newTable("profile-b")
	if err := register(secondProfileHandle, "segment-b-1.m4s"); err != nil {
		t.Fatalf("second profile below global limit: %v", err)
	}
	thirdProfileHandle := newTable("profile-c")
	if err := register(thirdProfileHandle, "segment-c-1.m4s"); err != nil {
		t.Fatalf("third profile at global limit: %v", err)
	}
	fourthProfileHandle := newTable("profile-d")
	if err := register(fourthProfileHandle, "segment-d-overflow.m4s"); !errors.Is(err, errDeliveryChildCapacity) {
		t.Fatalf("global overflow error = %v", err)
	}
	if global, profile := budget.usage("profile-a"); global != 4 || profile != 2 {
		t.Fatalf("budget usage = global %d/profile-a %d, want 4/2", global, profile)
	}
}

func TestDeliveryChildBudgetPruneAndCloseReleaseCapacity(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	budget := newDeliveryChildBudget(2, 2)
	service := &Service{now: func() time.Time { return now }, deliveryChildren: budget}
	newTable := func() *deliveryChildTable {
		table := newDeliveryChildTable()
		table.bindBudget(budget, "profile-a", func() time.Time { return now })
		return table
	}
	first := newTable()
	second := newTable()
	for index, table := range []*deliveryChildTable{first, second} {
		if _, err := table.register(deliveryChildState{assetID: "stream-1", file: fmt.Sprintf("segment-%d.m4s", index), start: "90"}); err != nil {
			t.Fatal(err)
		}
	}

	now = now.Add(deliveryChildTTL)
	service.PruneDeliveryHandles()
	if global, profile := budget.usage("profile-a"); global != 0 || profile != 0 {
		t.Fatalf("usage after prune = global %d/profile %d", global, profile)
	}
	if len(first.entries) != 0 || len(second.entries) != 0 {
		t.Fatalf("prune retained expired entries: %d/%d", len(first.entries), len(second.entries))
	}
	tracked := 0
	budget.tables.Range(func(_, _ any) bool {
		tracked++
		return true
	})
	if tracked != 0 {
		t.Fatalf("prune retained %d empty child tables", tracked)
	}

	if _, err := first.register(deliveryChildState{assetID: "stream-1", file: "segment-reused.m4s", start: "90"}); err != nil {
		t.Fatalf("capacity was not reusable after prune: %v", err)
	}
	handle := DeliveryHandle{sessionID: "session-id", assetID: "stream-1", token: "opaque-token", children: first}
	if err := service.Close(context.Background(), auth.Principal{}, handle); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Close terminal error = %v", err)
	}
	if global, profile := budget.usage("profile-a"); global != 0 || profile != 0 {
		t.Fatalf("usage after Close = global %d/profile %d", global, profile)
	}
	if _, err := first.register(deliveryChildState{assetID: "stream-1", file: "segment-after-close.m4s", start: "90"}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("closed handle accepted a new child: %v", err)
	}
}

func TestDeliveryChildBudgetConcurrentClearAndPruneNeverDoubleRelease(t *testing.T) {
	budget := newDeliveryChildBudget(128, 128)
	table := newDeliveryChildTable()
	table.bindBudget(budget, "profile-a", nil)
	start := make(chan struct{})
	var group sync.WaitGroup
	for worker := range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for index := range 16 {
				_, err := table.register(deliveryChildState{
					assetID: "stream-1", file: fmt.Sprintf("segment-%d-%d.m4s", worker, index), start: "90",
				})
				if err != nil && !errors.Is(err, errDeliveryChildCapacity) && !errors.Is(err, ErrSessionNotFound) {
					t.Errorf("register: %v", err)
				}
			}
		}()
	}
	service := &Service{deliveryChildren: budget}
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			service.PruneDeliveryHandles()
			table.clear()
		}()
	}
	close(start)
	group.Wait()
	table.clear()
	if global, profile := budget.usage("profile-a"); global != 0 || profile != 0 {
		t.Fatalf("concurrent final usage = global %d/profile %d", global, profile)
	}
}
