package collection

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
)

func TestListRejectsCapturedProfileAfterSelectionMutationWins(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run collection selection race tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open collection selection race database: %v", err)
	}
	t.Cleanup(pool.Close)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var userID, categoryID, profileAID, profileBID, deviceID, sessionID, collectionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-collection-selection-race-hash', 'member')
		RETURNING id::text
	`, "collection_selection_race_"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert collection selection race user: %v", err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		if sessionID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM auth_sessions WHERE id = $1::uuid`, sessionID)
		}
		if collectionID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM profile_collections WHERE id = $1::uuid`, collectionID)
		}
		if deviceID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM devices WHERE id = $1::uuid`, deviceID)
		}
		if profileAID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM profiles WHERE id = $1::uuid`, profileAID)
		}
		if profileBID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM profiles WHERE id = $1::uuid`, profileBID)
		}
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE id = $1::uuid`, userID)
	})
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name)
		VALUES ($1)
		RETURNING id::text, category_id::text
	`, "Collection selection profile A "+suffix).Scan(&profileAID, &categoryID); err != nil {
		t.Fatalf("insert collection selection profile A: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (category_id, name)
		VALUES ($1::uuid, $2)
		RETURNING id::text
	`, categoryID, "Collection selection profile B "+suffix).Scan(&profileBID); err != nil {
		t.Fatalf("insert collection selection profile B: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($1::uuid, $2::uuid, false), ($1::uuid, $3::uuid, false)
	`, userID, profileAID, profileBID); err != nil {
		t.Fatalf("grant collection selection profiles: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1::uuid, $2, 'test', $3::uuid, now())
		RETURNING id::text
	`, userID, "Collection selection device "+suffix, categoryID).Scan(&deviceID); err != nil {
		t.Fatalf("insert collection selection device: %v", err)
	}
	accessHash := sha256.Sum256([]byte("collection-selection-access-" + suffix))
	contextAHash := sha256.Sum256([]byte("collection-selection-context-a-" + suffix))
	contextBHash := sha256.Sum256([]byte("collection-selection-context-b-" + suffix))
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id, active_profile_id, profile_grant_expires_at,
			profile_context_hash
		) VALUES (
			$1::uuid, $2::uuid, $3, now() + interval '1 hour', now() + interval '2 hours',
			'category', $4::uuid, $5::uuid, now() + interval '2 hours', $6
		)
		RETURNING id::text
	`, userID, deviceID, accessHash[:], categoryID, profileAID, contextAHash[:]).Scan(&sessionID); err != nil {
		t.Fatalf("insert collection selection session: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		WITH collection AS (
			INSERT INTO profile_collections (profile_id, title, folders, position)
			VALUES ($1::uuid, $2, '[]'::jsonb, 0)
			RETURNING id
		), access AS (
			INSERT INTO collection_profile_access (collection_id, profile_id, position)
			SELECT id, $1::uuid, 0 FROM collection
			RETURNING collection_id
		)
		SELECT collection_id::text FROM access
	`, profileAID, "Profile A only "+suffix).Scan(&collectionID); err != nil {
		t.Fatalf("insert profile A collection: %v", err)
	}

	grantExpiresAt := time.Now().UTC().Add(2 * time.Hour)
	principal := auth.Principal{
		SessionID: sessionID, UserID: userID, DeviceID: deviceID, Role: "member",
		AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID,
		ActiveProfileID: &profileAID, ProfileGrantExpiresAt: &grantExpiresAt,
		ProfileContextHash: contextAHash[:],
	}
	service := NewService(pool, nil, nil, nil, nil)
	type listResult struct {
		collections []Collection
		err         error
	}

	cases := []struct {
		name   string
		mutate func(context.Context) error
	}{
		{
			name: "profile B selection",
			mutate: func(ctx context.Context) error {
				_, err := pool.Exec(ctx, `
					UPDATE auth_sessions
					SET active_profile_id = $2::uuid,
					    profile_grant_expires_at = now() + interval '2 hours',
					    profile_context_hash = $3
					WHERE id = $1::uuid
				`, sessionID, profileBID, contextBHash[:])
				return err
			},
		},
		{
			name: "selection clear",
			mutate: func(ctx context.Context) error {
				_, err := pool.Exec(ctx, `
					UPDATE auth_sessions
					SET active_profile_id = NULL,
					    profile_grant_expires_at = NULL,
					    profile_context_hash = NULL
					WHERE id = $1::uuid
				`, sessionID)
				return err
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `
				UPDATE auth_sessions
				SET active_profile_id = $2::uuid,
				    profile_grant_expires_at = now() + interval '2 hours',
				    profile_context_hash = $3,
				    revoked_at = NULL,
				    revoked_reason = NULL
				WHERE id = $1::uuid
			`, sessionID, profileAID, contextAHash[:]); err != nil {
				t.Fatalf("reset authoritative profile A selection: %v", err)
			}

			blocker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin profile A blocker: %v", err)
			}
			var blockerPID int
			var lockedProfileID string
			if err := blocker.QueryRow(ctx, `
				SELECT pg_backend_pid(), id::text
				FROM profiles
				WHERE id = $1::uuid
				FOR UPDATE
			`, profileAID).Scan(&blockerPID, &lockedProfileID); err != nil {
				_ = blocker.Rollback(context.Background())
				t.Fatalf("lock profile A: %v", err)
			}

			result := make(chan listResult, 1)
			go func() {
				collections, err := service.List(ctx, principal)
				result <- listResult{collections: collections, err: err}
			}()

			blocked := false
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if err := pool.QueryRow(ctx, `
					SELECT EXISTS (
						SELECT 1
						FROM pg_stat_activity activity
						WHERE $1 = ANY(pg_blocking_pids(activity.pid))
					)
				`, blockerPID).Scan(&blocked); err != nil {
					_ = blocker.Rollback(context.Background())
					<-result
					t.Fatalf("inspect blocked collection read: %v", err)
				}
				if blocked {
					break
				}
				select {
				case early := <-result:
					_ = blocker.Rollback(context.Background())
					t.Fatalf("collection read completed before profile blocker released: collections=%+v error=%v", early.collections, early.err)
				case <-time.After(10 * time.Millisecond):
				}
			}
			if !blocked {
				_ = blocker.Rollback(context.Background())
				early := <-result
				t.Fatalf("collection read did not block on profile A authorization: collections=%+v error=%v", early.collections, early.err)
			}
			if err := test.mutate(ctx); err != nil {
				_ = blocker.Rollback(context.Background())
				<-result
				t.Fatalf("commit winning %s: %v", test.name, err)
			}
			if err := blocker.Commit(ctx); err != nil {
				t.Fatalf("release profile A blocker: %v", err)
			}

			listed := <-result
			if !errors.Is(listed.err, ErrActiveProfileRequired) {
				t.Fatalf("List() after winning %s error = %v, want %v", test.name, listed.err, ErrActiveProfileRequired)
			}
			if len(listed.collections) != 0 {
				t.Fatalf("List() returned stale profile A collections after winning %s: %+v", test.name, listed.collections)
			}
		})
	}
}
