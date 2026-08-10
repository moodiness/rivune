package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type metadataQueryCounter struct {
	count              atomic.Int64
	candidateQueries   atomic.Int64
	candidateArrayArgs atomic.Bool
}

func (counter *metadataQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	counter.count.Add(1)
	if strings.Contains(data.SQL, "FROM titles AS title") && strings.Contains(data.SQL, "cached.expires_at") {
		counter.candidateQueries.Add(1)
		for _, argument := range data.Args {
			if _, isTextArray := argument.([]string); isTextArray {
				counter.candidateArrayArgs.Store(true)
			}
		}
	}
	return ctx
}

func (*metadataQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestExhaustiveRefreshUsesKeysetBatchesWithoutGrowingExclusions(t *testing.T) {
	const (
		titleCount = 17
		batchSize  = 4
	)
	counter := &metadataQueryCounter{}
	pool := newCanonicalMergeTestPool(t, counter)
	ctx := context.Background()
	provider := &refreshTestProvider{movies: make(map[string]ProviderMovie, titleCount)}
	for index := 1; index <= titleCount; index++ {
		titleID := fmt.Sprintf("f0000000-0000-4000-8000-%012d", index)
		externalID := fmt.Sprintf("%d", 400+index)
		if _, err := pool.Exec(ctx, `
			WITH inserted_title AS (
				INSERT INTO titles (id, media_type, display_title)
				VALUES ($1::uuid, 'movie', $2)
				RETURNING id
			)
			INSERT INTO title_external_ids (title_id, provider, namespace, external_id)
			SELECT id, 'tmdb', 'movie', $3
			FROM inserted_title
		`, titleID, fmt.Sprintf("Movie %02d", index), externalID); err != nil {
			t.Fatalf("seed refresh candidate %d: %v", index, err)
		}
		provider.movies[externalID] = ProviderMovie{
			ExternalID: externalID,
			Title:      fmt.Sprintf("Movie %02d", index),
			Cast:       []CastMember{},
		}
	}

	counter.count.Store(0)
	counter.candidateQueries.Store(0)
	counter.candidateArrayArgs.Store(false)
	result, err := NewService(pool, provider, nil, nil, time.Hour, nil).RefreshMissing(
		ctx,
		RefreshMissingOptions{Language: "en-US", BatchSize: batchSize, Exhaustive: true},
	)
	if err != nil {
		t.Fatalf("exhaustive keyset refresh: %v", err)
	}
	if result.Candidates != titleCount || result.Refreshed != titleCount || result.Failed != 0 {
		t.Fatalf("unexpected exhaustive refresh result: %+v", result)
	}
	const expectedCandidateQueries = 7
	if queries := counter.candidateQueries.Load(); queries != expectedCandidateQueries {
		t.Fatalf("candidate query count=%d, want %d fixed-size keyset pages", queries, expectedCandidateQueries)
	}
	if counter.candidateArrayArgs.Load() {
		t.Fatal("candidate query retransmitted a growing title-ID exclusion array")
	}
}

func TestCachedSeasonDetailsUsesBoundedDatabaseRoundTrips(t *testing.T) {
	oneEpisodeQueries := measureCachedSeasonDetailsQueries(t, 1)
	manyEpisodeQueries := measureCachedSeasonDetailsQueries(t, 24)
	if manyEpisodeQueries != oneEpisodeQueries {
		t.Fatalf("cached season query count grew with episodes: one=%d many=%d", oneEpisodeQueries, manyEpisodeQueries)
	}
	if manyEpisodeQueries > 12 {
		t.Fatalf("cached season used %d database queries, want a bounded constant", manyEpisodeQueries)
	}
}

func measureCachedSeasonDetailsQueries(t *testing.T, episodeCount int) int64 {
	t.Helper()
	counter := &metadataQueryCounter{}
	pool := newCanonicalMergeTestPool(t, counter)
	ctx := context.Background()
	const (
		seriesID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		seasonID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, display_title) VALUES ($1::uuid, 'series', 'Series');
		INSERT INTO titles (id, media_type, parent_id, ordinal) VALUES ($2::uuid, 'season', $1::uuid, 1);
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($1::uuid, 'tmdb', 'series', '100'),
			($2::uuid, 'tmdb', 'season', '200')
	`, pgx.QueryExecModeSimpleProtocol, seriesID, seasonID); err != nil {
		t.Fatalf("seed cached season hierarchy: %v", err)
	}
	episodes := make([]Episode, episodeCount)
	for index := range episodes {
		episodeID := fmt.Sprintf("cccccccc-cccc-4ccc-8ccc-%012d", index+1)
		episodes[index] = Episode{
			ID: episodeID, MediaType: MediaTypeEpisode, SeasonID: seasonID,
			Name: fmt.Sprintf("Episode %d", index+1), SeasonNumber: 1, EpisodeNumber: index + 1,
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO titles (id, media_type, parent_id, ordinal)
			VALUES ($1::uuid, 'episode', $2::uuid, $3)
		`, episodeID, seasonID, index+1); err != nil {
			t.Fatalf("seed cached episode %d: %v", index+1, err)
		}
	}
	payload, err := json.Marshal(Season{
		ID: seasonID, MediaType: MediaTypeSeason, SeriesID: seriesID,
		Name: "Season 1", SeasonNumber: 1, Episodes: episodes,
	})
	if err != nil {
		t.Fatalf("encode cached season: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at)
		VALUES ($1::uuid, 'tmdb', 'en-US', $2::jsonb, now() + interval '1 hour')
	`, seasonID, payload); err != nil {
		t.Fatalf("seed cached season metadata: %v", err)
	}

	counter.count.Store(0)
	season, err := NewService(pool, nil, nil, nil, 0, nil).SeasonDetails(ctx, canonicalMergePrincipal(), seasonID, "en-US", "tmdb")
	if err != nil {
		t.Fatalf("load cached season: %v", err)
	}
	if len(season.Episodes) != episodeCount {
		t.Fatalf("cached episode count=%d, want %d", len(season.Episodes), episodeCount)
	}
	for index, episode := range season.Episodes {
		if episode.EpisodeNumber != index+1 {
			t.Fatalf("cached episode order changed at index %d: %+v", index, episode)
		}
	}
	return counter.count.Load()
}

func TestCachedSeriesDetailsUsesBoundedDatabaseRoundTrips(t *testing.T) {
	oneSeasonQueries := measureCachedSeriesDetailsQueries(t, 1)
	manySeasonQueries := measureCachedSeriesDetailsQueries(t, 24)
	if manySeasonQueries != oneSeasonQueries {
		t.Fatalf("cached series query count grew with seasons: one=%d many=%d", oneSeasonQueries, manySeasonQueries)
	}
	if manySeasonQueries > 12 {
		t.Fatalf("cached series used %d database queries, want a bounded constant", manySeasonQueries)
	}
}

func measureCachedSeriesDetailsQueries(t *testing.T, seasonCount int) int64 {
	t.Helper()
	counter := &metadataQueryCounter{}
	pool := newCanonicalMergeTestPool(t, counter)
	ctx := context.Background()
	const seriesID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, display_title) VALUES ($1::uuid, 'series', 'Series');
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id)
		VALUES ($1::uuid, 'tmdb', 'series', '300')
	`, pgx.QueryExecModeSimpleProtocol, seriesID); err != nil {
		t.Fatalf("seed cached series: %v", err)
	}
	seasons := make([]SeasonSummary, seasonCount)
	for index := range seasons {
		seasonID := fmt.Sprintf("eeeeeeee-eeee-4eee-8eee-%012d", index+1)
		seasons[index] = SeasonSummary{
			ID: seasonID, MediaType: MediaTypeSeason, SeriesID: seriesID,
			Name: fmt.Sprintf("Season %d", index+1), SeasonNumber: index + 1,
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO titles (id, media_type, parent_id, ordinal)
			VALUES ($1::uuid, 'season', $2::uuid, $3)
		`, seasonID, seriesID, index+1); err != nil {
			t.Fatalf("seed cached season %d: %v", index+1, err)
		}
	}
	payload, err := json.Marshal(Series{
		ID: seriesID, MediaType: MediaTypeSeries, Name: "Series",
		Seasons: seasons, Cast: []CastMember{},
	})
	if err != nil {
		t.Fatalf("encode cached series: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at)
		VALUES ($1::uuid, 'tmdb', 'en-US', $2::jsonb, now() + interval '1 hour')
	`, seriesID, payload); err != nil {
		t.Fatalf("seed cached series metadata: %v", err)
	}

	counter.count.Store(0)
	series, err := NewService(pool, nil, nil, nil, 0, nil).SeriesDetails(ctx, canonicalMergePrincipal(), seriesID, SeriesDetailsOptions{Language: "en-US", MappingProvider: "tmdb"})
	if err != nil {
		t.Fatalf("load cached series: %v", err)
	}
	if len(series.Seasons) != seasonCount {
		t.Fatalf("cached season count=%d, want %d", len(series.Seasons), seasonCount)
	}
	for index, season := range series.Seasons {
		if season.SeasonNumber != index+1 {
			t.Fatalf("cached season order changed at index %d: %+v", index, season)
		}
	}
	return counter.count.Load()
}
