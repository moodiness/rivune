package medianotification

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/runtimesettings"
)

var (
	ErrActiveProfileRequired = errors.New("active profile required")
	ErrForbidden             = errors.New("notification subscription forbidden")
	ErrInvalidInput          = errors.New("invalid media notification input")
	ErrNotFound              = errors.New("media notification not found")
	ErrCapacity              = errors.New("media notification capacity exceeded")
)

const (
	DefaultHorizonDays = 30
	DefaultLeadDays    = 1
	MaximumHorizonDays = 366
	MaximumPageSize    = 100
	Retention          = 90 * 24 * time.Hour
	cleanupBatchSize          = 500
	maximumSubscriptions     = 4096
	mediaNotificationLockKey = int64(0x4d454449414e4f54)
	maximumSeriesSubjects    = 5000
)

type Kind string

const (
	KindCalendarEventUpcoming Kind = "calendar-event-upcoming"
	KindSeasonAvailable       Kind = "season-available"
	KindEpisodeAvailable      Kind = "episode-available"
	KindMovieRelease          Kind = "movie-release"
)

type Subscription struct {
	TitleID     string    `json:"titleId"`
	Timezone    string    `json:"timezone"`
	HorizonDays int       `json:"horizonDays"`
	LeadDays    int       `json:"leadDays"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type FollowInput struct {
	Timezone    string `json:"timezone,omitempty"`
	HorizonDays *int   `json:"horizonDays,omitempty"`
	LeadDays    *int   `json:"leadDays,omitempty"`
}

type Notification struct {
	ID             string     `json:"id"`
	Kind           Kind       `json:"kind"`
	TitleID        string     `json:"titleId"`
	SubjectTitleID *string    `json:"subjectTitleId,omitempty"`
	Title          string     `json:"title"`
	SeriesTitle    *string    `json:"seriesTitle,omitempty"`
	ReleaseDate    *string    `json:"releaseDate,omitempty"`
	SeasonNumber   *int       `json:"seasonNumber,omitempty"`
	EpisodeNumber  *int       `json:"episodeNumber,omitempty"`
	AvailableAt    time.Time  `json:"availableAt"`
	ReadAt         *time.Time `json:"readAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}

type Page struct {
	Notifications []Notification `json:"notifications"`
	NextCursor     string         `json:"nextCursor,omitempty"`
}

type AcknowledgementInput struct { State string `json:"state"` }

type Service struct {
	pool            *pgxpool.Pool
	runtimeSettings *runtimesettings.Source
	now             func() time.Time
}

func NewService(pool *pgxpool.Pool, runtimeSettings *runtimesettings.Source) *Service {
	return &Service{pool: pool, runtimeSettings: runtimeSettings, now: time.Now}
}

func (s *Service) ListSubscriptions(ctx context.Context, principal auth.Principal) ([]Subscription, error) {
	profileID, err := activeProfile(principal, s.now().UTC())
	if err != nil { return nil, err }
	tx, err := s.authorizedTx(ctx, principal, profileID, false)
	if err != nil { return nil, err }
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT title_id::text, timezone, horizon_days, lead_days, created_at, updated_at
		FROM profile_media_notification_subscriptions
		WHERE profile_id = $1::uuid ORDER BY title_id
		LIMIT $2
	`, profileID, maximumSubscriptions)
	if err != nil { return nil, fmt.Errorf("query notification subscriptions: %w", err) }
	defer rows.Close()
	result := make([]Subscription, 0)
	for rows.Next() {
		var item Subscription
		if err := rows.Scan(&item.TitleID, &item.Timezone, &item.HorizonDays, &item.LeadDays, &item.CreatedAt, &item.UpdatedAt); err != nil { return nil, fmt.Errorf("scan notification subscription: %w", err) }
		result = append(result, item)
	}
	if err := rows.Err(); err != nil { return nil, fmt.Errorf("iterate notification subscriptions: %w", err) }
	if err := tx.Commit(ctx); err != nil { return nil, fmt.Errorf("commit notification subscriptions: %w", err) }
	return result, nil
}

func (s *Service) Follow(ctx context.Context, principal auth.Principal, titleID string, input FollowInput) (Subscription, error) {
	profileID, err := activeProfile(principal, s.now().UTC())
	if err != nil { return Subscription{}, err }
	titleID = strings.TrimSpace(titleID)
	if titleID == "" { return Subscription{}, ErrInvalidInput }
	timezone := strings.TrimSpace(input.Timezone)
	if timezone == "" || timezone == "Local" || input.HorizonDays == nil || input.LeadDays == nil { return Subscription{}, ErrInvalidInput }
	if _, err := time.LoadLocation(timezone); err != nil { return Subscription{}, fmt.Errorf("%w: timezone", ErrInvalidInput) }
	horizon := *input.HorizonDays
	lead := *input.LeadDays
	if horizon < 1 || horizon > MaximumHorizonDays || lead < 0 || lead > 30 || lead > horizon { return Subscription{}, ErrInvalidInput }

	tx, err := s.authorizedTx(ctx, principal, profileID, false)
	if err != nil { return Subscription{}, err }
	defer func() { _ = tx.Rollback(ctx) }()
	var mediaType string
	if err := tx.QueryRow(ctx, `
		SELECT title.media_type FROM titles AS title
		JOIN profile_library AS library ON library.title_id = title.id AND library.profile_id = $2::uuid
		WHERE title.id = $1::uuid FOR SHARE OF title, library
	`, titleID, profileID).Scan(&mediaType); errors.Is(err, pgx.ErrNoRows) { return Subscription{}, ErrNotFound
	} else if err != nil { return Subscription{}, fmt.Errorf("load followed title: %w", err) }
	if mediaType != "movie" && mediaType != "series" { return Subscription{}, ErrInvalidInput }
	var result Subscription
	var inserted bool
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, profileID); err != nil {
		return Subscription{}, fmt.Errorf("lock notification subscription capacity: %w", err)
	}
	var subscriptionCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM profile_media_notification_subscriptions WHERE profile_id = $1::uuid`, profileID).Scan(&subscriptionCount); err != nil {
		return Subscription{}, fmt.Errorf("count notification subscriptions: %w", err)
	}
	if subscriptionCount >= maximumSubscriptions {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM profile_media_notification_subscriptions WHERE profile_id = $1::uuid AND title_id = $2::uuid)`, profileID, titleID).Scan(&exists); err != nil {
			return Subscription{}, fmt.Errorf("check notification subscription capacity: %w", err)
		}
		if !exists { return Subscription{}, ErrCapacity }
	}
	if mediaType == "series" {
		overflow, err := seriesSubjectOverflow(ctx, tx, titleID)
		if err != nil { return Subscription{}, err }
		if overflow { return Subscription{}, ErrCapacity }
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO profile_media_notification_subscriptions
			(profile_id, title_id, timezone, horizon_days, lead_days)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5)
		ON CONFLICT (profile_id, title_id) DO UPDATE SET
			timezone = EXCLUDED.timezone, horizon_days = EXCLUDED.horizon_days,
			lead_days = EXCLUDED.lead_days, updated_at = now()
		RETURNING title_id::text, timezone, horizon_days, lead_days, created_at, updated_at, (xmax = 0)
	`, profileID, titleID, timezone, horizon, lead).Scan(
		&result.TitleID, &result.Timezone, &result.HorizonDays, &result.LeadDays, &result.CreatedAt, &result.UpdatedAt, &inserted,
	); err != nil { return Subscription{}, fmt.Errorf("upsert notification subscription: %w", err) }
	if mediaType == "series" && inserted {
		if _, err := tx.Exec(ctx, `
			INSERT INTO profile_media_notification_observations
				(profile_id, root_title_id, subject_title_id, subject_kind, season_number, episode_number)
			SELECT $1::uuid AS profile_id, $2::uuid AS root_title_id,
			       season.id AS subject_title_id, 'season'::text AS subject_kind,
			       season.ordinal AS season_number, NULL::integer AS episode_number
			FROM titles AS season
			WHERE season.parent_id = $2::uuid AND season.media_type = 'season'
			UNION ALL
			SELECT $1::uuid, $2::uuid, episode.id, 'episode', season.ordinal, episode.ordinal
			FROM titles AS season
			JOIN titles AS episode ON episode.parent_id = season.id AND episode.media_type = 'episode'
			WHERE season.parent_id = $2::uuid AND season.media_type = 'season'
			ORDER BY subject_kind, season_number, episode_number, subject_title_id
			LIMIT $3
			ON CONFLICT DO NOTHING
		`, profileID, titleID, maximumSeriesSubjects); err != nil { return Subscription{}, fmt.Errorf("baseline notification subscription: %w", err) }
	}
	if err := s.generateOne(ctx, tx, s.now().UTC(), profileID, titleID); err != nil { return Subscription{}, err }
	if err := tx.Commit(ctx); err != nil { return Subscription{}, fmt.Errorf("commit notification subscription: %w", err) }
	return result, nil
}

func (s *Service) Unfollow(ctx context.Context, principal auth.Principal, titleID string) error {
	profileID, err := activeProfile(principal, s.now().UTC())
	if err != nil { return err }
	tx, err := s.authorizedTx(ctx, principal, profileID, false)
	if err != nil { return err }
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM profile_media_notifications WHERE profile_id = $1::uuid AND root_title_id = $2::uuid AND state = 'scheduled'`, profileID, titleID); err != nil { return fmt.Errorf("cancel scheduled media notifications: %w", err) }
	if _, err := tx.Exec(ctx, `DELETE FROM profile_media_notification_subscriptions WHERE profile_id = $1::uuid AND title_id = $2::uuid`, profileID, titleID); err != nil { return fmt.Errorf("delete notification subscription: %w", err) }
	if err := tx.Commit(ctx); err != nil { return fmt.Errorf("commit notification unsubscription: %w", err) }
	return nil
}

func (s *Service) List(ctx context.Context, principal auth.Principal, cursor string, limit int) (Page, error) {
	profileID, err := activeProfile(principal, s.now().UTC())
	if err != nil { return Page{}, err }
	if limit == 0 { limit = 30 }
	if limit < 1 || limit > MaximumPageSize { return Page{}, ErrInvalidInput }
	var before int64
	if cursor != "" { before, err = strconv.ParseInt(cursor, 10, 64); if err != nil || before < 1 { return Page{}, ErrInvalidInput } }
	tx, err := s.authorizedTx(ctx, principal, profileID, false)
	if err != nil { return Page{}, err }
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT id, kind, root_title_id::text, subject_title_id::text, title, series_title,
		       release_date::text, season_number, episode_number, available_at, read_at, created_at
		FROM profile_media_notifications
		WHERE profile_id = $1::uuid AND state IN ('unread', 'read')
		  AND ($2::bigint = 0 OR id < $2::bigint)
		ORDER BY id DESC LIMIT $3
	`, profileID, before, limit+1)
	if err != nil { return Page{}, fmt.Errorf("query media notifications: %w", err) }
	defer rows.Close()
	items := make([]Notification, 0, limit+1)
	for rows.Next() {
		var item Notification; var id int64
		if err := rows.Scan(&id, &item.Kind, &item.TitleID, &item.SubjectTitleID, &item.Title, &item.SeriesTitle, &item.ReleaseDate, &item.SeasonNumber, &item.EpisodeNumber, &item.AvailableAt, &item.ReadAt, &item.CreatedAt); err != nil { return Page{}, fmt.Errorf("scan media notification: %w", err) }
		item.ID = strconv.FormatInt(id, 10); items = append(items, item)
	}
	if err := rows.Err(); err != nil { return Page{}, fmt.Errorf("iterate media notifications: %w", err) }
	page := Page{Notifications: items}
	if len(items) > limit { page.Notifications = items[:limit]; page.NextCursor = items[limit-1].ID }
	if err := tx.Commit(ctx); err != nil { return Page{}, fmt.Errorf("commit media notification list: %w", err) }
	return page, nil
}

func (s *Service) Acknowledge(ctx context.Context, principal auth.Principal, notificationID, state string) error {
	profileID, err := activeProfile(principal, s.now().UTC())
	if err != nil { return err }
	id, err := strconv.ParseInt(notificationID, 10, 64)
	if err != nil || id < 1 || !validAcknowledgementState(state) { return ErrInvalidInput }
	tx, err := s.authorizedTx(ctx, principal, profileID, false)
	if err != nil { return err }
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
		UPDATE profile_media_notifications SET state = $3,
			read_at = CASE WHEN $3 = 'read' THEN COALESCE(read_at, $4) ELSE NULL END,
			dismissed_at = CASE WHEN $3 = 'dismissed' THEN COALESCE(dismissed_at, $4) ELSE NULL END,
			updated_at = $4
		WHERE id = $1 AND profile_id = $2::uuid
		  AND (state IN ('unread', 'read') OR state = 'dismissed' AND $3 = 'dismissed')
	`, id, profileID, state, s.now().UTC())
	if err != nil { return fmt.Errorf("acknowledge media notification: %w", err) }
	if command.RowsAffected() == 0 { return ErrNotFound }
	if err := tx.Commit(ctx); err != nil { return fmt.Errorf("commit media notification acknowledgement: %w", err) }
	return nil
}

func (s *Service) RunScheduled(ctx context.Context) error { return s.Generate(ctx) }

const generationPageSize = 256

type notificationRoot struct {
	profileID   string
	titleID     string
	timezone    string
	mediaType   string
	horizon     int
	lead        int
	title       *string
	releaseDate *time.Time
}

type notificationRootPageFetcher func(context.Context, string, string, int) ([]notificationRoot, error)

func iterateNotificationRoots(ctx context.Context, pageSize int, fetch notificationRootPageFetcher, process func(notificationRoot) error) error {
	if pageSize < 1 { return ErrInvalidInput }
	afterProfileID, afterTitleID := "", ""
	for {
		if err := ctx.Err(); err != nil { return err }
		page, err := fetch(ctx, afterProfileID, afterTitleID, pageSize)
		if err != nil { return err }
		if len(page) == 0 { return nil }
		for _, item := range page {
			if err := ctx.Err(); err != nil { return err }
			if err := process(item); err != nil { return err }
		}
		last := page[len(page)-1]
		afterProfileID, afterTitleID = last.profileID, last.titleID
		if len(page) < pageSize { return nil }
	}
}

func loadNotificationRootPage(ctx context.Context, tx pgx.Tx, afterProfileID, afterTitleID string, limit int) ([]notificationRoot, error) {
	rows, err := tx.Query(ctx, `
		SELECT subscription.profile_id::text, subscription.title_id::text, subscription.timezone,
		       subscription.horizon_days, subscription.lead_days,
		       root.media_type, root.display_title, root.release_date
		FROM profile_media_notification_subscriptions AS subscription
		JOIN titles AS root ON root.id = subscription.title_id
		WHERE NULLIF($1, '') IS NULL
		   OR (subscription.profile_id, subscription.title_id) > (NULLIF($1, '')::uuid, NULLIF($2, '')::uuid)
		ORDER BY subscription.profile_id, subscription.title_id
		LIMIT $3
	`, afterProfileID, afterTitleID, limit)
	if err != nil { return nil, fmt.Errorf("query notification generation audience: %w", err) }
	defer rows.Close()
	page := make([]notificationRoot, 0, limit)
	for rows.Next() {
		var item notificationRoot
		if err := rows.Scan(&item.profileID, &item.titleID, &item.timezone, &item.horizon, &item.lead, &item.mediaType, &item.title, &item.releaseDate); err != nil { return nil, fmt.Errorf("scan notification generation audience: %w", err) }
		page = append(page, item)
	}
	if err := rows.Err(); err != nil { return nil, fmt.Errorf("iterate notification generation audience: %w", err) }
	return page, nil
}

func loadNotificationRoot(ctx context.Context, tx pgx.Tx, profileID, titleID string) (notificationRoot, error) {
	var item notificationRoot
	err := tx.QueryRow(ctx, `
		SELECT subscription.profile_id::text, subscription.title_id::text, subscription.timezone,
		       subscription.horizon_days, subscription.lead_days,
		       root.media_type, root.display_title, root.release_date
		FROM profile_media_notification_subscriptions AS subscription
		JOIN titles AS root ON root.id = subscription.title_id
		WHERE subscription.profile_id = $1::uuid AND subscription.title_id = $2::uuid
	`, profileID, titleID).Scan(&item.profileID, &item.titleID, &item.timezone, &item.horizon, &item.lead, &item.mediaType, &item.title, &item.releaseDate)
	if err != nil { return notificationRoot{}, fmt.Errorf("query targeted notification generation audience: %w", err) }
	return item, nil
}

type notificationRootLoader func(context.Context, string, string) (notificationRoot, error)

func generateOneNotificationRoot(ctx context.Context, profileID, titleID string, load notificationRootLoader, process func(notificationRoot) error) error {
	if err := ctx.Err(); err != nil { return err }
	item, err := load(ctx, profileID, titleID)
	if err != nil { return err }
	if item.profileID != profileID || item.titleID != titleID { return fmt.Errorf("targeted notification generation identity changed") }
	if err := ctx.Err(); err != nil { return err }
	return process(item)
}

func (s *Service) generateOne(ctx context.Context, tx pgx.Tx, now time.Time, profileID, titleID string) error {
	return generateOneNotificationRoot(ctx, profileID, titleID,
		func(ctx context.Context, profileID, titleID string) (notificationRoot, error) {
			return loadNotificationRoot(ctx, tx, profileID, titleID)
		},
		func(item notificationRoot) error { return s.generateRoot(ctx, tx, now, item) },
	)
}

func (s *Service) generateRoot(ctx context.Context, tx pgx.Tx, now time.Time, item notificationRoot) error {
	location, err := time.LoadLocation(item.timezone)
	if err != nil { return fmt.Errorf("load notification timezone %q: %w", item.timezone, err) }
	localToday := dateAt(now, location)
	lastDate := localToday.AddDate(0, 0, item.horizon)
	if item.mediaType == "movie" && item.title != nil && item.releaseDate != nil {
		releaseDate := databaseDateAt(*item.releaseDate, location)
		if releaseWithinHorizon(releaseDate, localToday, item.horizon) {
			if err := upsertScheduled(ctx, tx, now, item.profileID, item.titleID, nil, KindCalendarEventUpcoming, notificationKey(KindCalendarEventUpcoming, item.titleID, nil, nil), *item.title, nil, releaseDate, nil, nil, dateAt(releaseDate.AddDate(0, 0, -item.lead), location)); err != nil { return err }
			if err := upsertScheduled(ctx, tx, now, item.profileID, item.titleID, nil, KindMovieRelease, notificationKey(KindMovieRelease, item.titleID, nil, nil), *item.title, nil, releaseDate, nil, nil, dateAt(releaseDate, location)); err != nil { return err }
		}
	}
	if item.mediaType == "series" && item.title != nil {
		if err := s.generateSeries(ctx, tx, now, localToday, lastDate, location, item.profileID, item.titleID, *item.title, item.lead); err != nil { return err }
	}
	return nil
}
type generationTransactionResult struct {
	done       bool
	statements int
	work       int
}

type generationTransactionMetrics struct {
	transactions  int
	maxStatements int
	maxWork       int
}

func runGenerationTransactions(ctx context.Context, run func(context.Context) (generationTransactionResult, error)) (generationTransactionMetrics, error) {
	var metrics generationTransactionMetrics
	for {
		if err := ctx.Err(); err != nil { return metrics, err }
		result, err := run(ctx)
		if err != nil { return metrics, err }
		metrics.transactions++
		metrics.maxStatements = max(metrics.maxStatements, result.statements)
		metrics.maxWork = max(metrics.maxWork, result.work)
		if result.done { return metrics, nil }
	}
}

func (s *Service) Generate(ctx context.Context) error {
	now := s.now().UTC()
	connection, err := s.pool.Acquire(ctx)
	if err != nil { return fmt.Errorf("acquire media notification worker connection: %w", err) }
	defer connection.Release()
	var locked bool
	if err := connection.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, mediaNotificationLockKey).Scan(&locked); err != nil {
		return fmt.Errorf("acquire media notification worker lock: %w", err)
	}
	if !locked { return nil }
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var unlocked bool
		_ = connection.QueryRow(unlockContext, `SELECT pg_advisory_unlock($1)`, mediaNotificationLockKey).Scan(&unlocked)
	}()
	if err := cleanupStaleSubscriptions(ctx, connection); err != nil { return err }
	if _, err := runGenerationTransactions(ctx, func(ctx context.Context) (generationTransactionResult, error) {
		return s.generateCommittedPage(ctx, connection, now)
	}); err != nil { return err }
	if err := publishDueNotifications(ctx, connection, now); err != nil { return err }
	return cleanupExpiredNotifications(ctx, connection, now)
}

func handleGenerationRootError(err error, recordCapacitySkip func() error) (bool, error) {
	if err == nil { return false, nil }
	if !errors.Is(err, ErrCapacity) { return false, err }
	if recordErr := recordCapacitySkip(); recordErr != nil { return false, recordErr }
	return true, nil
}

func (s *Service) generateCommittedPage(ctx context.Context, connection *pgxpool.Conn, now time.Time) (generationTransactionResult, error) {
	tx, err := connection.Begin(ctx)
	if err != nil { return generationTransactionResult{}, fmt.Errorf("begin media notification generation page: %w", err) }
	defer func() { _ = tx.Rollback(ctx) }()
	var afterProfileID, afterTitleID *string
	if err := tx.QueryRow(ctx, `SELECT after_profile_id::text, after_title_id::text FROM media_notification_worker_state WHERE singleton FOR UPDATE`).Scan(&afterProfileID, &afterTitleID); err != nil {
		return generationTransactionResult{}, fmt.Errorf("lock media notification worker cursor: %w", err)
	}
	profileCursor, titleCursor := "", ""
	if afterProfileID != nil { profileCursor = *afterProfileID }
	if afterTitleID != nil { titleCursor = *afterTitleID }
	page, err := loadNotificationRootPage(ctx, tx, profileCursor, titleCursor, generationPageSize)
	if err != nil { return generationTransactionResult{}, err }
	result := generationTransactionResult{statements: 2, work: len(page)}
	for _, item := range page {
		if err := ctx.Err(); err != nil { return generationTransactionResult{}, err }
		generationErr := s.generateRoot(ctx, tx, now, item)
		skipped, err := handleGenerationRootError(generationErr, func() error {
			_, recordErr := tx.Exec(ctx, `
				INSERT INTO media_notification_generation_skips
					(profile_id, root_title_id, reason, first_skipped_at, last_skipped_at)
				VALUES ($1::uuid, $2::uuid, 'series-subject-capacity', $3, $3)
				ON CONFLICT (profile_id, root_title_id) DO UPDATE SET
					occurrence_count = media_notification_generation_skips.occurrence_count + 1,
					last_skipped_at = EXCLUDED.last_skipped_at
			`, item.profileID, item.titleID, now)
			if recordErr != nil { return fmt.Errorf("record skipped media notification root: %w", recordErr) }
			return nil
		})
		if err != nil { return generationTransactionResult{}, err }
		if skipped { result.statements++; continue }
		if item.mediaType == "series" { result.statements += 3; result.work += maximumSeriesSubjects } else { result.statements += 2; result.work++ }
	}
	if len(page) < generationPageSize {
		if _, err := tx.Exec(ctx, `UPDATE media_notification_worker_state SET after_profile_id = NULL, after_title_id = NULL, cycle_started_at = NULL, updated_at = $1 WHERE singleton`, now); err != nil {
			return generationTransactionResult{}, fmt.Errorf("reset media notification worker cursor: %w", err)
		}
		result.done = true
	} else {
		last := page[len(page)-1]
		if _, err := tx.Exec(ctx, `UPDATE media_notification_worker_state SET after_profile_id = $1::uuid, after_title_id = $2::uuid, cycle_started_at = COALESCE(cycle_started_at, $3), updated_at = $3 WHERE singleton`, last.profileID, last.titleID, now); err != nil {
			return generationTransactionResult{}, fmt.Errorf("advance media notification worker cursor: %w", err)
		}
	}
	result.statements++
	if err := tx.Commit(ctx); err != nil { return generationTransactionResult{}, fmt.Errorf("commit media notification generation page: %w", err) }
	return result, nil
}

func cleanupStaleSubscriptions(ctx context.Context, connection *pgxpool.Conn) error {
	tx, err := connection.Begin(ctx)
	if err != nil { return fmt.Errorf("begin stale media subscription cleanup: %w", err) }
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		WITH stale AS MATERIALIZED (
			SELECT subscription.profile_id, subscription.title_id
			FROM profile_media_notification_subscriptions AS subscription
			WHERE NOT EXISTS (SELECT 1 FROM profile_library AS library WHERE library.profile_id = subscription.profile_id AND library.title_id = subscription.title_id)
			ORDER BY subscription.profile_id, subscription.title_id LIMIT $1
		), canceled AS (
			DELETE FROM profile_media_notifications AS notification USING stale
			WHERE notification.profile_id = stale.profile_id AND notification.root_title_id = stale.title_id AND notification.state = 'scheduled'
		)
		DELETE FROM profile_media_notification_subscriptions AS subscription USING stale
		WHERE subscription.profile_id = stale.profile_id AND subscription.title_id = stale.title_id
	`, cleanupBatchSize); err != nil { return fmt.Errorf("clean stale media notification subscriptions: %w", err) }
	if err := tx.Commit(ctx); err != nil { return fmt.Errorf("commit stale media subscription cleanup: %w", err) }
	return nil
}

func publishDueNotifications(ctx context.Context, connection *pgxpool.Conn, now time.Time) error {
	for {
		if err := ctx.Err(); err != nil { return err }
		tx, err := connection.Begin(ctx)
		if err != nil { return fmt.Errorf("begin due media notification publication: %w", err) }
		command, err := tx.Exec(ctx, `WITH due AS (SELECT id FROM profile_media_notifications WHERE state = 'scheduled' AND available_at <= $1 ORDER BY available_at, id FOR UPDATE SKIP LOCKED LIMIT $2) UPDATE profile_media_notifications AS notification SET state = 'unread', updated_at = $1 FROM due WHERE notification.id = due.id`, now, cleanupBatchSize)
		if err != nil { _ = tx.Rollback(ctx); return fmt.Errorf("publish due media notifications: %w", err) }
		if err := tx.Commit(ctx); err != nil { return fmt.Errorf("commit due media notification publication: %w", err) }
		if command.RowsAffected() < cleanupBatchSize { return nil }
	}
}

func cleanupExpiredNotifications(ctx context.Context, connection *pgxpool.Conn, now time.Time) error {
	for {
		if err := ctx.Err(); err != nil { return err }
		tx, err := connection.Begin(ctx)
		if err != nil { return fmt.Errorf("begin expired media notification cleanup: %w", err) }
		command, err := tx.Exec(ctx, `WITH expired AS (SELECT id FROM profile_media_notifications WHERE expires_at <= $1 ORDER BY expires_at, id FOR UPDATE SKIP LOCKED LIMIT $2) DELETE FROM profile_media_notifications AS notification USING expired WHERE notification.id = expired.id`, now, cleanupBatchSize)
		if err != nil { _ = tx.Rollback(ctx); return fmt.Errorf("delete expired media notifications: %w", err) }
		if err := tx.Commit(ctx); err != nil { return fmt.Errorf("commit expired media notification cleanup: %w", err) }
		if command.RowsAffected() < cleanupBatchSize { return nil }
	}
}


func seriesSubjectOverflow(ctx context.Context, tx pgx.Tx, rootID string) (bool, error) {
	var overflow bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM (
				SELECT season.id FROM titles AS season
				WHERE season.parent_id = $1::uuid AND season.media_type = 'season'
				UNION ALL
				SELECT episode.id FROM titles AS season
				JOIN titles AS episode ON episode.parent_id = season.id AND episode.media_type = 'episode'
				WHERE season.parent_id = $1::uuid AND season.media_type = 'season'
				OFFSET $2 LIMIT 1
			) AS overflow_subject
		)
	`, rootID, maximumSeriesSubjects).Scan(&overflow); err != nil {
		return false, fmt.Errorf("bound followed series tree: %w", err)
	}
	return overflow, nil
}
func (s *Service) generateSeries(ctx context.Context, tx pgx.Tx, now, today, lastDate time.Time, location *time.Location, profileID, rootID, seriesTitle string, lead int) error {
	overflow, err := seriesSubjectOverflow(ctx, tx, rootID)
	if err != nil { return err }
	if overflow { return ErrCapacity }

	if _, err := tx.Exec(ctx, `
		WITH current_subjects AS MATERIALIZED (
			SELECT season.id AS subject_title_id, 'season'::text AS subject_kind,
			       season.ordinal AS season_number, NULL::integer AS episode_number,
			       COALESCE(season.display_title, $3) AS title, season.release_date
			FROM titles AS season
			WHERE season.parent_id = $2::uuid AND season.media_type = 'season'
			UNION ALL
			SELECT episode.id, 'episode'::text, season.ordinal, episode.ordinal,
			       COALESCE(episode.display_title, $3), episode.release_date
			FROM titles AS season
			JOIN titles AS episode ON episode.parent_id = season.id AND episode.media_type = 'episode'
			WHERE season.parent_id = $2::uuid AND season.media_type = 'season'
			ORDER BY subject_kind, season_number, episode_number, subject_title_id
			LIMIT $6
		), fresh AS (
			INSERT INTO profile_media_notification_observations
				(profile_id, root_title_id, subject_title_id, subject_kind, season_number, episode_number, first_observed_at, last_observed_at)
			SELECT $1::uuid, $2::uuid, subject_title_id, subject_kind, season_number, episode_number, $4, $4
			FROM current_subjects
			ON CONFLICT DO NOTHING
			RETURNING subject_title_id, subject_kind, season_number, episode_number
		), refreshed AS (
			UPDATE profile_media_notification_observations AS observation
			SET last_observed_at = $4
			FROM current_subjects AS subject
			WHERE observation.profile_id = $1::uuid AND observation.root_title_id = $2::uuid
			  AND observation.subject_title_id = subject.subject_title_id
			RETURNING observation.subject_title_id
		)
		INSERT INTO profile_media_notifications
			(profile_id, root_title_id, subject_title_id, kind, dedupe_key, title, series_title,
			 release_date, season_number, episode_number, available_at, state, created_at, updated_at, expires_at)
		SELECT $1::uuid, $2::uuid, subject.subject_title_id,
		       CASE subject.subject_kind WHEN 'season' THEN 'season-available' ELSE 'episode-available' END,
		       CASE subject.subject_kind
			 WHEN 'season' THEN 'season:' || $2 || ':' || subject.season_number
			 ELSE 'episode:' || $2 || ':' || subject.season_number || ':' || subject.episode_number
		       END,
		       subject.title, $3, subject.release_date, subject.season_number, subject.episode_number,
		       COALESCE(subject.release_date::timestamp AT TIME ZONE $5, $4),
		       CASE WHEN COALESCE(subject.release_date::timestamp AT TIME ZONE $5, $4) <= $4 THEN 'unread' ELSE 'scheduled' END,
		       $4, $4, GREATEST(COALESCE(subject.release_date::timestamp AT TIME ZONE $5, $4), $4) + interval '90 days'
		FROM fresh
		JOIN current_subjects AS subject
		  ON subject.subject_title_id = fresh.subject_title_id
		 AND subject.subject_kind = fresh.subject_kind
		 AND subject.season_number = fresh.season_number
		 AND subject.episode_number IS NOT DISTINCT FROM fresh.episode_number
		ON CONFLICT (profile_id, dedupe_key) DO NOTHING
	`, profileID, rootID, seriesTitle, now, location.String(), maximumSeriesSubjects); err != nil {
		return fmt.Errorf("bulk generate series availability notifications: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO profile_media_notifications
			(profile_id, root_title_id, subject_title_id, kind, dedupe_key, title, series_title,
			 release_date, season_number, episode_number, available_at, state, created_at, updated_at, expires_at)
		SELECT $1::uuid, $2::uuid, episode.id, 'calendar-event-upcoming',
		       'calendar-event-upcoming:' || $2 || ':' || season.ordinal || ':' || episode.ordinal,
		       COALESCE(episode.display_title, $3), $3, episode.release_date, season.ordinal, episode.ordinal,
		       (episode.release_date - $6::integer)::timestamp AT TIME ZONE $7,
		       CASE WHEN ((episode.release_date - $6::integer)::timestamp AT TIME ZONE $7) <= $4 THEN 'unread' ELSE 'scheduled' END,
		       $4, $4,
		       GREATEST((episode.release_date - $6::integer)::timestamp AT TIME ZONE $7, $4) + interval '90 days'
		FROM titles AS season
		JOIN titles AS episode ON episode.parent_id = season.id AND episode.media_type = 'episode'
		WHERE season.parent_id = $2::uuid AND season.media_type = 'season'
		  AND episode.release_date BETWEEN $5::date AND $8::date
		ORDER BY season.ordinal, episode.ordinal, episode.id
		LIMIT $9
		ON CONFLICT (profile_id, dedupe_key) DO UPDATE SET
			subject_title_id = CASE WHEN profile_media_notifications.state = 'scheduled' THEN EXCLUDED.subject_title_id ELSE profile_media_notifications.subject_title_id END,
			title = CASE WHEN profile_media_notifications.state = 'scheduled' THEN EXCLUDED.title ELSE profile_media_notifications.title END,
			release_date = CASE WHEN profile_media_notifications.state = 'scheduled' THEN EXCLUDED.release_date ELSE profile_media_notifications.release_date END,
			available_at = CASE WHEN profile_media_notifications.state = 'scheduled' THEN EXCLUDED.available_at ELSE profile_media_notifications.available_at END,
			state = CASE WHEN profile_media_notifications.state = 'scheduled' AND EXCLUDED.available_at <= $4 THEN 'unread' ELSE profile_media_notifications.state END,
			updated_at = CASE WHEN profile_media_notifications.state = 'scheduled' THEN $4 ELSE profile_media_notifications.updated_at END,
			expires_at = CASE WHEN profile_media_notifications.state = 'scheduled' THEN GREATEST(profile_media_notifications.expires_at, EXCLUDED.expires_at) ELSE profile_media_notifications.expires_at END
	`, profileID, rootID, seriesTitle, now, today.Format(time.DateOnly), lead, location.String(), lastDate.Format(time.DateOnly), maximumSeriesSubjects); err != nil {
		return fmt.Errorf("bulk generate upcoming episode notifications: %w", err)
	}
	return nil
}


func upsertScheduled(ctx context.Context, tx pgx.Tx, now time.Time, profileID, rootID string, subjectID *string, kind Kind, key, title string, seriesTitle *string, releaseDate time.Time, season, episode *int, availableAt time.Time) error {
	var date *time.Time; if !releaseDate.IsZero() { date = &releaseDate }
	_, err := tx.Exec(ctx, `
		INSERT INTO profile_media_notifications (profile_id, root_title_id, subject_title_id, kind, dedupe_key, title, series_title, release_date, season_number, episode_number, available_at, state, created_at, updated_at, expires_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, CASE WHEN $11 <= $12 THEN 'unread' ELSE 'scheduled' END, $12, $12, $13)
		ON CONFLICT (profile_id, dedupe_key) DO UPDATE SET
			subject_title_id = CASE WHEN profile_media_notifications.state = 'scheduled' THEN EXCLUDED.subject_title_id ELSE profile_media_notifications.subject_title_id END,
			title = CASE WHEN profile_media_notifications.state = 'scheduled' THEN EXCLUDED.title ELSE profile_media_notifications.title END,
			series_title = CASE WHEN profile_media_notifications.state = 'scheduled' THEN EXCLUDED.series_title ELSE profile_media_notifications.series_title END,
			release_date = CASE WHEN profile_media_notifications.state = 'scheduled' THEN EXCLUDED.release_date ELSE profile_media_notifications.release_date END,
			available_at = CASE WHEN profile_media_notifications.state = 'scheduled' THEN EXCLUDED.available_at ELSE profile_media_notifications.available_at END,
			state = CASE WHEN profile_media_notifications.state = 'scheduled' AND EXCLUDED.available_at <= $12 THEN 'unread' ELSE profile_media_notifications.state END,
			updated_at = CASE WHEN profile_media_notifications.state = 'scheduled' THEN $12 ELSE profile_media_notifications.updated_at END,
			expires_at = CASE WHEN profile_media_notifications.state = 'scheduled' THEN GREATEST(profile_media_notifications.expires_at, EXCLUDED.expires_at) ELSE profile_media_notifications.expires_at END
	`, profileID, rootID, subjectID, kind, key, title, seriesTitle, date, season, episode, availableAt, now, expiryAt(now, availableAt))
	if err != nil { return fmt.Errorf("upsert %s media notification: %w", kind, err) }
	return nil
}

func (s *Service) authorizedTx(ctx context.Context, principal auth.Principal, profileID string, management bool) (pgx.Tx, error) {
	if s.pool == nil { return nil, errors.New("media notification database is unavailable") }
	tx, err := s.pool.Begin(ctx); if err != nil { return nil, fmt.Errorf("begin media notification transaction: %w", err) }
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, management)
	if err != nil { _ = tx.Rollback(ctx); return nil, fmt.Errorf("authorize media notification profile: %w", err) }
	if !authorized { _ = tx.Rollback(ctx); return nil, ErrForbidden }
	return tx, nil
}

func activeProfile(principal auth.Principal, now time.Time) (string, error) {
	if principal.ActiveProfileID == nil || strings.TrimSpace(*principal.ActiveProfileID) == "" || principal.ProfileGrantExpiresAt == nil || !principal.ProfileGrantExpiresAt.After(now) { return "", ErrActiveProfileRequired }
	return *principal.ActiveProfileID, nil
}

func (s *Service) defaultTimezone(ctx context.Context) string { if s.runtimeSettings == nil { return "UTC" }; return runtimesettings.Load(ctx, s.runtimeSettings).Timezone }
func dateAt(value time.Time, location *time.Location) time.Time { local := value.In(location); return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location).UTC() }
func zeroDate(value *time.Time, location *time.Location) time.Time { if value == nil { return time.Time{} }; return databaseDateAt(*value, location) }
func databaseDateAt(value time.Time, location *time.Location) time.Time { return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, location).UTC() }

func releaseWithinHorizon(releaseDate, today time.Time, horizonDays int) bool {
	return !releaseDate.Before(today) && !releaseDate.After(today.AddDate(0, 0, horizonDays))
}

func notificationKey(kind Kind, rootID string, season, episode *int) string {
	key := string(kind) + ":" + rootID
	if season != nil { key += ":" + strconv.Itoa(*season) }
	if episode != nil { key += ":" + strconv.Itoa(*episode) }
	return key
}

func expiryAt(now, availableAt time.Time) time.Time {
	if availableAt.Before(now) { return now.Add(Retention) }
	return availableAt.Add(Retention)
}

func validAcknowledgementState(state string) bool {
	return state == "read" || state == "dismissed"
}
