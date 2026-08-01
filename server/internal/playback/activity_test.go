package playback

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
)

func TestActivityModeUsesActualProcessingContract(t *testing.T) {
	tests := []struct {
		name   string
		assets []storedAsset
		want   string
	}{
		{name: "direct", assets: []storedAsset{{Kind: "stream"}}, want: "direct"},
		{name: "remux", assets: []storedAsset{{Kind: processingRemux}}, want: processingRemux},
		{name: "audio", assets: []storedAsset{{Kind: processingTranscodeAudio}}, want: processingTranscodeAudio},
		{name: "video", assets: []storedAsset{{Kind: processingTranscode}}, want: processingTranscode},
		{name: "subtitle then video", assets: []storedAsset{{Kind: assetKindEmbeddedSubtitle}, {Kind: processingTranscode}}, want: processingTranscode},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.assets)
			if err != nil {
				t.Fatal(err)
			}
			if got := activityMode(encoded); got != test.want {
				t.Fatalf("activity mode = %q, want %q", got, test.want)
			}
		})
	}
}

func TestActivityArtworkUsesFirstSafeCanonicalCandidate(t *testing.T) {
	candidates := [6]string{
		"https://user:secret@images.example.test/private.jpg",
		"file:///private/still.jpg",
		"https://images.example.test/season-poster.jpg",
		"https://images.example.test/season-backdrop.jpg",
		"https://images.example.test/series-poster.jpg",
		"",
	}
	if got := activityArtworkURL(&candidates); got != candidates[2] {
		t.Fatalf("artwork URL = %q, want first safe candidate %q", got, candidates[2])
	}

	unsafe := [6]string{"javascript:alert(1)", "file:///tmp/poster.jpg", "", "", "", ""}
	if got := activityArtworkURL(&unsafe); got != "" {
		t.Fatalf("unsafe artwork URL was exposed: %q", got)
	}
}

func TestFormatActivityTitle(t *testing.T) {
	season, episode := 5, 6
	tests := []struct {
		name          string
		storedTitle   string
		parentType    string
		parentTitle   string
		ancestorTitle string
		season        *int
		episode       *int
		want          string
	}{
		{
			name: "full episode label", storedTitle: "Astéroïque",
			parentType: "season", parentTitle: "Season 5", ancestorTitle: "Futurama",
			season: &season, episode: &episode, want: "Futurama · S05E06 · Astéroïque",
		},
		{
			name: "blank episode title", parentType: "season", parentTitle: "Season 5",
			ancestorTitle: "Futurama", season: &season, episode: &episode,
			want: "Futurama · S05E06",
		},
		{
			name: "UUID fallback suppressed",
			want: "Episode",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := formatActivityTitle(
				"episode", test.storedTitle, test.parentType, test.parentTitle,
				test.ancestorTitle, test.season, test.episode,
			)
			if got != test.want {
				t.Fatalf("activity title = %q, want %q", got, test.want)
			}
			if got == "5810d584-af52-4ba3-8cef-17a98bc19f77" {
				t.Fatalf("activity title exposed title UUID: %q", got)
			}
		})
	}
}

func TestActivityProjectsEpisodeHierarchyFromDatabase(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run the PostgreSQL playback activity test")
	}

	ctx := context.Background()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	// Provider identities are globally unique, so serialize copies of this fixture while it owns the live-shaped IDs.
	lockConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire playback activity fixture lock connection: %v", err)
	}
	if _, err := lockConn.Exec(ctx, "SELECT pg_advisory_lock(193648653, 1232874)"); err != nil {
		lockConn.Release()
		t.Fatalf("lock playback activity provider fixture: %v", err)
	}
	t.Cleanup(func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := lockConn.Exec(unlockCtx, "SELECT pg_advisory_unlock(193648653, 1232874)"); err != nil {
			t.Errorf("unlock playback activity provider fixture: %v", err)
		}
		lockConn.Release()
	})

	var userID, deviceID, authSessionID, profileID, seriesID, seasonID, episodeID, playbackSessionID string
	if err := pool.QueryRow(ctx, `
		SELECT gen_random_uuid()::text, gen_random_uuid()::text, gen_random_uuid()::text,
		       gen_random_uuid()::text, gen_random_uuid()::text, gen_random_uuid()::text,
		       gen_random_uuid()::text, gen_random_uuid()::text
	`).Scan(
		&userID, &deviceID, &authSessionID, &profileID,
		&seriesID, &seasonID, &episodeID, &playbackSessionID,
	); err != nil {
		t.Fatalf("generate playback activity fixture IDs: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin playback activity fixture: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	username := "activity-" + userID[:12]
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, username, password_hash, role)
		VALUES ($1::uuid, $2, 'test-password-hash', 'admin')
	`, userID, username); err != nil {
		t.Fatalf("seed playback activity user: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO devices (id, user_id, name, platform)
		VALUES ($1::uuid, $2::uuid, 'Activity test device', 'test')
	`, deviceID, userID); err != nil {
		t.Fatalf("seed playback activity device: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO profiles (id, name)
		VALUES ($1::uuid, 'Activity test profile')
	`, profileID); err != nil {
		t.Fatalf("seed playback activity profile: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($1::uuid, $2::uuid, true)
	`, userID, profileID); err != nil {
		t.Fatalf("seed playback activity profile access: %v", err)
	}

	accessTokenHash := sha256.Sum256([]byte(authSessionID + ":access"))
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_sessions (
			id, user_id, device_id, access_token_hash, access_expires_at,
			refresh_expires_at, active_profile_id, profile_grant_expires_at
		)
		VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4, now() + interval '1 hour',
			now() + interval '2 hours', $5::uuid, now() + interval '1 hour'
		)
	`, authSessionID, userID, deviceID, accessTokenHash[:], profileID); err != nil {
		t.Fatalf("seed playback activity auth session: %v", err)
	}

	const (
		episodeStill = "https://images.example.test/futurama/season-5/episode-6.jpg"
		seriesIMDb   = "tt0149460"
		seriesTMDB   = "615"
		episodeTMDB  = "1232874"
		episodeTVDB  = "131235"
	)
	if _, err := tx.Exec(ctx, `
		INSERT INTO titles (id, media_type, parent_id, ordinal, display_title, poster_url) VALUES
			($1::uuid, 'series', NULL, NULL, 'Futurama', 'https://images.example.test/futurama.jpg'),
			($2::uuid, 'season', $1::uuid, 5, 'Season 5', 'https://images.example.test/futurama/season-5.jpg'),
			($3::uuid, 'episode', $2::uuid, 6, 'Astéroïque', $4)
	`, seriesID, seasonID, episodeID, episodeStill); err != nil {
		t.Fatalf("seed playback activity title hierarchy: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($1::uuid, 'imdb', 'series', $3),
			($1::uuid, 'tmdb', 'series', $4),
			($2::uuid, 'tmdb', 'episode', $5),
			($2::uuid, 'tvdb', 'episode', $6)
	`, seriesID, episodeID, seriesIMDb, seriesTMDB, episodeTMDB, episodeTVDB); err != nil {
		t.Fatalf("seed playback activity external IDs: %v", err)
	}

	playbackTokenHash := sha256.Sum256([]byte(playbackSessionID + ":playback"))
	if _, err := tx.Exec(ctx, `
		INSERT INTO playback_sessions (
			id, auth_session_id, profile_id, title_id, media_type, resource_id,
			token_hash, assets, expires_at, last_seen_at
		)
		VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4, 'episode', 'tmdb:episode:1232874',
			$5, '[{"kind":"stream"}]'::jsonb, now() + interval '1 hour', now()
		)
	`, playbackSessionID, authSessionID, profileID, episodeID, playbackTokenHash[:]); err != nil {
		t.Fatalf("seed playback activity session: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit playback activity fixture: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cleanupTx, err := pool.Begin(cleanupCtx)
		if err != nil {
			t.Errorf("begin playback activity fixture cleanup: %v", err)
			return
		}
		defer func() { _ = cleanupTx.Rollback(context.Background()) }()
		cleanups := []struct {
			name  string
			query string
			id    string
		}{
			{name: "user", query: "DELETE FROM users WHERE id = $1::uuid", id: userID},
			{name: "device", query: "DELETE FROM devices WHERE id = $1::uuid", id: deviceID},
			{name: "profile", query: "DELETE FROM profiles WHERE id = $1::uuid", id: profileID},
			{name: "title hierarchy", query: "DELETE FROM titles WHERE id = $1::uuid", id: seriesID},
		}
		for _, cleanup := range cleanups {
			if _, err := cleanupTx.Exec(cleanupCtx, cleanup.query, cleanup.id); err != nil {
				t.Errorf("clean up playback activity fixture %s: %v", cleanup.name, err)
				return
			}
		}
		if err := cleanupTx.Commit(cleanupCtx); err != nil {
			t.Errorf("commit playback activity fixture cleanup: %v", err)
		}
	})

	service, err := NewService(pool, nil, nil, MediaOptions{TempDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("create playback service: %v", err)
	}
	activity, err := service.Activity(ctx, auth.Principal{Role: "admin"})
	if err != nil {
		t.Fatalf("load playback activity: %v", err)
	}

	var session *ActivitySession
	for index := range activity.Sessions {
		if activity.Sessions[index].ID == playbackSessionID {
			session = &activity.Sessions[index]
			break
		}
	}
	if session == nil {
		t.Fatalf("seeded playback session %q was not returned", playbackSessionID)
	}
	if session.TitleID != episodeID {
		t.Fatalf("activity title ID = %q, want %q", session.TitleID, episodeID)
	}
	if session.Title != "Futurama · S05E06 · Astéroïque" {
		t.Fatalf("activity title = %q, want %q", session.Title, "Futurama · S05E06 · Astéroïque")
	}
	if session.Title == episodeID {
		t.Fatalf("activity title exposed title UUID: %q", session.Title)
	}
	if session.ArtworkURL != episodeStill {
		t.Fatalf("activity artwork = %q, want episode artwork %q", session.ArtworkURL, episodeStill)
	}
	if session.Mode != "direct" {
		t.Fatalf("activity mode = %q, want direct", session.Mode)
	}
	if session.ExternalIDs.IMDb != seriesIMDb || session.ExternalIDMediaTypes.IMDb != "series" {
		t.Fatalf(
			"activity IMDb identity = %q/%q, want %q/series",
			session.ExternalIDs.IMDb, session.ExternalIDMediaTypes.IMDb, seriesIMDb,
		)
	}
	if session.ExternalIDs.TMDB != seriesTMDB || session.ExternalIDMediaTypes.TMDB != "series" {
		t.Fatalf(
			"activity TMDB identity = %q/%q, want %q/series",
			session.ExternalIDs.TMDB, session.ExternalIDMediaTypes.TMDB, seriesTMDB,
		)
	}
	if session.ExternalIDs.TVDB != episodeTVDB || session.ExternalIDMediaTypes.TVDB != "episode" {
		t.Fatalf(
			"activity TVDB identity = %q/%q, want %q/episode",
			session.ExternalIDs.TVDB, session.ExternalIDMediaTypes.TVDB, episodeTVDB,
		)
	}
}

func TestFFmpegDiagnosticsReportSlotPressure(t *testing.T) {
	processor := &FFmpegProcessor{slots: make(chan struct{}, 2)}
	processor.slots <- struct{}{}
	if processor.ActiveProcesses() != 1 || processor.ProcessLimit() != 2 {
		t.Fatalf("unexpected processor diagnostics: active=%d limit=%d", processor.ActiveProcesses(), processor.ProcessLimit())
	}
}
