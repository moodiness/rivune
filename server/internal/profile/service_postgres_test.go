package profile

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

type profileInvariantFixture struct {
	ctx         context.Context
	pool        *pgxpool.Pool
	categoryIDs [2]string
	nameSuffix  string
}

type storedProfileMutationState struct {
	categoryID string
	enabled    bool
	updatedAt  time.Time
	version    string
}

func newProfileInvariantFixture(t *testing.T) profileInvariantFixture {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run profile invariant tests")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	position := int(time.Now().UnixNano()%500_000_000) + 1_000_000_000
	fixture := profileInvariantFixture{ctx: ctx, pool: pool, nameSuffix: suffix}
	for index := range fixture.categoryIDs {
		if err := pool.QueryRow(ctx, `
			INSERT INTO access_categories (name, normalized_name, position)
			VALUES ($1, $2, $3)
			RETURNING id::text
		`, "Profile invariant "+suffix+" "+strconv.Itoa(index), "profile-invariant-"+suffix+"-"+strconv.Itoa(index), position+index).Scan(&fixture.categoryIDs[index]); err != nil {
			t.Fatalf("create profile invariant category %d: %v", index, err)
		}
	}
	t.Cleanup(func() {
		cleanupContext := context.Background()
		_, _ = pool.Exec(cleanupContext, `DELETE FROM profiles WHERE category_id = ANY($1::uuid[])`, fixture.categoryIDs[:])
		_, _ = pool.Exec(cleanupContext, `DELETE FROM access_categories WHERE id = ANY($1::uuid[])`, fixture.categoryIDs[:])
	})
	return fixture
}

func (fixture profileInvariantFixture) insertProfile(t *testing.T, categoryID, label string) string {
	t.Helper()
	var profileID string
	if err := fixture.pool.QueryRow(fixture.ctx, `
		INSERT INTO profiles (name, category_id)
		VALUES ($1, $2::uuid)
		RETURNING id::text
	`, label+" "+fixture.nameSuffix, categoryID).Scan(&profileID); err != nil {
		t.Fatalf("create profile %q: %v", label, err)
	}
	return profileID
}

func (fixture profileInvariantFixture) profileState(t *testing.T, profileID string) storedProfileMutationState {
	t.Helper()
	var state storedProfileMutationState
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT category_id::text, enabled, updated_at, xmin::text
		FROM profiles
		WHERE id = $1::uuid
	`, profileID).Scan(&state.categoryID, &state.enabled, &state.updatedAt, &state.version); err != nil {
		t.Fatalf("read stored profile state: %v", err)
	}
	return state
}

func assertStoredProfileStateEqual(t *testing.T, got, want storedProfileMutationState) {
	t.Helper()
	if got.categoryID != want.categoryID || got.enabled != want.enabled || !got.updatedAt.Equal(want.updatedAt) || got.version != want.version {
		t.Fatalf("stored profile state changed after refusal: got %+v, want %+v", got, want)
	}
}

func globalProfileAdministrator() auth.Principal {
	return auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator}
}

func TestUpdateProtectsUnrestrictedProfilePerCategoryAndRollsBack(t *testing.T) {
	fixture := newProfileInvariantFixture(t)
	service := NewService(fixture.pool, time.Hour, "UTC")
	targetID := fixture.insertProfile(t, fixture.categoryIDs[0], "Target")
	fixture.insertProfile(t, fixture.categoryIDs[1], "Other category")
	original := fixture.profileState(t, targetID)
	disabled := false

	if _, err := service.Update(fixture.ctx, globalProfileAdministrator(), targetID, UpdateInput{Enabled: &disabled}); !errors.Is(err, ErrLastUnrestrictedProfile) {
		t.Fatalf("disable final unrestricted category profile error = %v, want %v", err, ErrLastUnrestrictedProfile)
	}
	assertStoredProfileStateEqual(t, fixture.profileState(t, targetID), original)

	destinationCategoryID := fixture.categoryIDs[1]
	if _, err := service.Update(fixture.ctx, globalProfileAdministrator(), targetID, UpdateInput{CategoryID: &destinationCategoryID}); !errors.Is(err, ErrLastUnrestrictedProfile) {
		t.Fatalf("move final unrestricted category profile error = %v, want %v", err, ErrLastUnrestrictedProfile)
	}
	assertStoredProfileStateEqual(t, fixture.profileState(t, targetID), original)

	fixture.insertProfile(t, fixture.categoryIDs[0], "Same category")
	updated, err := service.Update(fixture.ctx, globalProfileAdministrator(), targetID, UpdateInput{Enabled: &disabled})
	if err != nil {
		t.Fatalf("disable profile with same-category unrestricted peer: %v", err)
	}
	if updated.Enabled {
		t.Fatal("accepted profile update did not disable the target")
	}
	stored := fixture.profileState(t, targetID)
	if stored.enabled {
		t.Fatal("accepted profile update was not persisted")
	}
}

func TestConcurrentUpdatesCannotRemoveEveryUnrestrictedCategoryProfile(t *testing.T) {
	fixture := newProfileInvariantFixture(t)
	service := NewService(fixture.pool, time.Hour, "UTC")
	profileIDs := [2]string{
		fixture.insertProfile(t, fixture.categoryIDs[0], "Concurrent A"),
		fixture.insertProfile(t, fixture.categoryIDs[0], "Concurrent B"),
	}
	original := map[string]storedProfileMutationState{
		profileIDs[0]: fixture.profileState(t, profileIDs[0]),
		profileIDs[1]: fixture.profileState(t, profileIDs[1]),
	}

	type updateResult struct {
		profileID string
		err       error
	}
	start := make(chan struct{})
	results := make(chan updateResult, len(profileIDs))
	var ready sync.WaitGroup
	ready.Add(len(profileIDs))
	for _, profileID := range profileIDs {
		go func(profileID string) {
			ready.Done()
			<-start
			disabled := false
			_, err := service.Update(fixture.ctx, globalProfileAdministrator(), profileID, UpdateInput{Enabled: &disabled})
			results <- updateResult{profileID: profileID, err: err}
		}(profileID)
	}
	ready.Wait()
	close(start)

	succeeded := 0
	refused := 0
	for range profileIDs {
		result := <-results
		stored := fixture.profileState(t, result.profileID)
		switch {
		case result.err == nil:
			succeeded++
			if stored.enabled {
				t.Fatalf("successful concurrent update left profile %s enabled", result.profileID)
			}
			if stored.version == original[result.profileID].version {
				t.Fatalf("successful concurrent update did not replace profile row version %s", result.profileID)
			}
		case errors.Is(result.err, ErrLastUnrestrictedProfile):
			refused++
			assertStoredProfileStateEqual(t, stored, original[result.profileID])
		default:
			t.Fatalf("concurrent profile update %s failed unexpectedly: %v", result.profileID, result.err)
		}
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("concurrent update results: succeeded=%d refused=%d, want one of each", succeeded, refused)
	}

	var unrestrictedCount int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*)
		FROM profiles
		WHERE category_id = $1::uuid
		  AND enabled
		  AND available_from IS NULL AND available_until IS NULL
		  AND access_start_time IS NULL AND access_end_time IS NULL
	`, fixture.categoryIDs[0]).Scan(&unrestrictedCount); err != nil {
		t.Fatalf("count unrestricted profiles after concurrent updates: %v", err)
	}
	if unrestrictedCount != 1 {
		t.Fatalf("unrestricted profile count after concurrent updates = %d, want 1", unrestrictedCount)
	}
}
