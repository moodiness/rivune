package savedsearch

import (
	"errors"
	"time"

	"github.com/moodiness/rivune/server/internal/watchstate"
)

var (
	ErrProfileRequired = errors.New("active profile required")
	ErrInvalidInput    = errors.New("invalid saved search input")
	ErrNotFound        = errors.New("saved search or smart collection not found")
	ErrConflict        = errors.New("saved search or smart collection revision conflict")
	ErrForbidden       = errors.New("saved search operation forbidden")
)

type SavedSearch struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Query     string    `json:"query"`
	MediaType string    `json:"mediaType,omitempty"`
	Sort      string    `json:"sort"`
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type SavedSearchInput struct {
	Name             string `json:"name"`
	Query            string `json:"query"`
	MediaType        string `json:"mediaType,omitempty"`
	Sort             string `json:"sort"`
	ExpectedRevision int64  `json:"expectedRevision,omitempty"`
}

type Rule struct {
	Type     string   `json:"type"`
	Operator string   `json:"operator,omitempty"`
	Value    string   `json:"value,omitempty"`
	Values   []string `json:"values,omitempty"`
	Number   *float64 `json:"number,omitempty"`
	Rules    []Rule   `json:"rules,omitempty"`
}

type SmartCollection struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Rules     Rule      `json:"rules"`
	Sort      string    `json:"sort"`
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type SmartCollectionInput struct {
	Name             string `json:"name"`
	Rules            Rule   `json:"rules"`
	Sort             string `json:"sort"`
	ExpectedRevision int64  `json:"expectedRevision,omitempty"`
}

type SmartCollectionPage struct {
	Items      []watchstate.CatalogTitle `json:"items"`
	Page       int                       `json:"page"`
	PageSize   int                       `json:"pageSize"`
	Total      int                       `json:"total"`
	TotalPages int                       `json:"totalPages"`
}
