package httpapi

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/calendar"
)

const calendarCapacityRetryAfterSeconds = 60

func (a *API) calendarEvents(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	result, err := a.calendar.List(r.Context(), principal, r.URL.Query().Get("from"), r.URL.Query().Get("to"), r.URL.Query().Get("language"))
	if err != nil {
		switch {
		case errors.Is(err, calendar.ErrInvalidInput):
			writeError(w, http.StatusBadRequest, "invalid_calendar_range", strings.TrimPrefix(err.Error(), calendar.ErrInvalidInput.Error()+": "))
		case errors.Is(err, calendar.ErrProfileRequired):
			writeError(w, http.StatusConflict, "profile_selection_required", "Select an active profile before viewing the calendar")
		case errors.Is(err, calendar.ErrCapacity):
			writeCalendarCapacityExceeded(w)
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

type calendarLinkResponse struct {
	Active    bool       `json:"active"`
	URL       string     `json:"url,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	RotatedAt *time.Time `json:"rotatedAt,omitempty"`
}

func (a *API) calendarLinkStatus(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	link, err := a.calendar.LinkStatus(r.Context(), principal, r.PathValue("profileId"))
	if writeCalendarLinkError(a, w, err, "read calendar link status") {
		return
	}
	writeJSON(w, http.StatusOK, newCalendarLinkResponse(link, ""))
}

func (a *API) createCalendarLink(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if a.config.PublicURL == "" {
		writeError(w, http.StatusServiceUnavailable, "public_url_required", "RIVUNE_PUBLIC_URL is required to create a calendar link")
		return
	}
	if !requireEmptyCalendarLinkBody(w, r) {
		return
	}
	credential, err := a.calendar.CreateLink(r.Context(), principal, r.PathValue("profileId"))
	if writeCalendarLinkError(a, w, err, "create calendar link") {
		return
	}
	writeJSON(w, http.StatusCreated, newCalendarLinkResponse(credential.Link, calendarCredentialURL(a.config.PublicURL, credential.Token)))
}

func (a *API) rotateCalendarLink(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if a.config.PublicURL == "" {
		writeError(w, http.StatusServiceUnavailable, "public_url_required", "RIVUNE_PUBLIC_URL is required to rotate a calendar link")
		return
	}
	if !requireEmptyCalendarLinkBody(w, r) {
		return
	}
	credential, err := a.calendar.RotateLink(r.Context(), principal, r.PathValue("profileId"))
	if writeCalendarLinkError(a, w, err, "rotate calendar link") {
		return
	}
	writeJSON(w, http.StatusOK, newCalendarLinkResponse(credential.Link, calendarCredentialURL(a.config.PublicURL, credential.Token)))
}

func (a *API) revokeCalendarLink(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if err := a.calendar.RevokeLink(r.Context(), principal, r.PathValue("profileId")); writeCalendarLinkError(a, w, err, "revoke calendar link") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) calendarFeed(w http.ResponseWriter, r *http.Request) {
	writeCalendarFeedSecurityHeaders(w)
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 1 || len(query["token"]) != 1 {
		writeCalendarFeedNotFound(w)
		return
	}
	release, retryAfter, admitted := a.calendarFeedAdmission.acquire(requestClientIP(r, a.config.TrustedProxies))
	if !admitted {
		writeAdmissionDenied(w, retryAfter)
		return
	}
	defer release()
	payload, err := a.calendar.Feed(r.Context(), query["token"][0], r.Method == http.MethodGet)
	if errors.Is(err, calendar.ErrNotFound) {
		writeCalendarFeedNotFound(w)
		return
	}
	if errors.Is(err, calendar.ErrCapacity) {
		writeCalendarCapacityExceeded(w)
		return
	}
	if err != nil {
		a.internalError(w, "render calendar feed", err)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="rivune-calendar.ics"`)
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(payload)
	}
}

func writeCalendarFeedSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
}

func writeCalendarFeedNotFound(w http.ResponseWriter) {
	writeCalendarFeedSecurityHeaders(w)
	writeError(w, http.StatusNotFound, "calendar_feed_not_found", "The calendar feed does not exist")
}

func writeCalendarCapacityExceeded(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(calendarCapacityRetryAfterSeconds))
	writeError(w, http.StatusServiceUnavailable, "calendar_capacity_exceeded", "The calendar is temporarily too large to render; retry later")
}

func writeCalendarLinkError(a *API, w http.ResponseWriter, err error, operation string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, calendar.ErrNotFound):
		writeError(w, http.StatusNotFound, "calendar_link_not_found", "The calendar link does not exist")
	case errors.Is(err, calendar.ErrLinkExists):
		writeError(w, http.StatusConflict, "calendar_link_exists", "An active calendar link already exists")
	default:
		a.internalError(w, operation, err)
	}
	return true
}

func newCalendarLinkResponse(link calendar.Link, credentialURL string) calendarLinkResponse {
	response := calendarLinkResponse{Active: link.Active, URL: credentialURL}
	if link.Active {
		response.CreatedAt, response.RotatedAt = &link.CreatedAt, &link.RotatedAt
	}
	return response
}

func calendarCredentialURL(publicURL, token string) string {
	return publicURL + "/api/v1/calendar.ics?token=" + url.QueryEscape(token)
}

func requireEmptyCalendarLinkBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil {
		return true
	}
	var byteBuffer [1]byte
	read, err := r.Body.Read(byteBuffer[:])
	if read != 0 || err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", "The request body must be empty")
		return false
	}
	return true
}
