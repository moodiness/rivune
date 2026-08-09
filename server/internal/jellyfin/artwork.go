package jellyfin

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	artworkdomain "github.com/moodiness/rivune/server/internal/artwork"
	"github.com/moodiness/rivune/server/internal/collection"
)

const compatImageAuthenticationRejectedMessage = "jellyfin compatibility image authentication rejected"

var compatImageTypes = [...]string{"Primary", "Backdrop", "Logo", "Thumb", "Banner", "Art"}

func (handler *Handler) handleImage(response http.ResponseWriter, request *http.Request) {
	handler.serveImage(response, request, false)
}

func (handler *Handler) handleIndexedImage(response http.ResponseWriter, request *http.Request) {
	handler.serveImage(response, request, true)
}

func (handler *Handler) handleImageInfos(response http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.catalog == nil || handler.authentication == nil {
		http.NotFound(response, request)
		return
	}
	if !validateImageInfoQuery(request) {
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The image metadata query is invalid")
		return
	}
	session, ok := handler.authenticateRequest(response, request, true)
	if !ok || !handler.requireOptionalQueryUser(response, request, session) {
		return
	}
	itemID, validID := canonicalCompatUUID(request.PathValue("id"))
	if !validID {
		http.NotFound(response, request)
		return
	}
	item := BaseItemDto{}
	resolved := false
	if views, available := handler.virtualViews(); available {
		for _, view := range views {
			if view.Id == itemID {
				item, resolved = view, true
				break
			}
		}
	}
	if !resolved && handler.collections != nil {
		value, resolveErr := handler.resolveCollectionView(request.Context(), session.Principal, itemID)
		if resolveErr == nil {
			var projectErr error
			item, projectErr = handler.collectionViewDTO(request.Context(), session.Principal, value)
			if projectErr != nil {
				handler.writeCollectionError(response, projectErr)
				return
			}
			resolved = true
		} else if !errors.Is(resolveErr, collection.ErrNotFound) {
			handler.writeCollectionError(response, resolveErr)
			return
		}
		if !resolved {
			value, folder, folderErr := handler.findCollectionFolder(request.Context(), session.Principal, itemID)
			if folderErr == nil {
				item = handler.collectionFolderDetailDTO(request.Context(), session.Principal, value, folder)
				resolved = true
			} else if !errors.Is(folderErr, collection.ErrNotFound) {
				handler.writeCollectionError(response, folderErr)
				return
			}
		}
	}
	if !resolved {
		title, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, itemID)
		if err != nil || title.ID != itemID {
			http.NotFound(response, request)
			return
		}
		if detailReader, available := handler.catalog.(catalogDetailReader); available {
			if enriched, detailErr := detailReader.EnrichCatalogTitle(request.Context(), session.Principal, title); detailErr == nil {
				title = enriched
			} else if request.Context().Err() != nil {
				return
			}
		}
		item = handler.baseItemDTO(title, false)
	}
	type imageTagProjection struct {
		tag      string
		valid    bool
		metadata artworkdomain.ImageMetadata
	}
	checked := make(map[string]imageTagProjection, len(compatImageTypes))
	project := func(candidate string) (string, bool) {
		tag, valid := opaqueArtworkKey(candidate)
		if !valid || handler.artwork == nil {
			return "", false
		}
		if result, found := checked[tag]; found {
			return result.tag, result.valid
		}
		key, registered := handler.artwork.LookupKey(request.Context(), localizedArtworkPrefix+tag)
		result := imageTagProjection{tag: key, valid: registered && key == tag}
		if result.valid {
			if metadataDelivery, available := handler.artwork.(artworkMetadataDelivery); available {
				result.metadata, _ = metadataDelivery.DescribeKey(request.Context(), key)
			}
		}
		checked[tag] = result
		return result.tag, result.valid
	}
	infos := make([]ImageInfo, 0, len(compatImageTypes))
	for _, imageType := range compatImageTypes {
		if imageType == "Backdrop" {
			candidate := ""
			if len(item.BackdropImageTags) != 0 {
				candidate = item.BackdropImageTags[0]
			} else if item.Type == "BoxSet" || item.Type == "CollectionFolder" {
				candidate = item.ImageTags["Primary"]
			}
			if tag, valid := project(candidate); valid {
				info := imageInfoFromProjection(imageType, tag, checked[tag].metadata)
				infos = append(infos, info)
			}
			continue
		}
		candidate := item.ImageTags[imageType]
		if imageType == "Thumb" && candidate == "" {
			candidate = item.ImageTags["Primary"]
		}
		if tag, valid := project(candidate); valid {
			info := imageInfoFromProjection(imageType, tag, checked[tag].metadata)
			infos = append(infos, info)
		}
	}
	handler.writeJSON(response, http.StatusOK, infos)
}

func imageInfoFromProjection(imageType, tag string, metadata artworkdomain.ImageMetadata) ImageInfo {
	info := ImageInfo{ImageType: imageType, ImageIndex: 0, ImageTag: tag}
	if metadata.Width > 0 {
		info.Width = &metadata.Width
	}
	if metadata.Height > 0 {
		info.Height = &metadata.Height
	}
	if metadata.Size > 0 {
		info.Size = &metadata.Size
	}
	return info
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
	if !supportedCompatImageType(imageType) {
		http.NotFound(response, request)
		return
	}
	if indexed && request.PathValue("index") != "0" {
		http.NotFound(response, request)
		return
	}
	userSelector, userSelectorPresent, queryValid := validateImageRequestQuery(request)
	if !queryValid {
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The image query is invalid")
		return
	}

	session, ok := handler.authenticateRequest(response, request, true)
	if !ok {
		handler.logImageAuthenticationRejection(request, imageType, indexed)
		return
	}
	if session.ProfileID == "" || session.Principal.ActiveProfileID == nil ||
		!strings.EqualFold(session.ProfileID, *session.Principal.ActiveProfileID) {
		http.NotFound(response, request)
		return
	}
	if userSelectorPresent && !handler.requireBoundUser(response, userSelector, session) {
		return
	}
	requestedKey, tagProvided := artworkTagKey(request)
	serveResolvedKey := func(key string) {
		if canonical, valid := opaqueArtworkKey(key); !valid || canonical != key || (tagProvided && requestedKey != key) {
			http.NotFound(response, request)
			return
		}
		handler.artwork.ServeKey(response, request, key)
	}

	requestID, validID := canonicalCompatUUID(request.PathValue("id"))
	if !validID {
		http.NotFound(response, request)
		return
	}
	materialized := ""
	item, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, requestID)
	if err == nil && supportedArtworkMediaType(item.MediaType) && item.ID == requestID {
		if detailReader, available := handler.catalog.(catalogDetailReader); available {
			if enriched, detailErr := detailReader.EnrichCatalogTitle(request.Context(), session.Principal, item); detailErr == nil {
				item = enriched
			} else if request.Context().Err() != nil {
				return
			}
		}
		switch imageType {
		case "Primary", "Thumb":
			materialized = item.PosterURL
		case "Backdrop":
			materialized = item.BackgroundURL
		case "Logo":
			materialized = item.LogoURL
		case "Banner":
			materialized = item.BannerURL
		case "Art":
			materialized = item.ArtURL
		}
		if tagProvided {
			if key, localized := localizedArtworkTag(materialized); localized {
				serveResolvedKey(key)
				return
			}
		}
	} else if handler.collections != nil {
		value, collectionErr := handler.resolveCollectionView(request.Context(), session.Principal, requestID)
		if collectionErr == nil {
			realID, realErr := ParseItemID(value.ID)
			if realErr == nil && requestID == realID.String() {
				item := handler.collectionDTO(request.Context(), session.Principal, value)
				if key := collectionItemArtworkKey(item, imageType); key != "" {
					serveResolvedKey(key)
					return
				}
			} else if item, viewErr := handler.collectionViewDTO(request.Context(), session.Principal, value); viewErr == nil {
				if key := collectionItemArtworkKey(item, imageType); key != "" {
					serveResolvedKey(key)
					return
				}
			}
		}
		value, folder, folderErr := handler.findCollectionFolder(request.Context(), session.Principal, requestID)
		if folderErr == nil {
			item := handler.collectionFolderDetailDTO(request.Context(), session.Principal, value, folder)
			if key := collectionItemArtworkKey(item, imageType); key != "" {
				serveResolvedKey(key)
				return
			}
		}
	}
	if materialized == "" {
		http.NotFound(response, request)
		return
	}
	key, ok := handler.artwork.LookupKey(request.Context(), materialized)
	if !ok {
		http.NotFound(response, request)
		return
	}
	serveResolvedKey(key)
}

func collectionItemArtworkKey(item BaseItemDto, imageType string) string {
	if imageType == "Backdrop" {
		if len(item.BackdropImageTags) != 0 {
			return item.BackdropImageTags[0]
		}
		return item.ImageTags["Primary"]
	}
	if tag := item.ImageTags[imageType]; tag != "" {
		return tag
	}
	if imageType == "Thumb" {
		return item.ImageTags["Primary"]
	}
	return ""
}

func supportedCompatImageType(value string) bool {
	for _, imageType := range compatImageTypes {
		if value == imageType {
			return true
		}
	}
	return false
}

func validateImageInfoQuery(request *http.Request) bool {
	if request == nil || request.URL == nil || len(request.URL.RawQuery) > maximumCompatRawQueryBytes {
		return false
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || validateQueryBudget(values) != nil {
		return false
	}
	for name := range values {
		switch strings.ToLower(name) {
		case "api_key", "apikey", "userid":
		default:
			return false
		}
	}
	userID, found, err := queryScalar(values, "UserId")
	return err == nil && (!found || validCompatUUID(strings.TrimSpace(userID)))
}

func validateImageRequestQuery(request *http.Request) (string, bool, bool) {
	if request == nil || request.URL == nil || len(request.URL.RawQuery) > maximumCompatRawQueryBytes {
		return "", false, false
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || validateQueryBudget(values) != nil {
		return "", false, false
	}
	for name := range values {
		switch strings.ToLower(name) {
		case "api_key", "apikey", "tag", "userid", "width", "maxwidth", "maxheight", "fillwidth", "fillheight", "quality":
		default:
			return "", false, false
		}
	}
	for _, name := range []string{"tag", "UserId", "width", "maxWidth", "maxHeight", "fillWidth", "fillHeight", "quality"} {
		if _, _, scalarErr := queryScalar(values, name); scalarErr != nil {
			return "", false, false
		}
	}
	if tag, found, _ := queryScalar(values, "tag"); found {
		if _, valid := opaqueArtworkKey(tag); !valid {
			return "", false, false
		}
	}
	parseDimension := func(name string, maximum int) (int, bool) {
		raw, found, _ := queryScalar(values, name)
		if !found {
			return 0, true
		}
		parsed, parseErr := strconv.ParseInt(raw, 10, 32)
		return int(parsed), parseErr == nil && parsed >= 1 && parsed <= int64(maximum) && strconv.FormatInt(parsed, 10) == raw
	}
	specifications := [...]struct {
		name    string
		maximum int
	}{{"width", 16384}, {"maxWidth", 16384}, {"maxHeight", 16384}, {"fillWidth", 16384}, {"fillHeight", 16384}, {"quality", 100}}
	var dimensions [6]int
	for index, value := range specifications {
		parsed, valid := parseDimension(value.name, value.maximum)
		if !valid {
			return "", false, false
		}
		dimensions[index] = parsed
	}
	width, maxWidth, maxHeight := dimensions[0], dimensions[1], dimensions[2]
	fillWidth, fillHeight := dimensions[3], dimensions[4]
	if width != 0 && (maxWidth != 0 || maxHeight != 0 || fillWidth != 0 || fillHeight != 0) ||
		(fillWidth != 0 || fillHeight != 0) && (maxWidth != 0 || maxHeight != 0) ||
		fillWidth != 0 && fillHeight != 0 && int64(fillWidth)*int64(fillHeight) > 40_000_000 {
		return "", false, false
	}
	userID, userFound, _ := queryScalar(values, "UserId")
	if userFound && !validCompatUUID(strings.TrimSpace(userID)) {
		return "", false, false
	}
	return userID, userFound, true
}

func (handler *Handler) logImageAuthenticationRejection(request *http.Request, imageType string, indexed bool) {
	if handler == nil || handler.logger == nil {
		return
	}
	tagValid := false
	if request != nil && request.URL != nil && len(request.URL.RawQuery) <= maximumCompatRawQueryBytes {
		_, tagValid = artworkTagKey(request)
	}
	handler.logger.LogAttrs(
		context.Background(),
		slog.LevelInfo,
		compatImageAuthenticationRejectedMessage,
		slog.String("image_type", imageType),
		slog.Bool("indexed", indexed),
		slog.Bool("tag_present", compatQueryNamePresent(request, "tag")),
		slog.Bool("tag_valid", tagValid),
		slog.Bool("credential_transport_present", compatCredentialTransportPresent(request)),
	)
}

func compatQueryNamePresent(request *http.Request, expected string) bool {
	if request == nil || request.URL == nil || len(request.URL.RawQuery) > maximumCompatRawQueryBytes {
		return false
	}
	raw := request.URL.RawQuery
	for len(raw) != 0 {
		component := raw
		if separator := strings.IndexByte(raw, '&'); separator >= 0 {
			component, raw = raw[:separator], raw[separator+1:]
		} else {
			raw = ""
		}
		if separator := strings.IndexByte(component, '='); separator >= 0 {
			component = component[:separator]
		}
		name, err := url.QueryUnescape(component)
		if err == nil && strings.EqualFold(name, expected) {
			return true
		}
	}
	return false
}

func compatCredentialTransportPresent(request *http.Request) bool {
	if request == nil {
		return false
	}
	if request.URL == nil || len(request.URL.RawQuery) > maximumCompatRawQueryBytes {
		return true
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
	if request == nil || request.URL == nil || len(request.URL.RawQuery) > maximumCompatRawQueryBytes {
		return "", false
	}
	value, found, err := queryScalar(request.URL.Query(), "tag")
	if err != nil || !found {
		return "", false
	}
	return localizedArtworkTag(localizedArtworkPrefix + strings.TrimSpace(value))
}

func opaqueArtworkKey(value string) (string, bool) {
	return localizedArtworkTag(localizedArtworkPrefix + strings.TrimSpace(value))
}

func supportedArtworkMediaType(mediaType string) bool {
	switch mediaType {
	case "movie", "series", "season", "episode", "video":
		return true
	default:
		return false
	}
}
