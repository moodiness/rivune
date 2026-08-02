package calendar

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/metadata"
)

type fakeEventRepository struct {
	profileID     string
	from          time.Time
	to            time.Time
	events        []Event
	err           error
	libraryTitles []libraryTitle
}

func (repository *fakeEventRepository) List(_ context.Context, profileID string, from, to time.Time) ([]Event, error) {
	repository.profileID, repository.from, repository.to = profileID, from, to
	return repository.events, repository.err
}

func (repository *fakeEventRepository) LibraryTitles(_ context.Context, profileID string) ([]libraryTitle, error) {
	repository.profileID = profileID
	return repository.libraryTitles, repository.err
}

type fakeMetadataReader struct {
	series      metadata.Series
	seasonCalls []string
}

func (reader *fakeMetadataReader) MovieDetails(context.Context, auth.Principal, string, string) (metadata.Movie, error) {
	return metadata.Movie{}, nil
}

func (reader *fakeMetadataReader) SeriesDetails(context.Context, auth.Principal, string, metadata.SeriesDetailsOptions) (metadata.Series, error) {
	return reader.series, nil
}

func (reader *fakeMetadataReader) SeasonDetails(_ context.Context, _ auth.Principal, seasonID, _, _ string) (metadata.Season, error) {
	reader.seasonCalls = append(reader.seasonCalls, seasonID)
	return metadata.Season{}, nil
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

	got, err := activeProfileID(auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &future}, now)
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
		auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt},
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

func TestListRefreshesLibrarySeasonThatCanOverlapRequestedMonth(t *testing.T) {
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	profileID := "11111111-1111-4111-8111-111111111111"
	expiresAt := now.Add(time.Hour)
	repository := &fakeEventRepository{
		events:        []Event{},
		libraryTitles: []libraryTitle{{ID: "series-id", MediaType: metadata.MediaTypeSeries}},
	}
	reader := &fakeMetadataReader{series: metadata.Series{Seasons: []metadata.SeasonSummary{
		{ID: "old-season", SeasonNumber: 10, AirDate: "2024-01-01"},
		{ID: "august-season", SeasonNumber: 11, AirDate: "2026-08-03"},
		{ID: "future-season", SeasonNumber: 12, AirDate: "2027-01-01"},
	}}}
	service := &Service{
		repository: repository,
		metadata:   reader,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:        func() time.Time { return now },
	}

	if _, err := service.List(
		context.Background(),
		auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt},
		"2026-08-01",
		"2026-08-31",
		"fr-FR",
	); err != nil {
		t.Fatalf("list refreshed calendar: %v", err)
	}
	if want := []string{"august-season"}; !reflect.DeepEqual(reader.seasonCalls, want) {
		t.Fatalf("unexpected refreshed seasons: got %v want %v", reader.seasonCalls, want)
	}
}
