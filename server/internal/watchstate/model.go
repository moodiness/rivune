package watchstate

import "time"

type ResolveTitleInput struct {
	MediaType     string
	Provider      string
	ExternalID    string
	ResourceID    string
	Title         string
	PosterURL     string
	BackgroundURL string
	ReleaseInfo   string
}

type TitleReference struct {
	TitleID       string `json:"titleId"`
	MediaType     string `json:"mediaType"`
	Provider      string `json:"provider"`
	ExternalID    string `json:"externalId"`
	ResourceID    string `json:"resourceId"`
	Title         string `json:"title"`
	PosterURL     string `json:"posterUrl,omitempty"`
	BackgroundURL string `json:"backgroundUrl,omitempty"`
	ReleaseInfo   string `json:"releaseInfo,omitempty"`
}

type LibraryItem struct {
	TitleID       string    `json:"titleId"`
	MediaType     string    `json:"mediaType"`
	Provider      string    `json:"provider,omitempty"`
	ExternalID    string    `json:"externalId,omitempty"`
	ResourceID    string    `json:"resourceId,omitempty"`
	Title         string    `json:"title,omitempty"`
	PosterURL     string    `json:"posterUrl,omitempty"`
	BackgroundURL string    `json:"backgroundUrl,omitempty"`
	ReleaseInfo   string    `json:"releaseInfo,omitempty"`
	AddedAt       time.Time `json:"addedAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type LibraryPage struct {
	Items        []LibraryItem `json:"items"`
	Page         int           `json:"page"`
	TotalPages   int           `json:"totalPages"`
	TotalResults int           `json:"totalResults"`
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
