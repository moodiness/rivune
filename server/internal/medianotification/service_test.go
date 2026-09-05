package medianotification

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/database"

	"github.com/moodiness/rivune/server/internal/auth"
)

func TestNotificationKindsAndStableKeys(t *testing.T) {
	season, episode := 4, 7
	tests := []struct {
		kind Kind
		season *int
		episode *int
		want string
	}{
		{KindCalendarEventUpcoming, nil, nil, "calendar-event-upcoming:root"},
		{KindSeasonAvailable, &season, nil, "season-available:root:4"},
		{KindEpisodeAvailable, &season, &episode, "episode-available:root:4:7"},
		{KindMovieRelease, nil, nil, "movie-release:root"},
	}
	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			if got := notificationKey(test.kind, "root", test.season, test.episode); got != test.want {
				t.Fatalf("notification key = %q, want %q", got, test.want)
			}
			if again := notificationKey(test.kind, "root", test.season, test.episode); again != test.want {
				t.Fatalf("retry notification key = %q, want stable %q", again, test.want)
			}
		})
	}
}

func TestReleaseHorizonBoundariesAreInclusive(t *testing.T) {
	today := time.Date(2026, 3, 8, 8, 0, 0, 0, time.UTC)
	if !releaseWithinHorizon(today, today, 30) { t.Fatal("today was excluded from horizon") }
	if !releaseWithinHorizon(today.AddDate(0, 0, 30), today, 30) { t.Fatal("last horizon day was excluded") }
	if releaseWithinHorizon(today.AddDate(0, 0, 31), today, 30) { t.Fatal("day after horizon was included") }
	if releaseWithinHorizon(today.AddDate(0, 0, -1), today, 30) { t.Fatal("past date was included") }
}

func TestDatabaseDateAtHonorsTimezoneAndDST(t *testing.T) {
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	if err != nil { t.Fatal(err) }
	databaseDate := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)
	got := databaseDateAt(databaseDate, losAngeles)
	want := time.Date(2026, 3, 8, 8, 0, 0, 0, time.UTC)
	if !got.Equal(want) { t.Fatalf("local release boundary = %v, want %v", got, want) }
	following := databaseDateAt(databaseDate.AddDate(0, 0, 1), losAngeles)
	if following.Sub(got) != 23*time.Hour { t.Fatalf("DST boundary = %s, want 23h", following.Sub(got)) }
}

func TestExpiryUsesRetentionAfterFutureAvailability(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	future := now.AddDate(0, 0, 300)
	if got, want := expiryAt(now, future), future.Add(Retention); !got.Equal(want) {
		t.Fatalf("future expiry = %v, want %v", got, want)
	}
	past := now.Add(-time.Hour)
	if got, want := expiryAt(now, past), now.Add(Retention); !got.Equal(want) {
		t.Fatalf("past expiry = %v, want %v", got, want)
	}
}

func TestAcknowledgementStatesAreClosed(t *testing.T) {
	for _, state := range []string{"read", "dismissed"} {
		if !validAcknowledgementState(state) { t.Fatalf("valid state %q rejected", state) }
	}
	for _, state := range []string{"", "unread", "scheduled", "deleted"} {
		if validAcknowledgementState(state) { t.Fatalf("invalid state %q accepted", state) }
	}
}

func TestActiveProfileIsProfileScopedAndExpiryBound(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	profileA, profileB := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	expires := now.Add(time.Minute)
	for _, profileID := range []string{profileA, profileB} {
		got, err := activeProfile(auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expires}, now)
		if err != nil || got != profileID { t.Fatalf("active profile = %q, %v, want %q", got, err, profileID) }
	}
	expired := now
	if _, err := activeProfile(auth.Principal{ActiveProfileID: &profileA, ProfileGrantExpiresAt: &expired}, now); err != ErrActiveProfileRequired {
		t.Fatalf("expired profile error = %v, want %v", err, ErrActiveProfileRequired)
	}
}

func TestNotificationGenerationPaginatesPast4096AndRetryDeduplicates(t *testing.T) {
	const count = 4097
	roots := make([]notificationRoot, count)
	for index := range roots {
		roots[index] = notificationRoot{
			profileID: fmt.Sprintf("%08d-0000-4000-8000-000000000000", index/17),
			titleID:   fmt.Sprintf("%08d-0000-4000-8000-000000000000", index),
		}
	}
	sort.Slice(roots, func(left, right int) bool {
		if roots[left].profileID != roots[right].profileID { return roots[left].profileID < roots[right].profileID }
		return roots[left].titleID < roots[right].titleID
	})
	pageCalls := 0
	fetch := func(ctx context.Context, afterProfileID, afterTitleID string, limit int) ([]notificationRoot, error) {
		if err := ctx.Err(); err != nil { return nil, err }
		pageCalls++
		start := sort.Search(len(roots), func(index int) bool {
			return roots[index].profileID > afterProfileID || roots[index].profileID == afterProfileID && roots[index].titleID > afterTitleID
		})
		end := min(start+limit, len(roots))
		return append([]notificationRoot(nil), roots[start:end]...), nil
	}

	seen := make(map[string]struct{}, count)
	process := func(root notificationRoot) error {
		seen[root.profileID+":"+root.titleID] = struct{}{}
		return nil
	}
	if err := iterateNotificationRoots(context.Background(), generationPageSize, fetch, process); err != nil { t.Fatal(err) }
	if len(seen) != count { t.Fatalf("processed roots = %d, want %d", len(seen), count) }
	last := roots[len(roots)-1]
	if _, ok := seen[last.profileID+":"+last.titleID]; !ok { t.Fatal("4097th subscription was never processed") }
	if pageCalls <= count/generationPageSize { t.Fatalf("page calls = %d, pagination did not reach final partial page", pageCalls) }

	if err := iterateNotificationRoots(context.Background(), generationPageSize, fetch, process); err != nil { t.Fatal(err) }
	if len(seen) != count { t.Fatalf("retry created duplicate identities: %d, want %d", len(seen), count) }
}

func TestNotificationGenerationPaginationHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := iterateNotificationRoots(ctx, 2,
		func(context.Context, string, string, int) ([]notificationRoot, error) {
			return []notificationRoot{{profileID: "a", titleID: "1"}, {profileID: "a", titleID: "2"}}, nil
		},
		func(notificationRoot) error {
			calls++
			cancel()
			return nil
		},
	)
	if err != context.Canceled { t.Fatalf("pagination error = %v, want context.Canceled", err) }
	if calls != 1 { t.Fatalf("processed after cancellation: %d roots", calls) }
}

func TestFollowGenerationTargetsOneRootWithConstantWork(t *testing.T) {
	const otherCount = 5000
	targetProfileID := "ffffffff-0000-4000-8000-000000000001"
	targetTitleID := "ffffffff-0000-4000-8000-000000000002"
	otherRoots := make([]notificationRoot, otherCount)
	for index := range otherRoots {
		otherRoots[index] = notificationRoot{
			profileID: fmt.Sprintf("%08d-0000-4000-8000-000000000000", index),
			titleID: fmt.Sprintf("%08d-0000-4000-8000-100000000000", index),
		}
	}
	loadCalls := 0
	processed := make([]notificationRoot, 0, 1)
	err := generateOneNotificationRoot(context.Background(), targetProfileID, targetTitleID,
		func(_ context.Context, profileID, titleID string) (notificationRoot, error) {
			loadCalls++
			if profileID != targetProfileID || titleID != targetTitleID { t.Fatalf("loaded foreign root %s/%s", profileID, titleID) }
			return notificationRoot{profileID: profileID, titleID: titleID}, nil
		},
		func(root notificationRoot) error { processed = append(processed, root); return nil },
	)
	if err != nil { t.Fatal(err) }
	if loadCalls != 1 { t.Fatalf("targeted generation queries = %d, want 1 despite %d other subscriptions", loadCalls, otherCount) }
	if len(processed) != 1 || processed[0].profileID != targetProfileID || processed[0].titleID != targetTitleID {
		t.Fatalf("targeted generation processed %+v", processed)
	}
	for _, other := range otherRoots {
		if processed[0] == other { t.Fatalf("targeted generation touched foreign root %+v", other) }
	}
}

func TestGenerationTransactionsBoundWorkAcross100kRoots(t *testing.T) {
	const total = 100_000
	committed := 0
	metrics, err := runGenerationTransactions(context.Background(), func(context.Context) (generationTransactionResult, error) {
		pageCount := min(generationPageSize, total-committed)
		committed += pageCount
		return generationTransactionResult{
			done: committed == total,
			statements: 3 + pageCount*3,
			work: pageCount * (maximumSeriesSubjects + 1),
		}, nil
	})
	if err != nil { t.Fatal(err) }
	wantTransactions := (total + generationPageSize - 1) / generationPageSize
	if metrics.transactions != wantTransactions { t.Fatalf("transactions = %d, want %d", metrics.transactions, wantTransactions) }
	if metrics.maxStatements > 3+generationPageSize*3 { t.Fatalf("transaction statements unbounded: %d", metrics.maxStatements) }
	if metrics.maxWork > generationPageSize*(maximumSeriesSubjects+1) { t.Fatalf("transaction work unbounded: %d", metrics.maxWork) }
	if committed != total { t.Fatalf("committed roots = %d, want %d", committed, total) }
}

func TestGenerationCrashResumesLastCommittedCursorWithoutStarvationOrDuplicates(t *testing.T) {
	const total = 1025
	durableCursor := 0
	processed := make([]int, total)
	crashed := false
	run := func(_ context.Context) (generationTransactionResult, error) {
		start := durableCursor
		end := min(start+generationPageSize, total)
		if !crashed && start == generationPageSize*2 {
			crashed = true
			return generationTransactionResult{}, errors.New("synthetic crash before page commit")
		}
		for index := start; index < end; index++ { processed[index]++ }
		durableCursor = end
		done := end == total
		if done { durableCursor = 0 }
		return generationTransactionResult{done: done, statements: 3, work: end - start}, nil
	}
	if _, err := runGenerationTransactions(context.Background(), run); err == nil { t.Fatal("synthetic crash was not surfaced") }
	if durableCursor != generationPageSize*2 { t.Fatalf("cursor after crash = %d, want last committed %d", durableCursor, generationPageSize*2) }
	if _, err := runGenerationTransactions(context.Background(), run); err != nil { t.Fatal(err) }
	if durableCursor != 0 { t.Fatalf("cursor did not reset atomically after cycle: %d", durableCursor) }
	for index, count := range processed {
		if count != 1 { t.Fatalf("root %d processed %d times", index, count) }
	}
}

func TestCapacityRootIsRecordedAndLaterRootsContinue(t *testing.T) {
	processed := []string{"poison", "later"}
	capacityRecords := 0
	skipped, err := handleGenerationRootError(ErrCapacity, func() error { capacityRecords++; return nil })
	if err != nil || !skipped || capacityRecords != 1 { t.Fatalf("capacity handling skipped=%v records=%d error=%v", skipped, capacityRecords, err) }
	processed = processed[1:]
	if processed[0] != "later" { t.Fatalf("later root starved: %v", processed) }
	skipped, err = handleGenerationRootError(nil, func() error { t.Fatal("recorded non-capacity root"); return nil })
	if err != nil || skipped { t.Fatalf("later root handling skipped=%v error=%v", skipped, err) }
}

func TestCapacityRootRecordFailureStopsBeforeCursorAdvance(t *testing.T) {
	recordFailure := errors.New("record failure")
	skipped, err := handleGenerationRootError(ErrCapacity, func() error { return recordFailure })
	if skipped || !errors.Is(err, recordFailure) { t.Fatalf("record failure skipped=%v error=%v", skipped, err) }
}

func TestFollowSeriesKeepsVariantSubjectsOutOfBaselineCurrentUpcomingAndOverflow(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" { t.Skip("set RIVUNE_TEST_DATABASE_URL to run PostgreSQL media notification tests") }
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil { t.Fatal(err) }
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil { t.Fatalf("migrate notification integration database: %v", err) }
	const (
		profileID            = "94000000-0000-4000-8000-000000000001"
		seriesID             = "94000000-0000-4000-8000-000000000002"
		seasonID             = "94000000-0000-4000-8000-000000000003"
		episodeID            = "94000000-0000-4000-8000-000000000004"
		variantSeasonID      = "94000000-0000-4000-8000-000000000005"
		variantEpisodeID     = "94000000-0000-4000-8000-000000000006"
		directVariantID      = "94000000-0000-4000-8000-000000000007"
		newSeasonID          = "94000000-0000-4000-8000-000000000008"
		newEpisodeID         = "94000000-0000-4000-8000-000000000009"
		newVariantSeasonID   = "94000000-0000-4000-8000-000000000010"
		newVariantEpisodeID  = "94000000-0000-4000-8000-000000000011"
	)
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id = $1::uuid`, profileID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM titles WHERE id = $1::uuid`, seriesID)
	}
	cleanup()
	t.Cleanup(cleanup)
	releaseDate := time.Now().UTC().AddDate(0, 0, 5).Format(time.DateOnly)
	if _, err := pool.Exec(ctx, `
		WITH profile AS (
			INSERT INTO profiles (id, category_id, name)
			SELECT $1::uuid, id, 'Notification integration profile' FROM access_categories WHERE is_default
		), series AS (
			INSERT INTO titles (id, media_type, display_title) VALUES ($2::uuid, 'series', 'Integration Series')
		), season AS (
			INSERT INTO titles (id, media_type, parent_id, ordinal, hierarchy_variant, display_title)
			VALUES
				($3::uuid, 'season', $2::uuid, 1, '', 'Season 1'),
				($5::uuid, 'season', $2::uuid, 1, 'tvdb:2', 'Variant Season')
		), episode AS (
			INSERT INTO titles (id, media_type, parent_id, ordinal, hierarchy_variant, display_title, release_date)
			VALUES
				($4::uuid, 'episode', $3::uuid, 1, '', 'Pilot', $8::date),
				($6::uuid, 'episode', $5::uuid, 1, 'tvdb:2', 'Variant Branch Pilot', $8::date),
				($7::uuid, 'episode', $3::uuid, 1, 'tvdb:3', 'Variant Direct Pilot', $8::date)
		), overflow_variants AS (
			INSERT INTO titles (media_type, parent_id, ordinal, hierarchy_variant, display_title, release_date)
			SELECT 'episode', $3::uuid, value + 10, 'tvdb:' || (value + 10), 'Variant Overflow ' || value, $8::date
			FROM generate_series(1, $9::integer) AS value
		)
		INSERT INTO profile_library (profile_id, title_id) VALUES ($1::uuid, $2::uuid)
	`, profileID, seriesID, seasonID, episodeID, variantSeasonID, variantEpisodeID, directVariantID,
		releaseDate, maximumSeriesSubjects+1); err != nil {
		t.Fatalf("seed notification series: %v", err)
	}
	expires := time.Now().UTC().Add(time.Hour)
	principal := auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: new(profileID), ProfileGrantExpiresAt: &expires}
	horizon, lead := 30, 1
	service := NewService(pool, nil)
	if _, err := service.Follow(ctx, principal, seriesID, FollowInput{Timezone: "UTC", HorizonDays: &horizon, LeadDays: &lead}); err != nil { t.Fatalf("follow new series: %v", err) }
	var observations, subscriptions int
	var observedIDs []string
	if err := pool.QueryRow(ctx, `
		SELECT count(*), array_agg(subject_title_id::text ORDER BY subject_title_id)
		FROM profile_media_notification_observations
		WHERE profile_id = $1::uuid AND root_title_id = $2::uuid
	`, profileID, seriesID).Scan(&observations, &observedIDs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM profile_media_notification_subscriptions WHERE profile_id = $1::uuid AND title_id = $2::uuid`, profileID, seriesID).Scan(&subscriptions); err != nil {
		t.Fatal(err)
	}
	if observations != 2 || subscriptions != 1 ||
		!reflect.DeepEqual(observedIDs, []string{seasonID, episodeID}) {
		t.Fatalf("baseline observations=%d ids=%v subscriptions=%d", observations, observedIDs, subscriptions)
	}
	var variantNotificationCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM profile_media_notifications
		WHERE profile_id = $1::uuid
		  AND subject_title_id IS NOT NULL
		  AND subject_title_id NOT IN ($2::uuid, $3::uuid)
	`, profileID, seasonID, episodeID).Scan(&variantNotificationCount); err != nil {
		t.Fatal(err)
	}
	if variantNotificationCount != 0 {
		t.Fatalf("initial generation persisted %d variant notifications", variantNotificationCount)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, parent_id, ordinal, hierarchy_variant, display_title) VALUES
			($2::uuid, 'season', $1::uuid, 2, '', 'Season 2'),
			($4::uuid, 'season', $1::uuid, 2, 'tvdb:4', 'Variant Season 2');
		INSERT INTO titles (id, media_type, parent_id, ordinal, hierarchy_variant, display_title, release_date) VALUES
			($3::uuid, 'episode', $2::uuid, 1, '', 'Second Pilot', $6::date),
			($5::uuid, 'episode', $4::uuid, 1, 'tvdb:4', 'Variant Second Pilot', $6::date);
	`, pgx.QueryExecModeSimpleProtocol, seriesID, newSeasonID, newEpisodeID, newVariantSeasonID, newVariantEpisodeID, releaseDate); err != nil {
		t.Fatalf("seed notification refresh subjects: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin targeted notification generation: %v", err)
	}
	if err := service.generateOne(ctx, tx, time.Now().UTC(), profileID, seriesID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("generate canonical notification refresh: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit canonical notification refresh: %v", err)
	}
	var newCanonicalNotifications, anyVariantNotifications int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (
		         WHERE subject_title_id IN ($3::uuid, $4::uuid)
		           AND kind IN ('season-available', 'episode-available', 'calendar-event-upcoming')
		       ),
		       count(*) FILTER (
		         WHERE subject_title_id IN ($5::uuid, $6::uuid, $7::uuid, $8::uuid, $9::uuid)
		            OR subject_title_id IN (
		                 SELECT id FROM titles WHERE hierarchy_variant <> ''
		               )
		       )
		FROM profile_media_notifications
		WHERE profile_id = $1::uuid AND root_title_id = $2::uuid
	`, profileID, seriesID, newSeasonID, newEpisodeID, variantSeasonID, variantEpisodeID,
		directVariantID, newVariantSeasonID, newVariantEpisodeID).Scan(&newCanonicalNotifications, &anyVariantNotifications); err != nil {
		t.Fatal(err)
	}
	if newCanonicalNotifications != 3 || anyVariantNotifications != 0 {
		t.Fatalf("refreshed notifications canonical=%d variant=%d", newCanonicalNotifications, anyVariantNotifications)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (media_type, parent_id, ordinal, hierarchy_variant, display_title)
		SELECT 'episode', $1::uuid, value + 100, '', 'Canonical Boundary ' || value
		FROM generate_series(1, $2::integer) AS value
	`, seasonID, maximumSeriesSubjects-4); err != nil {
		t.Fatalf("seed canonical notification boundary: %v", err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin notification overflow boundary: %v", err)
	}
	overflow, err := seriesSubjectOverflow(ctx, tx, seriesID)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("check exact notification boundary: %v", err)
	}
	if overflow {
		_ = tx.Rollback(ctx)
		t.Fatal("variants counted toward exact notification subject boundary")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO titles (media_type, parent_id, ordinal, hierarchy_variant, display_title)
		VALUES ('episode', $1::uuid, $2, '', 'Canonical Boundary Overflow')
	`, seasonID, maximumSeriesSubjects+101); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("seed notification boundary overflow: %v", err)
	}
	overflow, err = seriesSubjectOverflow(ctx, tx, seriesID)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("check notification boundary overflow: %v", err)
	}
	if !overflow {
		_ = tx.Rollback(ctx)
		t.Fatal("canonical notification subject 5001 did not overflow")
	}
	_ = tx.Rollback(ctx)
}
