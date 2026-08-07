package jellyfin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

type searchArtworkPresenter struct {
	localized  map[string]string
	registered map[string]string
	served     []string
	seen       []string
}

func (presenter *searchArtworkPresenter) LocalURLs(_ context.Context, upstream []string) []string {
	presenter.seen = append(presenter.seen, upstream...)
	result := make([]string, len(upstream))
	for index, value := range upstream {
		if localized := presenter.localized[value]; localized != "" {
			result[index] = localized
			continue
		}
		if key, valid := localizedArtworkTag(value); valid && presenter.registered[value] == key {
			result[index] = value
		}
	}
	return result
}
func (*searchArtworkPresenter) PresentAddonResources(context.Context, []addon.ResourceResult) {}

func (presenter *searchArtworkPresenter) LookupKey(_ context.Context, materialized string) (string, bool) {
	key, found := presenter.registered[materialized]
	return key, found
}

func (presenter *searchArtworkPresenter) ServeKey(response http.ResponseWriter, _ *http.Request, key string) {
	presenter.served = append(presenter.served, key)
	response.Header().Set("Content-Type", "image/png")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("image"))
}

type searchArtworkStore struct {
	titles        map[string]watchstate.CatalogTitle
	resolved      map[string]string
	resolveInputs []watchstate.ResolveTitleInput
}

func (store *searchArtworkStore) GetCatalogTitle(_ context.Context, _ auth.Principal, id string) (watchstate.CatalogTitle, error) {
	title, found := store.titles[id]
	if !found {
		return watchstate.CatalogTitle{}, watchstate.ErrNotFound
	}
	return title, nil
}

func (store *searchArtworkStore) GetCatalogTitles(_ context.Context, _ auth.Principal, ids []string) ([]watchstate.CatalogTitle, error) {
	result := make([]watchstate.CatalogTitle, 0, len(ids))
	for _, id := range ids {
		if title, found := store.titles[id]; found {
			result = append(result, title)
		}
	}
	return result, nil
}

func (*searchArtworkStore) ListCatalogItems(_ context.Context, _ auth.Principal, query watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
	return watchstate.CatalogPage{Items: []watchstate.CatalogTitle{}, Offset: query.Offset, Limit: query.Limit}, nil
}

func (store *searchArtworkStore) ResolveLinkedCatalogTitle(_ context.Context, _ auth.Principal, input watchstate.ResolveTitleInput) (watchstate.TitleReference, error) {
	store.resolveInputs = append(store.resolveInputs, input)
	id := store.resolved[input.Provider+":"+input.ExternalID]
	if id == "" {
		return watchstate.TitleReference{}, watchstate.ErrInvalidInput
	}
	title := store.titles[id]
	title.PosterURL = input.PosterURL
	title.BackgroundURL = input.BackgroundURL
	store.titles[id] = title
	return watchstate.TitleReference{TitleID: id, MediaType: input.MediaType}, nil
}

func TestCatalogSearchLocalizesMetadataAndAddonArtworkBeforeDTOAndImageRoute(t *testing.T) {
	const (
		metadataID = "81000000-0000-4000-8000-000000000001"
		rejectedID = "81000000-0000-4000-8000-000000000002"
		addonID    = "81000000-0000-4000-8000-000000000003"
	)
	metadataPoster := "https://image.tmdb.org/t/p/w500/poster.jpg?token=tmdb-secret"
	metadataBackdrop := "https://image.tmdb.org/t/p/original/backdrop.jpg?token=tmdb-backdrop-secret"
	addonPoster := "https://addon.invalid/poster.jpg?token=addon-secret"
	addonBackdrop := "https://addon.invalid/backdrop.jpg?token=addon-backdrop-secret"
	rejectedPoster := "http://127.0.0.1/private.jpg?token=rejected-secret"
	metadataPosterKey := strings.Repeat("a", 64)
	metadataBackdropKey := strings.Repeat("b", 64)
	addonPosterKey := strings.Repeat("c", 64)
	addonBackdropKey := strings.Repeat("d", 64)
	metadataPosterLocal := localizedArtworkPrefix + metadataPosterKey
	metadataBackdropLocal := localizedArtworkPrefix + metadataBackdropKey
	addonPosterLocal := localizedArtworkPrefix + addonPosterKey
	addonBackdropLocal := localizedArtworkPrefix + addonBackdropKey
	presenter := &searchArtworkPresenter{
		localized: map[string]string{
			metadataPoster: metadataPosterLocal, metadataBackdrop: metadataBackdropLocal,
			addonPoster: addonPosterLocal, addonBackdrop: addonBackdropLocal,
		},
		registered: map[string]string{
			metadataPosterLocal: metadataPosterKey, metadataBackdropLocal: metadataBackdropKey,
			addonPosterLocal: addonPosterKey, addonBackdropLocal: addonBackdropKey,
		},
	}
	store := &searchArtworkStore{
		titles: map[string]watchstate.CatalogTitle{
			metadataID: {ID: metadataID, MediaType: "movie", Title: "TMDB Result", PosterURL: metadataPoster, BackgroundURL: metadataBackdrop, Genres: []string{}, ProviderIDs: map[string]string{"tmdb": "11"}},
			rejectedID: {ID: rejectedID, MediaType: "movie", Title: "Rejected Artwork", PosterURL: rejectedPoster, Genres: []string{}, ProviderIDs: map[string]string{"tmdb": "22"}},
			addonID:    {ID: addonID, MediaType: "series", Title: "Add-on Result", PosterURL: addonPoster, BackgroundURL: addonBackdrop, Genres: []string{}, ProviderIDs: map[string]string{"tmdb": "33"}},
		},
		resolved: map[string]string{"tmdb:33": addonID},
	}
	metadataSearch := &orchestrationMetadata{
		movies: metadata.MoviePage{Items: []metadata.Movie{
			{ID: metadataID, MediaType: "movie", Title: "TMDB Result", PosterURL: metadataPoster, BackdropURL: metadataBackdrop, Genres: []metadata.Genre{}, ExternalIDs: map[string]string{"tmdb": "11"}},
			{ID: rejectedID, MediaType: "movie", Title: "Rejected Artwork", PosterURL: rejectedPoster, Genres: []metadata.Genre{}, ExternalIDs: map[string]string{"tmdb": "22"}},
		}, Page: 1, TotalPages: 1, TotalResults: 2},
		series: metadata.SeriesPage{Items: []metadata.Series{}, Page: 1, TotalPages: 1, TotalResults: 0},
	}
	addonSearch := &orchestrationAddons{page: addon.CatalogSearchPage{Complete: true, Items: []addon.CatalogSearchItem{{
		AddonID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", CatalogID: "search", AddonName: "Fixture Add-on",
		ResourceID: "addon-result", MediaType: "series", Title: "Add-on Result", PosterURL: addonPoster,
		BackgroundURL: addonBackdrop, ExternalIDs: map[string]string{"tmdb": "33"},
	}}}}
	catalog, err := NewCatalogReader(store, metadataSearch, addonSearch, presenter)
	if err != nil {
		t.Fatalf("construct localized catalog: %v", err)
	}
	serverID, err := ParseServerID(catalogTestServerID)
	if err != nil {
		t.Fatal(err)
	}
	profileID := catalogTestProfileID
	expiresAt := time.Now().Add(time.Hour)
	session := AuthenticatedSession{
		ID: "82000000-0000-4000-8000-000000000001", ProfileID: profileID, ProfileName: "Main", ExpiresAt: expiresAt,
		Principal: auth.Principal{SessionID: "83000000-0000-4000-8000-000000000001", UserID: "84000000-0000-4000-8000-000000000001", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt},
	}
	handler := &Handler{
		serverInfo: ServerInfo{ID: serverID, Name: "Rivune"}, authentication: &catalogHTTPAuthentication{session: session},
		catalog: catalog, artwork: presenter,
	}
	token, _, err := newCompatCredential()
	if err != nil {
		t.Fatal(err)
	}
	request := authenticatedCatalogRequest(t, token, "/Search/Hints?SearchTerm=result&IncludeItemTypes=Movie,Series&Limit=10")
	response := httptest.NewRecorder()
	handler.handleSearchHints(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", response.Code, response.Body.String())
	}
	var result SearchHintResult
	decodeCatalogResponse(t, response, &result)
	hints := make(map[string]SearchHintDto, len(result.SearchHints))
	for _, hint := range result.SearchHints {
		hints[hint.Id] = hint
	}
	if hints[metadataID].PrimaryImageTag != metadataPosterKey || hints[metadataID].BackdropImageTag != metadataBackdropKey {
		t.Fatalf("TMDB artwork tags = %+v", hints[metadataID])
	}
	if hints[addonID].PrimaryImageTag != addonPosterKey || hints[addonID].BackdropImageTag != addonBackdropKey {
		t.Fatalf("add-on artwork tags = %+v", hints[addonID])
	}
	if hints[rejectedID].PrimaryImageTag != "" || hints[rejectedID].BackdropImageTag != "" {
		t.Fatalf("unpresentable artwork was projected: %+v", hints[rejectedID])
	}
	body := response.Body.String()
	for _, forbidden := range []string{"image.tmdb.org", "addon.invalid", "127.0.0.1", "tmdb-secret", "addon-secret", "rejected-secret", localizedArtworkPrefix} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("search DTO exposed artwork source %q: %s", forbidden, body)
		}
	}
	if len(store.resolveInputs) != 1 || store.resolveInputs[0].PosterURL != addonPosterLocal || store.resolveInputs[0].BackgroundURL != addonBackdropLocal {
		t.Fatalf("add-on artwork was persisted before localization: %+v", store.resolveInputs)
	}
	if addonSearch.artwork != presenter {
		t.Fatal("compatibility search did not pass the artwork presenter to add-on payload decoding")
	}
	presented := strings.Join(presenter.seen, "\n")
	for _, source := range []string{metadataPoster, metadataBackdrop, addonPoster, addonBackdrop, rejectedPoster} {
		if !strings.Contains(presented, source) {
			t.Fatalf("artwork source did not pass through presenter: %q in %q", source, presented)
		}
	}

	for _, item := range []struct {
		id  string
		key string
	}{{metadataID, metadataPosterKey}, {addonID, addonPosterKey}} {
		imageRequest := authenticatedCatalogRequest(t, token, "/Items/"+item.id+"/Images/Primary")
		imageRequest.SetPathValue("id", item.id)
		imageRequest.SetPathValue("type", "Primary")
		imageResponse := httptest.NewRecorder()
		handler.handleImage(imageResponse, imageRequest)
		if imageResponse.Code != http.StatusOK || len(presenter.served) == 0 || presenter.served[len(presenter.served)-1] != item.key {
			t.Fatalf("authorized image %s status=%d served=%v", item.id, imageResponse.Code, presenter.served)
		}
	}
}
