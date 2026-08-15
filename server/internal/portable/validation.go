package portable

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/collection"
	"github.com/moodiness/rivune/server/internal/settings"
)

var (
	portableKeyPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	providerPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
)

func invalid(message string) error { return fmt.Errorf("%w: %s", ErrInvalidDocument, message) }

func Validate(document Document, now time.Time) error {
	if document.Version != DocumentVersion {
		return invalid("unsupported version")
	}
	encoded, err := json.Marshal(document)
	if err != nil || len(encoded) > MaximumDocumentBytes {
		return invalid("document exceeds the 16 MiB limit")
	}
	if document.ExportedAt.IsZero() || !validArchiveTime(document.ExportedAt, now) {
		return invalid("exportedAt is outside the supported range")
	}
	if err := settings.ValidatePortableProfileValues(document.Settings); err != nil {
		return invalid("settings are invalid")
	}
	if len(document.Addons) > maximumAddons || len(document.Collections) > maximumCollections || len(document.Titles) > maximumTitles ||
		len(document.Library) > maximumStateRows || len(document.Progress) > maximumStateRows ||
		len(document.Favorites) > maximumStateRows || len(document.UserData) > maximumStateRows {
		return invalid("section cardinality limit exceeded")
	}
	collectionInputs := make([]collection.SaveInput, len(document.Collections))
	addonKeys := make(map[string]struct{}, len(document.Addons))
	addonPositions := make(map[int]struct{}, len(document.Addons))
	addonReferences := make(map[string]string, len(document.Addons))
	addonManifestIDs := make(map[string]string, len(document.Addons))
	addonManifests := make(map[string]addon.Manifest, len(document.Addons))
	for index := range document.Addons {
		value := &document.Addons[index]
		if err := addUniqueKey(addonKeys, value.Key, "add-on"); err != nil {
			return err
		}
		if value.Position < 0 {
			return invalid("add-on position must be nonnegative")
		}
		if _, duplicate := addonPositions[value.Position]; duplicate {
			return invalid("duplicate add-on position")
		}
		addonPositions[value.Position] = struct{}{}
		normalizedURL, err := addon.NormalizeTransportURL(value.TransportURL)
		if err != nil || normalizedURL != value.TransportURL {
			return invalid(fmt.Sprintf("add-ons[%d].transportUrl is invalid", index))
		}
		manifest, compact, err := addon.ParseManifest(value.Manifest)
		if err != nil || len(compact) == 0 || manifest.ID == "" {
			return invalid(fmt.Sprintf("add-ons[%d].manifest is invalid", index))
		}
		addonReferences[deterministicUUID(value.Key, "archive-addon-ref")] = value.Key
		addonManifestIDs[value.Key] = manifest.ID
		addonManifests[value.Key] = manifest
	}
	collectionKeys := make(map[string]struct{}, len(document.Collections))
	for index := range document.Collections {
		value := &document.Collections[index]
		if err := addUniqueKey(collectionKeys, value.Key, "collection"); err != nil {
			return err
		}
		input := &value.Value
		if input.ProfileIDs != nil || input.CategoryIDs != nil || input.ExpectedVersion != 0 {
			return invalid("collections cannot carry assignments or runtime versions")
		}
		for _, folder := range input.Folders {
			if folder.ID != "" {
				return invalid("collections cannot carry source folder IDs")
			}
			for _, source := range folder.Sources {
				if source.ID != "" {
					return invalid("collections cannot carry source IDs")
				}
				if source.AddonCatalog != nil {
					key, exists := addonReferences[source.AddonCatalog.AddonID]
					if !exists || source.AddonCatalog.ManifestID != addonManifestIDs[key] {
						return invalid("collection references an add-on outside the archive")
					}
				}
			}
		}
		collectionInputs[index] = *input
		encodedInput, err := json.Marshal(input)
		if err != nil {
			return invalid(fmt.Sprintf("collections[%d] cannot be encoded", index))
		}
		var validationInput collection.SaveInput
		if err := json.Unmarshal(encodedInput, &validationInput); err != nil {
			return invalid(fmt.Sprintf("collections[%d] cannot be decoded", index))
		}
		if _, err := collection.NormalizePortable(validationInput); err != nil {
			return invalid(fmt.Sprintf("collections[%d] is invalid: %v", index, err))
		}
		for _, folder := range input.Folders {
			for _, source := range folder.Sources {
				if source.AddonCatalog == nil {
					continue
				}
				key := addonReferences[source.AddonCatalog.AddonID]
				extra := make([]addon.ExtraValue, len(source.AddonCatalog.Extra))
				for extraIndex, value := range source.AddonCatalog.Extra {
					extra[extraIndex] = addon.ExtraValue{Name: value.Name, Value: value.Value}
				}
				path := addonManifests[key].ApplyCatalogDefaults(addon.ResourcePath{
					Resource: "catalog", Type: source.AddonCatalog.Type,
					ID: source.AddonCatalog.CatalogID, Extra: extra,
				})
				if !addonManifests[key].Supports(path) {
					return invalid("collection source is not declared by its archived add-on manifest")
				}
			}
		}
	}
	if err := collection.ValidatePortableBudget(collectionInputs); err != nil {
		return invalid("collections exceed the document complexity limit")
	}
	if err := validateTitlesAndState(document, now, addonKeys); err != nil {
		return err
	}
	providers := make(map[string]struct{}, len(document.TrackingPreferences))
	for _, preference := range document.TrackingPreferences {
		if preference.Provider != "trakt" && preference.Provider != "simkl" {
			return invalid("unsupported tracking preference provider")
		}
		if _, duplicate := providers[preference.Provider]; duplicate {
			return invalid("duplicate tracking preference provider")
		}
		providers[preference.Provider] = struct{}{}
	}
	return nil
}

func validateTitlesAndState(document Document, now time.Time, addonKeys map[string]struct{}) error {
	titles := make(map[string]Title, len(document.Titles))
	globalIdentities := make(map[string]struct{})
	profileIdentities := make(map[string]struct{})
	for _, title := range document.Titles {
		if err := addUniqueTitle(titles, title); err != nil {
			return err
		}
		if err := validateTitle(title, addonKeys, globalIdentities, profileIdentities); err != nil {
			return err
		}
	}
	for _, title := range document.Titles {
		if title.ParentKey != "" {
			parent, exists := titles[title.ParentKey]
			if !exists || (title.MediaType == "season" && parent.MediaType != "series") || (title.MediaType == "episode" && parent.MediaType != "season") {
				return invalid("title hierarchy is inconsistent")
			}
		}
	}
	if err := rejectTitleCycles(titles); err != nil {
		return err
	}
	seenLibrary := make(map[string]struct{})
	for _, state := range document.Library {
		if err := validateStateReference(state.TitleKey, titles, seenLibrary, "library"); err != nil || !validArchiveTime(state.AddedAt, now) || !validArchiveTime(state.UpdatedAt, now) || state.UpdatedAt.Before(state.AddedAt) {
			if err != nil {
				return err
			}
			return invalid("library timestamps are invalid")
		}
		mediaType := titles[state.TitleKey].MediaType
		if mediaType != "movie" && mediaType != "series" && mediaType != "tv" {
			return invalid("library title type is invalid")
		}
	}
	seenProgress := make(map[string]struct{})
	for _, state := range document.Progress {
		if err := validateStateReference(state.TitleKey, titles, seenProgress, "progress"); err != nil {
			return err
		}
		mediaType := titles[state.TitleKey].MediaType
		if (mediaType != "movie" && mediaType != "episode") || state.PositionSeconds < 0 || state.DurationSeconds < 0 ||
			(state.DurationSeconds != 0 && state.PositionSeconds > state.DurationSeconds) || state.Version < 1 ||
			!validArchiveTime(state.LastWatchedAt, now) || !validArchiveTime(state.UpdatedAt, now) {
			return invalid("progress state is invalid")
		}
	}
	seenFavorites := make(map[string]struct{})
	for _, state := range document.Favorites {
		if err := validateStateReference(state.TitleKey, titles, seenFavorites, "favorite"); err != nil {
			return err
		}
		if !validArchiveTime(state.CreatedAt, now) || !validArchiveTime(state.UpdatedAt, now) || state.UpdatedAt.Before(state.CreatedAt) {
			return invalid("favorite timestamps are invalid")
		}
	}
	seenUserData := make(map[string]struct{})
	for _, state := range document.UserData {
		if err := validateStateReference(state.TitleKey, titles, seenUserData, "user data"); err != nil {
			return err
		}
		if !validUserData(state, now) {
			return invalid("user data state is invalid")
		}
	}
	return nil
}

func addUniqueTitle(titles map[string]Title, title Title) error {
	if !portableKeyPattern.MatchString(title.Key) {
		return invalid("title key is invalid")
	}
	if _, duplicate := titles[title.Key]; duplicate {
		return invalid("duplicate title key")
	}
	titles[title.Key] = title
	return nil
}

func validateTitle(title Title, addonKeys, global, scoped map[string]struct{}) error {
	if title.MediaType != "movie" && title.MediaType != "series" && title.MediaType != "season" && title.MediaType != "episode" && title.MediaType != "tv" {
		return invalid("unsupported title media type")
	}
	child := title.MediaType == "season" || title.MediaType == "episode"
	if child != (title.ParentKey != "" && title.Ordinal != nil) || title.Ordinal != nil && *title.Ordinal < 0 {
		return invalid("title hierarchy fields are invalid")
	}
	if title.ParentKey != "" && !portableKeyPattern.MatchString(title.ParentKey) {
		return invalid("title parent key is invalid")
	}
	if title.SourceAddonKey != "" {
		if _, ok := addonKeys[title.SourceAddonKey]; !ok {
			return invalid("title references an add-on outside the archive")
		}
	}
	if !validText(title.DisplayTitle, 500) || !validText(title.ReleaseInfo, 120) || !validText(title.ResourceID, 512) || !validText(title.SourceCatalogID, 512) || !validText(title.SourceName, 500) || !validText(title.Country, 128) || !validText(title.Language, 128) || !validText(title.Category, 256) || len(title.PosterURL) > 4096 || len(title.BackgroundURL) > 4096 {
		return invalid("title snapshot is invalid")
	}
	if title.ResourceProvider != "" && !providerPattern.MatchString(title.ResourceProvider) {
		return invalid("title resource provider is invalid")
	}
	if title.ReleaseDate != "" {
		parsed, err := time.Parse(time.DateOnly, title.ReleaseDate)
		if err != nil || parsed.Year() < 1 || parsed.Format(time.DateOnly) != title.ReleaseDate {
			return invalid("title release date is invalid")
		}
	}
	seen := make(map[string]struct{}, len(title.ExternalIDs))
	scopedIdentities := 0
	for _, identity := range title.ExternalIDs {
		if !providerPattern.MatchString(identity.Provider) || identity.ExternalID == "" || len(identity.ExternalID) > 512 || strings.TrimSpace(identity.ExternalID) != identity.ExternalID || identity.Namespace != title.MediaType {
			return invalid("title external identity is invalid")
		}
		if !identity.ProfileScoped && len(identity.ExternalID) > 128 {
			return invalid("global title external identity is too long")
		}
		key := identity.Provider + "\x00" + identity.Namespace + "\x00" + identity.ExternalID
		if _, duplicate := seen[key]; duplicate {
			return invalid("duplicate title external identity")
		}
		seen[key] = struct{}{}
		target := global
		if identity.ProfileScoped {
			target = scoped
			scopedIdentities++
			if scopedIdentities > 1 {
				return invalid("title has multiple profile-scoped identities")
			}
		}
		if _, duplicate := target[key]; duplicate {
			return invalid("external identity belongs to multiple titles")
		}
		target[key] = struct{}{}
	}
	return nil
}

func rejectTitleCycles(titles map[string]Title) error {
	for key := range titles {
		seen := make(map[string]struct{})
		for current := key; current != ""; current = titles[current].ParentKey {
			if _, duplicate := seen[current]; duplicate {
				return invalid("title hierarchy contains a cycle")
			}
			seen[current] = struct{}{}
		}
	}
	return nil
}

func validateStateReference(key string, titles map[string]Title, seen map[string]struct{}, section string) error {
	if _, exists := titles[key]; !exists {
		return invalid(section + " references an unknown title")
	}
	if _, duplicate := seen[key]; duplicate {
		return invalid("duplicate " + section + " title")
	}
	seen[key] = struct{}{}
	return nil
}

func validUserData(state UserDataState, now time.Time) bool {
	if !validArchiveTime(state.UpdatedAt, now) || state.Rating != nil && (*state.Rating < 0 || *state.Rating > 10) || state.PlayedPercentage != nil && (*state.PlayedPercentage < 0 || *state.PlayedPercentage > 100) || state.UnplayedItemCount != nil && *state.UnplayedItemCount < 0 || state.PlayCount != nil && *state.PlayCount < 0 {
		return false
	}
	if !state.RatingSet && state.Rating != nil || !state.PlayedPercentageSet && state.PlayedPercentage != nil || !state.UnplayedItemCountSet && state.UnplayedItemCount != nil || state.PlayCountSet != (state.PlayCount != nil) || !state.LikesSet && state.Likes != nil {
		return false
	}
	if !state.LastPlayedDateSet && (state.LastPlayedDate != nil || state.LastPlayedDateSubmicrosecond != nil) || (state.LastPlayedDate == nil) != (state.LastPlayedDateSubmicrosecond == nil) {
		return false
	}
	if state.LastPlayedDate != nil && !validArchiveTime(*state.LastPlayedDate, now) || state.LastPlayedDateSubmicrosecond != nil && (*state.LastPlayedDateSubmicrosecond < 0 || *state.LastPlayedDateSubmicrosecond > 999) {
		return false
	}
	return true
}

func addUniqueKey(values map[string]struct{}, key, kind string) error {
	if !portableKeyPattern.MatchString(key) {
		return invalid(kind + " key is invalid")
	}
	if _, duplicate := values[key]; duplicate {
		return invalid("duplicate " + kind + " key")
	}
	values[key] = struct{}{}
	return nil
}
func validText(value string, maximum int) bool {
	return len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00') && (value == "" || strings.TrimSpace(value) == value)
}
func validArchiveTime(value, now time.Time) bool {
	return !value.IsZero() && value.Year() >= 1 && value.Year() <= 9999 && !value.After(now.Add(5*time.Minute))
}
