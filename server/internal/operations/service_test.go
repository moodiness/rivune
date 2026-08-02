package operations

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/playback"
)

func TestScheduleInputValidation(t *testing.T) {
	tests := []struct {
		name  string
		input MetadataRefreshScheduleInput
		valid bool
	}{
		{name: "six hours", input: MetadataRefreshScheduleInput{IntervalHours: 6, Language: "en", BatchSize: 1}, valid: true},
		{name: "twelve hours", input: MetadataRefreshScheduleInput{IntervalHours: 12, Language: "en-US", BatchSize: 50}, valid: true},
		{name: "one day", input: MetadataRefreshScheduleInput{IntervalHours: 24, Language: "eng", BatchSize: 100}, valid: true},
		{name: "one week", input: MetadataRefreshScheduleInput{IntervalHours: 168, Language: "fr-FR", BatchSize: 25}, valid: true},
		{name: "arbitrary interval", input: MetadataRefreshScheduleInput{IntervalHours: 48, Language: "en", BatchSize: 25}},
		{name: "zero interval", input: MetadataRefreshScheduleInput{IntervalHours: 0, Language: "en", BatchSize: 25}},
		{name: "empty language", input: MetadataRefreshScheduleInput{IntervalHours: 24, Language: "", BatchSize: 25}},
		{name: "language name", input: MetadataRefreshScheduleInput{IntervalHours: 24, Language: "english", BatchSize: 25}},
		{name: "underscore language", input: MetadataRefreshScheduleInput{IntervalHours: 24, Language: "en_US", BatchSize: 25}},
		{name: "long region", input: MetadataRefreshScheduleInput{IntervalHours: 24, Language: "en-USA", BatchSize: 25}},
		{name: "zero batch", input: MetadataRefreshScheduleInput{IntervalHours: 24, Language: "en", BatchSize: 0}},
		{name: "oversized batch", input: MetadataRefreshScheduleInput{IntervalHours: 24, Language: "en", BatchSize: 101}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validSchedule(test.input); got != test.valid {
				t.Fatalf("validSchedule(%+v) = %t, want %t", test.input, got, test.valid)
			}
		})
	}
}

func TestUpdateMetadataRefreshScheduleRoundTripsSupportedPostgresIntervals(t *testing.T) {
	pool := newOperationsPostgresPool(t, operationSchedulesTestDDL)
	service := newTestService(pool, &fakeMetadataRefresher{}, &fakeMaintenanceCleaner{}, &fakePlaybackMaintenance{})
	ctx := context.Background()

	for _, hours := range []int{6, 12, 24, 168} {
		t.Run((time.Duration(hours) * time.Hour).String(), func(t *testing.T) {
			input := MetadataRefreshScheduleInput{
				Enabled: true, IntervalHours: hours, Language: " EN-us ", BatchSize: 37,
			}
			schedule, err := service.UpdateMetadataRefreshSchedule(ctx, adminPrincipal(), input)
			if err != nil {
				t.Fatalf("update schedule with %d-hour interval: %v", hours, err)
			}
			if schedule.Task != metadataRefreshTask || !schedule.Enabled || schedule.IntervalHours != hours || schedule.Language != "en-US" || schedule.BatchSize != 37 {
				t.Fatalf("unexpected schedule round trip: %+v", schedule)
			}
			if schedule.NextRunAt == nil {
				t.Fatal("enabled schedule has no next run")
			}

			var persistedHours, persistedBatch int
			var persistedLanguage string
			var secondsUntilRun int64
			err = pool.QueryRow(ctx, `
				SELECT interval_hours::integer, language, batch_size::integer,
				       extract(epoch FROM next_run_at - updated_at)::bigint
				FROM operation_schedules WHERE task = $1
			`, metadataRefreshTask).Scan(&persistedHours, &persistedLanguage, &persistedBatch, &secondsUntilRun)
			if err != nil {
				t.Fatalf("query persisted schedule: %v", err)
			}
			if persistedHours != hours || persistedLanguage != "en-US" || persistedBatch != 37 || secondsUntilRun != int64(hours*3600) {
				t.Fatalf("unexpected persisted schedule: hours=%d language=%q batch=%d seconds=%d", persistedHours, persistedLanguage, persistedBatch, secondsUntilRun)
			}
		})
	}

	disabled, err := service.UpdateMetadataRefreshSchedule(ctx, adminPrincipal(), MetadataRefreshScheduleInput{
		Enabled: false, IntervalHours: 24, Language: "fr", BatchSize: 100,
	})
	if err != nil {
		t.Fatalf("disable schedule: %v", err)
	}
	if disabled.NextRunAt != nil {
		t.Fatalf("disabled schedule retained next run %s", disabled.NextRunAt)
	}
}

func TestManualMetadataRefreshPersistsStatusAndResult(t *testing.T) {
	pool := newOperationsPostgresPool(t, operationSchedulesTestDDL)
	seedMetadataRefreshSchedule(t, pool, "de-DE", 19)
	fixedNow := time.Date(2026, time.August, 2, 14, 30, 0, 0, time.UTC)
	providerFailure := errors.New("metadata provider unavailable")
	tests := []struct {
		name       string
		result     metadata.RefreshResult
		refreshErr error
		status     string
	}{
		{name: "succeeded", result: metadata.RefreshResult{Candidates: 3, Refreshed: 3}, status: "succeeded"},
		{name: "partial", result: metadata.RefreshResult{Candidates: 3, Refreshed: 2, Failed: 1}, status: "partial"},
		{name: "all candidates failed", result: metadata.RefreshResult{Candidates: 3, Failed: 3}, status: "failed"},
		{name: "provider error", result: metadata.RefreshResult{Candidates: 3, Refreshed: 1}, refreshErr: providerFailure, status: "failed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			refresher := &fakeMetadataRefresher{result: test.result, err: test.refreshErr}
			service := newTestService(pool, refresher, &fakeMaintenanceCleaner{}, &fakePlaybackMaintenance{})
			service.now = func() time.Time { return fixedNow }

			run, err := service.RunAction(context.Background(), adminPrincipal(), ActionFetchMissingMetadata)
			if err != nil {
				t.Fatalf("run manual metadata refresh: %v", err)
			}
			if run.Status != test.status || run.Result.Metadata == nil || *run.Result.Metadata != test.result {
				t.Fatalf("unexpected manual refresh run: %+v", run)
			}
			if run.StartedAt != fixedNow || run.CompletedAt != fixedNow {
				t.Fatalf("unexpected run timestamps: started=%s completed=%s", run.StartedAt, run.CompletedAt)
			}
			calls, options := refresher.snapshot()
			if calls != 1 || len(options) != 1 || options[0] != (metadata.RefreshMissingOptions{Language: "de-DE", BatchSize: 19}) {
				t.Fatalf("unexpected metadata refresh dispatch: calls=%d options=%+v", calls, options)
			}

			schedule, err := service.metadataRefreshSchedule(context.Background())
			if err != nil {
				t.Fatalf("reload metadata refresh schedule: %v", err)
			}
			if schedule.LastStartedAt == nil || !schedule.LastStartedAt.Equal(fixedNow) || schedule.LastCompletedAt == nil || !schedule.LastCompletedAt.Equal(fixedNow) {
				t.Fatalf("manual refresh timestamps were not persisted: %+v", schedule)
			}
			if schedule.LastStatus == nil || *schedule.LastStatus != test.status || schedule.LastResult == nil || *schedule.LastResult != test.result {
				t.Fatalf("manual refresh outcome was not persisted: %+v", schedule)
			}
		})
	}
}

func TestManualMetadataRefreshRejectsOverlap(t *testing.T) {
	pool := newOperationsPostgresPool(t, operationSchedulesTestDDL)
	seedMetadataRefreshSchedule(t, pool, "en", 20)
	refresher := newBlockingMetadataRefresher()
	service := newTestService(pool, refresher, &fakeMaintenanceCleaner{}, &fakePlaybackMaintenance{})
	ctx := context.Background()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(refresher.release) }) }
	t.Cleanup(release)

	firstDone := make(chan error, 1)
	go func() {
		_, err := service.RunAction(ctx, adminPrincipal(), ActionFetchMissingMetadata)
		firstDone <- err
	}()

	select {
	case <-refresher.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first metadata refresh did not start")
	}

	_, err := service.RunAction(ctx, adminPrincipal(), ActionFetchMissingMetadata)
	if !errors.Is(err, ErrInProgress) {
		t.Fatalf("overlapping metadata refresh returned %v, want %v", err, ErrInProgress)
	}
	if calls := refresher.callCount(); calls != 1 {
		t.Fatalf("metadata refresher executed %d times while first refresh was active", calls)
	}

	release()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first metadata refresh failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first metadata refresh did not finish after release")
	}
}

func TestClearMetadataCachePreservesTitlesIdentitiesAndArtworkSnapshots(t *testing.T) {
	pool := newOperationsPostgresPool(t, metadataCacheTestDDL)
	ctx := context.Background()
	const titleID = "00000000-0000-4000-8000-000000000901"
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, display_title, poster_url, background_url)
		VALUES ($1::uuid, 'movie', 'Snapshot Movie', 'https://images.test/poster.jpg', 'https://images.test/background.jpg')
	`, titleID); err != nil {
		t.Fatalf("seed title snapshot: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($1::uuid, 'tmdb', 'movie', '901'),
			($1::uuid, 'imdb', 'movie', 'tt0000901')
	`, titleID); err != nil {
		t.Fatalf("seed title identities: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at) VALUES
			($1::uuid, 'tmdb', 'en', '{"title":"Snapshot Movie"}', now() + interval '1 day'),
			($1::uuid, 'imdb', 'en', '{"rating":8}', now() + interval '1 day')
	`, titleID); err != nil {
		t.Fatalf("seed metadata cache entries: %v", err)
	}

	service := newTestService(pool, &fakeMetadataRefresher{}, &fakeMaintenanceCleaner{}, &fakePlaybackMaintenance{})
	run, err := service.RunAction(ctx, adminPrincipal(), ActionClearMetadataCache)
	if err != nil {
		t.Fatalf("clear metadata cache: %v", err)
	}
	if run.Status != "succeeded" || run.Result.MetadataCache == nil || run.Result.MetadataCache.EntriesDeleted != 2 {
		t.Fatalf("unexpected clear metadata result: %+v", run)
	}

	var titleCount, identityCount, metadataCount int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*)::integer FROM titles),
		       (SELECT count(*)::integer FROM title_external_ids),
		       (SELECT count(*)::integer FROM title_metadata)
	`).Scan(&titleCount, &identityCount, &metadataCount); err != nil {
		t.Fatalf("query cache clear scope: %v", err)
	}
	if titleCount != 1 || identityCount != 2 || metadataCount != 0 {
		t.Fatalf("unsafe cache clear scope: titles=%d identities=%d metadata=%d", titleCount, identityCount, metadataCount)
	}
	var displayTitle, posterURL, backgroundURL string
	if err := pool.QueryRow(ctx, `SELECT display_title, poster_url, background_url FROM titles WHERE id = $1::uuid`, titleID).Scan(&displayTitle, &posterURL, &backgroundURL); err != nil {
		t.Fatalf("reload title snapshot: %v", err)
	}
	if displayTitle != "Snapshot Movie" || posterURL != "https://images.test/poster.jpg" || backgroundURL != "https://images.test/background.jpg" {
		t.Fatalf("title snapshot changed: title=%q poster=%q background=%q", displayTitle, posterURL, backgroundURL)
	}
}

func TestRunActionDispatchesToSelectedServices(t *testing.T) {
	t.Run("housekeeping", func(t *testing.T) {
		metadataService := &fakeMetadataRefresher{}
		cleaner := &fakeMaintenanceCleaner{}
		playbackService := &fakePlaybackMaintenance{housekeepingResult: playback.PurgeResult{SessionsRemoved: 2, JobsStopped: 1, StorageBytes: 4096}}
		service := newTestService(nil, metadataService, cleaner, playbackService)

		run, err := service.RunAction(context.Background(), adminPrincipal(), ActionRunHousekeeping)
		if err != nil {
			t.Fatalf("run housekeeping: %v", err)
		}
		if run.Status != "succeeded" || run.Result.Playback == nil || *run.Result.Playback != playbackService.housekeepingResult {
			t.Fatalf("unexpected housekeeping result: %+v", run)
		}
		if cleaner.calls != 1 || playbackService.housekeepingCalls != 1 || playbackService.resetCalls != 0 {
			t.Fatalf("unexpected housekeeping dispatch: auth=%d housekeeping=%d reset=%d", cleaner.calls, playbackService.housekeepingCalls, playbackService.resetCalls)
		}
		if calls, _ := metadataService.snapshot(); calls != 0 {
			t.Fatalf("housekeeping dispatched %d metadata refreshes", calls)
		}
	})

	t.Run("stream cache reset", func(t *testing.T) {
		metadataService := &fakeMetadataRefresher{}
		cleaner := &fakeMaintenanceCleaner{}
		playbackService := &fakePlaybackMaintenance{resetResult: playback.PurgeResult{SessionsRemoved: 4, JobsStopped: 3, StorageBytes: 8192}}
		service := newTestService(nil, metadataService, cleaner, playbackService)

		run, err := service.RunAction(context.Background(), adminPrincipal(), ActionClearStreamCache)
		if err != nil {
			t.Fatalf("clear stream cache: %v", err)
		}
		if run.Status != "succeeded" || run.Result.Playback == nil || *run.Result.Playback != playbackService.resetResult {
			t.Fatalf("unexpected stream cache result: %+v", run)
		}
		if cleaner.calls != 0 || playbackService.housekeepingCalls != 0 || playbackService.resetCalls != 1 {
			t.Fatalf("unexpected stream reset dispatch: auth=%d housekeeping=%d reset=%d", cleaner.calls, playbackService.housekeepingCalls, playbackService.resetCalls)
		}
		if calls, _ := metadataService.snapshot(); calls != 0 {
			t.Fatalf("stream cache reset dispatched %d metadata refreshes", calls)
		}
	})

	t.Run("unknown action", func(t *testing.T) {
		metadataService := &fakeMetadataRefresher{}
		cleaner := &fakeMaintenanceCleaner{}
		playbackService := &fakePlaybackMaintenance{}
		service := newTestService(nil, metadataService, cleaner, playbackService)

		_, err := service.RunAction(context.Background(), adminPrincipal(), OperationAction("delete-everything"))
		if !errors.Is(err, ErrActionNotFound) {
			t.Fatalf("unknown action returned %v, want %v", err, ErrActionNotFound)
		}
		calls, _ := metadataService.snapshot()
		if calls != 0 || cleaner.calls != 0 || playbackService.housekeepingCalls != 0 || playbackService.resetCalls != 0 {
			t.Fatalf("unknown action reached a service: metadata=%d auth=%d housekeeping=%d reset=%d", calls, cleaner.calls, playbackService.housekeepingCalls, playbackService.resetCalls)
		}
	})
}

func adminPrincipal() auth.Principal {
	return auth.Principal{Role: "admin"}
}

func newTestService(pool *pgxpool.Pool, metadataService MetadataRefresher, cleaner MaintenanceCleaner, playbackService PlaybackMaintenance) *Service {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(pool, metadataService, cleaner, playbackService, 30*time.Minute, logger)
}

type fakeMetadataRefresher struct {
	mu      sync.Mutex
	result  metadata.RefreshResult
	err     error
	calls   int
	options []metadata.RefreshMissingOptions
}

func (fake *fakeMetadataRefresher) RefreshMissing(_ context.Context, options metadata.RefreshMissingOptions) (metadata.RefreshResult, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls++
	fake.options = append(fake.options, options)
	return fake.result, fake.err
}

func (fake *fakeMetadataRefresher) snapshot() (int, []metadata.RefreshMissingOptions) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.calls, append([]metadata.RefreshMissingOptions(nil), fake.options...)
}

type blockingMetadataRefresher struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func newBlockingMetadataRefresher() *blockingMetadataRefresher {
	return &blockingMetadataRefresher{started: make(chan struct{}), release: make(chan struct{})}
}

func (fake *blockingMetadataRefresher) RefreshMissing(_ context.Context, _ metadata.RefreshMissingOptions) (metadata.RefreshResult, error) {
	fake.mu.Lock()
	fake.calls++
	call := fake.calls
	fake.mu.Unlock()
	if call == 1 {
		close(fake.started)
		<-fake.release
	}
	return metadata.RefreshResult{Candidates: 1, Refreshed: 1}, nil
}

func (fake *blockingMetadataRefresher) callCount() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.calls
}

type fakeMaintenanceCleaner struct {
	calls int
	err   error
}

func (fake *fakeMaintenanceCleaner) Cleanup(context.Context) error {
	fake.calls++
	return fake.err
}

type fakePlaybackMaintenance struct {
	housekeepingCalls  int
	resetCalls         int
	housekeepingResult playback.PurgeResult
	housekeepingErr    error
	resetResult        playback.PurgeResult
	resetErr           error
}

func (fake *fakePlaybackMaintenance) RunHousekeeping(context.Context) (playback.PurgeResult, error) {
	fake.housekeepingCalls++
	return fake.housekeepingResult, fake.housekeepingErr
}

func (fake *fakePlaybackMaintenance) ResetCache(context.Context) (playback.PurgeResult, error) {
	fake.resetCalls++
	return fake.resetResult, fake.resetErr
}

func newOperationsPostgresPool(t *testing.T, ddl ...string) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run the PostgreSQL operations service test")
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
	t.Cleanup(pool.Close)
	for _, statement := range ddl {
		if _, err := pool.Exec(context.Background(), statement); err != nil {
			t.Fatalf("create temporary operations test tables: %v", err)
		}
	}
	return pool
}

func seedMetadataRefreshSchedule(t *testing.T, pool *pgxpool.Pool, language string, batchSize int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO operation_schedules (task, enabled, interval_hours, language, batch_size, next_run_at, updated_at)
		VALUES ($1, true, 24, $2, $3, now() + interval '1 day', now())
	`, metadataRefreshTask, language, batchSize); err != nil {
		t.Fatalf("seed metadata refresh schedule: %v", err)
	}
}

const operationSchedulesTestDDL = `
	CREATE TEMPORARY TABLE operation_schedules (
		task text PRIMARY KEY,
		enabled boolean NOT NULL,
		interval_hours smallint NOT NULL,
		language text NOT NULL,
		batch_size smallint NOT NULL,
		next_run_at timestamptz,
		last_started_at timestamptz,
		last_completed_at timestamptz,
		last_status text,
		last_result jsonb,
		claim_token text,
		claim_expires_at timestamptz,
		updated_at timestamptz NOT NULL DEFAULT now()
	)
`

const metadataCacheTestDDL = `
	CREATE TEMPORARY TABLE titles (
		id uuid PRIMARY KEY,
		media_type text NOT NULL,
		parent_id uuid,
		display_title text NOT NULL,
		poster_url text,
		background_url text
	);
	CREATE TEMPORARY TABLE title_external_ids (
		title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
		provider text NOT NULL,
		namespace text NOT NULL,
		external_id text NOT NULL,
		PRIMARY KEY (provider, namespace, external_id),
		UNIQUE (title_id, provider)
	);
	CREATE TEMPORARY TABLE title_metadata (
		title_id uuid NOT NULL,
		provider text NOT NULL,
		language text NOT NULL,
		payload jsonb NOT NULL,
		expires_at timestamptz NOT NULL,
		PRIMARY KEY (title_id, provider, language),
		FOREIGN KEY (title_id, provider) REFERENCES title_external_ids(title_id, provider) ON DELETE CASCADE
	)
`
