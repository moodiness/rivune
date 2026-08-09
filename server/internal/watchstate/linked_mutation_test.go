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
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/tracking"
)

type blockingLinkedMutationSink struct {
	entered chan struct{}
	release chan struct{}
}

func (sink *blockingLinkedMutationSink) EnqueueTx(_ context.Context, _ pgx.Tx, _, _, _ string, _ tracking.Event) error {
	close(sink.entered)
	<-sink.release
	return nil
}

type failingLinkedMutationSink struct {
	err error
}

func (sink *failingLinkedMutationSink) EnqueueTx(context.Context, pgx.Tx, string, string, string, tracking.Event) error {
	return sink.err
}

type blockingLinkedCatalogProvider struct {
	entered chan struct{}
	release chan struct{}
}

func (provider *blockingLinkedCatalogProvider) MovieDetails(_ context.Context, externalID, _ string) (metadata.ProviderMovie, error) {
	close(provider.entered)
	<-provider.release
	return metadata.ProviderMovie{ExternalID: externalID, Title: "Linked catalog movie"}, nil
}

func (*blockingLinkedCatalogProvider) SeriesDetails(context.Context, string, string) (metadata.ProviderSeries, error) {
	return metadata.ProviderSeries{}, metadata.ErrProviderNotFound
}

func TestLinkedMutationsRevalidateAfterProviderWorkAndSerializeRevocationThroughCommit(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run linked mutation authorization race test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open linked mutation database: %v", err)
	}
	t.Cleanup(pool.Close)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var userID, profileID, categoryID, deviceID, sessionID, titleID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-linked-mutation-hash', 'member')
		RETURNING id::text
	`, "linked_mutation_"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert linked mutation user: %v", err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, "DELETE FROM users WHERE id = $1::uuid", userID)
		if profileID != "" {
			_, _ = pool.Exec(cleanup, "DELETE FROM profiles WHERE id = $1::uuid", profileID)
		}
		if titleID != "" {
			_, _ = pool.Exec(cleanup, "DELETE FROM titles WHERE id = $1::uuid", titleID)
		}
	})
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name) VALUES ($1)
		RETURNING id::text, category_id::text
	`, "Linked mutation "+suffix).Scan(&profileID, &categoryID); err != nil {
		t.Fatalf("insert linked mutation profile: %v", err)
	}
	configuredLocation, err := time.LoadLocation("Pacific/Kiritimati")
	if err != nil {
		t.Fatalf("load linked mutation configured timezone: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE profiles
		SET available_from = $2::date, access_timezone = 'Pacific/Honolulu'
		WHERE id = $1::uuid
	`, profileID, time.Now().In(configuredLocation).Format(time.DateOnly)); err != nil {
		t.Fatalf("configure divergent linked mutation timezone: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id) VALUES ($1, $2)
	`, userID, profileID); err != nil {
		t.Fatalf("grant linked mutation profile: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1, $2, 'compat-test', $3, now())
		RETURNING id::text
	`, userID, "Linked mutation device "+suffix, categoryID).Scan(&deviceID); err != nil {
		t.Fatalf("insert linked mutation device: %v", err)
	}
	accessHash := sha256.Sum256([]byte("linked-mutation-access-" + suffix))
	contextHash := sha256.Sum256([]byte("linked-mutation-context-" + suffix))
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
		t.Fatalf("insert linked mutation session: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO titles (media_type, display_title, resource_id, resource_provider)
		VALUES ('movie', $1, $2, 'tmdb')
		RETURNING id::text
	`, "Linked mutation movie "+suffix, "linked-mutation-"+suffix).Scan(&titleID); err != nil {
		t.Fatalf("insert linked mutation title: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO profile_library (profile_id, title_id) VALUES ($1, $2)
	`, profileID, titleID); err != nil {
		t.Fatalf("insert linked mutation library membership: %v", err)
	}

	grantExpiry := time.Now().UTC().Add(2 * time.Hour)
	principal := auth.Principal{
		SessionID: sessionID, UserID: userID, DeviceID: deviceID, Role: "member",
		AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID,
		ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiry,
		ProfileContextHash: contextHash[:],
	}
	sink := &blockingLinkedMutationSink{entered: make(chan struct{}), release: make(chan struct{})}
	service := NewService(pool, configuredLocation, sink)
	rating, percentage := 8.25, 42.5
	unplayed, playCount, liked := 3, 7, true
	lastPlayed := time.Date(2026, 8, 9, 1, 22, 23, 123456700, time.UTC)
	userDataState, err := service.UpdateUserDataForLinkedSession(ctx, principal, titleID, UpdateUserDataInput{
		Rating:            OptionalUserDataValue[float64]{Set: true, Value: &rating},
		PlayedPercentage:  OptionalUserDataValue[float64]{Set: true, Value: &percentage},
		UnplayedItemCount: OptionalUserDataValue[int]{Set: true, Value: &unplayed},
		PlayCount:         OptionalUserDataValue[int]{Set: true, Value: &playCount},
		Likes:             OptionalUserDataValue[bool]{Set: true, Value: &liked},
		LastPlayedDate:    OptionalUserDataValue[time.Time]{Set: true, Value: &lastPlayed},
	})
	if err != nil || userDataState.Progress != nil || userDataState.UserData == nil ||
		userDataState.UserData.Rating == nil || *userDataState.UserData.Rating != rating ||
		userDataState.UserData.PlayedPercentage == nil || *userDataState.UserData.PlayedPercentage != percentage ||
		userDataState.UserData.UnplayedItemCount == nil || *userDataState.UserData.UnplayedItemCount != unplayed ||
		userDataState.UserData.PlayCount == nil || *userDataState.UserData.PlayCount != playCount ||
		userDataState.UserData.Likes == nil || *userDataState.UserData.Likes != liked ||
		userDataState.UserData.LastPlayedDate == nil || !userDataState.UserData.LastPlayedDate.Equal(lastPlayed) {
		t.Fatalf("persist supplemental user data state=%+v error=%v", userDataState, err)
	}
	liked = false
	userDataState, err = service.UpdateUserDataForLinkedSession(ctx, principal, titleID, UpdateUserDataInput{
		Rating: OptionalUserDataValue[float64]{Set: true},
		Likes:  OptionalUserDataValue[bool]{Set: true, Value: &liked},
	})
	if err != nil || userDataState.UserData == nil || !userDataState.UserData.RatingSet || userDataState.UserData.Rating != nil ||
		!userDataState.UserData.PlayedPercentageSet || userDataState.UserData.PlayedPercentage == nil || *userDataState.UserData.PlayedPercentage != percentage ||
		!userDataState.UserData.LikesSet || userDataState.UserData.Likes == nil || *userDataState.UserData.Likes {
		t.Fatalf("partial supplemental user data state=%+v error=%v", userDataState, err)
	}
	detail, err := service.GetCatalogTitle(ctx, principal, titleID)
	if err != nil || detail.UserData == nil || !detail.UserData.RatingSet || detail.UserData.Rating != nil ||
		detail.UserData.PlayCount == nil || *detail.UserData.PlayCount != playCount ||
		detail.UserData.LastPlayedDate == nil || !detail.UserData.LastPlayedDate.Equal(lastPlayed) {
		t.Fatalf("catalog detail supplemental user data=%+v error=%v", detail.UserData, err)
	}
	page, err := service.ListCatalogItems(ctx, principal, CatalogQuery{Limit: 20})
	if err != nil || len(page.Items) != 1 || page.Items[0].UserData == nil ||
		page.Items[0].UserData.UnplayedItemCount == nil || *page.Items[0].UserData.UnplayedItemCount != unplayed {
		t.Fatalf("catalog list supplemental user data page=%+v error=%v", page, err)
	}
	invalidRating, favorite := 11.0, true
	if _, err := service.UpdateUserDataForLinkedSession(ctx, principal, titleID, UpdateUserDataInput{
		Rating: OptionalUserDataValue[float64]{Set: true, Value: &invalidRating}, Favorite: &favorite,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid atomic supplemental mutation error=%v", err)
	}
	var storedRating *float64
	var storedPercentage *float64
	var favoriteExists bool
	if err := pool.QueryRow(ctx, `
		SELECT data.rating, data.played_percentage,
		       EXISTS (SELECT 1 FROM profile_favorites favorite WHERE favorite.profile_id = $1::uuid AND favorite.title_id = $2::uuid)
		FROM profile_user_data data
		WHERE data.profile_id = $1::uuid AND data.title_id = $2::uuid
	`, profileID, titleID).Scan(&storedRating, &storedPercentage, &favoriteExists); err != nil {
		t.Fatalf("read supplemental atomic rollback: %v", err)
	}
	if storedRating != nil || storedPercentage == nil || *storedPercentage != percentage || favoriteExists {
		t.Fatalf("invalid mutation partially committed rating=%v percentage=%v favorite=%t", storedRating, storedPercentage, favoriteExists)
	}
	provider := &blockingLinkedCatalogProvider{entered: make(chan struct{}), release: make(chan struct{})}
	service.SetCanonicalProvider(provider, nil)
	providerExternalID := "linked-catalog-" + suffix
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM titles WHERE resource_provider = 'tmdb' AND resource_id = $1`, providerExternalID)
	})
	resolutionDone := make(chan error, 1)
	go func() {
		_, resolveErr := service.ResolveLinkedCatalogTitle(ctx, principal, ResolveTitleInput{
			MediaType: "movie", Provider: "tmdb", ExternalID: providerExternalID,
			ResourceID: providerExternalID, Title: "Untrusted client snapshot",
		})
		resolutionDone <- resolveErr
	}()
	select {
	case <-provider.entered:
	case <-ctx.Done():
		t.Fatalf("linked catalog resolution did not reach blocked provider: %v", ctx.Err())
	}
	if _, err := pool.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = now(), revoked_reason = 'linked_catalog_provider_race'
		WHERE id = $1::uuid
	`, sessionID); err != nil {
		t.Fatalf("revoke linked session during provider resolution: %v", err)
	}
	close(provider.release)
	if resolveErr := <-resolutionDone; !errors.Is(resolveErr, ErrForbidden) {
		t.Fatalf("post-provider linked resolution error = %v, want %v", resolveErr, ErrForbidden)
	}
	var providerRaceTitles int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::int FROM titles WHERE resource_provider = 'tmdb' AND resource_id = $1
	`, providerExternalID).Scan(&providerRaceTitles); err != nil {
		t.Fatalf("count post-revocation catalog titles: %v", err)
	}
	if providerRaceTitles != 0 {
		t.Fatalf("revoked linked catalog resolution persisted %d titles", providerRaceTitles)
	}
	profileScopedExternalID := "linked-profile-scoped-" + suffix
	if _, err := service.ResolveLinkedCatalogTitle(ctx, principal, ResolveTitleInput{
		MediaType: "movie", Provider: "addon", ExternalID: profileScopedExternalID,
		ResourceID: profileScopedExternalID, Title: "Revoked profile-scoped snapshot",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked profile-scoped linked resolution error = %v, want %v", err, ErrForbidden)
	}
	var profileScopedTitles int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::int FROM titles WHERE resource_provider = 'addon' AND resource_id = $1
	`, profileScopedExternalID).Scan(&profileScopedTitles); err != nil {
		t.Fatalf("count post-revocation profile-scoped titles: %v", err)
	}
	if profileScopedTitles != 0 {
		t.Fatalf("revoked profile-scoped linked resolution persisted %d titles", profileScopedTitles)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM titles WHERE resource_provider = 'addon' AND resource_id = $1`, profileScopedExternalID)
	})
	if _, err := pool.Exec(ctx, `
		UPDATE auth_sessions SET revoked_at = NULL, revoked_reason = NULL WHERE id = $1::uuid
	`, sessionID); err != nil {
		t.Fatalf("restore linked session after provider race: %v", err)
	}
	var foreignProfileID, seriesID, seasonID, episodeID string
	if err := pool.QueryRow(ctx, `INSERT INTO profiles (name) VALUES ($1) RETURNING id::text`, "Foreign favorites "+suffix).Scan(&foreignProfileID); err != nil {
		t.Fatalf("insert foreign favorites profile: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO titles (media_type, display_title) VALUES ('series', $1) RETURNING id::text`, "Favorite series "+suffix).Scan(&seriesID); err != nil {
		t.Fatalf("insert favorite series: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO titles (media_type, parent_id, ordinal, display_title) VALUES ('season', $1::uuid, 1, $2) RETURNING id::text`, seriesID, "Favorite season "+suffix).Scan(&seasonID); err != nil {
		t.Fatalf("insert favorite season: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO titles (media_type, parent_id, ordinal, display_title) VALUES ('episode', $1::uuid, 1, $2) RETURNING id::text`, seasonID, "Favorite episode "+suffix).Scan(&episodeID); err != nil {
		t.Fatalf("insert favorite episode: %v", err)
	}
	favoriteTitles := []struct {
		id        string
		mediaType string
	}{
		{id: titleID, mediaType: "movie"},
		{id: seriesID, mediaType: "series"},
		{id: seasonID, mediaType: "season"},
		{id: episodeID, mediaType: "episode"},
	}
	for _, favoriteTitle := range favoriteTitles {
		if _, err := pool.Exec(ctx, `INSERT INTO profile_favorites (profile_id, title_id) VALUES ($1::uuid, $2::uuid)`, foreignProfileID, favoriteTitle.id); err != nil {
			t.Fatalf("seed foreign %s favorite: %v", favoriteTitle.mediaType, err)
		}
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM titles WHERE id = $1::uuid`, seriesID)
		_, _ = pool.Exec(cleanup, `DELETE FROM profiles WHERE id = $1::uuid`, foreignProfileID)
	})
	desiredFavorite := true
	for _, favoriteTitle := range favoriteTitles {
		state, updateErr := service.UpdateUserDataForLinkedSession(ctx, principal, favoriteTitle.id, UpdateUserDataInput{Favorite: &desiredFavorite})
		if updateErr != nil || !state.Favorite || state.InLibrary != (favoriteTitle.mediaType == "movie") {
			t.Fatalf("favorite %s state=%+v error=%v", favoriteTitle.mediaType, state, updateErr)
		}
		duplicate, duplicateErr := service.UpdateUserDataForLinkedSession(ctx, principal, favoriteTitle.id, UpdateUserDataInput{Favorite: &desiredFavorite})
		if duplicateErr != nil || !duplicate.Favorite {
			t.Fatalf("duplicate favorite %s state=%+v error=%v", favoriteTitle.mediaType, duplicate, duplicateErr)
		}
		var ownCount, foreignCount int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE profile_id = $1::uuid), count(*) FILTER (WHERE profile_id = $2::uuid)
			FROM profile_favorites WHERE title_id = $3::uuid
		`, profileID, foreignProfileID, favoriteTitle.id).Scan(&ownCount, &foreignCount); err != nil {
			t.Fatalf("count %s favorites: %v", favoriteTitle.mediaType, err)
		}
		if ownCount != 1 || foreignCount != 1 {
			t.Fatalf("favorite %s crossed profiles: own=%d foreign=%d", favoriteTitle.mediaType, ownCount, foreignCount)
		}
	}
	desiredFavorite = false
	for _, favoriteTitle := range favoriteTitles {
		state, updateErr := service.UpdateUserDataForLinkedSession(ctx, principal, favoriteTitle.id, UpdateUserDataInput{Favorite: &desiredFavorite})
		if updateErr != nil || state.Favorite || state.InLibrary != (favoriteTitle.mediaType == "movie") {
			t.Fatalf("unfavorite %s state=%+v error=%v", favoriteTitle.mediaType, state, updateErr)
		}
		duplicate, duplicateErr := service.UpdateUserDataForLinkedSession(ctx, principal, favoriteTitle.id, UpdateUserDataInput{Favorite: &desiredFavorite})
		if duplicateErr != nil || duplicate.Favorite {
			t.Fatalf("duplicate unfavorite %s state=%+v error=%v", favoriteTitle.mediaType, duplicate, duplicateErr)
		}
		var ownCount, foreignCount int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE profile_id = $1::uuid), count(*) FILTER (WHERE profile_id = $2::uuid)
			FROM profile_favorites WHERE title_id = $3::uuid
		`, profileID, foreignProfileID, favoriteTitle.id).Scan(&ownCount, &foreignCount); err != nil {
			t.Fatalf("count unfavorited %s: %v", favoriteTitle.mediaType, err)
		}
		if ownCount != 0 || foreignCount != 1 {
			t.Fatalf("unfavorite %s crossed profiles: own=%d foreign=%d", favoriteTitle.mediaType, ownCount, foreignCount)
		}
	}
	var movieStillInLibrary bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM profile_library WHERE profile_id = $1::uuid AND title_id = $2::uuid)`, profileID, titleID).Scan(&movieStillInLibrary); err != nil || !movieStillInLibrary {
		t.Fatalf("unfavorite removed movie from library: exists=%t error=%v", movieStillInLibrary, err)
	}
	var firstSearchAddonID, secondSearchAddonID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO profile_addons (
			profile_id, transport_url, manifest, manifest_id, manifest_version, position, enabled
		) VALUES (
			NULL, $1, '{"id":"test.first","version":"1.0.0","name":"First Search"}'::jsonb,
			$2, '1.0.0', 0, true
		) RETURNING id::text
	`, "https://first-search.invalid/"+suffix+"/manifest.json", "test.first."+suffix).Scan(&firstSearchAddonID); err != nil {
		t.Fatalf("insert first linked search addon: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM profile_addons WHERE id = $1::uuid`, firstSearchAddonID)
	})
	if err := pool.QueryRow(ctx, `
		INSERT INTO profile_addons (
			profile_id, transport_url, manifest, manifest_id, manifest_version, position, enabled
		) VALUES (
			NULL, $1, '{"id":"test.second","version":"1.0.0","name":"Second Search"}'::jsonb,
			$2, '1.0.0', 0, true
		) RETURNING id::text
	`, "https://second-search.invalid/"+suffix+"/manifest.json", "test.second."+suffix).Scan(&secondSearchAddonID); err != nil {
		t.Fatalf("insert second linked search addon: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM profile_addons WHERE id = $1::uuid`, secondSearchAddonID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO addon_profile_access (addon_id, profile_id, position) VALUES
			($1::uuid, $3::uuid, 0), ($2::uuid, $3::uuid, 1)
	`, firstSearchAddonID, secondSearchAddonID, profileID); err != nil {
		t.Fatalf("grant linked search addon access: %v", err)
	}
	sharedResourceID := "shared-search-resource-" + suffix
	firstIdentity := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(firstSearchAddonID+"\x00movie\x00"+sharedResourceID)))
	secondIdentity := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(secondSearchAddonID+"\x00movie\x00"+sharedResourceID)))
	firstSearchTitle, err := service.ResolveLinkedCatalogTitle(ctx, principal, ResolveTitleInput{
		MediaType: "movie", Provider: "addon", ExternalID: firstIdentity,
		ResourceID: sharedResourceID, Title: "First producer title", SourceAddonID: firstSearchAddonID,
		SourceCatalogID: "search", SourceName: "First Search",
	})
	if err != nil {
		t.Fatalf("resolve first linked add-on search title: %v", err)
	}
	secondSearchTitle, err := service.ResolveLinkedCatalogTitle(ctx, principal, ResolveTitleInput{
		MediaType: "movie", Provider: "addon", ExternalID: secondIdentity,
		ResourceID: sharedResourceID, Title: "Second producer title", SourceAddonID: secondSearchAddonID,
		SourceCatalogID: "search", SourceName: "Second Search",
	})
	if err != nil {
		t.Fatalf("resolve second linked add-on search title: %v", err)
	}
	if firstSearchTitle.TitleID == secondSearchTitle.TitleID {
		t.Fatalf("two add-ons sharing resource ID converged: %+v %+v", firstSearchTitle, secondSearchTitle)
	}
	for _, expected := range []struct {
		id, addonID, name string
	}{
		{firstSearchTitle.TitleID, firstSearchAddonID, "First Search"},
		{secondSearchTitle.TitleID, secondSearchAddonID, "Second Search"},
	} {
		projected, readErr := service.GetCatalogTitle(ctx, principal, expected.id)
		if readErr != nil {
			t.Fatalf("read persisted add-on search projection: %v", readErr)
		}
		if projected.ResourceID != sharedResourceID || projected.SourceAddonID != expected.addonID ||
			projected.SourceCatalogID != "search" || projected.SourceName != expected.name {
			t.Fatalf("persisted add-on search provenance = %+v", projected)
		}
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM titles WHERE id = ANY($1::uuid[])`, []string{firstSearchTitle.TitleID, secondSearchTitle.TitleID})
	})
	mutationDone := make(chan error, 1)
	go func() {
		_, mutationErr := service.ApplyPlaybackEventForLinkedSession(ctx, principal, titleID, UpdateProgressInput{
			PositionSeconds: 30, DurationSeconds: 120,
		})
		mutationDone <- mutationErr
	}()
	select {
	case <-sink.entered:
	case <-ctx.Done():
		t.Fatalf("linked mutation did not reach blocked transaction: %v", ctx.Err())
	}

	revokeDone := make(chan error, 1)
	go func() {
		_, revokeErr := pool.Exec(ctx, `
			UPDATE auth_sessions
			SET revoked_at = now(), revoked_reason = 'linked_mutation_race'
			WHERE id = $1::uuid
		`, sessionID)
		revokeDone <- revokeErr
	}()
	select {
	case revokeErr := <-revokeDone:
		t.Fatalf("revocation bypassed linked mutation transaction lock: %v", revokeErr)
	case <-time.After(100 * time.Millisecond):
	}

	close(sink.release)
	if mutationErr := <-mutationDone; mutationErr != nil {
		t.Fatalf("commit authorized linked mutation: %v", mutationErr)
	}
	if revokeErr := <-revokeDone; revokeErr != nil {
		t.Fatalf("commit serialized revocation: %v", revokeErr)
	}
	if _, err := service.ApplyPlaybackEventForLinkedSession(ctx, principal, titleID, UpdateProgressInput{
		PositionSeconds: 40, DurationSeconds: 120, ExpectedVersion: 1,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("post-revocation linked playback error = %v, want %v", err, ErrForbidden)
	}
	if _, err := service.SetWatchedForLinkedSession(ctx, principal, titleID, true, CompletionInput{ExpectedVersion: 1}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("post-revocation linked mutation error = %v, want %v", err, ErrForbidden)
	}
	var position, duration int
	var completed bool
	var version int64
	if err := pool.QueryRow(ctx, `
		SELECT position_seconds, duration_seconds, completed, version
		FROM profile_progress WHERE profile_id = $1::uuid AND title_id = $2::uuid
	`, profileID, titleID).Scan(&position, &duration, &completed, &version); err != nil {
		t.Fatalf("read linked mutation result: %v", err)
	}
	if position != 30 || duration != 120 || completed || version != 1 {
		t.Fatalf("revocation race changed watchstate: position=%d duration=%d completed=%t version=%d", position, duration, completed, version)
	}
	if _, err := pool.Exec(ctx, `
		WITH restored AS (
			UPDATE auth_sessions SET revoked_at = NULL, revoked_reason = NULL
			WHERE id = $1::uuid RETURNING id
		)
		UPDATE profile_progress
		SET position_seconds = 120, duration_seconds = 120, completed = true, version = 2
		WHERE profile_id = $2::uuid AND title_id = $3::uuid
		  AND EXISTS (SELECT 1 FROM restored)
	`, sessionID, profileID, titleID); err != nil {
		t.Fatalf("seed completed replay state: %v", err)
	}
	failures := []struct {
		name string
		sink error
		want error
	}{
		{name: "canceled", sink: context.Canceled, want: context.Canceled},
		{name: "outbox", sink: fmt.Errorf("outbox unavailable: %w", tracking.ErrOutboxCapacity), want: ErrOutboxCapacity},
	}
	for _, failure := range failures {
		t.Run(failure.name, func(t *testing.T) {
			service.tracking = &failingLinkedMutationSink{err: failure.sink}
			if _, err := service.ApplyPlaybackEventForLinkedSession(ctx, principal, titleID, UpdateProgressInput{
				PositionSeconds: 10, DurationSeconds: 120, ExpectedVersion: 2,
			}); !errors.Is(err, failure.want) {
				t.Fatalf("atomic replay error = %v, want %v", err, failure.want)
			}
			if err := pool.QueryRow(ctx, `
				SELECT position_seconds, duration_seconds, completed, version
				FROM profile_progress WHERE profile_id = $1::uuid AND title_id = $2::uuid
			`, profileID, titleID).Scan(&position, &duration, &completed, &version); err != nil {
				t.Fatalf("read replay after rollback: %v", err)
			}
			if position != 120 || duration != 120 || !completed || version != 2 {
				t.Fatalf("failed replay partially committed: position=%d duration=%d completed=%t version=%d", position, duration, completed, version)
			}
			desired := true
			positionUpdate, playedUpdate := 10, false
			if _, err := service.UpdateUserDataForLinkedSession(ctx, principal, titleID, UpdateUserDataInput{
				PositionSeconds: &positionUpdate, DurationSeconds: 120, Played: &playedUpdate, Favorite: &desired,
			}); !errors.Is(err, failure.want) {
				t.Fatalf("atomic user data error = %v, want %v", err, failure.want)
			}
			var favoriteExists, libraryExists bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM profile_favorites WHERE profile_id = $1::uuid AND title_id = $2::uuid),
				       EXISTS (SELECT 1 FROM profile_library WHERE profile_id = $1::uuid AND title_id = $2::uuid)
			`, profileID, titleID).Scan(&favoriteExists, &libraryExists); err != nil {
				t.Fatalf("read user data after rollback: %v", err)
			}
			if favoriteExists || !libraryExists {
				t.Fatalf("failed user data partially committed: favorite=%t library=%t", favoriteExists, libraryExists)
			}
		})
	}
}
