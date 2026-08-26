package httpapi

import (
	"errors"
	"net/http"

	"github.com/moodiness/rivune/server/internal/addonincident"
	"github.com/moodiness/rivune/server/internal/auth"
)

func (a *API) listAddonIncidents(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireIncidentManager(w, principal) { return }
	incidents, err := a.addonIncidents.List(r.Context(), principal)
	if writeAddonIncidentError(a, w, err, "list addon incidents") { return }
	writeJSON(w, http.StatusOK, incidents)
}

func (a *API) addonIncidentDetail(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireIncidentManager(w, principal) { return }
	detail, err := a.addonIncidents.Detail(r.Context(), principal, r.PathValue("incidentId"))
	if writeAddonIncidentError(a, w, err, "read addon incident") { return }
	writeJSON(w, http.StatusOK, detail)
}

func (a *API) acknowledgeAddonIncident(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireIncidentManager(w, principal) { return }
	incident, err := a.addonIncidents.Acknowledge(r.Context(), principal, r.PathValue("incidentId"))
	if writeAddonIncidentError(a, w, err, "acknowledge addon incident") { return }
	writeJSON(w, http.StatusOK, incident)
}

func requireIncidentManager(w http.ResponseWriter, principal auth.Principal) bool {
	if principal.ActiveProfileID != nil && principal.ActiveProfileCanManage { return true }
	writeError(w, http.StatusForbidden, "profile_manager_required", "The active profile requires management access")
	return false
}

func writeAddonIncidentError(a *API, w http.ResponseWriter, err error, operation string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, addonincident.ErrForbidden):
		writeError(w, http.StatusForbidden, "profile_manager_required", "The active profile requires management access")
	case errors.Is(err, addonincident.ErrNotFound):
		writeError(w, http.StatusNotFound, "addon_incident_not_found", "The extension incident does not exist")
	case errors.Is(err, addonincident.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_addon_incident", "The extension incident request is invalid")
	default:
		a.internalError(w, operation, err)
	}
	return true
}
