package jellyfin

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
	"github.com/moodiness/rivune/server/internal/profile"
)

const (
	authLifecycleUserID          = "a1000000-0000-4000-8000-000000000001"
	authLifecycleProfileID       = "a2000000-0000-4000-8000-000000000002"
	authLifecycleNativeSessionID = "a3000000-0000-4000-8000-000000000003"
	authLifecycleCompatSessionID = "a4000000-0000-4000-8000-000000000004"
	authLifecycleAccessToken     = "rivune_at_auth-lifecycle-secret"
)

type authLifecycleNativeFake struct {
	authenticateErr error
	cleanup         func(context.Context, string) error
	logout          func(context.Context, auth.Principal, string) error
	cleanupCalls    atomic.Int32
	logoutCalls     atomic.Int32
}

func (fake *authLifecycleNativeFake) Authenticate(context.Context, string) (auth.Principal, error) {
	return auth.Principal{}, fake.authenticateErr
}

func (*authLifecycleNativeFake) ReloadLinkedPrincipal(context.Context, string, string) (auth.Principal, error) {
	return auth.Principal{}, errors.New("unexpected linked principal reload")
}

func (fake *authLifecycleNativeFake) RevokeUnfinishedLinkedSession(ctx context.Context, sessionID string) error {
	fake.cleanupCalls.Add(1)
	if fake.cleanup == nil {
		return nil
	}
	return fake.cleanup(ctx, sessionID)
}

func (*authLifecycleNativeFake) Account(context.Context, auth.Principal) (auth.Account, error) {
	return auth.Account{}, errors.New("unexpected account lookup")
}

func (fake *authLifecycleNativeFake) LogoutLinkedSession(ctx context.Context, principal auth.Principal, compatSessionID string) error {
	fake.logoutCalls.Add(1)
	if fake.logout == nil {
		return nil
	}
	return fake.logout(ctx, principal, compatSessionID)
}

type authLifecycleProfileSelector struct{}

func (authLifecycleProfileSelector) SelectForLinkedSession(context.Context, auth.Principal, string, *string, bool) (profile.Selection, error) {
	return profile.Selection{}, errors.New("unexpected profile selection")
}

func TestFailedCompatLoginCleanupIsDetachedFromRequestCancellation(t *testing.T) {
	active := true
	cleanupWasDetached := false
	cleanupSessionID := ""
	native := &authLifecycleNativeFake{
		authenticateErr: errors.New("new native session could not be authenticated"),
		cleanup: func(ctx context.Context, sessionID string) error {
			cleanupWasDetached = ctx.Err() == nil
			cleanupSessionID = sessionID
			active = false
			return nil
		},
	}
	service := newAuthLifecycleService(t, native)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := service.Login(ctx, validAuthLifecycleLogin())
	if err == nil || errors.Is(err, ErrCompatLoginCleanup) {
		t.Fatalf("login error = %v, want original post-login failure", err)
	}
	if native.cleanupCalls.Load() != 1 || !cleanupWasDetached || active || cleanupSessionID != authLifecycleNativeSessionID {
		t.Fatalf("cleanup calls=%d detached=%t nativeActive=%t session=%q", native.cleanupCalls.Load(), cleanupWasDetached, active, cleanupSessionID)
	}
}

func TestFailedCompatLoginCleanupTimesOutEvenWhenDependencyIgnoresCancellation(t *testing.T) {
	releaseCleanup := make(chan struct{})
	cleanupFinished := make(chan struct{})
	native := &authLifecycleNativeFake{
		authenticateErr: errors.New("force failed login after native issue"),
		cleanup: func(context.Context, string) error {
			defer close(cleanupFinished)
			<-releaseCleanup
			return nil
		},
	}
	service := newAuthLifecycleService(t, native)
	service.failedLoginCleanupTimeout = 20 * time.Millisecond
	started := time.Now()

	_, err := service.Login(context.Background(), validAuthLifecycleLogin())
	if !errors.Is(err, ErrCompatLoginCleanup) {
		t.Fatalf("login error = %v, want cleanup timeout", err)
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatalf("failed-login cleanup exceeded its bounded timeout")
	}
	if native.cleanupCalls.Load() != 1 {
		t.Fatalf("cleanup calls = %d, want 1", native.cleanupCalls.Load())
	}
	close(releaseCleanup)
	select {
	case <-cleanupFinished:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed-out cleanup did not observe its release")
	}
}

func TestFailedCompatLoginCleanupRedactsImmediateFailure(t *testing.T) {
	secretSessionID := authLifecycleNativeSessionID
	native := &authLifecycleNativeFake{
		authenticateErr: errors.New("force failed login after native issue"),
		cleanup: func(context.Context, string) error {
			return fmt.Errorf("cleanup driver failure token=%s session=%s", authLifecycleAccessToken, secretSessionID)
		},
	}
	service := newAuthLifecycleService(t, native)

	_, err := service.Login(context.Background(), validAuthLifecycleLogin())
	if !errors.Is(err, ErrCompatLoginCleanup) || err.Error() != ErrCompatLoginCleanup.Error() {
		t.Fatalf("login error = %q, want redacted cleanup failure", err)
	}
	for _, forbidden := range []string{authLifecycleAccessToken, secretSessionID, "driver failure", "token="} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("cleanup error exposed %q: %q", forbidden, err)
		}
	}
}

func TestCompatLogoutFailureIsRetryableAndUsesAuthoritativeLinkedRevocation(t *testing.T) {
	profileID := authLifecycleProfileID
	session := AuthenticatedSession{
		ID:        authLifecycleCompatSessionID,
		ProfileID: profileID,
		Principal: auth.Principal{
			SessionID:       authLifecycleNativeSessionID,
			UserID:          authLifecycleUserID,
			ActiveProfileID: &profileID,
		},
	}
	nativeActive, compatActive := true, true
	attempt := 0
	native := &authLifecycleNativeFake{logout: func(_ context.Context, principal auth.Principal, compatSessionID string) error {
		attempt++
		if principal.SessionID != authLifecycleNativeSessionID || compatSessionID != authLifecycleCompatSessionID {
			t.Fatalf("logout lost authoritative link: native=%q compat=%q", principal.SessionID, compatSessionID)
		}
		if attempt == 1 {
			return errors.New("injected atomic update failure")
		}
		nativeActive = false
		compatActive = false
		return nil
	}}
	service := &AuthenticationService{native: native}

	if err := service.Logout(context.Background(), session); err == nil {
		t.Fatal("first logout unexpectedly succeeded")
	}
	if !nativeActive || !compatActive {
		t.Fatalf("failed logout partially revoked state: native=%t compat=%t", nativeActive, compatActive)
	}
	if err := service.Logout(context.Background(), session); err != nil {
		t.Fatalf("retry logout: %v", err)
	}
	if nativeActive || compatActive || native.logoutCalls.Load() != 2 {
		t.Fatalf("retry state native=%t compat=%t calls=%d", nativeActive, compatActive, native.logoutCalls.Load())
	}
}

func TestAtomicLinkedLogoutRollsBackNativeAndCompatFailuresAndRetries(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run linked compatibility logout transaction tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open linked logout database: %v", err)
	}
	t.Cleanup(pool.Close)
	native, err := auth.NewService(pool, time.Hour, 24*time.Hour, "UTC")
	if err != nil {
		t.Fatalf("create native authentication service: %v", err)
	}
	service := &AuthenticationService{native: native}

	for _, target := range []string{"auth_sessions", "jellyfin_compat_sessions"} {
		t.Run(target, func(t *testing.T) {
			fixture := seedAtomicLogoutFixture(t, pool)
			dropFailure := installLogoutFailureTrigger(t, pool, target, fixture)
			if err := service.Logout(ctx, fixture.session); err == nil {
				t.Fatalf("logout succeeded with injected %s failure", target)
			}
			assertAtomicLogoutState(t, pool, fixture, false, time.Time{}, time.Time{})

			dropFailure()
			if err := service.Logout(ctx, fixture.session); err != nil {
				t.Fatalf("retry linked logout: %v", err)
			}
			nativeRevokedAt, compatRevokedAt := assertAtomicLogoutState(t, pool, fixture, true, time.Time{}, time.Time{})
			if err := service.Logout(ctx, fixture.session); err != nil {
				t.Fatalf("idempotent linked logout: %v", err)
			}
			assertAtomicLogoutState(t, pool, fixture, true, nativeRevokedAt, compatRevokedAt)
		})
	}
}

func TestAtomicLinkedLogoutHonorsCancellationWithoutPartialRevocation(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run linked compatibility logout cancellation test")
	}
	pool, err := database.Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open linked logout database: %v", err)
	}
	t.Cleanup(pool.Close)
	native, err := auth.NewService(pool, time.Hour, 24*time.Hour, "UTC")
	if err != nil {
		t.Fatalf("create native authentication service: %v", err)
	}
	fixture := seedAtomicLogoutFixture(t, pool)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = (&AuthenticationService{native: native}).Logout(ctx, fixture.session)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled logout error = %v, want context cancellation", err)
	}
	assertAtomicLogoutState(t, pool, fixture, false, time.Time{}, time.Time{})
}

func newAuthLifecycleService(t *testing.T, native *authLifecycleNativeFake) *AuthenticationService {
	t.Helper()
	service, err := NewAuthenticationService(
		func(context.Context, auth.LoginInput) (auth.TokenPair, error) {
			return auth.TokenPair{AccessToken: authLifecycleAccessToken, SessionID: authLifecycleNativeSessionID}, nil
		},
		native,
		authLifecycleProfileSelector{},
		&SessionStore{},
	)
	if err != nil {
		t.Fatalf("create authentication service: %v", err)
	}
	return service
}

func validAuthLifecycleLogin() CompatLoginInput {
	return CompatLoginInput{
		Username: "owner/Main",
		Password: "correct horse battery staple",
		Client: ClientIdentity{
			Client: "Infuse", Device: "Living Room", DeviceID: "auth-lifecycle-device", Version: "8.2",
		},
	}
}

type atomicLogoutFixture struct {
	userID, profileID, authSessionID, compatSessionID string
	session                                           AuthenticatedSession
}

func seedAtomicLogoutFixture(t *testing.T, pool *pgxpool.Pool) atomicLogoutFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	fixture := atomicLogoutFixture{}
	var categoryID, deviceID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-atomic-logout-hash', 'member')
		RETURNING id::text
	`, "compat_logout_"+suffix).Scan(&fixture.userID); err != nil {
		t.Fatalf("insert linked logout user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id::text = $1", fixture.userID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM profiles WHERE id::text = $1", fixture.profileID)
	})
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name) VALUES ($1)
		RETURNING id::text, category_id::text
	`, "Atomic logout "+suffix).Scan(&fixture.profileID, &categoryID); err != nil {
		t.Fatalf("insert linked logout profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id) VALUES ($1, $2)
	`, fixture.userID, fixture.profileID); err != nil {
		t.Fatalf("grant linked logout profile: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1, $2, 'Infuse', $3, now()) RETURNING id::text
	`, fixture.userID, "Atomic logout device "+suffix, categoryID).Scan(&deviceID); err != nil {
		t.Fatalf("insert linked logout device: %v", err)
	}
	accessHash := sha256.Sum256([]byte("atomic-logout-access-" + suffix))
	contextHash := sha256.Sum256([]byte("atomic-logout-context-" + suffix))
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id, active_profile_id, profile_grant_expires_at,
			profile_context_hash
		) VALUES (
			$1, $2, $3, now() + interval '1 hour', now() + interval '2 hours',
			'category', $4, $5, now() + interval '2 hours', $6
		) RETURNING id::text
	`, fixture.userID, deviceID, accessHash[:], categoryID, fixture.profileID, contextHash[:]).Scan(&fixture.authSessionID); err != nil {
		t.Fatalf("insert linked logout native session: %v", err)
	}
	tokenHash := sha256.Sum256([]byte("atomic-logout-compat-" + suffix))
	if err := pool.QueryRow(ctx, `
		INSERT INTO jellyfin_compat_sessions (
			auth_session_id, profile_id, token_hash, client_name, device_name,
			client_device_id, client_version, expires_at
		) VALUES ($1, $2, $3, 'Infuse', $4, $5, '8.2', now() + interval '2 hours')
		RETURNING id::text
	`, fixture.authSessionID, fixture.profileID, tokenHash[:], "Atomic logout device "+suffix, "atomic-logout-"+suffix).Scan(&fixture.compatSessionID); err != nil {
		t.Fatalf("insert linked logout compatibility session: %v", err)
	}
	profileID := fixture.profileID
	fixture.session = AuthenticatedSession{
		ID:        fixture.compatSessionID,
		ProfileID: profileID,
		Principal: auth.Principal{SessionID: fixture.authSessionID, UserID: fixture.userID, ActiveProfileID: &profileID},
	}
	return fixture
}

var logoutFailureSequence atomic.Uint64

func installLogoutFailureTrigger(t *testing.T, pool *pgxpool.Pool, table string, fixture atomicLogoutFixture) func() {
	t.Helper()
	if table != "auth_sessions" && table != "jellyfin_compat_sessions" {
		t.Fatalf("unsupported failure target %q", table)
	}
	rowID := fixture.authSessionID
	if table == "jellyfin_compat_sessions" {
		rowID = fixture.compatSessionID
	}
	identifier := fmt.Sprintf("test_logout_failure_%d_%d", time.Now().UnixNano(), logoutFailureSequence.Add(1))
	functionName := identifier + "_fn"
	functionDDL := fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $function$
		BEGIN
			IF NEW.id::text = %s THEN
				RAISE EXCEPTION 'injected linked logout update failure';
			END IF;
			RETURN NEW;
		END
		$function$
	`, functionName, quoteSQLLiteral(rowID))
	if _, err := pool.Exec(context.Background(), functionDDL); err != nil {
		t.Fatalf("install %s logout failure function: %v", table, err)
	}
	triggerDDL := fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE UPDATE ON %s
		FOR EACH ROW EXECUTE FUNCTION %s()
	`, identifier, table, functionName)
	if _, err := pool.Exec(context.Background(), triggerDDL); err != nil {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName))
		t.Fatalf("install %s logout failure trigger: %v", table, err)
	}
	dropped := false
	drop := func() {
		if dropped {
			return
		}
		dropped = true
		if _, err := pool.Exec(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s", identifier, table)); err != nil {
			t.Errorf("drop %s logout failure trigger: %v", table, err)
		}
		if _, err := pool.Exec(context.Background(), fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName)); err != nil {
			t.Errorf("drop %s logout failure function: %v", table, err)
		}
	}
	t.Cleanup(drop)
	return drop
}

func quoteSQLLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func assertAtomicLogoutState(t *testing.T, pool *pgxpool.Pool, fixture atomicLogoutFixture, revoked bool, expectedNative, expectedCompat time.Time) (time.Time, time.Time) {
	t.Helper()
	var nativeRevokedAt, compatRevokedAt *time.Time
	var nativeReason, compatReason *string
	if err := pool.QueryRow(context.Background(), `
		SELECT revoked_at, revoked_reason FROM auth_sessions WHERE id::text = $1
	`, fixture.authSessionID).Scan(&nativeRevokedAt, &nativeReason); err != nil {
		t.Fatalf("read native logout state: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT revoked_at, revoked_reason FROM jellyfin_compat_sessions WHERE id::text = $1
	`, fixture.compatSessionID).Scan(&compatRevokedAt, &compatReason); err != nil {
		t.Fatalf("read compatibility logout state: %v", err)
	}
	if !revoked {
		if nativeRevokedAt != nil || compatRevokedAt != nil || nativeReason != nil || compatReason != nil {
			t.Fatalf("logout state partially committed: native=%v/%v compat=%v/%v", nativeRevokedAt, nativeReason, compatRevokedAt, compatReason)
		}
		return time.Time{}, time.Time{}
	}
	if nativeRevokedAt == nil || compatRevokedAt == nil || nativeReason == nil || compatReason == nil || *nativeReason != "logout" || *compatReason != "logout" {
		t.Fatalf("logout trigger state native=%v/%v compat=%v/%v", nativeRevokedAt, nativeReason, compatRevokedAt, compatReason)
	}
	if !nativeRevokedAt.Equal(*compatRevokedAt) {
		t.Fatalf("native and compatibility revocations are not atomic: %v != %v", *nativeRevokedAt, *compatRevokedAt)
	}
	if !expectedNative.IsZero() && (!nativeRevokedAt.Equal(expectedNative) || !compatRevokedAt.Equal(expectedCompat)) {
		t.Fatalf("idempotent logout changed revocation timestamps: native=%v compat=%v", *nativeRevokedAt, *compatRevokedAt)
	}
	return *nativeRevokedAt, *compatRevokedAt
}

var _ NativeAuthentication = (*authLifecycleNativeFake)(nil)
var _ LinkedProfileSelector = authLifecycleProfileSelector{}
