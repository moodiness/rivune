package watchstate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

func TestRecommendationTitlePublishesOnlyClientMetadata(t *testing.T) {
	title := CatalogTitle{
		ID: "00000000-0000-4000-8000-000000000101", MediaType: "movie", Title: "Local title",
		PosterURL: "/api/v1/artwork/poster", BackgroundURL: "/api/v1/artwork/background",
		ReleaseInfo: "2026", ResourceID: "opaque-resource", ResourceProvider: "addon",
		SourceAddonID: "00000000-0000-4000-8000-000000000102", SourceCatalogID: "private-catalog",
		SourceName: "private-source", Overview: "not part of the compact recommendation contract",
		ProviderIDs: map[string]string{"imdb": "tt123"},
	}
	encoded, err := json.Marshal(recommendationTitle(title))
	if err != nil {
		t.Fatalf("marshal recommendation title: %v", err)
	}
	body := string(encoded)
	for _, expected := range []string{`"id"`, `"mediaType"`, `"resourceId"`, `"providerIds"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("recommendation projection omitted %s: %s", expected, body)
		}
	}
	for _, forbidden := range []string{"private-catalog", "private-source", "not part of"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("recommendation projection leaked %q: %s", forbidden, body)
		}
	}
}

func TestRecommendationArtworkShapeRequiresMatchingArtwork(t *testing.T) {
	title := CatalogTitle{PosterURL: "poster", BackgroundURL: "background"}
	if !recommendationHasArtwork(title, RecommendationArtworkPoster) || !recommendationHasArtwork(title, RecommendationArtworkLandscape) {
		t.Fatal("complete artwork was rejected")
	}
	if recommendationHasArtwork(CatalogTitle{PosterURL: "poster"}, RecommendationArtworkLandscape) {
		t.Fatal("poster-only title was accepted for landscape recommendations")
	}
	if recommendationHasArtwork(CatalogTitle{BackgroundURL: "background"}, RecommendationArtworkPoster) {
		t.Fatal("background-only title was accepted for poster recommendations")
	}
}

func TestRecommendationsFilterCandidatesByRequestedArtwork(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the recommendation artwork test")
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
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profiles (id uuid PRIMARY KEY, category_id uuid);
		CREATE TEMPORARY TABLE user_profile_access (user_id uuid NOT NULL, profile_id uuid NOT NULL, can_manage boolean NOT NULL DEFAULT false, PRIMARY KEY (user_id, profile_id));
		CREATE TEMPORARY TABLE profile_addons (id uuid PRIMARY KEY, enabled boolean NOT NULL DEFAULT true);
		CREATE TEMPORARY TABLE addon_profile_access (addon_id uuid NOT NULL, profile_id uuid NOT NULL, PRIMARY KEY (addon_id, profile_id));
		CREATE TEMPORARY TABLE addon_category_access (addon_id uuid NOT NULL, category_id uuid NOT NULL, PRIMARY KEY (addon_id, category_id));
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY, media_type text NOT NULL, parent_id uuid, ordinal integer,
			display_title text, poster_url text, background_url text, release_info text, release_date date,
			resource_id text, resource_provider text, source_addon_id uuid, source_catalog_id text, source_name text,
			country text, language text, category text, is_current boolean NOT NULL DEFAULT true,
			created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE TEMPORARY TABLE title_external_ids (title_id uuid NOT NULL, provider text NOT NULL, namespace text NOT NULL, external_id text NOT NULL, PRIMARY KEY (provider, namespace, external_id));
		CREATE TEMPORARY TABLE profile_title_external_ids (profile_id uuid NOT NULL, title_id uuid NOT NULL, provider text NOT NULL, namespace text NOT NULL, external_id text NOT NULL, PRIMARY KEY (profile_id, provider, namespace, external_id), UNIQUE (title_id));
		CREATE TEMPORARY TABLE profile_library (profile_id uuid NOT NULL, title_id uuid NOT NULL, added_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (profile_id, title_id));
		CREATE TEMPORARY TABLE profile_favorites (profile_id uuid NOT NULL, title_id uuid NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (profile_id, title_id));
		CREATE TEMPORARY TABLE profile_progress (profile_id uuid NOT NULL, title_id uuid NOT NULL, position_seconds integer NOT NULL, duration_seconds integer NOT NULL, completed boolean NOT NULL DEFAULT false, version bigint NOT NULL DEFAULT 1, last_watched_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (profile_id, title_id));
		CREATE TEMPORARY TABLE profile_user_data (
			profile_id uuid NOT NULL, title_id uuid NOT NULL, rating double precision, rating_set boolean NOT NULL DEFAULT false,
			played_percentage double precision, played_percentage_set boolean NOT NULL DEFAULT false,
			unplayed_item_count integer, unplayed_item_count_set boolean NOT NULL DEFAULT false,
			play_count integer, play_count_set boolean NOT NULL DEFAULT false, likes boolean, likes_set boolean NOT NULL DEFAULT false,
			last_played_date timestamptz, last_played_date_submicrosecond smallint, last_played_date_set boolean NOT NULL DEFAULT false,
			PRIMARY KEY (profile_id, title_id)
		);
		CREATE TEMPORARY TABLE title_metadata (title_id uuid NOT NULL, provider text NOT NULL, language text NOT NULL, payload jsonb NOT NULL, expires_at timestamptz NOT NULL, updated_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (title_id, provider, language));

		INSERT INTO profiles (id) VALUES ('11111111-1111-4111-8111-111111111111');
		INSERT INTO titles (id, media_type, display_title, poster_url, background_url, resource_id, resource_provider) VALUES
			('00000000-0000-4000-8000-000000000100', 'movie', 'Signal', '/signal-poster', '/signal-background', 'signal', 'tmdb'),
			('00000000-0000-4000-8000-000000000200', 'movie', 'Poster only', '/poster', NULL, 'poster', 'tmdb'),
			('00000000-0000-4000-8000-000000000300', 'movie', 'Landscape only', NULL, '/background', 'landscape', 'tmdb'),
			('00000000-0000-4000-8000-000000000400', 'movie', 'Both', '/both-poster', '/both-background', 'both', 'tmdb'),
			('00000000-0000-4000-8000-000000000500', 'movie', 'No artwork', NULL, NULL, 'none', 'tmdb');
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at)
		SELECT id, 'tmdb', 'en-US', '{"genres":[{"name":"Drama"}],"voteAverage":8}'::jsonb, now() + interval '1 hour' FROM titles;
		INSERT INTO profile_favorites (profile_id, title_id) VALUES ('11111111-1111-4111-8111-111111111111', '00000000-0000-4000-8000-000000000100');
	`); err != nil {
		t.Fatalf("create recommendation artwork fixtures: %v", err)
	}
	principal := captureActiveProfileTestSession(t, ctx, pool, auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator}, "11111111-1111-4111-8111-111111111111")
	service := NewService(pool, time.UTC)

	poster, err := service.Recommendations(ctx, principal, 10, RecommendationArtworkPoster)
	if err != nil {
		t.Fatalf("load poster recommendations: %v", err)
	}
	assertRecommendationIDs(t, poster, map[string]bool{
		"00000000-0000-4000-8000-000000000200": true,
		"00000000-0000-4000-8000-000000000400": true,
	})
	landscape, err := service.Recommendations(ctx, principal, 10, RecommendationArtworkLandscape)
	if err != nil {
		t.Fatalf("load landscape recommendations: %v", err)
	}
	assertRecommendationIDs(t, landscape, map[string]bool{
		"00000000-0000-4000-8000-000000000300": true,
		"00000000-0000-4000-8000-000000000400": true,
	})
}

func assertRecommendationIDs(t *testing.T, page RecommendationPage, expected map[string]bool) {
	t.Helper()
	actual := make(map[string]bool, len(page.Items))
	for _, item := range page.Items {
		actual[item.Item.ID] = true
	}
	if len(actual) != len(expected) {
		t.Fatalf("recommendation IDs = %v, want %v", actual, expected)
	}
	for id := range expected {
		if !actual[id] {
			t.Fatalf("recommendation IDs = %v, missing %s", actual, id)
		}
	}
}

func TestRecommendationsRejectInvalidInputsBeforeDatabase(t *testing.T) {
	service := &Service{}
	for _, limit := range []int{-1, MaximumRecommendationCount + 1} {
		if _, err := service.Recommendations(t.Context(), authPrincipalForRecommendationTest(), limit, RecommendationArtworkAny); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("limit %d returned %v", limit, err)
		}
	}
	if _, err := service.Recommendations(t.Context(), authPrincipalForRecommendationTest(), 20, "square"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid artwork shape returned %v", err)
	}
}

func authPrincipalForRecommendationTest() auth.Principal { return auth.Principal{} }
