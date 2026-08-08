package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

const (
	catalogTestServerID  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	catalogTestProfileID = "11111111-1111-4111-8111-111111111111"
)

type catalogHTTPAuthentication struct {
	session AuthenticatedSession
}

func (authentication *catalogHTTPAuthentication) Login(context.Context, CompatLoginInput) (LoginResult, error) {
	return LoginResult{}, errors.New("unexpected login")
}

func (authentication *catalogHTTPAuthentication) Authenticate(context.Context, string) (AuthenticatedSession, error) {
	return authentication.session, nil
}

func (*catalogHTTPAuthentication) Logout(context.Context, AuthenticatedSession) error { return nil }

type catalogHTTPReader struct {
	title    watchstate.CatalogTitle
	titleErr error
	page     watchstate.CatalogPage
	pageErr  error
	queries  []watchstate.CatalogQuery
	titleIDs []string
}

func (reader *catalogHTTPReader) GetCatalogTitle(_ context.Context, _ auth.Principal, titleID string) (watchstate.CatalogTitle, error) {
	reader.titleIDs = append(reader.titleIDs, titleID)
	return reader.title, reader.titleErr
}

func (reader *catalogHTTPReader) ListCatalogItems(_ context.Context, _ auth.Principal, query watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
	reader.queries = append(reader.queries, query)
	return reader.page, reader.pageErr
}

func TestCatalogViewsAreStableRootsAndRejectMismatchedUser(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	request := authenticatedCatalogRequest(t, token, "/Users/"+catalogTestProfileID+"/Views")
	request.SetPathValue("id", catalogTestProfileID)
	response := httptest.NewRecorder()
	handler.handleUserViews(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("views status = %d body=%s", response.Code, response.Body.String())
	}
	var result QueryResult[BaseItemDto]
	decodeCatalogResponse(t, response, &result)
	if result.TotalRecordCount != 3 || len(result.Items) != 3 || result.Items[0].Name != "Movies" ||
		result.Items[0].CollectionType != "movies" || result.Items[1].Name != "TV Shows" ||
		result.Items[1].CollectionType != "tvshows" || result.Items[2].Name != "Collections" ||
		result.Items[2].CollectionType != "boxsets" || result.Items[0].Id == result.Items[1].Id ||
		result.Items[1].Id == result.Items[2].Id || result.Items[0].ServerId != catalogTestServerID ||
		result.Items[0].Genres == nil || result.Items[0].BackdropImageTags == nil {
		t.Fatalf("unexpected virtual roots: %+v", result)
	}
	for _, view := range result.Items {
		if view.Etag != view.Id || view.DisplayPreferencesId != view.Id || view.LocationType != "FileSystem" || view.MediaType != "Unknown" ||
			view.ImageTags == nil || len(view.ImageTags) != 0 || view.UserData == nil || view.UserData.Key != view.Id || view.UserData.ItemId != view.Id {
			t.Fatalf("virtual root compatibility fields are incomplete: %+v", view)
		}
	}
	for _, field := range []string{`"Etag"`, `"DisplayPreferencesId"`, `"LocationType"`, `"ImageTags":{}`, `"UserData"`} {
		if !strings.Contains(response.Body.String(), field) {
			t.Fatalf("UserViews JSON omitted %s: %s", field, response.Body.String())
		}
	}
	if len(reader.queries) != 0 || len(reader.titleIDs) != 0 {
		t.Fatalf("virtual roots queried catalog: %+v %+v", reader.queries, reader.titleIDs)
	}

	mismatch := authenticatedCatalogRequest(t, token, "/Users/22222222-2222-4222-8222-222222222222/Views")
	mismatch.SetPathValue("id", "22222222-2222-4222-8222-222222222222")
	mismatchResponse := httptest.NewRecorder()
	handler.handleUserViews(mismatchResponse, mismatch)
	if mismatchResponse.Code != http.StatusNotFound || len(reader.queries) != 0 {
		t.Fatalf("mismatched user status=%d catalog calls=%d", mismatchResponse.Code, len(reader.queries))
	}
}

func TestCatalogItemsTranslateRootAndNeverDiscloseProvenance(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	views, ok := handler.virtualViews()
	if !ok {
		t.Fatal("derive virtual views")
	}
	lastPlayed := time.Date(2026, 8, 6, 12, 0, 0, 123, time.UTC)
	runtimeMinutes := 123
	rating := float32(8.25)
	reader.page = watchstate.CatalogPage{
		Offset: 2, Limit: 1, Total: 5,
		Items: []watchstate.CatalogTitle{{
			ID: "00000000-0000-4000-8000-000000000100", MediaType: "movie", Title: "ÉCLAIR Movie",
			Released: "2025-01-02", Overview: "Résumé", RuntimeMinutes: &runtimeMinutes,
			Genres: []string{"Drama"}, CommunityRating: &rating, InLibrary: true,
			OriginalTitle: "Original Éclair", Tagline: "A bright tagline", Status: "Released",
			People: []watchstate.CatalogPerson{{Name: "Lead Performer", Role: "Hero", Type: "Actor", ImageURL: localizedArtworkPrefix + strings.Repeat("b", 64)}},
			ProviderIDs: map[string]string{
				"imdb": "tt0000100", "tmdb": "100", "tvdb": "200",
				"addon": "profile-secret", "url": "https://provider.invalid/title/100", "unknown": "opaque-secondary",
			},
			PosterURL:     "/api/v1/artwork/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			BackgroundURL: "https://provider.invalid/private.jpg?token=source-secret",
			ResourceID:    "secret-resource", ResourceProvider: "secret-provider", SourceName: "secret-source",
			Progress: &watchstate.CatalogProgress{PositionSeconds: 61, DurationSeconds: 7380, Completed: true, LastWatchedAt: &lastPlayed},
		}},
	}
	target := "/Items?ParentId=" + views[0].Id + "&StartIndex=2&Limit=1&IncludeItemTypes=Movie"
	request := authenticatedCatalogRequest(t, token, target)
	response := httptest.NewRecorder()
	handler.handleItems(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("items status=%d body=%s", response.Code, response.Body.String())
	}
	if len(reader.queries) != 1 {
		t.Fatalf("catalog calls=%d, want one bounded batch", len(reader.queries))
	}
	query := reader.queries[0]
	if query.ParentID != "" || query.Offset != 2 || query.Limit != 1 || len(query.MediaTypes) != 1 || query.MediaTypes[0] != "movie" {
		t.Fatalf("unexpected root query: %+v", query)
	}
	var result QueryResult[BaseItemDto]
	decodeCatalogResponse(t, response, &result)
	if result.TotalRecordCount != 5 || result.StartIndex != 2 || len(result.Items) != 1 {
		t.Fatalf("pagination contract lost: %+v", result)
	}
	item := result.Items[0]
	if item.Type != "Movie" || item.MediaType != "Video" || !item.IsPlayable || item.RunTimeTicks == nil ||
		*item.RunTimeTicks != MinutesToTicks(123) || item.OriginalTitle != "Original Éclair" || len(item.Taglines) != 1 || item.Taglines[0] != "A bright tagline" ||
		item.Status != "Released" || len(item.People) != 1 || item.People[0].Name != "Lead Performer" || item.People[0].Role != "Hero" || item.People[0].PrimaryImageTag != strings.Repeat("b", 64) ||
		item.ProviderIds["Imdb"] != "tt0000100" || item.ProviderIds["Tmdb"] != "100" || item.ProviderIds["Tvdb"] != "200" ||
		item.ImageTags["Primary"] == "" || len(item.BackdropImageTags) != 0 || item.UserData == nil ||
		item.UserData.PlaybackPositionTicks != SecondsToTicks(61) || !item.UserData.Played || item.UserData.PlayCount != 1 {
		t.Fatalf("movie mapping incomplete: %+v", item)
	}
	requireDeferredMediaSource(t, item)
	body := response.Body.String()
	for _, forbidden := range []string{"provider.invalid", "profile-secret", "opaque-secondary", "source-secret", "secret-resource", "secret-provider", "secret-source", "/api/v1/artwork/"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("catalog response disclosed %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"MediaSources"`) || !strings.Contains(body, `"Path":"/Videos/00000000-0000-4000-8000-000000000100/stream.strm"`) {
		t.Fatalf("movie catalogue JSON omitted deferred media fields: %s", body)
	}
}

func TestLatestItemsReturnsJellyfinArrayForVirtualView(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	views, ok := handler.virtualViews()
	if !ok {
		t.Fatal("derive virtual views")
	}
	reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{{
		ID: "00000000-0000-4000-8000-000000000100", MediaType: "movie", Title: "Latest Movie",
		Genres: []string{}, ProviderIDs: map[string]string{},
	}}}
	request := authenticatedCatalogRequest(t, token, "/Items/Latest?userId="+catalogTestProfileID+"&parentId="+views[0].Id+"&limit=16&fields=PrimaryImageAspectRatio&fields=Path")
	response := httptest.NewRecorder()
	handler.handleLatestItems(response, request)
	if response.Code != http.StatusOK || len(reader.queries) != 1 {
		t.Fatalf("latest status=%d calls=%d body=%s", response.Code, len(reader.queries), response.Body.String())
	}
	query := reader.queries[0]
	if query.ParentID != "" || query.Limit != 16 || len(query.MediaTypes) != 1 || query.MediaTypes[0] != "movie" {
		t.Fatalf("unexpected latest query: %+v", query)
	}
	var items []BaseItemDto
	decodeCatalogResponse(t, response, &items)
	if len(items) != 1 || items[0].Name != "Latest Movie" || items[0].Type != "Movie" {
		t.Fatalf("unexpected latest items: %+v", items)
	}
}

func TestCatalogMapsSeasonZeroAndEpisodeDetailInOneRead(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	seasonZero := 0
	episodeOne := 1
	reader.title = watchstate.CatalogTitle{
		ID: "00000000-0000-4000-8000-000000000211", MediaType: "episode",
		ParentID: "00000000-0000-4000-8000-000000000210",
		SeriesID: "00000000-0000-4000-8000-000000000200", SeasonID: "00000000-0000-4000-8000-000000000210",
		Title: "Pilot Special", SeriesTitle: "Canonical Series", SeasonTitle: "Specials",
		Ordinal: &episodeOne, ParentOrdinal: &seasonZero, Released: "2020-03-04",
		Genres: []string{}, ProviderIDs: map[string]string{"tvdb": "2110"},
	}
	request := authenticatedCatalogRequest(t, token, "/Items/00000000-0000-4000-8000-000000000211")
	request.SetPathValue("id", reader.title.ID)
	response := httptest.NewRecorder()
	handler.handleItem(response, request)
	if response.Code != http.StatusOK || len(reader.titleIDs) != 1 {
		t.Fatalf("detail status=%d calls=%d body=%s", response.Code, len(reader.titleIDs), response.Body.String())
	}
	var item BaseItemDto
	decodeCatalogResponse(t, response, &item)
	if item.Type != "Episode" || item.SeriesId != reader.title.SeriesID || item.SeasonId != reader.title.SeasonID ||
		item.IndexNumber == nil || *item.IndexNumber != 1 || item.ParentIndexNumber == nil || *item.ParentIndexNumber != 0 ||
		item.SeriesName != "Canonical Series" || item.SeasonName != "Specials" || item.ProviderIds["Tvdb"] != "2110" {
		t.Fatalf("episode hierarchy mapping incomplete: %+v", item)
	}
	requireDeferredMediaSource(t, item)
	if !strings.Contains(response.Body.String(), `"MediaSources"`) {
		t.Fatalf("episode detail JSON omitted MediaSources: %s", response.Body.String())
	}

	reader.title = watchstate.CatalogTitle{
		ID: "00000000-0000-4000-8000-000000000200", MediaType: "series", Title: "Canonical Series",
		Genres: []string{}, ProviderIDs: map[string]string{"tvdb": "200"},
	}
	seriesRequest := authenticatedCatalogRequest(t, token, "/Items/00000000-0000-4000-8000-000000000200")
	seriesRequest.SetPathValue("id", reader.title.ID)
	seriesResponse := httptest.NewRecorder()
	handler.handleItem(seriesResponse, seriesRequest)
	var series BaseItemDto
	decodeCatalogResponse(t, seriesResponse, &series)
	if seriesResponse.Code != http.StatusOK || series.Type != "Series" || !series.IsFolder || series.Path != "" || len(series.MediaSources) != 0 || strings.Contains(seriesResponse.Body.String(), `"MediaSources"`) {
		t.Fatalf("series detail exposed playable media: status=%d item=%+v body=%s", seriesResponse.Code, series, seriesResponse.Body.String())
	}
}

func TestCatalogSearchIsRecursivePaginatedAndProfileBound(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	reader.page = watchstate.CatalogPage{
		Items:  []watchstate.CatalogTitle{{ID: "00000000-0000-4000-8000-000000000100", MediaType: "movie", Title: "ÉCLAIR", Genres: []string{}, ProviderIDs: map[string]string{}}},
		Offset: 3, Limit: 1, Total: 9,
	}
	request := authenticatedCatalogRequest(t, token, "/Search/Hints?SearchTerm=%C3%A9ClAiR&StartIndex=3&Limit=1&IncludeItemTypes=Movie,Series")
	response := httptest.NewRecorder()
	handler.handleSearchHints(response, request)
	if response.Code != http.StatusOK || len(reader.queries) != 1 {
		t.Fatalf("search status=%d calls=%d body=%s", response.Code, len(reader.queries), response.Body.String())
	}
	query := reader.queries[0]
	if query.SearchTerm != "éClAiR" || !query.Recursive || query.Offset != 3 || query.Limit != 1 || len(query.MediaTypes) != 2 {
		t.Fatalf("unexpected bounded search query: %+v", query)
	}
	var result SearchHintResult
	decodeCatalogResponse(t, response, &result)
	if result.TotalRecordCount != 9 || len(result.SearchHints) != 1 || result.SearchHints[0].Name != "ÉCLAIR" {
		t.Fatalf("search result lost exact total: %+v", result)
	}

	foreign := authenticatedCatalogRequest(t, token, "/Users/22222222-2222-4222-8222-222222222222/Search/Hints?SearchTerm=eclair")
	foreign.SetPathValue("id", "22222222-2222-4222-8222-222222222222")
	foreignResponse := httptest.NewRecorder()
	handler.handleUserSearchHints(foreignResponse, foreign)
	if foreignResponse.Code != http.StatusNotFound || len(reader.queries) != 1 {
		t.Fatalf("foreign search status=%d calls=%d", foreignResponse.Code, len(reader.queries))
	}
}

func TestCatalogSortIsForwardedOrRejected(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{}, Limit: 10}
	request := authenticatedCatalogRequest(t, token, "/Items?Limit=10&SortBy=SortName&SortOrder=Descending")
	response := httptest.NewRecorder()
	handler.handleItems(response, request)
	if response.Code != http.StatusOK || len(reader.queries) != 1 || reader.queries[0].SortBy != "sortname" || reader.queries[0].SortOrder != "descending" {
		t.Fatalf("supported sort status=%d queries=%+v body=%s", response.Code, reader.queries, response.Body.String())
	}

	vidHub := authenticatedCatalogRequest(t, token, "/Items?IncludeItemTypes=Series,Movie,Video,MusicVideo&SortBy=DateLastContentAdded,DateCreated,SortName&SortOrder=Descending")
	vidHubResponse := httptest.NewRecorder()
	handler.handleItems(vidHubResponse, vidHub)
	if vidHubResponse.Code != http.StatusOK || len(reader.queries) != 2 || reader.queries[1].SortBy != "" || reader.queries[1].SortOrder != "" ||
		!reflect.DeepEqual(reader.queries[1].MediaTypes, []string{"movie", "series"}) {
		t.Fatalf("VidHub query status=%d queries=%+v body=%s", vidHubResponse.Code, reader.queries, vidHubResponse.Body.String())
	}

	videoOnly := authenticatedCatalogRequest(t, token, "/Items?IncludeItemTypes=Video,MusicVideo")
	videoOnlyResponse := httptest.NewRecorder()
	handler.handleItems(videoOnlyResponse, videoOnly)
	if videoOnlyResponse.Code != http.StatusOK || len(reader.queries) != 2 {
		t.Fatalf("unprojected filter status=%d queries=%+v body=%s", videoOnlyResponse.Code, reader.queries, videoOnlyResponse.Body.String())
	}

	standardKinds := authenticatedCatalogRequest(t, token, "/Items?IncludeItemTypes=Movie,Audio,Folder,Trailer")
	standardKindsResponse := httptest.NewRecorder()
	handler.handleItems(standardKindsResponse, standardKinds)
	if standardKindsResponse.Code != http.StatusOK || len(reader.queries) != 3 ||
		!reflect.DeepEqual(reader.queries[2].MediaTypes, []string{"movie"}) {
		t.Fatalf("standard unprojected kinds status=%d queries=%+v body=%s", standardKindsResponse.Code, reader.queries, standardKindsResponse.Body.String())
	}

	for _, query := range []string{"?SortBy=CommunityRating&SortOrder=Ascending", "?SortOrder=Descending"} {
		request = authenticatedCatalogRequest(t, token, "/Items"+query)
		response = httptest.NewRecorder()
		handler.handleItems(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("standard sort %q status=%d body=%s", query, response.Code, response.Body.String())
		}
	}
	remuxSorts := authenticatedCatalogRequest(t, token, "/Items?SortBy=Default,AiredEpisodeOrder,DigitalReleaseDate,IsPlayed,VideoBitRate,AirTime,Studio,ParentIndexNumber,IndexNumber,SimilarityScore,SearchScore,ChannelOrder,CatalogOrder,DisplayOrder,PopularityAllTime,PopularityDay,PopularityWeek,PopularityMonth,TrendingWeek,TrendingMonth")
	remuxSortsResponse := httptest.NewRecorder()
	handler.handleItems(remuxSortsResponse, remuxSorts)
	if remuxSortsResponse.Code != http.StatusOK || len(reader.queries) != 6 || reader.queries[5].SortBy != "" || reader.queries[5].SortOrder != "" {
		t.Fatalf("Remux-compatible sorts status=%d queries=%+v body=%s", remuxSortsResponse.Code, reader.queries, remuxSortsResponse.Body.String())
	}
	request = authenticatedCatalogRequest(t, token, "/Items?SortBy=PrivateProviderURL")
	response = httptest.NewRecorder()
	handler.handleItems(response, request)
	if response.Code != http.StatusBadRequest || len(reader.queries) != 6 {
		t.Fatalf("unknown sort status=%d queries=%+v body=%s", response.Code, reader.queries, response.Body.String())
	}
}

func TestCatalogRejectionLogsOnlyClosedStage(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	var logs bytes.Buffer
	handler.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	privateValue := "PrivateProviderURLSecret"
	request := authenticatedCatalogRequest(t, token, "/Items?IncludeItemTypes="+privateValue)
	response := httptest.NewRecorder()
	handler.handleItems(response, request)
	if response.Code != http.StatusBadRequest || len(reader.queries) != 0 {
		t.Fatalf("rejected query status=%d calls=%d", response.Code, len(reader.queries))
	}
	logged := logs.String()
	if !strings.Contains(logged, `"msg":"`+compatCatalogRejectedMessage+`"`) ||
		!strings.Contains(logged, `"stage":"include_item_types"`) || strings.Contains(logged, privateValue) {
		t.Fatalf("unsafe or missing catalog diagnostic: %s", logged)
	}
}

func TestEpisodesSeasonSelectorIsValidatedAndIsolated(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	seriesID := "00000000-0000-4000-8000-000000000200"
	seasonID := "00000000-0000-4000-8000-000000000210"
	reader.title = watchstate.CatalogTitle{ID: seasonID, MediaType: "season", SeriesID: seriesID, Title: "Season"}
	reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{}, Limit: 10}
	request := authenticatedCatalogRequest(t, token, "/Shows/"+seriesID+"/Episodes?SeasonId="+seasonID+"&Limit=10")
	request.SetPathValue("seriesId", seriesID)
	response := httptest.NewRecorder()
	handler.handleEpisodes(response, request)
	if response.Code != http.StatusOK || len(reader.titleIDs) != 1 || len(reader.queries) != 1 || reader.queries[0].ParentID != seasonID || reader.queries[0].Recursive {
		t.Fatalf("season episodes status=%d titles=%+v queries=%+v body=%s", response.Code, reader.titleIDs, reader.queries, response.Body.String())
	}

	reader.title.SeriesID = "00000000-0000-4000-8000-000000000999"
	request = authenticatedCatalogRequest(t, token, "/Shows/"+seriesID+"/Episodes?SeasonId="+seasonID)
	request.SetPathValue("seriesId", seriesID)
	response = httptest.NewRecorder()
	handler.handleEpisodes(response, request)
	if response.Code != http.StatusNotFound || len(reader.queries) != 1 {
		t.Fatalf("foreign season status=%d queries=%+v", response.Code, reader.queries)
	}
}

func TestCatalogInaccessibleParentIsAnEmptyExactPage(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{}, Offset: 0, Limit: 20, Total: 0}
	request := authenticatedCatalogRequest(t, token, "/Items?ParentId=22222222-2222-4222-8222-222222222222&Limit=20")
	response := httptest.NewRecorder()
	handler.handleItems(response, request)
	if response.Code != http.StatusOK || len(reader.queries) != 1 || reader.queries[0].ParentID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("inaccessible parent status=%d queries=%+v body=%s", response.Code, reader.queries, response.Body.String())
	}
	var result QueryResult[BaseItemDto]
	decodeCatalogResponse(t, response, &result)
	if result.TotalRecordCount != 0 || result.Items == nil || len(result.Items) != 0 {
		t.Fatalf("empty page shape=%+v", result)
	}
}

func TestJellyfinProviderIDsAllowOnlyValidatedTitleIdentities(t *testing.T) {
	projected := jellyfinProviderIDs(map[string]string{
		"imdb": "tt1234567", "tmdb": "42", "tvdb": "7",
		"addon": "private", "url": "https://provider.invalid/title", "wikidata": "Q42",
	})
	want := map[string]string{"Imdb": "tt1234567", "Tmdb": "42", "Tvdb": "7"}
	if !reflect.DeepEqual(projected, want) {
		t.Fatalf("provider projection = %#v, want %#v", projected, want)
	}
	for _, values := range []map[string]string{
		{"imdb": "https://provider.invalid/tt123"},
		{"imdb": "TT123"},
		{"imdb": "tt"},
		{"imdb": "tt12345678901234567"},
		{"tmdb": "0"},
		{"tmdb": "-1"},
		{"tmdb": "1.5"},
		{"tvdb": "https://provider.invalid/7"},
		{"tvdb": "1234567890123456789"},
	} {
		if result := jellyfinProviderIDs(values); len(result) != 0 {
			t.Fatalf("invalid provider identity %#v projected as %#v", values, result)
		}
	}
}

func requireDeferredMediaSource(t *testing.T, item BaseItemDto) {
	t.Helper()
	if len(item.MediaSources) != 1 {
		t.Fatalf("item has %d deferred sources, want exactly one: %+v", len(item.MediaSources), item)
	}
	source := item.MediaSources[0]
	expectedPath := "/Videos/" + item.Id + "/stream"
	if source.Id != item.Id || source.Name != item.Name || source.Path != expectedPath || item.Path != expectedPath+".strm" ||
		source.Protocol != "File" || source.Type != "Default" || source.IsRemote || !source.SupportsDirectPlay ||
		!source.SupportsDirectStream || !source.SupportsTranscoding {
		t.Fatalf("deferred source contract is incomplete: item=%+v source=%+v", item, source)
	}
	if (source.RunTimeTicks == nil) != (item.RunTimeTicks == nil) || source.RunTimeTicks != nil && *source.RunTimeTicks != *item.RunTimeTicks {
		t.Fatalf("deferred source runtime differs from item: item=%v source=%v", item.RunTimeTicks, source.RunTimeTicks)
	}
}

func newCatalogHTTPHandler(t *testing.T) (*Handler, *catalogHTTPReader, string) {
	t.Helper()
	serverID, err := ParseServerID(catalogTestServerID)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := newCompatCredential()
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	profileID := catalogTestProfileID
	session := AuthenticatedSession{
		ID: "22222222-2222-4222-8222-222222222222", ProfileID: profileID, ProfileName: "Viewer", ExpiresAt: expiresAt,
		Principal: auth.Principal{SessionID: "33333333-3333-4333-8333-333333333333", UserID: "44444444-4444-4444-8444-444444444444", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt},
	}
	reader := &catalogHTTPReader{}
	return &Handler{serverInfo: ServerInfo{ID: serverID, Name: "Rivune"}, authentication: &catalogHTTPAuthentication{session: session}, catalog: reader}, reader, token
}

func authenticatedCatalogRequest(t *testing.T, token, target string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("X-Emby-Token", token)
	return request
}

func decodeCatalogResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode catalog response: %v body=%s", err, response.Body.String())
	}
}
