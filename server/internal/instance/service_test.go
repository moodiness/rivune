package instance

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/database"
)

func TestAcquireSetupPendingBlocksExclusiveSetupLockUntilRelease(t *testing.T) {
	pool := openInstanceTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var configured bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM instances WHERE id = 1)").Scan(&configured); err != nil {
		t.Fatalf("read setup state: %v", err)
	}
	if configured {
		t.Skip("test database is already configured")
	}

	service := NewService(pool, "setup-secret", "UTC", false)
	release, err := service.AcquireSetupPending(ctx)
	if err != nil {
		t.Fatalf("acquire shared admission: %v", err)
	}
	acquired := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		tx, err := pool.Begin(ctx)
		if err != nil {
			close(ready)
			acquired <- err
			return
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		close(ready)
		_, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", setupLockID)
		acquired <- err
	}()
	<-ready

	select {
	case err := <-acquired:
		t.Fatalf("exclusive setup lock acquired while demo admission was held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("exclusive setup lock after release: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("exclusive setup lock remained blocked after release")
	}
}

func TestAdmissionReleaseWorksAfterRequestCancellationAndIsIdempotent(t *testing.T) {
	pool := openInstanceTestPool(t)
	var configured bool
	if err := pool.QueryRow(context.Background(), "SELECT EXISTS (SELECT 1 FROM instances WHERE id = 1)").Scan(&configured); err != nil {
		t.Fatalf("read setup state: %v", err)
	}
	if configured {
		t.Skip("test database is already configured")
	}
	service := NewService(pool, "setup-secret", "UTC", false)
	requestContext, cancel := context.WithCancel(context.Background())
	release, err := service.AcquireSetupPending(requestContext)
	if err != nil {
		t.Fatalf("acquire admission: %v", err)
	}
	cancel()
	release()
	release()

	ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire verification connection: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", setupLockID); err != nil {
		t.Fatalf("exclusive verification lock: %v", err)
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", setupLockID); err != nil {
		t.Fatalf("release verification lock: %v", err)
	}
}

func TestAcquireSetupPendingRejectsConfiguredInstance(t *testing.T) {
	pool := openInstanceTestPool(t)
	var configured bool
	if err := pool.QueryRow(context.Background(), "SELECT EXISTS (SELECT 1 FROM instances WHERE id = 1)").Scan(&configured); err != nil {
		t.Fatalf("read setup state: %v", err)
	}
	if !configured {
		t.Skip("test database is not configured")
	}
	_, err := NewService(pool, "setup-secret", "UTC", false).AcquireSetupPending(context.Background())
	if !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("error = %v, want ErrAlreadyConfigured", err)
	}
}

func TestDemoSessionAdmissionEnforcesSourceAndGlobalLimitsAcrossServices(t *testing.T) {
	pool := openInstanceTestPool(t)
	prepareDemoAdmissionTable(t, pool)
	firstService := NewService(pool, "setup-secret", "UTC", false)
	secondService := NewService(pool, "setup-secret", "UTC", false)
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	sourceA := sha256.Sum256([]byte("source-a"))
	sourceB := sha256.Sum256([]byte("source-b"))
	sourceC := sha256.Sum256([]byte("source-c"))
	firstID, err := admitDemoSession(firstService, sourceA, now, now.Add(time.Hour), 2, 1)
	if err != nil {
		t.Fatalf("admit first source: %v", err)
	}
	preparedOnDenial := false
	_, deniedRelease, err := secondService.AdmitDemoSession(
		context.Background(), sourceA, "", now, now.Add(time.Hour), 2, 1,
		func() error {
			preparedOnDenial = true
			return nil
		},
	)
	if deniedRelease != nil {
		deniedRelease()
		t.Fatal("capacity denial retained a setup admission")
	}
	if !errors.Is(err, ErrDemoSessionCapacity) {
		t.Fatalf("same-source N+1 error = %v, want capacity", err)
	}
	if preparedOnDenial {
		t.Fatal("capacity denial performed session preparation")
	}
	secondID, err := admitDemoSession(secondService, sourceB, now, now.Add(time.Hour), 2, 1)
	if err != nil {
		t.Fatalf("admit other source at exact global limit: %v", err)
	}
	if _, err := admitDemoSession(firstService, sourceC, now, now.Add(time.Hour), 2, 1); !errors.Is(err, ErrDemoSessionCapacity) {
		t.Fatalf("global N+1 error = %v, want capacity", err)
	}
	var active int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM demo_session_admissions WHERE expires_at > $1", now).Scan(&active); err != nil {
		t.Fatalf("count active admissions: %v", err)
	}
	if active != 2 {
		t.Fatalf("active admissions after denials = %d, want 2", active)
	}
	var firstPresent, secondPresent bool
	if err := pool.QueryRow(context.Background(), `
		SELECT
			EXISTS (SELECT 1 FROM demo_session_admissions WHERE id = $1::uuid),
			EXISTS (SELECT 1 FROM demo_session_admissions WHERE id = $2::uuid)
	`, firstID, secondID).Scan(&firstPresent, &secondPresent); err != nil {
		t.Fatalf("verify active admission identities: %v", err)
	}
	if !firstPresent || !secondPresent {
		t.Fatal("capacity denial evicted an active admission")
	}

	releaseSetup, err := secondService.ReleaseDemoSession(context.Background(), firstID)
	if err != nil {
		t.Fatalf("release first admission: %v", err)
	}
	releaseSetup()
	if _, err := admitDemoSession(firstService, sourceC, now, now.Add(time.Hour), 2, 1); err != nil {
		t.Fatalf("released capacity was not reusable: %v", err)
	}
	later := now.Add(2 * time.Hour)
	if _, err := admitDemoSession(secondService, sourceA, later, later.Add(time.Hour), 2, 1); err != nil {
		t.Fatalf("expired admissions did not release capacity: %v", err)
	}
}

func TestConcurrentDemoAdmissionAcrossServicesNeverExceedsGlobalLimit(t *testing.T) {
	pool := openInstanceTestPool(t)
	prepareDemoAdmissionTable(t, pool)
	services := []*Service{
		NewService(pool, "setup-secret", "UTC", false),
		NewService(pool, "setup-secret", "UTC", false),
	}
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	const attempts = 12
	const globalLimit = 4
	start := make(chan struct{})
	results := make(chan error, attempts)
	var wait sync.WaitGroup
	for attempt := range attempts {
		wait.Add(1)
		go func(attempt int) {
			defer wait.Done()
			<-start
			source := sha256.Sum256([]byte(fmt.Sprintf("source-%d", attempt)))
			_, err := admitDemoSession(
				services[attempt%len(services)], source, now, now.Add(time.Hour), globalLimit, globalLimit,
			)
			results <- err
		}(attempt)
	}
	close(start)
	wait.Wait()
	close(results)

	admitted := 0
	denied := 0
	for err := range results {
		switch {
		case err == nil:
			admitted++
		case errors.Is(err, ErrDemoSessionCapacity):
			denied++
		default:
			t.Fatalf("unexpected concurrent admission error: %v", err)
		}
	}
	if admitted != globalLimit || denied != attempts-globalLimit {
		t.Fatalf("concurrent results admitted/denied = %d/%d, want %d/%d", admitted, denied, globalLimit, attempts-globalLimit)
	}
	var stored int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM demo_session_admissions WHERE expires_at > $1", now).Scan(&stored); err != nil {
		t.Fatalf("count concurrent admissions: %v", err)
	}
	if stored != globalLimit {
		t.Fatalf("stored admissions = %d, want %d", stored, globalLimit)
	}
}

func TestDemoAdmissionCleanupIsBounded(t *testing.T) {
	pool := openInstanceTestPool(t)
	prepareDemoAdmissionTable(t, pool)
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	source := sha256.Sum256([]byte("expired-source"))
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO demo_session_admissions (source_hash, created_at, expires_at)
		SELECT $1, $2, $3
		FROM generate_series(1, $4)
	`, source[:], now.Add(-2*time.Hour), now.Add(-time.Hour), demoSessionCleanupLimit+50); err != nil {
		t.Fatalf("seed expired admissions: %v", err)
	}
	activeSource := sha256.Sum256([]byte("active-source"))
	if _, err := admitDemoSession(
		NewService(pool, "setup-secret", "UTC", false), activeSource, now, now.Add(time.Hour), 2, 1,
	); err != nil {
		t.Fatalf("admit after expired cleanup: %v", err)
	}
	var expired int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM demo_session_admissions WHERE expires_at <= $1", now).Scan(&expired); err != nil {
		t.Fatalf("count expired admissions: %v", err)
	}
	if expired != 50 {
		t.Fatalf("expired rows after bounded cleanup = %d, want 50", expired)
	}
}

func TestSetupPersistsInitialJellyfinEnabled(t *testing.T) {
	pool := openInstanceTestPool(t)
	ctx := context.Background()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate setup database: %v", err)
	}
	var configured bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM instances WHERE id = 1)").Scan(&configured); err != nil {
		t.Fatalf("read setup state: %v", err)
	}
	if configured {
		t.Skip("test database is already configured")
	}

	result, err := NewService(pool, "setup-secret", "UTC", true).Setup(ctx, "setup-secret", SetupInput{
		InstanceName: "Jellyfin settings test",
		Username:     "jellyfin-settings-admin",
		Password:     "password-long-enough",
		ProfileName:  "Administrator",
	})
	if err != nil {
		t.Fatalf("setup instance: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM profiles WHERE id = $1::uuid", result.ProfileID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1::uuid", result.UserID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM instances WHERE id = 1")
	})

	var enabled bool
	if err := pool.QueryRow(ctx, `
		SELECT (settings ->> 'jellyfinEnabled')::boolean
		FROM instance_settings
		WHERE instance_id = 1
	`).Scan(&enabled); err != nil {
		t.Fatalf("read initial Jellyfin setting: %v", err)
	}
	if !enabled {
		t.Fatal("setup did not persist enabled Jellyfin setting")
	}
}

func admitDemoSession(
	service *Service,
	source [sha256.Size]byte,
	now, expiresAt time.Time,
	globalLimit, sourceLimit int,
) (string, error) {
	admissionID, release, err := service.AdmitDemoSession(
		context.Background(), source, "", now, expiresAt, globalLimit, sourceLimit, func() error { return nil },
	)
	if release != nil {
		release()
	}
	return admissionID, err
}

func prepareDemoAdmissionTable(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if err := database.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate demo admission table: %v", err)
	}
	var configured bool
	if err := pool.QueryRow(context.Background(), "SELECT EXISTS (SELECT 1 FROM instances WHERE id = 1)").Scan(&configured); err != nil {
		t.Fatalf("read setup state: %v", err)
	}
	if configured {
		t.Skip("test database is already configured")
	}
	if _, err := pool.Exec(context.Background(), "DELETE FROM demo_session_admissions"); err != nil {
		t.Fatalf("reset demo admissions: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM demo_session_admissions")
	})
}

func openInstanceTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run PostgreSQL instance admission tests")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
