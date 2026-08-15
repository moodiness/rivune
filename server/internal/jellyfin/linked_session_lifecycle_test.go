package jellyfin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
)

func TestLinkedScopeMismatchPublishesExactCompatibilityRevocationsAfterCommit(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run linked compatibility lifecycle tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open linked compatibility lifecycle database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate linked compatibility lifecycle database: %v", err)
	}

	target := seedCredentialStoreFixture(t, ctx, pool)
	unrelated := seedCredentialStoreFixture(t, ctx, pool)
	credentials, err := NewCredentialStore(pool, testCredentialRuntimeSettings(t))
	if err != nil {
		t.Fatalf("create linked compatibility credential store: %v", err)
	}
	targetCredential, err := credentials.Create(ctx, target.owner, target.profileID)
	if err != nil {
		t.Fatalf("create target linked credential: %v", err)
	}
	unrelatedCredential, err := credentials.Create(ctx, unrelated.owner, unrelated.profileID)
	if err != nil {
		t.Fatalf("create unrelated linked credential: %v", err)
	}
	authentication, err := auth.NewService(pool, 15*time.Minute, 2*time.Hour, "UTC")
	if err != nil {
		t.Fatalf("create linked authentication service: %v", err)
	}
	login := func(label string, credential ProfileCredential) auth.JellyfinProfileLoginResult {
		t.Helper()
		result, loginErr := authentication.LoginJellyfinProfile(ctx, auth.JellyfinProfileLoginInput{
			Username: credential.Username, Password: credential.Password,
			LinkedDeviceKey: "scope-mismatch-" + label + "-" + credential.Username,
			DeviceName:      "Scope mismatch " + label, Platform: "jellyfin-test",
		})
		if loginErr != nil {
			t.Fatalf("login %s linked credential: %v", label, loginErr)
		}
		return result
	}
	targetLogin := login("target", targetCredential)
	unrelatedLogin := login("unrelated", unrelatedCredential)
	targetPrincipal, err := authentication.Authenticate(ctx, targetLogin.Tokens.AccessToken)
	if err != nil {
		t.Fatalf("authenticate target linked session: %v", err)
	}
	unrelatedPrincipal, err := authentication.Authenticate(ctx, unrelatedLogin.Tokens.AccessToken)
	if err != nil {
		t.Fatalf("authenticate unrelated linked session: %v", err)
	}
	compatibility, err := NewSessionStore(pool, authentication)
	if err != nil {
		t.Fatalf("create linked compatibility session store: %v", err)
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

	var mismatchCategoryID string
	categorySuffix := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		VALUES ($1, $2, (SELECT COALESCE(max(position), 0) + 1 FROM access_categories))
		RETURNING id::text
	`, "Linked mismatch "+categorySuffix, "linked-mismatch-"+categorySuffix).Scan(&mismatchCategoryID); err != nil {
		t.Fatalf("create linked mismatch category: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE devices SET category_id = $2::uuid WHERE id = $1::uuid`, targetPrincipal.DeviceID, target.categoryID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM access_categories WHERE id = $1::uuid`, mismatchCategoryID)
	})
	if _, err := pool.Exec(ctx, `UPDATE devices SET category_id = $2::uuid WHERE id = $1::uuid`, targetPrincipal.DeviceID, mismatchCategoryID); err != nil {
		t.Fatalf("create linked session scope mismatch: %v", err)
	}

	handler, _, _, _ := newBootstrapHTTPFixture(t, true)
	targetSession := bootstrapSession(targetCompat.SessionID, target.profileID, "scope-mismatch-target")
	unrelatedSession := bootstrapSession(unrelatedCompat.SessionID, unrelated.profileID, "scope-mismatch-unrelated")
	targetLease, targetOK := handler.bootstrap.acquireSocket(targetSession)
	unrelatedLease, unrelatedOK := handler.bootstrap.acquireSocket(unrelatedSession)
	if !targetOK || !unrelatedOK {
		t.Fatal("failed to prepare linked scope mismatch socket fixtures")
	}
	handler.playSessions.entries["target-play"] = &playSessionEntry{
		compatSessionID: targetCompat.SessionID, playSessionID: "target-play", expiresAt: targetSession.ExpiresAt, sources: map[string]*playSessionSource{},
	}
	handler.playSessions.entries["unrelated-play"] = &playSessionEntry{
		compatSessionID: unrelatedCompat.SessionID, playSessionID: "unrelated-play", expiresAt: unrelatedSession.ExpiresAt, sources: map[string]*playSessionSource{},
	}
	var notifications [][]string
	authentication.SetCompatibilitySessionRevocationNotifier(func(sessionIDs []string) {
		notifications = append(notifications, append([]string(nil), sessionIDs...))
		handler.ForgetCompatibilitySessions(sessionIDs)
		panic("injected in-memory linked revocation failure")
	})

	dropRollbackFailure := installCredentialSessionRevocationFailure(t, ctx, pool, targetLogin.Tokens.SessionID)
	if _, err := authentication.ReloadLinkedPrincipal(ctx, targetLogin.Tokens.SessionID, target.profileID); err == nil || errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("scope mismatch rollback error = %v, want revocation error", err)
	}
	if len(notifications) != 0 {
		t.Fatalf("rolled-back scope mismatch published compatibility revocations: %#v", notifications)
	}
	select {
	case <-targetLease.closed:
		t.Fatal("rolled-back scope mismatch closed the target compatibility socket")
	default:
	}
	if handler.playSessions.entries["target-play"] == nil {
		t.Fatal("rolled-back scope mismatch retired the target compatibility playback session")
	}
	assertCredentialSessionsActive(t, ctx, pool, targetLogin.Tokens.SessionID, targetCompat.SessionID)
	dropRollbackFailure()

	dropCommitFailure := installLinkedSessionCommitFailure(t, ctx, pool, targetLogin.Tokens.SessionID)
	if _, err := authentication.ReloadLinkedPrincipal(ctx, targetLogin.Tokens.SessionID, target.profileID); err == nil || errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("scope mismatch commit failure error = %v, want commit error", err)
	}
	if len(notifications) != 0 {
		t.Fatalf("failed commit published compatibility revocations: %#v", notifications)
	}
	select {
	case <-targetLease.closed:
		t.Fatal("failed commit closed the target compatibility socket")
	default:
	}
	if handler.playSessions.entries["target-play"] == nil {
		t.Fatal("failed commit retired the target compatibility playback session")
	}
	assertCredentialSessionsActive(t, ctx, pool, targetLogin.Tokens.SessionID, targetCompat.SessionID)
	dropCommitFailure()

	if _, err := authentication.ReloadLinkedPrincipal(ctx, targetLogin.Tokens.SessionID, target.profileID); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("committed scope mismatch error = %v, want %v", err, auth.ErrInvalidToken)
	}
	if len(notifications) != 1 || len(notifications[0]) != 1 || notifications[0][0] != targetCompat.SessionID {
		t.Fatalf("scope mismatch compatibility revocations = %#v, want only %q", notifications, targetCompat.SessionID)
	}
	select {
	case <-targetLease.closed:
	default:
		t.Fatal("committed scope mismatch left target compatibility socket open")
	}
	select {
	case <-unrelatedLease.closed:
		t.Fatal("committed scope mismatch closed an unrelated compatibility socket")
	default:
	}
	if handler.playSessions.entries["target-play"] != nil || handler.playSessions.entries["unrelated-play"] == nil {
		t.Fatalf("scope mismatch playback retirement target=%t unrelated=%t", handler.playSessions.entries["target-play"] != nil, handler.playSessions.entries["unrelated-play"] != nil)
	}
	assertCredentialSessionsRevoked(t, ctx, pool, targetLogin.Tokens.SessionID, targetCompat.SessionID)
	assertCredentialSessionsActive(t, ctx, pool, unrelatedLogin.Tokens.SessionID, unrelatedCompat.SessionID)
	handler.bootstrap.releaseSocket(unrelatedLease)
}

func installLinkedSessionCommitFailure(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID string) func() {
	t.Helper()
	identifier := fmt.Sprintf("linked_session_commit_failure_%d", time.Now().UnixNano())
	functionName := identifier + "_fn"
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $function$
		BEGIN
			IF NEW.id::text = '%s' AND OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL THEN
				RAISE EXCEPTION 'injected linked session commit failure';
			END IF;
			RETURN NEW;
		END
		$function$;
		CREATE CONSTRAINT TRIGGER %s
		AFTER UPDATE ON auth_sessions
		DEFERRABLE INITIALLY DEFERRED
		FOR EACH ROW EXECUTE FUNCTION %s();
	`, functionName, sessionID, identifier, functionName)); err != nil {
		t.Fatalf("install linked session commit failure: %v", err)
	}
	dropped := false
	drop := func() {
		if dropped {
			return
		}
		dropped = true
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`
			DROP TRIGGER IF EXISTS %s ON auth_sessions;
			DROP FUNCTION IF EXISTS %s();
		`, identifier, functionName))
	}
	t.Cleanup(drop)
	return drop
}
