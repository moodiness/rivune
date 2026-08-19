package watchstate

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/rivune/server/internal/auth"
)

const maximumContinueOffset = 1_000_000

// ListResume returns only resumable movies and episodes. Unlike
// ContinueWatching it does not mix in unwatched next episodes, which lets
// protocol adapters implement Jellyfin's separate Resume and NextUp feeds.
func (s *Service) ListResume(ctx context.Context, principal auth.Principal, offset, limit int) (ContinueItemsPage, error) {
	if err := validateContinueWindow(offset, limit); err != nil {
		return ContinueItemsPage{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return ContinueItemsPage{}, fmt.Errorf("begin resume items query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := disableTransactionJIT(ctx, tx); err != nil {
		return ContinueItemsPage{}, err
	}
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return ContinueItemsPage{}, err
	}

	var total int
	if err := tx.QueryRow(ctx, resumeItemsCTE+`SELECT count(*)::int FROM selected`, profileID).Scan(&total); err != nil {
		return ContinueItemsPage{}, fmt.Errorf("count resume items: %w", err)
	}
	items, err := queryResumeItems(ctx, tx, profileID, offset, limit)
	if err != nil {
		return ContinueItemsPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContinueItemsPage{}, fmt.Errorf("commit resume items query: %w", err)
	}
	return ContinueItemsPage{Items: items, Offset: offset, Limit: limit, Total: total}, nil
}

// ListNextUp returns the first eligible unwatched episode after each series'
// latest completed episode, plus the first episode of series the profile has
// never started. seriesID optionally narrows the feed after the same
func (s *Service) ListNextUp(ctx context.Context, principal auth.Principal, seriesID string, offset, limit int) (ContinueItemsPage, error) {
	if err := validateContinueWindow(offset, limit); err != nil {
		return ContinueItemsPage{}, err
	}
	seriesID = strings.TrimSpace(seriesID)
	if seriesID != "" {
		var err error
		seriesID, err = normalizeTitleID(seriesID)
		if err != nil {
			return ContinueItemsPage{}, err
		}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return ContinueItemsPage{}, fmt.Errorf("begin next-up items query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := disableTransactionJIT(ctx, tx); err != nil {
		return ContinueItemsPage{}, err
	}
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return ContinueItemsPage{}, err
	}
	var seriesSelector any
	if seriesID != "" {
		mediaType, err := accessibleTitleMediaType(ctx, tx, profileID, seriesID)
		if err != nil {
			return ContinueItemsPage{}, err
		}
		if mediaType != "series" {
			return ContinueItemsPage{}, fmt.Errorf("%w: next-up parent must be a series", ErrInvalidInput)
		}
		seriesSelector = seriesID
	}

	var total int
	if err := tx.QueryRow(ctx, nextUpItemsCTE+`SELECT count(*)::int FROM selected`, profileID, seriesSelector).Scan(&total); err != nil {
		return ContinueItemsPage{}, fmt.Errorf("count next-up items: %w", err)
	}
	items, err := queryNextUpItems(ctx, tx, profileID, seriesSelector, offset, limit)
	if err != nil {
		return ContinueItemsPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContinueItemsPage{}, fmt.Errorf("commit next-up items query: %w", err)
	}
	return ContinueItemsPage{Items: items, Offset: offset, Limit: limit, Total: total}, nil
}

func validateContinueWindow(offset, limit int) error {
	if offset < 0 || offset > maximumContinueOffset {
		return fmt.Errorf("%w: offset must be between 0 and %d", ErrInvalidInput, maximumContinueOffset)
	}
	if limit < 1 || limit > maximumPageSize {
		return fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, maximumPageSize)
	}
	return nil
}

const resumeItemsCTE = `
	WITH accessible_titles AS (` + accessibleTitlesSQL + `),
	resumable AS (
		SELECT title.id,
		       title.media_type,
		       series.id AS series_id,
		       season.id AS season_id,
		       season.ordinal AS season_number,
		       title.ordinal AS episode_number,
		       progress.position_seconds,
		       progress.duration_seconds,
		       progress.version,
		       progress.last_watched_at,
		       COALESCE(CASE WHEN title.media_type = 'episode' THEN series.display_title ELSE title.display_title END, '') AS display_title,
		       COALESCE(CASE WHEN title.media_type = 'episode' THEN series.poster_url ELSE title.poster_url END, '') AS poster_url,
		       COALESCE(CASE WHEN title.media_type = 'episode' THEN series.background_url ELSE title.background_url END, '') AS background_url,
		       COALESCE(CASE WHEN title.media_type = 'episode' THEN series.release_info ELSE title.release_info END, '') AS release_info,
		       CASE WHEN title.media_type = 'episode'
		           THEN COALESCE(series.resource_id, '') || ':' || season.ordinal::text || ':' || title.ordinal::text
		           ELSE COALESCE(title.resource_id, '')
		       END AS resource_id,
		       COALESCE(CASE WHEN title.media_type = 'episode' THEN series.resource_provider ELSE title.resource_provider END, '') AS resource_provider,
		       COALESCE(CASE WHEN title.media_type = 'episode' THEN title.display_title END, '') AS episode_title,
		       COALESCE(CASE WHEN title.media_type = 'episode' THEN NULLIF(title.poster_url, '') END, CASE WHEN title.media_type = 'episode' THEN NULLIF(series.background_url, '') END, '') AS episode_still_url,
		       COALESCE(CASE WHEN title.media_type = 'episode' THEN title.release_date::text END, '') AS episode_air_date,
		       row_number() OVER (
		           PARTITION BY CASE WHEN title.media_type = 'episode' THEN series.id ELSE title.id END
		           ORDER BY progress.last_watched_at DESC, title.id
		       ) AS series_rank
		FROM profile_progress progress
		JOIN titles title ON title.id = progress.title_id
		JOIN accessible_titles accessible_title ON accessible_title.id = title.id
		LEFT JOIN titles season
		  ON title.media_type = 'episode' AND season.id = title.parent_id
		LEFT JOIN accessible_titles accessible_season ON accessible_season.id = season.id
		LEFT JOIN titles series
		  ON season.media_type = 'season' AND series.id = season.parent_id
		LEFT JOIN accessible_titles accessible_series ON accessible_series.id = series.id
		WHERE progress.profile_id = $1::uuid
		  AND NOT progress.completed
		  AND progress.position_seconds > 0
		  AND (
		      title.media_type <> 'episode'
		      OR (accessible_season.id IS NOT NULL AND accessible_series.id IS NOT NULL AND series.source_addon_id IS NULL)
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM profile_continue_dismissals dismissal
		      WHERE dismissal.profile_id = progress.profile_id
		        AND dismissal.title_id = COALESCE(series.id, title.id)
		  )
	), selected AS (
		SELECT * FROM resumable WHERE series_rank = 1
	)
`

func queryResumeItems(ctx context.Context, tx pgx.Tx, profileID string, offset, limit int) ([]ContinueItem, error) {
	rows, err := tx.Query(ctx, resumeItemsCTE+`
		SELECT id::text, media_type, series_id::text, season_id::text,
		       season_number, episode_number, position_seconds, duration_seconds,
		       version, display_title, poster_url, background_url, release_info,
		       resource_id, resource_provider, episode_title, episode_still_url,
		       episode_air_date, last_watched_at
		FROM selected
		ORDER BY last_watched_at DESC, id
		LIMIT $2 OFFSET $3
	`, profileID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query resume items: %w", err)
	}
	defer rows.Close()
	items := make([]ContinueItem, 0, limit)
	for rows.Next() {
		var item ContinueItem
		var seriesID, seasonID *string
		if err := rows.Scan(
			&item.TitleID, &item.MediaType, &seriesID, &seasonID,
			&item.SeasonNumber, &item.EpisodeNumber, &item.PositionSeconds,
			&item.DurationSeconds, &item.Version, &item.Title, &item.PosterURL,
			&item.BackgroundURL, &item.ReleaseInfo, &item.ResourceID,
			&item.ResourceProvider, &item.EpisodeTitle, &item.EpisodeStillURL,
			&item.EpisodeAirDate, &item.LastWatchedAt,
		); err != nil {
			return nil, fmt.Errorf("scan resume item: %w", err)
		}
		if seriesID != nil {
			item.SeriesID = *seriesID
		}
		if seasonID != nil {
			item.SeasonID = *seasonID
		}
		item.Reason = "resume"
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resume items: %w", err)
	}
	return items, nil
}

const nextUpItemsCTE = `
	WITH accessible_titles AS (` + accessibleTitlesSQL + `),
	latest_completed AS (
		SELECT DISTINCT ON (series.id)
		       series.id AS series_id,
		       season.ordinal AS season_number,
		       episode.ordinal AS episode_number,
		       progress.last_watched_at
		FROM profile_progress progress
		JOIN titles episode ON episode.id = progress.title_id AND episode.media_type = 'episode'
		JOIN accessible_titles accessible_episode ON accessible_episode.id = episode.id
		JOIN titles season ON season.id = episode.parent_id AND season.media_type = 'season'
		JOIN accessible_titles accessible_season ON accessible_season.id = season.id
		JOIN titles series ON series.id = season.parent_id AND series.media_type = 'series'
		JOIN accessible_titles accessible_series ON accessible_series.id = series.id
		WHERE progress.profile_id = $1::uuid
		  AND progress.completed
		  AND season.ordinal > 0
		  AND series.source_addon_id IS NULL
		  AND ($2::uuid IS NULL OR series.id = $2::uuid)
		ORDER BY series.id, season.ordinal DESC, episode.ordinal DESC, progress.last_watched_at DESC, episode.id
	), eligible_series AS (
		SELECT series.id AS series_id,
		       latest.season_number,
		       latest.episode_number,
		       latest.last_watched_at
		FROM titles series
		JOIN accessible_titles accessible_series ON accessible_series.id = series.id
		LEFT JOIN latest_completed latest ON latest.series_id = series.id
		WHERE series.media_type = 'series'
		  AND series.source_addon_id IS NULL
		  AND ($2::uuid IS NULL OR series.id = $2::uuid)
		  AND NOT EXISTS (
		      SELECT 1 FROM profile_continue_dismissals dismissal
		      WHERE dismissal.profile_id = $1::uuid AND dismissal.title_id = series.id
		  )
		  AND (
		      latest.series_id IS NOT NULL
		      OR NOT EXISTS (
		          SELECT 1
		          FROM profile_progress existing
		          JOIN titles existing_episode
		            ON existing_episode.id = existing.title_id
		           AND existing_episode.media_type = 'episode'
		          JOIN titles existing_season
		            ON existing_season.id = existing_episode.parent_id
		           AND existing_season.media_type = 'season'
		          WHERE existing.profile_id = $1::uuid
		            AND existing_season.parent_id = series.id
		      )
		  )
	), selected AS (
		SELECT next_episode.id,
		       eligible.series_id,
		       next_season.id AS season_id,
		       next_season.ordinal AS season_number,
		       next_episode.ordinal AS episode_number,
		       COALESCE(series_title.display_title, '') AS display_title,
		       COALESCE(series_title.poster_url, '') AS poster_url,
		       COALESCE(series_title.background_url, '') AS background_url,
		       COALESCE(series_title.release_info, '') AS release_info,
		       COALESCE(series_title.resource_id, '') || ':' || next_season.ordinal::text || ':' || next_episode.ordinal::text AS resource_id,
		       COALESCE(series_title.resource_provider, '') AS resource_provider,
		       COALESCE(next_episode.display_title, '') AS episode_title,
		       COALESCE(NULLIF(next_episode.poster_url, ''), NULLIF(series_title.background_url, ''), '') AS episode_still_url,
		       COALESCE(next_episode.release_date::text, '') AS episode_air_date,
		       COALESCE(eligible.last_watched_at, '0001-01-01T00:00:00Z'::timestamptz) AS last_watched_at
		FROM eligible_series eligible
		JOIN LATERAL (
			SELECT candidate_episode.id, candidate_episode.parent_id, candidate_episode.ordinal,
			       candidate_episode.display_title, candidate_episode.poster_url,
			       candidate_episode.release_date
			FROM titles candidate_episode
			JOIN accessible_titles accessible_candidate_episode ON accessible_candidate_episode.id = candidate_episode.id
			JOIN titles candidate_season
			  ON candidate_season.id = candidate_episode.parent_id AND candidate_season.media_type = 'season'
			JOIN accessible_titles accessible_candidate_season ON accessible_candidate_season.id = candidate_season.id
			WHERE candidate_episode.media_type = 'episode'
			  AND candidate_season.parent_id = eligible.series_id
			  AND candidate_season.ordinal > 0
			  AND (
			      eligible.season_number IS NULL
			      OR (candidate_season.ordinal, candidate_episode.ordinal) > (eligible.season_number, eligible.episode_number)
			  )
			  AND (candidate_season.release_date IS NULL OR candidate_season.release_date <= CURRENT_DATE)
			  AND (candidate_episode.release_date IS NULL OR candidate_episode.release_date <= CURRENT_DATE)
			  AND NOT EXISTS (
			      SELECT 1 FROM profile_progress existing
			      WHERE existing.profile_id = $1::uuid
			        AND existing.title_id = candidate_episode.id
			        AND existing.completed
			  )
			ORDER BY candidate_season.ordinal, candidate_episode.ordinal
			LIMIT 1
		) next_episode ON true
		JOIN titles next_season ON next_season.id = next_episode.parent_id
		JOIN titles series_title ON series_title.id = eligible.series_id
	)
`

func queryNextUpItems(ctx context.Context, tx pgx.Tx, profileID string, seriesID any, offset, limit int) ([]ContinueItem, error) {
	rows, err := tx.Query(ctx, nextUpItemsCTE+`
		SELECT id::text, series_id::text, season_id::text, season_number,
		       episode_number, display_title, poster_url, background_url,
		       release_info, resource_id, resource_provider, episode_title,
		       episode_still_url, episode_air_date, last_watched_at
		FROM selected
		ORDER BY last_watched_at DESC NULLS LAST, series_id, season_number, episode_number, id
		LIMIT $3 OFFSET $4
	`, profileID, seriesID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query next-up items: %w", err)
	}
	defer rows.Close()
	items := make([]ContinueItem, 0, limit)
	for rows.Next() {
		var item ContinueItem
		if err := rows.Scan(
			&item.TitleID, &item.SeriesID, &item.SeasonID, &item.SeasonNumber,
			&item.EpisodeNumber, &item.Title, &item.PosterURL, &item.BackgroundURL,
			&item.ReleaseInfo, &item.ResourceID, &item.ResourceProvider,
			&item.EpisodeTitle, &item.EpisodeStillURL, &item.EpisodeAirDate,
			&item.LastWatchedAt,
		); err != nil {
			return nil, fmt.Errorf("scan next-up item: %w", err)
		}
		item.MediaType = "episode"
		item.Reason = "next_episode"
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate next-up items: %w", err)
	}
	return items, nil
}
