package playback

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/database"
	"github.com/moodiness/rivune/server/internal/auth"
)

func TestFailoverExpirationUsesEarliestSourceReferenceAndFiveHourCeiling(t *testing.T) {
	now := time.Date(2026, time.August, 26, 10, 0, 0, 0, time.UTC)
	ceiling := now.Add(failoverTTL)
	if failoverTTL != 5*time.Hour {
		t.Fatalf("failover TTL = %s, want 5h", failoverTTL)
	}
	if got := earlierFailoverExpiration(ceiling, now.Add(90*time.Minute)); !got.Equal(now.Add(90 * time.Minute)) {
		t.Fatalf("earliest source expiry = %s", got)
	}
	if got := earlierFailoverExpiration(now.Add(time.Hour), now.Add(2*time.Hour)); !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("later source extended failover to %s", got)
	}
}

func TestServiceRestartCleanupIsBoundedAndScheduledContinuationDoesNotStarve(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run PostgreSQL failover restart tests")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	fixture := seedPlaybackAdmissionFixture(t, pool)
	now := time.Now().UTC()
	const candidates = `["primary-source-reference","backup-source-reference"]`
	if _, err := pool.Exec(ctx, `
		INSERT INTO playback_source_failovers
			(auth_session_id, profile_id, candidate_refs, current_source_ref, status, created_at, updated_at, expires_at)
		SELECT $1::uuid, $2::uuid, $3::jsonb, 'primary-source-reference', 'active', $4, $4, $5
		FROM generate_series(1, 600)
	`, fixture.authIDs[0], fixture.profileIDs[0], candidates, now.Add(-3*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO playback_source_failovers
			(auth_session_id, profile_id, candidate_refs, current_source_ref, status, created_at, updated_at, expires_at)
		SELECT $1::uuid, $2::uuid, $3::jsonb, 'primary-source-reference', 'cancelled', $4, $5, $6
		FROM generate_series(1, 600)
	`, fixture.authIDs[0], fixture.profileIDs[0], candidates, now.Add(-2*time.Hour), now.Add(-90*time.Minute), now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO playback_source_failovers
			(auth_session_id, profile_id, candidate_refs, current_source_ref, status, created_at, updated_at, expires_at)
		SELECT $1::uuid, $2::uuid, $3::jsonb, 'primary-source-reference', 'active', $4, $4, $5
		FROM generate_series(1, 6)
	`, fixture.authIDs[0], fixture.profileIDs[0], candidates, now.Add(-time.Hour), now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var currentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO playback_source_failovers
			(auth_session_id, profile_id, candidate_refs, current_source_ref, status, created_at, updated_at, expires_at)
		VALUES ($1::uuid, $2::uuid, $3::jsonb, 'primary-source-reference', 'active', $4, $4, $5)
		RETURNING id::text
	`, fixture.authIDs[0], fixture.profileIDs[0], candidates, now.Add(time.Hour), now.Add(2*time.Hour)).Scan(&currentID); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(pool, nil, nil, MediaOptions{TempDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var remaining, expired, current int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE expires_at <= now()), count(*) FILTER (WHERE id::text = $2)
		FROM playback_source_failovers WHERE auth_session_id = $1::uuid
	`, fixture.authIDs[0], currentID).Scan(&remaining, &expired, &current); err != nil {
		t.Fatal(err)
	}
	if remaining != 707 || expired != 100 || current != 1 {
		t.Fatalf("startup page remaining=%d expired=%d current=%d, want 707/100/1", remaining, expired, current)
	}
	if err := service.cleanupPersistedFailovers(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE id::text = $2)
		FROM playback_source_failovers WHERE auth_session_id = $1::uuid
	`, fixture.authIDs[0], currentID).Scan(&remaining, &current); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 || current != 1 {
		t.Fatalf("scheduled continuation remaining=%d current=%d, want 1/1", remaining, current)
	}
}
func TestCreateFailoverDeletesOnlyOneCleanupBatch(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run PostgreSQL failover request cleanup tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	fixture := seedPlaybackAdmissionFixture(t, pool)
	now := time.Now().UTC()
	grantExpiresAt := now.Add(8 * time.Hour)
	profileID := fixture.profileIDs[0]
	principal := auth.Principal{
		SessionID: fixture.authIDs[0], UserID: fixture.userID, DeviceID: fixture.deviceID,
		ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
	}
	references := newSourceReferenceStore(func() time.Time { return now })
	stored, err := references.putAll(principal, []sourceReference{
		{MediaType: "movie", ResourceID: "bounded-cleanup", Source: Source{ID: "primary", AddonID: "addon"}},
		{MediaType: "movie", ResourceID: "bounded-cleanup", Source: Source{ID: "backup", AddonID: "addon"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidates := []string{stored[0].ID, stored[1].ID}
	encodedCandidates, err := json.Marshal(candidates)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO playback_source_failovers
			(auth_session_id, profile_id, candidate_refs, current_source_ref, created_at, updated_at, expires_at)
		SELECT $1::uuid, $2::uuid, $3::jsonb, $4, $5, $5, $6
		FROM generate_series(1, 501)
	`, principal.SessionID, profileID, encodedCandidates, candidates[0], now.Add(-2*time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	service := &Service{
		pool: pool, now: func() time.Time { return now }, references: references, failoverStartedAt: now.Add(-time.Minute),
		profileTxFactory: func(ctx context.Context, _ auth.Principal) (playbackProfileTransaction, error) { return pool.Begin(ctx) },
	}
	if _, err := service.CreateFailover(ctx, principal, CreateFailoverInput{CandidateSourceRefs: candidates, SelectedSourceRef: candidates[0]}); err != nil {
		t.Fatal(err)
	}
	var expired int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM playback_source_failovers
		WHERE auth_session_id = $1::uuid AND expires_at <= now()
	`, principal.SessionID).Scan(&expired); err != nil {
		t.Fatal(err)
	}
	if expired != 1 {
		t.Fatalf("request cleanup removed %d rows, want exactly one %d-row page", 501-expired, failoverCleanupBatchSize)
	}
}

func TestFailoverAdmissionNeverCreatesSeventeenthActiveRow(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run PostgreSQL failover admission tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	fixture := seedPlaybackAdmissionFixture(t, pool)
	var now time.Time
	if err := pool.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	grantExpiresAt := now.Add(8 * time.Hour)
	profileID := fixture.profileIDs[0]
	principal := auth.Principal{
		SessionID: fixture.authIDs[0], UserID: fixture.userID, DeviceID: fixture.deviceID,
		ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
	}
	references := newSourceReferenceStore(func() time.Time { return now })
	stored, err := references.putAll(principal, []sourceReference{
		{MediaType: "movie", ResourceID: "failover-admission", Source: Source{ID: "primary", AddonID: "addon"}},
		{MediaType: "movie", ResourceID: "failover-admission", Source: Source{ID: "backup", AddonID: "addon"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidates := []string{stored[0].ID, stored[1].ID}
	encodedCandidates, err := json.Marshal(candidates)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO playback_source_failovers (auth_session_id, profile_id, candidate_refs, current_source_ref, expires_at)
		SELECT $1::uuid, $2::uuid, $3::jsonb, $4, now() + interval '1 hour'
		FROM generate_series(1, 15)
	`, principal.SessionID, profileID, encodedCandidates, candidates[0]); err != nil {
		t.Fatal(err)
	}
	service := &Service{
		now: func() time.Time { return now }, references: references,
		profileTxFactory: func(ctx context.Context, _ auth.Principal) (playbackProfileTransaction, error) { return pool.Begin(ctx) },
	}
	input := CreateFailoverInput{CandidateSourceRefs: candidates, SelectedSourceRef: candidates[0]}
	start := make(chan struct{})
	type result struct {
		state FailoverState
		err   error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			state, createErr := service.CreateFailover(ctx, principal, input)
			results <- result{state: state, err: createErr}
		}()
	}
	close(start)
	successes, rejections := 0, 0
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			successes++
			if !result.state.ExpiresAt.Equal(now.Add(sourceReferenceTTL)) {
				t.Fatalf("failover expiry = %s, want source expiry %s", result.state.ExpiresAt, now.Add(sourceReferenceTTL))
			}
		case errors.Is(result.err, ErrMediaCapacityReached):
			rejections++
		default:
			t.Fatalf("concurrent failover creation error: %v", result.err)
		}
	}
	if successes != 1 || rejections != 1 {
		t.Fatalf("concurrent admission successes=%d rejections=%d", successes, rejections)
	}
	var active int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM playback_source_failovers
		WHERE auth_session_id = $1::uuid AND status = 'active' AND expires_at > now()
	`, principal.SessionID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != maximumFailoversPerAuthSession {
		t.Fatalf("active failovers = %d, want %d and never 17", active, maximumFailoversPerAuthSession)
	}
}


func TestFailoverAdvancesInStableOrderAndResumesPosition(t *testing.T) {
	now := time.Date(2026, time.August, 26, 10, 0, 0, 0, time.UTC)
	record := failoverRecord{
		ID: "11111111-1111-4111-8111-111111111111",
		Candidates: []string{"primary-source-reference", "backup-source-reference", "last-source-reference"},
		CurrentSourceRef: "primary-source-reference", MaximumAttempts: 2, Revision: 1, Status: "active",
		ExpiresAt: now.Add(time.Hour),
	}

	next, advanced := advanceFailoverRecord(record, FailoverSourceFailed, 347.75, now)
	if !advanced || next.CurrentSourceRef != "backup-source-reference" {
		t.Fatalf("first failover = advanced %t current %q", advanced, next.CurrentSourceRef)
	}
	if next.PositionSeconds != 347.75 || next.AttemptCount != 1 || next.Revision != 2 {
		t.Fatalf("first failover state = %+v", next)
	}
	state := projectFailover(next)
	if state.CurrentPosition != 1 || state.CandidateHealth[0].Status != "cooling_down" || state.CandidateHealth[1].Status != "current" {
		t.Fatalf("projected failover = %+v", state)
	}
	if state.CandidateHealth[0].CooldownUntil == nil || !state.CandidateHealth[0].CooldownUntil.Equal(now.Add(failoverCooldown)) {
		t.Fatalf("primary cooldown = %+v", state.CandidateHealth[0])
	}
}

func TestFailoverNonEligibleErrorsNeverAdvance(t *testing.T) {
	for _, failure := range []FailoverError{FailoverDecodeFailed, FailoverAccessDenied, FailoverUserCancelled} {
		if !validFailoverError(failure) {
			t.Fatalf("closed enum rejected known failure %q", failure)
		}
		if eligibleFailoverError(failure) {
			t.Fatalf("non-eligible failure %q would advance", failure)
		}
	}
	if validFailoverError("provider_token=https://secret.example") {
		t.Fatal("open-ended failure value was accepted")
	}
}

func TestFailoverBudgetStopsWithoutCycling(t *testing.T) {
	now := time.Date(2026, time.August, 26, 10, 0, 0, 0, time.UTC)
	record := failoverRecord{
		Candidates: []string{"primary-source-reference", "backup-source-reference", "unused-source-reference"},
		CurrentSourceRef: "primary-source-reference", MaximumAttempts: 1, Revision: 1, Status: "active",
	}
	first, advanced := advanceFailoverRecord(record, FailoverSourceTimeout, 10, now)
	if !advanced || first.CurrentSourceRef != "backup-source-reference" {
		t.Fatalf("first failover did not select backup: %+v", first)
	}
	stopped, advanced := advanceFailoverRecord(first, FailoverEndedEarly, 20, now.Add(time.Second))
	if advanced || stopped.Status != "exhausted" || stopped.CurrentSourceRef != "backup-source-reference" || stopped.AttemptCount != 1 {
		t.Fatalf("budget did not stop failover: advanced=%t record=%+v", advanced, stopped)
	}
	if candidateIndex(stopped.FailedCandidates, "primary-source-reference") < 0 || candidateIndex(stopped.FailedCandidates, "backup-source-reference") < 0 {
		t.Fatalf("failed candidates were not persisted: %v", stopped.FailedCandidates)
	}
}

func TestProjectedFailoverDiagnosticsNeverExposeCandidates(t *testing.T) {
	state := projectFailover(failoverRecord{
		Candidates: []string{"opaque-primary", "opaque-backup"}, FailedCandidates: []string{"opaque-primary"},
		CurrentSourceRef: "opaque-backup", Status: "active", Revision: 2,
	})
	if len(state.CandidateHealth) != 2 || state.CandidateHealth[0].Position != 0 || state.CandidateHealth[1].Position != 1 {
		t.Fatalf("candidate health = %+v", state.CandidateHealth)
	}
	for _, health := range state.CandidateHealth {
		if health.Status != "cooling_down" && health.Status != "current" && health.Status != "available" {
			t.Fatalf("candidate health exposed an unexpected value: %+v", health)
		}
	}
}
