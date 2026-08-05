package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDeviceAuthorizationSourceHashUsesNetworkGranularity(t *testing.T) {
	if maximumOutstandingDeviceAuthorizationsPerSource != 4 {
		t.Fatalf("per-source capacity = %d, want 4", maximumOutstandingDeviceAuthorizationsPerSource)
	}
	if deviceAuthorizationSourceHash("192.0.2.10") != deviceAuthorizationSourceHash("::ffff:192.0.2.10") {
		t.Fatal("IPv4 and its IPv4-mapped representation did not share a /32 hash")
	}
	if deviceAuthorizationSourceHash("192.0.2.10") == deviceAuthorizationSourceHash("192.0.2.11") {
		t.Fatal("distinct IPv4 /32s shared a source hash")
	}
	if deviceAuthorizationSourceHash("2001:db8:1:2::1") != deviceAuthorizationSourceHash("2001:db8:1:2::ffff") {
		t.Fatal("addresses in the same IPv6 /64 did not share a source hash")
	}
	if deviceAuthorizationSourceHash("2001:db8:1:2::1") == deviceAuthorizationSourceHash("2001:db8:1:3::1") {
		t.Fatal("distinct IPv6 /64s shared a source hash")
	}
	if deviceAuthorizationSourceHash("") != deviceAuthorizationSourceHash("not-an-address") {
		t.Fatal("absent and invalid canonical sources did not share the fail-closed sentinel")
	}
}

func TestDeviceAuthorizationAdmissionEnforcesSourceCapacity(t *testing.T) {
	pool := openDeviceAdmissionPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	baseline := countOutstandingDeviceAuthorizations(t, ctx, pool)
	service := &Service{
		pool:                              pool,
		deviceAuthorizationCapacity:       deviceAdmissionHardCapacityForGeneralCount(baseline + 3),
		deviceAuthorizationSourceCapacity: 2,
	}
	deviceName := fmt.Sprintf("Source capacity regression %d", time.Now().UnixNano())
	cleanupDeviceAdmissionRows(t, pool, deviceName)
	seed := time.Now().UnixNano()
	sourceA := WithClientIP(ctx, deviceAdmissionTestIPv6Source(seed, 1))
	sourceB := WithClientIP(ctx, deviceAdmissionTestIPv6Source(seed, 2))

	for index := range 2 {
		if _, err := service.BeginDeviceAuthorization(sourceA, deviceName, "test"); err != nil {
			t.Fatalf("allocate source A slot %d: %v", index+1, err)
		}
	}
	if _, err := service.BeginDeviceAuthorization(sourceA, deviceName, "test"); !errors.Is(err, ErrDeviceAuthorizationCapacity) {
		t.Fatalf("source A N+1 error = %v, want capacity", err)
	}
	if _, err := service.BeginDeviceAuthorization(sourceB, deviceName, "test"); err != nil {
		t.Fatalf("distinct source was blocked by source A quota: %v", err)
	}
}

func TestDeviceAuthorizationAdmissionProtectsAdaptiveReserve(t *testing.T) {
	pool := openDeviceAdmissionPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	baseline := countOutstandingDeviceAuthorizations(t, ctx, pool)
	hardCapacity := 1
	for {
		generalCapacity := deviceAuthorizationGeneralCapacity(hardCapacity)
		if generalCapacity > baseline && hardCapacity-generalCapacity >= 2 {
			break
		}
		hardCapacity++
	}
	generalCapacity := deviceAuthorizationGeneralCapacity(hardCapacity)
	service := &Service{
		pool:                              pool,
		deviceAuthorizationCapacity:       hardCapacity,
		deviceAuthorizationSourceCapacity: maximumOutstandingDeviceAuthorizationsPerSource,
	}
	deviceName := fmt.Sprintf("Protected reserve regression %d", time.Now().UnixNano())
	cleanupDeviceAdmissionRows(t, pool, deviceName)
	seed := time.Now().UnixNano()
	existingSource := WithClientIP(ctx, deviceAdmissionTestIPv6Source(seed, 1))

	if _, err := service.BeginDeviceAuthorization(existingSource, deviceName, "test"); err != nil {
		t.Fatalf("allocate existing-source general slot: %v", err)
	}
	for index := 1; index < generalCapacity-baseline; index++ {
		source := WithClientIP(ctx, deviceAdmissionTestIPv6Source(seed, index+1))
		if _, err := service.BeginDeviceAuthorization(source, deviceName, "test"); err != nil {
			t.Fatalf("fill general slot %d: %v", index+1, err)
		}
	}
	if _, err := service.BeginDeviceAuthorization(existingSource, deviceName, "test"); !errors.Is(err, ErrDeviceAuthorizationCapacity) {
		t.Fatalf("existing source reserve error = %v, want capacity", err)
	}

	reserveSource := WithClientIP(ctx, deviceAdmissionTestIPv6Source(seed, 30_000))
	if _, err := service.BeginDeviceAuthorization(reserveSource, deviceName, "test"); err != nil {
		t.Fatalf("allocate first slot from protected reserve: %v", err)
	}
	if _, err := service.BeginDeviceAuthorization(reserveSource, deviceName, "test"); !errors.Is(err, ErrDeviceAuthorizationCapacity) {
		t.Fatalf("second slot from protected reserve error = %v, want capacity", err)
	}
	for index := 1; index < hardCapacity-generalCapacity; index++ {
		source := WithClientIP(ctx, deviceAdmissionTestIPv6Source(seed, 30_000+index))
		if _, err := service.BeginDeviceAuthorization(source, deviceName, "test"); err != nil {
			t.Fatalf("fill protected reserve slot %d: %v", index+1, err)
		}
	}
	finalSource := WithClientIP(ctx, deviceAdmissionTestIPv6Source(seed, 60_000))
	if _, err := service.BeginDeviceAuthorization(finalSource, deviceName, "test"); !errors.Is(err, ErrDeviceAuthorizationCapacity) {
		t.Fatalf("hard-cap N+1 error = %v, want capacity", err)
	}
}

func TestDeviceAuthorizationAdmissionSerializesSourceCapacityAcrossInstances(t *testing.T) {
	firstPool := openDeviceAdmissionPool(t)
	secondPool := openDeviceAdmissionPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	baseline := countOutstandingDeviceAuthorizations(t, ctx, firstPool)
	capacity := deviceAdmissionHardCapacityForGeneralCount(baseline + 2)
	firstService := &Service{
		pool: firstPool, deviceAuthorizationCapacity: capacity, deviceAuthorizationSourceCapacity: 1,
	}
	secondService := &Service{
		pool: secondPool, deviceAuthorizationCapacity: capacity, deviceAuthorizationSourceCapacity: 1,
	}
	deviceName := fmt.Sprintf("Cross-instance source capacity %d", time.Now().UnixNano())
	cleanupDeviceAdmissionRows(t, firstPool, deviceName)
	sourceContext := WithClientIP(ctx, deviceAdmissionTestIPv6Source(time.Now().UnixNano(), 1))

	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, service := range []*Service{firstService, secondService} {
		go func() {
			ready.Done()
			<-start
			_, err := service.BeginDeviceAuthorization(sourceContext, deviceName, "test")
			results <- err
		}()
	}
	ready.Wait()
	close(start)

	var successes, capacityErrors int
	for range 2 {
		switch err := <-results; {
		case err == nil:
			successes++
		case errors.Is(err, ErrDeviceAuthorizationCapacity):
			capacityErrors++
		default:
			t.Fatalf("unexpected cross-instance allocation error: %v", err)
		}
	}
	if successes != 1 || capacityErrors != 1 {
		t.Fatalf("successes=%d capacity errors=%d, want one of each", successes, capacityErrors)
	}
}

func TestDeviceAuthorizationAdmissionGroupsIPv6By64(t *testing.T) {
	pool := openDeviceAdmissionPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	baseline := countOutstandingDeviceAuthorizations(t, ctx, pool)
	service := &Service{
		pool:                              pool,
		deviceAuthorizationCapacity:       deviceAdmissionHardCapacityForGeneralCount(baseline + 3),
		deviceAuthorizationSourceCapacity: 1,
	}
	deviceName := fmt.Sprintf("IPv6 source grouping %d", time.Now().UnixNano())
	cleanupDeviceAdmissionRows(t, pool, deviceName)
	seed := uint16(time.Now().UnixNano())
	first := fmt.Sprintf("2001:db8:%x:1::1", seed)
	same64 := fmt.Sprintf("2001:db8:%x:1::ffff", seed)
	different64 := fmt.Sprintf("2001:db8:%x:2::1", seed)

	if deviceAuthorizationSourceHash(first) != deviceAuthorizationSourceHash(same64) {
		t.Fatal("addresses in the same IPv6 /64 did not share a source hash")
	}
	if deviceAuthorizationSourceHash(first) == deviceAuthorizationSourceHash(different64) {
		t.Fatal("addresses in distinct IPv6 /64s shared a source hash")
	}
	if _, err := service.BeginDeviceAuthorization(WithClientIP(ctx, first), deviceName, "test"); err != nil {
		t.Fatalf("allocate first IPv6 /64 slot: %v", err)
	}
	if _, err := service.BeginDeviceAuthorization(WithClientIP(ctx, same64), deviceName, "test"); !errors.Is(err, ErrDeviceAuthorizationCapacity) {
		t.Fatalf("same IPv6 /64 N+1 error = %v, want capacity", err)
	}
	if _, err := service.BeginDeviceAuthorization(WithClientIP(ctx, different64), deviceName, "test"); err != nil {
		t.Fatalf("distinct IPv6 /64 was blocked: %v", err)
	}
}

func TestDeviceAuthorizationAdmissionFailsClosedWithoutSource(t *testing.T) {
	pool := openDeviceAdmissionPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	baseline := countOutstandingDeviceAuthorizations(t, ctx, pool)
	missingSourceHash := deviceAuthorizationSourceHash("")
	var missingSourceBaseline int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM device_authorizations
		WHERE consumed_at IS NULL AND expires_at > now() AND source_hash = $1
	`, missingSourceHash[:]).Scan(&missingSourceBaseline); err != nil {
		t.Fatalf("count missing-source baseline: %v", err)
	}
	service := &Service{
		pool:                              pool,
		deviceAuthorizationCapacity:       deviceAdmissionHardCapacityForGeneralCount(baseline + 2),
		deviceAuthorizationSourceCapacity: missingSourceBaseline + 1,
	}
	deviceName := fmt.Sprintf("Missing source regression %d", time.Now().UnixNano())
	cleanupDeviceAdmissionRows(t, pool, deviceName)

	if _, err := service.BeginDeviceAuthorization(ctx, deviceName, "test"); err != nil {
		t.Fatalf("allocate fail-closed sentinel slot: %v", err)
	}
	if _, err := service.BeginDeviceAuthorization(ctx, deviceName, "test"); !errors.Is(err, ErrDeviceAuthorizationCapacity) {
		t.Fatalf("missing-source N+1 error = %v, want capacity", err)
	}
}

func TestExpiredDeviceAuthorizationReleasesSourceCapacity(t *testing.T) {
	pool := openDeviceAdmissionPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	baseline := countOutstandingDeviceAuthorizations(t, ctx, pool)
	service := &Service{
		pool:                              pool,
		deviceAuthorizationCapacity:       deviceAdmissionHardCapacityForGeneralCount(baseline + 2),
		deviceAuthorizationSourceCapacity: 1,
	}
	deviceName := fmt.Sprintf("Expired source capacity %d", time.Now().UnixNano())
	cleanupDeviceAdmissionRows(t, pool, deviceName)
	sourceContext := WithClientIP(ctx, deviceAdmissionTestIPv6Source(time.Now().UnixNano(), 1))

	if _, err := service.BeginDeviceAuthorization(sourceContext, deviceName, "test"); err != nil {
		t.Fatalf("allocate source slot before expiry: %v", err)
	}
	result, err := pool.Exec(ctx, `
		UPDATE device_authorizations
		SET expires_at = now() - interval '1 second'
		WHERE device_name = $1
	`, deviceName)
	if err != nil {
		t.Fatalf("expire source slot: %v", err)
	}
	if result.RowsAffected() != 1 {
		t.Fatalf("expired rows = %d, want 1", result.RowsAffected())
	}
	if _, err := service.BeginDeviceAuthorization(sourceContext, deviceName, "test"); err != nil {
		t.Fatalf("reallocate source slot after expiry: %v", err)
	}
}

func countOutstandingDeviceAuthorizations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM device_authorizations
		WHERE consumed_at IS NULL AND expires_at > now()
	`).Scan(&count); err != nil {
		t.Fatalf("count outstanding device authorizations: %v", err)
	}
	return count
}

func deviceAdmissionHardCapacityForGeneralCount(target int) int {
	hardCapacity := 1
	for deviceAuthorizationGeneralCapacity(hardCapacity) < target {
		hardCapacity++
	}
	return hardCapacity
}

func deviceAdmissionTestIPv6Source(seed int64, segment int) string {
	return fmt.Sprintf("2001:db8:%x:%x::1", uint16(seed), uint16(segment))
}

func cleanupDeviceAdmissionRows(t *testing.T, pool *pgxpool.Pool, deviceName string) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DELETE FROM device_authorizations WHERE device_name = $1", deviceName); err != nil {
			t.Errorf("clean device authorization admission rows: %v", err)
		}
	})
}

func TestDeviceAuthorizationCleanupIsExpiredAndBatchBounded(t *testing.T) {
	pool := openDeviceAdmissionPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ids := make([]string, 0, 3)
	sourceHash := deviceAuthorizationSourceHash("")
	for range 3 {
		deviceHash := make([]byte, 32)
		if _, err := rand.Read(deviceHash); err != nil {
			t.Fatalf("generate device hash: %v", err)
		}
		userCode, err := newDeviceUserCode()
		if err != nil {
			t.Fatalf("generate user code: %v", err)
		}
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO device_authorizations (device_code_hash, user_code, device_name, platform, source_hash, expires_at)
			VALUES ($1, $2, 'Expired cleanup regression', 'test', $3, now() - interval '100 years')
			RETURNING id::text
		`, deviceHash, userCode, sourceHash[:]).Scan(&id); err != nil {
			t.Fatalf("insert expired device authorization: %v", err)
		}
		ids = append(ids, id)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM device_authorizations WHERE id = ANY($1::uuid[])", ids)
	})

	result, err := pool.Exec(ctx, cleanupStaleDeviceAuthorizationsSQL, 2)
	if err != nil {
		t.Fatalf("run bounded device authorization cleanup: %v", err)
	}
	if result.RowsAffected() != 2 {
		t.Fatalf("cleanup removed %d rows, want exactly 2", result.RowsAffected())
	}
	var remaining int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM device_authorizations WHERE id = ANY($1::uuid[])", ids).Scan(&remaining); err != nil {
		t.Fatalf("count cleanup regression rows: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("expired rows remaining = %d, want 1 after bounded cleanup", remaining)
	}
}

func openDeviceAdmissionPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run device authorization admission tests")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
