package calendar

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/metadata"
)

var (
	ErrInvalidInput    = errors.New("invalid calendar range")
	ErrProfileRequired = errors.New("active profile required")
)

const maximumRangeDays = 93

type Event struct {
	ID               string `json:"id"`
	TitleID          string `json:"titleId"`
	MediaType        string `json:"mediaType"`
	Title            string `json:"title"`
	ReleaseDate      string `json:"releaseDate"`
	PosterURL        string `json:"posterUrl,omitempty"`
	ResourceID       string `json:"resourceId,omitempty"`
	ResourceProvider string `json:"resourceProvider,omitempty"`
	SeriesTitle      string `json:"seriesTitle,omitempty"`
	SeriesID         string `json:"seriesId,omitempty"`
	SeasonID         string `json:"seasonId,omitempty"`
	SeasonNumber     *int   `json:"seasonNumber,omitempty"`
	EpisodeNumber    *int   `json:"episodeNumber,omitempty"`
}

type Result struct {
	Events []Event `json:"events"`
}

type eventRepository interface {
	List(context.Context, string, time.Time, time.Time) ([]Event, error)
	LibraryTitles(context.Context, string) ([]libraryTitle, error)
}

type libraryTitle struct {
	ID        string
	MediaType string
}

type metadataReader interface {
	MovieDetails(context.Context, auth.Principal, string, string) (metadata.Movie, error)
	SeriesDetails(context.Context, auth.Principal, string, string, string) (metadata.Series, error)
	SeasonDetails(context.Context, auth.Principal, string, string, string) (metadata.Season, error)
}

type postgresRepository struct {
	pool *pgxpool.Pool
}

type Service struct {
	repository eventRepository
	metadata   metadataReader
	logger     *slog.Logger
	now        func() time.Time
}

func NewService(pool *pgxpool.Pool, metadataService metadataReader, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		repository: &postgresRepository{pool: pool},
		metadata:   metadataService,
		logger:     logger,
		now:        time.Now,
	}
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
	if s.metadata != nil {
		s.refreshLibraryMetadata(ctx, principal, profileID, from, to, language)
	}
	events, err := s.repository.List(ctx, profileID, from, to)
	if err != nil {
		return Result{}, err
	}
	sortEvents(events)
	return Result{Events: events}, nil
}

func (s *Service) refreshLibraryMetadata(ctx context.Context, principal auth.Principal, profileID string, from, to time.Time, language string) {
	titles, err := s.repository.LibraryTitles(ctx, profileID)
	if err != nil {
		s.logger.Warn("calendar metadata refresh skipped", "error", err)
		return
	}
	const maximumWorkers = 4
	workerCount := min(maximumWorkers, len(titles))
	if workerCount == 0 {
		return
	}
	jobs := make(chan libraryTitle)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for title := range jobs {
				if err := s.refreshTitleMetadata(ctx, principal, title, from, to, language); err != nil &&
					ctx.Err() == nil && !errors.Is(err, metadata.ErrNotFound) {
					s.logger.Warn("calendar title refresh failed", "titleId", title.ID, "mediaType", title.MediaType, "error", err)
				}
			}
		}()
	}
	for _, title := range titles {
		select {
		case jobs <- title:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return
		}
	}
	close(jobs)
	workers.Wait()
}

func (s *Service) refreshTitleMetadata(ctx context.Context, principal auth.Principal, title libraryTitle, from, to time.Time, language string) error {
	switch title.MediaType {
	case metadata.MediaTypeMovie:
		_, err := s.metadata.MovieDetails(ctx, principal, title.ID, language)
		return err
	case metadata.MediaTypeSeries:
		series, err := s.metadata.SeriesDetails(ctx, principal, title.ID, language, "tmdb")
		if err != nil {
			return err
		}
		for _, season := range series.Seasons {
			if !seasonMayOverlap(season.AirDate, from, to) {
				continue
			}
			if _, err := s.metadata.SeasonDetails(ctx, principal, season.ID, language, "tmdb"); err != nil {
				return fmt.Errorf("refresh season %d: %w", season.SeasonNumber, err)
			}
		}
	}
	return nil
}

func seasonMayOverlap(airDate string, from, to time.Time) bool {
	firstRelease, err := time.Parse(time.DateOnly, strings.TrimSpace(airDate))
	if err != nil || firstRelease.After(to) {
		return false
	}
	return !firstRelease.AddDate(1, 0, 0).Before(from)
}

func (repository *postgresRepository) LibraryTitles(ctx context.Context, profileID string) ([]libraryTitle, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT title.id::text, title.media_type
		FROM profile_library AS library
		JOIN titles AS title ON title.id = library.title_id
		WHERE library.profile_id = $1::uuid
		  AND title.media_type IN ('movie', 'series')
		ORDER BY title.id
	`, profileID)
	if err != nil {
		return nil, fmt.Errorf("query calendar library titles: %w", err)
	}
	defer rows.Close()
	titles := make([]libraryTitle, 0)
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

func (repository *postgresRepository) List(ctx context.Context, profileID string, from, to time.Time) ([]Event, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT event.id, event.title_id, event.media_type, event.title,
		       event.release_date, event.poster_url, event.resource_id,
		       event.resource_provider, event.series_title, event.series_id,
		       event.season_id, event.season_number, event.episode_number
		FROM (
			SELECT movie.id::text AS id, movie.id::text AS title_id,
			       'movie'::text AS media_type, movie.display_title AS title,
			       movie.release_date, COALESCE(movie.poster_url, '') AS poster_url,
			       COALESCE(movie.resource_id, '') AS resource_id,
			       COALESCE(movie.resource_provider, '') AS resource_provider,
			       ''::text AS series_title, ''::text AS series_id,
			       ''::text AS season_id, NULL::integer AS season_number,
			       NULL::integer AS episode_number
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
			       episode.ordinal AS episode_number
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
	`, profileID, from.Format(time.DateOnly), to.Format(time.DateOnly))
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
			&event.SeasonID, &event.SeasonNumber, &event.EpisodeNumber,
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
