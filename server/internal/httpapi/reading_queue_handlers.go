package httpapi

import (
	"errors"
	"net/http"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/readingqueue"
)

const readingQueueRequestMaximumBytes = 8 << 10

func (a *API) readingQueueItems(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	queue, err := a.readingQueue.Queue(r.Context(), principal, r.PathValue("profileId"))
	if writeReadingQueueError(a, w, err, "list reading queue") {
		return
	}
	writeJSON(w, http.StatusOK, queue)
}

func (a *API) addReadingQueueItem(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	var input readingqueue.AddInput
	if !decodeReadingQueueInput(w, r, &input) {
		return
	}
	mutation, err := a.readingQueue.Add(r.Context(), principal, r.PathValue("profileId"), input)
	if writeReadingQueueError(a, w, err, "add reading queue item") {
		return
	}
	writeJSON(w, http.StatusOK, mutation)
}

func (a *API) updateReadingQueueItem(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	var input readingqueue.UpdateInput
	if !decodeReadingQueueInput(w, r, &input) {
		return
	}
	mutation, err := a.readingQueue.Update(r.Context(), principal, r.PathValue("profileId"), r.PathValue("itemId"), input)
	if writeReadingQueueError(a, w, err, "update reading queue item") {
		return
	}
	writeJSON(w, http.StatusOK, mutation)
}

func (a *API) reorderReadingQueue(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	var input readingqueue.ReorderInput
	if !decodeReadingQueueInput(w, r, &input) {
		return
	}
	mutation, err := a.readingQueue.Reorder(r.Context(), principal, r.PathValue("profileId"), input)
	if writeReadingQueueError(a, w, err, "reorder reading queue") {
		return
	}
	writeJSON(w, http.StatusOK, mutation)
}

func (a *API) removeReadingQueueItem(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	var input readingqueue.MutationInput
	if !decodeReadingQueueInput(w, r, &input) {
		return
	}
	mutation, err := a.readingQueue.Remove(r.Context(), principal, r.PathValue("profileId"), r.PathValue("itemId"), input)
	if writeReadingQueueError(a, w, err, "remove reading queue item") {
		return
	}
	writeJSON(w, http.StatusOK, mutation)
}

func (a *API) consumeReadingQueueItem(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	var input readingqueue.MutationInput
	if !decodeReadingQueueInput(w, r, &input) {
		return
	}
	mutation, err := a.readingQueue.Consume(r.Context(), principal, r.PathValue("profileId"), r.PathValue("itemId"), input)
	if writeReadingQueueError(a, w, err, "consume reading queue item") {
		return
	}
	writeJSON(w, http.StatusOK, mutation)
}

func decodeReadingQueueInput(w http.ResponseWriter, r *http.Request, destination any) bool {
	if !requireJSON(w, r) {
		return false
	}
	if err := decodeJSONLimit(w, r, destination, readingQueueRequestMaximumBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Reading queue input must be one bounded JSON object")
		return false
	}
	return true
}

func writeReadingQueueError(a *API, w http.ResponseWriter, err error, operation string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, readingqueue.ErrActiveProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select the requested profile before accessing its reading queue")
	case errors.Is(err, readingqueue.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_reading_queue", "The reading queue request is invalid or exceeds a field limit")
	case errors.Is(err, readingqueue.ErrNotFound):
		writeError(w, http.StatusNotFound, "reading_queue_item_not_found", "The reading queue item does not exist")
	case errors.Is(err, readingqueue.ErrOperationConflict):
		writeError(w, http.StatusConflict, "reading_queue_operation_conflict", "The operation identifier was already used for a different reading queue request")
	case errors.Is(err, readingqueue.ErrConflict):
		writeError(w, http.StatusConflict, "reading_queue_conflict", "The reading queue changed on another device; reload it before retrying")
	case errors.Is(err, readingqueue.ErrCapacity):
		writeError(w, http.StatusConflict, "reading_queue_capacity", "The reading queue already contains the maximum of 500 items")
	default:
		a.internalError(w, operation, err)
	}
	return true
}
