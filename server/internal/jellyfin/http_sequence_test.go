package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/profile"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

const (
	sequenceServerID              = "71000000-0000-4000-8000-000000000001"
	sequencePrimaryProfileID      = "72000000-0000-4000-8000-000000000002"
	sequenceSecondaryProfileID    = "73000000-0000-4000-8000-000000000003"
	sequencePrimaryCredentialID   = "7d000000-0000-4000-8000-00000000000d"
	sequenceSecondaryCredentialID = "7e000000-0000-4000-8000-00000000000e"
	sequenceSeriesID              = "74000000-0000-4000-8000-000000000004"
	sequenceSeasonID              = "75000000-0000-4000-8000-000000000005"
	sequenceEpisodeID             = "76000000-0000-4000-8000-000000000006"
	sequenceProviderResource      = "PROVIDER_RESOURCE_SENTINEL"
	sequenceProviderSource        = "PROVIDER_SOURCE_REF_SENTINEL"
	sequenceProviderHeader        = "PROVIDER_HEADER_SECRET_SENTINEL"
	sequencePassword              = "PASSWORD_SECRET_SENTINEL"
)

type sequenceAuthentication struct {
	now      time.Time
	sessions map[string]AuthenticatedSession
	revoked  map[string]bool
}

func (authentication *sequenceAuthentication) Login(_ context.Context, input CompatLoginInput) (LoginResult, error) {
	if input.Password != sequencePassword {
		return LoginResult{}, ErrInvalidCompatLogin
	}
	var token, sessionID, profileID, profileName, nativeSessionID, userID string
	switch {
	case strings.EqualFold(input.Username, sequencePrimaryCredentialID):
		token = compatTestToken(31)
		sessionID = "77000000-0000-4000-8000-000000000007"
		profileID = sequencePrimaryProfileID
		profileName = "Main"
		nativeSessionID = "78000000-0000-4000-8000-000000000008"
		userID = "79000000-0000-4000-8000-000000000009"
	case strings.EqualFold(input.Username, sequenceSecondaryCredentialID):
		token = compatTestToken(32)
		sessionID = "7a000000-0000-4000-8000-00000000000a"
		profileID = sequenceSecondaryProfileID
		profileName = "Guest"
		nativeSessionID = "7b000000-0000-4000-8000-00000000000b"
		userID = "7c000000-0000-4000-8000-00000000000c"
	default:
		return LoginResult{}, ErrInvalidCompatLogin
	}
	principal := auth.Principal{
		SessionID:       nativeSessionID,
		UserID:          userID,
		DeviceID:        input.Client.DeviceID,
		ActiveProfileID: &profileID,
	}
	session := AuthenticatedSession{
		ID:          sessionID,
		ProfileID:   profileID,
		ProfileName: profileName,
		Client:      input.Client,
		ExpiresAt:   authentication.now.Add(time.Hour),
		Principal:   principal,
	}
	authentication.sessions[token] = session
	delete(authentication.revoked, token)
	return LoginResult{
		Credential: CompatCredential{Token: token, SessionID: sessionID, ExpiresAt: session.ExpiresAt},
		Profile:    profile.Profile{ID: profileID, Name: profileName, Accessible: true},
		Principal:  principal,
	}, nil
}

func (authentication *sequenceAuthentication) Authenticate(_ context.Context, token string) (AuthenticatedSession, error) {
	session, exists := authentication.sessions[token]
	if !exists || authentication.revoked[token] {
		return AuthenticatedSession{}, ErrInvalidCompatCredential
	}
	return session, nil
}

func (authentication *sequenceAuthentication) Logout(_ context.Context, session AuthenticatedSession) error {
	for token, candidate := range authentication.sessions {
		if candidate.ID == session.ID {
			authentication.revoked[token] = true
			return nil
		}
	}
	return ErrInvalidCompatCredential
}

type sequenceWatchstate struct {
	profiles map[string]*memoryWatchstate
}

func newSequenceWatchstate() *sequenceWatchstate {
	return &sequenceWatchstate{profiles: map[string]*memoryWatchstate{
		sequencePrimaryProfileID:   newMemoryWatchstate(),
		sequenceSecondaryProfileID: newMemoryWatchstate(),
	}}
}

func (state *sequenceWatchstate) forPrincipal(principal auth.Principal) *memoryWatchstate {
	if principal.ActiveProfileID == nil {
		return newMemoryWatchstate()
	}
	service := state.profiles[*principal.ActiveProfileID]
	if service == nil {
		service = newMemoryWatchstate()
		state.profiles[*principal.ActiveProfileID] = service
	}
	return service
}

func (state *sequenceWatchstate) GetProgress(ctx context.Context, principal auth.Principal, itemID string) (watchstate.Progress, error) {
	return state.forPrincipal(principal).GetProgress(ctx, principal, itemID)
}

func (state *sequenceWatchstate) UpdateProgress(ctx context.Context, principal auth.Principal, itemID string, input watchstate.UpdateProgressInput) (watchstate.Progress, error) {
	return state.forPrincipal(principal).UpdateProgress(ctx, principal, itemID, input)
}

func (state *sequenceWatchstate) ApplyPlaybackEventForLinkedSession(ctx context.Context, principal auth.Principal, itemID string, input watchstate.UpdateProgressInput) (watchstate.Progress, error) {
	return state.forPrincipal(principal).ApplyPlaybackEventForLinkedSession(ctx, principal, itemID, input)
}

func (state *sequenceWatchstate) SetWatched(ctx context.Context, principal auth.Principal, itemID string, completed bool, input watchstate.CompletionInput) (watchstate.Progress, error) {
	return state.forPrincipal(principal).SetWatched(ctx, principal, itemID, completed, input)
}

func (state *sequenceWatchstate) SetWatchedForLinkedSession(ctx context.Context, principal auth.Principal, itemID string, completed bool, input watchstate.CompletionInput) (watchstate.Progress, error) {
	return state.forPrincipal(principal).SetWatchedForLinkedSession(ctx, principal, itemID, completed, input)
}

func (state *sequenceWatchstate) ClearProgress(ctx context.Context, principal auth.Principal, itemID string, expectedVersion int64) error {
	return state.forPrincipal(principal).ClearProgress(ctx, principal, itemID, expectedVersion)
}

func (state *sequenceWatchstate) ListResume(ctx context.Context, principal auth.Principal, offset, limit int) (watchstate.ContinueItemsPage, error) {
	return state.forPrincipal(principal).ListResume(ctx, principal, offset, limit)
}

func (state *sequenceWatchstate) ListNextUp(ctx context.Context, principal auth.Principal, seriesID string, offset, limit int) (watchstate.ContinueItemsPage, error) {
	return state.forPrincipal(principal).ListNextUp(ctx, principal, seriesID, offset, limit)
}

type sequenceCatalog struct {
	items      map[string]watchstate.CatalogTitle
	order      []string
	watchstate *sequenceWatchstate
	listCalls  int
	getCalls   int
}

func (catalog *sequenceCatalog) GetCatalogTitle(_ context.Context, principal auth.Principal, itemID string) (watchstate.CatalogTitle, error) {
	catalog.getCalls++
	if !sequenceKnownProfile(principal) {
		return watchstate.CatalogTitle{}, watchstate.ErrNotFound
	}
	item, exists := catalog.items[itemID]
	if !exists {
		return watchstate.CatalogTitle{}, watchstate.ErrNotFound
	}
	return catalog.withProgress(principal, item), nil
}

func (catalog *sequenceCatalog) ListCatalogItems(_ context.Context, principal auth.Principal, query watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
	catalog.listCalls++
	if !sequenceKnownProfile(principal) {
		return watchstate.CatalogPage{}, watchstate.ErrNotFound
	}
	filtered := make([]watchstate.CatalogTitle, 0, len(catalog.order))
	for _, itemID := range catalog.order {
		item := catalog.items[itemID]
		if len(query.MediaTypes) > 0 && !slices.Contains(query.MediaTypes, item.MediaType) {
			continue
		}
		if query.ParentID != "" {
			underParent := item.ParentID == query.ParentID
			if query.Recursive && item.SeriesID == query.ParentID {
				underParent = true
			}
			if !underParent {
				continue
			}
		}
		if query.SearchTerm != "" && !strings.Contains(strings.ToLower(item.Title), strings.ToLower(query.SearchTerm)) {
			continue
		}
		if len(query.IDs) > 0 && !slices.Contains(query.IDs, item.ID) {
			continue
		}
		filtered = append(filtered, catalog.withProgress(principal, item))
	}
	total := len(filtered)
	start := min(query.Offset, total)
	end := min(start+query.Limit, total)
	return watchstate.CatalogPage{Items: filtered[start:end], Offset: query.Offset, Limit: query.Limit, Total: total}, nil
}

func (catalog *sequenceCatalog) withProgress(principal auth.Principal, item watchstate.CatalogTitle) watchstate.CatalogTitle {
	service := catalog.watchstate.forPrincipal(principal)
	progress, exists := service.progress[item.ID]
	if !exists {
		item.Progress = nil
		return item
	}
	lastWatched := progress.LastWatchedAt
	item.Progress = &watchstate.CatalogProgress{
		PositionSeconds: progress.PositionSeconds,
		DurationSeconds: progress.DurationSeconds,
		Completed:       progress.Completed,
		LastWatchedAt:   &lastWatched,
	}
	return item
}

func sequenceKnownProfile(principal auth.Principal) bool {
	return principal.ActiveProfileID != nil && (*principal.ActiveProfileID == sequencePrimaryProfileID || *principal.ActiveProfileID == sequenceSecondaryProfileID)
}

type sequencePlaybackDelivery struct {
	*fakeCompatPlaybackDelivery
	media          []byte
	sourceInputs   []playback.SourcesInput
	sourceProfiles []string
	providerHeader string
}

func (delivery *sequencePlaybackDelivery) Sources(_ context.Context, principal auth.Principal, input playback.SourcesInput) (playback.SourceList, error) {
	delivery.sourceInputs = append(delivery.sourceInputs, input)
	profileID := ""
	if principal.ActiveProfileID != nil {
		profileID = *principal.ActiveProfileID
	}
	delivery.sourceProfiles = append(delivery.sourceProfiles, profileID)
	return delivery.sources, nil
}

func (delivery *sequencePlaybackDelivery) SourcesAndPin(ctx context.Context, principal auth.Principal, input playback.SourcesInput) (playback.SourceList, error) {
	list, err := delivery.Sources(ctx, principal, input)
	if err != nil {
		return playback.SourceList{}, err
	}
	if err := delivery.PinSourceReferences(principal, sourceOptionReferenceIDs(list.Sources)); err != nil {
		return playback.SourceList{}, err
	}
	return list, nil
}

func (delivery *sequencePlaybackDelivery) Serve(response http.ResponseWriter, request *http.Request, handle playback.DeliveryHandle) error {
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	if !handle.Valid() {
		return playback.ErrSessionNotFound
	}
	delivery.serveCalls++
	delivery.servedMethod = request.Method
	delivery.servedRange = request.Header.Get("Range")
	delivery.servedStartTicks = request.URL.Query().Get("StartTimeTicks")
	start, end := 0, len(delivery.media)-1
	status := http.StatusOK
	if requestedRange := request.Header.Get("Range"); requestedRange != "" {
		if _, err := fmt.Sscanf(requestedRange, "bytes=%d-%d", &start, &end); err != nil || start < 0 || end < start || end >= len(delivery.media) {
			response.Header().Set("Content-Range", "bytes */"+strconv.Itoa(len(delivery.media)))
			response.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return nil
		}
		status = http.StatusPartialContent
		response.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(delivery.media)))
	}
	response.Header().Set("Accept-Ranges", "bytes")
	response.Header().Set("Content-Type", "video/mp4")
	response.Header().Set("Content-Length", strconv.Itoa(end-start+1))
	response.WriteHeader(status)
	if request.Method == http.MethodGet {
		_, _ = response.Write(delivery.media[start : end+1])
	}
	return nil
}

type recordedSequenceResponse struct {
	name     string
	response *httptest.ResponseRecorder
}

type sequenceHTTPFixture struct {
	prefix      string
	client      string
	tokenHeader string
	mux         http.Handler
	auth        *sequenceAuthentication
	catalog     *sequenceCatalog
	watchstate  *sequenceWatchstate
	artwork     *artworkDelivery
	playback    *sequencePlaybackDelivery
	logs        *bytes.Buffer
	responses   []recordedSequenceResponse
}

func TestJellyfinCompatibleClientHTTPSequences(t *testing.T) {
	for _, test := range []struct {
		name        string
		prefix      string
		client      string
		tokenHeader string
	}{
		{name: "Infuse root", client: "Infuse", tokenHeader: "X-MediaBrowser-Token"},
		{name: "VidHub emby alias", prefix: "/emby", client: "VidHub", tokenHeader: "X-Emby-Token"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSequenceHTTPFixture(t, test.prefix, test.client, test.tokenHeader)
			fixture.run(t)
		})
	}
}

func newSequenceHTTPFixture(t *testing.T, prefix, client, tokenHeader string) *sequenceHTTPFixture {
	t.Helper()
	now := time.Now().UTC()
	serverID, err := ParseServerID(sequenceServerID)
	if err != nil {
		t.Fatalf("parse sequence server ID: %v", err)
	}
	state := newSequenceWatchstate()
	posterKey := strings.Repeat("a", 64)
	posterURL := localizedArtworkPrefix + posterKey
	seasonIndex, episodeIndex := 1, 3
	runtimeMinutes := 60
	items := map[string]watchstate.CatalogTitle{
		sequenceSeriesID: {
			ID: sequenceSeriesID, MediaType: "series", Title: "Sequence Series", Released: "2025-01-02",
			PosterURL: posterURL, Genres: []string{"Drama"}, InLibrary: true, ProviderIDs: map[string]string{"tvdb": "7004"},
		},
		sequenceSeasonID: {
			ID: sequenceSeasonID, MediaType: "season", ParentID: sequenceSeriesID, SeriesID: sequenceSeriesID,
			Title: "Season One", SeriesTitle: "Sequence Series", Ordinal: &seasonIndex,
			PosterURL: posterURL, Genres: []string{}, InLibrary: true, ProviderIDs: map[string]string{"tvdb": "7005"},
		},
		sequenceEpisodeID: {
			ID: sequenceEpisodeID, MediaType: "episode", ParentID: sequenceSeasonID, SeriesID: sequenceSeriesID, SeasonID: sequenceSeasonID,
			Title: "Pilot Sequence", SeriesTitle: "Sequence Series", SeasonTitle: "Season One", Ordinal: &episodeIndex, ParentOrdinal: &seasonIndex,
			Released: "2025-01-03", Overview: "A deterministic compatibility fixture", RuntimeMinutes: &runtimeMinutes,
			PosterURL: posterURL, BackgroundURL: "https://provider.invalid/backdrop?token=PROVIDER_IMAGE_TOKEN_SENTINEL",
			Genres: []string{"Drama"}, InLibrary: true, ProviderIDs: map[string]string{"tvdb": "7006"},
			ResourceID: sequenceProviderResource, ResourceProvider: "PROVIDER_NAME_SENTINEL", SourceAddonID: "PROVIDER_ADDON_SENTINEL", SourceName: "PROVIDER_SOURCE_NAME_SENTINEL",
		},
	}
	catalog := &sequenceCatalog{items: items, order: []string{sequenceSeriesID, sequenceSeasonID, sequenceEpisodeID}, watchstate: state}
	authentication := &sequenceAuthentication{now: now, sessions: make(map[string]AuthenticatedSession), revoked: make(map[string]bool)}
	artwork := &artworkDelivery{keys: map[string]string{posterURL: posterKey}, body: []byte("pngbytes")}
	native := &fakeCompatPlaybackDelivery{
		now:    now,
		handle: opaquePlaybackHandle(t),
		sources: playback.SourceList{Sources: []playback.SourceOption{{
			SourceRef:  sequenceProviderSource,
			AddonID:    "PROVIDER_ADDON_SENTINEL",
			ManifestID: "PROVIDER_MANIFEST_SENTINEL",
			Name:       "Primary",
			Protocol:   "http",
			Container:  "mp4",
			ExpiresAt:  now.Add(time.Hour),
		}}},
	}
	delivery := &sequencePlaybackDelivery{
		fakeCompatPlaybackDelivery: native,
		media:                      []byte("0123456789"),
		providerHeader:             "Authorization: Bearer " + sequenceProviderHeader,
	}
	logs := &bytes.Buffer{}
	handler, err := New(Dependencies{
		ServerInfo:     ServerInfo{ID: serverID, Name: "Sequence Rivune", RuntimeVersion: "RUNTIME_SECRET_SENTINEL"},
		Authentication: authentication,
		Catalog:        catalog,
		Artwork:        artwork,
		Playback:       delivery,
		Watchstate:     state,
		Logger:         slog.New(slog.NewJSONHandler(logs, nil)),
	})
	if err != nil {
		t.Fatalf("create complete sequence handler: %v", err)
	}
	handler.playSessions.now = func() time.Time { return now }
	return &sequenceHTTPFixture{
		prefix: prefix, client: client, tokenHeader: tokenHeader, mux: handler,
		auth: authentication, catalog: catalog, watchstate: state, artwork: artwork, playback: delivery, logs: logs,
	}
}

func (fixture *sequenceHTTPFixture) run(t *testing.T) {
	t.Helper()

	public := fixture.request(t, "public-info", http.MethodGet, fixture.prefix+"/System/Info/Public", "", "")
	sequenceRequireStatus(t, public, http.StatusOK)
	var publicInfo PublicSystemInfo
	sequenceDecode(t, public, &publicInfo)
	sequenceRequireObjectKeys(t, public.Body.Bytes(), "Id", "ServerName", "Version", "ProductName", "StartupWizardCompleted")
	if publicInfo.Id != sequenceServerID || publicInfo.Version != CompatibilityVersion || publicInfo.ProductName != CompatibilityProduct || !publicInfo.StartupWizardCompleted {
		t.Fatalf("public compatibility identity is incomplete: %+v", publicInfo)
	}

	primaryLogin := fixture.login(t, "login-primary", sequencePrimaryCredentialID)
	sequenceRequireStatus(t, primaryLogin, http.StatusOK)
	var primaryAuth AuthenticationResult
	sequenceDecode(t, primaryLogin, &primaryAuth)
	sequenceRequireObjectKeys(t, primaryLogin.Body.Bytes(), "User", "SessionInfo", "AccessToken", "ServerId")
	if primaryAuth.AccessToken == "" || primaryAuth.User.Id != sequencePrimaryProfileID || primaryAuth.User.Name != "Main" || primaryAuth.SessionInfo.UserId != primaryAuth.User.Id || primaryAuth.SessionInfo.Client != fixture.client || primaryAuth.ServerId != sequenceServerID {
		t.Fatalf("primary authentication binding is incomplete: %+v", primaryAuth)
	}
	primaryToken := primaryAuth.AccessToken

	me := fixture.request(t, "me", http.MethodGet, fixture.prefix+"/Users/Me", "", primaryToken)
	sequenceRequireStatus(t, me, http.StatusOK)
	var user UserDto
	sequenceDecode(t, me, &user)
	sequenceRequireObjectKeys(t, me.Body.Bytes(), "Name", "ServerId", "Id", "Policy", "Configuration")
	if user.Id != primaryAuth.User.Id || user.ServerId != publicInfo.Id || !user.Policy.EnablePlayback {
		t.Fatalf("Users/Me lost authenticated binding or playback policy: %+v", user)
	}

	viewsResponse := fixture.request(t, "views", http.MethodGet, fixture.prefix+"/Users/"+url.PathEscape(user.Id)+"/Views", "", primaryToken)
	sequenceRequireStatus(t, viewsResponse, http.StatusOK)
	var views QueryResult[BaseItemDto]
	sequenceDecode(t, viewsResponse, &views)
	sequenceRequireObjectKeys(t, viewsResponse.Body.Bytes(), "Items", "TotalRecordCount", "StartIndex")
	if len(views.Items) != 3 || views.TotalRecordCount != 3 || views.Items[0].Id == "" || views.Items[1].Id == "" || views.Items[2].Id == "" || views.Items[0].Id == views.Items[1].Id || views.Items[1].Id == views.Items[2].Id {
		t.Fatalf("virtual views are incomplete or non-opaque: %+v", views)
	}
	var tvView BaseItemDto
	for _, view := range views.Items {
		if view.CollectionType == "tvshows" {
			tvView = view
		}
	}
	if tvView.Id == "" || !tvView.IsFolder || tvView.Type != "CollectionFolder" {
		t.Fatalf("TV view is structurally incomplete: %+v", tvView)
	}

	itemsResponse := fixture.request(t, "items", http.MethodGet, fixture.prefix+"/Items?ParentId="+url.QueryEscape(tvView.Id)+"&IncludeItemTypes=Series&EnableUserData=true", "", primaryToken)
	sequenceRequireStatus(t, itemsResponse, http.StatusOK)
	var seriesPage QueryResult[BaseItemDto]
	sequenceDecode(t, itemsResponse, &seriesPage)
	if len(seriesPage.Items) != 1 || seriesPage.TotalRecordCount != 1 || seriesPage.Items[0].Id == "" || seriesPage.Items[0].Type != "Series" || !seriesPage.Items[0].IsFolder || seriesPage.Items[0].ServerId != publicInfo.Id {
		t.Fatalf("series page is incomplete: %+v", seriesPage)
	}
	seriesID := seriesPage.Items[0].Id

	seasonsResponse := fixture.request(t, "seasons", http.MethodGet, fixture.prefix+"/Shows/"+url.PathEscape(seriesID)+"/Seasons?UserId="+url.QueryEscape(user.Id), "", primaryToken)
	sequenceRequireStatus(t, seasonsResponse, http.StatusOK)
	var seasons QueryResult[BaseItemDto]
	sequenceDecode(t, seasonsResponse, &seasons)
	if len(seasons.Items) != 1 || seasons.Items[0].Id == "" || seasons.Items[0].Type != "Season" || seasons.Items[0].SeriesId != seriesID || seasons.Items[0].IndexNumber == nil {
		t.Fatalf("season hierarchy is incomplete: %+v", seasons)
	}
	seasonID := seasons.Items[0].Id

	episodesResponse := fixture.request(t, "episodes", http.MethodGet, fixture.prefix+"/Shows/"+url.PathEscape(seriesID)+"/Episodes?UserId="+url.QueryEscape(user.Id)+"&SeasonId="+url.QueryEscape(seasonID), "", primaryToken)
	sequenceRequireStatus(t, episodesResponse, http.StatusOK)
	var episodes QueryResult[BaseItemDto]
	sequenceDecode(t, episodesResponse, &episodes)
	sequenceRequireArrayObjectKeys(t, episodesResponse.Body.Bytes(), "Items", 0, "Id", "ServerId", "Name", "Type", "MediaType", "IsFolder", "IsPlayable", "SeriesId", "SeasonId", "IndexNumber", "ParentIndexNumber", "Genres", "ImageTags", "BackdropImageTags", "UserData")
	if len(episodes.Items) != 1 || episodes.Items[0].Id == "" || episodes.Items[0].Type != "Episode" || !episodes.Items[0].IsPlayable || episodes.Items[0].SeriesId != seriesID || episodes.Items[0].SeasonId != seasonID || episodes.Items[0].RunTimeTicks == nil || episodes.Items[0].ImageTags["Primary"] == "" {
		t.Fatalf("episode hierarchy DTO is incomplete: %+v", episodes)
	}
	episodeID := episodes.Items[0].Id

	searchResponse := fixture.request(t, "search", http.MethodGet, fixture.prefix+"/Search/Hints?SearchTerm=pilot&IncludeItemTypes=Episode&Limit=10", "", primaryToken)
	sequenceRequireStatus(t, searchResponse, http.StatusOK)
	var search SearchHintResult
	sequenceDecode(t, searchResponse, &search)
	sequenceRequireObjectKeys(t, searchResponse.Body.Bytes(), "SearchHints", "TotalRecordCount")
	if len(search.SearchHints) != 1 || search.TotalRecordCount != 1 || search.SearchHints[0].Id != episodeID || search.SearchHints[0].ItemId != episodeID || search.SearchHints[0].Type != "Episode" {
		t.Fatalf("search did not preserve the returned episode identity: %+v", search)
	}

	imagePath := fixture.prefix + "/Items/" + url.PathEscape(episodeID) + "/Images/Primary?api_key=" + url.QueryEscape(primaryToken)
	imageGET := fixture.request(t, "image-get", http.MethodGet, imagePath, "", "")
	sequenceRequireStatus(t, imageGET, http.StatusOK)
	if imageGET.Body.String() != "pngbytes" || imageGET.Header().Get("Content-Type") != "image/png" || imageGET.Header().Get("Content-Length") != "8" || imageGET.Header().Get("ETag") == "" || imageGET.Header().Get("Location") != "" {
		t.Fatalf("image GET lost local delivery semantics: status=%d headers=%v body=%q", imageGET.Code, imageGET.Header(), imageGET.Body.String())
	}
	imageHEAD := fixture.request(t, "image-head", http.MethodHead, imagePath, "", "")
	sequenceRequireStatus(t, imageHEAD, http.StatusOK)
	if imageHEAD.Body.Len() != 0 || imageHEAD.Header().Get("Content-Length") != imageGET.Header().Get("Content-Length") || imageHEAD.Header().Get("ETag") != imageGET.Header().Get("ETag") || imageHEAD.Header().Get("Location") != "" {
		t.Fatalf("image HEAD differs from GET metadata: headers=%v body=%q", imageHEAD.Header(), imageHEAD.Body.String())
	}

	playbackInfoResponse := fixture.request(t, "playback-info", http.MethodGet, fixture.prefix+"/Items/"+url.PathEscape(episodeID)+"/PlaybackInfo?UserId="+url.QueryEscape(user.Id), "", primaryToken)
	sequenceRequireStatus(t, playbackInfoResponse, http.StatusOK)
	var playbackInfo PlaybackInfoResponse
	sequenceDecode(t, playbackInfoResponse, &playbackInfo)
	sequenceRequireObjectKeys(t, playbackInfoResponse.Body.Bytes(), "MediaSources", "PlaySessionId")
	sequenceRequireArrayObjectKeys(t, playbackInfoResponse.Body.Bytes(), "MediaSources", 0, "Id", "Name", "Path", "Container", "Protocol", "Type", "IsRemote", "SupportsDirectPlay", "SupportsDirectStream", "SupportsTranscoding")
	if playbackInfo.PlaySessionId == "" || len(playbackInfo.MediaSources) != 1 {
		t.Fatalf("playback negotiation returned no opaque session/source: %+v", playbackInfo)
	}
	mediaSource := playbackInfo.MediaSources[0]
	if mediaSource.Id == "" || mediaSource.Path == "" || mediaSource.Protocol != "Http" || mediaSource.Type != "Default" || mediaSource.IsRemote || !mediaSource.SupportsDirectPlay || !mediaSource.SupportsDirectStream || strings.Contains(mediaSource.Path, primaryToken) || strings.Contains(mediaSource.Path, sequenceProviderSource) || strings.Contains(mediaSource.Path, sequenceProviderResource) {
		t.Fatalf("media source DTO is incomplete or disclosed authority: %+v", mediaSource)
	}
	if len(fixture.playback.sourceInputs) != 1 || fixture.playback.sourceInputs[0].ResourceID != sequenceProviderResource || fixture.playback.sourceProfiles[0] != user.Id || fixture.playback.openCalls != 0 {
		t.Fatalf("playback source enumeration lost binding or became eager: inputs=%+v profiles=%v opens=%d", fixture.playback.sourceInputs, fixture.playback.sourceProfiles, fixture.playback.openCalls)
	}

	secondaryLogin := fixture.login(t, "login-secondary", sequenceSecondaryCredentialID)
	sequenceRequireStatus(t, secondaryLogin, http.StatusOK)
	var secondaryAuth AuthenticationResult
	sequenceDecode(t, secondaryLogin, &secondaryAuth)
	if secondaryAuth.AccessToken == "" || secondaryAuth.AccessToken == primaryToken || secondaryAuth.User.Id != sequenceSecondaryProfileID || secondaryAuth.SessionInfo.UserId != secondaryAuth.User.Id {
		t.Fatalf("secondary authentication is not independently bound: %+v", secondaryAuth)
	}
	secondaryToken := secondaryAuth.AccessToken

	listCalls := fixture.catalog.listCalls
	foreignItems := fixture.request(t, "cross-profile-items", http.MethodGet, fixture.prefix+"/Users/"+url.PathEscape(user.Id)+"/Items", "", secondaryToken)
	sequenceRequireStatus(t, foreignItems, http.StatusNotFound)
	if fixture.catalog.listCalls != listCalls {
		t.Fatal("cross-profile item selector reached the catalog")
	}
	crossPlayback := fixture.request(t, "cross-profile-playback", http.MethodGet, fixture.prefix+"/Items/"+url.PathEscape(episodeID)+"/PlaybackInfo?UserId="+url.QueryEscape(secondaryAuth.User.Id), "", primaryToken)
	sequenceRequireStatus(t, crossPlayback, http.StatusNotFound)
	if len(fixture.playback.sourceInputs) != 1 {
		t.Fatal("cross-profile PlaybackInfo enumerated sources")
	}

	streamTarget := fixture.prefix + mediaSource.Path
	foreignStream := fixture.request(t, "cross-session-stream", http.MethodGet, streamTarget, "", secondaryToken)
	sequenceRequireStatus(t, foreignStream, http.StatusNotFound)
	if fixture.playback.openCalls != 0 || fixture.playback.serveCalls != 0 {
		t.Fatalf("foreign token replay opened or served the primary handle: opens=%d serves=%d", fixture.playback.openCalls, fixture.playback.serveCalls)
	}

	primaryState := fixture.watchstate.profiles[user.Id]
	foreignEventBody := sequenceJSON(t, PlaybackProgressInfo{
		UserId: secondaryAuth.User.Id, ItemId: episodeID, MediaSourceId: mediaSource.Id,
		PlaySessionId: playbackInfo.PlaySessionId, PositionTicks: SecondsToTicks(20),
	})
	foreignEvent := fixture.request(t, "cross-profile-progress", http.MethodPost, fixture.prefix+"/Sessions/Playing/Progress", foreignEventBody, primaryToken)
	sequenceRequireStatus(t, foreignEvent, http.StatusNotFound)
	if len(primaryState.progress) != 0 || len(fixture.watchstate.profiles[secondaryAuth.User.Id].progress) != 0 {
		t.Fatal("cross-profile progress mutated watch state")
	}

	streamGETRequest := httptest.NewRequest(http.MethodGet, streamTarget, nil)
	streamGETRequest.Header.Set(fixture.tokenHeader, primaryToken)
	streamGETRequest.Header.Set("Range", "bytes=2-5")
	streamGETRequest.Header.Set("X-Provider-Authorization", sequenceProviderHeader)
	streamGET := fixture.serve("stream-range-get", streamGETRequest)
	sequenceRequireStatus(t, streamGET, http.StatusPartialContent)
	if streamGET.Body.String() != "2345" || streamGET.Header().Get("Content-Range") != "bytes 2-5/10" || streamGET.Header().Get("Content-Length") != "4" || streamGET.Header().Get("Accept-Ranges") != "bytes" || streamGET.Header().Get("Location") != "" {
		t.Fatalf("stream Range GET semantics are wrong: headers=%v body=%q", streamGET.Header(), streamGET.Body.String())
	}
	if fixture.playback.openCalls != 1 || len(fixture.playback.inputs) != 1 || fixture.playback.inputs[0].SourceRef != sequenceProviderSource || fixture.playback.inputs[0].TitleID != episodeID || fixture.playback.servedRange != "bytes=2-5" {
		t.Fatalf("stream did not bind opaque selectors to one native source: opens=%d inputs=%+v range=%q", fixture.playback.openCalls, fixture.playback.inputs, fixture.playback.servedRange)
	}

	streamHEADRequest := httptest.NewRequest(http.MethodHead, streamTarget, nil)
	streamHEADRequest.Header.Set(fixture.tokenHeader, primaryToken)
	streamHEADRequest.Header.Set("Range", "bytes=0-3")
	streamHEADRequest.Header.Set("X-Provider-Authorization", sequenceProviderHeader)
	streamHEAD := fixture.serve("stream-range-head", streamHEADRequest)
	sequenceRequireStatus(t, streamHEAD, http.StatusPartialContent)
	if streamHEAD.Body.Len() != 0 || streamHEAD.Header().Get("Content-Range") != "bytes 0-3/10" || streamHEAD.Header().Get("Content-Length") != "4" || fixture.playback.openCalls != 1 || fixture.playback.serveCalls != 2 || fixture.playback.servedMethod != http.MethodHead {
		t.Fatalf("stream HEAD reopened, wrote a body, or lost Range metadata: headers=%v body=%q opens=%d serves=%d", streamHEAD.Header(), streamHEAD.Body.String(), fixture.playback.openCalls, fixture.playback.serveCalls)
	}

	event := func(name, endpoint string, seconds int) *httptest.ResponseRecorder {
		body := sequenceJSON(t, PlaybackProgressInfo{
			UserId: user.Id, ItemId: episodeID, MediaSourceId: mediaSource.Id,
			PlaySessionId: playbackInfo.PlaySessionId, PositionTicks: SecondsToTicks(int64(seconds)),
		})
		return fixture.request(t, name, http.MethodPost, fixture.prefix+endpoint, body, primaryToken)
	}
	playing := event("playing", "/Sessions/Playing", 5)
	sequenceRequireStatus(t, playing, http.StatusNoContent)
	progress := event("progress", "/Sessions/Playing/Progress", 40)
	sequenceRequireStatus(t, progress, http.StatusNoContent)
	stale := event("stale-progress", "/Sessions/Playing/Progress", 10)
	sequenceRequireStatus(t, stale, http.StatusNoContent)
	if current := primaryState.progress[episodeID]; current.PositionSeconds != 40 || current.DurationSeconds != 3600 || current.Version != 2 || current.Completed {
		t.Fatalf("progress was not monotone after stale event: %+v", current)
	}

	foreignHandleBody := sequenceJSON(t, PlaybackProgressInfo{
		UserId: secondaryAuth.User.Id, ItemId: episodeID, MediaSourceId: mediaSource.Id,
		PlaySessionId: playbackInfo.PlaySessionId, PositionTicks: SecondsToTicks(50),
	})
	foreignHandle := fixture.request(t, "cross-session-progress", http.MethodPost, fixture.prefix+"/Sessions/Playing/Progress", foreignHandleBody, secondaryToken)
	sequenceRequireStatus(t, foreignHandle, http.StatusNotFound)
	if current := primaryState.progress[episodeID]; current.PositionSeconds != 40 || current.Version != 2 || len(fixture.watchstate.profiles[secondaryAuth.User.Id].progress) != 0 {
		t.Fatalf("foreign handle replay changed state: primary=%+v secondary=%+v", current, fixture.watchstate.profiles[secondaryAuth.User.Id].progress)
	}

	stopped := event("stopped", "/Sessions/Playing/Stopped", 60)
	sequenceRequireStatus(t, stopped, http.StatusNoContent)
	if current := primaryState.progress[episodeID]; current.PositionSeconds != 60 || current.DurationSeconds != 3600 || current.Version != 3 || current.Completed {
		t.Fatalf("stopped did not persist the final monotone position: %+v", current)
	}
	if fixture.playback.closeCalls != 1 {
		t.Fatalf("stopped closed native delivery %d times, want once", fixture.playback.closeCalls)
	}

	replayedStop := event("replayed-stop", "/Sessions/Playing/Stopped", 80)
	sequenceRequireStatus(t, replayedStop, http.StatusNotFound)
	replayedStream := fixture.request(t, "replayed-stream", http.MethodGet, streamTarget, "", primaryToken)
	sequenceRequireStatus(t, replayedStream, http.StatusNotFound)
	if current := primaryState.progress[episodeID]; current.PositionSeconds != 60 || current.Version != 3 || fixture.playback.serveCalls != 2 || fixture.playback.closeCalls != 1 {
		t.Fatalf("closed handle was replayable: progress=%+v serves=%d closes=%d", current, fixture.playback.serveCalls, fixture.playback.closeCalls)
	}

	finalItemResponse := fixture.request(t, "final-user-data", http.MethodGet, fixture.prefix+"/Users/"+url.PathEscape(user.Id)+"/Items/"+url.PathEscape(episodeID)+"?EnableUserData=true", "", primaryToken)
	sequenceRequireStatus(t, finalItemResponse, http.StatusOK)
	var finalItem BaseItemDto
	sequenceDecode(t, finalItemResponse, &finalItem)
	sequenceRequireObjectKeys(t, finalItemResponse.Body.Bytes(), "Id", "ServerId", "Name", "Type", "MediaType", "IsFolder", "IsPlayable", "Genres", "BackdropImageTags", "UserData")
	if finalItem.Id != episodeID || finalItem.UserData == nil || finalItem.UserData.Key != episodeID || finalItem.UserData.PlaybackPositionTicks != SecondsToTicks(60) || finalItem.UserData.Played || finalItem.UserData.PlayCount != 0 || !finalItem.UserData.IsFavorite || finalItem.UserData.LastPlayedDate == "" {
		t.Fatalf("final UserData did not reflect stopped state: %+v", finalItem.UserData)
	}

	logout := fixture.request(t, "logout-primary", http.MethodPost, fixture.prefix+"/Sessions/Logout", "", primaryToken)
	sequenceRequireStatus(t, logout, http.StatusNoContent)
	revokedMe := fixture.request(t, "revoked-primary", http.MethodGet, fixture.prefix+"/Users/Me", "", primaryToken)
	sequenceRequireStatus(t, revokedMe, http.StatusUnauthorized)
	logoutSecondary := fixture.request(t, "logout-secondary", http.MethodPost, fixture.prefix+"/Sessions/Logout", "", secondaryToken)
	sequenceRequireStatus(t, logoutSecondary, http.StatusNoContent)
	revokedSecondary := fixture.request(t, "revoked-secondary", http.MethodGet, fixture.prefix+"/Users/Me", "", secondaryToken)
	sequenceRequireStatus(t, revokedSecondary, http.StatusUnauthorized)

	invalidAliasPath := "/emby/emby/System/Info/Public"
	if fixture.prefix == "" {
		invalidAliasPath = "/Emby/System/Info/Public"
	}
	invalidAlias := fixture.request(t, "invalid-alias", http.MethodGet, invalidAliasPath, "", "")
	sequenceRequireStatus(t, invalidAlias, http.StatusNotFound)

	fixture.assertNoSecretOutputs(t, primaryToken, secondaryToken)
}

func (fixture *sequenceHTTPFixture) login(t *testing.T, name, username string) *httptest.ResponseRecorder {
	t.Helper()
	body := sequenceJSON(t, map[string]string{"Username": username, "Pw": sequencePassword})
	request := httptest.NewRequest(http.MethodPost, fixture.prefix+"/Users/AuthenticateByName", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Provider-Authorization", sequenceProviderHeader)
	if fixture.client == "VidHub" {
		request.Header.Set("Authorization", `Emby Client="VidHub", Device="Tablet", DeviceId="vidhub-sequence", Version="2.4"`)
	} else {
		request.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Infuse", Device="Living Room", DeviceId="infuse-sequence", Version="8.2"`)
	}
	return fixture.serve(name, request)
}

func (fixture *sequenceHTTPFixture) request(t *testing.T, name, method, target, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("X-Provider-Authorization", sequenceProviderHeader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set(fixture.tokenHeader, token)
	}
	return fixture.serve(name, request)
}

func (fixture *sequenceHTTPFixture) serve(name string, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	fixture.mux.ServeHTTP(response, request)
	fixture.responses = append(fixture.responses, recordedSequenceResponse{name: name, response: response})
	return response
}

func (fixture *sequenceHTTPFixture) assertNoSecretOutputs(t *testing.T, primaryToken, secondaryToken string) {
	t.Helper()
	forbidden := []string{
		"provider.invalid",
		"PROVIDER_IMAGE_TOKEN_SENTINEL",
		sequenceProviderResource,
		sequenceProviderSource,
		sequenceProviderHeader,
		"PROVIDER_NAME_SENTINEL",
		"PROVIDER_ADDON_SENTINEL",
		"PROVIDER_MANIFEST_SENTINEL",
		"PROVIDER_SOURCE_NAME_SENTINEL",
		"provider-secret",
		"native-secret-token",
		"native-asset-id",
		sequencePassword,
		"RUNTIME_SECRET_SENTINEL",
	}
	for _, recorded := range fixture.responses {
		body := recorded.response.Body.String()
		headers := fmt.Sprint(recorded.response.Header())
		redirect := recorded.response.Header().Get("Location")
		for _, secret := range forbidden {
			if strings.Contains(body, secret) || strings.Contains(headers, secret) || strings.Contains(redirect, secret) {
				t.Fatalf("%s disclosed %q in body, headers, or redirect: body=%q headers=%v", recorded.name, secret, body, recorded.response.Header())
			}
		}
		for _, token := range []string{primaryToken, secondaryToken} {
			if strings.Contains(headers, token) || strings.Contains(redirect, token) {
				t.Fatalf("%s disclosed compatibility token in headers or redirect", recorded.name)
			}
			allowedLoginBody := recorded.name == "login-primary" && token == primaryToken || recorded.name == "login-secondary" && token == secondaryToken
			if !allowedLoginBody && strings.Contains(body, token) {
				t.Fatalf("%s disclosed a compatibility token outside its authentication result", recorded.name)
			}
		}
	}
	logs := fixture.logs.String()
	for _, secret := range append(forbidden, primaryToken, secondaryToken) {
		if strings.Contains(logs, secret) {
			t.Fatalf("compatibility logs disclosed %q: %s", secret, logs)
		}
	}
	if !strings.Contains(logs, compatRequestCompletedMessage) || !strings.Contains(logs, `"route"`) || !strings.Contains(logs, `"status"`) {
		t.Fatalf("sequence did not exercise structured route tracing: %s", logs)
	}
}

func sequenceJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode sequence request: %v", err)
	}
	return string(encoded)
}

func sequenceDecode(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode status %d response: %v body=%s", response.Code, err, response.Body.String())
	}
}

func sequenceRequireObjectKeys(t *testing.T, document []byte, keys ...string) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(document, &object); err != nil {
		t.Fatalf("decode response object: %v body=%s", err, document)
	}
	for _, key := range keys {
		if _, exists := object[key]; !exists {
			t.Fatalf("response object is missing %q: %s", key, document)
		}
	}
}

func sequenceRequireArrayObjectKeys(t *testing.T, document []byte, field string, index int, keys ...string) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(document, &object); err != nil {
		t.Fatalf("decode response object: %v body=%s", err, document)
	}
	var values []map[string]json.RawMessage
	if err := json.Unmarshal(object[field], &values); err != nil || index < 0 || index >= len(values) {
		t.Fatalf("response field %q has no object at index %d: %v body=%s", field, index, err, document)
	}
	for _, key := range keys {
		if _, exists := values[index][key]; !exists {
			t.Fatalf("response %s[%d] is missing %q: %s", field, index, key, document)
		}
	}
}

func sequenceRequireStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d, body=%s", response.Code, want, response.Body.String())
	}
}

var (
	_ Authentication   = (*sequenceAuthentication)(nil)
	_ CatalogReader    = (*sequenceCatalog)(nil)
	_ Watchstate       = (*sequenceWatchstate)(nil)
	_ PlaybackDelivery = (*sequencePlaybackDelivery)(nil)
)
