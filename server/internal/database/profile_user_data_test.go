package database

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestProfileUserDataMigrationEnforcesNormalizedNullableState(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run profile user data migration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open profile user data migration database: %v", err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin profile user data migration verification: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var profileID, titleID string
	if err := tx.QueryRow(ctx, `INSERT INTO profiles (name) VALUES ($1) RETURNING id::text`, "User data migration "+suffix).Scan(&profileID); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO titles (media_type, display_title) VALUES ('movie', $1) RETURNING id::text`, "User data migration "+suffix).Scan(&titleID); err != nil {
		t.Fatalf("insert title: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO profile_user_data (
			profile_id, title_id, rating, rating_set, played_percentage, played_percentage_set,
			unplayed_item_count, unplayed_item_count_set, play_count, play_count_set,
			likes, likes_set, last_played_date, last_played_date_submicrosecond, last_played_date_set
		) VALUES ($1::uuid, $2::uuid, NULL, true, 42.5, true, 3, true, 0, true, false, true,
		          '2026-08-09T01:22:23.123456Z'::timestamptz, 700, true)
	`, profileID, titleID); err != nil {
		t.Fatalf("insert normalized nullable user data: %v", err)
	}

	invalidUpdates := []string{
		`UPDATE profile_user_data SET rating = 11, rating_set = true WHERE profile_id = $1::uuid AND title_id = $2::uuid`,
		`UPDATE profile_user_data SET play_count = NULL, play_count_set = true WHERE profile_id = $1::uuid AND title_id = $2::uuid`,
		`UPDATE profile_user_data SET last_played_date_submicrosecond = 1000 WHERE profile_id = $1::uuid AND title_id = $2::uuid`,
		`UPDATE profile_user_data SET last_played_date = NULL, last_played_date_submicrosecond = 1, last_played_date_set = true WHERE profile_id = $1::uuid AND title_id = $2::uuid`,
		`UPDATE profile_user_data SET last_played_date = 'infinity'::timestamptz WHERE profile_id = $1::uuid AND title_id = $2::uuid`,
	}
	for index, statement := range invalidUpdates {
		if _, err := tx.Exec(ctx, "SAVEPOINT invalid_user_data"); err != nil {
			t.Fatalf("create invalid update savepoint %d: %v", index, err)
		}
		if _, err := tx.Exec(ctx, statement, profileID, titleID); err == nil {
			t.Fatalf("invalid normalized user data update %d was accepted", index)
		}
		if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT invalid_user_data"); err != nil {
			t.Fatalf("roll back invalid update %d: %v", index, err)
		}
		if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT invalid_user_data"); err != nil {
			t.Fatalf("release invalid update savepoint %d: %v", index, err)
		}
	}

	var rating *float64
	var percentage float64
	var playCount int
	var lastPlayedSubmicrosecond int
	if err := tx.QueryRow(ctx, `
		SELECT rating, played_percentage, play_count, last_played_date_submicrosecond
		FROM profile_user_data WHERE profile_id = $1::uuid AND title_id = $2::uuid
	`, profileID, titleID).Scan(&rating, &percentage, &playCount, &lastPlayedSubmicrosecond); err != nil {
		t.Fatalf("read normalized user data after rejected updates: %v", err)
	}
	if rating != nil || percentage != 42.5 || playCount != 0 || lastPlayedSubmicrosecond != 700 {
		t.Fatalf("rejected update changed normalized user data: rating=%v percentage=%v playCount=%d remainder=%d", rating, percentage, playCount, lastPlayedSubmicrosecond)
	}
}
