package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/medianotification"
)

const mediaNotificationRequestMaximumBytes = 4 << 10

type mediaNotificationSubscriptionsResponse struct {
	Subscriptions []medianotification.Subscription `json:"subscriptions"`
}

func (a *API) mediaNotificationSubscriptions(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	subscriptions, err := a.mediaNotifications.ListSubscriptions(r.Context(), principal)
	if writeMediaNotificationError(a, w, err, "list media notification subscriptions") { return }
	writeJSON(w, http.StatusOK, mediaNotificationSubscriptionsResponse{Subscriptions: subscriptions})
}

func (a *API) followMediaNotifications(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) { return }
	var input medianotification.FollowInput
	if err := decodeJSONLimit(w, r, &input, mediaNotificationRequestMaximumBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "The notification subscription must be one bounded JSON object")
		return
	}
	subscription, err := a.mediaNotifications.Follow(r.Context(), principal, r.PathValue("titleId"), input)
	if writeMediaNotificationError(a, w, err, "follow media notifications") { return }
	writeJSON(w, http.StatusOK, subscription)
}

func (a *API) unfollowMediaNotifications(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if err := a.mediaNotifications.Unfollow(r.Context(), principal, r.PathValue("titleId")); writeMediaNotificationError(a, w, err, "unfollow media notifications") { return }
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) mediaNotificationInbox(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	limit := 30
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil { writeError(w, http.StatusUnprocessableEntity, "invalid_media_notification", "The notification page limit must be between 1 and 100"); return }
		limit = parsed
	}
	page, err := a.mediaNotifications.List(r.Context(), principal, r.URL.Query().Get("cursor"), limit)
	if writeMediaNotificationError(a, w, err, "list media notifications") { return }
	writeJSON(w, http.StatusOK, page)
}

func (a *API) acknowledgeMediaNotification(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) { return }
	var input medianotification.AcknowledgementInput
	if err := decodeJSONLimit(w, r, &input, mediaNotificationRequestMaximumBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "The notification acknowledgement must be one bounded JSON object")
		return
	}
	if err := a.mediaNotifications.Acknowledge(r.Context(), principal, r.PathValue("notificationId"), input.State); writeMediaNotificationError(a, w, err, "acknowledge media notification") { return }
	w.WriteHeader(http.StatusNoContent)
}

func writeMediaNotificationError(a *API, w http.ResponseWriter, err error, operation string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, medianotification.ErrActiveProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select an active profile before accessing notifications")
	case errors.Is(err, medianotification.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "The active profile cannot be accessed")
	case errors.Is(err, medianotification.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_media_notification", "The notification request is invalid or exceeds a field limit")
	case errors.Is(err, medianotification.ErrNotFound):
		writeError(w, http.StatusNotFound, "media_notification_not_found", "The notification or followed title does not exist")
	case errors.Is(err, medianotification.ErrCapacity):
		writeError(w, http.StatusConflict, "media_notification_capacity", "The profile reached a bounded media notification capacity")
	default:
		a.internalError(w, operation, err)
	}
	return true
}
