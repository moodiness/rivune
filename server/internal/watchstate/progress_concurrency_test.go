package watchstate

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
)

type firstWriteResult struct {
	writer   string
	progress *Progress
	err      error
}

func TestProgressFirstWritersSerializeAcrossNormalAndLinkedMutations(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run progress first-write concurrency tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open progress concurrency database: %v", err)
	}
	t.Cleanup(pool.Close)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var userID, profileID, categoryID, deviceID, sessionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-progress-concurrency-hash', 'member')
		RETURNING id::text
	`, "progress_concurrency_"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert progress concurrency user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name) VALUES ($1)
		RETURNING id::text, category_id::text
	`, "Progress concurrency "+suffix).Scan(&profileID, &categoryID); err != nil {
		t.Fatalf("insert progress concurrency profile: %v", err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE id = $1::uuid`, userID)
		_, _ = pool.Exec(cleanup, `DELETE FROM profiles WHERE id = $1::uuid`, profileID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id) VALUES ($1::uuid, $2::uuid)
	`, userID, profileID); err != nil {
		t.Fatalf("grant progress concurrency profile: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1::uuid, $2, 'concurrency-test', $3::uuid, now())
		RETURNING id::text
	`, userID, "Progress concurrency device "+suffix, categoryID).Scan(&deviceID); err != nil {
		t.Fatalf("insert progress concurrency device: %v", err)
	}
	accessHash := sha256.Sum256([]byte("progress-concurrency-access-" + suffix))
	contextHash := sha256.Sum256([]byte("progress-concurrency-context-" + suffix))
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id, active_profile_id, profile_grant_expires_at,
			profile_context_hash
		) VALUES (
			$1::uuid, $2::uuid, $3, now() + interval '2 hours', now() + interval '4 hours',
			'category', $4::uuid, $5::uuid, now() + interval '2 hours', $6
		) RETURNING id::text
	`, userID, deviceID, accessHash[:], categoryID, profileID, contextHash[:]).Scan(&sessionID); err != nil {
		t.Fatalf("insert progress concurrency session: %v", err)
	}
	grantExpiry := time.Now().UTC().Add(2 * time.Hour)
	principal := auth.Principal{
		SessionID: sessionID, UserID: userID, DeviceID: deviceID, Role: "member",
		AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID,
		ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiry,
		ProfileContextHash: contextHash[:],
	}
	service := NewService(pool, time.UTC)

	t.Run("normal update and watched writers", func(t *testing.T) {
		titleID := insertConcurrencyTitle(ctx, t, pool, suffix+"-normal")
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM titles WHERE id = $1::uuid`, titleID)
		})
		blocker := holdUserDataMutationLock(ctx, t, pool, profileID, titleID)
		defer func() { _ = blocker.Rollback(context.Background()) }()

		started := make(chan struct{}, 2)
		results := make(chan firstWriteResult, 2)
		go func() {
			started <- struct{}{}
			progress, updateErr := service.UpdateProgress(ctx, principal, titleID, UpdateProgressInput{
				PositionSeconds: 30, DurationSeconds: 120,
			})
			results <- firstWriteResult{writer: "progress", progress: &progress, err: updateErr}
		}()
		go func() {
			started <- struct{}{}
			progress, updateErr := service.SetWatched(ctx, principal, titleID, true, CompletionInput{})
			results <- firstWriteResult{writer: "watched", progress: &progress, err: updateErr}
		}()
		<-started
		<-started
		assertFirstWritersBlocked(t, results)
		if err := blocker.Commit(ctx); err != nil {
			t.Fatalf("release normal first writers: %v", err)
		}

		first, second := <-results, <-results
		successes, conflicts := 0, 0
		var winner firstWriteResult
		for _, result := range []firstWriteResult{first, second} {
			switch {
			case result.err == nil:
				successes++
				winner = result
			case errors.Is(result.err, ErrConflict):
				conflicts++
			default:
				t.Fatalf("normal %s first write error = %v", result.writer, result.err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("normal first writes successes=%d conflicts=%d: %+v %+v", successes, conflicts, first, second)
		}
		assertPersistedFirstWrite(t, ctx, pool, profileID, titleID, winner)
	})

	t.Run("linked user data and playback writers", func(t *testing.T) {
		titleID := insertConcurrencyTitle(ctx, t, pool, suffix+"-linked")
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM titles WHERE id = $1::uuid`, titleID)
		})
		blocker := holdUserDataMutationLock(ctx, t, pool, profileID, titleID)
		defer func() { _ = blocker.Rollback(context.Background()) }()

		started := make(chan struct{}, 2)
		results := make(chan firstWriteResult, 2)
		go func() {
			started <- struct{}{}
			position := 40
			state, updateErr := service.UpdateUserDataForLinkedSession(ctx, principal, titleID, UpdateUserDataInput{
				PositionSeconds: &position, DurationSeconds: 120,
			})
			results <- firstWriteResult{writer: "user-data", progress: state.Progress, err: updateErr}
		}()
		go func() {
			started <- struct{}{}
			progress, updateErr := service.ApplyPlaybackEventForLinkedSession(ctx, principal, titleID, UpdateProgressInput{
				PositionSeconds: 60, DurationSeconds: 120,
			})
			results <- firstWriteResult{writer: "playback", progress: &progress, err: updateErr}
		}()
		<-started
		<-started
		assertFirstWritersBlocked(t, results)
		if err := blocker.Commit(ctx); err != nil {
			t.Fatalf("release linked first writers: %v", err)
		}

		first, second := <-results, <-results
		var userData, playback firstWriteResult
		for _, result := range []firstWriteResult{first, second} {
			if result.writer == "user-data" {
				userData = result
			} else {
				playback = result
			}
		}
		if userData.err != nil || userData.progress == nil {
			t.Fatalf("linked user-data first write = %+v", userData)
		}
		if playback.err != nil && !errors.Is(playback.err, ErrConflict) {
			t.Fatalf("linked playback first write error = %v", playback.err)
		}
		var position int
		var completed bool
		var version int64
		if err := pool.QueryRow(ctx, `
			SELECT position_seconds, completed, version
			FROM profile_progress
			WHERE profile_id = $1::uuid AND title_id = $2::uuid
		`, profileID, titleID).Scan(&position, &completed, &version); err != nil {
			t.Fatalf("read linked first-write state: %v", err)
		}
		if position != 40 || completed {
			t.Fatalf("linked first-write state position=%d completed=%t version=%d", position, completed, version)
		}
		if errors.Is(playback.err, ErrConflict) && version != 1 {
			t.Fatalf("conflicted linked playback left version %d, want 1", version)
		}
		if playback.err == nil && version != 2 {
			t.Fatalf("serialized linked writes left version %d, want 2", version)
		}
	})
}

func insertConcurrencyTitle(ctx context.Context, t *testing.T, pool *pgxpool.Pool, suffix string) string {
	t.Helper()
	var titleID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO titles (media_type, display_title, resource_id, resource_provider)
		VALUES ('movie', $1, $2, 'tmdb')
		RETURNING id::text
	`, "Concurrency movie "+suffix, "concurrency-"+suffix).Scan(&titleID); err != nil {
		t.Fatalf("insert concurrency title: %v", err)
	}
	return titleID
}

func holdUserDataMutationLock(ctx context.Context, t *testing.T, pool *pgxpool.Pool, profileID, titleID string) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin first-write blocker: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "user-data:"+profileID+":"+titleID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("lock first-write blocker: %v", err)
	}
	return tx
}

func assertFirstWritersBlocked(t *testing.T, results <-chan firstWriteResult) {
	t.Helper()
	select {
	case result := <-results:
		t.Fatalf("%s first writer bypassed shared advisory lock: %v", result.writer, result.err)
	case <-time.After(100 * time.Millisecond):
	}
}

func assertPersistedFirstWrite(t *testing.T, ctx context.Context, pool *pgxpool.Pool, profileID, titleID string, winner firstWriteResult) {
	t.Helper()
	var position, duration int
	var completed bool
	var version int64
	if err := pool.QueryRow(ctx, `
		SELECT position_seconds, duration_seconds, completed, version
		FROM profile_progress
		WHERE profile_id = $1::uuid AND title_id = $2::uuid
	`, profileID, titleID).Scan(&position, &duration, &completed, &version); err != nil {
		t.Fatalf("read normal first-write state: %v", err)
	}
	if version != 1 {
		t.Fatalf("normal first-write version = %d, want 1", version)
	}
	switch winner.writer {
	case "progress":
		if position != 30 || duration != 120 || completed {
			t.Fatalf("persisted progress winner position=%d duration=%d completed=%t", position, duration, completed)
		}
	case "watched":
		if position != 0 || duration != 0 || !completed {
			t.Fatalf("persisted watched winner position=%d duration=%d completed=%t", position, duration, completed)
		}
	default:
		t.Fatalf("unknown normal first-write winner %q", winner.writer)
	}
}
