package jellyfin

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
)

func TestDisplayPreferencesPersistAcrossServicesAndIsolateEverySelector(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run display preference persistence tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open display preference database: %v", err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate display preference database: %v", err)
	}
	fixture := seedDisplayPreferenceFixture(t, ctx, pool)

	tokenProfileA, tokenProfileB, tokenUserB := compatTestToken(31), compatTestToken(32), compatTestToken(33)
	authentication := &bootstrapAuthenticationFake{sessions: map[string]AuthenticatedSession{
		tokenProfileA: displayPreferenceSession("session-profile-a", fixture.userA, fixture.profileA, "device-a"),
		tokenProfileB: displayPreferenceSession("session-profile-b", fixture.userA, fixture.profileB, "device-a"),
		tokenUserB:    displayPreferenceSession("session-user-b", fixture.userB, fixture.profileA, "device-b"),
	}, revoked: make(map[string]bool)}
	first := newDatabaseDisplayPreferenceHandler(t, pool, authentication)

	opaqueBody := `{"Id":"home","ViewType":"Poster","SortBy":"SortName","CustomPrefs":{"future.layout":"{\"columns\":7}","plugin.option":"opaque-value"},"RememberSorting":true}`
	updated := bootstrapRequest(t, first, http.MethodPost, "/DisplayPreferences/home?UserId="+fixture.profileA+"&Client=Infuse", opaqueBody, tokenProfileA, "application/json; charset=utf-8")
	if updated.Code != http.StatusNoContent || updated.Body.Len() != 0 || updated.Header().Get("Cache-Control") != "no-store" || updated.Header().Get("Content-Type") != "" {
		t.Fatalf("display preference update = %d headers=%v body=%q", updated.Code, updated.Header(), updated.Body.String())
	}

	// Recreate both repository/service and HTTP handler to prove the value is
	// durable rather than retained by an adapter instance.
	second := newDatabaseDisplayPreferenceHandler(t, pool, authentication)
	storedResponse := bootstrapRequest(t, second, http.MethodGet, "/DisplayPreferences/home?UserId="+fixture.profileA+"&Client=Infuse", "", tokenProfileA, "")
	var stored DisplayPreferencesDto
	decodeCompatTestResponse(t, storedResponse, &stored)
	if storedResponse.Code != http.StatusOK || storedResponse.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
		stored.Id != "home" || stored.Client != "Infuse" || stored.ViewType != "Poster" || !stored.RememberSorting ||
		stored.CustomPrefs["future.layout"] != `{"columns":7}` || stored.CustomPrefs["plugin.option"] != "opaque-value" {
		t.Fatalf("persisted display preferences = %d headers=%v value=%+v", storedResponse.Code, storedResponse.Header(), stored)
	}

	isolatedUpdates := []struct {
		token, path, marker string
	}{
		{tokenProfileA, "/DisplayPreferences/home?Client=VidHub", "client"},
		{tokenProfileA, "/DisplayPreferences/library?Client=Infuse", "display"},
		{tokenProfileB, "/DisplayPreferences/home?Client=Infuse", "profile"},
		{tokenUserB, "/DisplayPreferences/home?Client=Infuse", "user"},
	}
	for _, update := range isolatedUpdates {
		response := bootstrapRequest(t, second, http.MethodPost, update.path, `{"CustomPrefs":{"scope":"`+update.marker+`"}}`, update.token, "application/json")
		if response.Code != http.StatusNoContent {
			t.Fatalf("store isolated %s preference: status=%d body=%s", update.marker, response.Code, response.Body.String())
		}
	}
	for _, update := range isolatedUpdates {
		response := bootstrapRequest(t, second, http.MethodGet, update.path, "", update.token, "")
		var value DisplayPreferencesDto
		decodeCompatTestResponse(t, response, &value)
		if response.Code != http.StatusOK || value.CustomPrefs["scope"] != update.marker {
			t.Fatalf("read isolated %s preference: status=%d value=%+v", update.marker, response.Code, value)
		}
	}
	originalAgain := bootstrapRequest(t, second, http.MethodGet, "/DisplayPreferences/home?Client=Infuse", "", tokenProfileA, "")
	var original DisplayPreferencesDto
	decodeCompatTestResponse(t, originalAgain, &original)
	if original.CustomPrefs["future.layout"] != `{"columns":7}` || original.CustomPrefs["scope"] != "" {
		t.Fatalf("isolated writes changed original preference: %+v", original)
	}

	crossProfile := bootstrapRequest(t, second, http.MethodGet, "/DisplayPreferences/home?UserId="+fixture.profileB+"&Client=Infuse", "", tokenProfileA, "")
	if crossProfile.Code != http.StatusNotFound {
		t.Fatalf("cross-profile display preference read status=%d body=%s", crossProfile.Code, crossProfile.Body.String())
	}
	anonymous := bootstrapRequest(t, second, http.MethodGet, "/DisplayPreferences/home?Client=Infuse", "", "", "")
	if anonymous.Code != http.StatusUnauthorized || anonymous.Header().Get("WWW-Authenticate") != "MediaBrowser" {
		t.Fatalf("anonymous display preference read status=%d headers=%v", anonymous.Code, anonymous.Header())
	}
}

func TestDisplayPreferenceHandlerRejectsMalformedOversizedAndUnboundedJSONWithoutMutation(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run display preference validation tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open display preference validation database: %v", err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate display preference validation database: %v", err)
	}
	fixture := seedDisplayPreferenceFixture(t, ctx, pool)
	validationToken := compatTestToken(34)
	authentication := &bootstrapAuthenticationFake{sessions: map[string]AuthenticatedSession{
		validationToken: displayPreferenceSession("validation-session", fixture.userA, fixture.profileA, "validation-device"),
	}, revoked: make(map[string]bool)}
	handler := newDatabaseDisplayPreferenceHandler(t, pool, authentication)

	tooMany := make([]string, 0, maximumDisplayPreferenceFields+1)
	for index := 0; index <= maximumDisplayPreferenceFields; index++ {
		tooMany = append(tooMany, fmt.Sprintf("\"key-%d\":\"value\"", index))
	}
	cases := []struct {
		name, body, contentType string
	}{
		{"malformed", `{"CustomPrefs":`, "application/json"},
		{"unknown field", `{"CustomPrefs":{},"FutureGlobalSwitch":true}`, "application/json"},
		{"too many custom keys", `{"CustomPrefs":{` + strings.Join(tooMany, ",") + `}}`, "application/json"},
		{"oversized custom key", `{"CustomPrefs":{"` + strings.Repeat("k", 65) + `":"value"}}`, "application/json"},
		{"oversized custom value", `{"CustomPrefs":{"key":"` + strings.Repeat("x", maximumDisplayPreferenceValue+1) + `"}}`, "application/json"},
		{"oversized body", `{"CustomPrefs":{"key":"` + strings.Repeat("x", int(maximumCompatJSONBodyBytes)) + `"}}`, "application/json"},
		{"wrong content type", `{"CustomPrefs":{}}`, "text/plain"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response := bootstrapRequest(t, handler, http.MethodPost, "/DisplayPreferences/home?Client=Infuse", test.body, validationToken, test.contentType)
			if response.Code != http.StatusBadRequest || response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("invalid preference status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
		})
	}
	oversizedClient := bootstrapRequest(t, handler, http.MethodPost, "/DisplayPreferences/home?Client="+strings.Repeat("c", 65), `{"CustomPrefs":{}}`, validationToken, "application/json")
	if oversizedClient.Code != http.StatusBadRequest {
		t.Fatalf("oversized display client status=%d body=%s", oversizedClient.Code, oversizedClient.Body.String())
	}
	oversizedID := bootstrapRequest(t, handler, http.MethodPost, "/DisplayPreferences/"+strings.Repeat("i", 65)+"?Client=Infuse", `{"CustomPrefs":{}}`, validationToken, "application/json")
	if oversizedID.Code != http.StatusNotFound {
		t.Fatalf("oversized display preference id status=%d body=%s", oversizedID.Code, oversizedID.Body.String())
	}
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM jellyfin_display_preferences
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, fixture.userA, fixture.profileA).Scan(&count); err != nil {
		t.Fatalf("count rejected display preferences: %v", err)
	}
	if count != 0 {
		t.Fatalf("invalid display preferences persisted %d rows", count)
	}
	for index := range maximumDisplayPreferencesPerScope {
		path := fmt.Sprintf("/DisplayPreferences/pref-%03d?Client=Infuse", index)
		response := bootstrapRequest(t, handler, http.MethodPost, path, `{"CustomPrefs":{}}`, validationToken, "application/json")
		if response.Code != http.StatusNoContent {
			t.Fatalf("fill display preference quota at %d: status=%d body=%s", index, response.Code, response.Body.String())
		}
	}
	overflow := bootstrapRequest(t, handler, http.MethodPost, "/DisplayPreferences/overflow?Client=Infuse", `{"CustomPrefs":{}}`, validationToken, "application/json")
	if overflow.Code != http.StatusTooManyRequests {
		t.Fatalf("display preference quota overflow status=%d body=%s", overflow.Code, overflow.Body.String())
	}
	existing := bootstrapRequest(t, handler, http.MethodPost, "/DisplayPreferences/pref-000?Client=Infuse", `{"CustomPrefs":{"updated":"true"}}`, validationToken, "application/json")
	if existing.Code != http.StatusNoContent {
		t.Fatalf("display preference update at quota status=%d body=%s", existing.Code, existing.Body.String())
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM jellyfin_display_preferences
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, fixture.userA, fixture.profileA).Scan(&count); err != nil {
		t.Fatalf("count bounded display preferences: %v", err)
	}
	if count != maximumDisplayPreferencesPerScope {
		t.Fatalf("bounded display preferences = %d, want %d", count, maximumDisplayPreferencesPerScope)
	}
}

type displayPreferenceFixture struct {
	userA, userB, profileA, profileB string
}

func seedDisplayPreferenceFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) displayPreferenceFixture {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var fixture displayPreferenceFixture
	for name, target := range map[string]*string{"a": &fixture.userA, "b": &fixture.userB} {
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (username, password_hash, role)
			VALUES ($1, 'unused-display-preference-hash', 'member')
			RETURNING id::text
		`, "display_preference_user_"+name+"_"+suffix).Scan(target); err != nil {
			t.Fatalf("insert display preference user %s: %v", name, err)
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO profiles (name) VALUES ($1) RETURNING id::text`, "Display preference A "+suffix).Scan(&fixture.profileA); err != nil {
		t.Fatalf("insert first display preference profile: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name, category_id)
		SELECT $1, category_id FROM profiles WHERE id = $2::uuid
		RETURNING id::text
	`, "Display preference B "+suffix, fixture.profileA).Scan(&fixture.profileB); err != nil {
		t.Fatalf("insert second display preference profile: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1::uuid[])`, []string{fixture.userA, fixture.userB})
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id = ANY($1::uuid[])`, []string{fixture.profileA, fixture.profileB})
	})
	return fixture
}

func displayPreferenceSession(sessionID, userID, profileID, deviceID string) AuthenticatedSession {
	activeProfileID := profileID
	return AuthenticatedSession{
		ID: sessionID, ProfileID: profileID, ProfileName: "Preference profile", ExpiresAt: time.Now().UTC().Add(time.Hour),
		Client:    ClientIdentity{Client: "Compatibility Client", Device: "Test device", DeviceID: deviceID, Version: "1.0"},
		Principal: auth.Principal{SessionID: "native-" + sessionID, UserID: userID, DeviceID: deviceID, ActiveProfileID: &activeProfileID},
	}
}

func newDatabaseDisplayPreferenceHandler(t *testing.T, pool *pgxpool.Pool, authentication Authentication) *Handler {
	t.Helper()
	repository, err := NewDisplayPreferenceRepository(pool)
	if err != nil {
		t.Fatalf("create display preference repository: %v", err)
	}
	service, err := NewDisplayPreferenceService(repository)
	if err != nil {
		t.Fatalf("create display preference service: %v", err)
	}
	serverID, err := ParseServerID("d1000000-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Dependencies{
		ServerInfo: ServerInfo{ID: serverID, Name: "Rivune"}, Authentication: authentication, DisplayPreferences: service,
	})
	if err != nil {
		t.Fatalf("create display preference handler: %v", err)
	}
	return handler
}
