package demo

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/moodiness/rivune/server/internal/playback"
)

func (s *Service) dispatchApplication(w http.ResponseWriter, r *http.Request, current *session) bool {
	current.mu.Lock()
	defer current.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, APIPrefix)

	switch {
	case p == "/profiles" && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{"profiles": profileRecords()})
		return true
	case p == "/profiles/selection" && r.Method == http.MethodDelete:
		current.state.activeProfileID = ""
		w.WriteHeader(204)
		return true
	case strings.HasPrefix(p, "/profiles/") && strings.HasSuffix(p, "/select") && r.Method == http.MethodPost:
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/profiles/"), "/select")
		var input struct {
			PIN *string `json:"pin,omitempty"`
		}
		if err := decodeStrict(r, &input); err != nil {
			writeError(w, 400, "invalid_request", err.Error())
			return true
		}
		profile, ok := profileByID(id)
		if !ok {
			writeError(w, 404, "profile_not_found", "The profile does not exist")
			return true
		}
		current.state.activeProfileID = id
		writeJSON(w, 200, map[string]any{"profile": profile, "expiresAt": current.expiresAt})
		return true
	case strings.HasPrefix(p, "/profiles/") && strings.HasSuffix(p, "/settings/effective") && r.Method == http.MethodGet:
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/profiles/"), "/settings/effective")
		if _, ok := profileByID(id); !ok {
			writeError(w, 404, "profile_not_found", "The profile does not exist")
			return true
		}
		writeJSON(w, 200, effectiveSettings())
		return true
	case p == "/collections" && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{"collections": []any{collectionRecord(current.state.activeProfileID)}})
		return true
	case strings.HasPrefix(p, "/collections/") && strings.Contains(p, "/folders/") && strings.HasSuffix(p, "/items") && r.Method == http.MethodGet:
		return serveFolder(w, r, p, current.state.activeProfileID)
	case p == "/continue-watching" && r.Method == http.MethodGet:
		serveContinue(w, current)
		return true
	case strings.HasPrefix(p, "/continue-watching/") && r.Method == http.MethodDelete:
		state, ok := activeState(current)
		if !ok {
			profileRequired(w)
			return true
		}
		id := strings.TrimPrefix(p, "/continue-watching/")
		if !knownTitle(id) {
			notFound(w)
			return true
		}
		state.dismissed[id] = true
		w.WriteHeader(204)
		return true
	case p == "/library" && r.Method == http.MethodGet:
		serveLibrary(w, r, current)
		return true
	case strings.HasPrefix(p, "/library/") && (r.Method == http.MethodPut || r.Method == http.MethodDelete):
		serveLibraryMutation(w, r, current, strings.TrimPrefix(p, "/library/"))
		return true
	case p == "/titles/resolve" && r.Method == http.MethodPost:
		serveResolveTitle(w, r)
		return true
	case strings.HasPrefix(p, "/progress/"):
		return serveProgress(w, r, current, strings.TrimPrefix(p, "/progress/"))
	case strings.HasPrefix(p, "/titles/") && strings.HasSuffix(p, "/watched"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/titles/"), "/watched")
		return serveWatched(w, r, current, id)
	case strings.HasPrefix(p, "/addons/catalogs/search/") && r.Method == http.MethodGet:
		serveSearch(w, r, strings.TrimPrefix(p, "/addons/catalogs/search/"))
		return true
	case strings.HasPrefix(p, "/addons/resources/meta/") && r.Method == http.MethodGet:
		serveResourceMeta(w, strings.TrimPrefix(p, "/addons/resources/meta/"))
		return true
	case strings.HasPrefix(p, "/metadata/titles/") && strings.HasSuffix(p, "/trailers") && r.Method == http.MethodGet:
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/metadata/titles/"), "/trailers")
		if !knownTitle(id) {
			notFound(w)
			return true
		}
		writeJSON(w, 200, map[string]any{"trailers": []any{}})
		return true
	case strings.HasPrefix(p, "/metadata/series/") && strings.HasSuffix(p, "/trailers") && r.Method == http.MethodGet:
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/metadata/series/"), "/trailers")
		if _, ok := seriesMetadata(id); !ok {
			notFound(w)
			return true
		}
		writeJSON(w, 200, map[string]any{"trailers": []any{}})
		return true
	case strings.HasPrefix(p, "/metadata/seasons/") && strings.HasSuffix(p, "/trailers") && r.Method == http.MethodGet:
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/metadata/seasons/"), "/trailers")
		if _, ok := seasonMetadata(id); !ok {
			notFound(w)
			return true
		}
		writeJSON(w, 200, map[string]any{"trailers": []any{}})
		return true
	case strings.HasPrefix(p, "/metadata/titles/") && r.Method == http.MethodGet:
		value, ok := movieMetadata(strings.TrimPrefix(p, "/metadata/titles/"))
		if !ok {
			notFound(w)
			return true
		}
		writeJSON(w, 200, value)
		return true
	case strings.HasPrefix(p, "/metadata/series/") && r.Method == http.MethodGet:
		value, ok := seriesMetadata(strings.TrimPrefix(p, "/metadata/series/"))
		if !ok {
			notFound(w)
			return true
		}
		writeJSON(w, 200, value)
		return true
	case strings.HasPrefix(p, "/metadata/seasons/") && r.Method == http.MethodGet:
		value, ok := seasonMetadata(strings.TrimPrefix(p, "/metadata/seasons/"))
		if !ok {
			notFound(w)
			return true
		}
		writeJSON(w, 200, value)
		return true
	case p == "/playback/sources" && r.Method == http.MethodPost:
		servePlaybackSources(w, r, current.expiresAt)
		return true
	case p == "/playback/prepare" && r.Method == http.MethodPost:
		servePlaybackPrepare(w, r, current.expiresAt)
		return true
	case p == "/playback/resolve" && r.Method == http.MethodPost:
		s.servePlaybackResolve(w, r, current)
		return true
	case p == "/playback/markers" && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{"markers": []map[string]any{{"type": "intro", "startSeconds": 1, "endSeconds": 3, "confidence": 1, "submissionCount": 1}, {"type": "outro", "startSeconds": 10, "endSeconds": 12, "confidence": 1, "submissionCount": 1}}})
		return true
	case strings.HasPrefix(p, "/playback/sessions/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(p, "/playback/sessions/")
		if !s.deletePlaybackLocked(current, id, s.now().UTC()) {
			writeError(w, 404, "playback_session_not_found", "The playback session is invalid or expired")
			return true
		}
		w.WriteHeader(204)
		return true
	case p == "/calendar" && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{"events": []map[string]any{{"id": "d6000000-0000-4000-8000-000000000001", "titleId": OrbitEpisodeFour, "mediaType": "episode", "title": "Home Vector", "releaseDate": "2026-08-07", "posterUrl": asset("poster-orbit.svg"), "resourceId": "demo-orbit-station:2:2", "resourceProvider": "demo", "seriesTitle": "Orbit Station", "seriesId": OrbitSeriesID, "seasonId": OrbitSeasonTwoID, "seasonNumber": 2, "episodeNumber": 2}}})
		return true
	}
	return false
}

func profileByID(id string) (map[string]any, bool) {
	for _, p := range profileRecords() {
		if p["id"] == id {
			return p, true
		}
	}
	return nil, false
}
func activeState(current *session) (*profileState, bool) {
	if current.state.activeProfileID == "" {
		return nil, false
	}
	state := current.state.profiles[current.state.activeProfileID]
	return state, state != nil
}
func profileRequired(w http.ResponseWriter) {
	writeError(w, 409, "profile_selection_required", "Select an active profile before accessing demo content")
}
func notFound(w http.ResponseWriter) {
	writeError(w, 404, "demo_title_not_found", "The demo title does not exist")
}

func effectiveSettings() map[string]any {
	settings := map[string]any{
		"interfaceLanguage": "en", "theme": "system", "maximumResolution": "720p", "preferDirectPlay": true,
		"hideUnreleased": false, "metadataLanguage": "en-US", "metadataRegion": "US", "seriesMappingProvider": "tmdb",
		"audioLanguage": "en", "subtitleLanguage": "en", "forcedSubtitleLanguage": "en", "autoplayNextEpisode": true,
		"skipIntroEnabled": true, "skipRecapEnabled": true, "skipOutroEnabled": true, "cardDensity": "comfortable",
		"animationsEnabled": true, "subtitleSizePercent": 100, "subtitleTextColor": "#ffffff", "subtitleBackgroundOpacityPercent": 50,
		"notificationsEnabled": false, "notificationDurationSeconds": 5, "notificationPollIntervalSeconds": 30,
	}
	sources := make(map[string]string, len(settings))
	for key := range settings {
		sources[key] = "default"
	}
	return map[string]any{"schemaVersion": 1, "settings": settings, "sources": sources}
}

func collectionRecord(profileID string) map[string]any {
	return map[string]any{
		"id": HomeCollectionID, "title": "Demo Home", "heroEnabled": true, "backdropImageUrl": asset("backdrop-space.svg"),
		"pinToTop": true, "focusGlowEnabled": true, "viewMode": "follow_layout", "folderCoverShape": "poster",
		"folders": []map[string]any{
			{"id": SpotlightFolderID, "title": "Featured", "tileShape": "poster", "sourceView": "merged", "coverImageUrl": asset("poster-signal.svg"), "focusGifEnabled": false, "hideTitle": false, "sources": []any{}},
			{"id": SeriesFolderID, "title": "Stories in orbit", "tileShape": "landscape", "sourceView": "merged", "coverImageUrl": asset("poster-orbit.svg"), "focusGifEnabled": false, "hideTitle": false, "sources": []any{}},
			{"id": LiveFolderID, "title": "Live now", "tileShape": "landscape", "sourceView": "merged", "coverImageUrl": asset("channel-news.svg"), "focusGifEnabled": false, "hideTitle": false, "sources": []any{}},
		},
		"profileIds": []string{profileID}, "position": 0, "version": 1, "createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z",
	}
}

func serveFolder(w http.ResponseWriter, r *http.Request, p, profileID string) bool {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) != 5 || parts[0] != "collections" || parts[2] != "folders" || parts[4] != "items" {
		return false
	}
	if parts[1] != HomeCollectionID {
		writeError(w, 404, "collection_not_found", "The demo collection does not exist")
		return true
	}
	folderID := parts[3]
	collection := collectionRecord(profileID)
	folders := collection["folders"].([]map[string]any)
	var folder map[string]any
	for _, candidate := range folders {
		if candidate["id"] == folderID {
			folder = candidate
			break
		}
	}
	if folder == nil {
		writeError(w, 404, "collection_folder_not_found", "The demo collection folder does not exist")
		return true
	}
	items := []map[string]any{}
	for _, item := range mediaCatalog() {
		if folderID == SeriesFolderID && item.MediaType != "series" {
			continue
		}
		if folderID == LiveFolderID && item.MediaType != "tv" {
			continue
		}
		if folderID == SpotlightFolderID && item.MediaType == "tv" {
			continue
		}
		items = append(items, mediaItem(item))
	}
	page, pageSize, offset, ok := pagination(r, 1, 24)
	if !ok {
		writeError(w, 400, "invalid_request", "Pagination values must be positive integers")
		return true
	}
	start, end := paginationWindow(offset, pageSize, len(items))
	writeJSON(w, 200, map[string]any{"collectionId": HomeCollectionID, "folder": folder, "items": items[start:end], "page": page, "hasMore": end < len(items), "errors": []any{}})
	return true
}

func serveContinue(w http.ResponseWriter, current *session) {
	state, ok := activeState(current)
	if !ok {
		profileRequired(w)
		return
	}
	items := []map[string]any{}
	for id, progress := range state.progress {
		if progress.Completed || state.dismissed[id] {
			continue
		}
		entry := map[string]any{"titleId": id, "mediaType": "movie", "positionSeconds": progress.PositionSeconds, "durationSeconds": progress.DurationSeconds, "version": progress.Version, "reason": "resume", "lastWatchedAt": progress.UpdatedAt}
		if item, found := findMedia(id); found {
			entry["title"], entry["posterUrl"], entry["backgroundUrl"], entry["resourceId"], entry["resourceProvider"] = item.Title, item.Poster, item.Backdrop, item.ResourceID, "demo"
		} else if id == OrbitEpisodeTwo {
			entry["mediaType"], entry["seriesId"], entry["seasonId"], entry["seasonNumber"], entry["episodeNumber"] = "episode", OrbitSeriesID, OrbitSeasonOneID, 1, 2
			entry["title"], entry["posterUrl"], entry["backgroundUrl"], entry["resourceId"], entry["resourceProvider"] = "Orbit Station — Quiet Frequency", asset("poster-orbit.svg"), asset("backdrop-space.svg"), "demo-orbit-station:1:2", "demo"
		}
		items = append(items, entry)
	}
	sort.Slice(items, func(i, j int) bool {
		return fmt.Sprint(items[i]["lastWatchedAt"]) > fmt.Sprint(items[j]["lastWatchedAt"])
	})
	writeJSON(w, 200, map[string]any{"items": items})
}

func serveLibrary(w http.ResponseWriter, r *http.Request, current *session) {
	state, ok := activeState(current)
	if !ok {
		profileRequired(w)
		return
	}
	mediaType := r.URL.Query().Get("mediaType")
	if mediaType != "" && mediaType != "movie" && mediaType != "series" && mediaType != "tv" {
		writeError(w, 400, "invalid_request", "mediaType is invalid")
		return
	}
	items := []map[string]any{}
	now := current.createdAt
	for _, item := range mediaCatalog() {
		if state.library[item.TitleID] && (mediaType == "" || item.MediaType == mediaType) {
			items = append(items, libraryItem(item, now))
		}
	}
	page, pageSize, offset, valid := pagination(r, 1, 100)
	if !valid {
		writeError(w, 400, "invalid_request", "Pagination values must be positive integers")
		return
	}
	total := len(items)
	start, end := paginationWindow(offset, pageSize, total)
	pages := 0
	if total > 0 {
		pages = 1 + (total-1)/pageSize
	}
	writeJSON(w, 200, map[string]any{"items": items[start:end], "page": page, "totalPages": pages, "totalResults": total})
}

func serveLibraryMutation(w http.ResponseWriter, r *http.Request, current *session, id string) {
	state, ok := activeState(current)
	if !ok {
		profileRequired(w)
		return
	}
	item, found := findMedia(id)
	if !found {
		notFound(w)
		return
	}
	if r.Method == http.MethodDelete {
		delete(state.library, item.TitleID)
		w.WriteHeader(204)
		return
	}
	state.library[item.TitleID] = true
	writeJSON(w, 200, libraryItem(item, current.createdAt))
}

func serveResolveTitle(w http.ResponseWriter, r *http.Request) {
	var input struct {
		MediaType       string `json:"mediaType"`
		Provider        string `json:"provider"`
		ExternalID      string `json:"externalId"`
		ResourceID      string `json:"resourceId"`
		Title           string `json:"title"`
		PosterURL       string `json:"posterUrl"`
		BackgroundURL   string `json:"backgroundUrl"`
		ReleaseInfo     string `json:"releaseInfo"`
		Released        string `json:"released"`
		SourceAddonID   string `json:"sourceAddonId"`
		SourceCatalogID string `json:"sourceCatalogId"`
		SourceName      string `json:"sourceName"`
		Country         string `json:"country"`
		Language        string `json:"language"`
		Category        string `json:"category"`
	}
	if err := decodeStrict(r, &input); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	lookup := input.ResourceID
	if lookup == "" {
		lookup = input.ExternalID
	}
	item, ok := findMedia(lookup)
	if !ok {
		for _, candidate := range mediaCatalog() {
			if strings.EqualFold(candidate.Title, input.Title) {
				item, ok = candidate, true
				break
			}
		}
	}
	if !ok || (input.MediaType != "" && input.MediaType != item.MediaType) {
		notFound(w)
		return
	}
	writeJSON(w, 200, titleReference(item))
}

func serveProgress(w http.ResponseWriter, r *http.Request, current *session, id string) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodPut && r.Method != http.MethodDelete {
		return false
	}
	state, ok := activeState(current)
	if !ok {
		profileRequired(w)
		return true
	}
	if !knownTitle(id) {
		notFound(w)
		return true
	}
	progress, exists := state.progress[id]
	if r.Method == http.MethodGet {
		if !exists {
			w.WriteHeader(204)
		} else {
			writeProgress(w, id, progress)
		}
		return true
	}
	if r.Method == http.MethodDelete {
		expected, valid := queryInt64(r, "expectedVersion")
		if !valid {
			writeError(w, 400, "invalid_request", "expectedVersion must be an integer")
			return true
		}
		if exists && expected != 0 && expected != progress.Version {
			conflict(w)
			return true
		}
		delete(state.progress, id)
		w.WriteHeader(204)
		return true
	}
	var input struct {
		PositionSeconds int   `json:"positionSeconds"`
		DurationSeconds int   `json:"durationSeconds"`
		Completed       bool  `json:"completed"`
		ExpectedVersion int64 `json:"expectedVersion"`
	}
	if err := decodeStrict(r, &input); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return true
	}
	if input.PositionSeconds < 0 || input.DurationSeconds <= 0 || input.PositionSeconds > input.DurationSeconds {
		writeError(w, 422, "invalid_watch_state", "Playback progress is invalid")
		return true
	}
	if exists && input.ExpectedVersion != 0 && input.ExpectedVersion != progress.Version {
		conflict(w)
		return true
	}
	progress = progressState{PositionSeconds: input.PositionSeconds, DurationSeconds: input.DurationSeconds, Completed: input.Completed, Version: progress.Version + 1, UpdatedAt: current.createdAt}
	state.progress[id] = progress
	delete(state.dismissed, id)
	writeProgress(w, id, progress)
	return true
}

func serveWatched(w http.ResponseWriter, r *http.Request, current *session, id string) bool {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		return false
	}
	state, ok := activeState(current)
	if !ok {
		profileRequired(w)
		return true
	}
	if !knownTitle(id) {
		notFound(w)
		return true
	}
	progress := state.progress[id]
	var expected int64
	if r.Method == http.MethodPost {
		var input struct {
			ExpectedVersion int64 `json:"expectedVersion"`
		}
		if err := decodeStrict(r, &input); err != nil {
			writeError(w, 400, "invalid_request", err.Error())
			return true
		}
		expected = input.ExpectedVersion
	} else {
		var valid bool
		expected, valid = queryInt64(r, "expectedVersion")
		if !valid {
			writeError(w, 400, "invalid_request", "expectedVersion must be an integer")
			return true
		}
	}
	if progress.Version != 0 && expected != 0 && expected != progress.Version {
		conflict(w)
		return true
	}
	if progress.DurationSeconds == 0 {
		progress.DurationSeconds = 1440
	}
	progress.Version++
	progress.Completed = r.Method == http.MethodPost
	if progress.Completed {
		progress.PositionSeconds = progress.DurationSeconds
	} else {
		progress.PositionSeconds = 0
	}
	progress.UpdatedAt = current.createdAt
	state.progress[id] = progress
	writeProgress(w, id, progress)
	return true
}
func writeProgress(w http.ResponseWriter, id string, p progressState) {
	writeJSON(w, 200, map[string]any{"titleId": id, "positionSeconds": p.PositionSeconds, "durationSeconds": p.DurationSeconds, "completed": p.Completed, "version": p.Version, "updatedAt": p.UpdatedAt})
}
func conflict(w http.ResponseWriter) {
	writeError(w, 409, "watch_state_conflict", "The watch state changed; reload it before retrying")
}

func serveSearch(w http.ResponseWriter, r *http.Request, mediaType string) {
	if mediaType != "movie" && mediaType != "series" && mediaType != "tv" {
		writeError(w, 404, "demo_catalog_not_found", "The demo catalog does not exist")
		return
	}
	skip, limit, ok := skipLimit(r)
	if !ok {
		writeError(w, 400, "invalid_request", "skip and limit are invalid")
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))
	metas := []map[string]any{}
	for _, item := range mediaCatalog() {
		if item.MediaType == mediaType && (query == "" || strings.Contains(strings.ToLower(item.Title+" "+item.Description), query)) {
			metas = append(metas, addonMeta(item))
		}
	}
	start, end := paginationWindow(skip, limit, len(metas))
	writeJSON(w, 200, resourceBatch("catalog", mediaType, "demo-search", map[string]any{"metas": metas[start:end]}))
}
func serveResourceMeta(w http.ResponseWriter, remainder string) {
	parts := strings.SplitN(remainder, "/", 2)
	if len(parts) != 2 {
		notFound(w)
		return
	}
	item, ok := findMedia(parts[1])
	if !ok && parts[0] == "series" && strings.HasPrefix(parts[1], "demo-orbit-station:") {
		item, ok = findMedia(OrbitSeriesID)
	}
	if !ok || item.MediaType != parts[0] {
		notFound(w)
		return
	}
	writeJSON(w, 200, resourceBatch("meta", parts[0], parts[1], map[string]any{"meta": addonMeta(item)}))
}
func resourceBatch(resource, mediaType, id string, payload map[string]any) map[string]any {
	return map[string]any{"results": []map[string]any{{"addonId": "demo-addon", "manifestId": "demo.synthetic", "resource": resource, "type": mediaType, "id": id, "payload": payload}}, "errors": []any{}}
}

func servePlaybackSources(w http.ResponseWriter, r *http.Request, expiresAt any) {
	var input playback.SourcesInput
	if err := decodeStrict(r, &input); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if !knownResource(input.ResourceID) {
		notFound(w)
		return
	}
	writeJSON(w, 200, map[string]any{
		"sources": []map[string]any{
			{
				"id": "demo-option-720", "sourceRef": "demo-source-720-" + input.ResourceID,
				"stableIdentity": "demo:720:" + input.ResourceID,
				"addonId":        "demo-addon", "manifestId": "demo.synthetic", "streamIndex": 0,
				"name": "Demo 720p", "description": "Synthetic H.264/AAC demonstration stream",
				"filename": "demo-720p.mp4", "protocol": "http", "container": "mp4", "expiresAt": expiresAt,
			},
			{
				"id": "demo-option-360", "sourceRef": "demo-source-360-" + input.ResourceID,
				"stableIdentity": "demo:360:" + input.ResourceID,
				"addonId":        "demo-addon", "manifestId": "demo.synthetic", "streamIndex": 1,
				"name": "Demo 360p", "description": "Synthetic low-bandwidth stream",
				"filename": "demo-360p.mp4", "protocol": "http", "container": "mp4", "expiresAt": expiresAt,
			},
		},
		"providerErrors": []any{},
	})
}
func servePlaybackPrepare(w http.ResponseWriter, r *http.Request, expiresAt any) {
	var input playback.PrepareInput
	if err := decodeStrict(r, &input); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if !validSourceRef(input.SourceRef) {
		writeError(w, 404, "playback_source_not_found", "The demo playback source does not exist")
		return
	}
	height := 720
	if strings.HasPrefix(input.SourceRef, "demo-source-360-") {
		height = 360
	}
	writeJSON(w, 200, map[string]any{"sourceRef": input.SourceRef, "mode": "direct", "protocol": "http", "container": "mp4", "media": mediaInspection(height), "subtitleCount": 2, "expiresAt": expiresAt})
}
func (s *Service) servePlaybackResolve(w http.ResponseWriter, r *http.Request, current *session) {
	var input playback.ResolveInput
	if err := decodeStrict(r, &input); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if !validSourceRef(input.SourceRef) || (input.TitleID != "" && !knownTitle(input.TitleID)) {
		writeError(w, 404, "playback_source_not_found", "The demo playback source does not exist")
		return
	}
	id, allocated := s.allocatePlaybackLocked(current, input.TitleID, s.now().UTC())
	if !allocated {
		writeError(w, http.StatusTooManyRequests, "demo_playback_limit_reached", "The demo playback session limit has been reached")
		return
	}
	selected := "demo-stream-720"
	if strings.HasPrefix(input.SourceRef, "demo-source-360-") {
		selected = "demo-stream-360"
	}
	audio := 0
	if input.PreferredAudioTrack != nil {
		audio = *input.PreferredAudioTrack
	}
	writeJSON(w, 201, map[string]any{"id": id, "selectedSourceId": selected, "selectedAudioTrack": audio, "selectedSubtitleId": input.PreferredSubtitleID, "sources": []map[string]any{resolvedSource(720), resolvedSource(360)}, "subtitles": []map[string]any{{"id": "demo-sub-en", "addonId": "demo-addon", "manifestId": "demo.synthetic", "language": "en", "url": asset("demo.en.vtt"), "default": true}, {"id": "demo-sub-fr", "addonId": "demo-addon", "manifestId": "demo.synthetic", "language": "fr", "url": asset("demo.fr.vtt")}}, "providerErrors": []any{}, "expiresAt": current.expiresAt})
}
func resolvedSource(height int) map[string]any {
	quality := "720"
	if height == 360 {
		quality = "360"
	}
	return map[string]any{"id": "demo-stream-" + quality, "addonId": "demo-addon", "manifestId": "demo.synthetic", "name": "Demo " + quality + "p", "mode": "direct", "url": asset("demo-" + quality + "p.mp4"), "protocol": "http", "container": "mp4", "compatible": true, "media": mediaInspection(height)}
}
func mediaInspection(height int) map[string]any {
	width := 1280
	if height == 360 {
		width = 640
	}
	return map[string]any{"container": "mp4", "durationSeconds": 12, "hdrFormat": "sdr", "videoTracks": []map[string]any{{"index": 0, "type": "video", "codec": "h264", "width": width, "height": height}}, "audioTracks": []map[string]any{{"index": 1, "type": "audio", "codec": "aac", "language": "eng", "title": "English", "channels": 2}, {"index": 2, "type": "audio", "codec": "aac", "language": "fra", "title": "Français", "channels": 2}}, "subtitleTracks": []any{}}
}
func validSourceRef(value string) bool {
	return (strings.HasPrefix(value, "demo-source-720-") && knownResource(strings.TrimPrefix(value, "demo-source-720-"))) || (strings.HasPrefix(value, "demo-source-360-") && knownResource(strings.TrimPrefix(value, "demo-source-360-")))
}
func knownResource(id string) bool {
	if _, ok := findMedia(id); ok {
		return true
	}
	switch id {
	case "demo-orbit-station:1:1", "demo-orbit-station:1:2", "demo-orbit-station:2:1", "demo-orbit-station:2:2":
		return true
	}
	return false
}
func knownTitle(id string) bool {
	if _, ok := findMedia(id); ok {
		return true
	}
	switch id {
	case OrbitEpisodeOne, OrbitEpisodeTwo, OrbitEpisodeThree, OrbitEpisodeFour:
		return true
	}
	return false
}
func pagination(r *http.Request, defaultPage, defaultSize int) (int, int, int, bool) {
	page := defaultPage
	size := defaultSize
	var err error
	if value := r.URL.Query().Get("page"); value != "" {
		page, err = strconv.Atoi(value)
		if err != nil {
			return 0, 0, 0, false
		}
	}
	if value := r.URL.Query().Get("pageSize"); value != "" {
		size, err = strconv.Atoi(value)
		if err != nil {
			return 0, 0, 0, false
		}
	}
	if page <= 0 || size <= 0 || size > 100 || page-1 > maxInt()/size {
		return 0, 0, 0, false
	}
	return page, size, (page - 1) * size, true
}
func skipLimit(r *http.Request) (int, int, bool) {
	skip, limit := 0, 24
	var err error
	if value := r.URL.Query().Get("skip"); value != "" {
		skip, err = strconv.Atoi(value)
		if err != nil {
			return 0, 0, false
		}
	}
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil {
			return 0, 0, false
		}
	}
	return skip, limit, skip >= 0 && limit > 0 && limit <= 100
}
func paginationWindow(offset, limit, total int) (int, int) {
	if offset >= total {
		return total, total
	}
	if limit >= total-offset {
		return offset, total
	}
	return offset, offset + limit
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
func queryInt64(r *http.Request, name string) (int64, bool) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return 0, true
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return parsed, err == nil
}
