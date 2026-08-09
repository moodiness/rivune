package jellyfin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestUserWorkflowFavoriteResumeNextUpPersistsAcrossLogin(t *testing.T) {
	fixture := newSequenceHTTPFixture(t, "", "Workflow Client", "X-Emby-Token", "X-Emby-Authorization", `MediaBrowser Client="Workflow Client", Device="Living Room", DeviceId="workflow-device", Version="1.0"`)
	login := fixture.login(t, "workflow-login", sequencePrimaryCredentialID)
	sequenceRequireStatus(t, login, http.StatusOK)
	var authentication AuthenticationResult
	sequenceDecode(t, login, &authentication)
	token, userID := authentication.AccessToken, authentication.User.Id

	favoritePath := "/Users/" + url.PathEscape(userID) + "/FavoriteItems/" + url.PathEscape(sequenceEpisodeID)
	unfavorite := fixture.request(t, "workflow-unfavorite", http.MethodDelete, favoritePath, "", token)
	sequenceRequireStatus(t, unfavorite, http.StatusOK)
	favorite := fixture.request(t, "workflow-favorite", http.MethodPost, favoritePath, "", token)
	sequenceRequireStatus(t, favorite, http.StatusOK)
	var favoriteState UserItemDataDto
	sequenceDecode(t, favorite, &favoriteState)
	if !favoriteState.IsFavorite || favoriteState.ItemId != sequenceEpisodeID {
		t.Fatalf("favorite response did not expose persisted state: %+v", favoriteState)
	}

	favoriteItems := fixture.request(t, "workflow-favorite-items", http.MethodGet, "/Items?Ids="+url.QueryEscape(sequenceEpisodeID)+"&Filters=IsFavorite&EnableUserData=true", "", token)
	sequenceRequireStatus(t, favoriteItems, http.StatusOK)
	var favoritePage QueryResult[BaseItemDto]
	sequenceDecode(t, favoriteItems, &favoritePage)
	if favoritePage.TotalRecordCount != 1 || len(favoritePage.Items) != 1 || favoritePage.Items[0].Id != sequenceEpisodeID || favoritePage.Items[0].UserData == nil || !favoritePage.Items[0].UserData.IsFavorite {
		t.Fatalf("favorite was not immediately visible through Items: %+v", favoritePage)
	}

	secondaryLogin := fixture.login(t, "workflow-secondary-login", sequenceSecondaryCredentialID)
	sequenceRequireStatus(t, secondaryLogin, http.StatusOK)
	var secondary AuthenticationResult
	sequenceDecode(t, secondaryLogin, &secondary)
	crossProfile := fixture.request(t, "workflow-cross-profile-favorite", http.MethodDelete, favoritePath, "", secondary.AccessToken)
	sequenceRequireStatus(t, crossProfile, http.StatusNotFound)
	if !fixture.watchstate.profiles[userID].favorites[sequenceEpisodeID] || fixture.watchstate.profiles[secondary.User.Id].favorites[sequenceEpisodeID] != true {
		t.Fatal("cross-profile favorite selector changed profile state")
	}

	progressBody := `{"PlaybackPositionTicks":400000000,"Played":false}`
	progress := fixture.request(t, "workflow-progress", http.MethodPost, "/Users/"+url.PathEscape(userID)+"/Items/"+url.PathEscape(sequenceEpisodeID)+"/UserData", progressBody, token)
	sequenceRequireStatus(t, progress, http.StatusOK)
	var progressState UserItemDataDto
	sequenceDecode(t, progress, &progressState)
	if progressState.PlaybackPositionTicks != SecondsToTicks(40) || progressState.Played || !progressState.IsFavorite {
		t.Fatalf("progress response is inconsistent: %+v", progressState)
	}

	assertResume := func(name, credential string) {
		t.Helper()
		resume := fixture.request(t, name, http.MethodGet, "/UserItems/Resume?UserId="+url.QueryEscape(userID)+"&StartIndex=0&Limit=1", "", credential)
		sequenceRequireStatus(t, resume, http.StatusOK)
		var page QueryResult[BaseItemDto]
		sequenceDecode(t, resume, &page)
		if page.TotalRecordCount != 1 || page.StartIndex != 0 || len(page.Items) != 1 || page.Items[0].Id != sequenceEpisodeID || page.Items[0].UserData == nil || page.Items[0].UserData.PlaybackPositionTicks != SecondsToTicks(40) {
			t.Fatalf("resume page did not expose exact persisted progress: %+v", page)
		}
		if strings.Contains(resume.Body.String(), `"MediaSources"`) || strings.Contains(resume.Body.String(), `"Overview"`) {
			t.Fatalf("general resume list bypassed field gating: %s", resume.Body.String())
		}
	}
	assertResume("workflow-resume", token)

	reconnect := fixture.login(t, "workflow-reconnect", sequencePrimaryCredentialID)
	sequenceRequireStatus(t, reconnect, http.StatusOK)
	var reconnected AuthenticationResult
	sequenceDecode(t, reconnect, &reconnected)
	if reconnected.AccessToken == token {
		t.Fatal("reconnect reused the previous compatibility credential")
	}
	assertResume("workflow-resume-after-reconnect", reconnected.AccessToken)
	itemsAfterReconnect := fixture.request(t, "workflow-items-after-reconnect", http.MethodGet, "/Items?Ids="+url.QueryEscape(sequenceEpisodeID)+"&EnableUserData=true", "", reconnected.AccessToken)
	sequenceRequireStatus(t, itemsAfterReconnect, http.StatusOK)
	var reconnectedItems QueryResult[BaseItemDto]
	sequenceDecode(t, itemsAfterReconnect, &reconnectedItems)
	if reconnectedItems.TotalRecordCount != 1 || len(reconnectedItems.Items) != 1 || reconnectedItems.Items[0].UserData == nil ||
		!reconnectedItems.Items[0].UserData.IsFavorite || reconnectedItems.Items[0].UserData.PlaybackPositionTicks != SecondsToTicks(40) {
		t.Fatalf("Items lost favorite or progress across reconnect: %+v", reconnectedItems)
	}

	played := fixture.request(t, "workflow-played", http.MethodPost, "/Users/"+url.PathEscape(userID)+"/PlayedItems/"+url.PathEscape(sequenceEpisodeID), "", reconnected.AccessToken)
	sequenceRequireStatus(t, played, http.StatusOK)
	var playedState UserItemDataDto
	sequenceDecode(t, played, &playedState)
	if !playedState.Played || !playedState.IsFavorite {
		t.Fatalf("played mutation lost favorite or completion: %+v", playedState)
	}

	resumeAfterPlayed := fixture.request(t, "workflow-resume-after-played", http.MethodGet, "/Users/"+url.PathEscape(userID)+"/Items/Resume?Limit=1", "", reconnected.AccessToken)
	sequenceRequireStatus(t, resumeAfterPlayed, http.StatusOK)
	var emptyResume QueryResult[BaseItemDto]
	sequenceDecode(t, resumeAfterPlayed, &emptyResume)
	if emptyResume.TotalRecordCount != 0 || len(emptyResume.Items) != 0 {
		t.Fatalf("played item remained resumable: %+v", emptyResume)
	}

	nextUp := fixture.request(t, "workflow-next-up", http.MethodGet, "/Shows/NextUp?UserId="+url.QueryEscape(userID)+"&SeriesId="+url.QueryEscape(sequenceSeriesID)+"&Limit=1", "", reconnected.AccessToken)
	sequenceRequireStatus(t, nextUp, http.StatusOK)
	var nextPage QueryResult[BaseItemDto]
	sequenceDecode(t, nextUp, &nextPage)
	if nextPage.TotalRecordCount != 1 || len(nextPage.Items) != 1 || nextPage.Items[0].Id != sequenceNextEpisodeID ||
		nextPage.Items[0].SeriesId != sequenceSeriesID || nextPage.Items[0].SeasonId != sequenceSeasonID || nextPage.Items[0].ParentId != sequenceSeasonID ||
		nextPage.Items[0].IndexNumber == nil || *nextPage.Items[0].IndexNumber != 4 ||
		nextPage.Items[0].ParentIndexNumber == nil || *nextPage.Items[0].ParentIndexNumber != 1 {
		t.Fatalf("next-up hierarchy is incoherent: %+v", nextPage)
	}

	foreignNextUp := fixture.request(t, "workflow-cross-profile-next-up", http.MethodGet, "/Shows/NextUp?UserId="+url.QueryEscape(secondary.User.Id), "", reconnected.AccessToken)
	sequenceRequireStatus(t, foreignNextUp, http.StatusNotFound)

	unplayed := fixture.request(t, "workflow-unplayed", http.MethodDelete, "/Users/"+url.PathEscape(userID)+"/PlayedItems/"+url.PathEscape(sequenceEpisodeID), "", reconnected.AccessToken)

	sequenceRequireStatus(t, unplayed, http.StatusOK)
	nextAfterUnplayed := fixture.request(t, "workflow-next-up-after-unplayed", http.MethodGet, "/Shows/NextUp?UserId="+url.QueryEscape(userID)+"&Limit=1", "", reconnected.AccessToken)
	sequenceRequireStatus(t, nextAfterUnplayed, http.StatusOK)
	var emptyNext QueryResult[BaseItemDto]
	sequenceDecode(t, nextAfterUnplayed, &emptyNext)
	if emptyNext.TotalRecordCount != 0 || len(emptyNext.Items) != 0 {
		t.Fatalf("unplayed transition left a next-up candidate: %+v", emptyNext)
	}

	invalidItem := fixture.request(t, "workflow-invalid-item", http.MethodPost, "/Users/"+url.PathEscape(userID)+"/FavoriteItems/00000000-0000-4000-8000-000000000099", "", reconnected.AccessToken)
	sequenceRequireStatus(t, invalidItem, http.StatusNotFound)
}
