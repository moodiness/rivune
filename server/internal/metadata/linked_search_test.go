package metadata

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
)

type blockingLinkedSearchProvider struct {
	movieEntered  chan struct{}
	seriesEntered chan struct{}
	release       chan struct{}
	externalID    string
}

func (*blockingLinkedSearchProvider) DiscoverMovies(context.Context, QueryOptions) (ProviderMoviePage, error) {
	return ProviderMoviePage{}, nil
}

func (provider *blockingLinkedSearchProvider) SearchMovies(context.Context, SearchOptions) (ProviderMoviePage, error) {
	close(provider.movieEntered)
	<-provider.release
	return ProviderMoviePage{Items: []ProviderMovie{{ExternalID: provider.externalID, Title: "Revoked movie"}}, Page: 1, TotalPages: 1, TotalResults: 1}, nil
}

func (*blockingLinkedSearchProvider) MovieDetails(context.Context, string, string) (ProviderMovie, error) {
	return ProviderMovie{}, ErrProviderNotFound
}

func (*blockingLinkedSearchProvider) DiscoverSeries(context.Context, QueryOptions) (ProviderSeriesPage, error) {
	return ProviderSeriesPage{}, nil
}

func (provider *blockingLinkedSearchProvider) SearchSeries(context.Context, SearchOptions) (ProviderSeriesPage, error) {
	close(provider.seriesEntered)
	<-provider.release
	return ProviderSeriesPage{Items: []ProviderSeries{{ExternalID: provider.externalID, Name: "Revoked series"}}, Page: 1, TotalPages: 1, TotalResults: 1}, nil
}

func (*blockingLinkedSearchProvider) SeriesDetails(context.Context, string, string) (ProviderSeries, error) {
	return ProviderSeries{}, ErrProviderNotFound
}

func (*blockingLinkedSearchProvider) SeasonDetails(context.Context, string, int, string) (ProviderSeason, error) {
	return ProviderSeason{}, ErrProviderNotFound
}

func TestLinkedSearchRejectsProviderResultAfterLogout(t *testing.T) {
	for _, mediaType := range []string{MediaTypeMovie, MediaTypeSeries} {
		t.Run(mediaType, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			t.Cleanup(cancel)
			pool, principal := linkedSearchFixture(t)
			provider := &blockingLinkedSearchProvider{
				movieEntered: make(chan struct{}), seriesEntered: make(chan struct{}), release: make(chan struct{}),
				externalID: fmt.Sprintf("linked-search-%s-%d", mediaType, time.Now().UnixNano()),
			}
			service := NewService(pool, provider, nil, nil, time.Hour, nil, time.UTC)
			result := make(chan error, 1)
			go func() {
				if mediaType == MediaTypeMovie {
					_, err := service.SearchLinkedMovies(ctx, principal, SearchOptions{QueryOptions: QueryOptions{Page: 1}, Query: "revoked"})
					result <- err
					return
				}
				_, err := service.SearchLinkedSeries(ctx, principal, SearchOptions{QueryOptions: QueryOptions{Page: 1}, Query: "revoked"})
				result <- err
			}()
			var entered <-chan struct{}
			if mediaType == MediaTypeMovie {
				entered = provider.movieEntered
			} else {
				entered = provider.seriesEntered
			}
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatalf("linked %s search did not reach provider: %v", mediaType, ctx.Err())
			}
			if _, err := pool.Exec(ctx, `
				UPDATE auth_sessions SET revoked_at = now(), revoked_reason = 'linked_search_test'
				WHERE id = $1::uuid
			`, principal.SessionID); err != nil {
				t.Fatalf("revoke linked search session: %v", err)
			}
			close(provider.release)
			select {
			case err := <-result:
				if !errors.Is(err, ErrProfileRequired) {
					t.Fatalf("search after linked logout error = %v, want %v", err, ErrProfileRequired)
				}
			case <-ctx.Done():
				t.Fatalf("linked %s search did not finish after logout: %v", mediaType, ctx.Err())
			}
			var persisted int
			if err := pool.QueryRow(ctx, `
				SELECT count(*)::int FROM title_external_ids
				WHERE provider = 'tmdb' AND namespace = $1 AND external_id = $2
			`, mediaType, provider.externalID).Scan(&persisted); err != nil {
				t.Fatalf("count post-logout search identities: %v", err)
			}
			if persisted != 0 {
				t.Fatalf("revoked linked %s search persisted %d identities", mediaType, persisted)
			}
		})
	}
}

func linkedSearchFixture(t *testing.T) (*pgxpool.Pool, auth.Principal) {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run linked metadata search authorization tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open linked metadata search database: %v", err)
	}
	t.Cleanup(pool.Close)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var userID, profileID, categoryID, deviceID, sessionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-linked-search-hash', 'member') RETURNING id::text
	`, "linked_metadata_search_"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert linked search user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name) VALUES ($1) RETURNING id::text, category_id::text
	`, "Linked metadata search "+suffix).Scan(&profileID, &categoryID); err != nil {
		t.Fatalf("insert linked search profile: %v", err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, "DELETE FROM users WHERE id = $1::uuid", userID)
		_, _ = pool.Exec(cleanup, "DELETE FROM profiles WHERE id = $1::uuid", profileID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO user_profile_access (user_id, profile_id) VALUES ($1, $2)`, userID, profileID); err != nil {
		t.Fatalf("grant linked search profile: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1, $2, 'Infuse', $3, now()) RETURNING id::text
	`, userID, "Linked metadata search device "+suffix, categoryID).Scan(&deviceID); err != nil {
		t.Fatalf("insert linked search device: %v", err)
	}
	accessHash := sha256.Sum256([]byte("linked-search-access-" + suffix))
	contextHash := sha256.Sum256([]byte("linked-search-context-" + suffix))
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id, active_profile_id, profile_grant_expires_at,
			profile_context_hash
		) VALUES (
			$1, $2, $3, now() - interval '1 minute', now() + interval '2 hours',
			'category', $4, $5, now() + interval '2 hours', $6
		) RETURNING id::text
	`, userID, deviceID, accessHash[:], categoryID, profileID, contextHash[:]).Scan(&sessionID); err != nil {
		t.Fatalf("insert linked search session: %v", err)
	}
	expiresAt := time.Now().UTC().Add(2 * time.Hour)
	return pool, auth.Principal{
		SessionID: sessionID, UserID: userID, DeviceID: deviceID,
		ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt, ProfileContextHash: contextHash[:],
	}
}
