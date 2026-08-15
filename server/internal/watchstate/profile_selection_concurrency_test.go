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

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
)

func TestUpdateProgressRejectsCapturedProfileAfterSelectionWins(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run watchstate profile selection concurrency tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open watchstate profile selection database: %v", err)
	}
	t.Cleanup(pool.Close)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var userID, profileAID, profileBID, categoryID, deviceID, sessionID, titleID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-watchstate-selection-hash', 'member')
		RETURNING id::text
	`, "watchstate_selection_"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert watchstate selection user: %v", err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		if sessionID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM auth_sessions WHERE id = $1::uuid`, sessionID)
		}
		if deviceID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM devices WHERE id = $1::uuid`, deviceID)
		}
		if userID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE id = $1::uuid`, userID)
		}
		if profileAID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM profiles WHERE id = $1::uuid`, profileAID)
		}
		if profileBID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM profiles WHERE id = $1::uuid`, profileBID)
		}
		if titleID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM titles WHERE id = $1::uuid`, titleID)
		}
	})

	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name) VALUES ($1)
		RETURNING id::text, category_id::text
	`, "Watchstate selection A "+suffix).Scan(&profileAID, &categoryID); err != nil {
		t.Fatalf("insert watchstate selection profile A: %v", err)
	}
	var profileBCategoryID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name) VALUES ($1)
		RETURNING id::text, category_id::text
	`, "Watchstate selection B "+suffix).Scan(&profileBID, &profileBCategoryID); err != nil {
		t.Fatalf("insert watchstate selection profile B: %v", err)
	}
	if profileBCategoryID != categoryID {
		t.Fatalf("watchstate selection profile categories differ: A=%s B=%s", categoryID, profileBCategoryID)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id)
		VALUES ($1::uuid, $2::uuid), ($1::uuid, $3::uuid)
	`, userID, profileAID, profileBID); err != nil {
		t.Fatalf("grant watchstate selection profiles: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1::uuid, $2, 'selection-race-test', $3::uuid, now())
		RETURNING id::text
	`, userID, "Watchstate selection device "+suffix, categoryID).Scan(&deviceID); err != nil {
		t.Fatalf("insert watchstate selection device: %v", err)
	}

	accessHash := sha256.Sum256([]byte("watchstate-selection-access-" + suffix))
	contextAHash := sha256.Sum256([]byte("watchstate-selection-context-a-" + suffix))
	contextBHash := sha256.Sum256([]byte("watchstate-selection-context-b-" + suffix))
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id, active_profile_id, profile_grant_expires_at,
			profile_context_hash
		) VALUES (
			$1::uuid, $2::uuid, $3, now() + interval '2 hours', now() + interval '4 hours',
			'category', $4::uuid, $5::uuid, now() + interval '2 hours', $6
		) RETURNING id::text
	`, userID, deviceID, accessHash[:], categoryID, profileAID, contextAHash[:]).Scan(&sessionID); err != nil {
		t.Fatalf("insert watchstate selection session: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO titles (media_type, display_title, resource_id, resource_provider)
		VALUES ('movie', $1, $2, 'tmdb')
		RETURNING id::text
	`, "Watchstate selection movie "+suffix, "watchstate-selection-"+suffix).Scan(&titleID); err != nil {
		t.Fatalf("insert watchstate selection title: %v", err)
	}

	grantExpiry := time.Now().UTC().Add(2 * time.Hour)
	capturedA := auth.Principal{
		SessionID: sessionID, UserID: userID, DeviceID: deviceID, Role: "member",
		AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID,
		ActiveProfileID: &profileAID, ProfileGrantExpiresAt: &grantExpiry,
		ProfileContextHash: contextAHash[:],
	}
	service := NewService(pool, time.UTC)

	tests := []struct {
		name   string
		mutate func(context.Context, pgx.Tx) error
	}{
		{
			name: "profile B selection wins",
			mutate: func(ctx context.Context, tx pgx.Tx) error {
				var mutatedSessionID string
				return tx.QueryRow(ctx, `
					UPDATE auth_sessions
					SET active_profile_id = $2::uuid,
					    profile_grant_expires_at = now() + interval '2 hours',
					    profile_context_hash = $3
					WHERE id = $1::uuid
					RETURNING id::text
				`, sessionID, profileBID, contextBHash[:]).Scan(&mutatedSessionID)
			},
		},
		{
			name: "selection clear wins",
			mutate: func(ctx context.Context, tx pgx.Tx) error {
				var mutatedSessionID string
				return tx.QueryRow(ctx, `
					UPDATE auth_sessions
					SET active_profile_id = NULL,
					    profile_grant_expires_at = NULL,
					    profile_context_hash = NULL
					WHERE id = $1::uuid
					RETURNING id::text
				`, sessionID).Scan(&mutatedSessionID)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `
				WITH reset_session AS (
					UPDATE auth_sessions
					SET active_profile_id = $2::uuid,
					    profile_grant_expires_at = now() + interval '2 hours',
					    profile_context_hash = $3
					WHERE id = $1::uuid
					RETURNING id
				)
				DELETE FROM profile_progress
				WHERE title_id = $4::uuid
				  AND profile_id = ANY($5::uuid[])
				  AND EXISTS (SELECT 1 FROM reset_session)
			`, sessionID, profileAID, contextAHash[:], titleID, []string{profileAID, profileBID}); err != nil {
				t.Fatalf("reset watchstate selection fixture: %v", err)
			}

			blocker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin profile A blocker: %v", err)
			}
			defer func() { _ = blocker.Rollback(context.Background()) }()
			var lockedProfileID string
			if err := blocker.QueryRow(ctx, `
				SELECT id::text FROM profiles WHERE id = $1::uuid FOR UPDATE
			`, profileAID).Scan(&lockedProfileID); err != nil {
				t.Fatalf("lock profile A: %v", err)
			}

			started := make(chan struct{})
			updateDone := make(chan error, 1)
			go func() {
				close(started)
				_, updateErr := service.UpdateProgress(ctx, capturedA, titleID, UpdateProgressInput{
					PositionSeconds: 30, DurationSeconds: 120,
				})
				updateDone <- updateErr
			}()
			<-started
			select {
			case updateErr := <-updateDone:
				t.Fatalf("captured profile A update bypassed profile lock: %v", updateErr)
			case <-time.After(100 * time.Millisecond):
			}

			selectionTx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin authoritative selection mutation: %v", err)
			}
			if err := test.mutate(ctx, selectionTx); err != nil {
				_ = selectionTx.Rollback(ctx)
				t.Fatalf("mutate authoritative selection: %v", err)
			}
			if err := selectionTx.Commit(ctx); err != nil {
				t.Fatalf("commit authoritative selection mutation: %v", err)
			}
			if err := blocker.Commit(ctx); err != nil {
				t.Fatalf("release profile A blocker: %v", err)
			}

			updateErr := <-updateDone
			if !errors.Is(updateErr, ErrProfileRequired) {
				t.Fatalf("captured profile A update error = %v, want %v", updateErr, ErrProfileRequired)
			}
			var progressCount int
			if err := pool.QueryRow(ctx, `
				SELECT count(*)::int
				FROM profile_progress
				WHERE title_id = $1::uuid AND profile_id = ANY($2::uuid[])
			`, titleID, []string{profileAID, profileBID}).Scan(&progressCount); err != nil {
				t.Fatalf("count stale watchstate progress: %v", err)
			}
			if progressCount != 0 {
				t.Fatalf("captured profile A update left %d progress rows for profiles A or B, want 0", progressCount)
			}
		})
	}
}
