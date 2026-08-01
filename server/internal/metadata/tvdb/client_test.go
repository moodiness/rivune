package tvdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/moodiness/rivune/server/internal/metadata"
)

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
		case "/series/81189/extended":
			if r.Header.Get("Authorization") != "Bearer tvdb-token" {
				t.Fatalf("unexpected authorization %q", r.Header.Get("Authorization"))
			}
			writeJSON(t, w, map[string]any{
				"status": "success",
				"data": map[string]any{
					"id": 81189, "name": "Breaking Bad", "overview": "TVDB overview", "image": "https://artworks.thetvdb.com/poster.jpg",
					"defaultSeasonType": 1,
					"aliases":           []map[string]any{{"language": "eng", "name": "Breaking Bad"}, {"language": "eng", "name": "Breaking Bad"}, {"language": "spa", "name": "Breaking Bad: Química"}},
					"seasonTypes":       []map[string]any{{"id": 1, "name": "Aired Order", "alternateName": "Official", "type": "official"}, {"id": 2, "name": "DVD Order", "type": "dvd"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newWithBaseURL("api-key", "", server.URL, server.Client())
	input := metadata.ProviderSeries{AdditionalIDs: map[string]string{"tvdb": "81189"}}
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
	if first.Name != "Breaking Bad" || first.Overview != "TVDB overview" || first.PosterURL == "" {
		t.Fatalf("unexpected enriched series: %+v", first)
	}
	if len(first.Aliases) != 2 || len(first.EpisodeOrders) != 2 || !first.EpisodeOrders[0].IsDefault || first.EpisodeOrders[0].Name != "Official" {
		t.Fatalf("unexpected TVDB provider metadata: aliases=%+v orders=%+v", first.Aliases, first.EpisodeOrders)
	}
}

func TestEnrichSeasonMatchesOfficialEpisodesByNumberAndAirDate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]string{"token": "token"}})
		case "/series/81189/episodes/official":
			if r.URL.Query().Get("page") != "0" || r.URL.Query().Get("season") != "1" {
				t.Fatalf("unexpected query %q", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{
				"status": "success",
				"data": map[string]any{"episodes": []map[string]any{
					{"id": 62085, "name": "Pilot", "overview": "TVDB pilot", "aired": "2008-01-20", "image": "https://artworks.thetvdb.com/still.jpg", "runtime": 59, "seasonNumber": 1, "number": 1},
					{"id": 62086, "name": "Cat's in the Bag...", "aired": "2008-01-27", "seasonNumber": 1, "number": 2},
					{"id": 999, "name": "Other season", "seasonNumber": 2, "number": 1},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newWithBaseURL("api-key", "", server.URL, server.Client())
	season, err := client.EnrichSeason(context.Background(), "81189", metadata.ProviderSeason{
		SeasonNumber: 1,
		Episodes: []metadata.ProviderEpisode{
			{ExternalID: "tmdb-episode-1", SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2008-01-20"},
			{ExternalID: "tmdb-episode-2", Name: "Different hierarchy", SeasonNumber: 1, EpisodeNumber: 2, AirDate: "2025-09-01"},
		},
	})
	if err != nil {
		t.Fatalf("enrich season: %v", err)
	}
	episode := season.Episodes[0]
	if episode.AdditionalIDs["tvdb"] != "62085" || episode.Name != "Pilot" || episode.RuntimeMinutes != 59 || episode.StillURL == "" {
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

func TestSeriesMappingUsesOfficialTVDBSeasonHierarchy(t *testing.T) {
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
					},
					"seasons": []map[string]any{
						{"id": 1001, "name": "Season 1", "number": 1, "seriesId": 81797, "image": "https://artworks.thetvdb.com/s1.jpg", "type": map[string]any{"id": 1, "type": "official"}},
						{"id": 1002, "name": "Season 2", "number": 2, "seriesId": 81797, "image": "https://artworks.thetvdb.com/s2.jpg", "type": map[string]any{"id": 1, "type": "official"}},
						{"id": 2001, "name": "DVD Season", "number": 1, "seriesId": 81797, "type": map[string]any{"id": 2, "type": "dvd"}},
					},
				},
			})
		case "/series/81797/episodes/official":
			season := r.URL.Query().Get("season")
			if r.URL.Query().Get("page") != "0" {
				t.Fatalf("unexpected page query %q", r.URL.RawQuery)
			}
			episodes := []map[string]any{}
			if season == "1" {
				episodes = []map[string]any{
					{"id": 1101, "name": "Episode 1", "aired": "2024-01-07", "seasonNumber": 1, "number": 1},
					{"id": 1102, "name": "Episode 2", "aired": "2024-01-14", "seasonNumber": 1, "number": 2},
				}
			} else if season == "2" {
				episodes = []map[string]any{
					{"id": 1201, "name": "Episode 1", "overview": "Second season", "aired": "2025-01-05", "runtime": 24, "seasonNumber": 2, "number": 1},
				}
			} else {
				t.Fatalf("unexpected season query %q", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{"episodes": episodes}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newWithBaseURL("api-key", "", server.URL, server.Client())
	seasons, err := client.SeriesSeasons(context.Background(), "81797")
	if err != nil {
		t.Fatalf("map series seasons: %v", err)
	}
	if len(seasons) != 2 || seasons[0].ExternalID != "1001" || seasons[0].EpisodeCount != 2 || seasons[1].ExternalID != "1002" || seasons[1].EpisodeCount != 1 {
		t.Fatalf("unexpected official seasons: %+v", seasons)
	}
	season, err := client.SeriesSeason(context.Background(), "81797", "1002")
	if err != nil {
		t.Fatalf("map second season: %v", err)
	}
	if season.SeasonNumber != 2 || len(season.Episodes) != 1 || season.Episodes[0].ExternalID != "1201" || season.Episodes[0].EpisodeNumber != 1 {
		t.Fatalf("unexpected mapped second season: %+v", season)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
