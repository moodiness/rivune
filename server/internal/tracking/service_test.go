package tracking

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

const (
	trackingTestProfileID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	trackingTestUserID    = "22222222-2222-4222-8222-222222222222"
)

func TestAuthorizeProfileRejectsMalformedID(t *testing.T) {
	pool := openTrackingTestPool(t)
	service := &Service{pool: pool}
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin authorization transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = service.authorizeProfile(ctx, tx, trackingTestPrincipal(), "not-a-uuid")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("authorize malformed profile ID: got %v, want ErrForbidden", err)
	}
}

func TestAuthorizeProfileCanonicalizesUppercaseID(t *testing.T) {
	pool := openTrackingTestPool(t)
	service := &Service{pool: pool}
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin authorization transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	profileID, err := service.authorizeProfile(ctx, tx, trackingTestPrincipal(), strings.ToUpper(trackingTestProfileID))
	if err != nil {
		t.Fatalf("authorize uppercase profile ID: %v", err)
	}
	if profileID != trackingTestProfileID {
		t.Fatalf("authorize uppercase profile ID: got %q, want canonical %q", profileID, trackingTestProfileID)
	}
}

func TestCompleteDeviceAuthorizationPreservesQueryError(t *testing.T) {
	pool := openTrackingTestPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TEMPORARY TABLE profile_tracking_authorizations (id uuid PRIMARY KEY)`); err != nil {
		t.Fatalf("create malformed temporary authorizations table: %v", err)
	}

	service := &Service{pool: pool}
	_, err := service.CompleteDeviceAuthorization(ctx, trackingTestPrincipal(), trackingTestProfileID, "trakt", "33333333-3333-4333-8333-333333333333")
	if err == nil {
		t.Fatal("complete authorization unexpectedly succeeded")
	}
	if errors.Is(err, ErrAuthorizationGone) {
		t.Fatalf("query error was reported as ErrAuthorizationGone: %v", err)
	}
	if !strings.Contains(err.Error(), "query tracking authorization") {
		t.Fatalf("query error was not wrapped with operation context: %v", err)
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		t.Fatalf("wrapped error does not preserve PostgreSQL failure: %v", err)
	}
}

func TestCompleteDeviceAuthorizationReportsMissingAndExpiredAsGone(t *testing.T) {
	pool := openTrackingTestPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profile_tracking_authorizations (
			id uuid PRIMARY KEY,
			profile_id uuid NOT NULL,
			provider text NOT NULL,
			provider_code_encrypted bytea NOT NULL,
			interval_seconds integer NOT NULL,
			expires_at timestamptz NOT NULL,
			last_polled_at timestamptz
		)
	`); err != nil {
		t.Fatalf("create temporary authorizations table: %v", err)
	}
	const expiredAuthorizationID = "33333333-3333-4333-8333-333333333333"
	if _, err := pool.Exec(ctx, `
		INSERT INTO profile_tracking_authorizations (
			id, profile_id, provider, provider_code_encrypted, interval_seconds, expires_at
		) VALUES ($1::uuid, $2::uuid, 'trakt', '\x01'::bytea, 5, $3)
	`, expiredAuthorizationID, trackingTestProfileID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("seed expired authorization: %v", err)
	}

	service := &Service{pool: pool}
	for name, authorizationID := range map[string]string{
		"missing": "44444444-4444-4444-8444-444444444444",
		"expired": expiredAuthorizationID,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.CompleteDeviceAuthorization(ctx, trackingTestPrincipal(), trackingTestProfileID, "trakt", authorizationID)
			if !errors.Is(err, ErrAuthorizationGone) {
				t.Fatalf("complete %s authorization: got %v, want ErrAuthorizationGone", name, err)
			}
		})
	}
}

func openTrackingTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL tracking service test")
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profiles (id uuid PRIMARY KEY, category_id uuid);
		CREATE TEMPORARY TABLE user_profile_access (profile_id uuid NOT NULL, user_id uuid NOT NULL, can_manage boolean NOT NULL DEFAULT false)
	`); err != nil {
		t.Fatalf("create tracking authorization fixtures: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO profiles (id) VALUES ($1::uuid)`, trackingTestProfileID); err != nil {
		t.Fatalf("seed tracking authorization fixtures: %v", err)
	}
	return pool
}

func trackingTestPrincipal() auth.Principal {
	return auth.Principal{
		UserID: trackingTestUserID, Role: "admin",
		AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
	}
}
