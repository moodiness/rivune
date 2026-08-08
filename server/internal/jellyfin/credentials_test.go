package jellyfin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
	profileservice "github.com/moodiness/rivune/server/internal/profile"
	userservice "github.com/moodiness/rivune/server/internal/user"
)

func TestCredentialStoreHashOnlyStableUsernameAuthorizationAndRevocation(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run Jellyfin profile credential store tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open Jellyfin credential database: %v", err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate Jellyfin credential database: %v", err)
	}
	fixture := seedCredentialStoreFixture(t, ctx, pool)
	store, err := NewCredentialStore(pool)
	if err != nil {
		t.Fatalf("create Jellyfin credential store: %v", err)
	}

	status, err := store.Status(ctx, fixture.owner, fixture.profileID)
	if err != nil {
		t.Fatalf("read never-created credential status: %v", err)
	}
	if status.Active || status.Username != "" || !status.CanIssue {
		t.Fatalf("never-created credential status = %+v, want inactive without username", status)
	}
	if _, err := store.Status(ctx, fixture.outsider, fixture.profileID); !errors.Is(err, ErrCredentialForbidden) {
		t.Fatalf("unauthorized credential status error = %v, want %v", err, ErrCredentialForbidden)
	}
	globalWithoutGrant := fixture.outsider
	globalWithoutGrant.Role = "admin"
	globalWithoutGrant.AuthorizationScope = auth.AuthorizationScopeGlobalAdministrator
	globalWithoutGrant.CategoryID = nil
	globalWithoutGrant.ActiveProfileID = nil
	globalWithoutGrant.ProfileGrantExpiresAt = nil
	globalStatus, err := store.Status(ctx, globalWithoutGrant, fixture.profileID)
	if err != nil {
		t.Fatalf("read global credential status without owner grant: %v", err)
	}
	if globalStatus.CanIssue {
		t.Fatalf("global credential status without owner grant = %+v", globalStatus)
	}
	if _, err := store.Create(ctx, globalWithoutGrant, fixture.profileID); !errors.Is(err, ErrCredentialForbidden) {
		t.Fatalf("global credential creation without owner grant error = %v, want %v", err, ErrCredentialForbidden)
	}

	created, err := store.Create(ctx, fixture.owner, fixture.profileID)
	if err != nil {
		t.Fatalf("create own-profile Jellyfin credential: %v", err)
	}
	if !created.Active || !created.CanIssue || created.Username == "" || created.Password == "" || created.Generation != 1 {
		t.Fatalf("created credential = %+v", created.CredentialStatus)
	}
	digest, canonical := auth.JellyfinAppPasswordDigest(created.Password)
	if !canonical {
		t.Fatal("created password does not use canonical application-password representation")
	}
	var storedHash []byte
	var storedPasswordColumns int
	if err := pool.QueryRow(ctx, `
		SELECT password_hash,
		       (SELECT count(*) FROM information_schema.columns
		        WHERE table_schema = current_schema()
		          AND table_name = 'profile_jellyfin_credentials'
		          AND column_name IN ('password', 'secret', 'plaintext_password'))
		FROM profile_jellyfin_credentials
		WHERE id = $1::uuid
	`, created.Username).Scan(&storedHash, &storedPasswordColumns); err != nil {
		t.Fatalf("read stored Jellyfin credential digest: %v", err)
	}
	if !bytes.Equal(storedHash, digest) || bytes.Equal(storedHash, []byte(created.Password)) {
		t.Fatal("stored credential is not exactly the canonical SHA-256 digest")
	}
	if storedPasswordColumns != 0 {
		t.Fatalf("credential schema exposes %d plaintext password columns", storedPasswordColumns)
	}
	if _, err := store.Create(ctx, fixture.owner, fixture.profileID); !errors.Is(err, ErrCredentialExists) {
		t.Fatalf("duplicate credential creation error = %v, want %v", err, ErrCredentialExists)
	}

	rotatedSessionID, rotatedCompatID := seedCredentialSession(t, ctx, pool, fixture, created)
	dropFailure := installCredentialSessionRevocationFailure(t, ctx, pool, rotatedSessionID)
	if _, err := store.Rotate(ctx, fixture.manager, fixture.profileID); err == nil {
		t.Fatal("rotation succeeded despite linked-session revocation failure")
	}
	dropFailure()
	status, err = store.Status(ctx, fixture.owner, fixture.profileID)
	if err != nil {
		t.Fatalf("read credential after rolled-back rotation: %v", err)
	}
	if !status.Active || status.Generation != created.Generation || status.Username != created.Username {
		t.Fatalf("failed rotation partially changed credential: %+v", status)
	}
	assertCredentialSessionsActive(t, ctx, pool, rotatedSessionID, rotatedCompatID)
	rotated, err := store.Rotate(ctx, fixture.manager, fixture.profileID)
	if err != nil {
		t.Fatalf("manager rotates Jellyfin credential: %v", err)
	}
	if rotated.Username != created.Username || rotated.Generation != created.Generation+1 || rotated.Password == created.Password {
		t.Fatalf("rotated credential did not preserve username/increment generation: before=%+v after=%+v", created.CredentialStatus, rotated.CredentialStatus)
	}
	assertCredentialSessionsRevoked(t, ctx, pool, rotatedSessionID, rotatedCompatID)

	revokedSessionID, revokedCompatID := seedCredentialSession(t, ctx, pool, fixture, rotated)
	if err := store.Revoke(ctx, fixture.owner, fixture.profileID); err != nil {
		t.Fatalf("revoke own-profile Jellyfin credential: %v", err)
	}
	assertCredentialSessionsRevoked(t, ctx, pool, revokedSessionID, revokedCompatID)
	if err := store.Revoke(ctx, fixture.owner, fixture.profileID); err != nil {
		t.Fatalf("repeat Jellyfin credential revocation: %v", err)
	}
	status, err = store.Status(ctx, fixture.owner, fixture.profileID)
	if err != nil {
		t.Fatalf("read revoked credential status: %v", err)
	}
	if status.Active || status.Username != created.Username || status.RevokedAt == nil || status.Generation != rotated.Generation+1 {
		t.Fatalf("revoked credential status = %+v", status)
	}
	var revokedHash []byte
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM profile_jellyfin_credentials WHERE id = $1::uuid`, created.Username).Scan(&revokedHash); err != nil {
		t.Fatalf("read revoked credential hash: %v", err)
	}
	if revokedHash != nil {
		t.Fatal("revoked credential retained a password digest")
	}

	reactivated, err := store.Create(ctx, fixture.owner, fixture.profileID)
	if err != nil {
		t.Fatalf("reactivate revoked Jellyfin credential: %v", err)
	}
	if reactivated.Username != created.Username || !reactivated.Active || reactivated.Generation != status.Generation+1 {
		t.Fatalf("reactivated credential did not preserve UUID/increment generation: %+v", reactivated.CredentialStatus)
	}
	deletedOwnerSessionID, _ := seedCredentialSession(t, ctx, pool, fixture, reactivated)
	deletePrincipal := fixture.manager
	deletePrincipal.Role = "admin"
	deletePrincipal.AuthorizationScope = auth.AuthorizationScopeGlobalAdministrator
	deletePrincipal.CategoryID = nil
	type rotationResult struct {
		credential ProfileCredential
		err        error
	}
	start := make(chan struct{})
	rotationResults := make(chan rotationResult, 1)
	deletionResults := make(chan error, 1)
	go func() {
		<-start
		credential, rotateErr := store.Rotate(ctx, fixture.owner, fixture.profileID)
		rotationResults <- rotationResult{credential: credential, err: rotateErr}
	}()
	go func() {
		<-start
		deletionResults <- userservice.NewService(pool).Delete(ctx, deletePrincipal, fixture.ownerUserID)
	}()
	close(start)
	rotatedDuringDeletion := <-rotationResults
	if rotatedDuringDeletion.err != nil && !errors.Is(rotatedDuringDeletion.err, ErrCredentialForbidden) && !errors.Is(rotatedDuringDeletion.err, ErrCredentialNotFound) {
		t.Fatalf("rotation concurrent with owner deletion: %v", rotatedDuringDeletion.err)
	}
	if err := <-deletionResults; err != nil {
		t.Fatalf("delete Jellyfin credential owner concurrently: %v", err)
	}
	wantRetainedGeneration := reactivated.Generation + 1
	if rotatedDuringDeletion.err == nil {
		wantRetainedGeneration = rotatedDuringDeletion.credential.Generation + 1
	}
	var retainedUsername string
	var retainedOwnerID *string
	var retainedHash []byte
	var retainedRevokedAt *time.Time
	var retainedGeneration int64
	if err := pool.QueryRow(ctx, `
		SELECT id::text, owner_user_id::text, password_hash, revoked_at, generation
		FROM profile_jellyfin_credentials
		WHERE profile_id = $1::uuid
	`, fixture.profileID).Scan(&retainedUsername, &retainedOwnerID, &retainedHash, &retainedRevokedAt, &retainedGeneration); err != nil {
		t.Fatalf("read credential after owner deletion: %v", err)
	}
	if retainedUsername != created.Username || retainedOwnerID != nil || retainedHash != nil || retainedRevokedAt == nil || retainedGeneration != wantRetainedGeneration {
		t.Fatalf("credential after owner deletion username=%q owner=%v hash=%x revoked=%v generation=%d", retainedUsername, retainedOwnerID, retainedHash, retainedRevokedAt, retainedGeneration)
	}
	var deletedOwnerSessionExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM auth_sessions WHERE id = $1::uuid)`, deletedOwnerSessionID).Scan(&deletedOwnerSessionExists); err != nil {
		t.Fatalf("read deleted owner Jellyfin session: %v", err)
	}
	if deletedOwnerSessionExists {
		t.Fatal("owner deletion retained a Jellyfin session")
	}
	reassigned, err := store.Create(ctx, fixture.manager, fixture.profileID)
	if err != nil {
		t.Fatalf("reactivate Jellyfin credential after owner deletion: %v", err)
	}
	if reassigned.Username != created.Username || reassigned.Generation != retainedGeneration+1 || !reassigned.Active {
		t.Fatalf("credential reactivation after owner deletion = %+v", reassigned.CredentialStatus)
	}
}

func TestCredentialRotationConcurrentWithProfileDeletionDoesNotDeadlock(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run Jellyfin profile credential concurrency tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open Jellyfin credential concurrency database: %v", err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate Jellyfin credential concurrency database: %v", err)
	}
	fixture := seedCredentialStoreFixture(t, ctx, pool)
	store, err := NewCredentialStore(pool)
	if err != nil {
		t.Fatalf("create Jellyfin credential concurrency store: %v", err)
	}
	if _, err := store.Create(ctx, fixture.owner, fixture.profileID); err != nil {
		t.Fatalf("create concurrent profile credential: %v", err)
	}

	deletePrincipal := fixture.manager
	deletePrincipal.Role = "admin"
	deletePrincipal.AuthorizationScope = auth.AuthorizationScopeGlobalAdministrator
	deletePrincipal.CategoryID = nil
	start := make(chan struct{})
	rotationResults := make(chan error, 1)
	deletionResults := make(chan error, 1)
	go func() {
		<-start
		_, rotateErr := store.Rotate(ctx, fixture.manager, fixture.profileID)
		rotationResults <- rotateErr
	}()
	go func() {
		<-start
		deletionResults <- profileservice.NewService(pool, time.Hour, "UTC").Delete(ctx, deletePrincipal, fixture.profileID)
	}()
	close(start)
	rotateErr := <-rotationResults
	if rotateErr != nil && !errors.Is(rotateErr, ErrCredentialForbidden) && !errors.Is(rotateErr, ErrCredentialNotFound) {
		t.Fatalf("rotation concurrent with profile deletion: %v", rotateErr)
	}
	if err := <-deletionResults; err != nil {
		t.Fatalf("delete profile concurrently with credential rotation: %v", err)
	}
	var profileExists, credentialExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM profiles WHERE id = $1::uuid),
		       EXISTS (SELECT 1 FROM profile_jellyfin_credentials WHERE profile_id = $1::uuid)
	`, fixture.profileID).Scan(&profileExists, &credentialExists); err != nil {
		t.Fatalf("read concurrent profile deletion state: %v", err)
	}
	if profileExists || credentialExists {
		t.Fatalf("concurrent profile deletion retained profile=%t credential=%t", profileExists, credentialExists)
	}
}

type credentialStoreFixture struct {
	profileID   string
	categoryID  string
	ownerUserID string
	owner       auth.Principal
	manager     auth.Principal
	outsider    auth.Principal
}

func seedCredentialStoreFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) credentialStoreFixture {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	fixture := credentialStoreFixture{}
	var managerUserID, outsiderUserID, outsiderProfileID string
	for name, target := range map[string]*string{
		"owner":    &fixture.ownerUserID,
		"manager":  &managerUserID,
		"outsider": &outsiderUserID,
	} {
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (username, password_hash, role)
			VALUES ($1, 'unused-jellyfin-credential-test-hash', 'member')
			RETURNING id::text
		`, "jf_credential_"+name+"_"+suffix).Scan(target); err != nil {
			t.Fatalf("insert Jellyfin credential %s user: %v", name, err)
		}
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name) VALUES ($1)
		RETURNING id::text, category_id::text
	`, "Jellyfin credential target "+suffix).Scan(&fixture.profileID, &fixture.categoryID); err != nil {
		t.Fatalf("insert Jellyfin credential target profile: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name, category_id) VALUES ($1, $2::uuid)
		RETURNING id::text
	`, "Jellyfin credential outsider "+suffix, fixture.categoryID).Scan(&outsiderProfileID); err != nil {
		t.Fatalf("insert Jellyfin credential outsider profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($1, $2, false), ($3, $2, true), ($4, $5, false)
	`, fixture.ownerUserID, fixture.profileID, managerUserID, outsiderUserID, outsiderProfileID); err != nil {
		t.Fatalf("grant Jellyfin credential profile access: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1::uuid[])`, []string{fixture.ownerUserID, managerUserID, outsiderUserID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id = ANY($1::uuid[])`, []string{fixture.profileID, outsiderProfileID})
	})
	expiresAt := time.Now().UTC().Add(time.Hour)
	fixture.owner = auth.Principal{
		UserID: fixture.ownerUserID, Role: "member", AuthorizationScope: auth.AuthorizationScopeCategory,
		CategoryID: &fixture.categoryID, ActiveProfileID: &fixture.profileID, ProfileGrantExpiresAt: &expiresAt,
	}
	fixture.manager = auth.Principal{
		UserID: managerUserID, Role: "member", AuthorizationScope: auth.AuthorizationScopeCategory,
		CategoryID: &fixture.categoryID,
	}
	fixture.outsider = auth.Principal{
		UserID: outsiderUserID, Role: "member", AuthorizationScope: auth.AuthorizationScopeCategory,
		CategoryID: &fixture.categoryID, ActiveProfileID: &outsiderProfileID, ProfileGrantExpiresAt: &expiresAt,
	}
	return fixture
}

func seedCredentialSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture credentialStoreFixture, credential ProfileCredential) (string, string) {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var deviceID, sessionID, compatID, sessionUserID string
	if err := pool.QueryRow(ctx, `
		SELECT owner_user_id::text FROM profile_jellyfin_credentials WHERE id = $1::uuid
	`, credential.Username).Scan(&sessionUserID); err != nil {
		t.Fatalf("read credential session owner: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1::uuid, $2, 'jellyfin-test', $3::uuid, now())
		RETURNING id::text
	`, sessionUserID, "Jellyfin credential session "+suffix, fixture.categoryID).Scan(&deviceID); err != nil {
		t.Fatalf("insert credential session device: %v", err)
	}
	accessHash := sha256.Sum256([]byte("jellyfin-credential-access-" + suffix))
	contextHash := sha256.Sum256([]byte("jellyfin-credential-context-" + suffix))
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id, active_profile_id, profile_grant_expires_at,
			profile_context_hash, jellyfin_credential_id, jellyfin_credential_generation
		) VALUES (
			$1::uuid, $2::uuid, $3, now() + interval '1 hour', now() + interval '2 hours',
			'category', $4::uuid, $5::uuid, now() + interval '2 hours', $6, $7::uuid, $8
		) RETURNING id::text
	`, sessionUserID, deviceID, accessHash[:], fixture.categoryID, fixture.profileID,
		contextHash[:], credential.Username, credential.Generation).Scan(&sessionID); err != nil {
		t.Fatalf("insert credential-linked native session: %v", err)
	}
	compatHash := sha256.Sum256([]byte("jellyfin-credential-compat-" + suffix))
	if err := pool.QueryRow(ctx, `
		INSERT INTO jellyfin_compat_sessions (
			auth_session_id, profile_id, token_hash, client_name, device_name,
			client_device_id, client_version, expires_at
		) VALUES ($1::uuid, $2::uuid, $3, 'Generic Client', $4, $5, '1.0', now() + interval '2 hours')
		RETURNING id::text
	`, sessionID, fixture.profileID, compatHash[:], "Credential session "+suffix, "credential-session-"+suffix).Scan(&compatID); err != nil {
		t.Fatalf("insert credential-linked compatibility session: %v", err)
	}
	return sessionID, compatID
}

func assertCredentialSessionsRevoked(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID, compatID string) {
	t.Helper()
	var nativeRevokedAt, compatRevokedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT revoked_at FROM auth_sessions WHERE id = $1::uuid`, sessionID).Scan(&nativeRevokedAt); err != nil {
		t.Fatalf("read credential-linked native revocation: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT revoked_at FROM jellyfin_compat_sessions WHERE id = $1::uuid`, compatID).Scan(&compatRevokedAt); err != nil {
		t.Fatalf("read credential-linked compatibility revocation: %v", err)
	}
	if nativeRevokedAt == nil || compatRevokedAt == nil {
		t.Fatalf("credential session revocation native=%v compat=%v", nativeRevokedAt, compatRevokedAt)
	}
}

func assertCredentialSessionsActive(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID, compatID string) {
	t.Helper()
	var nativeActive, compatActive bool
	if err := pool.QueryRow(ctx, `SELECT revoked_at IS NULL FROM auth_sessions WHERE id = $1::uuid`, sessionID).Scan(&nativeActive); err != nil {
		t.Fatalf("read active credential-linked native session: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT revoked_at IS NULL FROM jellyfin_compat_sessions WHERE id = $1::uuid`, compatID).Scan(&compatActive); err != nil {
		t.Fatalf("read active credential-linked compatibility session: %v", err)
	}
	if !nativeActive || !compatActive {
		t.Fatalf("failed credential mutation partially revoked sessions: native=%t compat=%t", nativeActive, compatActive)
	}
}

func installCredentialSessionRevocationFailure(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID string) func() {
	t.Helper()
	identifier := fmt.Sprintf("jellyfin_credential_failure_%d", time.Now().UnixNano())
	functionName := identifier + "_fn"
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $function$
		BEGIN
			IF NEW.id::text = '%s' THEN
				RAISE EXCEPTION 'injected Jellyfin credential session revocation failure';
			END IF;
			RETURN NEW;
		END
		$function$;
		CREATE TRIGGER %s BEFORE UPDATE ON auth_sessions
		FOR EACH ROW EXECUTE FUNCTION %s();
	`, functionName, sessionID, identifier, functionName)); err != nil {
		t.Fatalf("install credential revocation failure trigger: %v", err)
	}
	drop := func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`
			DROP TRIGGER IF EXISTS %s ON auth_sessions;
			DROP FUNCTION IF EXISTS %s();
		`, identifier, functionName))
	}
	t.Cleanup(drop)
	return drop
}
