package addonincident

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
)

func TestIncidentAggregationRecoveryRetentionIsolationAndSafety(t *testing.T) {
	pool := incidentTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const (
		userID = "88000000-0000-4000-8000-000000000001"
		categoryID = "88000000-0000-4000-8000-000000000002"
		categoryBID = "88000000-0000-4000-8000-000000000009"
		profileAID = "88000000-0000-4000-8000-000000000003"
		profileBID = "88000000-0000-4000-8000-000000000004"
		addonAID = "88000000-0000-4000-8000-000000000005"
		addonBID = "88000000-0000-4000-8000-000000000006"
		deviceID = "88000000-0000-4000-8000-000000000007"
		sessionID = "88000000-0000-4000-8000-000000000008"
	)
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM devices WHERE id=$1::uuid`, deviceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id=ANY($1::uuid[])`, []string{profileAID, profileBID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1::uuid`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM access_categories WHERE id=ANY($1::uuid[])`, []string{categoryID, categoryBID})
	}
	cleanup()
	t.Cleanup(cleanup)
	manifest := `{"id":"incident-test","version":"1","name":"Incident test","types":["movie"],"resources":["catalog"],"catalogs":[]}`
	contextHash := []byte("0123456789abcdef0123456789abcdef")
	grantExpiry := time.Now().UTC().Add(time.Hour)
	if _, err := pool.Exec(ctx, `
		WITH category AS (INSERT INTO access_categories(id,name,normalized_name,position) VALUES($1::uuid,'Incident tests','incident tests',880000) RETURNING id),
		account AS (INSERT INTO users(id,username,password_hash,role) VALUES($2::uuid,'incident-test-user','unused','admin') RETURNING id),
		profiles_created AS (INSERT INTO profiles(id,category_id,name) VALUES($3::uuid,$1::uuid,'Incident A'),($4::uuid,$1::uuid,'Incident B') RETURNING id),
		grants AS (INSERT INTO user_profile_access(user_id,profile_id,can_manage) VALUES($2::uuid,$3::uuid,true),($2::uuid,$4::uuid,true) RETURNING profile_id),
		device AS (INSERT INTO devices(id,user_id,name,platform,category_id,approved_at) VALUES($8::uuid,$2::uuid,'Incident test device','test',$1::uuid,now()) RETURNING id),
		session AS (INSERT INTO auth_sessions(id,user_id,device_id,access_token_hash,access_expires_at,refresh_expires_at,active_profile_id,profile_grant_expires_at,profile_context_hash,authorization_scope,category_id) VALUES($9::uuid,$2::uuid,$8::uuid,decode(repeat('11',32),'hex'),now()+interval '1 hour',now()+interval '2 hours',$3::uuid,$10,$11,'global_admin',NULL) RETURNING id)
		INSERT INTO profile_addons(id,profile_id,transport_url,manifest,manifest_id,manifest_version,position)
		VALUES($5::uuid,$3::uuid,'https://safe-a.invalid/manifest.json',$7::jsonb,'incident-test','1',0),($6::uuid,$4::uuid,'https://safe-b.invalid/manifest.json',$7::jsonb,'incident-test','1',0)
	`, categoryID, userID, profileAID, profileBID, addonAID, addonBID, manifest, deviceID, sessionID, grantExpiry, contextHash); err != nil {
		t.Fatalf("seed incident test: %v", err)
	}
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `INSERT INTO access_categories(id,name,normalized_name,position) VALUES($1::uuid,'Incident moved','incident moved',880001)`, categoryBID); err != nil { t.Fatalf("seed destination category: %v", err) }
	service := NewService(pool)
	service.now = func() time.Time { return now }
	principalA := auth.Principal{SessionID: sessionID, UserID: userID, DeviceID: deviceID, Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: new(profileAID), ProfileGrantExpiresAt: &grantExpiry, ProfileContextHash: contextHash, ActiveProfileCanManage: true}
	if err := service.RecordFailure(ctx, profileAID, addonAID, "Safe extension", CodeTimeout); err != nil { t.Fatal(err) }
	now = now.Add(time.Minute)
	if err := service.RecordFailure(ctx, profileAID, addonAID, "Safe extension", CodeTimeout); err != nil { t.Fatal(err) }
	listed, err := service.List(ctx, principalA)
	if err != nil || len(listed.Incidents) != 1 || listed.Incidents[0].OccurrenceCount != 2 || listed.Incidents[0].State != StateOpen { t.Fatalf("aggregated incidents = %+v, error %v", listed, err) }
	incidentID := listed.Incidents[0].ID
	now = now.Add(time.Minute)
	if err := service.RecordSuccess(ctx, profileAID, addonAID); err != nil { t.Fatal(err) }
	listed, err = service.List(ctx, principalA)
	if err != nil || listed.Incidents[0].State != StateRecovering || listed.Incidents[0].LastSuccessAt == nil { t.Fatalf("recovering incident = %+v, error %v", listed, err) }
	now = now.Add(time.Minute)
	if err := service.RecordSuccess(ctx, profileAID, addonAID); err != nil { t.Fatal(err) }
	acknowledged, err := service.Acknowledge(ctx, principalA, incidentID)
	if err != nil || acknowledged.State != StateResolved || acknowledged.ResolvedAt == nil || acknowledged.AcknowledgedAt == nil { t.Fatalf("resolved acknowledgement = %+v, error %v", acknowledged, err) }
	detail, err := service.Detail(ctx, principalA, incidentID)
	if err != nil || len(detail.Events) != 5 { t.Fatalf("incident detail = %+v, error %v", detail, err) }
	serialized := strings.ToLower(strings.Join([]string{detail.Incident.AddonName, detail.Incident.Code, detail.Incident.Impact}, " "))
	for _, secret := range []string{"https://", "token", "query", "body"} { if strings.Contains(serialized, secret) { t.Fatalf("incident exposed %q: %s", secret, serialized) } }
	forged := principalA
	forged.ActiveProfileID = new(profileBID)
	if _, err := service.Detail(ctx, forged, incidentID); err != ErrForbidden { t.Fatalf("forged cross-profile detail error = %v", err) }
	viewer := principalA
	viewer.ActiveProfileCanManage = false
	if _, err := service.List(ctx, viewer); err != ErrForbidden { t.Fatalf("viewer list error = %v", err) }
	if _, err := pool.Exec(ctx, `UPDATE addon_incidents SET resolved_at=$2,updated_at=$2 WHERE id=$1::uuid`, incidentID, now.Add(-retention-time.Hour)); err != nil { t.Fatal(err) }
	now = now.Add(time.Minute)
	if err := service.RecordFailure(ctx, profileAID, addonAID, "Safe extension", CodeUnavailable); err != nil { t.Fatal(err) }
	var retained int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM addon_incidents WHERE id=$1::uuid`, incidentID).Scan(&retained); err != nil || retained != 0 { t.Fatalf("expired incident retained = %d, error %v", retained, err) }
	if _, err := pool.Exec(ctx, `UPDATE auth_sessions SET authorization_scope='category',category_id=$2::uuid WHERE id=$1::uuid`, sessionID, categoryID); err != nil { t.Fatal(err) }
	categoryPrincipal := principalA
	categoryPrincipal.AuthorizationScope = auth.AuthorizationScopeCategory
	categoryPrincipal.CategoryID = new(categoryID)
	locked, _, err := service.beginAuthorizedProfile(ctx, categoryPrincipal)
	if err != nil { t.Fatalf("lock incident authorization: %v", err) }
	revoked := make(chan error, 1)
	go func() { _, revokeErr := pool.Exec(context.Background(), `DELETE FROM user_profile_access WHERE user_id=$1::uuid AND profile_id=$2::uuid`, userID, profileAID); revoked <- revokeErr }()
	select { case err := <-revoked: t.Fatalf("grant revocation bypassed incident authorization lock: %v", err); case <-time.After(100 * time.Millisecond): }
	if err := locked.Commit(ctx); err != nil { t.Fatal(err) }
	if err := <-revoked; err != nil { t.Fatal(err) }
	if _, err := service.List(ctx, categoryPrincipal); err != ErrForbidden { t.Fatalf("list after grant revocation error = %v", err) }
	if _, err := pool.Exec(ctx, `INSERT INTO user_profile_access(user_id,profile_id,can_manage) VALUES($1::uuid,$2::uuid,true)`, userID, profileAID); err != nil { t.Fatal(err) }
	locked, _, err = service.beginAuthorizedProfile(ctx, categoryPrincipal)
	if err != nil { t.Fatalf("relock incident authorization: %v", err) }
	moved := make(chan error, 1)
	go func() { _, moveErr := pool.Exec(context.Background(), `UPDATE profiles SET category_id=$2::uuid WHERE id=$1::uuid`, profileAID, categoryBID); moved <- moveErr }()
	select { case err := <-moved: t.Fatalf("profile move bypassed incident authorization lock: %v", err); case <-time.After(100 * time.Millisecond): }
	if err := locked.Commit(ctx); err != nil { t.Fatal(err) }
	if err := <-moved; err != nil { t.Fatal(err) }
	if _, err := service.List(ctx, categoryPrincipal); err != ErrForbidden { t.Fatalf("list after category move error = %v", err) }
}

func incidentTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" { databaseURL = os.Getenv("RIVUNE_DATABASE_URL") }
	if databaseURL == "" { t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run addon incident persistence tests") }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil { t.Fatalf("open incident database: %v", err) }
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil { t.Fatalf("migrate incident database: %v", err) }
	return pool
}
