package jellyfin

import (
	"net/http"
	"strings"
)

func (handler *Handler) handleImage(response http.ResponseWriter, request *http.Request) {
	handler.serveImage(response, request, false)
}

func (handler *Handler) handleIndexedImage(response http.ResponseWriter, request *http.Request) {
	handler.serveImage(response, request, true)
}

func (handler *Handler) serveImage(response http.ResponseWriter, request *http.Request, indexed bool) {
	if handler == nil || handler.authentication == nil || handler.catalog == nil || handler.artwork == nil {
		http.NotFound(response, request)
		return
	}
	session, ok := handler.authenticateRequest(response, request, true)
	if !ok {
		return
	}
	if session.ProfileID == "" || session.Principal.ActiveProfileID == nil ||
		!strings.EqualFold(session.ProfileID, *session.Principal.ActiveProfileID) {
		http.NotFound(response, request)
		return
	}
	imageType := request.PathValue("type")
	if imageType != "Primary" && imageType != "Backdrop" {
		http.NotFound(response, request)
		return
	}
	if indexed && request.PathValue("index") != "0" {
		http.NotFound(response, request)
		return
	}

	item, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, request.PathValue("id"))
	if err != nil || !supportedArtworkMediaType(item.MediaType) || !strings.EqualFold(item.ID, request.PathValue("id")) {
		http.NotFound(response, request)
		return
	}
	materialized := item.PosterURL
	if imageType == "Backdrop" {
		materialized = item.BackgroundURL
	}
	key, ok := handler.artwork.LookupKey(request.Context(), materialized)
	if !ok {
		http.NotFound(response, request)
		return
	}
	handler.artwork.ServeKey(response, request, key)
}

func supportedArtworkMediaType(mediaType string) bool {
	switch mediaType {
	case "movie", "series", "season", "episode":
		return true
	default:
		return false
	}
}
