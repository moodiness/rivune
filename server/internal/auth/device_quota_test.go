package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/password"
)

type deviceQuotaFixture struct {
	userID    string
	username  string
	password  string
	deviceIDs []string
}

func TestDeviceQuotaExactLimitOwnershipAndDeviceCodeExchange(t *testing.T) {
	pool := openDeviceQuotaTestPool(t, "rivune-auth-device-quota-exact")
	fixture := seedDeviceQuotaFixture(t, pool, maximumDevicesPerUser-1)
	foreign := seedDeviceQuotaFixture(t, pool, 1)
	cleanupDeviceQuotaFixtures(t, pool, fixture, foreign)

	service := &Service{pool: pool, accessTTL: time.Minute, refreshTTL: time.Hour, timezone: "UTC"}
	created, err := service.Login(context.Background(), LoginInput{
		Username: fixture.username, Password: fixture.password, DeviceName: "Exact quota device", Platform: "test",
	})
	if err != nil {
		t.Fatalf("create device at exact limit: %v", err)
	}
	fixture.deviceIDs = append(fixture.deviceIDs, created.DeviceID)

	if _, err := service.Login(context.Background(), LoginInput{
		Username: fixture.username, Password: fixture.password, DeviceName: "Over quota device", Platform: "test",
	}); !errors.Is(err, ErrDeviceQuotaReached) {
		t.Fatalf("create device N+1 error = %v, want %v", err, ErrDeviceQuotaReached)
	}
	assertDeviceCount(t, pool, fixture.userID, maximumDevicesPerUser)

	if _, err := service.Login(context.Background(), LoginInput{
		Username: fixture.username, Password: fixture.password, DeviceID: created.DeviceID,
		DeviceName: "Renamed owned device", Platform: "test",
	}); err != nil {
		t.Fatalf("reuse owned device at quota: %v", err)
	}
	if _, err := service.Login(context.Background(), LoginInput{
		Username: fixture.username, Password: fixture.password, DeviceID: foreign.deviceIDs[0],
		DeviceName: "Foreign device", Platform: "test",
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("reuse foreign device error = %v, want %v", err, ErrInvalidInput)
	}

	deviceCode := insertApprovedQuotaDeviceAuthorization(t, pool, fixture.userID)
	if _, err := service.ExchangeDeviceAuthorization(context.Background(), deviceCode); !errors.Is(err, ErrDeviceQuotaReached) {
		t.Fatalf("exchange at quota error = %v, want %v", err, ErrDeviceQuotaReached)
	}
	var consumedAt *time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT consumed_at FROM device_authorizations WHERE device_code_hash = $1
	`, tokenDigest(deviceCode)).Scan(&consumedAt); err != nil {
		t.Fatalf("read rejected device authorization: %v", err)
	}
	if consumedAt != nil {
		t.Fatal("quota-rejected device authorization was consumed")
	}
	assertDeviceCount(t, pool, fixture.userID, maximumDevicesPerUser)
}

func TestDeviceQuotaSerializesLoginAndDeviceCodeAcrossServices(t *testing.T) {
	firstPool := openDeviceQuotaTestPool(t, "rivune-auth-device-quota-first")
	secondPool := openDeviceQuotaTestPool(t, "rivune-auth-device-quota-second")
	fixture := seedDeviceQuotaFixture(t, firstPool, maximumDevicesPerUser-1)
	cleanupDeviceQuotaFixtures(t, firstPool, fixture)
	deviceCode := insertApprovedQuotaDeviceAuthorization(t, firstPool, fixture.userID)

	firstService := &Service{pool: firstPool, accessTTL: time.Minute, refreshTTL: time.Hour, timezone: "UTC"}
	secondService := &Service{pool: secondPool, accessTTL: time.Minute, refreshTTL: time.Hour, timezone: "UTC"}
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := firstService.Login(context.Background(), LoginInput{
			Username: fixture.username, Password: fixture.password,
			DeviceName: "Concurrent password device", Platform: "test",
		})
		results <- err
	}()
	go func() {
		<-start
		_, err := secondService.ExchangeDeviceAuthorization(context.Background(), deviceCode)
		results <- err
	}()
	close(start)

	var successes, quotaRejections int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrDeviceQuotaReached):
			quotaRejections++
		default:
			t.Fatalf("concurrent device creation error = %v", err)
		}
	}
	if successes != 1 || quotaRejections != 1 {
		t.Fatalf("concurrent results: successes=%d quota rejections=%d, want 1 and 1", successes, quotaRejections)
	}
	assertDeviceCount(t, firstPool, fixture.userID, maximumDevicesPerUser)
}

func TestOrphanDeviceCleanupPreservesOwnersAndDependencies(t *testing.T) {
	pool := openDeviceQuotaTestPool(t, "rivune-auth-device-orphan-cleanup")
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin orphan cleanup fixture: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var sessionUserID, ownerUserID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-test-hash', 'member') RETURNING id::text
	`, "orphan_session_"+suffix).Scan(&sessionUserID); err != nil {
		t.Fatalf("insert dependency user: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-test-hash', 'member') RETURNING id::text
	`, "orphan_owner_"+suffix).Scan(&ownerUserID); err != nil {
		t.Fatalf("insert owner user: %v", err)
	}

	insertDevice := func(userID, name string) string {
		t.Helper()
		var deviceID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO devices (user_id, name, platform, last_seen_at)
			VALUES ($1::uuid, $2, 'test', now()) RETURNING id::text
		`, userID, name).Scan(&deviceID); err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
		return deviceID
	}
	orphanID := insertDevice(sessionUserID, "Orphan "+suffix)
	dependentID := insertDevice(sessionUserID, "Dependent "+suffix)
	ownedID := insertDevice(ownerUserID, "Owned "+suffix)

	accessHash := sha256.Sum256([]byte("dependent-" + suffix))
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at
		) VALUES ($1::uuid, $2::uuid, $3, now() + interval '1 hour', now() + interval '2 hours')
	`, sessionUserID, dependentID, accessHash[:]); err != nil {
		t.Fatalf("insert dependent session: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE devices
		SET user_id = NULL, created_at = TIMESTAMPTZ '1900-01-01 00:00:00+00'
		WHERE id = ANY($1::uuid[])
	`, []string{orphanID, dependentID}); err != nil {
		t.Fatalf("remove test device owners: %v", err)
	}
	if _, err := tx.Exec(ctx, cleanupOrphanDevicesSQL, 10); err != nil {
		t.Fatalf("clean orphan devices: %v", err)
	}

	assertDeviceExists := func(deviceID string, want bool) {
		t.Helper()
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM devices WHERE id = $1::uuid)", deviceID).Scan(&exists); err != nil {
			t.Fatalf("query device %s: %v", deviceID, err)
		}
		if exists != want {
			t.Fatalf("device %s exists=%t, want %t", deviceID, exists, want)
		}
	}
	assertDeviceExists(orphanID, false)
	assertDeviceExists(dependentID, true)
	assertDeviceExists(ownedID, true)
}

func openDeviceQuotaTestPool(t *testing.T, applicationName string) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run the PostgreSQL device quota tests")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 4
	config.ConnConfig.RuntimeParams["application_name"] = applicationName
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedDeviceQuotaFixture(t *testing.T, pool *pgxpool.Pool, deviceCount int) deviceQuotaFixture {
	t.Helper()
	ctx := context.Background()
	plainTextPassword := "quota-test-password"
	passwordHash, err := password.Hash(plainTextPassword)
	if err != nil {
		t.Fatalf("hash quota test password: %v", err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	fixture := deviceQuotaFixture{username: "device_quota_" + suffix, password: plainTextPassword}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin quota fixture: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, $2, 'member') RETURNING id::text
	`, fixture.username, passwordHash).Scan(&fixture.userID); err != nil {
		t.Fatalf("insert quota user: %v", err)
	}
	for index := range deviceCount {
		var deviceID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO devices (user_id, name, platform, last_seen_at)
			VALUES ($1::uuid, $2, 'test', now()) RETURNING id::text
		`, fixture.userID, fmt.Sprintf("Quota fixture %s %d", suffix, index)).Scan(&deviceID); err != nil {
			t.Fatalf("insert quota device %d: %v", index, err)
		}
		accessHash := sha256.Sum256([]byte(deviceID))
		if _, err := tx.Exec(ctx, `
			INSERT INTO auth_sessions (
				user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at
			) VALUES ($1::uuid, $2::uuid, $3, now() + interval '1 hour', now() + interval '2 hours')
		`, fixture.userID, deviceID, accessHash[:]); err != nil {
			t.Fatalf("insert quota device session %d: %v", index, err)
		}
		fixture.deviceIDs = append(fixture.deviceIDs, deviceID)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit quota fixture: %v", err)
	}
	return fixture
}

func cleanupDeviceQuotaFixtures(t *testing.T, pool *pgxpool.Pool, fixtures ...deviceQuotaFixture) {
	t.Helper()
	userIDs := make([]string, 0, len(fixtures))
	for _, fixture := range fixtures {
		userIDs = append(userIDs, fixture.userID)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := pool.Exec(ctx, "DELETE FROM auth_sessions WHERE user_id = ANY($1::uuid[])", userIDs); err != nil {
			t.Errorf("delete quota sessions: %v", err)
		}
		if _, err := pool.Exec(ctx, "DELETE FROM device_authorizations WHERE approved_user_id = ANY($1::uuid[])", userIDs); err != nil {
			t.Errorf("delete quota authorizations: %v", err)
		}
		if _, err := pool.Exec(ctx, "DELETE FROM devices WHERE user_id = ANY($1::uuid[])", userIDs); err != nil {
			t.Errorf("delete quota devices: %v", err)
		}
		if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::uuid[])", userIDs); err != nil {
			t.Errorf("delete quota users: %v", err)
		}
	})
}
func insertApprovedQuotaDeviceAuthorization(t *testing.T, pool *pgxpool.Pool, userID string) string {
	t.Helper()
	deviceCode := deviceCodePrefix + userID
	var categoryID string
	if err := pool.QueryRow(context.Background(), "SELECT id::text FROM access_categories WHERE is_default").Scan(&categoryID); err != nil {
		t.Fatalf("query default category: %v", err)
	}
	sourceHash := deviceAuthorizationSourceHash("")
	for range deviceUserCodeInsertAttempts {
		userCode, err := newDeviceUserCode()
		if err != nil {
			t.Fatalf("generate quota device user code: %v", err)
		}
		command, err := pool.Exec(context.Background(), `
			INSERT INTO device_authorizations (
				device_code_hash, user_code, device_name, platform, source_hash, approved_user_id,
				approved_category_id, approved_at, expires_at
			) VALUES ($1, $2, 'Quota paired device', 'test', $3, $4::uuid, $5::uuid, now(), now() + interval '10 minutes')
			ON CONFLICT DO NOTHING
		`, tokenDigest(deviceCode), userCode, sourceHash[:], userID, categoryID)
		if err != nil {
			t.Fatalf("insert approved device authorization: %v", err)
		}
		if command.RowsAffected() == 1 {
			return deviceCode
		}
	}
	t.Fatal("could not allocate quota device user code")
	return ""
}

func assertDeviceCount(t *testing.T, pool *pgxpool.Pool, userID string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM devices WHERE user_id = $1::uuid", userID).Scan(&count); err != nil {
		t.Fatalf("count quota devices: %v", err)
	}
	if count != want {
		t.Fatalf("device count = %d, want %d", count, want)
	}
}
