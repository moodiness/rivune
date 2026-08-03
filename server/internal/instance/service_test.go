package instance

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

	service := NewService(pool, "setup-secret", "UTC")
	release, err := service.AcquireSetupPending(ctx)
	if err != nil {
		t.Fatalf("acquire shared admission: %v", err)
	}
	acquired := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		tx, err := pool.Begin(ctx)
		if err != nil { close(ready); acquired <- err; return }
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
		if err != nil { t.Fatalf("exclusive setup lock after release: %v", err) }
	case <-ctx.Done():
		t.Fatal("exclusive setup lock remained blocked after release")
	}
}

func TestAdmissionReleaseWorksAfterRequestCancellationAndIsIdempotent(t *testing.T) {
	pool := openInstanceTestPool(t)
	var configured bool
	if err := pool.QueryRow(context.Background(), "SELECT EXISTS (SELECT 1 FROM instances WHERE id = 1)").Scan(&configured); err != nil { t.Fatalf("read setup state: %v", err) }
	if configured { t.Skip("test database is already configured") }
	service := NewService(pool, "setup-secret", "UTC")
	requestContext, cancel := context.WithCancel(context.Background())
	release, err := service.AcquireSetupPending(requestContext)
	if err != nil { t.Fatalf("acquire admission: %v", err) }
	cancel()
	release()
	release()

	ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	conn, err := pool.Acquire(ctx)
	if err != nil { t.Fatalf("acquire verification connection: %v", err) }
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", setupLockID); err != nil { t.Fatalf("exclusive verification lock: %v", err) }
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", setupLockID); err != nil { t.Fatalf("release verification lock: %v", err) }
}

func TestAcquireSetupPendingRejectsConfiguredInstance(t *testing.T) {
	pool := openInstanceTestPool(t)
	var configured bool
	if err := pool.QueryRow(context.Background(), "SELECT EXISTS (SELECT 1 FROM instances WHERE id = 1)").Scan(&configured); err != nil { t.Fatalf("read setup state: %v", err) }
	if !configured { t.Skip("test database is not configured") }
	_, err := NewService(pool, "setup-secret", "UTC").AcquireSetupPending(context.Background())
	if !errors.Is(err, ErrAlreadyConfigured) { t.Fatalf("error = %v, want ErrAlreadyConfigured", err) }
}

func openInstanceTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" { t.Skip("set RIVUNE_TEST_DATABASE_URL to run PostgreSQL instance admission tests") }
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil { t.Fatalf("open test database: %v", err) }
	t.Cleanup(pool.Close)
	return pool
}
