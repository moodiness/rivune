package jellyfin

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (handler *Handler) handleImage(response http.ResponseWriter, request *http.Request) {
	handler.serveImage(response, request, false)
}

func (handler *Handler) handleIndexedImage(response http.ResponseWriter, request *http.Request) {
	handler.serveImage(response, request, true)
}

func (handler *Handler) handleUserImage(response http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.authentication == nil || request == nil {
		http.NotFound(response, request)
		return
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || len(request.URL.RawQuery) > maximumCompatRawQueryBytes || validateQueryBudget(query) != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The user image query is invalid")
		return
	}
	if userID, found, scalarErr := queryScalar(query, "UserId"); scalarErr != nil || found && !validCompatUUID(strings.TrimSpace(userID)) {
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The user image query is invalid")
		return
	}
	session, ok := handler.authenticateRequest(response, request, true)
	if !ok {
		return
	}
	if userID := request.PathValue("userId"); userID != "" && !handler.requireBoundUser(response, userID, session) {
		return
	}

	body, etag := handler.profileAvatar(session.ProfileID)
	response.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	response.Header().Set("Content-Length", strconv.Itoa(len(body)))
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Cache-Control", "private, no-cache")
	response.Header().Set("ETag", etag)
	if avatarETagMatches(request.Header.Values("If-None-Match"), etag) {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	response.WriteHeader(http.StatusOK)
	if request.Method == http.MethodGet {
		_, _ = response.Write(body)
	}
}

func (handler *Handler) profileAvatar(profileID string) ([]byte, string) {
	profile, _ := parseUUID(profileID)
	var material [48]byte
	copy(material[:16], "rivune-avatar-v1")
	copy(material[16:32], handler.serverInfo.ID.value[:])
	copy(material[32:], profile[:])
	digest := sha256.Sum256(material[:])
	body := []byte(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="96" height="96" viewBox="0 0 96 96"><rect width="96" height="96" rx="18" fill="#%02x%02x%02x"/><circle cx="48" cy="34" r="17" fill="#%02x%02x%02x"/><path d="M17 88c2-22 14-33 31-33s29 11 31 33" fill="#%02x%02x%02x"/></svg>`,
		32+digest[0]&127, 32+digest[1]&127, 32+digest[2]&127,
		160+digest[3]&95, 160+digest[4]&95, 160+digest[5]&95,
		160+digest[6]&95, 160+digest[7]&95, 160+digest[8]&95,
	))
	bodyDigest := sha256.Sum256(body)
	return body, fmt.Sprintf(`"%x"`, bodyDigest)
}

func avatarETagMatches(values []string, etag string) bool {
	total := 0
	for _, value := range values {
		total += len(value)
		if total > maximumCompatAuthorizationHeaderBytes {
			return false
		}
		for _, candidate := range strings.Split(value, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "*" || candidate == etag || strings.TrimPrefix(candidate, "W/") == etag {
				return true
			}
		}
	}
	return false
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
		return item.ImageTags["Primary"]
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
