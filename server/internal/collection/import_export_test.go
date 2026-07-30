package collection

import (
	"errors"
	"testing"
)

func TestPortableCollectionRemovesRuntimeIDsAndAddsAddonIdentity(t *testing.T) {
	value := Collection{
		Title:            "Curated",
		ViewMode:         ViewModeRows,
		FolderCoverShape: TileShapeLandscape,
		Folders: []Folder{{
			ID:    "11111111-1111-4111-8111-111111111111",
			Title: "Featured",
			Sources: []Source{{
				ID:           "22222222-2222-4222-8222-222222222222",
				Kind:         SourceKindAddonCatalog,
				Title:        "Popular",
				AddonCatalog: &AddonCatalogSource{AddonID: "33333333-3333-4333-8333-333333333333", Type: "movie", CatalogID: "popular"},
			}},
		}},
	}
	portable := portableCollection(value, map[string]addonIdentity{
		"33333333-3333-4333-8333-333333333333": {
			ID: "33333333-3333-4333-8333-333333333333", ManifestID: "org.example.metadata",
		},
	})

	folder := portable.Folders[0]
	source := folder.Sources[0]
	if folder.ID != "" || source.ID != "" {
		t.Fatalf("runtime IDs leaked into portable collection: folder=%q source=%q", folder.ID, source.ID)
	}
	if source.AddonCatalog.ManifestID != "org.example.metadata" {
		t.Fatalf("addon identity was not exported: %+v", source.AddonCatalog)
	}
}

func TestPrepareImportedCollectionResolvesAddonAndRegeneratesIDs(t *testing.T) {
	input := SaveInput{
		Title: "Curated",
		Folders: []Folder{{
			ID:    "11111111-1111-4111-8111-111111111111",
			Title: "Featured",
			Sources: []Source{{
				ID:           "22222222-2222-4222-8222-222222222222",
				Kind:         SourceKindAddonCatalog,
				Title:        "Popular",
				AddonCatalog: &AddonCatalogSource{ManifestID: "org.example.metadata", Type: "movie", CatalogID: "popular"},
			}},
		}},
	}
	identities := addonIdentitySet{
		byID:       map[string]addonIdentity{},
		byManifest: map[string]string{"org.example.metadata": "33333333-3333-4333-8333-333333333333"},
	}

	if err := prepareImportedCollection(&input, identities); err != nil {
		t.Fatalf("prepare import: %v", err)
	}
	if input.Folders[0].ID != "" || input.Folders[0].Sources[0].ID != "" {
		t.Fatal("import did not discard exported runtime IDs")
	}
	addon := input.Folders[0].Sources[0].AddonCatalog
	if addon.AddonID != "33333333-3333-4333-8333-333333333333" || addon.ManifestID != "" {
		t.Fatalf("unexpected resolved addon reference: %+v", addon)
	}
}

func TestPrepareImportedCollectionRejectsMissingPortableAddon(t *testing.T) {
	input := SaveInput{
		Title: "Curated",
		Folders: []Folder{{Title: "Featured", Sources: []Source{{
			Kind:         SourceKindAddonCatalog,
			Title:        "Popular",
			AddonCatalog: &AddonCatalogSource{ManifestID: "org.example.missing", Type: "movie", CatalogID: "popular"},
		}}}},
	}
	identities := addonIdentitySet{byID: map[string]addonIdentity{}, byManifest: map[string]string{}}

	if err := prepareImportedCollection(&input, identities); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid input for missing addon, got %v", err)
	}
}
