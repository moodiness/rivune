package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/calendar"
)

func (a *API) calendarEvents(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	result, err := a.calendar.List(r.Context(), principal, r.URL.Query().Get("from"), r.URL.Query().Get("to"), r.URL.Query().Get("language"))
	if err != nil {
		switch {
		case errors.Is(err, calendar.ErrInvalidInput):
			writeError(w, http.StatusBadRequest, "invalid_calendar_range", strings.TrimPrefix(err.Error(), calendar.ErrInvalidInput.Error()+": "))
		case errors.Is(err, calendar.ErrProfileRequired):
			writeError(w, http.StatusConflict, "profile_selection_required", "Select an active profile before viewing the calendar")
		default:
			a.internalError(w, "list calendar events", err)
		}
		return
	}
	if a.artwork != nil {
		a.artwork.LocalizeCalendar(r.Context(), &result)
	}
	writeJSON(w, http.StatusOK, result)
}
