package calendar

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/metadata"
)

var (
	ErrInvalidInput    = errors.New("invalid calendar range")
	ErrProfileRequired = errors.New("active profile required")
	ErrNotFound        = errors.New("calendar link not found")
	ErrLinkExists      = errors.New("calendar link already exists")
	ErrCapacity        = errors.New("calendar event capacity exceeded")
)

const (
	maximumRangeDays          = 93
	maximumCalendarEvents     = 5_000
	calendarEventQueryLimit   = maximumCalendarEvents + 1
	defaultRefreshMinimum     = 5 * time.Minute
	calendarTitlePageSize     = 32
	calendarSeasonBudget      = 64
	defaultRefreshClaimLease  = 10 * time.Minute
	defaultRefreshTurnTimeout = 4 * time.Minute
	calendarTokenEntropyBytes = 32
	calendarTokenPrefix       = "rivune_cal_"
)

type Event struct {
	ID               string    `json:"id"`
	TitleID          string    `json:"titleId"`
	MediaType        string    `json:"mediaType"`
	Title            string    `json:"title"`
	ReleaseDate      string    `json:"releaseDate"`
	PosterURL        string    `json:"posterUrl,omitempty"`
	ResourceID       string    `json:"resourceId,omitempty"`
	ResourceProvider string    `json:"resourceProvider,omitempty"`
	SeriesTitle      string    `json:"seriesTitle,omitempty"`
	SeriesID         string    `json:"seriesId,omitempty"`
	SeasonID         string    `json:"seasonId,omitempty"`
	SeasonNumber     *int      `json:"seasonNumber,omitempty"`
	EpisodeNumber    *int      `json:"episodeNumber,omitempty"`
	UpdatedAt        time.Time `json:"-"`
}

type Result struct {
	Events []Event `json:"events"`
}

type Link struct {
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
	RotatedAt time.Time `json:"rotatedAt,omitempty"`
}

type Credential struct {
	Link
	Token string `json:"-"`
}

type eventRepository interface {
	List(context.Context, pgx.Tx, string, time.Time, time.Time) ([]Event, error)
	LibraryTitlePage(context.Context, pgx.Tx, string, refreshCursor, int) ([]libraryTitle, error)
	ClaimRefresh(context.Context, pgx.Tx, string, string, time.Time, time.Time, time.Time, string) (refreshCursor, bool, time.Time, error)
	CompleteRefresh(context.Context, pgx.Tx, string, string, refreshCursor, bool) (bool, time.Time, error)
}

type libraryTitle struct {
	ID        string
	MediaType string
}

type refreshCursor struct {
	AfterTitleID            string
	ResumeTitleID           string
	ResumeAfterSeasonNumber int
	ResumeAfterSeasonID     string
	HasSeasonCursor         bool
	From                    time.Time
	To                      time.Time
	Language                string
}

type refreshTurnResult struct {
	continuation  bool
	cycleComplete bool
	retryAt       time.Time
}

type metadataReader interface {
	MovieDetails(context.Context, auth.Principal, string, string) (metadata.Movie, error)
	SeriesDetails(context.Context, auth.Principal, string, metadata.SeriesDetailsOptions) (metadata.Series, error)
	SeasonDetails(context.Context, auth.Principal, string, string, string) (metadata.Season, error)
}

type postgresRepository struct{}

type Service struct {
	pool                   *pgxpool.Pool
	repository             eventRepository
	metadata               metadataReader
	logger                 *slog.Logger
	location               *time.Location
	now                    func() time.Time
	refreshMinimumInterval time.Duration
	refreshClaimLease      time.Duration
	refreshTurnTimeout     time.Duration
	titlePageSize          int
	seasonBudget           int
	refreshWake            chan struct{}
	refreshMu              sync.Mutex
	pendingRefreshes       map[string]refreshRequest
	refreshQueue           []string
	runningRefreshes       map[string]struct{}
}

type refreshRequest struct {
	key       string
	profileID string
	principal auth.Principal
	from      time.Time
	to        time.Time
	language  string
	notBefore time.Time
}

func NewService(pool *pgxpool.Pool, metadataService metadataReader, timezone string, logger *slog.Logger) (*Service, error) {
	if timezone == "" || timezone == "Local" || strings.TrimSpace(timezone) != timezone {
		return nil, fmt.Errorf("calendar timezone must be an explicit IANA timezone")
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("load calendar timezone %q: %w", timezone, err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		pool:                   pool,
		repository:             &postgresRepository{},
		metadata:               metadataService,
		logger:                 logger,
		location:               location,
		now:                    time.Now,
		refreshMinimumInterval: defaultRefreshMinimum,
		refreshClaimLease:      defaultRefreshClaimLease,
		refreshTurnTimeout:     defaultRefreshTurnTimeout,
		titlePageSize:          calendarTitlePageSize,
		seasonBudget:           calendarSeasonBudget,
		refreshWake:            make(chan struct{}, 1),
		pendingRefreshes:       make(map[string]refreshRequest),
		runningRefreshes:       make(map[string]struct{}),
	}, nil
}

func (s *Service) List(ctx context.Context, principal auth.Principal, fromValue, toValue, language string) (Result, error) {
	profileID, err := activeProfileID(principal, s.now().UTC())
	if err != nil {
		return Result{}, err
	}
	from, to, err := normalizeRange(fromValue, toValue)
	if err != nil {
		return Result{}, err
	}
	if s.pool == nil {
		if !principal.IsGlobalAdministrator() {
			return Result{}, ErrProfileRequired
		}
		events, err := s.repository.List(ctx, nil, profileID, from, to)
		if err != nil {
			return Result{}, err
		}
		if len(events) > maximumCalendarEvents {
			return Result{}, ErrCapacity
		}
		sortEvents(events)
		s.enqueueRefresh(principal, profileID, from, to, language)
		return Result{Events: events}, nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("begin calendar query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, false)
	if err != nil {
		return Result{}, fmt.Errorf("authorize active calendar profile: %w", err)
	}
	if !authorized {
		return Result{}, ErrProfileRequired
	}
	events, err := s.repository.List(ctx, tx, profileID, from, to)
	if err != nil {
		return Result{}, err
	}
	if len(events) > maximumCalendarEvents {
		return Result{}, ErrCapacity
	}
	sortEvents(events)
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("commit calendar query: %w", err)
	}
	s.enqueueRefresh(principal, profileID, from, to, language)
	return Result{Events: events}, nil
}

func (s *Service) LinkStatus(ctx context.Context, principal auth.Principal, profileID string) (Link, error) {
	tx, err := s.beginManagedProfile(ctx, principal, profileID)
	if err != nil {
		return Link{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var link Link
	err = tx.QueryRow(ctx, `
		SELECT created_at, rotated_at
		FROM profile_calendar_links
		WHERE profile_id = $1::uuid
	`, profileID).Scan(&link.CreatedAt, &link.RotatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		link = Link{Active: false}
	} else if err != nil {
		return Link{}, fmt.Errorf("query calendar link status: %w", err)
	} else {
		link.Active = true
	}
	if err := tx.Commit(ctx); err != nil {
		return Link{}, fmt.Errorf("commit calendar link status: %w", err)
	}
	return link, nil
}

func (s *Service) CreateLink(ctx context.Context, principal auth.Principal, profileID string) (Credential, error) {
	token, digest, err := newCalendarToken()
	if err != nil {
		return Credential{}, err
	}
	tx, err := s.beginManagedProfile(ctx, principal, profileID)
	if err != nil {
		return Credential{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var result Credential
	err = tx.QueryRow(ctx, `
		INSERT INTO profile_calendar_links (profile_id, token_hash)
		VALUES ($1::uuid, $2)
		ON CONFLICT (profile_id) DO NOTHING
		RETURNING created_at, rotated_at
	`, profileID, digest).Scan(&result.CreatedAt, &result.RotatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrLinkExists
	}
	if err != nil {
		return Credential{}, fmt.Errorf("create calendar link: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Credential{}, fmt.Errorf("commit calendar link creation: %w", err)
	}
	result.Active, result.Token = true, token
	return result, nil
}

func (s *Service) RotateLink(ctx context.Context, principal auth.Principal, profileID string) (Credential, error) {
	token, digest, err := newCalendarToken()
	if err != nil {
		return Credential{}, err
	}
	tx, err := s.beginManagedProfile(ctx, principal, profileID)
	if err != nil {
		return Credential{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var result Credential
	err = tx.QueryRow(ctx, `
		UPDATE profile_calendar_links
		SET token_hash = $2, rotated_at = now()
		WHERE profile_id = $1::uuid
		RETURNING created_at, rotated_at
	`, profileID, digest).Scan(&result.CreatedAt, &result.RotatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, fmt.Errorf("rotate calendar link: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Credential{}, fmt.Errorf("commit calendar link rotation: %w", err)
	}
	result.Active, result.Token = true, token
	return result, nil
}

func (s *Service) RevokeLink(ctx context.Context, principal auth.Principal, profileID string) error {
	tx, err := s.beginManagedProfile(ctx, principal, profileID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM profile_calendar_links WHERE profile_id = $1::uuid`, profileID); err != nil {
		return fmt.Errorf("revoke calendar link: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit calendar link revocation: %w", err)
	}
	return nil
}

func (s *Service) Feed(ctx context.Context, token string, includePayload bool) ([]byte, error) {
	digest, valid := calendarTokenDigest(token)
	if !valid || s.pool == nil {
		return nil, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin calendar feed: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var profileID string
	if err := tx.QueryRow(ctx, `
		SELECT profile_id::text
		FROM profile_calendar_links
		WHERE token_hash = $1
	`, digest).Scan(&profileID); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("resolve calendar feed profile: %w", err)
	}

	var access auth.ProfileAccess
	err = tx.QueryRow(ctx, `
		SELECT enabled, available_from::text, available_until::text,
		       to_char(access_start_time, 'HH24:MI'),
		       to_char(access_end_time, 'HH24:MI'),
		       COALESCE(access_timezone, 'UTC')
		FROM profiles
		WHERE id = $1::uuid
		FOR SHARE
	`, profileID).Scan(
		&access.Enabled, &access.AvailableFrom, &access.AvailableUntil,
		&access.AccessStartTime, &access.AccessEndTime, &access.AccessTimezone,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock calendar feed profile: %w", err)
	}

	var linkStillValid bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM profile_calendar_links
			WHERE profile_id = $1::uuid AND token_hash = $2
			FOR SHARE
		)
	`, profileID, digest).Scan(&linkStillValid); err != nil {
		return nil, fmt.Errorf("revalidate calendar feed link: %w", err)
	}
	if !linkStillValid {
		return nil, ErrNotFound
	}

	now := s.now()
	access.AccessTimezone = s.location.String()
	if !auth.ProfileAccessibleAt(access, now) {
		return nil, ErrNotFound
	}
	if !includePayload {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit calendar feed validation: %w", err)
		}
		return nil, nil
	}
	from, to := calendarFeedRange(now, s.location)
	events, err := s.repository.List(ctx, tx, profileID, from, to)
	if err != nil {
		return nil, err
	}
	if len(events) > maximumCalendarEvents {
		return nil, ErrCapacity
	}
	sortEvents(events)
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit calendar feed: %w", err)
	}
	return SerializeICS(events), nil
}

func (s *Service) beginManagedProfile(ctx context.Context, principal auth.Principal, profileID string) (pgx.Tx, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" || principal.ActiveProfileID == nil || *principal.ActiveProfileID != profileID || s.pool == nil {
		return nil, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin calendar link management: %w", err)
	}
	reloaded, valid, err := auth.ReloadAndLockPrincipal(ctx, tx, principal, s.now().UTC(), s.location)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("reload calendar link principal: %w", err)
	}
	if !valid || reloaded.ActiveProfileID == nil || *reloaded.ActiveProfileID != profileID || !reloaded.ActiveProfileCanManage {
		_ = tx.Rollback(ctx)
		return nil, ErrNotFound
	}
	return tx, nil
}

func newCalendarToken() (string, []byte, error) {
	entropy := make([]byte, calendarTokenEntropyBytes)
	if _, err := rand.Read(entropy); err != nil {
		return "", nil, fmt.Errorf("generate calendar token: %w", err)
	}
	token := calendarTokenPrefix + base64.RawURLEncoding.EncodeToString(entropy)
	digest := sha256.Sum256([]byte(token))
	return token, digest[:], nil
}

func calendarTokenDigest(token string) ([]byte, bool) {
	if !strings.HasPrefix(token, calendarTokenPrefix) || strings.TrimSpace(token) != token {
		return nil, false
	}
	encoded := strings.TrimPrefix(token, calendarTokenPrefix)
	entropy, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(entropy) != calendarTokenEntropyBytes || base64.RawURLEncoding.EncodeToString(entropy) != encoded {
		return nil, false
	}
	digest := sha256.Sum256([]byte(token))
	return digest[:], true
}

func calendarFeedRange(now time.Time, location *time.Location) (time.Time, time.Time) {
	local := now.In(location)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	return today.AddDate(0, 0, -31), today.AddDate(0, 0, 61)
}

// Run processes one bounded refresh turn per wake-up. List only records work;
// provider calls are made exclusively by this caller-owned worker.
func (s *Service) Run(ctx context.Context) {
	s.initializeRefreshState()
	for s.waitForRefresh(ctx) {
		request, ok := s.takeRefresh(s.now().UTC())
		if !ok {
			continue
		}
		turnContext, cancel := context.WithTimeout(ctx, s.refreshTurnTimeout)
		result, err := s.refresh(turnContext, request)
		cancel()
		s.finishRefresh(request, result, ctx.Err() == nil)
		if err != nil && ctx.Err() == nil {
			s.logger.Warn("calendar metadata refresh failed", "profileId", request.profileID, "error", err)
		}
		if ctx.Err() != nil {
			return
		}
		s.wakeRefreshWorker()
	}
}

func (s *Service) waitForRefresh(ctx context.Context) bool {
	s.refreshMu.Lock()
	var next time.Time
	for _, key := range s.refreshQueue {
		request, pending := s.pendingRefreshes[key]
		if !pending {
			continue
		}
		if next.IsZero() || request.notBefore.Before(next) {
			next = request.notBefore
		}
	}
	s.refreshMu.Unlock()

	if next.IsZero() {
		select {
		case <-ctx.Done():
			return false
		case <-s.refreshWake:
			return true
		}
	}
	wait := time.Until(next)
	if s.now != nil {
		wait = next.Sub(s.now().UTC())
	}
	if wait < 0 {
		wait = 0
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-s.refreshWake:
		return true
	case <-timer.C:
		return true
	}
}

func (s *Service) wakeRefreshWorker() {
	select {
	case s.refreshWake <- struct{}{}:
	default:
	}
}

func (s *Service) enqueueRefresh(principal auth.Principal, profileID string, from, to time.Time, language string) {
	if s.metadata == nil {
		return
	}
	s.initializeRefreshState()
	key := profileID
	s.refreshMu.Lock()
	if _, pending := s.pendingRefreshes[key]; pending {
		s.refreshMu.Unlock()
		return
	}
	if _, running := s.runningRefreshes[key]; running {
		s.refreshMu.Unlock()
		return
	}
	profileIDCopy := profileID
	principal.ActiveProfileID = &profileIDCopy
	if principal.ProfileGrantExpiresAt != nil {
		expiresAt := *principal.ProfileGrantExpiresAt
		principal.ProfileGrantExpiresAt = &expiresAt
	}
	s.pendingRefreshes[key] = refreshRequest{
		key: key, profileID: profileID, principal: principal,
		from: from, to: to, language: language,
	}
	s.refreshQueue = append(s.refreshQueue, key)
	s.refreshMu.Unlock()
	s.wakeRefreshWorker()
}

func (s *Service) takeRefresh(now time.Time) (refreshRequest, bool) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	for index := 0; index < len(s.refreshQueue); {
		key := s.refreshQueue[index]
		request, pending := s.pendingRefreshes[key]
		if !pending {
			s.refreshQueue = append(s.refreshQueue[:index], s.refreshQueue[index+1:]...)
			continue
		}
		if !request.notBefore.IsZero() && request.notBefore.After(now) {
			index++
			continue
		}
		s.refreshQueue = append(s.refreshQueue[:index], s.refreshQueue[index+1:]...)
		delete(s.pendingRefreshes, key)
		s.runningRefreshes[key] = struct{}{}
		return request, true
	}
	return refreshRequest{}, false
}

func (s *Service) finishRefresh(request refreshRequest, result refreshTurnResult, allowContinuation bool) {
	s.refreshMu.Lock()
	delete(s.runningRefreshes, request.key)
	if result.continuation && allowContinuation {
		if result.retryAt.IsZero() {
			result.retryAt = s.now().UTC().Add(s.refreshMinimumInterval)
		}
		request.notBefore = result.retryAt
		if _, pending := s.pendingRefreshes[request.key]; !pending {
			s.pendingRefreshes[request.key] = request
			s.refreshQueue = append(s.refreshQueue, request.key)
		}
	}
	s.refreshMu.Unlock()
}

func (s *Service) initializeRefreshState() {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if s.refreshMinimumInterval <= 0 {
		s.refreshMinimumInterval = defaultRefreshMinimum
	}
	if s.refreshClaimLease <= 0 {
		s.refreshClaimLease = defaultRefreshClaimLease
	}
	if s.refreshTurnTimeout <= 0 {
		s.refreshTurnTimeout = defaultRefreshTurnTimeout
	}
	if s.refreshTurnTimeout >= s.refreshClaimLease {
		s.refreshTurnTimeout = s.refreshClaimLease / 2
	}
	if s.titlePageSize <= 0 || s.titlePageSize > calendarTitlePageSize {
		s.titlePageSize = calendarTitlePageSize
	}
	if s.seasonBudget <= 0 || s.seasonBudget > calendarSeasonBudget {
		s.seasonBudget = calendarSeasonBudget
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	if s.refreshWake == nil {
		s.refreshWake = make(chan struct{}, 1)
	}
	if s.pendingRefreshes == nil {
		s.pendingRefreshes = make(map[string]refreshRequest)
	}
	if s.runningRefreshes == nil {
		s.runningRefreshes = make(map[string]struct{})
	}
}

func (s *Service) refresh(ctx context.Context, request refreshRequest) (refreshTurnResult, error) {
	if _, err := activeProfileID(request.principal, s.now().UTC()); err != nil {
		return refreshTurnResult{}, err
	}
	token, err := newRefreshClaimToken()
	if err != nil {
		return refreshTurnResult{}, err
	}
	cursor, titles, principal, claimed, retryAt, err := s.claimRefreshPage(ctx, request, token)
	if err != nil {
		return refreshTurnResult{}, err
	}
	if !claimed {
		return refreshTurnResult{continuation: true, retryAt: retryAt}, nil
	}

	nextCursor, continuation, refreshErr := s.refreshLibraryMetadata(
		ctx, principal, titles, cursor, cursor.From, cursor.To, cursor.Language,
	)
	cycleComplete := !continuation && ctx.Err() == nil
	committed, retryAt, commitErr := s.commitRefresh(
		ctx, request.profileID, token, nextCursor, cycleComplete,
	)
	if commitErr != nil {
		return refreshTurnResult{}, errors.Join(refreshErr, commitErr)
	}
	if !committed {
		return refreshTurnResult{}, refreshErr
	}
	return refreshTurnResult{
		continuation:  continuation,
		cycleComplete: cycleComplete,
		retryAt:       retryAt,
	}, errors.Join(refreshErr, ctx.Err())
}

func newRefreshClaimToken() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("generate calendar refresh claim token: %w", err)
	}
	return hex.EncodeToString(token[:]), nil
}

func (s *Service) claimRefreshPage(
	ctx context.Context,
	request refreshRequest,
	token string,
) (refreshCursor, []libraryTitle, auth.Principal, bool, time.Time, error) {
	if s.pool == nil {
		cursor, claimed, retryAt, err := s.repository.ClaimRefresh(
			ctx, nil, request.profileID, token, s.now().UTC().Add(s.refreshClaimLease),
			request.from, request.to, request.language,
		)
		if err != nil || !claimed {
			return refreshCursor{}, nil, auth.Principal{}, claimed, retryAt, err
		}
		titles, err := s.repository.LibraryTitlePage(ctx, nil, request.profileID, cursor, s.titlePageSize)
		return cursor, titles, request.principal, true, time.Time{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return refreshCursor{}, nil, auth.Principal{}, false, time.Time{}, fmt.Errorf("begin calendar refresh claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := s.now().UTC()
	principal, valid, err := auth.ReloadAndLockPrincipal(ctx, tx, request.principal, now, s.location)
	if err != nil {
		return refreshCursor{}, nil, auth.Principal{}, false, time.Time{}, fmt.Errorf("reload calendar refresh principal: %w", err)
	}
	if !valid || principal.ActiveProfileID == nil || *principal.ActiveProfileID != request.profileID {
		return refreshCursor{}, nil, auth.Principal{}, false, time.Time{}, ErrProfileRequired
	}
	cursor, claimed, retryAt, err := s.repository.ClaimRefresh(
		ctx, tx, request.profileID, token, now.Add(s.refreshClaimLease),
		request.from, request.to, request.language,
	)
	if err != nil || !claimed {
		return refreshCursor{}, nil, auth.Principal{}, claimed, retryAt, err
	}
	titles, err := s.repository.LibraryTitlePage(ctx, tx, request.profileID, cursor, s.titlePageSize)
	if err != nil {
		return refreshCursor{}, nil, auth.Principal{}, false, time.Time{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return refreshCursor{}, nil, auth.Principal{}, false, time.Time{}, fmt.Errorf("commit calendar refresh claim: %w", err)
	}
	return cursor, titles, principal, true, time.Time{}, nil
}

func (s *Service) commitRefresh(
	ctx context.Context,
	profileID, token string,
	cursor refreshCursor,
	cycleComplete bool,
) (bool, time.Time, error) {
	commitContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if s.pool == nil {
		return s.repository.CompleteRefresh(
			commitContext, nil, profileID, token, cursor, cycleComplete,
		)
	}
	tx, err := s.pool.Begin(commitContext)
	if err != nil {
		return false, time.Time{}, fmt.Errorf("begin calendar refresh completion: %w", err)
	}
	defer func() { _ = tx.Rollback(commitContext) }()
	committed, retryAt, err := s.repository.CompleteRefresh(
		commitContext, tx, profileID, token, cursor, cycleComplete,
	)
	if err != nil {
		return false, time.Time{}, err
	}
	if err := tx.Commit(commitContext); err != nil {
		return false, time.Time{}, fmt.Errorf("commit calendar refresh completion: %w", err)
	}
	return committed, retryAt, nil
}

type titleRefreshResult struct {
	complete          bool
	afterSeasonNumber int
	afterSeasonID     string
	hasSeasonCursor   bool
	err               error
}

func (s *Service) refreshLibraryMetadata(
	ctx context.Context,
	principal auth.Principal,
	page []libraryTitle,
	cursor refreshCursor,
	from, to time.Time,
	language string,
) (refreshCursor, bool, error) {
	hasMoreTitles := len(page) == s.titlePageSize
	if len(page) == 0 {
		return refreshCursor{}, false, nil
	}

	next := cursor
	seasonsStarted := 0
	var refreshErrors []error
	for _, title := range page {
		if ctx.Err() != nil {
			if title.ID == cursor.ResumeTitleID {
				return cursor, true, errors.Join(refreshErrors...)
			}
			next.ResumeTitleID = title.ID
			next.ResumeAfterSeasonNumber = 0
			next.ResumeAfterSeasonID = ""
			next.HasSeasonCursor = false
			return next, true, errors.Join(refreshErrors...)
		}
		result := s.refreshTitleMetadata(
			ctx, principal, title, cursor, &seasonsStarted, from, to, language,
		)
		if result.err != nil && !errors.Is(result.err, metadata.ErrNotFound) && !errors.Is(result.err, context.Canceled) {
			refreshErrors = append(refreshErrors, fmt.Errorf("refresh title %s: %w", title.ID, result.err))
		}
		if !result.complete {
			next.ResumeTitleID = title.ID
			next.ResumeAfterSeasonNumber = result.afterSeasonNumber
			next.ResumeAfterSeasonID = result.afterSeasonID
			next.HasSeasonCursor = result.hasSeasonCursor
			return next, true, errors.Join(refreshErrors...)
		}
		next.AfterTitleID = title.ID
		next.ResumeTitleID = ""
		next.ResumeAfterSeasonNumber = 0
		next.ResumeAfterSeasonID = ""
		next.HasSeasonCursor = false
	}
	return next, hasMoreTitles, errors.Join(refreshErrors...)
}

func (s *Service) refreshTitleMetadata(
	ctx context.Context,
	principal auth.Principal,
	title libraryTitle,
	cursor refreshCursor,
	seasonsStarted *int,
	from, to time.Time,
	language string,
) titleRefreshResult {
	switch title.MediaType {
	case metadata.MediaTypeMovie:
		_, err := s.metadata.MovieDetails(ctx, principal, title.ID, language)
		return titleRefreshResult{complete: ctx.Err() == nil, err: err}
	case metadata.MediaTypeSeries:
		series, err := s.metadata.SeriesDetails(ctx, principal, title.ID, metadata.SeriesDetailsOptions{Language: language, MappingProvider: "tmdb"})
		if err != nil {
			return titleRefreshResult{complete: ctx.Err() == nil, err: err}
		}
		sort.Slice(series.Seasons, func(i, j int) bool {
			if series.Seasons[i].SeasonNumber != series.Seasons[j].SeasonNumber {
				return series.Seasons[i].SeasonNumber < series.Seasons[j].SeasonNumber
			}
			return series.Seasons[i].ID < series.Seasons[j].ID
		})
		result := titleRefreshResult{}
		if cursor.ResumeTitleID == title.ID && cursor.HasSeasonCursor {
			result.afterSeasonNumber = cursor.ResumeAfterSeasonNumber
			result.afterSeasonID = cursor.ResumeAfterSeasonID
			result.hasSeasonCursor = true
		}
		for _, season := range series.Seasons {
			if !seasonMayOverlap(season.AirDate, from, to) ||
				(result.hasSeasonCursor && seasonAtOrBefore(season, result.afterSeasonNumber, result.afterSeasonID)) {
				continue
			}
			if *seasonsStarted >= s.seasonBudget {
				return result
			}
			*seasonsStarted = *seasonsStarted + 1
			_, seasonErr := s.metadata.SeasonDetails(ctx, principal, season.ID, language, "tmdb")
			if ctx.Err() != nil {
				result.err = errors.Join(result.err, seasonErr)
				return result
			}
			result.afterSeasonNumber = season.SeasonNumber
			result.afterSeasonID = season.ID
			result.hasSeasonCursor = true
			if seasonErr != nil {
				result.err = errors.Join(result.err, fmt.Errorf("refresh season %d: %w", season.SeasonNumber, seasonErr))
			}
		}
		result.complete = true
		return result
	default:
		return titleRefreshResult{complete: true}
	}
}

func seasonAtOrBefore(season metadata.SeasonSummary, number int, id string) bool {
	return season.SeasonNumber < number || (season.SeasonNumber == number && season.ID <= id)
}

func seasonMayOverlap(airDate string, from, to time.Time) bool {
	firstRelease, err := time.Parse(time.DateOnly, strings.TrimSpace(airDate))
	if err != nil || firstRelease.After(to) {
		return false
	}
	return !firstRelease.AddDate(1, 0, 0).Before(from)
}

func (repository *postgresRepository) LibraryTitlePage(
	ctx context.Context,
	tx pgx.Tx,
	profileID string,
	cursor refreshCursor,
	limit int,
) ([]libraryTitle, error) {
	rows, err := tx.Query(ctx, `
		SELECT title.id::text, title.media_type
		FROM profile_library AS library
		JOIN titles AS title ON title.id = library.title_id
		WHERE library.profile_id = $1::uuid
		  AND title.media_type IN ('movie', 'series')
		  AND CASE
			WHEN NULLIF($3, '') IS NOT NULL THEN title.id >= NULLIF($3, '')::uuid
			ELSE NULLIF($2, '') IS NULL OR title.id > NULLIF($2, '')::uuid
		  END
		ORDER BY title.id
		LIMIT $4
	`, profileID, cursor.AfterTitleID, cursor.ResumeTitleID, limit)
	if err != nil {
		return nil, fmt.Errorf("query calendar library title page: %w", err)
	}
	defer rows.Close()
	titles := make([]libraryTitle, 0, limit)
	for rows.Next() {
		var title libraryTitle
		if err := rows.Scan(&title.ID, &title.MediaType); err != nil {
			return nil, fmt.Errorf("scan calendar library title: %w", err)
		}
		titles = append(titles, title)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate calendar library titles: %w", err)
	}
	return titles, nil
}

func (repository *postgresRepository) ClaimRefresh(
	ctx context.Context,
	tx pgx.Tx,
	profileID, token string,
	expiresAt, requestedFrom, requestedTo time.Time,
	requestedLanguage string,
) (refreshCursor, bool, time.Time, error) {
	if token == "" {
		return refreshCursor{}, false, time.Time{}, fmt.Errorf("claim calendar refresh: token is required")
	}
	var cursor refreshCursor
	var claimed bool
	var retryAt time.Time
	err := tx.QueryRow(ctx, `
		WITH claimed AS (
			INSERT INTO calendar_refresh_state (
				profile_id, range_from, range_to, language,
				claim_token, claim_expires_at, updated_at
			)
			VALUES ($1::uuid, $4::date, $5::date, $6, $2, $3, now())
			ON CONFLICT (profile_id) DO UPDATE
			SET range_from = CASE
					WHEN calendar_refresh_state.claim_token IS NULL
					  AND calendar_refresh_state.after_title_id IS NULL
					  AND calendar_refresh_state.resume_title_id IS NULL
					THEN EXCLUDED.range_from
					ELSE calendar_refresh_state.range_from
			    END,
			    range_to = CASE
					WHEN calendar_refresh_state.claim_token IS NULL
					  AND calendar_refresh_state.after_title_id IS NULL
					  AND calendar_refresh_state.resume_title_id IS NULL
					THEN EXCLUDED.range_to
					ELSE calendar_refresh_state.range_to
			    END,
			    language = CASE
					WHEN calendar_refresh_state.claim_token IS NULL
					  AND calendar_refresh_state.after_title_id IS NULL
					  AND calendar_refresh_state.resume_title_id IS NULL
					THEN EXCLUDED.language
					ELSE calendar_refresh_state.language
			    END,
			    claim_token = EXCLUDED.claim_token,
			    claim_expires_at = EXCLUDED.claim_expires_at,
			    updated_at = now()
			WHERE (
				calendar_refresh_state.claim_token IS NULL
				OR calendar_refresh_state.claim_expires_at <= now()
			)
			  AND calendar_refresh_state.next_eligible_at <= now()
			RETURNING COALESCE(after_title_id::text, '') AS after_title_id,
			          COALESCE(resume_title_id::text, '') AS resume_title_id,
			          COALESCE(resume_after_season_number, 0) AS resume_after_season_number,
			          COALESCE(resume_after_season_id, '') AS resume_after_season_id,
			          resume_after_season_number IS NOT NULL AS has_season_cursor,
			          range_from, range_to, language
		)
		SELECT after_title_id, resume_title_id, resume_after_season_number,
		       resume_after_season_id, has_season_cursor,
		       range_from, range_to, language, true, now()
		FROM claimed
		UNION ALL
		SELECT '', '', 0, '', false,
		       state.range_from, state.range_to, state.language, false,
		       GREATEST(
		           state.next_eligible_at,
		           COALESCE(state.claim_expires_at, state.next_eligible_at)
		       )
		FROM calendar_refresh_state AS state
		WHERE state.profile_id = $1::uuid
		  AND NOT EXISTS (SELECT 1 FROM claimed)
		LIMIT 1
	`, profileID, token, expiresAt, requestedFrom, requestedTo, requestedLanguage).Scan(
		&cursor.AfterTitleID,
		&cursor.ResumeTitleID,
		&cursor.ResumeAfterSeasonNumber,
		&cursor.ResumeAfterSeasonID,
		&cursor.HasSeasonCursor,
		&cursor.From,
		&cursor.To,
		&cursor.Language,
		&claimed,
		&retryAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if retryErr := tx.QueryRow(ctx, `
			SELECT GREATEST(
				next_eligible_at,
				COALESCE(claim_expires_at, next_eligible_at)
			)
			FROM calendar_refresh_state
			WHERE profile_id = $1::uuid
		`, profileID).Scan(&retryAt); retryErr != nil {
			return refreshCursor{}, false, time.Time{}, fmt.Errorf("read calendar refresh retry deadline: %w", retryErr)
		}
		return refreshCursor{}, false, retryAt, nil
	}
	if err != nil {
		return refreshCursor{}, false, time.Time{}, fmt.Errorf("claim calendar refresh: %w", err)
	}
	if !claimed {
		return refreshCursor{}, false, retryAt, nil
	}
	return cursor, true, time.Time{}, nil
}

func (repository *postgresRepository) CompleteRefresh(
	ctx context.Context,
	tx pgx.Tx,
	profileID, token string,
	cursor refreshCursor,
	cycleComplete bool,
) (bool, time.Time, error) {
	if token == "" {
		return false, time.Time{}, fmt.Errorf("complete calendar refresh: token is required")
	}
	var completed bool
	var nextEligibleAt time.Time
	err := tx.QueryRow(ctx, `
		UPDATE calendar_refresh_state
		SET after_title_id = CASE WHEN $8::boolean THEN NULL ELSE NULLIF($3, '')::uuid END,
		    resume_title_id = CASE WHEN $8::boolean THEN NULL ELSE NULLIF($4, '')::uuid END,
		    resume_after_season_number = CASE WHEN $8::boolean OR NOT $7::boolean THEN NULL ELSE $5::integer END,
		    resume_after_season_id = CASE WHEN $8::boolean OR NOT $7::boolean THEN NULL ELSE NULLIF($6, '')::text END,
		    claim_token = NULL,
		    claim_expires_at = NULL,
		    next_eligible_at = now() + interval '5 minutes',
		    updated_at = now()
		WHERE profile_id = $1::uuid
		  AND claim_token = $2
		RETURNING true, next_eligible_at
	`, profileID, token, cursor.AfterTitleID, cursor.ResumeTitleID,
		cursor.ResumeAfterSeasonNumber, cursor.ResumeAfterSeasonID,
		cursor.HasSeasonCursor, cycleComplete,
	).Scan(&completed, &nextEligibleAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, time.Time{}, nil
	}
	if err != nil {
		return false, time.Time{}, fmt.Errorf("complete calendar refresh: %w", err)
	}
	return completed, nextEligibleAt, nil
}

const listEventsSQL = `
	SELECT event.id, event.title_id, event.media_type, event.title,
	       event.release_date, event.poster_url, event.resource_id,
	       event.resource_provider, event.series_title, event.series_id,
	       event.season_id, event.season_number, event.episode_number,
	       event.updated_at
	FROM (
		SELECT movie.id::text AS id, movie.id::text AS title_id,
		       'movie'::text AS media_type, movie.display_title AS title,
		       movie.release_date, COALESCE(movie.poster_url, '') AS poster_url,
		       COALESCE(movie.resource_id, '') AS resource_id,
		       COALESCE(movie.resource_provider, '') AS resource_provider,
		       ''::text AS series_title, ''::text AS series_id,
		       ''::text AS season_id, NULL::integer AS season_number,
		       NULL::integer AS episode_number, movie.updated_at
		FROM profile_library AS library
		JOIN titles AS movie ON movie.id = library.title_id
		WHERE library.profile_id = $1::uuid
		  AND movie.media_type = 'movie'
		  AND movie.display_title IS NOT NULL
		  AND movie.release_date BETWEEN $2::date AND $3::date

		UNION ALL

		SELECT episode.id::text AS id, episode.id::text AS title_id,
		       'episode'::text AS media_type, episode.display_title AS title,
		       episode.release_date,
		       COALESCE(episode.poster_url, series.poster_url, '') AS poster_url,
		       COALESCE(series.resource_id, '') AS resource_id,
		       COALESCE(series.resource_provider, '') AS resource_provider,
		       series.display_title AS series_title, series.id::text AS series_id,
		       season.id::text AS season_id, season.ordinal AS season_number,
		       episode.ordinal AS episode_number,
		       GREATEST(series.updated_at, season.updated_at, episode.updated_at) AS updated_at
		FROM profile_library AS library
		JOIN titles AS series
		  ON series.id = library.title_id AND series.media_type = 'series'
		JOIN titles AS season
		  ON season.parent_id = series.id AND season.media_type = 'season'
		JOIN titles AS episode
		  ON episode.parent_id = season.id AND episode.media_type = 'episode'
		WHERE library.profile_id = $1::uuid
		  AND series.display_title IS NOT NULL
		  AND episode.display_title IS NOT NULL
		  AND episode.release_date BETWEEN $2::date AND $3::date
	) AS event
	LIMIT $4
`

func (repository *postgresRepository) List(ctx context.Context, tx pgx.Tx, profileID string, from, to time.Time) ([]Event, error) {
	rows, err := tx.Query(ctx, listEventsSQL, profileID, from.Format(time.DateOnly), to.Format(time.DateOnly), calendarEventQueryLimit)
	if err != nil {
		return nil, fmt.Errorf("query calendar events: %w", err)
	}
	defer rows.Close()

	events := make([]Event, 0)
	for rows.Next() {
		var event Event
		var releaseDate time.Time
		if err := rows.Scan(
			&event.ID, &event.TitleID, &event.MediaType, &event.Title,
			&releaseDate, &event.PosterURL, &event.ResourceID,
			&event.ResourceProvider, &event.SeriesTitle, &event.SeriesID,
			&event.SeasonID, &event.SeasonNumber, &event.EpisodeNumber, &event.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan calendar event: %w", err)
		}
		event.ReleaseDate = releaseDate.Format(time.DateOnly)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate calendar events: %w", err)
	}
	return events, nil
}

func sortEvents(events []Event) {
	ordinal := func(value *int) int {
		if value == nil {
			return -1
		}
		return *value
	}
	sort.Slice(events, func(left, right int) bool {
		a, b := events[left], events[right]
		if a.ReleaseDate != b.ReleaseDate {
			return a.ReleaseDate < b.ReleaseDate
		}
		aTitle, bTitle := a.Title, b.Title
		if a.SeriesTitle != "" {
			aTitle = a.SeriesTitle
		}
		if b.SeriesTitle != "" {
			bTitle = b.SeriesTitle
		}
		if aTitle != bTitle {
			return aTitle < bTitle
		}
		if aSeason, bSeason := ordinal(a.SeasonNumber), ordinal(b.SeasonNumber); aSeason != bSeason {
			return aSeason < bSeason
		}
		if aEpisode, bEpisode := ordinal(a.EpisodeNumber), ordinal(b.EpisodeNumber); aEpisode != bEpisode {
			return aEpisode < bEpisode
		}
		if a.MediaType != b.MediaType {
			return a.MediaType < b.MediaType
		}
		return a.TitleID < b.TitleID
	})
}

func normalizeRange(fromValue, toValue string) (time.Time, time.Time, error) {
	from, err := parseDate(fromValue)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: from must be a YYYY-MM-DD date", ErrInvalidInput)
	}
	to, err := parseDate(toValue)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: to must be a YYYY-MM-DD date", ErrInvalidInput)
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: from must not be after to", ErrInvalidInput)
	}
	inclusiveDays := int(to.Sub(from)/(24*time.Hour)) + 1
	if inclusiveDays > maximumRangeDays {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: range must not exceed %d inclusive days", ErrInvalidInput, maximumRangeDays)
	}
	return from, to, nil
}

func parseDate(value string) (time.Time, error) {
	if strings.TrimSpace(value) != value || len(value) != len("2006-01-02") {
		return time.Time{}, errors.New("invalid date")
	}
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil || parsed.Year() < 1 || parsed.Format(time.DateOnly) != value {
		return time.Time{}, errors.New("invalid date")
	}
	return parsed, nil
}

func activeProfileID(principal auth.Principal, now time.Time) (string, error) {
	if principal.ActiveProfileID == nil || principal.ProfileGrantExpiresAt == nil || !principal.ProfileGrantExpiresAt.After(now) {
		return "", ErrProfileRequired
	}
	return *principal.ActiveProfileID, nil
}
