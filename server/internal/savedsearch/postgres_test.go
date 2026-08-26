package savedsearch

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

func TestPostgresProfileIsolationRevisionAndCatalogEvaluation(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run saved search PostgreSQL tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open saved search database: %v", err)
	}
	defer pool.Close()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	position := int(time.Now().UnixNano()%1_000_000_000) + 1_000_000_000
	var userID, categoryID, firstProfileID, secondProfileID, deviceID, sessionID, titleID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		VALUES ($1, $2, $3) RETURNING id::text
	`, "Saved search "+suffix, "saved search "+suffix, position).Scan(&categoryID); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (username, password_hash, role) VALUES ($1, 'unused', 'member') RETURNING id::text`, "saved_search_"+suffix).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO profiles (category_id, name) VALUES ($1::uuid, 'First') RETURNING id::text`, categoryID).Scan(&firstProfileID); err != nil {
		t.Fatalf("seed first profile: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO profiles (category_id, name) VALUES ($1::uuid, 'Second') RETURNING id::text`, categoryID).Scan(&secondProfileID); err != nil {
		t.Fatalf("seed second profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_profile_access (user_id, profile_id, can_manage) VALUES ($1::uuid, $2::uuid, false), ($1::uuid, $3::uuid, false)`, userID, firstProfileID, secondProfileID); err != nil {
		t.Fatalf("grant profiles: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO devices (user_id, name, platform, category_id, approved_at) VALUES ($1::uuid, 'Saved search test', 'test', $2::uuid, now()) RETURNING id::text`, userID, categoryID).Scan(&deviceID); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	accessHash := sha256.Sum256([]byte("saved-search-" + suffix))
	contextHash := sha256.Sum256([]byte("saved-search-context-" + suffix))
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
		  authorization_scope, category_id, active_profile_id, profile_grant_expires_at, profile_context_hash)
		VALUES ($1::uuid, $2::uuid, $3, now() + interval '1 hour', now() + interval '1 day',
		  'category', $4::uuid, $5::uuid, now() + interval '1 hour', $6) RETURNING id::text
	`, userID, deviceID, accessHash[:], categoryID, firstProfileID, contextHash[:]).Scan(&sessionID); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO titles (media_type, display_title, release_info, release_date, resource_id, resource_provider)
		VALUES ('movie', $1, '2024', '2024-01-02', $2, 'tmdb') RETURNING id::text
	`, "Saved drama "+suffix, "saved-drama-"+suffix).Scan(&titleID); err != nil {
		t.Fatalf("seed title: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id)
		VALUES ($1::uuid, 'tmdb', 'movie', $2)
	`, titleID, "saved-drama-"+suffix); err != nil {
		t.Fatalf("seed title provider identity: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at)
		VALUES ($1::uuid, 'tmdb', 'en', '{"genres":[{"name":"Drama"}],"voteAverage":8.5,"status":"Released"}', now() + interval '1 hour')
	`, titleID); err != nil {
		t.Fatalf("seed catalog metadata: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO profile_library (profile_id, title_id) VALUES ($1::uuid, $2::uuid)`, firstProfileID, titleID); err != nil {
		t.Fatalf("seed profile library: %v", err)
	}
	t.Cleanup(func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE id = $1::uuid`, userID)
		_, _ = pool.Exec(cleanup, `DELETE FROM access_categories WHERE id = $1::uuid`, categoryID)
		_, _ = pool.Exec(cleanup, `DELETE FROM titles WHERE id = $1::uuid`, titleID)
	})

	expires := time.Now().UTC().Add(time.Hour)
	active := firstProfileID
	principal := auth.Principal{SessionID: sessionID, UserID: userID, DeviceID: deviceID, Role: "member", AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID, ActiveProfileID: &active, ProfileGrantExpiresAt: &expires, ProfileContextHash: contextHash[:]}
	catalog := watchstate.NewService(pool, time.UTC)
	service := NewService(pool, catalog)

	saved, err := service.CreateSavedSearch(ctx, principal, SavedSearchInput{Name: "Space", Query: "space opera", MediaType: "movie", Sort: "relevance"})
	if err != nil {
		t.Fatalf("create saved search: %v", err)
	}
	stale := SavedSearchInput{Name: "Space 2", Query: "space", MediaType: "movie", Sort: "title", ExpectedRevision: saved.Revision + 1}
	if _, err := service.UpdateSavedSearch(ctx, principal, saved.ID, stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale saved search update = %v", err)
	}

	smart, err := service.CreateSmartCollection(ctx, principal, SmartCollectionInput{Name: "Recent drama", Sort: "rating", Rules: Rule{Type: "all", Rules: []Rule{
		{Type: "genre", Operator: "equals", Value: "drama"},
		{Type: "year", Operator: "gte", Number: new(float64(2020))},
		{Type: "status", Operator: "equals", Value: "released"},
	}}})
	if err != nil {
		t.Fatalf("create smart collection: %v", err)
	}
	page, err := service.EvaluateSmartCollection(ctx, principal, smart.ID, 1, 10)
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != titleID {
		t.Fatalf("evaluated smart collection = %+v, %v", page, err)
	}

	secondContextHash := sha256.Sum256([]byte("saved-search-context-second-" + suffix))
	if _, err := pool.Exec(ctx, `UPDATE auth_sessions SET active_profile_id = $2::uuid, profile_context_hash = $3 WHERE id = $1::uuid`, sessionID, secondProfileID, secondContextHash[:]); err != nil {
		t.Fatalf("switch profile session: %v", err)
	}
	active = secondProfileID
	principal.ProfileContextHash = secondContextHash[:]
	if values, err := service.ListSavedSearches(ctx, principal); err != nil || len(values) != 0 {
		t.Fatalf("second profile saved searches = %+v, %v", values, err)
	}
	if _, err := service.EvaluateSmartCollection(ctx, principal, smart.ID, 1, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign smart collection evaluation = %v", err)
	}
}
