package portable

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
	"github.com/moodiness/rivune/server/internal/runtimesettings"
)

func portableTestRuntimeSettings(t *testing.T) *runtimesettings.Source {
	t.Helper()
	source, err := runtimesettings.New(runtimesettings.Values{
		Revision:                     1,
		Timezone:                     "UTC",
		HardwareAcceleration:         runtimesettings.DefaultHardwareAcceleration,
		PreferredTranscodeVideoCodec: runtimesettings.DefaultPreferredTranscodeVideoCodec,
		TranscodeQualityPreset:       runtimesettings.DefaultTranscodeQualityPreset,
		TranscodeConcurrency:         runtimesettings.DefaultTranscodeConcurrency,
		TranscodeMaxBitrateKbps:      runtimesettings.DefaultTranscodeMaxBitrateKbps,
		MediaMaxStorageMB:            runtimesettings.DefaultMediaMaxStorageMB,
		ArtworkMaxStorageMB:          runtimesettings.DefaultArtworkMaxStorageMB,
		AllowTranscoding:             true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func portableTestPrincipal(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID, profileID, categoryID string) auth.Principal {
	t.Helper()
	var deviceID, sessionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1::uuid, $2, 'portable-test', $3::uuid, now())
		RETURNING id::text
	`, userID, "Portable session "+profileID, categoryID).Scan(&deviceID); err != nil {
		t.Fatal(err)
	}
	profileContextHash := sha256.Sum256([]byte("portable-profile-context:" + deviceID + ":" + profileID))
	accessTokenHash := sha256.Sum256([]byte("portable-access-token:" + deviceID + ":" + profileID))
	grantExpiry := time.Now().UTC().Add(time.Hour)
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, authorization_scope, active_profile_id,
			profile_grant_expires_at, profile_context_hash,
			access_token_hash, access_expires_at, refresh_expires_at, last_seen_at
		) VALUES ($1::uuid, $2::uuid, 'global_admin', $3::uuid, $4, $5, $6, $4, $4, now())
		RETURNING id::text
	`, userID, deviceID, profileID, grantExpiry, profileContextHash[:], accessTokenHash[:]).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM devices WHERE id=$1::uuid`, deviceID) })
	return auth.Principal{
		SessionID: sessionID, UserID: userID, DeviceID: deviceID, Role: "admin",
		AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID:    &profileID, ProfileGrantExpiresAt: &grantExpiry,
		ProfileContextHash: profileContextHash[:], ActiveProfileCanManage: true,
	}
}

func TestArchiveRoundTripPreservesEpisodeOrderVariant(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run portable archive integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	basePool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(basePool.Close)
	schema := fmt.Sprintf("portable_episode_order_%d", time.Now().UnixNano())
	if _, err := basePool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = basePool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO instances(id,name) VALUES(1,'Portable episode-order test'); INSERT INTO instance_settings(instance_id,schema_version,settings) VALUES(1,3,'{}')`); err != nil {
		t.Fatal(err)
	}

	const (
		seriesID           = "96000000-0000-4000-8000-000000000001"
		canonicalSeasonID  = "96000000-0000-4000-8000-000000000002"
		canonicalEpisodeID = "96000000-0000-4000-8000-000000000003"
		variantSeasonID    = "96000000-0000-4000-8000-000000000004"
		variantEpisodeID   = "96000000-0000-4000-8000-000000000005"
	)
	var categoryID, userID, sourceProfileID, targetProfileID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_categories WHERE is_default`).Scan(&categoryID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role) VALUES('portable-episode-order','test','admin') RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO profiles(name,category_id) VALUES('Episode order source',$1::uuid) RETURNING id::text`, categoryID).Scan(&sourceProfileID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO profiles(name,category_id) VALUES('Episode order target',$1::uuid) RETURNING id::text`, categoryID).Scan(&targetProfileID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO profile_settings(profile_id,schema_version,settings) VALUES($1::uuid,1,'{}'),($2::uuid,1,'{}')`, sourceProfileID, targetProfileID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_profile_access(user_id,profile_id,can_manage) VALUES($1::uuid,$2::uuid,true),($1::uuid,$3::uuid,true)`, userID, sourceProfileID, targetProfileID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles(id,media_type,parent_id,ordinal,hierarchy_variant,display_title,resource_id,resource_provider) VALUES
			($1::uuid,'series',NULL,NULL,'','Archive Series','404604','tvdb'),
			($2::uuid,'season',$1::uuid,1,'','Aired Season 1','871838','tvdb'),
			($3::uuid,'episode',$2::uuid,1,'','Aired Episode 1','7954418','tvdb'),
			($4::uuid,'season',$1::uuid,1,'tvdb:2','DVD Season 1','871838','tvdb'),
			($5::uuid,'episode',$4::uuid,1,'tvdb:2','DVD Episode 1','tvdb:10357450','tvdb')
	`, seriesID, canonicalSeasonID, canonicalEpisodeID, variantSeasonID, variantEpisodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO title_external_ids(title_id,provider,namespace,external_id) VALUES
			($1::uuid,'tvdb','series','404604'),
			($2::uuid,'tvdb','season','871838'),
			($3::uuid,'tvdb','episode','7954418')
	`, seriesID, canonicalSeasonID, canonicalEpisodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO title_episode_order_identities(title_id,series_title_id,provider,order_id,namespace,external_id) VALUES
			($1::uuid,$2::uuid,'tvdb','2','season','871838'),
			($3::uuid,$2::uuid,'tvdb','2','episode','10357450')
	`, variantSeasonID, seriesID, variantEpisodeID); err != nil {
		t.Fatal(err)
	}
	stateTime := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `
		INSERT INTO profile_progress(profile_id,title_id,position_seconds,duration_seconds,completed,version,last_watched_at,updated_at) VALUES
			($1::uuid,$2::uuid,120,600,false,1,$4,$4),
			($1::uuid,$3::uuid,240,600,false,1,$4,$4)
	`, sourceProfileID, canonicalEpisodeID, variantEpisodeID, stateTime); err != nil {
		t.Fatal(err)
	}

	principal := portableTestPrincipal(t, ctx, pool, userID, sourceProfileID, categoryID)
	service := NewService(pool, portableTestRuntimeSettings(t))
	document, err := service.Export(ctx, principal, sourceProfileID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var archive struct {
		Titles []map[string]any `json:"titles"`
	}
	if err := json.Unmarshal(encoded, &archive); err != nil {
		t.Fatal(err)
	}
	titleByKey := make(map[string]map[string]any, len(archive.Titles))
	for _, title := range archive.Titles {
		titleByKey[title["key"].(string)] = title
	}
	seriesKey := portableKey("title", seriesID)
	for _, fixture := range []struct {
		id, variant, namespace, externalID string
	}{
		{variantSeasonID, "tvdb:2", "season", "871838"},
		{variantEpisodeID, "tvdb:2", "episode", "10357450"},
	} {
		title := titleByKey[portableKey("title", fixture.id)]
		if title["hierarchyVariant"] != fixture.variant {
			t.Fatalf("exported %s hierarchyVariant = %v, want %q", fixture.namespace, title["hierarchyVariant"], fixture.variant)
		}
		identity, ok := title["episodeOrderIdentity"].(map[string]any)
		if !ok || identity["seriesKey"] != seriesKey || identity["provider"] != "tvdb" || identity["orderId"] != "2" || identity["namespace"] != fixture.namespace || identity["externalId"] != fixture.externalID {
			t.Fatalf("exported %s episode-order identity = %#v", fixture.namespace, title["episodeOrderIdentity"])
		}
	}
	var importedDocument Document
	if err := json.Unmarshal(encoded, &importedDocument); err != nil {
		t.Fatalf("decode exported archive before clean import: %v", err)
	}
	if _, err := service.Import(ctx, principal, targetProfileID, importedDocument); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `
		SELECT episode.id::text,episode.hierarchy_variant,season.hierarchy_variant,progress.position_seconds,series.id::text
		FROM profile_progress progress
		JOIN titles episode ON episode.id=progress.title_id
		JOIN titles season ON season.id=episode.parent_id
		JOIN titles series ON series.id=season.parent_id
		WHERE progress.profile_id=$1::uuid
		ORDER BY progress.position_seconds
	`, targetProfileID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type importedProgress struct {
		id, episodeVariant, seasonVariant, seriesID string
		position                                     int
	}
	var imported []importedProgress
	for rows.Next() {
		var value importedProgress
		if err := rows.Scan(&value.id, &value.episodeVariant, &value.seasonVariant, &value.position, &value.seriesID); err != nil {
			t.Fatal(err)
		}
		imported = append(imported, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []importedProgress{
		{id: canonicalEpisodeID, episodeVariant: "", seasonVariant: "", seriesID: seriesID, position: 120},
		{id: variantEpisodeID, episodeVariant: "tvdb:2", seasonVariant: "tvdb:2", seriesID: seriesID, position: 240},
	}
	if len(imported) != len(want) || imported[0] != want[0] || imported[1] != want[1] {
		t.Fatalf("imported progress = %+v, want %+v", imported, want)
	}
}

type identityClassSnapshot struct {
	boundTitleID, mediaType, parentID, hierarchyVariant, canonicalExternalIDs, scopedExternalIDs, episodeOrderIdentity string
	ordinal, progressPosition, progressDuration                                                                     int
	progressVersion                                                                                                  int64
	progressCompleted                                                                                                bool
	bindingUpdatedAt, progressLastWatchedAt, progressUpdatedAt                                                       time.Time
}

func portableIdentityClassTestService(t *testing.T) (context.Context, *Service, auth.Principal, string) {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run portable archive integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	basePool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(basePool.Close)
	schema := fmt.Sprintf("portable_identity_class_%d", time.Now().UnixNano())
	if _, err := basePool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = basePool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO instances(id,name) VALUES(1,'Portable identity-class test'); INSERT INTO instance_settings(instance_id,schema_version,settings) VALUES(1,3,'{}')`); err != nil {
		t.Fatal(err)
	}
	var categoryID, userID, profileID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_categories WHERE is_default`).Scan(&categoryID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role) VALUES('portable-identity-class','test','admin') RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO profiles(name,category_id) VALUES('Identity class',$1::uuid) RETURNING id::text`, categoryID).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO profile_settings(profile_id,schema_version,settings) VALUES($1::uuid,1,'{}')`, profileID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_profile_access(user_id,profile_id,can_manage) VALUES($1::uuid,$2::uuid,true)`, userID, profileID); err != nil {
		t.Fatal(err)
	}
	principal := portableTestPrincipal(t, ctx, pool, userID, profileID, categoryID)
	return ctx, NewService(pool, portableTestRuntimeSettings(t)), principal, profileID
}

func identityClassDocument(now time.Time, variantEpisode bool) Document {
	seriesKey := "sha256:" + strings.Repeat("a", 64)
	canonicalSeasonKey := "sha256:" + strings.Repeat("b", 64)
	variantSeasonKey := "sha256:" + strings.Repeat("c", 64)
	episodeKey := "sha256:" + strings.Repeat("d", 64)
	seasonOrdinal, episodeOrdinal := 1, 1
	episode := Title{
		Key: episodeKey, MediaType: "episode", ParentKey: canonicalSeasonKey, Ordinal: &episodeOrdinal,
		ExternalIDs: []ExternalID{{Provider: "tvdb", Namespace: "episode", ExternalID: "7954418"}},
	}
	position := 100
	if variantEpisode {
		episode.ParentKey = variantSeasonKey
		episode.HierarchyVariant = "tvdb:2"
		episode.EpisodeOrderIdentity = &EpisodeOrderIdentity{SeriesKey: seriesKey, Provider: "tvdb", OrderID: "2", Namespace: "episode", ExternalID: "10357450"}
		episode.ExternalIDs = []ExternalID{}
		position = 200
	}
	return Document{
		Version: DocumentVersion, ExportedAt: now,
		Identity: Identity{Name: "Identity class", Avatar: Avatar{Kind: "preset", PresetID: "aurora"}},
		Addons: []Addon{}, Collections: []PortableCollection{},
		Titles: []Title{
			{Key: seriesKey, MediaType: "series", ExternalIDs: []ExternalID{{Provider: "tvdb", Namespace: "series", ExternalID: "404604"}}},
			{Key: canonicalSeasonKey, MediaType: "season", ParentKey: seriesKey, Ordinal: &seasonOrdinal, ExternalIDs: []ExternalID{{Provider: "tvdb", Namespace: "season", ExternalID: "871838"}}},
			{
				Key: variantSeasonKey, MediaType: "season", ParentKey: seriesKey, Ordinal: &seasonOrdinal, HierarchyVariant: "tvdb:2",
				EpisodeOrderIdentity: &EpisodeOrderIdentity{SeriesKey: seriesKey, Provider: "tvdb", OrderID: "2", Namespace: "season", ExternalID: "871838"},
				ExternalIDs:          []ExternalID{},
			},
			episode,
		},
		Library: []LibraryState{},
		Progress: []ProgressState{{TitleKey: episodeKey, PositionSeconds: position, DurationSeconds: 600, Version: 1, LastWatchedAt: now, UpdatedAt: now}},
		Favorites: []FavoriteState{}, UserData: []UserDataState{}, ContinueDismissals: []ContinueDismissal{}, TrackingPreferences: []TrackingPreference{},
	}
}

func readIdentityClassSnapshot(t *testing.T, ctx context.Context, service *Service, profileID, key string) identityClassSnapshot {
	t.Helper()
	var snapshot identityClassSnapshot
	err := service.pool.QueryRow(ctx, `
		SELECT binding.resource_id::text,title.media_type,COALESCE(title.parent_id::text,''),COALESCE(title.ordinal,-1),title.hierarchy_variant,
		       COALESCE((SELECT string_agg(provider || ':' || namespace || ':' || external_id, ',' ORDER BY provider,namespace,external_id)
		                 FROM title_external_ids WHERE title_id=title.id),''),
		       COALESCE((SELECT profile_id::text || ':' || provider || ':' || namespace || ':' || external_id
		                 FROM profile_title_external_ids WHERE title_id=title.id),''),
		       COALESCE((SELECT series_title_id::text || ':' || provider || ':' || order_id || ':' || namespace || ':' || external_id
		                 FROM title_episode_order_identities WHERE title_id=title.id),''),
		       binding.updated_at,progress.position_seconds,progress.duration_seconds,progress.completed,
		       progress.version,progress.last_watched_at,progress.updated_at
		FROM portable_profile_resource_bindings binding
		JOIN titles title ON title.id=binding.resource_id
		JOIN profile_progress progress ON progress.profile_id=binding.profile_id AND progress.title_id=title.id
		WHERE binding.profile_id=$1::uuid AND binding.resource_kind='title' AND binding.portable_key=$2
	`, profileID, key).Scan(
		&snapshot.boundTitleID, &snapshot.mediaType, &snapshot.parentID, &snapshot.ordinal, &snapshot.hierarchyVariant,
		&snapshot.canonicalExternalIDs, &snapshot.scopedExternalIDs, &snapshot.episodeOrderIdentity,
		&snapshot.bindingUpdatedAt, &snapshot.progressPosition, &snapshot.progressDuration, &snapshot.progressCompleted,
		&snapshot.progressVersion, &snapshot.progressLastWatchedAt, &snapshot.progressUpdatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestImportRejectsEpisodeOrderIdentityClassChanges(t *testing.T) {
	for _, test := range []struct {
		name, initialClass string
		initialVariant     bool
	}{
		{name: "canonical to variant", initialClass: "canonical"},
		{name: "variant to canonical", initialClass: "variant", initialVariant: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, service, principal, profileID := portableIdentityClassTestService(t)
			now := time.Now().UTC().Truncate(time.Microsecond)
			initial := identityClassDocument(now, test.initialVariant)
			if _, err := service.Import(ctx, principal, profileID, initial); err != nil {
				t.Fatalf("initial %s import: %v", test.initialClass, err)
			}
			episodeKey := "sha256:" + strings.Repeat("d", 64)
			before := readIdentityClassSnapshot(t, ctx, service, profileID, episodeKey)
			if test.initialVariant {
				if before.hierarchyVariant != "tvdb:2" || before.canonicalExternalIDs != "" || before.episodeOrderIdentity == "" {
					t.Fatalf("initial variant snapshot = %+v", before)
				}
			} else if before.hierarchyVariant != "" || before.canonicalExternalIDs == "" || before.episodeOrderIdentity != "" {
				t.Fatalf("initial canonical snapshot = %+v", before)
			}

			reclassified := identityClassDocument(now.Add(time.Minute), !test.initialVariant)
			if _, err := service.Import(ctx, principal, profileID, reclassified); !errors.Is(err, ErrConflict) {
				t.Fatalf("%s reclassification error = %v, want ErrConflict", test.name, err)
			}
			after := readIdentityClassSnapshot(t, ctx, service, profileID, episodeKey)
			if after != before {
				t.Fatalf("%s changed bound title/state:\nbefore=%+v\nafter=%+v", test.name, before, after)
			}
		})
	}
}

func TestImportRejectsScopedIdentityOnEpisodeOrderVariant(t *testing.T) {
	for _, test := range []struct {
		name   string
		legacy bool
	}{
		{name: "exact order identity"},
		{name: "identity-less legacy variant", legacy: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, service, principal, profileID := portableIdentityClassTestService(t)
			now := time.Now().UTC().Truncate(time.Microsecond)
			document := identityClassDocument(now, true)
			if _, err := service.Import(ctx, principal, profileID, document); err != nil {
				t.Fatalf("initial variant import: %v", err)
			}
			episodeKey := "sha256:" + strings.Repeat("d", 64)
			initial := readIdentityClassSnapshot(t, ctx, service, profileID, episodeKey)
			if test.legacy {
				if _, err := service.pool.Exec(ctx, `DELETE FROM title_episode_order_identities WHERE title_id=$1::uuid`, initial.boundTitleID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := service.pool.Exec(ctx, `
				INSERT INTO profile_title_external_ids(profile_id,title_id,provider,namespace,external_id)
				VALUES($1::uuid,$2::uuid,'addon','episode','scoped-variant')
			`, profileID, initial.boundTitleID); err != nil {
				t.Fatal(err)
			}
			before := readIdentityClassSnapshot(t, ctx, service, profileID, episodeKey)
			if before.scopedExternalIDs == "" || test.legacy == (before.episodeOrderIdentity != "") {
				t.Fatalf("invalid %s precondition: %+v", test.name, before)
			}

			document.ExportedAt = now.Add(time.Minute)
			if _, err := service.Import(ctx, principal, profileID, document); !errors.Is(err, ErrConflict) {
				t.Fatalf("variant import with scoped identity error = %v, want ErrConflict", err)
			}
			after := readIdentityClassSnapshot(t, ctx, service, profileID, episodeKey)
			if after != before {
				t.Fatalf("variant import changed scoped bound title/state:\nbefore=%+v\nafter=%+v", before, after)
			}
		})
	}
}

func TestArchiveRoundTripIdempotenceSecretExclusionAndRollback(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run portable archive integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO instances(id,name) VALUES(1,'Portable test') ON CONFLICT(id) DO NOTHING; INSERT INTO instance_settings(instance_id,schema_version,settings) VALUES(1,3,'{}') ON CONFLICT(instance_id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var categoryID, userID, sourceProfileID, targetProfileID, titleID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_categories WHERE is_default`).Scan(&categoryID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role) VALUES($1,'not-exported-password-hash','admin') RETURNING id::text`, "portable-"+suffix).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO profiles(name,category_id,pin_hash) VALUES($1,$2::uuid,'not-exported-pin-hash') RETURNING id::text`, "Source "+suffix, categoryID).Scan(&sourceProfileID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO profiles(name,category_id,pin_hash) VALUES($1,$2::uuid,'target-pin-must-survive') RETURNING id::text`, "Target "+suffix, categoryID).Scan(&targetProfileID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id=ANY($1::uuid[])`, []string{sourceProfileID, targetProfileID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1::uuid`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM titles WHERE id=$1::uuid`, titleID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO profile_settings(profile_id,schema_version,settings) VALUES($1::uuid,1,'{"theme":"dark"}'),($2::uuid,1,'{"theme":"light"}')`, sourceProfileID, targetProfileID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_profile_access(user_id,profile_id,can_manage) VALUES($1::uuid,$2::uuid,true),($1::uuid,$3::uuid,true)`, userID, sourceProfileID, targetProfileID); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"org.example.portable","version":"1.0.0","name":"Portable","types":["movie"],"resources":["catalog","meta"],"catalogs":[{"type":"movie","id":"featured"}]}`
	secretURL := "https://addon.example/private/manifest.json?token=portable-secret"
	var addonID string
	if err := pool.QueryRow(ctx, `INSERT INTO profile_addons(profile_id,transport_url,manifest,manifest_id,manifest_version,position,enabled) VALUES($1::uuid,$2,$3::jsonb,'org.example.portable','1.0.0',0,true) RETURNING id::text`, sourceProfileID, secretURL, manifest).Scan(&addonID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO addon_profile_access(addon_id,profile_id,position) VALUES($1::uuid,$2::uuid,0)`, addonID, sourceProfileID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO addon_profile_order(addon_id,profile_id,position) VALUES($1::uuid,$2::uuid,0)`, addonID, sourceProfileID); err != nil {
		t.Fatal(err)
	}
	folders := fmt.Sprintf(`[{"id":"11111111-1111-4111-8111-111111111111","title":"Featured","tileShape":"square","sourceView":"merged","focusGifEnabled":false,"hideTitle":false,"sources":[{"id":"22222222-2222-4222-8222-222222222222","kind":"addon_catalog","title":"Movies","addonCatalog":{"addonId":"%s","type":"movie","catalogId":"featured"}}]}]`, addonID)
	var collectionID string
	if err := pool.QueryRow(ctx, `INSERT INTO profile_collections(profile_id,title,hero_enabled,pin_to_top,focus_glow_enabled,view_mode,folder_cover_shape,folders,position) VALUES($1::uuid,'Featured',false,true,true,'rows','square',$2::jsonb,0) RETURNING id::text`, sourceProfileID, folders).Scan(&collectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO collection_profile_access(collection_id,profile_id,position) VALUES($1::uuid,$2::uuid,0)`, collectionID, sourceProfileID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO collection_profile_order(collection_id,profile_id,position) VALUES($1::uuid,$2::uuid,0)`, collectionID, sourceProfileID); err != nil {
		t.Fatal(err)
	}
	externalID := "portable-" + suffix
	if err := pool.QueryRow(ctx, `INSERT INTO titles(media_type,display_title,resource_id,resource_provider,source_addon_id,source_catalog_id) VALUES('movie','Portable Movie',$1,'tmdb',$2::uuid,'featured') RETURNING id::text`, externalID, addonID).Scan(&titleID); err != nil {
		t.Fatal(err)
	}
	stateTime := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `INSERT INTO title_external_ids(title_id,provider,namespace,external_id) VALUES($1::uuid,'tmdb','movie',$2)`, titleID, externalID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO profile_library(profile_id,title_id,added_at,updated_at) VALUES($1::uuid,$2::uuid,$3,$3)`,
		`INSERT INTO profile_progress(profile_id,title_id,position_seconds,duration_seconds,completed,version,last_watched_at,updated_at) VALUES($1::uuid,$2::uuid,120,600,false,3,$3,$3)`,
		`INSERT INTO profile_favorites(profile_id,title_id,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3,$3)`,
		`INSERT INTO profile_user_data(profile_id,title_id,rating,rating_set,play_count,play_count_set,updated_at) VALUES($1::uuid,$2::uuid,8.5,true,2,true,$3)`,
	} {
		if _, err := pool.Exec(ctx, statement, sourceProfileID, titleID, stateTime); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO profile_tracking_preferences(profile_id,provider,sync_watched,sync_progress,sync_library) VALUES($1::uuid,'trakt',true,false,true)`, sourceProfileID); err != nil {
		t.Fatal(err)
	}
	principal := portableTestPrincipal(t, ctx, pool, userID, sourceProfileID, categoryID)
	service := NewService(pool, portableTestRuntimeSettings(t))
	document, err := service.Export(ctx, principal, sourceProfileID)
	if err != nil {
		t.Fatal(err)
	}
	var sameProfileFirst, sameProfileSecond ImportReport
	for attempt := range 2 {
		report, err := service.Import(ctx, principal, sourceProfileID, document)
		if err != nil {
			t.Fatalf("same-profile import attempt %d: %v", attempt+1, err)
		}
		if attempt == 0 {
			sameProfileFirst = report
		} else {
			sameProfileSecond = report
		}
	}
	for _, section := range sameProfileFirst.Sections {
		if section.Created != 0 || section.Updated != 0 {
			t.Fatalf("same-profile adoption report was not unchanged: %+v", section)
		}
	}
	for _, section := range sameProfileSecond.Sections {
		if section.Created != 0 || section.Updated != 0 {
			t.Fatalf("repeat same-profile import section changed: %+v", section)
		}
	}
	var sourceCollections, sourceCollectionBindings int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM collection_profile_access WHERE profile_id=$1::uuid),(SELECT count(*) FROM portable_profile_resource_bindings WHERE profile_id=$1::uuid AND resource_kind='collection')`, sourceProfileID).Scan(&sourceCollections, &sourceCollectionBindings); err != nil {
		t.Fatal(err)
	}
	if sourceCollections != 1 || sourceCollectionBindings != 1 {
		t.Fatalf("same-profile import duplicated collection: access=%d bindings=%d", sourceCollections, sourceCollectionBindings)
	}
	encoded, _ := json.Marshal(document)
	archive := string(encoded)
	for _, forbidden := range []string{sourceProfileID, addonID, collectionID, titleID, "not-exported-password-hash", "not-exported-pin-hash", "categoryIds", "profileIds", "accessToken", "refreshToken"} {
		if strings.Contains(archive, forbidden) {
			t.Fatalf("archive leaked forbidden value %q: %s", forbidden, archive)
		}
	}
	if strings.Count(archive, "portable-secret") != 1 {
		t.Fatalf("explicit add-on secret occurrence count = %d", strings.Count(archive, "portable-secret"))
	}
	var firstReport, secondReport ImportReport
	for attempt := range 2 {
		report, err := service.Import(ctx, principal, targetProfileID, document)
		if err != nil {
			t.Fatalf("import attempt %d: %v", attempt+1, err)
		}
		if attempt == 0 {
			firstReport = report
		} else {
			secondReport = report
		}
	}
	for _, section := range firstReport.Sections {
		switch section.Section {
		case "library", "progress", "favorites", "userData", "trackingPreferences":
			if section.Created != 1 || section.Updated != 0 || section.Unchanged != 0 {
				t.Fatalf("first import section %+v was not created", section)
			}
		}
	}
	for _, section := range secondReport.Sections {
		if section.Created != 0 || section.Updated != 0 {
			t.Fatalf("second import section %+v was not unchanged; first=%+v", section, firstReport.Sections)
		}
	}
	encodedAfterImport, _ := json.Marshal(document)
	if string(encodedAfterImport) != archive {
		t.Fatal("Import mutated its Document input")
	}
	var addons, collections, library, progress, favorites, userData, preferences int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM addon_profile_access WHERE profile_id=$1::uuid),(SELECT count(*) FROM collection_profile_access WHERE profile_id=$1::uuid),(SELECT count(*) FROM profile_library WHERE profile_id=$1::uuid),(SELECT count(*) FROM profile_progress WHERE profile_id=$1::uuid),(SELECT count(*) FROM profile_favorites WHERE profile_id=$1::uuid),(SELECT count(*) FROM profile_user_data WHERE profile_id=$1::uuid),(SELECT count(*) FROM profile_tracking_preferences WHERE profile_id=$1::uuid)`, targetProfileID).Scan(&addons, &collections, &library, &progress, &favorites, &userData, &preferences); err != nil {
		t.Fatal(err)
	}
	if addons != 1 || collections != 1 || library != 1 || progress != 1 || favorites != 1 || userData != 1 || preferences != 1 {
		t.Fatalf("non-idempotent target counts: addons=%d collections=%d library=%d progress=%d favorites=%d userData=%d preferences=%d", addons, collections, library, progress, favorites, userData, preferences)
	}
	var adoptedAddonID string
	if err := pool.QueryRow(ctx, `SELECT addon_id::text FROM addon_profile_access WHERE profile_id=$1::uuid`, targetProfileID).Scan(&adoptedAddonID); err != nil {
		t.Fatal(err)
	}
	if adoptedAddonID != addonID {
		t.Fatalf("exact add-on adoption id=%s want=%s", adoptedAddonID, addonID)
	}
	var targetPIN, targetTheme string
	if err := pool.QueryRow(ctx, `SELECT pin_hash,(SELECT settings->>'theme' FROM profile_settings WHERE profile_id=profiles.id) FROM profiles WHERE id=$1::uuid`, targetProfileID).Scan(&targetPIN, &targetTheme); err != nil {
		t.Fatal(err)
	}
	if targetPIN != "target-pin-must-survive" || targetTheme != "dark" {
		t.Fatalf("target identity/security changed: pin=%q theme=%q", targetPIN, targetTheme)
	}
	var conflictingTitleID string
	targetDocument, err := service.Export(ctx, principal, targetProfileID)
	if err != nil {
		t.Fatal(err)
	}
	targetDocument.ExportedAt = document.ExportedAt
	targetArchive, _ := json.Marshal(targetDocument)
	if string(targetArchive) != archive {
		t.Fatalf("round-trip archive mismatch:\nsource=%s\ntarget=%s", archive, targetArchive)
	}
	newerStateTime := stateTime.Add(time.Hour)
	if _, err := pool.Exec(ctx, `UPDATE profile_progress SET position_seconds=500,duration_seconds=700,completed=true,version=9,last_watched_at=$3,updated_at=$3 WHERE profile_id=$1::uuid AND title_id=$2::uuid`, targetProfileID, titleID, newerStateTime); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE profile_user_data SET rating=1,rating_set=true,play_count=7,play_count_set=true,updated_at=$3 WHERE profile_id=$1::uuid AND title_id=$2::uuid`, targetProfileID, titleID, newerStateTime); err != nil {
		t.Fatal(err)
	}
	olderReport, err := service.Import(ctx, principal, targetProfileID, document)
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range olderReport.Sections {
		if (section.Section == "progress" || section.Section == "userData") && section.Unchanged != 1 {
			t.Fatalf("older state report = %+v", section)
		}
	}
	var position, playCount int
	var rating float64
	if err := pool.QueryRow(ctx, `SELECT progress.position_seconds,data.rating,data.play_count FROM profile_progress progress JOIN profile_user_data data USING(profile_id,title_id) WHERE progress.profile_id=$1::uuid AND progress.title_id=$2::uuid`, targetProfileID, titleID).Scan(&position, &rating, &playCount); err != nil {
		t.Fatal(err)
	}
	if position != 500 || rating != 1 || playCount != 7 {
		t.Fatalf("older archive overwrote target state: position=%d rating=%v playCount=%d", position, rating, playCount)
	}
	if _, err := pool.Exec(ctx, `UPDATE profile_progress SET position_seconds=400,version=9,last_watched_at=$3,updated_at=$3 WHERE profile_id=$1::uuid AND title_id=$2::uuid`, targetProfileID, titleID, stateTime.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Import(ctx, principal, targetProfileID, document); err != nil {
		t.Fatal(err)
	}
	var progressVersion int64
	if err := pool.QueryRow(ctx, `SELECT position_seconds,version FROM profile_progress WHERE profile_id=$1::uuid AND title_id=$2::uuid`, targetProfileID, titleID).Scan(&position, &progressVersion); err != nil {
		t.Fatal(err)
	}
	if position != 120 || progressVersion != 9 {
		t.Fatalf("newer archive progress merge position=%d version=%d", position, progressVersion)
	}
	document.Progress[0].Version = 12
	document.Progress[0].LastWatchedAt = stateTime.Add(-2 * time.Hour)
	document.Progress[0].UpdatedAt = stateTime.Add(-2 * time.Hour)
	document.Progress[0].PositionSeconds = 1
	versionOnlyReport, err := service.Import(ctx, principal, targetProfileID, document)
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range versionOnlyReport.Sections {
		if section.Section == "progress" && section.Updated != 1 {
			t.Fatalf("version-only progress report = %+v", section)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT position_seconds,version FROM profile_progress WHERE profile_id=$1::uuid AND title_id=$2::uuid`, targetProfileID, titleID).Scan(&position, &progressVersion); err != nil {
		t.Fatal(err)
	}
	if position != 120 || progressVersion != 12 {
		t.Fatalf("version-only archive progress merge position=%d version=%d", position, progressVersion)
	}
	document.Progress[0].Version = 3
	document.Progress[0].LastWatchedAt = stateTime
	document.Progress[0].UpdatedAt = stateTime
	document.Progress[0].PositionSeconds = 120
	isolationDocument := document
	isolationDocument.Titles = append([]Title(nil), document.Titles...)
	isolationDocument.Titles[0].DisplayTitle = "Archive Must Not Rename Canonical"
	isolationDocument.Titles[0].ExternalIDs = append(append([]ExternalID(nil), document.Titles[0].ExternalIDs...), ExternalID{Provider: "addon", Namespace: "movie", ExternalID: "scoped-" + suffix, ProfileScoped: true})
	if _, err := service.Import(ctx, principal, targetProfileID, isolationDocument); err != nil {
		t.Fatal(err)
	}
	var canonicalTitle, canonicalSourceAddonID string
	var scopedIdentities int
	if err := pool.QueryRow(ctx, `SELECT display_title,source_addon_id::text,(SELECT count(*) FROM profile_title_external_ids WHERE profile_id=$2::uuid AND title_id=titles.id) FROM titles WHERE id=$1::uuid`, titleID, targetProfileID).Scan(&canonicalTitle, &canonicalSourceAddonID, &scopedIdentities); err != nil {
		t.Fatal(err)
	}
	if canonicalTitle != "Portable Movie" || canonicalSourceAddonID != addonID || scopedIdentities != 0 {
		t.Fatalf("canonical title was mutated/restricted: title=%q addon=%q scoped=%d", canonicalTitle, canonicalSourceAddonID, scopedIdentities)
	}
	addonConflict := document
	addonConflict.Addons = append([]Addon(nil), document.Addons...)
	addonConflict.Addons[0].Enabled = false
	if _, err := service.Import(ctx, principal, targetProfileID, addonConflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("shared add-on mutation error = %v", err)
	}
	var targetCollectionID string
	if err := pool.QueryRow(ctx, `SELECT collection_id::text FROM collection_profile_access WHERE profile_id=$1::uuid`, targetProfileID).Scan(&targetCollectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO collection_category_access(collection_id,category_id,position) VALUES($1::uuid,$2::uuid,(SELECT COALESCE(max(position)+1,0) FROM collection_category_access WHERE category_id=$2::uuid))`, targetCollectionID, categoryID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM collection_category_access WHERE collection_id=$1::uuid AND category_id=$2::uuid`, targetCollectionID, categoryID)
	}()
	collectionConflict := document
	collectionConflict.Collections = append([]PortableCollection(nil), document.Collections...)
	collectionConflict.Collections[0].Value.Title = "Shared Must Not Change"
	if _, err := service.Import(ctx, principal, targetProfileID, collectionConflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("shared collection mutation error = %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO titles(media_type,display_title) VALUES('movie','Conflict') RETURNING id::text`).Scan(&conflictingTitleID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM titles WHERE id=$1::uuid`, conflictingTitleID)
	})
	conflictingExternalID := "conflict-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO title_external_ids(title_id,provider,namespace,external_id) VALUES($1::uuid,'imdb','movie',$2)`, conflictingTitleID, conflictingExternalID); err != nil {
		t.Fatal(err)
	}
	light := "light"
	document.Settings.Theme = &light
	document.Titles[0].ExternalIDs = append(document.Titles[0].ExternalIDs, ExternalID{Provider: "imdb", Namespace: "movie", ExternalID: conflictingExternalID})
	if _, err := service.Import(ctx, principal, targetProfileID, document); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting import error = %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT settings->>'theme' FROM profile_settings WHERE profile_id=$1::uuid`, targetProfileID).Scan(&targetTheme); err != nil || targetTheme != "dark" {
		t.Fatalf("failed import was not rolled back: theme=%q err=%v", targetTheme, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1::uuid`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Export(ctx, principal, sourceProfileID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-global export error = %v", err)
	}
}

func TestConcurrentSameProfileImportDoesNotDuplicateResources(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run portable archive integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO instances(id,name) VALUES(1,'Portable test') ON CONFLICT(id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO instance_settings(instance_id,schema_version,settings) VALUES(1,3,'{}') ON CONFLICT(instance_id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	var categoryID, userID, profileID string
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_categories WHERE is_default`).Scan(&categoryID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role) VALUES($1,'x','admin') RETURNING id::text`, "portable-concurrent-"+suffix).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO profiles(name,category_id) VALUES($1,$2::uuid) RETURNING id::text`, "Concurrent "+suffix, categoryID).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id=$1::uuid`, profileID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1::uuid`, userID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO profile_settings(profile_id,schema_version,settings) VALUES($1::uuid,1,'{}')`, profileID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_profile_access(user_id,profile_id,can_manage) VALUES($1::uuid,$2::uuid,true)`, userID, profileID); err != nil {
		t.Fatal(err)
	}
	document := validDocument()
	second := document.Addons[0]
	second.Key = "sha256:" + strings.Repeat("b", 64)
	second.TransportURL = "https://second.example/manifest.json"
	document.Addons[0].Position = 9
	second.Position = 2
	document.Addons = []Addon{document.Addons[0], second}
	principal := portableTestPrincipal(t, ctx, pool, userID, profileID, categoryID)
	service := NewService(pool, portableTestRuntimeSettings(t))
	var group sync.WaitGroup
	errors := make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := service.Import(ctx, principal, profileID, document)
			errors <- err
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent import: %v", err)
		}
	}
	var addons, bindings int
	var orderedURLs []string
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM addon_profile_access WHERE profile_id=$1::uuid),(SELECT count(*) FROM portable_profile_resource_bindings WHERE profile_id=$1::uuid AND resource_kind='addon'),ARRAY(SELECT addon.transport_url FROM addon_profile_access access JOIN profile_addons addon ON addon.id=access.addon_id WHERE access.profile_id=$1::uuid ORDER BY access.position)`, profileID).Scan(&addons, &bindings, &orderedURLs); err != nil {
		t.Fatal(err)
	}
	if addons != 2 || bindings != 2 || len(orderedURLs) != 2 || orderedURLs[0] != second.TransportURL {
		t.Fatalf("concurrent/position result addons=%d bindings=%d urls=%v", addons, bindings, orderedURLs)
	}
}

func TestPortableImportRejectsAsymmetricProvenancePreservesTrackingAndSupportsScopedTV(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run portable archive integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO instances(id,name) VALUES(1,'Portable blockers') ON CONFLICT(id) DO NOTHING; INSERT INTO instance_settings(instance_id,schema_version,settings) VALUES(1,3,'{}') ON CONFLICT(instance_id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	var categoryID, userID, profileID, addonID, titleID string
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_categories WHERE is_default`).Scan(&categoryID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role) VALUES($1,'x','admin') RETURNING id::text`, "portable-blockers-"+suffix).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO profiles(name,category_id) VALUES($1,$2::uuid) RETURNING id::text`, "Blockers "+suffix, categoryID).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id=$1::uuid`, profileID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1::uuid`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM titles WHERE id=$1::uuid`, titleID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM profile_addons WHERE id=$1::uuid`, addonID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO profile_settings(profile_id,schema_version,settings) VALUES($1::uuid,1,'{}')`, profileID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_profile_access(user_id,profile_id,can_manage) VALUES($1::uuid,$2::uuid,true)`, userID, profileID); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"org.example.foreign","version":"1.0.0","name":"Foreign","types":["movie"],"resources":["meta"],"catalogs":[]}`
	if err := pool.QueryRow(ctx, `INSERT INTO profile_addons(profile_id,transport_url,manifest,manifest_id,manifest_version,position) VALUES($1::uuid,'https://foreign.example/manifest.json',$2::jsonb,'org.example.foreign','1.0.0',0) RETURNING id::text`, profileID, manifest).Scan(&addonID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO titles(media_type,display_title,source_addon_id) VALUES('movie','Foreign',$1::uuid) RETURNING id::text`, addonID).Scan(&titleID); err != nil {
		t.Fatal(err)
	}
	externalID := "foreign-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO title_external_ids(title_id,provider,namespace,external_id) VALUES($1::uuid,'tmdb','movie',$2)`, titleID, externalID); err != nil {
		t.Fatal(err)
	}
	principal := portableTestPrincipal(t, ctx, pool, userID, profileID, categoryID)
	service := NewService(pool, portableTestRuntimeSettings(t))
	provenance := validDocument()
	provenance.Addons = []Addon{}
	provenance.Titles = []Title{{Key: "sha256:" + strings.Repeat("c", 64), MediaType: "movie", ExternalIDs: []ExternalID{{Provider: "tmdb", Namespace: "movie", ExternalID: externalID}}}}
	if _, err := service.Import(ctx, principal, profileID, provenance); !errors.Is(err, ErrConflict) {
		t.Fatalf("asymmetric provenance import error = %v", err)
	}
	var bindings int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM portable_profile_resource_bindings WHERE profile_id=$1::uuid AND resource_kind='title'`, profileID).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatalf("provenance conflict was not rolled back: bindings=%d err=%v", bindings, err)
	}

	stateTitleID := ""
	if err := pool.QueryRow(ctx, `INSERT INTO titles(media_type,display_title) VALUES('movie','Tracking') RETURNING id::text`).Scan(&stateTitleID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM titles WHERE id=$1::uuid`, stateTitleID) })
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `INSERT INTO profile_library(profile_id,title_id,added_at,updated_at) VALUES($1::uuid,$2::uuid,$3,$3)`, profileID, stateTitleID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO profile_progress(profile_id,title_id,position_seconds,duration_seconds,completed,version,last_watched_at,updated_at) VALUES($1::uuid,$2::uuid,15,60,false,1,$3,$3)`, profileID, stateTitleID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO profile_tracking_accounts(profile_id,provider,access_token_encrypted,sync_watched,sync_progress,sync_library) VALUES($1::uuid,'trakt',decode(repeat('00',29),'hex'),true,true,true)`, profileID); err != nil {
		t.Fatal(err)
	}
	enabled := validDocument()
	enabled.Addons = []Addon{}
	enabled.TrackingPreferences = []TrackingPreference{{Provider: "trakt", SyncWatched: true, SyncProgress: true, SyncLibrary: true}}
	lockTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(0x524956554e454f42)); err != nil {
		t.Fatal(err)
	}
	importFinished := make(chan error, 1)
	go func() {
		_, err := service.Import(ctx, principal, profileID, enabled)
		importFinished <- err
	}()
	select {
	case err := <-importFinished:
		t.Fatalf("portable preference import bypassed tracking advisory lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := lockTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-importFinished; err != nil {
		t.Fatal(err)
	}
	readTitles := make(chan struct{})
	continueExport := make(chan struct{})
	snapshotService := NewService(pool, portableTestRuntimeSettings(t))
	snapshotService.afterExportTitlesRead = func() {
		close(readTitles)
		<-continueExport
	}
	type exportResult struct {
		document Document
		err      error
	}
	exported := make(chan exportResult, 1)
	go func() {
		document, err := snapshotService.Export(ctx, principal, profileID)
		exported <- exportResult{document: document, err: err}
	}()
	<-readTitles
	if _, err := pool.Exec(ctx, `UPDATE profile_progress SET position_seconds=45,version=2,updated_at=clock_timestamp(),last_watched_at=clock_timestamp() WHERE profile_id=$1::uuid AND title_id=$2::uuid`, profileID, stateTitleID); err != nil {
		t.Fatal(err)
	}
	close(continueExport)
	snapshot := <-exported
	if snapshot.err != nil {
		t.Fatalf("concurrent export: %v", snapshot.err)
	}
	if len(snapshot.document.Progress) != 1 || snapshot.document.Progress[0].PositionSeconds != 15 || snapshot.document.Progress[0].Version != 1 {
		t.Fatalf("export mixed concurrent snapshots: %+v", snapshot.document.Progress)
	}
	var queued int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM profile_tracking_outbox WHERE profile_id=$1::uuid AND provider='trakt'`, profileID).Scan(&queued); err != nil || queued != 3 {
		t.Fatalf("enabled preference did not seed tracking state: queued=%d err=%v", queued, err)
	}
	disabled := enabled
	disabled.TrackingPreferences = []TrackingPreference{{Provider: "trakt", SyncWatched: false, SyncProgress: false, SyncLibrary: false}}
	if _, err := service.Import(ctx, principal, profileID, disabled); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM profile_tracking_outbox WHERE profile_id=$1::uuid AND provider='trakt'`, profileID).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("disabled preference retained forbidden tracking work: queued=%d err=%v", queued, err)
	}

	tv := validDocument()
	tv.Addons = []Addon{}
	tv.Titles = []Title{{Key: "sha256:" + strings.Repeat("d", 64), MediaType: "tv", ExternalIDs: []ExternalID{{Provider: "addon", Namespace: "tv", ExternalID: "channel-" + suffix, ProfileScoped: true}}}}
	if _, err := service.Import(ctx, principal, profileID, tv); err != nil {
		t.Fatalf("profile-scoped TV identity import: %v", err)
	}
	var tvTitleID string
	if err := pool.QueryRow(ctx, `SELECT title_id::text FROM profile_title_external_ids WHERE profile_id=$1::uuid AND namespace='tv'`, profileID).Scan(&tvTitleID); err != nil {
		t.Fatalf("profile-scoped TV identity lookup: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM titles WHERE id=$1::uuid`, tvTitleID) })
}

func TestPortableArchiveSessionRevocationWinsBlockedExportAndImport(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run portable archive integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO instances(id,name) VALUES(1,'Portable session fence') ON CONFLICT(id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	var categoryID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_categories WHERE is_default`).Scan(&categoryID); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, portableTestRuntimeSettings(t))

	type fixture struct {
		userID, profileID string
		principal         auth.Principal
	}
	newFixture := func(t *testing.T, name string) fixture {
		t.Helper()
		suffix := fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
		var value fixture
		if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role) VALUES($1,'x','admin') RETURNING id::text`, "portable-fence-"+suffix).Scan(&value.userID); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO profiles(name,category_id) VALUES($1,$2::uuid) RETURNING id::text`, "Portable fence "+suffix, categoryID).Scan(&value.profileID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO profile_settings(profile_id,schema_version,settings) VALUES($1::uuid,1,'{}')
		`, value.profileID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO user_profile_access(user_id,profile_id,can_manage) VALUES($1::uuid,$2::uuid,true)
		`, value.userID, value.profileID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id=$1::uuid`, value.profileID)
			_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1::uuid`, value.userID)
		})
		value.principal = portableTestPrincipal(t, ctx, pool, value.userID, value.profileID, categoryID)
		return value
	}
	waitForSessionLock := func(t *testing.T, blockerPID int32) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			var blocked bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM pg_stat_activity
					WHERE pid <> pg_backend_pid()
					  AND wait_event_type = 'Lock'
					  AND $1::integer = ANY(pg_blocking_pids(pid))
					  AND query LIKE '%FROM auth_sessions%'
				)
			`, blockerPID).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("portable archive operation did not wait for the exact session lock")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	t.Run("revocation prevents export return", func(t *testing.T) {
		value := newFixture(t, "export")
		blocker, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = blocker.Rollback(context.Background()) }()
		var blockerPID int32
		var lockedSessionID string
		if err := blocker.QueryRow(ctx, `SELECT pg_backend_pid(), id::text FROM auth_sessions WHERE id=$1::uuid FOR UPDATE`, value.principal.SessionID).Scan(&blockerPID, &lockedSessionID); err != nil {
			t.Fatal(err)
		}
		type exportResult struct {
			document Document
			err      error
		}
		result := make(chan exportResult, 1)
		go func() {
			document, err := service.Export(ctx, value.principal, value.profileID)
			result <- exportResult{document: document, err: err}
		}()
		waitForSessionLock(t, blockerPID)
		if _, err := blocker.Exec(ctx, `UPDATE auth_sessions SET revoked_at=clock_timestamp(), revoked_reason='user_revoked' WHERE id=$1::uuid`, lockedSessionID); err != nil {
			t.Fatal(err)
		}
		if err := blocker.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		exported := <-result
		if !errors.Is(exported.err, ErrForbidden) {
			t.Fatalf("export after winning revocation error = %v, want %v", exported.err, ErrForbidden)
		}
		if exported.document.Version != 0 {
			t.Fatalf("revoked export returned an archive: %+v", exported.document)
		}
	})

	t.Run("logout prevents import mutation", func(t *testing.T) {
		value := newFixture(t, "import")
		blocker, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = blocker.Rollback(context.Background()) }()
		var blockerPID int32
		var lockedSessionID string
		if err := blocker.QueryRow(ctx, `SELECT pg_backend_pid(), id::text FROM auth_sessions WHERE id=$1::uuid FOR UPDATE`, value.principal.SessionID).Scan(&blockerPID, &lockedSessionID); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() {
			_, err := service.Import(ctx, value.principal, value.profileID, validDocument())
			result <- err
		}()
		waitForSessionLock(t, blockerPID)
		if _, err := blocker.Exec(ctx, `UPDATE auth_sessions SET revoked_at=clock_timestamp(), revoked_reason='logout' WHERE id=$1::uuid`, lockedSessionID); err != nil {
			t.Fatal(err)
		}
		if err := blocker.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := <-result; !errors.Is(err, ErrForbidden) {
			t.Fatalf("import after winning logout error = %v, want %v", err, ErrForbidden)
		}
		var addons, bindings int
		if err := pool.QueryRow(ctx, `
			SELECT
				(SELECT count(*) FROM addon_profile_access WHERE profile_id=$1::uuid),
				(SELECT count(*) FROM portable_profile_resource_bindings WHERE profile_id=$1::uuid)
		`, value.profileID).Scan(&addons, &bindings); err != nil {
			t.Fatal(err)
		}
		if addons != 0 || bindings != 0 {
			t.Fatalf("logout-losing import mutated resources: addons=%d bindings=%d", addons, bindings)
		}
	})
}
