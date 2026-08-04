package category

import (
	"errors"
	"time"
)

var (
	ErrInvalidInput         = errors.New("invalid category input")
	ErrForbidden            = errors.New("category operation forbidden")
	ErrNotFound             = errors.New("category resource not found")
	ErrConflict             = errors.New("category name already exists")
	ErrReassignmentRequired = errors.New("category reassignment is required")
	ErrDefaultCategory      = errors.New("default category cannot be deleted")
)

// Actor is the authenticated, server-derived authority used by category mutations.
type Actor struct {
	UserID              string
	GlobalAdministrator bool
}

type CategoryRef struct {
	ID    string
	Name  string
	Color *string
	Icon  *string
}

type Category struct {
	ID           string
	Name         string
	Description  *string
	Color        *string
	Icon         *string
	Position     int
	IsDefault    bool
	ProfileCount int64
	DeviceCount  int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type CreateInput struct {
	Name        string
	Description *string
	Color       *string
	Icon        *string
}

type UpdateInput struct {
	Name           *string
	DescriptionSet bool
	Description    *string
	ColorSet       bool
	Color          *string
	IconSet        bool
	Icon           *string
	MakeDefault    bool
}

type Device struct {
	ID           string
	Name         string
	Platform     string
	Category     CategoryRef
	InternalNote *string
	ApprovedAt   time.Time
	LastSeenAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type DeviceUpdateInput struct {
	Name            *string
	CategoryID      *string
	InternalNoteSet bool
	InternalNote    *string
}
