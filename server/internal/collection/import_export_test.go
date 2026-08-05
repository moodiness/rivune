package collection

import (
	"errors"
	"strings"
	"testing"

	"github.com/moodiness/rivune/server/internal/addon"
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
	identity := catalogImportIdentity("33333333-3333-4333-8333-333333333333", "org.example.metadata", "catalog", "movie", "popular")
	identities := addonIdentitySet{
		byID:       map[string]addonIdentity{identity.ID: identity},
		byManifest: map[string][]addonIdentity{identity.ManifestID: {identity}},
		assigned:   []addonIdentity{identity},
	}

	if err := prepareImportedCollection(&input, identities); err != nil {
		t.Fatalf("prepare import: %v", err)
	}
	if input.Folders[0].ID != "" || input.Folders[0].Sources[0].ID != "" {
		t.Fatal("import did not discard exported runtime IDs")
	}
	addon := input.Folders[0].Sources[0].AddonCatalog
	if addon.AddonID != identity.ID || addon.ManifestID != identity.ManifestID {
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
	identities := addonIdentitySet{byID: map[string]addonIdentity{}, byManifest: map[string][]addonIdentity{}}

	if err := prepareImportedCollection(&input, identities); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid input for missing addon, got %v", err)
	}
}

func TestPrepareImportedCollectionRebindsUniqueLegacyAddonCatalog(t *testing.T) {
	identity := catalogImportIdentity("44444444-4444-4444-8444-444444444444", "org.example.reinstalled", "catalog", "movie", "popular")
	input := legacyAddonCatalogImport("33333333-3333-4333-8333-333333333333", "movie", "popular")
	identities := addonIdentitySet{
		byID:       map[string]addonIdentity{identity.ID: identity},
		byManifest: map[string][]addonIdentity{identity.ManifestID: {identity}},
		assigned:   []addonIdentity{identity},
	}

	if err := prepareImportedCollection(&input, identities); err != nil {
		t.Fatalf("prepare legacy import: %v", err)
	}
	settings := input.Folders[0].Sources[0].AddonCatalog
	if settings.AddonID != identity.ID || settings.ManifestID != identity.ManifestID {
		t.Fatalf("legacy addon identity was not rebound: %+v", settings)
	}
}

func TestPrepareImportedCollectionRejectsLegacyAddonWithoutCompatibleCandidate(t *testing.T) {
	identity := catalogImportIdentity("44444444-4444-4444-8444-444444444444", "org.example.unrelated", "catalog", "movie", "featured")
	input := legacyAddonCatalogImport("33333333-3333-4333-8333-333333333333", "movie", "popular")
	identities := addonIdentitySet{
		byID:       map[string]addonIdentity{identity.ID: identity},
		byManifest: map[string][]addonIdentity{identity.ManifestID: {identity}},
		assigned:   []addonIdentity{identity},
	}

	if err := prepareImportedCollection(&input, identities); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("legacy import without a compatible addon = %v, want ErrInvalidInput", err)
	}
}

func TestPrepareImportedCollectionRejectsAmbiguousLegacyAddon(t *testing.T) {
	first := catalogImportIdentity("44444444-4444-4444-8444-444444444444", "org.example.first", "catalog", "movie", "popular")
	second := catalogImportIdentity("55555555-5555-4555-8555-555555555555", "org.example.second", "catalog", "movie", "popular")
	input := legacyAddonCatalogImport("33333333-3333-4333-8333-333333333333", "movie", "popular")
	identities := addonIdentitySet{
		byID: map[string]addonIdentity{first.ID: first, second.ID: second},
		byManifest: map[string][]addonIdentity{
			first.ManifestID:  {first},
			second.ManifestID: {second},
		},
		assigned: []addonIdentity{first, second},
	}

	if err := prepareImportedCollection(&input, identities); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ambiguous legacy import = %v, want ErrInvalidInput", err)
	}
}

func TestPrepareImportedCollectionRequiresExactCatalogResourceTypeAndID(t *testing.T) {
	exact := catalogImportIdentity("44444444-4444-4444-8444-444444444444", "org.example.exact", "catalog", "movie", "popular")
	wrongID := catalogImportIdentity("55555555-5555-4555-8555-555555555555", "org.example.wrong-id", "catalog", "movie", "featured")
	wrongType := catalogImportIdentity("66666666-6666-4666-8666-666666666666", "org.example.wrong-type", "catalog", "series", "popular")
	wrongResource := catalogImportIdentity("77777777-7777-4777-8777-777777777777", "org.example.wrong-resource", "addon_catalog", "movie", "popular")
	input := legacyAddonCatalogImport("33333333-3333-4333-8333-333333333333", "movie", "popular")
	identities := addonIdentitySet{
		byID: map[string]addonIdentity{
			exact.ID: exact, wrongID.ID: wrongID, wrongType.ID: wrongType, wrongResource.ID: wrongResource,
		},
		byManifest: map[string][]addonIdentity{
			exact.ManifestID:         {exact},
			wrongID.ManifestID:       {wrongID},
			wrongType.ManifestID:     {wrongType},
			wrongResource.ManifestID: {wrongResource},
		},
		assigned: []addonIdentity{wrongID, wrongType, wrongResource, exact},
	}

	if err := prepareImportedCollection(&input, identities); err != nil {
		t.Fatalf("prepare exact legacy catalog: %v", err)
	}
	settings := input.Folders[0].Sources[0].AddonCatalog
	if settings.AddonID != exact.ID || settings.ManifestID != exact.ManifestID {
		t.Fatalf("exact catalog candidate was not selected: %+v", settings)
	}
}

func TestPrepareImportedCollectionUsesExplicitManifestBeforeLegacyCompatibility(t *testing.T) {
	explicit := catalogImportIdentity("44444444-4444-4444-8444-444444444444", "org.example.explicit", "catalog", "movie", "popular")
	other := catalogImportIdentity("55555555-5555-4555-8555-555555555555", "org.example.other", "catalog", "movie", "popular")
	input := legacyAddonCatalogImport("33333333-3333-4333-8333-333333333333", "movie", "popular")
	input.Folders[0].Sources[0].AddonCatalog.ManifestID = explicit.ManifestID
	identities := addonIdentitySet{
		byID: map[string]addonIdentity{explicit.ID: explicit, other.ID: other},
		byManifest: map[string][]addonIdentity{
			explicit.ManifestID: {explicit},
			other.ManifestID:    {other},
		},
		assigned: []addonIdentity{other, explicit},
	}

	if err := prepareImportedCollection(&input, identities); err != nil {
		t.Fatalf("prepare explicit manifest import: %v", err)
	}
	if got := input.Folders[0].Sources[0].AddonCatalog.AddonID; got != explicit.ID {
		t.Fatalf("explicit manifest resolved addon = %q, want %q", got, explicit.ID)
	}
}

func TestPrepareImportedCollectionUsesValidCurrentAddonBeforeManifest(t *testing.T) {
	current := catalogImportIdentity("33333333-3333-4333-8333-333333333333", "org.example.current", "catalog", "movie", "popular")
	explicit := catalogImportIdentity("44444444-4444-4444-8444-444444444444", "org.example.explicit", "catalog", "movie", "popular")
	input := legacyAddonCatalogImport(current.ID, "movie", "popular")
	input.Folders[0].Sources[0].AddonCatalog.ManifestID = explicit.ManifestID
	identities := addonIdentitySet{
		byID: map[string]addonIdentity{current.ID: current, explicit.ID: explicit},
		byManifest: map[string][]addonIdentity{
			current.ManifestID:  {current},
			explicit.ManifestID: {explicit},
		},
		assigned: []addonIdentity{current, explicit},
	}

	if err := prepareImportedCollection(&input, identities); err != nil {
		t.Fatalf("prepare current addon import: %v", err)
	}
	settings := input.Folders[0].Sources[0].AddonCatalog
	if settings.AddonID != current.ID || settings.ManifestID != current.ManifestID {
		t.Fatalf("valid current addon did not retain precedence: %+v", settings)
	}
}

func TestPrepareImportedCollectionRejectsAmbiguousExplicitManifest(t *testing.T) {
	first := catalogImportIdentity("44444444-4444-4444-8444-444444444444", "org.example.duplicate", "catalog", "movie", "popular")
	second := catalogImportIdentity("55555555-5555-4555-8555-555555555555", first.ManifestID, "catalog", "movie", "popular")
	input := legacyAddonCatalogImport("33333333-3333-4333-8333-333333333333", "movie", "popular")
	input.Folders[0].Sources[0].AddonCatalog.ManifestID = first.ManifestID
	identities := addonIdentitySet{
		byID:       map[string]addonIdentity{first.ID: first, second.ID: second},
		byManifest: map[string][]addonIdentity{first.ManifestID: {first, second}},
		assigned:   []addonIdentity{first, second},
	}

	if err := prepareImportedCollection(&input, identities); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ambiguous explicit manifest import = %v, want ErrInvalidInput", err)
	}
}

func legacyAddonCatalogImport(addonID, mediaType, catalogID string) SaveInput {
	return SaveInput{
		Title: "Legacy",
		Folders: []Folder{{Title: "Discover", Sources: []Source{{
			Kind:  SourceKindAddonCatalog,
			Title: "Popular",
			AddonCatalog: &AddonCatalogSource{
				AddonID: addonID, Type: mediaType, CatalogID: catalogID,
			},
		}}}},
	}
}

func catalogImportIdentity(id, manifestID, resource, mediaType, catalogID string) addonIdentity {
	manifest := addon.Manifest{
		ID:        manifestID,
		Types:     []string{mediaType},
		Resources: []addon.ManifestResource{{Name: resource, Short: true}},
	}
	if resource == "addon_catalog" {
		manifest.AddonCatalogs = []addon.ManifestCatalog{{Type: mediaType, ID: catalogID}}
	} else {
		manifest.Catalogs = []addon.ManifestCatalog{{Type: mediaType, ID: catalogID}}
	}
	return addonIdentity{ID: id, ManifestID: manifestID, parsedManifest: manifest}
}

func TestCollectionImportDocumentBudgetBoundsAggregateNestedValues(t *testing.T) {
	collections := make([]SaveInput, maximumImportFolders/maximumFolders)
	for collectionIndex := range collections {
		collections[collectionIndex].Title = "Bounded"
		collections[collectionIndex].Folders = make([]Folder, maximumFolders)
		for folderIndex := range collections[collectionIndex].Folders {
			collections[collectionIndex].Folders[folderIndex].Sources =
				make([]Source, maximumImportSources/maximumImportFolders)
		}
	}
	document := ExportDocument{SchemaVersion: ExportSchemaVersion, Collections: collections}
	if err := validateImportDocumentBudget(document); err != nil {
		t.Fatalf("document at aggregate source limit: %v", err)
	}
	document.Collections[0].Folders[0].Sources = append(document.Collections[0].Folders[0].Sources, Source{})
	if err := validateImportDocumentBudget(document); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("document above aggregate source limit = %v, want ErrInvalidInput", err)
	}
}

func TestCollectionImportDocumentBudgetBoundsAggregateStrings(t *testing.T) {
	document := ExportDocument{
		SchemaVersion: ExportSchemaVersion,
		Collections: []SaveInput{{
			Title: strings.Repeat("x", maximumImportStringBytes+1),
		}},
	}
	if err := validateImportDocumentBudget(document); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("document above aggregate string limit = %v, want ErrInvalidInput", err)
	}
}
