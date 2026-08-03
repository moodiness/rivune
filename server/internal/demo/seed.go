package demo

import (
	"strings"
	"time"
)

type mediaSeed struct {
	TitleID, ResourceID, MediaType, Title, Description, ReleaseInfo string
	Poster, Backdrop, SourceName, Country, Language, Category       string
	CurrentProgram                                                  map[string]any
}

func freshState(now time.Time) sessionState {
	return sessionState{
		activeProfileID: AlexProfileID,
		profiles: map[string]*profileState{
			AlexProfileID: freshProfileState(now, false),
			KidsProfileID: freshProfileState(now, true),
		},
		playback: make(map[string]playbackState),
	}
}

func freshProfileState(now time.Time, kids bool) *profileState {
	library := map[string]bool{SignalMovieID: true, OrbitSeriesID: true, WorldNewsID: true}
	progress := map[string]progressState{
		SignalMovieID: {PositionSeconds: 842, DurationSeconds: 1440, Version: 3, UpdatedAt: now.Add(-18 * time.Minute)},
		OrbitEpisodeOne: {PositionSeconds: 1920, DurationSeconds: 1920, Completed: true, Version: 2, UpdatedAt: now.Add(-26 * time.Hour)},
		OrbitEpisodeTwo: {PositionSeconds: 611, DurationSeconds: 1860, Version: 4, UpdatedAt: now.Add(-3 * time.Hour)},
	}
	if kids {
		library = map[string]bool{OpenSkiesID: true, OrbitSeriesID: true, CultureLiveID: true}
		progress = map[string]progressState{
			OpenSkiesID: {PositionSeconds: 244, DurationSeconds: 1320, Version: 1, UpdatedAt: now.Add(-45 * time.Minute)},
			OrbitEpisodeOne: {PositionSeconds: 1920, DurationSeconds: 1920, Completed: true, Version: 1, UpdatedAt: now.Add(-48 * time.Hour)},
		}
	}
	return &profileState{library: library, progress: progress, dismissed: make(map[string]bool)}
}

func profileRecords() []map[string]any {
	return []map[string]any{
		profileRecord(AlexProfileID, "Alex", false, true, "avatar-alex.svg"),
		profileRecord(KidsProfileID, "Kids", true, false, "avatar-kids.svg"),
	}
}

func profileRecord(id, name string, child, manage bool, avatar string) map[string]any {
	return map[string]any{
		"id": id, "name": name, "isChild": child, "hasPin": false,
		"canManage": manage, "enabled": true, "availableFrom": nil, "availableUntil": nil,
		"accessStartTime": nil, "accessEndTime": nil, "accessTimezone": "UTC", "accessible": true,
		"avatar": map[string]any{"kind": "preset", "presetId": strings.TrimSuffix(avatar, ".svg"), "url": asset(avatar)},
	}
}

func mediaCatalog() []mediaSeed {
	return []mediaSeed{
		{SignalMovieID, "demo-signal-horizon", "movie", "Signal Horizon", "A rescue pilot follows an impossible signal beyond the mapped atmosphere.", "2026", asset("poster-signal.svg"), asset("backdrop-space.svg"), "Rivune Demo Cinema", "US", "en", "Science Fiction", nil},
		{LighthouseID, "demo-last-lighthouse", "movie", "The Last Lighthouse", "A keeper protects the final beacon during a week-long electric storm.", "2025", asset("poster-lighthouse.svg"), asset("backdrop-coast.svg"), "Rivune Demo Cinema", "GB", "en", "Drama", nil},
		{OpenSkiesID, "demo-open-skies", "movie", "Open Skies", "Two young inventors launch a hand-built glider across a floating archipelago.", "2024", asset("poster-skies.svg"), asset("backdrop-skies.svg"), "Rivune Demo Family", "CA", "en", "Family", nil},
		{OrbitSeriesID, "demo-orbit-station", "series", "Orbit Station", "The rotating crew of an orbital laboratory uncovers a message hidden in its telemetry.", "2025–", asset("poster-orbit.svg"), asset("backdrop-space.svg"), "Rivune Demo Series", "US", "en", "Science Fiction", nil},
		{WorldNewsID, "demo-world-news", "tv", "World News", "Fictional rolling headlines from around the demonstration world.", "Live", asset("channel-news.svg"), asset("backdrop-news.svg"), "Rivune Demo Live", "INT", "en", "News", map[string]any{"title": "World Briefing", "description": "Synthetic demonstration headlines", "start": "2026-08-03T18:00:00Z", "end": "2026-08-03T19:00:00Z"}},
		{CultureLiveID, "demo-culture-live", "tv", "Culture Live", "Performances, exhibitions, and conversations from fictional venues.", "Live", asset("channel-culture.svg"), asset("backdrop-culture.svg"), "Rivune Demo Live", "FR", "fr", "Culture", map[string]any{"title": "Studio Evening", "description": "Synthetic demonstration programme", "start": "2026-08-03T18:00:00Z", "end": "2026-08-03T20:00:00Z"}},
	}
}

func asset(name string) string {
	if strings.HasSuffix(name, ".svg") {
		name = "artwork.svg"
	}
	return APIPrefix + "/demo/assets/" + name
}

func findMedia(id string) (mediaSeed, bool) {
	for _, item := range mediaCatalog() {
		if item.TitleID == id || item.ResourceID == id { return item, true }
	}
	return mediaSeed{}, false
}

func mediaItem(item mediaSeed) map[string]any {
	value := map[string]any{
		"id": item.ResourceID, "titleId": item.TitleID, "mediaType": item.MediaType, "title": item.Title,
		"description": item.Description, "releaseInfo": item.ReleaseInfo, "posterUrl": item.Poster,
		"backgroundUrl": item.Backdrop, "resourceId": item.ResourceID, "sourceName": item.SourceName,
		"sources": []map[string]any{{"id": "demo-addon", "kind": "addon_catalog", "title": item.SourceName, "addonId": "demo-addon"}},
		"externalIds": map[string]string{"demo": item.ResourceID},
	}
	if item.MediaType == "tv" {
		value["logoUrl"], value["sourceAddonId"], value["sourceCatalogId"] = item.Poster, "demo-addon", "demo-live"
		value["country"], value["language"], value["category"] = item.Country, item.Language, item.Category
		value["available"], value["currentProgram"] = true, item.CurrentProgram
	}
	return value
}

func libraryItem(item mediaSeed, now time.Time) map[string]any {
	return map[string]any{
		"titleId": item.TitleID, "mediaType": item.MediaType, "provider": "demo", "externalId": item.ResourceID,
		"resourceId": item.ResourceID, "title": item.Title, "posterUrl": item.Poster, "backgroundUrl": item.Backdrop,
		"releaseInfo": item.ReleaseInfo, "sourceAddonId": "demo-addon", "sourceCatalogId": "demo-library",
		"sourceName": item.SourceName, "country": item.Country, "language": item.Language, "category": item.Category,
		"available": true, "currentProgram": item.CurrentProgram, "addedAt": now.Add(-7 * 24 * time.Hour), "updatedAt": now,
	}
}

func titleReference(item mediaSeed) map[string]any {
	return map[string]any{
		"titleId": item.TitleID, "mediaType": item.MediaType, "provider": "demo", "externalId": item.ResourceID,
		"resourceId": item.ResourceID, "title": item.Title, "posterUrl": item.Poster, "backgroundUrl": item.Backdrop,
		"releaseInfo": item.ReleaseInfo, "sourceAddonId": "demo-addon", "sourceCatalogId": "demo-catalog",
		"sourceName": item.SourceName, "country": item.Country, "language": item.Language, "category": item.Category, "available": true,
	}
}

func addonMeta(item mediaSeed) map[string]any {
	return map[string]any{
		"id": item.ResourceID, "resourceId": item.ResourceID, "type": item.MediaType, "name": item.Title,
		"description": item.Description, "poster": item.Poster, "background": item.Backdrop, "logo": item.Poster,
		"releaseInfo": item.ReleaseInfo, "sourceAddonId": "demo-addon", "sourceCatalogId": "demo-catalog",
		"sourceName": item.SourceName, "country": item.Country, "language": item.Language,
		"category": item.Category, "available": true, "currentProgram": item.CurrentProgram,
	}
}

func movieMetadata(id string) (map[string]any, bool) {
	item, ok := findMedia(id)
	if !ok || item.MediaType != "movie" { return nil, false }
	return map[string]any{
		"id": item.TitleID, "mediaType": "movie", "title": item.Title, "originalTitle": item.Title,
		"originalLanguage": item.Language, "overview": item.Description, "releaseDate": "2026-03-14",
		"posterUrl": item.Poster, "backdropUrl": item.Backdrop, "logoUrl": asset("logo-demo.svg"),
		"tagline": "Synthetic demonstration content", "runtimeMinutes": 24,
		"genres": []map[string]any{{"id": 878, "name": item.Category}},
		"cast": []map[string]any{{"id": "demo-cast-1", "name": "Mara Vale", "character": "Aster"}, {"id": "demo-cast-2", "name": "Jon Bell", "character": "Rowan"}},
		"voteAverage": 8.1, "voteCount": 428, "externalIds": map[string]string{"demo": item.ResourceID},
	}, true
}

func seriesMetadata(id string) (map[string]any, bool) {
	if id != OrbitSeriesID && id != "demo-orbit-station" { return nil, false }
	seasonOne, _ := seasonMetadata(OrbitSeasonOneID)
	seasonTwo, _ := seasonMetadata(OrbitSeasonTwoID)
	delete(seasonOne, "episodes"); delete(seasonTwo, "episodes")
	return map[string]any{
		"id": OrbitSeriesID, "mediaType": "series", "name": "Orbit Station", "originalName": "Orbit Station",
		"originalLanguage": "en", "overview": "The rotating crew of an orbital laboratory uncovers a message hidden in its telemetry.",
		"firstAirDate": "2025-02-11", "lastAirDate": "2026-04-22", "posterUrl": asset("poster-orbit.svg"),
		"backdropUrl": asset("backdrop-space.svg"), "logoUrl": asset("logo-demo.svg"), "tagline": "Every orbit leaves a trace.",
		"status": "Returning Series", "numberOfSeasons": 2, "numberOfEpisodes": 4,
		"genres": []map[string]any{{"id": 878, "name": "Science Fiction"}, {"id": 18, "name": "Drama"}},
		"cast": []map[string]any{{"id": "demo-cast-3", "name": "Avery Stone", "character": "Commander Ilya Voss"}, {"id": "demo-cast-4", "name": "Mina Park", "character": "Dr. Sera Vale"}},
		"voteAverage": 8.6, "voteCount": 812, "seasons": []map[string]any{seasonOne, seasonTwo},
		"episodeOrders": []map[string]any{{"id": "demo-aired", "name": "Aired Order", "type": "official", "isDefault": true}},
		"selectedEpisodeOrderId": "demo-aired", "mappingProvider": "tmdb", "externalIds": map[string]string{"demo": "demo-orbit-station"},
	}, true
}

func seasonMetadata(id string) (map[string]any, bool) {
	var seasonID, name, overview, airDate, poster string
	var number int
	var episodes []map[string]any
	switch id {
	case OrbitSeasonOneID, "demo-orbit-station-season-1":
		seasonID, name, overview, airDate, poster, number = OrbitSeasonOneID, "Season 1", "The station receives a signal from an empty orbit.", "2025-02-11", asset("poster-orbit-s1.svg"), 1
		episodes = []map[string]any{episode(OrbitEpisodeOne, seasonID, "Arrival Window", "A replacement crew arrives during an unexplained communications blackout.", 1, 1), episode(OrbitEpisodeTwo, seasonID, "Quiet Frequency", "Sera isolates a repeating pattern inside the station telemetry.", 1, 2)}
	case OrbitSeasonTwoID, "demo-orbit-station-season-2":
		seasonID, name, overview, airDate, poster, number = OrbitSeasonTwoID, "Season 2", "A second crew follows the message beyond the station.", "2026-04-15", asset("poster-orbit-s2.svg"), 2
		episodes = []map[string]any{episode(OrbitEpisodeThree, seasonID, "Shadow Transit", "The station passes through a region missing from every chart.", 2, 1), episode(OrbitEpisodeFour, seasonID, "Home Vector", "The crew chooses what the signal should become.", 2, 2)}
	default: return nil, false
	}
	return map[string]any{
		"id": seasonID, "mediaType": "season", "seriesId": OrbitSeriesID, "name": name, "overview": overview,
		"seasonNumber": number, "episodeCount": len(episodes), "airDate": airDate, "posterUrl": poster,
		"voteAverage": 8.5, "externalIds": map[string]string{"demo": "demo-orbit-station-season"}, "episodes": episodes,
	}, true
}

func episode(id, seasonID, name, overview string, season, number int) map[string]any {
	return map[string]any{
		"id": id, "mediaType": "episode", "seasonId": seasonID, "name": name, "overview": overview,
		"seasonNumber": season, "episodeNumber": number, "airDate": "2026-04-15", "stillUrl": asset("backdrop-space.svg"),
		"runtimeMinutes": 31, "voteAverage": 8.4, "voteCount": 176, "externalIds": map[string]string{"demo": "demo-orbit-episode"},
	}
}
