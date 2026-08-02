package watchstate

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/tracking"
)

var (
	ErrConflict         = errors.New("watch state version conflict")
	ErrInvalidInput     = errors.New("invalid watch state input")
	ErrNotFound         = errors.New("title or watch state not found")
	ErrProgressNotFound = errors.New("playback progress not found")
	ErrProfileRequired  = errors.New("active profile required")

	uuidPattern     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	providerPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
)

const (
	defaultPageSize = 20
	maximumPageSize = 100
)

type trackingSink interface {
	EnqueueTx(context.Context, pgx.Tx, string, string, string, tracking.Event) error
}

type Service struct {
	pool     *pgxpool.Pool
	tracking trackingSink
}

func NewService(pool *pgxpool.Pool, sinks ...trackingSink) *Service {
	service := &Service{pool: pool}
	if len(sinks) > 0 {
		service.tracking = sinks[0]
	}
	return service
}

func (s *Service) enqueueTrackingTx(ctx context.Context, tx pgx.Tx, profileID, titleID, key string, event tracking.Event) error {
	if s.tracking == nil {
		return nil
	}
	return s.tracking.EnqueueTx(ctx, tx, profileID, titleID, key, event)
}

func (s *Service) ResolveTitle(ctx context.Context, principal auth.Principal, input ResolveTitleInput) (TitleReference, error) {
	if _, err := activeProfileID(principal); err != nil {
		return TitleReference{}, err
	}
	input.MediaType = strings.ToLower(strings.TrimSpace(input.MediaType))
	input.Provider = strings.ToLower(strings.TrimSpace(input.Provider))
	input.ExternalID = strings.TrimSpace(input.ExternalID)
	input.ResourceID = strings.TrimSpace(input.ResourceID)
	input.Title = strings.TrimSpace(input.Title)
	input.PosterURL = strings.TrimSpace(input.PosterURL)
	input.BackgroundURL = strings.TrimSpace(input.BackgroundURL)
	input.ReleaseInfo = strings.TrimSpace(input.ReleaseInfo)
	input.Released = strings.TrimSpace(input.Released)
	if input.MediaType != "movie" && input.MediaType != "series" {
		return TitleReference{}, fmt.Errorf("%w: mediaType must be movie or series", ErrInvalidInput)
	}
	if !providerPattern.MatchString(input.Provider) {
		return TitleReference{}, fmt.Errorf("%w: invalid provider", ErrInvalidInput)
	}
	if len(input.ExternalID) < 1 || len(input.ExternalID) > 512 || len(input.ResourceID) < 1 || len(input.ResourceID) > 512 {
		return TitleReference{}, fmt.Errorf("%w: invalid external or resource identifier", ErrInvalidInput)
	}
	if len(input.Title) < 1 || len(input.Title) > 500 || len(input.PosterURL) > 4096 || len(input.BackgroundURL) > 4096 || len(input.ReleaseInfo) > 120 {
		return TitleReference{}, fmt.Errorf("%w: invalid title snapshot", ErrInvalidInput)
	}
	if input.MediaType == "movie" && input.Released != "" {
		released, err := time.Parse(time.DateOnly, input.Released)
		if err != nil || released.Year() < 1 || released.Format(time.DateOnly) != input.Released {
			return TitleReference{}, fmt.Errorf("%w: released must be a YYYY-MM-DD date", ErrInvalidInput)
		}
	}
	if input.MediaType == "series" {
		input.Released = ""
	}

	storedExternalID := input.ExternalID
	if len(storedExternalID) > 128 {
		storedExternalID = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(storedExternalID)))
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TitleReference{}, fmt.Errorf("begin title resolution: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockKey := input.Provider + ":" + input.MediaType + ":" + storedExternalID
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return TitleReference{}, fmt.Errorf("lock title resolution: %w", err)
	}

	var titleID string
	err = tx.QueryRow(ctx, `
		SELECT title_id::text
		FROM title_external_ids
		WHERE provider = $1 AND namespace = $2 AND external_id = $3
	`, input.Provider, input.MediaType, storedExternalID).Scan(&titleID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `
			INSERT INTO titles (
				media_type, display_title, poster_url, background_url, release_info,
				release_date, resource_id, resource_provider
			)
			VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''),
			        NULLIF($6, '')::date, $7, $8)
			RETURNING id::text
		`, input.MediaType, input.Title, input.PosterURL, input.BackgroundURL, input.ReleaseInfo,
			input.Released, input.ResourceID, input.Provider).Scan(&titleID); err != nil {
			return TitleReference{}, fmt.Errorf("create resolved title: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO title_external_ids (title_id, provider, external_id, namespace)
			VALUES ($1::uuid, $2, $3, $4)
		`, titleID, input.Provider, storedExternalID, input.MediaType); err != nil {
			return TitleReference{}, fmt.Errorf("store resolved title identity: %w", err)
		}
	} else if err != nil {
		return TitleReference{}, fmt.Errorf("find resolved title: %w", err)
	} else if _, err := tx.Exec(ctx, `
		UPDATE titles
		SET display_title = COALESCE(display_title, $2),
		    poster_url = COALESCE(poster_url, NULLIF($3, '')),
		    background_url = COALESCE(background_url, NULLIF($4, '')),
		    release_info = COALESCE(release_info, NULLIF($5, '')),
		    release_date = COALESCE(release_date, NULLIF($6, '')::date),
		    resource_id = $7,
		    resource_provider = $8,
		    updated_at = now()
		WHERE id = $1::uuid
	`, titleID, input.Title, input.PosterURL, input.BackgroundURL, input.ReleaseInfo,
		input.Released, input.ResourceID, input.Provider); err != nil {
		return TitleReference{}, fmt.Errorf("update resolved title snapshot: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return TitleReference{}, fmt.Errorf("commit title resolution: %w", err)
	}
	return TitleReference{
		TitleID: titleID, MediaType: input.MediaType, Provider: input.Provider,
		ExternalID: input.ExternalID, ResourceID: input.ResourceID, Title: input.Title,
		PosterURL: input.PosterURL, BackgroundURL: input.BackgroundURL, ReleaseInfo: input.ReleaseInfo,
	}, nil
}

func (s *Service) AddLibrary(ctx context.Context, principal auth.Principal, titleID string) (LibraryItem, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return LibraryItem{}, err
	}
	titleID, err = normalizeTitleID(titleID)
	if err != nil {
		return LibraryItem{}, err
	}
	mediaType, err := s.titleMediaType(ctx, titleID)
	if err != nil {
		return LibraryItem{}, err
	}
	if mediaType != "movie" && mediaType != "series" {
		return LibraryItem{}, fmt.Errorf("%w: library titles must be movies or series", ErrInvalidInput)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LibraryItem{}, fmt.Errorf("begin library addition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item := LibraryItem{TitleID: titleID, MediaType: mediaType}
	err = tx.QueryRow(ctx, `
		INSERT INTO profile_library (profile_id, title_id)
		VALUES ($1::uuid, $2::uuid)
		ON CONFLICT (profile_id, title_id) DO UPDATE SET updated_at = now()
		RETURNING added_at, updated_at
	`, profileID, titleID).Scan(&item.AddedAt, &item.UpdatedAt)
	if err != nil {
		return LibraryItem{}, fmt.Errorf("add library title: %w", err)
	}
	if err := s.enqueueTrackingTx(ctx, tx, profileID, titleID, fmt.Sprintf("library:add:%s:%d", titleID, item.UpdatedAt.UnixNano()), tracking.Event{
		Type: "library", TitleID: titleID, InLibrary: true, OccurredAt: item.UpdatedAt,
	}); err != nil {
		return LibraryItem{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LibraryItem{}, fmt.Errorf("commit library addition: %w", err)
	}
	return item, nil
}

func (s *Service) RemoveLibrary(ctx context.Context, principal auth.Principal, titleID string) error {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return err
	}
	titleID, err = normalizeTitleID(titleID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin library removal: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM profile_library WHERE profile_id = $1::uuid AND title_id = $2::uuid`, profileID, titleID); err != nil {
		return fmt.Errorf("remove library title: %w", err)
	}
	occurredAt := time.Now().UTC()
	if err := s.enqueueTrackingTx(ctx, tx, profileID, titleID, fmt.Sprintf("library:remove:%s:%d", titleID, occurredAt.UnixNano()), tracking.Event{
		Type: "library", TitleID: titleID, InLibrary: false, OccurredAt: occurredAt,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit library removal: %w", err)
	}
	return nil
}

func (s *Service) Library(ctx context.Context, principal auth.Principal, mediaType string, page, pageSize int) (LibraryPage, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return LibraryPage{}, err
	}
	mediaType, page, pageSize, err = normalizeLibraryQuery(mediaType, page, pageSize)
	if err != nil {
		return LibraryPage{}, err
	}

	var totalResults int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM profile_library library
		JOIN titles title ON title.id = library.title_id
		WHERE library.profile_id = $1::uuid AND ($2 = '' OR title.media_type = $2)
	`, profileID, mediaType).Scan(&totalResults); err != nil {
		return LibraryPage{}, fmt.Errorf("count library titles: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT title.id::text, title.media_type,
		       COALESCE(title.resource_provider, ''), COALESCE(identity.external_id, ''),
		       COALESCE(title.resource_id, ''), COALESCE(title.display_title, ''),
		       COALESCE(title.poster_url, ''), COALESCE(title.background_url, ''),
		       COALESCE(title.release_info, ''), library.added_at, library.updated_at
		FROM profile_library library
		JOIN titles title ON title.id = library.title_id
		LEFT JOIN title_external_ids identity
		  ON identity.title_id = title.id
		 AND identity.provider = title.resource_provider
		 AND identity.namespace = title.media_type
		WHERE library.profile_id = $1::uuid AND ($2 = '' OR title.media_type = $2)
		ORDER BY library.added_at DESC, title.id
		LIMIT $3 OFFSET $4
	`, profileID, mediaType, pageSize, (page-1)*pageSize)
	if err != nil {
		return LibraryPage{}, fmt.Errorf("query library titles: %w", err)
	}
	defer rows.Close()
	items := make([]LibraryItem, 0)
	for rows.Next() {
		var item LibraryItem
		if err := rows.Scan(
			&item.TitleID, &item.MediaType, &item.Provider, &item.ExternalID,
			&item.ResourceID, &item.Title, &item.PosterURL, &item.BackgroundURL,
			&item.ReleaseInfo, &item.AddedAt, &item.UpdatedAt,
		); err != nil {
			return LibraryPage{}, fmt.Errorf("scan library title: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return LibraryPage{}, fmt.Errorf("iterate library titles: %w", err)
	}
	totalPages := 0
	if totalResults > 0 {
		totalPages = (totalResults + pageSize - 1) / pageSize
	}
	return LibraryPage{Items: items, Page: page, TotalPages: totalPages, TotalResults: totalResults}, nil
}

func (s *Service) GetProgress(ctx context.Context, principal auth.Principal, titleID string) (Progress, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return Progress{}, err
	}
	titleID, err = normalizeTitleID(titleID)
	if err != nil {
		return Progress{}, err
	}
	progress, err := s.progress(ctx, profileID, titleID)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, titleErr := s.titleMediaType(ctx, titleID); titleErr != nil {
			return Progress{}, titleErr
		}
		return Progress{}, ErrProgressNotFound
	}
	return progress, err
}

func (s *Service) UpdateProgress(ctx context.Context, principal auth.Principal, titleID string, input UpdateProgressInput) (Progress, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return Progress{}, err
	}
	titleID, err = normalizeTitleID(titleID)
	if err != nil {
		return Progress{}, err
	}
	if err := validateProgressInput(input); err != nil {
		return Progress{}, err
	}
	mediaType, err := s.titleMediaType(ctx, titleID)
	if err != nil {
		return Progress{}, err
	}
	if mediaType != "movie" && mediaType != "episode" {
		return Progress{}, fmt.Errorf("%w: progress titles must be movies or episodes", ErrInvalidInput)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Progress{}, fmt.Errorf("begin playback progress update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var progress Progress
	if input.ExpectedVersion == 0 {
		err = scanProgress(tx.QueryRow(ctx, `
			INSERT INTO profile_progress (profile_id, title_id, position_seconds, duration_seconds, completed)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5)
			ON CONFLICT (profile_id, title_id) DO NOTHING
			RETURNING title_id::text, $6::text, position_seconds, duration_seconds,
			          completed, version, last_watched_at, updated_at
		`, profileID, titleID, input.PositionSeconds, input.DurationSeconds, input.Completed, mediaType), &progress)
	} else {
		err = scanProgress(tx.QueryRow(ctx, `
			UPDATE profile_progress
			SET position_seconds = $4, duration_seconds = $5, completed = $6,
			    version = version + 1, last_watched_at = now(), updated_at = now()
			WHERE profile_id = $1::uuid AND title_id = $2::uuid AND version = $3
			RETURNING title_id::text, $7::text, position_seconds, duration_seconds,
			          completed, version, last_watched_at, updated_at
		`, profileID, titleID, input.ExpectedVersion, input.PositionSeconds, input.DurationSeconds, input.Completed, mediaType), &progress)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Progress{}, ErrConflict
	}
	if err != nil {
		return Progress{}, fmt.Errorf("update playback progress: %w", err)
	}
	if err := s.enqueueTrackingTx(ctx, tx, profileID, titleID, fmt.Sprintf("progress:%s:%d", titleID, progress.Version), tracking.Event{
		Type: "progress", TitleID: titleID, Completed: progress.Completed,
		PositionSeconds: progress.PositionSeconds, DurationSeconds: progress.DurationSeconds,
		Version: progress.Version, OccurredAt: progress.UpdatedAt,
	}); err != nil {
		return Progress{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Progress{}, fmt.Errorf("commit playback progress update: %w", err)
	}
	return progress, nil
}

func (s *Service) SetWatched(ctx context.Context, principal auth.Principal, titleID string, completed bool, input CompletionInput) (Progress, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return Progress{}, err
	}
	titleID, err = normalizeTitleID(titleID)
	if err != nil {
		return Progress{}, err
	}
	if input.ExpectedVersion < 0 {
		return Progress{}, fmt.Errorf("%w: expectedVersion must be zero or greater", ErrInvalidInput)
	}
	mediaType, err := s.titleMediaType(ctx, titleID)
	if err != nil {
		return Progress{}, err
	}
	if mediaType != "movie" && mediaType != "episode" {
		return Progress{}, fmt.Errorf("%w: watched titles must be movies or episodes", ErrInvalidInput)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Progress{}, fmt.Errorf("begin watched state update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var progress Progress
	if input.ExpectedVersion == 0 {
		err = scanProgress(tx.QueryRow(ctx, `
			INSERT INTO profile_progress (profile_id, title_id, position_seconds, duration_seconds, completed)
			VALUES ($1::uuid, $2::uuid, 0, 0, $3)
			ON CONFLICT (profile_id, title_id) DO NOTHING
			RETURNING title_id::text, $4::text, position_seconds, duration_seconds,
			          completed, version, last_watched_at, updated_at
		`, profileID, titleID, completed, mediaType), &progress)
	} else {
		err = scanProgress(tx.QueryRow(ctx, `
			UPDATE profile_progress
			SET position_seconds = CASE WHEN $4 THEN duration_seconds ELSE 0 END,
			    completed = $4, version = version + 1, last_watched_at = now(), updated_at = now()
			WHERE profile_id = $1::uuid AND title_id = $2::uuid AND version = $3
			RETURNING title_id::text, $5::text, position_seconds, duration_seconds,
			          completed, version, last_watched_at, updated_at
		`, profileID, titleID, input.ExpectedVersion, completed, mediaType), &progress)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Progress{}, ErrConflict
	}
	if err != nil {
		return Progress{}, fmt.Errorf("set watched state: %w", err)
	}
	if err := s.enqueueTrackingTx(ctx, tx, profileID, titleID, fmt.Sprintf("watched:%s:%d:%t", titleID, progress.Version, completed), tracking.Event{
		Type: "watched", TitleID: titleID, Completed: completed,
		Version: progress.Version, OccurredAt: progress.UpdatedAt,
	}); err != nil {
		return Progress{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Progress{}, fmt.Errorf("commit watched state update: %w", err)
	}
	return progress, nil
}

func (s *Service) ClearProgress(ctx context.Context, principal auth.Principal, titleID string, expectedVersion int64) error {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return err
	}
	titleID, err = normalizeTitleID(titleID)
	if err != nil {
		return err
	}
	if expectedVersion < 0 {
		return fmt.Errorf("%w: expectedVersion must be zero or greater", ErrInvalidInput)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin playback progress clear: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		DELETE FROM profile_progress
		WHERE profile_id = $1::uuid AND title_id = $2::uuid AND version = $3
	`, profileID, titleID, expectedVersion)
	if err != nil {
		return fmt.Errorf("clear playback progress: %w", err)
	}
	if result.RowsAffected() == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM profile_progress WHERE profile_id = $1::uuid AND title_id = $2::uuid)`, profileID, titleID).Scan(&exists); err != nil {
			return fmt.Errorf("query playback progress after clear: %w", err)
		}
		if exists || expectedVersion != 0 {
			return ErrConflict
		}
	}
	occurredAt := time.Now().UTC()
	if err := s.enqueueTrackingTx(ctx, tx, profileID, titleID, fmt.Sprintf("progress:clear:%s:%d:%d", titleID, expectedVersion, occurredAt.UnixNano()), tracking.Event{
		Type: "progress", TitleID: titleID, Cleared: true, Version: expectedVersion, OccurredAt: occurredAt,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit playback progress clear: %w", err)
	}
	return nil
}

func (s *Service) ContinueWatching(ctx context.Context, principal auth.Principal, limit int) (ContinuePage, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return ContinuePage{}, err
	}
	if limit == 0 {
		limit = defaultPageSize
	}
	if limit < 1 || limit > maximumPageSize {
		return ContinuePage{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, maximumPageSize)
	}

	items, activeSeries, err := s.resumeItems(ctx, profileID, limit)
	if err != nil {
		return ContinuePage{}, err
	}
	if len(items) < limit {
		next, err := s.nextEpisodeItems(ctx, profileID, activeSeries, limit-len(items))
		if err != nil {
			return ContinuePage{}, err
		}
		items = append(items, next...)
	}
	sort.SliceStable(items, func(left, right int) bool {
		return items[left].LastWatchedAt.After(items[right].LastWatchedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return ContinuePage{Items: items}, nil
}

func (s *Service) resumeItems(ctx context.Context, profileID string, limit int) ([]ContinueItem, map[string]struct{}, error) {
	rows, err := s.pool.Query(ctx, `
		WITH resumable AS (
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
			       row_number() OVER (
			           PARTITION BY CASE
			               WHEN title.media_type = 'episode' THEN series.id
			               ELSE title.id
			           END
			           ORDER BY progress.last_watched_at DESC, title.id
			       ) AS series_rank
			FROM profile_progress progress
			JOIN titles title ON title.id = progress.title_id
			LEFT JOIN titles season
			  ON title.media_type = 'episode' AND season.id = title.parent_id
			LEFT JOIN titles series
			  ON season.media_type = 'season' AND series.id = season.parent_id
			WHERE progress.profile_id = $1::uuid
			  AND NOT progress.completed
			  AND progress.position_seconds > 0
		)
		SELECT id::text, media_type, series_id::text, season_id::text,
		       season_number, episode_number, position_seconds, duration_seconds,
		       version, display_title, poster_url, background_url, release_info,
		       resource_id, resource_provider, last_watched_at
		FROM resumable
		WHERE series_rank = 1
		ORDER BY last_watched_at DESC, id
		LIMIT $2
	`, profileID, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("query resumable titles: %w", err)
	}
	defer rows.Close()
	items := make([]ContinueItem, 0)
	activeSeries := make(map[string]struct{})
	for rows.Next() {
		var item ContinueItem
		var seriesID, seasonID *string
		if err := rows.Scan(
			&item.TitleID, &item.MediaType, &seriesID, &seasonID,
			&item.SeasonNumber, &item.EpisodeNumber, &item.PositionSeconds,
			&item.DurationSeconds, &item.Version, &item.Title, &item.PosterURL,
			&item.BackgroundURL, &item.ReleaseInfo, &item.ResourceID,
			&item.ResourceProvider, &item.LastWatchedAt,
		); err != nil {
			return nil, nil, fmt.Errorf("scan resumable title: %w", err)
		}
		if seriesID != nil {
			item.SeriesID = *seriesID
			activeSeries[*seriesID] = struct{}{}
		}
		if seasonID != nil {
			item.SeasonID = *seasonID
		}
		item.Reason = "resume"
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate resumable titles: %w", err)
	}
	return items, activeSeries, nil
}

const nextEpisodeQuery = `
		WITH latest_completed AS (
			SELECT DISTINCT ON (series.id)
			       series.id AS series_id,
			       season.ordinal AS season_number,
			       episode.ordinal AS episode_number,
			       progress.last_watched_at
			FROM profile_progress progress
			JOIN titles episode ON episode.id = progress.title_id AND episode.media_type = 'episode'
			JOIN titles season ON season.id = episode.parent_id AND season.media_type = 'season'
			JOIN titles series ON series.id = season.parent_id AND series.media_type = 'series'
			WHERE progress.profile_id = $1::uuid AND progress.completed AND season.ordinal > 0
			ORDER BY series.id, progress.last_watched_at DESC, episode.id
		)
		SELECT next_episode.id::text,
		       latest.series_id::text,
		       next_season.id::text,
		       next_season.ordinal,
		       next_episode.ordinal,
		       COALESCE(series_title.display_title, ''),
		       COALESCE(series_title.poster_url, ''),
		       COALESCE(series_title.background_url, ''),
		       COALESCE(series_title.release_info, ''),
		       COALESCE(series_title.resource_id, '') || ':' || next_season.ordinal::text || ':' || next_episode.ordinal::text,
		       COALESCE(series_title.resource_provider, ''),
		       latest.last_watched_at
		FROM latest_completed latest
		JOIN LATERAL (
			SELECT candidate_episode.id, candidate_episode.parent_id, candidate_episode.ordinal
			FROM titles candidate_episode
			JOIN titles candidate_season
			  ON candidate_season.id = candidate_episode.parent_id
			 AND candidate_season.media_type = 'season'
			WHERE candidate_episode.media_type = 'episode'
			  AND candidate_season.parent_id = latest.series_id
			  AND candidate_season.ordinal > 0
			  AND (candidate_season.ordinal, candidate_episode.ordinal) >
			      (latest.season_number, latest.episode_number)
			  AND (candidate_season.release_date IS NULL OR candidate_season.release_date <= CURRENT_DATE)
			  AND (candidate_episode.release_date IS NULL OR candidate_episode.release_date <= CURRENT_DATE)
			  AND NOT EXISTS (
				SELECT 1
				FROM profile_progress existing
				WHERE existing.profile_id = $1::uuid
				  AND existing.title_id = candidate_episode.id
				  AND existing.completed
			  )
			ORDER BY candidate_season.ordinal, candidate_episode.ordinal
			LIMIT 1
		) next_episode ON true
		JOIN titles next_season ON next_season.id = next_episode.parent_id
		JOIN titles series_title ON series_title.id = latest.series_id
		ORDER BY latest.last_watched_at DESC, latest.series_id
		LIMIT $2
	`

func (s *Service) nextEpisodeItems(ctx context.Context, profileID string, activeSeries map[string]struct{}, limit int) ([]ContinueItem, error) {
	if limit < 1 {
		return []ContinueItem{}, nil
	}
	rows, err := s.pool.Query(ctx, nextEpisodeQuery, profileID, limit+len(activeSeries))
	if err != nil {
		return nil, fmt.Errorf("query next episodes: %w", err)
	}
	defer rows.Close()
	items := make([]ContinueItem, 0)
	for rows.Next() {
		var item ContinueItem
		if err := rows.Scan(
			&item.TitleID, &item.SeriesID, &item.SeasonID, &item.SeasonNumber,
			&item.EpisodeNumber, &item.Title, &item.PosterURL, &item.BackgroundURL,
			&item.ReleaseInfo, &item.ResourceID, &item.ResourceProvider,
			&item.LastWatchedAt,
		); err != nil {
			return nil, fmt.Errorf("scan next episode: %w", err)
		}
		if _, exists := activeSeries[item.SeriesID]; exists {
			continue
		}
		item.MediaType = "episode"
		item.Reason = "next_episode"
		items = append(items, item)
		if len(items) == limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate next episodes: %w", err)
	}
	return items, nil
}

func (s *Service) progress(ctx context.Context, profileID, titleID string) (Progress, error) {
	var progress Progress
	err := scanProgress(s.pool.QueryRow(ctx, `
		SELECT progress.title_id::text, title.media_type,
		       progress.position_seconds, progress.duration_seconds,
		       progress.completed, progress.version,
		       progress.last_watched_at, progress.updated_at
		FROM profile_progress progress
		JOIN titles title ON title.id = progress.title_id
		WHERE progress.profile_id = $1::uuid AND progress.title_id = $2::uuid
	`, profileID, titleID), &progress)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Progress{}, err
		}
		return Progress{}, fmt.Errorf("query playback progress: %w", err)
	}
	return progress, nil
}

func (s *Service) titleMediaType(ctx context.Context, titleID string) (string, error) {
	var mediaType string
	if err := s.pool.QueryRow(ctx, "SELECT media_type FROM titles WHERE id = $1::uuid", titleID).Scan(&mediaType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("query title type: %w", err)
	}
	return mediaType, nil
}

func activeProfileID(principal auth.Principal) (string, error) {
	if principal.ActiveProfileID == nil || principal.ProfileGrantExpiresAt == nil || !principal.ProfileGrantExpiresAt.After(time.Now().UTC()) {
		return "", ErrProfileRequired
	}
	return *principal.ActiveProfileID, nil
}

func normalizeTitleID(titleID string) (string, error) {
	titleID = strings.TrimSpace(titleID)
	if !uuidPattern.MatchString(titleID) {
		return "", fmt.Errorf("%w: titleId must be a UUID", ErrInvalidInput)
	}
	return strings.ToLower(titleID), nil
}

func normalizeLibraryQuery(mediaType string, page, pageSize int) (string, int, int, error) {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != "" && mediaType != "movie" && mediaType != "series" {
		return "", 0, 0, fmt.Errorf("%w: mediaType must be movie or series", ErrInvalidInput)
	}
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = defaultPageSize
	}
	if page < 1 || pageSize < 1 || pageSize > maximumPageSize {
		return "", 0, 0, fmt.Errorf("%w: invalid pagination", ErrInvalidInput)
	}
	return mediaType, page, pageSize, nil
}

func validateProgressInput(input UpdateProgressInput) error {
	if input.ExpectedVersion < 0 {
		return fmt.Errorf("%w: expectedVersion must be zero or greater", ErrInvalidInput)
	}
	if input.PositionSeconds < 0 || input.DurationSeconds < 0 {
		return fmt.Errorf("%w: playback times must be zero or greater", ErrInvalidInput)
	}
	if input.DurationSeconds == 0 && input.PositionSeconds != 0 {
		return fmt.Errorf("%w: positionSeconds requires a duration", ErrInvalidInput)
	}
	if input.DurationSeconds > 0 && input.PositionSeconds > input.DurationSeconds {
		return fmt.Errorf("%w: positionSeconds cannot exceed durationSeconds", ErrInvalidInput)
	}
	return nil
}

type progressScanner interface {
	Scan(dest ...any) error
}

func scanProgress(scanner progressScanner, progress *Progress) error {
	return scanner.Scan(
		&progress.TitleID,
		&progress.MediaType,
		&progress.PositionSeconds,
		&progress.DurationSeconds,
		&progress.Completed,
		&progress.Version,
		&progress.LastWatchedAt,
		&progress.UpdatedAt,
	)
}
