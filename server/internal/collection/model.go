package collection

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrActiveProfileRequired = errors.New("an active profile is required")
	ErrInvalidInput          = errors.New("invalid collection input")
	ErrNotFound              = errors.New("collection not found")
	ErrConflict              = errors.New("collection changed on another device")
	ErrForbidden             = errors.New("collection operation forbidden")
	ErrProviderUnavailable   = errors.New("collection source provider unavailable")
)

const (
	ViewModeTabbedGrid   = "tabbed_grid"
	ViewModeRows         = "rows"
	ViewModeFollowLayout = "follow_layout"

	TileShapePoster      = "poster"
	TileShapeLandscape   = "landscape"
	TileShapeSquare      = "square"
	SourceViewMerged     = "merged"
	SourceViewCategories = "categories"
	SourceViewFolders    = "folders"

	SourceKindAddonCatalog = "addon_catalog"
	SourceKindTMDB         = "tmdb"
	SourceKindTrakt        = "trakt"
	SourceKindMDBList      = "mdblist"

	MediaTypeMovie  = "movie"
	MediaTypeSeries = "series"
	MediaTypeTV     = "tv"
	MediaTypeBoth   = "both"
)

type Collection struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	BackdropImageURL string    `json:"backdropImageUrl,omitempty"`
	HeroEnabled      bool      `json:"heroEnabled"`
	PinToTop         bool      `json:"pinToTop"`
	FocusGlowEnabled bool      `json:"focusGlowEnabled"`
	ViewMode         string    `json:"viewMode"`
	FolderCoverShape string    `json:"folderCoverShape"`
	Folders          []Folder  `json:"folders"`
	ProfileIDs       []string  `json:"profileIds"`
	Position         int       `json:"position"`
	Version          int       `json:"version"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type Folder struct {
	ID              string   `json:"id,omitempty"`
	Title           string   `json:"title"`
	TileShape       string   `json:"tileShape"`
	SourceView      string   `json:"sourceView"`
	CoverImageURL   string   `json:"coverImageUrl,omitempty"`
	CoverEmoji      string   `json:"coverEmoji,omitempty"`
	TitleLogoURL    string   `json:"titleLogoUrl,omitempty"`
	HeroBackdropURL string   `json:"heroBackdropUrl,omitempty"`
	HeroVideoURL    string   `json:"heroVideoUrl,omitempty"`
	FocusGIFURL     string   `json:"focusGifUrl,omitempty"`
	FocusGIFEnabled bool     `json:"focusGifEnabled"`
	HideTitle       bool     `json:"hideTitle"`
	Sources         []Source `json:"sources"`
}

type Source struct {
	ID           string              `json:"id,omitempty"`
	Kind         string              `json:"kind"`
	Title        string              `json:"title"`
	AddonCatalog *AddonCatalogSource `json:"addonCatalog,omitempty"`
	TMDB         *TMDBSource         `json:"tmdb,omitempty"`
	Trakt        *TraktSource        `json:"trakt,omitempty"`
	MDBList      *MDBListSource      `json:"mdblist,omitempty"`
}

type AddonCatalogSource struct {
	AddonID    string       `json:"addonId,omitempty"`
	ManifestID string       `json:"manifestId,omitempty"`
	Type       string       `json:"type"`
	CatalogID  string       `json:"catalogId"`
	Extra      []ExtraValue `json:"extra,omitempty"`
}

type ExtraValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type TMDBSource struct {
	SourceType string      `json:"sourceType"`
	TMDBID     *int64      `json:"tmdbId,omitempty"`
	MediaType  string      `json:"mediaType"`
	Sort       string      `json:"sort"`
	Filters    TMDBFilters `json:"filters"`
}

type TMDBFilters struct {
	Genres           []int64  `json:"genres,omitempty"`
	ReleaseDateFrom  string   `json:"releaseDateFrom,omitempty"`
	ReleaseDateTo    string   `json:"releaseDateTo,omitempty"`
	VoteAverageMin   *float64 `json:"voteAverageMin,omitempty"`
	VoteAverageMax   *float64 `json:"voteAverageMax,omitempty"`
	VoteCountMin     *int     `json:"voteCountMin,omitempty"`
	OriginalLanguage string   `json:"originalLanguage,omitempty"`
	OriginCountry    string   `json:"originCountry,omitempty"`
	Keywords         []int64  `json:"keywords,omitempty"`
	Companies        []int64  `json:"companies,omitempty"`
	Networks         []int64  `json:"networks,omitempty"`
	Year             *int     `json:"year,omitempty"`
	WatchRegion      string   `json:"watchRegion,omitempty"`
	WatchProviders   []int64  `json:"watchProviders,omitempty"`
}

type TraktSource struct {
	ListID    int64  `json:"listId"`
	MediaType string `json:"mediaType"`
	SortBy    string `json:"sortBy"`
	SortHow   string `json:"sortHow"`
}

type MDBListSource struct {
	ListID    int64  `json:"listId"`
	MediaType string `json:"mediaType"`
	Sort      string `json:"sort"`
	Order     string `json:"order"`
}

type SaveInput struct {
	Title            string   `json:"title"`
	BackdropImageURL string   `json:"backdropImageUrl,omitempty"`
	HeroEnabled      bool     `json:"heroEnabled"`
	PinToTop         bool     `json:"pinToTop"`
	FocusGlowEnabled bool     `json:"focusGlowEnabled"`
	ViewMode         string   `json:"viewMode"`
	FolderCoverShape string   `json:"folderCoverShape"`
	Folders          []Folder `json:"folders"`
	ProfileIDs       []string `json:"profileIds,omitempty"`
	ExpectedVersion  int      `json:"expectedVersion,omitempty"`
}

type ReorderInput struct {
	CollectionIDs []string `json:"collectionIds"`
}

const ExportSchemaVersion = 1

type ExportDocument struct {
	SchemaVersion int         `json:"schemaVersion"`
	ExportedAt    time.Time   `json:"exportedAt,omitempty"`
	Collections   []SaveInput `json:"collections"`
}

type ImportResult struct {
	Imported    int          `json:"imported"`
	Collections []Collection `json:"collections"`
}

type SourceReference struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	AddonID    string `json:"addonId,omitempty"`
	ManifestID string `json:"manifestId,omitempty"`
	CatalogID  string `json:"catalogId,omitempty"`
}

type Item struct {
	ID             string            `json:"id"`
	MediaType      string            `json:"mediaType"`
	Title          string            `json:"title"`
	PosterURL      string            `json:"posterUrl,omitempty"`
	BackgroundURL  string            `json:"backgroundUrl,omitempty"`
	LogoURL        string            `json:"logoUrl,omitempty"`
	Description    string            `json:"description,omitempty"`
	ReleaseInfo    string            `json:"releaseInfo,omitempty"`
	Released       string            `json:"released,omitempty"`
	VoteAverage    *float64          `json:"voteAverage,omitempty"`
	VoteCount      *int              `json:"voteCount,omitempty"`
	Popularity     *float64          `json:"popularity,omitempty"`
	ExternalIDs    map[string]string `json:"externalIds"`
	Sources        []SourceReference `json:"sources"`
	Raw            json.RawMessage   `json:"raw,omitempty"`
	FanartResolved bool              `json:"-"`
}

type SourcePage struct {
	Items           []Item
	Page            int
	HasMore         bool
	CoverImageURL   string
	HeroBackdropURL string
}

type SourceFailure struct {
	SourceID string `json:"sourceId"`
	Kind     string `json:"kind"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type ResolvedFolder struct {
	CollectionID     string            `json:"collectionId"`
	Folder           Folder            `json:"folder"`
	Items            []Item            `json:"items"`
	SourcePosterURLs map[string]string `json:"sourcePosterUrls,omitempty"`
	Page             int               `json:"page"`
	HasMore          bool              `json:"hasMore"`
	Errors           []SourceFailure   `json:"errors"`
}

type LookupResult struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	ImageURL string `json:"imageUrl,omitempty"`
}

type Genre struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}
