package addon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/moodiness/rivune/server/internal/auth"
)

const MaximumCatalogSearchResults = 200

// CatalogSearchItem is a provider-neutral, sanitized catalog projection. It
// intentionally carries no transport URL, request headers, or provider error.
type CatalogSearchItem struct {
	AddonID       string
	CatalogID     string
	AddonName     string
	ResourceID    string
	MediaType     string
	Title         string
	PosterURL     string
	BackgroundURL string
	ReleaseInfo   string
	Released      string
	ExternalIDs   map[string]string
}

// CatalogSearchPage is a bounded aggregate. Complete is false whenever an
// exact total is unavailable, including every provider payload without totals.
type CatalogSearchPage struct {
	Items    []CatalogSearchItem
	Complete bool
}

// CatalogSearchArtworkPresenter localizes provider artwork before payload sanitization.
type CatalogSearchArtworkPresenter interface {
	PresentAddonResources(context.Context, []ResourceResult)
}

// SearchCatalogItems searches enabled, active-profile-accessible searchable
// catalogs and decodes only the fields needed by protocol adapters.
func (service *Service) SearchCatalogItems(ctx context.Context, principal auth.Principal, contentTypes []string, query string, limit int, artwork CatalogSearchArtworkPresenter) (CatalogSearchPage, error) {
	query = strings.TrimSpace(query)
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) < 1 || utf8.RuneCountInString(query) > 200 {
		return CatalogSearchPage{}, fmt.Errorf("%w: search must contain 1 to 200 characters", ErrInvalidInput)
	}
	if limit < 1 || limit > MaximumCatalogSearchResults {
		return CatalogSearchPage{}, fmt.Errorf("%w: search limit must be between 1 and %d", ErrInvalidInput, MaximumCatalogSearchResults)
	}
	seenTypes := make(map[string]struct{}, len(contentTypes))
	page := CatalogSearchPage{Items: make([]CatalogSearchItem, 0, limit), Complete: true}
	perCatalogLimit := limit
	if perCatalogLimit > 100 {
		perCatalogLimit = 100
	}
	for _, rawType := range contentTypes {
		contentType := strings.ToLower(strings.TrimSpace(rawType))
		if contentType != "movie" && contentType != "series" {
			return CatalogSearchPage{}, fmt.Errorf("%w: unsupported catalog search type", ErrInvalidInput)
		}
		if _, duplicate := seenTypes[contentType]; duplicate {
			continue
		}
		seenTypes[contentType] = struct{}{}
		batch, err := service.SearchCatalogs(ctx, principal, contentType, CatalogSearchInput{Search: query, Limit: perCatalogLimit})
		if err != nil {
			return CatalogSearchPage{}, err
		}
		if artwork != nil {
			artwork.PresentAddonResources(ctx, batch.Results)
		}
		if len(batch.Results) != 0 {
			// Add-on catalog payloads do not carry an exact total. Even a short
			// page is not proof of exhaustion because providers may ignore limit.
			page.Complete = false
		}
		if len(batch.Errors) != 0 {
			page.Complete = false
		}
		for _, result := range batch.Results {
			items, complete, err := decodeCatalogSearchResult(ctx, result, perCatalogLimit)
			if err != nil {
				page.Complete = false
				continue
			}
			if !complete {
				page.Complete = false
			}
			for _, item := range items {
				if len(page.Items) == limit {
					page.Complete = false
					return page, nil
				}
				page.Items = append(page.Items, item)
			}
		}
	}
	return page, nil
}

func decodeCatalogSearchResult(ctx context.Context, result ResourceResult, requested int) ([]CatalogSearchItem, bool, error) {
	safe, err := SanitizeExposablePayload(result.Payload)
	if err != nil {
		return nil, false, err
	}
	var envelope struct {
		Metas []json.RawMessage `json:"metas"`
	}
	if err := json.Unmarshal(safe, &envelope); err != nil || envelope.Metas == nil {
		return nil, false, fmt.Errorf("%w: catalog search payload has no metas", ErrInvalidResponse)
	}
	if err := ConsumePayloadItems(ctx, len(envelope.Metas)); err != nil {
		return nil, false, err
	}
	items := make([]CatalogSearchItem, 0, len(envelope.Metas))
	for _, raw := range envelope.Metas {
		item, ok := parseCatalogSearchItem(result, raw)
		if ok {
			items = append(items, item)
		}
	}
	return items, len(envelope.Metas) < requested, nil
}

func parseCatalogSearchItem(result ResourceResult, raw json.RawMessage) (CatalogSearchItem, bool) {
	var value struct {
		ID          string            `json:"id"`
		Type        string            `json:"type"`
		Name        string            `json:"name"`
		Title       string            `json:"title"`
		Poster      string            `json:"poster"`
		Background  string            `json:"background"`
		ReleaseInfo string            `json:"releaseInfo"`
		Released    string            `json:"released"`
		ExternalIDs map[string]string `json:"externalIds"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return CatalogSearchItem{}, false
	}
	value.ID = strings.TrimSpace(value.ID)
	value.Type = strings.ToLower(strings.TrimSpace(value.Type))
	value.Name = strings.TrimSpace(value.Name)
	if value.Name == "" {
		value.Name = strings.TrimSpace(value.Title)
	}
	if !safeCatalogExternalID(value.ID) || value.Name == "" || len(value.Name) > 500 || (value.Type != "movie" && value.Type != "series") {
		return CatalogSearchItem{}, false
	}
	externalIDs := make(map[string]string, len(value.ExternalIDs)+1)
	for provider, id := range value.ExternalIDs {
		provider = strings.ToLower(strings.TrimSpace(provider))
		id = strings.TrimSpace(id)
		if provider != "" && len(provider) <= 32 && safeCatalogExternalID(id) {
			externalIDs[provider] = id
		}
	}
	if strings.HasPrefix(value.ID, "tt") {
		externalIDs["imdb"] = value.ID
	} else if parts := strings.SplitN(value.ID, ":", 2); len(parts) == 2 {
		provider := strings.ToLower(parts[0])
		if id := strings.TrimSpace(parts[1]); (provider == "tmdb" || provider == "tvdb") && safeCatalogExternalID(id) {
			externalIDs[provider] = id
		}
	}
	return CatalogSearchItem{
		AddonID: result.AddonID, CatalogID: result.ID, AddonName: result.AddonName,
		ResourceID: value.ID, MediaType: value.Type, Title: value.Name,
		PosterURL: value.Poster, BackgroundURL: value.Background,
		ReleaseInfo: strings.TrimSpace(value.ReleaseInfo), Released: strings.TrimSpace(value.Released),
		ExternalIDs: externalIDs,
	}, true
}

func safeCatalogExternalID(id string) bool {
	return id != "" && len(id) <= 512 && !strings.Contains(strings.ToLower(id), "://") && !strings.ContainsAny(id, "\r\n\t")
}
