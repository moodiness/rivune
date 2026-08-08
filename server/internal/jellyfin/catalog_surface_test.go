package jellyfin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moodiness/rivune/server/internal/watchstate"
)

func TestCatalogFiltersAndGenresUseOnlyAuthorizedBoundedProjection(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	reader.page = watchstate.CatalogPage{
		Items: []watchstate.CatalogTitle{
			{ID: "00000000-0000-4000-8000-000000000101", MediaType: "movie", Title: "First", Released: "2024-01-02", Genres: []string{"Thriller", "Drama"}},
			{ID: "00000000-0000-4000-8000-000000000102", MediaType: "movie", Title: "Second", Released: "2025-01-02", Genres: []string{"drama", "Comedy"}},
		},
		Total: 2,
	}

	filtersRequest := authenticatedCatalogRequest(t, token, "/Items/Filters2?IncludeItemTypes=Movie")
	filtersResponse := httptest.NewRecorder()
	handler.handleItemsFilters2(filtersResponse, filtersRequest)
	var filters QueryFilters
	decodeCatalogResponse(t, filtersResponse, &filters)
	if filtersResponse.Code != http.StatusOK || len(filters.Genres) != 3 || filters.Genres[0].Name != "Comedy" || filters.Genres[1].Name != "Drama" || filters.Tags == nil {
		t.Fatalf("unexpected filters: status=%d filters=%+v", filtersResponse.Code, filters)
	}
	for _, genre := range filters.Genres {
		if !validCompatUUID(genre.Id) {
			t.Fatalf("genre %q has invalid stable id %q", genre.Name, genre.Id)
		}
	}
	if len(reader.queries) != 1 || len(reader.queries[0].MediaTypes) != 1 || reader.queries[0].MediaTypes[0] != "movie" || reader.queries[0].Limit != MaximumQueryLimit {
		t.Fatalf("filters were not a bounded movie projection: %+v", reader.queries)
	}

	genresRequest := authenticatedCatalogRequest(t, token, "/Genres?SearchTerm=ra&StartIndex=0&Limit=1&IncludeItemTypes=Movie")
	genresResponse := httptest.NewRecorder()
	handler.handleGenres(genresResponse, genresRequest)
	var genres QueryResult[BaseItemDto]
	decodeCatalogResponse(t, genresResponse, &genres)
	if genresResponse.Code != http.StatusOK || genres.TotalRecordCount != 1 || len(genres.Items) != 1 || genres.Items[0].Name != "Drama" || genres.Items[0].Type != "Genre" || genres.Items[0].ImageTags == nil || genres.Items[0].BackdropImageTags == nil {
		t.Fatalf("unexpected genre page: status=%d result=%+v", genresResponse.Code, genres)
	}

	queriesBefore := len(reader.queries)
	mismatch := authenticatedCatalogRequest(t, token, "/Items/Filters?UserId=22222222-2222-4222-8222-222222222222")
	mismatchResponse := httptest.NewRecorder()
	handler.handleItemsFilters(mismatchResponse, mismatch)
	if mismatchResponse.Code != http.StatusNotFound || len(reader.queries) != queriesBefore || strings.Contains(mismatchResponse.Body.String(), "Thriller") {
		t.Fatalf("cross-profile filters leaked catalog: status=%d calls=%d body=%s", mismatchResponse.Code, len(reader.queries), mismatchResponse.Body.String())
	}
}

func TestPersonsSuggestionsAndSimilarAreFilteredStableDTOs(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	baselineID := "00000000-0000-4000-8000-000000000201"
	matchID := "00000000-0000-4000-8000-000000000202"
	otherID := "00000000-0000-4000-8000-000000000203"
	baseline := watchstate.CatalogTitle{
		ID: baselineID, MediaType: "movie", Title: "Baseline", Genres: []string{"Drama"},
		People: []watchstate.CatalogPerson{{Name: "Alice Actor", Type: "Actor"}},
	}
	match := watchstate.CatalogTitle{
		ID: matchID, MediaType: "movie", Title: "A match", Genres: []string{"Drama"},
		People:    []watchstate.CatalogPerson{{Name: "Alice Actor", Type: "Actor"}},
		PosterURL: "https://provider.invalid/poster?token=secret",
	}
	other := watchstate.CatalogTitle{ID: otherID, MediaType: "movie", Title: "Other", Genres: []string{"Comedy"}}
	reader.title = baseline
	reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{baseline, match, other}, Total: 3}

	personsRequest := authenticatedCatalogRequest(t, token, "/Persons?PersonTypes=Actor&SearchTerm=alice&Limit=10")
	personsResponse := httptest.NewRecorder()
	handler.handlePersons(personsResponse, personsRequest)
	var persons QueryResult[BaseItemDto]
	decodeCatalogResponse(t, personsResponse, &persons)
	if personsResponse.Code != http.StatusOK || persons.TotalRecordCount != 1 || len(persons.Items) != 1 || persons.Items[0].Type != "Person" || !validCompatUUID(persons.Items[0].Id) {
		t.Fatalf("unexpected persons result: status=%d result=%+v", personsResponse.Code, persons)
	}

	suggestionsRequest := authenticatedCatalogRequest(t, token, "/Items/Suggestions?Type=Movie&StartIndex=1&Limit=1")
	reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{match}, Offset: 1, Limit: 1, Total: 3}
	suggestionsResponse := httptest.NewRecorder()
	handler.handleSuggestions(suggestionsResponse, suggestionsRequest)
	var suggestions QueryResult[BaseItemDto]
	decodeCatalogResponse(t, suggestionsResponse, &suggestions)
	if suggestionsResponse.Code != http.StatusOK || suggestions.StartIndex != 1 || suggestions.TotalRecordCount != 3 || len(suggestions.Items) != 1 || !suggestions.Items[0].CanDownload {
		t.Fatalf("unexpected suggestions: status=%d result=%+v", suggestionsResponse.Code, suggestions)
	}
	if strings.Contains(suggestionsResponse.Body.String(), "provider.invalid") || strings.Contains(suggestionsResponse.Body.String(), "secret") {
		t.Fatalf("suggestions disclosed provider data: %s", suggestionsResponse.Body.String())
	}

	reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{baseline, other, match}, Total: 3}
	similarRequest := authenticatedCatalogRequest(t, token, "/Items/"+baselineID+"/Similar?Limit=10")
	similarRequest.SetPathValue("itemId", baselineID)
	similarResponse := httptest.NewRecorder()
	handler.handleSimilarItems(similarResponse, similarRequest)
	var similar QueryResult[BaseItemDto]
	decodeCatalogResponse(t, similarResponse, &similar)
	if similarResponse.Code != http.StatusOK || similar.TotalRecordCount != 1 || len(similar.Items) != 1 || similar.Items[0].Id != matchID || !similar.Items[0].CanDownload || len(similar.Items[0].People) != 1 || !validCompatUUID(similar.Items[0].People[0].Id) {
		t.Fatalf("unexpected similar result: status=%d result=%+v", similarResponse.Code, similar)
	}
}

func TestRecommendationsAndAbsentMediaDomainsStayHonest(t *testing.T) {
	handler, reader, token := newCatalogHTTPHandler(t)
	baselineID := "00000000-0000-4000-8000-000000000301"
	matchID := "00000000-0000-4000-8000-000000000302"
	baseline := watchstate.CatalogTitle{ID: baselineID, MediaType: "movie", Title: "Liked", Genres: []string{"Drama"}, InLibrary: true}
	match := watchstate.CatalogTitle{ID: matchID, MediaType: "movie", Title: "Related", Genres: []string{"Drama"}}
	reader.title = baseline
	reader.page = watchstate.CatalogPage{Items: []watchstate.CatalogTitle{baseline, match}, Total: 2}

	recommendationRequest := authenticatedCatalogRequest(t, token, "/Movies/Recommendations?CategoryLimit=1&ItemLimit=1")
	recommendationResponse := httptest.NewRecorder()
	handler.handleMovieRecommendations(recommendationResponse, recommendationRequest)
	var recommendations []RecommendationDto
	decodeCatalogResponse(t, recommendationResponse, &recommendations)
	if recommendationResponse.Code != http.StatusOK || len(recommendations) != 1 || len(recommendations[0].Items) != 1 || recommendations[0].Items[0].Id != matchID || !validCompatUUID(recommendations[0].CategoryId) {
		t.Fatalf("unexpected recommendations: status=%d result=%+v", recommendationResponse.Code, recommendations)
	}

	segmentsRequest := authenticatedCatalogRequest(t, token, "/MediaSegments/"+baselineID)
	segmentsRequest.SetPathValue("itemId", baselineID)
	segmentsResponse := httptest.NewRecorder()
	handler.handleMediaSegments(segmentsResponse, segmentsRequest)
	var segments QueryResult[MediaSegmentDto]
	decodeCatalogResponse(t, segmentsResponse, &segments)
	if segmentsResponse.Code != http.StatusOK || segments.Items == nil || segments.TotalRecordCount != 0 {
		t.Fatalf("unexpected empty media segments: status=%d result=%+v", segmentsResponse.Code, segments)
	}

	themeRequest := authenticatedCatalogRequest(t, token, "/Users/"+catalogTestProfileID+"/Items/"+baselineID+"/ThemeMedia")
	themeRequest.SetPathValue("userId", catalogTestProfileID)
	themeRequest.SetPathValue("itemId", baselineID)
	themeResponse := httptest.NewRecorder()
	handler.handleThemeMedia(themeResponse, themeRequest)
	var theme AllThemeMediaResult
	decodeCatalogResponse(t, themeResponse, &theme)
	if themeResponse.Code != http.StatusOK || theme.ThemeVideosResult == nil || theme.ThemeSongsResult == nil || theme.SoundtrackSongsResult == nil || theme.ThemeVideosResult.Items == nil || theme.ThemeVideosResult.OwnerId != baselineID {
		t.Fatalf("unexpected empty theme media: status=%d result=%+v", themeResponse.Code, theme)
	}

	callsBefore := len(reader.titleIDs)
	crossProfile := authenticatedCatalogRequest(t, token, "/Users/22222222-2222-4222-8222-222222222222/Items/"+baselineID+"/ThemeSongs")
	crossProfile.SetPathValue("userId", "22222222-2222-4222-8222-222222222222")
	crossProfile.SetPathValue("itemId", baselineID)
	crossProfileResponse := httptest.NewRecorder()
	handler.handleThemeSongs(crossProfileResponse, crossProfile)
	if crossProfileResponse.Code != http.StatusNotFound || len(reader.titleIDs) != callsBefore {
		t.Fatalf("cross-profile legacy media looked up item: status=%d calls=%d", crossProfileResponse.Code, len(reader.titleIDs))
	}
}
