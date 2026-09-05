package calendar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/metadata"
)

func newTestService(t testing.TB, pool *pgxpool.Pool, metadataService metadataReader, timezone string, logger *slog.Logger) *Service {
	t.Helper()
	service, err := NewService(pool, metadataService, timezone, logger)
	if err != nil {
		t.Fatalf("create calendar service: %v", err)
	}
	return service
}

type fakeEventRepository struct {
	profileID     string
	from          time.Time
	to            time.Time
	events        []Event
	err           error
	libraryTitles []libraryTitle
	listCalls     int
}

func (repository *fakeEventRepository) List(_ context.Context, _ pgx.Tx, profileID string, from, to time.Time) ([]Event, error) {
	repository.listCalls++
	repository.profileID, repository.from, repository.to = profileID, from, to
	return repository.events, repository.err
}

func (repository *fakeEventRepository) LibraryTitlePage(_ context.Context, _ pgx.Tx, profileID string, _ refreshCursor, limit int) ([]libraryTitle, error) {
	repository.profileID = profileID
	return append([]libraryTitle(nil), repository.libraryTitles[:min(limit, len(repository.libraryTitles))]...), repository.err
}

func (*fakeEventRepository) ClaimRefresh(
	_ context.Context, _ pgx.Tx, _, _ string, _ time.Time, from, to time.Time, language string,
) (refreshCursor, bool, time.Time, error) {
	return refreshCursor{From: from, To: to, Language: language}, true, time.Time{}, nil
}

func (*fakeEventRepository) CompleteRefresh(_ context.Context, _ pgx.Tx, _, _ string, _ refreshCursor, _ bool) (bool, time.Time, error) {
	return true, time.Now().Add(defaultRefreshMinimum), nil
}

type fakeMetadataReader struct {
	mu           sync.Mutex
	series       metadata.Series
	seasonCalls  []string
	seasonCalled chan struct{}
}

func (reader *fakeMetadataReader) MovieDetails(context.Context, auth.Principal, string, string) (metadata.Movie, error) {
	return metadata.Movie{}, nil
}

func (reader *fakeMetadataReader) SeriesDetails(context.Context, auth.Principal, string, metadata.SeriesDetailsOptions) (metadata.Series, error) {
	return reader.series, nil
}

func (reader *fakeMetadataReader) SeasonDetails(_ context.Context, _ auth.Principal, seasonID, _, _ string) (metadata.Season, error) {
	reader.mu.Lock()
	reader.seasonCalls = append(reader.seasonCalls, seasonID)
	reader.mu.Unlock()
	if reader.seasonCalled != nil {
		select {
		case reader.seasonCalled <- struct{}{}:
		default:
		}
	}
	return metadata.Season{}, nil
}

func (reader *fakeMetadataReader) recordedSeasonCalls() []string {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return append([]string(nil), reader.seasonCalls...)
}

func TestNormalizeRangeEnforcesStrictInclusiveBoundaries(t *testing.T) {
	from, to, err := normalizeRange("2026-01-01", "2026-04-03")
	if err != nil {
		t.Fatalf("expected 93-day range to be valid: %v", err)
	}
	if from.Format(time.DateOnly) != "2026-01-01" || to.Format(time.DateOnly) != "2026-04-03" {
		t.Fatalf("unexpected normalized range: %s to %s", from, to)
	}

	invalid := []struct {
		from string
		to   string
	}{
		{from: "2026-01-01", to: "2026-04-04"},
		{from: "2026-02-02", to: "2026-02-01"},
		{from: "2026-2-01", to: "2026-02-02"},
		{from: "2026-02-01T00:00:00Z", to: "2026-02-02"},
		{from: "2026-02-30", to: "2026-03-01"},
		{from: "", to: "2026-03-01"},
	}
	for _, test := range invalid {
		if _, _, err := normalizeRange(test.from, test.to); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid range for %q to %q, got %v", test.from, test.to, err)
		}
	}
}

func TestActiveProfileIDRequiresCurrentGrant(t *testing.T) {
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	profileID := "11111111-1111-4111-8111-111111111111"
	future := now.Add(time.Minute)
	past := now.Add(-time.Minute)

	got, err := activeProfileID(auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileID, ProfileGrantExpiresAt: &future}, now)
	if err != nil || got != profileID {
		t.Fatalf("expected active profile %q, got %q error %v", profileID, got, err)
	}
	for _, principal := range []auth.Principal{
		{},
		{ActiveProfileID: &profileID},
		{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &past},
	} {
		if _, err := activeProfileID(principal, now); !errors.Is(err, ErrProfileRequired) {
			t.Fatalf("expected inactive principal to be rejected, got %v", err)
		}
	}
}

func TestSortEventsIsDeterministic(t *testing.T) {
	seasonOne, episodeOne, episodeTwo := 1, 1, 2
	events := []Event{
		{TitleID: "d", MediaType: "movie", Title: "Zulu", ReleaseDate: "2026-02-02"},
		{TitleID: "c", MediaType: "episode", Title: "Second", SeriesTitle: "Alpha", SeasonNumber: &seasonOne, EpisodeNumber: &episodeTwo, ReleaseDate: "2026-02-02"},
		{TitleID: "b", MediaType: "episode", Title: "First", SeriesTitle: "Alpha", SeasonNumber: &seasonOne, EpisodeNumber: &episodeOne, ReleaseDate: "2026-02-02"},
		{TitleID: "a", MediaType: "movie", Title: "Earlier", ReleaseDate: "2026-02-01"},
	}

	sortEvents(events)
	got := make([]string, len(events))
	for index := range events {
		got[index] = events[index].TitleID
	}
	if want := []string{"a", "b", "c", "d"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected event order: got %v want %v", got, want)
	}
}

func TestListScopesEmptyResultsToActiveProfile(t *testing.T) {
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	profileID := "11111111-1111-4111-8111-111111111111"
	expiresAt := now.Add(time.Hour)
	repository := &fakeEventRepository{events: []Event{}}
	service := &Service{repository: repository, now: func() time.Time { return now }}

	result, err := service.List(
		context.Background(),
		auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt},
		"2026-07-01",
		"2026-07-31",
		"fr-FR",
	)
	if err != nil {
		t.Fatalf("list calendar: %v", err)
	}
	if repository.profileID != profileID {
		t.Fatalf("expected query to be scoped to active profile %q, got %q", profileID, repository.profileID)
	}
	if repository.from.Format(time.DateOnly) != "2026-07-01" || repository.to.Format(time.DateOnly) != "2026-07-31" {
		t.Fatalf("unexpected repository range: %s to %s", repository.from, repository.to)
	}
	if result.Events == nil || len(result.Events) != 0 {
		t.Fatalf("expected a non-nil empty event list, got %#v", result.Events)
	}
}

func TestListRejectsCapacitySentinelWithoutReturningPartialEvents(t *testing.T) {
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	profileID := "11111111-1111-4111-8111-111111111111"
	expiresAt := now.Add(time.Hour)
	principal := auth.Principal{
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt,
	}
	for _, test := range []struct {
		name    string
		count   int
		wantErr bool
	}{
		{name: "at capacity", count: maximumCalendarEvents},
		{name: "capacity sentinel", count: calendarEventQueryLimit, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeEventRepository{events: make([]Event, test.count)}
			service := &Service{repository: repository, now: func() time.Time { return now }}
			result, err := service.List(context.Background(), principal, "2026-07-01", "2026-07-31", "en-US")
			if test.wantErr {
				if !errors.Is(err, ErrCapacity) || result.Events != nil {
					t.Fatalf("over-capacity result = %d events, error %v; want no partial result and ErrCapacity", len(result.Events), err)
				}
				return
			}
			if err != nil || len(result.Events) != maximumCalendarEvents {
				t.Fatalf("at-capacity result = %d events, error %v", len(result.Events), err)
			}
		})
	}
}

func TestListEventsSQLReadsOnlyCapacitySentinel(t *testing.T) {
	normalized := strings.Join(strings.Fields(listEventsSQL), " ")
	if calendarEventQueryLimit != maximumCalendarEvents+1 || !strings.HasSuffix(normalized, "LIMIT $4") {
		t.Fatalf("calendar event query is not bounded to the capacity sentinel: limit=%d SQL=%s", calendarEventQueryLimit, normalized)
	}
}

func TestListRefreshesLibrarySeasonThatCanOverlapRequestedMonth(t *testing.T) {
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	profileID := "11111111-1111-4111-8111-111111111111"
	expiresAt := now.Add(time.Hour)
	repository := &fakeEventRepository{
		events:        []Event{},
		libraryTitles: []libraryTitle{{ID: "series-id", MediaType: metadata.MediaTypeSeries}},
	}
	reader := &fakeMetadataReader{
		series: metadata.Series{Seasons: []metadata.SeasonSummary{
			{ID: "old-season", SeasonNumber: 10, AirDate: "2024-01-01"},
			{ID: "august-season", SeasonNumber: 11, AirDate: "2026-08-03"},
			{ID: "future-season", SeasonNumber: 12, AirDate: "2027-01-01"},
		}},
		seasonCalled: make(chan struct{}, 1),
	}
	service := &Service{
		repository: repository,
		metadata:   reader,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:        func() time.Time { return now },
	}
	workerContext, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		service.Run(workerContext)
		close(workerDone)
	}()
	defer func() {
		stopWorker()
		<-workerDone
	}()

	if _, err := service.List(
		context.Background(),
		auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt},
		"2026-08-01",
		"2026-08-31",
		"fr-FR",
	); err != nil {
		t.Fatalf("list refreshed calendar: %v", err)
	}
	select {
	case <-reader.seasonCalled:
	case <-time.After(time.Second):
		t.Fatal("calendar refresh worker did not load the overlapping season")
	}
	if want, got := []string{"august-season"}, reader.recordedSeasonCalls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected refreshed seasons: got %v want %v", got, want)
	}
}

type refreshRepository struct {
	mu              sync.Mutex
	events          []Event
	titles          []libraryTitle
	titlesByProfile map[string][]libraryTitle
	states          map[string]fakeRefreshState
	now             func() time.Time
	libraryCalls    int
	libraryCalled   chan struct{}
	pageOrder       []string
}

type fakeRefreshState struct {
	cursor         refreshCursor
	claimToken     string
	expiresAt      time.Time
	nextEligibleAt time.Time
}

func (repository *refreshRepository) List(_ context.Context, _ pgx.Tx, _ string, _, _ time.Time) ([]Event, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return append([]Event(nil), repository.events...), nil
}

func (repository *refreshRepository) LibraryTitlePage(_ context.Context, _ pgx.Tx, profileID string, cursor refreshCursor, limit int) ([]libraryTitle, error) {
	repository.mu.Lock()
	repository.libraryCalls++
	repository.pageOrder = append(repository.pageOrder, profileID)
	source := repository.titles
	if repository.titlesByProfile != nil {
		source = repository.titlesByProfile[profileID]
	}
	titles := make([]libraryTitle, 0, min(limit, len(source)))
	for _, title := range source {
		if cursor.ResumeTitleID != "" {
			if title.ID < cursor.ResumeTitleID {
				continue
			}
		} else if cursor.AfterTitleID != "" && title.ID <= cursor.AfterTitleID {
			continue
		}
		titles = append(titles, title)
		if len(titles) == limit {
			break
		}
	}
	repository.mu.Unlock()
	if repository.libraryCalled != nil {
		select {
		case repository.libraryCalled <- struct{}{}:
		default:
		}
	}
	return titles, nil
}

func (repository *refreshRepository) ClaimRefresh(
	_ context.Context,
	_ pgx.Tx,
	profileID, token string,
	expiresAt, requestedFrom, requestedTo time.Time,
	requestedLanguage string,
) (refreshCursor, bool, time.Time, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.states == nil {
		repository.states = make(map[string]fakeRefreshState)
	}
	state := repository.states[profileID]
	now := time.Now()
	if repository.now != nil {
		now = repository.now()
	}
	if state.nextEligibleAt.After(now) || (state.claimToken != "" && state.expiresAt.After(now)) {
		retryAt := state.nextEligibleAt
		if state.expiresAt.After(retryAt) {
			retryAt = state.expiresAt
		}
		return refreshCursor{}, false, retryAt, nil
	}
	if state.claimToken == "" && state.cursor.AfterTitleID == "" && state.cursor.ResumeTitleID == "" {
		state.cursor.From = requestedFrom
		state.cursor.To = requestedTo
		state.cursor.Language = requestedLanguage
	}
	state.claimToken = token
	state.expiresAt = expiresAt
	repository.states[profileID] = state
	return state.cursor, true, time.Time{}, nil
}

func (repository *refreshRepository) CompleteRefresh(
	_ context.Context,
	_ pgx.Tx,
	profileID, token string,
	cursor refreshCursor,
	cycleComplete bool,
) (bool, time.Time, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state := repository.states[profileID]
	if state.claimToken != token {
		return false, time.Time{}, nil
	}
	if cycleComplete {
		state.cursor = refreshCursor{}
	} else {
		state.cursor = cursor
	}
	now := time.Now()
	if repository.now != nil {
		now = repository.now()
	}
	state.claimToken = ""
	state.expiresAt = time.Time{}
	state.nextEligibleAt = now.Add(defaultRefreshMinimum)
	repository.states[profileID] = state
	return true, state.nextEligibleAt, nil
}

func (repository *refreshRepository) setEvents(events []Event) {
	repository.mu.Lock()
	repository.events = append([]Event(nil), events...)
	repository.mu.Unlock()
}

func (repository *refreshRepository) recordedLibraryCalls() int {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.libraryCalls
}

type blockingMetadataReader struct {
	mu            sync.Mutex
	calls         int
	started       chan struct{}
	startedCall   chan struct{}
	release       chan struct{}
	completed     chan struct{}
	canceled      chan struct{}
	startedOnce   sync.Once
	completedOnce sync.Once
	canceledOnce  sync.Once
	err           error
	onSuccess     func()
}

func newBlockingMetadataReader() *blockingMetadataReader {
	return &blockingMetadataReader{
		started: make(chan struct{}), startedCall: make(chan struct{}, 16),
		release: make(chan struct{}), completed: make(chan struct{}), canceled: make(chan struct{}),
	}
}

func (reader *blockingMetadataReader) MovieDetails(ctx context.Context, _ auth.Principal, _, _ string) (metadata.Movie, error) {
	err := reader.call(ctx)
	return metadata.Movie{}, err
}

func (reader *blockingMetadataReader) SeriesDetails(ctx context.Context, _ auth.Principal, _ string, _ metadata.SeriesDetailsOptions) (metadata.Series, error) {
	err := reader.call(ctx)
	return metadata.Series{}, err
}

func (reader *blockingMetadataReader) SeasonDetails(ctx context.Context, _ auth.Principal, _, _, _ string) (metadata.Season, error) {
	err := reader.call(ctx)
	return metadata.Season{}, err
}

func (reader *blockingMetadataReader) call(ctx context.Context) error {
	reader.mu.Lock()
	reader.calls++
	reader.mu.Unlock()
	reader.startedCall <- struct{}{}
	reader.startedOnce.Do(func() { close(reader.started) })
	select {
	case <-reader.release:
		if reader.err == nil && reader.onSuccess != nil {
			reader.onSuccess()
		}
		reader.completedOnce.Do(func() { close(reader.completed) })
		return reader.err
	case <-ctx.Done():
		reader.canceledOnce.Do(func() { close(reader.canceled) })
		reader.completedOnce.Do(func() { close(reader.completed) })
		return ctx.Err()
	}
}

func (reader *blockingMetadataReader) recordedCalls() int {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.calls
}

func newRefreshService(now time.Time, repository eventRepository, reader metadataReader) *Service {
	clock := func() time.Time { return now }
	if refreshRepository, ok := repository.(*refreshRepository); ok {
		refreshRepository.now = clock
	}
	return &Service{
		repository: repository, metadata: reader,
		logger:                 slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:                    clock,
		refreshMinimumInterval: time.Hour,
	}
}

func refreshPrincipal(now time.Time) auth.Principal {
	profileID := "11111111-1111-4111-8111-111111111111"
	expiresAt := now.Add(time.Hour)
	return auth.Principal{
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt,
	}
}

func startRefreshWorker(service *Service) (context.CancelFunc, <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		service.Run(ctx)
		close(done)
	}()
	return cancel, done
}

func TestListReturnsCachedEventsWhileProviderIsBlocked(t *testing.T) {
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	cached := Event{ID: "cached", TitleID: "movie-id", MediaType: metadata.MediaTypeMovie, ReleaseDate: "2026-08-10"}
	repository := &refreshRepository{
		events: []Event{cached},
		titles: []libraryTitle{{ID: "movie-id", MediaType: metadata.MediaTypeMovie}},
	}
	reader := newBlockingMetadataReader()
	service := newRefreshService(now, repository, reader)
	stopWorker, workerDone := startRefreshWorker(service)
	defer func() {
		close(reader.release)
		stopWorker()
		<-workerDone
	}()

	startedAt := time.Now()
	result, err := service.List(context.Background(), refreshPrincipal(now), "2026-08-01", "2026-08-31", "en-US")
	if err != nil {
		t.Fatalf("list cached calendar: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 250*time.Millisecond {
		t.Fatalf("List waited %s for the blocked provider", elapsed)
	}
	if len(result.Events) != 1 || result.Events[0].ID != cached.ID {
		t.Fatalf("List did not return the cached event: %+v", result.Events)
	}
	select {
	case <-reader.started:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not reach the provider")
	}
	select {
	case <-reader.completed:
		t.Fatal("provider call completed before the blocked provider was released")
	default:
	}
}

func TestConcurrentListsDeduplicateRefreshAndSuccessfulRefreshBecomesVisible(t *testing.T) {
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	cached := Event{ID: "cached", TitleID: "movie-id", MediaType: metadata.MediaTypeMovie, ReleaseDate: "2026-08-10"}
	fresh := Event{ID: "fresh", TitleID: "movie-id", MediaType: metadata.MediaTypeMovie, ReleaseDate: "2026-08-11"}
	repository := &refreshRepository{
		events: []Event{cached},
		titles: []libraryTitle{{ID: "movie-id", MediaType: metadata.MediaTypeMovie}},
	}
	reader := newBlockingMetadataReader()
	reader.onSuccess = func() { repository.setEvents([]Event{fresh}) }
	service := newRefreshService(now, repository, reader)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var lists sync.WaitGroup
	lists.Add(2)
	for range 2 {
		go func() {
			defer lists.Done()
			<-start
			_, err := service.List(context.Background(), refreshPrincipal(now), "2026-08-01", "2026-08-31", "en-US")
			errs <- err
		}()
	}
	close(start)
	lists.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent List: %v", err)
		}
	}

	close(reader.release)
	stopWorker, workerDone := startRefreshWorker(service)
	defer func() {
		stopWorker()
		<-workerDone
	}()
	select {
	case <-reader.completed:
	case <-time.After(time.Second):
		t.Fatal("deduplicated refresh did not complete")
	}
	if calls := reader.recordedCalls(); calls != 1 {
		t.Fatalf("provider calls = %d, want exactly one", calls)
	}
	result, err := service.List(context.Background(), refreshPrincipal(now), "2026-08-01", "2026-08-31", "en-US")
	if err != nil {
		t.Fatalf("list refreshed calendar: %v", err)
	}
	if len(result.Events) != 1 || result.Events[0].ID != fresh.ID {
		t.Fatalf("successful refresh was not visible: %+v", result.Events)
	}
}

func TestFailedRefreshPreservesCachedEvents(t *testing.T) {
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	cached := Event{ID: "cached", TitleID: "movie-id", MediaType: metadata.MediaTypeMovie, ReleaseDate: "2026-08-10"}
	repository := &refreshRepository{
		events: []Event{cached},
		titles: []libraryTitle{{ID: "movie-id", MediaType: metadata.MediaTypeMovie}},
	}
	reader := newBlockingMetadataReader()
	reader.err = errors.New("provider unavailable")
	close(reader.release)
	service := newRefreshService(now, repository, reader)
	stopWorker, workerDone := startRefreshWorker(service)
	defer func() {
		stopWorker()
		<-workerDone
	}()

	if _, err := service.List(context.Background(), refreshPrincipal(now), "2026-08-01", "2026-08-31", "en-US"); err != nil {
		t.Fatalf("initial cached List: %v", err)
	}
	select {
	case <-reader.completed:
	case <-time.After(time.Second):
		t.Fatal("failed refresh did not complete")
	}
	result, err := service.List(context.Background(), refreshPrincipal(now), "2026-08-01", "2026-08-31", "en-US")
	if err != nil {
		t.Fatalf("List after failed refresh: %v", err)
	}
	if len(result.Events) != 1 || result.Events[0].ID != cached.ID {
		t.Fatalf("failed refresh changed cached events: %+v", result.Events)
	}
	if calls := reader.recordedCalls(); calls != 1 {
		t.Fatalf("failure was retried aggressively: provider calls = %d", calls)
	}
}

func TestRefreshWorkerCancellationCancelsBlockedProviderAndResumesCursor(t *testing.T) {
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	repository := &refreshRepository{
		events: []Event{},
		titles: []libraryTitle{
			{ID: "movie-1", MediaType: metadata.MediaTypeMovie},
			{ID: "movie-2", MediaType: metadata.MediaTypeMovie},
			{ID: "movie-3", MediaType: metadata.MediaTypeMovie},
			{ID: "movie-4", MediaType: metadata.MediaTypeMovie},
			{ID: "movie-5", MediaType: metadata.MediaTypeMovie},
		},
	}
	reader := newBlockingMetadataReader()
	service := newRefreshService(now, repository, reader)
	stopWorker, workerDone := startRefreshWorker(service)
	if _, err := service.List(context.Background(), refreshPrincipal(now), "2026-08-01", "2026-08-31", "en-US"); err != nil {
		t.Fatalf("List before shutdown: %v", err)
	}
	select {
	case <-reader.startedCall:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start the sequential provider call")
	}

	stopWorker()
	select {
	case <-reader.canceled:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel provider calls")
	}
	select {
	case <-workerDone:
	case <-time.After(time.Second):
		t.Fatal("refresh worker did not join after cancellation")
	}
	if calls := reader.recordedCalls(); calls != 1 {
		t.Fatalf("provider calls after cancellation = %d, want exactly one", calls)
	}
	repository.mu.Lock()
	resume := repository.states[*refreshPrincipal(now).ActiveProfileID].cursor.ResumeTitleID
	repository.mu.Unlock()
	if resume != "movie-1" {
		t.Fatalf("canceled prefix cursor resumes at %q, want movie-1", resume)
	}

	retryReader := &budgetMetadataReader{}
	service.metadata = retryReader
	retryAt := now.Add(service.refreshMinimumInterval)
	service.now = func() time.Time { return retryAt }
	repository.now = service.now
	service.enqueueRefresh(refreshPrincipal(retryAt), *refreshPrincipal(retryAt).ActiveProfileID, now, now.AddDate(0, 0, 30), "en-US")
	retry, ok := service.takeRefresh(retryAt)
	if !ok {
		t.Fatal("canceled refresh was not recoverable")
	}
	if _, err := service.refresh(context.Background(), retry); err != nil {
		t.Fatalf("resume canceled refresh: %v", err)
	}
	retryReader.mu.Lock()
	retriedIDs := append([]string(nil), retryReader.movieIDs...)
	retryReader.mu.Unlock()
	sort.Strings(retriedIDs)
	if len(retriedIDs) == 0 || retriedIDs[0] != "movie-1" {
		t.Fatalf("resumed provider calls = %v, want first unfinished movie", retriedIDs)
	}
}

func TestRefreshPageCancellationPreservesExistingSeasonCursor(t *testing.T) {
	cursor := refreshCursor{
		ResumeTitleID:           "series-1",
		ResumeAfterSeasonNumber: 64,
		ResumeAfterSeasonID:     "season-64",
		HasSeasonCursor:         true,
		From:                    time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC),
		To:                      time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC),
		Language:                "en-US",
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := &budgetMetadataReader{}
	service := &Service{metadata: reader, titlePageSize: calendarTitlePageSize, seasonBudget: calendarSeasonBudget}
	next, continuation, err := service.refreshLibraryMetadata(
		ctx,
		refreshPrincipal(time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)),
		[]libraryTitle{{ID: cursor.ResumeTitleID, MediaType: metadata.MediaTypeSeries}},
		cursor,
		cursor.From,
		cursor.To,
		cursor.Language,
	)
	if err != nil || !continuation {
		t.Fatalf("canceled resumed page: continuation=%v error=%v", continuation, err)
	}
	if !reflect.DeepEqual(next, cursor) {
		t.Fatalf("canceled resumed cursor = %+v, want unchanged %+v", next, cursor)
	}
	if titleCalls, seasonCalls, _ := reader.counts(); titleCalls != 0 || seasonCalls != 0 {
		t.Fatalf("canceled resumed page reached provider: titles=%d seasons=%d", titleCalls, seasonCalls)
	}
}

func TestEmptyBootstrapIsDeduplicatedWithinRefreshInterval(t *testing.T) {
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	repository := &refreshRepository{events: []Event{}, titles: []libraryTitle{}, libraryCalled: make(chan struct{}, 2)}
	service := newRefreshService(now, repository, &fakeMetadataReader{})
	for range 2 {
		if _, err := service.List(context.Background(), refreshPrincipal(now), "2026-08-01", "2026-08-31", "en-US"); err != nil {
			t.Fatalf("empty bootstrap List: %v", err)
		}
	}
	stopWorker, workerDone := startRefreshWorker(service)
	defer func() {
		stopWorker()
		<-workerDone
	}()
	select {
	case <-repository.libraryCalled:
	case <-time.After(time.Second):
		t.Fatal("empty bootstrap refresh did not run")
	}
	if _, err := service.List(context.Background(), refreshPrincipal(now), "2026-08-01", "2026-08-31", "en-US"); err != nil {
		t.Fatalf("repeat empty bootstrap List: %v", err)
	}
	select {
	case <-repository.libraryCalled:
		t.Fatal("empty bootstrap was refreshed again without waiting for the refresh interval")
	case <-time.After(50 * time.Millisecond):
	}
	if calls := repository.recordedLibraryCalls(); calls != 1 {
		t.Fatalf("empty bootstrap library queries = %d, want one", calls)
	}
}

type budgetMetadataReader struct {
	mu              sync.Mutex
	titleCalls      int
	seasonCalls     int
	inFlight        int
	maximumInFlight int
	movieIDs        []string
	seriesIDs       []string
	seasons         []metadata.SeasonSummary
	seasonIDs       []string
}

func (reader *budgetMetadataReader) begin() {
	reader.mu.Lock()
	reader.inFlight++
	if reader.inFlight > reader.maximumInFlight {
		reader.maximumInFlight = reader.inFlight
	}
	reader.mu.Unlock()
}

func (reader *budgetMetadataReader) end() {
	reader.mu.Lock()
	reader.inFlight--
	reader.mu.Unlock()
}

func (reader *budgetMetadataReader) MovieDetails(_ context.Context, _ auth.Principal, id, _ string) (metadata.Movie, error) {
	reader.begin()
	defer reader.end()
	reader.mu.Lock()
	reader.titleCalls++
	reader.movieIDs = append(reader.movieIDs, id)
	reader.mu.Unlock()
	return metadata.Movie{}, nil
}

func (reader *budgetMetadataReader) SeriesDetails(_ context.Context, _ auth.Principal, id string, _ metadata.SeriesDetailsOptions) (metadata.Series, error) {
	reader.begin()
	defer reader.end()
	reader.mu.Lock()
	reader.titleCalls++
	reader.seriesIDs = append(reader.seriesIDs, id)
	seasons := append([]metadata.SeasonSummary(nil), reader.seasons...)
	reader.mu.Unlock()
	return metadata.Series{Seasons: seasons}, nil
}

func (reader *budgetMetadataReader) SeasonDetails(_ context.Context, _ auth.Principal, seasonID string, _, _ string) (metadata.Season, error) {
	reader.begin()
	defer reader.end()
	reader.mu.Lock()
	reader.seasonCalls++
	reader.seasonIDs = append(reader.seasonIDs, seasonID)
	reader.mu.Unlock()
	return metadata.Season{}, nil
}

func (reader *budgetMetadataReader) counts() (int, int, int) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.titleCalls, reader.seasonCalls, reader.maximumInFlight
}

func TestRefreshPageEnforcesExactTitleSeasonAndConcurrencyBudgets(t *testing.T) {
	seasons := make([]metadata.SeasonSummary, calendarSeasonBudget+1)
	for index := range seasons {
		seasons[index] = metadata.SeasonSummary{
			ID: fmt.Sprintf("season-%03d", index), SeasonNumber: index, AirDate: "2026-08-01",
		}
	}
	reader := &budgetMetadataReader{seasons: seasons}
	service := &Service{
		metadata: reader, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		titlePageSize: calendarTitlePageSize, seasonBudget: calendarSeasonBudget,
	}
	titles := make([]libraryTitle, calendarTitlePageSize)
	for index := range titles {
		titles[index] = libraryTitle{ID: fmt.Sprintf("series-%03d", index), MediaType: metadata.MediaTypeSeries}
	}
	from := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC)
	cursor, continuation, err := service.refreshLibraryMetadata(context.Background(), refreshPrincipal(from), titles, refreshCursor{}, from, to, "en-US")
	if err != nil {
		t.Fatalf("refresh bounded page: %v", err)
	}
	titleCalls, seasonCalls, maximumInFlight := reader.counts()
	if titleCalls < 1 || titleCalls > calendarTitlePageSize {
		t.Fatalf("title calls = %d, want between one and %d", titleCalls, calendarTitlePageSize)
	}
	if seasonCalls != calendarSeasonBudget {
		t.Fatalf("season calls = %d, want exactly %d", seasonCalls, calendarSeasonBudget)
	}
	if maximumInFlight != 1 {
		t.Fatalf("maximum provider concurrency = %d, want sequential execution", maximumInFlight)
	}
	if !continuation || cursor.ResumeTitleID == "" {
		t.Fatalf("budget exhaustion did not preserve a resumable cursor: continuation=%v cursor=%+v", continuation, cursor)
	}
}

func TestRefreshPageResumesSeriesAtExactNextSeason(t *testing.T) {
	seasons := make([]metadata.SeasonSummary, calendarSeasonBudget+1)
	for index := range seasons {
		seasons[index] = metadata.SeasonSummary{
			ID: fmt.Sprintf("season-%03d", index), SeasonNumber: index, AirDate: "2026-08-01",
		}
	}
	firstReader := &budgetMetadataReader{seasons: seasons}
	service := &Service{
		metadata: firstReader, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		titlePageSize: calendarTitlePageSize, seasonBudget: calendarSeasonBudget,
	}
	from := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC)
	page := []libraryTitle{{ID: "series", MediaType: metadata.MediaTypeSeries}}
	cursor, continuation, err := service.refreshLibraryMetadata(context.Background(), refreshPrincipal(from), page, refreshCursor{}, from, to, "en-US")
	if err != nil || !continuation {
		t.Fatalf("first partial series turn: continuation=%v cursor=%+v error=%v", continuation, cursor, err)
	}
	if cursor.ResumeTitleID != "series" || cursor.ResumeAfterSeasonID != "season-063" {
		t.Fatalf("partial series cursor = %+v, want season-063", cursor)
	}

	secondReader := &budgetMetadataReader{seasons: seasons}
	service.metadata = secondReader
	cursor, continuation, err = service.refreshLibraryMetadata(context.Background(), refreshPrincipal(from), page, cursor, from, to, "en-US")
	if err != nil || continuation {
		t.Fatalf("resumed series turn: continuation=%v cursor=%+v error=%v", continuation, cursor, err)
	}
	secondReader.mu.Lock()
	resumedIDs := append([]string(nil), secondReader.seasonIDs...)
	secondReader.mu.Unlock()
	if want := []string{"season-064"}; !reflect.DeepEqual(resumedIDs, want) {
		t.Fatalf("resumed seasons = %v, want %v", resumedIDs, want)
	}
	if cursor.AfterTitleID != "series" || cursor.ResumeTitleID != "" {
		t.Fatalf("completed series cursor = %+v", cursor)
	}
}

func principalForProfile(now time.Time, profileID string) auth.Principal {
	expiresAt := now.Add(time.Hour)
	return auth.Principal{
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt,
	}
}

func TestRefreshWorkerRoundRobinsProfileContinuations(t *testing.T) {
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	const profileA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const profileB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	repository := &refreshRepository{titlesByProfile: map[string][]libraryTitle{
		profileA: {
			{ID: "a-1", MediaType: metadata.MediaTypeMovie},
			{ID: "a-2", MediaType: metadata.MediaTypeMovie},
			{ID: "a-3", MediaType: metadata.MediaTypeMovie},
			{ID: "a-4", MediaType: metadata.MediaTypeMovie},
			{ID: "a-5", MediaType: metadata.MediaTypeMovie},
		},
		profileB: {{ID: "b-1", MediaType: metadata.MediaTypeMovie}},
	}}
	reader := &budgetMetadataReader{}
	service := newRefreshService(now, repository, reader)
	service.titlePageSize = 2
	service.refreshMinimumInterval = time.Minute
	service.initializeRefreshState()
	service.enqueueRefresh(principalForProfile(now, profileA), profileA, now, now.AddDate(0, 0, 30), "en-US")
	firstA, ok := service.takeRefresh(now)
	if !ok {
		t.Fatal("profile A was not queued")
	}
	service.enqueueRefresh(principalForProfile(now, profileB), profileB, now, now.AddDate(0, 0, 30), "en-US")
	result, err := service.refresh(context.Background(), firstA)
	if err != nil {
		t.Fatalf("first profile A turn: %v", err)
	}
	service.finishRefresh(firstA, result, true)

	second, ok := service.takeRefresh(now)
	if !ok || second.profileID != profileB {
		t.Fatalf("next turn = %q, want pending profile B before cooling A continuation", second.profileID)
	}
	result, err = service.refresh(context.Background(), second)
	if err != nil {
		t.Fatalf("profile B turn: %v", err)
	}
	service.finishRefresh(second, result, true)
	now = now.Add(defaultRefreshMinimum)
	service.now = func() time.Time { return now }
	repository.now = service.now
	third, ok := service.takeRefresh(now)
	if !ok || third.profileID != profileA {
		t.Fatalf("third turn = %q, want profile A continuation after cooldown", third.profileID)
	}
	if got := append([]string(nil), repository.pageOrder...); !reflect.DeepEqual(got, []string{profileA, profileB}) {
		t.Fatalf("page order before resumed A = %v, want A/B", got)
	}
}

func TestLibraryTitlePageUsesStableKeysetAndHardLimit(t *testing.T) {
	repository := &refreshRepository{titles: []libraryTitle{
		{ID: "02", MediaType: metadata.MediaTypeMovie},
		{ID: "04", MediaType: metadata.MediaTypeMovie},
		{ID: "06", MediaType: metadata.MediaTypeMovie},
		{ID: "08", MediaType: metadata.MediaTypeMovie},
	}}
	page, err := repository.LibraryTitlePage(context.Background(), nil, "profile", refreshCursor{}, 3)
	if err != nil || len(page) != 3 {
		t.Fatalf("first LIMIT 3 page: len=%d error=%v", len(page), err)
	}
	cursor := refreshCursor{AfterTitleID: page[1].ID}
	repository.mu.Lock()
	repository.titles = []libraryTitle{
		{ID: "01", MediaType: metadata.MediaTypeMovie},
		{ID: "04", MediaType: metadata.MediaTypeMovie},
		{ID: "05", MediaType: metadata.MediaTypeMovie},
		{ID: "06", MediaType: metadata.MediaTypeMovie},
		{ID: "08", MediaType: metadata.MediaTypeMovie},
	}
	repository.mu.Unlock()
	page, err = repository.LibraryTitlePage(context.Background(), nil, "profile", cursor, 3)
	if err != nil {
		t.Fatalf("resume keyset page: %v", err)
	}
	got := make([]string, len(page))
	for index := range page {
		got[index] = page[index].ID
	}
	if want := []string{"05", "06", "08"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mutated keyset page = %v, want %v", got, want)
	}
}

func TestCalendarRefreshClaimIsExclusiveRecoverableAndRejectsStaleToken(t *testing.T) {
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	repository := &refreshRepository{now: func() time.Time { return now }}
	from := now.AddDate(0, 0, -1)
	to := now.AddDate(0, 0, 1)
	firstCursor, claimed, _, err := repository.ClaimRefresh(context.Background(), nil, "profile", "first", now.Add(time.Minute), from, to, "en-US")
	if err != nil || !claimed || firstCursor.From != from || firstCursor.To != to || firstCursor.Language != "en-US" {
		t.Fatalf("first claim: claimed=%v cursor=%+v error=%v", claimed, firstCursor, err)
	}
	if _, claimed, retryAt, err := repository.ClaimRefresh(context.Background(), nil, "profile", "second", now.Add(time.Minute), from, to, "fr-FR"); err != nil || claimed || !retryAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("concurrent second claim: claimed=%v retryAt=%v error=%v", claimed, retryAt, err)
	}
	now = now.Add(2 * time.Minute)
	if recovered, recoveredClaim, _, recoverErr := repository.ClaimRefresh(context.Background(), nil, "profile", "second", now.Add(time.Minute), from.AddDate(0, 1, 0), to.AddDate(0, 1, 0), "fr-FR"); recoverErr != nil || !recoveredClaim || recovered.Language != "en-US" {
		t.Fatalf("expired lease recovery changed persisted request: claimed=%v cursor=%+v error=%v", recoveredClaim, recovered, recoverErr)
	}
	staleCursor := refreshCursor{AfterTitleID: "skipped"}
	if committed, _, err := repository.CompleteRefresh(context.Background(), nil, "profile", "first", staleCursor, false); err != nil || committed {
		t.Fatalf("stale token completion: committed=%v error=%v", committed, err)
	}
	validCursor := refreshCursor{AfterTitleID: "finished", From: from, To: to, Language: "en-US"}
	nextEligibleAt := now.Add(defaultRefreshMinimum)
	if committed, retryAt, err := repository.CompleteRefresh(context.Background(), nil, "profile", "second", validCursor, false); err != nil || !committed || !retryAt.Equal(nextEligibleAt) {
		t.Fatalf("current token completion: committed=%v retryAt=%v error=%v", committed, retryAt, err)
	}
	if _, resumedClaim, retryAt, resumeErr := repository.ClaimRefresh(
		context.Background(), nil, "profile", "third", now.Add(time.Minute),
		from.AddDate(0, 1, 0), to.AddDate(0, 1, 0), "fr-FR",
	); resumeErr != nil || resumedClaim || !retryAt.Equal(nextEligibleAt) {
		t.Fatalf("cooldown claim: claimed=%v retryAt=%v error=%v", resumedClaim, retryAt, resumeErr)
	}
	now = nextEligibleAt
	resumed, resumedClaim, _, resumeErr := repository.ClaimRefresh(
		context.Background(), nil, "profile", "third", now.Add(time.Minute),
		from.AddDate(0, 1, 0), to.AddDate(0, 1, 0), "fr-FR",
	)
	if resumeErr != nil || !resumedClaim || resumed != validCursor {
		t.Fatalf("idle continuation changed persisted request: claimed=%v cursor=%+v error=%v", resumedClaim, resumed, resumeErr)
	}
	repository.mu.Lock()
	got := repository.states["profile"].cursor
	repository.mu.Unlock()
	if got != validCursor {
		t.Fatalf("stored cursor = %+v, want %+v", got, validCursor)
	}
}

type observingCalendarRepository struct {
	postgresRepository
	claims chan struct{}
}

func (repository *observingCalendarRepository) ClaimRefresh(
	ctx context.Context,
	tx pgx.Tx,
	profileID, token string,
	expiresAt, requestedFrom, requestedTo time.Time,
	requestedLanguage string,
) (refreshCursor, bool, time.Time, error) {
	cursor, claimed, retryAt, err := repository.postgresRepository.ClaimRefresh(
		ctx, tx, profileID, token, expiresAt, requestedFrom, requestedTo, requestedLanguage,
	)
	repository.claims <- struct{}{}
	return cursor, claimed, retryAt, err
}

func TestCalendarRefreshTwoInstancesReleasePoolBeforeProvider(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run calendar claim tests")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open calendar claim test database: %v", err)
	}
	t.Cleanup(pool.Close)
	var stateTableExists bool
	if err := pool.QueryRow(context.Background(), `SELECT to_regclass('calendar_refresh_state') IS NOT NULL`).Scan(&stateTableExists); err != nil {
		t.Fatalf("check calendar refresh migration: %v", err)
	}
	if !stateTableExists {
		t.Skip("calendar refresh state migration is not applied")
	}

	const (
		categoryID = "ca510000-0000-4000-8000-000000000001"
		userID     = "ca510000-0000-4000-8000-000000000002"
		profileID  = "ca510000-0000-4000-8000-000000000003"
		titleID    = "ca510000-0000-4000-8000-000000000004"
		deviceID   = "ca510000-0000-4000-8000-000000000005"
		sessionID  = "ca510000-0000-4000-8000-000000000006"
	)
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM auth_sessions WHERE id = $1::uuid`, sessionID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM devices WHERE id = $1::uuid`, deviceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id = $1::uuid`, profileID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM titles WHERE id = $1::uuid`, titleID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM access_categories WHERE id = $1::uuid`, categoryID)
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := pool.Exec(context.Background(), `
		WITH category AS (
			INSERT INTO access_categories (id, name, normalized_name, position)
			VALUES ($1::uuid, 'Calendar claim test', 'calendar claim test', 951000)
		), account AS (
			INSERT INTO users (id, username, password_hash, role)
			VALUES ($2::uuid, 'calendar-claim-test', 'unused-test-hash', 'admin')
		), profile AS (
			INSERT INTO profiles (id, category_id, name)
			VALUES ($3::uuid, $1::uuid, 'Calendar claim profile')
		), title AS (
			INSERT INTO titles (id, media_type)
			VALUES ($4::uuid, 'movie')
		), device AS (
			INSERT INTO devices (id, user_id, name, platform, approved_at)
			VALUES ($5::uuid, $2::uuid, 'Calendar claim device', 'test', now())
		), session AS (
			INSERT INTO auth_sessions (
				id, user_id, device_id, access_token_hash,
				access_expires_at, refresh_expires_at,
				active_profile_id, profile_grant_expires_at, profile_context_hash,
				authorization_scope
			)
			VALUES (
				$6::uuid, $2::uuid, $5::uuid, decode(repeat('ca', 32), 'hex'),
				now() + interval '1 hour', now() + interval '2 hours',
				$3::uuid, now() + interval '1 hour', decode(repeat('a3', 32), 'hex'), 'global_admin'
			)
		)
		INSERT INTO profile_library (profile_id, title_id)
		VALUES ($3::uuid, $4::uuid)
	`, categoryID, userID, profileID, titleID, deviceID, sessionID); err != nil {
		t.Fatalf("seed calendar claim test: %v", err)
	}

	now := time.Now().UTC()
	expiresAt := now.Add(time.Hour)
	principal := auth.Principal{
		SessionID: sessionID, UserID: userID, DeviceID: deviceID,
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: new(profileID), ProfileGrantExpiresAt: &expiresAt,
		ProfileContextHash: bytes.Repeat([]byte{0xa3}, sha256.Size),
	}
	reader := newBlockingMetadataReader()
	observedRepository := &observingCalendarRepository{claims: make(chan struct{}, 2)}
	first := newTestService(t, pool, reader, "UTC", slog.New(slog.NewTextHandler(io.Discard, nil)))
	second := newTestService(t, pool, reader, "UTC", slog.New(slog.NewTextHandler(io.Discard, nil)))
	first.repository = observedRepository
	second.repository = observedRepository
	if _, err := first.List(context.Background(), principal, "2026-08-01", "2026-08-31", "en-US"); err != nil {
		t.Fatalf("queue first calendar instance: %v", err)
	}
	if _, err := second.List(context.Background(), principal, "2026-08-01", "2026-08-31", "en-US"); err != nil {
		t.Fatalf("queue second calendar instance: %v", err)
	}
	cancelFirst, firstDone := startRefreshWorker(first)
	cancelSecond, secondDone := startRefreshWorker(second)
	defer func() {
		cancelFirst()
		cancelSecond()
		<-firstDone
		<-secondDone
	}()
	select {
	case <-reader.started:
	case <-time.After(time.Second):
		t.Fatal("claimed calendar refresh did not reach provider")
	}
	for range 2 {
		select {
		case <-observedRepository.claims:
		case <-time.After(time.Second):
			t.Fatal("both calendar instances did not attempt the persistent claim")
		}
	}
	if calls := reader.recordedCalls(); calls != 1 {
		t.Fatalf("two calendar instances started %d provider calls, want one exclusive claim", calls)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE auth_sessions SET revoked_at = now() WHERE id = $1::uuid
	`, sessionID); err != nil {
		t.Fatalf("revoke deferred calendar session: %v", err)
	}
	revokedReader := &budgetMetadataReader{}
	revokedService := newTestService(t, pool, revokedReader, "UTC", slog.New(slog.NewTextHandler(io.Discard, nil)))
	revokedService.enqueueRefresh(principal, profileID, now, now.AddDate(0, 0, 30), "en-US")
	revokedRequest, ok := revokedService.takeRefresh(time.Now().UTC())
	if !ok {
		t.Fatal("revoked session refresh was not queued")
	}
	if _, err := revokedService.refresh(context.Background(), revokedRequest); !errors.Is(err, ErrProfileRequired) {
		t.Fatalf("revoked session refresh error = %v, want active profile required", err)
	}
	if titleCalls, seasonCalls, _ := revokedReader.counts(); titleCalls != 0 || seasonCalls != 0 {
		t.Fatalf("revoked session reached provider: titles=%d seasons=%d", titleCalls, seasonCalls)
	}

	if _, err := pool.Exec(context.Background(), `
		UPDATE auth_sessions SET revoked_at = NULL WHERE id = $1::uuid
	`, sessionID); err != nil {
		t.Fatalf("restore deferred calendar session: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE users SET role = 'member' WHERE id = $1::uuid
	`, userID); err != nil {
		t.Fatalf("demote deferred calendar principal: %v", err)
	}
	demotedReader := &budgetMetadataReader{}
	demotedService := newTestService(t, pool, demotedReader, "UTC", slog.New(slog.NewTextHandler(io.Discard, nil)))
	demotedService.enqueueRefresh(principal, profileID, now, now.AddDate(0, 0, 30), "en-US")
	demotedRequest, ok := demotedService.takeRefresh(time.Now().UTC())
	if !ok {
		t.Fatal("demoted principal refresh was not queued")
	}
	if _, err := demotedService.refresh(context.Background(), demotedRequest); !errors.Is(err, ErrProfileRequired) {
		t.Fatalf("demoted principal refresh error = %v, want active profile required", err)
	}
	if titleCalls, seasonCalls, _ := demotedReader.counts(); titleCalls != 0 || seasonCalls != 0 {
		t.Fatalf("demoted principal reached provider: titles=%d seasons=%d", titleCalls, seasonCalls)
	}
	if _, err := pool.Exec(context.Background(), `
		WITH restored_user AS (
			UPDATE users SET role = 'admin' WHERE id = $1::uuid RETURNING id
		)
		UPDATE auth_sessions
		SET profile_context_hash = decode(repeat('b4', 32), 'hex')
		WHERE id = $2::uuid AND EXISTS (SELECT 1 FROM restored_user)
	`, userID, sessionID); err != nil {
		t.Fatalf("regenerate deferred calendar profile context: %v", err)
	}
	staleContextReader := &budgetMetadataReader{}
	staleContextService := newTestService(t, pool, staleContextReader, "UTC", slog.New(slog.NewTextHandler(io.Discard, nil)))
	staleContextService.enqueueRefresh(principal, profileID, now, now.AddDate(0, 0, 30), "en-US")
	staleContextRequest, ok := staleContextService.takeRefresh(time.Now().UTC())
	if !ok {
		t.Fatal("stale-context refresh was not queued")
	}
	if _, err := staleContextService.refresh(context.Background(), staleContextRequest); !errors.Is(err, ErrProfileRequired) {
		t.Fatalf("stale-context refresh error = %v, want active profile required", err)
	}
	if titleCalls, seasonCalls, _ := staleContextReader.counts(); titleCalls != 0 || seasonCalls != 0 {
		t.Fatalf("stale-context refresh reached provider: titles=%d seasons=%d", titleCalls, seasonCalls)
	}
	queryContext, cancelQuery := context.WithTimeout(context.Background(), time.Second)
	defer cancelQuery()
	var one int
	if err := pool.QueryRow(queryContext, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("MaxConns=1 query while provider blocked: value=%d error=%v", one, err)
	}
	cancelFirst()
	cancelSecond()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first calendar instance did not join")
	}
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second calendar instance did not join")
	}
}

func TestCalendarTokenFormatAndRollingFeedRange(t *testing.T) {
	token, digest, err := newCalendarToken()
	if err != nil {
		t.Fatalf("generate calendar token: %v", err)
	}
	if !strings.HasPrefix(token, calendarTokenPrefix) || len(digest) != sha256.Size {
		t.Fatalf("calendar token format invalid; digest bytes=%d", len(digest))
	}
	parsedDigest, valid := calendarTokenDigest(token)
	if !valid || !bytes.Equal(parsedDigest, digest) {
		t.Fatal("generated calendar token did not round-trip through strict validation")
	}
	for _, invalid := range []string{"", " " + token, token + " ", "rivune_cal_short", strings.Replace(token, "rivune_cal_", "other_", 1), token + "="} {
		if _, valid := calendarTokenDigest(invalid); valid {
			t.Fatalf("invalid calendar token was accepted: %q", invalid)
		}
	}

	configuredLocation, err := time.LoadLocation("Pacific/Kiritimati")
	if err != nil {
		t.Fatalf("load configured timezone: %v", err)
	}
	hostLocation, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load host timezone: %v", err)
	}
	originalLocal := time.Local
	time.Local = hostLocation
	t.Cleanup(func() { time.Local = originalLocal })
	from, to := calendarFeedRange(time.Date(2026, time.March, 1, 10, 30, 0, 0, time.UTC), configuredLocation)
	if from.Format(time.RFC3339) != "2026-01-30T00:00:00+14:00" || to.Format(time.RFC3339) != "2026-05-02T00:00:00+14:00" {
		t.Fatalf("configured-timezone rolling feed range = %s to %s", from.Format(time.RFC3339), to.Format(time.RFC3339))
	}
	for _, invalidTimezone := range []string{"", "Local", " not/a-timezone", "not/a-timezone"} {
		if _, err := NewService(nil, nil, invalidTimezone, nil); err == nil {
			t.Fatalf("calendar service accepted invalid configured timezone %q", invalidTimezone)
		}
	}
}

func TestCalendarLinkLifecyclePersistsOnlyHashAndImmediatelyRevokesOldTokens(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run calendar link lifecycle tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open calendar link test database: %v", err)
	}
	t.Cleanup(pool.Close)
	var tableExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('profile_calendar_links') IS NOT NULL`).Scan(&tableExists); err != nil {
		t.Fatalf("check calendar link migration: %v", err)
	}
	if !tableExists {
		t.Skip("profile calendar link migration is not applied")
	}

	const (
		categoryID     = "ca590000-0000-4000-8000-000000000001"
		profileID      = "ca590000-0000-4000-8000-000000000002"
		otherProfileID = "ca590000-0000-4000-8000-000000000003"
		userID         = "ca590000-0000-4000-8000-000000000004"
		deviceID       = "ca590000-0000-4000-8000-000000000005"
		sessionID      = "ca590000-0000-4000-8000-000000000006"
	)
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM auth_sessions WHERE id = $1::uuid`, sessionID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM devices WHERE id = $1::uuid`, deviceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id = ANY($1::uuid[])`, []string{profileID, otherProfileID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM access_categories WHERE id = $1::uuid`, categoryID)
	}
	cleanup()
	t.Cleanup(cleanup)
	now := time.Now().UTC()
	configuredLocation, err := time.LoadLocation("Pacific/Kiritimati")
	if err != nil {
		t.Fatalf("load calendar link configured timezone: %v", err)
	}
	availableDate := now.In(configuredLocation).Format(time.DateOnly)
	if _, err := pool.Exec(ctx, `
		WITH category AS (
			INSERT INTO access_categories (id, name, normalized_name, position)
			VALUES ($1::uuid, 'Calendar link test', 'calendar link test', 959000)
		), account AS (
			INSERT INTO users (id, username, password_hash, role)
			VALUES ($2::uuid, 'calendar-link-test', 'unused-test-hash', 'admin')
		), profile AS (
			INSERT INTO profiles (id, category_id, name, available_from, available_until, access_timezone)
			VALUES ($3::uuid, $1::uuid, 'Calendar link profile', $7::date, $7::date, 'Pacific/Honolulu'),
			       ($4::uuid, $1::uuid, 'Other calendar link profile', NULL, NULL, 'Pacific/Honolulu')
		), device AS (
			INSERT INTO devices (id, user_id, name, platform, approved_at)
			VALUES ($5::uuid, $2::uuid, 'Calendar link device', 'test', now())
		)
		INSERT INTO auth_sessions (
			id, user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			active_profile_id, profile_grant_expires_at, profile_context_hash, authorization_scope
		)
		VALUES (
			$6::uuid, $2::uuid, $5::uuid, decode(repeat('c9', 32), 'hex'),
			now() + interval '1 hour', now() + interval '2 hours',
			$3::uuid, now() + interval '1 hour', decode(repeat('a5', 32), 'hex'), 'global_admin'
		)
	`, categoryID, userID, profileID, otherProfileID, deviceID, sessionID, availableDate); err != nil {
		t.Fatalf("seed calendar link authorization fixture: %v", err)
	}

	expiresAt := now.Add(time.Hour)
	principal := auth.Principal{
		SessionID: sessionID, UserID: userID, DeviceID: deviceID,
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: new(profileID), ProfileGrantExpiresAt: &expiresAt,
		ProfileContextHash: bytes.Repeat([]byte{0xa5}, sha256.Size), ActiveProfileCanManage: true,
	}
	service := newTestService(t, pool, nil, "Pacific/Kiritimati", slog.New(slog.NewTextHandler(io.Discard, nil)))
	service.now = func() time.Time { return now }
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'member' WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("demote calendar link manager: %v", err)
	}
	if _, err := service.LinkStatus(ctx, principal, profileID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("authoritatively demoted calendar link status error = %v, want %v", err, ErrNotFound)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("restore calendar link manager: %v", err)
	}
	mismatched := principal
	mismatched.ActiveProfileID = new(otherProfileID)
	if _, err := service.LinkStatus(ctx, mismatched, profileID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mismatched active profile calendar link status error = %v, want %v", err, ErrNotFound)
	}

	first, err := service.CreateLink(ctx, principal, profileID)
	if err != nil {
		t.Fatalf("create calendar link: %v", err)
	}
	if first.Token == "" || !first.Active {
		t.Fatalf("created calendar credential = %+v", first)
	}
	if _, err := service.CreateLink(ctx, principal, profileID); !errors.Is(err, ErrLinkExists) {
		t.Fatalf("duplicate calendar link error = %v, want %v", err, ErrLinkExists)
	}
	var storedHash []byte
	if err := pool.QueryRow(ctx, `SELECT token_hash FROM profile_calendar_links WHERE profile_id = $1::uuid`, profileID).Scan(&storedHash); err != nil {
		t.Fatalf("read stored calendar token hash: %v", err)
	}
	firstHash := sha256.Sum256([]byte(first.Token))
	if !bytes.Equal(storedHash, firstHash[:]) || bytes.Contains(storedHash, []byte(first.Token)) {
		t.Fatal("calendar credential was not stored exclusively as its SHA-256 digest")
	}
	storedTimezoneService := newTestService(t, pool, nil, "Pacific/Honolulu", slog.New(slog.NewTextHandler(io.Discard, nil)))
	storedTimezoneService.now = func() time.Time { return now }
	if _, err := storedTimezoneService.LinkStatus(ctx, principal, profileID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("persisted profile timezone overrode configured management timezone: %v", err)
	}
	if _, err := storedTimezoneService.Feed(ctx, first.Token, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("persisted profile timezone overrode configured feed timezone: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE auth_sessions
		SET active_profile_id = $2::uuid, profile_context_hash = decode(repeat('b6', 32), 'hex')
		WHERE id = $1::uuid
	`, sessionID, otherProfileID); err != nil {
		t.Fatalf("switch authoritative calendar session profile: %v", err)
	}
	if _, err := service.LinkStatus(ctx, principal, profileID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale selected-profile calendar link status error = %v, want %v", err, ErrNotFound)
	}
	if _, err := service.RotateLink(ctx, principal, profileID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale selected-profile rotation error = %v, want %v", err, ErrNotFound)
	}
	var unchangedHash []byte
	if err := pool.QueryRow(ctx, `SELECT token_hash FROM profile_calendar_links WHERE profile_id = $1::uuid`, profileID).Scan(&unchangedHash); err != nil {
		t.Fatalf("read link after stale selected-profile rotation: %v", err)
	}
	if !bytes.Equal(unchangedHash, firstHash[:]) {
		t.Fatal("stale selected-profile rotation mutated the calendar link")
	}

	if _, err := pool.Exec(ctx, `
		UPDATE auth_sessions
		SET active_profile_id = $2::uuid
		WHERE id = $1::uuid
	`, sessionID, profileID); err != nil {
		t.Fatalf("restore selected profile with regenerated context: %v", err)
	}
	if err := service.RevokeLink(ctx, principal, profileID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale regenerated-context revocation error = %v, want %v", err, ErrNotFound)
	}
	var linkCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM profile_calendar_links WHERE profile_id = $1::uuid`, profileID).Scan(&linkCount); err != nil {
		t.Fatalf("count link after stale regenerated-context revocation: %v", err)
	}
	if linkCount != 1 {
		t.Fatalf("stale regenerated-context revocation left %d links, want 1", linkCount)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE auth_sessions
		SET profile_context_hash = decode(repeat('a5', 32), 'hex')
		WHERE id = $1::uuid
	`, sessionID); err != nil {
		t.Fatalf("restore captured calendar profile context: %v", err)
	}
	headRepository := &fakeEventRepository{err: errors.New("HEAD must not list calendar events")}
	service.repository = headRepository
	headPayload, err := service.Feed(ctx, first.Token, false)
	if err != nil || headPayload != nil || headRepository.listCalls != 0 {
		t.Fatalf("HEAD feed validation payload=%q list calls=%d error=%v", headPayload, headRepository.listCalls, err)
	}
	service.repository = &postgresRepository{}
	if _, err := service.Feed(ctx, first.Token, true); err != nil {
		t.Fatalf("read newly created calendar feed: %v", err)
	}

	rotated, err := service.RotateLink(ctx, principal, profileID)
	if err != nil {
		t.Fatalf("rotate calendar link: %v", err)
	}
	if rotated.Token == first.Token || rotated.RotatedAt.Before(first.RotatedAt) {
		t.Fatalf("rotated calendar credential = %+v after %+v", rotated, first)
	}
	if _, err := service.Feed(ctx, first.Token, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old calendar token after rotation error = %v, want %v", err, ErrNotFound)
	}
	if _, err := service.Feed(ctx, rotated.Token, true); err != nil {
		t.Fatalf("new calendar token after rotation: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE profiles SET enabled = false WHERE id = $1::uuid`, profileID); err != nil {
		t.Fatalf("restrict calendar feed profile: %v", err)
	}
	if _, err := service.Feed(ctx, rotated.Token, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inaccessible calendar profile feed error = %v, want %v", err, ErrNotFound)
	}
	if _, err := pool.Exec(ctx, `UPDATE profiles SET enabled = true WHERE id = $1::uuid`, profileID); err != nil {
		t.Fatalf("restore calendar feed profile: %v", err)
	}

	if err := service.RevokeLink(ctx, principal, profileID); err != nil {
		t.Fatalf("revoke calendar link: %v", err)
	}
	if err := service.RevokeLink(ctx, principal, profileID); err != nil {
		t.Fatalf("idempotent calendar link revoke: %v", err)
	}
	if _, err := service.Feed(ctx, rotated.Token, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked calendar token error = %v, want %v", err, ErrNotFound)
	}
	if _, err := service.RotateLink(ctx, principal, profileID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rotate absent calendar link error = %v, want %v", err, ErrNotFound)
	}

	credential, err := service.CreateLink(ctx, principal, profileID)
	if err != nil {
		t.Fatalf("recreate calendar link for cascade: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM profiles WHERE id = $1::uuid`, profileID); err != nil {
		t.Fatalf("delete calendar link profile: %v", err)
	}
	if _, err := service.Feed(ctx, credential.Token, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cascade-revoked calendar token error = %v, want %v", err, ErrNotFound)
	}
}

func TestVariantEpisodesStayOutOfCalendarOutputAndCapacitySentinel(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run PostgreSQL calendar tests")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse calendar test database URL: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("open calendar test database: %v", err)
	}
	t.Cleanup(pool.Close)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin calendar variant fixture: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), `
		CREATE TEMPORARY TABLE profile_library (profile_id uuid NOT NULL, title_id uuid NOT NULL);
		CREATE TEMPORARY TABLE titles (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(), media_type text NOT NULL,
			parent_id uuid REFERENCES titles(id) ON DELETE CASCADE, ordinal integer,
			hierarchy_variant text NOT NULL DEFAULT '', display_title text,
			release_date date, poster_url text, resource_id text, resource_provider text,
			updated_at timestamptz NOT NULL DEFAULT now()
		);
		INSERT INTO titles (id, media_type, display_title) VALUES
			('ca600000-0000-4000-8000-000000000001', 'series', 'Canonical Series');
		INSERT INTO titles (id, media_type, parent_id, ordinal, hierarchy_variant, display_title) VALUES
			('ca600000-0000-4000-8000-000000000002', 'season', 'ca600000-0000-4000-8000-000000000001', 1, '', 'Canonical Season'),
			('ca600000-0000-4000-8000-000000000003', 'season', 'ca600000-0000-4000-8000-000000000001', 1, 'tvdb:2', 'Variant Season');
		INSERT INTO titles (id, media_type, parent_id, ordinal, hierarchy_variant, display_title, release_date) VALUES
			('ca600000-0000-4000-8000-000000000004', 'episode', 'ca600000-0000-4000-8000-000000000002', 1, '', 'Canonical Episode', '2026-09-05'),
			('ca600000-0000-4000-8000-000000000005', 'episode', 'ca600000-0000-4000-8000-000000000003', 1, 'tvdb:2', 'Variant Branch Episode', '2026-09-05'),
			('ca600000-0000-4000-8000-000000000006', 'episode', 'ca600000-0000-4000-8000-000000000002', 1, 'tvdb:3', 'Variant Direct Episode', '2026-09-05');
		INSERT INTO profile_library (profile_id, title_id) VALUES
			('ca600000-0000-4000-8000-000000000010', 'ca600000-0000-4000-8000-000000000001');
	`); err != nil {
		t.Fatalf("seed calendar variant fixture: %v", err)
	}
	repository := &postgresRepository{}
	events, err := repository.List(t.Context(), tx, "ca600000-0000-4000-8000-000000000010",
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("list calendar variant fixture: %v", err)
	}
	if len(events) != 1 || events[0].Title != "Canonical Episode" {
		t.Fatalf("calendar events = %+v, want canonical episode only", events)
	}

	if _, err := tx.Exec(t.Context(), `
		DELETE FROM titles WHERE media_type = 'episode';
		INSERT INTO titles (media_type, parent_id, ordinal, hierarchy_variant, display_title, release_date)
		SELECT 'episode', 'ca600000-0000-4000-8000-000000000002'::uuid, value, '', 'Canonical ' || value, '2026-09-05'
		FROM generate_series(1, $1::integer) AS value;
		INSERT INTO titles (media_type, parent_id, ordinal, hierarchy_variant, display_title, release_date)
		VALUES
			('episode', 'ca600000-0000-4000-8000-000000000002', 1, 'tvdb:4', 'Variant Capacity One', '2026-09-05'),
			('episode', 'ca600000-0000-4000-8000-000000000003', 2, 'tvdb:2', 'Variant Capacity Two', '2026-09-05');
	`, pgx.QueryExecModeSimpleProtocol, maximumCalendarEvents); err != nil {
		t.Fatalf("seed calendar capacity boundary: %v", err)
	}
	events, err = repository.List(t.Context(), tx, "ca600000-0000-4000-8000-000000000010",
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if err != nil || len(events) != maximumCalendarEvents {
		t.Fatalf("canonical calendar boundary count=%d error=%v, want %d", len(events), err, maximumCalendarEvents)
	}
	if _, err := tx.Exec(t.Context(), `
		INSERT INTO titles (media_type, parent_id, ordinal, hierarchy_variant, display_title, release_date)
		VALUES ('episode', 'ca600000-0000-4000-8000-000000000002', $1, '', 'Canonical Overflow', '2026-09-05')
	`, maximumCalendarEvents+1); err != nil {
		t.Fatalf("seed canonical calendar overflow: %v", err)
	}
	events, err = repository.List(t.Context(), tx, "ca600000-0000-4000-8000-000000000010",
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if err != nil || len(events) != calendarEventQueryLimit {
		t.Fatalf("calendar overflow sentinel count=%d error=%v, want %d", len(events), err, calendarEventQueryLimit)
	}
}
