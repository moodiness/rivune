package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
)

func (a *API) listAddons(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	addons, err := a.addons.List(r.Context(), principal)
	if err != nil {
		a.writeAddonError(w, "list addons", err)
		return
	}
	if a.artwork != nil {
		a.artwork.PresentInstalledAddons(r.Context(), addons)
	}
	writeJSON(w, http.StatusOK, map[string]any{"addons": addons})
}

func (a *API) installAddon(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input addon.InstallInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	installed, err := a.addons.Install(r.Context(), principal, input)
	if err != nil {
		a.writeAddonError(w, "install addon", err)
		return
	}
	if a.artwork != nil {
		values := []addon.InstalledAddon{installed}
		a.artwork.PresentInstalledAddons(r.Context(), values)
		installed = values[0]
	}
	writeJSON(w, http.StatusCreated, installed)
}

func (a *API) removeAddon(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if err := a.addons.Remove(r.Context(), principal, r.PathValue("addonId")); err != nil {
		a.writeAddonError(w, "remove addon", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) reorderAddons(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input addon.ReorderInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	addons, err := a.addons.Reorder(r.Context(), principal, input)
	if err != nil {
		a.writeAddonError(w, "reorder addons", err)
		return
	}
	if a.artwork != nil {
		a.artwork.PresentInstalledAddons(r.Context(), addons)
	}
	writeJSON(w, http.StatusOK, map[string]any{"addons": addons})
}

func (a *API) refreshAddon(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	installed, err := a.addons.Refresh(r.Context(), principal, r.PathValue("addonId"))
	if err != nil {
		a.writeAddonError(w, "refresh addon", err)
		return
	}
	if a.artwork != nil {
		values := []addon.InstalledAddon{installed}
		a.artwork.PresentInstalledAddons(r.Context(), values)
		installed = values[0]
	}
	writeJSON(w, http.StatusOK, installed)
}
func (a *API) updateAddon(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input addon.UpdateAddonInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	installed, err := a.addons.Update(r.Context(), principal, r.PathValue("addonId"), input)
	if err != nil {
		a.writeAddonError(w, "update addon", err)
		return
	}
	if a.artwork != nil {
		values := []addon.InstalledAddon{installed}
		a.artwork.PresentInstalledAddons(r.Context(), values)
		installed = values[0]
	}
	writeJSON(w, http.StatusOK, installed)
}

func (a *API) addonCatalogDescriptors(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	catalogs, err := a.addons.Catalogs(r.Context(), principal)
	if err != nil {
		a.writeAddonError(w, "list addon catalogs", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"catalogs": catalogs})
}

func (a *API) fetchAddonResource(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	path, err := addonResourcePath(r)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_addon_request", err.Error())
		return
	}
	result, err := a.addons.Fetch(r.Context(), principal, r.PathValue("addonId"), path)
	if err != nil {
		a.writeAddonError(w, "fetch addon resource", err)
		return
	}
	if a.artwork != nil {
		values := []addon.ResourceResult{result}
		a.artwork.PresentAddonResources(r.Context(), values)
		result = values[0]
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) fetchAllAddonResources(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	path, err := addonResourcePath(r)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_addon_request", err.Error())
		return
	}
	batch, err := a.addons.FetchAll(r.Context(), principal, path)
	if err != nil {
		a.writeAddonError(w, "fetch addon resources", err)
		return
	}
	if a.artwork != nil {
		a.artwork.PresentAddonResources(r.Context(), batch.Results)
	}
	writeJSON(w, http.StatusOK, batch)
}

func (a *API) searchAddonCatalogs(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	query := r.URL.Query()
	if !query.Has("search") {
		writeError(w, http.StatusUnprocessableEntity, "invalid_addon_request", "search is required")
		return
	}
	skip, err := integerQuery(r, "skip")
	if err != nil || skip < 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_addon_request", "skip must be an integer greater than or equal to 0")
		return
	}
	limit := 24
	if query.Has("limit") {
		limit, err = integerQuery(r, "limit")
		if err != nil || limit < 1 || limit > 100 {
			writeError(w, http.StatusUnprocessableEntity, "invalid_addon_request", "limit must be an integer between 1 and 100")
			return
		}
	}
	extra, err := parseAddonExtras(r.URL.RawQuery, map[string]bool{"search": true, "skip": true, "limit": true})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_addon_request", err.Error())
		return
	}
	batch, err := a.addons.SearchCatalogs(r.Context(), principal, r.PathValue("type"), addon.CatalogSearchInput{
		Search: query.Get("search"),
		Skip:   skip,
		Limit:  limit,
		Extra:  extra,
	})
	if err != nil {
		a.writeAddonError(w, "search addon catalogs", err)
		return
	}
	if a.artwork != nil {
		a.artwork.PresentAddonResources(r.Context(), batch.Results)
	}
	writeJSON(w, http.StatusOK, batch)
}

func (a *API) discoverAddonCatalogs(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	extra, err := parseAddonExtras(r.URL.RawQuery, map[string]bool{"type": true})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_addon_request", err.Error())
		return
	}
	batch, err := a.addons.FetchCatalogs(r.Context(), principal, r.URL.Query().Get("type"), extra, true)
	if err != nil {
		a.writeAddonError(w, "discover addon catalogs", err)
		return
	}
	if a.artwork != nil {
		a.artwork.PresentAddonResources(r.Context(), batch.Results)
	}
	writeJSON(w, http.StatusOK, batch)
}

func addonResourcePath(r *http.Request) (addon.ResourcePath, error) {
	extra, err := parseAddonExtras(r.URL.RawQuery, nil)
	if err != nil {
		return addon.ResourcePath{}, err
	}
	return addon.ResourcePath{
		Resource: r.PathValue("resource"),
		Type:     r.PathValue("type"),
		ID:       r.PathValue("id"),
		Extra:    extra,
	}, nil
}

func parseAddonExtras(rawQuery string, reserved map[string]bool) ([]addon.ExtraValue, error) {
	if rawQuery == "" {
		return nil, nil
	}
	extra := make([]addon.ExtraValue, 0)
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		rawName, rawValue, found := strings.Cut(pair, "=")
		if !found {
			rawValue = ""
		}
		name, err := url.QueryUnescape(rawName)
		if err != nil || name == "" {
			return nil, errors.New("addon extra names must be valid URL encoded strings")
		}
		value, err := url.QueryUnescape(rawValue)
		if err != nil {
			return nil, errors.New("addon extra values must be valid URL encoded strings")
		}
		if reserved[name] {
			continue
		}
		extra = append(extra, addon.ExtraValue{Name: name, Value: value})
	}
	return extra, nil
}

func (a *API) writeAddonError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, addon.ErrInvalidTransportURL):
		writeError(w, http.StatusUnprocessableEntity, "invalid_addon_transport", strings.TrimPrefix(err.Error(), addon.ErrInvalidTransportURL.Error()+": "))
	case errors.Is(err, addon.ErrInvalidManifest):
		writeError(w, http.StatusUnprocessableEntity, "invalid_addon_manifest", strings.TrimPrefix(err.Error(), addon.ErrInvalidManifest.Error()+": "))
	case errors.Is(err, addon.ErrAlreadyInstalled):
		writeError(w, http.StatusConflict, "addon_already_installed", "This configured addon transport is already available to one of the selected profiles")
	case errors.Is(err, addon.ErrActiveProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select an active profile before accessing addons")
	case errors.Is(err, addon.ErrNotFound):
		writeError(w, http.StatusNotFound, "addon_not_found", "The addon is not installed for the active profile")
	case errors.Is(err, addon.ErrUnsupportedResource):
		writeError(w, http.StatusUnprocessableEntity, "addon_resource_unsupported", "The addon manifest does not support this resource, type, ID, or extra combination")
	case errors.Is(err, addon.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_addon_request", strings.TrimPrefix(err.Error(), addon.ErrInvalidInput.Error()+": "))
	case errors.Is(err, addon.ErrInvalidResponse):
		writeError(w, http.StatusBadGateway, "addon_invalid_response", "The addon returned an invalid resource response")
	case errors.Is(err, addon.ErrProviderUnavailable):
		writeError(w, http.StatusBadGateway, "addon_unavailable", "The addon request failed")
	case errors.Is(err, addon.ErrForbidden):
		writeError(w, http.StatusForbidden, "addon_forbidden", "The active profile cannot manage this addon's profile access")
	default:
		a.internalError(w, operation, err)
	}
}
