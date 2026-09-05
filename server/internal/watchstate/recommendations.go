package watchstate

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/moodiness/rivune/server/internal/auth"
)

type RecommendationTitle struct {
	ID               string            `json:"id"`
	MediaType        string            `json:"mediaType"`
	Title            string            `json:"title,omitempty"`
	PosterURL        string            `json:"posterUrl,omitempty"`
	BackgroundURL    string            `json:"backgroundUrl,omitempty"`
	ReleaseInfo      string            `json:"releaseInfo,omitempty"`
	ResourceID       string            `json:"resourceId,omitempty"`
	ResourceProvider string            `json:"resourceProvider,omitempty"`
	SourceAddonID    string            `json:"sourceAddonId,omitempty"`
	ProviderIDs      map[string]string `json:"providerIds"`
}

const MaximumRecommendationCount = 50

type RecommendationArtworkShape string

const (
	RecommendationArtworkAny       RecommendationArtworkShape = ""
	RecommendationArtworkPoster    RecommendationArtworkShape = "poster"
	RecommendationArtworkLandscape RecommendationArtworkShape = "landscape"
)

func (shape RecommendationArtworkShape) Valid() bool {
	return shape == RecommendationArtworkAny || shape == RecommendationArtworkPoster || shape == RecommendationArtworkLandscape
}

type Recommendation struct {
	Item   RecommendationTitle `json:"item"`
	Reason string              `json:"reason"`
	Score  float64             `json:"score"`
}

type RecommendationPage struct {
	Items []Recommendation `json:"items"`
}

type recommendationRank struct {
	titleID string
	reason  string
	score   float64
}

// Recommendations ranks only metadata already stored by this Rivune instance.
// A requested artwork shape excludes titles that cannot supply that shape.
// No profile signal or candidate title is sent to a recommendation service.
func (s *Service) Recommendations(ctx context.Context, principal auth.Principal, limit int, artworkShape RecommendationArtworkShape) (RecommendationPage, error) {
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > MaximumRecommendationCount {
		return RecommendationPage{}, ErrInvalidInput
	}
	if !artworkShape.Valid() {
		return RecommendationPage{}, ErrInvalidInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RecommendationPage{}, fmt.Errorf("begin recommendations query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return RecommendationPage{}, err
	}
	rows, err := tx.Query(ctx, `
		/* watchstate.local_recommendations */
		WITH accessible_titles AS (`+accessibleTitlesSQL+`),
		signal_titles AS MATERIALIZED (
			SELECT title.id,
			       CASE
			         WHEN favorite.title_id IS NOT NULL THEN 6.0
			         WHEN user_data.likes_set AND user_data.likes IS TRUE THEN 5.0
			         WHEN user_data.rating_set AND user_data.rating >= 7 THEN 4.0
			         WHEN progress.completed THEN 4.0
			         WHEN library.title_id IS NOT NULL THEN 2.0
			         ELSE 1.0
			       END AS weight,
			       metadata.payload
			FROM titles title
			JOIN accessible_titles accessible ON accessible.id = title.id
			LEFT JOIN profile_favorites favorite ON favorite.profile_id = $1::uuid AND favorite.title_id = title.id
			LEFT JOIN profile_library library ON library.profile_id = $1::uuid AND library.title_id = title.id
			LEFT JOIN profile_progress progress ON progress.profile_id = $1::uuid AND progress.title_id = title.id
			LEFT JOIN profile_user_data user_data ON user_data.profile_id = $1::uuid AND user_data.title_id = title.id
			JOIN LATERAL (
				SELECT payload FROM title_metadata
				WHERE title_id = title.id
				ORDER BY updated_at DESC, provider, language LIMIT 1
			) metadata ON true
			WHERE title.media_type IN ('movie', 'series')
			  AND title.parent_id IS NULL
			  AND title.hierarchy_variant = ''
			  AND (favorite.title_id IS NOT NULL OR library.title_id IS NOT NULL OR progress.title_id IS NOT NULL
			       OR (user_data.likes_set AND user_data.likes IS TRUE)
			       OR (user_data.rating_set AND user_data.rating >= 7))
		), affinity AS (
			SELECT lower(btrim(genre ->> 'name')) AS key,
			       min(btrim(genre ->> 'name')) AS label,
			       sum(signal.weight) AS weight
			FROM signal_titles signal
			CROSS JOIN LATERAL jsonb_array_elements(
				CASE WHEN jsonb_typeof(signal.payload -> 'genres') = 'array' THEN signal.payload -> 'genres' ELSE '[]'::jsonb END
			) genre
			WHERE jsonb_typeof(genre) = 'object' AND NULLIF(btrim(genre ->> 'name'), '') IS NOT NULL
			GROUP BY lower(btrim(genre ->> 'name'))
		), candidates AS MATERIALIZED (
			SELECT title.id, title.updated_at, metadata.payload
			FROM titles title
			JOIN accessible_titles accessible ON accessible.id = title.id
			JOIN LATERAL (
				SELECT payload FROM title_metadata
				WHERE title_id = title.id
				ORDER BY updated_at DESC, provider, language LIMIT 1
			) metadata ON true
			LEFT JOIN profile_progress progress ON progress.profile_id = $1::uuid AND progress.title_id = title.id
			WHERE title.media_type IN ('movie', 'series')
			  AND title.parent_id IS NULL
			  AND title.hierarchy_variant = ''
			  AND COALESCE(progress.completed, false) = false
			  AND NOT EXISTS (SELECT 1 FROM signal_titles signal WHERE signal.id = title.id)
			  AND ($3 = '' OR ($3 = 'poster' AND NULLIF(title.poster_url, '') IS NOT NULL)
			       OR ($3 = 'landscape' AND NULLIF(title.background_url, '') IS NOT NULL))
		), scored AS (
			SELECT candidate.id, candidate.updated_at,
			       COALESCE(sum(affinity.weight), 0.0)
			         + COALESCE(CASE
			             WHEN jsonb_typeof(candidate.payload -> 'voteAverage') = 'number'
			               THEN LEAST((candidate.payload ->> 'voteAverage')::double precision, 10.0) / 10.0
			             ELSE 0.0 END, 0.0) AS score,
			       (array_agg(affinity.label ORDER BY affinity.weight DESC, affinity.label)
			          FILTER (WHERE affinity.label IS NOT NULL))[1] AS strongest_genre
			FROM candidates candidate
			LEFT JOIN LATERAL jsonb_array_elements(
				CASE WHEN jsonb_typeof(candidate.payload -> 'genres') = 'array' THEN candidate.payload -> 'genres' ELSE '[]'::jsonb END
			) genre ON true
			LEFT JOIN affinity ON affinity.key = lower(btrim(genre ->> 'name'))
			GROUP BY candidate.id, candidate.updated_at, candidate.payload
		)
		SELECT id::text,
		       CASE WHEN strongest_genre IS NULL THEN 'Popular in your local catalog' ELSE 'Because you like ' || strongest_genre END,
		       score
		FROM scored
		ORDER BY score DESC, updated_at DESC, id
		LIMIT $2
	`, profileID, limit, artworkShape)
	if err != nil {
		return RecommendationPage{}, fmt.Errorf("query local recommendations: %w", err)
	}
	defer rows.Close()
	ranks := make([]recommendationRank, 0, limit)
	titleIDs := make([]string, 0, limit)
	for rows.Next() {
		var rank recommendationRank
		if err := rows.Scan(&rank.titleID, &rank.reason, &rank.score); err != nil {
			return RecommendationPage{}, fmt.Errorf("scan local recommendation: %w", err)
		}
		if math.IsNaN(rank.score) || math.IsInf(rank.score, 0) {
			return RecommendationPage{}, fmt.Errorf("invalid local recommendation score")
		}
		rank.reason = strings.TrimSpace(rank.reason)
		ranks = append(ranks, rank)
		titleIDs = append(titleIDs, rank.titleID)
	}
	if err := rows.Err(); err != nil {
		return RecommendationPage{}, fmt.Errorf("iterate local recommendations: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RecommendationPage{}, fmt.Errorf("commit local recommendations query: %w", err)
	}
	if len(titleIDs) == 0 {
		return RecommendationPage{Items: []Recommendation{}}, nil
	}
	titles, err := s.GetCatalogTitles(ctx, principal, titleIDs)
	if err != nil {
		return RecommendationPage{}, err
	}
	byID := make(map[string]CatalogTitle, len(titles))
	for _, title := range titles {
		byID[title.ID] = title
	}
	items := make([]Recommendation, 0, len(ranks))
	for _, rank := range ranks {
		if title, exists := byID[rank.titleID]; exists && recommendationHasArtwork(title, artworkShape) {
			items = append(items, Recommendation{Item: recommendationTitle(title), Reason: rank.reason, Score: rank.score})
		}
	}
	return RecommendationPage{Items: items}, nil
}

func recommendationHasArtwork(title CatalogTitle, shape RecommendationArtworkShape) bool {
	switch shape {
	case RecommendationArtworkPoster:
		return strings.TrimSpace(title.PosterURL) != ""
	case RecommendationArtworkLandscape:
		return strings.TrimSpace(title.BackgroundURL) != ""
	default:
		return true
	}
}

func recommendationTitle(title CatalogTitle) RecommendationTitle {
	return RecommendationTitle{
		ID: title.ID, MediaType: title.MediaType, Title: title.Title, PosterURL: title.PosterURL,
		BackgroundURL: title.BackgroundURL, ReleaseInfo: title.ReleaseInfo, ResourceID: title.ResourceID,
		ResourceProvider: title.ResourceProvider, SourceAddonID: title.SourceAddonID, ProviderIDs: title.ProviderIDs,
	}
}
