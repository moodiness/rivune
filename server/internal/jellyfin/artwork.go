package jellyfin

import (
	"net/http"
	"net/url"
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
	if imageType != "Primary" && imageType != "Backdrop" && imageType != "Thumb" {
		http.NotFound(response, request)
		return
	}
	if indexed && request.PathValue("index") != "0" {
		http.NotFound(response, request)
		return
	}
	if !compatCredentialTransportPresent(request) {
		if key, ok := artworkTagKey(request); ok {
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
	if key, valid := artworkTagKey(request); valid {
		handler.artwork.ServeKey(response, request, key)
		return
	}

	materialized := ""
	item, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, request.PathValue("id"))
	if err == nil && supportedArtworkMediaType(item.MediaType) && strings.EqualFold(item.ID, request.PathValue("id")) {
		materialized = item.PosterURL
		if imageType == "Backdrop" {
			materialized = item.BackgroundURL
		}
	} else if handler.collections != nil {
		value, collectionErr := handler.collections.Get(request.Context(), session.Principal, request.PathValue("id"))
		if collectionErr == nil {
			item := handler.collectionViewDTO(request.Context(), session.Principal, value)
			if key := collectionItemArtworkKey(item, imageType); key != "" {
				handler.artwork.ServeKey(response, request, key)
				return
			}
		}
		value, folder, folderErr := handler.findCollectionFolder(request.Context(), session.Principal, request.PathValue("id"))
		if folderErr == nil {
			item := handler.collectionFolderDetailDTO(request.Context(), session.Principal, value, folder)
			if key := collectionItemArtworkKey(item, imageType); key != "" {
				handler.artwork.ServeKey(response, request, key)
				return
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

func collectionItemArtworkKey(item BaseItemDto, imageType string) string {
	switch imageType {
	case "Primary":
		return item.ImageTags["Primary"]
	case "Thumb":
		if tag := item.ImageTags["Thumb"]; tag != "" {
			return tag
		}
		return item.ImageTags["Primary"]
	case "Backdrop":
		if len(item.BackdropImageTags) != 0 {
			return item.BackdropImageTags[0]
		}
	}
	return ""
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
	if len(request.Header.Values("Authorization")) != 0 {
		return true
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return true
	}
	for name := range query {
		if strings.EqualFold(name, "api_key") || strings.EqualFold(name, "ApiKey") {
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
	_, tokenFound := parameters["token"]
	return tokenFound
}

func artworkTagKey(request *http.Request) (string, bool) {
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
