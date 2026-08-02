package operations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/playback"
)

const (
	metadataRefreshTask = "metadata-refresh"
	claimLease          = 30 * time.Minute
)

var languagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z]{2})?$`)

type MetadataRefresher interface {
	RefreshMissing(context.Context, metadata.RefreshMissingOptions) (metadata.RefreshResult, error)
}

type MaintenanceCleaner interface {
	Cleanup(context.Context) error
}

type PlaybackMaintenance interface {
	RunHousekeeping(context.Context) (playback.PurgeResult, error)
	ResetCache(context.Context) (playback.PurgeResult, error)
}

type Service struct {
	pool                 *pgxpool.Pool
	metadata             MetadataRefresher
	auth                 MaintenanceCleaner
	playback             PlaybackMaintenance
	housekeepingInterval time.Duration
	logger               *slog.Logger
	now                  func() time.Time
	refreshMu            sync.Mutex
}

func NewService(
	pool *pgxpool.Pool,
	metadataService MetadataRefresher,
	authService MaintenanceCleaner,
	playbackService PlaybackMaintenance,
	housekeepingInterval time.Duration,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		pool: pool, metadata: metadataService, auth: authService, playback: playbackService,
		housekeepingInterval: housekeepingInterval, logger: logger,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (service *Service) Overview(ctx context.Context, principal auth.Principal) (OperationsOverview, error) {
	if err := requireAdministrator(principal); err != nil {
		return OperationsOverview{}, err
	}
	schedule, err := service.metadataRefreshSchedule(ctx)
	if err != nil {
		return OperationsOverview{}, err
	}
	cache, err := service.metadataCacheStatus(ctx, schedule.Language)
	if err != nil {
		return OperationsOverview{}, err
	}
	return OperationsOverview{
		MetadataCache: cache, MetadataRefresh: schedule,
		HousekeepingIntervalMinutes: int(service.housekeepingInterval / time.Minute),
	}, nil
}

func (service *Service) UpdateMetadataRefreshSchedule(
	ctx context.Context,
	principal auth.Principal,
	input MetadataRefreshScheduleInput,
) (MetadataRefreshSchedule, error) {
	if err := requireAdministrator(principal); err != nil {
		return MetadataRefreshSchedule{}, err
	}
	input.Language = normalizeLanguage(input.Language)
	if !validSchedule(input) {
		return MetadataRefreshSchedule{}, ErrInvalidInput
	}
	_, err := service.pool.Exec(ctx, `
		INSERT INTO operation_schedules (task, enabled, interval_hours, language, batch_size, next_run_at, updated_at)
		VALUES ($1, $2, $3::smallint, $4, $5::smallint,
		        CASE WHEN $2 THEN now() + make_interval(hours => $3::integer) ELSE NULL END,
		        now())
		ON CONFLICT (task) DO UPDATE
		SET enabled = EXCLUDED.enabled,
		    interval_hours = EXCLUDED.interval_hours,
		    language = EXCLUDED.language,
		    batch_size = EXCLUDED.batch_size,
		    next_run_at = CASE
		        WHEN EXCLUDED.enabled THEN now() + make_interval(hours => EXCLUDED.interval_hours::integer)
		        ELSE NULL
		    END,
		    updated_at = now()
	`, metadataRefreshTask, input.Enabled, input.IntervalHours, input.Language, input.BatchSize)
	if err != nil {
		return MetadataRefreshSchedule{}, fmt.Errorf("update metadata refresh schedule: %w", err)
	}
	return service.metadataRefreshSchedule(ctx)
}

func (service *Service) RunAction(ctx context.Context, principal auth.Principal, action OperationAction) (OperationRun, error) {
	if err := requireAdministrator(principal); err != nil {
		return OperationRun{}, err
	}
	if !validAction(action) {
		return OperationRun{}, ErrActionNotFound
	}
	run := OperationRun{Action: action, StartedAt: service.now(), Status: "succeeded"}
	var actionErr error
	switch action {
	case ActionFetchMissingMetadata:
		if !service.refreshMu.TryLock() {
			return OperationRun{}, ErrInProgress
		}
		result, err := service.runManualMetadataRefresh(ctx)
		service.refreshMu.Unlock()
		run.Result.Metadata = result
		run.Status = metadataRefreshStatus(result, err)
		actionErr = err
	case ActionRunHousekeeping:
		authErr := service.auth.Cleanup(ctx)
		result, playbackErr := service.playback.RunHousekeeping(ctx)
		run.Result.Playback = &result
		actionErr = errors.Join(authErr, playbackErr)
		if actionErr != nil {
			run.Status = "failed"
		}
	case ActionClearMetadataCache:
		command, err := service.pool.Exec(ctx, "DELETE FROM title_metadata")
		actionErr = err
		if err == nil {
			run.Result.MetadataCache = &MetadataCacheClearResult{EntriesDeleted: int(command.RowsAffected())}
		} else {
			run.Status = "failed"
		}
	case ActionClearStreamCache:
		result, err := service.playback.ResetCache(ctx)
		run.Result.Playback = &result
		actionErr = err
		if err != nil {
			run.Status = "failed"
		}
	}
	run.CompletedAt = service.now()
	if actionErr != nil {
		service.logger.Error("operation action failed", "action", action, "error", actionErr)
	}
	return run, nil
}

// RunScheduled durably claims and executes a due metadata refresh. A PostgreSQL
// advisory lock prevents another instance from executing the same task while
// this instance holds the durable claim.
func (service *Service) RunScheduled(ctx context.Context) error {
	if ctx.Err() != nil || !service.refreshMu.TryLock() {
		return ctx.Err()
	}
	defer service.refreshMu.Unlock()
	connection, err := service.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire scheduled operation connection: %w", err)
	}
	defer connection.Release()
	var locked bool
	if err := connection.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtext($1))", metadataRefreshTask).Scan(&locked); err != nil {
		return fmt.Errorf("lock scheduled metadata refresh: %w", err)
	}
	if !locked {
		return nil
	}
	defer func() {
		var unlocked bool
		if unlockErr := connection.QueryRow(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock(hashtext($1))", metadataRefreshTask).Scan(&unlocked); unlockErr != nil {
			service.logger.Error("unlock scheduled metadata refresh", "error", unlockErr)
		}
	}()

	token, err := newClaimToken()
	if err != nil {
		return err
	}
	var language string
	var batchSize int
	err = connection.QueryRow(ctx, `
		UPDATE operation_schedules
		SET claim_token = $2,
		    claim_expires_at = now() + $3::interval,
		    last_started_at = now(),
		    updated_at = now()
		WHERE task = $1
		  AND enabled
		  AND next_run_at <= now()
		  AND (claim_expires_at IS NULL OR claim_expires_at <= now())
		RETURNING language, batch_size
	`, metadataRefreshTask, token, intervalLiteral(claimLease)).Scan(&language, &batchSize)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim scheduled metadata refresh: %w", err)
	}

	result, refreshErr := service.metadata.RefreshMissing(ctx, metadata.RefreshMissingOptions{Language: language, BatchSize: batchSize})
	status := metadataRefreshStatus(&result, refreshErr)
	resultJSON, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return fmt.Errorf("encode scheduled metadata refresh result: %w", marshalErr)
	}
	_, completeErr := connection.Exec(context.WithoutCancel(ctx), `
		UPDATE operation_schedules
		SET next_run_at = CASE WHEN enabled THEN now() + make_interval(hours => interval_hours) ELSE NULL END,
		    claim_token = NULL,
		    claim_expires_at = NULL,
		    last_completed_at = now(),
		    last_status = $3,
		    last_result = $4::jsonb,
		    updated_at = now()
		WHERE task = $1 AND claim_token = $2
	`, metadataRefreshTask, token, status, resultJSON)
	if completeErr != nil {
		return fmt.Errorf("complete scheduled metadata refresh: %w", completeErr)
	}
	return refreshErr
}

func (service *Service) runManualMetadataRefresh(ctx context.Context) (*MetadataRefreshResult, error) {
	schedule, err := service.metadataRefreshSchedule(ctx)
	if err != nil {
		return nil, err
	}
	startedAt := service.now()
	if _, err := service.pool.Exec(ctx, `
		UPDATE operation_schedules SET last_started_at = $2, updated_at = now() WHERE task = $1
	`, metadataRefreshTask, startedAt); err != nil {
		return nil, fmt.Errorf("mark metadata refresh started: %w", err)
	}
	result, refreshErr := service.metadata.RefreshMissing(ctx, metadata.RefreshMissingOptions{
		Language: schedule.Language, BatchSize: schedule.BatchSize,
	})
	status := metadataRefreshStatus(&result, refreshErr)
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return &result, fmt.Errorf("encode metadata refresh result: %w", err)
	}
	if _, err := service.pool.Exec(context.WithoutCancel(ctx), `
		UPDATE operation_schedules
		SET last_completed_at = $2, last_status = $3, last_result = $4::jsonb, updated_at = now()
		WHERE task = $1
	`, metadataRefreshTask, service.now(), status, resultJSON); err != nil {
		return &result, fmt.Errorf("complete metadata refresh: %w", err)
	}
	return &result, refreshErr
}

func (service *Service) metadataCacheStatus(ctx context.Context, language string) (MetadataCacheStatus, error) {
	var status MetadataCacheStatus
	err := service.pool.QueryRow(ctx, `
		WITH eligible AS (
			SELECT title.id
			FROM titles AS title
			WHERE title.parent_id IS NULL
			  AND title.media_type IN ('movie', 'series')
			  AND EXISTS (
			      SELECT 1
			      FROM title_external_ids AS identity
			      WHERE identity.title_id = title.id
			        AND identity.namespace = title.media_type
			  )
		)
		SELECT
			(SELECT count(*)::integer FROM title_metadata),
			(SELECT count(*)::integer FROM title_metadata WHERE expires_at > now()),
			(SELECT count(*)::integer FROM title_metadata WHERE expires_at <= now()),
			(SELECT count(*)::integer FROM eligible),
			(SELECT count(*)::integer
			 FROM eligible
			 WHERE NOT EXISTS (
			     SELECT 1
			     FROM title_metadata AS cached
			     WHERE cached.title_id = eligible.id
			       AND cached.provider = 'tmdb'
			       AND cached.language = $1
			       AND cached.expires_at > now()
			 )),
			(SELECT count(*)::integer
			 FROM eligible
			 JOIN titles ON titles.id = eligible.id
			 WHERE NULLIF(btrim(COALESCE(titles.poster_url, '')), '') IS NOT NULL
			    OR NULLIF(btrim(COALESCE(titles.background_url, '')), '') IS NOT NULL)
	`, language).Scan(
		&status.Entries, &status.FreshEntries, &status.ExpiredEntries,
		&status.RootTitles, &status.MissingTitles, &status.ArtworkSnapshots,
	)
	if err != nil {
		return MetadataCacheStatus{}, fmt.Errorf("query metadata cache status: %w", err)
	}
	return status, nil
}

func (service *Service) metadataRefreshSchedule(ctx context.Context) (MetadataRefreshSchedule, error) {
	var schedule MetadataRefreshSchedule
	var resultJSON []byte
	err := service.pool.QueryRow(ctx, `
		SELECT task, enabled, interval_hours, language, batch_size, next_run_at,
		       last_started_at, last_completed_at, last_status, last_result
		FROM operation_schedules
		WHERE task = $1
	`, metadataRefreshTask).Scan(
		&schedule.Task, &schedule.Enabled, &schedule.IntervalHours, &schedule.Language,
		&schedule.BatchSize, &schedule.NextRunAt, &schedule.LastStartedAt,
		&schedule.LastCompletedAt, &schedule.LastStatus, &resultJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return MetadataRefreshSchedule{}, fmt.Errorf("metadata refresh schedule is not initialized")
	}
	if err != nil {
		return MetadataRefreshSchedule{}, fmt.Errorf("query metadata refresh schedule: %w", err)
	}
	if len(resultJSON) > 0 {
		var result MetadataRefreshResult
		if err := json.Unmarshal(resultJSON, &result); err != nil {
			return MetadataRefreshSchedule{}, fmt.Errorf("decode metadata refresh result: %w", err)
		}
		schedule.LastResult = &result
	}
	return schedule, nil
}

func metadataRefreshStatus(result *MetadataRefreshResult, err error) string {
	if err != nil || result == nil {
		return "failed"
	}
	if result.Failed == 0 {
		return "succeeded"
	}
	if result.Refreshed == 0 {
		return "failed"
	}
	return "partial"
}

func requireAdministrator(principal auth.Principal) error {
	if principal.Role != "admin" {
		return ErrForbidden
	}
	return nil
}

func validSchedule(input MetadataRefreshScheduleInput) bool {
	validInterval := input.IntervalHours == 6 || input.IntervalHours == 12 || input.IntervalHours == 24 || input.IntervalHours == 168
	return validInterval && input.BatchSize >= 1 && input.BatchSize <= 100 && languagePattern.MatchString(input.Language)
}

func normalizeLanguage(language string) string {
	parts := strings.Split(strings.TrimSpace(language), "-")
	if len(parts) == 0 {
		return ""
	}
	parts[0] = strings.ToLower(parts[0])
	if len(parts) == 2 {
		parts[1] = strings.ToUpper(parts[1])
	}
	return strings.Join(parts, "-")
}

func validAction(action OperationAction) bool {
	switch action {
	case ActionFetchMissingMetadata, ActionRunHousekeeping, ActionClearMetadataCache, ActionClearStreamCache:
		return true
	default:
		return false
	}
}

func newClaimToken() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create metadata refresh claim: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func intervalLiteral(duration time.Duration) string {
	return fmt.Sprintf("%d microseconds", duration.Microseconds())
}
