package tvdb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/moodiness/rivune/server/internal/metadata"
)

func TestProductionClientRejectsCrossOriginRedirect(t *testing.T) {
	client := New("test-api-key", "test-pin", nil)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://[::1]/private", nil)
	if err != nil {
		t.Fatalf("create redirect request: %v", err)
	}
	if err := client.httpClient.CheckRedirect(request, nil); err == nil {
		t.Fatal("TVDB production client accepted a loopback redirect")
	}
}

func TestEnrichSeriesAuthenticatesWithoutPINAndCachesToken(t *testing.T) {
	var loginCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			loginCalls.Add(1)
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode login: %v", err)
			}
			if body["apikey"] != "api-key" {
				t.Fatalf("unexpected API key %q", body["apikey"])
			}
			if _, included := body["pin"]; included {
				t.Fatal("login unexpectedly included a PIN")
			}
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]string{"token": "tvdb-token"}})
		case "/series/93001/extended":
			if r.Header.Get("Authorization") != "Bearer tvdb-token" {
				t.Fatalf("unexpected authorization %q", r.Header.Get("Authorization"))
			}
			writeJSON(t, w, map[string]any{
				"status": "success",
				"data": map[string]any{
					"id": 93001, "name": "Fixture Series", "overview": "Fixture series overview.", "image": "https://artworks.thetvdb.com/poster.jpg",
					"defaultSeasonType": 1,
					"aliases":           []map[string]any{{"language": "eng", "name": "Fixture Series"}, {"language": "eng", "name": "Fixture Series"}, {"language": "spa", "name": "Fixture Series Alternate"}},
					"seasonTypes": []map[string]any{
						{"id": 1, "name": "Aired Order", "alternateName": "Official", "type": "official"},
						{"id": 2, "name": "DVD Order", "type": "dvd"},
						{"id": 3, "name": "Absolute Order", "type": "absolute"},
						{"id": 4, "name": "Alternate Order", "alternateName": "Story Order", "type": "alternate"},
						{"id": 7, "name": "Alternate Order 2", "alternateName": "Streaming Order", "type": "alttwo"},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newWithBaseURL("api-key", "", server.URL, server.Client())
	input := metadata.ProviderSeries{AdditionalIDs: map[string]string{"tvdb": "93001"}}
	first, err := client.EnrichSeries(context.Background(), input)
	if err != nil {
		t.Fatalf("enrich first series: %v", err)
	}
	if _, err := client.EnrichSeries(context.Background(), input); err != nil {
		t.Fatalf("enrich second series: %v", err)
	}
	if loginCalls.Load() != 1 {
		t.Fatalf("expected one cached login, got %d", loginCalls.Load())
	}
	if first.Name != "Fixture Series" || first.Overview != "Fixture series overview." || first.PosterURL == "" {
		t.Fatalf("unexpected enriched series: %+v", first)
	}
	if len(first.Aliases) != 2 || len(first.EpisodeOrders) != 5 || !first.EpisodeOrders[0].IsDefault {
		t.Fatalf("unexpected TVDB provider metadata: aliases=%+v orders=%+v", first.Aliases, first.EpisodeOrders)
	}
	wantOrderNames := []string{"Aired Order", "DVD Order", "Absolute Order", "Story Order", "Streaming Order"}
	for index, wantName := range wantOrderNames {
		if first.EpisodeOrders[index].Name != wantName {
			t.Fatalf("unexpected order %d: got %+v want name %q", index, first.EpisodeOrders[index], wantName)
		}
	}
}

func TestEnrichSeasonMatchesOfficialEpisodesByNumberAndAirDate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]string{"token": "token"}})
		case "/series/93001/episodes/official":
			if r.URL.Query().Get("page") != "0" || r.URL.Query().Get("season") != "1" {
				t.Fatalf("unexpected query %q", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{
				"status": "success",
				"data": map[string]any{
					"series": map[string]any{"id": 93001},
					"episodes": []map[string]any{
						{"id": 9301101, "seriesId": 93001, "name": "Fixture Episode", "overview": "Fixture episode overview.", "aired": "2024-01-07", "image": "https://artworks.thetvdb.com/still.jpg", "runtime": 59, "seasonNumber": 1, "number": 1},
						{"id": 9301102, "seriesId": 93001, "name": "Fixture Episode 2", "aired": "2024-01-14", "seasonNumber": 1, "number": 2},
						{"id": 999, "seriesId": 93001, "name": "Other season", "seasonNumber": 2, "number": 1},
					},
				},
				"links": map[string]any{"next": nil},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newWithBaseURL("api-key", "", server.URL, server.Client())
	season, err := client.EnrichSeason(context.Background(), "93001", metadata.ProviderSeason{
		SeasonNumber: 1,
		Episodes: []metadata.ProviderEpisode{
			{ExternalID: "tmdb-episode-1", SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2024-01-07"},
			{ExternalID: "tmdb-episode-2", Name: "Different hierarchy", SeasonNumber: 1, EpisodeNumber: 2, AirDate: "2025-09-01"},
		},
	})
	if err != nil {
		t.Fatalf("enrich season: %v", err)
	}
	episode := season.Episodes[0]
	if episode.AdditionalIDs["tvdb"] != "9301101" || episode.Name != "Fixture Episode" || episode.RuntimeMinutes != 59 || episode.StillURL == "" {
		t.Fatalf("unexpected enriched episode: %+v", episode)
	}
	if _, exists := season.Episodes[1].AdditionalIDs["tvdb"]; exists || season.Episodes[1].Name != "Different hierarchy" {
		t.Fatalf("cross-linked episode with a different air date: %+v", season.Episodes[1])
	}
}

func TestEnrichSeriesRefreshesRejectedTokenOnce(t *testing.T) {
	var loginCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			call := loginCalls.Add(1)
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]string{"token": "token-" + string(rune('0'+call))}})
		case "/series/1/extended":
			if r.Header.Get("Authorization") == "Bearer token-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{"id": 1}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newWithBaseURL("api-key", "", server.URL, server.Client())
	if _, err := client.EnrichSeries(context.Background(), metadata.ProviderSeries{AdditionalIDs: map[string]string{"tvdb": "1"}}); err != nil {
		t.Fatalf("enrich after token refresh: %v", err)
	}
	if loginCalls.Load() != 2 {
		t.Fatalf("expected two logins, got %d", loginCalls.Load())
	}
}

func TestSeriesSeasonUsesOrderSpecificDVDCoordinates(t *testing.T) {
	for _, test := range []struct {
		name        string
		malformed   bool
		wantFailure bool
	}{
		{name: "split DVD coordinates"},
		{name: "malformed order hierarchy", malformed: true, wantFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.RequestURI())
				switch r.URL.Path {
				case "/login":
					writeJSON(t, w, map[string]any{"status": "success", "data": map[string]string{"token": "token"}})
				case "/series/404604/extended":
					writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{
						"id": 404604,
						"seasonTypes": []map[string]any{{"id": 2, "name": "DVD Order", "type": "dvd"}},
						"seasons": []map[string]any{{
							"id": 2112814, "name": "DVD Season 1", "number": 1, "seriesId": 404604,
							"type": map[string]any{"id": 2, "type": "dvd"},
						}},
					}})
				case "/seasons/2112814/extended":
					writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{
						"id": 2112814, "seriesId": 404604, "number": 1,
						"type": map[string]any{"id": 2, "type": "dvd"},
						"episodes": []map[string]any{{
							"id": 1, "name": "Unusable extended coordinate", "seasonNumber": 99, "number": 99,
						}},
					}})
				case "/series/404604/episodes/dvd":
					if r.URL.Query().Get("page") != "0" || r.URL.Query().Get("season") != "1" {
						t.Fatalf("unexpected order query %q", r.URL.RawQuery)
					}
					seriesID := 404604
					if test.malformed {
						seriesID = 999999
					}
					ids := []int{9226291, 9226292, 9226293, 9226294, 9226295, 9226296, 10357450, 10357451}
					episodes := make([]map[string]any, 0, len(ids))
					for index := len(ids) - 1; index >= 0; index-- {
						episodes = append(episodes, map[string]any{
							"id": ids[index], "seriesId": seriesID, "name": "DVD Episode",
							"seasonNumber": 1, "number": index + 1,
						})
					}
					writeJSON(t, w, map[string]any{
						"status": "success",
						"data": map[string]any{
							"series":   map[string]any{"id": 404604},
							"episodes": episodes,
						},
						"links": map[string]any{"next": nil},
					})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			client := newWithBaseURL("api-key", "", server.URL, server.Client())
			got, err := client.SeriesSeason(context.Background(), "404604", "2112814")
			if test.wantFailure {
				if !errors.Is(err, metadata.ErrProviderFailure) {
					t.Fatalf("malformed hierarchy error = %v, want provider failure", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("map DVD season: %v", err)
			}
			if got.EpisodeOrderID != "2" || got.EpisodeOrderType != "dvd" {
				t.Fatalf("order identity = %q/%q, want 2/dvd", got.EpisodeOrderID, got.EpisodeOrderType)
			}
			if len(got.Episodes) != 8 {
				t.Fatalf("episodes = %d, want 8", len(got.Episodes))
			}
			for index, episode := range got.Episodes {
				if episode.SeasonNumber != 1 || episode.EpisodeNumber != index+1 {
					t.Fatalf("episode %d coordinates = S%dE%d", index, episode.SeasonNumber, episode.EpisodeNumber)
				}
			}
			if got.Episodes[6].ExternalID != "10357450" || got.Episodes[7].ExternalID != "10357451" {
				t.Fatalf("split identities = %q/%q", got.Episodes[6].ExternalID, got.Episodes[7].ExternalID)
			}
			wantRequests := []string{
				"/login",
				"/series/404604/extended",
				"/seasons/2112814/extended",
				"/series/404604/episodes/dvd?page=0&season=1",
			}
			if len(requests) != len(wantRequests) {
				t.Fatalf("requests = %q, want %q", requests, wantRequests)
			}
			for index := range wantRequests {
				if requests[index] != wantRequests[index] {
					t.Fatalf("request %d = %q, want %q", index, requests[index], wantRequests[index])
				}
			}
		})
	}
}

func TestSeriesMappingUsesSelectedTVDBSeasonHierarchy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]string{"token": "token"}})
		case "/series/81797/extended":
			writeJSON(t, w, map[string]any{
				"status": "success",
				"data": map[string]any{
					"id": 81797,
					"seasonTypes": []map[string]any{
						{"id": 1, "name": "Aired Order", "type": "official"},
						{"id": 2, "name": "DVD Order", "type": "dvd"},
						{"id": 3, "name": "Absolute Order", "type": "absolute"},
						{"id": 7, "name": "Alternate Order 2", "alternateName": "Streaming Order", "type": "alttwo"},
					},
					"seasons": []map[string]any{
						{"id": 1001, "name": "Season 1", "number": 1, "seriesId": 81797, "image": "https://artworks.thetvdb.com/s1.jpg", "type": map[string]any{"id": 1, "type": "official"}},
						{"id": 1002, "name": "Season 2", "number": 2, "seriesId": 81797, "image": "https://artworks.thetvdb.com/s2.jpg", "type": map[string]any{"id": 1, "type": "official"}},
						{"id": 2001, "name": "DVD Season", "number": 1, "seriesId": 81797, "type": map[string]any{"id": 2, "type": "dvd"}},
						{"id": 3001, "name": "Absolute Season", "number": 1, "seriesId": 81797, "type": map[string]any{"id": 3, "type": "absolute"}},
						{"id": 7001, "name": "Streaming Season", "number": 1, "seriesId": 81797, "type": map[string]any{"id": 7, "type": "alttwo"}},
					},
				},
			})
		case "/seasons/1001/extended":
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{
				"id": 1001, "seriesId": 81797, "number": 1, "type": map[string]any{"id": 1, "type": "official"},
				"episodes": []map[string]any{
					{"id": 1101, "name": "Episode 1", "aired": "2024-01-07", "seasonNumber": 1, "number": 1},
					{"id": 1102, "name": "Episode 2", "aired": "2024-01-14", "seasonNumber": 1, "number": 2},
				},
			}})
		case "/seasons/1002/extended":
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{
				"id": 1002, "seriesId": 81797, "number": 2, "type": map[string]any{"id": 1, "type": "official"},
				"episodes": []map[string]any{
					{"id": 1201, "name": "Episode 1", "overview": "Second season", "aired": "2025-01-05", "runtime": 24, "seasonNumber": 2, "number": 1},
				},
			}})
		case "/seasons/2001/extended":
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{
				"id": 2001, "seriesId": 81797, "number": 1, "type": map[string]any{"id": 2, "type": "dvd"},
				"episodes": []map[string]any{
					{"id": 2101, "name": "DVD Episode", "aired": "2024-01-07", "seasonNumber": 1, "number": 1},
				},
			}})
		case "/seasons/3001/extended":
			episodes := make([]map[string]any, 501)
			for index := range episodes {
				episodes[index] = map[string]any{"id": 3101 + index, "name": "Absolute Episode", "seasonNumber": 1, "number": index + 1}
			}
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{
				"id": 3001, "seriesId": 81797, "number": 1, "type": map[string]any{"id": 3, "type": "absolute"}, "episodes": episodes,
			}})
		case "/seasons/7001/extended":
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{
				"id": 7001, "seriesId": 81797, "number": 1, "type": map[string]any{"id": 7, "type": "alttwo"},
				"episodes": []map[string]any{
					{"id": 7101, "name": "Streaming Episode 1", "seasonNumber": 1, "number": 1},
					{"id": 7102, "name": "Streaming Episode 2", "seasonNumber": 1, "number": 2},
				},
			}})
		case "/series/81797/episodes/official":
			if r.URL.Query().Get("season") != "2" || r.URL.Query().Get("page") != "0" {
				t.Fatalf("unexpected official order query %q", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{
				"series": map[string]any{"id": 81797},
				"episodes": []map[string]any{{"id": 1201, "seriesId": 81797, "name": "Episode 1", "overview": "Second season", "aired": "2025-01-05", "runtime": 24, "seasonNumber": 2, "number": 1}},
			}, "links": map[string]any{"next": nil}})
		case "/series/81797/episodes/dvd":
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{
				"series": map[string]any{"id": 81797},
				"episodes": []map[string]any{{"id": 2101, "seriesId": 81797, "name": "DVD Episode", "aired": "2024-01-07", "seasonNumber": 1, "number": 1}},
			}, "links": map[string]any{"next": nil}})
		case "/series/81797/episodes/absolute":
			page, err := strconv.Atoi(r.URL.Query().Get("page"))
			if err != nil || (page != 0 && page != 1) || r.URL.Query().Get("season") != "1" {
				t.Fatalf("unexpected absolute order query %q", r.URL.RawQuery)
			}
			start, count := 0, 500
			next := any("https://api4.thetvdb.com/v4/series/81797/episodes/absolute?page=1&season=1")
			if page == 1 {
				start, count, next = 500, 1, nil
			}
			episodes := make([]map[string]any, count)
			for index := range episodes {
				ordinal := start + index + 1
				episodes[index] = map[string]any{"id": 3100 + ordinal, "seriesId": 81797, "name": "Absolute Episode", "seasonNumber": 1, "number": ordinal}
			}
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{
				"series": map[string]any{"id": 81797}, "episodes": episodes,
			}, "links": map[string]any{"next": next}})
		case "/series/81797/episodes/alttwo":
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{
				"series": map[string]any{"id": 81797},
				"episodes": []map[string]any{
					{"id": 7101, "seriesId": 81797, "name": "Streaming Episode 1", "seasonNumber": 1, "number": 1},
					{"id": 7102, "seriesId": 81797, "name": "Streaming Episode 2", "seasonNumber": 1, "number": 2},
				},
			}, "links": map[string]any{"next": nil}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newWithBaseURL("api-key", "", server.URL, server.Client())
	seasons, err := client.SeriesSeasons(context.Background(), "81797", "")
	if err != nil {
		t.Fatalf("map series seasons: %v", err)
	}
	if len(seasons) != 2 || seasons[0].ExternalID != "1001" || seasons[0].EpisodeCount != 2 || seasons[1].ExternalID != "1002" || seasons[1].EpisodeCount != 1 {
		t.Fatalf("unexpected official seasons: %+v", seasons)
	}
	dvdSeasons, err := client.SeriesSeasons(context.Background(), "81797", "2")
	if err != nil {
		t.Fatalf("map DVD series seasons: %v", err)
	}
	if len(dvdSeasons) != 1 || dvdSeasons[0].ExternalID != "2001" || dvdSeasons[0].EpisodeCount != 1 {
		t.Fatalf("unexpected DVD seasons: %+v", dvdSeasons)
	}
	absoluteSeasons, err := client.SeriesSeasons(context.Background(), "81797", "3")
	if err != nil {
		t.Fatalf("map absolute series seasons: %v", err)
	}
	if len(absoluteSeasons) != 1 || absoluteSeasons[0].ExternalID != "3001" || absoluteSeasons[0].EpisodeCount != 501 {
		t.Fatalf("unexpected absolute seasons: %+v", absoluteSeasons)
	}
	streamingSeasons, err := client.SeriesSeasons(context.Background(), "81797", "7")
	if err != nil {
		t.Fatalf("map streaming series seasons: %v", err)
	}
	if len(streamingSeasons) != 1 || streamingSeasons[0].ExternalID != "7001" || streamingSeasons[0].EpisodeCount != 2 {
		t.Fatalf("unexpected streaming seasons: %+v", streamingSeasons)
	}
	season, err := client.SeriesSeason(context.Background(), "81797", "1002")
	if err != nil {
		t.Fatalf("map second season: %v", err)
	}
	if season.SeasonNumber != 2 || len(season.Episodes) != 1 || season.Episodes[0].ExternalID != "1201" || season.Episodes[0].EpisodeNumber != 1 {
		t.Fatalf("unexpected mapped second season: %+v", season)
	}
	dvdSeason, err := client.SeriesSeason(context.Background(), "81797", "2001")
	if err != nil {
		t.Fatalf("map DVD season: %v", err)
	}
	if dvdSeason.SeasonNumber != 1 || len(dvdSeason.Episodes) != 1 || dvdSeason.Episodes[0].ExternalID != "2101" {
		t.Fatalf("unexpected mapped DVD season: %+v", dvdSeason)
	}
	absoluteSeason, err := client.SeriesSeason(context.Background(), "81797", "3001")
	if err != nil {
		t.Fatalf("map absolute season: %v", err)
	}
	if len(absoluteSeason.Episodes) != 501 || absoluteSeason.Episodes[500].ExternalID != "3601" || absoluteSeason.Episodes[500].EpisodeNumber != 501 {
		t.Fatalf("unexpected mapped absolute season: count=%d last=%+v", len(absoluteSeason.Episodes), absoluteSeason.Episodes[len(absoluteSeason.Episodes)-1])
	}
	streamingSeason, err := client.SeriesSeason(context.Background(), "81797", "7001")
	if err != nil {
		t.Fatalf("map streaming season: %v", err)
	}
	if streamingSeason.SeasonNumber != 1 || len(streamingSeason.Episodes) != 2 || streamingSeason.Episodes[1].ExternalID != "7102" {
		t.Fatalf("unexpected mapped streaming season: %+v", streamingSeason)
	}
}

func TestSeriesMappingPreservesSpecialsSeasonZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]string{"token": "token"}})
		case "/series/81797/extended":
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{
				"id":                81797,
				"defaultSeasonType": 1,
				"seasonTypes":       []map[string]any{{"id": 1, "name": "Aired Order", "type": "official"}},
				"seasons": []map[string]any{{
					"id": 1000, "number": 0, "seriesId": 81797,
					"type": map[string]any{"id": 1, "type": "official"},
				}},
			}})
		case "/seasons/1000/extended":
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{
				"id": 1000, "seriesId": 81797, "number": 0,
				"type": map[string]any{"id": 1, "type": "official"},
				"episodes": []map[string]any{{
					"id": 10001, "name": "Behind the Scenes", "aired": "2024-01-01",
					"seasonNumber": 0, "number": 1,
				}},
			}})
		case "/series/81797/episodes/official":
			if r.URL.Query().Get("season") != "0" || r.URL.Query().Get("page") != "0" {
				t.Fatalf("unexpected specials order query %q", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{
				"series": map[string]any{"id": 81797},
				"episodes": []map[string]any{{
					"id": 10001, "seriesId": 81797, "name": "Behind the Scenes", "aired": "2024-01-01", "seasonNumber": 0, "number": 1,
				}},
			}, "links": map[string]any{"next": nil}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newWithBaseURL("api-key", "", server.URL, server.Client())
	seasons, err := client.SeriesSeasons(context.Background(), "81797", "")
	if err != nil {
		t.Fatalf("map specials summary: %v", err)
	}
	if len(seasons) != 1 || seasons[0].SeasonNumber != 0 || seasons[0].Name != "Specials" || seasons[0].EpisodeCount != 1 {
		t.Fatalf("unexpected specials summary: %+v", seasons)
	}
	season, err := client.SeriesSeason(context.Background(), "81797", "1000")
	if err != nil {
		t.Fatalf("map specials season: %v", err)
	}
	if season.SeasonNumber != 0 || season.Name != "Specials" || len(season.Episodes) != 1 ||
		season.Episodes[0].SeasonNumber != 0 || season.Episodes[0].ExternalID != "10001" {
		t.Fatalf("unexpected specials season: %+v", season)
	}
}

func TestSeriesSeasonBoundsOrderPagination(t *testing.T) {
	var pageRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]string{"token": "token"}})
		case "/series/1/extended":
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{
				"id": 1,
				"seasons": []map[string]any{{
					"id": 2, "number": 1, "seriesId": 1, "type": map[string]any{"id": 3, "type": "absolute"},
				}},
			}})
		case "/seasons/2/extended":
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{
				"id": 2, "number": 1, "seriesId": 1, "type": map[string]any{"id": 3, "type": "absolute"},
			}})
		case "/series/1/episodes/absolute":
			pageRequests.Add(1)
			writeJSON(t, w, map[string]any{
				"status": "success",
				"data": map[string]any{"series": map[string]any{"id": 1}, "episodes": []any{}},
				"links": map[string]any{"next": "http://127.0.0.1:1/private"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newWithBaseURL("api-key", "", server.URL, server.Client())
	_, err := client.SeriesSeason(context.Background(), "1", "2")
	if !errors.Is(err, metadata.ErrProviderFailure) {
		t.Fatalf("unbounded pagination error = %v, want provider failure", err)
	}
	if got := pageRequests.Load(); got != 100 {
		t.Fatalf("order page requests = %d, want 100 local requests", got)
	}
}

func TestSeasonPosterRejectsMislabeledLandscapeArtwork(t *testing.T) {
	const (
		bannerURL = "https://artworks.thetvdb.com/banners/v4/season/2121416/banners/68ce9cbc63f0d.jpg"
		posterURL = "https://artworks.thetvdb.com/banners/v4/season/2121416/posters/694fb9f886147.jpg"
	)
	season := seasonExtendedRecord{
		seasonRecord: seasonRecord{Image: bannerURL, ImageType: 7},
		Artwork: []artworkRecord{
			{ID: 64477755, Image: bannerURL, Type: 6, Width: 758, Height: 140, Score: 100},
			{ID: 64582983, Image: posterURL, Type: 7, Width: 680, Height: 1000, Score: 10},
			{ID: 1, Image: "https://artworks.thetvdb.com/other-portrait.jpg", Type: 8, Width: 1000, Height: 1500, Score: 100},
		},
	}

	if got := seasonPosterURL(season); got != posterURL {
		t.Fatalf("season poster = %q, want %q", got, posterURL)
	}

	season.Artwork = season.Artwork[:1]
	if got := seasonPosterURL(season); got != "" {
		t.Fatalf("landscape-only season poster = %q, want no poster", got)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
