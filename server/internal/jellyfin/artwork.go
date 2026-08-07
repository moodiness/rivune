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
	imageType := request.PathValue("type")
	if imageType != "Primary" && imageType != "Backdrop" {
		http.NotFound(response, request)
		return
	}
	if indexed && request.PathValue("index") != "0" {
		http.NotFound(response, request)
		return
	}
	if !compatCredentialTransportPresent(request) {
		if key, ok := anonymousArtworkKey(request); ok {
			handler.artwork.ServeKey(response, request, key)
			return
		}
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

	materialized := ""
	item, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, request.PathValue("id"))
	if err == nil && supportedArtworkMediaType(item.MediaType) && strings.EqualFold(item.ID, request.PathValue("id")) {
		materialized = item.PosterURL
		if imageType == "Backdrop" {
			materialized = item.BackgroundURL
		}
	} else if imageType == "Primary" && handler.collections != nil {
		_, folder, folderErr := handler.findCollectionFolder(request.Context(), session.Principal, request.PathValue("id"))
		localizer, canLocalize := handler.catalog.(catalogArtworkLocalizer)
		if folderErr == nil && canLocalize {
			localized := localizer.LocalizeArtworkURLs(request.Context(), []string{folder.CoverImageURL})
			if len(localized) == 1 {
				materialized = localized[0]
			}
		}
	}
	key, ok := handler.artwork.LookupKey(request.Context(), materialized)
	if !ok {
		http.NotFound(response, request)
		return
	}
	handler.artwork.ServeKey(response, request, key)
}

func compatCredentialTransportPresent(request *http.Request) bool {
	if request == nil {
		return false
	}
	for _, name := range []string{"X-Emby-Token", "X-MediaBrowser-Token"} {
		if len(request.Header.Values(name)) != 0 {
			return true
		}
	}
	for name := range request.URL.Query() {
		if strings.EqualFold(name, "api_key") {
			return true
		}
	}
	parameters, found, err := collectAuthorizationParameters(request.Header)
	if err != nil {
		return true
	}
	if !found {
		return false
	}
	token, tokenFound := parameters["token"]
	return tokenFound && strings.TrimSpace(token) != ""
}

func anonymousArtworkKey(request *http.Request) (string, bool) {
	if request == nil {
		return "", false
	}
	value, found, err := queryScalar(request.URL.Query(), "tag")
	if err != nil || !found {
		return "", false
	}
	return localizedArtworkTag(localizedArtworkPrefix + strings.TrimSpace(value))
}

func supportedArtworkMediaType(mediaType string) bool {
	switch mediaType {
	case "movie", "series", "season", "episode":
		return true
	default:
		return false
	}
}
