package httpapi

import (
	"errors"
	"net/http"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/operations"
)

func (a *API) operationsOverview(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireOperationsAdministrator(w, principal) {
		return
	}
	overview, err := a.operations.Overview(r.Context(), principal)
	if writeOperationsError(a, w, err, "read operations overview") {
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (a *API) updateMetadataRefreshSchedule(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireOperationsAdministrator(w, principal) {
		return
	}
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		Enabled       *bool   `json:"enabled"`
		IntervalHours *int    `json:"intervalHours"`
		Language      *string `json:"language"`
		BatchSize     *int    `json:"batchSize"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.Enabled == nil || request.IntervalHours == nil || request.Language == nil || request.BatchSize == nil {
		writeError(w, http.StatusBadRequest, "invalid_operation_schedule", "enabled, intervalHours, language, and batchSize are required")
		return
	}
	schedule, err := a.operations.UpdateMetadataRefreshSchedule(r.Context(), principal, operations.MetadataRefreshScheduleInput{
		Enabled: *request.Enabled, IntervalHours: *request.IntervalHours,
		Language: *request.Language, BatchSize: *request.BatchSize,
	})
	if writeOperationsError(a, w, err, "update metadata refresh schedule") {
		return
	}
	writeJSON(w, http.StatusOK, schedule)
}

func (a *API) runOperationAction(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireOperationsAdministrator(w, principal) {
		return
	}
	run, err := a.operations.RunAction(r.Context(), principal, operations.OperationAction(r.PathValue("action")))
	if writeOperationsError(a, w, err, "run operation action") {
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func requireOperationsAdministrator(w http.ResponseWriter, principal auth.Principal) bool {
	if principal.Role == "admin" {
		return true
	}
	writeError(w, http.StatusForbidden, "admin_required", "An administrator account is required")
	return false
}

func writeOperationsError(a *API, w http.ResponseWriter, err error, operation string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, operations.ErrForbidden):
		writeError(w, http.StatusForbidden, "admin_required", "An administrator account is required")
	case errors.Is(err, operations.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_operation_schedule", "The metadata refresh schedule is invalid")
	case errors.Is(err, operations.ErrActionNotFound):
		writeError(w, http.StatusNotFound, "operation_not_found", "The operation action does not exist")
	case errors.Is(err, operations.ErrInProgress):
		writeError(w, http.StatusConflict, "operation_in_progress", "A metadata refresh is already running")
	default:
		a.internalError(w, operation, err)
	}
	return true
}
