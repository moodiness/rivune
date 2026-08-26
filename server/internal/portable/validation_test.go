package portable

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/collection"
)

func validDocument() Document {
	return Document{
		Version:    DocumentVersion,
		ExportedAt: time.Now().UTC(),
		Identity: Identity{Name: "Portable profile", Avatar: Avatar{Kind: "preset", PresetID: "aurora"}},
		Addons: []Addon{{
			Key:          "sha256:" + strings.Repeat("a", 64),
			TransportURL: "https://addon.example/manifest.json?token=secret",
			Manifest:     json.RawMessage(`{"id":"org.example.portable","version":"1.0.0","name":"Portable","types":["movie"],"resources":["meta"],"catalogs":[]}`),
			Enabled:      true,
		}},
		Collections: []PortableCollection{}, Titles: []Title{}, Library: []LibraryState{}, Progress: []ProgressState{}, Favorites: []FavoriteState{}, UserData: []UserDataState{}, ContinueDismissals: []ContinueDismissal{}, TrackingPreferences: []TrackingPreference{},
	}
}

func TestValidateRejectsVersionBudgetAndDuplicateKeysBeforePersistence(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Document)
	}{
		{"version", func(value *Document) { value.Version = 1 }},
		{"duplicate addon", func(value *Document) { value.Addons = append(value.Addons, value.Addons[0]) }},
		{"collection assignment", func(value *Document) {
			value.Collections = []PortableCollection{{Key: "sha256:" + strings.Repeat("b", 64), Value: collection.SaveInput{ProfileIDs: []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := validDocument()
			test.mutate(&document)
			if err := Validate(document, time.Now().UTC()); !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
	document := validDocument()
	document.Titles = []Title{{
		Key:       "sha256:" + strings.Repeat("b", 64),
		MediaType: "movie",
		ExternalIDs: []ExternalID{
			{Provider: "addon", Namespace: "movie", ExternalID: "one", ProfileScoped: true},
			{Provider: "addon", Namespace: "movie", ExternalID: "two", ProfileScoped: true},
		},
	}}
	if err := Validate(document, time.Now().UTC()); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("multiple scoped identities error = %v", err)
	}
}

func TestValidateAcceptsExplicitSecretAddonURLWithoutLeakingItElsewhere(t *testing.T) {
	document := validDocument()
	if err := Validate(document, time.Now().UTC()); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(encoded), "token=secret") != 1 {
		t.Fatalf("secret URL occurrence count = %d", strings.Count(string(encoded), "token=secret"))
	}
}

func TestDocumentJSONRejectsMissingOrNullRequiredMembers(t *testing.T) {
	encoded, err := json.Marshal(validDocument())
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &members); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"identity", "settings", "addons", "collections", "titles", "library", "progress", "favorites", "userData", "continueDismissals", "trackingPreferences"} {
		t.Run(name+" missing", func(t *testing.T) {
			copy := make(map[string]json.RawMessage, len(members))
			for key, value := range members {
				copy[key] = value
			}
			delete(copy, name)
			body, _ := json.Marshal(copy)
			var document Document
			if err := json.Unmarshal(body, &document); err == nil {
				t.Fatal("missing member was accepted")
			}
		})
		t.Run(name+" null", func(t *testing.T) {
			copy := make(map[string]json.RawMessage, len(members))
			for key, value := range members {
				copy[key] = value
			}
			copy[name] = json.RawMessage("null")
			body, _ := json.Marshal(copy)
			var document Document
			if err := json.Unmarshal(body, &document); err == nil {
				t.Fatal("null member was accepted")
			}
		})
	}
	var roundTrip Document
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("export-shaped document rejected: %v", err)
	}
}

func TestDocumentJSONRequiresEveryNestedOpenAPIFieldAndAcceptsExplicitZeroValues(t *testing.T) {
	document := validDocument()
	document.Addons[0].Enabled = false
	document.Addons[0].Position = 0
	document.Collections = []PortableCollection{{
		Key: "sha256:" + strings.Repeat("b", 64),
		Value: collection.SaveInput{
			Title: "Required", HeroEnabled: false, PinToTop: false, FocusGlowEnabled: false,
			ViewMode: collection.ViewModeRows, FolderCoverShape: collection.TileShapePoster,
			Folders: []collection.Folder{{Title: "Folder", TileShape: collection.TileShapePoster, SourceView: collection.SourceViewMerged, FocusGIFEnabled: false, HideTitle: false, Sources: []collection.Source{{Kind: collection.SourceKindAddonCatalog, Title: "Source", AddonCatalog: &collection.AddonCatalogSource{Type: "movie", CatalogID: "featured", Extra: []collection.ExtraValue{{Name: "skip", Value: "0"}}}}}}},
		},
	}}
	document.Titles = []Title{{Key: "sha256:" + strings.Repeat("c", 64), MediaType: "movie", ExternalIDs: []ExternalID{{Provider: "tmdb", Namespace: "movie", ExternalID: "0", ProfileScoped: false}}}}
	now := time.Now().UTC()
	document.Library = []LibraryState{{TitleKey: document.Titles[0].Key, AddedAt: now, UpdatedAt: now}}
	document.Progress = []ProgressState{{TitleKey: document.Titles[0].Key, PositionSeconds: 0, DurationSeconds: 0, Completed: false, Version: 0, LastWatchedAt: now, UpdatedAt: now}}
	document.Favorites = []FavoriteState{{TitleKey: document.Titles[0].Key, CreatedAt: now, UpdatedAt: now}}
	document.UserData = []UserDataState{{TitleKey: document.Titles[0].Key, RatingSet: false, PlayedPercentageSet: false, UnplayedItemCountSet: false, PlayCountSet: false, LikesSet: false, LastPlayedDateSet: false, UpdatedAt: now}}
	document.TrackingPreferences = []TrackingPreference{{Provider: "trakt", SyncWatched: false, SyncProgress: false, SyncLibrary: false}}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var accepted Document
	if err := json.Unmarshal(encoded, &accepted); err != nil {
		t.Fatalf("explicit false/zero required values rejected: %v", err)
	}
	paths := [][]any{
		{"addons", 0, "key"}, {"addons", 0, "transportUrl"}, {"addons", 0, "manifest"}, {"addons", 0, "enabled"}, {"addons", 0, "position"},
		{"collections", 0, "key"}, {"collections", 0, "value"},
		{"collections", 0, "value", "title"}, {"collections", 0, "value", "heroEnabled"}, {"collections", 0, "value", "pinToTop"}, {"collections", 0, "value", "focusGlowEnabled"}, {"collections", 0, "value", "viewMode"}, {"collections", 0, "value", "folderCoverShape"}, {"collections", 0, "value", "folders"},
		{"collections", 0, "value", "folders", 0, "title"}, {"collections", 0, "value", "folders", 0, "tileShape"}, {"collections", 0, "value", "folders", 0, "sourceView"}, {"collections", 0, "value", "folders", 0, "focusGifEnabled"}, {"collections", 0, "value", "folders", 0, "hideTitle"}, {"collections", 0, "value", "folders", 0, "sources"},
		{"collections", 0, "value", "folders", 0, "sources", 0, "kind"}, {"collections", 0, "value", "folders", 0, "sources", 0, "title"}, {"collections", 0, "value", "folders", 0, "sources", 0, "addonCatalog", "type"}, {"collections", 0, "value", "folders", 0, "sources", 0, "addonCatalog", "catalogId"}, {"collections", 0, "value", "folders", 0, "sources", 0, "addonCatalog", "extra", 0, "name"}, {"collections", 0, "value", "folders", 0, "sources", 0, "addonCatalog", "extra", 0, "value"},
		{"titles", 0, "key"}, {"titles", 0, "mediaType"}, {"titles", 0, "externalIds"}, {"titles", 0, "externalIds", 0, "provider"}, {"titles", 0, "externalIds", 0, "namespace"}, {"titles", 0, "externalIds", 0, "externalId"}, {"titles", 0, "externalIds", 0, "profileScoped"},
		{"library", 0, "titleKey"}, {"library", 0, "addedAt"}, {"library", 0, "updatedAt"},
		{"progress", 0, "titleKey"}, {"progress", 0, "positionSeconds"}, {"progress", 0, "durationSeconds"}, {"progress", 0, "completed"}, {"progress", 0, "version"}, {"progress", 0, "lastWatchedAt"}, {"progress", 0, "updatedAt"},
		{"favorites", 0, "titleKey"}, {"favorites", 0, "createdAt"}, {"favorites", 0, "updatedAt"},
		{"userData", 0, "titleKey"}, {"userData", 0, "ratingSet"}, {"userData", 0, "playedPercentageSet"}, {"userData", 0, "unplayedItemCountSet"}, {"userData", 0, "playCountSet"}, {"userData", 0, "likesSet"}, {"userData", 0, "lastPlayedDateSet"}, {"userData", 0, "updatedAt"},
		{"trackingPreferences", 0, "provider"}, {"trackingPreferences", 0, "syncWatched"}, {"trackingPreferences", 0, "syncProgress"}, {"trackingPreferences", 0, "syncLibrary"},
	}
	for _, path := range paths {
		t.Run(fmt.Sprint(path), func(t *testing.T) {
			var value any
			if err := json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			removeJSONMember(t, value, path)
			body, _ := json.Marshal(value)
			var decoded Document
			if err := json.Unmarshal(body, &decoded); err == nil {
				t.Fatal("missing required nested member was accepted")
			}
		})
	}
}

func removeJSONMember(t *testing.T, value any, path []any) {
	t.Helper()
	current := value
	for _, component := range path[:len(path)-1] {
		switch key := component.(type) {
		case string:
			current = current.(map[string]any)[key]
		case int:
			current = current.([]any)[key]
		}
	}
	delete(current.(map[string]any), path[len(path)-1].(string))
}
