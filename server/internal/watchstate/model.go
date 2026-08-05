package watchstate

import "time"

type ResolveTitleInput struct {
	MediaType       string
	Provider        string
	ExternalID      string
	ResourceID      string
	Title           string
	PosterURL       string
	BackgroundURL   string
	ReleaseInfo     string
	Released        string
	SourceAddonID   string
	SourceCatalogID string
	SourceName      string
	Country         string
	Language        string
	Category        string
}

type TitleReference struct {
	TitleID         string `json:"titleId"`
	MediaType       string `json:"mediaType"`
	Provider        string `json:"provider"`
	ExternalID      string `json:"externalId"`
	ResourceID      string `json:"resourceId"`
	Title           string `json:"title"`
	PosterURL       string `json:"posterUrl,omitempty"`
	BackgroundURL   string `json:"backgroundUrl,omitempty"`
	ReleaseInfo     string `json:"releaseInfo,omitempty"`
	SourceAddonID   string `json:"sourceAddonId,omitempty"`
	SourceCatalogID string `json:"sourceCatalogId,omitempty"`
	SourceName      string `json:"sourceName,omitempty"`
	Country         string `json:"country,omitempty"`
	Language        string `json:"language,omitempty"`
	Category        string `json:"category,omitempty"`
}

type LibraryItem struct {
	TitleID         string    `json:"titleId"`
	MediaType       string    `json:"mediaType"`
	Provider        string    `json:"provider,omitempty"`
	ExternalID      string    `json:"externalId,omitempty"`
	ResourceID      string    `json:"resourceId,omitempty"`
	Title           string    `json:"title,omitempty"`
	PosterURL       string    `json:"posterUrl,omitempty"`
	BackgroundURL   string    `json:"backgroundUrl,omitempty"`
	ReleaseInfo     string    `json:"releaseInfo,omitempty"`
	SourceAddonID   string    `json:"sourceAddonId,omitempty"`
	SourceCatalogID string    `json:"sourceCatalogId,omitempty"`
	SourceName      string    `json:"sourceName,omitempty"`
	Country         string    `json:"country,omitempty"`
	Language        string    `json:"language,omitempty"`
	Category        string    `json:"category,omitempty"`
	Available       bool      `json:"available"`
	AddedAt         time.Time `json:"addedAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type LibraryPage struct {
	Items        []LibraryItem `json:"items"`
	Page         int           `json:"page"`
	TotalPages   int           `json:"totalPages"`
	TotalResults int           `json:"totalResults"`
}

type TVLibraryIdentity struct {
	SourceAddonID string `json:"sourceAddonId"`
	ResourceID    string `json:"resourceId"`
}

type TVLibraryMembership struct {
	SourceAddonID string `json:"sourceAddonId"`
	ResourceID    string `json:"resourceId"`
	TitleID       string `json:"titleId"`
}

type TVLibraryMembershipResult struct {
	Items []TVLibraryMembership `json:"items"`
}

type Progress struct {
	TitleID         string    `json:"titleId"`
	MediaType       string    `json:"mediaType"`
	PositionSeconds int       `json:"positionSeconds"`
	DurationSeconds int       `json:"durationSeconds"`
	Completed       bool      `json:"completed"`
	Version         int64     `json:"version"`
	LastWatchedAt   time.Time `json:"lastWatchedAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type UpdateProgressInput struct {
	PositionSeconds int
	DurationSeconds int
	Completed       bool
	ExpectedVersion int64
}

type CompletionInput struct {
	ExpectedVersion int64
}

const MaximumProgressBatchSize = 100

type ProgressBatchItem struct {
	TitleID  string    `json:"titleId"`
	Progress *Progress `json:"progress"`
}

type ProgressBatch struct {
	Items []ProgressBatchItem `json:"items"`
}

type SetWatchedBatchItem struct {
	TitleID         string `json:"titleId"`
	Completed       bool   `json:"completed"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

type ContinueItem struct {
	TitleID          string    `json:"titleId"`
	MediaType        string    `json:"mediaType"`
	SeriesID         string    `json:"seriesId,omitempty"`
	SeasonID         string    `json:"seasonId,omitempty"`
	SeasonNumber     *int      `json:"seasonNumber,omitempty"`
	EpisodeNumber    *int      `json:"episodeNumber,omitempty"`
	PositionSeconds  int       `json:"positionSeconds"`
	DurationSeconds  int       `json:"durationSeconds"`
	Version          int64     `json:"version"`
	Reason           string    `json:"reason"`
	Title            string    `json:"title,omitempty"`
	PosterURL        string    `json:"posterUrl,omitempty"`
	BackgroundURL    string    `json:"backgroundUrl,omitempty"`
	ReleaseInfo      string    `json:"releaseInfo,omitempty"`
	ResourceID       string    `json:"resourceId,omitempty"`
	ResourceProvider string    `json:"resourceProvider,omitempty"`
	LastWatchedAt    time.Time `json:"lastWatchedAt"`
}

type ContinuePage struct {
	Items []ContinueItem `json:"items"`
}
