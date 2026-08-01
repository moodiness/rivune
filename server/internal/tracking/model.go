package tracking

import (
	"errors"
	"time"
)

var (
	ErrInvalidInput        = errors.New("invalid tracking integration input")
	ErrForbidden           = errors.New("tracking integration forbidden")
	ErrNotConfigured       = errors.New("tracking provider is not configured")
	ErrNotConnected        = errors.New("tracking provider is not connected")
	ErrAuthorizationGone   = errors.New("tracking authorization expired or not found")
	ErrAuthorizationWait   = errors.New("tracking authorization is pending")
	ErrAuthorizationSlow   = errors.New("tracking authorization polling too quickly")
	ErrAuthorizationDenied = errors.New("tracking authorization was denied")
	ErrProviderUnavailable = errors.New("tracking provider unavailable")
)

type Status struct {
	Provider      string     `json:"provider"`
	Configured    bool       `json:"configured"`
	Connected     bool       `json:"connected"`
	SyncWatched   bool       `json:"syncWatched"`
	SyncProgress  bool       `json:"syncProgress"`
	SyncLibrary   bool       `json:"syncLibrary"`
	ConnectedAt   *time.Time `json:"connectedAt,omitempty"`
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
	LastError     string     `json:"lastError,omitempty"`
	PendingItems  int        `json:"pendingItems"`
}

type DeviceAuthorization struct {
	ID              string    `json:"id"`
	Provider        string    `json:"provider"`
	UserCode        string    `json:"userCode"`
	VerificationURL string    `json:"verificationUrl"`
	ExpiresAt       time.Time `json:"expiresAt"`
	IntervalSeconds int       `json:"intervalSeconds"`
}

type PreferencesInput struct {
	SyncWatched  *bool `json:"syncWatched"`
	SyncProgress *bool `json:"syncProgress"`
	SyncLibrary  *bool `json:"syncLibrary"`
}

type Event struct {
	Type            string    `json:"-"`
	TitleID         string    `json:"-"`
	Completed       bool      `json:"completed,omitempty"`
	InLibrary       bool      `json:"inLibrary,omitempty"`
	Cleared         bool      `json:"cleared,omitempty"`
	PositionSeconds int       `json:"positionSeconds,omitempty"`
	DurationSeconds int       `json:"durationSeconds,omitempty"`
	Version         int64     `json:"version,omitempty"`
	OccurredAt      time.Time `json:"occurredAt"`
}
