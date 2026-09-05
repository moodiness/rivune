package metadata

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	episodeOrderSeriesID          = "10000000-0000-4000-8000-000000000100"
	episodeOrderCanonicalSeasonID = "10000000-0000-4000-8000-000000000110"
)

var episodeOrderCanonicalEpisodeIDs = []string{
	"10000000-0000-4000-8000-000000000201",
	"10000000-0000-4000-8000-000000000202",
	"10000000-0000-4000-8000-000000000203",
	"10000000-0000-4000-8000-000000000204",
	"10000000-0000-4000-8000-000000000205",
	"10000000-0000-4000-8000-000000000206",
	"10000000-0000-4000-8000-000000000207",
}

type episodeOrderProvider struct {
	mapped     ProviderSeason
	mappedByID map[string]ProviderSeason
}

func (*episodeOrderProvider) DiscoverMovies(context.Context, QueryOptions) (ProviderMoviePage, error) {
	return ProviderMoviePage{}, nil
}
func (*episodeOrderProvider) SearchMovies(context.Context, SearchOptions) (ProviderMoviePage, error) {
	return ProviderMoviePage{}, nil
}
func (*episodeOrderProvider) MovieDetails(context.Context, string, string) (ProviderMovie, error) {
	return ProviderMovie{}, ErrProviderNotFound
}
func (*episodeOrderProvider) DiscoverSeries(context.Context, QueryOptions) (ProviderSeriesPage, error) {
	return ProviderSeriesPage{}, nil
}
func (*episodeOrderProvider) SearchSeries(context.Context, SearchOptions) (ProviderSeriesPage, error) {
	return ProviderSeriesPage{}, nil
}
func (*episodeOrderProvider) SeriesDetails(_ context.Context, externalID, _ string) (ProviderSeries, error) {
	if externalID != "700001" {
		return ProviderSeries{}, ErrProviderNotFound
	}
	return ProviderSeries{
		ExternalID:    "700001",
		Name:          "Fixture Series",
		Cast:          []CastMember{},
		AdditionalIDs: map[string]string{"tvdb": "404604"},
		Seasons: []ProviderSeasonSummary{{
			ExternalID: "700101", Name: "Season 1", SeasonNumber: 1, EpisodeCount: 7,
		}},
	}, nil
}
func (*episodeOrderProvider) SeasonDetails(_ context.Context, externalID string, seasonNumber int, _ string) (ProviderSeason, error) {
	if externalID != "700001" || seasonNumber != 1 {
		return ProviderSeason{}, ErrProviderNotFound
	}
	episodes := make([]ProviderEpisode, 0, len(episodeOrderCanonicalEpisodeIDs))
	for index := range episodeOrderCanonicalEpisodeIDs {
		episodes = append(episodes, ProviderEpisode{
			ExternalID:    fmt.Sprintf("80000%d", index+1),
			Name:          fmt.Sprintf("Aired Episode %d", index+1),
			SeasonNumber:  1,
			EpisodeNumber: index + 1,
			AdditionalIDs: map[string]string{"tvdb": fmt.Sprintf("922629%d", index+1)},
		})
	}
	return ProviderSeason{ExternalID: "700101", Name: "Season 1", SeasonNumber: 1, Episodes: episodes}, nil
}
func (*episodeOrderProvider) SeriesSeasons(context.Context, string, string) ([]ProviderSeasonSummary, error) {
	return nil, errors.New("unexpected series seasons call")
}
func (provider *episodeOrderProvider) SeriesSeason(_ context.Context, seriesTVDBID, seasonTVDBID string) (ProviderSeason, error) {
	if seriesTVDBID != "404604" {
		return ProviderSeason{}, ErrProviderNotFound
	}
	if provider.mappedByID != nil {
		mapped, exists := provider.mappedByID[seasonTVDBID]
		if !exists {
			return ProviderSeason{}, ErrProviderNotFound
		}
		return mapped, nil
	}
	if seasonTVDBID != "2112814" {
		return ProviderSeason{}, ErrProviderNotFound
	}
	return provider.mapped, nil
}

func TestMappedDVDSeasonPersistsIndependentStableHierarchy(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	seedEpisodeOrderCanonicalHierarchy(t, pool)
	provider := &episodeOrderProvider{mapped: dvdProviderSeason()}
	service := NewServiceWithProviderSource(pool, staticProviderSource{providers: ProviderSet{
		Primary: provider,
		Mapper:  provider,
	}}, time.Hour, nil)
	ctx := context.Background()
	principal := canonicalMergePrincipal()
	publicSeasonID := mappedSeasonID(episodeOrderSeriesID, provider.mapped.ExternalID)
	canonicalExternalIDs := externalIDSnapshot(t, pool)

	first, err := service.SeasonDetails(ctx, principal, publicSeasonID, "en-US", "tvdb")
	if err != nil {
		t.Fatalf("persist DVD season hierarchy: %v", err)
	}
	assertDVDSeasonResult(t, first, publicSeasonID)
	firstIDs := episodeIDs(first.Episodes)

	second, err := service.SeasonDetails(ctx, principal, publicSeasonID, "en-US", "tvdb")
	if err != nil {
		t.Fatalf("repeat DVD season hierarchy: %v", err)
	}
	if secondIDs := episodeIDs(second.Episodes); !reflect.DeepEqual(secondIDs, firstIDs) {
		t.Fatalf("DVD episode UUIDs changed between refreshes: first=%v second=%v", firstIDs, secondIDs)
	}
	if after := externalIDSnapshot(t, pool); after != canonicalExternalIDs {
		t.Fatalf("canonical external IDs changed:\nbefore:\n%s\nafter:\n%s", canonicalExternalIDs, after)
	}
	assertVariantCoordinatesAndResources(t, pool, provider.mapped)

	missingExternalID := provider.mapped.Episodes[len(provider.mapped.Episodes)-1].ExternalID
	provider.mapped.Episodes = append([]ProviderEpisode(nil), provider.mapped.Episodes[:len(provider.mapped.Episodes)-1]...)
	if _, err := service.SeasonDetails(ctx, principal, publicSeasonID, "en-US", "tvdb"); err != nil {
		t.Fatalf("refresh DVD season with an omitted episode: %v", err)
	}
	assertOnlyVariantEpisodeInactive(t, pool, missingExternalID)

	provider.mapped = dvdProviderSeason()
	reappeared, err := service.SeasonDetails(ctx, principal, publicSeasonID, "en-US", "tvdb")
	if err != nil {
		t.Fatalf("restore omitted DVD episode: %v", err)
	}
	if restoredIDs := episodeIDs(reappeared.Episodes); !reflect.DeepEqual(restoredIDs, firstIDs) {
		t.Fatalf("reappearing DVD identity did not retain its UUID: first=%v restored=%v", firstIDs, restoredIDs)
	}
	if after := externalIDSnapshot(t, pool); after != canonicalExternalIDs {
		t.Fatalf("canonical external IDs changed after omission/reappearance:\nbefore:\n%s\nafter:\n%s", canonicalExternalIDs, after)
	}
}

func TestMappedSeasonVariantAdvisoryLockCanonicalizesSeriesUUID(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	ctx := context.Background()
	const lowerSeriesID = "abcdefab-cdef-4abc-8def-abcdefabcdef"
	upperSeriesID := strings.ToUpper(lowerSeriesID)
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, display_title)
		VALUES ($1::uuid, 'series', 'Lock Fixture Series')
	`, lowerSeriesID); err != nil {
		t.Fatalf("seed advisory-lock series: %v", err)
	}
	provided := dvdProviderSeason()

	lockKey := func(seriesID string) string {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin advisory-lock transaction: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := persistMappedSeasonVariant(
			ctx,
			tx,
			seriesID,
			mappedSeasonID(seriesID, provided.ExternalID),
			"en-US",
			provided,
		); err != nil {
			t.Fatalf("persist mapped hierarchy for series %q: %v", seriesID, err)
		}
		var key string
		if err := tx.QueryRow(ctx, `
			SELECT classid::text || ':' || objid::text || ':' || objsubid::text
			FROM pg_locks
			WHERE locktype = 'advisory'
			  AND pid = pg_backend_pid()
			  AND granted
		`).Scan(&key); err != nil {
			t.Fatalf("query advisory lock for series %q: %v", seriesID, err)
		}
		return key
	}

	lowerKey := lockKey(lowerSeriesID)
	upperKey := lockKey(upperSeriesID)
	if lowerKey != upperKey {
		t.Fatalf("equivalent UUID spellings acquired different advisory locks: lower=%s upper=%s", lowerKey, upperKey)
	}
}

func TestMappedDVDSeasonRefreshKeepsOtherVariantSeasonsCurrent(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	seedEpisodeOrderCanonicalHierarchy(t, pool)
	seasonOne := dvdProviderSeason()
	seasonTwo := dvdProviderSeasonTwo()
	provider := &episodeOrderProvider{mappedByID: map[string]ProviderSeason{
		seasonOne.ExternalID: seasonOne,
		seasonTwo.ExternalID: seasonTwo,
	}}
	service := NewServiceWithProviderSource(pool, staticProviderSource{providers: ProviderSet{
		Primary: provider,
		Mapper:  provider,
	}}, time.Hour, nil)
	ctx := context.Background()
	principal := canonicalMergePrincipal()

	if _, err := service.SeasonDetails(ctx, principal, mappedSeasonID(episodeOrderSeriesID, seasonOne.ExternalID), "en-US", "tvdb"); err != nil {
		t.Fatalf("persist first DVD season: %v", err)
	}
	persistedSeasonTwo, err := service.SeasonDetails(ctx, principal, mappedSeasonID(episodeOrderSeriesID, seasonTwo.ExternalID), "en-US", "tvdb")
	if err != nil {
		t.Fatalf("persist second DVD season: %v", err)
	}
	seasonTwoEpisodeIDs := episodeIDs(persistedSeasonTwo.Episodes)
	if _, err := service.SeasonDetails(ctx, principal, mappedSeasonID(episodeOrderSeriesID, seasonOne.ExternalID), "en-US", "tvdb"); err != nil {
		t.Fatalf("refresh first DVD season: %v", err)
	}

	var storedSeasonTwoID string
	if err := pool.QueryRow(ctx, `
		SELECT title_id::text
		FROM title_episode_order_identities
		WHERE series_title_id = $1::uuid AND provider = 'tvdb' AND order_id = '2'
		  AND namespace = 'season' AND external_id = $2
	`, episodeOrderSeriesID, seasonTwo.ExternalID).Scan(&storedSeasonTwoID); err != nil {
		t.Fatalf("resolve stored second DVD season: %v", err)
	}
	var playbackAddressable int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM titles
		WHERE (id = $1::uuid OR parent_id = $1::uuid)
		  AND hierarchy_variant = 'tvdb:2'
		  AND is_current
		  AND resource_provider = 'tvdb'
		  AND resource_id <> ''
	`, storedSeasonTwoID).Scan(&playbackAddressable); err != nil {
		t.Fatalf("query playback-addressable second DVD hierarchy: %v", err)
	}
	if playbackAddressable != len(seasonTwo.Episodes)+1 {
		t.Fatalf("refreshing season 1 left only %d/%d season-2 titles playback-addressable", playbackAddressable, len(seasonTwo.Episodes)+1)
	}
	rows, err := pool.Query(ctx, `
		SELECT id::text
		FROM titles
		WHERE parent_id = $1::uuid AND media_type = 'episode'
		  AND hierarchy_variant = 'tvdb:2' AND is_current
		ORDER BY ordinal
	`, storedSeasonTwoID)
	if err != nil {
		t.Fatalf("query current second-season DVD episodes: %v", err)
	}
	defer rows.Close()
	currentSeasonTwoEpisodeIDs := make([]string, 0, len(seasonTwoEpisodeIDs))
	for rows.Next() {
		var episodeID string
		if err := rows.Scan(&episodeID); err != nil {
			t.Fatalf("scan current second-season DVD episode: %v", err)
		}
		currentSeasonTwoEpisodeIDs = append(currentSeasonTwoEpisodeIDs, episodeID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate current second-season DVD episodes: %v", err)
	}
	if !reflect.DeepEqual(currentSeasonTwoEpisodeIDs, seasonTwoEpisodeIDs) {
		t.Fatalf("refreshing season 1 changed/deactivated season-2 UUIDs: before=%v after=%v", seasonTwoEpisodeIDs, currentSeasonTwoEpisodeIDs)
	}
}

func TestMappedDVDSeasonRollbackIsAtomic(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	seedEpisodeOrderCanonicalHierarchy(t, pool)
	provided := dvdProviderSeason()
	provided.Episodes[len(provided.Episodes)-1].ExternalID = ""
	provider := &episodeOrderProvider{mapped: provided}
	service := NewServiceWithProviderSource(pool, staticProviderSource{providers: ProviderSet{
		Primary: provider,
		Mapper:  provider,
	}}, time.Hour, nil)

	_, err := service.SeasonDetails(
		context.Background(),
		canonicalMergePrincipal(),
		mappedSeasonID(episodeOrderSeriesID, provided.ExternalID),
		"en-US",
		"tvdb",
	)
	if !errors.Is(err, ErrProviderFailure) {
		t.Fatalf("invalid DVD episode error = %v, want provider failure", err)
	}
	var variantTitles, variantIdentities int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM titles WHERE hierarchy_variant = 'tvdb:2'`).Scan(&variantTitles); err != nil {
		t.Fatalf("count rolled back variant titles: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM title_episode_order_identities WHERE series_title_id = $1::uuid AND provider = 'tvdb' AND order_id = '2'`, episodeOrderSeriesID).Scan(&variantIdentities); err != nil {
		t.Fatalf("count rolled back variant identities: %v", err)
	}
	if variantTitles != 0 || variantIdentities != 0 {
		t.Fatalf("invalid DVD refresh left partial state: titles=%d identities=%d", variantTitles, variantIdentities)
	}
}

func TestMappedOfficialSeasonRetainsCanonicalMatching(t *testing.T) {
	pool := newCanonicalMergeTestPool(t)
	seedEpisodeOrderCanonicalHierarchy(t, pool)
	provided := dvdProviderSeason()
	provided.EpisodeOrderID = "1"
	provided.EpisodeOrderType = "OFFICIAL"
	provided.Episodes = provided.Episodes[:7]
	for index := range provided.Episodes {
		provided.Episodes[index].ExternalID = fmt.Sprintf("922629%d", index+1)
	}
	provider := &episodeOrderProvider{mapped: provided}
	service := NewServiceWithProviderSource(pool, staticProviderSource{providers: ProviderSet{
		Primary: provider,
		Mapper:  provider,
	}}, time.Hour, nil)

	season, err := service.SeasonDetails(
		context.Background(),
		canonicalMergePrincipal(),
		mappedSeasonID(episodeOrderSeriesID, provided.ExternalID),
		"en-US",
		"tvdb",
	)
	if err != nil {
		t.Fatalf("load official TVDB order: %v", err)
	}
	if got := episodeIDs(season.Episodes); !reflect.DeepEqual(got, episodeOrderCanonicalEpisodeIDs) {
		t.Fatalf("official order stopped using canonical UUIDs: got=%v want=%v", got, episodeOrderCanonicalEpisodeIDs)
	}
	var identityCount int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM title_episode_order_identities`).Scan(&identityCount); err != nil {
		t.Fatalf("count official order identities: %v", err)
	}
	if identityCount != 0 {
		t.Fatalf("official order persisted %d variant identities", identityCount)
	}
}

func dvdProviderSeason() ProviderSeason {
	rawIDs := []string{"9226291", "9226292", "9226293", "9226294", "9226295", "9226296", "10357450", "10357451"}
	episodes := make([]ProviderEpisode, 0, len(rawIDs))
	for index, rawID := range rawIDs {
		episodes = append(episodes, ProviderEpisode{
			ExternalID:     rawID,
			Name:           fmt.Sprintf("DVD Episode %d", index+1),
			Overview:       fmt.Sprintf("DVD episode %d overview", index+1),
			SeasonNumber:   1,
			EpisodeNumber:  index + 1,
			AirDate:        fmt.Sprintf("2024-01-%02d", index+1),
			StillURL:       fmt.Sprintf("https://images.example/dvd-%d.jpg", index+1),
			BackdropURL:    "https://images.example/dvd-background.jpg",
			RuntimeMinutes: 24,
		})
	}
	return ProviderSeason{
		ExternalID:       "2112814",
		Name:             "DVD Season 1",
		Overview:         "DVD season overview",
		SeasonNumber:     1,
		AirDate:          "2024-01-01",
		PosterURL:        "https://images.example/dvd-season.jpg",
		BackdropURL:      "https://images.example/dvd-background.jpg",
		EpisodeOrderID:   "2",
		EpisodeOrderType: "dvd",
		Episodes:         episodes,
	}
}

func dvdProviderSeasonTwo() ProviderSeason {
	return ProviderSeason{
		ExternalID:       "2112815",
		Name:             "DVD Season 2",
		Overview:         "Second DVD season overview",
		SeasonNumber:     2,
		AirDate:          "2024-02-01",
		PosterURL:        "https://images.example/dvd-season-2.jpg",
		BackdropURL:      "https://images.example/dvd-background.jpg",
		EpisodeOrderID:   "2",
		EpisodeOrderType: "dvd",
		Episodes: []ProviderEpisode{
			{
				ExternalID:  "20357450",
				Name:        "DVD Season 2 Episode 1",
				SeasonNumber: 2,
				EpisodeNumber: 1,
				AirDate:      "2024-02-01",
				StillURL:     "https://images.example/dvd-season-2-episode-1.jpg",
			},
			{
				ExternalID:  "20357451",
				Name:        "DVD Season 2 Episode 2",
				SeasonNumber: 2,
				EpisodeNumber: 2,
				AirDate:      "2024-02-08",
				StillURL:     "https://images.example/dvd-season-2-episode-2.jpg",
			},
		},
	}
}

func seedEpisodeOrderCanonicalHierarchy(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, media_type, display_title) VALUES
			('10000000-0000-4000-8000-000000000100', 'series', 'Fixture Series');
		INSERT INTO titles (id, media_type, parent_id, ordinal, display_title) VALUES
			('10000000-0000-4000-8000-000000000110', 'season', '10000000-0000-4000-8000-000000000100', 1, 'Season 1'),
			('10000000-0000-4000-8000-000000000201', 'episode', '10000000-0000-4000-8000-000000000110', 1, 'Aired Episode 1'),
			('10000000-0000-4000-8000-000000000202', 'episode', '10000000-0000-4000-8000-000000000110', 2, 'Aired Episode 2'),
			('10000000-0000-4000-8000-000000000203', 'episode', '10000000-0000-4000-8000-000000000110', 3, 'Aired Episode 3'),
			('10000000-0000-4000-8000-000000000204', 'episode', '10000000-0000-4000-8000-000000000110', 4, 'Aired Episode 4'),
			('10000000-0000-4000-8000-000000000205', 'episode', '10000000-0000-4000-8000-000000000110', 5, 'Aired Episode 5'),
			('10000000-0000-4000-8000-000000000206', 'episode', '10000000-0000-4000-8000-000000000110', 6, 'Aired Episode 6'),
			('10000000-0000-4000-8000-000000000207', 'episode', '10000000-0000-4000-8000-000000000110', 7, 'Aired Episode 7');
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			('10000000-0000-4000-8000-000000000100', 'tmdb', 'series', '700001'),
			('10000000-0000-4000-8000-000000000100', 'tvdb', 'series', '404604'),
			('10000000-0000-4000-8000-000000000110', 'tmdb', 'season', '700101'),
			('10000000-0000-4000-8000-000000000110', 'tvdb', 'season', '2112801'),
			('10000000-0000-4000-8000-000000000201', 'tmdb', 'episode', '800001'),
			('10000000-0000-4000-8000-000000000201', 'tvdb', 'episode', '9226291'),
			('10000000-0000-4000-8000-000000000202', 'tmdb', 'episode', '800002'),
			('10000000-0000-4000-8000-000000000202', 'tvdb', 'episode', '9226292'),
			('10000000-0000-4000-8000-000000000203', 'tmdb', 'episode', '800003'),
			('10000000-0000-4000-8000-000000000203', 'tvdb', 'episode', '9226293'),
			('10000000-0000-4000-8000-000000000204', 'tmdb', 'episode', '800004'),
			('10000000-0000-4000-8000-000000000204', 'tvdb', 'episode', '9226294'),
			('10000000-0000-4000-8000-000000000205', 'tmdb', 'episode', '800005'),
			('10000000-0000-4000-8000-000000000205', 'tvdb', 'episode', '9226295'),
			('10000000-0000-4000-8000-000000000206', 'tmdb', 'episode', '800006'),
			('10000000-0000-4000-8000-000000000206', 'tvdb', 'episode', '9226296'),
			('10000000-0000-4000-8000-000000000207', 'tmdb', 'episode', '800007'),
			('10000000-0000-4000-8000-000000000207', 'tvdb', 'episode', '9226297')
	`); err != nil {
		t.Fatalf("seed episode-order canonical hierarchy: %v", err)
	}
}

func assertDVDSeasonResult(t *testing.T, season Season, publicSeasonID string) {
	t.Helper()
	if season.ID != publicSeasonID || len(season.Episodes) != 8 {
		t.Fatalf("unexpected DVD season: id=%q episodes=%d", season.ID, len(season.Episodes))
	}
	canonical := make(map[string]struct{}, len(episodeOrderCanonicalEpisodeIDs))
	for _, id := range episodeOrderCanonicalEpisodeIDs {
		canonical[id] = struct{}{}
	}
	seen := make(map[string]struct{}, len(season.Episodes))
	for index, episode := range season.Episodes {
		if episode.SeasonID != publicSeasonID || episode.SeasonNumber != 1 || episode.EpisodeNumber != index+1 {
			t.Fatalf("DVD episode %d has unexpected hierarchy: %+v", index, episode)
		}
		if _, exists := canonical[episode.ID]; exists {
			t.Fatalf("DVD episode %d reused canonical UUID %s", index, episode.ID)
		}
		if _, exists := seen[episode.ID]; exists {
			t.Fatalf("DVD episode UUID repeated: %s", episode.ID)
		}
		seen[episode.ID] = struct{}{}
	}
}

func assertVariantCoordinatesAndResources(t *testing.T, pool *pgxpool.Pool, provided ProviderSeason) {
	t.Helper()
	ctx := context.Background()
	var seasonCoordinateCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM titles
		WHERE parent_id = $1::uuid AND media_type = 'season' AND ordinal = 1
		  AND hierarchy_variant IN ('', 'tvdb:2') AND is_current
	`, episodeOrderSeriesID).Scan(&seasonCoordinateCount); err != nil {
		t.Fatalf("count canonical and variant season coordinates: %v", err)
	}
	if seasonCoordinateCount != 2 {
		t.Fatalf("canonical and DVD season coordinates did not coexist: count=%d", seasonCoordinateCount)
	}
	rows, err := pool.Query(ctx, `
		SELECT identity.external_id, title.resource_id, title.resource_provider
		FROM title_episode_order_identities AS identity
		JOIN titles AS title ON title.id = identity.title_id
		WHERE identity.series_title_id = $1::uuid AND identity.provider = 'tvdb'
		  AND identity.order_id = '2' AND identity.namespace = 'episode'
		ORDER BY title.ordinal
	`, episodeOrderSeriesID)
	if err != nil {
		t.Fatalf("query DVD resources: %v", err)
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		var externalID, resourceID, resourceProvider string
		if err := rows.Scan(&externalID, &resourceID, &resourceProvider); err != nil {
			t.Fatalf("scan DVD resource: %v", err)
		}
		if index >= len(provided.Episodes) || externalID != provided.Episodes[index].ExternalID || resourceID != "tvdb:"+externalID || resourceProvider != "tvdb" {
			t.Fatalf("unexpected DVD resource at %d: external=%q resource=%q provider=%q", index, externalID, resourceID, resourceProvider)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate DVD resources: %v", err)
	}
	if index != len(provided.Episodes) {
		t.Fatalf("DVD resource rows=%d want=%d", index, len(provided.Episodes))
	}
}

func assertOnlyVariantEpisodeInactive(t *testing.T, pool *pgxpool.Pool, missingExternalID string) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT identity.external_id, title.is_current
		FROM title_episode_order_identities AS identity
		JOIN titles AS title ON title.id = identity.title_id
		WHERE identity.series_title_id = $1::uuid AND identity.provider = 'tvdb'
		  AND identity.order_id = '2' AND identity.namespace = 'episode'
	`, episodeOrderSeriesID)
	if err != nil {
		t.Fatalf("query omitted DVD identity: %v", err)
	}
	defer rows.Close()
	inactive := make([]string, 0, 1)
	for rows.Next() {
		var externalID string
		var current bool
		if err := rows.Scan(&externalID, &current); err != nil {
			t.Fatalf("scan omitted DVD identity: %v", err)
		}
		if !current {
			inactive = append(inactive, externalID)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate omitted DVD identities: %v", err)
	}
	if !reflect.DeepEqual(inactive, []string{missingExternalID}) {
		t.Fatalf("inactive DVD identities=%v want=%v", inactive, []string{missingExternalID})
	}
}

func externalIDSnapshot(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT title_id::text, provider, namespace, external_id
		FROM title_external_ids
		ORDER BY title_id, provider, namespace, external_id
	`)
	if err != nil {
		t.Fatalf("query canonical external ID snapshot: %v", err)
	}
	defer rows.Close()
	var snapshot strings.Builder
	for rows.Next() {
		var titleID, provider, namespace, externalID string
		if err := rows.Scan(&titleID, &provider, &namespace, &externalID); err != nil {
			t.Fatalf("scan canonical external ID snapshot: %v", err)
		}
		fmt.Fprintf(&snapshot, "%s\t%s\t%s\t%s\n", titleID, provider, namespace, externalID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate canonical external ID snapshot: %v", err)
	}
	return snapshot.String()
}

func episodeIDs(episodes []Episode) []string {
	ids := make([]string, len(episodes))
	for index := range episodes {
		ids[index] = episodes[index].ID
	}
	return ids
}

var _ Provider = (*episodeOrderProvider)(nil)
var _ TelevisionMapper = (*episodeOrderProvider)(nil)
