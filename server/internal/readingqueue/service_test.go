package readingqueue

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
)

func TestInputBoundsAndClosedMediaTypes(t *testing.T) {
	valid := AddInput{
		OperationID: "11111111-1111-4111-8111-111111111111", ExpectedRevision: 1,
		MediaType: "movie", ResourceID: "resource", Title: "Title",
	}
	if !validAdd(normalizeAdd(valid)) {
		t.Fatal("valid bounded queue input was rejected")
	}
	for _, mutate := range []func(AddInput) AddInput{
		func(input AddInput) AddInput { input.MediaType = "future"; return input },
		func(input AddInput) AddInput { input.ResourceID = string(bytes.Repeat([]byte("r"), 513)); return input },
		func(input AddInput) AddInput { input.Title = ""; return input },
		func(input AddInput) AddInput { input.PosterURL = string(bytes.Repeat([]byte("p"), 2049)); return input },
		func(input AddInput) AddInput { input.OperationID = "not-a-uuid"; return input },
	} {
		if validAdd(normalizeAdd(mutate(valid))) {
			t.Fatal("invalid queue input was accepted")
		}
	}
}

func TestQueuePersistsRejectsStaleRevisionAndDefinesDuplicates(t *testing.T) {
	fixture := newQueueDatabaseFixture(t)
	ctx := context.Background()
	service := NewService(fixture.pool)

	empty, err := service.Queue(ctx, fixture.principal, fixture.profileID)
	if err != nil || empty.Revision != 1 || len(empty.Items) != 0 {
		t.Fatalf("initial queue = %+v, error %v", empty, err)
	}
	first := AddInput{OperationID: "11111111-1111-4111-8111-111111111111", ExpectedRevision: 1, MediaType: "movie", ResourceID: "movie:first", Title: "First"}
	added, err := service.Add(ctx, fixture.principal, fixture.profileID, first)
	if err != nil || added.Revision != 2 || added.AffectedItemID == "" || added.Duplicate {
		t.Fatalf("add result = %+v, error %v", added, err)
	}
	duplicate, err := service.Add(ctx, fixture.principal, fixture.profileID, AddInput{
		OperationID: "22222222-2222-4222-8222-222222222222", ExpectedRevision: 2,
		MediaType: "movie", ResourceID: "movie:first", Title: "Different presentation is ignored for identity duplicate",
	})
	if err != nil || !duplicate.Duplicate || duplicate.Revision != 2 || duplicate.AffectedItemID != added.AffectedItemID {
		t.Fatalf("identity duplicate = %+v, error %v", duplicate, err)
	}
	second, err := service.Add(ctx, fixture.principal, fixture.profileID, AddInput{
		OperationID: "33333333-3333-4333-8333-333333333333", ExpectedRevision: 2,
		MediaType: "series", ResourceID: "series:second", Title: "Second",
	})
	if err != nil || second.Revision != 3 {
		t.Fatalf("second add = %+v, error %v", second, err)
	}
	stale := ReorderInput{OperationID: "44444444-4444-4444-8444-444444444444", ExpectedRevision: 2, ItemIDs: []string{second.AffectedItemID, added.AffectedItemID}}
	if _, err := service.Reorder(ctx, fixture.principal, fixture.profileID, stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale reorder error = %v, want %v", err, ErrConflict)
	}
	stale.ExpectedRevision = 3
	reordered, err := service.Reorder(ctx, fixture.principal, fixture.profileID, stale)
	if err != nil || reordered.Revision != 4 {
		t.Fatalf("reorder = %+v, error %v", reordered, err)
	}
	consumeInput := MutationInput{OperationID: "55555555-5555-4555-8555-555555555555", ExpectedRevision: 4}
	consumed, err := service.Consume(ctx, fixture.principal, fixture.profileID, second.AffectedItemID, consumeInput)
	if err != nil || consumed.Revision != 5 || consumed.AffectedItemID != second.AffectedItemID {
		t.Fatalf("consume = %+v, error %v", consumed, err)
	}
	replayed, err := service.Consume(ctx, fixture.principal, fixture.profileID, second.AffectedItemID, consumeInput)
	if err != nil || replayed != consumed {
		t.Fatalf("idempotent consume replay = %+v, error %v; want %+v", replayed, err, consumed)
	}
	if _, err := service.Remove(ctx, fixture.principal, fixture.profileID, added.AffectedItemID, consumeInput); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("operation id reuse error = %v, want %v", err, ErrOperationConflict)
	}

	restarted := NewService(fixture.pool)
	persisted, err := restarted.Queue(ctx, fixture.principal, fixture.profileID)
	if err != nil || persisted.Revision != 5 || len(persisted.Items) != 1 || persisted.Items[0].ID != added.AffectedItemID || persisted.Items[0].Position != 0 {
		t.Fatalf("persisted queue after service restart = %+v, error %v", persisted, err)
	}
}

func TestOperationRetentionPolicyIsExplicitAndCleanupIsBounded(t *testing.T) {
	if operationRetryWindow != 24*time.Hour {
		t.Fatalf("operation retry window = %s, want 24h", operationRetryWindow)
	}
	if operationCleanupBatch != 128 {
		t.Fatalf("operation cleanup batch = %d, want 128", operationCleanupBatch)
	}
	normalized := strings.Join(strings.Fields(cleanupExpiredOperationsSQL), " ")
	for _, fragment := range []string{
		"WHERE expires_at <= $1",
		"ORDER BY expires_at, profile_id, operation_id",
		"LIMIT $2",
		"FOR UPDATE SKIP LOCKED",
		"DELETE FROM profile_reading_queue_operations operation USING expired",
	} {
		if !strings.Contains(normalized, fragment) {
			t.Fatalf("operation cleanup lacks %q: %s", fragment, normalized)
		}
	}
}

func TestOperationRetryWindowExpiresAndCanBeReused(t *testing.T) {
	fixture := newQueueDatabaseFixture(t)
	ctx := context.Background()
	service := NewService(fixture.pool)
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	grantExpiresAt := createdAt.Add(2 * operationRetryWindow)
	fixture.principal.ProfileGrantExpiresAt = &grantExpiresAt
	service.now = func() time.Time { return createdAt }
	input := AddInput{
		OperationID: "61111111-1111-4111-8111-111111111111", ExpectedRevision: 1,
		MediaType: "movie", ResourceID: "retained:movie", Title: "Retained",
	}
	created, err := service.Add(ctx, fixture.principal, fixture.profileID, input)
	if err != nil || created.Revision != 2 {
		t.Fatalf("create retained operation = %+v, error %v", created, err)
	}
	var storedCreatedAt, expiresAt time.Time
	if err := fixture.pool.QueryRow(ctx, `SELECT created_at,expires_at FROM profile_reading_queue_operations WHERE profile_id=$1::uuid AND operation_id=$2::uuid`, fixture.profileID, input.OperationID).Scan(&storedCreatedAt, &expiresAt); err != nil {
		t.Fatalf("read retained operation: %v", err)
	}
	if !storedCreatedAt.Equal(createdAt) || !expiresAt.Equal(createdAt.Add(operationRetryWindow)) {
		t.Fatalf("operation retention = created %s expires %s", storedCreatedAt, expiresAt)
	}

	service.now = func() time.Time { return expiresAt.Add(-time.Nanosecond) }
	if replayed, err := service.Add(ctx, fixture.principal, fixture.profileID, input); err != nil || replayed != created {
		t.Fatalf("retry immediately before expiry = %+v, error %v", replayed, err)
	}
	service.now = func() time.Time { return expiresAt }
	if _, err := service.Add(ctx, fixture.principal, fixture.profileID, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry at expiry error = %v, want stale revision conflict", err)
	}

	input.ExpectedRevision = 2
	reused, err := service.Add(ctx, fixture.principal, fixture.profileID, input)
	if err != nil || reused.Revision != 2 || !reused.Duplicate {
		t.Fatalf("reuse after retention window = %+v, error %v", reused, err)
	}
	var operationCount int
	var renewedExpiry time.Time
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*),max(expires_at) FROM profile_reading_queue_operations WHERE profile_id=$1::uuid AND operation_id=$2::uuid`, fixture.profileID, input.OperationID).Scan(&operationCount, &renewedExpiry); err != nil {
		t.Fatalf("read renewed operation: %v", err)
	}
	if operationCount != 1 || !renewedExpiry.Equal(expiresAt.Add(operationRetryWindow)) {
		t.Fatalf("renewed operation count=%d expiry=%s, want %s", operationCount, renewedExpiry, expiresAt.Add(operationRetryWindow))
	}
}

func TestOperationCleanupDeletesOnlyOneBoundedBatch(t *testing.T) {
	fixture := newQueueDatabaseFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO profile_reading_queue_operations
		(profile_id,operation_id,operation,request_hash,result_revision,created_at,expires_at)
		SELECT $1::uuid,gen_random_uuid(),'add',decode(repeat('ab',32),'hex'),1,clock_timestamp()-interval '2 hours',clock_timestamp()-interval '1 hour'
		FROM generate_series(1,$2)
	`, fixture.profileID, operationCleanupBatch+3); err != nil {
		t.Fatalf("seed expired operations: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO profile_reading_queue_operations
		(profile_id,operation_id,operation,request_hash,result_revision,created_at,expires_at)
		VALUES ($1::uuid,gen_random_uuid(),'add',decode(repeat('cd',32),'hex'),1,clock_timestamp(),clock_timestamp()+interval '1 hour')
	`, fixture.profileID); err != nil {
		t.Fatalf("seed active operation: %v", err)
	}
	tx, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin cleanup transaction: %v", err)
	}
	deleted, err := (postgresRepository{}).PruneOperations(ctx, tx, now, operationCleanupBatch)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("prune operation batch: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit operation cleanup: %v", err)
	}
	var expired, active int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE expires_at<=$2),count(*) FILTER (WHERE expires_at>$2) FROM profile_reading_queue_operations WHERE profile_id=$1::uuid`, fixture.profileID, now).Scan(&expired, &active); err != nil {
		t.Fatalf("count retained operations: %v", err)
	}
	if deleted != operationCleanupBatch || expired != 3 || active != 1 {
		t.Fatalf("bounded cleanup deleted=%d expired=%d active=%d", deleted, expired, active)
	}
}

func TestStaleMutationStillCommitsOperationCleanup(t *testing.T) {
	fixture := newQueueDatabaseFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO profile_reading_queue_operations
		(profile_id,operation_id,operation,request_hash,result_revision,created_at,expires_at)
		VALUES ($1::uuid,gen_random_uuid(),'add',decode(repeat('ef',32),'hex'),1,clock_timestamp()-interval '2 hours',clock_timestamp()-interval '1 hour')
	`, fixture.profileID); err != nil {
		t.Fatalf("seed expired operation: %v", err)
	}
	service := NewService(fixture.pool)
	service.now = func() time.Time { return now }
	_, err := service.Add(ctx, fixture.principal, fixture.profileID, AddInput{
		OperationID: "71111111-1111-4111-8111-111111111111", ExpectedRevision: 999,
		MediaType: "movie", ResourceID: "stale:movie", Title: "Stale",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale mutation error = %v, want %v", err, ErrConflict)
	}
	var expired int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM profile_reading_queue_operations WHERE profile_id=$1::uuid AND expires_at<=$2`, fixture.profileID, now).Scan(&expired); err != nil {
		t.Fatalf("count expired operations: %v", err)
	}
	if expired != 0 {
		t.Fatalf("stale mutation rolled back cleanup; expired operations=%d", expired)
	}
}


type queueDatabaseFixture struct {
	pool      *pgxpool.Pool
	principal auth.Principal
	profileID string
}

func newQueueDatabaseFixture(t *testing.T) queueDatabaseFixture {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run PostgreSQL reading queue tests")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open reading queue test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate reading queue test database: %v", err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	accessHash := sha256.Sum256([]byte("reading-queue-access-" + suffix))
	contextHash := sha256.Sum256([]byte("reading-queue-context-" + suffix))
	var fixture queueDatabaseFixture
	fixture.pool = pool
	if err := pool.QueryRow(context.Background(), `
		WITH test_user AS (
			INSERT INTO users (username,password_hash,role) VALUES ($1,'unused-test-hash','admin') RETURNING id
		), profile AS (
			INSERT INTO profiles (name) VALUES ($2) RETURNING id,category_id
		), device AS (
			INSERT INTO devices (user_id,name,platform,category_id,approved_at)
			SELECT test_user.id,'Reading queue test','web',profile.category_id,now() FROM test_user,profile RETURNING id
		), session AS (
			INSERT INTO auth_sessions
			(user_id,device_id,authorization_scope,category_id,active_profile_id,profile_grant_expires_at,profile_context_hash,access_token_hash,access_expires_at,refresh_expires_at,last_seen_at)
			SELECT test_user.id,device.id,'global_admin',NULL,profile.id,now()+interval '2 hours',$3,$4,now()+interval '1 hour',now()+interval '2 hours',now()
			FROM test_user,device,profile RETURNING id
		)
		SELECT test_user.id::text,device.id::text,profile.id::text,session.id::text FROM test_user,device,profile,session
	`, "reading-queue-"+suffix, "Reading queue "+suffix, contextHash[:], accessHash[:]).Scan(
		&fixture.principal.UserID, &fixture.principal.DeviceID, &fixture.profileID, &fixture.principal.SessionID,
	); err != nil {
		t.Fatalf("seed reading queue fixture: %v", err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	fixture.principal.Role = "admin"
	fixture.principal.AuthorizationScope = auth.AuthorizationScopeGlobalAdministrator
	fixture.principal.ActiveProfileID = new(fixture.profileID)
	fixture.principal.ProfileGrantExpiresAt = &expiresAt
	fixture.principal.ProfileContextHash = append([]byte(nil), contextHash[:]...)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1::uuid`, fixture.principal.UserID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id=$1::uuid`, fixture.profileID)
	})
	return fixture
}
