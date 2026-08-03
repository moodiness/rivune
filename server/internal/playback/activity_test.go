package playback

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
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

func TestActivityJobsReportPlaylistProgressAndSpeed(t *testing.T) {
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	directory := t.TempDir()
	playlist := "#EXTM3U\n#EXTINF:4,\nsegment-000000.m4s\n#EXTINF:6,\nsegment-000001.m4s\n"
	if err := os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte(playlist), 0o600); err != nil {
		t.Fatal(err)
	}
	service := &Service{
		now: func() time.Time { return now },
		hlsJobs: map[string]*hlsJob{
			"job": {
				directory: directory, sessionID: "session-1", assetID: "asset-1", mode: processingTranscode,
				sourceDurationSeconds: 30, startOffsetSeconds: 10, createdAt: now.Add(-5 * time.Second), lastAccessed: now,
			},
		},
	}

	jobs := service.activityJobs()
	if len(jobs) != 1 {
		t.Fatalf("activity jobs = %d, want 1", len(jobs))
	}
	if jobs[0].ProgressPercent == nil || *jobs[0].ProgressPercent != 50 {
		t.Fatalf("progress percent = %v, want 50", jobs[0].ProgressPercent)
	}
	if jobs[0].Speed == nil || *jobs[0].Speed != 2 {
		t.Fatalf("speed = %v, want 2", jobs[0].Speed)
	}
}

func TestActivityJobsOmitUnknownProgressAndCapKnownProgress(t *testing.T) {
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	unknownDirectory := t.TempDir()
	missingDirectory := t.TempDir()
	complete := make(chan struct{})
	close(complete)
	cappedDirectory := t.TempDir()
	playlist := []byte("#EXTM3U\n#EXTINF:10,\nsegment-000000.m4s\n")
	for _, directory := range []string{unknownDirectory, cappedDirectory} {
		if err := os.WriteFile(filepath.Join(directory, "index.m3u8"), playlist, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{
		now: func() time.Time { return now },
		hlsJobs: map[string]*hlsJob{
			"unknown": {
				directory: unknownDirectory, assetID: "unknown", createdAt: now.Add(-10 * time.Second),
			},
			"missing": {
				directory: missingDirectory, assetID: "missing", sourceDurationSeconds: 5,
				createdAt: now.Add(-10 * time.Second),
			},
			"capped": {
				directory: cappedDirectory, assetID: "capped", sourceDurationSeconds: 5, done: complete,
				createdAt: now.Add(-10 * time.Second),
			},
		},
	}

	jobs := service.activityJobs()
	if len(jobs) != 3 {
		t.Fatalf("activity jobs = %d, want 3", len(jobs))
	}
	byAssetID := make(map[string]MediaActivityJob, len(jobs))
	for _, job := range jobs {
		byAssetID[job.AssetID] = job
	}
	if byAssetID["unknown"].ProgressPercent != nil {
		t.Fatalf("unknown-duration progress = %v, want nil", byAssetID["unknown"].ProgressPercent)
	}
	if byAssetID["unknown"].Speed == nil || *byAssetID["unknown"].Speed != 1 {
		t.Fatalf("unknown-duration speed = %v, want 1", byAssetID["unknown"].Speed)
	}
	if byAssetID["capped"].ProgressPercent == nil || *byAssetID["capped"].ProgressPercent != 100 {
		t.Fatalf("capped progress = %v, want 100", byAssetID["capped"].ProgressPercent)
	}
	if byAssetID["capped"].State != "complete" {
		t.Fatalf("capped job state = %q, want complete", byAssetID["capped"].State)
	}
	if byAssetID["missing"].ProgressPercent != nil || byAssetID["missing"].Speed != nil {
		t.Fatalf(
			"missing-playlist metrics = %v/%v, want nil/nil",
			byAssetID["missing"].ProgressPercent, byAssetID["missing"].Speed,
		)
	}
	encoded, err := json.Marshal(byAssetID["unknown"])
	if err != nil {
		t.Fatal(err)
	}
	var properties map[string]any
	if err := json.Unmarshal(encoded, &properties); err != nil {
		t.Fatal(err)
	}
	if _, exists := properties["progressPercent"]; exists {
		t.Fatalf("unknown-duration JSON unexpectedly includes progressPercent: %s", encoded)
	}
	encoded, err = json.Marshal(byAssetID["missing"])
	if err != nil {
		t.Fatal(err)
	}
	properties = nil
	if err := json.Unmarshal(encoded, &properties); err != nil {
		t.Fatal(err)
	}
	if _, progressExists := properties["progressPercent"]; progressExists {
		t.Fatalf("missing-playlist JSON unexpectedly includes progressPercent: %s", encoded)
	}
	if _, speedExists := properties["speed"]; speedExists {
		t.Fatalf("missing-playlist JSON unexpectedly includes speed: %s", encoded)
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
	if _, err := tx.Exec(ctx, `
		INSERT INTO profile_progress (
			profile_id, title_id, position_seconds, duration_seconds
		)
		VALUES ($1::uuid, $2::uuid, 605, 1320)
	`, profileID, episodeID); err != nil {
		t.Fatalf("seed playback activity progress: %v", err)
	}

	playbackTokenHash := sha256.Sum256([]byte(playbackSessionID + ":playback"))
	if _, err := tx.Exec(ctx, `
		INSERT INTO playback_sessions (
			id, auth_session_id, profile_id, title_id, media_type, resource_id,
			token_hash, assets, expires_at, last_seen_at
		)
		VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4, 'episode', 'tmdb:episode:1232874',
			$5, '[{"kind":"stream","durationSeconds":1320}]'::jsonb, now() + interval '1 hour', now()
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
	if session.PositionSeconds != 605 || session.DurationSeconds != 1320 {
		t.Fatalf(
			"activity progress = %d/%d, want 605/1320",
			session.PositionSeconds, session.DurationSeconds,
		)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM profile_progress
		WHERE profile_id = $1::uuid AND title_id = $2::uuid
	`, profileID, episodeID); err != nil {
		t.Fatalf("remove playback progress: %v", err)
	}
	activityWithoutProgress, err := service.Activity(ctx, auth.Principal{Role: "admin"})
	if err != nil {
		t.Fatalf("reload playback activity without saved progress: %v", err)
	}
	var sessionWithoutProgress *ActivitySession
	for index := range activityWithoutProgress.Sessions {
		if activityWithoutProgress.Sessions[index].ID == playbackSessionID {
			sessionWithoutProgress = &activityWithoutProgress.Sessions[index]
			break
		}
	}
	if sessionWithoutProgress == nil {
		t.Fatalf("seeded playback session %q was not returned after progress removal", playbackSessionID)
	}
	if sessionWithoutProgress.PositionSeconds != 0 || sessionWithoutProgress.DurationSeconds != 1320 {
		t.Fatalf(
			"activity session fallback = %d/%d, want 0/1320",
			sessionWithoutProgress.PositionSeconds, sessionWithoutProgress.DurationSeconds,
		)
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

func TestActivityDetailsExposePersistedPlaybackDecision(t *testing.T) {
	encoded, err := json.Marshal([]storedAsset{{
		ID: "stream-1", Kind: processingTranscode,
		Decision: &PlaybackDecision{
			Reason: decisionVideoTranscodeRequired, VideoAction: "transcode", AudioAction: "transcode",
			SubtitleAction: "none", ToneMapping: true,
			Source: &PlaybackDecisionSource{Container: "mkv", VideoCodec: "h265", Height: 2160},
			Target: &PlaybackDecisionTarget{Protocol: "hls", Container: "hls", VideoCodec: "h264", Height: 1080, VideoBitrateKbps: 8000},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	mode, decision := activityPlaybackDetails(encoded)
	if mode != processingTranscode || decision == nil || decision.Reason != decisionVideoTranscodeRequired ||
		decision.Source == nil || decision.Source.VideoCodec != "h265" ||
		decision.Target == nil || decision.Target.VideoBitrateKbps != 8000 || !decision.ToneMapping {
		t.Fatalf("activity details lost persisted decision: mode=%q decision=%+v", mode, decision)
	}
}
