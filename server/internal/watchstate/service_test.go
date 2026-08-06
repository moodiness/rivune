package watchstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/tracking"
)

type progressBatchQueryCounter struct {
	read                atomic.Int64
	write               atomic.Int64
	titleAddonShareLock atomic.Int64
	tvAddonShareLock    atomic.Int64
}

func (counter *progressBatchQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "watchstate.progress_batch") {
		counter.read.Add(1)
	}
	if strings.Contains(data.SQL, "watchstate.set_watched_batch") {
		counter.write.Add(1)
	}
	if strings.Contains(data.SQL, "watchstate.lock_title_addon") && strings.Contains(data.SQL, "FOR SHARE OF addon") {
		counter.titleAddonShareLock.Add(1)
	}
	if strings.Contains(data.SQL, "watchstate.lock_tv_addon") && strings.Contains(data.SQL, "FOR SHARE OF addon") {
		counter.tvAddonShareLock.Add(1)
	}
	return ctx
}

func (*progressBatchQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {
}

func TestActiveProfileIDRequiresUnexpiredSelection(t *testing.T) {
	profileID := "11111111-1111-4111-8111-111111111111"
	future := time.Now().UTC().Add(time.Hour)
	past := time.Now().UTC().Add(-time.Hour)

	tests := []struct {
		name      string
		principal auth.Principal
		wantErr   bool
	}{
		{name: "selected", principal: auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileID, ProfileGrantExpiresAt: &future}},
		{name: "missing", principal: auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator}, wantErr: true},
		{name: "expired", principal: auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileID, ProfileGrantExpiresAt: &past}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := activeProfileID(test.principal)
			if test.wantErr {
				if !errors.Is(err, ErrProfileRequired) {
					t.Fatalf("expected profile requirement, got %v", err)
				}
				return
			}
			if err != nil || got != profileID {
				t.Fatalf("expected profile %q, got %q error %v", profileID, got, err)
			}
		})
	}
}

func TestNormalizeLibraryQuery(t *testing.T) {
	mediaType, page, pageSize, err := normalizeLibraryQuery(" Series ", 0, 0)
	if err != nil {
		t.Fatalf("normalize defaults: %v", err)
	}
	if mediaType != "series" || page != 1 || pageSize != 20 {
		t.Fatalf("unexpected normalized query: %q %d %d", mediaType, page, pageSize)
	}
	mediaType, page, pageSize, err = normalizeLibraryQuery(" TV ", 1, 40)
	if err != nil || mediaType != "tv" || page != 1 || pageSize != 40 {
		t.Fatalf("unexpected normalized TV query: %q %d %d error %v", mediaType, page, pageSize, err)
	}
	for _, test := range []struct {
		mediaType string
		page      int
		pageSize  int
	}{
		{mediaType: "episode", page: 1, pageSize: 20},
		{mediaType: "movie", page: -1, pageSize: 20},
		{mediaType: "movie", page: 1, pageSize: 101},
	} {
		if _, _, _, err := normalizeLibraryQuery(test.mediaType, test.page, test.pageSize); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid query for %+v, got %v", test, err)
		}
	}
}

func TestTVLibraryMembershipRejectsUnboundedInputsBeforeQuery(t *testing.T) {
	service := NewService(nil)
	identities := make([]TVLibraryIdentity, MaximumTVLibraryMembershipIdentities+1)
	for index := range identities {
		identities[index] = TVLibraryIdentity{
			SourceAddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			ResourceID:    "channel",
		}
	}
	if _, err := service.TVLibraryMembership(context.Background(), auth.Principal{}, nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected empty membership batch rejection, got %v", err)
	}
	if _, err := service.TVLibraryMembership(context.Background(), auth.Principal{}, identities); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected oversized membership batch rejection, got %v", err)
	}
}

func TestValidateProgressInputBoundaries(t *testing.T) {
	valid := []UpdateProgressInput{
		{},
		{PositionSeconds: 10, DurationSeconds: 100, ExpectedVersion: 2},
		{PositionSeconds: 100, DurationSeconds: 100, Completed: true},
	}
	for _, input := range valid {
		if err := validateProgressInput(input); err != nil {
			t.Fatalf("expected valid input %+v, got %v", input, err)
		}
	}

	invalid := []UpdateProgressInput{
		{ExpectedVersion: -1},
		{PositionSeconds: -1, DurationSeconds: 100},
		{PositionSeconds: 1, DurationSeconds: 0},
		{PositionSeconds: 101, DurationSeconds: 100},
	}
	for _, input := range invalid {
		if err := validateProgressInput(input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid input %+v, got %v", input, err)
		}
	}
}

func TestNormalizeTitleID(t *testing.T) {
	got, err := normalizeTitleID(" 550E8400-E29B-41D4-A716-446655440000 ")
	if err != nil || got != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("unexpected normalized title ID %q error %v", got, err)
	}
	if _, err := normalizeTitleID("not-a-uuid"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid UUID error, got %v", err)
	}
}

func TestNormalizeProgressBatchInputs(t *testing.T) {
	first := "550E8400-E29B-41D4-A716-446655440000"
	second := "550e8400-e29b-41d4-a716-446655440001"
	normalized, err := normalizeProgressBatchTitleIDs([]string{first, second})
	if err != nil || normalized[0] != strings.ToLower(first) || normalized[1] != second {
		t.Fatalf("unexpected normalized progress batch %v error %v", normalized, err)
	}
	if _, err := normalizeProgressBatchTitleIDs(nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected empty progress batch rejection, got %v", err)
	}
	bounded := make([]string, MaximumProgressBatchSize)
	for index := range bounded {
		bounded[index] = fmt.Sprintf("550e8400-e29b-41d4-a716-%012d", index)
	}
	if normalized, err := normalizeProgressBatchTitleIDs(bounded); err != nil || len(normalized) != MaximumProgressBatchSize {
		t.Fatalf("expected exactly %d progress items to remain valid, got %d error %v", MaximumProgressBatchSize, len(normalized), err)
	}
	oversized := make([]string, MaximumProgressBatchSize+1)
	for index := range oversized {
		oversized[index] = first
	}
	if _, err := normalizeProgressBatchTitleIDs(oversized); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected oversized progress batch rejection, got %v", err)
	}
	if _, err := normalizeProgressBatchTitleIDs([]string{first, strings.ToLower(first)}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected duplicate progress batch rejection, got %v", err)
	}
	if _, err := normalizeSetWatchedBatchInput([]SetWatchedBatchItem{{TitleID: first, ExpectedVersion: -1}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected negative batch version rejection, got %v", err)
	}
}

func TestNormalizeCustomSeriesInputValidationAndIdentityScope(t *testing.T) {
	base := ResolveCustomSeriesInput{
		SourceAddonID: "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA",
		SourceType:    "anime",
		Series: CustomSeriesSnapshot{
			ResourceID: "opaque:series", Title: " Custom Show ",
			PosterURL: "/api/v1/artwork/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		Videos: []CustomVideoSnapshot{
			{ResourceID: "opaque:episode:2", Title: " Second ", SeasonNumber: 1, EpisodeNumber: 2, Released: "2026-08-06"},
			{ResourceID: "opaque:episode:1", SeasonNumber: 1, EpisodeNumber: 1},
		},
	}
	normalized, videos, err := normalizeCustomSeriesInput(base)
	if err != nil {
		t.Fatalf("normalize valid custom series: %v", err)
	}
	if normalized.SourceAddonID != strings.ToLower(base.SourceAddonID) || normalized.Series.Title != "Custom Show" || videos[0].Title != "Second" {
		t.Fatalf("unexpected normalized snapshot: %+v videos=%+v", normalized, videos)
	}
	if videos[0].EpisodeIdentity == videos[1].EpisodeIdentity || videos[0].SeasonIdentity != videos[1].SeasonIdentity {
		t.Fatalf("custom hierarchy identities are not correctly scoped: %+v", videos)
	}
	if videos[0].EpisodeIdentity == customTitleExternalID(normalized.SourceAddonID, "other", "episode", normalized.Series.ResourceID, videos[0].ResourceID) {
		t.Fatal("source type did not scope custom episode identity")
	}
	if videos[0].EpisodeIdentity == customTitleExternalID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", normalized.SourceType, "episode", normalized.Series.ResourceID, videos[0].ResourceID) {
		t.Fatal("addon installation did not scope custom episode identity")
	}

	invalid := []ResolveCustomSeriesInput{
		{SourceAddonID: "invalid", SourceType: "anime", Series: CustomSeriesSnapshot{ResourceID: "series", Title: "Show"}},
		{SourceAddonID: normalized.SourceAddonID, SourceType: "", Series: CustomSeriesSnapshot{ResourceID: "series", Title: "Show"}},
		{SourceAddonID: normalized.SourceAddonID, SourceType: "anime", Series: CustomSeriesSnapshot{ResourceID: " series ", Title: "Show"}},
		{SourceAddonID: normalized.SourceAddonID, SourceType: "anime", Series: CustomSeriesSnapshot{ResourceID: "series", Title: "Show", PosterURL: "https://raw.invalid/poster.jpg"}},
		{SourceAddonID: normalized.SourceAddonID, SourceType: "anime", Series: CustomSeriesSnapshot{ResourceID: "series", Title: "Show"}, Videos: []CustomVideoSnapshot{{ResourceID: "one", SeasonNumber: -1}}},
		{SourceAddonID: normalized.SourceAddonID, SourceType: "anime", Series: CustomSeriesSnapshot{ResourceID: "series", Title: "Show"}, Videos: []CustomVideoSnapshot{{ResourceID: "same"}, {ResourceID: "same", EpisodeNumber: 1}}},
		{SourceAddonID: normalized.SourceAddonID, SourceType: "anime", Series: CustomSeriesSnapshot{ResourceID: "series", Title: "Show"}, Videos: []CustomVideoSnapshot{{ResourceID: "one"}, {ResourceID: "two"}}},
		{SourceAddonID: normalized.SourceAddonID, SourceType: "anime", Series: CustomSeriesSnapshot{ResourceID: "series", Title: "Show"}, Videos: []CustomVideoSnapshot{{ResourceID: "one", Released: "2026-02-30"}}},
		{SourceAddonID: normalized.SourceAddonID, SourceType: "anime", Series: CustomSeriesSnapshot{ResourceID: "series", Title: "Show"}, Videos: []CustomVideoSnapshot{{ResourceID: "one", SeasonNumber: 2147483648}}},
	}
	for index, input := range invalid {
		if _, _, err := normalizeCustomSeriesInput(input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid custom input %d rejection, got %v", index, err)
		}
	}
	oversized := base
	oversized.Videos = make([]CustomVideoSnapshot, MaximumCustomSeriesVideos+1)
	if _, _, err := normalizeCustomSeriesInput(oversized); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected oversized video list rejection, got %v", err)
	}
}

func TestResolveTitleRejectsNonISOReleaseDate(t *testing.T) {
	profileID := "11111111-1111-4111-8111-111111111111"
	expiresAt := time.Now().UTC().Add(time.Hour)
	service := NewService(nil)
	_, err := service.ResolveTitle(context.Background(), auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt}, ResolveTitleInput{
		MediaType: "movie", Provider: "tmdb", ExternalID: "1", ResourceID: "1",
		Title: "Movie", Released: "2026-8-01",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid release date rejection, got %v", err)
	}
}

func TestResolveTitleRequiresAddonScopedTVIdentity(t *testing.T) {
	profileID := "11111111-1111-4111-8111-111111111111"
	expiresAt := time.Now().UTC().Add(time.Hour)
	principal := auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt}
	service := NewService(nil)

	for _, input := range []ResolveTitleInput{
		{MediaType: "tv", Provider: "tmdb", ResourceID: "channel", Title: "Channel", SourceAddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		{MediaType: "tv", Provider: "addon", ResourceID: "channel", Title: "Channel", SourceAddonID: "not-a-uuid"},
	} {
		if _, err := service.ResolveTitle(context.Background(), principal, input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid TV source identity rejection for %+v, got %v", input, err)
		}
	}
}

type canonicalProviderStub struct {
	movie metadata.ProviderMovie
}

func (provider *canonicalProviderStub) MovieDetails(context.Context, string, string) (metadata.ProviderMovie, error) {
	return provider.movie, nil
}

func (*canonicalProviderStub) SeriesDetails(context.Context, string, string) (metadata.ProviderSeries, error) {
	return metadata.ProviderSeries{}, metadata.ErrProviderNotFound
}

type canonicalResolverStub map[string]string

func (resolver canonicalResolverStub) ResolveExternalID(_ context.Context, mediaType, provider, externalID string) (string, error) {
	if resolved := resolver[mediaType+":"+provider+":"+externalID]; resolved != "" {
		return resolved, nil
	}
	return "", metadata.ErrProviderNotFound
}

type canonicalResolverFunc func(context.Context, string, string, string) (string, error)

func (resolver canonicalResolverFunc) ResolveExternalID(ctx context.Context, mediaType, provider, externalID string) (string, error) {
	return resolver(ctx, mediaType, provider, externalID)
}

func TestResolveTitleCanonicalIdentityCannotBePoisonedAcrossProfiles(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL title resolution test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profiles (
			id uuid PRIMARY KEY,
			category_id uuid
		);
		CREATE TEMPORARY TABLE user_profile_access (
			user_id uuid NOT NULL,
			profile_id uuid NOT NULL,
			can_manage boolean NOT NULL DEFAULT false,
			PRIMARY KEY (user_id, profile_id)
		);
		INSERT INTO profiles (id, category_id) VALUES
			('11111111-1111-4111-8111-111111111111', '33333333-3333-4333-8333-333333333333'),
			('55555555-5555-4555-8555-555555555555', '33333333-3333-4333-8333-333333333333');
		INSERT INTO user_profile_access (user_id, profile_id, can_manage) VALUES
			('44444444-4444-4444-8444-444444444444', '11111111-1111-4111-8111-111111111111', true),
			('66666666-6666-4666-8666-666666666666', '55555555-5555-4555-8555-555555555555', false);
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			media_type text NOT NULL,
			display_title text,
			poster_url text,
			background_url text,
			release_info text,
			release_date date,
			resource_id text,
			resource_provider text,
			source_addon_id uuid,
			source_catalog_id text,
			source_name text,
			country text,
			language text,
			category text,
			is_current boolean NOT NULL DEFAULT true,
			updated_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE TEMPORARY TABLE title_external_ids (
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			provider text NOT NULL,
			namespace text NOT NULL,
			external_id text NOT NULL,
			PRIMARY KEY (provider, namespace, external_id),
			UNIQUE (title_id, provider, namespace)
		);
		CREATE TEMPORARY TABLE profile_library (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL
		);
		CREATE TEMPORARY TABLE profile_progress (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL
		)
	`); err != nil {
		t.Fatalf("create title resolution fixtures: %v", err)
	}

	provider := &canonicalProviderStub{movie: metadata.ProviderMovie{
		ExternalID:  "123",
		Title:       "Canonical Movie",
		PosterURL:   "https://image.tmdb.org/canonical-poster.jpg",
		BackdropURL: "https://image.tmdb.org/canonical-background.jpg",
		ReleaseDate: "2025-04-18",
		AdditionalIDs: map[string]string{
			"imdb": "tt00123",
		},
	}}
	service := NewService(pool)
	service.SetCanonicalProvider(provider, canonicalResolverStub{"movie:imdb:tt00123": "123"})
	expiresAt := time.Now().UTC().Add(time.Hour)
	categoryID := "33333333-3333-4333-8333-333333333333"
	attackerProfileID := "11111111-1111-4111-8111-111111111111"
	victimProfileID := "55555555-5555-4555-8555-555555555555"
	attacker := auth.Principal{
		UserID: "44444444-4444-4444-8444-444444444444", Role: "member",
		AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID,
		ActiveProfileID: &attackerProfileID, ProfileGrantExpiresAt: &expiresAt,
	}
	victim := auth.Principal{
		UserID: "66666666-6666-4666-8666-666666666666", Role: "member",
		AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID,
		ActiveProfileID: &victimProfileID, ProfileGrantExpiresAt: &expiresAt,
	}

	attackerResult, err := service.ResolveTitle(ctx, attacker, ResolveTitleInput{
		MediaType: "movie", Provider: "imdb", ExternalID: "tt00123", ResourceID: "attacker-resource",
		Title: "Poisoned Movie", PosterURL: "https://attacker.invalid/poster.jpg", Released: "2024-01-01",
	})
	if err != nil {
		t.Fatalf("resolve attacker payload through canonical provider: %v", err)
	}
	if attackerResult.Title != "Canonical Movie" || attackerResult.ResourceID != "tt00123" {
		t.Fatalf("attacker payload survived canonical resolution: %+v", attackerResult)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO profile_library (profile_id, title_id)
		VALUES ($1::uuid, $2::uuid)
	`, attackerProfileID, attackerResult.TitleID); err != nil {
		t.Fatalf("retain attacker title in library: %v", err)
	}

	victimResult, err := service.ResolveTitle(ctx, victim, ResolveTitleInput{
		MediaType: "movie", Provider: "tmdb", ExternalID: "123", ResourceID: "victim-resource",
		Title: "Victim Client Snapshot", PosterURL: "https://victim.invalid/poster.jpg", Released: "2026-01-01",
	})
	if err != nil {
		t.Fatalf("resolve canonical identity for unrelated victim profile: %v", err)
	}
	if victimResult.TitleID != attackerResult.TitleID {
		t.Fatalf("canonical identities did not converge: attacker=%q victim=%q", attackerResult.TitleID, victimResult.TitleID)
	}
	idempotent, err := service.ResolveTitle(ctx, victim, ResolveTitleInput{
		MediaType: "movie", Provider: "tmdb", ExternalID: "123", ResourceID: "another-client-resource",
		Title: "Another Client Snapshot",
	})
	if err != nil || idempotent.TitleID != victimResult.TitleID {
		t.Fatalf("canonical resolution was not idempotent: result=%+v err=%v", idempotent, err)
	}

	var titleCount, identityCount int
	var title, posterURL, backgroundURL, releaseInfo, releaseDate, resourceID, resourceProvider string
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::int,
		       min(display_title), min(poster_url), min(background_url), min(release_info),
		       min(release_date::text), min(resource_id), min(resource_provider)
		FROM titles
	`).Scan(&titleCount, &title, &posterURL, &backgroundURL, &releaseInfo, &releaseDate, &resourceID, &resourceProvider); err != nil {
		t.Fatalf("query converged canonical title: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM title_external_ids`).Scan(&identityCount); err != nil {
		t.Fatalf("count canonical identities: %v", err)
	}
	if titleCount != 1 || identityCount != 2 || title != "Canonical Movie" ||
		posterURL != "https://image.tmdb.org/canonical-poster.jpg" ||
		backgroundURL != "https://image.tmdb.org/canonical-background.jpg" ||
		releaseInfo != "2025" || releaseDate != "2025-04-18" ||
		resourceID != "tt00123" || resourceProvider != "imdb" {
		t.Fatalf("canonical snapshot was corrupted: titles=%d identities=%d title=%q poster=%q background=%q releaseInfo=%q releaseDate=%q resource=%s:%s",
			titleCount, identityCount, title, posterURL, backgroundURL, releaseInfo, releaseDate, resourceProvider, resourceID)
	}

	const conflictingTitleID = "77777777-7777-4777-8777-777777777777"
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, display_title, resource_id, resource_provider)
		VALUES ($1::uuid, 'movie', 'Different Canonical Movie', '999', 'tvdb')
	`, conflictingTitleID); err != nil {
		t.Fatalf("seed conflicting canonical title: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id)
		VALUES ($1::uuid, 'tvdb', 'movie', '999')
	`, conflictingTitleID); err != nil {
		t.Fatalf("seed conflicting canonical identity: %v", err)
	}
	provider.movie.Title = "Conflicting Provider Update"
	provider.movie.AdditionalIDs["tvdb"] = "999"
	if _, err := service.ResolveTitle(ctx, victim, ResolveTitleInput{
		MediaType: "movie", Provider: "tmdb", ExternalID: "123", ResourceID: "123", Title: "Ignored",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("canonical identity conflict error = %v, want %v", err, ErrConflict)
	}
	var unchangedTitle string
	if err := pool.QueryRow(ctx, `
		SELECT display_title
		FROM titles
		WHERE id = $1::uuid
	`, victimResult.TitleID).Scan(&unchangedTitle); err != nil {
		t.Fatalf("query title after canonical conflict: %v", err)
	}
	if unchangedTitle != "Canonical Movie" {
		t.Fatalf("canonical conflict partially updated title: %q", unchangedTitle)
	}
}

func TestResolveTitleProfileScopedFallbackIsolatedAndLibraryPreservesIdentity(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL profile title identity test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profiles (
			id uuid PRIMARY KEY,
			category_id uuid
		);
		CREATE TEMPORARY TABLE user_profile_access (
			user_id uuid NOT NULL,
			profile_id uuid NOT NULL,
			can_manage boolean NOT NULL DEFAULT false,
			PRIMARY KEY (user_id, profile_id)
		);
		INSERT INTO profiles (id) VALUES
			('11111111-1111-4111-8111-111111111111'),
			('22222222-2222-4222-8222-222222222222');
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			media_type text NOT NULL,
			parent_id uuid,
			ordinal integer,
			display_title text,
			poster_url text,
			background_url text,
			release_info text,
			release_date date,
			resource_id text,
			resource_provider text,
			source_addon_id uuid,
			source_catalog_id text,
			source_name text,
			country text,
			language text,
			category text,
			is_current boolean NOT NULL DEFAULT true,
			updated_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE TEMPORARY TABLE title_external_ids (
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			provider text NOT NULL,
			namespace text NOT NULL,
			external_id text NOT NULL,
			PRIMARY KEY (provider, namespace, external_id),
			UNIQUE (title_id, provider, namespace)
		);
		CREATE TEMPORARY TABLE profile_title_external_ids (
			profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			provider text NOT NULL,
			namespace text NOT NULL,
			external_id text NOT NULL,
			PRIMARY KEY (profile_id, provider, namespace, external_id),
			UNIQUE (title_id)
		);
		CREATE TEMPORARY TABLE profile_addons (
			id uuid PRIMARY KEY,
			enabled boolean NOT NULL DEFAULT true
		);
		CREATE TEMPORARY TABLE addon_profile_access (
			addon_id uuid NOT NULL,
			profile_id uuid NOT NULL,
			PRIMARY KEY (addon_id, profile_id)
		);
		CREATE TEMPORARY TABLE profile_library (
			profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			added_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
		CREATE TEMPORARY TABLE profile_progress (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL,
			position_seconds integer NOT NULL,
			duration_seconds integer NOT NULL,
			completed boolean NOT NULL DEFAULT false,
			version bigint NOT NULL DEFAULT 1,
			last_watched_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
		CREATE TEMPORARY TABLE profile_continue_dismissals (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL,
			dismissed_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
	`); err != nil {
		t.Fatalf("create profile title identity fixtures: %v", err)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	profileOneID := "11111111-1111-4111-8111-111111111111"
	profileTwoID := "22222222-2222-4222-8222-222222222222"
	profileOne := auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileOneID, ProfileGrantExpiresAt: &expiresAt}
	profileTwo := auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileTwoID, ProfileGrantExpiresAt: &expiresAt}
	service := NewService(pool)
	externalID := strings.Repeat("addon-claim-", 20)

	first, err := service.ResolveTitle(ctx, profileOne, ResolveTitleInput{
		MediaType: "movie", Provider: "addon", ExternalID: externalID,
		ResourceID: "profile-one-resource", Title: "Profile One Snapshot",
		PosterURL: "https://one.invalid/poster.jpg",
	})
	if err != nil {
		t.Fatalf("resolve first profile addon title without canonical provider: %v", err)
	}
	second, err := service.ResolveTitle(ctx, profileTwo, ResolveTitleInput{
		MediaType: "movie", Provider: "addon", ExternalID: externalID,
		ResourceID: "profile-two-resource", Title: "Profile Two Snapshot",
		PosterURL: "https://two.invalid/poster.jpg",
	})
	if err != nil {
		t.Fatalf("resolve second profile addon title without canonical provider: %v", err)
	}
	if first.TitleID == second.TitleID {
		t.Fatalf("profile-scoped addon identities converged across profiles: %+v %+v", first, second)
	}
	repeated, err := service.ResolveTitle(ctx, profileOne, ResolveTitleInput{
		MediaType: "movie", Provider: "addon", ExternalID: externalID,
		ResourceID: "profile-one-updated-resource", Title: "Profile One Updated Snapshot",
	})
	if err != nil || repeated.TitleID != first.TitleID {
		t.Fatalf("profile-scoped identity was not idempotent: result=%+v err=%v", repeated, err)
	}
	unsupported, err := service.ResolveTitle(ctx, profileOne, ResolveTitleInput{
		MediaType: "series", Provider: "catalog", ExternalID: "series-only",
		ResourceID: "series-resource", Title: "Catalog Series",
	})
	if err != nil || unsupported.ExternalID != "series-only" {
		t.Fatalf("unsupported provider did not remain usable without canonical provider: result=%+v err=%v", unsupported, err)
	}

	var firstTitle, firstResource, secondTitle, secondResource string
	if err := pool.QueryRow(ctx, `
		SELECT first_title.display_title, first_title.resource_id,
		       second_title.display_title, second_title.resource_id
		FROM titles first_title
		CROSS JOIN titles second_title
		WHERE first_title.id = $1::uuid AND second_title.id = $2::uuid
	`, first.TitleID, second.TitleID).Scan(&firstTitle, &firstResource, &secondTitle, &secondResource); err != nil {
		t.Fatalf("query independent profile snapshots: %v", err)
	}
	if firstTitle != "Profile One Updated Snapshot" || firstResource != "profile-one-updated-resource" ||
		secondTitle != "Profile Two Snapshot" || secondResource != "profile-two-resource" {
		t.Fatalf("profile snapshots were not independent: first=%q/%q second=%q/%q", firstTitle, firstResource, secondTitle, secondResource)
	}

	added, err := service.AddLibrary(ctx, profileOne, first.TitleID)
	if err != nil {
		t.Fatalf("add profile-scoped title to owning library: %v", err)
	}
	if added.ExternalID != externalID {
		t.Fatalf("library addition returned external ID %q, want original %q", added.ExternalID, externalID)
	}
	if _, err := service.AddLibrary(ctx, profileTwo, first.TitleID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other profile accessed profile-scoped title: %v", err)
	}
	page, err := service.Library(ctx, profileOne, "movie", 1, 20)
	if err != nil {
		t.Fatalf("list profile-scoped library: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].TitleID != first.TitleID || page.Items[0].ExternalID != externalID {
		t.Fatalf("library did not preserve original profile-scoped identity: %+v", page)
	}

	var globalIdentityCount, scopedIdentityCount int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM title_external_ids`).Scan(&globalIdentityCount); err != nil {
		t.Fatalf("count global fallback identities: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM profile_title_external_ids`).Scan(&scopedIdentityCount); err != nil {
		t.Fatalf("count profile-scoped identities: %v", err)
	}
	if globalIdentityCount != 0 || scopedIdentityCount != 3 {
		t.Fatalf("fallback identity placement: global=%d scoped=%d", globalIdentityCount, scopedIdentityCount)
	}

	t.Run("watchstate references enforce scoped ownership atomically", func(t *testing.T) {
		ownedProgress, err := service.UpdateProgress(ctx, profileOne, first.TitleID, UpdateProgressInput{
			PositionSeconds: 120, DurationSeconds: 1200,
		})
		if err != nil {
			t.Fatalf("owner writes profile-scoped progress: %v", err)
		}
		if loaded, err := service.GetProgress(ctx, profileOne, first.TitleID); err != nil || loaded.Version != ownedProgress.Version {
			t.Fatalf("owner reads profile-scoped progress: progress=%+v err=%v", loaded, err)
		}
		ownerContinue, err := service.ContinueWatching(ctx, profileOne, 10)
		if err != nil || len(ownerContinue.Items) != 1 || ownerContinue.Items[0].TitleID != first.TitleID {
			t.Fatalf("owner continue list lost profile-scoped title: page=%+v err=%v", ownerContinue, err)
		}

		const canonicalTitleID = "99999999-9999-4999-8999-999999999999"
		if _, err := pool.Exec(ctx, `
			INSERT INTO titles (
				id, media_type, display_title, resource_id, resource_provider
			) VALUES ($1::uuid, 'movie', 'Shared Canonical Movie', '4242', 'tmdb');
			INSERT INTO title_external_ids (title_id, provider, namespace, external_id)
			VALUES ($1::uuid, 'tmdb', 'movie', 'sha256:canonical-4242');
			INSERT INTO profile_progress (
				profile_id, title_id, position_seconds, duration_seconds, completed
			) VALUES ($2::uuid, $3::uuid, 90, 900, false);
			INSERT INTO profile_library (profile_id, title_id)
			VALUES ($2::uuid, $3::uuid);
		`, pgx.QueryExecModeSimpleProtocol, canonicalTitleID, profileTwoID, first.TitleID); err != nil {
			t.Fatalf("seed legacy foreign watchstate references: %v", err)
		}

		if _, err := service.GetProgress(ctx, profileTwo, first.TitleID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign scoped progress read error = %v, want non-revealing not found", err)
		}
		if _, err := service.UpdateProgress(ctx, profileTwo, first.TitleID, UpdateProgressInput{
			PositionSeconds: 180, DurationSeconds: 900, ExpectedVersion: 1,
		}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign scoped progress update error = %v, want non-revealing not found", err)
		}
		if _, err := service.SetWatched(ctx, profileTwo, first.TitleID, true, CompletionInput{
			ExpectedVersion: 1,
		}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign scoped watched update error = %v, want non-revealing not found", err)
		}
		if err := service.ClearProgress(ctx, profileTwo, first.TitleID, 1); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign scoped progress clear error = %v, want non-revealing not found", err)
		}
		if err := service.DismissContinue(ctx, profileTwo, first.TitleID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign scoped continue dismissal error = %v, want non-revealing not found", err)
		}
		if _, err := service.GetProgressBatch(ctx, profileTwo, []string{second.TitleID, first.TitleID}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign scoped progress batch read error = %v, want non-revealing not found", err)
		}
		if _, err := service.SetWatchedBatch(ctx, profileTwo, []SetWatchedBatchItem{
			{TitleID: second.TitleID, Completed: true, ExpectedVersion: 0},
			{TitleID: first.TitleID, Completed: true, ExpectedVersion: 1},
		}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("mixed scoped watched batch error = %v, want non-revealing not found", err)
		}

		var ownProgressCount, foreignPosition int
		if err := pool.QueryRow(ctx, `
			SELECT count(*)::int
			FROM profile_progress
			WHERE profile_id = $1::uuid AND title_id = $2::uuid
		`, profileTwoID, second.TitleID).Scan(&ownProgressCount); err != nil {
			t.Fatalf("count progress after rejected mixed batch: %v", err)
		}
		if err := pool.QueryRow(ctx, `
			SELECT position_seconds
			FROM profile_progress
			WHERE profile_id = $1::uuid AND title_id = $2::uuid
		`, profileTwoID, first.TitleID).Scan(&foreignPosition); err != nil {
			t.Fatalf("read legacy foreign progress after rejected writes: %v", err)
		}
		if ownProgressCount != 0 || foreignPosition != 90 {
			t.Fatalf("rejected mixed batch partially wrote state: own=%d foreignPosition=%d", ownProgressCount, foreignPosition)
		}

		foreignLibrary, err := service.Library(ctx, profileTwo, "movie", 1, 20)
		if err != nil {
			t.Fatalf("list library containing legacy foreign reference: %v", err)
		}
		if foreignLibrary.TotalResults != 0 || len(foreignLibrary.Items) != 0 {
			t.Fatalf("foreign scoped metadata leaked through library: %+v", foreignLibrary)
		}

		canonicalProgress, err := service.UpdateProgress(ctx, profileTwo, canonicalTitleID, UpdateProgressInput{
			PositionSeconds: 50, DurationSeconds: 500,
		})
		if err != nil {
			t.Fatalf("write shared canonical progress: %v", err)
		}
		if _, err := service.UpdateProgress(ctx, profileOne, canonicalTitleID, UpdateProgressInput{
			PositionSeconds: 75, DurationSeconds: 500,
		}); err != nil {
			t.Fatalf("second profile writes shared canonical progress: %v", err)
		}
		canonicalBatch, err := service.GetProgressBatch(ctx, profileTwo, []string{canonicalTitleID, second.TitleID})
		if err != nil || len(canonicalBatch.Items) != 2 || canonicalBatch.Items[0].Progress == nil ||
			canonicalBatch.Items[0].Progress.Version != canonicalProgress.Version || canonicalBatch.Items[1].Progress != nil {
			t.Fatalf("shared canonical batch progress unavailable: batch=%+v err=%v", canonicalBatch, err)
		}
		profileTwoContinue, err := service.ContinueWatching(ctx, profileTwo, 10)
		if err != nil {
			t.Fatalf("list continue watching after legacy foreign progress: %v", err)
		}
		if len(profileTwoContinue.Items) != 1 || profileTwoContinue.Items[0].TitleID != canonicalTitleID {
			t.Fatalf("continue watching disclosed foreign scoped metadata: %+v", profileTwoContinue)
		}
	})

	service.SetCanonicalProvider(&canonicalProviderStub{}, canonicalResolverFunc(
		func(context.Context, string, string, string) (string, error) {
			return "", errors.New("temporary canonical provider failure")
		},
	))
	if _, err := service.ResolveTitle(ctx, profileOne, ResolveTitleInput{
		MediaType: "movie", Provider: "imdb", ExternalID: "tt1234567",
		ResourceID: "temporary", Title: "Must Not Persist",
	}); err == nil {
		t.Fatal("temporary canonical provider failure silently fell back to a client snapshot")
	}
	var identitiesAfterFailure int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM profile_title_external_ids`).Scan(&identitiesAfterFailure); err != nil {
		t.Fatalf("count identities after canonical provider failure: %v", err)
	}
	if identitiesAfterFailure != scopedIdentityCount {
		t.Fatalf("temporary canonical provider failure created a fallback identity: before=%d after=%d", scopedIdentityCount, identitiesAfterFailure)
	}
}

type recordingTrackingSink struct {
	calls int
}

func (sink *recordingTrackingSink) EnqueueTx(context.Context, pgx.Tx, string, string, string, tracking.Event) error {
	sink.calls++
	return nil
}

type capacityTrackingSink struct{}

func (*capacityTrackingSink) EnqueueTx(context.Context, pgx.Tx, string, string, string, tracking.Event) error {
	return fmt.Errorf("enqueue failed: %w", tracking.ErrOutboxCapacity)
}

func TestTrackingOutboxCapacityIsExposedAsWatchstateError(t *testing.T) {
	service := NewService(nil, &capacityTrackingSink{})
	err := service.enqueueTrackingTx(
		context.Background(), nil, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "550e8400-e29b-41d4-a716-446655440000",
		"progress:capacity", tracking.Event{Type: "progress"},
	)
	if !errors.Is(err, ErrOutboxCapacity) {
		t.Fatalf("tracking capacity error=%v, want watchstate ErrOutboxCapacity", err)
	}
	if translated := watchstateTrackingError(fmt.Errorf("batch failed: %w", tracking.ErrOutboxCapacity)); !errors.Is(translated, ErrOutboxCapacity) {
		t.Fatalf("batch tracking capacity error=%v, want watchstate ErrOutboxCapacity", translated)
	}
}

func TestTVLibraryIsProfileScopedAndSurvivesAddonRemoval(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL TV library test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	counter := &progressBatchQueryCounter{}
	config.ConnConfig.Tracer = counter
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profiles (
			id uuid PRIMARY KEY,
			category_id uuid
		);
		CREATE TEMPORARY TABLE user_profile_access (
			user_id uuid NOT NULL,
			profile_id uuid NOT NULL,
			can_manage boolean NOT NULL DEFAULT false,
			PRIMARY KEY (user_id, profile_id)
		);
		INSERT INTO profiles (id) VALUES
			('11111111-1111-4111-8111-111111111111'),
			('22222222-2222-4222-8222-222222222222');
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			media_type text NOT NULL,
			display_title text,
			poster_url text,
			background_url text,
			release_info text,
			release_date date,
			resource_id text,
			resource_provider text,
			source_addon_id uuid,
			source_catalog_id text,
			source_name text,
			country text,
			language text,
			category text,
			is_current boolean NOT NULL DEFAULT true,
			updated_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE TEMPORARY TABLE title_external_ids (
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			provider text NOT NULL,
			namespace text NOT NULL,
			external_id text NOT NULL,
			PRIMARY KEY (provider, namespace, external_id)
		);
		CREATE TEMPORARY TABLE profile_title_external_ids (
			profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			provider text NOT NULL,
			namespace text NOT NULL,
			external_id text NOT NULL,
			PRIMARY KEY (profile_id, provider, namespace, external_id),
			UNIQUE (title_id)
		);
		CREATE TEMPORARY TABLE profile_addons (
			id uuid PRIMARY KEY,
			enabled boolean NOT NULL DEFAULT true
		);
		CREATE TEMPORARY TABLE addon_profile_access (
			addon_id uuid NOT NULL,
			profile_id uuid NOT NULL,
			PRIMARY KEY (addon_id, profile_id)
		);
		CREATE TEMPORARY TABLE profile_library (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			added_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
		CREATE TEMPORARY TABLE profile_progress (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL
		);
		INSERT INTO profile_addons (id, enabled) VALUES
			('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', true),
			('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', true),
			('cccccccc-cccc-4ccc-8ccc-cccccccccccc', false);
		INSERT INTO addon_profile_access (addon_id, profile_id) VALUES
			('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', '11111111-1111-4111-8111-111111111111'),
			('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', '22222222-2222-4222-8222-222222222222'),
			('cccccccc-cccc-4ccc-8ccc-cccccccccccc', '11111111-1111-4111-8111-111111111111'),
			('dddddddd-dddd-4ddd-8ddd-dddddddddddd', '11111111-1111-4111-8111-111111111111');
	`); err != nil {
		t.Fatalf("create TV library fixtures: %v", err)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	profileOneID := "11111111-1111-4111-8111-111111111111"
	profileTwoID := "22222222-2222-4222-8222-222222222222"
	profileOne := auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileOneID, ProfileGrantExpiresAt: &expiresAt}
	profileTwo := auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileTwoID, ProfileGrantExpiresAt: &expiresAt}
	trackingSink := &recordingTrackingSink{}
	service := NewService(pool, trackingSink)

	first, err := service.ResolveTitle(ctx, profileOne, ResolveTitleInput{
		MediaType: "tv", Provider: "addon", ExternalID: "https://stream.invalid/live.m3u8",
		ResourceID: "news", Title: "News", SourceAddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		SourceCatalogID: "live", SourceName: "Provider One", Country: "US", Language: "en", Category: "News",
	})
	if err != nil {
		t.Fatalf("resolve first TV channel: %v", err)
	}
	sameChannel, err := service.ResolveTitle(ctx, profileOne, ResolveTitleInput{
		MediaType: "tv", Provider: "addon", ResourceID: "news", Title: "News",
		SourceAddonID:   "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		SourceCatalogID: "regional", SourceName: "Provider One", Country: "US", Language: "en", Category: "News",
	})
	if err != nil {
		t.Fatalf("resolve same TV channel from another catalog: %v", err)
	}
	if sameChannel.TitleID != first.TitleID || sameChannel.ExternalID != first.ExternalID {
		t.Fatalf("source catalog context changed durable TV identity: first=%+v same=%+v", first, sameChannel)
	}
	second, err := service.ResolveTitle(ctx, profileTwo, ResolveTitleInput{
		MediaType: "tv", Provider: "addon", ResourceID: "news", Title: "News",
		SourceAddonID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", SourceCatalogID: "live",
		SourceName: "Provider Two", Country: "US", Language: "en", Category: "News",
	})
	if err != nil {
		t.Fatalf("resolve homonymous TV channel: %v", err)
	}
	if first.TitleID == second.TitleID || first.ExternalID == second.ExternalID {
		t.Fatalf("homonymous channels from distinct addons collided: first=%+v second=%+v", first, second)
	}
	if first.ExternalID == "https://stream.invalid/live.m3u8" {
		t.Fatal("TV resolution persisted the caller-provided stream URL as identity")
	}
	if _, err := service.ResolveTitle(ctx, profileTwo, ResolveTitleInput{
		MediaType: "tv", Provider: "addon", ResourceID: "news", Title: "News",
		SourceAddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected inaccessible profile addon to be hidden, got %v", err)
	}
	for _, sourceAddonID := range []string{
		"cccccccc-cccc-4ccc-8ccc-cccccccccccc", // installed but disabled
		"dddddddd-dddd-4ddd-8ddd-dddddddddddd", // access row without an installation
	} {
		if _, err := service.ResolveTitle(ctx, profileOne, ResolveTitleInput{
			MediaType: "tv", Provider: "addon", ResourceID: "hidden", Title: "Hidden",
			SourceAddonID: sourceAddonID,
		}); err != ErrNotFound {
			t.Fatalf("disabled or missing TV addon %s leaked installation state: %v", sourceAddonID, err)
		}
	}

	if _, err := service.AddLibrary(ctx, profileOne, first.TitleID); err != nil {
		t.Fatalf("add first profile TV library entry: %v", err)
	}
	if _, err := service.AddLibrary(ctx, profileTwo, second.TitleID); err != nil {
		t.Fatalf("add second profile TV library entry: %v", err)
	}
	if counter.tvAddonShareLock.Load() < 3 {
		t.Fatalf("TV resolution emitted %d enabled addon FOR SHARE queries, want at least 3", counter.tvAddonShareLock.Load())
	}
	if counter.titleAddonShareLock.Load() < 2 {
		t.Fatalf("TV library additions emitted %d enabled addon FOR SHARE queries, want at least 2", counter.titleAddonShareLock.Load())
	}
	firstMembership, err := service.TVLibraryMembership(ctx, profileOne, []TVLibraryIdentity{
		{SourceAddonID: second.SourceAddonID, ResourceID: second.ResourceID},
		{SourceAddonID: first.SourceAddonID, ResourceID: first.ResourceID},
		{SourceAddonID: first.SourceAddonID, ResourceID: first.ResourceID},
	})
	if err != nil {
		t.Fatalf("query first profile TV membership: %v", err)
	}
	if len(firstMembership.Items) != 1 || firstMembership.Items[0].TitleID != first.TitleID ||
		firstMembership.Items[0].SourceAddonID != first.SourceAddonID || firstMembership.Items[0].ResourceID != first.ResourceID {
		t.Fatalf("unexpected first profile TV membership: %+v", firstMembership)
	}
	secondMembership, err := service.TVLibraryMembership(ctx, profileTwo, []TVLibraryIdentity{
		{SourceAddonID: first.SourceAddonID, ResourceID: first.ResourceID},
		{SourceAddonID: second.SourceAddonID, ResourceID: second.ResourceID},
	})
	if err != nil || len(secondMembership.Items) != 1 || secondMembership.Items[0].TitleID != second.TitleID {
		t.Fatalf("unexpected second profile TV membership: result=%+v err=%v", secondMembership, err)
	}
	if _, err := service.TVLibraryMembership(ctx, auth.Principal{}, []TVLibraryIdentity{{
		SourceAddonID: first.SourceAddonID,
		ResourceID:    first.ResourceID,
	}}); !errors.Is(err, ErrProfileRequired) {
		t.Fatalf("expected membership to require the same active profile grant, got %v", err)
	}
	firstPage, err := service.Library(ctx, profileOne, "tv", 1, 20)
	if err != nil {
		t.Fatalf("list first profile TV library: %v", err)
	}
	secondPage, err := service.Library(ctx, profileTwo, "tv", 1, 20)
	if err != nil {
		t.Fatalf("list second profile TV library: %v", err)
	}
	if firstPage.TotalResults != 1 || len(firstPage.Items) != 1 || firstPage.Items[0].TitleID != first.TitleID || !firstPage.Items[0].Available {
		t.Fatalf("unexpected first profile TV library: %+v", firstPage)
	}
	if secondPage.TotalResults != 1 || len(secondPage.Items) != 1 || secondPage.Items[0].TitleID != second.TitleID {
		t.Fatalf("unexpected second profile TV library: %+v", secondPage)
	}
	if firstPage.Items[0].SourceAddonID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" ||
		firstPage.Items[0].SourceCatalogID != "regional" || firstPage.Items[0].SourceName != "Provider One" {
		t.Fatalf("TV source snapshots were not returned: %+v", firstPage.Items[0])
	}

	if _, err := pool.Exec(ctx, `
		UPDATE profile_addons
		SET enabled = false
		WHERE id = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
	`); err != nil {
		t.Fatalf("disable first profile addon: %v", err)
	}
	if _, err := service.ResolveTitle(ctx, profileOne, ResolveTitleInput{
		MediaType: "tv", Provider: "addon", ResourceID: "news", Title: "Refreshed News",
		SourceAddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	}); err != ErrNotFound {
		t.Fatalf("disabled TV addon refresh leaked installation state: %v", err)
	}
	if _, err := service.AddLibrary(ctx, profileOne, first.TitleID); err != ErrNotFound {
		t.Fatalf("disabled TV addon library addition leaked installation state: %v", err)
	}
	unavailablePage, err := service.Library(ctx, profileOne, "tv", 1, 20)
	if err != nil {
		t.Fatalf("list unavailable TV library entry: %v", err)
	}
	if unavailablePage.TotalResults != 1 || len(unavailablePage.Items) != 1 || unavailablePage.Items[0].Available {
		t.Fatalf("unavailable TV entry was removed or reported available: %+v", unavailablePage)
	}
	unavailableMembership, err := service.TVLibraryMembership(ctx, profileOne, []TVLibraryIdentity{{
		SourceAddonID: first.SourceAddonID,
		ResourceID:    first.ResourceID,
	}})
	if err != nil || len(unavailableMembership.Items) != 1 || unavailableMembership.Items[0].TitleID != first.TitleID {
		t.Fatalf("unavailable saved TV entry lost membership: result=%+v err=%v", unavailableMembership, err)
	}
	if err := service.RemoveLibrary(ctx, profileOne, first.TitleID); err != nil {
		t.Fatalf("remove unavailable TV library entry: %v", err)
	}
	emptyPage, err := service.Library(ctx, profileOne, "tv", 1, 20)
	if err != nil || emptyPage.TotalResults != 0 || len(emptyPage.Items) != 0 {
		t.Fatalf("removed TV entry remained in profile library: page=%+v err=%v", emptyPage, err)
	}
	removedMembership, err := service.TVLibraryMembership(ctx, profileOne, []TVLibraryIdentity{{
		SourceAddonID: first.SourceAddonID,
		ResourceID:    first.ResourceID,
	}})
	if err != nil || len(removedMembership.Items) != 0 {
		t.Fatalf("removed TV entry remained a member: result=%+v err=%v", removedMembership, err)
	}
	if trackingSink.calls != 0 {
		t.Fatalf("TV library mutations were sent to tracking integrations %d times", trackingSink.calls)
	}
}

func TestNextEpisodeQuerySkipsKnownFutureSeasonAfterCompletedSeason(t *testing.T) {
	query := strings.Join(strings.Fields(nextEpisodeQuery), " ")

	for _, clause := range []string{
		"progress.completed AND season.ordinal > 0",
		"(candidate_season.ordinal, candidate_episode.ordinal) > (latest.season_number, latest.episode_number)",
		"(candidate_season.release_date IS NULL OR candidate_season.release_date <= CURRENT_DATE)",
		"(candidate_episode.release_date IS NULL OR candidate_episode.release_date <= CURRENT_DATE)",
	} {
		if !strings.Contains(query, clause) {
			t.Fatalf("next-episode query is missing %q", clause)
		}
	}
}

func TestNextEpisodeQueryKeepsReleasedAndUnknownCandidatesDeterministic(t *testing.T) {
	query := strings.Join(strings.Fields(nextEpisodeQuery), " ")

	if count := strings.Count(query, "release_date IS NULL OR"); count != 2 {
		t.Fatalf("unknown season and episode release dates must remain eligible; found %d nullable predicates", count)
	}
	if count := strings.Count(query, "release_date <= CURRENT_DATE"); count != 2 {
		t.Fatalf("released seasons and episodes must remain eligible; found %d database-date predicates", count)
	}
	if !strings.Contains(query, "ORDER BY candidate_season.ordinal, candidate_episode.ordinal LIMIT 1") {
		t.Fatal("next released or unknown-date episode must be selected in season and episode order")
	}
}

func TestNextEpisodeItemsExcludeKnownFutureReleases(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL next-episode service test")
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY,
			media_type text NOT NULL,
			parent_id uuid,
			ordinal integer,
			release_date date,
			display_title text,
			poster_url text,
			background_url text,
			release_info text,
			resource_id text,
			resource_provider text,
			is_current boolean NOT NULL DEFAULT true,
			source_addon_id uuid
		);
		CREATE TEMPORARY TABLE profile_title_external_ids (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL,
			provider text NOT NULL,
			namespace text NOT NULL,
			external_id text NOT NULL
		);
		CREATE TEMPORARY TABLE profile_addons (
			id uuid PRIMARY KEY,
			enabled boolean NOT NULL DEFAULT true
		);
		CREATE TEMPORARY TABLE addon_profile_access (
			addon_id uuid NOT NULL,
			profile_id uuid NOT NULL
		)
	`); err != nil {
		t.Fatalf("create temporary titles: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profile_progress (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL,
			completed boolean NOT NULL,
			last_watched_at timestamptz NOT NULL
		)
	`); err != nil {
		t.Fatalf("create temporary profile progress: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profile_continue_dismissals (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL,
			dismissed_at timestamptz NOT NULL,
			PRIMARY KEY (profile_id, title_id)
		)
	`); err != nil {
		t.Fatalf("create temporary continue dismissals: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (
			id, media_type, parent_id, ordinal, release_date, display_title,
			resource_id, resource_provider
		) VALUES
			('00000000-0000-4000-8000-000000000100', 'series', NULL, NULL, NULL, 'Future Season Show', 'future-season-show', 'tmdb'),
			('00000000-0000-4000-8000-000000000110', 'season', '00000000-0000-4000-8000-000000000100', 11, CURRENT_DATE - 365, 'Season 11', NULL, NULL),
			('00000000-0000-4000-8000-000000000111', 'episode', '00000000-0000-4000-8000-000000000110', 10, CURRENT_DATE - 30, 'Episode 10', NULL, NULL),
			('00000000-0000-4000-8000-000000000120', 'season', '00000000-0000-4000-8000-000000000100', 12, CURRENT_DATE + 30, 'Season 12', NULL, NULL),
			('00000000-0000-4000-8000-000000000121', 'episode', '00000000-0000-4000-8000-000000000120', 1, NULL, 'Episode 1', NULL, NULL),

			('00000000-0000-4000-8000-000000000200', 'series', NULL, NULL, NULL, 'Released Show', 'released-show', 'tmdb'),
			('00000000-0000-4000-8000-000000000210', 'season', '00000000-0000-4000-8000-000000000200', 1, CURRENT_DATE - 90, 'Season 1', NULL, NULL),
			('00000000-0000-4000-8000-000000000211', 'episode', '00000000-0000-4000-8000-000000000210', 1, CURRENT_DATE - 30, 'Episode 1', NULL, NULL),
			('00000000-0000-4000-8000-000000000212', 'episode', '00000000-0000-4000-8000-000000000210', 2, CURRENT_DATE + 1, 'Episode 2', NULL, NULL),
			('00000000-0000-4000-8000-000000000213', 'episode', '00000000-0000-4000-8000-000000000210', 3, CURRENT_DATE, 'Episode 3', NULL, NULL),

			('00000000-0000-4000-8000-000000000300', 'series', NULL, NULL, NULL, 'Legacy Show', 'legacy-show', 'tmdb'),
			('00000000-0000-4000-8000-000000000310', 'season', '00000000-0000-4000-8000-000000000300', 1, NULL, 'Season 1', NULL, NULL),
			('00000000-0000-4000-8000-000000000311', 'episode', '00000000-0000-4000-8000-000000000310', 1, NULL, 'Episode 1', NULL, NULL),
			('00000000-0000-4000-8000-000000000312', 'episode', '00000000-0000-4000-8000-000000000310', 2, NULL, 'Episode 2', NULL, NULL),
			('00000000-0000-4000-8000-000000000313', 'episode', '00000000-0000-4000-8000-000000000310', 3, NULL, 'Episode 3', NULL, NULL)
	`); err != nil {
		t.Fatalf("seed temporary titles: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO profile_progress (profile_id, title_id, completed, last_watched_at) VALUES
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000111', true, '2026-07-03T00:00:00Z'),
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000211', true, '2026-07-02T00:00:00Z'),
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000311', true, '2026-07-01T00:00:00Z')
	`); err != nil {
		t.Fatalf("seed temporary progress: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin next-episode query: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	items, err := nextEpisodeItems(
		ctx,
		tx,
		"11111111-1111-4111-8111-111111111111",
		nil,
		10,
	)
	if err != nil {
		t.Fatalf("load next episodes: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected released and legacy candidates only, got %#v", items)
	}

	if items[0].SeriesID != "00000000-0000-4000-8000-000000000200" ||
		items[0].TitleID != "00000000-0000-4000-8000-000000000213" ||
		items[0].EpisodeNumber == nil || *items[0].EpisodeNumber != 3 ||
		items[0].Reason != "next_episode" {
		t.Fatalf("expected the first released candidate after the future episode, got %#v", items[0])
	}
	if items[1].SeriesID != "00000000-0000-4000-8000-000000000300" ||
		items[1].TitleID != "00000000-0000-4000-8000-000000000312" ||
		items[1].EpisodeNumber == nil || *items[1].EpisodeNumber != 2 ||
		items[1].Reason != "next_episode" {
		t.Fatalf("expected the first deterministic unknown-date candidate, got %#v", items[1])
	}
}

func TestDismissContinuePersistsUntilNewWatchActivity(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL continue dismissal test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profiles (
			id uuid PRIMARY KEY,
			category_id uuid
		);
		CREATE TEMPORARY TABLE user_profile_access (
			user_id uuid NOT NULL,
			profile_id uuid NOT NULL,
			can_manage boolean NOT NULL DEFAULT false,
			PRIMARY KEY (user_id, profile_id)
		);
		INSERT INTO profiles (id)
		VALUES ('11111111-1111-4111-8111-111111111111');
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY,
			media_type text NOT NULL,
			parent_id uuid,
			ordinal integer,
			release_date date,
			display_title text,
			poster_url text,
			background_url text,
			release_info text,
			resource_id text,
			resource_provider text,
			is_current boolean NOT NULL DEFAULT true,
			source_addon_id uuid
		);
		CREATE TEMPORARY TABLE profile_title_external_ids (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL,
			provider text NOT NULL,
			namespace text NOT NULL,
			external_id text NOT NULL
		);
		CREATE TEMPORARY TABLE profile_addons (
			id uuid PRIMARY KEY,
			enabled boolean NOT NULL DEFAULT true
		);
		CREATE TEMPORARY TABLE addon_profile_access (
			addon_id uuid NOT NULL,
			profile_id uuid NOT NULL
		);
		CREATE TEMPORARY TABLE profile_progress (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL,
			position_seconds integer NOT NULL,
			duration_seconds integer NOT NULL,
			completed boolean NOT NULL DEFAULT false,
			version bigint NOT NULL DEFAULT 1,
			last_watched_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
		CREATE TEMPORARY TABLE profile_continue_dismissals (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL,
			dismissed_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
		INSERT INTO titles (id, media_type, parent_id, ordinal, display_title, resource_id, resource_provider) VALUES
			('00000000-0000-4000-8000-000000000400', 'series', NULL, NULL, 'Series', 'series', 'tmdb'),
			('00000000-0000-4000-8000-000000000410', 'season', '00000000-0000-4000-8000-000000000400', 1, 'Season 1', NULL, NULL),
			('00000000-0000-4000-8000-000000000411', 'episode', '00000000-0000-4000-8000-000000000410', 1, 'Episode 1', NULL, NULL),
			('00000000-0000-4000-8000-000000000500', 'movie', NULL, NULL, 'Movie', 'movie', 'tmdb');
		INSERT INTO profile_progress (profile_id, title_id, position_seconds, duration_seconds) VALUES
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000411', 200, 1000),
			('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000500', 300, 1000);
	`); err != nil {
		t.Fatalf("seed continue dismissal state: %v", err)
	}

	profileID := "11111111-1111-4111-8111-111111111111"
	expiresAt := time.Now().UTC().Add(time.Hour)
	principal := auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt}
	service := NewService(pool)

	initial, err := service.ContinueWatching(ctx, principal, 10)
	if err != nil || len(initial.Items) != 2 {
		t.Fatalf("initial continue items = %#v, error %v", initial.Items, err)
	}
	if err := service.DismissContinue(ctx, principal, "00000000-0000-4000-8000-000000000411"); err != nil {
		t.Fatalf("dismiss episode series: %v", err)
	}
	afterEpisode, err := service.ContinueWatching(ctx, principal, 10)
	if err != nil || len(afterEpisode.Items) != 1 || afterEpisode.Items[0].MediaType != "movie" {
		t.Fatalf("continue items after episode dismissal = %#v, error %v", afterEpisode.Items, err)
	}
	if err := service.DismissContinue(ctx, principal, "00000000-0000-4000-8000-000000000500"); err != nil {
		t.Fatalf("dismiss movie: %v", err)
	}
	afterMovie, err := service.ContinueWatching(ctx, principal, 10)
	if err != nil || len(afterMovie.Items) != 0 {
		t.Fatalf("continue items after movie dismissal = %#v, error %v", afterMovie.Items, err)
	}
	if _, err := service.UpdateProgress(ctx, principal, "00000000-0000-4000-8000-000000000411", UpdateProgressInput{
		PositionSeconds: 250, DurationSeconds: 1000, ExpectedVersion: 1,
	}); err != nil {
		t.Fatalf("update dismissed episode progress: %v", err)
	}
	restored, err := service.ContinueWatching(ctx, principal, 10)
	if err != nil || len(restored.Items) != 1 || restored.Items[0].TitleID != "00000000-0000-4000-8000-000000000411" {
		t.Fatalf("restored continue items = %#v, error %v", restored.Items, err)
	}
}

func TestProgressBatchUsesOneLogicalQueryAndAtomicVersions(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL progress batch test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	counter := &progressBatchQueryCounter{}
	config.ConnConfig.Tracer = counter
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profiles (
			id uuid PRIMARY KEY,
			category_id uuid
		);
		CREATE TEMPORARY TABLE user_profile_access (
			user_id uuid NOT NULL,
			profile_id uuid NOT NULL,
			can_manage boolean NOT NULL DEFAULT false,
			PRIMARY KEY (user_id, profile_id)
		);
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY,
			media_type text NOT NULL,
			source_addon_id uuid,
			is_current boolean NOT NULL DEFAULT true
		);
		CREATE TEMPORARY TABLE profile_title_external_ids (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL,
			provider text NOT NULL,
			namespace text NOT NULL,
			external_id text NOT NULL
		);
		CREATE TEMPORARY TABLE profile_addons (
			id uuid PRIMARY KEY,
			enabled boolean NOT NULL DEFAULT true
		);
		CREATE TEMPORARY TABLE addon_profile_access (
			addon_id uuid NOT NULL,
			profile_id uuid NOT NULL
		);
		CREATE TEMPORARY TABLE profile_progress (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL,
			position_seconds integer NOT NULL,
			duration_seconds integer NOT NULL,
			completed boolean NOT NULL DEFAULT false,
			version bigint NOT NULL DEFAULT 1,
			last_watched_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
		CREATE TEMPORARY TABLE profile_continue_dismissals (
			profile_id uuid NOT NULL,
			title_id uuid NOT NULL,
			dismissed_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
		INSERT INTO profiles (id, category_id)
		VALUES ('11111111-1111-4111-8111-111111111111', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa');
		INSERT INTO titles (id, media_type) VALUES
			('00000000-0000-4000-8000-000000000411', 'episode'),
			('00000000-0000-4000-8000-000000000412', 'episode');
		INSERT INTO profile_progress (
			profile_id, title_id, position_seconds, duration_seconds, completed, version
		) VALUES (
			'11111111-1111-4111-8111-111111111111',
			'00000000-0000-4000-8000-000000000411',
			200, 1000, false, 4
		);
	`); err != nil {
		t.Fatalf("seed progress batch state: %v", err)
	}

	profileID := "11111111-1111-4111-8111-111111111111"
	titleIDs := []string{
		"00000000-0000-4000-8000-000000000411",
		"00000000-0000-4000-8000-000000000412",
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	principal := auth.Principal{
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt,
	}
	service := NewService(pool)

	readBefore := counter.read.Load()
	initial, err := service.GetProgressBatch(ctx, principal, titleIDs)
	if err != nil {
		t.Fatalf("read progress batch: %v", err)
	}
	if counter.read.Load()-readBefore != 1 {
		t.Fatalf("progress batch executed %d logical queries", counter.read.Load()-readBefore)
	}
	if len(initial.Items) != 2 || initial.Items[0].TitleID != titleIDs[0] || initial.Items[0].Progress == nil ||
		initial.Items[0].Progress.Version != 4 || initial.Items[1].TitleID != titleIDs[1] || initial.Items[1].Progress != nil {
		t.Fatalf("progress batch lost order or missing state: %#v", initial.Items)
	}

	otherCategoryID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	unauthorized := auth.Principal{
		UserID: "22222222-2222-4222-8222-222222222222", Role: "member",
		AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &otherCategoryID,
		ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt,
	}
	if _, err := service.GetProgressBatch(ctx, unauthorized, titleIDs); !errors.Is(err, ErrProfileRequired) {
		t.Fatalf("expected cross-profile batch refusal, got %v", err)
	}

	titleLockBefore := counter.titleAddonShareLock.Load()
	writeBefore := counter.write.Load()
	updated, err := service.SetWatchedBatch(ctx, principal, []SetWatchedBatchItem{
		{TitleID: titleIDs[0], Completed: true, ExpectedVersion: 4},
		{TitleID: titleIDs[1], Completed: true, ExpectedVersion: 0},
	})
	if err != nil {
		t.Fatalf("set watched batch: %v", err)
	}
	if counter.write.Load()-writeBefore != 1 {
		t.Fatalf("watched batch executed %d logical queries", counter.write.Load()-writeBefore)
	}
	if counter.titleAddonShareLock.Load() != titleLockBefore {
		t.Fatalf("provider-independent watched batch acquired %d addon locks", counter.titleAddonShareLock.Load()-titleLockBefore)
	}
	if len(updated.Items) != 2 || updated.Items[0].Progress == nil || updated.Items[0].Progress.Version != 5 ||
		updated.Items[1].Progress == nil || updated.Items[1].Progress.Version != 1 {
		t.Fatalf("watched batch lost version results: %#v", updated.Items)
	}

	if _, err := service.SetWatchedBatch(ctx, principal, []SetWatchedBatchItem{
		{TitleID: titleIDs[0], Completed: false, ExpectedVersion: 4},
		{TitleID: titleIDs[1], Completed: false, ExpectedVersion: 1},
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected atomic watched batch conflict, got %v", err)
	}
	afterConflict, err := service.GetProgressBatch(ctx, principal, titleIDs)
	if err != nil {
		t.Fatalf("read progress after conflict: %v", err)
	}
	if afterConflict.Items[0].Progress == nil || afterConflict.Items[0].Progress.Version != 5 || !afterConflict.Items[0].Progress.Completed ||
		afterConflict.Items[1].Progress == nil || afterConflict.Items[1].Progress.Version != 1 || !afterConflict.Items[1].Progress.Completed {
		t.Fatalf("conflicting batch partially mutated state: %#v", afterConflict.Items)
	}
}

func TestResolveCustomSeriesPreservesStableHierarchyProgressAndScope(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the custom series resolution test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	counter := &progressBatchQueryCounter{}
	config.ConnConfig.Tracer = counter
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profiles (id uuid PRIMARY KEY, category_id uuid);
		CREATE TEMPORARY TABLE user_profile_access (
			user_id uuid NOT NULL, profile_id uuid NOT NULL, can_manage boolean NOT NULL DEFAULT false,
			PRIMARY KEY (user_id, profile_id)
		);
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(), media_type text NOT NULL,
			parent_id uuid REFERENCES titles(id) ON DELETE CASCADE, ordinal integer,
			display_title text, poster_url text, background_url text, release_info text,
			release_date date, resource_id text, resource_provider text, source_addon_id uuid,
			is_current boolean NOT NULL DEFAULT true,
			created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE UNIQUE INDEX titles_parent_ordinal_unique
			ON titles (parent_id, media_type, ordinal) WHERE is_current;
		CREATE TEMPORARY TABLE profile_title_external_ids (
			profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
			title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			provider text NOT NULL, namespace text NOT NULL, external_id text NOT NULL,
			PRIMARY KEY (profile_id, provider, namespace, external_id), UNIQUE (title_id)
		);
		CREATE TEMPORARY TABLE profile_addons (
			id uuid PRIMARY KEY,
			enabled boolean NOT NULL DEFAULT true
		);
		CREATE TEMPORARY TABLE addon_profile_access (
			addon_id uuid NOT NULL, profile_id uuid NOT NULL, position integer NOT NULL DEFAULT 0,
			PRIMARY KEY (addon_id, profile_id)
		);
		CREATE TEMPORARY TABLE profile_progress (
			profile_id uuid NOT NULL, title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
			position_seconds integer NOT NULL, duration_seconds integer NOT NULL,
			completed boolean NOT NULL DEFAULT false, version bigint NOT NULL DEFAULT 1,
			last_watched_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
		CREATE TEMPORARY TABLE profile_continue_dismissals (
			profile_id uuid NOT NULL, title_id uuid NOT NULL, dismissed_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
		INSERT INTO profiles (id, category_id) VALUES
			('11111111-1111-4111-8111-111111111111', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc'),
			('22222222-2222-4222-8222-222222222222', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc');
		INSERT INTO user_profile_access (user_id, profile_id, can_manage) VALUES
			('dddddddd-dddd-4ddd-8ddd-dddddddddddd', '22222222-2222-4222-8222-222222222222', false);
		INSERT INTO profile_addons (id, enabled) VALUES
			('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', true),
			('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', true);
		INSERT INTO addon_profile_access (addon_id, profile_id, position) VALUES
			('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', '11111111-1111-4111-8111-111111111111', 0),
			('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', '22222222-2222-4222-8222-222222222222', 0),
			('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', '11111111-1111-4111-8111-111111111111', 1);
	`); err != nil {
		t.Fatalf("create custom series fixtures: %v", err)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	profileOneID := "11111111-1111-4111-8111-111111111111"
	profileTwoID := "22222222-2222-4222-8222-222222222222"
	categoryID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	profileOne := auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileOneID, ProfileGrantExpiresAt: &expiresAt}
	profileTwo := auth.Principal{UserID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", Role: "member", AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID, ActiveProfileID: &profileTwoID, ProfileGrantExpiresAt: &expiresAt}
	service := NewService(pool)
	input := ResolveCustomSeriesInput{
		SourceAddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", SourceType: "anime",
		Series: CustomSeriesSnapshot{ResourceID: "opaque:show", Title: "Custom Show"},
		Videos: []CustomVideoSnapshot{
			{ResourceID: "opaque:second", Title: "Second", SeasonNumber: 2, EpisodeNumber: 8},
			{ResourceID: "opaque:first", Title: "First", SeasonNumber: 1, EpisodeNumber: 3},
		},
	}
	first, err := service.ResolveCustomSeries(ctx, profileOne, input)
	if err != nil {
		t.Fatalf("resolve initial custom series: %v", err)
	}
	if len(first.Seasons) != 2 || first.Seasons[0].SeasonNumber != 1 || first.Seasons[1].SeasonNumber != 2 {
		t.Fatalf("seasons not returned ascending: %+v", first.Seasons)
	}
	if len(first.Videos) != 2 || first.Videos[0].ResourceID != "opaque:second" || first.Videos[1].ResourceID != "opaque:first" {
		t.Fatalf("videos not returned in request order: %+v", first.Videos)
	}
	progress, err := service.UpdateProgress(ctx, profileOne, first.Videos[0].TitleID, UpdateProgressInput{PositionSeconds: 42, DurationSeconds: 120})
	if err != nil || progress.Version != 1 {
		t.Fatalf("store custom episode progress: %+v error %v", progress, err)
	}
	if counter.titleAddonShareLock.Load() != 1 {
		t.Fatalf("addon progress update emitted %d enabled addon FOR SHARE queries, want 1", counter.titleAddonShareLock.Load())
	}
	var currentTitleCount, identityCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE title.resource_provider = 'addon' AND title.source_addon_id = $2::uuid AND title.is_current)::int,
		       count(identity.title_id)::int
		FROM titles title
		LEFT JOIN profile_title_external_ids identity
		  ON identity.title_id = title.id AND identity.profile_id = $1::uuid
		WHERE title.id = $3::uuid OR title.parent_id = $3::uuid
		   OR title.parent_id IN (SELECT id FROM titles WHERE parent_id = $3::uuid)
	`, profileOneID, input.SourceAddonID, first.Series.TitleID).Scan(&currentTitleCount, &identityCount); err != nil {
		t.Fatalf("query custom hierarchy persistence: %v", err)
	}
	if currentTitleCount != 5 || identityCount != 5 {
		t.Fatalf("expected five addon-scoped current titles and identities, got titles=%d identities=%d", currentTitleCount, identityCount)
	}
	if _, err := service.SetWatched(ctx, profileOne, first.Videos[1].TitleID, true, CompletionInput{}); err != nil {
		t.Fatalf("complete custom episode: %v", err)
	}
	if counter.titleAddonShareLock.Load() != 2 {
		t.Fatalf("addon watched update emitted %d enabled addon FOR SHARE queries, want 2", counter.titleAddonShareLock.Load())
	}
	continuePage, err := service.ContinueWatching(ctx, profileOne, 20)
	if err != nil {
		t.Fatalf("query continue watching for custom hierarchy: %v", err)
	}
	if len(continuePage.Items) != 0 {
		t.Fatalf("custom episode hierarchy leaked into continue watching: %+v", continuePage.Items)
	}

	withoutSecond := input
	withoutSecond.Videos = []CustomVideoSnapshot{input.Videos[1]}
	remaining, err := service.ResolveCustomSeries(ctx, profileOne, withoutSecond)
	if err != nil {
		t.Fatalf("resolve authoritative reduced hierarchy: %v", err)
	}
	if remaining.Videos[0].TitleID != first.Videos[1].TitleID {
		t.Fatalf("remaining episode identity changed: before=%+v after=%+v", first.Videos, remaining.Videos)
	}
	if _, err := service.GetProgress(ctx, profileOne, first.Videos[0].TitleID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inactive stale episode remained accessible: %v", err)
	}

	reactivated, err := service.ResolveCustomSeries(ctx, profileOne, input)
	if err != nil {
		t.Fatalf("reactivate custom hierarchy: %v", err)
	}
	if reactivated.Series.TitleID != first.Series.TitleID ||
		reactivated.Seasons[0].TitleID != first.Seasons[0].TitleID || reactivated.Seasons[1].TitleID != first.Seasons[1].TitleID ||
		reactivated.Videos[0].TitleID != first.Videos[0].TitleID || reactivated.Videos[1].TitleID != first.Videos[1].TitleID {
		t.Fatalf("custom hierarchy IDs changed after reactivation: first=%+v reactivated=%+v", first, reactivated)
	}
	restoredProgress, err := service.GetProgress(ctx, profileOne, reactivated.Videos[0].TitleID)
	if err != nil || restoredProgress.PositionSeconds != 42 || restoredProgress.Version != 1 {
		t.Fatalf("custom progress was not preserved: %+v error %v", restoredProgress, err)
	}
	emptyInput := input
	emptyInput.Videos = []CustomVideoSnapshot{}
	emptied, err := service.ResolveCustomSeries(ctx, profileOne, emptyInput)
	if err != nil || len(emptied.Seasons) != 0 || len(emptied.Videos) != 0 {
		t.Fatalf("resolve empty authoritative hierarchy: %+v error %v", emptied, err)
	}
	if _, err := service.GetProgress(ctx, profileOne, first.Videos[0].TitleID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty authoritative hierarchy left episode accessible: %v", err)
	}
	reactivated, err = service.ResolveCustomSeries(ctx, profileOne, input)
	if err != nil || reactivated.Videos[0].TitleID != first.Videos[0].TitleID {
		t.Fatalf("reactivate hierarchy after empty snapshot: %+v error %v", reactivated, err)
	}

	isolated, err := service.ResolveCustomSeries(ctx, profileTwo, input)
	if err != nil {
		t.Fatalf("resolve second profile hierarchy: %v", err)
	}
	if isolated.Series.TitleID == first.Series.TitleID || isolated.Videos[0].TitleID == first.Videos[0].TitleID {
		t.Fatalf("custom identities leaked across profiles: first=%+v second=%+v", first, isolated)
	}
	otherAddonInput := input
	otherAddonInput.SourceAddonID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	otherAddon, err := service.ResolveCustomSeries(ctx, profileOne, otherAddonInput)
	if err != nil {
		t.Fatalf("resolve second addon installation hierarchy: %v", err)
	}
	if otherAddon.Series.TitleID == first.Series.TitleID || otherAddon.Videos[0].TitleID == first.Videos[0].TitleID {
		t.Fatalf("custom identities leaked across addon installations: first=%+v second=%+v", first, otherAddon)
	}
	if _, err := pool.Exec(ctx, `UPDATE profile_addons SET enabled = false WHERE id = $1::uuid`, input.SourceAddonID); err != nil {
		t.Fatalf("disable custom series addon: %v", err)
	}
	if _, err := service.ResolveCustomSeries(ctx, profileOne, input); err != ErrNotFound {
		t.Fatalf("disabled custom series addon leaked installation state: %v", err)
	}
	if _, err := service.GetProgress(ctx, profileOne, first.Videos[0].TitleID); err != ErrNotFound {
		t.Fatalf("disabled addon title remained accessible or leaked installation state: %v", err)
	}
	if _, err := service.UpdateProgress(ctx, profileOne, first.Videos[0].TitleID, UpdateProgressInput{
		PositionSeconds: 84, DurationSeconds: 120, ExpectedVersion: 1,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled addon progress update error = %v, want ErrNotFound", err)
	}
	if _, err := service.SetWatched(ctx, profileOne, first.Videos[1].TitleID, false, CompletionInput{ExpectedVersion: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled addon watched update error = %v, want ErrNotFound", err)
	}
	if _, err := service.SetWatchedBatch(ctx, profileOne, []SetWatchedBatchItem{{
		TitleID: first.Videos[0].TitleID, Completed: true, ExpectedVersion: 1,
	}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled addon watched batch error = %v, want ErrNotFound", err)
	}
	var unchangedPosition int
	var unchangedVersion int64
	if err := pool.QueryRow(ctx, `
		SELECT position_seconds, version
		FROM profile_progress
		WHERE profile_id = $1::uuid AND title_id = $2::uuid
	`, profileOneID, first.Videos[0].TitleID).Scan(&unchangedPosition, &unchangedVersion); err != nil {
		t.Fatalf("query progress after disabled mutations: %v", err)
	}
	if unchangedPosition != 42 || unchangedVersion != 1 {
		t.Fatalf("disabled addon mutation changed progress to position=%d version=%d", unchangedPosition, unchangedVersion)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM profile_addons WHERE id = $1::uuid`, input.SourceAddonID); err != nil {
		t.Fatalf("remove custom series addon installation: %v", err)
	}
	if _, err := service.ResolveCustomSeries(ctx, profileOne, input); err != ErrNotFound {
		t.Fatalf("missing custom series addon differed from disabled addon: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO profile_addons (id, enabled) VALUES ($1::uuid, true)`, input.SourceAddonID); err != nil {
		t.Fatalf("restore custom series addon installation: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM addon_profile_access WHERE addon_id = $1::uuid AND profile_id = $2::uuid`, input.SourceAddonID, profileOneID); err != nil {
		t.Fatalf("revoke addon access: %v", err)
	}
	if _, err := service.ResolveCustomSeries(ctx, profileOne, input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected inaccessible addon resolution to be not found, got %v", err)
	}
	if _, err := service.GetProgress(ctx, profileOne, first.Videos[0].TitleID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected revoked addon title to be inaccessible, got %v", err)
	}
}
