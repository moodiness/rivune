package jellyfin

import (
	"context"
	"net/http"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

// Authentication authenticates compat credentials and resolves their bound profile.
type Authentication interface {
	Login(context.Context, CompatLoginInput) (LoginResult, error)
	Authenticate(context.Context, string) (AuthenticatedSession, error)
	Logout(context.Context, AuthenticatedSession) error
}

// CatalogReader is the profile-authorized, read-only catalog boundary used by
// compatibility handlers. It deliberately returns domain models, not /api/v1
// transport DTOs.
type CatalogReader interface {
	GetCatalogTitle(context.Context, auth.Principal, string) (watchstate.CatalogTitle, error)
	ListCatalogItems(context.Context, auth.Principal, watchstate.CatalogQuery) (watchstate.CatalogPage, error)
}

type catalogBatchReader interface {
	GetCatalogTitles(context.Context, auth.Principal, []string) ([]watchstate.CatalogTitle, error)
}

type catalogSearcher interface {
	SearchCatalog(context.Context, auth.Principal, CatalogSearchQuery) (CatalogSearchPage, error)
}

// ArtworkDelivery serves only an already authorized registered artwork key.
type ArtworkDelivery interface {
	LookupKey(context.Context, string) (string, bool)
	ServeKey(http.ResponseWriter, *http.Request, string)
}

// PlaybackDelivery keeps native delivery handles opaque to the compatibility
// protocol and serves bytes without an internal HTTP request.
type PlaybackDelivery interface {
	Sources(context.Context, auth.Principal, playback.SourcesInput) (playback.SourceList, error)
	Open(context.Context, auth.Principal, playback.ResolveInput) (playback.Delivery, error)
	Serve(http.ResponseWriter, *http.Request, playback.DeliveryHandle) error
	Close(context.Context, auth.Principal, playback.DeliveryHandle) error
}
