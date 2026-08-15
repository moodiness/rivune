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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
	profileservice "github.com/moodiness/rivune/server/internal/profile"
	"github.com/moodiness/rivune/server/internal/runtimesettings"
	userservice "github.com/moodiness/rivune/server/internal/user"
)

func TestCompatibilitySessionRevocationNotifierIsBestEffort(t *testing.T) {
	notifyCompatibilitySessionRevocations(nil, []string{"session"})
	notifyCompatibilitySessionRevocations(func([]string) { panic("unavailable in-memory cleanup") }, []string{"session"})
}

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
	store, err := NewCredentialStore(pool, testCredentialRuntimeSettings(t))
	if err != nil {
		t.Fatalf("create Jellyfin credential store: %v", err)
	}
	handler := &Handler{bootstrap: newBootstrapRegistry()}
	var notifiedSessionIDs [][]string
	store.SetCompatibilitySessionRevocationNotifier(func(sessionIDs []string) {
		notifiedSessionIDs = append(notifiedSessionIDs, append([]string(nil), sessionIDs...))
		handler.ForgetCompatibilitySessions(sessionIDs)
	})

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
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = $1::uuid`, fixture.outsider.UserID); err != nil {
		t.Fatalf("promote global credential reader: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE auth_sessions SET authorization_scope = 'global_admin', category_id = NULL WHERE id = $1::uuid`, fixture.outsider.SessionID); err != nil {
		t.Fatalf("promote global credential reader session: %v", err)
	}
	globalWithoutGrant := fixture.outsider
	globalWithoutGrant.Role = "admin"
	globalWithoutGrant.AuthorizationScope = auth.AuthorizationScopeGlobalAdministrator
	globalWithoutGrant.CategoryID = nil
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
	rotatedSocketSession := bootstrapSession(rotatedCompatID, fixture.profileID, "rotated-credential-device")
	rotatedLease, rotatedLeaseOK := handler.bootstrap.acquireSocket(rotatedSocketSession)
	if !rotatedLeaseOK {
		t.Fatal("failed to prepare rotated credential socket")
	}
	dropFailure := installCredentialSessionRevocationFailure(t, ctx, pool, rotatedSessionID)
	if _, err := store.Rotate(ctx, fixture.manager, fixture.profileID); err == nil {
		t.Fatal("rotation succeeded despite linked-session revocation failure")
	}
	if len(notifiedSessionIDs) != 0 {
		t.Fatalf("rolled-back rotation published compatibility revocations: %#v", notifiedSessionIDs)
	}
	select {
	case <-rotatedLease.closed:
		t.Fatal("rolled-back credential rotation closed its compatibility socket")
	default:
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
	if len(notifiedSessionIDs) != 1 || len(notifiedSessionIDs[0]) != 1 || notifiedSessionIDs[0][0] != rotatedCompatID {
		t.Fatalf("rotation compatibility revocations = %#v, want only %q", notifiedSessionIDs, rotatedCompatID)
	}
	select {
	case <-rotatedLease.closed:
	default:
		t.Fatal("credential rotation did not close its compatibility socket after commit")
	}

	revokedSessionID, revokedCompatID := seedCredentialSession(t, ctx, pool, fixture, rotated)
	revokedSocketSession := bootstrapSession(revokedCompatID, fixture.profileID, "revoked-credential-device")
	revokedLease, revokedLeaseOK := handler.bootstrap.acquireSocket(revokedSocketSession)
	if !revokedLeaseOK {
		t.Fatal("failed to prepare revoked credential socket")
	}
	if err := store.Revoke(ctx, fixture.owner, fixture.profileID); err != nil {
		t.Fatalf("revoke own-profile Jellyfin credential: %v", err)
	}
	assertCredentialSessionsRevoked(t, ctx, pool, revokedSessionID, revokedCompatID)
	if len(notifiedSessionIDs) != 2 || len(notifiedSessionIDs[1]) != 1 || notifiedSessionIDs[1][0] != revokedCompatID {
		t.Fatalf("credential revoke compatibility revocations = %#v, want only %q", notifiedSessionIDs, revokedCompatID)
	}
	select {
	case <-revokedLease.closed:
	default:
		t.Fatal("credential revocation did not close its compatibility socket after commit")
	}
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

func TestCredentialMutationsLoseToAuthoritativeSessionInvalidation(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run Jellyfin credential session fence tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open Jellyfin credential session fence database: %v", err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate Jellyfin credential session fence database: %v", err)
	}

	tests := []struct {
		name      string
		operation string
		expire    bool
	}{
		{name: "create after logout", operation: "create"},
		{name: "rotate after access expiry", operation: "rotate", expire: true},
		{name: "revoke after logout", operation: "revoke"},
		{name: "status after logout", operation: "status"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := seedCredentialStoreFixture(t, ctx, pool)
			store, storeErr := NewCredentialStore(pool, testCredentialRuntimeSettings(t))
			if storeErr != nil {
				t.Fatalf("create fenced Jellyfin credential store: %v", storeErr)
			}
			if test.operation != "create" {
				if _, createErr := store.Create(ctx, fixture.owner, fixture.profileID); createErr != nil {
					t.Fatalf("create credential before fenced %s: %v", test.operation, createErr)
				}
			}
			before := readCredentialMutationState(t, ctx, pool, fixture.profileID)

			blocker, beginErr := pool.Begin(ctx)
			if beginErr != nil {
				t.Fatalf("begin authoritative session invalidation: %v", beginErr)
			}
			blockerFinished := false
			defer func() {
				if !blockerFinished {
					_ = blocker.Rollback(context.Background())
				}
			}()
			var blockerPID int
			var expiresAt time.Time
			if test.expire {
				if updateErr := blocker.QueryRow(ctx, `
					UPDATE auth_sessions
					SET access_expires_at = clock_timestamp() + interval '2 seconds'
					WHERE id = $1::uuid
					RETURNING pg_backend_pid(), access_expires_at
				`, fixture.owner.SessionID).Scan(&blockerPID, &expiresAt); updateErr != nil {
					t.Fatalf("lock near-expiry authoritative session: %v", updateErr)
				}
			} else if updateErr := blocker.QueryRow(ctx, `
				UPDATE auth_sessions
				SET revoked_at = clock_timestamp(), revoked_reason = 'test_logout'
				WHERE id = $1::uuid
				RETURNING pg_backend_pid()
			`, fixture.owner.SessionID).Scan(&blockerPID); updateErr != nil {
				t.Fatalf("lock logged-out authoritative session: %v", updateErr)
			}

			result := make(chan credentialMutationResult, 1)
			go func() {
				switch test.operation {
				case "create":
					credential, mutationErr := store.Create(ctx, fixture.owner, fixture.profileID)
					result <- credentialMutationResult{credential: credential, err: mutationErr}
				case "rotate":
					credential, mutationErr := store.Rotate(ctx, fixture.owner, fixture.profileID)
					result <- credentialMutationResult{credential: credential, err: mutationErr}
				case "revoke":
					result <- credentialMutationResult{err: store.Revoke(ctx, fixture.owner, fixture.profileID)}
				case "status":
					status, operationErr := store.Status(ctx, fixture.owner, fixture.profileID)
					result <- credentialMutationResult{credential: ProfileCredential{CredentialStatus: status}, err: operationErr}
				}
			}()
			waitForCredentialSessionLock(t, ctx, pool, blockerPID, result)
			if test.expire {
				if _, sleepErr := pool.Exec(ctx, `
					SELECT pg_sleep((GREATEST(EXTRACT(EPOCH FROM ($1::timestamptz - clock_timestamp())), 0) + 0.05)::double precision)
				`, expiresAt); sleepErr != nil {
					t.Fatalf("wait for authoritative access expiry: %v", sleepErr)
				}
			}
			if commitErr := blocker.Commit(ctx); commitErr != nil {
				t.Fatalf("commit authoritative session invalidation: %v", commitErr)
			}
			blockerFinished = true

			mutated := <-result
			if !errors.Is(mutated.err, ErrCredentialForbidden) {
				t.Fatalf("stale %s error = %v, want %v", test.operation, mutated.err, ErrCredentialForbidden)
			}
			if mutated.credential.Password != "" || mutated.credential.Username != "" {
				t.Fatalf("stale %s returned credential material: %+v", test.operation, mutated.credential)
			}
			after := readCredentialMutationState(t, ctx, pool, fixture.profileID)
			if after != before {
				t.Fatalf("stale %s changed credential state: before=%+v after=%+v", test.operation, before, after)
			}
		})
	}
}

func TestCredentialRotationRejectsCapturedAdministratorAfterDemotion(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run Jellyfin credential demotion tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open Jellyfin credential demotion database: %v", err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate Jellyfin credential demotion database: %v", err)
	}
	fixture := seedCredentialStoreFixture(t, ctx, pool)
	store, err := NewCredentialStore(pool, testCredentialRuntimeSettings(t))
	if err != nil {
		t.Fatalf("create Jellyfin credential demotion store: %v", err)
	}
	if _, err := store.Create(ctx, fixture.owner, fixture.profileID); err != nil {
		t.Fatalf("create credential before administrator demotion: %v", err)
	}
	administrator := fixture.manager
	administrator.Role = "admin"
	administrator.AuthorizationScope = auth.AuthorizationScopeGlobalAdministrator
	administrator.CategoryID = nil
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = $1::uuid`, administrator.UserID); err != nil {
		t.Fatalf("establish captured Jellyfin credential administrator user: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE auth_sessions SET authorization_scope = 'global_admin', category_id = NULL WHERE id = $1::uuid`, administrator.SessionID); err != nil {
		t.Fatalf("establish captured Jellyfin credential administrator session: %v", err)
	}
	before := readCredentialMutationState(t, ctx, pool, fixture.profileID)
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'member' WHERE id = $1::uuid`, administrator.UserID); err != nil {
		t.Fatalf("demote captured Jellyfin credential administrator: %v", err)
	}
	rotated, err := store.Rotate(ctx, administrator, fixture.profileID)
	if !errors.Is(err, ErrCredentialForbidden) {
		t.Fatalf("demoted administrator rotation error = %v, want %v", err, ErrCredentialForbidden)
	}
	if rotated.Password != "" || rotated.Username != "" {
		t.Fatalf("demoted administrator rotation returned credential material: %+v", rotated)
	}
	if after := readCredentialMutationState(t, ctx, pool, fixture.profileID); after != before {
		t.Fatalf("demoted administrator changed credential: before=%+v after=%+v", before, after)
	}
}

type credentialMutationResult struct {
	credential ProfileCredential
	err        error
}

type credentialMutationState struct {
	Count      int
	Generation int64
	Active     bool
	Hash       string
}

func readCredentialMutationState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, profileID string) credentialMutationState {
	t.Helper()
	var state credentialMutationState
	if err := pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(max(generation), 0),
		       COALESCE(bool_or(revoked_at IS NULL), false),
		       COALESCE(max(encode(password_hash, 'hex')), '')
		FROM profile_jellyfin_credentials
		WHERE profile_id = $1::uuid
	`, profileID).Scan(&state.Count, &state.Generation, &state.Active, &state.Hash); err != nil {
		t.Fatalf("read Jellyfin credential mutation state: %v", err)
	}
	return state
}

func waitForCredentialSessionLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blockerPID int, result <-chan credentialMutationResult) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE datname = current_database()
				  AND wait_event_type = 'Lock'
				  AND $1::integer = ANY(pg_blocking_pids(pid))
				  AND query LIKE '%FROM auth_sessions%'
			)
		`, blockerPID).Scan(&blocked); err != nil {
			t.Fatalf("inspect blocked Jellyfin credential mutation: %v", err)
		}
		if blocked {
			return
		}
		select {
		case early := <-result:
			t.Fatalf("credential mutation returned before exact session lock release: credential=%+v error=%v", early.credential, early.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("credential mutation did not wait on its exact authentication session row")
		}
		time.Sleep(10 * time.Millisecond)
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
	store, err := NewCredentialStore(pool, testCredentialRuntimeSettings(t))
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

	credentialBarrier, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin credential deletion barrier: %v", err)
	}
	barrierReleased := false
	defer func() {
		if !barrierReleased {
			_ = credentialBarrier.Rollback(context.Background())
		}
	}()
	var barrierPID int
	if err := credentialBarrier.QueryRow(ctx, `
		SELECT pg_backend_pid()
		FROM profile_jellyfin_credentials
		WHERE profile_id = $1::uuid
		FOR UPDATE
	`, fixture.profileID).Scan(&barrierPID); err != nil {
		t.Fatalf("lock credential deletion barrier: %v", err)
	}

	rotationResults := make(chan error, 1)
	go func() {
		_, rotateErr := store.Rotate(ctx, fixture.manager, fixture.profileID)
		rotationResults <- rotateErr
	}()
	rotationPID := waitForCredentialLockWaiter(t, ctx, pool, barrierPID, "profile_jellyfin_credentials", rotationResults)

	deletionResults := make(chan error, 1)
	go func() {
		deletionResults <- profileservice.NewService(pool, time.Hour, "UTC").Delete(ctx, deletePrincipal, fixture.profileID)
	}()
	waitForProfileDeletionWaiter(t, ctx, pool, rotationPID, deletionResults)

	if err := credentialBarrier.Commit(ctx); err != nil {
		t.Fatalf("release credential deletion barrier: %v", err)
	}
	barrierReleased = true
	if err := <-rotationResults; err != nil {
		t.Fatalf("rotation in ordered profile deletion race: %v", err)
	}
	if err := <-deletionResults; err != nil {
		t.Fatalf("delete profile after ordered credential rotation: %v", err)
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
func waitForCredentialLockWaiter(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blockerPID int, queryFragment string, result <-chan error) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiterPID int
		err := pool.QueryRow(ctx, `
			SELECT pid
			FROM pg_stat_activity
			WHERE datname = current_database()
			  AND $1::integer = ANY(pg_blocking_pids(pid))
			  AND query LIKE '%' || $2 || '%'
			ORDER BY pid
			LIMIT 1
		`, blockerPID, queryFragment).Scan(&waiterPID)
		if err == nil {
			return waiterPID
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("observe credential lock waiter: %v", err)
		}
		select {
		case early := <-result:
			t.Fatalf("credential operation returned before barrier release: %v", early)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("query containing %q did not wait on the credential barrier", queryFragment)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForProfileDeletionWaiter(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blockerPID int, result <-chan error) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE datname = current_database()
				  AND $1::integer = ANY(pg_blocking_pids(pid))
				  AND query LIKE '%FROM profiles%'
			)
		`, blockerPID).Scan(&waiting); err != nil {
			t.Fatalf("observe profile deletion waiter: %v", err)
		}
		if waiting {
			return
		}
		select {
		case early := <-result:
			t.Fatalf("profile deletion returned before credential rotation completed: %v", early)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("profile deletion did not wait behind the credential rotation profile lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNativeAndProfileAccessRevocationsCloseExactSockets(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run native revocation socket tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open native revocation socket database: %v", err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate native revocation socket database: %v", err)
	}
	target := seedCredentialStoreFixture(t, ctx, pool)
	unrelated := seedCredentialStoreFixture(t, ctx, pool)
	store, err := NewCredentialStore(pool, testCredentialRuntimeSettings(t))
	if err != nil {
		t.Fatalf("create native revocation credential store: %v", err)
	}
	targetCredential, err := store.Create(ctx, target.owner, target.profileID)
	if err != nil {
		t.Fatalf("create target native revocation credential: %v", err)
	}
	unrelatedCredential, err := store.Create(ctx, unrelated.owner, unrelated.profileID)
	if err != nil {
		t.Fatalf("create unrelated native revocation credential: %v", err)
	}
	targetNativeID, targetCompatID := seedCredentialSession(t, ctx, pool, target, targetCredential)
	_, unrelatedCompatID := seedCredentialSession(t, ctx, pool, unrelated, unrelatedCredential)
	handler := &Handler{bootstrap: newBootstrapRegistry()}
	targetLease, targetOK := handler.bootstrap.acquireSocket(bootstrapSession(targetCompatID, target.profileID, "native-revoke-target"))
	unrelatedLease, unrelatedOK := handler.bootstrap.acquireSocket(bootstrapSession(unrelatedCompatID, unrelated.profileID, "native-revoke-unrelated"))
	if !targetOK || !unrelatedOK {
		t.Fatal("failed to prepare native revocation socket fixtures")
	}
	authentication, err := auth.NewService(pool, time.Hour, 2*time.Hour, "UTC")
	if err != nil {
		t.Fatalf("create native revocation auth service: %v", err)
	}
	authentication.SetCompatibilitySessionRevocationNotifier(handler.ForgetCompatibilitySessions)
	if err := authentication.RevokeSession(ctx, target.owner, targetNativeID); err != nil {
		t.Fatalf("revoke linked native session: %v", err)
	}
	select {
	case <-targetLease.closed:
	default:
		t.Fatal("native session revocation did not close compatibility socket")
	}
	select {
	case <-unrelatedLease.closed:
		t.Fatal("native session revocation closed unrelated compatibility socket")
	default:
	}

	_, bulkCompatID := seedCredentialSession(t, ctx, pool, target, targetCredential)
	bulkLease, bulkOK := handler.bootstrap.acquireSocket(bootstrapSession(bulkCompatID, target.profileID, "account-update-target"))
	if !bulkOK {
		t.Fatal("failed to prepare account update socket")
	}
	users := userservice.NewService(pool)
	users.SetCompatibilitySessionRevocationNotifier(handler.ForgetCompatibilitySessions)
	administrator := target.manager
	administrator.Role = "admin"
	administrator.AuthorizationScope = auth.AuthorizationScopeGlobalAdministrator
	administrator.CategoryID = nil
	password := "updated-password-123"
	if _, err := users.Update(ctx, administrator, target.ownerUserID, userservice.UpdateInput{Password: &password}); err != nil {
		t.Fatalf("update user with linked compatibility session: %v", err)
	}
	select {
	case <-bulkLease.closed:
	default:
		t.Fatal("bulk user session revocation did not close compatibility socket")
	}
	select {
	case <-unrelatedLease.closed:
		t.Fatal("bulk user session revocation closed unrelated socket")
	default:
	}

	grantNativeID, grantCompatID := seedCredentialSession(t, ctx, pool, target, targetCredential)
	grantLease, grantOK := handler.bootstrap.acquireSocket(bootstrapSession(grantCompatID, target.profileID, "grant-revoke-target"))
	if !grantOK {
		t.Fatal("failed to prepare profile access revocation socket")
	}
	users.SetCompatibilitySessionRevocationNotifier(handler.ForgetCompatibilitySessions)
	if err := users.RevokeProfileAccess(ctx, target.manager, target.ownerUserID, target.profileID); err != nil {
		t.Fatalf("revoke linked profile access: %v", err)
	}
	select {
	case <-grantLease.closed:
	default:
		t.Fatal("profile access revocation did not close compatibility socket")
	}
	var nativeStillExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM auth_sessions WHERE id = $1::uuid)`, grantNativeID).Scan(&nativeStillExists); err != nil || !nativeStillExists {
		t.Fatalf("profile access revocation removed native session: exists=%t err=%v", nativeStillExists, err)
	}
	handler.bootstrap.releaseSocket(unrelatedLease)
}

func TestProfileDeleteClosesOnlyDeletedCompatibilitySockets(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run profile deletion socket tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open profile deletion socket database: %v", err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate profile deletion socket database: %v", err)
	}
	fixture := seedCredentialStoreFixture(t, ctx, pool)
	store, err := NewCredentialStore(pool, testCredentialRuntimeSettings(t))
	if err != nil {
		t.Fatalf("create profile deletion credential store: %v", err)
	}
	credential, err := store.Create(ctx, fixture.owner, fixture.profileID)
	if err != nil {
		t.Fatalf("create profile deletion credential: %v", err)
	}
	_, compatSessionID := seedCredentialSession(t, ctx, pool, fixture, credential)
	handler := &Handler{bootstrap: newBootstrapRegistry()}
	target := bootstrapSession(compatSessionID, fixture.profileID, "deleted-profile-device")
	foreign := bootstrapSession("c2000000-0000-4000-8000-000000000001", *fixture.outsider.ActiveProfileID, "foreign-device")
	targetLease, targetOK := handler.bootstrap.acquireSocket(target)
	foreignLease, foreignOK := handler.bootstrap.acquireSocket(foreign)
	if !targetOK || !foreignOK {
		t.Fatal("failed to prepare profile deletion socket fixtures")
	}
	profiles := profileservice.NewService(pool, time.Hour, "UTC")
	profiles.SetCompatibilitySessionRevocationNotifier(handler.ForgetCompatibilitySessions)
	deletePrincipal := fixture.manager
	deletePrincipal.Role = "admin"
	deletePrincipal.AuthorizationScope = auth.AuthorizationScopeGlobalAdministrator
	deletePrincipal.CategoryID = nil
	if err := profiles.Delete(ctx, deletePrincipal, fixture.profileID); err != nil {
		t.Fatalf("delete profile with compatibility socket: %v", err)
	}
	select {
	case <-targetLease.closed:
	default:
		t.Fatal("deleted profile compatibility socket remained open")
	}
	select {
	case <-foreignLease.closed:
		t.Fatal("unrelated compatibility socket closed during profile deletion")
	default:
	}
	handler.bootstrap.releaseSocket(foreignLease)
}

func TestProfileAccessChangeClosesSocketButMetadataEditDoesNot(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run profile access socket tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open profile access socket database: %v", err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate profile access socket database: %v", err)
	}
	fixture := seedCredentialStoreFixture(t, ctx, pool)
	store, err := NewCredentialStore(pool, testCredentialRuntimeSettings(t))
	if err != nil {
		t.Fatalf("create profile access credential store: %v", err)
	}
	credential, err := store.Create(ctx, fixture.owner, fixture.profileID)
	if err != nil {
		t.Fatalf("create profile access credential: %v", err)
	}
	_, compatSessionID := seedCredentialSession(t, ctx, pool, fixture, credential)
	handler := &Handler{bootstrap: newBootstrapRegistry()}
	lease, acquired := handler.bootstrap.acquireSocket(bootstrapSession(compatSessionID, fixture.profileID, "access-change-device"))
	if !acquired {
		t.Fatal("failed to prepare profile access socket fixture")
	}
	profiles := profileservice.NewService(pool, time.Hour, "UTC")
	var notifiedSessionIDs [][]string
	profiles.SetCompatibilitySessionRevocationNotifier(func(sessionIDs []string) {
		notifiedSessionIDs = append(notifiedSessionIDs, append([]string(nil), sessionIDs...))
		handler.ForgetCompatibilitySessions(sessionIDs)
	})
	name := "Metadata-only edit " + fmt.Sprintf("%d", time.Now().UnixNano())
	if _, err := profiles.Update(ctx, fixture.manager, fixture.profileID, profileservice.UpdateInput{Name: &name}); err != nil {
		t.Fatalf("update profile metadata: %v", err)
	}
	if len(notifiedSessionIDs) != 0 {
		t.Fatalf("metadata-only edit published compatibility revocations: %#v", notifiedSessionIDs)
	}
	select {
	case <-lease.closed:
		t.Fatal("metadata-only profile edit closed compatibility socket")
	default:
	}
	pin := "4321"
	if _, err := profiles.Update(ctx, fixture.manager, fixture.profileID, profileservice.UpdateInput{PINSet: true, PIN: &pin}); err != nil {
		t.Fatalf("update profile access PIN: %v", err)
	}
	if len(notifiedSessionIDs) != 1 || len(notifiedSessionIDs[0]) != 1 || notifiedSessionIDs[0][0] != compatSessionID {
		t.Fatalf("profile access compatibility revocations = %#v, want only %q", notifiedSessionIDs, compatSessionID)
	}
	select {
	case <-lease.closed:
	default:
		t.Fatal("profile access change did not close compatibility socket after commit")
	}
}

func TestProfileCategoryChangeClosesCompatibilitySocket(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run profile category socket tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open profile category socket database: %v", err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate profile category socket database: %v", err)
	}
	fixture := seedCredentialStoreFixture(t, ctx, pool)
	store, err := NewCredentialStore(pool, testCredentialRuntimeSettings(t))
	if err != nil {
		t.Fatalf("create profile category credential store: %v", err)
	}
	credential, err := store.Create(ctx, fixture.owner, fixture.profileID)
	if err != nil {
		t.Fatalf("create profile category credential: %v", err)
	}
	_, compatSessionID := seedCredentialSession(t, ctx, pool, fixture, credential)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var destinationCategoryID, destinationProfileID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		SELECT $1, $2, COALESCE(max(position), -1) + 1 FROM access_categories
		RETURNING id::text
	`, "Profile category destination "+suffix, "profile-category-destination-"+suffix).Scan(&destinationCategoryID); err != nil {
		t.Fatalf("create profile category destination: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name, category_id) VALUES ($1, $2::uuid)
		RETURNING id::text
	`, "Profile category destination peer "+suffix, destinationCategoryID).Scan(&destinationProfileID); err != nil {
		t.Fatalf("create profile category destination peer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id = ANY($1::uuid[])`, []string{fixture.profileID, destinationProfileID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM access_categories WHERE id = $1::uuid`, destinationCategoryID)
	})
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = $1::uuid`, fixture.manager.UserID); err != nil {
		t.Fatalf("authorize profile category administrator: %v", err)
	}
	handler := &Handler{bootstrap: newBootstrapRegistry()}
	lease, acquired := handler.bootstrap.acquireSocket(bootstrapSession(compatSessionID, fixture.profileID, "category-change-device"))
	if !acquired {
		t.Fatal("failed to prepare profile category socket fixture")
	}
	profiles := profileservice.NewService(pool, time.Hour, "UTC")
	profiles.SetCompatibilitySessionRevocationNotifier(handler.ForgetCompatibilitySessions)
	administrator := fixture.manager
	administrator.Role = "admin"
	administrator.AuthorizationScope = auth.AuthorizationScopeGlobalAdministrator
	administrator.CategoryID = nil
	if _, err := profiles.Update(ctx, administrator, fixture.profileID, profileservice.UpdateInput{CategoryID: &destinationCategoryID}); err != nil {
		t.Fatalf("move profile category with compatibility socket: %v", err)
	}
	select {
	case <-lease.closed:
	default:
		t.Fatal("category-changed profile compatibility socket remained open")
	}
}

func TestProfileDisableRevokesLinkedCredentialsWithoutAffectingOtherProfiles(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run profile disable credential tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open profile disable credential database: %v", err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate profile disable credential database: %v", err)
	}
	target := seedCredentialStoreFixture(t, ctx, pool)
	unrelated := seedCredentialStoreFixture(t, ctx, pool)
	credentialStore, err := NewCredentialStore(pool, testCredentialRuntimeSettings(t))
	if err != nil {
		t.Fatalf("create profile credential store: %v", err)
	}
	targetCredential, err := credentialStore.Create(ctx, target.owner, target.profileID)
	if err != nil {
		t.Fatalf("create target profile credential: %v", err)
	}
	unrelatedCredential, err := credentialStore.Create(ctx, unrelated.owner, unrelated.profileID)
	if err != nil {
		t.Fatalf("create unrelated profile credential: %v", err)
	}
	authentication, err := auth.NewService(pool, 15*time.Minute, 2*time.Hour, "UTC")
	if err != nil {
		t.Fatalf("create linked authentication service: %v", err)
	}
	login := func(label string, credential ProfileCredential) auth.JellyfinProfileLoginResult {
		t.Helper()
		result, loginErr := authentication.LoginJellyfinProfile(ctx, auth.JellyfinProfileLoginInput{
			Username: credential.Username, Password: credential.Password,
			LinkedDeviceKey: "profile-disable-" + label + "-" + credential.Username,
			DeviceName:      "Profile disable " + label, Platform: "jellyfin-test",
		})
		if loginErr != nil {
			t.Fatalf("login %s profile credential: %v", label, loginErr)
		}
		return result
	}
	targetLogin := login("target", targetCredential)
	unrelatedLogin := login("unrelated", unrelatedCredential)
	targetPrincipal, err := authentication.Authenticate(ctx, targetLogin.Tokens.AccessToken)
	if err != nil {
		t.Fatalf("authenticate target native token before disable: %v", err)
	}
	unrelatedPrincipal, err := authentication.Authenticate(ctx, unrelatedLogin.Tokens.AccessToken)
	if err != nil {
		t.Fatalf("authenticate unrelated native token before disable: %v", err)
	}
	compatibility, err := NewSessionStore(pool, authentication)
	if err != nil {
		t.Fatalf("create compatibility session store: %v", err)
	}
	targetCompat, err := compatibility.Issue(ctx, targetPrincipal, target.profileID, ClientIdentity{
		Client: "Jellyfin Test", Device: "Target client", DeviceID: "target-" + targetCredential.Username, Version: "1.0",
	}, time.Now().UTC().Add(30*time.Minute))
	if err != nil {
		t.Fatalf("issue target compatibility session: %v", err)
	}
	unrelatedCompat, err := compatibility.Issue(ctx, unrelatedPrincipal, unrelated.profileID, ClientIdentity{
		Client: "Jellyfin Test", Device: "Unrelated client", DeviceID: "unrelated-" + unrelatedCredential.Username, Version: "1.0",
	}, time.Now().UTC().Add(30*time.Minute))
	if err != nil {
		t.Fatalf("issue unrelated compatibility session: %v", err)
	}
	handler := &Handler{bootstrap: newBootstrapRegistry()}
	targetLease, targetLeaseOK := handler.bootstrap.acquireSocket(bootstrapSession(targetCompat.SessionID, target.profileID, "disabled-profile-device"))
	unrelatedLease, unrelatedLeaseOK := handler.bootstrap.acquireSocket(bootstrapSession(unrelatedCompat.SessionID, unrelated.profileID, "unrelated-profile-device"))
	if !targetLeaseOK || !unrelatedLeaseOK {
		t.Fatal("failed to prepare profile disable socket fixtures")
	}
	profiles := profileservice.NewService(pool, time.Hour, "UTC")
	var notifiedSessionIDs [][]string
	profiles.SetCompatibilitySessionRevocationNotifier(func(sessionIDs []string) {
		notifiedSessionIDs = append(notifiedSessionIDs, append([]string(nil), sessionIDs...))
		handler.ForgetCompatibilitySessions(sessionIDs)
	})
	disabled := false
	dropFailure := installCredentialSessionRevocationFailure(t, ctx, pool, targetLogin.Tokens.SessionID)
	if _, err := profiles.Update(ctx, target.manager, target.profileID, profileservice.UpdateInput{Enabled: &disabled}); err == nil {
		t.Fatal("profile disable succeeded despite linked-session revocation failure")
	}
	if len(notifiedSessionIDs) != 0 {
		t.Fatalf("rolled-back profile disable published compatibility revocations: %#v", notifiedSessionIDs)
	}
	select {
	case <-targetLease.closed:
		t.Fatal("rolled-back profile disable closed its compatibility socket")
	default:
	}
	dropFailure()
	var targetEnabled bool
	if err := pool.QueryRow(ctx, `SELECT enabled FROM profiles WHERE id = $1::uuid`, target.profileID).Scan(&targetEnabled); err != nil {
		t.Fatalf("read target profile after failed disable: %v", err)
	}
	if !targetEnabled {
		t.Fatal("failed session revocation committed the profile disable")
	}
	if _, err := authentication.Authenticate(ctx, targetLogin.Tokens.AccessToken); err != nil {
		t.Fatalf("failed profile disable invalidated reusable native session: %v", err)
	}
	if _, err := compatibility.Authenticate(ctx, targetCompat.Token); err != nil {
		t.Fatalf("failed profile disable invalidated reusable compatibility session: %v", err)
	}
	if _, err := profiles.Update(ctx, target.manager, target.profileID, profileservice.UpdateInput{Enabled: &disabled}); err != nil {
		t.Fatalf("disable target profile: %v", err)
	}
	if len(notifiedSessionIDs) != 1 || len(notifiedSessionIDs[0]) != 1 || notifiedSessionIDs[0][0] != targetCompat.SessionID {
		t.Fatalf("profile disable compatibility revocations = %#v, want only %q", notifiedSessionIDs, targetCompat.SessionID)
	}
	select {
	case <-targetLease.closed:
	default:
		t.Fatal("profile disable did not close its compatibility socket after commit")
	}
	select {
	case <-unrelatedLease.closed:
		t.Fatal("profile disable closed an unrelated compatibility socket")
	default:
	}
	enabled := true
	if _, err := profiles.Update(ctx, target.manager, target.profileID, profileservice.UpdateInput{Enabled: &enabled}); err != nil {
		t.Fatalf("re-enable target profile: %v", err)
	}
	if _, err := authentication.Authenticate(ctx, targetLogin.Tokens.AccessToken); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("target native access token after re-enable error = %v, want %v", err, auth.ErrInvalidToken)
	}
	if _, err := compatibility.Authenticate(ctx, targetCompat.Token); !errors.Is(err, ErrInvalidCompatCredential) {
		t.Fatalf("target compatibility token after re-enable error = %v, want %v", err, ErrInvalidCompatCredential)
	}
	if _, err := authentication.Authenticate(ctx, unrelatedLogin.Tokens.AccessToken); err != nil {
		t.Fatalf("unrelated native session after target disable: %v", err)
	}
	if _, err := compatibility.Authenticate(ctx, unrelatedCompat.Token); err != nil {
		t.Fatalf("unrelated compatibility session after target disable: %v", err)
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

func testCredentialRuntimeSettings(t *testing.T) *runtimesettings.Source {
	t.Helper()
	source, err := runtimesettings.New(runtimesettings.Values{
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
		t.Fatalf("create Jellyfin credential runtime settings: %v", err)
	}
	return source
}

func seedCredentialStoreFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) credentialStoreFixture {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	fixture := credentialStoreFixture{}
	var managerUserID, outsiderUserID, outsiderProfileID string
	userDeviceIDs := make(map[string]string)
	userSessionIDs := make(map[string]string)
	userContextHashes := make(map[string][]byte)
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
	for name, userID := range map[string]string{
		"owner": fixture.ownerUserID, "manager": managerUserID, "outsider": outsiderUserID,
	} {
		activeProfileID := fixture.profileID
		if name == "outsider" {
			activeProfileID = outsiderProfileID
		}
		var deviceID, sessionID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO devices (user_id, name, platform, category_id, approved_at)
			VALUES ($1::uuid, $2, 'credential-test', $3::uuid, now())
			RETURNING id::text
		`, userID, "Jellyfin credential "+name+" device "+suffix, fixture.categoryID).Scan(&deviceID); err != nil {
			t.Fatalf("insert Jellyfin credential %s device: %v", name, err)
		}
		accessHash := sha256.Sum256([]byte("jellyfin-credential-native-access-" + name + "-" + suffix))
		contextHash := sha256.Sum256([]byte("jellyfin-credential-native-context-" + name + "-" + suffix))
		if err := pool.QueryRow(ctx, `
			INSERT INTO auth_sessions (
				user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
				authorization_scope, category_id, active_profile_id, profile_grant_expires_at,
				profile_context_hash
			) VALUES (
				$1::uuid, $2::uuid, $3, now() + interval '1 hour', now() + interval '2 hours',
				'category', $4::uuid, $5::uuid, now() + interval '1 hour', $6
			) RETURNING id::text
		`, userID, deviceID, accessHash[:], fixture.categoryID, activeProfileID, contextHash[:]).Scan(&sessionID); err != nil {
			t.Fatalf("insert Jellyfin credential %s native session: %v", name, err)
		}
		userDeviceIDs[name] = deviceID
		userSessionIDs[name] = sessionID
		userContextHashes[name] = contextHash[:]
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1::uuid[])`, []string{fixture.ownerUserID, managerUserID, outsiderUserID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id = ANY($1::uuid[])`, []string{fixture.profileID, outsiderProfileID})
	})
	expiresAt := time.Now().UTC().Add(time.Hour)
	fixture.owner = auth.Principal{
		SessionID: userSessionIDs["owner"], UserID: fixture.ownerUserID, DeviceID: userDeviceIDs["owner"],
		Role: "member", AuthorizationScope: auth.AuthorizationScopeCategory,
		CategoryID: &fixture.categoryID, ActiveProfileID: &fixture.profileID, ProfileGrantExpiresAt: &expiresAt,
		ProfileContextHash: userContextHashes["owner"],
	}
	fixture.manager = auth.Principal{
		SessionID: userSessionIDs["manager"], UserID: managerUserID, DeviceID: userDeviceIDs["manager"],
		Role: "member", AuthorizationScope: auth.AuthorizationScopeCategory,
		CategoryID: &fixture.categoryID, ActiveProfileID: &fixture.profileID, ProfileGrantExpiresAt: &expiresAt,
		ProfileContextHash: userContextHashes["manager"], ActiveProfileCanManage: true,
	}
	fixture.outsider = auth.Principal{
		SessionID: userSessionIDs["outsider"], UserID: outsiderUserID, DeviceID: userDeviceIDs["outsider"],
		Role: "member", AuthorizationScope: auth.AuthorizationScopeCategory,
		CategoryID: &fixture.categoryID, ActiveProfileID: &outsiderProfileID, ProfileGrantExpiresAt: &expiresAt,
		ProfileContextHash: userContextHashes["outsider"],
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
