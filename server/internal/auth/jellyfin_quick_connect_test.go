package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/database"
)

func TestJellyfinQuickConnectApprovalExchangeBoundaries(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run the Jellyfin Quick Connect integration test")
	}
	pool, err := database.Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ctx := context.Background()
	suffix := time.Now().UTC().Format("150405.000000000")
	var categoryID, userID, profileID, foreignProfileID, deviceID, sessionID string

	position := int(time.Now().UnixNano()%1_000_000_000) + 1_000_000_000
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		VALUES ($1, $2, $3) RETURNING id::text
	`, "Quick Connect "+suffix, "quick-connect-"+suffix, position).Scan(&categoryID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-quick-connect-hash', 'admin') RETURNING id::text
	`, "quick_connect_"+suffix).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name, category_id) VALUES ($1, $2::uuid) RETURNING id::text
	`, "Quick profile "+suffix, categoryID).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name, category_id) VALUES ($1, $2::uuid) RETURNING id::text
	`, "Foreign quick profile "+suffix, categoryID).Scan(&foreignProfileID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($1::uuid, $2::uuid, false), ($1::uuid, $3::uuid, false)
	`, userID, profileID, foreignProfileID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1::uuid, 'Quick approver', 'web', $2::uuid, now()) RETURNING id::text
	`, userID, categoryID).Scan(&deviceID); err != nil {
		t.Fatal(err)
	}
	profileContext := make([]byte, 32)
	accessHash := make([]byte, 32)
	if _, err := rand.Read(profileContext); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(accessHash); err != nil {
		t.Fatal(err)
	}
	grantExpiry := time.Now().UTC().Add(time.Hour)
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, authorization_scope, active_profile_id,
			profile_grant_expires_at, profile_context_hash,
			access_token_hash, access_expires_at, refresh_expires_at, last_seen_at
		) VALUES ($1::uuid, $2::uuid, 'global_admin', $3::uuid, $4, $5, $6, $4, $4, now())
		RETURNING id::text
	`, userID, deviceID, profileID, grantExpiry, profileContext, accessHash).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_authorizations WHERE initiating_client_device_id = ANY($1::text[])`, []string{"arvio-device", "concurrent-quick-a", "concurrent-quick-b", "authority-expiry-device", "approval-expiry-device", "exchange-expiry-device", "no-grant-device", "demoted-device", "expired-device"})
		_, _ = pool.Exec(context.Background(), `DELETE FROM devices WHERE user_id = $1::uuid`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id = ANY($1::uuid[])`, []string{profileID, foreignProfileID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM access_categories WHERE id = $1::uuid`, categoryID)
	})

	principal := Principal{
		SessionID: sessionID, UserID: userID, DeviceID: deviceID, Role: "admin",
		AuthorizationScope: AuthorizationScopeGlobalAdministrator,
		ActiveProfileID:    &profileID, ProfileGrantExpiresAt: &grantExpiry,
		ProfileContextHash: profileContext, ActiveProfileCanManage: true,
	}
	service := &Service{pool: pool, accessTTL: time.Minute, refreshTTL: time.Hour, timezone: "UTC"}
	requestContext := WithClientIP(ctx, "192.0.2.144")
	type concurrentQuickAuthorization struct {
		authorization  DeviceAuthorization
		clientDeviceID string
	}
	concurrentQuickConnect := make([]concurrentQuickAuthorization, 0, 2)
	for _, clientDeviceID := range []string{"concurrent-quick-a", "concurrent-quick-b"} {
		startedConcurrent, beginErr := service.BeginJellyfinQuickConnect(requestContext, JellyfinQuickConnectInput{
			ClientDeviceID: clientDeviceID, DeviceName: "Concurrent Android", AppName: "ARVIO", AppVersion: "2.4.0",
		})
		if beginErr != nil {
			t.Fatalf("begin concurrent Quick Connect %q: %v", clientDeviceID, beginErr)
		}
		if approveErr := service.ApproveDeviceAuthorization(ctx, principal, DeviceAuthorizationApproval{UserCode: startedConcurrent.UserCode, CategoryID: categoryID}); approveErr != nil {
			t.Fatalf("approve concurrent Quick Connect %q: %v", clientDeviceID, approveErr)
		}
		concurrentQuickConnect = append(concurrentQuickConnect, concurrentQuickAuthorization{authorization: startedConcurrent, clientDeviceID: clientDeviceID})
	}
	barrierPID, releaseBarrier := holdJellyfinOwnerIssuanceBarrier(t, pool, userID)
	type exchangeOutcome struct {
		result JellyfinQuickConnectResult
		err    error
	}
	concurrentExchanges := make(chan exchangeOutcome, 2)
	for _, authorization := range concurrentQuickConnect {
		authorization := authorization
		go func() {
			result, exchangeErr := service.ExchangeJellyfinQuickConnect(ctx, authorization.authorization.DeviceCode, authorization.clientDeviceID)
			concurrentExchanges <- exchangeOutcome{result: result, err: exchangeErr}
		}()
	}
	waitForJellyfinOwnerIssuanceWaiters(t, pool, barrierPID, 2)
	releaseBarrier()
	concurrentDeviceIDs := make(map[string]struct{}, 2)
	for range 2 {
		outcome := <-concurrentExchanges
		assertNoJellyfinIssuanceDeadlock(t, outcome.err)
		concurrentDeviceIDs[outcome.result.Tokens.DeviceID] = struct{}{}
	}
	if len(concurrentDeviceIDs) != 2 {
		t.Fatalf("concurrent first-time Quick Connect exchanges created %d devices, want 2", len(concurrentDeviceIDs))
	}
	var concurrentMappings, concurrentDevices int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(DISTINCT device_id)
		FROM jellyfin_compat_devices
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
		  AND client_device_id = ANY($3::text[])
	`, userID, profileID, []string{"concurrent-quick-a", "concurrent-quick-b"}).Scan(&concurrentMappings, &concurrentDevices); err != nil {
		t.Fatalf("count concurrent Quick Connect mappings: %v", err)
	}
	if concurrentMappings != 2 || concurrentDevices != 2 {
		t.Fatalf("concurrent Quick Connect mappings=%d devices=%d, want 2 and 2", concurrentMappings, concurrentDevices)
	}
	var ownerDeviceCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE user_id = $1::uuid`, userID).Scan(&ownerDeviceCount); err != nil {
		t.Fatalf("count concurrent Quick Connect owner devices: %v", err)
	}
	if ownerDeviceCount != 3 || ownerDeviceCount > maximumDevicesPerUser {
		t.Fatalf("concurrent Quick Connect owner device count=%d, want 3 within quota %d", ownerDeviceCount, maximumDevicesPerUser)
	}
	nativeAuthorization, err := service.BeginDeviceAuthorization(requestContext, "Native device", "test")
	if err != nil {
		t.Fatalf("begin native-purpose authorization: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_authorizations WHERE device_code_hash = $1`, tokenDigest(nativeAuthorization.DeviceCode))
	})
	started, err := service.BeginJellyfinQuickConnect(requestContext, JellyfinQuickConnectInput{
		ClientDeviceID: "arvio-device", DeviceName: "Android", AppName: "ARVIO", AppVersion: "2.4.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	baseline := countUserSessions(t, pool, userID)
	t.Run("approval rejects approver expiring while row lock waits", func(t *testing.T) {
		expiring, err := service.BeginJellyfinQuickConnect(requestContext, JellyfinQuickConnectInput{
			ClientDeviceID: "authority-expiry-device", DeviceName: "Android", AppName: "ARVIO", AppVersion: "2.4.0",
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM device_authorizations WHERE device_code_hash = $1`, tokenDigest(expiring.DeviceCode))
		})
		gateTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin approver expiry gate: %v", err)
		}
		gateFinished := false
		defer func() {
			if !gateFinished {
				_ = gateTx.Rollback(ctx)
			}
		}()
		if _, err := gateTx.Exec(ctx, `
			SELECT id FROM device_authorizations WHERE device_code_hash = $1 FOR UPDATE
		`, tokenDigest(expiring.DeviceCode)); err != nil {
			t.Fatalf("lock approver expiry authorization: %v", err)
		}
		var blockerPID int
		if err := gateTx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
			t.Fatalf("read approver expiry blocker PID: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			UPDATE auth_sessions
			SET access_expires_at = clock_timestamp() + interval '2 seconds'
			WHERE id = $1::uuid
		`, sessionID); err != nil {
			t.Fatalf("stage approver access-token expiry: %v", err)
		}
		approvalResult := make(chan error, 1)
		go func() {
			approvalResult <- service.ApproveDeviceAuthorization(ctx, principal, DeviceAuthorizationApproval{
				UserCode: expiring.UserCode, CategoryID: categoryID,
			})
		}()
		waitForQuickConnectLock(t, pool, blockerPID)
		if _, err := gateTx.Exec(ctx, `SELECT pg_sleep(2.1)`); err != nil {
			t.Fatalf("wait past approver access-token expiry: %v", err)
		}
		if err := gateTx.Commit(ctx); err != nil {
			t.Fatalf("release approver expiry gate: %v", err)
		}
		gateFinished = true
		if err := <-approvalResult; !errors.Is(err, ErrForbidden) {
			t.Fatalf("approval after blocked approver expiry error = %v, want %v", err, ErrForbidden)
		}
		var approvedUserID *string
		if err := pool.QueryRow(ctx, `
			SELECT approved_user_id::text FROM device_authorizations WHERE device_code_hash = $1
		`, tokenDigest(expiring.DeviceCode)).Scan(&approvedUserID); err != nil {
			t.Fatalf("read rejected expired approver authorization: %v", err)
		}
		if approvedUserID != nil {
			t.Fatalf("expired approver authorized user %q", *approvedUserID)
		}
		if _, err := pool.Exec(ctx, `
			UPDATE auth_sessions
			SET access_expires_at = clock_timestamp() + interval '1 hour',
			    refresh_expires_at = clock_timestamp() + interval '2 hours'
			WHERE id = $1::uuid
		`, sessionID); err != nil {
			t.Fatalf("restore approver access-token expiry: %v", err)
		}
	})

	t.Run("approval rejects secret expiring while row lock waits", func(t *testing.T) {
		expiring, err := service.BeginJellyfinQuickConnect(requestContext, JellyfinQuickConnectInput{
			ClientDeviceID: "approval-expiry-device", DeviceName: "Android", AppName: "ARVIO", AppVersion: "2.4.0",
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM device_authorizations WHERE device_code_hash = $1`, tokenDigest(expiring.DeviceCode))
		})
		gateTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin approval expiry gate: %v", err)
		}
		gateFinished := false
		defer func() {
			if !gateFinished {
				_ = gateTx.Rollback(ctx)
			}
		}()
		if _, err := gateTx.Exec(ctx, `
			SELECT id FROM device_authorizations WHERE device_code_hash = $1 FOR UPDATE
		`, tokenDigest(expiring.DeviceCode)); err != nil {
			t.Fatalf("lock approval authorization: %v", err)
		}
		var blockerPID int
		if err := gateTx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
			t.Fatalf("read approval expiry blocker PID: %v", err)
		}
		approvalResult := make(chan error, 1)
		go func() {
			approvalResult <- service.ApproveDeviceAuthorization(ctx, principal, DeviceAuthorizationApproval{
				UserCode: expiring.UserCode, CategoryID: categoryID,
			})
		}()
		waitForQuickConnectLock(t, pool, blockerPID)
		if _, err := gateTx.Exec(ctx, `
			UPDATE device_authorizations
			SET expires_at = clock_timestamp() + interval '250 milliseconds'
			WHERE device_code_hash = $1
		`, tokenDigest(expiring.DeviceCode)); err != nil {
			t.Fatalf("stage blocked approval secret expiry: %v", err)
		}
		if _, err := gateTx.Exec(ctx, `SELECT pg_sleep(0.35)`); err != nil {
			t.Fatalf("wait past approval secret expiry: %v", err)
		}
		if err := gateTx.Commit(ctx); err != nil {
			t.Fatalf("release approval expiry gate: %v", err)
		}
		gateFinished = true
		if err := <-approvalResult; !errors.Is(err, ErrInvalidUserCode) {
			t.Fatalf("approval after blocked secret expiry error = %v, want %v", err, ErrInvalidUserCode)
		}
		var approvedUserID *string
		if err := pool.QueryRow(ctx, `
			SELECT approved_user_id::text FROM device_authorizations WHERE device_code_hash = $1
		`, tokenDigest(expiring.DeviceCode)).Scan(&approvedUserID); err != nil {
			t.Fatalf("read rejected expired approval: %v", err)
		}
		if approvedUserID != nil {
			t.Fatalf("expired authorization was approved for user %q", *approvedUserID)
		}
	})

	t.Run("exchange rejects secret expiring while advisory lock waits", func(t *testing.T) {
		expiring, err := service.BeginJellyfinQuickConnect(requestContext, JellyfinQuickConnectInput{
			ClientDeviceID: "exchange-expiry-device", DeviceName: "Android", AppName: "ARVIO", AppVersion: "2.4.0",
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM device_authorizations WHERE device_code_hash = $1`, tokenDigest(expiring.DeviceCode))
		})
		if err := service.ApproveDeviceAuthorization(ctx, principal, DeviceAuthorizationApproval{UserCode: expiring.UserCode, CategoryID: categoryID}); err != nil {
			t.Fatalf("approve exchange expiry fixture: %v", err)
		}
		gateTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin exchange expiry gate: %v", err)
		}
		gateFinished := false
		defer func() {
			if !gateFinished {
				_ = gateTx.Rollback(ctx)
			}
		}()
		if _, err := gateTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, profileID); err != nil {
			t.Fatalf("lock exchange profile session: %v", err)
		}
		var blockerPID int
		if err := gateTx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
			t.Fatalf("read exchange expiry blocker PID: %v", err)
		}
		exchangeResult := make(chan error, 1)
		go func() {
			_, exchangeErr := service.ExchangeJellyfinQuickConnect(ctx, expiring.DeviceCode, "exchange-expiry-device")
			exchangeResult <- exchangeErr
		}()
		waitForQuickConnectLock(t, pool, blockerPID)
		if _, err := pool.Exec(ctx, `
			UPDATE device_authorizations
			SET expires_at = clock_timestamp() + interval '250 milliseconds'
			WHERE device_code_hash = $1
		`, tokenDigest(expiring.DeviceCode)); err != nil {
			t.Fatalf("stage blocked exchange secret expiry: %v", err)
		}
		if _, err := gateTx.Exec(ctx, `SELECT pg_sleep(0.35)`); err != nil {
			t.Fatalf("wait past exchange secret expiry: %v", err)
		}
		if err := gateTx.Commit(ctx); err != nil {
			t.Fatalf("release exchange expiry gate: %v", err)
		}
		gateFinished = true
		if err := <-exchangeResult; !errors.Is(err, ErrDeviceAuthorizationExpired) {
			t.Fatalf("exchange after blocked secret expiry error = %v, want %v", err, ErrDeviceAuthorizationExpired)
		}
		var consumedAt *time.Time
		if err := pool.QueryRow(ctx, `
			SELECT consumed_at FROM device_authorizations WHERE device_code_hash = $1
		`, tokenDigest(expiring.DeviceCode)).Scan(&consumedAt); err != nil {
			t.Fatalf("read rejected expired exchange: %v", err)
		}
		if consumedAt != nil {
			t.Fatalf("expired exchange consumed authorization at %v", *consumedAt)
		}
		if count := countUserSessions(t, pool, userID); count != baseline {
			t.Fatalf("expired exchange created session count %d, want %d", count, baseline)
		}
	})
	pending, err := service.PollJellyfinQuickConnect(ctx, started.DeviceCode, "arvio-device")
	if err != nil || pending.Authenticated || pending.AppVersion != "2.4.0" {
		t.Fatalf("pending poll = %+v error=%v", pending, err)
	}
	if _, err := service.PollJellyfinQuickConnect(ctx, started.DeviceCode, "other-device"); !errors.Is(err, ErrInvalidDeviceCode) {
		t.Fatalf("wrong-client poll error = %v", err)
	}
	foreign := principal
	foreign.ActiveProfileID = &foreignProfileID
	if err := service.ApproveDeviceAuthorization(ctx, foreign, DeviceAuthorizationApproval{UserCode: started.UserCode, CategoryID: categoryID}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-profile approval error = %v", err)
	}
	if count := countUserSessions(t, pool, userID); count != baseline {
		t.Fatalf("session created before approval: got %d want %d", count, baseline)
	}
	if err := service.ApproveDeviceAuthorization(ctx, principal, DeviceAuthorizationApproval{UserCode: started.UserCode, CategoryID: categoryID}); err != nil {
		t.Fatal(err)
	}
	approved, err := service.PollJellyfinQuickConnect(ctx, started.DeviceCode, "arvio-device")
	if err != nil || !approved.Authenticated {
		t.Fatalf("approved poll = %+v error=%v", approved, err)
	}
	if count := countUserSessions(t, pool, userID); count != baseline {
		t.Fatalf("session created by approval: got %d want %d", count, baseline)
	}
	if _, err := service.ExchangeJellyfinQuickConnect(ctx, started.DeviceCode, "other-device"); !errors.Is(err, ErrInvalidDeviceCode) {
		t.Fatalf("wrong-client exchange error = %v", err)
	}
	result, err := service.ExchangeJellyfinQuickConnect(ctx, started.DeviceCode, "arvio-device")
	if err != nil {
		t.Fatal(err)
	}
	if result.ProfileID != profileID || result.Tokens.AuthorizationScope != AuthorizationScopeCategory ||
		result.DeviceID != "arvio-device" || result.DeviceName != "Android" ||
		result.AppName != "ARVIO" || result.AppVersion != "2.4.0" {
		t.Fatalf("exchange result = %+v", result)
	}
	var activeProfileID, sessionCategoryID string
	var credentialID *string
	if err := pool.QueryRow(ctx, `
		SELECT active_profile_id::text, category_id::text, jellyfin_credential_id::text
		FROM auth_sessions WHERE id = $1::uuid
	`, result.Tokens.SessionID).Scan(&activeProfileID, &sessionCategoryID, &credentialID); err != nil {
		t.Fatal(err)
	}
	if activeProfileID != profileID || sessionCategoryID != categoryID || credentialID != nil {
		t.Fatalf("native session profile=%q category=%q credential=%v", activeProfileID, sessionCategoryID, credentialID)
	}
	var persistentCredentials int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM profile_jellyfin_credentials WHERE profile_id = $1::uuid`, profileID).Scan(&persistentCredentials); err != nil {
		t.Fatal(err)
	}
	if persistentCredentials != 0 {
		t.Fatalf("Quick Connect created %d persistent profile credentials", persistentCredentials)
	}
	if _, err := service.ExchangeJellyfinQuickConnect(ctx, started.DeviceCode, "arvio-device"); !errors.Is(err, ErrInvalidDeviceCode) {
		t.Fatalf("replay error = %v", err)
	}
	if _, err := service.PollJellyfinQuickConnect(ctx, started.DeviceCode, "arvio-device"); !errors.Is(err, ErrInvalidDeviceCode) {
		t.Fatalf("consumed poll error = %v", err)
	}

	noGrant, err := service.BeginJellyfinQuickConnect(requestContext, JellyfinQuickConnectInput{
		ClientDeviceID: "no-grant-device", DeviceName: "Android", AppName: "ARVIO", AppVersion: "unknown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM user_profile_access WHERE user_id = $1::uuid AND profile_id = $2::uuid`, userID, profileID); err != nil {
		t.Fatal(err)
	}
	if err := service.ApproveDeviceAuthorization(ctx, principal, DeviceAuthorizationApproval{UserCode: noGrant.UserCode, CategoryID: categoryID}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing durable profile grant approval error = %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_profile_access (user_id, profile_id, can_manage) VALUES ($1::uuid, $2::uuid, false)`, userID, profileID); err != nil {
		t.Fatal(err)
	}

	demoted, err := service.BeginJellyfinQuickConnect(requestContext, JellyfinQuickConnectInput{
		ClientDeviceID: "demoted-device", DeviceName: "Android", AppName: "ARVIO", AppVersion: "unknown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ApproveDeviceAuthorization(ctx, principal, DeviceAuthorizationApproval{UserCode: demoted.UserCode, CategoryID: categoryID}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'member' WHERE id = $1::uuid`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExchangeJellyfinQuickConnect(ctx, demoted.DeviceCode, "demoted-device"); !errors.Is(err, ErrInvalidDeviceCode) {
		t.Fatalf("demoted non-manager exchange error = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = $1::uuid`, userID); err != nil {
		t.Fatal(err)
	}

	expired, err := service.BeginJellyfinQuickConnect(requestContext, JellyfinQuickConnectInput{
		ClientDeviceID: "expired-device", DeviceName: "Android", AppName: "ARVIO", AppVersion: "unknown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE device_authorizations SET expires_at = now() - interval '1 second' WHERE device_code_hash = $1`, tokenDigest(expired.DeviceCode)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PollJellyfinQuickConnect(ctx, expired.DeviceCode, "expired-device"); !errors.Is(err, ErrDeviceAuthorizationExpired) {
		t.Fatalf("expired poll error = %v", err)
	}
	if err := service.ApproveDeviceAuthorization(ctx, principal, DeviceAuthorizationApproval{UserCode: expired.UserCode, CategoryID: categoryID}); !errors.Is(err, ErrInvalidUserCode) {
		t.Fatalf("expired approval error = %v", err)
	}
}

func countUserSessions(t *testing.T, pool *pgxpool.Pool, userID string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM auth_sessions WHERE user_id = $1::uuid`, userID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func waitForQuickConnectLock(t *testing.T, pool *pgxpool.Pool, blockerPID int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*)
			FROM pg_stat_activity
			WHERE datname = current_database()
			  AND pid <> pg_backend_pid()
			  AND $1 = ANY(pg_blocking_pids(pid))
		`, blockerPID).Scan(&waiting); err != nil {
			t.Fatalf("observe Quick Connect lock blocker: %v", err)
		}
		if waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no Quick Connect query waited on backend %d", blockerPID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
