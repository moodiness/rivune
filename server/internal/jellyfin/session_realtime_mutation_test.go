package jellyfin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/watchstate"
)

func TestUserDataMutationsPublishCompleteEventsToEveryProfileSession(t *testing.T) {
	for _, mutation := range []string{"played", "favorite", "user-data"} {
		t.Run(mutation, func(t *testing.T) {
			handler, authentication, service, _, token, itemID, _, _ := stateHTTPFixture(t)
			handler.bootstrap = newBootstrapRegistry()
			service.progress[itemID] = watchstate.Progress{
				TitleID: itemID, MediaType: "movie", PositionSeconds: 25, DurationSeconds: 100, Version: 7,
			}
			rating, percentage, unplayed, playCount, likes := 9.25, 42.5, 3, 4, true
			lastPlayed := time.Date(2026, 8, 3, 4, 5, 6, 0, time.UTC)
			supplemental := watchstate.UserDataValues{
				Rating: &rating, RatingSet: true, PlayedPercentage: &percentage, PlayedPercentageSet: true,
				UnplayedItemCount: &unplayed, UnplayedItemCountSet: true, PlayCount: &playCount, PlayCountSet: true,
				Likes: &likes, LikesSet: true, LastPlayedDate: &lastPlayed, LastPlayedDateSet: true,
			}
			service.userData[itemID] = supplemental
			item := handler.catalog.(*stateCatalog).items[itemID]
			item.UserData = &supplemental
			handler.catalog.(*stateCatalog).items[itemID] = item
			ownerOne, ownerTwo, foreign := acquireMutationEventSockets(t, handler, authentication.session)
			defer handler.bootstrap.releaseSocket(ownerOne)
			defer handler.bootstrap.releaseSocket(ownerTwo)
			defer handler.bootstrap.releaseSocket(foreign)

			var response *httptest.ResponseRecorder
			switch mutation {
			case "played":
				request := httptest.NewRequest(http.MethodPost, "/Users/"+authentication.session.ProfileID+"/PlayedItems/"+itemID, nil)
				request.SetPathValue("userId", authentication.session.ProfileID)
				request.SetPathValue("itemId", itemID)
				request.Header.Set("X-Emby-Token", token)
				response = httptest.NewRecorder()
				handler.handlePlayedItem(response, request)
			case "favorite":
				request := httptest.NewRequest(http.MethodPost, "/UserItems/"+itemID+"/Favorite", nil)
				request.SetPathValue("itemId", itemID)
				request.Header.Set("X-Emby-Token", token)
				response = httptest.NewRecorder()
				handler.handleFavoriteItem(response, request)
			case "user-data":
				runtimeMinutes := 2
				item = handler.catalog.(*stateCatalog).items[itemID]
				item.RuntimeMinutes = &runtimeMinutes
				handler.catalog.(*stateCatalog).items[itemID] = item
				request := httptest.NewRequest(http.MethodPost, "/UserItems/"+itemID+"/UserData", strings.NewReader(`{"PlaybackPositionTicks":300000000,"Played":false}`))
				request.SetPathValue("itemId", itemID)
				request.Header.Set("X-Emby-Token", token)
				request.Header.Set("Content-Type", "application/json")
				response = httptest.NewRecorder()
				handler.handleUserData(response, request)
			}
			if response.Code != http.StatusOK {
				t.Fatalf("mutation status=%d body=%s", response.Code, response.Body.String())
			}
			var persisted UserItemDataDto
			if err := json.Unmarshal(response.Body.Bytes(), &persisted); err != nil {
				t.Fatalf("decode mutation response: %v body=%s", err, response.Body.String())
			}
			if persisted.Rating == nil || *persisted.Rating != rating ||
				persisted.PlayedPercentage == nil || *persisted.PlayedPercentage != percentage ||
				persisted.UnplayedItemCount == nil || *persisted.UnplayedItemCount != unplayed ||
				persisted.PlayCount != playCount || persisted.Likes == nil || *persisted.Likes != likes ||
				persisted.LastPlayedDate != "2026-08-03T04:05:06.0000000Z" {
				t.Fatalf("mutation response omitted persisted UserData: %+v", persisted)
			}
			for index, lease := range []*compatSocketLease{ownerOne, ownerTwo} {
				event := receiveCompatSocketEvent(t, lease)
				changed, ok := event.Data.(UserDataChangeInfo)
				if event.MessageType != "UserDataChanged" || !ok || changed.UserId != authentication.session.ProfileID ||
					len(changed.UserDataList) != 1 || !reflect.DeepEqual(changed.UserDataList[0], persisted) {
					t.Fatalf("owner %d event=%+v persisted=%+v", index+1, event, persisted)
				}
				libraryEvent := receiveCompatSocketEvent(t, lease)
				library, ok := libraryEvent.Data.(LibraryUpdateInfo)
				if libraryEvent.MessageType != "LibraryChanged" || !ok || library.IsEmpty ||
					!reflect.DeepEqual(library.ItemsUpdated, []string{itemID}) ||
					len(library.FoldersAddedTo) != 0 || len(library.FoldersRemovedFrom) != 0 ||
					len(library.ItemsAdded) != 0 || len(library.ItemsRemoved) != 0 || len(library.CollectionFolders) != 0 {
					t.Fatalf("owner %d library event=%+v", index+1, libraryEvent)
				}
				encoded, err := json.Marshal(libraryEvent)
				if err != nil || !strings.Contains(string(encoded), `"ItemsUpdated":["`+itemID+`"]`) ||
					!strings.Contains(string(encoded), `"ItemsAdded":[]`) ||
					!strings.Contains(string(encoded), `"ItemsRemoved":[]`) {
					t.Fatalf("owner %d serialized library event=%s err=%v", index+1, encoded, err)
				}
			}
			select {
			case leaked := <-foreign.outbound:
				t.Fatalf("mutation leaked across profiles: %+v", leaked)
			default:
			}
		})
	}
}

func TestFailedUserDataMutationPublishesNoEvent(t *testing.T) {
	handler, authentication, service, _, token, itemID, _, _ := stateHTTPFixture(t)
	handler.bootstrap = newBootstrapRegistry()
	ownerOne, ownerTwo, foreign := acquireMutationEventSockets(t, handler, authentication.session)
	defer handler.bootstrap.releaseSocket(ownerOne)
	defer handler.bootstrap.releaseSocket(ownerTwo)
	defer handler.bootstrap.releaseSocket(foreign)
	service.userDataErr = watchstate.ErrOutboxCapacity

	request := httptest.NewRequest(http.MethodPost, "/UserItems/"+itemID+"/Favorite", nil)
	request.SetPathValue("itemId", itemID)
	request.Header.Set("X-Emby-Token", token)
	response := httptest.NewRecorder()
	handler.handleFavoriteItem(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed mutation status=%d body=%s", response.Code, response.Body.String())
	}
	for name, lease := range map[string]*compatSocketLease{"owner one": ownerOne, "owner two": ownerTwo, "foreign": foreign} {
		select {
		case event := <-lease.outbound:
			t.Fatalf("%s received event for failed mutation: %+v", name, event)
		default:
		}
	}
}

func acquireMutationEventSockets(t *testing.T, handler *Handler, owner AuthenticatedSession) (*compatSocketLease, *compatSocketLease, *compatSocketLease) {
	t.Helper()
	ownerOne, ok := handler.bootstrap.acquireSocket(owner)
	if !ok {
		t.Fatal("acquire first owner socket")
	}
	second := owner
	second.ID = "55555555-5555-4555-8555-555555555555"
	second.Principal.SessionID = "66666666-6666-4666-8666-666666666666"
	ownerTwo, ok := handler.bootstrap.acquireSocket(second)
	if !ok {
		handler.bootstrap.releaseSocket(ownerOne)
		t.Fatal("acquire second owner socket")
	}
	foreignSession := owner
	foreignSession.ID = "77777777-7777-4777-8777-777777777777"
	foreignSession.ProfileID = "88888888-8888-4888-8888-888888888888"
	foreignSession.Principal.SessionID = "99999999-9999-4999-8999-999999999999"
	foreignSession.Principal.ActiveProfileID = &foreignSession.ProfileID
	foreign, ok := handler.bootstrap.acquireSocket(foreignSession)
	if !ok {
		handler.bootstrap.releaseSocket(ownerOne)
		handler.bootstrap.releaseSocket(ownerTwo)
		t.Fatal("acquire foreign socket")
	}
	return ownerOne, ownerTwo, foreign
}
