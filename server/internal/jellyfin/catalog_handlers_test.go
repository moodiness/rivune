package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
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
	err     error
}

func (authentication *catalogHTTPAuthentication) Login(context.Context, CompatLoginInput) (LoginResult, error) {
	return LoginResult{}, errors.New("unexpected login")
}

func (authentication *catalogHTTPAuthentication) Authenticate(context.Context, string) (AuthenticatedSession, error) {
	if authentication.err != nil {
		return AuthenticatedSession{}, authentication.err
	}
	return authentication.session, nil
}
func (authentication *catalogHTTPAuthentication) Revalidate(context.Context, AuthenticatedSession) (AuthenticatedSession, error) {
	return authentication.session, nil
}

func (*catalogHTTPAuthentication) Logout(context.Context, AuthenticatedSession) error { return nil }

type catalogHTTPReader struct {
	title    watchstate.CatalogTitle
	titles   map[string]watchstate.CatalogTitle
	titleErr error
	page     watchstate.CatalogPage
	pageErr  error
	pageFunc func(watchstate.CatalogQuery) (watchstate.CatalogPage, error)
	queries  []watchstate.CatalogQuery
	titleIDs []string
}

func (reader *catalogHTTPReader) GetCatalogTitle(_ context.Context, _ auth.Principal, titleID string) (watchstate.CatalogTitle, error) {
	reader.titleIDs = append(reader.titleIDs, titleID)
	if title, ok := reader.titles[titleID]; ok {
		return title, nil
	}
	return reader.title, reader.titleErr
}

func (reader *catalogHTTPReader) ListCatalogItems(_ context.Context, _ auth.Principal, query watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
	reader.queries = append(reader.queries, query)
	if reader.pageFunc != nil {
		return reader.pageFunc(query)
	}
	return reader.page, reader.pageErr
}

type catalogHTTPDetailReader struct {
	*catalogHTTPReader
	enrichedTitle  watchstate.CatalogTitle
	enrichmentRead []watchstate.CatalogTitle
}

func (reader *catalogHTTPDetailReader) EnrichCatalogTitle(_ context.Context, _ auth.Principal, title watchstate.CatalogTitle) (watchstate.CatalogTitle, error) {
	reader.enrichmentRead = append(reader.enrichmentRead, title)
	return reader.enrichedTitle, nil
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
	if result.TotalRecordCount != 2 || len(result.Items) != 2 || result.Items[0].Name != "Movies" ||
		result.Items[0].CollectionType != "movies" || result.Items[1].Name != "TV Shows" ||
		result.Items[1].CollectionType != "tvshows" || result.Items[0].Id == result.Items[1].Id ||
		result.Items[0].ServerId != catalogTestServerID || result.Items[0].Genres == nil || result.Items[0].BackdropImageTags == nil {
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
	compactUser := authenticatedCatalogRequest(t, token, "/UserViews?UserId="+strings.ReplaceAll(catalogTestProfileID, "-", ""))
	compactResponse := httptest.NewRecorder()
	handler.handleViews(compactResponse, compactUser)
	if compactResponse.Code != http.StatusOK {
		t.Fatalf("compact query UserId status=%d body=%s", compactResponse.Code, compactResponse.Body.String())
	}

	mismatch := authenticatedCatalogRequest(t, token, "/Users/22222222-2222-4222-8222-222222222222/Views")
	mismatch.SetPathValue("id", "22222222-2222-4222-8222-222222222222")
	mismatchResponse := httptest.NewRecorder()
	handler.handleUserViews(mismatchResponse, mismatch)
	if mismatchResponse.Code != http.StatusNotFound || len(reader.queries) != 0 {
		t.Fatalf("mismatched user status=%d catalog calls=%d", mismatchResponse.Code, len(reader.queries))
	}
}

func TestVirtualViewDetailsDoNotReadCatalog(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	views, ok := handler.virtualViews()
	if !ok {
		t.Fatal("derive virtual views")
	}
	for _, view := range views {
		request := authenticatedCatalogRequest(t, token, "/Items/"+view.Id)
		request.SetPathValue("id", view.Id)
		response := httptest.NewRecorder()
		handler.handleItem(response, request)
		var item BaseItemDto
		decodeCatalogResponse(t, response, &item)
		if response.Code != http.StatusOK || item.Id != view.Id || item.CollectionType != view.CollectionType || !item.IsFolder {
			t.Fatalf("virtual detail status=%d item=%+v", response.Code, item)
		}
	}
	if len(reader.titleIDs) != 0 || len(reader.queries) != 0 {
		t.Fatalf("virtual details reached catalog: titles=%v queries=%+v", reader.titleIDs, reader.queries)
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
			Genres: []string{"Drama"}, CommunityRating: &rating, InLibrary: false, Favorite: true,
			OriginalTitle: "Original Éclair", Tagline: "A bright tagline", Status: "Released",
			People: []watchstate.CatalogPerson{{ID: "9301", Name: "Lead Performer", Role: "Hero", Type: "Actor", ImageURL: localizedArtworkPrefix + strings.Repeat("b", 64)}},
			ProviderIDs: map[string]string{
				"imdb": "tt0000100", "tmdb": "100", "tvdb": "200",
				"addon": "profile-secret", "url": "https://provider.invalid/title/100", "unknown": "opaque-secondary",
			},
			PosterURL:     localizedArtworkPrefix + strings.Repeat("a", 64),
			BackgroundURL: localizedArtworkPrefix + strings.Repeat("c", 64),
			ResourceID:    "secret-resource", ResourceProvider: "secret-provider", SourceName: "secret-source",
			Progress: &watchstate.CatalogProgress{PositionSeconds: 61, DurationSeconds: 7380, Completed: true, LastWatchedAt: &lastPlayed},
		}},
	}
	target := "/Items?ParentId=" + views[0].Id + "&StartIndex=2&Limit=1&IncludeItemTypes=Movie&Fields=Etag,Genres,MediaSources,MediaStreams,OriginalTitle,Overview,ParentId,Path,People,PrimaryImageAspectRatio,ProviderIds,SortName,Taglines"
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
	if item.Type != "Movie" || item.MediaType != "Video" || !item.IsPlayable || item.PrimaryImageAspectRatio != 2.0/3.0 || item.RunTimeTicks == nil ||
		*item.RunTimeTicks != MinutesToTicks(123) || item.OriginalTitle != "Original Éclair" || len(item.Taglines) != 1 || item.Taglines[0] != "A bright tagline" ||
		item.Status != "Released" || len(item.People) != 1 || item.People[0].Name != "Lead Performer" || item.People[0].Role != "Hero" || item.People[0].PrimaryImageTag != strings.Repeat("b", 64) ||
		item.ProviderIds["Imdb"] != "tt0000100" || item.ProviderIds["Tmdb"] != "100" || item.ProviderIds["Tvdb"] != "200" ||
		item.ImageTags["Primary"] != strings.Repeat("a", 64) || len(item.BackdropImageTags) != 1 || item.BackdropImageTags[0] != strings.Repeat("c", 64) || item.UserData == nil ||
		!item.UserData.IsFavorite || item.UserData.PlaybackPositionTicks != SecondsToTicks(61) || !item.UserData.Played || item.UserData.PlayCount != 1 {
		t.Fatalf("movie mapping incomplete: %+v", item)
	}
	requireDeferredMediaSource(t, item)
	body := response.Body.String()
	for _, forbidden := range []string{"provider.invalid", "profile-secret", "opaque-secondary", "source-secret", "secret-resource", "secret-provider", "secret-source", "/api/v1/artwork/"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("catalog response disclosed %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"MediaSources"`) || !strings.Contains(body, `"Path":"/rivune/00000000-0000-4000-8000-000000000100/00000000-0000-4000-8000-000000000100.strm"`) {
		t.Fatalf("movie catalogue JSON omitted file-like media fields: %s", body)
	}
}

func TestCatalogGeneralListsOmitUnrequestedOptionalFields(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	views, ok := handler.virtualViews()
	if !ok {
		t.Fatal("derive virtual views")
	}
	rating := float32(8.5)
	reader.page = watchstate.CatalogPage{
		Items: []watchstate.CatalogTitle{{
			ID: "00000000-0000-4000-8000-000000000100", MediaType: "movie",
			ParentID: "00000000-0000-4000-8000-000000000099", Title: "Projected Movie",
			OriginalTitle: "Original", Overview: "Requested overview", Genres: []string{"Drama"},
			Studios: []string{"Studio One"}, CommunityRating: &rating, Tagline: "Hidden tagline",
			People:      []watchstate.CatalogPerson{{Name: "Hidden Person", Type: "Actor"}},
			ProviderIDs: map[string]string{"tmdb": "100"},
		}},
		Limit: 20, Total: 1,
	}
	request := authenticatedCatalogRequest(t, token, "/Items?ParentId="+views[0].Id+"&IncludeItemTypes=Movie&Fields=Overview")
	response := httptest.NewRecorder()
	handler.handleItems(response, request)
	var payload struct {
		Items []map[string]json.RawMessage `json:"Items"`
	}
	decodeCatalogResponse(t, response, &payload)
	if response.Code != http.StatusOK || len(payload.Items) != 1 {
		t.Fatalf("general-list projection status=%d payload=%+v body=%s", response.Code, payload, response.Body.String())
	}
	item := payload.Items[0]
	if _, ok := item["Overview"]; !ok {
		t.Fatalf("requested Overview absent: %s", response.Body.String())
	}
	for _, field := range []string{
		"DisplayPreferencesId", "Etag", "Genres", "MediaSources", "OriginalTitle", "ParentId",
		"Path", "People", "PrimaryImageAspectRatio", "ProviderIds", "SortName", "Studios", "Taglines",
	} {
		if _, present := item[field]; present {
			t.Fatalf("unrequested %s present: %s", field, response.Body.String())
		}
	}
}

func TestUserDataIsFieldForFieldEqualAcrossCatalogAndContinuationSurfaces(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	itemID := "00000000-0000-4000-8000-000000000111"
	seriesID := "00000000-0000-4000-8000-000000000100"
	seasonID := "00000000-0000-4000-8000-000000000110"
	progressDate := time.Date(2026, 8, 1, 2, 3, 4, 0, time.UTC)
	persistedDate := time.Date(2026, 8, 2, 3, 4, 5, 600_000_000, time.UTC)
	rating, percentage, unplayed, playCount, likes := 8.75, 37.25, 12, 0, true
	title := watchstate.CatalogTitle{
		ID: itemID, MediaType: "episode", Title: "Episode", SeriesID: seriesID, SeasonID: seasonID, Favorite: true,
		Progress: &watchstate.CatalogProgress{
			PositionSeconds: 25, DurationSeconds: 100, Completed: true, LastWatchedAt: &progressDate,
		},
		UserData: &watchstate.UserDataValues{
			Rating: &rating, RatingSet: true, PlayedPercentage: &percentage, PlayedPercentageSet: true,
			UnplayedItemCount: &unplayed, UnplayedItemCountSet: true, PlayCount: &playCount, PlayCountSet: true,
			Likes: &likes, LikesSet: true, LastPlayedDate: &persistedDate, LastPlayedDateSet: true,
		},
	}
	reader.title = title
	reader.titles = map[string]watchstate.CatalogTitle{itemID: title}
	reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{title}, Limit: 1, Total: 1}
	state := newMemoryWatchstate()
	state.resumePage = watchstate.ContinueItemsPage{Items: []watchstate.ContinueItem{{TitleID: itemID}}, Limit: 1, Total: 1}
	state.nextPage = watchstate.ContinueItemsPage{Items: []watchstate.ContinueItem{{TitleID: itemID, SeriesID: seriesID, SeasonID: seasonID}}, Limit: 1, Total: 1}
	handler.watchstate = state

	listResponse := httptest.NewRecorder()
	handler.handleItems(listResponse, authenticatedCatalogRequest(t, token, "/Items?Ids="+url.QueryEscape(itemID)+"&Limit=1"))
	var list QueryResult[BaseItemDto]
	decodeCatalogResponse(t, listResponse, &list)

	detailRequest := authenticatedCatalogRequest(t, token, "/Items/"+itemID)
	detailRequest.SetPathValue("id", itemID)
	detailResponse := httptest.NewRecorder()
	handler.handleItem(detailResponse, detailRequest)
	var detail BaseItemDto
	decodeCatalogResponse(t, detailResponse, &detail)

	resumeRequest := authenticatedCatalogRequest(t, token, "/Users/"+catalogTestProfileID+"/Items/Resume?Limit=1")
	resumeRequest.SetPathValue("id", catalogTestProfileID)
	resumeResponse := httptest.NewRecorder()
	handler.handleResumeItems(resumeResponse, resumeRequest)
	var resume QueryResult[BaseItemDto]
	decodeCatalogResponse(t, resumeResponse, &resume)

	nextResponse := httptest.NewRecorder()
	handler.handleNextUp(nextResponse, authenticatedCatalogRequest(t, token, "/Shows/NextUp?Limit=1"))
	var next QueryResult[BaseItemDto]
	decodeCatalogResponse(t, nextResponse, &next)

	if listResponse.Code != http.StatusOK || detailResponse.Code != http.StatusOK ||
		resumeResponse.Code != http.StatusOK || nextResponse.Code != http.StatusOK ||
		len(list.Items) != 1 || len(resume.Items) != 1 || len(next.Items) != 1 {
		t.Fatalf("surface responses list=%d/%+v detail=%d resume=%d/%+v next=%d/%+v",
			listResponse.Code, list, detailResponse.Code, resumeResponse.Code, resume, nextResponse.Code, next)
	}
	want := list.Items[0].UserData
	for name, got := range map[string]*UserItemDataDto{
		"detail": detail.UserData, "resume": resume.Items[0].UserData, "next-up": next.Items[0].UserData,
	} {
		if want == nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s UserData=%+v, catalog UserData=%+v", name, got, want)
		}
	}
	if want.PlayedPercentage == nil || *want.PlayedPercentage != percentage || want.PlayCount != 0 ||
		want.LastPlayedDate != "2026-08-02T03:04:05.6000000Z" {
		t.Fatalf("persisted UserData was not authoritative: %+v", want)
	}
}

func TestInfuseCatalogBootstrapAcceptsOfficialAndLegacyJellyfinQueryVocabulary(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	itemID := "00000000-0000-4000-8000-000000000111"
	title := watchstate.CatalogTitle{ID: itemID, MediaType: "movie", Title: "Infuse bootstrap"}
	reader.title = title
	reader.titles = map[string]watchstate.CatalogTitle{itemID: title}
	reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{title}, Limit: 1, Total: 1}
	state := newMemoryWatchstate()
	state.resumePage = watchstate.ContinueItemsPage{Items: []watchstate.ContinueItem{{TitleID: itemID}}, Limit: 1, Total: 1}
	state.nextPage = watchstate.ContinueItemsPage{Items: []watchstate.ContinueItem{{TitleID: itemID}}, Limit: 1, Total: 1}
	handler.watchstate = state

	fields := "BasicSyncInfo,CanDownload,Chapters,ChildCount,PrimaryImageAspectRatio,FutureSdkField"
	parentID := "00000000-0000-4000-8000-000000000222"
	requests := []struct {
		name    string
		target  string
		prepare func(*http.Request)
		handle  func(http.ResponseWriter, *http.Request)
	}{
		{name: "latest", target: "/Items/Latest?UserId=" + catalogTestProfileID + "&Fields=" + fields + "&IncludeItemTypes=Movie&Limit=1&GroupItems=true", handle: handler.handleLatestItems},
		{name: "legacy latest", target: "/Users/" + catalogTestProfileID + "/Items/Latest?Fields=" + fields + "&IncludeItemTypes=Movie&Limit=1&GroupItems=true", prepare: func(request *http.Request) { request.SetPathValue("id", catalogTestProfileID) }, handle: handler.handleUserLatestItems},
		{name: "detail", target: "/Items/" + itemID + "?UserId=" + catalogTestProfileID + "&Fields=" + fields, prepare: func(request *http.Request) { request.SetPathValue("id", itemID) }, handle: handler.handleItem},
		{name: "resume", target: "/UserItems/Resume?UserId=" + catalogTestProfileID + "&StartIndex=0&Limit=1&SearchTerm=Infuse&ParentId=" + parentID + "&Fields=" + fields + "&MediaTypes=Video&IncludeItemTypes=Movie,Episode&ExcludeItemTypes=Audio&EnableTotalRecordCount=false&EnableImages=true&ExcludeActiveSessions=false&Recursive=true", handle: handler.handleUserResumeItems},
		{name: "legacy resume", target: "/Users/" + catalogTestProfileID + "/Items/Resume?StartIndex=0&Limit=1&SearchTerm=Infuse&ParentId=" + parentID + "&Fields=" + fields + "&MediaTypes=Video&IncludeItemTypes=Movie,Episode&ExcludeItemTypes=Audio&EnableTotalRecordCount=false&EnableImages=true&ExcludeActiveSessions=false&Recursive=true", prepare: func(request *http.Request) { request.SetPathValue("id", catalogTestProfileID) }, handle: handler.handleResumeItems},
		{name: "next up", target: "/Shows/NextUp?UserId=" + catalogTestProfileID + "&ParentId=" + parentID + "&StartIndex=0&Limit=1&Fields=" + fields + "&MediaTypes=Video&NextUpDateCutoff=2026-01-01T00:00:00Z&EnableTotalRecordCount=false&DisableFirstEpisode=false&EnableResumable=true&EnableRewatching=false", handle: handler.handleNextUp},
	}
	for _, test := range requests {
		t.Run(test.name, func(t *testing.T) {
			request := authenticatedCatalogRequest(t, token, test.target)
			if test.prepare != nil {
				test.prepare(request)
			}
			response := httptest.NewRecorder()
			test.handle(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestBaseItemDTOAdvertisesOnlyLocalizedPosterAndBackdrop(t *testing.T) {
	handler, _, _ := newCatalogHTTPHandler(t)
	posterTag := strings.Repeat("d", 64)
	backdropTag := strings.Repeat("e", 64)
	for _, mediaType := range []string{"movie", "series", "episode"} {
		t.Run(mediaType, func(t *testing.T) {
			item := handler.baseItemDTO(watchstate.CatalogTitle{
				ID: "00000000-0000-4000-8000-000000000101", MediaType: mediaType, Title: "Artwork title",
				PosterURL: localizedArtworkPrefix + posterTag, BackgroundURL: localizedArtworkPrefix + backdropTag,
			}, false)
			if item.ImageTags["Primary"] != posterTag || len(item.BackdropImageTags) != 1 || item.BackdropImageTags[0] != backdropTag {
				t.Fatalf("%s artwork tags=%#v backdrops=%#v", mediaType, item.ImageTags, item.BackdropImageTags)
			}

			unservable := handler.baseItemDTO(watchstate.CatalogTitle{
				ID: "00000000-0000-4000-8000-000000000102", MediaType: mediaType, Title: "Unservable artwork",
				PosterURL: "https://provider.invalid/private-poster.jpg", BackgroundURL: "https://provider.invalid/private-backdrop.jpg",
			}, false)
			if len(unservable.ImageTags) != 0 || len(unservable.BackdropImageTags) != 0 {
				t.Fatalf("%s advertised unservable artwork: tags=%#v backdrops=%#v", mediaType, unservable.ImageTags, unservable.BackdropImageTags)
			}
		})
	}
}

func TestCatalogItemsSelectRequestedTitleBeforeRootViews(t *testing.T) {
	for _, mediaType := range []string{"movie", "series", "episode"} {
		t.Run(mediaType, func(t *testing.T) {
			handler, reader, token := newCatalogHTTPHandler(t)
			reader.title = watchstate.CatalogTitle{
				ID: "00000000-0000-4000-8000-000000000100", MediaType: mediaType, Title: "Selected title",
				Genres: []string{}, ProviderIDs: map[string]string{},
			}
			request := authenticatedCatalogRequest(t, token, "/Items?Ids="+reader.title.ID+"&UserId="+catalogTestProfileID+"&Fields=Overview,MediaSources,MediaStreams,Path")
			response := httptest.NewRecorder()
			handler.handleItems(response, request)
			var result QueryResult[BaseItemDto]
			decodeCatalogResponse(t, response, &result)
			if response.Code != http.StatusOK || result.TotalRecordCount != 1 || len(result.Items) != 1 ||
				result.Items[0].Id != reader.title.ID || len(reader.titleIDs) != 1 || len(reader.queries) != 0 {
				t.Fatalf("selected detail status=%d result=%+v titleReads=%v listReads=%+v", response.Code, result, reader.titleIDs, reader.queries)
			}
			if mediaType == "series" {
				if result.Items[0].Type != "Series" || len(result.Items[0].MediaSources) != 0 {
					t.Fatalf("series selection=%+v", result.Items[0])
				}
				return
			}
			if result.Items[0].Type != strings.ToUpper(mediaType[:1])+mediaType[1:] {
				t.Fatalf("selected media type=%+v", result.Items[0])
			}
			requireDeferredMediaSource(t, result.Items[0])
		})
	}
}

func TestCatalogItemsDoNotWidenDisjointCollectionFolderFilters(t *testing.T) {
	for _, target := range []string{
		"/Items?IncludeItemTypes=CollectionFolder&MediaTypes=Video",
		"/Items?IncludeItemTypes=CollectionFolder&Filters=IsNotFolder",
	} {
		handler, reader, token := newCatalogHTTPHandler(t)
		response := httptest.NewRecorder()
		handler.handleItems(response, authenticatedCatalogRequest(t, token, target))
		var result QueryResult[BaseItemDto]
		decodeCatalogResponse(t, response, &result)
		if response.Code != http.StatusOK || result.TotalRecordCount != 0 || len(result.Items) != 0 || len(reader.queries) != 0 || len(reader.titleIDs) != 0 {
			t.Fatalf("target=%q status=%d result=%+v queries=%+v titles=%v", target, response.Code, result, reader.queries, reader.titleIDs)
		}
	}
}

func TestCatalogItemsIDsUseOrdinaryMetadataFilterSemantics(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	id := "00000000-0000-4000-8000-000000000100"
	reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{{
		ID: id, MediaType: "movie", Title: "Filtered ID", People: []watchstate.CatalogPerson{{ID: "9301", Name: "Alice Actor"}},
	}}, Total: 1, Limit: 100}
	request := authenticatedCatalogRequest(t, token, "/Items?Ids="+id+"&OfficialRatings=PG-13&Tags=Featured&PersonIds=9301")
	response := httptest.NewRecorder()
	handler.handleItems(response, request)
	var result QueryResult[BaseItemDto]
	decodeCatalogResponse(t, response, &result)
	if response.Code != http.StatusOK || len(result.Items) != 1 || result.Items[0].Id != id || len(reader.titleIDs) != 0 || len(reader.queries) != 2 {
		t.Fatalf("status=%d result=%+v titleReads=%v queries=%+v", response.Code, result, reader.titleIDs, reader.queries)
	}
	query := reader.queries[1]
	if !query.Recursive || !reflect.DeepEqual(query.IDs, []string{id}) || !reflect.DeepEqual(query.OfficialRatings, []string{"PG-13"}) ||
		!reflect.DeepEqual(query.Tags, []string{"Featured"}) || !reflect.DeepEqual(query.PersonIDs, []string{"9301"}) {
		t.Fatalf("metadata-filtered ID query=%+v", query)
	}
}

func TestCatalogItemsLimitZeroReturnsCountWithoutZeroLimitRead(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	reader.page = watchstate.CatalogPage{
		Items:  []watchstate.CatalogTitle{{ID: "00000000-0000-4000-8000-000000000100", MediaType: "movie", Title: "Count sentinel"}},
		Offset: 5, Limit: 1, Total: 9,
	}
	request := authenticatedCatalogRequest(t, token, "/Items?ParentId=22222222-2222-4222-8222-222222222222&StartIndex=5&Limit=0")
	response := httptest.NewRecorder()
	handler.handleItems(response, request)
	var result QueryResult[BaseItemDto]
	decodeCatalogResponse(t, response, &result)
	if response.Code != http.StatusOK || result.TotalRecordCount != 9 || result.StartIndex != 5 || len(result.Items) != 0 {
		t.Fatalf("count-only status=%d result=%+v", response.Code, result)
	}
	if len(reader.queries) != 1 || reader.queries[0].Limit != 1 {
		t.Fatalf("count-only catalog query=%+v", reader.queries)
	}

	rootRequest := authenticatedCatalogRequest(t, token, "/Items?Limit=0")
	rootResponse := httptest.NewRecorder()
	handler.handleItems(rootResponse, rootRequest)
	var root QueryResult[BaseItemDto]
	decodeCatalogResponse(t, rootResponse, &root)
	if rootResponse.Code != http.StatusOK || root.TotalRecordCount != 2 || root.StartIndex != 0 || len(root.Items) != 0 {
		t.Fatalf("count-only root status=%d result=%+v", rootResponse.Code, root)
	}
	if len(reader.queries) != 1 {
		t.Fatalf("count-only root reached catalog: %+v", reader.queries)
	}
}

func TestCatalogItemsDisableTotalsAfterFiltering(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	views, ok := handler.virtualViews()
	if !ok {
		t.Fatal("derive virtual views")
	}
	reader.page = watchstate.CatalogPage{
		Items:  []watchstate.CatalogTitle{{ID: "00000000-0000-4000-8000-000000000100", MediaType: "movie", Title: "Filtered"}},
		Offset: 0, Limit: 1, Total: 9,
	}
	request := authenticatedCatalogRequest(t, token, "/Items?ParentId="+views[0].Id+"&IncludeItemTypes=Movie&Limit=1&EnableTotalRecordCount=false")
	response := httptest.NewRecorder()
	handler.handleItems(response, request)
	var result QueryResult[BaseItemDto]
	decodeCatalogResponse(t, response, &result)
	if response.Code != http.StatusOK || result.TotalRecordCount != 0 || len(result.Items) != 1 || len(reader.queries) != 1 || !reader.queries[0].OmitTotal {
		t.Fatalf("ordinary disabled total status=%d result=%+v queries=%+v", response.Code, result, reader.queries)
	}

	rootRequest := authenticatedCatalogRequest(t, token, "/Items?Limit=1&EnableTotalRecordCount=false&EnableUserData=false")
	rootResponse := httptest.NewRecorder()
	handler.handleItems(rootResponse, rootRequest)
	var root QueryResult[BaseItemDto]
	decodeCatalogResponse(t, rootResponse, &root)
	if rootResponse.Code != http.StatusOK || root.TotalRecordCount != 0 || len(root.Items) != 1 || root.Items[0].UserData != nil || len(reader.queries) != 1 {
		t.Fatalf("root disabled flags status=%d result=%+v queries=%+v", rootResponse.Code, root, reader.queries)
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

func TestCatalogItemsAggregatesRequestedLimitInBoundedPages(t *testing.T) {
	for _, userRoute := range []bool{false, true} {
		name := "items"
		if userRoute {
			name = "user_items"
		}
		t.Run(name, func(t *testing.T) {
			handler, reader, token := newCatalogHTTPHandler(t)
			const start = 17
			const requested = MaximumLatestQueryLimit
			const total = start + requested
			reader.pageFunc = func(query watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
				count := min(query.Limit, total-query.Offset)
				items := make([]watchstate.CatalogTitle, count)
				for index := range items {
					items[index] = watchstate.CatalogTitle{
						ID: strconv.Itoa(query.Offset + index), MediaType: "movie", Title: strconv.Itoa(query.Offset + index),
						Genres: []string{}, ProviderIDs: map[string]string{},
					}
				}
				return watchstate.CatalogPage{Items: items, Offset: query.Offset, Limit: query.Limit, Total: total}, nil
			}
			target := "/Items?IncludeItemTypes=Movie&StartIndex=17&Limit=" + strconv.Itoa(requested)
			request := authenticatedCatalogRequest(t, token, target)
			if userRoute {
				request = authenticatedCatalogRequest(t, token, "/Users/"+catalogTestProfileID+target)
				request.SetPathValue("id", catalogTestProfileID)
			}
			response := httptest.NewRecorder()
			if userRoute {
				handler.handleUserItems(response, request)
			} else {
				handler.handleItems(response, request)
			}
			var result QueryResult[BaseItemDto]
			decodeCatalogResponse(t, response, &result)
			if response.Code != http.StatusOK || len(result.Items) != requested || result.StartIndex != start || result.TotalRecordCount != total {
				t.Fatalf("status=%d items=%d start=%d total=%d body=%s", response.Code, len(result.Items), result.StartIndex, result.TotalRecordCount, response.Body.String())
			}
			for index, item := range result.Items {
				want := strconv.Itoa(start + index)
				if item.Id != want || item.Name != want {
					t.Fatalf("item[%d]=%+v, want id/name %q", index, item, want)
				}
			}
			if len(reader.queries) != 6 {
				t.Fatalf("catalog calls=%d, want 6: %+v", len(reader.queries), reader.queries)
			}
			for index, query := range reader.queries {
				wantOffset := start + index*MaximumQueryLimit
				wantLimit := min(MaximumQueryLimit, requested-index*MaximumQueryLimit)
				if query.Offset != wantOffset || query.Limit != wantLimit || query.Limit > MaximumQueryLimit {
					t.Fatalf("query[%d]=%+v, want offset=%d limit=%d", index, query, wantOffset, wantLimit)
				}
			}
		})
	}
}

func TestListCatalogItemsStopsSafelyOnShortAndChangingPages(t *testing.T) {
	tests := []struct {
		name      string
		page      func(watchstate.CatalogQuery) watchstate.CatalogPage
		wantItems int
		wantTotal int
		wantCalls int
	}{
		{
			name: "short page",
			page: func(query watchstate.CatalogQuery) watchstate.CatalogPage {
				count := query.Limit
				if query.Offset == 200 {
					count = 37
				}
				return catalogPaginationPage(query, count, 1000)
			},
			wantItems: 237, wantTotal: 1000, wantCalls: 2,
		},
		{
			name: "oversized page",
			page: func(query watchstate.CatalogQuery) watchstate.CatalogPage {
				return catalogPaginationPage(query, query.Limit+50, 300)
			},
			wantItems: 300, wantTotal: 300, wantCalls: 2,
		},
		{
			name: "total below returned pages",
			page: func(query watchstate.CatalogQuery) watchstate.CatalogPage {
				return catalogPaginationPage(query, query.Limit, 250)
			},
			wantItems: 250, wantTotal: 250, wantCalls: 2,
		},
		{
			name: "changing total remains stable",
			page: func(query watchstate.CatalogQuery) watchstate.CatalogPage {
				total := 450
				if query.Offset == 200 {
					total = 100
				} else if query.Offset > 200 {
					total = 900
				}
				page := catalogPaginationPage(query, query.Limit, total)
				page.Offset = -1
				return page
			},
			wantItems: 450, wantTotal: 450, wantCalls: 3,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, reader, _ := newCatalogHTTPHandler(t)
			reader.pageFunc = func(query watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
				return test.page(query), nil
			}
			page, err := handler.listCatalogItems(context.Background(), auth.Principal{}, watchstate.CatalogQuery{Limit: MaximumQueryLimit}, MaximumLatestQueryLimit)
			if err != nil {
				t.Fatalf("listCatalogItems: %v", err)
			}
			if len(page.Items) != test.wantItems || page.Offset != 0 || page.Limit != MaximumLatestQueryLimit || page.Total != test.wantTotal || len(reader.queries) != test.wantCalls {
				t.Fatalf("page items=%d offset=%d limit=%d total=%d calls=%d", len(page.Items), page.Offset, page.Limit, page.Total, len(reader.queries))
			}
		})
	}
}

func TestListCatalogItemsReturnsLateErrorsAndCancellationWithoutPartialPage(t *testing.T) {
	lateErr := errors.New("late catalog failure")
	t.Run("late error", func(t *testing.T) {
		handler, reader, _ := newCatalogHTTPHandler(t)
		reader.pageFunc = func(query watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
			if query.Offset != 0 {
				return watchstate.CatalogPage{}, lateErr
			}
			return catalogPaginationPage(query, query.Limit, 1000), nil
		}
		page, err := handler.listCatalogItems(context.Background(), auth.Principal{}, watchstate.CatalogQuery{Limit: MaximumQueryLimit}, MaximumLatestQueryLimit)
		if !errors.Is(err, lateErr) || len(page.Items) != 0 || len(reader.queries) != 2 {
			t.Fatalf("page=%+v err=%v calls=%d", page, err, len(reader.queries))
		}
	})
	t.Run("handler does not emit partial success", func(t *testing.T) {
		handler, reader, token := newCatalogHTTPHandler(t)
		reader.pageFunc = func(query watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
			if query.Offset != 0 {
				return watchstate.CatalogPage{}, lateErr
			}
			return catalogPaginationPage(query, query.Limit, 1000), nil
		}
		response := httptest.NewRecorder()
		handler.handleItems(response, authenticatedCatalogRequest(t, token, "/Items?IncludeItemTypes=Movie&Limit="+strconv.Itoa(MaximumLatestQueryLimit)))
		if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), `"Items"`) || len(reader.queries) != 2 {
			t.Fatalf("status=%d calls=%d body=%s", response.Code, len(reader.queries), response.Body.String())
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		handler, reader, _ := newCatalogHTTPHandler(t)
		ctx, cancel := context.WithCancel(context.Background())
		reader.pageFunc = func(query watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
			cancel()
			return catalogPaginationPage(query, query.Limit, 1000), nil
		}
		page, err := handler.listCatalogItems(ctx, auth.Principal{}, watchstate.CatalogQuery{Limit: MaximumQueryLimit}, MaximumLatestQueryLimit)
		if !errors.Is(err, context.Canceled) || len(page.Items) != 0 || len(reader.queries) != 1 {
			t.Fatalf("page=%+v err=%v calls=%d", page, err, len(reader.queries))
		}
	})
}

func TestCatalogItemsCountOnlyAndSmallLimitsKeepSingleRead(t *testing.T) {
	for _, limit := range []int{0, 200} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			handler, reader, token := newCatalogHTTPHandler(t)
			reader.page = catalogPaginationPage(watchstate.CatalogQuery{Limit: max(limit, 1)}, max(limit, 1), 750)
			response := httptest.NewRecorder()
			handler.handleItems(response, authenticatedCatalogRequest(t, token, "/Items?IncludeItemTypes=Movie&Limit="+strconv.Itoa(limit)))
			var result QueryResult[BaseItemDto]
			decodeCatalogResponse(t, response, &result)
			wantItems := limit
			if limit == 0 {
				wantItems = 0
			}
			if response.Code != http.StatusOK || len(reader.queries) != 1 || len(result.Items) != wantItems || result.TotalRecordCount != 750 || reader.queries[0].Limit != max(limit, 1) {
				t.Fatalf("status=%d result items=%d total=%d queries=%+v", response.Code, len(result.Items), result.TotalRecordCount, reader.queries)
			}
		})
	}
}

func catalogPaginationPage(query watchstate.CatalogQuery, count, total int) watchstate.CatalogPage {
	items := make([]watchstate.CatalogTitle, count)
	for index := range items {
		items[index] = watchstate.CatalogTitle{
			ID: strconv.Itoa(query.Offset + index), MediaType: "movie", Title: strconv.Itoa(query.Offset + index),
			Genres: []string{}, ProviderIDs: map[string]string{},
		}
	}
	return watchstate.CatalogPage{Items: items, Offset: query.Offset, Limit: query.Limit, Total: total}
}

func TestLatestItemsStreamsPinnedClientLimitsInBoundedCatalogChunks(t *testing.T) {
	for _, requested := range []int{1000, MaximumLatestQueryLimit} {
		t.Run(strconv.Itoa(requested), func(t *testing.T) {
			handler, reader, token := newCatalogHTTPHandler(t)
			views, ok := handler.virtualViews()
			if !ok {
				t.Fatal("derive virtual views")
			}
			reader.pageFunc = func(query watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
				count := min(query.Limit, 1200-query.Offset)
				items := make([]watchstate.CatalogTitle, count)
				for index := range items {
					items[index] = watchstate.CatalogTitle{
						ID: "00000000-0000-4000-8000-000000000100", MediaType: "movie", Title: "Latest Movie",
						Genres: []string{}, ProviderIDs: map[string]string{},
					}
				}
				return watchstate.CatalogPage{Items: items, Offset: query.Offset, Limit: query.Limit, Total: 1200}, nil
			}
			request := authenticatedCatalogRequest(t, token, "/Items/Latest?UserId="+catalogTestProfileID+"&ParentId="+views[0].Id+"&StartIndex=7&Limit="+strconv.Itoa(requested))
			response := httptest.NewRecorder()
			handler.handleLatestItems(response, request)
			var items []BaseItemDto
			decodeCatalogResponse(t, response, &items)
			expectedCalls := (requested + MaximumQueryLimit - 1) / MaximumQueryLimit
			if response.Code != http.StatusOK || len(items) != requested || len(reader.queries) != expectedCalls {
				t.Fatalf("Limit=%d status=%d items=%d queries=%+v body=%s", requested, response.Code, len(items), reader.queries, response.Body.String())
			}
			for index, query := range reader.queries {
				if query.Limit > MaximumQueryLimit || query.Offset != 7+index*MaximumQueryLimit {
					t.Fatalf("Limit=%d query[%d]=%+v", requested, index, query)
				}
			}
		})
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
	if result.TotalRecordCount != 9 || len(result.SearchHints) != 1 || result.SearchHints[0].Name != "ÉCLAIR" ||
		result.SearchHints[0].Artists == nil || result.SearchHints[0].ChannelId != nil ||
		result.SearchHints[0].PrimaryImageAspectRatio != 2.0/3.0 ||
		!strings.Contains(response.Body.String(), `"Artists":[]`) || !strings.Contains(response.Body.String(), `"ChannelId":null`) {
		t.Fatalf("search result lost exact total or observed DTO shape: %+v body=%s", result, response.Body.String())
	}

	foreign := authenticatedCatalogRequest(t, token, "/Users/22222222-2222-4222-8222-222222222222/Search/Hints?SearchTerm=eclair")
	foreign.SetPathValue("id", "22222222-2222-4222-8222-222222222222")
	foreignResponse := httptest.NewRecorder()
	handler.handleUserSearchHints(foreignResponse, foreign)
	if foreignResponse.Code != http.StatusNotFound || len(reader.queries) != 1 {
		t.Fatalf("foreign search status=%d calls=%d", foreignResponse.Code, len(reader.queries))
	}
}

func TestCatalogSortForwardsSupportedKeysAndIgnoresUnknownHints(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{}, Limit: 10}
	tests := []struct {
		key   string
		order string
		want  string
	}{
		{key: "SortName", order: "Descending", want: "sortname"},
		{key: "Name", order: "Ascending", want: "sortname"},
		{key: "SortName,SortName,ProductionYear", order: "Ascending", want: "sortname,sortname,productionyear"},
		{key: "DateCreated,SortName,ProductionYear", order: "Descending", want: "datecreated,sortname,productionyear"},
		{key: "DateLastContentAdded,DateCreated,SortName", order: "Descending", want: "datelastcontentadded,datecreated,sortname"},
		{key: "CommunityRating", order: "Descending", want: "communityrating"},
	}
	for index, test := range tests {
		request := authenticatedCatalogRequest(t, token, "/Items?ParentId=22222222-2222-4222-8222-222222222222&Limit=10&SortBy="+test.key+"&SortOrder="+test.order)
		response := httptest.NewRecorder()
		handler.handleItems(response, request)
		if response.Code != http.StatusOK || len(reader.queries) != index+1 {
			t.Fatalf("supported sort %s status=%d queries=%+v body=%s", test.key, response.Code, reader.queries, response.Body.String())
		}
		query := reader.queries[index]
		if query.SortBy != test.want || query.SortOrder != strings.ToLower(test.order) {
			t.Fatalf("supported sort %s status=%d query=%+v body=%s", test.key, response.Code, query, response.Body.String())
		}
	}
	supported := len(tests)
	reader.page.Offset = 4

	videoOnly := authenticatedCatalogRequest(t, token, "/Items?IncludeItemTypes=Video,MusicVideo&StartIndex=4")
	videoOnlyResponse := httptest.NewRecorder()
	handler.handleItems(videoOnlyResponse, videoOnly)
	var videoResult QueryResult[BaseItemDto]
	decodeCatalogResponse(t, videoOnlyResponse, &videoResult)
	if videoOnlyResponse.Code != http.StatusOK || len(reader.queries) != supported+1 ||
		!reflect.DeepEqual(reader.queries[supported].MediaTypes, []string{"video"}) ||
		videoResult.Items == nil || len(videoResult.Items) != 0 || videoResult.TotalRecordCount != 0 || videoResult.StartIndex != 4 {
		t.Fatalf("typed-empty Video projection status=%d queries=%+v result=%+v body=%s", videoOnlyResponse.Code, reader.queries, videoResult, videoOnlyResponse.Body.String())
	}

	standardKinds := authenticatedCatalogRequest(t, token, "/Items?ParentId=22222222-2222-4222-8222-222222222222&IncludeItemTypes=Movie,Audio,Folder,Trailer")
	standardKindsResponse := httptest.NewRecorder()
	handler.handleItems(standardKindsResponse, standardKinds)
	if standardKindsResponse.Code != http.StatusOK || len(reader.queries) != supported+2 ||
		!reflect.DeepEqual(reader.queries[supported+1].MediaTypes, []string{"movie"}) {
		t.Fatalf("standard unprojected kinds status=%d queries=%+v body=%s", standardKindsResponse.Code, reader.queries, standardKindsResponse.Body.String())
	}

	fallbacks := []struct {
		query string
		want  string
	}{
		{query: "&SortOrder=Descending"},
		{query: "&SortBy=Default,AiredEpisodeOrder,DigitalReleaseDate"},
		{query: "&SortBy=PrivateProviderURL"},
		{query: "&SortBy=SortName,ProductionYear,DateCreated,Name&SortOrder=Ascending", want: "sortname,productionyear,datecreated"},
	}
	for index, test := range fallbacks {
		request := authenticatedCatalogRequest(t, token, "/Items?ParentId=22222222-2222-4222-8222-222222222222"+test.query)
		response := httptest.NewRecorder()
		handler.handleItems(response, request)
		queryIndex := supported + 2 + index
		if response.Code != http.StatusOK || len(reader.queries) != queryIndex+1 || reader.queries[queryIndex].SortBy != test.want {
			t.Fatalf("fallback sort %q status=%d queries=%+v body=%s", test.query, response.Code, reader.queries, response.Body.String())
		}
	}
}

func TestCatalogProjectsCanonicalGenericVideoRows(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	reader.page = watchstate.CatalogPage{
		Items: []watchstate.CatalogTitle{{
			ID: "00000000-0000-4000-8000-000000000500", MediaType: "video", Title: "Generic Video",
			Genres: []string{}, ProviderIDs: map[string]string{},
		}},
		Limit: 20, Total: 1,
	}
	request := authenticatedCatalogRequest(t, token, "/Items?IncludeItemTypes=Video&Fields=Path,MediaSources")
	response := httptest.NewRecorder()
	handler.handleItems(response, request)
	var result QueryResult[BaseItemDto]
	decodeCatalogResponse(t, response, &result)
	if response.Code != http.StatusOK || len(reader.queries) != 1 || !reflect.DeepEqual(reader.queries[0].MediaTypes, []string{"video"}) ||
		result.TotalRecordCount != 1 || len(result.Items) != 1 || result.Items[0].Type != "Video" ||
		result.Items[0].MediaType != "Video" || !result.Items[0].IsPlayable || !result.Items[0].CanDownload {
		t.Fatalf("generic Video projection status=%d queries=%+v result=%+v body=%s", response.Code, reader.queries, result, response.Body.String())
	}
	requireDeferredMediaSource(t, result.Items[0])
}

func TestCatalogCommunityRatingSortMatchesARVIOOrdering(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	ratingNine, ratingFive := float32(9), float32(5)
	firstNine := "00000000-0000-4000-8000-000000000001"
	secondNine := "00000000-0000-4000-8000-000000000002"
	five := "00000000-0000-4000-8000-000000000003"
	missing := "00000000-0000-4000-8000-000000000004"
	reader.titles = map[string]watchstate.CatalogTitle{
		firstNine:  {ID: firstNine, MediaType: "movie", Title: "First Nine", CommunityRating: &ratingNine},
		secondNine: {ID: secondNine, MediaType: "movie", Title: "Second Nine", CommunityRating: &ratingNine},
		five:       {ID: five, MediaType: "movie", Title: "Five", CommunityRating: &ratingFive},
		missing:    {ID: missing, MediaType: "movie", Title: "Missing"},
	}
	ids := strings.Join([]string{secondNine, missing, five, firstNine}, ",")
	for _, test := range []struct {
		query       string
		want        []string
		wantRatings []float32
	}{
		{query: "SortBy=CommunityRating&SortOrder=Descending", want: []string{firstNine, secondNine, five, missing}, wantRatings: []float32{9, 9, 5}},
		{query: "SortBy=CommunityRating&SortOrder=Ascending", want: []string{five, firstNine, secondNine, missing}, wantRatings: []float32{5, 9, 9}},
	} {
		request := authenticatedCatalogRequest(t, token, "/Items?Ids="+ids+"&"+test.query)
		response := httptest.NewRecorder()
		handler.handleItems(response, request)
		var result QueryResult[BaseItemDto]
		decodeCatalogResponse(t, response, &result)
		got := make([]string, len(result.Items))
		for index := range result.Items {
			got[index] = result.Items[index].Id
		}
		gotRatings := make([]float32, 0, len(result.Items))
		for index := range result.Items {
			if result.Items[index].CommunityRating != nil {
				gotRatings = append(gotRatings, *result.Items[index].CommunityRating)
			}
		}
		if response.Code != http.StatusOK || !reflect.DeepEqual(got, test.want) ||
			!reflect.DeepEqual(gotRatings, test.wantRatings) || result.Items[len(result.Items)-1].CommunityRating != nil {
			t.Fatalf("%s status=%d ids=%v ratings=%v items=%+v body=%s", test.query, response.Code, got, gotRatings, result.Items, response.Body.String())
		}
	}
}

func TestCatalogSortUsesStableIDTieBreaker(t *testing.T) {
	baseline := []watchstate.CatalogTitle{
		{ID: "00000000-0000-4000-8000-000000000002", Title: "Same"},
		{ID: "00000000-0000-4000-8000-000000000003", Title: "Zulu"},
		{ID: "00000000-0000-4000-8000-000000000001", Title: "same"},
	}
	for _, test := range []struct {
		order string
		want  []string
	}{
		{order: "Ascending", want: []string{baseline[2].ID, baseline[0].ID, baseline[1].ID}},
		{order: "Descending", want: []string{baseline[1].ID, baseline[2].ID, baseline[0].ID}},
	} {
		items := append([]watchstate.CatalogTitle(nil), baseline...)
		sortCatalogSearch(items, test.order)
		got := make([]string, len(items))
		for index := range items {
			got[index] = items[index].ID
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("order=%s got=%v want=%v", test.order, got, test.want)
		}
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
	reader.titles = map[string]watchstate.CatalogTitle{
		seriesID: {ID: seriesID, MediaType: "series", Title: "Series"},
		seasonID: {ID: seasonID, MediaType: "season", SeriesID: seriesID, Title: "Season"},
	}
	reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{}, Limit: 10}
	request := authenticatedCatalogRequest(t, token, "/Shows/"+seriesID+"/Episodes?SeasonId="+seasonID+"&Limit=10")
	request.SetPathValue("seriesId", seriesID)
	response := httptest.NewRecorder()
	handler.handleEpisodes(response, request)
	if response.Code != http.StatusOK || len(reader.titleIDs) != 2 || len(reader.queries) != 1 || reader.queries[0].ParentID != seasonID || reader.queries[0].Recursive || !reader.queries[0].IncludePeople {
		t.Fatalf("season episodes status=%d titles=%+v queries=%+v body=%s", response.Code, reader.titleIDs, reader.queries, response.Body.String())
	}

	foreignSeason := reader.titles[seasonID]
	foreignSeason.SeriesID = "00000000-0000-4000-8000-000000000999"
	reader.titles[seasonID] = foreignSeason
	request = authenticatedCatalogRequest(t, token, "/Shows/"+seriesID+"/Episodes?SeasonId="+seasonID)
	request.SetPathValue("seriesId", seriesID)
	response = httptest.NewRecorder()
	handler.handleEpisodes(response, request)
	if response.Code != http.StatusNotFound || len(reader.queries) != 1 {
		t.Fatalf("foreign season status=%d queries=%+v", response.Code, reader.queries)
	}
}

func TestEpisodesRejectsMissingOrNonSeriesRootBeforeListing(t *testing.T) {
	seriesID := "00000000-0000-4000-8000-000000000200"
	tests := []struct {
		name  string
		title watchstate.CatalogTitle
		err   error
	}{
		{name: "missing", err: watchstate.ErrNotFound},
		{name: "wrong type", title: watchstate.CatalogTitle{ID: seriesID, MediaType: "movie"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, reader, token := newCatalogHTTPHandler(t)
			reader.title, reader.titleErr = test.title, test.err
			request := authenticatedCatalogRequest(t, token, "/Shows/"+seriesID+"/Episodes")
			request.SetPathValue("seriesId", seriesID)
			response := httptest.NewRecorder()
			handler.handleEpisodes(response, request)
			if response.Code != http.StatusNotFound || len(reader.titleIDs) != 1 || len(reader.queries) != 0 {
				t.Fatalf("status=%d titleIDs=%v queries=%v body=%s", response.Code, reader.titleIDs, reader.queries, response.Body.String())
			}
		})
	}
}

func TestHierarchyRoutesIgnoreUnusedQueryParametersLikeJellyfin(t *testing.T) {
	seriesID := "00000000-0000-4000-8000-000000000200"
	for _, target := range []struct {
		name   string
		path   string
		handle func(*Handler, http.ResponseWriter, *http.Request)
	}{
		{name: "seasons", path: "/Shows/" + seriesID + "/Seasons?excludeLocationTypes=Virtual&fields=Overview,FutureSdkField&limit=100&startIndex=0", handle: (*Handler).handleSeasons},
		{name: "episodes", path: "/Shows/" + seriesID + "/Episodes?Recursive=true", handle: (*Handler).handleEpisodes},
	} {
		t.Run(target.name, func(t *testing.T) {
			handler, reader, token := newCatalogHTTPHandler(t)
			reader.title = watchstate.CatalogTitle{ID: seriesID, MediaType: "series"}
			reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{}, Offset: 0, Limit: 100, Total: 0}
			request := authenticatedCatalogRequest(t, token, target.path)
			request.SetPathValue("seriesId", seriesID)
			response := httptest.NewRecorder()
			target.handle(handler, response, request)
			if response.Code != http.StatusOK || len(reader.titleIDs) != 1 || len(reader.queries) != 1 {
				t.Fatalf("status=%d titles=%v queries=%v body=%s", response.Code, reader.titleIDs, reader.queries, response.Body.String())
			}
		})
	}
}

func TestSeasonsDoesNotWriteServerErrorAfterClientCancellation(t *testing.T) {
	seriesID := "00000000-0000-4000-8000-000000000200"
	handler, reader, token := newCatalogHTTPHandler(t)
	reader.titleErr = context.Canceled
	request := authenticatedCatalogRequest(t, token, "/Shows/"+seriesID+"/Seasons?excludeLocationTypes=Virtual")
	canceledContext, cancel := context.WithCancel(request.Context())
	cancel()
	request = request.WithContext(canceledContext)
	request.SetPathValue("seriesId", seriesID)
	response := httptest.NewRecorder()
	handler.handleSeasons(response, request)
	if response.Body.Len() != 0 || response.Header().Get("Content-Type") != "" || strings.Contains(response.Body.String(), "InternalServerError") {
		t.Fatalf("canceled Seasons wrote an error response: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestSeasonsDoesNotWriteServerErrorWhenAuthenticationIsCanceled(t *testing.T) {
	seriesID := "00000000-0000-4000-8000-000000000200"
	handler, _, token := newCatalogHTTPHandler(t)
	handler.authentication.(*catalogHTTPAuthentication).err = context.Canceled
	request := authenticatedCatalogRequest(t, token, "/Shows/"+seriesID+"/Seasons?excludeLocationTypes=Virtual")
	canceledContext, cancel := context.WithCancel(request.Context())
	cancel()
	request = request.WithContext(canceledContext)
	request.SetPathValue("seriesId", seriesID)
	response := httptest.NewRecorder()
	handler.handleSeasons(response, request)
	if response.Body.Len() != 0 || response.Header().Get("Content-Type") != "" || strings.Contains(response.Body.String(), "InternalError") {
		t.Fatalf("canceled Seasons authentication wrote an error response: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
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

func TestCatalogItemsForwardObservedFilterMatrixBeforePagination(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{{
		ID: "00000000-0000-4000-8000-000000000100", MediaType: "movie", Title: "Filtered",
		Genres: []string{"Drama"}, People: []watchstate.CatalogPerson{{ID: "9301", Name: "Alice Actor"}},
	}}, Total: 1}
	target := "/Items?UserId=" + catalogTestProfileID +
		"&IncludeItemTypes=Movie,Series&ExcludeItemTypes=Series&MediaTypes=Video&Filters=IsFavorite,IsResumable" +
		"&IsPlayed=false&Genres=Drama%7CComedy&GenreIds=18&Years=2025&OfficialRatings=PG-13%7CTV-MA&Tags=Featured%7CClassic&PersonIds=9301" +
		"&Studios=Unavailable%7COther&MinCommunityRating=8.25&HasSubtitles=true&HasTrailer=true&EnableImages=false&EnableImageTypes=Primary,Backdrop&ImageTypeLimit=0" +
		"&EnableTotalRecordCount=false&StartIndex=3&Limit=1008"
	request := authenticatedCatalogRequest(t, token, target)
	response := httptest.NewRecorder()
	handler.handleItems(response, request)
	if response.Code != http.StatusOK || len(reader.queries) != 2 || len(reader.titleIDs) != 0 {
		t.Fatalf("filter matrix status=%d queries=%+v titles=%v body=%s", response.Code, reader.queries, reader.titleIDs, response.Body.String())
	}
	query := reader.queries[1]
	if query.ParentID != "" || !reflect.DeepEqual(query.MediaTypes, []string{"movie"}) || query.Offset != 3 || query.Limit != MaximumQueryLimit ||
		query.Played == nil || *query.Played || query.Favorite == nil || !*query.Favorite || query.Resumable == nil || !*query.Resumable ||
		query.MinCommunityRating == nil || *query.MinCommunityRating != 8.25 || query.HasSubtitles == nil || !*query.HasSubtitles ||
		!reflect.DeepEqual(query.Genres, []string{"Drama", "Comedy"}) || !reflect.DeepEqual(query.GenreIDs, []string{"18"}) ||
		!reflect.DeepEqual(query.Years, []int{2025}) || !reflect.DeepEqual(query.OfficialRatings, []string{"PG-13", "TV-MA"}) ||
		!reflect.DeepEqual(query.Tags, []string{"Featured", "Classic"}) || !reflect.DeepEqual(query.PersonIDs, []string{"9301"}) ||
		!reflect.DeepEqual(query.Studios, []string{"Unavailable", "Other"}) || !query.UnavailableDataFilter || !query.OmitTotal {
		t.Fatalf("unexpected forwarded catalog query: %+v", query)
	}
}

func TestCatalogItemsRejectInvalidOrDuplicateScalarFiltersBeforeCatalogAccess(t *testing.T) {
	for _, query := range []string{
		"MinCommunityRating=8&MinCommunityRating=9",
		"MinCommunityRating=-0.1",
		"MinCommunityRating=NaN",
		"HasSubtitles=true&HasSubtitles=false",
		"HasSubtitles=sometimes",
	} {
		handler, reader, token := newCatalogHTTPHandler(t)
		request := authenticatedCatalogRequest(t, token, "/Items?"+query)
		response := httptest.NewRecorder()
		handler.handleItems(response, request)
		if response.Code != http.StatusBadRequest || len(reader.queries) != 0 || len(reader.titleIDs) != 0 {
			t.Fatalf("query %q status=%d queries=%+v titles=%v body=%s", query, response.Code, reader.queries, reader.titleIDs, response.Body.String())
		}
	}
}

func TestCatalogFieldAndImageGatingIsConsistentForListsAndDetails(t *testing.T) {
	title := watchstate.CatalogTitle{
		ID: "00000000-0000-4000-8000-000000000100", MediaType: "movie", Title: "Fields", Overview: "Heavy overview",
		Genres: []string{"Drama"}, People: []watchstate.CatalogPerson{{ID: "9301", Name: "Alice Actor"}},
		PosterURL: localizedArtworkPrefix + strings.Repeat("a", 64), BackgroundURL: localizedArtworkPrefix + strings.Repeat("b", 64),
	}
	for _, test := range []struct {
		name, fields                          string
		wantOverview, wantPeople, wantSources bool
	}{
		{name: "absent"},
		{name: "overview", fields: "Overview", wantOverview: true},
		{name: "people", fields: "People", wantPeople: true},
		{name: "media sources", fields: "MediaSources", wantSources: true},
		{name: "media streams", fields: "MediaStreams", wantSources: true},
	} {
		t.Run("list "+test.name, func(t *testing.T) {
			handler, reader, token := newCatalogHTTPHandler(t)
			views, _ := handler.virtualViews()
			reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{title}, Total: 1, Limit: 20}
			target := "/Items?ParentId=" + views[0].Id + "&IncludeItemTypes=Movie&Limit=20"
			if test.fields != "" {
				target += "&Fields=" + url.QueryEscape(test.fields)
			}
			response := httptest.NewRecorder()
			handler.handleItems(response, authenticatedCatalogRequest(t, token, target))
			var result QueryResult[BaseItemDto]
			decodeCatalogResponse(t, response, &result)
			if response.Code != http.StatusOK || len(result.Items) != 1 || len(reader.queries) != 1 || len(reader.titleIDs) != 0 {
				t.Fatalf("status=%d result=%+v queries=%+v titles=%v", response.Code, result, reader.queries, reader.titleIDs)
			}
			item := result.Items[0]
			if (item.Overview != "") != test.wantOverview || (len(item.People) != 0) != test.wantPeople || (len(item.MediaSources) != 0) != test.wantSources {
				t.Fatalf("fields=%q item=%+v", test.fields, item)
			}
		})
	}
	for _, test := range []struct {
		name, query                           string
		wantOverview, wantPeople, wantSources bool
	}{
		{name: "absent", wantOverview: true, wantPeople: true, wantSources: true},
		{name: "overview only", query: "?Fields=Overview", wantOverview: true},
		{name: "people only without images", query: "?Fields=People&EnableImages=false", wantPeople: true},
	} {
		t.Run("detail "+test.name, func(t *testing.T) {
			handler, reader, token := newCatalogHTTPHandler(t)
			reader.title = title
			request := authenticatedCatalogRequest(t, token, "/Items/"+title.ID+test.query)
			request.SetPathValue("id", title.ID)
			response := httptest.NewRecorder()
			handler.handleItem(response, request)
			var item BaseItemDto
			decodeCatalogResponse(t, response, &item)
			if response.Code != http.StatusOK || len(reader.titleIDs) != 1 || (item.Overview != "") != test.wantOverview ||
				(len(item.People) != 0) != test.wantPeople || (len(item.MediaSources) != 0) != test.wantSources {
				t.Fatalf("status=%d item=%+v titleReads=%v", response.Code, item, reader.titleIDs)
			}
			if strings.Contains(test.query, "EnableImages=false") && (len(item.ImageTags) != 0 || len(item.BackdropImageTags) != 0) {
				t.Fatalf("disabled images survived: %+v", item)
			}
		})
	}
}

func TestCatalogDetailEnrichesRequestedMetadataForBothRoutes(t *testing.T) {
	snapshot := watchstate.CatalogTitle{
		ID: "00000000-0000-4000-8000-000000000100", MediaType: "movie", Title: "Snapshot",
		Genres: []string{}, ProviderIDs: map[string]string{},
	}
	for _, route := range []struct {
		name       string
		userScoped bool
	}{
		{name: "item"},
		{name: "user item", userScoped: true},
	} {
		for _, field := range []string{"", "OriginalTitle", "Taglines", "People"} {
			name := route.name
			if field == "" {
				name += " default fields"
			} else {
				name += " " + field
			}
			t.Run(name, func(t *testing.T) {
				handler, reader, token := newCatalogHTTPHandler(t)
				reader.title = snapshot
				enriched := snapshot
				enriched.Overview = "Provider overview"
				enriched.People = []watchstate.CatalogPerson{{Name: "Provider performer", Type: "Actor"}}
				switch field {
				case "":
					enriched.OriginalTitle = "Provider original title"
					enriched.Tagline = "Provider tagline"
				case "OriginalTitle":
					enriched.OriginalTitle = "Provider original title"
				case "Taglines":
					enriched.Tagline = "Provider tagline"
				}
				detailReader := &catalogHTTPDetailReader{catalogHTTPReader: reader, enrichedTitle: enriched}
				handler.catalog = detailReader

				target := "/Items/" + snapshot.ID
				if route.userScoped {
					target = "/Users/" + catalogTestProfileID + "/Items/" + snapshot.ID
				}
				if field != "" {
					target += "?Fields=" + field
				}
				request := authenticatedCatalogRequest(t, token, target)
				if route.userScoped {
					request.SetPathValue("userId", catalogTestProfileID)
					request.SetPathValue("itemId", snapshot.ID)
				} else {
					request.SetPathValue("id", snapshot.ID)
				}
				response := httptest.NewRecorder()
				if route.userScoped {
					handler.handleUserItem(response, request)
				} else {
					handler.handleItem(response, request)
				}

				var item BaseItemDto
				decodeCatalogResponse(t, response, &item)
				if response.Code != http.StatusOK || len(detailReader.enrichmentRead) != 1 || !reflect.DeepEqual(detailReader.enrichmentRead[0], snapshot) {
					t.Fatalf("status=%d enrichment=%+v item=%+v", response.Code, detailReader.enrichmentRead, item)
				}
				switch field {
				case "":
					if item.OriginalTitle != "Provider original title" || !reflect.DeepEqual(item.Taglines, []string{"Provider tagline"}) ||
						item.Overview != "Provider overview" || len(item.People) != 1 {
						t.Fatalf("default detail metadata was not enriched: %+v", item)
					}
				case "OriginalTitle":
					if item.OriginalTitle != "Provider original title" || item.Overview != "" || len(item.People) != 0 {
						t.Fatalf("OriginalTitle projection was not enriched and reduced: %+v", item)
					}
				case "Taglines":
					if !reflect.DeepEqual(item.Taglines, []string{"Provider tagline"}) || item.Overview != "" || len(item.People) != 0 {
						t.Fatalf("Taglines projection was not enriched and reduced: %+v", item)
					}
				case "People":
					if len(item.People) != 1 || item.People[0].Name != "Provider performer" || item.Overview != "" {
						t.Fatalf("People projection was not enriched and reduced: %+v", item)
					}
				}
			})
		}
	}
}

func TestCatalogLightIDSelectionDoesNotEnrichMetadataByDefault(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	reader.title = watchstate.CatalogTitle{
		ID: "00000000-0000-4000-8000-000000000100", MediaType: "movie", Title: "Snapshot",
		Genres: []string{}, ProviderIDs: map[string]string{},
	}
	enriched := reader.title
	enriched.OriginalTitle = "Provider original title"
	detailReader := &catalogHTTPDetailReader{catalogHTTPReader: reader, enrichedTitle: enriched}
	handler.catalog = detailReader

	response := httptest.NewRecorder()
	handler.handleItems(response, authenticatedCatalogRequest(t, token, "/Items?Ids="+reader.title.ID))
	var result QueryResult[BaseItemDto]
	decodeCatalogResponse(t, response, &result)
	if response.Code != http.StatusOK || len(result.Items) != 1 || len(detailReader.enrichmentRead) != 0 || result.Items[0].OriginalTitle != "" {
		t.Fatalf("status=%d result=%+v enrichment=%+v", response.Code, result, detailReader.enrichmentRead)
	}
}

func TestCatalogItemsIgnoreUnsupportedProjectedValuesLikeJellyfinBinder(t *testing.T) {
	for _, target := range []string{
		"/Items?Fields=FutureSdkField&EnableImageTypes=Primary,Unknown",
	} {
		handler, reader, token := newCatalogHTTPHandler(t)
		response := httptest.NewRecorder()
		handler.handleItems(response, authenticatedCatalogRequest(t, token, target))
		if response.Code != http.StatusOK || len(reader.queries) != 0 || len(reader.titleIDs) != 0 {
			t.Fatalf("target=%q status=%d queries=%+v titles=%v body=%s", target, response.Code, reader.queries, reader.titleIDs, response.Body.String())
		}
	}
}

func TestCatalogItemsAcceptClientDateAndMediaSourceCountFields(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	created := time.Date(2026, 8, 9, 12, 34, 56, 700_000_000, time.UTC)
	reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{{
		ID: "00000000-0000-4000-8000-000000000100", MediaType: "movie", Title: "Dated Movie",
		CreatedAt: created, Genres: []string{}, ProviderIDs: map[string]string{},
	}}, Total: 1, Limit: 20}
	response := httptest.NewRecorder()
	handler.handleItems(response, authenticatedCatalogRequest(t, token, "/Items?IncludeItemTypes=Movie&Fields=DateCreated,MediaSourceCount&Limit=20"))
	var result QueryResult[BaseItemDto]
	decodeCatalogResponse(t, response, &result)
	if response.Code != http.StatusOK || len(result.Items) != 1 || result.Items[0].DateCreated != "2026-08-09T12:34:56.7000000Z" {
		t.Fatalf("standard client fields status=%d result=%+v body=%s", response.Code, result, response.Body.String())
	}
}

func requireDeferredMediaSource(t *testing.T, item BaseItemDto) {
	t.Helper()
	if len(item.MediaSources) != 1 {
		t.Fatalf("item has %d deferred sources, want exactly one: %+v", len(item.MediaSources), item)
	}
	source := item.MediaSources[0]
	expectedPath := "/rivune/" + item.Id + "/" + item.Id + ".strm"
	expectedDirectURL := "/Videos/" + item.Id + "/stream?MediaSourceId=" + url.QueryEscape(item.Id) + "&Static=true"
	if source.Id != item.Id || source.Name != item.Name || source.Path != expectedPath || item.Path != expectedPath || source.DirectStreamUrl != expectedDirectURL ||
		source.Container != "strm" || source.Protocol != "File" || source.Type != "Default" || source.IsRemote || !source.SupportsDirectPlay ||
		!source.SupportsDirectStream || !source.SupportsTranscoding || !source.SupportsProbing || source.VideoType != "VideoFile" ||
		len(source.Formats) != 1 || source.Formats[0] != "strm" || source.RequiredHttpHeaders == nil || len(source.RequiredHttpHeaders) != 0 ||
		source.MediaAttachments == nil || len(source.MediaAttachments) != 0 || strings.ContainsAny(source.Path, "?#") {
		t.Fatalf("deferred file-like source contract is incomplete: item=%+v source=%+v", item, source)
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
