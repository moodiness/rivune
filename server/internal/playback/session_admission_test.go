package playback

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
)

type singlePlaybackSessionRows struct {
	id      string
	pending bool
}

func (rows *singlePlaybackSessionRows) Close() { rows.pending = false }
func (*singlePlaybackSessionRows) Err() error  { return nil }
func (*singlePlaybackSessionRows) CommandTag() pgconn.CommandTag {
	return pgconn.NewCommandTag("DELETE 1")
}
func (*singlePlaybackSessionRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (rows *singlePlaybackSessionRows) Next() bool {
	pending := rows.pending
	rows.pending = false
	return pending
}
func (*singlePlaybackSessionRows) Values() ([]any, error) { return nil, nil }
func (*singlePlaybackSessionRows) RawValues() [][]byte    { return nil }
func (*singlePlaybackSessionRows) Conn() *pgx.Conn        { return nil }
func (rows *singlePlaybackSessionRows) Scan(destinations ...any) error {
	if len(destinations) != 1 {
		return errors.New("unexpected inactive playback session destination count")
	}
	destination, ok := destinations[0].(*string)
	if !ok {
		return errors.New("unexpected inactive playback session destination")
	}
	*destination = rows.id
	return nil
}

func TestPlaybackSessionAssetPayloadIsBoundedBeforeStorage(t *testing.T) {
	empty, err := encodePlaybackSessionAssets(nil)
	if err != nil || string(empty) != "[]" {
		t.Fatalf("empty playback assets = %q, %v; want []", empty, err)
	}
	const prefix = "https://media.example/"
	maximumURL := prefix + strings.Repeat("a", 8192-len(prefix))
	assets := make([]storedAsset, 0, maximumAggregateProviderSubtitles+1)
	assets = append(assets, storedAsset{ID: "stream-1", Kind: "stream", URL: maximumURL})
	for index := range maximumAggregateProviderSubtitles {
		assets = append(assets, storedAsset{ID: fmt.Sprintf("subtitle-%d", index+1), Kind: "subtitle", URL: maximumURL})
	}
	encoded, err := encodePlaybackSessionAssets(assets)
	if err != nil {
		t.Fatalf("encode maximum valid selected asset set: %v", err)
	}
	if len(encoded) >= maximumPlaybackSessionAssetBytes {
		t.Fatalf("maximum valid selected asset set encoded to %d bytes, limit %d", len(encoded), maximumPlaybackSessionAssetBytes)
	}

	now := time.Now().UTC()
	profileID := "profile-id"
	grantExpiresAt := now.Add(time.Hour)
	transactionStarted := false
	service := &Service{
		now: func() time.Time { return now },
		profileTxFactory: func(context.Context, auth.Principal) (playbackProfileTransaction, error) {
			transactionStarted = true
			return nil, errors.New("unexpected transaction")
		},
	}
	_, err = service.createSession(context.Background(), auth.Principal{
		SessionID: "auth-session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
	}, sourceReference{MediaType: "movie", ResourceID: "resource-id"}, "", nil, nil, []storedAsset{{
		ID: "oversized", Kind: "stream", URL: strings.Repeat("x", maximumPlaybackSessionAssetBytes),
	}}, nil, nil, nil)
	if !errors.Is(err, ErrMediaCapacityReached) {
		t.Fatalf("oversized playback asset error = %v, want ErrMediaCapacityReached", err)
	}
	if transactionStarted {
		t.Fatal("oversized playback asset reached the PostgreSQL transaction boundary")
	}
}

func TestPlaybackSessionAdmissionChecksEveryCapacityAndCommitsCleanupBeforeStoppingHLS(t *testing.T) {
	tests := []struct {
		name   string
		counts playbackSessionCountsRow
	}{
		{name: "auth session", counts: playbackSessionCountsRow{authSession: maximumPlaybackSessionsPerAuthSession}},
		{name: "profile", counts: playbackSessionCountsRow{profile: maximumPlaybackSessionsPerProfile}},
		{name: "global", counts: playbackSessionCountsRow{global: maximumPlaybackSessionsGlobal}},
	}
	limits := playbackSessionDefaultLimits()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now().UTC()
			profileID := "profile-id"
			grantExpiresAt := now.Add(time.Hour)
			const staleID = "stale-session"
			committedBeforeStop := false
			done := make(chan struct{})
			close(done)
			transaction := &playbackTransactionStub{
				query: func(string, ...any) (pgx.Rows, error) {
					return &singlePlaybackSessionRows{id: staleID, pending: true}, nil
				},
				queryRow: func(query string, _ ...any) pgx.Row {
					if strings.Contains(query, "count(*) FILTER") {
						return test.counts
					}
					t.Fatalf("capacity rejection reached session insert: %s", query)
					return testPlaybackProfileRow{}
				},
			}
			service := &Service{
				now: func() time.Time { return now },
				hlsJobs: map[string]*hlsJob{
					staleID + "/asset": {
						directory: t.TempDir(), done: done,
						cancel: func() { committedBeforeStop = transaction.commitCalled },
					},
				},
				mediaOptions: MediaOptions{TempDirectory: t.TempDir()},
				profileTxFactory: func(context.Context, auth.Principal) (playbackProfileTransaction, error) {
					return transaction, nil
				},
			}
			_, err := service.createSessionWithLimits(context.Background(), auth.Principal{
				SessionID: "auth-session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
			}, sourceReference{MediaType: "movie", ResourceID: "resource-id"}, "", nil, nil, nil, nil, nil, nil, limits)
			if !errors.Is(err, ErrMediaCapacityReached) {
				t.Fatalf("capacity error = %v, want ErrMediaCapacityReached", err)
			}
			if !transaction.commitCalled || !committedBeforeStop {
				t.Fatalf("cleanup commit=%t committed before HLS stop=%t", transaction.commitCalled, committedBeforeStop)
			}
		})
	}
}

type playbackAdmissionFixture struct {
	userID     string
	deviceID   string
	profileIDs []string
	authIDs    []string
}

func TestPlaybackSessionAdmissionSerializesConcurrentPostgreSQLResolves(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run PostgreSQL playback session admission tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	tests := []struct {
		name              string
		limits            playbackSessionLimits
		seedAuth          int
		seedProfile       int
		candidateAuths    [2]int
		candidateProfiles [2]int
		globalRelative    bool
	}{
		{name: "auth session", limits: playbackSessionLimits{perAuthSession: 2, perProfile: 10, global: 10_000}, seedAuth: 0, seedProfile: 0, candidateAuths: [2]int{0, 0}, candidateProfiles: [2]int{0, 0}},
		{name: "profile", limits: playbackSessionLimits{perAuthSession: 10, perProfile: 2, global: 10_000}, seedAuth: 0, seedProfile: 0, candidateAuths: [2]int{1, 2}, candidateProfiles: [2]int{0, 0}},
		{name: "global", limits: playbackSessionLimits{perAuthSession: 10, perProfile: 10}, seedAuth: 0, seedProfile: 0, candidateAuths: [2]int{1, 2}, candidateProfiles: [2]int{1, 2}, globalRelative: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := seedPlaybackAdmissionFixture(t, pool)
			baseline := activePlaybackSessionCount(t, pool)
			if test.globalRelative {
				test.limits.global = baseline + 2
			}
			seedPlaybackAdmissionSession(t, pool, fixture.authIDs[test.seedAuth], fixture.profileIDs[test.seedProfile], "seed")
			for index := range 2 {
				if _, err := pool.Exec(ctx, `
					UPDATE auth_sessions
					SET active_profile_id = $2::uuid, profile_grant_expires_at = now() + interval '1 hour'
					WHERE id = $1::uuid
				`, fixture.authIDs[test.candidateAuths[index]], fixture.profileIDs[test.candidateProfiles[index]]); err != nil {
					t.Fatalf("bind candidate auth session %d to profile: %v", index, err)
				}
			}

			now := time.Now().UTC()
			service := &Service{
				now: func() time.Time { return now }, hlsJobs: make(map[string]*hlsJob),
				profileTxFactory: func(ctx context.Context, _ auth.Principal) (playbackProfileTransaction, error) {
					return pool.Begin(ctx)
				},
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			for index := range 2 {
				index := index
				go func() {
					<-start
					profileID := fixture.profileIDs[test.candidateProfiles[index]]
					grantExpiresAt := now.Add(time.Hour)
					_, createErr := service.createSessionWithLimits(ctx, auth.Principal{
						SessionID: fixture.authIDs[test.candidateAuths[index]], ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
					}, sourceReference{MediaType: "movie", ResourceID: "resource-id"}, "", nil, nil, nil, nil, nil, nil, test.limits)
					results <- createErr
				}()
			}
			close(start)
			successes, capacityRejections := 0, 0
			for range 2 {
				switch result := <-results; {
				case result == nil:
					successes++
				case errors.Is(result, ErrMediaCapacityReached):
					capacityRejections++
				default:
					t.Fatalf("concurrent playback session creation error: %v", result)
				}
			}
			if successes != 1 || capacityRejections != 1 {
				t.Fatalf("concurrent results: successes=%d capacity rejections=%d, want 1 and 1", successes, capacityRejections)
			}
			assertPlaybackAdmissionLimits(t, pool, fixture, test.limits)
			if test.globalRelative && activePlaybackSessionCount(t, pool) != baseline+2 {
				t.Fatalf("global active playback count did not stop at limit %d", baseline+2)
			}
		})
	}
}

func seedPlaybackAdmissionFixture(t *testing.T, pool *pgxpool.Pool) playbackAdmissionFixture {
	t.Helper()
	ctx := context.Background()
	fixture := playbackAdmissionFixture{profileIDs: make([]string, 3), authIDs: make([]string, 3)}
	if err := pool.QueryRow(ctx, "SELECT gen_random_uuid()::text, gen_random_uuid()::text").Scan(&fixture.userID, &fixture.deviceID); err != nil {
		t.Fatalf("generate playback admission owner IDs: %v", err)
	}
	for index := range 3 {
		if err := pool.QueryRow(ctx, "SELECT gen_random_uuid()::text, gen_random_uuid()::text").Scan(&fixture.profileIDs[index], &fixture.authIDs[index]); err != nil {
			t.Fatalf("generate playback admission IDs: %v", err)
		}
	}
	var categoryID string
	if err := pool.QueryRow(ctx, "SELECT id::text FROM access_categories WHERE is_default").Scan(&categoryID); err != nil {
		t.Fatalf("query default category: %v", err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO users (id, username, password_hash, role) VALUES ($1::uuid, $2, 'test-hash', 'member')", fixture.userID, "playback_admission_"+fixture.userID[:12]); err != nil {
		t.Fatalf("seed playback admission user: %v", err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO devices (id, user_id, name, platform, category_id, approved_at) VALUES ($1::uuid, $2::uuid, 'Playback admission', 'test', $3::uuid, now())", fixture.deviceID, fixture.userID, categoryID); err != nil {
		t.Fatalf("seed playback admission device: %v", err)
	}
	for index := range 3 {
		if _, err := pool.Exec(ctx, "INSERT INTO profiles (id, name, category_id) VALUES ($1::uuid, $2, $3::uuid)", fixture.profileIDs[index], fmt.Sprintf("Playback admission %d", index), categoryID); err != nil {
			t.Fatalf("seed playback admission profile %d: %v", index, err)
		}
		digest := sha256.Sum256([]byte(fixture.authIDs[index]))
		if _, err := pool.Exec(ctx, `
			INSERT INTO auth_sessions (
				id, user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
				active_profile_id, profile_grant_expires_at, profile_context_hash, authorization_scope, category_id
			) VALUES (
				$1::uuid, $2::uuid, $3::uuid, $4, now() + interval '1 hour', now() + interval '2 hours',
				$5::uuid, now() + interval '1 hour', decode(repeat('a1', 32), 'hex'), 'category', $6::uuid
			)
		`, fixture.authIDs[index], fixture.userID, fixture.deviceID, digest[:], fixture.profileIDs[index], categoryID); err != nil {
			t.Fatalf("seed playback admission auth session %d: %v", index, err)
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1::uuid", fixture.userID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM devices WHERE id = $1::uuid", fixture.deviceID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM profiles WHERE id = ANY($1::uuid[])", fixture.profileIDs)
	})
	return fixture
}

func seedPlaybackAdmissionSession(t *testing.T, pool *pgxpool.Pool, authSessionID, profileID, suffix string) {
	t.Helper()
	digest := sha256.Sum256([]byte(authSessionID + ":" + profileID + ":" + suffix))
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO playback_sessions (auth_session_id, profile_id, media_type, resource_id, token_hash, assets, expires_at)
		VALUES ($1::uuid, $2::uuid, 'movie', 'resource-id', $3, '[]'::jsonb, now() + interval '1 hour')
	`, authSessionID, profileID, digest[:]); err != nil {
		t.Fatalf("seed playback admission session: %v", err)
	}
}

func activePlaybackSessionCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM playback_sessions
		WHERE expires_at > now() AND last_seen_at > now() - $1::interval
	`, intervalLiteral(playbackSessionIdleTTL)).Scan(&count); err != nil {
		t.Fatalf("count active playback sessions: %v", err)
	}
	return count
}

func assertPlaybackAdmissionLimits(t *testing.T, pool *pgxpool.Pool, fixture playbackAdmissionFixture, limits playbackSessionLimits) {
	t.Helper()
	ctx := context.Background()
	for _, authSessionID := range fixture.authIDs {
		var count int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM playback_sessions
			WHERE auth_session_id = $1::uuid AND expires_at > now() AND last_seen_at > now() - $2::interval
		`, authSessionID, intervalLiteral(playbackSessionIdleTTL)).Scan(&count); err != nil {
			t.Fatalf("count auth-session playback sessions: %v", err)
		}
		if count > limits.perAuthSession {
			t.Fatalf("auth session %s active playback count %d exceeds %d", authSessionID, count, limits.perAuthSession)
		}
	}
	for _, profileID := range fixture.profileIDs {
		var count int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM playback_sessions
			WHERE profile_id = $1::uuid AND expires_at > now() AND last_seen_at > now() - $2::interval
		`, profileID, intervalLiteral(playbackSessionIdleTTL)).Scan(&count); err != nil {
			t.Fatalf("count profile playback sessions: %v", err)
		}
		if count > limits.perProfile {
			t.Fatalf("profile %s active playback count %d exceeds %d", profileID, count, limits.perProfile)
		}
	}
	if count := activePlaybackSessionCount(t, pool); count > limits.global {
		t.Fatalf("global active playback count %d exceeds %d", count, limits.global)
	}
}
