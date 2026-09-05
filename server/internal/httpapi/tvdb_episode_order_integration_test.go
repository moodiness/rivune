package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/metadata/tvdb"
	"github.com/moodiness/rivune/server/internal/settings"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

const (
	tvdbHandlerSeriesID  = "11000000-0000-4000-8000-000000000100"
	tvdbHandlerProfileID = "11000000-0000-4000-8000-000000000200"
	tvdbHandlerUserID    = "11000000-0000-4000-8000-000000000300"
	tvdbHandlerDeviceID  = "11000000-0000-4000-8000-000000000400"
	tvdbHandlerSessionID = "11000000-0000-4000-8000-000000000500"
)

var tvdbHandlerEpisodeExternalIDs = []string{
	"9226291", "9226292", "9226293", "9226294", "9226295", "9226296", "10357450", "10357451",
}

var tvdbHandlerEpisodeNumbers = []int{
	9226291, 9226292, 9226293, 9226294, 9226295, 9226296, 10357450, 10357451,
}

type tvdbHandlerProviderSource struct {
	client *tvdb.Client
}

func (source tvdbHandlerProviderSource) MetadataProviders() metadata.ProviderSet {
	return metadata.ProviderSet{Generation: 1, Television: source.client, Mapper: source.client}
}

func (tvdbHandlerProviderSource) WatchstateProviders() watchstate.ProviderSet {
	return watchstate.ProviderSet{Generation: 1}
}

type tvdbHandlerFixtureTransport struct {
	mu       sync.Mutex
	requests []string
}

func (transport *tvdbHandlerFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || request.URL.Host != "api4.thetvdb.com" {
		return nil, fmt.Errorf("unexpected external TVDB destination %s", request.URL.Redacted())
	}
	if request.Method != http.MethodGet && request.Method != http.MethodPost {
		return nil, fmt.Errorf("unexpected TVDB method %s", request.Method)
	}
	transport.mu.Lock()
	transport.requests = append(transport.requests, request.URL.RequestURI())
	transport.mu.Unlock()

	path := strings.TrimPrefix(request.URL.Path, "/v4")
	if path != "/login" && request.Header.Get("Authorization") != "Bearer deterministic-tvdb-token" {
		return nil, fmt.Errorf("missing deterministic TVDB authorization for %s", path)
	}
	var payload any
	switch path {
	case "/login":
		if request.Method != http.MethodPost {
			return nil, fmt.Errorf("TVDB login used %s", request.Method)
		}
		var credentials map[string]string
		if err := json.NewDecoder(request.Body).Decode(&credentials); err != nil {
			return nil, fmt.Errorf("decode TVDB login request: %w", err)
		}
		if credentials["apikey"] != "deterministic-api-key" || credentials["pin"] != "deterministic-pin" {
			return nil, fmt.Errorf("unexpected TVDB login credentials")
		}
		payload = map[string]any{"status": "success", "data": map[string]string{"token": "deterministic-tvdb-token"}}
	case "/series/404604/extended":
		payload = map[string]any{
			"status": "success",
			"data": map[string]any{
				"id": 404604, "name": "Fixture Series", "defaultSeasonType": 1,
				"seasonTypes": []map[string]any{
					{"id": 1, "name": "Aired Order", "type": "official"},
					{"id": 2, "name": "DVD Order", "type": "dvd"},
				},
				"seasons": []map[string]any{
					{"id": 2112801, "name": "Aired Season 1", "number": 1, "seriesId": 404604, "type": map[string]any{"id": 1, "type": "official"}},
					{"id": 2112814, "name": "DVD Season 1", "number": 1, "seriesId": 404604, "imageType": 7, "type": map[string]any{"id": 2, "type": "dvd"}},
				},
			},
		}
	case "/seasons/2112814/extended":
		episodes := make([]map[string]any, len(tvdbHandlerEpisodeNumbers))
		for index, id := range tvdbHandlerEpisodeNumbers {
			episodes[index] = map[string]any{"id": id, "seriesId": 404604, "seasonNumber": 1, "number": index + 1}
		}
		payload = map[string]any{
			"status": "success",
			"data": map[string]any{
				"id": 2112814, "seriesId": 404604, "name": "DVD Season 1", "number": 1,
				"image": "http://invalid.example/season.jpg", "imageType": 7,
				"type": map[string]any{"id": 2, "type": "dvd"},
				"artwork": []map[string]any{
					{"id": 1, "image": "https://artworks.thetvdb.com/landscape.jpg", "type": 7, "width": 1920, "height": 1080, "score": 100},
					{"id": 2, "image": "https://artworks.thetvdb.com/dvd-season-1.jpg", "type": 7, "width": 680, "height": 1000, "score": 50},
				},
				"episodes": episodes,
			},
		}
	case "/series/404604/episodes/dvd":
		if request.URL.Query().Get("page") != "0" || request.URL.Query().Get("season") != "1" {
			return nil, fmt.Errorf("unexpected DVD episode query %q", request.URL.RawQuery)
		}
		episodes := make([]map[string]any, 0, len(tvdbHandlerEpisodeNumbers))
		for index := len(tvdbHandlerEpisodeNumbers) - 1; index >= 0; index-- {
			episodes = append(episodes, map[string]any{
				"id": tvdbHandlerEpisodeNumbers[index], "seriesId": 404604,
				"name": fmt.Sprintf("DVD Episode %d", index+1), "overview": fmt.Sprintf("DVD episode %d overview", index+1),
				"aired": fmt.Sprintf("2024-01-%02d", index+1), "image": fmt.Sprintf("https://artworks.thetvdb.com/dvd-%d.jpg", index+1),
				"runtime": 24, "seasonNumber": 1, "number": index + 1,
			})
		}
		payload = map[string]any{
			"status": "success",
			"data": map[string]any{"series": map[string]any{"id": 404604}, "episodes": episodes},
			"links": map[string]any{"next": nil},
		}
	default:
		return nil, fmt.Errorf("unexpected TVDB fixture route %s", request.URL.RequestURI())
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode TVDB fixture response: %w", err)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    request,
	}, nil
}

func TestTVDBEpisodeOrderHandlersEndToEnd(t *testing.T) {
	pool := tvdbHandlerTestPool(t)
	principal := seedTVDBHandlerFixture(t, pool)
	transport := &tvdbHandlerFixtureTransport{}
	providerClient := tvdb.New("deterministic-api-key", "deterministic-pin", &http.Client{Transport: transport})
	providerSource := tvdbHandlerProviderSource{client: providerClient}

	metadataService := metadata.NewServiceWithProviderSource(
		pool,
		providerSource,
		time.Hour,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	watchstateService := watchstate.NewServiceWithProviderSource(pool, time.UTC, providerSource)
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: principal}
	api.metadata = metadataService
	api.watchstate = watchstateService
	api.settings = &fakeSettingsService{effective: settings.Effective{Values: settings.EffectiveValues{
		MaximumCastMembers: settings.DefaultMaximumCastMembers,
		MetadataLanguage:   "fr-FR",
	}}}

	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)
	client := server.Client()
	mappedSeasonID := "tvdb:" + tvdbHandlerSeriesID + ":2112814"

	seriesBody := tvdbHandlerRequest(t, client, http.MethodGet,
		server.URL+"/api/v1/metadata/series/"+tvdbHandlerSeriesID+"?language=fr-FR&mappingProvider=tvdb&episodeOrder=2", "")
	var series metadata.Series
	tvdbHandlerDecode(t, seriesBody, &series)
	if series.ID != tvdbHandlerSeriesID || series.MappingProvider != "tvdb" || series.SelectedEpisodeOrderID != "2" {
		t.Fatalf("series TVDB selection = id %q provider %q order %q", series.ID, series.MappingProvider, series.SelectedEpisodeOrderID)
	}
	if len(series.Seasons) != 1 || series.Seasons[0].ID != mappedSeasonID || series.Seasons[0].EpisodeCount != 8 {
		t.Fatalf("series DVD seasons = %+v", series.Seasons)
	}
	if series.Seasons[0].PosterURL != "https://artworks.thetvdb.com/dvd-season-1.jpg" {
		t.Fatalf("validated season artwork = %q", series.Seasons[0].PosterURL)
	}

	seasonPath := server.URL + "/api/v1/metadata/seasons/" + mappedSeasonID + "?language=fr-FR&mappingProvider=tvdb"
	firstSeasonBody := tvdbHandlerRequest(t, client, http.MethodGet, seasonPath, "")
	var firstSeason metadata.Season
	tvdbHandlerDecode(t, firstSeasonBody, &firstSeason)
	assertTVDBHandlerSeason(t, firstSeason, mappedSeasonID)
	firstIDs := tvdbHandlerEpisodeIDs(firstSeason.Episodes)

	secondSeasonBody := tvdbHandlerRequest(t, client, http.MethodGet, seasonPath, "")
	var secondSeason metadata.Season
	tvdbHandlerDecode(t, secondSeasonBody, &secondSeason)
	assertTVDBHandlerSeason(t, secondSeason, mappedSeasonID)
	if secondIDs := tvdbHandlerEpisodeIDs(secondSeason.Episodes); !reflect.DeepEqual(secondIDs, firstIDs) {
		t.Fatalf("repeated DVD season changed episode UUIDs: first=%v second=%v", firstIDs, secondIDs)
	}

	progressBody := tvdbHandlerRequest(t, client, http.MethodPut,
		server.URL+"/api/v1/progress/"+firstSeason.Episodes[6].ID,
		`{"positionSeconds":240,"durationSeconds":1440,"completed":false,"expectedVersion":0}`)
	var progress watchstate.Progress
	tvdbHandlerDecode(t, progressBody, &progress)
	if progress.TitleID != firstSeason.Episodes[6].ID || progress.PositionSeconds != 240 || progress.DurationSeconds != 1440 {
		t.Fatalf("recorded DVD progress = %+v", progress)
	}

	continueBody := tvdbHandlerRequest(t, client, http.MethodGet, server.URL+"/api/v1/continue-watching", "")
	var continued watchstate.ContinuePage
	tvdbHandlerDecode(t, continueBody, &continued)
	if len(continued.Items) != 1 {
		t.Fatalf("continue watching items = %d, want 1", len(continued.Items))
	}
	item := continued.Items[0]
	if item.TitleID != firstSeason.Episodes[6].ID || item.MappingProvider != "tvdb" || item.EpisodeOrderID != "2" || item.MetadataSeasonID != mappedSeasonID {
		t.Fatalf("continue watching TVDB context = %+v", item)
	}
	if item.ResourceProvider != "tvdb" || item.ResourceID != "tvdb:10357450" {
		t.Fatalf("continue watching resource = %q/%q", item.ResourceProvider, item.ResourceID)
	}
	var storedSeasonID string
	if err := pool.QueryRow(context.Background(), `
		SELECT title_id::text
		FROM title_episode_order_identities
		WHERE series_title_id = $1::uuid AND provider = 'tvdb' AND order_id = '2'
		  AND namespace = 'season' AND external_id = '2112814'
	`, tvdbHandlerSeriesID).Scan(&storedSeasonID); err != nil {
		t.Fatalf("resolve stored DVD season UUID: %v", err)
	}
	if item.SeasonID != storedSeasonID {
		t.Fatalf("continue watching season UUID = %q, want %q", item.SeasonID, storedSeasonID)
	}

	transport.mu.Lock()
	requests := append([]string(nil), transport.requests...)
	transport.mu.Unlock()
	if len(requests) != 9 {
		t.Fatalf("TVDB fixture requests = %v, want 9 deterministic requests", requests)
	}
}

func tvdbHandlerTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run TVDB episode-order handler integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	basePool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open TVDB handler integration database: %v", err)
	}
	t.Cleanup(basePool.Close)
	schema := fmt.Sprintf("tvdb_handler_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := basePool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create isolated TVDB handler schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = basePool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
	})
	configuration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse TVDB handler integration database URL: %v", err)
	}
	configuration.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		t.Fatalf("open isolated TVDB handler pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate isolated TVDB handler schema: %v", err)
	}
	var version int
	if err := pool.QueryRow(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read isolated TVDB handler schema version: %v", err)
	}
	if version != 95 {
		t.Fatalf("isolated TVDB handler schema version = %d, want 95", version)
	}
	return pool
}

func seedTVDBHandlerFixture(t *testing.T, pool *pgxpool.Pool) auth.Principal {
	t.Helper()
	base := metadata.Series{
		ID: tvdbHandlerSeriesID, MediaType: metadata.MediaTypeSeries, Name: "Fixture Series",
		Cast: []metadata.CastMember{}, Seasons: []metadata.SeasonSummary{}, Aliases: []metadata.Alias{},
		EpisodeOrders: []metadata.EpisodeOrder{
			{ID: "1", Name: "Aired Order", Type: "official", IsDefault: true},
			{ID: "2", Name: "DVD Order", Type: "dvd"},
		},
		MappingProvider: "tmdb",
		ExternalIDs: map[string]string{"tmdb": "700001", "tvdb": "404604"},
	}
	payload, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("encode canonical series fixture: %v", err)
	}
	contextHash := bytes.Repeat([]byte{0xb7}, 32)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, password_hash, role)
		VALUES ($1::uuid, 'tvdb-handler-admin', 'unused-test-password', 'admin');
		INSERT INTO profiles (id, name) VALUES ($2::uuid, 'TVDB Handler Profile');
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($1::uuid, $2::uuid, true);
		INSERT INTO devices (id, user_id, name, platform)
		VALUES ($3::uuid, $1::uuid, 'TVDB Handler Device', 'test');
		INSERT INTO auth_sessions (
			id, user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id, active_profile_id, profile_grant_expires_at, profile_context_hash
		) VALUES (
			$4::uuid, $1::uuid, $3::uuid, decode(repeat('b8', 32), 'hex'),
			now() + interval '1 hour', now() + interval '2 hours', 'global_admin', NULL,
			$2::uuid, now() + interval '1 hour', $5
		);
		INSERT INTO titles (id, media_type, display_title)
		VALUES ($6::uuid, 'series', 'Fixture Series');
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id) VALUES
			($6::uuid, 'tmdb', 'series', '700001'),
			($6::uuid, 'tvdb', 'series', '404604');
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at)
		VALUES ($6::uuid, 'tmdb', 'fr-FR', $7::jsonb, now() + interval '1 hour');
	`, pgx.QueryExecModeSimpleProtocol, tvdbHandlerUserID, tvdbHandlerProfileID, tvdbHandlerDeviceID,
		tvdbHandlerSessionID, contextHash, tvdbHandlerSeriesID, string(payload)); err != nil {
		t.Fatalf("seed TVDB handler integration fixture: %v", err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	return auth.Principal{
		SessionID: tvdbHandlerSessionID, UserID: tvdbHandlerUserID, DeviceID: tvdbHandlerDeviceID,
		Username: "tvdb-handler-admin", Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: new(tvdbHandlerProfileID), ProfileGrantExpiresAt: &expiresAt,
		ProfileContextHash: contextHash, ActiveProfileCanManage: true,
	}
}

func tvdbHandlerRequest(t *testing.T, client *http.Client, method, url, body string) []byte {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("create %s %s: %v", method, url, err)
	}
	request.Header.Set("Authorization", "Bearer integration-access-token")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("perform %s %s: %v", method, url, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, url, err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s %s status = %d, want 200; body=%s", method, url, response.StatusCode, responseBody)
	}
	if bytes.Contains(responseBody, []byte("internal_error")) {
		t.Fatalf("%s %s returned internal_error: %s", method, url, responseBody)
	}
	return responseBody
}

func tvdbHandlerDecode(t *testing.T, body []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(body, destination); err != nil {
		t.Fatalf("decode handler response: %v; body=%s", err, body)
	}
}

func assertTVDBHandlerSeason(t *testing.T, season metadata.Season, mappedSeasonID string) {
	t.Helper()
	if season.ID != mappedSeasonID || season.SeriesID != tvdbHandlerSeriesID || season.PosterURL != "https://artworks.thetvdb.com/dvd-season-1.jpg" {
		t.Fatalf("DVD season identity/artwork = %+v", season)
	}
	if len(season.Episodes) != len(tvdbHandlerEpisodeExternalIDs) {
		t.Fatalf("DVD season episodes = %d, want %d", len(season.Episodes), len(tvdbHandlerEpisodeExternalIDs))
	}
	seen := make(map[string]struct{}, len(season.Episodes))
	for index, episode := range season.Episodes {
		if episode.SeasonID != mappedSeasonID || episode.SeasonNumber != 1 || episode.EpisodeNumber != index+1 {
			t.Fatalf("DVD episode %d coordinates = season %q S%dE%d", index, episode.SeasonID, episode.SeasonNumber, episode.EpisodeNumber)
		}
		if episode.ExternalIDs["tvdb"] != tvdbHandlerEpisodeExternalIDs[index] {
			t.Fatalf("DVD episode %d TVDB identity = %q, want %q", index, episode.ExternalIDs["tvdb"], tvdbHandlerEpisodeExternalIDs[index])
		}
		if _, duplicate := seen[episode.ID]; duplicate || episode.ID == "" {
			t.Fatalf("DVD episode %d has invalid/repeated UUID %q", index, episode.ID)
		}
		seen[episode.ID] = struct{}{}
	}
}

func tvdbHandlerEpisodeIDs(episodes []metadata.Episode) []string {
	ids := make([]string, len(episodes))
	for index := range episodes {
		ids[index] = episodes[index].ID
	}
	return ids
}
