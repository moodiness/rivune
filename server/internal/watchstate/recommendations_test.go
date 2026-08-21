package watchstate

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

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

func TestRecommendationsRejectOutOfRangeLimitBeforeDatabase(t *testing.T) {
	service := &Service{}
	for _, limit := range []int{-1, MaximumRecommendationCount + 1} {
		if _, err := service.Recommendations(t.Context(), authPrincipalForRecommendationTest(), limit); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("limit %d returned %v", limit, err)
		}
	}
}

func authPrincipalForRecommendationTest() auth.Principal { return auth.Principal{} }
