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
	childURL := delivery.template.childURL("child-capability")
	childQuery := httptest.NewRequest(http.MethodGet, childURL, nil).URL.Query()
	if childQuery.Get("StartTimeTicks") != "900000000" || childQuery.Get("api_key") != "compat-token" {
		t.Fatalf("compat child URL lost seek or query authentication: %s", childURL)
	}
	if request.URL.Query().Get("file") != "" || request.URL.Query().Get("start") != "" {
		t.Fatal("delivery adaptation mutated the adapter request")
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
	childURL := template.childURL(childID)
	for _, forbidden := range []string{"/api/v1", "native-token", "native-signature", "provider.example", "provider_token", "target="} {
		if strings.Contains(childURL, forbidden) {
			t.Fatalf("compat child URL exposed %q: %s", forbidden, childURL)
		}
	}
	parsed := httptest.NewRequest(http.MethodGet, childURL, nil)
	if len(parsed.URL.Query()) != 5 || parsed.URL.Query().Get("PlaySessionId") != "compat-play-session" ||
		parsed.URL.Query().Get("MediaSourceId") != "compat-media-source" || parsed.URL.Query().Get(deliveryChildQueryName) != childID ||
		parsed.URL.Query().Get("StartTimeTicks") != "900000000" || parsed.URL.Query().Get("api_key") != "compat-secret" {
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
		if resolved.assetID != state.assetID || resolved.target != state.target || resolved.signature != state.signature {
			t.Fatalf("resolved child state = %#v", resolved)
		}
	}
	unknown := httptest.NewRequest(http.MethodGet, template.childURL("unknown-child"), nil)
	if _, err := requestForDelivery(unknown, handle); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("unknown child resolution error = %v", err)
	}
	oversized := httptest.NewRequest(http.MethodGet, template.childURL(strings.Repeat("x", maximumDeliveryChildIDLength+1)), nil)
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

func TestDeliveryChildTableExpiresUntouchedChildrenDuringContinuousActivity(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	table := newDeliveryChildTable()
	table.now = func() time.Time { return now }
	activeState := deliveryChildState{assetID: "stream-1", file: "segment-active.m4s", start: "90"}

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
	untouchedID, err := table.register(deliveryChildState{assetID: "stream-1", file: "segment-untouched.m4s", start: "90"})
	if err != nil {
		t.Fatal(err)
	}
	if entry := table.entries[untouchedID]; !entry.advertisedAt.Equal(now) || !entry.expiresAt.Equal(now.Add(deliveryChildTTL)) {
		t.Fatalf("unresolved child timestamps = %s/%s", entry.advertisedAt, entry.expiresAt)
	}

	for minute := 1; minute <= 6; minute++ {
		now = now.Add(time.Minute)
		if _, ok := table.resolve(activeID); !ok {
			t.Fatalf("active child retry %d failed", minute)
		}
		if _, err := table.register(deliveryChildState{
			assetID: "stream-1", file: fmt.Sprintf("segment-rotating-%06d.m4s", minute), start: "90",
		}); err != nil {
			t.Fatalf("continuous playlist activity %d: %v", minute, err)
		}
		if len(table.entries) > maximumDeliveryChildren {
			t.Fatalf("child table exceeded bound: %d", len(table.entries))
		}
	}
	if _, ok := table.resolve(untouchedID); ok {
		t.Fatal("untouched unresolved child survived its individual TTL")
	}
	if _, ok := table.resolve(activeID); !ok {
		t.Fatal("resolved child retry was not retained for its own TTL")
	}
	now = now.Add(deliveryChildTTL)
	if _, ok := table.resolve(activeID); ok {
		t.Fatal("resolved child survived beyond its retry TTL")
	}
	if _, err := table.register(deliveryChildState{assetID: "stream-1", target: strings.Repeat("x", maximumDeliveryTargetLength+1), signature: "signed"}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("oversized child state error = %v", err)
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

func TestCompatPlaylistLinksHideNativeHLSState(t *testing.T) {
	table := newDeliveryChildTable()
	template, err := newDeliveryLinkTemplate(httptest.NewRequest(http.MethodGet, "/emby/Videos/item/stream.m3u8?PlaySessionId=compat-play-session&MediaSourceId=compat-media-source&StartTimeTicks=900000000&api_key=compat-secret", nil).URL)
	if err != nil {
		t.Fatal(err)
	}
	build := func(target string) string {
		childID, registerErr := table.register(deliveryChildState{assetID: "stream-1", target: target, signature: "native-signature"})
		if registerErr != nil {
			t.Fatal(registerErr)
		}
		return template.childURL(childID)
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
		childID, registerErr := table.register(deliveryChildState{assetID: "stream-1", file: file, start: "90"})
		if registerErr != nil {
			t.Fatal(registerErr)
		}
		return template.childURL(childID)
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
		for _, forbidden := range []string{"/api/v1", "native_token", "native-signature", "provider.example", "target=", "signature="} {
			if strings.Contains(reference, forbidden) {
				t.Fatalf("playlist child link exposed %q: %s", forbidden, reference)
			}
		}
		request := httptest.NewRequest(http.MethodGet, reference, nil)
		query := request.URL.Query()
		if len(query) != 5 || query.Get(deliveryChildQueryName) == "" || query.Get("StartTimeTicks") != "900000000" || query.Get("api_key") != "compat-secret" {
			t.Fatalf("playlist child selectors = %s", reference)
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

func TestDeliveryHandleSelectsDefaultCompatibleAssetWithoutURLParsing(t *testing.T) {
	handle := deliveryHandleForSession("session-id", "native-token", []Source{
		{ID: "incompatible", Compatible: false, URL: "not a URL"},
		{ID: "selected", Compatible: true, Protocol: "hls", Mode: processingRemux, URL: "also not a URL"},
	}, []storedAsset{
		{ID: "incompatible", URL: "https://provider.example/first"},
		{ID: "selected", URL: "https://provider.example/selected", StartSeconds: 120},
	})

	if !handle.Valid() || handle.assetID != "selected" || handle.defaultFile != "index.m3u8" || handle.defaultStart != "120" {
		t.Fatalf("unexpected selected delivery handle: %+v", handle)
	}
}

func TestDeliveryChildBudgetEnforcesUserAndGlobalLimitsAcrossHandles(t *testing.T) {
	if maximumDeliveryChildrenPerUser < maximumPlaylistReferences {
		t.Fatalf("per-user child limit = %d, below one maximum playlist (%d)", maximumDeliveryChildrenPerUser, maximumPlaylistReferences)
	}
	if maximumDeliveryChildrenGlobal < maximumDeliveryChildrenPerUser {
		t.Fatalf("global child limit = %d, below per-user limit %d", maximumDeliveryChildrenGlobal, maximumDeliveryChildrenPerUser)
	}
	if maximumDeliveryChildrenGlobal > 8*maximumDeliveryChildrenPerUser {
		t.Fatalf("global child limit = %d, want a strict small multiple of per-user limit %d", maximumDeliveryChildrenGlobal, maximumDeliveryChildrenPerUser)
	}

	budget := newDeliveryChildBudget(4, 2)
	newTable := func(userID string) *deliveryChildTable {
		table := newDeliveryChildTable()
		table.bindBudget(budget, userID, nil)
		return table
	}
	register := func(table *deliveryChildTable, file string) error {
		t.Helper()
		_, err := table.register(deliveryChildState{assetID: "stream-1", file: file, start: "90"})
		return err
	}

	firstUserHandle := newTable("user-a")
	secondUserAHandle := newTable("user-a")
	if err := register(firstUserHandle, "segment-a-1.m4s"); err != nil {
		t.Fatal(err)
	}
	if err := register(secondUserAHandle, "segment-a-2.m4s"); err != nil {
		t.Fatal(err)
	}
	if err := register(secondUserAHandle, "segment-a-overflow.m4s"); !errors.Is(err, errDeliveryChildCapacity) {
		t.Fatalf("cross-handle per-user overflow error = %v", err)
	}

	secondUserHandle := newTable("user-b")
	if err := register(secondUserHandle, "segment-b-1.m4s"); err != nil {
		t.Fatalf("second user below global limit: %v", err)
	}
	thirdUserHandle := newTable("user-c")
	if err := register(thirdUserHandle, "segment-c-1.m4s"); err != nil {
		t.Fatalf("third user at global limit: %v", err)
	}
	fourthUserHandle := newTable("user-d")
	if err := register(fourthUserHandle, "segment-d-overflow.m4s"); !errors.Is(err, errDeliveryChildCapacity) {
		t.Fatalf("global overflow error = %v", err)
	}
	if global, user := budget.usage("user-a"); global != 4 || user != 2 {
		t.Fatalf("budget usage = global %d/user-a %d, want 4/2", global, user)
	}
}

func TestDeliveryChildBudgetPruneAndCloseReleaseCapacity(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	budget := newDeliveryChildBudget(2, 2)
	service := &Service{now: func() time.Time { return now }, deliveryChildren: budget}
	newTable := func() *deliveryChildTable {
		table := newDeliveryChildTable()
		table.bindBudget(budget, "user-a", func() time.Time { return now })
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
	if global, user := budget.usage("user-a"); global != 0 || user != 0 {
		t.Fatalf("usage after prune = global %d/user %d", global, user)
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
	if global, user := budget.usage("user-a"); global != 0 || user != 0 {
		t.Fatalf("usage after Close = global %d/user %d", global, user)
	}
	if _, err := first.register(deliveryChildState{assetID: "stream-1", file: "segment-after-close.m4s", start: "90"}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("closed handle accepted a new child: %v", err)
	}
}

func TestDeliveryChildBudgetConcurrentClearAndPruneNeverDoubleRelease(t *testing.T) {
	budget := newDeliveryChildBudget(128, 128)
	table := newDeliveryChildTable()
	table.bindBudget(budget, "user-a", nil)
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
	if global, user := budget.usage("user-a"); global != 0 || user != 0 {
		t.Fatalf("concurrent final usage = global %d/user %d", global, user)
	}
}
