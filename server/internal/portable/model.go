package portable

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/moodiness/rivune/server/internal/collection"
	"github.com/moodiness/rivune/server/internal/settings"
)

const (
	DocumentVersion      = 1
	MaximumDocumentBytes = 16 << 20
	maximumAddons        = 256
	maximumCollections   = 100
	maximumTitles        = 100_000
	maximumStateRows     = 100_000
)

var (
	ErrForbidden       = errors.New("portable archive operation forbidden")
	ErrProfileNotFound = errors.New("portable archive profile not found")
	ErrInvalidDocument = errors.New("invalid portable archive document")
	ErrConflict        = errors.New("portable archive conflicts with target data")
)

type Document struct {
	Version             int                  `json:"version"`
	ExportedAt          time.Time            `json:"exportedAt"`
	Settings            settings.Values      `json:"settings"`
	Addons              []Addon              `json:"addons"`
	Collections         []PortableCollection `json:"collections"`
	Titles              []Title              `json:"titles"`
	Library             []LibraryState       `json:"library"`
	Progress            []ProgressState      `json:"progress"`
	Favorites           []FavoriteState      `json:"favorites"`
	UserData            []UserDataState      `json:"userData"`
	TrackingPreferences []TrackingPreference `json:"trackingPreferences"`
}

func (document *Document) UnmarshalJSON(data []byte) error {
	if err := validateRequiredArchiveMembers(data); err != nil {
		return err
	}
	type plainDocument Document
	var decoded plainDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("portable archive contains multiple JSON values")
		}
		return err
	}
	*document = Document(decoded)
	return nil
}

func validateRequiredArchiveMembers(data []byte) error {
	root, err := requiredObject(data, "$", "version", "exportedAt", "settings", "addons", "collections", "titles", "library", "progress", "favorites", "userData", "trackingPreferences")
	if err != nil {
		return err
	}
	if err := validateRequiredArray(root["addons"], "addons", func(value json.RawMessage, path string) error {
		_, err := requiredObject(value, path, "key", "transportUrl", "manifest", "enabled", "position")
		return err
	}); err != nil {
		return err
	}
	if err := validateRequiredArray(root["collections"], "collections", validateRequiredCollection); err != nil {
		return err
	}
	if err := validateRequiredArray(root["titles"], "titles", func(value json.RawMessage, path string) error {
		members, err := requiredObject(value, path, "key", "mediaType", "externalIds")
		if err != nil {
			return err
		}
		return validateRequiredArray(members["externalIds"], path+".externalIds", func(identity json.RawMessage, identityPath string) error {
			_, err := requiredObject(identity, identityPath, "provider", "namespace", "externalId", "profileScoped")
			return err
		})
	}); err != nil {
		return err
	}
	for _, section := range []struct {
		name     string
		required []string
	}{
		{"library", []string{"titleKey", "addedAt", "updatedAt"}},
		{"progress", []string{"titleKey", "positionSeconds", "durationSeconds", "completed", "version", "lastWatchedAt", "updatedAt"}},
		{"favorites", []string{"titleKey", "createdAt", "updatedAt"}},
		{"userData", []string{"titleKey", "ratingSet", "playedPercentageSet", "unplayedItemCountSet", "playCountSet", "likesSet", "lastPlayedDateSet", "updatedAt"}},
		{"trackingPreferences", []string{"provider", "syncWatched", "syncProgress", "syncLibrary"}},
	} {
		if err := validateRequiredArray(root[section.name], section.name, func(value json.RawMessage, path string) error {
			_, err := requiredObject(value, path, section.required...)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func validateRequiredCollection(value json.RawMessage, path string) error {
	members, err := requiredObject(value, path, "key", "value")
	if err != nil {
		return err
	}
	contentPath := path + ".value"
	content, err := requiredObject(members["value"], contentPath, "title", "heroEnabled", "pinToTop", "focusGlowEnabled", "viewMode", "folderCoverShape", "folders")
	if err != nil {
		return err
	}
	return validateRequiredArray(content["folders"], contentPath+".folders", func(value json.RawMessage, folderPath string) error {
		folder, err := requiredObject(value, folderPath, "title", "tileShape", "sourceView", "focusGifEnabled", "hideTitle", "sources")
		if err != nil {
			return err
		}
		return validateRequiredArray(folder["sources"], folderPath+".sources", validateRequiredCollectionSource)
	})
}

func validateRequiredCollectionSource(value json.RawMessage, path string) error {
	source, err := requiredObject(value, path, "kind", "title")
	if err != nil {
		return err
	}
	for name, required := range map[string][]string{
		"addonCatalog": {"type", "catalogId"},
		"tmdb":         {"sourceType", "mediaType", "sort", "filters"},
		"trakt":        {"listId", "mediaType", "sortBy", "sortHow"},
		"mdblist":      {"listId", "mediaType", "sort", "order"},
	} {
		raw, exists := source[name]
		if !exists || isJSONNull(raw) {
			continue
		}
		config, err := requiredObject(raw, path+"."+name, required...)
		if err != nil {
			return err
		}
		if name == "addonCatalog" {
			if extra, exists := config["extra"]; exists && !isJSONNull(extra) {
				if err := validateRequiredArray(extra, path+".addonCatalog.extra", func(value json.RawMessage, extraPath string) error {
					_, err := requiredObject(value, extraPath, "name", "value")
					return err
				}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func requiredObject(data json.RawMessage, path string, required ...string) (map[string]json.RawMessage, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return nil, err
	}
	if members == nil {
		return nil, fmt.Errorf("portable archive member %q must be an object", path)
	}
	for _, name := range required {
		value, exists := members[name]
		if !exists || isJSONNull(value) {
			return nil, fmt.Errorf("required portable archive member %q is missing or null", path+"."+name)
		}
	}
	return members, nil
}

func validateRequiredArray(data json.RawMessage, path string, validate func(json.RawMessage, string) error) error {
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	if values == nil {
		return fmt.Errorf("portable archive member %q must be an array", path)
	}
	for index, value := range values {
		if err := validate(value, fmt.Sprintf("%s[%d]", path, index)); err != nil {
			return err
		}
	}
	return nil
}

func isJSONNull(value json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

type Addon struct {
	Key          string          `json:"key"`
	TransportURL string          `json:"transportUrl"`
	Manifest     json.RawMessage `json:"manifest"`
	Enabled      bool            `json:"enabled"`
	Position     int             `json:"position"`
}

type PortableCollection struct {
	Key   string               `json:"key"`
	Value collection.SaveInput `json:"value"`
}

type Title struct {
	Key              string       `json:"key"`
	MediaType        string       `json:"mediaType"`
	ParentKey        string       `json:"parentKey,omitempty"`
	Ordinal          *int         `json:"ordinal,omitempty"`
	DisplayTitle     string       `json:"displayTitle,omitempty"`
	PosterURL        string       `json:"posterUrl,omitempty"`
	BackgroundURL    string       `json:"backgroundUrl,omitempty"`
	ReleaseInfo      string       `json:"releaseInfo,omitempty"`
	ReleaseDate      string       `json:"releaseDate,omitempty"`
	ResourceID       string       `json:"resourceId,omitempty"`
	ResourceProvider string       `json:"resourceProvider,omitempty"`
	SourceAddonKey   string       `json:"sourceAddonKey,omitempty"`
	SourceCatalogID  string       `json:"sourceCatalogId,omitempty"`
	SourceName       string       `json:"sourceName,omitempty"`
	Country          string       `json:"country,omitempty"`
	Language         string       `json:"language,omitempty"`
	Category         string       `json:"category,omitempty"`
	ExternalIDs      []ExternalID `json:"externalIds"`
}

type ExternalID struct {
	Provider      string `json:"provider"`
	Namespace     string `json:"namespace"`
	ExternalID    string `json:"externalId"`
	ProfileScoped bool   `json:"profileScoped"`
}

type LibraryState struct {
	TitleKey  string    `json:"titleKey"`
	AddedAt   time.Time `json:"addedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ProgressState struct {
	TitleKey        string    `json:"titleKey"`
	PositionSeconds int       `json:"positionSeconds"`
	DurationSeconds int       `json:"durationSeconds"`
	Completed       bool      `json:"completed"`
	Version         int64     `json:"version"`
	LastWatchedAt   time.Time `json:"lastWatchedAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type FavoriteState struct {
	TitleKey  string    `json:"titleKey"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type UserDataState struct {
	TitleKey                     string     `json:"titleKey"`
	Rating                       *float64   `json:"rating,omitempty"`
	RatingSet                    bool       `json:"ratingSet"`
	PlayedPercentage             *float64   `json:"playedPercentage,omitempty"`
	PlayedPercentageSet          bool       `json:"playedPercentageSet"`
	UnplayedItemCount            *int       `json:"unplayedItemCount,omitempty"`
	UnplayedItemCountSet         bool       `json:"unplayedItemCountSet"`
	PlayCount                    *int       `json:"playCount,omitempty"`
	PlayCountSet                 bool       `json:"playCountSet"`
	Likes                        *bool      `json:"likes,omitempty"`
	LikesSet                     bool       `json:"likesSet"`
	LastPlayedDate               *time.Time `json:"lastPlayedDate,omitempty"`
	LastPlayedDateSubmicrosecond *int       `json:"lastPlayedDateSubmicrosecond,omitempty"`
	LastPlayedDateSet            bool       `json:"lastPlayedDateSet"`
	UpdatedAt                    time.Time  `json:"updatedAt"`
}

type TrackingPreference struct {
	Provider     string `json:"provider"`
	SyncWatched  bool   `json:"syncWatched"`
	SyncProgress bool   `json:"syncProgress"`
	SyncLibrary  bool   `json:"syncLibrary"`
}

type SectionReport struct {
	Section   string `json:"section"`
	Created   int    `json:"created"`
	Updated   int    `json:"updated"`
	Unchanged int    `json:"unchanged"`
}

type ImportReport struct {
	Mode             string          `json:"mode"`
	ProfileID        string          `json:"profileId"`
	Sections         []SectionReport `json:"sections"`
	TrackingAccounts int             `json:"trackingAccountsUpdated"`
}
