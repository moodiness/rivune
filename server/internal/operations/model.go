package operations

import (
	"errors"
	"time"

	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/playback"
)

var (
	ErrForbidden      = errors.New("operations require an administrator")
	ErrInvalidInput   = errors.New("invalid operations input")
	ErrActionNotFound = errors.New("operation action not found")
	ErrInProgress     = errors.New("operation already in progress")
)

type OperationAction string

const (
	ActionFetchMissingMetadata OperationAction = "fetch-missing-metadata"
	ActionRunHousekeeping      OperationAction = "run-housekeeping"
	ActionClearMetadataCache   OperationAction = "clear-metadata-cache"
	ActionClearStreamCache     OperationAction = "clear-stream-cache"
)

type MetadataRefreshResult = metadata.RefreshResult

type MetadataRefreshScheduleInput struct {
	Enabled       bool   `json:"enabled"`
	IntervalHours int    `json:"intervalHours"`
	Language      string `json:"language"`
	BatchSize     int    `json:"batchSize"`
}

type MetadataRefreshSchedule struct {
	Task            string                 `json:"task"`
	Enabled         bool                   `json:"enabled"`
	IntervalHours   int                    `json:"intervalHours"`
	Language        string                 `json:"language"`
	BatchSize       int                    `json:"batchSize"`
	NextRunAt       *time.Time             `json:"nextRunAt"`
	LastStartedAt   *time.Time             `json:"lastStartedAt"`
	LastCompletedAt *time.Time             `json:"lastCompletedAt"`
	LastStatus      *string                `json:"lastStatus"`
	LastResult      *MetadataRefreshResult `json:"lastResult"`
}

type MetadataCacheStatus struct {
	Entries          int `json:"entries"`
	FreshEntries     int `json:"freshEntries"`
	ExpiredEntries   int `json:"expiredEntries"`
	RootTitles       int `json:"rootTitles"`
	MissingTitles    int `json:"missingTitles"`
	ArtworkSnapshots int `json:"artworkSnapshots"`
}

type OperationsOverview struct {
	MetadataCache               MetadataCacheStatus     `json:"metadataCache"`
	MetadataRefresh             MetadataRefreshSchedule `json:"metadataRefresh"`
	HousekeepingIntervalMinutes int                     `json:"housekeepingIntervalMinutes"`
}

type MetadataCacheClearResult struct {
	EntriesDeleted int `json:"entriesDeleted"`
}

type OperationResult struct {
	Metadata      *MetadataRefreshResult    `json:"metadata,omitempty"`
	MetadataCache *MetadataCacheClearResult `json:"metadataCache,omitempty"`
	Playback      *playback.PurgeResult     `json:"playback,omitempty"`
}

type OperationRun struct {
	Action      OperationAction `json:"action"`
	StartedAt   time.Time       `json:"startedAt"`
	CompletedAt time.Time       `json:"completedAt"`
	Status      string          `json:"status"`
	Result      OperationResult `json:"result"`
}
