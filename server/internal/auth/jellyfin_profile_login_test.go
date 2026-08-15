package auth

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/database"
)

func TestJellyfinAppPasswordFormatAndDigest(t *testing.T) {
	plainText, generatedDigest, err := NewJellyfinAppPassword()
	if err != nil {
		t.Fatalf("generate Jellyfin application password: %v", err)
	}
	if !strings.HasPrefix(plainText, jellyfinAppPasswordPrefix) {
		t.Fatalf("application password %q lacks prefix %q", plainText, jellyfinAppPasswordPrefix)
	}
	parsedDigest, valid := JellyfinAppPasswordDigest(plainText)
	if !valid {
		t.Fatal("generated application password was rejected")
	}
	if !bytes.Equal(parsedDigest, generatedDigest) {
		t.Fatal("generated and parsed application password digests differ")
	}
	for _, malformed := range []string{
		"", "native-password", jellyfinAppPasswordPrefix,
		plainText + "=", strings.ToUpper(plainText), plainText[:len(plainText)-1],
	} {
		if _, valid := JellyfinAppPasswordDigest(malformed); valid {
			t.Fatalf("malformed application password %q was accepted", malformed)
		}
	}
}

func TestParseJellyfinCredentialUsernameRequiresCanonicalUUID(t *testing.T) {
	const username = "c19b118c-2099-4a61-9a5d-f6309b38e521"
	parsed, valid := parseJellyfinCredentialUsername(strings.ToUpper(username))
	if !valid || parsed != username {
		t.Fatalf("parsed UUID = %q, valid=%v", parsed, valid)
	}
	for _, malformed := range []string{
		"", "c19b118c20994a619a5df6309b38e521", "{" + username + "}", " " + username,
		"c19b118c-2099-4a61-9a5d-f6309b38e52z",
	} {
		if _, valid := parseJellyfinCredentialUsername(malformed); valid {
			t.Fatalf("malformed credential username %q was accepted", malformed)
		}
	}
}

func TestLoginJellyfinProfileCreatesBoundCategorySessionWithoutNativePasswordOrPIN(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run Jellyfin profile login tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open Jellyfin profile login database: %v", err)
	}
	t.Cleanup(pool.Close)

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	var categoryID, userID, profileID, credentialID string
	if err := pool.QueryRow(ctx, `
		SELECT id::text FROM access_categories WHERE is_default
	`).Scan(&categoryID); err != nil {
		t.Fatalf("read default access category: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'deliberately-not-a-native-password-hash', 'admin')
		RETURNING id::text
	`, "jellyfin_profile_login_"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert Jellyfin credential owner: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext := context.Background()
		_, _ = pool.Exec(cleanupContext, "DELETE FROM users WHERE id = $1::uuid", userID)
		if profileID != "" {
			_, _ = pool.Exec(cleanupContext, "DELETE FROM profiles WHERE id = $1::uuid", profileID)
		}
	})
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name, category_id, pin_hash)
		VALUES ($1, $2::uuid, 'deliberately-not-a-profile-pin-hash')
		RETURNING id::text
	`, "Jellyfin profile "+suffix, categoryID).Scan(&profileID); err != nil {
		t.Fatalf("insert Jellyfin profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($1::uuid, $2::uuid, true)
	`, userID, profileID); err != nil {
		t.Fatalf("grant Jellyfin profile access: %v", err)
	}
	applicationPassword, passwordHash, err := NewJellyfinAppPassword()
	if err != nil {
		t.Fatalf("generate fixture application password: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO profile_jellyfin_credentials (profile_id, owner_user_id, password_hash)
		VALUES ($1::uuid, $2::uuid, $3)
		RETURNING id::text
	`, profileID, userID, passwordHash).Scan(&credentialID); err != nil {
		t.Fatalf("insert Jellyfin profile credential: %v", err)
	}

	service := &Service{pool: pool, accessTTL: 5 * time.Minute, refreshTTL: 2 * time.Hour, timezone: "UTC"}
	loginInput := JellyfinProfileLoginInput{
		Username: credentialID, Password: applicationPassword,
		LinkedDeviceKey: "jellyfin-client-" + suffix,
		DeviceName:      "Jellyfin profile client", Platform: "Generic Client",
	}
	wrongPassword, _, err := NewJellyfinAppPassword()
	if err != nil {
		t.Fatalf("generate wrong application password: %v", err)
	}
	wrongInput := loginInput
	wrongInput.Password = wrongPassword
	if _, err := service.LoginJellyfinProfile(ctx, wrongInput); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong application password error = %v, want %v", err, ErrInvalidCredentials)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM user_profile_access WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, profileID); err != nil {
		t.Fatalf("remove Jellyfin owner profile grant: %v", err)
	}
	if _, err := service.LoginJellyfinProfile(ctx, loginInput); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("missing owner grant error = %v, want %v", err, ErrInvalidCredentials)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($1::uuid, $2::uuid, true)
	`, userID, profileID); err != nil {
		t.Fatalf("restore Jellyfin owner profile grant: %v", err)
	}

	if _, err := pool.Exec(ctx, "UPDATE profiles SET enabled = false WHERE id = $1::uuid", profileID); err != nil {
		t.Fatalf("disable Jellyfin profile: %v", err)
	}
	if _, err := service.LoginJellyfinProfile(ctx, loginInput); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("inaccessible profile error = %v, want %v", err, ErrInvalidCredentials)
	}
	if _, err := pool.Exec(ctx, "UPDATE profiles SET enabled = true WHERE id = $1::uuid", profileID); err != nil {
		t.Fatalf("re-enable Jellyfin profile: %v", err)
	}

	barrierPID, releaseBarrier := holdJellyfinOwnerIssuanceBarrier(t, pool, userID)
	type loginOutcome struct {
		result JellyfinProfileLoginResult
		err    error
	}
	concurrentLogins := make(chan loginOutcome, 2)
	for _, linkedDeviceKey := range []string{"jellyfin-concurrent-a-" + suffix, "jellyfin-concurrent-b-" + suffix} {
		concurrentInput := loginInput
		concurrentInput.LinkedDeviceKey = linkedDeviceKey
		go func() {
			result, loginErr := service.LoginJellyfinProfile(ctx, concurrentInput)
			concurrentLogins <- loginOutcome{result: result, err: loginErr}
		}()
	}
	waitForJellyfinOwnerIssuanceWaiters(t, pool, barrierPID, 2)
	releaseBarrier()
	concurrentDeviceIDs := make(map[string]struct{}, 2)
	for range 2 {
		outcome := <-concurrentLogins
		assertNoJellyfinIssuanceDeadlock(t, outcome.err)
		concurrentDeviceIDs[outcome.result.Tokens.DeviceID] = struct{}{}
	}
	if len(concurrentDeviceIDs) != 2 {
		t.Fatalf("concurrent first-time Jellyfin logins created %d devices, want 2", len(concurrentDeviceIDs))
	}
	var concurrentMappings, concurrentDevices int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(DISTINCT device_id)
		FROM jellyfin_compat_devices
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
		  AND client_device_id = ANY($3::text[])
	`, userID, profileID, []string{"jellyfin-concurrent-a-" + suffix, "jellyfin-concurrent-b-" + suffix}).Scan(&concurrentMappings, &concurrentDevices); err != nil {
		t.Fatalf("count concurrent Jellyfin device mappings: %v", err)
	}
	if concurrentMappings != 2 || concurrentDevices != 2 {
		t.Fatalf("concurrent Jellyfin mappings=%d devices=%d, want 2 and 2", concurrentMappings, concurrentDevices)
	}
	var ownerDeviceCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE user_id = $1::uuid`, userID).Scan(&ownerDeviceCount); err != nil {
		t.Fatalf("count concurrent Jellyfin owner devices: %v", err)
	}
	if ownerDeviceCount != 2 || ownerDeviceCount > maximumDevicesPerUser {
		t.Fatalf("concurrent Jellyfin owner device count=%d, want 2 within quota %d", ownerDeviceCount, maximumDevicesPerUser)
	}

	result, err := service.LoginJellyfinProfile(ctx, loginInput)
	if err != nil {
		t.Fatalf("login with profile application password: %v", err)
	}
	if result.ProfileID != profileID || result.ProfileName != "Jellyfin profile "+suffix {
		t.Fatalf("profile login result = %+v", result)
	}
	if result.Tokens.AuthorizationScope != AuthorizationScopeCategory || result.Tokens.Category == nil || result.Tokens.Category.ID != categoryID {
		t.Fatalf("profile login tokens = %+v", result.Tokens)
	}

	var sessionScope AuthorizationScope
	var sessionCategoryID, activeProfileID, sessionCredentialID string
	var sessionGeneration int64
	var grantExpiresAt, refreshExpiresAt time.Time
	var contextHash []byte
	if err := pool.QueryRow(ctx, `
		SELECT authorization_scope, category_id::text, active_profile_id::text,
		       profile_grant_expires_at, refresh_expires_at, profile_context_hash,
		       jellyfin_credential_id::text, jellyfin_credential_generation
		FROM auth_sessions
		WHERE id = $1::uuid
	`, result.Tokens.SessionID).Scan(
		&sessionScope, &sessionCategoryID, &activeProfileID,
		&grantExpiresAt, &refreshExpiresAt, &contextHash,
		&sessionCredentialID, &sessionGeneration,
	); err != nil {
		t.Fatalf("read linked Jellyfin native session: %v", err)
	}
	if sessionScope != AuthorizationScopeCategory || sessionCategoryID != categoryID || activeProfileID != profileID {
		t.Fatalf("linked session scope=%q category=%q profile=%q", sessionScope, sessionCategoryID, activeProfileID)
	}
	if !grantExpiresAt.Equal(refreshExpiresAt) || len(contextHash) != 32 {
		t.Fatalf("linked session grant expiry=%v refresh expiry=%v context bytes=%d", grantExpiresAt, refreshExpiresAt, len(contextHash))
	}
	if sessionCredentialID != credentialID || sessionGeneration != 1 {
		t.Fatalf("linked session credential=%q generation=%d", sessionCredentialID, sessionGeneration)
	}
	var lastUsedAt *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT last_used_at FROM profile_jellyfin_credentials WHERE id = $1::uuid
	`, credentialID).Scan(&lastUsedAt); err != nil {
		t.Fatalf("read Jellyfin credential last use: %v", err)
	}
	if lastUsedAt == nil {
		t.Fatal("successful Jellyfin login did not touch credential last_used_at")
	}

	var deviceCategoryID, mappedProfileID string
	if err := pool.QueryRow(ctx, `
		SELECT device.category_id::text, mapping.profile_id::text
		FROM devices device
		JOIN jellyfin_compat_devices mapping ON mapping.device_id = device.id
		WHERE device.id = $1::uuid
	`, result.Tokens.DeviceID).Scan(&deviceCategoryID, &mappedProfileID); err != nil {
		t.Fatalf("read profile-scoped Jellyfin device mapping: %v", err)
	}
	if deviceCategoryID != categoryID || mappedProfileID != profileID {
		t.Fatalf("device category=%q mapping profile=%q", deviceCategoryID, mappedProfileID)
	}
	reused, err := service.LoginJellyfinProfile(ctx, loginInput)
	if err != nil {
		t.Fatalf("reuse profile-scoped Jellyfin device mapping: %v", err)
	}
	if reused.Tokens.DeviceID != result.Tokens.DeviceID {
		t.Fatalf("reused mapping device=%q, want %q", reused.Tokens.DeviceID, result.Tokens.DeviceID)
	}

	principal, err := service.Authenticate(ctx, result.Tokens.AccessToken)
	if err != nil {
		t.Fatalf("authenticate Jellyfin native session: %v", err)
	}
	if principal.Role != "admin" || principal.IsGlobalAdministrator() ||
		principal.AuthorizationScope != AuthorizationScopeCategory ||
		principal.ActiveProfileID == nil || *principal.ActiveProfileID != profileID {
		t.Fatalf("Jellyfin principal was not category-bound: %+v", principal)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		SELECT $1::uuid, 'Jellyfin quota ' || value::text || $3, 'test', $2::uuid, now()
		FROM generate_series(1, 47) value
	`, userID, categoryID, suffix); err != nil {
		t.Fatalf("fill Jellyfin credential owner device quota: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE user_id = $1::uuid`, userID).Scan(&ownerDeviceCount); err != nil {
		t.Fatalf("count quota-filled Jellyfin owner devices: %v", err)
	}
	if ownerDeviceCount != maximumDevicesPerUser {
		t.Fatalf("quota-filled Jellyfin owner device count=%d, want %d", ownerDeviceCount, maximumDevicesPerUser)
	}
	quotaInput := loginInput
	quotaInput.LinkedDeviceKey = "jellyfin-quota-" + suffix
	if _, err := service.LoginJellyfinProfile(ctx, quotaInput); !errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrDeviceQuotaReached) {
		t.Fatalf("device quota login error = %v, want opaque %v", err, ErrInvalidCredentials)
	}
	var quotaMappingExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM jellyfin_compat_devices
			WHERE user_id = $1::uuid AND profile_id = $2::uuid AND client_device_id = $3
		)
	`, userID, profileID, quotaInput.LinkedDeviceKey).Scan(&quotaMappingExists); err != nil {
		t.Fatalf("read quota-rejected mapping: %v", err)
	}
	if quotaMappingExists {
		t.Fatal("quota-rejected Jellyfin login persisted a device mapping")
	}
}

func holdJellyfinOwnerIssuanceBarrier(t *testing.T, pool *pgxpool.Pool, userID string) (int, func()) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin Jellyfin owner issuance barrier: %v", err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			_ = tx.Rollback(context.Background())
		}
	})
	var blockerPID int
	var lockedUserID string
	if err := tx.QueryRow(ctx, `
		SELECT pg_backend_pid(), id::text
		FROM users
		WHERE id = $1::uuid
		FOR UPDATE
	`, userID).Scan(&blockerPID, &lockedUserID); err != nil {
		t.Fatalf("lock Jellyfin issuance owner barrier: %v", err)
	}
	return blockerPID, func() {
		t.Helper()
		if released {
			return
		}
		if err := tx.Commit(context.Background()); err != nil {
			t.Fatalf("release Jellyfin owner issuance barrier: %v", err)
		}
		released = true
	}
}

func waitForJellyfinOwnerIssuanceWaiters(t *testing.T, pool *pgxpool.Pool, blockerPID, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(context.Background(), `
			WITH RECURSIVE blocked(pid) AS (
				SELECT pid
				FROM pg_stat_activity
				WHERE datname = current_database()
				  AND $1 = ANY(pg_blocking_pids(pid))
				UNION
				SELECT activity.pid
				FROM pg_stat_activity activity
				JOIN blocked predecessor ON predecessor.pid = ANY(pg_blocking_pids(activity.pid))
				WHERE activity.datname = current_database()
			)
			SELECT count(*) FROM blocked
		`, blockerPID).Scan(&waiting); err != nil {
			t.Fatalf("observe Jellyfin owner issuance barrier: %v", err)
		}
		if waiting >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Jellyfin owner issuance barrier has %d waiters, want %d", waiting, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertNoJellyfinIssuanceDeadlock(t *testing.T, err error) {
	t.Helper()
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "40P01" {
		t.Fatalf("concurrent Jellyfin issuance failed with SQLSTATE 40P01: %v", err)
	}
	if err != nil {
		t.Fatalf("concurrent Jellyfin issuance failed: %v", err)
	}
}
