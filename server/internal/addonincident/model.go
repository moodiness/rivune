package addonincident

import (
	"errors"
	"time"
)

const (
	CodeTimeout         = "timeout"
	CodeUnavailable     = "unavailable"
	CodeInvalidResponse = "invalid_response"
	CodeUnhealthy       = "unhealthy"

	StateOpen       = "open"
	StateRecovering = "recovering"
	StateResolved   = "resolved"

	ImpactAvailability      = "availability"
	ImpactResponseIntegrity = "response_integrity"
)

var (
	ErrForbidden = errors.New("addon incident access forbidden")
	ErrNotFound  = errors.New("addon incident not found")
	ErrInvalid   = errors.New("invalid addon incident input")
)

type Incident struct {
	ID                   string     `json:"id"`
	ProfileID            string     `json:"profileId"`
	AddonID              string     `json:"addonId"`
	AddonName            string     `json:"addonName"`
	Code                 string     `json:"code"`
	State                string     `json:"state"`
	Impact               string     `json:"impact"`
	OccurrenceCount      int        `json:"occurrenceCount"`
	FirstOccurredAt      time.Time  `json:"firstOccurredAt"`
	LastOccurredAt       time.Time  `json:"lastOccurredAt"`
	LastSuccessAt        *time.Time `json:"lastSuccessAt"`
	RecoveryStartedAt    *time.Time `json:"recoveryStartedAt"`
	ResolvedAt           *time.Time `json:"resolvedAt"`
	AcknowledgedAt       *time.Time `json:"acknowledgedAt"`
	AcknowledgedByUserID *string    `json:"acknowledgedByUserId"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}

type Event struct {
	ID         int64     `json:"id"`
	Type       string    `json:"type"`
	Code       string    `json:"code"`
	OccurredAt time.Time `json:"occurredAt"`
}

type List struct {
	Incidents []Incident `json:"incidents"`
}

type Detail struct {
	Incident Incident `json:"incident"`
	Events   []Event  `json:"events"`
}

func impactFor(code string) string {
	if code == CodeInvalidResponse {
		return ImpactResponseIntegrity
	}
	return ImpactAvailability
}

func validFailureCode(code string) bool {
	switch code {
	case CodeTimeout, CodeUnavailable, CodeInvalidResponse, CodeUnhealthy:
		return true
	default:
		return false
	}
}
