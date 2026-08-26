package readingqueue

import (
	"errors"
	"time"
)

var (
	ErrActiveProfileRequired = errors.New("an active profile is required")
	ErrInvalidInput          = errors.New("invalid reading queue input")
	ErrNotFound              = errors.New("reading queue item not found")
	ErrConflict              = errors.New("reading queue revision conflict")
	ErrCapacity              = errors.New("reading queue capacity reached")
	ErrOperationConflict     = errors.New("reading queue operation id was reused with different input")
)

const MaximumItems = 500

type Item struct {
	ID            string    `json:"id"`
	MediaType     string    `json:"mediaType"`
	ResourceID    string    `json:"resourceId"`
	SourceAddonID string    `json:"sourceAddonId,omitempty"`
	TitleID       string    `json:"titleId,omitempty"`
	Title         string    `json:"title"`
	PosterURL     string    `json:"posterUrl,omitempty"`
	Position      int       `json:"position"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type Queue struct {
	Revision int64  `json:"revision"`
	Items    []Item `json:"items"`
}

type AddInput struct {
	OperationID      string `json:"operationId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	MediaType        string `json:"mediaType"`
	ResourceID       string `json:"resourceId"`
	SourceAddonID    string `json:"sourceAddonId,omitempty"`
	TitleID          string `json:"titleId,omitempty"`
	Title            string `json:"title"`
	PosterURL        string `json:"posterUrl,omitempty"`
}

type UpdateInput struct {
	OperationID      string `json:"operationId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Title            string `json:"title"`
	PosterURL        string `json:"posterUrl,omitempty"`
}

type ReorderInput struct {
	OperationID      string   `json:"operationId"`
	ExpectedRevision int64    `json:"expectedRevision"`
	ItemIDs          []string `json:"itemIds"`
}

type MutationInput struct {
	OperationID      string `json:"operationId"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

type Mutation struct {
	Revision       int64  `json:"revision"`
	AffectedItemID string `json:"affectedItemId,omitempty"`
	Duplicate      bool   `json:"duplicate,omitempty"`
}
